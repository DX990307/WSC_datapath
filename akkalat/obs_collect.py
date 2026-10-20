#!/usr/bin/env python3
"""Collect the P13 observation statistics of several runs into the CSVs of
isca27/observations/README.md.

usage: obs_collect.py OUT_DIR RESULT_DIR [RESULT_DIR ...]

Each RESULT_DIR is results/<exp_id> written by akkalat/isca27_run.sh with
-report-obs-* (and optionally -report-m1-sharing). workload and config come
from <exp_id> = <MMDD>_<category>_<item>_<config>_<commit>. Runs whose
meta.json exit_code is not 0 are listed in runs.csv but skipped.

Writes OUT_DIR/{runs,latency_segments,occupancy,links,sharing,fanin_hist,
owner_shift,requester_agg,remote_reuse}.csv.
"""

import csv
import json
import math
import os
import sys
from collections import defaultdict


def read_csv(path):
    if not os.path.exists(path):
        return []
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def fnum(v, default=0.0):
    try:
        return float(v)
    except (TypeError, ValueError):
        return default


def ident(run_dir):
    exp_id = os.path.basename(os.path.normpath(run_dir))
    parts = exp_id.split("_")
    if len(parts) >= 5:
        return exp_id, parts[2], parts[3], parts[4]
    return exp_id, exp_id, "", ""


def quantile(values, q):
    if not values:
        return 0.0
    v = sorted(values)
    i = min(len(v) - 1, max(0, int(math.ceil(q * len(v))) - 1))
    return v[i]


def write(path, header, rows):
    with open(path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(header)
        for r in rows:
            w.writerow([r.get(h, "") for h in header])


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    runs, lat, occ, links, sharing, fanin, owner, agg, reuse = (
        [] for _ in range(9))
    lat_header = None
    for d in sys.argv[2:]:
        exp_id, item, config, commit = ident(d)
        meta = {}
        if os.path.exists(os.path.join(d, "meta.json")):
            meta = json.load(open(os.path.join(d, "meta.json")))
        rc = meta.get("exit_code")
        total = ""
        for r in read_csv(os.path.join(d, "summary.csv")):
            if r.get("metric") == "total_time":
                total = r.get("value")
        runs.append({"exp_id": exp_id, "workload": item, "config": config,
                     "commit": commit, "exit_code": rc, "total_time_s": total,
                     "host": meta.get("host", ""), "start": meta.get("start", ""),
                     "end": meta.get("end", "")})
        if str(rc) != "0":
            continue
        base = {"workload": item, "config": config}
        st = os.path.join(d, "stats", "metrics")
        summ = {r["metric"]: r["value"]
                for r in read_csv(st + "_obs_summary.csv")}

        # latency segments
        for r in read_csv(st + "_obs_latency.csv"):
            r = dict(r)
            r.pop("workload", None)
            row = dict(base, **r)
            lat.append(row)
            if lat_header is None:
                lat_header = ["workload", "config"] + list(r.keys())

        # occupancy
        orows = read_csv(st + "_obs_occupancy.csv")
        for r in orows:
            r = dict(r)
            r.pop("workload", None)
            occ.append(dict(base, **r))

        # links
        lrows = read_csv(st + "_obs_links.csv")
        link_row = {}
        if lrows or "mesh_bytes" in summ:
            utils = [fnum(r["util"]) for r in lrows
                     if "SW[" in r["link_src"] and "SW[" in r["link_dst"]]
            expected = int(fnum(summ.get("links_expected"), len(utils)))
            utils += [0.0] * max(0, expected - len(utils))
            wmax = [fnum(r["max_window_util"]) for r in lrows]
            link_row = dict(base, link_mean=sum(utils) / len(utils) if utils else 0,
                            link_p99=quantile(utils, 0.99),
                            link_max=max(utils) if utils else 0,
                            link_window_max=max(wmax) if wmax else 0,
                            link_window_p99=summ.get("link_window_util_p99", ""),
                            mesh_bytes=summ.get("mesh_bytes", ""),
                            mesh_max_window_bytes=summ.get(
                                "mesh_max_window_bytes", ""),
                            links=expected)
            links.append(link_row)

        # sharing (-report-m1-sharing)
        wins = read_csv(st + "_m1_windows.csv")
        infl = {r["metric"]: fnum(r["value"])
                for r in read_csv(st + "_m1_inflight.csv")}
        by_w = defaultdict(list)
        for r in wins:
            by_w[r["window_ns"]].append(r)
        for wns, rows in sorted(by_w.items(), key=lambda kv: fnum(kv[0])):
            reqs = sum(fnum(r["requests"]) for r in rows)
            shared = [r for r in rows if fnum(r["sharers"]) >= 2]
            sreq = sum(fnum(r["requests"]) for r in shared)
            slines = sum(fnum(r["lines"]) for r in shared)
            pairs = sum(fnum(r["pairs"]) for r in shared)
            uni = sum(fnum(r["sum_owner_hops"]) for r in shared)
            tree = sum(fnum(r["sum_tree_links"]) for r in shared)
            all_uni = sum(fnum(r["sum_owner_hops"]) for r in rows)
            sharing.append(dict(
                base, window_ns=wns, remote_reads=reqs,
                shared_read_frac=sreq / reqs if reqs else 0,
                shared_lines=slines,
                mean_sharers=(sum(fnum(r["sharers"]) * fnum(r["lines"])
                                  for r in shared) / slines) if slines else 0,
                mean_pair_hops=(sum(fnum(r["sum_pair_hops"]) for r in shared)
                                / pairs) if pairs else 0,
                mean_min_pair_hops=(sum(fnum(r["sum_min_pair_hops"])
                                        for r in shared) / slines)
                if slines else 0,
                mean_owner_hops=(uni / sum(fnum(r["sharers"]) * fnum(r["lines"])
                                           for r in shared)) if slines else 0,
                multicast_links_saved_shared=(1 - tree / uni) if uni else 0,
                multicast_links_saved_all=((uni - tree) / all_uni)
                if all_uni else 0,
                requester_dup_same_gpm_frac=(
                    infl.get("requester_dup_same_gpm", 0) /
                    infl["requester_remote_reads"])
                if infl.get("requester_remote_reads") else "",
                requester_dup_other_gpm_only_frac=(
                    infl.get("requester_dup_other_gpm_only", 0) /
                    infl["requester_remote_reads"])
                if infl.get("requester_remote_reads") else "",
                owner_dup_frac=(infl.get("owner_dup", 0) / infl["owner_reads"])
                if infl.get("owner_reads") else ""))
            for r in rows:
                fanin.append(dict(base, window_ns=wns, sharers=r["sharers"],
                                  lines=r["lines"], requests=r["requests"]))

        # owner shift
        lat_all = next((r for r in read_csv(st + "_obs_latency.csv")
                        if r["hops"] == "all"), None)
        if lat_all is not None:
            bw = sum(fnum(r["remote_bw"]) for r in orows)
            owner.append(dict(
                base,
                owner_queue_mean=lat_all.get("owner_queue_mean", ""),
                owner_same_line_concurrent_frac=summ.get(
                    "owner_same_line_concurrent_frac", ""),
                owner_same_line_concurrent_other_gpm_frac=summ.get(
                    "owner_same_line_concurrent_other_gpm_frac", ""),
                rtt_mean=lat_all.get("rtt_mean", ""),
                remote_bw=bw if orows else "",
                link_max=link_row.get("link_max", ""),
                link_window_max=link_row.get("link_window_max", "")))

        # requester aggregation
        if "agg_remote_reads" in summ:
            n = fnum(summ["agg_remote_reads"])
            w = int(fnum(summ.get("agg_window_cycles"), 16))
            sent = fnum(summ.get("agg_sent_lines"))
            row = dict(
                base, remote_reads=int(n),
                same_line_dup_frac=fnum(summ["agg_same_line_window"]) / n
                if n else 0,
                same_page_frac=fnum(summ.get(f"agg_same_page_le_{w}cyc")) / n
                if n else 0,
                inflight_same_line_same_gpm_frac=fnum(
                    summ["agg_inflight_same_line_same_gpm"]) / n if n else 0,
                window_cycles=w,
                same_cycle_same_page_frac=fnum(
                    summ.get("agg_same_cycle_same_page_lines")) / sent
                if sent else 0,
                multi_line_packet_line_frac=fnum(
                    summ.get("agg_multi_line_packet_lines")) / sent
                if sent else 0)
            for th in (0, 1, 2, 4, 8, 16, 32, 64):
                row[f"same_page_le_{th}cyc_frac"] = fnum(
                    summ.get(f"agg_same_page_le_{th}cyc")) / n if n else 0
            agg.append(row)

        # remote reuse + L2 free
        if "reuse_remote_reads" in summ:
            reads = fnum(summ["reuse_remote_reads"])
            uniq = fnum(summ["reuse_unique_lines"])
            l2 = read_csv(st + "_obs_l2free.csv")
            mean = (lambda k: sum(fnum(r[k]) for r in l2) / len(l2)
                    if l2 else "")
            deliv = fnum(summ.get("multicast_delivered_lines"))
            reuse.append(dict(
                base, uniq_remote_lines=int(uniq),
                frac_lines_reused=fnum(summ["reuse_lines_reused"]) / uniq
                if uniq else 0,
                frac_reads_retouch=fnum(summ["reuse_retouch_reads"]) / reads
                if reads else 0,
                frac_reads_retouch_after_complete=fnum(
                    summ["reuse_retouch_reads_after_complete"]) / reads
                if reads else 0,
                reuse_interval_p50=summ.get("reuse_interval_p50_ns", ""),
                reuse_interval_p90=summ.get("reuse_interval_p90_ns", ""),
                l2_free_frac=mean("free_frac"),
                l2_invalid_frac=mean("invalid_frac"),
                l2_dead_frac=mean("dead_frac"),
                l2_tail_frac=mean("tail_frac"),
                multicast_reread_frac=fnum(summ.get("multicast_reread_lines"))
                / deliv if deliv else ""))

    write(os.path.join(out, "runs.csv"), ["exp_id", "workload", "config",
          "commit", "exit_code", "total_time_s", "host", "start", "end"], runs)
    if lat_header:
        write(os.path.join(out, "latency_segments.csv"), lat_header, lat)
    if occ:
        write(os.path.join(out, "occupancy.csv"),
              ["workload", "config", "gpm", "frac_time_full", "mean_used",
               "mean_waiting", "remote_bw", "owner_in_flight_mean",
               "top_dest_share", "samples", "limit", "max_used",
               "frac_used_ge32", "max_waiting", "remote_reads",
               "owner_in_flight_max", "top_dest"], occ)
    if links:
        write(os.path.join(out, "links.csv"),
              ["workload", "config", "link_mean", "link_p99", "link_max",
               "link_window_max", "link_window_p99", "mesh_bytes",
               "mesh_max_window_bytes", "links"], links)
    if sharing:
        write(os.path.join(out, "sharing.csv"),
              ["workload", "config", "window_ns", "remote_reads",
               "shared_read_frac", "shared_lines", "mean_sharers",
               "mean_pair_hops", "mean_min_pair_hops", "mean_owner_hops",
               "multicast_links_saved_shared", "multicast_links_saved_all",
               "requester_dup_same_gpm_frac",
               "requester_dup_other_gpm_only_frac", "owner_dup_frac"], sharing)
        write(os.path.join(out, "fanin_hist.csv"),
              ["workload", "config", "window_ns", "sharers", "lines",
               "requests"], fanin)
    if owner:
        write(os.path.join(out, "owner_shift.csv"),
              ["workload", "config", "owner_queue_mean",
               "owner_same_line_concurrent_frac",
               "owner_same_line_concurrent_other_gpm_frac", "rtt_mean",
               "remote_bw", "link_max", "link_window_max"], owner)
    if agg:
        write(os.path.join(out, "requester_agg.csv"),
              ["workload", "config", "remote_reads", "same_line_dup_frac",
               "same_page_frac", "inflight_same_line_same_gpm_frac",
               "window_cycles", "same_cycle_same_page_frac",
               "multi_line_packet_line_frac"] +
              [f"same_page_le_{th}cyc_frac" for th in (0, 1, 2, 4, 8, 16, 32, 64)],
              agg)
    if reuse:
        write(os.path.join(out, "remote_reuse.csv"),
              ["workload", "config", "uniq_remote_lines", "frac_lines_reused",
               "frac_reads_retouch", "frac_reads_retouch_after_complete",
               "reuse_interval_p50", "reuse_interval_p90", "l2_free_frac",
               "l2_invalid_frac", "l2_dead_frac", "l2_tail_frac",
               "multicast_reread_frac"], reuse)
    return 0


if __name__ == "__main__":
    sys.exit(main())
