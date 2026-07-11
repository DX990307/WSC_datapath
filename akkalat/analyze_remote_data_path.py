#!/usr/bin/env python3
"""Summarize whether the requester-side remote data path pays off."""

from __future__ import annotations

import argparse
import csv
import math
import re
from collections import defaultdict
from pathlib import Path
from typing import Iterable


RDMA_RE = re.compile(r"^GPU\[(\d+)]\.RDMA$")
L2_RE = re.compile(r"^GPU\[(\d+)]\.L2\[(\d+)]$")
BATCH_RE = re.compile(r"^remote_batch_size_(\d+)_packets$")
REMOTE_ABLATION_ORDER = (
    "baseline",
    "baseline_dram_batch_only",
    "baseline_remote_request_only",
    "baseline_remote_l2_only",
    "baseline_all_three",
    "baseline_all_three_prefetch",
)
REMOTE_ABLATION_CONFIGS = tuple(
    sorted(REMOTE_ABLATION_ORDER, key=len, reverse=True)
)

SUM_METRICS = [
    "dram_batch_miss_lines",
    "dram_batches_created",
    "dram_batches_drained",
    "dram_batch_lines",
    "dram_batch_singleton_fallbacks",
    "dram_batch_full_drains",
    "dram_batch_timeout_drains",
    "dram_batch_capacity_drains",
    "dram_batch_drain_drains",
    "dram_batch_wait_total_ns",
    "dram_batch_wait_samples",
    "dram_batch_multiline_reads",
    "dram_batch_singleline_reads",
    "remote_logical_reads",
    "remote_wire_lines",
    "remote_demand_wire_lines",
    "remote_prefetch_wire_lines",
    "remote_duplicate_reads",
    "remote_pre_send_merges",
    "remote_inflight_merges",
    "remote_ready_merges",
    "remote_l2_probe_hits",
    "remote_l2_probe_misses",
    "remote_l2_logical_responses",
    "remote_single_packets",
    "remote_bitmap_packets",
    "remote_bitmap_lines",
    "remote_au_prefetch_candidates",
    "remote_au_prefetch_converted_demand",
    "remote_au_prefetch_demand_merges",
    "remote_two_touch_candidates",
    "remote_two_touch_fill_attempts",
    "remote_prefetch_fill_attempts",
    "remote_two_touch_installed_fills",
    "remote_prefetch_installed_fills",
    "remote_fanout_responses",
    "remote_network_request_bytes",
    "remote_network_response_bytes",
    "remote_batch_queue_wait_samples",
    "remote_batch_queue_wait_total_ns",
    "remote_pre_network_wait_samples",
    "remote_pre_network_wait_total_ns",
    "remote_probe_latency_samples",
    "remote_probe_latency_total_ns",
    "remote_logical_read_latency_samples",
    "remote_logical_read_latency_total_ns",
    "remote_full_flushes",
    "remote_work_conserving_flushes",
    "remote_timeout_flushes",
    "remote_capacity_flushes",
    "remote_conflict_flushes",
    "remote_drain_flushes",
    "remote_filter_queries",
    "remote_filter_positives",
    "remote_filter_negatives",
    "remote_filter_false_positives",
    "remote_filter_true_positive_unavailable",
    "remote_clean_fills",
    "remote_l2_two_touch_fill_attempts",
    "remote_l2_prefetch_fill_attempts",
    "remote_installed_fills",
    "remote_l2_two_touch_installed_fills",
    "remote_l2_prefetch_installed_fills",
    "remote_dropped_fills",
    "remote_l2_two_touch_dropped_fills",
    "remote_l2_prefetch_dropped_fills",
    "remote_filter_insert_failures",
    "remote_tracked_evictions",
    "remote_unused_two_touch_retirements",
    "remote_unused_prefetch_retirements",
    "remote_replica_probe_hits",
    "remote_two_touch_replica_hits",
    "remote_prefetch_replica_hits",
    "remote_useful_two_touch_fills",
    "remote_useful_prefetch_fills",
    "remote_fill_into_invalid",
    "remote_fill_replaced_remote",
    "remote_fill_displaced_local_clean",
    "remote_two_touch_local_displacements",
    "remote_prefetch_local_displacements",
    "remote_current_replicas",
    "remote_peak_replicas",
]

MAX_METRICS = [
    "dram_batch_max_lines",
    "remote_batch_queue_wait_max_ns",
    "remote_pre_network_wait_max_ns",
    "remote_probe_latency_max_ns",
    "remote_logical_read_latency_max_ns",
]

CONFIG_METRICS = [
    "dram_batch_enabled",
    "remote_dedup_enabled",
    "remote_batching_enabled",
    "remote_requester_l2_enabled",
    "remote_config_batch_lines",
    "remote_config_wait_ns",
    "remote_config_max_batches",
    "remote_config_reuse_entries",
    "remote_au_prefetch_enabled",
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Analyze batching cost, wire savings, L2 replica reuse, and "
            "prefetch usefulness from simulator metrics CSV files."
        )
    )
    parser.add_argument("input", type=Path, help="metrics CSV or result directory")
    parser.add_argument(
        "--baseline-dir",
        type=Path,
        help="optional remote-off CSV/directory; files are paired by relative path/name",
    )
    parser.add_argument("--output", type=Path, help="summary CSV output path")
    parser.add_argument(
        "--flit-bytes",
        type=int,
        default=16,
        help="NoC flit payload bytes (default: 16)",
    )
    parser.add_argument(
        "--encoding-overhead",
        type=float,
        default=0.25,
        help="NoC encoding overhead fraction (default: 0.25)",
    )
    parser.add_argument(
        "--strict",
        action="store_true",
        help="return non-zero when a completed enabled run violates a balance check",
    )
    return parser.parse_args()


def discover_metrics(path: Path) -> list[Path]:
    if path.is_file():
        return [path]
    if not path.is_dir():
        raise FileNotFoundError(path)
    return sorted(path.rglob("*_metrics.csv"))


def read_metrics(path: Path) -> tuple[list[dict[str, str]], list[str]]:
    rows: list[dict[str, str]] = []
    warnings: list[str] = []
    seen: set[tuple[str, str]] = set()
    with path.open(newline="") as stream:
        for raw in csv.DictReader(stream, skipinitialspace=True):
            row = {
                (key or "").strip(): (value or "").strip()
                for key, value in raw.items()
            }
            where, what = row.get("where", ""), row.get("what", "")
            if not where or not what:
                continue
            pair = (where, what)
            if what.startswith(("remote_", "dram_batch")):
                if pair in seen:
                    warnings.append(f"duplicate metric {where}/{what}")
                seen.add(pair)
            rows.append(row)
    return rows, warnings


def value(row: dict[str, str]) -> float:
    return float(row.get("value", "0") or 0)


def metric_values(rows: Iterable[dict[str, str]], what: str) -> list[float]:
    return [value(row) for row in rows if row.get("what") == what]


def metric_sum(rows: Iterable[dict[str, str]], what: str) -> float:
    return sum(metric_values(rows, what))


def metric_max(rows: Iterable[dict[str, str]], what: str) -> float:
    values = metric_values(rows, what)
    return max(values, default=0.0)


def component_metric(
    rows: Iterable[dict[str, str]], where: str, what: str
) -> float | str:
    values = [
        value(row)
        for row in rows
        if row.get("where") == where and row.get("what") == what
    ]
    return values[0] if values else ""


def max_cp_kernel_time(rows: Iterable[dict[str, str]]) -> float | str:
    values = [
        value(row)
        for row in rows
        if row.get("what") == "kernel_time"
        and row.get("where", "").endswith(".CommandProcessor")
    ]
    return max(values) if values else ""


def safe_div(numerator: float, denominator: float) -> float | str:
    return numerator / denominator if denominator else ""


def percent(numerator: float, denominator: float) -> float | str:
    result = safe_div(numerator, denominator)
    return result * 100.0 if result != "" else ""


def percent_saved(baseline: float, actual: float) -> float | str:
    return percent(baseline - actual, baseline)


def speedup(baseline: float | str, experiment: float | str) -> float | str:
    if baseline == "" or experiment == "" or float(experiment) == 0:
        return ""
    return float(baseline) / float(experiment)


def encoded_flits(byte_count: int, flit_bytes: int, overhead: float) -> int:
    if byte_count <= 0:
        return 1
    encoded = byte_count + math.ceil(byte_count * overhead)
    return (encoded + flit_bytes - 1) // flit_bytes


def batch_histogram(rows: Iterable[dict[str, str]]) -> dict[int, int]:
    histogram: dict[int, int] = defaultdict(int)
    for row in rows:
        match = BATCH_RE.match(row.get("what", ""))
        if match:
            histogram[int(match.group(1))] += int(round(value(row)))
    return dict(histogram)


def histogram_percentile(histogram: dict[int, int], percentile: float) -> float | str:
    total = sum(histogram.values())
    if total == 0:
        return ""
    target = max(1, math.ceil(total * percentile / 100.0))
    cumulative = 0
    for lines, count in sorted(histogram.items()):
        cumulative += count
        if cumulative >= target:
            return float(lines)
    return ""


def actual_flits(
    histogram: dict[int, int], flit_bytes: int, overhead: float
) -> int:
    total = 0
    for lines, packets in histogram.items():
        if lines == 1:
            request_bytes, response_bytes = 12, 68
        else:
            request_bytes, response_bytes = 20, 4 + 64 * lines
        total += packets * (
            encoded_flits(request_bytes, flit_bytes, overhead)
            + encoded_flits(response_bytes, flit_bytes, overhead)
        )
    return total


def unique_config(rows: list[dict[str, str]], what: str, warnings: list[str]) -> float | str:
    values = sorted(set(metric_values(rows, what)))
    if not values:
        return ""
    if len(values) > 1:
        warnings.append(f"non-uniform {what}: {values}")
    return values[0]


def add_balance_warning(
    warnings: list[str], name: str, residual: float, enabled: bool
) -> None:
    if enabled and abs(residual) > 0.5:
        warnings.append(f"{name}={residual:g}")


def match_baseline(
    experiment: Path,
    input_path: Path,
    baseline_path: Path | None,
    baseline_files: list[Path],
    experiment_files: list[Path],
) -> Path | None:
    if baseline_path is None:
        return match_same_directory_ablation_baseline(
            experiment, experiment_files
        )
    if baseline_path.is_file():
        return baseline_path if len(baseline_files) == 1 else None
    if input_path.is_dir():
        candidate = baseline_path / experiment.relative_to(input_path)
        if candidate.is_file():
            return candidate
    same_name = [path for path in baseline_files if path.name == experiment.name]
    return same_name[0] if len(same_name) == 1 else None


def match_same_directory_ablation_baseline(
    experiment: Path, experiment_files: list[Path]
) -> Path | None:
    has_ablation = any(
        any(
            path.name.endswith(f"_{config}_metrics.csv")
            for config in REMOTE_ABLATION_ORDER
            if config != "baseline"
        )
        for path in experiment_files
    )
    if not has_ablation:
        return None

    for config in REMOTE_ABLATION_CONFIGS:
        suffix = f"_{config}_metrics.csv"
        if not experiment.name.endswith(suffix):
            continue
        prefix = experiment.name[: -len(suffix)]
        candidate = experiment.with_name(f"{prefix}_baseline_metrics.csv")
        if candidate in experiment_files:
            return candidate
    return None


def remote_ablation_identity(path: Path) -> tuple[str, str] | None:
    for config in REMOTE_ABLATION_CONFIGS:
        suffix = f"_{config}_metrics.csv"
        if path.name.endswith(suffix):
            return path.name[: -len(suffix)], config
    return None


def analyze_one(
    path: Path,
    baseline: Path | None,
    flit_bytes: int,
    overhead: float,
) -> tuple[dict[str, object], bool]:
    rows, warnings = read_metrics(path)
    remote_enabled_rows = [
        row for row in rows if row.get("what") == "remote_data_path_enabled"
    ]
    dram_enabled_rows = [
        row for row in rows if row.get("what") == "dram_batch_enabled"
    ]
    enabled_gpu_count = sum(
        1 for row in remote_enabled_rows if value(row) > 0.5
    )
    dram_enabled_slice_count = sum(
        1 for row in dram_enabled_rows if value(row) > 0.5
    )
    rdma_names = {
        row["where"]
        for row in remote_enabled_rows
        if RDMA_RE.match(row["where"])
    }
    l2_names = {
        row["where"]
        for row in rows
        if L2_RE.match(row["where"])
        and row.get("what") in {"remote_filter_queries", "dram_batch_enabled"}
    }
    if not remote_enabled_rows and not dram_enabled_rows:
        status = "legacy_metrics"
    elif enabled_gpu_count == 0 and dram_enabled_slice_count == 0:
        status = "disabled"
    else:
        status = "enabled"
    enabled = status == "enabled"
    remote_enabled = enabled_gpu_count > 0
    dram_enabled = dram_enabled_slice_count > 0

    result: dict[str, object] = {
        "run": path.stem.removesuffix("_metrics"),
        "metrics_file": str(path),
        "status": status,
        "rdma_gpu_count": len(rdma_names),
        "enabled_gpu_count": enabled_gpu_count,
        "dram_enabled_slice_count": dram_enabled_slice_count,
        "l2_slice_count": len(l2_names),
        "driver_total_time_s": component_metric(rows, "Driver", "total_time"),
        "max_cp_kernel_time_s": max_cp_kernel_time(rows),
        "max_wg_limit": component_metric(rows, "Driver", "max_wg_limit"),
        "max_wg_reached": component_metric(rows, "Driver", "max_wg_reached"),
    }
    if result["max_wg_reached"] != "" and float(result["max_wg_reached"]) > 0.5:
        warnings.append("run truncated at max-wg; kernel may be incomplete")
    identity = remote_ablation_identity(path)
    result["ablation_group"] = identity[0] if identity else ""
    result["ablation_config"] = identity[1] if identity else ""

    baseline_rows: list[dict[str, str]] = []
    if baseline is not None:
        baseline_rows, baseline_warnings = read_metrics(baseline)
        warnings.extend(f"baseline: {warning}" for warning in baseline_warnings)
        result["baseline_metrics_file"] = str(baseline)
        result["baseline_driver_total_time_s"] = component_metric(
            baseline_rows, "Driver", "total_time"
        )
        result["baseline_max_cp_kernel_time_s"] = max_cp_kernel_time(baseline_rows)
    else:
        result["baseline_metrics_file"] = ""
        result["baseline_driver_total_time_s"] = ""
        result["baseline_max_cp_kernel_time_s"] = ""
    result["driver_speedup"] = speedup(
        result["baseline_driver_total_time_s"], result["driver_total_time_s"]
    )
    result["kernel_speedup"] = speedup(
        result["baseline_max_cp_kernel_time_s"], result["max_cp_kernel_time_s"]
    )

    for metric in CONFIG_METRICS:
        result[metric] = unique_config(rows, metric, warnings)
    for metric in SUM_METRICS:
        result[metric] = metric_sum(rows, metric)
    for metric in MAX_METRICS:
        result[metric] = metric_max(rows, metric)

    histogram = batch_histogram(rows)
    dram_miss_lines = float(result["dram_batch_miss_lines"])
    dram_batch_lines = float(result["dram_batch_lines"])
    dram_multiline_reads = float(result["dram_batch_multiline_reads"])
    dram_singleline_reads = float(result["dram_batch_singleline_reads"])
    dram_actual_reads = dram_multiline_reads + dram_singleline_reads
    logical = float(result["remote_logical_reads"])
    if remote_enabled and logical == 0:
        warnings.append("no remote read opportunity")
    wire_lines = float(result["remote_wire_lines"])
    demand_wire_lines = float(result["remote_demand_wire_lines"])
    prefetch_wire_lines = float(result["remote_prefetch_wire_lines"])
    packets = float(result["remote_single_packets"]) + float(
        result["remote_bitmap_packets"]
    )
    request_bytes = float(result["remote_network_request_bytes"])
    response_bytes = float(result["remote_network_response_bytes"])
    actual_bytes = request_bytes + response_bytes
    naive_bytes = logical * 80.0
    naive_flits = int(logical) * (
        encoded_flits(12, flit_bytes, overhead)
        + encoded_flits(68, flit_bytes, overhead)
    )
    wire_flits = actual_flits(histogram, flit_bytes, overhead)

    result.update(
        {
            "dram_actual_reads": dram_actual_reads,
            "dram_batch_request_reduction_pct": percent(
                dram_miss_lines - dram_actual_reads, dram_miss_lines
            ),
            "dram_batch_multiline_coverage_pct": percent(
                2.0 * dram_multiline_reads, dram_miss_lines
            ),
            "dram_batch_avg_lines": safe_div(
                dram_batch_lines, float(result["dram_batches_drained"])
            ),
            "dram_batch_wait_avg_ns": safe_div(
                float(result["dram_batch_wait_total_ns"]),
                float(result["dram_batch_wait_samples"]),
            ),
            "wire_packets": packets,
            "naive_one_per_logical_bytes": naive_bytes,
            "actual_wire_bytes": actual_bytes,
            "wire_bytes_saved": naive_bytes - actual_bytes,
            "wire_bytes_saved_pct": percent_saved(naive_bytes, actual_bytes),
            "naive_one_per_logical_flits": naive_flits,
            "actual_wire_flits": wire_flits,
            "wire_flits_saved": naive_flits - wire_flits,
            "wire_flits_saved_pct": percent_saved(naive_flits, wire_flits),
            "packet_reduction_vs_logical_pct": percent(logical - packets, logical),
            "batching_only_packet_reduction_pct": percent(
                wire_lines - packets, wire_lines
            ),
            "dedup_pct": percent(
                float(result["remote_duplicate_reads"]), logical
            ),
            "demand_wire_avoidance_pct": percent(
                logical - demand_wire_lines, logical
            ),
            "prefetch_wire_line_pct": percent(prefetch_wire_lines, wire_lines),
            "avg_wire_lines_per_packet": safe_div(wire_lines, packets),
            "avg_bitmap_lines": safe_div(
                float(result["remote_bitmap_lines"]),
                float(result["remote_bitmap_packets"]),
            ),
            "batch_size_p50_lines": histogram_percentile(histogram, 50),
            "batch_size_p95_lines": histogram_percentile(histogram, 95),
            "l2_probe_hit_pct": percent(
                float(result["remote_l2_probe_hits"]),
                float(result["remote_l2_probe_hits"])
                + float(result["remote_l2_probe_misses"]),
            ),
            "l2_logical_response_pct": percent(
                float(result["remote_l2_logical_responses"]), logical
            ),
            "batch_queue_wait_avg_ns": safe_div(
                float(result["remote_batch_queue_wait_total_ns"]),
                float(result["remote_batch_queue_wait_samples"]),
            ),
            "pre_network_wait_avg_ns": safe_div(
                float(result["remote_pre_network_wait_total_ns"]),
                float(result["remote_pre_network_wait_samples"]),
            ),
            "probe_latency_avg_ns": safe_div(
                float(result["remote_probe_latency_total_ns"]),
                float(result["remote_probe_latency_samples"]),
            ),
            "logical_read_latency_avg_ns": safe_div(
                float(result["remote_logical_read_latency_total_ns"]),
                float(result["remote_logical_read_latency_samples"]),
            ),
            "filter_false_positive_pct": percent(
                float(result["remote_filter_false_positives"]),
                float(result["remote_filter_positives"]),
            ),
            "filter_negative_bypass_pct": percent(
                float(result["remote_filter_negatives"]),
                float(result["remote_filter_queries"]),
            ),
            "fill_install_pct": percent(
                float(result["remote_installed_fills"]),
                float(result["remote_clean_fills"]),
            ),
            "fill_drop_pct": percent(
                float(result["remote_dropped_fills"]),
                float(result["remote_clean_fills"]),
            ),
            "au_prefetch_converted_demand_pct": percent(
                float(result["remote_au_prefetch_converted_demand"]),
                float(result["remote_au_prefetch_candidates"]),
            ),
            "au_prefetch_demand_merges_per_wire_line": safe_div(
                float(result["remote_au_prefetch_demand_merges"]),
                prefetch_wire_lines,
            ),
            "two_touch_useful_fill_pct": percent(
                float(result["remote_useful_two_touch_fills"]),
                float(result["remote_l2_two_touch_installed_fills"]),
            ),
            "prefetch_useful_fill_pct": percent(
                float(result["remote_useful_prefetch_fills"]),
                float(result["remote_l2_prefetch_installed_fills"]),
            ),
            "replica_hits_per_installed_fill": safe_div(
                float(result["remote_replica_probe_hits"]),
                float(result["remote_installed_fills"]),
            ),
            "local_clean_displacements_per_fill": safe_div(
                float(result["remote_fill_displaced_local_clean"]),
                float(result["remote_installed_fills"]),
            ),
            "two_touch_local_displacement_pct": percent(
                float(result["remote_two_touch_local_displacements"]),
                float(result["remote_l2_two_touch_installed_fills"]),
            ),
            "prefetch_local_displacement_pct": percent(
                float(result["remote_prefetch_local_displacements"]),
                float(result["remote_l2_prefetch_installed_fills"]),
            ),
            "two_touch_unused_fills": float(
                result["remote_l2_two_touch_installed_fills"]
            )
            - float(result["remote_useful_two_touch_fills"]),
            "prefetch_unused_fills": float(
                result["remote_l2_prefetch_installed_fills"]
            )
            - float(result["remote_useful_prefetch_fills"]),
        }
    )

    add_balance_warning(
        warnings,
        "dram_batch_line_balance",
        dram_miss_lines - dram_batch_lines,
        dram_enabled,
    )
    add_balance_warning(
        warnings,
        "dram_batch_read_balance",
        float(result["dram_batches_drained"]) - dram_actual_reads,
        dram_enabled,
    )
    add_balance_warning(
        warnings,
        "dram_batch_read_line_balance",
        dram_batch_lines
        - 2.0 * dram_multiline_reads
        - dram_singleline_reads,
        dram_enabled,
    )
    add_balance_warning(
        warnings,
        "dram_batch_drain_reason_balance",
        float(result["dram_batches_drained"])
        - float(result["dram_batch_full_drains"])
        - float(result["dram_batch_timeout_drains"])
        - float(result["dram_batch_capacity_drains"])
        - float(result["dram_batch_drain_drains"]),
        dram_enabled,
    )
    add_balance_warning(
        warnings,
        "wire_line_balance",
        wire_lines
        - float(result["remote_single_packets"])
        - float(result["remote_bitmap_lines"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "wire_origin_balance",
        wire_lines - demand_wire_lines - prefetch_wire_lines,
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "histogram_packet_balance",
        sum(histogram.values()) - packets,
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "histogram_line_balance",
        sum(lines * count for lines, count in histogram.items()) - wire_lines,
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "merge_balance",
        float(result["remote_duplicate_reads"])
        - float(result["remote_pre_send_merges"])
        - float(result["remote_inflight_merges"])
        - float(result["remote_ready_merges"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "filter_query_balance",
        float(result["remote_filter_queries"])
        - float(result["remote_filter_positives"])
        - float(result["remote_filter_negatives"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "filter_positive_balance",
        float(result["remote_filter_positives"])
        - float(result["remote_filter_false_positives"])
        - float(result["remote_filter_true_positive_unavailable"])
        - float(result["remote_replica_probe_hits"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_balance",
        float(result["remote_clean_fills"])
        - float(result["remote_installed_fills"])
        - float(result["remote_dropped_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_class_balance",
        float(result["remote_installed_fills"])
        - float(result["remote_fill_into_invalid"])
        - float(result["remote_fill_replaced_remote"])
        - float(result["remote_fill_displaced_local_clean"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_attempt_origin_balance",
        float(result["remote_clean_fills"])
        - float(result["remote_l2_two_touch_fill_attempts"])
        - float(result["remote_l2_prefetch_fill_attempts"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_install_origin_balance",
        float(result["remote_installed_fills"])
        - float(result["remote_l2_two_touch_installed_fills"])
        - float(result["remote_l2_prefetch_installed_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_drop_origin_balance",
        float(result["remote_dropped_fills"])
        - float(result["remote_l2_two_touch_dropped_fills"])
        - float(result["remote_l2_prefetch_dropped_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "rdma_l2_fill_attempt_balance",
        float(result["remote_two_touch_fill_attempts"])
        + float(result["remote_prefetch_fill_attempts"])
        - float(result["remote_clean_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "rdma_l2_fill_install_balance",
        float(result["remote_two_touch_installed_fills"])
        + float(result["remote_prefetch_installed_fills"])
        - float(result["remote_installed_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fanout_balance",
        logical - float(result["remote_fanout_responses"]),
        remote_enabled,
    )
    expected_request_bytes = (
        12 * float(result["remote_single_packets"])
        + 20 * float(result["remote_bitmap_packets"])
    )
    expected_response_bytes = (
        68 * float(result["remote_single_packets"])
        + 4 * float(result["remote_bitmap_packets"])
        + 64 * float(result["remote_bitmap_lines"])
    )
    add_balance_warning(
        warnings,
        "request_byte_balance",
        request_bytes - expected_request_bytes,
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "response_byte_balance",
        response_bytes - expected_response_bytes,
        remote_enabled,
    )

    result["analysis_warnings"] = "; ".join(warnings)
    return result, bool(warnings and enabled)


def add_ablation_comparisons(
    results: list[dict[str, object]],
) -> None:
    indexed = {
        (str(result.get("ablation_group", "")),
         str(result.get("ablation_config", ""))): result
        for result in results
        if result.get("ablation_group") and result.get("ablation_config")
    }
    mechanisms = {
        "baseline": "Baseline",
        "baseline_dram_batch_only": "DRAM batching only",
        "baseline_remote_request_only": "Remote request only",
        "baseline_remote_l2_only": "Remote L2 only",
        "baseline_all_three": "All three combined",
        "baseline_all_three_prefetch": "All three + AU prefetch",
    }
    standalone_configs = (
        "baseline_dram_batch_only",
        "baseline_remote_request_only",
        "baseline_remote_l2_only",
    )
    for result in results:
        result["mechanism"] = mechanisms.get(
            str(result.get("ablation_config", "")), ""
        )
        result["runtime_reduction_vs_baseline_pct"] = ""
        result["standalone_runtime_reduction_pct"] = ""
        result["combined_runtime_reduction_pct"] = ""
        result["sum_standalone_runtime_reduction_pct"] = ""
        result["combined_interaction_pp"] = ""
        result["combined_vs_best_standalone_speedup"] = ""
        result["prefetch_incremental_speedup"] = ""
        config = str(result.get("ablation_config", ""))
        group = str(result.get("ablation_group", ""))
        if config not in REMOTE_ABLATION_ORDER:
            continue
        baseline = indexed.get((group, "baseline"))
        baseline_time = (
            baseline.get("driver_total_time_s", "")
            if baseline is not None
            else ""
        )
        current_time = result.get("driver_total_time_s", "")
        if baseline_time == "" or current_time == "" or float(baseline_time) == 0:
            continue
        reduction = percent(
            float(baseline_time) - float(current_time),
            float(baseline_time),
        )
        result["runtime_reduction_vs_baseline_pct"] = reduction
        if config in standalone_configs:
            result["standalone_runtime_reduction_pct"] = reduction

        if config == "baseline_all_three_prefetch":
            combined = indexed.get((group, "baseline_all_three"))
            if combined is not None:
                result["prefetch_incremental_speedup"] = speedup(
                    combined.get("driver_total_time_s", ""), current_time
                )

        if config != "baseline_all_three":
            continue
        standalone = [indexed.get((group, name)) for name in standalone_configs]
        if any(entry is None for entry in standalone):
            continue
        standalone_times = [
            entry.get("driver_total_time_s", "") for entry in standalone
        ]
        if any(value == "" for value in standalone_times):
            continue
        standalone_reductions = [
            100.0 * (float(baseline_time) - float(value)) / float(baseline_time)
            for value in standalone_times
        ]
        sum_standalone = sum(standalone_reductions)
        result["combined_runtime_reduction_pct"] = reduction
        result["sum_standalone_runtime_reduction_pct"] = sum_standalone
        result["combined_interaction_pp"] = float(reduction) - sum_standalone
        result["combined_vs_best_standalone_speedup"] = speedup(
            min(float(value) for value in standalone_times), current_time
        )


def sort_ablation_results(results: list[dict[str, object]]) -> None:
    def key(result: dict[str, object]) -> tuple[str, int, str]:
        config = str(result.get("ablation_config", ""))
        run = str(result.get("run", ""))
        group = str(result.get("ablation_group", "")) or run
        stage = (
            REMOTE_ABLATION_ORDER.index(config)
            if config in REMOTE_ABLATION_ORDER
            else len(REMOTE_ABLATION_ORDER)
        )
        return group, stage, run

    results.sort(key=key)


def write_csv(path: Path, results: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fields: list[str] = []
    for result in results:
        for field in result:
            if field not in fields:
                fields.append(field)
    with path.open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(results)


def format_value(value: object, suffix: str = "") -> str:
    if value == "" or value is None:
        return "n/a"
    return f"{float(value):.4g}{suffix}"


def write_markdown(path: Path, results: list[dict[str, object]]) -> None:
    lines = [
        "# Three-mechanism data-path analysis",
        "",
        "Each `only` row enables exactly one mechanism over the same baseline. "
        "Combined interaction is combined runtime reduction minus the sum of "
        "the three standalone reductions; negative means overlap. Local-clean "
        "displacement is only a pressure proxy, not proof of a later miss.",
        "",
        "| Run | Mechanism | Baseline speedup | Runtime reduction | "
        "Combined interaction | DRAM read reduction | Remote packet reduction | "
        "Wire-byte saving | Two-touch useful | Local displacement/fill | Warnings |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|",
    ]
    for result in results:
        warnings = str(result.get("analysis_warnings", "")).replace("|", "/")
        lines.append(
            "| {run} | {mechanism} | {speedup} | {reduction} | "
            "{interaction} | {dram} | {packets} | {bytes_} | "
            "{two_touch} | {displacement} | {warnings} |".format(
                run=result["run"],
                mechanism=result.get("mechanism") or result["status"],
                speedup=format_value(result.get("driver_speedup"), "x"),
                reduction=format_value(
                    result.get("runtime_reduction_vs_baseline_pct"), "%"
                ),
                interaction=format_value(
                    result.get("combined_interaction_pp"), " pp"
                ),
                dram=format_value(
                    result.get("dram_batch_request_reduction_pct"), "%"
                ),
                packets=format_value(
                    result.get("packet_reduction_vs_logical_pct"), "%"
                ),
                bytes_=format_value(result.get("wire_bytes_saved_pct"), "%"),
                two_touch=format_value(
                    result.get("two_touch_useful_fill_pct"), "%"
                ),
                displacement=format_value(
                    result.get("local_clean_displacements_per_fill")
                ),
                warnings=warnings or "none",
            )
        )
    path.write_text("\n".join(lines) + "\n")


def main() -> int:
    args = parse_args()
    if args.flit_bytes <= 0:
        raise ValueError("--flit-bytes must be positive")
    if args.encoding_overhead < 0:
        raise ValueError("--encoding-overhead must be non-negative")

    inputs = discover_metrics(args.input)
    if not inputs:
        raise FileNotFoundError(f"no *_metrics.csv files under {args.input}")
    baselines = discover_metrics(args.baseline_dir) if args.baseline_dir else []
    output = args.output
    if output is None:
        root = args.input if args.input.is_dir() else args.input.parent
        output = root / "remote_data_path_analysis.csv"

    results: list[dict[str, object]] = []
    strict_failure = False
    for experiment in inputs:
        baseline = match_baseline(
            experiment, args.input, args.baseline_dir, baselines, inputs
        )
        result, has_warning = analyze_one(
            experiment, baseline, args.flit_bytes, args.encoding_overhead
        )
        if args.baseline_dir and baseline is None:
            result["analysis_warnings"] = (
                str(result["analysis_warnings"])
                + ("; " if result["analysis_warnings"] else "")
                + "baseline file not matched"
            )
            has_warning = result["status"] == "enabled"
        results.append(result)
        strict_failure = strict_failure or has_warning

    sort_ablation_results(results)
    add_ablation_comparisons(results)
    write_csv(output, results)
    markdown = output.with_suffix(".md")
    write_markdown(markdown, results)
    print(f"wrote {output}")
    print(f"wrote {markdown}")
    return 2 if args.strict and strict_failure else 0


if __name__ == "__main__":
    raise SystemExit(main())
