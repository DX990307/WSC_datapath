#!/usr/bin/env python3
"""Plot the current Combined speedup in the PTCL paper-figure style.

MM and MT intentionally use the older, work-matched baseline requested for
the provisional figure. The emitted source CSV marks those pairs as
cross-binary and they must be replaced before the final paper plot.
"""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

from plot_ptcl_sweep_speedup import LABELS, plot_speedups, read_total_time


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_BASELINE = (
    ROOT / "akkalat/results/baseline-library"
)
DEFAULT_ABLATION = (
    ROOT / "akkalat/results/2026-07-14-final-current-candidate-primary-ablation"
)
DEFAULT_FALLBACK = ROOT / "akkalat/results/2026-07-14-final-kerneldrained-baseline"
DEFAULT_SPMV_ABLATION = (
    ROOT
    / "akkalat/results/2026-07-14-final-kerneldrained-pageadaptive-ablation-w4"
)
DEFAULT_OUTPUT = ROOT / "akkalat/results/2026-07-15-current-ablation-provisional-plot"

BENCHMARKS = [
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fft",
    "fir",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
]

PLOT_CONFIGS = [
    ("baseline", "Baseline", "#83CEE2", ""),
    ("baseline_all_three", "CuPath", "#F18541", ""),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline-dir", type=Path, default=DEFAULT_BASELINE)
    parser.add_argument("--ablation-dir", type=Path, default=DEFAULT_ABLATION)
    parser.add_argument("--fallback-baseline-dir", type=Path, default=DEFAULT_FALLBACK)
    parser.add_argument("--spmv-ablation-dir", type=Path, default=DEFAULT_SPMV_ABLATION)
    parser.add_argument("--output-dir", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def metrics_name(benchmark: str, config: str) -> str:
    file_benchmark = "kmeans-reuse-smoke" if benchmark == "kmeans" else benchmark
    return f"baseline_{file_benchmark}_{config}_metrics.csv"


def main() -> None:
    args = parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)

    times: dict[tuple[str, str], float] = {}
    source_rows = []
    speedup_rows = []

    for benchmark in BENCHMARKS:
        baseline_path = args.baseline_dir / metrics_name(benchmark, "baseline")
        provenance = "current_frozen_binary"
        if not baseline_path.exists() and benchmark in {
            "matrixmultiplication",
            "matrixtranspose",
            "spmv",
        }:
            baseline_path = args.fallback_baseline_dir / metrics_name(
                benchmark, "baseline"
            )
            provenance = (
                "provisional_previous_same_binary_pair"
                if benchmark == "spmv"
                else "provisional_cross_binary"
            )

        combined_dir = (
            args.spmv_ablation_dir if benchmark == "spmv" else args.ablation_dir
        )
        combined_path = combined_dir / metrics_name(benchmark, "baseline_all_three")
        if not baseline_path.exists():
            raise FileNotFoundError(f"missing baseline for {benchmark}: {baseline_path}")
        if not combined_path.exists():
            raise FileNotFoundError(f"missing Combined result for {benchmark}: {combined_path}")

        baseline_time = read_total_time(baseline_path)
        combined_time = read_total_time(combined_path)
        speedup = baseline_time / combined_time
        times[(benchmark, "baseline")] = baseline_time
        times[(benchmark, "baseline_all_three")] = combined_time
        source_rows.append(
            {
                "benchmark": LABELS.get(benchmark, benchmark),
                "baseline_metrics": str(baseline_path),
                "combined_metrics": str(combined_path),
                "provenance": provenance,
            }
        )
        speedup_rows.append(
            {
                "benchmark": LABELS.get(benchmark, benchmark),
                "baseline_time_s": f"{baseline_time:.12g}",
                "combined_time_s": f"{combined_time:.12g}",
                "speedup": f"{speedup:.9f}",
                "provenance": provenance,
            }
        )

    geomean = math.exp(
        sum(math.log(float(row["speedup"])) for row in speedup_rows)
        / len(speedup_rows)
    )
    speedup_rows.append(
        {
            "benchmark": "GMEAN",
            "baseline_time_s": "",
            "combined_time_s": "",
            "speedup": f"{geomean:.9f}",
            "provenance": "includes_provisional_MM_MT_SPMV",
        }
    )

    with (args.output_dir / "current_combined_speedup.csv").open(
        "w", newline="", encoding="utf-8"
    ) as stream:
        writer = csv.DictWriter(stream, fieldnames=speedup_rows[0].keys())
        writer.writeheader()
        writer.writerows(speedup_rows)

    with (args.output_dir / "current_combined_sources.csv").open(
        "w", newline="", encoding="utf-8"
    ) as stream:
        writer = csv.DictWriter(stream, fieldnames=source_rows[0].keys())
        writer.writeheader()
        writer.writerows(source_rows)

    plot_speedups(
        args.output_dir,
        times,
        BENCHMARKS,
        "combined_provisional",
        "current_ablation",
        args.dpi,
        PLOT_CONFIGS,
    )
    print(args.output_dir / "current_combined_speedup.csv")
    print(args.output_dir / "current_combined_sources.csv")


if __name__ == "__main__":
    main()
