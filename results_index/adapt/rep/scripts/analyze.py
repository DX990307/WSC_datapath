#!/usr/bin/env python3
"""P21 candidate 1 (oracle region replication): speedup tables.
usage: analyze.py <out dir> <runs tsv> ...   (rows from collect.py: adapt, qt, place results)"""
import csv, math, sys
WL = ["pr-g24", "npbcg-W", "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "lud-n1024", "npbis-n1m-i1",
      "km-g1", "fft2d-n1024", "fw-n2048-p4", "moe-t64", "mt-g4", "pa-b14s7p32",
      "atax-2688x8192-r64", "shocsort-n688k", "bh-n86k", "spmv-g32", "i2c-g64", "gups-L18m8",
      "cfd-c172k-b128", "mg-W", "embbag-b512", "bfs-s18e2", "ar-pc1"]
G = {}
for w in WL[:8]: G[w] = "热点广播"
for w in WL[8:12]: G[w] = "私有放错"
for w in WL[12:14]: G[w] = "流式"
for w in WL[14:20]: G[w] = "随机共享"
for w in WL[20:]: G[w] = "混合"
GROUPS = ["热点广播", "私有放错", "流式", "随机共享", "混合"]
NOREP = {"km-g1", "fft2d-n1024", "fw-n2048-p4", "gups-L18m8", "lud-n1024", "ar-pc1", "shocsort-n688k"}
CFG = [("baseline", ["1022_diag_qt-w128b384-{w}_mi200_9e87ac5"]),
       ("M3+", ["1022_abl_place-w128b384-{w}_mi200+m3p_20f323e", "1022_abl_place-w128b384-{w}_mi200+m3p_810531a",
                "1023_abl_rep-w128b384-{w}_mi200+m3p_f7b83ff"]),
       ("v4m3", ["1022_diag_qt-w128c128b384-{w}_mi200+v4m3_9e87ac5"]),
       ("first-touch+M3+", ["1023_abl_rep-w128b384-{w}_mi200+m3pft_f7b83ff"]),
       ("oracle P-按类+M3+", ["1022_abl_place-w128b384-{w}-cls_mi200+m3p_20f323e"]),
       ("全部本地", ["1022_abl_place-w128b384-{w}-alllocal_mi200_20f323e", "1022_abl_place-w128b384-{w}-alllocal_mi200_810531a"]),
       ("baseline+复制4", ["1023_abl_rep-w128b384-{w}_mi200+rep4_f7b83ff"]),
       ("M3++复制4", ["1023_abl_rep-w128b384-{w}_mi200+m3prep4_f7b83ff"]),
       ("M3++复制9", ["1023_abl_rep-w128b384-{w}_mi200+m3prep9_f7b83ff"]),
       ("v4m3+复制4", ["1023_abl_rep-w128b384-{w}_mi200+v4m3rep4_f7b83ff"]),
       ("first-touch+M3++复制4", ["1023_abl_rep-w128b384-{w}_mi200+m3pftrep4_f7b83ff"])]
# replication configs fall back to the config without replication when the workload has no list
FALLBACK = {"baseline+复制4": "baseline", "M3++复制4": "M3+", "M3++复制9": "M3+", "v4m3+复制4": "v4m3",
            "first-touch+M3++复制4": "first-touch+M3+"}
runs = {}
for f in sys.argv[2:]:
    for r in csv.DictReader(open(f), delimiter="\t"):
        runs[r["exp"]] = r
def gm(xs):
    xs = [x for x in xs if x]
    return math.exp(sum(map(math.log, xs)) / len(xs)) if xs else None
T, R = {}, {}
for w in WL:
    for lab, pats in CFG:
        for p in pats:
            r = runs.get(p.format(w=w))
            if r and r["exit"] == "0" and r["total_time"]:
                T[(w, lab)] = float(r["total_time"]); R[(w, lab)] = r; break
    for lab, base in FALLBACK.items():
        if w in NOREP and (w, lab) not in T and (w, base) in T:
            T[(w, lab)] = T[(w, base)]; R[(w, lab)] = R[(w, base)]
sp, valid = {}, {}
for w in WL:
    b = R.get((w, "baseline"))
    if not b:
        continue
    for lab, _ in CFG:
        if (w, lab) in T:
            sp[(w, lab)] = T[(w, "baseline")] / T[(w, lab)]
            r = R[(w, lab)]
            valid[(w, lab)] = (r["wg"] == b["wg"] and abs(float(r["inst"]) - float(b["inst"])) <= 0.01 * float(b["inst"]))
labs = [l for l, _ in CFG]
out = sys.argv[1]
with open(f"{out}/speedup.csv", "w", newline="") as f:
    wr = csv.writer(f); wr.writerow(["workload", "group"] + labs)
    for w in WL:
        wr.writerow([w, G[w]] + [("%.4f%s" % (sp[(w, l)], "" if valid[(w, l)] else "*")) if (w, l) in sp else "" for l in labs])
def g(lab, over=None, sel=lambda w: True, need=None):
    xs = []
    for w in WL:
        if not sel(w) or (w, lab) not in sp or not valid[(w, lab)]:
            continue
        if need and any((w, n) not in sp for n in need):
            continue
        if over:
            if (w, over) not in sp:
                continue
            xs.append(sp[(w, lab)] / sp[(w, over)])
        else:
            xs.append(sp[(w, lab)])
    return gm(xs), len(xs)
def c(v): return ("%.3f（%d）" % v) if v[0] else "–"
md = ["## 相对 baseline 的 GMEAN（只取有效运行；无可复制页的 7 个 workload 复制配置 = 对应的不复制配置）", "",
      "| 配置 | 全部 | " + " | ".join(GROUPS) + " |", "|---" * (len(GROUPS) + 2) + "|"]
for l in labs:
    md.append(f"| {l} | {c(g(l))} | " + " | ".join(c(g(l, sel=lambda w, G0=G0: G[w] == G0)) for G0 in GROUPS) + " |")
md += ["", "## 复制带来的增益（同一底座，复制 / 不复制）", "", "| 比较 | 全部 | " + " | ".join(GROUPS) + " | 只算有可复制页的 18 个 |",
       "|---" * (len(GROUPS) + 3) + "|"]
for a, b in [("baseline+复制4", "baseline"), ("M3++复制4", "M3+"), ("M3++复制9", "M3+"), ("v4m3+复制4", "v4m3"),
             ("first-touch+M3++复制4", "first-touch+M3+"), ("first-touch+M3+", "M3+"), ("v4m3+复制4", "M3++复制4"),
             ("M3++复制4", "oracle P-按类+M3+"), ("全部本地", "v4m3+复制4")]:
    md.append(f"| {a} / {b} | {c(g(a, b))} | " + " | ".join(c(g(a, b, lambda w, G0=G0: G[w] == G0)) for G0 in GROUPS)
              + f" | {c(g(a, b, lambda w: w not in NOREP))} |")
md += ["", "## 逐 workload（相对 baseline；* = 指令数或 WG 数和 baseline 不符）", "",
       "| workload | 类 | " + " | ".join(labs) + " |", "|---" * (len(labs) + 2) + "|"]
for w in WL:
    md.append(f"| {w} | {G[w]} | " + " | ".join(("%.3f%s" % (sp[(w, l)], "" if valid[(w, l)] else "*")) if (w, l) in sp else "–" for l in labs) + " |")
md += ["", "## 复制统计（M3++复制4）", "", "| workload | 建副本的页 | 读被重定向到副本 | 写折回 | kernel 边界折回 |", "|---|---|---|---|---|"]
for w in WL:
    r = R.get((w, "M3++复制4"))
    if r and "rep4" in r["exp"]:
        md.append(f"| {w} | {r['pa_replica_on']} | {r['pa_replica_reads']} | {r['pa_replica_collapse_write']} | {r['pa_replica_collapse_kernel']} |")
open(f"{out}/tables.md", "w").write("\n".join(md) + "\n")
print("\n".join(md))
