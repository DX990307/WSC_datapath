#!/usr/bin/env python3
"""Plot first Mechanism 1 figures from offline experiment CSVs."""

from __future__ import annotations

import argparse
import csv
import os
from collections import defaultdict
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


def read_csv(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    with path.open(newline="") as f:
        return list(csv.DictReader(f))


def as_float(value: str, default: float = 0.0) -> float:
    if value is None or value == "":
        return default
    return float(value)


def plot_fifo_fragmentation(rows: list[dict[str, str]], out_dir: Path) -> None:
    selected = [
        row
        for row in rows
        if row["request_class"] == "all" and int(row["window_size"]) == 160
    ]
    if not selected:
        return
    selected.sort(
        key=lambda row: as_float(row["window_visible_largest_page_group_avg"]),
        reverse=True,
    )

    workloads = [row["workload"] for row in selected]
    fifo = [
        as_float(row["fifo_consecutive_largest_page_group_avg"]) for row in selected
    ]
    visible = [as_float(row["window_visible_largest_page_group_avg"]) for row in selected]
    x = list(range(len(workloads)))
    width = 0.38

    fig, ax = plt.subplots(figsize=(max(8, len(workloads) * 0.75), 4.2))
    ax.bar([i - width / 2 for i in x], fifo, width, label="FIFO consecutive")
    ax.bar([i + width / 2 for i in x], visible, width, label="Window-visible")
    ax.set_ylabel("Largest same-page group / 160-request window")
    ax.set_xticks(x)
    ax.set_xticklabels(workloads, rotation=35, ha="right")
    ax.legend(frameon=False)
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    fig.savefig(out_dir / "m1_fifo_fragmentation.pdf")
    fig.savefig(out_dir / "m1_fifo_fragmentation.png", dpi=180)
    plt.close(fig)


def plot_cache_dram_tradeoff(rows: list[dict[str, str]], out_dir: Path) -> None:
    selected = [
        row
        for row in rows
        if row["request_class"] == "all"
        and int(row["window_size"]) == 160
        and row["max_age_ns"] == "unlimited"
    ]
    if not selected:
        return

    by_workload: dict[str, dict[str, dict[str, str]]] = defaultdict(dict)
    for row in selected:
        by_workload[row["workload"]][row["policy"]] = row

    xs = []
    ys = []
    labels = []
    for workload, policy_rows in by_workload.items():
        fifo = policy_rows.get("fifo")
        if not fifo:
            continue
        fifo_reuse = as_float(fifo["same_page_reuse_distance_avg"], 0.0)
        fifo_row_hit = as_float(fifo["row_buffer_hit_rate"], 0.0)
        for policy, row in policy_rows.items():
            reuse = as_float(row["same_page_reuse_distance_avg"], fifo_reuse)
            row_hit = as_float(row["row_buffer_hit_rate"], fifo_row_hit)
            reuse_reduction = (
                (fifo_reuse - reuse) / fifo_reuse if fifo_reuse > 0 else 0.0
            )
            row_hit_gain = row_hit - fifo_row_hit
            xs.append(reuse_reduction)
            ys.append(row_hit_gain)
            labels.append((workload, policy))

    if not xs:
        return
    fig, ax = plt.subplots(figsize=(6.2, 4.6))
    colors = {
        "fifo": "#4C78A8",
        "l2_only": "#59A14F",
        "dram_only": "#F28E2B",
        "hlq": "#E15759",
    }
    for (workload, policy), x, y in zip(labels, xs, ys):
        ax.scatter(x, y, s=48, color=colors.get(policy, "#777777"), label=policy)
        if policy == "hlq":
            ax.annotate(workload, (x, y), fontsize=7, xytext=(3, 3), textcoords="offset points")
    handles, handle_labels = ax.get_legend_handles_labels()
    dedup = dict(zip(handle_labels, handles))
    ax.legend(dedup.values(), dedup.keys(), frameon=False)
    ax.axhline(0, color="#999999", linewidth=0.8)
    ax.axvline(0, color="#999999", linewidth=0.8)
    ax.set_xlabel("Same-page reuse-distance reduction vs FIFO")
    ax.set_ylabel("Row-buffer hit-rate gain vs FIFO")
    ax.grid(alpha=0.25)
    fig.tight_layout()
    fig.savefig(out_dir / "m1_cache_dram_tradeoff.pdf")
    fig.savefig(out_dir / "m1_cache_dram_tradeoff.png", dpi=180)
    plt.close(fig)


def plot_window_sensitivity(rows: list[dict[str, str]], out_dir: Path) -> None:
    selected = [
        row
        for row in rows
        if row["request_class"] == "all"
        and row["policy"] in {"fifo", "hlq"}
        and row["max_age_ns"] == "unlimited"
    ]
    if not selected:
        return
    by_key: dict[tuple[str, int, str], dict[str, str]] = {}
    for row in selected:
        by_key[(row["workload"], int(row["window_size"]), row["policy"])] = row

    by_window: dict[int, list[tuple[float, float]]] = defaultdict(list)
    for workload, window_size, policy in list(by_key):
        if policy != "hlq":
            continue
        fifo = by_key.get((workload, window_size, "fifo"))
        hlq = by_key[(workload, window_size, "hlq")]
        if not fifo:
            continue
        fifo_reuse = as_float(fifo["same_page_reuse_distance_avg"], 0.0)
        hlq_reuse = as_float(hlq["same_page_reuse_distance_avg"], fifo_reuse)
        reuse_reduction = (
            (fifo_reuse - hlq_reuse) / fifo_reuse if fifo_reuse > 0 else 0.0
        )
        row_gain = as_float(hlq["row_buffer_hit_rate"]) - as_float(
            fifo["row_buffer_hit_rate"]
        )
        by_window[window_size].append((reuse_reduction, row_gain))

    windows = sorted(by_window)
    if not windows:
        return
    reuse = [
        sum(item[0] for item in by_window[window]) / len(by_window[window])
        for window in windows
    ]
    row_gain = [
        sum(item[1] for item in by_window[window]) / len(by_window[window])
        for window in windows
    ]

    fig, ax = plt.subplots(figsize=(5.8, 4.0))
    ax.plot(windows, reuse, marker="o", label="Same-page reuse reduction")
    ax.plot(windows, row_gain, marker="s", label="Row-hit gain")
    ax.set_xlabel("Window size")
    ax.set_ylabel("Average improvement vs FIFO")
    ax.set_xticks(windows)
    ax.legend(frameon=False)
    ax.grid(alpha=0.25)
    fig.tight_layout()
    fig.savefig(out_dir / "m1_window_sensitivity.pdf")
    fig.savefig(out_dir / "m1_window_sensitivity.png", dpi=180)
    plt.close(fig)


def plot_age_bound_cost(rows: list[dict[str, str]], out_dir: Path) -> None:
    selected = [
        row
        for row in rows
        if row["request_class"] == "all"
        and row["policy"] in {"fifo", "hlq"}
        and int(row["window_size"]) == 160
    ]
    if not selected:
        return
    by_key: dict[tuple[str, str, str], dict[str, str]] = {}
    for row in selected:
        by_key[(row["workload"], row["policy"], row["max_age_ns"])] = row

    age_order = ["unlimited", "100", "50"]
    locality_by_age: dict[str, list[float]] = defaultdict(list)
    wait_by_age: dict[str, list[float]] = defaultdict(list)

    for (workload, policy, age), row in by_key.items():
        if policy != "hlq":
            continue
        fifo = by_key.get((workload, "fifo", age))
        if not fifo:
            continue
        fifo_reuse = as_float(fifo["same_page_reuse_distance_avg"], 0.0)
        hlq_reuse = as_float(row["same_page_reuse_distance_avg"], fifo_reuse)
        locality = (fifo_reuse - hlq_reuse) / fifo_reuse if fifo_reuse > 0 else 0.0
        locality_by_age[age].append(locality)
        wait_by_age[age].append(as_float(row["p95_queue_wait_ns"]))

    ages = [age for age in age_order if age in locality_by_age]
    if not ages:
        return
    locality = [
        sum(locality_by_age[age]) / len(locality_by_age[age]) for age in ages
    ]
    waits = [sum(wait_by_age[age]) / len(wait_by_age[age]) for age in ages]

    fig, ax1 = plt.subplots(figsize=(5.8, 4.0))
    x = list(range(len(ages)))
    ax1.plot(x, locality, marker="o", color="#4C78A8", label="Locality benefit")
    ax1.set_ylabel("Same-page reuse reduction vs FIFO", color="#4C78A8")
    ax1.tick_params(axis="y", labelcolor="#4C78A8")
    ax1.set_xticks(x)
    ax1.set_xticklabels(ages)
    ax1.set_xlabel("Max age ns")
    ax2 = ax1.twinx()
    ax2.plot(x, waits, marker="s", color="#E15759", label="p95 queue wait")
    ax2.set_ylabel("HLQ p95 queue wait ns", color="#E15759")
    ax2.tick_params(axis="y", labelcolor="#E15759")
    ax1.grid(alpha=0.25)
    fig.tight_layout()
    fig.savefig(out_dir / "m1_age_bound_cost.pdf")
    fig.savefig(out_dir / "m1_age_bound_cost.png", dpi=180)
    plt.close(fig)


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Plot M1 experiment results.")
    parser.add_argument(
        "--m1-dir",
        type=Path,
        required=True,
        help="Directory containing m1_summary.csv and m1_fragmentation.csv.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Plot directory. Defaults to <m1-dir>/plots.",
    )
    return parser


def main() -> None:
    args = build_arg_parser().parse_args()
    out_dir = args.out_dir or args.m1_dir / "plots"
    out_dir.mkdir(parents=True, exist_ok=True)
    fragmentation = read_csv(args.m1_dir / "m1_fragmentation.csv")
    summary = read_csv(args.m1_dir / "m1_summary.csv")

    plot_fifo_fragmentation(fragmentation, out_dir)
    plot_cache_dram_tradeoff(summary, out_dir)
    plot_window_sensitivity(summary, out_dir)
    plot_age_bound_cost(summary, out_dir)
    print(f"[m1] wrote plots to {out_dir}")


if __name__ == "__main__":
    main()
