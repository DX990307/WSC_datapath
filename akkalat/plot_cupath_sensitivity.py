#!/usr/bin/env python3
"""Summarize representative typed-filter sensitivity campaigns."""

from __future__ import annotations

import argparse
import csv
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

from plot_cupath_typed_ablation import read_metrics


BENCHMARKS = ("aes", "kmeans", "pagerank", "matrixtranspose")
MAIN = {
    "capacity": ("cap32768", 32768),
    "fingerprint": ("fp13", 13),
    "predictor": ("pred64", 64),
    "lookup_latency": ("lookup1", 1),
}
POINTS = {
    "capacity": (("cap16384", 16384), ("cap32768", 32768), ("cap65536", 65536)),
    "fingerprint": (("fp11", 11), ("fp13", 13), ("fp16", 16)),
    "predictor": (("pred16", 16), ("pred64", 64), ("pred256", 256)),
    "lookup_latency": (("lookup0", 0), ("lookup1", 1), ("lookup2", 2)),
}
TYPE_NAMES = ("resident", "pattern", "pending", "seen")


def gm(values):
    if len(values) != len(BENCHMARKS) or any(value <= 0 for value in values):
        return ""
    return math.exp(sum(math.log(value) for value in values) / len(values))


def load_metric(path):
    metrics, _ = read_metrics(path)
    return metrics


def metric_path(root, benchmark, config):
    paths = sorted(root.glob(f"*_{benchmark}_{config}_metrics.csv"))
    if len(paths) != 1:
        return None
    return paths[0]


def load(args):
    baseline = {}
    main = {}
    for benchmark in BENCHMARKS:
        path = metric_path(args.main_campaign, benchmark, "baseline")
        if path:
            baseline[benchmark] = load_metric(path)
        path = metric_path(args.main_campaign, benchmark, "complete_cupath")
        if path is None:
            path = metric_path(args.main_campaign, benchmark, "complete")
        if path:
            main[benchmark] = load_metric(path)
    rows = []
    for dimension, points in POINTS.items():
        main_name, _ = MAIN[dimension]
        for name, value in points:
            for benchmark in BENCHMARKS:
                metrics = None
                source = ""
                if name == main_name:
                    metrics = main.get(benchmark)
                    source = str(args.main_campaign)
                else:
                    directory = args.sensitivity_root / dimension / name
                    path = metric_path(directory, benchmark, "complete_cupath")
                    if path:
                        metrics = load_metric(path)
                        source = str(path)
                base = baseline.get(benchmark)
                speedup = ""
                if base and metrics:
                    speedup = base["__driver_total_time"] / metrics["__driver_total_time"]
                queries = sum(metrics.get(f"typed_filter_{kind}_queries", 0.0) for kind in TYPE_NAMES) if metrics else ""
                false_positives = sum(metrics.get(f"typed_filter_{kind}_false_positives", 0.0) for kind in TYPE_NAMES) if metrics else ""
                failures = sum(metrics.get(f"typed_filter_{kind}_insert_failures", 0.0) for kind in TYPE_NAMES) if metrics else ""
                fpr = "" if queries in ("", 0) else false_positives / queries
                storage_mib = "" if not metrics else metrics.get("typed_filter_storage_bits", 0.0) / 8.0 / 1024.0 / 1024.0
                rows.append({
                    "dimension": dimension, "point": name, "value": value,
                    "benchmark": benchmark, "speedup": speedup,
                    "filter_queries": queries, "false_positives": false_positives,
                    "measured_fpr": fpr, "insert_failures": failures,
                    "lookup_port_stalls": "" if not metrics else metrics.get("typed_filter_lookup_port_stalls", 0.0),
                    "update_port_stalls": "" if not metrics else metrics.get("typed_filter_update_port_stalls", 0.0),
                    "storage_mib_wafer": storage_mib, "source": source,
                })
    return rows


def write_csv(path, rows):
    fields = tuple(rows[0])
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def write_summary(path, rows):
    with path.open("w", newline="", encoding="utf-8") as stream:
        fields = (
            "dimension", "point", "value", "geomean_speedup_4",
            "total_filter_queries", "total_false_positives", "measured_fpr",
            "total_insert_failures", "lookup_port_stalls", "update_port_stalls",
            "mean_storage_mib_wafer", "approx_target_fpr",
        )
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        for dimension, points in POINTS.items():
            for name, value in points:
                selected = [r for r in rows if r["dimension"] == dimension and r["point"] == name]
                speeds = [r["speedup"] for r in selected if r["speedup"] != ""]
                queries = sum(r["filter_queries"] for r in selected if r["filter_queries"] != "")
                fps = sum(r["false_positives"] for r in selected if r["false_positives"] != "")
                storage = [r["storage_mib_wafer"] for r in selected if r["storage_mib_wafer"] != ""]
                target = ""
                if dimension == "fingerprint":
                    target = 2.0 * 4.0 / (2.0 ** value)
                writer.writerow({
                    "dimension": dimension, "point": name, "value": value,
                    "geomean_speedup_4": gm(speeds),
                    "total_filter_queries": queries,
                    "total_false_positives": fps,
                    "measured_fpr": "" if queries == 0 else fps / queries,
                    "total_insert_failures": sum(r["insert_failures"] for r in selected if r["insert_failures"] != ""),
                    "lookup_port_stalls": sum(r["lookup_port_stalls"] for r in selected if r["lookup_port_stalls"] != ""),
                    "update_port_stalls": sum(r["update_port_stalls"] for r in selected if r["update_port_stalls"] != ""),
                    "mean_storage_mib_wafer": "" if not storage else sum(storage) / len(storage),
                    "approx_target_fpr": target,
                })


def plot(path, rows):
    labels = {
        "capacity": "Filter slots/slice",
        "fingerprint": "Fingerprint bits",
        "predictor": "Predictor entries",
        "lookup_latency": "Lookup latency (cycles)",
    }
    fig, axes_grid = plt.subplots(2, 2, figsize=(3.45, 2.45), sharey=True)
    axes = tuple(axes_grid.ravel())
    for ax, (dimension, points) in zip(axes, POINTS.items()):
        values = []
        names = []
        for name, value in points:
            selected = [r["speedup"] for r in rows if r["dimension"] == dimension and r["point"] == name and r["speedup"] != ""]
            values.append(np.nan if len(selected) != len(BENCHMARKS) else gm(selected))
            names.append(str(value))
        x = np.arange(len(points))
        ax.plot(x, values, color="#31ADCE", linewidth=1.4)
        main_name, _ = MAIN[dimension]
        main_index = next(i for i, (name, _) in enumerate(points) if name == main_name)
        other = [i for i in range(len(points)) if i != main_index]
        ax.scatter(other, [values[i] for i in other], color="#83CEE2",
                   s=14, zorder=3)
        ax.scatter([main_index], [values[main_index]], color="#F4A371",
                   s=17, zorder=4)
        ax.fill_between(x, 1.0, values, color="#D6EFF5", alpha=0.8)
        ax.axhline(1.0, color="#656667", linewidth=0.65)
        ax.set_xticks(range(len(points)))
        ax.set_xticklabels(names)
        ax.set_xlabel(labels[dimension], fontsize=5.8, labelpad=1.5)
        ax.tick_params(axis="both", labelsize=5.4, length=1.8, width=0.55)
        ax.grid(axis="y", color="#E5E5E6", linewidth=0.4)
        for spine in ax.spines.values():
            spine.set_visible(True)
            spine.set_color("#656667")
            spine.set_linewidth(0.6)
    axes[0].set_ylabel("Geomean speedup", fontsize=6.2)
    axes[2].set_ylabel("Geomean speedup", fontsize=6.2)
    fig.tight_layout(pad=0.25, w_pad=0.35, h_pad=0.35)
    fig.savefig(path, dpi=300, bbox_inches="tight", pad_inches=0.025,
                facecolor="white", transparent=False)
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--main-campaign", type=Path, required=True)
    parser.add_argument("--sensitivity-root", type=Path, required=True)
    args = parser.parse_args()
    args.main_campaign = args.main_campaign.resolve()
    args.sensitivity_root = args.sensitivity_root.resolve()
    rows = load(args)
    detail = args.sensitivity_root / "cupath_sensitivity_detail.csv"
    summary = args.sensitivity_root / "cupath_sensitivity_summary.csv"
    figure = args.sensitivity_root / "cupath_sensitivity.png"
    write_csv(detail, rows)
    write_summary(summary, rows)
    plot(figure, rows)
    print(detail)
    print(summary)
    print(figure)


if __name__ == "__main__":
    main()
