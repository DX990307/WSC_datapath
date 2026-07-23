from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import subprocess
import threading

from runall2_constants import ROOT_DIR

output_dir = ""
BINARY_MANIFEST_NAME = "EXPERIMENT_BINARIES.json"
EXPERIMENT_METADATA_NAME = "EXPERIMENT_METADATA.json"
FROZEN_BINARY_DIR_NAME = "frozen-binaries"
running_processes = set()
running_processes_lock = threading.Lock()
stopping = False


def load_experiments_from_metadata(results_dir):
    """Reconstruct the exact original commands for a missing-cell rerun.

    A rerun must not rebuild the campaign from today's benchmark/config
    defaults: those lists can evolve while a long experiment is in flight.
    The original metadata is the authoritative campaign definition.
    """
    path = Path(results_dir) / EXPERIMENT_METADATA_NAME
    if not path.is_file():
        raise RuntimeError(
            f"missing {EXPERIMENT_METADATA_NAME} in resume directory "
            f"{results_dir}"
        )
    with path.open() as stream:
        metadata = json.load(stream)

    records = metadata.get("experiments")
    if not isinstance(records, list) or not records:
        raise RuntimeError(f"invalid experiment metadata: {path}")

    experiments = []
    for record in records:
        command = record.get("command")
        target = record.get("target")
        benchmark = record.get("benchmark")
        config_name = record.get("configuration")
        if not isinstance(command, list) or len(command) < 3 or not all(
            isinstance(arg, str) for arg in command
        ):
            raise RuntimeError(f"invalid recorded command in {path}: {record}")
        if not all(isinstance(value, str) and value for value in (
            target, benchmark, config_name
        )):
            raise RuntimeError(f"invalid experiment identity in {path}: {record}")

        benchmark_flag = f"-benchmark={benchmark}"
        if command[1] != benchmark_flag:
            raise RuntimeError(
                f"recorded benchmark mismatch in {path}: "
                f"expected {benchmark_flag}, found {command[1]}"
            )
        metric_flags = [
            arg for arg in command[2:]
            if arg.startswith("-metric-file-name=")
        ]
        if len(metric_flags) != 1:
            raise RuntimeError(
                f"recorded command must contain one metric output in {path}"
            )
        preserved_flags = [
            arg for arg in command[2:] if arg != metric_flags[0]
        ]
        experiments.append({
            "target": target,
            "benchmark": benchmark,
            "config_name": config_name,
            "binary_path": command[0],
            "common_flags": preserved_flags,
            "flags": [],
        })
    return experiments


def build_env():
    env = os.environ.copy()
    env.setdefault("GOCACHE", "/tmp/gocache")
    return env


def build_targets(exps):
    env = build_env()
    targets = sorted({exp["target"] for exp in exps})

    for target in targets:
        target_dir = os.path.join(ROOT_DIR, target)
        print(f"Building {target} in {target_dir}")
        process = subprocess.Popen(
            ["go", "build", "-buildvcs=false"],
            cwd=target_dir,
            env=env,
        )
        process.wait()
        if process.returncode != 0:
            raise RuntimeError(f"failed to build {target}")


def start_process(cmd, **kwargs):
    """Start and register a child atomically with respect to shutdown.

    A signal can arrive after a worker has been dequeued but before its child
    has been registered.  If shutdown snapshots the process set in that gap,
    the newly-started simulator escapes termination and the worker immediately
    advances the queue.  Holding the lifecycle lock across the stop check,
    Popen, and registration closes that race.
    """
    with running_processes_lock:
        if stopping:
            return None
        process = subprocess.Popen(cmd, **kwargs)
        running_processes.add(process)
        return process


def unregister_process(process):
    with running_processes_lock:
        running_processes.discard(process)


def terminate_process(process, grace_seconds=10):
    if process.poll() is not None:
        return

    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return

    try:
        process.wait(timeout=grace_seconds)
        return
    except subprocess.TimeoutExpired:
        pass

    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    process.wait()


def terminate_all_processes():
    global stopping
    with running_processes_lock:
        stopping = True
        processes = list(running_processes)

    for process in processes:
        terminate_process(process)


def install_signal_handlers():
    global stopping
    with running_processes_lock:
        stopping = False

    def handle_signal(signum, _frame):
        print(
            f"Received signal {signum}; terminating running experiments.",
            flush=True,
        )
        terminate_all_processes()
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)


def set_output_dir(path):
    global output_dir
    output_dir = path


def target_binary_hashes(exps, root_dir=ROOT_DIR):
    hashes = {}
    by_target = {}
    for exp in exps:
        target = exp["target"]
        binary = exp.get("binary_path") or os.path.join(
            root_dir, target, target)
        previous = by_target.setdefault(target, binary)
        if previous != binary:
            raise RuntimeError(
                f"target {target} has multiple experiment binaries: "
                f"{previous}, {binary}"
            )
    for target, binary in sorted(by_target.items()):
        digest = hashlib.sha256()
        with open(binary, "rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
        hashes[target] = digest.hexdigest()
    return hashes


def freeze_experiment_binaries(exps, results_dir=None, root_dir=ROOT_DIR):
    """Copy each target binary into an immutable campaign-local path.

    Experiment metadata must not point at a normal build output that a later
    ``go build`` can overwrite.  The content hash participates in the frozen
    filename, and an existing destination is accepted only after rehashing.
    """
    directory = Path(results_dir or output_dir)
    frozen_dir = directory / FROZEN_BINARY_DIR_NAME
    frozen_dir.mkdir(parents=True, exist_ok=True)

    sources = {}
    for exp in exps:
        target = exp["target"]
        source = Path(exp.get("binary_path") or Path(root_dir) / target / target)
        source = source.resolve()
        previous = sources.setdefault(target, source)
        if previous != source:
            raise RuntimeError(
                f"target {target} has multiple experiment binaries: "
                f"{previous}, {source}"
            )
    frozen = {}
    for target, source in sorted(sources.items()):
        if not source.is_file():
            raise RuntimeError(f"experiment binary is missing: {source}")
        digest = hashlib.sha256(source.read_bytes()).hexdigest()
        destination = frozen_dir / f"{target}-{digest}"
        if destination.exists():
            existing = hashlib.sha256(destination.read_bytes()).hexdigest()
            if existing != digest:
                raise RuntimeError(
                    f"frozen binary hash mismatch: {destination}"
                )
        else:
            temporary = destination.with_suffix(".tmp")
            shutil.copy2(source, temporary)
            temporary.chmod(source.stat().st_mode | 0o111)
            temporary.replace(destination)
        destination.chmod(0o555)
        frozen[target] = str(destination.resolve())

    for exp in exps:
        exp["binary_path"] = frozen[exp["target"]]
    return frozen


def verify_or_write_binary_manifest(
    exps,
    require_existing=False,
    results_dir=None,
    root_dir=ROOT_DIR,
):
    directory = results_dir or output_dir
    path = os.path.join(directory, BINARY_MANIFEST_NAME)
    current = {
        "version": 1,
        "sha256_by_target": target_binary_hashes(exps, root_dir),
    }
    if os.path.exists(path):
        with open(path) as stream:
            recorded = json.load(stream)
        if recorded != current:
            raise RuntimeError(
                "experiment binary hash mismatch; refusing to mix builds in "
                f"{directory}: recorded={recorded}, current={current}"
            )
        return path
    if require_existing:
        raise RuntimeError(
            f"missing {BINARY_MANIFEST_NAME} in resume directory {directory}; "
            "refusing an unverifiable rerun"
        )
    if any(Path(directory).glob("*_metrics.csv")):
        raise RuntimeError(
            f"existing metrics in {directory} have no "
            f"{BINARY_MANIFEST_NAME}; use a new empty output directory"
        )
    with open(path, "w") as stream:
        json.dump(current, stream, indent=2, sort_keys=True)
        stream.write("\n")
    return path


def write_experiment_metadata(
    exps, results_dir=None, preserve_existing=False, launcher=None
):
    """Persist the exact commands and source state used by a run."""
    directory = results_dir or output_dir
    repo_root = str(Path(ROOT_DIR).parent)

    def git_output(*args):
        return subprocess.check_output(
            ["git", *args], cwd=repo_root, text=True,
        ).strip()

    metadata = {
        "version": 1,
        "created_at": datetime.now().isoformat(),
        "git_revision": git_output("rev-parse", "HEAD"),
        "git_status_porcelain": git_output("status", "--short"),
        "experiment_count": len(exps),
        "launcher": dict(launcher or {}),
        "experiments": [
            {
                "target": exp["target"],
                "benchmark": exp["benchmark"],
                "configuration": exp["config_name"],
                "command": experiment_command(exp),
            }
            for exp in exps
        ],
    }
    path = os.path.join(directory, EXPERIMENT_METADATA_NAME)
    if preserve_existing and os.path.exists(path):
        stamp = datetime.now().strftime("%Y%m%d-%H%M%S-%f")
        path = os.path.join(
            directory, f"EXPERIMENT_RERUN_METADATA_{stamp}.json"
        )
    with open(path, "w") as stream:
        json.dump(metadata, stream, indent=2, sort_keys=True)
        stream.write("\n")
    return path


def exp_file_stem(exp):
    return os.path.join(
        output_dir,
        f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}',
    )


def cell_result_path(exp):
    return Path(exp_file_stem(exp) + "_result.json")


def max_wg_limit(exp):
    for flag in (*exp.get("common_flags", []), *exp.get("flags", [])):
        if flag.startswith("-max-wg="):
            return int(flag.split("=", 1)[1])
    return 0


def expected_max_wg_observation(report, limit):
    """Return the number of WGs a runtime-only stopper can observe.

    ``max-wg`` is an upper bound, not a request to manufacture work.  A
    workload whose complete launched grid is smaller than the limit must run
    to natural completion and can only expose the full requested count.
    """
    requested = int(report.get("requested_total_wg", 0))
    if requested <= 0:
        raise RuntimeError("WG mapping report has no requested workgroups")
    return min(limit, requested)


def wg_termination_mode(report, limit):
    expected = expected_max_wg_observation(report, limit)
    if expected < limit:
        return "workload_completed_before_max_wg"
    return "runner_map_wg_observed_limit"


def audit_wg_mapping(exp, metric_file_name):
    limit = max_wg_limit(exp)
    path = Path(metric_file_name + "_wg_mapping.json")
    if not path.is_file():
        raise RuntimeError(f"missing WG mapping report: {path}")
    report = json.loads(path.read_text(encoding="utf-8"))
    if report.get("max_wg_specific_wg_filter"):
        raise RuntimeError(f"max-wg-specific WGFilter detected in {path}")
    requested = int(report.get("requested_total_wg", 0))
    if requested <= 0:
        raise RuntimeError(f"WG mapping report has no requested workgroups: {path}")
    observed = int(report.get("observed_wg_count", -1))
    if limit <= 0:
        if report.get("stop_reason") != "natural_completion":
            raise RuntimeError(f"invalid full-workload stop reason in {path}")
        if report.get("max_wg") != 0 or observed != requested:
            raise RuntimeError(
                f"full-workload observation mismatch in {path}: "
                f"max_wg={report.get('max_wg')}, requested={requested}, "
                f"observed={observed}"
            )
        completed = int(report.get("completed_wg_count", -1))
        if completed != requested:
            raise RuntimeError(
                f"full-workload completion mismatch in {path}: "
                f"requested={requested}, completed={completed}"
            )
        if float(report.get("stop_time_ns", 0)) != 0:
            raise RuntimeError(
                f"full workload has a nonzero stopper time in {path}"
            )
        termination_mode = "natural_completion"
    else:
        if report.get("stop_reason") != "runner_map_wg_observed_limit":
            raise RuntimeError(f"invalid max-wg stop reason in {path}")
        expected = expected_max_wg_observation(report, limit)
        if report.get("max_wg") != limit or observed != expected:
            raise RuntimeError(
                f"max-wg observation mismatch in {path}: "
                f"limit={limit}, report={report.get('max_wg')}, "
                f"requested={requested}, expected={expected}, "
                f"observed={observed}"
            )
        termination_mode = wg_termination_mode(report, limit)
        if (termination_mode == "workload_completed_before_max_wg"
                and float(report.get("stop_time_ns", 0)) != 0):
            raise RuntimeError(
                f"natural completion has a nonzero stopper time in {path}"
            )
    per_gpu = report.get("per_gpu", [])
    if sum(int(row.get("observed_wg_count", 0)) for row in per_gpu) != observed:
        raise RuntimeError(f"per-GPU observed WG count mismatch in {path}")
    launches = report.get("launches", [])
    if not launches:
        raise RuntimeError(f"missing full-grid dispatch evidence in {path}")
    if limit <= 0:
        if int(report.get("executed_kernel_count", -1)) != len(launches):
            raise RuntimeError(f"executed-kernel count mismatch in {path}")
        for field in (
            "observed_sampling_coverage", "completed_sampling_coverage"
        ):
            if abs(float(report.get(field, -1)) - 1.0) > 1e-12:
                raise RuntimeError(
                    f"full-workload {field} is not one in {path}"
                )
    for launch in launches:
        requested = int(launch.get("requested_total_wg", 0))
        partitions = launch.get("partitions", [])
        if requested <= 0 or not partitions:
            raise RuntimeError(f"invalid launch mapping in {path}: {launch}")
        if launch.get("unified"):
            if int(partitions[0].get("begin", -1)) != 0:
                raise RuntimeError(f"unified partition does not start at zero in {path}")
            previous = 0
            for partition in partitions:
                begin = int(partition.get("begin", -1))
                end = int(partition.get("end", -1))
                if begin != previous or end < begin:
                    raise RuntimeError(f"non-contiguous unified partition in {path}")
                previous = end
            if previous != requested:
                raise RuntimeError(
                    f"unified partition covers {previous}, expected {requested} in {path}"
                )
            if launch.get("wg_filter_kind") != "original_unified_partition":
                raise RuntimeError(f"unexpected unified WGFilter in {path}")
        elif launch.get("wg_filter_kind") != "none":
            raise RuntimeError(f"ordinary launch uses a WGFilter in {path}")
    return {
        "path": str(path),
        # Version-1 frozen sidecars name the installed stopper in
        # ``stop_reason`` even when a smaller grid completes naturally.  Keep
        # that raw evidence and record the audited termination separately.
        "stop_reason": termination_mode,
        "report_stop_reason": report["stop_reason"],
        "max_wg": report["max_wg"],
        "observed_wg_count": report["observed_wg_count"],
        "requested_total_wg": report["requested_total_wg"],
        "completed_wg_count": report.get("completed_wg_count"),
        "executed_kernel_count": report.get(
            "executed_kernel_count", len(launches)),
        "observed_sampling_coverage": report.get(
            "observed_sampling_coverage"),
        "completed_sampling_coverage": report.get(
            "completed_sampling_coverage"),
        "global_wg_set_sha256": report["global_wg_set_sha256"],
        "per_gpu": per_gpu,
        "launches": launches,
    }


def write_cell_result(exp, result):
    path = cell_result_path(exp)
    payload = {
        "version": 1,
        "target": exp["target"],
        "benchmark": exp["benchmark"],
        "configuration": exp["config_name"],
        **result,
    }
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(
        json.dumps(payload, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    temporary.replace(path)
    return path


def create_output_dir():
    global output_dir
    output_dir = os.path.join(
        ROOT_DIR,
        "results",
        datetime.now().strftime("%Y-%m-%d-%H-%M-%S-sampled-validation"),
    )

    results_dir = os.path.join(ROOT_DIR, "results")
    if not os.path.exists(results_dir):
        os.makedirs(results_dir)

    if not os.path.exists(output_dir):
        os.makedirs(output_dir)


def experiment_command(exp):
    binary = exp.get("binary_path") or os.path.join(
        ROOT_DIR, exp["target"], exp["target"])
    file_stem = exp_file_stem(exp)
    metric_file_name = f"{file_stem}_metrics"
    cmd = [
        binary,
        f'-benchmark={exp["benchmark"]}',
        *exp["common_flags"],
        *exp["flags"],
        f"-metric-file-name={metric_file_name}",
    ]
    if exp.get("trace_sharing"):
        cmd.extend([
            "-trace-sharing",
            f"-trace-sharing-file={file_stem}_sharing.csv.gz",
            f'-trace-sharing-sample={exp["trace_sharing_sample"]}',
            f'-trace-sharing-max-records={exp["trace_sharing_max_records"]}',
        ])
    if exp.get("trace_remote_origin"):
        cmd.extend([
            "-trace-remote-origin",
            f"-trace-remote-origin-file={file_stem}_remote_origin",
            (
                "-trace-remote-origin-max-records="
                f'{exp["trace_remote_origin_max_records"]}'
            ),
        ])
    if exp.get("trace_memory_path"):
        cmd.extend([
            "-trace-memory-path",
            f"-trace-memory-path-file={file_stem}_memory_path",
            (
                "-trace-memory-path-warmup-accesses="
                f'{exp["trace_memory_path_warmup_accesses"]}'
            ),
            (
                "-trace-memory-path-max-records="
                f'{exp["trace_memory_path_max_records"]}'
            ),
        ])
        if exp.get("trace_memory_path_exit_on_complete"):
            cmd.append("-trace-memory-path-exit-on-complete")
    if exp.get("trace_observation"):
        cmd.extend([
            "-trace-observation",
            f"-trace-observation-file={file_stem}_observation",
            (
                "-trace-observation-warmup-accesses="
                f'{exp["trace_observation_warmup_accesses"]}'
            ),
            (
                "-trace-observation-max-records="
                f'{exp["trace_observation_max_records"]}'
            ),
            (
                "-trace-observation-dram-warmup-accesses="
                f'{exp["trace_observation_dram_warmup_accesses"]}'
            ),
            (
                "-trace-observation-dram-max-records="
                f'{exp["trace_observation_dram_max_records"]}'
            ),
            (
                "-trace-observation-remote-warmup-requests="
                f'{exp["trace_observation_remote_warmup_requests"]}'
            ),
            (
                "-trace-observation-remote-max-records="
                f'{exp["trace_observation_remote_max_records"]}'
            ),
            (
                "-trace-observation-l2-sample-max="
                f'{exp["trace_observation_l2_sample_max"]}'
            ),
        ])
        if exp.get("trace_observation_exit_on_complete"):
            cmd.append("-trace-observation-exit-on-complete")
    return cmd


def run_exp(exp):
    cmd = experiment_command(exp)
    cmd_str = shlex.join(cmd)
    print(cmd_str)

    file_stem = exp_file_stem(exp)
    metric_file_name = f"{file_stem}_metrics"
    out_file_name = f"{file_stem}_out.stdout"

    with open(out_file_name, "w") as out_file:
        out_file.write(f"Executing {cmd_str}\n")
        start_time = datetime.now()
        out_file.write(f"Start time: {start_time}\n")
        out_file.flush()

        process = start_process(
            cmd,
            stdout=out_file,
            stderr=out_file,
            cwd=ROOT_DIR,
            start_new_session=True,
        )
        if process is None:
            out_file.write("Cancelled before process start\n")
            return {
                "exp": exp,
                "returncode": -signal.SIGTERM,
                "cancelled": True,
            }
        timed_out = wait_for_experiment(exp, process)

        end_time = datetime.now()
        out_file.write(f"Return code: {process.returncode}\n")
        if timed_out:
            out_file.write(
                f'Timed out after {exp.get("timeout_seconds", 0)} seconds\n')
        out_file.write(f"End time: {end_time}\n")
        out_file.write(f"Elapsed time: {end_time - start_time}\n")

    result = exp_result(
        exp,
        cmd_str,
        metric_file_name,
        end_time - start_time,
        timed_out,
        process.returncode,
    )
    result["cell_result"] = str(write_cell_result(exp, {
        key: value for key, value in result.items() if key != "exp"
    }))
    return result


def wait_for_experiment(exp, process):
    timeout_seconds = exp.get("timeout_seconds", 0)
    timed_out = False
    try:
        try:
            process.wait(
                timeout=timeout_seconds if timeout_seconds > 0 else None)
        except subprocess.TimeoutExpired:
            timed_out = True
            terminate_process(process)
    finally:
        unregister_process(process)
    return timed_out


def exp_result(
    exp,
    cmd_str,
    metric_file_name,
    elapsed_time,
    timed_out,
    returncode,
):
    host_elapsed_seconds = elapsed_time.total_seconds()
    if timed_out:
        print(f"Timed out executing {cmd_str}")
        return {
            "exp": exp,
            "returncode": -9,
            "timeout": exp.get("timeout_seconds", 0),
            "host_elapsed_seconds": host_elapsed_seconds,
            "success": False,
        }

    if returncode != 0:
        print(f"Error executing {cmd_str}")
        return {
            "exp": exp,
            "returncode": returncode,
            "host_elapsed_seconds": host_elapsed_seconds,
            "success": False,
        }

    metrics_csv = metric_file_name + ".csv"
    if not os.path.exists(metrics_csv):
        print(f"Missing metrics file for {cmd_str}: {metrics_csv}")
        return {
            "exp": exp,
            "returncode": -1,
            "missing_metrics": metrics_csv,
            "host_elapsed_seconds": host_elapsed_seconds,
            "success": False,
        }

    print(f"Executed {cmd_str}, time {elapsed_time}")
    return {
        "exp": exp,
        "returncode": 0,
        "simulator_returncode": returncode,
        "metrics": metrics_csv,
        "host_elapsed_seconds": host_elapsed_seconds,
        "success": True,
    }


def dry_run_commands(exps):
    for exp in exps:
        print(shlex.join(experiment_command(exp)))
