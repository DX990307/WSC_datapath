#!/usr/bin/env python3
"""Plot top shared pages on a 7x7 wafer GPU layout."""

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
from matplotlib import colors
from matplotlib.patches import Rectangle


GRID_WIDTH = 7
GRID_HEIGHT = 7
CENTER = (GRID_WIDTH // 2, GRID_HEIGHT // 2)
FONT_SIZE = 12

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

TRADITIONAL_DISPLAY_NAMES = {
    "aes": "AES",
    "bitonicsort": "Bitonic",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "Floyd",
    "im2col": "Im2Col",
    "kmeans": "KMeans",
    "matrixmultiplication": "MatMul",
    "matrixtranspose": "Transpose",
    "pagerank": "PageRank",
    "relu": "ReLU",
    "simpleconvolution": "Conv",
    "spmv": "SpMV",
}


def device_coords():
    coords = {}
    dev = 1
    for y in range(GRID_HEIGHT):
        for x in range(GRID_WIDTH):
            if (x, y) == CENTER:
                continue
            coords[dev] = (x, y)
            dev += 1
    return coords


COORDS = device_coords()


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
        help="Output directory. Defaults to results_dir/figures/shared_page_wafer_maps.",
    )
    parser.add_argument(
        "--top-pages",
        type=int,
        default=8,
        help="Number of top shared pages to show per operator.",
    )
    parser.add_argument(
        "--selection",
        choices=("top", "diverse"),
        default="top",
        help=(
            "Page selection policy. `top` uses highest-access shared pages; "
            "`diverse` mixes hot pages, high-sharer pages, distinct owners, "
            "and different requester sets."
        ),
    )
    parser.add_argument(
        "--only",
        default=None,
        help="Only plot files whose parsed name contains this string. Use commas for multiple filters.",
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

    name = path.name
    prefix = "baseline_"
    suffix = "_baseline_sharing.csv.gz"
    if name.startswith(prefix) and name.endswith(suffix):
        benchmark = name[len(prefix) : -len(suffix)]
        display = TRADITIONAL_DISPLAY_NAMES.get(benchmark, benchmark)
        return benchmark, display, 10_000

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
            sharer_count = int(float(row["sharer_count"]))
            if sharer_count <= 1:
                continue
            pages.append(
                {
                    "pid": int(row.get("pid", 0)),
                    "page_id": int(row["page_id"]),
                    "accesses": int(float(row["accesses"])),
                    "remote_accesses": int(float(row.get("remote_bytes", 0))),
                    "sharer_count": sharer_count,
                    "selection_reason": "top access",
                }
            )
            if len(pages) >= top_pages:
                break
    return pages


def gpu_distance(a, b):
    if a not in COORDS or b not in COORDS:
        return 0.0
    ax, ay = COORDS[a]
    bx, by = COORDS[b]
    return abs(ax - bx) + abs(ay - by)


def page_average_distance(counts, owner):
    total = sum(counts.values())
    if total == 0 or owner is None:
        return 0.0
    return sum(gpu_distance(requester, owner) * count for requester, count in counts.items()) / total


def read_all_page_stats(trace_path, log2_page_size):
    requester_counts = defaultdict(Counter)
    owner_counts = defaultdict(Counter)
    remote_counts = Counter()

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
            requester = int(row["requester"])
            owner = int(row["owner"])
            requester_counts[key][requester] += 1
            owner_counts[key][owner] += 1
            if requester != owner:
                remote_counts[key] += 1

    pages = []
    for key, counts in requester_counts.items():
        sharer_count = len(counts)
        if sharer_count <= 1:
            continue
        owner = owner_counts[key].most_common(1)[0][0] if owner_counts[key] else None
        pages.append(
            {
                "pid": key[0],
                "page_id": key[1],
                "accesses": sum(counts.values()),
                "remote_accesses": remote_counts[key],
                "sharer_count": sharer_count,
                "owner": owner,
                "avg_distance": page_average_distance(counts, owner),
                "requesters": set(counts.keys()),
                "selection_reason": "",
            }
        )
    return pages, requester_counts, owner_counts


def add_selected(selected, page, reason):
    key = (page["pid"], page["page_id"])
    if any((item["pid"], item["page_id"]) == key for item in selected):
        return
    copied = dict(page)
    copied["selection_reason"] = reason
    selected.append(copied)


def requester_jaccard_distance(page, selected):
    if not selected:
        return 1.0
    requesters = page["requesters"]
    distances = []
    for item in selected:
        other = item["requesters"]
        union = requesters | other
        if not union:
            distances.append(0.0)
            continue
        distances.append(1.0 - len(requesters & other) / len(union))
    return min(distances)


def select_diverse_pages(trace_path, top_pages, log2_page_size):
    pages, requester_counts, owner_counts = read_all_page_stats(trace_path, log2_page_size)
    if not pages:
        return [], requester_counts, owner_counts

    max_accesses = max(page["accesses"] for page in pages)
    min_accesses = max(4, int(max_accesses * 0.01))
    candidates = [page for page in pages if page["accesses"] >= min_accesses]
    if len(candidates) < top_pages:
        candidates = pages

    selected = []
    add_selected(
        selected,
        max(candidates, key=lambda page: (page["accesses"], page["sharer_count"])),
        "hottest page",
    )
    add_selected(
        selected,
        max(candidates, key=lambda page: (page["sharer_count"], page["accesses"])),
        "most sharers",
    )
    add_selected(
        selected,
        max(candidates, key=lambda page: (page["remote_accesses"], page["accesses"])),
        "most remote accesses",
    )
    add_selected(
        selected,
        max(candidates, key=lambda page: (page["avg_distance"], page["accesses"])),
        "widest requester spread",
    )

    best_by_owner = {}
    for page in sorted(candidates, key=lambda item: item["accesses"], reverse=True):
        best_by_owner.setdefault(page["owner"], page)
    for owner, page in sorted(
        best_by_owner.items(),
        key=lambda item: item[1]["accesses"],
        reverse=True,
    ):
        if len(selected) >= top_pages:
            break
        add_selected(selected, page, f"top page for owner {owner}")

    max_log_access = math.log1p(max(page["accesses"] for page in candidates))
    max_sharers = max(page["sharer_count"] for page in candidates)
    max_distance = max(page["avg_distance"] for page in candidates) or 1.0

    while len(selected) < top_pages:
        remaining = [
            page
            for page in candidates
            if not any(
                (item["pid"], item["page_id"]) == (page["pid"], page["page_id"])
                for item in selected
            )
        ]
        if not remaining:
            break

        selected_owners = {page["owner"] for page in selected}

        def score(page):
            access_score = math.log1p(page["accesses"]) / max_log_access
            sharer_score = page["sharer_count"] / max_sharers
            distance_score = page["avg_distance"] / max_distance
            diversity_score = requester_jaccard_distance(page, selected)
            owner_bonus = 0.20 if page["owner"] not in selected_owners else 0.0
            return (
                0.52 * access_score
                + 0.18 * sharer_score
                + 0.16 * distance_score
                + 0.34 * diversity_score
                + owner_bonus
            )

        add_selected(
            selected,
            max(remaining, key=score),
            "diverse requester set",
        )

    return selected[:top_pages], requester_counts, owner_counts


def read_trace_counts(trace_path, selected_pages, log2_page_size):
    selected = {(page["pid"], page["page_id"]) for page in selected_pages}
    requester_counts = defaultdict(Counter)
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
            requester_counts[key][requester] += 1
            owner_counts[key][owner] += 1

    return requester_counts, owner_counts


def owner_for_page(page, owner_counts):
    key = (page["pid"], page["page_id"])
    if not owner_counts[key]:
        return None
    return owner_counts[key].most_common(1)[0][0]


def draw_grid(ax, counts, owner, norm, cmap):
    for y in range(GRID_HEIGHT):
        for x in range(GRID_WIDTH):
            if (x, y) == CENTER:
                rect = Rectangle(
                    (x - 0.46, y - 0.46),
                    0.92,
                    0.92,
                    facecolor="#ECEFF1",
                    edgecolor="#B0BEC5",
                    linewidth=0.8,
                )
                ax.add_patch(rect)
                ax.text(x, y, "C", ha="center", va="center", fontsize=8, color="#78909C")
                continue

            gpu = None
            for dev, coord in COORDS.items():
                if coord == (x, y):
                    gpu = dev
                    break

            accesses = counts.get(gpu, 0)
            face = cmap(norm(accesses)) if accesses else "#FFFFFF"
            edge = "#CFD8DC"
            linewidth = 0.8
            if gpu == owner:
                edge = "#00A6D6"
                linewidth = 2.1
            rect = Rectangle(
                (x - 0.46, y - 0.46),
                0.92,
                0.92,
                facecolor=face,
                edgecolor=edge,
                linewidth=linewidth,
            )
            ax.add_patch(rect)
            label_color = "white" if accesses and norm(accesses) > 0.55 else "#455A64"
            ax.text(x, y, str(gpu), ha="center", va="center", fontsize=7, color=label_color)

    ax.set_xlim(-0.75, GRID_WIDTH - 0.25)
    ax.set_ylim(GRID_HEIGHT - 0.25, -0.75)
    ax.set_aspect("equal")
    ax.axis("off")


def select_pages(trace_path, top_pages, log2_page_size, selection):
    if selection == "diverse":
        return select_diverse_pages(trace_path, top_pages, log2_page_size)

    pages = read_top_pages(trace_path, top_pages)
    requester_counts, owner_counts = read_trace_counts(trace_path, pages, log2_page_size)
    return pages, requester_counts, owner_counts


def save_operator_gallery(trace_path, out_dir, top_pages, log2_page_size, selection):
    stem, title_name, _ = parsed_name(trace_path)
    pages, requester_counts, owner_counts = select_pages(
        trace_path,
        top_pages,
        log2_page_size,
        selection,
    )
    if not pages:
        return None

    max_accesses = max(
        (count for page in pages for count in requester_counts[(page["pid"], page["page_id"])].values()),
        default=0,
    )
    if max_accesses == 0:
        return None

    norm = colors.LogNorm(vmin=1, vmax=max_accesses)
    cmap = plt.get_cmap("YlOrRd")
    cols = min(4, len(pages))
    rows = math.ceil(len(pages) / cols)

    plt.rcParams.update(
        {
            "font.size": FONT_SIZE,
            "axes.titlesize": FONT_SIZE,
            "axes.labelsize": FONT_SIZE,
        }
    )

    fig, axes = plt.subplots(rows, cols, figsize=(3.0 * cols, 3.15 * rows + 0.45))
    if rows == 1 and cols == 1:
        axes = [[axes]]
    elif rows == 1:
        axes = [axes]
    elif cols == 1:
        axes = [[ax] for ax in axes]

    for ax in [ax for row in axes for ax in row]:
        ax.axis("off")

    summary_rows = []
    for index, page in enumerate(pages):
        row = index // cols
        col = index % cols
        ax = axes[row][col]
        key = (page["pid"], page["page_id"])
        counts = requester_counts[key]
        owner = owner_for_page(page, owner_counts)
        sharers = len(counts)
        draw_grid(ax, counts, owner, norm, cmap)
        ax.set_title(
            f"p{page['page_id']} owner={owner}\nsharers={sharers}, accesses={sum(counts.values())}",
            fontsize=10,
            pad=2,
        )
        summary_rows.append(
            {
                "page_id": page["page_id"],
                "owner": owner,
                "sharers": sharers,
                "accesses": sum(counts.values()),
                "remote_accesses": sum(
                    access_count
                    for requester, access_count in counts.items()
                    if requester != owner
                ),
                "avg_distance": page_average_distance(counts, owner),
                "selection_reason": page.get("selection_reason", ""),
            }
        )

    sm = plt.cm.ScalarMappable(norm=norm, cmap=cmap)
    sm.set_array([])
    cbar = fig.colorbar(sm, ax=[ax for row in axes for ax in row], fraction=0.025, pad=0.012)
    cbar.set_label("accesses/requester GPU")

    title_prefix = "Selected" if selection == "diverse" else "Top"
    fig.suptitle(
        f"{title_name}: {title_prefix} Shared Pages on 7x7 GPU Grid",
        fontsize=14,
        y=0.995,
    )
    fig.text(
        0.5,
        0.01,
        "Colored cells are requester GPUs for that page; blue border marks page owner; C is the center tile.",
        ha="center",
        va="bottom",
        fontsize=10,
        color="#444444",
    )

    out_dir.mkdir(parents=True, exist_ok=True)
    png = out_dir / f"{stem}_shared_page_wafer_map.png"
    pdf = out_dir / f"{stem}_shared_page_wafer_map.pdf"
    fig.savefig(png, dpi=240, bbox_inches="tight")
    fig.savefig(pdf, bbox_inches="tight")
    plt.close(fig)

    return {
        "name": stem,
        "title": title_name,
        "pages": len(pages),
        "max_accesses_per_gpu": max_accesses,
        "png": png,
        "pdf": pdf,
        "page_summaries": summary_rows,
    }


def write_summary(rows, out_dir):
    path = out_dir / "shared_page_wafer_map_summary.csv"
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(
            f,
            fieldnames=[
                "name",
                "title",
                "pages",
                "max_accesses_per_gpu",
                "png",
                "pdf",
            ],
        )
        writer.writeheader()
        for row in rows:
            copied = dict(row)
            copied.pop("page_summaries", None)
            writer.writerow(copied)
    return path


def write_page_summary(rows, out_dir):
    path = out_dir / "selected_shared_pages.csv"
    fieldnames = [
        "name",
        "title",
        "rank",
        "selection_reason",
        "page_id",
        "owner",
        "sharers",
        "accesses",
        "remote_accesses",
        "avg_distance",
        "png",
        "pdf",
    ]
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for row in rows:
            for rank, page in enumerate(row["page_summaries"], start=1):
                writer.writerow(
                    {
                        "name": row["name"],
                        "title": row["title"],
                        "rank": rank,
                        "selection_reason": page["selection_reason"],
                        "page_id": page["page_id"],
                        "owner": page["owner"],
                        "sharers": page["sharers"],
                        "accesses": page["accesses"],
                        "remote_accesses": page["remote_accesses"],
                        "avg_distance": f"{page['avg_distance']:.3f}",
                        "png": row["png"],
                        "pdf": row["pdf"],
                    }
                )
    return path


def main():
    args = parse_args()
    results_dir = args.results_dir.resolve()
    out_dir = args.out_dir or results_dir / "figures" / "shared_page_wafer_maps"
    only_filters = []
    if args.only:
        only_filters = [item.strip() for item in args.only.split(",") if item.strip()]

    paths = []
    for path in results_dir.glob("*_sharing.csv.gz"):
        stem, _, index = parsed_name(path)
        if only_filters and not any(only_filter in stem for only_filter in only_filters):
            continue
        paths.append((index, stem, path))
    paths.sort()

    summaries = []
    for _, _, path in paths:
        summary = save_operator_gallery(
            path,
            out_dir,
            args.top_pages,
            args.log2_page_size,
            args.selection,
        )
        if summary:
            summaries.append(summary)

    summary_path = write_summary(summaries, out_dir)
    page_summary_path = write_page_summary(summaries, out_dir)
    print(f"Generated {len(summaries)} shared-page wafer map galleries.")
    print(f"Output directory: {out_dir}")
    print(summary_path)
    print(page_summary_path)


if __name__ == "__main__":
    main()
