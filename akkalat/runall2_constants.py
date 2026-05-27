import os

ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
TARGETS = [
    "baseline",
]

# These timing simulations are heavy. Keep parallelism conservative unless
# you're sure the machine can handle more concurrent runs.
MAX_WORKERS = 15

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
    "atax",
    "bicg",
    "bitonicsort",
    "conv2d",
    "maxpooling",
    "avgpooling",
    "fulllayer",
    "fastwalshtransform",
    "fir",
    "fft",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixmultiplication-middletile",
    "matrixtranspose",
    "matrixtranspose-middletile",
    "nbody",
    "nw",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
    "stencil2d",
]

LLM_BENCHMARKS = [
    "bert",
    "gpt",
    "kvcache",
    "kvcache-decode",
    "kvcache-decode-30b",
    "resnet",
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
    TRADITIONAL_BENCHMARKS + LLM_BENCHMARKS + EXPERIMENTAL_BENCHMARKS
))

BENCHMARK_ALIASES = {
    "all": ALL_BENCHMARKS,
    "default": DEFAULT_RUN_BENCHMARKS,
    "experimental": EXPERIMENTAL_BENCHMARKS,
    "traditional": TRADITIONAL_BENCHMARKS,
    "traditional-lite": TRADITIONAL_LITE_BENCHMARKS,
    "llm": LLM_BENCHMARKS,
}

BENCHMARKS_BY_TARGET = {
    "baseline": [
        "default"
    ],
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
