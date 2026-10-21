#!/usr/bin/env python3
"""Collect the pm campaign: run ext_pm.py on both servers, join with the
reused baseline / Complete runs of abl3c384 (same binary family, same flags),
write ../pm.csv. Only runs with exit code 0 are used for numbers."""
import csv, json, os, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "pm.csv")
ABL = os.path.join(HERE, "..", "..", "abl3c384", "abl3c384.csv")
MECH = os.path.join(HERE, "..", "..", "abl3c384", "mechanism_stats.csv")
REMOTE = "~/akita-exp-test/pm"

rows = []
for host in ("yss2", "yss3"):
    subprocess.run(["ssh", host, f"cat > {REMOTE}/ext_pm.py"],
                   stdin=open(os.path.join(HERE, "ext_pm.py")), check=True)
    out = subprocess.run(
        ["ssh", host, f"cd {REMOTE}/src/results && ls -d 1021_abl_pm-* 2>/dev/null | "
         f"xargs -r python3 {REMOTE}/ext_pm.py"],
        capture_output=True, text=True, check=True).stdout
    for l in out.splitlines():
        o = json.loads(l)
        o["host_dir"] = host
        rows.append(o)

base, cplt = {}, {}
for r in csv.DictReader(open(ABL)):
    if r["config"] == "baseline":
        base[r["workload"]] = r
    elif r["config"] == "Complete":
        cplt[r["workload"]] = r
mech = {r["workload"]: r for r in csv.DictReader(open(MECH))} if os.path.exists(MECH) else {}

PM_KEYS = ["pm_faults", "pm_counter_triggers", "pm_batches", "pm_migrations",
           "pm_noop_migrations", "pm_pages_migrated", "pm_bytes_migrated",
           "pm_pingpong", "pm_pinned_units", "pm_shootdowns",
           "pm_tlb_entries_invalidated", "pm_held_accesses", "pm_remote_accesses",
           "pm_remote_reads", "pm_units_touched", "pm_avg_migration_time",
           "pm_avg_blocked_time", "pm_avg_drain_wait", "pm_avg_copy_time",
           "pm_dma_pulled_bytes", "pm_hot_owner_read_share", "pm_hot_owner_device",
           "pm_sw_latency_ns", "pm_shootdown_ns", "pm_threshold", "pm_unit_bytes",
           "pm_max_migrations", "pm_cooldown_ns"]
out = []
for o in rows:
    e = o["exp"]
    # 1021_abl_pm-<item>_<base>+<tag>_<commit>
    _, _, item, cfg, commit = e.split("_")
    item = item[len("pm-"):]
    b, tag = cfg.split("+", 1)
    d = dict(workload=item, base="Complete" if b == "v3complete" else "baseline",
             variant=tag, exp=e, host=o.get("host") or o["host_dir"], rc=o.get("exit"),
             commit=commit)
    ok = o.get("exit") == 0 and o.get("total_time")
    d["total_time"] = o.get("total_time") if ok else None
    bt = float(base[item]["total_time"]) if item in base else None
    ct = float(cplt[item]["total_time"]) if item in cplt else None
    d["t_baseline"], d["t_complete"] = bt, ct
    d["speedup_vs_baseline"] = bt / d["total_time"] if ok and bt else None
    d["speedup_vs_complete"] = ct / d["total_time"] if ok and ct else None
    if ok and item in base:
        bi, bw = float(base[item]["inst"]), float(base[item]["wgs"])
        d["inst_wg_match_baseline"] = (bw == o["wgs"] and abs(bi - o["inst"]) <= 1e-6 * bi)
    for k in PM_KEYS:
        d[k] = o.get(k)
    for k in ("owner_top1", "owner_top1_share", "owner_top3_share"):
        d[k] = o.get(k)
    d["base_top_dest_share_abl3c384"] = mech.get(item, {}).get("base_top_dest_share")
    out.append(d)
out.sort(key=lambda d: (d["workload"], d["base"], d["variant"]))
keys = list(out[0].keys()) if out else []
for d in out:
    for k in d:
        if k not in keys:
            keys.append(k)
with open(OUT, "w", newline="") as fh:
    w = csv.DictWriter(fh, fieldnames=keys)
    w.writeheader()
    w.writerows(out)
print(f"{len(out)} rows -> {OUT}; exit0 {sum(1 for d in out if d['rc'] == 0)}")
