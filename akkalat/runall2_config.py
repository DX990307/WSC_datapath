from pathlib import Path
import json
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
        common_flags = replace_or_append_flag(
            common_flags,
            "-l1v-mshr-entries=",
            f"-l1v-mshr-entries={args.l1v_mshr_entries}",
        )
    if args.l1v_max_concurrent_trans > 0:
        common_flags = replace_or_append_flag(
            common_flags,
            "-l1v-max-concurrent-trans=",
            f"-l1v-max-concurrent-trans={args.l1v_max_concurrent_trans}",
        )
    if args.force_local_data_access:
        common_flags.append("-force-local-data-access")
    if args.allocation_profile:
        common_flags.append("-allocation-profile")
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
    if args.allocation_profile:
        if args.trace_observation or args.trace_memory_path or args.trace_sharing:
            raise ValueError(
                "--allocation-profile cannot be combined with tracing modes"
            )
        if args.remote_ablation:
            raise ValueError(
                "--allocation-profile cannot be combined with ablation sweeps"
            )
        if sampled_param_sweep_requested(args):
            raise ValueError(
                "--allocation-profile cannot be combined with sampled sweeps"
            )
        if args.configs and parse_csv(args.configs) != ["baseline"]:
            raise ValueError(
                "--allocation-profile only supports --configs=baseline"
            )
        return [("baseline", [])]

    if args.trace_observation:
        if args.trace_memory_path:
            raise ValueError(
                "--trace-observation cannot be combined with the legacy "
                "--trace-memory-path"
            )
        if sampled_param_sweep_requested(args):
            raise ValueError(
                "--trace-observation cannot be combined with sampled parameter "
                "sweeps; observation timing must come from full simulation"
            )
        sampled_execution_flags = {
            "-sampled",
            "-branch-sampled",
            "-kernel-sampled",
            "-loop-sampled",
        }
        extra_flags = shlex.split(args.extra_benchmark_flags or "")
        enabled_sampled_flags = sorted(
            flag_name
            for flag_name in sampled_execution_flags
            if any(
                token == flag_name or token.startswith(flag_name + "=")
                for token in extra_flags
            )
        )
        if enabled_sampled_flags:
            raise ValueError(
                "--trace-observation rejects sampled execution flags in "
                "--extra-benchmark-flags: " + ", ".join(enabled_sampled_flags)
            )
        if (
            args.trace_observation_exit_on_complete
            and args.trace_observation_max_records == 0
        ):
            raise ValueError(
                "--trace-observation-exit-on-complete requires "
                "--trace-observation-max-records to be greater than zero"
            )
        for name in (
            "trace_observation_warmup_accesses",
            "trace_observation_max_records",
            "trace_observation_dram_warmup_accesses",
            "trace_observation_dram_max_records",
            "trace_observation_remote_warmup_requests",
            "trace_observation_remote_max_records",
            "trace_observation_l2_sample_max",
        ):
            if getattr(args, name) < 0:
                raise ValueError(name.replace("_", "-") + " must be non-negative")

        # The raw O1--O6 opportunity trace is baseline-only, but a single
        # launcher invocation may also run the M1/M2/M3/Complete cells and
        # collect their ordinary effectiveness counters. runall2.py attaches
        # -trace-observation only to the mechanisms-off baseline experiment.
        if not args.remote_ablation:
            if args.configs and parse_csv(args.configs) != ["baseline"]:
                raise ValueError(
                    "--trace-observation without --remote-ablation only "
                    "supports --configs=baseline"
                )
            return [("baseline", [])]

    if args.remote_ablation:
        return build_remote_data_path_ablation_configs(args)

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


def build_remote_data_path_ablation_configs(args):
    if sampled_param_sweep_requested(args):
        raise ValueError(
            "--remote-ablation cannot be combined with sampled parameter sweeps"
        )

    batch_lines = args.remote_data_path_batch_lines
    max_batches = args.remote_data_path_batches
    if batch_lines < 1 or batch_lines > 64:
        raise ValueError("remote-data-path-batch-lines must be in [1, 64]")
    if max_batches < 1:
        raise ValueError("remote-data-path-batches must be positive")
    fixed_flags = [
        f"-remote-data-path-batch-lines={batch_lines}",
        f"-remote-data-path-batches={max_batches}",
        "-typed-filter-mode=cuckoo",
    ]

    def mechanism_flags(
        resident_filter,
        granularity_adaptation,
        adaptive_pair,
        fill_forwarding,
        remote_enabled,
        remote_dedup,
        remote_batching,
        requester_l2,
        remote_prefetch,
    ):
        return fixed_flags + [
            f"-l2-resident-filter-enable={str(resident_filter).lower()}",
            f"-l2-fill-forwarding-enable={str(fill_forwarding).lower()}",
            # Keep the generic same-row policy disabled. M1 separately enables
            # AggregateContinuation through its granularity mode; that path
            # recognizes only the two independent reads sharing one PairID.
            "-dram-row-continuation-enable=false",
            "-l2-filter-prefetch-enable=false",
            "-l2-prefetch-predictor-only=false",
            "-l2-prefetch-ungated=false",
            f"-l2-granularity-adaptation-enable={str(granularity_adaptation).lower()}",
            f"-l2-adaptive-pair-enable={str(adaptive_pair).lower()}",
            "-l2-granularity-without-filter=false",
            "-l2-granularity-always-expand=false",
            "-l2-granularity-predictor-only=false",
            f"-remote-data-path-enable={str(remote_enabled).lower()}",
            f"-remote-data-path-dedup-enable={str(remote_dedup).lower()}",
            f"-remote-data-path-batching-enable={str(remote_batching).lower()}",
            f"-remote-data-path-l2-enable={str(requester_l2).lower()}",
            f"-remote-filter-prefetch-enable={str(remote_prefetch).lower()}",
        ]

    # Formal five-way paper ablation. Every entry inherits the same explicit
    # 16-entry L1V MSHR setting from BASE_COMMON_FLAGS.
    formal_configs = [
        ("baseline", mechanism_flags(False, False, False, False, False, False, False, False, False)),
        (
            "m1",
            # Preserve the best historical prediction/inflight/buffer policy
            # and aligned 128-B access. The shared per-slice Cuckoo Filter
            # suppresses only exactly confirmed RESIDENT/PENDING siblings.
            mechanism_flags(False, False, True, False, False, False, False, False, False),
        ),
        (
            "m2",
            mechanism_flags(False, False, False, False, True, True, True, False, True),
        ),
        (
            "m3",
            mechanism_flags(False, False, False, False, True, False, False, True, False),
        ),
        (
            "complete",
            mechanism_flags(False, False, True, False, True, True, True, True, True),
        ),
    ]
    if not args.configs:
        return formal_configs

    baseline_flags = formal_configs[0][1]
    m1_flags = formal_configs[1][1]

    def diagnostic_flags(base, replacements):
        flags = base[:]
        for prefix, value in replacements:
            flags = replace_or_append_flag(flags, prefix, value)
        return flags

    diagnostic_configs = [
        (
            "old_m1_independent_prefetch",
            diagnostic_flags(baseline_flags, [
                ("-l2-filter-prefetch-enable=", "-l2-filter-prefetch-enable=true"),
            ]),
        ),
        (
            "cuckoo_filter_only",
            diagnostic_flags(baseline_flags, [
                ("-l2-resident-filter-enable=", "-l2-resident-filter-enable=true"),
            ]),
        ),
        (
            "m1_bypass_fill_only",
            diagnostic_flags(baseline_flags, [
                ("-l2-resident-filter-enable=", "-l2-resident-filter-enable=true"),
                ("-l2-fill-forwarding-enable=", "-l2-fill-forwarding-enable=true"),
            ]),
        ),
        (
            "m1_bypass_fill_predictor_only",
            # Isolate paired-read traffic while retaining every other local
            # M1 behavior. Predictor-only implicitly enables the granularity
            # frontend (and therefore the same optimistic resident-negative
            # bypass) but never allocates or sends a sibling transaction.
            diagnostic_flags(baseline_flags, [
                ("-l2-resident-filter-enable=", "-l2-resident-filter-enable=true"),
                ("-l2-fill-forwarding-enable=", "-l2-fill-forwarding-enable=true"),
                ("-l2-granularity-predictor-only=", "-l2-granularity-predictor-only=true"),
            ]),
        ),
        (
            "always_pair",
            diagnostic_flags(baseline_flags, [
                ("-l2-granularity-always-expand=", "-l2-granularity-always-expand=true"),
            ]),
        ),
        (
            "predictor_only",
            diagnostic_flags(baseline_flags, [
                ("-l2-granularity-predictor-only=", "-l2-granularity-predictor-only=true"),
            ]),
        ),
        (
            "paired_read_without_filter",
            diagnostic_flags(baseline_flags, [
                ("-l2-granularity-without-filter=", "-l2-granularity-without-filter=true"),
            ]),
        ),
        (
            "m1_without_cuckoo",
            # Same historical 128-B adapter, but the typed metadata array is
            # disabled. This isolates the Cuckoo Filter's M1 contribution.
            diagnostic_flags(m1_flags, [
                ("-typed-filter-mode=", "-typed-filter-mode=disabled"),
            ]),
        ),
        ("new_m1", m1_flags[:]),
    ]
    configs = formal_configs + diagnostic_configs

    selected_names = parse_csv(args.configs)
    if "all" in selected_names:
        selected_names = [name for name, _ in formal_configs]
    known_names = {name for name, _ in configs}
    unknown = [name for name in selected_names if name not in known_names]
    if unknown:
        raise ValueError(f"unknown remote-ablation configs: {unknown}")

    selected_set = set(selected_names)
    return [config for config in configs if config[0] in selected_set]


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

    # Schedule configuration-major so every paper benchmark receives Complete
    # before workers move to the next configuration. This keeps early partial
    # results broad instead of launching all variants of AES first.
    if args.remote_ablation:
        config_order = {
            "complete": 0,
            "baseline": 1,
            "m1": 2,
            "m2": 3,
            "m3": 4,
        }
        scheduled_configs = sorted(
            ablation_configs,
            key=lambda item: config_order.get(item[0], len(config_order)),
        )
        for target in TARGETS:
            benchmarks = get_selected_benchmarks(args, target)
            for config_name, config_flags in scheduled_configs:
                for benchmark in benchmarks:
                    exps.append({
                        "target": target,
                        "benchmark": benchmark,
                        "config_name": config_name,
                        "flags": (
                            DEFAULT_BENCHMARK_FLAGS
                            + extra_flags
                            + config_flags
                        ),
                    })
        return exps

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
        result_json = missing_dir / f"{stem}_result.json"
        complete = metrics_csv.exists() and result_json.exists()
        if complete:
            try:
                result = json.loads(result_json.read_text(encoding="utf-8"))
                complete = (
                    bool(result.get("success"))
                    and result.get("returncode") == 0
                    and result.get("simulator_returncode") == 0
                )
            except (OSError, ValueError):
                complete = False
        if not complete:
            missing.append(exp)
    return missing


def valid_runtime_mapping(path: Path, max_wg: int) -> bool:
    """Validate either passive full-workload or runtime-prefix evidence."""
    if max_wg < 0 or not path.is_file():
        return False
    try:
        report = json.loads(path.read_text(encoding="utf-8"))
        observed = int(report["observed_wg_count"])
        requested = int(report["requested_total_wg"])
        expected = requested if max_wg == 0 else min(max_wg, requested)
        per_gpu = report["per_gpu"]
        per_gpu_total = sum(int(row["observed_wg_count"]) for row in per_gpu)
    except (KeyError, OSError, TypeError, ValueError):
        return False
    common = (
        int(report.get("max_wg", -1)) == max_wg
        and requested > 0
        and observed == expected
        and per_gpu_total == observed
        and report.get("max_wg_specific_wg_filter") is False
        and bool(report.get("global_wg_set_sha256"))
        and bool(report.get("launches"))
    )
    if not common:
        return False
    if max_wg == 0:
        return (
            report.get("stop_reason") == "natural_completion"
            and int(report.get("completed_wg_count", -1)) == requested
            and int(report.get("executed_kernel_count", -1))
            == len(report["launches"])
            and float(report.get("observed_sampling_coverage", -1)) == 1.0
            and float(report.get("completed_sampling_coverage", -1)) == 1.0
            and float(report.get("stop_time_ns", 0)) == 0
        )
    return (
        report.get("stop_reason") == "runner_map_wg_observed_limit"
        and (requested >= max_wg or float(report.get("stop_time_ns", 0)) == 0)
    )
