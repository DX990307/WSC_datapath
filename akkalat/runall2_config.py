from pathlib import Path
import shlex

from runall2_constants import (
    ALL_BENCHMARKS,
    BALANCED_BRANCH_COVERAGE_THRESHOLD,
    BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD,
    BALANCED_KERNEL_DISTANCE_THRESHOLD,
    BALANCED_SAMPLED_SWEEP_GRANULARITIES,
    BALANCED_SAMPLED_SWEEP_WARMUPS,
    BALANCED_SAMPLED_THRESHOLD,
    BASE_COMMON_FLAGS,
    BENCHMARK_ALIASES,
    BENCHMARKS_BY_TARGET,
    CONFIGS,
    DEFAULT_BENCHMARK_FLAGS,
    DEFAULT_SAMPLED_SWEEP_GRANULARITIES,
    DEFAULT_SAMPLED_SWEEP_WARMUPS,
    QUICK_BENCHMARKS,
    QUICK_CONFIGS,
    TARGETS,
)


def build_common_flags(args):
    common_flags = BASE_COMMON_FLAGS[:]
    if args.switch_latency > 0:
        common_flags = replace_or_append_flag(
            common_flags, "-switch-latency=", f"-switch-latency={args.switch_latency}")
    if args.disable_servers:
        common_flags.append("-disable-servers")
    if args.report_l2_source:
        common_flags.append("-report-l2-source")
        common_flags.append(f"-l2-source-tile-width={args.l2_source_tile_width}")
    common_flags += [
        f"-mmutlb-lookup-latency={args.mmutlb_lookup_latency}",
    ]
    if args.l1v_remote_max_inflight > 0:
        common_flags.append(
            f"-l1v-remote-max-inflight={args.l1v_remote_max_inflight}")
    if args.l1v_mshr_entries > 0:
        common_flags.append(f"-l1v-mshr-entries={args.l1v_mshr_entries}")
    if args.l1v_max_concurrent_trans > 0:
        common_flags.append(
            f"-l1v-max-concurrent-trans={args.l1v_max_concurrent_trans}")
    if args.force_local_data_access:
        common_flags.append("-force-local-data-access")
    if args.max_wg > 0:
        common_flags.append(f"-max-wg={args.max_wg}")
    return common_flags


def replace_or_append_flag(flags, prefix, value):
    updated = []
    replaced = False
    for flag in flags:
        if flag.startswith(prefix):
            updated.append(value)
            replaced = True
        else:
            updated.append(flag)
    if not replaced:
        updated.append(value)
    return updated


def build_ablation_configs(args):
    selected_names = selected_config_names(args)
    sampled_param_grid = build_sampled_param_grid(args)
    selected = []
    for name, flags in CONFIGS:
        if name not in selected_names:
            continue

        config_flags = flags[:]
        if (args.photon_debug or args.photon_verbose) and name != "baseline":
            config_flags.append("-photon-debug")
        if args.photon_verbose and name != "baseline":
            config_flags.append("-photon-debug-verbose")

        config_flags += sampled_control_flags(args, config_flags)

        if "-sampled" not in config_flags or not sampled_param_grid:
            selected.append((name, config_flags))
            continue

        for warmup, granularity in sampled_param_grid:
            swept_flags = config_flags + [
                f"-sampled-warmup={warmup}",
                f"-sampled-granularity={granularity}",
            ]
            selected.append((f"{name}_w{warmup}_g{granularity}", swept_flags))

    return selected


def sampled_control_flags(args, config_flags):
    flags = []
    add_sampled_thresholds(args, config_flags, flags)
    add_branch_thresholds(args, config_flags, flags)
    add_kernel_thresholds(args, config_flags, flags)
    add_loop_thresholds(args, config_flags, flags)
    return flags


def add_sampled_thresholds(args, config_flags, flags):
    threshold = positive_or_zero(args.sampled_threshold, "sampled-threshold")
    if threshold == 0 and args.balanced_sweep:
        threshold = BALANCED_SAMPLED_THRESHOLD
    if threshold > 0 and "-sampled" in config_flags:
        flags.append(f"-sampled-threshold={threshold}")


def add_branch_thresholds(args, config_flags, flags):
    coverage = positive_or_zero(
        args.branch_sampled_coverage_threshold,
        "branch-sampled-coverage-threshold",
    )
    if coverage == 0 and args.balanced_sweep:
        coverage = BALANCED_BRANCH_COVERAGE_THRESHOLD
    if coverage > 0 and "-branch-sampled" in config_flags:
        flags.append(f"-branch-sampled-coverage-threshold={coverage}")

    threshold = positive_or_zero(
        args.branch_sampled_threshold, "branch-sampled-threshold")
    if threshold == 0 and args.balanced_sweep:
        threshold = BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD
    if threshold > 0 and "-branch-sampled" in config_flags:
        flags.append(f"-branch-sampled-threshold={threshold}")


def add_kernel_thresholds(args, config_flags, flags):
    threshold = positive_or_zero(
        args.kernel_sampled_threshold, "kernel-sampled-threshold")
    if threshold > 0 and "-kernel-sampled" in config_flags:
        flags.append(f"-kernel-sampled-threshold={threshold}")

    distance = positive_or_zero(
        args.kernel_sampled_distance_threshold,
        "kernel-sampled-distance-threshold",
    )
    if distance == 0 and args.balanced_sweep:
        distance = BALANCED_KERNEL_DISTANCE_THRESHOLD
    if distance > 0 and "-kernel-sampled" in config_flags:
        flags.append(f"-kernel-sampled-distance-threshold={distance}")


def add_loop_thresholds(args, config_flags, flags):
    warmup = positive_or_zero(args.loop_sampled_warmup, "loop-sampled-warmup")
    if warmup > 0 and "-loop-sampled" in config_flags:
        flags.append(f"-loop-sampled-warmup={warmup}")

    min_iters = positive_or_zero(
        args.loop_sampled_min_iters, "loop-sampled-min-iters")
    if min_iters > 0 and "-loop-sampled" in config_flags:
        flags.append(f"-loop-sampled-min-iters={min_iters}")

    threshold = positive_or_zero(
        args.loop_sampled_threshold, "loop-sampled-threshold")
    if threshold > 0 and "-loop-sampled" in config_flags:
        flags.append(f"-loop-sampled-threshold={threshold}")


def positive_or_zero(value, label):
    if value < 0:
        raise ValueError(f"{label} must be non-negative")
    return value


def parse_csv(value):
    return [item.strip() for item in value.split(",") if item.strip()]


def unique_preserving_order(items):
    seen = set()
    unique = []
    for item in items:
        if item in seen:
            continue
        seen.add(item)
        unique.append(item)
    return unique


def expand_benchmark_selection(selected):
    expanded = []
    for item in selected:
        if item in BENCHMARK_ALIASES:
            expanded += BENCHMARK_ALIASES[item]
        else:
            expanded.append(item)
    return unique_preserving_order(expanded)


def parse_int_csv(value, label):
    values = []
    for item in parse_csv(value):
        parsed = int(item)
        if parsed <= 0:
            raise ValueError(f"{label} values must be positive: {item}")
        values.append(parsed)
    return values


def build_sampled_param_grid(args):
    if args.balanced_sweep:
        warmups = BALANCED_SAMPLED_SWEEP_WARMUPS[:]
        granularities = BALANCED_SAMPLED_SWEEP_GRANULARITIES[:]
    elif args.sampled_sweep:
        warmups = DEFAULT_SAMPLED_SWEEP_WARMUPS[:]
        granularities = DEFAULT_SAMPLED_SWEEP_GRANULARITIES[:]
    else:
        warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
        granularities = parse_int_csv(
            args.sampled_granularities, "sampled-granularities")

    if args.sampled_warmups:
        warmups = parse_int_csv(args.sampled_warmups, "sampled-warmups")
    if args.sampled_granularities:
        granularities = parse_int_csv(
            args.sampled_granularities, "sampled-granularities")

    if not warmups and not granularities:
        return []
    if not warmups:
        warmups = [1024]
    if not granularities:
        granularities = [2048]

    return [(warmup, granularity) for warmup in warmups for granularity in granularities]


def sampled_param_sweep_requested(args):
    return (
        args.sampled_sweep
        or args.balanced_sweep
        or bool(args.sampled_warmups)
        or bool(args.sampled_granularities)
    )


def selected_config_names(args):
    if args.configs:
        requested = parse_csv(args.configs)
    elif args.quick:
        requested = QUICK_CONFIGS[:]
    else:
        requested = ["all"]

    all_config_names = [name for name, _ in CONFIGS]
    if "all" in requested:
        return all_config_names

    unknown = sorted(set(requested) - set(all_config_names))
    if unknown:
        raise ValueError(f"unknown configs: {unknown}")
    return requested


def get_benchmarks_for_target(target):
    selected = BENCHMARKS_BY_TARGET.get(target, ["all"])
    selected = expand_benchmark_selection(selected)
    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks for {target}: {unknown}")
    return selected


def get_selected_benchmarks(args, target):
    if args.benchmarks:
        selected = parse_csv(args.benchmarks)
    elif args.quick:
        selected = QUICK_BENCHMARKS[:]
    else:
        selected = get_benchmarks_for_target(target)

    selected = expand_benchmark_selection(selected)
    unknown = sorted(set(selected) - set(ALL_BENCHMARKS))
    if unknown:
        raise ValueError(f"unknown benchmarks: {unknown}")
    return selected


def make_exps(args, ablation_configs):
    exps = []
    extra_flags = shlex.split(args.extra_benchmark_flags)
    for target in TARGETS:
        for benchmark in get_selected_benchmarks(args, target):
            for config_name, config_flags in ablation_configs:
                exps.append({
                    "target": target,
                    "benchmark": benchmark,
                    "config_name": config_name,
                    "flags": DEFAULT_BENCHMARK_FLAGS + extra_flags + config_flags,
                })
    return exps


def filter_missing_metric_exps(exps, results_dir):
    missing = []
    missing_dir = Path(results_dir)
    for exp in exps:
        stem = f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}'
        metrics_csv = missing_dir / f"{stem}_metrics.csv"
        if not metrics_csv.exists():
            missing.append(exp)
    return missing
