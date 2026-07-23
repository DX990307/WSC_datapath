#!/usr/bin/env python3
"""Plot paper-style O1--O6 figures from analyze_observations.py outputs."""

import argparse
import csv
import math
from collections import defaultdict
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np
from matplotlib.colors import LinearSegmentedColormap
from matplotlib.patches import Patch


BLUE = {
    50: "#EAF7FA", 100: "#D6EFF5", 200: "#ADDEEB",
    300: "#83CEE2", 400: "#5ABED8", 500: "#31ADCE",
    600: "#278BA5", 700: "#1D687C", 800: "#144552",
    900: "#0A2329", 950: "#051115",
}

ORANGE = {
    50: "#FDF0E7", 100: "#FBE0D0", 200: "#F8C2A0",
    300: "#F4A371", 400: "#F18541", 500: "#ED6612",
    600: "#BE520E", 700: "#8E3D0B", 800: "#5F2907",
    900: "#2F1404", 950: "#180A02",
}

COLORS = {
    "blue": BLUE[500],
    "orange": ORANGE[400],
    "green": BLUE[300],
    "red": ORANGE[600],
    "yellow": ORANGE[200],
    "gray": "#7E7F81",
    "dark": BLUE[950],
}

ORANGE_HEATMAP = LinearSegmentedColormap.from_list(
    "cubatch_orange",
    [
        ORANGE[50], ORANGE[100], ORANGE[200], ORANGE[300],
        ORANGE[400], ORANGE[500], ORANGE[600], ORANGE[700],
    ],
)

BENCH_LABEL = {
    "aes": "AES", "bitonicsort": "BT", "fastwalshtransform": "FWT",
    "fft": "FFT", "fir": "FIR", "floydwarshall": "FWS",
    "im2col": "I2C", "kmeans": "KM", "matrixmultiplication": "MM",
    "matrixtranspose": "MT", "pagerank": "PR", "relu": "RELU",
    "simpleconvolution": "SC", "spmv": "SPMV",
}


def read_rows(path):
    with path.open(newline="") as stream:
        return list(csv.DictReader(stream))


def f(row, key):
    try:
        value = float(row[key])
        return value if math.isfinite(value) else 0.0
    except (KeyError, ValueError):
        return 0.0


def benches(rows):
    return sorted({r["benchmark"] for r in rows}, key=lambda b: list(BENCH_LABEL).index(b) if b in BENCH_LABEL else 99)


def style(ax, ylabel=None):
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.7, zorder=0)
    ax.tick_params(labelsize=8)
    if ylabel:
        ax.set_ylabel(ylabel, fontsize=9)


def save(fig, out, name):
    fig.tight_layout()
    fig.savefig(out / f"{name}.png", dpi=300, bbox_inches="tight")
    fig.savefig(out / f"{name}.pdf", bbox_inches="tight")
    plt.close(fig)


def plot_o1(root, out):
    all_rows = read_rows(root / "o1_component_latency_breakdown.csv")
    l2_rows = read_rows(root / "o1_l2_demand_read_miss_rate.csv")
    # The component-level file already contains only completed demand-read
    # MSHR leaders and excludes both L1 and L2 MSHR wait. Local bars study
    # HBM-reaching leaders. Remote bars include both owner-L2 hits and
    # owner-HBM leaders because both traverse the wafer-scale remote path.
    rows = [r for r in all_rows if f(r, "demand_read_paths") > 0]
    workload_order = [
        ("aes", "AES"), ("bitonicsort", "BT"),
        ("fastwalshtransform", "FWT"), ("fft", "FFT"), ("fir", "FIR"),
        ("floydwarshall", "FWS"), ("im2col", "I2C"), ("kmeans", "KM"),
        ("matrixmultiplication", "MM"), ("matrixtranspose", "MT"),
        ("pagerank", "PR"), ("relu", "RELU"),
        ("simpleconvolution", "SC"), ("spmv", "SPMV"),
    ]
    bs = [name for name, _ in workload_order]

    groups = [
        ("L1 Cache", "L1 Cache", COLORS["blue"]),
        ("L2 Cache", "L2 Cache", COLORS["green"]),
        ("HBM", "HBM", COLORS["orange"]),
        ("Remote Communication", "Remote Communication", COLORS["red"]),
    ]

    def coarse_component(row):
        component = row["component"]
        if component in {"Requester RDMA", "Owner RDMA",
                         "Remote Communication"}:
            return "Remote Communication"
        return component

    x_step = .82
    x = np.arange(len(bs), dtype=float) * x_step
    group_width = .56
    width = group_width / 2
    fig, (ax, mx) = plt.subplots(
        2, 1, figsize=(3.45, 2.25), sharex=True,
        gridspec_kw={"height_ratios": [1.8, 1.0], "hspace": .10},
    )
    path_specs = [
        ("local", {"dram"}, -width / 2),
        ("remote", {"l2", "dram"}, width / 2),
    ]
    path_present = {"local": np.zeros(len(bs), dtype=bool),
                    "remote": np.zeros(len(bs), dtype=bool)}
    for component_key, label, color in groups:
        for path_key, sources, offset in path_specs:
            values = []
            for b in bs:
                group = [r for r in rows if r["benchmark"] == b
                         and r["route"] == path_key
                         and r["source"] in sources]
                component_ns = sum(
                    f(r, "mean_ns") * f(r, "demand_read_paths")
                    for r in group
                    if coarse_component(r) == component_key
                )
                classified_ns = sum(
                    f(r, "mean_ns") * f(r, "demand_read_paths")
                    for r in group
                )
                path_present[path_key][bs.index(b)] = classified_ns > 0
                values.append(100 * component_ns / classified_ns
                              if classified_ns else 0)
            values = np.array(values)
            bottom_key = (path_key,)
            if not hasattr(plot_o1, "_bottoms"):
                plot_o1._bottoms = {}
            bottom = plot_o1._bottoms.setdefault(bottom_key, np.zeros(len(bs)))
            ax.bar(x + offset, values, bottom=bottom, color=color,
                   label=label if path_key == "local" else None,
                   width=width * .94, edgecolor="none", linewidth=0, zorder=3)
            plot_o1._bottoms[bottom_key] = bottom + values
    # Avoid retaining state if the function is called again in one process.
    del plot_o1._bottoms

    ax.set_ylim(0, 100)
    ax.set_ylabel("Path latency (%)", fontsize=6.0, labelpad=.5)
    ax.set_yticks([0, 50, 100])
    ax.set_yticklabels(["0", "50", "100"])
    ax.set_xticks(x)
    ax.set_xticklabels([])
    ax.set_xlim(x[0] - group_width * .62, x[-1] + group_width * .62)
    ax.grid(axis="y", color="#dddddd", linewidth=.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=.7)
    for path_key, _, offset in path_specs:
        short = "L" if path_key == "local" else "R"
        for i, xpos in enumerate(x):
            if path_present[path_key][i]:
                ax.text(xpos + offset, 2.0, short, ha="center", va="bottom",
                        fontsize=4.8, color="white", fontweight="bold", zorder=5)
            elif path_key == "remote":
                ax.text(xpos + offset, 1.0, "--", ha="center", va="bottom",
                        fontsize=5.2, color=COLORS["gray"], zorder=5)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(.7)
    handles = [Patch(facecolor=color, edgecolor="none", label=label)
               for _, label, color in groups]
    ax.legend(handles=handles, loc="lower left",
              bbox_to_anchor=(.02, 1.06, .96, .16), ncol=2, mode="expand",
              frameon=False, columnspacing=.9, handlelength=1.0,
              handletextpad=.45, fontsize=5.7)

    # The lower panel reports the demand-read miss probability at the local
    # or owner L2 for the same completed-leader population. A missing remote
    # bar means that no qualifying remote path was observed; a real zero is
    # shown explicitly so that absence is not confused with an all-hit trace.
    miss_present = {
        "local": np.zeros(len(bs), dtype=bool),
        "remote": np.zeros(len(bs), dtype=bool),
    }
    miss_values = {
        "local": np.zeros(len(bs), dtype=float),
        "remote": np.zeros(len(bs), dtype=float),
    }
    for path_key in ("local", "remote"):
        for i, benchmark in enumerate(bs):
            selected = [
                row for row in l2_rows
                if row["benchmark"] == benchmark
                and row["route"] == path_key
                and f(row, "demand_read_paths") > 0
            ]
            samples = sum(f(row, "demand_read_paths") for row in selected)
            misses = sum(f(row, "l2_read_misses") for row in selected)
            if samples:
                miss_present[path_key][i] = True
                miss_values[path_key][i] = 100 * misses / samples

    for path_key, _, offset in path_specs:
        color = BLUE[300] if path_key == "local" else ORANGE[300]
        values = miss_values[path_key]
        present = miss_present[path_key]
        mx.bar(
            x[present] + offset, values[present], width=width * .94,
            color=color, edgecolor="none", linewidth=0, zorder=3,
        )
        for i, xpos in enumerate(x):
            short = "L" if path_key == "local" else "R"
            if present[i] and values[i] == 0:
                mx.text(xpos + offset, 2.0, short + "0", ha="center",
                        va="bottom", fontsize=4.6, color=COLORS["dark"],
                        zorder=5)
            elif present[i]:
                mx.text(xpos + offset, min(2.0, values[i] * .45), short,
                        ha="center", va="bottom", fontsize=4.8,
                        color="white" if values[i] >= 10 else COLORS["dark"],
                        fontweight="bold", zorder=5)
            elif path_key == "remote" and not present[i]:
                mx.text(xpos + offset, 1.0, "--", ha="center", va="bottom",
                        fontsize=5.2, color=COLORS["gray"], zorder=5)

    mx.set_ylim(0, 100)
    mx.set_yticks([0, 50, 100])
    mx.set_yticklabels(["0", "50", "100"])
    mx.set_ylabel("L2 miss rate (%)", fontsize=6.0, labelpad=.5)
    mx.set_xticks(x)
    mx.set_xticklabels(
        [label for _, label in workload_order], rotation=30,
        ha="right", rotation_mode="anchor", fontsize=6.0,
    )
    mx.set_xlim(x[0] - group_width * .62, x[-1] + group_width * .62)
    mx.grid(axis="y", color="#dddddd", linewidth=.45, zorder=1)
    mx.tick_params(axis="both", labelsize=6.0, length=2.4, width=.7)
    for spine in mx.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(.7)
    save(fig, out, "o1_memory_path_latency")


def plot_o2(root, out):
    rows = read_rows(root / "o2_adjacent_line_short_window_cdf.csv")
    bs = benches(rows)
    windows = [0, 4, 16, 64]
    aggregate = []
    for window in windows:
        selected = [r for r in rows if int(f(r, "window_l1_cycles")) == window]
        count = sum(f(r, "count") for r in selected)
        total = sum(f(r, "total_l2_miss_reads") for r in selected)
        aggregate.append(100 * count / total if total else 0)
    matrix = [aggregate]
    for b in bs:
        by_window = {int(f(r, "window_l1_cycles")):
                     100 * f(r, "cdf_fraction_all_requests")
                     for r in rows if r["benchmark"] == b}
        matrix.append([by_window.get(window, 0) for window in windows])
    fig, hx = plt.subplots(figsize=(3.45, 1.95))
    im = hx.imshow(matrix, aspect="auto", cmap=ORANGE_HEATMAP,
                   vmin=0, vmax=50)
    hx.set_xticks(range(len(windows)), [str(window) for window in windows],
                  fontsize=6)
    hx.set_yticks(range(len(bs) + 1),
                  ["Weighted"] + [BENCH_LABEL.get(b, b) for b in bs],
                  fontsize=5.6)
    hx.axhline(.5, color="white", linewidth=1.0)
    hx.set_xlabel("Nearest sibling window (L1 cycles)", fontsize=6.5)
    cbar = fig.colorbar(im, ax=hx, fraction=.055, pad=.025)
    cbar.set_ticks([0, 25, 50])
    cbar.ax.tick_params(labelsize=5.5)
    cbar.set_label("Requests (%)", fontsize=6)
    save(fig, out, "o2_adjacent_line_cdf")


def plot_o3(root, out):
    rows = [r for r in read_rows(root / "o3_physical_locality_cdf.csv") if r["distance"] == "cycles"]
    relations = ["same_access_unit", "same_row_different_column", "same_bank_different_row", "different_bank_same_controller"]
    labels = ["Same 128-B unit", "Same row", "Same bank/new row", "Different bank"]
    colors = [COLORS["blue"], COLORS["green"], COLORS["orange"], COLORS["red"]]
    fig, (ax, hx) = plt.subplots(
        1, 2, figsize=(3.45, 1.72),
        gridspec_kw={"width_ratios": [1.0, 1.08]})
    for rel, label, color in zip(relations, labels, colors):
        by_window = defaultdict(lambda: [0,0])
        for r in rows:
            if r["relation"] == rel:
                w=int(float(r["window"])); by_window[w][0] += int(float(r["count"])); by_window[w][1] += int(float(r["total_physical_read_arrivals"]))
        xs=sorted(by_window); ys=[100*by_window[x][0]/by_window[x][1] if by_window[x][1] else 0 for x in xs]
        ax.plot(xs, ys, marker="o", ms=2.0, lw=1.0, color=color, label=label)
    ax.set_xscale("symlog", linthresh=1, base=2)
    ax.set_xlabel("Window (cycles)", fontsize=5.7)
    ax.legend(fontsize=3.8, frameon=False, ncol=1, columnspacing=.5,
              handlelength=1.2, handletextpad=.25, loc="upper left")
    style(ax, "Weighted CDF (%)")
    ax.tick_params(labelsize=5.5)
    ax.yaxis.label.set_size(5.7)
    bs=benches(rows); target=16; matrix=[]
    for b in bs:
        line=[]
        for rel in relations:
            candidates=[r for r in rows if r["benchmark"]==b and r["relation"]==rel]
            chosen=min(candidates, key=lambda r: abs(f(r,"window")-target)) if candidates else None
            line.append(100*f(chosen,"cdf_fraction") if chosen else 0)
        matrix.append(line)
    im=hx.imshow(matrix, aspect="auto", cmap=ORANGE_HEATMAP,
                 vmin=0, vmax=100)
    hx.set_xticks(range(4), ["128B", "Row", "Bank", "Diff."],
                  rotation=30, ha="right", fontsize=4.3)
    hx.set_yticks(range(len(bs)), [BENCH_LABEL.get(b,b) for b in bs],
                  fontsize=3.8)
    hx.set_title(f"Within {target} cycles (%)", fontsize=5.4)
    cbar = fig.colorbar(im, ax=hx, fraction=.055, pad=.025)
    cbar.set_ticks([0, 50, 100])
    cbar.ax.tick_params(labelsize=4.2)
    save(fig, out, "o3_dram_physical_locality")


def plot_o4(root, out):
    rows=read_rows(root/"o4_remote_amplification.csv"); bs=benches(rows); x=np.arange(len(bs))
    byte_hops=[f(next(r for r in rows if r["benchmark"]==b),"byte_hops_per_logical_byte") for b in bs]
    latency=[f(next(r for r in rows if r["benchmark"]==b),"latency_mean_ns") for b in bs]
    fig, ax=plt.subplots(figsize=(3.45,1.62)); ax.bar(x,byte_hops,color=COLORS["red"],width=.7,zorder=2)
    ax2=ax.twinx(); ax2.plot(x,latency,color=COLORS["blue"],marker="o",lw=1.5,ms=3,label="Remote latency")
    ax.set_xticks(x,[BENCH_LABEL.get(b,b) for b in bs],rotation=40,ha="right",
                  fontsize=5.2)
    style(ax,"Byte-hops / logical byte")
    ax.yaxis.label.set_size(6.3); ax.tick_params(axis="y", labelsize=5.5)
    ax2.set_ylabel("Mean latency (ns)",fontsize=6.3,color=COLORS["blue"])
    ax.spines["right"].set_visible(False)
    ax2.spines["top"].set_visible(False)
    ax2.spines["bottom"].set_visible(False)
    ax2.spines["left"].set_visible(False)
    ax2.spines["right"].set_visible(True)
    ax2.spines["right"].set_linewidth(.7)
    ax2.spines["right"].set_color(COLORS["dark"])
    ax2.tick_params(axis="y",labelsize=5.5,colors=COLORS["blue"])
    for i, b in enumerate(bs):
        row = next(r for r in rows if r["benchmark"] == b)
        if f(row, "remote_requests") == 0:
            ax.text(i, .08, "--", ha="center", va="bottom",
                    fontsize=7, color=COLORS["gray"])
    save(fig,out,"o4_remote_amplification")


def plot_o5(root,out):
    exact=read_rows(root/"o5_exact_inflight_dedup.csv"); spatial=read_rows(root/"o5_remote_page_spatial_cdf.csv"); bs=benches(exact); x=np.arange(len(bs)); window=16
    e=[100*f(next(r for r in exact if r["benchmark"]==b),"exact_inflight_dedup_fraction") for b in bs]
    s=[]
    for b in bs:
        rs=[r for r in spatial if r["benchmark"]==b]; chosen=min(rs,key=lambda r:abs(f(r,"window_remote_cycles")-window)) if rs else None; s.append(100*f(chosen,"cdf_fraction_all_requests") if chosen else 0)
    fig,ax=plt.subplots(figsize=(3.45,1.62)); ax.bar(x-.19,e,.38,color=COLORS["blue"],label="Exact inflight",zorder=2); ax.bar(x+.19,s,.38,color=COLORS["orange"],label=f"Same page (≤{window} cyc.)",zorder=2)
    for i, b in enumerate(bs):
        row = next(r for r in exact if r["benchmark"] == b)
        if f(row, "read_requests") == 0:
            ax.text(i, 1.0, "--", ha="center", va="bottom",
                    fontsize=7, color=COLORS["gray"])
    ax.set_xticks(x,[BENCH_LABEL.get(b,b) for b in bs],rotation=40,ha="right",
                  fontsize=5.2)
    ax.legend(fontsize=5.1,frameon=False,ncol=2, columnspacing=.8,
              handletextpad=.35, loc="lower center",
              bbox_to_anchor=(.5, 1.0))
    style(ax,"Remote reads (%)")
    ax.yaxis.label.set_size(6.3); ax.tick_params(axis="y", labelsize=5.5)
    save(fig,out,"o5_remote_aggregation")


def plot_o6(root,out):
    reuse=read_rows(root/"o6_remote_reuse_summary.csv"); l2=read_rows(root/"o6_l2_headroom.csv"); bs=benches(l2); x=np.arange(len(bs))
    repeated=[]; recurring=[]; free=[]; read_counts=[]
    for b in bs:
        rr=next((r for r in reuse if r["benchmark"]==b),None); lr=next(r for r in l2 if r["benchmark"]==b)
        repeated.append(100*f(rr,"repeated_read_fraction") if rr else 0)
        recurring.append(100*f(rr,"reused_key_fraction") if rr else 0)
        read_counts.append(f(rr, "read_requests") if rr else 0)
        free.append(100*f(lr,"free_fraction_mean"))
    fig,ax=plt.subplots(figsize=(3.45,1.72))
    width=.25
    ax.bar(x-width,recurring,width,color=COLORS["yellow"],label="Recurring lines",zorder=2)
    ax.bar(x,repeated,width,color=COLORS["red"],label="Reads after first",zorder=2)
    ax.bar(x+width,free,width,color=COLORS["green"],label="Free L2",zorder=2)
    for i, count in enumerate(read_counts):
        if count == 0:
            ax.text(i, 1.0, "--", ha="center", va="bottom",
                    fontsize=7, color=COLORS["gray"])
    ax.legend(fontsize=4.4,frameon=False,ncol=3, columnspacing=.55,
              handletextpad=.3, loc="lower center",
              bbox_to_anchor=(.5, 1.0))
    ax.set_xticks(x,[BENCH_LABEL.get(b,b) for b in bs],rotation=40,ha="right",
                  fontsize=5.0)
    style(ax,"Share / free capacity (%)")
    ax.yaxis.label.set_size(5.8); ax.tick_params(axis="y", labelsize=5.2)
    save(fig,out,"o6_remote_reuse_l2_headroom")


def main():
    p=argparse.ArgumentParser(); p.add_argument("analysis_dir",type=Path); p.add_argument("--output-dir",type=Path); a=p.parse_args()
    out=a.output_dir or a.analysis_dir/"figures"; out.mkdir(parents=True,exist_ok=True)
    plt.rcParams.update({"font.family":"DejaVu Sans","axes.labelcolor":COLORS["dark"],"text.color":COLORS["dark"]})
    for fn in (plot_o1,plot_o2,plot_o3,plot_o4,plot_o5,plot_o6): fn(a.analysis_dir,out)
    print(f"wrote O1--O6 figures to {out}")


if __name__ == "__main__": main()
