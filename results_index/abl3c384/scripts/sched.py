#!/usr/bin/env python3
"""Main-run scheduler (one per host). Reads queue.tsv each loop:
  pair<TAB>est_rss_gb<TAB>exp_id<TAB>configs<TAB>bench args (space separated)
Both runs of a pair are launched together when
  MemAvailable - outstanding reservations - 2*est*1.3 >= 30 GB.
Reservation of a running sim = max(0, est*1.3 - current RSS).
MemAvailable < 15 GB -> SIGSTOP most recently started running sim of ours;
MemAvailable > 40 GB -> SIGCONT the most recently paused one.
Never kills. PAUSE file stops new launches.
"""
import os, signal, subprocess, sys, time, json

D = os.path.expanduser("~/akita-exp-test/v3c384")
COMMIT = "4235991"
BIN = f"{D}/bin/baseline_{COMMIT}"
F = 1.3
FLOOR = 30.0


def log(msg):
    with open(f"{D}/sched.log", "a") as f:
        f.write(time.strftime("%Y-%m-%dT%H:%M:%S ") + msg + "\n")


def memavail():
    for l in open("/proc/meminfo"):
        if l.startswith("MemAvailable:"):
            return int(l.split()[1]) / 1048576
    return 0.0


def children(pid):
    out = []
    try:
        r = subprocess.run(["ps", "-eo", "pid,ppid,args"], capture_output=True, text=True).stdout
    except Exception:
        return out
    procs = []
    for l in r.splitlines()[1:]:
        p = l.split(None, 2)
        if len(p) >= 2:
            procs.append((int(p[0]), int(p[1]), p[2] if len(p) > 2 else ""))
    frontier = [pid]
    while frontier:
        q = frontier.pop()
        for c, pp, a in procs:
            if pp == q:
                out.append((c, a))
                frontier.append(c)
    return out


def rss_gb(pid):
    try:
        for l in open(f"/proc/{pid}/status"):
            if l.startswith("VmRSS:"):
                return int(l.split()[1]) / 1048576
    except Exception:
        pass
    return 0.0


def read_queue():
    q = []
    if not os.path.exists(f"{D}/queue.tsv"):
        return q
    for l in open(f"{D}/queue.tsv"):
        l = l.rstrip("\n")
        if not l or l.startswith("#"):
            continue
        p = l.split("\t")
        q.append(dict(pair=p[0], est=float(p[1]), exp=p[2], cfg=p[3], args=p[4].split()))
    return q


running = {}  # exp -> dict(proc, sim, est, start, paused)
done = set()
if os.path.exists(f"{D}/done.tsv"):
    for l in open(f"{D}/done.tsv"):
        done.add(l.split("\t")[0])
launched = set()
if os.path.exists(f"{D}/launched.tsv"):
    for l in open(f"{D}/launched.tsv"):
        launched.add(l.split("\t")[0])


def launch(r):
    os.makedirs(f"{D}/runs", exist_ok=True)
    lf = open(f"{D}/runs/{r['exp']}.log", "w")
    env = dict(os.environ, ISCA27_BINDIR=f"{D}/bin")
    cmd = ["/usr/bin/time", "-v", "akkalat/isca27_run.sh", r["exp"], COMMIT, "V3C384", r["cfg"]] + r["args"]
    p = subprocess.Popen(cmd, cwd=f"{D}/src", stdout=lf, stderr=subprocess.STDOUT,
                         env=env, start_new_session=True)
    running[r["exp"]] = dict(proc=p, sim=None, est=r["est"], start=time.time(), paused=False)
    launched.add(r["exp"])
    with open(f"{D}/launched.tsv", "a") as f:
        f.write(f"{r['exp']}\t{p.pid}\t{time.strftime('%Y-%m-%dT%H:%M:%S')}\n")
    with open(f"{D}/pids.txt", "a") as f:
        f.write(f"{p.pid} wrapper {r['exp']} {time.strftime('%Y-%m-%dT%H:%M:%S')}\n")
    log(f"launch {r['exp']} pid={p.pid} est={r['est']}")


with open(f"{D}/sched.pid", "w") as f:
    f.write(str(os.getpid()))
log("scheduler start")
while True:
    # reap / find sim pids
    for exp, st in list(running.items()):
        rc = st["proc"].poll()
        if rc is not None:
            el = (time.time() - st["start"]) / 3600
            with open(f"{D}/done.tsv", "a") as f:
                f.write(f"{exp}\t{rc}\t{el:.2f}\t{time.strftime('%Y-%m-%dT%H:%M:%S')}\n")
            log(f"done {exp} rc={rc} wall={el:.2f}h")
            done.add(exp)
            del running[exp]
            continue
        if time.time() - st["start"] > 3 * 3600 and not st.get("warned"):
            st["warned"] = True
            log(f"LONG {exp} wall>3h (not killed)")
        if st["sim"] is None:
            for c, a in children(st["proc"].pid):
                if BIN in a or f"baseline_{COMMIT}" in a.split()[0]:
                    st["sim"] = c
                    with open(f"{D}/pids.txt", "a") as f:
                        f.write(f"{c} sim {exp} {time.strftime('%Y-%m-%dT%H:%M:%S')}\n")
                    break
    ma = memavail()
    # memory guard
    if ma < 15:
        cand = [(st["start"], e) for e, st in running.items() if st["sim"] and not st["paused"]]
        if cand:
            _, e = max(cand)
            try:
                os.kill(running[e]["sim"], signal.SIGSTOP)
                running[e]["paused"] = True
                log(f"SIGSTOP {e} sim={running[e]['sim']} memavail={ma:.1f}")
            except Exception as ex:
                log(f"SIGSTOP failed {e}: {ex}")
    elif ma > 40:
        cand = [(st["start"], e) for e, st in running.items() if st["paused"]]
        if cand:
            _, e = max(cand)
            try:
                os.kill(running[e]["sim"], signal.SIGCONT)
                running[e]["paused"] = False
                log(f"SIGCONT {e} memavail={ma:.1f}")
            except Exception as ex:
                log(f"SIGCONT failed {e}: {ex}")
    # launch
    if not os.path.exists(f"{D}/PAUSE") and not any(st["paused"] for st in running.values()):
        resv = sum(max(0.0, st["est"] * F - (rss_gb(st["sim"]) if st["sim"] else 0.0))
                   for st in running.values())
        q = [r for r in read_queue() if r["exp"] not in launched and r["exp"] not in done]
        pairs = []
        for r in q:
            if pairs and pairs[-1][0]["pair"] == r["pair"]:
                pairs[-1].append(r)
            else:
                pairs.append([r])
        for pr in pairs:
            need = sum(r["est"] * F for r in pr)
            if ma - resv - need >= FLOOR:
                for r in pr:
                    launch(r)
                resv += need
                time.sleep(2)
            else:
                break  # keep longest-first order
    with open(f"{D}/status.json", "w") as f:
        json.dump(dict(t=time.strftime('%Y-%m-%dT%H:%M:%S'), memavail=round(ma, 1),
                       running={e: dict(sim=st["sim"], paused=st["paused"],
                                        rss=round(rss_gb(st["sim"]), 2) if st["sim"] else 0)
                                for e, st in running.items()}), f, indent=1)
    if not running and not [r for r in read_queue() if r["exp"] not in launched] and not os.path.exists(f"{D}/HOLD"):
        log("queue empty, exiting")
        break
    time.sleep(30)
