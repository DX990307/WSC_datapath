#!/usr/bin/env python3
"""Strictly audit the formal 14-workload M1/M2/M3 rerun campaign."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
from datetime import datetime
from pathlib import Path

from audit_baseline_complete_campaign import (
    COMMON_FLAGS,
    audit_passive_wg_stopper_source,
    audit_wg_completion,
)
from analyze_wg_mapping import (
    launch_signature,
    partition_signature,
    per_gpu_signature,
    validate_report,
)
from plot_cupath_typed_ablation import (
    REQUIRED_M1_METRICS,
    REQUIRED_M2_METRICS,
    REQUIRED_M3_METRICS,
    WORKLOADS,
    read_metrics,
)


CONFIGS = ("m1", "m2", "m3")
EXPECTED_CELLS = {
    (benchmark, config)
    for benchmark, _label, _group in WORKLOADS
    for config in CONFIGS
}


def mechanism_flags(
    *, adaptive_pair: bool, remote: bool, dedup: bool,
    batching: bool, requester_l2: bool, remote_prefetch: bool,
) -> set[str]:
    return {
        "-l2-resident-filter-enable=false",
        "-l2-fill-forwarding-enable=false",
        "-dram-row-continuation-enable=false",
        "-l2-filter-prefetch-enable=false",
        "-l2-prefetch-predictor-only=false",
        "-l2-prefetch-ungated=false",
        "-l2-granularity-adaptation-enable=false",
        f"-l2-adaptive-pair-enable={str(adaptive_pair).lower()}",
        "-l2-granularity-without-filter=false",
        "-l2-granularity-always-expand=false",
        "-l2-granularity-predictor-only=false",
        f"-remote-data-path-enable={str(remote).lower()}",
        f"-remote-data-path-dedup-enable={str(dedup).lower()}",
        f"-remote-data-path-batching-enable={str(batching).lower()}",
        f"-remote-data-path-l2-enable={str(requester_l2).lower()}",
        f"-remote-filter-prefetch-enable={str(remote_prefetch).lower()}",
    }


MECHANISM_FLAGS = {
    "m1": mechanism_flags(
        adaptive_pair=True, remote=False, dedup=False, batching=False,
        requester_l2=False, remote_prefetch=False,
    ),
    "m2": mechanism_flags(
        adaptive_pair=False, remote=True, dedup=True, batching=True,
        requester_l2=False, remote_prefetch=True,
    ),
    "m3": mechanism_flags(
        adaptive_pair=False, remote=True, dedup=False, batching=False,
        requester_l2=True, remote_prefetch=False,
    ),
}
REQUIRED_METRICS = {
    "m1": REQUIRED_M1_METRICS,
    "m2": REQUIRED_M2_METRICS,
    "m3": REQUIRED_M3_METRICS,
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def audit_metadata(root: Path) -> tuple[Path, str]:
    metadata = load_json(root / "EXPERIMENT_METADATA.json")
    manifest = load_json(root / "EXPERIMENT_BINARIES.json")
    experiments = metadata.get("experiments", [])
    if metadata.get("experiment_count") != 42 or len(experiments) != 42:
        raise ValueError(f"metadata contains {len(experiments)}/42 commands")
    launcher = metadata.get("launcher", {})
    expected_launcher = {
        "max_workers": 14,
        "requested_max_workers": 14,
        "initial_workers": 1,
        "dynamic_memory_admission": True,
        "memory_reserve_gib": 50.0,
        "memory_per_worker_gib": 0.0,
        "memory_scan_minutes": 30.0,
        "memory_worker_cap": 14,
        "skip_build": True,
    }
    for field, expected in expected_launcher.items():
        if launcher.get(field) != expected:
            raise ValueError(
                f"launcher {field}={launcher.get(field)!r}, expected {expected!r}"
            )
    if float(launcher.get("mem_available_gib_at_launch", 0)) <= 50.0:
        raise ValueError("campaign began without the 50 GiB reserve")

    cells = set()
    binaries = set()
    for experiment in experiments:
        cell = (experiment.get("benchmark"), experiment.get("configuration"))
        if cell not in EXPECTED_CELLS or cell in cells:
            raise ValueError(f"unexpected or duplicate metadata cell {cell}")
        cells.add(cell)
        command = experiment.get("command", [])
        if not command:
            raise ValueError(f"empty command for {cell}")
        binaries.add(command[0])
        tokens = command[1:]
        token_set = set(tokens)
        if len(tokens) != len(token_set):
            raise ValueError(f"duplicate flags in {cell}")
        benchmark, config = cell
        missing = sorted((COMMON_FLAGS | MECHANISM_FLAGS[config]) - token_set)
        if missing:
            raise ValueError(f"{cell} missing flags: {missing}")
        if f"-benchmark={benchmark}" not in token_set:
            raise ValueError(f"benchmark flag mismatch for {cell}")
        expected_metric = (
            root / f"baseline_{benchmark}_{config}_metrics"
        ).resolve()
        if f"-metric-file-name={expected_metric}" not in token_set:
            raise ValueError(f"metric path mismatch for {cell}")
        forbidden = (
            "-trace-observation", "-sampled", "-branch-sampled",
            "-kernel-sampled", "-loop-sampled", "-force-local-data-access",
        )
        if any(
            token == flag or token.startswith(flag + "=")
            for token in token_set for flag in forbidden
        ):
            raise ValueError(f"{cell} contains forbidden sampling/trace flag")
    if cells != EXPECTED_CELLS or len(binaries) != 1:
        raise ValueError("metadata grid or frozen-binary identity is inconsistent")

    binary = Path(next(iter(binaries)))
    expected_hash = manifest.get("sha256_by_target", {}).get("baseline")
    actual_hash = sha256(binary)
    if not expected_hash or actual_hash != expected_hash:
        raise ValueError(
            f"frozen binary hash mismatch: {actual_hash} != {expected_hash}"
        )
    return binary, actual_hash


def audit_admission(root: Path) -> int:
    path = root / "DYNAMIC_ADMISSION_AUDIT.csv"
    with path.open(newline="", encoding="utf-8") as stream:
        rows = list(csv.DictReader(stream))
    previous_time = None
    seen = set()
    for row in rows:
        if row.get("decision") != "admitted":
            raise ValueError(f"unexpected admission decision: {row}")
        available = float(row["mem_available_gib"])
        reserve = float(row["reserve_gib"])
        old = int(row["previous_concurrency"])
        new = int(row["new_concurrency"])
        cell = (row["launched_benchmark"], row["launched_config"])
        timestamp = datetime.fromisoformat(row["event_utc"].replace("Z", "+00:00"))
        if available <= reserve or reserve != 50.0:
            raise ValueError(f"admission violated the strict reserve: {row}")
        if new != old + 1 or old < 1 or new > 14:
            raise ValueError(f"invalid concurrency transition: {row}")
        if cell not in EXPECTED_CELLS or cell in seen:
            raise ValueError(f"invalid or duplicate admitted cell: {row}")
        if previous_time is not None and (timestamp - previous_time).total_seconds() < 1790:
            raise ValueError("dynamic admissions occurred faster than 30 minutes")
        previous_time = timestamp
        seen.add(cell)
    return len(rows)


def audit_results(root: Path) -> list[dict[str, object]]:
    expected = {
        root / f"baseline_{benchmark}_{config}_result.json"
        for benchmark, config in EXPECTED_CELLS
    }
    observed = set(root.glob("*_result.json"))
    if observed != expected:
        missing = sorted(path.name for path in expected - observed)
        extra = sorted(path.name for path in observed - expected)
        raise ValueError(f"result grid is not 42/42; missing={missing}; extra={extra}")
    rows = []
    for benchmark, config in sorted(EXPECTED_CELLS):
        result_path = root / f"baseline_{benchmark}_{config}_result.json"
        result = load_json(result_path)
        if not result.get("success") or result.get("returncode") != 0:
            raise ValueError(f"failed result: {benchmark}/{config}: {result}")
        metrics_path = Path(result.get("metrics", ""))
        if metrics_path.resolve() != (
            root / f"baseline_{benchmark}_{config}_metrics.csv"
        ).resolve():
            raise ValueError(f"metric path mismatch in result {result_path.name}")
        metrics, values = read_metrics(metrics_path)
        missing_metrics = sorted(REQUIRED_METRICS[config] - metrics.keys())
        if missing_metrics:
            raise ValueError(
                f"{benchmark}/{config} missing mechanism metrics: {missing_metrics}"
            )
        wg = audit_wg_completion(root, benchmark, config, metrics, values)
        rows.append(wg)
    return rows


def audit_mapping_identity(
    root: Path, baseline_root: Path
) -> list[dict[str, object]]:
    expected_paths = []
    for benchmark, _label, _group in WORKLOADS:
        expected_paths.append(
            baseline_root / f"baseline_{benchmark}_baseline_metrics_wg_mapping.json"
        )
        expected_paths.extend(
            root / f"baseline_{benchmark}_{config}_metrics_wg_mapping.json"
            for config in CONFIGS
        )
    present = [path.is_file() for path in expected_paths]
    if not any(present):
        return []
    if not all(present):
        missing = [str(path) for path, exists in zip(expected_paths, present) if not exists]
        raise ValueError(f"WG mapping sidecars are only partially present: {missing}")

    rows = []
    for benchmark, _label, _group in WORKLOADS:
        baseline_path = baseline_root / (
            f"baseline_{benchmark}_baseline_metrics_wg_mapping.json"
        )
        if not baseline_path.is_file():
            raise ValueError(f"missing Baseline WG mapping report: {baseline_path}")
        baseline = load_json(baseline_path)
        validate_report(baseline_path, baseline)
        expected = {
            "launch": launch_signature(baseline),
            "partition": partition_signature(baseline),
            "wg_set": baseline["global_wg_set_sha256"],
            "per_gpu": per_gpu_signature(baseline),
        }
        for config in CONFIGS:
            path = root / (
                f"baseline_{benchmark}_{config}_metrics_wg_mapping.json"
            )
            if not path.is_file():
                raise ValueError(f"missing ablation WG mapping report: {path}")
            report = load_json(path)
            validate_report(path, report)
            observed = {
                "launch": launch_signature(report),
                "partition": partition_signature(report),
                "wg_set": report["global_wg_set_sha256"],
                "per_gpu": per_gpu_signature(report),
            }
            matches = {name: observed[name] == expected[name] for name in expected}
            if not all(matches.values()):
                failed = sorted(name for name, passed in matches.items() if not passed)
                raise ValueError(
                    f"{benchmark}/{config} changes Baseline WG identity: {failed}"
                )
            rows.append({
                "benchmark": benchmark,
                "configuration": config,
                "requested_total_wg": report["requested_total_wg"],
                "observed_wg_count": report["observed_wg_count"],
                "completed_wg_count": report.get("completed_wg_count"),
                "launch_matches_baseline": True,
                "partition_matches_baseline": True,
                "wg_set_matches_baseline": True,
                "per_gpu_matches_baseline": True,
                "strict_identity_matches_baseline": True,
            })
    return rows


def write_report(
    root: Path, rows: list[dict[str, object]], binary: Path,
    binary_hash: str, admissions: int,
    mapping_rows: list[dict[str, object]] | None,
) -> None:
    output = root / "cupath_m1_m2_m3_completion_audit.csv"
    with output.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    limited = sum(row["termination"] == "completed_wg_limit" for row in rows)
    text = [
        "# CuPath M1/M2/M3 campaign audit",
        "",
        f"- Audited cells: {len(rows)}/42",
        f"- Frozen binary: `{binary}`",
        f"- SHA-256: `{binary_hash}`",
        "- L1V configuration: 16 MSHRs and 16 concurrent transactions in every cell",
        f"- Exact 78,600-completion stops: {limited}",
        f"- Natural workload completions below the limit: {len(rows) - limited}",
        f"- Logged 30-minute memory admissions above 50 GiB: {admissions}",
        "- Sampling, observation tracing, launch-time WG limiting, and kernel draining: absent",
        "- Required M1/M2/M3 effectiveness counters: present in every cell",
    ]
    if mapping_rows:
        mapping_path = root / "cupath_m1_m2_m3_wg_identity_audit.csv"
        with mapping_path.open("w", newline="", encoding="utf-8") as stream:
            writer = csv.DictWriter(stream, fieldnames=tuple(mapping_rows[0]))
            writer.writeheader()
            writer.writerows(mapping_rows)
        text.append(
            "- Launch descriptors, original partitions, global WG sets, and "
            "per-GPU assignments: 42/42 match the paired Baseline"
        )
    else:
        text.append(
            "- Runtime WG mapping sidecars: not emitted; no per-WG hash claim is made"
        )
        text.append(
            "- WG semantics evidence: same frozen binary, identical workload inputs, "
            "completion-only stopper source, and no launch limiting or kernel draining"
        )
    (root / "CUPATH_M1_M2_M3_CAMPAIGN_AUDIT.md").write_text(
        "\n".join(text) + "\n", encoding="utf-8"
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--metadata-only", action="store_true")
    parser.add_argument("--baseline-results", type=Path)
    args = parser.parse_args()
    root = args.results.resolve()
    binary, binary_hash = audit_metadata(root)
    admissions = audit_admission(root)
    if args.metadata_only:
        print(
            f"metadata audit PASS: cells=42 hash={binary_hash} "
            f"admissions={admissions}"
        )
        return 0
    rows = audit_results(root)
    audit_passive_wg_stopper_source(root)
    mapping_rows = None
    if args.baseline_results is not None:
        baseline_root = args.baseline_results.resolve()
        baseline_manifest = load_json(baseline_root / "EXPERIMENT_BINARIES.json")
        baseline_hash = baseline_manifest.get("sha256_by_target", {}).get("baseline")
        if baseline_hash != binary_hash:
            raise ValueError(
                f"ablation/Baseline frozen binary mismatch: "
                f"{binary_hash} != {baseline_hash}"
            )
        mapping_rows = audit_mapping_identity(root, baseline_root)
    write_report(root, rows, binary, binary_hash, admissions, mapping_rows)
    print(
        f"campaign audit PASS: cells={len(rows)} hash={binary_hash} "
        f"admissions={admissions}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
