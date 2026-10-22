#!/usr/bin/env python3
"""P20 step 1 (offline): placement bounds per workload from one baseline -report-pgcls run.

usage: place_bound.py <prefix> <cls_pages.csv.gz> <label>  -> JSON line on stdout
  <prefix>          .../stats/metrics_pgcls (needs _readers.csv.gz)
  <cls_pages.csv.gz> per-page classes written by pgcls/scripts/pgcls_page.py

Units: one remote read = one L1V line read served by another GPM (one round trip in
the baseline). Weights are L1V line reads only (per 512 B sub-block, so 1 KB blocks are
pairs); writes are not in the per-sub-block data of these runs.
Routing: Y first, then X; link load = remote read replies (owner -> reader) per link.
Retention (M3+ model, pgcls R_retain): a reader re-reading a line it already read
(same kernel, or an earlier kernel without a write in between) does not go to the
network again: remaining fraction per reader = (reads - rep_intra - rep_cross) / reads.
"""
import csv, gzip, json, sys, collections

W = 7
CENTER = 24
TILES = [t for t in range(W * W) if t != CENTER]
TX = [t % W for t in range(W * W)]
TY = [t // W for t in range(W * W)]
C4 = [17, 23, 25, 31]                  # the four tiles around the centre (CPU) tile
C2 = [(23, 25), (17, 31)]              # k = 2: pairs, alternating by page
REP4 = [8, 12, 36, 40]                 # dm/ hotspot what-if: 4 replicas
RING = [10, 16, 17, 18, 22, 23, 25, 26, 30, 31, 32, 38]  # tiles within 2 hops of the centre


def hops(a, b):
    return abs(TX[a] - TX[b]) + abs(TY[a] - TY[b])


def best_tile(w):
    cx = [0.0] * W
    cy = [0.0] * W
    for t, v in w.items():
        for i in range(W):
            cx[i] += v * abs(TX[t] - i)
            cy[i] += v * abs(TY[t] - i)
    best, bc = None, None
    for t in TILES:
        c = cx[TX[t]] + cy[TY[t]]
        if bc is None or c < bc - 1e-9 or (abs(c - bc) <= 1e-9 and w.get(t, 0) > w.get(best, 0)):
            best, bc = t, c
    return best


def main_user(w, owner):
    m = max(w.values()) if w else 0
    if m == 0 or w.get(owner, 0) == m:
        return owner
    return max(w, key=lambda g: (w[g], -g))


def route(src, dst):
    """links (u, v) of the Y-then-X route src -> dst"""
    x, y = TX[src], TY[src]
    out = []
    while y != TY[dst]:
        ny = y + (1 if TY[dst] > y else -1)
        out.append((x + W * y, x + W * ny))
        y = ny
    while x != TX[dst]:
        nx = x + (1 if TX[dst] > x else -1)
        out.append((x + W * y, nx + W * y))
        x = nx
    return out


ROUTES = {(a, b): route(a, b) for a in range(W * W) for b in range(W * W)}


class Acc:
    """remote traffic matrix (server tile, reader tile) -> reads, plus retained version"""

    def __init__(self):
        self.m = collections.Counter()
        self.mr = collections.Counter()
        self.mig = 0.0   # line-hops to move blocks to their new home (16 lines per KB)

    def add(self, srv, g, r, keep):
        if srv != g and r:
            self.m[(srv, g)] += r
            self.mr[(srv, g)] += r * keep

    def stats(self, base_link=None):
        R = sum(self.m.values())
        H = sum(v * hops(s, g) for (s, g), v in self.m.items())
        Rr = sum(self.mr.values())
        Hr = sum(v * hops(s, g) for (s, g), v in self.mr.items())
        own = collections.Counter()
        link = collections.Counter()
        for (s, g), v in self.m.items():
            own[s] += v
            for l in ROUTES[(s, g)]:
                link[l] += v
        return dict(R=R, H=H, R_ret=Rr, H_ret=Hr,
                    own_max_share=(max(own.values()) / R) if R else 0.0,
                    link_max=max(link.values()) if link else 0, mig=self.mig)


def load(prefix, clsfile):
    cls = {}
    with gzip.open(clsfile, "rt") as f:
        for r in csv.DictReader(f):
            cls[int(r["page"])] = (r["cls"], int(r["owner"]))
    pages = collections.defaultdict(dict)
    with gzip.open(prefix + "_readers.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            reads = int(r["reads"])
            if reads == 0:
                continue
            p = int(r["page"])
            if p not in cls:
                continue          # page without remote reads in the baseline
            s = [int(x) for x in r["sub_reads"].split(";")]
            rep = int(r["rep_intra"]) + int(r["rep_cross"])
            pages[p][int(r["reader"])] = ([s[0] + s[1], s[2] + s[3], s[4] + s[5], s[6] + s[7]],
                                          max(0.0, (reads - rep) / reads))
    return cls, pages


POLICIES = ["none", "page", "sub1k", "center", "k2", "k4", "ring", "rep4", "cls_k2", "cls_k4",
            "cls_ctr", "cls_ring", "cls_hotnone", "sub_ring", "sub_hotnone", "v2", "med", "page64k", "page2m"]


def run(prefix, clsfile, label):
    cls, pages = load(prefix, clsfile)
    acc = {p: Acc() for p in POLICIES}
    hot = {p: Acc() for p in ["none", "center", "k2", "k4", "ring", "rep4"]}   # hot pages only
    # large pages: unit -> weights
    units = {"page64k": collections.defaultdict(collections.Counter),
             "page2m": collections.defaultdict(collections.Counter)}
    owner_of = {}
    cls_reads = collections.Counter()
    hot_own = collections.Counter()
    for pg, R in pages.items():
        c, o = cls[pg]
        if c == "热点广播":
            hot_own[o] += sum(sum(b) for g, (b, _) in R.items() if g != o)
    conc = (max(hot_own.values()) / sum(hot_own.values()) >= 0.3) if sum(hot_own.values()) else False
    bycls = {p: collections.defaultdict(Acc) for p in ("none", "v2", "med")}
    for pg, R in pages.items():
        c, o = cls[pg]
        owner_of[pg] = o
        for key, sh in (("page64k", 4), ("page2m", 9)):
            for g, (b, _) in R.items():
                units[key][pg >> sh][g] += sum(b)
    for key in units:
        units[key] = {u: main_user(w, max(w, key=lambda g: w[g])) for u, w in units[key].items()}
        # tie-free: unit placed at its top user; the original owner of each page keeps it
        # when that user is the owner (handled below: target == owner -> no move)

    for pg, R in pages.items():
        c, o = cls[pg]
        wpage = {g: sum(b) for g, (b, _) in R.items()}
        cls_reads[c] += sum(v for g, v in wpage.items() if g != o)
        tpage = main_user(wpage, o)
        tctr = best_tile(wpage)
        tblk = []
        for i in range(4):
            wb = {g: b[i] for g, (b, _) in R.items() if b[i]}
            tblk.append(main_user(wb, o) if wb else o)
        k4 = [C4[(i + pg) % 4] for i in range(4)]
        pair = C2[pg % 2]
        k2 = [pair[0], pair[0], pair[1], pair[1]]
        ring = [RING[(4 * pg + i) % len(RING)] for i in range(4)]
        targets = {
            "none": [o] * 4, "page": [tpage] * 4, "sub1k": tblk, "center": [tctr] * 4,
            "k2": k2, "k4": k4, "ring": ring,
            "page64k": [units["page64k"][pg >> 4]] * 4, "page2m": [units["page2m"][pg >> 9]] * 4,
        }
        hotp = c == "热点广播"
        targets["sub_ring"] = ring if hotp else tblk
        targets["sub_hotnone"] = [o] * 4 if hotp else tblk
        tmed = []
        for i in range(4):
            wb = {g: b[i] for g, (b, _) in R.items() if b[i]}
            tmed.append(best_tile(wb) if wb else o)
        hot_t = ring if conc else [o] * 4
        targets["v2"] = hot_t if hotp else tblk
        targets["med"] = hot_t if hotp else tmed
        for pol in ("none", "v2", "med"):
            a = bycls[pol][c]
            for i in range(4):
                for g, (b, keep) in R.items():
                    a.add(targets[pol][i], g, b[i], keep)
        if c in ("私有放错", "流式"):
            cl = tblk
            targets.update(cls_k2=cl, cls_k4=cl, cls_ctr=cl, cls_ring=cl, cls_hotnone=cl)
        elif hotp:
            targets.update(cls_k2=k2, cls_k4=k4, cls_ctr=[tctr] * 4, cls_ring=ring,
                           cls_hotnone=[o] * 4)
        else:
            targets.update(cls_k2=[o] * 4, cls_k4=[o] * 4, cls_ctr=[o] * 4, cls_ring=[o] * 4,
                           cls_hotnone=[o] * 4)
        for pol in POLICIES:
            a = acc[pol]
            if pol == "rep4":
                for g, (b, keep) in R.items():
                    srv = o
                    if hotp:
                        srv = min(REP4, key=lambda t: (hops(t, g), t))
                    for i in range(4):
                        a.add(srv, g, b[i], keep)
                if hotp:
                    a.mig += 64 * sum(hops(o, t) for t in REP4)
                continue
            t = targets[pol]
            for i in range(4):
                a.mig += 16 * hops(o, t[i])
                for g, (b, keep) in R.items():
                    a.add(t[i], g, b[i], keep)
        if hotp:
            for pol in hot:
                a = hot[pol]
                for g, (b, keep) in R.items():
                    for i in range(4):
                        if pol == "rep4":
                            srv = min(REP4, key=lambda t: (hops(t, g), t))
                        else:
                            srv = targets[pol][i]
                        a.add(srv, g, b[i], keep)
    out = dict(label=label, cls_reads=dict(cls_reads), hot_concentrated=conc)
    out["bycls"] = {p: {c: a.stats() for c, a in d.items()} for p, d in bycls.items()}
    out["all"] = {p: a.stats() for p, a in acc.items()}
    out["hot"] = {p: a.stats() for p, a in hot.items()}
    return out


if __name__ == "__main__":
    print(json.dumps(run(sys.argv[1], sys.argv[2], sys.argv[3]), ensure_ascii=False))
