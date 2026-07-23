#!/usr/bin/env python3
"""Measure an offline upper bound for demand-only DRAM access-unit merging.

All logical requests remain 64 B.  A later local read is mergeable only when
an earlier read to the same physical DRAM controller, PID, and aligned access
unit is still in flight.  No timeout, prediction, or widened request is
assumed.  The conservative count additionally requires the earlier request to
complete no later than the later request did in the baseline trace.
"""

from __future__ import annotations

import argparse
import csv
import gzip
import re
from dataclasses import dataclass
from pathlib import Path


DRAM_RE = re.compile(r"GPU\[\d+\]\.DRAM\[\d+\]")


@dataclass(frozen=True)
class Demand:
    arrival_ps: int
    completion_ps: int
    address: int


def event_time(events: str, name: str) -> int | None:
    prefix = name + "@"
    for item in events.split(";"):
        if item.startswith(prefix):
            return int(item[len(prefix) :])
    return None


def benchmark_from_path(path: Path) -> str:
    prefix = "baseline_"
    suffix = "_baseline_observation_paths.csv.gz"
    if not path.name.startswith(prefix) or not path.name.endswith(suffix):
        raise ValueError(path)
    return path.name[len(prefix) : -len(suffix)]


def analyze(path: Path, access_bytes: int) -> dict[str, object]:
    groups: dict[tuple[str, int, int], list[Demand]] = {}
    total = 0
    with gzip.open(path, "rt", newline="") as stream:
        for row in csv.DictReader(stream):
            if (
                row.get("operation") != "read"
                or row.get("route") != "local"
                or row.get("source") != "dram"
                or row.get("l1_role") != "leader"
                or row.get("l2_role") != "leader"
                or row.get("status") != "complete"
                or int(row.get("bytes", 0)) != 64
            ):
                continue
            match = DRAM_RE.search(row.get("component_path", ""))
            if match is None:
                continue
            arrival = event_time(row.get("events", ""), "dram_subtransaction_arrive")
            completion = event_time(row.get("events", ""), "dram_transaction_complete")
            if arrival is None or completion is None or completion < arrival:
                continue
            address = int(row["address"])
            key = (match.group(0), int(row["pid"]), address // access_bytes)
            groups.setdefault(key, []).append(Demand(arrival, completion, address))
            total += 1

    overlap = 0
    conservative = 0
    distinct_line = 0
    saved_latency_ps = 0
    for demands in groups.values():
        demands.sort(key=lambda item: (item.arrival_ps, item.completion_ps))
        leader: Demand | None = None
        for demand in demands:
            if leader is None or demand.arrival_ps > leader.completion_ps:
                leader = demand
                continue
            overlap += 1
            if demand.address != leader.address:
                distinct_line += 1
            if leader.completion_ps <= demand.completion_ps:
                conservative += 1
                saved_latency_ps += demand.completion_ps - leader.completion_ps
            # If a later baseline request finishes first, it becomes the best
            # non-regressive completion source for subsequent followers.
            if demand.completion_ps < leader.completion_ps:
                leader = demand

    return {
        "benchmark": benchmark_from_path(path),
        "trace": str(path),
        "logical_request_bytes": 64,
        "physical_access_bytes": access_bytes,
        "local_dram_read_leaders": total,
        "overlapping_same_unit_followers": overlap,
        "overlap_fraction": overlap / total if total else 0.0,
        "distinct_line_overlapping_followers": distinct_line,
        "distinct_line_overlap_fraction": distinct_line / total if total else 0.0,
        "conservative_mergeable_followers": conservative,
        "conservative_merge_fraction": conservative / total if total else 0.0,
        "estimated_follower_latency_saved_ns": saved_latency_ps / 1000.0,
        "average_saved_ns_per_merge": (
            saved_latency_ps / conservative / 1000.0 if conservative else 0.0
        ),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("roots", nargs="+", type=Path)
    parser.add_argument("--physical-access-bytes", type=int, default=128)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if args.physical_access_bytes < 64 or args.physical_access_bytes & (
        args.physical_access_bytes - 1
    ):
        raise SystemExit("physical access size must be a power of two >= 64")

    traces: dict[str, Path] = {}
    for root in args.roots:
        for path in root.glob("baseline_*_baseline_observation_paths.csv.gz"):
            traces[benchmark_from_path(path)] = path
    rows = [
        analyze(path, args.physical_access_bytes)
        for _, path in sorted(traces.items())
    ]
    if not rows:
        raise SystemExit("no baseline observation path traces found")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    for row in rows:
        print(
            f"{row['benchmark']}: {row['conservative_merge_fraction']:.2%} "
            f"mergeable, {row['average_saved_ns_per_merge']:.2f} ns/merge"
        )
    print(args.output)


if __name__ == "__main__":
    main()
