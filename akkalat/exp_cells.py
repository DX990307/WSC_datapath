#!/usr/bin/env python3
"""Campaign adapter for `akita exp`: one runall2 campaign directory as exp-cells/1.

Usage: python3 akkalat/exp_cells.py <campaign-dir>

Prints a CampaignCells JSON document (see akita experiment/DESIGN.md):
dimensions workload / variant / config, metrics total_time (Driver, seconds)
and wgs (completed work-groups), one cell per <target>_<benchmark>_<config>
of EXPERIMENT_METADATA.json (+ EXPERIMENT_RERUN_METADATA_*.json), and the
sweep of a sensitivity campaign (SENSITIVITY_CAMPAIGN.json).

Read-only, standard library only, a single file: `akita exp` may run it on a
server as `python3 - <dir> < exp_cells.py`. The status rules follow
plot_latest_cupath_figures.py / isca27_plot.py: a cell is ok when its
result.json says success with returncode 0 and simulator_returncode 0 and its
metrics.csv has a positive Driver total_time and a completed WG count.
"""

import glob
import json
import os
import re
import sys
from collections import Counter

SCHEMA = "exp-cells/1"

# Mechanism configurations of runall2, in the paper's order.
MODES = ("baseline", "m1", "m2", "m3", "complete")
MODE_ORDER = ("baseline", "m2", "m3", "m1", "complete")
MODE_LABELS = {
    "baseline": "Baseline", "m1": "Local Pairing", "m2": "Remote Aggregation",
    "m3": "Remote Reuse", "complete": "Complete",
}

# Workload order and short labels of the paper figures
# (plot_cupath_overall_speedup.py, isca27_plot.py).
WORKLOAD_ORDER = (
    "aes", "bitonicsort", "fastwalshtransform", "fft", "fir", "relu",
    "simpleconvolution", "floydwarshall", "kmeans", "matrixmultiplication",
    "pagerank", "im2col", "matrixtranspose", "spmv",
)
WORKLOAD_LABELS = {
    "aes": "AES", "bitonicsort": "BT", "fastwalshtransform": "FWT", "fft": "FFT",
    "fir": "FIR", "floydwarshall": "FWS", "im2col": "I2C", "kmeans": "KM",
    "matrixmultiplication": "MM", "matrixtranspose": "MT", "pagerank": "PR",
    "relu": "RELU", "simpleconvolution": "SC", "spmv": "SPMV",
}

# Sensitivity dimensions written by run_cupath_sensitivity.py.
DIMENSIONS = {
    "filter-size": {"key": "filter_buckets_per_gpm", "label": "Cuckoo filter capacity per GPM",
                    "unit": "buckets"},
    "l2-size": {"key": "l2_size_mb", "label": "L2 capacity per GPM", "unit": "MiB"},
    "row-locality": {"key": "row_region_lines", "label": "HBM row-locality region",
                     "unit": "lines"},
}
REFERENCE_DEFAULTS = {"filter_buckets_per_gpm": 16 * 1024, "l2_size_mb": 4, "row_region_lines": 2}
REFERENCE_DIMENSION = "reference"
# Flags that carry a sensitivity point's values: ignored when the commands of
# a sensitivity point are compared with a reference from another campaign.
SWEEP_FLAGS = ["-typed-filter-capacity=", "-l2-cache-size-mb=", "-l2-adaptive-pair-region-lines="]
# Per-run output paths, dropped from the commands.
OUTPUT_FLAGS = ("-metric-file-name=", "-heartbeat-file=")


def read_json(path):
    try:
        with open(path, "rb") as f:
            return json.loads(f.read().decode("utf-8", "replace"))
    except (OSError, ValueError):
        return None


def driver_values(path):
    """Driver rows (index, where, what, value) of a metrics CSV.

    The files hold ~100k rows of other components, so look for "Driver"
    instead of splitting every line.
    """
    with open(path, "rb") as f:
        data = f.read()
    values = {}
    i = data.find(b"Driver")
    while i >= 0:
        start = data.rfind(b"\n", 0, i) + 1
        end = data.find(b"\n", i)
        if end < 0:
            end = len(data)
        parts = data[start:end].split(b",")
        if len(parts) >= 4 and parts[1].strip() == b"Driver":
            try:
                values[parts[2].strip().decode()] = float(parts[3].decode())
            except (ValueError, UnicodeDecodeError):
                pass
        i = data.find(b"Driver", end)
    return values


def stdout_return_code(path):
    """The "Return code: N" line runall2 appends when a cell finishes."""
    try:
        with open(path, "rb") as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - 4096))
            tail = f.read().decode("utf-8", "replace")
    except OSError:
        return None
    found = re.findall(r"^Return code: (-?\d+)\s*$", tail, re.M)
    return int(found[-1]) if found else None


def evaluate(stem):
    """(status, reason, total_time_s, completed_wgs) of one cell."""
    result, metrics, stdout = stem + "_result.json", stem + "_metrics.csv", stem + "_out.stdout"
    if os.path.exists(result):
        r = read_json(result)
        if not isinstance(r, dict):
            return "failed", "result.json is not readable", None, None
        if not r.get("success") or r.get("returncode") != 0 or r.get("simulator_returncode") != 0:
            reason = "success=%s returncode=%s simulator_returncode=%s" % (
                r.get("success"), r.get("returncode"), r.get("simulator_returncode"))
            if r.get("timeout"):
                reason += " timeout=%ss" % r.get("timeout")
            return "failed", reason, None, None
    elif not os.path.exists(metrics):
        if os.path.exists(stdout):
            code = stdout_return_code(stdout)
            if code is None:
                return "running", "stdout exists, no result.json yet", None, None
            return "failed", "returncode=%d, no metrics" % code, None, None
        return "missing", "not started", None, None
    if not os.path.exists(metrics):
        return "bad", "no metrics.csv", None, None
    try:
        values = driver_values(metrics)
    except OSError as e:
        return "bad", "metrics not readable: %s" % e, None, None
    total = values.get("total_time")
    wgs = values.get("max_wg_completed", values.get("total_wg_count"))
    if total is None or not total > 0 or total == float("inf"):
        return "bad", "no positive Driver total_time", None, None
    if wgs is None:
        return "bad", "no completed WG count (max_wg_completed / total_wg_count)", None, None
    return "ok", "", total, int(round(wgs))


def split_configuration(configuration, modes):
    """"l2-1mb_complete" -> ("l2-1mb", "complete"); "m1" -> ("", "m1")."""
    if configuration in modes:
        return "", configuration
    for mode in sorted(modes, key=len, reverse=True):
        suffix = "_" + mode
        if configuration.endswith(suffix) and len(configuration) > len(suffix):
            return configuration[:-len(suffix)], mode
    return "", configuration


def _natural_key(text):
    return [(0, int(p), "") if p.isdigit() else (1, 0, p) for p in re.split(r"(\d+)", text)]


def order_workloads(workloads):
    def key(w):
        if w in WORKLOAD_ORDER:
            return (0, WORKLOAD_ORDER.index(w), [])
        return (1, 0, _natural_key(w))
    return sorted(dict.fromkeys(workloads), key=key)


def workload_label(workload):
    if workload in WORKLOAD_LABELS:
        return WORKLOAD_LABELS[workload]
    m = re.match(r"llmop_([a-z0-9]+)_[^_]+_\d+_(.+)$", workload)
    if m:
        return "%s %s" % (m.group(1).upper(), re.sub(r"^layer\d+_", "", m.group(2)))
    m = re.match(r"resnet_resnet_resnet-(\d+)", workload)
    if m:
        return "ResNet-%s" % m.group(1)
    return workload


def _numeric_keys(point):
    return [k for k, v in point.items() if isinstance(v, (int, float)) and not isinstance(v, bool)]


def format_point(dimension, value, name):
    """Axis tick of a sensitivity point; the unit is the dimension's."""
    if value is None:
        return name
    if dimension == "filter-size":
        return "%gK" % (value / 1024)
    return "%g" % value


def sweep_of(manifest):
    """exp-cells/1 sweep: dimensions with their points and reference point."""
    points = [p for p in manifest.get("points") or [] if isinstance(p, dict) and p.get("name")]
    reference = next((p for p in points if p.get("dimension") == REFERENCE_DIMENSION), None)
    if reference is not None:
        ref_values = {k: reference[k] for k in _numeric_keys(reference)}
    else:
        # Inferred: a dimension's value in every other dimension's points.
        ref_values = dict(REFERENCE_DEFAULTS)
        for dim, info in DIMENSIONS.items():
            seen = Counter(p[info["key"]] for p in points
                           if p.get("dimension") != dim and info["key"] in p)
            if seen:
                ref_values[info["key"]] = seen.most_common(1)[0][0]
    order = list(DIMENSIONS) + [p.get("dimension") for p in points]
    dims = []
    for dim in dict.fromkeys(order):
        if dim == REFERENCE_DIMENSION:
            continue
        specs = [p for p in points if p.get("dimension") == dim]
        if not specs:
            continue
        info = DIMENSIONS.get(dim, {})
        key = info.get("key")
        if not key:
            for k in _numeric_keys(specs[0]):
                vals = {p.get(k) for p in specs}
                if len(vals) > 1 or (ref_values.get(k) is not None and vals != {ref_values.get(k)}):
                    key = k
                    break
        ref_value = ref_values.get(key) if key else None
        out = []
        for p in specs:
            value = p.get(key) if key else None
            out.append({"value": value, "label": format_point(dim, value, p["name"]),
                        "cell": {"variant": p["name"]}, "reference": False})
        # The reference configuration (the main campaign's values); its cells
        # are in this campaign only when the manifest has a reference point.
        out.append({"value": ref_value,
                    "label": format_point(dim, ref_value, "") if ref_value is not None else "",
                    "cell": {"variant": reference["name"]} if reference else None,
                    "reference": True})
        dims.append({"name": dim, "label": info.get("label", dim),
                     "unit": info.get("unit", "" if info else (key or "")), "points": out})
    return {"dimensions": dims}


def experiments_of(d):
    """[(target, benchmark, configuration)], {key: command}, metadata, inferred."""
    experiments, meta = [], None
    seen, commands = set(), {}
    paths = [os.path.join(d, "EXPERIMENT_METADATA.json")]
    paths += sorted(glob.glob(os.path.join(d, "EXPERIMENT_RERUN_METADATA_*.json")))
    for path in paths:
        data = read_json(path)
        if not isinstance(data, dict):
            continue
        if meta is None:
            meta = data
        for e in data.get("experiments") or []:
            try:
                key = (e.get("target") or "baseline", e["benchmark"], e["configuration"])
            except (KeyError, TypeError, AttributeError):
                continue
            if key not in seen:
                seen.add(key)
                experiments.append(key)
                if isinstance(e.get("command"), list):
                    commands[key] = e["command"]
    inferred = not experiments
    if inferred:
        pattern = re.compile(r"^([^_]+)_([^_]+)_(.+?)_(metrics\.csv|result\.json|out\.stdout)$")
        for f in sorted(os.listdir(d)):
            m = pattern.match(f)
            if m and m.group(1, 2, 3) not in seen:
                seen.add(m.group(1, 2, 3))
                experiments.append(m.group(1, 2, 3))
    return experiments, commands, meta or {}, inferred


def campaign_cells(d):
    d = os.path.normpath(os.path.expanduser(d))
    if not os.path.isdir(d):
        raise SystemExit("exp_cells: no such directory: %s" % d)
    experiments, commands_of, meta, inferred = experiments_of(d)
    manifest = read_json(os.path.join(d, "SENSITIVITY_CAMPAIGN.json"))
    manifest = manifest if isinstance(manifest, dict) else None

    modes = list(MODES)
    points = set()
    if manifest:
        modes += [m for m in manifest.get("mechanisms_per_point") or [] if m not in modes]
        points = {p.get("name") for p in manifest.get("points") or [] if isinstance(p, dict)}

    # Cells keyed (workload, variant, config): a sensitivity configuration
    # "<point>_<mechanism>" becomes (point, mechanism); an LLM configuration
    # "<op>_<mechanism>" without a manifest becomes workload "<benchmark>_<op>".
    # Commands of one benchmark (all its configurations), enough to tell
    # whether two campaigns ran the same binary flags: aes when it ran.
    benches = [k[1] for k in experiments if k in commands_of]
    cmd_bench = ("aes" if "aes" in benches else benches[0]) if benches else None

    cells, warnings, folded = {}, [], False
    for target, bench, config in experiments:
        status, reason, total, wgs = evaluate(os.path.join(d, "%s_%s_%s" % (target, bench, config)))
        variant, mode = split_configuration(config, modes)
        workload, fold = bench, False
        if variant and variant not in points:
            if manifest:
                warnings.append("configuration %s is not a point of SENSITIVITY_CAMPAIGN.json" % config)
            else:
                workload, variant, folded, fold = "%s_%s" % (bench, variant), "", True, True
        cell = {"key": {"workload": workload, "variant": variant, "config": mode},
                "status": status, "metrics": {}}
        if reason:
            cell["reason"] = reason
        if total is not None:
            cell["metrics"]["total_time"] = total
        if wgs is not None:
            cell["metrics"]["wgs"] = wgs
        cmd = commands_of.get((target, bench, config)) if bench == cmd_bench else None
        # LLM operators: no per-operator command comparison.
        if cmd and not fold:
            cell["command"] = [str(a) for a in cmd[1:] if not str(a).startswith(OUTPUT_FLAGS)]
        cells[(workload, variant, mode)] = cell

    keys = list(cells)
    workloads = order_workloads(list((manifest or {}).get("benchmarks") or []) + [k[0] for k in keys])
    variant_order = [p.get("name") for p in (manifest or {}).get("points") or [] if isinstance(p, dict)]
    variants = list(dict.fromkeys([v for v in variant_order if any(k[1] == v for k in keys)]
                                  + [k[1] for k in keys]))
    present = {k[2] for k in keys}
    known = [m for m in MODE_ORDER if m in present]
    configs = known + sorted(present - set(known))
    if inferred:
        warnings.append("no EXPERIMENT_METADATA.json: cells inferred from file names")

    binaries = read_json(os.path.join(d, "EXPERIMENT_BINARIES.json"))
    digests = (binaries.get("sha256_by_target") or {}) if isinstance(binaries, dict) else {}
    binary = (digests.get("baseline") or next(iter(digests.values()), "")) if digests else ""

    kind = "sensitivity" if manifest else "llm" if folded else "main"
    out_meta = {k: meta.get(k) for k in ("experiment_count", "created_at", "git_revision")}
    out_meta["kind"] = kind
    if manifest:
        out_meta["sweep_flags"] = SWEEP_FLAGS
    if folded:
        out_meta["notes"] = ["LLM: per-operator speedups; the whole-model speedup weights operators "
                             "by their repeat counts (cupath_llm_summary.csv) and differs from this GMEAN"]
    labels = {"workload": {w: workload_label(w) for w in workloads},
              "config": {m: MODE_LABELS[m] for m in configs if m in MODE_LABELS}}
    return {
        "schema": SCHEMA,
        "dimensions": ["workload", "variant", "config"],
        "labels": labels,
        "order": {"workload": workloads, "variant": variants, "config": configs},
        "cells": list(cells.values()),
        "sweep": sweep_of(manifest) if manifest else None,
        "total": len(cells),
        "warnings": warnings,
        "binary": str(binary)[:12],
        "meta": out_meta,
    }


def main(argv):
    if len(argv) != 2:
        sys.stderr.write("usage: exp_cells.py <campaign-dir>\n")
        return 2
    json.dump(campaign_cells(argv[1]), sys.stdout)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
