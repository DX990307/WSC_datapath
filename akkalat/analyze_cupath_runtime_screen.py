#!/usr/bin/env python3
"""Audit the seven-workload CuPath representative screen.

The same checker accepts either a parallel functional screen or an explicitly
controlled single-worker runtime screen.  It never presents co-scheduled wall
time as isolated simulator overhead.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
from pathlib import Path

from plot_cupath_typed_ablation import (
    CONFIGS,
    REQUIRED_BASELINE_METRICS,
    REQUIRED_COMPLETE_METRICS,
    REQUIRED_EXECUTION_METRICS,
    REQUIRED_M1_METRICS,
    REQUIRED_M2_METRICS,
    REQUIRED_M3_METRICS,
    geomean,
    load_campaign,
    read_elapsed_seconds,
    read_return_code,
)
from analyze_wg_mapping import audit as audit_wg_mapping


BENCHMARKS = (
    "aes",
    "fastwalshtransform",
    "fft",
    "kmeans",
    "pagerank",
    "matrixtranspose",
    "spmv",
)
REQUIRED_FLAGS = {
    "-timing",
    "-max-wg=76800",
    "-sampled",
    "-branch-sampled",
    "-kernel-sampled",
    "-l1v-mshr-entries=16",
    "-num-memory-banks=4",
    "-bandwidth=48",
    "-switch-latency=32",
    "-rdma-pipeline-width=8",
    "-rdma-pipeline-latency=10",
    "-rdma-max-outstanding=64",
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
MECHANISM_FIELDS = (
    "l2-resident-filter-enable",
    "l2-filter-prefetch-enable",
    "remote-data-path-enable",
    "remote-data-path-dedup-enable",
    "remote-data-path-batching-enable",
    "remote-data-path-l2-enable",
    "remote-filter-prefetch-enable",
)
MECHANISMS = {
    "baseline": (False, False, False, False, False, False, False),
    "m1": (False, True, False, False, False, False, False),
    "m2": (False, False, True, True, True, False, True),
    "m3": (False, False, True, False, False, True, False),
    "complete": (False, True, True, True, True, True, True),
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def audit_protocol(
    root: Path,
    expected_workers: int = 1,
    expected_sha256: str | None = None,
) -> tuple[str, str]:
    metadata = json.loads(
        (root / "EXPERIMENT_METADATA.json").read_text(encoding="utf-8")
    )
    manifest = json.loads(
        (root / "EXPERIMENT_BINARIES.json").read_text(encoding="utf-8")
    )
    launcher = metadata.get("launcher", {})
    if launcher.get("max_workers") != expected_workers:
        raise SystemExit(
            "representative-screen worker mismatch: "
            f"expected={expected_workers}, launcher={launcher!r}"
        )
    experiments = metadata.get("experiments", [])
    expected = {
        (benchmark, config)
        for benchmark in BENCHMARKS
        for config, _, _ in CONFIGS
    }
    observed = {
        (exp.get("benchmark"), exp.get("configuration"))
        for exp in experiments
    }
    if (
        metadata.get("experiment_count") != 35
        or len(experiments) != 35
        or observed != expected
    ):
        raise SystemExit(
            "runtime screen grid mismatch: "
            f"{len(experiments)} commands, {len(observed)}/35 unique cells"
        )
    for exp in experiments:
        if exp.get("target") != "baseline":
            raise SystemExit(
                f"runtime screen target mismatch: {exp.get('benchmark')}/"
                f"{exp.get('configuration')}"
            )
    binaries = {str(exp["command"][0]) for exp in experiments}
    if len(binaries) != 1:
        raise SystemExit(f"runtime screen mixes binaries: {sorted(binaries)}")
    binary = Path(next(iter(binaries)))
    recorded = manifest.get("sha256_by_target", {}).get("baseline")
    actual = sha256(binary)
    if recorded != actual:
        raise SystemExit(
            f"runtime binary hash mismatch: manifest={recorded}, actual={actual}"
        )
    if expected_sha256 is not None and actual != expected_sha256:
        raise SystemExit(
            f"unexpected frozen binary hash: expected={expected_sha256}, "
            f"actual={actual}"
        )
    for exp in experiments:
        command_flags = exp["command"][1:]
        flags = set(command_flags)
        if len(flags) != len(command_flags):
            raise SystemExit(
                f"{exp['benchmark']}/{exp['configuration']} contains "
                "duplicate flags"
            )
        missing = sorted(REQUIRED_FLAGS - flags)
        if missing:
            raise SystemExit(
                f"{exp['benchmark']}/{exp['configuration']} missing flags: "
                + ", ".join(missing)
            )
        config = exp["configuration"]
        expected_mechanisms = {
            f"-{field}={str(value).lower()}"
            for field, value in zip(MECHANISM_FIELDS, MECHANISMS[config])
        }
        if not expected_mechanisms <= flags:
            raise SystemExit(
                f"{exp['benchmark']}/{config} mechanism flags do not match "
                "the controlled ablation"
            )
        forbidden = (
            "128b", "128-b", "128-byte", "hlq", "prefetch-wait",
            "batching-timeout", "global-scheduler", "force-local-data-access",
        )
        if any(any(name in token.lower() for name in forbidden) for token in flags):
            raise SystemExit(
                f"{exp['benchmark']}/{config} contains a forbidden flag"
            )
        expected_dynamic = {
            f"-benchmark={exp['benchmark']}",
            (
                "-metric-file-name="
                + str(
                    (
                        root
                        / (
                            f"baseline_{exp['benchmark']}_{config}_metrics"
                        )
                    ).resolve()
                )
            ),
        }
        expected_flags = REQUIRED_FLAGS | expected_mechanisms | expected_dynamic
        unexpected = sorted(flags - expected_flags)
        if unexpected:
            raise SystemExit(
                f"{exp['benchmark']}/{config} contains unexpected flags: "
                + ", ".join(unexpected)
            )
    return str(binary), actual


def analyze(root: Path) -> tuple[list[dict[str, object]], dict[str, float]]:
    campaign, paths = load_campaign(root)
    expected = {
        (benchmark, config)
        for benchmark in BENCHMARKS
        for config, _, _ in CONFIGS
    }
    if set(campaign) != expected:
        raise SystemExit(
            f"runtime metrics grid mismatch: {len(campaign)}/35 cells"
        )
    rows: list[dict[str, object]] = []
    ratios: dict[str, list[float]] = {
        config: [] for config, _, _ in CONFIGS
    }
    simulated_speedups: dict[str, list[float]] = {
        config: [] for config, _, _ in CONFIGS
    }
    required_by_config = {
        "baseline": REQUIRED_BASELINE_METRICS,
        "m1": REQUIRED_M1_METRICS,
        "m2": REQUIRED_M2_METRICS,
        "m3": REQUIRED_M3_METRICS,
        "complete": REQUIRED_COMPLETE_METRICS,
    }
    for benchmark in BENCHMARKS:
        base_metrics = campaign[(benchmark, "baseline")]
        base_sim = base_metrics["__driver_total_time"]
        base_observed = base_metrics.get("max_wg_observed")
        base_requested = base_metrics.get("wg_requested_total")
        base_wall = read_elapsed_seconds(paths[(benchmark, "baseline")])
        if base_wall is None or base_wall <= 0 or base_sim <= 0:
            raise SystemExit(f"invalid Baseline runtime for {benchmark}")
        base_host_cost = base_wall / base_sim
        requested_values: list[float] = []
        workload_pages: list[float] = []
        overall_pages: list[float] = []
        for config, _, _ in CONFIGS:
            metrics = campaign[(benchmark, config)]
            metrics_path = paths[(benchmark, config)]
            missing = sorted(
                (REQUIRED_EXECUTION_METRICS | required_by_config[config])
                - metrics.keys()
            )
            if missing:
                raise SystemExit(
                    f"missing required metrics for {benchmark}/{config}: "
                    + ", ".join(missing)
                )
            if read_return_code(metrics_path) != 0:
                raise SystemExit(f"non-zero return for {benchmark}/{config}")
            result_path = metrics_path.with_name(
                metrics_path.name.removesuffix("_metrics.csv")
                + "_result.json"
            )
            if not result_path.is_file():
                raise SystemExit(f"missing result JSON for {benchmark}/{config}")
            result = json.loads(result_path.read_text(encoding="utf-8"))
            if (
                result.get("success") is not True
                or result.get("returncode") != 0
                or result.get("simulator_returncode") != 0
            ):
                raise SystemExit(
                    f"failed result JSON for {benchmark}/{config}: {result}"
                )
            if (
                result.get("target") != "baseline"
                or result.get("benchmark") != benchmark
                or result.get("configuration") != config
            ):
                raise SystemExit(
                    f"result identity mismatch for {benchmark}/{config}"
                )
            if (
                not isinstance(result.get("metrics"), str)
                or Path(result["metrics"]).resolve() != metrics_path.resolve()
            ):
                raise SystemExit(
                    f"result metric artifact mismatch for {benchmark}/{config}"
                )
            mapping = result.get("wg_mapping")
            expected_mapping = metrics_path.with_name(
                metrics_path.name.removesuffix(".csv") + "_wg_mapping.json"
            ).resolve()
            recorded_mapping = (
                mapping.get("path") if isinstance(mapping, dict) else None
            )
            if (
                not isinstance(recorded_mapping, str)
                or Path(recorded_mapping).resolve() != expected_mapping
                or not expected_mapping.is_file()
            ):
                raise SystemExit(
                    f"result WG-mapping artifact mismatch for "
                    f"{benchmark}/{config}"
                )
            observed = metrics.get("max_wg_observed")
            counted = metrics.get("total_wg_count")
            requested = metrics.get("wg_requested_total")
            limit = metrics.get("max_wg_limit")
            expected_observed = min(limit, requested)
            expected_reached = 1 if requested >= limit else 0
            if (
                observed != base_observed
                or counted != observed
                or requested != base_requested
                or observed != expected_observed
                or metrics.get("max_wg_runtime_stopper") != 1
                or metrics.get("max_wg_reached") != expected_reached
                or metrics.get("wg_max_wg_specific_filter") != 0
            ):
                raise SystemExit(
                    f"work mismatch for {benchmark}/{config}: "
                    f"observed={observed}, counted={counted}, "
                    f"requested={requested}, limit={limit}, "
                    f"expected={expected_observed}, "
                    f"baseline observed={base_observed}, "
                    f"baseline requested={base_requested}"
                )
            if metrics.get("allocation_page_size") != 4096:
                raise SystemExit(
                    f"unexpected allocation page size for {benchmark}/{config}"
                )
            requested_values.append(requested)
            workload_pages.append(metrics["allocation_workload_allocated_pages"])
            overall_pages.append(metrics["allocation_overall_allocated_pages"])
            wall = read_elapsed_seconds(metrics_path)
            if wall is None or wall <= 0:
                raise SystemExit(f"missing wall time for {benchmark}/{config}")
            simulated = metrics["__driver_total_time"]
            normalized = (wall / simulated) / base_host_cost
            ratios[config].append(normalized)
            simulated_speedups[config].append(base_sim / simulated)
            rows.append({
                "benchmark": benchmark,
                "config": config,
                "simulated_time_s": simulated,
                "wall_time_s": wall,
                "simulated_speedup": base_sim / simulated,
                "wall_time_over_baseline": wall / base_wall,
                "normalized_host_cost_over_baseline": normalized,
                "requested_total_wg": requested,
                "observed_wg": observed,
                "workload_allocated_pages": metrics[
                    "allocation_workload_allocated_pages"
                ],
                "overall_allocated_pages": metrics[
                    "allocation_overall_allocated_pages"
                ],
                "dram_physical_reads": metrics[
                    "dram_physical_read_accesses"
                ],
                "dram_physical_writes": metrics[
                    "dram_physical_write_accesses"
                ],
                "l2_tag_lookups_skipped": metrics.get(
                    "l2_resident_filter_read_bypasses", 0.0
                ),
                "l2_mshr_full_stall_cycles": metrics.get(
                    "l2_mshr_full_stall_cycles", 0.0
                ),
                "m1_issued": metrics.get("filter_prefetch_issued", 0.0),
                "m1_useful": metrics.get("filter_prefetch_useful", 0.0),
                "m1_late": metrics.get("filter_prefetch_late", 0.0),
                "m1_unused": metrics.get("filter_prefetch_unused", 0.0),
                "m2_logical_remote_reads": metrics.get(
                    "remote_logical_reads", 0.0
                ),
                "m2_exact_duplicate_reads": metrics.get(
                    "remote_duplicate_reads", 0.0
                ),
                "m2_wire_lines": metrics.get("remote_wire_lines", 0.0),
                "m2_single_packets": metrics.get(
                    "remote_single_packets", 0.0
                ),
                "m2_bitmap_packets": metrics.get(
                    "remote_bitmap_packets", 0.0
                ),
                "m2_width_stalls": sum(
                    metrics.get(field, 0.0)
                    for field in (
                        "remote_requester_issue_width_stalls",
                        "remote_response_fanout_width_stalls",
                        "remote_owner_issue_width_stalls",
                        "remote_owner_response_width_stalls",
                    )
                ),
                "m3_requester_l2_hits": metrics.get(
                    "remote_requester_l2_hits", 0.0
                ),
                "m3_installed_fills": metrics.get(
                    "remote_installed_fills", 0.0
                ),
                "m3_unused_fills": metrics.get(
                    "remote_requester_l2_unused_fills", 0.0
                ),
                "m3_local_clean_displacements": metrics.get(
                    "remote_fill_displaced_local_clean", 0.0
                ),
            })
        if len(set(requested_values)) != 1:
            raise SystemExit(
                f"configuration-dependent full grid for {benchmark}: "
                f"{requested_values}"
            )
        if len(set(workload_pages)) != 1 or len(set(overall_pages)) != 1:
            raise SystemExit(
                f"configuration-dependent allocation footprint for {benchmark}"
            )
    aggregate: dict[str, float] = {}
    for config, values in ratios.items():
        aggregate[f"{config}_host_cost"] = geomean(values)
        aggregate[f"{config}_simulated_speedup"] = geomean(
            simulated_speedups[config]
        )
    return rows, aggregate


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--expected-workers", type=int, default=1)
    parser.add_argument("--expected-sha256")
    args = parser.parse_args()
    root = args.results.resolve()
    binary, digest = audit_protocol(
        root,
        expected_workers=args.expected_workers,
        expected_sha256=args.expected_sha256,
    )
    mapping_rows = audit_wg_mapping(root, list(BENCHMARKS))
    rows, aggregate = analyze(root)
    mapping = {
        (row["benchmark"], row["configuration"]): row
        for row in mapping_rows
    }
    for row in rows:
        evidence = mapping[(row["benchmark"], row["config"])]
        row["partition_sha256"] = evidence["partition_sha256"]
        row["global_wg_set_sha256"] = evidence["global_wg_set_sha256"]
        row["per_gpu_sha256"] = evidence["per_gpu_sha256"]

    csv_path = root / "cupath_controlled_runtime.csv"
    with csv_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)

    lines = [
        "# CuPath representative screen audit",
        "",
        "This diagnostic uses seven representative paper workloads, five "
        "configurations, and a runner-observed 76,800-WG runtime window over "
        "the unchanged full workload grid. It is a pre-formal functional and "
        "mechanism-evidence gate, not a formal hardware-performance result.",
        "",
        f"Frozen binary: `{binary}` (`{digest}`).",
        f"Launcher workers: {args.expected_workers}.",
        "",
        "| Configuration | Simulated speedup | Normalized host cost / Baseline |",
        "|---|---:|---:|",
    ]
    for config, label, _ in CONFIGS:
        lines.append(
            f"| {label} | "
            f"{aggregate[f'{config}_simulated_speedup']:.4f}x | "
            f"{aggregate[f'{config}_host_cost']:.4f}x |"
        )
    lines += [
        "",
        "Normalized host cost is `(wall time / simulated GPU time)` divided "
        "by the same quantity for Baseline. Raw simulated and wall times "
        "remain in `cupath_controlled_runtime.csv`.",
    ]
    if args.expected_workers != 1:
        lines += [
            "",
            "Because this screen is co-scheduled, wall-time ratios are only "
            "resource-safety diagnostics. They are not credited as isolated "
            "simulator-overhead measurements.",
        ]
    report = root / "CUPATH_CONTROLLED_RUNTIME.md"
    report.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(csv_path)
    print(report)


if __name__ == "__main__":
    main()
