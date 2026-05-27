#!/usr/bin/env python3
"""Run BERT/GPT as a sequence of small llmop benchmarks.

This mirrors the old DNN sampledrunner style: each transformer sub-op is run as
its own benchmark process, and the per-op metrics can be summed afterward.
"""

import argparse
import concurrent.futures
import csv
from pathlib import Path
import shlex
import subprocess
import sys

import bertconfig
import gptconfig
import runall2


CONFIG_FLAGS = {name: flags for name, flags in runall2.CONFIGS}
PROFILE_NAMES = sorted(set(bertconfig.PROFILES) | set(gptconfig.PROFILES))


def parse_args():
    parser = argparse.ArgumentParser(
        description="Run decomposed BERT/GPT through the llmop benchmark.")
    parser.add_argument("--model", choices=["bert", "gpt"], default="bert")
    parser.add_argument(
        "--profile", choices=PROFILE_NAMES, default="tiny")
    parser.add_argument(
        "--configs", default="baseline",
        help="Comma-separated runall2 configs, e.g. baseline,sample_all_loop.")
    parser.add_argument("--max-workers", type=int, default=1)
    parser.add_argument(
        "--limit", type=int, default=0,
        help="Run only the first N ops. 0 means all ops.")
    parser.add_argument(
        "--layers", type=int, default=0,
        help="Override the profile layer count. 0 uses the profile default.")
    parser.add_argument(
        "--split-k", default="1",
        help=(
            "Use split-linear with this K split count for all linear ops, "
            "or 'auto' to choose per-op split counts for multi-GPU WG spread."
        ))
    parser.add_argument(
        "--target-gpus", type=int, default=48,
        help="Target actual GPU count for --split-k auto.")
    parser.add_argument(
        "--cu-per-gpu", type=int, default=32,
        help="Actual CU count per GPU used by --split-k auto.")
    parser.add_argument(
        "--max-split-k", type=int, default=16,
        help="Maximum per-op split count used by --split-k auto.")
    parser.add_argument("--sampled-warmup", type=int, default=128)
    parser.add_argument("--sampled-granularity", type=int, default=512)
    parser.add_argument("--log-subtasks", action="store_true")
    parser.add_argument(
        "--include-transfers", action="store_true",
        help="Insert llmop transfer benchmarks between ops with different placements.")
    parser.add_argument(
        "--transfer-copy-gpus", default="dst",
        help="Value passed to -copy-gpus for inserted transfer ops.")
    parser.add_argument(
        "--placement-output", default="llm_decomposed_placement.csv",
        help="Operator placement CSV. Relative paths are under results_dir.")
    parser.add_argument(
        "--log2-page-size", type=int, default=12,
        help="Page size log2 used for per-GPU output-memory estimates.")
    parser.add_argument(
        "--summarize", action="store_true",
        help="Run summarize_llm_decomposed.py after all ops finish.")
    parser.add_argument(
        "--enable-servers", action="store_true",
        help="Do not pass -disable-servers to the benchmark binary.")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args()


def config_flags(name, args):
    if name not in CONFIG_FLAGS:
        raise ValueError(f"unknown config {name!r}")

    flags = CONFIG_FLAGS[name][:]
    if "-sampled" in flags:
        flags += [
            f"-sampled-warmup={args.sampled_warmup}",
            f"-sampled-granularity={args.sampled_granularity}",
        ]
    return flags


def op_kind(flags):
    for flag in flags:
        if flag.startswith("-op="):
            return flag.split("=", 1)[1]
    return ""


def parse_split_k(value):
    if value == "auto":
        return value
    try:
        split_k = int(value)
    except ValueError as err:
        raise ValueError("--split-k must be a positive integer or 'auto'") from err
    if split_k <= 0:
        raise ValueError("--split-k must be positive")
    return split_k


def flag_value(flags, name):
    prefix = f"-{name}="
    for flag in flags:
        if flag.startswith(prefix):
            return flag.split("=", 1)[1]
    return None


def replace_or_append_flag(flags, name, value):
    prefix = f"-{name}="
    new_flag = f"-{name}={value}"
    out = []
    replaced = False
    for flag in flags:
        if flag.startswith(prefix):
            out.append(new_flag)
            replaced = True
        else:
            out.append(flag)
    if not replaced:
        out.append(new_flag)
    return out


def ceil_div(numerator, denominator):
    return (numerator + denominator - 1) // denominator


def uses_target_gpus(total_wg, target_gpus, cu_per_gpu):
    if target_gpus <= 1:
        return total_wg > 0

    total_cu = target_gpus * cu_per_gpu
    wg_per_cu = ceil_div(total_wg, total_cu)
    return total_wg > (target_gpus - 1) * cu_per_gpu * wg_per_cu


def choose_auto_split_k(rows, input_dim, output_dim, args):
    block_size = 16
    base_wg = ceil_div(rows, block_size) * ceil_div(output_dim, block_size)
    max_split = max(1, min(args.max_split_k, input_dim))

    for split_k in range(1, max_split + 1):
        if uses_target_gpus(
            base_wg * split_k, args.target_gpus, args.cu_per_gpu,
        ):
            return split_k

    return max_split


def auto_split_linear_flags(flags, args):
    op = op_kind(flags)
    if op not in {"linear", "split-linear"}:
        return flags

    rows = int(flag_value(flags, "rows"))
    input_dim = int(flag_value(flags, "input-dim"))
    output_dim = int(flag_value(flags, "output-dim"))
    split_k = choose_auto_split_k(rows, input_dim, output_dim, args)
    if split_k <= 1:
        return flags

    out = replace_or_append_flag(flags, "op", "split-linear")
    return replace_or_append_flag(out, "split-k", split_k)


def int_flag(flags, name, default=0):
    value = flag_value(flags, name)
    if value is None:
        return default
    return int(value)


def output_bytes(flags):
    op = op_kind(flags)
    rows = int_flag(flags, "rows")
    hidden = int_flag(flags, "hidden")
    elements = int_flag(flags, "elements")

    if op in {"embedding", "layernorm", "attention", "causal-attention"}:
        return rows * hidden * 4
    if op in {"linear", "split-linear", "mlp"}:
        return rows * int_flag(flags, "output-dim") * 4
    if op in {"gelu", "residual-add"}:
        if elements == 0:
            elements = rows * hidden
        return elements * 4
    if op == "row-softmax":
        return rows * int_flag(flags, "cols", rows) * 4
    if op == "causal-mask":
        return rows * rows * 4
    if op == "batchnorm2d":
        seq_len = int_flag(flags, "seq-len")
        cols = int_flag(flags, "cols", seq_len)
        return rows * hidden * seq_len * cols * 4
    return 0


def estimated_wg(flags):
    op = op_kind(flags)
    rows = int_flag(flags, "rows")
    hidden = int_flag(flags, "hidden")
    elements = int_flag(flags, "elements")

    if op == "embedding":
        return ceil_div(rows * hidden, 64)
    if op == "layernorm":
        return rows
    if op in {"linear", "split-linear", "mlp"}:
        output_dim = int_flag(flags, "output-dim")
        split_k = int_flag(flags, "split-k", 1)
        return ceil_div(rows, 16) * ceil_div(output_dim, 16) * split_k
    if op == "gelu":
        return ceil_div(elements, 64)
    if op == "residual-add":
        return ceil_div(elements, 64)
    if op == "row-softmax":
        return rows
    if op == "causal-mask":
        return ceil_div(rows * rows, 64)
    if op in {"attention", "causal-attention"}:
        return ceil_div(rows, 16) * ceil_div(hidden, 16)
    if op == "batchnorm2d":
        seq_len = int_flag(flags, "seq-len")
        cols = int_flag(flags, "cols", seq_len)
        return ceil_div(rows * hidden * seq_len * cols, 64)
    return 0


def estimated_placement(flags, args):
    total_wg = estimated_wg(flags)
    used_gpus = estimated_used_gpus(total_wg, args)
    if used_gpus <= 1:
        return "first"
    if used_gpus >= args.target_gpus:
        return "all"
    return f"1-{used_gpus}"


def op_output_placement(flags, args):
    if op_kind(flags) == "transfer":
        return flag_value(flags, "dst-gpus") or "first"
    return estimated_placement(flags, args)


def op_compute_placement(flags, args):
    if op_kind(flags) == "transfer":
        src = resolve_placement_spec(flag_value(flags, "src-gpus") or "all", args)
        dst = resolve_placement_spec(flag_value(flags, "dst-gpus") or "first", args)
        return normalize_gpu_spec(
            flag_value(flags, "copy-gpus") or "dst", args, src=src, dst=dst)
    return estimated_placement(flags, args)


def estimated_used_gpus(total_wg, args):
    if total_wg <= 0:
        return 1

    total_cu = args.target_gpus * args.cu_per_gpu
    wg_per_cu = ceil_div(total_wg, total_cu)
    wg_per_gpu = args.cu_per_gpu * wg_per_cu
    return min(args.target_gpus, ceil_div(total_wg, wg_per_gpu))


def resolve_placement_spec(spec, args, src=None, dst=None):
    spec = (spec or "").strip().lower()
    if spec == "all":
        return list(range(1, args.target_gpus + 1))
    if spec == "first":
        return [1]
    if spec == "src":
        return list(src or [])
    if spec == "dst":
        return list(dst or [])

    gpus = []
    seen = set()
    for token in spec.split(","):
        token = token.strip()
        if not token:
            continue
        parts = token.split("-")
        if len(parts) == 1:
            start = end = int(parts[0])
        elif len(parts) == 2:
            start = int(parts[0])
            end = int(parts[1])
        else:
            raise ValueError(f"invalid GPU placement {spec!r}")
        if start <= 0 or end < start or end > args.target_gpus:
            raise ValueError(f"invalid GPU placement {spec!r}")
        for gpu in range(start, end + 1):
            if gpu not in seen:
                gpus.append(gpu)
                seen.add(gpu)
    if not gpus:
        raise ValueError(f"empty GPU placement {spec!r}")
    return gpus


def compact_gpu_list(gpus, args):
    if not gpus:
        return ""
    if gpus == [1]:
        return "first"
    if gpus == list(range(1, args.target_gpus + 1)):
        return "all"

    ranges = []
    start = prev = gpus[0]
    for gpu in gpus[1:]:
        if gpu == prev + 1:
            prev = gpu
            continue
        ranges.append(f"{start}-{prev}" if start != prev else str(start))
        start = prev = gpu
    ranges.append(f"{start}-{prev}" if start != prev else str(start))
    return ",".join(ranges)


def normalize_gpu_spec(spec, args, src=None, dst=None):
    return compact_gpu_list(resolve_placement_spec(spec, args, src, dst), args)


def distribute_bytes(byte_count, gpus, args):
    page_size = 1 << args.log2_page_size
    num_pages = ceil_div(byte_count, page_size)
    out = {gpu: 0 for gpu in gpus}
    if num_pages == 0:
        return out

    pages_per_gpu = num_pages // len(gpus)
    gpus_to_use = 0
    if pages_per_gpu > 0:
        gpus_to_use = num_pages // pages_per_gpu
    if gpus_to_use > len(gpus):
        gpus_to_use = len(gpus)

    last_gpu_index = 0
    for i in range(gpus_to_use):
        out[gpus[i]] += pages_per_gpu * page_size
        last_gpu_index = i

    remaining_pages = num_pages % len(gpus)
    out[gpus[last_gpu_index]] += remaining_pages * page_size

    if sum(out.values()) > byte_count:
        overage = sum(out.values()) - byte_count
        for gpu in reversed(gpus):
            if out[gpu] >= overage:
                out[gpu] -= overage
                break
    return out


def format_gpu_bytes(gpu_bytes):
    return ";".join(
        f"{gpu}:{byte_count}"
        for gpu, byte_count in gpu_bytes.items()
        if byte_count > 0
    )


def transfer_bytes(flags):
    if op_kind(flags) != "transfer":
        return 0
    return int_flag(flags, "transfer-bytes")


def op_bytes(flags):
    if op_kind(flags) == "transfer":
        return transfer_bytes(flags)
    return output_bytes(flags)


def op_metadata(index, label, flags, args):
    output_spec = op_output_placement(flags, args)
    output_gpus = resolve_placement_spec(output_spec, args)
    compute_spec = op_compute_placement(flags, args)
    compute_gpus = resolve_placement_spec(compute_spec, args)
    byte_count = op_bytes(flags)
    per_gpu_bytes = distribute_bytes(byte_count, output_gpus, args)

    return {
        "op_index": index,
        "label": label,
        "op": op_kind(flags),
        "output_bytes": byte_count,
        "estimated_wg": estimated_wg(flags),
        "compute_gpus": compact_gpu_list(compute_gpus, args),
        "compute_gpu_count": len(compute_gpus),
        "output_gpus": compact_gpu_list(output_gpus, args),
        "output_gpu_count": len(output_gpus),
        "per_gpu_output_bytes": format_gpu_bytes(per_gpu_bytes),
        "flags": " ".join(flags),
    }


def insert_transfer_ops(ops, args):
    if len(ops) < 2:
        return ops

    out = []
    for i, (label, flags) in enumerate(ops[:-1]):
        next_label, next_flags = ops[i + 1]
        out.append((label, flags))

        src = op_output_placement(flags, args)
        dst = op_compute_placement(next_flags, args)
        if src == dst:
            continue

        bytes_to_move = output_bytes(flags)
        if bytes_to_move <= 0:
            continue

        transfer_label = f"{label}_to_{next_label}_transfer"
        transfer_flags = [
            "-op=transfer",
            f"-transfer-bytes={bytes_to_move}",
            f"-src-gpus={src}",
            f"-dst-gpus={dst}",
            f"-copy-gpus={args.transfer_copy_gpus}",
        ]
        out.append((transfer_label, transfer_flags))

    out.append(ops[-1])
    return out


def write_placement_report(args, ops):
    path = Path(args.placement_output)
    if not path.is_absolute():
        path = Path(runall2.output_dir) / path

    fieldnames = [
        "op_index",
        "label",
        "op",
        "output_bytes",
        "estimated_wg",
        "compute_gpus",
        "compute_gpu_count",
        "output_gpus",
        "output_gpu_count",
        "per_gpu_output_bytes",
        "flags",
    ]
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for index, (label, flags) in enumerate(ops):
            writer.writerow(op_metadata(index, label, flags, args))
    print(f"Wrote placement report: {path}")


def llm_mixed_config_name(config, op_flags):
    if config != "llm_mixed":
        return config

    op = op_kind(op_flags)
    if op in {"linear", "split-linear"}:
        return "sample_all_loop"
    return "sample_wf"


def build_exps(args, ops=None):
    if ops is None:
        ops = prepare_ops(args)

    common_flags = runall2.BASE_COMMON_FLAGS[:] + [
        f"-mmutlb-lookup-latency={runall2.DEFAULT_MMUTLB_LOOKUP_LATENCY}",
    ]
    if not args.enable_servers:
        common_flags.append("-disable-servers")

    exps = []
    for index, (label, flags) in enumerate(ops):
        for config in args.configs.split(","):
            config = config.strip()
            if not config:
                continue
            actual_config = llm_mixed_config_name(config, flags)
            exp_flags = config_flags(actual_config, args) + flags
            if args.log_subtasks:
                exp_flags.append("-llmop-log-subtasks")
            exps.append({
                "target": "baseline",
                "benchmark": "llmop",
                "config_name": (
                    f"{args.model}_{args.profile}_{index:03d}_{label}_{config}"
                ),
                "common_flags": common_flags,
                "flags": exp_flags,
            })
    return exps


def prepare_ops(args):
    split_k = parse_split_k(args.split_k)
    if args.layers < 0:
        raise ValueError("--layers must be non-negative")
    if args.target_gpus <= 0:
        raise ValueError("--target-gpus must be positive")
    if args.cu_per_gpu <= 0:
        raise ValueError("--cu-per-gpu must be positive")
    if args.max_split_k <= 0:
        raise ValueError("--max-split-k must be positive")
    if args.log2_page_size <= 0:
        raise ValueError("--log2-page-size must be positive")

    config_split_k = 1 if split_k == "auto" else split_k

    if args.model == "bert":
        benchmarks = bertconfig.init_bert(
            args.profile, config_split_k, layers=args.layers or None)
        ops = bertconfig.run_bert(benchmarks)
    else:
        benchmarks = gptconfig.init_gpt(
            args.profile, config_split_k, layers=args.layers or None)
        ops = gptconfig.run_gpt(benchmarks)
    if split_k == "auto":
        ops = [
            (label, auto_split_linear_flags(flags, args))
            for label, flags in ops
        ]
    if args.limit > 0:
        ops = ops[:args.limit]
    if args.include_transfers:
        ops = insert_transfer_ops(ops, args)
    return ops


def summarize_output(args):
    cmd = [
        sys.executable,
        f"{runall2.ROOT_DIR}/summarize_llm_decomposed.py",
        runall2.output_dir,
        "--model",
        args.model,
        "--profile",
        args.profile,
        "--show",
    ]
    print(shlex.join(cmd))
    subprocess.run(cmd, check=True)


def print_dry_run(exps):
    for exp in exps:
        binary = f'{runall2.ROOT_DIR}/{exp["target"]}/{exp["target"]}'
        cmd = [
            binary,
            f'-benchmark={exp["benchmark"]}',
            *exp["common_flags"],
            *exp["flags"],
            "-metric-file-name=<output>",
        ]
        print(shlex.join(cmd))


def main():
    args = parse_args()
    ops = prepare_ops(args)
    exps = build_exps(args, ops)

    if args.dry_run:
        print_dry_run(exps)
        for index, (label, flags) in enumerate(ops):
            metadata = op_metadata(index, label, flags, args)
            print(
                "# placement "
                f"{metadata['op_index']} {metadata['label']} "
                f"op={metadata['op']} "
                f"bytes={metadata['output_bytes']} "
                f"compute={metadata['compute_gpus']} "
                f"output={metadata['output_gpus']} "
                f"per_gpu={metadata['per_gpu_output_bytes']}"
            )
        return

    runall2.install_signal_handlers()
    runall2.create_output_dir()
    write_placement_report(args, ops)
    runall2.build_targets(exps)

    try:
        with concurrent.futures.ThreadPoolExecutor(
            max_workers=args.max_workers,
        ) as executor:
            futures = [executor.submit(runall2.run_exp, exp) for exp in exps]
            for future in concurrent.futures.as_completed(futures):
                print(future.result())
    finally:
        runall2.terminate_all_processes()

    if args.summarize:
        summarize_output(args)


if __name__ == "__main__":
    main()
