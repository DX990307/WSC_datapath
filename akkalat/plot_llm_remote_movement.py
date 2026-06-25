#!/usr/bin/env python3
"""Plot structured remote-data movement patterns for decomposed LLM operators."""

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
from matplotlib.patches import FancyArrowPatch, Rectangle


GRID_WIDTH = 7
GRID_HEIGHT = 7
CENTER = (GRID_WIDTH // 2, GRID_HEIGHT // 2)

OP_ORDER = [
    "000_embedding",
    "001_layer00_norm1",
    "006_layer00_causal_mask",
    "007_layer00_attn_softmax",
    "010_layer00_attn_residual",
    "011_layer00_norm2",
    "013_layer00_mlp_gelu",
    "015_layer00_mlp_residual",
]

OP_NAMES = {
    "000_embedding": "Embed",
    "001_layer00_norm1": "Norm1",
    "006_layer00_causal_mask": "Mask",
    "007_layer00_attn_softmax": "Softmax",
    "010_layer00_attn_residual": "Attn Res",
    "011_layer00_norm2": "Norm2",
    "013_layer00_mlp_gelu": "MLP GELU",
    "015_layer00_mlp_residual": "MLP Res",
}

COLORS = {
    "local": "#2F80ED",
    "remote": "#EB5757",
    "oracle": "#59A14F",
    "current": "#F28E2B",
    "private": "#7B61FF",
    "grid": "#D8DEE9",
    "text": "#263238",
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing LLM *_sharing.csv.gz traces.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Directory for figures. Defaults to results_dir/figures/remote_movement.",
    )
    parser.add_argument(
        "--top-flows",
        type=int,
        default=14,
        help="Number of requester-owner flows to draw in each wafer map.",
    )
    return parser.parse_args()


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


def manhattan(a, b):
    ax, ay = COORDS[a]
    bx, by = COORDS[b]
    return abs(ax - bx) + abs(ay - by)


def delta(requester, owner):
    rx, ry = COORDS[requester]
    ox, oy = COORDS[owner]
    return ox - rx, oy - ry


def op_from_trace(path):
    name = path.name
    name = name.replace("baseline_llmop_gpt_gpt-7b_", "")
    return name.replace("_baseline_sharing.csv.gz", "")


def op_from_metrics(path):
    name = path.name
    name = name.replace("baseline_llmop_gpt_gpt-7b_", "")
    return name.replace("_baseline_metrics.csv", "")


def is_valid_gzip(path):
    try:
        with gzip.open(path, "rb") as f:
            while f.read(1024 * 1024):
                pass
        return True
    except Exception:
        return False


def collect_trace_stats(path):
    page_requesters = defaultdict(Counter)
    pair_counts = Counter()
    delta_counts = Counter()
    owner_counts = Counter()
    requester_counts = Counter()

    total = 0
    remote = 0
    current_hop_sum = 0

    with gzip.open(path, "rt", newline="") as f:
        reader = csv.DictReader(f)
        for row in reader:
            requester = int(row["requester"])
            owner = int(row["owner"])
            if requester not in COORDS or owner not in COORDS:
                continue

            page = int(row["vaddr"]) >> 12
            distance = int(row.get("distance") or manhattan(requester, owner))

            total += 1
            current_hop_sum += distance
            page_requesters[page][requester] += 1

            if requester != owner:
                remote += 1
                pair_counts[(requester, owner)] += 1
                delta_counts[delta(requester, owner)] += 1
                owner_counts[owner] += 1
                requester_counts[requester] += 1

    oracle_hop_sum = 0
    private_accesses = 0
    dominant80_accesses = 0
    shared_accesses = 0
    for counts in page_requesters.values():
        accesses = sum(counts.values())
        if len(counts) == 1:
            private_accesses += accesses
        else:
            shared_accesses += accesses
        if accesses and max(counts.values()) / accesses >= 0.80:
            dominant80_accesses += accesses

        best = min(
            sum(manhattan(requester, owner) * count for requester, count in counts.items())
            for owner in COORDS
        )
        oracle_hop_sum += best

    return {
        "total": total,
        "remote": remote,
        "local": total - remote,
        "remote_ratio": remote / total if total else 0.0,
        "local_ratio": (total - remote) / total if total else 0.0,
        "current_avg_hop": current_hop_sum / total if total else 0.0,
        "oracle_avg_hop": oracle_hop_sum / total if total else 0.0,
        "private_access_ratio": private_accesses / total if total else 0.0,
        "dominant80_access_ratio": dominant80_accesses / total if total else 0.0,
        "shared_access_ratio": shared_accesses / total if total else 0.0,
        "pair_counts": pair_counts,
        "delta_counts": delta_counts,
        "owner_counts": owner_counts,
        "requester_counts": requester_counts,
    }


def collect_gpu_usage(results_dir):
    usage = {}
    for path in results_dir.glob("*_metrics.csv"):
        if path.name.endswith("_sharing_summary_metrics.csv"):
            continue
        op = op_from_metrics(path)
        driver_total = 0.0
        gpu_times = []
        with open(path, newline="") as f:
            reader = csv.DictReader(f, skipinitialspace=True)
            for row in reader:
                clean_row = {key.strip(): value for key, value in row.items() if key is not None}
                where = clean_row["where"].strip()
                what = clean_row["what"].strip()
                value = float(clean_row["value"])
                if where == "Driver" and what == "total_time":
                    driver_total = value
                match = re.fullmatch(r"GPU\[(\d+)\]\.CommandProcessor", where)
                if match and what == "kernel_time":
                    gpu_times.append(value)

        active = [value for value in gpu_times if value > 0]
        usage[op] = {
            "active_gpus": len(active),
            "tile_time_util": (
                sum(gpu_times) / (driver_total * max(len(gpu_times), 1))
                if driver_total
                else 0.0
            ),
        }
    return usage


def load_rows(results_dir):
    usage = collect_gpu_usage(results_dir)
    rows = []
    skipped = []
    for path in sorted(results_dir.glob("*_sharing.csv.gz")):
        op = op_from_trace(path)
        if not is_valid_gzip(path):
            skipped.append(op)
            continue
        if op not in OP_ORDER:
            continue
        stats = collect_trace_stats(path)
        stats["op"] = op
        stats["name"] = OP_NAMES.get(op, op)
        stats.update(usage.get(op, {"active_gpus": 0, "tile_time_util": 0.0}))
        rows.append(stats)

    order = {op: index for index, op in enumerate(OP_ORDER)}
    rows.sort(key=lambda row: order.get(row["op"], 1000))
    return rows, skipped


def save(fig, out_dir, stem, tight=True):
    out_dir.mkdir(parents=True, exist_ok=True)
    png = out_dir / f"{stem}.png"
    pdf = out_dir / f"{stem}.pdf"
    if tight:
        fig.tight_layout(pad=0.55)
    fig.savefig(png, dpi=240)
    fig.savefig(pdf)
    plt.close(fig)
    return png, pdf


def style_axis(ax):
    ax.grid(axis="y", color=COLORS["grid"], linewidth=0.8, alpha=0.85)
    ax.set_axisbelow(True)
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)


def plot_dashboard(rows, out_dir):
    names = [row["name"] for row in rows]
    x = list(range(len(rows)))

    fig, axes = plt.subplots(2, 2, figsize=(13.5, 8.2))
    ax = axes[0][0]
    local = [row["local_ratio"] for row in rows]
    remote = [row["remote_ratio"] for row in rows]
    ax.bar(x, local, color=COLORS["local"], label="Local")
    ax.bar(x, remote, bottom=local, color=COLORS["remote"], label="Remote")
    ax.set_xticks(x, names, rotation=24, ha="right")
    ax.set_ylim(0, 1)
    ax.set_ylabel("Access ratio")
    ax.set_title("Remote tensor accesses dominate many operators")
    ax.legend(ncols=2, frameon=False, loc="lower right")
    ax.yaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    style_axis(ax)

    ax = axes[0][1]
    width = 0.38
    current = [row["current_avg_hop"] for row in rows]
    oracle = [row["oracle_avg_hop"] for row in rows]
    ax.bar([i - width / 2 for i in x], current, width, color=COLORS["current"], label="Current")
    ax.bar([i + width / 2 for i in x], oracle, width, color=COLORS["oracle"], label="Best static owner")
    ax.set_xticks(x, names, rotation=24, ha="right")
    ax.set_ylabel("Average hop per access")
    ax.set_title("Ownership-aware placement leaves a large gap")
    ax.legend(frameon=False)
    style_axis(ax)

    ax = axes[1][0]
    shared = [row["shared_access_ratio"] for row in rows]
    private = [row["private_access_ratio"] for row in rows]
    ax.bar(x, private, color=COLORS["private"], label="Private-page accesses")
    ax.bar(x, shared, bottom=private, color="#FFB000", label="Shared-page accesses")
    ax.set_xticks(x, names, rotation=24, ha="right")
    ax.set_ylim(0, 1)
    ax.set_ylabel("Access ratio")
    ax.set_title("Most traffic is private, not shared")
    ax.legend(frameon=False, loc="lower right")
    ax.yaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    style_axis(ax)

    ax = axes[1][1]
    util = [row["tile_time_util"] for row in rows]
    remote_ratio = [row["remote_ratio"] for row in rows]
    sizes = [80 + 8 * row["active_gpus"] for row in rows]
    ax.scatter(util, remote_ratio, s=sizes, color="#37474F", alpha=0.82)
    for row, u, r in zip(rows, util, remote_ratio):
        ax.annotate(row["name"], (u, r), xytext=(5, 5), textcoords="offset points", fontsize=9)
    ax.set_xlim(0, max(util + [0.9]) * 1.08)
    ax.set_ylim(0, 1.05)
    ax.set_xlabel("Tile-time utilization proxy")
    ax.set_ylabel("Remote access ratio")
    ax.set_title("High usage does not imply local data")
    ax.xaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    ax.yaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    style_axis(ax)

    return save(fig, out_dir, "remote_movement_dashboard")


def delta_matrix(delta_counts):
    extent = range(-(GRID_WIDTH - 1), GRID_WIDTH)
    matrix = [[0 for _ in extent] for _ in extent]
    for (dx, dy), count in delta_counts.items():
        matrix[dy + GRID_HEIGHT - 1][dx + GRID_WIDTH - 1] = count
    return matrix


def plot_offset_gallery(rows, out_dir):
    fig, axes = plt.subplots(2, 4, figsize=(14.6, 7.6), sharex=True, sharey=True)
    max_count = max((max(row["delta_counts"].values()) for row in rows if row["delta_counts"]), default=1)
    norm = colors.LogNorm(vmin=1, vmax=max_count)
    cmap = plt.get_cmap("magma")

    image = None
    for ax, row in zip(axes.flat, rows):
        mat = delta_matrix(row["delta_counts"])
        image = ax.imshow(
            mat,
            origin="lower",
            cmap=cmap,
            norm=norm,
            extent=[-6.5, 6.5, -6.5, 6.5],
        )
        ax.axhline(0, color="white", linewidth=0.8, alpha=0.65)
        ax.axvline(0, color="white", linewidth=0.8, alpha=0.65)
        top = row["delta_counts"].most_common(3)
        coverage = sum(count for _, count in top) / row["remote"] if row["remote"] else 0.0
        label = ", ".join(f"{d}" for d, _ in top[:2])
        ax.set_title(f"{row['name']}\nTop3 {100 * coverage:.0f}%: {label}", fontsize=9.5, pad=4)
        ax.set_xticks([-6, -3, 0, 3, 6])
        ax.set_yticks([-6, -3, 0, 3, 6])
        ax.tick_params(length=0)

    for ax in axes[-1, :]:
        ax.set_xlabel("owner_x - requester_x")
    for ax in axes[:, 0]:
        ax.set_ylabel("owner_y - requester_y")

    fig.subplots_adjust(left=0.06, right=0.90, bottom=0.09, top=0.84, wspace=0.18, hspace=0.34)
    if image:
        cax = fig.add_axes([0.925, 0.18, 0.018, 0.58])
        cbar = fig.colorbar(image, cax=cax)
        cbar.set_label("Remote accesses")
    fig.suptitle("Remote Traffic Offset Gallery", fontsize=15, y=0.965)
    return save(fig, out_dir, "remote_offset_gallery", tight=False)


def draw_wafer_grid(ax):
    for y in range(GRID_HEIGHT):
        for x in range(GRID_WIDTH):
            if (x, y) == CENTER:
                rect = Rectangle((x - 0.44, y - 0.44), 0.88, 0.88, facecolor="#ECEFF1", edgecolor="#B0BEC5")
                ax.add_patch(rect)
                ax.text(x, y, "C", ha="center", va="center", fontsize=8, color="#78909C")
                continue
            rect = Rectangle((x - 0.44, y - 0.44), 0.88, 0.88, facecolor="white", edgecolor="#CFD8DC")
            ax.add_patch(rect)
    for dev, (x, y) in COORDS.items():
        ax.text(x, y, str(dev), ha="center", va="center", fontsize=7, color="#455A64")
    ax.set_xlim(-0.8, GRID_WIDTH - 0.2)
    ax.set_ylim(GRID_HEIGHT - 0.2, -0.8)
    ax.set_aspect("equal")
    ax.axis("off")


def add_flow(ax, requester, owner, weight, max_weight, color):
    rx, ry = COORDS[requester]
    ox, oy = COORDS[owner]
    strength = weight / max_weight if max_weight else 0.0
    arrow = FancyArrowPatch(
        (rx, ry),
        (ox, oy),
        connectionstyle="arc3,rad=0.10",
        arrowstyle="-|>",
        mutation_scale=8 + 14 * strength,
        linewidth=0.8 + 5.0 * strength,
        color=color,
        alpha=0.18 + 0.58 * strength,
        zorder=5,
    )
    ax.add_patch(arrow)


def plot_wafer_flows(rows, out_dir, top_flows):
    selected_names = {"000_embedding", "007_layer00_attn_softmax", "013_layer00_mlp_gelu", "015_layer00_mlp_residual"}
    selected = [row for row in rows if row["op"] in selected_names]
    fig, axes = plt.subplots(1, len(selected), figsize=(4.2 * len(selected), 4.55))
    if len(selected) == 1:
        axes = [axes]
    palette = ["#E53935", "#8E24AA", "#1E88E5", "#00897B"]
    for ax, row, color in zip(axes, selected, palette):
        draw_wafer_grid(ax)
        pairs = row["pair_counts"].most_common(top_flows)
        max_weight = pairs[0][1] if pairs else 1
        for (requester, owner), weight in reversed(pairs):
            add_flow(ax, requester, owner, weight, max_weight, color)
        coverage = sum(weight for _, weight in pairs) / row["remote"] if row["remote"] else 0.0
        ax.set_title(f"{row['name']}\nTop {top_flows} flows cover {100 * coverage:.0f}%", fontsize=10.5, pad=6)
    fig.subplots_adjust(left=0.02, right=0.99, bottom=0.04, top=0.79, wspace=0.14)
    fig.suptitle("Wafer Flow Maps: requester -> owner", fontsize=15, y=0.96)
    return save(fig, out_dir, "wafer_flow_maps", tight=False)


def write_values(rows, skipped, out_dir):
    path = out_dir / "remote_movement_values.csv"
    fields = [
        "op",
        "name",
        "total",
        "remote_ratio",
        "current_avg_hop",
        "oracle_avg_hop",
        "private_access_ratio",
        "shared_access_ratio",
        "active_gpus",
        "tile_time_util",
        "top_delta",
        "top_delta_coverage",
        "top3_delta_coverage",
    ]
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            top = row["delta_counts"].most_common(3)
            writer.writerow(
                {
                    "op": row["op"],
                    "name": row["name"],
                    "total": row["total"],
                    "remote_ratio": row["remote_ratio"],
                    "current_avg_hop": row["current_avg_hop"],
                    "oracle_avg_hop": row["oracle_avg_hop"],
                    "private_access_ratio": row["private_access_ratio"],
                    "shared_access_ratio": row["shared_access_ratio"],
                    "active_gpus": row["active_gpus"],
                    "tile_time_util": row["tile_time_util"],
                    "top_delta": top[0][0] if top else "",
                    "top_delta_coverage": top[0][1] / row["remote"] if top and row["remote"] else 0.0,
                    "top3_delta_coverage": sum(count for _, count in top) / row["remote"] if row["remote"] else 0.0,
                }
            )

    readme = out_dir / "README.md"
    with open(readme, "w") as f:
        f.write("# LLM Remote Movement Figures\n\n")
        f.write("Generated from valid `*_sharing.csv.gz` traces in the result directory.\n\n")
        f.write("- `remote_movement_dashboard`: local/remote ratio, current vs best static owner hop, private/shared access ratio, and usage-vs-remote scatter.\n")
        f.write("- `remote_offset_gallery`: heatmaps of `(owner_x - requester_x, owner_y - requester_y)` for remote accesses.\n")
        f.write("- `wafer_flow_maps`: top requester-to-owner flows over the 7x7 wafer layout with the center tile skipped.\n")
        if skipped:
            f.write("\nSkipped truncated gzip traces:\n")
            for op in skipped:
                f.write(f"- `{op}`\n")
    return path, readme


def main():
    args = parse_args()
    out_dir = args.out_dir or args.results_dir / "figures" / "remote_movement"
    rows, skipped = load_rows(args.results_dir)
    if not rows:
        raise SystemExit("No valid LLM sharing traces found.")

    generated = []
    generated.extend(plot_dashboard(rows, out_dir))
    generated.extend(plot_offset_gallery(rows, out_dir))
    generated.extend(plot_wafer_flows(rows, out_dir, args.top_flows))
    values, readme = write_values(rows, skipped, out_dir)

    print(f"Generated {len(generated)} figure files.")
    print(f"Output directory: {out_dir}")
    print(values)
    print(readme)
    if skipped:
        print("Skipped truncated gzip traces:")
        for op in skipped:
            print(f"  {op}")


if __name__ == "__main__":
    main()
