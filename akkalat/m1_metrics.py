#!/usr/bin/env python3
"""Metrics for Mechanism 1 offline reorder experiments."""

from __future__ import annotations

from collections import Counter, defaultdict
from math import sqrt
from statistics import mean
from typing import Iterable

from m1_parse_trace import MappingConfig, Request
from m1_policies import IssuedRequest


REQUEST_CLASSES = ("all", "local", "remote")

SUMMARY_FIELDS = [
    "workload",
    "trace_name",
    "policy",
    "window_size",
    "max_age_ns",
    "request_class",
    "num_requests",
    "same_page_reuse_distance_avg",
    "same_page_reuse_distance_p50",
    "same_page_reuse_distance_p95",
    "avg_lines_per_page_per_window",
    "p90_lines_per_page_per_window",
    "largest_page_group_per_window",
    "estimated_mshr_merge_opportunity",
    "row_buffer_hit_rate",
    "row_buffer_conflict_rate",
    "bytes_per_ACT",
    "bank_switch_rate",
    "channel_balance_cv",
    "avg_queue_wait_ns",
    "p95_queue_wait_ns",
    "p99_queue_wait_ns",
    "age_escape_count",
    "reorder_distance_avg",
    "reorder_distance_p99",
]

WINDOW_FIELDS = [
    "workload",
    "trace_name",
    "policy",
    "window_size",
    "max_age_ns",
    "window_id",
    "request_class",
    "num_requests",
    "num_unique_pages",
    "num_unique_rows",
    "largest_page_group",
    "largest_row_group",
    "remote_ratio",
    "local_ratio",
]

FRAGMENTATION_FIELDS = [
    "workload",
    "trace_name",
    "window_size",
    "request_class",
    "num_windows",
    "fifo_consecutive_largest_page_group_avg",
    "fifo_consecutive_largest_page_group_p90",
    "window_visible_largest_page_group_avg",
    "window_visible_largest_page_group_p90",
    "fifo_consecutive_largest_line_group_avg",
    "window_visible_largest_line_group_avg",
]


def percentile(values: Iterable[float], pct: float) -> float | str:
    ordered = sorted(values)
    if not ordered:
        return ""
    if len(ordered) == 1:
        return ordered[0]
    rank = (len(ordered) - 1) * pct / 100.0
    lower = int(rank)
    upper = min(lower + 1, len(ordered) - 1)
    weight = rank - lower
    return ordered[lower] * (1.0 - weight) + ordered[upper] * weight


def avg(values: Iterable[float]) -> float | str:
    collected = list(values)
    if not collected:
        return ""
    return mean(collected)


def filter_issued(
    issued: list[IssuedRequest],
    request_class: str,
) -> list[IssuedRequest]:
    if request_class == "all":
        return issued
    if request_class == "local":
        return [item for item in issued if not item.request.is_remote]
    if request_class == "remote":
        return [item for item in issued if item.request.is_remote]
    raise ValueError(f"unknown request class: {request_class}")


def filter_requests(
    requests: list[Request],
    request_class: str,
) -> list[Request]:
    if request_class == "all":
        return requests
    if request_class == "local":
        return [request for request in requests if not request.is_remote]
    if request_class == "remote":
        return [request for request in requests if request.is_remote]
    raise ValueError(f"unknown request class: {request_class}")


def same_page_reuse_distances(issued: list[IssuedRequest]) -> list[int]:
    last_position: dict[tuple[int, int], int] = {}
    distances: list[int] = []
    for position, item in enumerate(issued):
        request = item.request
        key = (request.target_gpu, request.page_id)
        if key in last_position:
            distances.append(position - last_position[key] - 1)
        last_position[key] = position
    return distances


def lines_per_page_per_window(
    issued: list[IssuedRequest],
    window_size: int,
) -> tuple[list[int], list[int]]:
    line_counts: list[int] = []
    request_counts: list[int] = []

    for start in range(0, len(issued), window_size):
        window = issued[start : start + window_size]
        page_lines: dict[tuple[int, int], set[int]] = defaultdict(set)
        page_requests: Counter[tuple[int, int]] = Counter()
        for item in window:
            request = item.request
            page_key = (request.target_gpu, request.page_id)
            page_lines[page_key].add(request.line_addr)
            page_requests[page_key] += 1
        line_counts.extend(len(lines) for lines in page_lines.values())
        request_counts.extend(page_requests.values())

    return line_counts, request_counts


def estimated_mshr_merge_opportunity(
    issued: list[IssuedRequest],
    issue_window: int = 8,
) -> int:
    count = 0
    for index, item in enumerate(issued):
        key = (item.request.target_gpu, item.request.line_addr)
        start = max(0, index - issue_window + 1)
        for previous in issued[start:index]:
            previous_key = (previous.request.target_gpu, previous.request.line_addr)
            if previous_key == key:
                count += 1
                break
    return count


def coefficient_of_variation(values: list[int]) -> float | str:
    if not values:
        return ""
    avg_value = mean(values)
    if avg_value == 0:
        return ""
    variance = mean((value - avg_value) ** 2 for value in values)
    return sqrt(variance) / avg_value


def row_buffer_metrics(
    issued: list[IssuedRequest],
    mapping: MappingConfig,
) -> dict[str, float | str]:
    open_rows: dict[tuple[int, int, int], int] = {}
    channel_counts = Counter()
    row_hits = 0
    row_conflicts = 0
    acts = 0
    total_bytes = 0
    bank_switches = 0
    previous_bank: tuple[int, int, int] | None = None

    for item in issued:
        request = item.request
        bank_key = (request.target_gpu, request.channel, request.bank)
        total_bytes += request.bytes
        channel_counts[request.channel] += 1
        if previous_bank is not None and previous_bank != bank_key:
            bank_switches += 1
        previous_bank = bank_key

        if open_rows.get(bank_key) == request.row:
            row_hits += 1
        else:
            row_conflicts += 1
            acts += 1
            open_rows[bank_key] = request.row

    num_requests = len(issued)
    channel_values = [
        channel_counts[channel] for channel in range(mapping.num_hbm_channels)
    ]
    return {
        "row_buffer_hit_rate": row_hits / num_requests if num_requests else "",
        "row_buffer_conflict_rate": row_conflicts / num_requests if num_requests else "",
        "bytes_per_ACT": total_bytes / acts if acts else "",
        "bank_switch_rate": bank_switches / (num_requests - 1)
        if num_requests > 1
        else "",
        "channel_balance_cv": coefficient_of_variation(channel_values),
    }


def summary_row(
    workload: str,
    trace_name: str,
    policy: str,
    window_size: int,
    max_age_ns: int | None,
    request_class: str,
    issued: list[IssuedRequest],
    mapping: MappingConfig,
) -> dict[str, object]:
    selected = filter_issued(issued, request_class)
    reuse = same_page_reuse_distances(selected)
    line_counts, page_group_counts = lines_per_page_per_window(selected, window_size)
    waits = [item.issue_time_ns - item.request.arrival_time_ns for item in selected]
    reorder_distances = [
        abs(item.output_position - item.request.fifo_position) for item in selected
    ]
    row_metrics = row_buffer_metrics(selected, mapping)

    row: dict[str, object] = {
        "workload": workload,
        "trace_name": trace_name,
        "policy": policy,
        "window_size": window_size,
        "max_age_ns": "unlimited" if max_age_ns is None else max_age_ns,
        "request_class": request_class,
        "num_requests": len(selected),
        "same_page_reuse_distance_avg": avg(reuse),
        "same_page_reuse_distance_p50": percentile(reuse, 50),
        "same_page_reuse_distance_p95": percentile(reuse, 95),
        "avg_lines_per_page_per_window": avg(line_counts),
        "p90_lines_per_page_per_window": percentile(line_counts, 90),
        "largest_page_group_per_window": max(page_group_counts, default=0),
        "estimated_mshr_merge_opportunity": estimated_mshr_merge_opportunity(selected),
        "avg_queue_wait_ns": avg(waits),
        "p95_queue_wait_ns": percentile(waits, 95),
        "p99_queue_wait_ns": percentile(waits, 99),
        "age_escape_count": sum(1 for item in selected if item.age_escape),
        "reorder_distance_avg": avg(reorder_distances),
        "reorder_distance_p99": percentile(reorder_distances, 99),
    }
    row.update(row_metrics)
    return row


def window_metric_rows(
    workload: str,
    trace_name: str,
    policy: str,
    window_size: int,
    max_age_ns: int | None,
    issued: list[IssuedRequest],
) -> list[dict[str, object]]:
    rows: list[dict[str, object]] = []
    for request_class in REQUEST_CLASSES:
        selected = filter_issued(issued, request_class)
        for window_id, start in enumerate(range(0, len(selected), window_size)):
            window = selected[start : start + window_size]
            page_counts = Counter(
                (item.request.target_gpu, item.request.page_id) for item in window
            )
            row_counts = Counter(
                (
                    item.request.target_gpu,
                    item.request.channel,
                    item.request.bank,
                    item.request.row,
                )
                for item in window
            )
            remote = sum(1 for item in window if item.request.is_remote)
            num_requests = len(window)
            rows.append(
                {
                    "workload": workload,
                    "trace_name": trace_name,
                    "policy": policy,
                    "window_size": window_size,
                    "max_age_ns": "unlimited" if max_age_ns is None else max_age_ns,
                    "window_id": window_id,
                    "request_class": request_class,
                    "num_requests": num_requests,
                    "num_unique_pages": len(page_counts),
                    "num_unique_rows": len(row_counts),
                    "largest_page_group": max(page_counts.values(), default=0),
                    "largest_row_group": max(row_counts.values(), default=0),
                    "remote_ratio": remote / num_requests if num_requests else "",
                    "local_ratio": (num_requests - remote) / num_requests
                    if num_requests
                    else "",
                }
            )
    return rows


def largest_consecutive_group(
    requests: list[Request],
    key_fn,
) -> int:
    best = 0
    current = 0
    previous_key = None
    for request in requests:
        key = key_fn(request)
        if key == previous_key:
            current += 1
        else:
            current = 1
            previous_key = key
        best = max(best, current)
    return best


def largest_visible_group(
    requests: list[Request],
    key_fn,
) -> int:
    counts = Counter(key_fn(request) for request in requests)
    return max(counts.values(), default=0)


def fragmentation_rows(
    workload: str,
    trace_name: str,
    requests: list[Request],
    window_size: int,
) -> list[dict[str, object]]:
    rows: list[dict[str, object]] = []
    ordered = sorted(requests, key=lambda r: (r.arrival_time_ns, r.sequence))

    for request_class in REQUEST_CLASSES:
        selected = filter_requests(ordered, request_class)
        consecutive_page: list[int] = []
        visible_page: list[int] = []
        consecutive_line: list[int] = []
        visible_line: list[int] = []

        for start in range(0, len(selected), window_size):
            window = selected[start : start + window_size]
            page_key = lambda r: (r.target_gpu, r.page_id)
            line_key = lambda r: (r.target_gpu, r.line_addr)
            consecutive_page.append(largest_consecutive_group(window, page_key))
            visible_page.append(largest_visible_group(window, page_key))
            consecutive_line.append(largest_consecutive_group(window, line_key))
            visible_line.append(largest_visible_group(window, line_key))

        rows.append(
            {
                "workload": workload,
                "trace_name": trace_name,
                "window_size": window_size,
                "request_class": request_class,
                "num_windows": len(consecutive_page),
                "fifo_consecutive_largest_page_group_avg": avg(consecutive_page),
                "fifo_consecutive_largest_page_group_p90": percentile(
                    consecutive_page, 90
                ),
                "window_visible_largest_page_group_avg": avg(visible_page),
                "window_visible_largest_page_group_p90": percentile(
                    visible_page, 90
                ),
                "fifo_consecutive_largest_line_group_avg": avg(consecutive_line),
                "window_visible_largest_line_group_avg": avg(visible_line),
            }
        )

    return rows
