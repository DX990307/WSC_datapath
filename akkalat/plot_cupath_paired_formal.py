#!/usr/bin/env python3
"""Plot the strict 14-workload, five-configuration CuPath ablation.

The input is produced by ``analyze_cupath_paired_formal.py``.  This plotter
does not accept a partial grid: blank or selectively omitted workloads must
not enter the paper's overall-performance figure.
"""

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


WORKLOADS = (
    ("aes", "AES"),
    ("bitonicsort", "BT"),
    ("fastwalshtransform", "FWT"),
    ("fft", "FFT"),
    ("fir", "FIR"),
    ("relu", "RELU"),
    ("simpleconvolution", "SC"),
    ("floydwarshall", "FWS"),
    ("kmeans", "KM"),
    ("matrixmultiplication", "MM"),
    ("pagerank", "PR"),
    ("im2col", "I2C"),
    ("matrixtranspose", "MT"),
    ("spmv", "SPMV"),
)
CONFIGS = (
    ("baseline", "Baseline", "#EAF7FA"),
    ("m2", "M2", "#5ABED8"),
    ("m3", "M3", "#F8C2A0"),
    ("m1", "M1", "#ADDEEB"),
    ("complete", "Complete", "#F18541"),
)
GROUPS = (
    ("All Local", 0, 7),
    ("Mixed", 7, 11),
    ("Remote", 11, 14),
)
EDGE = "#656667"
TEXT = "#4C4C4D"


def read_strict_grid(path: Path) -> dict[tuple[str, str], float]:
    with path.open(newline="", encoding="utf-8") as stream:
        rows = list(csv.DictReader(stream))
    expected = {
        (benchmark, config)
        for benchmark, _ in WORKLOADS
        for config, _, _ in CONFIGS
    }
    observed: dict[tuple[str, str], float] = {}
    for row in rows:
        key = (row.get("benchmark", ""), row.get("config", ""))
        if key not in expected:
            raise ValueError(f"unexpected formal cell {key}")
        if key in observed:
            raise ValueError(f"duplicate formal cell {key}")
        try:
            value = float(row["speedup_vs_baseline"])
        except (KeyError, TypeError, ValueError) as error:
            raise ValueError(f"invalid speedup for {key}") from error
        if not math.isfinite(value) or value <= 0:
            raise ValueError(f"non-positive speedup for {key}: {value}")
        observed[key] = value
    missing = expected - set(observed)
    if missing:
        raise ValueError(
            f"formal plot requires the exact 14x5 grid; missing {len(missing)}"
        )
    return observed


def geometric_mean(values: list[float]) -> float:
    if not values or any(value <= 0 for value in values):
        raise ValueError("geometric mean requires positive values")
    return math.exp(sum(math.log(value) for value in values) / len(values))


def series_with_overall_geomean(
    grid: dict[tuple[str, str], float], config: str
) -> list[float]:
    values = [grid[(benchmark, config)] for benchmark, _ in WORKLOADS]
    return values + [geometric_mean(values)]


def plot(grid: dict[tuple[str, str], float], output: Path) -> None:
    labels = [label for _, label in WORKLOADS] + ["GM"]
    x = np.arange(len(labels), dtype=float)
    width = 0.15
    fig, ax = plt.subplots(figsize=(3.45, 2.05))

    maximum = 1.0
    for index, (config, label, color) in enumerate(CONFIGS):
        values = series_with_overall_geomean(grid, config)
        maximum = max(maximum, max(values))
        ax.bar(
            x + (index - 2) * width,
            values,
            width=width,
            label=label,
            color=color,
            edgecolor=EDGE,
            linewidth=0.35,
            zorder=3,
        )

    ax.axhline(1.0, color=TEXT, linewidth=0.6, linestyle="--", zorder=2)
    for boundary in (6.5, 10.5, 13.5):
        ax.axvline(boundary, color="#CBCCCD", linewidth=0.55, zorder=1)

    ylim = max(1.12, maximum * 1.13)
    ax.set_ylim(0, ylim)
    for name, start, end in GROUPS:
        ax.text(
            (start + end - 1) / 2,
            ylim * 0.985,
            name,
            ha="center",
            va="top",
            fontsize=5.0,
            color=TEXT,
        )
    ax.text(14, ylim * 0.985, "Overall", ha="center", va="top",
            fontsize=5.0, color=TEXT)

    ax.set_ylabel("Speedup over Baseline", fontsize=6.2, color=TEXT)
    ax.set_xticks(x, labels, rotation=35, ha="right", fontsize=5.1)
    ax.tick_params(axis="y", labelsize=5.3, colors=TEXT,
                   width=0.55, length=2)
    ax.tick_params(axis="x", colors=TEXT, width=0.55, length=2)
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.45, zorder=0)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color(EDGE)
        spine.set_linewidth(0.65)
    ax.legend(
        frameon=False,
        fontsize=5.0,
        ncol=5,
        loc="upper center",
        bbox_to_anchor=(0.5, 1.16),
        handlelength=1.0,
        columnspacing=0.7,
        handletextpad=0.3,
    )
    fig.subplots_adjust(left=0.14, right=0.995, bottom=0.23, top=0.84)
    output.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(
        output,
        dpi=300,
        bbox_inches="tight",
        pad_inches=0.025,
        facecolor="white",
        transparent=False,
    )
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("summary", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    output = args.output or args.summary.with_name(
        "cupath_paired_overall_speedup.png"
    )
    plot(read_strict_grid(args.summary), output)
    print(output)


if __name__ == "__main__":
    main()
