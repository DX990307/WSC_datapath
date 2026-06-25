#!/usr/bin/env python3
"""Estimate hidden and unhidden memory latency for selected benchmarks.

The script uses two different signals:

1. L1VCache request exposure:
   avg_request_latency * request_count.  This is not wall-clock time; it is
   the total latency seen by all vector memory requests before overlap.

2. CPIStack.VMem:
   the part of kernel wall time attributed to vector-memory stalls.  This is
   used as the unhidden memory latency estimate.

The hidden latency estimate is therefore:

    max(0, L1VCache exposure - VMem wall time)

This is a conservative diagnostic, not a cycle-accurate critical-path proof.
"""

from __future__ import annotations

import argparse
import csv
import math
import os
import re
from collections import defaultdict
from pathlib import Path
from typing import Iterable

os.environ.setdefault("MPLCONFIGDIR", "/tmp/akkalat-matplotlib")
Path(os.environ["MPLCONFIGDIR"]).mkdir(parents=True, exist_ok=True)

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


CACHE_COUNT_METRICS = (
    "read-hit",
    "read-miss",
    "read-mshr-hit",
    "write-hit",
    "write-miss",
    "write-mshr-hit",
)
TLB_COUNT_METRICS = ("hit", "miss", "mshr-hit")
DEFAULT_INCLUDE = ("matrixmultiplication", "simpleconvolution", "conv2d")
STAGE_COLUMNS = ("L1VCache", "MMU", "L1VTLB", "L2TLB", "RDMA", "DRAM", "L2Cache")


def fnum(value: str | None) -> float:
    if value is None or value == "":
        return 0.0
    try:
        parsed = float(value)
    except ValueError:
        return 0.0
    if not math.isfinite(parsed):
        return 0.0
    return parsed


def pct(num: float, den: float) -> float:
    return 100.0 * num / den if den else 0.0


def safe_ratio(num: float, den: float) -> float:
    return num / den if den else 0.0


def clean_benchmark_name(path: Path) -> str:
    name = path.name
    if name.startswith("baseline_"):
        name = name.removeprefix("baseline_")
    name = name.removesuffix("_metrics.csv")
    name = name.removesuffix("_baseline")
    return name


def classify_component(where: str) -> str | None:
    if ".L1VCache" in where:
        return "L1VCache"
    if ".L1SCache" in where:
        return "L1SCache"
    if ".L1ICache" in where:
        return "L1ICache"
    if ".L2[" in where:
        return "L2Cache"
    if ".L1VTLB" in where:
        return "L1VTLB"
    if ".L1STLB" in where:
        return "L1STLB"
    if ".L1ITLB" in where:
        return "L1ITLB"
    if ".L2TLB" in where:
        return "L2TLB"
    if ".RDMA" in where:
        return "RDMA"
    if ".GMMU" in where:
        return "GMMU"
    if where == "MMU":
        return "MMU"
    if ".DRAM[" in where:
        return "DRAM"
    if where == "IOMMUTLB":
        return "IOMMUTLB"
    return None


def is_cache_stage(stage: str) -> bool:
    return stage in {"L1VCache", "L1SCache", "L1ICache", "L2Cache"}


def is_tlb_stage(stage: str) -> bool:
    return stage in {"L1VTLB", "L1STLB", "L1ITLB", "L2TLB"}


def weighted_avg(values: Iterable[tuple[float, float]]) -> float:
    values = list(values)
    total_w = sum(w for _, w in values)
    if total_w == 0:
        return 0.0
    return sum(v * w for v, w in values) / total_w


def component_request_count(comp: dict[str, float]) -> float:
    stage = str(comp["stage"])
    if is_cache_stage(stage):
        return sum(comp.get(k, 0.0) for k in CACHE_COUNT_METRICS)
    if is_tlb_stage(stage):
        return sum(comp.get(k, 0.0) for k in TLB_COUNT_METRICS)
    if stage in {"RDMA", "GMMU", "MMU"}:
        return comp.get("incoming_trans_count", 0.0)
    if stage == "DRAM":
        return comp.get("read_trans_count", 0.0) + comp.get("write_trans_count", 0.0)
    if stage == "IOMMUTLB":
        return comp.get("incoming_req_count", 0.0)
    return 0.0


def parse_metrics(path: Path) -> tuple[dict[str, float], dict[str, dict[str, float]], dict[str, dict[str, float]]]:
    meta: dict[str, float] = defaultdict(float)
    components: dict[str, dict[str, float]] = defaultdict(lambda: defaultdict(float))
    cpi_stack: dict[str, dict[str, float]] = defaultdict(lambda: defaultdict(float))
    seen_metric_rows: set[tuple[str, str, str]] = set()
    seen_singleton_component_rows: set[tuple[str, str, str]] = set()

    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            where = (row.get("where") or "").strip()
            what = (row.get("what") or "").strip()
            value_text = row.get("value") or ""
            exact_key = (where, what, value_text)
            if exact_key in seen_metric_rows:
                continue
            seen_metric_rows.add(exact_key)
            value = fnum(value_text)

            if where == "Driver" and what == "total_time":
                meta["driver_total_time_s"] = value
            elif where == "Driver" and what == "kernel_time":
                meta["driver_kernel_time_s"] = value
            elif where.endswith(".CommandProcessor") and what == "kernel_time":
                meta["gpu_kernel_time_sum_s"] += value
                if value > 0:
                    meta["active_gpu_count"] += 1
                meta["max_gpu_kernel_time_s"] = max(meta["max_gpu_kernel_time_s"], value)
            elif what == "cu_CPI" and value > 0:
                meta["cu_cpi_sum"] += value
                meta["cu_cpi_count"] += 1
                meta["cu_cpi_max"] = max(meta["cu_cpi_max"], value)
            elif what == "cu_inst_count":
                meta["total_cu_inst"] += value
            elif what.startswith("CPIStack."):
                stack_name = what.removeprefix("CPIStack.")
                if value > 0:
                    cpi_stack[stack_name]["sum"] += value
                    cpi_stack[stack_name]["count"] += 1

            stage = classify_component(where)
            if not stage:
                continue
            if where in {"MMU", "IOMMUTLB"}:
                duplicate_key = (where, what, value_text)
                if duplicate_key in seen_singleton_component_rows:
                    continue
                seen_singleton_component_rows.add(duplicate_key)
            comp = components[where]
            comp["stage"] = stage
            comp[what] += value

    return dict(meta), components, cpi_stack


def aggregate_stage_stats(components: dict[str, dict[str, float]]) -> dict[str, dict[str, float]]:
    grouped: dict[str, list[dict[str, float]]] = defaultdict(list)
    for comp in components.values():
        grouped[str(comp["stage"])].append(comp)

    stats: dict[str, dict[str, float]] = {}
    for stage, comps in grouped.items():
        requests = sum(component_request_count(comp) for comp in comps)
        if stage == "DRAM":
            read_count = sum(comp.get("read_trans_count", 0.0) for comp in comps)
            write_count = sum(comp.get("write_trans_count", 0.0) for comp in comps)
            read_lat_ns = weighted_avg(
                (comp.get("read_avg_latency", 0.0) * 1e9, comp.get("read_trans_count", 0.0))
                for comp in comps
            )
            write_lat_ns = weighted_avg(
                (comp.get("write_avg_latency", 0.0) * 1e9, comp.get("write_trans_count", 0.0))
                for comp in comps
            )
            avg_latency_ns = weighted_avg(((read_lat_ns, read_count), (write_lat_ns, write_count)))
        else:
            avg_latency_ns = weighted_avg(
                (comp.get("req_average_latency", 0.0) * 1e9, component_request_count(comp))
                for comp in comps
            )

        hit = miss = mshr = 0.0
        if is_cache_stage(stage):
            hit = sum(comp.get("read-hit", 0.0) + comp.get("write-hit", 0.0) for comp in comps)
            miss = sum(comp.get("read-miss", 0.0) + comp.get("write-miss", 0.0) for comp in comps)
            mshr = sum(
                comp.get("read-mshr-hit", 0.0) + comp.get("write-mshr-hit", 0.0)
                for comp in comps
            )
        elif is_tlb_stage(stage):
            hit = sum(comp.get("hit", 0.0) for comp in comps)
            miss = sum(comp.get("miss", 0.0) for comp in comps)
            mshr = sum(comp.get("mshr-hit", 0.0) for comp in comps)

        stats[stage] = {
            "requests": requests,
            "avg_latency_ns": avg_latency_ns,
            "latency_exposure_us": avg_latency_ns * requests / 1000.0,
            "hit_count": hit,
            "miss_count": miss,
            "mshr_hit_count": mshr,
            "miss_or_mshr_pct": pct(miss + mshr, hit + miss + mshr),
        }
    return stats


def aggregate_cpi_stack(cpi_stack: dict[str, dict[str, float]]) -> dict[str, float]:
    avg = {
        name: vals["sum"] / vals["count"]
        for name, vals in cpi_stack.items()
        if vals.get("count", 0.0)
    }
    total_without_total = sum(value for name, value in avg.items() if name != "total")
    total = avg.get("total", total_without_total) or total_without_total
    out = {f"{name}_avg": value for name, value in avg.items()}
    out["stack_total_avg"] = total
    for name, value in avg.items():
        if name == "total":
            continue
        out[f"{name}_share_pct"] = pct(value, total)
    return out


def infer_l2_source_path(metrics_file: Path) -> Path:
    return metrics_file.with_name(metrics_file.name.removesuffix(".csv") + "_l2_source_summary.csv")


def aggregate_l2_sources(path: Path) -> dict[tuple[str, str], dict[str, float]]:
    out: dict[tuple[str, str], dict[str, float]] = defaultdict(lambda: defaultdict(float))
    if not path.exists():
        return out
    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            source = (row.get("source_tier") or row.get("source") or "").strip()
            access_type = (row.get("access_type") or "any").strip() or "any"
            if not source:
                continue
            key = (source, access_type)
            accesses = fnum(row.get("accesses"))
            out[key]["requests"] += accesses
            out[key]["bytes"] += fnum(row.get("bytes"))
            out[key]["lat_sum"] += fnum(row.get("avg_latency_ns")) * accesses

    for vals in out.values():
        requests = vals.get("requests", 0.0)
        avg_latency_ns = vals.get("lat_sum", 0.0) / requests if requests else 0.0
        vals["avg_latency_ns"] = avg_latency_ns
        vals["latency_exposure_us"] = avg_latency_ns * requests / 1000.0
    return out


def discover_metrics(inputs: list[Path], include: list[str]) -> list[Path]:
    selected: list[Path] = []
    include_re = re.compile("|".join(re.escape(x) for x in include), re.IGNORECASE)
    for path in inputs:
        if path.is_file():
            selected.append(path)
            continue
        for candidate in sorted(path.glob("*_metrics.csv")):
            name = candidate.name
            if name.endswith("_l2_source_summary.csv"):
                continue
            if "sharing_summary" in name:
                continue
            if include_re.search(name):
                selected.append(candidate)
    return selected


def summarize_metrics(metrics_file: Path) -> dict[str, object]:
    meta, components, cpi_stack = parse_metrics(metrics_file)
    stages = aggregate_stage_stats(components)
    stack = aggregate_cpi_stack(cpi_stack)
    l2_sources = aggregate_l2_sources(infer_l2_source_path(metrics_file))

    benchmark = clean_benchmark_name(metrics_file)
    kernel_us = (meta.get("max_gpu_kernel_time_s", 0.0) or meta.get("driver_total_time_s", 0.0)) * 1e6
    driver_total_us = meta.get("driver_total_time_s", 0.0) * 1e6
    l1v = stages.get("L1VCache", {})
    l1v_exposure_us = l1v.get("latency_exposure_us", 0.0)
    vmem_share_pct = stack.get("VMem_share_pct", 0.0)
    has_cpi_stack = "VMem_share_pct" in stack and stack.get("stack_total_avg", 0.0) > 0
    unhidden_vmem_us = kernel_us * vmem_share_pct / 100.0 if has_cpi_stack else 0.0
    hidden_l1v_exposure_us = max(0.0, l1v_exposure_us - unhidden_vmem_us) if has_cpi_stack else 0.0
    speedup_upper_bound = (
        1.0 / (1.0 - vmem_share_pct / 100.0)
        if has_cpi_stack and vmem_share_pct < 100.0
        else 0.0
    )

    stage_exposure_total = sum(stages.get(stage, {}).get("latency_exposure_us", 0.0) for stage in STAGE_COLUMNS)
    remote_l2_exposure = sum(
        vals.get("latency_exposure_us", 0.0)
        for (source, _), vals in l2_sources.items()
        if source == "remote_gpm"
    )
    local_l2_exposure = sum(
        vals.get("latency_exposure_us", 0.0)
        for (source, _), vals in l2_sources.items()
        if source == "local_dram"
    )

    row: dict[str, object] = {
        "benchmark": benchmark,
        "metrics_file": str(metrics_file),
        "kernel_time_us": kernel_us,
        "driver_total_time_us": driver_total_us,
        "active_gpus": meta.get("active_gpu_count", 0.0),
        "avg_cu_cpi": safe_ratio(meta.get("cu_cpi_sum", 0.0), meta.get("cu_cpi_count", 0.0)),
        "max_cu_cpi": meta.get("cu_cpi_max", 0.0),
        "has_cpi_stack": has_cpi_stack,
        "vmem_stack_share_pct": vmem_share_pct if has_cpi_stack else "",
        "valu_stack_share_pct": stack.get("VALU_share_pct", "") if has_cpi_stack else "",
        "l1v_requests": l1v.get("requests", 0.0),
        "l1v_avg_latency_ns": l1v.get("avg_latency_ns", 0.0),
        "l1v_exposure_us": l1v_exposure_us,
        "l1v_exposure_over_kernel_x": safe_ratio(l1v_exposure_us, kernel_us),
        "unhidden_vmem_us": unhidden_vmem_us if has_cpi_stack else "",
        "unhidden_vmem_pct_of_kernel": vmem_share_pct if has_cpi_stack else "",
        "hidden_l1v_exposure_us": hidden_l1v_exposure_us if has_cpi_stack else "",
        "hidden_l1v_exposure_pct": pct(hidden_l1v_exposure_us, l1v_exposure_us) if has_cpi_stack else "",
        "unhidden_l1v_exposure_pct": pct(unhidden_vmem_us, l1v_exposure_us) if has_cpi_stack else "",
        "optimization_upper_bound_pct_of_kernel": vmem_share_pct if has_cpi_stack else "",
        "optimization_speedup_upper_bound_x": speedup_upper_bound if has_cpi_stack else "",
        "remote_l2_source_exposure_us": remote_l2_exposure,
        "local_l2_source_exposure_us": local_l2_exposure,
        "remote_l2_source_pressure_share_pct": pct(remote_l2_exposure, remote_l2_exposure + local_l2_exposure),
        "notes": "" if has_cpi_stack else "missing CPIStack.VMem; rerun with -report-cpi-stack or -report-all",
    }

    for stage in STAGE_COLUMNS:
        stat = stages.get(stage, {})
        prefix = stage.lower()
        row[f"{prefix}_requests"] = stat.get("requests", 0.0)
        row[f"{prefix}_avg_latency_ns"] = stat.get("avg_latency_ns", 0.0)
        row[f"{prefix}_exposure_us"] = stat.get("latency_exposure_us", 0.0)
        row[f"{prefix}_pressure_share_pct"] = pct(
            stat.get("latency_exposure_us", 0.0),
            stage_exposure_total,
        )
        row[f"{prefix}_miss_or_mshr_pct"] = stat.get("miss_or_mshr_pct", 0.0)

    for access_type in ("read", "write"):
        vals = l2_sources.get(("remote_gpm", access_type), {})
        row[f"remote_gpm_{access_type}_requests"] = vals.get("requests", 0.0)
        row[f"remote_gpm_{access_type}_avg_latency_ns"] = vals.get("avg_latency_ns", 0.0)
        row[f"remote_gpm_{access_type}_exposure_us"] = vals.get("latency_exposure_us", 0.0)

    return row


def build_stage_detail_rows(metrics_file: Path, summary: dict[str, object]) -> list[dict[str, object]]:
    _, components, _ = parse_metrics(metrics_file)
    stages = aggregate_stage_stats(components)
    l2_sources = aggregate_l2_sources(infer_l2_source_path(metrics_file))

    kernel_us = float(summary.get("kernel_time_us", 0.0) or 0.0)
    rows: list[dict[str, object]] = []
    for stage in STAGE_COLUMNS:
        stat = stages.get(stage, {})
        if not stat:
            continue
        exposure_us = stat.get("latency_exposure_us", 0.0)
        row: dict[str, object] = {
            "benchmark": summary["benchmark"],
            "kind": "pipeline_stage",
            "stage": stage,
            "requests": stat.get("requests", 0.0),
            "avg_latency_ns": stat.get("avg_latency_ns", 0.0),
            "latency_exposure_us": exposure_us,
            "exposure_over_kernel_x": safe_ratio(exposure_us, kernel_us),
            "pressure_share_pct": 0.0,
            "miss_or_mshr_pct": stat.get("miss_or_mshr_pct", 0.0),
            "unhidden_wall_time_us": "",
            "hidden_exposure_us": "",
            "optimization_upper_bound_pct_of_kernel": "",
            "notes": "Stage exposure; these stages can overlap.",
        }
        if stage == "L1VCache" and summary.get("has_cpi_stack"):
            row["unhidden_wall_time_us"] = summary.get("unhidden_vmem_us", "")
            row["hidden_exposure_us"] = summary.get("hidden_l1v_exposure_us", "")
            row["optimization_upper_bound_pct_of_kernel"] = summary.get(
                "optimization_upper_bound_pct_of_kernel", ""
            )
            row["notes"] = "L1V exposure split with CPIStack.VMem."
        rows.append(row)

    for (source, access_type), vals in sorted(l2_sources.items()):
        exposure_us = vals.get("latency_exposure_us", 0.0)
        rows.append(
            {
                "benchmark": summary["benchmark"],
                "kind": "l2_source",
                "stage": f"L2Source:{source}:{access_type}",
                "requests": vals.get("requests", 0.0),
                "avg_latency_ns": vals.get("avg_latency_ns", 0.0),
                "latency_exposure_us": exposure_us,
                "exposure_over_kernel_x": safe_ratio(exposure_us, kernel_us),
                "pressure_share_pct": 0.0,
                "miss_or_mshr_pct": "",
                "unhidden_wall_time_us": "",
                "hidden_exposure_us": "",
                "optimization_upper_bound_pct_of_kernel": "",
                "notes": "L2 fill/source path from *_l2_source_summary.csv.",
            }
        )

    total_exposure = sum(float(row["latency_exposure_us"]) for row in rows)
    for row in rows:
        row["pressure_share_pct"] = pct(float(row["latency_exposure_us"]), total_exposure)
    return rows


def format_value(value: object) -> str:
    if isinstance(value, bool):
        return "yes" if value else "no"
    if isinstance(value, float):
        if value == 0:
            return "0"
        if abs(value) >= 1000:
            return f"{value:.1f}"
        return f"{value:.3f}"
    return str(value)


def format_suffix(value: object, suffix: str) -> str:
    if value == "" or value is None:
        return ""
    return f"{format_value(value)}{suffix}"


def write_csv(rows: list[dict[str, object]], path: Path) -> None:
    fields: list[str] = []
    for row in rows:
        for key in row:
            if key not in fields:
                fields.append(key)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def write_markdown(rows: list[dict[str, object]], path: Path) -> None:
    with path.open("w") as f:
        f.write("# Hidden Latency Summary\n\n")
        f.write("Method: `L1VCache exposure = avg request latency * request count`. ")
        f.write("`Unhidden VMem` comes from `kernel_time * CPIStack.VMem_share`. ")
        f.write("`Hidden exposure` is the remaining L1VCache exposure that did not appear as wall-clock VMem stall time. ")
        f.write("The optimization upper bound is the VMem share of kernel time, assuming vector-memory stalls could be eliminated.\n\n")
        f.write("| Benchmark | Kernel us | L1V exposure/kernel | Unhidden VMem % kernel | Hidden L1V exposure % | Speedup upper bound | Main note |\n")
        f.write("|---|---:|---:|---:|---:|---:|---|\n")
        for row in rows:
            hidden = row.get("hidden_l1v_exposure_pct", "")
            unhidden = row.get("unhidden_vmem_pct_of_kernel", "")
            speedup = row.get("optimization_speedup_upper_bound_x", "")
            f.write(
                "| "
                f"{row['benchmark']} | "
                f"{format_value(row['kernel_time_us'])} | "
                f"{format_suffix(row['l1v_exposure_over_kernel_x'], 'x')} | "
                f"{format_suffix(unhidden, '%')} | "
                f"{format_suffix(hidden, '%')} | "
                f"{format_suffix(speedup, 'x')} | "
                f"{row.get('notes', '')} |\n"
            )

        f.write("\n## Stage Pressure\n\n")
        f.write("Stage pressure is exposure share among selected data-path stages. These stages overlap and should not be added as wall time.\n\n")
        f.write("| Benchmark | L1VCache | MMU | L1VTLB | L2TLB | RDMA | DRAM | Remote L2 source |\n")
        f.write("|---|---:|---:|---:|---:|---:|---:|---:|\n")
        for row in rows:
            f.write(
                "| "
                f"{row['benchmark']} | "
                f"{format_value(row.get('l1vcache_pressure_share_pct', 0.0))}% | "
                f"{format_value(row.get('mmu_pressure_share_pct', 0.0))}% | "
                f"{format_value(row.get('l1vtlb_pressure_share_pct', 0.0))}% | "
                f"{format_value(row.get('l2tlb_pressure_share_pct', 0.0))}% | "
                f"{format_value(row.get('rdma_pressure_share_pct', 0.0))}% | "
                f"{format_value(row.get('dram_pressure_share_pct', 0.0))}% | "
                f"{format_value(row.get('remote_l2_source_pressure_share_pct', 0.0))}% |\n"
            )


def write_stage_detail_markdown(rows: list[dict[str, object]], path: Path) -> None:
    with path.open("w") as f:
        f.write("# Stage Latency Detail\n\n")
        f.write("Each row is one observed data-path step for one benchmark. ")
        f.write("`Exposure/kernel` can be much larger than 1x because requests overlap across CUs, caches, TLBs, RDMA, and DRAM. ")
        f.write("Only the L1VCache row is split into hidden/unhidden latency, using CPIStack.VMem as the wall-time proxy.\n\n")
        f.write("| Benchmark | Kind | Stage | Requests | Avg latency ns | Exposure us | Exposure/kernel | Pressure | Miss/MSHR | Unhidden wall us | Hidden exposure us | Notes |\n")
        f.write("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
        for row in rows:
            f.write(
                "| "
                f"{row['benchmark']} | "
                f"{row['kind']} | "
                f"{row['stage']} | "
                f"{format_value(row['requests'])} | "
                f"{format_value(row['avg_latency_ns'])} | "
                f"{format_value(row['latency_exposure_us'])} | "
                f"{format_suffix(row['exposure_over_kernel_x'], 'x')} | "
                f"{format_suffix(row['pressure_share_pct'], '%')} | "
                f"{format_suffix(row['miss_or_mshr_pct'], '%')} | "
                f"{format_value(row['unhidden_wall_time_us'])} | "
                f"{format_value(row['hidden_exposure_us'])} | "
                f"{row['notes']} |\n"
            )


def plot_hidden_latency(rows: list[dict[str, object]], path_prefix: Path) -> None:
    plotted = [row for row in rows if row.get("has_cpi_stack") and row.get("l1v_exposure_us", 0.0)]
    if not plotted:
        return
    labels = [str(row["benchmark"]) for row in plotted]
    unhidden = [float(row["unhidden_vmem_us"]) for row in plotted]
    hidden = [float(row["hidden_l1v_exposure_us"]) for row in plotted]
    fig, ax = plt.subplots(figsize=(max(7, len(plotted) * 1.8), 4.5))
    xs = range(len(plotted))
    ax.bar(xs, unhidden, color="#D55E00", label="Unhidden VMem wall time")
    ax.bar(xs, hidden, bottom=unhidden, color="#56B4E9", label="Hidden L1V exposure")
    ax.set_yscale("log")
    ax.set_ylabel("Latency exposure (us, log scale)")
    ax.set_title("Hidden vs unhidden vector-memory latency")
    ax.set_xticks(list(xs))
    ax.set_xticklabels(labels, rotation=25, ha="right")
    ax.grid(axis="y", alpha=0.25)
    ax.legend(frameon=False)
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(path_prefix.with_suffix(f".{ext}"), dpi=220)
    plt.close(fig)


def plot_optimization_space(rows: list[dict[str, object]], path_prefix: Path) -> None:
    plotted = [row for row in rows if row.get("has_cpi_stack")]
    if not plotted:
        return
    labels = [str(row["benchmark"]) for row in plotted]
    vals = [float(row["optimization_upper_bound_pct_of_kernel"]) for row in plotted]
    fig, ax = plt.subplots(figsize=(max(7, len(plotted) * 1.8), 4.0))
    ax.bar(range(len(plotted)), vals, color="#009E73")
    ax.set_ylabel("Optimization upper bound (% of kernel time)")
    ax.set_title("Potential gain if VMem stalls are eliminated")
    ax.set_ylim(0, min(100, max(vals) * 1.25 if vals else 100))
    ax.set_xticks(list(range(len(plotted))))
    ax.set_xticklabels(labels, rotation=25, ha="right")
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(path_prefix.with_suffix(f".{ext}"), dpi=220)
    plt.close(fig)


def plot_stage_pressure(rows: list[dict[str, object]], path_prefix: Path) -> None:
    if not rows:
        return
    labels = [str(row["benchmark"]) for row in rows]
    colors = {
        "L1VCache": "#4C78A8",
        "MMU": "#F58518",
        "L1VTLB": "#54A24B",
        "L2TLB": "#B279A2",
        "RDMA": "#E45756",
        "DRAM": "#72B7B2",
        "L2Cache": "#9D755D",
    }
    bottoms = [0.0 for _ in rows]
    fig, ax = plt.subplots(figsize=(max(7, len(rows) * 1.8), 4.4))
    for stage in STAGE_COLUMNS:
        vals = [float(row.get(f"{stage.lower()}_pressure_share_pct", 0.0)) for row in rows]
        ax.bar(range(len(rows)), vals, bottom=bottoms, label=stage, color=colors[stage])
        bottoms = [b + v for b, v in zip(bottoms, vals)]
    ax.set_ylabel("Exposure pressure share (%)")
    ax.set_title("Selected data-path pressure breakdown")
    ax.set_xticks(list(range(len(rows))))
    ax.set_xticklabels(labels, rotation=25, ha="right")
    ax.legend(ncol=4, frameon=False, fontsize=8)
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(path_prefix.with_suffix(f".{ext}"), dpi=220)
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("inputs", nargs="+", type=Path, help="metrics CSV files or result directories")
    parser.add_argument(
        "--include",
        nargs="+",
        default=list(DEFAULT_INCLUDE),
        help="benchmark-name substrings to include when an input is a directory",
    )
    parser.add_argument("--out-dir", type=Path, help="output directory")
    args = parser.parse_args()

    metrics_files = discover_metrics(args.inputs, args.include)
    if not metrics_files:
        raise SystemExit("No matching *_metrics.csv files found.")

    out_dir = args.out_dir or metrics_files[0].parent / "figures" / "hidden_latency"
    out_dir.mkdir(parents=True, exist_ok=True)

    rows = [summarize_metrics(path) for path in metrics_files]
    rows.sort(key=lambda row: str(row["benchmark"]))

    csv_path = out_dir / "hidden_latency_summary.csv"
    md_path = out_dir / "hidden_latency_summary.md"
    detail_rows: list[dict[str, object]] = []
    for row in rows:
        detail_rows.extend(build_stage_detail_rows(Path(str(row["metrics_file"])), row))
    detail_csv_path = out_dir / "hidden_latency_stage_detail.csv"
    detail_md_path = out_dir / "hidden_latency_stage_detail.md"
    write_csv(rows, csv_path)
    write_markdown(rows, md_path)
    write_csv(detail_rows, detail_csv_path)
    write_stage_detail_markdown(detail_rows, detail_md_path)
    plot_hidden_latency(rows, out_dir / "hidden_latency_breakdown")
    plot_optimization_space(rows, out_dir / "hidden_latency_optimization_space")
    plot_stage_pressure(rows, out_dir / "hidden_latency_stage_pressure")

    print(f"Wrote {csv_path}")
    print(f"Wrote {md_path}")
    print(f"Wrote {detail_csv_path}")
    print(f"Wrote {detail_md_path}")
    missing = [str(row["benchmark"]) for row in rows if not row.get("has_cpi_stack")]
    if missing:
        print("Missing CPIStack.VMem for: " + ", ".join(missing))
        print("Re-run those benchmarks with -report-cpi-stack or -report-all to estimate unhidden latency.")


if __name__ == "__main__":
    main()
