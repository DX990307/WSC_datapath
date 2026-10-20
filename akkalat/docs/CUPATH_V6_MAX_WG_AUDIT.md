# CuPath V6 runtime-only max-WG audit

## Implemented contract

`max-wg` is owned only by the runner-side tracer. The Driver always sees the
complete kernel grid, computes the original ordinary or unified-GPU
partition, and dispatches through the original scheduler. The stopper watches
the existing `MapWGReq` lifetime at physical CUs and invokes the normal
`atexit` reporting path after the global observed count reaches the limit.

The removed Driver-side implementation names are guarded by
`test_max_wg_never_appears_in_driver_partitioning`. Ordinary launch code has
no `WGFilter`; the only filter reported for a unified launch is the original
unified partition filter.

## Unit and source-invariant evidence

- `TestMaxWGDoesNotChangeUnifiedWGDistribution`
- `TestMaxWGDoesNotChangeOrdinaryKernelDispatch`
- `TestMaxWGUsesRunnerMapWGStopperOnly`
- `TestMaxWGStopperTriggersAtObservedCount`
- `TestMaxWGDisabledAllocatesNoStopperState`
- `TestWGMappingAccumulatesNormalKernelLaunches`
- `TestWGMappingReportsUnexpectedWGFilter`
- `test_max_wg_never_appears_in_driver_partitioning`

Focused tests passed on 2026-07-19 for the runner, Driver, CU, ROB, RDMA,
writeback cache, DRAM, address translator, and passive trace packages. The
stopper regression test also models Akita's ID-only `EndTask` callback rather
than incorrectly expecting the ending task to repeat `Kind` and `What`.  The
multi-launch regression proves that iterative workloads sum the untouched
requested grids of all normally launched kernels while the runner observes a
single global stream of `MapWGReq` lifetimes.  The
CuPath Python suite currently reports 129 passing tests after the analysis,
provenance/formal protocol, strict missing-cell resume, and
selective-ablation runner migration.

## End-to-end smoke evidence

The final FIR runtime-stop smoke in
`/tmp/cupath-v6-fir-origin-final-smoke-20260719` launched the complete 524,288-WG
grid. The original partition contains 10,944 WGs for GPUs 1--47 and 9,920 for
GPU 48. The first naturally completed WG was flattened WG 383,050 on GPU 36;
the runner stopped at observed count one, wrote metrics and a mapping sidecar,
and exited with simulator return code zero. This is direct evidence against a
prefix selection or fixed per-GPU quota.

The current freeze candidate (SHA-256
`cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e`)
independently passed the ReLU end-to-end smoke in
`/tmp/cupath-v6-cbb-relu-smoke`: it launched the complete 5,242,880-WG grid,
retained the original 48-way partition, naturally observed flattened WG
3,277,466 on GPU 31, stopped at one, emitted the sidecar, and returned zero.

## Full FIR mapping gate

The four-configuration FIR audit in
`akkalat/results/2026-07-19-cupath-v6-runtime-stop-fir-audit` passed for
Baseline, M2, M3, and Complete.  Every cell launched all 524,288 requested
WGs, retained the same original 48-GPU partition hash
`74e86b5ceac3957bee07d99d077e1fe720c8a379e6345dcfd33579c07806a420`,
observed all 48 physical GPUs, stopped at exactly 76,800 completed
`MapWGReq` lifetimes, emitted metrics and a mapping sidecar, and returned zero.
No cell reports a max-WG-specific filter.  The global WG-set and per-GPU
mapping hashes differ naturally among configurations; the audit reports all
three differences from Baseline rather than introducing a quota to conceal
them.

After this gate and the full FIR provenance gate passed, the formal binary was
frozen as `/tmp/cupath-runtime-stop-frozen-v6` with SHA-256
`cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e`.
