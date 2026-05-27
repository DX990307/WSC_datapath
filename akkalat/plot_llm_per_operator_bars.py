#!/usr/bin/env python3
"""Grouped per-operator bars for baseline vs llm_mixed."""

import argparse
import csv
import os
import tempfile
from pathlib import Path

os.environ.setdefault(
    "MPLCONFIGDIR",
    str(Path(tempfile.gettempdir()) / "matplotlib-cache"),
)

import matplotlib.pyplot as plt


def parse_args():
    parser = argparse.ArgumentParser(
        description="Plot per-operator baseline vs llm_mixed bars.")
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
        help="Metric to plot. Defaults to wall-clock simulation seconds.",
    )
    parser.add_argument(
        "--output",
        default="llm_per_operator_baseline_vs_llm_mixed.png",
        help="Output figure path. Relative paths are written under results_dir.",
    )
    parser.add_argument(
        "--linear",
        action="store_true",
        help="Use a linear x-axis instead of log scale.",
    )
    return parser.parse_args()


def read_rows(path):
    with path.open(newline="") as f:
        reader = csv.DictReader(f)
        rows = []
        for row in reader:
            if row["config"] not in {"baseline", "llm_mixed"}:
                continue
            row["op_index"] = int(row["op_index"])
            for key in [
                "stdout_elapsed_seconds",
                "driver_total_time",
                "command_processor_kernel_time_sum",
            ]:
                row[key] = float(row[key])
            rows.append(row)
        return rows


def metric_label(metric):
    if metric == "stdout_elapsed_seconds":
        return "Wall-clock simulation time (seconds)"
    if metric == "driver_total_time":
        return "Driver total virtual time (seconds)"
    return "Command processor kernel time sum (seconds)"


def short_name(name):
    name = name.replace("layer00_", "")
    replacements = {
        "embedding_to_norm1_transfer": "embed->norm1",
        "norm1_to_attn_q_transfer": "norm1->q",
        "attn_v_to_attn_score_transfer": "v->score",
        "attn_score_to_causal_mask_transfer": "score->mask",
        "attn_softmax_to_attn_value_transfer": "softmax->value",
        "attn_out_to_attn_residual_transfer": "out->attn_res",
        "attn_residual_to_norm2_transfer": "attn_res->norm2",
        "norm2_to_mlp_fc1_transfer": "norm2->fc1",
        "mlp_fc1_to_mlp_gelu_transfer": "fc1->gelu",
        "mlp_fc2_to_mlp_residual_transfer": "fc2->mlp_res",
    }
    return replacements.get(name, name)


def output_path(results_dir, value):
    path = Path(value)
    if path.is_absolute():
        return path
    return results_dir / path


def main():
    args = parse_args()
    results_dir = Path(args.results_dir).resolve()
    rows = read_rows(results_dir / "llm_decomposed_per_op_summary.csv")

    by_key = {}
    for row in rows:
        key = (row["op_index"], row["op_name"])
        by_key.setdefault(key, {})[row["config"]] = row

    paired = []
    for key, configs in sorted(by_key.items()):
        if "baseline" not in configs or "llm_mixed" not in configs:
            continue
        baseline = configs["baseline"][args.metric]
        mixed = configs["llm_mixed"][args.metric]
        paired.append((key[0], key[1], baseline, mixed))

    if not paired:
        raise RuntimeError("no paired baseline/llm_mixed operators found")

    height = max(9.0, 0.36 * len(paired) + 2.2)
    fig, ax = plt.subplots(figsize=(14, height))
    fig.patch.set_facecolor("white")

    y = list(range(len(paired)))
    bar_h = 0.38
    baseline_vals = [max(v[2], 1e-9) for v in paired]
    mixed_vals = [max(v[3], 1e-9) for v in paired]
    labels = [f"{idx:02d} {short_name(name)}" for idx, name, _, _ in paired]

    ax.barh(
        [v - bar_h / 2 for v in y],
        baseline_vals,
        height=bar_h,
        color="#4C78A8",
        label="Baseline",
    )
    ax.barh(
        [v + bar_h / 2 for v in y],
        mixed_vals,
        height=bar_h,
        color="#F58518",
        label="LLM mixed",
    )

    ax.set_yticks(y)
    ax.set_yticklabels(labels, fontsize=9)
    ax.invert_yaxis()
    ax.set_xlabel(metric_label(args.metric))
    if not args.linear:
        ax.set_xscale("log")
        ax.set_xlabel(metric_label(args.metric) + ", log scale")
    ax.set_title("Per-operator comparison: baseline vs LLM mixed")
    ax.grid(axis="x", linestyle="--", alpha=0.25)
    ax.legend(loc="lower right", frameon=False)

    for yi, (_, _, baseline, mixed) in zip(y, paired):
        if mixed <= 0:
            continue
        speedup = baseline / mixed
        x = max(baseline, mixed)
        ax.text(
            x * 1.08,
            yi,
            f"{speedup:.2f}x",
            va="center",
            fontsize=8,
            color="#333333",
        )

    baseline_total = sum(v[2] for v in paired)
    mixed_total = sum(v[3] for v in paired)
    total_speedup = baseline_total / mixed_total if mixed_total else 0.0
    subtitle = (
        f"Total: baseline={baseline_total / 3600:.2f}h, "
        f"llm_mixed={mixed_total / 3600:.2f}h, "
        f"speedup={total_speedup:.2f}x"
        if args.metric == "stdout_elapsed_seconds"
        else f"Total speedup={total_speedup:.2f}x"
    )
    fig.suptitle(subtitle, y=0.995, fontsize=11)
    fig.tight_layout(rect=[0, 0, 1, 0.98])

    out = output_path(results_dir, args.output)
    fig.savefig(out, dpi=220, bbox_inches="tight")
    if out.suffix.lower() != ".pdf":
        fig.savefig(out.with_suffix(".pdf"), bbox_inches="tight")
    print(out)


if __name__ == "__main__":
    main()
