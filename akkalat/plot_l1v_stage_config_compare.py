#!/usr/bin/env python3
import argparse
import csv
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch


REQUEST_FINE_STAGES = [
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

RETURN_FINE_STAGES = [
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

COARSE_STAGE_FALLBACKS = {
    "Cross-GPU request": REQUEST_FINE_STAGES,
    "Cross-GPU data return": RETURN_FINE_STAGES,
}

STAGE_GROUPS = [
    ("Enter L1V", ["at_to_l1v_top"], "#4C78A8", None),
    (
        "L1V lookup/fill",
        [
            "l1v_coalesce_wait",
            "l1v_dir_lookup",
            "l1v_bank_hit",
            "l1v_bottom_response_parse",
            "l1v_mshr_wakeup",
            "l1v_fill_parent_done",
        ],
        "#72B7B2",
        None,
    ),
    ("Local: L1V -> L2", ["l1v_bottom_send_to_local_l2"], "#59A14F", "local"),
    ("Local: L2 -> L1V", ["local_l2_to_l1v_response"], "#8CD17D", "local"),
    ("Remote: L1V -> local RDMA", ["l1v_bottom_send_to_local_rdma"], "#B07AA1", "remote"),
    ("Local RDMA request queue", ["local_rdma_request_output_wait"], "#BAB0AC", "remote"),
    (
        "Req endpoint queue/inject",
        [
            "cross_gpu_request_endpoint_queue",
            "cross_gpu_request_endpoint_inject_wait",
        ],
        "#1F77B4",
        "remote",
    ),
    ("Req channel", ["cross_gpu_request_channel_transfer"], "#17BECF", "remote"),
    ("Req switch input queue", ["cross_gpu_request_switch_input_queue"], "#AEC7E8", "remote"),
    (
        "Req switch pipeline",
        ["cross_gpu_request_switch_pipeline", "cross_gpu_request_switch_route_wait"],
        "#6BAED6",
        "remote",
    ),
    (
        "Req switch arbitration",
        ["cross_gpu_request_switch_arb_wait", "cross_gpu_request_switch_output_wait"],
        "#3182BD",
        "remote",
    ),
    (
        "Req endpoint assemble/deliver",
        [
            "cross_gpu_request_endpoint_assemble_wait",
            "cross_gpu_request_endpoint_deliver_wait",
        ],
        "#9ECAE1",
        "remote",
    ),
    (
        "Cross-GPU request",
        ["local_rdma_to_remote_rdma_request"],
        "#D62728",
        "remote",
    ),
    ("Remote RDMA request queue", ["remote_rdma_request_output_wait"], "#C7C7C7", "remote"),
    ("Remote: RDMA -> L2", ["remote_rdma_to_remote_l2"], "#FF9DA7", "remote"),
    (
        "Local: L2 lookup/MSHR",
        [
            "l2_top_to_dir",
            "l2_dir_lookup",
            "l2_bank_hit",
            "l2_mshr_wait",
            "l2_writebuffer_wait",
        ],
        "#B6992D",
        "local",
    ),
    (
        "Remote: L2 lookup/MSHR",
        [
            "l2_top_to_dir",
            "l2_dir_lookup",
            "l2_bank_hit",
            "l2_mshr_wait",
            "l2_writebuffer_wait",
        ],
        "#8C6D1F",
        "remote",
    ),
    ("Local: L2 -> DRAM", ["l2_bottom_send_to_dram"], "#F58518", "local"),
    ("Remote: L2 -> DRAM", ["l2_bottom_send_to_dram"], "#E15759", "remote"),
    ("Local: DRAM service", ["dram_queue_and_service"], "#FFBE7D", "local"),
    ("Remote: DRAM service", ["dram_queue_and_service"], "#D3722C", "remote"),
    ("Local: DRAM -> L2", ["dram_to_l2_response"], "#FDD0A2", "local"),
    ("Remote: DRAM -> L2", ["dram_to_l2_response"], "#A63603", "remote"),
    ("Local: L2 fill/return", ["l2_fill_and_response"], "#EDC948", "local"),
    ("Remote: L2 fill", ["l2_fill_and_response"], "#C49C94", "remote"),
    ("Remote: L2 -> RDMA", ["remote_l2_to_remote_rdma_response"], "#9C755F", "remote"),
    ("Remote RDMA return queue", ["remote_rdma_response_output_wait"], "#D9D9D9", "remote"),
    (
        "Return endpoint queue/inject",
        [
            "cross_gpu_return_endpoint_queue",
            "cross_gpu_return_endpoint_inject_wait",
        ],
        "#E6550D",
        "remote",
    ),
    ("Return channel", ["cross_gpu_return_channel_transfer"], "#FD8D3C", "remote"),
    ("Return switch input queue", ["cross_gpu_return_switch_input_queue"], "#FDBE85", "remote"),
    (
        "Return switch pipeline",
        ["cross_gpu_return_switch_pipeline", "cross_gpu_return_switch_route_wait"],
        "#F16913",
        "remote",
    ),
    (
        "Return switch arbitration",
        ["cross_gpu_return_switch_arb_wait", "cross_gpu_return_switch_output_wait"],
        "#D94801",
        "remote",
    ),
    (
        "Return endpoint assemble/deliver",
        [
            "cross_gpu_return_endpoint_assemble_wait",
            "cross_gpu_return_endpoint_deliver_wait",
        ],
        "#FDD0A2",
        "remote",
    ),
    (
        "Cross-GPU data return",
        ["remote_rdma_to_local_rdma_response"],
        "#9467BD",
        "remote",
    ),
    ("Local RDMA return queue", ["local_rdma_response_output_wait"], "#BDBDBD", "remote"),
    ("Remote: local RDMA -> L1V", ["local_rdma_to_l1v_response"], "#C5B0D5", "remote"),
]


def read_csv(path):
    if not path.exists():
        return []
    with path.open(newline="") as f:
        return list(csv.DictReader(f))


def bench_from_path(path, suffix):
    name = path.name
    prefix = "baseline_"
    if name.startswith(prefix):
        name = name[len(prefix) :]
    return name[: -len(suffix)]


def short_name(name):
    replacements = {
        "matrixmultiplication": "matmul",
        "fastwalshtransform": "fwt",
        "simpleconvolution": "simpleconv",
        "matrixtranspose": "transpose",
        "middletile": "midtile",
    }
    out = name
    for src, dst in replacements.items():
        out = out.replace(src, dst)
    return out


def discover(root):
    path_suffix = "_baseline_memory_path_l1v_path_summary.csv"
    stage_suffix = "_baseline_memory_path_l1v_path_stage_summary.csv"
    paths = {
        bench_from_path(path, path_suffix): path
        for path in root.glob(f"baseline_*{path_suffix}")
    }
    if paths:
        return paths
    return {
        bench_from_path(path, stage_suffix): path
        for path in root.glob(f"baseline_*{stage_suffix}")
    }


def raw_records(root, bench):
    rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_summary.csv")
    for row in rows:
        if row.get("scope") == "steady":
            return int(row.get("raw_records", 0) or 0)
    if rows:
        return int(rows[0].get("raw_records", 0) or 0)
    return 0


def route_of(row):
    route = (row.get("route") or "").strip().lower()
    if route:
        return route
    return "remote" if (row.get("is_remote") or "").strip().lower() == "true" else "local"


def stage_ns(row, stage):
    return float(row.get(f"{stage}_ns", 0) or 0)


def route_matches(row, route_filter):
    return route_filter is None or route_of(row) == route_filter


def has_fine_stage_in_paths(path_rows, stages, route_filter):
    for row in path_rows:
        if not route_matches(row, route_filter):
            continue
        if any(stage_ns(row, stage) > 0 for stage in stages):
            return True
    return False


def has_fine_stage_in_summary(by_stage, stages):
    return any(by_stage.get(stage, 0) > 0 for stage in stages)


def grouped_stage_values(root, bench):
    path_rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_l1v_path_summary.csv")
    if path_rows:
        denom = len(path_rows)
        values = {}
        for group, stages, _, route_filter in STAGE_GROUPS:
            fallback_fine_stages = COARSE_STAGE_FALLBACKS.get(group)
            if fallback_fine_stages and has_fine_stage_in_paths(
                path_rows, fallback_fine_stages, route_filter
            ):
                values[group] = 0.0
                continue
            total = 0.0
            for row in path_rows:
                if not route_matches(row, route_filter):
                    continue
                total += sum(stage_ns(row, stage) for stage in stages)
            values[group] = total / denom if denom else 0.0
        return values

    rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_l1v_path_stage_summary.csv")
    by_stage = {
        row["stage"]: int(row.get("total_latency_ns", 0) or 0)
        for row in rows
    }
    denom = raw_records(root, bench)
    if denom <= 0:
        paths = [
            int(row.get("paths", 0) or 0)
            for row in rows
            if row.get("stage") == "at_to_l1v_top"
        ]
        denom = paths[0] if paths else 0

    values = {}
    for group, stages, _, route_filter in STAGE_GROUPS:
        fallback_fine_stages = COARSE_STAGE_FALLBACKS.get(group)
        if fallback_fine_stages and has_fine_stage_in_summary(by_stage, fallback_fine_stages):
            values[group] = 0.0
            continue
        if route_filter is not None:
            values[group] = 0.0
            continue
        total = sum(by_stage.get(stage, 0) for stage in stages)
        values[group] = total / denom if denom else 0.0
    return values


def collect(root):
    benches = discover(root)
    return {
        bench: grouped_stage_values(root, bench)
        for bench in sorted(benches)
    }


def additive_total(values):
    return sum(values.get(group, 0.0) for group, _, _, _ in STAGE_GROUPS)


def plot_compare(baseline, l1v160, out_dir, name, title):
    common = sorted(set(baseline) & set(l1v160), key=lambda b: additive_total(baseline[b]))
    if not common:
        raise SystemExit("No common benchmarks with L1V path stage summaries.")

    labels = [short_name(b) for b in common]
    height = max(7.4, 0.58 * len(common) + 4.8)
    fig, ax = plt.subplots(figsize=(16.2, height))
    fig.subplots_adjust(bottom=0.34, top=0.92)

    y_base = [i - 0.18 for i in range(len(common))]
    y_new = [i + 0.18 for i in range(len(common))]
    left_base = [0.0] * len(common)
    left_new = [0.0] * len(common)

    for group, _, color, _ in STAGE_GROUPS:
        base_vals = [baseline[b].get(group, 0.0) for b in common]
        new_vals = [l1v160[b].get(group, 0.0) for b in common]
        ax.barh(
            y_base,
            base_vals,
            left=left_base,
            height=0.32,
            color=color,
            edgecolor="white",
            linewidth=0.4,
        )
        ax.barh(
            y_new,
            new_vals,
            left=left_new,
            height=0.32,
            color=color,
            edgecolor="#222222",
            linewidth=0.35,
            hatch="//",
        )
        left_base = [l + v for l, v in zip(left_base, base_vals)]
        left_new = [l + v for l, v in zip(left_new, new_vals)]

    ax.set_yticks(range(len(common)))
    ax.set_yticklabels(labels)
    ax.set_xlabel("Average additive hop/stage contribution per selected L1V path (ns)")
    ax.set_title(title)
    ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)

    max_total = max(max(left_base), max(left_new))
    for y, total in zip(y_base, left_base):
        ax.text(total + max_total * 0.01, y, f"{total:.0f}", va="center", fontsize=7)
    for y, total in zip(y_new, left_new):
        ax.text(total + max_total * 0.01, y, f"{total:.0f}", va="center", fontsize=7)

    stage_handles = [Patch(facecolor=color, label=group) for group, _, color, _ in STAGE_GROUPS]
    config_handles = [
        Patch(facecolor="#DDDDDD", edgecolor="white", label="baseline"),
        Patch(facecolor="#DDDDDD", edgecolor="#222222", hatch="//", label="L1V 160/160"),
    ]
    fig.legend(
        handles=stage_handles + config_handles,
        loc="lower center",
        bbox_to_anchor=(0.5, 0.035),
        ncol=4,
        fontsize=6.2,
        frameon=True,
        framealpha=0.92,
    )

    out_dir.mkdir(parents=True, exist_ok=True)
    for ext in ("pdf", "png"):
        fig.savefig(out_dir / f"{name}.{ext}", bbox_inches="tight", dpi=240)
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--baseline-dir",
        default="akkalat/results/2026-06-08-05-43-31-sampled-validation",
    )
    parser.add_argument(
        "--l1v160-dir",
        default="akkalat/results/2026-06-08-17-16-58-sampled-validation",
    )
    parser.add_argument("--out-dir", default="weeklyreport/06102026/figures")
    parser.add_argument(
        "--name",
        default="traditional_stage_breakdown_baseline_vs_l1v160",
    )
    parser.add_argument(
        "--title",
        default="Traditional workloads: baseline vs. L1V 160-MSHR/160-transaction window",
    )
    args = parser.parse_args()

    baseline = collect(Path(args.baseline_dir))
    l1v160 = collect(Path(args.l1v160_dir))
    plot_compare(baseline, l1v160, Path(args.out_dir), args.name, args.title)
    print(f"Wrote {Path(args.out_dir) / (args.name + '.pdf')}")


if __name__ == "__main__":
    main()
