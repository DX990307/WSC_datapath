#!/usr/bin/env python3
"""Summarize address-translation vs data-access critical-path time."""

import argparse
import csv
from pathlib import Path


MECHANISMS = ("m1_m2", "baseline", "m1", "m2", "m3")
CONFIG_SUFFIXES = ("sample_all_loop", "sample_all")
COMPONENTS = (
    "total_request",
    "address_translation",
    "address_translation_tlb",
    "at_to_l1v_top",
    "data_access_total",
)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        required=True,
        help="Directory containing *_critical_path_stage_summary.csv files.",
    )
    parser.add_argument(
        "--benchmark",
        default="relu",
        help="Benchmark to include. Use 'all' to include all benchmarks.",
    )
    parser.add_argument(
        "--output-prefix",
        help="Output prefix. Defaults to <results-dir>/critical_path_split.",
    )
    return parser.parse_args()


def parse_experiment_name(path):
    name = path.name
    suffixes = (
        "_metrics_memory_path_critical_path_stage_summary.csv",
        "_memory_path_critical_path_stage_summary.csv",
        "_critical_path_stage_summary.csv",
    )
    for suffix in suffixes:
        if name.endswith(suffix):
            name = name[: -len(suffix)]
            break

    target = ""
    rest = name
    if "_" in name:
        target, rest = name.split("_", 1)

    for mechanism in MECHANISMS:
        marker = f"_{mechanism}_"
        if marker in rest:
            benchmark, config = rest.split(marker, 1)
            return {
                "experiment": name,
                "target": target,
                "benchmark": benchmark,
                "mechanism": mechanism,
                "config": config,
            }

    for config in CONFIG_SUFFIXES:
        suffix = f"_{config}"
        if rest.endswith(suffix):
            benchmark = rest[: -len(suffix)]
            return {
                "experiment": name,
                "target": target,
                "benchmark": benchmark,
                "mechanism": "baseline",
                "config": config,
            }

    return {
        "experiment": name,
        "target": target,
        "benchmark": rest,
        "mechanism": "baseline" if target == "baseline" else "",
        "config": "",
    }


def read_components(path):
    components = {}
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            component = row["component"]
            if component not in COMPONENTS:
                continue
            components[component] = {
                "paths": int(row["paths"]),
                "remote_paths": int(row["remote_paths"]),
                "avg_latency_ns": int(row["avg_latency_ns"]),
                "p50_latency_ns": int(row["p50_latency_ns"]),
                "p90_latency_ns": int(row["p90_latency_ns"]),
                "p99_latency_ns": int(row["p99_latency_ns"]),
                "total_latency_ns": int(row["total_latency_ns"]),
                "avg_fraction_of_total_request": float(
                    row["avg_fraction_of_total_request"]
                ),
                "total_fraction_of_total_request": float(
                    row["total_fraction_of_total_request"]
                ),
            }
    return components


def pct(num, den):
    return 100.0 * num / den if den else 0.0


def summarize_file(path):
    meta = parse_experiment_name(path)
    components = read_components(path)
    total = components.get("total_request", {})
    addr = components.get("address_translation_tlb", {})
    at_to_l1v = components.get("at_to_l1v_top", {})
    data = components.get("data_access_total", {})

    total_avg = total.get("avg_latency_ns", 0)
    addr_avg = addr.get("avg_latency_ns", 0)
    at_to_l1v_avg = at_to_l1v.get("avg_latency_ns", 0)
    data_avg = data.get("avg_latency_ns", 0)
    total_sum = total.get("total_latency_ns", 0)
    addr_sum = addr.get("total_latency_ns", 0)
    at_to_l1v_sum = at_to_l1v.get("total_latency_ns", 0)
    data_sum = data.get("total_latency_ns", 0)
    user_total_avg = addr_avg + data_avg
    user_total_sum = addr_sum + data_sum

    return {
        **meta,
        "paths": total.get("paths", 0),
        "remote_paths": total.get("remote_paths", 0),
        "avg_total_request_ns": user_total_avg,
        "avg_address_translation_ns": addr_avg,
        "avg_at_to_l1v_top_ns_excluded": at_to_l1v_avg,
        "avg_data_access_ns": data_avg,
        "avg_address_translation_pct": pct(addr_avg, user_total_avg),
        "avg_data_access_pct": pct(data_avg, user_total_avg),
        "total_request_ns": user_total_sum,
        "total_address_translation_ns": addr_sum,
        "total_at_to_l1v_top_ns_excluded": at_to_l1v_sum,
        "total_data_access_ns": data_sum,
        "total_address_translation_pct": pct(addr_sum, user_total_sum),
        "total_data_access_pct": pct(data_sum, user_total_sum),
        "data_to_translation_avg_ratio": data_avg / addr_avg if addr_avg else 0.0,
        "trace_total_request_ns": total_sum,
    }


def write_csv(path, rows):
    fields = [
        "experiment",
        "target",
        "benchmark",
        "mechanism",
        "config",
        "paths",
        "remote_paths",
        "avg_total_request_ns",
        "avg_address_translation_ns",
        "avg_at_to_l1v_top_ns_excluded",
        "avg_data_access_ns",
        "avg_address_translation_pct",
        "avg_data_access_pct",
        "total_request_ns",
        "total_address_translation_ns",
        "total_at_to_l1v_top_ns_excluded",
        "total_data_access_ns",
        "total_address_translation_pct",
        "total_data_access_pct",
        "data_to_translation_avg_ratio",
        "trace_total_request_ns",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def write_markdown(path, rows):
    headers = [
        "benchmark",
        "mechanism",
        "config",
        "paths",
        "avg addr+data ns",
        "avg addr tlb ns",
        "avg data ns",
        "addr %",
        "data %",
    ]
    with path.open("w") as f:
        f.write("| " + " | ".join(headers) + " |\n")
        f.write("| " + " | ".join(["---"] * len(headers)) + " |\n")
        for row in rows:
            values = [
                row["benchmark"],
                row["mechanism"],
                row["config"],
                str(row["paths"]),
                str(row["avg_total_request_ns"]),
                str(row["avg_address_translation_ns"]),
                str(row["avg_data_access_ns"]),
                f"{row['avg_address_translation_pct']:.2f}",
                f"{row['avg_data_access_pct']:.2f}",
            ]
            f.write("| " + " | ".join(values) + " |\n")


def main():
    args = parse_args()
    results_dir = Path(args.results_dir)
    output_prefix = (
        Path(args.output_prefix)
        if args.output_prefix
        else results_dir / "critical_path_split"
    )

    files = sorted(results_dir.glob("*_critical_path_stage_summary.csv"))
    if not files:
        raise SystemExit(
            f"no *_critical_path_stage_summary.csv files in {results_dir}"
        )

    rows = []
    for path in files:
        row = summarize_file(path)
        if args.benchmark != "all" and row["benchmark"] != args.benchmark:
            continue
        rows.append(row)

    if not rows:
        raise SystemExit(f"no rows matched benchmark={args.benchmark}")

    write_csv(output_prefix.with_suffix(".csv"), rows)
    write_markdown(output_prefix.with_suffix(".md"), rows)
    print(f"Wrote {output_prefix.with_suffix('.csv')}")
    print(f"Wrote {output_prefix.with_suffix('.md')}")


if __name__ == "__main__":
    main()
