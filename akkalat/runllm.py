#!/usr/bin/env python3
"""Run the LLM-style workloads through runall2.py.

This wrapper keeps the execution machinery in runall2.py, but provides
workload-size presets for ResNet, BERT, and GPT. The default preset is a small
debug profile; larger profiles can be selected explicitly when needed.
"""

import argparse
from pathlib import Path
import shlex
import subprocess
import sys


ROOT_DIR = Path(__file__).resolve().parent


PROFILES = {
    "tiny": {
        "description": "Very small smoke test for kernel/opcode validation.",
        "max_wg": 0,
        "flags": [
            "-resnet-mode=block",
            "-resnet-depth=18",
            "-resnet-batch-size=1",
            "-resnet-image-size=4",
            "-bert-mode=block",
            "-bert-size=tiny",
            "-bert-batch-size=1",
            "-bert-seq-len=2",
            "-bert-hidden-size=16",
            "-bert-num-heads=4",
            "-bert-num-layers=1",
            "-bert-intermediate-size=32",
            "-gpt-mode=block",
            "-gpt-size=tiny",
            "-gpt-batch-size=1",
            "-gpt-seq-len=2",
            "-gpt-hidden-size=16",
            "-gpt-num-heads=4",
            "-gpt-num-layers=1",
            "-gpt-intermediate-size=32",
        ],
    },
    "small": {
        "description": (
            "Manageable LLM debug profile. BERT/GPT use one transformer layer "
            "with hidden=1024 and intermediate=4096; ResNet uses a modest "
            "single block."
        ),
        "max_wg": 0,
        "flags": [
            "-resnet-mode=block",
            "-resnet-depth=18",
            "-resnet-batch-size=1",
            "-resnet-image-size=96",
            "-bert-mode=full",
            "-bert-size=custom",
            "-bert-batch-size=1",
            "-bert-seq-len=32",
            "-bert-hidden-size=1024",
            "-bert-num-heads=16",
            "-bert-num-layers=1",
            "-bert-intermediate-size=4096",
            "-gpt-mode=full",
            "-gpt-size=custom",
            "-gpt-batch-size=1",
            "-gpt-seq-len=32",
            "-gpt-hidden-size=1024",
            "-gpt-num-heads=16",
            "-gpt-num-layers=1",
            "-gpt-intermediate-size=4096",
        ],
    },
    "middle": {
        "description": (
            "Middle-size loop-sampling test profile. BERT/GPT use two "
            "transformer layers with seq=64, hidden=512, and intermediate=2048. "
            "This gives more repeated loop opportunities than tiny, while "
            "keeping GEMM much smaller than the 512mb profile."
        ),
        "max_wg": 0,
        "flags": [
            "-resnet-mode=block",
            "-resnet-depth=18",
            "-resnet-batch-size=1",
            "-resnet-image-size=128",
            "-bert-mode=full",
            "-bert-size=custom",
            "-bert-batch-size=1",
            "-bert-seq-len=64",
            "-bert-hidden-size=512",
            "-bert-num-heads=8",
            "-bert-num-layers=2",
            "-bert-intermediate-size=2048",
            "-gpt-mode=full",
            "-gpt-size=custom",
            "-gpt-batch-size=1",
            "-gpt-seq-len=64",
            "-gpt-hidden-size=512",
            "-gpt-num-heads=8",
            "-gpt-num-layers=2",
            "-gpt-intermediate-size=2048",
        ],
    },
    "512mb": {
        "description": (
            "Moderate profile intended to stay well below the previous 4096 "
            "hidden-size setup. BERT/GPT use one transformer layer with "
            "hidden=2048 and intermediate=8192; ResNet uses a medium block."
        ),
        "max_wg": 0,
        "flags": [
            "-resnet-mode=block",
            "-resnet-depth=18",
            "-resnet-batch-size=2",
            "-resnet-image-size=160",
            "-bert-mode=full",
            "-bert-size=custom",
            "-bert-batch-size=1",
            "-bert-seq-len=64",
            "-bert-hidden-size=2048",
            "-bert-num-heads=16",
            "-bert-num-layers=1",
            "-bert-intermediate-size=8192",
            "-gpt-mode=full",
            "-gpt-size=custom",
            "-gpt-batch-size=1",
            "-gpt-seq-len=64",
            "-gpt-hidden-size=2048",
            "-gpt-num-heads=16",
            "-gpt-num-layers=1",
            "-gpt-intermediate-size=8192",
        ],
    },
    "gpt7b-proxy": {
        "description": (
            "GPT-only-ish 7B hidden/intermediate shape, still using one layer "
            "by default to keep simulation manageable."
        ),
        "max_wg": 0,
        "flags": [
            "-resnet-mode=block",
            "-resnet-depth=18",
            "-resnet-batch-size=4",
            "-resnet-image-size=224",
            "-bert-mode=full",
            "-bert-size=custom",
            "-bert-batch-size=1",
            "-bert-seq-len=32",
            "-bert-hidden-size=4096",
            "-bert-num-heads=32",
            "-bert-num-layers=1",
            "-bert-intermediate-size=11008",
            "-gpt-mode=full",
            "-gpt-size=7b-proxy",
            "-gpt-batch-size=1",
            "-gpt-seq-len=32",
            "-gpt-num-layers=1",
        ],
    },
    "custom": {
        "description": "No preset flags; use --extra-benchmark-flags.",
        "max_wg": 0,
        "flags": [],
    },
}


def parse_args():
    parser = argparse.ArgumentParser(
        description=(
            "LLM workload runner based on runall2.py. Unknown arguments are "
            "forwarded to runall2.py."
        )
    )
    parser.add_argument(
        "--profile",
        choices=sorted(PROFILES),
        default="small",
        help="Workload-size preset.",
    )
    parser.add_argument(
        "--list-profiles",
        action="store_true",
        help="Print available profiles and exit.",
    )
    parser.add_argument(
        "--benchmarks",
        default="resnet,bert,gpt",
        help="Comma-separated workload list. Default: resnet,bert,gpt.",
    )
    parser.add_argument(
        "--configs",
        default="baseline",
        help="runall2 config list, e.g. baseline or baseline,sample_all.",
    )
    parser.add_argument(
        "--max-workers",
        type=int,
        default=1,
        help="Maximum concurrent experiments.",
    )
    parser.add_argument(
        "--max-wg",
        type=int,
        default=None,
        help="Override profile -max-wg. Use 0 to disable.",
    )
    parser.add_argument(
        "--sampled-warmups",
        default="",
        help="Forwarded to runall2.py.",
    )
    parser.add_argument(
        "--sampled-granularities",
        default="",
        help="Forwarded to runall2.py.",
    )
    parser.add_argument(
        "--extra-benchmark-flags",
        default="",
        help=(
            "Extra benchmark flags appended after the profile flags, so they "
            "can override the preset."
        ),
    )
    parser.add_argument(
        "--log-subtasks",
        dest="log_subtasks",
        action="store_true",
        default=True,
        help="Print model steps and sub-operators. Enabled by default.",
    )
    parser.add_argument(
        "--no-log-subtasks",
        dest="log_subtasks",
        action="store_false",
        help="Disable model step and sub-operator logging.",
    )
    parser.add_argument(
        "--enable-servers",
        action="store_true",
        help="Do not pass --disable-servers to runall2.py.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print runall2 commands without building or running.",
    )
    return parser.parse_known_args()


def print_profiles():
    for name in sorted(PROFILES):
        profile = PROFILES[name]
        print(f"{name}: {profile['description']}")
        if profile["flags"]:
            print("  " + shlex.join(profile["flags"]))


def build_command(args, forwarded):
    profile = PROFILES[args.profile]
    max_wg = args.max_wg if args.max_wg is not None else profile["max_wg"]

    extra_flags = (
        shlex.split(args.extra_benchmark_flags)
        if args.extra_benchmark_flags
        else []
    )
    log_flags = []
    if args.log_subtasks:
        log_flags = [
            "-resnet-log-subtasks",
            "-bert-log-subtasks",
            "-gpt-log-subtasks",
        ]

    benchmark_flags = profile["flags"] + log_flags + extra_flags

    cmd = [
        sys.executable,
        str(ROOT_DIR / "runall2.py"),
        "--benchmarks",
        args.benchmarks,
        "--configs",
        args.configs,
        "--max-workers",
        str(args.max_workers),
        "--extra-benchmark-flags",
        shlex.join(benchmark_flags),
    ]

    if not args.enable_servers:
        cmd.append("--disable-servers")
    if max_wg > 0:
        cmd += ["--max-wg", str(max_wg)]
    if args.sampled_warmups:
        cmd += ["--sampled-warmups", args.sampled_warmups]
    if args.sampled_granularities:
        cmd += ["--sampled-granularities", args.sampled_granularities]
    if args.dry_run:
        cmd.append("--dry-run")

    cmd += forwarded
    return cmd


def main():
    args, forwarded = parse_args()
    if args.list_profiles:
        print_profiles()
        return

    cmd = build_command(args, forwarded)
    print("Running:", flush=True)
    print(shlex.join(cmd), flush=True)
    raise SystemExit(subprocess.call(cmd, cwd=ROOT_DIR.parent))


if __name__ == "__main__":
    main()
