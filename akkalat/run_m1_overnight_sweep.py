#!/usr/bin/env python3
"""Run an overnight M1 baseline/reorder sweep and aggregate comparisons."""

from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from datetime import datetime
from math import exp, log
from pathlib import Path
import shlex
import subprocess
import sys


ROOT_DIR = Path(__file__).resolve().parent
REPO_ROOT = ROOT_DIR.parent
TRACE_SUFFIX = "_memory_path_l1v_path_summary.csv"
UNICODE_DASH_TRANSLATION = str.maketrans({
    "\u2013": "-",
    "\u2014": "-",
    "\u2212": "-",
})


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


def parse_int_csv(text: str) -> list[int]:
    if text.strip() == "":
        return []
    return [int(item.strip()) for item in text.split(",") if item.strip()]


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
    parser.add_argument("--timeout-minutes", type=float, default=0)
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


def runall_base_cmd(args: argparse.Namespace, out_dir: Path) -> list[str]:
    forwarded_max_wg = args.max_wg * args.max_wg_multiplier
    cmd = [
        sys.executable,
        str(ROOT_DIR / "runall2.py"),
        "--benchmarks",
        args.benchmarks,
        "--configs",
        args.configs,
        "--output-dir",
        str(out_dir),
        "--max-workers",
        str(args.max_workers),
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
        "--trace-memory-path",
        "--trace-memory-path-warmup-accesses",
        str(args.warmup_accesses),
        "--trace-memory-path-max-records",
        str(args.max_records),
    ]
    if not args.enable_servers:
        cmd.append("--disable-servers")
    if not args.run_until_max_wg:
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
    return cmd


def experiment_cmd(
    args: argparse.Namespace,
    spec: Experiment,
    out_dir: Path,
) -> list[str]:
    cmd = runall_base_cmd(args, out_dir)
    cmd += [
        "--l1v-bottom-reorder-policy",
        spec.policy,
        "--l1v-bottom-reorder-window",
        str(spec.window),
        "--l1v-bottom-reorder-max-age-ns",
        str(spec.max_age_ns),
    ]
    return cmd


def compare_cmd(baseline_dir: Path, experiment_dir: Path, out_dir: Path) -> list[str]:
    return [
        sys.executable,
        str(ROOT_DIR / "compare_m1_sim_datapath.py"),
        "--baseline-dir",
        str(baseline_dir),
        "--experiment-dir",
        str(experiment_dir),
        "--out-dir",
        str(out_dir),
    ]


def run_cmd(title: str, cmd: list[str], dry_run: bool) -> int:
    print(f"\n=== {title} ===", flush=True)
    print(shlex.join(cmd), flush=True)
    if dry_run:
        return 0
    return subprocess.call(cmd, cwd=REPO_ROOT)


def has_trace_output(path: Path) -> bool:
    return any(path.glob(f"*{TRACE_SUFFIX}"))


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
    if args.resume and has_trace_output(out_dir):
        print(f"\n=== {title} ===", flush=True)
        print(f"[m1] resume: found traces in {out_dir}; skipping run", flush=True)
        return 0
    return run_cmd(title, cmd, args.dry_run)


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
    args = build_arg_parser().parse_args(normalize_option_dashes(sys.argv[1:]))
    output_root = (args.output_root or default_output_root()).resolve()
    baseline_dir = output_root / "baseline"
    specs = experiments(args)
    output_root.mkdir(parents=True, exist_ok=True)

    print(f"[m1] output root: {output_root}", flush=True)
    print(f"[m1] experiments: {', '.join(spec.name for spec in specs)}", flush=True)

    ret = maybe_run(
        "baseline trace run",
        runall_base_cmd(args, baseline_dir),
        baseline_dir,
        args,
    )
    if ret != 0:
        return ret

    for spec in specs:
        exp_dir = output_root / spec.name
        ret = maybe_run(
            f"{spec.name} trace run",
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
            compare_cmd(baseline_dir, exp_dir, output_root / "compare" / spec.name),
            args.dry_run,
        )
        if ret != 0:
            return ret

    if not args.skip_compare and not args.dry_run:
        aggregate_results(output_root, specs)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
