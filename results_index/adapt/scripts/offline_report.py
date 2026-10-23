#!/usr/bin/env python3
"""Collect raw/{A,B,C,D} into offline_{A,B,C,D}.csv and print the markdown tables of offline.md."""
import csv, json, glob, os, sys, collections

BASE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
RAW = os.path.join(BASE, "raw")
WL = ["km-g1", "fft2d-n1024", "fw-n2048-p4", "moe-t64", "mt-g4", "pa-b14s7p32", "pr-g24",
      "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "cfd-c172k-b128", "mg-W",
      "embbag-b512", "bh-n86k", "atax-2688x8192-r64"]
SAMPLE = {"pr-g24": 1, "tc-s12e16": 1, "mm-g12": 1, "sssp-s18e4-r1": 1, "ivf-n8k-q328": 1}
f = lambda x: float(x) if x not in ("", None) else None


def fmt(x, n=2):
    if x is None or x == "":
        return "–"
    return ("%." + str(n) + "f") % float(x)


def A():
    H = ["w", "sig", "p", "K", "hot_remote", "tot_remote", "recall", "after", "before", "t_med_us", "false",
         "false_私有放错", "false_流式", "false_自己反复读", "false_other", "false_pages"]
    R = [dict(zip(H, r)) for r in csv.reader(open(os.path.join(RAW, "A", "all.csv")))]
    with open(os.path.join(BASE, "offline_A.csv"), "w", newline="") as fo:
        wr = csv.DictWriter(fo, fieldnames=H)
        wr.writeheader()
        for r in sorted(R, key=lambda r: (WL.index(r["w"]), r["sig"], int(r["p"]), int(r["K"]))):
            wr.writerow(r)
    hotwl = {r["w"] for r in R if int(r["tot_remote"]) and int(r["hot_remote"]) / int(r["tot_remote"]) >= 0.05}
    g = collections.defaultdict(list)
    for r in R:
        g[(r["sig"], int(r["p"]), int(r["K"]))].append(r)
    print("\n### A-1 参数网格汇总（召回/之后/判定前/时间：7 个热点 workload 平均；误判：16 个平均）\n")
    print("| 信号 | N 或 T | K | 召回 | 判定后的热点读 | 判定前已发生 | 首访→判定中位 µs | 误判（全部） | 其中 私有放错+流式+自己反复读 | 误判最大的 workload |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    name = {"i": "(i) owner 前 N 次远程读", "ii": "(ii) 翻译端 首访后 T", "ii-s": "(ii-s) 滑动窗口 T", "ii-sr": "(ii-sr) 滑动窗口 T，只计读"}
    for k in sorted(g, key=lambda k: (["i", "ii", "ii-s", "ii-sr"].index(k[0]), k[1], k[2])):
        v = g[k]
        hv = [r for r in v if r["w"] in hotwl]
        m = lambda key, L: (sum(f(r[key]) for r in L if f(r[key]) is not None) / max(1, sum(1 for r in L if f(r[key]) is not None)))
        harm = sum(f(r["false_私有放错"]) + f(r["false_流式"]) + f(r["false_自己反复读"]) for r in v) / len(v)
        mx = max(v, key=lambda r: f(r["false"]))
        print("| %s | %d | %d | %s | %s | %s | %s | %s | %s | %s %s |" % (name[k[0]], k[1], k[2], fmt(m("recall", hv), 3), fmt(m("after", hv), 3),
              fmt(m("before", hv), 3), fmt(m("t_med_us", hv), 1), fmt(m("false", v), 3), fmt(harm, 4), mx["w"].split("-")[0], fmt(mx["false"])))
    return R


def A_wl(R, cfgs):
    print("\n### A-2 逐 workload（推荐与对照参数）\n")
    hdr = "| workload | 采样 | 热点占远程读 | " + " | ".join("%s %s/K=%s：召回 / 判定前 / 中位µs / 误判（其中有害）" % c for c in cfgs) + " |"
    print(hdr)
    print("|" + "---|" * (3 + len(cfgs)))
    for w in WL:
        cells = []
        hs = None
        for c in cfgs:
            r = [x for x in R if x["w"] == w and (x["sig"], x["p"], x["K"]) == c][0]
            hs = int(r["hot_remote"]) / int(r["tot_remote"])
            harm = f(r["false_私有放错"]) + f(r["false_流式"]) + f(r["false_自己反复读"])
            cells.append("%s / %s / %s / %s (%s)" % (fmt(r["recall"]), fmt(r["before"], 3), fmt(r["t_med_us"], 1), fmt(r["false"], 3), fmt(harm, 3)))
        print("| %s | 1/%d | %s | %s |" % (w, SAMPLE.get(w, 4), fmt(hs), " | ".join(cells)))


def B():
    rows = []
    summ = []
    for w in WL:
        p = os.path.join(RAW, "B", w + ".json")
        if not os.path.exists(p):
            continue
        d = json.load(open(p))
        if not d["summary"]["hot_buffers"]:
            continue
        summ.append(d["summary"])
        for b in d["buffers"]:
            b = dict(b)
            b["w"] = w
            rows.append(b)
    keys = ["w", "blk", "N", "pages_rr", "hot", "hot_frac", "hot_frac_all", "longest_run", "hot_reads_share", "conc_pages",
            "conc_reads", "devices", "t_d_us", "tr_pages", "unacc", "prec", "prec_r", "gain", "gain_inc", "wrong_reads",
            "first_det_hot", "g_t_us", "g_unacc", "g_prec", "g_gain", "g_gain_inc", "g_wrong_reads"]
    with open(os.path.join(BASE, "offline_B.csv"), "w", newline="") as fo:
        wr = csv.DictWriter(fo, fieldnames=keys, extrasaction="ignore")
        wr.writeheader()
        for r in rows:
            wr.writerow({k: (round(v, 4) if isinstance(v, float) else v) for k, v in r.items()})
    print("\n### B-1 每个含热点页的 buffer（只列热点读占比 ≥ 1% 的 buffer）\n")
    print("| workload | buffer（页数） | 热点页 / 有远程读的页 | 最长连续热点段 / 热点页 | 占该 workload 热点读 | 最多设备份额（页 / 读） | 首次判定 µs | 当时未访问页 | 其中真热点（页 / 读加权） | 传播覆盖的热点读 | 其中自己判定会漏掉的 | 误拆非热点页的读 / 全部远程读 |")
    print("|" + "---|" * 12)
    for r in rows:
        if r["hot_reads_share"] < 0.01:
            continue
        print("| %s | %d（%d） | %d / %d = %s | %s | %s | %s / %s | %s | %s | %s / %s | %s | %s | %s |" % (
            r["w"], r["blk"], r["N"], r["hot"], r["pages_rr"], fmt(r["hot_frac"]), fmt(r["longest_run"]), fmt(r["hot_reads_share"]),
            fmt(r["conc_pages"]), fmt(r["conc_reads"]), fmt(r.get("t_d_us"), 1), fmt(r.get("unacc")), fmt(r.get("prec")), fmt(r.get("prec_r")),
            fmt(r.get("gain")), fmt(r.get("gain_inc"), 3), fmt(r.get("wrong_reads"), 3)))
    print("\n### B-2 workload 汇总\n")
    print("| workload | 采样 | 含热点 buffer | P20 workload 级最忙 owner 份额 → 集中? | buffer 级判断一致（按热点读加权） | 热点页比例（buffer 内，读加权） | 传播：覆盖热点读 / 增量 / 精度（页） / 误拆读 | 门控传播（已访问页 ≥ 50% 判热点）：覆盖 / 增量 / 精度 / 误拆读 |")
    print("|" + "---|" * 8)
    for s in summ:
        print("| %s | 1/%d | %d | %s → %s | %s | %s | %s / %s / %s / %s | %s / %s / %s / %s |" % (
            s["w"], s["sample"], s["hot_buffers"], fmt(s["wl_conc"]), "是" if s["wl_concentrated"] else "否", fmt(s["buf_conc_agree_w"]),
            fmt(s["hot_frac_w"]), fmt(s["gain"]), fmt(s["gain_inc"], 3), fmt(s["prec_pages"]), fmt(s["wrong_reads"], 3),
            fmt(s["g_gain"]), fmt(s["g_gain_inc"], 3), fmt(s["g_prec_pages"]), fmt(s["g_wrong_reads"], 3)))


def C():
    out = []
    for w in WL:
        p = os.path.join(RAW, "C", w + ".json")
        if not os.path.exists(p) or os.path.getsize(p) == 0:
            continue
        d = json.load(open(p))
        for g in ("page", "blk1k"):
            x = d[g]
            for m in ("m2", "m3", "m4", "m8"):
                y = x[m]
                rb = y["rb"]
                out.append(dict(w=w, gran=g, m=int(m[1:]), units=x["units"], mig_units=x["mig_units"],
                                mig_frac_reads=x["mig_frac_reads"],
                                **{"gfit_" + k: v for k, v in x["global_hit"].items()},
                                **{"online_hit_" + k: v for k, v in y["hit_online"].items()},
                                conf_buffers=y["conf_buffers"], buffers=y["buffers"], obs_to_conf=y.get("obs_to_conf"),
                                unacc_at_conf=y["unacc_at_conf"], os_cover_bytes=y["cover_bytes"], os_cover_reads=y["cover_reads"],
                                os_prec_units=y["prec_units"], os_prec_reads=y["prec_reads"], os_harm_reads=y.get("harm_reads"),
                                rb_cover_bytes=rb["cover_bytes"], rb_cover_reads=rb["cover_reads"], rb_prec_units=rb["prec_units"],
                                rb_prec_reads=rb["prec_reads"], rb_harm_reads=rb.get("harm_reads"), rb_rollbacks=rb["rollbacks"],
                                rules=";".join("%s:%d" % kv for kv in y["rules"].items())))
    keys = list(out[0].keys())
    with open(os.path.join(BASE, "offline_C.csv"), "w", newline="") as fo:
        wr = csv.DictWriter(fo, fieldnames=keys)
        wr.writeheader()
        for r in out:
            wr.writerow({k: (round(v, 4) if isinstance(v, float) else v) for k, v in r.items()})
    return out


def C_tables(out, mrec):
    print("\n### C-1 规律命中率（全局拟合，按被迁单元的远程读加权）与可迁比例\n")
    print("| workload | 被迁单元数（页 / 1 KB） | 被迁单元占远程读（页 / 1 KB） | 页：常数 / 线性 / 步长 / owner 偏移 / 最好 | 1 KB：常数 / 线性 / 步长 / owner 偏移 / 最好 |")
    print("|---|---|---|---|---|")
    for w in WL:
        P = [r for r in out if r["w"] == w and r["m"] == mrec]
        if not P:
            print("| %s | (未完成) | | |" % w)
            continue
        pg = [r for r in P if r["gran"] == "page"][0]
        bk = [r for r in P if r["gran"] == "blk1k"][0]
        cell = lambda r: " / ".join(fmt(r.get("gfit_" + k)) for k in ("const", "lin", "step", "owner", "best")) if r["mig_units"] else "无被迁单元"
        print("| %s | %d / %d | %s / %s | %s | %s |" % (w, pg["mig_units"], bk["mig_units"], fmt(pg["mig_frac_reads"]), fmt(bk["mig_frac_reads"]), cell(pg), cell(bk)))
    for gran in ("page", "blk1k"):
        print("\n### C-2 在线学习（%s；一次性 = 置信后按规律预放置全部未访问单元；回退 = 预测错就回退、重新置信）\n" % ("4 KB 页" if gran == "page" else "1 KB 块"))
        print("| workload | m | 置信 buffer / 含被迁单元的 buffer | 置信前观察数 | 确认时未访问 | 一次性：覆盖被迁字节 / 读 · 精度（单元 / 读）· 误迁 owner 页的读 | 回退：覆盖被迁字节 / 读 · 精度（单元 / 读）· 误迁读 · 回退次数 |")
        print("|" + "---|" * 7)
        for w in WL:
            for r in [r for r in out if r["w"] == w and r["gran"] == gran]:
                if not r["mig_units"]:
                    if r["m"] == 2:
                        print("| %s | – | 无被迁单元 | | | | |" % w)
                    continue
                print("| %s | %d | %d / %d | %s | %s | %s / %s · %s / %s · %s | %s / %s · %s / %s · %s · %d |" % (
                    w, r["m"], r["conf_buffers"], r["buffers"], fmt(r["obs_to_conf"], 0), fmt(r["unacc_at_conf"]),
                    fmt(r["os_cover_bytes"]), fmt(r["os_cover_reads"]), fmt(r["os_prec_units"]), fmt(r["os_prec_reads"]), fmt(r["os_harm_reads"], 3),
                    fmt(r["rb_cover_bytes"]), fmt(r["rb_cover_reads"]), fmt(r["rb_prec_units"]), fmt(r["rb_prec_reads"]), fmt(r["rb_harm_reads"], 3), r["rb_rollbacks"]))
    print("\n### C-3 汇总（16 个 workload；通过条件：≥ 6 个 workload 的被迁字节覆盖 ≥ 0.5）\n")
    print("| 粒度 | m | 一次性：覆盖 ≥ 0.5 的 workload 数 / 平均覆盖（字节）/ 平均精度 | 回退：覆盖 ≥ 0.5 的 workload 数 / 平均覆盖（字节）/ 平均精度（单元） / 平均精度（读） | 全局拟合最好规律命中 ≥ 0.5 的 workload 数 |")
    print("|---|---|---|---|---|")
    for gran in ("page", "blk1k"):
        for m in (2, 3, 4, 8):
            v = [r for r in out if r["gran"] == gran and r["m"] == m]
            mv = [r for r in v if r["mig_units"]]
            pm = lambda k: sum(r[k] for r in mv if r[k] is not None) / max(1, sum(1 for r in mv if r[k] is not None))
            print("| %s | %d | %d / %s / %s | %d / %s / %s / %s | %d |" % (
                gran, m, sum(1 for r in v if r["os_cover_bytes"] >= 0.5), fmt(sum(r["os_cover_bytes"] for r in v) / len(v)), fmt(pm("os_prec_units")),
                sum(1 for r in v if r["rb_cover_bytes"] >= 0.5), fmt(sum(r["rb_cover_bytes"] for r in v) / len(v)), fmt(pm("rb_prec_units")), fmt(pm("rb_prec_reads")),
                sum(1 for r in v if r["mig_units"] and r["gfit_best"] >= 0.5)))
    print("\n（n = %d 个 workload 有数据）" % len({r["w"] for r in out}))
    big = sorted({r["w"] for r in out if r["mig_frac_reads"] >= 0.1 and r["gran"] == "page"})
    print("\n只看被迁单元占远程读 ≥ 10%% 的 workload（%d 个：%s）：\n" % (len(big), "、".join(w.split("-")[0] for w in big)))
    print("| 粒度 | m | 一次性：覆盖 ≥ 0.5 的个数 / 平均覆盖 | 回退：覆盖 ≥ 0.5 的个数 / 平均覆盖 / 平均精度（单元） / 平均误迁读 |")
    print("|---|---|---|---|")
    for gran in ("page", "blk1k"):
        for m in (2, 3, 4, 8):
            v = [r for r in out if r["gran"] == gran and r["m"] == m and r["w"] in big]
            print("| %s | %d | %d / %s | %d / %s / %s / %s |" % (gran, m, sum(1 for r in v if r["os_cover_bytes"] >= 0.5),
                  fmt(sum(r["os_cover_bytes"] for r in v) / len(v)), sum(1 for r in v if r["rb_cover_bytes"] >= 0.5),
                  fmt(sum(r["rb_cover_bytes"] for r in v) / len(v)), fmt(sum(r["rb_prec_units"] or 0 for r in v) / len(v)),
                  fmt(sum(r["rb_harm_reads"] or 0 for r in v) / len(v), 3)))


def D():
    H = ["w", "gran", "policy", "w_cost", "alpha", "units", "mig_reads_share", "before", "stream_units", "stream_reads_share",
         "s1_units", "s1_reads_share", "hot_units", "hot_reads_share", "net_gain", "neg_frac", "tot_remote_reads"]
    R = [dict(zip(H, r)) for r in csv.reader(open(os.path.join(RAW, "D", "all.csv")))]
    with open(os.path.join(BASE, "offline_D.csv"), "w", newline="") as fo:
        wr = csv.DictWriter(fo, fieldnames=H)
        wr.writeheader()
        for r in sorted(R, key=lambda r: (WL.index(r["w"]), r["gran"], r["policy"], r["w_cost"], float(r["alpha"]))):
            wr.writerow(r)
    print("\n### D-1 汇总（16 个 workload 平均；净收益 = 判定后省下的延迟 − 迁移代价(w=1)，相对全部远程访问延迟）\n")
    print("| 粒度 | 策略 | w | α | 迁的单元数（采样页内） | 迁的单元占远程读 | 判定前已发生的读（迁移单元内，平均） | 流式页被迁（读占比） | 每 line 读 < 2 次的单元被迁（读占比） | 热点页被迁（读占比） | 平均净收益 | 净收益 < −1% 的 workload | 最差 workload 净收益 | 负收益迁移占比 |")
    print("|" + "---|" * 14)
    g = collections.defaultdict(list)
    for r in R:
        g[(r["gran"], r["policy"], r["w_cost"], float(r["alpha"]))].append(r)
    for k in sorted(g, key=lambda k: (k[0] != "page", k[1] != "a", -float(k[2]), k[3])):
        v = g[k]
        n = len(v)
        bef = [f(r["before"]) for r in v if f(r["before"]) is not None]
        U = sum(int(r["units"]) for r in v)
        worst = min(v, key=lambda r: f(r["net_gain"]))
        print("| %s | %s | %s | %s | %d | %s | %s | %s | %s | %s | %s | %d | %s %s | %s |" % (
            k[0], "收益/代价" if k[1] == "a" else "固定 256", k[2] if k[1] == "a" else "–", fmt(k[3]) if k[1] == "a" else "–", U,
            fmt(sum(f(r["mig_reads_share"]) for r in v) / n), fmt(sum(bef) / len(bef)),
            fmt(sum(f(r["stream_reads_share"]) for r in v) / n, 3), fmt(sum(f(r["s1_reads_share"]) for r in v) / n, 3),
            fmt(sum(f(r["hot_reads_share"]) for r in v) / n, 3), fmt(sum(f(r["net_gain"]) for r in v) / n, 3),
            sum(1 for r in v if f(r["net_gain"]) < -0.01), worst["w"].split("-")[0], fmt(worst["net_gain"]),
            fmt(sum((f(r["neg_frac"]) or 0) * int(r["units"]) for r in v) / max(1, U))))
    for gran in ("page", "blk1k"):
        print("\n### D-2 逐 workload（%s，w = 1；每格：迁的单元占远程读 / 判定前已发生 / 净收益；流式被迁时加 *）\n" % ("4 KB 页" if gran == "page" else "1 KB 块"))
        print("| workload | α=0.25 | α=0.5 | α=1 | α=2 | α=4 | 固定 256 |")
        print("|---|---|---|---|---|---|---|")
        for w in WL:
            cells = []
            for pol, a in [("a", x) for x in (0.25, 0.5, 1, 2, 4)] + [("fixed256", 0)]:
                r = [x for x in R if x["w"] == w and x["gran"] == gran and x["policy"] == pol and x["w_cost"] == "1.0" and float(x["alpha"]) == a][0]
                st = "*" if f(r["stream_reads_share"]) > 0.01 or f(r["s1_reads_share"]) > 0.01 else ""
                cells.append("%s / %s / %s%s" % (fmt(r["mig_reads_share"]), fmt(r["before"]), fmt(r["net_gain"]), st))
            print("| %s | %s |" % (w, " | ".join(cells)))


if __name__ == "__main__":
    what = sys.argv[1] if len(sys.argv) > 1 else "all"
    if what in ("A", "all"):
        R = A()
        A_wl(R, [("ii-sr", "2", "4"), ("ii-sr", "5", "6"), ("i", "64", "4"), ("ii", "10", "4")])
    if what in ("B", "all"):
        B()
    if what in ("C", "all"):
        C_tables(C(), 8)
    if what in ("D", "all"):
        D()


def EF():
    rd = lambda p: json.load(open(p))
    erows, prow, xrow, frows = [], [], [], []
    print("\n### E-1 同一 kernel 里各 buffer 的线性切分参数（全程聚合、整张目标图；只列命中 ≥ 0.5 的 buffer）\n")
    print("| workload | buffer（页数） | 每设备页数 n | 自身命中 | 被迁页占读 | 读它的 kernel | 首个 kernel 的 WG/CU | 每 WG 字节 |")
    print("|---|---|---|---|---|---|---|---|")
    for w in WL:
        p = os.path.join(RAW, "E", w + ".json")
        if not os.path.exists(p):
            continue
        d = rd(p)
        for b in d["buffers"]:
            erows.append(dict(w=w, **{k: v for k, v in b.items() if k != "kernels"}, k_first=b["kernels"][0] if b["kernels"] else None,
                              k_last=b["kernels"][1] if b["kernels"] else None))
            if b["hit"] >= 0.5:
                print("| %s | %d（%d） | %s | %s | %s | %s | %s | %s |" % (w, b["blk"], b["N"], fmt(b["n_pages_per_dev"]), fmt(b["hit"]), fmt(b["mig_w"]),
                      "%d–%d" % tuple(b["kernels"]) if b["kernels"] else "–", b["wg_per_cu"], fmt(b["bytes_per_wg"], 0)))
        for q in d["pairs"]:
            prow.append(dict(w=w, **q))
        for x in d["xkernel"]:
            xrow.append(dict(w=w, **x))
    print("\n### E-2 buffer 两两之间（kernel 区间重叠；有序对 A→B）\n")
    print("| workload | 对数 | σ 相同（误差 < 2%） | 成 2 的幂比例 | B 用 A 的全部参数 | B 用 A 的 σ、自己学偏移 | 再允许 ×2^k | B 自己拟合 |")
    print("|---|---|---|---|---|---|---|---|")
    for w in WL:
        p = os.path.join(RAW, "E", w + ".json")
        if not os.path.exists(p):
            continue
        s = rd(p)["summary"]
        if not s["pairs"]:
            continue
        print("| %s | %d | %d | %d | %s | %s | %s | %s |" % (w, s["pairs"], s["pairs_same_sigma"], s["pairs_pow2"], fmt(s["hit_full_mean"]),
              fmt(s["hit_sigma_mean"]), fmt(s["hit_pow2_mean"]), fmt(s["hit_own_mean"])))
    print("\n### E-3 跨 kernel（trace，按全局 kernel；1/4 页采样的 workload 只看样本页）\n")
    print("| workload | buffer | 读它的 kernel 数 | 相邻 kernel 页目标一致（读加权） | 第一个 kernel 的规则用于后面 kernel 的命中 |")
    print("|---|---|---|---|---|")
    for x in xrow:
        print("| %s | %s | %d | %s | %s |" % (x["w"], x["blk"], x["kernels"], fmt(x["agree"]), fmt(x["rule_first"])))
    for name, rows in (("offline_E_buffers.csv", erows), ("offline_E_pairs.csv", prow), ("offline_E_xkernel.csv", xrow)):
        keys = []
        for r in rows:
            for k in r:
                if k not in keys:
                    keys.append(k)
        with open(os.path.join(BASE, name), "w", newline="") as fo:
            wr = csv.DictWriter(fo, fieldnames=keys)
            wr.writeheader()
            for r in rows:
                wr.writerow({k: (round(v, 4) if isinstance(v, float) else v) for k, v in r.items()})
    # ---------------- F
    print("\n### F-1 LADM 风格放置表与 first-touch（剩余读×跳 / baseline 读×跳；\"+M3+\" = 加保留，仍除以不含保留的 baseline；baseline 自己 +M3+ 一列作对照）\n")
    print("| workload | 采样 | 应用 buffer / 全部 | 换 home 的页 | 这些页占远程读 | LADM | first-touch | pg（每页给主读者，oracle） | baseline+M3+ | LADM+M3+ | FT+M3+ | pg+M3+ | 首访者 ≠ 主读者的页占远程读 | FT 相对 pg 多出的读×跳（按类，≥ 0.005） |")
    print("|" + "---|" * 14)
    for w in WL:
        p = os.path.join(RAW, "F", w + ".json")
        if not os.path.exists(p):
            continue
        d = rd(p)
        loss = "、".join("%s %+.2f" % (c, v) for c, v in sorted(d["ft_loss_by_cls"].items(), key=lambda x: -abs(x[1])))
        print("| %s | 1/%d | %d / %d | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |" % (
            w, d["sample"], d["applied_buffers"], d["buffers"], d["moved_pages"], fmt(d["ladm_rr_share"]), fmt(d["H_ladm"]), fmt(d["H_ft"]),
            fmt(d["H_pg"]), fmt(d["Hret_base"]), fmt(d["Hret_ladm"]), fmt(d["Hret_ft"]), fmt(d["Hret_pg"]), fmt(d["ft_ne_main_rr"]), loss or "–"))
        frows.append({k: v for k, v in d.items() if k not in ("buffer_detail", "ft_loss_by_cls")} | {"ft_loss_by_cls": json.dumps(d["ft_loss_by_cls"], ensure_ascii=False)})
    import math
    gm = lambda k: math.exp(sum(math.log(max(1e-3, r[k])) for r in frows) / len(frows))
    am = lambda k: sum(r[k] for r in frows) / len(frows)
    print("| **平均（算术 / 几何）** | | | | | %s / %s | %s / %s | %s / %s | %s / %s | %s / %s | %s / %s | %s / %s | | |" % tuple(
        x for k in ("H_ladm", "H_ft", "H_pg", "Hret_base", "Hret_ladm", "Hret_ft", "Hret_pg") for x in (fmt(am(k)), fmt(gm(k)))))
    keys = list(frows[0].keys())
    with open(os.path.join(BASE, "offline_F.csv"), "w", newline="") as fo:
        wr = csv.DictWriter(fo, fieldnames=keys)
        wr.writeheader()
        for r in frows:
            wr.writerow({k: (round(v, 4) if isinstance(v, float) else v) for k, v in r.items()})
    print("\n### F-2 LADM 每个 buffer 的判定（只列第一个 kernel 里有 ≥ 8 个有目标页的 buffer）\n")
    print("| workload | buffer（页数） | 拟合用的 kernel | 有目标的页（样本） | 最好规则 | 命中 | 被迁页占该 kernel 读 | 应用 | 换 home 的页 |")
    print("|---|---|---|---|---|---|---|---|---|")
    for w in WL:
        p = os.path.join(RAW, "F", w + ".json")
        if not os.path.exists(p):
            continue
        for b in rd(p)["buffer_detail"]:
            if b.get("tgt_pages", 0) < 8:
                continue
            print("| %s | %d（%d） | %d | %d | %s | %s | %s | %s | %s |" % (w, b["blk"], b["N"], b["first_k"], b["tgt_pages"], b.get("rule", "–"),
                  fmt(b.get("hit")), fmt(b.get("mig_frac")), "是" if b["applied"] else "否", b.get("moved_pages", "–")))


if __name__ == "__main__" and len(sys.argv) > 1 and sys.argv[1] == "EF":
    EF()
