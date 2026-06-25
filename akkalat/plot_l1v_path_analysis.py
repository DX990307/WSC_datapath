#!/usr/bin/env python3
import argparse
import csv
import os
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.lines import Line2D
from matplotlib.patches import Patch


DEFAULT_LLM_DIR = "akkalat/results/2026-06-08-05-02-13-sampled-validation"
DEFAULT_TRAD_DIR = "akkalat/results/2026-06-08-05-43-31-sampled-validation"

SOURCE_ORDER = [
    "local_l2_cache",
    "local_l2_mshr",
    "local_dram",
    "remote_l2_cache",
    "remote_l2_mshr",
    "remote_dram",
    "remote_gpm",
    "unknown",
]

SOURCE_COLORS = {
    "local_l2_cache": "#4C78A8",
    "local_l2_mshr": "#72B7B2",
    "local_dram": "#59A14F",
    "remote_l2_cache": "#F28E2B",
    "remote_l2_mshr": "#FFBE7D",
    "remote_dram": "#E15759",
    "remote_gpm": "#B07AA1",
    "unknown": "#9D9D9D",
}

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
    if name.startswith("baseline_"):
        name = name[len("baseline_") :]
    return name[: -len(suffix)]


def discover_benches(root):
    suffix = "_baseline_memory_path_summary.csv"
    return sorted(bench_from_path(p, suffix) for p in root.glob(f"baseline_*{suffix}"))


def memory_summary(root, bench):
    rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_summary.csv")
    for row in rows:
        if row.get("scope") == "steady":
            return row
    return rows[0] if rows else {}


def stage_latency(root, bench):
    rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_stage_latency.csv")
    stages = {}
    for row in rows:
        if row.get("scope") != "steady":
            continue
        stages[row["stage"]] = row
    return stages


def l1v_stage_summary(root, bench):
    rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_l1v_path_stage_summary.csv")
    return {
        row["stage"]: {
            "paths": int(row["paths"]),
            "hops": int(row["hops"]),
            "avg_latency_ns": int(row["avg_latency_ns"]),
            "total_latency_ns": int(row["total_latency_ns"]),
        }
        for row in rows
    }


def l1v_path_rows(root, bench):
    return read_csv(root / f"baseline_{bench}_baseline_memory_path_l1v_path_summary.csv")


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


def has_fine_stage_in_summary(l1v_stages, stages):
    return any(l1v_stages.get(stage, {}).get("total_latency_ns", 0) > 0 for stage in stages)


def grouped_stage_avg(path_rows, l1v_stages, raw_records, total_accesses, group, stage_names, route_filter):
    fallback_fine_stages = COARSE_STAGE_FALLBACKS.get(group)
    if path_rows:
        if fallback_fine_stages and has_fine_stage_in_paths(path_rows, fallback_fine_stages, route_filter):
            return 0.0
        denom = len(path_rows)
        total = 0.0
        for row in path_rows:
            if not route_matches(row, route_filter):
                continue
            total += sum(stage_ns(row, stage) for stage in stage_names)
        return total / denom if denom else 0.0

    if fallback_fine_stages and has_fine_stage_in_summary(l1v_stages, fallback_fine_stages):
        return 0.0
    if route_filter is not None:
        return 0.0

    denom = raw_records if raw_records > 0 else total_accesses
    total = sum(l1v_stages.get(stage, {}).get("total_latency_ns", 0) for stage in stage_names)
    return total / denom if denom else 0


def source_breakdown(root, bench):
    rows = read_csv(root / f"baseline_{bench}_baseline_metrics_l2_source_summary.csv")
    totals = {}
    total_accesses = 0
    for row in rows:
        source = row.get("source_tier", "unknown") or "unknown"
        accesses = int(row.get("accesses", 0) or 0)
        totals[source] = totals.get(source, 0) + accesses
        total_accesses += accesses
    return totals, total_accesses


def joint_summary(root, bench):
    rows = read_csv(root / f"baseline_{bench}_baseline_memory_path_joint_miss.csv")
    for row in rows:
        if row.get("scope") == "steady" and row.get("pair") == "any_tlb_vs_any_cache":
            return row
    return {}


def collect_dataset(label, root):
    benches = discover_benches(root)
    records = []
    source_rows = []
    stage_rows = []
    for bench in benches:
        mem = memory_summary(root, bench)
        stages = stage_latency(root, bench)
        l1v_stages = l1v_stage_summary(root, bench)
        paths = l1v_path_rows(root, bench)
        joint = joint_summary(root, bench)
        sources, source_total = source_breakdown(root, bench)

        raw_records = int(mem.get("raw_records", 0) or 0)
        total_accesses = int(mem.get("total_accesses", 0) or 0)
        remote_accesses = int(mem.get("remote_accesses", 0) or 0)
        l1v_avg = int(stages.get("l1v_cache_end_to_end", {}).get("avg_latency_ns", 0) or 0)
        l1v_total = int(stages.get("l1v_cache_end_to_end", {}).get("total_latency_ns", 0) or 0)
        remote_ratio = float(mem.get("remote_ratio", 0) or 0)
        strict_joint = float(joint.get("strict_joint_miss_ratio", 0) or 0)
        not_hit_joint = float(joint.get("not_hit_joint_miss_ratio", 0) or 0)

        records.append(
            {
                "dataset": label,
                "bench": bench,
                "raw_records": raw_records,
                "total_accesses": total_accesses,
                "remote_accesses": remote_accesses,
                "remote_ratio": remote_ratio,
                "avg_l1v_ns": l1v_avg,
                "total_l1v_ns": l1v_total,
                "strict_joint": strict_joint,
                "not_hit_joint": not_hit_joint,
                "top_source": mem.get("top_source", ""),
                "top_source_accesses": int(mem.get("top_source_accesses", 0) or 0),
            }
        )

        for source in SOURCE_ORDER:
            source_rows.append(
                {
                    "dataset": label,
                    "bench": bench,
                    "source": source,
                    "accesses": sources.get(source, 0),
                    "ratio": (sources.get(source, 0) / source_total) if source_total else 0,
                }
            )

        for group, stage_names, color, route_filter in STAGE_GROUPS:
            stage_rows.append(
                {
                    "dataset": label,
                    "bench": bench,
                    "group": group,
                    "avg_per_path_ns": grouped_stage_avg(
                        paths,
                        l1v_stages,
                        raw_records,
                        total_accesses,
                        group,
                        stage_names,
                        route_filter,
                    ),
                    "color": color,
                }
            )
    return records, source_rows, stage_rows


def short_name(name):
    replacements = {
        "matrixmultiplication": "matmul",
        "llm-prefill": "prefill",
        "llm-decode": "decode",
        "fastwalshtransform": "fwt",
        "simpleconvolution": "simpleconv",
        "matrixtranspose": "transpose",
        "middletile": "midtile",
    }
    out = name
    for src, dst in replacements.items():
        out = out.replace(src, dst)
    return out


def savefig(fig, out_dir, name):
    for ext in ("pdf", "png"):
        fig.savefig(out_dir / f"{name}.{ext}", bbox_inches="tight", dpi=240)
    plt.close(fig)


def plot_latency(records, out_dir):
    for dataset in ("llm-like", "traditional"):
        rows = sorted([r for r in records if r["dataset"] == dataset], key=lambda r: r["avg_l1v_ns"])
        height = max(4, 0.32 * len(rows) + 1.4)
        fig, ax = plt.subplots(figsize=(8.2, height))
        labels = [short_name(r["bench"]) for r in rows]
        values = [r["avg_l1v_ns"] for r in rows]
        colors = ["#4C78A8" if dataset == "llm-like" else "#59A14F"] * len(rows)
        ax.barh(labels, values, color=colors)
        ax.set_xlabel("Average L1V path latency (ns)")
        ax.set_title(f"{dataset}: average L1V request path latency")
        ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
        for y, v in enumerate(values):
            ax.text(v + max(values) * 0.01, y, f"{v:.0f}", va="center", fontsize=8)
        savefig(fig, out_dir, f"01_{dataset.replace('-', '_')}_avg_l1v_latency")


def plot_remote_ratio(records, out_dir):
    fig, axes = plt.subplots(1, 2, figsize=(13, 8), sharex=True)
    for ax, dataset in zip(axes, ("llm-like", "traditional")):
        rows = sorted([r for r in records if r["dataset"] == dataset], key=lambda r: r["remote_ratio"])
        labels = [short_name(r["bench"]) for r in rows]
        values = [100 * r["remote_ratio"] for r in rows]
        ax.barh(labels, values, color="#F28E2B")
        ax.set_title(dataset)
        ax.set_xlabel("Remote accesses / total accesses (%)")
        ax.set_xlim(0, 100)
        ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
    fig.suptitle("Remote access ratio")
    savefig(fig, out_dir, "02_remote_ratio")


def plot_source_breakdown(source_rows, records, out_dir):
    record_lookup = {(r["dataset"], r["bench"]): r for r in records}
    for dataset in ("llm-like", "traditional"):
        benches = sorted(
            [r["bench"] for r in records if r["dataset"] == dataset],
            key=lambda b: record_lookup[(dataset, b)]["remote_ratio"],
        )
        height = max(4.5, 0.34 * len(benches) + 1.4)
        fig, ax = plt.subplots(figsize=(9.2, height))
        left = [0.0] * len(benches)
        for source in SOURCE_ORDER:
            vals = []
            for bench in benches:
                match = next(
                    r for r in source_rows if r["dataset"] == dataset and r["bench"] == bench and r["source"] == source
                )
                vals.append(100 * match["ratio"])
            ax.barh(
                [short_name(b) for b in benches],
                vals,
                left=left,
                color=SOURCE_COLORS[source],
                label=source,
                height=0.78,
            )
            left = [l + v for l, v in zip(left, vals)]
        ax.set_xlim(0, 100)
        ax.set_xlabel("L2/data source share (%)")
        ax.set_title(f"{dataset}: source breakdown for lower-level serviced requests")
        ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
        ax.legend(loc="lower center", bbox_to_anchor=(0.5, -0.25), ncol=4, fontsize=8)
        savefig(fig, out_dir, f"03_{dataset.replace('-', '_')}_source_breakdown")


def plot_stage_breakdown(stage_rows, records, out_dir):
    record_lookup = {(r["dataset"], r["bench"]): r for r in records}
    for dataset in ("llm-like", "traditional"):
        benches = sorted(
            [r["bench"] for r in records if r["dataset"] == dataset],
            key=lambda b: record_lookup[(dataset, b)]["avg_l1v_ns"],
        )
        height = max(6.0, 0.36 * len(benches) + 3.6)
        fig, ax = plt.subplots(figsize=(14.5, height))
        fig.subplots_adjust(bottom=0.36, top=0.92)
        left = [0.0] * len(benches)
        for group, _, color, _ in STAGE_GROUPS:
            vals = []
            for bench in benches:
                match = next(
                    r for r in stage_rows if r["dataset"] == dataset and r["bench"] == bench and r["group"] == group
                )
                vals.append(match["avg_per_path_ns"])
            ax.barh(
                [short_name(b) for b in benches],
                vals,
                left=left,
                color=color,
                label=group,
                height=0.78,
            )
            left = [l + v for l, v in zip(left, vals)]
        ax.set_xlabel("Average contribution per selected L1V path (ns)")
        ax.set_title(f"{dataset}: additive hop/stage contribution")
        ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
        stage_handles = [Patch(facecolor=color, label=group) for group, _, color, _ in STAGE_GROUPS]
        fig.legend(
            handles=stage_handles,
            loc="lower center",
            bbox_to_anchor=(0.5, 0.035),
            ncol=4,
            fontsize=6.2,
            frameon=True,
            framealpha=0.92,
        )
        savefig(fig, out_dir, f"04_{dataset.replace('-', '_')}_stage_breakdown")


def plot_stage_breakdown_percent(stage_rows, records, out_dir):
    record_lookup = {(r["dataset"], r["bench"]): r for r in records}
    for dataset in ("llm-like", "traditional"):
        benches = sorted(
            [r["bench"] for r in records if r["dataset"] == dataset],
            key=lambda b: record_lookup[(dataset, b)]["avg_l1v_ns"],
        )
        denom_by_bench = {}
        for bench in benches:
            denom_by_bench[bench] = sum(
                r["avg_per_path_ns"]
                for r in stage_rows
                if r["dataset"] == dataset and r["bench"] == bench
            )

        height = max(6.0, 0.36 * len(benches) + 3.6)
        fig, ax = plt.subplots(figsize=(14.5, height))
        fig.subplots_adjust(bottom=0.36, top=0.92)
        left = [0.0] * len(benches)
        for group, _, color, _ in STAGE_GROUPS:
            vals = []
            for bench in benches:
                match = next(
                    r
                    for r in stage_rows
                    if r["dataset"] == dataset
                    and r["bench"] == bench
                    and r["group"] == group
                )
                denom = denom_by_bench[bench]
                vals.append(100 * match["avg_per_path_ns"] / denom if denom else 0)
            ax.barh(
                [short_name(b) for b in benches],
                vals,
                left=left,
                color=color,
                label=group,
                height=0.78,
            )
            left = [l + v for l, v in zip(left, vals)]
        ax.set_xlim(0, 100)
        ax.set_xlabel("Share of additive hop/stage contribution (%)")
        ax.set_title(f"{dataset}: normalized additive hop/stage contribution")
        ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
        stage_handles = [Patch(facecolor=color, label=group) for group, _, color, _ in STAGE_GROUPS]
        fig.legend(
            handles=stage_handles,
            loc="lower center",
            bbox_to_anchor=(0.5, 0.035),
            ncol=4,
            fontsize=6.2,
            frameon=True,
            framealpha=0.92,
        )
        savefig(fig, out_dir, f"04b_{dataset.replace('-', '_')}_stage_breakdown_percent")


def plot_scatter(records, out_dir):
    fig, ax = plt.subplots(figsize=(8.2, 5.6))
    colors = {"llm-like": "#4C78A8", "traditional": "#59A14F"}
    for dataset in ("llm-like", "traditional"):
        rows = [r for r in records if r["dataset"] == dataset]
        ax.scatter(
            [100 * r["remote_ratio"] for r in rows],
            [r["avg_l1v_ns"] for r in rows],
            s=52,
            color=colors[dataset],
            alpha=0.86,
            label=dataset,
        )
        for r in sorted(rows, key=lambda x: x["avg_l1v_ns"], reverse=True)[:4]:
            ax.annotate(
                short_name(r["bench"]),
                (100 * r["remote_ratio"], r["avg_l1v_ns"]),
                textcoords="offset points",
                xytext=(5, 5),
                fontsize=8,
            )
    ax.set_xlabel("Remote accesses / total accesses (%)")
    ax.set_ylabel("Average L1V path latency (ns)")
    ax.set_title("Remote access ratio vs. L1V path latency")
    ax.grid(color="#d9d9d9", linewidth=0.8)
    ax.legend()
    savefig(fig, out_dir, "05_remote_ratio_vs_latency")


def plot_joint(records, out_dir):
    for dataset in ("llm-like", "traditional"):
        rows = sorted([r for r in records if r["dataset"] == dataset], key=lambda r: r["not_hit_joint"])
        height = max(4, 0.32 * len(rows) + 1.4)
        fig, ax = plt.subplots(figsize=(8.2, height))
        labels = [short_name(r["bench"]) for r in rows]
        y = range(len(rows))
        strict = [100 * r["strict_joint"] for r in rows]
        not_hit = [100 * r["not_hit_joint"] for r in rows]
        ax.barh([v - 0.18 for v in y], strict, height=0.35, color="#E15759", label="strict: miss + miss")
        ax.barh([v + 0.18 for v in y], not_hit, height=0.35, color="#B07AA1", label="not-hit: miss/MSHR + miss/MSHR")
        ax.set_yticks(list(y))
        ax.set_yticklabels(labels)
        ax.set_xlim(0, 100)
        ax.set_xlabel("Joint miss ratio (%)")
        ax.set_title(f"{dataset}: TLB/cache joint miss")
        ax.grid(axis="x", color="#d9d9d9", linewidth=0.8)
        ax.legend(loc="lower right", fontsize=8)
        savefig(fig, out_dir, f"06_{dataset.replace('-', '_')}_joint_miss")


def write_overview(records, out_dir):
    path = out_dir / "l1v_path_overview.csv"
    fields = [
        "dataset",
        "bench",
        "raw_records",
        "total_accesses",
        "remote_accesses",
        "remote_ratio",
        "avg_l1v_ns",
        "strict_joint",
        "not_hit_joint",
        "top_source",
        "top_source_accesses",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in records:
            writer.writerow({field: row[field] for field in fields})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--llm-dir", default=DEFAULT_LLM_DIR)
    parser.add_argument("--traditional-dir", default=DEFAULT_TRAD_DIR)
    parser.add_argument("--out-dir", default="akkalat/results/2026-06-08-l1v-path-analysis/figures")
    args = parser.parse_args()

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    all_records = []
    all_sources = []
    all_stages = []
    for label, root in [
        ("llm-like", Path(args.llm_dir)),
        ("traditional", Path(args.traditional_dir)),
    ]:
        records, sources, stages = collect_dataset(label, root)
        all_records.extend(records)
        all_sources.extend(sources)
        all_stages.extend(stages)

    write_overview(all_records, out_dir)
    plot_latency(all_records, out_dir)
    plot_remote_ratio(all_records, out_dir)
    plot_source_breakdown(all_sources, all_records, out_dir)
    plot_stage_breakdown(all_stages, all_records, out_dir)
    plot_stage_breakdown_percent(all_stages, all_records, out_dir)
    plot_scatter(all_records, out_dir)
    plot_joint(all_records, out_dir)

    print(f"Wrote figures to {out_dir}")


if __name__ == "__main__":
    main()
