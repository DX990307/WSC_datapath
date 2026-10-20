#!/usr/bin/env python3
"""Reproduce the complete CuPath SRAM-array area budget with CACTI 7.0.

CACTI estimates only regular arrays and their configured ports.  The generated
table deliberately keeps the optional coherence directory separate and does
not silently turn the result into a post-layout logic-area claim.
"""

from __future__ import annotations

import argparse
import csv
import json
import re
import shutil
import subprocess
from dataclasses import asdict, dataclass
from pathlib import Path


@dataclass(frozen=True)
class Structure:
    name: str
    description: str
    count_per_gpm: int
    capacity_bytes: int
    block_bytes: int
    output_bits: int
    banks: int
    cache_type: str = "ram"
    associativity: int = 1
    read_write_ports: int = 1
    read_ports: int = 0
    write_ports: int = 0
    category: str = "required"
    tag_bits: int | None = None
    accounted_capacity_bytes: int | None = None


STRUCTURES = (
    Structure(
        "l2_partition", "Baseline 1-MiB L2 data and tag array", 4,
        1_048_576, 64, 512, 16, "cache", 16,
    ),
    Structure(
        "typed_filter", "CACTI cache-style proxy for 8K four-slot buckets", 4,
        # Model each slot as a 16-bit fingerprint/type tag and one byte of
        # valid/reference data.  This rounds the implemented 21-bit slot up to
        # 24 bits and makes CACTI include the four-way tag comparators and
        # selection logic. Two read ports cover the two candidate buckets;
        # the read/write port covers updates and relocations.
        32_768, 1, 8, 16, "cache", 4,
        read_write_ports=1, read_ports=2, write_ports=0, tag_bits=16,
        accounted_capacity_bytes=86_016,
    ),
    Structure(
        "remote_predictor", "256-entry requester-RDMA predictor at 64 B/entry",
        1, 16_384, 64, 512, 1,
    ),
    Structure(
        "local_pair_history", "64 16-B region records per L2 partition", 4,
        1_024, 16, 128, 1,
    ),
    Structure(
        "m1_response_data", "16 returned 64-B lines per L2 partition", 4,
        1_024, 64, 512, 1,
    ),
    Structure(
        "m1_response_metadata", "16 16-B tag/control entries per response buffer",
        4, 256, 16, 128, 1,
    ),
    Structure(
        "rdma_line_table", "1024-entry exact remote-line table at 32 B/entry", 1,
        32_768, 32, 256, 8,
    ),
    Structure(
        "rdma_waiter_table", "1024-entry bounded waiter table at 16 B/entry", 1,
        16_384, 16, 128, 8,
    ),
    Structure(
        "requester_batch_table", "64 requester page descriptors and bitmaps at 32 B/entry",
        1, 2_048, 32, 256, 8,
    ),
    Structure(
        "owner_child_table", "512 owner-side child descriptors at 16 B/entry", 1,
        8_192, 16, 128, 8,
    ),
    Structure(
        "owner_batch_table", "64 owner page descriptors at 32 B/entry", 1,
        2_048, 32, 256, 8,
    ),
    Structure(
        "requester_response_data", "64 packets times eight returned cachelines", 1,
        # The requester line table permits twice MaxBatches*MaxBatchLines.
        # Provision one returned line for every such entry.
        65_536, 64, 512, 8,
    ),
    Structure(
        "owner_response_data", "64 packets times eight returned cachelines", 1,
        32_768, 64, 512, 8,
    ),
    Structure(
        "remote_line_state", "Remote and reuse-state bits for 16K L2 lines", 4,
        4_096, 8, 8, 1,
    ),
)


def replace_setting(text: str, key: str, value: str) -> str:
    pattern = re.compile(rf"(?m)^-{re.escape(key)}.*$")
    replacement = f"-{key} {value}"
    text, count = pattern.subn(replacement, text, count=1)
    if count != 1:
        raise RuntimeError(f"unable to replace CACTI setting {key!r}")
    return text


def render_config(template: str, item: Structure) -> str:
    text = template
    settings = {
        "size (bytes)": str(item.capacity_bytes),
        "block size (bytes)": str(item.block_bytes),
        "associativity": str(item.associativity),
        "read-write port": str(item.read_write_ports),
        "exclusive read port": str(item.read_ports),
        "exclusive write port": str(item.write_ports),
        "UCA bank count": str(item.banks),
        "technology (u)": "0.032",
        "output/input bus width": str(item.output_bits),
        "cache type": f'"{item.cache_type}"',
    }
    if item.tag_bits is not None:
        settings["tag size (b)"] = str(item.tag_bits)
    for key, value in settings.items():
        text = replace_setting(text, key, value)
    return text


def parse_cacti_output(path: Path) -> dict[str, float]:
    with path.open(newline="", encoding="utf-8") as handle:
        rows = list(csv.reader(handle))
    if len(rows) < 2:
        raise RuntimeError(f"CACTI produced no result row in {path}")
    header = [field.strip() for field in rows[0]]
    values = [field.strip() for field in rows[-1]]
    row = dict(zip(header, values))
    return {
        "access_time_ns": float(row["Access time (ns)"]),
        "cycle_time_ns": float(row["Random cycle time (ns)"]),
        "read_energy_nj": float(row["Dynamic read energy (nJ)"]),
        "write_energy_nj": float(row["Dynamic write energy (nJ)"]),
        "area_mm2": float(row["Area (mm2)"]),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cacti", type=Path, required=True)
    parser.add_argument(
        "--template", type=Path,
        default=Path(__file__).with_name("cacti32") / "cupath_filter.cfg",
    )
    parser.add_argument(
        "--output-dir", type=Path,
        default=Path(__file__).with_name("cacti_full"),
    )
    args = parser.parse_args()

    cacti = args.cacti.resolve()
    if not cacti.is_file():
        raise SystemExit(f"CACTI executable not found at {cacti}")
    template = args.template.read_text(encoding="utf-8")
    args.output_dir.mkdir(parents=True, exist_ok=True)

    expected_stems = {item.name for item in STRUCTURES}
    for old_config in args.output_dir.glob("*.cfg"):
        if old_config.stem in expected_stems:
            continue
        old_config.unlink()
        Path(f"{old_config}.out").unlink(missing_ok=True)
        (args.output_dir / f"{old_config.stem}_output.txt").unlink(
            missing_ok=True
        )

    results = []
    for item in STRUCTURES:
        config_path = (args.output_dir / f"{item.name}.cfg").resolve()
        config_path.write_text(render_config(template, item), encoding="utf-8")
        subprocess.run(
            [str(cacti), "-infile", str(config_path)],
            cwd=cacti.parent,
            check=True,
            stdout=subprocess.DEVNULL,
        )
        generated = Path(f"{config_path}.out")
        if not generated.is_file():
            raise RuntimeError(f"CACTI did not create {generated}")
        preserved = args.output_dir / f"{item.name}_output.txt"
        shutil.copyfile(generated, preserved)
        measured = parse_cacti_output(preserved)
        row = asdict(item) | measured
        row["area_per_gpm_mm2"] = measured["area_mm2"] * item.count_per_gpm
        row["capacity_per_gpm_bytes"] = (
            (item.accounted_capacity_bytes or item.capacity_bytes) *
            item.count_per_gpm
        )
        results.append(row)

    csv_path = args.output_dir / "cacti_full_results.csv"
    with csv_path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(results[0]))
        writer.writeheader()
        writer.writerows(results)

    l2_area = next(
        row["area_per_gpm_mm2"] for row in results
        if row["name"] == "l2_partition"
    )
    required = sum(
        row["area_per_gpm_mm2"] for row in results
        if row["category"] == "required"
        and row["name"] != "l2_partition"
    )
    summary = {
        "cacti_executable": str(cacti),
        "l2_area_per_gpm_mm2": l2_area,
        "required_cupath_array_area_per_gpm_mm2": required,
        "required_array_overhead_percent_of_l2": 100 * required / l2_area,
        "required_capacity_per_gpm_bytes": sum(
            row["capacity_per_gpm_bytes"] for row in results
            if row["category"] == "required"
            and row["name"] != "l2_partition"
        ),
    }
    (args.output_dir / "cacti_full_summary.json").write_text(
        json.dumps(summary, indent=2) + "\n", encoding="utf-8"
    )
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
