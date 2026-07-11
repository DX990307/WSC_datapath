import argparse

from runall2_constants import (
    BENCHMARK_ALIASES,
    CONFIGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
    DEFAULT_SAMPLED_PARALLEL_LIMIT,
    DEFAULT_TIMEOUT_MINUTES,
    MAX_WORKERS,
)

DEFAULT_MAX_WG = 78600


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--rerun-missing",
        dest="rerun_missing",
        default="",
        help="Reuse a results directory and rerun missing metrics.",
    )
    parser.add_argument(
        "--output-dir",
        dest="output_dir",
        default="",
        help="Write new experiment outputs to this directory instead of a timestamped results dir.",
    )
    parser.add_argument(
        "--max-workers",
        dest="max_workers",
        type=int,
        default=MAX_WORKERS,
        help="Maximum number of concurrent experiments to launch.",
    )
    parser.add_argument(
        "--skip-build",
        action="store_true",
        help="Assume target binaries are already built.",
    )
    parser.add_argument(
        "--mmutlb-lookup-latency",
        dest="mmutlb_lookup_latency",
        type=int,
        default=DEFAULT_MMUTLB_LOOKUP_LATENCY,
        help="Fixed MMUTLB/IOTLB lookup latency, in cycles.",
    )
    parser.add_argument(
        "--switch-latency",
        dest="switch_latency",
        type=int,
        default=32,
        help=(
            "Override -switch-latency in the benchmark command. "
            "0 keeps runall2_constants.py default."
        ),
    )
    parser.add_argument(
        "--benchmarks",
        dest="benchmarks",
        default="",
        help=(
            "Comma-separated benchmark list. Presets: "
            + ",".join(sorted(BENCHMARK_ALIASES))
            + ". traditional/traditional-lite exclude LLM workloads."
        ),
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
        "--remote-ablation",
        action="store_true",
        help=(
            "Run baseline, each of DRAM batching / remote request / remote "
            "L2 alone, and one all-three combined configuration. "
            "Cannot be combined with --configs."
        ),
    )
    parser.add_argument(
        "--remote-ablation-include-prefetch",
        action="store_true",
        help=(
            "Append a sixth all-three configuration with AU prefetch to the "
            "remote ablation; implies --remote-ablation."
        ),
    )
    parser.add_argument(
        "--dram-batch-entries",
        type=int,
        default=16,
        help="Maximum active 128B DRAM batch windows per L2 slice (default: 16).",
    )
    parser.add_argument(
        "--dram-batch-lines",
        type=int,
        default=2,
        help="Maximum adjacent 64B lines per DRAM batch (default: 2).",
    )
    parser.add_argument(
        "--dram-batch-wait-ns",
        type=int,
        default=0,
        help="Deprecated compatibility option; adaptive DRAM prefetch does not wait.",
    )
    parser.add_argument(
        "--dram-row-reorder-max-age",
        type=int,
        default=64,
        help="Oldest-ready threshold for local DRAM row reorder (default: 64 cycles).",
    )
    parser.add_argument(
        "--remote-data-path-batch-lines",
        type=int,
        default=8,
        help="Maximum bitmap batch lines for --remote-ablation (default: 8).",
    )
    parser.add_argument(
        "--remote-data-path-wait-ns",
        type=int,
        default=0,
        help=(
            "Deprecated compatibility option; remote batching is "
            "work-conserving and does not wait."
        ),
    )
    parser.add_argument(
        "--remote-data-path-batches",
        type=int,
        default=64,
        help="Maximum collecting page batches for --remote-ablation (default: 64).",
    )
    parser.add_argument(
        "--remote-data-path-reuse-entries",
        type=int,
        default=4096,
        help="Two-touch history entries for --remote-ablation (default: 4096).",
    )
    parser.add_argument(
        "--extra-benchmark-flags",
        dest="extra_benchmark_flags",
        default="",
        help="Additional flags appended to each benchmark binary command.",
    )
    parser.add_argument(
        "--quick",
        action="store_true",
        help="Run a small Photon validation set.",
    )
    parser.add_argument(
        "--max-wg",
        dest="max_wg",
        type=int,
        default=DEFAULT_MAX_WG,
        help=(
            "Pass -max-wg to each benchmark. "
            f"Defaults to {DEFAULT_MAX_WG}; use 0 for no limit."
        ),
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
        "--report-l2-source",
        action="store_true",
        help="Pass -report-l2-source to each benchmark.",
    )
    parser.add_argument(
        "--l2-source-tile-width",
        type=int,
        default=7,
        help="Tile-array width used to compute Manhattan hops.",
    )
    parser.add_argument(
        "--trace-sharing",
        dest="trace_sharing",
        action="store_true",
        help="Emit a compact gzip page-sharing trace for each experiment.",
    )
    parser.add_argument(
        "--trace-sharing-sample",
        dest="trace_sharing_sample",
        type=int,
        default=1,
        help="Record one translated data access every N accesses when --trace-sharing is enabled.",
    )
    parser.add_argument(
        "--trace-sharing-max-records",
        dest="trace_sharing_max_records",
        type=int,
        default=1000000,
        help="Maximum records per page-sharing trace; 0 means unlimited.",
    )
    parser.add_argument(
        "--trace-memory-path",
        dest="trace_memory_path",
        action="store_true",
        help="Emit request-level memory-path trace and joint TLB/cache miss summaries.",
    )
    parser.add_argument(
        "--trace-memory-path-warmup-accesses",
        dest="trace_memory_path_warmup_accesses",
        type=int,
        default=100000,
        help="Observed L1V memory accesses to skip before writing raw memory-path rows.",
    )
    parser.add_argument(
        "--trace-memory-path-max-records",
        dest="trace_memory_path_max_records",
        type=int,
        default=100000,
        help="Maximum stable-window memory-path raw rows per experiment; 0 means unlimited.",
    )
    parser.add_argument(
        "--trace-memory-path-exit-on-complete",
        dest="trace_memory_path_exit_on_complete",
        action="store_true",
        help="Stop each benchmark process after the memory-path raw window reaches max records.",
    )
    parser.add_argument(
        "--l1v-remote-max-inflight",
        dest="l1v_remote_max_inflight",
        type=int,
        default=0,
        help=(
            "Limit in-flight remote L1V bottom transactions per L1V cache. "
            "0 disables remote-only throttling."
        ),
    )
    parser.add_argument(
        "--l1v-mshr-entries",
        dest="l1v_mshr_entries",
        type=int,
        default=0,
        help=(
            "Pass -l1v-mshr-entries to each benchmark. "
            "0 keeps the benchmark binary default."
        ),
    )
    parser.add_argument(
        "--l1v-max-concurrent-trans",
        dest="l1v_max_concurrent_trans",
        type=int,
        default=0,
        help=(
            "Pass -l1v-max-concurrent-trans to each benchmark. "
            "0 keeps the benchmark binary default."
        ),
    )
    parser.add_argument(
        "--l1v-bottom-reorder-policy",
        dest="l1v_bottom_reorder_policy",
        default="",
        help=(
            "Pass -l1v-bottom-reorder-policy to each benchmark. "
            "Choices in the simulator are none, fifo, and hlq."
        ),
    )
    parser.add_argument(
        "--l1v-bottom-reorder-window",
        dest="l1v_bottom_reorder_window",
        type=int,
        default=0,
        help=(
            "Pass -l1v-bottom-reorder-window to each benchmark. "
            "0 keeps the benchmark binary default."
        ),
    )
    parser.add_argument(
        "--l1v-bottom-reorder-max-age-ns",
        dest="l1v_bottom_reorder_max_age_ns",
        type=int,
        default=-1,
        help=(
            "Pass -l1v-bottom-reorder-max-age-ns to each benchmark. "
            "-1 keeps the benchmark binary default; 0 means unlimited."
        ),
    )
    parser.add_argument(
        "--force-local-data-access",
        dest="force_local_data_access",
        action="store_true",
        help=(
            "Pass -force-local-data-access to each benchmark, forcing L1V "
            "data-cache misses to use the requester's local L2/DRAM path."
        ),
    )
    add_sampled_args(parser)
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print commands without building or running them.",
    )
    return parser.parse_args()


def add_sampled_args(parser):
    parser.add_argument(
        "--sampled-sweep",
        action="store_true",
        help="Scan common WF sampled warmup/granularity pairs.",
    )
    parser.add_argument(
        "--balanced-sweep",
        action="store_true",
        help="Use a smaller accuracy/speed tradeoff sweep.",
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
        help="Pass -sampled-threshold to sampled configs.",
    )
    parser.add_argument(
        "--branch-sampled-coverage-threshold",
        type=float,
        default=0,
        help="Pass -branch-sampled-coverage-threshold.",
    )
    parser.add_argument(
        "--branch-sampled-threshold",
        type=float,
        default=0,
        help="Pass -branch-sampled-threshold.",
    )
    parser.add_argument(
        "--kernel-sampled-threshold",
        type=int,
        default=0,
        help="Pass -kernel-sampled-threshold.",
    )
    parser.add_argument(
        "--kernel-sampled-distance-threshold",
        type=int,
        default=0,
        help="Pass -kernel-sampled-distance-threshold.",
    )
    parser.add_argument(
        "--loop-sampled-warmup",
        type=int,
        default=0,
        help="Pass -loop-sampled-warmup.",
    )
    parser.add_argument(
        "--loop-sampled-min-iters",
        type=int,
        default=0,
        help="Pass -loop-sampled-min-iters.",
    )
    parser.add_argument(
        "--loop-sampled-threshold",
        type=float,
        default=0,
        help="Pass -loop-sampled-threshold.",
    )
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
