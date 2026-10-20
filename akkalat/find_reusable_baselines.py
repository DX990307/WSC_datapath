#!/usr/bin/env python3
"""Find reusable Baseline cells by modeled configuration, not binary hash."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
from pathlib import Path


PAPER_BENCHMARKS = (
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fft",
    "fir",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
)

CORE_FLAGS = {
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
    "-mmutlb-lookup-latency=80",
    "-max-wg=78600",
    "-sampled",
    "-branch-sampled",
    "-kernel-sampled",
}


def normalized_config(command: list[str]) -> tuple[str, ...]:
    """Remove only binary-, artifact-, and workload-identity fields."""
    normalized = []
    for token in command[1:]:
        token = str(token)
        if token.startswith("-benchmark="):
            continue
        if token.startswith("-metric-file-name="):
            continue
        normalized.append(token)
    return tuple(sorted(normalized))


def config_digest(config: tuple[str, ...]) -> str:
    return hashlib.sha256("\n".join(config).encode("utf-8")).hexdigest()


def load_json(path: Path) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    return value if isinstance(value, dict) else {}


def experiments_by_identity(directory: Path) -> dict[tuple[str, str, str], dict]:
    metadata = load_json(directory / "EXPERIMENT_METADATA.json")
    result = {}
    for exp in metadata.get("experiments", []):
        if not isinstance(exp, dict):
            continue
        key = (
            str(exp.get("target", "")),
            str(exp.get("benchmark", "")),
            str(exp.get("configuration", "")),
        )
        result[key] = exp
    return result


def find_reference(results_dir: Path) -> tuple[tuple[str, ...], Path]:
    candidates = []
    for metadata_path in results_dir.rglob("EXPERIMENT_METADATA.json"):
        metadata = load_json(metadata_path)
        created = str(metadata.get("created_at", ""))
        for exp in metadata.get("experiments", []):
            if not isinstance(exp, dict) or exp.get("configuration") != "baseline":
                continue
            command = [str(token) for token in exp.get("command", [])]
            config = normalized_config(command)
            if CORE_FLAGS.issubset(config):
                candidates.append((created, str(metadata_path), config, metadata_path))
    if not candidates:
        raise ValueError("no planned Baseline command matches the 78,600-WG protocol")
    _, _, config, path = max(candidates)
    return config, path


def driver_time(metrics_path: Path) -> float:
    try:
        with metrics_path.open(newline="", encoding="utf-8") as stream:
            for row in csv.DictReader(stream, skipinitialspace=True):
                if row.get("where", "").strip() == "Driver" and \
                        row.get("what", "").strip() == "total_time":
                    return float(row["value"])
    except (OSError, ValueError, KeyError):
        pass
    return 0.0


def valid_bounded_mapping(mapping: dict) -> bool:
    if int(mapping.get("max_wg", 0)) != 78600:
        return False
    observed = int(mapping.get("observed_wg_count", 0))
    requested = int(mapping.get("requested_total_wg", 0))
    reason = str(mapping.get("stop_reason", ""))
    if reason == "runner_map_wg_observed_limit":
        return observed == 78600
    if reason == "natural_completion":
        return requested <= 78600 and observed == requested
    return False


def discover(results_dir: Path, reference: tuple[str, ...]) -> list[dict]:
    rows = []
    for result_path in results_dir.rglob("*_result.json"):
        result = load_json(result_path)
        if not result.get("success") or result.get("configuration") != "baseline":
            continue
        directory = result_path.parent
        identity = (
            str(result.get("target", "")),
            str(result.get("benchmark", "")),
            str(result.get("configuration", "")),
        )
        exp = experiments_by_identity(directory).get(identity)
        if exp is None or normalized_config(exp.get("command", [])) != reference:
            continue
        mapping = result.get("wg_mapping", {})
        if not isinstance(mapping, dict) or not valid_bounded_mapping(mapping):
            continue
        metrics = Path(str(result.get("metrics", "")))
        if not metrics.is_absolute():
            metrics = directory / metrics
        binary_metadata = load_json(directory / "EXPERIMENT_BINARIES.json")
        binary_sha = str(
            binary_metadata.get("sha256_by_target", {}).get(identity[0], "")
        )
        rows.append({
            "benchmark": str(result.get("benchmark", "")),
            "status": "reusable_config_match",
            "result_file": str(result_path.resolve()),
            "metrics_file": str(metrics.resolve()),
            "binary_sha256": binary_sha,
            "driver_total_time": driver_time(metrics),
            "wg_observed_count": int(mapping.get("observed_wg_count", 0)),
            "wg_completed_count": int(mapping.get("completed_wg_count", 0)),
            "wg_set_sha256": str(mapping.get("global_wg_set_sha256", "")),
            "stop_reason": str(mapping.get("stop_reason", "")),
        })
    return rows


def manifest_rows(discovered: list[dict]) -> list[dict]:
    by_benchmark = {}
    for row in discovered:
        current = by_benchmark.get(row["benchmark"])
        if current is None or row["result_file"] > current["result_file"]:
            by_benchmark[row["benchmark"]] = row
    rows = []
    for benchmark in PAPER_BENCHMARKS:
        rows.append(by_benchmark.get(benchmark, {
            "benchmark": benchmark,
            "status": "missing",
            "result_file": "",
            "metrics_file": "",
            "binary_sha256": "",
            "driver_total_time": 0.0,
            "wg_observed_count": 0,
            "wg_completed_count": 0,
            "wg_set_sha256": "",
            "stop_reason": "",
        }))
    return rows


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--results-dir", type=Path,
        default=Path(__file__).resolve().parent / "results",
    )
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()

    reference, reference_path = find_reference(args.results_dir)
    rows = manifest_rows(discover(args.results_dir, reference))
    output = args.output or args.results_dir / "BASELINE_REUSE_MANIFEST_78600.csv"
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    print(output)
    print(f"reference={reference_path}")
    print(f"config_sha256={config_digest(reference)}")
    print(f"reusable={sum(row['status'] == 'reusable_config_match' for row in rows)}/14")


if __name__ == "__main__":
    main()
