#!/usr/bin/env python3
"""P20 (10-22 update) step 1, offline: page-level migration targets by reader distribution.

usage: place_targets.py <prefix> <cls_pages.csv.gz> <label>  -> JSON on stdout
  <prefix> .../stats/metrics_pgcls of a baseline run (W = 128 main config)

Per 4 KB page: weights w[g] = L1V line reads of GPM g (owner's local reads included);
retention keep[g] as in place_bound.py. A page "migrates" only if some remote GPM reaches
the pm counter threshold (256 remote accesses; reads only here), as with -page-migration
counter. Target rules (all on the 7x7 mesh, centre tile 24 = CPU excluded):
  argmax    the remote GPM with the largest count (pm original; owner not counted)
  median    weighted Manhattan median of all readers (owner included), ties -> lowest load
  center    the read-weighted best of the 4 tiles next to the centre
  ring      shared pages (>= 2 readers) interleaved over the 12 tiles within 2 hops of the centre
  rep4      read-only upper bound: 4 replicas (tiles 8/12/36/40), each reader reads the nearest
  class(d, n, k, x)  dominant share >= d -> that GPM; readers > n -> k tiles around the median
            page by page (k = 0: stay); else median; decline if weighted hops drop < x.
Groups by number of readers (>= 1 % share): 1, 2-4, 5-16, 17-31, >= 32.
"""
import csv, gzip, json, sys, collections, itertools

W = 7
CENTER = 24
TILES = [t for t in range(W * W) if t != CENTER]
TX = [t % W for t in range(W * W)]
TY = [t // W for t in range(W * W)]
RING = [10, 16, 17, 18, 22, 23, 25, 26, 30, 31, 32, 38]
NEXT = [17, 23, 25, 31]
REP4 = [8, 12, 36, 40]
THRESH = 256
NOISE = 0.01


def hops(a, b):
    return abs(TX[a] - TX[b]) + abs(TY[a] - TY[b])


def route(src, dst):
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


def costs(w):
    return {t: sum(v * hops(g, t) for g, v in w.items()) for t in TILES}


def ranked(w):
    c = costs(w)
    return sorted(TILES, key=lambda t: (c[t], t)), c


def group(n):
    return "1" if n <= 1 else "2-4" if n <= 4 else "5-16" if n <= 16 else "17-31" if n <= 31 else ">=32"


class Acc:
    def __init__(self):
        self.m = collections.Counter()
        self.mr = collections.Counter()
        self.mig = 0
        self.moves = 0

    def add(self, srv, g, r, keep):
        if srv != g and r:
            self.m[(srv, g)] += r
            self.mr[(srv, g)] += r * keep

    def stats(self):
        R = sum(self.m.values())
        H = sum(v * hops(s, g) for (s, g), v in self.m.items())
        own = collections.Counter()
        link = collections.Counter()
        for (s, g), v in self.m.items():
            own[s] += v
            for l in ROUTES[(s, g)]:
                link[l] += v
        return dict(R=R, H=H, R_ret=sum(self.mr.values()),
                    H_ret=sum(v * hops(s, g) for (s, g), v in self.mr.items()),
                    own=(max(own.values()) / R) if R else 0.0,
                    link=max(link.values()) if link else 0, mig=self.mig, moves=self.moves)


DOMS = [0.5, 0.6, 0.75]
MAXR = [7, 15, 31, 47]
KS = [0, 2, 4]
XS = [0.0, 0.1, 0.25]


def main():
    prefix, clsfile, label = sys.argv[1:4]
    cls = {}
    with gzip.open(clsfile, "rt") as f:
        for r in csv.DictReader(f):
            cls[int(r["page"])] = (r["cls"], int(r["owner"]))
    pages = collections.defaultdict(dict)
    with gzip.open(prefix + "_readers.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            reads = int(r["reads"])
            p = int(r["page"])
            if reads == 0 or p not in cls:
                continue
            rep = int(r["rep_intra"]) + int(r["rep_cross"])
            pages[p][int(r["reader"])] = (reads, max(0.0, (reads - rep) / reads))
    simple = ["none", "argmax", "median", "center", "ring", "rep4"]
    acc = {p: Acc() for p in simple}
    combos = list(itertools.product(DOMS, MAXR, KS, XS))
    cacc = {c: Acc() for c in combos}
    nofire = Acc()
    grp = collections.defaultdict(lambda: collections.defaultdict(Acc))
    gstat = collections.Counter()
    gdisp = collections.Counter()
    for pg in sorted(pages):
        R = pages[pg]
        c, o = cls[pg]
        w = {g: v[0] for g, v in R.items()}
        tot = sum(w.values())
        readers = [g for g, v in w.items() if v >= NOISE * tot]
        n = len(readers)
        gname = group(n)
        rem = {g: v for g, v in w.items() if g != o}
        fires = bool(rem) and max(rem.values()) >= THRESH
        rk, cost = ranked(w)
        med = rk[0]
        targets = {"none": o}
        targets["argmax"] = max(rem, key=lambda g: (rem[g], -g)) if fires else o
        targets["median"] = med if fires else o
        ctr = min(NEXT, key=lambda t: (cost[t], t))
        targets["center"] = ctr if fires else o
        targets["ring"] = RING[pg % len(RING)] if (fires and n >= 2) else o
        rr = sum(v for g, v in w.items() if g != o)
        gstat[(gname, "R")] += rr
        gstat[(gname, "H")] += sum(v * hops(g, o) for g, v in w.items())
        gstat[(gname, "pages")] += 1
        if n >= 2:
            ph = [hops(a, b) for i, a in enumerate(readers) for b in readers[i + 1:]]
            gdisp[(gname, "w")] += rr
            gdisp[(gname, "hops")] += rr * sum(ph) / len(ph)
        for p in simple:
            a = acc[p]
            if p == "rep4":
                for g, (r_, keep) in R.items():
                    srv = min(REP4, key=lambda t: (hops(t, g), t)) if (fires and n >= 2) else o
                    a.add(srv, g, r_, keep)
                continue
            t = targets[p]
            if t != o:
                a.mig += 64 * hops(o, t)
                a.moves += 1
            for g, (r_, keep) in R.items():
                a.add(t, g, r_, keep)
            if p in ("none", "argmax", "median"):
                ga = grp[p][gname]
                for g, (r_, keep) in R.items():
                    ga.add(t, g, r_, keep)
        # class sweep (pages whose counter never fires stay put in every combination)
        if not fires:
            for g, (r_, keep) in R.items():
                nofire.add(o, g, r_, keep)
            if True:
                ga = grp["class"][gname]
                for g, (r_, keep) in R.items():
                    ga.add(o, g, r_, keep)
            continue
        best = max(w, key=lambda g: (w[g], -g))
        share = w[best] / tot
        contrib = {}
        for (d, nmax, k, x) in combos:
            if share >= d:
                t = best
            elif n > nmax:
                t = rk[pg % k] if k else o
            else:
                t = med
            if t != o and (cost[o] == 0 or (cost[o] - cost[t]) < x * cost[o]):
                t = o
            a = cacc[(d, nmax, k, x)]
            if t != o:
                a.mig += 64 * hops(o, t)
                a.moves += 1
            if t not in contrib:
                cm, cmr = collections.Counter(), collections.Counter()
                for g, (r_, keep) in R.items():
                    if t != g and r_:
                        cm[(t, g)] += r_
                        cmr[(t, g)] += r_ * keep
                contrib[t] = (cm, cmr)
            a.m.update(contrib[t][0])
            a.mr.update(contrib[t][1])
            if (d, nmax, k, x) == (0.6, 31, 4, 0.1):
                ga = grp["class"][gname]
                for g, (r_, keep) in R.items():
                    ga.add(t, g, r_, keep)
    for a in cacc.values():
        a.m.update(nofire.m)
        a.mr.update(nofire.mr)
    out = dict(label=label,
               groups={gname: dict(R=gstat[(gname, "R")], H=gstat[(gname, "H")],
                                   pages=gstat[(gname, "pages")],
                                   disp=(gdisp[(gname, "hops")] / gdisp[(gname, "w")]) if gdisp[(gname, "w")] else None)
                       for gname in ("1", "2-4", "5-16", "17-31", ">=32")},
               simple={p: a.stats() for p, a in acc.items()},
               bygroup={p: {g: a.stats() for g, a in d.items()} for p, d in grp.items()},
               sweep={"%g|%d|%d|%g" % c: a.stats() for c, a in cacc.items()})
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main()
