#!/usr/bin/env python3
"""Run benchmark-agnostic typed-filter sensitivity points.

Each point is a separate runall2 campaign so its command, frozen binary hash,
Git revision, and complete hardware flags are retained in the ordinary
experiment metadata.  The 13-bit/4-slot/1-cycle/16-wide main point is reused
from the representative ablation rather than rerun here.
"""

from __future__ import annotations

import argparse
import subprocess
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path


POINTS = (
    ("capacity", "cap16384", ("-typed-filter-capacity=16384",)),
    ("capacity", "cap65536", ("-typed-filter-capacity=65536",)),
    ("fingerprint", "fp16", ("-typed-filter-fingerprint-bits=16",)),
    ("fingerprint", "fp11", ("-typed-filter-fingerprint-bits=11",)),
    ("predictor", "pred16", ("-prefetch-predictor-entries=16",)),
    ("predictor", "pred256", ("-prefetch-predictor-entries=256",)),
    ("lookup_latency", "lookup0", ("-typed-filter-lookup-latency=0",)),
    ("lookup_latency", "lookup2", ("-typed-filter-lookup-latency=2",)),
)


def run_point(args, dimension, name, flags):
    output = args.output_dir / dimension / name
    output.mkdir(parents=True, exist_ok=True)
    extra = " ".join(("-sampled", "-branch-sampled", "-kernel-sampled", *flags))
    command = [
        "python3", "akkalat/runall2.py",
        "--configs=complete_cupath",
        f"--benchmarks={args.benchmarks}",
        f"--max-workers={args.workers_per_point}",
        f"--max-wg={args.max_wg}",
        f"--timeout-minutes={args.timeout_minutes}",
        "--skip-build", f"--binary-path={args.binary}",
        "--disable-servers", f"--output-dir={output}",
        f"--extra-benchmark-flags={extra}",
    ]
    log = output / "sensitivity_driver.log"
    with log.open("w", encoding="utf-8") as stream:
        stream.write("COMMAND: " + " ".join(command) + "\n")
        stream.flush()
        result = subprocess.run(command, stdout=stream, stderr=subprocess.STDOUT)
    return dimension, name, result.returncode, log


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument(
        "--benchmarks", default="aes,kmeans,pagerank,matrixtranspose",
    )
    parser.add_argument("--max-wg", type=int, default=192)
    parser.add_argument("--workers-per-point", type=int, default=4)
    parser.add_argument("--parallel-points", type=int, default=2)
    parser.add_argument("--timeout-minutes", type=int, default=30)
    args = parser.parse_args()
    args.binary = args.binary.resolve()
    args.output_dir = args.output_dir.resolve()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    failures = []
    with ThreadPoolExecutor(max_workers=args.parallel_points) as executor:
        futures = [
            executor.submit(run_point, args, dimension, name, flags)
            for dimension, name, flags in POINTS
        ]
        for future in as_completed(futures):
            dimension, name, returncode, log = future.result()
            print(f"{dimension}/{name}: returncode={returncode}: {log}", flush=True)
            if returncode != 0:
                failures.append((dimension, name, returncode))
    if failures:
        raise SystemExit(f"sensitivity failures: {failures}")


if __name__ == "__main__":
    main()
