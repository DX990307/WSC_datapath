#!/usr/bin/env python3
"""Plot a compact snapshot for LLM-like matmul/conv2d bottleneck runs.

This script is intentionally conservative about partially completed runs:
it records run status from stdout and plots only the metrics/traces that exist.
"""

import argparse
import csv
import math
import os
import re
from collections import defaultdict
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt


STATUS_COLORS = {
    "completed": "#009E73",
    "panic": "#D55E00",
    "incomplete": "#CC79A7",
    "missing_stdout": "#999999",
}


def pct(num, den):
    return 100.0 * num / den if den else 0.0


def int_value(row, key, default=0):
    value = row.get(key, "")
    if value == "":
        return default
    return int(float(value))


def float_value(row, key, default=0.0):
    value = row.get(key, "")
    if value == "":
        return default
    return float(value)


def benchmark_from_name(name):
    match = re.match(r"^baseline_(?P<bench>.+?)_baseline_", name)
    if match:
        return match.group("bench")
    return name


def short_name(bench):
    name = bench
    for prefix in ("conv2d-llm-", "matrixmultiplication-llm-"):
        if name.startswith(prefix):
            name = name[len(prefix) :]
    return name.replace("-pointwise", "-pt").replace("-local", "-local")


def read_status(result_dir):
    statuses = {}
    for stdout in sorted(result_dir.glob("baseline_*_baseline_out.stdout")):
        bench = benchmark_from_name(stdout.name)
        text = stdout.read_text(errors="replace")
        if "Return code: 0" in text:
            status = "completed"
        elif "Panic:" in text or "Return code: 1" in text or "Return code: 2" in text:
            status = "panic"
        elif "Return code:" in text:
            status = "incomplete"
        else:
            status = "incomplete"
        statuses[bench] = status
    return statuses


def discover_benchmarks(result_dir):
    benches = set()
    for path in result_dir.glob("baseline_*_baseline_*"):
        benches.add(benchmark_from_name(path.name))
    return sorted(benches)


def read_page_summary(path):
    out = defaultdict(float)
    pages = 0
    remote_pages = 0
    remote_pages_with_local = 0
    max_sharers = 0
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            pages += 1
            total = int_value(row, "total_accesses")
            remote = int_value(row, "remote_accesses")
            local = int_value(row, "local_accesses")
            sharers = int_value(row, "sharer_count")
            max_sharers = max(max_sharers, sharers)
            out["total_accesses"] += total
            out["remote_accesses"] += remote
            out["local_accesses"] += local
            if sharers > 1:
                out["shared_pages"] += 1
                out["shared_accesses"] += total
                out["shared_remote_accesses"] += remote
            if remote > 0:
                remote_pages += 1
                out["remote_page_accesses"] += total
                if local > 0:
                    remote_pages_with_local += 1
                    out["local_accesses_on_remote_pages"] += local
    out["pages"] = pages
    out["remote_pages"] = remote_pages
    out["remote_pages_with_local"] = remote_pages_with_local
    out["max_sharers"] = max_sharers
    out["remote_access_pct"] = pct(out["remote_accesses"], out["total_accesses"])
    out["shared_access_pct"] = pct(out["shared_accesses"], out["total_accesses"])
    out["shared_remote_pct"] = pct(out["shared_remote_accesses"], out["remote_accesses"])
    out["remote_page_access_pct"] = pct(out["remote_page_accesses"], out["total_accesses"])
    out["remote_pages_with_local_pct"] = pct(remote_pages_with_local, remote_pages)
    out["local_access_on_remote_pages_pct"] = pct(
        out["local_accesses_on_remote_pages"], out["total_accesses"]
    )
    return dict(out)


def read_l2_source_summary(path):
    by_tier = defaultdict(lambda: {"accesses": 0, "bytes": 0, "latency_weight": 0.0})
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            tier = row["source_tier"]
            accesses = int_value(row, "accesses")
            bytes_ = int_value(row, "bytes")
            latency = float_value(row, "avg_latency_ns")
            by_tier[tier]["accesses"] += accesses
            by_tier[tier]["bytes"] += bytes_
            by_tier[tier]["latency_weight"] += accesses * latency
    total = sum(v["accesses"] for v in by_tier.values())
    out = {}
    for tier, vals in by_tier.items():
        acc = vals["accesses"]
        out[f"{tier}_accesses"] = acc
        out[f"{tier}_bytes"] = vals["bytes"]
        out[f"{tier}_access_pct"] = pct(acc, total)
        out[f"{tier}_avg_latency_ns"] = vals["latency_weight"] / acc if acc else 0.0
    out["l2_source_total_accesses"] = total
    return out


def read_remote_fill_reuse(path):
    out = defaultdict(float)
    lines = 0
    reused_lines = 0
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            lines += 1
            fills = int_value(row, "remote_dram_fills")
            reuses = int_value(row, "local_l2_hit_reuses")
            out["remote_dram_fills"] += fills
            out["remote_dram_fill_bytes"] += int_value(row, "remote_dram_fill_bytes")
            out["local_l2_hit_reuses"] += reuses
            out["local_l2_hit_reuse_bytes"] += int_value(row, "local_l2_hit_reuse_bytes")
            if reuses > 0:
                reused_lines += 1
    out["remote_fill_lines"] = lines
    out["remote_fill_reused_lines"] = reused_lines
    out["local_l2_reuse_per_remote_fill"] = (
        out["local_l2_hit_reuses"] / out["remote_dram_fills"]
        if out["remote_dram_fills"]
        else 0.0
    )
    out["reused_line_pct"] = pct(reused_lines, lines)
    return dict(out)


def read_metrics(path):
    rows = []
    seen_metric_rows = set()
    seen_singleton_component_rows = set()
    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            where = row["where"].strip()
            what = row["what"].strip()
            value_text = row["value"].strip()
            exact_key = (where, what, value_text)
            if exact_key in seen_metric_rows:
                continue
            seen_metric_rows.add(exact_key)
            if where in {"MMU", "IOMMUTLB"}:
                duplicate_key = (where, what, value_text)
                if duplicate_key in seen_singleton_component_rows:
                    continue
                seen_singleton_component_rows.add(duplicate_key)
            value = float(value_text)
            if not math.isfinite(value):
                continue
            rows.append((where, what, value))

    out = {}
    for where, what, value in rows:
        if where == "Driver":
            out[what] = value

    stack = defaultdict(float)
    for _, what, value in rows:
        if what.startswith("CPIStack."):
            stack[what.split(".", 1)[1]] += value
    total = stack.get("total", 0.0)
    if total:
        for name, value in stack.items():
            if name != "total":
                out[f"cpistack_{name}_pct"] = pct(value, total)

    def aggregate_cache(names, hit_keys, miss_keys, mshr_keys):
        by_where = defaultdict(lambda: defaultdict(float))
        for where, what, value in rows:
            if any(name in where for name in names):
                by_where[where][what] += value
        hit = miss = mshr = total_req = latency_weight = 0.0
        for metrics in by_where.values():
            h = sum(metrics.get(k, 0.0) for k in hit_keys)
            mi = sum(metrics.get(k, 0.0) for k in miss_keys)
            ms = sum(metrics.get(k, 0.0) for k in mshr_keys)
            req = h + mi + ms
            hit += h
            miss += mi
            mshr += ms
            total_req += req
            latency_weight += req * metrics.get("req_average_latency", 0.0)
        return {
            "req": total_req,
            "hit_pct": pct(hit, total_req),
            "miss_pct": pct(miss, total_req),
            "mshr_pct": pct(mshr, total_req),
            "avg_latency_ns": latency_weight / total_req * 1e9 if total_req else 0.0,
        }

    for label, names, hit_keys, miss_keys, mshr_keys in [
        (
            "l1v",
            ["L1VCache"],
            ["read-hit", "write-hit"],
            ["read-miss", "write-miss"],
            ["read-mshr-hit", "write-mshr-hit"],
        ),
        (
            "l2",
            [".L2["],
            ["hit", "read-hit", "write-hit"],
            ["miss", "read-miss", "write-miss"],
            ["mshr-hit", "read-mshr-hit", "write-mshr-hit"],
        ),
        ("l1vtlb", ["L1VTLB"], ["hit"], ["miss"], ["mshr-hit"]),
        ("l2tlb", ["L2TLB"], ["hit"], ["miss"], ["mshr-hit"]),
    ]:
        stats = aggregate_cache(names, hit_keys, miss_keys, mshr_keys)
        for key, value in stats.items():
            out[f"{label}_{key}"] = value

    def avg_latency_by_count(component, count_key, latency_key):
        by_where = defaultdict(dict)
        for where, what, value in rows:
            if component in where and what in (count_key, latency_key):
                by_where[where][what] = value
        count = sum(metrics.get(count_key, 0.0) for metrics in by_where.values())
        latency = sum(
            metrics.get(count_key, 0.0) * metrics.get(latency_key, 0.0)
            for metrics in by_where.values()
        )
        return count, latency / count * 1e9 if count else 0.0

    for label, component, count_key, latency_key in [
        ("mmu", "MMU", "incoming_trans_count", "req_average_latency"),
        ("dram_read", "DRAM", "read_trans_count", "read_avg_latency"),
    ]:
        count, avg_latency_ns = avg_latency_by_count(component, count_key, latency_key)
        out[f"{label}_req"] = count
        out[f"{label}_avg_latency_ns"] = avg_latency_ns

    return out


def collect(result_dir):
    statuses = read_status(result_dir)
    rows = []
    for bench in discover_benchmarks(result_dir):
        row = {"benchmark": bench, "display": short_name(bench)}
        row["status"] = statuses.get(bench, "missing_stdout")

        page_file = result_dir / f"baseline_{bench}_baseline_sharing_pages.csv"
        metrics_file = result_dir / f"baseline_{bench}_baseline_metrics.csv"
        l2_summary_file = result_dir / f"baseline_{bench}_baseline_metrics_l2_source_summary.csv"
        reuse_file = result_dir / f"baseline_{bench}_baseline_metrics_l2_source_remote_fill_reuse.csv"

        row["has_pages"] = page_file.exists()
        row["has_metrics"] = metrics_file.exists()
        row["has_l2_source"] = l2_summary_file.exists()
        row["has_remote_fill_reuse"] = reuse_file.exists()

        if page_file.exists():
            row.update(read_page_summary(page_file))
        if metrics_file.exists():
            row.update(read_metrics(metrics_file))
        if l2_summary_file.exists():
            row.update(read_l2_source_summary(l2_summary_file))
        if reuse_file.exists():
            row.update(read_remote_fill_reuse(reuse_file))
        rows.append(row)
    return rows


def write_summary(rows, out_dir):
    fieldnames = sorted({key for row in rows for key in row.keys()})
    csv_path = out_dir / "llm_like_bottleneck_snapshot_summary.csv"
    with csv_path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)

    md_path = out_dir / "llm_like_bottleneck_snapshot_summary.md"
    with md_path.open("w") as f:
        f.write("# LLM-like Bottleneck Snapshot\n\n")
        f.write("Partial runs are included only where metrics/traces exist.\n\n")
        f.write(
            "| benchmark | status | remote/total | shared access | remote_gpm | VMem CPI | L1V miss | L2 miss | reuse/fill |\n"
        )
        f.write("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
        for row in rows:
            f.write(
                "| {display} | {status} | {remote:.2f}% | {shared:.2f}% | {remote_gpm:.2f}% | {vmem:.2f}% | {l1v:.2f}% | {l2:.2f}% | {reuse:.6f} |\n".format(
                    display=row["display"],
                    status=row["status"],
                    remote=row.get("remote_access_pct", 0.0),
                    shared=row.get("shared_access_pct", 0.0),
                    remote_gpm=row.get("remote_gpm_access_pct", 0.0),
                    vmem=row.get("cpistack_VMem_pct", 0.0),
                    l1v=row.get("l1v_miss_pct", 0.0),
                    l2=row.get("l2_miss_pct", 0.0),
                    reuse=row.get("local_l2_reuse_per_remote_fill", 0.0),
                )
            )
    return csv_path, md_path


def save(fig, path_prefix):
    fig.tight_layout()
    for ext in ("pdf", "png"):
        fig.savefig(path_prefix.with_suffix(f".{ext}"), dpi=220)
    plt.close(fig)


def label_axes(ax, xs, labels, rotation=20):
    ax.set_xticks(list(xs))
    ax.set_xticklabels(labels, rotation=rotation, ha="right")


def plot_status(rows, out_dir):
    labels = [row["display"] for row in rows]
    colors = [STATUS_COLORS.get(row["status"], "#999999") for row in rows]
    values = [1 for _ in rows]
    fig, ax = plt.subplots(figsize=(max(7, len(rows) * 0.8), 3.2))
    xs = range(len(rows))
    ax.bar(xs, values, color=colors)
    label_axes(ax, xs, labels)
    ax.set_yticks([])
    ax.set_title("Run status in result snapshot")
    for x, row in zip(xs, rows):
        ax.text(x, 0.5, row["status"], rotation=90, ha="center", va="center", color="white", fontsize=8)
    save(fig, out_dir / "01_run_status")


def plot_remote_sharing(rows, out_dir):
    plotted = [row for row in rows if row.get("has_pages")]
    if not plotted:
        return
    labels = [row["display"] for row in plotted]
    xs = list(range(len(plotted)))
    width = 0.24
    fig, ax = plt.subplots(figsize=(max(7, len(plotted) * 1.3), 4.2))
    series = [
        ("Remote / total", "remote_access_pct", "#D55E00"),
        ("Shared-page access / total", "shared_access_pct", "#0072B2"),
        ("Local use on remote pages / total", "local_access_on_remote_pages_pct", "#009E73"),
    ]
    for i, (name, key, color) in enumerate(series):
        vals = [row.get(key, 0.0) for row in plotted]
        pos = [x + (i - 1) * width for x in xs]
        ax.bar(pos, vals, width=width, label=name, color=color)
    ax.set_ylabel("Access share (%)")
    ax.set_ylim(0, 100)
    ax.set_title("Remote access vs true sharing evidence")
    label_axes(ax, xs, labels)
    ax.grid(axis="y", alpha=0.25)
    ax.legend(frameon=False, fontsize=8)
    save(fig, out_dir / "02_remote_sharing_bars")


def plot_l2_source(rows, out_dir):
    plotted = [row for row in rows if row.get("has_l2_source")]
    if not plotted:
        return
    labels = [row["display"] for row in plotted]
    xs = list(range(len(plotted)))
    local = [row.get("local_dram_access_pct", 0.0) for row in plotted]
    remote = [row.get("remote_gpm_access_pct", 0.0) for row in plotted]
    remote_lat = [row.get("remote_gpm_avg_latency_ns", 0.0) for row in plotted]
    fig, ax = plt.subplots(figsize=(max(7, len(plotted) * 1.3), 4.4))
    ax.bar(xs, local, label="Local DRAM source", color="#56B4E9")
    ax.bar(xs, remote, bottom=local, label="Remote GPM source", color="#D55E00")
    ax.set_ylabel("L2 source access share (%)")
    ax.set_ylim(0, 100)
    ax.grid(axis="y", alpha=0.25)
    ax.set_title("L2 source mix and remote-source latency")
    label_axes(ax, xs, labels)
    ax2 = ax.twinx()
    ax2.plot(xs, remote_lat, marker="o", color="#000000", linewidth=1.5, label="Remote avg latency")
    ax2.set_ylabel("Remote source avg latency (ns)")
    lines, line_labels = ax.get_legend_handles_labels()
    lines2, line_labels2 = ax2.get_legend_handles_labels()
    ax.legend(lines + lines2, line_labels + line_labels2, frameon=False, fontsize=8, loc="upper left")
    save(fig, out_dir / "03_l2_source_mix")


def plot_remote_fill_reuse(rows, out_dir):
    plotted = [row for row in rows if row.get("has_remote_fill_reuse")]
    if not plotted:
        return
    labels = [row["display"] for row in plotted]
    xs = list(range(len(plotted)))
    fills = [row.get("remote_dram_fills", 0.0) for row in plotted]
    reuses = [row.get("local_l2_hit_reuses", 0.0) for row in plotted]
    fig, ax = plt.subplots(figsize=(max(7, len(plotted) * 1.3), 4.1))
    ax.bar([x - 0.18 for x in xs], fills, width=0.36, label="Remote DRAM fills", color="#CC79A7")
    ax.bar([x + 0.18 for x in xs], reuses, width=0.36, label="Later local L2 hits", color="#009E73")
    ax.set_yscale("log")
    ax.set_ylabel("Count (log scale)")
    ax.set_title("Remote fills almost never become local-L2 reuse")
    label_axes(ax, xs, labels)
    ax.grid(axis="y", alpha=0.25)
    ax.legend(frameon=False, fontsize=8)
    save(fig, out_dir / "04_remote_fill_reuse")


def plot_cpistack(rows, out_dir):
    plotted = [row for row in rows if "cpistack_VMem_pct" in row]
    if not plotted:
        return
    labels = [row["display"] for row in plotted]
    xs = list(range(len(plotted)))
    series = [
        ("VMem", "cpistack_VMem_pct", "#D55E00"),
        ("Idle", "cpistack_Idle_pct", "#999999"),
        ("VALU", "cpistack_VALU_pct", "#0072B2"),
        ("ScalarMem", "cpistack_ScalarMem_pct", "#E69F00"),
    ]
    fig, ax = plt.subplots(figsize=(max(7, len(plotted) * 1.3), 4.1))
    bottom = [0.0 for _ in plotted]
    for name, key, color in series:
        vals = [row.get(key, 0.0) for row in plotted]
        ax.bar(xs, vals, bottom=bottom, label=name, color=color)
        bottom = [b + v for b, v in zip(bottom, vals)]
    other = [max(0.0, 100.0 - b) for b in bottom]
    ax.bar(xs, other, bottom=bottom, label="Other", color="#BBBBBB")
    ax.set_ylim(0, 100)
    ax.set_ylabel("CPIStack share (%)")
    ax.set_title("Partial CPIStack breakdown")
    label_axes(ax, xs, labels)
    ax.grid(axis="y", alpha=0.25)
    ax.legend(frameon=False, fontsize=8, ncol=5)
    save(fig, out_dir / "05_cpistack_breakdown")


def plot_cache_tlb(rows, out_dir):
    plotted = [row for row in rows if "l1v_req" in row]
    if not plotted:
        return
    labels = [row["display"] for row in plotted]
    components = [
        ("L1VCache", "l1v"),
        ("L2Cache", "l2"),
        ("L1VTLB", "l1vtlb"),
        ("L2TLB", "l2tlb"),
    ]
    fig, axes = plt.subplots(2, 2, figsize=(max(8, len(plotted) * 1.6), 6.4), sharey=True)
    xs = list(range(len(plotted)))
    for ax, (title, prefix) in zip(axes.flat, components):
        bottom = [0.0 for _ in plotted]
        for label, suffix, color in [
            ("Hit", "hit_pct", "#009E73"),
            ("Miss", "miss_pct", "#D55E00"),
            ("MSHR hit", "mshr_pct", "#0072B2"),
        ]:
            vals = [row.get(f"{prefix}_{suffix}", 0.0) for row in plotted]
            ax.bar(xs, vals, bottom=bottom, label=label, color=color)
            bottom = [b + v for b, v in zip(bottom, vals)]
        ax.set_title(title)
        ax.set_ylim(0, 100)
        ax.grid(axis="y", alpha=0.25)
        label_axes(ax, xs, labels, rotation=25)
    axes[0][0].set_ylabel("Share (%)")
    axes[1][0].set_ylabel("Share (%)")
    handles, legend_labels = axes[0][0].get_legend_handles_labels()
    fig.legend(handles, legend_labels, frameon=False, ncol=3, loc="upper center")
    fig.suptitle("Cache/TLB hit-miss-MSHR breakdown", y=1.02)
    save(fig, out_dir / "06_cache_tlb_breakdown")


def plot_latency_overview(rows, out_dir):
    plotted = [row for row in rows if "l1v_avg_latency_ns" in row]
    if not plotted:
        return
    labels = [row["display"] for row in plotted]
    xs = list(range(len(plotted)))
    series = [
        ("L1V", "l1v_avg_latency_ns", "#D55E00"),
        ("L2", "l2_avg_latency_ns", "#56B4E9"),
        ("L1VTLB", "l1vtlb_avg_latency_ns", "#009E73"),
        ("L2TLB", "l2tlb_avg_latency_ns", "#CC79A7"),
        ("DRAM read", "dram_read_avg_latency_ns", "#E69F00"),
    ]
    width = 0.14
    fig, ax = plt.subplots(figsize=(max(8, len(plotted) * 1.6), 4.4))
    for i, (name, key, color) in enumerate(series):
        vals = [max(row.get(key, 0.0), 1e-3) for row in plotted]
        ax.bar([x + (i - 2) * width for x in xs], vals, width=width, label=name, color=color)
    ax.set_yscale("log")
    ax.set_ylabel("Average latency (ns, log scale)")
    ax.set_title("Memory hierarchy latency snapshot")
    label_axes(ax, xs, labels)
    ax.grid(axis="y", alpha=0.25)
    ax.legend(frameon=False, fontsize=8, ncol=5)
    save(fig, out_dir / "07_latency_overview")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("result_dir", type=Path)
    parser.add_argument("--out-dir", type=Path)
    args = parser.parse_args()

    result_dir = args.result_dir
    out_dir = args.out_dir or result_dir / "figures" / "llm_like_bottleneck_snapshot"
    out_dir.mkdir(parents=True, exist_ok=True)

    rows = collect(result_dir)
    write_summary(rows, out_dir)
    plot_status(rows, out_dir)
    plot_remote_sharing(rows, out_dir)
    plot_l2_source(rows, out_dir)
    plot_remote_fill_reuse(rows, out_dir)
    plot_cpistack(rows, out_dir)
    plot_cache_tlb(rows, out_dir)
    plot_latency_overview(rows, out_dir)

    print(f"Wrote figures and summary to {out_dir}")


if __name__ == "__main__":
    raise SystemExit(main())
