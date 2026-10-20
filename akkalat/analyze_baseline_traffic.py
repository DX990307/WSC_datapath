#!/usr/bin/env python3
"""Create the pre-registered CuPath V6 baseline local/remote classification."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

from analyze_wg_mapping import audit as audit_wg_mapping
from analyze_cupath_runtime_screen import (
    MECHANISM_FIELDS,
    MECHANISMS,
    REQUIRED_FLAGS,
)
from plot_cupath_typed_ablation import WORKLOADS


LOCAL_DOMINANT_MAX = 0.25
MIXED_MAX = 0.75


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def audit_profile_protocol(
    root: Path,
    expected_workers: int = 14,
    expected_sha256: str | None = None,
    expected_memory_reserve_gib: float | None = None,
    expected_memory_per_worker_gib: float | None = None,
) -> tuple[str, str]:
    """Reject provenance profiles that are not the frozen Baseline grid."""
    metadata = json.loads(
        (root / "EXPERIMENT_METADATA.json").read_text(encoding="utf-8")
    )
    manifest = json.loads(
        (root / "EXPERIMENT_BINARIES.json").read_text(encoding="utf-8")
    )
    experiments = metadata.get("experiments", [])
    expected_benchmarks = {name for name, _, _ in WORKLOADS}
    observed = {
        (experiment.get("benchmark"), experiment.get("configuration"))
        for experiment in experiments
    }
    expected = {(benchmark, "baseline") for benchmark in expected_benchmarks}
    if (
        metadata.get("experiment_count") != len(expected)
        or len(experiments) != len(expected)
        or observed != expected
    ):
        raise SystemExit(
            f"Baseline provenance grid mismatch: {len(observed)}/14 cells"
        )
    launcher = metadata.get("launcher", {})
    if launcher.get("max_workers") != expected_workers:
        raise SystemExit(
            "Baseline provenance worker mismatch: "
            f"expected={expected_workers}, launcher={launcher!r}"
        )
    if expected_memory_reserve_gib is not None:
        if launcher.get("memory_reserve_gib") != expected_memory_reserve_gib:
            raise SystemExit(
                "Baseline provenance memory-reserve mismatch: "
                f"expected={expected_memory_reserve_gib}, launcher={launcher!r}"
            )
        if launcher.get("memory_per_worker_gib") != expected_memory_per_worker_gib:
            raise SystemExit(
                "Baseline provenance per-worker memory budget mismatch: "
                f"expected={expected_memory_per_worker_gib}, launcher={launcher!r}"
            )
        if launcher.get("memory_worker_cap", 0) < expected_workers:
            raise SystemExit(
                "Baseline provenance worker count exceeds recorded memory cap: "
                f"launcher={launcher!r}"
            )
        if launcher.get("mem_available_gib_at_launch", 0) <= expected_memory_reserve_gib:
            raise SystemExit(
                "Baseline provenance launch did not retain the recorded memory reserve: "
                f"launcher={launcher!r}"
            )

    expected_mechanisms = {
        f"-{field}={str(value).lower()}"
        for field, value in zip(MECHANISM_FIELDS, MECHANISMS["baseline"])
    }
    binaries: set[str] = set()
    for experiment in experiments:
        benchmark = experiment["benchmark"]
        if (
            experiment.get("configuration") != "baseline"
            or experiment.get("target") != "baseline"
        ):
            raise SystemExit(f"Baseline/{benchmark} profile identity mismatch")
        command = experiment.get("command", [])
        if not command:
            raise SystemExit(f"empty command for Baseline/{benchmark}")
        command_flags = command[1:]
        flags = set(command_flags)
        if len(flags) != len(command_flags):
            raise SystemExit(f"Baseline/{benchmark} contains duplicate flags")
        missing = sorted(
            (REQUIRED_FLAGS | expected_mechanisms | {"-trace-remote-origin"})
            - flags
        )
        if missing:
            raise SystemExit(
                f"Baseline/{benchmark} missing profile flags: "
                + ", ".join(missing)
            )
        expected_prefix = (
            root / f"baseline_{benchmark}_baseline_remote_origin"
        ).resolve()
        expected_metric = (
            root / f"baseline_{benchmark}_baseline_metrics"
        ).resolve()
        expected_dynamic = {
            f"-benchmark={benchmark}",
            f"-metric-file-name={expected_metric}",
            "-trace-remote-origin",
            f"-trace-remote-origin-file={expected_prefix}",
            "-trace-remote-origin-max-records=100000",
        }
        if not expected_dynamic <= flags:
            raise SystemExit(
                f"Baseline/{benchmark} remote-origin destination mismatch"
            )
        expected_flags = REQUIRED_FLAGS | expected_mechanisms | expected_dynamic
        unexpected = sorted(flags - expected_flags)
        if unexpected:
            raise SystemExit(
                f"Baseline/{benchmark} contains unexpected profile flags: "
                + ", ".join(unexpected)
            )
        binaries.add(command[0])

    if len(binaries) != 1:
        raise SystemExit(
            f"Baseline provenance mixes binaries: {sorted(binaries)}"
        )
    binary = Path(next(iter(binaries)))
    recorded = manifest.get("sha256_by_target", {}).get("baseline")
    actual = sha256(binary)
    if recorded != actual:
        raise SystemExit(
            f"Baseline provenance binary mismatch: {recorded} != {actual}"
        )
    if expected_sha256 is not None and actual != expected_sha256:
        raise SystemExit(
            "unexpected Baseline provenance binary hash: "
            f"expected={expected_sha256}, actual={actual}"
        )
    return str(binary), actual


def traffic_class(remote_fraction: float) -> str:
    if remote_fraction == 0:
        return "Exact-local"
    if remote_fraction <= LOCAL_DOMINANT_MAX:
        return "Local-dominant"
    if remote_fraction <= MIXED_MAX:
        return "Mixed"
    return "Remote-dominant"


def summary_path(root: Path, benchmark: str) -> Path:
    return root / f"baseline_{benchmark}_baseline_remote_origin_summary.csv"


def audit_completed_result(root: Path, benchmark: str) -> None:
    path = root / f"baseline_{benchmark}_baseline_result.json"
    if not path.is_file():
        raise ValueError(f"missing Baseline result JSON: {path}")
    result = json.loads(path.read_text(encoding="utf-8"))
    if (
        result.get("success") is not True
        or result.get("returncode") != 0
        or result.get("simulator_returncode") != 0
    ):
        raise ValueError(f"failed Baseline provenance result: {path}: {result}")
    if (
        result.get("benchmark") != benchmark
        or result.get("configuration") != "baseline"
        or result.get("target") != "baseline"
    ):
        raise ValueError(f"Baseline provenance result identity mismatch: {path}")
    metrics = (
        root / f"baseline_{benchmark}_baseline_metrics.csv"
    ).resolve()
    recorded_metrics = result.get("metrics")
    if (
        not isinstance(recorded_metrics, str)
        or Path(recorded_metrics).resolve() != metrics
        or not metrics.is_file()
    ):
        raise ValueError(f"Baseline provenance metric artifact mismatch: {path}")
    mapping = result.get("wg_mapping")
    mapping_path = (
        root / f"baseline_{benchmark}_baseline_metrics_wg_mapping.json"
    ).resolve()
    recorded_mapping = mapping.get("path") if isinstance(mapping, dict) else None
    if (
        not isinstance(recorded_mapping, str)
        or Path(recorded_mapping).resolve() != mapping_path
        or not mapping_path.is_file()
    ):
        raise ValueError(f"Baseline provenance mapping artifact mismatch: {path}")


def analyze(root: Path, workloads=WORKLOADS) -> list[dict[str, object]]:
    benchmarks = [benchmark for benchmark, _, _ in workloads]
    audit_wg_mapping(root, benchmarks, ("baseline",))
    rows: list[dict[str, object]] = []
    for benchmark, label, _ in workloads:
        audit_completed_result(root, benchmark)
        path = summary_path(root, benchmark)
        if not path.is_file():
            raise ValueError(f"missing Baseline traffic profile: {path}")
        total_requests = 0
        remote_requests = 0
        total_bytes = 0
        remote_bytes = 0
        read_requests = 0
        write_requests = 0
        with path.open(newline="", encoding="utf-8") as stream:
            for record in csv.DictReader(stream):
                requests = int(record["requests"])
                byte_count = int(record["bytes"])
                total_requests += requests
                total_bytes += byte_count
                if record["operation"] == "read":
                    read_requests += requests
                elif record["operation"] == "write":
                    write_requests += requests
                if record["is_remote"].lower() == "true":
                    remote_requests += requests
                    remote_bytes += byte_count
        if total_requests == 0:
            raise ValueError(
                f"Baseline traffic profile contains no data demand: {path}"
            )
        fraction = remote_requests / total_requests
        rows.append({
            "benchmark": benchmark,
            "label": label,
            "total_requests": total_requests,
            "local_requests": total_requests - remote_requests,
            "remote_requests": remote_requests,
            "remote_fraction": fraction,
            "remote_percent": 100.0 * fraction,
            "read_requests": read_requests,
            "write_requests": write_requests,
            "total_bytes": total_bytes,
            "remote_bytes": remote_bytes,
            "traffic_class": traffic_class(fraction),
        })
    return rows


def write_outputs(output: Path, rows: list[dict[str, object]]) -> None:
    output.mkdir(parents=True, exist_ok=True)
    table = output / "cupath_baseline_traffic_classification.csv"
    with table.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)

    ordered = sorted(rows, key=lambda row: float(row["remote_fraction"]))
    cdf = output / "cupath_baseline_remote_fraction_cdf.csv"
    with cdf.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow(["rank", "label", "remote_fraction", "cdf"])
        for index, row in enumerate(ordered, 1):
            writer.writerow([
                index, row["label"], row["remote_fraction"],
                index / len(ordered),
            ])

    fig, (ax0, ax1) = plt.subplots(1, 2, figsize=(3.45, 1.42))
    x = np.arange(len(rows))
    values = [100.0 * float(row["remote_fraction"]) for row in rows]
    ax0.bar(x, values, color="#83CEE2", edgecolor="#5ABED8", linewidth=0.4)
    ax0.set_xticks(x)
    ax0.set_xticklabels(
        [str(row["label"]) for row in rows], rotation=48, ha="right", fontsize=4.5
    )
    ax0.set_ylabel("Remote requests (%)", fontsize=5.7)
    ax0.tick_params(axis="y", labelsize=4.8, length=1.5)
    cdf_x = [100.0 * float(row["remote_fraction"]) for row in ordered]
    cdf_y = np.arange(1, len(ordered) + 1) / len(ordered)
    ax1.step(cdf_x, cdf_y, where="post", color="#ED6612", linewidth=1.05)
    ax1.scatter(cdf_x, cdf_y, s=5, color="#F4A371", zorder=3)
    ax1.set_xlabel("Remote requests (%)", fontsize=5.7)
    ax1.set_ylabel("Workloads (CDF)", fontsize=5.7)
    ax1.tick_params(labelsize=4.8, length=1.5)
    for ax in (ax0, ax1):
        ax.grid(axis="y", color="#E5E5E6", linewidth=0.35, zorder=0)
        for spine in ax.spines.values():
            spine.set_visible(True)
            spine.set_color("#656667")
            spine.set_linewidth(0.5)
    fig.tight_layout(pad=0.25, w_pad=0.7)
    fig.savefig(
        output / "cupath_baseline_remote_fraction.png",
        dpi=300, bbox_inches="tight", pad_inches=0.025,
        facecolor="white", transparent=False,
    )
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--expected-workers", type=int, default=14)
    parser.add_argument("--expected-sha256")
    parser.add_argument("--expected-memory-reserve-gib", type=float)
    parser.add_argument("--expected-memory-per-worker-gib", type=float)
    parser.add_argument(
        "--benchmarks",
        help="Optional comma-separated subset; default is all paper workloads.",
    )
    args = parser.parse_args()
    root = args.results.resolve()
    output = (args.output_dir or root).resolve()
    audit_profile_protocol(
        root,
        expected_workers=args.expected_workers,
        expected_sha256=args.expected_sha256,
        expected_memory_reserve_gib=args.expected_memory_reserve_gib,
        expected_memory_per_worker_gib=args.expected_memory_per_worker_gib,
    )
    workloads = WORKLOADS
    if args.benchmarks:
        requested = {
            item.strip() for item in args.benchmarks.split(",") if item.strip()
        }
        unknown = requested - {benchmark for benchmark, _, _ in WORKLOADS}
        if unknown:
            raise SystemExit("unknown benchmarks: " + ", ".join(sorted(unknown)))
        workloads = tuple(
            item for item in WORKLOADS if item[0] in requested
        )
    rows = analyze(root, workloads)
    write_outputs(output, rows)
    print(output / "cupath_baseline_traffic_classification.csv")


if __name__ == "__main__":
    main()
