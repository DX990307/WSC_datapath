"""Incast throughput (B/ns of data): akita platbench (current binary, b384/b48;
old 768 GB/s data) vs ASTRA-sim E4. Writes calibration_incast.csv."""
import csv, glob, os
HERE = os.path.dirname(os.path.abspath(__file__)); A = os.path.join(HERE, "..")
ak = {}
for bw, tag in (("b384", "24"), ("b48", "3")):
    for f in glob.glob(os.path.join(A, "mgpusim_calib", f"inc-k*_bw{tag}.csv")):
        for r in csv.DictReader(open(f)):
            ak[(bw, int(r["k"]), r["mode"])] = float(r["bandwidth_B_per_cycle"])
for r in csv.DictReader(open(os.path.join(A, "..", "platform_mi200", "incast.csv"))):
    ak[("old768", int(r["k"]), r["mode"])] = float(r["bandwidth_B_per_cycle"])
astra = {}
for r in csv.DictReader(open(os.path.join(A, "runs.csv"))):
    if r.get("exp") == "E4" and r["rc"] == "0":
        astra[(r["bw"], int(r["k"]), r["traffic"].split("-")[1])] = float(r["bytes"]) / float(r["end_ns"])
out = open(os.path.join(A, "calibration_incast.csv"), "w")
w = csv.writer(out)
w.writerow(["mode", "k", "akita_old768", "akita_b384", "astra_b384", "akita_b48", "astra_b48"])
for mode in ("shared", "disjoint"):
    for k in (1, 2, 4, 8, 16, 47):
        g = lambda d, key: f"{d[key]:.1f}" if key in d else ""
        w.writerow([mode, k, g(ak, ("old768", k, mode)), g(ak, ("b384", k, mode)), g(astra, ("b384", k, mode)),
                    g(ak, ("b48", k, mode)), g(astra, ("b48", k, mode))])
out.close()
print(open(os.path.join(A, "calibration_incast.csv")).read())
