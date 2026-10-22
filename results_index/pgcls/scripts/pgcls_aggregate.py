#!/usr/bin/env python3
"""Aggregate the per-run JSONs of pgcls_page.py into the pgcls/*.csv tables.

usage: pgcls_aggregate.py <json_dir> <out_dir>
<json_dir> holds <item>.json (baseline, pgcls_page.py), <item>_v4.json (pgcls_v4.py) and
runs.tsv (exp, exit, total_time, inst, wg; both servers concatenated).
Writes class_mix.csv, class_features.csv, bounds.csv, a3_subpage.csv, sampling_error.csv,
class_traffic.csv, oracle.csv, class_summary.csv.
"""
import csv, glob, json, math, os, sys

CLASSES = ["热点广播", "轮转广播", "亲和/区域", "随机共享", "私有放错", "自己反复读",
           "流式", "写共享", "owner为主"]
# workload-level type from dm/classification.md
WTYPE = {"pr-g24": "热点广播", "npbcg-W": "热点广播", "tc-s12e16": "热点广播",
         "mm-g12": "热点广播", "atax-2688x8192-r64": "热点广播",
         "fw-n2048-p4": "轮转广播", "lud-n1024": "轮转广播", "npbis-n1m-i1": "轮转广播",
         "cfd-c172k-b128": "亲和/区域", "mg-W": "亲和/区域", "moe-t64": "亲和/区域",
         "pa-b14s7p32": "亲和/区域", "shocsort-n688k": "亲和/区域",
         "sssp-s18e4-r1": "随机共享", "bfs-s18e2": "随机共享", "ivf-n8k-q328": "随机共享",
         "ar-pc1": "随机共享", "bh-n86k": "随机共享", "embbag-b512": "随机共享",
         "gups-L18m8": "随机共享", "fft2d-n1024": "私有（放错）", "i2c-g64": "私有（放错）",
         "km-g1": "私有（自己反复读）", "mt-g4": "私有（流式）", "spmv-g32": "私有（流式）"}
ORDER = list(WTYPE)
SINGLE = ["retain", "place", "replicate", "sub512"]


def r3(x):
    return "" if x is None else f"{x:.3f}"


def main():
    jd, out = sys.argv[1], sys.argv[2]
    runs, v4 = {}, {}
    for f in glob.glob(os.path.join(jd, "*.json")):
        d = json.load(open(f))
        if f.endswith("_v4.json"):
            v4[d["label"]] = d
        else:
            runs[d["label"]] = d
    labels = [l for l in ORDER if l in runs] + sorted(l for l in runs if l not in ORDER)

    with open(os.path.join(out, "class_mix.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["workload", "workload_type_dm", "remote_reads", "read_hops", "pages",
                    "kernels"] + [c + "_reads" for c in CLASSES] +
                   [c + "_hops" for c in CLASSES] +
                   ["top_class", "top_share", "classes_ge10pct", "mixed"])
        for l in labels:
            d = runs[l]
            R = d["agg"].get("remote_reads", 0) or 1
            H = d["agg"].get("read_hops", 0) or 1
            sh = {c: d["cls_reads"].get(c, 0) / R for c in CLASSES}
            hh = {c: d["cls_hops"].get(c, 0) / H for c in CLASSES}
            top = max(sh, key=sh.get)
            n10 = sum(1 for v in sh.values() if v >= 0.10)
            w.writerow([l, WTYPE.get(l, ""), int(R), int(H), int(d["agg"].get("pages", 0)),
                        d["kernels"]] + [r3(sh[c]) for c in CLASSES] +
                       [r3(hh[c]) for c in CLASSES] +
                       [top, r3(sh[top]), n10, int(sh[top] < 0.8)])

    with open(os.path.join(out, "class_features.csv"), "w", newline="") as f:
        w = csv.writer(f)
        keys = ["n_eff", "line_readers", "share", "pair_hops", "c_win", "w_jacc", "written",
                "prod_cons", "rep_frac", "sub_diff", "line_private"]
        w.writerow(["workload", "class", "reads_share", "pages"] + keys)
        for l in labels:
            d = runs[l]
            R = d["agg"].get("remote_reads", 0) or 1
            for c in CLASSES:
                if c not in d["cls_reads"]:
                    continue
                F = d["cls_feat"].get(c, {})
                w.writerow([l, c, r3(d["cls_reads"][c] / R), d["cls_pages"].get(c, 0)] +
                           [r3(F.get(k, 0)) for k in keys])

    with open(os.path.join(out, "bounds.csv"), "w", newline="") as f:
        w = csv.writer(f)
        acts = ["retain", "place", "replicate", "sub512", "best_single_workload", "rule",
                "oracle_r", "place_retain"]
        w.writerow(["workload", "remote_reads"] + [f"R_{a}" for a in acts] +
                   ["best_single_workload_R", "hops_none"] + [f"H_{a}" for a in acts] +
                   ["best_single_workload_H", "H_oracle_h"])
        for l in labels:
            d = runs[l]
            R0 = d["act_reads"]["none"] or 1
            H0 = d["act_hops"]["none"] or 1
            rr = {a: d["act_reads"][a] / R0 for a in d["act_reads"]}
            hh = {a: d["act_hops"][a] / H0 for a in d["act_hops"]}
            bR = min(SINGLE, key=lambda a: rr[a])
            bH = min(SINGLE, key=lambda a: hh[a])
            rr["best_single_workload"] = rr[bR]
            hh["best_single_workload"] = hh[bH]
            hh["oracle_r"] = hh["oracle_r"]
            w.writerow([l, int(R0)] + [r3(rr[a]) for a in acts] + [bR, int(H0)] +
                       [r3(hh[a]) for a in acts] + [bH, r3(hh["oracle_h"])])

    with open(os.path.join(out, "a3_subpage.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["workload", "reads_on_pages_sub512_tiles_differ", "reads_gain10_512",
                    "reads_gain10_1k", "reads_gain10_2k", "H_place", "H_sub512",
                    "H_sub512_vs_place"])
        for l in labels:
            d = runs[l]
            R0 = d["act_reads"]["none"] or 1
            H0 = d["act_hops"]["none"] or 1
            a = d["a3_reads"]
            hp, hs = d["act_hops"]["place"], d["act_hops"]["sub512"]
            w.writerow([l, r3(a.get("diff512", 0) / R0), r3(a.get("gain512", 0) / R0),
                        r3(a.get("gain1k", 0) / R0), r3(a.get("gain2k", 0) / R0),
                        r3(hp / H0), r3(hs / H0), r3(hs / hp if hp else None)])

    with open(os.path.join(out, "sampling_error.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["workload", "sample_every", "class", "exact_share", "sampled_share",
                    "abs_err"])
        for l in labels:
            d = runs[l]
            R = d["agg"].get("remote_reads", 0) or 1
            Rs = sum(d.get("cls_reads_sampled", {}).values()) or 1
            for c in CLASSES:
                e = d["cls_reads"].get(c, 0) / R
                s = d.get("cls_reads_sampled", {}).get(c, 0) / Rs
                if e or s:
                    w.writerow([l, d.get("sample_every"), c, r3(e), r3(s), r3(abs(e - s))])


    extra(jd, out, runs, v4, labels)


KEY = {"热点广播": "hotb", "轮转广播": "rotb", "亲和/区域": "aff", "随机共享": "rand",
       "私有放错": "misp", "自己反复读": "self", "流式": "strm", "写共享": "wsh",
       "owner为主": "own"}
BEST = {"热点广播": "replicate", "轮转广播": "push", "亲和/区域": "place", "随机共享": None,
        "私有放错": "place", "自己反复读": "retain", "流式": None, "写共享": None,
        "owner为主": None}
BEST_TXT = {"热点广播": "每个读者一份副本（读到后保留到被写）", "轮转广播": "产生后整块多播（推送）",
            "亲和/区域": "放到读者的最优位置（伙伴中点）", "随机共享": "只合并 / 打包（= v4）",
            "私有放错": "迁到主读者", "自己反复读": "跨 kernel 保留（只作废被写的 line）",
            "流式": "只合并 / 打包（= v4）", "写共享": "只合并 / 打包（= v4）", "owner为主": "不动"}
B_PER_HOP = 112  # bytes per remote read per hop (dm/ byte model)


def valid(o, b):
    """T15 rule: same WG count and instruction count within 1% of the run without oracle;
    a slowdown below 0.95x is flagged as an oracle artefact (2)."""
    if o[2] != b[2] or abs(o[1] - b[1]) > 0.01 * b[1]:
        return 0
    return 2 if b[0] / o[0] < 0.95 else 1


def gmean(xs):
    xs = [x for x in xs if x and x > 0]
    return math.exp(sum(math.log(x) for x in xs) / len(xs)) if xs else None


def extra(jd, out, runs, v4, labels):
    rt = {}
    p = os.path.join(jd, "runs.tsv")
    if os.path.exists(p):
        for l in open(p):
            f = l.rstrip("\n").split("\t")
            if len(f) >= 5 and f[1] == "0" and f[2].strip():
                rt[f[0]] = (float(f[2]), int(f[3] or 0), int(f[4] or 0))

    def run(item, tag, cfg):
        for e, v in rt.items():
            if e.startswith(f"1022_diag_pgcls-w256b384-{item}{tag}_{cfg}_"):
                return v
        return None

    # ---- class_traffic.csv (workload x class)
    T = []
    for l in labels:
        d = runs[l]
        R = d["agg"].get("remote_reads", 0) or 1
        H = d["agg"].get("read_hops", 0) or 1
        L = sum(d.get("cls_lat", {}).values()) or 1
        V = v4.get(l)
        for c in CLASSES:
            r = d["cls_reads"].get(c, 0)
            if not r:
                continue
            h, lat = d["cls_hops"].get(c, 0), d.get("cls_lat", {}).get(c, 0)
            row = dict(workload=l, cls=c, reads=r, reads_share=r / R, hops_share=h / H,
                       lat_share=lat / L)
            if V:
                row.update(v4_net_frac=V["net"].get(c, 0) / r,
                           v4_hops_frac=V["net_hops"].get(c, 0) / h if h else None,
                           v4_lat_frac=V["lat"].get(c, 0) / lat if lat else None)
            a = BEST[c]
            ar, ah = d["cls_act_reads"][c], d["cls_act_hops"][c]
            if a:
                row.update(best_reads_frac=ar[a] / r, best_hops_frac=ah[a] / h if h else None)
            row.update(oracle_single_reads_frac=min(ar[x] for x in SINGLE + ["push"]) / r)
            o = run(l, "-or-" + KEY[c], "mi200+v4complete")
            b = run(l, "", "mi200+v4complete")
            if o and b:
                row.update(oracle_speedup_v4=b[0] / o[0], oracle_valid=valid(o, b))
            T.append(row)
    cols = ["workload", "cls", "reads", "reads_share", "hops_share", "lat_share", "v4_net_frac",
            "v4_hops_frac", "v4_lat_frac", "best_reads_frac", "best_hops_frac",
            "oracle_single_reads_frac", "oracle_speedup_v4", "oracle_valid"]
    with open(os.path.join(out, "class_traffic.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(cols)
        for row in T:
            w.writerow([row.get(k) if isinstance(row.get(k), (str, int)) else r3(row.get(k))
                        for k in cols])

    # ---- oracle.csv (workload level: v4 / baseline all-local headroom)
    with open(os.path.join(out, "oracle.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["workload", "T_base_us", "T_v4_us", "v4_speedup", "all_local_over_base",
                    "all_local_over_v4"] + [f"or_{KEY[c]}_over_v4" for c in CLASSES])
        for l in labels:
            b, c4 = run(l, "", "mi200"), run(l, "", "mi200+v4complete")
            ab, a4 = run(l, "-or-all", "mi200"), run(l, "-or-all", "mi200+v4complete")
            fl = lambda o, x: "" if valid(o, x) == 1 else "*"
            row = [l, r3(b[0] * 1e6) if b else "", r3(c4[0] * 1e6) if c4 else "",
                   r3(b[0] / c4[0]) if b and c4 else "",
                   r3(b[0] / ab[0]) + fl(ab, b) if b and ab else "",
                   r3(c4[0] / a4[0]) + fl(a4, c4) if c4 and a4 else ""]
            for c in CLASSES:
                o = run(l, "-or-" + KEY[c], "mi200+v4complete")
                row.append((r3(c4[0] / o[0]) + ("" if valid(o, c4) == 1 else "*"))
                           if o and c4 else "")
            w.writerow(row)

    # ---- class_summary.csv (class x everything)
    with open(os.path.join(out, "class_summary.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["cls", "best_way", "workloads_ge5pct", "share_all_reads", "share_all_hops",
                    "share_all_lat", "mean_wl_share", "v4_eaten_reads", "v4_eaten_hops",
                    "v4_eaten_lat", "v4_left_share_net", "v4_left_share_lat",
                    "best_eaten_reads", "best_eaten_hops", "oracle_gmean_over_v4",
                    "oracle_max_over_v4", "oracle_runs", "oracle_list"])
        totR = sum(runs[l]["agg"].get("remote_reads", 0) for l in labels) or 1
        totH = sum(runs[l]["agg"].get("read_hops", 0) for l in labels) or 1
        totL = sum(sum(runs[l].get("cls_lat", {}).values()) for l in labels) or 1
        V4N = sum(sum(v["net"].values()) for v in v4.values()) or 1
        V4L = sum(sum(v["lat"].values()) for v in v4.values()) or 1
        for c in CLASSES:
            rows = [t for t in T if t["cls"] == c]
            if not rows:
                continue
            vl = sum(v4[t["workload"]]["lat"].get(c, 0) for t in rows if t["workload"] in v4)
            bl = sum(runs[t["workload"]].get("cls_lat", {}).get(c, 0) for t in rows
                     if t["workload"] in v4) or 1
            r = sum(t["reads"] for t in rows)
            h = sum(runs[t["workload"]]["cls_hops"].get(c, 0) for t in rows)
            lat = sum(runs[t["workload"]].get("cls_lat", {}).get(c, 0) for t in rows)
            vn = sum(v4[t["workload"]]["net"].get(c, 0) for t in rows if t["workload"] in v4)
            vh = sum(v4[t["workload"]]["net_hops"].get(c, 0) for t in rows if t["workload"] in v4)
            rv = sum(t["reads"] for t in rows if t["workload"] in v4) or 1
            hv = sum(runs[t["workload"]]["cls_hops"].get(c, 0) for t in rows if t["workload"] in v4) or 1
            a = BEST[c]
            br = bh = None
            if a:
                br = 1 - sum(runs[t["workload"]]["cls_act_reads"][c][a] for t in rows) / r
                bh = 1 - sum(runs[t["workload"]]["cls_act_hops"][c][a] for t in rows) / (h or 1)
            sp = [t["oracle_speedup_v4"] for t in rows
                  if t.get("oracle_speedup_v4") and t.get("oracle_valid") == 1]
            ol = ";".join(f'{t["workload"]}:{t["oracle_speedup_v4"]:.3f}'
                          + ("" if t.get("oracle_valid") == 1 else "*") for t in rows
                          if t.get("oracle_speedup_v4"))
            w.writerow([c, BEST_TXT[c], sum(1 for t in rows if t["reads_share"] >= 0.05),
                        r3(r / totR), r3(h / totH), r3(lat / totL),
                        r3(sum(t["reads_share"] for t in rows) / len(labels)),
                        r3(1 - vn / rv), r3(1 - vh / hv), r3(1 - vl / bl), r3(vn / V4N),
                        r3(vl / V4L), r3(br), r3(bh),
                        r3(gmean(sp)), r3(max(sp) if sp else None), len(sp), ol])


if __name__ == "__main__":
    main()
