from datetime import datetime
import os
import shlex
import signal
import subprocess
import threading

from runall2_constants import ROOT_DIR

output_dir = ""
running_processes = set()
running_processes_lock = threading.Lock()


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


def register_process(process):
    with running_processes_lock:
        running_processes.add(process)


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
    with running_processes_lock:
        processes = list(running_processes)

    for process in processes:
        terminate_process(process)


def install_signal_handlers():
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


def exp_file_stem(exp):
    return os.path.join(
        output_dir,
        f'{exp["target"]}_{exp["benchmark"]}_{exp["config_name"]}',
    )


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
    binary = os.path.join(ROOT_DIR, exp["target"], exp["target"])
    metric_file_name = f"{exp_file_stem(exp)}_metrics"
    return [
        binary,
        f'-benchmark={exp["benchmark"]}',
        *exp["common_flags"],
        *exp["flags"],
        f"-metric-file-name={metric_file_name}",
    ]


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

        process = subprocess.Popen(
            cmd,
            stdout=out_file,
            stderr=out_file,
            cwd=ROOT_DIR,
            start_new_session=True,
        )
        timed_out = wait_for_experiment(exp, process)

        end_time = datetime.now()
        out_file.write(f"Return code: {process.returncode}\n")
        if timed_out:
            out_file.write(
                f'Timed out after {exp.get("timeout_seconds", 0)} seconds\n')
        out_file.write(f"End time: {end_time}\n")
        out_file.write(f"Elapsed time: {end_time - start_time}\n")

    return exp_result(
        exp,
        cmd_str,
        metric_file_name,
        end_time - start_time,
        timed_out,
        process.returncode,
    )


def wait_for_experiment(exp, process):
    register_process(process)
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
    if timed_out:
        print(f"Timed out executing {cmd_str}")
        return {
            "exp": exp,
            "returncode": -9,
            "timeout": exp.get("timeout_seconds", 0),
        }

    if returncode != 0:
        print(f"Error executing {cmd_str}")
        return {"exp": exp, "returncode": returncode}

    metrics_csv = metric_file_name + ".csv"
    if not os.path.exists(metrics_csv):
        print(f"Missing metrics file for {cmd_str}: {metrics_csv}")
        return {"exp": exp, "returncode": -1, "missing_metrics": metrics_csv}

    print(f"Executed {cmd_str}, time {elapsed_time}")
    return {"exp": exp, "returncode": 0}


def dry_run_commands(exps):
    for exp in exps:
        print(shlex.join(experiment_command(exp)))
