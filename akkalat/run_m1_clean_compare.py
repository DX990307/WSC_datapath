#!/usr/bin/env python3
"""Run baseline vs current Mechanism 1 using runall2's runner engine."""

from __future__ import annotations

import argparse
import atexit
import concurrent.futures
import csv
from datetime import datetime
from pathlib import Path
import shlex
import sys

import runall2


ROOT_DIR = Path(__file__).resolve().parent
UNICODE_DASH_TRANSLATION = str.maketrans({
    "\u2013": "-",
    "\u2014": "-",
    "\u2212": "-",
})
ARMS = ("baseline", "mechanism1")


def normalize_option_dashes(argv: list[str]) -> list[str]:
    normalized = []
    for arg in argv:
        if not arg.startswith(("-", "\u2013", "\u2014", "\u2212")):
            normalized.append(arg)
            continue
        if "=" in arg:
            name, value = arg.split("=", 1)
            normalized.append(f"{name.translate(UNICODE_DASH_TRANSLATION)}={value}")
        else:
            normalized.append(arg.translate(UNICODE_DASH_TRANSLATION))
    return normalized


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Run baseline and current Mechanism 1 with identical runall2 "
            "benchmark/config settings."
        )
    )
    parser.add_argument("--benchmarks", default="all")
    parser.add_argument("--configs", default="sample_all")
    parser.add_argument("--output-root", type=Path, default=None)
    parser.add_argument("--max-workers", type=int, default=10)
    parser.add_argument("--max-wg", type=int, default=78600)
    parser.add_argument("--sampled-warmups", default="512")
    parser.add_argument("--sampled-granularities", default="512")
    parser.add_argument("--sampled-sweep", action="store_true")
    parser.add_argument("--balanced-sweep", action="store_true")
    parser.add_argument("--sampled-threshold", type=float, default=0)
    parser.add_argument("--branch-sampled-coverage-threshold", type=float, default=0)
    parser.add_argument("--branch-sampled-threshold", type=float, default=0)
    parser.add_argument("--kernel-sampled-threshold", type=int, default=0)
    parser.add_argument("--kernel-sampled-distance-threshold", type=int, default=0)
    parser.add_argument("--loop-sampled-warmup", type=int, default=0)
    parser.add_argument("--loop-sampled-min-iters", type=int, default=0)
    parser.add_argument("--loop-sampled-threshold", type=float, default=0)
    parser.add_argument(
        "--sampled-parallel-limit",
        type=int,
        default=runall2.DEFAULT_SAMPLED_PARALLEL_LIMIT,
    )
    parser.add_argument("--switch-latency", type=int, default=1)
    parser.add_argument("--mmutlb-lookup-latency", type=int, default=80)
    parser.add_argument("--l2-dir-batch-window", type=int, default=4)
    parser.add_argument("--timeout-minutes", type=float, default=0)
    parser.add_argument("--extra-benchmark-flags", default="")
    parser.add_argument("--quick", action="store_true")
    parser.add_argument("--photon-debug", action="store_true")
    parser.add_argument("--photon-verbose", action="store_true")
    parser.add_argument("--report-l2-source", action="store_true")
    parser.add_argument("--l2-source-tile-width", type=int, default=7)
    parser.add_argument("--force-local-data-access", action="store_true")
    parser.add_argument(
        "--arm",
        default="baseline,mechanism1",
        help=(
            "Comma-separated arms to run: baseline, mechanism1, or both. "
            "Default: baseline,mechanism1."
        ),
    )
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument("--skip-baseline", action="store_true")
    parser.add_argument("--skip-mechanism", action="store_true")
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--compare-only", action="store_true")
    parser.add_argument("--skip-summary", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument(
        "--enable-servers",
        "--enable-server",
        dest="enable_servers",
        action="store_true",
        help="Do not pass --disable-servers to benchmark binaries.",
    )
    return parser


def default_output_root() -> Path:
    stamp = datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
    return ROOT_DIR / "results" / f"{stamp}-m1-baseline-vs-mechanism1"


def runall_args(args: argparse.Namespace, arm: str) -> argparse.Namespace:
    mechanism = arm == "mechanism1"
    return argparse.Namespace(
        rerun_missing="",
        output_dir="",
        max_workers=args.max_workers,
        skip_build=args.skip_build,
        mmutlb_lookup_latency=args.mmutlb_lookup_latency,
        switch_latency=args.switch_latency,
        benchmarks=args.benchmarks,
        configs=args.configs,
        extra_benchmark_flags=args.extra_benchmark_flags,
        quick=args.quick,
        max_wg=args.max_wg,
        timeout_minutes=args.timeout_minutes,
        photon_debug=args.photon_debug,
        photon_verbose=args.photon_verbose,
        disable_servers=not args.enable_servers,
        report_l2_source=args.report_l2_source,
        l2_source_tile_width=args.l2_source_tile_width,
        force_local_data_access=args.force_local_data_access,
        l2_dir_batch_window=args.l2_dir_batch_window if mechanism else 0,
        l2_dram_access_unit_coalesce=mechanism,
        l1v_remote_max_inflight=0,
        l1v_mshr_entries=0,
        l1v_max_concurrent_trans=0,
        l1v_bottom_reorder_policy="",
        l1v_bottom_reorder_window=0,
        l1v_bottom_reorder_max_age_ns=-1,
        trace_sharing=False,
        trace_sharing_sample=1,
        trace_sharing_max_records=1000000,
        trace_memory_path=False,
        trace_memory_path_warmup_accesses=100000,
        trace_memory_path_max_records=100000,
        trace_memory_path_exit_on_complete=False,
        sampled_sweep=args.sampled_sweep,
        balanced_sweep=args.balanced_sweep,
        sampled_warmups=args.sampled_warmups,
        sampled_granularities=args.sampled_granularities,
        sampled_threshold=args.sampled_threshold,
        branch_sampled_coverage_threshold=(
            args.branch_sampled_coverage_threshold
        ),
        branch_sampled_threshold=args.branch_sampled_threshold,
        kernel_sampled_threshold=args.kernel_sampled_threshold,
        kernel_sampled_distance_threshold=args.kernel_sampled_distance_threshold,
        loop_sampled_warmup=args.loop_sampled_warmup,
        loop_sampled_min_iters=args.loop_sampled_min_iters,
        loop_sampled_threshold=args.loop_sampled_threshold,
        allow_sampled_parallel=False,
        sampled_parallel_limit=args.sampled_parallel_limit,
    )


def build_arm_exps(args: argparse.Namespace, arm: str) -> list[dict]:
    rargs = runall_args(args, arm)
    ablation_configs = runall2.build_ablation_configs(rargs)
    common_flags = runall2.build_common_flags(rargs)
    exps = runall2.make_exps(rargs, ablation_configs)
    runall2.prepare_exps(rargs, exps, common_flags)
    for exp in exps:
        exp["arm"] = arm
        exp["base_config_name"] = exp["config_name"]
        exp["config_name"] = f"{arm}_{exp['config_name']}"
    return exps


def build_all_exps(args: argparse.Namespace) -> list[dict]:
    exps = []
    arms = parse_arms(args.arm)
    run_baseline = "baseline" in arms and not args.skip_baseline
    run_mechanism = "mechanism1" in arms and not args.skip_mechanism
    if run_baseline:
        exps += build_arm_exps(args, "baseline")
    if run_mechanism:
        exps += build_arm_exps(args, "mechanism1")
    return exps


def parse_arms(value: str) -> set[str]:
    valid = {"baseline", "mechanism1"}
    requested = {item.strip() for item in value.split(",") if item.strip()}
    if "both" in requested:
        requested.remove("both")
        requested.update(valid)
    unknown = sorted(requested - valid)
    if unknown:
        raise ValueError(f"unknown --arm values: {unknown}")
    if not requested:
        raise ValueError("--arm must include baseline, mechanism1, or both")
    return requested


def metrics_path(output_root: Path, exp: dict) -> Path:
    stem = f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}'
    return output_root / f"{stem}_metrics.csv"


def filter_existing(output_root: Path, exps: list[dict]) -> list[dict]:
    return [exp for exp in exps if not metrics_path(output_root, exp).exists()]


def choose_max_workers(args: argparse.Namespace, exp_count: int) -> int:
    if args.max_workers <= 0:
        raise ValueError("--max-workers must be greater than 0")
    max_workers = min(args.max_workers, exp_count)
    if runall2.sampled_param_sweep_requested(runall_args(args, "baseline")):
        cap = args.sampled_parallel_limit
        if cap > 0 and max_workers > cap:
            print(
                "Sampled parameter sweep parallelism capped at "
                f"max_workers={cap}. Use --sampled-parallel-limit=0 "
                "to disable this cap.",
                flush=True,
            )
            max_workers = cap
    return max_workers


def print_launch_summary(output_root: Path, exps: list[dict], max_workers: int) -> None:
    counts = {arm: 0 for arm in ARMS}
    for exp in exps:
        counts[exp["arm"]] += 1
    print(f"output root: {output_root}", flush=True)
    print(
        "experiments: "
        + ", ".join(f"{arm}={counts[arm]}" for arm in ARMS)
        + f", total={len(exps)}, max_workers={max_workers}",
        flush=True,
    )


def write_commands(output_root: Path, exps: list[dict]) -> None:
    path = output_root / "commands.txt"
    with path.open("w") as f:
        for exp in exps:
            f.write(shlex.join(runall2.experiment_command(exp)))
            f.write("\n")


def run_experiments(exps: list[dict], max_workers: int) -> list[dict]:
    results = []
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as pool:
            futures = [pool.submit(runall2.run_exp, exp) for exp in exps]
            for future in concurrent.futures.as_completed(futures):
                result = future.result()
                results.append(result)
                print(result, flush=True)
    finally:
        runall2.terminate_all_processes()
    return results


def read_driver_metrics(path: Path) -> dict[str, float]:
    metrics: dict[str, float] = {}
    if not path.exists():
        return metrics
    with path.open(newline="") as f:
        for row in csv.DictReader(f, skipinitialspace=True):
            if row.get("where", "").strip() != "Driver":
                continue
            try:
                metrics[row.get("what", "").strip()] = float(row["value"])
            except (KeyError, ValueError):
                pass
    return metrics


def pct_delta(new: float | None, old: float | None) -> float | str:
    if not old or new is None:
        return ""
    return (new - old) / old * 100


def speedup_pct(old: float | None, new: float | None) -> float | str:
    if not old or not new:
        return ""
    return old / new * 100 - 100


def exp_key(exp: dict) -> tuple[str, str]:
    return exp["benchmark"], exp["base_config_name"]


def collect_summary_metrics(
    output_root: Path,
    all_exps: list[dict],
) -> dict[tuple[str, str], dict[str, dict[str, float]]]:
    grouped: dict[tuple[str, str], dict[str, dict[str, float]]] = {}
    for exp in all_exps:
        grouped.setdefault(exp_key(exp), {})[exp["arm"]] = read_driver_metrics(
            metrics_path(output_root, exp)
        )
    return grouped


def write_summary(output_root: Path, all_exps: list[dict]) -> Path:
    summary_csv = output_root / "summary.csv"
    grouped = collect_summary_metrics(output_root, all_exps)
    fields = [
        "benchmark",
        "config",
        "baseline_time",
        "mechanism_time",
        "time_delta_pct",
        "speedup_pct",
        "baseline_wg",
        "mechanism_wg",
        "baseline_max_wg_reached",
        "mechanism_max_wg_reached",
        "l2_batch_dir_groups",
        "l2_batch_dir_requests",
        "l2_batch_dir_avg_size",
        "l2_batch_dir_max_size",
        "l2_batch_access_unit_reads",
        "l2_batch_access_unit_coalesced",
        "l2_batch_access_unit_saved_pct",
    ]

    with summary_csv.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for benchmark, config in sorted(grouped):
            row_metrics = grouped[(benchmark, config)]
            baseline = row_metrics.get("baseline", {})
            mechanism = row_metrics.get("mechanism1", {})
            baseline_time = baseline.get("total_time")
            mechanism_time = mechanism.get("total_time")
            reads = mechanism.get("l2_batch_access_unit_reads", 0.0)
            coalesced = mechanism.get("l2_batch_access_unit_coalesced", 0.0)
            access_unit_total = reads + coalesced
            writer.writerow({
                "benchmark": benchmark,
                "config": config,
                "baseline_time": baseline_time if baseline_time is not None else "",
                "mechanism_time": mechanism_time if mechanism_time is not None else "",
                "time_delta_pct": pct_delta(mechanism_time, baseline_time),
                "speedup_pct": speedup_pct(baseline_time, mechanism_time),
                "baseline_wg": baseline.get("total_wg_count", ""),
                "mechanism_wg": mechanism.get("total_wg_count", ""),
                "baseline_max_wg_reached": baseline.get("max_wg_reached", ""),
                "mechanism_max_wg_reached": mechanism.get("max_wg_reached", ""),
                "l2_batch_dir_groups": mechanism.get("l2_batch_dir_groups", ""),
                "l2_batch_dir_requests": mechanism.get("l2_batch_dir_requests", ""),
                "l2_batch_dir_avg_size": mechanism.get("l2_batch_dir_avg_size", ""),
                "l2_batch_dir_max_size": mechanism.get("l2_batch_dir_max_size", ""),
                "l2_batch_access_unit_reads": reads,
                "l2_batch_access_unit_coalesced": coalesced,
                "l2_batch_access_unit_saved_pct": (
                    coalesced / access_unit_total * 100 if access_unit_total else ""
                ),
            })
    return summary_csv


def main() -> int:
    args = build_arg_parser().parse_args(normalize_option_dashes(sys.argv[1:]))
    output_root = (args.output_root or default_output_root()).resolve()
    output_root.mkdir(parents=True, exist_ok=True)
    runall2.set_output_dir(str(output_root))

    all_exps = build_all_exps(args)
    if not all_exps:
        print("No experiments configured.", flush=True)
        return 0

    write_commands(output_root, all_exps)
    run_exps = filter_existing(output_root, all_exps) if args.resume else all_exps
    if args.compare_only:
        run_exps = []

    if run_exps:
        max_workers = choose_max_workers(args, len(run_exps))
        print_launch_summary(output_root, run_exps, max_workers)
        if args.dry_run:
            runall2.dry_run_commands(run_exps)
            return 0

        runall2.install_signal_handlers()
        atexit.register(runall2.terminate_all_processes)
        if args.skip_build:
            print("Skipping target build (--skip-build).", flush=True)
        else:
            runall2.build_targets(run_exps)
        results = run_experiments(run_exps, max_workers)
        failed = [r for r in results if r.get("returncode") != 0]
        if failed:
            print(f"{len(failed)} experiments failed.", flush=True)
    else:
        print(f"No experiments to run. output root: {output_root}", flush=True)

    if not args.skip_summary and not args.dry_run:
        summary_csv = write_summary(output_root, all_exps)
        print(f"summary: {summary_csv}", flush=True)

    return 1 if run_exps and any(r.get("returncode") != 0 for r in results) else 0


if __name__ == "__main__":
    raise SystemExit(main())
