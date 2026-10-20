#!/usr/bin/env python3
"""Revalidate simulator-success cells rejected only by an old WG audit.

This tool never reruns a simulator and never edits metrics, stdout, mapping
sidecars, manifests, or commands.  It can only replace a ``returncode=-2``
cell result after independently proving that the simulator returned zero and
the current mapping auditor accepts the immutable artifacts.
"""

from __future__ import annotations

import argparse
from datetime import datetime
import json
from pathlib import Path

import runall2_process


def parse_csv(value: str) -> set[str]:
    return {item.strip() for item in value.split(",") if item.strip()}


def revalidate(results: Path, benchmarks: set[str], configs: set[str]) -> list[Path]:
    experiments = runall2_process.load_experiments_from_metadata(str(results))
    runall2_process.set_output_dir(str(results))
    repaired = []

    for exp in experiments:
        if benchmarks and exp["benchmark"] not in benchmarks:
            continue
        if configs and exp["config_name"] not in configs:
            continue

        result_path = runall2_process.cell_result_path(exp)
        if not result_path.is_file():
            continue
        previous = json.loads(result_path.read_text(encoding="utf-8"))
        if previous.get("success"):
            continue
        if (previous.get("returncode") != -2
                or previous.get("simulator_returncode") != 0):
            continue

        stem = runall2_process.exp_file_stem(exp)
        stdout_path = Path(stem + "_out.stdout")
        stdout = stdout_path.read_text(encoding="utf-8", errors="replace")
        if "Return code: 0\n" not in stdout or "Timed out after" in stdout:
            raise RuntimeError(
                f"cannot prove simulator success from {stdout_path}"
            )

        metric_stem = stem + "_metrics"
        metrics = Path(metric_stem + ".csv")
        if not metrics.is_file():
            raise RuntimeError(f"missing metrics for revalidation: {metrics}")
        mapping = runall2_process.audit_wg_mapping(exp, metric_stem)

        runall2_process.write_cell_result(exp, {
            "returncode": 0,
            "simulator_returncode": 0,
            "metrics": str(metrics),
            "wg_mapping": mapping,
            "success": True,
            "revalidated_at": datetime.now().isoformat(),
            "revalidated_from": {
                "returncode": previous.get("returncode"),
                "mapping_error": previous.get("mapping_error"),
            },
        })
        repaired.append(result_path)

    return repaired


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--benchmarks", default="")
    parser.add_argument("--configs", default="")
    args = parser.parse_args()

    repaired = revalidate(
        args.results.resolve(),
        parse_csv(args.benchmarks),
        parse_csv(args.configs),
    )
    for path in repaired:
        print(path)
    print(f"Revalidated {len(repaired)} cell(s).")


if __name__ == "__main__":
    main()
