#!/usr/bin/env python3
"""Estimate the useful lead time of a page-local pure-64B stride predictor.

This is an offline upper-bound study.  It never changes simulator behavior and
does not assume a widened request.  It compares two implementable observation
points: one predictor per destination L2 slice, and one predictor at the
requester-side L2 ingress before slice routing.  Both are page-local.  A
predictor is considered trained only after two consecutive equal, non-zero
demand strides.
"""

import argparse
import csv
import gzip
import math
import re
import sys
from collections import defaultdict
from pathlib import Path


L2_RE = re.compile(r"GPU\[\d+\]\.L2\[\d+\]")
LOOKAHEADS = (1, 2, 4, 8, 16, 32)
LEAD_THRESHOLDS_NS = (0, 10, 34, 64, 128)


def benchmark_from_path(path: Path) -> str:
    name = path.name
    prefix = "baseline_"
    suffix = "_baseline_observation_paths.csv.gz"
    if not name.startswith(prefix) or not name.endswith(suffix):
        raise ValueError(f"unexpected trace name: {name}")
    return name[len(prefix) : -len(suffix)]


def load_streams(path: Path, scope: str):
    streams = defaultdict(list)
    total = 0
    with gzip.open(path, "rt", newline="") as stream:
        for row in csv.DictReader(stream):
            if (
                row.get("operation") != "read"
                or row.get("route") != "local"
                or row.get("source") != "dram"
                or row.get("l1_role") != "leader"
                or row.get("l2_role") != "leader"
            ):
                continue
            match = L2_RE.search(row.get("component_path", ""))
            if match is None:
                continue
            address = int(row["address"])
            requester_key = (
                row["l1_cache"],
                int(row["pid"]),
                address >> 12,
            )
            if scope == "l2-page":
                key = (*requester_key, match.group(0))
            else:
                key = requester_key
            streams[key].append(
                (int(row["sequence"]), int(row["start_ps"]), address)
            )
            total += 1
    for values in streams.values():
        values.sort()
    return streams, total


def analyze_streams(streams, total):
    issued = {lookahead: 0 for lookahead in LOOKAHEADS}
    correct = {lookahead: 0 for lookahead in LOOKAHEADS}
    covered = {
        lookahead: {threshold: set() for threshold in LEAD_THRESHOLDS_NS}
        for lookahead in LOOKAHEADS
    }
    stable_events = 0

    for stream_id, values in streams.items():
        if len(values) < 4:
            continue
        deltas = [
            values[index][2] - values[index - 1][2]
            for index in range(1, len(values))
        ]
        for index in range(2, len(values)):
            delta = deltas[index - 1]
            if delta == 0 or deltas[index - 2] != delta:
                continue
            stable_events += 1
            for lookahead in LOOKAHEADS:
                target = index + lookahead
                issued[lookahead] += 1
                if target >= len(values):
                    continue
                # Strict upper bound for a simple stride stream: every demand
                # before the target must preserve the learned stride.
                if any(
                    deltas[pos - 1] != delta
                    for pos in range(index + 1, target + 1)
                ):
                    continue
                correct[lookahead] += 1
                target_id = (stream_id, target)
                lead_ns = (values[target][1] - values[index][1]) / 1000.0
                for threshold in LEAD_THRESHOLDS_NS:
                    if lead_ns >= threshold:
                        covered[lookahead][threshold].add(target_id)

    rows = []
    for lookahead in LOOKAHEADS:
        row = {
            "lookahead_lines": lookahead,
            "predictor_events": issued[lookahead],
            "strict_correct_predictions": correct[lookahead],
            "strict_accuracy": (
                correct[lookahead] / issued[lookahead]
                if issued[lookahead]
                else math.nan
            ),
            "total_local_dram_leaders": total,
            "stable_stride_events": stable_events,
        }
        for threshold in LEAD_THRESHOLDS_NS:
            count = len(covered[lookahead][threshold])
            row[f"covered_ge_{threshold}ns"] = count
            row[f"coverage_ge_{threshold}ns"] = count / total if total else 0
        rows.append(row)
    return rows


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("roots", nargs="+", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument(
        "--scope",
        choices=("requester-page", "l2-page"),
        default="requester-page",
        help="Predict before slice routing, or independently within each slice.",
    )
    args = parser.parse_args()

    traces = {}
    for root in args.roots:
        for path in root.glob("baseline_*_baseline_observation_paths.csv.gz"):
            traces[benchmark_from_path(path)] = path
    if not traces:
        raise SystemExit("no baseline observation path traces found")

    output_rows = []
    rejected_traces = []
    for benchmark, path in sorted(traces.items()):
        try:
            streams, total = load_streams(path, args.scope)
        except (EOFError, gzip.BadGzipFile) as error:
            rejected_traces.append((benchmark, path, str(error)))
            print(
                f"warning: rejecting incomplete trace for {benchmark}: "
                f"{path}: {error}",
                file=sys.stderr,
            )
            continue
        for row in analyze_streams(streams, total):
            output_rows.append(
                {
                    "benchmark": benchmark,
                    "scope": args.scope,
                    "trace": str(path),
                    **row,
                }
            )
        print(f"{benchmark}: {total} local DRAM leader demands")

    if not output_rows:
        raise SystemExit("no complete baseline observation path traces found")

    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=output_rows[0].keys())
        writer.writeheader()
        writer.writerows(output_rows)
    print(args.output)
    if rejected_traces:
        print(
            f"rejected {len(rejected_traces)} incomplete trace(s); "
            "they are absent from the output",
            file=sys.stderr,
        )


if __name__ == "__main__":
    main()
