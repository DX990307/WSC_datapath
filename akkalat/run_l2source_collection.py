#!/usr/bin/env python3
"""Run a two-stage L2-source data collection sweep.

Stage 1 runs the regular runall2 benchmark set with -report-l2-source.
Stage 2 runs the LLM wrapper workloads, also with -report-l2-source.
"""

import argparse
from pathlib import Path
import shlex
import subprocess
import sys


ROOT_DIR = Path(__file__).resolve().parent

DEFAULT_RUNALL_BENCHMARKS = "traditional-lite"

DEFAULT_LLM_BENCHMARKS = "resnet,bert,gpt"


def parse_args():
    parser = argparse.ArgumentParser(
        description=(
            "Collect L2 source CSVs by running runall2 first and runllm second."
        )
    )
    parser.add_argument(
        "--skip-runall",
        action="store_true",
        help="Skip the regular runall2 stage.",
    )
    parser.add_argument(
        "--skip-llm",
        action="store_true",
        help="Skip the LLM stage.",
    )
    parser.add_argument(
        "--runall-benchmarks",
        default=DEFAULT_RUNALL_BENCHMARKS,
        help=(
            "Comma-separated runall2 benchmark list. Presets include "
            "'traditional-lite', 'traditional', and 'all'."
        ),
    )
    parser.add_argument(
        "--llm-benchmarks",
        default=DEFAULT_LLM_BENCHMARKS,
        help="Comma-separated LLM benchmark list for runllm.py.",
    )
    parser.add_argument(
        "--runall-configs",
        default="baseline",
        help="Config list for the runall2 stage.",
    )
    parser.add_argument(
        "--llm-configs",
        default="baseline",
        help="Config list for the runllm stage.",
    )
    parser.add_argument(
        "--llm-profile",
        default="small",
        help="runllm.py profile, e.g. tiny, small, middle, 512mb.",
    )
    parser.add_argument(
        "--runall-max-workers",
        type=int,
        default=1,
        help="Parallel workers for the runall2 stage.",
    )
    parser.add_argument(
        "--llm-max-workers",
        type=int,
        default=1,
        help="Parallel workers for the runllm stage.",
    )
    parser.add_argument(
        "--timeout-minutes",
        type=float,
        default=0,
        help="Per-experiment timeout. 0 disables timeout.",
    )
    parser.add_argument(
        "--l2-source-tile-width",
        type=int,
        default=7,
        help="Tile-array width used to compute requester/provider hop count.",
    )
    parser.add_argument(
        "--extra-runall-flags",
        default="",
        help="Extra benchmark-binary flags passed through runall2.",
    )
    parser.add_argument(
        "--extra-llm-flags",
        default="",
        help="Extra benchmark-binary flags appended after the LLM profile flags.",
    )
    parser.add_argument(
        "--sampled-warmups",
        default="",
        help="Forward sampled warmup list to both stages.",
    )
    parser.add_argument(
        "--sampled-granularities",
        default="",
        help="Forward sampled granularity list to both stages.",
    )
    parser.add_argument(
        "--enable-servers",
        action="store_true",
        help="Do not pass --disable-servers.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print commands without running.",
    )
    return parser.parse_args()


def maybe_add_timeout(cmd, timeout_minutes):
    if timeout_minutes > 0:
        cmd += ["--timeout-minutes", str(timeout_minutes)]


def add_common_collection_flags(cmd, args):
    cmd.append("--report-l2-source")
    cmd += ["--l2-source-tile-width", str(args.l2_source_tile_width)]
    if not args.enable_servers:
        cmd.append("--disable-servers")
    maybe_add_timeout(cmd, args.timeout_minutes)
    if args.sampled_warmups:
        cmd += ["--sampled-warmups", args.sampled_warmups]
    if args.sampled_granularities:
        cmd += ["--sampled-granularities", args.sampled_granularities]
    if args.dry_run:
        cmd.append("--dry-run")


def build_runall_cmd(args):
    cmd = [
        sys.executable,
        str(ROOT_DIR / "runall2.py"),
        "--benchmarks",
        args.runall_benchmarks,
        "--configs",
        args.runall_configs,
        "--max-workers",
        str(args.runall_max_workers),
    ]
    add_common_collection_flags(cmd, args)
    if args.extra_runall_flags:
        cmd += ["--extra-benchmark-flags", args.extra_runall_flags]
    return cmd


def build_llm_cmd(args):
    cmd = [
        sys.executable,
        str(ROOT_DIR / "runllm.py"),
        "--profile",
        args.llm_profile,
        "--benchmarks",
        args.llm_benchmarks,
        "--configs",
        args.llm_configs,
        "--max-workers",
        str(args.llm_max_workers),
    ]
    add_common_collection_flags(cmd, args)
    if args.extra_llm_flags:
        cmd += ["--extra-benchmark-flags", args.extra_llm_flags]
    return cmd


def run_stage(name, cmd, dry_run):
    print(f"\n=== {name} ===", flush=True)
    print(shlex.join(cmd), flush=True)
    if dry_run:
        return 0
    return subprocess.call(cmd, cwd=ROOT_DIR.parent)


def main():
    args = parse_args()

    if args.skip_runall and args.skip_llm:
        print("Nothing to run: both stages are skipped.")
        return 0

    if not args.skip_runall:
        ret = run_stage("runall2 L2-source stage", build_runall_cmd(args), args.dry_run)
        if ret != 0:
            return ret

    if not args.skip_llm:
        ret = run_stage("LLM L2-source stage", build_llm_cmd(args), args.dry_run)
        if ret != 0:
            return ret

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
