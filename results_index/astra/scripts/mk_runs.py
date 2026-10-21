"""Build run specs for the P18 ASTRA-sim cross-check.

E1  closed loop: every active requester keeps W reads/writes in flight
    (N per requester), traffic x {b384, b48} x W in {64, 256, 448}.
E2a open loop at the akita average rates (Poisson, 100 us) for the three
    akita scenarios (w64b384 @ b384, w256b48m @ b48, w2048b48 @ b48).
E2b open-loop sweep: w64b384 traffic shapes scaled so that the predicted
    busiest link has utilisation rho in RHO, at b384 and b48.
Traffic: the six dm workloads + uniform all-to-all + hotspot (47 GPMs read tile 0).
1 tick = 1/24 ns. Read = 16 B request / 96 B response; write = 96 B / 16 B.
usage: python3 mk_runs.py runs_dir
"""
import json, os, random, sys, zlib
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import traffic as T  # noqa: E402

TPN = 24
BPT = {"b384": 16, "b48": 2}
REQ, RSP = 16, 96
WS = (64, 256, 448)
RHO = (0.1, 0.3, 0.5, 0.7, 0.9, 1.1)
OPEN_NS = 100_000
MAX_REQS = 1_000_000  # cap on requests per open-loop run (shorter window if needed)
SYN_OWNER_NS = 45.0
G = T.GPMS


def shapes():
    """traffic name -> (pair weights {(r,o): reads}, writes per requester, owner_ns)"""
    out = {}
    for wl in T.load.SIX:
        m = T.matrix("w64b384", wl)
        out[wl] = (m["counts"], {}, m["owner_ns"])
    out["uniform"] = ({(r, o): 1.0 for r in G for o in G if r != o}, {}, SYN_OWNER_NS)
    out["hotspot"] = ({(r, 0): 1.0 for r in G if r != 0}, {}, SYN_OWNER_NS)
    return out


def akita_writes(sc, wl):
    o = T.runs()[(sc, wl, "baseline")]
    return {t: max(0.0, T.F(o["gpm"][t].get("outgoing_trans_count", 0)) -
                   T.F(o["gpm"][t].get("rdma_observed_remote_reads", 0))) for t in G}


def rows(counts):
    r = {}
    for (a, b), n in counts.items():
        if n > 0:
            r.setdefault(a, []).append((b, n))
    return r


def sampler(row, rng):
    owners = [o for o, _ in row]
    cum, s = [], 0.0
    for _, n in row:
        s += n; cum.append(s)
    import bisect
    return lambda: owners[min(bisect.bisect(cum, rng.random() * s), len(owners) - 1)]


def entry(o, is_write, t=0):
    return [o, RSP, REQ, t] if is_write else [o, REQ, RSP, t]


def closed_spec(counts, wr, owner_ns, W, n_per, seed):
    rng = random.Random(seed)
    R = rows(counts)
    tot = {r: sum(n for _, n in R[r]) for r in R}
    mean = sum(tot.values()) / len(tot)
    seq = {}
    for r in sorted(R):
        if tot[r] < 0.05 * mean:
            continue
        pick = sampler(R[r], rng)
        pw = wr.get(r, 0) / (wr.get(r, 0) + tot[r]) if wr else 0.0
        seq[str(r)] = [entry(pick(), rng.random() < pw) for _ in range(n_per)]
    return seq


def open_spec(counts, wr, rate_scale, dur_ns, seed):
    """counts are reads per ns per pair after rate_scale; Poisson per requester."""
    rng = random.Random(seed)
    R = rows(counts)
    total = sum(n for n in counts.values()) * rate_scale + sum(wr.values()) * rate_scale
    dur_ns = min(dur_ns, MAX_REQS / total) if total > 0 else dur_ns
    seq = {}
    for r in sorted(R):
        lam = sum(n for _, n in R[r]) * rate_scale
        lw = wr.get(r, 0) * rate_scale
        if lam + lw <= 0:
            continue
        pick = sampler(R[r], rng)
        t, ev = 0.0, []
        while True:
            t += rng.expovariate(lam + lw)
            if t >= dur_ns:
                break
            ev.append(entry(pick(), rng.random() < lw / (lam + lw), int(t * TPN)))
        if ev:
            seq[str(r)] = ev
    return seq, dur_ns


def max_link_flits_per_ns(counts, wr):
    reads = T.link_flits(counts, 1, 6)
    # writes: spread like the requester's reads
    R = rows(counts)
    wc = {}
    for r, row in R.items():
        s = sum(n for _, n in row)
        for o, n in row:
            wc[(r, o)] = wr.get(r, 0) * n / s if s else 0
    wf = T.link_flits(wc, 6, 1)
    tot = {k: reads.get(k, 0) + wf.get(k, 0) for k in set(reads) | set(wf)}
    return max(tot.values())


def put(d, name, meta, spec):
    os.makedirs(os.path.join(d, name), exist_ok=True)
    json.dump(spec, open(os.path.join(d, name, "spec.json"), "w"))
    json.dump(meta, open(os.path.join(d, name, "meta.json"), "w"), indent=1)


def main(d):
    S = shapes()
    n = 0
    for tr, (counts, _, owner_ns) in S.items():
        wr = akita_writes("w64b384", tr) if tr in T.load.SIX else {}
        for bw in BPT:
            for W in WS:
                n_per = max(10 * W, 3000)
                seq = closed_spec(counts, wr, owner_ns, W, n_per, seed=zlib.crc32(f"{tr}{bw}{W}".encode()))
                put(d, f"E1_{tr}_{bw}_W{W}", dict(exp="E1", traffic=tr, bw=bw, W=W, n_per=n_per, owner_ns=owner_ns),
                    dict(npus=49, W=W, req_bytes=REQ, rsp_bytes=RSP, owner_delay=round(owner_ns * TPN), seq=seq))
                n += 1
            # E2b sweep
            cap = BPT[bw] / 16  # flits per tick -> per ns below
            fpn = max_link_flits_per_ns(counts, wr)  # flits per unit of counts
            for rho in RHO:
                scale = rho * cap * TPN / fpn  # counts * scale = reads per ns
                seq, dur = open_spec(counts, wr, scale, OPEN_NS, seed=zlib.crc32(f"{tr}{bw}{rho}".encode()))
                put(d, f"E2b_{tr}_{bw}_rho{rho}", dict(exp="E2b", traffic=tr, bw=bw, rho_pred=rho, dur_ns=dur,
                                                       owner_ns=owner_ns, open=True),
                    dict(npus=49, W=0, req_bytes=REQ, rsp_bytes=RSP, open=True,
                         owner_delay=round(owner_ns * TPN), seq=seq))
                n += 1
    for sc, bw in (("w64b384", "b384"), ("w256b48m", "b48"), ("w2048b48", "b48")):
        for wl in T.load.SIX:
            m = T.matrix(sc, wl)
            wr = akita_writes(sc, wl)
            end = m["end_ns"]
            seq, dur = open_spec(m["counts"], wr, 1.0 / end, OPEN_NS, seed=zlib.crc32(f"{sc}{wl}".encode()))
            put(d, f"E2a_{wl}_{sc}", dict(exp="E2a", traffic=wl, bw=bw, scenario=sc, dur_ns=dur,
                                          owner_ns=m["owner_ns"], akita_end_ns=end, open=True),
                dict(npus=49, W=0, req_bytes=REQ, rsp_bytes=RSP, open=True,
                     owner_delay=round(m["owner_ns"] * TPN), seq=seq))
            n += 1
    print(n, "runs")


if __name__ == "__main__":
    main(sys.argv[1])
