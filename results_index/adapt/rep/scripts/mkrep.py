#!/usr/bin/env python3
"""P21 candidate 1 (oracle region replication): replica lists from a W=128 baseline pgcls run.

usage: mkrep.py <stats dir of the baseline run> <out prefix>
Writes <out>_rep4.txt and <out>_rep9.txt: '<ppn> <tile> ...' for every page that no GPM
ever wrote (whole run) and that >= 2 non-owner GPMs read (remote reads >= MINR).
Regions: k=4 pinwheel quadrants of the 7x7 mesh (12 GPM tiles each); k=9 = 3x3 blocks
(x, y in {0,1} {2,3,4} {5,6}). One replica per region that has readers, at the tile of the
region minimising sum(reads x hops) over the region's readers; none if that is the owner.
Also prints a JSON summary (pages, share of remote reads covered)."""
import sys, gzip, csv, json, collections
MINR = 8
W = 7
def xy(t): return t % W, t // W
def hops(a, b):
    ax, ay = xy(a); bx, by = xy(b); return abs(ax - bx) + abs(ay - by)
GPM = [t for t in range(49) if t != 24]
def quad(t):
    x, y = xy(t)
    if x < 3 and y < 3: return 0
    if x > 3 and y < 3: return 1
    if x < 3 and y > 3: return 2
    if x > 3 and y > 3: return 3
    if x == 3: return 0 if y < 3 else 3     # pinwheel
    return 1 if x > 3 else 2                # y == 3
def grp(v): return 0 if v <= 1 else (1 if v <= 4 else 2)
def nine(t):
    x, y = xy(t); return grp(x) * 3 + grp(y)
REG = {4: quad, 9: nine}
d, out = sys.argv[1], sys.argv[2]
pages = {}
with gzip.open(f"{d}/metrics_pgcls_pages.csv.gz", "rt") as f:
    for r in csv.DictReader(f):
        pages[int(r["page"])] = (int(r["owner"]), int(r["rd_remote"]), int(r["wr_remote"]) + int(r["wr_local"]))
rd = collections.defaultdict(dict)
wr = collections.Counter()
with gzip.open(f"{d}/metrics_pgcls_readers.csv.gz", "rt") as f:
    for r in csv.DictReader(f):
        p = int(r["page"])
        rd[p][int(r["reader"])] = int(r["reads"])
        wr[p] += int(r["writes"])
tot_remote = sum(v[1] for v in pages.values())
summ = {"remote_reads": tot_remote}
for k, reg in REG.items():
    n = cov = reps = 0
    with open(f"{out}_rep{k}.txt", "w") as fo:
        for p, (own, rr, w) in pages.items():
            if w or wr[p] or rr < MINR:
                continue
            readers = {t: c for t, c in rd[p].items() if t != own and c > 0}
            if len(readers) < 2:
                continue
            byreg = collections.defaultdict(dict)
            for t, c in readers.items():
                byreg[reg(t)][t] = c
            tiles = []
            for g, rs in sorted(byreg.items()):
                cand = [t for t in GPM if reg(t) == g]
                best = min(cand, key=lambda c: (sum(v * hops(c, t) for t, v in rs.items()), c))
                if best != own:
                    tiles.append(best)
            if not tiles:
                continue
            fo.write(f"{p} " + " ".join(map(str, tiles)) + "\n")
            n += 1; cov += rr; reps += len(tiles)
    summ[f"k{k}"] = dict(pages=n, replicas=reps, remote_read_share=cov / max(1, tot_remote))
print(json.dumps(summ))
