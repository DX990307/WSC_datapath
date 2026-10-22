#!/usr/bin/env python3
"""Markdown tables for analysis.md from ../pm.csv (exit-0 runs only)."""
import csv, math, os, sys, collections

HERE = os.path.dirname(os.path.abspath(__file__))
rows = [r for r in csv.DictReader(open(os.path.join(HERE, "..", "pm.csv")))]
V = ["pmobs", "pmf4k", "pmf2m", "pmc4k", "pmc2m", "pmf4kideal", "pmf2mideal",
     "pmc4kideal", "pmc2mideal"]
NAME = {"pmobs": "observe", "pmf4k": "fault 4K", "pmf2m": "fault 2M", "pmc4k": "counter 4K",
        "pmc2m": "counter 2M", "pmf4kideal": "fault 4K ideal", "pmf2mideal": "fault 2M ideal",
        "pmc4kideal": "counter 4K ideal", "pmc2mideal": "counter 2M ideal"}
by = {}
for r in rows:
    by[(r["workload"], r["base"], r["variant"])] = r
W = sorted({r["workload"] for r in rows})


def f(x):
    try:
        return float(x)
    except Exception:
        return None


def fmt(x, d=2):
    return "–" if x is None else f"{x:.{d}f}"


def gmean(xs):
    xs = [x for x in xs if x]
    return math.exp(sum(math.log(x) for x in xs) / len(xs)) if xs else None


def sp(w, v, base="baseline", ref="speedup_vs_baseline"):
    r = by.get((w, base, v))
    return f(r[ref]) if r and r["rc"] == "0" else None


out = []
P = out.append
P("### T1 speedup over baseline (T_baseline / T_variant; >1 = faster), Complete for reference\n")
P("| workload | " + " | ".join(NAME[v] for v in V[1:]) + " | Complete | best PM / Complete |")
P("|---" * (len(V) + 2) + "|---|")
best_cols = collections.defaultdict(list)
for w in W:
    vals = [sp(w, v) for v in V[1:]]
    r0 = by.get((w, "baseline", "pmf4k")) or next((r for r in rows if r["workload"] == w), None)
    cs = f(r0["t_baseline"]) / f(r0["t_complete"]) if r0 and f(r0["t_complete"]) else None
    best = max([x for x in vals[:4] if x] or [None]) if any(vals[:4]) else None
    P(f"| {w} | " + " | ".join(fmt(x) for x in vals) + f" | {fmt(cs)} | {fmt(best / cs if best and cs else None)} |")
    for v, x in zip(V[1:], vals):
        best_cols[v].append(x)
    best_cols["Complete"].append(cs)
P("| **GMEAN** | " + " | ".join(fmt(gmean(best_cols[v])) for v in V[1:]) +
  f" | {fmt(gmean(best_cols['Complete']))} | |")
P("")
P("### T2 observe vs reused baseline (timing must be identical)\n")
P("| workload | observe µs | baseline µs (abl3c384) | equal | hot-owner read share (observe) | owner |")
P("|---|---|---|---|---|---|")
for w in W:
    r = by.get((w, "baseline", "pmobs"))
    if not r or r["rc"] != "0":
        P(f"| {w} | – | – | – | – | – |")
        continue
    t, b = f(r["total_time"]), f(r["t_baseline"])
    P(f"| {w} | {fmt(t * 1e6, 3)} | {fmt(b * 1e6, 3)} | {'yes' if abs(t - b) < 1e-12 else 'NO'} | "
      f"{fmt(f(r['owner_top1_share']))} | {r['owner_top1']} |")
P("")
P("### T3 migration activity (4 policies)\n")
P("| workload | variant | migrations | pages | MB moved | ping-pong | pinned | TLB entries inval. | held accesses | avg migr. µs | avg blocked µs | hot-owner share |")
P("|---|---|---|---|---|---|---|---|---|---|---|---|")
for w in W:
    for v in V[1:5]:
        r = by.get((w, "baseline", v))
        if not r or r["rc"] != "0":
            continue
        g = lambda k: f(r[k]) or 0.0
        P(f"| {w} | {NAME[v]} | {g('pm_migrations'):.0f} | {g('pm_pages_migrated'):.0f} | "
          f"{g('pm_bytes_migrated') / 2**20:.1f} | {g('pm_pingpong'):.0f} | {g('pm_pinned_units'):.0f} | "
          f"{g('pm_tlb_entries_invalidated'):.0f} | {g('pm_held_accesses'):.0f} | "
          f"{g('pm_avg_migration_time') * 1e6:.2f} | {g('pm_avg_blocked_time') * 1e6:.2f} | "
          f"{fmt(f(r['owner_top1_share']))} |")
P("")
P("### T4 PM + Complete (T_Complete / T_(Complete+PM); >1 = PM adds on top)\n")
P("| workload | variant | Complete+PM / baseline | Complete / baseline | Complete+PM vs Complete |")
P("|---|---|---|---|---|")
cv = collections.defaultdict(list)
for r in rows:
    if r["base"] != "Complete" or r["rc"] != "0":
        continue
    a, c = f(r["speedup_vs_baseline"]), f(r["speedup_vs_complete"])
    cs = f(r["t_baseline"]) / f(r["t_complete"])
    P(f"| {r['workload']} | {NAME.get(r['variant'], r['variant'])} | {fmt(a)} | {fmt(cs)} | {fmt(c)} |")
    cv[r["variant"]].append(c)
for v, xs in cv.items():
    P(f"| **GMEAN** | {NAME.get(v, v)} | | | {fmt(gmean(xs))} |")
print("\n".join(out))

# T5: remote accesses (L1 misses to remote memory) relative to observe, and
# the share of remote reads served by the busiest owner GPM
o2 = []
o2.append("\n### T5 remote accesses relative to observe (same workload), and hot-owner read share\n")
o2.append("| workload | remote acc. (observe, M) | fault 4K | counter 4K | fault 2M | counter 2M | owner share observe | fault 4K | counter 4K |")
o2.append("|---|---|---|---|---|---|---|---|---|")
for w in W:
    ob = by.get((w, "baseline", "pmobs"))
    if not ob or ob["rc"] != "0":
        continue
    ra = f(ob["pm_remote_accesses"])
    def g(v, k):
        x = by.get((w, "baseline", v))
        return f(x[k]) if x and x["rc"] == "0" else None
    rat = [g(v, "pm_remote_accesses") / ra if g(v, "pm_remote_accesses") is not None else None
           for v in ("pmf4k", "pmc4k", "pmf2m", "pmc2m")]
    sh = [g(v, "owner_top1_share") for v in ("pmobs", "pmf4k", "pmc4k")]
    o2.append(f"| {w} | {ra / 1e6:.2f} | " + " | ".join(fmt(x) for x in rat) + " | " +
              " | ".join(fmt(x) for x in sh) + " |")
# T6: sensitivity
S = [("pmf4k", "fault 4K sw5"), ("pmf4ksw1", "f4K sw1"), ("pmf4ksw2", "f4K sw2"), ("pmf4ksw10", "f4K sw10"),
     ("pmf4ksw20", "f4K sw20"), ("pmf4kideal", "f4K ideal"), ("pmf4kmax1", "f4K max1"),
     ("pmc4k", "counter 4K t256 sw5"), ("pmc4ksw1", "c4K sw1"), ("pmc4ksw2", "c4K sw2"),
     ("pmc4ksw10", "c4K sw10"), ("pmc4ksw20", "c4K sw20"), ("pmc4kideal", "c4K ideal"),
     ("pmc4kt64", "c4K t64"), ("pmc4kt1024", "c4K t1024")]
WS = [w for w in W if any(by.get((w, "baseline", v)) for v, _ in S[1:5] + S[6:7] + S[13:15])]
o2.append("\n### T6 sensitivity (speedup over baseline)\n")
o2.append("| variant | " + " | ".join(WS) + " | GMEAN (all 9 present) |")
o2.append("|---" * (len(WS) + 2) + "|")
for v, nm in S:
    xs = [sp(w, v) for w in WS]
    gm = gmean(xs) if all(xs) else None
    o2.append(f"| {nm} | " + " | ".join(fmt(x) for x in xs) + f" | {fmt(gm)} |")
# hangs
o2.append("\n### Runs that hung (watchdog; known L2TLB->IOMMU lost wakeup)\n")
for r in rows:
    if r.get("status") == "hang":
        o2.append(f"- {r['workload']} {r['base']} {r['variant']} ({r['exp']})")
print("\n".join(o2))
