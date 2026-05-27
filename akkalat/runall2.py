import atexit
import concurrent.futures
import os
import shlex

from runall2_args import parse_args
from runall2_config import (
    build_ablation_configs,
    build_common_flags,
    filter_missing_metric_exps,
    make_exps,
    sampled_param_sweep_requested,
)
from runall2_process import (
    build_targets,
    create_output_dir,
    dry_run_commands,
    install_signal_handlers,
    run_exp,
    set_output_dir,
    terminate_all_processes,
)


def main():
    install_signal_handlers()
    atexit.register(terminate_all_processes)

    args = parse_args()
    common_flags = build_common_flags(args)
    ablation_configs = build_ablation_configs(args)
    exps = make_exps(args, ablation_configs)
    if not exps:
        print("No experiments configured.")
        return

    prepare_output_dir(args, exps)
    exps = maybe_filter_missing(args, exps)
    if not exps:
        return

    max_workers = choose_max_workers(args, len(exps))
    prepare_exps(args, exps, common_flags)
    print_launch_summary(args, common_flags, exps, max_workers)

    if args.dry_run:
        dry_run_commands(exps)
        return

    build_targets(exps)
    run_all_experiments(exps, max_workers)


def prepare_output_dir(args, exps):
    del exps
    if args.rerun_missing:
        results_dir = os.path.abspath(args.rerun_missing)
        if not os.path.isdir(results_dir):
            raise ValueError(f"results directory does not exist: {results_dir}")
        set_output_dir(results_dir)
        return

    create_output_dir()


def maybe_filter_missing(args, exps):
    if not args.rerun_missing:
        return exps

    results_dir = os.path.abspath(args.rerun_missing)
    missing = filter_missing_metric_exps(exps, results_dir)
    if not missing:
        print(f"No missing-metrics experiments found in {results_dir}")
        return []

    print(f"Rerunning {len(missing)} experiments with missing metrics in {results_dir}")
    return missing


def choose_max_workers(args, exp_count):
    if args.max_workers <= 0:
        raise ValueError("MAX_WORKERS must be greater than 0")

    max_workers = min(args.max_workers, exp_count)
    if sampled_param_sweep_requested(args):
        cap = args.sampled_parallel_limit
        if cap > 0 and max_workers > cap:
            print(
                "Sampled parameter sweep parallelism capped at "
                f"max_workers={cap}. Use --sampled-parallel-limit=0 "
                "to disable this cap."
            )
            max_workers = cap
    return max_workers


def prepare_exps(args, exps, common_flags):
    timeout_seconds = int(args.timeout_minutes * 60)
    for exp in exps:
        exp["common_flags"] = common_flags
        exp["timeout_seconds"] = timeout_seconds


def print_launch_summary(args, common_flags, exps, max_workers):
    print(f"Using common flags: {shlex.join(common_flags)}")
    if args.timeout_minutes > 0:
        print(f"Experiment timeout: {args.timeout_minutes} minutes")
    print(f"Launching {len(exps)} experiments with max_workers={max_workers}")


def run_all_experiments(exps, max_workers):
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
            futures = [executor.submit(run_exp, exp) for exp in exps]
            for future in concurrent.futures.as_completed(futures):
                print(future.result())
    finally:
        terminate_all_processes()


if __name__ == "__main__":
    main()
