#!/usr/bin/env python3
"""Plot exact-state-verified Cuckoo Filter bypass effectiveness.

The input must be one complete, authoritative-audit campaign.  Older
campaigns contain only Filter decisions and are intentionally rejected.
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch

from plot_cupath_typed_ablation import read_metrics


WORKLOADS = (
    ("aes", "AES"),
    ("bitonicsort", "BT"),
    ("fastwalshtransform", "FWT"),
    ("fft", "FFT"),
    ("fir", "FIR"),
    ("floydwarshall", "FWS"),
    ("im2col", "I2C"),
    ("kmeans", "KM"),
    ("matrixmultiplication", "MM"),
    ("matrixtranspose", "MT"),
    ("pagerank", "PR"),
    ("relu", "RELU"),
    ("simpleconvolution", "SC"),
    ("spmv", "SPMV"),
)

REQUIRED_COUNTERS = (
    "l2_resident_filter_authoritative_audit_enabled",
    "l2_resident_filter_read_filter_eligible",
    "l2_resident_filter_read_issued_bypasses",
    "l2_resident_filter_read_exact_tag_lookups",
    "l2_resident_filter_read_authoritative_checks",
    "l2_resident_filter_read_verified_safe_bypasses",
    "l2_resident_filter_read_authoritative_false_negatives",
    "l2_resident_filter_read_authoritative_mshr_hits",
    "remote_authoritative_audit_enabled",
    "remote_inflight_filter_negatives",
    "remote_pending_authoritative_checks",
    "remote_pending_verified_safe_bypasses",
    "remote_pending_authoritative_false_negatives",
    "remote_exact_table_lookups",
    "remote_requester_l2_authoritative_checks",
    "remote_requester_l2_verified_safe_bypasses",
    "remote_requester_l2_authoritative_false_negatives",
    "remote_requester_l2_authoritative_unavailable",
    "remote_l2_probe_hits",
    "remote_l2_probe_misses",
)

BLUE_300 = "#83CEE2"
ORANGE_200 = "#F8C2A0"
ORANGE_400 = "#F18541"
RED_500 = "#D6295D"
DARK = "#323334"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "result_dir",
        type=Path,
        help="Authoritative-audit campaign containing all Complete metrics.",
    )
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def value(metrics: dict[str, float], field: str, path: Path) -> float:
    if field not in metrics:
        raise ValueError(
            f"{path} lacks authoritative counter {field}. "
            "This is an old or unverified campaign; rerun Complete with "
            "-typed-filter-authoritative-audit=true."
        )
    result = metrics[field]
    if not math.isfinite(result) or result < 0:
        raise ValueError(f"invalid {field} in {path}: {result}")
    return result


def require_equal(lhs: float, rhs: float, relation: str, path: Path) -> None:
    if not math.isclose(lhs, rhs, rel_tol=0.0, abs_tol=1e-9):
        raise ValueError(
            f"authoritative counter invariant failed in {path}: "
            f"{relation} ({lhs:g} != {rhs:g})"
        )


def rate(numerator: float, denominator: float) -> float | None:
    if denominator == 0:
        if numerator != 0:
            raise ValueError("nonzero verified-safe numerator with zero opportunities")
        return None
    if numerator > denominator + 1e-9:
        raise ValueError(
            f"verified-safe numerator {numerator:g} exceeds "
            f"opportunities {denominator:g}"
        )
    return 100.0 * numerator / denominator


def audit_campaign_provenance(root: Path) -> str:
    binaries_path = root / "EXPERIMENT_BINARIES.json"
    metadata_path = root / "EXPERIMENT_METADATA.json"
    if not binaries_path.is_file() or not metadata_path.is_file():
        raise ValueError("campaign provenance files are missing")

    binaries = json.loads(binaries_path.read_text(encoding="utf-8"))
    hashes = set(binaries.get("sha256_by_target", {}).values())
    if len(hashes) != 1:
        raise ValueError(f"Complete campaign does not use one frozen binary: {hashes}")

    metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    complete = [
        item
        for item in metadata.get("experiments", [])
        if item.get("configuration") == "complete"
    ]
    expected = {name for name, _ in WORKLOADS}
    observed = {str(item.get("benchmark")) for item in complete}
    if len(complete) != len(expected) or observed != expected:
        raise ValueError("metadata does not describe exactly 14 Complete workloads")
    if any(item.get("target") not in binaries["sha256_by_target"] for item in complete):
        raise ValueError("a Complete cell does not reference the frozen binary manifest")
    return hashes.pop()


def load_workload(path: Path, benchmark: str, label: str) -> dict[str, float | str]:
    metrics, observed_values = read_metrics(path)
    for field in REQUIRED_COUNTERS:
        value(metrics, field, path)

    local_audit_values = observed_values.get(
        "l2_resident_filter_authoritative_audit_enabled", set()
    )
    if local_audit_values != {1.0}:
        raise ValueError(
            f"authoritative L2 audit was not enabled at every cache in {path}: "
            f"{sorted(local_audit_values)}"
        )
    remote_audit_values = observed_values.get(
        "remote_authoritative_audit_enabled", set()
    )
    if remote_audit_values != {1.0}:
        raise ValueError(
            f"authoritative RDMA audit was not enabled at every endpoint in {path}: "
            f"{sorted(remote_audit_values)}"
        )

    local_eligible = value(
        metrics, "l2_resident_filter_read_filter_eligible", path
    )
    local_issued = value(
        metrics, "l2_resident_filter_read_issued_bypasses", path
    )
    local_exact = value(
        metrics, "l2_resident_filter_read_exact_tag_lookups", path
    )
    local_checks = value(
        metrics, "l2_resident_filter_read_authoritative_checks", path
    )
    local_safe = value(
        metrics, "l2_resident_filter_read_verified_safe_bypasses", path
    )
    local_fn = value(
        metrics, "l2_resident_filter_read_authoritative_false_negatives", path
    )
    local_mshr = value(
        metrics, "l2_resident_filter_read_authoritative_mshr_hits", path
    )
    require_equal(
        local_eligible,
        local_issued + local_exact + local_mshr,
        "local eligible = issued bypasses + exact tag lookups + MSHR hits",
        path,
    )
    require_equal(
        local_checks,
        local_safe + local_fn + local_mshr,
        "local authoritative checks = verified safe + false negatives + MSHR hits",
        path,
    )
    require_equal(
        local_issued,
        local_safe,
        "local issued bypasses = verified-safe bypasses",
        path,
    )

    pending_checks = value(
        metrics, "remote_pending_authoritative_checks", path
    )
    pending_safe = value(
        metrics, "remote_pending_verified_safe_bypasses", path
    )
    pending_fn = value(
        metrics, "remote_pending_authoritative_false_negatives", path
    )
    pending_negatives = value(metrics, "remote_inflight_filter_negatives", path)
    pending_exact = value(metrics, "remote_exact_table_lookups", path)
    require_equal(
        pending_checks,
        pending_safe + pending_fn,
        "RDMA checks = verified safe + false negatives",
        path,
    )
    require_equal(
        pending_negatives,
        pending_checks,
        "RDMA Filter negatives = authoritative checks",
        path,
    )

    requester_checks = value(
        metrics, "remote_requester_l2_authoritative_checks", path
    )
    requester_safe = value(
        metrics, "remote_requester_l2_verified_safe_bypasses", path
    )
    requester_fn = value(
        metrics, "remote_requester_l2_authoritative_false_negatives", path
    )
    requester_unavailable = value(
        metrics, "remote_requester_l2_authoritative_unavailable", path
    )
    requester_hits = value(metrics, "remote_l2_probe_hits", path)
    requester_misses = value(metrics, "remote_l2_probe_misses", path)
    require_equal(
        requester_checks,
        requester_safe + requester_fn,
        "requester-L2 checks = verified safe + false negatives",
        path,
    )
    if requester_unavailable != 0:
        raise ValueError(
            f"authoritative requester-L2 state was unavailable "
            f"{requester_unavailable:g} times in {path}"
        )

    return {
        "benchmark": benchmark,
        "label": label,
        "local_safe": local_safe,
        "local_opportunities": local_eligible,
        "local_false_negatives": local_fn,
        "local_mshr_races": local_mshr,
        "pending_safe": pending_safe,
        "pending_opportunities": pending_safe + pending_exact,
        "pending_false_negatives": pending_fn,
        "requester_safe": requester_safe,
        "requester_opportunities": requester_safe + requester_hits + requester_misses,
        "requester_false_negatives": requester_fn,
        "authoritative_false_negatives": local_fn + pending_fn + requester_fn,
    }


def load_campaign(root: Path) -> list[dict[str, float | str]]:
    rows: list[dict[str, float | str]] = []
    for benchmark, label in WORKLOADS:
        path = root / f"baseline_{benchmark}_complete_metrics.csv"
        if not path.is_file():
            raise ValueError(f"incomplete Complete campaign; missing {path}")
        rows.append(load_workload(path, benchmark, label))
    return rows


def count_weighted_row(rows: list[dict[str, float | str]]) -> dict[str, float | str]:
    output: dict[str, float | str] = {"benchmark": "all", "label": "ALL"}
    for stage in ("local", "pending", "requester"):
        output[f"{stage}_safe"] = sum(float(row[f"{stage}_safe"]) for row in rows)
        output[f"{stage}_opportunities"] = sum(
            float(row[f"{stage}_opportunities"]) for row in rows
        )
        output[f"{stage}_false_negatives"] = sum(
            float(row[f"{stage}_false_negatives"]) for row in rows
        )
    output["local_mshr_races"] = sum(float(row["local_mshr_races"]) for row in rows)
    output["authoritative_false_negatives"] = sum(
        float(row["authoritative_false_negatives"]) for row in rows
    )
    return output


def add_rates(row: dict[str, float | str]) -> dict[str, float | str]:
    output = dict(row)
    for stage in ("local", "pending", "requester"):
        stage_rate = rate(
            float(row[f"{stage}_safe"]),
            float(row[f"{stage}_opportunities"]),
        )
        output[f"{stage}_verified_safe_rate_pct"] = (
            "" if stage_rate is None else stage_rate
        )
    return output


def write_csv(
    rows: list[dict[str, float | str]],
    all_row: dict[str, float | str],
    path: Path,
    campaign: str,
    binary_sha256: str,
) -> None:
    output = []
    for row in rows + [all_row]:
        enriched = add_rates(row)
        enriched["source_campaign"] = campaign
        enriched["binary_sha256"] = binary_sha256
        output.append(enriched)
    fieldnames = (
        "source_campaign",
        "binary_sha256",
        "benchmark",
        "label",
        "local_safe",
        "local_opportunities",
        "local_verified_safe_rate_pct",
        "local_false_negatives",
        "local_mshr_races",
        "pending_safe",
        "pending_opportunities",
        "pending_verified_safe_rate_pct",
        "pending_false_negatives",
        "requester_safe",
        "requester_opportunities",
        "requester_verified_safe_rate_pct",
        "requester_false_negatives",
        "authoritative_false_negatives",
    )
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(output)


def compact_count(number: float) -> str:
    if number >= 1_000_000:
        return f"{number / 1_000_000:.2f}M"
    if number >= 1_000:
        return f"{number / 1_000:.1f}K"
    return f"{number:.0f}"


def plot(
    rows: list[dict[str, float | str]],
    all_row: dict[str, float | str],
    output: Path,
    dpi: int,
) -> None:
    """Draw a paper-sized grouped bar chart with a count-weighted ALL group."""
    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.7,
        }
    )
    series = (
        ("Local L2 tags", BLUE_300, "local"),
        ("Requester RDMA table", ORANGE_200, "pending"),
        ("Requester L2 tags", ORANGE_400, "requester"),
    )
    plotted_rows = rows + [all_row]
    labels = [str(row["label"]) for row in plotted_rows]
    x_step = 0.82
    xs = [index * x_step for index in range(len(plotted_rows))]
    group_width = 0.62
    bar_width = group_width / len(series)

    fig, ax = plt.subplots(figsize=(7.0, 2.45))
    all_x = xs[-1]
    ax.axvspan(all_x - 0.38, all_x + 0.38, color="#F5F5F5", zorder=0)
    false_negative_labels: list[tuple[float, float, float]] = []

    for series_index, (legend, color, stage) in enumerate(series):
        values = []
        edgecolors = []
        linewidths = []
        offset = -group_width / 2 + bar_width / 2 + series_index * bar_width
        positions = [x + offset for x in xs]
        for position, row in zip(positions, plotted_rows):
            stage_rate = rate(
                float(row[f"{stage}_safe"]),
                float(row[f"{stage}_opportunities"]),
            )
            values.append(math.nan if stage_rate is None else stage_rate)
            false_negatives = float(row[f"{stage}_false_negatives"])
            edgecolors.append(RED_500 if false_negatives else "none")
            linewidths.append(1.0 if false_negatives else 0.0)
            if false_negatives and stage_rate is not None:
                false_negative_labels.append(
                    (position, min(stage_rate + 2.0, 98.0), false_negatives)
                )
        ax.bar(
            positions,
            values,
            width=bar_width * 0.94,
            color=color,
            edgecolor=edgecolors,
            linewidth=linewidths,
            zorder=3,
        )

    for x, y, false_negatives in false_negative_labels:
        ax.text(
            x,
            y,
            f"FN={compact_count(false_negatives)}",
            ha="center",
            va="bottom",
            rotation=90,
            fontsize=5.5,
            fontweight="bold",
            color=RED_500,
            zorder=4,
        )

    total_false_negatives = float(all_row["authoritative_false_negatives"])
    if total_false_negatives == 0:
        ax.text(
            0.99,
            0.955,
            "Authoritative false negatives = 0",
            transform=ax.transAxes,
            ha="right",
            va="top",
            fontsize=6.4,
            color=DARK,
        )

    ax.axvline(all_x - x_step / 2, color="#888888", linestyle=":", linewidth=0.7)
    ax.set_ylim(0, 105)
    ax.set_yticks((0, 20, 40, 60, 80, 100))
    ax.set_ylabel("Verified-safe bypasses\n(% of exact-path opportunities)", fontsize=7.8)
    ax.set_xticks(xs, labels)
    ax.set_xlim(xs[0] - group_width * 0.62, xs[-1] + group_width * 0.62)
    ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=1)
    ax.tick_params(axis="y", labelsize=7.0, length=2.2, width=0.6)
    ax.tick_params(axis="x", labelsize=7.0, length=0, pad=3)
    for tick, label in zip(ax.get_xticklabels(), labels):
        tick.set_rotation(30)
        tick.set_ha("right")
        tick.set_rotation_mode("anchor")
        if label == "ALL":
            tick.set_fontweight("bold")
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_color("#656667")
        spine.set_linewidth(0.65)

    handles = [
        Patch(facecolor=color, edgecolor="none", label=legend)
        for legend, color, _ in series
    ]
    if total_false_negatives:
        handles.append(
            Patch(
                facecolor="white",
                edgecolor=RED_500,
                linewidth=1.0,
                label="Authoritative false negative",
            )
        )
    ax.legend(
        handles=handles,
        loc="lower center",
        bbox_to_anchor=(0.0, 1.02, 1.0, 0.08),
        mode="expand",
        ncol=len(handles),
        frameon=False,
        borderaxespad=0,
        columnspacing=0.7,
        handlelength=1.0,
        handletextpad=0.4,
        fontsize=7.0 if len(handles) == 3 else 6.2,
    )
    fig.subplots_adjust(left=0.105, right=0.995, top=0.84, bottom=0.22)
    fig.savefig(output.with_suffix(".png"), dpi=dpi, bbox_inches="tight", pad_inches=0.025)
    fig.savefig(output.with_suffix(".pdf"), bbox_inches="tight", pad_inches=0.025)
    plt.close(fig)


def main() -> None:
    args = parse_args()
    root = args.result_dir.resolve()
    output_dir = (args.output_dir or root).resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    binary_sha256 = audit_campaign_provenance(root)
    rows = load_campaign(root)
    all_row = count_weighted_row(rows)
    output = output_dir / "cupath_filter_effectiveness_by_benchmark"
    csv_path = output.with_suffix(".csv")
    write_csv(rows, all_row, csv_path, root.name, binary_sha256)
    plot(rows, all_row, output, args.dpi)

    print(csv_path)
    print(output.with_suffix(".png"))
    print(output.with_suffix(".pdf"))
    total_false_negatives = int(float(all_row["authoritative_false_negatives"]))
    print(f"authoritative false negatives: {total_false_negatives}")


if __name__ == "__main__":
    main()
