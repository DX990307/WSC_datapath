#!/usr/bin/env python3
import argparse
import concurrent.futures
import csv
import os
import struct
import subprocess
import sys
import tempfile
import time
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_LOG_DIR = Path(os.environ.get("TMPDIR", "/tmp")) / "tier2-hsaco-logs"
DEFAULT_DEFINES = [
    item.strip()
    for item in os.environ.get("HSACO_DEFINES", "SINGLE_PRECISION").split(",")
    if item.strip()
]
IGNORED_DIR_NAMES = {"__pycache__", "compile_logs"}
CLANG_OFFLOAD_BUNDLE_MAGIC = b"__CLANG_OFFLOAD_BUNDLE__"


def default_rocm_device_lib_path():
    env_path = os.environ.get("ROCM_DEVICE_LIB_PATH")
    if env_path:
        return env_path

    candidates = [Path("/usr/lib/x86_64-linux-gnu/amdgcn/bitcode")]
    for rocm_root in sorted(Path("/opt").glob("rocm*")):
        candidates.extend(sorted(
            rocm_root.glob("lib/llvm/lib/clang/*/lib/amdgcn/bitcode")
        ))

    for path in candidates:
        if path.is_dir():
            return str(path)

    return str(candidates[0])


def parse_csv(value):
    if not value:
        return []
    return [item.strip() for item in value.split(",") if item.strip()]


def discover_benchmarks(root):
    benchmarks = []
    for child in root.iterdir():
        if (
            child.is_dir()
            and not child.name.startswith(".")
            and child.name not in IGNORED_DIR_NAMES
        ):
            benchmarks.append(child)
    return sorted(benchmarks, key=lambda path: path.name)


def select_benchmarks(root, only):
    benchmarks = discover_benchmarks(root)
    if not only:
        return benchmarks

    wanted = set(parse_csv(only))
    selected = [path for path in benchmarks if path.name in wanted]
    found = {path.name for path in selected}
    missing = sorted(wanted - found)
    if missing:
        raise SystemExit(f"unknown benchmark(s): {', '.join(missing)}")
    return selected


def find_opencl_sources(bench_dir):
    candidates = []
    for pattern in ("sim/*.cl", "native/*.cl", "*.cl"):
        candidates.extend(sorted(bench_dir.glob(pattern)))
    seen = set()
    unique = []
    for path in candidates:
        resolved = path.resolve()
        if resolved in seen:
            continue
        seen.add(resolved)
        unique.append(path)
    return unique


def find_hip_sources(bench_dir):
    candidates = []
    for pattern in ("sim/*.hip", "native/*.hip", "*.hip"):
        candidates.extend(sorted(bench_dir.glob(pattern)))
    seen = set()
    unique = []
    for path in candidates:
        resolved = path.resolve()
        if resolved in seen:
            continue
        seen.add(resolved)
        unique.append(path)
    return unique


def find_sources(bench_dir, args):
    if args.source_kind in ("auto", "opencl"):
        opencl_sources = find_opencl_sources(bench_dir)
        if opencl_sources or args.source_kind == "opencl":
            return [("opencl", src) for src in opencl_sources]

    if args.source_kind in ("auto", "hip"):
        hip_sources = find_hip_sources(bench_dir)
        if hip_sources or args.source_kind == "hip":
            return [("hip", src) for src in hip_sources]

    return []


def infer_output_name(src):
    if src.suffix.lower() == ".hip":
        return "kernels.hsaco"
    stem = src.stem
    if stem.lower() in ("kernel", "kernels"):
        return "kernels.hsaco"
    return f"{stem}.hsaco"


def output_path_for(root, bench_dir, src, args):
    output_name = args.output_name or infer_output_name(src)
    if args.output_root:
        out_dir = Path(args.output_root).resolve() / bench_dir.relative_to(root)
    else:
        out_dir = bench_dir
    return out_dir / output_name


def compile_commands(src, obj, out, args):
    compile_cmd = [
        args.clang,
        "-x",
        "cl",
        "-target",
        args.target,
        f"-mcpu={args.gpu}",
        f"-mcode-object-version={args.code_object_version}",
        f"-cl-std={args.opencl_std}",
        f"--rocm-device-lib-path={args.rocm_device_lib_path}",
        "-c",
        str(src),
        "-o",
        str(obj),
    ]
    for include_dir in args.include_dirs:
        compile_cmd.extend(["-I", str(include_dir)])
    for define in args.defines:
        compile_cmd.append(f"-D{define}")

    link_cmd = [
        args.ld_lld,
        "-shared",
        str(obj),
        "-o",
        str(out),
    ]
    return compile_cmd, link_cmd


def hip_compile_command(src, out, args):
    compile_cmd = [
        args.hipcc,
        "--genco",
        f"--offload-arch={args.hip_gpu}",
        str(src),
        "-o",
        str(out),
    ]
    for include_dir in args.include_dirs:
        compile_cmd.extend(["-I", str(include_dir)])
    for define in args.defines:
        compile_cmd.append(f"-D{define}")
    compile_cmd.extend(args.hip_extra_args)
    return compile_cmd


def command_string(cmd):
    return " ".join(str(part) for part in cmd)


def run_command(cmd, log):
    log.write("$ " + command_string(cmd) + "\n")
    log.flush()
    return subprocess.run(
        cmd,
        stdout=log,
        stderr=subprocess.STDOUT,
        text=True,
    ).returncode


def extract_clang_offload_bundle_if_needed(path, gpu, log):
    data = path.read_bytes()
    if not data.startswith(CLANG_OFFLOAD_BUNDLE_MAGIC):
        return 0

    pos = len(CLANG_OFFLOAD_BUNDLE_MAGIC)
    if len(data) < pos + 8:
        log.write("invalid clang offload bundle: missing entry count\n")
        return 1

    num_entries = struct.unpack_from("<Q", data, pos)[0]
    pos += 8
    entries = []
    for _ in range(num_entries):
        if len(data) < pos + 24:
            log.write("invalid clang offload bundle: truncated entry header\n")
            return 1
        offset, size, triple_len = struct.unpack_from("<QQQ", data, pos)
        pos += 24
        if len(data) < pos + triple_len:
            log.write("invalid clang offload bundle: truncated triple\n")
            return 1
        triple = data[pos:pos + triple_len].decode("utf-8", errors="replace")
        pos += triple_len
        entries.append((offset, size, triple))

    preferred = [
        entry for entry in entries
        if "amdgcn" in entry[2] and gpu in entry[2] and entry[1] > 0
    ]
    if not preferred:
        preferred = [
            entry for entry in entries
            if "amdgcn" in entry[2] and entry[1] > 0
        ]
    if not preferred:
        log.write("invalid clang offload bundle: no AMDGPU entry found\n")
        return 1

    offset, size, triple = preferred[0]
    if offset + size > len(data):
        log.write(
            "invalid clang offload bundle: entry outside file "
            f"offset={offset} size={size} file_size={len(data)}\n"
        )
        return 1

    extracted = data[offset:offset + size]
    if not extracted.startswith(b"\x7fELF"):
        log.write(
            "invalid clang offload bundle: extracted entry is not ELF "
            f"triple={triple}\n"
        )
        return 1

    path.write_bytes(extracted)
    log.write(f"extracted clang offload bundle entry: {triple}\n")
    return 0


def compile_source(root, bench_dir, source_kind, src, args, log):
    out = output_path_for(root, bench_dir, src, args)
    out.parent.mkdir(parents=True, exist_ok=True)
    include_dirs = list(args.include_dirs)
    include_dirs.extend([src.parent, bench_dir])
    args_for_source = argparse.Namespace(**vars(args))
    args_for_source.include_dirs = include_dirs

    if source_kind == "hip":
        compile_cmd = hip_compile_command(src, out, args_for_source)
        if args.dry_run:
            log.write("$ " + command_string(compile_cmd) + "\n")
            return 0, out
        rc = run_command(compile_cmd, log)
        if rc != 0:
            return rc, out
        rc = extract_clang_offload_bundle_if_needed(
            out, args_for_source.hip_gpu, log)
        return rc, out

    with tempfile.TemporaryDirectory(prefix="hsaco-", dir=args.tmp_dir) as tmp:
        obj = Path(tmp) / f"{src.stem}.o"
        compile_cmd, link_cmd = compile_commands(src, obj, out, args_for_source)

        if args.dry_run:
            log.write("$ " + command_string(compile_cmd) + "\n")
            log.write("$ " + command_string(link_cmd) + "\n")
            return 0, out

        rc = run_command(compile_cmd, log)
        if rc != 0:
            return rc, out
        rc = run_command(link_cmd, log)
        return rc, out


def run_one(root, bench_dir, args, log_dir):
    start = time.time()
    sources = find_sources(bench_dir, args)
    log_path = log_dir / f"{bench_dir.name}.log"

    if not sources:
        return {
            "benchmark": bench_dir.name,
            "status": "missing_source",
            "returncode": 0,
            "seconds": 0.0,
            "sources": "",
            "outputs": "",
            "log": "",
        }

    outputs = []
    status = "ok"
    returncode = 0

    if not args.dry_run:
        log_dir.mkdir(parents=True, exist_ok=True)

    with log_path.open("w", encoding="utf-8", errors="replace") as log:
        for source_kind, src in sources:
            rc, out = compile_source(
                root, bench_dir, source_kind, src, args, log)
            outputs.append(str(out))
            if rc != 0:
                status = "fail"
                returncode = rc
                if args.stop_source_on_failure:
                    break

    return {
        "benchmark": bench_dir.name,
        "status": status,
        "returncode": returncode,
        "seconds": time.time() - start,
        "sources": ";".join(f"{kind}:{src}" for kind, src in sources),
        "outputs": ";".join(outputs),
        "log": str(log_path),
    }


def write_summary(results, log_dir):
    summary_path = log_dir / "summary.csv"
    log_dir.mkdir(parents=True, exist_ok=True)
    with summary_path.open("w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(
            f,
            fieldnames=[
                "benchmark",
                "status",
                "returncode",
                "seconds",
                "sources",
                "outputs",
                "log",
            ],
        )
        writer.writeheader()
        for result in results:
            writer.writerow({
                "benchmark": result["benchmark"],
                "status": result["status"],
                "returncode": result["returncode"],
                "seconds": f"{result['seconds']:.2f}",
                "sources": result["sources"],
                "outputs": result["outputs"],
                "log": result["log"],
            })
    return summary_path


def print_result(result):
    duration = f"{result['seconds']:.1f}s"
    if result["status"] == "missing_source":
        print(f"[missing_source] {result['benchmark']}")
    elif result["status"] == "ok":
        print(f"[ok] {result['benchmark']} ({duration}) -> {result['outputs']}")
    else:
        print(
            f"[fail] {result['benchmark']} ({duration}) "
            f"log={result['log']}"
        )


def selected_source_kinds(benchmarks, args):
    kinds = set()
    for bench_dir in benchmarks:
        for source_kind, _ in find_sources(bench_dir, args):
            kinds.add(source_kind)
    return kinds


def validate_tool(tool):
    if subprocess.run(
        ["bash", "-lc", f"command -v {tool}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    ).returncode != 0:
        raise SystemExit(f"missing required tool: {tool}")


def validate_tools(args, source_kinds):
    if "opencl" in source_kinds:
        for tool in (args.clang, args.ld_lld):
            validate_tool(tool)

    if "hip" in source_kinds:
        validate_tool(args.hipcc)

    if "opencl" in source_kinds:
        if not Path(args.rocm_device_lib_path).is_dir():
            raise SystemExit(
                "missing ROCm device libs: "
                f"{args.rocm_device_lib_path}"
            )


def parse_args():
    parser = argparse.ArgumentParser(
        description=(
            "Compile OpenCL/HIP simulator kernels into MGPUSim .hsaco files."
        )
    )
    parser.add_argument(
        "--root",
        default=str(SCRIPT_DIR),
        help="Benchmark root whose immediate subdirectories are scanned.",
    )
    parser.add_argument(
        "--only",
        default="",
        help="Comma-separated benchmark directory names to compile.",
    )
    parser.add_argument(
        "--jobs",
        type=int,
        default=1,
        help="Number of benchmarks to compile in parallel.",
    )
    parser.add_argument(
        "--output-root",
        default="",
        help=(
            "Optional output root. If omitted, .hsaco files are written into "
            "each benchmark directory."
        ),
    )
    parser.add_argument(
        "--output-name",
        default="",
        help="Optional fixed .hsaco filename for every source.",
    )
    parser.add_argument(
        "--log-dir",
        default=str(DEFAULT_LOG_DIR),
        help="Directory for per-benchmark compile logs.",
    )
    parser.add_argument(
        "--source-kind",
        choices=["auto", "opencl", "hip"],
        default=os.environ.get("HSACO_SOURCE_KIND", "auto"),
        help=(
            "Source type to compile. auto prefers .cl when present and falls "
            "back to .hip."
        ),
    )
    parser.add_argument("--clang", default=os.environ.get("CLANG", "clang-14"))
    parser.add_argument("--ld-lld", default=os.environ.get("LD_LLD", "ld.lld-14"))
    parser.add_argument("--hipcc", default=os.environ.get("HIPCC", "hipcc"))
    parser.add_argument(
        "--rocm-device-lib-path",
        default=default_rocm_device_lib_path(),
    )
    parser.add_argument(
        "--target",
        default=os.environ.get("TARGET", "amdgcn-amd-amdhsa"),
    )
    parser.add_argument("--gpu", default=os.environ.get("GPU", "fiji"))
    parser.add_argument(
        "--code-object-version",
        default=os.environ.get("CODE_OBJECT_VERSION", "2"),
    )
    parser.add_argument("--opencl-std", default=os.environ.get("OPENCL_STD", "CL2.0"))
    parser.add_argument("--hip-gpu", default=os.environ.get("HIP_GPU", "gfx803"))
    parser.add_argument(
        "--hip-extra-arg",
        dest="hip_extra_args",
        action="append",
        default=[],
        help="Extra argument passed to hipcc. Can be repeated.",
    )
    parser.add_argument(
        "--include-dir",
        dest="include_dirs",
        action="append",
        default=[],
        help="Extra include directory. Can be repeated.",
    )
    parser.add_argument(
        "--define",
        dest="defines",
        action="append",
        default=DEFAULT_DEFINES,
        help="Preprocessor definition passed as -DNAME or -DNAME=VALUE.",
    )
    parser.add_argument(
        "--tmp-dir",
        default=os.environ.get("TMPDIR", "/tmp"),
        help="Temporary object directory parent.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print compile/link commands into logs without running them.",
    )
    parser.add_argument(
        "--fail-on-missing-source",
        action="store_true",
        help="Return non-zero if a selected benchmark has no .cl/.hip source.",
    )
    parser.add_argument(
        "--stop-source-on-failure",
        action="store_true",
        help="Stop compiling more sources in a benchmark after one source fails.",
    )
    parser.add_argument(
        "--stop-on-failure",
        action="store_true",
        help="Stop submitting new benchmarks after the first failed benchmark.",
    )
    return parser.parse_args()


def main():
    args = parse_args()
    if args.jobs < 1:
        raise SystemExit("--jobs must be >= 1")
    args.include_dirs = [Path(path).resolve() for path in args.include_dirs]

    root = Path(args.root).resolve()
    benchmarks = select_benchmarks(root, args.only)
    if not benchmarks:
        raise SystemExit("no benchmarks found")

    source_kinds = selected_source_kinds(benchmarks, args)
    if not args.dry_run:
        validate_tools(args, source_kinds)

    log_dir = Path(args.log_dir).resolve()
    log_dir.mkdir(parents=True, exist_ok=True)

    print(f"Benchmark root: {root}")
    print(f"Benchmarks: {len(benchmarks)}")
    print(f"Source kinds: {','.join(sorted(source_kinds)) or 'none'}")
    print(f"OpenCL GPU target: {args.gpu}")
    print(f"HIP GPU target: {args.hip_gpu}")
    print(f"Logs: {log_dir}")
    if args.output_root:
        print(f"Output root: {Path(args.output_root).resolve()}")

    results = []
    failed = False

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as executor:
        future_to_bench = {}
        pending = list(benchmarks)

        while pending or future_to_bench:
            while pending and len(future_to_bench) < args.jobs and not (
                failed and args.stop_on_failure
            ):
                bench = pending.pop(0)
                future = executor.submit(run_one, root, bench, args, log_dir)
                future_to_bench[future] = bench

            if not future_to_bench:
                break

            done, _ = concurrent.futures.wait(
                future_to_bench,
                return_when=concurrent.futures.FIRST_COMPLETED,
            )
            for future in done:
                future_to_bench.pop(future)
                result = future.result()
                results.append(result)
                print_result(result)
                if result["status"] == "fail":
                    failed = True

    results.sort(key=lambda item: item["benchmark"])
    summary_path = write_summary(results, log_dir)
    print(f"Summary: {summary_path}")

    num_ok = sum(1 for result in results if result["status"] == "ok")
    num_fail = sum(1 for result in results if result["status"] == "fail")
    num_missing = sum(
        1 for result in results if result["status"] == "missing_source"
    )
    print(f"Done: ok={num_ok} missing_source={num_missing} failed={num_fail}")

    if num_fail:
        return 1
    if args.fail_on_missing_source and num_missing:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
