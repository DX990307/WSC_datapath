#!/usr/bin/env python3
"""Run one globally interleaved CuPath sensitivity campaign.

Every sensitivity point contains a matched Baseline and Complete cell.  The
cells share one worker pool and one memory-admission gate, so a slow point
cannot block all experiments belonging to the other points.
"""

from __future__ import annotations

import argparse
import atexit
import json
import os
import subprocess
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace

from runall2 import run_all_experiments
from runall2_config import (
    build_remote_data_path_ablation_configs,
    expand_benchmark_selection,
    filter_missing_metric_exps,
    parse_csv,
)
from runall2_constants import (
    ALL_BENCHMARKS,
    BASE_COMMON_FLAGS,
    DEFAULT_BENCHMARK_FLAGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
)
from runall2_process import (
    dry_run_commands,
    freeze_experiment_binaries,
    install_signal_handlers,
    load_experiments_from_metadata,
    set_output_dir,
    terminate_all_processes,
    verify_or_write_binary_manifest,
    write_experiment_metadata,
)


ROOT = Path(__file__).resolve().parents[1]
SLOTS_PER_BUCKET = 4
FILTERS_PER_GPM = 4
REFERENCE_FILTER_BUCKETS = 16 * 1024
REFERENCE_L2_MB = 4
REFERENCE_ROW_LINES = 2
SAMPLED_FLAGS = ("-sampled", "-branch-sampled", "-kernel-sampled")
MECHANISMS = ("baseline", "complete")


@dataclass(frozen=True)
class SensitivityPoint:
    dimension: str
    name: str
    filter_buckets_per_gpm: int = REFERENCE_FILTER_BUCKETS
    l2_size_mb: int = REFERENCE_L2_MB
    row_region_lines: int = REFERENCE_ROW_LINES

    @property
    def filter_buckets_per_partition(self) -> int:
        if self.filter_buckets_per_gpm % FILTERS_PER_GPM:
            raise ValueError(
                "Filter bucket count per GPM must be divisible by four"
            )
        return self.filter_buckets_per_gpm // FILTERS_PER_GPM

    @property
    def filter_slots_per_partition(self) -> int:
        return self.filter_buckets_per_partition * SLOTS_PER_BUCKET

    def benchmark_flags(self) -> tuple[str, ...]:
        return (
            f"-typed-filter-capacity={self.filter_slots_per_partition}",
            f"-l2-cache-size-mb={self.l2_size_mb}",
            f"-l2-adaptive-pair-region-lines={self.row_region_lines}",
        )


POINTS = (
    SensitivityPoint("filter-size", "filter-4k-buckets", 4 * 1024),
    SensitivityPoint("filter-size", "filter-8k-buckets", 8 * 1024),
    SensitivityPoint("filter-size", "filter-32k-buckets", 32 * 1024),
    SensitivityPoint("l2-size", "l2-1mb", l2_size_mb=1),
    SensitivityPoint("l2-size", "l2-2mb", l2_size_mb=2),
    SensitivityPoint("row-locality", "row-4-lines", row_region_lines=4),
    SensitivityPoint("row-locality", "row-8-lines", row_region_lines=8),
    SensitivityPoint("row-locality", "row-16-lines", row_region_lines=16),
)

REFERENCE_POINT = SensitivityPoint("reference", "main-16k-4mb-2-lines")


def parse_dimensions(value: str) -> set[str]:
    aliases = {
        "filter": "filter-size",
        "l2": "l2-size",
        "row": "row-locality",
    }
    selected = set()
    for raw in value.split(","):
        name = aliases.get(raw.strip().lower(), raw.strip().lower())
        if not name:
            continue
        if name not in {"filter-size", "l2-size", "row-locality"}:
            raise ValueError(f"unknown sensitivity dimension: {raw}")
        selected.add(name)
    if not selected:
        raise ValueError("at least one sensitivity dimension is required")
    return selected


def round_robin_points(points: list[SensitivityPoint]) -> list[SensitivityPoint]:
    """Alternate dimensions instead of grouping a complete sweep together."""
    dimensions = []
    grouped = {}
    for point in points:
        if point.dimension not in grouped:
            grouped[point.dimension] = []
            dimensions.append(point.dimension)
        grouped[point.dimension].append(point)

    ordered = []
    while any(grouped.values()):
        for dimension in dimensions:
            if grouped[dimension]:
                ordered.append(grouped[dimension].pop(0))
    return ordered


def selected_points(args) -> list[SensitivityPoint]:
    dimensions = parse_dimensions(args.dimensions)
    points = [point for point in POINTS if point.dimension in dimensions]
    points = round_robin_points(points)
    if args.include_reference:
        points.insert(0, REFERENCE_POINT)
    return points


def selected_benchmarks(value: str) -> list[str]:
    benchmarks = expand_benchmark_selection(parse_csv(value))
    unknown = sorted(set(benchmarks) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks: {unknown}")
    if not benchmarks:
        raise ValueError("at least one benchmark is required")
    return benchmarks


def mechanism_flags() -> dict[str, list[str]]:
    args = SimpleNamespace(
        sampled_sweep=False,
        balanced_sweep=False,
        sampled_warmups="",
        sampled_granularities="",
        remote_data_path_batch_lines=8,
        remote_data_path_batches=64,
        configs="baseline,complete",
    )
    return dict(build_remote_data_path_ablation_configs(args))


def common_flags(args) -> list[str]:
    flags = BASE_COMMON_FLAGS[:]
    flags.extend((
        "-disable-servers",
        f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}",
    ))
    if args.max_wg > 0:
        flags.append(f"-max-wg={args.max_wg}")
    return flags


def config_name(point: SensitivityPoint, mechanism: str) -> str:
    return f"{point.name}_{mechanism}"


def experiment_variants(points: list[SensitivityPoint]):
    return [
        (point, mechanism)
        for point in points
        for mechanism in MECHANISMS
    ]


def interleaved_cells(benchmarks, variants):
    """Diagonalize the benchmark-by-variant matrix.

    Each scheduling wave contains every benchmark once, while the selected
    sensitivity point and Baseline/Complete mode rotate across benchmarks.
    This avoids point-major, mechanism-major, and benchmark-major blocking.
    """
    cells = []
    for wave in range(len(variants)):
        for benchmark_index, benchmark in enumerate(benchmarks):
            variant = variants[(benchmark_index + wave) % len(variants)]
            cells.append((benchmark, *variant))
    return cells


def build_experiments(args, points, binary: Path) -> list[dict]:
    benchmarks = selected_benchmarks(args.benchmarks)
    variants = experiment_variants(points)
    configs = mechanism_flags()
    shared_flags = common_flags(args)
    experiments = []
    for benchmark, point, mechanism in interleaved_cells(
        benchmarks, variants
    ):
        experiments.append({
            "target": "baseline",
            "benchmark": benchmark,
            "config_name": config_name(point, mechanism),
            "binary_path": str(binary),
            "common_flags": shared_flags[:],
            "flags": (
                DEFAULT_BENCHMARK_FLAGS
                + list(SAMPLED_FLAGS)
                + configs[mechanism]
                + list(point.benchmark_flags())
            ),
            "timeout_seconds": int(args.timeout_minutes * 60),
        })
    return experiments


def default_output_dir() -> Path:
    stamp = datetime.now(timezone.utc).strftime("%Y-%m-%d-%H-%M-%S")
    return ROOT / "akkalat" / "results" / f"{stamp}-cupath-sensitivity"


def build_binary(output_dir: Path) -> Path:
    binary = output_dir / "campaign-binary" / "cupath-sensitivity"
    binary.parent.mkdir(parents=True, exist_ok=True)
    if binary.is_file():
        print("Reusing sensitivity binary:", binary, flush=True)
        return binary.resolve()
    command = ["go", "build", "-buildvcs=false", "-o", str(binary), "."]
    environment = os.environ.copy()
    environment.setdefault("GOCACHE", "/tmp/gocache")
    print("Building sensitivity binary:", " ".join(command), flush=True)
    subprocess.run(
        command,
        cwd=ROOT / "akkalat" / "baseline",
        env=environment,
        check=True,
    )
    return binary.resolve()


def campaign_manifest(args, points) -> dict:
    return {
        "kind": "cupath_sensitivity_campaign",
        "schedule": "globally_interleaved_diagonal",
        "mechanisms_per_point": list(MECHANISMS),
        "filter_bucket_scope": "total_per_gpm",
        "filters_per_gpm": FILTERS_PER_GPM,
        "slots_per_bucket": SLOTS_PER_BUCKET,
        "benchmarks": selected_benchmarks(args.benchmarks),
        "max_wg": args.max_wg,
        "points": [
            {
                **asdict(point),
                "filter_buckets_per_partition": (
                    point.filter_buckets_per_partition
                ),
                "filter_slots_per_partition": point.filter_slots_per_partition,
                "benchmark_flags": list(point.benchmark_flags()),
                "configurations": [
                    config_name(point, mechanism)
                    for mechanism in MECHANISMS
                ],
            }
            for point in points
        ],
    }


def verify_or_write_campaign_manifest(args, points) -> Path:
    path = args.output_dir / "SENSITIVITY_CAMPAIGN.json"
    expected = campaign_manifest(args, points)
    if path.is_file():
        recorded = json.loads(path.read_text(encoding="utf-8"))
        if recorded != expected:
            raise RuntimeError(f"sensitivity campaign mismatch: {path}")
        return path
    path.write_text(json.dumps(expected, indent=2) + "\n", encoding="utf-8")
    return path


def worker_counts(args, count):
    maximum = min(args.max_workers, count)
    initial = maximum
    if args.memory_reserve_gib > 0:
        initial = min(args.initial_workers, maximum)
    return maximum, initial


def run_queue(args, experiments, preserve_metadata=False):
    maximum, initial = worker_counts(args, len(experiments))
    print(
        f"Launching {len(experiments)} globally interleaved cells with "
        f"max_workers={maximum}, initial_workers={initial}",
        flush=True,
    )
    print(
        f"Every sensitivity point runs {MECHANISMS[0]} and "
        f"{MECHANISMS[1]}; memory reserve={args.memory_reserve_gib:g} GiB, "
        f"scan interval={args.memory_scan_minutes:g} minutes.",
        flush=True,
    )
    write_experiment_metadata(
        experiments,
        preserve_existing=preserve_metadata,
        launcher={
            "kind": "cupath_sensitivity",
            "schedule": "globally_interleaved_diagonal",
            "max_workers": maximum,
            "requested_max_workers": args.max_workers,
            "initial_workers": initial,
            "dynamic_memory_admission": args.memory_reserve_gib > 0,
            "memory_reserve_gib": args.memory_reserve_gib,
            "memory_scan_minutes": args.memory_scan_minutes,
            "timeout_minutes": args.timeout_minutes,
            "rerun_missing": preserve_metadata,
        },
    )
    run_all_experiments(
        experiments,
        maximum,
        initial_workers=initial,
        memory_reserve_gib=args.memory_reserve_gib,
        memory_scan_seconds=args.memory_scan_minutes * 60,
    )


def resume_campaign(args):
    experiments = load_experiments_from_metadata(args.output_dir)
    experiments = filter_missing_metric_exps(experiments, args.output_dir)
    if not experiments:
        print(f"No missing experiments in {args.output_dir}")
        return
    for experiment in experiments:
        experiment["timeout_seconds"] = int(args.timeout_minutes * 60)
    verify_or_write_binary_manifest(experiments, require_existing=True)
    if args.dry_run:
        dry_run_commands(experiments)
        return
    run_queue(args, experiments, preserve_metadata=True)


def parse_args():
    parser = argparse.ArgumentParser(
        description=(
            "Run Baseline and Complete for all CuPath sensitivity points "
            "through one globally interleaved worker pool."
        )
    )
    parser.add_argument("--output-dir", type=Path, default=None)
    parser.add_argument("--binary", type=Path, default=None)
    parser.add_argument("--benchmarks", default="traditional")
    parser.add_argument("--dimensions", default="filter,l2,row")
    parser.add_argument("--include-reference", action="store_true")
    parser.add_argument("--max-workers", type=int, default=14)
    parser.add_argument("--initial-workers", type=int, default=1)
    parser.add_argument("--memory-reserve-gib", type=float, default=50.0)
    parser.add_argument("--memory-scan-minutes", type=float, default=30.0)
    parser.add_argument("--max-wg", type=int, default=78600)
    parser.add_argument("--timeout-minutes", type=float, default=0.0)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.max_workers < 1 or args.initial_workers < 1:
        parser.error("worker counts must be positive")
    if args.initial_workers > args.max_workers:
        parser.error("--initial-workers cannot exceed --max-workers")
    if args.memory_reserve_gib < 0 or args.memory_scan_minutes <= 0:
        parser.error("invalid memory admission settings")
    if args.max_wg < 0 or args.timeout_minutes < 0:
        parser.error("WG and timeout limits cannot be negative")
    try:
        selected_benchmarks(args.benchmarks)
        parse_dimensions(args.dimensions)
    except ValueError as error:
        parser.error(str(error))
    args.output_dir = (
        args.output_dir.resolve() if args.output_dir else default_output_dir()
    )
    if args.binary:
        args.binary = args.binary.resolve()
        if not args.binary.is_file():
            parser.error(f"binary does not exist: {args.binary}")
    return args


def main():
    install_signal_handlers()
    atexit.register(terminate_all_processes)
    args = parse_args()
    points = selected_points(args)

    metadata = args.output_dir / "EXPERIMENT_METADATA.json"
    if metadata.is_file():
        set_output_dir(str(args.output_dir))
        verify_or_write_campaign_manifest(args, points)
        resume_campaign(args)
        return

    if args.dry_run:
        binary = args.binary or ROOT / "akkalat" / "baseline" / "baseline"
        experiments = build_experiments(args, points, binary)
        print(
            f"Dry run contains {len(experiments)} interleaved Baseline/Complete "
            "cells."
        )
        dry_run_commands(experiments)
        return

    if args.output_dir.exists() and any(args.output_dir.iterdir()):
        raise RuntimeError(
            "output directory is non-empty but has no experiment metadata: "
            f"{args.output_dir}"
        )
    args.output_dir.mkdir(parents=True, exist_ok=True)
    set_output_dir(str(args.output_dir))
    binary = args.binary or build_binary(args.output_dir)
    experiments = build_experiments(args, points, binary)
    freeze_experiment_binaries(experiments)
    verify_or_write_binary_manifest(experiments)
    verify_or_write_campaign_manifest(args, points)
    run_queue(args, experiments)


if __name__ == "__main__":
    main()
