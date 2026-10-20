#!/usr/bin/env python3
"""Summarize CuPath M1/filter diagnostic campaigns.

Missing cells remain empty.  The script validates the same 16-MSHR and
four-slice invariants as the formal ablation plotter and reports speedup both
against the architectural baseline and, when present, against transformations
without approximate metadata.
"""

from __future__ import annotations

import argparse
import csv
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

from plot_cupath_typed_ablation import WORKLOADS, read_metrics


CONFIG_LABELS = {
    "baseline": "Baseline",
    "local_optimization": "Pure-64B local path",
    "cf_fast_miss_only": "CF fast miss",
    "exact_metadata_m1": "Exact metadata M1",
    "transformations_no_filter": "No filter",
    "exact_metadata_transformations": "Exact metadata",
    "cuckoo_metadata_transformations": "Cuckoo metadata",
    "cuckoo_filter_only": "Filter only",
    "predictor_only": "Predictor only",
    "ungated_prefetch": "Ungated prefetch",
    "filter_coupled_prefetch": "Filter-coupled",
    "exact_metadata_prefetch": "Exact + prefetch",
    "cuckoo_metadata_prefetch": "Cuckoo + prefetch",
    "m2": "M2",
    "m3": "M3",
    "complete_cupath": "Complete CuPath",
}
COLORS = (
    "#D6EFF5", "#ADDEEB", "#83CEE2", "#FBE0D0", "#F8C2A0",
    "#F4A371", "#5ABED8", "#ED6612", "#EAF7FA", "#FDF0E7",
)
CONFIG_COLORS = {
    "baseline": "#D6EFF5",
    "transformations_no_filter": "#83CEE2",
    "exact_metadata_transformations": "#F8C2A0",
    "cuckoo_metadata_transformations": "#F4A371",
    "cuckoo_filter_only": "#FBE0D0",
}
WORK_FIELDS = (
    "l2_resident_filter_read_bypasses",
    "l2_resident_filter_read_parallel_mshr_merges",
    "l2_miss_to_dram_issue_samples",
    "l2_miss_to_dram_issue_total_ns",
    "l2_to_dram_64b_requests",
    "remote_duplicate_reads",
    "remote_single_packets",
    "remote_bitmap_packets",
    "remote_first_touch_lines",
    "remote_second_touch_admissions",
    "remote_multiple_demand_admissions",
    "remote_installed_fills",
    "remote_requester_l2_hits",
    "remote_l2_logical_responses",
    "typed_filter_lookup_port_stalls",
    "typed_filter_update_port_stalls",
    "typed_filter_storage_bits",
    "typed_filter_pattern_queries",
    "typed_filter_pattern_positives",
    "typed_filter_pattern_false_positives",
    "typed_filter_pattern_insert_failures",
    "typed_filter_pending_positives",
    "typed_filter_resident_positives",
    "typed_filter_seen_positives",
    "filter_prefetch_real_demands",
    "filter_prefetch_candidates",
    "filter_prefetch_predictor_only_candidates",
    "filter_prefetch_ungated_candidates",
    "filter_prefetch_issued",
    "filter_prefetch_useful",
    "filter_prefetch_late",
    "filter_prefetch_unused",
    "filter_prefetch_demand_merges",
    "filter_prefetch_controller_busy_drops",
    "filter_prefetch_mshr_drops",
    "filter_prefetch_victim_drops",
    "filter_prefetch_output_busy_drops",
    "filter_prefetch_additional_dram_reads",
    "filter_prefetch_demand_delay_events",
    "remote_prefetch_candidates",
    "remote_prefetch_piggyback_lines",
    "remote_prefetch_no_existing_batch_drops",
    "remote_prefetch_batch_full_drops",
    "remote_prefetch_useful",
    "remote_prefetch_unused",
    "remote_prefetch_avoided_remote_requests",
    "remote_prefetch_added_response_bytes",
    "remote_prefetch_additional_owner_reads",
    "remote_prefetch_wire_lines",
    "remote_inflight_merges",
    "remote_bitmap_packets",
    "remote_requester_l2_hits",
    "remote_requester_l2_unused_fills",
    "remote_fill_into_invalid",
    "remote_dropped_fills",
    "remote_speculative_invalid_only_attempts",
    "remote_speculative_invalid_only_drops",
)


def geomean(values):
    values = list(values)
    if not values or any(not isinstance(value, (int, float)) or value <= 0 for value in values):
        return ""
    return math.exp(sum(math.log(value) for value in values) / len(values))


def load(root: Path, benchmarks, configs):
    campaign = {}
    for benchmark in benchmarks:
        for config in configs:
            matches = sorted(root.glob(f"*_{benchmark}_{config}_metrics.csv"))
            if len(matches) > 1:
                raise ValueError(f"duplicate result for {benchmark}/{config}: {matches}")
            if matches:
                campaign[(benchmark, config)], _ = read_metrics(matches[0])
    return campaign


def ratio(campaign, benchmark, config, reference):
    value = campaign.get((benchmark, config))
    base = campaign.get((benchmark, reference))
    if value is None or base is None:
        return ""
    return base["__driver_total_time"] / value["__driver_total_time"]


def write_tables(root, campaign, benchmarks, configs):
    speed_path = root / "cupath_diagnostic_speedup.csv"
    with speed_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "benchmark", "config", "time_s", "speedup_vs_baseline",
            "speedup_vs_transformations_no_filter",
        ])
        for benchmark in benchmarks:
            for config in configs:
                metrics = campaign.get((benchmark, config))
                writer.writerow([
                    benchmark,
                    config,
                    "" if metrics is None else metrics["__driver_total_time"],
                    ratio(campaign, benchmark, config, "baseline"),
                    ratio(
                        campaign, benchmark, config,
                        "transformations_no_filter",
                    ),
                ])
        for reference, column in (
            ("baseline", "geomean_vs_baseline"),
            ("transformations_no_filter", "geomean_vs_transformations_no_filter"),
        ):
            for config in configs:
                values = [ratio(campaign, b, config, reference) for b in benchmarks]
                writer.writerow([
                    "geomean", config, "", geomean(values) if column.endswith("baseline") else "",
                    geomean(values) if column.endswith("no_filter") else "",
                ])

    work_path = root / "cupath_diagnostic_work.csv"
    with work_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["benchmark", "config", *WORK_FIELDS])
        for benchmark in benchmarks:
            for config in configs:
                metrics = campaign.get((benchmark, config))
                if metrics is None:
                    writer.writerow([benchmark, config, *([""] * len(WORK_FIELDS))])
                else:
                    writer.writerow([
                        benchmark, config,
                        *(metrics.get(field, 0.0) for field in WORK_FIELDS),
                    ])
    return speed_path, work_path


def write_summary(root, campaign, benchmarks, configs):
    def gm(config):
        return geomean(
            ratio(campaign, benchmark, config, "baseline")
            for benchmark in benchmarks
        )

    def total(config, field):
        return sum(
            campaign.get((benchmark, config), {}).get(field, 0.0)
            for benchmark in benchmarks
        )

    lines = [
        "# CuPath campaign diagnostics",
        "",
        f"This campaign contains {len(benchmarks)} workloads. Geomeans include "
        "only configurations with a complete result for every listed workload.",
        "",
        "| Configuration | Geomean speedup |",
        "|---|---:|",
    ]
    for config in configs:
        value = gm(config)
        lines.append(
            f"| {CONFIG_LABELS.get(config, config)} | "
            + ("-- |" if value == "" else f"{value:.4f}x |")
        )

    lines += [
        "",
        "## Mechanism counters (sum across this screen)",
        "",
        "| Configuration | Candidates | Issued/piggybacked | Useful | Unused | Controller drop | Victim drop | Extra reads | Invalid-only admits/drops | Requester-L2 hits |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for config in configs:
        candidates = total(config, "filter_prefetch_candidates") + total(
            config, "remote_prefetch_candidates")
        issued = total(config, "filter_prefetch_issued") + total(
            config, "remote_prefetch_piggyback_lines")
        useful = total(config, "filter_prefetch_useful") + total(
            config, "remote_prefetch_useful")
        unused = total(config, "filter_prefetch_unused") + total(
            config, "remote_prefetch_unused")
        lines.append(
            f"| {CONFIG_LABELS.get(config, config)} | {candidates:.0f} | "
            f"{issued:.0f} | {useful:.0f} | {unused:.0f} | "
            f"{total(config, 'filter_prefetch_controller_busy_drops'):.0f} | "
            f"{total(config, 'filter_prefetch_victim_drops'):.0f} | "
            f"{total(config, 'filter_prefetch_additional_dram_reads') + total(config, 'remote_prefetch_additional_owner_reads'):.0f} | "
            f"{total(config, 'remote_speculative_invalid_only_attempts'):.0f}/"
            f"{total(config, 'remote_speculative_invalid_only_drops'):.0f} | "
            f"{total(config, 'remote_requester_l2_hits'):.0f} |"
        )

    no_filter = gm("transformations_no_filter")
    exact = gm("exact_metadata_transformations")
    cuckoo = gm("cuckoo_metadata_transformations")
    filter_only = gm("cuckoo_filter_only")
    if all(value != "" for value in (no_filter, exact, cuckoo, filter_only)):
        lines += [
            "",
            "Cuckoo and exact metadata differ by "
            f"{100.0 * (cuckoo / exact - 1.0):+.2f}% in this sample. "
            f"Filter-only reaches {filter_only:.4f}x, so the metadata lookup "
            "accounts only for the local definite-miss transformation. "
            f"The no-filter configuration differs from Cuckoo metadata by "
            f"{100.0 * (no_filter / cuckoo - 1.0):+.2f}% and serves as an "
            "analysis control for metadata-guided work elimination.",
        ]
    path = root / "CUPATH_DIAGNOSTIC_SUMMARY.md"
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return path


def plot(root, campaign, benchmarks, configs):
    labels = [
        dict((name, label) for name, label, _ in WORKLOADS).get(b, b)
        for b in benchmarks
    ] + ["GM"]
    fig, ax = plt.subplots(figsize=(3.45, 1.78))
    x = np.arange(len(labels), dtype=float)
    width = 0.78 / max(1, len(configs))
    all_heights = []
    for index, config in enumerate(configs):
        values = [ratio(campaign, b, config, "baseline") for b in benchmarks]
        gm = geomean(values)
        heights = [np.nan if value == "" else value for value in values]
        heights.append(np.nan if gm == "" else gm)
        all_heights.extend(value for value in heights if math.isfinite(value))
        offset = (index - (len(configs) - 1) / 2.0) * width
        ax.bar(
            x + offset, heights, width=width * 0.93,
            color=CONFIG_COLORS.get(config, COLORS[index % len(COLORS)]),
            edgecolor="none", linewidth=0,
            label=CONFIG_LABELS.get(config, config),
        )
    ax.axhline(1.0, color="#4C4C4D", linewidth=0.65)
    ax.set_xticks(x)
    ax.set_xticklabels(labels, fontsize=5.2)
    ax.set_ylabel("Speedup", fontsize=6.4)
    ax.tick_params(axis="y", labelsize=5.2, length=1.8, width=0.55)
    ax.tick_params(axis="x", length=1.6, width=0.55, pad=1)
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.4)
    ax.set_axisbelow(True)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.55)
    ax.legend(
        ncol=min(3, len(configs)), frameon=False, fontsize=4.6,
        loc="lower center", bbox_to_anchor=(0.5, 1.06),
        columnspacing=0.55, handlelength=0.85, handletextpad=0.25,
        borderaxespad=0,
    )
    if all_heights:
        ax.set_ylim(
            min(0.94, min(all_heights) - 0.03), max(all_heights) + 0.05
        )
    ax.set_xlim(-0.48, len(labels) - 0.52)
    fig.tight_layout(pad=0.22)
    output = root / "cupath_diagnostic_speedup.png"
    fig.savefig(
        output, dpi=300, bbox_inches="tight", pad_inches=0.025,
        facecolor="white", transparent=False,
    )
    plt.close(fig)
    return output


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("result_dir", type=Path)
    parser.add_argument("--benchmarks", required=True)
    parser.add_argument("--configs", required=True)
    parser.add_argument(
        "--plot-configs",
        help="comma-separated subset of --configs to include in the figure",
    )
    args = parser.parse_args()
    benchmarks = tuple(item.strip() for item in args.benchmarks.split(",") if item.strip())
    configs = tuple(item.strip() for item in args.configs.split(",") if item.strip())
    plot_configs = configs
    if args.plot_configs:
        plot_configs = tuple(
            item.strip() for item in args.plot_configs.split(",") if item.strip()
        )
        unknown = set(plot_configs) - set(configs)
        if unknown:
            raise SystemExit(
                "--plot-configs is not a subset of --configs: "
                + ", ".join(sorted(unknown))
            )
    root = args.result_dir.resolve()
    campaign = load(root, benchmarks, configs)
    outputs = (
        *write_tables(root, campaign, benchmarks, configs),
        write_summary(root, campaign, benchmarks, configs),
        plot(root, campaign, benchmarks, plot_configs),
    )
    print(f"Loaded {len(campaign)}/{len(benchmarks) * len(configs)} cells")
    for output in outputs:
        print(output)


if __name__ == "__main__":
    main()
