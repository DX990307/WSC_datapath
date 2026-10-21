"""Side-by-side ASTRA-sim (P18) vs akita/MGPUSim tables. Reads runs.csv/hops.csv
(copied from the server) + dm/raw + m2w/m2w.csv. Writes compare_*.csv."""
import csv, os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import traffic as T  # noqa: E402
A = os.path.join(HERE, "..")
F = T.F
SIX = T.load.SIX
E1MAP = {("b384", 64): "w64b384", ("b384", 256): "w256b384", ("b384", 448): "w2048b384",
         ("b48", 64): "w64b48", ("b48", 256): "w256b48m", ("b48", 448): "w2048b48"}


def akita():
    """(cond, workload) -> dict of akita baseline metrics."""
    out = {}
    for r in csv.DictReader(open(os.path.join(A, "..", "m2w", "m2w.csv"))):
        if r["config"] == "baseline":
            out[(r["cond"], r["workload"])] = dict(
                total_time_us=F(r["total_time"]) * 1e6, rtt=F(r["lat:rtt_mean"]), ret_net=F(r["lat:ret_net_mean"]),
                mean_used=F(r["occ:mean_used"]), frac_full=F(r["occ:frac_time_full"]),
                bw_gpm=F(r["occ:remote_bw"]), link_mean=F(r["links:link_mean"]), link_max=F(r["links:link_max"]),
                link_p99=F(r["links:link_p99"]), link_window_max=F(r["links:link_window_max"]))
    for (sc, wl, cfg), o in T.runs().items():
        if cfg != "baseline":
            continue
        a0 = {(0 if r["hops"] == "all" else int(r["hops"])): r for r in o["latency"]}[0]
        ls = o["links"]
        hot = [l for l in ls if T.load.link_tiles(l) == (0, 7)][0]
        top = max(ls, key=lambda l: F(l["util"]))
        occ = o["occupancy"]
        d = out.setdefault((sc, wl), {})
        d.update(total_time_us=F(o["total_time"]) * 1e6, rtt=F(a0["rtt_mean"]), fwd=F(a0["fwd_mean"]),
                 ret_net=F(a0["ret_net_mean"]), owner_queue=F(a0["owner_queue_mean"]),
                 mean_used=sum(F(r["mean_used"]) for r in occ) / len(occ),
                 frac_full=sum(F(r["frac_time_full"]) for r in occ) / len(occ),
                 bw_gpm=sum(F(r["remote_bw"]) for r in occ) / len(occ),
                 link_mean=sum(F(l["util"]) for l in ls) / len(ls), link_max=F(top["util"]),
                 link_max_at="%d->%d" % T.load.link_tiles(top),
                 link_window_max=max(F(l["max_window_util"]) for l in ls),
                 hot07=F(hot["util"]), hot07_window=F(hot["max_window_util"]))
    return out


def akita_complete():
    """(cond, workload) -> akita Complete speedup and network metrics."""
    out = {}
    for r in csv.DictReader(open(os.path.join(A, "..", "m2w", "m2w.csv"))):
        if r["config"] == "Complete":
            out[(r["cond"], r["workload"])] = dict(speedup=F(r["speedup_vs_baseline"]), link_max=F(r["links:link_max"]),
                                                   rtt=F(r["lat:rtt_mean"]) if r["lat:rtt_mean"] else "")
    R = T.runs()
    for (sc, wl, cfg), o in R.items():
        if cfg != "Complete" or (sc, wl) in out or (sc, wl, "baseline") not in R:
            continue
        a0 = {(0 if x["hops"] == "all" else int(x["hops"])): x for x in o["latency"]}[0]
        out[(sc, wl)] = dict(speedup=F(R[(sc, wl, "baseline")]["total_time"]) / F(o["total_time"]),
                             link_max=max(F(l["util"]) for l in o["links"]), rtt=F(a0["rtt_mean"]))
    return out


def rd(p):
    return list(csv.DictReader(open(os.path.join(A, p))))


def main():
    ak = akita()
    runs = {r["run"]: r for r in rd("runs.csv")}
    # E1: closed loop
    rows = []
    for tr in SIX + ["uniform", "hotspot"]:
        for bw in ("b384", "b48"):
            for W in (64, 256, 448):
                r = runs.get(f"E1_{tr}_{bw}_W{W}")
                if not r or r["rc"] != "0":
                    continue
                k = ak.get((E1MAP[(bw, W)], tr), {})
                rows.append(dict(traffic=tr, bw=bw, W=W, akita_cond=E1MAP[(bw, W)] if k else "",
                                 astra_gpm_Bpns=F(r["thr_gpm_mean"]) * 64, astra_total_Bpns=F(r["thr_total"]) * 64,
                                 astra_rtt=F(r["rtt_mean"]), astra_rtt_p99=F(r["rtt_p99"]),
                                 astra_W_over_rtt_Bpns=W / F(r["rtt_mean"]) * 64,
                                 astra_link_mean=F(r["link_util_mean"]), astra_link_max=F(r["link_util_max"]),
                                 astra_link_max_at=r["link_max_at"], astra_hot07=F(r["hot07_util"]),
                                 astra_eject0=F(r["eject0_util"]), astra_inject0=F(r["inject0_util"]),
                                 akita_rtt=k.get("rtt", ""), akita_mean_used=k.get("mean_used", ""),
                                 akita_frac_full=k.get("frac_full", ""), akita_gpm_Bpns_avg=k.get("bw_gpm", ""),
                                 akita_link_mean=k.get("link_mean", ""), akita_link_max=k.get("link_max", ""),
                                 akita_link_window_max=k.get("link_window_max", ""),
                                 akita_total_time_us=k.get("total_time_us", "")))
    wr(rows, "compare_E1_closed.csv")
    # E2a: open loop at akita rates
    rows = []
    for sc in ("w64b384", "w256b48m", "w2048b48"):
        for wl in SIX:
            r = runs.get(f"E2a_{wl}_{sc}")
            if not r or r["rc"] != "0":
                continue
            k = ak[(sc, wl)]
            rows.append(dict(scenario=sc, workload=wl,
                             astra_fwd=F(r["fwd_mean"]), akita_fwd=k["fwd"],
                             astra_ret=F(r["ret_mean"]), akita_ret_net=k["ret_net"],
                             astra_rtt=F(r["rtt_mean"]), akita_rtt=k["rtt"], akita_owner_queue=k["owner_queue"],
                             astra_link_mean=F(r["link_util_mean"]), akita_link_mean=k["link_mean"],
                             astra_link_max=F(r["link_util_max"]), astra_link_max_at=r["link_max_at"],
                             akita_link_max=k["link_max"], akita_link_max_at=k["link_max_at"],
                             astra_window_max=F(r["link_window_max"]), akita_window_max=k["link_window_max"],
                             astra_hot07=F(r["hot07_util"]), akita_hot07=k["hot07"]))
    wr(rows, "compare_E2a_matched.csv")
    # E2b: sweep
    rows = []
    for name, r in sorted(runs.items()):
        if r.get("exp") != "E2b" or r["rc"] != "0":
            continue
        rows.append(dict(traffic=r["traffic"], bw=r["bw"], rho_pred=F(r["rho_pred"]),
                         offered_per_ns=F(r["n_req"]) / F(r["dur_ns"]), accepted_per_ns=F(r["thr_total"]),
                         link_max=F(r["link_util_max"]), link_max_at=r["link_max_at"],
                         rtt_mean=F(r["rtt_mean"]), rtt_p99=F(r["rtt_p99"]), ret_mean=F(r["ret_mean"]),
                         tail_ns=F(r["end_ns"]) - F(r["dur_ns"])))
    wr(rows, "compare_E2b_sweep.csv")
    # E3: Complete traffic (option A) vs baseline traffic, closed loop
    akc = akita_complete()
    rows = []
    for wl in SIX:
        for bw in ("b384", "b48"):
            for W in (64, 256, 448):
                b, c = runs.get(f"E1_{wl}_{bw}_W{W}"), runs.get(f"E3_{wl}_{bw}_W{W}")
                if not b or not c or b["rc"] != "0" or c["rc"] != "0":
                    continue
                cond = E1MAP[(bw, W)]
                kb, kc = ak.get((cond, wl), {}), akc.get((cond, wl), {})
                lb, lc = F(b["logical_reads_per_ns"]), F(c["logical_reads_per_ns"])
                rows.append(dict(workload=wl, bw=bw, W=W, akita_cond=cond,
                                 astra_speedup=lc / lb if lb else 0,
                                 akita_speedup=kc.get("speedup", ""),
                                 astra_base_logical_per_ns=lb, astra_cplt_logical_per_ns=lc,
                                 astra_base_rtt=F(b["rtt_mean"]), astra_cplt_rtt=F(c["rtt_mean"]),
                                 astra_base_hot=F(b["link_util_max"]), astra_cplt_hot=F(c["link_util_max"]),
                                 astra_base_inject0=F(b["inject0_util"]), astra_cplt_inject0=F(c["inject0_util"]),
                                 akita_base_link_max=kb.get("link_max", ""), akita_cplt_link_max=kc.get("link_max", ""),
                                 akita_base_rtt=kb.get("rtt", ""), akita_cplt_rtt=kc.get("rtt", "")))
    wr(rows, "compare_E3_mechanisms.csv")


def wr(rows, name):
    if not rows:
        return
    with open(os.path.join(A, name), "w") as f:
        w = csv.DictWriter(f, list(rows[0])); w.writeheader()
        for r in rows:
            w.writerow({k: (f"{v:.4g}" if isinstance(v, float) else v) for k, v in r.items()})
    print(name, len(rows))


if __name__ == "__main__":
    main()
