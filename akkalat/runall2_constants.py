import os

ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
TARGETS = [
    "baseline",
]

# These timing simulations are heavy. Keep parallelism conservative unless
# you're sure the machine can handle more concurrent runs.
MAX_WORKERS = 4
DEFAULT_L1V_MSHR_ENTRIES = 16
DEFAULT_L1V_MAX_CONCURRENT_TRANS = 16

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
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
    # "stencil2d",
    # "atax",
    # "bicg",
    # "matrixtranspose-middletile",
    # "nbody",
    # "nw",
    # "matrixmultiplication-middletile",
    # "conv2d",
    # "maxpooling",
    # "avgpooling",
    # "fulllayer",
]

# The paper reports all 14 traditional workloads. Keep the historical alias so
# older commands continue to parse, but never silently drop SPMV from formal
# coverage or the geomean.
TRADITIONAL_PRIMARY_BENCHMARKS = TRADITIONAL_BENCHMARKS[:]

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

# Fast end-to-end validation of the same MM kernel, address mapping, RDMA,
# L2, and DRAM paths used by the traditional workload. This is diagnostic
# only and is never included in the 14-workload paper geomean.
MECHANISM_SMOKE_BENCHMARKS = [
    "aes-pipeline-smoke",
    "kmeans-reuse-smoke",
    "matrixmultiplication-pipeline-smoke",
    "matrixtranspose-pipeline-smoke",
    "pagerank-pipeline-smoke",
]

ALL_BENCHMARKS = list(dict.fromkeys(
    TRADITIONAL_BENCHMARKS
    + LLM_BENCHMARKS
    + LLM_LIKE_BOTTLENECK_BENCHMARKS
    + EXPERIMENTAL_BENCHMARKS
    + MECHANISM_SMOKE_BENCHMARKS
))

BENCHMARK_ALIASES = {
    "all": ALL_BENCHMARKS,
    "default": DEFAULT_RUN_BENCHMARKS,
    "experimental": EXPERIMENTAL_BENCHMARKS,
    "llm-like-bottleneck": LLM_LIKE_BOTTLENECK_BENCHMARKS,
    "mechanism-smoke": MECHANISM_SMOKE_BENCHMARKS,
    "traditional": TRADITIONAL_BENCHMARKS,
    "traditional-primary": TRADITIONAL_PRIMARY_BENCHMARKS,
    "traditional-lite": TRADITIONAL_LITE_BENCHMARKS,
    "llm": LLM_BENCHMARKS,
}

BENCHMARKS_BY_TARGET = {
    "baseline": [
        "default"
    ],
}

DEFAULT_BENCHMARK_FLAGS = [
    # Keep the physical metadata design explicit in every formal or
    # diagnostic command. Individual configurations select cuckoo/exact/
    # disabled mode, but never silently change the ports or entry format.
    "-typed-filter-slots-per-bucket=4",
    "-typed-filter-fingerprint-bits=13",
    "-typed-filter-lookup-latency=1",
    "-typed-filter-lookup-width=16",
    "-typed-filter-update-latency=1",
    "-typed-filter-update-width=16",
    # One shared local predictor entry per aggregate L2 MSHR (4 x 64) in a
    # GPM. This is a single global hardware setting, never workload tuned.
    "-prefetch-predictor-entries=256",
]

BASE_COMMON_FLAGS = [
    "-timing",
    "-num-memory-banks=4",
    f"-l1v-mshr-entries={DEFAULT_L1V_MSHR_ENTRIES}",
    f"-l1v-max-concurrent-trans={DEFAULT_L1V_MAX_CONCURRENT_TRANS}",
    "-bandwidth=48",
    "-switch-latency=32",
    "-rdma-pipeline-width=8",
    "-rdma-pipeline-latency=10",
    "-rdma-max-outstanding=64",
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
# Respect --max-workers for sampled runs unless the user explicitly requests
# a lower cap with --sampled-parallel-limit.
DEFAULT_SAMPLED_PARALLEL_LIMIT = 0

BALANCED_SAMPLED_SWEEP_WARMUPS = [128, 512, 1024]
BALANCED_SAMPLED_SWEEP_GRANULARITIES = [512, 1024]
BALANCED_SAMPLED_THRESHOLD = 0.02
BALANCED_BRANCH_COVERAGE_THRESHOLD = 0.98
BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD = 0.005
BALANCED_KERNEL_DISTANCE_THRESHOLD = 8

CONFIGS = [
    ("baseline", []),
    (
        "dram_row_continuation",
        [
            "-dram-row-continuation-enable=true",
        ],
    ),
    (
        "local_cf",
        [
            "-l2-resident-filter-enable=true",
        ],
    ),
    (
        "predictor_only",
        ["-l2-prefetch-predictor-only=true"],
    ),
    (
        "ungated_prefetch",
        ["-l2-prefetch-ungated=true"],
    ),
    (
        "filter_coupled_prefetch",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-resident-filter-enable=true",
            "-l2-filter-prefetch-enable=true",
        ],
    ),
    (
        "filter_gated_prefetch_only",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-filter-prefetch-enable=true",
        ],
    ),
    (
        "exact_metadata_prefetch",
        [
            "-typed-filter-mode=exact",
            "-l2-resident-filter-enable=true",
            "-l2-filter-prefetch-enable=true",
        ],
    ),
    (
        "cuckoo_metadata_prefetch",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-resident-filter-enable=true",
            "-l2-filter-prefetch-enable=true",
        ],
    ),
    (
        "l2_fill_forwarding",
        [
            "-l2-fill-forwarding-enable=true",
        ],
    ),
    (
        "cf_fast_miss_only",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-resident-filter-enable=true",
        ],
    ),
    (
        "exact_metadata_m1",
        [
            "-typed-filter-mode=exact",
            "-l2-resident-filter-enable=true",
        ],
    ),
    (
        "transformations_no_filter",
        [
            "-typed-filter-mode=disabled",
            "-l2-resident-filter-enable=true",
            "-l2-fill-forwarding-enable=true",
            "-remote-data-path-enable=true",
            "-remote-data-path-dedup-enable=true",
            "-remote-data-path-batching-enable=true",
            "-remote-data-path-l2-enable=true",
        ],
    ),
    (
        "exact_metadata_transformations",
        [
            "-typed-filter-mode=exact",
            "-l2-resident-filter-enable=true",
            "-remote-data-path-enable=true",
            "-remote-data-path-dedup-enable=true",
            "-remote-data-path-batching-enable=true",
            "-remote-data-path-l2-enable=true",
        ],
    ),
    (
        "cuckoo_metadata_transformations",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-resident-filter-enable=true",
            "-remote-data-path-enable=true",
            "-remote-data-path-dedup-enable=true",
            "-remote-data-path-batching-enable=true",
            "-remote-data-path-l2-enable=true",
        ],
    ),
    (
        "cuckoo_filter_only",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-resident-filter-enable=true",
        ],
    ),
    (
        "m2",
        [
            "-typed-filter-mode=cuckoo",
            "-remote-data-path-enable=true",
            "-remote-data-path-dedup-enable=true",
            "-remote-data-path-batching-enable=true",
            "-remote-data-path-l2-enable=false",
            "-remote-filter-prefetch-enable=true",
        ],
    ),
    (
        "m3",
        [
            "-typed-filter-mode=cuckoo",
            "-remote-data-path-enable=true",
            "-remote-data-path-dedup-enable=false",
            "-remote-data-path-batching-enable=false",
            "-remote-data-path-l2-enable=true",
            "-remote-filter-prefetch-enable=false",
        ],
    ),
    (
        "complete_cupath",
        [
            "-typed-filter-mode=cuckoo",
            "-l2-resident-filter-enable=true",
            "-l2-filter-prefetch-enable=true",
            "-remote-data-path-enable=true",
            "-remote-data-path-dedup-enable=true",
            "-remote-data-path-batching-enable=true",
            "-remote-data-path-l2-enable=true",
            "-remote-filter-prefetch-enable=true",
        ],
    ),
    (
        "local_optimization",
        [
            "-l2-resident-filter-enable=true",
            "-l2-fill-forwarding-enable=true",
            "-dram-row-continuation-enable=true",
        ],
    ),
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

QUICK_BENCHMARKS = [
    "relu",
]

QUICK_CONFIGS = [
    "baseline",
    "sample_all",
    "sample_all_loop",
    "sample_wf",
    "sample_branch",
    "sample_kernel",
    "sample_loop",
]
