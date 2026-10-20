#!/usr/bin/env python3
"""Paper figures and tables straight from CuPath campaign result directories.

Works on the directories written by runall2.py, CuPathLLM.py and
run_cupath_sensitivity.py (EXPERIMENT_METADATA.json plus one
``<target>_<benchmark>_<configuration>_{result.json,metrics.csv}`` pair per
cell).  The figures follow the paper style of plot_latest_cupath_figures.py,
plot_cupath_ablation_pasta_style.py and plot_cupath_sensitivity_*.py.

Speedup semantics are those of plot_latest_cupath_figures.py: a cell counts
only when its result.json (if present) reports success, returncode 0 and
simulator_returncode 0; speedup = baseline Driver total_time / config Driver
total_time for the same workload, only when the completed-WG counts
(max_wg_completed, else total_wg_count) match; GMEAN is the geometric mean
across workloads.  Missing, failed, running and WG-mismatched cells are
reported (and marked in the figures) instead of aborting; --strict turns any
of them into an error for final paper figures.

Series drawn side by side (configs, campaigns, sensitivity points of one
dimension) share one GMEAN workload set: the workloads OK in every series that
has any result, as in the web monitor.  Reports, CSVs and *_sources.json give
that n/N and the excluded workloads; "GMEAN*" marks a partial set, and the
geomean over each series' own workloads is the extra geomean_available column.

Subcommands (all write PDF+PNG figures and CSV tables under --out):

  ablation DIR [DIR ...] [--baseline-dir B]
      Per-workload grouped bars of m1/m2/m3/complete speedup over baseline,
      plus GMEAN.  Several DIRs are layered: a later DIR's successful cell
      replaces the same cell of an earlier DIR (e.g. OLD_RUN NEW_RERUN).
  sensitivity DIR [--baseline-dir MAIN] [--normalize matched|fixed]
      Per sensitivity dimension (read from SENSITIVITY_CAMPAIGN.json): bars per
      workload for every point, plus a geomean-vs-point line figure.  MAIN
      supplies the reference point when DIR does not contain one: MAIN's own
      reference point when MAIN is a sensitivity campaign run with
      --include-reference (Stage 4: sens-l2-row --baseline-dir sens-filter),
      else MAIN's plain configs.  With --normalize fixed every point, the
      reference included, is divided by one fixed baseline (MAIN's, the same
      cells, if given, else the reference point's baseline).
  compare DIR_A DIR_B [...] --labels hpca,mi200 [--config complete]
      Side-by-side speedup of one configuration per workload, each campaign
      over its own baseline (or --baseline-dirs).
  table DIR [DIR ...] [--merge] [--baseline-dir B]
      CSV + Markdown of Driver total_time, speedups and WG checks per cell.
  auto DIR [DIR ...] [--baseline-dir MAIN]
      Detects each campaign's kind and runs sensitivity or ablation plus table
      into OUT/<campaign name>/.
  slim SRC DST
      Copies a campaign with only what this script reads (metadata, result
      json, Driver rows of metrics, stdout tails): a few KB per cell.

Data extraction is pure Python; matplotlib is imported only when drawing, so
--no-plot (and the unit tests) work without it.

Examples:
  python3 akkalat/isca27_plot.py ablation RESULTS/1013_t13s4-main_wafer48_hpca_260ce73 --out /tmp/fig
  python3 akkalat/isca27_plot.py sensitivity RESULTS/<sens-dir> --baseline-dir RESULTS/<main-dir> --out /tmp/fig
  python3 akkalat/isca27_plot.py compare RESULTS/<hpca-main> RESULTS/<mi200-main> --labels hpca,mi200 --out /tmp/fig
  python3 akkalat/isca27_plot.py table RESULTS/<dir> --out /tmp/fig
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
import re
import shutil
import sys
import tempfile
from collections import Counter
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path


# ---------------------------------------------------------------------------
# Constants (paper style)
# ---------------------------------------------------------------------------

MODES = ("baseline", "m1", "m2", "m3", "complete")

# Order, legend text and colors of plot_cupath_ablation_pasta_style.py.
CONFIG_STYLE = (
    ("baseline", "Baseline", "#D6EFF5"),
    ("m2", "Remote Aggregation", "#5ABED8"),
    ("m3", "Remote Reuse", "#278BA5"),
    ("m1", "Local Pairing", "#ADDEEB"),
    ("complete", "Complete", "#F18541"),
)
CONFIG_LABELS = {key: label for key, label, _ in CONFIG_STYLE}
CONFIG_COLORS = {key: color for key, _, color in CONFIG_STYLE}
EXTRA_CONFIG_COLORS = ("#1D687C", "#F4A371", "#0A2329", "#FBE0D0")

# Workload order of the latest paper ablation figure.
WORKLOAD_ORDER = (
    "aes", "bitonicsort", "fastwalshtransform", "fft", "fir", "relu",
    "simpleconvolution", "floydwarshall", "kmeans", "matrixmultiplication",
    "pagerank", "im2col", "matrixtranspose", "spmv",
)
LABELS = {
    "aes": "AES",
    "bitonicsort": "BT",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "FWS",
    "im2col": "I2C",
    "kmeans": "KM",
    "matrixmultiplication": "MM",
    "matrixtranspose": "MT",
    "pagerank": "PR",
    "relu": "RELU",
    "simpleconvolution": "SC",
    "spmv": "SPMV",
}

# Sequential blues of plot_cupath_sensitivity_*.py (light -> dark).
SENSITIVITY_COLORS = ("#83CEE2", "#31ADCE", "#1D687C", "#0A2329")
SENSITIVITY_RAMP = ("#D6EFF5", "#83CEE2", "#31ADCE", "#1D687C", "#0A2329")
# Two-campaign comparison: paper blue/orange; text uses the darker shade.
COMPARE_COLORS = ("#31ADCE", "#F18541", "#1D687C", "#BE520E")
COMPARE_TEXT_COLORS = ("#1D687C", "#BE520E", "#0A2329", "#8A3A08")
CLIP_TEXT_COLOR = "#BE520E"
MISSING_COLOR = "#98999A"
MISMATCH_COLOR = "#C0392B"
LINE_COLOR = "#1D687C"
REFERENCE_COLOR = "#F18541"

SINGLE_COLUMN = 3.45
DOUBLE_COLUMN = 7.16
PAPER_GROUPS = 15  # 14 workloads + GMEAN fit one column

# Sensitivity dimensions known to run_cupath_sensitivity.py.
DIMENSIONS = {
    "filter-size": {
        "key": "filter_buckets_per_gpm",
        "title": "Cuckoo Filter Capacity",
        "xlabel": "Filter buckets per GPM",
        "short": "filter",
    },
    "l2-size": {
        "key": "l2_size_mb",
        "title": "L2 Cache Capacity",
        "xlabel": "L2 capacity per GPM (MiB)",
        "short": "l2",
    },
    "row-locality": {
        "key": "row_region_lines",
        "title": "HBM Row-Local Region",
        "xlabel": "Region size (cachelines)",
        "short": "row",
    },
}
REFERENCE_DEFAULTS = {
    "filter_buckets_per_gpm": 16 * 1024,
    "l2_size_mb": 4,
    "row_region_lines": 2,
}
REFERENCE_NAME = "reference"
# Flags that legitimately differ between a sensitivity point and the main run.
POINT_FLAGS = (
    "-typed-filter-capacity=",
    "-l2-cache-size-mb=",
    "-l2-adaptive-pair-region-lines=",
)
NOISE_FLAGS = ("-metric-file-name=", "-heartbeat-file=")

OK = "ok"
FAILED = "failed"
RUNNING = "running"
MISSING = "missing"
BAD = "bad-metrics"
NO_BASELINE = "no-baseline"
WG_MISMATCH = "wg-mismatch"
FLAG_SHORT = {
    OK: "",
    FAILED: "fail",
    RUNNING: "run",
    MISSING: "-",
    BAD: "bad",
    NO_BASELINE: "nobase",
    WG_MISMATCH: "WG!=",
}


class CampaignError(Exception):
    """A campaign directory cannot be read or a request cannot be met."""


# ---------------------------------------------------------------------------
# Reading cells
# ---------------------------------------------------------------------------

def read_driver_metrics(path: Path) -> dict[str, float]:
    """Driver rows (index, where, what, value) of a metrics CSV, as floats.

    Equivalent to the csv.reader loop of the paper scripts, but checks for
    "Driver" before splitting: the files hold ~100k rows of other components.
    """
    values: dict[str, float] = {}
    with Path(path).open("rb") as stream:
        for line in stream:
            if b"Driver" not in line:
                continue
            parts = line.split(b",")
            if len(parts) < 4 or parts[1].strip() != b"Driver":
                continue
            try:
                values[parts[2].strip().decode()] = float(parts[3])
            except (ValueError, UnicodeDecodeError):
                continue
    return values


def completed_wgs(values: dict[str, float]) -> int | None:
    value = values.get("max_wg_completed", values.get("total_wg_count"))
    if value is None:
        return None
    return int(round(value))


def stdout_return_code(path: Path) -> int | None:
    """The "Return code: N" line runall2 appends when a cell finishes."""
    try:
        with Path(path).open("rb") as stream:
            stream.seek(0, os.SEEK_END)
            size = stream.tell()
            stream.seek(max(0, size - 4096))
            tail = stream.read().decode("utf-8", "replace")
    except OSError:
        return None
    found = re.findall(r"^Return code: (-?\d+)\s*$", tail, re.M)
    return int(found[-1]) if found else None


@dataclass
class Cell:
    root: Path
    target: str
    benchmark: str
    configuration: str
    workload: str
    variant: str
    mode: str
    command: list = field(default_factory=list)
    status: str = MISSING
    reason: str = ""
    total_time: float | None = None
    completed_wgs: int | None = None

    @property
    def ok(self) -> bool:
        return self.status == OK

    @property
    def source(self) -> str:
        return self.root.name

    @property
    def stem(self) -> str:
        return f"{self.target}_{self.benchmark}_{self.configuration}"

    def describe(self) -> str:
        variant = f"{self.variant}/" if self.variant else ""
        return f"{self.workload} {variant}{self.mode}"


def evaluate_cell(cell: Cell) -> Cell:
    """Fill status/total_time/completed_wgs from the cell's files."""
    result_path = cell.root / f"{cell.stem}_result.json"
    metrics_path = cell.root / f"{cell.stem}_metrics.csv"
    stdout_path = cell.root / f"{cell.stem}_out.stdout"

    if result_path.is_file():
        try:
            result = json.loads(result_path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as error:
            cell.status, cell.reason = FAILED, f"unreadable result.json: {error}"
            return cell
        if (
            not result.get("success")
            or result.get("returncode") != 0
            or result.get("simulator_returncode") != 0
        ):
            reason = (
                f"success={result.get('success')} "
                f"returncode={result.get('returncode')} "
                f"simulator_returncode={result.get('simulator_returncode')}"
            )
            if result.get("timeout"):
                reason += f" timeout={result.get('timeout')}s"
            cell.status, cell.reason = FAILED, reason
            return cell
    elif not metrics_path.is_file():
        if stdout_path.is_file():
            code = stdout_return_code(stdout_path)
            if code is None:
                cell.status, cell.reason = RUNNING, "stdout without result"
            else:
                cell.status, cell.reason = FAILED, f"returncode={code}, no metrics"
        else:
            cell.status, cell.reason = MISSING, "not started"
        return cell

    if not metrics_path.is_file():
        cell.status, cell.reason = BAD, "metrics.csv missing"
        return cell
    try:
        values = read_driver_metrics(metrics_path)
    except OSError as error:
        cell.status, cell.reason = BAD, f"unreadable metrics: {error}"
        return cell
    total = values.get("total_time")
    wgs = completed_wgs(values)
    if total is None or not math.isfinite(total) or total <= 0:
        cell.status, cell.reason = BAD, "no positive Driver total_time"
        return cell
    if wgs is None:
        cell.status, cell.reason = BAD, "no completed-WG metric"
        return cell
    cell.total_time = total
    cell.completed_wgs = wgs
    cell.status, cell.reason = OK, ""
    return cell


# ---------------------------------------------------------------------------
# Campaigns
# ---------------------------------------------------------------------------

def split_configuration(configuration: str, modes=MODES) -> tuple[str, str]:
    """"l2-1mb_complete" -> ("l2-1mb", "complete"); "m1" -> ("", "m1")."""
    if configuration in modes:
        return "", configuration
    for mode in sorted(modes, key=len, reverse=True):
        suffix = "_" + mode
        if configuration.endswith(suffix) and len(configuration) > len(suffix):
            return configuration[: -len(suffix)], mode
    return "", configuration


def natural_key(text: str):
    return [int(part) if part.isdigit() else part
            for part in re.split(r"(\d+)", text)]


def workload_sort_key(workload: str):
    if workload in WORKLOAD_ORDER:
        return (0, WORKLOAD_ORDER.index(workload), [])
    return (1, 0, natural_key(workload))


def order_workloads(workloads) -> list[str]:
    return sorted(dict.fromkeys(workloads), key=workload_sort_key)


def workload_label(workload: str) -> str:
    if workload in LABELS:
        return LABELS[workload]
    match = re.match(r"llmop_([a-z0-9]+)_[^_]+_\d+_(.+)$", workload)
    if match:
        operation = re.sub(r"^layer\d+_", "", match.group(2))
        return f"{match.group(1).upper()} {operation}"
    match = re.match(r"resnet_resnet_resnet-(\d+)", workload)
    if match:
        return f"ResNet-{match.group(1)}"
    return workload


def config_label(mode: str) -> str:
    return CONFIG_LABELS.get(mode, mode)


def config_color(mode: str, index: int = 0) -> str:
    if mode in CONFIG_COLORS:
        return CONFIG_COLORS[mode]
    return EXTRA_CONFIG_COLORS[index % len(EXTRA_CONFIG_COLORS)]


def order_modes(modes) -> list[str]:
    known = [key for key, _, _ in CONFIG_STYLE if key in modes]
    return known + [mode for mode in dict.fromkeys(modes) if mode not in known]


@dataclass
class Campaign:
    root: Path
    kind: str
    cells: dict
    workloads: list
    variants: list
    modes: list
    manifest: dict | None = None
    metadata: dict = field(default_factory=dict)
    binary_digest: str = ""
    sources: list = field(default_factory=list)
    warnings: list = field(default_factory=list)

    @property
    def name(self) -> str:
        return self.root.name

    def get(self, workload: str, variant: str, mode: str) -> Cell | None:
        return self.cells.get((workload, variant, mode))

    def status_counts(self) -> Counter:
        return Counter(cell.status for cell in self.cells.values())

    def status_text(self) -> str:
        counts = self.status_counts()
        parts = [f"{counts[s]} {s}" for s in (OK, FAILED, RUNNING, MISSING, BAD)
                 if counts[s]]
        return f"{len(self.cells)} cells: " + (", ".join(parts) or "empty")

    def describe(self) -> str:
        return f"{self.name} [{self.kind}] {self.status_text()}"


def _experiments_from_files(root: Path) -> list[dict]:
    pattern = re.compile(
        r"^([^_]+)_([^_]+)_(.+?)_(metrics\.csv|result\.json|out\.stdout)$"
    )
    seen = {}
    for path in sorted(root.iterdir()):
        match = pattern.match(path.name)
        if match:
            key = match.group(1, 2, 3)
            seen.setdefault(key, {
                "target": key[0], "benchmark": key[1],
                "configuration": key[2], "command": [],
            })
    return list(seen.values())


def read_experiments(root: Path) -> tuple[list[dict], dict, list[str]]:
    """Union of EXPERIMENT_METADATA.json and EXPERIMENT_RERUN_METADATA_*.json."""
    warnings = []
    metadata = {}
    experiments = {}
    paths = [root / "EXPERIMENT_METADATA.json"]
    paths += sorted(root.glob("EXPERIMENT_RERUN_METADATA_*.json"))
    for path in paths:
        if not path.is_file():
            continue
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as error:
            warnings.append(f"unreadable {path.name}: {error}")
            continue
        if not metadata:
            metadata = data
        for item in data.get("experiments", []):
            key = (item.get("target", "baseline"), item["benchmark"],
                   item["configuration"])
            experiments.setdefault(key, {
                "target": key[0], "benchmark": key[1], "configuration": key[2],
                "command": item.get("command", []),
            })
    if not experiments:
        found = _experiments_from_files(root)
        if found:
            warnings.append("no EXPERIMENT_METADATA.json: cells inferred from files")
        return found, metadata, warnings
    return list(experiments.values()), metadata, warnings


def read_binary_digest(root: Path) -> str:
    path = root / "EXPERIMENT_BINARIES.json"
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return ""
    digests = data.get("sha256_by_target", {})
    return digests.get("baseline", "") or next(iter(digests.values()), "")


def load_campaign(root) -> Campaign:
    root = Path(root).expanduser().resolve()
    if not root.is_dir():
        raise CampaignError(f"not a directory: {root}")
    experiments, metadata, warnings = read_experiments(root)
    if not experiments:
        raise CampaignError(
            f"{root} has no EXPERIMENT_METADATA.json and no cell files")

    manifest = None
    manifest_path = root / "SENSITIVITY_CAMPAIGN.json"
    if manifest_path.is_file():
        try:
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        except (OSError, ValueError) as error:
            warnings.append(f"unreadable SENSITIVITY_CAMPAIGN.json: {error}")
    modes = list(MODES)
    points = set()
    if manifest:
        modes += [m for m in manifest.get("mechanisms_per_point", []) if m not in modes]
        points = {point["name"] for point in manifest.get("points", [])}

    cells = {}
    folded = False
    for item in experiments:
        variant, mode = split_configuration(item["configuration"], modes)
        workload = item["benchmark"]
        if variant and variant not in points:
            if manifest:
                warnings.append(
                    f"configuration {item['configuration']} is not a manifest point")
            else:
                # LLM operators: the configuration names the operator.
                workload = f"{item['benchmark']}_{variant}"
                variant = ""
                folded = True
        cell = Cell(
            root=root,
            target=item["target"],
            benchmark=item["benchmark"],
            configuration=item["configuration"],
            workload=workload,
            variant=variant,
            mode=mode,
            command=list(item.get("command") or []),
        )
        cells[(workload, variant, mode)] = evaluate_cell(cell)

    if manifest:
        kind = "sensitivity"
    elif folded:
        kind = "llm"
        warnings.append(
            "per-operator speedups; GMEAN is their plain geomean, while model "
            "speedups weight Driver times by operator multiplicity "
            "(cupath_llm_summary.csv)")
    else:
        kind = "main"
    manifest_benchmarks = (manifest or {}).get("benchmarks", [])
    workloads = order_workloads(
        list(manifest_benchmarks) + [key[0] for key in cells])
    variant_order = [p["name"] for p in (manifest or {}).get("points", [])]
    variants = list(dict.fromkeys(
        [v for v in variant_order if any(k[1] == v for k in cells)]
        + [key[1] for key in cells]))
    return Campaign(
        root=root,
        kind=kind,
        cells=cells,
        workloads=workloads,
        variants=variants,
        modes=order_modes({key[2] for key in cells}),
        manifest=manifest,
        metadata=metadata,
        binary_digest=read_binary_digest(root),
        sources=[root],
        warnings=warnings,
    )


def merge_campaigns(campaigns: list[Campaign]) -> Campaign:
    """Layer campaigns: a later campaign's OK cell replaces an earlier cell.

    A later failed or missing cell never hides an earlier OK one.
    """
    if not campaigns:
        raise CampaignError("no campaigns to merge")
    if len(campaigns) == 1:
        return campaigns[0]
    cells = {}
    for campaign in campaigns:
        for key, cell in campaign.cells.items():
            current = cells.get(key)
            if (current is None or cell.ok
                    or (not current.ok and cell.status != MISSING)):
                cells[key] = cell
    kinds = {campaign.kind for campaign in campaigns}
    warnings = [w for campaign in campaigns for w in campaign.warnings]
    digests = {c.binary_digest for c in campaigns if c.binary_digest}
    if len(digests) > 1:
        warnings.append(
            "merged campaigns use different frozen binaries: "
            + ", ".join(f"{c.name}={c.binary_digest[:12]}" for c in campaigns))
    return Campaign(
        root=campaigns[-1].root,
        kind=kinds.pop() if len(kinds) == 1 else "merged",
        cells=cells,
        workloads=order_workloads(w for c in campaigns for w in c.workloads),
        variants=list(dict.fromkeys(v for c in campaigns for v in c.variants)),
        modes=order_modes({key[2] for key in cells}),
        manifest=next((c.manifest for c in campaigns if c.manifest), None),
        metadata=campaigns[-1].metadata,
        binary_digest=campaigns[-1].binary_digest,
        sources=[root for c in campaigns for root in c.sources],
        warnings=warnings,
    )


# ---------------------------------------------------------------------------
# Speedups
# ---------------------------------------------------------------------------

@dataclass
class Pair:
    workload: str
    variant: str
    mode: str
    cell: Cell | None
    base: Cell | None
    speedup: float | None
    flag: str
    note: str = ""

    @property
    def ok(self) -> bool:
        return self.flag == OK


def pair_cells(workload, variant, mode, cell: Cell | None,
               base: Cell | None) -> Pair:
    """Speedup of ``cell`` over ``base`` with the paper's validity checks."""
    if cell is None:
        return Pair(workload, variant, mode, None, base, None, MISSING, "no such cell")
    if not cell.ok:
        return Pair(workload, variant, mode, cell, base, None, cell.status, cell.reason)
    if base is None or not base.ok:
        note = "baseline " + (base.status if base else "missing")
        return Pair(workload, variant, mode, cell, base, None, NO_BASELINE, note)
    if cell.completed_wgs != base.completed_wgs:
        return Pair(
            workload, variant, mode, cell, base, None, WG_MISMATCH,
            f"completed WGs baseline={base.completed_wgs} "
            f"result={cell.completed_wgs}")
    return Pair(workload, variant, mode, cell, base,
                base.total_time / cell.total_time, OK)


def geomean(values) -> float | None:
    values = [value for value in values if value is not None]
    if not values:
        return None
    return math.exp(sum(math.log(value) for value in values) / len(values))


@dataclass
class Series:
    key: str
    label: str
    color: str
    pairs: list
    text_color: str | None = None
    # Workloads of the GMEAN shared by the series drawn together (share_gmean);
    # None: this series alone, over every workload it has.
    common: tuple | None = None

    @property
    def values(self) -> list:
        return [pair.speedup for pair in self.pairs]

    @property
    def n_ok(self) -> int:
        return sum(1 for pair in self.pairs if pair.ok)

    @property
    def complete(self) -> bool:
        return self.n_ok == len(self.pairs)

    @property
    def available_gmean(self) -> float | None:
        """Geomean over every workload this series has (an extra CSV column)."""
        return geomean(self.values)

    @property
    def gmean(self) -> float | None:
        """The GMEAN bar: over the shared workloads once share_gmean() ran."""
        if self.common is None:
            return self.available_gmean
        if not self.common:
            return None
        return geomean([pair.speedup for pair in self.pairs
                        if pair.workload in self.common])


@dataclass
class GmeanScope:
    """Workloads that the GMEAN of series drawn side by side covers."""
    common: list
    excluded: list
    empty: list      # labels of series without a single usable workload
    counted: int     # series with at least one usable workload
    total: int

    @property
    def partial(self) -> bool:
        return len(self.common) < self.total or bool(self.empty)

    def describe(self) -> str:
        text = (f"GMEAN over {len(self.common)}/{self.total} workloads OK in all "
                f"{self.counted} series with results")
        if self.excluded:
            text += "; excluded: " + ", ".join(workload_label(w) for w in self.excluded)
        if self.empty:
            text += "; no results (left out): " + ", ".join(self.empty)
        return text

    def as_dict(self) -> dict:
        return {
            "rule": "geomean over the workloads OK in every series that has any "
                    "usable workload (series without any are left out)",
            "workloads": len(self.common), "total": self.total,
            "common": list(self.common), "excluded": list(self.excluded),
            "series_without_results": list(self.empty),
        }


def share_gmean(series_list) -> GmeanScope:
    """Make the series' GMEANs comparable: restrict them to one workload set.

    The set is the workloads OK in every series that has any OK workload (a
    series with none, e.g. a configuration that has not finished anywhere, is
    left out instead of emptying every GMEAN).  This is the web monitor's rule.
    Idempotent; call it again after changing the list of drawn series.
    """
    workloads = list(dict.fromkeys(p.workload for s in series_list for p in s.pairs))
    with_data = [s for s in series_list if s.n_ok]
    ok_sets = [{p.workload for p in s.pairs if p.ok} for s in with_data]
    common = [w for w in workloads if ok_sets and all(w in ok for ok in ok_sets)]
    for series in series_list:
        series.common = tuple(common)
    return GmeanScope(
        common=common,
        excluded=[w for w in workloads if w not in common],
        empty=[s.label for s in series_list if not s.n_ok],
        counted=len(with_data),
        total=len(workloads),
    )


def ablation_series(campaign: Campaign, configs, workloads, variant="",
                    baseline: Campaign | None = None) -> list[Series]:
    base_source = baseline or campaign
    series = []
    extra = 0
    for mode in configs:
        pairs = []
        for workload in workloads:
            base = base_source.get(workload, variant, "baseline")
            cell = base if mode == "baseline" else campaign.get(workload, variant, mode)
            pairs.append(pair_cells(workload, variant, mode, cell, base))
        if mode not in CONFIG_COLORS:
            extra += 1
        series.append(Series(mode, config_label(mode),
                             config_color(mode, extra - 1), pairs))
    share_gmean(series)
    return series


def compare_series(campaigns, labels, config, workloads, variant="",
                   baselines=None) -> list[Series]:
    series = []
    for index, (campaign, label) in enumerate(zip(campaigns, labels)):
        base_source = (baselines or [None] * len(campaigns))[index] or campaign
        pairs = []
        for workload in workloads:
            base = base_source.get(workload, variant, "baseline")
            cell = campaign.get(workload, variant, config)
            pairs.append(pair_cells(workload, variant, config, cell, base))
        series.append(Series(
            campaign.name, label, COMPARE_COLORS[index % len(COMPARE_COLORS)],
            pairs, COMPARE_TEXT_COLORS[index % len(COMPARE_TEXT_COLORS)]))
    share_gmean(series)
    return series


def command_differences(command_a, command_b, ignore=()) -> tuple[list, list]:
    """Flags only in A, only in B (binary path and per-run paths ignored)."""
    prefixes = tuple(NOISE_FLAGS) + tuple(ignore)

    def flags(command):
        return {flag for flag in list(command)[1:]
                if not flag.startswith(prefixes)}

    a, b = flags(command_a or []), flags(command_b or [])
    return sorted(a - b), sorted(b - a)


def first_command(campaign: Campaign, mode: str, variant: str = "",
                  workloads=None) -> tuple[str, list]:
    for workload in workloads or campaign.workloads:
        cell = campaign.get(workload, variant, mode)
        if cell is not None and cell.command:
            return workload, cell.command
    return "", []


def describe_differences(name_a, cmd_a, name_b, cmd_b, ignore=()) -> list[str]:
    if not cmd_a or not cmd_b:
        return []
    only_a, only_b = command_differences(cmd_a, cmd_b, ignore)
    lines = []
    if only_a:
        lines.append(f"flags only in {name_a}: {' '.join(only_a)}")
    if only_b:
        lines.append(f"flags only in {name_b}: {' '.join(only_b)}")
    return lines


# ---------------------------------------------------------------------------
# Sensitivity
# ---------------------------------------------------------------------------

@dataclass
class SensPoint:
    name: str
    dimension: str
    value: float | None
    reference: bool
    source: Campaign
    variant: str

    def tick(self) -> str:
        return format_point(self.dimension, self.value, self.name, legend=False)

    def legend(self) -> str:
        return format_point(self.dimension, self.value, self.name, legend=True)


@dataclass
class SensDimension:
    name: str
    title: str
    xlabel: str
    points: list
    series: list = field(default_factory=list)
    scope: GmeanScope | None = None


def format_point(dimension, value, name, legend=False) -> str:
    if value is None:
        return name
    if dimension == "filter-size":
        text = f"{value / 1024:g}K"
        return f"{text} buckets" if legend else text
    if dimension == "l2-size":
        return f"{value:g} MiB" if legend else f"{value:g}"
    if dimension == "row-locality":
        return f"{value:g} lines" if legend else f"{value:g}"
    return f"{value:g}"


def numeric_keys(point: dict) -> list[str]:
    return [key for key, value in point.items()
            if isinstance(value, (int, float)) and not isinstance(value, bool)]


def dimension_key(dimension: str, points: list[dict], reference: dict) -> str | None:
    if dimension in DIMENSIONS:
        return DIMENSIONS[dimension]["key"]
    candidates = numeric_keys(points[0]) if points else []
    for key in candidates:
        values = {point.get(key) for point in points}
        if len(values) > 1 or (reference.get(key) is not None
                               and values != {reference.get(key)}):
            return key
    return None


def reference_values(manifest: dict) -> dict:
    """Values of the reference point: explicit, else inferred, else defaults."""
    points = manifest.get("points", [])
    for point in points:
        if point.get("dimension") == REFERENCE_NAME:
            return {key: point[key] for key in numeric_keys(point)}
    values = dict(REFERENCE_DEFAULTS)
    keys = {DIMENSIONS[d]["key"]: d for d in DIMENSIONS}
    for key, own_dimension in keys.items():
        seen = Counter(point[key] for point in points
                       if point.get("dimension") != own_dimension and key in point)
        if seen:
            values[key] = seen.most_common(1)[0][0]
    return values


def reference_point(manifest: dict | None) -> dict | None:
    """The point of dimension "reference" (run_cupath_sensitivity.py --include-reference)."""
    return next((p for p in (manifest or {}).get("points", [])
                 if p.get("dimension") == REFERENCE_NAME), None)


def build_sensitivity(campaign: Campaign, main: Campaign | None = None,
                      normalize: str = "matched", config: str = "complete",
                      dimensions=None, workloads=None):
    """Per-dimension speedup series for a sensitivity campaign.

    Returns (dimensions, notes).  Each dimension's ``series`` holds one Series
    per point, ordered by the swept value, and its GMEANs cover the workloads
    OK in every point (``scope``).  The reference point comes from the
    campaign itself or, when absent, from ``main``: its reference point when
    ``main`` is a sensitivity campaign run with --include-reference (Stage 4
    splits the dimensions over two campaigns), else its plain configs.
    ``normalize="fixed"`` divides every point, the reference included, by
    ``main``'s baseline (the same cells) or, without ``main``, by the
    reference point's baseline.
    """
    manifest = campaign.manifest
    if not manifest:
        raise CampaignError(f"{campaign.name} has no SENSITIVITY_CAMPAIGN.json")
    if normalize not in ("matched", "fixed"):
        raise CampaignError(f"unknown normalization {normalize}")
    notes = []
    workloads = list(workloads or campaign.workloads)
    ref_values = reference_values(manifest)

    manifest_points = manifest.get("points", [])
    reference = reference_point(manifest)

    # Which cells of ``main`` stand for "the main configuration".
    main_variant = ""
    if main is not None and main.manifest:
        main_reference = reference_point(main.manifest)
        if main_reference is None:
            raise CampaignError(
                f"--baseline-dir {main.name} is a sensitivity campaign without a "
                "reference point (run it with --include-reference, or pass a main "
                "campaign)")
        main_variant = main_reference["name"]
        if reference is None:
            # Place the borrowed reference at the values it was really run with.
            main_values = {key: main_reference[key] for key in numeric_keys(main_reference)}
            for key, value in main_values.items():
                if key in ref_values and ref_values[key] != value:
                    notes.append(
                        f"WARNING reference point {main_variant} of {main.name} has "
                        f"{key}={value:g}, but {campaign.name} implies {ref_values[key]:g}")
            ref_values.update(main_values)
    main_text = (f"{main.name} ({main_variant})" if main is not None and main_variant
                 else main.name if main is not None else "")

    if reference is not None:
        ref_source, ref_variant = campaign, reference["name"]
        notes.append(f"reference point {reference['name']} from {campaign.name}")
        if main is not None and normalize == "matched":
            notes.append(f"--baseline-dir {main.name} unused "
                         "(campaign has its own reference; see --normalize fixed)")
    elif main is not None:
        ref_source, ref_variant = main, main_variant
        notes.append(f"reference point from {'sensitivity' if main_variant else 'main'} "
                     f"campaign {main_text}")
    else:
        ref_source = ref_variant = None
        notes.append("no reference point (campaign has none and no --baseline-dir)")

    if normalize == "fixed":
        if main is not None:
            fixed_source, fixed_variant = main, main_variant
        elif ref_source is not None:
            fixed_source, fixed_variant = ref_source, ref_variant
        else:
            raise CampaignError(
                "--normalize fixed needs a reference point or --baseline-dir")
        notes.append(f"normalization: fixed baseline from {fixed_source.name}"
                     + (f" ({fixed_variant})" if fixed_variant else ""))
    else:
        fixed_source = fixed_variant = None
        notes.append("normalization: matched (each point over its own baseline)")

    # Provenance: does the reference/fixed baseline run the same flags?
    checked = set()
    point = next((p["name"] for p in manifest_points
                  if p.get("dimension") != REFERENCE_NAME), None)
    for other, variant, label in (
        (ref_source, ref_variant, "reference"),
        (fixed_source, fixed_variant, "fixed baseline"),
    ):
        if other is None or other is campaign or (id(other), variant) in checked:
            continue
        checked.add((id(other), variant))
        if point is None:
            continue
        _, a = first_command(campaign, "baseline", point, workloads)
        _, b = first_command(other, "baseline", variant, workloads)
        if not a or not b:
            where = (f"{campaign.name} ({point})" if not a else
                     f"{other.name}" + (f" ({variant})" if variant else ""))
            notes.append(f"WARNING {label} flags not checked: no baseline command "
                         f"recorded for {where}")
            continue
        for line in describe_differences(campaign.name, a, other.name, b, POINT_FLAGS):
            notes.append(f"WARNING {label} differs: {line}")

    order = [d for d in DIMENSIONS] + [
        p["dimension"] for p in manifest_points
        if p.get("dimension") not in DIMENSIONS and p.get("dimension") != REFERENCE_NAME]
    selected = list(dict.fromkeys(order))
    selected = [d for d in selected
                if any(p.get("dimension") == d for p in manifest_points)]
    if dimensions:
        wanted = [aliased_dimension(d) for d in dimensions]
        missing = [d for d in wanted if d not in selected]
        if missing:
            raise CampaignError(f"dimensions not in campaign: {missing}")
        selected = [d for d in selected if d in wanted]

    result = []
    for dimension in selected:
        specs = [p for p in manifest_points if p.get("dimension") == dimension]
        key = dimension_key(dimension, specs, ref_values)
        points = [
            SensPoint(spec["name"], dimension,
                      spec.get(key) if key else None, False, campaign, spec["name"])
            for spec in specs
        ]
        if ref_source is not None:
            value = ref_values.get(key) if key else None
            if value is None or value not in [p.value for p in points]:
                name = ref_variant or REFERENCE_NAME
                points.append(SensPoint(name, dimension, value, True,
                                        ref_source, ref_variant))
        if all(p.value is not None for p in points):
            points.sort(key=lambda p: p.value)
        info = DIMENSIONS.get(dimension, {})
        dim = SensDimension(
            dimension,
            info.get("title", dimension),
            info.get("xlabel", key or dimension),
            points,
        )
        colors = sequential_colors(len(points))
        for point, color in zip(points, colors):
            pairs = []
            for workload in workloads:
                cell = point.source.get(workload, point.variant, config)
                if normalize == "fixed":
                    base = fixed_source.get(workload, fixed_variant, "baseline")
                else:
                    base = point.source.get(workload, point.variant, "baseline")
                pairs.append(pair_cells(workload, point.name, config, cell, base))
            dim.series.append(Series(point.name, point.legend(), color, pairs))
        dim.scope = share_gmean(dim.series)
        result.append(dim)
    return result, notes


def aliased_dimension(name: str) -> str:
    aliases = {info["short"]: key for key, info in DIMENSIONS.items()}
    return aliases.get(name.strip().lower(), name.strip().lower())


def _hex_to_rgb(color: str):
    color = color.lstrip("#")
    return tuple(int(color[i:i + 2], 16) for i in (0, 2, 4))


def sequential_colors(count: int) -> list[str]:
    """Paper blues for <= 4 points, interpolated along the same ramp beyond."""
    if count <= len(SENSITIVITY_COLORS):
        return list(SENSITIVITY_COLORS[:count])
    ramp = [_hex_to_rgb(c) for c in SENSITIVITY_RAMP[1:]]
    colors = []
    for index in range(count):
        position = index / (count - 1) * (len(ramp) - 1)
        low = min(int(position), len(ramp) - 2)
        t = position - low
        rgb = [round(a + (b - a) * t) for a, b in zip(ramp[low], ramp[low + 1])]
        colors.append("#" + "".join(f"{v:02X}" for v in rgb))
    return colors


# ---------------------------------------------------------------------------
# Tables
# ---------------------------------------------------------------------------

def fmt(value, digits=6) -> str:
    if value is None:
        return ""
    if isinstance(value, float):
        return f"{value:.{digits}g}" if math.isfinite(value) else ""
    return str(value)


PAIR_FIELDS = (
    "workload", "label", "variant", "config", "flag", "note",
    "baseline_source", "baseline_time_s", "baseline_wgs",
    "source", "time_s", "wgs", "speedup",
)


def pair_row(pair: Pair) -> dict:
    return {
        "workload": pair.workload,
        "label": workload_label(pair.workload),
        "variant": pair.variant,
        "config": pair.mode,
        "flag": pair.flag,
        "note": pair.note,
        "baseline_source": pair.base.source if pair.base else "",
        "baseline_time_s": pair.base.total_time if pair.base else None,
        "baseline_wgs": pair.base.completed_wgs if pair.base else None,
        "source": pair.cell.source if pair.cell else "",
        "time_s": pair.cell.total_time if pair.cell else None,
        "wgs": pair.cell.completed_wgs if pair.cell else None,
        "speedup": pair.speedup,
    }


def write_csv(path: Path, fields, rows) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(fields))
        writer.writeheader()
        for row in rows:
            writer.writerow({key: fmt(row.get(key), 12) for key in fields})
    return path


GMEAN_FIELDS = ("geomean_available",)


def gmean_note(series, scope: GmeanScope) -> str:
    text = (f"{len(scope.common)}/{scope.total} workloads OK in all "
            f"{scope.counted} series with results "
            f"(this series: {series.n_ok}/{len(series.pairs)} OK)")
    if scope.excluded:
        text += "; excluded: " + " ".join(scope.excluded)
    return text


def series_rows(series_list, extra=None) -> list[dict]:
    """Pair rows plus one GMEAN row per series.

    The GMEAN row's speedup is the shared GMEAN (share_gmean); the series'
    geomean over all of its own workloads is the extra geomean_available column.
    ``extra``: columns added to every row, or a list of them, one per series.
    """
    scope = share_gmean(series_list)
    extras = extra if isinstance(extra, list) else [extra or {}] * len(series_list)
    rows = []
    for series, more in zip(series_list, extras):
        for pair in series.pairs:
            row = pair_row(pair)
            row["series"] = series.label
            row.update(more)
            rows.append(row)
        gm = series.gmean
        rows.append({
            **more,
            "series": series.label,
            "workload": "GMEAN",
            "label": "GMEAN",
            "config": series.pairs[0].mode if series.pairs else "",
            "variant": series.pairs[0].variant if series.pairs else "",
            "flag": "none" if gm is None else "partial" if scope.partial else OK,
            "note": gmean_note(series, scope),
            "speedup": gm,
            "geomean_available": series.available_gmean,
        })
    return rows


def markdown_table(headers, rows) -> str:
    def clean(value):
        return str(value).replace("|", "\\|")

    lines = ["| " + " | ".join(clean(h) for h in headers) + " |",
             "|" + "|".join("---" for _ in headers) + "|"]
    lines += ["| " + " | ".join(clean(c) for c in row) + " |" for row in rows]
    return "\n".join(lines)


def pair_cell_text(pair: Pair) -> str:
    if pair.ok:
        return f"{pair.speedup:.3f}"
    return FLAG_SHORT.get(pair.flag, pair.flag)


def campaign_pairs(campaign: Campaign, baseline: Campaign | None = None,
                   workloads=None) -> list[Pair]:
    """Every (workload, variant, mode) of a campaign paired with its baseline."""
    base_source = baseline or campaign
    workloads = list(workloads or campaign.workloads)
    pairs = []
    for variant in campaign.variants:
        for mode in campaign.modes:
            for workload in workloads:
                cell = campaign.get(workload, variant, mode)
                if cell is None:
                    continue  # not part of this campaign's grid
                base = base_source.get(workload, variant, "baseline")
                pairs.append(pair_cells(workload, variant, mode, cell, base))
    return pairs


def summarize_pairs(pairs: list[Pair]) -> list[dict]:
    groups = {}
    for pair in pairs:
        groups.setdefault((pair.variant, pair.mode), []).append(pair)
    rows = []
    for (variant, mode), items in groups.items():
        values = [pair.speedup for pair in items if pair.ok]
        rows.append({
            "variant": variant,
            "config": mode,
            "ok": len(values),
            "expected": len(items),
            "geomean_available": geomean(values),
            "geomean_complete": geomean(values) if len(values) == len(items) else None,
            "issues": ", ".join(
                f"{n} {flag}" for flag, n in Counter(
                    p.flag for p in items if not p.ok).most_common()),
        })
    return rows


def issue_lines(pairs: list[Pair], limit: int = 12, prefix: str = "") -> list[str]:
    grouped = {}
    for pair in pairs:
        if not pair.ok:
            grouped.setdefault(pair.flag, []).append(pair)
    lines = []
    for flag in (WG_MISMATCH, FAILED, BAD, NO_BASELINE, RUNNING, MISSING):
        items = grouped.get(flag, [])
        if not items:
            continue
        shown = []
        for pair in items[:limit]:
            variant = f"{pair.variant}/" if pair.variant else ""
            text = f"{workload_label(pair.workload)} {variant}{pair.mode}"
            if flag in (WG_MISMATCH, FAILED, BAD) and pair.note:
                text += f" ({pair.note})"
            shown.append(text)
        more = f", +{len(items) - limit} more" if len(items) > limit else ""
        lines.append(f"{prefix}{flag} ({len(items)}): " + "; ".join(shown) + more)
    return lines


def campaign_markdown(campaign: Campaign, pairs: list[Pair]) -> str:
    out = [f"## {campaign.name}", "",
           f"- kind: {campaign.kind}; {campaign.status_text()}"]
    revision = campaign.metadata.get("git_revision")
    if revision:
        out.append(f"- git revision: {revision}")
    if campaign.binary_digest:
        out.append(f"- baseline binary sha256: {campaign.binary_digest[:16]}...")
    if len(campaign.sources) > 1:
        out.append("- layered sources: " + ", ".join(p.name for p in campaign.sources))
    for warning in campaign.warnings:
        out.append(f"- warning: {warning}")
    out.append("")

    columns = list(dict.fromkeys((p.variant, p.mode) for p in pairs))
    workloads = list(dict.fromkeys(p.workload for p in pairs))
    index = {(p.workload, p.variant, p.mode): p for p in pairs}

    def header(variant, mode):
        return f"{variant}/{mode}" if variant else mode

    speed_columns = [c for c in columns if c[1] != "baseline"]
    if speed_columns:
        rows = []
        for workload in workloads:
            row = [workload_label(workload)]
            for variant, mode in speed_columns:
                pair = index.get((workload, variant, mode))
                row.append(pair_cell_text(pair) if pair else "")
            rows.append(row)
        gm = ["GMEAN"]
        for variant, mode in speed_columns:
            items = [index[(w, variant, mode)] for w in workloads
                     if (w, variant, mode) in index]
            values = [p.speedup for p in items if p.ok]
            value = geomean(values)
            if value is None:
                gm.append("")
            else:
                star = "" if len(values) == len(items) else f" ({len(values)}/{len(items)})"
                gm.append(f"{value:.3f}{star}")
        rows.append(gm)
        out += ["### Speedup over baseline", "",
                markdown_table(["workload"] + [header(*c) for c in speed_columns], rows),
                ""]

    rows = []
    for workload in workloads:
        row = [workload_label(workload)]
        wgs = set()
        for variant, mode in columns:
            pair = index.get((workload, variant, mode))
            cell = pair.cell if pair else None
            if cell is not None and cell.ok:
                row.append(f"{cell.total_time * 1e6:.2f}")
                wgs.add(cell.completed_wgs)
            elif cell is not None:
                row.append(FLAG_SHORT.get(cell.status, cell.status))
            else:
                row.append("")
        row.append("/".join(str(w) for w in sorted(wgs)))
        rows.append(row)
    out += ["### Driver total_time (us) and completed WGs", "",
            markdown_table(["workload"] + [header(*c) for c in columns] + ["WGs"], rows),
            ""]
    issues = issue_lines(pairs, limit=40)
    out += ["### Issues", ""] + ([f"- {line}" for line in issues] or ["- none"]) + [""]
    return "\n".join(out)


# ---------------------------------------------------------------------------
# Figure layout helpers (pure)
# ---------------------------------------------------------------------------

def spread_positions(desired, widths, gap=0.0, lower=None, upper=None) -> list[float]:
    """Centers for labels of given widths so that none overlap.

    Overlapping labels are merged into clusters centered on the mean of their
    desired positions (least-squares displacement); then the row is clamped
    into [lower, upper].  Input order is preserved in the output.
    """
    count = len(desired)
    if count == 0:
        return []
    order = sorted(range(count), key=lambda i: desired[i])
    clusters = []
    for i in order:
        clusters.append({"idx": [i], "rel": [0.0], "shift": desired[i]})
        while len(clusters) > 1:
            left, right = clusters[-2], clusters[-1]
            left_edge = (left["shift"] + left["rel"][-1]
                         + widths[left["idx"][-1]] / 2)
            right_edge = (right["shift"] + right["rel"][0]
                          - widths[right["idx"][0]] / 2)
            if left_edge + gap <= right_edge + 1e-12:
                break
            idx = left["idx"] + right["idx"]
            rel = [0.0]
            for k in range(1, len(idx)):
                rel.append(rel[-1] + (widths[idx[k - 1]] + widths[idx[k]]) / 2 + gap)
            shift = sum(desired[j] - r for j, r in zip(idx, rel)) / len(idx)
            clusters[-2:] = [{"idx": idx, "rel": rel, "shift": shift}]
    positions = [0.0] * count
    for cluster in clusters:
        for j, r in zip(cluster["idx"], cluster["rel"]):
            positions[j] = cluster["shift"] + r
    ordered = sorted(range(count), key=lambda i: (positions[i], i))
    if lower is not None:
        previous = None
        for i in ordered:
            bound = (lower + widths[i] / 2 if previous is None else
                     positions[previous] + (widths[previous] + widths[i]) / 2 + gap)
            positions[i] = max(positions[i], bound)
            previous = i
    if upper is not None:
        previous = None
        for i in reversed(ordered):
            bound = (upper - widths[i] / 2 if previous is None else
                     positions[previous] - (widths[previous] + widths[i]) / 2 - gap)
            positions[i] = min(positions[i], bound)
            previous = i
    return positions


TICK_STEPS = (0.1, 0.2, 0.25, 0.5, 1, 2, 2.5, 5, 10, 20, 25, 50, 100)


def tick_step(ymax: float) -> float:
    for step in TICK_STEPS:
        if ymax / step <= 3.0 + 1e-9:
            return step
    return TICK_STEPS[-1]


def auto_ymax(values) -> float:
    finite = [v for v in values if v is not None and math.isfinite(v)]
    target = max(finite + [1.0]) * 1.08
    step = tick_step(target)
    return math.ceil(target / step - 1e-9) * step


def y_ticks(ymax: float) -> list[float]:
    step = tick_step(ymax)
    count = int(math.floor(ymax / step + 1e-9))
    return [round(i * step, 6) for i in range(count + 1)]


def figure_width(groups: int, requested: float | None = None) -> float:
    if requested:
        return requested
    if groups <= PAPER_GROUPS:
        return SINGLE_COLUMN
    return min(DOUBLE_COLUMN, SINGLE_COLUMN * groups / PAPER_GROUPS)


def legend_rows(count: int) -> list[list[int]]:
    """Paper legends: one row up to four entries, else rows of three."""
    if count <= 4:
        return [list(range(count))]
    return [list(range(i, min(i + 3, count))) for i in range(0, count, 3)]


# ---------------------------------------------------------------------------
# Drawing (matplotlib imported lazily)
# ---------------------------------------------------------------------------

@dataclass
class BarChart:
    workloads: list
    series: list
    ymax: float | str = 2.0
    ylabel: str = "Speedup"
    clip_color: str | None = CLIP_TEXT_COLOR  # None: each series' text color
    clip_fontsize: float = 4.9
    clip_bold: bool = True
    legend_fontsize: float = 5.7

    def group_labels(self) -> list[str]:
        # "*": the GMEAN group covers only the workloads every series has.
        partial = share_gmean(self.series).partial
        return [workload_label(w) for w in self.workloads] + [
            "GMEAN*" if partial else "GMEAN"]

    def resolved_ymax(self) -> float:
        if self.ymax == "auto":
            values = [v for s in self.series for v in s.values + [s.gmean]]
            return auto_ymax(values)
        return float(self.ymax)


def pyplot():
    os.environ.setdefault("MPLCONFIGDIR",
                          os.path.join(tempfile.gettempdir(), "matplotlib"))
    try:
        import matplotlib
    except ImportError as error:
        raise CampaignError(
            "matplotlib is not installed for this python; run with a python "
            "that has it (e.g. yss2 ~/miniconda3/bin/python3) or pass --no-plot"
        ) from error
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    plt.rcParams.update({
        "font.family": "DejaVu Sans",
        "pdf.fonttype": 42,
        "ps.fonttype": 42,
        "axes.linewidth": 0.8,
    })
    return plt


def style_axis(ax, xs, labels, ymax: float, ylabel: str, show_xlabels=True):
    x_step = xs[1] - xs[0] if len(xs) > 1 else 0.82
    gm_x = xs[-1]
    ax.axvspan(gm_x - 0.36, gm_x + 0.36, color="#F5F5F5", zorder=0)
    ax.axhline(1.0, color="#333333", linestyle=(0, (4, 2)), linewidth=0.9, zorder=2)
    ax.axvline(gm_x - x_step / 2, color="#888888", linestyle=":", linewidth=0.7)
    ax.set_ylim(0, ymax)
    ticks = y_ticks(ymax)
    ax.set_yticks(ticks)
    ax.set_yticklabels([f"{t:g}" for t in ticks])
    ax.set_ylabel(ylabel, fontsize=7.0)
    ax.set_xticks(xs)
    ax.set_xticklabels([])
    ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=0.7)
    if show_xlabels:
        # Paper labels (<= 6 characters) are centered; long ones such as LLM
        # operator names end at their group so they do not cross the plot.
        long_labels = max(len(label) for label in labels) > 6
        for x, label in zip(xs, labels):
            ax.text(x + (0.12 if long_labels else 0.0), -0.08 if not long_labels else -0.04,
                    label, transform=ax.get_xaxis_transform(),
                    ha="right" if long_labels else "center", va="top",
                    rotation=30, rotation_mode="anchor", fontsize=6.0,
                    clip_on=False)
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)


def place_clipped_labels(fig, ax, labels, minimum_font=3.0) -> float:
    """Spread capped-value labels on one row without overlap; return min gap px."""
    if not labels:
        return float("inf")
    desired = [label.get_position()[0] for label in labels]
    x_low, x_high = ax.get_xlim()
    while True:
        fig.canvas.draw()
        renderer = fig.canvas.get_renderer()
        inverse = ax.transData.inverted()
        widths = []
        for label in labels:
            box = label.get_window_extent(renderer=renderer)
            left = inverse.transform((box.x0, box.y0))[0]
            right = inverse.transform((box.x1, box.y0))[0]
            widths.append(abs(right - left))
        pixel = abs(inverse.transform((1.5, 0))[0] - inverse.transform((0, 0))[0])
        positions = spread_positions(desired, widths, pixel, x_low, x_high)
        for label, x in zip(labels, positions):
            label.set_x(x)
        gap = label_gap(fig, labels)
        if gap >= 0 or labels[0].get_fontsize() <= minimum_font:
            return gap
        for label in labels:
            label.set_fontsize(label.get_fontsize() - 0.4)


def label_gap(fig, labels) -> float:
    if len(labels) < 2:
        return float("inf")
    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    boxes = sorted((label.get_window_extent(renderer=renderer) for label in labels),
                   key=lambda box: box.x0)
    return min(right.x0 - left.x1 for left, right in zip(boxes, boxes[1:]))


def draw_bar_panel(ax, chart: BarChart, show_xlabels=True, legend=True):
    from matplotlib.patches import Patch

    labels = chart.group_labels()
    x_step = 0.82
    xs = [i * x_step for i in range(len(labels))]
    count = max(len(chart.series), 1)
    group_width = 0.56 if count <= 5 else 0.66
    bar_width = group_width / max(count, 3)
    ymax = chart.resolved_ymax()
    clipped = []

    for index, series in enumerate(chart.series):
        offset = -bar_width * count / 2 + bar_width / 2 + index * bar_width
        values = series.values + [series.gmean]
        flags = [pair.flag for pair in series.pairs] + [
            OK if series.gmean is not None else MISSING]
        heights = [math.nan if v is None else min(v, ymax) for v in values]
        bars = ax.bar([x + offset for x in xs], heights, width=bar_width * 0.94,
                      color=series.color, edgecolor="none", linewidth=0, zorder=3)
        text_color = chart.clip_color or series.text_color or series.color
        for x, bar, value, flag in zip(xs, bars, values, flags):
            center = x + offset
            if value is None:
                mark = "≠" if flag == WG_MISMATCH else "×"
                ax.text(center, ymax * 0.012, mark, ha="center", va="bottom",
                        fontsize=4.6, zorder=4,
                        color=MISMATCH_COLOR if flag == WG_MISMATCH else MISSING_COLOR)
                continue
            if value > ymax:
                clipped.append(ax.text(
                    center, ymax + 0.0125 * ymax, f"{value:.1f}",
                    ha="center", va="bottom", fontsize=chart.clip_fontsize,
                    fontweight="bold" if chart.clip_bold else "normal",
                    color=text_color, clip_on=False))

    style_axis(ax, xs, labels, ymax, chart.ylabel, show_xlabels)
    ax.set_xlim(xs[0] - group_width * 0.62, xs[-1] + group_width * 0.62)

    if legend:
        handles = [Patch(facecolor=s.color, edgecolor="none", linewidth=0,
                         label=s.label) for s in chart.series]
        rows = legend_rows(len(handles))
        style = {"mode": "expand", "frameon": False, "borderaxespad": 0,
                 "columnspacing": 0.9, "handlelength": 1.0,
                 "handletextpad": 0.45, "fontsize": chart.legend_fontsize}
        for row_index, row in enumerate(rows):
            bottom = 1.06 + 0.09 * (len(rows) - 1 - row_index)
            artist = ax.legend(handles=[handles[i] for i in row], loc="lower center",
                               bbox_to_anchor=(0.0, bottom, 1.0, 0.08),
                               ncol=max(len(row), len(rows[0])), **style)
            if row_index < len(rows) - 1:
                ax.add_artist(artist)
                # add_artist clips to the axes, which hides the legend from
                # tight_layout and bbox_inches="tight" (top row got cropped).
                artist.set_clip_on(False)
    return clipped


def save_figure(fig, base: Path, formats, dpi: int) -> list[Path]:
    base.parent.mkdir(parents=True, exist_ok=True)
    written = []
    for extension in formats:
        path = base.with_suffix("." + extension)
        options = {"bbox_inches": "tight", "pad_inches": 0.025}
        if extension == "png":
            options["dpi"] = dpi
        fig.savefig(path, **options)
        written.append(path)
    return written


def render_bar_chart(chart: BarChart, base: Path, formats, dpi,
                     width=None, height=1.58) -> list[Path]:
    plt = pyplot()
    fig, ax = plt.subplots(figsize=(figure_width(len(chart.workloads) + 1, width),
                                    height))
    clipped = draw_bar_panel(ax, chart)
    fig.tight_layout(pad=0.25)
    gap = place_clipped_labels(fig, ax, clipped)
    if gap < 0:
        print(f"warning: clipped labels overlap by {-gap:.1f}px in {base.name}",
              file=sys.stderr)
    written = save_figure(fig, base, formats, dpi)
    plt.close(fig)
    return written


def render_stacked(charts, base: Path, formats, dpi, width=None) -> list[Path]:
    plt = pyplot()
    groups = max(len(c.workloads) for c in charts) + 1
    fig, axes = plt.subplots(len(charts), 1, squeeze=False,
                             figsize=(max(DOUBLE_COLUMN, figure_width(groups, width)),
                                      1.57 * len(charts)))
    clipped = [draw_bar_panel(ax, chart) for ax, chart in zip(axes[:, 0], charts)]
    fig.tight_layout(pad=0.35, h_pad=0.70)
    for ax, labels in zip(axes[:, 0], clipped):
        place_clipped_labels(fig, ax, labels)
    written = save_figure(fig, base, formats, dpi)
    plt.close(fig)
    return written


def render_sensitivity_lines(dimensions, base: Path, formats, dpi,
                             width=None) -> list[Path]:
    """Geomean speedup vs swept value, one panel per dimension (shared y)."""
    plt = pyplot()
    count = len(dimensions)
    fig, axes = plt.subplots(
        1, count, squeeze=False, sharey=True,
        figsize=(width or (SINGLE_COLUMN if count == 1 else DOUBLE_COLUMN * min(count, 3) / 3),
                 1.72))
    values = [s.gmean for d in dimensions for s in d.series if s.gmean is not None]
    low = min(values + [1.0])
    high = max(values + [1.0])
    pad = max(0.06, (high - low) * 0.18)
    for index, (ax, dim) in enumerate(zip(axes[0], dimensions)):
        xs = list(range(len(dim.points)))
        scope = dim.scope or share_gmean(dim.series)
        ys = [s.gmean for s in dim.series]
        ax.plot([x for x, y in zip(xs, ys) if y is not None],
                [y for y in ys if y is not None],
                color=LINE_COLOR, linewidth=1.2, zorder=3)
        for x, y, point, series in zip(xs, ys, dim.points, dim.series):
            if y is None:
                ax.text(x, low - pad * 0.6, f"n/a\n{series.n_ok}/{len(series.pairs)}",
                        ha="center", va="bottom", fontsize=5.0, color=MISSING_COLOR)
                continue
            face = REFERENCE_COLOR if point.reference else LINE_COLOR
            if scope.partial:
                face = "white"   # GMEAN over the n/N workloads all points have
            ax.plot([x], [y], marker="o", markersize=4.0, zorder=4,
                    markerfacecolor=face, markeredgewidth=0.9,
                    markeredgecolor=REFERENCE_COLOR if point.reference else LINE_COLOR)
            star = "*" if scope.partial else ""
            ax.annotate(f"{y:.2f}x{star}", (x, y), xytext=(0, 3.5),
                        textcoords="offset points", ha="center", va="bottom",
                        fontsize=5.6, color="#333333",
                        fontweight="bold" if point.reference else "normal")
        ax.axhline(1.0, color="#333333", linestyle=(0, (4, 2)), linewidth=0.9, zorder=1)
        ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=0)
        ax.set_xticks(xs)
        ax.set_xticklabels([p.tick() for p in dim.points])
        for tick, point in zip(ax.get_xticklabels(), dim.points):
            if point.reference:
                tick.set_fontweight("bold")
        ax.set_xlim(-0.45, len(xs) - 0.55)
        ax.set_xlabel(dim.xlabel + (f" (*{len(scope.common)}/{scope.total} workloads)"
                                    if scope.partial else ""),
                      fontsize=6.4, labelpad=2.0)
        ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=0.7)
        if index == 0:
            ax.set_ylabel("Geomean speedup", fontsize=7.0)
        for spine in ax.spines.values():
            spine.set_visible(True)
            spine.set_linewidth(0.7)
    axes[0][0].set_ylim(max(0.0, low - pad), high + pad * 1.6)
    fig.tight_layout(pad=0.3, w_pad=0.55)
    written = save_figure(fig, base, formats, dpi)
    plt.close(fig)
    return written


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def select_workloads(available, requested: str | None) -> list[str]:
    if not requested:
        return list(available)
    by_label = {workload_label(w).lower(): w for w in available}
    chosen = []
    for item in requested.split(","):
        item = item.strip()
        if not item:
            continue
        if item in available:
            chosen.append(item)
        elif item.lower() in by_label:
            chosen.append(by_label[item.lower()])
        else:
            raise CampaignError(f"unknown workload {item}; have: {', '.join(available)}")
    return chosen


def parse_formats(value: str) -> list[str]:
    formats = [f.strip().lower().lstrip(".") for f in value.split(",") if f.strip()]
    bad = [f for f in formats if f not in ("pdf", "png", "svg")]
    if bad or not formats:
        raise argparse.ArgumentTypeError(f"unsupported formats {value}")
    return formats


def parse_ymax(value: str):
    if value == "auto":
        return "auto"
    try:
        number = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError(
            f"must be a number or auto: {value}") from error
    if number <= 1.0:
        raise argparse.ArgumentTypeError("must be above 1")
    return number


def write_sources(path: Path, payload: dict) -> Path:
    payload = {"created_at": datetime.now().isoformat(timespec="seconds"),
               "command": " ".join(sys.argv), **payload}
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, default=str) + "\n", encoding="utf-8")
    return path


def campaign_source(campaign: Campaign) -> dict:
    return {
        "directory": str(campaign.root),
        "layers": [str(p) for p in campaign.sources],
        "kind": campaign.kind,
        "git_revision": campaign.metadata.get("git_revision", ""),
        "binary_sha256": campaign.binary_digest,
        "status": dict(campaign.status_counts()),
        "warnings": campaign.warnings,
    }


def report(title: str, lines) -> None:
    print(f"== {title}")
    for line in lines:
        print(f"   {line}")


def check_strict(args, pairs) -> None:
    bad = [pair for pair in pairs if not pair.ok]
    if getattr(args, "strict", False) and bad:
        raise CampaignError(
            f"--strict: {len(bad)} cells are not usable: "
            + "; ".join(issue_lines(bad, limit=5)))


def series_summary(series_list) -> list[str]:
    """The shared GMEAN scope, then each series' GMEAN over it."""
    scope = share_gmean(series_list)
    lines = [scope.describe()]
    for series in series_list:
        lines.append(f"GMEAN {series.label}: {series_gmean_text(series, scope)}")
    return lines


def series_gmean_text(series, scope: GmeanScope) -> str:
    gm = series.gmean
    text = "n/a" if gm is None else f"{gm:.4f}"
    text += f" ({len(scope.common)}/{scope.total})"
    available = series.available_gmean
    if (series.n_ok and series.n_ok != len(scope.common) and available is not None
            and (gm is None or not math.isclose(available, gm, rel_tol=1e-12))):
        text += (f"; over its own {series.n_ok}/{len(series.pairs)}: "
                 f"{available:.4f}")
    return text


def command_ablation(args) -> dict:
    campaigns = [load_campaign(d) for d in args.dirs]
    campaign = merge_campaigns(campaigns)
    baseline = load_campaign(args.baseline_dir) if args.baseline_dir else None
    configs = ([c.strip() for c in args.configs.split(",") if c.strip()]
               if args.configs else
               [m for m in order_modes(campaign.modes)])
    if not args.no_baseline_bar and "baseline" not in configs:
        configs = ["baseline"] + configs
    if args.no_baseline_bar:
        configs = [c for c in configs if c != "baseline"]
    workloads = select_workloads(campaign.workloads, args.workloads)
    series = ablation_series(campaign, configs, workloads, args.variant, baseline)
    pairs = [p for s in series for p in s.pairs]

    name = args.name or "ablation"
    out = Path(args.out)
    written = [write_csv(out / f"{name}.csv", ("series",) + PAIR_FIELDS + GMEAN_FIELDS,
                         series_rows(series))]
    sources = {"campaigns": [campaign_source(c) for c in campaigns],
               "baseline_dir": campaign_source(baseline) if baseline else None,
               "configs": configs, "variant": args.variant,
               "gmean": share_gmean(series).as_dict(),
               "issues": issue_lines(pairs, limit=100)}
    written.append(write_sources(out / f"{name}_sources.json", sources))
    report(campaign.describe() if len(campaigns) == 1 else
           "layered " + " < ".join(c.name for c in campaigns),
           campaign.warnings + series_summary(series) + issue_lines(pairs))
    check_strict(args, pairs)
    if not args.no_plot:
        chart = BarChart(workloads, series, args.ymax)
        written += render_bar_chart(chart, out / name, args.formats, args.dpi,
                                    args.width, args.height)
    return {"written": written, "series": series}


def command_compare(args) -> dict:
    if len(args.dirs) < 2:
        raise CampaignError("compare needs at least two campaign directories")
    campaigns = [load_campaign(d) for d in args.dirs]
    labels = ([l.strip() for l in args.labels.split(",")] if args.labels
              else [c.name for c in campaigns])
    if len(labels) != len(campaigns):
        raise CampaignError("--labels needs one label per directory")
    baselines = None
    if args.baseline_dirs:
        items = [i.strip() for i in args.baseline_dirs.split(",")]
        if len(items) != len(campaigns):
            raise CampaignError("--baseline-dirs needs one entry per directory "
                                "(leave an entry empty to use the campaign itself)")
        baselines = [load_campaign(i) if i else None for i in items]
    workloads = select_workloads(
        order_workloads(w for c in campaigns for w in c.workloads), args.workloads)
    series = compare_series(campaigns, labels, args.config, workloads,
                            args.variant, baselines)
    pairs = [p for s in series for p in s.pairs]
    notes = []
    ref_workload, ref_cmd = first_command(campaigns[0], args.config,
                                          args.variant, workloads)
    for campaign, label in zip(campaigns[1:], labels[1:]):
        _, other = first_command(campaign, args.config, args.variant,
                                 [ref_workload] + workloads)
        notes += describe_differences(labels[0], ref_cmd, label, other)

    name = args.name or f"compare_{args.config}"
    out = Path(args.out)
    written = [write_csv(out / f"{name}.csv", ("series",) + PAIR_FIELDS + GMEAN_FIELDS,
                         series_rows(series))]
    sources = {"campaigns": [campaign_source(c) for c in campaigns],
               "labels": labels, "config": args.config,
               "baseline_dirs": [campaign_source(b) if b else None
                                 for b in (baselines or [])],
               "gmean": share_gmean(series).as_dict(),
               "flag_differences": notes,
               "issues": [line for s in series for line in
                          issue_lines(s.pairs, 100, f"{s.label}: ")]}
    written.append(write_sources(out / f"{name}_sources.json", sources))
    report("compare " + " vs ".join(labels),
           [c.describe() for c in campaigns] + notes + series_summary(series)
           + [line for s in series for line in issue_lines(s.pairs, 12, f"{s.label}: ")])
    check_strict(args, pairs)
    if not args.no_plot:
        chart = BarChart(workloads, series, args.ymax, clip_color=None)
        written += render_bar_chart(chart, out / name, args.formats, args.dpi,
                                    args.width, args.height)
    return {"written": written, "series": series}


def command_sensitivity(args) -> dict:
    campaign = load_campaign(args.dir)
    main = load_campaign(args.baseline_dir) if args.baseline_dir else None
    dimensions = ([d for d in args.dimensions.split(",") if d.strip()]
                  if args.dimensions else None)
    workloads = select_workloads(campaign.workloads, args.workloads)
    dims, notes = build_sensitivity(campaign, main, args.normalize, args.config,
                                    dimensions, workloads)
    if not dims:
        raise CampaignError(f"{campaign.name}: no sensitivity dimensions found")
    name = args.name or "sensitivity"
    out = Path(args.out)
    rows, summary, pairs = [], [], []
    lines = list(notes)
    for dim in dims:
        scope = share_gmean(dim.series)
        lines.append(f"{dim.name}: {scope.describe()}")
        rows += series_rows(dim.series, [
            {"dimension": dim.name, "point": point.name, "value": point.value,
             "reference": point.reference} for point in dim.points])
        for point, series in zip(dim.points, dim.series):
            pairs += series.pairs
            summary.append({
                "dimension": dim.name, "point": point.name, "value": point.value,
                "reference": point.reference, "source": point.source.name,
                "ok": series.n_ok, "expected": len(series.pairs),
                "geomean": series.gmean,
                "workloads": len(scope.common),
                "excluded": " ".join(scope.excluded),
                "geomean_available": series.available_gmean,
                "geomean_complete": (series.available_gmean
                                     if series.complete else None),
            })
            lines.append(
                f"{dim.name:<13} {point.name:<28} "
                + series_gmean_text(series, scope)
                + ("  [ref]" if point.reference else ""))
    fields = (("dimension", "point", "value", "reference", "series")
              + PAIR_FIELDS + GMEAN_FIELDS)
    written = [
        write_csv(out / f"{name}.csv", fields, rows),
        write_csv(out / f"{name}_summary.csv",
                  ("dimension", "point", "value", "reference", "source", "ok",
                   "expected", "geomean", "workloads", "excluded",
                   "geomean_available", "geomean_complete"), summary),
    ]
    sources = {"campaign": campaign_source(campaign),
               "baseline_dir": campaign_source(main) if main else None,
               "normalize": args.normalize, "config": args.config,
               "gmean": {dim.name: share_gmean(dim.series).as_dict() for dim in dims},
               "notes": notes, "issues": issue_lines(pairs, limit=100)}
    written.append(write_sources(out / f"{name}_sources.json", sources))
    report(campaign.describe(), campaign.warnings + lines + issue_lines(pairs))
    check_strict(args, pairs)
    if not args.no_plot:
        charts = []
        for dim in dims:
            chart = BarChart(workloads, dim.series, args.ymax, clip_color=None,
                             clip_fontsize=3.6, clip_bold=False,
                             legend_fontsize=5.2)
            charts.append(chart)
            short = DIMENSIONS.get(dim.name, {}).get("short", dim.name)
            written += render_bar_chart(chart, out / f"{name}_{short}",
                                        args.formats, args.dpi, args.width,
                                        args.height)
        if len(charts) > 1:
            written += render_stacked(charts, out / f"{name}_workloads",
                                      args.formats, args.dpi)
        written += render_sensitivity_lines(dims, out / f"{name}_geomean",
                                            args.formats, args.dpi)
    return {"written": written, "dimensions": dims, "notes": notes}


def command_table(args) -> dict:
    campaigns = [load_campaign(d) for d in args.dirs]
    if args.merge:
        campaigns = [merge_campaigns(campaigns)]
    baseline = load_campaign(args.baseline_dir) if args.baseline_dir else None
    name = args.name or "table"
    out = Path(args.out)
    rows, summary, markdown, all_pairs = [], [], [], []
    title = "# Campaign table\n\nSpeedup = baseline Driver total_time / config " \
            "Driver total_time, only when completed WGs match; GMEAN over " \
            "workloads (n/N when partial).\n"
    markdown.append(title)
    for campaign in campaigns:
        workloads = select_workloads(campaign.workloads, args.workloads)
        pairs = campaign_pairs(campaign, baseline, workloads)
        all_pairs += pairs
        for pair in pairs:
            row = pair_row(pair)
            row["campaign"] = campaign.name
            if pair.cell is not None and pair.base is not None and pair.cell.ok \
                    and pair.base.ok:
                row["wg_match"] = pair.cell.completed_wgs == pair.base.completed_wgs
            rows.append(row)
        for item in summarize_pairs(pairs):
            item["campaign"] = campaign.name
            summary.append(item)
        markdown.append(campaign_markdown(campaign, pairs))
        report(campaign.describe(),
               campaign.warnings + [
                   f"{(s['variant'] + '/') if s['variant'] else ''}{s['config']}: "
                   + ("n/a" if s["geomean_available"] is None
                      else f"{s['geomean_available']:.4f}")
                   + f" ({s['ok']}/{s['expected']})"
                   for s in summarize_pairs(pairs) if s["config"] != "baseline"]
               + issue_lines(pairs))
    written = [
        write_csv(out / f"{name}_cells.csv",
                  ("campaign",) + PAIR_FIELDS[:-1] + ("wg_match", "speedup"), rows),
        write_csv(out / f"{name}_summary.csv",
                  ("campaign", "variant", "config", "ok", "expected",
                   "geomean_available", "geomean_complete", "issues"), summary),
    ]
    md_path = out / f"{name}.md"
    md_path.write_text("\n".join(markdown) + "\n", encoding="utf-8")
    written.append(md_path)
    check_strict(args, all_pairs)
    return {"written": written}


def command_auto(args) -> dict:
    """Sensitivity figures for sensitivity campaigns, ablation otherwise."""
    written = []
    shared = {key: getattr(args, key) for key in (
        "workloads", "strict", "formats", "dpi", "ymax", "width", "height",
        "no_plot")}
    for directory in args.dirs:
        campaign = load_campaign(directory)
        out = str(Path(args.out) / campaign.name)
        if campaign.kind == "sensitivity":
            written += command_sensitivity(argparse.Namespace(
                **shared, out=out, name=None, dir=directory,
                baseline_dir=args.baseline_dir, normalize=args.normalize,
                config=args.config, dimensions=None))["written"]
        elif len(campaign.modes) > 1:
            written += command_ablation(argparse.Namespace(
                **shared, out=out, name=None, dirs=[directory],
                baseline_dir=None, configs=None, variant="",
                no_baseline_bar=False))["written"]
        else:
            print(f"{campaign.name}: one configuration only, table without figure")
        written += command_table(argparse.Namespace(
            **shared, out=out, name=None, dirs=[directory], merge=False,
            baseline_dir=None))["written"]
    return {"written": written}


SLIM_JSON = re.compile(r"^(EXPERIMENT_.*|SENSITIVITY_CAMPAIGN|.*_result)\.json$")


def command_slim(args) -> dict:
    source = Path(args.src).expanduser().resolve()
    target = Path(args.dst).expanduser().resolve()
    if not source.is_dir():
        raise CampaignError(f"not a directory: {source}")
    if target == source or source in target.parents:
        raise CampaignError("slim destination must be outside the source")
    if target.exists() and any(target.iterdir()) and not args.force:
        raise CampaignError(f"{target} is not empty (use --force)")
    target.mkdir(parents=True, exist_ok=True)
    counts = Counter()
    for path in sorted(source.iterdir()):
        if not path.is_file():
            continue
        if SLIM_JSON.match(path.name):
            shutil.copy2(path, target / path.name)
            counts["json"] += 1
        elif path.name.endswith("_metrics.csv"):
            with path.open("rb") as stream, (target / path.name).open("wb") as sink:
                for index, line in enumerate(stream):
                    if index == 0 or b"Driver" in line:
                        sink.write(line)
            counts["metrics"] += 1
        elif path.name.endswith("_out.stdout"):
            with path.open("rb") as stream:
                stream.seek(0, os.SEEK_END)
                stream.seek(max(0, stream.tell() - 2048))
                (target / path.name).write_bytes(stream.read())
            counts["stdout"] += 1
    print(f"slim copy {source} -> {target}: " +
          ", ".join(f"{n} {k}" for k, n in sorted(counts.items())))
    return {"written": [target]}


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=__doc__.split("\n\n")[0],
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="See the module docstring (head of the file) for details.")
    sub = parser.add_subparsers(dest="command", required=True)

    output = argparse.ArgumentParser(add_help=False)
    output.add_argument("--out", required=True, help="output directory")
    output.add_argument("--name", help="output file name prefix")
    output.add_argument("--workloads",
                        help="comma list (benchmark names or labels) to select/order")
    output.add_argument("--strict", action="store_true",
                        help="fail if any cell is missing/failed/WG-mismatched")

    figure = argparse.ArgumentParser(add_help=False)
    figure.add_argument("--formats", default="pdf,png", type=parse_formats)
    figure.add_argument("--dpi", type=int, default=300)
    figure.add_argument("--ymax", default="2", type=parse_ymax,
                        help="y-axis cap (paper: 2; values above are printed); or auto")
    figure.add_argument("--width", type=float, help="figure width in inches")
    figure.add_argument("--height", type=float, default=1.58)
    figure.add_argument("--no-plot", action="store_true",
                        help="only write tables (no matplotlib needed)")

    p = sub.add_parser("ablation", parents=[output, figure],
                       help="m1/m2/m3/complete speedup bars per workload")
    p.add_argument("dirs", nargs="+", help="campaign dirs; later ones override")
    p.add_argument("--baseline-dir", help="take baseline cells from this campaign")
    p.add_argument("--configs", help="comma list, default: all present in paper order")
    p.add_argument("--variant", default="", help="sensitivity point to plot")
    p.add_argument("--no-baseline-bar", action="store_true")
    p.set_defaults(func=command_ablation)

    p = sub.add_parser("sensitivity", parents=[output, figure],
                       help="per-dimension sensitivity figures")
    p.add_argument("dir")
    p.add_argument("--baseline-dir", "--reference-dir", dest="baseline_dir",
                   help="main campaign: reference point if DIR lacks one, "
                        "and the fixed baseline for --normalize fixed")
    p.add_argument("--normalize", choices=("matched", "fixed"), default="matched",
                   help="matched: point complete / point baseline (default); "
                        "fixed: point complete / one fixed baseline")
    p.add_argument("--config", default="complete")
    p.add_argument("--dimensions", help="comma list, e.g. l2,row")
    p.set_defaults(func=command_sensitivity)

    p = sub.add_parser("compare", parents=[output, figure],
                       help="one config's speedup side by side across campaigns")
    p.add_argument("dirs", nargs="+")
    p.add_argument("--labels", help="comma list, one per dir (e.g. hpca,mi200)")
    p.add_argument("--config", default="complete")
    p.add_argument("--variant", default="")
    p.add_argument("--baseline-dirs",
                   help="comma list aligned with dirs; empty entry = own baseline")
    p.set_defaults(func=command_compare)

    p = sub.add_parser("table", parents=[output],
                       help="CSV/Markdown of times, speedups and WG checks")
    p.add_argument("dirs", nargs="+")
    p.add_argument("--merge", action="store_true", help="layer dirs like ablation")
    p.add_argument("--baseline-dir")
    p.set_defaults(func=command_table)

    p = sub.add_parser("auto", parents=[output, figure],
                       help="detect each campaign's kind and draw its figures")
    p.add_argument("dirs", nargs="+")
    p.add_argument("--baseline-dir", help="main campaign for sensitivity dirs")
    p.add_argument("--normalize", choices=("matched", "fixed"), default="matched")
    p.add_argument("--config", default="complete")
    p.set_defaults(func=command_auto)

    p = sub.add_parser("slim", help="copy only what this script reads")
    p.add_argument("src")
    p.add_argument("dst")
    p.add_argument("--force", action="store_true")
    p.set_defaults(func=command_slim)
    return parser


def main(argv=None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        result = args.func(args)
    except CampaignError as error:
        print(f"error: {error}", file=sys.stderr)
        return 2
    for path in result.get("written", []):
        print(path)
    return 0


if __name__ == "__main__":
    sys.exit(main())
