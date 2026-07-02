#!/usr/bin/env python3
"""Analyze per-GPU network imbalance from memory-path traces."""

import argparse
import csv
import math
from collections import defaultdict
from pathlib import Path


MECHANISMS = ("m1_m2", "baseline", "m1", "m2")

REQUEST_FINE_STAGES = (
    "cross_gpu_request_endpoint_queue_ns",
    "cross_gpu_request_endpoint_inject_wait_ns",
    "cross_gpu_request_channel_transfer_ns",
    "cross_gpu_request_switch_input_queue_ns",
    "cross_gpu_request_switch_pipeline_ns",
    "cross_gpu_request_switch_route_wait_ns",
    "cross_gpu_request_switch_arb_wait_ns",
    "cross_gpu_request_switch_output_wait_ns",
    "cross_gpu_request_endpoint_assemble_wait_ns",
    "cross_gpu_request_endpoint_deliver_wait_ns",
)

RETURN_FINE_STAGES = (
    "cross_gpu_return_endpoint_queue_ns",
    "cross_gpu_return_endpoint_inject_wait_ns",
    "cross_gpu_return_channel_transfer_ns",
    "cross_gpu_return_switch_input_queue_ns",
    "cross_gpu_return_switch_pipeline_ns",
    "cross_gpu_return_switch_route_wait_ns",
    "cross_gpu_return_switch_arb_wait_ns",
    "cross_gpu_return_switch_output_wait_ns",
    "cross_gpu_return_endpoint_assemble_wait_ns",
    "cross_gpu_return_endpoint_deliver_wait_ns",
)

REQUEST_EDGE_STAGES = (
    "l1v_bottom_send_to_local_rdma_ns",
    "local_rdma_request_output_wait_ns",
    "remote_rdma_request_output_wait_ns",
    "remote_rdma_to_remote_l2_ns",
)

RETURN_EDGE_STAGES = (
    "remote_l2_to_remote_rdma_response_ns",
    "remote_rdma_response_output_wait_ns",
    "local_rdma_response_output_wait_ns",
    "local_rdma_to_l1v_response_ns",
)

REQUEST_COARSE_STAGE = "local_rdma_to_remote_rdma_request_ns"
RETURN_COARSE_STAGE = "remote_rdma_to_local_rdma_response_ns"


class LatencyAgg:
    def __init__(self):
        self.paths = 0
        self.total_ns = 0.0
        self.request_ns = 0.0
        self.return_ns = 0.0
        self.latencies = []

    def add(self, total_ns, request_ns, return_ns):
        self.paths += 1
        self.total_ns += total_ns
        self.request_ns += request_ns
        self.return_ns += return_ns
        self.latencies.append(total_ns)

    @property
    def avg_ns(self):
        return self.total_ns / self.paths if self.paths else 0.0

    @property
    def avg_request_ns(self):
        return self.request_ns / self.paths if self.paths else 0.0

    @property
    def avg_return_ns(self):
        return self.return_ns / self.paths if self.paths else 0.0


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        required=True,
        help="Directory containing *_memory_path_l1v_path_summary.csv files.",
    )
    parser.add_argument(
        "--output-prefix",
        default="",
        help="Output prefix. Defaults to <results-dir>/network_gpu.",
    )
    parser.add_argument(
        "--benchmarks",
        default="all",
        help="Comma-separated benchmark filter, or all.",
    )
    parser.add_argument(
        "--mechanisms",
        default="all",
        help="Comma-separated mechanism filter, or all.",
    )
    parser.add_argument(
        "--min-gpu-paths",
        type=int,
        default=50,
        help="Minimum remote paths for a GPU to count in min/max spread.",
    )
    parser.add_argument(
        "--min-pair-paths",
        type=int,
        default=20,
        help="Minimum remote paths for slowest requester-owner pair reporting.",
    )
    parser.add_argument(
        "--max-heatmap-benchmarks",
        type=int,
        default=6,
        help="Maximum benchmarks to plot as requester-owner heatmaps.",
    )
    parser.add_argument(
        "--no-plots",
        action="store_true",
        help="Only write CSV/Markdown outputs.",
    )
    return parser.parse_args()


def parse_filter(value):
    if value == "all":
        return None
    return {item.strip() for item in value.split(",") if item.strip()}


def parse_experiment_name(path):
    name = path.name
    for suffix in (
        "_metrics_memory_path_l1v_path_summary.csv",
        "_memory_path_l1v_path_summary.csv",
    ):
        if name.endswith(suffix):
            name = name[: -len(suffix)]
            break

    target = ""
    rest = name
    if "_" in name:
        target, rest = name.split("_", 1)

    for mechanism in MECHANISMS:
        marker = f"_{mechanism}_"
        if marker in rest:
            benchmark, config = rest.split(marker, 1)
            return {
                "experiment": name,
                "target": target,
                "benchmark": benchmark,
                "mechanism": mechanism,
                "config": f"{mechanism}_{config}",
            }

    return {
        "experiment": name,
        "target": target,
        "benchmark": rest,
        "mechanism": "",
        "config": "",
    }


def number(row, key):
    value = row.get(key, "")
    if value == "" or value is None:
        return 0.0
    return float(value)


def sum_columns(row, columns):
    return sum(number(row, col) for col in columns)


def canonical_network_latency(row):
    request_fine = sum_columns(row, REQUEST_FINE_STAGES)
    return_fine = sum_columns(row, RETURN_FINE_STAGES)

    request_core = (
        request_fine if request_fine > 0 else number(row, REQUEST_COARSE_STAGE)
    )
    return_core = (
        return_fine if return_fine > 0 else number(row, RETURN_COARSE_STAGE)
    )

    request_ns = request_core + sum_columns(row, REQUEST_EDGE_STAGES)
    return_ns = return_core + sum_columns(row, RETURN_EDGE_STAGES)
    return request_ns + return_ns, request_ns, return_ns


def percentile(values, pct):
    values = sorted(values)
    if not values:
        return 0.0
    if len(values) == 1:
        return values[0]
    pos = (len(values) - 1) * pct / 100.0
    lo = int(math.floor(pos))
    hi = int(math.ceil(pos))
    if lo == hi:
        return values[lo]
    return values[lo] + (values[hi] - values[lo]) * (pos - lo)


def write_csv(path, rows, fieldnames):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for row in rows:
            writer.writerow(row)


def read_traces(results_dir, benchmark_filter, mechanism_filter):
    requester_aggs = defaultdict(LatencyAgg)
    pair_aggs = defaultdict(LatencyAgg)
    experiment_aggs = defaultdict(LatencyAgg)
    metadata = {}

    files = sorted(results_dir.glob("*_memory_path_l1v_path_summary.csv"))
    if not files:
        raise SystemExit(
            f"no *_memory_path_l1v_path_summary.csv files in {results_dir}"
        )

    for path in files:
        meta = parse_experiment_name(path)
        benchmark = meta["benchmark"]
        mechanism = meta["mechanism"]
        if benchmark_filter is not None and benchmark not in benchmark_filter:
            continue
        if mechanism_filter is not None and mechanism not in mechanism_filter:
            continue

        exp_key = (
            meta["target"],
            benchmark,
            mechanism,
            meta["config"],
            meta["experiment"],
        )
        metadata[exp_key] = meta
        # Keep experiments with no remote paths so all benchmarks can appear in
        # motivation plots. Their network imbalance is reported as N/A.
        experiment_aggs[exp_key]

        with path.open(newline="") as f:
            reader = csv.DictReader(f)
            for row in reader:
                if row.get("is_remote") != "true":
                    continue
                total_ns, request_ns, return_ns = canonical_network_latency(row)
                if total_ns <= 0:
                    continue

                requester = int(row["requester_gpm"])
                owner = int(row["owner_gpm"])
                requester_aggs[exp_key + (requester,)].add(
                    total_ns, request_ns, return_ns
                )
                pair_aggs[exp_key + (requester, owner)].add(
                    total_ns, request_ns, return_ns
                )
                experiment_aggs[exp_key].add(total_ns, request_ns, return_ns)

    if not experiment_aggs:
        raise SystemExit("no matching remote memory paths found")

    return metadata, experiment_aggs, requester_aggs, pair_aggs


def build_requester_rows(experiment_aggs, requester_aggs):
    rows = []
    for key, agg in sorted(requester_aggs.items()):
        target, benchmark, mechanism, config, experiment, requester = key
        total = experiment_aggs[key[:-1]].total_ns
        rows.append(
            {
                "experiment": experiment,
                "target": target,
                "benchmark": benchmark,
                "mechanism": mechanism,
                "config": config,
                "requester_gpu": requester,
                "remote_paths": agg.paths,
                "total_network_ns": round(agg.total_ns),
                "avg_network_ns": f"{agg.avg_ns:.3f}",
                "p50_network_ns": f"{percentile(agg.latencies, 50):.3f}",
                "p95_network_ns": f"{percentile(agg.latencies, 95):.3f}",
                "p99_network_ns": f"{percentile(agg.latencies, 99):.3f}",
                "avg_request_network_ns": f"{agg.avg_request_ns:.3f}",
                "avg_return_network_ns": f"{agg.avg_return_ns:.3f}",
                "share_of_experiment_network_pct": (
                    f"{100.0 * agg.total_ns / total:.3f}" if total else "0.000"
                ),
            }
        )
    return rows


def build_pair_rows(experiment_aggs, pair_aggs):
    rows = []
    for key, agg in sorted(pair_aggs.items()):
        target, benchmark, mechanism, config, experiment, requester, owner = key
        total = experiment_aggs[key[:-2]].total_ns
        rows.append(
            {
                "experiment": experiment,
                "target": target,
                "benchmark": benchmark,
                "mechanism": mechanism,
                "config": config,
                "requester_gpu": requester,
                "owner_gpu": owner,
                "remote_paths": agg.paths,
                "total_network_ns": round(agg.total_ns),
                "avg_network_ns": f"{agg.avg_ns:.3f}",
                "p50_network_ns": f"{percentile(agg.latencies, 50):.3f}",
                "p95_network_ns": f"{percentile(agg.latencies, 95):.3f}",
                "p99_network_ns": f"{percentile(agg.latencies, 99):.3f}",
                "avg_request_network_ns": f"{agg.avg_request_ns:.3f}",
                "avg_return_network_ns": f"{agg.avg_return_ns:.3f}",
                "share_of_experiment_network_pct": (
                    f"{100.0 * agg.total_ns / total:.3f}" if total else "0.000"
                ),
            }
        )
    return rows


def build_summary_rows(experiment_aggs, requester_aggs, min_gpu_paths):
    rows = []
    for exp_key, exp_agg in sorted(experiment_aggs.items()):
        gpus = []
        for key, agg in requester_aggs.items():
            if key[:-1] == exp_key:
                gpus.append((key[-1], agg))

        target, benchmark, mechanism, config, experiment = exp_key
        if not gpus:
            rows.append(
                {
                    "experiment": experiment,
                    "target": target,
                    "benchmark": benchmark,
                    "mechanism": mechanism,
                    "config": config,
                    "has_remote_network": 0,
                    "remote_paths": 0,
                    "active_requester_gpus": 0,
                    "stable_requester_gpus": 0,
                    "avg_network_ns": "0.000",
                    "avg_request_network_ns": "0.000",
                    "avg_return_network_ns": "0.000",
                    "min_gpu": "",
                    "min_avg_network_ns": "0.000",
                    "min_gpu_paths": 0,
                    "max_gpu": "",
                    "max_avg_network_ns": "0.000",
                    "max_gpu_paths": 0,
                    "max_minus_min_ns": "0.000",
                    "max_min_ratio": "0.000",
                    "p90_minus_p10_ns": "0.000",
                    "p90_p10_ratio": "0.000",
                    "max_total_share_pct": "0.000",
                }
            )
            continue

        stable = [
            (gpu, agg) for gpu, agg in gpus if agg.paths >= min_gpu_paths
        ]
        if not stable:
            stable = gpus

        avg_values = [(gpu, agg.avg_ns, agg.paths, agg.total_ns) for gpu, agg in stable]
        min_gpu, min_avg, min_paths, _ = min(avg_values, key=lambda item: item[1])
        max_gpu, max_avg, max_paths, _ = max(avg_values, key=lambda item: item[1])
        per_gpu_avg = [avg for _, avg, _, _ in avg_values]
        p10 = percentile(per_gpu_avg, 10)
        p90 = percentile(per_gpu_avg, 90)
        totals = [agg.total_ns for _, agg in gpus]
        max_total_share = max(totals) / sum(totals) * 100.0 if totals else 0.0

        rows.append(
            {
                "experiment": experiment,
                "target": target,
                "benchmark": benchmark,
                "mechanism": mechanism,
                "config": config,
                "has_remote_network": 1,
                "remote_paths": exp_agg.paths,
                "active_requester_gpus": len(gpus),
                "stable_requester_gpus": len(stable),
                "avg_network_ns": f"{exp_agg.avg_ns:.3f}",
                "avg_request_network_ns": f"{exp_agg.avg_request_ns:.3f}",
                "avg_return_network_ns": f"{exp_agg.avg_return_ns:.3f}",
                "min_gpu": min_gpu,
                "min_avg_network_ns": f"{min_avg:.3f}",
                "min_gpu_paths": min_paths,
                "max_gpu": max_gpu,
                "max_avg_network_ns": f"{max_avg:.3f}",
                "max_gpu_paths": max_paths,
                "max_minus_min_ns": f"{max_avg - min_avg:.3f}",
                "max_min_ratio": f"{max_avg / min_avg:.3f}" if min_avg else "0.000",
                "p90_minus_p10_ns": f"{p90 - p10:.3f}",
                "p90_p10_ratio": f"{p90 / p10:.3f}" if p10 else "0.000",
                "max_total_share_pct": f"{max_total_share:.3f}",
            }
        )
    return rows


def sort_benchmarks(summary_rows):
    benchmark_scores = {}
    for row in summary_rows:
        benchmark = row["benchmark"]
        avg = float(row["avg_network_ns"])
        if row["mechanism"] == "baseline":
            benchmark_scores[benchmark] = max(
                benchmark_scores.get(benchmark, 0.0), avg
            )
        else:
            benchmark_scores.setdefault(benchmark, avg)
    return sorted(benchmark_scores, key=lambda b: benchmark_scores[b], reverse=True)


def write_markdown(path, summary_rows, pair_rows, min_pair_paths):
    benchmarks = sort_benchmarks(summary_rows)
    rows_by_key = {
        (r["benchmark"], r["mechanism"], r["config"]): r for r in summary_rows
    }
    mechanisms = sorted({row["mechanism"] for row in summary_rows})

    lines = [
        "# Network GPU Imbalance",
        "",
        (
            "Per-GPU latency is grouped by requester GPU using `requester_gpm` "
            "from the L1V path summary. Network latency uses fine `cross_gpu_*` "
            "stages when present; otherwise it falls back to the coarse "
            "RDMA-to-RDMA segment, plus RDMA edge stages."
        ),
        "",
        "| benchmark | mechanism | avg ns/path | active GPUs | min GPU avg | max GPU avg | max-min | max/min | p90-p10 | max total share |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]

    for benchmark in benchmarks:
        for mechanism in mechanisms:
            matches = [
                row
                for key, row in rows_by_key.items()
                if key[0] == benchmark and key[1] == mechanism
            ]
            for row in sorted(matches, key=lambda r: r["config"]):
                if int(row["remote_paths"]) == 0:
                    lines.append(
                        "| {benchmark} | {mechanism} | N/A | 0 | N/A | "
                        "N/A | N/A | N/A | N/A | N/A |".format(
                            benchmark=benchmark,
                            mechanism=mechanism,
                        )
                    )
                    continue
                lines.append(
                    "| {benchmark} | {mechanism} | {avg:.1f} | {active} | "
                    "GPU{min_gpu} {min_avg:.1f} | GPU{max_gpu} {max_avg:.1f} | "
                    "{spread:.1f} | {ratio:.2f}x | {p90p10:.1f} | {share:.1f}% |".format(
                        benchmark=benchmark,
                        mechanism=mechanism,
                        avg=float(row["avg_network_ns"]),
                        active=row["active_requester_gpus"],
                        min_gpu=row["min_gpu"],
                        min_avg=float(row["min_avg_network_ns"]),
                        max_gpu=row["max_gpu"],
                        max_avg=float(row["max_avg_network_ns"]),
                        spread=float(row["max_minus_min_ns"]),
                        ratio=float(row["max_min_ratio"]),
                        p90p10=float(row["p90_minus_p10_ns"]),
                        share=float(row["max_total_share_pct"]),
                    )
                )

    lines += [
        "",
        f"## Slowest Requester-Owner Pairs",
        "",
        f"Only pairs with at least {min_pair_paths} remote paths are considered.",
        "",
        "| benchmark | mechanism | pair | paths | avg ns | p95 ns |",
        "|---|---|---|---:|---:|---:|",
    ]

    for benchmark in benchmarks:
        for mechanism in mechanisms:
            candidates = [
                row
                for row in pair_rows
                if row["benchmark"] == benchmark
                and row["mechanism"] == mechanism
                and int(row["remote_paths"]) >= min_pair_paths
            ]
            if not candidates:
                continue
            row = max(candidates, key=lambda r: float(r["avg_network_ns"]))
            lines.append(
                "| {benchmark} | {mechanism} | GPU{req}->GPU{owner} | "
                "{paths} | {avg:.1f} | {p95:.1f} |".format(
                    benchmark=benchmark,
                    mechanism=mechanism,
                    req=row["requester_gpu"],
                    owner=row["owner_gpu"],
                    paths=row["remote_paths"],
                    avg=float(row["avg_network_ns"]),
                    p95=float(row["p95_network_ns"]),
                )
            )

    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def maybe_import_matplotlib():
    try:
        import matplotlib

        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
        import numpy as np
        from matplotlib.colors import LogNorm

        return plt, np, LogNorm
    except Exception as exc:
        print(f"Skipping plots: failed to import matplotlib ({exc})")
        return None, None, None


def plot_spread(output_prefix, summary_rows):
    plt, np, _ = maybe_import_matplotlib()
    if plt is None:
        return []

    figures_dir = output_prefix.parent / "figures"
    figures_dir.mkdir(parents=True, exist_ok=True)
    benchmarks = sort_benchmarks(summary_rows)
    mechanisms = sorted({row["mechanism"] for row in summary_rows})
    preferred = [m for m in ("baseline", "m1", "m2", "m1_m2") if m in mechanisms]
    mechanisms = preferred + [m for m in mechanisms if m not in preferred]
    colors = {
        "baseline": "#4C78A8",
        "m1": "#54A24B",
        "m2": "#B279A2",
        "m1_m2": "#F58518",
    }

    x = np.arange(len(benchmarks))
    width = min(0.8 / max(len(mechanisms), 1), 0.36)
    fig, ax = plt.subplots(figsize=(max(10, 1.7 * len(benchmarks)), 5.6))
    for idx, mechanism in enumerate(mechanisms):
        vals = []
        for benchmark in benchmarks:
            rows = [
                row
                for row in summary_rows
                if row["benchmark"] == benchmark and row["mechanism"] == mechanism
            ]
            vals.append(float(rows[0]["p90_minus_p10_ns"]) if rows else 0.0)
        offset = (idx - (len(mechanisms) - 1) / 2.0) * width
        bars = ax.bar(
            x + offset,
            vals,
            width=width,
            label=mechanism,
            color=colors.get(mechanism),
        )
        for bar, value in zip(bars, vals):
            if value > 0:
                ax.text(
                    bar.get_x() + bar.get_width() / 2,
                    value,
                    f"{value:.0f}",
                    ha="center",
                    va="bottom",
                    fontsize=10,
                )

    ax.set_title("Per-GPU Network Latency Spread by Requester GPU", fontsize=18)
    ax.set_ylabel("P90 - P10 of per-GPU avg network latency (ns)", fontsize=13)
    ax.set_xticks(x)
    ax.set_xticklabels(benchmarks, rotation=20, ha="right", fontsize=12)
    ax.tick_params(axis="y", labelsize=12)
    ax.legend(fontsize=12)
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()

    png = figures_dir / f"{output_prefix.name}_latency_spread.png"
    pdf = figures_dir / f"{output_prefix.name}_latency_spread.pdf"
    fig.savefig(png, dpi=180)
    fig.savefig(pdf)
    plt.close(fig)
    return [png, pdf]


def plot_requester_boxplot(output_prefix, requester_rows, summary_rows):
    plt, np, _ = maybe_import_matplotlib()
    if plt is None:
        return []

    figures_dir = output_prefix.parent / "figures"
    figures_dir.mkdir(parents=True, exist_ok=True)
    benchmarks = [
        benchmark
        for benchmark in sort_benchmarks(summary_rows)
        if any(row["benchmark"] == benchmark for row in requester_rows)
    ]
    if not benchmarks:
        return []
    mechanisms = sorted({row["mechanism"] for row in summary_rows})
    preferred = [m for m in ("baseline", "m1", "m2", "m1_m2") if m in mechanisms]
    mechanisms = preferred + [m for m in mechanisms if m not in preferred]
    colors = {
        "baseline": "#4C78A8",
        "m1": "#54A24B",
        "m2": "#B279A2",
        "m1_m2": "#F58518",
    }

    data = []
    positions = []
    tick_pos = []
    tick_labels = []
    pos = 1.0
    for benchmark in benchmarks:
        base_pos = pos
        tick_pos.append(base_pos + (len(mechanisms) - 1) * 0.35)
        tick_labels.append(benchmark)
        for idx, mechanism in enumerate(mechanisms):
            vals = [
                float(row["avg_network_ns"])
                for row in requester_rows
                if row["benchmark"] == benchmark and row["mechanism"] == mechanism
            ]
            data.append(vals)
            positions.append(base_pos + idx * 0.7)
        pos += max(1.8, 0.8 * len(mechanisms))

    fig, ax = plt.subplots(figsize=(max(10, 1.8 * len(benchmarks)), 5.8))
    box = ax.boxplot(
        data,
        positions=positions,
        widths=0.5,
        patch_artist=True,
        showfliers=False,
    )
    for patch, idx in zip(box["boxes"], range(len(data))):
        mechanism = mechanisms[idx % len(mechanisms)]
        patch.set_facecolor(colors.get(mechanism, "#999999"))
        patch.set_alpha(0.78)
    for median in box["medians"]:
        median.set_color("black")
        median.set_linewidth(1.4)

    ax.set_title("Distribution of Per-GPU Average Network Latency", fontsize=18)
    ax.set_ylabel("Avg network latency per remote path (ns)", fontsize=13)
    ax.set_xticks(tick_pos)
    ax.set_xticklabels(tick_labels, rotation=20, ha="right", fontsize=12)
    ax.tick_params(axis="y", labelsize=12)
    ax.grid(axis="y", alpha=0.25)

    from matplotlib.patches import Patch

    ax.legend(
        [
            Patch(facecolor=colors.get(mech, "#999999"), alpha=0.78)
            for mech in mechanisms
        ],
        mechanisms,
        fontsize=12,
    )
    fig.tight_layout()

    png = figures_dir / f"{output_prefix.name}_avg_latency_boxplot.png"
    pdf = figures_dir / f"{output_prefix.name}_avg_latency_boxplot.pdf"
    fig.savefig(png, dpi=180)
    fig.savefig(pdf)
    plt.close(fig)
    return [png, pdf]


def plot_baseline_max_min_ratio(output_prefix, summary_rows):
    plt, np, _ = maybe_import_matplotlib()
    if plt is None:
        return []

    baseline_rows = [
        row for row in summary_rows if row["mechanism"] == "baseline"
    ]
    if not baseline_rows:
        return []

    figures_dir = output_prefix.parent / "figures"
    figures_dir.mkdir(parents=True, exist_ok=True)
    baseline_rows = sorted(
        baseline_rows,
        key=lambda row: (
            int(row["remote_paths"]) > 0,
            float(row["max_min_ratio"]),
        ),
        reverse=True,
    )

    labels = [row["benchmark"] for row in baseline_rows]
    if len(set(labels)) != len(labels):
        labels = [
            f"{row['benchmark']}\n{row['config']}" for row in baseline_rows
        ]
    ratios = [float(row["max_min_ratio"]) for row in baseline_rows]
    spreads = [float(row["max_minus_min_ns"]) for row in baseline_rows]
    has_remote = [int(row["remote_paths"]) > 0 for row in baseline_rows]

    x = np.arange(len(baseline_rows))
    fig, ax = plt.subplots(figsize=(max(9.5, 1.35 * len(baseline_rows)), 5.8))
    colors = ["#4C78A8" if ok else "#B8B8B8" for ok in has_remote]
    bars = ax.bar(x, ratios, color=colors, width=0.68)
    for bar, ratio, spread, ok in zip(bars, ratios, spreads, has_remote):
        label = f"{ratio:.1f}x\n{spread:.0f} ns" if ok else "N/A"
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            bar.get_height(),
            label,
            ha="center",
            va="bottom",
            fontsize=11,
        )

    ax.set_title(
        "Baseline GPU Network Latency Imbalance",
        fontsize=19,
    )
    ax.set_ylabel(
        "Max / min per-GPU avg network latency",
        fontsize=14,
    )
    ax.set_xlabel("Benchmark", fontsize=14)
    ax.set_xticks(x)
    ax.set_xticklabels(labels, rotation=20, ha="right", fontsize=12)
    ax.tick_params(axis="y", labelsize=12)
    ax.grid(axis="y", alpha=0.25)
    ax.set_ylim(0, max(ratios) * 1.22 if max(ratios, default=0) > 0 else 1)
    fig.tight_layout()

    png = figures_dir / f"{output_prefix.name}_baseline_max_min_ratio.png"
    pdf = figures_dir / f"{output_prefix.name}_baseline_max_min_ratio.pdf"
    fig.savefig(png, dpi=180)
    fig.savefig(pdf)
    plt.close(fig)
    return [png, pdf]


def plot_pair_heatmaps(output_prefix, pair_rows, summary_rows, max_benchmarks):
    plt, np, LogNorm = maybe_import_matplotlib()
    if plt is None or max_benchmarks <= 0:
        return []

    figures_dir = output_prefix.parent / "figures"
    figures_dir.mkdir(parents=True, exist_ok=True)
    benchmarks = sort_benchmarks(summary_rows)[:max_benchmarks]
    mechanisms = sorted({row["mechanism"] for row in summary_rows})
    preferred = [m for m in ("baseline", "m1", "m2", "m1_m2") if m in mechanisms]
    mechanisms = preferred + [m for m in mechanisms if m not in preferred]
    written = []

    for benchmark in benchmarks:
        for mechanism in mechanisms:
            rows = [
                row
                for row in pair_rows
                if row["benchmark"] == benchmark and row["mechanism"] == mechanism
            ]
            if not rows:
                continue

            requesters = sorted({int(row["requester_gpu"]) for row in rows})
            owners = sorted({int(row["owner_gpu"]) for row in rows})
            req_index = {gpu: idx for idx, gpu in enumerate(requesters)}
            owner_index = {gpu: idx for idx, gpu in enumerate(owners)}
            latency = np.full((len(owners), len(requesters)), np.nan)
            traffic = np.zeros((len(owners), len(requesters)))

            for row in rows:
                x = req_index[int(row["requester_gpu"])]
                y = owner_index[int(row["owner_gpu"])]
                latency[y, x] = float(row["avg_network_ns"])
                traffic[y, x] = int(row["remote_paths"])

            for kind, matrix, cmap, cbar_label in (
                (
                    "latency",
                    latency,
                    "magma",
                    "Avg network latency per remote path (ns)",
                ),
                ("traffic", traffic, "viridis", "Remote access count"),
            ):
                fig, ax = plt.subplots(figsize=(8.8, 7.0))
                if kind == "traffic" and np.nanmax(matrix) > 1:
                    masked = np.ma.masked_where(matrix <= 0, matrix)
                    image = ax.imshow(
                        masked,
                        origin="lower",
                        aspect="auto",
                        cmap=cmap,
                        norm=LogNorm(vmin=1, vmax=np.nanmax(matrix)),
                    )
                    cbar_label = "Remote access count (log scale)"
                else:
                    image = ax.imshow(matrix, origin="lower", aspect="auto", cmap=cmap)

                ax.set_title(
                    f"{benchmark} {mechanism} GPU-Pair Network {kind.title()}",
                    fontsize=15,
                )
                ax.set_xlabel("Requester GPU", fontsize=12)
                ax.set_ylabel("Owner GPU", fontsize=12)

                x_step = max(1, len(requesters) // 12)
                y_step = max(1, len(owners) // 12)
                ax.set_xticks(range(0, len(requesters), x_step))
                ax.set_xticklabels(requesters[::x_step], fontsize=9)
                ax.set_yticks(range(0, len(owners), y_step))
                ax.set_yticklabels(owners[::y_step], fontsize=9)

                cbar = fig.colorbar(image, ax=ax)
                cbar.set_label(cbar_label, fontsize=11)
                fig.tight_layout()

                png = (
                    figures_dir
                    / f"{output_prefix.name}_pair_{kind}_heatmap_{benchmark}_{mechanism}.png"
                )
                pdf = png.with_suffix(".pdf")
                fig.savefig(png, dpi=180)
                fig.savefig(pdf)
                plt.close(fig)
                written.extend([png, pdf])

    return written


def main():
    args = parse_args()
    results_dir = Path(args.results_dir)
    output_prefix = (
        Path(args.output_prefix)
        if args.output_prefix
        else results_dir / "network_gpu"
    )
    benchmark_filter = parse_filter(args.benchmarks)
    mechanism_filter = parse_filter(args.mechanisms)

    _, experiment_aggs, requester_aggs, pair_aggs = read_traces(
        results_dir, benchmark_filter, mechanism_filter
    )
    requester_rows = build_requester_rows(experiment_aggs, requester_aggs)
    pair_rows = build_pair_rows(experiment_aggs, pair_aggs)
    summary_rows = build_summary_rows(
        experiment_aggs, requester_aggs, args.min_gpu_paths
    )

    requester_path = Path(str(output_prefix) + "_requester_detail.csv")
    pair_path = Path(str(output_prefix) + "_pair_detail.csv")
    summary_path = Path(str(output_prefix) + "_requester_summary.csv")
    markdown_path = Path(str(output_prefix) + "_imbalance.md")

    write_csv(
        requester_path,
        requester_rows,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "requester_gpu",
            "remote_paths",
            "total_network_ns",
            "avg_network_ns",
            "p50_network_ns",
            "p95_network_ns",
            "p99_network_ns",
            "avg_request_network_ns",
            "avg_return_network_ns",
            "share_of_experiment_network_pct",
        ],
    )
    write_csv(
        pair_path,
        pair_rows,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "requester_gpu",
            "owner_gpu",
            "remote_paths",
            "total_network_ns",
            "avg_network_ns",
            "p50_network_ns",
            "p95_network_ns",
            "p99_network_ns",
            "avg_request_network_ns",
            "avg_return_network_ns",
            "share_of_experiment_network_pct",
        ],
    )
    write_csv(
        summary_path,
        summary_rows,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "has_remote_network",
            "remote_paths",
            "active_requester_gpus",
            "stable_requester_gpus",
            "avg_network_ns",
            "avg_request_network_ns",
            "avg_return_network_ns",
            "min_gpu",
            "min_avg_network_ns",
            "min_gpu_paths",
            "max_gpu",
            "max_avg_network_ns",
            "max_gpu_paths",
            "max_minus_min_ns",
            "max_min_ratio",
            "p90_minus_p10_ns",
            "p90_p10_ratio",
            "max_total_share_pct",
        ],
    )
    write_markdown(
        markdown_path, summary_rows, pair_rows, args.min_pair_paths
    )

    print(f"Wrote {summary_path}")
    print(f"Wrote {requester_path}")
    print(f"Wrote {pair_path}")
    print(f"Wrote {markdown_path}")

    if not args.no_plots:
        written = []
        written += plot_spread(output_prefix, summary_rows)
        written += plot_requester_boxplot(
            output_prefix, requester_rows, summary_rows
        )
        written += plot_baseline_max_min_ratio(output_prefix, summary_rows)
        written += plot_pair_heatmaps(
            output_prefix,
            pair_rows,
            summary_rows,
            args.max_heatmap_benchmarks,
        )
        for path in written:
            print(f"Wrote {path}")


if __name__ == "__main__":
    main()
