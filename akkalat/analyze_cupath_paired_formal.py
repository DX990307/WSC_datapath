#!/usr/bin/env python3
"""Strict formal audit for the full-workload paired-read CuPath campaign.

The historical V5 analyzer intentionally remains tied to its capped workload
and old M1.  This analyzer accepts only the current 14-workload, five-config
campaign, passive natural-completion WG evidence, and the two-physical-64-B
paired-read accounting model.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import math
from pathlib import Path

import analyze_m1_paired_read as paired
import plot_cupath_paired_formal as formal_plot
from analyze_wg_mapping import audit as audit_wg_mapping
from analyze_wg_mapping import write_outputs as write_wg_mapping_outputs


WORKLOADS = (
    "aes", "bitonicsort", "fastwalshtransform", "fft", "fir", "relu",
    "simpleconvolution", "floydwarshall", "kmeans",
    "matrixmultiplication", "pagerank", "im2col", "matrixtranspose",
    "spmv",
)
CONFIGS = ("baseline", "m1", "m2", "m3", "complete")
DIAGNOSTIC_WORKLOADS = ("aes", "fft", "fir", "kmeans", "spmv")
DIAGNOSTIC_CONFIGS = (
    "baseline", "old_m1_independent_prefetch", "cuckoo_filter_only",
    "always_pair", "predictor_only", "paired_read_without_filter",
    "new_m1",
)
GROUPS = {
    "All Local": WORKLOADS[:7],
    "Mixed": WORKLOADS[7:11],
    "Remote": WORKLOADS[11:],
    "Overall": WORKLOADS,
}

FIXED_FLAGS = {
    "-timing",
    "-num-memory-banks=4",
    "-l1v-mshr-entries=16",
    "-bandwidth=48",
    "-switch-latency=32",
    "-rdma-pipeline-width=8",
    "-rdma-pipeline-latency=10",
    "-rdma-max-outstanding=64",
    "-magic-memory-copy",
    "-report-all",
    "-disable-servers",
    "-mmutlb-lookup-latency=80",
    "-typed-filter-slots-per-bucket=4",
    "-typed-filter-fingerprint-bits=13",
    "-typed-filter-lookup-latency=1",
    "-typed-filter-lookup-width=16",
    "-typed-filter-update-latency=1",
    "-typed-filter-update-width=16",
    "-prefetch-predictor-entries=256",
    "-sampled",
    "-branch-sampled",
    "-kernel-sampled",
    "-remote-data-path-batch-lines=8",
    "-remote-data-path-batches=64",
    "-typed-filter-mode=cuckoo",
}

MECHANISM_FIELDS = (
    "l2-resident-filter-enable",
    "l2-fill-forwarding-enable",
    "dram-row-continuation-enable",
    "l2-filter-prefetch-enable",
    "l2-prefetch-predictor-only",
    "l2-prefetch-ungated",
    "l2-granularity-adaptation-enable",
    "l2-granularity-without-filter",
    "l2-granularity-always-expand",
    "l2-granularity-predictor-only",
    "remote-data-path-enable",
    "remote-data-path-dedup-enable",
    "remote-data-path-batching-enable",
    "remote-data-path-l2-enable",
    "remote-filter-prefetch-enable",
)


def mechanism_values(config: str) -> tuple[bool, ...]:
    local = config in {"m1", "complete"}
    remote = config in {"m2", "m3", "complete"}
    m2 = config in {"m2", "complete"}
    m3 = config in {"m3", "complete"}
    return (
        False, False, False, False, False, False,
        local, False, False, False,
        remote, m2, m2, m3, m2,
    )


def expected_mechanism_tokens(config: str) -> set[str]:
    return {
        f"-{field}={str(value).lower()}"
        for field, value in zip(MECHANISM_FIELDS, mechanism_values(config))
    }


def file_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def audit_experiment_metadata(
    root: Path, expected_sha256: str | None = None
) -> dict:
    metadata = json.loads(
        (root / "EXPERIMENT_METADATA.json").read_text(encoding="utf-8")
    )
    manifest = json.loads(
        (root / "EXPERIMENT_BINARIES.json").read_text(encoding="utf-8")
    )
    experiments = metadata.get("experiments", [])
    expected_cells = {(b, c) for b in WORKLOADS for c in CONFIGS}
    if metadata.get("experiment_count") != len(expected_cells) or \
            len(experiments) != len(expected_cells):
        raise ValueError(
            f"formal metadata has {len(experiments)}/{len(expected_cells)} cells"
        )

    seen = set()
    binary_paths = set()
    common_signatures = set()
    mechanism_prefixes = tuple(f"-{field}=" for field in MECHANISM_FIELDS)
    for record in experiments:
        benchmark = record.get("benchmark")
        config = record.get("configuration")
        cell = (benchmark, config)
        if cell not in expected_cells or cell in seen:
            raise ValueError(f"unexpected or duplicate formal cell {cell}")
        seen.add(cell)
        command = record.get("command", [])
        if not command or not all(isinstance(token, str) for token in command):
            raise ValueError(f"invalid command for {cell}")
        flags = command[1:]
        if len(flags) != len(set(flags)):
            raise ValueError(f"duplicate flags for {cell}")
        tokens = set(flags)
        missing = FIXED_FLAGS - tokens
        if missing:
            raise ValueError(f"{cell} missing fixed flags {sorted(missing)}")
        max_flags = [token for token in flags if token.startswith("-max-wg=")]
        if any(token != "-max-wg=0" for token in max_flags):
            raise ValueError(f"{cell} uses a positive or invalid max-wg prefix")
        expected_mechanisms = expected_mechanism_tokens(config)
        actual_mechanisms = {
            token for token in tokens if token.startswith(mechanism_prefixes)
        }
        if actual_mechanisms != expected_mechanisms:
            raise ValueError(f"{cell} mechanism matrix mismatch")
        benchmark_flag = f"-benchmark={benchmark}"
        metric_flag = (
            f"-metric-file-name="
            f"{(root / f'baseline_{benchmark}_{config}_metrics').resolve()}"
        )
        if benchmark_flag not in tokens or metric_flag not in tokens:
            raise ValueError(f"{cell} benchmark or metric destination mismatch")
        forbidden_words = (
            "128b", "128-b", "128-byte", "hlq", "batching-timeout",
            "prefetch-wait", "force-local-data-access",
        )
        if any(any(word in token.lower() for word in forbidden_words)
               for token in tokens):
            raise ValueError(f"{cell} contains a forbidden formal flag")
        common = tokens - actual_mechanisms - {
            benchmark_flag, metric_flag, "-max-wg=0",
        }
        common_signatures.add(tuple(sorted(common)))
        binary_paths.add(command[0])

    if seen != expected_cells:
        raise ValueError("formal metadata does not cover the exact 14x5 grid")
    if len(common_signatures) != 1:
        raise ValueError("formal cells do not share one common flag signature")
    if len(binary_paths) != 1:
        raise ValueError("formal metadata mixes simulator binaries")
    binary = Path(next(iter(binary_paths))).resolve()
    if not binary.is_file():
        raise ValueError(f"formal binary is missing: {binary}")
    actual_sha = file_sha256(binary)
    manifest_sha = manifest.get("sha256_by_target", {}).get("baseline")
    if actual_sha != manifest_sha:
        raise ValueError(
            f"formal binary hash differs from manifest: {actual_sha} != {manifest_sha}"
        )
    if expected_sha256 and actual_sha != expected_sha256:
        raise ValueError(
            f"formal binary hash differs from expected: {actual_sha} != {expected_sha256}"
        )
    return {
        "binary": str(binary),
        "binary_sha256": actual_sha,
        "launcher": metadata.get("launcher", {}),
        "common_flags": list(next(iter(common_signatures))),
        "cell_count": len(seen),
    }


def strict_result_rows(root: Path) -> list[dict]:
    expected_paths = {
        root / f"baseline_{benchmark}_{config}_result.json"
        for benchmark in WORKLOADS for config in CONFIGS
    }
    observed_paths = set(root.glob("*_result.json"))
    if observed_paths != expected_paths:
        raise ValueError(
            f"formal result grid mismatch: {len(observed_paths)}/"
            f"{len(expected_paths)}"
        )
    rows = paired.add_baseline_speedups(paired.summarize_directory(root))
    if {(row["benchmark"], row["config"]) for row in rows} != {
        (benchmark, config) for benchmark in WORKLOADS for config in CONFIGS
    }:
        raise ValueError("summarized formal grid does not match 14x5 scope")
    invalid = accounting_invalid_rows(rows)
    if invalid:
        raise ValueError(
            "paired/physical accounting failed for "
            + ", ".join(
                f'{row["benchmark"]}/{row["config"]}' for row in invalid
            )
        )
    errors = paired.full_workload_errors(rows)
    if errors:
        raise ValueError("; ".join(errors))
    return rows


def accounting_invalid_rows(rows: list[dict]) -> list[dict]:
    return [
        row for row in rows
        if not row["success"] or not row["physical_read_relation_ok"]
        or not row["physical_read_byte_relation_ok"]
        or not row["paired_member_relation_ok"]
        or not row["sibling_timeliness_partition_ok"]
        or not row["sibling_terminal_partition_ok"]
        or not row["aggregate_row_reuse_bounded"]
        or not row["dram_physical_access_unit_consistent"]
        or not row["general_row_continuation_disabled"]
        or not row["aggregate_continuation_mode_ok"]
        or not row["granularity_runtime_mode_ok"]
    ]


def strict_diagnostic_rows(
    root: Path, expected_sha256: str
) -> tuple[list[dict], list[dict], dict]:
    rows = paired.add_baseline_speedups(paired.summarize_directory(root))
    identity = paired.audit_campaign_identity(
        root,
        rows,
        DIAGNOSTIC_WORKLOADS,
        DIAGNOSTIC_CONFIGS,
        expected_sha256,
    )
    audit_wg_mapping(
        root,
        list(DIAGNOSTIC_WORKLOADS),
        DIAGNOSTIC_CONFIGS,
        require_baseline_match=True,
    )
    invalid = accounting_invalid_rows(rows)
    if invalid:
        raise ValueError(
            "diagnostic paired/physical accounting failed for "
            + ", ".join(
                f'{row["benchmark"]}/{row["config"]}' for row in invalid
            )
        )
    errors = paired.full_workload_errors(rows)
    if errors:
        raise ValueError("diagnostic full-workload audit failed: " + "; ".join(errors))
    retention = paired.retention_summary(rows, "new_m1")
    if not retention:
        raise ValueError("diagnostic campaign has no M1 retention evidence")
    return rows, retention, identity


def group_rows(rows: list[dict]) -> list[dict]:
    by_cell = {(row["benchmark"], row["config"]): row for row in rows}
    output = []
    for group, workloads in GROUPS.items():
        for config in CONFIGS:
            values = [
                by_cell[(benchmark, config)]["speedup_vs_baseline"]
                for benchmark in workloads
            ]
            if any(value <= 0 for value in values):
                raise ValueError(f"missing speedup in {group}/{config}")
            output.append({
                "group": group,
                "config": config,
                "benchmark_count": len(values),
                "geomean_speedup": math.exp(
                    sum(math.log(value) for value in values) / len(values)
                ),
                "positive_count": sum(value > 1.005 for value in values),
                "neutral_count": sum(0.995 <= value <= 1.005 for value in values),
                "negative_count": sum(value < 0.995 for value in values),
            })
    return output


def write_csv(path: Path, rows: list[dict]) -> None:
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def formal_retention_evidence(rows: list[dict]) -> dict:
    m1_rows = [row for row in rows if row["config"] == "m1"]
    applicable = [
        row for row in m1_rows
        if row["granularity_frontend_paired_read_aggregates"] > 0
    ]
    positive = sum(
        row["speedup_vs_baseline"] > 1.005 for row in applicable
    )
    m1_majority = bool(applicable) and positive > len(applicable) / 2

    complete = [
        row["speedup_vs_baseline"]
        for row in rows if row["config"] == "complete"
    ]
    complete_geomean = (
        math.exp(sum(math.log(value) for value in complete) / len(complete))
        if complete and all(value > 0 for value in complete) else 0.0
    )
    complete_negative = sum(value < 0.995 for value in complete)
    # "No systematic regression" is operationalized globally rather than
    # per benchmark: Complete must stay within 0.5% of Baseline in geomean and
    # regress on no more than half of the declared 14-workload suite.
    complete_safe = (
        len(complete) == len(WORKLOADS)
        and complete_geomean >= 0.995
        and complete_negative <= len(complete) / 2
    )
    failures = []
    if not m1_majority:
        failures.append("no_formal_positive_majority_on_applicable_workloads")
    if not complete_safe:
        failures.append("complete_has_systematic_regression")
    return {
        "m1_applicable_benchmark_count": len(applicable),
        "m1_positive_benchmark_count": positive,
        "m1_formal_positive_majority": int(m1_majority),
        "complete_geomean_speedup": complete_geomean,
        "complete_negative_benchmark_count": complete_negative,
        "complete_no_systematic_regression": int(complete_safe),
        "formal_gate_failures": failures,
    }


def retention_decision(
    retention: list[dict], formal_evidence: dict | None = None
) -> tuple[str, list[str]]:
    """Separate valid campaign evidence from the architectural decision."""
    if not retention:
        return "unproven", ["missing_m1_retention_evidence"]
    row = retention[0]
    failures = [
        item for item in str(
            row.get("diagnostic_retain_gate_failures", "")
        ).split(";") if item
    ]
    passed = int(row.get("diagnostic_retain_gate_pass", 0)) == 1
    if formal_evidence is not None:
        failures.extend(formal_evidence.get("formal_gate_failures", []))
    if passed and not failures:
        return "retain", []
    if not failures:
        failures = ["m1_retention_gate_failed_without_reason"]
    return "reject", failures


def write_audit(
    root: Path,
    metadata: dict,
    rows: list[dict],
    diagnostic_identity: dict,
    retention: list[dict],
) -> None:
    groups = group_rows(rows)
    formal_evidence = formal_retention_evidence(rows)
    decision, decision_reasons = retention_decision(
        retention, formal_evidence)
    payload = {
        "version": 1,
        "status": "valid_results",
        "campaign_validation": "passed",
        "m1_decision": decision,
        "m1_decision_reasons": decision_reasons,
        "diagnostic_campaign": diagnostic_identity,
        "formal_retention_evidence": formal_evidence,
        **metadata,
        "workload_count": len(WORKLOADS),
        "configuration_count": len(CONFIGS),
        "group_results": groups,
        "m1_retention": retention,
    }
    (root / "CUPATH_PAIRED_FORMAL_AUDIT.json").write_text(
        json.dumps(payload, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    overall = {
        row["config"]: row for row in groups if row["group"] == "Overall"
    }
    lines = [
        "# CuPath paired-read formal audit",
        "",
        f"- Binary SHA-256: `{metadata['binary_sha256']}`",
        f"- Completed cells: {metadata['cell_count']}/70",
        "- Workload execution: natural completion with identical per-benchmark "
        "kernel-launch and WG-set identities across all five configurations",
        "- Physical model: every paired descriptor is audited as two independent "
        "64-B frontend and physical HBM reads",
        f"- M1 retention decision: **{decision.upper()}**",
        "- M1 decision reasons: " + (
            ", ".join(decision_reasons) if decision_reasons else "all gates passed"
        ),
        f"- Diagnostic cells: {diagnostic_identity['cell_count']}/35, same "
        f"binary SHA-256 `{diagnostic_identity['binary_sha256']}`",
        "",
        "## Overall geometric mean",
        "",
    ]
    for config in CONFIGS:
        lines.append(
            f"- {config}: {overall[config]['geomean_speedup']:.4f}x "
            f"({overall[config]['positive_count']} positive, "
            f"{overall[config]['neutral_count']} neutral, "
            f"{overall[config]['negative_count']} negative)"
        )
    (root / "CUPATH_PAIRED_FORMAL_AUDIT.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument(
        "--diagnostic-results", type=Path, required=True,
        help=(
            "strict natural-completion 5x7 M1 diagnostic campaign produced "
            "by the same immutable binary"
        ),
    )
    parser.add_argument("--expected-sha256")
    args = parser.parse_args()
    root = args.results.resolve()
    metadata = audit_experiment_metadata(root, args.expected_sha256)
    rows = strict_result_rows(root)
    _, retention, diagnostic_identity = strict_diagnostic_rows(
        args.diagnostic_results.resolve(), metadata["binary_sha256"])
    paired.write_summary(rows, root / "cupath_paired_formal_ablation.csv")
    paired.write_geomeans(
        rows, root / "cupath_paired_formal_ablation_geomean.csv"
    )
    write_csv(root / "cupath_paired_formal_groups.csv", group_rows(rows))
    mapping_rows = audit_wg_mapping(
        root, list(WORKLOADS), CONFIGS, require_baseline_match=True
    )
    write_wg_mapping_outputs(root, mapping_rows)
    write_audit(root, metadata, rows, diagnostic_identity, retention)
    formal_plot.plot(
        formal_plot.read_strict_grid(
            root / "cupath_paired_formal_ablation.csv"
        ),
        root / "cupath_paired_overall_speedup.png",
    )
    print(root / "CUPATH_PAIRED_FORMAL_AUDIT.md")


if __name__ == "__main__":
    main()
