# Photon / Wafer-Scale GPU Handoff Notes

Date: 2026-05-12

This document summarizes the current state of the work so it can be moved to a
new branch for next week's development. It is intentionally detailed and
practical: file paths, command examples, experiment folders, known problems, and
next-step priorities are included.

## 1. High-Level Goal

The overall goal of this line of work is to integrate Photon-style sampled GPU
simulation into the wafer-scale GPU model, validate that it works on small and
large wafer-scale configurations, and then extend the workload set toward LLM
inference workloads.

The current research direction has three connected parts:

1. Clean up the translation / TLB stack so the baseline wafer-scale GPU model is
   simpler and easier to reason about.
2. Port Photon sampling mechanisms into the vanilla MGPUSim/Akita stack and
   validate them on wafer-scale GPU configurations.
3. Add larger and more realistic workloads, especially LLM-style workloads such
   as KV cache decode, and later full transformer-layer components.

## 2. Repository Context

Current working directory:

```bash
/home/daoxuanxu/vanilla
```
Important top-level directories:

```text
akita/                     Akita memory system, MMU, TLB, L2 TLB, NoC, etc.
mgpusim/                   MGPUSim simulator, benchmarks, samples, Photon sampling code.
akkalat/                   Wafer-scale GPU evaluation harness and run scripts.
photon/                    Reference Photon repo/submodules used for porting.
```

This directory is not always seen by `git` as a normal worktree from the repo
root. Earlier, `git status` from `/home/daoxuanxu/vanilla` returned:

```text
fatal: not a git repository (or any of the parent directories): .git
```

So when moving this work to a new branch, do not assume normal `git diff` from
the root will work unless the branch/worktree is set up correctly. In previous
push attempts, the bare-git style command was used:

```bash
git --git-dir=/tmp/wsg-baseline-model.git \
    --work-tree=/home/daoxuanxu/vanilla \
    status
```

or push with a specific SSH key:

```bash
git -c core.sshCommand="ssh -i /home/daoxuanxu/.ssh/id_ed25519_github_wsg -o IdentitiesOnly=yes" \
    --git-dir=/tmp/wsg-baseline-model.git \
    --work-tree=/home/daoxuanxu/vanilla \
    push origin HEAD:photon
```

## 3. Build Environment Notes

Go builds often need two extra settings in this environment:

1. Use `/tmp/gocache` because the default home Go cache can be read-only.
2. Use `-buildvcs=false` because the workspace may not have valid VCS metadata.

Recommended pattern:

```bash
GOCACHE=/tmp/gocache go build -buildvcs=false ./...
GOCACHE=/tmp/gocache go test -buildvcs=false ./...
```

For `akkalat/baseline`:

```bash
cd /home/daoxuanxu/vanilla/akkalat/baseline
GOCACHE=/tmp/gocache go build -buildvcs=false
```

The earlier error was:

```text
error obtaining VCS status: exit status 128
Use -buildvcs=false to disable VCS stamping.
```

This is not a simulator bug. It is Go trying to stamp VCS metadata into the
binary while the checkout/bare worktree metadata is not usable.

## 4. Translation / TLB Cleanup Work

The conversation started from the MMU and TLB stack.

Relevant paths:

```text
akita/mem/vm/mmu/
akita/mem/vm/mmuCache/
akita/mem/vm/mmuTLB/
akita/mem/vm/tlb/
akita/mem/vm/tlb_gmmu/      historical naming / old global TLB path
akita/mem/vm/l2tlb/         new L2 TLB path
akkalat/baseline/runner/
```

### 4.1 Original MMU Question

The first code focus was:

```go
func (mmu *MMU) coalescedUpperLatency(req *vm.TranslationReq) uint64 {
    upperLatency := req.TransLatency
    if !mmu.walkCoalescingEnabled {
        return upperLatency
    }

    for _, walking := range mmu.walkingTranslations {
        if walking.req == nil || walking.req.PID != req.PID {
            continue
        }

        if mmu.isInTheSameLastLevel(req.VAddr, walking.req.VAddr) {
            mmu.lastLevelCoalescedCount++
            return 0
        }

        if mmu.isInTheSameTwoLevels(req.VAddr, walking.req.VAddr) &&
            upperLatency > 100 {
            mmu.twoLevelCoalescedCount++
            upperLatency = 100
        }
    }

    return upperLatency
}
```

The user wanted to delete this function and simplify the page-walk behavior.

### 4.2 Cacheline / Multi-Return Removal

There was also a feature where the MMU/cache path could return a cacheline-like
group of translations, e.g. "一次 return 8 个". The request was to remove that
feature and make translation return one entry at a time.

Conceptual direction:

```text
old behavior: one request may return several nearby translations
new behavior: one request returns exactly one translation
```

This matters for clean baseline experiments because bundled translation returns
can hide some TLB/MMU behavior and make comparison with Photon harder.

### 4.3 PTCL / Prefetch Cleanup

The user then asked to remove the PTCL system from Akita and also remove the
mmuTLB prefetcher.

Important note for next branch:

```bash
rg -n "PTCL|ptcl|prefetch|cacheline|coalesc|walkCoalesc" akita/mem/vm akkalat mgpusim
```

At the time this document was written, there are still some coalescing-related
symbols in the current tree:

```text
akita/mem/vm/mmu/mmu.go
akita/mem/vm/mmu/builder.go
akita/mem/vm/mmu/stats.go
akkalat/baseline/runner/flag.go
akkalat/baseline/runner/report.go
```

So before claiming "all coalescing/PTCL code is gone" in a paper/report, audit
the remaining symbols. Some may now only be stats/flags, but they should be
checked.

### 4.4 MSHR Change

The user asked to change the MSHR to follow the `tlb` package's MSHR style.

Relevant current files:

```text
akita/mem/vm/tlb/tlbmshr.go
akita/mem/vm/mmuTLB/tlbmshr copy.go
akita/mem/vm/l2tlb/mshr.go
```

There are still files with names like `tlbmshr copy.go`; these may be old
scratch files and should be audited before a clean branch is created.

Recommended audit:

```bash
find akita/mem/vm -name '*copy*' -print
rg -n "type .*MSHR|mshr|MSHR" akita/mem/vm/tlb akita/mem/vm/mmuTLB akita/mem/vm/l2tlb
```

### 4.5 TLB GMMU -> L2 TLB

The user wanted to abandon the old L2 TLB design and repurpose `tlb_gmmu` into
the new L2 TLB.

The current tree now has:

```text
akita/mem/vm/l2tlb/
```

with files such as:

```text
builder.go
l2tlb.go
mshr.go
stats.go
tlbprotocol.go
tlb_test.go
l2tlb_external_suite_test.go
```

This should be treated as the new L2 TLB path for future work. The next branch
should verify which runner/platform files instantiate this L2 TLB and whether
any old `tlb_gmmu` imports remain.

Recommended audit:

```bash
rg -n "tlb_gmmu|l2tlb|mmuTLB|MMUTLB" akita mgpusim akkalat
```

## 5. `akkalat` Runner / Script Work

The main script is:

```text
akkalat/runall2.py
```

Current core behavior:

```python
TARGETS = ["baseline"]
MAX_WORKERS = 15
```

Current default `ALL_BENCHMARKS` is not the old full benchmark list. Many
classical benchmarks are commented out. Current enabled list is:

```text
kvcache
kvcache-decode
kvcache-decode-30b
matrixmultiplication
matrixmultiplication-middletile
matrixtranspose
matrixtranspose-middletile
relu
```

If the next branch needs to run every benchmark again, uncomment or re-add:

```text
aes
atax
bicg
bitonicsort
conv2d
fft
fastwalshtransform
fir
floydwarshall
im2col
kmeans
nw
pagerank
simpleconvolution
spmv
stencil2d
```

### 5.1 Baseline Coalescing Removal from Runs

Earlier `runall` generated both `baseline` and `baseline_coalescing` style
results. The user asked to remove `baseline_coalescing`. The intended direction
is:

```text
baseline only
sample_* Photon configs separately
no old coalescing config in the default run matrix
```

### 5.2 Configs

Current `CONFIGS` in `runall2.py`:

```python
CONFIGS = [
    ("baseline", []),
    ("sample_all", ["-sampled", "-branch-sampled", "-kernel-sampled"]),
    ("sample_wf", ["-sampled"]),
    ("sample_branch", ["-branch-sampled"]),
    ("sample_kernel", ["-kernel-sampled"]),
    ("sample_loop", ["-loop-sampled"]),
]
```

Meanings:

```text
baseline       Full detailed simulation. No Photon skip.
sample_wf      WF/WG-level timing sampling. Uses warmup/granularity.
sample_branch  Branch/basic-block-level sampling.
sample_kernel  Kernel-level history-table sampling.
sample_all     sample_wf + sample_branch + sample_kernel together.
sample_loop    Prototype loop-level sampling.
```

### 5.3 Common Flags

Current `BASE_COMMON_FLAGS`:

```python
BASE_COMMON_FLAGS = [
    "-timing",
    "-num-memory-banks=16",
    "-bandwidth=48",
    "-switch-latency=32",
    "-magic-memory-copy",
    "-report-all",
]
```

Then `runall2.py` adds:

```text
-mmutlb-lookup-latency=<value>
```

Default:

```text
DEFAULT_MMUTLB_LOOKUP_LATENCY = 80
```

### 5.4 Sampled Sweep Defaults

Current default sampled sweep grid:

```python
DEFAULT_SAMPLED_SWEEP_WARMUPS = [64, 128, 256, 512, 1024, 2048, 4096]
DEFAULT_SAMPLED_SWEEP_GRANULARITIES = [128, 256, 512, 1024, 2048, 4096, 8192]
```

So a full sampled sweep can become very large:

```text
7 warmups * 7 granularities = 49 sampled points per sampled config per benchmark
```

The user specifically requested these values:

```bash
--sampled-warmups 64,128,256,512,1024,2048,4096 \
--sampled-granularities 128,256,512,1024,2048,4096,8192
```

### 5.5 Balanced Sweep

Current balanced sweep constants:

```python
BALANCED_SAMPLED_SWEEP_WARMUPS = [128, 512, 1024]
BALANCED_SAMPLED_SWEEP_GRANULARITIES = [512, 1024]
BALANCED_SAMPLED_THRESHOLD = 0.02
BALANCED_BRANCH_COVERAGE_THRESHOLD = 0.98
BALANCED_BRANCH_LEAST_SQUARE_THRESHOLD = 0.005
BALANCED_KERNEL_DISTANCE_THRESHOLD = 8
```

This is a smaller grid intended for accuracy/speed tradeoff runs.

### 5.6 Parallelism

The user wanted `max_workers=15`.

Current default:

```python
MAX_WORKERS = 15
DEFAULT_SAMPLED_PARALLEL_LIMIT = MAX_WORKERS
```

Earlier, sampled sweeps were forced to `max_workers=1` because long sampled
runs were hanging near exit. Later the script was adjusted so sampled sweeps can
use parallel workers again, with a cap.

Useful flags:

```bash
--max-workers 15
--sampled-parallel-limit 15
--timeout-minutes 0
```

`--timeout-minutes 0` disables timeout.

### 5.7 Process Cleanup / Hang Near Exit

There was a long debugging sequence where sampled runs completed most of the
work but appeared to hang near the end. The user's intuition was that it might
be an at-exit / process cleanup problem.

The current `runall2.py` has explicit process tracking:

```python
running_processes = set()
running_processes_lock = threading.Lock()
register_process(process)
unregister_process(process)
terminate_process(process)
terminate_all_processes()
```

It uses process groups and sends SIGTERM/SIGKILL on timeout/exit.

When debugging future hangs:

```bash
pgrep -af baseline
ps -e -o pid,ppid,pgid,stat,etime,cmd | rg 'baseline|runall2'
```

Then inspect the end of the stdout:

```bash
tail -n 120 akkalat/results/<run>/baseline_<bench>_<config>_out.stdout
```

## 6. Photon Port

Photon reference repo:

```text
photon/
```

Important Photon directories:

```text
photon/akita/
photon/dnn/
photon/sampled-mgpu-sim/
```

Photon README describes it as a fork of MGPUSim with sampled GPU simulation
methodology from MICRO 2023.

### 6.1 Main Photon Mechanisms Ported / Investigated

The current vanilla tree contains Photon-style code under:

```text
mgpusim/samples/sampledrunner/
```

Important files:

```text
sampledengine.go       time-skipping engine and SampledTimeEngine
wgtracer.go            WF/WG-level sampling
branchtracer.go        branch/basic-block-level sampling and loop prototype
kernelsampled.go       kernel-level sampled history table
per_gpu.go             per-GPU sampled engine state
debug.go               Photon debug flags/log helpers
bb_model.go            basic-block timing model
looptracer_test.go     loop-sampling related test coverage
```

### 6.2 Per-GPU Sampling

The user pointed out that in `akkalat`, each GPU can have different features,
so Photon should be configured per GPU rather than globally.

Current file:

```text
mgpusim/samples/sampledrunner/per_gpu.go
```

It contains per-GPU maps:

```go
sampledEngines       map[uint64]*SampledEngine
branchSampledEngines map[uint64]*BranchSampledEngine
kernelSampledEngines map[uint64]*KernelSampledEngine
sampledTimeEngines   map[uint64]*SampledTimeEngine
```

Important functions:

```go
ClearGPUSampledEngines()
InitGPUSampledEngines(gpuID, engine, freq, staticComputeUnit)
SampledEngineForGPU(gpuID)
BranchSampledEngineForGPU(gpuID)
KernelSampledEngineForGPU(gpuID)
SampledTimeEngineForGPU(gpuID)
ResetGPUSampledEngines(gpuID)
```

The debug labels are per GPU:

```text
GPU0.WF
GPU0.Branch
GPU0.Kernel
GPU0.Time
```

This is important for wafer-scale systems because sampling stability may differ
across GPUs.

### 6.3 Photon Debug

Useful flags:

```bash
--photon-debug
--photon-verbose
```

These add simulator flags:

```text
-photon-debug
-photon-debug-verbose
```

Useful log phrases searched by `summarize_runall2.py`:

```text
[Photon]
wf engine enabled
branch engine enabled
kernel collect start
branch static analysis complete
sample analysis complete
wf sampled enabled
branch bbl solved
branch-level sampled start
wf sampled skip
branch sampled skip
kernel sampled wf marked skip
sampled wf queued
sampled wf completion fired
```

### 6.4 Sampling Levels

#### WF/WG-Level Sampling (`-sampled`)

This is the most important and currently most usable level.

Key flags:

```text
-sampled
-sampled-warmup=<N>
-sampled-granularity=<N>
-sampled-threshold=<float>
```

Current defaults in `wgtracer.go`:

```go
sampled-threshold    0.03
sampled-warmup       1024
sampled-granularity  2048
```

Interpretation:

```text
warmup      Number of detailed wavefronts/work items observed before attempting
            stable prediction.

granularity Length of the long stability window. Larger granularity is usually
            more stable but requires more detailed simulation before skipping.

threshold   Allowed deviation around stable rate. Smaller threshold is more
            conservative and usually more accurate but may reduce speedup.
```

The filename:

```text
baseline_aes_sample_all_w128_g8192_out.stdout
```

means:

```text
target:      baseline
benchmark:   aes
config:      sample_all
warmup:      128
granularity: 8192
stdout log
```

Important clarification from the conversation:

```text
g=4096 or g=8192 is a sampled window parameter for the run/config. It is not
"4096 workgroups per GPU" in the benchmark definition.
```

#### Branch-Level Sampling (`-branch-sampled`)

Branch sampling tries to detect stable basic-block behavior.

Current defaults:

```go
branch-sampled-coverage-threshold = 0.95
branch-sampled-threshold          = 0.01
```

Observed behavior:

In some logs the engine detected stable BBLs, but coverage did not reach 0.95,
so it did not enter true branch-level sampled skip. In that case simulated time
remains close to baseline because almost no work is skipped.

#### Kernel-Level Sampling (`-kernel-sampled`)

Kernel sampling uses history tables of previous kernel wavefront behavior.

Current defaults:

```go
kernel-sampled-threshold          = 32
kernel-sampled-distance-threshold = 16
```

It tries to find a suitable history table and replay/predict later wavefront
times based on previous kernel executions.

#### Loop-Level Sampling (`-loop-sampled`)

Loop-level sampling is a prototype added later.

Current flags:

```text
-loop-sampled
-loop-sampled-warmup=<N>
-loop-sampled-min-iters=<N>
-loop-sampled-threshold=<float>
```

Current defaults in `branchtracer.go`:

```go
loop-sampled-warmup    8
loop-sampled-min-iters 16
loop-sampled-threshold 0.03
```

This is not yet validated as strongly as WF-level sampling. It is a next-week
research item.

## 7. Important Concept Clarifications

### 7.1 WF vs WG

WG means workgroup. It is the OpenCL/CUDA-style group of work-items launched by
the kernel.

WF means wavefront. It is the hardware execution unit of lanes, similar to a
warp. On AMD-like GPUs this is often 64 lanes.

A single workgroup can contain one or more wavefronts depending on local size.

### 7.2 `sample_all` vs `sample_wf`

`sample_wf` enables only WF/WG-level sampling:

```text
-sampled
```

`sample_all` enables:

```text
-sampled -branch-sampled -kernel-sampled
```

So `sample_all` can use multiple skip mechanisms. However, this does not
guarantee better speedup. Extra analysis overhead and conservative thresholds
can make it slower or less effective for some benchmarks.

### 7.3 Simulated Time vs Wall Time

`Driver total_time` in metrics CSV is simulated application time.

`Elapsed time` in stdout is wall-clock time spent by the simulator process.

Photon is mainly intended to reduce wall-clock simulation cost while preserving
simulated-time accuracy. It can have:

```text
good wall-clock speedup + small simulated-time error
bad wall-clock speedup + small simulated-time error
good wall-clock speedup + large simulated-time error
```

The useful tradeoff is:

```text
low simulated-time error + high wall-clock speedup
```

### 7.4 "Photon wall"

When we said "Photon wall", it meant Photon run wall-clock time, not simulated
time. In plots, wall speedup is usually:

```text
baseline wall-clock time / Photon wall-clock time
```

## 8. Benchmark Size / Workgroup Findings

A recurring issue is that large memory footprint does not automatically mean
many workgroups.

For a 7x7 wafer-scale GPU:

```text
49 GPUs
32 CUs per GPU
10 resident workgroups per CU target
```

Very rough saturation target:

```text
49 * 32 * 10 = 15,680 resident workgroups
```

For Photon sampling, we need far more than just enough resident workgroups
because Photon first spends work on warmup and stability detection. If the
benchmark only has a small number of total workgroups, sampling may finish too
late and have too little remaining work to skip.

This explains the Matrix Multiplication / Matrix Transpose issue:

```text
Even if sampling finishes correctly, there may be too few remaining WGs.
Then Photon cannot amortize warmup and prediction overhead.
```

## 9. Matrix Multiplication / Matrix Transpose Workload Redesign

### 9.1 Problem

On the 7x7 wafer-scale model, matrix multiplication and matrix transpose often
did not show useful Photon acceleration.

Reason:

```text
They had too few workgroups per CU for the chosen problem size and tiling.
```

The user wanted to increase workgroup count without changing total problem size.

### 9.2 Small-Tile Attempt

A small-tile benchmark version was tried.

It increased workgroup count, but CU utilization became too low and baseline
performance got worse.

Observed example from the report:

```text
vanilla matrix transpose:  0.028275329000
modified small-tile MT:    0.118611286000
```

So the small-tile direction was not good enough: it increased WG count but made
each WG too small/inefficient.

### 9.3 Middle-Tile Compromise

The next design was a middle-tile version.

Current paths:

```text
mgpusim/benchmarks/amdappsdk/matrixmultiplication/benchmark.go
mgpusim/benchmarks/amdappsdk/matrixtranspose/matrixtranspose.go
```

Matrix multiplication:

```go
func NewMiddleTileBenchmark(driver *driver.Driver) *Benchmark {
    b := NewBenchmark(driver)
    b.WorkGroupSizeX = 8
    b.WorkGroupSizeY = 4
    return b
}
```

Comment in file:

```text
middle-sized 8x4 workgroup. It doubles WG count without dropping to the
16-work-item small-tile shape.
```

Matrix transpose:

```go
const (
    defaultBlockSize    = 16
    middleTileBlockSize = 8
)

func NewMiddleTileBenchmark(driver *driver.Driver) *Benchmark {
    b := NewBenchmark(driver)
    b.blockSize = middleTileBlockSize
    return b
}
```

Comment in file:

```text
middle-sized tile with one full 64-lane wavefront per workgroup.
```

Current benchmark names in `akkalat/benchmarkselection/benchmark.go`:

```text
matrixmultiplication
matrixmultiplication-middletile
matrixtranspose
matrixtranspose-middletile
```

Current selected sizes:

```go
matrixmultiplication.X = 256
matrixmultiplication.Y = 2048 * 128
matrixmultiplication.Z = 256

matrixtranspose.Width = 8192 * 2
```

## 10. Benchmark Size Scaling

The user first asked to make benchmark memory footprint around 1GB, then asked
to scale all runall benchmarks roughly 6x.

Current `akkalat/benchmarkselection/benchmark.go` has:

```go
const (
    oneMiB = 1024 * 800
    oneGBScale = 6
)
```

Some current sizes:

```text
aes.Length                         = oneMiB * 512
atax.NX/NY                         = 12288
bicg.NX/NY                         = 12288
bitonicsort.Length                 = 64M elements
fastwalshtransform.Length          = 64M elements
floydwarshall.NumNodes             = 8192
kmeans.NumPoints                   = oneMiB * 4
relu.Length                        = 10485760 * oneGBScale
matrixmultiplication               = 256 x (2048*128) x 256
matrixtranspose.Width              = 16384
spmv.Dim                           = 1024*1024*8
stencil2d                          = 8192 x 8192, 3 iterations
```

Note:

```text
"around 1GB" is approximate. Some workloads require power-of-two sizes or
square dimensions, so exact memory footprints vary.
```

## 11. Experiment Folders and Observations

Important result directories:

```text
akkalat/results/2026-05-06-05-19-16-sampled-validation
akkalat/results/2026-05-07-20-42-47-sampled-validation
akkalat/results/2026-05-09-15-41-40-sampled-validation
akkalat/results/2026-05-10-03-49-33-sampled-validation
akkalat/results/2026-05-11-04-01-28-sampled-validation
akkalat/results/2026-05-11-18-21-35-sampled-validation
akkalat/results/2026-05-12-00-27-37-sampled-validation
akkalat/results/2026-05-12-02-56-50-sampled-validation
```

### 11.1 3x3 ReLU Sweep

Folder:

```text
akkalat/results/2026-05-06-05-19-16-sampled-validation
```

This was the important 3x3 validation sweep. It showed that Photon can work on
the wafer-scale GPU model.

Main conclusion used in the weekly report:

```text
Some ReLU sampled configurations achieved about 2x to 2.6x wall-clock speedup
while keeping simulated-time error below 5%.
```

The better tradeoff found in this sweep:

```text
w = 128
g = 512
```

This became the chosen hyperparameter pair for the 7x7 experiment.

Generated figure:

```text
akkalat/reports/figures/relu_3x3_tradeoff_large.pdf
akkalat/reports/figures/relu_3x3_tradeoff_large.png
```

### 11.2 7x7 Validation

Folder:

```text
akkalat/results/2026-05-07-20-42-47-sampled-validation
```

The user asked to analyze only benchmarks that had completed. The key
observation was:

```text
ReLU benefits from Photon because it has enough per-CU work.
Matrix multiplication / matrix transpose often do not benefit because they have
too little remaining work after sampling finishes.
```

Generated figure:

```text
akkalat/reports/figures/photon_2026_05_07_success_large.pdf
akkalat/reports/figures/photon_2026_05_07_success_large.png
```

### 11.3 Workload Time/Error Plot

Folder:

```text
akkalat/results/2026-05-11-04-01-28-sampled-validation
```

Generated figure:

```text
akkalat/results/2026-05-11-04-01-28-sampled-validation/workload_time_error.pdf
akkalat/reports/figures/workload_time_error_large.pdf
akkalat/reports/figures/workload_time_error_large.png
```

This plot compares workload, simulation time, and error rate.

### 11.4 Weekly Report

Main report files:

```text
akkalat/reports/weekly_report_photon.tex
akkalat/reports/weekly_report_photon.pdf
```

Copied report package:

```text
akkalat/reports/report 5_11_2026/
```

This folder contains:

```text
weekly_report_photon.tex
weekly_report_photon.pdf
figures/
```

The report was revised to:

1. Introduce Photon and the sampling levels.
2. Use "error rate" instead of "accuracy" for simulated-time deviation.
3. Explain warmup and granularity.
4. Keep the 3x3 ReLU figure.
5. Add the 7x7 workload/time/error figure.
6. Add a workgroup table explaining why MT/MM have less acceleration.
7. Explain the small-tile workload modification and its CU-utilization problem.
8. Set next steps to:
   - Test whether loop sampling works.
   - Build LLM workloads such as Bert, GPT2, T5, and Llama.

## 12. Analysis / Plot Scripts

### 12.1 Summarize Logs

File:

```text
akkalat/summarize_runall2.py
```

Purpose:

```text
Summarize runall2 result directories, parse metrics/logs, count Photon debug
events, detect errors/timeouts, and emit CSV/Markdown summaries.
```

Useful commands:

```bash
cd /home/daoxuanxu/vanilla/akkalat
python3 summarize_runall2.py --results-dir results/<run-dir> --show
python3 summarize_runall2.py --results-dir results/<run-dir> --scan-mode full --show
```

Fast mode scans head/tail chunks of huge logs. Full mode scans entire logs.

### 12.2 Plot Sampled Sweep

File:

```text
akkalat/plot_sampled_sweep.py
```

Purpose:

```text
Plot warmup/granularity vs simulated-time error and wall-clock speedup for a
sampled sweep.
```

Default folder:

```text
akkalat/results/2026-05-06-05-19-16-sampled-validation
```

Useful command:

```bash
cd /home/daoxuanxu/vanilla
python3 akkalat/plot_sampled_sweep.py \
    --results-dir akkalat/results/2026-05-06-05-19-16-sampled-validation \
    --benchmark relu \
    --config sample_wf
```

## 13. KV Cache / LLM Benchmark Work

The user asked whether a simple KV cache benchmark could be built, then asked
for a more realistic decode-style KV cache and then a 30B example.

Current paths:

```text
mgpusim/benchmarks/llm/kvcache/
mgpusim/samples/kvcache/main.go
```

Files:

```text
mgpusim/benchmarks/llm/kvcache/benchmark.go
mgpusim/benchmarks/llm/kvcache/kernels.cl
mgpusim/benchmarks/llm/kvcache/kernels.hsaco
mgpusim/samples/kvcache/main.go
```

### 13.1 Important Limitation

The checked-in HSACO currently uses a simple memory-touch kernel loaded as:

```go
b.kernel = kernels.LoadProgramFromMemory(hsacoBytes, "ReLUForward")
```

This is a proxy kernel so the benchmark can run without needing a local AMDGPU
OpenCL compiler. It is not yet a numerically correct attention/KV-cache kernel.

This should be clearly stated in any report:

```text
The current KV-cache benchmark models memory footprint and workgroup structure,
not exact LLM attention math.
```

### 13.2 Modes

Sample CLI:

```bash
cd /home/daoxuanxu/vanilla/mgpusim
GOCACHE=/tmp/gocache go run -buildvcs=false ./samples/kvcache -- -mode decode
```

Current flags in `mgpusim/samples/kvcache/main.go`:

```text
-mode scan|decode|decode-30b
-layers
-heads
-kv-heads
-seq-len
-head-dim
-decode-steps
-seq-block
-wg-size
```

Mode behavior:

```text
scan        Old/simple benchmark. Scans K and V caches.
decode      Decode-stage proxy with append K, append V, QK score, V reduce.
decode-30b  Larger 30B/33B-style shape.
```

### 13.3 Current Shapes

`kvcache`:

```go
NumLayers  = 8
NumHeads   = 16
NumKVHeads = 16
SeqLen     = 2048
HeadDim    = 128
DecodeStep = 1
SeqBlock   = 64
WGSize     = 64
```

`kvcache-decode`:

```go
NumLayers  = 32
NumHeads   = 32
NumKVHeads = 32
SeqLen     = 2048
HeadDim    = 128
DecodeStep = 1
SeqBlock   = 64
decodeMode = true
```

`kvcache-decode-30b`:

```go
NumLayers  = 60
NumHeads   = 52
NumKVHeads = 52
SeqLen     = 2048
HeadDim    = 128
DecodeStep = 1
SeqBlock   = 64
```

Approximate footprint for `decode-30b`:

```text
K cache fp32: about 3.0 GiB
V cache fp32: about 3.0 GiB
K+V fp32:     about 6.1 GiB
K+V fp16:     about 3.0 GiB
```

Approximate decode-stage workgroups:

```text
NumLayers * NumHeads * ceil(SeqLen / SeqBlock)
= 60 * 52 * ceil(2048 / 64)
= 60 * 52 * 32
= 99,840 QK-score WGs
```

The benchmark launches four stages per decode step:

```text
append K
append V
QK score
V reduce
```

So total launched stage work is larger than just QK WGs.

### 13.4 `akkalat` Benchmark Names

Current `akkalat/benchmarkselection/benchmark.go` names:

```text
kvcache
kvcache-decode
kvcache-decode-30b
```

Current runall default includes all three.

### 13.5 KV Cache Result Folders

Relevant folders:

```text
akkalat/results/2026-05-12-00-27-37-sampled-validation
akkalat/results/2026-05-12-02-56-50-sampled-validation
akkalat/results/2026-05-12-03-14-00-sampled-validation
akkalat/results/2026-05-12-03-14-55-sampled-validation
```

The user observed KV cache could be slow. Reasons discussed:

1. Large KV cache footprint causes heavy memory traffic.
2. The proxy kernel is simple but can still create many memory requests.
3. Decode mode launches several stages per token.
4. On wafer-scale simulation, even simple kernels can be expensive when spread
   across many GPUs/CUs and when detailed timing is enabled.

## 14. Photon DNN Reference Investigation

The user asked to read the DNN in the Photon folder.

Important findings:

```text
photon/dnn
```

is an independent Go module:

```text
module gitlab.com/akita/dnn
```

It contains:

```text
tensor/
layers/
training/
training/optimization/
dataset/mnist/
dataset/imagenet/
dataset/cifar10/
example/lenet/
example/vgg16/
example/minerva/
example/xor/
```

The DNN layer benchmark wrappers live in:

```text
photon/sampled-mgpu-sim/benchmarks/dnn/
```

Photon DNN benchmark directories:

```text
avgpooling
conv2d
fulllayer
gputraining
im2col
lenet
maxpooling
minerva
relu
tensor
vgg16
xor
```

Important Photon DNN pattern:

```text
Photon does not only run whole VGG16. It decomposes VGG/ResNet into many
layer-level benchmarks such as conv2d, maxpooling, avgpooling, and fulllayer.
```

For example:

```text
photon/sampled-mgpu-sim/samples/sampledrunner/vgg16config.py
```

builds a list of layer commands.

This is useful for LLM work:

```text
Do the same decomposition for LLMs:
QKV projection, attention score, softmax, value reduce, MLP GEMM, KV cache.
```

## 15. DNN Benchmark Migration Into Vanilla MGPUSim

The user then asked to migrate Photon DNN benchmark code into vanilla MGPUSim.

Important observation:

```text
vanilla mgpusim already had most DNN code, but under a newer layout:
mgpusim/benchmarks/dnn/layer_benchmarks/
mgpusim/benchmarks/dnn/training_benchmarks/
mgpusim/benchmarks/dnn/gputensor/
```

Already present before the latest migration:

```text
conv2d
im2col
relu
lenet
vgg16
minerva
xor
gputensor
layers
training
```

Missing pieces from Photon that were added:

```text
mgpusim/benchmarks/dnn/layer_benchmarks/maxpooling/benchmark.go
mgpusim/benchmarks/dnn/layer_benchmarks/avgpooling/benchmark.go
mgpusim/benchmarks/dnn/layer_benchmarks/fulllayer/benchmark.go

mgpusim/samples/maxpooling/main.go
mgpusim/samples/avgpooling/main.go
mgpusim/samples/fulllayer/main.go
```

The old Photon imports:

```text
gitlab.com/akita/...
```

were adapted to vanilla imports:

```text
github.com/sarchlab/mgpusim/v3/...
```

### 15.1 DNN Migration Verification

Verified with:

```bash
cd /home/daoxuanxu/vanilla/mgpusim
GOCACHE=/tmp/gocache go test -buildvcs=false ./benchmarks/dnn/layer_benchmarks/...
GOCACHE=/tmp/gocache go build -buildvcs=false ./samples/maxpooling
GOCACHE=/tmp/gocache go build -buildvcs=false ./samples/avgpooling
GOCACHE=/tmp/gocache go build -buildvcs=false ./samples/fulllayer
```

All passed.

Temporary binaries created by `go build` in `mgpusim/` were removed:

```text
mgpusim/maxpooling
mgpusim/avgpooling
mgpusim/fulllayer
```

### 15.2 DNN Migration Caveats

The new layer benchmarks are currently single-GPU only, matching the Photon
version:

```go
if len(gpus) > 1 {
    panic("... benchmark can only run on a single GPU for now.")
}
```

The sample programs keep the `-output-channel` flag for compatibility with
existing sample runner config strings, but pooling layers do not actually use
output channels.

## 16. Run Commands for Next Week

### 16.1 Quick Sanity Build

```bash
cd /home/daoxuanxu/vanilla/mgpusim
GOCACHE=/tmp/gocache go test -buildvcs=false ./benchmarks/dnn/layer_benchmarks/...
GOCACHE=/tmp/gocache go test -buildvcs=false ./benchmarks/llm/kvcache

cd /home/daoxuanxu/vanilla/akkalat/baseline
GOCACHE=/tmp/gocache go build -buildvcs=false
```

### 16.2 Dry Run Current Runall

```bash
cd /home/daoxuanxu/vanilla/akkalat
python3 runall2.py \
    --benchmarks kvcache-decode-30b \
    --configs baseline,sample_all \
    --sampled-warmups 128 \
    --sampled-granularities 512 \
    --dry-run
```

### 16.3 Run KV Cache Decode 30B

```bash
cd /home/daoxuanxu/vanilla/akkalat
python3 runall2.py \
    --benchmarks kvcache-decode-30b \
    --configs baseline,sample_all \
    --sampled-warmups 128,512 \
    --sampled-granularities 512,1024,4096 \
    --max-workers 15 \
    --photon-debug
```

### 16.4 Run Matrix Middle-Tile Test

```bash
cd /home/daoxuanxu/vanilla/akkalat
python3 runall2.py \
    --benchmarks matrixmultiplication,matrixmultiplication-middletile,matrixtranspose,matrixtranspose-middletile \
    --configs baseline,sample_all \
    --sampled-warmups 128 \
    --sampled-granularities 512 \
    --max-workers 15 \
    --photon-debug
```

### 16.5 Run Loop Sampling Prototype

```bash
cd /home/daoxuanxu/vanilla/akkalat
python3 runall2.py \
    --benchmarks matrixmultiplication-middletile \
    --configs baseline,sample_loop \
    --loop-sampled-warmup 8 \
    --loop-sampled-min-iters 16 \
    --loop-sampled-threshold 0.03 \
    --max-workers 4 \
    --photon-debug
```

### 16.6 Summarize Results

```bash
cd /home/daoxuanxu/vanilla/akkalat
python3 summarize_runall2.py \
    --results-dir results/<run-dir> \
    --show
```

For exact Photon event counts:

```bash
python3 summarize_runall2.py \
    --results-dir results/<run-dir> \
    --scan-mode full \
    --show
```

## 17. Branch Migration Checklist

When creating a new branch, copy or preserve these areas.

### 17.1 Akita / Translation Stack

```text
akita/mem/vm/mmu/
akita/mem/vm/mmuCache/
akita/mem/vm/mmuTLB/
akita/mem/vm/l2tlb/
akita/mem/vm/tlb/
```

Audit:

```bash
rg -n "PTCL|ptcl|prefetch|cacheline|coalesc|walkCoalesc" akita/mem/vm akkalat
rg -n "tlb_gmmu|l2tlb|mmuTLB|MMUTLB" akita mgpusim akkalat
find akita/mem/vm -name '*copy*' -print
```

### 17.2 Photon Sampling

```text
mgpusim/samples/sampledrunner/
```

Especially:

```text
per_gpu.go
wgtracer.go
branchtracer.go
kernelsampled.go
sampledengine.go
debug.go
```

### 17.3 Benchmarks

```text
mgpusim/benchmarks/llm/kvcache/
mgpusim/samples/kvcache/

mgpusim/benchmarks/amdappsdk/matrixmultiplication/
mgpusim/benchmarks/amdappsdk/matrixtranspose/

mgpusim/benchmarks/dnn/layer_benchmarks/
mgpusim/samples/maxpooling/
mgpusim/samples/avgpooling/
mgpusim/samples/fulllayer/
```

### 17.4 Akkalat

```text
akkalat/runall2.py
akkalat/benchmarkselection/benchmark.go
akkalat/summarize_runall2.py
akkalat/plot_sampled_sweep.py
akkalat/reports/
```

### 17.5 Reports / Figures

```text
akkalat/reports/weekly_report_photon.tex
akkalat/reports/weekly_report_photon.pdf
akkalat/reports/report 5_11_2026/
akkalat/reports/figures/
```

## 18. Known Risks and Cleanup Items

### 18.1 KV Cache Is Still a Proxy

The KV cache benchmark is useful for memory footprint and workgroup-structure
experiments, but it is not yet a real attention kernel.

Next step:

```text
Replace proxy ReLUForward HSACO with real kernels:
1. append K/V
2. QK dot product
3. softmax or approximate softmax
4. value reduce
```

### 18.2 DNN Layer Benchmarks Are Single-GPU

The newly migrated `maxpooling`, `avgpooling`, and `fulllayer` benchmarks are
single-GPU wrappers. That is fine for Photon layer-level sample runner tests,
but not enough for true multi-GPU wafer-scale DNN experiments.

### 18.3 `runall2.py` Default "All" Is Currently Curated

`ALL_BENCHMARKS` currently has many benchmarks commented out. If the next
branch wants a full suite, this needs to be changed.

### 18.4 Coalescing/PTCL Residue

The earlier intent was to remove PTCL/cacheline/prefetch/coalescing features.
Some coalescing-related flags/stats still exist. Audit before final claims.

### 18.5 Generated Artifacts

Before committing/pushing a clean branch, avoid committing:

```text
__pycache__/
*.aux
*.log
*.out
*.fls
*.fdb_latexmk
*.synctex.gz
large result logs unless intentionally archived
temporary go build binaries
```

Some report auxiliary files exist under:

```text
akkalat/reports/
akkalat/reports/report 5_11_2026/
```

Decide whether the new branch should keep only `.tex`, `.pdf`, and figure
assets.

## 19. Next Week Priorities

Recommended order:

1. Create clean branch and copy current working changes carefully.
2. Run build sanity checks with `GOCACHE=/tmp/gocache` and `-buildvcs=false`.
3. Audit remaining PTCL/coalescing/prefetch symbols.
4. Validate current Photon WF sampling on one small benchmark:
   ```text
   relu baseline vs sample_wf w128 g512
   ```
5. Validate current Photon sampling on one LLM benchmark:
   ```text
   kvcache-decode baseline vs sample_all w128 g512
   ```
6. Test loop-level sampling:
   ```text
   matrixmultiplication-middletile sample_loop
   kvcache-decode sample_loop
   ```
7. Replace or extend proxy KV cache kernels with more realistic attention
   stages.
8. Build LLM benchmark families:
   ```text
   BERT
   GPT-2
   T5
   LLaMA
   ```
9. Follow the Photon DNN pattern: decompose full models into layer/submodule
   benchmarks rather than immediately trying to run one giant end-to-end model.

## 20. Suggested LLM Benchmark Decomposition

Following Photon's DNN layer-level approach, build LLM workloads as separate
sub-benchmarks first:

```text
llm/qkv-proj          GEMM for Q, K, V projection.
llm/attention-score   QK^T score computation.
llm/softmax           softmax or approximate softmax over sequence.
llm/value-reduce      attention probabilities times V.
llm/mlp-gemm          FFN up/down projection.
llm/kvcache           existing KV cache scan/decode.
```

Then write a config file similar to:

```text
mgpusim/samples/sampledrunner/vgg16config.py
```

For example:

```text
mgpusim/samples/sampledrunner/llama7bconfig.py
mgpusim/samples/sampledrunner/gpt2config.py
mgpusim/samples/sampledrunner/bertconfig.py
```

Each config should generate a list of commands:

```text
("qkv-proj", "./qkv-proj -layers ... -hidden ...")
("kvcache", "./kvcache -mode decode ...")
("mlp-gemm", "./mlp-gemm ...")
```

This gives Photon more layer-level repetition to exploit, similar to VGG/ResNet.

## 21. Useful One-Liners

Find latest sampled result:

```bash
find akkalat/results -maxdepth 1 -type d -name '*-sampled-validation' | sort | tail -n 1
```

Find failed runs:

```bash
rg -n "Return code: [^0]|Timed out|panic:|fatal|error:" akkalat/results/<run-dir>/*_out.stdout
```

Check Photon actually skipped something:

```bash
rg -n "wf sampled skip|branch sampled skip|kernel sampled wf marked skip|sampled wf completion fired" \
    akkalat/results/<run-dir>/*_out.stdout
```

Check final simulated time:

```bash
rg -n "Driver.*, total_time|where, what, value" akkalat/results/<run-dir>/*_metrics.csv
```

Build one sample binary without leaving VCS-stamp problems:

```bash
cd /home/daoxuanxu/vanilla/mgpusim
GOCACHE=/tmp/gocache go build -buildvcs=false ./samples/kvcache
```

## 22. Short Narrative Summary for Advisor / Report

A concise English summary of the current state:

```text
This week I integrated Photon-style sampled simulation into our wafer-scale GPU
model and validated it on a 3x3 wafer-scale configuration. On ReLU, several
sampling configurations achieved around 2x-2.6x simulator wall-clock speedup
while keeping simulated-time error below 5%. The best tradeoff in this sweep was
around warmup=128 and granularity=512.

When scaling the wafer-scale GPU model to 7x7, Photon worked well for workloads
with enough per-CU work, such as ReLU. However, matrix multiplication and matrix
transpose showed limited acceleration because the total number of workgroups was
too small. After Photon completed warmup and stability analysis, too few
remaining workgroups were left to skip.

I tried increasing workgroup count by modifying the matrix workloads. A
small-tile version increased workgroup count but reduced CU utilization and
hurt baseline performance. I then added middle-tile versions as a compromise,
keeping the same problem size while increasing workgroup count less aggressively.

I also started adding LLM-style workloads. The current KV-cache benchmark models
scan and decode-stage memory/workgroup structure, including a 30B-style
configuration. It currently uses a simple memory-touch proxy kernel, so the next
step is to replace it with more realistic attention-stage kernels.

Next steps are to validate loop-level sampling and build more complete LLM
workloads, including BERT, GPT-2, T5, and LLaMA-style model components.
```

## 23. Final State Snapshot

Most important current additions/changes for the next branch:

```text
Photon sampling:
  mgpusim/samples/sampledrunner/per_gpu.go
  mgpusim/samples/sampledrunner/wgtracer.go
  mgpusim/samples/sampledrunner/branchtracer.go
  mgpusim/samples/sampledrunner/kernelsampled.go
  mgpusim/samples/sampledrunner/sampledengine.go

LLM KV cache:
  mgpusim/benchmarks/llm/kvcache/benchmark.go
  mgpusim/benchmarks/llm/kvcache/kernels.cl
  mgpusim/benchmarks/llm/kvcache/kernels.hsaco
  mgpusim/samples/kvcache/main.go

Middle tile workloads:
  mgpusim/benchmarks/amdappsdk/matrixmultiplication/benchmark.go
  mgpusim/benchmarks/amdappsdk/matrixtranspose/matrixtranspose.go

DNN layer benchmark migration:
  mgpusim/benchmarks/dnn/layer_benchmarks/maxpooling/benchmark.go
  mgpusim/benchmarks/dnn/layer_benchmarks/avgpooling/benchmark.go
  mgpusim/benchmarks/dnn/layer_benchmarks/fulllayer/benchmark.go
  mgpusim/samples/maxpooling/main.go
  mgpusim/samples/avgpooling/main.go
  mgpusim/samples/fulllayer/main.go

Akkalat experiment harness:
  akkalat/runall2.py
  akkalat/benchmarkselection/benchmark.go
  akkalat/summarize_runall2.py
  akkalat/plot_sampled_sweep.py

Report:
  akkalat/reports/weekly_report_photon.tex
  akkalat/reports/report 5_11_2026/
  akkalat/reports/figures/
```
