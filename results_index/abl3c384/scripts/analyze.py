#!/usr/bin/env python3
"""Pull ext.py rows from yss2/yss3 and build abl3c384.csv + markdown tables (stdout)."""
import json, subprocess, math, csv, collections, sys
OUT = "/Users/daoxuan/Desktop/CuPath/isca27/results_index/abl3c384"
rows = []
for h in ("yss2", "yss3"):
    cmd = ("cd ~/akita-exp-test/v3c384/src/results && "
           "for e in $(awk -F'\\t' '$2==0{print $1}' ../../done.tsv); do echo $e; done | xargs python3 ../../ext.py")
    out = subprocess.run(["ssh", "-o", "ControlMaster=no", h, cmd], capture_output=True, text=True).stdout
    done = subprocess.run(["ssh", "-o", "ControlMaster=no", h, "cat ~/akita-exp-test/v3c384/done.tsv; cat ~/akita-exp-test/v3c384/queue.tsv"], capture_output=True, text=True).stdout
    wall = {}; flags = {}
    for l in done.splitlines():
        p = l.split("\t")
        if len(p) == 4 and p[1].lstrip("-").isdigit(): wall[p[0]] = (int(p[1]), float(p[2]))
        elif len(p) == 5: flags[p[2]] = p[4]
    for l in out.splitlines():
        try: o = json.loads(l)
        except Exception: continue
        o["host"] = h; o["rc"], o["wall_h"] = wall.get(o["exp"], (None, None)); o["flags"] = flags.get(o["exp"], "")
        rows.append(o)
CF = [("mi200", "baseline"), ("mi200+v3complete", "Complete"), ("mi200+v3m1", "M1"), ("mi200+v3m1m2", "M1+M2"),
      ("mi200+v3m1m3", "M1+M3"), ("mi200+m3", "M3"), ("mi200+v3c-nomerge", "C-merge"), ("mi200+v3c-nopack", "C-pack"),
      ("mi200+v3c-allocshared", "C-alloc=shared"), ("mi200+v3c-allocnear", "C-alloc=near")]
name = dict(CF)
R = collections.defaultdict(dict)
for o in rows:
    p = o["exp"].split("_"); o["workload"] = p[2].replace("v3c-", ""); o["config"] = name.get(p[3], p[3])
    if o.get("rc") == 0 and o.get("exit") == 0 and o.get("total_time"):
        R[o["workload"]][o["config"]] = o
W = sorted(R, key=lambda w: w)
def sp(w, c, ref="baseline"):
    a, b = R[w].get(ref), R[w].get(c)
    return a["total_time"] / b["total_time"] if a and b else None
def match(w, c):
    a, b = R[w].get("baseline"), R[w].get(c)
    if not a or not b: return None
    return a["wgs"] == b["wgs"] and abs(a["inst"] - b["inst"]) <= 1e-6 * a["inst"]
def gm(v):
    v = [x for x in v if x]
    return math.exp(sum(map(math.log, v)) / len(v)) if v else None
fmt = lambda x, d=2: "–" if x is None else (f"{x:.{d}f}")
HANG = {("mg-W", "M1+M2")}
hf = lambda w, c, x, d=2: "hang" if (w, c) in HANG else fmt(x, d)
# CSV
keys = ["workload", "config", "exp", "host", "flags", "rc", "wall_h", "total_time", "speedup_vs_baseline", "inst", "wgs", "inst_wg_match_baseline"]
extra = sorted({k for o in rows for k in o if k not in keys and k not in ("exit",)})
with open(f"{OUT}/abl3c384.csv", "w", newline="") as f:
    wr = csv.writer(f); wr.writerow(keys + extra)
    hang = {"workload": "mg-W", "config": "M1+M2", "exp": "1021_abl_v3c-mg-W_mi200+v3m1m2_4235991", "host": "yss2", "flags": "-benchmark npb_mg -mg-class W -bandwidth=24 -switch-buffer-flits=16 -report-cupath-v3-stats", "rc": "hang (L2TLB->IOMMU lost wakeup, pre-existing bug; sim time stuck at 542.614 us; process left running, PID 2415098)"}
    for w in W:
        for c, _ in CF:
            o = R[w].get(name[c] if c in name else c)
            if not o: continue
            o = dict(o); o["speedup_vs_baseline"] = sp(w, o["config"]); o["inst_wg_match_baseline"] = match(w, o["config"])
            wr.writerow([json.dumps(o.get(k)) if isinstance(o.get(k), dict) else o.get(k) for k in keys + extra])
        if w == "mg-W": wr.writerow([hang.get(k, "") for k in keys + extra])
P = lambda *a: print(*a)
P("### T1 Complete vs baseline\n\n| workload | baseline µs | Complete µs | speedup | inst/WG match | busy48 base | busy48 Complete |\n|---|---|---|---|---|---|---|")
for w in W:
    b, c = R[w].get("baseline"), R[w].get("Complete")
    P(f"| {w} | {fmt(b and b['total_time']*1e6,1)} | {fmt(c and c['total_time']*1e6,1)} | {fmt(sp(w,'Complete'))} | {match(w,'Complete')} | {fmt(b and b.get('busy48_frac'))} | {fmt(c and c.get('busy48_frac'))} |")
P(f"| **GMEAN** ({sum(1 for w in W if sp(w,'Complete'))}) | | | **{fmt(gm(sp(w,'Complete') for w in W),3)}** | | | |")
P("\n### T2 cumulative (speedup vs baseline)\n\n| workload | M1 | M1+M2 | Complete | M1+M3 | M3 |\n|---|---|---|---|---|---|")
for w in W: P(f"| {w} | " + " | ".join(hf(w, c, sp(w, c)) for c in ("M1", "M1+M2", "Complete", "M1+M3", "M3")) + " |")
full = [w for w in W if all(sp(w, c) for c in ("M1", "M1+M2", "Complete", "M1+M3", "M3"))]
P(f"| **GMEAN** ({len(full)} with all 5) | " + " | ".join(fmt(gm(sp(w, c) for w in full), 3) for c in ("M1", "M1+M2", "Complete", "M1+M3", "M3")) + " |")
P("\n### T3 leave-one-out: slowdown of Complete when one mechanism is removed (T_without / T_Complete; >1 = the mechanism helps)\n\n| workload | −M1 (= M3 only) | −M2 (= M1+M3) | −M3 (= M1+M2) |\n|---|---|---|---|")
loo = {"−M1": "M3", "−M2": "M1+M3", "−M3": "M1+M2"}
def loss(w, c):
    a, b = R[w].get(c), R[w].get("Complete")
    return a["total_time"] / b["total_time"] if a and b else None
for w in W: P(f"| {w} | " + " | ".join(hf(w, c, loss(w, c), 3) for c in loo.values()) + " |")
P("| **GMEAN** | " + " | ".join(fmt(gm(loss(w, c) for w in W), 3) for c in loo.values()) + " |")
P("\n### T4 M1 internal (T_variant / T_Complete; >1 = removing / changing hurts)\n\n| workload | −merge | −pack | alloc shared | alloc near |\n|---|---|---|---|---|")
M1I = ["C-merge", "C-pack", "C-alloc=shared", "C-alloc=near"]
for w in W: P(f"| {w} | " + " | ".join(fmt(loss(w, c), 3) for c in M1I) + " |")
P("| **GMEAN** | " + " | ".join(fmt(gm(loss(w, c) for w in W), 3) for c in M1I) + " |")
P("\n### T5 largest contributor (largest T_without/T_Complete among merge, pack, alloc(rtt vs shared), M2, M3)\n\n| workload | merge | pack | alloc | M2 | M3 | largest | M1 as a whole (−M1) |\n|---|---|---|---|---|---|---|---|")
comp = {"merge": "C-merge", "pack": "C-pack", "alloc": "C-alloc=shared", "M2": "M1+M3", "M3": "M1+M2"}
lead = collections.Counter()
for w in W:
    v = {k: loss(w, c) for k, c in comp.items()}
    vv = {k: x for k, x in v.items() if x}
    big = max(vv, key=vv.get) if vv else None
    if big and vv[big] < 1.02: big = "none (<2%)"
    lead[big] += 1
    P(f"| {w} | " + " | ".join(fmt(v[k], 3) for k in comp) + f" | {big} | {fmt(loss(w,'M3'),3)} |")
P("\nlargest-contributor counts:", dict(lead))
P("\nrows:", len(rows), "valid:", sum(len(v) for v in R.values()), "nonzero:", [o["exp"] for o in rows if o.get("rc") not in (0, None)])
P("mismatch:", [(w, c) for w in W for c in R[w] if c != "baseline" and match(w, c) is False])
json.dump(rows, open(f"{OUT}/.rows.json", "w"))
