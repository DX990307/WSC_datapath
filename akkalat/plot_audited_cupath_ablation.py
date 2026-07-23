#!/usr/bin/env python3
"""Plot the audited Baseline/M1/M2/M3/Complete CuPath campaign."""

from __future__ import annotations

import argparse
import csv
import json
import math
import sys
from pathlib import Path

import os

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np


WORKLOADS = (
    ("aes", "AES"),
    ("bitonicsort", "BT"),
    ("fastwalshtransform", "FWT"),
    ("fft", "FFT"),
    ("fir", "FIR"),
    ("relu", "RELU"),
    ("simpleconvolution", "SC"),
    ("floydwarshall", "FWS"),
    ("kmeans", "KM"),
    ("matrixmultiplication", "MM"),
    ("pagerank", "PR"),
    ("im2col", "I2C"),
    ("matrixtranspose", "MT"),
    ("spmv", "SPMV"),
)
CONFIGS = (
    ("baseline", "Baseline", "#D6EFF5"),
    ("m1", "M1", "#ADDEEB"),
    ("m2", "M2", "#83CEE2"),
    ("m3", "M3", "#FBE0D0"),
    ("complete", "Complete", "#F4A371"),
)


def read_binary_digest(root: Path) -> str:
    data = json.loads((root / "EXPERIMENT_BINARIES.json").read_text())
    digest = data.get("sha256_by_target", {}).get("baseline", "")
    if len(digest) != 64:
        raise ValueError(f"invalid frozen-binary digest in {root}")
    return digest


def read_completion_audit(path: Path, expected_configs: set[str]):
    rows = list(csv.DictReader(path.open(newline="", encoding="utf-8")))
    expected = {(benchmark, config) for benchmark, _ in WORKLOADS
                for config in expected_configs}
    observed = {(row["benchmark"], row["configuration"]) for row in rows}
    if observed != expected:
        raise ValueError(
            f"completion grid mismatch in {path}: "
            f"missing={sorted(expected-observed)}, extra={sorted(observed-expected)}"
        )
    result = {}
    for row in rows:
        if row["launch_limited"] != "False" or row["kernel_drained"] != "False":
            raise ValueError(f"non-native WG handling in {path}: {row}")
        completed = int(row["completed_wg_count"])
        reached = row["max_wg_reached"] == "1"
        if reached and (
            completed != int(row["max_wg_limit"])
            or row["completion_message_verified"] != "True"
        ):
            raise ValueError(f"invalid completion stop in {path}: {row}")
        result[(row["benchmark"], row["configuration"])] = completed
    return result


def verify_equal_work(baseline_root: Path, ablation_root: Path):
    baseline = read_completion_audit(
        baseline_root / "cupath_wg_completion_audit.csv",
        {"baseline", "complete"},
    )
    ablation = read_completion_audit(
        ablation_root / "cupath_m1_m2_m3_completion_audit.csv",
        {"m1", "m2", "m3"},
    )
    for benchmark, _ in WORKLOADS:
        counts = {
            config: (baseline if config in {"baseline", "complete"} else ablation)[
                (benchmark, config)
            ]
            for config, _, _ in CONFIGS
        }
        if len(set(counts.values())) != 1:
            raise ValueError(f"completed-WG mismatch for {benchmark}: {counts}")
    return baseline | ablation


def load_campaign(repo: Path, baseline_root: Path, ablation_root: Path):
    sys.path.insert(0, str(repo / "akkalat"))
    import plot_cupath_typed_ablation as typed

    campaign, paths = typed.load_campaign_roots([baseline_root, ablation_root])
    expected = len(WORKLOADS) * len(CONFIGS)
    if len(campaign) != expected:
        raise ValueError(f"expected {expected} result cells, found {len(campaign)}")
    return typed, campaign, paths, typed.speedups(campaign)


def geomean(values):
    return math.exp(sum(math.log(value) for value in values) / len(values))


def write_table(path: Path, campaign, speedups, completed_wgs):
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            ["benchmark", "label", "completed_wgs"]
            + [f"{config}_driver_time_s" for config, _, _ in CONFIGS]
            + [f"{config}_speedup" for config, _, _ in CONFIGS]
        )
        for benchmark, label in WORKLOADS:
            writer.writerow(
                [benchmark, label, completed_wgs[(benchmark, "baseline")]]
                + [campaign[(benchmark, config)]["__driver_total_time"]
                   for config, _, _ in CONFIGS]
                + [speedups[(benchmark, config)] for config, _, _ in CONFIGS]
            )
        writer.writerow(
            ["geomean_14", "GM", ""]
            + [""] * len(CONFIGS)
            + [geomean([speedups[(benchmark, config)]
                        for benchmark, _ in WORKLOADS])
               for config, _, _ in CONFIGS]
        )


def plot(path_base: Path, speedups, y_min: float, y_limit: float,
         annotate_geomean: bool = False):
    labels = [label for _, label in WORKLOADS] + ["GM"]
    x = np.arange(len(labels), dtype=float)
    width = 0.13
    fig, ax = plt.subplots(figsize=(3.45, 1.82))
    gm_index = len(WORKLOADS)
    ax.axvspan(gm_index - 0.45, gm_index + 0.45, color="#F2F2F2", zorder=0)

    for index, (config, label, color) in enumerate(CONFIGS):
        values = [speedups[(benchmark, config)] for benchmark, _ in WORKLOADS]
        values.append(geomean(values))
        positions = x + (index - 2) * width
        bars = ax.bar(
            positions,
            np.minimum(values, y_limit),
            width=width * 0.92,
            label=label,
            color=color,
            edgecolor="none",
            linewidth=0,
            zorder=3,
        )
        for value_index, (bar, value) in enumerate(zip(bars, values)):
            clipped = value > y_limit
            if clipped or (annotate_geomean and value_index == gm_index):
                ax.text(
                    bar.get_x() + bar.get_width() / 2,
                    (y_limit if clipped else value) +
                    (0.012 if y_min > 0 else 0.035),
                    f"{value:.2f}",
                    ha="center",
                    va="bottom",
                    fontsize=4.2,
                    color="#8E3D0B" if clipped else "#323334",
                    fontweight="bold" if clipped else "normal",
                    clip_on=False,
                )

    ax.axhline(1.0, color="#4C4C4D", linewidth=0.65, zorder=2)
    for boundary in (6.5, 10.5, 13.5):
        ax.axvline(boundary, color="#98999A", linewidth=0.45,
                   linestyle=":", zorder=1)
    ax.set_ylabel("Speedup over Baseline", fontsize=6.2)
    ax.set_xticks(x)
    ax.set_xticklabels(
        labels, rotation=32, ha="right", rotation_mode="anchor", fontsize=4.8
    )
    if y_min > 0:
        ax.set_yticks([1.0, 1.1, 1.2, 1.3, 1.4, 1.5, 1.6])
    else:
        ax.set_yticks([0, 0.5, 1.0, 1.5, 2.0, 2.5])
    ax.tick_params(axis="y", labelsize=5.0, length=1.8, width=0.55)
    ax.tick_params(axis="x", length=1.6, width=0.55, pad=1)
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.4, zorder=0)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.55)
    ax.legend(
        ncol=5,
        frameon=False,
        fontsize=4.6,
        loc="lower center",
        bbox_to_anchor=(0.5, 1.11),
        columnspacing=0.48,
        handlelength=0.82,
        handletextpad=0.23,
        borderaxespad=0,
    )
    ax.set_ylim(y_min, y_limit)
    ax.set_xlim(-0.48, len(labels) - 0.52)
    fig.tight_layout(pad=0.22)
    fig.savefig(path_base.with_suffix(".png"), dpi=300, bbox_inches="tight",
                pad_inches=0.025, facecolor="white", transparent=False)
    fig.savefig(path_base.with_suffix(".pdf"), bbox_inches="tight",
                pad_inches=0.025, facecolor="white", transparent=False)
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("baseline_results", type=Path)
    parser.add_argument("ablation_results", type=Path)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()

    repo = Path(__file__).resolve().parent.parent
    baseline_root = args.baseline_results.resolve()
    ablation_root = args.ablation_results.resolve()
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)

    baseline_digest = read_binary_digest(baseline_root)
    ablation_digest = read_binary_digest(ablation_root)
    if baseline_digest != ablation_digest:
        raise ValueError(
            f"frozen-binary mismatch: {baseline_digest} != {ablation_digest}"
        )
    completed_wgs = verify_equal_work(baseline_root, ablation_root)
    _, campaign, _, speedup = load_campaign(
        repo, baseline_root, ablation_root
    )

    table_base = output / "cupath_audited_ablation"
    write_table(table_base.with_suffix(".csv"), campaign, speedup,
                completed_wgs)
    full_base = output / "cupath_audited_ablation_full_scale"
    zoom_base = output / "cupath_audited_ablation_zoomed"
    plot(full_base, speedup, 0.0, 2.65)
    plot(zoom_base, speedup, 0.95, 1.68, annotate_geomean=True)
    summary = {
        config: geomean([speedup[(benchmark, config)]
                         for benchmark, _ in WORKLOADS])
        for config, _, _ in CONFIGS
    }
    (table_base.with_name(table_base.name + "_summary.json")).write_text(
        json.dumps({
            "frozen_binary_sha256": baseline_digest,
            "cells": len(campaign),
            "geomean_speedup": summary,
            "includes_remote_batch_filter_state": False,
        }, indent=2) + "\n",
        encoding="utf-8",
    )
    print(full_base.with_suffix(".png"))
    print(full_base.with_suffix(".pdf"))
    print(zoom_base.with_suffix(".png"))
    print(zoom_base.with_suffix(".pdf"))
    print(table_base.with_suffix(".csv"))


if __name__ == "__main__":
    main()
