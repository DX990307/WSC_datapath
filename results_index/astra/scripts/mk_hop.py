"""Zero-load hop calibration: tile 0 reads N lines from one owner at each distance h=1..12, W=1."""
import json, sys
N = 20
seq = []
for h in range(1, 13):
    o = h if h <= 6 else 6 + 7 * (h - 6)
    seq += [o] * N
json.dump({"npus": 49, "W": 1, "req_bytes": 16, "rsp_bytes": 96, "owner_delay": 0,
           "seq": {"0": seq}}, open(sys.argv[1], "w"))
