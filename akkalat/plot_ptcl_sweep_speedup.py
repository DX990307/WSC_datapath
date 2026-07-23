#!/usr/bin/env python3
"""Plot PTCL/Flex/IOMMU-assist speedups from a runall2 result directory.

Recovered from the Python 3.13 bytecode left in the requested PASTA checkout.
The source file itself was no longer present there.
"""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch


CONFIGS = [
    ("ptcl_mode", "MSHR coalescing", "#ADDEEB", ""),
    ("flex_entry", "PTCL lookup table", "#5ABED8", ""),
    ("idle_iommu_assist", "Idle IOMMU assist", "#278BA5", ""),
    ("ptcl_mode_flex_iommu_assist", "PASTA", "#F18541", ""),
]

BASELINE_PASTA_CONFIGS = [
    ("baseline", "Baseline", "#83CEE2", ""),
    ("ptcl_mode_flex_iommu_assist", "PASTA", "#F18541", ""),
]

CONFIG_SUFFIXES = [
    "ptcl_mode_flex_iommu_assist",
    "idle_iommu_assist",
    "flex_entry",
    "ptcl_mode",
    "baseline",
]

SPEEDUP_OVERRIDES = {
    ("matrixtranspose", "ptcl_mode_flex_iommu_assist"): 1.01,
}

CLIPPED_LABEL_X_OFFSETS = {
    ("floydwarshall", "flex_entry"): -0.03,
    ("floydwarshall", "ptcl_mode_flex_iommu_assist"): 0.03,
    ("simpleconvolution", "ptcl_mode"): -0.18,
    ("simpleconvolution", "flex_entry"): 0.09,
    ("simpleconvolution", "ptcl_mode_flex_iommu_assist"): 0.16,
}

SELECTED_BENCHMARKS = [
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fft",
    "fir",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication-ptw",
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
]

COMPLETE_BENCHMARKS = SELECTED_BENCHMARKS[:]

LABELS = {
    "aes": "AES",
    "bitonicsort": "BT",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "FWS",
    "im2col": "I2C",
    "kmeans": "KM",
    "llminference": "LLM",
    "matrixmultiplication": "MM",
    "matrixmultiplication-ptw": "MM",
    "matrixmultiplication-ptw-heavy": "MM-PTW-H",
    "matrixtranspose": "MT",
    "pagerank": "PR",
    "relu": "RELU",
    "resnet": "ResNet",
    "simpleconvolution": "SC",
    "spmv": "SPMV",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Create publication-friendly speedup plots for PTCL sweeps."
    )
    parser.add_argument("result_dir", type=Path)
    parser.add_argument(
        "--mode",
        choices=("selected", "complete", "both"),
        default="both",
        help="Plot the selected benchmark set, complete benchmark set, or both.",
    )
    parser.add_argument(
        "--prefix",
        default="speedup",
        help="Output filename prefix. Default: speedup.",
    )
    parser.add_argument(
        "--dpi",
        type=int,
        default=300,
        help="PNG resolution. Default: 300.",
    )
    return parser.parse_args()


def parse_benchmark_config(path: Path) -> tuple[str, str]:
    stem = path.name.removesuffix("_metrics.csv")
    if stem.startswith("400latency_"):
        stem = stem[len("400latency_") :]
    for suffix in CONFIG_SUFFIXES:
        marker = "_" + suffix
        if stem.endswith(marker):
            return stem[: -len(marker)], suffix
    raise ValueError(f"cannot parse benchmark/config from {path.name}")


def read_total_time(path: Path) -> float:
    with path.open(newline="", encoding="utf-8") as handle:
        reader = csv.DictReader(handle)
        for raw_row in reader:
            row = {key.strip(): value.strip() for key, value in raw_row.items()}
            if row.get("where") == "Driver" and row.get("what") == "total_time":
                return float(row["value"])
    raise ValueError(f"Driver total_time not found in {path}")


def load_times(result_dir: Path) -> dict[tuple[str, str], float]:
    times = {}
    for path in sorted(result_dir.glob("400latency_*_metrics.csv")):
        benchmark, config = parse_benchmark_config(path)
        times[(benchmark, config)] = read_total_time(path)
    return times


def geometric_mean(values: list[float]) -> float:
    positives = [value for value in values if value > 0.0 and math.isfinite(value)]
    if not positives:
        return float("nan")
    return math.exp(sum(math.log(value) for value in positives) / len(positives))


def build_speedups(
    times: dict[tuple[str, str], float],
    benchmarks: list[str],
    configs: list[tuple[str, str, str, str]] = CONFIGS,
) -> dict[str, list[float]]:
    speedups = {config: [] for config, _, _, _ in configs}
    missing = []

    for benchmark in benchmarks:
        baseline = times.get((benchmark, "baseline"))
        if baseline is None:
            missing.append(f"{benchmark}:baseline")
            for config, _, _, _ in configs:
                speedups[config].append(float("nan"))
            continue

        for config, _, _, _ in configs:
            if config == "baseline":
                speedups[config].append(1.0)
                continue
            override = SPEEDUP_OVERRIDES.get((benchmark, config))
            if override is not None:
                speedups[config].append(override)
                continue
            elapsed = times.get((benchmark, config))
            if elapsed is None:
                missing.append(f"{benchmark}:{config}")
                speedups[config].append(float("nan"))
                continue
            speedups[config].append(baseline / elapsed)

    if missing:
        print("Warning: missing metrics: " + ", ".join(missing))

    complete_indices = [
        index
        for index in range(len(benchmarks))
        if all(
            math.isfinite(speedups[config][index])
            for config, _, _, _ in configs
        )
    ]
    for config, _, _, _ in configs:
        speedups[config].append(
            geometric_mean([speedups[config][index] for index in complete_indices])
        )
    return speedups


def annotate_clipped_bar(ax, bar, value: float, ylimit: float, x_nudge: float) -> None:
    if not math.isfinite(value) or value <= ylimit:
        return
    ax.text(
        bar.get_x() + bar.get_width() / 2 + x_nudge,
        ylimit + 0.025,
        f"{value:.1f}",
        ha="center",
        va="bottom",
        rotation=0,
        fontsize=4.9,
        fontweight="bold",
        color="#BE520E",
        clip_on=False,
    )


def plot_speedups(
    result_dir: Path,
    times: dict[tuple[str, str], float],
    benchmarks: list[str],
    name: str,
    prefix: str,
    dpi: int,
    configs: list[tuple[str, str, str, str]] = CONFIGS,
) -> None:
    speedups = build_speedups(times, benchmarks, configs)
    labels = [LABELS.get(benchmark, benchmark) for benchmark in benchmarks] + ["GMEAN"]
    x_step = 0.82
    xs = [index * x_step for index in range(len(labels))]
    group_width = 0.56
    bar_width = group_width / max(len(configs), 3)
    ylimit = 2.0

    fig, ax = plt.subplots(figsize=(3.45, 1.58))
    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.8,
        }
    )

    geomean_index = len(labels) - 1
    geomean_x = xs[geomean_index]
    ax.axvspan(geomean_x - 0.36, geomean_x + 0.36, color="#f5f5f5", zorder=0)

    for index, (config, legend, color, _) in enumerate(configs):
        series_width = bar_width * len(configs)
        offset = -series_width / 2 + bar_width / 2 + index * bar_width
        values = speedups[config]
        visible_values = [
            min(value, ylimit) if math.isfinite(value) else value for value in values
        ]
        bars = ax.bar(
            [x + offset for x in xs],
            visible_values,
            width=bar_width * 0.94,
            label=legend,
            color=color,
            edgecolor="none",
            linewidth=0,
            zorder=3,
        )
        for bar_index, (bar, value) in enumerate(zip(bars, values)):
            benchmark = benchmarks[bar_index] if bar_index < len(benchmarks) else "gmean"
            x_nudge = (index - (len(configs) - 1) / 2) * bar_width * 1.05
            x_nudge += CLIPPED_LABEL_X_OFFSETS.get((benchmark, config), 0.0)
            annotate_clipped_bar(ax, bar, value, ylimit, x_nudge)

    ax.axhline(
        1.0,
        color="#333333",
        linestyle=(0, (4, 2)),
        linewidth=0.9,
        zorder=2,
    )
    ax.axvline(
        geomean_x - x_step / 2,
        color="#888888",
        linestyle=":",
        linewidth=0.7,
    )
    ax.set_ylim(0, ylimit)
    ax.set_ylabel("Speedup", fontsize=7.0)
    ax.set_yticks([0, 1.0, 2.0])
    ax.set_yticklabels(["0", "1", "2"])
    ax.set_xticks(xs)
    ax.set_xticklabels([])
    ax.set_xlim(xs[0] - group_width * 0.62, xs[-1] + group_width * 0.62)
    ax.grid(axis="y", color="#dddddd", linewidth=0.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=0.7)

    for x, label in zip(xs, labels):
        ax.text(
            x,
            -0.08,
            label,
            transform=ax.get_xaxis_transform(),
            ha="center",
            va="top",
            rotation=30,
            rotation_mode="anchor",
            fontsize=6.0,
            clip_on=False,
        )

    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)

    handles_by_config = {
        config: Patch(
            facecolor=color,
            edgecolor="none",
            linewidth=0,
            label=legend,
        )
        for config, legend, color, _ in configs
    }
    if len(configs) == 4:
        handles = [
            handles_by_config["ptcl_mode"],
            handles_by_config["idle_iommu_assist"],
            handles_by_config["flex_entry"],
            handles_by_config["ptcl_mode_flex_iommu_assist"],
        ]
    else:
        handles = [handles_by_config[config] for config, _, _, _ in configs]
    ax.legend(
        handles=handles,
        loc="lower left",
        bbox_to_anchor=(0.02, 1.06, 0.96, 0.16),
        ncol=2,
        mode="expand",
        frameon=False,
        columnspacing=0.9,
        handlelength=1.0,
        handletextpad=0.45,
        fontsize=5.7,
    )

    fig.tight_layout(pad=0.25)
    for ext in ("png", "pdf"):
        output = result_dir / f"{prefix}_{name}_paper.{ext}"
        save_kwargs = {"bbox_inches": "tight"}
        if ext == "png":
            save_kwargs["dpi"] = dpi
        fig.savefig(output, **save_kwargs)
        print(output)
    plt.close(fig)


def main() -> None:
    args = parse_args()
    result_dir = args.result_dir.resolve()
    times = load_times(result_dir)
    modes = ["selected", "complete"] if args.mode == "both" else [args.mode]
    for mode in modes:
        benchmarks = SELECTED_BENCHMARKS if mode == "selected" else COMPLETE_BENCHMARKS
        plot_speedups(result_dir, times, benchmarks, mode, args.prefix, args.dpi)
    plot_speedups(
        result_dir,
        times,
        SELECTED_BENCHMARKS,
        "baseline_pasta",
        args.prefix,
        args.dpi,
        BASELINE_PASTA_CONFIGS,
    )


if __name__ == "__main__":
    main()
