import atexit
import concurrent.futures
from datetime import datetime
import os
import shlex
import json
import time
from pathlib import Path

import slot_pool
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
    exp_stdout_path,
    freeze_experiment_binaries,
    install_signal_handlers,
    load_experiments_from_metadata,
    run_exp,
    running_process_pids,
    set_output_dir,
    terminate_all_processes,
    verify_or_write_binary_manifest,
    write_experiment_metadata,
)

# While a simulator child is stopped, launches are held and retried this often.
STOPPED_POLL_SECONDS = 15.0
# The stall watchdog samples the children at most this far apart.
WATCHDOG_POLL_SECONDS = 60.0


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
            "launch_interval_seconds": args.launch_interval_seconds,
            "stall_alert_minutes": args.stall_alert_minutes,
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
        launch_interval_seconds=args.launch_interval_seconds,
        stall_alert_seconds=args.stall_alert_minutes * 60,
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
            "launch_interval_seconds": args.launch_interval_seconds,
            "stall_alert_minutes": args.stall_alert_minutes,
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
        launch_interval_seconds=args.launch_interval_seconds,
        stall_alert_seconds=args.stall_alert_minutes * 60,
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
    if args.launch_interval_seconds < 0 or args.stall_alert_minutes < 0:
        raise ValueError(
            "--launch-interval-seconds and --stall-alert-minutes must not "
            "be negative"
        )

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
    if args.launch_interval_seconds > 0:
        print(
            "Launch pacing: at most one launch every "
            f"{args.launch_interval_seconds:g} seconds"
        )
    if args.stall_alert_minutes > 0:
        print(f"Stall alert after {args.stall_alert_minutes:g} minutes")


def read_process_state(pid, proc_root="/proc"):
    """Returns the state letter of pid from /proc/<pid>/stat, or None."""
    try:
        with open(os.path.join(proc_root, str(pid), "stat"),
                  encoding="utf-8", errors="replace") as stream:
            stat = stream.read()
    except OSError:
        return None
    # The command name is in parentheses and may contain spaces or
    # parentheses itself; the state is the first field after the last ")".
    fields = stat[stat.rfind(")") + 1:].split()
    return fields[0] if fields else None


def count_child_states(pids=None, state_fn=read_process_state):
    """Returns (running, stopped) counts of this campaign's simulators.

    Stopped means state T (stopped by SIGSTOP or a similar signal). A child
    whose state cannot be read (it just exited, or the host has no /proc)
    counts as running, so it never holds launches back.
    """
    if pids is None:
        pids = running_process_pids()
    stopped = sum(state_fn(pid) == "T" for pid in pids)
    return len(pids) - stopped, stopped


def child_stdout_sizes(exps):
    """Maps the stdout file of each active experiment to its size."""
    sizes = {}
    for exp in exps:
        path = exp_stdout_path(exp)
        try:
            sizes[path] = os.path.getsize(path)
        except OSError:  # not created yet
            continue
    return sizes


class StallWatchdog:
    """Notices a campaign whose simulators have stopped making progress.

    A stall is every child stopped, or no child's stdout growing, for
    alert_seconds; check() then returns one alert line per alert_seconds
    until progress resumes. A campaign without children is waiting for
    admission (memory or shared slots), which is not a stall.
    """

    def __init__(self, alert_seconds, now):
        self.alert_seconds = alert_seconds
        self.sizes = {}
        self.last_growth = now
        self.all_stopped_since = None
        self.last_alert = None

    def progress(self, now):
        """Records progress that stdout does not show (a finished child)."""
        self.last_growth = now

    def check(self, now, sizes, running, stopped):
        """sizes maps each active child's stdout path to its size."""
        if any(size > self.sizes.get(path, -1)
               for path, size in sizes.items()):
            self.last_growth = now
        self.sizes = dict(sizes)
        if not sizes and not running and not stopped:
            self.last_growth = now
            self.all_stopped_since = None
            self.last_alert = None
            return None
        if stopped and not running:
            if self.all_stopped_since is None:
                self.all_stopped_since = now
        else:
            self.all_stopped_since = None

        if (self.all_stopped_since is not None and
                now - self.all_stopped_since >= self.alert_seconds):
            minutes = (now - self.all_stopped_since) / 60
            reason = (f"all {stopped} simulator children stopped (state T) "
                      f"for {minutes:.1f} minutes")
        elif now - self.last_growth >= self.alert_seconds:
            minutes = (now - self.last_growth) / 60
            reason = f"no simulator stdout grew for {minutes:.1f} minutes"
        else:
            self.last_alert = None
            return None
        if (self.last_alert is not None and
                now - self.last_alert < self.alert_seconds):
            return None
        self.last_alert = now
        stamp = datetime.now().isoformat(timespec="seconds")
        return (f"*** STALL {stamp}: {reason}; children running={running}, "
                f"stopped={stopped} ***")


def run_all_experiments(
    exps,
    max_workers,
    initial_workers=None,
    memory_reserve_gib=0.0,
    memory_scan_seconds=1800.0,
    mem_available_fn=read_mem_available_gib,
    launch_interval_seconds=0.0,
    stall_alert_seconds=0.0,
    child_states_fn=count_child_states,
):
    dynamic = memory_reserve_gib > 0
    if initial_workers is None or not dynamic:
        initial_workers = max_workers
    if not 1 <= initial_workers <= max_workers:
        raise ValueError("initial_workers must be between 1 and max_workers")
    if dynamic and memory_scan_seconds <= 0:
        raise ValueError("memory_scan_seconds must be greater than 0")
    if launch_interval_seconds < 0 or stall_alert_seconds < 0:
        raise ValueError(
            "launch_interval_seconds and stall_alert_seconds must not be "
            "negative"
        )

    executor = concurrent.futures.ThreadPoolExecutor(max_workers=max_workers)
    pending = iter(exps)
    pending_exhausted = False
    futures = set()
    future_exps = {}
    # Optional cross-campaign cap (EXP_SLOT_DIR, see slot_pool.py): every
    # launch also needs a shared slot; slots[future] holds it until the end.
    pool = slot_pool.SlotPool.from_env()
    slots = {}
    held_back = []
    failures = []
    admitted_concurrency = initial_workers
    next_scan = time.monotonic() + memory_scan_seconds
    # Launch pacing: a new simulator takes minutes to reach its working set,
    # so MemAvailable read right after a launch overstates the free memory.
    # Spacing launches lets each admission see its predecessors' memory.
    last_launch = None
    holding_for_stopped = False
    watchdog = None
    next_watch = None
    if stall_alert_seconds > 0:
        watchdog = StallWatchdog(stall_alert_seconds, time.monotonic())
        watch_period = min(WATCHDOG_POLL_SECONDS, stall_alert_seconds)
        next_watch = time.monotonic()

    def memory_allows_launch():
        if not dynamic:
            return True, None
        available = mem_available_fn()
        return available > memory_reserve_gib, available

    def next_exp():
        nonlocal pending_exhausted
        if held_back:
            return held_back.pop()
        try:
            return next(pending)
        except StopIteration:
            pending_exhausted = True
            return None

    def has_pending():
        # Peeks without consuming: a held launch keeps its place in line.
        exp = next_exp()
        if exp is None:
            return False
        held_back.append(exp)
        return True

    def fill_available_slots():
        """Launches what is admitted; returns when to retry a held launch."""
        nonlocal last_launch, holding_for_stopped
        # Registered as waiting only while this campaign could launch now but
        # every shared slot is taken, so lower-priority campaigns are held
        # back only when they would otherwise take a slot this one can use.
        waiting = False
        retry_at = None
        while len(futures) < admitted_concurrency and not pending_exhausted:
            # A stopped child keeps its memory and may be resumed at any
            # time, so nothing is launched beside it.
            running, stopped = child_states_fn()
            if stopped:
                if has_pending():
                    if not holding_for_stopped:
                        print(
                            "Holding launches: simulator children stopped: "
                            f"running={running}, stopped={stopped}",
                            flush=True,
                        )
                    holding_for_stopped = True
                    retry_at = time.monotonic() + STOPPED_POLL_SECONDS
                break
            if holding_for_stopped:
                print("No simulator child is stopped; launches resume.",
                      flush=True)
                holding_for_stopped = False
            if launch_interval_seconds > 0 and last_launch is not None:
                ready_at = last_launch + launch_interval_seconds
                if time.monotonic() < ready_at:
                    if has_pending():
                        retry_at = ready_at
                    break
            allowed, available = memory_allows_launch()
            if not allowed:
                break
            exp = next_exp()
            if exp is None:
                break
            slot = None
            if pool:
                slot = pool.try_acquire()
                if slot is None:
                    held_back.append(exp)
                    waiting = True
                    break
            future = executor.submit(run_exp, exp)
            futures.add(future)
            future_exps[future] = exp
            if slot is not None:
                slots[future] = slot
            last_launch = time.monotonic()
            if launch_interval_seconds > 0:
                memory = ("" if available is None
                          else f"MemAvailable={available:.1f} GiB, ")
                print(
                    f"Paced launch: {memory}"
                    f"active={len(futures)}/{admitted_concurrency}, "
                    f"next launch in >= {launch_interval_seconds:g} s",
                    flush=True,
                )
        if pool:
            pool.set_waiting(waiting)
        return retry_at

    def remaining(deadline):
        return max(0.0, deadline - time.monotonic())

    try:
        while futures or not pending_exhausted:
            now = time.monotonic()
            if dynamic and now >= next_scan:
                available = mem_available_fn()
                running, stopped = child_states_fn()
                children = f"children running={running}, stopped={stopped}"
                # Stopped children may resume and need their memory back,
                # so a scan does not admit more while any is stopped.
                if (available > memory_reserve_gib and not stopped and
                        admitted_concurrency < max_workers):
                    admitted_concurrency += 1
                    print(
                        "Memory scan admitted one additional experiment: "
                        f"MemAvailable={available:.1f} GiB, "
                        f"concurrency={admitted_concurrency}/{max_workers}, "
                        f"{children}",
                        flush=True,
                    )
                else:
                    print(
                        "Memory scan kept current concurrency: "
                        f"MemAvailable={available:.1f} GiB, "
                        f"reserve={memory_reserve_gib:.1f} GiB, "
                        f"concurrency={admitted_concurrency}/{max_workers}, "
                        f"{children}",
                        flush=True,
                    )
                next_scan = now + memory_scan_seconds

            if watchdog and now >= next_watch:
                running, stopped = child_states_fn()
                alert = watchdog.check(
                    now, child_stdout_sizes(future_exps.values()),
                    running, stopped,
                )
                if alert:
                    print(alert, flush=True)
                next_watch = now + watch_period

            retry_at = fill_available_slots()
            wake_at = min(
                (t for t in (retry_at, next_watch) if t is not None),
                default=None,
            )
            if not futures:
                if pending_exhausted:
                    break
                if pool:
                    delay = slot_pool.POLL_SECONDS
                else:
                    delay = max(0.0, next_scan - time.monotonic())
                if wake_at is not None:
                    delay = min(delay, remaining(wake_at))
                time.sleep(delay)
                continue

            timeout = None
            if dynamic:
                timeout = max(0.0, next_scan - time.monotonic())
            if pool:
                timeout = (slot_pool.POLL_SECONDS if timeout is None
                           else min(timeout, slot_pool.POLL_SECONDS))
            if wake_at is not None:
                timeout = (remaining(wake_at) if timeout is None
                           else min(timeout, remaining(wake_at)))
            done, _ = concurrent.futures.wait(
                futures,
                timeout=timeout,
                return_when=concurrent.futures.FIRST_COMPLETED,
            )
            for future in done:
                futures.remove(future)
                future_exps.pop(future, None)
                if watchdog:
                    watchdog.progress(time.monotonic())
                if future in slots:
                    slots.pop(future).close()
                result = future.result()
                print(result)
                if not result.get("success"):
                    failures.append(result)
    finally:
        terminate_all_processes()
        for slot in slots.values():
            slot.close()
        if pool:
            pool.set_waiting(False)
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
