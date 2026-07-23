#!/usr/bin/env python3
"""Plot auditable stage-local work reduction from a CuPath V5 campaign."""

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
from matplotlib.colors import LinearSegmentedColormap

from plot_cupath_typed_ablation import WORKLOADS


ORANGE = LinearSegmentedColormap.from_list(
    "cupath_orange",
    ("#FDF0E7", "#FBE0D0", "#F8C2A0", "#F4A371", "#F18541", "#ED6612"),
)


def number(row: dict[str, str], field: str) -> float | None:
    value = row.get(field, "")
    if value in ("", None):
        return None
    return float(value)


def percent(numerator: float | None, denominator: float | None) -> float | None:
    if numerator is None or denominator is None or denominator <= 0:
        return None
    return 100.0 * numerator / denominator


def read_by_benchmark(path: Path) -> dict[str, dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as stream:
        return {row["benchmark"]: row for row in csv.DictReader(stream)}


def reductions(work: dict[str, str]):
    skipped = number(work, "l2_tag_lookups_skipped")
    # Use the L2 request-side query counter.  The aggregate typed-filter
    # counter also includes non-L2 users of the shared physical structure and
    # therefore is not the correct denominator for skipped L2 tag work.
    resident_queries = number(work, "l2_resident_filter_queries")
    # Duplicate and requester-L2 counters describe transformations within the
    # Complete request stream.  Using that same logical stream as denominator
    # keeps each stage auditable and avoids double-counting a coalesced L2 hit.
    logical_remote = number(work, "complete_remote_logical_reads")
    duplicates = number(work, "remote_duplicate_reads_eliminated")
    wire_lines = number(work, "complete_remote_wire_lines")
    packets = number(work, "complete_remote_packets")
    traversals = number(work, "repeated_wafer_traversals_eliminated")
    packets_removed = None
    if wire_lines is not None and packets is not None:
        packets_removed = max(0.0, wire_lines - packets)
    remote_dedup_pct = percent(duplicates, logical_remote)
    traversal_pct = percent(traversals, logical_remote)
    packet_pct = percent(packets_removed, wire_lines)
    # A completed workload with no traffic at a remote stage has zero work
    # removed there; it is not a missing experiment cell.
    if logical_remote == 0:
        remote_dedup_pct = 0.0
        traversal_pct = 0.0
    if wire_lines == 0:
        packet_pct = 0.0
    values = (
        percent(skipped, resident_queries),
        remote_dedup_pct,
        packet_pct,
        traversal_pct,
    )
    for name, value in zip(
        ("L2 tag", "remote dedup", "packet", "traversal"), values
    ):
        if value is not None and not -1e-9 <= value <= 100.0 + 1e-9:
            raise ValueError(f"{name} reduction is outside [0, 100]: {value}")
    counts = (
        (skipped, resident_queries),
        (duplicates, logical_remote),
        (packets_removed, wire_lines),
        (traversals, logical_remote),
    )
    return values, counts


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--work-csv", type=Path, required=True)
    parser.add_argument("--filter-csv", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    work = read_by_benchmark(args.work_csv)
    # Retain --filter-csv for compatibility with the other CuPath plotting
    # commands.  Work-reduction denominators now all come from the work table.
    read_by_benchmark(args.filter_csv)

    labels = []
    rows = []
    aggregate = [[0.0, 0.0] for _ in range(4)]
    complete_count = 0
    for benchmark, label, _ in WORKLOADS:
        values, counts = reductions(work.get(benchmark, {}))
        labels.append(label)
        rows.append(values)
        if all(value is not None for value in values):
            complete_count += 1
        for i, (num, den) in enumerate(counts):
            if num is not None and den is not None:
                aggregate[i][0] += num
                aggregate[i][1] += den
    weighted = tuple(percent(num, den) for num, den in aggregate)
    labels.append("Weighted" if complete_count == len(WORKLOADS) else "Weighted*")
    rows.append(weighted)

    out_csv = args.output_dir / "cupath_work_reduction_summary.csv"
    fields = (
        "benchmark", "l2_tag_work_removed_pct",
        "remote_reads_deduplicated_pct", "wire_packets_removed_pct",
        "repeated_traversals_removed_pct",
    )
    with out_csv.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(fields)
        for label, values in zip(labels, rows):
            writer.writerow([label, *("" if value is None else value for value in values)])

    data = np.array([
        [np.nan if value is None else value for value in row] for row in rows
    ])
    fig, ax = plt.subplots(figsize=(3.45, 2.15))
    masked = np.ma.masked_invalid(data)
    ax.imshow(masked, cmap=ORANGE, vmin=0, vmax=100, aspect="auto")
    ax.set_facecolor("white")
    ax.set_xticks(
        range(4),
        ("L2 tag\nwork", "Remote\nreads", "Wire\npackets", "Wafer\ntraversals"),
        fontsize=5.1,
    )
    ax.set_yticks(range(len(labels)), labels, fontsize=4.8)
    ax.tick_params(length=0)
    ax.axhline(len(labels) - 1.5, color="#4C4C4D", linewidth=0.8)
    for i, row in enumerate(rows):
        for j, value in enumerate(row):
            text = "--" if value is None or not math.isfinite(value) else f"{value:.0f}"
            color = "white" if value is not None and value >= 58 else "#323334"
            ax.text(j, i, text, ha="center", va="center", fontsize=4.3,
                    color=color, fontweight="bold" if i == len(rows) - 1 else "normal")
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.65)
    fig.tight_layout(pad=0.25)
    figure = args.output_dir / "cupath_work_reduction.png"
    fig.savefig(figure, dpi=300, bbox_inches="tight", pad_inches=0.025,
                facecolor="white", transparent=False)
    plt.close(fig)
    print(f"completed workloads: {complete_count}/{len(WORKLOADS)}")
    print(out_csv)
    print(figure)


if __name__ == "__main__":
    main()
