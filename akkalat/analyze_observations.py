#!/usr/bin/env python3
"""Turn the independent observation traces into paper-ready O1--O6 CSVs.

The script intentionally uses only the Python standard library.  It consumes
the files emitted by ``-trace-observation``:

* ``*_observation_paths.csv.gz`` for exclusive path timing and L1-originated
  addresses/events;
* ``*_observation_dram_locality.csv`` for mapper-derived physical DRAM
  locality;
* ``*_observation_remote_requests.csv.gz`` for passive RDMA demand streams;
* ``*_observation_l2_utilization.csv.gz`` for bounded L2 headroom samples; and
* ``*_observation_validation.csv`` and
  ``*_observation_remote_validation.csv`` for emitter/instrumentation
  invariants.

Accounting checks are strict by default.  A malformed completed path, a
non-zero residual, a duplicate boundary, or a time regression stops the
analysis instead of silently producing a misleading figure.
"""

from __future__ import annotations

import argparse
import csv
import glob
import gzip
import heapq
import math
import re
import struct
import sys
import tempfile
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Dict, Iterator, List, Mapping, MutableMapping, Optional
from typing import Sequence, Tuple


PATH_SUFFIX = "_observation_paths.csv.gz"
DRAM_SUFFIX = "_observation_dram_locality.csv"
REMOTE_SUFFIX = "_observation_remote_requests.csv.gz"
L2_UTIL_SUFFIX = "_observation_l2_utilization.csv.gz"
PATH_VALIDATION_SUFFIX = "_observation_validation.csv"
REMOTE_VALIDATION_SUFFIX = "_observation_remote_validation.csv"
RELAXED_PATH_SUFFIX = "_paths.csv.gz"
RELAXED_DRAM_SUFFIX = "_dram_locality.csv"
RELAXED_REMOTE_SUFFIX = "_remote_requests.csv.gz"
RELAXED_L2_UTIL_SUFFIX = "_l2_utilization.csv.gz"
RELAXED_PATH_VALIDATION_SUFFIX = "_validation.csv"
RELAXED_REMOTE_VALIDATION_SUFFIX = "_remote_validation.csv"

# The observation path schema deliberately suffixes every exclusive bucket
# with _ps.  These are timestamps/totals, not buckets, and must be excluded.
NON_STAGE_PS_COLUMNS = {
    "start_ps",
    "end_ps",
    "total_ps",
    "accounted_ps",
    "residual_ps",
}

DEFAULT_O2_WINDOWS_CYCLES = (0, 1, 2, 4, 8, 16, 32, 64)

# Filename parsing is unambiguous for observation runs because the simulator
# only permits the baseline data path.  The longer names make the fallback
# useful when traces are moved or renamed for comparisons.
KNOWN_CONFIGS = tuple(
    sorted(
        {
            "baseline",
            "dram_batch",
            "dram_row_reorder",
            "dram_batch_row_reorder",
            "local_optimization_only",
            "remote_request_only",
            "remote_l2_only",
            "all_three",
        },
        key=len,
        reverse=True,
    )
)


class AnalysisError(RuntimeError):
    """Raised when trace data violate an analysis invariant."""


@dataclass(frozen=True, order=True)
class TraceIdentity:
    run: str
    target: str
    benchmark: str
    config: str
    stem: str


@dataclass
class O1Validation:
    identity: TraceIdentity
    source_file: str
    rows: int = 0
    completed_rows: int = 0
    demand_reads: int = 0
    incomplete_demand_reads: int = 0
    accounting_errors: int = 0
    stage_sum_errors: int = 0
    duplicate_event_rows: int = 0
    time_regression_rows: int = 0
    unattributed_nonzero_rows: int = 0

    @property
    def passed(self) -> bool:
        return not (
            self.incomplete_demand_reads
            or self.accounting_errors
            or self.stage_sum_errors
            or self.duplicate_event_rows
            or self.time_regression_rows
        )


@dataclass(frozen=True)
class EmitterValidation:
    identity: TraceIdentity
    source_file: str
    emitter: str
    check: str
    value: int
    emitter_status: str
    passed: bool


@dataclass(frozen=True)
class O2Access:
    identity: TraceIdentity
    requester: str
    line64: int
    event_ps: int
    route: str


@dataclass
class O2Distribution:
    identity: TraceIdentity
    total: int
    distance_counts_ps: Dict[int, int]

    @property
    def matched_any_prior(self) -> int:
        return sum(self.distance_counts_ps.values())


@dataclass
class O2Diagnostics:
    identity: TraceIdentity
    path_rows: int = 0
    demand_reads: int = 0
    local_l2_miss_reads: int = 0
    missing_event: int = 0
    exact_duplicate_events_filtered: int = 0


@dataclass
class NumericSeries:
    counts: Dict[int, int]
    count: int = 0
    total_sum: int = 0
    nonzero: int = 0

    def add(self, value: int) -> None:
        self.counts[value] = self.counts.get(value, 0) + 1
        self.count += 1
        self.total_sum += value
        if value:
            self.nonzero += 1

    def merge(self, other: "NumericSeries") -> None:
        for value, count in other.counts.items():
            self.counts[value] = self.counts.get(value, 0) + count
        self.count += other.count
        self.total_sum += other.total_sum
        self.nonzero += other.nonzero

    def value_at_rank(self, rank: int) -> int:
        if rank < 0 or rank >= self.count:
            raise IndexError(rank)
        seen = 0
        for value, count in sorted(self.counts.items()):
            seen += count
            if rank < seen:
                return value
        raise AssertionError("histogram count mismatch")

    def percentile(self, fraction: float) -> float:
        if not self.count:
            return math.nan
        if self.count == 1:
            return float(next(iter(self.counts)))
        position = fraction * (self.count - 1)
        lower = math.floor(position)
        upper = math.ceil(position)
        lower_value = self.value_at_rank(lower)
        if lower == upper:
            return float(lower_value)
        upper_value = self.value_at_rank(upper)
        weight = position - lower
        return lower_value * (1.0 - weight) + upper_value * weight


def open_csv(path: Path):
    if path.name.endswith(".gz"):
        return gzip.open(path, "rt", newline="")
    return path.open("r", newline="")


def int_field(row: Mapping[str, str], name: str) -> int:
    value = row.get(name, "")
    if value is None or value == "":
        raise AnalysisError(f"missing integer field {name!r}")
    try:
        return int(value, 0)
    except ValueError as error:
        raise AnalysisError(f"invalid integer {name}={value!r}") from error


def bool_field(row: Mapping[str, str], name: str) -> bool:
    value = row.get(name, "").strip().lower()
    if value in {"true", "1", "yes"}:
        return True
    if value in {"false", "0", "no", ""}:
        return False
    raise AnalysisError(f"invalid boolean {name}={row.get(name)!r}")


def is_demand_read(row: Mapping[str, str]) -> bool:
    op = row.get("operation", "").strip().lower()
    return op == "read" or op.startswith("read_") or op == "load"


def parse_trace_identity(path: Path) -> TraceIdentity:
    name = path.name
    if name.endswith(PATH_SUFFIX):
        stem = name[: -len(PATH_SUFFIX)]
    elif name.endswith(DRAM_SUFFIX):
        stem = name[: -len(DRAM_SUFFIX)]
    elif name.endswith(REMOTE_SUFFIX):
        stem = name[: -len(REMOTE_SUFFIX)]
    elif name.endswith(L2_UTIL_SUFFIX):
        stem = name[: -len(L2_UTIL_SUFFIX)]
    elif name.endswith(REMOTE_VALIDATION_SUFFIX):
        stem = name[: -len(REMOTE_VALIDATION_SUFFIX)]
    elif name.endswith(PATH_VALIDATION_SUFFIX):
        stem = name[: -len(PATH_VALIDATION_SUFFIX)]
    elif name.endswith(RELAXED_PATH_SUFFIX):
        stem = name[: -len(RELAXED_PATH_SUFFIX)]
    elif name.endswith(RELAXED_DRAM_SUFFIX):
        stem = name[: -len(RELAXED_DRAM_SUFFIX)]
    elif name.endswith(RELAXED_REMOTE_SUFFIX):
        stem = name[: -len(RELAXED_REMOTE_SUFFIX)]
    elif name.endswith(RELAXED_L2_UTIL_SUFFIX):
        stem = name[: -len(RELAXED_L2_UTIL_SUFFIX)]
    elif name.endswith(RELAXED_REMOTE_VALIDATION_SUFFIX):
        stem = name[: -len(RELAXED_REMOTE_VALIDATION_SUFFIX)]
    elif name.endswith(RELAXED_PATH_VALIDATION_SUFFIX):
        stem = name[: -len(RELAXED_PATH_VALIDATION_SUFFIX)]
    else:
        stem = re.sub(r"\.(?:csv|csv\.gz)$", "", name)

    target, separator, remainder = stem.partition("_")
    if not separator:
        target, remainder = "unknown", stem

    config = "unknown"
    benchmark = remainder
    for candidate in KNOWN_CONFIGS:
        marker = "_" + candidate
        if remainder.endswith(marker):
            benchmark = remainder[: -len(marker)]
            config = candidate
            break
    if not benchmark:
        benchmark = "unknown"
    return TraceIdentity(
        run=path.parent.name or ".",
        target=target,
        benchmark=benchmark,
        config=config,
        stem=stem,
    )


def discover_inputs(
    values: Sequence[str],
) -> Tuple[List[Path], List[Path], List[Path], List[Path], List[Path], List[Path]]:
    # Directory scans intentionally use the narrow standard names, so old
    # memory-path CSVs in a result directory are never considered. Literal
    # files and files reached through a glob are instead classified by schema;
    # this supports arbitrary -trace-observation-file prefixes.
    candidates: Dict[Path, str] = {}

    def add_directory(directory: Path) -> None:
        for candidate in directory.rglob(f"*{PATH_SUFFIX}"):
            candidates[candidate.resolve()] = "directory"
        for candidate in directory.rglob(f"*{DRAM_SUFFIX}"):
            candidates[candidate.resolve()] = "directory"
        for candidate in directory.rglob(f"*{REMOTE_SUFFIX}"):
            candidates[candidate.resolve()] = "directory"
        for candidate in directory.rglob(f"*{L2_UTIL_SUFFIX}"):
            candidates[candidate.resolve()] = "directory"
        for candidate in directory.rglob(f"*{PATH_VALIDATION_SUFFIX}"):
            candidates[candidate.resolve()] = "directory"
        for candidate in directory.rglob(f"*{REMOTE_VALIDATION_SUFFIX}"):
            candidates[candidate.resolve()] = "directory"

    for value in values:
        path = Path(value).expanduser()
        if path.is_dir():
            add_directory(path)
            continue
        if path.is_file():
            candidates[path.resolve()] = "literal"
            continue
        matches = glob.glob(str(path), recursive=True)
        for match in matches:
            matched = Path(match)
            if matched.is_dir():
                add_directory(matched)
            elif matched.is_file():
                candidates[matched.resolve()] = "glob"

    path_files = []
    dram_files = []
    remote_files = []
    l2_util_files = []
    path_validation_files = []
    remote_validation_files = []
    for path, source in sorted(candidates.items()):
        if path.name.endswith(PATH_SUFFIX):
            path_files.append(path)
            continue
        if path.name.endswith(DRAM_SUFFIX):
            dram_files.append(path)
            continue
        if path.name.endswith(REMOTE_SUFFIX):
            remote_files.append(path)
            continue
        if path.name.endswith(L2_UTIL_SUFFIX):
            l2_util_files.append(path)
            continue
        if path.name.endswith(REMOTE_VALIDATION_SUFFIX):
            remote_validation_files.append(path)
            continue
        if path.name.endswith(PATH_VALIDATION_SUFFIX):
            path_validation_files.append(path)
            continue
        kind = classify_explicit_observation_file(path)
        if kind == "path":
            path_files.append(path)
        elif kind == "dram":
            dram_files.append(path)
        elif kind == "remote":
            remote_files.append(path)
        elif kind == "l2_util":
            l2_util_files.append(path)
        elif kind == "path_validation":
            path_validation_files.append(path)
        elif kind == "remote_validation":
            remote_validation_files.append(path)
        elif source == "literal":
            raise AnalysisError(
                f"explicit file is not an observation path/locality CSV: {path}"
            )
    if not any(
        (
            path_files,
            dram_files,
            remote_files,
            l2_util_files,
            path_validation_files,
            remote_validation_files,
        )
    ):
        raise AnalysisError(
            "no observation inputs found; expected "
            f"*{PATH_SUFFIX}, *{DRAM_SUFFIX}, *{REMOTE_SUFFIX}, "
            f"*{L2_UTIL_SUFFIX}, *{PATH_VALIDATION_SUFFIX}, "
            f"*{REMOTE_VALIDATION_SUFFIX}, or explicit files with their schemas"
        )
    return (
        sorted(set(path_files)),
        sorted(set(dram_files)),
        sorted(set(remote_files)),
        sorted(set(l2_util_files)),
        sorted(set(path_validation_files)),
        sorted(set(remote_validation_files)),
    )


def classify_explicit_observation_file(path: Path) -> Optional[str]:
    """Classify one explicitly supplied CSV using its header/schema.

    This is deliberately not used to broaden recursive directory discovery.
    A glob may include metrics, raw physical events, and tracer summaries; an
    unrecognized glob member is ignored, while an unrecognized literal is
    reported by ``discover_inputs``.
    """
    try:
        with open_csv(path) as stream:
            reader = csv.reader(stream)
            header = next(reader, None)
            if not header:
                return None
            fields = set(header)
            path_fields = {
                "schema_version",
                "path_id",
                "total_ps",
                "accounted_ps",
                "residual_ps",
                "events",
                "duplicate_events",
                "time_regressions",
            }
            if path_fields <= fields and any(
                field.endswith("_ps") and field not in NON_STAGE_PS_COLUMNS
                for field in fields
            ):
                first_row = next(reader, None)
                if first_row is None:
                    return "path"
                schema_index = header.index("schema_version")
                if schema_index < len(first_row) and first_row[schema_index] == (
                    "observation-path-v1"
                ):
                    return "path"
                return None
            dram_fields = {
                "relation",
                "distance",
                "direction",
                "window",
                "count",
                "total",
                "matched_within_max_window",
            }
            if dram_fields <= fields:
                return "dram"
            remote_fields = {
                "sequence",
                "status",
                "logical_request_id",
                "pid",
                "operation",
                "line_address",
                "requester_name",
                "owner_name",
                "manhattan_hops",
                "arrival_ps",
                "issue_ps",
                "completion_ps",
                "write_epoch",
                "same_line_inflight_at_arrival",
                "forward_traffic_bytes",
                "return_traffic_bytes",
                "total_traffic_bytes",
                "time_regression",
            }
            if remote_fields <= fields:
                return "remote"
            l2_fields = {
                "sequence",
                "time_ps",
                "cache_name",
                "valid_blocks",
                "total_blocks",
                "free_blocks",
                "occupancy_ppm",
                "dirty_blocks",
                "locked_blocks",
                "mshr_entries",
                "status",
            }
            if l2_fields <= fields:
                return "l2_util"
            if {"invariant", "value", "pass"} <= fields:
                return "path_validation"
            if {"metric", "value", "status"} <= fields:
                return "remote_validation"
    except (OSError, csv.Error, gzip.BadGzipFile, UnicodeError):
        return None
    return None


def read_emitter_validations(
    path_files: Sequence[Path], remote_files: Sequence[Path]
) -> List[EmitterValidation]:
    """Read the tracer's own invariant reports without weakening their schema.

    These reports validate the instrumentation itself, whereas the O1 and
    O4--O6 validation tables validate the emitted request rows. Keeping both
    layers visible prevents a well-formed row sample from hiding an emitter
    invariant failure.
    """
    rows: List[EmitterValidation] = []

    for path in path_files:
        identity = parse_trace_identity(path)
        seen = set()
        with open_csv(path) as stream:
            reader = csv.DictReader(stream)
            required = {"invariant", "value", "pass"}
            missing = required - set(reader.fieldnames or [])
            if missing:
                raise AnalysisError(
                    f"{path}: missing path-emitter validation columns "
                    f"{sorted(missing)}"
                )
            for line_number, row in enumerate(reader, start=2):
                check = row.get("invariant", "").strip()
                if not check:
                    raise AnalysisError(
                        f"{path}:{line_number}: empty validation invariant"
                    )
                if check in seen:
                    raise AnalysisError(
                        f"{path}:{line_number}: duplicate validation invariant "
                        f"{check!r}"
                    )
                seen.add(check)
                try:
                    value = int_field(row, "value")
                except AnalysisError as error:
                    raise AnalysisError(f"{path}:{line_number}: {error}") from error
                if value < 0:
                    raise AnalysisError(
                        f"{path}:{line_number}: negative validation value {value}"
                    )
                pass_text = row.get("pass", "").strip().lower()
                if pass_text not in {"true", "false"}:
                    raise AnalysisError(
                        f"{path}:{line_number}: invalid validation pass "
                        f"{row.get('pass')!r}; expected true or false"
                    )
                passed = pass_text == "true"
                rows.append(
                    EmitterValidation(
                        identity,
                        str(path),
                        "path",
                        check,
                        value,
                        "pass" if passed else "fail",
                        passed,
                    )
                )
        if not seen:
            raise AnalysisError(f"{path}: empty path-emitter validation report")

    for path in remote_files:
        identity = parse_trace_identity(path)
        seen = set()
        with open_csv(path) as stream:
            reader = csv.DictReader(stream)
            required = {"metric", "value", "status"}
            missing = required - set(reader.fieldnames or [])
            if missing:
                raise AnalysisError(
                    f"{path}: missing remote-emitter validation columns "
                    f"{sorted(missing)}"
                )
            for line_number, row in enumerate(reader, start=2):
                check = row.get("metric", "").strip()
                if not check:
                    raise AnalysisError(
                        f"{path}:{line_number}: empty validation metric"
                    )
                if check in seen:
                    raise AnalysisError(
                        f"{path}:{line_number}: duplicate validation metric "
                        f"{check!r}"
                    )
                seen.add(check)
                try:
                    value = int_field(row, "value")
                except AnalysisError as error:
                    raise AnalysisError(f"{path}:{line_number}: {error}") from error
                if value < 0:
                    raise AnalysisError(
                        f"{path}:{line_number}: negative validation value {value}"
                    )
                status = row.get("status", "").strip().lower()
                if status not in {"ok", "info", "error"}:
                    raise AnalysisError(
                        f"{path}:{line_number}: invalid remote validation "
                        f"status {row.get('status')!r}"
                    )
                rows.append(
                    EmitterValidation(
                        identity,
                        str(path),
                        "remote",
                        check,
                        value,
                        status,
                        status != "error",
                    )
                )
        if not seen:
            raise AnalysisError(f"{path}: empty remote-emitter validation report")

    return rows


def write_emitter_validations(
    output_dir: Path, rows: Sequence[EmitterValidation]
) -> None:
    with (output_dir / "emitter_instrumentation_validation.csv").open(
        "w", newline=""
    ) as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "emitter",
                "source_file",
                "check",
                "value",
                "emitter_status",
                "strict_pass",
            ]
        )
        for item in sorted(
            rows,
            key=lambda value: (
                value.identity,
                value.emitter,
                value.source_file,
                value.check,
            ),
        ):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    item.emitter,
                    item.source_file,
                    item.check,
                    item.value,
                    item.emitter_status,
                    str(item.passed).lower(),
                ]
            )


def enforce_emitter_validations(rows: Sequence[EmitterValidation]) -> None:
    failed = [item for item in rows if not item.passed]
    if not failed:
        return
    details = "; ".join(
        f"{Path(item.source_file).name}:{item.check}="
        f"{item.value} ({item.emitter_status})"
        for item in failed
    )
    raise AnalysisError("emitter/instrumentation validation failed: " + details)


def add_missing_emitter_validation_checks(
    rows: List[EmitterValidation],
    path_files: Sequence[Path],
    remote_files: Sequence[Path],
    path_validation_files: Sequence[Path],
    remote_validation_files: Sequence[Path],
) -> None:
    """Make a missing emitter report an explicit strict failure.

    Raw-row validation cannot detect every missing architectural hook.  A
    strict paper analysis therefore requires the emitter's own invariant file
    for every path and remote trace identity, rather than silently treating an
    omitted validation file as success.
    """

    def add_missing(
        raw_files: Sequence[Path],
        validation_files: Sequence[Path],
        emitter: str,
    ) -> None:
        covered = {parse_trace_identity(path) for path in validation_files}
        for raw_path in raw_files:
            identity = parse_trace_identity(raw_path)
            if identity in covered:
                continue
            rows.append(
                EmitterValidation(
                    identity=identity,
                    source_file=str(raw_path),
                    emitter=emitter,
                    check="validation_file_present",
                    value=0,
                    emitter_status="missing",
                    passed=False,
                )
            )

    add_missing(path_files, path_validation_files, "path")
    add_missing(remote_files, remote_validation_files, "remote")


def parse_events(value: str) -> Dict[str, int]:
    events: Dict[str, int] = {}
    if not value:
        return events
    for item in value.split(";"):
        if not item:
            continue
        name, separator, timestamp = item.rpartition("@")
        if not separator or not name:
            raise AnalysisError(f"invalid observation event {item!r}")
        if name in events:
            raise AnalysisError(f"duplicate serialized observation event {name!r}")
        try:
            events[name] = int(timestamp)
        except ValueError as error:
            raise AnalysisError(f"invalid observation event time {item!r}") from error
    return events


def demand_read_path_class(row: Mapping[str, str]) -> str:
    if row.get("l1_role", "") != "leader":
        return row.get("l1_role", "") or "follower"
    route = row.get("route", "unknown") or "unknown"
    source = row.get("source", "unknown") or "unknown"
    if route == "l1_hit" or source == "l1":
        return "l1_hit"
    return f"{route}_{source}"


def _identity_fields(identity: TraceIdentity) -> List[str]:
    return [
        identity.run,
        identity.target,
        identity.benchmark,
        identity.config,
        identity.stem,
    ]


IDENTITY_HEADER = ["run", "target", "benchmark", "config", "trace_stem"]


def analyze_paths(
    path_files: Sequence[Path],
    *,
    o2_event: str,
    o2_include_remote: bool,
    strict: bool,
) -> Tuple[
    MutableMapping[Tuple[TraceIdentity, str, str, str, str], NumericSeries],
    List[O1Validation],
    List[O2Distribution],
    List[O2Diagnostics],
]:
    o1: MutableMapping[
        Tuple[TraceIdentity, str, str, str, str], NumericSeries
    ] = defaultdict(lambda: NumericSeries({}))
    validations: List[O1Validation] = []
    o2_distributions: List[O2Distribution] = []
    o2_diagnostics: List[O2Diagnostics] = []

    for path in path_files:
        identity = parse_trace_identity(path)
        validation = O1Validation(identity=identity, source_file=str(path))
        diagnostic = O2Diagnostics(identity=identity)
        seen_o2_events = set()
        file_o2_accesses: List[O2Access] = []

        with open_csv(path) as stream:
            reader = csv.DictReader(stream)
            if reader.fieldnames is None:
                raise AnalysisError(f"empty path trace: {path}")
            required = {
                "schema_version",
                "operation",
                "address",
                "l1_cache",
                "l1_role",
                "l1_result",
                "l2_result",
                "status",
                "start_ps",
                "end_ps",
                "total_ps",
                "accounted_ps",
                "residual_ps",
                "route",
                "remote",
                "source",
                "events",
                "duplicate_events",
                "time_regressions",
            }
            missing = required - set(reader.fieldnames)
            if missing:
                raise AnalysisError(f"{path}: missing columns {sorted(missing)}")
            stage_columns = [
                column
                for column in reader.fieldnames
                if column.endswith("_ps") and column not in NON_STAGE_PS_COLUMNS
            ]
            if not stage_columns:
                raise AnalysisError(f"{path}: no exclusive stage columns")

            for line_number, row in enumerate(reader, start=2):
                validation.rows += 1
                diagnostic.path_rows += 1
                try:
                    if row.get("schema_version") != "observation-path-v1":
                        raise AnalysisError(
                            f"unsupported schema {row.get('schema_version')!r}"
                        )
                    start = int_field(row, "start_ps")
                    end = int_field(row, "end_ps")
                    total = int_field(row, "total_ps")
                    accounted = int_field(row, "accounted_ps")
                    residual = int_field(row, "residual_ps")
                    stage_sum = sum(int_field(row, column) for column in stage_columns)
                    duplicate_events = int_field(row, "duplicate_events")
                    time_regressions = int_field(row, "time_regressions")
                except AnalysisError as error:
                    raise AnalysisError(f"{path}:{line_number}: {error}") from error

                if row.get("status") == "complete":
                    validation.completed_rows += 1
                if end < start or end - start != total or total != accounted or residual != 0:
                    validation.accounting_errors += 1
                if stage_sum != accounted:
                    validation.stage_sum_errors += 1
                if duplicate_events:
                    validation.duplicate_event_rows += 1
                if time_regressions:
                    validation.time_regression_rows += 1
                if int(row.get("unattributed_ps", "0") or 0):
                    validation.unattributed_nonzero_rows += 1

                if not is_demand_read(row):
                    continue
                validation.demand_reads += 1
                diagnostic.demand_reads += 1
                if row.get("status") != "complete":
                    validation.incomplete_demand_reads += 1
                    continue

                route = row.get("route", "unknown") or "unknown"
                source = row.get("source", "unknown") or "unknown"
                path_class = demand_read_path_class(row)
                for stage_column in stage_columns + ["total_ps"]:
                    stage = stage_column[: -len("_ps")]
                    value = int_field(row, stage_column)
                    o1[(identity, route, source, path_class, stage)].add(value)

                # O2 intentionally studies the local L2-miss stream that can
                # feed the 64B-to-128B batching mechanism.  MSHR followers do
                # not issue a physical L2 miss and therefore are not samples.
                if row.get("l1_role", "leader") != "leader":
                    continue
                if not o2_include_remote and bool_field(row, "remote"):
                    continue
                if "miss" not in row.get("l1_result", "").lower():
                    continue
                if "miss" not in row.get("l2_result", "").lower():
                    continue
                if not o2_include_remote and source != "dram":
                    continue
                diagnostic.local_l2_miss_reads += 1
                events = parse_events(row.get("events", ""))
                event_ps = events.get(o2_event)
                if event_ps is None:
                    diagnostic.missing_event += 1
                    continue
                address = int_field(row, "address")
                line64 = address >> 6
                requester = row.get("l1_cache", "")
                exact_key = (requester, line64, event_ps)
                if exact_key in seen_o2_events:
                    diagnostic.exact_duplicate_events_filtered += 1
                    continue
                seen_o2_events.add(exact_key)
                file_o2_accesses.append(
                    O2Access(identity, requester, line64, event_ps, route)
                )

        validations.append(validation)
        o2_diagnostics.append(diagnostic)
        o2_distributions.append(build_o2_distribution(identity, file_o2_accesses))

    if strict:
        failed = [item for item in validations if not item.passed]
        missing_events = sum(item.missing_event for item in o2_diagnostics)
        if failed or missing_events:
            details = []
            for item in failed:
                details.append(
                    f"{Path(item.source_file).name}: incomplete={item.incomplete_demand_reads}, "
                    f"accounting={item.accounting_errors}, stage_sum={item.stage_sum_errors}, "
                    f"duplicates={item.duplicate_event_rows}, "
                    f"regressions={item.time_regression_rows}"
                )
            if missing_events:
                details.append(
                    f"{missing_events} selected O2 L2-miss paths lack event {o2_event!r}"
                )
            raise AnalysisError("strict observation validation failed: " + "; ".join(details))

    return o1, validations, o2_distributions, o2_diagnostics


def write_o1(
    output_dir: Path,
    aggregates: Mapping[Tuple[TraceIdentity, str, str, str, str], NumericSeries],
    validations: Sequence[O1Validation],
) -> None:
    output = output_dir / "o1_exclusive_stage_breakdown.csv"
    with output.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "route",
                "source",
                "path_class",
                "analysis_unit",
                "stage",
                "demand_read_paths",
                "nonzero_paths",
                "mean_ps",
                "p50_ps",
                "p95_ps",
                "sum_ps",
                "fraction_of_group_total_latency",
                "mean_ns",
                "p50_ns",
                "p95_ns",
            ]
        )
        all_routes: MutableMapping[Tuple[TraceIdentity, str], NumericSeries] = (
            defaultdict(lambda: NumericSeries({}))
        )
        for (identity, _route, _source, _class, stage), series in aggregates.items():
            all_routes[(identity, stage)].merge(series)
        entries = list(aggregates.items())
        entries.extend(
            (
                (identity, "all", "all", "all", stage),
                series,
            )
            for (identity, stage), series in all_routes.items()
        )
        entry_lookup = {key: series for key, series in entries}
        for key, series in sorted(entries, key=lambda item: item[0]):
            identity, route, source, path_class, stage = key
            mean = series.total_sum / series.count
            p50 = series.percentile(0.50)
            p95 = series.percentile(0.95)
            total_series = entry_lookup[
                (identity, route, source, path_class, "total")
            ]
            latency_fraction = (
                series.total_sum / total_series.total_sum
                if total_series.total_sum
                else 0.0
            )
            writer.writerow(
                _identity_fields(identity)
                + [
                    route,
                    source,
                    path_class,
                    "post_coalesced_l1_transaction",
                    stage,
                    series.count,
                    series.nonzero,
                    format(mean, ".9g"),
                    format(p50, ".9g"),
                    format(p95, ".9g"),
                    series.total_sum,
                    format(latency_fraction, ".12g"),
                    format(mean / 1000.0, ".9g"),
                    format(p50 / 1000.0, ".9g"),
                    format(p95 / 1000.0, ".9g"),
                ]
            )

    with (output_dir / "o1_validation.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "source_file",
                "rows",
                "completed_rows",
                "demand_reads",
                "incomplete_demand_reads",
                "accounting_errors",
                "stage_sum_errors",
                "duplicate_event_rows",
                "time_regression_rows",
                "unattributed_nonzero_rows",
                "strict_pass",
            ]
        )
        for item in sorted(validations, key=lambda value: value.identity):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    item.source_file,
                    item.rows,
                    item.completed_rows,
                    item.demand_reads,
                    item.incomplete_demand_reads,
                    item.accounting_errors,
                    item.stage_sum_errors,
                    item.duplicate_event_rows,
                    item.time_regression_rows,
                    item.unattributed_nonzero_rows,
                    str(item.passed).lower(),
                ]
            )


def parse_cycle_windows(value: str) -> Tuple[int, ...]:
    try:
        windows = tuple(sorted({int(item.strip()) for item in value.split(",")}))
    except ValueError as error:
        raise argparse.ArgumentTypeError("windows must be comma-separated integers") from error
    if not windows or windows[0] < 0:
        raise argparse.ArgumentTypeError("windows must be non-negative")
    return windows


def build_o2_distribution(
    identity: TraceIdentity, accesses: List[O2Access]
) -> O2Distribution:
    accesses.sort(key=lambda value: (value.event_ps, value.requester, value.line64))
    last_by_requester_line: Dict[Tuple[str, int], int] = {}
    distances: Dict[int, int] = {}
    for sample in accesses:
        # XOR selects the other 64-B half of the same aligned 128-B DRAM
        # access unit.  It excludes exact-line reuse by construction.
        sibling_line = sample.line64 ^ 1
        previous = last_by_requester_line.get((sample.requester, sibling_line))
        if previous is not None:
            distance = sample.event_ps - previous
            if distance < 0:
                raise AnalysisError("O2 event order regression after sorting")
            distances[distance] = distances.get(distance, 0) + 1
        last_by_requester_line[(sample.requester, sample.line64)] = sample.event_ps
    return O2Distribution(identity, len(accesses), distances)


def compute_o2(
    distributions: Sequence[O2Distribution],
    windows_cycles: Sequence[int],
    cycle_ps: int,
) -> List[List[object]]:
    output: List[List[object]] = []
    for distribution in sorted(distributions, key=lambda value: value.identity):
        identity = distribution.identity
        total = distribution.total
        finite_count = distribution.matched_any_prior
        max_ps = max(windows_cycles) * cycle_ps
        matched_within_max = sum(
            count
            for value, count in distribution.distance_counts_ps.items()
            if value <= max_ps
        )
        for window_cycles in windows_cycles:
            window_ps = window_cycles * cycle_ps
            count = sum(
                occurrences
                for value, occurrences in distribution.distance_counts_ps.items()
                if value <= window_ps
            )
            fraction = count / total if total else 0.0
            conditional = count / finite_count if finite_count else 0.0
            output.append(
                _identity_fields(identity)
                + [
                    "same_requester_same_128B_sibling",
                    "nearest_prior",
                    window_cycles,
                    window_ps,
                    format(window_ps / 1000.0, ".9g"),
                    count,
                    total,
                    finite_count,
                    matched_within_max,
                    format(fraction, ".12g"),
                    format(conditional, ".12g"),
                ]
            )
    return output


def write_o2(
    output_dir: Path,
    rows: Sequence[Sequence[object]],
    diagnostics: Sequence[O2Diagnostics],
    event_name: str,
) -> None:
    with (output_dir / "o2_adjacent_line_short_window_cdf.csv").open(
        "w", newline=""
    ) as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "relation",
                "direction",
                "window_l1_cycles",
                "window_ps",
                "window_ns",
                "count",
                "total_l2_miss_reads",
                "matched_any_prior",
                "matched_within_max_window",
                "cdf_fraction_all_requests",
                "cdf_fraction_conditional_on_match",
            ]
        )
        writer.writerows(rows)

    with (output_dir / "o2_validation.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "event",
                "path_rows",
                "demand_reads",
                "local_l2_miss_reads",
                "missing_event",
                "exact_duplicate_events_filtered",
            ]
        )
        for item in sorted(diagnostics, key=lambda value: value.identity):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    event_name,
                    item.path_rows,
                    item.demand_reads,
                    item.local_l2_miss_reads,
                    item.missing_event,
                    item.exact_duplicate_events_filtered,
                ]
            )


def analyze_o3(dram_files: Sequence[Path]) -> List[List[object]]:
    # Aggregate repeated shards by experiment and CDF cell using integer
    # counts.  Fractions are always recomputed; averaging fractions would give
    # small shards too much weight.
    cells: MutableMapping[
        Tuple[TraceIdentity, str, str, str, int], List[int]
    ] = defaultdict(lambda: [0, 0, 0])
    for path in dram_files:
        identity = parse_trace_identity(path)
        with path.open("r", newline="") as stream:
            reader = csv.DictReader(stream)
            required = {
                "relation",
                "distance",
                "direction",
                "window",
                "count",
                "total",
                "matched_within_max_window",
            }
            missing = required - set(reader.fieldnames or [])
            if missing:
                raise AnalysisError(f"{path}: missing O3 columns {sorted(missing)}")
            for line_number, row in enumerate(reader, start=2):
                try:
                    count = int_field(row, "count")
                    total = int_field(row, "total")
                    matched = int_field(row, "matched_within_max_window")
                    key = (
                        identity,
                        row["relation"],
                        row["distance"],
                        row["direction"],
                        int_field(row, "window"),
                    )
                    if count > total or matched > total:
                        raise AnalysisError(
                            f"count={count}, matched={matched}, total={total}"
                        )
                    if row.get("fraction", ""):
                        reported = float(row["fraction"])
                        expected = count / total if total else 0.0
                        if not math.isclose(reported, expected, rel_tol=1e-9, abs_tol=1e-12):
                            raise AnalysisError(
                                f"reported fraction {reported} != {count}/{total}"
                            )
                    cells[key][0] += count
                    cells[key][1] += total
                    cells[key][2] += matched
                except (AnalysisError, ValueError) as error:
                    raise AnalysisError(f"{path}:{line_number}: {error}") from error

    curves: MutableMapping[
        Tuple[TraceIdentity, str, str, str], List[Tuple[int, int, int, int]]
    ] = defaultdict(list)
    totals_by_identity: MutableMapping[TraceIdentity, set] = defaultdict(set)
    for key, values in cells.items():
        identity, relation, distance, direction, window = key
        count, total, matched = values
        curves[(identity, relation, distance, direction)].append(
            (window, count, total, matched)
        )
        totals_by_identity[identity].add(total)
    for curve_key, points in curves.items():
        points.sort()
        counts = [point[1] for point in points]
        totals = {point[2] for point in points}
        matched = {point[3] for point in points}
        if any(after < before for before, after in zip(counts, counts[1:])):
            raise AnalysisError(f"non-monotonic O3 CDF: {curve_key}")
        if len(totals) != 1 or len(matched) != 1:
            raise AnalysisError(f"inconsistent O3 curve denominators: {curve_key}")
        if points and counts[-1] != points[-1][3]:
            raise AnalysisError(
                f"last O3 window does not equal matched-within-max: {curve_key}"
            )
    for identity, totals in totals_by_identity.items():
        if len(totals) != 1:
            raise AnalysisError(
                f"physical relation denominators differ for {identity.stem}: {sorted(totals)}"
            )

    rows: List[List[object]] = []
    for key in sorted(cells):
        identity, relation, distance, direction, window = key
        count, total, matched = cells[key]
        if count > total or matched > total:
            raise AnalysisError(
                f"invalid O3 histogram {identity.stem}/{relation}/{distance}/{window}: "
                f"count={count}, matched={matched}, total={total}"
            )
        fraction = count / total if total else 0.0
        coverage = matched / total if total else 0.0
        rows.append(
            _identity_fields(identity)
            + [
                relation,
                o3_relation_class(relation),
                distance,
                direction,
                window,
                count,
                total,
                matched,
                format(fraction, ".12g"),
                format(coverage, ".12g"),
            ]
        )
    return rows


def o3_relation_class(relation: str) -> str:
    return {
        "same_access_unit": "128B_merge_opportunity",
        "same_row_different_column": "row_activation_reuse",
        "same_bank_different_row": "row_conflict",
        "different_bank_same_controller": "bank_parallelism",
    }.get(relation, "unknown")


O3_HEADER = IDENTITY_HEADER + [
    "relation",
    "relation_class",
    "distance",
    "direction",
    "window",
    "count",
    "total_physical_read_arrivals",
    "matched_within_max_window",
    "cdf_fraction",
    "coverage_within_max_window",
]


def write_o3(output_dir: Path, rows: Sequence[Sequence[object]]) -> None:
    with (output_dir / "o3_physical_locality_cdf.csv").open(
        "w", newline=""
    ) as stream:
        writer = csv.writer(stream)
        writer.writerow(O3_HEADER)
        writer.writerows(rows)

    # This explicit long form lets plotting code map relation to the heatmap Y
    # axis, window to X, and value to color without parsing the CDF table.
    with (output_dir / "o3_physical_locality_heatmap_long.csv").open(
        "w", newline=""
    ) as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "heatmap_y_relation",
                "relation_class",
                "heatmap_x_distance",
                "heatmap_x_window",
                "value_cdf_fraction",
                "coverage_within_max_window",
                "sample_count",
                "sample_total",
            ]
        )
        for row in rows:
            writer.writerow(
                row[:5]
                + [
                    row[5],
                    row[6],
                    row[7],
                    row[9],
                    row[13],
                    row[14],
                    row[10],
                    row[11],
                ]
            )


REMOTE_SORT_CHUNK_ROWS = 65_536


class ExternalTupleSorter:
    """Exact bounded-memory external sort for non-negative integer tuples."""

    def __init__(self, width: int, label: str, chunk_rows: int = REMOTE_SORT_CHUNK_ROWS):
        self.width = width
        self.chunk_rows = chunk_rows
        self.record = struct.Struct("<" + "Q" * width)
        self.directory = tempfile.TemporaryDirectory(prefix=f"observation-{label}-")
        self.buffer: List[Tuple[int, ...]] = []
        self.chunks: List[Path] = []

    def add(self, values: Tuple[int, ...]) -> None:
        if len(values) != self.width or any(value < 0 for value in values):
            raise AnalysisError(f"invalid external-sort tuple {values!r}")
        self.buffer.append(values)
        if len(self.buffer) >= self.chunk_rows:
            self._flush()

    def _flush(self) -> None:
        if not self.buffer:
            return
        self.buffer.sort()
        path = Path(self.directory.name) / f"chunk-{len(self.chunks):05d}.bin"
        payload = bytearray()
        for values in self.buffer:
            payload.extend(self.record.pack(*values))
        with path.open("wb") as stream:
            stream.write(payload)
        self.chunks.append(path)
        self.buffer.clear()

    def _read_chunk(self, path: Path) -> Iterator[Tuple[int, ...]]:
        block_size = self.record.size * 4096
        with path.open("rb") as stream:
            while True:
                block = stream.read(block_size)
                if not block:
                    break
                if len(block) % self.record.size:
                    raise AnalysisError(f"truncated external-sort chunk {path}")
                yield from self.record.iter_unpack(block)

    def merged(self) -> Iterator[Tuple[int, ...]]:
        self._flush()
        yield from heapq.merge(*(self._read_chunk(path) for path in self.chunks))

    def close(self) -> None:
        self.buffer.clear()
        self.directory.cleanup()


class NameInterner:
    def __init__(self) -> None:
        self.ids: Dict[str, int] = {}
        self.names: List[str] = []

    def intern(self, name: str) -> int:
        value = self.ids.get(name)
        if value is not None:
            return value
        value = len(self.names)
        self.ids[name] = value
        self.names.append(name)
        return value


@dataclass
class RemoteValidation:
    identity: TraceIdentity
    source_file: str
    rows: int = 0
    completed: int = 0
    incomplete: int = 0
    malformed_rows: int = 0
    identity_errors: int = 0
    accounting_errors: int = 0
    time_regressions: int = 0
    sequence_regressions: int = 0

    @property
    def passed(self) -> bool:
        return not (
            self.incomplete
            or self.malformed_rows
            or self.identity_errors
            or self.accounting_errors
            or self.time_regressions
            or self.sequence_regressions
        )


@dataclass
class O4Aggregate:
    identity: TraceIdentity
    requests: int = 0
    reads: int = 0
    writes: int = 0
    other_ops: int = 0
    logical_bytes: int = 0
    forward_bytes: int = 0
    return_bytes: int = 0
    total_network_bytes: int = 0
    forward_byte_hops: int = 0
    return_byte_hops: int = 0
    total_byte_hops: int = 0
    hops: NumericSeries = None
    queue_wait: NumericSeries = None
    service: NumericSeries = None
    latency: NumericSeries = None

    def __post_init__(self) -> None:
        self.hops = NumericSeries({})
        self.queue_wait = NumericSeries({})
        self.service = NumericSeries({})
        self.latency = NumericSeries({})


@dataclass
class O5ExactAggregate:
    identity: TraceIdentity
    read_requests: int = 0
    read_logical_bytes: int = 0
    read_network_bytes: int = 0
    exact_requests: int = 0
    exact_logical_bytes: int = 0
    exact_network_bytes: int = 0
    inflight_predecessors: int = 0


@dataclass
class O6ReuseResult:
    identity: TraceIdentity
    total_reads: int
    unique_keys: int
    reused_keys: int
    repeated_reads: int
    max_accesses: int
    gini: float
    frequency: Dict[int, int]
    heavy: List[Tuple[int, Tuple[int, int, int, int, int]]]
    requester_names: List[str]
    owner_names: List[str]


@dataclass
class L2Validation:
    identity: TraceIdentity
    source_file: str
    rows: int = 0
    valid_rows: int = 0
    invalid_status: int = 0
    accounting_errors: int = 0

    @property
    def passed(self) -> bool:
        return not (self.invalid_status or self.accounting_errors)


@dataclass
class O6L2Aggregate:
    identity: TraceIdentity
    samples: int = 0
    under50: int = 0
    occupancy_ppm: NumericSeries = None
    free_ppm: NumericSeries = None
    free_blocks: NumericSeries = None
    mshr_entries: NumericSeries = None

    def __post_init__(self) -> None:
        self.occupancy_ppm = NumericSeries({})
        self.free_ppm = NumericSeries({})
        self.free_blocks = NumericSeries({})
        self.mshr_entries = NumericSeries({})


REMOTE_REQUIRED_COLUMNS = {
    "sequence",
    "status",
    "logical_request_id",
    "pid",
    "operation",
    "address",
    "line_address",
    "bytes",
    "requester_name",
    "owner_name",
    "requester_gpu",
    "owner_gpu",
    "manhattan_hops",
    "arrival_ps",
    "issue_ps",
    "completion_ps",
    "queue_wait_ps",
    "service_ps",
    "total_ps",
    "write_epoch",
    "same_line_inflight_at_arrival",
    "forward_traffic_bytes",
    "return_traffic_bytes",
    "total_traffic_bytes",
    "time_regression",
}


def remote_is_read(operation: str) -> bool:
    operation = operation.strip().lower()
    return operation == "read" or operation == "load" or operation.startswith("read_")


def remote_is_write(operation: str) -> bool:
    operation = operation.strip().lower()
    return operation == "write" or operation == "store" or operation.startswith("write_")


def analyze_remote_files(
    remote_files: Sequence[Path],
    *,
    strict: bool,
    remote_cycle_ps: int,
    heavy_hitters: int,
) -> Tuple[
    List[O4Aggregate],
    List[O5ExactAggregate],
    List[O2Distribution],
    List[O6ReuseResult],
    List[RemoteValidation],
]:
    o4_results: List[O4Aggregate] = []
    exact_results: List[O5ExactAggregate] = []
    spatial_results: List[O2Distribution] = []
    reuse_results: List[O6ReuseResult] = []
    validations: List[RemoteValidation] = []

    for path in remote_files:
        identity = parse_trace_identity(path)
        validation = RemoteValidation(identity, str(path))
        o4 = O4Aggregate(identity)
        exact = O5ExactAggregate(identity)
        requester_names = NameInterner()
        owner_names = NameInterner()
        spatial_sorter = ExternalTupleSorter(7, "remote-spatial")
        reuse_sorter = ExternalTupleSorter(5, "remote-reuse")
        try:
            with open_csv(path) as stream:
                reader = csv.DictReader(stream)
                missing = REMOTE_REQUIRED_COLUMNS - set(reader.fieldnames or [])
                if missing:
                    raise AnalysisError(
                        f"{path}: missing remote columns {sorted(missing)}"
                    )
                for line_number, row in enumerate(reader, start=2):
                    validation.rows += 1
                    try:
                        sequence = int_field(row, "sequence")
                        pid = int_field(row, "pid")
                        address = int_field(row, "address")
                        line_address = int_field(row, "line_address")
                        byte_size = int_field(row, "bytes")
                        requester_gpu = int_field(row, "requester_gpu")
                        owner_gpu = int_field(row, "owner_gpu")
                        hops = int_field(row, "manhattan_hops")
                        arrival = int_field(row, "arrival_ps")
                        issue = int_field(row, "issue_ps")
                        epoch = int_field(row, "write_epoch")
                        inflight = int_field(row, "same_line_inflight_at_arrival")
                        forward = int_field(row, "forward_traffic_bytes")
                        returned = int_field(row, "return_traffic_bytes")
                        network = int_field(row, "total_traffic_bytes")
                        regression = bool_field(row, "time_regression")
                    except AnalysisError as error:
                        validation.malformed_rows += 1
                        if strict:
                            raise AnalysisError(f"{path}:{line_number}: {error}") from error
                        continue

                    if row.get("status") != "complete":
                        validation.incomplete += 1
                        continue
                    validation.completed += 1
                    try:
                        completion = int_field(row, "completion_ps")
                        queue_wait = int_field(row, "queue_wait_ps")
                        service = int_field(row, "service_ps")
                        total_latency = int_field(row, "total_ps")
                    except AnalysisError as error:
                        validation.malformed_rows += 1
                        if strict:
                            raise AnalysisError(f"{path}:{line_number}: {error}") from error
                        continue

                    identity_error = (
                        not row.get("logical_request_id")
                        or not row.get("requester_name")
                        or not row.get("owner_name")
                        or requester_gpu < 0
                        or owner_gpu < 0
                        or hops < 0
                        or byte_size <= 0
                        or line_address != address & ~63
                    )
                    accounting_error = (
                        issue < arrival
                        or completion < issue
                        or queue_wait != issue - arrival
                        or service != completion - issue
                        or total_latency != completion - arrival
                        or total_latency != queue_wait + service
                        or network != forward + returned
                    )
                    if identity_error:
                        validation.identity_errors += 1
                    if accounting_error:
                        validation.accounting_errors += 1
                    if regression:
                        validation.time_regressions += 1
                    if identity_error or accounting_error or regression:
                        continue

                    operation = row.get("operation", "")
                    o4.requests += 1
                    if remote_is_read(operation):
                        o4.reads += 1
                    elif remote_is_write(operation):
                        o4.writes += 1
                    else:
                        o4.other_ops += 1
                    o4.logical_bytes += byte_size
                    o4.forward_bytes += forward
                    o4.return_bytes += returned
                    o4.total_network_bytes += network
                    o4.forward_byte_hops += forward * hops
                    o4.return_byte_hops += returned * hops
                    o4.total_byte_hops += network * hops
                    o4.hops.add(hops)
                    o4.queue_wait.add(queue_wait)
                    o4.service.add(service)
                    o4.latency.add(total_latency)

                    if not remote_is_read(operation):
                        continue
                    exact.read_requests += 1
                    exact.read_logical_bytes += byte_size
                    exact.read_network_bytes += network
                    if inflight > 0:
                        exact.exact_requests += 1
                        exact.exact_logical_bytes += byte_size
                        exact.exact_network_bytes += network
                        exact.inflight_predecessors += inflight

                    requester_id = requester_names.intern(row["requester_name"])
                    owner_id = owner_names.intern(row["owner_name"])
                    reuse_sorter.add(
                        (pid, requester_id, owner_id, line_address, epoch)
                    )
                    # Exact inflight followers belong to the dedup category,
                    # not the orthogonal spatial-page opportunity.
                    if inflight == 0:
                        spatial_sorter.add(
                            (
                                sequence,
                                arrival,
                                pid,
                                requester_id,
                                owner_id,
                                line_address >> 12,
                                line_address >> 6,
                            )
                        )

            spatial, sequence_regressions = build_remote_spatial_distribution(
                identity, spatial_sorter.merged(), remote_cycle_ps
            )
            validation.sequence_regressions += sequence_regressions
            reuse = build_remote_reuse_result(
                identity,
                reuse_sorter.merged(),
                requester_names.names,
                owner_names.names,
                heavy_hitters,
            )
        finally:
            spatial_sorter.close()
            reuse_sorter.close()

        o4_results.append(o4)
        exact_results.append(exact)
        spatial_results.append(spatial)
        reuse_results.append(reuse)
        validations.append(validation)

    if strict:
        failed = [item for item in validations if not item.passed]
        if failed:
            raise AnalysisError(
                "strict remote validation failed: "
                + "; ".join(
                    f"{Path(item.source_file).name}: incomplete={item.incomplete}, "
                    f"malformed={item.malformed_rows}, identity={item.identity_errors}, "
                    f"accounting={item.accounting_errors}, "
                    f"time={item.time_regressions}, sequence={item.sequence_regressions}"
                    for item in failed
                )
            )
    return o4_results, exact_results, spatial_results, reuse_results, validations


def build_remote_spatial_distribution(
    identity: TraceIdentity,
    ordered: Iterator[Tuple[int, ...]],
    cycle_ps: int,
) -> Tuple[O2Distribution, int]:
    max_distance = max(DEFAULT_O2_WINDOWS_CYCLES) * cycle_ps
    # group -> [latest_line, latest_time, other_line, other_time, version]
    states: Dict[Tuple[int, int, int, int], List[int]] = {}
    expiry: List[Tuple[int, int, Tuple[int, int, int, int]]] = []
    distance_counts: Dict[int, int] = {}
    total = 0
    previous_arrival = -1
    regressions = 0

    for _sequence, arrival, pid, requester, owner, page, line in ordered:
        total += 1
        if arrival < previous_arrival:
            regressions += 1
        previous_arrival = max(previous_arrival, arrival)
        while expiry and expiry[0][0] < arrival:
            _expires, version, group = heapq.heappop(expiry)
            state = states.get(group)
            if state is not None and state[4] == version:
                del states[group]

        group = (pid, requester, owner, page)
        state = states.get(group)
        previous = None
        if state is not None:
            latest_line, latest_time, other_line, other_time, version = state
            if arrival - latest_time > max_distance:
                state = None
            elif latest_line != line:
                previous = latest_time
            elif other_line != line and arrival - other_time <= max_distance:
                previous = other_time
        if previous is not None:
            distance = arrival - previous
            distance_counts[distance] = distance_counts.get(distance, 0) + 1

        if state is None:
            version = 1
            states[group] = [line, arrival, line, arrival, version]
        else:
            latest_line, latest_time, other_line, other_time, version = state
            version += 1
            if latest_line == line:
                state[:] = [line, arrival, other_line, other_time, version]
            else:
                state[:] = [line, arrival, latest_line, latest_time, version]
        heapq.heappush(expiry, (arrival + max_distance, version, group))
    return O2Distribution(identity, total, distance_counts), regressions


def build_remote_reuse_result(
    identity: TraceIdentity,
    ordered: Iterator[Tuple[int, ...]],
    requester_names: List[str],
    owner_names: List[str],
    heavy_limit: int,
) -> O6ReuseResult:
    total_reads = 0
    unique_keys = 0
    reused_keys = 0
    repeated_reads = 0
    max_accesses = 0
    frequency: Dict[int, int] = {}
    heavy: List[Tuple[int, Tuple[int, int, int, int, int]]] = []

    current = None
    count = 0

    def finish(key: Optional[Tuple[int, int, int, int, int]], accesses: int) -> None:
        nonlocal total_reads, unique_keys, reused_keys, repeated_reads, max_accesses
        if key is None:
            return
        total_reads += accesses
        unique_keys += 1
        frequency[accesses] = frequency.get(accesses, 0) + 1
        max_accesses = max(max_accesses, accesses)
        if accesses > 1:
            reused_keys += 1
            repeated_reads += accesses - 1
        item = (accesses, key)
        if heavy_limit > 0:
            if len(heavy) < heavy_limit:
                heapq.heappush(heavy, item)
            elif item > heavy[0]:
                heapq.heapreplace(heavy, item)

    for values in ordered:
        key = tuple(values)
        if key == current:
            count += 1
            continue
        finish(current, count)
        current = key
        count = 1
    finish(current, count)

    weighted_rank_sum = 0
    rank = 1
    for accesses, key_count in sorted(frequency.items()):
        last_rank = rank + key_count - 1
        weighted_rank_sum += accesses * (rank + last_rank) * key_count // 2
        rank = last_rank + 1
    gini = 0.0
    if unique_keys and total_reads:
        gini = (
            2.0 * weighted_rank_sum / (unique_keys * total_reads)
            - (unique_keys + 1.0) / unique_keys
        )
    heavy.sort(reverse=True)
    return O6ReuseResult(
        identity,
        total_reads,
        unique_keys,
        reused_keys,
        repeated_reads,
        max_accesses,
        gini,
        frequency,
        heavy,
        list(requester_names),
        list(owner_names),
    )


def top_key_access_share(frequency: Mapping[int, int], fraction: float) -> float:
    unique = sum(frequency.values())
    total = sum(accesses * keys for accesses, keys in frequency.items())
    if not unique or not total:
        return 0.0
    take = max(1, math.ceil(unique * fraction))
    covered = 0
    for accesses, keys in sorted(frequency.items(), reverse=True):
        selected = min(take, keys)
        covered += selected * accesses
        take -= selected
        if take == 0:
            break
    return covered / total


def analyze_l2_utilization(
    files: Sequence[Path], *, strict: bool
) -> Tuple[List[O6L2Aggregate], List[L2Validation]]:
    results: List[O6L2Aggregate] = []
    validations: List[L2Validation] = []
    required = {
        "sequence",
        "time_ps",
        "cache_name",
        "gpu_id",
        "valid_blocks",
        "total_blocks",
        "free_blocks",
        "occupancy_ppm",
        "dirty_blocks",
        "locked_blocks",
        "mshr_entries",
        "status",
    }
    for path in files:
        identity = parse_trace_identity(path)
        aggregate = O6L2Aggregate(identity)
        validation = L2Validation(identity, str(path))
        with open_csv(path) as stream:
            reader = csv.DictReader(stream)
            missing = required - set(reader.fieldnames or [])
            if missing:
                raise AnalysisError(f"{path}: missing L2 columns {sorted(missing)}")
            for line_number, row in enumerate(reader, start=2):
                validation.rows += 1
                if row.get("status") != "complete":
                    validation.invalid_status += 1
                    continue
                try:
                    valid = int_field(row, "valid_blocks")
                    total = int_field(row, "total_blocks")
                    free = int_field(row, "free_blocks")
                    occupancy = int_field(row, "occupancy_ppm")
                    dirty = int_field(row, "dirty_blocks")
                    locked = int_field(row, "locked_blocks")
                    mshr = int_field(row, "mshr_entries")
                except AnalysisError as error:
                    raise AnalysisError(f"{path}:{line_number}: {error}") from error
                expected_occupancy = valid * 1_000_000 // total if total > 0 else -1
                invalid = (
                    total <= 0
                    or valid < 0
                    or valid > total
                    or free != total - valid
                    or occupancy != expected_occupancy
                    or dirty < 0
                    or dirty > valid
                    or locked < 0
                    or locked > valid
                    or mshr < 0
                )
                if invalid:
                    validation.accounting_errors += 1
                    continue
                validation.valid_rows += 1
                aggregate.samples += 1
                aggregate.under50 += int(valid * 2 < total)
                aggregate.occupancy_ppm.add(occupancy)
                aggregate.free_ppm.add(1_000_000 - occupancy)
                aggregate.free_blocks.add(free)
                aggregate.mshr_entries.add(mshr)
        results.append(aggregate)
        validations.append(validation)

    if strict:
        failed = [item for item in validations if not item.passed]
        if failed:
            raise AnalysisError(
                "strict L2-utilization validation failed: "
                + "; ".join(
                    f"{Path(item.source_file).name}: status={item.invalid_status}, "
                    f"accounting={item.accounting_errors}"
                    for item in failed
                )
            )
    return results, validations


def series_stat(series: NumericSeries, fraction: float) -> float:
    return series.percentile(fraction) if series.count else math.nan


def write_o4_o5_o6(
    output_dir: Path,
    o4: Sequence[O4Aggregate],
    exact: Sequence[O5ExactAggregate],
    spatial: Sequence[O2Distribution],
    reuse: Sequence[O6ReuseResult],
    remote_validation: Sequence[RemoteValidation],
    l2: Sequence[O6L2Aggregate],
    l2_validation: Sequence[L2Validation],
    remote_cycle_ps: int,
) -> None:
    with (output_dir / "o4_remote_amplification.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "remote_requests", "read_requests", "write_requests", "other_requests",
                "logical_bytes", "forward_network_bytes", "return_network_bytes",
                "total_network_bytes", "network_bytes_per_logical_byte",
                "manhattan_hops_mean", "manhattan_hops_p50", "manhattan_hops_p95",
                "forward_byte_hops", "return_byte_hops", "total_byte_hops",
                "byte_hops_per_logical_byte", "queue_wait_mean_ps",
                "service_mean_ps", "latency_mean_ps", "latency_p50_ps",
                "latency_p95_ps", "latency_mean_ns", "latency_p50_ns",
                "latency_p95_ns",
            ]
        )
        for item in sorted(o4, key=lambda value: value.identity):
            logical = item.logical_bytes
            mean_latency = item.latency.total_sum / item.latency.count if item.latency.count else 0
            p50_latency = series_stat(item.latency, 0.50)
            p95_latency = series_stat(item.latency, 0.95)
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    item.requests, item.reads, item.writes, item.other_ops,
                    logical, item.forward_bytes, item.return_bytes,
                    item.total_network_bytes,
                    format(item.total_network_bytes / logical if logical else 0, ".12g"),
                    format(item.hops.total_sum / item.hops.count if item.hops.count else 0, ".12g"),
                    format(series_stat(item.hops, 0.50), ".12g"),
                    format(series_stat(item.hops, 0.95), ".12g"),
                    item.forward_byte_hops, item.return_byte_hops, item.total_byte_hops,
                    format(item.total_byte_hops / logical if logical else 0, ".12g"),
                    format(item.queue_wait.total_sum / item.queue_wait.count if item.queue_wait.count else 0, ".12g"),
                    format(item.service.total_sum / item.service.count if item.service.count else 0, ".12g"),
                    format(mean_latency, ".12g"), format(p50_latency, ".12g"),
                    format(p95_latency, ".12g"), format(mean_latency / 1000, ".12g"),
                    format(p50_latency / 1000, ".12g"),
                    format(p95_latency / 1000, ".12g"),
                ]
            )

    with (output_dir / "o5_exact_inflight_dedup.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "analysis_scope", "read_requests", "read_logical_bytes",
                "read_network_bytes", "exact_inflight_dedup_requests",
                "exact_inflight_dedup_fraction", "deduplicable_logical_bytes",
                "deduplicable_network_bytes", "inflight_predecessor_sum",
            ]
        )
        for item in sorted(exact, key=lambda value: value.identity):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    "reads", item.read_requests, item.read_logical_bytes,
                    item.read_network_bytes, item.exact_requests,
                    format(item.exact_requests / item.read_requests if item.read_requests else 0, ".12g"),
                    item.exact_logical_bytes, item.exact_network_bytes,
                    item.inflight_predecessors,
                ]
            )

    spatial_rows = compute_o2(spatial, DEFAULT_O2_WINDOWS_CYCLES, remote_cycle_ps)
    with (output_dir / "o5_remote_page_spatial_cdf.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "relation", "direction", "window_remote_cycles", "window_ps",
                "window_ns", "count", "total_non_exact_read_requests",
                "matched_spatial_prior_within_max_window",
                "cdf_fraction_all_requests", "cdf_fraction_conditional_on_match",
            ]
        )
        for row in spatial_rows:
            row = list(row)
            row[5] = "same_requester_owner_4KB_page_different_64B_line"
            writer.writerow(row[:12] + [row[13], row[14], row[15]])

    with (output_dir / "o6_remote_reuse_summary.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "key", "read_requests", "unique_keys", "single_touch_keys",
                "reused_keys", "repeated_reads", "repeated_read_fraction",
                "reused_key_fraction", "max_accesses_per_key", "gini",
                "top_1pct_key_access_share", "top_5pct_key_access_share",
                "top_10pct_key_access_share",
            ]
        )
        for item in sorted(reuse, key=lambda value: value.identity):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    "pid,requester,owner,line,write_epoch", item.total_reads,
                    item.unique_keys, item.unique_keys - item.reused_keys,
                    item.reused_keys, item.repeated_reads,
                    format(item.repeated_reads / item.total_reads if item.total_reads else 0, ".12g"),
                    format(item.reused_keys / item.unique_keys if item.unique_keys else 0, ".12g"),
                    item.max_accesses, format(item.gini, ".12g"),
                    format(top_key_access_share(item.frequency, 0.01), ".12g"),
                    format(top_key_access_share(item.frequency, 0.05), ".12g"),
                    format(top_key_access_share(item.frequency, 0.10), ".12g"),
                ]
            )

    with (output_dir / "o6_remote_reuse_frequency.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + ["accesses_per_key", "key_count", "key_fraction", "access_fraction"]
        )
        for item in sorted(reuse, key=lambda value: value.identity):
            for accesses, key_count in sorted(item.frequency.items()):
                writer.writerow(
                    _identity_fields(item.identity)
                    + [
                        accesses, key_count,
                        format(key_count / item.unique_keys if item.unique_keys else 0, ".12g"),
                        format(accesses * key_count / item.total_reads if item.total_reads else 0, ".12g"),
                    ]
                )

    with (output_dir / "o6_remote_reuse_heavy_hitters.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "rank", "pid", "requester", "owner", "line_address",
                "line_address_hex", "write_epoch", "read_accesses", "reuse_count",
                "access_share", "cumulative_access_share",
            ]
        )
        for item in sorted(reuse, key=lambda value: value.identity):
            cumulative = 0
            for rank, (accesses, key) in enumerate(item.heavy, start=1):
                pid, requester, owner, line, epoch = key
                cumulative += accesses
                writer.writerow(
                    _identity_fields(item.identity)
                    + [
                        rank, pid, item.requester_names[requester], item.owner_names[owner],
                        line, hex(line), epoch, accesses, accesses - 1,
                        format(accesses / item.total_reads if item.total_reads else 0, ".12g"),
                        format(cumulative / item.total_reads if item.total_reads else 0, ".12g"),
                    ]
                )

    with (output_dir / "o6_l2_headroom.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "samples", "occupancy_mean", "occupancy_p50", "occupancy_p95",
                "free_fraction_mean", "free_fraction_p50", "free_fraction_p95",
                "free_blocks_mean", "free_blocks_p50", "free_blocks_p95",
                "under_50pct_samples", "under_50pct_fraction", "mshr_mean",
                "mshr_p50", "mshr_p95",
            ]
        )
        for item in sorted(l2, key=lambda value: value.identity):
            samples = item.samples
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    samples,
                    format(item.occupancy_ppm.total_sum / samples / 1e6 if samples else 0, ".12g"),
                    format(series_stat(item.occupancy_ppm, 0.50) / 1e6 if samples else 0, ".12g"),
                    format(series_stat(item.occupancy_ppm, 0.95) / 1e6 if samples else 0, ".12g"),
                    format(item.free_ppm.total_sum / samples / 1e6 if samples else 0, ".12g"),
                    format(series_stat(item.free_ppm, 0.50) / 1e6 if samples else 0, ".12g"),
                    format(series_stat(item.free_ppm, 0.95) / 1e6 if samples else 0, ".12g"),
                    format(item.free_blocks.total_sum / samples if samples else 0, ".12g"),
                    format(series_stat(item.free_blocks, 0.50), ".12g"),
                    format(series_stat(item.free_blocks, 0.95), ".12g"),
                    item.under50,
                    format(item.under50 / samples if samples else 0, ".12g"),
                    format(item.mshr_entries.total_sum / samples if samples else 0, ".12g"),
                    format(series_stat(item.mshr_entries, 0.50), ".12g"),
                    format(series_stat(item.mshr_entries, 0.95), ".12g"),
                ]
            )

    with (output_dir / "o4_o5_o6_validation.csv").open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            IDENTITY_HEADER
            + [
                "stream", "source_file", "rows", "valid_rows", "incomplete_or_status",
                "malformed_rows", "identity_errors", "accounting_errors",
                "time_regressions", "sequence_regressions", "strict_pass",
            ]
        )
        for item in sorted(remote_validation, key=lambda value: value.identity):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    "remote_requests", item.source_file, item.rows, item.completed,
                    item.incomplete, item.malformed_rows, item.identity_errors,
                    item.accounting_errors, item.time_regressions,
                    item.sequence_regressions, str(item.passed).lower(),
                ]
            )
        for item in sorted(l2_validation, key=lambda value: value.identity):
            writer.writerow(
                _identity_fields(item.identity)
                + [
                    "l2_utilization", item.source_file, item.rows, item.valid_rows,
                    item.invalid_status, 0, 0, item.accounting_errors, 0, 0,
                    str(item.passed).lower(),
                ]
            )


def analyze(
    inputs: Sequence[str],
    output_dir: Path,
    *,
    strict: bool = True,
    o2_event: str = "l2_lookup_result",
    o2_include_remote: bool = False,
    o2_windows_cycles: Sequence[int] = DEFAULT_O2_WINDOWS_CYCLES,
    o2_cycle_ps: int = 1000,
    remote_cycle_ps: int = 1000,
    heavy_hitters: int = 100,
) -> Dict[str, int]:
    if o2_cycle_ps <= 0:
        raise AnalysisError("--o2-cycle-ps must be positive")
    if remote_cycle_ps <= 0:
        raise AnalysisError("--remote-cycle-ps must be positive")
    if heavy_hitters < 0:
        raise AnalysisError("--heavy-hitters must be non-negative")
    (
        path_files,
        dram_files,
        remote_files,
        l2_util_files,
        path_validation_files,
        remote_validation_files,
    ) = discover_inputs(inputs)
    output_dir.mkdir(parents=True, exist_ok=True)

    emitter_validations = read_emitter_validations(
        path_validation_files, remote_validation_files
    )
    add_missing_emitter_validation_checks(
        emitter_validations,
        path_files,
        remote_files,
        path_validation_files,
        remote_validation_files,
    )
    write_emitter_validations(output_dir, emitter_validations)
    if strict:
        enforce_emitter_validations(emitter_validations)

    o1, validations, distributions, diagnostics = analyze_paths(
        path_files,
        o2_event=o2_event,
        o2_include_remote=o2_include_remote,
        strict=strict,
    ) if path_files else ({}, [], [], [])
    write_o1(output_dir, o1, validations)
    o2_rows = compute_o2(distributions, o2_windows_cycles, o2_cycle_ps)
    write_o2(output_dir, o2_rows, diagnostics, o2_event)

    o3_rows = analyze_o3(dram_files)
    write_o3(output_dir, o3_rows)

    o4, exact, spatial, reuse, remote_validation = analyze_remote_files(
        remote_files,
        strict=strict,
        remote_cycle_ps=remote_cycle_ps,
        heavy_hitters=heavy_hitters,
    )
    l2, l2_validation = analyze_l2_utilization(l2_util_files, strict=strict)
    write_o4_o5_o6(
        output_dir,
        o4,
        exact,
        spatial,
        reuse,
        remote_validation,
        l2,
        l2_validation,
        remote_cycle_ps,
    )
    return {
        "path_files": len(path_files),
        "dram_files": len(dram_files),
        "demand_read_groups": len(o1),
        "o2_accesses": sum(item.total for item in distributions),
        "o3_cells": len(o3_rows),
        "remote_files": len(remote_files),
        "remote_requests": sum(item.requests for item in o4),
        "l2_util_files": len(l2_util_files),
        "l2_samples": sum(item.samples for item in l2),
        "path_validation_files": len(path_validation_files),
        "remote_validation_files": len(remote_validation_files),
        "emitter_validation_checks": len(emitter_validations),
    }


def _write_self_test_path(path: Path) -> None:
    header = [
        "schema_version",
        "path_id",
        "l1_role",
        "l1_cache",
        "address",
        "operation",
        "status",
        "start_ps",
        "end_ps",
        "total_ps",
        "accounted_ps",
        "residual_ps",
        "route",
        "remote",
        "source",
        "l1_result",
        "l2_result",
        "events",
        "duplicate_events",
        "time_regressions",
        "l1_lookup_ps",
        "dram_queue_service_ps",
        "unattributed_ps",
    ]
    rows = []
    for index, (address, event_ps) in enumerate(((0x1000, 1000), (0x1040, 3000))):
        rows.append(
            [
                "observation-path-v1",
                f"path-{index}",
                "leader",
                "GPU[0].L1V[0]",
                str(address),
                "read",
                "complete",
                "0",
                "10000",
                "10000",
                "10000",
                "0",
                "local",
                "false",
                "dram",
                "read-miss",
                "read-miss",
                f"path_start@0;l2_lookup_result@{event_ps};path_complete@10000",
                "0",
                "0",
                "2000",
                "8000",
                "0",
            ]
        )
    with gzip.open(path, "wt", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(header)
        writer.writerows(rows)


def _write_self_test_dram(path: Path) -> None:
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(
            [
                "relation",
                "distance",
                "direction",
                "window",
                "count",
                "total",
                "fraction",
                "matched_within_max_window",
            ]
        )
        writer.writerow(
            ["same_access_unit", "cycles", "nearest_prior", 2, 1, 2, 0.5, 1]
        )
        writer.writerow(
            [
                "same_row_different_column",
                "cycles",
                "nearest_prior",
                2,
                1,
                2,
                0.5,
                1,
            ]
        )


def _write_self_test_remote(path: Path) -> None:
    header = [
        "sequence", "status", "logical_request_id", "pid", "operation",
        "address", "line_address", "bytes", "requester_name", "owner_name",
        "requester_gpu", "owner_gpu", "manhattan_hops", "arrival_ps",
        "issue_ps", "completion_ps", "queue_wait_ps", "service_ps", "total_ps",
        "write_epoch", "same_line_inflight_at_arrival", "forward_wire_id",
        "forward_traffic_bytes", "return_wire_id", "return_traffic_bytes",
        "total_traffic_bytes", "time_regression",
    ]

    def row(sequence: int, line: int, arrival: int, inflight: int) -> List[object]:
        issue = arrival + 1000
        completion = arrival + 9000
        return [
            sequence, "complete", f"remote-{sequence}", 7, "read", line, line, 64,
            "GPU[0].RDMA", "GPU[1].RDMA", 0, 1, 1, arrival, issue, completion,
            1000, 8000, 9000, 0, inflight, f"forward-{sequence}", 16,
            f"return-{sequence}", 80, 96, "false",
        ]

    # Completion-order output is intentionally not arrival-order output.
    rows = [
        row(3, 0x4040, 3000, 0),
        row(1, 0x4000, 1000, 0),
        row(2, 0x4000, 1500, 1),
        row(4, 0x4000, 5000, 0),
    ]
    with gzip.open(path, "wt", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(header)
        writer.writerows(rows)


def _write_self_test_l2(path: Path) -> None:
    header = [
        "sequence", "time_ps", "cache_name", "gpu_id", "valid_blocks",
        "total_blocks", "free_blocks", "occupancy_ppm", "dirty_blocks",
        "locked_blocks", "mshr_entries", "status",
    ]
    rows = [
        [1, 1000, "GPU[0].L2[0]", 0, 25, 100, 75, 250000, 0, 0, 1, "complete"],
        [2, 2000, "GPU[0].L2[0]", 0, 50, 100, 50, 500000, 0, 0, 2, "complete"],
        [3, 3000, "GPU[0].L2[0]", 0, 75, 100, 25, 750000, 0, 0, 3, "complete"],
    ]
    with gzip.open(path, "wt", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(header)
        writer.writerows(rows)


def _write_self_test_path_validation(path: Path, *, fail: bool = False) -> None:
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["invariant", "value", "pass"])
        writer.writerow(["accounting_mismatches", 1 if fail else 0, not fail])
        writer.writerow(["started_equals_terminal", 2, True])


def _write_self_test_remote_validation(path: Path, *, fail: bool = False) -> None:
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["metric", "value", "status"])
        writer.writerow(["time_regressions", 1 if fail else 0, "error" if fail else "ok"])
        writer.writerow(["max_active_requests", 2, "info"])


def self_test() -> None:
    with tempfile.TemporaryDirectory(prefix="observation-analysis-test-") as directory:
        root = Path(directory) / "2026-07-12-observation"
        root.mkdir()
        prefix = root / "baseline_relu_baseline"
        _write_self_test_path(Path(str(prefix) + PATH_SUFFIX))
        _write_self_test_dram(Path(str(prefix) + DRAM_SUFFIX))
        _write_self_test_remote(Path(str(prefix) + REMOTE_SUFFIX))
        _write_self_test_l2(Path(str(prefix) + L2_UTIL_SUFFIX))
        _write_self_test_path_validation(
            Path(str(prefix) + PATH_VALIDATION_SUFFIX)
        )
        _write_self_test_remote_validation(
            Path(str(prefix) + REMOTE_VALIDATION_SUFFIX)
        )
        output = root / "analysis"
        counts = analyze([str(root)], output)
        if (
            counts["path_files"] != 1
            or counts["dram_files"] != 1
            or counts["remote_files"] != 1
            or counts["l2_util_files"] != 1
            or counts["path_validation_files"] != 1
            or counts["remote_validation_files"] != 1
        ):
            raise AssertionError(counts)

        with (output / "o1_exclusive_stage_breakdown.csv").open(newline="") as stream:
            o1_rows = list(csv.DictReader(stream))
        total_rows = [
            row for row in o1_rows if row["route"] == "all" and row["stage"] == "total"
        ]
        assert len(total_rows) == 1 and total_rows[0]["mean_ps"] == "10000", total_rows

        with (output / "o2_adjacent_line_short_window_cdf.csv").open(
            newline=""
        ) as stream:
            o2_rows = list(csv.DictReader(stream))
        at_two = [row for row in o2_rows if row["window_l1_cycles"] == "2"]
        assert len(at_two) == 1 and at_two[0]["count"] == "1", at_two
        assert at_two[0]["total_l2_miss_reads"] == "2", at_two

        with (output / "o3_physical_locality_cdf.csv").open(newline="") as stream:
            o3_rows = list(csv.DictReader(stream))
        assert len(o3_rows) == 2 and o3_rows[0]["cdf_fraction"] == "0.5", o3_rows
        with (output / "o3_physical_locality_heatmap_long.csv").open(
            newline=""
        ) as stream:
            heatmap_rows = list(csv.DictReader(stream))
        assert len(heatmap_rows) == 2, heatmap_rows
        assert heatmap_rows[0]["heatmap_x_distance"] == "cycles", heatmap_rows[0]
        assert heatmap_rows[0]["heatmap_x_window"] == "2", heatmap_rows[0]
        assert heatmap_rows[0]["value_cdf_fraction"] == "0.5", heatmap_rows[0]

        with (output / "o4_remote_amplification.csv").open(newline="") as stream:
            o4_rows = list(csv.DictReader(stream))
        assert len(o4_rows) == 1, o4_rows
        assert o4_rows[0]["logical_bytes"] == "256", o4_rows[0]
        assert o4_rows[0]["total_network_bytes"] == "384", o4_rows[0]
        assert o4_rows[0]["network_bytes_per_logical_byte"] == "1.5", o4_rows[0]
        assert o4_rows[0]["latency_mean_ps"] == "9000", o4_rows[0]

        with (output / "o5_exact_inflight_dedup.csv").open(newline="") as stream:
            exact_rows = list(csv.DictReader(stream))
        assert exact_rows[0]["exact_inflight_dedup_requests"] == "1", exact_rows
        with (output / "o5_remote_page_spatial_cdf.csv").open(newline="") as stream:
            spatial_rows = list(csv.DictReader(stream))
        spatial_at_two = [
            row for row in spatial_rows if row["window_remote_cycles"] == "2"
        ]
        assert len(spatial_at_two) == 1, spatial_at_two
        assert spatial_at_two[0]["count"] == "2", spatial_at_two[0]
        assert spatial_at_two[0]["total_non_exact_read_requests"] == "3", (
            spatial_at_two[0]
        )

        with (output / "o6_remote_reuse_summary.csv").open(newline="") as stream:
            reuse_rows = list(csv.DictReader(stream))
        assert reuse_rows[0]["read_requests"] == "4", reuse_rows
        assert reuse_rows[0]["unique_keys"] == "2", reuse_rows
        assert reuse_rows[0]["max_accesses_per_key"] == "3", reuse_rows
        with (output / "o6_remote_reuse_heavy_hitters.csv").open(
            newline=""
        ) as stream:
            heavy_rows = list(csv.DictReader(stream))
        assert heavy_rows[0]["read_accesses"] == "3", heavy_rows
        with (output / "o6_l2_headroom.csv").open(newline="") as stream:
            l2_rows = list(csv.DictReader(stream))
        assert l2_rows[0]["occupancy_mean"] == "0.5", l2_rows
        assert l2_rows[0]["under_50pct_samples"] == "1", l2_rows

        with (output / "emitter_instrumentation_validation.csv").open(
            newline=""
        ) as stream:
            emitter_rows = list(csv.DictReader(stream))
        assert len(emitter_rows) == 4, emitter_rows
        assert all(row["strict_pass"] == "true" for row in emitter_rows), (
            emitter_rows
        )

        # A manually selected output prefix need not contain the standard
        # `_observation_` filename marker. Direct files are schema-classified,
        # while a directory scan must continue to ignore those relaxed names.
        custom_prefix = root / "observation_relu_l2fixed"
        custom_path = Path(str(custom_prefix) + RELAXED_PATH_SUFFIX)
        custom_dram = Path(str(custom_prefix) + RELAXED_DRAM_SUFFIX)
        custom_remote = Path(str(custom_prefix) + RELAXED_REMOTE_SUFFIX)
        custom_l2 = Path(str(custom_prefix) + RELAXED_L2_UTIL_SUFFIX)
        custom_path_validation = Path(
            str(custom_prefix) + RELAXED_PATH_VALIDATION_SUFFIX
        )
        custom_remote_validation = Path(
            str(custom_prefix) + RELAXED_REMOTE_VALIDATION_SUFFIX
        )
        _write_self_test_path(custom_path)
        _write_self_test_dram(custom_dram)
        _write_self_test_remote(custom_remote)
        _write_self_test_l2(custom_l2)
        _write_self_test_path_validation(custom_path_validation)
        _write_self_test_remote_validation(custom_remote_validation)
        (
            directory_paths,
            directory_dram,
            directory_remote,
            directory_l2,
            directory_path_validation,
            directory_remote_validation,
        ) = discover_inputs([str(root)])
        assert directory_paths == [Path(str(prefix) + PATH_SUFFIX)], directory_paths
        assert directory_dram == [Path(str(prefix) + DRAM_SUFFIX)], directory_dram
        assert directory_remote == [Path(str(prefix) + REMOTE_SUFFIX)], directory_remote
        assert directory_l2 == [Path(str(prefix) + L2_UTIL_SUFFIX)], directory_l2
        assert directory_path_validation == [
            Path(str(prefix) + PATH_VALIDATION_SUFFIX)
        ], directory_path_validation
        assert directory_remote_validation == [
            Path(str(prefix) + REMOTE_VALIDATION_SUFFIX)
        ], directory_remote_validation

        custom_output = root / "custom-analysis"
        custom_counts = analyze(
            [
                str(custom_path),
                str(custom_dram),
                str(custom_remote),
                str(custom_l2),
                str(custom_path_validation),
                str(custom_remote_validation),
            ],
            custom_output,
        )
        assert custom_counts["path_files"] == 1, custom_counts
        assert custom_counts["dram_files"] == 1, custom_counts
        assert custom_counts["remote_files"] == 1, custom_counts
        assert custom_counts["l2_util_files"] == 1, custom_counts
        assert custom_counts["path_validation_files"] == 1, custom_counts
        assert custom_counts["remote_validation_files"] == 1, custom_counts
        with (custom_output / "o1_validation.csv").open(newline="") as stream:
            custom_rows = list(csv.DictReader(stream))
        assert len(custom_rows) == 1, custom_rows
        assert custom_rows[0]["trace_stem"] == "observation_relu_l2fixed", (
            custom_rows
        )

        missing_validation_output = root / "missing-validation-analysis"
        try:
            analyze([str(custom_path)], missing_validation_output)
        except AnalysisError as error:
            assert "validation_file_present" in str(error), error
        else:
            raise AssertionError("strict mode accepted a path trace without emitter validation")

        # Strict mode must honor the emitter's own validation status for both
        # path and remote instrumentation. Relaxed mode still writes the
        # failing rows so a debugging run cannot hide them.
        failing_path = root / "custom_path_validation.csv"
        _write_self_test_path_validation(failing_path, fail=True)
        failing_path_output = root / "failing-path-analysis"
        try:
            analyze([str(failing_path)], failing_path_output)
        except AnalysisError as error:
            assert "emitter/instrumentation validation failed" in str(error), error
        else:
            raise AssertionError("strict mode accepted path pass=false")
        analyze([str(failing_path)], failing_path_output, strict=False)
        with (failing_path_output / "emitter_instrumentation_validation.csv").open(
            newline=""
        ) as stream:
            failing_rows = list(csv.DictReader(stream))
        assert any(row["strict_pass"] == "false" for row in failing_rows), failing_rows

        failing_remote = root / "custom_remote_validation.csv"
        _write_self_test_remote_validation(failing_remote, fail=True)
        try:
            analyze([str(failing_remote)], root / "failing-remote-analysis")
        except AnalysisError as error:
            assert "emitter/instrumentation validation failed" in str(error), error
        else:
            raise AssertionError("strict mode accepted remote status=error")

        # A broad glob can include online summaries; only exact validation
        # schemas are admitted, so summaries are never treated as validators.
        summary = root / "baseline_relu_baseline_observation_summary.csv"
        with summary.open("w", newline="") as stream:
            writer = csv.writer(stream)
            writer.writerow(["kind", "name", "accesses", "total_ps", "average_ps"])
            writer.writerow(["stage", "l1_lookup", 2, 4000, 2000])
        glob_inputs = discover_inputs([str(root / "*.csv")])
        assert summary not in set().union(*(set(items) for items in glob_inputs))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Analyze the independent observation traces for O1--O6."
    )
    parser.add_argument(
        "inputs",
        nargs="*",
        help="result directory, observation file, or quoted glob (recursive directories supported)",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path("observation_analysis"),
        help="output directory (default: ./observation_analysis)",
    )
    parser.add_argument(
        "--no-strict",
        action="store_true",
        help="write diagnostics despite accounting errors or missing selected O2 events",
    )
    parser.add_argument(
        "--o2-event",
        default="l2_lookup_result",
        help="serialized boundary used as O2 arrival time (default: l2_lookup_result)",
    )
    parser.add_argument(
        "--o2-include-remote",
        action="store_true",
        help="include remote L2 misses in O2 (default studies local DRAM-bound misses only)",
    )
    parser.add_argument(
        "--o2-windows-cycles",
        type=parse_cycle_windows,
        default=DEFAULT_O2_WINDOWS_CYCLES,
        help="CDF windows in L1 cycles (default: 0,1,2,4,8,16,32,64)",
    )
    parser.add_argument(
        "--o2-cycle-ps",
        type=int,
        default=1000,
        help="L1 cycle length in ps (default 1000, matching the 1GHz baseline)",
    )
    parser.add_argument(
        "--remote-cycle-ps",
        type=int,
        default=1000,
        help="Cycle length for O5 remote short-window CDFs (default 1000 ps).",
    )
    parser.add_argument(
        "--remote-heavy-hitters",
        type=int,
        default=100,
        help="Maximum O6 remote reuse heavy hitters per trace (default 100).",
    )
    parser.add_argument(
        "--self-test", action="store_true", help="run an embedded synthetic regression test"
    )
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.self_test:
            self_test()
            print("observation analysis self-test: PASS")
            return 0
        if not args.inputs:
            raise AnalysisError("provide at least one result directory, file, or glob")
        counts = analyze(
            args.inputs,
            args.output_dir,
            strict=not args.no_strict,
            o2_event=args.o2_event,
            o2_include_remote=args.o2_include_remote,
            o2_windows_cycles=args.o2_windows_cycles,
            o2_cycle_ps=args.o2_cycle_ps,
            remote_cycle_ps=args.remote_cycle_ps,
            heavy_hitters=args.remote_heavy_hitters,
        )
    except (AnalysisError, OSError, csv.Error) as error:
        print(f"error: {error}", file=sys.stderr)
        return 2
    print(
        "observation analysis complete: "
        + ", ".join(f"{name}={value}" for name, value in counts.items())
        + f", output={args.output_dir}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
