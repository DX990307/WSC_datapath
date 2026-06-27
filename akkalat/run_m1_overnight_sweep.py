#!/usr/bin/env python3
"""Run an overnight M1 baseline/reorder sweep and aggregate comparisons."""

from __future__ import annotations

import argparse
import atexit
import concurrent.futures
import csv
from dataclasses import dataclass
from datetime import datetime
from math import exp, log
import os
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import threading
import time

from runall2_constants import ALL_BENCHMARKS, BENCHMARK_ALIASES


ROOT_DIR = Path(__file__).resolve().parent
REPO_ROOT = ROOT_DIR.parent
TRACE_SUFFIX = "_memory_path_l1v_path_summary.csv"
METRICS_SUFFIX = "_metrics.csv"
UNICODE_DASH_TRANSLATION = str.maketrans({
    "\u2013": "-",
    "\u2014": "-",
    "\u2212": "-",
})
RUNNING_PROCESSES = set()
RUNNING_PROCESSES_LOCK = threading.Lock()


def register_process(process: subprocess.Popen) -> None:
    with RUNNING_PROCESSES_LOCK:
        RUNNING_PROCESSES.add(process)


def unregister_process(process: subprocess.Popen) -> None:
    with RUNNING_PROCESSES_LOCK:
        RUNNING_PROCESSES.discard(process)


def terminate_process(process: subprocess.Popen, grace_seconds: int = 30) -> None:
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


def terminate_all_processes() -> None:
    with RUNNING_PROCESSES_LOCK:
        processes = list(RUNNING_PROCESSES)

    for process in processes:
        terminate_process(process)


def install_signal_handlers() -> None:
    def handle_signal(signum: int, _frame: object) -> None:
        print(
            f"Received signal {signum}; terminating running M1 jobs.",
            flush=True,
        )
        terminate_all_processes()
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)


def tracked_subprocess_call(
    cmd: list[str],
    cwd: Path,
    env: dict[str, str] | None = None,
) -> int:
    process = subprocess.Popen(
        cmd,
        cwd=cwd,
        env=env,
        start_new_session=True,
    )
    register_process(process)
    try:
        return process.wait()
    finally:
        unregister_process(process)


def normalize_option_dashes(argv: list[str]) -> list[str]:
    normalized = []
    for arg in argv:
        if not arg.startswith(("-", "\u2013", "\u2014", "\u2212")):
            normalized.append(arg)
            continue
        if "=" in arg:
            name, value = arg.split("=", 1)
            normalized.append(f"{name.translate(UNICODE_DASH_TRANSLATION)}={value}")
        else:
            normalized.append(arg.translate(UNICODE_DASH_TRANSLATION))
    return normalized


@dataclass(frozen=True)
class Experiment:
    policy: str
    window: int
    max_age_ns: int

    @property
    def name(self) -> str:
        return f"{self.policy}_w{self.window}_age{self.max_age_ns}"


@dataclass(frozen=True)
class Job:
    title: str
    benchmark: str
    out_dir: Path
    cmd: list[str]


def parse_csv(text: str) -> list[str]:
    return [item.strip() for item in text.split(",") if item.strip()]


def parse_int_csv(text: str) -> list[int]:
    if text.strip() == "":
        return []
    return [int(item.strip()) for item in text.split(",") if item.strip()]


def unique_preserving_order(items: list[str]) -> list[str]:
    seen = set()
    unique = []
    for item in items:
        if item in seen:
            continue
        seen.add(item)
        unique.append(item)
    return unique


def expand_benchmarks(selection: str) -> list[str]:
    expanded = []
    for item in parse_csv(selection):
        if item in BENCHMARK_ALIASES:
            expanded += BENCHMARK_ALIASES[item]
        else:
            expanded.append(item)
    expanded = unique_preserving_order(expanded)
    unknown = sorted(set(expanded) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks: {unknown}")
    return expanded


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Run baseline once, sweep simulator-side M1 reorder configs, "
            "compare each run, and aggregate datapath evidence."
        )
    )
    parser.add_argument("--benchmarks", default="traditional")
    parser.add_argument("--configs", default="baseline")
    parser.add_argument("--output-root", type=Path, default=None)
    parser.add_argument("--max-workers", type=int, default=1)
    parser.add_argument(
        "--schedule",
        choices=("by-config", "global"),
        default="by-config",
        help=(
            "by-config runs one full arm at a time. global schedules each "
            "(benchmark, arm) as an independent job and removes config-level barriers."
        ),
    )
    parser.add_argument(
        "--min-available-mem-gb",
        type=float,
        default=0,
        help=(
            "In global scheduling mode, wait before starting each simulator job "
            "until /proc/meminfo MemAvailable is at least this many GiB. "
            "0 disables the memory gate."
        ),
    )
    parser.add_argument(
        "--memory-check-interval-sec",
        type=float,
        default=60,
        help="Seconds to wait between memory-gate checks.",
    )
    parser.add_argument("--timeout-minutes", type=float, default=0)
    parser.add_argument(
        "--no-trace-memory-path",
        action="store_true",
        help=(
            "Do not collect memory-path trace CSVs. The sweep still records "
            "simulator metrics and compares total runtime from *_metrics.csv."
        ),
    )
    parser.add_argument("--warmup-accesses", type=int, default=600000)
    parser.add_argument("--max-records", type=int, default=1000000)
    parser.add_argument("--max-wg", type=int, default=78600)
    parser.add_argument(
        "--max-wg-multiplier",
        type=int,
        default=1,
        help=(
            "Multiply --max-wg before forwarding to runall2.py. "
            "Use 48 to interpret --max-wg as a per-active-GPU target."
        ),
    )
    parser.add_argument(
        "--run-until-max-wg",
        action="store_true",
        help=(
            "Do not stop when the memory-path trace reaches --max-records; "
            "let each benchmark run until --max-wg or natural completion."
        ),
    )
    parser.add_argument(
        "--enable-servers",
        "--enable-server",
        dest="enable_servers",
        action="store_true",
        help="Do not pass --disable-servers to runall2.py.",
    )
    parser.add_argument("--switch-latency", type=int, default=32)
    parser.add_argument("--mmutlb-lookup-latency", type=int, default=80)
    parser.add_argument("--l1v-mshr-entries", type=int, default=160)
    parser.add_argument("--l1v-max-concurrent-trans", type=int, default=160)
    parser.add_argument("--sampled-warmups", default="")
    parser.add_argument("--sampled-granularities", default="")
    parser.add_argument("--sampled-threshold", type=float, default=0)
    parser.add_argument("--balanced-sweep", action="store_true")
    parser.add_argument("--sampled-sweep", action="store_true")
    parser.add_argument("--photon-debug", action="store_true")
    parser.add_argument("--photon-verbose", action="store_true")
    parser.add_argument("--hlq-windows", default="16,32,64,128")
    parser.add_argument("--hlq-ages", default="100")
    parser.add_argument("--fifo-windows", default="64")
    parser.add_argument("--fifo-ages", default="100")
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--compare-only", action="store_true")
    parser.add_argument("--skip-compare", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser


def default_output_root() -> Path:
    stamp = datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
    return ROOT_DIR / "results" / f"{stamp}-m1-overnight-sweep"


def experiments(args: argparse.Namespace) -> list[Experiment]:
    specs: list[Experiment] = []
    for window in parse_int_csv(args.hlq_windows):
        for age in parse_int_csv(args.hlq_ages):
            specs.append(Experiment("hlq", window, age))
    for window in parse_int_csv(args.fifo_windows):
        for age in parse_int_csv(args.fifo_ages):
            specs.append(Experiment("fifo", window, age))
    return list(dict.fromkeys(specs))


def runall_base_cmd(
    args: argparse.Namespace,
    out_dir: Path,
    benchmarks: str | None = None,
    runall_workers: int | None = None,
    skip_build: bool = False,
) -> list[str]:
    forwarded_max_wg = args.max_wg * args.max_wg_multiplier
    cmd = [
        sys.executable,
        str(ROOT_DIR / "runall2.py"),
        "--benchmarks",
        benchmarks or args.benchmarks,
        "--configs",
        args.configs,
        "--output-dir",
        str(out_dir),
        "--max-workers",
        str(runall_workers or args.max_workers),
        "--switch-latency",
        str(args.switch_latency),
        "--mmutlb-lookup-latency",
        str(args.mmutlb_lookup_latency),
        "--max-wg",
        str(forwarded_max_wg),
        "--l1v-mshr-entries",
        str(args.l1v_mshr_entries),
        "--l1v-max-concurrent-trans",
        str(args.l1v_max_concurrent_trans),
    ]
    if not args.no_trace_memory_path:
        cmd += [
            "--trace-memory-path",
            "--trace-memory-path-warmup-accesses",
            str(args.warmup_accesses),
            "--trace-memory-path-max-records",
            str(args.max_records),
        ]
    if not args.enable_servers:
        cmd.append("--disable-servers")
    if not args.no_trace_memory_path and not args.run_until_max_wg:
        cmd.append("--trace-memory-path-exit-on-complete")
    if args.sampled_warmups:
        cmd += ["--sampled-warmups", args.sampled_warmups]
    if args.sampled_granularities:
        cmd += ["--sampled-granularities", args.sampled_granularities]
    if args.sampled_threshold > 0:
        cmd += ["--sampled-threshold", str(args.sampled_threshold)]
    if args.balanced_sweep:
        cmd.append("--balanced-sweep")
    if args.sampled_sweep:
        cmd.append("--sampled-sweep")
    if args.photon_debug:
        cmd.append("--photon-debug")
    if args.photon_verbose:
        cmd.append("--photon-verbose")
    if args.timeout_minutes > 0:
        cmd += ["--timeout-minutes", str(args.timeout_minutes)]
    if skip_build:
        cmd.append("--skip-build")
    return cmd


def experiment_cmd(
    args: argparse.Namespace,
    spec: Experiment,
    out_dir: Path,
    benchmarks: str | None = None,
    runall_workers: int | None = None,
    skip_build: bool = False,
) -> list[str]:
    cmd = runall_base_cmd(
        args,
        out_dir,
        benchmarks=benchmarks,
        runall_workers=runall_workers,
        skip_build=skip_build,
    )
    cmd += [
        "--l1v-bottom-reorder-policy",
        spec.policy,
        "--l1v-bottom-reorder-window",
        str(spec.window),
        "--l1v-bottom-reorder-max-age-ns",
        str(spec.max_age_ns),
    ]
    return cmd


def compare_cmd(
    baseline_dir: Path,
    experiment_dir: Path,
    out_dir: Path,
    metrics_only: bool = False,
) -> list[str]:
    cmd = [
        sys.executable,
        str(ROOT_DIR / "compare_m1_sim_datapath.py"),
        "--baseline-dir",
        str(baseline_dir),
        "--experiment-dir",
        str(experiment_dir),
        "--out-dir",
        str(out_dir),
    ]
    if metrics_only:
        cmd.append("--metrics-only")
    return cmd


def run_cmd(title: str, cmd: list[str], dry_run: bool) -> int:
    print(f"\n=== {title} ===", flush=True)
    print(shlex.join(cmd), flush=True)
    if dry_run:
        return 0
    return tracked_subprocess_call(cmd, cwd=REPO_ROOT)


def has_trace_output(path: Path) -> bool:
    return any(path.glob(f"*{TRACE_SUFFIX}"))


def has_benchmark_trace_output(path: Path, benchmark: str) -> bool:
    return any(path.glob(f"baseline_{benchmark}_*{TRACE_SUFFIX}"))


def has_metric_output(path: Path) -> bool:
    return any(path.glob(f"*{METRICS_SUFFIX}"))


def has_benchmark_metric_output(path: Path, benchmark: str) -> bool:
    return any(path.glob(f"baseline_{benchmark}_*{METRICS_SUFFIX}"))


def has_result_output(path: Path, args: argparse.Namespace) -> bool:
    if args.no_trace_memory_path:
        return has_metric_output(path)
    return has_trace_output(path)


def has_benchmark_result_output(
    path: Path,
    benchmark: str,
    args: argparse.Namespace,
) -> bool:
    if args.no_trace_memory_path:
        return has_benchmark_metric_output(path, benchmark)
    return has_benchmark_trace_output(path, benchmark)


def build_baseline_once(args: argparse.Namespace) -> int:
    if args.dry_run or args.compare_only:
        return 0

    env = os.environ.copy()
    env.setdefault("GOCACHE", "/tmp/gocache")
    target_dir = ROOT_DIR / "baseline"
    print(f"\n=== build baseline once ===", flush=True)
    print(f"go build -buildvcs=false  # cwd={target_dir}", flush=True)
    return tracked_subprocess_call(
        ["go", "build", "-buildvcs=false"],
        cwd=target_dir,
        env=env,
    )


def available_memory_gb() -> float | None:
    try:
        with Path("/proc/meminfo").open() as f:
            for line in f:
                if not line.startswith("MemAvailable:"):
                    continue
                parts = line.split()
                if len(parts) < 2:
                    return None
                return int(parts[1]) / 1024 / 1024
    except OSError:
        return None
    return None


def wait_for_memory_gate(
    title: str,
    min_available_gb: float,
    interval_sec: float,
) -> None:
    if min_available_gb <= 0:
        return

    interval_sec = max(interval_sec, 1.0)
    while True:
        available = available_memory_gb()
        if available is None:
            print(
                "[m1] memory gate requested but MemAvailable is unavailable; "
                f"starting {title}",
                flush=True,
            )
            return
        if available >= min_available_gb:
            print(
                f"[m1] memory gate ok for {title}: "
                f"MemAvailable={available:.1f}GiB >= {min_available_gb:.1f}GiB",
                flush=True,
            )
            return
        print(
            f"[m1] waiting to start {title}: "
            f"MemAvailable={available:.1f}GiB < {min_available_gb:.1f}GiB",
            flush=True,
        )
        time.sleep(interval_sec)


def maybe_run(
    title: str,
    cmd: list[str],
    out_dir: Path,
    args: argparse.Namespace,
) -> int:
    if args.compare_only:
        print(f"\n=== {title} ===", flush=True)
        print(f"[m1] compare-only: assuming existing output in {out_dir}", flush=True)
        return 0
    if args.resume and has_result_output(out_dir, args):
        print(f"\n=== {title} ===", flush=True)
        print(f"[m1] resume: found results in {out_dir}; skipping run", flush=True)
        return 0
    return run_cmd(title, cmd, args.dry_run)


def build_global_jobs(
    args: argparse.Namespace,
    output_root: Path,
    specs: list[Experiment],
) -> list[Job]:
    baseline_dir = output_root / "baseline"
    jobs: list[Job] = []
    for benchmark in expand_benchmarks(args.benchmarks):
        jobs.append(
            Job(
                title=f"baseline {benchmark}",
                benchmark=benchmark,
                out_dir=baseline_dir,
                cmd=runall_base_cmd(
                    args,
                    baseline_dir,
                    benchmarks=benchmark,
                    runall_workers=1,
                    skip_build=True,
                ),
            )
        )
        for spec in specs:
            exp_dir = output_root / spec.name
            jobs.append(
                Job(
                    title=f"{spec.name} {benchmark}",
                    benchmark=benchmark,
                    out_dir=exp_dir,
                    cmd=experiment_cmd(
                        args,
                        spec,
                        exp_dir,
                        benchmarks=benchmark,
                        runall_workers=1,
                        skip_build=True,
                    ),
                )
            )
    return jobs


def should_skip_job(args: argparse.Namespace, job: Job) -> bool:
    return args.resume and has_benchmark_result_output(
        job.out_dir,
        job.benchmark,
        args,
    )


def run_job(
    job: Job,
    dry_run: bool,
) -> tuple[str, int]:
    print(f"\n=== {job.title} ===", flush=True)
    print(shlex.join(job.cmd), flush=True)
    if dry_run:
        return job.title, 0
    return job.title, tracked_subprocess_call(job.cmd, cwd=REPO_ROOT)


def submit_global_job(
    executor: concurrent.futures.ThreadPoolExecutor,
    job: Job,
    args: argparse.Namespace,
) -> concurrent.futures.Future:
    wait_for_memory_gate(
        job.title,
        args.min_available_mem_gb,
        args.memory_check_interval_sec,
    )
    return executor.submit(run_job, job, False)


def pause_after_memory_gated_launch(args: argparse.Namespace) -> None:
    if args.min_available_mem_gb <= 0:
        return
    pause = max(args.memory_check_interval_sec, 1.0)
    print(
        f"[m1] memory gate: waiting {pause:.0f}s after launch before filling next job",
        flush=True,
    )
    time.sleep(pause)


def run_global_jobs(
    args: argparse.Namespace,
    output_root: Path,
    specs: list[Experiment],
) -> int:
    if args.compare_only:
        print("[m1] compare-only: skipping global job execution", flush=True)
        return 0

    jobs = [
        job for job in build_global_jobs(args, output_root, specs)
        if not should_skip_job(args, job)
    ]
    skipped = len(build_global_jobs(args, output_root, specs)) - len(jobs)
    if skipped:
        print(f"[m1] resume: skipped {skipped} completed benchmark jobs", flush=True)
    if not jobs:
        print("[m1] no global jobs to run", flush=True)
        return 0

    ret = build_baseline_once(args)
    if ret != 0:
        return ret

    workers = min(max(args.max_workers, 1), len(jobs))
    print(f"[m1] global scheduler: {len(jobs)} jobs, max_workers={workers}", flush=True)
    if args.min_available_mem_gb > 0:
        print(
            "[m1] memory gate: start a job only when "
            f"MemAvailable >= {args.min_available_mem_gb:.1f}GiB",
            flush=True,
        )
    if args.dry_run:
        for job in jobs:
            run_job(job, args.dry_run)
        return 0

    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
        futures: dict[concurrent.futures.Future, str] = {}
        failed = False

        next_job = 0

        def fill_workers() -> None:
            nonlocal next_job
            while next_job < len(jobs) and len(futures) < workers:
                job = jobs[next_job]
                next_job += 1
                future = submit_global_job(executor, job, args)
                futures[future] = job.title
                if next_job < len(jobs) and len(futures) < workers:
                    pause_after_memory_gated_launch(args)

        fill_workers()
        while futures:
            done, _ = concurrent.futures.wait(
                futures,
                return_when=concurrent.futures.FIRST_COMPLETED,
            )
            for future in done:
                futures.pop(future)
                title, ret = future.result()
                print(f"[m1] finished {title}: returncode={ret}", flush=True)
                if ret != 0:
                    failed = True
            fill_workers()
        return 1 if failed else 0


def read_csv(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    with path.open(newline="") as f:
        return list(csv.DictReader(f, skipinitialspace=True))


def write_csv(path: Path, fieldnames: list[str], rows: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def to_float(value: object) -> float | None:
    if value is None:
        return None
    text = str(value).strip()
    if text == "":
        return None
    return float(text)


def mean(values: list[float]) -> float | str:
    if not values:
        return ""
    return sum(values) / len(values)


def geomean(values: list[float]) -> float | str:
    positive = [value for value in values if value > 0]
    if not positive:
        return ""
    return exp(sum(log(value) for value in positive) / len(positive))


def minimum(values: list[float]) -> float | str:
    if not values:
        return ""
    return min(values)


def numeric_values(rows: list[dict[str, object]], field: str) -> list[float]:
    values = []
    for row in rows:
        value = to_float(row.get(field))
        if value is not None:
            values.append(value)
    return values


def aggregate_results(
    output_root: Path,
    specs: list[Experiment],
) -> None:
    all_rows: list[dict[str, object]] = []
    summary_rows: list[dict[str, object]] = []

    for spec in specs:
        compare_dir = output_root / "compare" / spec.name
        rows = read_csv(compare_dir / "m1_sim_datapath_comparison.csv")
        tagged_rows = []
        for row in rows:
            tagged = {
                "experiment": spec.name,
                "policy": spec.policy,
                "window": spec.window,
                "max_age_ns": spec.max_age_ns,
                **row,
            }
            tagged_rows.append(tagged)
            all_rows.append(tagged)

        speedups = numeric_values(tagged_rows, "total_time_speedup")
        avg_l1v_reductions = numeric_values(
            tagged_rows,
            "avg_total_l1v_path_latency_reduction_pct",
        )
        p95_l1v_reductions = numeric_values(
            tagged_rows,
            "p95_total_l1v_path_latency_reduction_pct",
        )
        remote_l1v_reductions = numeric_values(
            tagged_rows,
            "remote_avg_total_l1v_path_latency_reduction_pct",
        )
        total_wg_counts = numeric_values(tagged_rows, "experiment_total_wg_count")
        max_wg_reached = numeric_values(tagged_rows, "experiment_max_wg_reached")
        summary_rows.append(
            {
                "experiment": spec.name,
                "policy": spec.policy,
                "window": spec.window,
                "max_age_ns": spec.max_age_ns,
                "workloads": len(tagged_rows),
                "mean_total_time_speedup": mean(speedups),
                "geomean_total_time_speedup": geomean(speedups),
                "mean_avg_l1v_path_reduction_pct": mean(avg_l1v_reductions),
                "mean_p95_l1v_path_reduction_pct": mean(p95_l1v_reductions),
                "mean_remote_l1v_path_reduction_pct": mean(remote_l1v_reductions),
                "mean_experiment_total_wg_count": mean(total_wg_counts),
                "min_experiment_total_wg_count": minimum(total_wg_counts),
                "workloads_reached_max_wg": sum(1 for value in max_wg_reached if value >= 1),
            }
        )

    compare_root = output_root / "compare"
    if all_rows:
        fieldnames = list(all_rows[0].keys())
        write_csv(compare_root / "m1_overnight_all_workloads.csv", fieldnames, all_rows)

    summary_fields = [
        "experiment",
        "policy",
        "window",
        "max_age_ns",
        "workloads",
        "mean_total_time_speedup",
        "geomean_total_time_speedup",
        "mean_avg_l1v_path_reduction_pct",
        "mean_p95_l1v_path_reduction_pct",
        "mean_remote_l1v_path_reduction_pct",
        "mean_experiment_total_wg_count",
        "min_experiment_total_wg_count",
        "workloads_reached_max_wg",
    ]
    write_csv(compare_root / "m1_overnight_config_summary.csv", summary_fields, summary_rows)
    write_markdown_summary(compare_root / "m1_overnight_config_summary.md", summary_rows)
    print(f"[m1] wrote {compare_root / 'm1_overnight_config_summary.csv'}")
    print(f"[m1] wrote {compare_root / 'm1_overnight_config_summary.md'}")


def fmt_number(value: object, suffix: str = "") -> str:
    parsed = to_float(value)
    if parsed is None:
        return ""
    return f"{parsed:.4g}{suffix}"


def write_markdown_summary(
    path: Path,
    rows: list[dict[str, object]],
) -> None:
    ordered = sorted(
        rows,
        key=lambda row: to_float(row.get("geomean_total_time_speedup")) or 0.0,
        reverse=True,
    )
    with path.open("w") as f:
        f.write("# M1 Overnight Sweep Summary\n\n")
        f.write(
            "| experiment | workloads | geomean speedup | mean speedup | "
            "mean avg L1V reduction | mean p95 L1V reduction | "
            "mean remote L1V reduction | mean WG count | reached max-wg |\n"
        )
        f.write("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
        for row in ordered:
            f.write(
                "| {experiment} | {workloads} | {geomean} | {mean_speedup} | "
                "{avg_l1v} | {p95_l1v} | {remote_l1v} | {mean_wg} | "
                "{reached} |\n".format(
                    experiment=row["experiment"],
                    workloads=row["workloads"],
                    geomean=fmt_number(row["geomean_total_time_speedup"]),
                    mean_speedup=fmt_number(row["mean_total_time_speedup"]),
                    avg_l1v=fmt_number(
                        row["mean_avg_l1v_path_reduction_pct"], "%"
                    ),
                    p95_l1v=fmt_number(
                        row["mean_p95_l1v_path_reduction_pct"], "%"
                    ),
                    remote_l1v=fmt_number(
                        row["mean_remote_l1v_path_reduction_pct"], "%"
                    ),
                    mean_wg=fmt_number(row["mean_experiment_total_wg_count"]),
                    reached=row["workloads_reached_max_wg"],
                )
            )


def main() -> int:
    install_signal_handlers()
    atexit.register(terminate_all_processes)

    args = build_arg_parser().parse_args(normalize_option_dashes(sys.argv[1:]))
    output_root = (args.output_root or default_output_root()).resolve()
    baseline_dir = output_root / "baseline"
    specs = experiments(args)
    output_root.mkdir(parents=True, exist_ok=True)

    print(f"[m1] output root: {output_root}", flush=True)
    print(f"[m1] experiments: {', '.join(spec.name for spec in specs)}", flush=True)

    if args.schedule == "global":
        ret = run_global_jobs(args, output_root, specs)
        if ret != 0:
            return ret

        if args.skip_compare:
            return 0

        for spec in specs:
            exp_dir = output_root / spec.name
            ret = run_cmd(
                f"{spec.name} datapath comparison",
                compare_cmd(
                    baseline_dir,
                    exp_dir,
                    output_root / "compare" / spec.name,
                    metrics_only=args.no_trace_memory_path,
                ),
                args.dry_run,
            )
            if ret != 0:
                return ret

        if not args.dry_run:
            aggregate_results(output_root, specs)
        return 0

    ret = maybe_run(
        "baseline metric run" if args.no_trace_memory_path else "baseline trace run",
        runall_base_cmd(args, baseline_dir),
        baseline_dir,
        args,
    )
    if ret != 0:
        return ret

    for spec in specs:
        exp_dir = output_root / spec.name
        ret = maybe_run(
            (
                f"{spec.name} metric run"
                if args.no_trace_memory_path
                else f"{spec.name} trace run"
            ),
            experiment_cmd(args, spec, exp_dir),
            exp_dir,
            args,
        )
        if ret != 0:
            return ret

        if args.skip_compare:
            continue

        ret = run_cmd(
            f"{spec.name} datapath comparison",
            compare_cmd(
                baseline_dir,
                exp_dir,
                output_root / "compare" / spec.name,
                metrics_only=args.no_trace_memory_path,
            ),
            args.dry_run,
        )
        if ret != 0:
            return ret

    if not args.skip_compare and not args.dry_run:
        aggregate_results(output_root, specs)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
