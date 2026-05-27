#!/usr/bin/env python3
"""Plot baseline vs llm_mixed decomposed LLM results."""

import argparse
import csv
from collections import defaultdict
from pathlib import Path

import matplotlib.pyplot as plt


def parse_args():
    parser = argparse.ArgumentParser(
        description="Plot baseline vs llm_mixed decomposed LLM results.")
    parser.add_argument(
        "results_dir",
        help="Directory containing llm_decomposed_per_op_summary.csv.",
    )
    parser.add_argument(
        "--metric",
        default="stdout_elapsed_seconds",
        choices=[
            "stdout_elapsed_seconds",
            "driver_total_time",
            "command_processor_kernel_time_sum",
        ],
        help="Metric to compare. Defaults to wall-clock simulation seconds.",
    )
    parser.add_argument(
        "--output",
        default="llm_baseline_vs_llm_mixed.png",
        help="Output figure path. Relative paths are written under results_dir.",
    )
    return parser.parse_args()


def read_rows(path):
    with path.open(newline="") as f:
        reader = csv.DictReader(f)
        rows = []
        for row in reader:
            row["op_index"] = int(row["op_index"])
            for key in [
                "stdout_elapsed_seconds",
                "driver_total_time",
                "command_processor_kernel_time_sum",
            ]:
                row[key] = float(row[key])
            rows.append(row)
        return rows


def category(op_name):
    if "transfer" in op_name:
        return "Transfers"
    if any(
        token in op_name
        for token in [
            "attn_q",
            "attn_k",
            "attn_v",
            "attn_out",
            "mlp_fc1",
            "mlp_fc2",
        ]
    ):
        return "Large GEMMs"
    if "attn_score" in op_name or "attn_value" in op_name:
        return "Attention matmul"
    if "norm" in op_name or "softmax" in op_name or "gelu" in op_name:
        return "Norm/softmax/GELU"
    if "residual" in op_name or "mask" in op_name or "embedding" in op_name:
        return "Elementwise/other"
    return "Other"


def metric_label(metric):
    if metric == "stdout_elapsed_seconds":
        return "Wall-clock simulation time"
    if metric == "driver_total_time":
        return "Driver total virtual time"
    return "Command processor kernel time"


def metric_unit(metric):
    if metric == "stdout_elapsed_seconds":
        return "hours"
    return "ms"


def scale_value(value, metric):
    if metric == "stdout_elapsed_seconds":
        return value / 3600.0
    return value * 1000.0


def output_path(results_dir, value):
    path = Path(value)
    if path.is_absolute():
        return path
    return results_dir / path


def main():
    args = parse_args()
    results_dir = Path(args.results_dir).resolve()
    rows = read_rows(results_dir / "llm_decomposed_per_op_summary.csv")
    rows = [r for r in rows if r["config"] in {"baseline", "llm_mixed"}]

    by_config = defaultdict(list)
    for row in rows:
        by_config[row["config"]].append(row)

    missing = {"baseline", "llm_mixed"} - set(by_config)
    if missing:
        raise RuntimeError(f"missing config(s): {sorted(missing)}")

    category_totals = {
        "baseline": defaultdict(float),
        "llm_mixed": defaultdict(float),
    }
    for config, config_rows in by_config.items():
        for row in config_rows:
            category_totals[config][category(row["op_name"])] += row[args.metric]

    categories = [
        "Large GEMMs",
        "Attention matmul",
        "Transfers",
        "Norm/softmax/GELU",
        "Elementwise/other",
        "Other",
    ]
    categories = [
        c
        for c in categories
        if category_totals["baseline"][c] > 0
        or category_totals["llm_mixed"][c] > 0
    ]

    baseline_by_op = {
        (r["op_index"], r["op_name"]): r for r in by_config["baseline"]
    }
    mixed_by_op = {
        (r["op_index"], r["op_name"]): r for r in by_config["llm_mixed"]
    }
    common_keys = sorted(set(baseline_by_op) & set(mixed_by_op))

    speedups = []
    for key in common_keys:
        base = baseline_by_op[key][args.metric]
        mixed = mixed_by_op[key][args.metric]
        if base <= 0 or mixed <= 0:
            continue
        op_index, op_name = key
        if base < 60 and args.metric == "stdout_elapsed_seconds":
            continue
        speedups.append((base / mixed, op_index, op_name, base, mixed))

    speedups.sort(key=lambda x: x[0], reverse=True)
    speedups = speedups[:10]

    fig, axes = plt.subplots(
        1, 2, figsize=(14, 6.5), gridspec_kw={"width_ratios": [1.0, 1.25]}
    )
    fig.patch.set_facecolor("white")

    colors = {
        "Large GEMMs": "#4C78A8",
        "Attention matmul": "#72B7B2",
        "Transfers": "#F58518",
        "Norm/softmax/GELU": "#54A24B",
        "Elementwise/other": "#B279A2",
        "Other": "#9D755D",
    }

    ax = axes[0]
    y_pos = [1, 0]
    labels = ["baseline", "llm_mixed"]
    left = [0.0, 0.0]
    for cat in categories:
        values = [
            scale_value(category_totals[label][cat], args.metric)
            for label in labels
        ]
        ax.barh(
            y_pos,
            values,
            left=left,
            label=cat,
            color=colors.get(cat, "#BAB0AC"),
            edgecolor="white",
            linewidth=0.7,
        )
        left = [l + v for l, v in zip(left, values)]

    for y, total in zip(y_pos, left):
        ax.text(
            total * 1.01,
            y,
            f"{total:.2f} {metric_unit(args.metric)}",
            va="center",
            fontsize=10,
        )
    ax.set_yticks(y_pos)
    ax.set_yticklabels(["Baseline", "LLM mixed"])
    ax.set_xlabel(f"{metric_label(args.metric)} ({metric_unit(args.metric)})")
    ax.set_title("Total time by operator category")
    ax.grid(axis="x", linestyle="--", alpha=0.25)
    ax.legend(loc="lower right", fontsize=8, frameon=False)

    ax = axes[1]
    if speedups:
        names = [name.replace("layer00_", "") for _, _, name, _, _ in speedups]
        vals = [item[0] for item in speedups]
        ypos = list(range(len(speedups)))
        bar_colors = [
            "#4C78A8" if value >= 1.0 else "#E45756"
            for value in vals
        ]
        ax.barh(ypos, vals, color=bar_colors)
        ax.axvline(1.0, color="#333333", linewidth=1)
        ax.set_yticks(ypos)
        ax.set_yticklabels(names)
        ax.invert_yaxis()
        ax.set_xlabel("Speedup: baseline / llm_mixed")
        ax.set_title("Main per-operator speedups")
        ax.grid(axis="x", linestyle="--", alpha=0.25)
        for y, value in zip(ypos, vals):
            ax.text(value * 1.02, y, f"{value:.2f}x", va="center", fontsize=9)
    else:
        ax.text(0.5, 0.5, "No common high-cost ops", ha="center", va="center")
        ax.axis("off")

    base_total = sum(r[args.metric] for r in by_config["baseline"])
    mixed_total = sum(r[args.metric] for r in by_config["llm_mixed"])
    total_speedup = base_total / mixed_total if mixed_total else 0.0
    fig.suptitle(
        f"GPT-7B one-layer decomposed benchmark: baseline vs llm_mixed "
        f"({total_speedup:.2f}x total speedup)",
        fontsize=14,
        fontweight="bold",
    )
    fig.tight_layout(rect=[0, 0, 1, 0.94])

    out = output_path(results_dir, args.output)
    fig.savefig(out, dpi=220, bbox_inches="tight")
    if out.suffix.lower() != ".pdf":
        fig.savefig(out.with_suffix(".pdf"), bbox_inches="tight")
    print(out)


if __name__ == "__main__":
    main()
