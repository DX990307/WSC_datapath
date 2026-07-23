#!/usr/bin/env python3
"""Measure same-row work hidden in the DRAM subtransaction queue.

This analysis is read-only. It asks whether a column command closes a row while
another traced subtransaction for that physical bank and row has arrived but
has not yet been converted into a command. Such events bound the benefit of
widening only command formation; they do not justify waiting for future work.
"""

from __future__ import annotations

import argparse
import csv
import gzip
from collections import Counter, defaultdict
from pathlib import Path


COLUMN_KINDS = {"Read", "ReadPrecharge", "Write", "WritePrecharge"}


def benchmark(path: Path) -> str:
    prefix = "baseline_"
    suffix = "_baseline_observation_dram_physical.csv.gz"
    return path.name[len(prefix) : -len(suffix)]


def analyze(path: Path) -> dict[str, object]:
    pending: dict[str, dict[str, tuple[int, int, int, int]]] = defaultdict(dict)
    pending_locations: dict[str, Counter[tuple[int, int, int, int]]] = defaultdict(Counter)
    column_issues = 0
    hidden_events = 0
    hidden_peers = 0
    ready_peer_events = 0
    row_hits = 0
    subq_nonempty = 0
    subq_depth = 0
    cmdq_depth = 0

    with gzip.open(path, "rt", newline="") as stream:
        for row in csv.DictReader(stream):
            controller = row["controller_name"]
            sub_id = row["subtransaction_id"]
            location = (
                int(row["rank"]),
                int(row["bank_group"]),
                int(row["bank"]),
                int(row["row"]),
            )
            if row["event"] == "subtransaction_arrive":
                pending[controller][sub_id] = location
                pending_locations[controller][location] += 1
                continue
            if row["event"] == "command_enqueue":
                old = pending[controller].pop(sub_id, None)
                if old is not None:
                    pending_locations[controller][old] -= 1
                    if pending_locations[controller][old] <= 0:
                        del pending_locations[controller][old]
                continue
            if row["event"] != "command_issue" or row["command_kind"] not in COLUMN_KINDS:
                continue

            column_issues += 1
            sub_depth = int(row["subtransaction_queue_depth"])
            subq_depth += sub_depth
            cmdq_depth += int(row["controller_queue_depth"])
            if sub_depth > 0:
                subq_nonempty += 1
            if row["ready_same_open_row"] != "0":
                ready_peer_events += 1
            if row["column_row_hit"] == "true":
                row_hits += 1
            peers = pending_locations[controller][location]
            if peers > 0:
                hidden_events += 1
                hidden_peers += peers

    return {
        "benchmark": benchmark(path),
        "column_issues": column_issues,
        "column_issue_subqueue_nonempty_fraction": (
            subq_nonempty / column_issues if column_issues else 0.0
        ),
        "average_subtransaction_queue_depth": (
            subq_depth / column_issues if column_issues else 0.0
        ),
        "average_command_queue_depth": (
            cmdq_depth / column_issues if column_issues else 0.0
        ),
        "visible_ready_same_row_fraction": (
            ready_peer_events / column_issues if column_issues else 0.0
        ),
        "hidden_arrived_same_row_fraction": (
            hidden_events / column_issues if column_issues else 0.0
        ),
        "average_hidden_same_row_peers_when_present": (
            hidden_peers / hidden_events if hidden_events else 0.0
        ),
        "baseline_row_hit_fraction": row_hits / column_issues if column_issues else 0.0,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("roots", nargs="+", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    paths: dict[str, Path] = {}
    for root in args.roots:
        for path in root.glob("baseline_*_baseline_observation_dram_physical.csv.gz"):
            paths[benchmark(path)] = path
    rows = [analyze(path) for _, path in sorted(paths.items())]
    if not rows:
        raise SystemExit("no physical DRAM traces found")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    for row in rows:
        print(
            f"{row['benchmark']}: hidden={row['hidden_arrived_same_row_fraction']:.2%}, "
            f"visible={row['visible_ready_same_row_fraction']:.2%}, "
            f"subq={row['average_subtransaction_queue_depth']:.1f}, "
            f"cmdq={row['average_command_queue_depth']:.1f}"
        )
    print(args.output)


if __name__ == "__main__":
    main()
