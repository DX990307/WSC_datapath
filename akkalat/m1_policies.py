#!/usr/bin/env python3
"""Reorder policies for Mechanism 1 offline experiments."""

from __future__ import annotations

from collections import Counter, defaultdict
from dataclasses import dataclass, field

from m1_parse_trace import Request


ROW_HIT_BONUS = 4
BANK_IDLE_BONUS = 2
CACHE_AFFINITY_BONUS = 2
CHANNEL_BALANCE_BONUS = 1
AGE_BONUS_PER_50NS = 1


@dataclass
class ReorderState:
    open_row: dict[tuple[int, int, int], int] = field(default_factory=dict)
    bank_counts: Counter[tuple[int, int, int]] = field(default_factory=Counter)
    channel_counts: Counter[tuple[int, int]] = field(default_factory=Counter)
    issued_count: int = 0


@dataclass(frozen=True)
class Selection:
    request: Request
    age_escape: bool = False


@dataclass(frozen=True)
class IssuedRequest:
    request: Request
    issue_time_ns: int
    output_position: int
    age_escape: bool


def request_order_key(request: Request) -> tuple[int, int]:
    return (request.arrival_time_ns, request.sequence)


def oldest_request(pending: list[Request]) -> Request:
    return min(pending, key=request_order_key)


def age_escape(
    pending: list[Request],
    now_ns: int,
    max_age_ns: int | None,
) -> Selection | None:
    if max_age_ns is None:
        return None
    oldest = oldest_request(pending)
    if now_ns - oldest.arrival_time_ns >= max_age_ns:
        return Selection(oldest, age_escape=True)
    return None


def grouped_requests(
    pending: list[Request],
    key_fn,
) -> dict[tuple, list[Request]]:
    groups: dict[tuple, list[Request]] = defaultdict(list)
    for request in pending:
        groups[key_fn(request)].append(request)
    return groups


def group_oldest(requests: list[Request]) -> int:
    return min(r.arrival_time_ns for r in requests)


def choose_first_by_line(requests: list[Request]) -> Request:
    return min(
        requests,
        key=lambda r: (
            r.line_addr,
            r.line_in_page,
            r.arrival_time_ns,
            r.sequence,
        ),
    )


class Policy:
    name = "policy"

    def select(
        self,
        pending: list[Request],
        state: ReorderState,
        now_ns: int,
        max_age_ns: int | None,
    ) -> Selection:
        raise NotImplementedError


class FifoPolicy(Policy):
    name = "fifo"

    def select(
        self,
        pending: list[Request],
        state: ReorderState,
        now_ns: int,
        max_age_ns: int | None,
    ) -> Selection:
        del state, now_ns, max_age_ns
        return Selection(oldest_request(pending))


class L2OnlyPolicy(Policy):
    name = "l2_only"

    def select(
        self,
        pending: list[Request],
        state: ReorderState,
        now_ns: int,
        max_age_ns: int | None,
    ) -> Selection:
        del state
        escaped = age_escape(pending, now_ns, max_age_ns)
        if escaped is not None:
            return escaped

        groups = grouped_requests(
            pending,
            lambda r: (r.target_gpu, r.l2_slice, r.page_id),
        )
        _, requests = max(
            groups.items(),
            key=lambda item: (
                len(item[1]),
                -group_oldest(item[1]),
                -min(r.sequence for r in item[1]),
            ),
        )
        return Selection(choose_first_by_line(requests))


class DramOnlyPolicy(Policy):
    name = "dram_only"

    def select(
        self,
        pending: list[Request],
        state: ReorderState,
        now_ns: int,
        max_age_ns: int | None,
    ) -> Selection:
        escaped = age_escape(pending, now_ns, max_age_ns)
        if escaped is not None:
            return escaped

        groups = grouped_requests(
            pending,
            lambda r: (r.target_gpu, r.channel, r.bank, r.row),
        )

        def score(item: tuple[tuple, list[Request]]) -> tuple[int, int, int, int, int]:
            key, requests = item
            target_gpu, channel, bank, row = key
            bank_key = (target_gpu, channel, bank)
            channel_key = (target_gpu, channel)
            row_hit = int(state.open_row.get(bank_key) == row)
            return (
                row_hit,
                len(requests),
                -state.bank_counts[bank_key],
                -state.channel_counts[channel_key],
                -group_oldest(requests),
            )

        _, requests = max(groups.items(), key=score)
        return Selection(min(requests, key=request_order_key))


class HlqPolicy(Policy):
    name = "hlq"

    def select(
        self,
        pending: list[Request],
        state: ReorderState,
        now_ns: int,
        max_age_ns: int | None,
    ) -> Selection:
        escaped = age_escape(pending, now_ns, max_age_ns)
        if escaped is not None:
            return escaped

        buckets = grouped_requests(
            pending,
            lambda r: (
                r.target_gpu,
                r.is_remote,
                r.channel,
                r.bank,
                r.row,
            ),
        )

        def largest_page_group_size(requests: list[Request]) -> int:
            counts = Counter((r.target_gpu, r.page_id) for r in requests)
            return max(counts.values(), default=0)

        max_channel_count = max(state.channel_counts.values(), default=0)

        def score(item: tuple[tuple, list[Request]]) -> tuple[int, int, int, int]:
            key, requests = item
            target_gpu, _is_remote, channel, bank, row = key
            bank_key = (target_gpu, channel, bank)
            channel_key = (target_gpu, channel)
            oldest = group_oldest(requests)

            value = 0
            if state.open_row.get(bank_key) == row:
                value += ROW_HIT_BONUS
            if state.bank_counts[bank_key] == 0:
                value += BANK_IDLE_BONUS
            value += CACHE_AFFINITY_BONUS * largest_page_group_size(requests)
            value += CHANNEL_BALANCE_BONUS * (
                max_channel_count - state.channel_counts[channel_key]
            )
            value += AGE_BONUS_PER_50NS * max(0, (now_ns - oldest) // 50)
            return (value, len(requests), -oldest, -min(r.sequence for r in requests))

        _, requests = max(buckets.items(), key=score)
        return Selection(choose_request_inside_hlq_bucket(requests))


def choose_request_inside_hlq_bucket(requests: list[Request]) -> Request:
    pages = grouped_requests(requests, lambda r: (r.target_gpu, r.page_id))
    _, page_requests = max(
        pages.items(),
        key=lambda item: (
            len(item[1]),
            -group_oldest(item[1]),
            -min(r.sequence for r in item[1]),
        ),
    )
    return min(
        page_requests,
        key=lambda r: (
            r.line_in_page,
            r.arrival_time_ns,
            r.sequence,
        ),
    )


def update_state(state: ReorderState, request: Request) -> None:
    bank_key = (request.target_gpu, request.channel, request.bank)
    channel_key = (request.target_gpu, request.channel)
    state.open_row[bank_key] = request.row
    state.bank_counts[bank_key] += 1
    state.channel_counts[channel_key] += 1
    state.issued_count += 1


POLICY_CLASSES = {
    FifoPolicy.name: FifoPolicy,
    L2OnlyPolicy.name: L2OnlyPolicy,
    DramOnlyPolicy.name: DramOnlyPolicy,
    HlqPolicy.name: HlqPolicy,
}


def make_policy(name: str) -> Policy:
    normalized = name.strip().lower().replace("-", "_")
    aliases = {
        "fifo": "fifo",
        "l2": "l2_only",
        "l2_only": "l2_only",
        "dram": "dram_only",
        "dram_only": "dram_only",
        "hlq": "hlq",
    }
    key = aliases.get(normalized)
    if key is None:
        raise ValueError(f"unknown policy: {name}")
    return POLICY_CLASSES[key]()


def process_trace(
    requests: list[Request],
    policy: Policy,
    window_size: int,
    max_age_ns: int | None = None,
    issue_interval_ns: int = 1,
) -> list[IssuedRequest]:
    if not requests:
        return []

    ordered = sorted(requests, key=request_order_key)
    pending: list[Request] = []
    output: list[IssuedRequest] = []
    state = ReorderState()
    next_input = 0
    issue_time_ns = ordered[0].arrival_time_ns

    while next_input < len(ordered) or pending:
        while next_input < len(ordered) and len(pending) < window_size:
            request = ordered[next_input]
            pending.append(request)
            issue_time_ns = max(issue_time_ns, request.arrival_time_ns)
            next_input += 1

        selection = policy.select(
            pending=pending,
            state=state,
            now_ns=issue_time_ns,
            max_age_ns=max_age_ns,
        )
        issued = selection.request
        output.append(
            IssuedRequest(
                request=issued,
                issue_time_ns=max(issue_time_ns, issued.arrival_time_ns),
                output_position=len(output),
                age_escape=selection.age_escape,
            )
        )
        pending.remove(issued)
        update_state(state, issued)
        issue_time_ns += issue_interval_ns

    return output
