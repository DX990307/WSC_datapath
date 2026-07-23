import argparse

from runall2_constants import (
    BENCHMARK_ALIASES,
    CONFIGS,
    DEFAULT_L1V_MAX_CONCURRENT_TRANS,
    DEFAULT_L1V_MSHR_ENTRIES,
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
        "--reuse-baseline-dir",
        dest="reuse_baseline_dir",
        default="",
        help=(
            "Use the validated Baseline metrics in this standalone library "
            "and remove Baseline cells from a new ablation campaign."
        ),
    )
    parser.add_argument(
        "--max-workers",
        dest="max_workers",
        type=int,
        default=MAX_WORKERS,
        help="Maximum number of concurrent experiments to launch.",
    )
    parser.add_argument(
        "--memory-reserve-gib",
        dest="memory_reserve_gib",
        type=float,
        default=0.0,
        help=(
            "Launch new experiments only while MemAvailable is above this "
            "threshold. 0 disables dynamic memory admission."
        ),
    )
    parser.add_argument(
        "--initial-workers",
        dest="initial_workers",
        type=int,
        default=1,
        help=(
            "Initial concurrent experiments when dynamic memory admission "
            "is enabled. Concurrency can grow to --max-workers."
        ),
    )
    parser.add_argument(
        "--memory-scan-minutes",
        dest="memory_scan_minutes",
        type=float,
        default=30.0,
        help=(
            "Minutes between memory scans; each successful scan admits one "
            "additional concurrent experiment."
        ),
    )
    parser.add_argument(
        "--memory-per-worker-gib",
        dest="memory_per_worker_gib",
        type=float,
        default=0.0,
        help=(
            "Deprecated compatibility option; dynamic admission no longer "
            "assumes a fixed amount of memory per worker."
        ),
    )
    parser.add_argument(
        "--skip-build",
        action="store_true",
        help="Assume target binaries are already built.",
    )
    parser.add_argument(
        "--binary-path",
        dest="binary_path",
        default="",
        help=(
            "Run the single configured target from this frozen executable "
            "instead of target/target. Requires --skip-build; the selected "
            "binary is still SHA-256-checked by the experiment manifest."
        ),
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
            + ". The traditional and traditional-primary paper presets "
            "both contain the same 14 workloads, including SPMV."
        ),
    )
    parser.add_argument(
        "--configs",
        dest="configs",
        default="",
        help=(
            "Comma-separated config list. Choices: "
            + ",".join(name for name, _ in CONFIGS)
            + ". With --remote-ablation, choices are "
            "baseline,m1,m2,m3,complete; explicit diagnostics are "
            "old_m1_independent_prefetch,cuckoo_filter_only,m1_bypass_fill_only,"
            "m1_bypass_fill_predictor_only,always_pair,"
            "predictor_only,paired_read_without_filter,m1_without_cuckoo,"
            "new_m1. "
            "Use all for the five formal configs."
        ),
    )
    parser.add_argument(
        "--remote-ablation",
        action="store_true",
        help=(
            "Run the formal CuPath paper ablation: Baseline, M1 filter-guided "
            "paired-read aggregation, M2 remote aggregation with candidate "
            "piggybacking, M3 requester-L2 reuse, and Complete. M1 uses the "
            "historical aligned 128B HBM access. "
            "--configs may select a subset without changing any selected "
            "cell's flags."
        ),
    )
    parser.add_argument(
        "--remote-data-path-batch-lines",
        type=int,
        default=8,
        help="Maximum bitmap batch lines for --remote-ablation (default: 8).",
    )
    parser.add_argument(
        "--remote-data-path-batches",
        type=int,
        default=64,
        help="Maximum collecting page batches for --remote-ablation (default: 64).",
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
        "--trace-remote-origin",
        dest="trace_remote_origin",
        action="store_true",
        help=(
            "Emit the diagnostic WG/object local-vs-remote request audit; "
            "this does not change scheduling or mechanisms."
        ),
    )
    parser.add_argument(
        "--trace-remote-origin-max-records",
        dest="trace_remote_origin_max_records",
        type=int,
        default=100000,
        help="Maximum raw rows per remote-origin audit; aggregates are complete.",
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
        "--allocation-profile",
        dest="allocation_profile",
        action="store_true",
        help=(
            "Record workload allocation pages and exit immediately before "
            "the first kernel launch; runs baseline only."
        ),
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
        "--trace-observation",
        dest="trace_observation",
        action="store_true",
        help=(
            "Emit the new exclusive memory-path and physical-DRAM traces. "
            "In a multi-config ablation launch, only the mechanisms-off "
            "baseline experiment is traced."
        ),
    )
    parser.add_argument(
        "--trace-observation-warmup-accesses",
        dest="trace_observation_warmup_accesses",
        type=int,
        default=100000,
        help="Post-coalescing L1 demand reads to skip before the O1/O2 window.",
    )
    parser.add_argument(
        "--trace-observation-max-records",
        dest="trace_observation_max_records",
        type=int,
        default=100000,
        help="Maximum O1/O2 demand-read paths per experiment; 0 means unlimited.",
    )
    parser.add_argument(
        "--trace-observation-exit-on-complete",
        dest="trace_observation_exit_on_complete",
        action="store_true",
        help="Stop after all selected O1/O2 paths complete.",
    )
    parser.add_argument(
        "--trace-observation-dram-warmup-accesses",
        dest="trace_observation_dram_warmup_accesses",
        type=int,
        default=100000,
        help="Physical DRAM reads to skip before the O3 locality window.",
    )
    parser.add_argument(
        "--trace-observation-dram-max-records",
        dest="trace_observation_dram_max_records",
        type=int,
        default=100000,
        help="Maximum physical DRAM reads in the O3 window; 0 means unlimited.",
    )
    parser.add_argument(
        "--trace-observation-remote-warmup-requests",
        dest="trace_observation_remote_warmup_requests",
        type=int,
        default=0,
        help="Remote requests to skip before the O4/O5/O6 window.",
    )
    parser.add_argument(
        "--trace-observation-remote-max-records",
        dest="trace_observation_remote_max_records",
        type=int,
        default=100000,
        help="Maximum remote requests in the O4/O5/O6 window.",
    )
    parser.add_argument(
        "--trace-observation-l2-sample-max",
        dest="trace_observation_l2_sample_max",
        type=int,
        default=100000,
        help="Maximum periodic L2 utilization samples for O6.",
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
        default=DEFAULT_L1V_MSHR_ENTRIES,
        help=(
            "L1V MSHR entries per cache. Formal experiments default to 16; "
            "0 is accepted only for explicit diagnostic use."
        ),
    )
    parser.add_argument(
        "--l1v-max-concurrent-trans",
        dest="l1v_max_concurrent_trans",
        type=int,
        default=DEFAULT_L1V_MAX_CONCURRENT_TRANS,
        help=(
            "Maximum concurrent L1V transactions per cache. Formal "
            "experiments default to 16; 0 keeps the binary default."
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
        help=(
            "Optional max_workers cap for sampled runs. The default 0 "
            "disables the cap and respects --max-workers."
        ),
    )
