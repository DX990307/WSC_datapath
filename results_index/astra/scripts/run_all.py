"""Run every runs/*/spec.json on this server (P18). Max JOBS at once; a run
starts only if MemAvailable minus its estimated footprint stays above 30 GB.
Each run: gen_et -> ASTRA-sim -> drop et/, gzip msgs.csv. Log in run_all.log."""
import glob, json, os, subprocess, time
H = os.path.expanduser("~/astrasim-test/p18")
JOBS, RESERVE_KB = 8, 30 * 1024 * 1024
NET = {"b384": "configs/mesh7_b384.yml", "b48": "configs/mesh7_b48.yml"}
EP = "115"


def avail_kb():
    for l in open("/proc/meminfo"):
        if l.startswith("MemAvailable"):
            return int(l.split()[1])


def log(msg):
    with open(f"{H}/run_all.log", "a") as f:
        f.write(time.strftime("%H:%M:%S ") + msg + "\n")


def todo():
    out = []
    for d in sorted(glob.glob(f"{H}/runs/*/")):
        if os.path.exists(d + "done") or not os.path.exists(d + "meta.json"):
            continue
        spec = json.load(open(d + "spec.json"))
        n = sum(len(v) for v in spec["seq"].values())
        out.append((n, d))
    return sorted(out)  # small first


def cmd(d):
    bw = json.load(open(d + "meta.json"))["bw"]
    return (f"cd {H} && ../venv/bin/python scripts/gen_et.py {d}spec.json {d}et/wl > {d}gen.log 2>&1 && "
            f"scripts/run_one.sh {d} {NET[bw]} {EP} > {d}run.log 2>&1; rc=$?; "
            f"rm -rf {d}et; gzip -f {d}msgs.csv; echo $rc > {d}done")


running = {}
queue = todo()
log(f"start {len(queue)} runs")
while queue or running:
    for d, p in list(running.items()):
        if p.poll() is not None:
            log(f"end {os.path.basename(d[:-1])} rc={open(d + 'done').read().strip() if os.path.exists(d + 'done') else '?'}")
            del running[d]
    while queue and len(running) < JOBS:
        n, d = queue[0]
        est_kb = 1024 * 1024 + n * 6  # ~6 KB per request + 1 GB
        if avail_kb() - est_kb < RESERVE_KB:
            break
        queue.pop(0)
        running[d] = subprocess.Popen(["nice", "-n", "10", "bash", "-c", cmd(d)])
        log(f"launch {os.path.basename(d[:-1])} n={n} pid={running[d].pid}")
    time.sleep(5)
log("all done")
