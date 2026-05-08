import argparse
import csv
import os
import re
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt


DEFAULT_RESULTS_DIR = Path(
    "akkalat/results/2026-05-06-05-19-16-sampled-validation"
)

CONFIG_RE = re.compile(
    r"^(?P<target>[^_]+)_(?P<benchmark>[^_]+)_"
    r"(?P<config>sample_wf)_w(?P<warmup>\d+)_g(?P<granularity>\d+)_"
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        default=str(DEFAULT_RESULTS_DIR),
        help="Directory containing runall2 metrics/log files.",
    )
    parser.add_argument(
        "--benchmark",
        default="relu",
        help="Benchmark to plot.",
    )
    parser.add_argument(
        "--config",
        default="sample_wf",
        help="Sampled config prefix to plot.",
    )
    parser.add_argument(
        "--output",
        default="",
        help="PNG output path. Defaults to <results-dir>/<benchmark>_<config>_tradeoff.png.",
    )
    parser.add_argument(
        "--csv",
        default="",
        help="CSV output path. Defaults to <results-dir>/<benchmark>_<config>_tradeoff.csv.",
    )
    return parser.parse_args()


def read_driver_total_time(path):
    with path.open(newline="") as f:
        reader = csv.DictReader(f, skipinitialspace=True)
        for row in reader:
            if row.get("where") == "Driver" and row.get("what") == "total_time":
                return float(row["value"])
    raise ValueError(f"Driver total_time not found in {path}")


def parse_elapsed_seconds(path):
    if not path.exists():
        return None

    elapsed = None
    with path.open(errors="replace") as f:
        for line in f:
            if not line.startswith("Elapsed time:"):
                continue
            elapsed = line.split(":", 1)[1].strip()

    if not elapsed:
        return None

    parts = elapsed.split(":")
    if len(parts) != 3:
        return None

    hours = int(parts[0])
    minutes = int(parts[1])
    seconds = float(parts[2])
    return hours * 3600 + minutes * 60 + seconds


def parse_sample_file(path, benchmark, config):
    match = CONFIG_RE.match(path.name)
    if not match:
        return None
    if match.group("benchmark") != benchmark:
        return None
    if match.group("config") != config:
        return None
    return int(match.group("warmup")), int(match.group("granularity"))


def collect_rows(results_dir, benchmark, config):
    baseline_metrics = results_dir / f"baseline_{benchmark}_baseline_metrics.csv"
    baseline_log = results_dir / f"baseline_{benchmark}_baseline_out.stdout"
    baseline_sim_time = read_driver_total_time(baseline_metrics)
    baseline_wall_seconds = parse_elapsed_seconds(baseline_log)

    rows = []
    for metrics_path in sorted(results_dir.glob(f"baseline_{benchmark}_{config}_w*_g*_metrics.csv")):
        parsed = parse_sample_file(metrics_path, benchmark, config)
        if parsed is None:
            continue

        warmup, granularity = parsed
        stem = metrics_path.name[: -len("_metrics.csv")]
        log_path = results_dir / f"{stem}_out.stdout"
        sim_time = read_driver_total_time(metrics_path)
        wall_seconds = parse_elapsed_seconds(log_path)
        error_pct = (sim_time - baseline_sim_time) / baseline_sim_time * 100
        wall_speedup = None
        if baseline_wall_seconds and wall_seconds and wall_seconds > 0:
            wall_speedup = baseline_wall_seconds / wall_seconds

        rows.append(
            {
                "warmup": warmup,
                "granularity": granularity,
                "sim_time": sim_time,
                "error_pct": error_pct,
                "abs_error_pct": abs(error_pct),
                "wall_seconds": wall_seconds,
                "wall_minutes": None if wall_seconds is None else wall_seconds / 60,
                "wall_speedup": wall_speedup,
            }
        )

    if not rows:
        raise ValueError(
            f"No {benchmark} {config} sweep metrics found in {results_dir}"
        )

    return baseline_sim_time, baseline_wall_seconds, rows


def pivot(rows, value_key):
    warmups = sorted({row["warmup"] for row in rows})
    granularities = sorted({row["granularity"] for row in rows})
    values = []
    row_by_key = {(row["warmup"], row["granularity"]): row for row in rows}
    for warmup in warmups:
        value_row = []
        for granularity in granularities:
            row = row_by_key.get((warmup, granularity))
            value_row.append(None if row is None else row[value_key])
        values.append(value_row)
    return warmups, granularities, values


def annotate_heatmap(ax, values, fmt, suffix=""):
    for y, row in enumerate(values):
        for x, value in enumerate(row):
            if value is None:
                continue
            ax.text(
                x,
                y,
                f"{value:{fmt}}{suffix}",
                ha="center",
                va="center",
                color="white" if abs(value) > 10 else "black",
                fontsize=8,
            )


def draw_heatmap(ax, rows, value_key, title, cbar_label, fmt, suffix=""):
    warmups, granularities, values = pivot(rows, value_key)
    masked = [[float("nan") if value is None else value for value in row] for row in values]
    image = ax.imshow(masked, aspect="auto", cmap="viridis")
    ax.set_title(title)
    ax.set_xlabel("granularity")
    ax.set_ylabel("warmup")
    ax.set_xticks(range(len(granularities)), granularities)
    ax.set_yticks(range(len(warmups)), warmups)
    annotate_heatmap(ax, values, fmt, suffix)
    cbar = plt.colorbar(image, ax=ax)
    cbar.set_label(cbar_label)


def draw_scatter(ax, rows):
    plotted = []
    for row in rows:
        if row["wall_speedup"] is None:
            continue
        plotted.append(row)

    if not plotted:
        ax.text(0.5, 0.5, "No wall-clock data", ha="center", va="center")
        ax.set_axis_off()
        return

    scatter = ax.scatter(
        [row["abs_error_pct"] for row in plotted],
        [row["wall_speedup"] for row in plotted],
        c=[row["granularity"] for row in plotted],
        s=[40 + row["warmup"] / 16 for row in plotted],
        cmap="plasma",
        edgecolors="black",
        linewidths=0.5,
    )

    for row in plotted:
        ax.annotate(
            f"w{row['warmup']} g{row['granularity']}",
            (row["abs_error_pct"], row["wall_speedup"]),
            textcoords="offset points",
            xytext=(4, 4),
            fontsize=7,
        )

    ax.axvline(5, color="red", linestyle="--", linewidth=1)
    ax.set_title("Accuracy / Wall-Clock Tradeoff")
    ax.set_xlabel("absolute simulated-time error vs baseline (%)")
    ax.set_ylabel("wall-clock speedup vs baseline")
    ax.grid(True, alpha=0.25)
    cbar = plt.colorbar(scatter, ax=ax)
    cbar.set_label("granularity")


def write_rows(path, rows):
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(
            f,
            fieldnames=[
                "warmup",
                "granularity",
                "sim_time",
                "error_pct",
                "abs_error_pct",
                "wall_seconds",
                "wall_minutes",
                "wall_speedup",
            ],
        )
        writer.writeheader()
        writer.writerows(rows)


def main():
    args = parse_args()
    results_dir = Path(args.results_dir).resolve()
    output_path = (
        Path(args.output).resolve()
        if args.output
        else results_dir / f"{args.benchmark}_{args.config}_tradeoff.png"
    )
    csv_path = (
        Path(args.csv).resolve()
        if args.csv
        else results_dir / f"{args.benchmark}_{args.config}_tradeoff.csv"
    )

    baseline_sim_time, baseline_wall_seconds, rows = collect_rows(
        results_dir, args.benchmark, args.config
    )
    write_rows(csv_path, rows)

    fig, axes = plt.subplots(1, 3, figsize=(18, 5.5), constrained_layout=True)
    draw_heatmap(
        axes[0],
        rows,
        "error_pct",
        "Simulated-Time Error",
        "error vs baseline (%)",
        "+.1f",
        "%",
    )
    draw_heatmap(
        axes[1],
        rows,
        "wall_minutes",
        "Wall-Clock Execution Time",
        "minutes",
        ".1f",
        "m",
    )
    draw_scatter(axes[2], rows)

    title = (
        f"{args.benchmark} {args.config}: warmup/granularity sweep\n"
        f"baseline simulated time={baseline_sim_time:.9f}s"
    )
    if baseline_wall_seconds:
        title += f", baseline wall time={baseline_wall_seconds / 60:.2f}m"
    fig.suptitle(title, fontsize=13)
    fig.savefig(output_path, dpi=180)

    print(f"Wrote plot: {output_path}")
    print(f"Wrote data: {csv_path}")


if __name__ == "__main__":
    main()
