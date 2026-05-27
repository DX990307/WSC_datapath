import argparse

from runall2_constants import (
    BENCHMARK_ALIASES,
    CONFIGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
    DEFAULT_SAMPLED_PARALLEL_LIMIT,
    DEFAULT_TIMEOUT_MINUTES,
    MAX_WORKERS,
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--rerun-missing",
        dest="rerun_missing",
        default="",
        help="Reuse a results directory and rerun missing metrics.",
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
        help="Fixed MMUTLB/IOTLB lookup latency, in cycles.",
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
