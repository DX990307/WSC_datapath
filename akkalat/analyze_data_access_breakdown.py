#!/usr/bin/env python3
"""Summarize data-access critical-path breakdown from memory-path traces."""

import argparse
import csv
from pathlib import Path


L1_CACHE_STAGES = {
    "l1v_coalesce_wait",
    "l1v_dir_lookup",
    "l1v_bank_hit",
    "l1v_mshr_wait",
    "l1v_bottom_response_parse",
    "l1v_mshr_wakeup",
    "l1v_fill_parent_done",
}

L2_CACHE_STAGES = {
    "l2_top_to_dir",
    "l2_dir_lookup",
    "l2_bank_hit",
    "l2_mshr_wait",
    "l2_writebuffer_wait",
    "l2_fill_and_response",
}

DRAM_STAGES = {
    "l2_bottom_send_to_dram",
    "dram_queue_and_service",
    "dram_to_l2_response",
}

NETWORK_STAGES = {
    "l1v_bottom_send_to_local_rdma",
    "local_rdma_request_output_wait",
    "local_rdma_to_remote_rdma_request",
    "cross_gpu_request_endpoint_queue",
    "cross_gpu_request_endpoint_inject_wait",
    "cross_gpu_request_channel_transfer",
    "cross_gpu_request_switch_input_queue",
    "cross_gpu_request_switch_pipeline",
    "cross_gpu_request_switch_route_wait",
    "cross_gpu_request_switch_arb_wait",
    "cross_gpu_request_switch_output_wait",
    "cross_gpu_request_endpoint_assemble_wait",
    "cross_gpu_request_endpoint_deliver_wait",
    "remote_rdma_request_output_wait",
    "remote_rdma_to_remote_l2",
    "remote_l2_to_remote_rdma_response",
    "remote_rdma_response_output_wait",
    "remote_rdma_to_local_rdma_response",
    "cross_gpu_return_endpoint_queue",
    "cross_gpu_return_endpoint_inject_wait",
    "cross_gpu_return_channel_transfer",
    "cross_gpu_return_switch_input_queue",
    "cross_gpu_return_switch_pipeline",
    "cross_gpu_return_switch_route_wait",
    "cross_gpu_return_switch_arb_wait",
    "cross_gpu_return_switch_output_wait",
    "cross_gpu_return_endpoint_assemble_wait",
    "cross_gpu_return_endpoint_deliver_wait",
    "local_rdma_response_output_wait",
    "local_rdma_to_l1v_response",
}

COARSE_REQUEST_STAGE = "local_rdma_to_remote_rdma_request"
COARSE_RETURN_STAGE = "remote_rdma_to_local_rdma_response"
FINE_REQUEST_PREFIX = "cross_gpu_request_"
FINE_RETURN_PREFIX = "cross_gpu_return_"

LOCAL_DATA_PATH_STAGES = {
    "l1v_bottom_send_to_local_l2",
    "local_l2_to_l1v_response",
}

ADDRESS_TRANSLATION_STAGES = {
    "at_to_l1v_top",
}

MECHANISMS = ["m1_m2", "baseline", "m1", "m2"]


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir",
        required=True,
        help="Directory containing *_memory_path_l1v_path_stage_summary.csv files.",
    )
    parser.add_argument(
        "--output-prefix",
        default="",
        help="Output prefix. Defaults to <results-dir>/data_access_breakdown.",
    )
    parser.add_argument(
        "--include-address-translation",
        action="store_true",
        help="Include at_to_l1v_top in the percentage denominator.",
    )
    return parser.parse_args()


def category_for_stage(stage):
    if stage in L1_CACHE_STAGES:
        return "l1_cache"
    if stage in L2_CACHE_STAGES:
        return "l2_cache"
    if stage in DRAM_STAGES:
        return "dram"
    if stage in NETWORK_STAGES:
        return "network"
    if stage in LOCAL_DATA_PATH_STAGES:
        return "local_data_path"
    if stage in ADDRESS_TRANSLATION_STAGES:
        return "address_translation"
    return "other_data_path"


def parse_experiment_name(path):
    name = path.name
    for suffix in (
        "_metrics_memory_path_l1v_path_stage_summary.csv",
        "_memory_path_l1v_path_stage_summary.csv",
    ):
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
                "config": f"{mechanism}_{config}",
            }

    return {
        "experiment": name,
        "target": target,
        "benchmark": rest,
        "mechanism": "",
        "config": "",
    }


def read_stage_summary(path):
    rows = []
    with path.open(newline="") as f:
        reader = csv.DictReader(f)
        for row in reader:
            stage = row["stage"]
            rows.append(
                {
                    "stage": stage,
                    "category": category_for_stage(stage),
                    "paths": int(row["paths"]),
                    "hops": int(row["hops"]),
                    "avg_latency_ns": int(row["avg_latency_ns"]),
                    "total_latency_ns": int(row["total_latency_ns"]),
                }
            )
    return rows


def fine_cross_gpu_present(stage_rows, prefix):
    return any(
        row["stage"].startswith(prefix) and row["total_latency_ns"] > 0
        for row in stage_rows
    )


def stage_included_in_data_access(
    stage, include_address_translation, has_fine_request, has_fine_return
):
    if stage in ADDRESS_TRANSLATION_STAGES and not include_address_translation:
        return False
    if stage == COARSE_REQUEST_STAGE and has_fine_request:
        return False
    if stage == COARSE_RETURN_STAGE and has_fine_return:
        return False
    return True


def aggregate_categories(
    stage_rows,
    include_address_translation,
    has_fine_request,
    has_fine_return,
):
    totals = {}
    for row in stage_rows:
        category = row["category"]
        if not stage_included_in_data_access(
            row["stage"],
            include_address_translation,
            has_fine_request,
            has_fine_return,
        ):
            continue
        totals[category] = totals.get(category, 0) + row["total_latency_ns"]

    denominator = sum(totals.values())
    out = []
    for category in sorted(totals):
        total = totals[category]
        pct = (100.0 * total / denominator) if denominator else 0.0
        out.append(
            {
                "category": category,
                "total_latency_ns": total,
                "pct_data_access": pct,
            }
        )
    return out, denominator


def write_csv(path, rows, fieldnames):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for row in rows:
            writer.writerow(row)


def write_markdown(path, category_rows):
    by_experiment = {}
    for row in category_rows:
        by_experiment.setdefault(row["experiment"], []).append(row)

    lines = [
        "# Data Access Critical Path Breakdown",
        "",
        "Percentages exclude address translation by default.",
        "",
    ]
    for experiment in sorted(by_experiment):
        rows = sorted(
            by_experiment[experiment],
            key=lambda r: r["total_latency_ns"],
            reverse=True,
        )
        meta = rows[0]
        lines += [
            f"## {experiment}",
            "",
            (
                f"benchmark=`{meta['benchmark']}`, "
                f"mechanism=`{meta['mechanism'] or 'unknown'}`"
            ),
            "",
            "| category | total latency ns | percent |",
            "|---|---:|---:|",
        ]
        for row in rows:
            lines.append(
                "| {category} | {total_latency_ns} | {pct_data_access:.2f}% |".format(
                    **row
                )
            )
        lines.append("")

    path.write_text("\n".join(lines), encoding="utf-8")


def main():
    args = parse_args()
    results_dir = Path(args.results_dir)
    output_prefix = (
        Path(args.output_prefix)
        if args.output_prefix
        else results_dir / "data_access_breakdown"
    )

    files = sorted(
        results_dir.glob("*_memory_path_l1v_path_stage_summary.csv")
    )
    if not files:
        raise SystemExit(
            f"no *_memory_path_l1v_path_stage_summary.csv files in {results_dir}"
        )

    category_rows = []
    stage_rows_out = []
    for path in files:
        meta = parse_experiment_name(path)
        stage_rows = read_stage_summary(path)
        has_fine_request = fine_cross_gpu_present(
            stage_rows, FINE_REQUEST_PREFIX)
        has_fine_return = fine_cross_gpu_present(
            stage_rows, FINE_RETURN_PREFIX)
        categories, data_access_total = aggregate_categories(
            stage_rows,
            args.include_address_translation,
            has_fine_request,
            has_fine_return,
        )

        for row in categories:
            category_rows.append(
                {
                    **meta,
                    **row,
                    "data_access_total_latency_ns": data_access_total,
                }
            )

        for row in stage_rows:
            included = stage_included_in_data_access(
                row["stage"],
                args.include_address_translation,
                has_fine_request,
                has_fine_return,
            )
            denominator = data_access_total
            pct = (
                100.0 * row["total_latency_ns"] / denominator
                if included and denominator
                else 0.0
            )
            stage_rows_out.append(
                {
                    **meta,
                    **row,
                    "included_in_data_access": int(included),
                    "pct_data_access": pct,
                    "data_access_total_latency_ns": data_access_total,
                }
            )

    write_csv(
        output_prefix.with_suffix(".csv"),
        category_rows,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "category",
            "total_latency_ns",
            "pct_data_access",
            "data_access_total_latency_ns",
        ],
    )
    write_csv(
        Path(str(output_prefix) + "_stages.csv"),
        stage_rows_out,
        [
            "experiment",
            "target",
            "benchmark",
            "mechanism",
            "config",
            "stage",
            "category",
            "paths",
            "hops",
            "avg_latency_ns",
            "total_latency_ns",
            "included_in_data_access",
            "pct_data_access",
            "data_access_total_latency_ns",
        ],
    )
    write_markdown(output_prefix.with_suffix(".md"), category_rows)

    print(f"Wrote {output_prefix.with_suffix('.csv')}")
    print(f"Wrote {Path(str(output_prefix) + '_stages.csv')}")
    print(f"Wrote {output_prefix.with_suffix('.md')}")


if __name__ == "__main__":
    main()
