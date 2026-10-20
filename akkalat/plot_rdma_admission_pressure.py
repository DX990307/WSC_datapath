#!/usr/bin/env python3
"""Plot baseline RDMA saturation and M2 admission-pressure reduction."""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
from matplotlib.patches import Patch


BLUE = "#83CEE2"
BLUE_DARK = "#278BA5"
ORANGE = "#F4A371"
ORANGE_DARK = "#BE520E"
GRID = "#E5E5E6"
TEXT = "#4C4C4D"

WORKLOADS = (
    ("aes", "AES"),
    ("fir", "FIR"),
    ("floydwarshall", "FWS"),
    ("im2col", "I2C"),
    ("kmeans", "KM"),
    ("matrixmultiplication", "MM"),
    ("matrixtranspose", "MT"),
    ("pagerank", "PR"),
    ("simpleconvolution", "SC"),
    ("spmv", "SPMV"),
)

SUM_METRICS = {
    "rdma_observed_remote_reads",
    "remote_logical_reads",
    "rdma_outstanding_full_stalls",
    "rdma_requester_outstanding_full_stalls",
    "rdma_owner_outstanding_full_stalls",
}
MAX_METRICS = {
    "rdma_max_outstanding",
    "rdma_peak_outstanding",
}
DRIVER_METRICS = {"max_wg_completed", "total_wg_count"}


def binary_digest(root: Path) -> str:
    path = root / "EXPERIMENT_BINARIES.json"
    payload = json.loads(path.read_text(encoding="utf-8"))
    digest = payload.get("sha256_by_target", {}).get("baseline", "")
    if len(digest) != 64:
        raise ValueError(f"invalid binary digest in {path}")
    return digest


def read_metrics(path: Path) -> dict[str, float]:
    values = {name: 0.0 for name in SUM_METRICS | MAX_METRICS}
    drivers = {}
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.reader(stream):
            if len(row) < 4:
                continue
            component = row[1].strip()
            metric = row[2].strip()
            try:
                value = float(row[3])
            except ValueError:
                continue
            if metric in SUM_METRICS:
                values[metric] += value
            elif metric in MAX_METRICS:
                values[metric] = max(values[metric], value)
            elif component == "Driver" and metric in DRIVER_METRICS:
                drivers[metric] = value
    if "max_wg_completed" not in drivers:
        raise ValueError(f"missing completed-WG metric in {path}")
    values.update(drivers)
    return values


def metric_path(root: Path, benchmark: str, config: str) -> Path:
    path = root / f"baseline_{benchmark}_{config}_metrics.csv"
    if not path.is_file():
        raise FileNotFoundError(path)
    return path


def load_rows(baseline_root: Path, m2_root: Path) -> list[dict[str, float]]:
    if binary_digest(baseline_root) != binary_digest(m2_root):
        raise ValueError("Baseline and M2 use different frozen binaries")

    rows = []
    for benchmark, label in WORKLOADS:
        baseline = read_metrics(metric_path(
            baseline_root, benchmark, "baseline"
        ))
        m2 = read_metrics(metric_path(m2_root, benchmark, "m2"))
        if int(baseline["max_wg_completed"]) != int(m2["max_wg_completed"]):
            raise ValueError(f"completed-WG mismatch for {benchmark}")

        baseline_reads = baseline["rdma_observed_remote_reads"]
        m2_reads = m2["remote_logical_reads"]
        if baseline_reads <= 0 or m2_reads <= 0:
            raise ValueError(f"missing remote reads for {benchmark}")
        capacity = baseline["rdma_max_outstanding"]
        if capacity <= 0:
            raise ValueError(f"invalid RDMA capacity for {benchmark}")
        if m2["rdma_max_outstanding"] != capacity:
            raise ValueError(f"RDMA capacity mismatch for {benchmark}")

        rows.append({
            "benchmark": benchmark,
            "label": label,
            "completed_wgs": baseline["max_wg_completed"],
            "rdma_capacity": capacity,
            "baseline_remote_reads": baseline_reads,
            "baseline_peak_outstanding": baseline["rdma_peak_outstanding"],
            "baseline_peak_occupancy_pct": (
                100.0 * baseline["rdma_peak_outstanding"] / capacity
            ),
            "baseline_full_stalls": baseline[
                "rdma_outstanding_full_stalls"
            ],
            "baseline_blocked_retries_per_read": (
                baseline["rdma_outstanding_full_stalls"] / baseline_reads
            ),
            "baseline_stalls_per_1k_reads": (
                1000.0 * baseline["rdma_outstanding_full_stalls"]
                / baseline_reads
            ),
            "m2_remote_reads": m2_reads,
            "m2_peak_outstanding": m2["rdma_peak_outstanding"],
            "m2_full_stalls": m2["rdma_outstanding_full_stalls"],
            "m2_blocked_retries_per_read": (
                m2["rdma_outstanding_full_stalls"] / m2_reads
            ),
            "m2_stalls_per_1k_reads": (
                1000.0 * m2["rdma_outstanding_full_stalls"] / m2_reads
            ),
        })
    return rows


def write_csv(path: Path, rows: list[dict[str, float]]) -> None:
    fields = list(rows[0])
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def weighted_retries(rows, prefix: str) -> float:
    stalls = sum(row[f"{prefix}_full_stalls"] for row in rows)
    reads = sum(row[f"{prefix}_remote_reads"] for row in rows)
    return stalls / reads


def style_axis(ax) -> None:
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)
    ax.grid(axis="y", color=GRID, linewidth=0.55, zorder=0)
    ax.tick_params(axis="both", labelsize=5.8, length=2.2, width=0.65)


def plot_saturation(path: Path, rows: list[dict[str, float]]) -> None:
    labels = [row["label"] for row in rows]
    x = np.arange(len(rows), dtype=float)
    peak = [row["baseline_peak_outstanding"] for row in rows]
    capacity = rows[0]["rdma_capacity"]
    saturated = sum(math.isclose(value, capacity) for value in peak)

    fig, ax = plt.subplots(figsize=(3.45, 1.42))
    ax.bar(x, peak, width=0.64, color=BLUE, edgecolor="none", zorder=2)
    ax.axhline(
        capacity, color=ORANGE_DARK, linewidth=0.7, linestyle=(0, (3, 2))
    )
    ax.set_ylim(0, 71)
    ax.set_yticks((0, 32, 64))
    ax.set_ylabel("Peak outstanding\nrequests", fontsize=6.6)
    ax.set_xticks(x)
    ax.set_xticklabels(labels, rotation=30, ha="right", fontsize=5.8)
    ax.text(
        0.02,
        0.90,
        f"{saturated}/{len(rows)} reach the {int(capacity)}-entry limit",
        transform=ax.transAxes,
        ha="left",
        va="top",
        fontsize=5.8,
        color=ORANGE_DARK,
        fontweight="bold",
    )
    style_axis(ax)
    fig.subplots_adjust(left=0.18, right=0.99, top=0.97, bottom=0.27)
    fig.savefig(path.with_suffix(".png"), dpi=300, bbox_inches="tight")
    fig.savefig(path.with_suffix(".pdf"), bbox_inches="tight")
    plt.close(fig)


def plot_blocked_retries(path: Path, rows: list[dict[str, float]]) -> None:
    labels = [row["label"] for row in rows]
    x = np.arange(len(rows), dtype=float)
    baseline_retries = [
        row["baseline_blocked_retries_per_read"] for row in rows
    ]
    m2_retries = [row["m2_blocked_retries_per_read"] for row in rows]
    base_weighted = weighted_retries(rows, "baseline")
    m2_weighted = weighted_retries(rows, "m2")
    reduction = 100.0 * (base_weighted - m2_weighted) / base_weighted

    fig, ax = plt.subplots(figsize=(3.45, 1.55))
    width = 0.36
    ax.bar(
        x - width / 2,
        baseline_retries,
        width=width,
        color=BLUE,
        edgecolor="none",
        zorder=2,
    )
    ax.bar(
        x + width / 2,
        m2_retries,
        width=width,
        color=ORANGE,
        edgecolor="none",
        zorder=2,
    )
    ax.set_ylim(0, 14.5)
    ax.set_yticks((0, 5, 10))
    ax.set_ylabel("Blocked retries per\nremote read", fontsize=6.6)
    ax.set_xticks(x)
    ax.set_xticklabels(labels, rotation=30, ha="right", fontsize=5.8)
    ax.text(
        0.98,
        0.94,
        f"Weighted {base_weighted:.1f} \u2192 {m2_weighted:.1f} retries/read",
        transform=ax.transAxes,
        ha="right",
        va="top",
        fontsize=5.8,
        color=ORANGE_DARK,
        fontweight="bold",
    )
    style_axis(ax)
    handles = (
        Patch(facecolor=BLUE, edgecolor="none", label="Baseline"),
        Patch(facecolor=ORANGE, edgecolor="none", label="Remote Aggregation"),
    )
    ax.legend(
        handles=handles,
        loc="lower left",
        bbox_to_anchor=(0.0, 1.03, 1.0, 0.10),
        ncol=2,
        mode="expand",
        frameon=False,
        borderaxespad=0,
        fontsize=5.8,
        handlelength=1.0,
        handletextpad=0.4,
        columnspacing=1.2,
    )
    fig.subplots_adjust(left=0.18, right=0.99, top=0.84, bottom=0.27)
    fig.savefig(path.with_suffix(".png"), dpi=300, bbox_inches="tight")
    fig.savefig(path.with_suffix(".pdf"), bbox_inches="tight")
    plt.close(fig)

    print(
        f"Baseline weighted retries/read={base_weighted:.3f}, "
        f"M2={m2_weighted:.3f}, reduction={reduction:.1f}%"
    )


def plot(path: Path, rows: list[dict[str, float]]) -> None:
    plt.rcParams.update({
        "font.family": "DejaVu Sans",
        "pdf.fonttype": 42,
        "ps.fonttype": 42,
        "axes.linewidth": 0.7,
        "text.color": TEXT,
        "axes.labelcolor": TEXT,
    })
    labels = [row["label"] for row in rows]
    x = np.arange(len(rows), dtype=float)
    peak = [row["baseline_peak_outstanding"] for row in rows]
    baseline_retries = [
        row["baseline_blocked_retries_per_read"] for row in rows
    ]
    m2_retries = [row["m2_blocked_retries_per_read"] for row in rows]
    capacity = rows[0]["rdma_capacity"]
    saturated = sum(math.isclose(value, capacity) for value in peak)

    fig, (top, bottom) = plt.subplots(
        2,
        1,
        figsize=(3.45, 2.50),
        sharex=True,
        gridspec_kw={"height_ratios": (0.85, 1.15), "hspace": 0.18},
    )

    top.bar(x, peak, width=0.64, color=BLUE, edgecolor="none", zorder=2)
    top.axhline(
        capacity, color=ORANGE_DARK, linewidth=0.7, linestyle=(0, (3, 2))
    )
    top.set_ylim(0, 71)
    top.set_yticks((0, 32, 64))
    top.set_ylabel("Peak outstanding\nrequests", fontsize=6.6)
    top.text(
        0.02,
        0.90,
        f"{saturated}/{len(rows)} reach the {int(capacity)}-entry limit",
        transform=top.transAxes,
        ha="left",
        va="top",
        fontsize=5.8,
        color=ORANGE_DARK,
        fontweight="bold",
    )
    top.text(
        0.01, 1.03, "(a) Baseline RDMA saturation",
        transform=top.transAxes, fontsize=6.3, fontweight="bold",
    )
    style_axis(top)

    width = 0.36
    bottom.bar(
        x - width / 2,
        baseline_retries,
        width=width,
        color=BLUE,
        edgecolor="none",
        zorder=2,
    )
    bottom.bar(
        x + width / 2,
        m2_retries,
        width=width,
        color=ORANGE,
        edgecolor="none",
        zorder=2,
    )
    bottom.set_ylim(0, 14.5)
    bottom.set_yticks((0, 5, 10))
    bottom.set_ylabel("Blocked retries per\nremote read", fontsize=6.6)
    bottom.set_xticks(x)
    bottom.set_xticklabels(labels, rotation=30, ha="right", fontsize=5.8)
    bottom.text(
        0.01, 1.03, "(b) Outstanding-capacity stalls",
        transform=bottom.transAxes, fontsize=6.3, fontweight="bold",
    )
    base_weighted = weighted_retries(rows, "baseline")
    m2_weighted = weighted_retries(rows, "m2")
    reduction = 100.0 * (base_weighted - m2_weighted) / base_weighted
    bottom.text(
        0.98,
        0.94,
        f"Weighted {base_weighted:.1f} \u2192 {m2_weighted:.1f} retries/read",
        transform=bottom.transAxes,
        ha="right",
        va="top",
        fontsize=5.8,
        color=ORANGE_DARK,
        fontweight="bold",
    )
    style_axis(bottom)

    handles = (
        Patch(facecolor=BLUE, edgecolor="none", label="Baseline"),
        Patch(facecolor=ORANGE, edgecolor="none", label="Remote Aggregation"),
    )
    bottom.legend(
        handles=handles,
        loc="upper center",
        bbox_to_anchor=(0.5, -0.48),
        ncol=2,
        frameon=False,
        fontsize=5.8,
        handlelength=1.0,
        handletextpad=0.4,
        columnspacing=1.2,
    )

    fig.subplots_adjust(left=0.18, right=0.99, top=0.97, bottom=0.24)
    fig.savefig(path.with_suffix(".png"), dpi=300, bbox_inches="tight")
    fig.savefig(path.with_suffix(".pdf"), bbox_inches="tight")
    plt.close(fig)

    print(
        f"Baseline weighted retries/read={base_weighted:.3f}, "
        f"M2={m2_weighted:.3f}, reduction={reduction:.1f}%"
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline-results", type=Path, required=True)
    parser.add_argument("--m2-results", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()

    baseline_root = args.baseline_results.resolve()
    m2_root = args.m2_results.resolve()
    output_dir = args.output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    rows = load_rows(baseline_root, m2_root)
    csv_path = output_dir / "rdma_admission_pressure.csv"
    figure_path = output_dir / "rdma_admission_pressure"
    saturation_path = output_dir / "rdma_peak_outstanding"
    retries_path = output_dir / "rdma_blocked_retries"
    write_csv(csv_path, rows)
    plot(figure_path, rows)
    plot_saturation(saturation_path, rows)
    plot_blocked_retries(retries_path, rows)
    print(csv_path)
    print(figure_path.with_suffix(".png"))
    print(figure_path.with_suffix(".pdf"))
    print(saturation_path.with_suffix(".png"))
    print(saturation_path.with_suffix(".pdf"))
    print(retries_path.with_suffix(".png"))
    print(retries_path.with_suffix(".pdf"))


if __name__ == "__main__":
    main()
