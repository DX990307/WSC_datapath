#!/usr/bin/env python3
"""Data sanity checks: alloc dev->tile mapping vs baseline owner and trace own; driver
split rule; trace page sampling; trace order; class mix."""
import sys, json, collections
from offline_common import *


def main(w):
    info, bl = load_alloc(w)
    pa = pages_agg(w)
    out = dict(w=w, alloc_pages=len(info), blocks=len(bl))
    # driver rule: dev = min(48, i // floor(N/48) + 1), N<48 -> 1
    bad_rule = 0
    for ppn, (vpn, dev, blk, i, n) in info.items():
        c = n // 48
        exp = 1 if c == 0 else min(48, i // c + 1)
        if exp != dev:
            bad_rule += 1
    out["rule_mismatch"] = bad_rule
    m = mm = miss = 0
    for p, r in pa.items():
        if p not in info:
            miss += 1
            continue
        if dev2tile(info[p][1]) == int(r["owner"]):
            m += 1
        else:
            mm += 1
    out.update(agg_pages=len(pa), agg_owner_match=m, agg_owner_mismatch=mm, agg_not_in_alloc=miss)
    # trace
    tm = tmm = tmiss = 0
    pages = set()
    reqtiles = set()
    last = -1
    unordered = 0
    n = 0
    for t, req, own, pg, off, op in trace_rows(w):
        n += 1
        if t < last:
            unordered += 1
        last = t
        pages.add(pg)
        reqtiles.add(req)
        if pg not in info:
            tmiss += 1
        elif dev2tile(info[pg][1]) == own:
            tm += 1
        else:
            tmm += 1
    S = trace_sample(w)
    hs = sum(1 for p in pages if pghash(p) % S == 0)
    out.update(trace_rows=n, trace_own_match=tm, trace_own_mismatch=tmm, trace_not_in_alloc=tmiss,
               trace_pages=len(pages), trace_sample=S, trace_pages_hash_ok=hs,
               trace_unordered=unordered, req_tiles=sorted(reqtiles)[:3] + ["..."] + sorted(reqtiles)[-3:],
               req_has_24=24 in reqtiles)
    js = json.load(open(os.path.join(ROOT, "cls", w + ".json")))
    tot = sum(js["cls_reads"].values())
    out["mix"] = {c: round(v / tot, 3) for c, v in sorted(js["cls_reads"].items(), key=lambda x: -x[1]) if v / tot >= 0.02}
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main(sys.argv[1])
