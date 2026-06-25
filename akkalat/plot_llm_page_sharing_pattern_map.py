#!/usr/bin/env python3
"""Plot a compact page-sharing pattern map for LLM per-op traces."""

import argparse
import csv
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt
from matplotlib.lines import Line2D


FONT_SIZE = 14
FIGSIZE = (8, 3.6)

GROUPS = [
    ("embedding", "Embedding", "Other"),
    ("layer00_norm1|layer00_norm2", "Norms", "Elementwise"),
    (
        "layer00_attn_q|layer00_attn_k|layer00_attn_v|layer00_attn_out",
        "Q/K/V/Out",
        "Split-K GEMM",
    ),
    ("layer00_attn_score", "Score", "Split-K GEMM"),
    ("layer00_causal_mask", "Mask", "Elementwise"),
    ("layer00_attn_softmax", "Softmax", "Elementwise"),
    ("layer00_attn_value", "Value", "Split-K GEMM"),
    (
        "layer00_attn_residual|layer00_mlp_residual",
        "Residuals",
        "Elementwise",
    ),
    ("layer00_mlp_fc1", "MLP FC1", "Split-K GEMM"),
    ("layer00_mlp_gelu", "GELU", "Elementwise"),
    ("layer00_mlp_fc2", "MLP FC2", "Split-K GEMM"),
]

COLORS = {
    "Split-K GEMM": "#4C78A8",
    "Elementwise": "#7F7F7F",
    "Other": "#59A14F",
}

LABEL_OFFSETS = {
    "Embedding": (-58, 12),
    "Norms": (-14, -26),
    "Q/K/V/Out": (-88, 16),
    "Score": (-42, -34),
    "Mask": (8, 8),
    "Softmax": (8, -18),
    "Value": (16, 16),
    "Residuals": (-72, -14),
    "MLP FC1": (14, 2),
    "GELU": (-34, -18),
    "MLP FC2": (8, -28),
}

LABELLED_POINTS = {
    "Embedding",
    "Q/K/V/Out",
    "Score",
    "Value",
    "MLP FC1",
    "MLP FC2",
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing llm_sharing_analysis_summary.csv.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=None,
        help="Output path. Defaults to results_dir/figures/llm_page_sharing_pattern_map.png.",
    )
    return parser.parse_args()


def read_rows(path):
    with open(path, newline="") as f:
        rows = {}
        for row in csv.DictReader(f):
            rows[row["op_name"]] = {
                "remote": float(row["remote_access_ratio_pct"]),
                "shared": float(row["shared_access_ratio_pct"]),
                "top10": float(row["top10_shared_page_access_pct"]),
                "hotspot": float(row["remote_accesses_per_remote_page"]),
            }
        return rows


def average_group(rows, spec):
    names = spec.split("|")
    values = [rows[name] for name in names if name in rows]
    if not values:
        return None

    return {
        "remote": sum(v["remote"] for v in values) / len(values),
        "shared": sum(v["shared"] for v in values) / len(values),
        "top10": sum(v["top10"] for v in values) / len(values),
        "hotspot": sum(v["hotspot"] for v in values) / len(values),
    }


def output_paths(results_dir, output):
    if output is None:
        output = results_dir / "figures" / "llm_page_sharing_pattern_map.png"
    elif not output.is_absolute():
        output = results_dir / output
    return output, output.with_suffix(".pdf")


def main():
    args = parse_args()
    results_dir = args.results_dir.resolve()
    rows = read_rows(results_dir / "llm_sharing_analysis_summary.csv")

    points = []
    for spec, label, category in GROUPS:
        metrics = average_group(rows, spec)
        if metrics:
            points.append({"label": label, "category": category, **metrics})

    plt.rcParams.update(
        {
            "font.size": FONT_SIZE,
            "axes.titlesize": FONT_SIZE,
            "axes.labelsize": FONT_SIZE,
            "xtick.labelsize": FONT_SIZE,
            "ytick.labelsize": FONT_SIZE,
            "legend.fontsize": 11,
        }
    )

    fig, ax = plt.subplots(figsize=FIGSIZE)
    fig.patch.set_facecolor("white")

    ax.axhspan(-5, 15, color="#F2F2F2", zorder=0)
    ax.axhspan(85, 105, color="#E8F2FA", zorder=0)
    ax.axhline(50, color="#B8B8B8", linewidth=1.0, linestyle="--", zorder=1)

    for point in points:
        size = 55 + 10.5 * point["top10"]
        ax.scatter(
            point["remote"],
            point["shared"],
            s=size,
            color=COLORS[point["category"]],
            edgecolors="white",
            linewidths=1.2,
            alpha=0.88,
            zorder=3,
        )
        if point["label"] in LABELLED_POINTS:
            dx, dy = LABEL_OFFSETS.get(point["label"], (6, 6))
            ax.annotate(
                point["label"],
                (point["remote"], point["shared"]),
                xytext=(dx, dy),
                textcoords="offset points",
                fontsize=9,
                color="#222222",
                arrowprops={
                    "arrowstyle": "-",
                    "color": "#777777",
                    "linewidth": 0.6,
                    "shrinkA": 4,
                    "shrinkB": 4,
                },
            )

    ax.text(
        28,
        6,
        "remote but not shared",
        fontsize=10,
        color="#555555",
        va="center",
    )
    ax.text(
        73,
        6,
        "elementwise ops",
        fontsize=10,
        color="#666666",
        va="center",
    )
    ax.text(
        29,
        94,
        "shared accesses",
        fontsize=10,
        color="#355C7D",
        va="center",
    )

    ax.set_xlim(18, 106)
    ax.set_ylim(-8, 112)
    ax.set_xlabel("Remote access ratio (%)")
    ax.set_ylabel("Shared access ratio (%)")
    ax.set_title("LLM Page-Sharing Pattern Map", pad=8)
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
            label="small top-10 share",
            markerfacecolor="#CFCFCF",
            markeredgecolor="white",
            markersize=6,
        ),
        Line2D(
            [0],
            [0],
            marker="o",
            color="none",
            label="large top-10 share",
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
