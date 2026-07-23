#!/usr/bin/env python3
"""Compare M1 candidate screens without inventing cross-WG speedups."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path

import analyze_m1_paired_read


FIELDS = (
    "label",
    "benchmark",
    "binary_sha256",
    "success",
    "wg_observed_count",
    "wg_set_sha256",
    "driver_time_us",
    "formal_time_comparison",
    "time_ratio_vs_reference",
    "patterns_established",
    "pattern_replacements",
    "predicted_candidates",
    "accepted_speculative_pairs",
    "demand_pair_filter_probes",
    "demand_pair_filter_positives",
    "demand_pair_filter_negatives",
    "demand_pair_filter_exact_avoidance",
    "ready_demand_pair_opportunities",
    "ready_demand_pairs",
    "paired_descriptors",
    "useful_siblings",
    "timely_siblings",
    "late_siblings",
    "terminal_unused_siblings",
    "sibling_accuracy",
    "timely_fraction",
    "mshr_pressure_drops",
    "dram_queue_pressure_drops",
    "physical_reads",
    "demand_latency_ns",
    "pair_row_reuse_rate",
    "immediate_pair_continuations",
    "result_dir",
)


def parse_spec(value: str) -> tuple[str, Path]:
    if "=" not in value:
        raise argparse.ArgumentTypeError("candidate must be LABEL=RESULT_DIR")
    label, path = value.split("=", 1)
    if not label or not path:
        raise argparse.ArgumentTypeError("candidate must be LABEL=RESULT_DIR")
    return label, Path(path)


def binary_sha(directory: Path) -> str:
    path = directory / "EXPERIMENT_BINARIES.json"
    try:
        metadata = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return ""
    values = metadata.get("sha256_by_target", {})
    if not isinstance(values, dict):
        return ""
    unique = sorted({str(value) for value in values.values() if value})
    return unique[0] if len(unique) == 1 else ";".join(unique)


def candidate_rows(label: str, directory: Path) -> list[dict]:
    sha = binary_sha(directory)
    result = []
    try:
        sources = analyze_m1_paired_read.summarize_directory(directory)
    except FileNotFoundError:
        return result
    for source in sources:
        if source["config"] not in {"m1", "new_m1"}:
            continue
        result.append({
            "label": label,
            "benchmark": source["benchmark"],
            "binary_sha256": sha,
            "success": source["success"],
            "wg_observed_count": source["wg_observed_count"],
            "wg_set_sha256": source["wg_global_set_sha256"],
            "driver_time_us": source["driver_total_time"] * 1e6,
            "formal_time_comparison": 0,
            "time_ratio_vs_reference": "",
            "patterns_established": source["granularity_patterns_established"],
            "pattern_replacements": source[
                "granularity_predictor_pattern_replacements"
            ],
            "predicted_candidates": source["granularity_predicted_candidates"],
            "accepted_speculative_pairs": source[
                "granularity_accepted_aggregates"
            ],
            "demand_pair_filter_probes": source[
                "granularity_demand_pair_filter_probes"
            ],
            "demand_pair_filter_positives": source[
                "granularity_demand_pair_filter_positives"
            ],
            "demand_pair_filter_negatives": source[
                "granularity_demand_pair_filter_negatives"
            ],
            "demand_pair_filter_exact_avoidance": source[
                "demand_pair_filter_exact_avoidance"
            ],
            "ready_demand_pair_opportunities": source[
                "granularity_demand_pair_ready_opportunities"
            ],
            "ready_demand_pairs": source[
                "granularity_demand_pair_aggregates"
            ],
            "paired_descriptors": source[
                "granularity_frontend_paired_read_aggregates"
            ],
            "useful_siblings": source["granularity_useful_sibling_lines"],
            "timely_siblings": source["granularity_timely_sibling_lines"],
            "late_siblings": source["granularity_late_sibling_lines"],
            "terminal_unused_siblings": source[
                "granularity_terminal_unused_sibling_lines"
            ],
            "sibling_accuracy": source["sibling_accuracy"],
            "timely_fraction": source["sibling_timely_fraction"],
            "mshr_pressure_drops": source["granularity_mshr_pressure_drops"],
            "dram_queue_pressure_drops": source[
                "granularity_dram_queue_pressure_drops"
            ],
            "physical_reads": source["dram_physical_read_accesses"],
            "demand_latency_ns": source["l2_demand_read_latency_avg_ns"],
            "pair_row_reuse_rate": source["paired_row_reuse_rate"],
            "immediate_pair_continuations": source[
                "dram_aggregate_immediate_continuations"
            ],
            "result_dir": str(directory.resolve()),
        })
    return result


def add_reference_ratios(rows: list[dict], reference_label: str) -> None:
    reference = {
        row["benchmark"]: row
        for row in rows
        if row["label"] == reference_label and row["success"]
    }
    for row in rows:
        base = reference.get(row["benchmark"])
        same_work = bool(
            base
            and row["success"]
            and base["wg_set_sha256"]
            and row["wg_set_sha256"] == base["wg_set_sha256"]
            and base["wg_observed_count"] == row["wg_observed_count"]
        )
        row["formal_time_comparison"] = int(same_work)
        row["time_ratio_vs_reference"] = ""
        if same_work and row["driver_time_us"] > 0:
            row["time_ratio_vs_reference"] = (
                base["driver_time_us"] / row["driver_time_us"]
            )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("candidate", nargs="+", type=parse_spec)
    parser.add_argument("--reference-label")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    reference_label = args.reference_label or args.candidate[0][0]
    rows = []
    for label, directory in args.candidate:
        rows.extend(candidate_rows(label, directory))
    add_reference_ratios(rows, reference_label)
    rows.sort(key=lambda row: (row["benchmark"], row["label"]))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=FIELDS)
        writer.writeheader()
        writer.writerows(rows)
    print(args.output)
    print(f"rows={len(rows)} reference={reference_label}")
    print(
        "formal_time_rows="
        f"{sum(row['formal_time_comparison'] for row in rows)}"
    )


if __name__ == "__main__":
    main()
