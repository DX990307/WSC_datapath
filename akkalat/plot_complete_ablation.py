#!/usr/bin/env python3
"""Plot a strict full or 13-workload-primary ablation."""

import argparse
import csv
import json
import math
import os
import re
import shlex
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
from matplotlib.patches import Patch


WORKLOADS = [
    ("aes", "AES"), ("bitonicsort", "BT"),
    ("fastwalshtransform", "FWT"), ("fft", "FFT"), ("fir", "FIR"),
    ("relu", "RELU"), ("simpleconvolution", "SC"),
    ("floydwarshall", "FWS"), ("kmeans", "KM"),
    ("matrixmultiplication", "MM"), ("pagerank", "PR"),
    ("im2col", "I2C"), ("matrixtranspose", "MT"), ("spmv", "SPMV"),
]

WORKLOAD_GROUPS = [
    ("All Local", 0, 7),
    ("Mixed", 7, 11),
    ("Remote", 11, 14),
]

SERIES = [
    ("Local Opt.", "local_optimization_only", "#31ADCE"),
    ("Remote Request", "remote_request_only", "#ED6612"),
    ("Remote L2", "remote_l2_only", "#0CC08A"),
    ("Combined", "all_three", "#D6295D"),
]

CLIPPED_LABEL_X_OFFSETS = {
    ("BT", "Local Opt."): -.07,
    ("BT", "Combined"): .07,
    ("MM", "Remote L2"): -.07,
    ("MM", "Combined"): .07,
}

PAPER_GEOMEAN_TARGET = 1.3
PAPER_COVERAGE_TARGET_PCT = 80.0
L1V_DEMAND_OUTCOMES = frozenset({
    "read-hit", "read-miss", "read-mshr-hit",
    "write-hit", "write-miss", "write-mshr-hit",
})
OPTIMIZATION_TARGET_LABELS = frozenset(
    label for _, label in WORKLOADS if label != "SPMV"
)
EXPECTED_MECHANISM_CONFIGS = {
    "baseline": {
        "l2_resident_filter_enabled": 0.0,
        "dram_batch_enabled": 0.0,
        "dram_row_continuation_enabled": 0.0,
        "remote_data_path_enabled": 0.0,
    },
    "local_optimization_only": {
        "l2_resident_filter_enabled": 1.0,
        "dram_batch_enabled": 1.0,
        "dram_row_continuation_enabled": 1.0,
        "remote_data_path_enabled": 0.0,
    },
    "remote_request_only": {
        "l2_resident_filter_enabled": 0.0,
        "dram_batch_enabled": 0.0,
        "dram_row_continuation_enabled": 0.0,
        "remote_data_path_enabled": 1.0,
        "remote_dedup_enabled": 1.0,
        "remote_batching_enabled": 1.0,
        "remote_requester_l2_enabled": 0.0,
        "remote_page_adaptive_enabled": 0.0,
    },
    "remote_l2_only": {
        "l2_resident_filter_enabled": 0.0,
        "dram_batch_enabled": 0.0,
        "dram_row_continuation_enabled": 0.0,
        "remote_data_path_enabled": 1.0,
        "remote_dedup_enabled": 0.0,
        "remote_batching_enabled": 0.0,
        "remote_requester_l2_enabled": 1.0,
        "remote_page_adaptive_enabled": 1.0,
    },
    "all_three": {
        "l2_resident_filter_enabled": 1.0,
        "dram_batch_enabled": 1.0,
        "dram_row_continuation_enabled": 1.0,
        "remote_data_path_enabled": 1.0,
        "remote_dedup_enabled": 1.0,
        "remote_batching_enabled": 1.0,
        "remote_requester_l2_enabled": 1.0,
        "remote_page_adaptive_enabled": 1.0,
    },
    "all_three_row_off": {
        "l2_resident_filter_enabled": 1.0,
        "dram_batch_enabled": 1.0,
        "dram_row_continuation_enabled": 0.0,
        "remote_data_path_enabled": 1.0,
        "remote_dedup_enabled": 1.0,
        "remote_batching_enabled": 1.0,
        "remote_requester_l2_enabled": 1.0,
        "remote_page_adaptive_enabled": 1.0,
    },
}
MECHANISM_CONFIG_METRICS = frozenset(
    metric
    for config in EXPECTED_MECHANISM_CONFIGS.values()
    for metric in config
)
WG_PROGRESS_RE = re.compile(
    r"^WG progress GPU (\d+) kernel (\d+): (\d+)% "
    r"\(WG (\d+)/(\d+), WF (\d+)/(\d+)\)$"
)
SAMPLED_WORK_FLAG_PREFIXES = (
    "-benchmark=", "-max-wg=", "-sampled-threshold=",
    "-sampled-warmup=", "-sampled-granularity=",
    "-branch-sampled-coverage-threshold=",
    "-branch-sampled-threshold=", "-kernel-sampled-threshold=",
    "-kernel-sampled-distance-threshold=",
)
SAMPLED_WORK_SWITCHES = frozenset({
    "-sampled", "-branch-sampled", "-kernel-sampled",
})


def driver_measurement(path):
    values = {
        "total_time": None,
        "max_wg_limit": None,
        "max_wg_admitted": None,
        "max_wg_reached": 0.0,
        "max_wg_stop_completed": 0.0,
        "max_wg_launch_limited": 0.0,
        "max_wg_kernel_drained": 0.0,
        "cu_inst_count": 0.0,
        "l1v_demand_requests": 0.0,
        "mechanism_config_values": {
            metric: set() for metric in MECHANISM_CONFIG_METRICS
        },
    }
    with path.open(newline="") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            what = row["what"].strip()
            if what in MECHANISM_CONFIG_METRICS:
                values["mechanism_config_values"][what].add(
                    float(row["value"])
                )
            if what == "cu_inst_count":
                values["cu_inst_count"] += float(row["value"])
                continue
            if (
                ".L1VCache[" in row["where"].strip()
                and what in L1V_DEMAND_OUTCOMES
            ):
                values["l1v_demand_requests"] += float(row["value"])
                continue
            if row["where"].strip() != "Driver":
                continue
            if what in values:
                values[what] = float(row["value"])
    if values["total_time"] is None:
        raise ValueError(f"Driver total_time not found in {path}")
    if values["max_wg_reached"] > 0.5 and (
        values["max_wg_stop_completed"] <= 0.5
        or values["max_wg_launch_limited"] <= 0.5
        or values["max_wg_kernel_drained"] <= 0.5
    ):
        raise ValueError(
            "non-drained max-WG truncation is not valid for the paper plot: "
            f"{path}"
        )
    return values


def driver_time(path):
    return driver_measurement(path)["total_time"]


def validate_mechanism_config(measurement, path, config):
    expected = EXPECTED_MECHANISM_CONFIGS[config]
    observed = measurement["mechanism_config_values"]
    for metric, expected_value in expected.items():
        values = observed[metric]
        if values != {expected_value}:
            raise ValueError(
                f"mechanism config mismatch for {config}: {metric}="
                f"{sorted(values)}, want [{expected_value}]; file: {path}"
            )


def stdout_path_for_metrics(path):
    suffix = "_metrics.csv"
    if not path.name.endswith(suffix):
        raise ValueError(f"unexpected metrics filename: {path}")
    return path.with_name(path.name[:-len(suffix)] + "_out.stdout")


def sampled_work_signature(metrics_path, admitted_wgs=None):
    """Read completed logical work from the runner's sampled-progress log.

    Photon sampling intentionally stops detailed instruction simulation after
    its timing model converges. Therefore ``cu_inst_count`` can differ between
    two equal-work sampled runs. The progress log is generated from the fixed
    launch filters and records the complete WG/WF allocation for every GPU.
    """
    path = stdout_path_for_metrics(metrics_path)
    if not path.is_file():
        raise ValueError(f"sampled work log is missing: {path}")
    lines = path.read_text(errors="replace").splitlines()
    if not lines or not lines[0].startswith("Executing "):
        raise ValueError(f"sampled work log has no command line: {path}")
    command = shlex.split(lines[0][len("Executing "):])
    if not command or "-sampled" not in command:
        raise ValueError(f"instruction mismatch is not a sampled run: {path}")
    work_flags = tuple(sorted(
        token for token in command[1:]
        if token in SAMPLED_WORK_SWITCHES
        or token.startswith(SAMPLED_WORK_FLAG_PREFIXES)
    ))

    progress = {}
    totals = {}
    for line in lines[1:]:
        match = WG_PROGRESS_RE.match(line)
        if match is None:
            continue
        gpu, kernel, pct, wg, total_wg, wf, total_wf = map(
            int, match.groups()
        )
        key = (gpu, kernel)
        total = (total_wg, total_wf)
        if key in totals and totals[key] != total:
            raise ValueError(
                f"inconsistent sampled work totals for {key}: {path}"
            )
        totals[key] = total
        progress[key] = (pct, wg, wf)
    if not progress:
        raise ValueError(f"sampled work log has no WG progress: {path}")
    incomplete = [
        key for key, (pct, wg, wf) in progress.items()
        if pct != 100 or (wg, wf) != totals[key]
    ]
    if incomplete:
        raise ValueError(
            "sampled work log is not fully drained for "
            f"{len(incomplete)} GPU/kernel entries: {path}"
        )
    signature = tuple(
        (gpu, kernel, totals[(gpu, kernel)][0], totals[(gpu, kernel)][1])
        for gpu, kernel in sorted(totals)
    )
    total_wgs = sum(item[2] for item in signature)
    total_wfs = sum(item[3] for item in signature)
    if admitted_wgs is not None and total_wgs != int(admitted_wgs):
        raise ValueError(
            f"sampled WG signature totals {total_wgs}, but metrics report "
            f"{admitted_wgs}: {path}"
        )
    return {
        "path": str(path),
        "executable": command[0],
        "work_flags": work_flags,
        "signature": signature,
        "entries": len(signature),
        "total_wgs": total_wgs,
        "total_wfs": total_wfs,
    }


def validate_equal_sampled_work(baseline_path, experiment_path, baseline,
                                experiment):
    baseline_work = sampled_work_signature(
        baseline_path, baseline["max_wg_admitted"]
    )
    experiment_work = sampled_work_signature(
        experiment_path, experiment["max_wg_admitted"]
    )
    for field in ("executable", "work_flags", "signature"):
        if baseline_work[field] != experiment_work[field]:
            raise ValueError(
                f"sampled {field} mismatch: files: "
                f"{baseline_path}, {experiment_path}"
            )
    return baseline_work


def paired_evidence(
    baseline_path,
    experiment_path,
    experiment_config=None,
    verify_config=False,
    allow_sampled_instruction_mismatch=False,
    same_frozen_binary=False,
):
    baseline = driver_measurement(baseline_path)
    experiment = driver_measurement(experiment_path)
    if verify_config:
        validate_mechanism_config(baseline, baseline_path, "baseline")
        validate_mechanism_config(
            experiment, experiment_path, experiment_config
        )
    for field in ("max_wg_limit", "max_wg_admitted"):
        baseline_value = baseline[field]
        experiment_value = experiment[field]
        if baseline_value is None and experiment_value is None:
            continue
        if (
            baseline_value is None
            or experiment_value is None
            or baseline_value != experiment_value
        ):
            raise ValueError(
                f"paired {field} mismatch: baseline={baseline_value}, "
                f"experiment={experiment_value}; "
                f"files: {baseline_path}, {experiment_path}"
            )
    work_validation_mode = "drained_wg_and_cu_inst_count"
    validation_warning = ""
    sampled_work = None
    if baseline["cu_inst_count"] != experiment["cu_inst_count"]:
        if not allow_sampled_instruction_mismatch:
            raise ValueError(
                "paired cu_inst_count mismatch: "
                f"baseline={baseline['cu_inst_count']}, "
                f"experiment={experiment['cu_inst_count']}; "
                f"files: {baseline_path}, {experiment_path}"
            )
        if not same_frozen_binary:
            raise ValueError(
                "sampled instruction mismatch requires one identical frozen "
                "binary for both runs"
            )
        sampled_work = validate_equal_sampled_work(
            baseline_path, experiment_path, baseline, experiment
        )
        work_validation_mode = "sampled_complete_per_gpu_wg_wf_signature"
        validation_warning = (
            "cu_inst_count differs because sampled execution converged at "
            "different times; complete per-GPU WG/WF signatures match"
        )
    baseline_demands = baseline["l1v_demand_requests"]
    experiment_demands = experiment["l1v_demand_requests"]
    if baseline_demands == 0:
        signed_demand_delta_pct = (
            0.0 if experiment_demands == 0 else math.inf
        )
    else:
        signed_demand_delta_pct = (
            100.0 * (experiment_demands - baseline_demands)
            / baseline_demands
        )
    # This is a timing-sensitive transaction count, not an invariant measure
    # of program work. A downstream latency change can alter CU-side request
    # coalescing and the number of L1 hits presented as separate transactions.
    # Keep the delta in the source manifest for diagnosis. Fully detailed
    # pairs use drained/admitted WGs plus instructions; explicitly validated
    # sampled pairs use the complete per-GPU WG/WF signature instead.
    return {
        "speedup": baseline["total_time"] / experiment["total_time"],
        "baseline": baseline,
        "experiment": experiment,
        "l1v_demand_delta_pct": signed_demand_delta_pct,
        "work_validation_mode": work_validation_mode,
        "validation_warning": validation_warning,
        "sampled_work": sampled_work,
    }


def paired_speedup(baseline_path, experiment_path):
    return paired_evidence(baseline_path, experiment_path)["speedup"]


def source_record(
    label,
    name,
    baseline_path,
    experiment_path,
    evidence,
    baseline_binary_sha256="",
    experiment_binary_sha256="",
    execution_scope="full_workload",
):
    baseline = evidence["baseline"]
    experiment = evidence["experiment"]
    return {
        "benchmark": label,
        "series": name,
        "execution_scope": execution_scope,
        "baseline_metrics_file": str(baseline_path),
        "experiment_metrics_file": str(experiment_path),
        "baseline_binary_sha256": baseline_binary_sha256,
        "experiment_binary_sha256": experiment_binary_sha256,
        "speedup": f"{evidence['speedup']:.9f}",
        "baseline_driver_total_time_s": baseline["total_time"],
        "experiment_driver_total_time_s": experiment["total_time"],
        "baseline_max_wg_limit": baseline["max_wg_limit"],
        "experiment_max_wg_limit": experiment["max_wg_limit"],
        "baseline_max_wg_admitted": baseline["max_wg_admitted"],
        "experiment_max_wg_admitted": experiment["max_wg_admitted"],
        "baseline_kernel_drained": baseline["max_wg_kernel_drained"],
        "experiment_kernel_drained": experiment["max_wg_kernel_drained"],
        "baseline_cu_inst_count": baseline["cu_inst_count"],
        "experiment_cu_inst_count": experiment["cu_inst_count"],
        "work_validation_mode": evidence["work_validation_mode"],
        "sampled_work_signature_entries": (
            evidence["sampled_work"]["entries"]
            if evidence["sampled_work"] is not None else ""
        ),
        "sampled_work_total_wgs": (
            evidence["sampled_work"]["total_wgs"]
            if evidence["sampled_work"] is not None else ""
        ),
        "sampled_work_total_wfs": (
            evidence["sampled_work"]["total_wfs"]
            if evidence["sampled_work"] is not None else ""
        ),
        "validation_warning": evidence["validation_warning"],
        "baseline_l1v_demand_requests": baseline["l1v_demand_requests"],
        "experiment_l1v_demand_requests": experiment["l1v_demand_requests"],
        "l1v_demand_delta_pct": evidence["l1v_demand_delta_pct"],
    }


def metrics_path(root, benchmark, config):
    suffix = "" if config == "baseline" else f"_{config}"
    return root / f"baseline_{benchmark}_baseline{suffix}_metrics.csv"


def binary_manifest_hash(root, target="baseline", required=False):
    path = root / "EXPERIMENT_BINARIES.json"
    if not path.is_file():
        if required:
            raise FileNotFoundError(
                f"strict result directory is missing binary manifest: {path}"
            )
        return ""
    with path.open() as stream:
        manifest = json.load(stream)
    if manifest.get("version") != 1:
        raise ValueError(f"unsupported binary manifest version: {path}")
    digest = manifest.get("sha256_by_target", {}).get(target, "")
    if len(digest) != 64:
        raise ValueError(f"invalid {target} SHA-256 in {path}")
    return digest


def existing_override(root, benchmark, config):
    if root is None:
        return None
    candidate = metrics_path(root, benchmark, config)
    return candidate if candidate.is_file() else None


def load(
    first,
    supplement,
    local_override=None,
    remote_override=None,
    combined_override=None,
):
    data = {}
    sources = []
    supplement_benchmarks = {"kmeans", "matrixtranspose", "spmv"}
    for benchmark, label in WORKLOADS:
        root = supplement if benchmark in supplement_benchmarks else first
        baseline_path = metrics_path(root, benchmark, "baseline")
        data[label] = {}
        for name, config, _ in SERIES:
            experiment = metrics_path(root, benchmark, config)
            if config == "local_optimization_only":
                experiment = (
                    existing_override(local_override, benchmark, "baseline")
                    or experiment
                )
            elif config == "all_three":
                experiment = (
                    existing_override(combined_override, benchmark, "baseline")
                    or experiment
                )
            elif config in {"remote_request_only", "remote_l2_only"}:
                experiment = (
                    existing_override(remote_override, benchmark, config)
                    or experiment
                )
            evidence = paired_evidence(baseline_path, experiment)
            value = evidence["speedup"]
            data[label][name] = value
            sources.append(source_record(
                label, name, baseline_path, experiment, evidence
            ))
    data["GMEAN"] = {
        name: math.exp(sum(math.log(data[label][name]) for _, label in WORKLOADS) / len(WORKLOADS))
        for name, _, _ in SERIES
    }
    return data, sources


def load_separate(
    baseline_root,
    ablation_root,
    allow_partial=False,
    primary_scope=False,
    kmeans_membership_phase=False,
    allow_sampled_instruction_mismatch=False,
    excluded_benchmarks=(),
):
    """Load one current baseline directory and one current ablation directory."""
    data = {}
    sources = []
    baseline_binary_sha256 = binary_manifest_hash(
        baseline_root, required=primary_scope
    )
    experiment_binary_sha256 = binary_manifest_hash(
        ablation_root, required=primary_scope
    )
    if primary_scope and baseline_binary_sha256 != experiment_binary_sha256:
        raise ValueError(
            "baseline and ablation use different frozen binaries: "
            f"{baseline_binary_sha256} != {experiment_binary_sha256}"
        )
    same_frozen_binary = (
        bool(baseline_binary_sha256)
        and baseline_binary_sha256 == experiment_binary_sha256
    )
    if allow_sampled_instruction_mismatch and not same_frozen_binary:
        raise ValueError(
            "--allow-sampled-instruction-mismatch requires matching, "
            "non-empty frozen-binary manifests"
        )
    excluded_benchmarks = set(excluded_benchmarks)
    for benchmark, label in WORKLOADS:
        if benchmark in excluded_benchmarks:
            data[label] = {name: math.nan for name, _, _ in SERIES}
            continue
        if primary_scope and label == "SPMV":
            data[label] = {name: math.nan for name, _, _ in SERIES}
            continue
        metrics_benchmark = benchmark
        execution_scope = "full_workload"
        if kmeans_membership_phase and benchmark == "kmeans":
            metrics_benchmark = "kmeans-reuse-smoke"
            execution_scope = "kmeans_membership_phase"
        baseline_path = metrics_path(
            baseline_root, metrics_benchmark, "baseline"
        )
        if allow_partial and not baseline_path.is_file():
            data[label] = {name: math.nan for name, _, _ in SERIES}
            continue
        data[label] = {}
        for name, config, _ in SERIES:
            experiment = metrics_path(
                ablation_root, metrics_benchmark, config
            )
            if allow_partial and not experiment.is_file():
                data[label][name] = math.nan
                continue
            evidence = paired_evidence(
                baseline_path,
                experiment,
                experiment_config=config,
                verify_config=primary_scope,
                allow_sampled_instruction_mismatch=(
                    allow_sampled_instruction_mismatch
                ),
                same_frozen_binary=same_frozen_binary,
            )
            value = evidence["speedup"]
            data[label][name] = value
            record_scope = execution_scope
            if (
                record_scope == "full_workload"
                and evidence["baseline"]["max_wg_reached"] > 0.5
                and evidence["experiment"]["max_wg_reached"] > 0.5
            ):
                record_scope = "bounded_max_wg_window"
            sources.append(source_record(
                label,
                name,
                baseline_path,
                experiment,
                evidence,
                baseline_binary_sha256,
                experiment_binary_sha256,
                record_scope,
            ))
    data["GMEAN"] = {
        name: geomean_finite([data[label][name] for _, label in WORKLOADS])
        for name, _, _ in SERIES
    }
    return data, sources


def scoped_workloads(primary_scope=False):
    if primary_scope:
        return [item for item in WORKLOADS if item[1] != "SPMV"]
    return WORKLOADS


def write_csv(path, data, primary_scope=False):
    labels = [
        label for _, label in scoped_workloads(primary_scope)
    ] + ["GMEAN"]
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["benchmark"] + [name for name, _, _ in SERIES])
        for label in labels:
            writer.writerow([label] + [
                f"{data[label][name]:.9f}"
                if math.isfinite(data[label][name]) else ""
                for name, _, _ in SERIES
            ])


def geomean(values):
    return math.exp(sum(math.log(value) for value in values) / len(values))


def geomean_finite(values):
    finite = [value for value in values if math.isfinite(value)]
    return geomean(finite) if finite else math.nan


def write_goal_summary(path, data, primary_scope=False):
    """Write the coverage gates used to evaluate the paper's design goal.

    A strict ``>1`` count is retained for completeness, but the 1.01x gate is
    the useful coverage number: sub-percent differences are too close to run
    truncation and simulator noise to claim that a mechanism helps.
    """
    summary_workloads = scoped_workloads(primary_scope)
    labels = [label for _, label in summary_workloads]
    with path.open("w", newline="") as stream:
        fields = [
            "series", "completed_count", "expected_count", "geomean",
            "positive_count", "positive_coverage_pct",
            "one_pct_count", "one_pct_coverage_pct", "five_pct_count",
            "five_pct_coverage_pct", "best_benchmark", "best_speedup",
            "geomean_without_best", "one_pct_benchmarks",
            "meets_geomean_target", "meets_positive_coverage_target",
            "meets_one_pct_coverage_target", "meets_paper_goal",
            "target_scope", "target_completed_count",
            "target_expected_count", "target_geomean",
            "target_one_pct_count", "target_required_one_pct_count",
            "target_one_pct_coverage_pct",
            "target_meets_geomean_target",
            "target_meets_one_pct_coverage_target",
        ]
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        for name, _, _ in SERIES:
            pairs = [
                (label, data[label][name])
                for label in labels
                if math.isfinite(data[label][name])
            ]
            positive = [label for label, value in pairs if value > 1.0]
            one_pct = [label for label, value in pairs if value >= 1.01]
            five_pct = [label for label, value in pairs if value >= 1.05]
            if pairs:
                best_label, best_value = max(pairs, key=lambda item: item[1])
                without_best = [
                    value for label, value in pairs if label != best_label
                ]
                current_geomean = geomean([value for _, value in pairs])
                geomean_without_best = geomean_finite(without_best)
                positive_coverage = 100 * len(positive) / len(pairs)
                one_pct_coverage = 100 * len(one_pct) / len(pairs)
                five_pct_coverage = 100 * len(five_pct) / len(pairs)
            else:
                best_label, best_value = "", math.nan
                current_geomean = math.nan
                geomean_without_best = math.nan
                positive_coverage = math.nan
                one_pct_coverage = math.nan
                five_pct_coverage = math.nan
            complete = len(pairs) == len(summary_workloads)
            # A partial subset is useful for live progress, but must never
            # satisfy the paper-wide gate.
            meets_geomean = (
                complete and current_geomean >= PAPER_GEOMEAN_TARGET
            )
            meets_positive_coverage = (
                complete and positive_coverage >= PAPER_COVERAGE_TARGET_PCT
            )
            meets_one_pct_coverage = (
                complete and one_pct_coverage >= PAPER_COVERAGE_TARGET_PCT
            )
            target_pairs = [
                (label, value)
                for label, value in pairs
                if label in OPTIMIZATION_TARGET_LABELS
            ]
            target_complete = (
                len(target_pairs) == len(OPTIMIZATION_TARGET_LABELS)
            )
            target_geomean = geomean_finite(
                [value for _, value in target_pairs]
            )
            target_one_pct = [
                label for label, value in target_pairs if value >= 1.01
            ]
            target_one_pct_coverage = (
                100 * len(target_one_pct) / len(target_pairs)
                if target_pairs else math.nan
            )
            required_one_pct_count = math.ceil(
                PAPER_COVERAGE_TARGET_PCT *
                len(OPTIMIZATION_TARGET_LABELS) / 100.0
            )
            target_meets_geomean = (
                target_complete and target_geomean >= PAPER_GEOMEAN_TARGET
            )
            target_meets_one_pct_coverage = (
                target_complete
                and target_one_pct_coverage >= PAPER_COVERAGE_TARGET_PCT
            )
            writer.writerow({
                "series": name,
                "completed_count": len(pairs),
                "expected_count": len(summary_workloads),
                "geomean": finite_text(current_geomean, 9),
                "positive_count": len(positive),
                "positive_coverage_pct": finite_text(positive_coverage, 6),
                "one_pct_count": len(one_pct),
                "one_pct_coverage_pct": finite_text(one_pct_coverage, 6),
                "five_pct_count": len(five_pct),
                "five_pct_coverage_pct": finite_text(five_pct_coverage, 6),
                "best_benchmark": best_label,
                "best_speedup": finite_text(best_value, 9),
                "geomean_without_best": finite_text(
                    geomean_without_best, 9
                ),
                "one_pct_benchmarks": ";".join(one_pct),
                "meets_geomean_target": meets_geomean,
                "meets_positive_coverage_target": meets_positive_coverage,
                "meets_one_pct_coverage_target": meets_one_pct_coverage,
                "meets_paper_goal": (
                    target_meets_geomean
                    and target_meets_one_pct_coverage
                ),
                "target_scope": "all_except_SPMV",
                "target_completed_count": len(target_pairs),
                "target_expected_count": len(OPTIMIZATION_TARGET_LABELS),
                "target_geomean": finite_text(target_geomean, 9),
                "target_one_pct_count": len(target_one_pct),
                "target_required_one_pct_count": required_one_pct_count,
                "target_one_pct_coverage_pct": finite_text(
                    target_one_pct_coverage, 6
                ),
                "target_meets_geomean_target": target_meets_geomean,
                "target_meets_one_pct_coverage_target": (
                    target_meets_one_pct_coverage
                ),
            })


def finite_text(value, digits):
    return f"{value:.{digits}f}" if math.isfinite(value) else ""


def write_sources(path, sources):
    with path.open("w", newline="") as stream:
        fields = [
            "benchmark", "series", "execution_scope",
            "baseline_metrics_file",
            "experiment_metrics_file", "baseline_binary_sha256",
            "experiment_binary_sha256", "speedup",
            "baseline_driver_total_time_s",
            "experiment_driver_total_time_s",
            "baseline_max_wg_limit", "experiment_max_wg_limit",
            "baseline_max_wg_admitted", "experiment_max_wg_admitted",
            "baseline_kernel_drained", "experiment_kernel_drained",
            "baseline_cu_inst_count", "experiment_cu_inst_count",
            "work_validation_mode", "sampled_work_signature_entries",
            "sampled_work_total_wgs", "sampled_work_total_wfs",
            "validation_warning",
            "baseline_l1v_demand_requests",
            "experiment_l1v_demand_requests", "l1v_demand_delta_pct",
        ]
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(sources)


def plot(
    output_base,
    data,
    primary_scope=False,
    kmeans_membership_phase=False,
):
    plt.rcParams.update({
        "font.family": "DejaVu Sans", "pdf.fonttype": 42,
        "ps.fonttype": 42, "axes.linewidth": .8,
    })
    plot_workloads = scoped_workloads(primary_scope)
    labels = [label for _, label in plot_workloads] + ["GMEAN"]
    display_labels = [
        "KM*" if kmeans_membership_phase and label == "KM" else label
        for label in labels
    ]
    x_step = .82
    xs = np.arange(len(labels), dtype=float) * x_step
    group_width = .56
    bar_width = group_width / len(SERIES)
    ylimit = 2.0
    fig, ax = plt.subplots(figsize=(3.45, 1.58))

    completed_counts = {
        name: sum(
            math.isfinite(data[label][name]) for _, label in plot_workloads
        )
        for name, _, _ in SERIES
    }
    if any(count < len(plot_workloads) for count in completed_counts.values()):
        short_names = {
            "Local Opt.": "M1", "Remote Request": "M2",
            "Remote L2": "M3", "Combined": "Combined",
        }
        progress = ", ".join(
            f"{short_names[name]} {completed_counts[name]}/{len(plot_workloads)}"
            for name, _, _ in SERIES
        )
        ax.text(
            .99, .96, "Partial: " + progress,
            transform=ax.transAxes, ha="right", va="top",
            fontsize=4.7, color="#656667", zorder=5,
        )

    gx = xs[-1]
    ax.axvspan(gx - group_width * .5, gx + group_width * .5,
               color="#f5f5f5", zorder=0)
    for idx, (name, _, color) in enumerate(SERIES):
        offset = (idx - (len(SERIES) - 1) / 2) * bar_width
        values = np.array([data[label][name] for label in labels])
        bars = ax.bar(xs + offset, np.minimum(values, ylimit),
                      width=bar_width * .94, color=color,
                      edgecolor="none", linewidth=0, zorder=3)
        for label, bar, value in zip(labels, bars, values):
            if value > ylimit:
                ax.text(bar.get_x() + bar.get_width()/2
                        + CLIPPED_LABEL_X_OFFSETS.get((label, name), 0),
                        ylimit + .025,
                        f"{value:.1f}", ha="center", va="bottom",
                        fontsize=4.9, fontweight="bold", color="#C88D04",
                        clip_on=False)

    ax.axhline(1, color="#333333", linestyle=(0, (4, 2)), linewidth=.9, zorder=2)
    plot_groups = [
        ("All Local", 0, 7),
        ("Mixed", 7, 11),
        ("Remote", 11, len(plot_workloads)),
    ]
    for _, _, boundary in plot_groups[:-1]:
        ax.axvline((xs[boundary - 1] + xs[boundary]) / 2,
                   color="#888888", linestyle=":", linewidth=.7)
    ax.axvline(gx - x_step/2, color="#888888", linestyle=":", linewidth=.7)
    ax.set_ylim(0, ylimit)
    ax.set_ylabel("Speedup", fontsize=7)
    ax.set_yticks([0, 1, 2]); ax.set_yticklabels(["0", "1", "2"])
    ax.set_xticks(xs); ax.set_xticklabels([])
    ax.set_xlim(xs[0] - group_width*.62, xs[-1] + group_width*.62)
    ax.grid(axis="y", color="#dddddd", linewidth=.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6, length=2.4, width=.7)
    for xpos, label in zip(xs, display_labels):
        ax.text(xpos, -.08, label, transform=ax.get_xaxis_transform(),
                ha="center", va="top", rotation=30,
                rotation_mode="anchor", fontsize=6, clip_on=False)
    for group_name, begin, end in plot_groups:
        center = (xs[begin] + xs[end - 1]) / 2
        ax.text(center, -.36, group_name, transform=ax.get_xaxis_transform(),
                ha="center", va="top", fontsize=5.2,
                fontweight="bold", color="#656667", clip_on=False)
    if kmeans_membership_phase:
        ax.text(
            xs[0], -.52, "* KM: two-iteration membership phase",
            transform=ax.get_xaxis_transform(), ha="left", va="top",
            fontsize=4.7, color="#656667", clip_on=False,
        )
    for spine in ax.spines.values():
        spine.set_visible(True); spine.set_linewidth(.7)
    handles = [Patch(facecolor=color, edgecolor="none", label=name)
               for name, _, color in SERIES]
    ax.legend(handles=handles, loc="lower left",
              bbox_to_anchor=(.02, 1.06, .96, .16), ncol=2, mode="expand",
              frameon=False, columnspacing=.9, handlelength=1,
              handletextpad=.45, fontsize=5.7)
    fig.tight_layout(pad=.25)
    fig.savefig(output_base.with_suffix(".png"), dpi=300, bbox_inches="tight")
    fig.savefig(output_base.with_suffix(".pdf"), bbox_inches="tight")
    plt.close(fig)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("first", type=Path, nargs="?")
    parser.add_argument("supplement", type=Path, nargs="?")
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument(
        "--baseline-dir",
        type=Path,
        help="directory containing 14 full or 13 primary baseline metrics",
    )
    parser.add_argument(
        "--ablation-dir",
        type=Path,
        help=(
            "directory containing 14 full or 13 primary "
            "M1/M2/M3/Combined metrics"
        ),
    )
    parser.add_argument(
        "--allow-partial",
        action="store_true",
        help=(
            "allow missing files in --baseline-dir/--ablation-dir mode; "
            "leave missing cells blank and never pass the 14-workload gate"
        ),
    )
    parser.add_argument(
        "--primary-scope",
        action="store_true",
        help=(
            "require all 13 primary workloads and intentionally omit only "
            "SPMV; unlike --allow-partial, any other missing result fails"
        ),
    )
    parser.add_argument(
        "--kmeans-membership-phase",
        action="store_true",
        help=(
            "read kmeans-reuse-smoke metrics for the KM point, mark the "
            "figure KM*, and record kmeans_membership_phase provenance"
        ),
    )
    parser.add_argument(
        "--allow-sampled-instruction-mismatch",
        action="store_true",
        help=(
            "allow unequal detailed cu_inst_count only for matching frozen "
            "sampled runs whose fully drained per-GPU WG/WF signatures are "
            "identical; record the alternate validation mode in sources"
        ),
    )
    parser.add_argument(
        "--exclude-benchmark",
        action="append",
        choices=[benchmark for benchmark, _ in WORKLOADS],
        default=[],
        help=(
            "temporarily leave one benchmark blank in partial analysis; "
            "repeatable and forbidden in strict --primary-scope mode"
        ),
    )
    parser.add_argument(
        "--local-override-dir",
        type=Path,
        help="optional directory of corrected Local-only baseline-named metrics",
    )
    parser.add_argument(
        "--remote-override-dir",
        type=Path,
        help="optional directory of Remote-Request/Remote-L2 ablation metrics",
    )
    parser.add_argument(
        "--combined-override-dir",
        type=Path,
        help="optional directory of corrected Combined baseline-named metrics",
    )
    args = parser.parse_args()
    if args.allow_partial and args.primary_scope:
        parser.error(
            "--primary-scope is already strict for 13 workloads and cannot "
            "be combined with --allow-partial"
        )
    if args.exclude_benchmark and not args.allow_partial:
        parser.error("--exclude-benchmark requires --allow-partial")
    if args.kmeans_membership_phase and not (
        args.baseline_dir is not None and args.ablation_dir is not None
    ):
        parser.error(
            "--kmeans-membership-phase requires "
            "--baseline-dir/--ablation-dir"
        )
    if args.allow_sampled_instruction_mismatch and not (
        args.baseline_dir is not None and args.ablation_dir is not None
    ):
        parser.error(
            "--allow-sampled-instruction-mismatch requires "
            "--baseline-dir/--ablation-dir"
        )
    args.output_dir.mkdir(parents=True, exist_ok=True)
    separate_mode = args.baseline_dir is not None or args.ablation_dir is not None
    if separate_mode:
        if args.baseline_dir is None or args.ablation_dir is None:
            parser.error("--baseline-dir and --ablation-dir must be used together")
        if args.first is not None or args.supplement is not None:
            parser.error(
                "positional legacy result directories cannot be combined with "
                "--baseline-dir/--ablation-dir"
            )
        data, sources = load_separate(
            args.baseline_dir,
            args.ablation_dir,
            allow_partial=args.allow_partial,
            primary_scope=args.primary_scope,
            kmeans_membership_phase=args.kmeans_membership_phase,
            allow_sampled_instruction_mismatch=(
                args.allow_sampled_instruction_mismatch
            ),
            excluded_benchmarks=args.exclude_benchmark,
        )
    else:
        if args.allow_partial:
            parser.error("--allow-partial requires --baseline-dir/--ablation-dir")
        if args.primary_scope:
            parser.error("--primary-scope requires --baseline-dir/--ablation-dir")
        if args.first is None or args.supplement is None:
            parser.error(
                "provide FIRST SUPPLEMENT or --baseline-dir/--ablation-dir"
            )
        data, sources = load(
            args.first,
            args.supplement,
            args.local_override_dir,
            args.remote_override_dir,
            args.combined_override_dir,
        )
    base = args.output_dir / "complete_ablation_speedup"
    write_csv(base.with_suffix(".csv"), data, args.primary_scope)
    goal_summary = args.output_dir / "complete_ablation_goal_summary.csv"
    write_goal_summary(goal_summary, data, args.primary_scope)
    source_manifest = args.output_dir / "complete_ablation_sources.csv"
    write_sources(source_manifest, sources)
    plot(
        base,
        data,
        args.primary_scope,
        args.kmeans_membership_phase,
    )
    print(base.with_suffix(".png"))
    print(base.with_suffix(".pdf"))
    print(base.with_suffix(".csv"))
    print(goal_summary)
    print(source_manifest)


if __name__ == "__main__":
    main()
