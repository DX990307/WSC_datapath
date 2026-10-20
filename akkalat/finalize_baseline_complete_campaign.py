#!/usr/bin/env python3
"""Wait for and strictly finalize the 14-workload Baseline+Complete run."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

from plot_cupath_typed_ablation import WORKLOADS


CONFIGS = ("baseline", "complete")


def expected_result_paths(root: Path) -> list[Path]:
    return [
        root / f"baseline_{benchmark}_{config}_result.json"
        for benchmark, _label, _group in WORKLOADS
        for config in CONFIGS
    ]


def completed_results(root: Path) -> tuple[int, list[str]]:
    completed = 0
    failures = []
    for path in expected_result_paths(root):
        if not path.is_file():
            continue
        try:
            result = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            continue
        completed += 1
        if not result.get("success") or result.get("returncode") != 0:
            failures.append(path.name)
    return completed, failures


def run(command: list[str], cwd: Path) -> None:
    print("[finalizer] run:", " ".join(command), flush=True)
    subprocess.run(command, cwd=cwd, check=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--wait", action="store_true")
    parser.add_argument("--poll-seconds", type=float, default=30.0)
    args = parser.parse_args()
    if args.poll_seconds <= 0:
        parser.error("--poll-seconds must be positive")

    root = args.results.resolve()
    repo = Path(__file__).resolve().parent.parent
    last_count = None
    while True:
        count, failures = completed_results(root)
        if failures:
            raise RuntimeError(
                "campaign contains failed cells: " + ", ".join(failures)
            )
        if count != last_count:
            stamp = datetime.now(timezone.utc).isoformat(timespec="seconds")
            print(f"[finalizer] {stamp} completed={count}/28", flush=True)
            last_count = count
        if count == 28:
            break
        if not args.wait:
            print("[finalizer] campaign is incomplete", flush=True)
            return 2
        time.sleep(args.poll_seconds)

    python = sys.executable
    output = root / "observation-analysis"
    run(
        [
            python,
            "akkalat/analyze_observations.py",
            str(root),
            "--output-dir",
            str(output),
        ],
        repo,
    )
    run(
        [
            python,
            "akkalat/plot_observations.py",
            str(output),
            "--output-dir",
            str(output / "figures"),
        ],
        repo,
    )
    run(
        [
            python,
            "akkalat/plot_cupath_typed_ablation.py",
            str(root),
            "--output-dir",
            str(root),
        ],
        repo,
    )
    run(
        [
            python,
            "akkalat/summarize_baseline_complete_campaign.py",
            str(root),
        ],
        repo,
    )
    run(
        [
            python,
            "akkalat/audit_baseline_complete_campaign.py",
            "--require-analysis",
            str(root),
        ],
        repo,
    )
    print("[finalizer] strict finalization PASS", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
