#!/usr/bin/env python3
"""Run Akkalat benchmark experiments.

This file is intentionally self-contained. It owns benchmark/config selection,
command construction, build, process management, dry-run, and rerun-missing
logic so the main experiment path does not depend on helper modules.
"""

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
TARGETS = ["baseline"]

MAX_WORKERS = 15
DEFAULT_MAX_WG = 78600
DEFAULT_MMUTLB_LOOKUP_LATENCY = 80
DEFAULT_TIMEOUT_MINUTES = 0

DEFAULT_RUN_BENCHMARKS = [
    "bert",
    "conv2d",
    "gpt",
    "maxpooling",
    "avgpooling",
    "fulllayer",
    "fulllayer-large",
    "fulllayer-gemm-tiny",
    "fulllayer-gemm-debug",
    "fulllayer-7bcompute",
    "fulllayer-1gb",
    "im2col",
    "kvcache",
    "kvcache-decode",
    "kvcache-decode-30b",
    "matrixmultiplication",
    "matrixmultiplication-middletile",
    "matrixtranspose",
    "matrixtranspose-middletile",
    "relu",
    "resnet",
]

TRADITIONAL_LITE_BENCHMARKS = [
    "conv2d",
    "maxpooling",
    "avgpooling",
    "im2col",
    "matrixmultiplication",
    "matrixmultiplication-middletile",
    "matrixtranspose",
    "matrixtranspose-middletile",
    "relu",
]

TRADITIONAL_BENCHMARKS = [
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fir",
    "fft",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixmultiplication-middletile",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
]

LLM_BENCHMARKS = [
    "bert",
    "gpt",
    "kvcache",
    "kvcache-decode",
    "kvcache-decode-30b",
    "resnet",
]

LLM_LIKE_BOTTLENECK_BENCHMARKS = [
    "matrixmultiplication-llm-prefill-attn",
    "matrixmultiplication-llm-decode-attn",
    "matrixmultiplication-llm-prefill-mlp-up",
    "matrixmultiplication-llm-decode-mlp-up",
    "matrixmultiplication-llm-prefill-mlp-down",
    "matrixmultiplication-llm-decode-mlp-down",
    "conv2d-llm-prefill-pointwise",
    "conv2d-llm-decode-pointwise",
    "conv2d-llm-prefill-local",
    "conv2d-llm-decode-local",
]

EXPERIMENTAL_BENCHMARKS = [
    "fulllayer-large",
    "fulllayer-gemm-tiny",
    "fulllayer-gemm-debug",
    "fulllayer-7bcompute",
    "fulllayer-1gb",
    "llmop",
]

ALL_BENCHMARKS = list(dict.fromkeys(
    TRADITIONAL_BENCHMARKS
    + LLM_BENCHMARKS
    + LLM_LIKE_BOTTLENECK_BENCHMARKS
    + EXPERIMENTAL_BENCHMARKS
))

BENCHMARK_ALIASES = {
    "all": ALL_BENCHMARKS,
    "default": DEFAULT_RUN_BENCHMARKS,
    "experimental": EXPERIMENTAL_BENCHMARKS,
    "llm-like-bottleneck": LLM_LIKE_BOTTLENECK_BENCHMARKS,
    "traditional": TRADITIONAL_BENCHMARKS,
    "traditional-lite": TRADITIONAL_LITE_BENCHMARKS,
    "llm": LLM_BENCHMARKS,
}

BENCHMARKS_BY_TARGET = {"baseline": ["default"]}
DEFAULT_BENCHMARK_FLAGS = []

BASE_COMMON_FLAGS = [
    "-timing",
    "-num-memory-banks=16",
    "-bandwidth=48",
    "-switch-latency=1",
    "-magic-memory-copy",
    "-report-all",
]

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

CONFIGS = [
    ("baseline", []),
    ("all_local", ["-force-local-data-access"]),
    ("sample_all", ["-sampled", "-branch-sampled", "-kernel-sampled"]),
    (
        "sample_all_loop",
        ["-sampled", "-branch-sampled", "-kernel-sampled", "-loop-sampled"],
    ),
    ("sample_wf", ["-sampled"]),
    ("sample_branch", ["-branch-sampled"]),
    ("sample_kernel", ["-kernel-sampled"]),
    ("sample_loop", ["-loop-sampled"]),
]

QUICK_BENCHMARKS = ["relu"]
QUICK_CONFIGS = [
    "baseline",
    "sample_all",
    "sample_all_loop",
    "sample_wf",
    "sample_branch",
    "sample_kernel",
    "sample_loop",
]

output_dir = ""
running_processes = set()
running_processes_lock = threading.Lock()


def parse_args():
    parser = argparse.ArgumentParser(
        description="Run baseline benchmark experiments."
    )
    parser.add_argument(
        "--rerun-missing",
        default="",
        help="Reuse a results directory and rerun missing metrics.",
    )
    parser.add_argument(
        "--output-dir",
        default="",
        help="Write outputs to this directory instead of a timestamped one.",
    )
    parser.add_argument(
        "--max-workers",
        type=int,
        default=MAX_WORKERS,
        help="Maximum number of concurrent experiments.",
    )
    parser.add_argument(
        "--skip-build",
        action="store_true",
        help="Assume target binaries are already built.",
    )
    parser.add_argument(
        "--mmutlb-lookup-latency",
        type=int,
        default=DEFAULT_MMUTLB_LOOKUP_LATENCY,
        help="Fixed MMUTLB/IOTLB lookup latency, in cycles.",
    )
    parser.add_argument(
        "--switch-latency",
        type=int,
        default=0,
        help="Override -switch-latency. 0 keeps the default.",
    )
    parser.add_argument(
        "--benchmarks",
        default="",
        help=(
            "Comma-separated benchmark list. Presets: "
            + ",".join(sorted(BENCHMARK_ALIASES))
            + "."
        ),
    )
    parser.add_argument(
        "--configs",
        default="",
        help=(
            "Comma-separated config list. Choices: "
            + ",".join(name for name, _ in CONFIGS)
            + ". Use all for every config."
        ),
    )
    parser.add_argument(
        "--extra-benchmark-flags",
        default="",
        help="Additional flags appended to each benchmark binary command.",
    )
    parser.add_argument("--quick", action="store_true")
    parser.add_argument(
        "--max-wg",
        type=int,
        default=DEFAULT_MAX_WG,
        help=f"Pass -max-wg to each benchmark. Defaults to {DEFAULT_MAX_WG}; 0 disables.",
    )
    parser.add_argument(
        "--timeout-minutes",
        type=float,
        default=DEFAULT_TIMEOUT_MINUTES,
        help="Kill an experiment after this many minutes. 0 disables timeout.",
    )
    parser.add_argument("--photon-debug", action="store_true")
    parser.add_argument("--photon-verbose", action="store_true")
    parser.add_argument("--disable-servers", action="store_true")
    parser.add_argument("--report-l2-source", action="store_true")
    parser.add_argument("--l2-source-tile-width", type=int, default=7)
    parser.add_argument("--force-local-data-access", action="store_true")
    parser.add_argument("--l2-dir-batch-window", type=int, default=0)
    parser.add_argument("--l2-dram-access-unit-coalesce", action="store_true")
    add_legacy_compat_args(parser)
    add_sampled_args(parser)
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print commands without building or running them.",
    )
    return parser.parse_args()


def add_legacy_compat_args(parser):
    hidden = argparse.SUPPRESS
    parser.add_argument("--l1v-remote-max-inflight", type=int, default=0, help=hidden)
    parser.add_argument("--l1v-mshr-entries", type=int, default=0, help=hidden)
    parser.add_argument("--l1v-max-concurrent-trans", type=int, default=0, help=hidden)
    parser.add_argument("--l1v-bottom-reorder-policy", default="", help=hidden)
    parser.add_argument("--l1v-bottom-reorder-window", type=int, default=0, help=hidden)
    parser.add_argument("--l1v-bottom-reorder-max-age-ns", type=int, default=-1, help=hidden)
    parser.add_argument("--trace-sharing", action="store_true", help=hidden)
    parser.add_argument("--trace-sharing-sample", type=int, default=1, help=hidden)
    parser.add_argument("--trace-sharing-max-records", type=int, default=1000000, help=hidden)
    parser.add_argument("--trace-memory-path", action="store_true", help=hidden)
    parser.add_argument(
        "--trace-memory-path-warmup-accesses",
        type=int,
        default=100000,
        help=hidden,
    )
    parser.add_argument(
        "--trace-memory-path-max-records",
        type=int,
        default=100000,
        help=hidden,
    )
    parser.add_argument(
        "--trace-memory-path-exit-on-complete",
        action="store_true",
        help=hidden,
    )


def add_sampled_args(parser):
    parser.add_argument("--sampled-sweep", action="store_true")
    parser.add_argument("--balanced-sweep", action="store_true")
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
    parser.add_argument("--sampled-threshold", type=float, default=0)
    parser.add_argument("--branch-sampled-coverage-threshold", type=float, default=0)
    parser.add_argument("--branch-sampled-threshold", type=float, default=0)
    parser.add_argument("--kernel-sampled-threshold", type=int, default=0)
    parser.add_argument("--kernel-sampled-distance-threshold", type=int, default=0)
    parser.add_argument("--loop-sampled-warmup", type=int, default=0)
    parser.add_argument("--loop-sampled-min-iters", type=int, default=0)
    parser.add_argument("--loop-sampled-threshold", type=float, default=0)
    parser.add_argument(
        "--allow-sampled-parallel",
        action="store_true",
        help="Kept for compatibility; sweeps already use max workers.",
    )
    parser.add_argument(
        "--sampled-parallel-limit",
        type=int,
        default=DEFAULT_SAMPLED_PARALLEL_LIMIT,
        help="Maximum max_workers allowed for sampled sweeps. 0 disables cap.",
    )


def build_common_flags(args):
    common_flags = BASE_COMMON_FLAGS[:]
    if args.switch_latency > 0:
        common_flags = replace_or_append_flag(
            common_flags,
            "-switch-latency=",
            f"-switch-latency={args.switch_latency}",
        )
    if args.disable_servers:
        common_flags.append("-disable-servers")
    if args.report_l2_source:
        common_flags.append("-report-l2-source")
        common_flags.append(f"-l2-source-tile-width={args.l2_source_tile_width}")
    common_flags.append(f"-mmutlb-lookup-latency={args.mmutlb_lookup_latency}")
    if args.l1v_remote_max_inflight > 0:
        common_flags.append(f"-l1v-remote-max-inflight={args.l1v_remote_max_inflight}")
    if args.l1v_mshr_entries > 0:
        common_flags.append(f"-l1v-mshr-entries={args.l1v_mshr_entries}")
    if args.l1v_max_concurrent_trans > 0:
        common_flags.append(
            f"-l1v-max-concurrent-trans={args.l1v_max_concurrent_trans}"
        )
    if args.l1v_bottom_reorder_policy:
        common_flags.append(
            f"-l1v-bottom-reorder-policy={args.l1v_bottom_reorder_policy}"
        )
    if args.l1v_bottom_reorder_window > 0:
        common_flags.append(
            f"-l1v-bottom-reorder-window={args.l1v_bottom_reorder_window}"
        )
    if args.l1v_bottom_reorder_max_age_ns >= 0:
        common_flags.append(
            f"-l1v-bottom-reorder-max-age-ns={args.l1v_bottom_reorder_max_age_ns}"
        )
    if args.l2_dir_batch_window > 0:
        common_flags.append(f"-l2-dir-batch-window={args.l2_dir_batch_window}")
    if args.l2_dram_access_unit_coalesce:
        common_flags.append("-l2-dram-access-unit-coalesce")
    if args.force_local_data_access:
        common_flags.append("-force-local-data-access")
    if args.max_wg > 0:
        common_flags.append(f"-max-wg={args.max_wg}")
    return common_flags


def replace_or_append_flag(flags, prefix, value):
    updated = []
    replaced = False
    for flag in flags:
        if flag.startswith(prefix):
            updated.append(value)
            replaced = True
        else:
            updated.append(flag)
    if not replaced:
        updated.append(value)
    return updated


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
            selected.append((f"{name}_w{warmup}_g{granularity}", swept_flags))
    return selected


def sampled_control_flags(args, config_flags):
    flags = []
    add_sampled_thresholds(args, config_flags, flags)
    add_branch_thresholds(args, config_flags, flags)
    add_kernel_thresholds(args, config_flags, flags)
    add_loop_thresholds(args, config_flags, flags)
    return flags


def add_sampled_thresholds(args, config_flags, flags):
    threshold = positive_or_zero(args.sampled_threshold, "sampled-threshold")
    if threshold == 0 and args.balanced_sweep:
        threshold = BALANCED_SAMPLED_THRESHOLD
    if threshold > 0 and "-sampled" in config_flags:
        flags.append(f"-sampled-threshold={threshold}")


def add_branch_thresholds(args, config_flags, flags):
    coverage = positive_or_zero(
        args.branch_sampled_coverage_threshold,
        "branch-sampled-coverage-threshold",
    )
    if coverage == 0 and args.balanced_sweep:
        coverage = BALANCED_BRANCH_COVERAGE_THRESHOLD
    if coverage > 0 and "-branch-sampled" in config_flags:
        flags.append(f"-branch-sampled-coverage-threshold={coverage}")

    threshold = positive_or_zero(
        args.branch_sampled_threshold,
        "branch-sampled-threshold",
    )
    if threshold == 0 and args.balanced_sweep:
        threshold = BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD
    if threshold > 0 and "-branch-sampled" in config_flags:
        flags.append(f"-branch-sampled-threshold={threshold}")


def add_kernel_thresholds(args, config_flags, flags):
    threshold = positive_or_zero(
        args.kernel_sampled_threshold,
        "kernel-sampled-threshold",
    )
    if threshold > 0 and "-kernel-sampled" in config_flags:
        flags.append(f"-kernel-sampled-threshold={threshold}")

    distance = positive_or_zero(
        args.kernel_sampled_distance_threshold,
        "kernel-sampled-distance-threshold",
    )
    if distance == 0 and args.balanced_sweep:
        distance = BALANCED_KERNEL_DISTANCE_THRESHOLD
    if distance > 0 and "-kernel-sampled" in config_flags:
        flags.append(f"-kernel-sampled-distance-threshold={distance}")


def add_loop_thresholds(args, config_flags, flags):
    warmup = positive_or_zero(args.loop_sampled_warmup, "loop-sampled-warmup")
    if warmup > 0 and "-loop-sampled" in config_flags:
        flags.append(f"-loop-sampled-warmup={warmup}")

    min_iters = positive_or_zero(args.loop_sampled_min_iters, "loop-sampled-min-iters")
    if min_iters > 0 and "-loop-sampled" in config_flags:
        flags.append(f"-loop-sampled-min-iters={min_iters}")

    threshold = positive_or_zero(args.loop_sampled_threshold, "loop-sampled-threshold")
    if threshold > 0 and "-loop-sampled" in config_flags:
        flags.append(f"-loop-sampled-threshold={threshold}")


def positive_or_zero(value, label):
    if value < 0:
        raise ValueError(f"{label} must be non-negative")
    return value


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


def unique_preserving_order(items):
    seen = set()
    unique = []
    for item in items:
        if item in seen:
            continue
        seen.add(item)
        unique.append(item)
    return unique


def expand_benchmark_selection(selected):
    expanded = []
    for item in selected:
        if item in BENCHMARK_ALIASES:
            expanded += BENCHMARK_ALIASES[item]
        else:
            expanded.append(item)
    return unique_preserving_order(expanded)


def build_sampled_param_grid(args):
    if args.balanced_sweep:
        warmups = BALANCED_SAMPLED_SWEEP_WARMUPS[:]
        granularities = BALANCED_SAMPLED_SWEEP_GRANULARITIES[:]
    elif args.sampled_sweep:
        warmups = DEFAULT_SAMPLED_SWEEP_WARMUPS[:]
        granularities = DEFAULT_SAMPLED_SWEEP_GRANULARITIES[:]
    else:
        warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
        granularities = parse_int_csv(args.sampled_granularities, "sampled-granularities")

    if args.sampled_warmups:
        warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
    if args.sampled_granularities:
        granularities = parse_int_csv(args.sampled_granularities, "sampled-granularities")

    if not warmups and not granularities:
        return []
    if not warmups:
        warmups = [1024]
    if not granularities:
        granularities = [2048]
    return [(warmup, granularity) for warmup in warmups for granularity in granularities]


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
    selected = expand_benchmark_selection(selected)
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

    selected = expand_benchmark_selection(selected)
    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks: {unknown}")
    return selected


def make_exps(args, ablation_configs):
    exps = []
    extra_flags = shlex.split(args.extra_benchmark_flags)
    for target in TARGETS:
        for benchmark in get_selected_benchmarks(args, target):
            for config_name, config_flags in ablation_configs:
                exps.append({
                    "target": target,
                    "benchmark": benchmark,
                    "config_name": config_name,
                    "flags": DEFAULT_BENCHMARK_FLAGS + extra_flags + config_flags,
                })
    return exps


def filter_missing_metric_exps(exps, results_dir):
    missing = []
    missing_dir = Path(results_dir)
    for exp in exps:
        stem = f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}'
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
        print(f"Received signal {signum}; terminating running experiments.", flush=True)
        terminate_all_processes()
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)


def set_output_dir(path):
    global output_dir
    output_dir = path


def create_output_dir():
    global output_dir
    output_dir = os.path.join(
        ROOT_DIR,
        "results",
        datetime.now().strftime("%Y-%m-%d-%H-%M-%S-sampled-validation"),
    )
    os.makedirs(os.path.join(ROOT_DIR, "results"), exist_ok=True)
    os.makedirs(output_dir, exist_ok=True)


def exp_file_stem(exp):
    return os.path.join(
        output_dir,
        f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}',
    )


def experiment_command(exp):
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
    if exp.get("trace_sharing"):
        cmd.extend([
            "-trace-sharing",
            f"-trace-sharing-file={file_stem}_sharing.csv.gz",
            f'-trace-sharing-sample={exp["trace_sharing_sample"]}',
            f'-trace-sharing-max-records={exp["trace_sharing_max_records"]}',
        ])
    if exp.get("trace_memory_path"):
        cmd.extend([
            "-trace-memory-path",
            f"-trace-memory-path-file={file_stem}_memory_path",
            (
                "-trace-memory-path-warmup-accesses="
                f'{exp["trace_memory_path_warmup_accesses"]}'
            ),
            (
                "-trace-memory-path-max-records="
                f'{exp["trace_memory_path_max_records"]}'
            ),
        ])
        if exp.get("trace_memory_path_exit_on_complete"):
            cmd.append("-trace-memory-path-exit-on-complete")
    return cmd


def run_exp(exp):
    cmd = experiment_command(exp)
    cmd_str = shlex.join(cmd)
    print(cmd_str)

    file_stem = exp_file_stem(exp)
    metric_file_name = f"{file_stem}_metrics"
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
        timed_out = wait_for_experiment(exp, process)

        end_time = datetime.now()
        out_file.write(f"Return code: {process.returncode}\n")
        if timed_out:
            out_file.write(f'Timed out after {exp.get("timeout_seconds", 0)} seconds\n')
        out_file.write(f"End time: {end_time}\n")
        out_file.write(f"Elapsed time: {end_time - start_time}\n")

    return exp_result(
        exp,
        cmd_str,
        metric_file_name,
        end_time - start_time,
        timed_out,
        process.returncode,
    )


def wait_for_experiment(exp, process):
    register_process(process)
    timeout_seconds = exp.get("timeout_seconds", 0)
    timed_out = False
    try:
        try:
            process.wait(timeout=timeout_seconds if timeout_seconds > 0 else None)
        except subprocess.TimeoutExpired:
            timed_out = True
            terminate_process(process)
    finally:
        unregister_process(process)
    return timed_out


def exp_result(exp, cmd_str, metric_file_name, elapsed_time, timed_out, returncode):
    if timed_out:
        print(f"Timed out executing {cmd_str}")
        return {
            "exp": exp,
            "returncode": -9,
            "timeout": exp.get("timeout_seconds", 0),
        }

    if returncode != 0:
        print(f"Error executing {cmd_str}")
        return {"exp": exp, "returncode": returncode}

    metrics_csv = metric_file_name + ".csv"
    if not os.path.exists(metrics_csv):
        print(f"Missing metrics file for {cmd_str}: {metrics_csv}")
        return {"exp": exp, "returncode": -1, "missing_metrics": metrics_csv}

    print(f"Executed {cmd_str}, time {elapsed_time}")
    return {"exp": exp, "returncode": 0}


def dry_run_commands(exps):
    for exp in exps:
        print(shlex.join(experiment_command(exp)))


def prepare_output_dir(args):
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


def choose_max_workers(args, exp_count):
    if args.max_workers <= 0:
        raise ValueError("MAX_WORKERS must be greater than 0")

    max_workers = min(args.max_workers, exp_count)
    if sampled_param_sweep_requested(args):
        cap = args.sampled_parallel_limit
        if cap > 0 and max_workers > cap:
            print(
                "Sampled parameter sweep parallelism capped at "
                f"max_workers={cap}. Use --sampled-parallel-limit=0 "
                "to disable this cap."
            )
            max_workers = cap
    return max_workers


def prepare_exps(args, exps, common_flags):
    timeout_seconds = int(args.timeout_minutes * 60)
    for exp in exps:
        exp["common_flags"] = common_flags
        exp["timeout_seconds"] = timeout_seconds
        exp["trace_sharing"] = args.trace_sharing
        exp["trace_sharing_sample"] = args.trace_sharing_sample
        exp["trace_sharing_max_records"] = args.trace_sharing_max_records
        exp["trace_memory_path"] = args.trace_memory_path
        exp["trace_memory_path_warmup_accesses"] = args.trace_memory_path_warmup_accesses
        exp["trace_memory_path_max_records"] = args.trace_memory_path_max_records
        exp["trace_memory_path_exit_on_complete"] = args.trace_memory_path_exit_on_complete


def print_launch_summary(args, common_flags, exps, max_workers):
    print(f"Using common flags: {shlex.join(common_flags)}")
    if args.timeout_minutes > 0:
        print(f"Experiment timeout: {args.timeout_minutes} minutes")
    print(f"Launching {len(exps)} experiments with max_workers={max_workers}")


def run_all_experiments(exps, max_workers):
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
            futures = [executor.submit(run_exp, exp) for exp in exps]
            for future in concurrent.futures.as_completed(futures):
                print(future.result())
    finally:
        terminate_all_processes()


def main():
    install_signal_handlers()
    atexit.register(terminate_all_processes)

    args = parse_args()
    common_flags = build_common_flags(args)
    ablation_configs = build_ablation_configs(args)
    exps = make_exps(args, ablation_configs)
    if not exps:
        print("No experiments configured.")
        return

    prepare_output_dir(args)
    exps = maybe_filter_missing(args, exps)
    if not exps:
        return

    max_workers = choose_max_workers(args, len(exps))
    prepare_exps(args, exps, common_flags)
    print_launch_summary(args, common_flags, exps, max_workers)

    if args.dry_run:
        dry_run_commands(exps)
        return

    if args.skip_build:
        print("Skipping target build (--skip-build).")
    else:
        build_targets(exps)
    run_all_experiments(exps, max_workers)


if __name__ == "__main__":
    main()
