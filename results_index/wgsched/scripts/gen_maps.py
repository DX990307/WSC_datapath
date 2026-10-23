#!/usr/bin/env python3
"""wg-to-data: build per-kernel WG -> GPM maps from pass-1 statistics.

Usage: gen_maps.py <pass1 results dir> <out map file> [eps]

Inputs (written by -report-wgaff):
  stats/metrics_wgaff.csv   kernel,wg,owner_dev,reads,writes,bytes
  stats/metrics_wglaunch.csv kernel,total_wg,mapped,per_gpm
Rule per kernel (N WGs, 48 GPMs, cap = ceil(N/48*(1+eps))):
  WGs ordered by the share of their bytes on their top GPM (desc), then bytes;
  each goes to the GPM holding most of its bytes if it has room, else to the
  GPM with room minimising sum(bytes(owner) * hops(gpm, owner)).
  WGs with no recorded access keep their default GPM if it has room, else
  go to the least loaded GPM.
Also writes <out>.summary.csv: per kernel N, cap, local byte fraction under
the default and the map, max/avg WGs per GPM under default and map.
"""
import csv, math, sys, collections

NG = 48
W = 7


def tile(i):
    return i if i < 24 else i + 1


XY = [(tile(i) % W, tile(i) // W) for i in range(NG)]
HOPS = [[abs(XY[a][0] - XY[b][0]) + abs(XY[a][1] - XY[b][1]) for b in range(NG)] for a in range(NG)]


def default_assign(per_gpm):
    out = []
    for g, n in enumerate(per_gpm):
        out += [g] * n
    return out


def main():
    rd, outp = sys.argv[1], sys.argv[2]
    eps = float(sys.argv[3]) if len(sys.argv) > 3 else 0.1
    launches = {}
    for r in csv.DictReader(open(f"{rd}/stats/metrics_wglaunch.csv")):
        launches[int(r["kernel"])] = (int(r["total_wg"]), [int(x) for x in r["per_gpm"].split()])
    aff = collections.defaultdict(lambda: collections.defaultdict(dict))
    for r in csv.DictReader(open(f"{rd}/stats/metrics_wgaff.csv")):
        o = int(r["owner_dev"]) - 1
        if not 0 <= o < NG:
            continue
        aff[int(r["kernel"])][int(r["wg"])][o] = int(r["bytes"])
    lines, summ = [], []
    for k in sorted(launches):
        N, per = launches[k]
        dflt = default_assign(per)
        if len(dflt) != N:
            continue
        cap = max(1, math.ceil(N / NG * (1 + eps)))
        load = [0] * NG
        asg = [-1] * N
        wgs = aff.get(k, {})
        order = []
        for w, d in wgs.items():
            if w >= N or not d:
                continue
            tot = sum(d.values())
            top = max(d.values())
            order.append((-(top / tot) if tot else 0, -tot, w))
        order.sort()
        for _, _, w in order:
            d = wgs[w]
            best = max(d, key=lambda o: (d[o], -o))
            if load[best] < cap:
                g = best
            else:
                g, bc = -1, None
                for c in range(NG):
                    if load[c] >= cap:
                        continue
                    cost = sum(b * HOPS[c][o] for o, b in d.items())
                    key = (cost, load[c])
                    if bc is None or key < bc:
                        g, bc = c, key
            asg[w] = g
            load[g] += 1
        for w in range(N):
            if asg[w] >= 0:
                continue
            g = dflt[w]
            if load[g] >= cap:
                g = min(range(NG), key=lambda c: (load[c], c))
            asg[w] = g
            load[g] += 1
        tot = loc_d = loc_m = 0
        hop_d = hop_m = 0
        for w, d in wgs.items():
            if w >= N:
                continue
            for o, b in d.items():
                tot += b
                hop_d += b * HOPS[dflt[w]][o]
                hop_m += b * HOPS[asg[w]][o]
                if o == dflt[w]:
                    loc_d += b
                if o == asg[w]:
                    loc_m += b
        avg = N / NG
        summ.append(dict(kernel=k, total_wg=N, cap=cap, bytes=tot,
                         local_default=round(loc_d / tot, 4) if tot else "",
                         local_map=round(loc_m / tot, 4) if tot else "",
                         byte_hops_ratio=round(hop_m / hop_d, 4) if hop_d else "",
                         max_over_avg_default=round(max(per) / avg, 3),
                         max_over_avg_map=round(max(load) / avg, 3),
                         active_gpm_default=sum(1 for x in per if x),
                         active_gpm_map=sum(1 for x in load if x)))
        lines.append(f"{k} {N} " + " ".join(map(str, asg)))
    with open(outp + ".tmp", "w") as f:
        f.write("# wg-to-data map: kernel total_wg gpm_of_wg...\n")
        f.write("\n".join(lines) + "\n")
    with open(outp + ".summary.csv", "w") as f:
        if summ:
            w = csv.DictWriter(f, fieldnames=list(summ[0]))
            w.writeheader()
            w.writerows(summ)
    import os
    os.replace(outp + ".tmp", outp)


if __name__ == "__main__":
    main()
