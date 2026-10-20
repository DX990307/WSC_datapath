#!/usr/bin/env python3
"""Write a strict, count-weighted summary of the formal 28-cell campaign."""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

from plot_cupath_typed_ablation import WORKLOADS


def read_rows(path: Path) -> list[dict[str, str]]:
    if not path.is_file() or path.stat().st_size == 0:
        raise ValueError(f"missing input: {path}")
    with path.open(newline="", encoding="utf-8") as stream:
        return list(csv.DictReader(stream))


def number(row: dict[str, str], field: str) -> float:
    value = row.get(field, "").strip()
    if not value:
        raise ValueError(f"missing {field} for {row.get('benchmark', 'row')}")
    return float(value)


def ratio(numerator: float, denominator: float) -> float:
    return numerator / denominator if denominator else math.nan


def fmt(value: float) -> str:
    return "N/A" if not math.isfinite(value) else f"{value:.4f}"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    args = parser.parse_args()
    root = args.results.resolve()
    analysis = root / "observation-analysis"

    speed_rows = read_rows(root / "cupath_baseline_complete_speedup.csv")
    benchmark_names = {benchmark for benchmark, _label, _group in WORKLOADS}
    per_benchmark = {
        row["benchmark"]: row
        for row in speed_rows
        if row.get("benchmark") in benchmark_names
    }
    if set(per_benchmark) != benchmark_names:
        raise ValueError(
            f"speedup table covers {len(per_benchmark)}/14 benchmarks"
        )
    speedups = {
        benchmark: number(per_benchmark[benchmark], "complete_speedup")
        for benchmark in sorted(benchmark_names)
    }
    if any(value <= 0 for value in speedups.values()):
        raise ValueError("all speedups must be positive")
    geomean = math.exp(
        sum(math.log(value) for value in speedups.values()) / len(speedups)
    )

    mechanism_rows = read_rows(root / "cupath_mechanism_effectiveness.csv")
    mechanisms = {
        row["benchmark"]: row
        for row in mechanism_rows
        if row.get("benchmark") in benchmark_names
    }
    if set(mechanisms) != benchmark_names:
        raise ValueError(
            f"mechanism table covers {len(mechanisms)}/14 benchmarks"
        )
    for benchmark, row in mechanisms.items():
        for field in ("m1_metric_source", "m2_metric_source", "m3_metric_source"):
            if row.get(field) != "complete":
                raise ValueError(
                    f"{benchmark} {field}={row.get(field)!r}; expected Complete"
                )

    def total(field: str) -> float:
        return sum(number(row, field) for row in mechanisms.values())

    m1_predictions = total("m1_pair_predictions")
    m1_useful = total("m1_useful_prefetches")
    m1_timely = total("m1_timely_buffer_hits")
    m1_unused = total("m1_retired_unused_prefetches")
    m2_logical = total("m2_logical_remote_reads")
    m2_duplicates = total("m2_duplicate_remote_reads")
    m2_wire = total("m2_wire_lines")
    m2_packets = total("m2_request_packets")
    m3_logical = total("m3_logical_remote_reads")
    m3_hits = total("m3_requester_l2_hits")
    m3_installed = total("m3_installed_remote_fills")
    m3_unused = total("m3_unused_remote_fills")
    filter_queries = total("filter_queries")
    filter_positives = total("filter_positives")
    filter_false = total("filter_false_positives")
    filter_insertions = total("filter_insertions")
    filter_insert_failures = total("filter_insert_failures")
    filter_fail_open = total("filter_fail_open_events")
    filter_lookup_stalls = total("filter_lookup_port_stalls")
    filter_update_stalls = total("filter_update_port_stalls")

    o1_validation = read_rows(analysis / "o1_validation.csv")
    emitter_validation = read_rows(
        analysis / "emitter_instrumentation_validation.csv"
    )
    if len(o1_validation) != 14 or any(
        row.get("strict_pass") != "true" for row in o1_validation
    ):
        raise ValueError("O1 validation is not 14/14 strict-pass")
    if not emitter_validation or any(
        row.get("strict_pass") != "true" for row in emitter_validation
    ):
        raise ValueError("emitter validation contains a strict failure")

    observation_files = (
        "o1_component_latency_breakdown.csv",
        "o1_l2_demand_read_miss_rate.csv",
        "o2_adjacent_line_window_heatmap.csv",
        "o3_physical_locality_heatmap_long.csv",
        "o4_remote_work_before_owner_mshr.csv",
        "o5_exact_inflight_dedup.csv",
        "o6_remote_reuse_summary.csv",
        "o6_l2_headroom.csv",
    )
    observation_rows = {}
    for name in observation_files:
        observation_rows[name] = len(read_rows(analysis / name))

    summary = [
        ("benchmarks", 14),
        ("complete_geomean_speedup", geomean),
        ("benchmarks_above_1x", sum(value > 1.0 for value in speedups.values())),
        ("benchmarks_below_1x", sum(value < 1.0 for value in speedups.values())),
        ("m1_pair_predictions", m1_predictions),
        ("m1_useful_prefetches", m1_useful),
        ("m1_useful_rate", ratio(m1_useful, m1_predictions)),
        ("m1_timely_rate", ratio(m1_timely, m1_predictions)),
        ("m1_retired_unused_prefetches", m1_unused),
        ("m2_logical_remote_reads", m2_logical),
        ("m2_duplicate_remote_reads", m2_duplicates),
        ("m2_coalescing_rate", ratio(m2_duplicates, m2_logical)),
        ("m2_wire_lines", m2_wire),
        ("m2_request_packets", m2_packets),
        ("m2_average_lines_per_packet", ratio(m2_wire, m2_packets)),
        ("m3_logical_remote_reads", m3_logical),
        ("m3_requester_l2_hits", m3_hits),
        ("m3_requester_l2_hit_fraction", ratio(m3_hits, m3_logical)),
        ("m3_installed_remote_fills", m3_installed),
        ("m3_unused_remote_fills", m3_unused),
        ("filter_queries", filter_queries),
        ("filter_positives", filter_positives),
        ("filter_false_positives", filter_false),
        ("filter_false_positives_per_query", ratio(filter_false, filter_queries)),
        ("filter_false_positives_per_positive", ratio(filter_false, filter_positives)),
        ("filter_insertions", filter_insertions),
        ("filter_insert_failures", filter_insert_failures),
        ("filter_insert_failure_rate", ratio(filter_insert_failures, filter_insertions)),
        ("filter_fail_open_events", filter_fail_open),
        ("filter_lookup_port_stalls", filter_lookup_stalls),
        ("filter_update_port_stalls", filter_update_stalls),
        ("o1_strict_pass", len(o1_validation)),
        ("emitter_strict_pass", len(emitter_validation)),
    ]
    with (root / "cupath_final_summary.csv").open(
        "w", newline="", encoding="utf-8"
    ) as stream:
        writer = csv.writer(stream)
        writer.writerow(["metric", "value"])
        writer.writerows(summary)

    labels = {benchmark: label for benchmark, label, _group in WORKLOADS}
    lines = [
        "# CuPath Baseline+Complete final results",
        "",
        f"- Formal cells: 28/28",
        f"- Complete geometric-mean speedup: {geomean:.4f}x",
        f"- Workloads above/below 1.0x: "
        f"{sum(v > 1 for v in speedups.values())}/{sum(v < 1 for v in speedups.values())}",
        f"- O1 path validations: {len(o1_validation)}/14 strict-pass",
        f"- Emitter checks: {len(emitter_validation)}/{len(emitter_validation)} strict-pass",
        "",
        "## Mechanism effectiveness from Complete",
        "",
        f"- M1 useful/timely rates: {fmt(ratio(m1_useful, m1_predictions))} / "
        f"{fmt(ratio(m1_timely, m1_predictions))}",
        f"- M2 duplicate coalescing rate: {fmt(ratio(m2_duplicates, m2_logical))}",
        f"- M2 average wire lines per request packet: "
        f"{fmt(ratio(m2_wire, m2_packets))}",
        f"- M3 requester-L2 hit fraction: {fmt(ratio(m3_hits, m3_logical))}",
        f"- Cuckoo Filter false positives per query/positive: "
        f"{fmt(ratio(filter_false, filter_queries))} / "
        f"{fmt(ratio(filter_false, filter_positives))}",
        f"- Cuckoo Filter fail-open events: {filter_fail_open:.0f}",
        "",
        "## Per-workload speedup",
        "",
        "| Benchmark | Speedup |",
        "|---|---:|",
    ]
    for benchmark, _label, _group in WORKLOADS:
        lines.append(f"| {labels[benchmark]} | {speedups[benchmark]:.4f}x |")
    lines.extend(["", "## Observation artifact rows", ""])
    for name, count in observation_rows.items():
        lines.append(f"- `{name}`: {count}")
    (root / "CUPATH_FINAL_RESULTS.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )
    print(
        f"final summary PASS: benchmarks=14 geomean={geomean:.6f} "
        f"o1={len(o1_validation)} emitter_checks={len(emitter_validation)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
