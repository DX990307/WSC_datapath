#!/usr/bin/env python3
"""Summarize decomposed BERT/GPT llmop results.

The decomposed runner emits one metrics CSV per transformer sub-op. This script
adds the per-op times back together, matching the spirit of the old DNN
sampledrunner "Sum" line.
"""

import argparse
import csv
from dataclasses import dataclass
from pathlib import Path


ROOT_DIR = Path(__file__).resolve().parent
RESULTS_DIR = ROOT_DIR / "results"
CONFIG_NAMES = [
    "sample_all_loop",
    "sample_branch",
    "sample_kernel",
    "sample_all",
    "sample_loop",
    "sample_wf",
    "llm_mixed",
    "baseline",
]


@dataclass
class ResultID:
    target: str
    benchmark: str
    model: str
    profile: str
    op_index: int
    op_name: str
    config: str


@dataclass
class MetricRecord:
    result_id: ResultID
    metrics_path: Path
    stdout_path: Path
    driver_kernel_time: float = 0.0
    driver_total_time: float = 0.0
    command_processor_kernel_time: float = 0.0
    max_command_processor_kernel_time: float = 0.0
    stdout_elapsed_seconds: float = 0.0
    return_code: str = ""


def parse_args():
    parser = argparse.ArgumentParser(
        description="Summarize decomposed llmop BERT/GPT results.")
    parser.add_argument(
        "results_dir",
        nargs="?",
        default="",
        help="Result directory. Defaults to latest *-sampled-validation.",
    )
    parser.add_argument(
        "--model",
        choices=["bert", "gpt"],
        default="",
        help="Only summarize one model.",
    )
    parser.add_argument(
        "--profile",
        default="",
        help="Only summarize one profile, e.g. tiny or middle.",
    )
    parser.add_argument(
        "--summary-output",
        default="llm_decomposed_summary.csv",
        help="Config-level summary CSV. Relative paths are under results_dir.",
    )
    parser.add_argument(
        "--per-op-output",
        default="llm_decomposed_per_op_summary.csv",
        help="Per-op summary CSV. Relative paths are under results_dir.",
    )
    parser.add_argument(
        "--show",
        action="store_true",
        help="Print the config-level summary table.",
    )
    return parser.parse_args()


def latest_results_dir():
    candidates = sorted(
        p for p in RESULTS_DIR.glob("*-sampled-validation") if p.is_dir()
    )
    if not candidates:
        raise FileNotFoundError(f"no sampled-validation results under {RESULTS_DIR}")
    return candidates[-1]


def resolve_results_dir(value):
    if value:
        path = Path(value)
        if not path.is_absolute():
            path = Path.cwd() / path
        return path.resolve()
    return latest_results_dir()


def output_path(results_dir, value):
    path = Path(value)
    if path.is_absolute():
        return path
    return results_dir / path


def parse_result_id(path):
    name = path.name
    suffix = "_metrics.csv"
    if not name.endswith(suffix):
        raise ValueError(f"not a metrics file: {name}")

    stem = name[: -len(suffix)]
    parts = stem.split("_")
    if len(parts) < 7:
        raise ValueError(f"cannot parse decomposed llmop filename: {name}")

    target = parts[0]
    benchmark = parts[1]
    model = parts[2]
    profile = parts[3]
    try:
        op_index = int(parts[4])
    except ValueError as err:
        raise ValueError(f"invalid op index in {name}") from err

    rest = "_".join(parts[5:])
    for config in CONFIG_NAMES:
        marker = "_" + config
        if rest.endswith(marker):
            op_name = rest[: -len(marker)]
            if not op_name:
                break
            return ResultID(
                target=target,
                benchmark=benchmark,
                model=model,
                profile=profile,
                op_index=op_index,
                op_name=op_name,
                config=config,
            )

    raise ValueError(f"cannot find config suffix in {name}")


def parse_metrics(path):
    driver_kernel_time = 0.0
    driver_total_time = 0.0
    cp_kernel_time = 0.0
    max_cp_kernel_time = 0.0

    with path.open(newline="") as f:
        reader = csv.reader(f, skipinitialspace=True)
        for row in reader:
            if len(row) < 4:
                continue

            where = row[1].strip()
            what = row[2].strip()
            try:
                value = float(row[3].strip())
            except ValueError:
                continue

            if where == "Driver" and what == "kernel_time":
                driver_kernel_time = value
            elif where == "Driver" and what == "total_time":
                driver_total_time = value
            elif where.endswith(".CommandProcessor") and what == "kernel_time":
                cp_kernel_time += value
                max_cp_kernel_time = max(max_cp_kernel_time, value)

    return (
        driver_kernel_time,
        driver_total_time,
        cp_kernel_time,
        max_cp_kernel_time,
    )


def elapsed_to_seconds(text):
    parts = text.strip().split(":")
    if len(parts) != 3:
        return 0.0
    hours, minutes, seconds = parts
    return int(hours) * 3600 + int(minutes) * 60 + float(seconds)


def parse_stdout(path):
    elapsed = 0.0
    return_code = ""
    if not path.exists():
        return elapsed, return_code

    with path.open(errors="replace") as f:
        for line in f:
            if line.startswith("Return code:"):
                return_code = line.split(":", 1)[1].strip()
            elif line.startswith("Elapsed time:"):
                elapsed = elapsed_to_seconds(line.split(":", 1)[1].strip())

    return elapsed, return_code


def matching_records(results_dir, model_filter, profile_filter):
    records = []
    skipped = []
    for metrics_path in sorted(results_dir.glob("*_llmop_*_metrics.csv")):
        try:
            result_id = parse_result_id(metrics_path)
        except ValueError as err:
            skipped.append((metrics_path.name, str(err)))
            continue

        if model_filter and result_id.model != model_filter:
            continue
        if profile_filter and result_id.profile != profile_filter:
            continue

        if result_id.benchmark != "llmop":
            continue

        stdout_path = metrics_path.with_name(
            metrics_path.name.replace("_metrics.csv", "_out.stdout"))
        metrics = parse_metrics(metrics_path)
        elapsed, return_code = parse_stdout(stdout_path)
        records.append(MetricRecord(
            result_id=result_id,
            metrics_path=metrics_path,
            stdout_path=stdout_path,
            driver_kernel_time=metrics[0],
            driver_total_time=metrics[1],
            command_processor_kernel_time=metrics[2],
            max_command_processor_kernel_time=metrics[3],
            stdout_elapsed_seconds=elapsed,
            return_code=return_code,
        ))

    return records, skipped


def write_per_op_csv(path, records):
    with path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([
            "model",
            "profile",
            "config",
            "op_index",
            "op_name",
            "driver_total_time",
            "driver_kernel_time",
            "command_processor_kernel_time_sum",
            "max_command_processor_kernel_time",
            "stdout_elapsed_seconds",
            "return_code",
            "metrics_file",
            "stdout_file",
        ])
        for record in records:
            rid = record.result_id
            writer.writerow([
                rid.model,
                rid.profile,
                rid.config,
                rid.op_index,
                rid.op_name,
                f"{record.driver_total_time:.12f}",
                f"{record.driver_kernel_time:.12f}",
                f"{record.command_processor_kernel_time:.12f}",
                f"{record.max_command_processor_kernel_time:.12f}",
                f"{record.stdout_elapsed_seconds:.6f}",
                record.return_code,
                record.metrics_path.name,
                record.stdout_path.name,
            ])


def summarize(records):
    groups = {}
    for record in records:
        rid = record.result_id
        key = (rid.model, rid.profile, rid.config)
        group = groups.setdefault(key, {
            "ops": 0,
            "driver_total_time": 0.0,
            "driver_kernel_time": 0.0,
            "command_processor_kernel_time": 0.0,
            "max_command_processor_kernel_time_sum": 0.0,
            "stdout_elapsed_seconds": 0.0,
            "ok": 0,
            "missing_return_code": 0,
        })
        group["ops"] += 1
        group["driver_total_time"] += record.driver_total_time
        group["driver_kernel_time"] += record.driver_kernel_time
        group["command_processor_kernel_time"] += (
            record.command_processor_kernel_time)
        group["max_command_processor_kernel_time_sum"] += (
            record.max_command_processor_kernel_time)
        group["stdout_elapsed_seconds"] += record.stdout_elapsed_seconds
        if record.return_code == "0":
            group["ok"] += 1
        elif record.return_code == "":
            group["missing_return_code"] += 1

    return groups


def write_summary_csv(path, groups):
    with path.open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow([
            "model",
            "profile",
            "config",
            "ops",
            "driver_total_time_sum",
            "driver_total_time_us",
            "driver_kernel_time_sum",
            "command_processor_kernel_time_sum",
            "command_processor_kernel_time_us",
            "max_command_processor_kernel_time_sum",
            "stdout_elapsed_seconds_sum",
            "return_code_0_count",
            "missing_return_code_count",
        ])
        for (model, profile, config), group in sorted(groups.items()):
            writer.writerow([
                model,
                profile,
                config,
                group["ops"],
                f"{group['driver_total_time']:.12f}",
                f"{group['driver_total_time'] * 1e6:.3f}",
                f"{group['driver_kernel_time']:.12f}",
                f"{group['command_processor_kernel_time']:.12f}",
                f"{group['command_processor_kernel_time'] * 1e6:.3f}",
                f"{group['max_command_processor_kernel_time_sum']:.12f}",
                f"{group['stdout_elapsed_seconds']:.6f}",
                group["ok"],
                group["missing_return_code"],
            ])


def print_summary(groups):
    print("model profile config ops driver_total_us cp_kernel_us stdout_elapsed_s ok")
    for (model, profile, config), group in sorted(groups.items()):
        print(
            f"{model} {profile} {config} {group['ops']} "
            f"{group['driver_total_time'] * 1e6:.3f} "
            f"{group['command_processor_kernel_time'] * 1e6:.3f} "
            f"{group['stdout_elapsed_seconds']:.3f} "
            f"{group['ok']}/{group['ops']}"
        )


def main():
    args = parse_args()
    results_dir = resolve_results_dir(args.results_dir)
    if not results_dir.is_dir():
        raise ValueError(f"results directory does not exist: {results_dir}")

    records, skipped = matching_records(results_dir, args.model, args.profile)
    if not records:
        raise RuntimeError(f"no decomposed llmop metrics found in {results_dir}")

    records.sort(key=lambda r: (
        r.result_id.model,
        r.result_id.profile,
        r.result_id.config,
        r.result_id.op_index,
        r.result_id.op_name,
    ))
    groups = summarize(records)

    summary_path = output_path(results_dir, args.summary_output)
    per_op_path = output_path(results_dir, args.per_op_output)
    write_summary_csv(summary_path, groups)
    write_per_op_csv(per_op_path, records)

    print(f"Results dir: {results_dir}")
    print(f"Wrote summary: {summary_path}")
    print(f"Wrote per-op summary: {per_op_path}")
    print(f"Matched metrics: {len(records)}")
    if skipped:
        print(f"Skipped files: {len(skipped)}")

    if args.show:
        print_summary(groups)


if __name__ == "__main__":
    main()
