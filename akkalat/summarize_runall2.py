import argparse
import csv
from datetime import datetime
from pathlib import Path


ROOT_DIR = Path(__file__).resolve().parent
RESULTS_DIR = ROOT_DIR / "results"

CONFIG_NAMES = [
    "sample_branch",
    "sample_kernel",
    "sample_all",
    "sample_wf",
    "baseline",
]

LOG_PATTERNS = {
    "photon_lines": "[Photon]",
    "wf_engine_enabled": "wf engine enabled",
    "branch_engine_enabled": "branch engine enabled",
    "kernel_collect_start": "kernel collect start",
    "branch_static_analysis": "branch static analysis complete",
    "sample_analysis": "sample analysis complete",
    "wf_sampled_enabled": "wf sampled enabled",
    "branch_bbl_solved": "branch bbl solved",
    "branch_level_start": "branch-level sampled start",
    "wf_skip": "wf sampled skip",
    "branch_skip": "branch sampled skip",
    "kernel_skip": "kernel sampled wf marked skip",
    "sampled_queued": "sampled wf queued",
    "sampled_completion": "sampled wf completion fired",
}

ERROR_HINT_PATTERNS = (
    "Panic:",
    "panic:",
    "fatal",
    "error:",
    "invalid time",
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        default="",
        help="Result directory to summarize. Defaults to the latest sampled-validation run.",
    )
    parser.add_argument(
        "--csv",
        default="",
        help="Optional CSV output path. Defaults to <results-dir>/summary.csv.",
    )
    parser.add_argument(
        "--md",
        default="",
        help="Optional Markdown output path. Defaults to <results-dir>/summary.md.",
    )
    parser.add_argument(
        "--show",
        action="store_true",
        help="Print the Markdown summary to stdout.",
    )
    parser.add_argument(
        "--scan-mode",
        choices=["fast", "full"],
        default="fast",
        help="fast scans log heads/tails; full scans entire logs for exact counts.",
    )
    parser.add_argument(
        "--head-mb",
        type=int,
        default=64,
        help="In fast mode, read this many MiB from the start of each log.",
    )
    parser.add_argument(
        "--tail-mb",
        type=int,
        default=16,
        help="In fast mode, read this many MiB from the end of each log.",
    )
    return parser.parse_args()


def latest_results_dir():
    candidates = sorted(
        p for p in RESULTS_DIR.glob("*-sampled-validation") if p.is_dir()
    )
    if not candidates:
        raise FileNotFoundError(f"no sampled-validation results under {RESULTS_DIR}")
    return candidates[-1]


def parse_stem(path):
    stem = path.name
    for suffix in ("_out.stdout", "_metrics.csv"):
        if stem.endswith(suffix):
            stem = stem[: -len(suffix)]
            break

    for config in CONFIG_NAMES:
        suffix = "_" + config
        if stem.endswith(suffix):
            prefix = stem[: -len(suffix)]
            target, benchmark = prefix.split("_", 1)
            return target, benchmark, config

        marker = "_" + config + "_"
        idx = stem.rfind(marker)
        if idx >= 0:
            prefix = stem[:idx]
            target, benchmark = prefix.split("_", 1)
            return target, benchmark, stem[idx + 1 :]

    raise ValueError(f"cannot parse result filename: {path.name}")


def empty_log_summary():
    summary = {name: 0 for name in LOG_PATTERNS}
    summary.update(
        {
            "log_exists": False,
            "return_code": "",
            "elapsed": "",
            "timed_out": False,
            "error_hint": "",
            "log_lines": 0,
            "log_size_mb": 0.0,
            "log_scanned_mb": 0.0,
            "log_truncated": False,
        }
    )
    return summary


def parse_status_line(summary, line):
    clean = line.strip()
    if line.startswith("Return code:"):
        summary["return_code"] = line.split(":", 1)[1].strip()
    elif line.startswith("Elapsed time:"):
        summary["elapsed"] = line.split(":", 1)[1].strip()
    elif "Timed out after" in line:
        summary["timed_out"] = True

    lower = clean.lower()
    if any(pattern.lower() in lower for pattern in ERROR_HINT_PATTERNS):
        if "Panic:" in clean or summary["error_hint"] == "":
            summary["error_hint"] = clean[:240]


def parse_log_full(path):
    summary = empty_log_summary()
    if not path.exists():
        return summary

    summary["log_exists"] = True
    stat = path.stat()
    summary["log_size_mb"] = stat.st_size / (1024 * 1024)
    summary["log_scanned_mb"] = summary["log_size_mb"]

    with path.open(errors="replace") as f:
        for line in f:
            summary["log_lines"] += 1
            for name, pattern in LOG_PATTERNS.items():
                if pattern in line:
                    summary[name] += 1

            parse_status_line(summary, line)

    return summary


def parse_log_fast(path, head_mb, tail_mb):
    summary = empty_log_summary()
    if not path.exists():
        return summary

    summary["log_exists"] = True
    stat = path.stat()
    size = stat.st_size
    head_bytes = max(0, head_mb) * 1024 * 1024
    tail_bytes = max(0, tail_mb) * 1024 * 1024
    summary["log_size_mb"] = size / (1024 * 1024)

    with path.open("rb") as f:
        if size <= head_bytes + tail_bytes:
            data = f.read()
        else:
            head = f.read(head_bytes)
            f.seek(max(0, size - tail_bytes))
            tail = f.read()
            data = head + b"\n" + tail
            summary["log_truncated"] = True

    summary["log_scanned_mb"] = len(data) / (1024 * 1024)
    summary["log_lines"] = data.count(b"\n")
    for name, pattern in LOG_PATTERNS.items():
        summary[name] = data.count(pattern.encode())

    text = data.decode(errors="replace")
    for line in text.splitlines():
        parse_status_line(summary, line)

    return summary


def parse_log(path, scan_mode, head_mb, tail_mb):
    if scan_mode == "full":
        return parse_log_full(path)
    return parse_log_fast(path, head_mb, tail_mb)


def metric_rows(path):
    if not path.exists():
        return []

    rows = []
    with path.open(newline="") as f:
        reader = csv.DictReader(f)
        for row in reader:
            rows.append({k.strip(): v.strip() for k, v in row.items()})
    return rows


def float_or_blank(value):
    if value == "":
        return ""
    try:
        return float(value)
    except ValueError:
        return ""


def parse_metrics(path):
    rows = metric_rows(path)
    summary = {
        "metrics_exists": path.exists(),
        "driver_total_time": "",
        "max_cp_kernel_time": "",
        "sum_cp_kernel_time": "",
        "iommu_req_count": "",
        "req_to_mmu_count": "",
        "lookup_latency_cycles": "",
    }
    if not rows:
        return summary

    cp_kernel_times = []
    for row in rows:
        where = row.get("where", "")
        what = row.get("what", "")
        value = row.get("value", "")

        if where == "Driver" and what == "total_time":
            summary["driver_total_time"] = float_or_blank(value)
        elif "CommandProcessor" in where and what == "kernel_time":
            parsed = float_or_blank(value)
            if parsed != "":
                cp_kernel_times.append(parsed)
        elif what in summary and summary[what] == "":
            summary[what] = float_or_blank(value)

    if cp_kernel_times:
        summary["max_cp_kernel_time"] = max(cp_kernel_times)
        summary["sum_cp_kernel_time"] = sum(cp_kernel_times)

    return summary


def collect_results(results_dir, scan_mode, head_mb, tail_mb):
    records = {}
    for log_path in results_dir.glob("*_out.stdout"):
        target, benchmark, config = parse_stem(log_path)
        key = (target, benchmark, config)
        records.setdefault(key, {})
        records[key]["target"] = target
        records[key]["benchmark"] = benchmark
        records[key]["config"] = config
        records[key].update(parse_log(log_path, scan_mode, head_mb, tail_mb))

    for metrics_path in results_dir.glob("*_metrics.csv"):
        target, benchmark, config = parse_stem(metrics_path)
        key = (target, benchmark, config)
        records.setdefault(key, {})
        records[key]["target"] = target
        records[key]["benchmark"] = benchmark
        records[key]["config"] = config
        records[key].update(parse_metrics(metrics_path))

    for record in records.values():
        record.setdefault("target", "")
        record.setdefault("benchmark", "")
        record.setdefault("config", "")
        for key, value in empty_log_summary().items():
            record.setdefault(key, value)
        for key, value in parse_metrics(Path("/missing")).items():
            record.setdefault(key, value)

    add_speedups(records.values())
    return sorted(
        records.values(),
        key=lambda r: (r["target"], r["benchmark"], config_rank(r["config"])),
    )


def config_rank(config):
    for idx, name in enumerate(CONFIG_NAMES):
        if config == name or config.startswith(name + "_"):
            return idx
    return 99


def add_speedups(records):
    baseline_by_benchmark = {}
    for record in records:
        if record["config"] != "baseline":
            continue
        total_time = record.get("driver_total_time", "")
        if isinstance(total_time, float) and total_time > 0:
            baseline_by_benchmark[
                (record["target"], record["benchmark"])
            ] = total_time

    for record in records:
        baseline = baseline_by_benchmark.get(
            (record["target"], record["benchmark"])
        )
        total_time = record.get("driver_total_time", "")
        if baseline and isinstance(total_time, float) and total_time > 0:
            record["speedup_vs_baseline"] = baseline / total_time
        else:
            record["speedup_vs_baseline"] = ""


def status(record):
    if record.get("timed_out"):
        return "timeout"
    return_code = record.get("return_code", "")
    if return_code == "0":
        return "ok"
    if return_code != "":
        return "failed"
    if record.get("metrics_exists"):
        return "metrics-only"
    if record.get("log_exists"):
        return "incomplete"
    return "missing"


def fmt(value, digits=4):
    if value == "":
        return ""
    if isinstance(value, float):
        return f"{value:.{digits}g}"
    return str(value)


def write_csv(records, path):
    fieldnames = [
        "target",
        "benchmark",
        "config",
        "status",
        "log_exists",
        "return_code",
        "elapsed",
        "error_hint",
        "driver_total_time",
        "speedup_vs_baseline",
        "max_cp_kernel_time",
        "sum_cp_kernel_time",
        "photon_lines",
        "wf_engine_enabled",
        "branch_engine_enabled",
        "kernel_collect_start",
        "branch_static_analysis",
        "sample_analysis",
        "wf_sampled_enabled",
        "branch_bbl_solved",
        "branch_level_start",
        "wf_skip",
        "branch_skip",
        "kernel_skip",
        "sampled_queued",
        "sampled_completion",
        "log_lines",
        "log_size_mb",
        "log_scanned_mb",
        "log_truncated",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for record in records:
            row = {name: record.get(name, "") for name in fieldnames}
            row["status"] = status(record)
            writer.writerow(row)


def markdown_table(records):
    headers = [
        "benchmark",
        "config",
        "status",
        "elapsed",
        "total_time",
        "speedup",
        "photon",
        "enabled",
        "analysis",
        "skip",
        "queued",
        "hint",
    ]
    rows = []
    for record in records:
        enabled = (
            record["wf_engine_enabled"]
            + record["branch_engine_enabled"]
            + record["wf_sampled_enabled"]
            + record["branch_level_start"]
            + record["kernel_collect_start"]
        )
        analysis = record["branch_static_analysis"] + record["sample_analysis"]
        skip = record["wf_skip"] + record["branch_skip"] + record["kernel_skip"]
        rows.append(
            [
                record["benchmark"],
                record["config"],
                status(record),
                record["elapsed"],
                fmt(record["driver_total_time"]),
                fmt(record["speedup_vs_baseline"], 3),
                str(record["photon_lines"]),
                str(enabled),
                str(analysis),
                str(skip),
                str(record["sampled_queued"]),
                record["error_hint"],
            ]
        )

    table = [
        "| " + " | ".join(headers) + " |",
        "| " + " | ".join(["---"] * len(headers)) + " |",
    ]
    for row in rows:
        escaped = [cell.replace("|", "\\|") for cell in row]
        table.append("| " + " | ".join(escaped) + " |")
    return "\n".join(table)


def markdown_summary(records, results_dir, args):
    ok = sum(1 for record in records if status(record) == "ok")
    failed = sum(1 for record in records if status(record) == "failed")
    timeout = sum(1 for record in records if status(record) == "timeout")
    incomplete = sum(1 for record in records if status(record) == "incomplete")
    metrics_only = sum(1 for record in records if status(record) == "metrics-only")
    missing = sum(1 for record in records if status(record) == "missing")

    lines = [
        f"# Run Summary: {results_dir.name}",
        "",
        f"Generated: {datetime.now()}",
        "",
        (
            f"Log scan: {args.scan_mode}"
            if args.scan_mode == "full"
            else f"Log scan: fast, first {args.head_mb} MiB + last {args.tail_mb} MiB"
        ),
        "",
        (
            f"Experiments: {len(records)} total, {ok} ok, "
            f"{failed} failed, {timeout} timeout, "
            f"{incomplete} incomplete, {metrics_only} metrics-only, {missing} missing"
        ),
        "",
        markdown_table(records),
        "",
        "Photon column notes:",
        "",
        "- photon: total lines containing [Photon]",
        "- enabled: engine/sample start signals",
        "- analysis: static/BBV analysis completions",
        "- skip: sampled wavefront skip events",
        "- queued: sampled completion events queued",
        "- fast mode keeps counts lightweight for multi-GB logs; use --scan-mode full for exact log counts",
        "",
    ]
    return "\n".join(lines)


def main():
    args = parse_args()
    results_dir = Path(args.results_dir).resolve() if args.results_dir else latest_results_dir()
    if not results_dir.is_dir():
        raise ValueError(f"results directory does not exist: {results_dir}")

    records = collect_results(results_dir, args.scan_mode, args.head_mb, args.tail_mb)
    csv_path = Path(args.csv).resolve() if args.csv else results_dir / "summary.csv"
    md_path = Path(args.md).resolve() if args.md else results_dir / "summary.md"

    write_csv(records, csv_path)
    summary = markdown_summary(records, results_dir, args)
    md_path.write_text(summary)

    print(f"Wrote {csv_path}")
    print(f"Wrote {md_path}")
    if args.show:
        print()
        print(summary)


if __name__ == "__main__":
    main()
