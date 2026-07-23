#!/usr/bin/env python3
"""Validate, summarize, and plot the CuPath typed-filter paper ablation.

The script accepts partial campaigns but leaves missing cells empty. A formal
14-benchmark geomean is emitted only when every benchmark is present.
"""

from __future__ import annotations

import argparse
import csv
import math
import re
from collections import defaultdict
from pathlib import Path

import os

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

from analyze_wg_mapping import audit as audit_wg_mapping
from analyze_wg_mapping import write_outputs as write_wg_mapping_outputs


WORKLOADS = (
    ("aes", "AES", ""),
    ("bitonicsort", "BT", ""),
    ("fastwalshtransform", "FWT", ""),
    ("fft", "FFT", ""),
    ("fir", "FIR", ""),
    ("relu", "RELU", ""),
    ("simpleconvolution", "SC", ""),
    ("floydwarshall", "FWS", ""),
    ("kmeans", "KM", ""),
    ("matrixmultiplication", "MM", ""),
    ("pagerank", "PR", ""),
    ("im2col", "I2C", ""),
    ("matrixtranspose", "MT", ""),
    ("spmv", "SPMV", ""),
)
CONFIGS = (
    ("baseline", "Baseline", "#D6EFF5"),
    ("m1", "M1", "#ADDEEB"),
    ("m2", "M2", "#83CEE2"),
    ("m3", "M3", "#FBE0D0"),
    ("complete", "Complete", "#F4A371"),
)
POSITIVE_THRESHOLD = 1.005
NEGATIVE_THRESHOLD = 0.995
METRICS_RE = re.compile(
    r"^(?P<target>[^_]+)_(?P<benchmark>.+)_(?P<config>baseline|m1|m2|m3|complete)_metrics\.csv$"
)
ELAPSED_RE = re.compile(
    r"Elapsed time:\s*(?:(?P<days>\d+) days?,\s*)?"
    r"(?P<hours>\d+):(?P<minutes>\d+):(?P<seconds>[0-9.]+)"
)
RETURN_CODE_RE = re.compile(r"Return code:\s*(-?\d+)")
MAX_METRICS = {
    "l2_miss_to_dram_issue_max_ns",
    "l2_fast_miss_to_dram_issue_max_ns",
    "l2_demand_read_latency_max_ns",
    "filter_prefetch_predictor_lookahead_max",
    "remote_prefetch_predictor_lookahead_max",
    "remote_batch_queue_wait_max_ns",
    "remote_pre_network_wait_max_ns",
    "remote_probe_latency_max_ns",
    "remote_logical_read_latency_max_ns",
}
UNIFORM_METRICS = (
    "typed_filter_fingerprint_bits",
    "typed_filter_reference_bits",
    "typed_filter_slots_per_bucket",
    "typed_filter_lookup_latency_cycles",
    "typed_filter_update_latency_cycles",
    "typed_filter_lookup_width",
    "typed_filter_update_width",
)

# A complete formal campaign must preserve the raw counters needed to audit
# the Goal's predictor, Filter, M1, M2, and M3 claims.  Partial screens remain
# loadable, but a 70-cell paper campaign must not silently turn a missing
# reporter into a zero in a derived table.
COMPLETE_WORK_METRICS = (
    "l2_resident_filter_queries",
    "l2_resident_filter_read_bypasses",
    "l2_resident_filter_read_negative_mshr_merges",
    "l2_resident_filter_read_parallel_mshr_merges",
    "l2_resident_filter_primed_lookups",
    "l2_mshr_full_stall_cycles",
    "l2_to_dram_64b_requests",
    "l2_demand_read_latency_samples",
    "l2_demand_read_latency_total_ns",
    "l2_demand_read_latency_max_ns",
    "dram_row_column_commands",
    "dram_row_activate_commands",
    "dram_row_precharge_commands",
    "dram_row_auto_precharge_stops",
    "dram_row_reuse_hits",
    "remote_logical_reads",
    "remote_wire_lines",
    "remote_demand_wire_lines",
    "remote_prefetch_wire_lines",
    "remote_duplicate_reads",
    "remote_l2_logical_responses",
    "remote_exact_table_lookups",
    "remote_exact_table_lookups_avoided",
    "remote_peak_line_entries",
    "remote_peak_waiter_entries",
    "remote_logical_read_latency_samples",
    "remote_logical_read_latency_total_ns",
    "remote_logical_read_latency_max_ns",
    "remote_batch_queue_wait_samples",
    "remote_batch_queue_wait_total_ns",
    "remote_batch_queue_wait_max_ns",
    "remote_pre_network_wait_samples",
    "remote_pre_network_wait_total_ns",
    "remote_pre_network_wait_max_ns",
    "remote_probe_latency_samples",
    "remote_probe_latency_total_ns",
    "remote_probe_latency_max_ns",
    "remote_first_touch_lines",
    "remote_second_touch_admissions",
    "remote_multiple_demand_admissions",
    "remote_resident_queries",
    "remote_resident_positives",
    "remote_resident_negatives",
    "remote_seen_queries",
    "remote_seen_hits",
    "remote_seen_negatives",
    "remote_installed_fills",
    "remote_local_clean_protection_drops",
    "remote_reuse_write_uncacheable_skips",
)

REQUIRED_COMPLETE_METRICS = {
    *COMPLETE_WORK_METRICS,
    "l2_miss_to_dram_issue_samples",
    "l2_miss_to_dram_issue_total_ns",
    "l2_miss_to_dram_issue_max_ns",
    "l2_fast_miss_to_dram_issue_samples",
    "l2_fast_miss_to_dram_issue_total_ns",
    "l2_fast_miss_to_dram_issue_max_ns",
    "filter_prefetch_real_demands",
    "filter_prefetch_candidates",
    "filter_prefetch_predictor_evidence_one",
    "filter_prefetch_predictor_evidence_two",
    "filter_prefetch_predictor_capacity",
    "filter_prefetch_predictor_patterns",
    "filter_prefetch_predictor_timely_feedback",
    "filter_prefetch_predictor_late_feedback",
    "filter_prefetch_predictor_late_distance_increases",
    "filter_prefetch_predictor_stale_distance_feedback",
    "filter_prefetch_predictor_lookahead_total",
    "filter_prefetch_predictor_lookahead_max",
    "filter_prefetch_stride_one",
    "filter_prefetch_stride_small",
    "filter_prefetch_stride_medium",
    "filter_prefetch_stride_large",
    "filter_prefetch_stride_negative",
    "filter_prefetch_pattern_installs",
    "filter_prefetch_pattern_install_drops",
    "filter_prefetch_candidate_busy_drops",
    "filter_prefetch_pattern_negative_drops",
    "filter_prefetch_resident_positive_drops",
    "filter_prefetch_pending_positive_drops",
    "filter_prefetch_wrong_slice_drops",
    "filter_prefetch_demand_priority_drops",
    "filter_prefetch_controller_busy_drops",
    "filter_prefetch_output_busy_drops",
    "filter_prefetch_mshr_drops",
    "filter_prefetch_victim_drops",
    "filter_prefetch_pending_insert_drops",
    "filter_prefetch_issued",
    "filter_prefetch_outstanding",
    "filter_prefetch_redundant_races",
    "filter_prefetch_fills",
    "filter_prefetch_useful",
    "filter_prefetch_timely",
    "filter_prefetch_late",
    "filter_prefetch_late_after_dram_issue",
    "filter_prefetch_unused",
    "filter_prefetch_unused_evictions",
    "filter_prefetch_unused_reset_retirements",
    "filter_prefetch_current_prefetch_only_lines",
    "filter_prefetch_peak_prefetch_only_lines",
    "filter_prefetch_demand_merges",
    "filter_prefetch_demand_won_races",
    "filter_prefetch_additional_dram_reads",
    "filter_prefetch_demand_delay_events",
    "filter_prefetch_mshr_headroom_drops",
    "remote_prefetch_real_demands",
    "remote_prefetch_candidates",
    "remote_prefetch_pattern_installs",
    "remote_prefetch_pattern_install_drops",
    "remote_prefetch_filter_drops",
    "remote_prefetch_same_group_drops",
    "remote_prefetch_capacity_drops",
    "remote_prefetch_no_existing_batch_drops",
    "remote_prefetch_batch_full_drops",
    "remote_prefetch_piggyback_lines",
    "remote_prefetch_useful",
    "remote_prefetch_unused",
    "remote_prefetch_standalone_prevented",
    "remote_prefetch_added_response_bytes",
    "remote_prefetch_additional_owner_reads",
    "remote_prefetch_predictor_evidence_one",
    "remote_prefetch_predictor_evidence_two",
    "remote_prefetch_stride_one",
    "remote_prefetch_stride_small",
    "remote_prefetch_stride_medium",
    "remote_prefetch_stride_large",
    "remote_prefetch_stride_negative",
    "remote_pre_send_merges",
    "remote_inflight_merges",
    "remote_ready_merges",
    "remote_fanout_responses",
    "remote_single_packets",
    "remote_bitmap_packets",
    "remote_network_request_bytes",
    "remote_network_response_bytes",
    "remote_requester_issue_width_stalls",
    "remote_response_fanout_width_stalls",
    "remote_owner_issue_width_stalls",
    "remote_owner_response_width_stalls",
    "remote_requester_l2_hits",
    "remote_requester_l2_unused_fills",
    "remote_l2_two_touch_installed_fills",
    "remote_fill_into_invalid",
    "remote_fill_replaced_remote",
    "remote_fill_displaced_local_clean",
    "remote_two_touch_local_displacements",
    "remote_dropped_fills",
    "remote_tracked_evictions",
    "remote_requester_l2_current_lines",
    "remote_requester_l2_peak_lines",
    "remote_requester_l2_unused_pattern_retirements",
    "remote_speculative_invalid_only_attempts",
    "remote_speculative_invalid_only_drops",
    "remote_l2_one_touch_probe_bypasses",
    "remote_l2_probe_hits",
    "remote_l2_probe_misses",
    "typed_filter_lookup_port_stalls",
    "typed_filter_update_port_stalls",
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
}
BASELINE_WORK_METRICS = (
    "l2_mshr_full_stall_cycles",
    "l2_to_dram_64b_requests",
    "l2_demand_read_latency_samples",
    "l2_demand_read_latency_total_ns",
    "l2_demand_read_latency_max_ns",
    "dram_row_column_commands",
    "dram_row_activate_commands",
    "dram_row_precharge_commands",
    "rdma_observed_remote_reads",
)

REQUIRED_BASELINE_METRICS = {
    *BASELINE_WORK_METRICS,
    "l2_miss_to_dram_issue_samples",
    "l2_miss_to_dram_issue_total_ns",
    "l2_miss_to_dram_issue_max_ns",
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
}

M1_PREFETCH_METRICS = (
    "filter_prefetch_real_demands",
    "filter_prefetch_candidates",
    "filter_prefetch_pattern_installs",
    "filter_prefetch_pattern_install_drops",
    "filter_prefetch_candidate_busy_drops",
    "filter_prefetch_pattern_negative_drops",
    "filter_prefetch_resident_positive_drops",
    "filter_prefetch_pending_positive_drops",
    "filter_prefetch_wrong_slice_drops",
    "filter_prefetch_demand_priority_drops",
    "filter_prefetch_controller_busy_drops",
    "filter_prefetch_output_busy_drops",
    "filter_prefetch_mshr_drops",
    "filter_prefetch_victim_drops",
    "filter_prefetch_pending_insert_drops",
    "filter_prefetch_issued",
    "filter_prefetch_outstanding",
    "filter_prefetch_redundant_races",
    "filter_prefetch_fills",
    "filter_prefetch_useful",
    "filter_prefetch_timely",
    "filter_prefetch_late",
    "filter_prefetch_late_after_dram_issue",
    "filter_prefetch_demand_won_races",
    "filter_prefetch_unused",
    "filter_prefetch_unused_evictions",
    "filter_prefetch_unused_reset_retirements",
    "filter_prefetch_current_prefetch_only_lines",
    "filter_prefetch_peak_prefetch_only_lines",
    "filter_prefetch_demand_merges",
    "filter_prefetch_additional_dram_reads",
    "filter_prefetch_demand_delay_events",
    "filter_prefetch_mshr_headroom_drops",
    "filter_prefetch_predictor_capacity",
    "filter_prefetch_predictor_patterns",
    "filter_prefetch_predictor_evidence_one",
    "filter_prefetch_predictor_evidence_two",
    "filter_prefetch_predictor_timely_feedback",
    "filter_prefetch_predictor_late_feedback",
    "filter_prefetch_predictor_late_distance_increases",
    "filter_prefetch_predictor_stale_distance_feedback",
    "filter_prefetch_predictor_lookahead_total",
    "filter_prefetch_predictor_lookahead_max",
    "filter_prefetch_stride_one",
    "filter_prefetch_stride_small",
    "filter_prefetch_stride_medium",
    "filter_prefetch_stride_large",
    "filter_prefetch_stride_negative",
)
M1_ADAPTIVE_PAIR_METRICS = (
    "adaptive_pair_miss_lines_seen",
    "adaptive_pair_observations",
    "adaptive_pair_useful",
    "adaptive_pair_predictions",
    "adaptive_pair_inflight_hits",
    "adaptive_pair_buffer_hits",
    "adaptive_pair_prefetch_unused",
    "adaptive_pair_prefetch_unused_evictions",
    "adaptive_pair_prefetch_unused_invalidates",
    "adaptive_pair_prefetch_unused_reset_retires",
    "adaptive_pair_current_prefetch_only_lines",
    "adaptive_pair_peak_prefetch_only_lines",
    "adaptive_pair_wide_128b_reads",
    "adaptive_pair_filter_candidates",
    "adaptive_pair_filter_lookups",
    "adaptive_pair_filter_busy_fallbacks",
    "adaptive_pair_filter_not_ready_fallbacks",
    "adaptive_pair_filter_unreliable_fallbacks",
    "adaptive_pair_resident_filter_positives",
    "adaptive_pair_resident_exact_suppressions",
    "adaptive_pair_resident_false_positives",
    "adaptive_pair_pending_filter_positives",
    "adaptive_pair_pending_exact_suppressions",
    "adaptive_pair_pending_false_positives",
    "adaptive_pair_pending_filter_insert_failures",
)
REQUIRED_M1_METRICS = {
    "l2_miss_to_dram_issue_samples",
    "l2_miss_to_dram_issue_total_ns",
    "l2_miss_to_dram_issue_max_ns",
    "l2_fast_miss_to_dram_issue_samples",
    "l2_fast_miss_to_dram_issue_total_ns",
    "l2_fast_miss_to_dram_issue_max_ns",
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
    "l2_mshr_full_stall_cycles",
    "l2_to_dram_64b_requests",
    "l2_demand_read_latency_samples",
    "l2_demand_read_latency_total_ns",
    "l2_demand_read_latency_max_ns",
    *M1_ADAPTIVE_PAIR_METRICS,
}

# The formal M1/Complete configurations use the adaptive paired-read path.
# Legacy filter-prefetch counters may still be emitted for diagnostics, but
# they are not authoritative coverage requirements for formal M1.
REQUIRED_COMPLETE_METRICS.update(M1_ADAPTIVE_PAIR_METRICS)

M2_REMOTE_METRICS = (
    "remote_logical_reads",
    "remote_wire_lines",
    "remote_demand_wire_lines",
    "remote_prefetch_wire_lines",
    "remote_duplicate_reads",
    "remote_pre_send_merges",
    "remote_inflight_merges",
    "remote_ready_merges",
    "remote_fanout_responses",
    "remote_single_packets",
    "remote_bitmap_packets",
    "remote_bitmap_lines",
    "remote_network_request_bytes",
    "remote_network_response_bytes",
    "remote_peak_line_entries",
    "remote_peak_waiter_entries",
    "remote_requester_issue_width_stalls",
    "remote_response_fanout_width_stalls",
    "remote_owner_issue_width_stalls",
    "remote_owner_response_width_stalls",
    "remote_logical_read_latency_samples",
    "remote_logical_read_latency_total_ns",
    "remote_logical_read_latency_max_ns",
    "remote_batch_queue_wait_samples",
    "remote_batch_queue_wait_total_ns",
    "remote_batch_queue_wait_max_ns",
    "remote_pre_network_wait_samples",
    "remote_pre_network_wait_total_ns",
    "remote_pre_network_wait_max_ns",
    "remote_prefetch_real_demands",
    "remote_prefetch_candidates",
    "remote_prefetch_pattern_installs",
    "remote_prefetch_pattern_install_drops",
    "remote_prefetch_filter_drops",
    "remote_prefetch_same_group_drops",
    "remote_prefetch_capacity_drops",
    "remote_prefetch_no_existing_batch_drops",
    "remote_prefetch_batch_full_drops",
    "remote_prefetch_piggyback_lines",
    "remote_prefetch_useful",
    "remote_prefetch_unused",
    "remote_prefetch_standalone_prevented",
    "remote_prefetch_added_response_bytes",
    "remote_prefetch_additional_owner_reads",
    "remote_prefetch_predictor_evidence_one",
    "remote_prefetch_predictor_evidence_two",
    "remote_prefetch_predictor_timely_feedback",
    "remote_prefetch_predictor_late_feedback",
    "remote_prefetch_predictor_late_distance_increases",
    "remote_prefetch_predictor_lookahead_total",
    "remote_prefetch_predictor_lookahead_max",
    "remote_prefetch_stride_one",
    "remote_prefetch_stride_small",
    "remote_prefetch_stride_medium",
    "remote_prefetch_stride_large",
    "remote_prefetch_stride_negative",
)
REQUIRED_M2_METRICS = {
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
    *M2_REMOTE_METRICS,
}

M3_REMOTE_METRICS = (
    "remote_logical_reads",
    "remote_wire_lines",
    "remote_demand_wire_lines",
    "remote_duplicate_reads",
    "remote_single_packets",
    "remote_network_request_bytes",
    "remote_network_response_bytes",
    "remote_logical_read_latency_samples",
    "remote_logical_read_latency_total_ns",
    "remote_logical_read_latency_max_ns",
    "remote_probe_latency_samples",
    "remote_probe_latency_total_ns",
    "remote_probe_latency_max_ns",
    "remote_l2_one_touch_probe_bypasses",
    "remote_l2_probe_hits",
    "remote_l2_probe_misses",
    "remote_l2_logical_responses",
    "remote_requester_l2_hits",
    "remote_requester_l2_unused_fills",
    "remote_l2_two_touch_installed_fills",
    "remote_fill_into_invalid",
    "remote_fill_replaced_remote",
    "remote_fill_displaced_local_clean",
    "remote_two_touch_local_displacements",
    "remote_dropped_fills",
    "remote_tracked_evictions",
    "remote_requester_l2_current_lines",
    "remote_requester_l2_peak_lines",
    "remote_requester_l2_unused_pattern_retirements",
    "remote_speculative_invalid_only_attempts",
    "remote_speculative_invalid_only_drops",
    "remote_first_touch_lines",
    "remote_second_touch_admissions",
    "remote_multiple_demand_admissions",
    "remote_resident_queries",
    "remote_resident_positives",
    "remote_resident_negatives",
    "remote_seen_queries",
    "remote_seen_hits",
    "remote_seen_negatives",
    "remote_installed_fills",
    "remote_local_clean_protection_drops",
    "remote_reuse_write_uncacheable_skips",
)
REQUIRED_M3_METRICS = {
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
    *M3_REMOTE_METRICS,
}
REQUIRED_EXECUTION_METRICS = {
    "total_wg_count",
    "max_wg_limit",
    "max_wg_observed",
    "max_wg_runtime_stopper",
    "max_wg_reached",
    "wg_requested_total",
    "wg_stop_time_ns",
    "wg_max_wg_specific_filter",
    "allocation_page_size",
    "allocation_workload_allocated_pages",
    "allocation_overall_allocated_pages",
}
for _kind in ("pattern", "resident", "pending", "seen"):
    for _field in (
        "queries", "positives", "negatives", "false_positives",
        "insertions", "deletes", "insert_failures", "fail_open",
        "lookup_busy_drops", "update_busy_drops", "occupancy",
        "peak_occupancy",
    ):
        REQUIRED_COMPLETE_METRICS.add(f"typed_filter_{_kind}_{_field}")
        REQUIRED_M2_METRICS.add(f"typed_filter_{_kind}_{_field}")
        REQUIRED_M3_METRICS.add(f"typed_filter_{_kind}_{_field}")


def read_metrics(path: Path) -> tuple[dict[str, float], dict[str, set[float]]]:
    totals: dict[str, float] = defaultdict(float)
    values: dict[str, set[float]] = defaultdict(set)
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            what = row["what"].strip()
            value = float(row["value"])
            if what in MAX_METRICS:
                totals[what] = max(totals.get(what, 0.0), value)
            else:
                totals[what] += value
            values[what].add(value)
            if row["where"].strip() == "Driver" and what == "total_time":
                totals["__driver_total_time"] = value
    if "__driver_total_time" not in totals:
        raise ValueError(f"Driver total_time missing: {path}")
    if values["config_l1v_mshr_entries"] != {16.0}:
        raise ValueError(
            f"formal result is not 16-MSHR: {path}: "
            f"{sorted(values['config_l1v_mshr_entries'])}"
        )
    if values["config_l2_slices_per_gpm"] != {4.0}:
        raise ValueError(
            f"formal result is not four-slice: {path}: "
            f"{sorted(values['config_l2_slices_per_gpm'])}"
        )
    for what in UNIFORM_METRICS:
        observed = values.get(what, set())
        if observed:
            if len(observed) != 1:
                raise ValueError(
                    f"non-uniform {what} in {path}: {sorted(observed)}"
                )
            totals[f"__{what}"] = next(iter(observed))
    return dict(totals), dict(values)


def load_campaign(root: Path):
    paper_names = {name for name, _, _ in WORKLOADS}
    config_names = {name for name, _, _ in CONFIGS}
    campaign = {}
    paths = {}
    for path in sorted(root.glob("*_metrics.csv")):
        match = METRICS_RE.match(path.name)
        if not match:
            continue
        benchmark = match.group("benchmark")
        config = match.group("config")
        if benchmark not in paper_names or config not in config_names:
            continue
        key = (benchmark, config)
        if key in campaign:
            raise ValueError(f"duplicate result for {key}: {paths[key]}, {path}")
        campaign[key], _ = read_metrics(path)
        paths[key] = path
    return campaign, paths


def load_campaign_roots(roots):
    """Merge disjoint formal cells from independently launched campaigns."""
    campaign = {}
    paths = {}
    for root in roots:
        partial, partial_paths = load_campaign(root)
        for key, metrics in partial.items():
            if key in campaign:
                raise ValueError(
                    f"duplicate result for {key}: {paths[key]}, "
                    f"{partial_paths[key]}"
                )
            campaign[key] = metrics
            paths[key] = partial_paths[key]
    return campaign, paths


def validate_complete_metric_coverage(campaign):
    if len(campaign) != len(WORKLOADS) * len(CONFIGS):
        return
    for benchmark, _, _ in WORKLOADS:
        for config, _, _ in CONFIGS:
            metrics = campaign[(benchmark, config)]
            missing = sorted(REQUIRED_EXECUTION_METRICS - metrics.keys())
            if missing:
                raise ValueError(
                    f"{benchmark}/{config} is missing execution metrics: "
                    + ", ".join(missing)
                )
        for config, required in (
            ("baseline", REQUIRED_BASELINE_METRICS),
            ("m1", REQUIRED_M1_METRICS),
            ("m2", REQUIRED_M2_METRICS),
            ("m3", REQUIRED_M3_METRICS),
            ("complete", REQUIRED_COMPLETE_METRICS),
        ):
            metrics = campaign[(benchmark, config)]
            missing = sorted(required - metrics.keys())
            if missing:
                raise ValueError(
                    f"{benchmark}/{config} is missing required raw metrics: "
                    + ", ".join(missing)
                )


def speedups(campaign):
    result = {}
    for benchmark, _, _ in WORKLOADS:
        baseline = campaign.get((benchmark, "baseline"))
        if baseline is None:
            continue
        baseline_time = baseline["__driver_total_time"]
        for config, _, _ in CONFIGS:
            metrics = campaign.get((benchmark, config))
            if metrics is None:
                continue
            result[(benchmark, config)] = (
                baseline_time / metrics["__driver_total_time"]
            )
    return result


def read_elapsed_seconds(metrics_path: Path):
    stdout = metrics_path.with_name(
        metrics_path.name.removesuffix("_metrics.csv") + "_out.stdout"
    )
    if not stdout.exists():
        return None
    match = ELAPSED_RE.search(stdout.read_text(encoding="utf-8", errors="replace"))
    if match is None:
        return None
    return (
        int(match.group("days") or 0) * 86400
        + int(match.group("hours")) * 3600
        + int(match.group("minutes")) * 60
        + float(match.group("seconds"))
    )


def read_return_code(metrics_path: Path):
    stdout = metrics_path.with_name(
        metrics_path.name.removesuffix("_metrics.csv") + "_out.stdout"
    )
    if not stdout.exists():
        return None
    match = RETURN_CODE_RE.search(
        stdout.read_text(encoding="utf-8", errors="replace")
    )
    return None if match is None else int(match.group(1))


def write_execution_audit(path: Path, campaign, paths):
    """Export runtime-stopper and process status for every formal cell."""
    fields = (
        "total_wg_count", "max_wg_limit", "max_wg_observed",
        "max_wg_runtime_stopper", "max_wg_reached",
        "wg_requested_total", "wg_stop_time_ns",
        "wg_max_wg_specific_filter", "allocation_page_size",
        "allocation_workload_allocated_pages",
        "allocation_overall_allocated_pages", "return_code",
    )
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            ["benchmark"]
            + [f"{config}_{field}" for config, _, _ in CONFIGS for field in fields]
        )
        for benchmark, _, _ in WORKLOADS:
            values = []
            for config, _, _ in CONFIGS:
                metrics = campaign.get((benchmark, config))
                metrics_path = paths.get((benchmark, config))
                if metrics is None or metrics_path is None:
                    values.extend([""] * len(fields))
                    continue
                values.extend([
                    metrics.get("total_wg_count", 0.0),
                    metrics.get("max_wg_limit", 0.0),
                    metrics.get("max_wg_observed", 0.0),
                    metrics.get("max_wg_runtime_stopper", 0.0),
                    metrics.get("max_wg_reached", 0.0),
                    metrics.get("wg_requested_total", 0.0),
                    metrics.get("wg_stop_time_ns", 0.0),
                    metrics.get("wg_max_wg_specific_filter", 0.0),
                    metrics.get("allocation_page_size", 0.0),
                    metrics.get("allocation_workload_allocated_pages", 0.0),
                    metrics.get("allocation_overall_allocated_pages", 0.0),
                    read_return_code(metrics_path),
                ])
            writer.writerow([benchmark, *values])


def write_workload_footprint_table(path: Path, campaign):
    """Export the paper workload sizes from modeled allocation counters."""
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "benchmark", "label", "observed_workgroups", "page_size_bytes",
            "workload_allocated_pages", "workload_footprint_mib",
            "overall_allocated_pages",
        ])
        for benchmark, label, _ in WORKLOADS:
            metrics = campaign.get((benchmark, "baseline"))
            if metrics is None:
                writer.writerow([benchmark, label, "", "", "", "", ""])
                continue
            page_size = metrics.get("allocation_page_size", 0.0)
            pages = metrics.get("allocation_workload_allocated_pages", 0.0)
            writer.writerow([
                benchmark,
                label,
                metrics.get("total_wg_count", 0.0),
                page_size,
                pages,
                pages * page_size / (1024 * 1024),
                metrics.get("allocation_overall_allocated_pages", 0.0),
            ])


def write_runtime_table(path: Path, paths, campaign):
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            ["benchmark"]
            + [f"{name}_wall_s" for name, _, _ in CONFIGS]
            + [f"{name}_wall_over_baseline" for name, _, _ in CONFIGS]
            + [f"{name}_wall_per_simulated_s" for name, _, _ in CONFIGS]
            + [
                f"{name}_normalized_host_cost_over_baseline"
                for name, _, _ in CONFIGS
            ]
        )
        for benchmark, _, _ in WORKLOADS:
            times = [
                read_elapsed_seconds(paths[(benchmark, name)])
                if (benchmark, name) in paths else None
                for name, _, _ in CONFIGS
            ]
            baseline = times[0]
            ratios = [
                "" if value is None or baseline in (None, 0) else value / baseline
                for value in times
            ]
            simulated = [
                campaign.get((benchmark, name), {}).get("__driver_total_time")
                for name, _, _ in CONFIGS
            ]
            host_cost = [
                "" if wall is None or sim_time in (None, 0)
                else wall / sim_time
                for wall, sim_time in zip(times, simulated)
            ]
            baseline_host_cost = host_cost[0]
            normalized_host_cost = [
                "" if cost == "" or baseline_host_cost in ("", 0)
                else cost / baseline_host_cost
                for cost in host_cost
            ]
            writer.writerow([
                benchmark,
                *("" if value is None else value for value in times),
                *ratios,
                *host_cost,
                *normalized_host_cost,
            ])


def geomean(values):
    values = list(values)
    if not values or any(value <= 0 for value in values):
        return None
    return math.exp(sum(math.log(value) for value in values) / len(values))


def load_traffic_classes(path: Path) -> dict[str, str]:
    if not path.is_file():
        return {}
    with path.open(newline="", encoding="utf-8") as stream:
        return {
            row["benchmark"]: row["traffic_class"]
            for row in csv.DictReader(stream)
        }


def write_speedup_table(path: Path, campaign, speedup, traffic_classes=None):
    traffic_classes = traffic_classes or {}
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            ["benchmark", "label", "group"]
            + [f"{name}_time_s" for name, _, _ in CONFIGS]
            + [f"{name}_speedup" for name, _, _ in CONFIGS]
        )
        for benchmark, label, _ in WORKLOADS:
            times = [
                campaign.get((benchmark, name), {}).get("__driver_total_time", "")
                for name, _, _ in CONFIGS
            ]
            gains = [speedup.get((benchmark, name), "") for name, _, _ in CONFIGS]
            writer.writerow([
                benchmark, label, traffic_classes.get(benchmark, ""),
                *times, *gains,
            ])
        full = []
        for name, _, _ in CONFIGS:
            values = [speedup.get((benchmark, name)) for benchmark, _, _ in WORKLOADS]
            full.append(geomean(values) if all(v is not None for v in values) else "")
        writer.writerow(["geomean_14", "GM", "All", *([""] * len(CONFIGS)), *full])


def write_baseline_complete_speedup_table(path: Path, campaign, speedup):
    """Write the formal two-configuration result without empty ablation columns."""
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "benchmark", "label", "baseline_time_s", "complete_time_s",
            "baseline_speedup", "complete_speedup",
        ])
        complete_values = []
        for benchmark, label, _ in WORKLOADS:
            baseline = campaign.get((benchmark, "baseline"), {})
            complete = campaign.get((benchmark, "complete"), {})
            complete_speedup = speedup.get((benchmark, "complete"), "")
            writer.writerow([
                benchmark,
                label,
                baseline.get("__driver_total_time", ""),
                complete.get("__driver_total_time", ""),
                speedup.get((benchmark, "baseline"), ""),
                complete_speedup,
            ])
            if complete_speedup != "":
                complete_values.append(complete_speedup)
        complete_geomean = (
            geomean(complete_values)
            if len(complete_values) == len(WORKLOADS)
            else ""
        )
        writer.writerow(["geomean_14", "GM", "", "", 1.0, complete_geomean])


def write_filter_table(path: Path, campaign):
    physical = (
        "__typed_filter_fingerprint_bits",
        "__typed_filter_reference_bits",
        "__typed_filter_slots_per_bucket",
        "__typed_filter_lookup_latency_cycles",
        "__typed_filter_update_latency_cycles",
        "__typed_filter_lookup_width",
        "__typed_filter_update_width",
        "typed_filter_slots",
        "typed_filter_storage_bits",
        "typed_filter_peak_occupancy",
        "typed_filter_lookup_port_stalls",
        "typed_filter_update_port_stalls",
    )
    per_type = tuple(
        f"typed_filter_{kind}_{field}"
        for kind in (
            "pattern", "resident", "pending", "seen", "granularity_pending"
        )
        for field in (
            "reliable", "queries", "positives", "negatives", "false_positives",
            "insertions", "deletes", "insert_failures", "fail_open",
            "lookup_busy_drops", "update_busy_drops", "occupancy",
            "peak_occupancy",
        )
    )
    fields = physical + per_type
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["benchmark", *fields])
        for benchmark, _, _ in WORKLOADS:
            metrics = campaign.get((benchmark, "complete"))
            if metrics is None:
                writer.writerow([benchmark, *([""] * len(fields))])
            else:
                writer.writerow([benchmark, *(metrics.get(field, 0.0) for field in fields)])


def write_prefetch_table(path: Path, campaign):
    """Export the closed-loop predictor and prefetch evidence for Complete.

    This table intentionally keeps raw counters.  Ratios can then be recomputed
    without relying on a plotting choice or hiding workloads with no traffic.
    """
    # Reuse the same authoritative field sets as formal coverage validation
    # and standalone attribution. A hand-maintained subset previously omitted
    # timely local feedback and would have silently exported it as zero.
    fields = tuple(dict.fromkeys((
        *M1_PREFETCH_METRICS,
        *M2_REMOTE_METRICS,
        *M3_REMOTE_METRICS,
    )))
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "benchmark",
            "baseline_dram_physical_read_accesses",
            "complete_dram_physical_read_accesses",
            "baseline_dram_physical_write_accesses",
            "complete_dram_physical_write_accesses",
            *fields,
        ])
        for benchmark, _, _ in WORKLOADS:
            base = campaign.get((benchmark, "baseline"))
            complete = campaign.get((benchmark, "complete"))
            if base is None or complete is None:
                writer.writerow([benchmark, *([""] * (len(fields) + 4))])
                continue
            writer.writerow([
                benchmark,
                base.get("dram_physical_read_accesses", 0.0),
                complete.get("dram_physical_read_accesses", 0.0),
                base.get("dram_physical_write_accesses", 0.0),
                complete.get("dram_physical_write_accesses", 0.0),
                *(complete.get(field, 0.0) for field in fields),
            ])


def write_m1_attribution_table(path: Path, campaign):
    """Export Baseline-versus-M1 counters needed to audit local speculation.

    Complete counters cannot identify whether a resource-pressure change came
    from local prediction or from the remote mechanisms.  Keeping this table
    configuration-specific prevents that attribution error.
    """
    fields = (
        "dram_physical_read_accesses",
        "dram_physical_write_accesses",
        "l2_to_dram_64b_requests",
        "l2_demand_read_latency_samples",
        "l2_demand_read_latency_total_ns",
        "l2_demand_read_latency_max_ns",
        "l2_mshr_full_stall_cycles",
        "l2_miss_to_dram_issue_samples",
        "l2_miss_to_dram_issue_total_ns",
        "l2_miss_to_dram_issue_max_ns",
        "l2_fast_miss_to_dram_issue_samples",
        "l2_fast_miss_to_dram_issue_total_ns",
        "l2_fast_miss_to_dram_issue_max_ns",
        "l2_resident_filter_queries",
        "l2_resident_filter_read_bypasses",
        "l2_resident_filter_write_bypasses",
        *M1_ADAPTIVE_PAIR_METRICS,
        *M1_PREFETCH_METRICS,
    )
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            ["benchmark"]
            + [f"{config}_{field}" for config in ("baseline", "m1") for field in fields]
        )
        for benchmark, _, _ in WORKLOADS:
            values = []
            for config in ("baseline", "m1"):
                metrics = campaign.get((benchmark, config))
                if metrics is None:
                    values.extend([""] * len(fields))
                else:
                    values.extend(metrics.get(field, 0.0) for field in fields)
            writer.writerow([benchmark, *values])


def write_config_attribution_table(
    path: Path,
    campaign,
    config: str,
    fields: tuple[str, ...],
):
    """Export raw counters for one standalone mechanism configuration.

    M2 and M3 observe different request populations from Complete.  Keeping
    their raw counters in configuration-specific tables prevents a Complete
    interaction from being presented as standalone mechanism evidence.
    """
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["benchmark", *fields])
        for benchmark, _, _ in WORKLOADS:
            metrics = campaign.get((benchmark, config))
            if metrics is None:
                writer.writerow([benchmark, *([""] * len(fields))])
            else:
                writer.writerow(
                    [benchmark, *(metrics.get(field, 0.0) for field in fields)]
                )


def write_mechanism_effectiveness_table(path: Path, campaign):
    """Export the paper's direct mechanism-success and Filter-quality rates."""
    fields = (
        "m1_metric_source",
        "m1_local_l2_tag_lookups_skipped",
        "m1_pair_predictions",
        "m1_timely_buffer_hits",
        "m1_late_inflight_hits",
        "m1_useful_prefetches",
        "m1_prefetch_hit_rate",
        "m1_timely_prefetch_rate",
        "m1_retired_unused_prefetches",
        "m1_current_prefetches",
        "m1_prefetch_accounting_residual",
        "m1_filter_candidates",
        "m1_filter_lookups",
        "m1_exact_candidate_suppressions",
        "m1_filter_false_positives",
        "m2_metric_source",
        "m2_logical_remote_reads",
        "m2_duplicate_remote_reads",
        "m2_request_coalescing_rate",
        "m2_wire_lines",
        "m2_request_packets",
        "m2_packet_reduction_rate",
        "m2_average_lines_per_packet",
        "m3_metric_source",
        "m3_logical_remote_reads",
        "m3_requester_l2_hits",
        "m3_requester_l2_hit_fraction",
        "m3_installed_remote_fills",
        "m3_unused_remote_fills",
        "m3_local_line_displacements",
        "filter_queries",
        "filter_positives",
        "filter_negatives",
        "filter_false_positives",
        "filter_false_positives_per_query",
        "filter_false_positives_per_positive",
        "filter_insertions",
        "filter_insert_failures",
        "filter_fail_open_events",
        "filter_lookup_port_stalls",
        "filter_update_port_stalls",
        "filter_lookup_stall_fraction",
    )

    def get(metrics, name):
        return metrics.get(name, 0.0) if metrics is not None else 0.0

    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["benchmark", *fields])
        for benchmark, _, _ in WORKLOADS:
            m1 = campaign.get((benchmark, "m1"))
            m2 = campaign.get((benchmark, "m2"))
            m3 = campaign.get((benchmark, "m3"))
            complete = campaign.get((benchmark, "complete"))
            if all(item is None for item in (m1, m2, m3, complete)):
                writer.writerow([benchmark, *("" for _ in fields)])
                continue

            # A Baseline+Complete collection still contains all direct
            # mechanism counters. Prefer standalone cells when present for
            # isolated attribution, otherwise use Complete and state that
            # source explicitly so interactions are never hidden.
            m1_source = "m1" if m1 is not None else (
                "complete" if complete is not None else ""
            )
            m2_source = "m2" if m2 is not None else (
                "complete" if complete is not None else ""
            )
            m3_source = "m3" if m3 is not None else (
                "complete" if complete is not None else ""
            )
            if m1 is None:
                m1 = complete
            if m2 is None:
                m2 = complete
            if m3 is None:
                m3 = complete

            predictions = get(m1, "adaptive_pair_predictions")
            timely = get(m1, "adaptive_pair_buffer_hits")
            late = get(m1, "adaptive_pair_inflight_hits")
            useful = timely + late
            unused = get(m1, "adaptive_pair_prefetch_unused")
            current = get(m1, "adaptive_pair_current_prefetch_only_lines")
            resident_suppressions = get(
                m1, "adaptive_pair_resident_exact_suppressions"
            )
            pending_suppressions = get(
                m1, "adaptive_pair_pending_exact_suppressions"
            )
            resident_false = get(
                m1, "adaptive_pair_resident_false_positives"
            )
            pending_false = get(
                m1, "adaptive_pair_pending_false_positives"
            )

            m2_logical = get(m2, "remote_logical_reads")
            m2_duplicates = get(m2, "remote_duplicate_reads")
            m2_wire = get(m2, "remote_wire_lines")
            m2_single_packets = get(m2, "remote_single_packets")
            m2_bitmap_packets = get(m2, "remote_bitmap_packets")
            m2_packets = m2_single_packets + m2_bitmap_packets
            m2_packet_lines = m2_single_packets + get(
                m2, "remote_bitmap_lines"
            )

            m3_logical = get(m3, "remote_logical_reads")
            m3_hits = get(m3, "remote_requester_l2_hits")

            filter_types = (
                "resident", "pending", "seen", "pattern",
                "granularity_pending",
            )
            filter_queries = sum(
                get(complete, f"typed_filter_{kind}_queries")
                for kind in filter_types
            )
            filter_positives = sum(
                get(complete, f"typed_filter_{kind}_positives")
                for kind in filter_types
            )
            filter_negatives = sum(
                get(complete, f"typed_filter_{kind}_negatives")
                for kind in filter_types
            )
            filter_false_positives = sum(
                get(complete, f"typed_filter_{kind}_false_positives")
                for kind in filter_types
            )
            filter_insertions = sum(
                get(complete, f"typed_filter_{kind}_insertions")
                for kind in filter_types
            )
            filter_insert_failures = sum(
                get(complete, f"typed_filter_{kind}_insert_failures")
                for kind in filter_types
            )
            filter_fail_open = sum(
                get(complete, f"typed_filter_{kind}_fail_open")
                for kind in filter_types
            )
            lookup_stalls = get(complete, "typed_filter_lookup_port_stalls")
            update_stalls = get(complete, "typed_filter_update_port_stalls")

            values = (
                m1_source,
                get(m1, "l2_resident_filter_read_bypasses"),
                predictions,
                timely,
                late,
                useful,
                safe_ratio(useful, predictions),
                safe_ratio(timely, predictions),
                unused,
                current,
                predictions - useful - unused - current,
                get(m1, "adaptive_pair_filter_candidates"),
                get(m1, "adaptive_pair_filter_lookups"),
                resident_suppressions + pending_suppressions,
                resident_false + pending_false,
                m2_source,
                m2_logical,
                m2_duplicates,
                safe_ratio(m2_duplicates, m2_logical),
                m2_wire,
                m2_packets,
                safe_ratio(m2_wire - m2_packets, m2_wire),
                safe_ratio(m2_packet_lines, m2_packets),
                m3_source,
                m3_logical,
                m3_hits,
                safe_ratio(m3_hits, m3_logical),
                get(m3, "remote_l2_two_touch_installed_fills"),
                get(m3, "remote_requester_l2_unused_fills"),
                get(m3, "remote_two_touch_local_displacements"),
                filter_queries,
                filter_positives,
                filter_negatives,
                filter_false_positives,
                safe_ratio(filter_false_positives, filter_queries),
                safe_ratio(filter_false_positives, filter_positives),
                filter_insertions,
                filter_insert_failures,
                filter_fail_open,
                lookup_stalls,
                update_stalls,
                safe_ratio(lookup_stalls, filter_queries + lookup_stalls),
            )
            writer.writerow([benchmark, *values])


def percent_reduction(before: float, after: float):
    if before <= 0:
        return ""
    return 100.0 * (before - after) / before


def safe_ratio(numerator: float, denominator: float):
    if denominator <= 0:
        return ""
    return numerator / denominator


def write_work_table(path: Path, campaign):
    fields = (
        "l2_resident_filter_queries",
        "l2_tag_lookups_skipped",
        "preserved_mshr_merges",
        "parallel_mshr_merges",
        "primed_filter_lookups",
        "baseline_l2_mshr_full_stall_cycles",
        "complete_l2_mshr_full_stall_cycles",
        "baseline_l2_miss_to_dram_issue_samples",
        "baseline_l2_miss_to_dram_issue_total_ns",
        "baseline_l2_miss_to_dram_issue_avg_ns",
        "baseline_l2_miss_to_dram_issue_max_ns",
        "l2_miss_to_dram_issue_samples",
        "l2_miss_to_dram_issue_total_ns",
        "l2_miss_to_dram_issue_avg_ns",
        "l2_fast_miss_to_dram_issue_samples",
        "l2_fast_miss_to_dram_issue_total_ns",
        "l2_fast_miss_to_dram_issue_avg_ns",
        "l2_miss_to_dram_issue_max_ns",
        "complete_l2_combined_issue_samples",
        "complete_l2_combined_issue_total_ns",
        "complete_l2_combined_issue_avg_ns",
        "complete_l2_combined_issue_max_ns",
        "baseline_l2_to_dram_64b_requests",
        "complete_l2_to_dram_64b_requests",
        "baseline_dram_column_commands",
        "complete_dram_column_commands",
        "dram_column_commands_reduction_pct",
        "baseline_dram_activate_commands",
        "complete_dram_activate_commands",
        "dram_activate_commands_reduction_pct",
        "baseline_dram_precharge_commands",
        "complete_dram_precharge_commands",
        "dram_precharge_commands_reduction_pct",
        "complete_dram_auto_precharge_stops",
        "complete_dram_row_reuse_hits",
        "baseline_remote_reads",
        "complete_remote_logical_reads",
        "complete_remote_wire_lines",
        "complete_remote_demand_wire_lines",
        "complete_remote_prefetch_wire_lines",
        "remote_duplicate_reads_eliminated",
        "remote_exact_table_lookups",
        "remote_exact_table_lookups_avoided",
        "remote_collecting_merges",
        "remote_inflight_merges",
        "remote_ready_merges",
        "remote_cacheline_transactions_eliminated",
        "remote_wire_lines_reduction_pct",
        "complete_remote_packets",
        "remote_network_request_bytes",
        "remote_network_response_bytes",
        "remote_peak_line_entries",
        "remote_peak_waiter_entries",
        "remote_requester_issue_width_stalls",
        "remote_response_fanout_width_stalls",
        "remote_logical_read_latency_samples",
        "remote_logical_read_latency_total_ns",
        "remote_logical_read_latency_avg_ns",
        "remote_logical_read_latency_max_ns",
        "remote_batch_queue_wait_samples",
        "remote_batch_queue_wait_total_ns",
        "remote_batch_queue_wait_avg_ns",
        "remote_batch_queue_wait_max_ns",
        "remote_pre_network_wait_samples",
        "remote_pre_network_wait_total_ns",
        "remote_pre_network_wait_avg_ns",
        "remote_pre_network_wait_max_ns",
        "remote_probe_latency_samples",
        "remote_probe_latency_total_ns",
        "remote_probe_latency_avg_ns",
        "remote_probe_latency_max_ns",
        "remote_first_touch_lines",
        "remote_second_touch_admissions",
        "remote_multiple_demand_admissions",
        "remote_resident_filter_queries",
        "remote_resident_filter_positives",
        "remote_resident_filter_negatives",
        "remote_seen_filter_queries",
        "remote_seen_filter_hits",
        "remote_seen_filter_negatives",
        "remote_l2_probe_bypasses",
        "remote_l2_probe_hits",
        "remote_l2_probe_misses",
        "remote_l2_probe_elimination_pct",
        "remote_requester_l2_fills",
        "remote_requester_l2_hits",
        "repeated_wafer_traversals_eliminated",
        "remote_local_clean_protection_drops",
        "remote_unused_fills",
        "remote_write_uncacheable_skips",
    )
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["benchmark", *fields])
        for benchmark, _, _ in WORKLOADS:
            base = campaign.get((benchmark, "baseline"))
            complete = campaign.get((benchmark, "complete"))
            if base is None or complete is None:
                writer.writerow([benchmark, *([""] * len(fields))])
                continue
            miss_samples = complete.get("l2_miss_to_dram_issue_samples", 0.0)
            fast_samples = complete.get("l2_fast_miss_to_dram_issue_samples", 0.0)
            base_miss_samples = base.get(
                "l2_miss_to_dram_issue_samples", 0.0
            )
            base_miss_total = base.get(
                "l2_miss_to_dram_issue_total_ns", 0.0
            )
            miss_total = complete.get(
                "l2_miss_to_dram_issue_total_ns", 0.0
            )
            fast_total = complete.get(
                "l2_fast_miss_to_dram_issue_total_ns", 0.0
            )
            combined_issue_samples = miss_samples + fast_samples
            combined_issue_total = miss_total + fast_total
            remote_latency_samples = complete.get(
                "remote_logical_read_latency_samples", 0.0
            )
            remote_batch_samples = complete.get(
                "remote_batch_queue_wait_samples", 0.0
            )
            remote_network_samples = complete.get(
                "remote_pre_network_wait_samples", 0.0
            )
            remote_probe_samples = complete.get(
                "remote_probe_latency_samples", 0.0
            )
            base_columns = base.get("dram_row_column_commands", 0.0)
            complete_columns = complete.get("dram_row_column_commands", 0.0)
            base_activates = base.get("dram_row_activate_commands", 0.0)
            complete_activates = complete.get("dram_row_activate_commands", 0.0)
            base_precharges = base.get("dram_row_precharge_commands", 0.0)
            complete_precharges = complete.get("dram_row_precharge_commands", 0.0)
            base_dram = base.get("l2_to_dram_64b_requests", 0.0)
            complete_dram = complete.get("l2_to_dram_64b_requests", 0.0)
            base_remote = base.get("rdma_observed_remote_reads", 0.0)
            # Remote arrivals can change when latency changes the amount of
            # upstream MSHR merging.  Stage-local M2/M3 reductions therefore
            # use the Complete logical stream, not a timing-dependent Baseline
            # arrival count, as their denominator.
            complete_logical = complete.get("remote_logical_reads", 0.0)
            complete_wire = complete.get("remote_wire_lines", 0.0)
            complete_demand_wire = complete.get(
                "remote_demand_wire_lines", 0.0
            )
            complete_prefetch_wire = complete.get(
                "remote_prefetch_wire_lines", 0.0
            )
            l2_logical = complete.get("remote_l2_logical_responses", 0.0)
            l2_probe_bypasses = complete.get(
                "remote_l2_one_touch_probe_bypasses", 0.0
            )
            l2_probe_hits = complete.get("remote_l2_probe_hits", 0.0)
            l2_probe_misses = complete.get("remote_l2_probe_misses", 0.0)
            l2_probe_opportunities = (
                l2_probe_bypasses + l2_probe_hits + l2_probe_misses
            )
            duplicates = complete.get("remote_duplicate_reads", 0.0)
            for name, value in (
                ("demand wire lines", complete_demand_wire),
                ("duplicate reads", duplicates),
                ("requester-L2 responses", l2_logical),
            ):
                if value > complete_logical:
                    raise ValueError(
                        f"{benchmark}: {name} ({value}) exceeds Complete "
                        f"logical remote reads ({complete_logical})"
                    )
            if complete_wire != complete_demand_wire + complete_prefetch_wire:
                raise ValueError(
                    f"{benchmark}: total wire lines ({complete_wire}) != "
                    f"demand ({complete_demand_wire}) + prefetch "
                    f"({complete_prefetch_wire})"
                )
            writer.writerow([
                benchmark,
                complete.get("l2_resident_filter_queries", 0.0),
                complete.get("l2_resident_filter_read_bypasses", 0.0),
                complete.get("l2_resident_filter_read_negative_mshr_merges", 0.0) +
                complete.get("l2_resident_filter_read_parallel_mshr_merges", 0.0),
                complete.get("l2_resident_filter_read_parallel_mshr_merges", 0.0),
                complete.get("l2_resident_filter_primed_lookups", 0.0),
                base.get("l2_mshr_full_stall_cycles", 0.0),
                complete.get("l2_mshr_full_stall_cycles", 0.0),
                base_miss_samples,
                base_miss_total,
                safe_ratio(base_miss_total, base_miss_samples),
                base.get("l2_miss_to_dram_issue_max_ns", 0.0),
                miss_samples,
                miss_total,
                safe_ratio(miss_total, miss_samples),
                fast_samples,
                fast_total,
                safe_ratio(fast_total, fast_samples),
                complete.get("l2_miss_to_dram_issue_max_ns", 0.0),
                combined_issue_samples,
                combined_issue_total,
                safe_ratio(combined_issue_total, combined_issue_samples),
                max(
                    complete.get("l2_miss_to_dram_issue_max_ns", 0.0),
                    complete.get("l2_fast_miss_to_dram_issue_max_ns", 0.0),
                ),
                base_dram,
                complete_dram,
                base_columns,
                complete_columns,
                percent_reduction(base_columns, complete_columns),
                base_activates,
                complete_activates,
                percent_reduction(base_activates, complete_activates),
                base_precharges,
                complete_precharges,
                percent_reduction(base_precharges, complete_precharges),
                complete.get("dram_row_auto_precharge_stops", 0.0),
                complete.get("dram_row_reuse_hits", 0.0),
                base_remote,
                complete_logical,
                complete_wire,
                complete_demand_wire,
                complete_prefetch_wire,
                duplicates,
                complete.get("remote_exact_table_lookups", 0.0),
                complete.get("remote_exact_table_lookups_avoided", 0.0),
                complete.get("remote_pre_send_merges", 0.0),
                complete.get("remote_inflight_merges", 0.0),
                complete.get("remote_ready_merges", 0.0),
                complete_logical - complete_demand_wire,
                percent_reduction(complete_logical, complete_demand_wire),
                complete.get("remote_single_packets", 0.0) + complete.get("remote_bitmap_packets", 0.0),
                complete.get("remote_network_request_bytes", 0.0),
                complete.get("remote_network_response_bytes", 0.0),
                complete.get("remote_peak_line_entries", 0.0),
                complete.get("remote_peak_waiter_entries", 0.0),
                complete.get("remote_requester_issue_width_stalls", 0.0),
                complete.get("remote_response_fanout_width_stalls", 0.0),
                remote_latency_samples,
                complete.get("remote_logical_read_latency_total_ns", 0.0),
                safe_ratio(
                    complete.get("remote_logical_read_latency_total_ns", 0.0),
                    remote_latency_samples,
                ),
                complete.get("remote_logical_read_latency_max_ns", 0.0),
                remote_batch_samples,
                complete.get("remote_batch_queue_wait_total_ns", 0.0),
                safe_ratio(
                    complete.get("remote_batch_queue_wait_total_ns", 0.0),
                    remote_batch_samples,
                ),
                complete.get("remote_batch_queue_wait_max_ns", 0.0),
                remote_network_samples,
                complete.get("remote_pre_network_wait_total_ns", 0.0),
                safe_ratio(
                    complete.get("remote_pre_network_wait_total_ns", 0.0),
                    remote_network_samples,
                ),
                complete.get("remote_pre_network_wait_max_ns", 0.0),
                remote_probe_samples,
                complete.get("remote_probe_latency_total_ns", 0.0),
                safe_ratio(
                    complete.get("remote_probe_latency_total_ns", 0.0),
                    remote_probe_samples,
                ),
                complete.get("remote_probe_latency_max_ns", 0.0),
                complete.get("remote_first_touch_lines", 0.0),
                complete.get("remote_second_touch_admissions", 0.0),
                complete.get("remote_multiple_demand_admissions", 0.0),
                complete.get("remote_resident_queries", 0.0),
                complete.get("remote_resident_positives", 0.0),
                complete.get("remote_resident_negatives", 0.0),
                complete.get("remote_seen_queries", 0.0),
                complete.get("remote_seen_hits", 0.0),
                complete.get("remote_seen_negatives", 0.0),
                l2_probe_bypasses,
                l2_probe_hits,
                l2_probe_misses,
                percent_reduction(l2_probe_opportunities, l2_probe_hits + l2_probe_misses),
                complete.get("remote_installed_fills", 0.0),
                complete.get("remote_requester_l2_hits", 0.0),
                l2_logical,
                complete.get("remote_local_clean_protection_drops", 0.0),
                complete.get("remote_requester_l2_unused_fills", 0.0),
                complete.get("remote_reuse_write_uncacheable_skips", 0.0),
            ])


def write_summary(path: Path, speedup):
    lines = [
        "# CuPath typed-filter ablation summary",
        "",
        f"Classification threshold was fixed before the full run: positive > {POSITIVE_THRESHOLD:.3f}x; negative < {NEGATIVE_THRESHOLD:.3f}x; otherwise neutral.",
        "",
        "| Configuration | Available | Geomean | Positive | Neutral | Negative |",
        "|---|---:|---:|---:|---:|---:|",
    ]
    for config, label, _ in CONFIGS:
        values = [
            speedup[(benchmark, config)]
            for benchmark, _, _ in WORKLOADS
            if (benchmark, config) in speedup
        ]
        gm = geomean(values)
        positive = sum(v > POSITIVE_THRESHOLD for v in values)
        negative = sum(v < NEGATIVE_THRESHOLD for v in values)
        neutral = len(values) - positive - negative
        gm_text = f"{gm:.4f}x" if gm is not None else "--"
        if len(values) != len(WORKLOADS):
            gm_text += " (partial; not paper geomean)"
        lines.append(
            f"| {label} | {len(values)}/14 | {gm_text} | {positive} | {neutral} | {negative} |"
        )
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def plot(path: Path, speedup):
    labels = [label for _, label, _ in WORKLOADS] + ["GM"]
    x = np.arange(len(labels), dtype=float)
    width = 0.125
    fig, ax = plt.subplots(figsize=(3.45, 1.78))
    gm_index = len(WORKLOADS)
    ax.axvspan(gm_index - 0.44, gm_index + 0.44, color="#F2F2F2", zorder=0)
    for index, (config, label, color) in enumerate(CONFIGS):
        values = [speedup.get((benchmark, config), np.nan) for benchmark, _, _ in WORKLOADS]
        finite_values = [value for value in values if math.isfinite(value)]
        values.append(
            geomean(finite_values)
            if len(finite_values) == len(WORKLOADS)
            else np.nan
        )
        ax.bar(
            x + (index - 2) * width,
            values,
            width=width * 0.94,
            label=label,
            color=color,
            edgecolor="none",
            linewidth=0,
            zorder=3,
        )
    ax.axhline(1.0, color="#4C4C4D", linewidth=0.65, zorder=2)
    ax.set_ylabel("Speedup", fontsize=6.4)
    ax.set_xticks(x)
    ax.set_xticklabels(labels, rotation=32, ha="right", rotation_mode="anchor", fontsize=4.8)
    ax.tick_params(axis="y", labelsize=5.2, length=1.8, width=0.55)
    ax.tick_params(axis="x", length=1.6, width=0.55, pad=1)
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.4, zorder=0)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.55)
    ax.legend(
        ncol=5, frameon=False, fontsize=4.7, loc="lower center",
        bbox_to_anchor=(0.5, 1.11), columnspacing=0.55, handlelength=0.85,
        handletextpad=0.25, borderaxespad=0,
    )
    finite = [value for value in speedup.values() if math.isfinite(value)]
    if finite:
        lower = min(0.9, max(0.0, min(finite) - 0.04))
        upper = max(1.08, max(finite) + 0.04)
        ax.set_ylim(lower, upper)
    ax.set_xlim(-0.48, len(labels) - 0.52)
    fig.tight_layout(pad=0.22)
    fig.savefig(path, dpi=300, bbox_inches="tight", pad_inches=0.025,
                facecolor="white", transparent=False)
    plt.close(fig)


def plot_baseline_complete(path: Path, speedup):
    """Plot only the two configurations executed by the observation campaign."""
    labels = [label for _, label, _ in WORKLOADS] + ["GM"]
    complete = [
        speedup.get((benchmark, "complete"), np.nan)
        for benchmark, _, _ in WORKLOADS
    ]
    finite = [value for value in complete if math.isfinite(value)]
    complete.append(
        geomean(finite) if len(finite) == len(WORKLOADS) else np.nan
    )
    baseline = [1.0] * len(labels)
    x = np.arange(len(labels), dtype=float)
    width = 0.31
    fig, ax = plt.subplots(figsize=(3.45, 1.78))
    gm_index = len(WORKLOADS)
    ax.axvspan(gm_index - 0.44, gm_index + 0.44, color="#F2F2F2", zorder=0)
    ax.bar(
        x - width / 2, baseline, width=width, label="Baseline",
        color="#D6EFF5", edgecolor="none", linewidth=0, zorder=3,
    )
    ax.bar(
        x + width / 2, complete, width=width, label="CuPath",
        color="#F4A371", edgecolor="none", linewidth=0, zorder=3,
    )
    ax.axhline(1.0, color="#4C4C4D", linewidth=0.65, zorder=2)
    ax.set_ylabel("Normalized performance", fontsize=6.4)
    ax.set_xticks(x)
    ax.set_xticklabels(
        labels, rotation=32, ha="right", rotation_mode="anchor", fontsize=4.8
    )
    ax.tick_params(axis="y", labelsize=5.2, length=1.8, width=0.55)
    ax.tick_params(axis="x", length=1.6, width=0.55, pad=1)
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.4, zorder=0)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.55)
    ax.legend(
        ncol=2, frameon=False, fontsize=4.9, loc="lower center",
        bbox_to_anchor=(0.5, 1.11), columnspacing=0.8, handlelength=0.9,
        handletextpad=0.3, borderaxespad=0,
    )
    if finite:
        lower = min(0.9, max(0.0, min(finite) - 0.04))
        upper = max(1.08, max(finite) + 0.04)
        ax.set_ylim(lower, upper)
    ax.set_xlim(-0.48, len(labels) - 0.52)
    fig.tight_layout(pad=0.22)
    fig.savefig(
        path, dpi=300, bbox_inches="tight", pad_inches=0.025,
        facecolor="white", transparent=False,
    )
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument(
        "--additional-results",
        type=Path,
        action="append",
        default=[],
        help="merge disjoint formal cells from another result directory",
    )
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    root = args.results.resolve()
    roots = [root, *(path.resolve() for path in args.additional_results)]
    output = (args.output_dir or root).resolve()
    output.mkdir(parents=True, exist_ok=True)
    campaign, paths = load_campaign_roots(roots)
    validate_complete_metric_coverage(campaign)
    gains = speedups(campaign)
    traffic_classes = load_traffic_classes(
        output / "cupath_baseline_traffic_classification.csv"
    )
    write_speedup_table(
        output / "cupath_speedup_table.csv", campaign, gains,
        traffic_classes,
    )
    write_baseline_complete_speedup_table(
        output / "cupath_baseline_complete_speedup.csv", campaign, gains
    )
    write_filter_table(output / "cupath_filter_statistics.csv", campaign)
    write_work_table(output / "cupath_work_reduction.csv", campaign)
    write_prefetch_table(output / "cupath_prefetch_statistics.csv", campaign)
    write_m1_attribution_table(output / "cupath_m1_attribution.csv", campaign)
    write_mechanism_effectiveness_table(
        output / "cupath_mechanism_effectiveness.csv", campaign
    )
    write_config_attribution_table(
        output / "cupath_m2_attribution.csv",
        campaign,
        "m2",
        (
            "dram_physical_read_accesses",
            "dram_physical_write_accesses",
            *M2_REMOTE_METRICS,
        ),
    )
    write_config_attribution_table(
        output / "cupath_m3_attribution.csv",
        campaign,
        "m3",
        (
            "dram_physical_read_accesses",
            "dram_physical_write_accesses",
            *M3_REMOTE_METRICS,
        ),
    )
    write_runtime_table(
        output / "cupath_simulator_runtime.csv", paths, campaign
    )
    write_execution_audit(
        output / "cupath_execution_audit.csv", campaign, paths
    )
    if len(campaign) == len(WORKLOADS) * len(CONFIGS) and len(roots) == 1:
        mapping_rows = audit_wg_mapping(
            root, [benchmark for benchmark, _, _ in WORKLOADS]
        )
        write_wg_mapping_outputs(output, mapping_rows)
    write_workload_footprint_table(
        output / "cupath_workload_footprints.csv", campaign
    )
    write_summary(output / "cupath_ablation_summary.md", gains)
    plot(output / "cupath_typed_ablation.png", gains)
    plot_baseline_complete(output / "cupath_baseline_complete_speedup.png", gains)
    print(
        f"loaded {len(campaign)}/70 formal result cells from "
        + ", ".join(str(path) for path in roots)
    )
    print(f"wrote CuPath tables and plot to {output}")


if __name__ == "__main__":
    main()
