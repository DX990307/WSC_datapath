#!/usr/bin/env python3
import csv
import glob
import math
import os
import re
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

plt.rcParams.update(
    {
        "font.size": 14,
        "axes.titlesize": 18,
        "axes.labelsize": 16,
        "xtick.labelsize": 13,
        "ytick.labelsize": 13,
        "legend.fontsize": 13,
        "figure.titlesize": 22,
    }
)


ROOT = Path("akkalat/results/m1-baseline-vs-mechanism1")
OUT = Path("akkalat/reports/m1_artifacts")
FIG = OUT / "figures"
IMPROVEMENT_FIG = OUT / "figures_improvement"


def load_metrics(path):
    values = {}
    if not path.exists():
        return values
    with path.open() as f:
        reader = csv.reader(f)
        next(reader, None)
        for row in reader:
            if len(row) < 4:
                continue
            try:
                value = float(row[3])
                if math.isfinite(value):
                    values[(row[1].strip(), row[2].strip())] = value
            except ValueError:
                pass
    return values


def pct(new, old):
    if old in (0, None) or new is None:
        return None
    if not math.isfinite(old) or not math.isfinite(new):
        return None
    return (new / old - 1) * 100


def reduction_improvement(delta):
    if delta is None or not math.isfinite(delta):
        return None
    return -delta


def driver(metrics, name):
    return metrics.get(("Driver", name), 0.0)


def optional_driver(metrics, name):
    return metrics.get(("Driver", name))


def bytes_to_mb(value):
    if value is None:
        return None
    return value / 1e6


def dram_stats(metrics):
    trans = 0.0
    size = 0.0
    lat_weighted = 0.0
    for (where, what), value in metrics.items():
        if ".DRAM[" not in where or what != "read_trans_count":
            continue
        count = value
        trans += count
        size += metrics.get((where, "read_size"), 0.0)
        lat_weighted += metrics.get((where, "read_avg_latency"), 0.0) * count
    latency = lat_weighted / trans if trans else 0.0
    return trans, size, latency


def cache_latency(metrics, name_part):
    total = 0.0
    count = 0.0
    for (where, what), latency in metrics.items():
        if what != "req_average_latency" or name_part not in where:
            continue
        reqs = (
            metrics.get((where, "read-hit"), 0.0)
            + metrics.get((where, "read-miss"), 0.0)
            + metrics.get((where, "read-mshr-hit"), 0.0)
        )
        if reqs:
            total += latency * reqs
            count += reqs
    return (total / count if count else 0.0), count


def cpi_avg(metrics, what):
    values = [
        value
        for (where, name), value in metrics.items()
        if name == what and ".CU[" in where
    ]
    return sum(values) / len(values) if values else 0.0


def fmt(value, digits=2, suffix=""):
    if value is None or not math.isfinite(value):
        return ""
    return f"{value:.{digits}f}{suffix}"


def fmt_signed(value, digits=2):
    if value is None or not math.isfinite(value):
        return ""
    return f"{value:+.{digits}f}%"


def discover_pairs():
    summary = ROOT / "summary.csv"
    rows = []
    if summary.exists():
        with summary.open() as f:
            for row in csv.DictReader(f):
                rows.append(row["benchmark"])
    else:
        found = set()
        for path in ROOT.glob("*_metrics.csv"):
            name = path.name
            match = re.match(r"baseline_(.+)_(baseline|mechanism1)_(.+)_metrics\.csv", name)
            if match:
                found.add(match.group(1))
        rows = sorted(found)

    pairs = []
    for benchmark in rows:
        base = next(ROOT.glob(f"baseline_{benchmark}_baseline_*_metrics.csv"), None)
        mech = next(ROOT.glob(f"baseline_{benchmark}_mechanism1_*_metrics.csv"), None)
        pairs.append((benchmark, base, mech))
    return pairs


def collect_rows():
    rows = []
    for benchmark, base_path, mech_path in discover_pairs():
        base = load_metrics(base_path) if base_path else {}
        mech = load_metrics(mech_path) if mech_path else {}

        paired = bool(base and mech)
        baseline_time = driver(base, "total_time") if base else None
        mechanism_time = driver(mech, "total_time") if mech else None
        speedup = (
            (baseline_time / mechanism_time - 1) * 100
            if baseline_time and mechanism_time
            else None
        )

        b_trans = b_size = b_dram_lat = 0.0
        m_trans = m_size = m_dram_lat = 0.0
        b_l1v = m_l1v = b_l2 = m_l2 = 0.0
        b_vmem = m_vmem = b_cpi = m_cpi = 0.0
        if paired:
            b_trans, b_size, b_dram_lat = dram_stats(base)
            m_trans, m_size, m_dram_lat = dram_stats(mech)
            b_l1v, _ = cache_latency(base, "L1VCache")
            m_l1v, _ = cache_latency(mech, "L1VCache")
            b_l2, _ = cache_latency(base, ".L2[")
            m_l2, _ = cache_latency(mech, ".L2[")
            b_vmem = cpi_avg(base, "CPIStack.VMem")
            m_vmem = cpi_avg(mech, "CPIStack.VMem")
            b_cpi = cpi_avg(base, "CPIStack.total")
            m_cpi = cpi_avg(mech, "CPIStack.total")

        b_max = driver(base, "max_wg_reached") if base else None
        m_max = driver(mech, "max_wg_reached") if mech else None
        if paired and b_max == 1 and m_max == 1:
            status = "full_max_wg"
        elif paired:
            status = "paired_short"
        elif base and not mech:
            status = "baseline_only"
        elif mech and not base:
            status = "mechanism_only"
        else:
            status = "missing"

        au_reads = driver(mech, "l2_batch_access_unit_reads") if mech else 0.0
        au_coalesced = driver(mech, "l2_batch_access_unit_coalesced") if mech else 0.0
        au_saved = (
            au_coalesced / (au_reads + au_coalesced) * 100
            if au_reads + au_coalesced
            else None
        )

        dram_trans_delta = pct(m_trans, b_trans) if paired else None
        dram_bytes_delta = pct(m_size, b_size) if paired else None
        dram_latency_delta = pct(m_dram_lat, b_dram_lat) if paired else None
        l1v_latency_delta = pct(m_l1v, b_l1v) if paired else None
        l2_latency_delta = pct(m_l2, b_l2) if paired else None
        cpi_vmem_delta = pct(m_vmem, b_vmem) if paired else None
        cpi_total_delta = pct(m_cpi, b_cpi) if paired else None
        base_l2_issued = (
            optional_driver(base, "l2_batch_dram_read_issued_bytes")
            if base else None
        )
        base_l2_useful = (
            optional_driver(base, "l2_batch_dram_read_useful_bytes")
            if base else None
        )
        base_l2_overfetch = (
            optional_driver(base, "l2_batch_dram_read_overfetch_pct")
            if base else None
        )
        mech_l2_issued = (
            optional_driver(mech, "l2_batch_dram_read_issued_bytes")
            if mech else None
        )
        mech_l2_useful = (
            optional_driver(mech, "l2_batch_dram_read_useful_bytes")
            if mech else None
        )
        mech_l2_overfetch = (
            optional_driver(mech, "l2_batch_dram_read_overfetch_pct")
            if mech else None
        )

        rows.append(
            {
                "benchmark": benchmark,
                "status": status,
                "baseline_time_us": baseline_time * 1e6 if baseline_time else None,
                "mechanism_time_us": mechanism_time * 1e6 if mechanism_time else None,
                "speedup_pct": speedup,
                "runtime_improvement_pct": speedup,
                "baseline_wg": driver(base, "total_wg_count") if base else None,
                "mechanism_wg": driver(mech, "total_wg_count") if mech else None,
                "baseline_max_wg_reached": b_max,
                "mechanism_max_wg_reached": m_max,
                "dram_trans_delta_pct": dram_trans_delta,
                "dram_bytes_delta_pct": dram_bytes_delta,
                "dram_latency_delta_pct": dram_latency_delta,
                "l1v_latency_delta_pct": l1v_latency_delta,
                "l2_latency_delta_pct": l2_latency_delta,
                "cpi_vmem_delta_pct": cpi_vmem_delta,
                "cpi_total_delta_pct": cpi_total_delta,
                "dram_trans_improvement_pct": reduction_improvement(dram_trans_delta),
                "dram_bytes_improvement_pct": reduction_improvement(dram_bytes_delta),
                "dram_latency_improvement_pct": reduction_improvement(dram_latency_delta),
                "l1v_latency_improvement_pct": reduction_improvement(l1v_latency_delta),
                "l2_latency_improvement_pct": reduction_improvement(l2_latency_delta),
                "cpi_vmem_improvement_pct": reduction_improvement(cpi_vmem_delta),
                "cpi_total_improvement_pct": reduction_improvement(cpi_total_delta),
                "au_coalesce_pct": au_saved,
                "dir_avg_size": driver(mech, "l2_batch_dir_avg_size") if mech else None,
                "baseline_dram_read_mb": b_size / 1e6 if paired else None,
                "mechanism_dram_read_mb": m_size / 1e6 if paired else None,
                "baseline_dram_latency_ns": b_dram_lat * 1e9 if paired else None,
                "mechanism_dram_latency_ns": m_dram_lat * 1e9 if paired else None,
                "baseline_l2_dram_read_issued_mb": bytes_to_mb(base_l2_issued),
                "baseline_l2_dram_read_useful_mb": bytes_to_mb(base_l2_useful),
                "baseline_l2_dram_read_overfetch_pct": base_l2_overfetch,
                "mechanism_l2_dram_read_issued_mb": bytes_to_mb(mech_l2_issued),
                "mechanism_l2_dram_read_useful_mb": bytes_to_mb(mech_l2_useful),
                "mechanism_l2_dram_read_overfetch_pct": mech_l2_overfetch,
            }
        )
    return rows


def write_tables(rows):
    OUT.mkdir(parents=True, exist_ok=True)
    fields = [
        "benchmark",
        "status",
        "baseline_time_us",
        "mechanism_time_us",
        "speedup_pct",
        "baseline_wg",
        "mechanism_wg",
        "baseline_max_wg_reached",
        "mechanism_max_wg_reached",
        "dram_trans_delta_pct",
        "dram_bytes_delta_pct",
        "dram_latency_delta_pct",
        "l1v_latency_delta_pct",
        "l2_latency_delta_pct",
        "cpi_vmem_delta_pct",
        "cpi_total_delta_pct",
        "runtime_improvement_pct",
        "dram_trans_improvement_pct",
        "dram_bytes_improvement_pct",
        "dram_latency_improvement_pct",
        "l1v_latency_improvement_pct",
        "l2_latency_improvement_pct",
        "cpi_vmem_improvement_pct",
        "cpi_total_improvement_pct",
        "au_coalesce_pct",
        "dir_avg_size",
        "baseline_dram_read_mb",
        "mechanism_dram_read_mb",
        "baseline_dram_latency_ns",
        "mechanism_dram_latency_ns",
        "baseline_l2_dram_read_issued_mb",
        "baseline_l2_dram_read_useful_mb",
        "baseline_l2_dram_read_overfetch_pct",
        "mechanism_l2_dram_read_issued_mb",
        "mechanism_l2_dram_read_useful_mb",
        "mechanism_l2_dram_read_overfetch_pct",
    ]
    csv_path = OUT / "m1_full_results.csv"
    with csv_path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)

    md_path = OUT / "m1_full_results.md"
    with md_path.open("w") as f:
        f.write("# Mechanism 1 Current Full Results\n\n")
        f.write(f"Source: `{ROOT}`\n\n")
        f.write("| Benchmark | Status | Speedup | DRAM Trans | DRAM Bytes | DRAM Lat | L1V Lat | L2 Lat | CPI VMem | CPI Total | AU Coalesce | L2 Useful MB | L2 Issued MB | L2 Overfetch | Dir Avg |\n")
        f.write("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
        for row in rows:
            f.write(
                "| {benchmark} | {status} | {speedup} | {dram_trans} | {dram_bytes} | {dram_lat} | {l1v} | {l2} | {cpi_vmem} | {cpi_total} | {au} | {useful} | {issued} | {overfetch} | {dir_avg} |\n".format(
                    benchmark=row["benchmark"],
                    status=row["status"],
                    speedup=fmt_signed(row["speedup_pct"]),
                    dram_trans=fmt_signed(row["dram_trans_delta_pct"]),
                    dram_bytes=fmt_signed(row["dram_bytes_delta_pct"]),
                    dram_lat=fmt_signed(row["dram_latency_delta_pct"]),
                    l1v=fmt_signed(row["l1v_latency_delta_pct"]),
                    l2=fmt_signed(row["l2_latency_delta_pct"]),
                    cpi_vmem=fmt_signed(row["cpi_vmem_delta_pct"]),
                    cpi_total=fmt_signed(row["cpi_total_delta_pct"]),
                    au=fmt(row["au_coalesce_pct"], 2, "%"),
                    useful=fmt(row["mechanism_l2_dram_read_useful_mb"], 3),
                    issued=fmt(row["mechanism_l2_dram_read_issued_mb"], 3),
                    overfetch=fmt(
                        row["mechanism_l2_dram_read_overfetch_pct"], 2, "%"),
                    dir_avg=fmt(row["dir_avg_size"], 3),
                )
            )
    return csv_path, md_path


def barh(path, rows, key, title, xlabel, color_pos="#2f80ed", color_neg="#c84c4c"):
    data = [row for row in rows if row.get(key) is not None]
    data.sort(key=lambda row: row[key])
    labels = [row["benchmark"] for row in data]
    values = [row[key] for row in data]
    colors = [color_pos if value >= 0 else color_neg for value in values]
    height = max(5, 0.32 * len(data) + 1.5)
    fig, ax = plt.subplots(figsize=(11, height))
    ax.barh(labels, values, color=colors)
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_title(title)
    ax.set_xlabel(xlabel)
    ax.grid(axis="x", alpha=0.25)
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def clean_barh(path, rows, key, title, xlabel):
    data = [
        row
        for row in rows
        if row.get(key) is not None and math.isfinite(row.get(key))
    ]
    data.sort(key=lambda row: row[key])
    labels = [row["benchmark"] for row in data]
    values = [row[key] for row in data]
    colors = ["#2f80ed" if value >= 0 else "#c84c4c" for value in values]
    height = max(6, 0.36 * len(data) + 1.6)
    fig, ax = plt.subplots(figsize=(12, height))
    bars = ax.barh(labels, values, color=colors, alpha=0.9)
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_title(title)
    ax.set_xlabel(xlabel)
    ax.grid(axis="x", alpha=0.25)
    pad = max(abs(value) for value in values) * 0.015 if values else 0.2
    for bar, value in zip(bars, values):
        x = value + pad if value >= 0 else value - pad
        ha = "left" if value >= 0 else "right"
        ax.text(
            x,
            bar.get_y() + bar.get_height() / 2,
            f"{value:+.1f}%",
            va="center",
            ha=ha,
            fontsize=8,
        )
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def grouped_barh(path, rows, keys, labels, title, xlabel, sort_key="speedup_pct"):
    data = [
        row
        for row in rows
        if all(row.get(key) is not None and math.isfinite(row.get(key)) for key in keys)
    ]
    data.sort(
        key=lambda row: row.get(sort_key)
        if row.get(sort_key) is not None and math.isfinite(row.get(sort_key))
        else 0.0
    )
    benchmarks = [row["benchmark"] for row in data]
    y = np.arange(len(data))
    group_height = 0.82
    bar_height = group_height / len(keys)
    colors = ["#2f80ed", "#f2994a", "#27ae60", "#9b51e0", "#c84c4c"]

    fig, ax = plt.subplots(figsize=(12.5, max(6, 0.38 * len(data) + 1.8)))
    for index, (key, label) in enumerate(zip(keys, labels)):
        offset = (index - (len(keys) - 1) / 2) * bar_height
        values = [row[key] for row in data]
        ax.barh(
            y + offset,
            values,
            height=bar_height * 0.86,
            label=label,
            color=colors[index % len(colors)],
            alpha=0.92,
        )
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_yticks(y, benchmarks)
    ax.set_title(title)
    ax.set_xlabel(xlabel)
    ax.grid(axis="x", alpha=0.25)
    ax.legend(loc="best", fontsize=8)
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def improvement_rows(rows):
    data = [
        row
        for row in rows
        if row["status"] == "full_max_wg"
        and row.get("runtime_improvement_pct") is not None
        and math.isfinite(row["runtime_improvement_pct"])
    ]
    data.sort(key=lambda row: row["runtime_improvement_pct"], reverse=True)
    return data


def vertical_improvement_bar(path, rows, key, title):
    data = [
        row
        for row in improvement_rows(rows)
        if row.get(key) is not None and math.isfinite(row.get(key))
    ]
    labels = [row["benchmark"] for row in data]
    values = [row[key] for row in data]
    x = np.arange(len(data))
    colors = ["#2f80ed" if value >= 0 else "#c84c4c" for value in values]

    fig, ax = plt.subplots(figsize=(max(15, 0.68 * len(data) + 3), 8.5))
    bars = ax.bar(x, values, color=colors, alpha=0.92)
    ax.axhline(0, color="#222222", linewidth=0.9)
    ax.set_title(title)
    ax.set_xlabel("Benchmark")
    ax.set_ylabel("Improvement (%)")
    ax.set_xticks(x, labels, rotation=65, ha="right")
    ax.grid(axis="y", alpha=0.25)

    if values:
        pad = max(abs(value) for value in values) * 0.012
        for bar, value in zip(bars, values):
            y = value + pad if value >= 0 else value - pad
            va = "bottom" if value >= 0 else "top"
            ax.text(
                bar.get_x() + bar.get_width() / 2,
                y,
                f"{value:+.1f}",
                ha="center",
                va=va,
                rotation=90,
                fontsize=10,
            )

    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def vertical_grouped_improvement_bar(path, rows, keys, labels, title):
    data = [
        row
        for row in improvement_rows(rows)
        if all(row.get(key) is not None and math.isfinite(row.get(key)) for key in keys)
    ]
    benchmarks = [row["benchmark"] for row in data]
    x = np.arange(len(data))
    width = 0.82 / len(keys)
    colors = ["#2f80ed", "#f2994a", "#27ae60", "#9b51e0", "#c84c4c"]

    fig, ax = plt.subplots(figsize=(max(16, 0.72 * len(data) + 3), 8.8))
    for index, (key, label) in enumerate(zip(keys, labels)):
        offset = (index - (len(keys) - 1) / 2) * width
        values = [row[key] for row in data]
        ax.bar(
            x + offset,
            values,
            width=width * 0.9,
            label=label,
            color=colors[index % len(colors)],
            alpha=0.92,
        )

    ax.axhline(0, color="#222222", linewidth=0.9)
    ax.set_title(title)
    ax.set_xlabel("Benchmark")
    ax.set_ylabel("Improvement (%)")
    ax.set_xticks(x, benchmarks, rotation=65, ha="right")
    ax.grid(axis="y", alpha=0.25)
    ax.legend(loc="best")
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def vertical_improvement_dashboard(path, rows):
    data = improvement_rows(rows)
    labels = [row["benchmark"] for row in data]
    x = np.arange(len(data))
    fig, axes = plt.subplots(4, 1, figsize=(max(18, 0.78 * len(data) + 3), 22), sharex=True)

    specs = [
        (
            "End-to-end runtime improvement",
            ["runtime_improvement_pct"],
            ["Runtime"],
            ["#2f80ed"],
        ),
        (
            "DRAM traffic improvement",
            ["dram_trans_improvement_pct", "dram_bytes_improvement_pct"],
            ["Transactions", "Read bytes"],
            ["#2f80ed", "#f2994a"],
        ),
        (
            "Latency improvement",
            [
                "dram_latency_improvement_pct",
                "l1v_latency_improvement_pct",
                "l2_latency_improvement_pct",
            ],
            ["DRAM", "L1V", "L2"],
            ["#2f80ed", "#27ae60", "#9b51e0"],
        ),
        (
            "CU CPI improvement",
            ["cpi_vmem_improvement_pct", "cpi_total_improvement_pct"],
            ["VMem", "Total"],
            ["#2f80ed", "#c84c4c"],
        ),
    ]

    for ax, (title, keys, names, colors) in zip(axes, specs):
        width = 0.82 / len(keys)
        for index, (key, name, color) in enumerate(zip(keys, names, colors)):
            offset = (index - (len(keys) - 1) / 2) * width
            values = [
                row.get(key)
                if row.get(key) is not None and math.isfinite(row.get(key))
                else 0.0
                for row in data
            ]
            ax.bar(x + offset, values, width=width * 0.9, label=name, color=color, alpha=0.92)
        ax.axhline(0, color="#222222", linewidth=0.9)
        ax.set_ylabel("Improvement (%)")
        ax.set_title(title)
        ax.grid(axis="y", alpha=0.25)
        ax.legend(loc="best")

    axes[-1].set_xlabel("Benchmark")
    axes[-1].set_xticks(x, labels, rotation=65, ha="right")
    fig.suptitle("Mechanism 1 Improvement Dashboard (full max-wg)")
    fig.tight_layout(rect=[0, 0, 1, 0.975])
    fig.savefig(path, dpi=180)
    plt.close(fig)


def dashboard(path, rows):
    data = [
        row
        for row in rows
        if row["status"] == "full_max_wg"
        and row.get("speedup_pct") is not None
        and math.isfinite(row.get("speedup_pct"))
    ]
    data.sort(key=lambda row: row["speedup_pct"])
    labels = [row["benchmark"] for row in data]
    y = np.arange(len(data))

    fig, axes = plt.subplots(2, 2, figsize=(18, 13), sharey=True)
    ax = axes[0, 0]
    speed = [row["speedup_pct"] for row in data]
    ax.barh(labels, speed, color=["#2f80ed" if v >= 0 else "#c84c4c" for v in speed])
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_title("End-to-end speedup")
    ax.set_xlabel("speedup (%)")
    ax.grid(axis="x", alpha=0.25)

    ax = axes[0, 1]
    for offset, key, color, label in [
        (-0.18, "dram_trans_delta_pct", "#2f80ed", "DRAM trans"),
        (0.18, "dram_bytes_delta_pct", "#f2994a", "DRAM bytes"),
    ]:
        ax.barh(y + offset, [row.get(key) or 0.0 for row in data], height=0.32, color=color, label=label)
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_title("DRAM traffic")
    ax.set_xlabel("delta (%)")
    ax.grid(axis="x", alpha=0.25)
    ax.legend(fontsize=8)

    ax = axes[1, 0]
    for offset, key, color, label in [
        (-0.24, "dram_latency_delta_pct", "#2f80ed", "DRAM"),
        (0.0, "l1v_latency_delta_pct", "#27ae60", "L1V"),
        (0.24, "l2_latency_delta_pct", "#9b51e0", "L2"),
    ]:
        ax.barh(y + offset, [row.get(key) or 0.0 for row in data], height=0.22, color=color, label=label)
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_title("Latency")
    ax.set_xlabel("delta (%)")
    ax.grid(axis="x", alpha=0.25)
    ax.legend(fontsize=8)

    ax = axes[1, 1]
    for offset, key, color, label in [
        (-0.18, "cpi_vmem_delta_pct", "#2f80ed", "CPI VMem"),
        (0.18, "cpi_total_delta_pct", "#c84c4c", "CPI total"),
    ]:
        values = [
            row.get(key)
            if row.get(key) is not None and math.isfinite(row.get(key))
            else 0.0
            for row in data
        ]
        ax.barh(y + offset, values, height=0.32, color=color, label=label)
    ax.axvline(0, color="#222222", linewidth=0.8)
    ax.set_title("CU CPI")
    ax.set_xlabel("delta (%)")
    ax.grid(axis="x", alpha=0.25)
    ax.legend(fontsize=8)

    fig.suptitle("Mechanism 1 Full max-wg Dashboard", fontsize=16)
    fig.tight_layout(rect=[0, 0, 1, 0.97])
    fig.savefig(path, dpi=180)
    plt.close(fig)


def quadrant(path, rows):
    data = [
        row
        for row in rows
        if row["status"] == "full_max_wg"
        and row.get("speedup_pct") is not None
        and row.get("dram_bytes_delta_pct") is not None
        and row.get("au_coalesce_pct") is not None
        and math.isfinite(row["speedup_pct"])
        and math.isfinite(row["dram_bytes_delta_pct"])
        and math.isfinite(row["au_coalesce_pct"])
    ]
    fig, ax = plt.subplots(figsize=(11, 7))
    sizes = [35 + row["au_coalesce_pct"] * 2.2 for row in data]
    points = ax.scatter(
        [row["dram_bytes_delta_pct"] for row in data],
        [row["speedup_pct"] for row in data],
        s=sizes,
        c=[row["au_coalesce_pct"] for row in data],
        cmap="viridis",
        edgecolor="#222222",
        linewidth=0.5,
        alpha=0.9,
    )
    ax.axhline(0, color="#333333", linewidth=0.8)
    ax.axvline(0, color="#333333", linewidth=0.8)
    ax.set_xlabel("DRAM read bytes delta (%)")
    ax.set_ylabel("speedup (%)")
    ax.set_title("Speedup vs Overfetch (bubble size/color = AU coalesce)")
    ax.grid(alpha=0.25)
    for row in data:
        if abs(row["speedup_pct"]) > 5 or row["dram_bytes_delta_pct"] > 40:
            ax.annotate(
                row["benchmark"],
                (row["dram_bytes_delta_pct"], row["speedup_pct"]),
                textcoords="offset points",
                xytext=(5, 4),
                fontsize=7,
            )
    fig.colorbar(points, ax=ax, label="AU coalesce (%)")
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def heatmap(path, rows):
    keys = [
        "speedup_pct",
        "dram_trans_delta_pct",
        "dram_bytes_delta_pct",
        "dram_latency_delta_pct",
        "l1v_latency_delta_pct",
        "l2_latency_delta_pct",
        "cpi_total_delta_pct",
    ]
    names = [
        "Speedup",
        "DRAM trans",
        "DRAM bytes",
        "DRAM lat",
        "L1V lat",
        "L2 lat",
        "CPI total",
    ]
    data_rows = [row for row in rows if row["status"] == "full_max_wg"]
    data_rows.sort(key=lambda row: row["speedup_pct"] if row["speedup_pct"] is not None else 0)
    matrix = np.array(
        [
            [
                row.get(key)
                if row.get(key) is not None and math.isfinite(row.get(key))
                else 0.0
                for key in keys
            ]
            for row in data_rows
        ]
    )
    labels = [row["benchmark"] for row in data_rows]

    fig, ax = plt.subplots(figsize=(11.5, max(6, 0.34 * len(labels) + 1.8)))
    image = ax.imshow(matrix, aspect="auto", cmap="RdBu", vmin=-60, vmax=60)
    ax.set_xticks(range(len(names)), names, rotation=35, ha="right")
    ax.set_yticks(range(len(labels)), labels)
    ax.set_title("Mechanism 1 Full max-wg Delta Heatmap (%)")
    for y in range(matrix.shape[0]):
        for x in range(matrix.shape[1]):
            ax.text(x, y, f"{matrix[y, x]:+.1f}", ha="center", va="center", fontsize=7)
    fig.colorbar(image, ax=ax, label="delta (%)")
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def scatter(path, rows):
    data = [
        row
        for row in rows
        if row["status"] == "full_max_wg"
        and row["au_coalesce_pct"] is not None
        and row["dram_bytes_delta_pct"] is not None
        and row["speedup_pct"] is not None
    ]
    x = [row["au_coalesce_pct"] for row in data]
    y = [row["dram_bytes_delta_pct"] for row in data]
    c = [row["speedup_pct"] for row in data]
    fig, ax = plt.subplots(figsize=(11, 7))
    points = ax.scatter(x, y, c=c, cmap="RdYlGn", s=70, edgecolor="#222222", linewidth=0.5)
    ax.axhline(0, color="#333333", linewidth=0.8)
    ax.set_xlabel("AU coalesce (%)")
    ax.set_ylabel("DRAM read bytes delta (%)")
    ax.set_title("AU Coalescing vs Overfetch (full max-wg)")
    ax.grid(alpha=0.25)
    for row in data:
        if abs(row["dram_bytes_delta_pct"]) > 20 or abs(row["speedup_pct"]) > 10:
            ax.annotate(
                row["benchmark"],
                (row["au_coalesce_pct"], row["dram_bytes_delta_pct"]),
                textcoords="offset points",
                xytext=(4, 4),
                fontsize=7,
            )
    fig.colorbar(points, ax=ax, label="speedup (%)")
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def table_image(path, rows):
    data = [row for row in rows if row["status"] == "full_max_wg"]
    data.sort(key=lambda row: row["speedup_pct"] if row["speedup_pct"] is not None else 0, reverse=True)
    columns = ["Benchmark", "Speedup", "DRAM trans", "DRAM bytes", "DRAM lat", "L1V", "L2", "AU"]
    cells = [
        [
            row["benchmark"],
            fmt_signed(row["speedup_pct"]),
            fmt_signed(row["dram_trans_delta_pct"]),
            fmt_signed(row["dram_bytes_delta_pct"]),
            fmt_signed(row["dram_latency_delta_pct"]),
            fmt_signed(row["l1v_latency_delta_pct"]),
            fmt_signed(row["l2_latency_delta_pct"]),
            fmt(row["au_coalesce_pct"], 1, "%"),
        ]
        for row in data
    ]
    fig, ax = plt.subplots(figsize=(14, max(6, 0.35 * len(cells) + 1.3)))
    ax.axis("off")
    table = ax.table(cellText=cells, colLabels=columns, loc="center", cellLoc="right")
    table.auto_set_font_size(False)
    table.set_fontsize(8)
    table.scale(1, 1.25)
    for (r, c), cell in table.get_celld().items():
        if r == 0:
            cell.set_facecolor("#30343b")
            cell.set_text_props(color="white", weight="bold")
        elif c == 0:
            cell.set_text_props(ha="left")
        else:
            text = cell.get_text().get_text()
            if text.startswith("+"):
                cell.set_facecolor("#edf7ed")
            elif text.startswith("-"):
                cell.set_facecolor("#f8eeee")
    ax.set_title("Mechanism 1 Full max-wg Results", pad=12)
    fig.tight_layout()
    fig.savefig(path, dpi=180)
    plt.close(fig)


def write_figures(rows):
    FIG.mkdir(parents=True, exist_ok=True)
    IMPROVEMENT_FIG.mkdir(parents=True, exist_ok=True)
    full = [row for row in rows if row["status"] == "full_max_wg"]
    paired = [row for row in rows if row["status"] in ("full_max_wg", "paired_short")]
    vertical_improvement_bar(
        IMPROVEMENT_FIG / "m1_improvement_runtime_full_max_wg.png",
        rows,
        "runtime_improvement_pct",
        "Mechanism 1 Runtime Improvement (full max-wg)",
    )
    vertical_grouped_improvement_bar(
        IMPROVEMENT_FIG / "m1_improvement_dram_traffic_full_max_wg.png",
        rows,
        ["dram_trans_improvement_pct", "dram_bytes_improvement_pct"],
        ["DRAM transactions", "DRAM read bytes"],
        "Mechanism 1 DRAM Traffic Improvement (full max-wg)",
    )
    vertical_grouped_improvement_bar(
        IMPROVEMENT_FIG / "m1_improvement_latency_full_max_wg.png",
        rows,
        [
            "dram_latency_improvement_pct",
            "l1v_latency_improvement_pct",
            "l2_latency_improvement_pct",
        ],
        ["DRAM latency", "L1V latency", "L2 latency"],
        "Mechanism 1 Latency Improvement (full max-wg)",
    )
    vertical_grouped_improvement_bar(
        IMPROVEMENT_FIG / "m1_improvement_cpi_full_max_wg.png",
        rows,
        ["cpi_vmem_improvement_pct", "cpi_total_improvement_pct"],
        ["CPI VMem", "CPI total"],
        "Mechanism 1 CPI Improvement (full max-wg)",
    )
    vertical_improvement_dashboard(
        IMPROVEMENT_FIG / "m1_improvement_dashboard_full_max_wg.png",
        rows,
    )
    clean_barh(
        FIG / "m1_bar_speedup_full_max_wg.png",
        full,
        "speedup_pct",
        "Mechanism 1 Speedup (full max-wg)",
        "speedup (%)",
    )
    grouped_barh(
        FIG / "m1_bar_dram_traffic_full_max_wg.png",
        full,
        ["dram_trans_delta_pct", "dram_bytes_delta_pct"],
        ["DRAM transactions", "DRAM read bytes"],
        "Mechanism 1 DRAM Traffic Delta (full max-wg)",
        "delta (%)",
    )
    grouped_barh(
        FIG / "m1_bar_latency_full_max_wg.png",
        full,
        ["dram_latency_delta_pct", "l1v_latency_delta_pct", "l2_latency_delta_pct"],
        ["DRAM latency", "L1V latency", "L2 latency"],
        "Mechanism 1 Latency Delta (full max-wg)",
        "delta (%)",
    )
    grouped_barh(
        FIG / "m1_bar_cpi_full_max_wg.png",
        full,
        ["cpi_vmem_delta_pct", "cpi_total_delta_pct"],
        ["CPI VMem", "CPI total"],
        "Mechanism 1 CPI Delta (full max-wg)",
        "delta (%)",
    )
    dashboard(FIG / "m1_dashboard_full_max_wg.png", rows)
    quadrant(FIG / "m1_speedup_vs_overfetch_bubble.png", rows)
    barh(
        FIG / "m1_speedup_full_max_wg.png",
        full,
        "speedup_pct",
        "Mechanism 1 Speedup (full max-wg only)",
        "speedup (%)",
    )
    barh(
        FIG / "m1_speedup_all_paired.png",
        paired,
        "speedup_pct",
        "Mechanism 1 Speedup (all paired results)",
        "speedup (%)",
    )
    heatmap(FIG / "m1_full_max_wg_delta_heatmap.png", rows)
    scatter(FIG / "m1_au_coalesce_vs_overfetch.png", rows)
    table_image(FIG / "m1_full_max_wg_table.png", rows)


def main():
    rows = collect_rows()
    write_tables(rows)
    write_figures(rows)
    full = sum(1 for row in rows if row["status"] == "full_max_wg")
    paired = sum(1 for row in rows if row["status"] in ("full_max_wg", "paired_short"))
    print(f"wrote {OUT}")
    print(f"paired={paired} full_max_wg={full} total_rows={len(rows)}")


if __name__ == "__main__":
    main()
