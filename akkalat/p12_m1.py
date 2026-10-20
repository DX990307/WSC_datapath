#!/usr/bin/env python3
"""P12 (T18): summarize -report-m1-sharing outputs of several runs.

    python3 akkalat/p12_m1.py <out_dir> <workload>=<run_dir> ...

Writes
<out_dir>/m1_summary.csv     one row per workload x window (1 us, 10 us):
    remote_reads          remote reads observed at the L1Vs (L1V misses to RDMA)
    shared_req_frac       fraction of remote reads whose (window, line) was read
                          by >= 2 GPMs
    shared_line_frac      fraction of (window, line) pairs with >= 2 GPMs
    mean_sharers_per_req  request-weighted mean number of GPMs per (window, line)
    req_merge_saving      fraction of remote reads removed if each GPM sent one
                          request per (window, line) (requester-side merge)
    owner_merge_saving    fraction removed if the owner served each (window,
                          line) once (= 1 - lines / requests)
    mean_pair_hops        mean hops between two GPMs that read the same line
    mean_owner_hops_shared mean hops requester -> owner on shared lines
    multicast_link_saving for shared lines, 1 - YX tree links / sum of unicast
                          hops (response traffic saved by owner multicast)
    multicast_link_saving_all  the same over all remote response hops
<out_dir>/m1_inflight.csv    one row per workload: in-flight duplicate rates
<out_dir>/m1_sharers.csv     workload x window x sharers: lines, requests
<out_dir>/m1_pairhops.csv    workload x window x hops: GPM pairs
"""

import csv
import os
import sys


def read_csv(path):
    with open(path) as f:
        return list(csv.DictReader(f))


def main():
    out_dir = sys.argv[1]
    os.makedirs(out_dir, exist_ok=True)
    summary, inflight, sharers, pairhops = [], [], [], []
    for arg in sys.argv[2:]:
        wl, run = arg.split("=", 1)
        prefix = os.path.join(run, "stats", "metrics_m1")
        win = read_csv(prefix + "_windows.csv")
        inf = {r["metric"]: int(r["value"]) for r in read_csv(prefix + "_inflight.csv")}
        for r in read_csv(prefix + "_pairhops.csv"):
            pairhops.append({"workload": wl, **r})
        by_w = {}
        for r in win:
            by_w.setdefault(r["window_ns"], []).append(r)
            sharers.append({"workload": wl, "window_ns": r["window_ns"], "sharers": r["sharers"],
                            "lines": r["lines"], "requests": r["requests"]})
        for w, rows in by_w.items():
            g = lambda r, k: int(r[k])  # noqa: E731
            reqs = sum(g(r, "requests") for r in rows)
            lines = sum(g(r, "lines") for r in rows)
            gpm_lines = sum(g(r, "lines") * int(r["sharers"]) for r in rows)
            sh = [r for r in rows if int(r["sharers"]) >= 2]
            sh_reqs = sum(g(r, "requests") for r in sh)
            sh_lines = sum(g(r, "lines") for r in sh)
            sh_gpm_lines = sum(g(r, "lines") * int(r["sharers"]) for r in sh)
            pairs = sum(g(r, "pairs") for r in sh)
            uni_sh = sum(g(r, "sum_owner_hops") for r in sh)
            uni_all = sum(g(r, "sum_owner_hops") for r in rows)
            tree = sum(g(r, "sum_tree_links") for r in sh)
            summary.append({
                "workload": wl, "window_ns": w, "windows": rows[0]["windows"],
                "remote_reads": reqs,
                "shared_req_frac": sh_reqs / reqs if reqs else "",
                "shared_line_frac": sh_lines / lines if lines else "",
                "mean_sharers_per_req": sum(g(r, "requests") * int(r["sharers"]) for r in rows) / reqs
                if reqs else "",
                "req_merge_saving": 1 - gpm_lines / reqs if reqs else "",
                "owner_merge_saving": 1 - lines / reqs if reqs else "",
                "mean_pair_hops": sum(g(r, "sum_pair_hops") for r in sh) / pairs if pairs else "",
                "mean_min_pair_hops": sum(g(r, "sum_min_pair_hops") for r in sh) / sh_lines
                if sh_lines else "",
                "mean_owner_hops_shared": uni_sh / sh_gpm_lines if sh_gpm_lines else "",
                "mean_owner_hops_all": uni_all / gpm_lines if gpm_lines else "",
                "multicast_link_saving": 1 - tree / uni_sh if uni_sh else "",
                "multicast_link_saving_all": (uni_sh - tree) / uni_all if uni_all else "",
            })
        rr = inf.get("requester_remote_reads", 0)
        ow = inf.get("owner_reads", 0)
        row = {
            "workload": wl, "requester_remote_reads": rr,
            "dup_same_gpm_frac": inf["requester_dup_same_gpm"] / rr if rr else "",
            "dup_other_gpm_only_frac": inf["requester_dup_other_gpm_only"] / rr if rr else "",
            "dup_any_frac": (inf["requester_dup_same_gpm"] + inf["requester_dup_other_gpm_only"]) / rr
            if rr else "",
            "requester_max_outstanding": inf["requester_max_outstanding"],
            "owner_reads": ow,
            "owner_dup_frac": inf["owner_dup"] / ow if ow else "",
            "owner_dup_other_requester_frac": inf["owner_dup_other_requester"] / ow if ow else "",
        }
        cross = {int(k.rsplit("_", 1)[1]): v for k, v in inf.items()
                 if k.startswith("requester_dup_other_gpm_only_min_hops_")}
        n = sum(cross.values())
        row["dup_other_gpm_mean_min_hops"] = sum(h * v for h, v in cross.items()) / n if n else ""
        row["dup_other_gpm_within_2_hops_frac"] = sum(v for h, v in cross.items() if h <= 2) / n if n else ""
        inflight.append(row)

    for name, rows in (("m1_summary", summary), ("m1_inflight", inflight),
                       ("m1_sharers", sharers), ("m1_pairhops", pairhops)):
        path = os.path.join(out_dir, name + ".csv")
        with open(path, "w", newline="") as f:
            keys = list(rows[0].keys()) if rows else ["none"]
            w = csv.DictWriter(f, keys)
            w.writeheader()
            for r in rows:
                w.writerow({k: (f"{v:.4f}" if isinstance(v, float) else v) for k, v in r.items()})
        print("wrote", path)


if __name__ == "__main__":
    main()
