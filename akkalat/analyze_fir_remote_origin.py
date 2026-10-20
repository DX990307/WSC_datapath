#!/usr/bin/env python3
"""Audit FIR requester/page-owner provenance for the CuPath V6 runs."""

from __future__ import annotations

import argparse
import csv
import json
import re
from collections import defaultdict
from pathlib import Path

from analyze_wg_mapping import audit as audit_wg_mapping


CONFIGS = ("baseline", "m2", "m3", "complete")
DATA_OBJECTS = ("history", "input", "filter", "output")
AUXILIARY_OBJECTS = ("unclassified",)
OBJECTS = DATA_OBJECTS + AUXILIARY_OBJECTS
RDMA_RE = re.compile(r"^GPU\[\d+\]\.RDMA$")


def read_rows(path: Path) -> list[dict[str, str]]:
    if not path.is_file():
        raise ValueError(f"missing FIR provenance file: {path}")
    with path.open(newline="", encoding="utf-8") as stream:
        rows = []
        for row in csv.DictReader(stream):
            # The simulator's metric reporter writes headers as
            # `, where, what, value`, while trace CSVs have compact headers.
            # Normalize both formats at the reader boundary.
            rows.append({key.strip(): value.strip() for key, value in row.items()})
        return rows


def prefix(root: Path, config: str) -> Path:
    return root / f"baseline_fir_{config}_remote_origin"


def integer(row: dict[str, str], field: str) -> int:
    return int(row[field])


def rdma_inside_transactions(root: Path, config: str) -> int:
    path = root / f"baseline_fir_{config}_metrics.csv"
    total = 0
    for row in read_rows(path):
        if (
            RDMA_RE.match(row["where"].strip())
            and row["what"].strip() == "outgoing_trans_count"
        ):
            total += int(float(row["value"]))
    return total


def analyze(root: Path, configs: tuple[str, ...] = CONFIGS):
    mapping = audit_wg_mapping(root, ["fir"], configs)
    if any(row["requested_total_wg"] != 524_288 for row in mapping):
        raise ValueError("FIR did not launch the complete 524,288-WG grid")
    if any(not row["partition_matches_baseline"] for row in mapping):
        raise ValueError("FIR original partition differs across configurations")

    summary_rows: list[dict[str, object]] = []
    matrix_rows: list[dict[str, object]] = []
    per_gpu_rows: list[dict[str, object]] = []
    first_remote_rows: list[dict[str, str]] = []

    for config in configs:
        base = prefix(root, config)
        rows = read_rows(Path(str(base) + "_summary.csv"))
        raw = read_rows(Path(str(base) + "_raw.csv"))
        rdma_transactions = rdma_inside_transactions(root, config)
        observed_objects = {row["object"] for row in rows}
        unexpected = observed_objects - set(OBJECTS)
        if unexpected:
            raise ValueError(
                f"FIR {config} contains unexpected objects: "
                + ", ".join(sorted(unexpected))
            )

        by_object_op: dict[tuple[str, str, bool], list[int]] = defaultdict(
            lambda: [0, 0]
        )
        by_pair: dict[tuple[int, int, str, str], list[int]] = defaultdict(
            lambda: [0, 0]
        )
        by_requester: dict[int, list[int]] = defaultdict(lambda: [0, 0])
        auxiliary_by_requester: dict[int, int] = defaultdict(int)
        for row in rows:
            requests = integer(row, "requests")
            byte_count = integer(row, "bytes")
            remote = row["is_remote"].lower() == "true"
            by_object_op[(row["object"], row["operation"], remote)][0] += requests
            by_object_op[(row["object"], row["operation"], remote)][1] += byte_count
            requester = integer(row, "requester_gpu")
            owner = integer(row, "page_owner_gpu")
            by_pair[(requester, owner, row["object"], row["operation"])][0] += requests
            by_pair[(requester, owner, row["object"], row["operation"])][1] += byte_count
            if row["object"] in DATA_OBJECTS:
                by_requester[requester][1 if remote else 0] += requests
            else:
                auxiliary_by_requester[requester] += requests

        for object_name in OBJECTS:
            for operation in ("read", "write"):
                local = by_object_op[(object_name, operation, False)]
                remote = by_object_op[(object_name, operation, True)]
                summary_rows.append({
                    "configuration": config,
                    "object": object_name,
                    "operation": operation,
                    "local_requests": local[0],
                    "remote_requests": remote[0],
                    "local_bytes": local[1],
                    "remote_bytes": remote[1],
                })
        for (requester, owner, object_name, operation), values in sorted(by_pair.items()):
            matrix_rows.append({
                "configuration": config,
                "requester_gpu": requester,
                "owner_gpu": owner,
                "object": object_name,
                "operation": operation,
                "requests": values[0],
                "bytes": values[1],
                "is_remote": requester != owner,
            })
        for requester, values in sorted(by_requester.items()):
            total = sum(values)
            per_gpu_rows.append({
                "configuration": config,
                "requester_gpu": requester,
                "local_requests": values[0],
                "remote_requests": values[1],
                "total_requests": total,
                "remote_fraction": values[1] / total if total else 0.0,
                "auxiliary_unclassified_requests": auxiliary_by_requester[requester],
                "rdma_inside_transactions_all_gpus": rdma_transactions,
            })
        for object_name in DATA_OBJECTS:
            first_remote = next(
                (
                    row for row in raw
                    if row["is_remote"].lower() == "true"
                    and row["object"] == object_name
                ),
                None,
            )
            if first_remote is not None:
                first_remote_rows.append(
                    {"configuration": config, **first_remote}
                )

    return mapping, summary_rows, matrix_rows, per_gpu_rows, first_remote_rows


def write_csv(path: Path, rows: list[dict[str, object]]) -> None:
    if not rows:
        return
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def write_outputs(root: Path, analysis) -> None:
    mapping, summary, matrix, per_gpu, first_remote = analysis
    write_csv(root / "fir_remote_origin_by_object.csv", summary)
    write_csv(root / "fir_requester_owner_matrix.csv", matrix)
    write_csv(root / "fir_per_gpu_local_remote.csv", per_gpu)
    write_csv(root / "fir_first_remote_request.csv", first_remote)

    total_by_config: dict[str, list[int]] = defaultdict(lambda: [0, 0])
    for row in per_gpu:
        total_by_config[str(row["configuration"])][0] += int(row["local_requests"])
        total_by_config[str(row["configuration"])][1] += int(row["remote_requests"])
    lines = [
        "# FIR remote-origin audit",
        "",
        "Every audited configuration launches the complete 524,288-WG FIR grid with "
        "the same original unified-GPU partition. The runtime stopper only "
        "ends the run after the configured number of MapWGReq lifetimes.",
        "",
        "| Configuration | Translated local | Translated remote | Remote fraction | Legacy RDMA inside transactions | First raw remote evidence |",
        "|---|---:|---:|---:|---:|---|",
    ]
    first_configs = {row["configuration"] for row in first_remote}
    for config in (row["configuration"] for row in mapping):
        local, remote = total_by_config[str(config)]
        total = local + remote
        fraction = remote / total if total else 0.0
        downstream = next(
            int(row["rdma_inside_transactions_all_gpus"])
            for row in per_gpu
            if row["configuration"] == config
        )
        lines.append(
            f"| {config} | {local:,} | {remote:,} | {fraction:.6%} | "
            f"{downstream:,} | "
            f"{'yes' if config in first_configs else 'not captured in bounded raw trace'} |"
        )
    lines += [
        "",
        "`fir_remote_origin_by_object.csv` separates history, input, filter, "
        "and output traffic and preserves unclassified scalar-side traffic "
        "as an explicit auxiliary row. Local/remote fractions use only the "
        "four registered FIR data objects. `fir_requester_owner_matrix.csv` and "
        "`fir_per_gpu_local_remote.csv` preserve the complete aggregate "
        "evidence; `fir_first_remote_request.csv` preserves the earliest "
        "bounded raw example for each registered FIR object when available. "
        "The legacy RDMA counter includes "
        "all inside `req_in` transactions and is shown only as a cross-check; "
        "it is not labeled as a data-only remote-request count.",
    ]
    (root / "FIR_REMOTE_ORIGIN_AUDIT.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--configs", default=",".join(CONFIGS))
    args = parser.parse_args()
    root = args.results.resolve()
    configs = tuple(item.strip() for item in args.configs.split(",") if item.strip())
    result = analyze(root, configs)
    write_outputs(root, result)
    print(root / "FIR_REMOTE_ORIGIN_AUDIT.md")


if __name__ == "__main__":
    main()
