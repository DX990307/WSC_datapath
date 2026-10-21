#!/usr/bin/env python3
"""Pull v3m2w results from yss2/yss3 and build m2w.csv (+ obs_raw/).

- New runs: ext.py on every run with done.tsv rc == 0 (ext.py also reads
  meta.json exit_code; rows with exit != 0 are dropped from the speedup set).
- obs: akkalat/obs_collect.py on the FULL-stat runs (exit 0) -> obs_raw/<host>/.
- w64b384: rows of ../abl3c384/abl3c384.csv for the same 6 workloads and 4
  configs, obs from ../abl3c384/obs_raw/.
"""
import csv, json, os, subprocess, collections

OUT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PREV = os.path.join(os.path.dirname(OUT), "abl3c384")
WL = ["pr-g24", "npbcg-W", "sssp-s18e4-r1", "bfs-s18e2", "ar-pc1", "cfd-c172k-b128"]
CFGN = {"mi200": "baseline", "mi200+v3complete": "Complete", "mi200+v3m1m3": "M1+M3",
        "mi200+v3c-allocshared": "alloc_shared"}
COND = {"w64b384": (64, 384), "w256b384": (256, 384), "w2048b384": (2048, 384), "w64b48": (64, 48)}


def ssh(h, cmd):
    return subprocess.run(["ssh", "-o", "ControlMaster=no", h, cmd], capture_output=True, text=True).stdout


rows = []
hangs = {}
for h in ("yss2", "yss3"):
    D = "~/akita-exp-test/v3m2w"
    done = {}
    for l in ssh(h, f"cat {D}/done.tsv 2>/dev/null").splitlines():
        p = l.split("\t")
        if len(p) >= 3:
            done[p[0]] = (int(p[1]), float(p[2]))
    for l in ssh(h, f"cat {D}/hangs.tsv 2>/dev/null").splitlines():
        p = l.split("\t")
        hangs[p[0]] = l
    flags = {}
    for l in ssh(h, f"cat {D}/queue.tsv").splitlines():
        p = l.split("\t")
        if len(p) == 5:
            flags[p[2]] = p[4]
    ok = [e for e, (rc, _) in done.items() if rc == 0]
    ext = {}
    if ok:
        out = ssh(h, f"cd {D}/src/results && python3 ../../ext.py " + " ".join(ok))
        for l in out.splitlines():
            try:
                o = json.loads(l)
                ext[o["exp"]] = o
            except Exception:
                pass
    full = [e for e in ok if "-report-obs-all" in flags.get(e, "")]
    if full:
        ssh(h, f"cd {D}/src && rm -rf ../obs && python3 akkalat/obs_collect.py ../obs "
               + " ".join("results/" + e for e in full) + " >/dev/null 2>&1")
        os.makedirs(f"{OUT}/obs_raw/{h}", exist_ok=True)
        subprocess.run(["scp", "-q", "-o", "ControlMaster=no", f"{h}:akita-exp-test/v3m2w/obs/*.csv", f"{OUT}/obs_raw/{h}/"])
    for e, (rc, wall) in done.items():
        o = dict(ext.get(e, {}))
        o.update(exp=e, host=h, rc=rc, wall_h=wall, flags=flags.get(e, ""))
        if e in hangs:
            o["rc"] = f"hang ({rc})"
        rows.append(o)
    # runs still running / queued are not listed

for o in rows:
    p = o["exp"].split("_")
    it = p[2][len("m2w-"):]
    o["cond"], o["workload"] = it.split("-", 1)
    o["config"] = CFGN[p[3]]
# w256b48 was cancelled by the user (2026-10-21 12:03); its runs stay on the
# servers but are not part of m2w.csv.
rows = [o for o in rows if o["cond"] != "w256b48"]

# previous campaign (w64b384)
for r in csv.DictReader(open(f"{PREV}/abl3c384.csv")):
    if r["workload"] in WL and r["config"] in ("baseline", "Complete", "M1+M3", "C-alloc=shared"):
        o = {k: v for k, v in r.items()}
        o["config"] = "alloc_shared" if r["config"] == "C-alloc=shared" else r["config"]
        o["cond"] = "w64b384"
        o["exit"] = 0 if r["rc"] == "0" else None
        o["win_stat"] = 64; o["table_stat"] = 128 if o["config"] != "baseline" else ""
        for k in ("total_time", "inst", "wgs"):
            o[k] = float(o[k]) if o.get(k) else None
        rows.append(o)


# obs per (cond, workload, config)
def obs_load(path, cond_fn):
    out = collections.defaultdict(dict)
    for name in ("links", "owner_shift", "sharing"):
        for f in path:
            fn = os.path.join(f, name + ".csv")
            if not os.path.exists(fn):
                continue
            for r in csv.DictReader(open(fn)):
                key = cond_fn(r)
                if key:
                    out[key].update({f"{name}:{k}": v for k, v in r.items() if k not in ("workload", "config")})
    # occupancy: mean over GPMs
    for f in path:
        fn = os.path.join(f, "occupancy.csv")
        if not os.path.exists(fn):
            continue
        acc = collections.defaultdict(list)
        for r in csv.DictReader(open(fn)):
            key = cond_fn(r)
            if key:
                acc[key].append(r)
        for key, rs in acc.items():
            d = out[key]
            for c in ("frac_time_full", "mean_used", "mean_waiting", "remote_bw", "max_used", "limit"):
                v = [float(r[c]) for r in rs if r.get(c) not in (None, "")]
                d["occ:" + c] = sum(v) / len(v) if v else None
            d["occ:max_used_max"] = max(float(r["max_used"]) for r in rs)
    # latency segments: weight by n over hops
    for f in path:
        fn = os.path.join(f, "latency_segments.csv")
        if not os.path.exists(fn):
            continue
        acc = collections.defaultdict(lambda: collections.defaultdict(float))
        for r in csv.DictReader(open(fn)):
            key = cond_fn(r)
            if not key:
                continue
            a = acc[key]
            for c in ("n", "n_net", "n_owner_merged", "n_multicast_leaf", "n_local", "n_req_merged_inflight"):
                a[c] += float(r[c] or 0)
            for seg in ("wait_rrt", "owner_service_merged", "owner_rsp_wait", "ret_net", "rtt", "total", "merge_wait", "owner_queue"):
                n = float(r.get(seg + "_n") or 0)
                a[seg + "_sum"] += n * float(r.get(seg + "_mean") or 0)
                a[seg + "_n"] += n
                a[seg + "_p95max"] = max(a[seg + "_p95max"], float(r.get(seg + "_p95") or 0))
        for key, a in acc.items():
            d = out[key]
            for c in ("n", "n_net", "n_owner_merged", "n_multicast_leaf", "n_local", "n_req_merged_inflight"):
                d["lat:" + c] = a[c]
            for seg in ("wait_rrt", "owner_service_merged", "owner_rsp_wait", "ret_net", "rtt", "total", "merge_wait", "owner_queue"):
                d[f"lat:{seg}_mean"] = a[seg + "_sum"] / a[seg + "_n"] if a[seg + "_n"] else None
                d[f"lat:{seg}_n"] = a[seg + "_n"]
                d[f"lat:{seg}_p95max"] = a[seg + "_p95max"]
    return out


def new_key(r):
    w = r["workload"]
    if not w.startswith("m2w-"):
        return None
    cond, wl = w[4:].split("-", 1)
    if cond == "w256b48":
        return None
    return (cond, wl, CFGN.get(r["config"]))


def old_key(r):
    w = r["workload"].replace("v3c-", "")
    if w not in WL:
        return None
    return ("w64b384", w, CFGN.get(r["config"]))


obs = obs_load([f"{OUT}/obs_raw/yss2", f"{OUT}/obs_raw/yss3"], new_key)
obs.update(obs_load([f"{PREV}/obs_raw/yss2", f"{PREV}/obs_raw/yss3"], old_key))
for o in rows:
    o.update(obs.get((o["cond"], o["workload"], o["config"]), {}))
    o["W"], o["bw_GBps"] = COND[o["cond"]]

# speedup + inst/WG match
B = {(o["cond"], o["workload"]): o for o in rows if o["config"] == "baseline" and o.get("exit") == 0 and o.get("total_time")}
for o in rows:
    b = B.get((o["cond"], o["workload"]))
    if b and o.get("exit") == 0 and o.get("total_time"):
        o["speedup_vs_baseline"] = float(b["total_time"]) / float(o["total_time"])
        o["inst_wg_match_baseline"] = (float(b["wgs"]) == float(o["wgs"]) and abs(float(b["inst"]) - float(o["inst"])) <= 1e-6 * float(b["inst"]))

first = ["cond", "W", "bw_GBps", "workload", "config", "exp", "host", "rc", "exit", "wall_h", "total_time",
         "speedup_vs_baseline", "inst", "wgs", "inst_wg_match_baseline", "win_stat", "table_stat", "flags"]
keys = first + sorted({k for o in rows for k in o} - set(first) - {"err", "hop_sent"})
order = {c: i for i, c in enumerate(COND)}
corder = {"baseline": 0, "M1+M3": 1, "Complete": 2, "alloc_shared": 3}
rows.sort(key=lambda o: (order[o["cond"]], WL.index(o["workload"]), corder[o["config"]]))
with open(f"{OUT}/m2w.csv", "w", newline="") as f:
    w = csv.DictWriter(f, fieldnames=keys, extrasaction="ignore")
    w.writeheader()
    for o in rows:
        w.writerow(o)
print(len(rows), "rows;", sum(1 for o in rows if o["cond"] != "w64b384"), "new; hangs:", list(hangs))
