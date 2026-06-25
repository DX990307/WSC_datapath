#!/usr/bin/env python3
import argparse
import csv
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt


REQUEST_FINE_STAGE_NAMES = [
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
]

RESPONSE_FINE_STAGE_NAMES = [
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
]

REQUEST_STAGES = [
    ("L1V -> local RDMA", ["l1v_bottom_send_to_local_rdma"], "#4C78A8"),
    ("local RDMA output wait", ["local_rdma_request_output_wait"], "#72B7B2"),
    (
        "Req endpoint queue/inject",
        [
            "cross_gpu_request_endpoint_queue",
            "cross_gpu_request_endpoint_inject_wait",
        ],
        "#1F77B4",
    ),
    ("Req channel", ["cross_gpu_request_channel_transfer"], "#17BECF"),
    ("Req switch input queue", ["cross_gpu_request_switch_input_queue"], "#AEC7E8"),
    (
        "Req switch pipeline",
        ["cross_gpu_request_switch_pipeline", "cross_gpu_request_switch_route_wait"],
        "#6BAED6",
    ),
    (
        "Req switch arbitration",
        ["cross_gpu_request_switch_arb_wait", "cross_gpu_request_switch_output_wait"],
        "#3182BD",
    ),
    (
        "Req endpoint assemble/deliver",
        [
            "cross_gpu_request_endpoint_assemble_wait",
            "cross_gpu_request_endpoint_deliver_wait",
        ],
        "#9ECAE1",
    ),
    ("local -> remote RDMA (coarse)", ["local_rdma_to_remote_rdma_request"], "#F28E2B"),
    ("remote RDMA output wait", ["remote_rdma_request_output_wait"], "#FFBE7D"),
    ("remote RDMA -> L2", ["remote_rdma_to_remote_l2"], "#59A14F"),
]

RESPONSE_STAGES = [
    ("remote L2 -> RDMA", ["remote_l2_to_remote_rdma_response"], "#59A14F"),
    ("remote RDMA output wait", ["remote_rdma_response_output_wait"], "#FFBE7D"),
    (
        "Return endpoint queue/inject",
        [
            "cross_gpu_return_endpoint_queue",
            "cross_gpu_return_endpoint_inject_wait",
        ],
        "#E6550D",
    ),
    ("Return channel", ["cross_gpu_return_channel_transfer"], "#FD8D3C"),
    ("Return switch input queue", ["cross_gpu_return_switch_input_queue"], "#FDBE85"),
    (
        "Return switch pipeline",
        ["cross_gpu_return_switch_pipeline", "cross_gpu_return_switch_route_wait"],
        "#F16913",
    ),
    (
        "Return switch arbitration",
        ["cross_gpu_return_switch_arb_wait", "cross_gpu_return_switch_output_wait"],
        "#D94801",
    ),
    (
        "Return endpoint assemble/deliver",
        [
            "cross_gpu_return_endpoint_assemble_wait",
            "cross_gpu_return_endpoint_deliver_wait",
        ],
        "#FDD0A2",
    ),
    ("remote -> local RDMA (coarse)", ["remote_rdma_to_local_rdma_response"], "#F28E2B"),
    ("local RDMA output wait", ["local_rdma_response_output_wait"], "#72B7B2"),
    ("local RDMA -> L1V", ["local_rdma_to_l1v_response"], "#4C78A8"),
]


def short_name(name):
    replacements = {
        "matrixmultiplication": "matmul",
        "fastwalshtransform": "fwt",
        "matrixtranspose": "transpose",
        "middletile": "midtile",
    }
    out = name
    for src, dst in replacements.items():
        out = out.replace(src, dst)
    return out


def bench_from_path(path):
    name = path.name
    prefix = "baseline_"
    suffix = "_baseline_memory_path_l1v_path_stage_summary.csv"
    if name.startswith(prefix):
        name = name[len(prefix) :]
    return name[: -len(suffix)]


def read_stage_summary(path):
    rows = {}
    with path.open(newline="") as f:
        for row in csv.DictReader(f):
            rows[row["stage"]] = {
                "paths": int(row.get("paths", 0) or 0),
                "hops": int(row.get("hops", 0) or 0),
                "total_latency_ns": int(row.get("total_latency_ns", 0) or 0),
            }
    return rows


def stage_total(stages, stage_names):
    return sum(stages.get(stage, {}).get("total_latency_ns", 0) for stage in stage_names)


def has_any_stage(stages, stage_names):
    return any(stages.get(stage, {}).get("total_latency_ns", 0) > 0 for stage in stage_names)


def denom_for(stages, preferred_stages, stage_defs):
    for stage in preferred_stages:
        hops = stages.get(stage, {}).get("hops", 0)
        if hops > 0:
            return hops
    counts = [
        stages.get(stage, {}).get("hops", 0)
        for _, stage_names, _ in stage_defs
        for stage in stage_names
    ]
    return max(counts) if counts else 0


def collect(root):
    suffix = "_baseline_memory_path_l1v_path_stage_summary.csv"
    records = []
    for path in sorted(root.glob(f"baseline_*{suffix}")):
        bench = bench_from_path(path)
        stages = read_stage_summary(path)
        req_denom = denom_for(
            stages,
            ["local_rdma_to_remote_rdma_request", "local_rdma_request_output_wait"],
            REQUEST_STAGES,
        )
        rsp_denom = denom_for(
            stages,
            ["remote_rdma_to_local_rdma_response", "remote_rdma_response_output_wait"],
            RESPONSE_STAGES,
        )
        if req_denom == 0 and rsp_denom == 0:
            continue
        req_has_fine = has_any_stage(stages, REQUEST_FINE_STAGE_NAMES)
        rsp_has_fine = has_any_stage(stages, RESPONSE_FINE_STAGE_NAMES)
        record = {
            "bench": bench,
            "request_denom": req_denom,
            "response_denom": rsp_denom,
        }
        for label, stage_names, _ in REQUEST_STAGES:
            if label.endswith("(coarse)") and req_has_fine:
                total = 0
            else:
                total = stage_total(stages, stage_names)
            record[f"request::{label}"] = total / req_denom if req_denom else 0
        for label, stage_names, _ in RESPONSE_STAGES:
            if label.endswith("(coarse)") and rsp_has_fine:
                total = 0
            else:
                total = stage_total(stages, stage_names)
            record[f"response::{label}"] = total / rsp_denom if rsp_denom else 0
        records.append(record)
    return records


def plot(records, stage_defs, prefix, out_dir):
    if prefix == "request":
        denom_key = "request_denom"
        title = "RDMA request path breakdown"
    else:
        denom_key = "response_denom"
        title = "RDMA response path breakdown"

    rows = [
        r for r in records
        if r.get(denom_key, 0) > 0
    ]
    rows.sort(key=lambda r: sum(r[f"{prefix}::{label}"] for label, _, _ in stage_defs))
    if not rows:
        return

    height = max(4.5, 0.38 * len(rows) + 1.5)
    fig, ax = plt.subplots(figsize=(10.8, height))
    left = [0.0] * len(rows)
    ylabels = [short_name(r["bench"]) for r in rows]

    for label, _, color in stage_defs:
        vals = [r[f"{prefix}::{label}"] for r in rows]
        ax.barh(ylabels, vals, left=left, color=color, label=label, height=0.78)
        left = [l + v for l, v in zip(left, vals)]

    max_total = max(left)
    for y, total, row in zip(range(len(rows)), left, rows):
        ax.text(
            total + max_total * 0.01,
            y,
            f"{total:.0f} ns / {row[denom_key]} ops",
            va="center",
            fontsize=7,
        )

    ax.set_xlabel("Average contribution per RDMA transaction (ns)")
    ax.set_title(title)
    ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
    ax.legend(loc="lower center", bbox_to_anchor=(0.5, -0.28), ncol=3, fontsize=8)
    for ext in ("pdf", "png"):
        fig.savefig(out_dir / f"rdma_{prefix}_breakdown.{ext}", bbox_inches="tight", dpi=240)
    plt.close(fig)


def write_csv(records, out_dir):
    if not records:
        return
    fields = ["bench", "request_denom", "response_denom"]
    for label, _, _ in REQUEST_STAGES:
        fields.append(f"request::{label}")
    for label, _, _ in RESPONSE_STAGES:
        fields.append(f"response::{label}")
    with (out_dir / "rdma_breakdown_values.csv").open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in records:
            writer.writerow({field: row.get(field, 0) for field in fields})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("result_dir")
    parser.add_argument("--out-dir", default=None)
    args = parser.parse_args()

    root = Path(args.result_dir)
    out_dir = Path(args.out_dir) if args.out_dir else root / "figures" / "rdma_path_breakdown"
    out_dir.mkdir(parents=True, exist_ok=True)

    records = collect(root)
    write_csv(records, out_dir)
    plot(records, REQUEST_STAGES, "request", out_dir)
    plot(records, RESPONSE_STAGES, "response", out_dir)
    print(f"Wrote RDMA breakdown outputs to {out_dir}")


if __name__ == "__main__":
    main()
