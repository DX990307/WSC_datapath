#!/usr/bin/env python3
"""Wait for, audit, and summarize the standalone M1/M2/M3 campaign."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

from plot_cupath_typed_ablation import WORKLOADS


CONFIGS = ("m1", "m2", "m3")


def scan(root: Path, configs: tuple[str, ...]) -> tuple[int, list[str]]:
    completed = 0
    failures = []
    for benchmark, _label, _group in WORKLOADS:
        for config in configs:
            path = root / f"baseline_{benchmark}_{config}_result.json"
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
    print("[ablation-finalizer] run:", " ".join(command), flush=True)
    subprocess.run(command, cwd=cwd, check=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("results", type=Path)
    parser.add_argument("--baseline-results", type=Path, required=True)
    parser.add_argument("--wait", action="store_true")
    parser.add_argument("--poll-seconds", type=float, default=30.0)
    args = parser.parse_args()
    if args.poll_seconds <= 0:
        parser.error("--poll-seconds must be positive")

    root = args.results.resolve()
    baseline_root = args.baseline_results.resolve()
    repo = Path(__file__).resolve().parent.parent
    last_state = None
    while True:
        count, failures = scan(root, CONFIGS)
        baseline_count, baseline_failures = scan(
            baseline_root, ("baseline", "complete")
        )
        failures.extend(baseline_failures)
        if failures:
            raise RuntimeError(
                "campaign contains failed cells: " + ", ".join(failures)
            )
        state = (count, baseline_count)
        if state != last_state:
            stamp = datetime.now(timezone.utc).isoformat(timespec="seconds")
            print(
                f"[ablation-finalizer] {stamp} ablation={count}/42 "
                f"baseline_complete={baseline_count}/28",
                flush=True,
            )
            last_state = state
        if count == 42 and baseline_count == 28:
            break
        if not args.wait:
            print("[ablation-finalizer] campaign is incomplete", flush=True)
            return 2
        time.sleep(args.poll_seconds)

    python = sys.executable
    run(
        [
            python,
            "akkalat/audit_m1_m2_m3_campaign.py",
            str(root),
            "--baseline-results",
            str(baseline_root),
        ],
        repo,
    )
    run(
        [
            python,
            "akkalat/plot_cupath_typed_ablation.py",
            str(baseline_root),
            "--additional-results",
            str(root),
            "--output-dir",
            str(root),
        ],
        repo,
    )
    print("[ablation-finalizer] strict finalization PASS", flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
