#!/usr/bin/env python3
"""Run paired baseline vs simulator-side M1 HLQ experiments."""

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
        description="Run baseline and simulator-side HLQ, then compare datapath evidence."
    )
    parser.add_argument("--benchmarks", default="traditional")
    parser.add_argument("--configs", default="baseline")
    parser.add_argument("--output-root", type=Path, default=None)
    parser.add_argument("--max-workers", type=int, default=1)
    parser.add_argument("--timeout-minutes", type=float, default=0)
    parser.add_argument("--warmup-accesses", type=int, default=600000)
    parser.add_argument("--max-records", type=int, default=1000000)
    parser.add_argument("--max-wg", type=int, default=78600)
    parser.add_argument(
        "--run-until-max-wg",
        action="store_true",
        help=(
            "Do not stop when the memory-path trace reaches --max-records; "
            "let the benchmark run until --max-wg or natural completion."
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
    parser.add_argument("--hlq-window", type=int, default=64)
    parser.add_argument("--hlq-max-age-ns", type=int, default=100)
    parser.add_argument("--skip-baseline", action="store_true")
    parser.add_argument("--skip-hlq", action="store_true")
    parser.add_argument("--skip-compare", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser


def default_output_root() -> Path:
    stamp = datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
    return ROOT_DIR / "results" / f"{stamp}-m1-baseline-vs-hlq"


def runall_base_cmd(args: argparse.Namespace, out_dir: Path) -> list[str]:
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
        str(args.max_wg),
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
    return cmd


def baseline_cmd(args: argparse.Namespace, out_dir: Path) -> list[str]:
    return runall_base_cmd(args, out_dir)


def hlq_cmd(args: argparse.Namespace, out_dir: Path) -> list[str]:
    cmd = runall_base_cmd(args, out_dir)
    cmd += [
        "--l1v-bottom-reorder-policy",
        "hlq",
        "--l1v-bottom-reorder-window",
        str(args.hlq_window),
        "--l1v-bottom-reorder-max-age-ns",
        str(args.hlq_max_age_ns),
    ]
    return cmd


def compare_cmd(baseline_dir: Path, hlq_dir: Path, out_dir: Path) -> list[str]:
    return [
        sys.executable,
        str(ROOT_DIR / "compare_m1_sim_datapath.py"),
        "--baseline-dir",
        str(baseline_dir),
        "--experiment-dir",
        str(hlq_dir),
        "--out-dir",
        str(out_dir),
    ]


def run_cmd(title: str, cmd: list[str], dry_run: bool) -> int:
    print(f"\n=== {title} ===", flush=True)
    print(shlex.join(cmd), flush=True)
    if dry_run:
        return 0
    return subprocess.call(cmd, cwd=REPO_ROOT)


def main() -> int:
    args = build_arg_parser().parse_args(normalize_option_dashes(sys.argv[1:]))
    output_root = (args.output_root or default_output_root()).resolve()
    baseline_dir = output_root / "baseline"
    hlq_dir = output_root / f"hlq_w{args.hlq_window}_age{args.hlq_max_age_ns}"
    compare_dir = output_root / "compare"
    output_root.mkdir(parents=True, exist_ok=True)

    if not args.skip_baseline:
        ret = run_cmd("baseline trace run", baseline_cmd(args, baseline_dir), args.dry_run)
        if ret != 0:
            return ret

    if not args.skip_hlq:
        ret = run_cmd("HLQ trace run", hlq_cmd(args, hlq_dir), args.dry_run)
        if ret != 0:
            return ret

    if args.skip_compare:
        return 0

    return run_cmd(
        "compare datapath evidence",
        compare_cmd(baseline_dir, hlq_dir, compare_dir),
        args.dry_run,
    )


if __name__ == "__main__":
    raise SystemExit(main())
