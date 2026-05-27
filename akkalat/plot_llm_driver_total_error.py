#!/usr/bin/env python3
"""Plot Driver,total_time relative error for llm_mixed vs baseline."""

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
        description="Plot llm_mixed Driver,total_time error against baseline.")
    parser.add_argument(
        "results_dir",
        help="Directory containing llm_decomposed_per_op_summary.csv.",
    )
    parser.add_argument(
        "--output",
        default="llm_driver_total_time_error.png",
        help="Output figure path. Relative paths are written under results_dir.",
    )
    parser.add_argument(
        "--csv-output",
        default="llm_driver_total_time_error.csv",
        help="Output CSV path. Relative paths are written under results_dir.",
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
            row["driver_total_time"] = float(row["driver_total_time"])
            rows.append(row)
        return rows


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


def write_error_csv(path, paired, total_error, mape, weighted_mape):
    with path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([
            "op_index",
            "op_name",
            "baseline_driver_total_time",
            "llm_mixed_driver_total_time",
            "relative_error",
            "relative_error_percent",
            "absolute_error_percent",
        ])
        for item in paired:
            writer.writerow([
                item["op_index"],
                item["op_name"],
                f"{item['baseline']:.12f}",
                f"{item['mixed']:.12f}",
                f"{item['error']:.12f}",
                f"{item['error'] * 100:.6f}",
                f"{abs(item['error']) * 100:.6f}",
            ])
        writer.writerow([])
        writer.writerow(["summary", "total_relative_error", total_error])
        writer.writerow(["summary", "mape", mape])
        writer.writerow(["summary", "weighted_mape", weighted_mape])


def main():
    args = parse_args()
    results_dir = Path(args.results_dir).resolve()
    rows = read_rows(results_dir / "llm_decomposed_per_op_summary.csv")

    by_key = {}
    for row in rows:
        key = (row["op_index"], row["op_name"])
        by_key.setdefault(key, {})[row["config"]] = row

    paired = []
    for (op_index, op_name), configs in sorted(by_key.items()):
        if "baseline" not in configs or "llm_mixed" not in configs:
            continue
        baseline = configs["baseline"]["driver_total_time"]
        mixed = configs["llm_mixed"]["driver_total_time"]
        if baseline <= 0:
            continue
        error = (mixed - baseline) / baseline
        paired.append({
            "op_index": op_index,
            "op_name": op_name,
            "baseline": baseline,
            "mixed": mixed,
            "error": error,
        })

    if not paired:
        raise RuntimeError("no paired baseline/llm_mixed records found")

    baseline_total = sum(item["baseline"] for item in paired)
    mixed_total = sum(item["mixed"] for item in paired)
    total_error = (mixed_total - baseline_total) / baseline_total
    mape = sum(abs(item["error"]) for item in paired) / len(paired)
    weighted_mape = (
        sum(item["baseline"] * abs(item["error"]) for item in paired)
        / baseline_total
    )

    csv_path = output_path(results_dir, args.csv_output)
    write_error_csv(csv_path, paired, total_error, mape, weighted_mape)

    height = max(8.5, 0.34 * len(paired) + 2.0)
    fig, ax = plt.subplots(figsize=(12.5, height))
    fig.patch.set_facecolor("white")

    labels = [
        f"{item['op_index']:02d} {short_name(item['op_name'])}"
        for item in paired
    ]
    errors = [item["error"] * 100 for item in paired]
    y = list(range(len(paired)))
    colors = ["#E45756" if value > 0 else "#4C78A8" for value in errors]

    ax.barh(y, errors, color=colors)
    ax.axvline(0, color="#333333", linewidth=1)
    ax.set_yticks(y)
    ax.set_yticklabels(labels, fontsize=9)
    ax.invert_yaxis()
    ax.set_xlabel(
        "Relative error of Driver,total_time: "
        "(llm_mixed - baseline) / baseline (%)"
    )
    ax.set_title("Per-operator Driver,total_time error")
    ax.grid(axis="x", linestyle="--", alpha=0.25)

    max_abs = max(abs(value) for value in errors)
    offset = max(0.3, max_abs * 0.025)
    for yi, value in zip(y, errors):
        ha = "left" if value >= 0 else "right"
        x = value + offset if value >= 0 else value - offset
        ax.text(x, yi, f"{value:+.2f}%", va="center", ha=ha, fontsize=8)

    fig.suptitle(
        "GPT-7B one-layer: llm_mixed error against baseline "
        f"(total error={total_error * 100:+.2f}%, "
        f"weighted MAPE={weighted_mape * 100:.2f}%)",
        fontsize=13,
        fontweight="bold",
        y=0.997,
    )
    fig.tight_layout(rect=[0, 0, 1, 0.98])

    out = output_path(results_dir, args.output)
    fig.savefig(out, dpi=220, bbox_inches="tight")
    if out.suffix.lower() != ".pdf":
        fig.savefig(out.with_suffix(".pdf"), bbox_inches="tight")
    print(out)
    print(csv_path)


if __name__ == "__main__":
    main()
