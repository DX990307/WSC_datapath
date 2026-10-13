#!/usr/bin/env python3
"""Audit CuPath Filter and RDMA capacity from completed metric files.

The simulator currently reports ending occupancy and a peak for each physical
Filter.  It does not report a time integral, so this script deliberately does
not relabel ending occupancy as temporal-average occupancy.  A sum of
per-Filter peaks is also identified as a conservative peak sum because the
individual peaks need not occur in the same cycle.
"""

from __future__ import annotations

import argparse
import csv
import json
import re
from collections import defaultdict
from pathlib import Path


FILTER_TYPES = (
    "resident",
    "pending",
    "seen",
    "pattern",
    "granularity_pending",
)
ACTIVE_TYPES = ("resident", "pending", "granularity_pending")
HISTORY_TYPES = ("seen", "pattern")
EVENT_SUFFIXES = (
    "queries",
    "insertions",
    "insert_failures",
    "reference_count_saturations",
    "fail_open",
    "lookup_busy_drops",
    "update_busy_drops",
)
L2_RE = re.compile(r"^GPU\[\d+\]\.L2\[\d+\]$")
RDMA_RE = re.compile(r"^GPU\[\d+\]\.RDMA$")


def benchmark_from_path(path: Path) -> str:
    name = path.name
    prefix = "baseline_"
    suffix = "_complete_metrics.csv"
    if not name.startswith(prefix) or not name.endswith(suffix):
        return path.stem
    return name[len(prefix):-len(suffix)]


def read_metrics(path: Path) -> dict[tuple[str, str], float]:
    metrics: dict[tuple[str, str], float] = {}
    with path.open(newline="", encoding="utf-8") as handle:
        for row in csv.reader(handle):
            if len(row) < 4:
                continue
            where, metric = row[1].strip(), row[2].strip()
            try:
                value = float(row[3].strip())
            except ValueError:
                continue
            metrics[(where, metric)] = value
    return metrics


def values_for(
    metrics: dict[tuple[str, str], float],
    where_pattern: re.Pattern[str],
    metric: str,
) -> list[float]:
    return [
        value for (where, name), value in metrics.items()
        if name == metric and where_pattern.fullmatch(where)
    ]


def total(values: list[float]) -> float:
    return sum(values) if values else 0.0


def ratio(numerator: float, denominator: float) -> float:
    return numerator / denominator if denominator else 0.0


def analyze_one(path: Path) -> tuple[dict[str, float | str], dict[str, dict[str, float | None]]]:
    metrics = read_metrics(path)
    row: dict[str, float | str] = {"benchmark": benchmark_from_path(path)}

    slots = values_for(metrics, L2_RE, "typed_filter_slots")
    peaks = values_for(metrics, L2_RE, "typed_filter_peak_occupancy")
    endings = values_for(metrics, L2_RE, "typed_filter_occupancy")
    row.update({
        "physical_filters": len(slots),
        "slots_per_filter": slots[0] if slots else 0,
        "slots_total": total(slots),
        "ending_occupancy_sum": total(endings),
        "ending_occupancy_fraction": ratio(total(endings), total(slots)),
        "mean_ending_occupancy_per_filter": ratio(total(endings), len(endings)),
        "sum_of_filter_peaks": total(peaks),
        "sum_of_filter_peaks_fraction": ratio(total(peaks), total(slots)),
        "mean_per_filter_peak": ratio(total(peaks), len(peaks)),
        "maximum_individual_filter_peak": max(peaks, default=0.0),
        "maximum_individual_filter_peak_fraction": ratio(
            max(peaks, default=0.0), slots[0] if slots else 0.0
        ),
    })

    type_rows: dict[str, dict[str, float | None]] = {}
    for kind in FILTER_TYPES:
        data: dict[str, float | None] = {}
        for suffix in EVENT_SUFFIXES:
            name = f"typed_filter_{kind}_{suffix}"
            values = values_for(metrics, L2_RE, name)
            data[suffix] = total(values) if values else None
        for suffix in ("occupancy", "peak_occupancy"):
            name = f"typed_filter_{kind}_{suffix}"
            values = values_for(metrics, L2_RE, name)
            data[suffix] = total(values) if values else None
        insertions = data["insertions"] or 0.0
        failures = data["insert_failures"] or 0.0
        queries = data["queries"] or 0.0
        fallbacks = (data["fail_open"] or 0.0) + (
            data["lookup_busy_drops"] or 0.0
        )
        data["insertion_failure_fraction"] = ratio(failures, insertions)
        data["lookup_fallback_fraction"] = ratio(fallbacks, queries)
        type_rows[kind] = data
        for key, value in data.items():
            row[f"{kind}_{key}"] = "" if value is None else value

    for group, kinds in (("active", ACTIVE_TYPES), ("history", HISTORY_TYPES)):
        insertions = sum(float(type_rows[k]["insertions"] or 0) for k in kinds)
        failures = sum(float(type_rows[k]["insert_failures"] or 0) for k in kinds)
        fail_open = sum(float(type_rows[k]["fail_open"] or 0) for k in kinds)
        queries = sum(float(type_rows[k]["queries"] or 0) for k in kinds)
        row[f"{group}_insertions"] = insertions
        row[f"{group}_insert_failures"] = failures
        row[f"{group}_insertion_failure_fraction"] = ratio(failures, insertions)
        row[f"{group}_fail_open"] = fail_open
        row[f"{group}_fail_open_fraction"] = ratio(fail_open, queries)

    for metric in (
        "remote_config_line_entries",
        "remote_peak_line_entries",
        "remote_config_waiter_entries",
        "remote_peak_waiter_entries",
        "remote_config_owner_child_lines",
        "remote_owner_peak_child_lines",
        "remote_line_entry_full_stalls",
        "remote_waiter_entry_full_stalls",
        "remote_owner_child_line_full_stalls",
    ):
        values = values_for(metrics, RDMA_RE, metric)
        row[f"{metric}_max"] = max(values, default=0.0)
        row[f"{metric}_sum"] = total(values)

    pair_peaks = values_for(metrics, L2_RE, "adaptive_pair_peak_prefetch_only_lines")
    row["adaptive_pair_peak_sum"] = total(pair_peaks)
    row["adaptive_pair_peak_max"] = max(pair_peaks, default=0.0)
    return row, type_rows


def format_percent(value: float) -> str:
    return f"{100.0 * value:.3f}%"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("campaign", type=Path)
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    output = args.output_dir or args.campaign / "capacity_audit"
    output.mkdir(parents=True, exist_ok=True)

    paths = sorted(args.campaign.glob("*_complete_metrics.csv"))
    if not paths:
        raise SystemExit(f"no complete metric files in {args.campaign}")
    rows = []
    all_types: dict[str, dict[str, float | None]] = {
        kind: {suffix: 0.0 for suffix in EVENT_SUFFIXES}
        for kind in FILTER_TYPES
    }
    saturation_available = {kind: True for kind in FILTER_TYPES}
    for path in paths:
        row, type_rows = analyze_one(path)
        rows.append(row)
        for kind, data in type_rows.items():
            for suffix in EVENT_SUFFIXES:
                value = data[suffix]
                if value is None:
                    saturation_available[kind] = False
                    continue
                all_types[kind][suffix] = float(all_types[kind][suffix] or 0) + value

    with (output / "capacity_by_benchmark.csv").open(
        "w", newline="", encoding="utf-8"
    ) as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)

    type_output = []
    for kind in FILTER_TYPES:
        data = all_types[kind]
        insertions = float(data["insertions"] or 0)
        failures = float(data["insert_failures"] or 0)
        queries = float(data["queries"] or 0)
        fallback = float(data["fail_open"] or 0) + float(
            data["lookup_busy_drops"] or 0
        )
        output_data = dict(data)
        if not saturation_available[kind]:
            output_data["reference_count_saturations"] = ""
        type_output.append({
            "type": kind,
            **output_data,
            "insertion_failure_fraction": ratio(failures, insertions),
            "lookup_fallback_fraction": ratio(fallback, queries),
            "saturation_counter_available": saturation_available[kind],
        })
    with (output / "filter_type_events.csv").open(
        "w", newline="", encoding="utf-8"
    ) as handle:
        writer = csv.DictWriter(handle, fieldnames=list(type_output[0]))
        writer.writeheader()
        writer.writerows(type_output)

    worst_peak = max(rows, key=lambda item: float(item["sum_of_filter_peaks_fraction"]))
    worst_individual = max(
        rows, key=lambda item: float(item["maximum_individual_filter_peak_fraction"])
    )
    active_insertions = sum(float(row["active_insertions"]) for row in rows)
    active_failures = sum(float(row["active_insert_failures"]) for row in rows)
    history_insertions = sum(float(row["history_insertions"]) for row in rows)
    history_failures = sum(float(row["history_insert_failures"]) for row in rows)
    saturation_reported = all(saturation_available.values())
    summary = {
        "workloads": len(rows),
        "physical_filters_per_wafer": int(rows[0]["physical_filters"]),
        "physical_filters_per_gpm": int(rows[0]["physical_filters"]) // 48,
        "slots_per_physical_filter": int(rows[0]["slots_per_filter"]),
        "buckets_per_physical_filter": int(rows[0]["slots_per_filter"]) // 4,
        "worst_sum_of_per_filter_peaks_benchmark": worst_peak["benchmark"],
        "worst_sum_of_per_filter_peaks_fraction": worst_peak["sum_of_filter_peaks_fraction"],
        "worst_individual_filter_benchmark": worst_individual["benchmark"],
        "worst_individual_filter_peak_fraction": worst_individual[
            "maximum_individual_filter_peak_fraction"
        ],
        "active_insertions": active_insertions,
        "active_insert_failures": active_failures,
        "active_insertion_failure_fraction": ratio(active_failures, active_insertions),
        "history_insertions": history_insertions,
        "history_insert_failures": history_failures,
        "history_insertion_failure_fraction": ratio(history_failures, history_insertions),
        "reference_count_saturation_metrics_present": saturation_reported,
        "temporal_average_occupancy_available": False,
    }
    (output / "capacity_summary.json").write_text(
        json.dumps(summary, indent=2) + "\n", encoding="utf-8"
    )

    lines = [
        "# CuPath capacity audit",
        "",
        f"Campaign: `{args.campaign}`",
        "",
        f"- {summary['physical_filters_per_gpm']} physical Filters/GPM, "
        f"{summary['buckets_per_physical_filter']:,} buckets and "
        f"{summary['slots_per_physical_filter']:,} slots per Filter.",
        f"- Worst conservative sum of per-Filter peaks: "
        f"{worst_peak['benchmark']} at "
        f"{format_percent(float(worst_peak['sum_of_filter_peaks_fraction']))}.",
        f"- Worst individual Filter peak: {worst_individual['benchmark']} at "
        f"{format_percent(float(worst_individual['maximum_individual_filter_peak_fraction']))}.",
        f"- Active-state insertion failures: {active_failures:,.0f}/"
        f"{active_insertions:,.0f} "
        f"({format_percent(ratio(active_failures, active_insertions))}).",
        f"- History-state insertion failures: {history_failures:,.0f}/"
        f"{history_insertions:,.0f} "
        f"({format_percent(ratio(history_failures, history_insertions))}).",
        "- Temporal-average occupancy is not present in these completed metrics. "
        "Ending occupancy and per-Filter peaks are reported without relabeling "
        "either as a temporal average.",
        "- Reference-count saturation counters are "
        + ("present." if saturation_reported else "absent from this frozen binary's metrics; the value is unknown, not zero."),
        "",
        "## Per-workload occupancy",
        "",
        "| Workload | Ending occupancy | Sum of Filter peaks | Worst Filter peak |",
        "|---|---:|---:|---:|",
    ]
    for row in rows:
        lines.append(
            f"| {row['benchmark']} | "
            f"{format_percent(float(row['ending_occupancy_fraction']))} | "
            f"{format_percent(float(row['sum_of_filter_peaks_fraction']))} | "
            f"{format_percent(float(row['maximum_individual_filter_peak_fraction']))} |"
        )
    (output / "CAPACITY_AUDIT.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
