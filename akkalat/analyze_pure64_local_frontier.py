#!/usr/bin/env python3
"""Audit remaining local-path work in a pure-64B CuPath campaign.

The audit deliberately compares Baseline and M1 from the same 70-cell result
directory. It reports whether M1 removes physical DRAM transactions, how much
definite-miss tag work it filters, which correctness-preserving merges and
full-line-write shortcuts are already present, and whether L2 capacity is
actually under pressure. It does not infer a performance opportunity from a
counter that is not measured.
"""

from __future__ import annotations

import argparse
import csv
import math
from collections import defaultdict
from pathlib import Path


BENCHMARKS = (
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fft",
    "fir",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
)

MAX_METRICS = {
    "l2_miss_to_dram_issue_max_ns",
    "l2_fast_miss_to_dram_issue_max_ns",
    "l2_demand_read_latency_max_ns",
    "filter_prefetch_predictor_lookahead_max",
}


def load_metrics(path: Path) -> tuple[float, dict[str, float]]:
    values: dict[str, float] = defaultdict(float)
    driver_time: float | None = None
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            where = row["where"].strip()
            what = row["what"].strip()
            value = float(row["value"])
            if what in MAX_METRICS:
                values[what] = max(values[what], value)
            else:
                values[what] += value
            if where == "Driver" and what == "total_time":
                driver_time = value
    if driver_time is None or driver_time <= 0:
        raise ValueError(f"missing positive Driver total_time in {path}")
    return driver_time, dict(values)


def ratio(numerator: float, denominator: float) -> float:
    return numerator / denominator if denominator > 0 else 0.0


def geomean(values: list[float]) -> float:
    return math.exp(sum(math.log(value) for value in values) / len(values))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument(
        "--benchmarks",
        default=",".join(BENCHMARKS),
        help="Comma-separated benchmark names to analyze.",
    )
    parser.add_argument(
        "--allow-partial",
        action="store_true",
        help="Skip missing Baseline/M1 pairs instead of rejecting the campaign.",
    )
    args = parser.parse_args()
    results = args.results.resolve()
    output = (args.output_dir or results / "analysis").resolve()
    output.mkdir(parents=True, exist_ok=True)

    rows: list[dict[str, float | str]] = []
    benchmarks = tuple(
        name.strip() for name in args.benchmarks.split(",") if name.strip()
    )
    for benchmark in benchmarks:
        baseline_path = results / f"baseline_{benchmark}_baseline_metrics.csv"
        m1_path = results / f"baseline_{benchmark}_m1_metrics.csv"
        if not baseline_path.exists() or not m1_path.exists():
            if args.allow_partial:
                print(f"skipping incomplete Baseline/M1 pair: {benchmark}")
                continue
            raise SystemExit(f"missing Baseline/M1 pair for {benchmark}")
        baseline_time, baseline = load_metrics(baseline_path)
        m1_time, m1 = load_metrics(m1_path)

        physical_reads_before = baseline.get("dram_physical_read_accesses", 0.0)
        physical_reads_after = m1.get("dram_physical_read_accesses", 0.0)
        local_dram_before = baseline.get("l2_to_dram_64b_requests", 0.0)
        local_dram_after = m1.get("l2_to_dram_64b_requests", 0.0)
        prefetch_dram = m1.get("filter_prefetch_additional_dram_reads", 0.0)
        demand_dram_after = local_dram_after - prefetch_dram
        if demand_dram_after < 0:
            raise SystemExit(
                f"{benchmark}: prefetch DRAM reads exceed total L2-to-DRAM reads"
            )
        lifecycle_fields = (
            "filter_prefetch_issued",
            "filter_prefetch_fills",
            "filter_prefetch_redundant_races",
            "filter_prefetch_outstanding",
        )
        if all(field in m1 for field in lifecycle_fields):
            issued = m1["filter_prefetch_issued"]
            accounted = (
                m1["filter_prefetch_fills"]
                + m1["filter_prefetch_redundant_races"]
                + m1["filter_prefetch_outstanding"]
            )
            if issued != accounted:
                raise SystemExit(
                    f"{benchmark}: issued prefetches do not partition into "
                    "fills, redundant races, and runtime-stop outstanding "
                    "requests"
                )
        prefetch_late = m1.get("filter_prefetch_late", 0.0)
        prefetch_late_after_dram = m1.get(
            "filter_prefetch_late_after_dram_issue", 0.0
        )
        if prefetch_late_after_dram > prefetch_late:
            raise SystemExit(
                f"{benchmark}: late-after-DRAM events exceed all late events"
            )
        baseline_demand_latency = ratio(
            baseline.get("l2_demand_read_latency_total_ns", 0.0),
            baseline.get("l2_demand_read_latency_samples", 0.0),
        )
        m1_demand_latency = ratio(
            m1.get("l2_demand_read_latency_total_ns", 0.0),
            m1.get("l2_demand_read_latency_samples", 0.0),
        )
        physical_writes_before = baseline.get("dram_physical_write_accesses", 0.0)
        physical_writes_after = m1.get("dram_physical_write_accesses", 0.0)
        resident_queries = m1.get("l2_resident_filter_queries", 0.0)
        resident_negatives = m1.get("l2_resident_filter_negatives", 0.0)
        filter_slots = m1.get("typed_filter_slots", 0.0)
        # The retained capacity derivation allocates two filter slots per L2
        # line: one resident slot plus equal transient/reuse headroom.
        physical_l2_lines = filter_slots / 2.0
        resident_peak = m1.get("typed_filter_resident_peak_occupancy", 0.0)
        row_columns = m1.get("dram_row_column_commands", 0.0)
        row_hits = m1.get("dram_row_reuse_hits", 0.0)

        rows.append(
            {
                "benchmark": benchmark,
                "m1_speedup": baseline_time / m1_time,
                "resident_negative_pct": 100.0 * ratio(
                    resident_negatives, resident_queries
                ),
                "read_fast_misses": m1.get(
                    "l2_resident_filter_read_bypasses", 0.0
                ),
                "parallel_mshr_merges": m1.get(
                    "l2_resident_filter_read_parallel_mshr_merges", 0.0
                ),
                "negative_mshr_merges": m1.get(
                    "l2_resident_filter_read_negative_mshr_merges", 0.0
                ),
                "full_line_write_fast_misses": m1.get(
                    "l2_resident_filter_write_full_line_bypasses", 0.0
                ),
                "partial_write_rfos_preserved": m1.get(
                    "l2_resident_filter_write_partial_bypasses", 0.0
                ),
                "physical_reads_baseline": physical_reads_before,
                "physical_reads_m1": physical_reads_after,
                "physical_reads_removed_pct": 100.0 * ratio(
                    physical_reads_before - physical_reads_after,
                    physical_reads_before,
                ),
                "l2_to_dram_reads_baseline": local_dram_before,
                "l2_to_dram_demand_reads_m1": demand_dram_after,
                "l2_to_dram_prefetch_reads_m1": prefetch_dram,
                "l2_to_dram_total_reads_m1": local_dram_after,
                "l2_to_dram_demand_reads_removed_pct": 100.0 * ratio(
                    local_dram_before - demand_dram_after, local_dram_before
                ),
                "l2_to_dram_total_reads_removed_pct": 100.0 * ratio(
                    local_dram_before - local_dram_after, local_dram_before
                ),
                "l2_demand_read_latency_avg_ns_baseline": baseline_demand_latency,
                "l2_demand_read_latency_avg_ns_m1": m1_demand_latency,
                "l2_demand_read_latency_samples_baseline": baseline.get(
                    "l2_demand_read_latency_samples", 0.0
                ),
                "l2_demand_read_latency_total_ns_baseline": baseline.get(
                    "l2_demand_read_latency_total_ns", 0.0
                ),
                "l2_demand_read_latency_samples_m1": m1.get(
                    "l2_demand_read_latency_samples", 0.0
                ),
                "l2_demand_read_latency_total_ns_m1": m1.get(
                    "l2_demand_read_latency_total_ns", 0.0
                ),
                "l2_demand_read_latency_delta_pct": 100.0 * ratio(
                    m1_demand_latency - baseline_demand_latency,
                    baseline_demand_latency,
                ),
                "l2_demand_read_latency_max_ns_baseline": baseline.get(
                    "l2_demand_read_latency_max_ns", 0.0
                ),
                "l2_demand_read_latency_max_ns_m1": m1.get(
                    "l2_demand_read_latency_max_ns", 0.0
                ),
                "physical_writes_baseline": physical_writes_before,
                "physical_writes_m1": physical_writes_after,
                "resident_peak_lines": resident_peak,
                "l2_line_capacity": physical_l2_lines,
                "resident_peak_capacity_pct": 100.0 * ratio(
                    resident_peak, physical_l2_lines
                ),
                "row_reuse_pct": 100.0 * ratio(row_hits, row_columns),
                "mshr_full_stall_cycles": m1.get(
                    "l2_mshr_full_stall_cycles", 0.0
                ),
                "mshr_full_stall_delta_pct": 100.0 * ratio(
                    m1.get("l2_mshr_full_stall_cycles", 0.0)
                    - baseline.get("l2_mshr_full_stall_cycles", 0.0),
                    baseline.get("l2_mshr_full_stall_cycles", 0.0),
                ),
                "prefetch_candidates": m1.get(
                    "filter_prefetch_candidates", 0.0
                ),
                "prefetch_real_demands": m1.get(
                    "filter_prefetch_real_demands", 0.0
                ),
                "prefetch_issued": m1.get("filter_prefetch_issued", 0.0),
                "prefetch_outstanding": m1.get(
                    "filter_prefetch_outstanding", 0.0
                ),
                "prefetch_fills": m1.get("filter_prefetch_fills", 0.0),
                "prefetch_redundant_races": m1.get(
                    "filter_prefetch_redundant_races", 0.0
                ),
                "prefetch_useful": m1.get("filter_prefetch_useful", 0.0),
                "prefetch_timely": m1.get("filter_prefetch_timely", 0.0),
                "prefetch_late": prefetch_late,
                "prefetch_late_before_dram_issue": (
                    prefetch_late - prefetch_late_after_dram
                ),
                "prefetch_late_after_dram_issue": prefetch_late_after_dram,
                "prefetch_demand_won_races": m1.get(
                    "filter_prefetch_demand_won_races", 0.0
                ),
                "prefetch_unused_evictions": m1.get(
                    "filter_prefetch_unused_evictions", 0.0
                ),
                "prefetch_unused_reset_retirements": m1.get(
                    "filter_prefetch_unused_reset_retirements", 0.0
                ),
                "prefetch_peak_only_lines": m1.get(
                    "filter_prefetch_peak_prefetch_only_lines", 0.0
                ),
                "prefetch_peak_l2_capacity_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_peak_prefetch_only_lines", 0.0),
                    physical_l2_lines,
                ),
                "prefetch_additional_dram_reads": m1.get(
                    "filter_prefetch_additional_dram_reads", 0.0
                ),
                "prefetch_mshr_headroom_drops": m1.get(
                    "filter_prefetch_mshr_headroom_drops", 0.0
                ),
                "prefetch_training_pending_drops": m1.get(
                    "filter_prefetch_training_pending_drops", 0.0
                ),
                "prefetch_outstanding_capacity_drops": m1.get(
                    "filter_prefetch_outstanding_capacity_drops", 0.0
                ),
                "prefetch_demand_mshr_covered_drops": m1.get(
                    "filter_prefetch_demand_mshr_covered_drops", 0.0
                ),
                "prefetch_prefetch_mshr_covered_drops": m1.get(
                    "filter_prefetch_prefetch_mshr_covered_drops", 0.0
                ),
                "prefetch_predictor_demand_covered_feedback": m1.get(
                    "filter_prefetch_predictor_demand_covered_feedback", 0.0
                ),
                "prefetch_predictor_covered_distance_increases": m1.get(
                    "filter_prefetch_predictor_covered_distance_increases", 0.0
                ),
                "prefetch_predictor_horizon_distance_increases": m1.get(
                    "filter_prefetch_predictor_horizon_distance_increases", 0.0
                ),
                "prefetch_predictor_page_frontier_clamps": m1.get(
                    "filter_prefetch_predictor_page_frontier_clamps", 0.0
                ),
                "prefetch_accuracy_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_useful", 0.0),
                    m1.get("filter_prefetch_issued", 0.0),
                ),
                "prefetch_candidate_coverage_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_candidates", 0.0),
                    m1.get("filter_prefetch_real_demands", 0.0),
                ),
                "prefetch_issue_coverage_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_issued", 0.0),
                    m1.get("filter_prefetch_real_demands", 0.0),
                ),
                "prefetch_useful_coverage_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_useful", 0.0),
                    m1.get("filter_prefetch_real_demands", 0.0),
                ),
                "prefetch_timely_coverage_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_timely", 0.0),
                    m1.get("filter_prefetch_real_demands", 0.0),
                ),
                "prefetch_timely_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_timely", 0.0),
                    m1.get("filter_prefetch_useful", 0.0),
                ),
                "prefetch_late_pct": 100.0 * ratio(
                    m1.get("filter_prefetch_late", 0.0),
                    m1.get("filter_prefetch_useful", 0.0),
                ),
                "prefetch_mean_lookahead": ratio(
                    m1.get("filter_prefetch_predictor_lookahead_total", 0.0),
                    m1.get("filter_prefetch_candidates", 0.0),
                ),
                "prefetch_max_lookahead": m1.get(
                    "filter_prefetch_predictor_lookahead_max", 0.0
                ),
                "prefetch_stale_distance_feedback": m1.get(
                    "filter_prefetch_predictor_stale_distance_feedback", 0.0
                ),
            }
        )

    if not rows:
        raise SystemExit("no complete Baseline/M1 pairs found")

    fields = list(rows[0])
    csv_path = output / "pure64_local_frontier.csv"
    with csv_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)

    weighted = lambda field: sum(float(row[field]) for row in rows)
    total_queries = weighted("read_fast_misses")
    physical_before = weighted("physical_reads_baseline")
    physical_after = weighted("physical_reads_m1")
    local_dram_before = weighted("l2_to_dram_reads_baseline")
    local_dram_demand_after = weighted("l2_to_dram_demand_reads_m1")
    local_dram_prefetch_after = weighted("l2_to_dram_prefetch_reads_m1")
    local_dram_after = weighted("l2_to_dram_total_reads_m1")
    baseline_latency_samples = weighted(
        "l2_demand_read_latency_samples_baseline"
    )
    baseline_latency_total = weighted(
        "l2_demand_read_latency_total_ns_baseline"
    )
    m1_latency_samples = weighted("l2_demand_read_latency_samples_m1")
    m1_latency_total = weighted("l2_demand_read_latency_total_ns_m1")
    max_capacity = max(float(row["resident_peak_capacity_pct"]) for row in rows)
    issued = weighted("prefetch_issued")
    real_demands = weighted("prefetch_real_demands")
    candidates = weighted("prefetch_candidates")
    outstanding = weighted("prefetch_outstanding")
    useful = weighted("prefetch_useful")
    timely = weighted("prefetch_timely")
    late = weighted("prefetch_late")
    late_before_dram = weighted("prefetch_late_before_dram_issue")
    late_after_dram = weighted("prefetch_late_after_dram_issue")
    demand_won = weighted("prefetch_demand_won_races")
    extra_reads = weighted("prefetch_additional_dram_reads")
    training_drops = weighted("prefetch_training_pending_drops")
    unused_evictions = weighted("prefetch_unused_evictions")
    unused_at_reset = weighted("prefetch_unused_reset_retirements")
    max_prefetch_capacity = max(
        float(row["prefetch_peak_l2_capacity_pct"]) for row in rows
    )
    lines = [
        "# Pure-64B local opportunity audit",
        "",
        f"M1 geomean: {geomean([float(row['m1_speedup']) for row in rows]):.4f}x.",
        f"Across all workloads, M1 issues {physical_after:,.0f} physical reads "
        f"versus {physical_before:,.0f} in Baseline "
        f"({100.0 * ratio(physical_before - physical_after, physical_before):.3f}% removed).",
        f"At the L2-to-DRAM boundary, Baseline sends {local_dram_before:,.0f} "
        f"64-B reads. M1 sends {local_dram_demand_after:,.0f} demand reads plus "
        f"{local_dram_prefetch_after:,.0f} prefetch reads, or "
        f"{local_dram_after:,.0f} total. This separates demand reads displaced "
        "by prediction from actual net request reduction.",
        f"Sample-weighted L2 demand-read latency changes from "
        f"{ratio(baseline_latency_total, baseline_latency_samples):.3f} ns to "
        f"{ratio(m1_latency_total, m1_latency_samples):.3f} ns. This is the "
        "actual request-accept-to-response interval; demand-delay events remain "
        "only a pressure correlation counter.",
        f"The largest summed-slice RESIDENT peak uses {max_capacity:.3f}% of "
        "the wafer-wide L2 line capacity.",
        f"The prefetch path issues {issued:,.0f} independent 64-B requests: "
        f"{useful:,.0f} are consumed by a demand, {timely:,.0f} complete before "
        f"that demand, and {demand_won:,.0f} lose the race to demand. "
        f"{extra_reads:,.0f} reach DRAM, and {outstanding:,.0f} remain "
        "outstanding at the runtime-stop report boundary.",
        f"Timeliness training suppresses {training_drops:,.0f} overlapping "
        "candidates from streams that have not yet demonstrated a timely hit.",
        f"Candidate/issue/useful/timely coverage over {real_demands:,.0f} "
        f"real demands is {100.0 * ratio(candidates, real_demands):.3f}%/"
        f"{100.0 * ratio(issued, real_demands):.3f}%/"
        f"{100.0 * ratio(useful, real_demands):.3f}%/"
        f"{100.0 * ratio(timely, real_demands):.3f}%. Useful-per-issued "
        f"accuracy is {100.0 * ratio(useful, issued):.3f}%, and "
        f"{100.0 * ratio(timely, useful):.3f}% of useful prefetches are "
        f"timely. The {late:,.0f} late useful prefetches split into "
        f"{late_before_dram:,.0f} demand arrivals before prefetch DRAM issue "
        f"and {late_after_dram:,.0f} after issue but before fill. Outstanding "
        "requests are right-censored, not unused.",
        f"Unused prefetch-only lines cause {unused_evictions:,.0f} runtime "
        f"evictions; {unused_at_reset:,.0f} more remain unused when cache "
        f"history is reset. Their largest summed-slice occupancy peak is "
        f"{max_prefetch_capacity:.3f}% of wafer L2 capacity.",
        "",
        "M1 retains ordinary 64-B cacheline requests. Its RESIDENT negative "
        f"test shortcuts {total_queries:,.0f} definite read misses, while its "
        "page-local predictor can only convert a later demand read into an "
        "earlier prefetch read; it does not merge distinct cachelines into one "
        "physical transaction.",
        "",
        "The performance test is therefore net work reduction and timeliness: "
        "physical reads must not grow materially, enough useful predictions "
        "must finish before demand, and L2 MSHR stalls must not offset the "
        "hidden latency.",
        "",
        f"Machine-readable table: `{csv_path.name}`.",
    ]
    report_path = output / "PURE64_LOCAL_FRONTIER.md"
    report_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(csv_path)
    print(report_path)


if __name__ == "__main__":
    main()
