#!/usr/bin/env python3
"""Build a stable, provenance-carrying Baseline result library."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import shutil
from pathlib import Path


BENCHMARKS = (
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

SOURCE_NAMES = {
    "kmeans": ("kmeans", "kmeans-reuse-smoke"),
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def driver_time(path: Path) -> float:
    with path.open(newline="", encoding="utf-8") as stream:
        for row in csv.DictReader(stream, skipinitialspace=True):
            if (
                row.get("where", "").strip() == "Driver"
                and row.get("what", "").strip() == "total_time"
            ):
                return float(row["value"])
    raise ValueError(f"Driver total_time is missing from {path}")


def binary_sha(source: Path) -> str:
    path = source / "EXPERIMENT_BINARIES.json"
    if not path.is_file():
        return ""
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
        return str(manifest.get("sha256_by_target", {}).get("baseline", ""))
    except (OSError, ValueError, TypeError):
        return ""


def source_names(benchmark: str) -> tuple[str, ...]:
    return SOURCE_NAMES.get(benchmark, (benchmark,))


def find_metrics(sources: list[Path], benchmark: str) -> tuple[Path, str]:
    for source in sources:
        for source_name in source_names(benchmark):
            candidate = source / f"baseline_{source_name}_baseline_metrics.csv"
            if candidate.is_file():
                return candidate, source_name
    raise FileNotFoundError(f"no Baseline metrics found for {benchmark}")


def install(source: Path, destination: Path, replace: bool) -> str:
    source_hash = sha256(source)
    if destination.exists():
        if destination.is_file() and sha256(destination) == source_hash:
            return "existing"
        if not replace:
            raise FileExistsError(
                f"different library artifact already exists: {destination}; "
                "pass --replace to update it"
            )
        destination.unlink()
    try:
        os.link(source, destination)
        return "hardlink"
    except OSError:
        shutil.copy2(source, destination)
        return "copy"


def companion(source: Path, source_name: str, suffix: str) -> Path:
    return source.parent / f"baseline_{source_name}_baseline_{suffix}"


def build_library(
    sources: list[Path],
    output: Path,
    replace: bool = False,
) -> list[dict]:
    if not sources:
        raise ValueError("at least one --source-dir is required")
    sources = [source.resolve() for source in sources]
    for source in sources:
        if not source.is_dir():
            raise FileNotFoundError(f"Baseline source directory is missing: {source}")
    output.mkdir(parents=True, exist_ok=True)

    rows = []
    for benchmark in BENCHMARKS:
        metrics, source_name = find_metrics(sources, benchmark)
        canonical = output / f"baseline_{benchmark}_baseline_metrics.csv"
        install_mode = install(metrics, canonical, replace)
        installed = [canonical.name]

        stdout = companion(metrics, source_name, "out.stdout")
        if stdout.is_file():
            stdout_dst = output / f"baseline_{benchmark}_baseline_out.stdout"
            install(stdout, stdout_dst, replace)
            installed.append(stdout_dst.name)

        # Preserve the historical KMeans phase name for analysis scripts that
        # intentionally request the membership-only workload.
        if benchmark == "kmeans" and source_name != benchmark:
            alias = output / f"baseline_{source_name}_baseline_metrics.csv"
            install(metrics, alias, replace)
            installed.append(alias.name)

        rows.append({
            "benchmark": benchmark,
            "source_benchmark": source_name,
            "metrics_file": canonical.name,
            "driver_total_time": driver_time(metrics),
            "metrics_sha256": sha256(metrics),
            "source_dir": str(metrics.parent),
            "source_file": metrics.name,
            "binary_sha256": binary_sha(metrics.parent),
            "install_mode": install_mode,
            "installed_files": installed,
            "formal_compatibility": "unverified_missing_command_metadata",
        })

    manifest = {
        "version": 1,
        "kind": "reusable_baseline_library",
        "benchmark_count": len(rows),
        "source_priority": [str(source) for source in sources],
        "formal_compatibility": "unverified_missing_command_metadata",
        "note": (
            "These are the historical Baseline files used by the provisional "
            "paper analysis. Reuse does not prove equality with a future "
            "campaign unless its command and WG mapping are independently matched."
        ),
        "benchmarks": rows,
    }
    (output / "BASELINE_LIBRARY.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    with (output / "BASELINE_TIMES.csv").open(
        "w", newline="", encoding="utf-8"
    ) as stream:
        writer = csv.DictWriter(
            stream,
            fieldnames=(
                "benchmark",
                "source_benchmark",
                "driver_total_time",
                "metrics_file",
                "source_dir",
                "binary_sha256",
                "formal_compatibility",
            ),
        )
        writer.writeheader()
        writer.writerows({key: row[key] for key in writer.fieldnames} for row in rows)
    return rows


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-dir", type=Path, action="append", required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--replace", action="store_true")
    args = parser.parse_args()
    rows = build_library(args.source_dir, args.output_dir, args.replace)
    print(args.output_dir.resolve())
    print(f"baseline_cells={len(rows)}/{len(BENCHMARKS)}")


if __name__ == "__main__":
    main()
