import argparse
import atexit
import concurrent.futures
from datetime import datetime
import os
from pathlib import Path
import shlex
import signal
import subprocess
import threading

ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
TARGETS = [
    "baseline",
]

# These timing simulations are heavy. Keep parallelism conservative unless
# you're sure the machine can handle more concurrent runs.
MAX_WORKERS = 15

ALL_BENCHMARKS = [
    "aes",
    # "atax",
    # "bicg",
    "bitonicsort",
    # "conv2d",
    "fft",
    "fastwalshtransform",
    "fir",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixtranspose",
    # "nw",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
    # "stencil2d",
]


# Configure which benchmarks to run for each target here.
# Use ["all"] to expand to every benchmark in ALL_BENCHMARKS.
BENCHMARKS_BY_TARGET = {
    "baseline": [
        "all"
    ],
    # "TLBSensitiveStudy": ["all"],
}

DEFAULT_BENCHMARK_FLAGS = []

BASE_COMMON_FLAGS = [
    "-timing",
    "-num-memory-banks=16",
    "-bandwidth=48",
    "-switch-latency=32",
    "-magic-memory-copy",
    "-report-all",
]

DEFAULT_MMUTLB_LOOKUP_LATENCY = 80
DEFAULT_TIMEOUT_MINUTES = 0
DEFAULT_SAMPLED_SWEEP_WARMUPS = [64, 128, 256, 512, 1024, 2048, 4096]
DEFAULT_SAMPLED_SWEEP_GRANULARITIES = [
    128,
    256,
    512,
    1024,
    2048,
    4096,
    8192,
]
DEFAULT_SAMPLED_PARALLEL_LIMIT = MAX_WORKERS

BALANCED_SAMPLED_SWEEP_WARMUPS = [128, 512, 1024]
BALANCED_SAMPLED_SWEEP_GRANULARITIES = [512, 1024]
BALANCED_SAMPLED_THRESHOLD = 0.02
BALANCED_BRANCH_COVERAGE_THRESHOLD = 0.98
BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD = 0.005
BALANCED_KERNEL_DISTANCE_THRESHOLD = 8

output_dir = ""
running_processes = set()
running_processes_lock = threading.Lock()

CONFIGS = [
    (
        "baseline",
        [],
    ),
    (
        "sample_all",
        ["-sampled", "-branch-sampled", "-kernel-sampled"],
    ),
    (
        "sample_wf",
        ["-sampled"],
    ),
    (
        "sample_branch",
        ["-branch-sampled"],
    ),
    (
        "sample_kernel",
        ["-kernel-sampled"],
    ),
]

QUICK_BENCHMARKS = [
    "relu",
]

QUICK_CONFIGS = [
    "baseline",
    "sample_all",
    "sample_wf",
    "sample_branch",
    "sample_kernel",
]


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--rerun-missing",
        dest="rerun_missing",
        default="",
        help="Reuse an existing results directory and rerun only experiments whose metrics CSV is missing.",
    )
    parser.add_argument(
        "--max-workers",
        dest="max_workers",
        type=int,
        default=MAX_WORKERS,
        help="Maximum number of concurrent experiments to launch.",
    )
    parser.add_argument(
        "--mmutlb-lookup-latency",
        dest="mmutlb_lookup_latency",
        type=int,
        default=DEFAULT_MMUTLB_LOOKUP_LATENCY,
        help="Fixed MMUTLB/IOTLB lookup latency, in cycles, applied before each buffered translation request is looked up.",
    )
    parser.add_argument(
        "--benchmarks",
        dest="benchmarks",
        default="",
        help="Comma-separated benchmark list to run. Use all for every benchmark.",
    )
    parser.add_argument(
        "--configs",
        dest="configs",
        default="",
        help=(
            "Comma-separated config list. Choices: "
            + ",".join(name for name, _ in CONFIGS)
            + ". Use all for every config."
        ),
    )
    parser.add_argument(
        "--quick",
        action="store_true",
        help="Run a small Photon validation set: relu with baseline and all sampled configs.",
    )
    parser.add_argument(
        "--max-wg",
        dest="max_wg",
        type=int,
        default=0,
        help="Pass -max-wg to each benchmark. 0 means no limit.",
    )
    parser.add_argument(
        "--timeout-minutes",
        dest="timeout_minutes",
        type=float,
        default=DEFAULT_TIMEOUT_MINUTES,
        help="Kill an experiment after this many minutes. 0 disables timeout.",
    )
    parser.add_argument(
        "--photon-debug",
        action="store_true",
        help="Add -photon-debug to sampled configs.",
    )
    parser.add_argument(
        "--photon-verbose",
        action="store_true",
        help="Add -photon-debug and -photon-debug-verbose to sampled configs.",
    )
    parser.add_argument(
        "--disable-servers",
        action="store_true",
        help="Pass -disable-servers to each benchmark.",
    )
    parser.add_argument(
        "--sampled-sweep",
        action="store_true",
        help=(
            "Scan common WF sampled warmup/granularity pairs. "
            "Applies only to configs containing -sampled."
        ),
    )
    parser.add_argument(
        "--balanced-sweep",
        action="store_true",
        help=(
            "Use a smaller accuracy/speed tradeoff sweep and conservative "
            "Photon thresholds. Equivalent to warmups=512,1024,2048 and "
            "granularities=2048,4096 unless explicitly overridden."
        ),
    )
    parser.add_argument(
        "--sampled-warmups",
        default="",
        help="Comma-separated -sampled-warmup values for sampled configs.",
    )
    parser.add_argument(
        "--sampled-granularities",
        default="",
        help="Comma-separated -sampled-granularity values for sampled configs.",
    )
    parser.add_argument(
        "--sampled-threshold",
        type=float,
        default=0,
        help=(
            "Pass -sampled-threshold to configs containing -sampled. "
            "0 uses the binary default, except --balanced-sweep uses 0.02."
        ),
    )
    parser.add_argument(
        "--branch-sampled-coverage-threshold",
        type=float,
        default=0,
        help=(
            "Pass -branch-sampled-coverage-threshold to branch sampled "
            "configs. 0 uses the binary default, except --balanced-sweep "
            "uses 0.98."
        ),
    )
    parser.add_argument(
        "--branch-sampled-threshold",
        type=float,
        default=0,
        help=(
            "Pass -branch-sampled-threshold to branch sampled configs. "
            "0 uses the binary default, except --balanced-sweep uses 0.005."
        ),
    )
    parser.add_argument(
        "--kernel-sampled-threshold",
        type=int,
        default=0,
        help="Pass -kernel-sampled-threshold to kernel sampled configs. 0 uses the binary default.",
    )
    parser.add_argument(
        "--kernel-sampled-distance-threshold",
        type=int,
        default=0,
        help=(
            "Pass -kernel-sampled-distance-threshold to kernel sampled configs. "
            "0 uses the binary default, except --balanced-sweep uses 8."
        ),
    )
    parser.add_argument(
        "--allow-sampled-parallel",
        action="store_true",
        help=(
            "Allow sampled parameter sweeps to run with --max-workers. "
            "Sampled sweeps now use --max-workers by default."
        ),
    )
    parser.add_argument(
        "--sampled-parallel-limit",
        type=int,
        default=DEFAULT_SAMPLED_PARALLEL_LIMIT,
        help=(
            "Maximum max_workers allowed for sampled parameter sweeps when "
            "sampled sweep is enabled. Use 0 to disable this cap."
        ),
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print commands without building or running them.",
    )
    return parser.parse_args()


def build_common_flags(args):
    common_flags = BASE_COMMON_FLAGS[:]
    if args.disable_servers:
        common_flags.append("-disable-servers")
    common_flags += [
        f"-mmutlb-lookup-latency={args.mmutlb_lookup_latency}",
    ]
    if args.max_wg > 0:
        common_flags.append(f"-max-wg={args.max_wg}")
    return common_flags


def build_ablation_configs(args):
    selected_names = selected_config_names(args)
    sampled_param_grid = build_sampled_param_grid(args)
    selected = []
    for name, flags in CONFIGS:
        if name not in selected_names:
            continue

        config_flags = flags[:]
        if (args.photon_debug or args.photon_verbose) and name != "baseline":
            config_flags.append("-photon-debug")
        if args.photon_verbose and name != "baseline":
            config_flags.append("-photon-debug-verbose")

        config_flags += sampled_control_flags(args, config_flags)

        if "-sampled" not in config_flags or not sampled_param_grid:
            selected.append((name, config_flags))
            continue

        for warmup, granularity in sampled_param_grid:
            swept_flags = config_flags + [
                f"-sampled-warmup={warmup}",
                f"-sampled-granularity={granularity}",
            ]
            swept_name = f"{name}_w{warmup}_g{granularity}"
            selected.append((swept_name, swept_flags))

    return selected


def positive_or_zero(value, label):
    if value < 0:
        raise ValueError(f"{label} must be non-negative")
    return value


def sampled_control_flags(args, config_flags):
    flags = []

    sampled_threshold = positive_or_zero(
        args.sampled_threshold, "sampled-threshold")
    if sampled_threshold == 0 and args.balanced_sweep:
        sampled_threshold = BALANCED_SAMPLED_THRESHOLD
    if sampled_threshold > 0 and "-sampled" in config_flags:
        flags.append(f"-sampled-threshold={sampled_threshold}")

    branch_coverage = positive_or_zero(
        args.branch_sampled_coverage_threshold,
        "branch-sampled-coverage-threshold")
    if branch_coverage == 0 and args.balanced_sweep:
        branch_coverage = BALANCED_BRANCH_COVERAGE_THRESHOLD
    if branch_coverage > 0 and "-branch-sampled" in config_flags:
        flags.append(
            f"-branch-sampled-coverage-threshold={branch_coverage}")

    branch_threshold = positive_or_zero(
        args.branch_sampled_threshold, "branch-sampled-threshold")
    if branch_threshold == 0 and args.balanced_sweep:
        branch_threshold = BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD
    if branch_threshold > 0 and "-branch-sampled" in config_flags:
        flags.append(f"-branch-sampled-threshold={branch_threshold}")

    kernel_threshold = positive_or_zero(
        args.kernel_sampled_threshold, "kernel-sampled-threshold")
    if kernel_threshold > 0 and "-kernel-sampled" in config_flags:
        flags.append(f"-kernel-sampled-threshold={kernel_threshold}")

    kernel_distance = positive_or_zero(
        args.kernel_sampled_distance_threshold,
        "kernel-sampled-distance-threshold")
    if kernel_distance == 0 and args.balanced_sweep:
        kernel_distance = BALANCED_KERNEL_DISTANCE_THRESHOLD
    if kernel_distance > 0 and "-kernel-sampled" in config_flags:
        flags.append(f"-kernel-sampled-distance-threshold={kernel_distance}")

    return flags


def parse_csv(value):
    return [item.strip() for item in value.split(",") if item.strip()]


def parse_int_csv(value, label):
    values = []
    for item in parse_csv(value):
        parsed = int(item)
        if parsed <= 0:
            raise ValueError(f"{label} values must be positive: {item}")
        values.append(parsed)
    return values


def build_sampled_param_grid(args):
    if args.balanced_sweep:
        warmups = BALANCED_SAMPLED_SWEEP_WARMUPS[:]
        granularities = BALANCED_SAMPLED_SWEEP_GRANULARITIES[:]
    elif args.sampled_sweep:
        warmups = DEFAULT_SAMPLED_SWEEP_WARMUPS[:]
        granularities = DEFAULT_SAMPLED_SWEEP_GRANULARITIES[:]
    else:
        warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
        granularities = parse_int_csv(
            args.sampled_granularities, "sampled-granularities")

    if args.sampled_warmups:
        warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
    if args.sampled_granularities:
        granularities = parse_int_csv(
            args.sampled_granularities, "sampled-granularities")

    if not warmups and not granularities:
        return []
    if not warmups:
        warmups = [1024]
    if not granularities:
        granularities = [2048]

    return [
        (warmup, granularity)
        for warmup in warmups
        for granularity in granularities
    ]


def sampled_param_sweep_requested(args):
    return (
        args.sampled_sweep
        or args.balanced_sweep
        or bool(args.sampled_warmups)
        or bool(args.sampled_granularities)
    )


def selected_config_names(args):
    if args.configs:
        requested = parse_csv(args.configs)
    elif args.quick:
        requested = QUICK_CONFIGS[:]
    else:
        requested = ["all"]

    all_config_names = [name for name, _ in CONFIGS]
    if "all" in requested:
        return all_config_names

    unknown = sorted(set(requested) - set(all_config_names))
    if unknown:
        raise ValueError(f"unknown configs: {unknown}")

    return requested


def get_benchmarks_for_target(target):
    selected = BENCHMARKS_BY_TARGET.get(target, ["all"])
    if "all" in selected:
        return ALL_BENCHMARKS[:]

    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks for {target}: {unknown}")

    return selected


def get_selected_benchmarks(args, target):
    if args.benchmarks:
        selected = parse_csv(args.benchmarks)
    elif args.quick:
        selected = QUICK_BENCHMARKS[:]
    else:
        selected = get_benchmarks_for_target(target)

    if "all" in selected:
        return ALL_BENCHMARKS[:]

    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks: {unknown}")

    return selected


def make_exps(args, ablation_configs):
    exps = []
    for target in TARGETS:
        for benchmark in get_selected_benchmarks(args, target):
            for config_name, config_flags in ablation_configs:
                exps.append(
                    {
                        "target": target,
                        "benchmark": benchmark,
                        "config_name": config_name,
                        "flags": DEFAULT_BENCHMARK_FLAGS + config_flags,
                    }
                )
    return exps


def filter_missing_metric_exps(exps, results_dir):
    missing = []
    missing_dir = Path(results_dir)
    for exp in exps:
        stem = (
            f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}'
        )
        metrics_csv = missing_dir / f"{stem}_metrics.csv"
        if not metrics_csv.exists():
            missing.append(exp)

    return missing


def build_env():
    env = os.environ.copy()
    env.setdefault("GOCACHE", "/tmp/gocache")
    return env


def build_targets(exps):
    env = build_env()
    targets = sorted({exp["target"] for exp in exps})

    for target in targets:
        target_dir = os.path.join(ROOT_DIR, target)
        print(f"Building {target} in {target_dir}")
        process = subprocess.Popen(
            ["go", "build", "-buildvcs=false"],
            cwd=target_dir,
            env=env,
        )
        process.wait()
        if process.returncode != 0:
            raise RuntimeError(f"failed to build {target}")


def register_process(process):
    with running_processes_lock:
        running_processes.add(process)


def unregister_process(process):
    with running_processes_lock:
        running_processes.discard(process)


def terminate_process(process, grace_seconds=10):
    if process.poll() is not None:
        return

    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    try:
        process.wait(timeout=grace_seconds)
        return
    except subprocess.TimeoutExpired:
        pass

    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    process.wait()


def terminate_all_processes():
    with running_processes_lock:
        processes = list(running_processes)

    for process in processes:
        terminate_process(process)


def install_signal_handlers():
    def handle_signal(signum, _frame):
        print(
            f"Received signal {signum}; terminating running experiments.",
            flush=True,
        )
        terminate_all_processes()
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)


def exp_file_stem(exp):
    return os.path.join(
        output_dir,
        f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}',
    )


def run_exp(exp):
    binary = os.path.join(ROOT_DIR, exp["target"], exp["target"])
    file_stem = exp_file_stem(exp)
    metric_file_name = f"{file_stem}_metrics"

    cmd = [
        binary,
        f'-benchmark={exp["benchmark"]}',
        *exp["common_flags"],
        *exp["flags"],
        f"-metric-file-name={metric_file_name}",
    ]
    cmd_str = shlex.join(cmd)
    print(cmd_str)

    out_file_name = f"{file_stem}_out.stdout"
    with open(out_file_name, "w") as out_file:
        out_file.write(f"Executing {cmd_str}\n")
        start_time = datetime.now()
        out_file.write(f"Start time: {start_time}\n")
        out_file.flush()

        process = subprocess.Popen(
            cmd,
            stdout=out_file,
            stderr=out_file,
            cwd=ROOT_DIR,
            start_new_session=True,
        )
        register_process(process)
        timeout_seconds = exp.get("timeout_seconds", 0)
        timed_out = False
        try:
            try:
                process.wait(
                    timeout=timeout_seconds if timeout_seconds > 0 else None)
            except subprocess.TimeoutExpired:
                timed_out = True
                terminate_process(process)
        finally:
            unregister_process(process)

        end_time = datetime.now()
        out_file.write(f"Return code: {process.returncode}\n")
        if timed_out:
            out_file.write(
                f"Timed out after {timeout_seconds} seconds\n")
        out_file.write(f"End time: {end_time}\n")
        out_file.write(f"Elapsed time: {end_time - start_time}\n")

    if timed_out:
        print(f"Timed out executing {cmd_str}")
        return {"exp": exp, "returncode": -9, "timeout": timeout_seconds}

    if process.returncode != 0:
        print(f"Error executing {cmd_str}")
        return {"exp": exp, "returncode": process.returncode}

    metrics_csv = metric_file_name + ".csv"
    if not os.path.exists(metrics_csv):
        print(f"Missing metrics file for {cmd_str}: {metrics_csv}")
        return {"exp": exp, "returncode": -1, "missing_metrics": metrics_csv}

    print(f"Executed {cmd_str}, time {end_time - start_time}")
    return {"exp": exp, "returncode": 0}


def create_output_dir():
    global output_dir
    output_dir = os.path.join(
        ROOT_DIR,
        "results",
        datetime.now().strftime("%Y-%m-%d-%H-%M-%S-sampled-validation"),
    )

    results_dir = os.path.join(ROOT_DIR, "results")
    if not os.path.exists(results_dir):
        os.makedirs(results_dir)

    if not os.path.exists(output_dir):
        os.makedirs(output_dir)


def main():
    global output_dir

    install_signal_handlers()
    atexit.register(terminate_all_processes)

    args = parse_args()
    common_flags = build_common_flags(args)
    ablation_configs = build_ablation_configs(args)
    exps = make_exps(args, ablation_configs)
    if not exps:
        print("No experiments configured.")
        return

    if args.rerun_missing:
        output_dir = os.path.abspath(args.rerun_missing)
        if not os.path.isdir(output_dir):
            raise ValueError(f"results directory does not exist: {output_dir}")
        exps = filter_missing_metric_exps(exps, output_dir)
        if not exps:
            print(f"No missing-metrics experiments found in {output_dir}")
            return
        print(
            f"Rerunning {len(exps)} experiments with missing metrics in {output_dir}"
        )
    else:
        create_output_dir()

    if args.max_workers <= 0:
        raise ValueError("MAX_WORKERS must be greater than 0")

    max_workers = min(args.max_workers, len(exps))
    if sampled_param_sweep_requested(args):
        if args.sampled_parallel_limit > 0 and max_workers > args.sampled_parallel_limit:
            print(
                "Sampled parameter sweep parallelism capped at "
                f"max_workers={args.sampled_parallel_limit}. "
                "Use --sampled-parallel-limit=0 to disable this cap."
            )
            max_workers = args.sampled_parallel_limit

    print(f"Using common flags: {shlex.join(common_flags)}")
    if args.timeout_minutes > 0:
        print(f"Experiment timeout: {args.timeout_minutes} minutes")
    print(f"Launching {len(exps)} experiments with max_workers={max_workers}")
    for exp in exps:
        exp["common_flags"] = common_flags
        exp["timeout_seconds"] = int(args.timeout_minutes * 60)

    if args.dry_run:
        for exp in exps:
            binary = os.path.join(ROOT_DIR, exp["target"], exp["target"])
            metric_file_name = f"{exp_file_stem(exp)}_metrics"
            cmd = [
                binary,
                f'-benchmark={exp["benchmark"]}',
                *exp["common_flags"],
                *exp["flags"],
                f"-metric-file-name={metric_file_name}",
            ]
            print(shlex.join(cmd))
        return

    build_targets(exps)

    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
            futures = [executor.submit(run_exp, exp) for exp in exps]
            for future in concurrent.futures.as_completed(futures):
                print(future.result())
    finally:
        terminate_all_processes()


if __name__ == "__main__":
    main()
