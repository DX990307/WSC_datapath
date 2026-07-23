#!/usr/bin/env python3
"""Inventory akkalat experiment results before starting a new campaign.

The script is intentionally read-only with respect to experiment directories.
It writes two reports in ``akkalat/results``:

* RESULTS_MANIFEST.csv: machine-readable per-directory inventory
* RESULTS_CLEANUP.md: protected data, cleanup recommendations, and space budget
"""

from __future__ import annotations

import csv
import json
import os
import re
import shutil
from collections import Counter
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path


AKKALAT_DIR = Path(__file__).resolve().parent
REPO_ROOT = AKKALAT_DIR.parent
RESULTS_DIR = AKKALAT_DIR / "results"
MANIFEST_PATH = RESULTS_DIR / "RESULTS_MANIFEST.csv"
CLEANUP_PATH = RESULTS_DIR / "RESULTS_CLEANUP.md"

PAPER_BENCHMARKS = (
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fir",
    "fft",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
)

RESULT_REF_RE = re.compile(r"akkalat/results/([^\s/'\"}]+)")
MSHR_RE = re.compile(r"-l1v-mshr-entries(?:=|\s+)(\d+)")
RETURN_RE = re.compile(r"Return code:\s*(-?\d+)")
BENCHMARK_RE = re.compile(r"-benchmark(?:=|\s+)([^\s]+)")


@dataclass
class ResultRecord:
    name: str
    full_path: str
    created_utc: str
    modified_utc: str
    size_bytes: int
    file_count: int
    stdout_count: int
    metrics_count: int
    successful_stdout: int
    failed_stdout: int
    unfinished_stdout: int
    benchmark_count: int
    benchmarks: str
    configs: str
    l1v_mshr_entries: str
    l2_slices_per_gpm: str
    git_revision: str
    completion_status: str
    has_final_csv_or_summary: bool
    paper_referenced: bool
    plot_referenced: bool
    can_regenerate: str
    classification: str
    notes: str


def human_bytes(value: int) -> str:
    units = ("B", "KiB", "MiB", "GiB", "TiB")
    size = float(value)
    for unit in units:
        if size < 1024.0 or unit == units[-1]:
            return f"{size:.1f} {unit}"
        size /= 1024.0
    raise AssertionError("unreachable")


def read_edges(path: Path, edge_bytes: int = 65536) -> str:
    """Read bounded head and tail text without loading multi-MiB logs."""
    try:
        size = path.stat().st_size
        with path.open("rb") as handle:
            head = handle.read(edge_bytes)
            if size > edge_bytes:
                handle.seek(max(0, size - edge_bytes))
                tail = handle.read(edge_bytes)
            else:
                tail = b""
        return (head + b"\n" + tail).decode("utf-8", errors="replace")
    except OSError:
        return ""


def discover_paper_references() -> dict[str, list[str]]:
    refs: dict[str, list[str]] = {}
    roots = (REPO_ROOT / "weeklyreport", AKKALAT_DIR)
    allowed_suffixes = {".tex", ".py", ".md"}
    for root in roots:
        if not root.exists():
            continue
        for path in root.rglob("*"):
            if not path.is_file() or path.suffix not in allowed_suffixes:
                continue
            if RESULTS_DIR in path.parents:
                continue
            try:
                text = path.read_text(encoding="utf-8", errors="replace")
            except OSError:
                continue
            for match in RESULT_REF_RE.finditer(text):
                name = match.group(1).rstrip(".,;:)")
                refs.setdefault(name, []).append(str(path.relative_to(REPO_ROOT)))
    return refs


def infer_config_from_stdout_name(name: str) -> str:
    stem = name.removesuffix("_out.stdout")
    for benchmark in sorted(PAPER_BENCHMARKS, key=len, reverse=True):
        marker = f"_{benchmark}_"
        if marker in stem:
            return stem.split(marker, 1)[1]
    return "unknown"


def inspect_result_dir(path: Path, paper_refs: dict[str, list[str]]) -> ResultRecord:
    size_bytes = 0
    files: list[Path] = []
    for root, _, names in os.walk(path):
        root_path = Path(root)
        for name in names:
            file_path = root_path / name
            files.append(file_path)
            try:
                size_bytes += file_path.stat().st_size
            except OSError:
                pass

    stdout_files = [p for p in files if p.name.endswith("_out.stdout")]
    metrics_files = [p for p in files if p.name.endswith("_metrics.csv")]
    successful = 0
    failed = 0
    unfinished = 0
    benchmarks: set[str] = set()
    configs: set[str] = set()
    mshr_values: set[int] = set()
    saw_command_without_mshr = False
    failure_markers: Counter[str] = Counter()
    metadata_path = path / "EXPERIMENT_METADATA.json"
    binary_metadata_path = path / "EXPERIMENT_BINARIES.json"
    metadata: dict = {}
    if metadata_path.exists():
        try:
            loaded = json.loads(metadata_path.read_text(encoding="utf-8"))
            if isinstance(loaded, dict):
                metadata = loaded
        except (OSError, ValueError):
            failure_markers["invalid experiment metadata"] += 1

    metadata_experiments = metadata.get("experiments", [])
    if not isinstance(metadata_experiments, list):
        metadata_experiments = []
    for experiment in metadata_experiments:
        if not isinstance(experiment, dict):
            continue
        benchmark = experiment.get("benchmark")
        config = experiment.get("configuration")
        if benchmark:
            benchmarks.add(str(benchmark))
        if config:
            configs.add(str(config))
        command = experiment.get("command", [])
        if isinstance(command, list):
            command_text = " ".join(str(part) for part in command)
            mshr_values.update(int(v) for v in MSHR_RE.findall(command_text))

    for stdout in stdout_files:
        text = read_edges(stdout)
        benchmark_match = BENCHMARK_RE.search(text)
        if benchmark_match:
            benchmarks.add(benchmark_match.group(1))
            if not MSHR_RE.search(text):
                saw_command_without_mshr = True
        mshr_values.update(int(v) for v in MSHR_RE.findall(text))
        configs.add(infer_config_from_stdout_name(stdout.name))

        returns = RETURN_RE.findall(text)
        if returns:
            if int(returns[-1]) == 0:
                successful += 1
            else:
                failed += 1
                failure_markers["nonzero return"] += 1
        else:
            unfinished += 1
            lowered = text.lower()
            for marker in ("panic", "fatal", "timed out", "timeout", "killed"):
                if marker in lowered:
                    failure_markers[marker] += 1

    for document_name in ("README.md", "EXPERIMENT_MANIFEST.md"):
        document = path / document_name
        if not document.exists():
            continue
        text = read_edges(document)
        mshr_values.update(int(v) for v in MSHR_RE.findall(text))

    if mshr_values:
        mshr_description = ";".join(str(v) for v in sorted(mshr_values))
    elif saw_command_without_mshr:
        # All historical commands were emitted while the simulator default was
        # 160. New formal runs must always print the explicit value 16.
        mshr_description = "160 (implicit historical default)"
    else:
        mshr_description = "unknown"

    l2_slice_values: set[int] = set()
    for metrics in metrics_files[:1]:
        try:
            with metrics.open(newline="", encoding="utf-8") as stream:
                for row in csv.DictReader(stream, skipinitialspace=True):
                    if row.get("what", "").strip() == "config_l2_slices_per_gpm":
                        l2_slice_values.add(int(float(row["value"])))
        except (OSError, ValueError, KeyError):
            pass
    l2_slices = ";".join(str(value) for value in sorted(l2_slice_values)) or "unknown"

    references = paper_refs.get(path.name, [])
    paper_referenced = any(reference.endswith(".tex") for reference in references)
    plot_referenced = any(reference.endswith(".py") for reference in references)
    notes: list[str] = []
    if references:
        notes.append("referenced by " + "; ".join(sorted(set(references))))
    if failure_markers:
        notes.append(", ".join(f"{key}={value}" for key, value in failure_markers.items()))
    if not files:
        notes.append("empty directory")

    if not files:
        completion_status = "empty"
    elif failed:
        completion_status = "failed"
    elif unfinished:
        completion_status = "incomplete"
    else:
        completion_status = "complete"

    has_final = any(
        p.suffix == ".csv" and (
            "summary" in p.name.lower() or "speedup" in p.name.lower()
        )
        for p in files
    ) or any(p.suffix == ".md" and "summary" in p.name.lower() for p in files)
    dirty_source = bool(str(metadata.get("git_status_porcelain", "")).strip())
    if metadata_experiments and binary_metadata_path.exists() and not dirty_source:
        can_regenerate = "yes (clean revision, commands, and binary hashes recorded)"
    elif metadata_experiments and binary_metadata_path.exists():
        can_regenerate = "partial (commands/hash recorded; source worktree was dirty)"
    elif metadata_experiments:
        can_regenerate = "partial (commands recorded)"
    else:
        can_regenerate = "unknown (no experiment metadata)"

    if paper_referenced or plot_referenced:
        classification = "paper-active"
    elif not files:
        classification = "disposable"
    elif failed or unfinished:
        classification = "incomplete"
    elif mshr_description != "16":
        classification = "legacy"
    else:
        classification = "reference"

    stat = path.stat()
    created = metadata.get("created_at", "")
    if not created:
        created = datetime.fromtimestamp(stat.st_mtime, timezone.utc).isoformat()

    return ResultRecord(
        name=path.name,
        full_path=str(path.resolve()),
        created_utc=str(created),
        modified_utc=datetime.fromtimestamp(stat.st_mtime, timezone.utc).isoformat(),
        size_bytes=size_bytes,
        file_count=len(files),
        stdout_count=len(stdout_files),
        metrics_count=len(metrics_files),
        successful_stdout=successful,
        failed_stdout=failed,
        unfinished_stdout=unfinished,
        benchmark_count=len(benchmarks),
        benchmarks=";".join(sorted(benchmarks)),
        configs=";".join(sorted(configs)),
        l1v_mshr_entries=mshr_description,
        l2_slices_per_gpm=l2_slices,
        git_revision=str(metadata.get("git_revision", "unknown")),
        completion_status=completion_status,
        has_final_csv_or_summary=has_final,
        paper_referenced=paper_referenced,
        plot_referenced=plot_referenced,
        can_regenerate=can_regenerate,
        classification=classification,
        notes=" | ".join(notes),
    )


def write_manifest(records: list[ResultRecord]) -> None:
    fields = list(ResultRecord.__dataclass_fields__)
    with MANIFEST_PATH.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=fields)
        writer.writeheader()
        for record in records:
            writer.writerow(record.__dict__)


def write_cleanup_report(
    records: list[ResultRecord], paper_refs: dict[str, list[str]]
) -> None:
    usage = shutil.disk_usage(REPO_ROOT)
    total_results = sum(record.size_bytes for record in records)
    largest = max((record.size_bytes for record in records), default=0)
    measured_cells = sum(record.metrics_count for record in records)
    measured_bytes = sum(record.size_bytes for record in records if record.metrics_count)
    average_cell = measured_bytes // measured_cells if measured_cells else largest
    planned_cells = len(PAPER_BENCHMARKS) * 5
    planned_budget = average_cell * planned_cells
    required_with_margin = planned_budget * 3 // 2
    disposable_bytes = sum(
        r.size_bytes for r in records if r.classification == "disposable"
    )
    incomplete_bytes = sum(
        r.size_bytes for r in records if r.classification == "incomplete"
    )
    class_counts = Counter(record.classification for record in records)
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")

    previous_free = human_bytes(usage.free)
    if CLEANUP_PATH.exists():
        previous = CLEANUP_PATH.read_text(encoding="utf-8", errors="replace")
        match = re.search(r"Free before cleanup: ([^\n]+)", previous)
        if match:
            previous_free = match.group(1).strip()

    lines = [
        "# Results inventory and cleanup plan",
        "",
        f"Generated: {now}",
        "",
        "## Decision",
        "",
        "No existing result directory is deleted automatically. The available "
        "space exceeds the measured new-campaign estimate with a 50% margin, so preserving "
        "old evidence is safer than destructive cleanup.",
        "",
        "## Space budget",
        "",
        f"- Filesystem capacity: {human_bytes(usage.total)}",
        f"- Free before cleanup: {previous_free}",
        f"- Current `akkalat/results`: {human_bytes(total_results)}",
        f"- Largest historical result directory: {human_bytes(largest)}",
        f"- Measured average per result cell: {human_bytes(average_cell)} "
        f"({measured_cells} metrics files)",
        f"- Formal campaign: 14 benchmarks x 5 configurations = {planned_cells} cells",
        f"- Planned-run estimate: {human_bytes(planned_budget)}",
        f"- Estimate with 50% safety margin: {human_bytes(required_with_margin)}",
        f"- Clearly disposable data: {human_bytes(disposable_bytes)}",
        f"- Incomplete data retained for diagnosis: {human_bytes(incomplete_bytes)}",
        f"- Free after cleanup: {human_bytes(usage.free)} (no deletion performed)",
        "",
        "## Classification summary",
        "",
    ]
    for classification, count in sorted(class_counts.items()):
        lines.append(f"- {classification}: {count} directories")

    lines += [
        "",
        "## Protected paper-active directories",
        "",
    ]
    protected = [
        record for record in records
        if record.paper_referenced or record.plot_referenced
    ]
    if protected:
        for record in sorted(protected, key=lambda r: r.name):
            refs = "; ".join(sorted(set(paper_refs.get(record.name, []))))
            lines.append(
                f"- `{record.name}` ({human_bytes(record.size_bytes)}): {refs}"
            )
    else:
        lines.append("- None discovered; inspect paper paths before cleanup.")

    lines += [
        "",
        "## Cleanup candidates",
        "",
        "Only empty directories are immediately disposable. Incomplete and "
        "non-16-MSHR runs are retained as debugging/reference evidence but must "
        "not be mixed into the new paper geomean.",
        "",
    ]
    disposable = [r for r in records if r.classification == "disposable"]
    if disposable:
        for record in sorted(disposable, key=lambda r: r.name):
            lines.append(f"- `{record.name}` ({human_bytes(record.size_bytes)})")
    else:
        lines.append("- No non-empty directory is approved for deletion.")

    lines += [
        "",
        "## New experiment invariants",
        "",
        "- Paper scope is exactly 14 benchmarks: " + ", ".join(PAPER_BENCHMARKS) + ".",
        "- Every formal command must contain `-l1v-mshr-entries=16`.",
        "- Every output directory must record commit, binary hash, benchmark, "
        "configuration, sampled/max-WG flags, and completion status.",
        "- Historical implicit-default runs are labeled as 160-entry legacy data.",
        "- New experiments use new result directories and never overwrite protected data.",
        "",
        f"Machine-readable details: `{MANIFEST_PATH.relative_to(REPO_ROOT)}`.",
    ]
    CLEANUP_PATH.write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> None:
    paper_refs = discover_paper_references()
    directories = sorted(path for path in RESULTS_DIR.iterdir() if path.is_dir())
    records = [inspect_result_dir(path, paper_refs) for path in directories]
    write_manifest(records)
    write_cleanup_report(records, paper_refs)
    print(f"wrote {MANIFEST_PATH}")
    print(f"wrote {CLEANUP_PATH}")
    print(f"inventoried {len(records)} result directories")


if __name__ == "__main__":
    main()
