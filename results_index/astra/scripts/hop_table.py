"""Zero-load per-hop one-way latency (ns) from an ASTRA-sim hop calibration run."""
import csv, sys
TICK = 1 / 24
W = 7
def hops(a, b): return abs(a % W - b % W) + abs(a // W - b // W)
rows = list(csv.DictReader(open(sys.argv[1])))
out = {}
for r in rows:
    s, d, b = int(r["src"]), int(r["dst"]), int(r["bytes"])
    lat = (int(r["arrival_time"]) - int(r["send_time"])) * TICK
    h = hops(s, d)
    out.setdefault(h, {}).setdefault(b, []).append(lat)
print("hops,req_ns,rsp_ns,rtt_ns")
for h in sorted(out):
    q = sum(out[h][16]) / len(out[h][16]); p = sum(out[h][96]) / len(out[h][96])
    print(f"{h},{q:.2f},{p:.2f},{q+p:.2f}")
