#!/usr/bin/env python3
"""Run Mechanism 1 offline reorder experiments on Akita memory-path traces."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path

from m1_metrics import (
    FRAGMENTATION_FIELDS,
    REQUEST_CLASSES,
    SUMMARY_FIELDS,
    WINDOW_FIELDS,
    fragmentation_rows,
    summary_row,
    window_metric_rows,
)
from m1_parse_trace import MappingConfig, find_l1v_summary_paths, load_requests
from m1_policies import make_policy, process_trace


def parse_csv_list(text: str) -> list[str]:
    return [item.strip() for item in text.split(",") if item.strip()]


def parse_int_list(text: str) -> list[int]:
    return [int(item) for item in parse_csv_list(text)]


def parse_age_list(text: str) -> list[int | None]:
    ages: list[int | None] = []
    for item in parse_csv_list(text):
        if item.lower() in {"none", "unlimited", "inf"}:
            ages.append(None)
        else:
            ages.append(int(item))
    return ages


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Run M1 FIFO/L2-only/DRAM-only/HLQ offline experiments."
    )
    parser.add_argument(
        "--result-dir",
        type=Path,
        required=True,
        help="Directory containing *_memory_path_l1v_path_summary.csv traces.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Output directory. Defaults to <result-dir>/m1.",
    )
    parser.add_argument(
        "--trace-file",
        type=Path,
        action="append",
        default=None,
        help="Explicit trace file. Can be passed multiple times.",
    )
    parser.add_argument(
        "--workloads",
        default="",
        help="Comma-separated workload filters. Empty means all traces in result-dir.",
    )
    parser.add_argument(
        "--policies",
        default="fifo,l2_only,dram_only,hlq",
        help="Comma-separated policies: fifo,l2_only,dram_only,hlq.",
    )
    parser.add_argument(
        "--window-sizes",
        default="16,32,64,160",
        help="Comma-separated finite reorder windows.",
    )
    parser.add_argument(
        "--max-ages",
        default="unlimited,50,100",
        help="Comma-separated max ages in ns; use unlimited for no age bound.",
    )
    parser.add_argument(
        "--max-requests",
        type=int,
        default=None,
        help="Limit requests per trace for smoke tests.",
    )
    parser.add_argument(
        "--max-files",
        type=int,
        default=None,
        help="Limit number of traces for smoke tests.",
    )
    parser.add_argument(
        "--issue-interval-ns",
        type=int,
        default=1,
        help="Offline issue interval used for queue-wait approximation.",
    )
    parser.add_argument("--num-l2-slices", type=int, default=49)
    parser.add_argument("--num-hbm-channels", type=int, default=8)
    parser.add_argument("--num-hbm-banks", type=int, default=16)
    parser.add_argument("--row-bytes", type=int, default=2048)
    return parser


def write_header(path: Path, fieldnames: list[str]) -> csv.DictWriter:
    path.parent.mkdir(parents=True, exist_ok=True)
    f = path.open("w", newline="")
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    writer._m1_file = f  # type: ignore[attr-defined]
    return writer


def close_writer(writer: csv.DictWriter) -> None:
    writer._m1_file.close()  # type: ignore[attr-defined]


def write_metadata(
    out_dir: Path,
    args: argparse.Namespace,
    trace_paths: list[Path],
    mapping: MappingConfig,
    policy_names: list[str],
    window_sizes: list[int],
    max_ages: list[int | None],
) -> None:
    metadata = {
        "experiment": "mechanism_1_hierarchical_locality_queue_offline",
        "approximate_address_mapping": True,
        "mapping_note": (
            "l2_slice/channel/bank/row are deterministic approximations derived "
            "from paddr; replace with simulator-accurate placement when exposed."
        ),
        "result_dir": str(args.result_dir),
        "trace_files": [str(path) for path in trace_paths],
        "policies": policy_names,
        "window_sizes": window_sizes,
        "max_ages_ns": [
            "unlimited" if age is None else age for age in max_ages
        ],
        "max_requests": args.max_requests,
        "issue_interval_ns": args.issue_interval_ns,
        "mapping": {
            "cacheline_bytes": mapping.cacheline_bytes,
            "page_bytes": mapping.page_bytes,
            "num_l2_slices": mapping.num_l2_slices,
            "num_hbm_channels": mapping.num_hbm_channels,
            "num_hbm_banks": mapping.num_hbm_banks,
            "row_bytes": mapping.row_bytes,
        },
    }
    with (out_dir / "m1_metadata.json").open("w") as f:
        json.dump(metadata, f, indent=2)
        f.write("\n")


def main() -> None:
    args = build_arg_parser().parse_args()
    out_dir = args.out_dir or args.result_dir / "m1"
    out_dir.mkdir(parents=True, exist_ok=True)

    workloads = parse_csv_list(args.workloads)
    policies = [make_policy(name) for name in parse_csv_list(args.policies)]
    window_sizes = parse_int_list(args.window_sizes)
    max_ages = parse_age_list(args.max_ages)
    mapping = MappingConfig(
        num_l2_slices=args.num_l2_slices,
        num_hbm_channels=args.num_hbm_channels,
        num_hbm_banks=args.num_hbm_banks,
        row_bytes=args.row_bytes,
    )

    if args.trace_file:
        trace_paths = sorted(args.trace_file)
    else:
        trace_paths = find_l1v_summary_paths(args.result_dir, workloads)
    if args.max_files is not None:
        trace_paths = trace_paths[: args.max_files]
    if not trace_paths:
        raise SystemExit(f"no L1V path summary traces found in {args.result_dir}")

    write_metadata(
        out_dir=out_dir,
        args=args,
        trace_paths=trace_paths,
        mapping=mapping,
        policy_names=[policy.name for policy in policies],
        window_sizes=window_sizes,
        max_ages=max_ages,
    )

    summary_writer = write_header(out_dir / "m1_summary.csv", SUMMARY_FIELDS)
    window_writer = write_header(out_dir / "m1_window_metrics.csv", WINDOW_FIELDS)
    frag_writer = write_header(out_dir / "m1_fragmentation.csv", FRAGMENTATION_FIELDS)

    try:
        for trace_path in trace_paths:
            requests = load_requests(trace_path, mapping, args.max_requests)
            if not requests:
                continue
            workload = requests[0].workload
            trace_name = requests[0].trace_name
            print(
                f"[m1] {trace_path.name}: {len(requests)} requests, "
                f"workload={workload}"
            )

            for window_size in window_sizes:
                for row in fragmentation_rows(
                    workload=workload,
                    trace_name=trace_name,
                    requests=requests,
                    window_size=window_size,
                ):
                    frag_writer.writerow(row)

            for policy in policies:
                for window_size in window_sizes:
                    for max_age_ns in max_ages:
                        issued = process_trace(
                            requests=requests,
                            policy=policy,
                            window_size=window_size,
                            max_age_ns=max_age_ns,
                            issue_interval_ns=args.issue_interval_ns,
                        )
                        for request_class in REQUEST_CLASSES:
                            summary_writer.writerow(
                                summary_row(
                                    workload=workload,
                                    trace_name=trace_name,
                                    policy=policy.name,
                                    window_size=window_size,
                                    max_age_ns=max_age_ns,
                                    request_class=request_class,
                                    issued=issued,
                                    mapping=mapping,
                                )
                            )
                        for row in window_metric_rows(
                            workload=workload,
                            trace_name=trace_name,
                            policy=policy.name,
                            window_size=window_size,
                            max_age_ns=max_age_ns,
                            issued=issued,
                        ):
                            window_writer.writerow(row)
    finally:
        close_writer(summary_writer)
        close_writer(window_writer)
        close_writer(frag_writer)

    print(f"[m1] wrote {out_dir / 'm1_summary.csv'}")
    print(f"[m1] wrote {out_dir / 'm1_window_metrics.csv'}")
    print(f"[m1] wrote {out_dir / 'm1_fragmentation.csv'}")
    print(f"[m1] wrote {out_dir / 'm1_metadata.json'}")


if __name__ == "__main__":
    main()
