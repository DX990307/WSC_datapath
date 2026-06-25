#!/usr/bin/env python3
"""Plot a page-sharing pattern map for traditional benchmark traces."""

import argparse
import csv
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt
from matplotlib.lines import Line2D


FONT_SIZE = 14
FIGSIZE = (8, 3.8)

BENCHMARK_ORDER = [
    "aes",
    "matrixmultiplication",
    "bitonicsort",
    "matrixtranspose",
    "fastwalshtransform",
    "im2col",
    "pagerank",
    "spmv",
    "floydwarshall",
    "simpleconvolution",
    "fir",
    "kmeans",
    "fft",
    "relu",
]

DISPLAY_NAMES = {
    "aes": "AES",
    "bitonicsort": "Bitonic",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "Floyd",
    "im2col": "Im2Col",
    "kmeans": "KMeans",
    "matrixmultiplication": "MatMul",
    "matrixtranspose": "Transpose",
    "pagerank": "PageRank",
    "relu": "ReLU",
    "simpleconvolution": "Conv",
    "spmv": "SpMV",
}

CATEGORY = {
    "aes": "Hotspot",
    "matrixmultiplication": "Hotspot",
    "bitonicsort": "Broad sharing",
    "matrixtranspose": "Broad sharing",
    "fastwalshtransform": "Broad sharing",
    "im2col": "Mixed",
    "pagerank": "Mixed",
    "spmv": "Mixed",
    "floydwarshall": "Mixed",
    "simpleconvolution": "Mixed",
    "fir": "Placement/private",
    "kmeans": "Placement/private",
    "fft": "Placement/private",
    "relu": "Placement/private",
}

COLORS = {
    "Hotspot": "#D62728",
    "Broad sharing": "#4C78A8",
    "Mixed": "#F28E2B",
    "Placement/private": "#7F7F7F",
}

LABEL_OFFSETS = {
    "AES": (6, -22),
    "MatMul": (-78, -34),
    "Bitonic": (-60, -26),
    "Transpose": (-104, 10),
    "FWT": (-58, 16),
    "Im2Col": (-46, -18),
    "PageRank": (-42, 12),
    "SpMV": (-30, -18),
    "Floyd": (18, 18),
    "Conv": (22, -16),
    "FIR": (8, 8),
    "KMeans": (18, -14),
    "FFT": (22, 4),
    "ReLU": (20, 22),
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing traditional *_sharing_summary_metrics.csv files.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=None,
        help="Output path. Defaults to results_dir/figures/traditional_page_sharing_pattern_map.png.",
    )
    return parser.parse_args()


def benchmark_from_summary(path):
    name = path.name
    prefix = "baseline_"
    suffix = "_baseline_sharing_summary_metrics.csv"
    if name.startswith(prefix) and name.endswith(suffix):
        return name[len(prefix) : -len(suffix)]
    return name.replace("_sharing_summary_metrics.csv", "")


def read_metrics(path):
    metrics = {}
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            value = row["value"]
            metrics[row["metric"]] = float(value) if value else 0.0
    return metrics


def top_page_share(path, metrics, count=10):
    top_path = Path(str(path).replace("_sharing_summary_metrics.csv", "_sharing_top_shared_pages.csv"))
    if not top_path.exists():
        return 0.0
    total = metrics.get("TotalAccesses") or metrics.get("TotalBytes") or 0.0
    if total <= 0:
        return 0.0

    accesses = 0.0
    with open(top_path, newline="") as f:
        for index, row in enumerate(csv.DictReader(f)):
            if index >= count:
                break
            accesses += float(row.get("accesses") or row.get("bytes") or 0.0)
    return 100.0 * accesses / total


def read_rows(results_dir):
    rows = []
    for path in sorted(results_dir.glob("*_sharing_summary_metrics.csv")):
        benchmark = benchmark_from_summary(path)
        if benchmark not in DISPLAY_NAMES:
            continue
        metrics = read_metrics(path)
        rows.append(
            {
                "benchmark": benchmark,
                "name": DISPLAY_NAMES[benchmark],
                "category": CATEGORY[benchmark],
                "remote": 100.0 * metrics.get("RemoteAccessRatio", 0.0),
                "shared": 100.0 * metrics.get("SharedByteRatio", 0.0),
                "shared_page": 100.0 * metrics.get("SharedPageRatio", 0.0),
                "top10": top_page_share(path, metrics),
                "hotspot": metrics.get("RemoteAccessesPerRemotePage", 0.0),
            }
        )
    order = {name: index for index, name in enumerate(BENCHMARK_ORDER)}
    rows.sort(key=lambda row: order.get(row["benchmark"], 10_000))
    return rows


def output_paths(results_dir, output):
    if output is None:
        output = results_dir / "figures" / "traditional_page_sharing_pattern_map.png"
    elif not output.is_absolute():
        output = results_dir / output
    return output, output.with_suffix(".pdf")


def main():
    args = parse_args()
    results_dir = args.results_dir.resolve()
    rows = read_rows(results_dir)

    plt.rcParams.update(
        {
            "font.size": FONT_SIZE,
            "axes.titlesize": FONT_SIZE,
            "axes.labelsize": FONT_SIZE,
            "xtick.labelsize": FONT_SIZE,
            "ytick.labelsize": FONT_SIZE,
            "legend.fontsize": 10,
        }
    )

    fig, ax = plt.subplots(figsize=FIGSIZE)
    fig.patch.set_facecolor("white")
    ax.axhspan(-5, 15, color="#F2F2F2", zorder=0)
    ax.axhspan(85, 105, color="#E8F2FA", zorder=0)
    ax.axhline(50, color="#B8B8B8", linewidth=1.0, linestyle="--", zorder=1)

    for row in rows:
        size = 55 + 8.0 * min(row["top10"], 100.0)
        ax.scatter(
            row["remote"],
            row["shared"],
            s=size,
            color=COLORS[row["category"]],
            edgecolors="white",
            linewidths=1.1,
            alpha=0.9,
            zorder=3,
        )
        dx, dy = LABEL_OFFSETS.get(row["name"], (6, 6))
        ax.annotate(
            row["name"],
            (row["remote"], row["shared"]),
            xytext=(dx, dy),
            textcoords="offset points",
            fontsize=9,
            color="#222222",
            arrowprops={
                "arrowstyle": "-",
                "color": "#777777",
                "linewidth": 0.55,
                "shrinkA": 4,
                "shrinkB": 4,
            },
        )

    ax.text(8, 6, "remote/private", fontsize=10, color="#555555", va="center")
    ax.text(18, 94, "shared traffic", fontsize=10, color="#355C7D", va="center")
    ax.set_xlim(-2, 103)
    ax.set_ylim(-7, 112)
    ax.set_xlabel("Remote access ratio (%)")
    ax.set_ylabel("Shared access ratio (%)")
    ax.set_title("Traditional Workload Page-Sharing Pattern Map", pad=8)
    ax.grid(True, linestyle="--", linewidth=0.6, alpha=0.28)

    category_handles = [
        Line2D(
            [0],
            [0],
            marker="o",
            color="none",
            label=category,
            markerfacecolor=color,
            markeredgecolor="white",
            markersize=8,
        )
        for category, color in COLORS.items()
    ]
    hotspot_handles = [
        Line2D(
            [0],
            [0],
            marker="o",
            color="none",
            label="small top-10 page share",
            markerfacecolor="#CFCFCF",
            markeredgecolor="white",
            markersize=6,
        ),
        Line2D(
            [0],
            [0],
            marker="o",
            color="none",
            label="large top-10 page share",
            markerfacecolor="#CFCFCF",
            markeredgecolor="white",
            markersize=13,
        ),
    ]
    ax.legend(
        handles=category_handles + hotspot_handles,
        loc="upper left",
        bbox_to_anchor=(1.01, 1.02),
        frameon=False,
        borderaxespad=0,
    )

    out_png, out_pdf = output_paths(results_dir, args.output)
    out_png.parent.mkdir(parents=True, exist_ok=True)
    fig.tight_layout()
    fig.savefig(out_png, dpi=240, bbox_inches="tight")
    fig.savefig(out_pdf, bbox_inches="tight")
    print(out_png)
    print(out_pdf)


if __name__ == "__main__":
    main()
