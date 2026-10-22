#!/usr/bin/env python3
"""P20 pass 1 -> pass 2: placement tables (-place-table) from one baseline run.

usage: mktable.py <prefix> <cls_pages.csv.gz> <out_dir> <wl>
  <prefix>  .../stats/metrics_pgcls of the pass-1 baseline (-report-pgcls -pgcls-place-ext)
  <cls_pages.csv.gz> pgcls_page.py output for the same run (page classes)
Writes <out_dir>/<wl>_{pg,sb,cls,ctr}.txt, lines "<1 KB block> <tile>", only blocks whose
target differs from their physical owner, and <out_dir>/<wl>_tables.json (counts).
Weights per (1 KB block, GPM) = L1V line reads (incl. the owner's local reads) + writes.
  pg  : each 4 KB page -> its main user (ties keep the owner)
  sb  : each 1 KB block -> its main user
  cls : P-按类 v2 (bound.md): hot-broadcast pages -> 4 blocks spread over the 12 tiles within
        2 hops of the centre ("ring 12") if the workload's hot pages are concentrated (the
        busiest owner serves >= HOT_CONC of the hot pages' remote reads), otherwise left at
        their owner; every other page -> 1 KB blocks to their main user
  ctr : like cls, but (concentrated) hot-broadcast pages whole to the read-weighted best tile
"""
import csv, gzip, json, os, sys, collections

W = 7
CENTER = 24
TILES = [t for t in range(W * W) if t != CENTER]
TX = [t % W for t in range(W * W)]
TY = [t // W for t in range(W * W)]
HOT_CONC = 0.3   # bound.md (a): concentrated workloads 0.63-1.0, spread ones <= 0.11
RING = [10, 16, 17, 18, 22, 23, 25, 26, 30, 31, 32, 38]


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
    if not w:
        return owner
    m = max(w.values())
    if m == 0 or w.get(owner, 0) == m:
        return owner
    return max(w, key=lambda g: (w[g], -g))


def main():
    prefix, clsfile, out, wl = sys.argv[1:5]
    cls = {}
    with gzip.open(clsfile, "rt") as f:
        for r in csv.DictReader(f):
            cls[int(r["page"])] = r["cls"]
    owner = {}
    with gzip.open(prefix + "_pages.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            owner[int(r["page"])] = int(r["owner"])
    w = collections.defaultdict(lambda: [collections.Counter() for _ in range(4)])
    with gzip.open(prefix + "_readers.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            s = [int(x) for x in r["sub_reads"].split(";")]
            pg, g = int(r["page"]), int(r["reader"])
            for i in range(4):
                if s[2 * i] + s[2 * i + 1]:
                    w[pg][i][g] += s[2 * i] + s[2 * i + 1]
    pe = prefix + "_place.csv.gz"
    if not os.path.exists(pe):
        sys.exit(f"missing {pe} (run pass 1 with -pgcls-place-ext)")
    with gzip.open(pe, "rt") as f:
        for r in csv.DictReader(f):
            pg, g = int(r["page"]), int(r["tile"])
            for i, x in enumerate(r["wsub1k"].split(";")):
                if int(x):
                    w[pg][i][g] += int(x)
    hot_own = collections.Counter()
    for pg, blocks in w.items():
        o = owner.get(pg)
        if o is None or cls.get(pg) != "热点广播":
            continue
        hot_own[o] += sum(v for b in blocks for g, v in b.items() if g != o)
    hot_share = max(hot_own.values()) / sum(hot_own.values()) if sum(hot_own.values()) else 0.0
    move_hot = hot_share >= HOT_CONC
    tables = {k: [] for k in ("pg", "sb", "cls", "ctr")}
    nhot = 0
    for pg in sorted(w):
        o = owner.get(pg)
        if o is None:
            continue
        blocks = w[pg]
        page_w = collections.Counter()
        for b in blocks:
            page_w.update(b)
        # pages touched only by their owner need no entry
        if all(g == o for g in page_w):
            continue
        tpg = main_user(page_w, o)
        tsb = [main_user(b, o) for b in blocks]
        hot = cls.get(pg) == "热点广播"
        if hot and not move_hot:
            nhot += 1
            ring = ctr = [o] * 4
        elif hot:
            nhot += 1
            ring = [RING[(4 * pg + i) % len(RING)] for i in range(4)]
            ctr = [best_tile(page_w)] * 4
        else:
            ring = ctr = tsb
        for name, t in (("pg", [tpg] * 4), ("sb", tsb), ("cls", ring), ("ctr", ctr)):
            for i in range(4):
                if t[i] != o:
                    tables[name].append(f"{(pg << 2) + i} {t[i]}")
    os.makedirs(out, exist_ok=True)
    for name, lines in tables.items():
        tmp = os.path.join(out, f".{wl}_{name}.tmp")
        with open(tmp, "w") as f:
            f.write("# P20 placement table %s %s\n" % (wl, name))
            f.write("\n".join(lines) + ("\n" if lines else ""))
        os.replace(tmp, os.path.join(out, f"{wl}_{name}.txt"))
    json.dump(dict(wl=wl, hot_pages=nhot, hot_owner_share=round(hot_share, 4), move_hot=move_hot, **{k: len(v) for k, v in tables.items()}),
              open(os.path.join(out, f"{wl}_tables.json"), "w"))


if __name__ == "__main__":
    main()
