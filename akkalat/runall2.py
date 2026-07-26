import atexit
import concurrent.futures
import os
import shlex
import json
import time
from pathlib import Path

from runall2_args import parse_args
from runall2_config import (
    build_ablation_configs,
    build_common_flags,
    disable_layernorm_loop_sampling,
    filter_missing_metric_exps,
    make_exps,
    sampled_param_sweep_requested,
)
from runall2_process import (
    build_targets,
    create_output_dir,
    dry_run_commands,
    freeze_experiment_binaries,
    install_signal_handlers,
    load_experiments_from_metadata,
    run_exp,
    set_output_dir,
    terminate_all_processes,
    verify_or_write_binary_manifest,
    write_experiment_metadata,
)


def main():
    install_signal_handlers()
    atexit.register(terminate_all_processes)

    args = parse_args()
    validate_binary_override(args)

    if args.rerun_missing:
        run_missing_experiments(args)
        return

    common_flags = build_common_flags(args)
    ablation_configs = build_ablation_configs(args)
    exps = make_exps(args, ablation_configs)
    exps = apply_reusable_baseline(args, exps)
    if not exps:
        print("No experiments configured.")
        return

    prepare_output_dir(args, exps)
    exps = maybe_filter_missing(args, exps)
    if not exps:
        return

    max_workers = choose_max_workers(args, len(exps))
    initial_workers = choose_initial_workers(args, max_workers)
    prepare_exps(args, exps, common_flags)
    print_launch_summary(
        args, common_flags, exps, max_workers, initial_workers)

    if args.dry_run:
        dry_run_commands(exps)
        return

    if args.skip_build:
        print("Skipping target build (--skip-build).")
    else:
        build_targets(exps)
    frozen = freeze_experiment_binaries(exps)
    print(f"Froze experiment binaries: {frozen}")
    manifest = verify_or_write_binary_manifest(
        exps, require_existing=bool(args.rerun_missing)
    )
    print(f"Verified experiment binary manifest: {manifest}")
    metadata = write_experiment_metadata(
        exps,
        preserve_existing=bool(args.rerun_missing),
        launcher={
            "max_workers": max_workers,
            "requested_max_workers": args.max_workers,
            "initial_workers": initial_workers,
            "dynamic_memory_admission": args.memory_reserve_gib > 0,
            "memory_reserve_gib": args.memory_reserve_gib,
            "memory_per_worker_gib": args.memory_per_worker_gib,
            "memory_scan_minutes": args.memory_scan_minutes,
            "mem_available_gib_at_launch": getattr(
                args, "mem_available_gib_at_launch", None
            ),
            "memory_worker_cap": getattr(args, "memory_worker_cap", None),
            "timeout_minutes": args.timeout_minutes,
            "skip_build": args.skip_build,
            "rerun_missing": False,
            "reused_baseline_dir": args.reuse_baseline_dir or None,
        },
    )
    print(f"Recorded experiment metadata: {metadata}")
    run_all_experiments(
        exps,
        max_workers,
        initial_workers=initial_workers,
        memory_reserve_gib=args.memory_reserve_gib,
        memory_scan_seconds=args.memory_scan_minutes * 60,
    )


def run_missing_experiments(args):
    if args.reuse_baseline_dir:
        raise ValueError(
            "--reuse-baseline-dir cannot change a recorded --rerun-missing campaign"
        )
    results_dir = os.path.abspath(args.rerun_missing)
    if not os.path.isdir(results_dir):
        raise ValueError(f"results directory does not exist: {results_dir}")
    set_output_dir(results_dir)

    exps = load_experiments_from_metadata(results_dir)
    if args.rerun_layernorm_safe_sampling:
        updated = disable_layernorm_loop_sampling(exps)
        print(
            "Disabled unsafe loop sampling for "
            f"{updated} recorded LayerNorm cells."
        )
    if args.binary_path:
        requested_binary = os.path.abspath(args.binary_path)
        recorded = {os.path.abspath(exp["binary_path"]) for exp in exps}
        if recorded != {requested_binary}:
            raise RuntimeError(
                "--binary-path does not match the frozen binary recorded by "
                f"the original campaign: recorded={sorted(recorded)}, "
                f"requested={requested_binary}"
            )
    exps = maybe_filter_missing(args, exps)
    if not exps:
        return

    timeout_seconds = int(args.timeout_minutes * 60)
    for exp in exps:
        exp["timeout_seconds"] = timeout_seconds
    max_workers = choose_max_workers(args, len(exps))
    initial_workers = choose_initial_workers(args, max_workers)
    print_launch_summary(args, [], exps, max_workers, initial_workers)
    if args.dry_run:
        dry_run_commands(exps)
        return
    if not args.skip_build:
        raise ValueError("--rerun-missing requires --skip-build")
    manifest = verify_or_write_binary_manifest(exps, require_existing=True)
    print(f"Verified experiment binary manifest: {manifest}")
    metadata = write_experiment_metadata(
        exps,
        preserve_existing=True,
        launcher={
            "max_workers": max_workers,
            "requested_max_workers": args.max_workers,
            "initial_workers": initial_workers,
            "dynamic_memory_admission": args.memory_reserve_gib > 0,
            "memory_reserve_gib": args.memory_reserve_gib,
            "memory_per_worker_gib": args.memory_per_worker_gib,
            "memory_scan_minutes": args.memory_scan_minutes,
            "mem_available_gib_at_launch": getattr(
                args, "mem_available_gib_at_launch", None
            ),
            "memory_worker_cap": getattr(args, "memory_worker_cap", None),
            "timeout_minutes": args.timeout_minutes,
            "skip_build": args.skip_build,
            "rerun_missing": True,
            "rerun_layernorm_safe_sampling": (
                args.rerun_layernorm_safe_sampling
            ),
        },
    )
    print(f"Recorded experiment metadata: {metadata}")
    run_all_experiments(
        exps,
        max_workers,
        initial_workers=initial_workers,
        memory_reserve_gib=args.memory_reserve_gib,
        memory_scan_seconds=args.memory_scan_minutes * 60,
    )


def prepare_output_dir(args, exps):
    del exps
    if args.rerun_missing:
        results_dir = os.path.abspath(args.rerun_missing)
        if not os.path.isdir(results_dir):
            raise ValueError(f"results directory does not exist: {results_dir}")
        set_output_dir(results_dir)
        return

    if args.output_dir:
        results_dir = os.path.abspath(args.output_dir)
        os.makedirs(results_dir, exist_ok=True)
        set_output_dir(results_dir)
        return

    create_output_dir()


def apply_reusable_baseline(args, exps):
    if not args.reuse_baseline_dir:
        return exps
    directory = Path(args.reuse_baseline_dir).resolve()
    manifest_path = directory / "BASELINE_LIBRARY.json"
    if not manifest_path.is_file():
        raise ValueError(f"missing Baseline library manifest: {manifest_path}")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ValueError(f"invalid Baseline library manifest: {manifest_path}") from error
    if manifest.get("kind") != "reusable_baseline_library":
        raise ValueError(f"not a reusable Baseline library: {manifest_path}")

    baseline_cells = [exp for exp in exps if exp["config_name"] == "baseline"]
    missing = []
    for exp in baseline_cells:
        path = directory / f"baseline_{exp['benchmark']}_baseline_metrics.csv"
        if not path.is_file():
            missing.append(exp["benchmark"])
    if missing:
        raise ValueError(
            "Baseline library is missing selected benchmarks: "
            + ",".join(sorted(set(missing)))
        )
    args.reuse_baseline_dir = str(directory)
    if baseline_cells:
        print(
            f"Reusing {len(baseline_cells)} Baseline cells from {directory}; "
            "no Baseline simulator will be launched."
        )
    return [exp for exp in exps if exp["config_name"] != "baseline"]


def maybe_filter_missing(args, exps):
    if not args.rerun_missing:
        return exps

    results_dir = os.path.abspath(args.rerun_missing)
    missing = filter_missing_metric_exps(exps, results_dir)
    if not missing:
        print(f"No missing-metrics experiments found in {results_dir}")
        return []

    print(f"Rerunning {len(missing)} experiments with missing metrics in {results_dir}")
    return missing


def read_mem_available_gib(path="/proc/meminfo"):
    with open(path, encoding="utf-8") as stream:
        for line in stream:
            fields = line.split()
            if fields and fields[0] == "MemAvailable:":
                return int(fields[1]) / (1024 * 1024)
    raise RuntimeError(f"MemAvailable is missing from {path}")


def choose_max_workers(args, exp_count, mem_available_gib=None):
    if args.max_workers <= 0:
        raise ValueError("MAX_WORKERS must be greater than 0")

    reserve = args.memory_reserve_gib
    per_worker = args.memory_per_worker_gib
    if reserve < 0 or per_worker < 0:
        raise ValueError("memory budgets must not be negative")
    if args.memory_scan_minutes <= 0:
        raise ValueError("--memory-scan-minutes must be greater than 0")

    max_workers = min(args.max_workers, exp_count)
    extra_flags = set(shlex.split(args.extra_benchmark_flags or ""))
    sampled_run = sampled_param_sweep_requested(args) or bool(
        extra_flags.intersection({
            "-sampled",
            "-branch-sampled",
            "-kernel-sampled",
            "-loop-sampled",
        })
    )
    if sampled_run:
        cap = args.sampled_parallel_limit
        if cap > 0 and max_workers > cap:
            print(
                "Sampled execution parallelism capped at "
                f"max_workers={cap}. Use --sampled-parallel-limit=0 "
                "to disable this cap."
            )
            max_workers = cap

    if per_worker > 0:
        print(
            "Ignoring deprecated --memory-per-worker-gib; dynamic memory "
            "admission uses only MemAvailable and --memory-reserve-gib."
        )
    if reserve > 0:
        if mem_available_gib is None:
            mem_available_gib = read_mem_available_gib()
        args.mem_available_gib_at_launch = round(mem_available_gib, 3)
        args.memory_worker_cap = max_workers
    return max_workers


def choose_initial_workers(args, max_workers):
    if args.memory_reserve_gib <= 0:
        return max_workers
    if args.initial_workers <= 0:
        raise ValueError("--initial-workers must be greater than 0")
    return min(args.initial_workers, max_workers)


def prepare_exps(args, exps, common_flags):
    binary_path = os.path.abspath(args.binary_path) if args.binary_path else ""
    timeout_seconds = int(args.timeout_minutes * 60)
    for exp in exps:
        if binary_path:
            exp["binary_path"] = binary_path
        exp["common_flags"] = common_flags
        exp["timeout_seconds"] = timeout_seconds
        exp["trace_sharing"] = args.trace_sharing
        exp["trace_sharing_sample"] = args.trace_sharing_sample
        exp["trace_sharing_max_records"] = args.trace_sharing_max_records
        exp["trace_remote_origin"] = args.trace_remote_origin
        exp["trace_remote_origin_max_records"] = (
            args.trace_remote_origin_max_records
        )
        exp["trace_memory_path"] = args.trace_memory_path
        exp["trace_memory_path_warmup_accesses"] = (
            args.trace_memory_path_warmup_accesses
        )
        exp["trace_memory_path_max_records"] = args.trace_memory_path_max_records
        exp["trace_memory_path_exit_on_complete"] = (
            args.trace_memory_path_exit_on_complete
        )
        # An ablation sweep can share one launch with observation collection,
        # but only the exact mechanisms-off baseline is allowed to carry the
        # tracer. Names such as baseline_remote_request_only are mechanisms-on
        # configurations and must not match here.
        exp["trace_observation"] = (
            args.trace_observation and exp["config_name"] == "baseline"
        )
        exp["trace_observation_warmup_accesses"] = (
            args.trace_observation_warmup_accesses
        )
        exp["trace_observation_max_records"] = (
            args.trace_observation_max_records
        )
        exp["trace_observation_exit_on_complete"] = (
            args.trace_observation_exit_on_complete
        )
        exp["trace_observation_dram_warmup_accesses"] = (
            args.trace_observation_dram_warmup_accesses
        )
        exp["trace_observation_dram_max_records"] = (
            args.trace_observation_dram_max_records
        )
        exp["trace_observation_remote_warmup_requests"] = (
            args.trace_observation_remote_warmup_requests
        )
        exp["trace_observation_remote_max_records"] = (
            args.trace_observation_remote_max_records
        )
        exp["trace_observation_l2_sample_max"] = (
            args.trace_observation_l2_sample_max
        )


def validate_binary_override(args):
    if not args.binary_path:
        return
    if not args.skip_build:
        raise ValueError("--binary-path requires --skip-build")
    binary_path = os.path.abspath(args.binary_path)
    if not os.path.isfile(binary_path):
        raise ValueError(f"binary path does not exist: {binary_path}")
    if not os.access(binary_path, os.X_OK):
        raise ValueError(f"binary path is not executable: {binary_path}")


def print_launch_summary(
    args, common_flags, exps, max_workers, initial_workers=None
):
    print(f"Using common flags: {shlex.join(common_flags)}")
    if args.trace_observation:
        traced = sum(exp.get("trace_observation", False) for exp in exps)
        print(
            "Observation tracing enabled for mechanisms-off baseline only "
            f"({traced} of {len(exps)} experiments)."
        )
    if args.timeout_minutes > 0:
        print(f"Experiment timeout: {args.timeout_minutes} minutes")
    print(f"Launching {len(exps)} experiments with max_workers={max_workers}")
    if args.memory_reserve_gib > 0:
        print(
            "Dynamic memory admission: "
            f"initial_workers={initial_workers}, "
            f"reserve={args.memory_reserve_gib:.1f} GiB, "
            f"scan_interval={args.memory_scan_minutes:g} minutes"
        )


def run_all_experiments(
    exps,
    max_workers,
    initial_workers=None,
    memory_reserve_gib=0.0,
    memory_scan_seconds=1800.0,
    mem_available_fn=read_mem_available_gib,
):
    dynamic = memory_reserve_gib > 0
    if initial_workers is None or not dynamic:
        initial_workers = max_workers
    if not 1 <= initial_workers <= max_workers:
        raise ValueError("initial_workers must be between 1 and max_workers")
    if dynamic and memory_scan_seconds <= 0:
        raise ValueError("memory_scan_seconds must be greater than 0")

    executor = concurrent.futures.ThreadPoolExecutor(max_workers=max_workers)
    pending = iter(exps)
    pending_exhausted = False
    futures = set()
    failures = []
    admitted_concurrency = initial_workers
    next_scan = time.monotonic() + memory_scan_seconds

    def memory_allows_launch():
        if not dynamic:
            return True
        available = mem_available_fn()
        return available > memory_reserve_gib

    def fill_available_slots():
        nonlocal pending_exhausted
        while len(futures) < admitted_concurrency and not pending_exhausted:
            if not memory_allows_launch():
                return
            try:
                exp = next(pending)
            except StopIteration:
                pending_exhausted = True
                return
            futures.add(executor.submit(run_exp, exp))

    try:
        while futures or not pending_exhausted:
            now = time.monotonic()
            if dynamic and now >= next_scan:
                available = mem_available_fn()
                if (available > memory_reserve_gib and
                        admitted_concurrency < max_workers):
                    admitted_concurrency += 1
                    print(
                        "Memory scan admitted one additional experiment: "
                        f"MemAvailable={available:.1f} GiB, "
                        f"concurrency={admitted_concurrency}/{max_workers}",
                        flush=True,
                    )
                else:
                    print(
                        "Memory scan kept current concurrency: "
                        f"MemAvailable={available:.1f} GiB, "
                        f"reserve={memory_reserve_gib:.1f} GiB, "
                        f"concurrency={admitted_concurrency}/{max_workers}",
                        flush=True,
                    )
                next_scan = now + memory_scan_seconds

            fill_available_slots()
            if not futures:
                if pending_exhausted:
                    break
                time.sleep(max(0.0, next_scan - time.monotonic()))
                continue

            timeout = None
            if dynamic:
                timeout = max(0.0, next_scan - time.monotonic())
            done, _ = concurrent.futures.wait(
                futures,
                timeout=timeout,
                return_when=concurrent.futures.FIRST_COMPLETED,
            )
            for future in done:
                futures.remove(future)
                result = future.result()
                print(result)
                if not result.get("success"):
                    failures.append(result)
    finally:
        terminate_all_processes()
        # A ThreadPoolExecutor context manager waits for every queued future.
        # On Ctrl-C that caused the pending experiments to start one by one
        # after the active simulator processes had already been terminated.
        # Cancel queued work before waiting for the active workers to unwind.
        for future in list(futures):
            future.cancel()
        executor.shutdown(wait=True, cancel_futures=True)
    if failures:
        raise RuntimeError(
            f"{len(failures)} experiment cell(s) failed execution or audit"
        )


if __name__ == "__main__":
    main()
