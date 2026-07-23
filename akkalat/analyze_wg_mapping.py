#!/usr/bin/env python3
"""Audit runtime-only max-wg mapping evidence without changing scheduling."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
from pathlib import Path


CONFIGS = ("baseline", "m1", "m2", "m3", "complete")


def mapping_path(root: Path, benchmark: str, config: str) -> Path:
    return root / f"baseline_{benchmark}_{config}_metrics_wg_mapping.json"


def stdout_path(root: Path, benchmark: str, config: str) -> Path:
    return root / f"baseline_{benchmark}_{config}_out.stdout"


def stable_json_hash(value) -> str:
    encoded = json.dumps(
        value, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def partition_signature(report: dict) -> str:
    launches = [{
        "requested_total_wg": launch["requested_total_wg"],
        "unified": launch["unified"],
        "partitions": launch["partitions"],
        "wg_filter_kind": launch["wg_filter_kind"],
    } for launch in report["launches"]]
    return stable_json_hash(launches)


def launch_signature(report: dict) -> str:
    """Hash every launch descriptor field available in the passive trace.

    Packet addresses are not used as a substitute for kernel semantics, but
    matching them in addition to launch order, grid WG count, partition, and
    the executed WG set catches a changed dispatch sequence that the older
    partition-only signature could miss.
    """
    launches = [{
        "requested_total_wg": launch["requested_total_wg"],
        "unified": launch["unified"],
        "packet_addresses": launch.get("packet_addresses", []),
        "partitions": launch["partitions"],
        "wg_filter_kind": launch["wg_filter_kind"],
    } for launch in report["launches"]]
    return stable_json_hash(launches)


def per_gpu_signature(report: dict) -> str:
    return stable_json_hash([{
        "gpu_id": row["gpu_id"],
        "observed_wg_count": row["observed_wg_count"],
        "flattened_wg_id_min": row["flattened_wg_id_min"],
        "flattened_wg_id_max": row["flattened_wg_id_max"],
        "wg_set_sha256": row["wg_set_sha256"],
    } for row in report["per_gpu"]])


def termination_mode(report: dict) -> str:
    if report["max_wg"] == 0:
        return "natural_completion"
    expected = min(report["max_wg"], report["requested_total_wg"])
    if expected < report["max_wg"]:
        return "workload_completed_before_max_wg"
    return "runner_map_wg_observed_limit"


def validate_report(path: Path, report: dict) -> None:
    required = (
        "max_wg", "requested_total_wg", "observed_wg_count",
        "stop_reason", "global_wg_set_sha256", "launches", "per_gpu",
    )
    missing = [field for field in required if field not in report]
    if missing:
        raise ValueError(f"{path}: missing {', '.join(missing)}")
    if report["requested_total_wg"] <= 0:
        raise ValueError(f"{path}: requested count must be positive")
    full = report["max_wg"] == 0
    expected = (
        report["requested_total_wg"] if full
        else min(report["max_wg"], report["requested_total_wg"])
    )
    expected_reason = (
        "natural_completion" if full
        else "runner_map_wg_observed_limit"
    )
    if report["stop_reason"] != expected_reason:
        raise ValueError(f"{path}: wrong stop reason")
    if report["observed_wg_count"] != expected:
        raise ValueError(
            f"{path}: observed count {report['observed_wg_count']} does not "
            f"equal expected observed count {expected}"
        )
    if (full or expected < report["max_wg"]) and \
            report.get("stop_time_ns", 0) != 0:
        raise ValueError(f"{path}: natural completion has a stopper time")
    if full:
        for field in (
            "completed_wg_count", "executed_kernel_count",
            "observed_sampling_coverage", "completed_sampling_coverage",
        ):
            if field not in report:
                raise ValueError(f"{path}: missing {field}")
        if report["completed_wg_count"] != expected:
            raise ValueError(f"{path}: incomplete workgroup execution")
        if report["executed_kernel_count"] != len(report["launches"]):
            raise ValueError(f"{path}: executed-kernel count mismatch")
        if report["executed_kernel_count"] <= 0:
            raise ValueError(f"{path}: no executed kernels")
        if report["observed_sampling_coverage"] != 1.0 or \
                report["completed_sampling_coverage"] != 1.0:
            raise ValueError(f"{path}: incomplete sampling coverage")
    if report.get("max_wg_specific_wg_filter"):
        raise ValueError(f"{path}: max-wg-specific WGFilter is forbidden")
    if sum(row["observed_wg_count"] for row in report["per_gpu"]) != expected:
        raise ValueError(f"{path}: per-GPU counts do not sum to observed count")
    for launch in report["launches"]:
        requested = launch["requested_total_wg"]
        partitions = launch["partitions"]
        if not partitions or partitions[0]["begin"] != 0:
            raise ValueError(f"{path}: partition does not start at zero")
        previous = 0
        for partition in partitions:
            if partition["begin"] != previous or partition["end"] < previous:
                raise ValueError(f"{path}: non-contiguous partition")
            previous = partition["end"]
        if previous != requested:
            raise ValueError(f"{path}: partition does not cover full grid")
        expected_filter = (
            "original_unified_partition" if launch["unified"] else "none"
        )
        if launch["wg_filter_kind"] != expected_filter:
            raise ValueError(f"{path}: unexpected WGFilter kind")


def audit(
    root: Path,
    benchmarks: list[str],
    configs: tuple[str, ...] = CONFIGS,
    *,
    require_baseline_match: bool = False,
):
    rows = []
    for benchmark in benchmarks:
        reports = {}
        for config in configs:
            path = mapping_path(root, benchmark, config)
            if not path.is_file():
                raise ValueError(f"missing mapping report: {path}")
            report = json.loads(path.read_text(encoding="utf-8"))
            validate_report(path, report)
            stdout = stdout_path(root, benchmark, config)
            text = stdout.read_text(encoding="utf-8", errors="replace")
            for forbidden in (
                "limited from", "drained max-wg", "admitted_wg=",
            ):
                if forbidden in text:
                    raise ValueError(f"{stdout}: stale limiter text {forbidden!r}")
            reports[config] = report

        baseline = reports[configs[0]]
        baseline_partition = partition_signature(baseline)
        baseline_launch = launch_signature(baseline)
        baseline_per_gpu = per_gpu_signature(baseline)
        for config in configs:
            report = reports[config]
            launch_matches = launch_signature(report) == baseline_launch
            partition_matches = (
                partition_signature(report) == baseline_partition
            )
            wg_set_matches = (
                report["global_wg_set_sha256"]
                == baseline["global_wg_set_sha256"]
            )
            per_gpu_matches = (
                per_gpu_signature(report) == baseline_per_gpu
            )
            rows.append({
                "benchmark": benchmark,
                "configuration": config,
                "requested_total_wg": report["requested_total_wg"],
                "observed_wg_count": report["observed_wg_count"],
                "completed_wg_count": report.get("completed_wg_count"),
                "executed_kernel_count": report.get(
                    "executed_kernel_count", len(report["launches"])),
                "observed_sampling_coverage": report.get(
                    "observed_sampling_coverage"),
                "completed_sampling_coverage": report.get(
                    "completed_sampling_coverage"),
                "stop_reason": termination_mode(report),
                "report_stop_reason": report["stop_reason"],
                "launch_sha256": launch_signature(report),
                "launch_matches_baseline": launch_matches,
                "partition_sha256": partition_signature(report),
                "partition_matches_baseline": partition_matches,
                "global_wg_set_sha256": report["global_wg_set_sha256"],
                "wg_set_matches_baseline": wg_set_matches,
                "per_gpu_sha256": per_gpu_signature(report),
                "per_gpu_matches_baseline": per_gpu_matches,
                "strict_identity_matches_baseline": all((
                    launch_matches,
                    partition_matches,
                    wg_set_matches,
                    per_gpu_matches,
                )),
                "physical_gpus_observed": len(report["per_gpu"]),
                "stop_time_ns": report["stop_time_ns"],
                "any_wg_filter": report["any_wg_filter"],
                "max_wg_specific_wg_filter": report[
                    "max_wg_specific_wg_filter"
                ],
            })
    if require_baseline_match:
        mismatches = [
            f'{row["benchmark"]}/{row["configuration"]}'
            for row in rows
            if not row["strict_identity_matches_baseline"]
        ]
        if mismatches:
            raise ValueError(
                "formal workload identity differs from Baseline: "
                + ", ".join(mismatches)
            )
    return rows


def write_outputs(root: Path, rows: list[dict]) -> None:
    csv_path = root / "cupath_wg_mapping_audit.csv"
    with csv_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)

    differing_sets = [
        row for row in rows if not row["wg_set_matches_baseline"]
    ]
    differing_launches = [
        row for row in rows if not row["launch_matches_baseline"]
    ]
    differing_per_gpu = [
        row for row in rows if not row["per_gpu_matches_baseline"]
    ]
    lines = [
        "# CuPath WG mapping audit",
        "",
        f"Audited {len(rows)} cells. Every cell records the original full-grid "
        "partition and exact MapWGReq workgroup set; full runs also prove "
        "natural completion and 100% observed/completed coverage.",
        "",
        f"Launch-descriptor differences from Baseline: "
        f"{len(differing_launches)}.",
        f"Observed WG-set differences from Baseline: {len(differing_sets)}.",
        f"Per-GPU mapping differences from Baseline: {len(differing_per_gpu)}.",
        "These differences are reported rather than repaired with a quota or "
        "pre-dispatch filter.",
    ]
    (root / "CUPATH_WG_MAPPING_AUDIT.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--benchmarks", required=True)
    parser.add_argument("--configs", default=",".join(CONFIGS))
    parser.add_argument(
        "--require-baseline-match", action="store_true",
        help=(
            "reject any launch, partition, global-WG, or per-GPU WG-set "
            "identity difference from the first configuration"
        ),
    )
    args = parser.parse_args()
    root = args.results.resolve()
    benchmarks = [item.strip() for item in args.benchmarks.split(",") if item.strip()]
    configs = tuple(item.strip() for item in args.configs.split(",") if item.strip())
    rows = audit(
        root, benchmarks, configs,
        require_baseline_match=args.require_baseline_match,
    )
    write_outputs(root, rows)
    print(root / "cupath_wg_mapping_audit.csv")


if __name__ == "__main__":
    main()
