#!/usr/bin/env python3
"""Trace parsing helpers for Mechanism 1 offline reorder experiments."""

from __future__ import annotations

import csv
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Iterable


CACHELINE_BYTES = 64
PAGE_BYTES = 4096
NUM_L2_SLICES = 49
NUM_HBM_CHANNELS = 8
NUM_HBM_BANKS = 16
ROW_BYTES = 2048

L1V_SUMMARY_SUFFIX = "_memory_path_l1v_path_summary.csv"


@dataclass(frozen=True)
class MappingConfig:
    cacheline_bytes: int = CACHELINE_BYTES
    page_bytes: int = PAGE_BYTES
    num_l2_slices: int = NUM_L2_SLICES
    num_hbm_channels: int = NUM_HBM_CHANNELS
    num_hbm_banks: int = NUM_HBM_BANKS
    row_bytes: int = ROW_BYTES


@dataclass(frozen=True)
class Request:
    trace_name: str
    workload: str
    path_id: str
    sequence: int
    fifo_position: int
    arrival_time_ns: int
    completion_time_ns: int
    total_l1v_path_latency_ns: int
    access_type: str
    pid: int
    vaddr: int
    paddr: int
    page_paddr: int
    cacheline_addr: int
    bytes: int
    requester_gpm: int
    owner_gpm: int
    target_gpu: int
    hops: int
    is_remote: bool
    route: str
    final_source: str
    l1v_result: str
    l2_result: str
    parent_count: int
    line_addr: int
    page_id: int
    line_in_page: int
    l2_slice: int
    channel: int
    bank: int
    row: int


def parse_int(value: object, default: int = 0) -> int:
    if value is None:
        return default
    text = str(value).strip()
    if text == "":
        return default
    try:
        return int(text, 0)
    except ValueError:
        return int(float(text))


def parse_bool(value: object) -> bool:
    return str(value).strip().lower() in {"1", "true", "t", "yes", "y"}


def infer_workload(path: Path) -> str:
    name = path.name
    if name.startswith("baseline_"):
        name = name[len("baseline_") :]
    if name.endswith(L1V_SUMMARY_SUFFIX):
        name = name[: -len(L1V_SUMMARY_SUFFIX)]
    return name


def find_l1v_summary_paths(
    result_dir: Path,
    workloads: Iterable[str] | None = None,
) -> list[Path]:
    requested = set(workloads or [])
    paths = sorted(result_dir.glob(f"*{L1V_SUMMARY_SUFFIX}"))
    if not requested:
        return paths

    selected = []
    for path in paths:
        workload = infer_workload(path)
        if workload in requested or any(token in workload for token in requested):
            selected.append(path)
    return selected


def target_gpu_for(requester_gpm: int, owner_gpm: int, is_remote: bool) -> int:
    if is_remote:
        if owner_gpm >= 0:
            return owner_gpm
        if requester_gpm >= 0:
            return requester_gpm
        return 0

    if requester_gpm >= 0:
        return requester_gpm
    if owner_gpm >= 0:
        return owner_gpm
    return 0


def request_from_row(
    row: dict[str, str],
    path: Path,
    workload: str,
    fifo_position: int,
    mapping: MappingConfig,
) -> Request:
    paddr = parse_int(row.get("paddr"))
    cacheline_addr = parse_int(row.get("cacheline_addr"), paddr)
    page_paddr = parse_int(row.get("page_paddr"), paddr - (paddr % mapping.page_bytes))
    completion_time_ns = parse_int(row.get("completion_time_ns"))
    total_latency_ns = parse_int(row.get("total_l1v_path_latency_ns"))
    arrival_time_ns = max(0, completion_time_ns - total_latency_ns)
    requester_gpm = parse_int(row.get("requester_gpm"), -1)
    owner_gpm = parse_int(row.get("owner_gpm"), -1)
    is_remote = parse_bool(row.get("is_remote"))

    line_addr = paddr // mapping.cacheline_bytes
    page_id = paddr // mapping.page_bytes
    line_in_page = (paddr % mapping.page_bytes) // mapping.cacheline_bytes

    return Request(
        trace_name=path.stem,
        workload=workload,
        path_id=str(row.get("path_id", "")),
        sequence=parse_int(row.get("sequence"), fifo_position),
        fifo_position=fifo_position,
        arrival_time_ns=arrival_time_ns,
        completion_time_ns=completion_time_ns,
        total_l1v_path_latency_ns=total_latency_ns,
        access_type=str(row.get("access_type", "")),
        pid=parse_int(row.get("pid")),
        vaddr=parse_int(row.get("vaddr")),
        paddr=paddr,
        page_paddr=page_paddr,
        cacheline_addr=cacheline_addr,
        bytes=parse_int(row.get("bytes"), mapping.cacheline_bytes),
        requester_gpm=requester_gpm,
        owner_gpm=owner_gpm,
        target_gpu=target_gpu_for(requester_gpm, owner_gpm, is_remote),
        hops=parse_int(row.get("hops"), -1),
        is_remote=is_remote,
        route=str(row.get("route", "")),
        final_source=str(row.get("final_source", "")),
        l1v_result=str(row.get("l1v_result", "")),
        l2_result=str(row.get("l2_result", "")),
        parent_count=parse_int(row.get("parent_count"), 1),
        line_addr=line_addr,
        page_id=page_id,
        line_in_page=line_in_page,
        l2_slice=line_addr % mapping.num_l2_slices,
        channel=line_addr % mapping.num_hbm_channels,
        bank=(line_addr // mapping.num_hbm_channels) % mapping.num_hbm_banks,
        row=paddr // mapping.row_bytes,
    )


def load_requests(
    path: Path,
    mapping: MappingConfig,
    max_requests: int | None = None,
) -> list[Request]:
    workload = infer_workload(path)
    requests: list[Request] = []

    with path.open(newline="") as f:
        reader = csv.DictReader(f)
        for row_position, row in enumerate(reader):
            if max_requests is not None and len(requests) >= max_requests:
                break
            requests.append(
                request_from_row(
                    row=row,
                    path=path,
                    workload=workload,
                    fifo_position=row_position,
                    mapping=mapping,
                )
            )

    requests.sort(key=lambda r: (r.arrival_time_ns, r.sequence))
    return [replace(request, fifo_position=i) for i, request in enumerate(requests)]
