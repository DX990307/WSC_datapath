#!/usr/bin/env python3
"""Per run: M3 admission / retention counters summed over the GPM (run on server).
usage: m3why.py <results dir glob>"""
import csv, collections, glob, os, sys
K = ["remote_logical_reads", "remote_wire_lines", "remote_demand_wire_lines", "remote_prefetch_wire_lines",
     "remote_duplicate_reads", "remote_inflight_merges", "remote_ready_merges", "remote_pre_send_merges",
     "remote_replica_probe_hits", "remote_l2_probe_misses", "remote_first_touch_lines",
     "remote_second_touch_admissions", "remote_l2_two_touch_fill_attempts", "remote_installed_fills",
     "remote_dropped_fills", "remote_unused_two_touch_retirements", "remote_tracked_evictions",
     "remote_fill_replaced_remote", "remote_local_clean_protection_drops", "remote_fill_into_invalid",
     "remote_reuse_write_uncacheable_skips", "remote_peak_replicas", "remote_seen_insert_failures",
     "remote_seen_false_positives", "remote_reuse_boundary_lines_invalidated", "remote_one_touch_probe_bypasses",
     "remote_line_entry_full_stalls", "remote_speculative_invalid_only_drops"]
w = csv.writer(sys.stdout)
w.writerow(["exp"] + K)
for d in sorted(glob.glob(sys.argv[1])):
    m = os.path.join(d, "stats", "metrics.csv")
    if not os.path.exists(m):
        continue
    a = collections.defaultdict(float)
    for r in csv.reader(open(m)):
        if len(r) >= 4:
            try:
                a[r[2].strip()] += float(r[3])
            except ValueError:
                pass
    w.writerow([os.path.basename(d)] + [a[k] for k in K])
