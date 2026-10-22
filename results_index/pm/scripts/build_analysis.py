#!/usr/bin/env python3
"""analysis.md = analysis_head.md (with summary numbers filled in) + analyze.py tables."""
import csv, math, os, subprocess
HERE = os.path.dirname(os.path.abspath(__file__))
rows = list(csv.DictReader(open(os.path.join(HERE, "..", "pm.csv"))))
by = {(r["workload"], r["base"], r["variant"]): r for r in rows}
W = sorted({r["workload"] for r in rows})
def gm(xs):
    xs = [x for x in xs if x]
    return math.exp(sum(map(math.log, xs)) / len(xs)), len(xs)
def vc(w, v):
    r = by.get((w, "Complete", v))
    return float(r["speedup_vs_complete"]) if r and r["status"] == "ok" else None
c4k, n = gm([vc(w, "pmc4k") for w in W])
f4k, _ = gm([vc(w, "pmf4k") for w in W])
mx = []
for w in W:
    b = by[(w, "baseline", "pmf4k")]
    c = float(b["t_baseline"]) / float(b["t_complete"])
    mx.append(max([c] + [float(by[(w, "Complete", v)]["speedup_vs_baseline"]) for v in ("pmf4k", "pmc4k")
                         if by.get((w, "Complete", v)) and by[(w, "Complete", v)]["status"] == "ok"]))
m, _ = gm(mx)
head = open(os.path.join(HERE, "analysis_head.md")).read()
head = (head.replace("COMPLC4KN", str(n)).replace("COMPLC4K", f"{c4k:.2f}")
        .replace("COMPLF4K", f"{f4k:.2f}").replace("MAXCOMPL", f"{m:.2f}"))
tables = subprocess.run(["python3", os.path.join(HERE, "analyze.py")], capture_output=True, text=True, check=True).stdout
open(os.path.join(HERE, "..", "analysis.md"), "w").write(head + tables)
print(f"Complete+c4k {c4k:.3f} (n={n}), Complete+f4k {f4k:.3f}, max {m:.3f}")
