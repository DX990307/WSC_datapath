#!/usr/bin/env python3
"""Plot request-level memory-path joint-miss diagnostics.

The script consumes the CSV files emitted by -trace-memory-path:

  <stem>_memory_path_summary.csv
  <stem>_memory_path_joint_miss.csv
  <stem>_memory_path_stage_latency.csv

It defaults to matrixmultiplication/conv2d-style workloads because those are the
current focus for hidden latency and memory-hierarchy pressure.
"""

from __future__ import annotations

import argparse
import csv
import math
import os
import re
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/akkalat-matplotlib")
Path(os.environ["MPLCONFIGDIR"]).mkdir(parents=True, exist_ok=True)

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


DEFAULT_INCLUDE = ("matrixmultiplication", "conv2d", "simpleconvolution")
STAGE_ORDER = (
    "l1v_cache_end_to_end",
    "l1v_tlb",
    "l2_tlb",
    "l2_cache_end_to_end",
    "data_source",
    "remote_gpm",
)
STAGE_LABELS = {
    "l1v_cache_end_to_end": "L1V e2e",
    "l1v_tlb": "L1VTLB",
    "l2_tlb": "L2TLB",
    "l2_cache_end_to_end": "L2 e2e",
    "data_source": "Data source",
    "remote_gpm": "Remote GPM",
}


def fnum(value: str | None) -> float:
    if value in {None, ""}:
        return 0.0
    try:
        parsed = float(value)
    except ValueError:
        return 0.0
    return parsed if math.isfinite(parsed) else 0.0


def pct(num: float, den: float) -> float:
    return 100.0 * num / den if den else 0.0


def benchmark_from_summary(path: Path) -> str:
    name = path.name.removesuffix("_memory_path_summary.csv")
    if name.startswith("baseline_"):
        name = name.removeprefix("baseline_")
    return name.removesuffix("_baseline")


def short_name(name: str) -> str:
    for prefix in ("matrixmultiplication-", "conv2d-", "simpleconvolution-"):
        if name.startswith(prefix):
            name = name[len(prefix):]
    return name.replace("llm-", "").replace("-pointwise", "-pt")


def include_bench(name: str, includes: tuple[str, ...]) -> bool:
    if not includes:
        return True
    return any(token in name for token in includes)


def read_scope_rows(path: Path, key: str = "scope") -> dict[str, dict[str, str]]:
    rows = {}
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            rows[row[key]] = row
    return rows


def read_joint(path: Path, scope: str = "steady") -> dict[str, dict[str, float]]:
    out = {}
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            if row["scope"] != scope:
                continue
            pair = row["pair"]
            out[pair] = {k: fnum(v) for k, v in row.items() if k not in {"scope", "pair"}}
    return out


def read_stages(path: Path, scope: str = "steady") -> dict[str, dict[str, float]]:
    out = {}
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            if row["scope"] != scope:
                continue
            stage = row["stage"]
            out[stage] = {
                "accesses": fnum(row.get("accesses")),
                "avg_latency_ns": fnum(row.get("avg_latency_ns")),
                "total_latency_ns": fnum(row.get("total_latency_ns")),
            }
    return out


def read_metrics_for_hidden_estimate(path: Path) -> dict[str, float]:
    if not path.exists():
        return {}

    kernel_time = 0.0
    stack = {}
    seen = set()
    with path.open(newline="") as f:
        for row in csv.DictReader(f, skipinitialspace=True):
            where = (row.get("where") or "").strip()
            what = (row.get("what") or "").strip()
            value_text = (row.get("value") or "").strip()
            key = (where, what, value_text)
            if key in seen:
                continue
            seen.add(key)
            value = fnum(value_text)
            if where == "Driver" and what == "kernel_time":
                kernel_time = value
            if what.startswith("CPIStack."):
                stack[what.removeprefix("CPIStack.")] = stack.get(
                    what.removeprefix("CPIStack."), 0.0
                ) + value

    total_stack = stack.get("total", 0.0)
    vmem_share = stack.get("VMem", 0.0) / total_stack if total_stack else 0.0
    return {
        "kernel_time_ns": kernel_time * 1e9,
        "vmem_wall_ns": kernel_time * 1e9 * vmem_share,
    }


def load_rows(result_dir: Path, includes: tuple[str, ...]) -> list[dict[str, object]]:
    rows = []
    for summary_path in sorted(result_dir.glob("*_memory_path_summary.csv")):
        bench = benchmark_from_summary(summary_path)
        if not include_bench(bench, includes):
            continue
        stem = summary_path.name.removesuffix("_memory_path_summary.csv")
        joint_path = result_dir / f"{stem}_memory_path_joint_miss.csv"
        stage_path = result_dir / f"{stem}_memory_path_stage_latency.csv"
        metrics_path = result_dir / f"{stem}_metrics.csv"
        if not joint_path.exists() or not stage_path.exists():
            continue
        summary = read_scope_rows(summary_path).get("steady", {})
        rows.append({
            "benchmark": bench,
            "label": short_name(bench),
            "summary": summary,
            "joint": read_joint(joint_path),
            "stages": read_stages(stage_path),
            "hidden": read_metrics_for_hidden_estimate(metrics_path),
        })
    return rows


def savefig(fig, out_dir: Path, name: str) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    fig.tight_layout()
    fig.savefig(out_dir / f"{name}.pdf")
    fig.savefig(out_dir / f"{name}.png", dpi=180)
    plt.close(fig)


def plot_joint_rates(rows: list[dict[str, object]], out_dir: Path) -> None:
    labels = [r["label"] for r in rows]
    strict = []
    not_hit = []
    for row in rows:
        pair = row["joint"].get("any_tlb_vs_l2_cache", {})  # type: ignore[index]
        strict.append(100.0 * pair.get("strict_joint_miss_ratio", 0.0))
        not_hit.append(100.0 * pair.get("not_hit_joint_miss_ratio", 0.0))

    fig, ax = plt.subplots(figsize=(max(8, len(rows) * 0.65), 3.2))
    x = range(len(rows))
    ax.bar([i - 0.18 for i in x], strict, width=0.36, label="strict miss")
    ax.bar([i + 0.18 for i in x], not_hit, width=0.36, label="not-hit")
    ax.set_ylabel("joint miss / requests (%)")
    ax.set_xticks(list(x), labels, rotation=35, ha="right")
    ax.legend(frameon=False)
    ax.grid(axis="y", alpha=0.25)
    savefig(fig, out_dir, "01_joint_miss_rate")


def plot_joint_breakdown(rows: list[dict[str, object]], out_dir: Path) -> None:
    labels = [r["label"] for r in rows]
    neither = []
    tlb_only = []
    cache_only = []
    both = []
    for row in rows:
        pair = row["joint"].get("any_tlb_vs_l2_cache", {})  # type: ignore[index]
        total = pair.get("total", 0.0)
        joint = pair.get("not_hit_joint_miss", 0.0)
        tlb = max(0.0, pair.get("tlb_not_hit", 0.0) - joint)
        cache = max(0.0, pair.get("cache_not_hit", 0.0) - joint)
        none = max(0.0, total - joint - tlb - cache)
        neither.append(pct(none, total))
        tlb_only.append(pct(tlb, total))
        cache_only.append(pct(cache, total))
        both.append(pct(joint, total))

    fig, ax = plt.subplots(figsize=(max(8, len(rows) * 0.65), 3.4))
    x = list(range(len(rows)))
    bottom = [0.0] * len(rows)
    for vals, label, color in [
        (neither, "neither", "#999999"),
        (tlb_only, "TLB only", "#56B4E9"),
        (cache_only, "Cache only", "#E69F00"),
        (both, "TLB+Cache", "#D55E00"),
    ]:
        ax.bar(x, vals, bottom=bottom, label=label, color=color)
        bottom = [a + b for a, b in zip(bottom, vals)]
    ax.set_ylabel("request breakdown (%)")
    ax.set_xticks(x, labels, rotation=35, ha="right")
    ax.set_ylim(0, 100)
    ax.legend(ncol=4, frameon=False, loc="upper center", bbox_to_anchor=(0.5, 1.18))
    savefig(fig, out_dir, "02_joint_miss_breakdown")


def plot_stage_latency(rows: list[dict[str, object]], out_dir: Path) -> None:
    labels = [r["label"] for r in rows]
    fig, ax = plt.subplots(figsize=(max(9, len(rows) * 0.8), 4.0))
    x = list(range(len(rows)))
    width = 0.12
    offsets = [(-2.5 + i) * width for i in range(len(STAGE_ORDER))]
    for stage, offset in zip(STAGE_ORDER, offsets):
        values = [
            r["stages"].get(stage, {}).get("avg_latency_ns", 0.0)  # type: ignore[index]
            for r in rows
        ]
        ax.bar([i + offset for i in x], values, width=width, label=STAGE_LABELS[stage])
    ax.set_ylabel("average latency (ns)")
    ax.set_xticks(x, labels, rotation=35, ha="right")
    ax.legend(ncol=3, frameon=False)
    ax.grid(axis="y", alpha=0.25)
    savefig(fig, out_dir, "03_stage_latency")


def plot_hidden_estimate(rows: list[dict[str, object]], out_dir: Path) -> None:
    labels = [r["label"] for r in rows]
    exposed = []
    unhidden = []
    hidden = []
    for row in rows:
        l1v = row["stages"].get("l1v_cache_end_to_end", {})  # type: ignore[index]
        exposure = l1v.get("total_latency_ns", 0.0)
        vmem_wall = row["hidden"].get("vmem_wall_ns", 0.0)  # type: ignore[index]
        exposed.append(exposure / 1e9)
        unhidden.append(vmem_wall / 1e9)
        hidden.append(max(0.0, exposure - vmem_wall) / 1e9)

    fig, ax = plt.subplots(figsize=(max(8, len(rows) * 0.65), 3.4))
    x = range(len(rows))
    ax.bar([i - 0.22 for i in x], exposed, width=0.22, label="L1V exposure")
    ax.bar(x, unhidden, width=0.22, label="VMem wall est.")
    ax.bar([i + 0.22 for i in x], hidden, width=0.22, label="hidden est.")
    ax.set_ylabel("latency exposure (s)")
    ax.set_xticks(list(x), labels, rotation=35, ha="right")
    ax.legend(frameon=False)
    ax.grid(axis="y", alpha=0.25)
    savefig(fig, out_dir, "04_hidden_latency_estimate")


def plot_direct_path(rows: list[dict[str, object]], out_dir: Path) -> None:
    labels = [r["label"] for r in rows]
    opportunity = []
    ns_per_req = []
    for row in rows:
        pair = row["joint"].get("any_tlb_vs_l2_cache", {})  # type: ignore[index]
        stages = row["stages"]  # type: ignore[assignment]
        ratio_value = pair.get("not_hit_joint_miss_ratio", 0.0)
        downstream = (
            stages.get("l2_tlb", {}).get("avg_latency_ns", 0.0)
            + stages.get("data_source", {}).get("avg_latency_ns", 0.0)
            + stages.get("remote_gpm", {}).get("avg_latency_ns", 0.0)
        )
        opportunity.append(100.0 * ratio_value)
        ns_per_req.append(ratio_value * downstream)

    fig, ax1 = plt.subplots(figsize=(max(8, len(rows) * 0.65), 3.4))
    x = list(range(len(rows)))
    ax1.bar([i - 0.18 for i in x], opportunity, width=0.36, color="#D55E00", label="joint not-hit")
    ax1.set_ylabel("direct-path candidate requests (%)")
    ax1.set_xticks(x, labels, rotation=35, ha="right")
    ax2 = ax1.twinx()
    ax2.plot([i + 0.18 for i in x], ns_per_req, color="#0072B2", marker="o", label="candidate ns/request")
    ax2.set_ylabel("candidate latency per request (ns)")
    ax1.grid(axis="y", alpha=0.25)
    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2, frameon=False, loc="upper left")
    savefig(fig, out_dir, "05_direct_path_opportunity")


def write_summary_csv(rows: list[dict[str, object]], out_dir: Path) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)
    path = out_dir / "memory_path_analysis_summary.csv"
    fields = [
        "benchmark",
        "steady_accesses",
        "remote_ratio_pct",
        "strict_joint_any_tlb_l2_pct",
        "not_hit_joint_any_tlb_l2_pct",
        "l1v_avg_latency_ns",
        "l2_avg_latency_ns",
        "l1vtlb_avg_latency_ns",
        "l2tlb_avg_latency_ns",
        "data_source_avg_latency_ns",
        "remote_gpm_avg_latency_ns",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            summary = row["summary"]  # type: ignore[assignment]
            pair = row["joint"].get("any_tlb_vs_l2_cache", {})  # type: ignore[index]
            stages = row["stages"]  # type: ignore[assignment]
            writer.writerow({
                "benchmark": row["benchmark"],
                "steady_accesses": summary.get("total_accesses", "0"),
                "remote_ratio_pct": 100.0 * fnum(summary.get("remote_ratio")),
                "strict_joint_any_tlb_l2_pct": (
                    100.0 * pair.get("strict_joint_miss_ratio", 0.0)
                ),
                "not_hit_joint_any_tlb_l2_pct": (
                    100.0 * pair.get("not_hit_joint_miss_ratio", 0.0)
                ),
                "l1v_avg_latency_ns": stages.get("l1v_cache_end_to_end", {}).get("avg_latency_ns", 0.0),
                "l2_avg_latency_ns": stages.get("l2_cache_end_to_end", {}).get("avg_latency_ns", 0.0),
                "l1vtlb_avg_latency_ns": stages.get("l1v_tlb", {}).get("avg_latency_ns", 0.0),
                "l2tlb_avg_latency_ns": stages.get("l2_tlb", {}).get("avg_latency_ns", 0.0),
                "data_source_avg_latency_ns": stages.get("data_source", {}).get("avg_latency_ns", 0.0),
                "remote_gpm_avg_latency_ns": stages.get("remote_gpm", {}).get("avg_latency_ns", 0.0),
            })


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("result_dir", type=Path)
    parser.add_argument(
        "--include",
        default=",".join(DEFAULT_INCLUDE),
        help="Comma-separated benchmark substrings to include. Empty means all.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Output directory. Defaults to <result_dir>/figures/memory_path_analysis.",
    )
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    includes = tuple(token.strip() for token in args.include.split(",") if token.strip())
    out_dir = args.out_dir or args.result_dir / "figures" / "memory_path_analysis"
    rows = load_rows(args.result_dir, includes)
    if not rows:
        raise SystemExit(f"No memory-path rows found in {args.result_dir}")

    write_summary_csv(rows, out_dir)
    plot_joint_rates(rows, out_dir)
    plot_joint_breakdown(rows, out_dir)
    plot_stage_latency(rows, out_dir)
    plot_hidden_estimate(rows, out_dir)
    plot_direct_path(rows, out_dir)
    print(f"Wrote memory-path analysis for {len(rows)} benchmarks to {out_dir}")


if __name__ == "__main__":
    main()
