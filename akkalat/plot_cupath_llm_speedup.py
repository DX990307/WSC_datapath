#!/usr/bin/env python3
"""Plot weighted BERT/GPT speedup from a completed CuPath LLM campaign."""

from __future__ import annotations

import argparse
import csv
import os
import tempfile
from pathlib import Path

os.environ.setdefault(
    "MPLCONFIGDIR",
    str(Path(tempfile.gettempdir()) / "matplotlib-cache"),
)

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch


MODELS = (
    ("bert", "BERT-7B"),
    ("gpt", "GPT-7B"),
    ("resnet", "ResNet-50"),
)
BASELINE_COLOR = "#83CEE2"
CUPATH_COLOR = "#F18541"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "result_dir",
        type=Path,
        help="Directory containing cupath_llm_summary.csv.",
    )
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def load_rows(path: Path) -> dict[tuple[str, str], dict[str, str]]:
    rows = {}
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream):
            if row["model"] not in {model for model, _ in MODELS}:
                continue
            if row["config"] not in {"baseline", "complete"}:
                continue
            if int(row["return_code_0_representatives"]) != int(
                row["representative_ops"]
            ):
                raise ValueError(
                    f"incomplete summary row: {row['model']} {row['config']}"
                )
            rows[(row["model"], row["config"])] = row
    return rows


def build_records(rows: dict[tuple[str, str], dict[str, str]]) -> list[dict]:
    records = []
    for model, label in MODELS:
        baseline = rows.get((model, "baseline"))
        complete = rows.get((model, "complete"))
        if baseline is None or complete is None:
            raise ValueError(f"missing baseline/complete pair for {model}")
        baseline_time = float(baseline["driver_total_time_weighted_sum"])
        complete_time = float(complete["driver_total_time_weighted_sum"])
        if baseline_time <= 0 or complete_time <= 0:
            raise ValueError(f"non-positive Driver,total_time for {model}")
        records.append(
            {
                "model": model,
                "label": label,
                "baseline_time_s": baseline_time,
                "cupath_time_s": complete_time,
                "baseline_speedup": 1.0,
                "cupath_speedup": baseline_time / complete_time,
            }
        )
    return records


def write_source_csv(path: Path, records: list[dict]) -> None:
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            [
                "model",
                "baseline_weighted_driver_time_s",
                "cupath_weighted_driver_time_s",
                "baseline_speedup",
                "cupath_speedup",
            ]
        )
        for record in records:
            writer.writerow(
                [
                    record["label"],
                    f"{record['baseline_time_s']:.12f}",
                    f"{record['cupath_time_s']:.12f}",
                    "1.000000000000",
                    f"{record['cupath_speedup']:.12f}",
                ]
            )


def draw(result_dir: Path, records: list[dict], dpi: int) -> Path:
    labels = [record["label"] for record in records]
    baseline = [1.0] * len(labels)
    cupath = [record["cupath_speedup"] for record in records]
    xs = list(range(len(labels)))
    width = 0.27

    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.75,
        }
    )
    fig, ax = plt.subplots(figsize=(3.45, 1.58))

    baseline_bars = ax.bar(
        [x - width / 2 for x in xs],
        baseline,
        width=width,
        color=BASELINE_COLOR,
        edgecolor="none",
        zorder=3,
    )
    cupath_bars = ax.bar(
        [x + width / 2 for x in xs],
        cupath,
        width=width,
        color=CUPATH_COLOR,
        edgecolor="none",
        zorder=3,
    )

    for bar, value in zip(baseline_bars, baseline):
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            value + 0.12,
            f"{value:.2f}x",
            ha="center",
            va="bottom",
            fontsize=5.7,
        )
    for bar, value in zip(cupath_bars, cupath):
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            value + 0.12,
            f"{value:.2f}x",
            ha="center",
            va="bottom",
            fontsize=5.7,
            fontweight="bold",
            color="#BE520E",
        )

    ax.axhline(
        1.0,
        color="#333333",
        linestyle=(0, (4, 2)),
        linewidth=0.8,
        zorder=2,
    )
    ax.set_ylim(0, 7.2)
    ax.set_yticks([0, 1, 2, 4, 6])
    ax.set_ylabel("Speedup", fontsize=7.0)
    ax.set_xticks(xs)
    ax.set_xticklabels(labels, fontsize=6.2)
    ax.tick_params(axis="y", labelsize=6.0, length=2.3, width=0.7)
    ax.tick_params(axis="x", length=0)
    ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=1)
    ax.set_xlim(-0.48, len(labels) - 0.52)

    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)

    ax.legend(
        handles=[
            Patch(facecolor=BASELINE_COLOR, edgecolor="none", label="Baseline"),
            Patch(facecolor=CUPATH_COLOR, edgecolor="none", label="CuPath"),
        ],
        loc="lower left",
        bbox_to_anchor=(0.02, 1.04, 0.96, 0.16),
        ncol=2,
        mode="expand",
        frameon=False,
        handlelength=1.0,
        handletextpad=0.45,
        fontsize=5.9,
    )

    fig.tight_layout(pad=0.28)
    output = result_dir / "cupath_llm_performance_improvement.png"
    fig.savefig(output, dpi=dpi, bbox_inches="tight")
    plt.close(fig)
    return output


def main() -> None:
    args = parse_args()
    result_dir = args.result_dir.resolve()
    rows = load_rows(result_dir / "cupath_llm_summary.csv")
    records = build_records(rows)
    source = result_dir / "cupath_llm_performance_improvement.csv"
    write_source_csv(source, records)
    output = draw(result_dir, records, args.dpi)
    print(source)
    print(output)
    for record in records:
        print(f"{record['label']}: {record['cupath_speedup']:.6f}x")


if __name__ == "__main__":
    main()
