#!/usr/bin/env python3
"""P20 (10-22 update) step 2: migration-target runs -> runs_pm.csv, speedup_pm.csv, tables_pm.md.

usage: analyze_pm.py <out_dir> <runs_yss2.tsv> <runs_yss3.tsv> [<p19_v4m3.tsv>]
Speedup = T(baseline) / T(config). baseline / M3+ / all-local of the 14 original workloads
come from 20f323e (bit-identical to 810531a with the switches off); MG and atax from 810531a.
Valid = exit 0, same WG count, instructions within 1 % of the baseline.
"""
import csv, math, sys

WL = ["km-g1", "fft2d-n1024", "fw-n2048-p4", "moe-t64", "mt-g4", "pa-b14s7p32", "pr-g24",
      "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "cfd-c172k-b128", "mg-W",
      "embbag-b512", "bh-n86k", "atax-2688x8192-r64"]
GROUP = {"km-g1": "私有放错", "fft2d-n1024": "私有放错", "fw-n2048-p4": "私有放错", "moe-t64": "私有放错",
         "mt-g4": "流式", "pa-b14s7p32": "流式", "pr-g24": "热点广播", "tc-s12e16": "热点广播",
         "mm-g12": "热点广播", "sssp-s18e4-r1": "热点广播", "ivf-n8k-q328": "热点广播",
         "cfd-c172k-b128": "混合", "mg-W": "混合", "embbag-b512": "混合", "bh-n86k": "混合",
         "atax-2688x8192-r64": "混合"}
MULTI = {"pr-g24", "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "cfd-c172k-b128",
         "mg-W", "embbag-b512", "bh-n86k", "atax-2688x8192-r64"}   # multi-reader workloads
# label -> (item suffix, config part); "{C}" picks the commit
CFG = [("baseline", "", "mi200"), ("M3+", "", "mi200+m3p"), ("全部本地", "-alllocal", "mi200"),
       ("c4k", "", "mi200+c4k"), ("c4k-median", "", "mi200+c4kmed"), ("c4k-class", "", "mi200+c4kcls"),
       ("c4k-class-stay", "", "mi200+c4kclsstay"), ("f4k", "", "mi200+f4k"), ("c2m", "", "mi200+c2m"),
       ("Q6", "-q6", "mi200+c4k"), ("交错1KB", "-ilv", "mi200"),
       ("M3+ c4k", "", "mi200+m3pc4k"), ("M3+ c4k-median", "", "mi200+m3pc4kmed"),
       ("M3+ c4k-class", "", "mi200+m3pc4kcls"), ("M3+ c4k-class-stay", "", "mi200+m3pc4kclsstay"),
       ("M3+ f4k", "", "mi200+m3pf4k"), ("M3+ c2m", "", "mi200+m3pc2m"), ("M3+ Q6", "-q6", "mi200+m3pc4k"),
       ("M3+ 交错1KB", "-ilv", "mi200+m3p"),
       ("M3+ class X0.25", "-x25", "mi200+m3pc4kcls"), ("M3+ class r7", "-r7", "mi200+m3pc4kcls"),
       ("M3+ class r47", "-r47", "mi200+m3pc4kcls"), ("M3+ class L1计数", "-l1cnt", "mi200+m3pc4kcls"),
       ("M3+ c4k L1计数", "-l1cnt", "mi200+m3pc4k"),
       ("v4m3", "", "mi200+v4m3"), ("v4m3 c4k-class", "", "mi200+v4m3c4kcls"),
       ("oracle P-按类+M3+", "-cls", "mi200+m3p")]


def gm(xs):
    xs = [x for x in xs if x]
    return math.exp(sum(math.log(x) for x in xs) / len(xs)) if xs else None


def main():
    out = sys.argv[1]
    runs = {}
    for f in sys.argv[2:4]:
        for r in csv.DictReader(open(f), delimiter="\t"):
            runs[r["exp"]] = r
    p19 = {}
    if len(sys.argv) > 4:
        for l in open(sys.argv[4]):
            e, tt, ins, wg = l.rstrip("\n").split("\t")
            p19[e] = dict(exp=e, exit="0", total_time=tt, inst=ins, wg=wg, pm="")
    table, rows = {}, []
    for w in WL:
        it = f"1022_abl_place-w128b384-{w}"
        for lab, suf, cfg in CFG:
            r = None
            for c in ("810531a", "20f323e"):
                r = runs.get(f"{it}{suf}_{cfg}_{c}")
                if r:
                    break
            if r is None and lab == "v4m3":
                r = p19.get(f"1022_diag_qt-w128c128b384-{w}_mi200+v4m3_9e87ac5")
            if r is None:
                continue
            table[(w, lab)] = r
    sp = {}
    for w in WL:
        b = table.get((w, "baseline"))
        if not b or b["exit"] != "0" or not b["total_time"]:
            continue
        tb, ib, wb = float(b["total_time"]), float(b["inst"]), b["wg"]
        for lab, _, _ in CFG:
            r = table.get((w, lab))
            if not r or r["exit"] != "0" or not r["total_time"]:
                continue
            valid = r["wg"] == wb and abs(float(r["inst"]) - ib) <= 0.01 * ib
            sp[(w, lab)] = (tb / float(r["total_time"]), valid)
    for (w, lab), r in table.items():
        s = sp.get((w, lab))
        pm = dict(kv.split("=", 1) for kv in r.get("pm", "").split(";") if "=" in kv)
        rows.append(dict(workload=w, config=lab, exp=r["exp"], exit=r["exit"],
                         total_time_us=float(r["total_time"]) * 1e6 if r["total_time"] else "",
                         inst=r["inst"], wg=r["wg"], speedup=round(s[0], 4) if s else "",
                         valid=int(s[1]) if s else "", **{k: pm.get(k, "") for k in (
                             "migrations", "pingpong", "pinned_units", "held_accesses",
                             "hot_owner_read_share", "target_dominant", "target_median",
                             "target_broadcast", "declined_same_place", "declined_small_saving",
                             "declined_broadcast")}))
    rows.sort(key=lambda r: (WL.index(r["workload"]), [c[0] for c in CFG].index(r["config"])))
    if rows:
        with open(f"{out}/runs_pm.csv", "w", newline="") as f:
            wr = csv.DictWriter(f, list(rows[0].keys()))
            wr.writeheader()
            wr.writerows(rows)
    labs = [l for l, _, _ in CFG]
    with open(f"{out}/speedup_pm.csv", "w", newline="") as f:
        wr = csv.writer(f)
        wr.writerow(["workload", "group"] + labs)
        for w in WL:
            wr.writerow([w, GROUP[w]] + [("%.4f%s" % (sp[(w, l)][0], "" if sp[(w, l)][1] else "*"))
                                         if (w, l) in sp else "" for l in labs])

    def g(lab, sel=lambda w: True, over=None):
        xs = []
        for w in WL:
            if not sel(w) or (w, lab) not in sp or not sp[(w, lab)][1]:
                continue
            if over:
                if (w, over) not in sp or not sp[(w, over)][1]:
                    continue
                xs.append(sp[(w, lab)][0] / sp[(w, over)][0])
            else:
                xs.append(sp[(w, lab)][0])
        return gm(xs), len(xs)

    def cell(v):
        return ("%.3f（%d）" % v) if v[0] else "–"
    md = ["| 配置 | GMEAN 全部 | 私有放错 | 流式 | 热点广播 | 混合 | 多读者 |", "|---|---|---|---|---|---|---|"]
    for l in labs:
        md.append(f"| {l} | {cell(g(l))} | " + " | ".join(cell(g(l, lambda w, G=G: GROUP[w] == G))
                                                        for G in ("私有放错", "流式", "热点广播", "混合"))
                  + f" | {cell(g(l, lambda w: w in MULTI))} |")
    md += ["", "| workload | " + " | ".join(labs) + " |", "|---" * (len(labs) + 1) + "|"]
    for w in WL:
        md.append(f"| {w} | " + " | ".join(
            ("%.3f%s" % (sp[(w, l)][0], "" if sp[(w, l)][1] else "*")) if (w, l) in sp else "–"
            for l in labs) + " |")
    cmp = [("c4k-median / c4k（多读者）", g("c4k-median", lambda w: w in MULTI, "c4k"), 1.03),
           ("M3+ c4k-median / M3+ c4k（多读者）", g("M3+ c4k-median", lambda w: w in MULTI, "M3+ c4k"), None),
           ("c4k-class / c4k", g("c4k-class", over="c4k"), None),
           ("M3+ c4k-class / M3+", g("M3+ c4k-class", over="M3+"), 1.05),
           ("M3+ c4k / M3+", g("M3+ c4k", over="M3+"), None),
           ("M3+ c4k-class / M3+ c4k", g("M3+ c4k-class", over="M3+ c4k"), None),
           ("M3+ c4k-class / M3+ f4k", g("M3+ c4k-class", over="M3+ f4k"), None),
           ("M3+ c4k-class / M3+ Q6", g("M3+ c4k-class", over="M3+ Q6"), None),
           ("M3+ c4k-class-stay / M3+ c4k-class", g("M3+ c4k-class-stay", over="M3+ c4k-class"), None),
           ("oracle P-按类+M3+ / M3+ c4k-class", g("oracle P-按类+M3+", over="M3+ c4k-class"), None),
           ("全部本地 / M3+ c4k-class", g("全部本地", over="M3+ c4k-class"), None),
           ("v4m3 c4k-class / v4m3", g("v4m3 c4k-class", over="v4m3"), None),
           ("v4m3 c4k-class / M3+ c4k-class", g("v4m3 c4k-class", over="M3+ c4k-class"), None)]
    md += ["", "| 比较 | GMEAN（个数） | 门槛 | 结论 |", "|---|---|---|---|"]
    for name, (v, n), th in cmp:
        if v is None:
            md.append(f"| {name} | – | {th or ''} | |")
            continue
        verdict = "" if th is None else ("保留" if v >= th else "**低于门槛**")
        md.append(f"| {name} | {v:.3f}（{n}） | {th or ''} | {verdict} |")
    open(f"{out}/tables_pm.md", "w").write("\n".join(md) + "\n")
    print("\n".join(md))


if __name__ == "__main__":
    main()
