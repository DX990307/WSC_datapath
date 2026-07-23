#!/usr/bin/env python3
"""Validate and summarize Combined DRAM row-on versus row-off A/B runs."""

import argparse
import csv
import math
from pathlib import Path

from analyze_remote_data_path import (
    component_latency_metrics,
    metric_sum,
    read_metrics,
)
from plot_complete_ablation import (
    WORKLOADS,
    binary_manifest_hash,
    driver_measurement,
    paired_evidence,
    validate_mechanism_config,
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--row-on-dir", type=Path, required=True)
    parser.add_argument("--row-off-dir", type=Path, required=True)
    parser.add_argument("--baseline-dir", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--allow-partial", action="store_true")
    parser.add_argument("--kmeans-membership-phase", action="store_true")
    parser.add_argument(
        "--allow-sampled-instruction-mismatch", action="store_true"
    )
    return parser.parse_args()


def primary_workloads(kmeans_membership_phase):
    for benchmark, label in WORKLOADS:
        if label == "SPMV":
            continue
        metrics_benchmark = benchmark
        scope = "bounded_max_wg_window"
        if benchmark == "kmeans" and kmeans_membership_phase:
            metrics_benchmark = "kmeans-reuse-smoke"
            scope = "kmeans_membership_phase"
        yield benchmark, metrics_benchmark, label, scope


def combined_path(root, benchmark):
    return root / f"baseline_{benchmark}_baseline_all_three_metrics.csv"


def row_off_path(root, benchmark):
    return root / f"baseline_{benchmark}_all_three_row_off_metrics.csv"


def baseline_path(root, benchmark):
    return root / f"baseline_{benchmark}_baseline_metrics.csv"


def finite_geomean(values):
    values = [value for value in values if math.isfinite(value)]
    if not values:
        return math.nan
    return math.exp(sum(math.log(value) for value in values) / len(values))


def percent_reduction(reference, value):
    if reference == 0:
        return 0.0 if value == 0 else math.nan
    return 100.0 * (reference - value) / reference


def latency_delta(off, on):
    if off in ("", 0) or on == "":
        return ""
    return 100.0 * (float(off) - float(on)) / float(off)


def analyze_pair(row_on, row_off, allow_sampled_mismatch, same_binary):
    on_measurement = driver_measurement(row_on)
    off_measurement = driver_measurement(row_off)
    validate_mechanism_config(on_measurement, row_on, "all_three")
    validate_mechanism_config(
        off_measurement, row_off, "all_three_row_off"
    )
    evidence = paired_evidence(
        row_off,
        row_on,
        allow_sampled_instruction_mismatch=allow_sampled_mismatch,
        same_frozen_binary=same_binary,
    )
    on_rows, _ = read_metrics(row_on)
    off_rows, _ = read_metrics(row_off)
    on_latency = component_latency_metrics(on_rows)
    off_latency = component_latency_metrics(off_rows)
    on_reads = metric_sum(on_rows, "dram_physical_read_accesses")
    off_reads = metric_sum(off_rows, "dram_physical_read_accesses")
    on_writes = metric_sum(on_rows, "dram_physical_write_accesses")
    off_writes = metric_sum(off_rows, "dram_physical_write_accesses")
    return {
        "row_on_over_row_off_speedup": evidence["speedup"],
        "row_on_driver_time_s": on_measurement["total_time"],
        "row_off_driver_time_s": off_measurement["total_time"],
        "row_on_cu_inst_count": on_measurement["cu_inst_count"],
        "row_off_cu_inst_count": off_measurement["cu_inst_count"],
        "work_validation_mode": evidence["work_validation_mode"],
        "validation_warning": evidence["validation_warning"],
        "row_on_dram_read_latency_ns": on_latency[
            "dram_read_avg_latency_ns"
        ],
        "row_off_dram_read_latency_ns": off_latency[
            "dram_read_avg_latency_ns"
        ],
        "row_on_dram_read_latency_reduction_pct": latency_delta(
            off_latency["dram_read_avg_latency_ns"],
            on_latency["dram_read_avg_latency_ns"],
        ),
        "row_on_dram_write_latency_ns": on_latency[
            "dram_write_avg_latency_ns"
        ],
        "row_off_dram_write_latency_ns": off_latency[
            "dram_write_avg_latency_ns"
        ],
        "row_on_dram_write_latency_reduction_pct": latency_delta(
            off_latency["dram_write_avg_latency_ns"],
            on_latency["dram_write_avg_latency_ns"],
        ),
        "row_on_physical_reads": on_reads,
        "row_off_physical_reads": off_reads,
        "row_on_physical_read_reduction_pct": percent_reduction(
            off_reads, on_reads
        ),
        "row_on_physical_writes": on_writes,
        "row_off_physical_writes": off_writes,
        "row_on_physical_write_reduction_pct": percent_reduction(
            off_writes, on_writes
        ),
        "row_reuse_hits": metric_sum(on_rows, "dram_row_reuse_hits"),
        "auto_precharge_stops": metric_sum(
            on_rows, "dram_row_auto_precharge_stops"
        ),
    }


def main():
    args = parse_args()
    on_hash = binary_manifest_hash(args.row_on_dir, required=True)
    off_hash = binary_manifest_hash(args.row_off_dir, required=True)
    if on_hash != off_hash:
        raise ValueError(f"frozen binary mismatch: {on_hash} != {off_hash}")
    if args.baseline_dir:
        baseline_hash = binary_manifest_hash(args.baseline_dir, required=True)
        if baseline_hash != on_hash:
            raise ValueError(
                f"baseline frozen binary mismatch: {baseline_hash} != {on_hash}"
            )

    records = []
    for _, metrics_benchmark, label, scope in primary_workloads(
        args.kmeans_membership_phase
    ):
        on_path = combined_path(args.row_on_dir, metrics_benchmark)
        off_path = row_off_path(args.row_off_dir, metrics_benchmark)
        if not on_path.is_file() or not off_path.is_file():
            if args.allow_partial:
                continue
            missing = [
                str(path) for path in (on_path, off_path) if not path.is_file()
            ]
            raise FileNotFoundError(", ".join(missing))
        record = {
            "benchmark": label,
            "execution_scope": scope,
            "binary_sha256": on_hash,
            "row_on_metrics_file": str(on_path),
            "row_off_metrics_file": str(off_path),
        }
        record.update(analyze_pair(
            on_path,
            off_path,
            args.allow_sampled_instruction_mismatch,
            True,
        ))
        record["row_on_speedup_vs_baseline"] = ""
        record["row_off_speedup_vs_baseline"] = ""
        if args.baseline_dir:
            base_path = baseline_path(args.baseline_dir, metrics_benchmark)
            if base_path.is_file():
                record["row_on_speedup_vs_baseline"] = paired_evidence(
                    base_path,
                    on_path,
                    allow_sampled_instruction_mismatch=(
                        args.allow_sampled_instruction_mismatch
                    ),
                    same_frozen_binary=True,
                )["speedup"]
                record["row_off_speedup_vs_baseline"] = paired_evidence(
                    base_path,
                    off_path,
                    allow_sampled_instruction_mismatch=(
                        args.allow_sampled_instruction_mismatch
                    ),
                    same_frozen_binary=True,
                )["speedup"]
        records.append(record)

    if records:
        summary = {field: "" for field in records[0]}
        summary["benchmark"] = "GMEAN"
        summary["execution_scope"] = "completed_pairs"
        summary["binary_sha256"] = on_hash
        summary["row_on_over_row_off_speedup"] = finite_geomean([
            float(record["row_on_over_row_off_speedup"])
            for record in records
        ])
        for field in (
            "row_on_speedup_vs_baseline", "row_off_speedup_vs_baseline"
        ):
            summary[field] = finite_geomean([
                float(record[field])
                for record in records if record[field] != ""
            ])
        records.append(summary)

    args.output.parent.mkdir(parents=True, exist_ok=True)
    if not records:
        args.output.write_text("")
        return
    with args.output.open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(records[0]))
        writer.writeheader()
        writer.writerows(records)


if __name__ == "__main__":
    main()
