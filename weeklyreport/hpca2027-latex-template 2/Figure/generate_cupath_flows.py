#!/usr/bin/env python3
"""Generate the four raster-only CuPath design figures used by the paper."""

from pathlib import Path

import matplotlib.pyplot as plt
from matplotlib.patches import FancyArrowPatch, FancyBboxPatch


HERE = Path(__file__).resolve().parent

BLUE_50 = "#EAF7FA"
BLUE_100 = "#D6EFF5"
BLUE_200 = "#ADDEEB"
BLUE_300 = "#83CEE2"
BLUE_500 = "#31ADCE"
BLUE_700 = "#1D687C"
ORANGE_50 = "#FDF0E7"
ORANGE_100 = "#FBE0D0"
ORANGE_200 = "#F8C2A0"
ORANGE_300 = "#F4A371"
ORANGE_500 = "#ED6612"
ORANGE_700 = "#8E3D0B"
GRAY = "#4C4C4D"
LIGHT_GRAY = "#F2F2F2"
WHITE = "#FFFFFF"


def setup(width=10.2, height=5.6):
    fig, ax = plt.subplots(figsize=(width, height), dpi=180)
    fig.subplots_adjust(left=0, right=1, bottom=0, top=1)
    ax.set_xlim(0, 1)
    ax.set_ylim(0, 1)
    ax.axis("off")
    fig.patch.set_facecolor(WHITE)
    ax.set_facecolor(WHITE)
    return fig, ax


def box(ax, x, y, w, h, text, *, fc=WHITE, ec=GRAY, lw=1.8,
        fontsize=10, weight="semibold", radius=0.016, color=GRAY):
    p = FancyBboxPatch(
        (x, y), w, h,
        boxstyle=f"round,pad=0.008,rounding_size={radius}",
        linewidth=lw, edgecolor=ec, facecolor=fc,
    )
    ax.add_patch(p)
    ax.text(x + w / 2, y + h / 2, text, ha="center", va="center",
            fontsize=fontsize, fontweight=weight, color=color,
            linespacing=1.15)
    return p


def region(ax, x, y, w, h, title, *, fc=WHITE, ec=GRAY, title_color=GRAY):
    p = FancyBboxPatch(
        (x, y), w, h,
        boxstyle="round,pad=0.012,rounding_size=0.018",
        linewidth=2.0, edgecolor=ec, facecolor=fc,
    )
    ax.add_patch(p)
    ax.text(x + 0.014, y + h - 0.035, title, ha="left", va="top",
            fontsize=10, fontweight="bold", color=title_color)
    return p


def arrow(ax, x1, y1, x2, y2, *, color=ORANGE_500, lw=2.1,
          style="-|>", mutation=13, connection="arc3"):
    a = FancyArrowPatch(
        (x1, y1), (x2, y2), arrowstyle=style,
        mutation_scale=mutation, linewidth=lw, color=color,
        connectionstyle=connection, shrinkA=2, shrinkB=2,
    )
    ax.add_patch(a)
    return a


def label(ax, x, y, text, *, color=GRAY, fontsize=8.5, ha="center"):
    ax.text(x, y, text, ha=ha, va="center", fontsize=fontsize,
            color=color, fontweight="semibold")


def shared_filter(ax, x, y, w, h, *, compact=False):
    region(ax, x, y, w, h, "One shared typed Cuckoo Filter / L2 slice",
           fc=BLUE_50, ec=BLUE_500, title_color=BLUE_700)
    pad = 0.018
    gap = 0.010
    cell_w = (w - 2 * pad - 4 * gap) / 5
    yy = y + 0.025
    hh = h - 0.105
    for i, (name, purpose, fc) in enumerate([
        ("PATTERN", "real evidence", ORANGE_100),
        ("RESIDENT", "L2 line", BLUE_100),
        ("PENDING", "in flight", ORANGE_200),
        ("SEEN", "real reuse", BLUE_200),
        ("LOCAL-PEND", "local MSHR", ORANGE_100),
    ]):
        txt = name if compact else f"{name}\n{purpose}"
        box(ax, x + pad + i * (cell_w + gap), yy, cell_w, hh, txt,
            fc=fc, ec=ORANGE_700 if i in (0, 2, 4) else BLUE_700,
            lw=1.2, fontsize=5.7 if compact else 5.9, radius=0.008)


def finish(fig, name):
    fig.savefig(HERE / name, dpi=300, bbox_inches="tight", pad_inches=0.035,
                facecolor=WHITE, transparent=False)
    plt.close(fig)


def overall():
    fig, ax = setup(10.4, 5.4)
    region(ax, 0.012, 0.02, 0.976, 0.95,
           "CuPath: one request, progressively richer visibility",
           fc=WHITE, ec=GRAY)

    shared_filter(ax, 0.285, 0.72, 0.43, 0.18, compact=False)

    stages = [
        (0.045, "Requesting GPM\nL1", LIGHT_GRAY, GRAY),
        (0.180, "Requesting GPM\nexisting L2", BLUE_50, BLUE_700),
        (0.335, "Requesting GPM\nRDMA", ORANGE_50, ORANGE_700),
        (0.475, "Wafer\nnetwork", LIGHT_GRAY, GRAY),
        (0.615, "Home GPM\nRDMA", ORANGE_50, ORANGE_700),
        (0.755, "Home GPM\nL2 + HBM", BLUE_50, BLUE_700),
    ]
    y, w, h = 0.43, 0.105, 0.15
    for i, (x, text, fc, ec) in enumerate(stages):
        box(ax, x, y, w, h, text, fc=fc, ec=ec, fontsize=8.8)
        if i + 1 < len(stages):
            arrow(ax, x + w, y + h / 2, stages[i + 1][0], y + h / 2)
    arrow(ax, 0.86, y + 0.02, 0.09, y + 0.02, color=BLUE_500,
          connection="arc3,rad=-0.23")
    label(ax, 0.475, 0.31, "data response and metadata update", color=BLUE_700)

    actions = [
        (0.050, "M1  FILTER + PAIR\npair a real peer or screen one sibling", BLUE_100, BLUE_700),
        (0.375, "M2  AGGREGATE\npiggyback an existing home-GPM/page batch", ORANGE_100, ORANGE_700),
        (0.700, "M3  RETAIN + FEEDBACK\nreuse existing requesting-GPM L2", BLUE_200, BLUE_700),
    ]
    for x, text, fc, ec in actions:
        box(ax, x, 0.105, 0.25, 0.115, text, fc=fc, ec=ec, fontsize=8.3)

    label(ax, 0.50, 0.055,
          "real evidence  →  Filter qualification  →  aggregate/retain or drop immediately",
          color=GRAY, fontsize=9.0)
    finish(fig, "CuPathoverall.png")


def m1():
    fig, ax = setup(10.2, 5.4)
    region(ax, 0.012, 0.02, 0.976, 0.95,
           "M1 — Filter-guided local pair formation",
           fc=WHITE, ec=GRAY)
    shared_filter(ax, 0.31, 0.735, 0.38, 0.14, compact=True)

    label(ax, 0.035, 0.695, "Step 1: classify the real demand", color=BLUE_700,
          fontsize=8.3, ha="left")
    demand = [
        (0.035, 0.12, "Real L1\nmiss", LIGHT_GRAY, GRAY),
        (0.19, 0.15, "RESIDENT\nmembership", BLUE_50, BLUE_700),
        (0.39, 0.15, "Reliable negative:\nskip 10-cycle tag", BLUE_100, BLUE_700),
        (0.59, 0.15, "Exact MSHR\nmerge / allocate", ORANGE_50, ORANGE_700),
        (0.79, 0.17, "Ordinary DRAM read;\nexisting L2 fill", ORANGE_100, ORANGE_700),
    ]
    y, h = 0.54, 0.115
    for i, (x, w, text, fc, ec) in enumerate(demand):
        box(ax, x, y, w, h, text, fc=fc, ec=ec, fontsize=7.4)
        if i + 1 < len(demand):
            arrow(ax, x + w, y + h / 2, demand[i + 1][0], y + h / 2)
    arrow(ax, 0.50, 0.735, 0.265, y + h, color=BLUE_500,
          connection="arc3,rad=0.10")

    box(ax, 0.39, 0.39, 0.15, 0.085,
        "Positive / unreliable\n→ normal tag lookup",
        fc=LIGHT_GRAY, ec=GRAY, fontsize=7.1)
    arrow(ax, 0.265, y, 0.43, 0.475, color=GRAY,
          connection="arc3,rad=0.16")
    arrow(ax, 0.54, 0.432, 0.665, y, color=GRAY,
          connection="arc3,rad=-0.15")

    label(ax, 0.035, 0.335, "Steps 2–4: classify the sibling and form a pair", color=ORANGE_700,
          fontsize=8.3, ha="left")
    sibling = [
        (0.035, 0.12, "Later real\npair hint", ORANGE_50, ORANGE_700),
        (0.185, 0.13, "LOCAL-PENDING\nmembership", BLUE_50, BLUE_700),
        (0.345, 0.18, "Possible match:\nexact MSHR", BLUE_100, BLUE_700),
    ]
    y, h = 0.16, 0.115
    for i, (x, w, text, fc, ec) in enumerate(sibling):
        box(ax, x, y, w, h, text, fc=fc, ec=ec, fontsize=7.2)
        if i + 1 < len(sibling):
            arrow(ax, x + w, y + h / 2, sibling[i + 1][0], y + h / 2)
    arrow(ax, 0.56, 0.735, 0.43, y + h, color=BLUE_500)

    box(ax, 0.56, 0.245, 0.17, 0.085,
        "Exact ready peer\n→ pair real demands",
        fc=BLUE_100, ec=BLUE_700, fontsize=6.9)
    box(ax, 0.56, 0.145, 0.17, 0.085,
        "Pattern + absent\n+ resources → fetch",
        fc=ORANGE_100, ec=ORANGE_700, fontsize=6.9)
    box(ax, 0.56, 0.045, 0.17, 0.075,
        "Resident / unready\n→ singleton",
        fc=LIGHT_GRAY, ec=GRAY, fontsize=6.7)
    arrow(ax, 0.525, y + h / 2, 0.56, 0.287, color=BLUE_500,
          connection="arc3,rad=-0.12")
    arrow(ax, 0.525, y + h / 2, 0.56, 0.187, color=ORANGE_500)
    arrow(ax, 0.525, y + h / 2, 0.56, 0.082, color=GRAY,
          connection="arc3,rad=0.12")

    box(ax, 0.78, 0.17, 0.18, 0.105,
        "Pair descriptor:\ntwo adjacent cachelines",
        fc=ORANGE_200, ec=ORANGE_700, fontsize=7.1)
    arrow(ax, 0.73, 0.287, 0.78, 0.245, color=BLUE_500,
          connection="arc3,rad=0.12")
    arrow(ax, 0.73, 0.187, 0.78, 0.205, color=ORANGE_500,
          connection="arc3,rad=-0.08")

    label(ax, 0.855, 0.125,
          "same-bank/row → preserve reuse",
          color=ORANGE_700, fontsize=6.9)
    finish(fig, "M1.png")


def m2():
    fig, ax = setup(10.2, 5.4)
    region(ax, 0.012, 0.02, 0.976, 0.95,
           "M2 — Existing-batch-only remote candidate aggregation",
           fc=WHITE, ec=GRAY)
    shared_filter(ax, 0.31, 0.68, 0.38, 0.18, compact=True)

    box(ax, 0.035, 0.50, 0.13, 0.12, "Remote real\ndemand", fc=LIGHT_GRAY)
    box(ax, 0.21, 0.50, 0.16, 0.12, "Exact same-line\nwaiter aggregation",
        fc=BLUE_50, ec=BLUE_700, fontsize=8.0)
    box(ax, 0.42, 0.50, 0.14, 0.12, "One remote\ncandidate", fc=ORANGE_50,
        ec=ORANGE_700)
    box(ax, 0.61, 0.50, 0.17, 0.12, "Same home GPM + page\nexisting batch?",
        fc=BLUE_100, ec=BLUE_700, fontsize=8.0)
    box(ax, 0.83, 0.50, 0.13, 0.12, "Piggyback\nbitmap line",
        fc=ORANGE_100, ec=ORANGE_700, fontsize=8.0)
    for x1, x2 in [(0.165, 0.21), (0.37, 0.42), (0.56, 0.61), (0.78, 0.83)]:
        arrow(ax, x1, 0.56, x2, 0.56)

    arrow(ax, 0.695, 0.50, 0.695, 0.36, color=BLUE_500)
    box(ax, 0.58, 0.245, 0.23, 0.105,
        "No batch / batch full\n→ drop candidate",
        fc=BLUE_50, ec=BLUE_700, fontsize=8.0)
    arrow(ax, 0.895, 0.50, 0.895, 0.36, color=ORANGE_500)
    box(ax, 0.82, 0.245, 0.15, 0.105,
        "Insert PENDING\nno extra wait",
        fc=ORANGE_50, ec=ORANGE_700, fontsize=8.0)

    box(ax, 0.07, 0.085, 0.25, 0.095,
        "Candidate never creates\na standalone packet",
        fc=LIGHT_GRAY, ec=GRAY, fontsize=8.0)
    box(ax, 0.375, 0.085, 0.25, 0.095,
        "Later demand joins\nthe exact pending line",
        fc=BLUE_50, ec=BLUE_700, fontsize=8.0)
    box(ax, 0.68, 0.085, 0.25, 0.095,
        "Bounded issue, home-GPM expansion,\nand response fanout",
        fc=ORANGE_50, ec=ORANGE_700, fontsize=7.8)
    finish(fig, "M2.png")


def m3():
    fig, ax = setup(10.2, 6.1)
    region(ax, 0.012, 0.02, 0.976, 0.95,
           "M3 — Filter-gated requesting-GPM L2 probe and remote reuse",
           fc=WHITE, ec=GRAY)
    shared_filter(ax, 0.31, 0.72, 0.38, 0.15, compact=True)

    region(ax, 0.035, 0.10, 0.44, 0.59, "Before remote traversal",
           fc=WHITE, ec=BLUE_500, title_color=BLUE_700)
    box(ax, 0.065, 0.525, 0.13, 0.095, "Real remote\ndemand",
        fc=LIGHT_GRAY, fontsize=7.7)
    box(ax, 0.235, 0.525, 0.19, 0.095, "RESIDENT + SEEN\nmembership",
        fc=BLUE_50, ec=BLUE_700, fontsize=7.7)
    arrow(ax, 0.195, 0.572, 0.235, 0.572)

    box(ax, 0.065, 0.355, 0.155, 0.105,
        "Reliable negative\n→ skip L2 probe",
        fc=ORANGE_50, ec=ORANGE_700, fontsize=7.5)
    box(ax, 0.265, 0.355, 0.16, 0.105,
        "Positive / unreliable\n→ exact L2 probe",
        fc=BLUE_100, ec=BLUE_700, fontsize=7.3)
    arrow(ax, 0.285, 0.525, 0.15, 0.46, color=ORANGE_500,
          connection="arc3,rad=0.18")
    arrow(ax, 0.36, 0.525, 0.35, 0.46, color=BLUE_500)

    box(ax, 0.075, 0.18, 0.14, 0.09, "RDMA / home GPM",
        fc=ORANGE_100, ec=ORANGE_700, fontsize=7.7)
    box(ax, 0.275, 0.18, 0.14, 0.09, "L2 hit: finish\nmiss: home GPM",
        fc=BLUE_200, ec=BLUE_700, fontsize=7.5)
    arrow(ax, 0.145, 0.355, 0.145, 0.27)
    arrow(ax, 0.345, 0.355, 0.345, 0.27, color=BLUE_500)

    region(ax, 0.525, 0.10, 0.44, 0.59, "After remote response",
           fc=WHITE, ec=ORANGE_500, title_color=ORANGE_700)
    box(ax, 0.555, 0.525, 0.14, 0.095, "Remote response\nremove PENDING",
        fc=ORANGE_50, ec=ORANGE_700, fontsize=7.5)
    box(ax, 0.75, 0.525, 0.17, 0.095, "Real waiter / SEEN\nreuse evidence",
        fc=BLUE_50, ec=BLUE_700, fontsize=7.5)
    arrow(ax, 0.695, 0.572, 0.75, 0.572)

    box(ax, 0.555, 0.355, 0.17, 0.105,
        "Best-effort fill\nexisting requesting-GPM L2",
        fc=BLUE_100, ec=BLUE_700, fontsize=7.5)
    box(ax, 0.77, 0.355, 0.15, 0.105,
        "No safe victim\n→ drop fill",
        fc=ORANGE_50, ec=ORANGE_700, fontsize=7.5)
    arrow(ax, 0.80, 0.525, 0.65, 0.46, color=BLUE_500,
          connection="arc3,rad=0.17")
    arrow(ax, 0.86, 0.525, 0.845, 0.46)

    box(ax, 0.555, 0.18, 0.17, 0.09,
        "Useful real hit\n→ keep PATTERN",
        fc=BLUE_200, ec=BLUE_700, fontsize=7.5)
    box(ax, 0.77, 0.18, 0.15, 0.09,
        "Unused eviction\n→ remove PATTERN",
        fc=ORANGE_100, ec=ORANGE_700, fontsize=7.5)
    arrow(ax, 0.64, 0.355, 0.64, 0.27, color=BLUE_500)
    arrow(ax, 0.845, 0.355, 0.845, 0.27)

    label(ax, 0.50, 0.055,
          "Metadata gates work; exact tags supply data; the existing L2 stores every retained line",
          color=GRAY, fontsize=8.3)
    finish(fig, "M3.png")


if __name__ == "__main__":
    overall()
    m1()
    m2()
    m3()
