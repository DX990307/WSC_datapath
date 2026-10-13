#!/usr/bin/env python3
"""Generate paper figures that audit CuPath's mechanism-level claims.

The script deliberately separates traffic attribution from performance
ablation.  Existing Complete and M1/M2 counters are sufficient for the three
work-reduction rates and the Local Pairing diagnostics.  Same-line-only and
same-page-only speedups are emitted only when their result files exist.
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

from plot_cupath_typed_ablation import WORKLOADS


BLUE_100 = "#D6EFF5"
BLUE_200 = "#ADDEEB"
BLUE_300 = "#83CEE2"
BLUE_400 = "#5ABED8"
BLUE_500 = "#31ADCE"
ORANGE_100 = "#FBE0D0"
ORANGE_200 = "#F8C2A0"
ORANGE_300 = "#F4A371"
ORANGE_400 = "#F18541"
GRAY = "#656667"
TEXT = "#323334"
LINE_BYTES = 64.0


def ratio(numerator: float, denominator: float) -> float:
    return math.nan if denominator <= 0 else numerator / denominator


def percent(numerator: float, denominator: float) -> float:
    value = ratio(numerator, denominator)
    return value if math.isnan(value) else 100.0 * value


def sum_metrics(path: Path) -> dict[str, float]:
    totals: dict[str, float] = {}
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            name = row["what"].strip()
            try:
                value = float(row["value"])
            except (TypeError, ValueError):
                continue
            totals[name] = totals.get(name, 0.0) + value
    return totals


def read_table(path: Path, key: str = "benchmark") -> dict[str, dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as stream:
        return {row[key]: row for row in csv.DictReader(stream)}


def field(row: dict[str, str], name: str) -> float:
    value = row.get(name, "")
    return float(value) if value not in ("", None) else 0.0


def style_axis(ax, grid: bool = True):
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color(GRAY)
        spine.set_linewidth(0.65)
    ax.tick_params(axis="both", colors=TEXT, width=0.55, length=2.0)
    if grid:
        ax.grid(axis="y", color="#E5E5E6", linewidth=0.45, zorder=0)
    ax.set_axisbelow(True)


def finish(fig, path: Path):
    fig.savefig(path.with_suffix(".pdf"), bbox_inches="tight", pad_inches=0.025)
    fig.savefig(
        path.with_suffix(".png"), dpi=400, bbox_inches="tight",
        pad_inches=0.025, facecolor="white", transparent=False,
    )
    plt.close(fig)


def write_csv(path: Path, fields: list[str], rows: list[dict[str, object]]):
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def plot_remote_work(complete_dir: Path, output_dir: Path):
    work = read_table(complete_dir / "cupath_work_reduction.csv")
    rows: list[dict[str, object]] = []
    totals = {
        "dedup_num": 0.0, "logical": 0.0,
        "packet_num": 0.0, "wire": 0.0,
        "traversal_num": 0.0,
    }
    labels: list[str] = []
    values: list[list[float]] = []
    for benchmark, label, _ in WORKLOADS:
        row = work[benchmark]
        logical = field(row, "complete_remote_logical_reads")
        duplicate = field(row, "remote_duplicate_reads_eliminated")
        wire = field(row, "complete_remote_wire_lines")
        packets = field(row, "complete_remote_packets")
        packet_removed = max(0.0, wire - packets)
        traversal = field(row, "repeated_wafer_traversals_eliminated")
        metric = [
            0.0 if logical == 0 else percent(duplicate, logical),
            0.0 if wire == 0 else percent(packet_removed, wire),
            0.0 if logical == 0 else percent(traversal, logical),
        ]
        labels.append(label)
        values.append(metric)
        rows.append({
            "benchmark": label,
            "remote_logical_reads": logical,
            "same_line_reads_deduplicated": duplicate,
            "same_line_dedup_pct": metric[0],
            "post_dedup_wire_lines": wire,
            "request_packets": packets,
            "same_page_packets_removed": packet_removed,
            "same_page_packet_reduction_pct": metric[1],
            "repeated_traversals_eliminated": traversal,
            "repeated_traversal_elimination_pct": metric[2],
        })
        totals["dedup_num"] += duplicate
        totals["logical"] += logical
        totals["packet_num"] += packet_removed
        totals["wire"] += wire
        totals["traversal_num"] += traversal

    weighted = [
        percent(totals["dedup_num"], totals["logical"]),
        percent(totals["packet_num"], totals["wire"]),
        percent(totals["traversal_num"], totals["logical"]),
    ]
    labels.append("Weighted")
    values.append(weighted)
    rows.append({
        "benchmark": "Weighted",
        "remote_logical_reads": totals["logical"],
        "same_line_reads_deduplicated": totals["dedup_num"],
        "same_line_dedup_pct": weighted[0],
        "post_dedup_wire_lines": totals["wire"],
        "request_packets": totals["wire"] - totals["packet_num"],
        "same_page_packets_removed": totals["packet_num"],
        "same_page_packet_reduction_pct": weighted[1],
        "repeated_traversals_eliminated": totals["traversal_num"],
        "repeated_traversal_elimination_pct": weighted[2],
    })
    fields = list(rows[0])
    write_csv(output_dir / "cupath_remote_work_reduction.csv", fields, rows)

    data = np.asarray(values)
    x = np.arange(len(labels))
    width = 0.245
    fig, ax = plt.subplots(figsize=(3.45, 1.58))
    series = (
        ("Same-line dedup", BLUE_300),
        ("Same-page packetization", ORANGE_200),
        ("Repeated traversal", ORANGE_400),
    )
    for index, (name, color) in enumerate(series):
        ax.bar(
            x + (index - 1) * width, data[:, index], width,
            label=name, color=color, edgecolor="white", linewidth=0.3,
            zorder=3,
        )
    ax.axvline(len(labels) - 1.5, color=GRAY, linewidth=0.7)
    for index, value in enumerate(weighted):
        ax.text(
            x[-1] + (index - 1) * width, value + 2.0, f"{value:.1f}",
            ha="center", va="bottom", fontsize=4.8, color=TEXT,
        )
    ax.set_ylabel("Work removed (%)", fontsize=6.1)
    ax.set_ylim(0, 105)
    ax.set_yticks((0, 25, 50, 75, 100))
    ax.set_xticks(x, labels, rotation=30, ha="right", fontsize=5.4)
    ax.tick_params(axis="y", labelsize=5.2)
    ax.legend(
        loc="lower left", bbox_to_anchor=(0, 1.02, 1, 0.10),
        ncol=3, mode="expand", borderaxespad=0,
        frameon=False, fontsize=4.7, columnspacing=0.5,
        handlelength=0.9, handletextpad=0.3,
    )
    style_axis(ax)
    fig.tight_layout(pad=0.2)
    finish(fig, output_dir / "cupath_remote_work_reduction")


def optional_speedup(
    baseline_dir: Path,
    experiment_dir: Path | None,
    benchmark: str,
    config: str,
) -> float:
    if experiment_dir is None:
        return math.nan
    baseline = baseline_dir / f"baseline_{benchmark}_baseline_metrics.csv"
    candidate = experiment_dir / f"baseline_{benchmark}_{config}_metrics.csv"
    if not baseline.exists() or not candidate.exists():
        return math.nan
    baseline_time = sum_metrics(baseline).get("total_time", 0.0)
    candidate_time = sum_metrics(candidate).get("total_time", 0.0)
    return ratio(baseline_time, candidate_time)


def plot_remote_aggregation(
    m2_dir: Path,
    baseline_dir: Path,
    split_dir: Path | None,
    output_dir: Path,
):
    labels: list[str] = []
    traffic: list[list[float]] = []
    performance: list[list[float]] = []
    rows: list[dict[str, object]] = []
    traffic_totals = [0.0, 0.0, 0.0, 0.0]
    speedup_products = [1.0, 1.0, 1.0]
    speedup_counts = [0, 0, 0]
    for benchmark, label, _ in WORKLOADS:
        metrics = sum_metrics(m2_dir / f"baseline_{benchmark}_m2_metrics.csv")
        logical = metrics.get("remote_logical_reads", 0.0)
        duplicates = metrics.get("remote_duplicate_reads", 0.0)
        wire = metrics.get("remote_wire_lines", 0.0)
        packets = (
            metrics.get("remote_single_packets", 0.0) +
            metrics.get("remote_bitmap_packets", 0.0)
        )
        packet_removed = max(0.0, wire - packets)
        traffic_values = [
            0.0 if logical == 0 else percent(duplicates, logical),
            0.0 if wire == 0 else percent(packet_removed, wire),
        ]
        split_values = [
            optional_speedup(
                baseline_dir, split_dir, benchmark, "m2_same_line_only"),
            optional_speedup(
                baseline_dir, split_dir, benchmark, "m2_same_page_only"),
            optional_speedup(baseline_dir, m2_dir, benchmark, "m2"),
        ]
        # The formal M2 result always resides in m2_dir.  Keep the helper's
        # explicit existence check, then fill from that directory.
        split_values[2] = optional_speedup(
            baseline_dir, m2_dir, benchmark, "m2")
        labels.append(label)
        traffic.append(traffic_values)
        performance.append(split_values)
        rows.append({
            "benchmark": label,
            "remote_logical_reads": logical,
            "same_line_reads_deduplicated": duplicates,
            "same_line_dedup_pct": traffic_values[0],
            "post_dedup_wire_lines": wire,
            "same_page_packets_removed": packet_removed,
            "same_page_packet_reduction_pct": traffic_values[1],
            "same_line_only_speedup": split_values[0],
            "same_page_only_speedup": split_values[1],
            "combined_m2_speedup": split_values[2],
        })
        traffic_totals[0] += duplicates
        traffic_totals[1] += logical
        traffic_totals[2] += packet_removed
        traffic_totals[3] += wire
        for i, value in enumerate(split_values):
            if math.isfinite(value) and value > 0:
                speedup_products[i] *= value
                speedup_counts[i] += 1

    weighted_traffic = [
        percent(traffic_totals[0], traffic_totals[1]),
        percent(traffic_totals[2], traffic_totals[3]),
    ]
    geomeans = [
        product ** (1.0 / count) if count == len(WORKLOADS) else math.nan
        for product, count in zip(speedup_products, speedup_counts)
    ]
    labels.append("Weighted")
    traffic.append(weighted_traffic)
    performance.append(geomeans)
    rows.append({
        "benchmark": "Weighted/GMEAN",
        "remote_logical_reads": traffic_totals[1],
        "same_line_reads_deduplicated": traffic_totals[0],
        "same_line_dedup_pct": weighted_traffic[0],
        "post_dedup_wire_lines": traffic_totals[3],
        "same_page_packets_removed": traffic_totals[2],
        "same_page_packet_reduction_pct": weighted_traffic[1],
        "same_line_only_speedup": geomeans[0],
        "same_page_only_speedup": geomeans[1],
        "combined_m2_speedup": geomeans[2],
    })
    write_csv(
        output_dir / "cupath_remote_aggregation_breakdown.csv",
        list(rows[0]), rows,
    )

    x = np.arange(len(labels))
    fig, (top, bottom) = plt.subplots(
        2, 1, figsize=(3.45, 3.05), sharex=True,
        gridspec_kw={"height_ratios": (1.0, 1.05), "hspace": 0.13},
    )
    traffic_data = np.asarray(traffic)
    width = 0.36
    top.bar(
        x - width / 2, traffic_data[:, 0], width,
        label="Same-line dedup", color=BLUE_300, edgecolor="white",
        linewidth=0.3, zorder=3,
    )
    top.bar(
        x + width / 2, traffic_data[:, 1], width,
        label="Same-page packetization", color=ORANGE_200,
        edgecolor="white", linewidth=0.3, zorder=3,
    )
    top.set_ylabel("Traffic removed (%)", fontsize=5.8)
    top.set_ylim(0, 105)
    top.set_yticks((0, 25, 50, 75, 100))
    top.tick_params(axis="y", labelsize=5.0)
    top.legend(
        loc="upper center", bbox_to_anchor=(0.5, 1.24), ncol=2,
        frameon=False, fontsize=5.0, columnspacing=0.9,
    )
    style_axis(top)

    perf_data = np.asarray(performance)
    pwidth = 0.25
    perf_series = (
        ("Same-line only", BLUE_200),
        ("Same-page only", ORANGE_100),
        ("Combined M2", ORANGE_300),
    )
    for index, (name, color) in enumerate(perf_series):
        bottom.bar(
            x + (index - 1) * pwidth, perf_data[:, index], pwidth,
            label=name, color=color, edgecolor="white", linewidth=0.3,
            zorder=3,
        )
    bottom.axhline(1.0, color=GRAY, linewidth=0.7, zorder=2)
    bottom.axvline(len(labels) - 1.5, color=GRAY, linewidth=0.7)
    bottom.set_ylabel("Speedup", fontsize=5.8)
    finite = perf_data[np.isfinite(perf_data)]
    ymax = max(1.25, (finite.max() * 1.08) if finite.size else 1.25)
    bottom.set_ylim(0, ymax)
    bottom.tick_params(axis="y", labelsize=5.0)
    bottom.set_xticks(x, labels, rotation=45, ha="right", fontsize=5.0)
    bottom.legend(
        loc="upper center", bbox_to_anchor=(0.5, 1.13), ncol=3,
        frameon=False, fontsize=4.8, columnspacing=0.7, handlelength=1.0,
    )
    style_axis(bottom)
    fig.subplots_adjust(left=0.12, right=0.99, bottom=0.16, top=0.93)
    finish(fig, output_dir / "cupath_remote_aggregation_breakdown")


def plot_local_pairing(
    m1_dir: Path,
    baseline_dir: Path,
    output_dir: Path,
):
    labels: list[str] = []
    quality: list[list[float]] = []
    cost: list[list[float]] = []
    staging: list[list[float]] = []
    rows: list[dict[str, object]] = []
    sums = {
        "predictions": 0.0, "useful": 0.0, "misses": 0.0,
        "timely": 0.0, "unused": 0.0, "evictions": 0.0,
        "m1_acts": 0.0, "baseline_acts": 0.0,
        "m1_read_bytes": 0.0, "baseline_read_bytes": 0.0,
    }
    for benchmark, label, _ in WORKLOADS:
        m1 = sum_metrics(m1_dir / f"baseline_{benchmark}_m1_metrics.csv")
        baseline = sum_metrics(
            baseline_dir / f"baseline_{benchmark}_baseline_metrics.csv"
        )
        predictions = m1.get("adaptive_pair_predictions", 0.0)
        timely = m1.get("adaptive_pair_buffer_hits", 0.0)
        late = m1.get("adaptive_pair_inflight_hits", 0.0)
        useful = timely + late
        misses = m1.get("adaptive_pair_miss_lines_seen", 0.0)
        unused = m1.get("adaptive_pair_prefetch_unused", 0.0)
        evictions = m1.get("adaptive_pair_prefetch_unused_evictions", 0.0)
        m1_acts = m1.get("dram_row_activate_commands", 0.0)
        baseline_acts = baseline.get("dram_row_activate_commands", 0.0)
        m1_read_bytes = m1.get("dram_frontend_read_bytes", 0.0)
        baseline_read_bytes = baseline.get("dram_frontend_read_bytes", 0.0)
        q = [
            percent(useful, predictions),
            percent(useful, misses),
            percent(timely, useful),
        ]
        c = [
            percent(unused, predictions),
            percent(m1_read_bytes - baseline_read_bytes, baseline_read_bytes),
            percent(m1_acts - baseline_acts, baseline_acts),
        ]
        s = [
            1000.0 * ratio(timely, predictions),
            1000.0 * ratio(evictions, predictions),
        ]
        labels.append(label)
        quality.append(q)
        cost.append(c)
        staging.append(s)
        rows.append({
            "benchmark": label,
            "issued_pairs": predictions,
            "useful_pairs": useful,
            "timely_buffer_hits": timely,
            "late_inflight_hits": late,
            "local_miss_lines_seen": misses,
            "prediction_accuracy_pct": q[0],
            "prediction_coverage_pct": q[1],
            "prediction_timeliness_pct": q[2],
            "classified_unused_lines": unused,
            "unused_adjacent_lines_pct": c[0],
            "gross_adjacent_hbm_bytes": predictions * LINE_BYTES,
            "classified_unused_hbm_bytes": unused * LINE_BYTES,
            "classified_unused_bytes_over_demand_miss_bytes_pct": percent(
                unused * LINE_BYTES, misses * LINE_BYTES
            ),
            "baseline_hbm_read_bytes": baseline_read_bytes,
            "m1_hbm_read_bytes": m1_read_bytes,
            "additional_hbm_read_bytes": m1_read_bytes - baseline_read_bytes,
            "hbm_read_byte_change_pct": c[1],
            "baseline_row_activations": baseline_acts,
            "m1_row_activations": m1_acts,
            "row_activation_change_pct": c[2],
            "staging_buffer_hits_per_1k_pairs": s[0],
            "staging_buffer_evictions": evictions,
            "staging_buffer_evictions_per_1k_pairs": s[1],
        })
        for key, value in (
            ("predictions", predictions), ("useful", useful),
            ("misses", misses), ("timely", timely), ("unused", unused),
            ("evictions", evictions), ("m1_acts", m1_acts),
            ("baseline_acts", baseline_acts),
            ("m1_read_bytes", m1_read_bytes),
            ("baseline_read_bytes", baseline_read_bytes),
        ):
            sums[key] += value

    weighted_quality = [
        percent(sums["useful"], sums["predictions"]),
        percent(sums["useful"], sums["misses"]),
        percent(sums["timely"], sums["useful"]),
    ]
    weighted_cost = [
        percent(sums["unused"], sums["predictions"]),
        percent(
            sums["m1_read_bytes"] - sums["baseline_read_bytes"],
            sums["baseline_read_bytes"],
        ),
        percent(sums["m1_acts"] - sums["baseline_acts"], sums["baseline_acts"]),
    ]
    weighted_staging = [
        1000.0 * ratio(sums["timely"], sums["predictions"]),
        1000.0 * ratio(sums["evictions"], sums["predictions"]),
    ]
    labels.append("Weighted")
    quality.append(weighted_quality)
    cost.append(weighted_cost)
    staging.append(weighted_staging)
    rows.append({
        "benchmark": "Weighted",
        "issued_pairs": sums["predictions"],
        "useful_pairs": sums["useful"],
        "timely_buffer_hits": sums["timely"],
        "late_inflight_hits": sums["useful"] - sums["timely"],
        "local_miss_lines_seen": sums["misses"],
        "prediction_accuracy_pct": weighted_quality[0],
        "prediction_coverage_pct": weighted_quality[1],
        "prediction_timeliness_pct": weighted_quality[2],
        "classified_unused_lines": sums["unused"],
        "unused_adjacent_lines_pct": weighted_cost[0],
        "gross_adjacent_hbm_bytes": sums["predictions"] * LINE_BYTES,
        "classified_unused_hbm_bytes": sums["unused"] * LINE_BYTES,
        "classified_unused_bytes_over_demand_miss_bytes_pct": percent(
            sums["unused"] * LINE_BYTES, sums["misses"] * LINE_BYTES
        ),
        "baseline_hbm_read_bytes": sums["baseline_read_bytes"],
        "m1_hbm_read_bytes": sums["m1_read_bytes"],
        "additional_hbm_read_bytes": (
            sums["m1_read_bytes"] - sums["baseline_read_bytes"]
        ),
        "hbm_read_byte_change_pct": weighted_cost[1],
        "baseline_row_activations": sums["baseline_acts"],
        "m1_row_activations": sums["m1_acts"],
        "row_activation_change_pct": weighted_cost[2],
        "staging_buffer_hits_per_1k_pairs": weighted_staging[0],
        "staging_buffer_evictions": sums["evictions"],
        "staging_buffer_evictions_per_1k_pairs": weighted_staging[1],
    })
    write_csv(
        output_dir / "cupath_local_pairing_diagnostics.csv",
        list(rows[0]), rows,
    )

    # The paper text reports aggregate outcomes.  Three compact panels keep
    # the different units legible in the same 3.45 x 1.58 inch footprint as
    # the PASTA ablation figure.  Per-workload values remain in the CSV above.
    fig, axes = plt.subplots(
        1, 3, figsize=(3.45, 1.58),
        gridspec_kw={"wspace": 0.52},
    )
    panels = (
        (
            axes[0], weighted_quality,
            ("Accuracy", "Coverage", "Timely"),
            (BLUE_300, BLUE_500, ORANGE_200),
            "Prediction (%)", (0, 105), (0, 50, 100),
        ),
        (
            axes[1], weighted_cost,
            ("Unused", "Read bytes", "Row ACT"),
            (ORANGE_100, ORANGE_300, BLUE_300),
            "HBM effect (%)", (-15, 25), (-10, 0, 10, 20),
        ),
        (
            axes[2], weighted_staging,
            ("Hits", "Evictions"),
            (BLUE_300, ORANGE_300),
            "Events / 1K pairs", (0, 105), (0, 50, 100),
        ),
    )
    for ax, values, names, colors, title, limits, ticks in panels:
        positions = np.arange(len(values))
        bars = ax.bar(
            positions, values, width=0.62, color=colors,
            edgecolor="white", linewidth=0.3, zorder=3,
        )
        ax.axhline(0, color=GRAY, linewidth=0.65, zorder=2)
        ax.set_title(title, fontsize=5.6, pad=2.0)
        ax.set_ylim(*limits)
        ax.set_yticks(ticks)
        ax.set_xticks(positions, names, rotation=30, ha="right", fontsize=4.5)
        ax.tick_params(axis="y", labelsize=4.7)
        for bar, value in zip(bars, values):
            y = value + 2.0 if value >= 0 else value - 2.0
            ax.text(
                bar.get_x() + bar.get_width() / 2, y, f"{value:.1f}",
                ha="center", va="bottom" if value >= 0 else "top",
                fontsize=4.5, color=TEXT, clip_on=False,
            )
        style_axis(ax)
    fig.subplots_adjust(left=0.08, right=0.995, bottom=0.25, top=0.88)
    finish(fig, output_dir / "cupath_local_pairing_diagnostics")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--complete-dir", type=Path,
        default=Path("akkalat/results/2026-07-23-23-41-05-sampled-validation"),
    )
    parser.add_argument(
        "--m2-dir", type=Path,
        default=Path("akkalat/results/2026-07-22-m1-m2-m3-rerun"),
    )
    parser.add_argument(
        "--baseline-dir", type=Path,
        default=Path("akkalat/results/2026-07-22-baseline-complete-observation"),
    )
    parser.add_argument(
        "--split-dir", type=Path,
        help="Optional directory containing m2_same_line_only and "
             "m2_same_page_only results.",
    )
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    output_dir = args.output_dir or args.complete_dir
    output_dir.mkdir(parents=True, exist_ok=True)

    plot_remote_work(args.complete_dir, output_dir)
    plot_remote_aggregation(
        args.m2_dir, args.baseline_dir, args.split_dir, output_dir,
    )
    plot_local_pairing(args.complete_dir, args.baseline_dir, output_dir)
    print(output_dir / "cupath_remote_work_reduction.pdf")
    print(output_dir / "cupath_remote_aggregation_breakdown.pdf")
    print(output_dir / "cupath_local_pairing_diagnostics.pdf")


if __name__ == "__main__":
    main()
