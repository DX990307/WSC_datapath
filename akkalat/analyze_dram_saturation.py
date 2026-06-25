#!/usr/bin/env python3
"""Summarize DRAM pressure from akkalat metrics CSV files.

The script uses the existing per-DRAM transaction metrics emitted by
`-report-all`/`-report-dram-transaction-count`.
"""

from __future__ import annotations

import argparse
import csv
import math
import re
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


DRAM_FREQ_HZ = 500e6
DRAM_BUS_WIDTH_BITS = 256
DRAM_BURST_LENGTH = 4
DRAM_BURST_CYCLE = DRAM_BURST_LENGTH / 2
DRAM_CONTROLLER_PEAK_GBPS = (
    (DRAM_BUS_WIDTH_BITS / 8) * DRAM_BURST_LENGTH * DRAM_FREQ_HZ / DRAM_BURST_CYCLE / 1e9
)


def fnum(value: str | None) -> float:
    if value is None or value == "":
        return 0.0
    try:
        return float(value)
    except ValueError:
        return 0.0


def pct(num: float, den: float) -> float:
    return 100.0 * num / den if den else 0.0


def percentile(values: list[float], q: float) -> float:
    if not values:
        return 0.0
    values = sorted(values)
    if len(values) == 1:
        return values[0]
    pos = (len(values) - 1) * q
    lo = int(math.floor(pos))
    hi = int(math.ceil(pos))
    if lo == hi:
        return values[lo]
    return values[lo] * (hi - pos) + values[hi] * (pos - lo)


def read_metric_kv(path: Path) -> dict[str, str]:
    if not path.exists():
        return {}
    out: dict[str, str] = {}
    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            key = (row.get("metric") or "").strip()
            if key:
                out[key] = (row.get("value") or "").strip()
    return out


def read_l2_remote_summary(path: Path) -> dict[str, dict[str, str]]:
    if not path.exists():
        return {}
    rows: dict[str, dict[str, str]] = {}
    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            benchmark = (row.get("benchmark") or "").strip()
            if benchmark:
                rows[benchmark] = row
    return rows


def parse_name(path: Path, workload_type: str) -> tuple[str, str]:
    stem = path.name.removesuffix("_metrics.csv")
    if workload_type == "llm":
        m = re.match(r"baseline_llmop_[^_]+_[^_]+_(\d+_.+)_(?:baseline|llm_mixed)$", stem)
        if m:
            label = m.group(1)
            op = re.sub(r"^\d+_", "", label)
            return label, op
    m = re.match(r"baseline_(.+)_baseline$", stem)
    if m:
        return m.group(1), m.group(1)
    return stem, stem


def matching_metrics(result_dir: Path, workload_type: str) -> list[Path]:
    if workload_type == "llm":
        return sorted(result_dir.glob("baseline_llmop_*_metrics.csv"))
    paths = []
    for path in sorted(result_dir.glob("baseline_*_baseline_metrics.csv")):
        if "baseline_llmop_" not in path.name:
            paths.append(path)
    return paths


def collect_one(path: Path, result_dir: Path, workload_type: str, l2_summary: dict[str, dict[str, str]]) -> dict[str, object]:
    label, display = parse_name(path, workload_type)
    prefix = path.name.removesuffix("_metrics.csv")

    driver_total_time = 0.0
    cp_kernel_times: list[float] = []
    dram: dict[str, dict[str, float]] = {}

    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            where = (row.get("where") or "").strip()
            what = (row.get("what") or "").strip()
            value = fnum(row.get("value"))

            if where == "Driver" and what == "total_time":
                driver_total_time = value
            elif where.endswith(".CommandProcessor") and what == "kernel_time":
                cp_kernel_times.append(value)
            elif ".DRAM[" in where and what in {
                "read_trans_count",
                "write_trans_count",
                "read_avg_latency",
                "write_avg_latency",
                "read_size",
                "write_size",
            }:
                dram.setdefault(where, {})[what] = value

    max_kernel_time = max(cp_kernel_times) if cp_kernel_times else driver_total_time
    runtime = max_kernel_time or driver_total_time
    read_bytes = sum(d.get("read_size", 0.0) for d in dram.values())
    write_bytes = sum(d.get("write_size", 0.0) for d in dram.values())
    total_bytes = read_bytes + write_bytes
    read_trans = sum(d.get("read_trans_count", 0.0) for d in dram.values())
    write_trans = sum(d.get("write_trans_count", 0.0) for d in dram.values())
    total_trans = read_trans + write_trans

    controller_bytes = [
        d.get("read_size", 0.0) + d.get("write_size", 0.0) for d in dram.values()
    ]
    controller_bw = [b / runtime / 1e9 if runtime else 0.0 for b in controller_bytes]
    avg_controller_bw = sum(controller_bw) / len(controller_bw) if controller_bw else 0.0
    max_controller_bw = max(controller_bw) if controller_bw else 0.0

    weighted_read_latency = (
        sum(d.get("read_avg_latency", 0.0) * d.get("read_trans_count", 0.0) for d in dram.values())
        / read_trans
        if read_trans
        else 0.0
    )
    weighted_write_latency = (
        sum(
            d.get("write_avg_latency", 0.0) * d.get("write_trans_count", 0.0)
            for d in dram.values()
        )
        / write_trans
        if write_trans
        else 0.0
    )

    sharing = read_metric_kv(result_dir / f"{prefix}_sharing_summary_metrics.csv")
    l2_row = l2_summary.get(label, {})
    remote_ratio = fnum(sharing.get("RemoteAccessRatio")) * 100.0
    if remote_ratio == 0.0:
        remote_ratio = fnum(l2_row.get("remote_access_pct"))
    shared_ratio = fnum(sharing.get("SharedByteRatio")) * 100.0
    if shared_ratio == 0.0:
        shared_ratio = fnum(l2_row.get("shared_readmostly_remote_pct"))

    active_gpus = len(cp_kernel_times)
    dram_controller_count = len(dram)
    aggregate_peak = dram_controller_count * DRAM_CONTROLLER_PEAK_GBPS
    effective_bw_kernel = total_bytes / runtime / 1e9 if runtime else 0.0
    effective_bw_driver = total_bytes / driver_total_time / 1e9 if driver_total_time else 0.0

    return {
        "workload_type": workload_type,
        "name": label,
        "display_name": display,
        "metrics_file": path.name,
        "driver_total_time_s": driver_total_time,
        "max_cp_kernel_time_s": max_kernel_time,
        "active_gpu_count": active_gpus,
        "dram_controller_count": dram_controller_count,
        "dram_total_read_bytes": read_bytes,
        "dram_total_write_bytes": write_bytes,
        "dram_total_bytes": total_bytes,
        "dram_read_trans": read_trans,
        "dram_write_trans": write_trans,
        "dram_effective_bw_GBps_driver_time": effective_bw_driver,
        "dram_effective_bw_GBps_kernel_time": effective_bw_kernel,
        "dram_aggregate_peak_GBps_est": aggregate_peak,
        "dram_aggregate_util_pct_est": pct(effective_bw_kernel, aggregate_peak),
        "dram_max_controller_bw_GBps": max_controller_bw,
        "dram_max_controller_util_pct_est": pct(max_controller_bw, DRAM_CONTROLLER_PEAK_GBPS),
        "dram_p95_controller_bw_GBps": percentile(controller_bw, 0.95),
        "dram_avg_controller_bw_GBps": avg_controller_bw,
        "dram_controller_imbalance_max_over_avg": (
            max_controller_bw / avg_controller_bw if avg_controller_bw else 0.0
        ),
        "dram_weighted_read_latency_ns": weighted_read_latency * 1e9,
        "dram_max_read_latency_ns": max(
            (d.get("read_avg_latency", 0.0) * 1e9 for d in dram.values()), default=0.0
        ),
        "dram_weighted_write_latency_ns": weighted_write_latency * 1e9,
        "remote_access_pct": remote_ratio,
        "shared_byte_pct": shared_ratio,
        "remote_read_dram_service_pct": fnum(l2_row.get("remote_read_dram_service_pct")),
        "remote_read_l2_service_pct": fnum(l2_row.get("remote_read_l2_service_pct")),
        "remote_neighbor_read_pct": fnum(l2_row.get("remote_neighbor_read_pct")),
    }


def classify(row: dict[str, object]) -> str:
    agg = float(row["dram_aggregate_util_pct_est"])
    maxu = float(row["dram_max_controller_util_pct_est"])
    read_lat = float(row["dram_weighted_read_latency_ns"])
    if agg >= 75 or (agg >= 50 and read_lat >= 150):
        return "global_saturation_candidate"
    if maxu >= 75:
        return "local_controller_saturation_candidate"
    if agg >= 40 or maxu >= 40:
        return "moderate_pressure"
    if read_lat >= 100:
        return "latency_elevated_low_bw"
    return "not_saturated"


def write_csv(rows: list[dict[str, object]], path: Path) -> None:
    fields = [
        "workload_type",
        "name",
        "display_name",
        "classification",
        "driver_total_time_s",
        "max_cp_kernel_time_s",
        "active_gpu_count",
        "dram_controller_count",
        "dram_total_read_bytes",
        "dram_total_write_bytes",
        "dram_total_bytes",
        "dram_read_trans",
        "dram_write_trans",
        "dram_effective_bw_GBps_driver_time",
        "dram_effective_bw_GBps_kernel_time",
        "dram_aggregate_peak_GBps_est",
        "dram_aggregate_util_pct_est",
        "dram_max_controller_bw_GBps",
        "dram_max_controller_util_pct_est",
        "dram_p95_controller_bw_GBps",
        "dram_avg_controller_bw_GBps",
        "dram_controller_imbalance_max_over_avg",
        "dram_weighted_read_latency_ns",
        "dram_max_read_latency_ns",
        "dram_weighted_write_latency_ns",
        "remote_access_pct",
        "shared_byte_pct",
        "remote_read_dram_service_pct",
        "remote_read_l2_service_pct",
        "remote_neighbor_read_pct",
        "metrics_file",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            out = dict(row)
            out["classification"] = classify(row)
            writer.writerow(out)


def plot_util(rows: list[dict[str, object]], out_dir: Path) -> None:
    fig, axes = plt.subplots(1, 2, figsize=(14, 7), sharex=False)
    for ax, workload_type, title in [
        (axes[0], "traditional", "Traditional"),
        (axes[1], "llm", "LLM Operators"),
    ]:
        subset = [r for r in rows if r["workload_type"] == workload_type]
        subset.sort(key=lambda r: float(r["dram_aggregate_util_pct_est"]), reverse=True)
        labels = [str(r["display_name"]) for r in subset]
        y = list(range(len(subset)))
        agg = [float(r["dram_aggregate_util_pct_est"]) for r in subset]
        maxu = [float(r["dram_max_controller_util_pct_est"]) for r in subset]
        ax.barh([v + 0.18 for v in y], agg, height=0.34, label="aggregate DRAM BW / peak")
        ax.barh([v - 0.18 for v in y], maxu, height=0.34, label="max controller BW / peak")
        ax.set_yticks(y)
        ax.set_yticklabels(labels, fontsize=7)
        ax.invert_yaxis()
        ax.set_xlabel("Estimated utilization (%)")
        ax.set_title(title)
        ax.axvline(75, color="#C62828", lw=1, ls="--")
        ax.axvline(40, color="#F9A825", lw=1, ls=":")
        ax.grid(axis="x", alpha=0.25)
    axes[0].legend(loc="lower right", fontsize=8)
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(out_dir / f"dram_utilization_bars.{ext}", dpi=220)
    plt.close(fig)


def plot_latency(rows: list[dict[str, object]], out_dir: Path) -> None:
    fig, axes = plt.subplots(1, 2, figsize=(14, 7), sharex=False)
    for ax, workload_type, title in [
        (axes[0], "traditional", "Traditional"),
        (axes[1], "llm", "LLM Operators"),
    ]:
        subset = [r for r in rows if r["workload_type"] == workload_type]
        subset.sort(key=lambda r: float(r["dram_weighted_read_latency_ns"]), reverse=True)
        labels = [str(r["display_name"]) for r in subset]
        y = list(range(len(subset)))
        read_lat = [float(r["dram_weighted_read_latency_ns"]) for r in subset]
        max_read_lat = [float(r["dram_max_read_latency_ns"]) for r in subset]
        ax.barh([v + 0.18 for v in y], read_lat, height=0.34, label="weighted read avg")
        ax.barh([v - 0.18 for v in y], max_read_lat, height=0.34, label="max controller read avg")
        ax.set_yticks(y)
        ax.set_yticklabels(labels, fontsize=7)
        ax.invert_yaxis()
        ax.set_xlabel("DRAM request latency (ns)")
        ax.set_title(title)
        ax.axvline(100, color="#F9A825", lw=1, ls=":")
        ax.axvline(150, color="#C62828", lw=1, ls="--")
        ax.grid(axis="x", alpha=0.25)
    axes[0].legend(loc="lower right", fontsize=8)
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(out_dir / f"dram_latency_bars.{ext}", dpi=220)
    plt.close(fig)


def plot_scatter(rows: list[dict[str, object]], out_dir: Path) -> None:
    fig, ax = plt.subplots(figsize=(8, 6))
    colors = {"traditional": "#2878B5", "llm": "#D45E00"}
    for workload_type in ("traditional", "llm"):
        subset = [r for r in rows if r["workload_type"] == workload_type]
        ax.scatter(
            [float(r["dram_aggregate_util_pct_est"]) for r in subset],
            [float(r["dram_weighted_read_latency_ns"]) for r in subset],
            s=[30 + max(0.0, float(r["remote_access_pct"])) * 1.2 for r in subset],
            alpha=0.75,
            label=workload_type,
            color=colors[workload_type],
            edgecolor="white",
            linewidth=0.6,
        )
        for r in subset:
            x = float(r["dram_aggregate_util_pct_est"])
            y = float(r["dram_weighted_read_latency_ns"])
            if x >= 25 or y >= 90:
                ax.annotate(str(r["display_name"]), (x, y), fontsize=7, xytext=(3, 3), textcoords="offset points")
    ax.axvline(75, color="#C62828", lw=1, ls="--")
    ax.axvline(40, color="#F9A825", lw=1, ls=":")
    ax.axhline(150, color="#C62828", lw=1, ls="--")
    ax.axhline(100, color="#F9A825", lw=1, ls=":")
    ax.set_xlabel("Aggregate DRAM BW / estimated peak (%)")
    ax.set_ylabel("Weighted DRAM read latency (ns)")
    ax.set_title("DRAM pressure: bandwidth utilization vs latency")
    ax.grid(alpha=0.25)
    ax.legend()
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(out_dir / f"dram_bw_vs_latency.{ext}", dpi=220)
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--traditional-dir", type=Path, required=True)
    parser.add_argument("--llm-dir", type=Path, required=True)
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=Path("akkalat/results/dram_saturation_analysis"),
    )
    args = parser.parse_args()

    rows: list[dict[str, object]] = []

    traditional_l2 = read_l2_remote_summary(
        args.traditional_dir / "traditional_l2_remote_bottleneck_summary.csv"
    )
    for path in matching_metrics(args.traditional_dir, "traditional"):
        rows.append(collect_one(path, args.traditional_dir, "traditional", traditional_l2))

    for path in matching_metrics(args.llm_dir, "llm"):
        rows.append(collect_one(path, args.llm_dir, "llm", {}))

    args.out_dir.mkdir(parents=True, exist_ok=True)
    write_csv(rows, args.out_dir / "dram_saturation_summary.csv")
    plot_util(rows, args.out_dir)
    plot_latency(rows, args.out_dir)
    plot_scatter(rows, args.out_dir)

    print(f"Wrote {args.out_dir / 'dram_saturation_summary.csv'}")
    print(f"Controller peak estimate: {DRAM_CONTROLLER_PEAK_GBPS:.2f} GB/s")
    by_type = {}
    for row in rows:
        by_type.setdefault(row["workload_type"], []).append(row)
    for workload_type, subset in by_type.items():
        max_agg = max(float(r["dram_aggregate_util_pct_est"]) for r in subset)
        max_ctrl = max(float(r["dram_max_controller_util_pct_est"]) for r in subset)
        max_lat = max(float(r["dram_weighted_read_latency_ns"]) for r in subset)
        print(
            f"{workload_type}: n={len(subset)} "
            f"max aggregate util={max_agg:.1f}% "
            f"max controller util={max_ctrl:.1f}% "
            f"max weighted read latency={max_lat:.1f} ns"
        )


if __name__ == "__main__":
    main()
