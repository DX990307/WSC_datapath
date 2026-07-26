#!/usr/bin/env python3
"""Plot the conversion from O2 adjacent correlation to Local Pairing outcome."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np


BENCHMARKS = [
    ("aes", "AES"),
    ("bitonicsort", "BT"),
    ("fastwalshtransform", "FWT"),
    ("fft", "FFT"),
    ("fir", "FIR"),
    ("floydwarshall", "FWS"),
    ("im2col", "I2C"),
    ("kmeans", "KM"),
    ("matrixmultiplication", "MM"),
    ("matrixtranspose", "MT"),
    ("pagerank", "PR"),
    ("relu", "RELU"),
    ("simpleconvolution", "SC"),
    ("spmv", "SPMV"),
]

BLUE = "#83CEE2"
BLUE_DARK = "#1D687C"
ORANGE = "#F4A371"
GRAY = "#656667"


def read_o2(path: Path, window: int) -> dict[str, float]:
    result: dict[str, float] = {}
    with path.open(newline="") as stream:
        for row in csv.DictReader(stream):
            if int(row["window_l1_cycles"]) == window:
                result[row["benchmark"]] = (
                    100.0 * float(row["cdf_fraction_all_requests"])
                )
    return result


def sum_metric(path: Path, metric: str) -> float:
    total = 0.0
    with path.open(newline="") as stream:
        reader = csv.reader(stream)
        next(reader, None)
        for row in reader:
            if len(row) >= 4 and row[2].strip() == metric:
                total += float(row[3])
    return total


def read_speedups(path: Path) -> dict[str, float]:
    with path.open(newline="") as stream:
        return {
            row["benchmark"]: float(row["m1_speedup"])
            for row in csv.DictReader(stream)
            if row["benchmark"] != "geomean_14"
        }


def close_axes(ax: plt.Axes) -> None:
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#98999A")
        spine.set_linewidth(0.55)
    ax.tick_params(width=0.5, length=2.0, color="#777777")
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.45, zorder=0)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline-dir", type=Path, required=True)
    parser.add_argument("--m1-dir", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--window", type=int, default=16)
    args = parser.parse_args()

    o2 = read_o2(
        args.baseline_dir / "observation-analysis"
        / "o2_adjacent_line_short_window_cdf.csv",
        args.window,
    )
    speedup = read_speedups(args.m1_dir / "cupath_latest_ablation.csv")

    correlation: list[float] = []
    request_reduction: list[float] = []
    speedups: list[float] = []
    source_rows: list[list[object]] = []
    for benchmark, _label in BENCHMARKS:
        baseline_metric = (
            args.baseline_dir / f"baseline_{benchmark}_baseline_metrics.csv"
        )
        m1_metric = args.m1_dir / f"baseline_{benchmark}_m1_metrics.csv"
        baseline_requests = sum_metric(
            baseline_metric, "dram_frontend_read_requests"
        )
        m1_requests = sum_metric(m1_metric, "dram_frontend_read_requests")
        reduction = (
            100.0 * (baseline_requests - m1_requests) / baseline_requests
            if baseline_requests
            else 0.0
        )
        corr = o2.get(benchmark, 0.0)
        spd = speedup[benchmark]
        correlation.append(corr)
        request_reduction.append(reduction)
        speedups.append(spd)
        source_rows.append([
            benchmark,
            corr,
            baseline_requests,
            m1_requests,
            reduction,
            spd,
        ])

    args.output_dir.mkdir(parents=True, exist_ok=True)
    with (args.output_dir / "o2_opportunity_to_outcome.csv").open(
        "w", newline=""
    ) as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "benchmark",
            f"adjacent_access_within_{args.window}ns_percent",
            "baseline_hbm_read_requests",
            "local_pairing_hbm_read_requests",
            "hbm_read_request_reduction_percent",
            "local_pairing_speedup",
        ])
        writer.writerows(source_rows)

    labels = [label for _benchmark, label in BENCHMARKS]
    x = np.arange(len(labels))
    plt.rcParams.update({
        "font.family": "DejaVu Sans",
        "font.size": 6.2,
        "pdf.fonttype": 42,
        "ps.fonttype": 42,
    })
    fig, (top, bottom) = plt.subplots(
        2, 1, figsize=(3.45, 2.55), sharex=True,
        gridspec_kw={"height_ratios": [1.0, 1.08], "hspace": 0.12},
    )

    top.bar(
        x, correlation, width=0.68, color=BLUE, edgecolor=BLUE_DARK,
        linewidth=0.35, zorder=2, label=f"Adjacent access $\\leq${args.window} ns",
    )
    top.set_ylim(0, 55)
    top.set_ylabel("Local L2 read\nmisses (%)", fontsize=6.2)
    top.text(
        0.015, 0.91, "(a) Correlation", transform=top.transAxes,
        fontsize=6.1, fontweight="bold", color=GRAY, va="top",
    )
    top.legend(
        loc="upper right", bbox_to_anchor=(1.0, 1.01), frameon=False,
        fontsize=5.5, handlelength=1.2, handletextpad=0.4,
    )
    close_axes(top)

    reduction_bars = bottom.bar(
        x, request_reduction, width=0.68, color=ORANGE,
        edgecolor="#BE520E", linewidth=0.35, zorder=2,
        label="HBM read requests reduced",
    )
    bottom.set_ylim(-3, 50)
    bottom.set_ylabel("HBM requests\nreduced (%)", fontsize=6.2)
    bottom.axhline(0, color="#777777", linewidth=0.5, zorder=1)
    bottom.text(
        0.015, 0.91, "(b) Outcome", transform=bottom.transAxes,
        fontsize=6.1, fontweight="bold", color=GRAY, va="top",
    )
    close_axes(bottom)

    speed_ax = bottom.twinx()
    speed_line = speed_ax.plot(
        x, speedups, color=BLUE_DARK, marker="o", markersize=2.7,
        linewidth=0.8, zorder=3, label="Local Pairing speedup",
    )[0]
    speed_ax.axhline(1.0, color=BLUE_DARK, linewidth=0.5,
                     linestyle=(0, (2, 2)), alpha=0.65)
    speed_ax.set_ylim(0.90, 1.75)
    speed_ax.set_ylabel("Speedup", fontsize=6.2, color=BLUE_DARK)
    speed_ax.tick_params(
        axis="y", labelsize=5.5, labelcolor=BLUE_DARK,
        width=0.5, length=2.0,
    )
    speed_ax.spines["right"].set_color(BLUE_DARK)
    speed_ax.spines["right"].set_linewidth(0.55)
    speed_ax.spines["top"].set_visible(False)
    speed_ax.spines["left"].set_visible(False)
    speed_ax.spines["bottom"].set_visible(False)

    bottom.set_xticks(x, labels, rotation=35, ha="right", fontsize=5.5)
    bottom.legend(
        [reduction_bars, speed_line],
        ["HBM requests reduced", "Speedup"],
        loc="upper right", bbox_to_anchor=(1.0, 1.02), ncol=2,
        frameon=False, fontsize=5.25, columnspacing=0.8,
        handlelength=1.1, handletextpad=0.35,
    )

    fig.subplots_adjust(left=0.14, right=0.87, top=0.98, bottom=0.20)
    for suffix in ("png", "pdf"):
        fig.savefig(
            args.output_dir / f"o2_opportunity_to_outcome.{suffix}",
            dpi=400, bbox_inches="tight", pad_inches=0.02,
        )
    plt.close(fig)

    correlation_fig, correlation_ax = plt.subplots(figsize=(3.45, 1.52))
    correlation_ax.bar(
        x, correlation, width=0.68, color=BLUE, edgecolor=BLUE_DARK,
        linewidth=0.35, zorder=2,
        label=f"Adjacent access $\\leq${args.window} ns",
    )
    correlation_ax.set_ylim(0, 55)
    correlation_ax.set_xlim(-0.45, len(labels) - 0.55)
    correlation_ax.set_ylabel("Local L2 read misses (%)", fontsize=6.2)
    correlation_ax.set_xticks(
        x, labels, rotation=35, ha="right", fontsize=5.5,
    )
    correlation_ax.legend(
        loc="lower center",
        bbox_to_anchor=(0.5, 1.015),
        ncol=1,
        frameon=False, fontsize=5.5,
        handlelength=1.25, handletextpad=0.4, borderaxespad=0,
    )
    close_axes(correlation_ax)
    correlation_fig.subplots_adjust(
        left=0.12, right=0.995, top=0.84, bottom=0.29,
    )
    for suffix in ("png", "pdf"):
        correlation_fig.savefig(
            args.output_dir / f"o2_adjacent_correlation.{suffix}",
            dpi=400, bbox_inches="tight", pad_inches=0.02,
        )
    plt.close(correlation_fig)

    outcome_fig, outcome_ax = plt.subplots(figsize=(3.45, 1.62))
    outcome_bars = outcome_ax.bar(
        x, request_reduction, width=0.68, color=ORANGE,
        edgecolor="#BE520E", linewidth=0.35, zorder=2,
        label="HBM requests reduced",
    )
    outcome_ax.set_ylim(-3, 50)
    outcome_ax.set_xlim(-0.45, len(labels) - 0.55)
    outcome_ax.set_ylabel("HBM requests reduced (%)", fontsize=6.2)
    outcome_ax.axhline(0, color="#777777", linewidth=0.5, zorder=1)
    outcome_ax.set_xticks(
        x, labels, rotation=35, ha="right", fontsize=5.5,
    )
    close_axes(outcome_ax)

    outcome_speed_ax = outcome_ax.twinx()
    outcome_speed_line = outcome_speed_ax.plot(
        x, speedups, color=BLUE_DARK, marker="o", markersize=2.7,
        linewidth=0.8, zorder=3, label="Speedup",
    )[0]
    outcome_speed_ax.axhline(
        1.0, color=BLUE_DARK, linewidth=0.5,
        linestyle=(0, (2, 2)), alpha=0.65,
    )
    outcome_speed_ax.set_ylim(0.90, 1.75)
    outcome_speed_ax.set_ylabel("Speedup", fontsize=6.2, color=BLUE_DARK)
    outcome_speed_ax.tick_params(
        axis="y", labelsize=5.5, labelcolor=BLUE_DARK,
        width=0.5, length=2.0,
    )
    outcome_speed_ax.spines["right"].set_color(BLUE_DARK)
    outcome_speed_ax.spines["right"].set_linewidth(0.55)
    outcome_speed_ax.spines["top"].set_visible(False)
    outcome_speed_ax.spines["left"].set_visible(False)
    outcome_speed_ax.spines["bottom"].set_visible(False)
    outcome_ax.legend(
        [outcome_bars, outcome_speed_line],
        ["HBM requests reduced", "Speedup"],
        loc="lower center",
        bbox_to_anchor=(0.5, 1.015),
        ncol=2,
        frameon=False, fontsize=5.25,
        columnspacing=1.2, handlelength=1.25, handletextpad=0.4,
        borderaxespad=0,
    )
    outcome_fig.subplots_adjust(
        left=0.12, right=0.90, top=0.84, bottom=0.29,
    )
    for suffix in ("png", "pdf"):
        outcome_fig.savefig(
            args.output_dir / f"o2_realized_outcome.{suffix}",
            dpi=400, bbox_inches="tight", pad_inches=0.02,
        )
    plt.close(outcome_fig)


if __name__ == "__main__":
    main()
