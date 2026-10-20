#!/usr/bin/env python3
"""Summarize driver allocation-page metrics across benchmark result files."""

import argparse
import csv
import glob
from pathlib import Path

from runall2_constants import ALL_BENCHMARKS


METRICS = (
    "allocation_page_size",
    "allocation_workload_calls",
    "allocation_workload_requested_bytes",
    "allocation_workload_allocated_pages",
    "allocation_workload_rounded_bytes",
    "allocation_runtime_calls",
    "allocation_runtime_requested_bytes",
    "allocation_runtime_allocated_pages",
    "allocation_runtime_rounded_bytes",
    "allocation_overall_calls",
    "allocation_overall_requested_bytes",
    "allocation_overall_allocated_pages",
    "allocation_overall_rounded_bytes",
)


def discover(inputs):
    files = []
    for item in inputs:
        matches = [Path(path) for path in glob.glob(item)]
        if not matches:
            matches = [Path(item)]
        for path in matches:
            if path.is_dir():
                files.extend(path.rglob("*_metrics.csv"))
            elif path.is_file():
                files.append(path)
    return sorted(set(files))


def identity(path):
    stem = path.name.removesuffix("_metrics.csv")
    for benchmark in sorted(ALL_BENCHMARKS, key=len, reverse=True):
        marker = f"_{benchmark}_"
        if marker not in stem:
            continue
        target, config = stem.split(marker, 1)
        return target, benchmark, config
    return "unknown", "unknown", stem


def read_metrics(path):
    values = {}
    with path.open(newline="") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            if row.get("where") != "Driver":
                continue
            name = row.get("what", "")
            if name not in METRICS:
                continue
            values[name] = int(round(float(row["value"])))
    return values


def mib(value):
    return f"{value / (1024 * 1024):.3f}"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("inputs", nargs="+", help="result directories or metrics CSVs")
    parser.add_argument("--output", help="output CSV path")
    parser.add_argument(
        "--all-configs", action="store_true",
        help="include every configuration instead of baseline only",
    )
    args = parser.parse_args()

    rows = []
    missing = []
    for path in discover(args.inputs):
        target, benchmark, config = identity(path)
        if not args.all_configs and config != "baseline":
            continue
        values = read_metrics(path)
        if "allocation_workload_allocated_pages" not in values:
            missing.append(path)
            continue
        rows.append({
            "target": target,
            "benchmark": benchmark,
            "config": config,
            "page_size_bytes": values["allocation_page_size"],
            "workload_calls": values["allocation_workload_calls"],
            "workload_requested_bytes": values["allocation_workload_requested_bytes"],
            "workload_allocated_pages": values["allocation_workload_allocated_pages"],
            "workload_rounded_bytes": values["allocation_workload_rounded_bytes"],
            "runtime_calls": values["allocation_runtime_calls"],
            "runtime_allocated_pages": values["allocation_runtime_allocated_pages"],
            "overall_allocated_pages": values["allocation_overall_allocated_pages"],
            "source": str(path),
        })

    rows.sort(key=lambda row: (row["benchmark"], row["config"], row["target"]))
    if not rows:
        raise SystemExit(
            "no allocation metrics found; rebuild the benchmark and generate new metrics"
        )

    fields = list(rows[0])
    if args.output:
        output = Path(args.output)
        output.parent.mkdir(parents=True, exist_ok=True)
        with output.open("w", newline="") as stream:
            writer = csv.DictWriter(stream, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)

    print("| Benchmark | Config | Page size | Workload pages | Workload MiB | Runtime pages | Overall pages |")
    print("|---|---|---:|---:|---:|---:|---:|")
    for row in rows:
        print(
            f"| {row['benchmark']} | {row['config']} | {row['page_size_bytes']} | "
            f"{row['workload_allocated_pages']} | {mib(row['workload_rounded_bytes'])} | "
            f"{row['runtime_allocated_pages']} | {row['overall_allocated_pages']} |"
        )
    if missing:
        print(f"Skipped {len(missing)} older metrics files without allocation counters.")


if __name__ == "__main__":
    main()
