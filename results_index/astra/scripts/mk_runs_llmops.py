"""E5: LLM / DLRM operators (paged attention, MoE, GEMM, embedding bag) on
ASTRA-sim, baseline vs Complete traffic (P18). Both shapes come from the
akita main-configuration runs (w64b384), so only (b384, W=64) is a direct
comparison; the other (bw, W) cells reuse that traffic shape.
usage: python3 mk_runs_llmops.py runs_dir"""
import os, sys, zlib
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import mk_runs as M  # noqa: E402
import mk_runs_complete as MC  # noqa: E402
import traffic_complete as C  # noqa: E402

OPS = ["pa-b14s7p32", "moe-t64", "mm-g12", "embbag-b512"]
SC = "w64b384"


def main(d):
    n = 0
    for wl in OPS:
        mb = M.T.matrix(SC, wl)
        wr = M.akita_writes(SC, wl)
        sc_ = C.shape(SC, wl)
        for bw in ("b384", "b48"):
            for W in (64, 256, 448):
                n_per = max(10 * W, 3000)
                seq = M.closed_spec(mb["counts"], wr, mb["owner_ns"], W, n_per, zlib.crc32(f"E5b{wl}{bw}{W}".encode()))
                M.put(d, f"E5b_{wl}_{bw}_W{W}", dict(exp="E5b", traffic=wl, bw=bw, W=W, n_per=n_per, shape=SC,
                                                     owner_ns=mb["owner_ns"]),
                      dict(npus=49, W=W, req_bytes=16, rsp_bytes=96, owner_delay=round(mb["owner_ns"] * M.TPN), seq=seq))
                seq, factor = MC.closed_spec(sc_, W, n_per, zlib.crc32(f"E5c{wl}{bw}{W}".encode()))
                M.put(d, f"E5c_{wl}_{bw}_W{W}", dict(exp="E5c", traffic=wl, bw=bw, W=W, n_per=n_per, shape=SC,
                                                     owner_ns=sc_["owner_ns"], logical_factor=factor),
                      dict(npus=49, W=W, req_bytes=16, rsp_bytes=96, owner_delay=round(sc_["owner_ns"] * M.TPN), seq=seq))
                n += 2
    print(n, "runs")


if __name__ == "__main__":
    main(sys.argv[1])
