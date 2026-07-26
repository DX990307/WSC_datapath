#!/usr/bin/env python3
"""Plot CuPath speedup across the evaluated large-model workloads."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


COLORS = {"Baseline": "#83CEE2", "CuPath": "#F18541"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("result_dir", type=Path)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def load_totals(path: Path) -> dict[tuple[str, str], float]:
    totals = {}
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream):
            model = row["model"]
            config = row["config"]
            if model in {"bert", "gpt", "resnet"} and config in {
                "baseline", "complete"
            }:
                totals[(model, config)] = float(
                    row["driver_total_time_weighted_sum"]
                )
    expected = {
        (model, config)
        for model in ("bert", "gpt", "resnet")
        for config in ("baseline", "complete")
    }
    missing = sorted(expected - totals.keys())
    if missing:
        raise ValueError(f"missing summary rows: {missing}")
    return totals


def calculate_speedups(totals: dict[tuple[str, str], float]) -> list[float]:
    return [
        totals[(model, "baseline")] / totals[(model, "complete")]
        for model in ("bert", "gpt", "resnet")
    ]


def plot(result_dir: Path, dpi: int) -> tuple[Path, Path]:
    totals = load_totals(result_dir / "cupath_llm_summary.csv")
    cupath = calculate_speedups(totals)
    labels = ["BERT", "GPT", "ResNet"]
    baseline = [1.0] * len(labels)
    xs = list(range(len(labels)))
    width = 0.28

    plt.rcParams.update({
        "font.family": "DejaVu Sans",
        "pdf.fonttype": 42,
        "ps.fonttype": 42,
        "axes.linewidth": 0.8,
    })
    fig, ax = plt.subplots(figsize=(3.45, 1.72))

    baseline_bars = ax.bar(
        [x - width / 2 for x in xs], baseline, width,
        color=COLORS["Baseline"], edgecolor="none", label="Baseline", zorder=3,
    )
    cupath_bars = ax.bar(
        [x + width / 2 for x in xs], cupath, width,
        color=COLORS["CuPath"], edgecolor="none", label="CuPath", zorder=3,
    )

    for bar, value in zip(baseline_bars, baseline):
        ax.text(
            bar.get_x() + bar.get_width() / 2, value + 0.12, f"{value:.1f}",
            ha="center", va="bottom", fontsize=6.1,
        )
    for bar, value in zip(cupath_bars, cupath):
        ax.text(
            bar.get_x() + bar.get_width() / 2, value + 0.12, f"{value:.2f}",
            ha="center", va="bottom", fontsize=6.1, fontweight="bold",
            color="#8E3D0B",
        )

    ax.axhline(1.0, color="#333333", linestyle=(0, (4, 2)), linewidth=0.8)
    ax.set_ylabel("Speedup", fontsize=7.2)
    ax.set_xticks(xs, labels)
    ax.set_ylim(0, 7.3)
    ax.set_yticks(range(0, 8))
    ax.set_xlim(-0.48, 2.48)
    ax.grid(axis="y", color="#dddddd", linewidth=0.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6.4, length=2.4, width=0.7)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)
    ax.legend(
        loc="lower left", bbox_to_anchor=(0.02, 1.02, 0.96, 0.15),
        ncol=2, mode="expand", frameon=False, fontsize=6.2,
        handlelength=1.1, handletextpad=0.45,
    )

    fig.tight_layout(pad=0.3)
    pdf = result_dir / "cupath_large_model_speedup.pdf"
    png = result_dir / "cupath_large_model_speedup.png"
    fig.savefig(pdf, bbox_inches="tight")
    fig.savefig(png, dpi=dpi, bbox_inches="tight")
    plt.close(fig)
    return pdf, png


def main() -> None:
    args = parse_args()
    pdf, png = plot(args.result_dir.resolve(), args.dpi)
    print(pdf)
    print(png)


if __name__ == "__main__":
    main()
