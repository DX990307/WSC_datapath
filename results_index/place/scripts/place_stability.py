#!/usr/bin/env python3
"""P20 step 1 (b), offline: is the best location of a page / 1 KB block stable over time?

usage: place_stability.py <prefix> <cls_pages.csv.gz> <label>   -> JSON on stdout
  <prefix>  .../stats/metrics_pgcls of a baseline run with a raw trace (-pgcls-sample N);
            the trace keeps every L1V access of 1/N of the pages (page-hash sampling).

For every sampled page and 1 KB block, epochs are global kernels (gk) and 10 us windows.
Per epoch the "best location" is the block's main user (most L1V reads, ties -> earlier
choice / owner). Reported, weighted by the block's remote reads (owner = the run's owner):
  chg      fraction of consecutive active epochs whose main user differs
  pingpong fraction of changes that return to the location held two changes earlier
  dyn_H / static_H   read-hops if each epoch used its own best location (plus a 16-line move
           per change x hops) vs the whole-run static best location (P-sub1k)
  mig_over_saved     move line-hops of the dynamic policy / read-hops it saves vs static
Restricted to the classes P acts on (私有放错, 流式) and to 热点广播 separately.
"""
import csv, gzip, json, sys, collections

W = 7
TX = [t % W for t in range(W * W)]
TY = [t // W for t in range(W * W)]


def hops(a, b):
    return abs(TX[a] - TX[b]) + abs(TY[a] - TY[b])


def main_user(w, prefer):
    m = max(w.values())
    if w.get(prefer, 0) == m:
        return prefer
    return max(w, key=lambda g: (w[g], -g))


def analyse(blocks, owner, groups):
    res = {}
    for gname, keep in groups.items():
        agg = collections.Counter()
        for (pg, b), ep in blocks.items():
            if keep(pg) is False:
                continue
            o = owner[pg]
            tot = collections.Counter()
            for w in ep.values():
                tot.update(w)
            rr = sum(v for g, v in tot.items() if g != o)
            if rr == 0:
                continue
            st = main_user(tot, o)
            H_static = sum(v * hops(g, st) for g, v in tot.items()) + 16 * hops(o, st)
            seq = []
            cur = o
            H_dyn = 0
            mig = 0
            for e in sorted(ep):
                w = ep[e]
                t = main_user(w, cur)
                if t != cur:
                    mig += 16 * hops(cur, t)
                seq.append(t)
                cur = t
                H_dyn += sum(v * hops(g, t) for g, v in w.items())
            ch = sum(1 for i in range(1, len(seq)) if seq[i] != seq[i - 1])
            changes = [seq[0]] + [seq[i] for i in range(1, len(seq)) if seq[i] != seq[i - 1]]
            pp = sum(1 for i in range(2, len(changes)) if changes[i] == changes[i - 2])
            trans = max(0, len(seq) - 1)
            agg["w"] += rr
            agg["chg_w"] += rr * (ch / trans if trans else 0.0)
            agg["multi_w"] += rr if trans else 0
            agg["changes"] += ch
            agg["pingpong"] += pp
            agg["H_static"] += H_static
            agg["H_dyn"] += H_dyn
            agg["mig"] += mig
            agg["blocks"] += 1
        w = agg["w"] or 1
        saved = agg["H_static"] - agg["H_dyn"]
        res[gname] = dict(
            blocks=agg["blocks"], remote_reads_sampled=agg["w"],
            multi_epoch_share=agg["multi_w"] / w,
            chg=agg["chg_w"] / w,
            pingpong=(agg["pingpong"] / agg["changes"]) if agg["changes"] else 0.0,
            dyn_over_static_H=((agg["H_dyn"] + agg["mig"]) / agg["H_static"]) if agg["H_static"] else None,
            dyn_noMig_over_static_H=(agg["H_dyn"] / agg["H_static"]) if agg["H_static"] else None,
            mig_over_saved=(agg["mig"] / saved) if saved > 0 else None)
    return res


def main():
    prefix, clsfile, label = sys.argv[1:4]
    cls = {}
    owner_cls = {}
    with gzip.open(clsfile, "rt") as f:
        for r in csv.DictReader(f):
            cls[int(r["page"])] = r["cls"]
            owner_cls[int(r["page"])] = int(r["owner"])
    tot = {}
    with open(prefix + "_totals.csv") as f:
        for r in csv.DictReader(f):
            tot[r["key"]] = int(r["value"])
    win = tot.get("window_ns", 10000)
    byk = collections.defaultdict(lambda: collections.defaultdict(collections.Counter))
    byw = collections.defaultdict(lambda: collections.defaultdict(collections.Counter))
    with gzip.open(prefix + "_trace.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            if r["op"] != "R" or r["src"] != "v":
                continue
            pg = int(r["page"])
            if pg not in owner_cls:
                continue          # no remote reads in the run: nothing to place
            g = int(r["req"])
            key = (pg, int(r["off"]) >> 10)
            byk[key][int(r["gk"])][g] += 1
            byw[key][int(float(r["t_ns"]) // win)][g] += 1
    owner = owner_cls
    groups = {
        "P_classes": lambda pg: cls.get(pg) in ("私有放错", "流式"),
        "hot": lambda pg: cls.get(pg) == "热点广播",
        "all": lambda pg: True,
    }
    out = dict(label=label, kernels=max((e for ep in byk.values() for e in ep), default=0),
               kernel=analyse(byk, owner, groups), window=analyse(byw, owner, groups))
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main()
