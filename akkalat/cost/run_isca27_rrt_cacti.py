#!/usr/bin/env python3
"""ISCA'27 table C: CACTI 7 estimates for the M1 (T18) tables.

Same method as the HPCA submission (run_full_cacti.py, 95b4f46): CACTI 7.0,
32 nm, ITRS-HP cells and peripherals, UCA, 360 K, the cupath_filter.cfg
template; a cuckoo fingerprint array is modeled with CACTI's 4-way cache
model (tag = fingerprint, one data byte of state, 2 read ports for the two
candidate buckets + 1 read/write port), plain arrays as scratch RAMs.

Structures, per GPM, for W = 256 / 1024 / 4096 (T18 defaults: table slots
S = 2W, 4 ways, 12-bit fingerprint, waiter pool 4S, owner window = W):

  RRT (remote request tracker, requester side)
    rrt_fp      S slots: 12-bit fingerprint tag + 1 B state (valid/open/sent)
    rrt_addr    S x 8 B: 42-bit line address, 6-bit destination GPM,
                waiter-list head (log2(4S) bits), padded to 64 bits
    rrt_waiter  4S x 4 B: CU (5 b) + L1V MSHR slot (4 b) + next pointer
                (log2(4S) <= 15 b), padded to 32 bits
  CAM (same tracking capacity, fully associative, for comparison)
    cam_W       W entries: 42-bit line-address tag searched in parallel,
                8 B data (destination, waiter head, state); CACTI cache with
                associativity 0 and one search port. cam_2W has the same slot
                count as the cuckoo table.
  OMT (owner merge table)
    omt_fp      S slots, as rrt_fp
    omt_side    S x 16 B: 42-bit line address + 48-bit requester bitmap +
                4 state bits (open, multicast, live, valid), padded to 128 b

Run: run_isca27_rrt_cacti.py --cacti <path to cacti binary> --output-dir DIR
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import re
import subprocess
from dataclasses import asdict, dataclass, replace
from pathlib import Path

from run_full_cacti import replace_setting


@dataclass(frozen=True)
class Array:
    name: str
    group: str
    W: int
    entries: int
    capacity_bytes: int
    block_bytes: int
    output_bits: int
    banks: int
    cache_type: str = "ram"
    associativity: int = 1
    read_write_ports: int = 1
    read_ports: int = 0
    write_ports: int = 0
    search_ports: int = 0
    tag_bits: int | None = None


def structures(ws=(256, 1024, 4096)):
    # Reference: one mi200 L2 slice (256 KiB, 16-way, 64-B lines; 8 per
    # GPM), banked like the HPCA 1-MiB partition (64 KiB per bank).
    out = [Array("l2_slice_mi200", "L2", 0, 4096, 262_144, 64, 512, 4,
                 "cache", 16)]
    for w in ws:
        s = 2 * w
        fp_banks = max(1, s // 2048)  # HPCA filter: 2K slots per bank
        out += [
            Array(f"rrt_fp_W{w}", "RRT", w, s, s, 1, 8, fp_banks, "cache", 4,
                  1, 2, 0, 0, 12),
            Array(f"rrt_addr_W{w}", "RRT", w, s, s * 8, 8, 64, 1),
            Array(f"rrt_waiter_W{w}", "RRT", w, 4 * s, 4 * s * 4, 4, 32, 1),
            Array(f"cam_W{w}", "CAM", w, w, w * 8, 8, 64, 1, "cache", 0,
                  1, 0, 0, 1, 42),
            Array(f"cam2_W{w}", "CAM2", w, s, s * 8, 8, 64, 1, "cache", 0,
                  1, 0, 0, 1, 42),
            Array(f"omt_fp_W{w}", "OMT", w, s, s, 1, 8, fp_banks, "cache", 4,
                  1, 2, 0, 0, 12),
            Array(f"omt_side_W{w}", "OMT", w, s, s * 16, 16, 128, 1),
        ]
    return out


def render(template: str, a: Array) -> str:
    text = template
    settings = {
        "size (bytes)": str(a.capacity_bytes),
        "block size (bytes)": str(a.block_bytes),
        "associativity": str(a.associativity),
        "read-write port": str(a.read_write_ports),
        "exclusive read port": str(a.read_ports),
        "exclusive write port": str(a.write_ports),
        "UCA bank count": str(a.banks),
        "technology (u)": "0.032",
        "output/input bus width": str(a.output_bits),
        "cache type": f'"{a.cache_type}"',
    }
    if a.tag_bits is not None:
        settings["tag size (b)"] = str(a.tag_bits)
    for k, v in settings.items():
        text = replace_setting(text, k, v)
    if a.search_ports:
        text = re.sub(r"(?m)^-single ended read ports.*$",
                      lambda m: m.group(0) + f"\n-search port {a.search_ports}",
                      text, count=1)
    return text


def parse(path: Path) -> dict:
    rows = list(csv.reader(path.open(newline="")))
    head = [h.strip() for h in rows[0]]
    row = dict(zip(head, [v.strip() for v in rows[-1]]))

    def num(k):
        v = row.get(k, "N/A")
        return float(v) if v not in ("N/A", "") else None

    return {
        "access_time_ns": num("Access time (ns)"),
        "cycle_time_ns": num("Random cycle time (ns)"),
        "search_energy_nj": num("Dynamic search energy (nJ)"),
        "read_energy_nj": num("Dynamic read energy (nJ)"),
        "write_energy_nj": num("Dynamic write energy (nJ)"),
        "leakage_mw_per_bank": num("Standby leakage per bank(mW)"),
        "area_mm2": num("Area (mm2)"),
    }


def run_one(cacti: Path, outdir: Path, template: str, a: Array):
    """Run CACTI; if it finds no organization, retry with more banks."""
    tried = []
    for banks in [a.banks, a.banks * 2, a.banks * 4]:
        b = replace(a, banks=banks)
        cfg = (outdir / f"{a.name}.cfg").resolve()
        cfg.write_text(render(template, b))
        out = Path(f"{cfg}.out")
        out.unlink(missing_ok=True)
        p = subprocess.run([str(cacti), "-infile", str(cfg)], cwd=cacti.parent,
                           capture_output=True, text=True)
        tried.append(banks)
        if p.returncode == 0 and out.is_file():
            (outdir / f"{a.name}_output.txt").write_text(out.read_text())
            return b, parse(out), tried
    return a, None, tried


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cacti", type=Path, required=True)
    ap.add_argument("--template", type=Path,
                    default=Path(__file__).with_name("cacti32") / "cupath_filter.cfg")
    ap.add_argument("--output-dir", type=Path,
                    default=Path(__file__).with_name("cacti_isca27"))
    args = ap.parse_args()
    cacti = args.cacti.resolve()
    template = args.template.read_text()
    args.output_dir.mkdir(parents=True, exist_ok=True)

    rows = []
    for a in structures():
        used, m, tried = run_one(cacti, args.output_dir, template, a)
        row = asdict(used) | {"banks_tried": " ".join(map(str, tried))}
        if m is None:
            row["error"] = "no valid organization"
        else:
            row |= m
            row["leakage_mw"] = (m["leakage_mw_per_bank"] or 0) * used.banks
        rows.append(row)
        print(json.dumps(row))

    keys = []
    for r in rows:
        keys += [k for k in r if k not in keys]
    with (args.output_dir / "rrt_cacti_results.csv").open("w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=keys)
        w.writeheader()
        w.writerows(rows)


if __name__ == "__main__":
    main()
