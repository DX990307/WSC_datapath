#!/usr/bin/env python3
"""Aggregate data-pipeline costs from akkalat metrics.

This script is intentionally conservative: stage latencies are reported as
observed average request costs, not as additive critical-path components.
"""

from __future__ import annotations

import argparse
import csv
import math
import os
import re
from collections import defaultdict
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

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


def weighted_avg(values: list[tuple[float, float]]) -> float:
    total_w = sum(w for _, w in values)
    if total_w == 0:
        return 0.0
    return sum(v * w for v, w in values) / total_w


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
                meta["active_gpu_count"] += 1
                meta["max_gpu_kernel_time_s"] = max(meta["max_gpu_kernel_time_s"], value)
            elif what == "cu_inst_count":
                meta["cu_count"] += 1
                meta["total_cu_inst"] += value
            elif what == "cu_CPI":
                if value > 0:
                    meta["cu_cpi_sum"] += value
                    meta["cu_cpi_count"] += 1
                    meta["cu_cpi_max"] = max(meta["cu_cpi_max"], value)
            elif what.startswith("CPIStack."):
                name = what.removeprefix("CPIStack.")
                if value > 0:
                    cpi_stack[name]["sum"] += value
                    cpi_stack[name]["count"] += 1

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


def component_request_count(comp: dict[str, float]) -> float:
    stage = str(comp["stage"])
    if is_cache_stage(stage):
        return sum(comp.get(k, 0.0) for k in CACHE_COUNT_METRICS)
    if is_tlb_stage(stage):
        return sum(comp.get(k, 0.0) for k in TLB_COUNT_METRICS)
    if stage == "RDMA":
        return comp.get("incoming_trans_count", 0.0)
    if stage == "GMMU":
        return comp.get("incoming_trans_count", 0.0)
    if stage == "MMU":
        return comp.get("incoming_trans_count", 0.0)
    if stage == "DRAM":
        return comp.get("read_trans_count", 0.0) + comp.get("write_trans_count", 0.0)
    if stage == "IOMMUTLB":
        return comp.get("incoming_req_count", 0.0)
    return 0.0


def aggregate_stage_rows(meta: dict[str, float], components: dict[str, dict[str, float]]) -> list[dict[str, object]]:
    kernel_us = (
        meta.get("max_gpu_kernel_time_s", 0.0)
        or meta.get("driver_total_time_s", 0.0)
    ) * 1e6
    grouped: dict[str, list[dict[str, float]]] = defaultdict(list)
    for comp in components.values():
        grouped[str(comp["stage"])].append(comp)

    rows: list[dict[str, object]] = []
    for stage, comps in grouped.items():
        count = sum(component_request_count(c) for c in comps)
        latency_pairs = []
        for comp in comps:
            c = component_request_count(comp)
            if comp.get("req_average_latency", 0.0) and c:
                latency_pairs.append((comp["req_average_latency"] * 1e9, c))
        if stage == "DRAM":
            read_count = sum(c.get("read_trans_count", 0.0) for c in comps)
            write_count = sum(c.get("write_trans_count", 0.0) for c in comps)
            read_lat = weighted_avg(
                [(c.get("read_avg_latency", 0.0) * 1e9, c.get("read_trans_count", 0.0)) for c in comps]
            )
            write_lat = weighted_avg(
                [(c.get("write_avg_latency", 0.0) * 1e9, c.get("write_trans_count", 0.0)) for c in comps]
            )
            avg_lat = weighted_avg([(read_lat, read_count), (write_lat, write_count)])
            bytes_ = sum(c.get("read_size", 0.0) + c.get("write_size", 0.0) for c in comps)
        else:
            avg_lat = weighted_avg(latency_pairs)
            bytes_ = 0.0

        hit = miss = mshr = 0.0
        if is_cache_stage(stage):
            hit = sum(c.get("read-hit", 0.0) + c.get("write-hit", 0.0) for c in comps)
            miss = sum(c.get("read-miss", 0.0) + c.get("write-miss", 0.0) for c in comps)
            mshr = sum(c.get("read-mshr-hit", 0.0) + c.get("write-mshr-hit", 0.0) for c in comps)
        elif is_tlb_stage(stage):
            hit = sum(c.get("hit", 0.0) for c in comps)
            miss = sum(c.get("miss", 0.0) for c in comps)
            mshr = sum(c.get("mshr-hit", 0.0) for c in comps)

        exposure_us = avg_lat * count / 1000.0
        rows.append(
            {
                "kind": "pipeline_stage",
                "stage": stage,
                "requests": count,
                "avg_latency_ns": avg_lat,
                "latency_exposure_us": exposure_us,
                "exposure_over_kernel_pct": pct(exposure_us, kernel_us),
                "exposure_pressure_share_pct": "",
                "hit_count": hit,
                "miss_count": miss,
                "mshr_hit_count": mshr,
                "miss_or_mshr_pct": pct(miss + mshr, hit + miss + mshr),
                "bytes": bytes_,
                "notes": "",
            }
        )

    if meta.get("cu_cpi_count", 0.0):
        rows.append(
            {
                "kind": "execution",
                "stage": "CU",
                "requests": meta.get("total_cu_inst", 0.0),
                "avg_latency_ns": "",
                "latency_exposure_us": "",
                "exposure_over_kernel_pct": "",
                "exposure_pressure_share_pct": "",
                "hit_count": "",
                "miss_count": "",
                "mshr_hit_count": "",
                "miss_or_mshr_pct": "",
                "bytes": "",
                "notes": (
                    f"avg_CPI={meta['cu_cpi_sum']/meta['cu_cpi_count']:.3f}; "
                    f"max_CPI={meta.get('cu_cpi_max', 0.0):.3f}; "
                    f"CU_count={int(meta.get('cu_count', 0.0))}"
                ),
            }
        )
    fill_pressure_shares(rows)
    return sorted(rows, key=lambda r: str(r["stage"]))


def fill_pressure_shares(rows: list[dict[str, object]]) -> None:
    total_exposure = sum(
        float(r["latency_exposure_us"])
        for r in rows
        if isinstance(r.get("latency_exposure_us"), float)
    )
    if total_exposure == 0:
        return
    for row in rows:
        exposure = row.get("latency_exposure_us")
        if isinstance(exposure, float):
            row["exposure_pressure_share_pct"] = pct(exposure, total_exposure)


def add_l2_source_rows(
    rows: list[dict[str, object]],
    l2_source_summary: Path,
    kernel_us: float,
) -> None:
    if not l2_source_summary.exists():
        return
    grouped: dict[tuple[str, str], dict[str, float]] = defaultdict(lambda: defaultdict(float))
    with l2_source_summary.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            source = (row.get("source_tier") or row.get("source") or "").strip()
            access_type = (row.get("access_type") or "").strip()
            if not source:
                continue
            key = (source, access_type)
            accesses = fnum(row.get("accesses"))
            grouped[key]["accesses"] += accesses
            grouped[key]["bytes"] += fnum(row.get("bytes"))
            grouped[key]["lat_sum"] += fnum(row.get("avg_latency_ns")) * accesses

    for (source, access_type), vals in sorted(grouped.items()):
        count = vals["accesses"]
        avg = vals["lat_sum"] / count if count else 0.0
        exposure_us = avg * count / 1000.0
        rows.append(
            {
                "kind": "l2_source",
                "stage": f"L2Source:{source}:{access_type or 'any'}",
                "requests": count,
                "avg_latency_ns": avg,
                "latency_exposure_us": exposure_us,
                "exposure_over_kernel_pct": pct(exposure_us, kernel_us),
                "exposure_pressure_share_pct": "",
                "hit_count": "",
                "miss_count": "",
                "mshr_hit_count": "",
                "miss_or_mshr_pct": "",
                "bytes": vals["bytes"],
                "notes": "From *_l2_source_summary.csv",
            }
        )
    fill_pressure_shares(rows)


def add_cpi_stack_rows(rows: list[dict[str, object]], cpi_stack: dict[str, dict[str, float]]) -> None:
    stack_totals = {
        name: vals["sum"] / vals["count"]
        for name, vals in cpi_stack.items()
        if name != "total" and vals.get("count", 0.0)
    }
    total_stack = sum(stack_totals.values())
    for name, vals in sorted(cpi_stack.items()):
        count = vals.get("count", 0.0)
        if not count:
            continue
        avg_stack = vals["sum"] / count
        share = pct(stack_totals.get(name, 0.0), total_stack) if name != "total" else 0.0
        rows.append(
            {
                "kind": "cpi_stack",
                "stage": f"CPIStack:{name}",
                "requests": "",
                "avg_latency_ns": "",
                "latency_exposure_us": "",
                "exposure_over_kernel_pct": "",
                "exposure_pressure_share_pct": "",
                "hit_count": "",
                "miss_count": "",
                "mshr_hit_count": "",
                "miss_or_mshr_pct": "",
                "bytes": "",
                "notes": (
                    f"avg_stack_value={avg_stack:.6f}; "
                    f"stack_share={share:.2f}%; "
                    f"components={int(count)}"
                ),
            }
        )


def write_rows(rows: list[dict[str, object]], path: Path) -> None:
    fields = [
        "kind",
        "stage",
        "requests",
        "avg_latency_ns",
        "latency_exposure_us",
        "exposure_over_kernel_pct",
        "exposure_pressure_share_pct",
        "hit_count",
        "miss_count",
        "mshr_hit_count",
        "miss_or_mshr_pct",
        "bytes",
        "notes",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def write_markdown(rows: list[dict[str, object]], meta: dict[str, float], path: Path, metrics_file: Path) -> None:
    kernel_us = meta.get("max_gpu_kernel_time_s", meta.get("driver_total_time_s", 0.0)) * 1e6
    with path.open("w") as f:
        f.write(f"# Data Pipeline Cost Summary\n\n")
        f.write(f"Metrics file: `{metrics_file}`\n\n")
        f.write(f"- Max GPU kernel time: {kernel_us:.3f} us\n")
        f.write(f"- Driver total time: {meta.get('driver_total_time_s', 0.0)*1e6:.3f} us\n")
        if meta.get("cu_cpi_count", 0.0):
            f.write(f"- Average CU CPI: {meta['cu_cpi_sum']/meta['cu_cpi_count']:.3f}\n")
            f.write(f"- Max CU CPI: {meta.get('cu_cpi_max', 0.0):.3f}\n")
            f.write(f"- Total CU instructions: {meta.get('total_cu_inst', 0.0):.0f}\n")
        f.write("\n")
        f.write("Important caveat: stage latencies overlap and are not additive. ")
        f.write("`latency_exposure_us = avg_latency * request_count` is useful for pressure ranking; ")
        f.write("when it greatly exceeds kernel time, much of that request latency must be overlapped by parallelism or compute. ")
        f.write("`Exposure/kernel` is therefore not an additive time percentage and can exceed 100%.\n\n")
        f.write("| Kind | Stage | Requests | Avg latency ns | Exposure us | Exposure/kernel | Pressure share | Miss/MSHR % | Notes |\n")
        f.write("|---|---:|---:|---:|---:|---:|---:|---:|---|\n")
        for r in rows:
            requests = r["requests"]
            if isinstance(requests, float):
                requests_s = f"{requests:.0f}"
            else:
                requests_s = str(requests)
            avg = r["avg_latency_ns"]
            avg_s = f"{avg:.2f}" if isinstance(avg, float) else str(avg)
            exp = r["latency_exposure_us"]
            exp_s = f"{exp:.2f}" if isinstance(exp, float) else str(exp)
            exp_kernel = r["exposure_over_kernel_pct"]
            exp_kernel_s = (
                f"{exp_kernel:.1f}%"
                if isinstance(exp_kernel, float)
                else str(exp_kernel)
            )
            pressure = r["exposure_pressure_share_pct"]
            pressure_s = (
                f"{pressure:.1f}%"
                if isinstance(pressure, float)
                else str(pressure)
            )
            miss = r["miss_or_mshr_pct"]
            miss_s = f"{miss:.2f}" if isinstance(miss, float) else str(miss)
            f.write(
                f"| {r['kind']} | {r['stage']} | {requests_s} | {avg_s} | {exp_s} | {exp_kernel_s} | {pressure_s} | {miss_s} | {r['notes']} |\n"
            )


def plot_latency(rows: list[dict[str, object]], path_prefix: Path) -> None:
    subset = [
        r
        for r in rows
        if r["kind"] in {"pipeline_stage", "l2_source"} and isinstance(r["avg_latency_ns"], float) and r["avg_latency_ns"] > 0
    ]
    subset.sort(key=lambda r: float(r["avg_latency_ns"]), reverse=True)
    labels = [str(r["stage"]) for r in subset]
    vals = [float(r["avg_latency_ns"]) for r in subset]
    fig, ax = plt.subplots(figsize=(9, max(4, 0.35 * len(subset))))
    ax.barh(range(len(subset)), vals, color="#4C78A8")
    ax.set_yticks(range(len(subset)))
    ax.set_yticklabels(labels, fontsize=8)
    ax.invert_yaxis()
    ax.set_xlabel("Average request latency (ns)")
    ax.set_title("Data pipeline average cost by stage")
    ax.grid(axis="x", alpha=0.25)
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(path_prefix.with_suffix(f".{ext}"), dpi=220)
    plt.close(fig)


def infer_l2_source_path(metrics_file: Path) -> Path:
    return metrics_file.with_name(metrics_file.name.removesuffix(".csv") + "_l2_source_summary.csv")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("metrics_file", type=Path)
    parser.add_argument("--l2-source-summary", type=Path)
    parser.add_argument("--out-dir", type=Path)
    args = parser.parse_args()

    out_dir = args.out_dir or args.metrics_file.parent / "figures" / "pipeline_costs"
    out_dir.mkdir(parents=True, exist_ok=True)

    meta, components, cpi_stack = parse_metrics(args.metrics_file)
    rows = aggregate_stage_rows(meta, components)
    add_cpi_stack_rows(rows, cpi_stack)
    l2_source_summary = args.l2_source_summary or infer_l2_source_path(args.metrics_file)
    kernel_us = (
        meta.get("max_gpu_kernel_time_s", 0.0)
        or meta.get("driver_total_time_s", 0.0)
    ) * 1e6
    add_l2_source_rows(rows, l2_source_summary, kernel_us)

    stem = args.metrics_file.name.removesuffix(".csv")
    csv_path = out_dir / f"{stem}_pipeline_costs.csv"
    md_path = out_dir / f"{stem}_pipeline_costs.md"
    write_rows(rows, csv_path)
    write_markdown(rows, meta, md_path, args.metrics_file)
    plot_latency(rows, out_dir / f"{stem}_pipeline_latency")

    print(f"Wrote {csv_path}")
    print(f"Wrote {md_path}")
    if not cpi_stack:
        print("No CPIStack.* rows found. Re-run with -report-cpi-stack or -report-all after the flag patch.")


if __name__ == "__main__":
    main()
