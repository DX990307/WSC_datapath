#!/usr/bin/env python3
"""Plot requester-GPU by shared-page heatmaps from sharing traces."""

import argparse
import csv
import gzip
import math
import os
import re
from collections import Counter, defaultdict
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt
import numpy as np


FONT_SIZE = 14
LLM_OP_PATTERN = re.compile(
    r"^baseline_llmop_(?P<model>[^_]+)_(?P<profile>[^_]+)_"
    r"(?P<index>\d{3})_(?P<label>.+)_(?P<config>llm_mixed|baseline)_"
    r"sharing\.csv\.gz$"
)

LLM_DISPLAY_NAMES = {
    "embedding": "Embedding",
    "layer00_norm1": "Norm1",
    "layer00_attn_q": "Attn Q",
    "layer00_attn_k": "Attn K",
    "layer00_attn_v": "Attn V",
    "layer00_attn_score": "Attn Score",
    "layer00_causal_mask": "Causal Mask",
    "layer00_attn_softmax": "Attn Softmax",
    "layer00_attn_value": "Attn Value",
    "layer00_attn_out": "Attn Out",
    "layer00_attn_residual": "Attn Residual",
    "layer00_norm2": "Norm2",
    "layer00_mlp_fc1": "MLP FC1",
    "layer00_mlp_gelu": "MLP GELU",
    "layer00_mlp_fc2": "MLP FC2",
    "layer00_mlp_residual": "MLP Residual",
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing *_sharing.csv.gz and *_sharing_top_shared_pages.csv.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Output directory. Defaults to results_dir/figures/shared_page_heatmaps.",
    )
    parser.add_argument(
        "--top-pages",
        type=int,
        default=16,
        help="Number of top shared pages to show per heatmap.",
    )
    parser.add_argument(
        "--only",
        default=None,
        help="Only plot files whose parsed name contains this string.",
    )
    parser.add_argument(
        "--log2-page-size",
        type=int,
        default=12,
        help="Used to derive page_id from vaddr when page_id is absent.",
    )
    return parser.parse_args()


def parsed_name(path):
    match = LLM_OP_PATTERN.match(path.name)
    if match:
        index = int(match.group("index"))
        label = match.group("label")
        display = LLM_DISPLAY_NAMES.get(label, label)
        return f"llm_{index:03d}_{label}", f"{index:02d} {display}", index

    stem = path.name.replace("_sharing.csv.gz", "")
    return stem, stem, 10_000


def top_shared_path(trace_path):
    return Path(str(trace_path).replace("_sharing.csv.gz", "_sharing_top_shared_pages.csv"))


def read_top_pages(trace_path, top_pages):
    path = top_shared_path(trace_path)
    if not path.exists():
        return []

    pages = []
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            pages.append(
                {
                    "pid": int(row.get("pid", 0)),
                    "page_id": int(row["page_id"]),
                    "accesses": int(float(row["accesses"])),
                    "sharer_count": int(float(row["sharer_count"])),
                }
            )
            if len(pages) >= top_pages:
                break
    return pages


def read_trace_counts(trace_path, selected_pages, log2_page_size):
    selected = {(page["pid"], page["page_id"]) for page in selected_pages}
    counts = defaultdict(Counter)
    owner_counts = defaultdict(Counter)

    with gzip.open(trace_path, "rt", newline="") as f:
        reader = csv.DictReader(f)
        has_pid = "pid" in (reader.fieldnames or [])
        has_page_id = "page_id" in (reader.fieldnames or [])
        for row in reader:
            pid = int(row["pid"]) if has_pid else 0
            if has_page_id:
                page_id = int(row["page_id"])
            else:
                page_id = int(row["vaddr"]) >> log2_page_size
            key = (pid, page_id)
            if key not in selected:
                continue
            requester = int(row["requester"])
            owner = int(row["owner"])
            counts[key][requester] += 1
            owner_counts[key][owner] += 1

    return counts, owner_counts


def make_matrix(selected_pages, counts):
    matrix = np.zeros((48, len(selected_pages)), dtype=float)
    for col, page in enumerate(selected_pages):
        key = (page["pid"], page["page_id"])
        for requester, accesses in counts[key].items():
            if 1 <= requester <= 48:
                matrix[requester - 1, col] = accesses
    return matrix


def page_owner_label(page, owner_counts):
    key = (page["pid"], page["page_id"])
    if not owner_counts[key]:
        return "?"
    return str(owner_counts[key].most_common(1)[0][0])


def save_heatmap(trace_path, out_dir, top_pages, log2_page_size):
    stem, title_name, _ = parsed_name(trace_path)
    selected_pages = read_top_pages(trace_path, top_pages)
    selected_pages = [page for page in selected_pages if page["sharer_count"] > 1]
    if not selected_pages:
        return None

    counts, owner_counts = read_trace_counts(trace_path, selected_pages, log2_page_size)
    matrix = make_matrix(selected_pages, counts)
    if matrix.sum() == 0:
        return None

    # Log scale preserves zeros while making hot pages readable.
    plotted = np.log10(matrix + 1.0)
    fig_width = 8.0
    fig_height = 4.8

    plt.rcParams.update(
        {
            "font.size": FONT_SIZE,
            "axes.titlesize": FONT_SIZE,
            "axes.labelsize": FONT_SIZE,
            "xtick.labelsize": 9,
            "ytick.labelsize": 10,
        }
    )

    fig, ax = plt.subplots(figsize=(fig_width, fig_height))
    fig.patch.set_facecolor("white")

    im = ax.imshow(plotted, aspect="auto", cmap="magma", origin="lower")
    ax.set_title(f"{title_name}: Shared Page Heatmap", pad=8)
    ax.set_xlabel("Top shared pages (page id / owner)")
    ax.set_ylabel("Requester GPU")

    xlabels = [
        f"p{page['page_id']}\nO{page_owner_label(page, owner_counts)}"
        for page in selected_pages
    ]
    ax.set_xticks(range(len(selected_pages)))
    ax.set_xticklabels(xlabels, rotation=0, ha="center")
    yticks = list(range(0, 48, 4))
    ax.set_yticks(yticks)
    ax.set_yticklabels([str(i + 1) for i in yticks])

    for col, page in enumerate(selected_pages):
        owner = page_owner_label(page, owner_counts)
        if owner == "?":
            continue
        owner_idx = int(owner) - 1
        ax.scatter(
            [col],
            [owner_idx],
            marker="o",
            s=34,
            facecolors="none",
            edgecolors="#00E5FF",
            linewidths=1.2,
        )

    cbar = fig.colorbar(im, ax=ax, pad=0.012)
    cbar.set_label("log10(accesses + 1)")

    subtitle = (
        f"Top {len(selected_pages)} shared pages; cyan circle marks page owner GPU"
    )
    ax.text(
        0.0,
        -0.22,
        subtitle,
        transform=ax.transAxes,
        fontsize=10,
        color="#444444",
        va="top",
    )

    out_dir.mkdir(parents=True, exist_ok=True)
    png = out_dir / f"{stem}_shared_page_heatmap.png"
    pdf = out_dir / f"{stem}_shared_page_heatmap.pdf"
    fig.tight_layout()
    fig.savefig(png, dpi=240, bbox_inches="tight")
    fig.savefig(pdf, bbox_inches="tight")
    plt.close(fig)

    nonzero_gpus = int((matrix.sum(axis=1) > 0).sum())
    return {
        "name": stem,
        "title": title_name,
        "pages": len(selected_pages),
        "requester_gpus": nonzero_gpus,
        "accesses": int(matrix.sum()),
        "png": png,
        "pdf": pdf,
    }


def write_summary(rows, out_dir):
    path = out_dir / "shared_page_heatmap_summary.csv"
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(
            f,
            fieldnames=["name", "title", "pages", "requester_gpus", "accesses", "png", "pdf"],
        )
        writer.writeheader()
        writer.writerows(rows)
    return path


def main():
    args = parse_args()
    results_dir = args.results_dir.resolve()
    out_dir = args.out_dir or results_dir / "figures" / "shared_page_heatmaps"

    paths = []
    for path in results_dir.glob("*_sharing.csv.gz"):
        stem, _, index = parsed_name(path)
        if args.only and args.only not in stem:
            continue
        paths.append((index, stem, path))
    paths.sort()

    summaries = []
    for _, _, path in paths:
        summary = save_heatmap(path, out_dir, args.top_pages, args.log2_page_size)
        if summary:
            summaries.append(summary)

    summary_path = write_summary(summaries, out_dir)
    print(f"Generated {len(summaries)} shared-page heatmaps.")
    print(f"Output directory: {out_dir}")
    print(summary_path)


if __name__ == "__main__":
    main()
