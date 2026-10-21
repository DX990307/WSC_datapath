#!/usr/bin/env python3
"""Markdown tables from m2w.csv (stdout)."""
import csv, math, os, collections

OUT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WL = ["pr-g24", "npbcg-W", "sssp-s18e4-r1", "bfs-s18e2", "ar-pc1", "cfd-c172k-b128"]
CONDS = ["w64b384", "w256b384", "w2048b384", "w64b48"]
R = collections.defaultdict(dict)
for o in csv.DictReader(open(f"{OUT}/m2w.csv")):
    if str(o["exit"]) in ("0", "0.0") and o["total_time"] and str(o["rc"]) == "0":
        R[(o["cond"], o["workload"])][o["config"]] = o


def F(o, k):
    try:
        return float(o[k])
    except Exception:
        return None


def T(c, w, cfg):
    o = R[(c, w)].get(cfg)
    return F(o, "total_time") if o else None


def ratio(a, b):
    return a / b if a and b else None


def gm(v):
    v = [x for x in v if x]
    return math.exp(sum(map(math.log, v)) / len(v)) if v else None


def fmt(x, d=3):
    return "–" if x is None else f"{x:.{d}f}"


def table(title, fn, d=3, gmean=True):
    print(f"\n### {title}\n")
    print("| workload | " + " | ".join(CONDS) + " |")
    print("|---" * (len(CONDS) + 1) + "|")
    cols = collections.defaultdict(list)
    for w in WL:
        cells = []
        for c in CONDS:
            v = fn(c, w)
            cells.append(fmt(v, d))
            cols[c].append(v)
        print(f"| {w} | " + " | ".join(cells) + " |")
    if gmean:
        print("| **GMEAN** | " + " | ".join(
            f"**{fmt(gm(cols[c]), d)}** ({sum(1 for x in cols[c] if x)})" for c in CONDS) + " |")


table("A. Complete speedup vs baseline (same W / bandwidth)", lambda c, w: ratio(T(c, w, "baseline"), T(c, w, "Complete")))
table("B. M2 gain = T(M1+M3) / T(Complete)  (>1: M2 helps)", lambda c, w: ratio(T(c, w, "M1+M3"), T(c, w, "Complete")))
table("C. allocation = T(alloc shared) / T(Complete, rtt)  (>1: rtt helps)", lambda c, w: ratio(T(c, w, "alloc_shared"), T(c, w, "Complete")))
table("D. M1+M3 speedup vs baseline", lambda c, w: ratio(T(c, w, "baseline"), T(c, w, "M1+M3")))
table("E. baseline time (us)", lambda c, w: (T(c, w, "baseline") or 0) * 1e6 or None, 1, False)


def stat(cfg, k, scale=1):
    def f(c, w):
        o = R[(c, w)].get(cfg)
        v = F(o, k) if o else None
        return v * scale if v is not None else None
    return f


for cfg in ("baseline", "Complete"):
    table(f"F. {cfg} link mean utilization", stat(cfg, "links:link_mean"), 3, False)
    table(f"G. {cfg} link max 1-us-window utilization", stat(cfg, "links:link_window_max"), 3, False)
table("H. baseline requester wait_rrt mean (ns)", stat("baseline", "lat:wait_rrt_mean"), 0, False)
table("I. Complete wait_rrt mean (ns)", stat("Complete", "lat:wait_rrt_mean"), 0, False)
table("J. Complete req window-full frac", stat("Complete", "req_window_full_frac"), 2, False)
table("K. Complete owner merge frac (merged / owner full-line reads)", stat("Complete", "owner_merge_frac"), 3, False)
table("L. Complete multicast dests per packet", stat("Complete", "mc_dests_avg"), 2, False)
table("M. Complete multicast packets", stat("Complete", "mc_packets"), 0, False)
table("N. Complete multicast responses saved / owner reads",
      lambda c, w: ratio(stat("Complete", "mc_responses_saved")(c, w), stat("Complete", "owner_reads")(c, w)), 3, False)
table("O. baseline owner same-line concurrent other-GPM frac",
      stat("baseline", "owner_shift:owner_same_line_concurrent_other_gpm_frac"), 3, False)
table("P. M1+M3 owner merge frac", stat("M1+M3", "owner_merge_frac"), 3, False)
table("Q. Complete remote read latency (ns)", stat("Complete", "remote_read_lat_ns"), 0, False)
table("R. M1+M3 remote read latency (ns)", stat("M1+M3", "remote_read_lat_ns"), 0, False)

# sssp / bfs M2 detail
print("\n### S. sssp / bfs: M1+M3 vs Complete detail\n")
ks = ["total_time", "remote_read_lat_ns", "owner_merged", "owner_merge_frac", "mc_packets", "mc_dests_avg",
      "lat:owner_service_merged_mean", "lat:owner_service_merged_p95max", "lat:owner_rsp_wait_mean", "lat:owner_rsp_wait_p95max",
      "lat:ret_net_mean", "lat:ret_net_p95max", "lat:rtt_mean", "lat:rtt_p95max", "lat:wait_rrt_mean", "lat:merge_wait_mean",
      "lat:total_mean", "lat:total_p95max", "links:link_mean", "links:link_window_max", "req_window_full_frac", "busy48_frac"]
print("| cond | workload | config | " + " | ".join(k.replace("lat:", "") for k in ks) + " |")
print("|---" * (len(ks) + 3) + "|")
for c in CONDS:
    for w in ("sssp-s18e4-r1", "bfs-s18e2"):
        for cfg in ("M1+M3", "Complete"):
            o = R[(c, w)].get(cfg)
            if not o:
                continue
            vals = []
            for k in ks:
                v = F(o, k)
                if k == "total_time" and v:
                    v *= 1e6
                vals.append(fmt(v, 3 if v is not None and abs(v) < 10 else 0))
            print(f"| {c} | {w} | {cfg} | " + " | ".join(vals) + " |")
