#!/usr/bin/env python3
"""Strictly audit a 14-workload Baseline+Complete observation campaign."""

from __future__ import annotations

import argparse
import csv
import gzip
import hashlib
import json
from pathlib import Path

from analyze_wg_mapping import audit as audit_wg_mapping
from analyze_wg_mapping import write_outputs as write_wg_mapping_outputs
from plot_cupath_typed_ablation import WORKLOADS


CONFIGS = ("baseline", "complete")
EXPECTED_CELLS = {
    (benchmark, config)
    for benchmark, _label, _group in WORKLOADS
    for config in CONFIGS
}

COMMON_FLAGS = {
    "-timing",
    "-num-memory-banks=4",
    "-l1v-mshr-entries=16",
    "-l1v-max-concurrent-trans=16",
    "-bandwidth=48",
    "-switch-latency=32",
    "-rdma-pipeline-width=8",
    "-rdma-pipeline-latency=10",
    "-rdma-max-outstanding=64",
    "-magic-memory-copy",
    "-report-all",
    "-disable-servers",
    "-mmutlb-lookup-latency=80",
    "-max-wg=78600",
    "-typed-filter-slots-per-bucket=4",
    "-typed-filter-fingerprint-bits=13",
    "-typed-filter-lookup-latency=1",
    "-typed-filter-lookup-width=16",
    "-typed-filter-update-latency=1",
    "-typed-filter-update-width=16",
    "-prefetch-predictor-entries=256",
    "-remote-data-path-batch-lines=8",
    "-remote-data-path-batches=64",
    "-typed-filter-mode=cuckoo",
}

MECHANISM_FLAGS = {
    "baseline": {
        "-l2-resident-filter-enable=false",
        "-l2-fill-forwarding-enable=false",
        "-dram-row-continuation-enable=false",
        "-l2-filter-prefetch-enable=false",
        "-l2-prefetch-predictor-only=false",
        "-l2-prefetch-ungated=false",
        "-l2-granularity-adaptation-enable=false",
        "-l2-adaptive-pair-enable=false",
        "-l2-granularity-without-filter=false",
        "-l2-granularity-always-expand=false",
        "-l2-granularity-predictor-only=false",
        "-remote-data-path-enable=false",
        "-remote-data-path-dedup-enable=false",
        "-remote-data-path-batching-enable=false",
        "-remote-data-path-l2-enable=false",
        "-remote-filter-prefetch-enable=false",
    },
    "complete": {
        "-l2-resident-filter-enable=false",
        "-l2-fill-forwarding-enable=false",
        "-dram-row-continuation-enable=false",
        "-l2-filter-prefetch-enable=false",
        "-l2-prefetch-predictor-only=false",
        "-l2-prefetch-ungated=false",
        "-l2-granularity-adaptation-enable=false",
        "-l2-adaptive-pair-enable=true",
        "-l2-granularity-without-filter=false",
        "-l2-granularity-always-expand=false",
        "-l2-granularity-predictor-only=false",
        "-remote-data-path-enable=true",
        "-remote-data-path-dedup-enable=true",
        "-remote-data-path-batching-enable=true",
        "-remote-data-path-l2-enable=true",
        "-remote-filter-prefetch-enable=true",
    },
}

TRACE_FIXED_FLAGS = {
    "-trace-observation",
    "-trace-observation-warmup-accesses=100000",
    "-trace-observation-max-records=100000",
    "-trace-observation-dram-warmup-accesses=100000",
    "-trace-observation-dram-max-records=100000",
    "-trace-observation-remote-warmup-requests=0",
    "-trace-observation-remote-max-records=100000",
    "-trace-observation-l2-sample-max=100000",
}

REQUIRED_COMPLETE_METRICS = {
    "adaptive_pair_predictions",
    "adaptive_pair_inflight_hits",
    "adaptive_pair_buffer_hits",
    "adaptive_pair_prefetch_unused",
    "remote_logical_reads",
    "remote_duplicate_reads",
    "remote_single_packets",
    "remote_bitmap_packets",
    "remote_bitmap_lines",
    "remote_requester_l2_hits",
    "remote_l2_two_touch_installed_fills",
    "typed_filter_lookup_port_stalls",
    "typed_filter_update_port_stalls",
    "typed_filter_granularity_pending_queries",
}

TRACE_SUFFIXES = (
    "_paths.csv.gz",
    "_validation.csv",
    "_dram_locality.csv",
    "_remote_requests.csv.gz",
    "_l2_utilization.csv.gz",
    "_remote_validation.csv",
)

ANALYSIS_ARTIFACTS = (
    "DYNAMIC_ADMISSION_AUDIT.csv",
    "cupath_wg_completion_audit.csv",
    "CUPATH_WG_COMPLETION_AUDIT.md",
    "cupath_workload_footprints.csv",
    "observation-analysis/emitter_instrumentation_validation.csv",
    "observation-analysis/o1_component_latency_breakdown.csv",
    "observation-analysis/o1_l2_demand_read_miss_rate.csv",
    "observation-analysis/o2_adjacent_line_window_heatmap.csv",
    "observation-analysis/o3_physical_locality_heatmap_long.csv",
    "observation-analysis/o4_remote_work_before_owner_mshr.csv",
    "observation-analysis/o5_exact_inflight_dedup.csv",
    "observation-analysis/o6_remote_reuse_summary.csv",
    "observation-analysis/o6_l2_headroom.csv",
    "cupath_speedup_table.csv",
    "cupath_baseline_complete_speedup.csv",
    "cupath_baseline_complete_speedup.png",
    "cupath_mechanism_effectiveness.csv",
    "cupath_final_summary.csv",
    "CUPATH_FINAL_RESULTS.md",
)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read {path}: {error}") from error


def audit_metadata(root: Path) -> tuple[Path, str]:
    metadata = load_json(root / "EXPERIMENT_METADATA.json")
    manifest = load_json(root / "EXPERIMENT_BINARIES.json")
    experiments = metadata.get("experiments", [])
    if metadata.get("experiment_count") != 28 or len(experiments) != 28:
        raise ValueError(f"metadata contains {len(experiments)}/28 commands")

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
    }
    for field, expected in expected_launcher.items():
        if launcher.get(field) != expected:
            raise ValueError(
                f"launcher {field}={launcher.get(field)!r}, expected {expected!r}"
            )
    if float(launcher.get("mem_available_gib_at_launch", 0)) <= 50.0:
        raise ValueError("campaign began without the 50 GiB safety reserve")

    observed_cells = set()
    binary_paths = set()
    for experiment in experiments:
        benchmark = experiment.get("benchmark")
        config = experiment.get("configuration")
        cell = (benchmark, config)
        if cell not in EXPECTED_CELLS or cell in observed_cells:
            raise ValueError(f"unexpected or duplicate metadata cell {cell}")
        observed_cells.add(cell)
        command = experiment.get("command", [])
        if not command:
            raise ValueError(f"empty command for {benchmark}/{config}")
        binary_paths.add(command[0])
        tokens = command[1:]
        if len(tokens) != len(set(tokens)):
            raise ValueError(f"duplicate flags in {benchmark}/{config}")
        token_set = set(tokens)
        required = COMMON_FLAGS | MECHANISM_FLAGS[config]
        missing = sorted(required - token_set)
        if missing:
            raise ValueError(
                f"{benchmark}/{config} missing flags: {', '.join(missing)}"
            )
        if f"-benchmark={benchmark}" not in token_set:
            raise ValueError(f"benchmark flag mismatch for {benchmark}/{config}")
        metric_prefix = (root / f"baseline_{benchmark}_{config}_metrics").resolve()
        if f"-metric-file-name={metric_prefix}" not in token_set:
            raise ValueError(f"metric path mismatch for {benchmark}/{config}")
        trace_tokens = {
            token for token in token_set if token.startswith("-trace-observation")
        }
        if config == "baseline":
            prefix = (
                root / f"baseline_{benchmark}_baseline_observation"
            ).resolve()
            expected_trace = TRACE_FIXED_FLAGS | {
                f"-trace-observation-file={prefix}"
            }
            if trace_tokens != expected_trace:
                raise ValueError(
                    f"baseline trace flags mismatch for {benchmark}: "
                    f"{sorted(trace_tokens ^ expected_trace)}"
                )
        elif trace_tokens:
            raise ValueError(f"Complete unexpectedly enables tracing: {benchmark}")
        if any(
            token == flag or token.startswith(flag + "=")
            for token in token_set
            for flag in (
                "-sampled", "-branch-sampled", "-kernel-sampled",
                "-loop-sampled", "-force-local-data-access",
            )
        ):
            raise ValueError(f"forbidden execution flag in {benchmark}/{config}")

    if observed_cells != EXPECTED_CELLS:
        raise ValueError("metadata does not cover the 14x2 grid")
    if len(binary_paths) != 1:
        raise ValueError(f"campaign mixes binaries: {sorted(binary_paths)}")
    binary = Path(next(iter(binary_paths)))
    expected_hash = manifest.get("sha256_by_target", {}).get("baseline")
    if not binary.is_file() or not expected_hash:
        raise ValueError("frozen binary or manifest hash is missing")
    actual_hash = sha256(binary)
    if actual_hash != expected_hash:
        raise ValueError(
            f"frozen binary hash mismatch: {actual_hash} != {expected_hash}"
        )
    return binary, actual_hash


def read_metrics(path: Path) -> tuple[dict[str, float], dict[str, set[float]]]:
    totals: dict[str, float] = {}
    values: dict[str, set[float]] = {}
    driver_time = None
    with path.open(newline="", encoding="utf-8") as stream:
        reader = csv.DictReader(stream, skipinitialspace=True)
        for row in reader:
            name = row["what"].strip()
            value = float(row["value"])
            totals[name] = totals.get(name, 0.0) + value
            values.setdefault(name, set()).add(value)
            if row["where"].strip() == "Driver" and name == "total_time":
                driver_time = value
    if driver_time is None or driver_time <= 0:
        raise ValueError(f"Driver total_time is missing from {path}")
    totals["__driver_total_time"] = driver_time
    return totals, values


def check_gzip_header(path: Path) -> None:
    try:
        with gzip.open(path, "rt", newline="") as stream:
            header = next(csv.reader(stream), [])
    except (OSError, EOFError) as error:
        raise ValueError(f"invalid gzip trace {path}: {error}") from error
    if not header:
        raise ValueError(f"trace has no CSV header: {path}")


def audit_wg_completion(
    root: Path,
    benchmark: str,
    config: str,
    metrics: dict[str, float],
    values: dict[str, set[float]],
) -> dict[str, object]:
    expected = {
        "max_wg_limit": {78600.0},
        "max_wg_launch_limited": {0.0},
        "max_wg_kernel_drained": {0.0},
    }
    for name, wanted in expected.items():
        if values.get(name) != wanted:
            raise ValueError(
                f"{benchmark}/{config} has invalid {name}: {values.get(name)}"
            )
    completed_values = values.get("max_wg_completed")
    reached_values = values.get("max_wg_reached")
    total_values = values.get("total_wg_count")
    if not completed_values or len(completed_values) != 1:
        raise ValueError(f"{benchmark}/{config} has ambiguous completed WG count")
    if not reached_values or len(reached_values) != 1:
        raise ValueError(f"{benchmark}/{config} has ambiguous max-wg state")
    completed = int(next(iter(completed_values)))
    reached = int(next(iter(reached_values)))
    if total_values != {float(completed)}:
        raise ValueError(
            f"{benchmark}/{config} total/completed WG mismatch: "
            f"{total_values} vs {completed}"
        )
    if completed <= 0 or completed > 78600:
        raise ValueError(
            f"{benchmark}/{config} completed invalid WG count {completed}"
        )
    stdout = root / f"baseline_{benchmark}_{config}_out.stdout"
    text = stdout.read_text(encoding="utf-8", errors="replace")
    stopper_line = f"[Runner] reached max-wg=78600 completed_wg=78600"
    if reached == 1:
        if completed != 78600 or stopper_line not in text:
            raise ValueError(
                f"{benchmark}/{config} lacks exact completion-stop evidence"
            )
        termination = "completed_wg_limit"
    elif reached == 0:
        if completed >= 78600 or stopper_line in text:
            raise ValueError(
                f"{benchmark}/{config} has inconsistent natural completion"
            )
        termination = "workload_completed_before_limit"
    else:
        raise ValueError(f"{benchmark}/{config} has invalid max_wg_reached={reached}")
    for forbidden in ("limited from", "drained max-wg", "admitted_wg="):
        if forbidden in text:
            raise ValueError(
                f"{benchmark}/{config} contains stale WG limiter text {forbidden!r}"
            )
    return {
        "benchmark": benchmark,
        "configuration": config,
        "max_wg_limit": 78600,
        "completed_wg_count": completed,
        "max_wg_reached": reached,
        "termination": termination,
        "launch_limited": False,
        "kernel_drained": False,
        "completion_message_verified": reached == 1,
    }


def audit_passive_wg_stopper_source(root: Path) -> None:
    repo = root.parents[2]
    tracer = (repo / "akkalat/baseline/runner/wgtracer.go").read_text(
        encoding="utf-8"
    )
    hooks = (repo / "akkalat/baseline/runner/tracers.go").read_text(
        encoding="utf-8"
    )
    required = (
        'task.What != "*protocol.WGCompletionMsg"',
        "t.count == t.maxCount",
        "completed_wg=%d",
    )
    if any(fragment not in tracer for fragment in required):
        raise ValueError("WG stopper is not the audited completion-only implementation")
    if "MapWGReq" in tracer or "WGFilter" in tracer:
        raise ValueError("WG stopper modifies or filters dispatch-time WG mapping")
    if "r.maxWGStopper = newWGStopper(*maxWGCount)" not in hooks or \
            "r.maxWGStopper)" not in hooks:
        raise ValueError("one shared completion stopper is not attached across CUs")


def write_wg_completion_outputs(root: Path, rows: list[dict[str, object]]) -> None:
    path = root / "cupath_wg_completion_audit.csv"
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    limited = sum(row["termination"] == "completed_wg_limit" for row in rows)
    natural = len(rows) - limited
    lines = [
        "# CuPath WG completion audit",
        "",
        f"- Audited cells: {len(rows)}/{len(EXPECTED_CELLS)}",
        f"- Exact 78,600-completion stops: {limited}",
        f"- Workloads naturally completed below the limit: {natural}",
        "- Launch-time WG limiting: absent in every cell",
        "- Post-limit kernel draining: absent in every cell",
        "- Stopper implementation: one shared passive WGCompletionMsg observer",
        "- WG generation, IDs, and original GPU assignment are not modified by the stopper",
    ]
    (root / "CUPATH_WG_COMPLETION_AUDIT.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )


def audit_results(root: Path) -> list[dict[str, object]]:
    expected_paths = {
        root / f"baseline_{benchmark}_{config}_result.json"
        for benchmark, config in EXPECTED_CELLS
    }
    observed_paths = set(root.glob("*_result.json"))
    if observed_paths != expected_paths:
        missing = sorted(path.name for path in expected_paths - observed_paths)
        extra = sorted(path.name for path in observed_paths - expected_paths)
        raise ValueError(
            f"result grid is not 28/28; missing={missing}; unexpected={extra}"
        )

    rows = []
    wg_rows = []
    for benchmark, config in sorted(EXPECTED_CELLS):
        result_path = root / f"baseline_{benchmark}_{config}_result.json"
        result = load_json(result_path)
        if (
            result.get("success") is not True
            or result.get("returncode") != 0
            or result.get("simulator_returncode") != 0
            or result.get("benchmark") != benchmark
            or result.get("configuration") != config
            or result.get("target") != "baseline"
        ):
            raise ValueError(f"failed or mismatched result: {result_path}")
        metrics_path = root / f"baseline_{benchmark}_{config}_metrics.csv"
        recorded_metrics = result.get("metrics")
        if (
            not isinstance(recorded_metrics, str)
            or Path(recorded_metrics).resolve() != metrics_path.resolve()
            or not metrics_path.is_file()
        ):
            raise ValueError(f"metric artifact mismatch: {result_path}")
        metrics, values = read_metrics(metrics_path)
        if values.get("config_l1v_mshr_entries") != {16.0}:
            raise ValueError(f"L1 MSHR mismatch: {metrics_path}")
        if values.get("config_l1v_max_concurrent_transactions") != {16.0}:
            raise ValueError(f"L1 concurrency mismatch: {metrics_path}")
        if config == "complete":
            missing_metrics = sorted(REQUIRED_COMPLETE_METRICS - metrics.keys())
            if missing_metrics:
                raise ValueError(
                    f"{benchmark}/complete missing mechanism metrics: "
                    + ", ".join(missing_metrics)
                )
        wg_rows.append(audit_wg_completion(
            root, benchmark, config, metrics, values
        ))
        rows.append({
            "benchmark": benchmark,
            "configuration": config,
            "success": True,
            "host_elapsed_seconds": result.get("host_elapsed_seconds"),
            "driver_time_seconds": metrics["__driver_total_time"],
        })

    for benchmark, _label, _group in WORKLOADS:
        prefix = root / f"baseline_{benchmark}_baseline_observation"
        for suffix in TRACE_SUFFIXES:
            path = Path(str(prefix) + suffix)
            if not path.is_file() or path.stat().st_size == 0:
                raise ValueError(f"missing or empty baseline trace: {path}")
            if path.name.endswith(".gz"):
                check_gzip_header(path)

    audit_passive_wg_stopper_source(root)
    write_wg_completion_outputs(root, wg_rows)

    mapping_paths = [
        root / f"baseline_{benchmark}_{config}_metrics_wg_mapping.json"
        for benchmark, config in EXPECTED_CELLS
    ]
    if any(path.is_file() for path in mapping_paths):
        if not all(path.is_file() for path in mapping_paths):
            raise ValueError("WG mapping sidecars are only partially present")
        mapping_rows = audit_wg_mapping(
            root,
            [benchmark for benchmark, _label, _group in WORKLOADS],
            configs=CONFIGS,
            require_baseline_match=False,
        )
        write_wg_mapping_outputs(root, mapping_rows)
    return rows


def audit_analysis_artifacts(root: Path) -> None:
    for relative in ANALYSIS_ARTIFACTS:
        path = root / relative
        if not path.is_file() or path.stat().st_size == 0:
            raise ValueError(f"missing analysis artifact: {path}")
    validation = root / (
        "observation-analysis/emitter_instrumentation_validation.csv"
    )
    with validation.open(newline="", encoding="utf-8") as stream:
        rows = list(csv.DictReader(stream))
    if not rows or any(row.get("strict_pass") != "true" for row in rows):
        raise ValueError("observation emitter validation did not strictly pass")

    with (root / "cupath_wg_completion_audit.csv").open(
        newline="", encoding="utf-8"
    ) as stream:
        wg_rows = list(csv.DictReader(stream))
    baseline_wg = {
        row["benchmark"]: int(row["completed_wg_count"])
        for row in wg_rows
        if row.get("configuration") == "baseline"
    }
    expected_benchmarks = {
        benchmark for benchmark, _label, _group in WORKLOADS
    }
    if set(baseline_wg) != expected_benchmarks:
        raise ValueError(
            f"WG audit covers {len(baseline_wg)}/14 baseline workloads"
        )

    with (root / "cupath_workload_footprints.csv").open(
        newline="", encoding="utf-8"
    ) as stream:
        footprint_rows = {
            row["benchmark"]: row for row in csv.DictReader(stream)
        }
    if set(footprint_rows) != expected_benchmarks:
        raise ValueError(
            f"workload footprint table covers {len(footprint_rows)}/14 workloads"
        )
    for benchmark, row in footprint_rows.items():
        observed_wg = int(float(row["observed_workgroups"]))
        if observed_wg != baseline_wg[benchmark]:
            raise ValueError(
                f"{benchmark} footprint WG count {observed_wg} != "
                f"completion audit {baseline_wg[benchmark]}"
            )
        page_size = float(row["page_size_bytes"])
        workload_pages = float(row["workload_allocated_pages"])
        footprint_mib = float(row["workload_footprint_mib"])
        overall_pages = float(row["overall_allocated_pages"])
        expected_mib = workload_pages * page_size / (1024 * 1024)
        if page_size <= 0 or workload_pages <= 0 or overall_pages < workload_pages:
            raise ValueError(f"{benchmark} has invalid allocation accounting")
        if abs(footprint_mib - expected_mib) > 1e-9:
            raise ValueError(f"{benchmark} has inconsistent footprint size")


def audit_dynamic_admission(root: Path) -> int:
    path = root / "DYNAMIC_ADMISSION_AUDIT.csv"
    if not path.is_file():
        raise ValueError(f"missing dynamic admission audit: {path}")
    with path.open(newline="", encoding="utf-8") as stream:
        rows = list(csv.DictReader(stream))
    if not rows:
        raise ValueError("dynamic admission audit contains no scan events")
    for index, row in enumerate(rows, 1):
        available = float(row["mem_available_gib"])
        reserve = float(row["reserve_gib"])
        previous = int(row["previous_concurrency"])
        current = int(row["new_concurrency"])
        if reserve != 50.0 or available <= reserve:
            raise ValueError(
                f"admission row {index} violates reserve: "
                f"MemAvailable={available}, reserve={reserve}"
            )
        if current != previous + 1 or current > 14:
            raise ValueError(
                f"admission row {index} has invalid concurrency transition "
                f"{previous}->{current}"
            )
        if row.get("decision") != "admitted":
            raise ValueError(f"admission row {index} is not an admitted event")
    return len(rows)


def write_report(
    root: Path,
    binary: Path,
    binary_hash: str,
    rows: list[dict[str, object]] | None,
    analysis_checked: bool,
) -> None:
    lines = [
        "# Baseline+Complete Campaign Audit",
        "",
        "- Metadata grid: 28/28 Baseline+Complete commands",
        "- Baseline observation commands: 14/14",
        "- Complete observation commands: 0/14",
        "- Max WG: 78,600",
        "- L1V MSHRs and concurrent transactions: 16 and 16",
        "- Memory admission: initial 1, maximum 14, reserve 50 GiB, scan 30 min",
        f"- Frozen binary: `{binary}`",
        f"- Frozen binary SHA-256: `{binary_hash}`",
    ]
    if rows is not None:
        lines.extend([
            "- Successful result cells: 28/28",
            "- WG completion accounting: 28/28 strictly validated",
            "- WG stopper source invariant: completion-only and mapping-preserving",
            "- Baseline trace families: 14/14 complete",
        ])
    else:
        lines.append("- Result audit: pending campaign completion")
    lines.append(
        "- Analysis artifacts: "
        + ("strictly validated" if analysis_checked else "not requested")
    )
    if analysis_checked:
        lines.append(
            "- Workload completed-WG and footprint accounting: 14/14 cross-validated"
        )
    (root / "BASELINE_COMPLETE_CAMPAIGN_AUDIT.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )
    if rows is not None:
        with (root / "baseline_complete_cell_audit.csv").open(
            "w", newline="", encoding="utf-8"
        ) as stream:
            writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
            writer.writeheader()
            writer.writerows(rows)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--metadata-only", action="store_true")
    parser.add_argument("--require-analysis", action="store_true")
    args = parser.parse_args()
    root = args.results.resolve()
    binary, binary_hash = audit_metadata(root)
    admission_events = audit_dynamic_admission(root)
    rows = None if args.metadata_only else audit_results(root)
    if args.require_analysis:
        if args.metadata_only:
            raise ValueError("--require-analysis cannot use --metadata-only")
        audit_analysis_artifacts(root)
    write_report(root, binary, binary_hash, rows, args.require_analysis)
    print(
        f"Baseline+Complete campaign audit PASS: metadata=28/28, "
        f"admission_events={admission_events}, "
        f"results={'pending' if rows is None else '28/28'}, "
        f"analysis={'checked' if args.require_analysis else 'not requested'}"
    )


if __name__ == "__main__":
    main()
