"""E3 (option A): closed-loop runs with the akita Complete traffic (P18).
Same (bw, W) grid as E1. Packets carry k lines (batch-size histogram of the
requester); the logical reads per read packet of each requester is stored in
meta.json (logical_factor) so analyze.py can report logical-read throughput.
usage: python3 mk_runs_complete.py runs_dir"""
import bisect, os, random, sys, zlib
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import mk_runs as M  # noqa: E402
import traffic_complete as C  # noqa: E402

SHAPE = {("b384", 64): "w64b384", ("b384", 256): "w64b384", ("b384", 448): "w64b384",
         ("b48", 64): "w256b48m", ("b48", 256): "w256b48m", ("b48", 448): "w2048b48"}


def closed_spec(s, W, n_per, seed):
    rng = random.Random(seed)
    R = M.rows(s["counts"])
    tot = {r: sum(n for _, n in R[r]) for r in R}
    mean = sum(tot.values()) / len(tot)
    seq, factor = {}, {}
    for r in sorted(R):
        if tot[r] < 0.05 * mean:
            continue
        pick = M.sampler(R[r], rng)
        ks = [k for k in range(1, 9) if s["hist"][r][k] > 0]
        cum, acc = [], 0.0
        for k in ks:
            acc += s["hist"][r][k]; cum.append(acc)
        pw = s["writes"][r] / (s["writes"][r] + s["packets"][r])
        ev = []
        for _ in range(n_per):
            o = pick()
            if rng.random() < pw:
                ev.append([o, 96, 16, 0])
            else:
                k = ks[min(bisect.bisect(cum, rng.random() * acc), len(ks) - 1)]
                ev.append([o, C.req_bytes(k), C.rsp_bytes(k), 0])
        seq[str(r)] = ev
        factor[str(r)] = s["logical"][r] / s["packets"][r]
    return seq, factor


def main(d):
    n = 0
    for wl in M.T.load.SIX:
        for (bw, W), sc in SHAPE.items():
            s = C.shape(sc, wl)
            n_per = max(10 * W, 3000)
            seq, factor = closed_spec(s, W, n_per, zlib.crc32(f"E3{wl}{bw}{W}".encode()))
            M.put(d, f"E3_{wl}_{bw}_W{W}", dict(exp="E3", traffic=wl, bw=bw, W=W, n_per=n_per, shape=sc,
                                                owner_ns=s["owner_ns"], logical_factor=factor),
                  dict(npus=49, W=W, req_bytes=16, rsp_bytes=96, owner_delay=round(s["owner_ns"] * M.TPN), seq=seq))
            n += 1
    print(n, "runs")


if __name__ == "__main__":
    main(sys.argv[1])
