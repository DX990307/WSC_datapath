#!/usr/bin/env python3
"""Plot the flat, matched Baseline/Complete CuPath sensitivity campaign."""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
from dataclasses import dataclass
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


REFERENCE = "main-16k-4mb-2-lines"
SERIES_COLORS = ("#83CEE2", "#31ADCE", "#1D687C", "#0A2329")
TOP_LABEL_Y_AXES = 1.012


@dataclass(frozen=True)
class PlotPoint:
    name: str
    label: str
    reference: bool = False


DIMENSIONS = {
    "filter-size": {
        "title": "Cuckoo Filter Capacity",
        "xlabel": "Filter buckets per GPM",
        "output": "cupath_filter_capacity_sensitivity",
        "points": (
            PlotPoint("filter-4k-buckets", "4K"),
            PlotPoint("filter-8k-buckets", "8K"),
            PlotPoint(REFERENCE, "16K", True),
            PlotPoint("filter-32k-buckets", "32K"),
        ),
    },
    "l2-size": {
        "title": "L2 Cache Capacity",
        "xlabel": "L2 capacity per GPM (MiB)",
        "output": "cupath_l2_capacity_sensitivity",
        "points": (
            PlotPoint("l2-1mb", "1"),
            PlotPoint("l2-2mb", "2"),
            PlotPoint(REFERENCE, "4", True),
        ),
    },
    "row-locality": {
        "title": "HBM Row-Local Region",
        "xlabel": "Region size (cachelines)",
        "output": "cupath_row_locality_sensitivity",
        "points": (
            PlotPoint(REFERENCE, "2", True),
            PlotPoint("row-4-lines", "4"),
            PlotPoint("row-8-lines", "8"),
            PlotPoint("row-16-lines", "16"),
        ),
    },
}


def geomean(values: list[float]) -> float:
    return math.exp(sum(math.log(value) for value in values) / len(values))


def successful(path: Path) -> bool:
    if not path.is_file():
        return False
    try:
        return bool(json.loads(path.read_text(encoding="utf-8"))["success"])
    except (KeyError, json.JSONDecodeError, OSError):
        return False


def driver_time(root: Path, benchmark: str, point: str, mode: str) -> float | None:
    stem = f"baseline_{benchmark}_{point}_{mode}"
    if not successful(root / f"{stem}_result.json"):
        return None
    metric_path = root / f"{stem}_metrics.csv"
    if not metric_path.is_file():
        return None
    with metric_path.open(newline="", encoding="utf-8") as handle:
        for row in csv.reader(handle):
            if len(row) < 4:
                continue
            if row[1].strip() == "Driver" and row[2].strip() == "total_time":
                value = float(row[3])
                return value if value > 0 else None
    return None


def load_campaign(root: Path):
    manifest_path = root / "SENSITIVITY_CAMPAIGN.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    benchmarks = tuple(manifest["benchmarks"])
    rows = []
    summaries = {}

    for dimension, spec in DIMENSIONS.items():
        for point in spec["points"]:
            values = []
            for benchmark in benchmarks:
                baseline = driver_time(root, benchmark, point.name, "baseline")
                complete = driver_time(root, benchmark, point.name, "complete")
                speedup = None
                if baseline is not None and complete is not None:
                    speedup = baseline / complete
                    values.append(speedup)
                rows.append(
                    {
                        "dimension": dimension,
                        "point": point.name,
                        "benchmark": benchmark,
                        "baseline_time": "" if baseline is None else baseline,
                        "complete_time": "" if complete is None else complete,
                        "speedup": "" if speedup is None else speedup,
                        "status": "complete" if speedup is not None else "pending",
                    }
                )
            count = len(values)
            summaries[(dimension, point.name)] = {
                "completed": count,
                "total": len(benchmarks),
                "available_geomean": geomean(values) if values else None,
                "paper_geomean": geomean(values) if count == len(benchmarks) else None,
            }
    return benchmarks, rows, summaries


def write_tables(root: Path, rows, summaries) -> None:
    detail_path = root / "cupath_sensitivity_detail.csv"
    with detail_path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)

    summary_path = root / "cupath_sensitivity_summary.csv"
    fields = (
        "dimension",
        "point",
        "completed_benchmarks",
        "total_benchmarks",
        "available_geomean",
        "paper_geomean",
    )
    with summary_path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=fields)
        writer.writeheader()
        for dimension, spec in DIMENSIONS.items():
            for point in spec["points"]:
                item = summaries[(dimension, point.name)]
                writer.writerow(
                    {
                        "dimension": dimension,
                        "point": point.name,
                        "completed_benchmarks": item["completed"],
                        "total_benchmarks": item["total"],
                        "available_geomean": item["available_geomean"],
                        "paper_geomean": item["paper_geomean"],
                    }
                )
    print(detail_path)
    print(summary_path)


def style_axis(ax, show_ylabel: bool) -> None:
    ax.axhline(1.0, color="#333333", linestyle=(0, (4, 2)), linewidth=0.9, zorder=1)
    ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=0)
    ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=0.7)
    if show_ylabel:
        ax.set_ylabel("Geomean speedup", fontsize=7.0)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)


def draw_dimension(ax, dimension: str, summaries, show_ylabel: bool):
    spec = DIMENSIONS[dimension]
    points = spec["points"]
    xs = list(range(len(points)))
    values = [summaries[(dimension, point.name)]["paper_geomean"] for point in points]
    numeric = [float("nan") if value is None else value for value in values]

    colors = SERIES_COLORS[:len(points)]
    bars = ax.bar(
        xs,
        numeric,
        width=0.58,
        color=colors,
        edgecolor=colors,
        linewidth=0.7,
        zorder=2,
    )
    top_labels = []
    for x, bar, point, value, color in zip(xs, bars, points, values, colors):
        item = summaries[(dimension, point.name)]
        if value is None:
            ax.text(
                x,
                0.025,
                f"pending\n{item['completed']}/{item['total']}",
                transform=ax.get_xaxis_transform(),
                ha="center",
                va="bottom",
                fontsize=5.2,
                color="#98999A",
            )
            continue
        top_labels.append(ax.text(
            x,
            TOP_LABEL_Y_AXES,
            f"{value:.2f}x",
            transform=ax.get_xaxis_transform(),
            ha="center",
            va="bottom",
            fontsize=5.6,
            color=color,
            fontweight="bold" if point.reference else "normal",
            clip_on=False,
        ))

    ax.set_xlabel(spec["xlabel"], fontsize=6.4, labelpad=2.0)
    ax.set_xticks(xs)
    ax.set_xticklabels([point.label for point in points])
    ax.set_xlim(-0.45, len(points) - 0.55)
    style_axis(ax, show_ylabel)
    return [value for value in values if value is not None], top_labels


def verify_top_labels(fig, labels) -> float:
    if len(labels) < 2:
        return float("inf")
    if max(label.get_position()[1] for label in labels) - min(
        label.get_position()[1] for label in labels
    ) > 1e-12:
        raise RuntimeError("value labels are not on one coordinate row")
    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    boxes = sorted(
        (label.get_window_extent(renderer=renderer) for label in labels),
        key=lambda box: box.x0,
    )
    minimum_gap = min(
        right.x0 - left.x1 for left, right in zip(boxes, boxes[1:])
    )
    if minimum_gap < 0:
        raise RuntimeError(
            f"top value labels overlap by {-minimum_gap:.2f} pixels"
        )
    return minimum_gap


def set_shared_limits(axes, values: list[float]) -> None:
    high = max([1.0] + values)
    upper = math.ceil((high + 0.08) * 10) / 10
    for ax in axes:
        ax.set_ylim(0.0, upper)


def save_figure(fig, base: Path, dpi: int) -> None:
    fig.savefig(base.with_suffix(".png"), dpi=dpi, bbox_inches="tight", pad_inches=0.025)
    fig.savefig(base.with_suffix(".pdf"), bbox_inches="tight", pad_inches=0.025)
    print(base.with_suffix(".png"))
    print(base.with_suffix(".pdf"))


def plot(root: Path, summaries, dpi: int) -> None:
    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.8,
        }
    )

    all_values = []
    figures = []
    for dimension, spec in DIMENSIONS.items():
        fig, ax = plt.subplots(figsize=(3.45, 1.58))
        values, top_labels = draw_dimension(ax, dimension, summaries, True)
        all_values.extend(values)
        figures.append((fig, ax, top_labels, root / spec["output"]))

    for fig, ax, top_labels, base in figures:
        set_shared_limits([ax], all_values)
        fig.tight_layout(pad=0.25)
        verify_top_labels(fig, top_labels)
        save_figure(fig, base, dpi)
        plt.close(fig)

    fig, axes = plt.subplots(1, 3, figsize=(7.16, 1.72), sharey=True)
    combined_values = []
    combined_labels = []
    for index, (ax, dimension) in enumerate(zip(axes, DIMENSIONS)):
        values, top_labels = draw_dimension(
            ax, dimension, summaries, index == 0
        )
        combined_values.extend(values)
        combined_labels.append(top_labels)
    set_shared_limits(axes, combined_values)
    fig.tight_layout(pad=0.3, w_pad=0.55)
    for top_labels in combined_labels:
        verify_top_labels(fig, top_labels)
    save_figure(fig, root / "cupath_sensitivity_three_panel", dpi)
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("campaign", type=Path)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()
    root = args.campaign.resolve()
    _, rows, summaries = load_campaign(root)
    write_tables(root, rows, summaries)
    plot(root, summaries, args.dpi)


if __name__ == "__main__":
    main()
