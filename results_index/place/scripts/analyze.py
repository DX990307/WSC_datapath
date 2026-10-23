#!/usr/bin/env python3
"""P20 step 2: runs.csv + speedup.csv + markdown tables from the collect.py outputs.

usage: analyze.py <out_dir> <runs_yss2.tsv> <runs_yss3.tsv> [<p19_v4m3.tsv>]
  p19_v4m3.tsv (optional): exp<TAB>total_time<TAB>inst<TAB>wg of P19 qt-w128c128b384 v4m3 runs,
  used as the v4m3 reference when this round did not run it.
Speedup = T(baseline) / T(config). A run is valid when exit 0 and its WG count equals and its
instruction count is within 1% of the baseline's (pgcls §5); invalid ones are marked * and
left out of every GMEAN.
"""
import csv, math, sys, collections

C = "20f323e"
WL = ["km-g1", "fft2d-n1024", "fw-n2048-p4", "moe-t64", "mt-g4", "pa-b14s7p32", "pr-g24",
      "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "cfd-c172k-b128", "embbag-b512",
      "bh-n86k"]
GROUP = {"km-g1": "私有放错", "fft2d-n1024": "私有放错", "fw-n2048-p4": "私有放错", "moe-t64": "私有放错",
         "mt-g4": "流式", "pa-b14s7p32": "流式", "pr-g24": "热点广播", "tc-s12e16": "热点广播",
         "mm-g12": "热点广播", "sssp-s18e4-r1": "热点广播", "ivf-n8k-q328": "热点广播",
         "cfd-c172k-b128": "混合", "embbag-b512": "混合", "bh-n86k": "混合"}
# label -> (item suffix, config part)
CFG = [("baseline", "", "mi200"), ("M3+", "", "mi200+m3p"), ("v4m3", "", "mi200+v4m3"),
       ("P-整页", "-pg", "mi200"), ("P-子块", "-sb", "mi200"), ("P-按类", "-cls", "mi200"),
       ("P-按类-中心", "-ctr", "mi200"), ("P-按类+M3+", "-cls", "mi200+m3p"),
       ("P-按类+v4m3", "-cls", "mi200+v4m3"), ("P-med", "-med", "mi200"),
       ("P-med+M3+", "-med", "mi200+m3p"), ("全部本地", "-alllocal", "mi200")]


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
            p19[e] = dict(exp=e, exit="0", total_time=tt, inst=ins, wg=wg, misrouted="")
    table = {}
    rows = []
    for w in WL:
        it = f"1022_abl_place-w128b384-{w}"
        for lab, suf, cfg in CFG:
            e = f"{it}{suf}_{cfg}_{C}"
            r = runs.get(e)
            src = "P20"
            if r is None and lab == "v4m3":
                r = p19.get(f"1022_diag_qt-w128c128b384-{w}_mi200+v4m3_9e87ac5")
                src = "P19"
            if r is None:
                continue
            table[(w, lab)] = r
            rows.append(dict(workload=w, config=lab, exp=r["exp"], source=src, exit=r["exit"],
                             total_time_us=float(r["total_time"]) * 1e6 if r["total_time"] else "",
                             inst=r["inst"], wg=r["wg"], misrouted=r.get("misrouted", ""),
                             l1_local=r.get("l1_local", ""), l1_remote=r.get("l1_remote", ""),
                             pg_remote_R=r.get("pg_remote_R", "")))
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
            valid = r["wg"] == wb and abs(float(r["inst"]) - ib) <= 0.01 * ib and r.get("misrouted", "") in ("", "0")
            sp[(w, lab)] = (tb / float(r["total_time"]), valid)
    for row in rows:
        s = sp.get((row["workload"], row["config"]))
        row["speedup"] = round(s[0], 4) if s else ""
        row["valid"] = int(s[1]) if s else ""
    with open(f"{out}/runs.csv", "w", newline="") as f:
        wr = csv.DictWriter(f, list(rows[0].keys()))
        wr.writeheader()
        wr.writerows(rows)
    labs = [l for l, _, _ in CFG]
    with open(f"{out}/speedup.csv", "w", newline="") as f:
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

    md = ["| workload | 类 | " + " | ".join(labs) + " |", "|---" * (len(labs) + 2) + "|"]
    for w in WL:
        md.append(f"| {w} | {GROUP[w]} | " + " | ".join(
            ("%.3f%s" % (sp[(w, l)][0], "" if sp[(w, l)][1] else "*")) if (w, l) in sp else "–"
            for l in labs) + " |")
    md.append("| **GMEAN（有效个数）** | | " + " | ".join(
        ("%.3f（%d）" % g(l)) if g(l)[0] else "–" for l in labs) + " |")
    for grp in ("私有放错", "流式", "热点广播", "混合"):
        md.append(f"| GMEAN {grp} | | " + " | ".join(
            ("%.3f（%d）" % g(l, lambda w: GROUP[w] == grp)) if g(l, lambda w: GROUP[w] == grp)[0] else "–"
            for l in labs) + " |")
    ps = lambda w: GROUP[w] in ("私有放错", "流式")
    conc = lambda w: w in ("pr-g24", "tc-s12e16", "mm-g12")
    cut = [
        ("P-子块 / P-整页（私有放错 + 流式）", g("P-子块", ps, "P-整页"), 1.03),
        ("P-按类 / P-按类-中心（PR、tc、MM）", g("P-按类", conc, "P-按类-中心"), 1.03),
        ("P-按类+M3+ / M3+（全部）", g("P-按类+M3+", over="M3+"), 1.05),
        ("P-按类+v4m3 / v4m3（全部）", g("P-按类+v4m3", over="v4m3"), None),
        ("全部本地 / P-按类+M3+", g("全部本地", over="P-按类+M3+"), None),
        ("P-med / P-按类", g("P-med", over="P-按类"), None),
        ("P-med+M3+ / P-按类+M3+", g("P-med+M3+", over="P-按类+M3+"), None),
        ("全部本地 / P-按类+v4m3", g("全部本地", over="P-按类+v4m3"), None),
    ]
    md += ["", "| 比较 | GMEAN（个数） | 门槛 | 结论 |", "|---|---|---|---|"]
    for name, (v, n), th in cut:
        if v is None:
            md.append(f"| {name} | – | {th or ''} | |")
            continue
        verdict = "" if th is None else ("保留" if v >= th else "**低于门槛**")
        md.append(f"| {name} | {v:.3f}（{n}） | {th or ''} | {verdict} |")
    open(f"{out}/tables.md", "w").write("\n".join(md) + "\n")
    print("\n".join(md))


if __name__ == "__main__":
    main()
