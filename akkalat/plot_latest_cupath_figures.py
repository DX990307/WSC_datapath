#!/usr/bin/env python3
"""Render the latest CuPath overall and ablation paper figures.

The script intentionally accepts separate result roots because the current
Local Pairing and Complete results were collected after the standalone Remote
Aggregation and Remote Reuse campaign.  Every plotted cell is checked against
the baseline completed-WG count before a speedup is calculated.
"""

from __future__ import annotations

import argparse
import csv
import json
import math
from pathlib import Path

import plot_cupath_ablation_pasta_style as ablation_plot
import plot_cupath_overall_speedup as overall_plot


WORKLOADS = ablation_plot.WORKLOADS
CONFIG_SOURCES = {
    "baseline": "baseline",
    "m1": "current",
    "m2": "standalone",
    "m3": "standalone",
    "complete": "current",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline-results", type=Path, required=True)
    parser.add_argument("--current-results", type=Path, required=True)
    parser.add_argument("--standalone-results", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def read_metrics(path: Path) -> dict[str, float]:
    values: dict[str, float] = {}
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.reader(stream):
            if len(row) < 4 or row[1].strip() != "Driver":
                continue
            try:
                values[row[2].strip()] = float(row[3])
            except ValueError:
                continue
    if "total_time" not in values:
        raise ValueError(f"missing Driver total_time in {path}")
    return values


def completed_wgs(values: dict[str, float]) -> int:
    value = values.get("max_wg_completed", values.get("total_wg_count"))
    if value is None:
        raise ValueError("missing completed-WG metric")
    return int(round(value))


def verify_success(root: Path, benchmark: str, config: str) -> None:
    path = root / f"baseline_{benchmark}_{config}_result.json"
    if not path.exists():
        return
    result = json.loads(path.read_text(encoding="utf-8"))
    if (
        not result.get("success")
        or result.get("returncode") != 0
        or result.get("simulator_returncode") != 0
    ):
        raise ValueError(f"unsuccessful experiment {path}")


def geomean(values: list[float]) -> float:
    return math.exp(sum(math.log(value) for value in values) / len(values))


def main() -> None:
    args = parse_args()
    roots = {
        "baseline": args.baseline_results.resolve(),
        "current": args.current_results.resolve(),
        "standalone": args.standalone_results.resolve(),
    }
    output_dir = args.output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    timings: dict[tuple[str, str], float] = {}
    speedups: dict[tuple[str, str], float] = {}
    work: dict[str, int] = {}

    for benchmark, _ in WORKLOADS:
        baseline_path = (
            roots["baseline"] / f"baseline_{benchmark}_baseline_metrics.csv"
        )
        verify_success(roots["baseline"], benchmark, "baseline")
        baseline_values = read_metrics(baseline_path)
        baseline_time = baseline_values["total_time"]
        baseline_wgs = completed_wgs(baseline_values)
        work[benchmark] = baseline_wgs
        timings[(benchmark, "baseline")] = baseline_time
        speedups[(benchmark, "baseline")] = 1.0

        for config in ("m1", "m2", "m3", "complete"):
            root = roots[CONFIG_SOURCES[config]]
            path = root / f"baseline_{benchmark}_{config}_metrics.csv"
            verify_success(root, benchmark, config)
            values = read_metrics(path)
            config_wgs = completed_wgs(values)
            if config_wgs != baseline_wgs:
                raise ValueError(
                    f"completed-WG mismatch for {benchmark} {config}: "
                    f"baseline={baseline_wgs}, result={config_wgs}"
                )
            timings[(benchmark, config)] = values["total_time"]
            speedups[(benchmark, config)] = baseline_time / values["total_time"]

    table_path = output_dir / "cupath_latest_ablation.csv"
    with table_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            ["benchmark", "label", "completed_wgs"]
            + [f"{config}_driver_time_s" for config in CONFIG_SOURCES]
            + [f"{config}_speedup" for config in CONFIG_SOURCES]
        )
        for benchmark, label in WORKLOADS:
            writer.writerow(
                [benchmark, label, work[benchmark]]
                + [timings[(benchmark, config)] for config in CONFIG_SOURCES]
                + [speedups[(benchmark, config)] for config in CONFIG_SOURCES]
            )
        writer.writerow(
            ["geomean_14", "GMEAN", ""]
            + [""] * len(CONFIG_SOURCES)
            + [
                geomean(
                    [speedups[(benchmark, config)] for benchmark, _ in WORKLOADS]
                )
                for config in CONFIG_SOURCES
            ]
        )

    overall_csv = output_dir / "cupath_baseline_complete_speedup.csv"
    with overall_csv.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            [
                "benchmark",
                "baseline_driver_time_s",
                "complete_driver_time_s",
                "baseline_speedup",
                "complete_speedup",
            ]
        )
        for benchmark, _ in WORKLOADS:
            writer.writerow(
                [
                    benchmark,
                    timings[(benchmark, "baseline")],
                    timings[(benchmark, "complete")],
                    1.0,
                    speedups[(benchmark, "complete")],
                ]
            )

    source_path = output_dir / "cupath_latest_figure_sources.csv"
    with source_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["configuration", "result_directory"])
        writer.writerow(["baseline", roots["baseline"]])
        writer.writerow(["m1", roots["current"]])
        writer.writerow(["m2", roots["standalone"]])
        writer.writerow(["m3", roots["standalone"]])
        writer.writerow(["complete", roots["current"]])

    ablation_plot.plot(
        output_dir / "cupath_latest_ablation_pasta_style", speedups
    )
    overall_plot.BENCHMARKS = [benchmark for benchmark, _ in WORKLOADS]
    overall_plot.plot(output_dir, args.dpi)

    summary = {
        config: geomean(
            [speedups[(benchmark, config)] for benchmark, _ in WORKLOADS]
        )
        for config in CONFIG_SOURCES
    }
    print(table_path)
    print(overall_csv)
    print(source_path)
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
