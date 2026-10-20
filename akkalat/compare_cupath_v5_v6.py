#!/usr/bin/env python3
"""Compare invalid pre-dispatch V5 speedups with runtime-stop V6 results."""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path


CONFIGS = ("m1", "m2", "m3", "complete")


def geomean(values) -> float:
    values = list(values)
    if not values or any(value <= 0 for value in values):
        raise ValueError("speedups must be positive")
    return math.exp(sum(math.log(value) for value in values) / len(values))


def read_table(path: Path) -> dict[str, dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as stream:
        return {
            row["benchmark"]: row
            for row in csv.DictReader(stream)
            if row["benchmark"] != "geomean_14"
        }


def compare(v5_path: Path, v6_path: Path) -> list[dict[str, object]]:
    v5 = read_table(v5_path)
    v6 = read_table(v6_path)
    if set(v5) != set(v6) or len(v6) != 14:
        raise ValueError("V5/V6 speedup tables do not cover the same 14 workloads")
    rows: list[dict[str, object]] = []
    for benchmark in v6:
        label = v6[benchmark].get("label", benchmark)
        for config in CONFIGS:
            old = float(v5[benchmark][f"{config}_speedup"])
            new = float(v6[benchmark][f"{config}_speedup"])
            old_gain = old - 1.0
            invalid_excess = old - new
            rows.append({
                "benchmark": benchmark,
                "label": label,
                "configuration": config,
                "invalid_v5_speedup": old,
                "runtime_stop_v6_speedup": new,
                "v6_over_v5_ratio": new / old,
                "invalid_v5_excess_speedup": invalid_excess,
                "invalid_excess_fraction_of_v5_gain": (
                    invalid_excess / old_gain if old_gain > 0 else ""
                ),
            })
    return rows


def write_outputs(output: Path, rows: list[dict[str, object]]) -> None:
    output.mkdir(parents=True, exist_ok=True)
    csv_path = output / "cupath_v5_v6_comparison.csv"
    with csv_path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=tuple(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    fir = [
        row for row in rows
        if row["benchmark"] == "fir" and row["configuration"] == "complete"
    ][0]
    fraction = fir["invalid_excess_fraction_of_v5_gain"]
    fraction_text = "not defined"
    if fraction != "":
        fraction_text = f"{100.0 * float(fraction):.2f}%"
    geomeans = {}
    for config in CONFIGS:
        config_rows = [
            row for row in rows if row["configuration"] == config
        ]
        if len(config_rows) != 14:
            raise ValueError(f"missing V5/V6 rows for {config}")
        geomeans[config] = (
            geomean(float(row["invalid_v5_speedup"]) for row in config_rows),
            geomean(
                float(row["runtime_stop_v6_speedup"])
                for row in config_rows
            ),
        )
    largest_excess = sorted(
        rows,
        key=lambda row: float(row["invalid_v5_excess_speedup"]),
        reverse=True,
    )[:10]
    lines = [
        "# CuPath invalid-V5 versus runtime-stop-V6 comparison",
        "",
        "V5 is retained only as a methodological comparison. Its Driver-side "
        "pre-dispatch limiter changed unified-GPU placement and is not a "
        "paper result.",
        "",
        "## Fourteen-workload geomean",
        "",
        "| Configuration | Invalid V5 | Runtime-stop V6 | V6 / V5 |",
        "|---|---:|---:|---:|",
    ]
    for config in CONFIGS:
        old, new = geomeans[config]
        lines.append(
            f"| {config.upper()} | {old:.4f}x | {new:.4f}x | "
            f"{new / old:.4f}x |"
        )
    lines += [
        "",
        "## FIR Complete",
        "",
        f"- Invalid V5 speedup: {float(fir['invalid_v5_speedup']):.4f}x",
        f"- Runtime-stop V6 speedup: {float(fir['runtime_stop_v6_speedup']):.4f}x",
        f"- V5 excess relative to V6: {float(fir['invalid_v5_excess_speedup']):.4f}x",
        f"- V5 excess as a fraction of its reported gain above 1x: {fraction_text}",
        "",
        "The last quantity is a controlled result-difference attribution, not "
        "a decomposition of microarchitectural latency. Per-workload and "
        "per-mechanism differences are preserved in "
        "`cupath_v5_v6_comparison.csv`.",
        "",
        "## Largest invalid-V5 excesses",
        "",
        "| Benchmark | Configuration | Invalid V5 | Runtime-stop V6 | V5 excess |",
        "|---|---|---:|---:|---:|",
    ]
    for row in largest_excess:
        lines.append(
            f"| {row['label']} | {str(row['configuration']).upper()} | "
            f"{float(row['invalid_v5_speedup']):.4f}x | "
            f"{float(row['runtime_stop_v6_speedup']):.4f}x | "
            f"{float(row['invalid_v5_excess_speedup']):.4f}x |"
        )
    (output / "CUPATH_V5_V6_COMPARISON.md").write_text(
        "\n".join(lines) + "\n", encoding="utf-8"
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("v5", type=Path)
    parser.add_argument("v6", type=Path)
    parser.add_argument("--output-dir", type=Path)
    args = parser.parse_args()
    output = (args.output_dir or args.v6).resolve()
    rows = compare(
        args.v5.resolve() / "cupath_speedup_table.csv",
        args.v6.resolve() / "cupath_speedup_table.csv",
    )
    write_outputs(output, rows)
    print(output / "CUPATH_V5_V6_COMPARISON.md")


if __name__ == "__main__":
    main()
