#!/usr/bin/env python3

"""Plot M1 paired-read diagnostics from analyze_m1_paired_read.py output."""

from __future__ import annotations

import argparse
import csv
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
from matplotlib.colors import LinearSegmentedColormap


BLUE = ("#EAF7FA", "#D6EFF5", "#ADDEEB", "#83CEE2", "#5ABED8")
ORANGE = ("#FDF0E7", "#FBE0D0", "#F8C2A0", "#F4A371", "#F18541")
GRAY = "#4C4C4D"
LABELS = {
    "aes": "AES",
    "bitonicsort": "BT",
    "fastwalshtransform": "FWT",
    "fir": "FIR",
    "fft": "FFT",
    "floydwarshall": "FWS",
    "im2col": "I2C",
    "kmeans": "KM",
    "matrixmultiplication": "MM",
    "matrixtranspose": "MT",
    "pagerank": "PR",
    "relu": "RELU",
    "simpleconvolution": "SC",
    "spmv": "SPMV",
    "aes-pipeline-smoke": "AES-smoke",
}
ORDER = tuple(LABELS)


def number(row: dict[str, str] | None, field: str) -> float | None:
    if not row:
        return None
    value = row.get(field, "")
    if value in ("", None):
        return None
    return float(value)


def ratio(numerator: float | None, denominator: float | None) -> float | None:
    if numerator is None or denominator is None or denominator <= 0:
        return None
    return numerator / denominator


def percent_reduction(before: float | None, after: float | None) -> float | None:
    value = ratio(None if before is None or after is None else before - after,
                  before)
    return None if value is None else 100.0 * value


def read_summary(path: Path):
    with path.open(newline="", encoding="utf-8") as stream:
        rows = list(csv.DictReader(stream))
    return {(row["benchmark"], row["config"]): row for row in rows}


def benchmark_order(rows):
    present = {benchmark for benchmark, _ in rows}
    ordered = [benchmark for benchmark in ORDER if benchmark in present]
    ordered.extend(sorted(present - set(ordered)))
    return ordered


def sibling_composition(row):
    fills = number(row, "granularity_sibling_fills")
    timely = ratio(number(row, "granularity_timely_sibling_lines"), fills)
    late = ratio(number(row, "granularity_late_sibling_lines"), fills)
    unused = ratio(
        number(row, "granularity_terminal_unused_sibling_lines"), fills)
    return tuple(None if value is None else 100.0 * value
                 for value in (timely, late, unused))


def filter_contribution(filtered, unfiltered):
    exact = percent_reduction(
        number(unfiltered, "filter_exact_lookup_total"),
        number(filtered, "filter_exact_lookup_total"),
    )
    wasted = percent_reduction(
        number(unfiltered, "granularity_terminal_wasted_sibling_bytes"),
        number(filtered, "granularity_terminal_wasted_sibling_bytes"),
    )
    traffic = percent_reduction(
        number(unfiltered, "dram_physical_read_bytes"),
        number(filtered, "dram_physical_read_bytes"),
    )
    return exact, wasted, traffic


def dram_changes(m1, baseline):
    fields = (
        "dram_physical_read_bytes",
        "dram_row_activate_commands",
        "dram_row_precharge_commands",
    )
    changes = []
    for field in fields:
        base = number(baseline, field)
        value = number(m1, field)
        normalized = ratio(value, base)
        changes.append(None if normalized is None else 100.0 * (normalized - 1))
    reuse = number(m1, "paired_row_reuse_rate")
    changes.append(None if reuse is None else 100.0 * reuse)
    return tuple(changes)


def style_axis(ax):
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.65)
    ax.tick_params(colors=GRAY, labelsize=5.5, width=0.6, length=2)


def save(fig, path):
    fig.savefig(path, dpi=300, bbox_inches="tight", pad_inches=0.025,
                facecolor="white", transparent=False)
    plt.close(fig)


def plot_work(rows, benchmarks, output, m1_config):
    x = np.arange(len(benchmarks))
    fig, axes = plt.subplots(2, 1, figsize=(3.45, 3.0), sharex=True,
                             gridspec_kw={"hspace": 0.12})
    configs = (("paired_read_without_filter", "No Filter", BLUE[3]),
               (m1_config, "Filter-gated", ORANGE[3]))
    width = 0.34
    for index, (config, label, color) in enumerate(configs):
        values = []
        for benchmark in benchmarks:
            always = number(rows.get((benchmark, "always_pair")),
                            "granularity_frontend_paired_read_aggregates")
            issued = number(rows.get((benchmark, config)),
                            "granularity_frontend_paired_read_aggregates")
            normalized = ratio(issued, always)
            values.append(np.nan if normalized is None else 100.0 * normalized)
        axes[0].bar(x + (index - 0.5) * width, values, width=width,
                    label=label, color=color, edgecolor="#4C4C4D", linewidth=0.4)
    axes[0].axhline(100, color=GRAY, linewidth=0.65, linestyle="--")
    axes[0].set_ylabel("Sibling reads\n(% always-pair)", fontsize=6.2, color=GRAY)
    axes[0].legend(frameon=False, fontsize=5.5, ncol=2, loc="upper right")
    style_axis(axes[0])

    bottoms = np.zeros(len(benchmarks))
    for part, color, label in ((0, BLUE[3], "Timely"),
                               (1, ORANGE[2], "Late"),
                               (2, ORANGE[4], "Unused")):
        values = []
        for benchmark in benchmarks:
            composition = sibling_composition(rows.get((benchmark, m1_config)))
            value = composition[part]
            values.append(np.nan if value is None else value)
        values = np.asarray(values)
        axes[1].bar(x, values, bottom=bottoms, color=color, label=label,
                    edgecolor="#4C4C4D", linewidth=0.35)
        bottoms += np.nan_to_num(values)
    axes[1].set_ylim(0, 100)
    axes[1].set_ylabel("Issued sibling\ncomposition (%)", fontsize=6.2,
                       color=GRAY)
    axes[1].set_xticks(x, [LABELS.get(b, b) for b in benchmarks], rotation=30,
                       ha="right", fontsize=5.4)
    axes[1].legend(frameon=False, fontsize=5.3, ncol=3, loc="upper right")
    style_axis(axes[1])
    fig.subplots_adjust(left=0.17, right=0.99, bottom=0.18, top=0.98,
                        hspace=0.12)
    save(fig, output)


def plot_filter(rows, benchmarks, output, m1_config):
    x = np.arange(len(benchmarks))
    width = 0.25
    series = ((0, "Exact lookups", BLUE[3]),
              (1, "Wasted bytes", ORANGE[2]),
              (2, "Physical read bytes", ORANGE[4]))
    fig, ax = plt.subplots(figsize=(3.45, 2.05))
    for index, label, color in series:
        values = []
        for benchmark in benchmarks:
            contribution = filter_contribution(
                rows.get((benchmark, m1_config)),
                rows.get((benchmark, "paired_read_without_filter")),
            )[index]
            values.append(np.nan if contribution is None else contribution)
        ax.bar(x + (index - 1) * width, values, width=width, label=label,
               color=color, edgecolor="#4C4C4D", linewidth=0.4)
    ax.axhline(0, color=GRAY, linewidth=0.65)
    ax.set_ylabel("Reduction vs. no Filter (%)", fontsize=6.3, color=GRAY)
    ax.set_xticks(x, [LABELS.get(b, b) for b in benchmarks], rotation=30,
                  ha="right", fontsize=5.4)
    ax.legend(frameon=False, fontsize=5.2, ncol=3, loc="upper right")
    style_axis(ax)
    fig.tight_layout(pad=0.25)
    save(fig, output)


def plot_dram(rows, benchmarks, output, m1_config):
    data = np.array([
        [np.nan if value is None else value for value in dram_changes(
            rows.get((benchmark, m1_config)),
            rows.get((benchmark, "baseline")),
        )]
        for benchmark in benchmarks
    ])
    cmap = LinearSegmentedColormap.from_list(
        "m1_blue_orange", (BLUE[4], "#FFFFFF", ORANGE[4]))
    fig, ax = plt.subplots(figsize=(3.45, max(1.5, 0.25 * len(benchmarks) + 0.65)))
    masked = np.ma.masked_invalid(data)
    ax.imshow(masked, cmap=cmap, vmin=-25, vmax=25, aspect="auto")
    ax.set_facecolor("white")
    ax.set_xticks(range(4), ("Read bytes\nchange", "ACT\nchange",
                             "PRE\nchange", "Pair row\nreuse"), fontsize=5.3)
    ax.set_yticks(range(len(benchmarks)),
                  [LABELS.get(b, b) for b in benchmarks], fontsize=5.3)
    ax.tick_params(length=0)
    for i, values in enumerate(data):
        for j, value in enumerate(values):
            text = "--" if not math.isfinite(value) else f"{value:+.0f}" if j < 3 else f"{value:.0f}"
            ax.text(j, i, text, ha="center", va="center", fontsize=4.8,
                    color=GRAY)
    style_axis(ax)
    fig.tight_layout(pad=0.2)
    save(fig, output)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--summary", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--m1-config", default="new_m1")
    parser.add_argument(
        "--benchmarks", default="",
        help="optional comma-separated display order; missing cells stay blank",
    )
    args = parser.parse_args()
    rows = read_summary(args.summary)
    benchmarks = ([item.strip() for item in args.benchmarks.split(",")
                   if item.strip()] if args.benchmarks else benchmark_order(rows))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    outputs = (
        args.output_dir / "m1_work_reduction.png",
        args.output_dir / "m1_filter_contribution.png",
        args.output_dir / "m1_dram_work.png",
    )
    plot_work(rows, benchmarks, outputs[0], args.m1_config)
    plot_filter(rows, benchmarks, outputs[1], args.m1_config)
    plot_dram(rows, benchmarks, outputs[2], args.m1_config)
    for output in outputs:
        print(output)


if __name__ == "__main__":
    main()
