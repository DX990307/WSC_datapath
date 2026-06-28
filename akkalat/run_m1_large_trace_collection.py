#!/usr/bin/env python3
"""Collect large memory-path traces for legacy offline M1 analysis."""

from __future__ import annotations

import argparse
from datetime import datetime
from pathlib import Path
import shlex
import subprocess
import sys


ROOT_DIR = Path(__file__).resolve().parent
REPO_ROOT = ROOT_DIR.parent
UNICODE_DASH_TRANSLATION = str.maketrans({
    "\u2013": "-",
    "\u2014": "-",
    "\u2212": "-",
})


def normalize_option_dashes(argv: list[str]) -> list[str]:
    normalized = []
    for arg in argv:
        if not arg.startswith(("-", "\u2013", "\u2014", "\u2212")):
            normalized.append(arg)
            continue
        if "=" in arg:
            name, value = arg.split("=", 1)
            normalized.append(f"{name.translate(UNICODE_DASH_TRANSLATION)}={value}")
        else:
            normalized.append(arg.translate(UNICODE_DASH_TRANSLATION))
    return normalized


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Run large per-benchmark memory-path traces for legacy offline M1 "
            "analysis. Use run_m1_clean_compare.py for current Mechanism 1."
        )
    )
    parser.add_argument(
        "--benchmarks",
        default="traditional",
        help="Comma-separated runall2 benchmarks or presets, e.g. traditional, llm, all.",
    )
    parser.add_argument(
        "--configs",
        default="baseline",
        help="runall2 config list. Default: baseline.",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=None,
        help="Trace output directory. Defaults to akkalat/results/<timestamp>-m1-large-trace.",
    )
    parser.add_argument("--max-workers", type=int, default=1)
    parser.add_argument("--timeout-minutes", type=float, default=0)
    parser.add_argument("--warmup-accesses", type=int, default=600000)
    parser.add_argument("--max-records", type=int, default=1000000)
    parser.add_argument("--max-wg", type=int, default=78600)
    parser.add_argument(
        "--max-wg-multiplier",
        type=int,
        default=1,
        help=(
            "Multiply --max-wg before forwarding to runall2.py. "
            "Use 48 to interpret --max-wg as a per-active-GPU target."
        ),
    )
    parser.add_argument(
        "--run-until-max-wg",
        action="store_true",
        help=(
            "Do not stop when the memory-path trace reaches --max-records; "
            "let each benchmark run until --max-wg or natural completion."
        ),
    )
    parser.add_argument(
        "--enable-servers",
        "--enable-server",
        dest="enable_servers",
        action="store_true",
        help="Do not pass --disable-servers to runall2.py.",
    )
    parser.add_argument("--switch-latency", type=int, default=32)
    parser.add_argument("--mmutlb-lookup-latency", type=int, default=80)
    parser.add_argument("--l1v-mshr-entries", type=int, default=160)
    parser.add_argument("--l1v-max-concurrent-trans", type=int, default=160)
    parser.add_argument(
        "--trace-only",
        action="store_true",
        help="Skip M1 offline analysis after trace collection.",
    )
    parser.add_argument(
        "--m1-policies",
        default="fifo,l2_only,dram_only,hlq",
        help="Policies passed to run_m1_experiments.py.",
    )
    parser.add_argument(
        "--m1-window-sizes",
        default="16,32,64,160",
        help="Window sizes passed to run_m1_experiments.py.",
    )
    parser.add_argument(
        "--m1-max-ages",
        default="unlimited,50,100",
        help="Max ages passed to run_m1_experiments.py.",
    )
    parser.add_argument(
        "--sim-reorder-policy",
        default="",
        help="Optional simulator-side policy for trace collection: none, fifo, or hlq.",
    )
    parser.add_argument(
        "--sim-reorder-window",
        type=int,
        default=0,
        help="Optional simulator-side L1V bottom reorder queue window.",
    )
    parser.add_argument(
        "--sim-reorder-max-age-ns",
        type=int,
        default=-1,
        help="Optional simulator-side max age ns. -1 leaves default; 0 means unlimited.",
    )
    parser.add_argument(
        "--extra-runall-flags",
        default="",
        help="Extra args forwarded to runall2.py.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print commands without running them.",
    )
    return parser


def default_output_dir() -> Path:
    stamp = datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
    return ROOT_DIR / "results" / f"{stamp}-m1-large-trace"


def build_runall_cmd(args: argparse.Namespace, out_dir: Path) -> list[str]:
    forwarded_max_wg = args.max_wg * args.max_wg_multiplier
    cmd = [
        sys.executable,
        str(ROOT_DIR / "runall2.py"),
        "--benchmarks",
        args.benchmarks,
        "--configs",
        args.configs,
        "--output-dir",
        str(out_dir),
        "--max-workers",
        str(args.max_workers),
        "--switch-latency",
        str(args.switch_latency),
        "--mmutlb-lookup-latency",
        str(args.mmutlb_lookup_latency),
        "--max-wg",
        str(forwarded_max_wg),
        "--l1v-mshr-entries",
        str(args.l1v_mshr_entries),
        "--l1v-max-concurrent-trans",
        str(args.l1v_max_concurrent_trans),
        "--trace-memory-path",
        "--trace-memory-path-warmup-accesses",
        str(args.warmup_accesses),
        "--trace-memory-path-max-records",
        str(args.max_records),
    ]
    if not args.enable_servers:
        cmd.append("--disable-servers")
    if not args.run_until_max_wg:
        cmd.append("--trace-memory-path-exit-on-complete")
    if args.timeout_minutes > 0:
        cmd += ["--timeout-minutes", str(args.timeout_minutes)]
    if args.sim_reorder_policy:
        cmd += ["--l1v-bottom-reorder-policy", args.sim_reorder_policy]
    if args.sim_reorder_window > 0:
        cmd += ["--l1v-bottom-reorder-window", str(args.sim_reorder_window)]
    if args.sim_reorder_max_age_ns >= 0:
        cmd += [
            "--l1v-bottom-reorder-max-age-ns",
            str(args.sim_reorder_max_age_ns),
        ]
    if args.extra_runall_flags:
        cmd += shlex.split(args.extra_runall_flags)
    if args.dry_run:
        cmd.append("--dry-run")
    return cmd


def build_m1_cmd(args: argparse.Namespace, out_dir: Path) -> list[str]:
    m1_dir = out_dir / "m1"
    return [
        sys.executable,
        str(ROOT_DIR / "run_m1_experiments.py"),
        "--result-dir",
        str(out_dir),
        "--out-dir",
        str(m1_dir),
        "--policies",
        args.m1_policies,
        "--window-sizes",
        args.m1_window_sizes,
        "--max-ages",
        args.m1_max_ages,
    ]


def run_cmd(title: str, cmd: list[str], dry_run: bool) -> int:
    print(f"\n=== {title} ===", flush=True)
    print(shlex.join(cmd), flush=True)
    if dry_run:
        return 0
    return subprocess.call(cmd, cwd=REPO_ROOT)


def main() -> int:
    args = build_arg_parser().parse_args(normalize_option_dashes(sys.argv[1:]))
    out_dir = (args.output_dir or default_output_dir()).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    ret = run_cmd(
        "collect large memory-path traces",
        build_runall_cmd(args, out_dir),
        args.dry_run,
    )
    if ret != 0 or args.trace_only:
        return ret

    ret = run_cmd("run M1 offline analysis", build_m1_cmd(args, out_dir), args.dry_run)
    if ret != 0:
        return ret

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
