#!/usr/bin/env python3
"""Quantify the Filter's independent and coupled value on a fixed binary."""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

from plot_cupath_typed_ablation import read_metrics


MAIN_CONFIGS = (
    "baseline",
    "cuckoo_filter_only",
    "ungated_prefetch",
    "filter_coupled_prefetch",
)
EXACT_CONFIG = "exact_metadata_prefetch"


def metric_path(root: Path, benchmark: str, config: str) -> Path:
    paths = sorted(root.glob(f"*_{benchmark}_{config}_metrics.csv"))
    if len(paths) != 1:
        raise RuntimeError(
            f"expected one {benchmark}/{config} metric in {root}, found {len(paths)}"
        )
    return paths[0]


def load(path: Path) -> dict[str, float]:
    metrics, _ = read_metrics(path)
    return metrics


def ratio(numerator: float, denominator: float) -> float:
    return 0.0 if denominator == 0 else numerator / denominator


def geomean(values: list[float]) -> float:
    if not values or any(value <= 0 for value in values):
        return float("nan")
    return math.exp(sum(math.log(value) for value in values) / len(values))


def row_for(benchmark: str, config: str, metrics: dict[str, float], baseline: float):
    queries = sum(
        metrics.get(f"typed_filter_{kind}_queries", 0.0)
        for kind in ("pattern", "resident", "pending", "seen")
    )
    false_positives = sum(
        metrics.get(f"typed_filter_{kind}_false_positives", 0.0)
        for kind in ("pattern", "resident", "pending", "seen")
    )
    return {
        "benchmark": benchmark,
        "config": config,
        "time_s": metrics["__driver_total_time"],
        "speedup": baseline / metrics["__driver_total_time"],
        "candidates": metrics.get("filter_prefetch_candidates", 0.0),
        "issued": metrics.get("filter_prefetch_issued", 0.0),
        "useful": metrics.get("filter_prefetch_useful", 0.0),
        "late": metrics.get("filter_prefetch_late", 0.0),
        "unused": metrics.get("filter_prefetch_unused", 0.0),
        "extra_dram_reads": metrics.get(
            "filter_prefetch_additional_dram_reads", 0.0
        ),
        "demand_delay_events": metrics.get(
            "filter_prefetch_demand_delay_events", 0.0
        ),
        "filter_queries": queries,
        "filter_false_positives": false_positives,
        "filter_fpr": ratio(false_positives, queries),
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--main-campaign", type=Path, required=True)
    parser.add_argument("--exact-campaign", type=Path, required=True)
    parser.add_argument("--benchmarks", required=True)
    args = parser.parse_args()
    benchmarks = tuple(x.strip() for x in args.benchmarks.split(",") if x.strip())
    rows = []
    for benchmark in benchmarks:
        baseline_metrics = load(metric_path(
            args.main_campaign, benchmark, "baseline"))
        baseline = baseline_metrics["__driver_total_time"]
        for config in MAIN_CONFIGS:
            metrics = load(metric_path(args.main_campaign, benchmark, config))
            rows.append(row_for(benchmark, config, metrics, baseline))
        exact = load(metric_path(args.exact_campaign, benchmark, EXACT_CONFIG))
        rows.append(row_for(benchmark, EXACT_CONFIG, exact, baseline))

    output_csv = args.exact_campaign / "cupath_coupling.csv"
    with output_csv.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)

    by_config = {
        config: [row for row in rows if row["config"] == config]
        for config in (*MAIN_CONFIGS, EXACT_CONFIG)
    }
    totals = {
        config: {
            key: sum(row[key] for row in selected)
            for key in (
                "candidates", "issued", "useful", "late", "unused",
                "extra_dram_reads", "demand_delay_events",
                "filter_queries", "filter_false_positives",
            )
        }
        for config, selected in by_config.items()
    }
    ungated = totals["ungated_prefetch"]
    coupled = totals["filter_coupled_prefetch"]
    exact = totals[EXACT_CONFIG]
    output_md = args.exact_campaign / "CUPATH_COUPLING_SUMMARY.md"
    lines = [
        "# Filter coupling evidence",
        "",
        "All points use one frozen binary and identical workload/sampling settings.",
        "",
        "| Configuration | Geomean | Candidates | Issued | Useful | Accuracy | Extra DRAM reads | Demand-delay events |",
        "|---|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for config in (*MAIN_CONFIGS, EXACT_CONFIG):
        selected = by_config[config]
        total = totals[config]
        lines.append(
            f"| {config} | {geomean([row['speedup'] for row in selected]):.4f}x | "
            f"{total['candidates']:.0f} | {total['issued']:.0f} | "
            f"{total['useful']:.0f} | {ratio(total['useful'], total['issued']):.3f} | "
            f"{total['extra_dram_reads']:.0f} | {total['demand_delay_events']:.0f} |"
        )
    lines += [
        "",
        "## Direct answers",
        "",
        f"- Cuckoo gating removes {1-ratio(coupled['issued'], ungated['issued']):.1%} "
        f"of issued candidates while retaining {ratio(coupled['useful'], ungated['useful']):.1%} "
        "of useful candidates.",
        f"- It changes extra DRAM reads from {ungated['extra_dram_reads']:.0f} to "
        f"{coupled['extra_dram_reads']:.0f} and accuracy from "
        f"{ratio(ungated['useful'], ungated['issued']):.3f} to "
        f"{ratio(coupled['useful'], coupled['issued']):.3f}.",
        f"- Exact metadata and Cuckoo metadata differ by "
        f"{(geomean([r['speedup'] for r in by_config[EXACT_CONFIG]]) / geomean([r['speedup'] for r in by_config['filter_coupled_prefetch']]) - 1):+.2%} "
        "in geomean speedup.",
        f"- Cuckoo measured false positives are "
        f"{coupled['filter_false_positives']:.0f}/{coupled['filter_queries']:.0f} "
        f"({ratio(coupled['filter_false_positives'], coupled['filter_queries']):.4%}); "
        f"the exact point reports {exact['filter_false_positives']:.0f}.",
        "",
        "Filter-only speedup isolates the RESIDENT negative lookup. The ungated-to-"
        "coupled comparison isolates candidate suppression; it does not claim that "
        "approximate membership itself performs the data movement.",
    ]
    output_md.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(output_csv)
    print(output_md)


if __name__ == "__main__":
    main()
