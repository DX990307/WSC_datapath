"""E4: ASTRA-sim counterpart of the T14 platbench incast (P18 calibration).
Owner tile 25 (driver 25); readers = the k nearest GPMs (hops, then tile id),
as in platbench runIncast. shared: every reader reads the same 1 MB
(16384 lines); disjoint: each reads 1 MB / k. Closed loop W = 64, owner 45 ns.
usage: python3 mk_incast.py runs_dir"""
import os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import mk_runs as M  # noqa: E402

OWNER, LINES = 25, 16384


def main(d):
    others = sorted((t for t in M.G if t != OWNER), key=lambda t: (M.T.hops(t, OWNER), t))
    n = 0
    for bw in ("b384", "b48"):
        for k in (1, 2, 4, 8, 16, 47):
            for mode in ("shared", "disjoint"):
                per = LINES if mode == "shared" else LINES // k
                seq = {str(r): [OWNER] * per for r in others[:k]}
                M.put(d, f"E4_inc-k{k}-{mode}_{bw}", dict(exp="E4", traffic=f"incast-{mode}", k=k, bw=bw, W=64,
                                                          bytes=64 * per * k, owner_ns=M.SYN_OWNER_NS),
                      dict(npus=49, W=64, req_bytes=16, rsp_bytes=96, owner_delay=round(M.SYN_OWNER_NS * M.TPN), seq=seq))
                n += 1
    print(n, "runs")


if __name__ == "__main__":
    main(sys.argv[1])
