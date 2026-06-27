#!/usr/bin/env python3
"""Compare baseline vs simulator-side M1 datapath evidence."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path
from statistics import mean


L1V_TRACE_FILE_SUFFIX = "_memory_path_l1v_path_summary.csv"
L1V_SUMMARY_SUFFIX = "_l1v_path_summary.csv"
MEMORY_PATH_SUFFIX = "_memory_path"

REQUEST_STAGE_GROUPS = {
    "l1v_mshr_wait_ns": ["l1v_mshr_wait_ns"],
    "l1v_to_local_l2_ns": ["l1v_bottom_send_to_local_l2_ns"],
    "l1v_to_local_rdma_ns": ["l1v_bottom_send_to_local_rdma_ns"],
    "local_rdma_request_queue_ns": ["local_rdma_request_output_wait_ns"],
    "remote_rdma_request_queue_ns": ["remote_rdma_request_output_wait_ns"],
    "dram_queue_and_service_ns": ["dram_queue_and_service_ns"],
    "local_rdma_response_queue_ns": ["local_rdma_response_output_wait_ns"],
    "cross_gpu_request_ns": [
        "local_rdma_to_remote_rdma_request_ns",
        "cross_gpu_request_endpoint_queue_ns",
        "cross_gpu_request_endpoint_inject_wait_ns",
        "cross_gpu_request_channel_transfer_ns",
        "cross_gpu_request_switch_input_queue_ns",
        "cross_gpu_request_switch_pipeline_ns",
        "cross_gpu_request_switch_route_wait_ns",
        "cross_gpu_request_switch_arb_wait_ns",
        "cross_gpu_request_switch_output_wait_ns",
        "cross_gpu_request_endpoint_assemble_wait_ns",
        "cross_gpu_request_endpoint_deliver_wait_ns",
    ],
    "cross_gpu_return_ns": [
        "remote_rdma_to_local_rdma_response_ns",
        "cross_gpu_return_endpoint_queue_ns",
        "cross_gpu_return_endpoint_inject_wait_ns",
        "cross_gpu_return_channel_transfer_ns",
        "cross_gpu_return_switch_input_queue_ns",
        "cross_gpu_return_switch_pipeline_ns",
        "cross_gpu_return_switch_route_wait_ns",
        "cross_gpu_return_switch_arb_wait_ns",
        "cross_gpu_return_switch_output_wait_ns",
        "cross_gpu_return_endpoint_assemble_wait_ns",
        "cross_gpu_return_endpoint_deliver_wait_ns",
    ],
}

SUMMARY_FIELDS = [
    "workload",
    "baseline_requests",
    "experiment_requests",
    "baseline_remote_ratio",
    "experiment_remote_ratio",
    "baseline_total_time_s",
    "experiment_total_time_s",
    "total_time_speedup",
    "baseline_avg_total_l1v_path_latency_ns",
    "experiment_avg_total_l1v_path_latency_ns",
    "avg_total_l1v_path_latency_reduction_pct",
    "baseline_p95_total_l1v_path_latency_ns",
    "experiment_p95_total_l1v_path_latency_ns",
    "p95_total_l1v_path_latency_reduction_pct",
    "baseline_remote_avg_total_l1v_path_latency_ns",
    "experiment_remote_avg_total_l1v_path_latency_ns",
    "remote_avg_total_l1v_path_latency_reduction_pct",
    "baseline_local_avg_total_l1v_path_latency_ns",
    "experiment_local_avg_total_l1v_path_latency_ns",
    "local_avg_total_l1v_path_latency_reduction_pct",
]

for metric in REQUEST_STAGE_GROUPS:
    SUMMARY_FIELDS += [
        f"baseline_avg_{metric}",
        f"experiment_avg_{metric}",
        f"{metric}_reduction_pct",
    ]

STAGE_FIELDS = [
    "workload",
    "stage",
    "baseline_avg_latency_ns",
    "experiment_avg_latency_ns",
    "avg_latency_delta_ns",
    "avg_latency_reduction_pct",
    "baseline_total_latency_ns",
    "experiment_total_latency_ns",
    "total_latency_delta_ns",
    "total_latency_reduction_pct",
]


def read_csv(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    with path.open(newline="") as f:
        return list(csv.DictReader(f, skipinitialspace=True))


def to_float(value: object, default: float = 0.0) -> float:
    if value is None:
        return default
    text = str(value).strip()
    if text == "":
        return default
    return float(text)


def to_bool(value: object) -> bool:
    return str(value).strip().lower() in {"1", "true", "t", "yes", "y"}


def pct_reduction(baseline: float, experiment: float) -> float | str:
    if baseline == 0:
        return ""
    return (baseline - experiment) / baseline * 100.0


def speedup(baseline: float, experiment: float) -> float | str:
    if experiment == 0:
        return ""
    return baseline / experiment


def fmt_number(value: object, suffix: str = "") -> str:
    if value is None or value == "":
        return ""
    return f"{to_float(value):.4g}{suffix}"


def percentile(values: list[float], pct: float) -> float | str:
    values = sorted(values)
    if not values:
        return ""
    if len(values) == 1:
        return values[0]
    rank = (len(values) - 1) * pct / 100.0
    lo = int(rank)
    hi = min(lo + 1, len(values) - 1)
    weight = rank - lo
    return values[lo] * (1.0 - weight) + values[hi] * weight


def avg(values: list[float]) -> float | str:
    if not values:
        return ""
    return mean(values)


def workload_key(path: Path) -> str:
    return experiment_prefix(path)


def trace_prefix(path: Path) -> str:
    name = path.name
    if name.endswith(L1V_SUMMARY_SUFFIX):
        return name[: -len(L1V_SUMMARY_SUFFIX)]
    return path.stem


def experiment_prefix(path: Path) -> str:
    prefix = trace_prefix(path)
    if prefix.endswith(MEMORY_PATH_SUFFIX):
        return prefix[: -len(MEMORY_PATH_SUFFIX)]
    return prefix


def metrics_path(trace_path: Path) -> Path:
    return trace_path.with_name(f"{experiment_prefix(trace_path)}_metrics.csv")


def stage_summary_path(trace_path: Path) -> Path:
    return trace_path.with_name(
        f"{trace_prefix(trace_path)}_l1v_path_stage_summary.csv"
    )


def find_l1v_traces(result_dir: Path) -> dict[str, Path]:
    return {
        workload_key(path): path
        for path in sorted(result_dir.glob(f"*{L1V_TRACE_FILE_SUFFIX}"))
    }


def driver_total_time(trace_path: Path) -> float | str:
    for row in read_csv(metrics_path(trace_path)):
        if row.get("where") == "Driver" and row.get("what") == "total_time":
            return to_float(row.get("value"))
    return ""


def request_values(rows: list[dict[str, str]], field: str) -> list[float]:
    return [to_float(row.get(field)) for row in rows if row.get(field, "") != ""]


def request_group_avg(rows: list[dict[str, str]], fields: list[str]) -> float | str:
    values = []
    for row in rows:
        values.append(sum(to_float(row.get(field)) for field in fields))
    return avg(values)


def request_stats(trace_path: Path) -> dict[str, object]:
    rows = read_csv(trace_path)
    total = len(rows)
    remote_rows = [row for row in rows if to_bool(row.get("is_remote"))]
    local_rows = [row for row in rows if not to_bool(row.get("is_remote"))]
    latencies = request_values(rows, "total_l1v_path_latency_ns")
    remote_latencies = request_values(remote_rows, "total_l1v_path_latency_ns")
    local_latencies = request_values(local_rows, "total_l1v_path_latency_ns")

    stats: dict[str, object] = {
        "requests": total,
        "remote_ratio": len(remote_rows) / total if total else "",
        "avg_total_l1v_path_latency_ns": avg(latencies),
        "p95_total_l1v_path_latency_ns": percentile(latencies, 95),
        "remote_avg_total_l1v_path_latency_ns": avg(remote_latencies),
        "local_avg_total_l1v_path_latency_ns": avg(local_latencies),
        "total_time_s": driver_total_time(trace_path),
    }
    for metric, fields in REQUEST_STAGE_GROUPS.items():
        stats[f"avg_{metric}"] = request_group_avg(rows, fields)
    return stats


def comparison_row(
    workload: str,
    baseline_path: Path,
    experiment_path: Path,
) -> dict[str, object]:
    baseline = request_stats(baseline_path)
    experiment = request_stats(experiment_path)

    row: dict[str, object] = {
        "workload": workload,
        "baseline_requests": baseline["requests"],
        "experiment_requests": experiment["requests"],
        "baseline_remote_ratio": baseline["remote_ratio"],
        "experiment_remote_ratio": experiment["remote_ratio"],
        "baseline_total_time_s": baseline["total_time_s"],
        "experiment_total_time_s": experiment["total_time_s"],
        "total_time_speedup": speedup(
            to_float(baseline["total_time_s"]),
            to_float(experiment["total_time_s"]),
        ),
        "baseline_avg_total_l1v_path_latency_ns": baseline[
            "avg_total_l1v_path_latency_ns"
        ],
        "experiment_avg_total_l1v_path_latency_ns": experiment[
            "avg_total_l1v_path_latency_ns"
        ],
        "avg_total_l1v_path_latency_reduction_pct": pct_reduction(
            to_float(baseline["avg_total_l1v_path_latency_ns"]),
            to_float(experiment["avg_total_l1v_path_latency_ns"]),
        ),
        "baseline_p95_total_l1v_path_latency_ns": baseline[
            "p95_total_l1v_path_latency_ns"
        ],
        "experiment_p95_total_l1v_path_latency_ns": experiment[
            "p95_total_l1v_path_latency_ns"
        ],
        "p95_total_l1v_path_latency_reduction_pct": pct_reduction(
            to_float(baseline["p95_total_l1v_path_latency_ns"]),
            to_float(experiment["p95_total_l1v_path_latency_ns"]),
        ),
        "baseline_remote_avg_total_l1v_path_latency_ns": baseline[
            "remote_avg_total_l1v_path_latency_ns"
        ],
        "experiment_remote_avg_total_l1v_path_latency_ns": experiment[
            "remote_avg_total_l1v_path_latency_ns"
        ],
        "remote_avg_total_l1v_path_latency_reduction_pct": pct_reduction(
            to_float(baseline["remote_avg_total_l1v_path_latency_ns"]),
            to_float(experiment["remote_avg_total_l1v_path_latency_ns"]),
        ),
        "baseline_local_avg_total_l1v_path_latency_ns": baseline[
            "local_avg_total_l1v_path_latency_ns"
        ],
        "experiment_local_avg_total_l1v_path_latency_ns": experiment[
            "local_avg_total_l1v_path_latency_ns"
        ],
        "local_avg_total_l1v_path_latency_reduction_pct": pct_reduction(
            to_float(baseline["local_avg_total_l1v_path_latency_ns"]),
            to_float(experiment["local_avg_total_l1v_path_latency_ns"]),
        ),
    }

    for metric in REQUEST_STAGE_GROUPS:
        baseline_value = to_float(baseline[f"avg_{metric}"])
        experiment_value = to_float(experiment[f"avg_{metric}"])
        row[f"baseline_avg_{metric}"] = baseline[f"avg_{metric}"]
        row[f"experiment_avg_{metric}"] = experiment[f"avg_{metric}"]
        row[f"{metric}_reduction_pct"] = pct_reduction(
            baseline_value,
            experiment_value,
        )

    return row


def stage_rows(
    workload: str,
    baseline_path: Path,
    experiment_path: Path,
) -> list[dict[str, object]]:
    baseline_stage = {
        row["stage"]: row
        for row in read_csv(stage_summary_path(baseline_path))
    }
    experiment_stage = {
        row["stage"]: row
        for row in read_csv(stage_summary_path(experiment_path))
    }

    rows = []
    for stage in sorted(set(baseline_stage) | set(experiment_stage)):
        b = baseline_stage.get(stage, {})
        e = experiment_stage.get(stage, {})
        b_avg = to_float(b.get("avg_latency_ns"))
        e_avg = to_float(e.get("avg_latency_ns"))
        b_total = to_float(b.get("total_latency_ns"))
        e_total = to_float(e.get("total_latency_ns"))
        rows.append(
            {
                "workload": workload,
                "stage": stage,
                "baseline_avg_latency_ns": b_avg,
                "experiment_avg_latency_ns": e_avg,
                "avg_latency_delta_ns": e_avg - b_avg,
                "avg_latency_reduction_pct": pct_reduction(b_avg, e_avg),
                "baseline_total_latency_ns": b_total,
                "experiment_total_latency_ns": e_total,
                "total_latency_delta_ns": e_total - b_total,
                "total_latency_reduction_pct": pct_reduction(b_total, e_total),
            }
        )
    return rows


def write_csv(path: Path, fieldnames: list[str], rows: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def write_markdown_summary(
    path: Path,
    summary_rows: list[dict[str, object]],
) -> None:
    with path.open("w") as f:
        f.write("# M1 Simulator Datapath Evidence\n\n")
        f.write("| workload | speedup | avg L1V path reduction | p95 L1V path reduction | remote avg reduction |\n")
        f.write("| --- | ---: | ---: | ---: | ---: |\n")
        for row in summary_rows:
            f.write(
                "| {workload} | {speedup} | {avg} | {p95} | {remote} |\n".format(
                    workload=row["workload"],
                    speedup=fmt_number(row["total_time_speedup"]),
                    avg=fmt_number(
                        row["avg_total_l1v_path_latency_reduction_pct"], "%"
                    ),
                    p95=fmt_number(
                        row["p95_total_l1v_path_latency_reduction_pct"], "%"
                    ),
                    remote=fmt_number(
                        row["remote_avg_total_l1v_path_latency_reduction_pct"], "%"
                    ),
                )
            )


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Compare baseline and simulator-side M1 datapath outputs."
    )
    parser.add_argument("--baseline-dir", type=Path, required=True)
    parser.add_argument("--experiment-dir", type=Path, required=True)
    parser.add_argument("--out-dir", type=Path, required=True)
    return parser


def main() -> None:
    args = build_arg_parser().parse_args()
    baseline = find_l1v_traces(args.baseline_dir)
    experiment = find_l1v_traces(args.experiment_dir)
    common = sorted(set(baseline) & set(experiment))
    if not common:
        raise SystemExit("no matching L1V path summary traces found")

    summary = [
        comparison_row(workload, baseline[workload], experiment[workload])
        for workload in common
    ]
    stages = []
    for workload in common:
        stages.extend(stage_rows(workload, baseline[workload], experiment[workload]))

    write_csv(args.out_dir / "m1_sim_datapath_comparison.csv", SUMMARY_FIELDS, summary)
    write_csv(args.out_dir / "m1_sim_stage_comparison.csv", STAGE_FIELDS, stages)
    write_markdown_summary(args.out_dir / "m1_sim_evidence_summary.md", summary)
    print(f"[m1] compared {len(common)} workloads")
    print(f"[m1] wrote {args.out_dir / 'm1_sim_datapath_comparison.csv'}")
    print(f"[m1] wrote {args.out_dir / 'm1_sim_stage_comparison.csv'}")
    print(f"[m1] wrote {args.out_dir / 'm1_sim_evidence_summary.md'}")


if __name__ == "__main__":
    main()
