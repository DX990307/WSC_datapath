#!/usr/bin/env python3
"""Plot baseline / llm_mixed ratio per benchmark."""

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
        description="Collect and plot baseline/llm_mixed ratio.")
    parser.add_argument(
        "results_dir",
        help="Directory containing llm_decomposed_per_op_summary.csv.",
    )
    parser.add_argument(
        "--output",
        default="llm_driver_total_time_baseline_over_llm_mixed.png",
        help="Output figure path. Relative paths are written under results_dir.",
    )
    parser.add_argument(
        "--csv-output",
        default="llm_driver_total_time_baseline_over_llm_mixed.csv",
        help="Output CSV path. Relative paths are written under results_dir.",
    )
    parser.add_argument(
        "--metric",
        default="driver_total_time",
        choices=["driver_total_time", "stdout_elapsed_seconds"],
        help="Metric to compare. Use stdout_elapsed_seconds for simulation wall time.",
    )
    parser.add_argument(
        "--vertical",
        action="store_true",
        help="Use benchmark on x-axis and percentage on y-axis.",
    )
    parser.add_argument(
        "--split-count",
        type=int,
        default=1,
        help="Split the figure into this many parts for readability.",
    )
    parser.add_argument(
        "--font-scale",
        type=float,
        default=1.0,
        help="Scale all plot fonts. Use 1.4-1.8 for report figures.",
    )
    parser.add_argument(
        "--dpi",
        type=int,
        default=300,
        help="PNG output DPI.",
    )
    parser.add_argument(
        "--bar-width",
        type=float,
        default=0.55,
        help="Bar width for vertical plots, or bar height for horizontal plots.",
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
            row["stdout_elapsed_seconds"] = float(row["stdout_elapsed_seconds"])
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


def metric_display_name(metric):
    if metric == "stdout_elapsed_seconds":
        return "simulation wall time"
    return "Driver,total_time"


def paired_records(rows, metric):
    by_key = {}
    for row in rows:
        key = (row["op_index"], row["op_name"])
        by_key.setdefault(key, {})[row["config"]] = row

    paired = []
    for (op_index, op_name), configs in sorted(by_key.items()):
        if "baseline" not in configs or "llm_mixed" not in configs:
            continue
        baseline = configs["baseline"][metric]
        llm_mixed = configs["llm_mixed"][metric]
        if llm_mixed <= 0:
            continue
        ratio = baseline / llm_mixed
        paired.append({
            "op_index": op_index,
            "benchmark": op_name,
            "baseline": baseline,
            "llm_mixed": llm_mixed,
            "ratio": ratio,
        })
    return paired


def write_csv(path, records, metric):
    total_baseline = sum(r["baseline"] for r in records)
    total_llm_mixed = sum(r["llm_mixed"] for r in records)
    total_ratio = total_baseline / total_llm_mixed

    with path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([
            "op_index",
            "benchmark",
            f"baseline_{metric}",
            f"llm_mixed_{metric}",
            "baseline_over_llm_mixed",
            "baseline_over_llm_mixed_percent",
            "ratio_minus_1_percent",
        ])
        for r in records:
            writer.writerow([
                r["op_index"],
                r["benchmark"],
                f"{r['baseline']:.12f}",
                f"{r['llm_mixed']:.12f}",
                f"{r['ratio']:.12f}",
                f"{r['ratio'] * 100:.6f}",
                f"{(r['ratio'] - 1) * 100:.6f}",
            ])
        writer.writerow([
            "TOTAL",
            "TOTAL",
            f"{total_baseline:.12f}",
            f"{total_llm_mixed:.12f}",
            f"{total_ratio:.12f}",
            f"{total_ratio * 100:.6f}",
            f"{(total_ratio - 1) * 100:.6f}",
        ])


def split_labels_and_ratios(labels, ratios, split_count):
    if split_count <= 1:
        return [(labels, ratios, "")]

    has_total = labels and labels[-1] == "TOTAL"
    op_labels = labels[:-1] if has_total else labels
    op_ratios = ratios[:-1] if has_total else ratios
    total_label = labels[-1] if has_total else None
    total_ratio = ratios[-1] if has_total else None

    chunk_size = (len(op_labels) + split_count - 1) // split_count
    chunks = []
    for part in range(split_count):
        start = part * chunk_size
        end = min(len(op_labels), start + chunk_size)
        if start >= end:
            continue
        chunk_labels = op_labels[start:end]
        chunk_ratios = op_ratios[start:end]
        if has_total and part == split_count - 1:
            chunk_labels = chunk_labels + [total_label]
            chunk_ratios = chunk_ratios + [total_ratio]
        chunks.append((chunk_labels, chunk_ratios, f"_part{part + 1}"))
    return chunks


def with_suffix_part(path, part_suffix):
    if not part_suffix:
        return path
    return path.with_name(f"{path.stem}{part_suffix}{path.suffix}")


def vertical_label(label):
    if label == "TOTAL":
        return label
    return label.replace(" ", "\n", 1)


def draw_plot(args, labels, ratios, display_name, total_ratio, out):
    fs = args.font_scale
    tick_font = 13 * fs
    axis_font = 17 * fs
    title_font = 18 * fs
    suptitle_font = 21 * fs
    value_font = 13 * fs

    if args.vertical:
        width = max(14.0, (0.95 * len(labels) + 4.0) * fs)
        height = max(9.0, 7.8 * fs)
        fig, ax = plt.subplots(figsize=(width, height))
    else:
        width = max(14.0, 12.5 * fs)
        height = max(7.0, (0.55 * len(labels) + 2.5) * fs)
        fig, ax = plt.subplots(figsize=(width, height))
    fig.patch.set_facecolor("white")

    colors = [
        "#222222" if label == "TOTAL"
        else "#4C78A8" if ratio >= 100
        else "#E45756"
        for label, ratio in zip(labels, ratios)
    ]

    max_ratio = max(ratios)
    min_ratio = min(ratios)
    if args.vertical:
        x = list(range(len(labels)))
        ax.bar(x, ratios, color=colors, width=args.bar_width)
        ax.axhline(100, color="#333333", linewidth=1.2)
        ax.set_xticks(x)
        ax.set_xticklabels(
            [vertical_label(label) for label in labels],
            rotation=45,
            ha="right",
            fontsize=tick_font,
        )
        for tick in ax.get_xticklabels() + ax.get_yticklabels():
            tick.set_fontweight("bold")
        ax.tick_params(axis="y", labelsize=tick_font)
        ax.set_ylabel(
            f"Baseline / LLM mixed {display_name} (%)",
            fontsize=axis_font,
            fontweight="bold",
        )
        ax.set_xlabel("Benchmark", fontsize=axis_font, fontweight="bold")
        ax.set_title(
            f"{display_name} ratio by benchmark",
            fontsize=title_font,
            fontweight="bold",
        )
        ax.grid(axis="y", linestyle="--", alpha=0.25)
        headroom = max(8.0, max_ratio * 0.32)
        ax.set_ylim(min(80, min_ratio - 5), max(135, max_ratio + headroom))
        for xi, ratio in zip(x, ratios):
            ax.text(
                xi,
                ratio + max(1.0, max_ratio * 0.015),
                f"{ratio:.1f}%",
                ha="center",
                va="bottom",
                rotation=90,
                fontsize=value_font,
                fontweight="bold",
            )
    else:
        y = list(range(len(labels)))
        ax.barh(y, ratios, color=colors, height=args.bar_width)
        ax.axvline(100, color="#333333", linewidth=1.2)
        ax.set_yticks(y)
        ax.set_yticklabels(labels, fontsize=tick_font)
        for tick in ax.get_xticklabels() + ax.get_yticklabels():
            tick.set_fontweight("bold")
        ax.tick_params(axis="x", labelsize=tick_font)
        ax.invert_yaxis()
        ax.set_xlabel(
            f"Baseline / LLM mixed {display_name} (%)",
            fontsize=axis_font,
            fontweight="bold",
        )
        ax.set_title(
            f"{display_name} ratio by benchmark",
            fontsize=title_font,
            fontweight="bold",
        )
        ax.grid(axis="x", linestyle="--", alpha=0.25)
        ax.set_xlim(min(80, min_ratio - 5), max(135, max_ratio + 8))
        for yi, ratio in zip(y, ratios):
            ax.text(
                ratio + 1.2,
                yi,
                f"{ratio:.2f}%",
                va="center",
                fontsize=value_font,
                fontweight="bold",
            )

    fig.suptitle(
        f"GPT-7B one-layer: baseline / llm_mixed using {display_name} "
        f"(TOTAL={total_ratio * 100:.2f}%)",
        fontsize=suptitle_font,
        fontweight="bold",
        y=0.997,
    )
    fig.tight_layout(rect=[0, 0, 1, 0.98])

    fig.savefig(out, dpi=args.dpi, bbox_inches="tight")
    if out.suffix.lower() != ".pdf":
        fig.savefig(out.with_suffix(".pdf"), bbox_inches="tight")
    plt.close(fig)


def main():
    args = parse_args()
    if args.split_count <= 0:
        raise ValueError("--split-count must be positive")

    results_dir = Path(args.results_dir).resolve()
    rows = read_rows(results_dir / "llm_decomposed_per_op_summary.csv")
    records = paired_records(rows, args.metric)
    if not records:
        raise RuntimeError("no paired baseline/llm_mixed records found")

    csv_path = output_path(results_dir, args.csv_output)
    write_csv(csv_path, records, args.metric)

    total_baseline = sum(r["baseline"] for r in records)
    total_llm_mixed = sum(r["llm_mixed"] for r in records)
    total_ratio = total_baseline / total_llm_mixed

    labels = [
        f"{r['op_index']:02d} {short_name(r['benchmark'])}"
        for r in records
    ] + ["TOTAL"]
    ratios = [r["ratio"] * 100 for r in records] + [total_ratio * 100]
    display_name = metric_display_name(args.metric)

    out = output_path(results_dir, args.output)
    chunks = split_labels_and_ratios(labels, ratios, args.split_count)
    output_files = []
    for chunk_labels, chunk_ratios, suffix in chunks:
        chunk_out = with_suffix_part(out, suffix)
        draw_plot(args, chunk_labels, chunk_ratios, display_name, total_ratio, chunk_out)
        output_files.append(chunk_out)

    for output_file in output_files:
        print(output_file)
    print(csv_path)
    print(f"baseline_total={total_baseline:.12f}")
    print(f"llm_mixed_total={total_llm_mixed:.12f}")
    print(f"baseline_over_llm_mixed={total_ratio:.12f}")
    print(f"baseline_over_llm_mixed_percent={total_ratio * 100:.6f}")


if __name__ == "__main__":
    main()
