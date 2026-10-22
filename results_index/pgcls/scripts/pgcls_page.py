#!/usr/bin/env python3
"""Per-page classification and per-page "best action" bounds for one run.

usage: pgcls_page.py <prefix> <label> <out_dir>
  <prefix>  = .../stats/metrics_pgcls   (files <prefix>_pages.csv.gz, _readers.csv.gz,
              _kernels.csv, _totals.csv written by -report-pgcls)
Writes <out_dir>/<label>.json (workload summary) and <out_dir>/<label>_pages.csv.gz
(one row per page with remote reads).

Pure python (no numpy). Units: one remote read = one L1-miss line read that crosses the
network = one round trip in the baseline; read-hops = remote reads x Manhattan hops
(bytes x hops = read-hops x 112 B with the dm/ byte model).
"""
import csv, gzip, json, math, os, sys, collections

W = 7
CENTER = 24
TILES = [t for t in range(W * W) if t != CENTER]
LINES = 64
SUBS = 8
TX = [t % W for t in range(W * W)]
TY = [t // W for t in range(W * W)]


def hops(a, b):
    return abs(TX[a] - TX[b]) + abs(TY[a] - TY[b])


# ---------------------------------------------------------------- thresholds (see analysis.md)
NOISE = 0.01        # a reader counts if it has >= 1% of the page's reads
BCAST_N = 8         # broadcast: each touched line is read by >= 8 GPMs on average
LINE_PRIV = 1.2     # line-private: <= 1.2 readers per line although the page has several
HOT_CWIN = 4.0      # hot broadcast: >= 4 readers per active 10 us window (read-weighted) ...
HOT_WN = 3          # ... in >= 3 windows; otherwise the broadcast rotates through its readers
PRIV_SHARE = 0.9    # private: main reader has >= 90% of the page's reads
STREAM_REUSE = 2.0  # streaming: main reader reads each line < 2 times on average
AFF_HOPS = 3.0      # affinity: mean pairwise hops between readers <= 3 (random pair: 4.7)
WSHARE_FRAC = 0.2   # write-shared: >= 20% of the reads hit lines written by another GPM in the same kernel
SAMPLE = 16         # raw-trace page sampling of the runs (-pgcls-sample)
SUB_GAIN = 0.10     # A3: sub-page placement saves >= 10% read-hops over page placement

CLASSES = ["热点广播", "轮转广播", "亲和/区域", "随机共享", "私有放错", "自己反复读",
           "流式", "写共享", "owner为主"]


def best_tile(w):
    """w: dict tile -> weight. Returns (tile, cost) minimising sum w*hops (separable)."""
    cx = [0.0] * W
    cy = [0.0] * W
    for t, v in w.items():
        x, y = TX[t], TY[t]
        for i in range(W):
            cx[i] += v * abs(x - i)
            cy[i] += v * abs(y - i)
    best, bc = None, None
    for t in TILES:
        c = cx[TX[t]] + cy[TY[t]]
        if bc is None or c < bc - 1e-9 or (abs(c - bc) <= 1e-9 and w.get(t, 0) > w.get(best, 0)):
            best, bc = t, c
    return best, bc


M64 = (1 << 64) - 1


def pghash(x):
    """splitmix64, identical to pgclsHash in akita/mem/trace/pgcls.go"""
    x = (x + 0x9e3779b97f4a7c15) & M64
    x = ((x ^ (x >> 30)) * 0xbf58476d1ce4e5b9) & M64
    x = ((x ^ (x >> 27)) * 0x94d049bb133111eb) & M64
    return x ^ (x >> 31)


def xy_links(o, readers):
    """Links of the multicast tree owner -> readers along the Y-then-X routes (union)."""
    links = set()
    for r in readers:
        x, y = TX[o], TY[o]
        while y != TY[r]:
            ny = y + (1 if TY[r] > y else -1)
            links.add((x + W * y, x + W * ny))
            y = ny
        while x != TX[r]:
            nx = x + (1 if TX[r] > x else -1)
            links.add((x + W * y, nx + W * y))
            x = nx
    return len(links)


def popcount(x):
    return bin(x).count("1")


def load_totals(prefix):
    d = {}
    with open(prefix + "_totals.csv") as f:
        for r in csv.DictReader(f):
            d[r["key"]] = int(r["value"])
    return d


def load_kernels(prefix):
    gk = 0
    with open(prefix + "_kernels.csv") as f:
        for r in csv.DictReader(f):
            gk = max(gk, int(r["global_k"]))
    return gk


def readers_by_page(prefix):
    cur, rows = None, []
    with gzip.open(prefix + "_readers.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            p = int(r["page"])
            if p != cur:
                if cur is not None:
                    yield cur, rows
                cur, rows = p, []
            rows.append(r)
    if cur is not None:
        yield cur, rows


def main():
    prefix, label, out = sys.argv[1], sys.argv[2], sys.argv[3]
    os.makedirs(out, exist_ok=True)
    tot = load_totals(prefix)
    K = load_kernels(prefix)
    Wn = max(1, tot["end_ns"] // max(1, tot["window_ns"]))
    pages = {}
    with gzip.open(prefix + "_pages.csv.gz", "rt") as f:
        for r in csv.DictReader(f):
            pages[int(r["page"])] = r

    agg = collections.defaultdict(float)          # totals
    cls_r = collections.Counter()                 # remote reads by class
    cls_h = collections.Counter()                 # read-hops by class
    cls_act_r = collections.defaultdict(collections.Counter)
    cls_act_h = collections.defaultdict(collections.Counter)
    cls_lat = collections.Counter()               # remote read latency total (ns) by class
    cls_net = collections.Counter()               # network read lines by class
    cls_neth = collections.Counter()              # network read line-hops by class
    cls_rs = collections.Counter()                # remote reads by class, raw-trace page sample
    cls_pages = collections.Counter()
    cls_feat = collections.defaultdict(lambda: collections.defaultdict(float))
    act_r = collections.Counter()                 # remote reads after action (all pages)
    act_h = collections.Counter()
    sub_r = collections.Counter()                 # A3
    sub_h = collections.Counter()
    pfh = gzip.open(os.path.join(out, label + "_pages.csv.gz"), "wt")
    pw = csv.writer(pfh)
    pw.writerow(["page", "owner", "cls", "rd_remote", "rd_local", "hops0", "n_all", "n_eff",
                 "main", "main_share", "pair_hops", "c_win", "c_k", "k_n", "w_n", "w_jacc",
                 "written", "other_wr_frac", "prod_cons_frac", "reuse_main", "rep_frac", "cross_frac",
                 "best_t", "R_retain", "R_place", "R_place_retain", "R_repl", "R_sub512",
                 "H_retain", "H_place", "H_place_retain", "H_repl", "H_sub512", "H_sub1k",
                 "H_sub2k", "sub_diff512", "line_readers", "line_private"])

    for pg, rows in readers_by_page(prefix):
        P = pages.get(pg)
        if P is None:
            continue
        o = int(P["owner"])
        rdr = int(P["rd_remote"])
        if rdr == 0:
            agg["local_only_pages"] += 1
            continue
        R = {}
        for r in rows:
            g = int(r["reader"])
            reads = int(r["reads"])
            if reads == 0:
                continue
            R[g] = dict(reads=reads, sub=[int(x) for x in r["sub_reads"].split(";")],
                        mask=int(r["line_mask"], 16),
                        lines=popcount(int(r["line_mask"], 16)), intra=int(r["rep_intra"]),
                        cross=int(r["rep_cross"]), nk=int(r["n_k"]))
        tot_reads = sum(v["reads"] for v in R.values())
        H0 = sum(v["reads"] * hops(g, o) for g, v in R.items() if g != o)
        R0 = sum(v["reads"] for g, v in R.items() if g != o)
        if R0 != rdr:
            agg["mismatch_pages"] += 1
        eff = [g for g, v in R.items() if v["reads"] >= NOISE * tot_reads]
        main = max(R, key=lambda g: R[g]["reads"])
        share = R[main]["reads"] / tot_reads
        n_eff = len(eff)
        if n_eff >= 2:
            ph = [hops(a, b) for i, a in enumerate(eff) for b in eff[i + 1:]]
            pair_hops = sum(ph) / len(ph)
        else:
            pair_hops = 0.0
        w_rsum = float(P["w_rsum"]) or 1
        k_rsum = float(P["k_rsum"]) or 1
        c_win = float(P["w_wsum"]) / w_rsum
        c_k = float(P["k_wsum"]) / k_rsum
        k_n, w_n = int(P["k_n"]), int(P["w_n"])
        w_jacc = float(P["w_jacc"]) / int(P["w_trans"]) if int(P["w_trans"]) else 1.0
        written = int(P["writer_mask"], 16) != 0
        other_wr = int(P["rd_other_written_same_k"]) / tot_reads   # same kernel
        prod_cons = (int(P["rd_other_written"]) - int(P["rd_other_written_same_k"])) / tot_reads
        m = R[main]
        reuse_main = m["reads"] / max(1, m["lines"])
        rep_main = m["intra"] + m["cross"]
        rep_frac = rep_main / m["reads"]
        cross_frac = m["cross"] / rep_main if rep_main else 0.0

        # line-level sharing: mean number of GPMs that read each touched line
        cnt = [0] * LINES
        for v in R.values():
            mk = v["mask"]
            while mk:
                b = mk & -mk
                cnt[b.bit_length() - 1] += 1
                mk ^= b
        touched = [x for x in cnt if x]
        lr = sum(touched) / len(touched) if touched else 0.0
        line_private = share < PRIV_SHARE and lr <= LINE_PRIV  # page-level false sharing
        if line_private:
            rr = [v for g, v in R.items() if g != o] or list(R.values())
            reuse_main = sum(v["reads"] for v in rr) / max(1, sum(v["lines"] for v in rr))
            rp = sum(v["intra"] + v["cross"] for v in rr)
            cross_frac = sum(v["cross"] for v in rr) / rp if rp else 0.0
            rep_frac = rp / max(1, sum(v["reads"] for v in rr))

        # ---------------- classification
        if other_wr >= WSHARE_FRAC:
            c = "写共享"
        elif lr >= BCAST_N:
            hot = c_win >= HOT_CWIN and w_n >= HOT_WN
            c = "热点广播" if hot else "轮转广播"
        elif share >= PRIV_SHARE or line_private:
            if main == o and not line_private:
                c = "owner为主"
            elif reuse_main < STREAM_REUSE:
                c = "流式"
            elif cross_frac >= 0.5:
                c = "自己反复读"
            else:
                c = "私有放错"
        elif pair_hops <= AFF_HOPS:
            c = "亲和/区域"
        else:
            c = "随机共享"

        # ---------------- actions (remote reads after, read-hops after)
        rep = {g: v["intra"] + v["cross"] for g, v in R.items()}
        R_ret = sum(v["reads"] - rep[g] for g, v in R.items() if g != o)
        H_ret = sum((v["reads"] - rep[g]) * hops(g, o) for g, v in R.items() if g != o)
        wts = {g: v["reads"] for g, v in R.items()}
        bt, _ = best_tile(wts)
        mig = LINES * hops(o, bt)  # one bulk copy of the page, counted in hops only
        R_pl = sum(v["reads"] for g, v in R.items() if g != bt)
        H_pl = sum(v["reads"] * hops(g, bt) for g, v in R.items()) + mig
        R_plr = sum(v["reads"] - rep[g] for g, v in R.items() if g != bt)
        H_plr = sum((v["reads"] - rep[g]) * hops(g, bt) for g, v in R.items()) + mig
        if not written:
            # replicate read-only page: each reader fetches each line once, from the nearest
            # of {owner, other readers} (neighbour fetch, ideal ordering)
            R_rep = sum(v["lines"] for g, v in R.items() if g != o)
            H_rep = 0
            for g, v in R.items():
                if g == o:
                    continue
                d = min([hops(g, o)] + [hops(g, x) for x in R if x != g])
                H_rep += v["lines"] * max(1, d)
        else:
            R_rep, H_rep = R_ret, H_ret
        # sub-page placement: each 512 B / 1 KB / 2 KB block at its own best tile
        Hs = {}
        Rs512 = 0
        sub_tiles = []
        for size in (1, 2, 4):
            hsum = 0
            for b in range(0, SUBS, size):
                w = {g: sum(v["sub"][b:b + size]) for g, v in R.items()}
                w = {g: x for g, x in w.items() if x}
                if not w:
                    continue
                t, cst = best_tile(w)
                hsum += cst + LINES * size / SUBS * hops(o, t)
                if size == 1:
                    Rs512 += sum(x for g, x in w.items() if g != t)
                    sub_tiles.append(t)
            Hs[size] = hsum
        sub_diff = len(set(sub_tiles)) > 1

        # push / multicast: every fetch that remains after retention (first read of a line
        # per reader and per write epoch / kernel) is pushed by the owner instead of being
        # requested: no requester round trip; each production event is one multicast tree
        # to the page's remote readers (events ~ remaining fetches / remote readers).
        rem = [g for g in R if g != o]
        if rem:
            H_push = R_ret / len(rem) * xy_links(o, rem)
        else:
            H_push = 0
        acts = dict(none=(R0, H0), retain=(R_ret, H_ret), place=(R_pl, H_pl), push=(0, H_push),
                    place_retain=(R_plr, H_plr), replicate=(R_rep, H_rep),
                    sub512=(Rs512, Hs[1]))
        # class-mapped single action (the "most suitable" mechanism of each class)
        rule = {"私有放错": "sub512" if line_private else "place", "自己反复读": "retain", "流式": "none",
                "热点广播": "replicate", "轮转广播": "push", "亲和/区域": "place",
                "随机共享": "none", "写共享": "none", "owner为主": "none"}[c]
        acts["rule"] = acts[rule]
        # oracle single action per page (no combinations)
        single = ["none", "retain", "place", "replicate", "sub512"]
        acts["oracle_r"] = min(acts[a] for a in single)
        acts["oracle_h"] = min((acts[a] for a in single), key=lambda x: (x[1], x[0]))
        for a, (r_, h_) in acts.items():
            act_r[a] += r_
            act_h[a] += h_
            cls_act_r[c][a] += r_
            cls_act_h[c][a] += h_

        cls_r[c] += R0
        cls_lat[c] += float(P.get("lat_sum_ns") or 0)
        cls_net[c] += int(P.get("net_lines") or 0)
        cls_neth[c] += int(P.get("net_hops") or 0)
        if pghash(pg) % SAMPLE == 0:
            cls_rs[c] += R0
        cls_h[c] += H0
        cls_pages[c] += 1
        F = cls_feat[c]
        F["n_eff"] += n_eff * R0
        F["share"] += share * R0
        F["pair_hops"] += pair_hops * R0
        F["c_win"] += c_win * R0
        F["w_jacc"] += w_jacc * R0
        F["written"] += (1 if written else 0) * R0
        F["prod_cons"] += prod_cons * R0
        F["rep_frac"] += rep_frac * R0
        F["sub_diff"] += (1 if sub_diff else 0) * R0
        F["line_readers"] += lr * R0
        F["line_private"] += (1 if line_private else 0) * R0
        # A3 potential
        if sub_diff:
            sub_r["diff512"] += R0
        for size, key in ((1, "512"), (2, "1k"), (4, "2k")):
            if H_pl - Hs[size] >= SUB_GAIN * max(1, H_pl):
                sub_r["gain" + key] += R0
                sub_h["gain" + key] += H0
        agg["remote_reads"] += R0
        agg["read_hops"] += H0
        agg["pages"] += 1
        pw.writerow([pg, o, c, R0, int(P["rd_local"]), H0, len(R), n_eff, main,
                     round(share, 3), round(pair_hops, 2), round(c_win, 2), round(c_k, 2),
                     k_n, w_n, round(w_jacc, 3), int(written), round(other_wr, 3), round(prod_cons, 3),
                     round(reuse_main, 2), round(rep_frac, 3), round(cross_frac, 3), bt,
                     R_ret, R_pl, R_plr, R_rep, Rs512, H_ret, H_pl, H_plr, H_rep,
                     round(Hs[1]), round(Hs[2]), round(Hs[4]), int(sub_diff), round(lr, 2), int(line_private)])

    pfh.close()
    res = dict(label=label, kernels=K, windows=Wn, totals=tot, agg=dict(agg),
               cls_reads=dict(cls_r), cls_hops=dict(cls_h), cls_lat=dict(cls_lat), cls_net=dict(cls_net),
               cls_net_hops=dict(cls_neth), cls_reads_sampled=dict(cls_rs), sample_every=SAMPLE, cls_pages=dict(cls_pages),
               cls_feat={c: {k: v / max(1, cls_r[c]) for k, v in F.items()}
                         for c, F in cls_feat.items()},
               cls_act_reads={c: dict(v) for c, v in cls_act_r.items()},
               cls_act_hops={c: dict(v) for c, v in cls_act_h.items()},
               act_reads=dict(act_r), act_hops=dict(act_h), a3_reads=dict(sub_r),
               a3_hops=dict(sub_h))
    with open(os.path.join(out, label + ".json"), "w") as f:
        json.dump(res, f, ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
