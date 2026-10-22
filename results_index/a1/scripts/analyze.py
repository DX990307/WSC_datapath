#!/usr/bin/env python3
"""Markdown tables from a1.csv. usage: analyze.py a1.csv"""
import csv, math, sys
rows = list(csv.DictReader(open(sys.argv[1])))
CF = ["v4", "v4+A1", "v4+ft2", "v4+A1+ft1", "v4+A1+ft2", "v4+A1+reset", "v4+A1+reset+ft2", "v4+A1+reset+ft1"]
W = sorted({r["workload"] for r in rows})
get = {(r["workload"], r["config"]): r for r in rows}
def f(x):
    try: return float(x)
    except: return float("nan")
def ok(w, c):
    r = get.get((w, c)); return r is not None and r["exit"] == "0" and f(r["total_time"]) == f(r["total_time"])
print("### 相对 v4_complete 的加速比（total_time 之比；`–` = 没跑或没跑完，`×` = 非 0 退出）\n")
print("| workload | v4 对 baseline | " + " | ".join(CF[1:]) + " |")
print("|---" * (len(CF) + 1) + "|")
for w in W:
    cells = []
    for c in CF[1:]:
        r = get.get((w, c))
        if r is None: cells.append("–")
        elif r["exit"] not in ("0",): cells.append("×" if r["exit"] else "–")
        else: cells.append(f"{f(r['speedup_vs_v4']):.3f}")
    v = get.get((w, "v4"))
    vb = f"{f(v['speedup_vs_baseline']):.2f}" if v and v["exit"] == "0" and f(v["speedup_vs_baseline"]) == f(v["speedup_vs_baseline"]) else "–"
    print(f"| {w} | {vb} | " + " | ".join(cells) + " |")
print()
print("### GMEAN（只取该配置和 v4、baseline 都完成的 workload）\n")
print("| 配置 | n | 对 v4 | 对 baseline |\n|---|---|---|---|")
for c in CF:
    ws = [w for w in W if ok(w, c) and ok(w, "v4") and ok(w, "baseline")]
    if not ws: print(f"| {c} | 0 | – | – |"); continue
    g = lambda k: math.exp(sum(math.log(f(get[(w, c)][k])) for w in ws) / len(ws))
    print(f"| {c} | {len(ws)} | {g('speedup_vs_v4'):.4f} | {g('speedup_vs_baseline'):.3f} |")
print()
print("### 网络 line（remote_wire_lines）相对 v4\n")
print("| workload | " + " | ".join(CF[1:]) + " |")
print("|---" * len(CF) + "|")
for w in W:
    cells = [f"{f(get[(w, c)]['wire_lines_vs_v4']):.3f}" if ok(w, c) else "–" for c in CF[1:]]
    print(f"| {w} | " + " | ".join(cells) + " |")
print()
print("### A1 边界统计（v4+A1）\n")
print("| workload | 纪元数 | 边界检查的副本行 | 失效 | 保留 | 跨 kernel 命中 | 保留后被用到的行 | 每 home 每纪元最多写的页 | 每纪元最多写的页（全 wafer） |")
print("|---|---|---|---|---|---|---|---|---|")
for w in W:
    r = get.get((w, "v4+A1"))
    if not r or r["exit"] != "0": continue
    inv, ret = f(r["a1_lines_invalidated"]), f(r["a1_lines_retained"])
    print(f"| {w} | {int(f(r['a1_epochs']))} | {int(inv+ret)} | {int(inv)} | {int(ret)} | {int(f(r['a1_retained_hits']))} | "
          f"{int(f(r['a1_retained_lines_used']))} | {int(f(r['a1_max_regions_owner_epoch']))} | {int(f(r['a1_max_regions_epoch']))} |")
