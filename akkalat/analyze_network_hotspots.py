#!/usr/bin/env python3
"""Find switch/channel hotspots from memory-path network hop traces."""

import argparse
import csv
import gzip
import re
from collections import defaultdict
from pathlib import Path


NETWORK_PREFIXES = ("cross_gpu_request_", "cross_gpu_return_")
CHANNEL_STAGE = "channel_transfer"
SWITCH_STAGES = (
    "switch_input_queue",
    "switch_pipeline",
    "switch_route_wait",
    "switch_arb_wait",
    "switch_output_wait",
)
ENDPOINT_STAGES = (
    "endpoint_queue",
    "endpoint_inject_wait",
    "endpoint_assemble_wait",
    "endpoint_deliver_wait",
)
MECHANISMS = ("m1_m2", "baseline", "m1", "m2")
COMPONENT_RE = re.compile(r"(?:^| )component=([^ ]+)")


class Agg:
    def __init__(self):
        self.hops = 0
        self.paths = set()
        self.total_ns = 0.0

    def add(self, path_id, latency):
        self.hops += 1
        if path_id:
            self.paths.add(path_id)
        self.total_ns += latency

    @property
    def avg_ns(self):
        return self.total_ns / self.hops if self.hops else 0.0


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        required=True,
        help="Directory containing *_memory_path_l1v_path_hops_raw.csv.gz files.",
    )
    parser.add_argument(
        "--benchmarks",
        default="all",
        help="Comma-separated benchmark filter, or all.",
    )
    parser.add_argument(
        "--output-prefix",
        default="",
        help="Output prefix. Defaults to <results-dir>/network_hotspots.",
    )
    parser.add_argument(
        "--top-n",
        type=int,
        default=12,
        help="Number of top channels/switches to show in the markdown summary.",
    )
    return parser.parse_args()


def parse_filter(value):
    if value == "all":
        return None
    return {item.strip() for item in value.split(",") if item.strip()}


def parse_experiment_name(path):
    name = path.name
    suffixes = (
        "_metrics_memory_path_l1v_path_hops_raw.csv.gz",
        "_memory_path_l1v_path_hops_raw.csv.gz",
    )
    for suffix in suffixes:
        if name.endswith(suffix):
            name = name[: -len(suffix)]
            break

    target = ""
    rest = name
    if "_" in rest:
        target, rest = rest.split("_", 1)

    for mechanism in MECHANISMS:
        marker = f"_{mechanism}_"
        if marker in rest:
            benchmark, config = rest.split(marker, 1)
            return target, benchmark, mechanism, f"{mechanism}_{config}"

    if rest.endswith("_sample_all"):
        rest = rest[: -len("_sample_all")]
        config = "sample_all"
    else:
        config = ""
    return target, rest, "", config


def network_stage(segment):
    for prefix in NETWORK_PREFIXES:
        if segment.startswith(prefix):
            direction = "request" if prefix.endswith("request_") else "return"
            stage = segment[len(prefix) :]
            return direction, stage
    return "", ""


def latency(row):
    value = row.get("latency_ns", "0")
    return float(value) if value else 0.0


def note_component(row):
    match = COMPONENT_RE.search(row.get("notes", ""))
    return match.group(1) if match else ""


def channel_key(row):
    component = note_component(row)
    if component:
        return component
    src = row.get("from_port") or row.get("from_component")
    dst = row.get("to_port") or row.get("to_component")
    return f"{src}->{dst}"


def switch_key(row):
    component = note_component(row)
    if component:
        return component
    for key in ("from_component", "to_component"):
        value = row.get(key, "")
        if ".SW[" in value or value.startswith("Mesh.SW["):
            return value
    return row.get("from_component") or row.get("to_component") or "unknown"


def endpoint_key(row):
    component = note_component(row)
    if component:
        return component
    for key in ("from_component", "to_component"):
        value = row.get(key, "")
        if ".EP[" in value or value.startswith("Mesh.EP["):
            return value
    return row.get("from_component") or row.get("to_component") or "unknown"


def add(agg_map, key, path_id, ns):
    agg_map[key].add(path_id, ns)


def summarize_resource(meta_key, resource_map):
    rows = []
    grouped = defaultdict(list)
    for key, agg in resource_map.items():
        exp, benchmark, direction, resource_type, resource = key
        grouped[(exp, benchmark, direction, resource_type)].append((resource, agg))

    for (exp, benchmark, direction, resource_type), entries in sorted(grouped.items()):
        total = sum(agg.total_ns for _, agg in entries)
        avg = total / len(entries) if entries else 0.0
        top_resource, top_agg = max(entries, key=lambda item: item[1].total_ns)
        rows.append(
            {
                "experiment": exp,
                "benchmark": benchmark,
                "direction": direction,
                "resource_type": resource_type,
                "active_resources": len(entries),
                "total_latency_ns": round(total),
                "avg_resource_latency_ns": f"{avg:.3f}",
                "max_avg_ratio": f"{top_agg.total_ns / avg:.3f}" if avg else "0.000",
                "top_resource": top_resource,
                "top_resource_latency_ns": round(top_agg.total_ns),
                "top_resource_share_pct": f"{100 * top_agg.total_ns / total:.3f}"
                if total
                else "0.000",
            }
        )
    return rows


def read_traces(results_dir, benchmark_filter):
    stage_aggs = defaultdict(Agg)
    channel_aggs = defaultdict(Agg)
    switch_aggs = defaultdict(Agg)
    endpoint_aggs = defaultdict(Agg)
    detail_rows = []

    files = sorted(results_dir.glob("*_memory_path_l1v_path_hops_raw.csv.gz"))
    if not files:
        raise SystemExit(f"no hop raw files in {results_dir}")

    for path in files:
        target, benchmark, mechanism, config = parse_experiment_name(path)
        if benchmark_filter and benchmark not in benchmark_filter:
            continue
        experiment = path.name.split("_metrics_memory_path_l1v_path_hops_raw.csv.gz")[0]

        with gzip.open(path, "rt", newline="") as f:
            reader = csv.DictReader(f)
            for row in reader:
                direction, stage = network_stage(row.get("segment", ""))
                if not direction:
                    continue
                ns = latency(row)
                if ns <= 0:
                    continue
                path_id = row.get("path_id", "")
                add(stage_aggs, (experiment, benchmark, direction, stage), path_id, ns)

                if stage == CHANNEL_STAGE:
                    resource = channel_key(row)
                    key = (experiment, benchmark, direction, "channel", resource)
                    add(channel_aggs, key, path_id, ns)
                elif stage in SWITCH_STAGES:
                    resource = switch_key(row)
                    key = (experiment, benchmark, direction, "switch", resource)
                    add(switch_aggs, key, path_id, ns)
                elif stage in ENDPOINT_STAGES:
                    resource = endpoint_key(row)
                    key = (experiment, benchmark, direction, "endpoint", resource)
                    add(endpoint_aggs, key, path_id, ns)

    for key, agg in sorted(channel_aggs.items()):
        exp, benchmark, direction, resource_type, resource = key
        detail_rows.append(
            resource_row(exp, benchmark, direction, resource_type, resource, agg)
        )
    for key, agg in sorted(switch_aggs.items()):
        exp, benchmark, direction, resource_type, resource = key
        detail_rows.append(
            resource_row(exp, benchmark, direction, resource_type, resource, agg)
        )
    for key, agg in sorted(endpoint_aggs.items()):
        exp, benchmark, direction, resource_type, resource = key
        detail_rows.append(
            resource_row(exp, benchmark, direction, resource_type, resource, agg)
        )

    stage_rows = []
    for key, agg in sorted(stage_aggs.items()):
        exp, benchmark, direction, stage = key
        stage_rows.append(
            {
                "experiment": exp,
                "benchmark": benchmark,
                "direction": direction,
                "stage": stage,
                "hops": agg.hops,
                "paths": len(agg.paths),
                "total_latency_ns": round(agg.total_ns),
                "avg_latency_ns": f"{agg.avg_ns:.3f}",
            }
        )

    summary_rows = []
    summary_rows.extend(summarize_resource("channel", channel_aggs))
    summary_rows.extend(summarize_resource("switch", switch_aggs))
    summary_rows.extend(summarize_resource("endpoint", endpoint_aggs))

    return stage_rows, detail_rows, summary_rows


def resource_row(exp, benchmark, direction, resource_type, resource, agg):
    return {
        "experiment": exp,
        "benchmark": benchmark,
        "direction": direction,
        "resource_type": resource_type,
        "resource": resource,
        "hops": agg.hops,
        "paths": len(agg.paths),
        "total_latency_ns": round(agg.total_ns),
        "avg_latency_ns": f"{agg.avg_ns:.3f}",
    }


def write_csv(path, rows, fieldnames):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for row in rows:
            writer.writerow(row)


def write_markdown(path, stage_rows, detail_rows, summary_rows, top_n):
    stage_by_exp = defaultdict(list)
    for row in stage_rows:
        stage_by_exp[(row["experiment"], row["direction"])].append(row)

    detail_by_exp_type = defaultdict(list)
    for row in detail_rows:
        detail_by_exp_type[
            (row["experiment"], row["direction"], row["resource_type"])
        ].append(row)

    lines = ["# Network Hotspot Summary", ""]
    for row in sorted(
        summary_rows,
        key=lambda r: (
            r["benchmark"],
            r["direction"],
            r["resource_type"],
            -float(r["top_resource_share_pct"]),
        ),
    ):
        if row["resource_type"] not in ("channel", "switch"):
            continue
        lines.append(
            f"## {row['benchmark']} {row['direction']} {row['resource_type']}"
        )
        lines.append("")
        lines.append(
            f"active={row['active_resources']}, "
            f"max/avg={row['max_avg_ratio']}, "
            f"top_share={row['top_resource_share_pct']}%, "
            f"top={row['top_resource']}"
        )
        lines.append("")

        exp = row["experiment"]
        direction = row["direction"]
        rtype = row["resource_type"]
        details = sorted(
            detail_by_exp_type[(exp, direction, rtype)],
            key=lambda item: float(item["total_latency_ns"]),
            reverse=True,
        )[:top_n]
        lines.append("| resource | hops | paths | total ns | avg ns |")
        lines.append("|---|---:|---:|---:|---:|")
        for detail in details:
            lines.append(
                f"| {detail['resource']} | {detail['hops']} | "
                f"{detail['paths']} | {detail['total_latency_ns']} | "
                f"{detail['avg_latency_ns']} |"
            )
        lines.append("")
    path.write_text("\n".join(lines))


def main():
    args = parse_args()
    results_dir = Path(args.results_dir)
    output_prefix = (
        Path(args.output_prefix)
        if args.output_prefix
        else results_dir / "network_hotspots"
    )
    benchmark_filter = parse_filter(args.benchmarks)

    stage_rows, detail_rows, summary_rows = read_traces(results_dir, benchmark_filter)

    write_csv(
        output_prefix.with_name(output_prefix.name + "_stage_summary.csv"),
        stage_rows,
        [
            "experiment",
            "benchmark",
            "direction",
            "stage",
            "hops",
            "paths",
            "total_latency_ns",
            "avg_latency_ns",
        ],
    )
    write_csv(
        output_prefix.with_name(output_prefix.name + "_resources.csv"),
        detail_rows,
        [
            "experiment",
            "benchmark",
            "direction",
            "resource_type",
            "resource",
            "hops",
            "paths",
            "total_latency_ns",
            "avg_latency_ns",
        ],
    )
    write_csv(
        output_prefix.with_name(output_prefix.name + "_summary.csv"),
        summary_rows,
        [
            "experiment",
            "benchmark",
            "direction",
            "resource_type",
            "active_resources",
            "total_latency_ns",
            "avg_resource_latency_ns",
            "max_avg_ratio",
            "top_resource",
            "top_resource_latency_ns",
            "top_resource_share_pct",
        ],
    )
    write_markdown(
        output_prefix.with_suffix(".md"),
        stage_rows,
        detail_rows,
        summary_rows,
        args.top_n,
    )
    print(f"Wrote {output_prefix}_stage_summary.csv")
    print(f"Wrote {output_prefix}_resources.csv")
    print(f"Wrote {output_prefix}_summary.csv")
    print(f"Wrote {output_prefix}.md")


if __name__ == "__main__":
    main()
