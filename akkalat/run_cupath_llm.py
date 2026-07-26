#!/usr/bin/env python3
"""Run the PASTA-style decomposed LLM workload with CuPath configs.

Each transformer operator is an independent simulator cell. This mirrors the
PASTA LLM campaign, makes failures resumable, and allows whole-model time to be
reconstructed by adding the operator results.
"""

import argparse
import atexit
import csv
from datetime import datetime
from pathlib import Path
from types import SimpleNamespace
import os
import shlex
import sys

import runall2_process
from runall2 import run_all_experiments
from runall2_config import build_remote_data_path_ablation_configs
from runall2_constants import (
    BASE_COMMON_FLAGS,
    DEFAULT_BENCHMARK_FLAGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
    ROOT_DIR,
)
import runllm_decomposed as llm
import summarize_llm_decomposed as llm_summary


DEFAULT_CONFIGS = "baseline,complete"
DEFAULT_MODELS = "bert,gpt"
FORMAL_CONFIGS = ("baseline", "m1", "m2", "m3", "complete")
# The current BERT table does not name the PASTA 7B proxy. Use an existing
# source profile for op construction, then apply the explicit PASTA shape.
MODEL_SOURCE_PROFILES = {"bert": "bert-large", "gpt": "gpt-7b"}
MODEL_RESULT_PROFILES = {"bert": "bert-7b-proxy", "gpt": "gpt-7b"}


def parse_args(argv=None):
    parser = argparse.ArgumentParser(
        description=(
            "Run PASTA-style decomposed BERT/GPT with the current formal "
            "CuPath configurations."
        )
    )
    parser.add_argument(
        "--models",
        default=DEFAULT_MODELS,
        help="Comma-separated models: bert,gpt (default: bert,gpt).",
    )
    parser.add_argument(
        "--configs",
        default=DEFAULT_CONFIGS,
        help=(
            "Comma-separated CuPath configurations: baseline,m1,m2,m3,complete "
            "or all (default: baseline,complete)."
        ),
    )
    parser.add_argument("--batch", type=int, default=1)
    parser.add_argument(
        "--seq-len",
        type=int,
        default=512,
        help="PASTA llmworkload1 used 512, overriding its nominal 5120 profile.",
    )
    parser.add_argument("--hidden", type=int, default=4096)
    parser.add_argument("--heads", type=int, default=32)
    parser.add_argument(
        "--layers",
        type=int,
        default=32,
        help=(
            "Logical transformer-layer count. Only layer 0 is simulated; its "
            "representative results are multiplied by this count."
        ),
    )
    parser.add_argument("--intermediate", type=int, default=11008)
    parser.add_argument(
        "--split-k",
        default="4",
        help="Linear-op K split count or auto (PASTA result default: 4).",
    )
    parser.add_argument("--target-gpus", type=int, default=48)
    parser.add_argument("--cu-per-gpu", type=int, default=32)
    parser.add_argument("--max-split-k", type=int, default=16)
    parser.add_argument(
        "--limit",
        type=int,
        default=0,
        help="Run only the first N operators per model; 0 runs all operators.",
    )
    parser.add_argument(
        "--max-wg",
        type=int,
        default=78600,
        help=(
            "Runtime WG observation limit. It does not repartition or rewrite "
            "launched grids. Use 0 for natural completion."
        ),
    )
    parser.add_argument("--max-workers", type=int, default=14)
    parser.add_argument("--initial-workers", type=int, default=1)
    parser.add_argument("--memory-reserve-gib", type=float, default=50.0)
    parser.add_argument("--memory-scan-minutes", type=float, default=30.0)
    parser.add_argument(
        "--timeout-minutes",
        type=float,
        default=0.0,
        help="Per-operator timeout; 0 disables the timeout.",
    )
    parser.add_argument("--remote-data-path-batch-lines", type=int, default=8)
    parser.add_argument("--remote-data-path-batches", type=int, default=64)
    parser.add_argument("--sampled-warmup", type=int, default=512)
    parser.add_argument("--sampled-granularity", type=int, default=512)
    parser.add_argument(
        "--photon",
        dest="photon",
        action="store_true",
        default=True,
        help="Enable PASTA-style wavefront, branch, kernel, and loop sampling.",
    )
    parser.add_argument(
        "--no-photon",
        dest="photon",
        action="store_false",
        help="Run without execution sampling.",
    )
    parser.add_argument("--include-transfers", action="store_true")
    parser.add_argument("--log-subtasks", action="store_true")
    parser.add_argument("--disable-servers", action="store_true")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument(
        "--output-dir",
        default="",
        help="Result directory; defaults to a timestamped *-cupath-llm directory.",
    )
    parser.add_argument(
        "--summarize",
        dest="summarize",
        action="store_true",
        default=True,
    )
    parser.add_argument("--no-summarize", dest="summarize", action="store_false")
    parser.add_argument("--dry-run", action="store_true")
    return parser.parse_args(argv)


def parse_csv(value):
    return [item.strip() for item in value.split(",") if item.strip()]


def selected_models(value):
    models = parse_csv(value)
    unknown = sorted(set(models) - set(MODEL_SOURCE_PROFILES))
    if not models or unknown:
        raise ValueError(f"unknown or empty --models selection: {unknown or models}")
    return list(dict.fromkeys(models))


def selected_configs(value):
    configs = parse_csv(value)
    if "all" in configs:
        configs = list(FORMAL_CONFIGS)
    unknown = sorted(set(configs) - set(FORMAL_CONFIGS))
    if not configs or unknown:
        raise ValueError(f"unknown or empty --configs selection: {unknown or configs}")
    return list(dict.fromkeys(configs))


def validate_args(args):
    for name in ("batch", "seq_len", "hidden", "heads", "layers", "intermediate"):
        if getattr(args, name) <= 0:
            raise ValueError(f"--{name.replace('_', '-')} must be positive")
    if args.limit < 0 or args.max_wg < 0:
        raise ValueError("--limit and --max-wg must be non-negative")
    if args.max_workers <= 0 or args.initial_workers <= 0:
        raise ValueError("worker counts must be positive")
    if args.initial_workers > args.max_workers:
        raise ValueError("--initial-workers cannot exceed --max-workers")
    if args.memory_reserve_gib < 0 or args.timeout_minutes < 0:
        raise ValueError("memory reserve and timeout must be non-negative")
    if args.memory_reserve_gib > 0 and args.memory_scan_minutes <= 0:
        raise ValueError("--memory-scan-minutes must be positive")
    if args.sampled_warmup < 0 or args.sampled_granularity <= 0:
        raise ValueError("sampling controls are invalid")


def formal_config_flags(args, config_names):
    adapter = SimpleNamespace(
        configs=",".join(config_names),
        remote_data_path_batch_lines=args.remote_data_path_batch_lines,
        remote_data_path_batches=args.remote_data_path_batches,
        sampled_sweep=False,
        balanced_sweep=False,
        sampled_warmups="",
        sampled_granularities="",
    )
    by_name = dict(build_remote_data_path_ablation_configs(adapter))
    return [(name, by_name[name]) for name in config_names]


def make_llm_args(args, model):
    return SimpleNamespace(
        model=model,
        profile=MODEL_SOURCE_PROFILES[model],
        configs=args.configs,
        max_workers=args.max_workers,
        limit=args.limit,
        # Generate one representative transformer layer. The logical layer
        # count is preserved separately as a reconstruction multiplicity.
        layers=1,
        batch=args.batch,
        seq_len=args.seq_len,
        hidden=args.hidden,
        heads=args.heads,
        intermediate=args.intermediate,
        split_k=args.split_k,
        target_gpus=args.target_gpus,
        cu_per_gpu=args.cu_per_gpu,
        max_split_k=args.max_split_k,
        sampled_warmup=args.sampled_warmup,
        sampled_granularity=args.sampled_granularity,
        log_subtasks=args.log_subtasks,
        include_transfers=args.include_transfers,
        transfer_copy_gpus="dst",
        placement_output=f"cupath_llm_{model}_placement.csv",
        log2_page_size=12,
        summarize=False,
        enable_servers=not args.disable_servers,
        trace_sharing=False,
        trace_sharing_sample=1,
        trace_sharing_max_records=0,
        trace_memory_path=False,
        trace_memory_path_warmup_accesses=0,
        trace_memory_path_max_records=0,
        trace_memory_path_exit_on_complete=False,
        l1v_remote_max_inflight=0,
        force_local_data_access=False,
        report_l2_source=False,
        l2_source_tile_width=7,
        dry_run=args.dry_run,
    )


def common_flags(args):
    flags = BASE_COMMON_FLAGS[:] + [
        f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}",
    ]
    if args.max_wg > 0:
        flags.append(f"-max-wg={args.max_wg}")
    if args.disable_servers:
        flags.append("-disable-servers")
    if args.photon:
        flags += [
            "-sampled",
            "-branch-sampled",
            "-kernel-sampled",
            "-loop-sampled",
            f"-sampled-warmup={args.sampled_warmup}",
            f"-sampled-granularity={args.sampled_granularity}",
        ]
    return flags


def sampling_flags_for_op(flags, op):
    """Return sampling flags that are safe for the selected operator.

    Photon loop fast-forward cannot preserve workgroup-wide barriers in the
    current timing model. LayerNorm uses several such barriers, so it keeps
    wavefront, branch, and kernel sampling but executes its loops normally.
    """
    if op != "layernorm":
        return list(flags)
    return [
        flag
        for flag in flags
        if flag != "-loop-sampled"
        and not flag.startswith("-loop-sampled-")
    ]


def is_repeated_layer_op(label):
    return label.startswith("layer00_")


def op_signature(flags):
    """Return the simulator-visible identity of one operator benchmark."""
    return tuple(sorted(flags))


def deduplicate_representative_ops(ops, logical_layers):
    """Merge simulator-identical operators and retain reconstruction weights."""
    representatives = []
    metadata = []
    by_signature = {}

    for label, flags in ops:
        signature = op_signature(flags)
        repeated = is_repeated_layer_op(label)
        multiplicity = logical_layers if repeated else 1
        if signature in by_signature:
            entry = metadata[by_signature[signature]]
            entry["equivalent_labels"].append(label)
            entry["logical_multiplicity"] += multiplicity
            entry["one_layer_occurrences"] += int(repeated)
            entry["model_once_occurrences"] += int(not repeated)
            continue

        by_signature[signature] = len(representatives)
        representatives.append((label, list(flags)))
        metadata.append({
            "representative_label": label,
            "op": llm.op_kind(flags),
            "flags": list(flags),
            "equivalent_labels": [label],
            "logical_multiplicity": multiplicity,
            "one_layer_occurrences": int(repeated),
            "model_once_occurrences": int(not repeated),
            "logical_layers": logical_layers,
        })

    return representatives, metadata


def build_campaign(args):
    models = selected_models(args.models)
    config_names = selected_configs(args.configs)
    configs = formal_config_flags(args, config_names)
    base_flags = common_flags(args)
    timeout_seconds = int(args.timeout_minutes * 60)
    exps = []
    model_ops = []

    for model in models:
        model_args = make_llm_args(args, model)
        raw_ops = llm.prepare_ops(model_args)
        ops, representative_metadata = deduplicate_representative_ops(
            raw_ops, args.layers
        )
        result_profile = MODEL_RESULT_PROFILES[model]
        model_ops.append((
            model_args,
            ops,
            representative_metadata,
            result_profile,
        ))
        for index, (label, op_flags) in enumerate(ops):
            op = llm.op_kind(op_flags)
            op_common_flags = sampling_flags_for_op(base_flags, op)
            for config_name, config_flags in configs:
                flags = DEFAULT_BENCHMARK_FLAGS + config_flags + op_flags
                if args.log_subtasks:
                    flags.append("-llmop-log-subtasks")
                exps.append({
                    "target": "baseline",
                    "benchmark": "llmop",
                    "config_name": (
                        f"{model}_{result_profile}_{index:03d}_{label}_"
                        f"{config_name}"
                    ),
                    "common_flags": op_common_flags,
                    "flags": flags,
                    "timeout_seconds": timeout_seconds,
                })
    return exps, model_ops


def prepare_output_dir(args):
    if args.output_dir:
        output_dir = Path(args.output_dir).expanduser().resolve()
    else:
        output_dir = (
            Path(ROOT_DIR)
            / "results"
            / datetime.now().strftime("%Y-%m-%d-%H-%M-%S-cupath-llm")
        )
    output_dir.mkdir(parents=True, exist_ok=False)
    runall2_process.set_output_dir(str(output_dir))
    (output_dir / "launcher.pid").write_text(f"{os.getpid()}\n", encoding="utf-8")
    return output_dir


def representative_weight_map(model_ops):
    weights = {}
    for model_args, ops, metadata, result_profile in model_ops:
        if len(ops) != len(metadata):
            raise RuntimeError("representative metadata does not match operators")
        for index, ((label, _), entry) in enumerate(zip(ops, metadata)):
            key = (model_args.model, result_profile, index, label)
            if key in weights:
                raise RuntimeError(f"duplicate representative key: {key}")
            weights[key] = entry
    return weights


def write_representative_manifest(output_dir, model_ops):
    path = output_dir / "cupath_llm_representatives.csv"
    fieldnames = [
        "model",
        "profile",
        "representative_index",
        "representative_label",
        "op",
        "equivalent_labels",
        "one_layer_occurrences",
        "model_once_occurrences",
        "logical_layers",
        "logical_multiplicity",
        "flags",
    ]
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=fieldnames)
        writer.writeheader()
        for model_args, ops, metadata, result_profile in model_ops:
            for index, ((label, _), entry) in enumerate(zip(ops, metadata)):
                writer.writerow({
                    "model": model_args.model,
                    "profile": result_profile,
                    "representative_index": index,
                    "representative_label": label,
                    "op": entry["op"],
                    "equivalent_labels": ";".join(entry["equivalent_labels"]),
                    "one_layer_occurrences": entry["one_layer_occurrences"],
                    "model_once_occurrences": entry["model_once_occurrences"],
                    "logical_layers": entry["logical_layers"],
                    "logical_multiplicity": entry["logical_multiplicity"],
                    "flags": shlex.join(entry["flags"]),
                })
    return path


def weighted_groups(records, weight_by_result):
    groups = {}
    for record in records:
        rid = record.result_id
        weight_key = (
            rid.model,
            rid.profile,
            rid.op_index,
            rid.op_name,
        )
        entry = weight_by_result.get(weight_key)
        if entry is None:
            raise RuntimeError(
                f"missing reconstruction weight for result {weight_key}"
            )
        weight = entry["logical_multiplicity"]
        group_key = (rid.model, rid.profile, rid.config)
        group = groups.setdefault(group_key, {
            "representative_ops": 0,
            "logical_ops": 0,
            "driver_total_time": 0.0,
            "driver_kernel_time": 0.0,
            "command_processor_kernel_time": 0.0,
            "max_command_processor_kernel_time_sum": 0.0,
            "measured_stdout_elapsed_seconds": 0.0,
            "weighted_stdout_elapsed_seconds": 0.0,
            "ok_representatives": 0,
            "ok_logical_ops": 0,
            "missing_return_code_representatives": 0,
        })
        group["representative_ops"] += 1
        group["logical_ops"] += weight
        group["driver_total_time"] += record.driver_total_time * weight
        group["driver_kernel_time"] += record.driver_kernel_time * weight
        group["command_processor_kernel_time"] += (
            record.command_processor_kernel_time * weight
        )
        group["max_command_processor_kernel_time_sum"] += (
            record.max_command_processor_kernel_time * weight
        )
        group["measured_stdout_elapsed_seconds"] += record.stdout_elapsed_seconds
        group["weighted_stdout_elapsed_seconds"] += (
            record.stdout_elapsed_seconds * weight
        )
        if record.return_code == "0":
            group["ok_representatives"] += 1
            group["ok_logical_ops"] += weight
        elif record.return_code == "":
            group["missing_return_code_representatives"] += 1
    return groups


def write_weighted_summary(path, groups):
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "model",
            "profile",
            "config",
            "representative_ops",
            "logical_op_instances",
            "driver_total_time_weighted_sum",
            "driver_total_time_weighted_us",
            "speedup_vs_baseline",
            "driver_kernel_time_weighted_sum",
            "command_processor_kernel_time_weighted_sum",
            "max_command_processor_kernel_time_weighted_sum",
            "measured_stdout_elapsed_seconds_sum",
            "weighted_stdout_elapsed_seconds_sum",
            "return_code_0_representatives",
            "return_code_0_logical_instances",
            "missing_return_code_representatives",
        ])
        for (model, profile, config), group in sorted(groups.items()):
            baseline = groups.get((model, profile, "baseline"))
            speedup = ""
            if (
                baseline is not None
                and group["driver_total_time"] > 0
            ):
                speedup = (
                    baseline["driver_total_time"]
                    / group["driver_total_time"]
                )
            writer.writerow([
                model,
                profile,
                config,
                group["representative_ops"],
                group["logical_ops"],
                f"{group['driver_total_time']:.12f}",
                f"{group['driver_total_time'] * 1e6:.3f}",
                f"{speedup:.12f}" if speedup != "" else "",
                f"{group['driver_kernel_time']:.12f}",
                f"{group['command_processor_kernel_time']:.12f}",
                f"{group['max_command_processor_kernel_time_sum']:.12f}",
                f"{group['measured_stdout_elapsed_seconds']:.6f}",
                f"{group['weighted_stdout_elapsed_seconds']:.6f}",
                group["ok_representatives"],
                group["ok_logical_ops"],
                group["missing_return_code_representatives"],
            ])


def write_weighted_per_op(path, records, weight_by_result):
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.writer(stream)
        writer.writerow([
            "model",
            "profile",
            "config",
            "representative_index",
            "representative_label",
            "equivalent_labels",
            "logical_multiplicity",
            "driver_total_time",
            "weighted_driver_total_time",
            "driver_kernel_time",
            "weighted_driver_kernel_time",
            "command_processor_kernel_time",
            "weighted_command_processor_kernel_time",
            "stdout_elapsed_seconds",
            "return_code",
            "metrics_file",
            "stdout_file",
        ])
        for record in records:
            rid = record.result_id
            entry = weight_by_result[(
                rid.model,
                rid.profile,
                rid.op_index,
                rid.op_name,
            )]
            weight = entry["logical_multiplicity"]
            writer.writerow([
                rid.model,
                rid.profile,
                rid.config,
                rid.op_index,
                rid.op_name,
                ";".join(entry["equivalent_labels"]),
                weight,
                f"{record.driver_total_time:.12f}",
                f"{record.driver_total_time * weight:.12f}",
                f"{record.driver_kernel_time:.12f}",
                f"{record.driver_kernel_time * weight:.12f}",
                f"{record.command_processor_kernel_time:.12f}",
                f"{record.command_processor_kernel_time * weight:.12f}",
                f"{record.stdout_elapsed_seconds:.6f}",
                record.return_code,
                record.metrics_path.name,
                record.stdout_path.name,
            ])


def print_weighted_summary(groups):
    print(
        "model profile config representatives logical_ops "
        "weighted_driver_us speedup ok"
    )
    for (model, profile, config), group in sorted(groups.items()):
        baseline = groups.get((model, profile, "baseline"))
        speedup = ""
        if baseline and group["driver_total_time"] > 0:
            speedup = (
                f"{baseline['driver_total_time'] / group['driver_total_time']:.4f}"
            )
        print(
            f"{model} {profile} {config} "
            f"{group['representative_ops']} {group['logical_ops']} "
            f"{group['driver_total_time'] * 1e6:.3f} "
            f"{speedup or 'n/a'} "
            f"{group['ok_representatives']}/{group['representative_ops']}"
        )


def summarize(output_dir, model_ops):
    # Teach the existing filename parser the formal CuPath suffixes without
    # changing its legacy Photon behavior.
    for config in reversed(FORMAL_CONFIGS):
        if config not in llm_summary.CONFIG_NAMES:
            llm_summary.CONFIG_NAMES.insert(0, config)
    records, skipped = llm_summary.matching_records(output_dir, "", "")
    if not records:
        raise RuntimeError(f"no decomposed llmop metrics found in {output_dir}")
    records.sort(key=lambda record: (
        record.result_id.model,
        record.result_id.profile,
        record.result_id.config,
        record.result_id.op_index,
        record.result_id.op_name,
    ))
    weight_by_result = representative_weight_map(model_ops)
    groups = weighted_groups(records, weight_by_result)
    summary_path = output_dir / "cupath_llm_summary.csv"
    per_op_path = output_dir / "cupath_llm_per_op_summary.csv"
    write_weighted_summary(summary_path, groups)
    write_weighted_per_op(per_op_path, records, weight_by_result)
    print(f"Wrote weighted summary: {summary_path}", flush=True)
    print(f"Wrote weighted per-op summary: {per_op_path}", flush=True)
    if skipped:
        print(f"Skipped unrelated metric files: {len(skipped)}", flush=True)
    print_weighted_summary(groups)


def main(argv=None):
    args = parse_args(argv)
    validate_args(args)
    runall2_process.install_signal_handlers()
    atexit.register(runall2_process.terminate_all_processes)

    exps, model_ops = build_campaign(args)
    output_dir = prepare_output_dir(args)
    representative_manifest = write_representative_manifest(
        output_dir, model_ops
    )
    print(
        f"Wrote representative manifest: {representative_manifest}",
        flush=True,
    )
    for model_args, ops, _, _ in model_ops:
        llm.write_placement_report(model_args, ops)

    print(f"CuPathLLM results: {output_dir}", flush=True)
    print(f"Configured {len(exps)} representative operator/config cells.", flush=True)
    for model_args, ops, metadata, result_profile in model_ops:
        logical_ops = sum(entry["logical_multiplicity"] for entry in metadata)
        print(
            f"{model_args.model}/{result_profile}: {len(ops)} representatives "
            f"reconstruct {logical_ops} logical operator instances.",
            flush=True,
        )
    print(f"Common flags: {shlex.join(common_flags(args))}", flush=True)
    if args.dry_run:
        runall2_process.dry_run_commands(exps)
        return

    if args.skip_build:
        print("Skipping target build (--skip-build).", flush=True)
    else:
        runall2_process.build_targets(exps)
    frozen = runall2_process.freeze_experiment_binaries(exps)
    print(f"Froze experiment binaries: {frozen}", flush=True)
    manifest = runall2_process.verify_or_write_binary_manifest(exps)
    print(f"Verified experiment binary manifest: {manifest}", flush=True)
    metadata = runall2_process.write_experiment_metadata(
        exps,
        launcher={
            "kind": "cupath_llm",
            "models": selected_models(args.models),
            "configs": selected_configs(args.configs),
            "max_workers": args.max_workers,
            "initial_workers": args.initial_workers,
            "memory_reserve_gib": args.memory_reserve_gib,
            "memory_scan_minutes": args.memory_scan_minutes,
            "timeout_minutes": args.timeout_minutes,
            "photon": args.photon,
            "max_wg": args.max_wg,
            "representative_layer_only": True,
            "logical_layers": args.layers,
            "operator_equivalence": "same simulator flags",
        },
    )
    print(f"Recorded experiment metadata: {metadata}", flush=True)

    run_all_experiments(
        exps,
        min(args.max_workers, len(exps)),
        initial_workers=min(args.initial_workers, len(exps)),
        memory_reserve_gib=args.memory_reserve_gib,
        memory_scan_seconds=args.memory_scan_minutes * 60,
    )
    if args.summarize:
        summarize(output_dir, model_ops)


if __name__ == "__main__":
    main()
