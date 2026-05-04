import argparse
import concurrent.futures
from datetime import datetime
import os
from pathlib import Path
import shlex
import subprocess

ROOT_DIR = os.path.dirname(os.path.abspath(__file__))
TARGETS = [
    "baseline",
]

# These timing simulations are heavy. Keep parallelism conservative unless
# you're sure the machine can handle more concurrent runs.
MAX_WORKERS = 18

ALL_BENCHMARKS = [
    "bitonicsort",
    "relu",
    "matrixmultiplication",
    "matrixtranspose",
    "kmeans",
    "spmv",
    "im2col",
    "aes",
    "fft",
    "floydwarshall",
    "pagerank",
    "simpleconvolution",
    "fastwalshtransform",
    "fir",
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

output_dir = ""


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
    return parser.parse_args()


def build_common_flags(args):
    return BASE_COMMON_FLAGS + [
        f"-mmutlb-lookup-latency={args.mmutlb_lookup_latency}",
    ]


def build_ablation_configs(args):
    return [
        (
            "baseline",
            [],
        ),
    ]


def get_benchmarks_for_target(target):
    selected = BENCHMARKS_BY_TARGET.get(target, ["all"])
    if "all" in selected:
        return ALL_BENCHMARKS[:]

    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks for {target}: {unknown}")

    return selected


def make_exps(ablation_configs):
    exps = []
    for target in TARGETS:
        for benchmark in get_benchmarks_for_target(target):
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
        )
        process.wait()

        end_time = datetime.now()
        out_file.write(f"Return code: {process.returncode}\n")
        out_file.write(f"End time: {end_time}\n")
        out_file.write(f"Elapsed time: {end_time - start_time}\n")

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
        datetime.now().strftime("%Y-%m-%d-%H-%M-%S-translation-sweep"),
    )

    results_dir = os.path.join(ROOT_DIR, "results")
    if not os.path.exists(results_dir):
        os.makedirs(results_dir)

    if not os.path.exists(output_dir):
        os.makedirs(output_dir)


def main():
    global output_dir

    args = parse_args()
    common_flags = build_common_flags(args)
    ablation_configs = build_ablation_configs(args)
    exps = make_exps(ablation_configs)
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

    build_targets(exps)

    if args.max_workers <= 0:
        raise ValueError("MAX_WORKERS must be greater than 0")

    max_workers = min(args.max_workers, len(exps))
    print(f"Using common flags: {shlex.join(common_flags)}")
    print(f"Launching {len(exps)} experiments with max_workers={max_workers}")
    with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
        for exp in exps:
            exp["common_flags"] = common_flags
        futures = [executor.submit(run_exp, exp) for exp in exps]
        for future in concurrent.futures.as_completed(futures):
            print(future.result())


if __name__ == "__main__":
    main()
