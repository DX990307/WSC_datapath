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

from plot_complete_ablation import (
    binary_manifest_hash,
    validate_equal_sampled_work,
)


RDMA_RE = re.compile(r"^GPU\[(\d+)]\.RDMA$")
DRAM_RE = re.compile(r"^GPU\[(\d+)]\.DRAM\[(\d+)]$")
L2_RE = re.compile(r"^GPU\[(\d+)]\.L2\[(\d+)]$")
L1V_RE = re.compile(r"^GPU\[(\d+)]\.SA\[(\d+)]\.L1VCache\[(\d+)]$")
BATCH_RE = re.compile(r"^remote_batch_size_(\d+)_packets$")
L1V_DEMAND_OUTCOMES = frozenset({
    "read-hit",
    "read-miss",
    "read-mshr-hit",
    "write-hit",
    "write-miss",
    "write-mshr-hit",
})
COMPONENT_LATENCY_METRICS = (
    "l1v_req_avg_latency_ns",
    "l2_req_avg_latency_ns",
    "dram_read_avg_latency_ns",
    "dram_write_avg_latency_ns",
    "rdma_generic_req_avg_latency_ns",
)
MAX_WG_WORK_DELTA_WARNING_PCT = 1.0
REMOTE_ABLATION_ORDER = (
    "baseline",
    "baseline_local_optimization_only",
    "baseline_remote_request_only",
    "baseline_remote_l2_only",
    "baseline_all_three",
)
REMOTE_ABLATION_CONFIGS = tuple(
    sorted(REMOTE_ABLATION_ORDER, key=len, reverse=True)
)
NON_FATAL_WARNING_PREFIXES = (
    "bounded max-wg window;",
    "no remote read opportunity",
    "sampled detailed instruction-count mismatch;",
)
RDMA_SUM_METRICS = (
    "rdma_pipeline_wait_cycles",
    "rdma_outstanding_full_stalls",
    "rdma_requester_outstanding_full_stalls",
    "rdma_owner_outstanding_full_stalls",
)
RDMA_MAX_METRICS = (
    "rdma_peak_outstanding",
    "rdma_requester_peak_outstanding",
    "rdma_owner_peak_outstanding",
)
RDMA_CONFIG_METRICS = (
    "rdma_pipeline_width",
    "rdma_pipeline_latency_cycles",
    "rdma_max_outstanding",
)

SUM_METRICS = [
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
    "dram_row_commands_issued",
    "dram_row_column_commands",
    "dram_row_reuse_hits",
    "dram_row_auto_precharge_stops",
    "dram_row_activate_commands",
    "dram_row_precharge_commands",
    *RDMA_SUM_METRICS,
    "l2_resident_filter_queries",
    "l2_resident_filter_positives",
    "l2_resident_filter_negatives",
    "l2_resident_filter_read_bypasses",
    "l2_resident_filter_read_negative_mshr_merges",
    "l2_resident_filter_write_bypasses",
    "l2_resident_filter_write_full_line_bypasses",
    "l2_resident_filter_write_partial_bypasses",
    "l2_resident_filter_false_positives",
    "l2_resident_filter_insert_failures",
    "dram_batch_miss_lines",
    "dram_batches_created",
    "dram_batches_drained",
    "dram_batch_lines",
    "dram_batch_multiline_reads",
    "dram_batch_singleline_reads",
    "dram_adapter_observations",
    "dram_adapter_useful",
    "dram_adapter_predictions",
    "dram_adapter_inflight_hits",
    "dram_adapter_buffer_hits",
    "dram_adapter_buffer_bank_stalls",
    "dram_adapter_redundant_predictions_avoided",
    "dram_adapter_unused",
    "dram_adapter_unused_predictions",
    "dram_adapter_pending_predictions",
    "remote_logical_reads",
    "rdma_observed_remote_reads",
    "rdma_observed_remote_writes",
    "remote_line_entry_full_stalls",
    "remote_waiter_entry_full_stalls",
    "remote_owner_child_line_full_stalls",
    "remote_wire_lines",
    "remote_demand_wire_lines",
    "remote_duplicate_reads",
	"remote_inflight_filter_queries",
	"remote_inflight_filter_positives",
	"remote_inflight_filter_negatives",
	"remote_inflight_filter_false_positives",
	"remote_inflight_filter_insert_failures",
	"remote_exact_table_lookups",
	"remote_exact_table_lookups_avoided",
    "remote_pre_send_merges",
    "remote_inflight_merges",
    "remote_ready_merges",
    "remote_l2_probe_hits",
    "remote_l2_probe_misses",
    "remote_l2_one_touch_probe_bypasses",
    "remote_reuse_first_transactions",
    "remote_reuse_second_transactions",
    "remote_reuse_third_plus_transactions",
    "remote_reuse_page_filter_queries",
    "remote_reuse_page_filter_positives",
    "remote_reuse_page_filter_false_positives",
    "remote_reuse_page_shadow_evictions",
    "remote_reuse_proven_pages",
    "remote_reuse_page_insert_failures",
    "remote_reuse_page_early_admissions",
    "remote_reuse_write_uncacheable_skips",
    "remote_l2_logical_responses",
    "remote_single_packets",
    "remote_bitmap_packets",
    "remote_bitmap_lines",
    "remote_bitmap_response_packets",
    "remote_bitmap_response_lines",
    "remote_early_bitmap_responses",
    "remote_two_touch_candidates",
    "remote_two_touch_fill_attempts",
    "remote_two_touch_installed_fills",
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
    "remote_installed_fills",
    "remote_l2_two_touch_installed_fills",
    "remote_dropped_fills",
    "remote_l2_two_touch_dropped_fills",
    "remote_filter_insert_failures",
    "remote_tracked_evictions",
    "remote_unused_two_touch_retirements",
    "remote_replica_probe_hits",
    "remote_two_touch_replica_hits",
    "remote_useful_two_touch_fills",
    "remote_fill_into_invalid",
    "remote_fill_replaced_remote",
    "remote_fill_displaced_local_clean",
    "remote_two_touch_local_displacements",
	"remote_local_clean_protection_drops",
    "remote_current_replicas",
    "remote_peak_replicas",
    "remote_page_early_fill_attempts",
    "remote_page_early_installed_fills",
    "remote_page_early_dropped_fills",
    "remote_page_early_replica_hits",
    "remote_page_early_useful_fills",
    "remote_page_early_unused_retirements",
]

MAX_METRICS = [
    "dram_adapter_confidence",
    "dram_row_max_queue_age_cycles",
    *RDMA_MAX_METRICS,
    "remote_batch_queue_wait_max_ns",
    "remote_pre_network_wait_max_ns",
    "remote_probe_latency_max_ns",
    "remote_logical_read_latency_max_ns",
    "remote_peak_line_entries",
    "remote_peak_waiter_entries",
    "remote_owner_peak_child_lines",
    "remote_reuse_page_shadow_peak_entries",
]

CONFIG_METRICS = [
    "dram_physical_access_bytes",
    "dram_row_continuation_enabled",
    *RDMA_CONFIG_METRICS,
    "l2_resident_filter_enabled",
    "l2_resident_filter_reliable",
    "dram_batch_enabled",
    "dram_adapter_prediction_threshold",
    "remote_dedup_enabled",
    "remote_batching_enabled",
    "remote_requester_l2_enabled",
    "remote_page_adaptive_enabled",
    "remote_config_batch_lines",
    "remote_config_max_batches",
    "remote_config_reuse_entries",
    "remote_config_line_entries",
    "remote_config_waiter_entries",
    "remote_config_owner_child_lines",
    "remote_reuse_page_shadow_capacity",
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Analyze batching cost, wire savings, and L2 replica reuse from "
            "simulator metrics CSV files."
        )
    )
    parser.add_argument("input", type=Path, help="metrics CSV or result directory")
    parser.add_argument(
        "--baseline-dir",
        dest="baseline_dirs",
        type=Path,
        action="append",
        help=(
            "optional remote-off CSV/directory; repeat the option to search "
            "multiple result directories in priority order"
        ),
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
    parser.add_argument(
        "--allow-sampled-instruction-mismatch",
        action="store_true",
        help=(
            "retain paired speedup when matching frozen sampled runs have "
            "different detailed cu_inst_count but identical, fully drained "
            "per-GPU WG/WF signatures"
        ),
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


def l1v_demand_requests(rows: Iterable[dict[str, str]]) -> float:
    """Count timing-sensitive transactions presented to L1 vector caches.

    A downstream latency change can alter CU-side coalescing and therefore
    this count even when admitted work and retired instructions are identical.
    Report it as a datapath outcome, not as an equal-work invariant.
    """
    return sum(
        value(row)
        for row in rows
        if L1V_RE.match(row.get("where", ""))
        and row.get("what") in L1V_DEMAND_OUTCOMES
    )


def cu_inst_count(rows: Iterable[dict[str, str]]) -> float:
    """Return the total retired CU instruction count for paired-work checks."""
    return metric_sum(rows, "cu_inst_count")


def cache_pressure_metrics(
    rows: Iterable[dict[str, str]],
) -> dict[str, float | str]:
    """Aggregate demand outcomes separately for L1V and L2 caches."""
    materialized = list(rows)
    result: dict[str, float | str] = {}
    for level, pattern in (("l1v", L1V_RE), ("l2", L2_RE)):
        counts = {}
        for operation in ("read", "write"):
            for outcome in ("hit", "miss", "mshr-hit"):
                metric = f"{operation}-{outcome}"
                count = sum(
                    value(row)
                    for row in materialized
                    if pattern.match(row.get("where", ""))
                    and row.get("what") == metric
                )
                key = f"{level}_{operation}_{outcome.replace('-', '_')}"
                counts[key] = count
                result[key] = count
            total = sum(
                counts[f"{level}_{operation}_{name}"]
                for name in ("hit", "miss", "mshr_hit")
            )
            result[f"{level}_{operation}_total"] = total
            for outcome in ("hit", "miss", "mshr_hit"):
                count = counts[f"{level}_{operation}_{outcome}"]
                result[f"{level}_{operation}_{outcome}_pct"] = (
                    100.0 * count / total if total else ""
                )
    return result


def weighted_component_latency(
    rows: Iterable[dict[str, str]],
    pattern: re.Pattern[str],
    average_metric: str,
    count_metrics: Iterable[str],
) -> float | str:
    """Combine per-component latency averages using transaction counts."""
    materialized = list(rows)
    averages = {
        row.get("where", ""): value(row)
        for row in materialized
        if pattern.match(row.get("where", ""))
        and row.get("what") == average_metric
    }
    count_names = frozenset(count_metrics)
    counts: dict[str, float] = defaultdict(float)
    for row in materialized:
        where = row.get("where", "")
        if pattern.match(where) and row.get("what") in count_names:
            counts[where] += value(row)
    total = sum(counts[where] for where in averages)
    if total == 0:
        return ""
    seconds = sum(
        average * counts[where] for where, average in averages.items()
    ) / total
    return seconds * 1e9


def component_latency_metrics(
    rows: Iterable[dict[str, str]],
) -> dict[str, float | str]:
    """Return request-count-weighted component execution latencies."""
    materialized = list(rows)
    return {
        "l1v_req_avg_latency_ns": weighted_component_latency(
            materialized,
            L1V_RE,
            "req_average_latency",
            L1V_DEMAND_OUTCOMES,
        ),
        "l2_req_avg_latency_ns": weighted_component_latency(
            materialized,
            L2_RE,
            "req_average_latency",
            L1V_DEMAND_OUTCOMES,
        ),
        "dram_read_avg_latency_ns": weighted_component_latency(
            materialized, DRAM_RE, "read_avg_latency", ("read_trans_count",)
        ),
        "dram_write_avg_latency_ns": weighted_component_latency(
            materialized,
            DRAM_RE,
            "write_avg_latency",
            ("write_trans_count",),
        ),
        # This is generic RDMA/routing-component latency, not remote-data-only
        # latency. Remote-data latency is reported by the explicit M2 tracer.
        "rdma_generic_req_avg_latency_ns": weighted_component_latency(
            materialized,
            RDMA_RE,
            "req_average_latency",
            ("incoming_trans_count", "outgoing_trans_count"),
        ),
    }


def dram_row_metrics(metrics: dict[str, object]) -> dict[str, float | str]:
    """Derive row-scheduler efficiency from globally aggregated counters.

    A reuse hit is a column command that did not need an ACTIVATE for its
    backing transaction. Row continuation preserves command order and has no
    timeout or age threshold.
    """
    issued = float(metrics["dram_row_commands_issued"])
    columns = float(metrics["dram_row_column_commands"])
    activates = float(metrics["dram_row_activate_commands"])
    precharges = float(metrics["dram_row_precharge_commands"])
    return {
        "dram_row_reuse_pct": percent(
            float(metrics["dram_row_reuse_hits"]), columns
        ),
        "dram_row_auto_precharge_stop_pct": percent(
            float(metrics["dram_row_auto_precharge_stops"]), columns
        ),
        "dram_row_activates_per_column": safe_div(activates, columns),
        "dram_row_precharges_per_column": safe_div(precharges, columns),
        "dram_row_management_command_pct": percent(
            activates + precharges, issued
        ),
        "dram_row_commands_per_column": safe_div(issued, columns),
    }


def rdma_pipeline_metrics(metrics: dict[str, object]) -> dict[str, float | str]:
    """Summarize generic RDMA-engine capacity pressure.

    These counters include traffic handled by the RDMA/routing component and
    must not be labeled as remote-data request counts. Peaks are per-engine
    maxima; stall and wait counters are summed across engines.
    """
    configured = metrics["rdma_max_outstanding"]
    if configured == "":
        return {
            "rdma_peak_outstanding_utilization_pct": "",
            "rdma_requester_peak_outstanding_utilization_pct": "",
            "rdma_owner_peak_outstanding_utilization_pct": "",
            "rdma_requester_full_stall_share_pct": "",
            "rdma_owner_full_stall_share_pct": "",
        }
    capacity = float(configured)
    stalls = float(metrics["rdma_outstanding_full_stalls"])
    return {
        "rdma_peak_outstanding_utilization_pct": percent(
            float(metrics["rdma_peak_outstanding"]), capacity
        ),
        "rdma_requester_peak_outstanding_utilization_pct": percent(
            float(metrics["rdma_requester_peak_outstanding"]), capacity
        ),
        "rdma_owner_peak_outstanding_utilization_pct": percent(
            float(metrics["rdma_owner_peak_outstanding"]), capacity
        ),
        "rdma_requester_full_stall_share_pct": percent(
            float(metrics["rdma_requester_outstanding_full_stalls"]),
            stalls,
        ),
        "rdma_owner_full_stall_share_pct": percent(
            float(metrics["rdma_owner_outstanding_full_stalls"]),
            stalls,
        ),
    }


def dram_adapter_prediction_accounting(
    metrics: dict[str, object], available: bool
) -> float | str:
    """Return zero when every issued sibling prediction has one outcome.

    Older metrics files do not contain the exact unused/pending counters. An
    empty value keeps those runs readable without claiming conservation from
    their mixed legacy counter.
    """
    if not available:
        return ""
    return (
        float(metrics["dram_adapter_predictions"])
        - float(metrics["dram_adapter_inflight_hits"])
        - float(metrics["dram_adapter_buffer_hits"])
        - float(metrics["dram_adapter_unused_predictions"])
        - float(metrics["dram_adapter_pending_predictions"])
    )


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


def has_fatal_warning(warnings: Iterable[str]) -> bool:
    """Return whether strict analysis should reject the run.

    A deliberately bounded, fully drained and equal-work window is valid for
    paired comparison.  Likewise, a workload with no remote reads is a valid
    no-op observation for M2/M3.  Keep both visible in the report without
    turning them into strict-analysis failures.
    """
    return any(
        not warning.startswith(NON_FATAL_WARNING_PREFIXES)
        for warning in warnings
    )


def match_baseline(
    experiment: Path,
    input_path: Path,
    baseline_paths: list[Path] | None,
    baseline_files: list[Path],
    experiment_files: list[Path],
) -> Path | None:
    if not baseline_paths:
        return match_same_directory_ablation_baseline(
            experiment, experiment_files
        )
    baseline_file_paths = [path for path in baseline_paths if path.is_file()]
    if baseline_file_paths:
        return baseline_file_paths[0] if len(baseline_file_paths) == 1 else None

    # ``--remote-ablation`` intentionally does not rerun the baseline.  Its
    # output names therefore have an ablation suffix (for example,
    # ``foo_baseline_all_three_metrics.csv``), while the reusable reference is
    # named ``foo_baseline_metrics.csv``.  Match that reference explicitly
    # before trying the legacy exact-name rules below.
    identity = remote_ablation_identity(experiment)
    if identity is not None:
        prefix, _ = identity
        baseline_name = f"{prefix}_baseline_metrics.csv"
        if input_path.is_dir():
            relative_parent = experiment.relative_to(input_path).parent
            for baseline_path in baseline_paths:
                if not baseline_path.is_dir():
                    continue
                candidate = baseline_path / relative_parent / baseline_name
                if candidate.is_file():
                    return candidate
        named_baselines = [
            path for path in baseline_files if path.name == baseline_name
        ]
        if len(named_baselines) == 1:
            return named_baselines[0]

    if input_path.is_dir():
        for baseline_path in baseline_paths:
            if not baseline_path.is_dir():
                continue
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
    allow_sampled_instruction_mismatch: bool = False,
) -> tuple[dict[str, object], bool]:
    rows, warnings = read_metrics(path)
    metric_names = {row.get("what") for row in rows}
    adapter_accounting_available = {
        "dram_adapter_unused_predictions",
        "dram_adapter_pending_predictions",
    }.issubset(metric_names)
    page_shadow_metrics_available = {
        "remote_reuse_page_shadow_capacity",
        "remote_reuse_page_shadow_peak_entries",
        "remote_reuse_page_shadow_evictions",
    }.issubset(metric_names)
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
        "cu_inst_count": cu_inst_count(rows),
        "l1v_demand_requests": l1v_demand_requests(rows),
        "max_wg_limit": component_metric(rows, "Driver", "max_wg_limit"),
        "max_wg_reached": component_metric(rows, "Driver", "max_wg_reached"),
        "max_wg_stop_completed": component_metric(
            rows, "Driver", "max_wg_stop_completed"
        ),
        "max_wg_launch_limited": component_metric(
            rows, "Driver", "max_wg_launch_limited"
        ),
        "max_wg_kernel_drained": component_metric(
            rows, "Driver", "max_wg_kernel_drained"
        ),
        "max_wg_admitted": component_metric(
            rows, "Driver", "max_wg_admitted"
        ),
        "work_validation_mode": "unpaired",
        "sampled_work_signature_entries": "",
        "sampled_work_total_wgs": "",
        "sampled_work_total_wfs": "",
    }
    pressure = cache_pressure_metrics(rows)
    result.update(pressure)
    result.update(component_latency_metrics(rows))
    if result["max_wg_reached"] != "" and float(result["max_wg_reached"]) > 0.5:
        warnings.append("bounded max-wg window; application phase may be incomplete")
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
        result["baseline_cu_inst_count"] = cu_inst_count(baseline_rows)
        result["baseline_l1v_demand_requests"] = l1v_demand_requests(
            baseline_rows
        )
        for key, metric_value in cache_pressure_metrics(baseline_rows).items():
            result[f"baseline_{key}"] = metric_value
        for key, metric_value in component_latency_metrics(
            baseline_rows
        ).items():
            result[f"baseline_{key}"] = metric_value
        result["baseline_max_wg_reached"] = component_metric(
            baseline_rows, "Driver", "max_wg_reached"
        )
        result["baseline_max_wg_stop_completed"] = component_metric(
            baseline_rows, "Driver", "max_wg_stop_completed"
        )
        result["baseline_max_wg_launch_limited"] = component_metric(
            baseline_rows, "Driver", "max_wg_launch_limited"
        )
        result["baseline_max_wg_kernel_drained"] = component_metric(
            baseline_rows, "Driver", "max_wg_kernel_drained"
        )
        result["baseline_max_wg_admitted"] = component_metric(
            baseline_rows, "Driver", "max_wg_admitted"
        )
    else:
        result["baseline_metrics_file"] = ""
        result["baseline_driver_total_time_s"] = ""
        result["baseline_max_cp_kernel_time_s"] = ""
        result["baseline_cu_inst_count"] = ""
        result["baseline_l1v_demand_requests"] = ""
        for key in pressure:
            result[f"baseline_{key}"] = ""
        for key in COMPONENT_LATENCY_METRICS:
            result[f"baseline_{key}"] = ""
        result["baseline_max_wg_reached"] = ""
        result["baseline_max_wg_stop_completed"] = ""
        result["baseline_max_wg_launch_limited"] = ""
        result["baseline_max_wg_kernel_drained"] = ""
        result["baseline_max_wg_admitted"] = ""
    result["driver_speedup"] = speedup(
        result["baseline_driver_total_time_s"], result["driver_total_time_s"]
    )
    result["kernel_speedup"] = speedup(
        result["baseline_max_cp_kernel_time_s"], result["max_cp_kernel_time_s"]
    )
    for metric in COMPONENT_LATENCY_METRICS:
        baseline_latency = result[f"baseline_{metric}"]
        current_latency = result[metric]
        result[f"{metric.removesuffix('_ns')}_reduction_pct"] = (
            percent_saved(float(baseline_latency), float(current_latency))
            if baseline_latency != "" and current_latency != ""
            else ""
        )
    baseline_l1v_demand = result["baseline_l1v_demand_requests"]
    result["l1v_demand_request_delta_pct"] = (
        percent(
            float(result["l1v_demand_requests"]) - float(baseline_l1v_demand),
            float(baseline_l1v_demand),
        )
        if baseline_l1v_demand != ""
        else ""
    )
    paired_max_wg_truncation = (
        result["max_wg_reached"] != ""
        and float(result["max_wg_reached"]) > 0.5
        and result["baseline_max_wg_reached"] != ""
        and float(result["baseline_max_wg_reached"]) > 0.5
    )
    current_completed_stop = (
        result["max_wg_stop_completed"] != ""
        and float(result["max_wg_stop_completed"]) > 0.5
    )
    baseline_completed_stop = (
        result["baseline_max_wg_stop_completed"] != ""
        and float(result["baseline_max_wg_stop_completed"]) > 0.5
    )
    current_launch_limited = (
        result["max_wg_launch_limited"] != ""
        and float(result["max_wg_launch_limited"]) > 0.5
    )
    baseline_launch_limited = (
        result["baseline_max_wg_launch_limited"] != ""
        and float(result["baseline_max_wg_launch_limited"]) > 0.5
    )
    current_kernel_drained = (
        result["max_wg_kernel_drained"] != ""
        and float(result["max_wg_kernel_drained"]) > 0.5
    )
    baseline_kernel_drained = (
        result["baseline_max_wg_kernel_drained"] != ""
        and float(result["baseline_max_wg_kernel_drained"]) > 0.5
    )
    work_delta = result["l1v_demand_request_delta_pct"]
    if paired_max_wg_truncation and (
        current_completed_stop != baseline_completed_stop
    ):
        warnings.append("max-wg stop semantics mismatch")
    valid_drained_pair = (
        current_completed_stop
        and baseline_completed_stop
        and current_launch_limited
        and baseline_launch_limited
        and current_kernel_drained
        and baseline_kernel_drained
    )
    if paired_max_wg_truncation and not valid_drained_pair:
        warnings.append(
            "non-drained max-wg comparison: speedup is invalid"
        )
        result["driver_speedup"] = ""
        result["kernel_speedup"] = ""
    admitted = result["max_wg_admitted"]
    baseline_admitted = result["baseline_max_wg_admitted"]
    if (
        paired_max_wg_truncation
        and valid_drained_pair
        and admitted != ""
        and baseline_admitted != ""
        and float(admitted) != float(baseline_admitted)
    ):
        warnings.append("max-wg admitted-work mismatch: speedup is invalid")
        result["driver_speedup"] = ""
        result["kernel_speedup"] = ""
    baseline_inst = result["baseline_cu_inst_count"]
    if (
        baseline_inst != ""
        and float(result["cu_inst_count"]) != float(baseline_inst)
    ):
        sampled_work = None
        if allow_sampled_instruction_mismatch and baseline is not None:
            try:
                sampled_work = validate_equal_sampled_work(
                    baseline,
                    path,
                    {"max_wg_admitted": result["baseline_max_wg_admitted"]},
                    {"max_wg_admitted": result["max_wg_admitted"]},
                )
            except ValueError as error:
                warnings.append(
                    "CU instruction-count mismatch: speedup is invalid "
                    f"({error})"
                )
        if sampled_work is None:
            if not any(
                warning.startswith("CU instruction-count mismatch")
                for warning in warnings
            ):
                warnings.append(
                    "CU instruction-count mismatch: speedup is invalid"
                )
            result["driver_speedup"] = ""
            result["kernel_speedup"] = ""
        else:
            result["work_validation_mode"] = (
                "sampled_complete_per_gpu_wg_wf_signature"
            )
            result["sampled_work_signature_entries"] = sampled_work["entries"]
            result["sampled_work_total_wgs"] = sampled_work["total_wgs"]
            result["sampled_work_total_wfs"] = sampled_work["total_wfs"]
            warnings.append(
                "sampled detailed instruction-count mismatch; complete "
                "per-GPU WG/WF signatures match"
            )
    elif baseline_inst != "":
        result["work_validation_mode"] = "drained_wg_and_cu_inst_count"
    if (
        paired_max_wg_truncation
        and work_delta != ""
        and abs(float(work_delta)) > MAX_WG_WORK_DELTA_WARNING_PCT
    ):
        warnings.append(
            "max-wg L1V transaction delta (timing-sensitive): "
            f"{float(work_delta):.4g}%"
        )

    for metric in CONFIG_METRICS:
        result[metric] = unique_config(rows, metric, warnings)
    for metric in SUM_METRICS:
        result[metric] = metric_sum(rows, metric)
    for metric in MAX_METRICS:
        result[metric] = metric_max(rows, metric)
    if page_shadow_metrics_available:
        shadow_capacity = float(result["remote_reuse_page_shadow_capacity"])
        shadow_peak = float(result["remote_reuse_page_shadow_peak_entries"])
        result["remote_reuse_page_shadow_peak_utilization_pct"] = percent(
            shadow_peak, shadow_capacity
        )
        if shadow_peak > shadow_capacity:
            warnings.append(
                "remote_reuse_page_shadow_capacity_overflow="
                f"{shadow_peak:g}/{shadow_capacity:g}"
            )
        page_adaptive = result["remote_page_adaptive_enabled"]
        if (
            page_adaptive != ""
            and float(page_adaptive) > 0.5
            and shadow_capacity <= 0
        ):
            warnings.append("remote_reuse_page_shadow_has_zero_capacity")
    else:
        result["remote_reuse_page_shadow_peak_utilization_pct"] = ""
    result.update(dram_row_metrics(result))
    result.update(rdma_pipeline_metrics(result))

    baseline_rdma: dict[str, object] = {}
    for metric in RDMA_CONFIG_METRICS:
        baseline_rdma[metric] = unique_config(
            baseline_rows, metric, warnings
        )
    for metric in RDMA_SUM_METRICS:
        baseline_rdma[metric] = metric_sum(baseline_rows, metric)
    for metric in RDMA_MAX_METRICS:
        baseline_rdma[metric] = metric_max(baseline_rows, metric)
    baseline_rdma.update(rdma_pipeline_metrics(baseline_rdma))
    for metric, metric_value in baseline_rdma.items():
        result[f"baseline_{metric}"] = metric_value
    result["rdma_pipeline_wait_cycle_reduction_pct"] = percent_saved(
        float(baseline_rdma["rdma_pipeline_wait_cycles"]),
        float(result["rdma_pipeline_wait_cycles"]),
    )
    result["rdma_outstanding_full_stall_reduction_pct"] = percent_saved(
        float(baseline_rdma["rdma_outstanding_full_stalls"]),
        float(result["rdma_outstanding_full_stalls"]),
    )

    baseline_physical_reads = metric_sum(
        baseline_rows, "dram_physical_read_accesses"
    )
    baseline_physical_access_bytes = unique_config(
        baseline_rows, "dram_physical_access_bytes", warnings
    )
    result["baseline_dram_physical_read_accesses"] = baseline_physical_reads
    result["baseline_dram_physical_access_bytes"] = (
        baseline_physical_access_bytes
    )
    result["dram_physical_read_reduction_pct"] = percent_saved(
        baseline_physical_reads,
        float(result["dram_physical_read_accesses"]),
    )
    current_access_bytes = result["dram_physical_access_bytes"]
    current_physical_read_bytes = (
        float(result["dram_physical_read_accesses"])
        * float(current_access_bytes)
        if current_access_bytes != ""
        else ""
    )
    baseline_physical_read_bytes = (
        baseline_physical_reads * float(baseline_physical_access_bytes)
        if baseline_physical_access_bytes != ""
        else ""
    )
    result["dram_physical_read_bytes"] = current_physical_read_bytes
    result["baseline_dram_physical_read_bytes"] = (
        baseline_physical_read_bytes
    )
    result["dram_physical_read_byte_reduction_pct"] = (
        percent_saved(
            float(baseline_physical_read_bytes),
            float(current_physical_read_bytes),
        )
        if baseline_physical_read_bytes != ""
        and current_physical_read_bytes != ""
        else ""
    )

    histogram = batch_histogram(rows)
    dram_miss_lines = float(result["dram_batch_miss_lines"])
    dram_batch_lines = float(result["dram_batch_lines"])
    dram_multiline_reads = float(result["dram_batch_multiline_reads"])
    dram_singleline_reads = float(result["dram_batch_singleline_reads"])
    dram_actual_reads = dram_multiline_reads + dram_singleline_reads
    adapter_predictions = float(result["dram_adapter_predictions"])
    adapter_prefetch_hits = (
        float(result["dram_adapter_inflight_hits"])
        + float(result["dram_adapter_buffer_hits"])
    )
    adapter_redundant_avoided = float(
        result["dram_adapter_redundant_predictions_avoided"]
    )
    adapter_unused_predictions = float(
        result["dram_adapter_unused_predictions"]
    )
    adapter_prediction_accounting_delta = dram_adapter_prediction_accounting(
        result, adapter_accounting_available
    )
    logical = float(result["remote_logical_reads"])
    if remote_enabled and logical == 0:
        warnings.append("no remote read opportunity")
    wire_lines = float(result["remote_wire_lines"])
    demand_wire_lines = float(result["remote_demand_wire_lines"])
    packets = float(result["remote_single_packets"]) + float(
        result["remote_bitmap_packets"]
    )
    request_bytes = float(result["remote_network_request_bytes"])
    response_bytes = float(result["remote_network_response_bytes"])
    reuse_transactions = (
        float(result["remote_reuse_first_transactions"])
        + float(result["remote_reuse_second_transactions"])
        + float(result["remote_reuse_third_plus_transactions"])
    )
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
            "dram_adapter_prefetch_hits": adapter_prefetch_hits,
            "dram_adapter_prefetch_hit_pct": percent(
                adapter_prefetch_hits, adapter_predictions
            ),
            "dram_adapter_prediction_pct": percent(
                adapter_predictions, dram_miss_lines
            ),
            "dram_adapter_redundant_avoidance_pct": percent(
                adapter_redundant_avoided,
                adapter_predictions + adapter_redundant_avoided,
            ),
            "dram_adapter_unused_prediction_pct": percent(
                adapter_unused_predictions, adapter_predictions
            ),
            "dram_adapter_prediction_accounting_delta": (
                adapter_prediction_accounting_delta
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
			"remote_reuse_transactions": reuse_transactions,
			"remote_second_transaction_pct": percent(
				float(result["remote_reuse_second_transactions"]),
				reuse_transactions,
			),
			"remote_third_plus_transaction_pct": percent(
				float(result["remote_reuse_third_plus_transactions"]),
				reuse_transactions,
			),
			"remote_reuse_page_filter_positive_pct": percent(
				float(result["remote_reuse_page_filter_positives"]),
				float(result["remote_reuse_page_filter_queries"]),
			),
			"remote_reuse_page_filter_false_positive_pct": percent(
				float(result["remote_reuse_page_filter_false_positives"]),
				float(result["remote_reuse_page_filter_positives"]),
			),
			"remote_page_early_admission_pct": percent(
				float(result["remote_reuse_page_early_admissions"]),
				float(result["remote_reuse_second_transactions"]),
			),
			"remote_proven_pages_per_1k_logical_reads": safe_div(
				1000.0 * float(result["remote_reuse_proven_pages"]),
				logical,
			),
			"resident_filter_negative_pct": percent(
				float(result["l2_resident_filter_negatives"]),
				float(result["l2_resident_filter_queries"]),
			),
			"resident_filter_negative_mshr_merge_pct": percent(
				float(result[
					"l2_resident_filter_read_negative_mshr_merges"
				]),
				float(result["l2_resident_filter_read_bypasses"]),
			),
			"resident_filter_false_positive_pct": percent(
				float(result["l2_resident_filter_false_positives"]),
				float(result["l2_resident_filter_positives"]),
			),
			"resident_filter_full_line_write_bypass_pct": percent(
				float(result[
					"l2_resident_filter_write_full_line_bypasses"
				]),
				float(result["l2_resident_filter_write_bypasses"]),
			),
			"inflight_filter_negative_pct": percent(
				float(result["remote_inflight_filter_negatives"]),
				float(result["remote_inflight_filter_queries"]),
			),
			"inflight_filter_false_positive_pct": percent(
				float(result["remote_inflight_filter_false_positives"]),
				float(result["remote_inflight_filter_positives"]),
			),
            "exact_table_lookup_avoidance_pct": percent(
                float(result["remote_exact_table_lookups_avoided"]),
                float(result["remote_inflight_filter_queries"]),
            ),
            "remote_line_entry_peak_utilization_pct": percent(
                float(result["remote_peak_line_entries"]),
                float(result["remote_config_line_entries"])
                if result["remote_config_line_entries"] != ""
                else 0.0,
            ),
            "remote_waiter_entry_peak_utilization_pct": percent(
                float(result["remote_peak_waiter_entries"]),
                float(result["remote_config_waiter_entries"])
                if result["remote_config_waiter_entries"] != ""
                else 0.0,
            ),
            "remote_owner_child_line_peak_utilization_pct": percent(
                float(result["remote_owner_peak_child_lines"]),
                float(result["remote_config_owner_child_lines"])
                if result["remote_config_owner_child_lines"] != ""
                else 0.0,
            ),
            "demand_wire_avoidance_pct": percent(
                logical - demand_wire_lines, logical
            ),
            "avg_wire_lines_per_packet": safe_div(wire_lines, packets),
            "avg_bitmap_lines": safe_div(
                float(result["remote_bitmap_lines"]),
                float(result["remote_bitmap_packets"]),
            ),
            "avg_bitmap_response_lines": safe_div(
                float(result["remote_bitmap_response_lines"]),
                float(result["remote_bitmap_response_packets"]),
            ),
            "early_bitmap_response_pct": percent(
                float(result["remote_early_bitmap_responses"]),
                float(result["remote_bitmap_response_packets"]),
            ),
            "batch_size_p50_lines": histogram_percentile(histogram, 50),
            "batch_size_p95_lines": histogram_percentile(histogram, 95),
            "l2_probe_hit_pct": percent(
                float(result["remote_l2_probe_hits"]),
                float(result["remote_l2_probe_hits"])
                + float(result["remote_l2_probe_misses"]),
            ),
            "l2_one_touch_probe_bypass_pct": percent(
                float(result["remote_l2_one_touch_probe_bypasses"]),
                logical,
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
            "two_touch_useful_fill_pct": percent(
                float(result["remote_useful_two_touch_fills"]),
                float(result["remote_l2_two_touch_installed_fills"]),
            ),
            "reuse_admitted_fill_install_pct": percent(
                float(result["remote_installed_fills"]),
                float(result["remote_clean_fills"]),
            ),
            "reuse_admitted_useful_fill_pct": percent(
                float(result["remote_useful_two_touch_fills"]),
                float(result["remote_installed_fills"]),
            ),
            "reuse_admitted_hits_per_installed_fill": safe_div(
                float(result["remote_replica_probe_hits"]),
                float(result["remote_installed_fills"]),
            ),
            "page_early_fill_install_pct": percent(
                float(result["remote_page_early_installed_fills"]),
                float(result["remote_page_early_fill_attempts"]),
            ),
            "page_early_useful_fill_pct": percent(
                float(result["remote_page_early_useful_fills"]),
                float(result["remote_page_early_installed_fills"]),
            ),
            "page_early_hits_per_installed_fill": safe_div(
                float(result["remote_page_early_replica_hits"]),
                float(result["remote_page_early_installed_fills"]),
            ),
            "line_third_installed_fills": (
                float(result["remote_installed_fills"])
                - float(result["remote_page_early_installed_fills"])
            ),
            "line_third_useful_fills": (
                float(result["remote_useful_two_touch_fills"])
                - float(result["remote_page_early_useful_fills"])
            ),
            "line_third_replica_hits": (
                float(result["remote_replica_probe_hits"])
                - float(result["remote_page_early_replica_hits"])
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
            "two_touch_unused_fills": float(
                result["remote_l2_two_touch_installed_fills"]
            )
            - float(result["remote_useful_two_touch_fills"]),
        }
    )

    if adapter_prefetch_hits > adapter_predictions:
        warnings.append(
            "dram_adapter_prefetch_hits_exceed_predictions="
            f"{adapter_prefetch_hits - adapter_predictions:g}"
        )
    if (
        adapter_prediction_accounting_delta != ""
        and float(adapter_prediction_accounting_delta) != 0
    ):
        warnings.append(
            "dram_adapter_prediction_accounting_delta="
            f"{float(adapter_prediction_accounting_delta):g}"
        )
    rdma_capacity = result["rdma_max_outstanding"]
    if (
        rdma_capacity != ""
        and float(result["rdma_peak_outstanding"]) > float(rdma_capacity)
    ):
        warnings.append(
            "rdma_peak_outstanding_exceeds_capacity="
            f"{float(result['rdma_peak_outstanding']) - float(rdma_capacity):g}"
        )
    add_balance_warning(
        warnings,
        "rdma_full_stall_balance",
        float(result["rdma_outstanding_full_stalls"])
        - float(result["rdma_requester_outstanding_full_stalls"])
        - float(result["rdma_owner_outstanding_full_stalls"]),
        enabled,
    )
    add_balance_warning(
        warnings,
        "resident_filter_write_class_balance",
        float(result["l2_resident_filter_write_bypasses"])
        - float(result["l2_resident_filter_write_full_line_bypasses"])
        - float(result["l2_resident_filter_write_partial_bypasses"]),
        bool(metric_values(
            rows, "l2_resident_filter_write_full_line_bypasses"
        )) or bool(metric_values(
            rows, "l2_resident_filter_write_partial_bypasses"
        )),
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
        wire_lines - demand_wire_lines,
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
        "page_early_fill_balance",
        float(result["remote_page_early_fill_attempts"])
        - float(result["remote_page_early_installed_fills"])
        - float(result["remote_page_early_dropped_fills"]),
        remote_enabled,
    )
    if (
        float(result["remote_page_early_useful_fills"])
        > float(result["remote_page_early_installed_fills"])
    ):
        warnings.append("page_early_useful_fills_exceed_installed")
    if (
        float(result["remote_page_early_replica_hits"])
        < float(result["remote_page_early_useful_fills"])
    ):
        warnings.append("page_early_hits_below_useful_fills")
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
        - float(result["remote_l2_two_touch_fill_attempts"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_install_origin_balance",
        float(result["remote_installed_fills"])
        - float(result["remote_l2_two_touch_installed_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "fill_drop_origin_balance",
        float(result["remote_dropped_fills"])
        - float(result["remote_l2_two_touch_dropped_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "rdma_l2_fill_attempt_balance",
        float(result["remote_two_touch_fill_attempts"])
        - float(result["remote_clean_fills"]),
        remote_enabled,
    )
    add_balance_warning(
        warnings,
        "rdma_l2_fill_install_balance",
        float(result["remote_two_touch_installed_fills"])
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
    bitmap_response_packets = float(result["remote_bitmap_response_packets"])
    bitmap_response_lines = float(result["remote_bitmap_response_lines"])
    # Older metrics predate partial bitmap responses and therefore have no
    # response-packet counters. They used exactly one response per request.
    if bitmap_response_packets == 0:
        bitmap_response_packets = float(result["remote_bitmap_packets"])
        bitmap_response_lines = float(result["remote_bitmap_lines"])
    expected_response_bytes = (
        68 * float(result["remote_single_packets"])
        + 4 * bitmap_response_packets
        + 64 * bitmap_response_lines
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
    return result, bool(enabled and has_fatal_warning(warnings))


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
        "baseline_local_optimization_only": "Local optimization only",
        "baseline_remote_request_only": "Remote request only",
        "baseline_remote_l2_only": "Remote L2 only",
        "baseline_all_three": "All three combined",
    }
    standalone_configs = (
        "baseline_local_optimization_only",
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
        config = str(result.get("ablation_config", ""))
        group = str(result.get("ablation_group", ""))
        if config not in REMOTE_ABLATION_ORDER:
            continue
        baseline = indexed.get((group, "baseline"))
        if baseline is not None:
            baseline_time = baseline.get("driver_total_time_s", "")
        else:
            # --remote-ablation intentionally does not rerun baseline. Use
            # the externally matched baseline already attached to this row.
            baseline_time = result.get("baseline_driver_total_time_s", "")
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
        "displacement is only a pressure proxy, not proof of a later miss. "
        "DRAM reduction counts L2-to-controller requests. The reported "
        "physical-access size must be used to determine whether that also "
        "reduces DRAM column commands.",
        "",
        "| Run | Mechanism | Baseline speedup | Runtime reduction | "
        "Combined interaction | DRAM controller-request reduction | "
        "Physical read-unit reduction | Physical read-byte reduction | "
        "DRAM access unit | DRAM row reuse | keep-open | ACT/column | "
        "M1 sibling use | M1 negative-MSHR share | "
        "Remote packet reduction | Wire-byte saving | RDMA stall reduction | "
        "Early bitmap response | Reuse-admitted useful | "
        "Local displacement/fill | Warnings |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|",
    ]
    for result in results:
        warnings = str(result.get("analysis_warnings", "")).replace("|", "/")
        lines.append(
            "| {run} | {mechanism} | {speedup} | {reduction} | "
            "{interaction} | {dram} | {physical_reads} | "
            "{physical_read_bytes} | {access_unit} | {row_reuse} | {row_keep_open} | "
            "{activates} | {sibling_use} | {mshr_merges} | "
            "{packets} | {bytes_} | {rdma_stalls} | "
            "{early_response} | {two_touch} | {displacement} | {warnings} |".format(
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
                physical_reads=format_value(
                    result.get("dram_physical_read_reduction_pct"), "%"
                ),
                physical_read_bytes=format_value(
                    result.get("dram_physical_read_byte_reduction_pct"), "%"
                ),
                access_unit=format_value(
                    result.get("dram_physical_access_bytes"), " B"
                ),
                row_reuse=format_value(
                    result.get("dram_row_reuse_pct"), "%"
                ),
                row_keep_open=format_value(
                    result.get("dram_row_auto_precharge_stop_pct"), "%"
                ),
                activates=format_value(
                    result.get("dram_row_activates_per_column")
                ),
                sibling_use=format_value(
                    result.get("dram_adapter_prefetch_hit_pct"), "%"
                ),
                mshr_merges=format_value(
                    result.get(
                        "resident_filter_negative_mshr_merge_pct"
                    ),
                    "%",
                ),
                packets=format_value(
                    result.get("packet_reduction_vs_logical_pct"), "%"
                ),
                bytes_=format_value(result.get("wire_bytes_saved_pct"), "%"),
                rdma_stalls=format_value(
                    result.get("rdma_outstanding_full_stall_reduction_pct"),
                    "%",
                ),
                early_response=format_value(
                    result.get("early_bitmap_response_pct"), "%"
                ),
                two_touch=format_value(
                    result.get("reuse_admitted_useful_fill_pct"), "%"
                ),
                displacement=format_value(
                    result.get("local_clean_displacements_per_fill")
                ),
                warnings=warnings or "none",
            )
        )
    lines.extend(
        [
            "",
            "## Component latency change",
            "",
            "Reductions are request-count-weighted baseline-to-mechanism "
            "changes; positive is better. RDMA is the generic routing "
            "component, while remote logical latency is measured only for "
            "the enabled remote-data path.",
            "",
            "| Run | L1V reduction | L2 reduction | DRAM-read reduction | "
            "DRAM-write reduction | Generic-RDMA reduction | "
            "Remote logical latency |",
            "|---|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for result in results:
        lines.append(
            "| {run} | {l1v} | {l2} | {dram_read} | {dram_write} | "
            "{rdma} | {remote} |".format(
                run=result["run"],
                l1v=format_value(
                    result.get("l1v_req_avg_latency_reduction_pct"), "%"
                ),
                l2=format_value(
                    result.get("l2_req_avg_latency_reduction_pct"), "%"
                ),
                dram_read=format_value(
                    result.get("dram_read_avg_latency_reduction_pct"), "%"
                ),
                dram_write=format_value(
                    result.get("dram_write_avg_latency_reduction_pct"), "%"
                ),
                rdma=format_value(
                    result.get(
                        "rdma_generic_req_avg_latency_reduction_pct"
                    ),
                    "%",
                ),
                remote=format_value(
                    result.get("logical_read_latency_avg_ns"), " ns"
                ),
            )
        )
    lines.extend(
        [
            "",
            "## RDMA bounded state",
            "",
            "Packet outstanding descriptors and line-level coalescing state "
            "are separate resources. A candidate is not credited for moving "
            "backpressure from the former to the latter.",
            "",
            "| Run | Packet capacity | Requester packet peak | "
            "Owner packet peak | Line capacity | Line peak | "
            "Line-full stalls | Waiter capacity | Waiter peak | "
            "Waiter-full stalls | Owner child capacity | "
            "Owner child peak | Owner child-full stalls |",
            "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for result in results:
        lines.append(
            "| {run} | {packet_capacity} | {requester_peak} | "
            "{owner_peak} | {line_capacity} | {line_peak} | "
            "{line_stalls} | {waiter_capacity} | {waiter_peak} | "
            "{waiter_stalls} | {owner_child_capacity} | "
            "{owner_children} | {owner_child_stalls} |".format(
                run=result["run"],
                packet_capacity=format_value(
                    result.get("rdma_max_outstanding")
                ),
                requester_peak=format_value(
                    result.get("rdma_requester_peak_outstanding")
                ),
                owner_peak=format_value(
                    result.get("rdma_owner_peak_outstanding")
                ),
                line_capacity=format_value(
                    result.get("remote_config_line_entries")
                ),
                line_peak=format_value(
                    result.get("remote_peak_line_entries")
                ),
                line_stalls=format_value(
                    result.get("remote_line_entry_full_stalls")
                ),
                waiter_capacity=format_value(
                    result.get("remote_config_waiter_entries")
                ),
                waiter_peak=format_value(
                    result.get("remote_peak_waiter_entries")
                ),
                waiter_stalls=format_value(
                    result.get("remote_waiter_entry_full_stalls")
                ),
                owner_child_capacity=format_value(
                    result.get("remote_config_owner_child_lines")
                ),
                owner_children=format_value(
                    result.get("remote_owner_peak_child_lines")
                ),
                owner_child_stalls=format_value(
                    result.get("remote_owner_child_line_full_stalls")
                ),
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
    baseline_dirs = args.baseline_dirs or []
    if args.allow_sampled_instruction_mismatch:
        if not baseline_dirs or not args.input.is_dir():
            raise ValueError(
                "--allow-sampled-instruction-mismatch requires one result "
                "directory and at least one --baseline-dir"
            )
        experiment_hash = binary_manifest_hash(args.input, required=True)
        for baseline_dir in baseline_dirs:
            baseline_hash = binary_manifest_hash(
                baseline_dir if baseline_dir.is_dir() else baseline_dir.parent,
                required=True,
            )
            if baseline_hash != experiment_hash:
                raise ValueError(
                    "sampled instruction mismatch requires matching frozen "
                    "baseline and experiment binaries"
                )
    baselines = [
        path
        for baseline_dir in baseline_dirs
        for path in discover_metrics(baseline_dir)
    ]
    output = args.output
    if output is None:
        root = args.input if args.input.is_dir() else args.input.parent
        output = root / "remote_data_path_analysis.csv"

    results: list[dict[str, object]] = []
    strict_failure = False
    for experiment in inputs:
        baseline = match_baseline(
            experiment, args.input, baseline_dirs, baselines, inputs
        )
        result, has_warning = analyze_one(
            experiment,
            baseline,
            args.flit_bytes,
            args.encoding_overhead,
            allow_sampled_instruction_mismatch=(
                args.allow_sampled_instruction_mismatch
            ),
        )
        if baseline_dirs and baseline is None:
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
