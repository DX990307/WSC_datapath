#!/usr/bin/env python3
"""Create compact O2/O3 figures for the real-demand/Filter opportunity."""

from __future__ import annotations

import argparse
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

from plot_cupath_typed_ablation import read_metrics


LABELS = {
    "aes": "AES", "fastwalshtransform": "FWT", "fft": "FFT",
    "kmeans": "KM", "pagerank": "PR", "matrixtranspose": "MT",
    "spmv": "SPMV",
}
BLUE = "#83CEE2"
BLUE_DARK = "#31ADCE"
ORANGE = "#F8C2A0"
ORANGE_DARK = "#F4A371"
GRAY = "#656667"


def metric(root: Path, benchmark: str, config: str) -> dict[str, float]:
    paths = sorted(root.glob(f"*_{benchmark}_{config}_metrics.csv"))
    if len(paths) != 1:
        raise RuntimeError(f"missing {benchmark}/{config} in {root}")
    values, _ = read_metrics(paths[0])
    return values


def finish(ax):
    ax.grid(axis="y", color="#E5E5E6", linewidth=0.45, zorder=0)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color(GRAY)
        spine.set_linewidth(0.65)
    ax.tick_params(axis="both", labelsize=5.4, length=2, width=0.6)
    ax.legend(frameon=False, fontsize=5.7, ncol=2, loc="upper center")


def plot_o2(path: Path, names, predictor):
    candidate = []
    evidence = []
    for name in names:
        values = predictor[name]
        demands = values.get("filter_prefetch_predictor_real_demands", 0.0)
        candidate.append(100 * values.get("filter_prefetch_predictor_candidates", 0.0) / demands if demands else 0)
        evidence.append(100 * values.get("filter_prefetch_predictor_evidence_two", 0.0) / demands if demands else 0)
    x = np.arange(len(names))
    width = 0.36
    fig, ax = plt.subplots(figsize=(3.45, 1.55))
    ax.bar(x-width/2, evidence, width, color=ORANGE, edgecolor=ORANGE_DARK,
           linewidth=0.55, label="Repeated-stride evidence", zorder=2)
    ax.bar(x+width/2, candidate, width, color=BLUE, edgecolor=BLUE_DARK,
           linewidth=0.55, label="Candidates / demand", zorder=2)
    ax.set_ylabel("Real demands (%)", fontsize=6.3)
    ax.set_xticks(x, [LABELS[name] for name in names])
    ax.set_ylim(bottom=0)
    finish(ax)
    fig.tight_layout(pad=0.3)
    fig.savefig(path, dpi=300, bbox_inches="tight", pad_inches=0.025,
                facecolor="white", transparent=False)
    plt.close(fig)


def plot_o3(path: Path, names, ungated, coupled):
    removed = []
    retained = []
    for name in names:
        u, c = ungated[name], coupled[name]
        ui = u.get("filter_prefetch_issued", 0.0)
        uu = u.get("filter_prefetch_useful", 0.0)
        removed.append(100 * (1-c.get("filter_prefetch_issued", 0.0)/ui) if ui else 0)
        retained.append(100 * c.get("filter_prefetch_useful", 0.0)/uu if uu else 0)
    x = np.arange(len(names))
    width = 0.36
    fig, ax = plt.subplots(figsize=(3.45, 1.55))
    ax.bar(x-width/2, removed, width, color=ORANGE, edgecolor=ORANGE_DARK,
           linewidth=0.55, label="Issued work removed", zorder=2)
    ax.bar(x+width/2, retained, width, color=BLUE, edgecolor=BLUE_DARK,
           linewidth=0.55, label="Useful work retained", zorder=2)
    ax.axhline(100, color=GRAY, linewidth=0.55, linestyle="--")
    ax.set_ylabel("Ungated reference (%)", fontsize=6.3)
    ax.set_xticks(x, [LABELS[name] for name in names])
    ax.set_ylim(0, max(110, max(retained, default=0)*1.08))
    finish(ax)
    fig.tight_layout(pad=0.3)
    fig.savefig(path, dpi=300, bbox_inches="tight", pad_inches=0.025,
                facecolor="white", transparent=False)
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("campaign", type=Path)
    parser.add_argument("--benchmarks", required=True)
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    names = tuple(x.strip() for x in args.benchmarks.split(",") if x.strip())
    output = args.output_dir or args.campaign / "figures"
    output.mkdir(parents=True, exist_ok=True)
    predictor = {name: metric(args.campaign, name, "predictor_only") for name in names}
    ungated = {name: metric(args.campaign, name, "ungated_prefetch") for name in names}
    coupled = {name: metric(args.campaign, name, "filter_coupled_prefetch") for name in names}
    o2 = output / "o2_predictive_coverage.png"
    o3 = output / "o3_filter_candidate_quality.png"
    plot_o2(o2, names, predictor)
    plot_o3(o3, names, ungated, coupled)
    print(o2)
    print(o3)


if __name__ == "__main__":
    main()
