#!/usr/bin/env python3
"""Plot sensitivity Complete runs normalized to one fixed old Baseline."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path

from plot_cupath_sensitivity_campaign import write_tables
from plot_cupath_sensitivity_workload_bars import (
    BENCHMARKS,
    DIMENSIONS,
    geomean,
    load_campaign,
    plot,
)


def read_driver_time(path: Path) -> float:
    with path.open(newline="", encoding="utf-8") as handle:
        for row in csv.reader(handle):
            if (
                len(row) >= 4
                and row[1].strip() == "Driver"
                and row[2].strip() == "total_time"
            ):
                return float(row[3])
    raise ValueError(f"missing Driver total_time in {path}")


def rebase(rows, baseline_dir: Path):
    baseline_times = {
        benchmark: read_driver_time(
            baseline_dir / f"baseline_{benchmark}_baseline_metrics.csv"
        )
        for benchmark in BENCHMARKS
    }
    rebased = []
    for original in rows:
        row = dict(original)
        baseline = baseline_times[row["benchmark"]]
        complete = row["complete_time"]
        row["baseline_time"] = baseline
        row["speedup"] = "" if complete == "" else baseline / float(complete)
        row["status"] = "complete" if complete != "" else "pending"
        row["baseline_source"] = str(baseline_dir)
        rebased.append(row)
    return rebased


def summarize(rows):
    summaries = {}
    for dimension, spec in DIMENSIONS.items():
        for point in spec["points"]:
            values = [
                float(row["speedup"])
                for row in rows
                if row["dimension"] == dimension
                and row["point"] == point.name
                and row["speedup"] != ""
            ]
            summaries[(dimension, point.name)] = {
                "completed": len(values),
                "total": len(BENCHMARKS),
                "available_geomean": geomean(values) if values else None,
                "paper_geomean": (
                    geomean(values) if len(values) == len(BENCHMARKS) else None
                ),
            }
    return summaries


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("campaign", type=Path)
    parser.add_argument("--baseline-dir", type=Path, required=True)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    root = args.campaign.resolve()
    baseline_dir = args.baseline_dir.resolve()
    _, matched_rows, _ = load_campaign(root)
    rows = rebase(matched_rows, baseline_dir)
    summaries = summarize(rows)
    write_tables(root, rows, summaries)

    provenance = root / "cupath_sensitivity_normalization.json"
    provenance.write_text(
        json.dumps(
            {
                "normalization": "fixed_old_baseline",
                "baseline_directory": str(baseline_dir),
                "complete_directory": str(root),
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )
    print(provenance)
    plot(root, rows, args.dpi)


if __name__ == "__main__":
    main()
