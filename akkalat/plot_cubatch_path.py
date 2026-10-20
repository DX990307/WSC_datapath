#!/usr/bin/env python3
"""Draw the paper's request-centric CuBatch overview."""

from pathlib import Path

import matplotlib.pyplot as plt
from matplotlib.patches import FancyArrowPatch, FancyBboxPatch


BLUE_300 = "#83CEE2"
BLUE_400 = "#5ABED8"
BLUE_500 = "#31ADCE"
ORANGE_400 = "#F18541"
ORANGE_600 = "#BE520E"
DARK = "#051115"
LIGHT = "#F2F2F2"


def box(ax, x, y, w, h, title, body, color):
    patch = FancyBboxPatch(
        (x, y), w, h,
        boxstyle="round,pad=0.015,rounding_size=0.025",
        linewidth=1.15, edgecolor=color, facecolor="white",
    )
    ax.add_patch(patch)
    title_w = 0.30 * w
    ax.add_patch(FancyBboxPatch(
        (x, y), title_w, h,
        boxstyle="round,pad=0.015,rounding_size=0.025",
        linewidth=0, facecolor=color,
    ))
    ax.text(x + title_w / 2, y + h / 2, title, ha="center", va="center",
            color="white", fontsize=5.0, fontweight="bold", linespacing=1.0)
    ax.text(x + title_w + (w - title_w) / 2, y + h / 2, body,
            ha="center", va="center", color=DARK, fontsize=4.5,
            linespacing=1.1)


def arrow(ax, x0, y0, x1, y1, label=None, color=DARK):
    ax.add_patch(FancyArrowPatch(
        (x0, y0), (x1, y1), arrowstyle="-|>", mutation_scale=9,
        linewidth=1.0, color=color, shrinkA=1, shrinkB=1,
    ))
    if label:
        ax.text((x0 + x1) / 2, (y0 + y1) / 2 + 0.025, label,
                ha="center", va="bottom", fontsize=5.8, color=color)


def main():
    out = Path(__file__).resolve().parent / "results" / \
        "2026-07-12-combined-observations-paper-v3" / "figures"
    out.mkdir(parents=True, exist_ok=True)

    fig, ax = plt.subplots(figsize=(3.45, 2.05))
    ax.set_xlim(0, 1)
    ax.set_ylim(0, 1)
    ax.axis("off")

    ax.text(0.5, 0.982, "A request is transformed as visibility changes",
            ha="center", va="top", fontsize=7.0, fontweight="bold", color=DARK)

    x, w, h = 0.12, 0.76, 0.10
    box(ax, x, 0.82, w, h, "L1 MISS", "Route by physical\npage owner", BLUE_500)
    box(ax, x, 0.65, w, h, "REQUESTER", "Terminate reuse\nMerge in-flight lines", BLUE_400)
    box(ax, x, 0.48, w, h, "RDMA +\nWAFER", "Group visible work\nby owner/page", ORANGE_400)
    box(ax, x, 0.31, w, h, "OWNER\nMEMORY", "Filter L2 misses\nShape 64-B accesses", BLUE_300)
    box(ax, x, 0.14, w, h, "RETURN", "Fan out response\nRecurrent fill", ORANGE_600)

    for top, bottom in ((0.82, 0.75), (0.65, 0.58), (0.48, 0.41), (0.31, 0.24)):
        arrow(ax, 0.5, top, 0.5, bottom)

    ax.text(0.5, 0.018,
            "At every boundary: filter unnecessary work, aggregate visible work, otherwise fall back immediately.",
            ha="center", va="bottom", fontsize=4.8, color=DARK)

    fig.tight_layout(pad=0.15)
    for suffix in ("pdf", "png"):
        fig.savefig(out / f"cubatch_path_overview.{suffix}", dpi=300,
                    bbox_inches="tight", pad_inches=0.02)
    plt.close(fig)


if __name__ == "__main__":
    main()
