#!/usr/bin/env python3
"""Analyze GPU RDMA imbalance from final metrics.csv files."""

import argparse
import csv
import re
from collections import defaultdict
from pathlib import Path


MECHANISMS = ("m1_m2", "baseline", "m1", "m2")
RDMA_RE = re.compile(r"^GPU\[(\d+)\]\.RDMA$")


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        required=True,
        help="Directory containing *_metrics.csv files.",
    )
    parser.add_argument(
        "--output-prefix",
        default="",
        help="Output prefix. Defaults to <results-dir>/rdma_metrics.",
    )
    parser.add_argument(
        "--benchmarks",
        default="all",
        help="Comma-separated benchmark filter, or all.",
    )
    parser.add_argument(
        "--mechanisms",
        default="baseline",
        help="Comma-separated mechanism filter, or all.",
    )
    parser.add_argument(
        "--min-rdma-transactions",
        type=float,
        default=1,
        help="Minimum outgoing+incoming RDMA transactions for a GPU to count.",
    )
    parser.add_argument("--no-plots", action="store_true")
    return parser.parse_args()


def parse_filter(value):
    if value == "all":
        return None
    return {item.strip() for item in value.split(",") if item.strip()}


def clean_row(row):
    return {key.strip(): value.strip() for key, value in row.items()}


def parse_experiment_name(path):
    name = path.name
    if name.endswith("_metrics.csv"):
        name = name[: -len("_metrics.csv")]

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


def read_metrics(results_dir, benchmark_filter, mechanism_filter):
    experiments = {}
    gpu_rows = {}

    for path in sorted(results_dir.glob("*_metrics.csv")):
        meta = parse_experiment_name(path)
        if benchmark_filter is not None and meta["benchmark"] not in benchmark_filter:
            continue
        if mechanism_filter is not None and meta["mechanism"] not in mechanism_filter:
            continue

        key = (
            meta["target"],
            meta["benchmark"],
            meta["mechanism"],
            meta["config"],
            meta["experiment"],
        )
        experiments[key] = meta
        per_gpu = defaultdict(
            lambda: {
                "outgoing_trans_count": 0.0,
                "incoming_trans_count": 0.0,
                "req_average_latency_ns": 0.0,
            }
        )

        with path.open(newline="") as f:
            reader = csv.DictReader(f)
            for raw in reader:
                row = clean_row(raw)
                match = RDMA_RE.match(row.get("where", ""))
                if not match:
                    continue
                gpu = int(match.group(1))
                what = row.get("what", "")
                try:
                    value = float(row.get("value", "0"))
                except ValueError:
                    value = 0.0
                if what in ("outgoing_trans_count", "incoming_trans_count"):
                    per_gpu[gpu][what] += value
                elif what == "req_average_latency":
                    per_gpu[gpu]["req_average_latency_ns"] = value * 1e9

        gpu_rows[key] = per_gpu

    if not experiments:
        raise SystemExit(f"no matching *_metrics.csv files in {results_dir}")

    return experiments, gpu_rows


def build_rows(experiments, gpu_rows, min_transactions):
    detail_rows = []
    summary_rows = []

    for key in sorted(experiments):
        meta = experiments[key]
        rows = []
        total_out = 0.0
        total_in = 0.0
        for gpu, counters in sorted(gpu_rows.get(key, {}).items()):
            outgoing = counters["outgoing_trans_count"]
            incoming = counters["incoming_trans_count"]
            trans = outgoing + incoming
            latency = counters["req_average_latency_ns"]
            active = trans >= min_transactions and latency > 0
            total_out += outgoing
            total_in += incoming
            detail_rows.append(
                {
                    **meta,
                    "gpu": gpu,
                    "outgoing_trans_count": f"{outgoing:.0f}",
                    "incoming_trans_count": f"{incoming:.0f}",
                    "total_trans_count": f"{trans:.0f}",
                    "req_average_latency_ns": f"{latency:.3f}",
                    "included_in_ratio": int(active),
                }
            )
            if active:
                rows.append((gpu, latency, trans))

        if rows:
            min_gpu, min_latency, min_trans = min(rows, key=lambda item: item[1])
            max_gpu, max_latency, max_trans = max(rows, key=lambda item: item[1])
            ratio = max_latency / min_latency if min_latency else 0.0
            spread = max_latency - min_latency
        else:
            min_gpu = max_gpu = ""
            min_latency = max_latency = ratio = spread = 0.0
            min_trans = max_trans = 0.0

        summary_rows.append(
            {
                **meta,
                "has_rdma_traffic": int(total_out + total_in > 0),
                "included_gpu_count": len(rows),
                "total_outgoing_trans_count": f"{total_out:.0f}",
                "total_incoming_trans_count": f"{total_in:.0f}",
                "min_gpu": min_gpu,
                "min_req_average_latency_ns": f"{min_latency:.3f}",
                "min_gpu_trans_count": f"{min_trans:.0f}",
                "max_gpu": max_gpu,
                "max_req_average_latency_ns": f"{max_latency:.3f}",
                "max_gpu_trans_count": f"{max_trans:.0f}",
                "max_minus_min_ns": f"{spread:.3f}",
                "max_min_ratio": f"{ratio:.3f}",
            }
        )

    return summary_rows, detail_rows


def write_csv(path, rows, fieldnames):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for row in rows:
            writer.writerow(row)


def write_markdown(path, summary_rows):
    lines = [
        "# RDMA Metrics GPU Imbalance",
        "",
        "This summary uses final `GPU[*].RDMA` metrics, not memory-path trace windows.",
        "",
        "| benchmark | mechanism | RDMA traffic | outgoing | incoming | GPUs in ratio | max/min | max-min ns |",
        "|---|---|---:|---:|---:|---:|---:|---:|",
    ]
    for row in sorted(
        summary_rows,
        key=lambda r: (float(r["max_min_ratio"]), r["benchmark"]),
        reverse=True,
    ):
        ratio = (
            f"{float(row['max_min_ratio']):.2f}x"
            if int(row["included_gpu_count"]) > 0
            else "N/A"
        )
        spread = (
            f"{float(row['max_minus_min_ns']):.1f}"
            if int(row["included_gpu_count"]) > 0
            else "N/A"
        )
        lines.append(
            "| {benchmark} | {mechanism} | {traffic} | {outgoing} | {incoming} | "
            "{gpus} | {ratio} | {spread} |".format(
                benchmark=row["benchmark"],
                mechanism=row["mechanism"],
                traffic="yes" if int(row["has_rdma_traffic"]) else "no",
                outgoing=row["total_outgoing_trans_count"],
                incoming=row["total_incoming_trans_count"],
                gpus=row["included_gpu_count"],
                ratio=ratio,
                spread=spread,
            )
        )
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def plot_baseline_ratio(output_prefix, summary_rows):
    try:
        import matplotlib

        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
        import numpy as np
    except Exception as exc:
        print(f"Skipping plots: failed to import matplotlib ({exc})")
        return []

    rows = [row for row in summary_rows if row["mechanism"] == "baseline"]
    if not rows:
        return []
    rows = sorted(
        rows,
        key=lambda r: (
            int(r["included_gpu_count"]) > 0,
            float(r["max_min_ratio"]),
            int(r["has_rdma_traffic"]),
        ),
        reverse=True,
    )

    labels = [row["benchmark"] for row in rows]
    ratios = [float(row["max_min_ratio"]) for row in rows]
    spreads = [float(row["max_minus_min_ns"]) for row in rows]
    active = [int(row["included_gpu_count"]) > 0 for row in rows]
    has_traffic = [int(row["has_rdma_traffic"]) > 0 for row in rows]

    figures_dir = output_prefix.parent / "figures"
    figures_dir.mkdir(parents=True, exist_ok=True)
    fig, ax = plt.subplots(figsize=(max(10, 1.35 * len(rows)), 5.8))
    x = np.arange(len(rows))
    colors = [
        "#4C78A8" if ok else ("#B8B8B8" if traffic else "#DDDDDD")
        for ok, traffic in zip(active, has_traffic)
    ]
    bars = ax.bar(x, ratios, color=colors, width=0.68)
    for bar, ratio, spread, ok, traffic in zip(
        bars, ratios, spreads, active, has_traffic
    ):
        if ok:
            label = f"{ratio:.1f}x\n{spread:.0f} ns"
        elif traffic:
            label = "traffic\nno latency"
        else:
            label = "no RDMA"
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            bar.get_height(),
            label,
            ha="center",
            va="bottom",
            fontsize=10,
        )

    ax.set_title("Baseline GPU RDMA Latency Imbalance", fontsize=19)
    ax.set_ylabel("Max / min per-GPU RDMA avg latency", fontsize=14)
    ax.set_xlabel("Benchmark", fontsize=14)
    ax.set_xticks(x)
    ax.set_xticklabels(labels, rotation=20, ha="right", fontsize=12)
    ax.tick_params(axis="y", labelsize=12)
    ax.grid(axis="y", alpha=0.25)
    ax.set_ylim(0, max(ratios) * 1.22 if max(ratios, default=0) > 0 else 1)
    fig.tight_layout()

    png = output_prefix.parent / "figures" / f"{output_prefix.name}_baseline_max_min_ratio.png"
    pdf = png.with_suffix(".pdf")
    fig.savefig(png, dpi=180)
    fig.savefig(pdf)
    plt.close(fig)
    return [png, pdf]


def main():
    args = parse_args()
    results_dir = Path(args.results_dir)
    output_prefix = (
        Path(args.output_prefix)
        if args.output_prefix
        else results_dir / "rdma_metrics"
    )
    benchmark_filter = parse_filter(args.benchmarks)
    mechanism_filter = parse_filter(args.mechanisms)
    experiments, gpu_rows = read_metrics(
        results_dir, benchmark_filter, mechanism_filter
    )
    summary_rows, detail_rows = build_rows(
        experiments, gpu_rows, args.min_rdma_transactions
    )

    summary_path = Path(str(output_prefix) + "_summary.csv")
    detail_path = Path(str(output_prefix) + "_gpu_detail.csv")
    markdown_path = Path(str(output_prefix) + "_imbalance.md")

    write_csv(
        summary_path,
        summary_rows,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "has_rdma_traffic",
            "included_gpu_count",
            "total_outgoing_trans_count",
            "total_incoming_trans_count",
            "min_gpu",
            "min_req_average_latency_ns",
            "min_gpu_trans_count",
            "max_gpu",
            "max_req_average_latency_ns",
            "max_gpu_trans_count",
            "max_minus_min_ns",
            "max_min_ratio",
        ],
    )
    write_csv(
        detail_path,
        detail_rows,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "gpu",
            "outgoing_trans_count",
            "incoming_trans_count",
            "total_trans_count",
            "req_average_latency_ns",
            "included_in_ratio",
        ],
    )
    write_markdown(markdown_path, summary_rows)
    print(f"Wrote {summary_path}")
    print(f"Wrote {detail_path}")
    print(f"Wrote {markdown_path}")

    if not args.no_plots:
        for path in plot_baseline_ratio(output_prefix, summary_rows):
            print(f"Wrote {path}")


if __name__ == "__main__":
    main()
