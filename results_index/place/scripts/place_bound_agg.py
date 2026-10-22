#!/usr/bin/env python3
"""Aggregate place_bound.py / place_stability.py JSONs into bound.csv and print markdown tables.

usage: place_bound_agg.py <bound_dir> <stab_dir> <out_csv>
"""
import csv, json, os, sys, math

ORDER = ["km-g1", "fft2d-n1024", "fw-n2048-p4", "moe-t64", "mt-g4", "pa-b14s7p32",
         "pr-g24", "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328",
         "cfd-c172k-b128", "embbag-b512", "bh-n86k",
         "npbcg-W", "atax-2688x8192-r64", "lud-n1024", "npbis-n1m-i1", "mg-W", "shocsort-n688k",
         "bfs-s18e2", "ar-pc1", "gups-L18m8", "i2c-g64", "spmv-g32"]
P20 = set(ORDER[:14])
HOT = ["pr-g24", "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "npbcg-W", "ar-pc1"]


def gm(xs):
    xs = [x for x in xs if x and x > 0]
    return math.exp(sum(math.log(x) for x in xs) / len(xs)) if xs else float("nan")


def main():
    bdir, sdir, out = sys.argv[1:4]
    B = {w: json.load(open(os.path.join(bdir, w + ".json"))) for w in ORDER
         if os.path.exists(os.path.join(bdir, w + ".json"))}
    S = {w: json.load(open(os.path.join(sdir, w + ".json"))) for w in ORDER
         if os.path.exists(os.path.join(sdir, w + ".json"))}
    rows = []
    for w in ORDER:
        if w not in B:
            continue
        a = B[w]["all"]
        n = a["none"]
        R0, H0, L0 = n["R"] or 1, n["H"] or 1, n["link_max"] or 1
        row = dict(workload=w, p20=int(w in P20), remote_reads=n["R"], read_hops=n["H"],
                   data="w256" if w == "moe-t64" else "w128")
        for p, v in a.items():
            row[f"R_{p}"] = round(v["R"] / R0, 4)
            row[f"H_{p}"] = round(v["H"] / H0, 4)
            row[f"Rret_{p}"] = round(v["R_ret"] / R0, 4)
            row[f"Hret_{p}"] = round(v["H_ret"] / H0, 4)
            row[f"own_{p}"] = round(v["own_max_share"], 4)
            row[f"link_{p}"] = round(v["link_max"] / L0, 4)
            saved = n["H"] - v["H"]
            row[f"mig_{p}"] = round(v["mig"] / H0, 4)
            row[f"migsaved_{p}"] = round(v["mig"] / saved, 4) if saved > 0 else ""
        h = B[w]["hot"]
        hn = h["none"]
        if hn["R"]:
            for p, v in h.items():
                row[f"hotH_{p}"] = round(v["H"] / hn["H"], 4)
                row[f"hotown_{p}"] = round(v["own_max_share"], 4)
                row[f"hotlink_{p}"] = round(v["link_max"] / (hn["link_max"] or 1), 4)
        cr = B[w]["cls_reads"]
        tot = sum(cr.values()) or 1
        for c in ("热点广播", "私有放错", "流式"):
            row[f"share_{c}"] = round(cr.get(c, 0) / tot, 3)
        if w in S:
            for ep in ("kernel", "window"):
                for g in ("P_classes", "hot"):
                    s = S[w][ep][g]
                    row[f"stab_{ep}_{g}_chg"] = round(s["chg"], 3) if s["blocks"] else ""
                    row[f"stab_{ep}_{g}_pp"] = round(s["pingpong"], 3) if s["blocks"] else ""
                    d = s["dyn_over_static_H"]
                    row[f"stab_{ep}_{g}_dyn"] = round(d, 3) if d else ""
            row["kernels"] = S[w]["kernels"]
        # P-按类 v2 with the concentration rule (bound.md): ring 12 only if the busiest owner
        # serves >= 0.3 of the hot pages' remote reads, else hot pages stay at their owner
        conc = row.get("hotown_none", 0) >= 0.3
        src = "sub_ring" if conc else "sub_hotnone"
        row["hot_concentrated"] = int(conc)
        for k in ("R", "H", "Rret", "Hret", "own", "link", "mig", "migsaved"):
            row[f"{k}_v2"] = row[f"{k}_{src}"]
        rows.append(row)
    keys = []
    for r in rows:
        for k in r:
            if k not in keys:
                keys.append(k)
    with open(out, "w", newline="") as f:
        wr = csv.DictWriter(f, keys)
        wr.writeheader()
        wr.writerows(rows)
    json.dump(rows, open(out.replace(".csv", ".json"), "w"), ensure_ascii=False, indent=0)


if __name__ == "__main__":
    main()
