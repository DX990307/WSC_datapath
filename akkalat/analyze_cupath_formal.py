#!/usr/bin/env python3
"""Create the auditable final CuPath analysis from a 70-cell campaign.

This script refuses to call a partial campaign a paper result.  It consumes
the CSVs produced by ``plot_cupath_typed_ablation.py`` and
``plot_cupath_work_reduction.py`` and emits a per-workload causal table plus a
compact Markdown report suitable for checking the paper text.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import math
from collections import defaultdict
from pathlib import Path

from plot_cupath_typed_ablation import (
    CONFIGS,
    NEGATIVE_THRESHOLD,
    POSITIVE_THRESHOLD,
    WORKLOADS,
    geomean,
)
from analyze_wg_mapping import audit as audit_wg_mapping


def rows_by_key(path: Path, key: str = "benchmark") -> dict[str, dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as stream:
        return {row[key]: row for row in csv.DictReader(stream)}


def rows_by_pair(
    path: Path, first: str = "benchmark", second: str = "config"
) -> dict[tuple[str, str], dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as stream:
        return {
            (row[first], row[second]): row
            for row in csv.DictReader(stream)
        }


def number(row: dict[str, str], field: str) -> float | None:
    value = row.get(field, "")
    if value in ("", None):
        return None
    return float(value)


def ratio(numerator: float | None, denominator: float | None) -> float | None:
    if numerator is None or denominator is None or denominator <= 0:
        return None
    return numerator / denominator


def percent(numerator: float | None, denominator: float | None) -> float | None:
    value = ratio(numerator, denominator)
    return None if value is None else 100.0 * value


def reduction(before: float | None, after: float | None) -> float | None:
    if before is None or after is None:
        return None
    return percent(before - after, before)


def fmt(value: float | None, suffix: str = "", digits: int = 2) -> str:
    if value is None or not math.isfinite(value):
        return "--"
    return f"{value:.{digits}f}{suffix}"


def classify(value: float) -> str:
    if value > POSITIVE_THRESHOLD:
        return "positive"
    if value < NEGATIVE_THRESHOLD:
        return "negative"
    return "neutral"


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def audit_experiment_metadata(
    root: Path,
    expected_workers: int = 14,
    expected_sha256: str | None = None,
    expected_memory_reserve_gib: float | None = None,
    expected_memory_per_worker_gib: float | None = None,
) -> tuple[str, str]:
    """Reject a formal campaign whose recorded commands are not comparable."""
    metadata = json.loads(
        (root / "EXPERIMENT_METADATA.json").read_text(encoding="utf-8")
    )
    manifest = json.loads(
        (root / "EXPERIMENT_BINARIES.json").read_text(encoding="utf-8")
    )
    experiments = metadata.get("experiments", [])
    if metadata.get("experiment_count") != 70 or len(experiments) != 70:
        raise SystemExit(
            f"formal metadata contains {len(experiments)}/70 commands"
        )
    launcher = metadata.get("launcher", {})
    if launcher.get("max_workers") != expected_workers:
        raise SystemExit(
            "formal worker mismatch: "
            f"expected={expected_workers}, launcher={launcher!r}"
        )
    if expected_memory_reserve_gib is not None:
        if launcher.get("memory_reserve_gib") != expected_memory_reserve_gib:
            raise SystemExit(
                "formal memory-reserve mismatch: "
                f"expected={expected_memory_reserve_gib}, launcher={launcher!r}"
            )
        if launcher.get("memory_per_worker_gib") != expected_memory_per_worker_gib:
            raise SystemExit(
                "formal per-worker memory budget mismatch: "
                f"expected={expected_memory_per_worker_gib}, launcher={launcher!r}"
            )
        if launcher.get("memory_worker_cap", 0) < expected_workers:
            raise SystemExit(
                "formal worker count exceeds recorded memory cap: "
                f"launcher={launcher!r}"
            )
        if launcher.get("mem_available_gib_at_launch", 0) <= expected_memory_reserve_gib:
            raise SystemExit(
                "formal launch did not retain the recorded memory reserve: "
                f"launcher={launcher!r}"
            )

    expected_cells = {
        (benchmark, config)
        for benchmark, _, _ in WORKLOADS
        for config, _, _ in CONFIGS
    }
    observed_cells: set[tuple[str, str]] = set()
    common = {
        "-timing",
        "-num-memory-banks=4",
        "-l1v-mshr-entries=16",
        "-l1v-max-concurrent-trans=16",
        "-bandwidth=48",
        "-switch-latency=32",
        "-rdma-pipeline-width=8",
        "-rdma-pipeline-latency=10",
        "-rdma-max-outstanding=64",
        "-max-wg=76800",
        "-sampled",
        "-branch-sampled",
        "-kernel-sampled",
        "-typed-filter-mode=cuckoo",
        "-typed-filter-slots-per-bucket=4",
        "-typed-filter-fingerprint-bits=13",
        "-typed-filter-lookup-latency=1",
        "-typed-filter-lookup-width=16",
        "-typed-filter-update-latency=1",
        "-typed-filter-update-width=16",
        "-prefetch-predictor-entries=256",
        "-remote-data-path-batch-lines=8",
        "-remote-data-path-batches=64",
        "-l2-fill-forwarding-enable=false",
        "-dram-row-continuation-enable=false",
        "-l2-prefetch-predictor-only=false",
        "-l2-prefetch-ungated=false",
        "-magic-memory-copy",
        "-report-all",
        "-disable-servers",
        "-mmutlb-lookup-latency=80",
    }
    mechanisms = {
        "baseline": (False, False, False, False, False, False, False),
        "m1": (False, True, False, False, False, False, False),
        "m2": (False, False, True, True, True, False, True),
        "m3": (False, False, True, False, False, True, False),
        "complete": (False, True, True, True, True, True, True),
    }
    fields = (
        "l2-resident-filter-enable",
        "l2-filter-prefetch-enable",
        "remote-data-path-enable",
        "remote-data-path-dedup-enable",
        "remote-data-path-batching-enable",
        "remote-data-path-l2-enable",
        "remote-filter-prefetch-enable",
    )
    binary_paths: set[str] = set()
    for experiment in experiments:
        benchmark = experiment.get("benchmark")
        config = experiment.get("configuration")
        cell = (benchmark, config)
        if cell not in expected_cells or cell in observed_cells:
            raise SystemExit(f"unexpected or duplicate metadata cell: {cell}")
        observed_cells.add(cell)
        command = experiment.get("command", [])
        if not command:
            raise SystemExit(f"empty command for {benchmark}/{config}")
        command_flags = command[1:]
        tokens = set(command_flags)
        if len(tokens) != len(command_flags):
            raise SystemExit(
                f"duplicate formal flags in {benchmark}/{config}"
            )
        missing = common - tokens
        if missing:
            raise SystemExit(
                f"{benchmark}/{config} is missing fixed flags: "
                + ", ".join(sorted(missing))
            )
        expected_mechanisms = {
            f"-{field}={str(value).lower()}"
            for field, value in zip(fields, mechanisms[config])
        }
        if not expected_mechanisms <= tokens:
            raise SystemExit(
                f"{benchmark}/{config} mechanism flags do not match the "
                "formal ablation"
            )
        if f"-benchmark={benchmark}" not in tokens:
            raise SystemExit(f"benchmark flag mismatch for {benchmark}/{config}")
        metric_path = (
            root / f"baseline_{benchmark}_{config}_metrics"
        ).resolve()
        metric_flag = f"-metric-file-name={metric_path}"
        if metric_flag not in tokens:
            raise SystemExit(f"metric destination mismatch for {benchmark}/{config}")
        forbidden = (
            "128b", "128-b", "128-byte", "hlq", "prefetch-wait",
            "batching-timeout", "global-scheduler", "force-local-data-access",
            "trace-remote-origin",
        )
        if any(any(name in token.lower() for name in forbidden) for token in tokens):
            raise SystemExit(f"forbidden mechanism flag in {benchmark}/{config}")
        expected_flags = (
            common
            | expected_mechanisms
            | {f"-benchmark={benchmark}", metric_flag}
        )
        unexpected = sorted(tokens - expected_flags)
        if unexpected:
            raise SystemExit(
                f"unexpected formal flags in {benchmark}/{config}: "
                + ", ".join(unexpected)
            )
        binary_paths.add(command[0])

    if observed_cells != expected_cells:
        raise SystemExit("formal metadata does not cover the expected 14x5 grid")
    if len(binary_paths) != 1:
        raise SystemExit(f"formal metadata mixes binaries: {sorted(binary_paths)}")
    binary_path = Path(next(iter(binary_paths)))
    expected_hash = manifest.get("sha256_by_target", {}).get("baseline")
    if not expected_hash:
        raise SystemExit("frozen binary hash is missing from manifest")
    if not binary_path.is_file():
        raise SystemExit(f"frozen binary is unavailable: {binary_path}")
    actual_hash = file_sha256(binary_path)
    if actual_hash != expected_hash:
        raise SystemExit(
            f"frozen binary hash mismatch: {actual_hash} != {expected_hash}"
        )
    if expected_sha256 is not None and actual_hash != expected_sha256:
        raise SystemExit(
            f"unexpected formal binary hash: expected={expected_sha256}, "
            f"actual={actual_hash}"
        )
    return str(binary_path), actual_hash


def audit_completed_results(root: Path) -> None:
    """Require one successful runner result and its artifacts for every cell."""
    expected = {
        root / f"baseline_{benchmark}_{config}_result.json": (benchmark, config)
        for benchmark, _, _ in WORKLOADS
        for config, _, _ in CONFIGS
    }
    observed = set(root.glob("*_result.json"))
    if observed != set(expected):
        missing = sorted(path.name for path in set(expected) - observed)
        extra = sorted(path.name for path in observed - set(expected))
        details = []
        if missing:
            details.append("missing=" + ", ".join(missing))
        if extra:
            details.append("unexpected=" + ", ".join(extra))
        raise SystemExit(
            f"formal completion grid mismatch: {len(observed)}/70 result JSONs"
            + ("; " + "; ".join(details) if details else "")
        )

    for result_path, (benchmark, config) in sorted(
        expected.items(), key=lambda item: item[0].name
    ):
        try:
            result = json.loads(result_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            raise SystemExit(f"unreadable formal result {result_path}: {error}")
        if (
            result.get("success") is not True
            or result.get("returncode") != 0
            or result.get("simulator_returncode") != 0
        ):
            raise SystemExit(
                f"failed formal result for {benchmark}/{config}: {result}"
            )
        if (
            result.get("benchmark") != benchmark
            or result.get("configuration") != config
            or result.get("target") != "baseline"
        ):
            raise SystemExit(
                f"formal result identity mismatch for {benchmark}/{config}"
            )

        expected_metrics = (
            root / f"baseline_{benchmark}_{config}_metrics.csv"
        ).resolve()
        recorded_metrics = result.get("metrics")
        if (
            not isinstance(recorded_metrics, str)
            or Path(recorded_metrics).resolve() != expected_metrics
            or not expected_metrics.is_file()
        ):
            raise SystemExit(
                f"formal metric artifact mismatch for {benchmark}/{config}"
            )

        mapping = result.get("wg_mapping")
        expected_mapping = (
            root / f"baseline_{benchmark}_{config}_metrics_wg_mapping.json"
        ).resolve()
        recorded_mapping = mapping.get("path") if isinstance(mapping, dict) else None
        if (
            not isinstance(recorded_mapping, str)
            or Path(recorded_mapping).resolve() != expected_mapping
            or not expected_mapping.is_file()
        ):
            raise SystemExit(
                f"formal WG-mapping artifact mismatch for {benchmark}/{config}"
            )


def audit_runtime_stop_values(
    benchmark: str,
    config: str,
    total_wg: float,
    max_wg: float,
    observed_wg: float,
    runtime_stopper: float,
    reached: float,
    requested_wg: float,
    max_wg_filter: float,
) -> None:
    """Validate a runtime observation upper bound without inventing WGs."""
    expected_observed = min(max_wg, requested_wg)
    expected_reached = 1 if requested_wg >= max_wg else 0
    if total_wg != observed_wg or observed_wg != expected_observed:
        raise SystemExit(
            f"runtime-stop WG mismatch for {benchmark}/{config}: "
            f"total={total_wg:g}, observed={observed_wg:g}, "
            f"limit={max_wg:g}, requested={requested_wg:g}, "
            f"expected={expected_observed:g}"
        )
    if (
        runtime_stopper != 1
        or reached != expected_reached
        or max_wg_filter != 0
    ):
        raise SystemExit(
            f"invalid runtime-stopper evidence for {benchmark}/{config}: "
            f"runtime={runtime_stopper:g}, reached={reached:g}, "
            f"expected-reached={expected_reached:g}, "
            f"max-wg-filter={max_wg_filter:g}"
        )


def diagnose(row: dict[str, object]) -> str:
    """Assign only counter-supported, category-level diagnoses."""
    reasons: list[str] = []
    if row["l2_tag_removed_pct"] is not None and row["l2_tag_removed_pct"] < 10:
        reasons.append("little definite-miss tag work")
    if row["remote_reads_removed_pct"] is not None and row["remote_reads_removed_pct"] < 1:
        reasons.append("little same-line remote duplication")
    if row["wire_packets_removed_pct"] is not None and row["wire_packets_removed_pct"] < 5:
        reasons.append("little destination aggregation")
    if row["traversals_removed_pct"] is not None and row["traversals_removed_pct"] < 1:
        reasons.append("little remote recurrence")
    if (
        row["complete_l2_mshr_full_stall_cycles"] is not None
        and row["complete_l2_mshr_full_stall_cycles"] > 0
    ):
        reasons.append("L2 MSHR-full stalls remain")
    if (
        (row["filter_lookup_stalls"] or 0) > 0
        or (row["filter_update_stalls"] or 0) > 0
    ):
        reasons.append("shared Filter port stalls are present")
    significant_removed_work = any(
        row[field] is not None and row[field] >= threshold
        for field, threshold in (
            ("l2_tag_removed_pct", 10),
            ("remote_reads_removed_pct", 1),
            ("wire_packets_removed_pct", 5),
            ("traversals_removed_pct", 1),
        )
    )
    if significant_removed_work:
        reasons.append(
            "the measured work removal does not reduce the remaining "
            "end-to-end critical path enough"
        )
    elif not reasons:
        reasons.append("the request stream exposes little removable work")
    return "; ".join(reasons)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--summary-dir", type=Path, required=True)
    parser.add_argument("--expected-workers", type=int, default=14)
    parser.add_argument("--expected-sha256")
    parser.add_argument("--expected-memory-reserve-gib", type=float)
    parser.add_argument("--expected-memory-per-worker-gib", type=float)
    args = parser.parse_args()
    root = args.summary_dir.resolve()
    binary_path, binary_hash = audit_experiment_metadata(
        root,
        expected_workers=args.expected_workers,
        expected_sha256=args.expected_sha256,
        expected_memory_reserve_gib=args.expected_memory_reserve_gib,
        expected_memory_per_worker_gib=args.expected_memory_per_worker_gib,
    )
    audit_completed_results(root)
    mapping_rows = audit_wg_mapping(
        root, [benchmark for benchmark, _, _ in WORKLOADS]
    )
    partition_mismatches = [
        row for row in mapping_rows
        if not row["partition_matches_baseline"]
    ]
    if partition_mismatches:
        raise SystemExit(
            "configuration-dependent original WG partition: "
            + ", ".join(
                f"{row['benchmark']}/{row['configuration']}"
                for row in partition_mismatches
            )
        )
    speed = rows_by_key(root / "cupath_speedup_table.csv")
    work = rows_by_key(root / "cupath_work_reduction.csv")
    filt = rows_by_key(root / "cupath_filter_statistics.csv")
    prefetch = rows_by_key(root / "cupath_prefetch_statistics.csv")
    m1 = rows_by_key(root / "cupath_m1_attribution.csv")
    m2 = rows_by_key(root / "cupath_m2_attribution.csv")
    m3 = rows_by_key(root / "cupath_m3_attribution.csv")
    runtime = rows_by_key(root / "cupath_simulator_runtime.csv")
    execution = rows_by_key(root / "cupath_execution_audit.csv")
    controller = rows_by_pair(root / "cupath_controller_slice_summary.csv")
    traffic = rows_by_key(root / "cupath_baseline_traffic_classification.csv")

    expected = {name for name, _, _ in WORKLOADS}
    missing = (
        expected - speed.keys()
        | expected - work.keys()
        | expected - filt.keys()
        | expected - prefetch.keys()
        | expected - m1.keys()
        | expected - m2.keys()
        | expected - m3.keys()
        | expected - runtime.keys()
        | expected - execution.keys()
        | expected - traffic.keys()
    )
    if missing:
        raise SystemExit("missing paper workloads: " + ", ".join(sorted(missing)))
    expected_cells = {
        (benchmark, config)
        for benchmark, _, _ in WORKLOADS
        for config, _, _ in CONFIGS
    }
    missing_controller = expected_cells - controller.keys()
    if missing_controller:
        raise SystemExit(
            "missing controller/slice summaries: "
            + ", ".join(
                f"{benchmark}/{config}"
                for benchmark, config in sorted(missing_controller)
            )
        )
    for key in sorted(expected_cells):
        dram_instances = number(controller[key], "dram_instances")
        l2_slices = number(controller[key], "l2_slices")
        if dram_instances != 192 or l2_slices != 192:
            raise SystemExit(
                f"incomplete component coverage for {key[0]}/{key[1]}: "
                f"{dram_instances} DRAM instances, {l2_slices} L2 slices"
            )

    detail: list[dict[str, object]] = []
    groups: dict[str, list[float]] = defaultdict(list)
    config_values: dict[str, list[float]] = defaultdict(list)
    for benchmark, label, _ in WORKLOADS:
        group = traffic[benchmark]["traffic_class"]
        speed_row = speed[benchmark]
        work_row = work[benchmark]
        config_speedups = {
            config: number(speed_row, f"{config}_speedup")
            for config, _, _ in CONFIGS
        }
        if any(value is None for value in config_speedups.values()):
            raise SystemExit(f"partial formal campaign: missing speedup for {benchmark}")
        observed_windows = []
        requested_totals = []
        page_sizes = []
        workload_pages = []
        overall_pages = []
        for config, _, _ in CONFIGS:
            total_wg = number(execution[benchmark], f"{config}_total_wg_count")
            max_wg = number(
                execution[benchmark], f"{config}_max_wg_limit"
            )
            observed_wg = number(
                execution[benchmark], f"{config}_max_wg_observed"
            )
            runtime_stopper = number(
                execution[benchmark], f"{config}_max_wg_runtime_stopper"
            )
            reached = number(
                execution[benchmark], f"{config}_max_wg_reached"
            )
            requested_wg = number(
                execution[benchmark], f"{config}_wg_requested_total"
            )
            max_wg_filter = number(
                execution[benchmark], f"{config}_wg_max_wg_specific_filter"
            )
            page_size = number(
                execution[benchmark], f"{config}_allocation_page_size"
            )
            pages = number(
                execution[benchmark],
                f"{config}_allocation_workload_allocated_pages",
            )
            total_pages = number(
                execution[benchmark],
                f"{config}_allocation_overall_allocated_pages",
            )
            return_code = number(execution[benchmark], f"{config}_return_code")
            if (
                total_wg is None
                or max_wg is None
                or observed_wg is None
                or runtime_stopper is None
                or reached is None
                or requested_wg is None
                or max_wg_filter is None
                or page_size is None
                or pages is None
                or total_pages is None
                or return_code is None
            ):
                raise SystemExit(
                    f"partial execution audit for {benchmark}/{config}"
                )
            if return_code != 0:
                raise SystemExit(
                    f"nonzero return code for {benchmark}/{config}: "
                    f"{return_code:g}"
                )
            audit_runtime_stop_values(
                benchmark,
                config,
                total_wg,
                max_wg,
                observed_wg,
                runtime_stopper,
                reached,
                requested_wg,
                max_wg_filter,
            )
            if requested_wg < observed_wg:
                raise SystemExit(
                    f"requested grid smaller than observed window for "
                    f"{benchmark}/{config}: {requested_wg:g} < {observed_wg:g}"
                )
            observed_windows.append(observed_wg)
            requested_totals.append(requested_wg)
            page_sizes.append(page_size)
            workload_pages.append(pages)
            overall_pages.append(total_pages)
        if len(set(observed_windows)) != 1:
            raise SystemExit(
                f"configuration-dependent observed window for {benchmark}: "
                + ", ".join(f"{value:g}" for value in observed_windows)
            )
        if len(set(requested_totals)) != 1:
            raise SystemExit(
                f"configuration-dependent full requested grid for {benchmark}: "
                + ", ".join(f"{value:g}" for value in requested_totals)
            )
        if set(page_sizes) != {4096.0}:
            raise SystemExit(
                f"unexpected allocation page size for {benchmark}: "
                + ", ".join(f"{value:g}" for value in page_sizes)
            )
        if len(set(workload_pages)) != 1 or len(set(overall_pages)) != 1:
            raise SystemExit(
                f"configuration-dependent allocation footprint for {benchmark}"
            )
        complete = float(config_speedups["complete"])
        groups[group].append(complete)
        for config, value in config_speedups.items():
            config_values[config].append(float(value))

        l2_queries = number(work_row, "l2_resident_filter_queries")
        l2_skips = number(work_row, "l2_tag_lookups_skipped")
        logical_remote = number(work_row, "complete_remote_logical_reads")
        duplicates = number(work_row, "remote_duplicate_reads_eliminated")
        wire_lines = number(work_row, "complete_remote_wire_lines")
        packets = number(work_row, "complete_remote_packets")
        traversals = number(work_row, "repeated_wafer_traversals_eliminated")
        remote_removed_pct = (
            0.0 if logical_remote == 0 else percent(duplicates, logical_remote)
        )
        traversal_removed_pct = (
            0.0 if logical_remote == 0 else percent(traversals, logical_remote)
        )
        packet_removed_pct = (
            0.0 if wire_lines == 0 else reduction(wire_lines, packets)
        )
        row: dict[str, object] = {
            "benchmark": benchmark,
            "label": label,
            "group": group,
            **{f"{config}_speedup": value for config, value in config_speedups.items()},
            "classification": classify(complete),
            "observed_workgroups": observed_windows[0],
            "requested_total_workgroups": requested_totals[0],
            "baseline_total_data_requests": number(
                traffic[benchmark], "total_requests"
            ),
            "baseline_remote_data_requests": number(
                traffic[benchmark], "remote_requests"
            ),
            "baseline_remote_request_pct": number(
                traffic[benchmark], "remote_percent"
            ),
            "workload_allocated_pages": workload_pages[0],
            "workload_footprint_mib": workload_pages[0] * 4096 / (1024 * 1024),
            "l2_tag_removed_pct": percent(l2_skips, l2_queries),
            "remote_reads_removed_pct": remote_removed_pct,
            "wire_packets_removed_pct": packet_removed_pct,
            "traversals_removed_pct": traversal_removed_pct,
            "baseline_l2_mshr_full_stall_cycles": number(
                work_row, "baseline_l2_mshr_full_stall_cycles"
            ),
            "complete_l2_mshr_full_stall_cycles": number(
                work_row, "complete_l2_mshr_full_stall_cycles"
            ),
            "filter_lookup_stalls": number(
                filt[benchmark], "typed_filter_lookup_port_stalls"
            ),
            "filter_update_stalls": number(
                filt[benchmark], "typed_filter_update_port_stalls"
            ),
        }
        row["counter_supported_diagnosis"] = diagnose(row)
        detail.append(row)

    detail_path = root / "cupath_formal_causal_table.csv"
    fields = tuple(detail[0])
    with detail_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(detail)

    complete_values = config_values["complete"]
    positive = sum(value > POSITIVE_THRESHOLD for value in complete_values)
    negative = sum(value < NEGATIVE_THRESHOLD for value in complete_values)
    neutral = len(complete_values) - positive - negative
    runtime_values: dict[str, list[float]] = {}
    for config, _, _ in CONFIGS:
        values = [
            number(runtime[b], f"{config}_wall_over_baseline")
            for b in expected
        ]
        runtime_values[config] = [float(value) for value in values if value is not None]

    filter_totals: dict[str, tuple[float, float, float, float, float]] = {}
    for kind in ("pattern", "resident", "pending", "seen"):
        queries = sum(number(filt[b], f"typed_filter_{kind}_queries") or 0 for b in expected)
        positives = sum(
            number(filt[b], f"typed_filter_{kind}_positives") or 0
            for b in expected
        )
        fps = sum(number(filt[b], f"typed_filter_{kind}_false_positives") or 0 for b in expected)
        failures = sum(number(filt[b], f"typed_filter_{kind}_insert_failures") or 0 for b in expected)
        reliable = sum(number(filt[b], f"typed_filter_{kind}_reliable") or 0 for b in expected)
        filter_totals[kind] = (queries, positives, fps, failures, reliable)
    all_queries = sum(value[0] for value in filter_totals.values())
    all_fps = sum(value[2] for value in filter_totals.values())
    all_failures = sum(value[3] for value in filter_totals.values())

    filter_lifecycle: dict[str, dict[str, float]] = {}
    for kind in ("pattern", "resident", "pending", "seen"):
        filter_lifecycle[kind] = {
            "insertions": sum(
                number(filt[b], f"typed_filter_{kind}_insertions") or 0
                for b in expected
            ),
            "deletes": sum(
                number(filt[b], f"typed_filter_{kind}_deletes") or 0
                for b in expected
            ),
            "lookup_busy": sum(
                number(filt[b], f"typed_filter_{kind}_lookup_busy_drops") or 0
                for b in expected
            ),
            "update_busy": sum(
                number(filt[b], f"typed_filter_{kind}_update_busy_drops") or 0
                for b in expected
            ),
            # Each row sums all 192 physical slices in one independent run.
            # A maximum preserves a real workload-level bound; summing rows
            # would fabricate simultaneous occupancy across simulations.
            "ending": max(
                number(filt[b], f"typed_filter_{kind}_occupancy") or 0
                for b in expected
            ),
            "peak": max(
                number(filt[b], f"typed_filter_{kind}_peak_occupancy") or 0
                for b in expected
            ),
        }

    def total_filter(field: str) -> float:
        return sum(number(filt[b], field) or 0 for b in expected)

    pending_positives = total_filter("typed_filter_pending_positives")
    pending_insertions = total_filter("typed_filter_pending_insertions")
    pending_deletes = total_filter("typed_filter_pending_deletes")
    if pending_insertions != pending_deletes:
        raise SystemExit("PENDING Filter lifecycle does not balance")

    def total_prefetch(field: str) -> float:
        return sum(number(prefetch[b], field) or 0 for b in expected)

    local_real_demands = total_prefetch("filter_prefetch_real_demands")
    local_candidates = total_prefetch("filter_prefetch_candidates")
    local_pattern_installs = total_prefetch("filter_prefetch_pattern_installs")
    local_pattern_install_drops = total_prefetch(
        "filter_prefetch_pattern_install_drops"
    )
    local_evidence_one = total_prefetch(
        "filter_prefetch_predictor_evidence_one"
    )
    local_evidence_two = total_prefetch(
        "filter_prefetch_predictor_evidence_two"
    )
    local_stride = {
        bucket: total_prefetch(f"filter_prefetch_stride_{bucket}")
        for bucket in ("one", "small", "medium", "large", "negative")
    }
    local_issued = total_prefetch("filter_prefetch_issued")
    local_outstanding = total_prefetch("filter_prefetch_outstanding")
    local_redundant_races = total_prefetch(
        "filter_prefetch_redundant_races"
    )
    local_fills = total_prefetch("filter_prefetch_fills")
    local_useful = total_prefetch("filter_prefetch_useful")
    local_timely = total_prefetch("filter_prefetch_timely")
    local_late = total_prefetch("filter_prefetch_late")
    local_late_after_dram = total_prefetch(
        "filter_prefetch_late_after_dram_issue"
    )
    local_demand_won = total_prefetch("filter_prefetch_demand_won_races")
    local_unused = total_prefetch("filter_prefetch_unused")
    local_demand_merges = total_prefetch("filter_prefetch_demand_merges")
    local_delay = total_prefetch("filter_prefetch_demand_delay_events")
    local_extra_dram = total_prefetch("filter_prefetch_additional_dram_reads")
    if local_issued != local_fills + local_redundant_races + local_outstanding:
        raise SystemExit(
            "Complete local issued prefetches do not partition into fills, "
            "redundant races, and runtime-stop outstanding requests"
        )
    remote_real_demands = total_prefetch("remote_prefetch_real_demands")
    remote_candidates = total_prefetch("remote_prefetch_candidates")
    remote_pattern_installs = total_prefetch("remote_prefetch_pattern_installs")
    remote_pattern_install_drops = total_prefetch(
        "remote_prefetch_pattern_install_drops"
    )
    remote_evidence_one = total_prefetch(
        "remote_prefetch_predictor_evidence_one"
    )
    remote_evidence_two = total_prefetch(
        "remote_prefetch_predictor_evidence_two"
    )
    remote_stride = {
        bucket: total_prefetch(f"remote_prefetch_stride_{bucket}")
        for bucket in ("one", "small", "medium", "large", "negative")
    }
    remote_filter_drops = total_prefetch("remote_prefetch_filter_drops")
    remote_piggyback = total_prefetch("remote_prefetch_piggyback_lines")
    remote_useful = total_prefetch("remote_prefetch_useful")
    remote_unused = total_prefetch("remote_prefetch_unused")
    remote_standalone_prevented = total_prefetch(
        "remote_prefetch_standalone_prevented"
    )
    remote_added_response_bytes = total_prefetch(
        "remote_prefetch_added_response_bytes"
    )
    remote_extra_owner = total_prefetch("remote_prefetch_additional_owner_reads")
    remote_pre_send_merges = total_prefetch("remote_pre_send_merges")
    remote_inflight_merges = total_prefetch("remote_inflight_merges")
    remote_ready_merges = total_prefetch("remote_ready_merges")
    remote_fanout_responses = total_prefetch("remote_fanout_responses")
    local_gate_drops = {
        "predictor busy": total_prefetch("filter_prefetch_candidate_busy_drops"),
        "PATTERN negative": total_prefetch(
            "filter_prefetch_pattern_negative_drops"
        ),
        "RESIDENT positive": total_prefetch(
            "filter_prefetch_resident_positive_drops"
        ),
        "PENDING positive": total_prefetch(
            "filter_prefetch_pending_positive_drops"
        ),
        "wrong target slice": total_prefetch(
            "filter_prefetch_wrong_slice_drops"
        ),
        "demand priority": total_prefetch(
            "filter_prefetch_demand_priority_drops"
        ),
        "lower-path input busy (legacy controller name)": total_prefetch(
            "filter_prefetch_controller_busy_drops"
        ),
        "output busy": total_prefetch("filter_prefetch_output_busy_drops"),
        "MSHR unavailable": total_prefetch("filter_prefetch_mshr_drops"),
        "demand MSHR headroom": total_prefetch(
            "filter_prefetch_mshr_headroom_drops"
        ),
        "all bank opportunities busy": total_prefetch(
            "filter_prefetch_demand_path_busy_drops"
        ),
        "no invalid victim": total_prefetch("filter_prefetch_victim_drops"),
        "PENDING insert": total_prefetch(
            "filter_prefetch_pending_insert_drops"
        ),
    }
    remote_gate_drops = {
        "Filter suppression": remote_filter_drops,
        "same request group": total_prefetch(
            "remote_prefetch_same_group_drops"
        ),
        "tracking capacity": total_prefetch(
            "remote_prefetch_capacity_drops"
        ),
        "no existing batch": total_prefetch(
            "remote_prefetch_no_existing_batch_drops"
        ),
        "existing batch full": total_prefetch(
            "remote_prefetch_batch_full_drops"
        ),
    }
    rdma_width_stalls = {
        "requester issue": total_prefetch(
            "remote_requester_issue_width_stalls"
        ),
        "requester fanout": total_prefetch(
            "remote_response_fanout_width_stalls"
        ),
        "owner issue": total_prefetch("remote_owner_issue_width_stalls"),
        "owner response": total_prefetch(
            "remote_owner_response_width_stalls"
        ),
    }
    requester_l2_hits = total_prefetch("remote_requester_l2_hits")
    requester_l2_unused = total_prefetch("remote_requester_l2_unused_fills")
    requester_l2_unused_pattern = total_prefetch(
        "remote_requester_l2_unused_pattern_retirements"
    )
    requester_l2_installed = total_prefetch("remote_l2_two_touch_installed_fills")
    # Each workload row already sums the per-slice peaks. Taking a second sum
    # across workloads would invent concurrent occupancy across independent
    # simulations, so retain the largest workload-level upper bound instead.
    requester_l2_peak_lines = max(
        number(prefetch[b], "remote_requester_l2_peak_lines") or 0
        for b in expected
    )
    requester_l2_replaced_remote = total_prefetch("remote_fill_replaced_remote")
    requester_l2_into_invalid = total_prefetch("remote_fill_into_invalid")
    requester_l2_displaced_local = total_prefetch(
        "remote_fill_displaced_local_clean"
    )
    requester_l2_local_displacements = total_prefetch(
        "remote_two_touch_local_displacements"
    )
    requester_l2_evictions = total_prefetch("remote_tracked_evictions")
    requester_l2_current_lines = total_prefetch(
        "remote_requester_l2_current_lines"
    )
    if requester_l2_installed != (
        requester_l2_into_invalid
        + requester_l2_replaced_remote
        + requester_l2_displaced_local
    ):
        raise SystemExit(
            "requester-L2 fill partition does not sum to installed fills"
        )
    if requester_l2_installed != (
        requester_l2_evictions + requester_l2_current_lines
    ):
        raise SystemExit(
            "requester-L2 installed fills do not equal evictions + current lines"
        )
    if requester_l2_unused > requester_l2_evictions:
        raise SystemExit(
            "requester-L2 unused retirements exceed tracked evictions"
        )
    if requester_l2_unused_pattern > requester_l2_unused:
        raise SystemExit(
            "unused PATTERN retirements exceed unused requester-L2 lines"
        )
    base_physical_reads = total_prefetch("baseline_dram_physical_read_accesses")
    complete_physical_reads = total_prefetch("complete_dram_physical_read_accesses")
    base_physical_writes = total_prefetch(
        "baseline_dram_physical_write_accesses"
    )
    complete_physical_writes = total_prefetch(
        "complete_dram_physical_write_accesses"
    )

    def total_work(field: str) -> float:
        return sum(number(work[b], field) or 0 for b in expected)

    remote_duplicate_reads = total_work("remote_duplicate_reads_eliminated")
    total_l2_filter_queries = total_work("l2_resident_filter_queries")
    total_l2_tag_skips = total_work("l2_tag_lookups_skipped")
    baseline_complete_mshr = total_work(
        "baseline_l2_mshr_full_stall_cycles"
    )
    complete_complete_mshr = total_work(
        "complete_l2_mshr_full_stall_cycles"
    )

    def weighted_work_average(total_field: str, sample_field: str) -> float | None:
        return ratio(total_work(total_field), total_work(sample_field))

    def max_work(field: str) -> float:
        return max(number(work[b], field) or 0 for b in expected)

    remote_l2_bypasses = total_work("remote_l2_probe_bypasses")
    remote_l2_probe_hits = total_work("remote_l2_probe_hits")
    remote_l2_probe_misses = total_work("remote_l2_probe_misses")
    remote_l2_logical_responses = total_work(
        "repeated_wafer_traversals_eliminated"
    )
    if remote_l2_logical_responses < remote_l2_probe_hits:
        raise SystemExit(
            "logical requester-L2 responses are fewer than exact probe hits"
        )
    remote_l2_opportunities = (
        remote_l2_bypasses + remote_l2_probe_hits + remote_l2_probe_misses
    )
    l2_miss_issue_avg = weighted_work_average(
        "l2_miss_to_dram_issue_total_ns", "l2_miss_to_dram_issue_samples"
    )
    baseline_l2_issue_avg = weighted_work_average(
        "baseline_l2_miss_to_dram_issue_total_ns",
        "baseline_l2_miss_to_dram_issue_samples",
    )
    l2_fast_issue_avg = weighted_work_average(
        "l2_fast_miss_to_dram_issue_total_ns",
        "l2_fast_miss_to_dram_issue_samples",
    )
    complete_l2_combined_issue_avg = weighted_work_average(
        "complete_l2_combined_issue_total_ns",
        "complete_l2_combined_issue_samples",
    )
    remote_read_latency_avg = weighted_work_average(
        "remote_logical_read_latency_total_ns",
        "remote_logical_read_latency_samples",
    )
    remote_batch_wait_avg = weighted_work_average(
        "remote_batch_queue_wait_total_ns", "remote_batch_queue_wait_samples"
    )
    remote_network_wait_avg = weighted_work_average(
        "remote_pre_network_wait_total_ns", "remote_pre_network_wait_samples"
    )
    remote_probe_latency_avg = weighted_work_average(
        "remote_probe_latency_total_ns", "remote_probe_latency_samples"
    )

    def controller_values(config: str, field: str) -> list[float]:
        return [
            float(number(controller[(benchmark, config)], field) or 0.0)
            for benchmark in expected
        ]

    def mean(values: list[float]) -> float | None:
        return None if not values else sum(values) / len(values)

    base_issue = controller_values("baseline", "dram_column_commands_per_instance_cycle")
    m1_issue = controller_values("m1", "dram_column_commands_per_instance_cycle")
    complete_issue = controller_values("complete", "dram_column_commands_per_instance_cycle")
    m1_issue_ratio = geomean(
        after / before for before, after in zip(base_issue, m1_issue) if before > 0
    )
    complete_issue_ratio = geomean(
        after / before
        for before, after in zip(base_issue, complete_issue)
        if before > 0
    )
    base_queue_age = controller_values("baseline", "dram_max_queue_age_cycles")
    m1_queue_age = controller_values("m1", "dram_max_queue_age_cycles")
    complete_queue_age = controller_values("complete", "dram_max_queue_age_cycles")
    base_command_cv = controller_values("baseline", "dram_column_command_cv")
    m1_command_cv = controller_values("m1", "dram_column_command_cv")
    complete_command_cv = controller_values("complete", "dram_column_command_cv")
    m1_candidate_cv = [
        value
        for value in controller_values("m1", "prefetch_candidate_cv")
        if value > 0
    ]
    complete_candidate_cv = [
        value
        for value in controller_values("complete", "prefetch_candidate_cv")
        if value > 0
    ]

    def total_m1(config: str, field: str) -> float:
        return sum(
            number(m1[b], f"{config}_{field}") or 0 for b in expected
        )

    baseline_m1_reads = total_m1("baseline", "dram_physical_read_accesses")
    m1_reads = total_m1("m1", "dram_physical_read_accesses")
    baseline_m1_writes = total_m1("baseline", "dram_physical_write_accesses")
    m1_writes = total_m1("m1", "dram_physical_write_accesses")
    baseline_m1_l2_dram_reads = total_m1(
        "baseline", "l2_to_dram_64b_requests"
    )
    m1_l2_dram_reads = total_m1("m1", "l2_to_dram_64b_requests")
    baseline_m1_mshr = total_m1("baseline", "l2_mshr_full_stall_cycles")
    m1_mshr = total_m1("m1", "l2_mshr_full_stall_cycles")
    baseline_m1_issue_samples = total_m1(
        "baseline", "l2_miss_to_dram_issue_samples"
    )
    baseline_m1_issue_total = total_m1(
        "baseline", "l2_miss_to_dram_issue_total_ns"
    )
    baseline_m1_demand_latency_samples = total_m1(
        "baseline", "l2_demand_read_latency_samples"
    )
    baseline_m1_demand_latency_total = total_m1(
        "baseline", "l2_demand_read_latency_total_ns"
    )
    m1_issue_samples = (
        total_m1("m1", "l2_miss_to_dram_issue_samples")
        + total_m1("m1", "l2_fast_miss_to_dram_issue_samples")
    )
    m1_issue_total = (
        total_m1("m1", "l2_miss_to_dram_issue_total_ns")
        + total_m1("m1", "l2_fast_miss_to_dram_issue_total_ns")
    )
    m1_demand_latency_samples = total_m1(
        "m1", "l2_demand_read_latency_samples"
    )
    m1_demand_latency_total = total_m1(
        "m1", "l2_demand_read_latency_total_ns"
    )
    m1_issued = total_m1("m1", "filter_prefetch_issued")
    m1_outstanding = total_m1("m1", "filter_prefetch_outstanding")
    m1_real_demands = total_m1("m1", "filter_prefetch_real_demands")
    m1_candidates = total_m1("m1", "filter_prefetch_candidates")
    m1_pattern_installs = total_m1("m1", "filter_prefetch_pattern_installs")
    m1_pattern_install_drops = total_m1(
        "m1", "filter_prefetch_pattern_install_drops"
    )
    m1_evidence_one = total_m1(
        "m1", "filter_prefetch_predictor_evidence_one"
    )
    m1_evidence_two = total_m1(
        "m1", "filter_prefetch_predictor_evidence_two"
    )
    m1_stride = {
        bucket: total_m1("m1", f"filter_prefetch_stride_{bucket}")
        for bucket in ("one", "small", "medium", "large", "negative")
    }
    m1_gate_drops = {
        "predictor busy": total_m1(
            "m1", "filter_prefetch_candidate_busy_drops"
        ),
        "PATTERN negative": total_m1(
            "m1", "filter_prefetch_pattern_negative_drops"
        ),
        "RESIDENT positive": total_m1(
            "m1", "filter_prefetch_resident_positive_drops"
        ),
        "PENDING positive": total_m1(
            "m1", "filter_prefetch_pending_positive_drops"
        ),
        "wrong target slice": total_m1(
            "m1", "filter_prefetch_wrong_slice_drops"
        ),
        "demand priority": total_m1(
            "m1", "filter_prefetch_demand_priority_drops"
        ),
        "lower-path input busy (legacy controller name)": total_m1(
            "m1", "filter_prefetch_controller_busy_drops"
        ),
        "output busy": total_m1("m1", "filter_prefetch_output_busy_drops"),
        "MSHR unavailable": total_m1("m1", "filter_prefetch_mshr_drops"),
        "demand MSHR headroom": total_m1(
            "m1", "filter_prefetch_mshr_headroom_drops"
        ),
        "all bank opportunities busy": total_m1(
            "m1", "filter_prefetch_demand_path_busy_drops"
        ),
        "no invalid victim": total_m1(
            "m1", "filter_prefetch_victim_drops"
        ),
        "PENDING insert": total_m1(
            "m1", "filter_prefetch_pending_insert_drops"
        ),
    }
    m1_redundant_races = total_m1(
        "m1", "filter_prefetch_redundant_races"
    )
    m1_fills = total_m1("m1", "filter_prefetch_fills")
    m1_extra_dram = total_m1(
        "m1", "filter_prefetch_additional_dram_reads"
    )
    m1_demand_dram_reads = m1_l2_dram_reads - m1_extra_dram
    if m1_demand_dram_reads < 0:
        raise SystemExit(
            "M1 prefetch DRAM reads exceed all reported L2-to-DRAM reads"
        )
    m1_useful = total_m1("m1", "filter_prefetch_useful")
    m1_timely = total_m1("m1", "filter_prefetch_timely")
    m1_late = total_m1("m1", "filter_prefetch_late")
    m1_late_after_dram = total_m1(
        "m1", "filter_prefetch_late_after_dram_issue"
    )
    m1_demand_won = total_m1(
        "m1", "filter_prefetch_demand_won_races"
    )
    m1_unused = total_m1("m1", "filter_prefetch_unused")
    m1_unused_evictions = total_m1(
        "m1", "filter_prefetch_unused_evictions"
    )
    m1_unused_resets = total_m1(
        "m1", "filter_prefetch_unused_reset_retirements"
    )
    m1_peak_prefetch_only = max(
        number(m1[b], "m1_filter_prefetch_peak_prefetch_only_lines") or 0
        for b in expected
    )
    m1_current_prefetch_only = max(
        number(m1[b], "m1_filter_prefetch_current_prefetch_only_lines") or 0
        for b in expected
    )
    m1_delay = total_m1("m1", "filter_prefetch_demand_delay_events")
    if m1_issued != m1_fills + m1_redundant_races + m1_outstanding:
        raise SystemExit(
            "M1 issued prefetches do not partition into fills, redundant "
            "races, and runtime-stop outstanding requests"
        )
    pollution_metrics_present = all(
        number(m1[b], f"m1_{field}") is not None
        for b in expected
        for field in (
            "filter_prefetch_unused_evictions",
            "filter_prefetch_unused_reset_retirements",
            "filter_prefetch_peak_prefetch_only_lines",
            "filter_prefetch_current_prefetch_only_lines",
        )
    )
    if pollution_metrics_present and m1_unused != (
        m1_unused_evictions + m1_unused_resets
    ):
        raise SystemExit(
            "M1 unused retirements do not partition into eviction and reset"
        )

    def total_stage(
        rows: dict[str, dict[str, str]], field: str
    ) -> float:
        return sum(number(rows[b], field) or 0 for b in expected)

    m2_logical_reads = total_stage(m2, "remote_logical_reads")
    m2_duplicate_reads = total_stage(m2, "remote_duplicate_reads")
    m2_wire_lines = total_stage(m2, "remote_wire_lines")
    m2_demand_wire_lines = total_stage(m2, "remote_demand_wire_lines")
    m2_prefetch_wire_lines = total_stage(m2, "remote_prefetch_wire_lines")
    m2_candidates = total_stage(m2, "remote_prefetch_candidates")
    m2_piggybacks = total_stage(m2, "remote_prefetch_piggyback_lines")
    m2_single_packets = total_stage(m2, "remote_single_packets")
    m2_bitmap_packets = total_stage(m2, "remote_bitmap_packets")
    m2_width_stalls = sum(
        total_stage(m2, field)
        for field in (
            "remote_requester_issue_width_stalls",
            "remote_response_fanout_width_stalls",
            "remote_owner_issue_width_stalls",
            "remote_owner_response_width_stalls",
        )
    )
    m2_physical_reads = total_stage(m2, "dram_physical_read_accesses")
    m2_physical_writes = total_stage(m2, "dram_physical_write_accesses")

    m3_logical_reads = total_stage(m3, "remote_logical_reads")
    m3_probe_bypasses = total_stage(
        m3, "remote_l2_one_touch_probe_bypasses"
    )
    m3_probe_hits = total_stage(m3, "remote_l2_probe_hits")
    m3_probe_misses = total_stage(m3, "remote_l2_probe_misses")
    m3_l2_hits = total_stage(m3, "remote_requester_l2_hits")
    m3_installed = total_stage(m3, "remote_l2_two_touch_installed_fills")
    m3_unused = total_stage(m3, "remote_requester_l2_unused_fills")
    m3_local_displacements = total_stage(
        m3, "remote_fill_displaced_local_clean"
    )
    m3_peak_lines = max(
        number(m3[b], "remote_requester_l2_peak_lines") or 0
        for b in expected
    )
    m3_physical_reads = total_stage(m3, "dram_physical_read_accesses")
    m3_physical_writes = total_stage(m3, "dram_physical_write_accesses")
    if m2_duplicate_reads > m2_logical_reads:
        raise SystemExit("M2 duplicate reads exceed logical remote reads")
    if m2_piggybacks > m2_candidates:
        raise SystemExit("M2 piggybacked candidates exceed generated candidates")
    if m3_unused > m3_installed:
        raise SystemExit("M3 unused fills exceed installed requester-L2 fills")

    aggregate = rows_by_key(root / "cupath_work_reduction_summary.csv").get("Weighted", {})
    if not aggregate:
        aggregate = rows_by_key(root / "cupath_work_reduction_summary.csv").get("Weighted*", {})

    footprint_values = [float(row["workload_footprint_mib"]) for row in detail]
    cap_reached = sum(
        float(row["observed_workgroups"]) == 76800 for row in detail
    )

    lines = [
        "# CuPath formal 14-workload analysis",
        "",
        f"All 70 cells are present. Positive is > {POSITIVE_THRESHOLD:.3f}x, "
        f"negative is < {NEGATIVE_THRESHOLD:.3f}x, and the interval between is neutral.",
        "",
        "## End-to-end performance",
        "",
        "Every cell returned zero and the runner-side tracer observed the "
        "same runtime window without changing the full requested grid or "
        "original Driver partition. All commands use the one frozen binary "
        f"`{binary_path}` (SHA-256 `{binary_hash}`), and the metadata audit "
        "accepts the fixed 16-MSHR, four-bank, sampled 14x5 flag matrix.",
        f"The 4-KB allocation counters are identical across configurations. "
        f"Measured workload footprints span {min(footprint_values):.0f}--"
        f"{max(footprint_values):.0f} MiB; {cap_reached} workloads reach the "
        "76,800-WG observation limit.",
        "",
        "| Configuration | 14-workload geomean |",
        "|---|---:|",
    ]
    for config, label, _ in CONFIGS:
        lines.append(f"| {label} | {geomean(config_values[config]):.4f}x |")
    lines += [
        "",
        f"Complete: {positive} positive, {neutral} neutral, and {negative} negative workloads.",
        f"The requested 1.5x target is {'met' if geomean(complete_values) >= 1.5 else 'not met'}; "
        f"the measured geomean is {geomean(complete_values):.4f}x.",
        "",
        "| Group | Complete geomean |",
        "|---|---:|",
    ]
    for group in (
        "Exact-local", "Local-dominant", "Mixed", "Remote-dominant"
    ):
        values = groups.get(group, [])
        lines.append(
            f"| {group} | {fmt(geomean(values), 'x', 4) if values else '--'} |"
        )

    lines += [
        "",
        "The traffic classes are generated from the separately profiled "
        "Baseline request stream using thresholds frozen before V6 results: "
        "0% is Exact-local, (0%, 25%] Local-dominant, (25%, 75%] Mixed, "
        "and above 75% Remote-dominant. All classes remain in the overall "
        "fourteen-workload geomean.",
        "",
        "| Benchmark | Baseline remote requests | Class |",
        "|---|---:|---|",
    ]
    for row in detail:
        lines.append(
            f"| {row['label']} | "
            f"{fmt(row['baseline_remote_request_pct'], '%', 3)} | "
            f"{row['group']} |"
        )

    lines += [
        "",
        "## Standalone scope and negative evidence",
        "",
        "| Configuration | Positive | Neutral | Negative | Non-positive workloads |",
        "|---|---:|---:|---:|---|",
    ]
    for config, label, _ in CONFIGS[1:4]:
        values = config_values[config]
        config_positive = sum(value > POSITIVE_THRESHOLD for value in values)
        config_negative = sum(value < NEGATIVE_THRESHOLD for value in values)
        config_neutral = len(values) - config_positive - config_negative
        non_positive = [
            f"{row['label']} {float(row[f'{config}_speedup']):.3f}x"
            for row in detail
            if float(row[f"{config}_speedup"]) <= POSITIVE_THRESHOLD
        ]
        lines.append(
            f"| {label} | {config_positive} | {config_neutral} | "
            f"{config_negative} | {', '.join(non_positive) or '--'} |"
        )
    lines += [
        "",
        "These standalone outcomes bound each stage's scope; Complete is not "
        "used to relabel a neutral or regressing standalone mechanism as "
        "independently effective.",
    ]

    lines += [
        "",
        "Simulator wall-clock ratios are reported for reproducibility; cells "
        "were co-scheduled, so these are not hardware performance metrics.",
        "",
        "| Configuration | Simulator wall time / Baseline |",
        "|---|---:|",
    ]
    for config, label, _ in CONFIGS:
        values = runtime_values[config]
        value = geomean(values) if len(values) == len(expected) else None
        lines.append(f"| {label} | {fmt(value, 'x', 3)} |")

    lines += [
        "",
        "## Per-workload result and removed work",
        "",
        "| Benchmark | M1 | M2 | M3 | Complete | Class | L2 tag | Remote read | Packet | Traversal |",
        "|---|---:|---:|---:|---:|---|---:|---:|---:|---:|",
    ]
    for row in detail:
        lines.append(
            f"| {row['label']} | {float(row['m1_speedup']):.3f} | "
            f"{float(row['m2_speedup']):.3f} | {float(row['m3_speedup']):.3f} | "
            f"{float(row['complete_speedup']):.3f} | {row['classification']} | "
            f"{fmt(row['l2_tag_removed_pct'], '%', 1)} | "
            f"{fmt(row['remote_reads_removed_pct'], '%', 1)} | "
            f"{fmt(row['wire_packets_removed_pct'], '%', 1)} | "
            f"{fmt(row['traversals_removed_pct'], '%', 1)} |"
        )

    lines += [
        "",
        "Weighted work removed along the request path: "
        f"{fmt(number(aggregate, 'l2_tag_work_removed_pct'), '%', 1)} L2 tag work, "
        f"{fmt(number(aggregate, 'remote_reads_deduplicated_pct'), '%', 1)} remote reads, "
        f"{fmt(number(aggregate, 'wire_packets_removed_pct'), '%', 1)} wire packets, and "
        f"{fmt(number(aggregate, 'repeated_traversals_removed_pct'), '%', 1)} repeated wafer traversals.",
        f"In absolute terms, Complete issues {total_l2_filter_queries:,.0f} "
        f"RESIDENT membership queries and skips {total_l2_tag_skips:,.0f} "
        "L2 tag lookups. Aggregate L2 MSHR-full stall cycles change from "
        f"{baseline_complete_mshr:,.0f} in Baseline to "
        f"{complete_complete_mshr:,.0f} in Complete "
        f"({fmt(reduction(baseline_complete_mshr, complete_complete_mshr), '%', 2)} reduction; "
        "a negative value denotes added pressure).",
        "",
        "## Demand latency and controller pressure",
        "",
        "The sample-weighted L2-miss-to-DRAM-issue latency is "
        f"{fmt(baseline_l2_issue_avg, ' ns', 2)} in Baseline and "
        f"{fmt(complete_l2_combined_issue_avg, ' ns', 2)} across all ordinary "
        "and Filter-negative Complete issues. Within Complete, ordinary misses "
        f"average {fmt(l2_miss_issue_avg, ' ns', 2)} and reliable negative "
        f"fast misses average {fmt(l2_fast_issue_avg, ' ns', 2)}. The remote logical-read path averages "
        f"{fmt(remote_read_latency_avg, ' ns', 2)}. Across workloads, the "
        f"maximum observed Baseline/Complete local-issue latencies are "
        f"{max_work('baseline_l2_miss_to_dram_issue_max_ns'):.0f}/"
        f"{max_work('complete_l2_combined_issue_max_ns'):.0f} ns; the maximum remote-read latency is "
        f"{max_work('remote_logical_read_latency_max_ns'):.0f} ns. Within the remote path, existing "
        f"batch collection averages {fmt(remote_batch_wait_avg, ' ns', 2)}, "
        f"pre-network waiting averages {fmt(remote_network_wait_avg, ' ns', 2)}, "
        f"and exact requester-L2 probes average {fmt(remote_probe_latency_avg, ' ns', 2)}. "
        "These are absolute Complete-path measurements because the legacy "
        "Baseline RDMA path does not export the same logical-request samples.",
        "",
        "All 70 cells expose 192 modeled DRAM controller/bank instances and "
        "192 L2 slices. Relative to Baseline, the geometric mean of the "
        "per-instance-cycle DRAM column-command issue proxy is "
        f"{fmt(m1_issue_ratio, 'x', 3)} for M1 and "
        f"{fmt(complete_issue_ratio, 'x', 3)} for Complete. The mean of each "
        "workload's maximum controller queue age is "
        f"{fmt(mean(base_queue_age), ' cycles', 1)} in Baseline, "
        f"{fmt(mean(m1_queue_age), ' cycles', 1)} in M1, and "
        f"{fmt(mean(complete_queue_age), ' cycles', 1)} in Complete; the "
        f"largest observed values are {max(base_queue_age):.0f}, "
        f"{max(m1_queue_age):.0f}, and {max(complete_queue_age):.0f} cycles. "
        "The corresponding mean command-count CVs across controllers are "
        f"{fmt(mean(base_command_cv), '', 3)}, "
        f"{fmt(mean(m1_command_cv), '', 3)}, and "
        f"{fmt(mean(complete_command_cv), '', 3)}. Raw per-controller and "
        "per-slice rows remain in `cupath_controller_slice_detail.csv`; "
        f"candidate-distribution CV averages are {fmt(mean(m1_candidate_cv), '', 3)} "
        f"for M1 and {fmt(mean(complete_candidate_cv), '', 3)} for Complete.",
        "",
        "## Closed-loop prefetch evidence",
        "",
        "The two distributed predictor sites share one bounded design and "
        "train only on real demands. Evidence-one/evidence-two are cumulative "
        "real-demand transitions that first establish a nonzero stride and "
        "then confirm its repetition; stride buckets count generated "
        "candidates, not additional memory-width transfers.",
        "",
        "| Stage | Real demands | Candidates | PATTERN install/drop | Evidence 1 | Evidence 2 | Candidate stride (+1/small/medium/large/negative) |",
        "|---|---:|---:|---:|---:|---:|---:|",
        f"| Local | {local_real_demands:,.0f} | {local_candidates:,.0f} | "
        f"{local_pattern_installs:,.0f}/{local_pattern_install_drops:,.0f} | "
        f"{local_evidence_one:,.0f} | {local_evidence_two:,.0f} | "
        f"{local_stride['one']:,.0f}/{local_stride['small']:,.0f}/"
        f"{local_stride['medium']:,.0f}/{local_stride['large']:,.0f}/"
        f"{local_stride['negative']:,.0f} |",
        f"| Remote | {remote_real_demands:,.0f} | {remote_candidates:,.0f} | "
        f"{remote_pattern_installs:,.0f}/{remote_pattern_install_drops:,.0f} | "
        f"{remote_evidence_one:,.0f} | {remote_evidence_two:,.0f} | "
        f"{remote_stride['one']:,.0f}/{remote_stride['small']:,.0f}/"
        f"{remote_stride['medium']:,.0f}/{remote_stride['large']:,.0f}/"
        f"{remote_stride['negative']:,.0f} |",
        "",
        "| Stage | Candidates | Issued/piggybacked | Outstanding | Demand-consumed | Timely/late | Unused |",
        "|---|---:|---:|---:|---:|---:|---:|",
        f"| Local | {local_candidates:,.0f} | {local_issued:,.0f} | {local_outstanding:,.0f} | {local_useful:,.0f} | {local_timely:,.0f}/{local_late:,.0f} | {local_unused:,.0f} |",
        f"| Remote | {remote_candidates:,.0f} | {remote_piggyback:,.0f} | n/a | {remote_useful:,.0f} | n/a | {remote_unused:,.0f} |",
        "",
        f"Local candidate/issue/useful/timely coverage over real demands is "
        f"{fmt(percent(local_candidates, local_real_demands), '%', 2)}/"
        f"{fmt(percent(local_issued, local_real_demands), '%', 2)}/"
        f"{fmt(percent(local_useful, local_real_demands), '%', 2)}/"
        f"{fmt(percent(local_timely, local_real_demands), '%', 2)}. "
        f"Of all candidates, {fmt(percent(local_issued, local_candidates), '%', 2)} issue; "
        f"useful-per-issued accuracy is {fmt(percent(local_useful, local_issued), '%', 2)} "
        f"({fmt(percent(local_timely, local_useful), '%', 2)} timely and "
        f"{fmt(percent(local_late, local_useful), '%', 2)} late among consumed lines). "
        f"Of those late merges, {local_late_after_dram:,.0f} occur after DRAM issue; "
        f"{local_demand_won:,.0f} additional candidates lose to demand before supplying data, and "
        f"{fmt(percent(local_unused, local_issued), '%', 2)} of issued lines retire unused. "
        f"Another {local_outstanding:,.0f} issued requests are still in flight at "
        "the runtime-stop boundary and are right-censored rather than classified "
        "as useful or unused. Remote "
        f"candidate coverage is {fmt(percent(remote_candidates, remote_real_demands), '%', 2)}; "
        f"the Filter rejects {fmt(percent(remote_filter_drops, remote_candidates), '%', 2)}, "
        f"{fmt(percent(remote_piggyback, remote_candidates), '%', 2)} piggyback an existing batch, "
        f"and {fmt(percent(remote_useful, remote_candidates), '%', 2)} of all "
        "generated candidates are eventually consumed by a real demand. "
        "Useful and piggybacked are overlapping, not nested, populations: a "
        "demand can take over a candidate before batch insertion, so their "
        "intersection is not reconstructed from aggregate counters. Remote "
        "unused counts candidates retired before installation; installed "
        "lines that later retire unused are counted separately by requester-L2 "
        "eviction statistics.",
        "",
        "Candidates are work-conserving: every failed gate drops the "
        "candidate immediately and does not delay the demand or create a "
        "batch. The following counters expose where opportunities end. They "
        "are diagnostic categories rather than a partition: for example, a "
        "full existing batch is also a capacity drop.",
        "",
        "| M1 immediate-drop gate | Candidates |",
        "|---|---:|",
    ]
    for reason, count in local_gate_drops.items():
        lines.append(f"| {reason} | {count:,.0f} |")
    lines += [
        "",
        "| M2 immediate-drop gate | Candidates |",
        "|---|---:|",
    ]
    for reason, count in remote_gate_drops.items():
        lines.append(f"| {reason} | {count:,.0f} |")
    lines += [
        "",
        "| Bounded RDMA stage | Width-stall events |",
        "|---|---:|",
    ]
    for stage, count in rdma_width_stalls.items():
        lines.append(f"| {stage} | {count:,.0f} |")
    lines += [
        "",
        f"Local candidates produce {local_demand_merges:,.0f} exact demand merges, "
        f"{local_extra_dram:,.0f} additional DRAM reads, and {local_delay:,.0f} "
        "demand-delay exposure events (a demand encounters MSHR-full while a "
        "local prefetch is outstanding; this is correlation, not causal "
        "attribution). Remote candidates add "
        f"{remote_extra_owner:,.0f} owner reads. The remote lifecycle records "
        f"{remote_pre_send_merges:,.0f} pre-send, "
        f"{remote_inflight_merges:,.0f} in-flight, and "
        f"{remote_ready_merges:,.0f} ready-response exact merges, followed by "
        f"{remote_fanout_responses:,.0f} logical response fanouts. It also "
        f"credits {remote_standalone_prevented:,.0f} candidate standalone "
        "requests suppressed before issue by requester-L2 hits or the "
        "existing-batch-only admission rule, and adds "
        f"{remote_added_response_bytes:,.0f} response bytes. We do not claim "
        "a candidate-specific avoided-demand count; exact demand deduplication "
        "and packet aggregation are instead established by the separate "
        "removed-work counters. The existing requester L2 "
        f"serves {requester_l2_hits:,.0f} useful hits and retires "
        f"{requester_l2_unused:,.0f} unused remote fills. It installs "
        f"{requester_l2_installed:,.0f} remote-clean lines; "
        f"{fmt(percent(requester_l2_unused, requester_l2_installed), '%', 2)} "
        "are observed to retire unused, a lower bound because lines still "
        "resident at simulation end are not classified. Installed fills "
        f"partition into {requester_l2_into_invalid:,.0f} invalid ways and "
        f"{requester_l2_replaced_remote:,.0f} replacements of older "
        f"remote-clean lines; {requester_l2_evictions:,.0f} are later "
        f"evicted and {requester_l2_current_lines:,.0f} remain resident at "
        "the ends of their independent runs. The largest workload-level sum of per-slice peak "
        "remote occupancy is "
        f"{requester_l2_peak_lines:,.0f} lines "
        f"({fmt(percent(requester_l2_peak_lines, 192 * 1024 * 1024 / 64), '%', 3)} "
        "of baseline L2 data lines, a conservative non-simultaneous upper "
        f"bound). Remote fills displace "
        f"{requester_l2_displaced_local:,.0f} ordinary local-clean lines "
        f"({requester_l2_local_displacements:,.0f} two-touch local "
        "displacements). Of the unused retirements, "
        f"{requester_l2_unused_pattern:,.0f} carry candidate provenance and "
        "delete the associated PATTERN. A useful patterned line instead "
        "retains PATTERN, but the aggregate long-run counters do not isolate "
        "that subset from all requester-L2 hits.",
        "",
        f"Remote candidate control rejects {remote_filter_drops:,.0f} "
        "candidates at the shared Filter. For M3's requester-L2 gate, "
        f"reliable negative metadata bypasses {remote_l2_bypasses:,.0f} "
        f"exact probes ({fmt(percent(remote_l2_bypasses, remote_l2_opportunities), '%', 2)} "
        "of eligible unique-request probe opportunities); the probes that "
        f"remain produce {remote_l2_probe_hits:,.0f} hits and "
        f"{remote_l2_probe_misses:,.0f} misses. Those physical hit probes "
        f"serve {remote_l2_logical_responses:,.0f} logical responses because "
        "an exact line entry can fan out to multiple waiters; the latter is "
        "the repeated-traversal count. This proves removed L2 work, "
        "not an independently isolated timing gain versus always-probe M3.",
        "",
        f"Physical DRAM reads change from {base_physical_reads:,.0f} in Baseline "
        f"to {complete_physical_reads:,.0f} in Complete "
        f"({fmt(reduction(base_physical_reads, complete_physical_reads), '%', 2)} reduction; "
        "a negative reduction denotes added work). Physical writes change "
        f"from {base_physical_writes:,.0f} to {complete_physical_writes:,.0f} "
        f"({fmt(reduction(base_physical_writes, complete_physical_writes), '%', 2)} "
        "reduction).",
        "",
        "## M1-specific resource attribution",
        "",
        "The M1-only configuration is the correct comparison for local "
        "prediction pressure; Complete also changes remote arrival timing. "
        f"Across fourteen workloads, M1 issues {m1_issued:,.0f} local "
        f"prefetches: {m1_fills:,.0f} fill and {m1_redundant_races:,.0f} "
        f"lose a race to an ordinary demand, while {m1_outstanding:,.0f} "
        "remain in flight at the runtime-stop boundary. Of the fills, "
        f"{m1_extra_dram:,.0f} reach the DRAM issue point. The mechanism "
        f"records {m1_useful:,.0f} useful ({m1_timely:,.0f} timely and "
        f"{m1_late:,.0f} prefetch-led MSHR merges, including "
        f"{m1_late_after_dram:,.0f} after DRAM issue), "
        f"{m1_demand_won:,.0f} demand-won races, and "
        f"{m1_unused:,.0f} unused retirements, and observes "
        f"{m1_delay:,.0f} demand-delay "
        "exposure events.",
        f"M1 candidate/issue/useful/timely coverage over real demands is "
        f"{fmt(percent(m1_candidates, m1_real_demands), '%', 2)}/"
        f"{fmt(percent(m1_issued, m1_real_demands), '%', 2)}/"
        f"{fmt(percent(m1_useful, m1_real_demands), '%', 2)}/"
        f"{fmt(percent(m1_timely, m1_real_demands), '%', 2)}; useful-per-issued "
        f"accuracy is {fmt(percent(m1_useful, m1_issued), '%', 2)}, and "
        f"{fmt(percent(m1_timely, m1_useful), '%', 2)} of useful prefetches "
        "are timely. Outstanding requests are right-censored and excluded "
        "from useful/unused conclusions.",
        f"Unused retirements partition into {m1_unused_evictions:,.0f} "
        f"runtime evictions and {m1_unused_resets:,.0f} cache-reset/end-of-run "
        "retirements. The largest summed-slice peak of completed but not-yet-"
        f"used prefetch lines is {m1_peak_prefetch_only:,.0f} "
        f"({fmt(percent(m1_peak_prefetch_only, 192 * 1024 * 1024 / 64), '%', 3)} "
        "of wafer L2 capacity); the largest end-of-report population is "
        f"{m1_current_prefetch_only:,.0f}. Runtime evictions are the direct "
        "pollution signal, while reset retirements are censored reuse outcomes.",
        "",
        "| M1-only predictor evidence | Count |",
        "|---|---:|",
        f"| Real demands / candidates | {m1_real_demands:,.0f} / {m1_candidates:,.0f} |",
        f"| PATTERN install / drop | {m1_pattern_installs:,.0f} / {m1_pattern_install_drops:,.0f} |",
        f"| Evidence one / two | {m1_evidence_one:,.0f} / {m1_evidence_two:,.0f} |",
        f"| Candidate stride +1/small/medium/large/negative | "
        f"{m1_stride['one']:,.0f}/{m1_stride['small']:,.0f}/"
        f"{m1_stride['medium']:,.0f}/{m1_stride['large']:,.0f}/"
        f"{m1_stride['negative']:,.0f} |",
        "",
        "| M1-only immediate-drop gate | Candidates |",
        "|---|---:|",
    ]
    for reason, count in m1_gate_drops.items():
        lines.append(f"| {reason} | {count:,.0f} |")
    lines += [
        "",
        f"Baseline-to-M1 physical reads change from {baseline_m1_reads:,.0f} "
        f"to {m1_reads:,.0f}; physical writes change from "
        f"{baseline_m1_writes:,.0f} to {m1_writes:,.0f}; and L2 MSHR-full "
        f"stall cycles change from {baseline_m1_mshr:,.0f} to "
        f"{m1_mshr:,.0f}. Sample-weighted L2-to-DRAM issue latency changes "
        f"from {fmt(ratio(baseline_m1_issue_total, baseline_m1_issue_samples), ' ns', 2)} "
        f"to {fmt(ratio(m1_issue_total, m1_issue_samples), ' ns', 2)} when "
        "ordinary and Filter-negative M1 issues are combined. Actual "
        "L2 demand-read latency (request accepted through response sent) "
        f"changes from {fmt(ratio(baseline_m1_demand_latency_total, baseline_m1_demand_latency_samples), ' ns', 2)} "
        f"to {fmt(ratio(m1_demand_latency_total, m1_demand_latency_samples), ' ns', 2)}. The explicit "
        f"L2-to-DRAM split is {baseline_m1_l2_dram_reads:,.0f} Baseline "
        f"demand reads versus {m1_demand_dram_reads:,.0f} M1 demand reads "
        f"plus {m1_extra_dram:,.0f} M1 prefetch reads "
        f"({m1_l2_dram_reads:,.0f} total). The explicit additional-read "
        "counter describes prefetch requests that reached the lower-module "
        "issue point; it is not expected to equal the net "
        "Baseline-to-M1 physical-read delta because demand merging and timing "
        "also change which ordinary requests reach DRAM. These totals "
        "include KMeans, whose long execution "
        "dominates the absolute MSHR counter; per-workload raw values remain "
        "in `cupath_m1_attribution.csv`.",
        "",
        "## M2/M3 standalone attribution",
        "",
        "These counters come from the standalone M2 and M3 cells, not from "
        "Complete. They expose each stage's own request population and are "
        "not added to the Complete counters.",
        "",
        "| M2-only evidence | Count |",
        "|---|---:|",
        f"| Logical remote reads / exact duplicate reads | {m2_logical_reads:,.0f} / {m2_duplicate_reads:,.0f} |",
        f"| Wire lines (demand / piggyback prefetch) | {m2_wire_lines:,.0f} ({m2_demand_wire_lines:,.0f} / {m2_prefetch_wire_lines:,.0f}) |",
        f"| Generated / existing-batch-piggybacked candidates | {m2_candidates:,.0f} / {m2_piggybacks:,.0f} |",
        f"| Single-line / bitmap packets | {m2_single_packets:,.0f} / {m2_bitmap_packets:,.0f} |",
        f"| Bounded requester/owner width-stall events | {m2_width_stalls:,.0f} |",
        f"| Physical DRAM reads / writes | {m2_physical_reads:,.0f} / {m2_physical_writes:,.0f} |",
        "",
        "| M3-only evidence | Count |",
        "|---|---:|",
        f"| Logical remote reads | {m3_logical_reads:,.0f} |",
        f"| Probe bypass / hit / miss | {m3_probe_bypasses:,.0f} / {m3_probe_hits:,.0f} / {m3_probe_misses:,.0f} |",
        f"| Requester-L2 hits | {m3_l2_hits:,.0f} |",
        f"| Installed / observed-unused remote-clean lines | {m3_installed:,.0f} / {m3_unused:,.0f} |",
        f"| Ordinary local-clean displacements | {m3_local_displacements:,.0f} |",
        f"| Largest workload-level requester-L2 peak | {m3_peak_lines:,.0f} lines |",
        f"| Physical DRAM reads / writes | {m3_physical_reads:,.0f} / {m3_physical_writes:,.0f} |",
        "",
        "For reference, the corresponding fourteen Baseline cells perform "
        f"{base_physical_reads:,.0f} physical reads and "
        f"{base_physical_writes:,.0f} physical writes. Remote logical "
        "populations are not compared directly with the legacy Baseline RDMA "
        "arrival counter because latency changes upstream MSHR merging.",
        "",
        "## Typed Filter evidence",
        "",
        f"Across the fourteen Complete runs, the four key classes issue {all_queries:,.0f} queries, "
        f"observe {all_fps:,.0f} analysis-confirmed false positives "
        f"({100.0 * all_fps / all_queries if all_queries else 0.0:.6f}%), and record "
        f"{all_failures:,.0f} insertion failures.",
        "",
        "| Key type | Queries | Positives | False positives | Insert failures | Reliable slice-runs |",
        "|---|---:|---:|---:|---:|---:|",
    ]
    for kind, (queries, positives, fps, failures, reliable) in filter_totals.items():
        lines.append(
            f"| {kind.upper()} | {queries:,.0f} | {positives:,.0f} | "
            f"{fps:,.0f} | {failures:,.0f} | {reliable:,.0f}/2,688 |"
        )
    lines += [
        "",
        "| Key type | Insertions | Deletes | Lookup/update busy drops | Max ending/peak lines |",
        "|---|---:|---:|---:|---:|",
    ]
    for kind, lifecycle in filter_lifecycle.items():
        lines.append(
            f"| {kind.upper()} | {lifecycle['insertions']:,.0f} | "
            f"{lifecycle['deletes']:,.0f} | "
            f"{lifecycle['lookup_busy']:,.0f}/"
            f"{lifecycle['update_busy']:,.0f} | "
            f"{lifecycle['ending']:,.0f}/{lifecycle['peak']:,.0f} |"
        )
    lines += [
        "",
        "The occupancy columns are the largest one-workload sums across its "
        "192 physical slices. They are not sums across independent runs.",
        "",
        "RESIDENT and PENDING have zero insertion failures. PATTERN and SEEN "
        "are low-priority performance hints; their capacity failures suppress "
        "the corresponding optimization in an affected slice and cannot "
        "change demand correctness.",
        "",
        f"PENDING records {pending_positives:,.0f} positive hints. Its "
        f"{pending_insertions:,.0f} insertions and {pending_deletes:,.0f} "
        "deletions balance exactly, and exact RDMA line state confirms "
        f"{remote_duplicate_reads:,.0f} real-demand merges. These are kept "
        "separate: the approximate hint can suppress speculative work, but "
        "only exact state is credited with eliminating a demand request.",
        "",
        "## M1 non-positive workload attribution",
        "",
        "| Workload | Speedup | Physical reads delta | Physical writes delta | MSHR-full cycles delta | Demand latency delta | Issued/useful/unused/outstanding |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]
    for row in detail:
        if float(row["m1_speedup"]) > POSITIVE_THRESHOLD:
            continue
        benchmark = str(row["benchmark"])
        m1_row = m1[benchmark]

        def m1_change(field: str) -> float | None:
            before = number(m1_row, f"baseline_{field}")
            after = number(m1_row, f"m1_{field}")
            removed = reduction(before, after)
            return None if removed is None else -removed

        base_latency = ratio(
            number(m1_row, "baseline_l2_demand_read_latency_total_ns") or 0,
            number(m1_row, "baseline_l2_demand_read_latency_samples") or 0,
        )
        after_latency = ratio(
            number(m1_row, "m1_l2_demand_read_latency_total_ns") or 0,
            number(m1_row, "m1_l2_demand_read_latency_samples") or 0,
        )
        latency_delta = (
            100.0 * (after_latency - base_latency) / base_latency
            if base_latency > 0
            else None
        )

        lines.append(
            f"| {row['label']} | {float(row['m1_speedup']):.3f}x | "
            f"{fmt(m1_change('dram_physical_read_accesses'), '%', 2)} | "
            f"{fmt(m1_change('dram_physical_write_accesses'), '%', 2)} | "
            f"{fmt(m1_change('l2_mshr_full_stall_cycles'), '%', 2)} | "
            f"{fmt(latency_delta, '%', 2)} | "
            f"{number(m1_row, 'm1_filter_prefetch_issued') or 0:,.0f}/"
            f"{number(m1_row, 'm1_filter_prefetch_useful') or 0:,.0f}/"
            f"{number(m1_row, 'm1_filter_prefetch_unused') or 0:,.0f}/"
            f"{number(m1_row, 'm1_filter_prefetch_outstanding') or 0:,.0f} |"
        )
    lines += [
        "",
        "Positive deltas denote added work or pressure. These counters show "
        "why high prefetch accuracy alone does not guarantee speedup.",
    ]

    weak = [row for row in detail if row["classification"] != "positive"]
    lines += [
        "",
        "## Counter-supported weak-workload audit",
        "",
    ]
    if not weak:
        lines.append("All workloads are positive under the predeclared threshold.")
    else:
        for row in weak:
            lines.append(
                f"- **{row['label']} ({float(row['complete_speedup']):.3f}x):** "
                f"{row['counter_supported_diagnosis']}."
            )
    lines += [
        "",
        "The diagnoses above are deliberately limited to observed work counters; "
        "they do not infer compute boundedness without a separate utilization trace.",
    ]

    report_path = root / "CUPATH_FORMAL_ANALYSIS.md"
    report_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(detail_path)
    print(report_path)


if __name__ == "__main__":
    main()
