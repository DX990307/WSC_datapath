#!/usr/bin/env python3
"""exploration: per buffer, run-length encoding of the page main reader (device) vs page index"""
import sys, collections
from offline_common import *

w = sys.argv[1]
info, bl = load_alloc(w)
cnt = collections.defaultdict(collections.Counter)
for r in readers(w):
    rd = int(r["reads"])
    if rd:
        cnt[int(r["page"])][int(r["reader"])] += rd
for blk, pl in sorted(bl.items()):
    N = len(pl)
    seq = []
    for i, p in enumerate(pl):
        c = cnt.get(p)
        own = dev2tile(info[p][1])
        if not c:
            seq.append(".")
            continue
        tot = sum(c.values())
        g, v = c.most_common(1)[0]
        if v < 0.6 * tot:
            seq.append("?")
        elif g == own:
            seq.append("=")
        else:
            seq.append(str(tile2dev(g)))
    rle = []
    for s in seq:
        if rle and rle[-1][0] == s:
            rle[-1][1] += 1
        else:
            rle.append([s, 1])
    print(f"blk {blk} N={N} chunk={N//48} runs={len(rle)}: " + " ".join(f"{s}x{n}" for s, n in rle[:80]))
