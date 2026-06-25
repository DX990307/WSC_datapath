#!/usr/bin/env python3
"""Plot page-sharing summary metrics from analyze_sharing_trace.py outputs."""

import argparse
import csv
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt
from matplotlib.ticker import FuncFormatter, PercentFormatter

FONT_SIZE = 14
FIGSIZE = (8, 3)

plt.rcParams.update(
    {
        "font.size": FONT_SIZE,
        "axes.titlesize": FONT_SIZE,
        "axes.labelsize": FONT_SIZE,
        "xtick.labelsize": FONT_SIZE,
        "ytick.labelsize": FONT_SIZE,
        "legend.fontsize": FONT_SIZE,
    }
)

BENCHMARK_ORDER = [
    "bitonicsort",
    "relu",
    "matrixmultiplication",
    "matrixtranspose",
    "kmeans",
    "spmv",
    "im2col",
    "aes",
    "fft",
    "floydwarshall",
    "pagerank",
    "simpleconvolution",
    "fastwalshtransform",
    "fir",
]

DISPLAY_NAMES = {
    "aes": "AES",
    "bitonicsort": "Bitonic Sort",
    "fastwalshtransform": "Fast Walsh",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "Floyd Warshall",
    "im2col": "Im2Col",
    "kmeans": "KMeans",
    "matrixmultiplication": "Matrix Mul.",
    "matrixtranspose": "Matrix Trans.",
    "pagerank": "PageRank",
    "relu": "ReLU",
    "simpleconvolution": "Simple Conv.",
    "spmv": "SpMV",
}

PLOT_NAMES = {
    "aes": "AES",
    "bitonicsort": "Bit",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "FW",
    "im2col": "Im2Col",
    "kmeans": "KM",
    "matrixmultiplication": "MM",
    "matrixtranspose": "MT",
    "pagerank": "PR",
    "relu": "ReLU",
    "simpleconvolution": "Conv",
    "spmv": "SpMV",
}

METRICS = [
    "TotalAccesses",
    "LocalAccessRatio",
    "RemoteAccessRatio",
    "TotalPages",
    "SharedPages",
    "SharedPageRatio",
    "RemoteAccesses",
    "RemotePages",
    "RemotePagesPerRemoteAccess",
    "RemoteAccessesPerRemotePage",
    "WeightedSharingDistance",
    "PairEntropyNorm",
]

COLOR_LOCAL = "#2F80ED"
COLOR_REMOTE = "#EB5757"
COLOR_SINGLE = "#4E79A7"
COLOR_TRAFFIC = "#59A14F"
COLOR_DISTANCE = "#F28E2B"
COLOR_DISPERSION = "#7B61FF"


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing *_sharing_summary_metrics.csv files.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Directory for figures. Defaults to results_dir/figures.",
    )
    return parser.parse_args()


def benchmark_from_path(path):
    name = path.name
    prefix = "baseline_"
    suffix = "_baseline_sharing_summary_metrics.csv"
    if name.startswith(prefix) and name.endswith(suffix):
        return name[len(prefix) : -len(suffix)]
    return name.replace("_sharing_summary_metrics.csv", "")


def read_metrics(path):
    metrics = {}
    with open(path, newline="") as f:
        reader = csv.DictReader(f)
        for row in reader:
            metric = row["metric"]
            value = row["value"]
            metrics[metric] = float(value) if value else None
    return metrics


def load_rows(results_dir):
    rows = []
    for path in sorted(results_dir.glob("*_sharing_summary_metrics.csv")):
        benchmark = benchmark_from_path(path)
        metrics = read_metrics(path)
        row = {"benchmark": benchmark, "name": DISPLAY_NAMES.get(benchmark, benchmark)}
        for metric in METRICS:
            row[metric] = metrics.get(metric)
        rows.append(row)

    order = {name: index for index, name in enumerate(BENCHMARK_ORDER)}
    rows.sort(key=lambda row: (order.get(row["benchmark"], 10_000), row["benchmark"]))
    return rows


def write_values_csv(rows, out_dir):
    path = out_dir / "sharing_plot_values.csv"
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=["benchmark", "name"] + METRICS)
        writer.writeheader()
        writer.writerows(rows)
    return path


def save_figure(fig, out_dir, stem):
    png = out_dir / f"{stem}.png"
    pdf = out_dir / f"{stem}.pdf"
    fig.tight_layout(pad=0.25)
    fig.savefig(png, dpi=220)
    fig.savefig(pdf)
    plt.close(fig)
    return png, pdf


def style_axis(ax):
    ax.grid(axis="y", color="#D8DEE9", linewidth=0.8, alpha=0.85)
    ax.set_axisbelow(True)
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)
    ax.tick_params(axis="x", length=0)


def compact_number(value, _pos=None):
    abs_value = abs(value)
    if abs_value >= 1_000_000:
        return f"{value / 1_000_000:.1f}M"
    if abs_value >= 1_000:
        return f"{value / 1_000:.1f}K"
    return f"{value:.0f}"


def compact_label(value):
    if abs(value) >= 1_000:
        return compact_number(value)
    return f"{value:.2f}"


def plot_names(rows):
    return [PLOT_NAMES.get(row["benchmark"], row["name"]) for row in rows]


def figure_size(rows, height=6.4):
    return FIGSIZE


def remove_stale_figures(out_dir):
    for stem in ("shared_cacheline_ratio", "shared_traffic_ratio"):
        for suffix in (".png", ".pdf"):
            path = out_dir / f"{stem}{suffix}"
            if path.exists():
                path.unlink()


def make_stacked_local_remote(rows, out_dir):
    names = plot_names(rows)
    local = [row["LocalAccessRatio"] or 0 for row in rows]
    remote = [row["RemoteAccessRatio"] or 0 for row in rows]
    x = range(len(rows))

    fig, ax = plt.subplots(figsize=figure_size(rows))
    ax.bar(x, local, color=COLOR_LOCAL, label="Local")
    ax.bar(x, remote, bottom=local, color=COLOR_REMOTE, label="Remote")
    ax.set_xticks(list(x), names)
    ax.set_ylim(0, 1.22)
    ax.yaxis.set_major_formatter(PercentFormatter(1.0))
    ax.set_xlabel("Benchmark")
    ax.set_ylabel("Access ratio")
    ax.set_title("Local vs Remote Access Ratio")
    ax.legend(ncols=2, loc="upper center", bbox_to_anchor=(0.5, 1.03), frameon=False)
    style_axis(ax)

    return save_figure(fig, out_dir, "local_vs_remote_access_ratio")


def make_percent_bar(rows, out_dir, metric, title, xlabel, stem, color):
    names = plot_names(rows)
    raw_values = [row[metric] for row in rows]
    values = [value if value is not None else 0 for value in raw_values]
    x = range(len(rows))
    finite_values = [value for value in values if value > 0]

    fig, ax = plt.subplots(figsize=figure_size(rows))
    colors = [color if value is not None else "#C7CDD8" for value in raw_values]
    bars = ax.bar(x, values, color=colors)
    ax.set_xticks(list(x), names)
    ax.set_ylim(0, min(1.35, max(finite_values) * 1.35 + 0.02) if finite_values else 1)
    ax.yaxis.set_major_formatter(PercentFormatter(1.0))
    ax.set_xlabel("Benchmark")
    ax.set_ylabel(xlabel)
    ax.set_title(title)
    style_axis(ax)
    offset = ax.get_ylim()[1] * 0.012
    for bar, value, raw_value in zip(bars, values, raw_values):
        if raw_value is None or value < 0.02:
            continue
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            value + offset,
            f"{value * 100:.1f}%",
            va="bottom",
            ha="center",
            fontsize=FONT_SIZE,
            rotation=90,
        )
    return save_figure(fig, out_dir, stem)


def make_number_bar(rows, out_dir, metric, title, xlabel, stem, color):
    names = plot_names(rows)
    raw_values = [row[metric] for row in rows]
    values = [value if value is not None else 0 for value in raw_values]
    x = range(len(rows))

    fig, ax = plt.subplots(figsize=figure_size(rows))
    colors = [color if value is not None else "#C7CDD8" for value in raw_values]
    bars = ax.bar(x, values, color=colors)
    ax.set_xticks(list(x), names)
    ax.set_ylim(0, max(values) * 1.38 if values else 1)
    if max(values) >= 1_000:
        ax.yaxis.set_major_formatter(FuncFormatter(compact_number))
    ax.set_xlabel("Benchmark")
    ax.set_ylabel(xlabel)
    ax.set_title(title)
    style_axis(ax)
    finite_values = [value for value in values if value > 0]
    offset = max(finite_values) * 0.012 if finite_values else 0.05
    for bar, value, raw_value in zip(bars, values, raw_values):
        if raw_value is None or value <= 0:
            continue
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            value + offset,
            compact_label(value),
            va="bottom",
            ha="center",
            fontsize=FONT_SIZE,
            rotation=90,
        )
    return save_figure(fig, out_dir, stem)


def write_notes(results_dir, out_dir, rows):
    summarized = {row["benchmark"] for row in rows}
    traced = {
        path.name[len("baseline_") : -len("_baseline_sharing.csv.gz")]
        for path in results_dir.glob("baseline_*_baseline_sharing.csv.gz")
    }
    missing = sorted(traced - summarized, key=lambda name: (BENCHMARK_ORDER.index(name) if name in BENCHMARK_ORDER else 10_000, name))

    path = out_dir / "README.md"
    with open(path, "w") as f:
        f.write("# Sharing Metric Figures\n\n")
        f.write("Generated from `*_sharing_summary_metrics.csv` files in this result directory.\n\n")
        f.write("Metric name mapping:\n\n")
        f.write("- `SharedPageRatio` -> `Shared Page Ratio`\n")
        f.write("- `RemoteAccessesPerRemotePage` -> `Remote Page Hotspot`\n")
        f.write("- `RemotePagesPerRemoteAccess` stores the literal unique remote pages / remote accesses ratio; the plotted figure uses its inverse so larger means hotter.\n")
        f.write("- `WeightedSharingDistance` -> `Average Sharing Distance`\n")
        f.write("- `PairEntropyNorm` -> `Requester-Owner Pair Dispersion`\n\n")
        f.write("Compact x-axis labels: `Bit` = Bitonic Sort, `MM` = Matrix Multiplication, `MT` = Matrix Transpose, `KM` = KMeans, `FW` = Floyd Warshall, `PR` = PageRank, `Conv` = Simple Convolution, `FWT` = Fast Walsh Transform.\n\n")
        f.write("Figures:\n\n")
        f.write("- `local_vs_remote_access_ratio.png`\n")
        f.write("- `shared_page_ratio.png`\n")
        f.write("- `remote_page_hotspot.png`\n")
        f.write("- `average_sharing_distance.png`\n")
        f.write("- `requester_owner_pair_dispersion.png`\n\n")
        if missing:
            f.write("Workloads with raw sharing traces but no summary metrics were not plotted:\n\n")
            for name in missing:
                f.write(f"- `{name}`\n")
        else:
            f.write("All raw sharing traces with this naming pattern had summary metrics and were plotted.\n")
    return path, missing


def main():
    args = parse_args()
    results_dir = args.results_dir
    out_dir = args.out_dir or results_dir / "figures"
    out_dir.mkdir(parents=True, exist_ok=True)
    remove_stale_figures(out_dir)

    rows = load_rows(results_dir)
    if not rows:
        raise SystemExit(f"No *_sharing_summary_metrics.csv files found in {results_dir}")

    written = []
    written.append(write_values_csv(rows, out_dir))
    written.extend(make_stacked_local_remote(rows, out_dir))
    written.extend(
        make_percent_bar(
            rows,
            out_dir,
            "SharedPageRatio",
            "Shared Page Ratio",
            "Shared page ratio",
            "shared_page_ratio",
            COLOR_SINGLE,
        )
    )
    written.extend(
        make_number_bar(
            rows,
            out_dir,
            "RemoteAccessesPerRemotePage",
            "Remote Page Hotspot",
            "Remote accesses/page",
            "remote_page_hotspot",
            COLOR_TRAFFIC,
        )
    )
    written.extend(
        make_number_bar(
            rows,
            out_dir,
            "WeightedSharingDistance",
            "Average Sharing Distance",
            "Avg. hop distance",
            "average_sharing_distance",
            COLOR_DISTANCE,
        )
    )
    written.extend(
        make_percent_bar(
            rows,
            out_dir,
            "PairEntropyNorm",
            "Requester-Owner Pair Dispersion",
            "Pair dispersion",
            "requester_owner_pair_dispersion",
            COLOR_DISPERSION,
        )
    )
    notes, missing = write_notes(results_dir, out_dir, rows)
    written.append(notes)

    print(f"Plotted {len(rows)} workloads.")
    if missing:
        print("Missing summary metrics for: " + ", ".join(missing))
    print(f"Output directory: {out_dir}")
    for path in written:
        print(path)


if __name__ == "__main__":
    main()
