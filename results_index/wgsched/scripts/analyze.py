#!/usr/bin/env python3
"""wg-to-data: table from all.tsv (collect.py output) + map summaries.
Usage: analyze.py all.tsv maps_dir class_mix.csv"""
import csv, math, re, sys, os, collections

allp, mapsd, mixp = sys.argv[1:4]
T = {}
for l in open(allp):
    sub, e, host, rc, tt = (l.rstrip("\n").split("\t") + [""] * 5)[:5]
    if rc != "0" or not tt:
        continue
    T[(sub, e)] = float(tt)


def get(sub, pat):
    for (s, e), v in T.items():
        if s == sub and re.fullmatch(pat, e):
            return v
    return None


cls = {}
for r in csv.DictReader(open(mixp)):
    cls[r["workload"]] = r["top_class"] + ("*" if r["mixed"] == "1" else "")

WLS = [l.split("\t")[0] for l in open(os.path.join(os.path.dirname(allp), "wl_order.txt")).read().split()] if False else None
wls = sorted({re.match(r"1022_place_wgs-(?:p1-)?(.+?)_mi200", e).group(1) for (s, e) in T if s == "wgsched"})
C = "a739ac3"
GS = {}
gp = os.path.join(os.path.dirname(allp), "guard_status.txt")
if os.path.exists(gp):
    for l in open(gp):
        a = l.split()
        if len(a) == 4: GS[a[0]] = a[3]
rows = []
for w in wls:
    r = dict(wl=w, cls=cls.get(w, ""))
    r["p1"] = get("wgsched", rf"1022_place_wgs-p1-{w}_mi200\+wgaff_{C}")
    r["base"] = get("qt", rf"1022_diag_qt-w128b384-{w}_mi200_9e87ac5")
    r["v4m3"] = get("qt", rf"1022_diag_qt-w128c128b384-{w}_mi200\+v4m3_9e87ac5")
    r["wg"] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+wgmap_{C}")
    r["v4m3wg"] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+v4m3\+wgmap_{C}")
    for t in ("wgbal", "wgshuf", "wgmapg"):
        r[t] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+{t}_{C}")
        r["v4" + t] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+v4m3\+{t}_{C}")
    st = GS.get(w)
    if st == "none":
        r["wgmapg"] = r["base"]; r["v4wgmapg"] = r["v4m3"]
    elif st == "same":
        r["wgmapg"] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+wgmap_{C}")
        r["v4wgmapg"] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+v4m3\+wgmap_{C}")
    r["m3p"] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+m3p_{C}")
    r["m3pwg"] = get("wgsched", rf"1022_place_wgs-{w}_mi200\+m3p\+wgmap_{C}")
    r["pg"] = get("place", rf"1022_abl_place-w128b384-{w}-pg_mi200_20f323e")
    r["sb"] = get("place", rf"1022_abl_place-w128b384-{w}-sb_mi200_20f323e")
    r["cls_o"] = get("place", rf"1022_abl_place-w128b384-{w}-cls_mi200_20f323e")
    r["cls_v4m3"] = get("place", rf"1022_abl_place-w128b384-{w}-cls_mi200\+v4m3_20f323e")
    r["cls_m3p"] = get("place", rf"1022_abl_place-w128b384-{w}-cls_mi200\+m3p_20f323e")
    r["local"] = get("place", rf"1022_abl_place-w128b384-{w}-alllocal_mi200_(20f323e|810531a)")
    sp = os.path.join(mapsd, f"{w}.map.summary.csv")
    if os.path.exists(sp):
        ks = list(csv.DictReader(open(sp)))
        tb = sum(float(k["bytes"] or 0) for k in ks)
        if tb:
            r["loc_d"] = sum(float(k["bytes"]) * float(k["local_default"] or 0) for k in ks) / tb
            r["loc_m"] = sum(float(k["bytes"]) * float(k["local_map"] or 0) for k in ks) / tb
        nw = sum(int(k["total_wg"]) for k in ks)
        r["mx_d"] = max(float(k["max_over_avg_default"]) for k in ks)
        r["mx_m"] = max(float(k["max_over_avg_map"]) for k in ks)
        r["mxw_d"] = sum(float(k["max_over_avg_default"]) * int(k["total_wg"]) for k in ks) / nw
        r["mxw_m"] = sum(float(k["max_over_avg_map"]) * int(k["total_wg"]) for k in ks) / nw
        r["nk"] = len(ks)
    rows.append(r)


def sp(a, b):
    return a / b if a and b else None


def f(x, d=2):
    return "" if x is None else f"{x:.{d}f}"


def gm(xs):
    xs = [x for x in xs if x]
    return math.exp(sum(map(math.log, xs)) / len(xs)) if xs else None


hdr = ["workload", "类别", "K", "本地字节 默认→映射", "WG/GPM max/avg 默认→映射(加权)",
       "p1/base", "WG÷base", "只均衡÷base", "随机均衡÷base", "WG保护÷base", "v4m3÷base", "v4m3+WG÷base", "v4m3+WG÷v4m3", "v4m3+只均衡÷v4m3", "v4m3+WG保护÷v4m3", "M3+÷base", "M3+WG÷M3+",
       "P-整页", "P-子块", "P-按类", "P-按类+v4m3", "全部本地"]
print("| " + " | ".join(hdr) + " |")
print("|" + "---|" * len(hdr))
cols = collections.defaultdict(list)
for r in sorted(rows, key=lambda r: (r["cls"], r["wl"])):
    b = r["base"]
    vals = dict(
        p1=sp(b, r["p1"]), wg=sp(b, r["wg"]), bal=sp(b, r["wgbal"]), shuf=sp(b, r["wgshuf"]), g=sp(b, r["wgmapg"]),
        v4bal=sp(r["v4m3"], r["v4wgbal"]), v4g=sp(r["v4m3"], r["v4wgmapg"]), v4=sp(b, r["v4m3"]), v4wg=sp(b, r["v4m3wg"]),
        v4wg_v4=sp(r["v4m3"], r["v4m3wg"]), m3=sp(b, r["m3p"]), m3wg=sp(r["m3p"], r["m3pwg"]),
        pg=sp(b, r["pg"]), sb=sp(b, r["sb"]), cls=sp(b, r["cls_o"]), clsv=sp(b, r["cls_v4m3"]),
        loc=sp(b, r["local"]))
    for k, v in vals.items():
        cols[k].append(v)
    print("| " + " | ".join([
        r["wl"], r["cls"], str(r.get("nk", "")),
        f"{f(r.get('loc_d'))}→{f(r.get('loc_m'))}",
        f"{f(r.get('mxw_d'))}→{f(r.get('mxw_m'))}",
        f(vals["p1"], 3), f(vals["wg"]), f(vals["bal"]), f(vals["shuf"]), f(vals["g"]), f(vals["v4"]), f(vals["v4wg"]), f(vals["v4wg_v4"]), f(vals["v4bal"]), f(vals["v4g"]),
        f(vals["m3"]), f(vals["m3wg"]), f(vals["pg"]), f(vals["sb"]), f(vals["cls"]), f(vals["clsv"]),
        f(vals["loc"])]) + " |")
print("| GMEAN | | | | | " + " | ".join(f(gm(cols[k]), 3 if k == "p1" else 2) for k in
      ["p1", "wg", "bal", "shuf", "g", "v4", "v4wg", "v4wg_v4", "v4bal", "v4g", "m3", "m3wg", "pg", "sb", "cls", "clsv", "loc"]) + " |")
print()
print("加速比 = base 时间 / 该配置时间（v4m3+WG÷v4m3、M3+WG÷M3+ 为相对各自无 WG 映射的加速）。")
