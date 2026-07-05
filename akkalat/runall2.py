import argparse
from datetime import datetime
import os
from pathlib import Path
import shlex
import subprocess
import time

ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
TARGETS = [
    "baseline",
]

DEFAULT_MAX_WORKERS = 16
DEFAULT_MIN_FREE_RAM_GB = 60.0
DEFAULT_MEMORY_SCAN_INTERVAL_MINUTES = 30.0
INITIAL_FILL_SETTLE_SECONDS = 2.0

TRADITIONAL_BENCHMARKS = [
    "bitonicsort",
    "relu",
    "spmv",
    "matrixmultiplication",
    "matrixtranspose",
    "fastwalshtransform",
    "fft",
    "kmeans",
    "im2col",
    "aes",
    "floydwarshall",
    "pagerank",
    "simpleconvolution",
    "fir",
]

POLYBENCH_BENCHMARKS = [
    "atax",
    "bicg",
]

MATRIX_VARIANT_BENCHMARKS = [
    "matrixmultiplication-pipeline-smoke",
    "matrixmultiplication-middletile",
    "matrixmultiplication-middletile-pipeline-smoke",
    "matrixtranspose-middletile",
]

ADDITIONAL_STANDALONE_BENCHMARKS = [
    "nbody",
    "nw",
    "stencil2d",
]

GPU_BENCHMARKS_TIER2_RUNNABLE = [
    "rodinia_bfs",
    "graph_pr",
    "heteromark_pagerank",
    "lonestar_sssp",
    "shoc_spmv",
    "parboil_spmv",
    "npb_cg",
    "altis_gups",
    "cuda_transpose",
    "shoc_stencil2d",
    "rodinia_hotspot",
]

GPU_BENCHMARKS_TIER2_EXISTING_WRAPPER_ALIASES = [
    "heteromark_aes",
    "heteromark_fir",
    "polybench_atax",
    "polybench_bicg",
    "rodinia_nw",
    "shoc_fft",
]

GPU_BENCHMARKS_TIER2_HSACO_ALL = [
    "altis_cfd",
    "altis_gups",
    "altis_particlefilter",
    "altis_raytracing",
    "chai_cedd",
    "chai_hsto",
    "cuda_convolution_separable",
    "cuda_nbody",
    "cuda_scan_large",
    "cuda_transpose",
    "graph_cc",
    "graph_pr",
    "graph_tc",
    "heteromark_aes",
    "heteromark_fir",
    "heteromark_pagerank",
    "lonestar_bh",
    "lonestar_dmr",
    "lonestar_sssp",
    "npb_cg",
    "npb_ep",
    "npb_is",
    "npb_mg",
    "parboil_cutcp",
    "parboil_histogram",
    "parboil_lbm",
    "parboil_sgemm",
    "parboil_spmv",
    "parboil_stencil",
    "polybench_2dconv",
    "polybench_2mm",
    "polybench_3dconv",
    "polybench_3mm",
    "polybench_atax",
    "polybench_bicg",
    "polybench_correlation",
    "polybench_fdtd2d",
    "polybench_gemm",
    "polybench_gramschmidt",
    "polybench_jacobi2d",
    "polybench_mvt",
    "polybench_syr2k",
    "rodinia_backprop",
    "rodinia_bfs",
    "rodinia_gaussian",
    "rodinia_hotspot",
    "rodinia_hotspot3d",
    "rodinia_kmeans",
    "rodinia_lavamd",
    "rodinia_lud",
    "rodinia_nw",
    "rodinia_pathfinder",
    "rodinia_srad",
    "shoc_devicememory",
    "shoc_fft",
    "shoc_gemm",
    "shoc_reduction",
    "shoc_scan",
    "shoc_sort",
    "shoc_spmv",
    "shoc_stencil2d",
    "shoc_triad",
    "tango_binomial_options",
    "tango_blackscholes",
]

GPU_BENCHMARKS_TIER2 = GPU_BENCHMARKS_TIER2_RUNNABLE[:]

# One representative from each currently implemented tier2 wrapper family.
# Several tier2 names are selectable for traceability, but their Go wrappers
# are copied from the same implementation.
GPU_BENCHMARKS_TIER2_UNIQUE = [
    "rodinia_bfs",      # also covers lonestar_sssp in the current wrapper
    "graph_pr",        # also covers heteromark_pagerank in tier2
    "shoc_spmv",       # also covers parboil_spmv, npb_cg, altis_gups
    "cuda_transpose",
    "shoc_stencil2d",  # also covers rodinia_hotspot in the current wrapper
]

GPU_BENCHMARKS_TIER2_SELECTABLE = list(dict.fromkeys(
    GPU_BENCHMARKS_TIER2_RUNNABLE
    + GPU_BENCHMARKS_TIER2_EXISTING_WRAPPER_ALIASES
))

GPU_BENCHMARKS_TIER2_PENDING_WRAPPERS = [
    benchmark for benchmark in GPU_BENCHMARKS_TIER2_HSACO_ALL
    if benchmark not in GPU_BENCHMARKS_TIER2_SELECTABLE
]

TRAINING_BENCHMARKS = [
    "lenet",
    "minerva",
    "vgg16",
]

CONCURRENT_BENCHMARKS = [
    "prfws",
    "btfwt",
    "fwtfws",
    "kmsc",
    "scsc",
    "mmfir",
    "prsc",
    "fwtmt",
    "spmvmt",
]

RUNNABLE_BENCHMARKS = list(dict.fromkeys(
    TRADITIONAL_BENCHMARKS
    + POLYBENCH_BENCHMARKS
    + MATRIX_VARIANT_BENCHMARKS
    + ADDITIONAL_STANDALONE_BENCHMARKS
    + GPU_BENCHMARKS_TIER2_SELECTABLE
    + TRAINING_BENCHMARKS
    + CONCURRENT_BENCHMARKS
))

CANONICAL_BENCHMARKS = list(dict.fromkeys(
    TRADITIONAL_BENCHMARKS
    + POLYBENCH_BENCHMARKS
    + ADDITIONAL_STANDALONE_BENCHMARKS
    + ["rodinia_bfs"]
))

ALL_BENCHMARKS = list(dict.fromkeys(
    RUNNABLE_BENCHMARKS
    + GPU_BENCHMARKS_TIER2_PENDING_WRAPPERS
))

DEFAULT_RUN_BENCHMARKS = TRADITIONAL_BENCHMARKS[:]

BENCHMARK_ALIASES = {
    "all": CANONICAL_BENCHMARKS,
    "canonical": CANONICAL_BENCHMARKS,
    "unique": CANONICAL_BENCHMARKS,
    "all_unique": CANONICAL_BENCHMARKS,
    "all_with_aliases": RUNNABLE_BENCHMARKS,
    "supported": RUNNABLE_BENCHMARKS,
    "traditional": TRADITIONAL_BENCHMARKS,
    "polybench": POLYBENCH_BENCHMARKS,
    "experimental": MATRIX_VARIANT_BENCHMARKS,
    "matrix_variants": MATRIX_VARIANT_BENCHMARKS,
    "standalone": ADDITIONAL_STANDALONE_BENCHMARKS,
    "tier2": GPU_BENCHMARKS_TIER2,
    "gpu_benchmarks_tier2": GPU_BENCHMARKS_TIER2,
    "tier2_runnable": GPU_BENCHMARKS_TIER2,
    "tier2_unique": GPU_BENCHMARKS_TIER2_UNIQUE,
    "gpu_benchmarks_tier2_unique": GPU_BENCHMARKS_TIER2_UNIQUE,
    "tier2_existing_wrapper_aliases": GPU_BENCHMARKS_TIER2_EXISTING_WRAPPER_ALIASES,
    "tier2_all": GPU_BENCHMARKS_TIER2_HSACO_ALL,
    "tier2_hsaco_all": GPU_BENCHMARKS_TIER2_HSACO_ALL,
    "gpu_benchmarks_tier2_all": GPU_BENCHMARKS_TIER2_HSACO_ALL,
    "training": TRAINING_BENCHMARKS,
    "concurrent": CONCURRENT_BENCHMARKS,
}


# Configure which benchmarks to run when --benchmarks is omitted.
BENCHMARKS_BY_TARGET = {
    "baseline": ["all"],
    "400latency": DEFAULT_RUN_BENCHMARKS,
    # "TLBSensitiveStudy": ["all"],
}

DEFAULT_BENCHMARK_FLAGS = [
    # "-max-wg=157200",
    "-max-wg=76800",
    # "-max-wg=38400",
]

BASE_COMMON_FLAGS = [
    "-timing",
    "-num-memory-banks=16",
    "-bandwidth=48",
    "-switch-latency=32",
    "-magic-memory-copy",
    "-report-all",
]

DEFAULT_MMUTLB_LOOKUP_LATENCY = 10
DEFAULT_TIMEOUT_MINUTES = 0.0
DEFAULT_PHOTON_SAMPLED_WARMUP = 512
DEFAULT_PHOTON_SAMPLED_GRANULARITY = 512
DEFAULT_PHOTON_LOOP_SAMPLED_WARMUP = 512

GLOBAL_PHOTON_FLAGS = [
    "-sampled",
    "-branch-sampled",
    "-kernel-sampled",
    "-loop-sampled",
]
PHOTON_BRANCH_LOOP_FLAGS = {
    "-branch-sampled",
    "-loop-sampled",
}

CONFIGS = [
    ("baseline", []),
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

MECHANISM_ALIASES = {
    "all": [
        "baseline",
        "m1",
        "m2",
        "m3",
        "m1_m2",
        "m1_m3",
        "m2_m3",
        "m1_m2_m3",
    ],
    "base": ["baseline"],
    "m1.1": ["m1"],
    "m1.2": ["m1"],
    "m1_1": ["m1"],
    "m1_2": ["m1"],
    "m1.1+m1.2": ["m1"],
    "m1+m2": ["m1_m2"],
    "m1+m3": ["m1_m3"],
    "m2+m3": ["m2_m3"],
    "m1.1+m2+m3": ["m1_m2_m3"],
    "m1.2+m2+m3": ["m1_m2_m3"],
    "m1_1_m2_m3": ["m1_m2_m3"],
    "m1_2_m2_m3": ["m1_m2_m3"],
    "m1+m2+m3": ["m1_m2_m3"],
}

MECHANISM_NAMES = [
    "baseline",
    "m1",
    "m2",
    "m3",
    "m1_m2",
    "m1_m3",
    "m2_m3",
    "m1_m2_m3",
]

output_dir = ""



def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--only-config",
        dest="only_config",
        default="",
        help="Only run experiments with this config name.",
    )
    parser.add_argument(
        "--output-dir",
        dest="output_dir",
        default="",
        help=(
            "Write results to this directory instead of a timestamped directory "
            "under akkalat/results."
        ),
    )
    parser.add_argument(
        "--target",
        "--targets",
        dest="targets",
        default=",".join(TARGETS),
        help=(
            "Comma-separated benchmark binary targets under akkalat/. "
            "Default: baseline."
        ),
    )
    parser.add_argument(
        "--skip-build",
        dest="skip_build",
        action="store_true",
        help="Use existing target binaries and skip go build.",
    )
    parser.add_argument(
        "--mechanisms",
        "--mechanism",
        dest="mechanisms",
        default="baseline",
        help=(
            "Comma-separated mechanism arms to run: baseline,m1,m2,m3,"
            "m1_m2,m1_m3,m2_m3,m1_m2_m3,all. M1 is direct "
            "local DRAM bypass with 128B AU coalescing."
        ),
    )
    parser.add_argument(
        "--m2-max-batch-lines",
        dest="m2_max_batch_lines",
        type=int,
        default=8,
        help="M2 maximum unique cache lines per bitmap RDMA batch.",
    )
    parser.add_argument(
        "--m2-max-wait-ns",
        dest="m2_max_wait_ns",
        type=int,
        default=50,
        help="M2 maximum collection wait in ns.",
    )
    parser.add_argument(
        "--m2-batch-table-entries",
        dest="m2_batch_table_entries",
        type=int,
        default=64,
        help="M2 active requester-side batch table entries per RDMA engine.",
    )
    parser.add_argument(
        "--m3-l1-remote-cache-entries",
        dest="m3_l1_remote_cache_entries",
        type=int,
        default=128,
        help="M3 64B remote-data entries per L1V cache.",
    )
    parser.add_argument(
        "--mmutlb-lookup-latency",
        dest="mmutlb_lookup_latency",
        type=int,
        default=DEFAULT_MMUTLB_LOOKUP_LATENCY,
        help="Fixed MMUTLB/IOTLB lookup latency in cycles.",
    )
    parser.add_argument(
        "--l1v-tlb-mshr-entries",
        dest="l1v_tlb_mshr_entries",
        type=int,
        default=4,
        help="Number of L1V TLB MSHR entries.",
    )
    parser.add_argument(
        "--l1v-req-per-cycle",
        dest="l1v_req_per_cycle",
        type=int,
        default=32,
        help="L1V request-path width for ROB, address translator, TLB, and cache.",
    )
    parser.add_argument(
        "--log2-page-size",
        dest="log2_page_size",
        type=int,
        default=None,
        help="GPU page size as log2(bytes). For example 12=4KB, 15=32KB.",
    )
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
        default=DEFAULT_MAX_WORKERS,
        help=(
            "Maximum number of benchmark workloads to run concurrently."
        ),
    )
    parser.add_argument(
        "--max-workloads",
        dest="max_workers",
        type=int,
        default=argparse.SUPPRESS,
        help=argparse.SUPPRESS,
    )
    parser.add_argument(
        "--benchmarks",
        dest="benchmarks",
        default="",
        help=(
            "Comma-separated benchmark list. Presets: "
            + ",".join(sorted(BENCHMARK_ALIASES))
            + "."
        ),
    )
    parser.add_argument(
        "--configs",
        dest="configs",
        default="",
        help=(
            "Comma-separated config list. Choices: "
            + ",".join(name for name, _ in CONFIGS)
            + ". Use photon_all or all for grouped configs."
        ),
    )
    parser.add_argument(
        "--extra-benchmark-flags",
        dest="extra_benchmark_flags",
        default="",
        help="Additional flags appended to each benchmark binary command.",
    )
    parser.add_argument(
        "--trace-memory-path",
        action="store_true",
        help=(
            "Enable request-level memory-path tracing. This writes compact "
            "stage summaries plus a bounded raw window."
        ),
    )
    parser.add_argument(
        "--trace-memory-path-warmup-accesses",
        type=int,
        default=100000,
        help="Observed L1V accesses to skip before the memory-path raw window.",
    )
    parser.add_argument(
        "--trace-memory-path-max-records",
        type=int,
        default=20000,
        help=(
            "Maximum memory-path raw records after warmup. Stage summaries are "
            "still emitted; use --trace-memory-path-exit-on-complete for a "
            "bounded trace-only run."
        ),
    )
    parser.add_argument(
        "--trace-memory-path-remote-only",
        action="store_true",
        help=(
            "Only count and write remote L1V memory paths. Warmup and "
            "max-records are applied to remote paths, not all L1V accesses."
        ),
    )
    parser.add_argument(
        "--trace-memory-path-stream",
        action="store_true",
        help=(
            "Stream memory-path raw/L1V path rows directly to output files "
            "and release completed request records."
        ),
    )
    parser.add_argument(
        "--trace-memory-path-tail-window",
        action="store_true",
        help=(
            "Keep the last max-records memory-path rows after warmup and "
            "dump that tail window at benchmark end."
        ),
    )
    parser.add_argument(
        "--trace-memory-path-exit-on-complete",
        action="store_true",
        help=(
            "Exit each benchmark after the memory-path raw window is full. "
            "Useful for focused critical-path tracing."
        ),
    )
    parser.add_argument(
        "--report-l2-source",
        action="store_true",
        help="Emit aggregate L2 data-source and remote-GPM summary CSV files.",
    )
    parser.add_argument(
        "--l2-source-tile-width",
        type=int,
        default=7,
        help="Tile-array width used to compute GPM Manhattan hops.",
    )
    parser.add_argument(
        "--max-wg",
        dest="max_wg",
        type=int,
        default=None,
        help=(
            "Pass -max-wg to each benchmark. 0 disables the default cap."
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
        "--min-free-ram-gb",
        dest="min_free_ram_gb",
        type=float,
        default=DEFAULT_MIN_FREE_RAM_GB,
        help=(
            "Minimum Linux MemAvailable, in GiB, required before launching the "
            "next benchmark."
        ),
    )
    parser.add_argument(
        "--memory-scan-interval-minutes",
        dest="memory_scan_interval_minutes",
        type=float,
        default=DEFAULT_MEMORY_SCAN_INTERVAL_MINUTES,
        help=(
            "How often to check MemAvailable and consider launching one "
            "benchmark. A completed benchmark also triggers one immediate "
            "memory check."
        ),
    )
    parser.add_argument(
        "--photon-debug",
        action="store_true",
        help="Add -photon-debug to sampled WSG-style configs.",
    )
    parser.add_argument(
        "--photon",
        action="store_true",
        help=(
            "Append the script-level Photon sampled flags to every selected "
            "config."
        ),
    )
    parser.add_argument(
        "--photon-no-branch-loop",
        action="store_true",
        help=(
            "When --photon is set, skip -branch-sampled and -loop-sampled "
            "for debugging sampled execution."
        ),
    )
    parser.add_argument(
        "--photon-verbose",
        action="store_true",
        help="Add -photon-debug and -photon-debug-verbose to sampled configs.",
    )
    parser.add_argument(
        "--disable-servers",
        action="store_true",
        help="Pass -disable-servers to each benchmark process.",
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
        "--dry-run",
        action="store_true",
        help="Print commands without building or running them.",
    )
    return parser.parse_args()


def parse_csv(value):
    return [item.strip() for item in value.split(",") if item.strip()]


def selected_targets(args):
    targets = parse_csv(args.targets)
    if not targets:
        raise ValueError("--target must include at least one target")
    return unique_preserving_order(targets)


def selected_mechanisms(args):
    expanded = []
    for item in parse_csv(args.mechanisms):
        expanded += MECHANISM_ALIASES.get(item, [item])
    if not expanded:
        expanded = ["baseline"]

    mechanisms = unique_preserving_order(expanded)
    unknown = sorted(set(mechanisms) - set(MECHANISM_NAMES))
    if unknown:
        allowed = sorted(set(MECHANISM_NAMES) | set(MECHANISM_ALIASES))
        raise ValueError(
            f"unknown mechanism: {','.join(unknown)}. "
            f"Allowed: {', '.join(allowed)}"
        )
    return mechanisms


def mechanism_flags(args, mechanism):
    if mechanism == "baseline":
        return []
    m1_flags = ["-m1-direct-dram-bypass-enable"]
    m2_flags = [
        "-m2-rdma-batch-enable",
        f"-m2-max-batch-lines={args.m2_max_batch_lines}",
        f"-m2-max-wait-ns={args.m2_max_wait_ns}",
        f"-m2-batch-table-entries={args.m2_batch_table_entries}",
    ]
    m2_au_flags = m2_flags + ["-m2-au-prefetch-enable"]
    m3_flags = [
        "-m3-l1-remote-cache-enable",
        f"-m3-l1-remote-cache-entries={args.m3_l1_remote_cache_entries}",
    ]
    if mechanism == "m1":
        return m1_flags
    if mechanism == "m2":
        return m2_flags
    if mechanism == "m3":
        return m3_flags
    if mechanism == "m1_m2":
        return m1_flags + m2_flags
    if mechanism == "m1_m3":
        return m1_flags + m3_flags
    if mechanism == "m2_m3":
        return m2_au_flags + m3_flags
    if mechanism == "m1_m2_m3":
        return m1_flags + m2_au_flags + m3_flags
    raise ValueError(f"unknown mechanism: {mechanism}")


def mechanism_config_name(mechanism, config_name, mechanisms):
    if len(mechanisms) == 1 and mechanism == "baseline":
        return config_name
    return f"{mechanism}_{config_name}"


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


def build_common_flags(args):
    flags = BASE_COMMON_FLAGS + [
        f"-mmutlb-lookup-latency={args.mmutlb_lookup_latency}",
        f"-l1v-tlb-mshr-entries={args.l1v_tlb_mshr_entries}",
        f"-l1v-req-per-cycle={args.l1v_req_per_cycle}",
    ]
    if args.log2_page_size is not None:
        flags.append(f"-log2-page-size={args.log2_page_size}")
    if args.disable_servers:
        flags.append("-disable-servers")
    if args.trace_memory_path:
        flags += [
            "-trace-memory-path",
            (
                "-trace-memory-path-warmup-accesses="
                f"{args.trace_memory_path_warmup_accesses}"
            ),
            (
                "-trace-memory-path-max-records="
                f"{args.trace_memory_path_max_records}"
            ),
        ]
        if args.trace_memory_path_exit_on_complete:
            flags.append("-trace-memory-path-exit-on-complete")
        if args.trace_memory_path_remote_only:
            flags.append("-trace-memory-path-remote-only")
        if args.trace_memory_path_stream:
            flags.append("-trace-memory-path-stream")
        if args.trace_memory_path_tail_window:
            flags.append("-trace-memory-path-tail-window")
    if args.report_l2_source:
        flags += [
            "-report-l2-source",
            f"-l2-source-tile-width={args.l2_source_tile_width}",
        ]
    return flags


def build_configs(args):
    if args.configs:
        return build_selected_configs(args)
    return build_photon_configs(args, ["baseline"])


def build_selected_configs(args):
    requested = parse_csv(args.configs)
    photon_configs = {name: flags for name, flags in CONFIGS}
    selected = []

    for name in requested:
        if name in ("all", "photon_all"):
            selected += build_photon_configs(args, list(photon_configs))
            continue

        if name in photon_configs:
            selected += build_photon_configs(args, [name])
            continue

        allowed = sorted(set(photon_configs) | {"photon_all", "all"})
        raise ValueError(
            f"unknown config: {name}. Allowed: {', '.join(allowed)}"
        )

    return unique_preserving_config_names(selected)


def unique_preserving_config_names(configs):
    seen = set()
    unique = []
    for name, flags in configs:
        if name in seen:
            continue
        seen.add(name)
        unique.append((name, flags))
    return unique


def build_photon_configs(args, requested):
    selected = []
    for name, flags in CONFIGS:
        if name not in requested:
            continue
        config_flags = flags[:]
        if (args.photon_debug or args.photon_verbose) and name != "baseline":
            config_flags.append("-photon-debug")
        if args.photon_verbose and name != "baseline":
            config_flags.append("-photon-debug-verbose")
        selected += expand_sampled_params(args, name, config_flags)

    return selected


def expand_sampled_params(args, name, flags):
    if "-sampled" not in flags:
        return [(name, flags)]

    warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
    granularities = parse_int_csv(
        args.sampled_granularities, "sampled-granularities")
    if not warmups and not granularities:
        return [(name, flags)]
    if not warmups:
        warmups = [1024]
    if not granularities:
        granularities = [2048]

    expanded = []
    for warmup in warmups:
        for granularity in granularities:
            expanded.append((
                f"{name}_w{warmup}_g{granularity}",
                flags + [
                    f"-sampled-warmup={warmup}",
                    f"-sampled-granularity={granularity}",
                ],
            ))
    return expanded


def get_benchmarks_for_target(target):
    selected = BENCHMARKS_BY_TARGET.get(target, ["all"])
    selected = expand_benchmark_selection(selected)

    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks for {target}: {unknown}")
    pending = sorted(set(selected) & set(GPU_BENCHMARKS_TIER2_PENDING_WRAPPERS))
    if pending:
        raise ValueError(
            "tier2 HSACO exists but Go wrapper is not implemented for "
            f"{target}: {', '.join(pending)}"
        )

    return selected


def get_selected_benchmarks(args, target):
    if args.benchmarks:
        selected = parse_csv(args.benchmarks)
    else:
        selected = get_benchmarks_for_target(target)

    selected = expand_benchmark_selection(selected)
    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks: {unknown}")
    pending = sorted(set(selected) & set(GPU_BENCHMARKS_TIER2_PENDING_WRAPPERS))
    if pending:
        raise ValueError(
            "these tier2 workloads have compiled HSACO but still need "
            "MGPUSim Go wrappers: " + ", ".join(pending)
        )
    return selected


def strip_disable_server_flags(flags):
    return [
        flag for flag in flags
        if flag not in ("-disable-servers", "--disable-servers")
    ]


def has_flag_with_prefix(flags, prefix):
    return any(flag.startswith(prefix) for flag in flags)


def append_unique_flag(flags, flag):
    if flag not in flags:
        flags.append(flag)


def selected_global_photon_flags(args):
    flags = list(GLOBAL_PHOTON_FLAGS)
    if args.photon_no_branch_loop:
        flags = [
            flag for flag in flags
            if flag not in PHOTON_BRANCH_LOOP_FLAGS
        ]
    return flags


def append_default_photon_tuning_flags(args, flags):
    if (
        "-loop-sampled" in flags and
        not has_flag_with_prefix(flags, "-loop-sampled-warmup=")
    ):
        flags.append(
            f"-loop-sampled-warmup={DEFAULT_PHOTON_LOOP_SAMPLED_WARMUP}"
        )

    if not has_flag_with_prefix(flags, "-sampled-warmup="):
        flags.append(
            f"-sampled-warmup={DEFAULT_PHOTON_SAMPLED_WARMUP}"
        )
    if not has_flag_with_prefix(flags, "-sampled-granularity="):
        flags.append(
            f"-sampled-granularity={DEFAULT_PHOTON_SAMPLED_GRANULARITY}"
        )


def add_global_photon_flags(args, flags):
    if not args.photon:
        return flags

    photon_flags = flags[:]
    for flag in selected_global_photon_flags(args):
        append_unique_flag(photon_flags, flag)

    if args.photon_debug or args.photon_verbose:
        append_unique_flag(photon_flags, "-photon-debug")
    if args.photon_verbose:
        append_unique_flag(photon_flags, "-photon-debug-verbose")

    append_default_photon_tuning_flags(args, photon_flags)

    return photon_flags


def default_benchmark_flags(args):
    if args.max_wg is not None:
        if args.max_wg <= 0:
            return []
        return [f"-max-wg={args.max_wg}"]
    return DEFAULT_BENCHMARK_FLAGS[:]


def make_exps(args, configs):
    exps = []
    extra_flags = strip_disable_server_flags(shlex.split(args.extra_benchmark_flags))
    mechanisms = selected_mechanisms(args)
    for target in selected_targets(args):
        for benchmark in get_selected_benchmarks(args, target):
            for config_name, config_flags in configs:
                for mechanism in mechanisms:
                    flags = (
                        default_benchmark_flags(args)
                        + strip_disable_server_flags(config_flags)
                        + mechanism_flags(args, mechanism)
                        + extra_flags
                    )
                    flags = add_global_photon_flags(args, flags)
                    exps.append(
                        {
                            "target": target,
                            "benchmark": benchmark,
                            "mechanism": mechanism,
                            "config_name": mechanism_config_name(
                                mechanism, config_name, mechanisms),
                            "base_config_name": config_name,
                            "flags": flags,
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
            ["go", "build", "-buildvcs=false"], cwd=target_dir, env=env)
        process.wait()
        if process.returncode != 0:
            raise RuntimeError(f"failed to build {target}")


def exp_file_stem(exp):
    return os.path.join(
        output_dir,
        f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}',
    )


def experiment_command(exp):
    binary = os.path.join(ROOT_DIR, exp["target"], exp["target"])
    metric_file_name = f"{exp_file_stem(exp)}_metrics"
    return [
        binary,
        f'-benchmark={exp["benchmark"]}',
        *exp["common_flags"],
        *exp["flags"],
        f"-metric-file-name={metric_file_name}",
    ]


def read_mem_available_kb():
    try:
        with open("/proc/meminfo", "r", encoding="utf-8") as meminfo_file:
            for line in meminfo_file:
                if line.startswith("MemAvailable:"):
                    parts = line.split()
                    if len(parts) >= 2:
                        return int(parts[1])
    except (FileNotFoundError, PermissionError, ValueError):
        return None

    return None


def format_memory_kb(kb):
    if kb is None:
        return "unknown"
    if kb >= 1024 * 1024:
        return f"{kb / (1024 * 1024):.2f} GiB"
    if kb >= 1024:
        return f"{kb / 1024:.2f} MiB"
    return f"{kb} KiB"


def launch_experiment(exp):
    file_stem = exp_file_stem(exp)
    metric_file_name = f"{file_stem}_metrics"

    cmd = experiment_command(exp)
    cmd_str = shlex.join(cmd)
    print(cmd_str, flush=True)

    out_file_name = f"{file_stem}_out.stdout"
    out_file = open(out_file_name, "w", encoding="utf-8")
    start_time = datetime.now()
    launch_mem_kb = read_mem_available_kb()
    out_file.write(f"Executing {cmd_str}\n")
    out_file.write(f"Start time: {start_time}\n")
    out_file.write(
        "Launch MemAvailable: "
        f"{launch_mem_kb} KiB ({format_memory_kb(launch_mem_kb)})\n"
    )
    out_file.flush()

    process = subprocess.Popen(
        cmd,
        stdout=out_file,
        stderr=subprocess.STDOUT,
        cwd=ROOT_DIR,
        text=True,
        bufsize=1,
    )

    return {
        "exp": exp,
        "process": process,
        "cmd_str": cmd_str,
        "metric_file_name": metric_file_name,
        "out_file": out_file,
        "start_time": start_time,
        "start_monotonic": time.monotonic(),
    }


def finalize_experiment(state, timed_out=False):
    process = state["process"]
    if timed_out and process.poll() is None:
        process.kill()
        process.wait()

    end_time = datetime.now()
    elapsed_time = end_time - state["start_time"]
    out_file = state["out_file"]
    out_file.write(f"Return code: {process.returncode}\n")
    if timed_out:
        timeout_seconds = state["exp"].get("timeout_seconds", 0)
        out_file.write(f"Timed out after {timeout_seconds} seconds\n")
    out_file.write(f"End time: {end_time}\n")
    out_file.write(f"Elapsed time: {elapsed_time}\n")
    out_file.close()

    cmd_str = state["cmd_str"]
    if timed_out:
        print(f"Timed out executing {cmd_str}")
        return {
            "exp": state["exp"],
            "returncode": -9,
            "timeout": state["exp"].get("timeout_seconds", 0),
        }

    if process.returncode != 0:
        print(f"Error executing {cmd_str}")
        return {"exp": state["exp"], "returncode": process.returncode}

    metrics_csv = state["metric_file_name"] + ".csv"
    if not os.path.exists(metrics_csv):
        print(f"Missing metrics file for {cmd_str}: {metrics_csv}")
        return {
            "exp": state["exp"],
            "returncode": -1,
            "missing_metrics": metrics_csv,
        }

    print(f"Executed {cmd_str}, time {elapsed_time}")
    return {"exp": state["exp"], "returncode": 0}


def running_cap_reached(args, running):
    return len(running) >= effective_workload_cap(args)


def effective_workload_cap(args):
    return args.max_workers


def print_scheduler_status(prefix, queued, running, completed, failed):
    print(
        f"{prefix} queued={len(queued)} running={len(running)} "
        f"completed={completed} failed={failed}",
        flush=True,
    )


def try_launch_ready_experiments(
    queued,
    running,
    args,
    min_mem_available_kb,
    status_prefix,
    completed,
    failed,
    settle_seconds=0.0,
    max_launches=None,
):
    launched_any = False
    blocked_reason = ""
    launched_count = 0

    while (
        queued
        and not running_cap_reached(args, running)
        and (max_launches is None or launched_count < max_launches)
    ):
        available_kb = read_mem_available_kb()
        print(
            f"{status_prefix} "
            f"MemAvailable={available_kb} KiB "
            f"({format_memory_kb(available_kb)}), "
            f"threshold={min_mem_available_kb} KiB "
            f"({format_memory_kb(min_mem_available_kb)})",
            flush=True,
        )
        print_scheduler_status(
            status_prefix, queued, running, completed, failed)

        if available_kb is None:
            blocked_reason = "mem_unknown"
            break

        if available_kb < min_mem_available_kb:
            blocked_reason = "low_mem"
            break

        exp = queued.pop(0)
        running.append(launch_experiment(exp))
        launched_any = True
        launched_count += 1
        print_scheduler_status("[launch]", queued, running, completed, failed)

        if settle_seconds > 0 and queued and not running_cap_reached(args, running):
            time.sleep(settle_seconds)

    if queued and running_cap_reached(args, running):
        blocked_reason = "cap"

    return launched_any, blocked_reason


def memory_gated_run(exps, args):
    queued = list(exps)
    running = []
    completed = 0
    failed = 0
    min_mem_available_kb = int(args.min_free_ram_gb * 1024 * 1024)
    scan_interval_seconds = args.memory_scan_interval_minutes * 60
    next_scan_time = time.monotonic() + scan_interval_seconds
    initial_fill = True
    initial_fill_cap_logged = False

    print(
        "Memory gate: "
        f"MemAvailable >= {min_mem_available_kb} KiB "
        f"({format_memory_kb(min_mem_available_kb)}), "
        f"scan interval={args.memory_scan_interval_minutes} minutes",
        flush=True,
    )
    print(
        f"Max concurrent workloads: {effective_workload_cap(args)}",
        flush=True,
    )

    while queued or running:
        now = time.monotonic()
        still_running = []
        completed_this_round = False
        for state in running:
            process = state["process"]
            timeout_seconds = state["exp"].get("timeout_seconds", 0)
            timed_out = (
                timeout_seconds > 0
                and process.poll() is None
                and now - state["start_monotonic"] >= timeout_seconds
            )

            if timed_out:
                result = finalize_experiment(state, timed_out=True)
            elif process.poll() is None:
                still_running.append(state)
                continue
            else:
                result = finalize_experiment(state)

            completed_this_round = True
            completed += 1
            if result["returncode"] != 0:
                failed += 1
            print(result, flush=True)

        running = still_running
        if completed_this_round:
            initial_fill_cap_logged = False
            if queued and not initial_fill:
                next_scan_time = time.monotonic()

        if queued and initial_fill:
            launched_any, blocked_reason = try_launch_ready_experiments(
                queued,
                running,
                args,
                min_mem_available_kb,
                "[initial-fill]",
                completed,
                failed,
                settle_seconds=INITIAL_FILL_SETTLE_SECONDS,
            )
            if launched_any:
                initial_fill_cap_logged = False

            if blocked_reason == "mem_unknown":
                print(
                    "Initial fill paused: cannot read Linux MemAvailable.",
                    flush=True,
                )
                initial_fill = False
                next_scan_time = time.monotonic() + scan_interval_seconds
            elif blocked_reason == "low_mem":
                print(
                    "Initial fill complete: not enough free RAM to launch "
                    "the next benchmark.",
                    flush=True,
                )
                initial_fill = False
                next_scan_time = time.monotonic() + scan_interval_seconds

            if queued and running_cap_reached(args, running):
                if not initial_fill_cap_logged:
                    print(
                        "Initial fill paused: max-workers cap is reached.",
                        flush=True,
                    )
                    print_scheduler_status(
                        "[initial-fill]", queued, running, completed, failed)
                    initial_fill_cap_logged = True

            if launched_any:
                continue

        now = time.monotonic()
        if queued and not initial_fill and now >= next_scan_time:
            _, blocked_reason = try_launch_ready_experiments(
                queued,
                running,
                args,
                min_mem_available_kb,
                "[memory-scan]",
                completed,
                failed,
                settle_seconds=INITIAL_FILL_SETTLE_SECONDS,
            )

            if blocked_reason == "mem_unknown":
                print(
                    "Waiting: cannot read Linux MemAvailable.",
                    flush=True,
                )
            elif blocked_reason == "low_mem":
                print(
                    "Waiting: not enough free RAM to launch the next benchmark.",
                    flush=True,
                )
            elif blocked_reason == "cap":
                print(
                    "Waiting: max-workers cap is reached.",
                    flush=True,
                )

            next_scan_time = time.monotonic() + scan_interval_seconds

        if queued or running:
            if queued and not initial_fill:
                now = time.monotonic()
                sleep_seconds = max(0.1, min(30.0, next_scan_time - now))
            else:
                sleep_seconds = 5.0
            time.sleep(sleep_seconds)

    print_scheduler_status("[summary]", queued, running, completed, failed)
    if failed > 0:
        raise SystemExit(1)


def dry_run_commands(exps):
    for exp in exps:
        print(shlex.join(experiment_command(exp)))


def create_output_dir(args):
    global output_dir
    if args.output_dir:
        output_dir = os.path.abspath(args.output_dir)
    else:
        output_dir = os.path.join(
            ROOT_DIR,
            "results",
            datetime.now().strftime("%Y-%m-%d-%H-%M-%S-runall"),
        )

    results_dir = os.path.join(ROOT_DIR, "results")
    if not os.path.exists(results_dir):
        os.makedirs(results_dir)

    if not os.path.exists(output_dir):
        os.makedirs(output_dir)


def main():
    global output_dir

    args = parse_args()
    if args.trace_memory_path_tail_window and args.trace_memory_path_stream:
        raise ValueError(
            "--trace-memory-path-tail-window cannot be used with "
            "--trace-memory-path-stream"
        )
    if (
        args.trace_memory_path_tail_window
        and args.trace_memory_path_exit_on_complete
    ):
        raise ValueError(
            "--trace-memory-path-tail-window cannot be used with "
            "--trace-memory-path-exit-on-complete"
        )
    common_flags = build_common_flags(args)
    configs = build_configs(args)
    if args.only_config:
        configs = [c for c in configs if c[0] == args.only_config]
        if not configs:
            raise ValueError(f"no config named '{args.only_config}'")
    exps = make_exps(args, configs)
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
        create_output_dir(args)

    timeout_seconds = int(args.timeout_minutes * 60)
    for exp in exps:
        exp["common_flags"] = common_flags
        exp["timeout_seconds"] = timeout_seconds

    if args.max_workers <= 0:
        raise ValueError("--max-workers must be greater than 0")
    if args.min_free_ram_gb < 0:
        raise ValueError("--min-free-ram-gb must be non-negative")
    if args.memory_scan_interval_minutes <= 0:
        raise ValueError("--memory-scan-interval-minutes must be greater than 0")
    if args.trace_memory_path_warmup_accesses < 0:
        raise ValueError("--trace-memory-path-warmup-accesses must be non-negative")
    if args.trace_memory_path_max_records < 0:
        raise ValueError("--trace-memory-path-max-records must be non-negative")
    if args.l2_source_tile_width <= 0:
        raise ValueError("--l2-source-tile-width must be greater than 0")

    print(f"Using common flags: {shlex.join(common_flags)}")
    print(f"Targets: {','.join(selected_targets(args))}")
    print(f"Mechanisms: {','.join(selected_mechanisms(args))}")
    if args.photon:
        photon_defaults = selected_global_photon_flags(args)
        append_default_photon_tuning_flags(args, photon_defaults)
        print(f"Global Photon flags: {shlex.join(photon_defaults)}")
    if args.timeout_minutes > 0:
        print(f"Experiment timeout: {args.timeout_minutes} minutes")
    print(f"Queued {len(exps)} experiments")
    if args.dry_run:
        dry_run_commands(exps)
        return

    if args.skip_build:
        print("Skipping target build (--skip-build).")
    else:
        build_targets(exps)
    memory_gated_run(exps, args)


if __name__ == "__main__":
    try:
        main()
    except ValueError as err:
        raise SystemExit(f"error: {err}")
