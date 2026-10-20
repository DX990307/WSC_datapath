#!/usr/bin/env python3
"""Export per-controller DRAM and per-slice local-prefetch evidence.

The formal CSVs retain the component name on every metric row.  The main
paper plots aggregate those rows, so this companion analysis preserves the
controller/slice distribution required to audit imbalance and queue pressure
without changing or rerunning the frozen simulator.
"""

from __future__ import annotations

import argparse
import csv
import math
import re
import statistics
from collections import defaultdict
from pathlib import Path

from plot_cupath_typed_ablation import CONFIGS, METRICS_RE, WORKLOADS


DRAM_RE = re.compile(r"^GPU\[\d+\]\.DRAM\[\d+\]$")
L2_RE = re.compile(r"^GPU\[\d+\]\.L2\[\d+\]$")
COMPONENT_FIELDS = (
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
    "dram_row_column_commands",
    "dram_row_activate_commands",
    "dram_row_precharge_commands",
    "dram_row_max_queue_age_cycles",
    "filter_prefetch_candidates",
    "filter_prefetch_issued",
    "filter_prefetch_useful",
    "filter_prefetch_mshr_drops",
    "filter_prefetch_controller_busy_drops",
)
MAX_FIELDS = {"dram_row_max_queue_age_cycles"}


def read_components(path: Path) -> tuple[float, dict[str, dict[str, float]]]:
    driver_time = 0.0
    components: dict[str, dict[str, float]] = defaultdict(
        lambda: defaultdict(float)
    )
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            where = row["where"].strip()
            what = row["what"].strip()
            value = float(row["value"])
            if where == "Driver" and what == "total_time":
                driver_time = value
            if not (DRAM_RE.match(where) or L2_RE.match(where)):
                continue
            if what not in COMPONENT_FIELDS:
                continue
            if what in MAX_FIELDS:
                components[where][what] = max(
                    components[where].get(what, 0.0), value
                )
            else:
                components[where][what] += value
    if driver_time <= 0:
        raise ValueError(f"Driver total_time missing: {path}")
    return driver_time, {name: dict(values) for name, values in components.items()}


def coefficient_of_variation(values: list[float]) -> float | None:
    if not values or sum(values) == 0:
        return None
    mean = statistics.fmean(values)
    return statistics.pstdev(values) / mean if mean else None


def summarize(
    driver_time: float,
    components: dict[str, dict[str, float]],
) -> dict[str, float | int | None]:
    drams = [values for name, values in components.items() if DRAM_RE.match(name)]
    slices = [values for name, values in components.items() if L2_RE.match(name)]
    columns = [values.get("dram_row_column_commands", 0.0) for values in drams]
    candidates = [values.get("filter_prefetch_candidates", 0.0) for values in slices]
    cycles = driver_time * 1e9
    return {
        "dram_instances": len(drams),
        "dram_physical_reads": sum(
            values.get("dram_physical_read_accesses", 0.0) for values in drams
        ),
        "dram_physical_writes": sum(
            values.get("dram_physical_write_accesses", 0.0) for values in drams
        ),
        "dram_column_commands": sum(columns),
        # This is an issue-rate proxy, not a claim that every DRAM command
        # occupies exactly one full controller cycle.
        "dram_column_commands_per_instance_cycle": (
            sum(columns) / (cycles * len(drams)) if drams and cycles > 0 else None
        ),
        "dram_column_command_cv": coefficient_of_variation(columns),
        "dram_max_queue_age_cycles": max(
            (values.get("dram_row_max_queue_age_cycles", 0.0) for values in drams),
            default=0.0,
        ),
        "l2_slices": len(slices),
        "prefetch_candidate_slices": sum(value > 0 for value in candidates),
        "prefetch_candidates": sum(candidates),
        "prefetch_candidate_cv": coefficient_of_variation(candidates),
        "prefetch_issued": sum(
            values.get("filter_prefetch_issued", 0.0) for values in slices
        ),
        "prefetch_useful": sum(
            values.get("filter_prefetch_useful", 0.0) for values in slices
        ),
        "prefetch_mshr_drops": sum(
            values.get("filter_prefetch_mshr_drops", 0.0) for values in slices
        ),
        "prefetch_controller_busy_drops": sum(
            values.get("filter_prefetch_controller_busy_drops", 0.0)
            for values in slices
        ),
    }


def value_text(value: float | int | None) -> str | float | int:
    if value is None or isinstance(value, float) and not math.isfinite(value):
        return ""
    return value


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    root = args.results.resolve()
    output = (args.output_dir or root).resolve()
    output.mkdir(parents=True, exist_ok=True)
    paper_benchmarks = {name for name, _, _ in WORKLOADS}
    paper_configs = {name for name, _, _ in CONFIGS}

    long_rows: list[dict[str, object]] = []
    summary_rows: list[dict[str, object]] = []
    for path in sorted(root.glob("*_metrics.csv")):
        match = METRICS_RE.match(path.name)
        if not match:
            continue
        benchmark = match.group("benchmark")
        config = match.group("config")
        if benchmark not in paper_benchmarks or config not in paper_configs:
            continue
        driver_time, components = read_components(path)
        for component, metrics in sorted(components.items()):
            long_rows.append({
                "benchmark": benchmark,
                "config": config,
                "component_type": "DRAM" if DRAM_RE.match(component) else "L2",
                "component": component,
                "driver_time_s": driver_time,
                **{field: metrics.get(field, 0.0) for field in COMPONENT_FIELDS},
            })
        summary_rows.append({
            "benchmark": benchmark,
            "config": config,
            **summarize(driver_time, components),
        })

    long_path = output / "cupath_controller_slice_detail.csv"
    long_fields = (
        "benchmark", "config", "component_type", "component", "driver_time_s",
        *COMPONENT_FIELDS,
    )
    with long_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=long_fields)
        writer.writeheader()
        writer.writerows(long_rows)

    summary_path = output / "cupath_controller_slice_summary.csv"
    summary_fields = tuple(summary_rows[0]) if summary_rows else (
        "benchmark", "config"
    )
    with summary_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=summary_fields)
        writer.writeheader()
        for row in summary_rows:
            writer.writerow({key: value_text(value) for key, value in row.items()})

    print(long_path)
    print(summary_path)


if __name__ == "__main__":
    main()
