#!/usr/bin/env python3

"""Summarize M1 paired-read work and validate physical-HBM accounting."""

import argparse
import csv
import hashlib
import json
import math
from collections import defaultdict
from pathlib import Path


SUM_METRICS = (
    "granularity_adaptation_enabled",
    "granularity_without_filter",
    "granularity_always_expand",
    "granularity_predictor_only",
    "granularity_real_read_demands",
    "granularity_patterns_established",
    "granularity_pattern_insert_drops",
    "granularity_pattern_filter_positives",
    "granularity_pattern_negative_drops",
    "granularity_predictor_throttled_drops",
    "granularity_predicted_candidates",
    "granularity_sibling_candidates",
    "granularity_predictor_only_candidates",
    "granularity_candidate_not_sibling_drops",
    "granularity_page_boundary_drops",
    "granularity_wrong_slice_drops",
    "granularity_wrong_controller_drops",
    "granularity_accepted_aggregates",
    "granularity_demand_pair_ready_opportunities",
    "granularity_demand_pair_filter_probes",
    "granularity_demand_pair_filter_positives",
    "granularity_demand_pair_filter_negatives",
    "granularity_demand_pair_aggregates",
    "granularity_demand_pair_resource_drops",
    "granularity_expansion_attempts",
    "granularity_frontend_single_64b_descriptors",
    "granularity_frontend_paired_read_aggregates",
    "granularity_frontend_read_bytes",
    "granularity_resident_negative_lookup_skips",
    "granularity_pending_negative_lookup_skips",
    "granularity_resident_exact_lookups",
    "granularity_pending_exact_lookups",
    "granularity_resident_exact_suppressions",
    "granularity_pending_exact_suppressions",
    "granularity_resident_filter_positives",
    "granularity_pending_filter_positives",
    "granularity_resident_false_positives",
    "granularity_pending_false_positives",
    "granularity_filter_busy_drops",
    "granularity_filter_not_ready_drops",
    "granularity_filter_unreliable_drops",
    "granularity_pending_insert_drops",
    "granularity_demand_pending_insert_failures",
    "granularity_mshr_pressure_drops",
    "granularity_inflight_capacity_drops",
    "granularity_dram_queue_pressure_drops",
    "granularity_victim_unavailable_drops",
    "granularity_remote_victim_protection_drops",
    "granularity_clean_victim_displacements",
    "granularity_sibling_fills",
    "granularity_sibling_inflight_merges",
    "granularity_sibling_l2_hits",
    "granularity_useful_sibling_lines",
    "granularity_timely_sibling_lines",
    "granularity_late_sibling_lines",
    "granularity_unused_sibling_lines",
    "granularity_unused_sibling_evictions",
    "granularity_unused_sibling_reset_retires",
    "granularity_current_sibling_only_lines",
    "granularity_peak_sibling_only_lines",
    "granularity_useful_sibling_bytes",
    "granularity_wasted_sibling_bytes",
    "granularity_predictor_capacity",
    "granularity_predictor_real_demands",
    "granularity_predictor_candidates",
    "granularity_predictor_patterns",
    "granularity_predictor_pattern_replacements",
    "granularity_predictor_pattern_insert_fails",
    "granularity_predictor_evidence_one",
    "granularity_predictor_evidence_two",
    "granularity_predictor_useful_feedback",
    "granularity_predictor_unused_feedback",
    "granularity_predictor_timely_feedback",
    "granularity_predictor_late_feedback",
    "granularity_predictor_stale_feedback_ignored",
    "granularity_predictor_candidate_lookahead_total",
    "granularity_predictor_candidate_lookahead_max",
    "granularity_predictor_stride_one",
    "granularity_predictor_stride_small",
    "granularity_predictor_stride_medium",
    "granularity_predictor_stride_large",
    "granularity_predictor_stride_negative",
    "l2_fill_forwarding_enabled",
    "l2_fill_forwarding_eligible_read_entries",
    "l2_fill_forwarding_forwarded_read_entries",
    "l2_fill_forwarding_forwarded_reads",
    "l2_fill_forwarding_buffer_fallbacks",
    "l2_resident_filter_enabled",
    "l2_resident_filter_reliable",
    "l2_resident_filter_queries",
    "l2_resident_filter_positives",
    "l2_resident_filter_negatives",
    "l2_resident_filter_read_bypasses",
    "l2_resident_filter_read_positive_fast_paths",
    "l2_resident_filter_read_busy_fallbacks",
    "l2_resident_filter_read_negative_mshr_merges",
    "l2_resident_filter_read_parallel_mshr_merges",
    "l2_resident_filter_primed_lookups",
    "l2_resident_filter_write_bypasses",
    "l2_resident_filter_write_full_line_bypasses",
    "l2_resident_filter_write_partial_bypasses",
    "l2_resident_filter_false_positives",
    "l2_resident_filter_insert_failures",
    "l2_mshr_full_stall_cycles",
    "l2_to_dram_64b_requests",
    "l2_miss_to_dram_issue_samples",
    "l2_miss_to_dram_issue_total_ns",
    "l2_miss_to_dram_issue_max_ns",
    "l2_fast_miss_to_dram_issue_samples",
    "l2_fast_miss_to_dram_issue_total_ns",
    "l2_fast_miss_to_dram_issue_max_ns",
    "l2_demand_read_latency_samples",
    "l2_demand_read_latency_total_ns",
    "l2_demand_read_latency_max_ns",
    "dram_frontend_read_requests",
    "dram_frontend_write_requests",
    "dram_frontend_read_bytes",
    "dram_frontend_write_bytes",
    "dram_paired_read_descriptors",
    "dram_paired_read_members",
    "dram_paired_read_demand_members",
    "dram_paired_read_sibling_members",
    "dram_physical_read_accesses",
    "dram_physical_write_accesses",
    "dram_row_column_commands",
    "dram_row_reuse_hits",
    "dram_row_activate_commands",
    "dram_row_precharge_commands",
    "dram_row_continuation_enabled",
    "dram_aggregate_continuation_enabled",
    "dram_row_commands_issued",
    "dram_row_auto_precharge_stops",
    "dram_row_max_queue_age_cycles",
    "dram_aggregate_auto_precharge_stops",
    "dram_aggregate_immediate_continuations",
    "typed_filter_lookup_port_stalls",
    "typed_filter_update_port_stalls",
    "typed_filter_occupancy",
    "typed_filter_peak_occupancy",
    "typed_filter_resident_queries",
    "typed_filter_resident_positives",
    "typed_filter_resident_negatives",
    "typed_filter_resident_false_positives",
    "typed_filter_resident_insert_failures",
    "typed_filter_resident_occupancy",
    "typed_filter_resident_peak_occupancy",
    "typed_filter_pending_queries",
    "typed_filter_pending_positives",
    "typed_filter_pending_negatives",
    "typed_filter_pending_false_positives",
    "typed_filter_pending_insert_failures",
    "typed_filter_pending_occupancy",
    "typed_filter_pending_peak_occupancy",
    "typed_filter_seen_queries",
    "typed_filter_seen_positives",
    "typed_filter_seen_negatives",
    "typed_filter_seen_false_positives",
    "typed_filter_seen_insert_failures",
    "typed_filter_seen_occupancy",
    "typed_filter_seen_peak_occupancy",
    "typed_filter_pattern_queries",
    "typed_filter_pattern_positives",
    "typed_filter_pattern_negatives",
    "typed_filter_pattern_false_positives",
    "typed_filter_pattern_insert_failures",
    "typed_filter_pattern_occupancy",
    "typed_filter_pattern_peak_occupancy",
    "typed_filter_granularity_pending_queries",
    "typed_filter_granularity_pending_positives",
    "typed_filter_granularity_pending_negatives",
    "typed_filter_granularity_pending_false_positives",
    "typed_filter_granularity_pending_insert_failures",
    "typed_filter_granularity_pending_occupancy",
    "typed_filter_granularity_pending_peak_occupancy",
    "typed_filter_resident_reliable",
    "typed_filter_resident_insertions",
    "typed_filter_resident_deletes",
    "typed_filter_resident_fail_open",
    "typed_filter_resident_lookup_busy_drops",
    "typed_filter_resident_update_busy_drops",
    "typed_filter_pending_reliable",
    "typed_filter_pending_insertions",
    "typed_filter_pending_deletes",
    "typed_filter_pending_fail_open",
    "typed_filter_pending_lookup_busy_drops",
    "typed_filter_pending_update_busy_drops",
    "typed_filter_seen_reliable",
    "typed_filter_seen_insertions",
    "typed_filter_seen_deletes",
    "typed_filter_seen_fail_open",
    "typed_filter_seen_lookup_busy_drops",
    "typed_filter_seen_update_busy_drops",
    "typed_filter_pattern_reliable",
    "typed_filter_pattern_insertions",
    "typed_filter_pattern_deletes",
    "typed_filter_pattern_fail_open",
    "typed_filter_pattern_lookup_busy_drops",
    "typed_filter_pattern_update_busy_drops",
    "typed_filter_granularity_pending_reliable",
    "typed_filter_granularity_pending_insertions",
    "typed_filter_granularity_pending_deletes",
    "typed_filter_granularity_pending_fail_open",
    "typed_filter_granularity_pending_lookup_busy_drops",
    "typed_filter_granularity_pending_update_busy_drops",
)


# Generic row continuation is intentionally absent from every formal and
# diagnostic cell. M1 receives its strictly narrower PairID-only behavior from
# AggregateContinuation, which the builder derives from the granularity mode.
GENERAL_ROW_CONTINUATION_BY_CONFIG = {
    "baseline": False,
    "old_m1_independent_prefetch": False,
    "cuckoo_filter_only": False,
    "m1_bypass_fill_only": False,
    "m1_bypass_fill_predictor_only": False,
    "always_pair": False,
    "predictor_only": False,
    "paired_read_without_filter": False,
    "new_m1": False,
    "m1": False,
    "m2": False,
    "m3": False,
    "complete": False,
}

AGGREGATE_CONTINUATION_CONFIGS = {
    "always_pair", "predictor_only", "paired_read_without_filter",
    "new_m1", "m1", "complete", "m1_bypass_fill_predictor_only",
}


GRANULARITY_MODE_BY_CONFIG = {
    "baseline": (False, False, False, False),
    "old_m1_independent_prefetch": (False, False, False, False),
    "cuckoo_filter_only": (False, False, False, False),
    "m1_bypass_fill_only": (False, False, False, False),
    "m1_bypass_fill_predictor_only": (True, False, False, True),
    "always_pair": (True, False, True, False),
    "predictor_only": (True, False, False, True),
    "paired_read_without_filter": (True, True, False, False),
    "new_m1": (True, False, False, False),
    "m1": (True, False, False, False),
    "m2": (False, False, False, False),
    "m3": (False, False, False, False),
    "complete": (True, False, False, False),
}


def audit_general_row_continuation(config, command):
    if config not in GENERAL_ROW_CONTINUATION_BY_CONFIG:
        return
    prefix = "-dram-row-continuation-enable="
    observed = [token for token in command if token.startswith(prefix)]
    expected = (
        prefix + str(GENERAL_ROW_CONTINUATION_BY_CONFIG[config]).lower()
    )
    if observed != [expected]:
        raise ValueError(
            f"{config} general-row-continuation mismatch: "
            f"expected {expected}, found {observed}"
        )


def read_metrics(path):
    totals = defaultdict(float)
    filters = {}
    dram_controllers = set()
    physical_access_bytes = set()
    with Path(path).open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            where = row["where"].strip()
            what = row["what"].strip()
            value = float(row["value"])
            if what in SUM_METRICS:
                totals[what] += value
            elif where == "Driver" and what == "total_time":
                totals["driver_total_time"] = value
            elif what == "dram_physical_access_bytes" and value > 0:
                physical_access_bytes.add(value)
                dram_controllers.add(where)
            if what == "typed_filter_storage_bits" and value > 0:
                filters[where] = value
    totals["typed_filter_count"] = float(len(filters))
    totals["typed_filter_total_storage_bits"] = sum(filters.values())
    totals["dram_controller_count"] = float(len(dram_controllers))
    totals["dram_physical_access_unit_bytes"] = (
        physical_access_bytes.pop() if len(physical_access_bytes) == 1 else 0.0
    )
    totals["dram_physical_access_unit_consistent"] = float(
        len(physical_access_bytes) == 0
        and totals["dram_physical_access_unit_bytes"] > 0
    )
    return totals


def positive_max_wg(flags):
    for flag in flags:
        if flag.startswith("-max-wg="):
            return int(flag.split("=", 1)[1])
    return 0


def launch_signature(launches):
    """Hash the complete launch descriptor available in the passive trace."""
    stable = [{
        "requested_total_wg": int(launch.get("requested_total_wg", 0)),
        "unified": bool(launch.get("unified")),
        "packet_addresses": launch.get("packet_addresses", []),
        "partitions": launch.get("partitions", []),
        "wg_filter_kind": launch.get("wg_filter_kind", ""),
    } for launch in launches]
    encoded = json.dumps(
        stable, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def summarize_cell(result_path):
    result = json.loads(Path(result_path).read_text(encoding="utf-8"))
    exp = result.get("exp")
    if exp is None:
        exp = {
            "target": result["target"],
            "benchmark": result["benchmark"],
            "config_name": result["configuration"],
            "common_flags": [],
        }
    metrics_path = result.get("metrics")
    metrics = (
        read_metrics(metrics_path)
        if metrics_path else defaultdict(float)
    )
    paired = metrics["granularity_frontend_paired_read_aggregates"]
    frontend = metrics["dram_frontend_read_requests"]
    physical = metrics["dram_physical_read_accesses"]
    paired_descriptors = metrics["dram_paired_read_descriptors"]
    paired_members = metrics["dram_paired_read_members"]
    paired_demands = metrics["dram_paired_read_demand_members"]
    paired_siblings = metrics["dram_paired_read_sibling_members"]
    fills = metrics["granularity_sibling_fills"]
    useful = metrics["granularity_useful_sibling_lines"]
    retired_unused = metrics["granularity_unused_sibling_lines"]
    terminal_resident_unused = metrics[
        "granularity_current_sibling_only_lines"
    ]
    # Reporting happens after the simulator has drained all requests but
    # before a final cache flush.  A sibling-only line still resident at that
    # point cannot be consumed by this workload and is therefore terminally
    # unused.  Keep the raw counters visible and derive the workload-level
    # value here rather than mutating cache state during reporting.
    terminal_unused = retired_unused + terminal_resident_unused
    terminal_wasted_bytes = (
        metrics["granularity_wasted_sibling_bytes"]
        + terminal_resident_unused * 64.0
    )
    exact_lookups = (
        metrics["granularity_resident_exact_lookups"]
        + metrics["granularity_pending_exact_lookups"]
    )
    lookup_skips = (
        metrics["granularity_resident_negative_lookup_skips"]
        + metrics["granularity_pending_negative_lookup_skips"]
    )
    physical_unit = metrics["dram_physical_access_unit_bytes"]
    config_name = exp["config_name"]
    controller_count = metrics["dram_controller_count"]
    expected_aggregate_enabled = (
        config_name in AGGREGATE_CONTINUATION_CONFIGS
    )
    aggregate_enabled_count = metrics[
        "dram_aggregate_continuation_enabled"
    ]
    expected_granularity_mode = GRANULARITY_MODE_BY_CONFIG.get(config_name)
    observed_granularity_mode = (
        metrics["granularity_adaptation_enabled"],
        metrics["granularity_without_filter"],
        metrics["granularity_always_expand"],
        metrics["granularity_predictor_only"],
    )
    l2_slice_count = metrics["typed_filter_count"]
    expected_fill_forwarding = config_name in {
        "m1", "complete", "new_m1", "m1_bypass_fill_only",
        "m1_bypass_fill_predictor_only",
    }
    fill_forwarding_enabled_count = metrics["l2_fill_forwarding_enabled"]
    fill_eligible = metrics["l2_fill_forwarding_eligible_read_entries"]
    fill_forwarded = metrics["l2_fill_forwarding_forwarded_read_entries"]
    fill_fallbacks = metrics["l2_fill_forwarding_buffer_fallbacks"]
    resident_read_bypasses = metrics["l2_resident_filter_read_bypasses"]
    resident_positive_fast_paths = metrics[
        "l2_resident_filter_read_positive_fast_paths"
    ]
    resident_read_busy_fallbacks = metrics[
        "l2_resident_filter_read_busy_fallbacks"
    ]
    miss_issue_samples = metrics["l2_miss_to_dram_issue_samples"]
    fast_miss_issue_samples = metrics[
        "l2_fast_miss_to_dram_issue_samples"
    ]
    demand_latency_samples = metrics["l2_demand_read_latency_samples"]
    expected_granularity_counts = (
        tuple(
            l2_slice_count if enabled else 0
            for enabled in expected_granularity_mode
        )
        if expected_granularity_mode is not None else None
    )
    mapping = result.get("wg_mapping", {})
    launches = mapping.get("launches", [])
    requested_wg = sum(
        int(launch.get("requested_total_wg", 0)) for launch in launches
    )
    row = {
        "target": exp["target"],
        "benchmark": exp["benchmark"],
        "config": config_name,
        "success": int(bool(result.get("success"))),
        "wg_mapping_present": int(bool(mapping)),
        "wg_global_set_sha256": mapping.get("global_wg_set_sha256", ""),
        "wg_observed_count": int(mapping.get("observed_wg_count", 0)),
        "wg_completed_count": int(mapping.get("completed_wg_count", 0)),
        "wg_requested_count": requested_wg,
        "wg_kernel_count": int(mapping.get(
            "executed_kernel_count", len(launches))),
        "wg_launch_signature": launch_signature(launches),
        "wg_observed_sampling_coverage": float(mapping.get(
            "observed_sampling_coverage", 0)),
        "wg_completed_sampling_coverage": float(mapping.get(
            "completed_sampling_coverage", 0)),
        "wg_stop_reason": mapping.get("stop_reason", ""),
        "diagnostic_prefix_wg": (
            positive_max_wg(exp.get("common_flags", []))
            or int(result.get("wg_mapping", {}).get("max_wg", 0))
        ),
        **{name: metrics[name] for name in SUM_METRICS},
        "typed_filter_count": metrics["typed_filter_count"],
        "dram_controller_count": controller_count,
        "typed_filter_total_storage_bits": metrics[
            "typed_filter_total_storage_bits"
        ],
        "dram_physical_access_unit_bytes": physical_unit,
        "dram_physical_access_unit_consistent": int(
            metrics["dram_physical_access_unit_consistent"]
        ),
        "general_row_continuation_disabled": int(
            metrics["dram_row_continuation_enabled"] == 0
        ),
        "aggregate_continuation_mode_ok": int(
            aggregate_enabled_count == (
                controller_count if expected_aggregate_enabled else 0
            )
        ),
        "granularity_runtime_mode_ok": int(
            expected_granularity_counts is not None
            and observed_granularity_mode == expected_granularity_counts
        ),
        "fill_forwarding_runtime_mode_ok": int(
            fill_forwarding_enabled_count == (
                l2_slice_count if expected_fill_forwarding else 0
            )
        ),
        "fill_forwarding_entry_coverage": (
            fill_forwarded / fill_eligible if fill_eligible else 0.0
        ),
        "fill_forwarding_buffer_fallback_fraction": (
            fill_fallbacks / fill_eligible if fill_eligible else 0.0
        ),
        "resident_negative_read_bypass_coverage": (
            resident_read_bypasses /
            (resident_read_bypasses + resident_read_busy_fallbacks)
            if resident_read_bypasses + resident_read_busy_fallbacks else 0.0
        ),
        "resident_positive_read_fast_path_coverage": (
            resident_positive_fast_paths /
            metrics["l2_resident_filter_positives"]
            if metrics["l2_resident_filter_positives"] else 0.0
        ),
        "resident_filter_negative_fraction": (
            metrics["l2_resident_filter_negatives"] /
            metrics["l2_resident_filter_queries"]
            if metrics["l2_resident_filter_queries"] else 0.0
        ),
        "fast_miss_issue_fraction": (
            fast_miss_issue_samples / miss_issue_samples
            if miss_issue_samples else 0.0
        ),
        "l2_miss_to_dram_issue_avg_ns": (
            metrics["l2_miss_to_dram_issue_total_ns"] / miss_issue_samples
            if miss_issue_samples else 0.0
        ),
        "l2_fast_miss_to_dram_issue_avg_ns": (
            metrics["l2_fast_miss_to_dram_issue_total_ns"] /
            fast_miss_issue_samples
            if fast_miss_issue_samples else 0.0
        ),
        "l2_demand_read_latency_avg_ns": (
            metrics["l2_demand_read_latency_total_ns"] /
            demand_latency_samples
            if demand_latency_samples else 0.0
        ),
        "dram_physical_read_bytes": physical * physical_unit,
        "dram_physical_write_bytes": (
            metrics["dram_physical_write_accesses"] * physical_unit
        ),
        "driver_total_time": metrics["driver_total_time"],
        "granularity_terminal_unused_sibling_lines": terminal_unused,
        "granularity_terminal_wasted_sibling_bytes": terminal_wasted_bytes,
        "sibling_accuracy": useful / fills if fills else 0.0,
        "predictor_candidate_coverage": (
            metrics["granularity_predicted_candidates"]
            / metrics["granularity_real_read_demands"]
            if metrics["granularity_real_read_demands"] else 0.0
        ),
        "sibling_candidate_coverage": (
            metrics["granularity_sibling_candidates"]
            / metrics["granularity_real_read_demands"]
            if metrics["granularity_real_read_demands"] else 0.0
        ),
        "aggregate_acceptance_rate": (
            paired / metrics["granularity_expansion_attempts"]
            if metrics["granularity_expansion_attempts"] else 0.0
        ),
        "demand_pair_filter_positive_fraction": (
            metrics["granularity_demand_pair_filter_positives"] /
            (metrics["granularity_demand_pair_filter_positives"]
             + metrics["granularity_demand_pair_filter_negatives"])
            if (metrics["granularity_demand_pair_filter_positives"]
                + metrics["granularity_demand_pair_filter_negatives"])
            else 0.0
        ),
        "demand_pair_filter_exact_avoidance": (
            metrics["granularity_demand_pair_filter_negatives"] /
            (metrics["granularity_demand_pair_filter_positives"]
             + metrics["granularity_demand_pair_filter_negatives"])
            if (metrics["granularity_demand_pair_filter_positives"]
                + metrics["granularity_demand_pair_filter_negatives"])
            else 0.0
        ),
        "sibling_waste_fraction": terminal_unused / fills if fills else 0.0,
        "sibling_timely_fraction": (
            metrics["granularity_timely_sibling_lines"] / useful
            if useful else 0.0
        ),
        "sibling_late_fraction": (
            metrics["granularity_late_sibling_lines"] / useful
            if useful else 0.0
        ),
        "sibling_timeliness_partition_ok": int(
            metrics["granularity_timely_sibling_lines"]
            + metrics["granularity_late_sibling_lines"] == useful
        ),
        "sibling_terminal_partition_ok": int(
            useful + terminal_unused == fills
        ),
        "sibling_terminal_accounting_bounded": int(
            fills <= paired_descriptors
            and terminal_unused <= fills
            and useful + terminal_unused <= paired_descriptors
        ),
        "filter_exact_lookup_total": exact_lookups,
        "filter_negative_lookup_skip_total": lookup_skips,
        "filter_exact_lookup_avoidance": (
            lookup_skips / (lookup_skips + exact_lookups)
            if lookup_skips + exact_lookups else 0.0
        ),
        "paired_row_reuse_rate": (
            metrics["dram_aggregate_auto_precharge_stops"] / paired
            if paired else 0.0
        ),
        # Every L2-to-controller read is now an independent 64-B request.
        # A paired descriptor contributes two frontend requests and two
        # physical accesses; it no longer contributes one wide request.
        "physical_read_relation_ok": int(physical == frontend),
        "physical_read_byte_relation_ok": int(
            physical * physical_unit == metrics["dram_frontend_read_bytes"]
        ),
        "paired_member_relation_ok": int(
            paired_members == 2 * paired_descriptors
            and paired_demands == paired_descriptors
            and paired_siblings == paired_descriptors
        ),
        "paired_submission_drained": int(
            paired_descriptors == paired
        ),
        "paired_submission_bounded": int(
            paired_descriptors <= paired
        ),
        "paired_work_partition_ok": int(
            paired == metrics["granularity_accepted_aggregates"]
            + metrics["granularity_demand_pair_aggregates"]
        ),
        "demand_pair_accounting_bounded": int(
            metrics["granularity_demand_pair_aggregates"]
            <= metrics["granularity_demand_pair_ready_opportunities"]
            and metrics["granularity_demand_pair_aggregates"] <= paired
        ),
        "aggregate_row_reuse_bounded": int(
            metrics["dram_aggregate_auto_precharge_stops"] <= paired
        ),
        "aggregate_immediate_continuation_bounded": int(
            metrics["dram_aggregate_immediate_continuations"]
            <= metrics["dram_aggregate_auto_precharge_stops"]
            and metrics["dram_aggregate_immediate_continuations"] <= paired
        ),
    }
    return row


def add_baseline_speedups(rows):
    baselines = {
        row["benchmark"]: row["driver_total_time"]
        for row in rows
        if row["config"] == "baseline" and row["success"]
        and row["driver_total_time"] > 0
    }
    for row in rows:
        baseline = baselines.get(row["benchmark"], 0.0)
        measured = row["driver_total_time"]
        row["speedup_vs_baseline"] = (
            baseline / measured if baseline > 0 and measured > 0 else 0.0
        )
    return rows


def geomean_rows(rows):
    grouped = defaultdict(list)
    for row in rows:
        speedup = row.get("speedup_vs_baseline", 0.0)
        if row["success"] and speedup > 0:
            grouped[(row["target"], row["config"])].append(speedup)
    result = []
    for (target, config), speedups in sorted(grouped.items()):
        result.append({
            "target": target,
            "config": config,
            "benchmark_count": len(speedups),
            "geomean_speedup": math.exp(
                sum(math.log(value) for value in speedups) / len(speedups)
            ),
            "improved_count": sum(value > 1.0 for value in speedups),
            "neutral_count": sum(abs(value - 1.0) <= 0.005 for value in speedups),
            "regressed_count": sum(value < 1.0 for value in speedups),
        })
    return result


def filter_contribution_rows(rows):
    by_cell = {(row["benchmark"], row["config"]): row for row in rows}
    result = []
    for benchmark in sorted({row["benchmark"] for row in rows}):
        filtered = by_cell.get((benchmark, "new_m1"))
        unfiltered = by_cell.get((benchmark, "paired_read_without_filter"))
        if filtered is None or unfiltered is None:
            continue
        exact_before = unfiltered["filter_exact_lookup_total"]
        exact_after = filtered["filter_exact_lookup_total"]
        result.append({
            "benchmark": benchmark,
            "filter_exact_lookups_avoided": exact_before - exact_after,
            "filter_exact_lookup_reduction": (
                (exact_before - exact_after) / exact_before
                if exact_before else 0.0
            ),
            "physical_read_access_delta": (
                filtered["dram_physical_read_accesses"]
                - unfiltered["dram_physical_read_accesses"]
            ),
            "physical_read_byte_delta": (
                filtered["dram_physical_read_bytes"]
                - unfiltered["dram_physical_read_bytes"]
            ),
            "wasted_sibling_byte_delta": (
                filtered["granularity_terminal_wasted_sibling_bytes"]
                - unfiltered["granularity_terminal_wasted_sibling_bytes"]
            ),
            "filtered_speedup": filtered["speedup_vs_baseline"],
            "unfiltered_speedup": unfiltered["speedup_vs_baseline"],
            "filtered_over_unfiltered": (
                unfiltered["driver_total_time"] / filtered["driver_total_time"]
                if filtered["driver_total_time"] > 0
                and unfiltered["driver_total_time"] > 0 else 0.0
            ),
        })
    return result


def retention_summary(rows, m1_config=None):
    configs = {row["config"] for row in rows}
    if m1_config is None:
        m1_config = "new_m1" if "new_m1" in configs else "m1"
    m1_rows = [row for row in rows
               if row["config"] == m1_config and row["success"]]
    if not m1_rows:
        return []
    by_cell = {(row["benchmark"], row["config"]): row for row in rows}
    useful = sum(row["granularity_useful_sibling_lines"] for row in m1_rows)
    unused = sum(row["granularity_terminal_unused_sibling_lines"]
                 for row in m1_rows)
    pairs = sum(row["granularity_frontend_paired_read_aggregates"]
                for row in m1_rows)
    row_reuse = sum(row["dram_aggregate_auto_precharge_stops"]
                    for row in m1_rows)
    speedups = [row["speedup_vs_baseline"] for row in m1_rows
                if row["speedup_vs_baseline"] > 0]
    applicable_rows = [
        row for row in m1_rows
        if row["granularity_frontend_paired_read_aggregates"] > 0
    ]
    applicable_positive = sum(
        row["speedup_vs_baseline"] > 1.005
        for row in applicable_rows
        if row["speedup_vs_baseline"] > 0
    )
    comparable = []
    for row in m1_rows:
        unfiltered = by_cell.get(
            (row["benchmark"], "paired_read_without_filter"))
        if unfiltered is not None and unfiltered["success"]:
            comparable.append((row, unfiltered))
    filtered_waste = sum(
        row["granularity_terminal_wasted_sibling_bytes"]
        for row, _ in comparable)
    unfiltered_waste = sum(
        row["granularity_terminal_wasted_sibling_bytes"]
        for _, row in comparable)
    filtered_reads = sum(row["dram_physical_read_bytes"]
                         for row, _ in comparable)
    unfiltered_reads = sum(row["dram_physical_read_bytes"]
                           for _, row in comparable)
    waste_reduction = percent_delta(unfiltered_waste, filtered_waste)
    read_reduction = percent_delta(unfiltered_reads, filtered_reads)
    useful_gate = useful > unused
    row_reuse_gate = pairs > 0 and row_reuse > 0
    filter_gate = waste_reduction is not None and waste_reduction > 0
    broad_gate = (
        len(applicable_rows) > 0
        and applicable_positive > len(applicable_rows) / 2
    )
    gate_failures = []
    if not useful_gate:
        gate_failures.append("useful_not_greater_than_unused")
    if not row_reuse_gate:
        gate_failures.append("no_pair_row_reuse")
    if not filter_gate:
        gate_failures.append("filter_did_not_reduce_wasted_bytes")
    if not broad_gate:
        gate_failures.append("no_positive_majority_on_applicable_workloads")
    return [{
        "m1_config": m1_config,
        "benchmark_count": len(m1_rows),
        "applicable_benchmark_count": len(applicable_rows),
        "applicable_positive_speedup_count": applicable_positive,
        "applicable_positive_majority": int(broad_gate),
        "positive_speedup_count": sum(value > 1.0 for value in speedups),
        "geomean_speedup": (
            math.exp(sum(math.log(value) for value in speedups) / len(speedups))
            if speedups else 0.0
        ),
        "paired_read_aggregates": pairs,
        "useful_sibling_lines": useful,
        "terminal_unused_sibling_lines": unused,
        "useful_exceeds_unused": int(useful > unused),
        "sibling_accuracy": useful / (useful + unused)
        if useful + unused else 0.0,
        "paired_row_reuse_rate": row_reuse / pairs if pairs else 0.0,
        "pair_row_reuse_observed": int(row_reuse_gate),
        "filter_comparable_benchmark_count": len(comparable),
        "filter_wasted_byte_reduction": waste_reduction,
        "filter_physical_read_byte_reduction": read_reduction,
        "filter_reduces_wasted_bytes": int(
            waste_reduction is not None and waste_reduction > 0),
        "filter_reduces_physical_read_bytes": int(
            read_reduction is not None and read_reduction > 0),
        "diagnostic_retain_gate_pass": int(not gate_failures),
        "diagnostic_retain_gate_failures": ";".join(gate_failures),
    }]


def percent_delta(before, after):
    if before <= 0:
        return None
    return (before - after) / before


def workload_identity_errors(rows):
    errors = []
    by_benchmark = defaultdict(list)
    for row in rows:
        by_benchmark[row["benchmark"]].append(row)
    for benchmark, cells in sorted(by_benchmark.items()):
        identities = {
            (
                row["wg_global_set_sha256"],
                row["wg_observed_count"],
                row.get("wg_completed_count", 0),
                row["wg_requested_count"],
                row.get("wg_kernel_count", 0),
                row.get("wg_launch_signature", ""),
            )
            for row in cells if row["success"]
        }
        if len(identities) > 1:
            errors.append(f"{benchmark}: workload identity differs across configs")
    return errors


def full_workload_errors(rows):
    errors = workload_identity_errors(rows)
    for row in rows:
        cell = f'{row["benchmark"]}/{row["config"]}'
        if not row["wg_mapping_present"]:
            errors.append(f"{cell}: missing WG mapping")
        if row["diagnostic_prefix_wg"]:
            errors.append(f"{cell}: positive max-WG prefix")
        if row["wg_stop_reason"] != "natural_completion":
            errors.append(
                f'{cell}: stop reason {row["wg_stop_reason"] or "missing"}'
            )
        if row["wg_requested_count"] <= 0:
            errors.append(f"{cell}: missing requested WG count")
        elif row["wg_observed_count"] != row["wg_requested_count"]:
            errors.append(
                f'{cell}: observed {row["wg_observed_count"]} of '
                f'{row["wg_requested_count"]} WGs'
            )
        if row.get("wg_completed_count", 0) != row["wg_requested_count"]:
            errors.append(
                f'{cell}: completed {row.get("wg_completed_count", 0)} of '
                f'{row["wg_requested_count"]} WGs'
            )
        if row.get("wg_kernel_count", 0) <= 0:
            errors.append(f"{cell}: missing executed kernel count")
        for field in (
            "wg_observed_sampling_coverage",
            "wg_completed_sampling_coverage",
        ):
            if abs(row.get(field, 0.0) - 1.0) > 1e-12:
                errors.append(f"{cell}: {field} is not one")
    return errors


def bounded_workload_errors(rows, expected_max_wg):
    """Audit an at-most-N campaign without changing WG selection semantics.

    A workload larger than the bound must stop after observing exactly N
    mapped WGs.  A smaller workload must finish naturally.  Across configs,
    the launch descriptor and the set hash must match; merely using the same
    numeric limit is not accepted as equal work.
    """
    if expected_max_wg <= 0:
        raise ValueError("expected max-WG must be positive")
    errors = []
    by_benchmark = defaultdict(list)
    for row in rows:
        by_benchmark[row["benchmark"]].append(row)
        cell = f'{row["benchmark"]}/{row["config"]}'
        if not row["wg_mapping_present"]:
            errors.append(f"{cell}: missing WG mapping")
            continue
        if row["diagnostic_prefix_wg"] != expected_max_wg:
            errors.append(
                f'{cell}: max-WG {row["diagnostic_prefix_wg"]} != '
                f'{expected_max_wg}'
            )
        requested = row["wg_requested_count"]
        if requested <= 0:
            errors.append(f"{cell}: missing requested WG count")
            continue
        expected_observed = min(expected_max_wg, requested)
        if row["wg_observed_count"] != expected_observed:
            errors.append(
                f'{cell}: observed {row["wg_observed_count"]} WGs, '
                f'expected {expected_observed}'
            )
        expected_reason = (
            "runner_map_wg_observed_limit"
            if requested > expected_max_wg else "natural_completion"
        )
        if row["wg_stop_reason"] != expected_reason:
            errors.append(
                f'{cell}: stop reason {row["wg_stop_reason"] or "missing"}, '
                f'expected {expected_reason}'
            )
        if row.get("wg_kernel_count", 0) <= 0:
            errors.append(f"{cell}: missing executed kernel count")

    for benchmark, cells in sorted(by_benchmark.items()):
        identities = {
            (
                row["wg_global_set_sha256"],
                row["wg_observed_count"],
                row["wg_requested_count"],
                row.get("wg_kernel_count", 0),
                row.get("wg_launch_signature", ""),
            )
            for row in cells if row["success"] and row["wg_mapping_present"]
        }
        if len(identities) > 1:
            errors.append(
                f"{benchmark}: bounded WG identity differs across configs"
            )
    return errors


def file_sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def audit_campaign_identity(
    results_dir,
    rows,
    expected_benchmarks=None,
    expected_configs=None,
    expected_sha256=None,
):
    root = Path(results_dir)
    observed_cells = {
        (row["benchmark"], row["config"])
        for row in rows
    }
    if (expected_benchmarks is None) != (expected_configs is None):
        raise ValueError(
            "expected benchmarks and configs must be supplied together"
        )
    if expected_benchmarks is not None:
        expected_cells = {
            (benchmark, config)
            for benchmark in expected_benchmarks
            for config in expected_configs
        }
        if observed_cells != expected_cells:
            missing = expected_cells - observed_cells
            extra = observed_cells - expected_cells
            raise ValueError(
                "campaign result grid mismatch: "
                f"observed={len(observed_cells)}, "
                f"expected={len(expected_cells)}, "
                f"missing={len(missing)}, extra={len(extra)}"
            )

    metadata_path = root / "EXPERIMENT_METADATA.json"
    manifest_path = root / "EXPERIMENT_BINARIES.json"
    if not metadata_path.is_file() or not manifest_path.is_file():
        raise ValueError("campaign metadata or binary manifest is missing")
    metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    experiments = metadata.get("experiments", [])
    metadata_cells = set()
    binaries = set()
    for experiment in experiments:
        cell = (
            experiment.get("benchmark", ""),
            experiment.get("configuration", ""),
        )
        command = experiment.get("command", [])
        if cell in metadata_cells or not command:
            raise ValueError(f"invalid or duplicate metadata cell {cell}")
        if command[1:2] != [f"-benchmark={cell[0]}"]:
            raise ValueError(f"metadata command benchmark mismatch for {cell}")
        audit_general_row_continuation(cell[1], command)
        metadata_cells.add(cell)
        binaries.add(str(Path(command[0]).resolve()))
    if metadata.get("experiment_count") != len(experiments):
        raise ValueError("metadata experiment count mismatch")
    if metadata_cells != observed_cells:
        raise ValueError("metadata and completed-result grids differ")
    if len(binaries) != 1:
        raise ValueError("campaign mixes simulator binaries")
    binary = Path(next(iter(binaries)))
    if not binary.is_file():
        raise ValueError(f"campaign binary is missing: {binary}")
    actual_sha256 = file_sha256(binary)
    manifest_sha256 = manifest.get("sha256_by_target", {}).get("baseline")
    if actual_sha256 != manifest_sha256:
        raise ValueError(
            "campaign binary differs from manifest: "
            f"{actual_sha256} != {manifest_sha256}"
        )
    if expected_sha256 and actual_sha256 != expected_sha256:
        raise ValueError(
            "campaign binary differs from expected SHA-256: "
            f"{actual_sha256} != {expected_sha256}"
        )
    return {
        "cell_count": len(observed_cells),
        "binary": str(binary),
        "binary_sha256": actual_sha256,
    }


def parse_csv_list(value):
    return tuple(item.strip() for item in value.split(",") if item.strip())


def summarize_directory(results_dir):
    paths = sorted(Path(results_dir).glob("*_result.json"))
    if not paths:
        raise FileNotFoundError(f"no result JSON files in {results_dir}")
    return [summarize_cell(path) for path in paths]


def write_summary(rows, output):
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def write_geomeans(rows, output):
    geomeans = geomean_rows(rows)
    if not geomeans:
        return
    with Path(output).open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(geomeans[0]))
        writer.writeheader()
        writer.writerows(geomeans)


def write_filter_contribution(rows, output):
    contributions = filter_contribution_rows(rows)
    if not contributions:
        return False
    with Path(output).open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(contributions[0]))
        writer.writeheader()
        writer.writerows(contributions)
    return True


def write_retention(rows, output, m1_config=None):
    summary = retention_summary(rows, m1_config)
    if not summary:
        return False
    with Path(output).open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(summary[0]))
        writer.writeheader()
        writer.writerows(summary)
    return True


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("results_dir", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument(
        "--strict", action="store_true",
        help="fail if a cell failed or violates paired/physical accounting",
    )
    parser.add_argument(
        "--require-full-workload", action="store_true",
        help="also reject prefixes, early stops, missing maps, or cross-config WG mismatches",
    )
    parser.add_argument(
        "--require-max-wg", type=int, default=0,
        help=(
            "audit an at-most-N campaign and require the exact same mapped "
            "WG set across configurations"
        ),
    )
    parser.add_argument(
        "--m1-config", default=None,
        help="M1 row used by the retention summary (auto: new_m1, then m1)",
    )
    parser.add_argument(
        "--expected-benchmarks", default="",
        help="comma-separated exact campaign workload grid",
    )
    parser.add_argument(
        "--expected-configs", default="",
        help="comma-separated exact campaign configuration grid",
    )
    parser.add_argument("--expected-sha256", default="")
    args = parser.parse_args()
    rows = add_baseline_speedups(summarize_directory(args.results_dir))
    output = args.output or args.results_dir / "m1_paired_read_summary.csv"
    write_summary(rows, output)
    geomean_output = output.with_name(output.stem + "_geomean.csv")
    write_geomeans(rows, geomean_output)
    filter_output = output.with_name(output.stem + "_filter_contribution.csv")
    has_filter_comparison = write_filter_contribution(rows, filter_output)
    retention_output = output.with_name(output.stem + "_retention.csv")
    has_retention = write_retention(rows, retention_output, args.m1_config)
    print(output)
    print(geomean_output)
    if has_filter_comparison:
        print(filter_output)
    if has_retention:
        print(retention_output)
    if args.strict:
        bounded_prefix = bool(args.require_max_wg)
        invalid = [
            row for row in rows
            if not row["success"] or not row["physical_read_relation_ok"]
            or not row["physical_read_byte_relation_ok"]
            or not row["paired_member_relation_ok"]
            or not row["paired_work_partition_ok"]
            or not row["demand_pair_accounting_bounded"]
            or not (
                row["paired_submission_bounded"]
                if bounded_prefix else row["paired_submission_drained"]
            )
            or not row["sibling_timeliness_partition_ok"]
            or not (
                row["sibling_terminal_accounting_bounded"]
                if bounded_prefix else row["sibling_terminal_partition_ok"]
            )
            or not row["aggregate_row_reuse_bounded"]
            or not row["aggregate_immediate_continuation_bounded"]
            or not row["dram_physical_access_unit_consistent"]
            or not row["general_row_continuation_disabled"]
            or not row["aggregate_continuation_mode_ok"]
            or not row["granularity_runtime_mode_ok"]
            or not row["fill_forwarding_runtime_mode_ok"]
        ]
        if invalid:
            raise SystemExit(
                "paired-read accounting failed for: "
                + ", ".join(
                    f'{row["benchmark"]}/{row["config"]}' for row in invalid
                )
            )
    if args.require_full_workload:
        errors = full_workload_errors(rows)
        if errors:
            raise SystemExit("full-workload audit failed: " + "; ".join(errors))
    if args.require_max_wg:
        try:
            errors = bounded_workload_errors(rows, args.require_max_wg)
        except ValueError as error:
            raise SystemExit(f"bounded-workload audit failed: {error}")
        if errors:
            raise SystemExit(
                "bounded-workload audit failed: " + "; ".join(errors)
            )
    expected_benchmarks = parse_csv_list(args.expected_benchmarks)
    expected_configs = parse_csv_list(args.expected_configs)
    if expected_benchmarks or expected_configs or args.expected_sha256:
        try:
            audit_campaign_identity(
                args.results_dir,
                rows,
                expected_benchmarks or None,
                expected_configs or None,
                args.expected_sha256 or None,
            )
        except (OSError, ValueError, KeyError) as error:
            raise SystemExit(f"campaign identity audit failed: {error}")


if __name__ == "__main__":
    main()
