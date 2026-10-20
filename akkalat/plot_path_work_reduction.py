#!/usr/bin/env python3
"""Plot stage-local work elimination over the CuPath request lifecycle."""

import csv
from collections import defaultdict
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np
from matplotlib.colors import LinearSegmentedColormap


ROOT = Path(__file__).resolve().parent
SOURCE = ROOT / "results" / "2026-07-15-current-ablation-provisional-plot" / \
    "current_combined_sources.csv"
OUT = ROOT / "results" / "2026-07-15-current-ablation-provisional-plot"

ORANGE_HEATMAP = LinearSegmentedColormap.from_list(
    "cupath_orange",
    [
        "#FDF0E7", "#FBE0D0", "#F8C2A0", "#F4A371",
        "#F18541", "#ED6612", "#BE520E", "#8E3D0B",
    ],
)


def read_metrics(path):
    totals = defaultdict(float)
    with Path(path).open(newline="") as f:
        for row in csv.DictReader(f, skipinitialspace=True):
            totals[row["what"].strip()] += float(row["value"])
    return totals


def ratio(num, den):
    return 100.0 * num / den if den else 0.0


def reductions(m):
    dram_avoided = m["dram_adapter_buffer_hits"] + m["dram_adapter_inflight_hits"]
    request_packets = m["remote_bitmap_packets"] + m["remote_single_packets"]
    return (
        ratio(m["remote_replica_probe_hits"],
              m["remote_logical_reads"] + m["remote_replica_probe_hits"]),
        ratio(m["remote_duplicate_reads"], m["remote_logical_reads"]),
        ratio(m["remote_wire_lines"] - request_packets, m["remote_wire_lines"]),
        ratio(dram_avoided, m["dram_batch_miss_lines"]),
    )


def main():
    rows = []
    aggregate = defaultdict(float)
    with SOURCE.open(newline="") as f:
        for source in csv.DictReader(f):
            metrics = read_metrics(source["combined_metrics"])
            rows.append((source["benchmark"], *reductions(metrics)))
            for key, value in metrics.items():
                aggregate[key] += value

    weighted = ("Weighted", *reductions(aggregate))
    rows.append(weighted)

    csv_path = OUT / "path_work_reduction.csv"
    with csv_path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(("benchmark", "remote_paths_terminated_pct",
                         "exact_remote_merged_pct", "wire_packets_reduced_pct",
                         "dram_reads_avoided_pct"))
        writer.writerows(rows)

    labels = [r[0] for r in rows]
    data = np.asarray([r[1:] for r in rows])
    fig, ax = plt.subplots(figsize=(3.45, 1.85))
    image = ax.imshow(data, cmap=ORANGE_HEATMAP, vmin=0, vmax=100,
                      aspect="auto")
    ax.set_xticks(range(4), ["Remote\npaths", "Exact remote\nrequests",
                             "Wire\npackets", "DRAM\nrequests"], fontsize=5.3)
    ax.set_yticks(range(len(labels)), labels, fontsize=4.8)
    ax.tick_params(length=0)
    ax.axhline(len(labels) - 1.5, color="#323334", linewidth=0.9)
    for i in range(data.shape[0]):
        for j in range(data.shape[1]):
            value = data[i, j]
            color = "white" if value >= 55 else "#323334"
            ax.text(j, i, f"{value:.0f}", ha="center", va="center",
                    fontsize=4.4, color=color,
                    fontweight="bold" if i == len(labels) - 1 else "normal")
    ax.set_title("Work eliminated as visibility evolves (%)",
                 fontsize=7.0, fontweight="bold", pad=3)
    fig.tight_layout(pad=0.2)
    for suffix in ("pdf", "png"):
        fig.savefig(OUT / f"path_work_reduction.{suffix}", dpi=300,
                    bbox_inches="tight", pad_inches=0.02)
    plt.close(fig)

    print(", ".join(f"{name}={value:.1f}%" for name, value in
                    zip(("termination", "dedup", "packets", "DRAM"), weighted[1:])))


if __name__ == "__main__":
    main()
