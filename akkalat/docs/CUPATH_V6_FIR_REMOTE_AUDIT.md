# CuPath V6 FIR remote-path audit

## Status

The final Baseline provenance run completed successfully with return code 0.
It launched the full 524,288-WG FIR grid and stopped only after the runner
observed 76,800 normally mapped WG lifetimes.  The complete machine-readable
evidence is in
`akkalat/results/2026-07-19-cupath-v6-runtime-stop-fir-provenance`.

## Full-run mapping evidence

The original unified-GPU partition contains 10,944 WGs for physical GPUs
1--47 and 9,920 WGs for GPU 48.  It was computed from all 524,288 requested
WGs, not from the stopping threshold.  All 48 physical GPUs were observed.
The mapping reporter records stop reason `runner_map_wg_observed_limit`, stop
time 211,603 ns, and global observed-WG-set hash
`eadfdc15bb5fbdb9199be90f438a19c3265a4aa9c49a509839fc3768585a0ca9`.
The only WG filter is the pre-existing `original_unified_partition`; the
report explicitly records that no max-WG-specific filter was active.

## What the provenance trace proves

The final translated-demand aggregate contains 22,344,150 local and 4,242,763
remote requests among the four registered FIR data objects (15.958088%
remote).  All 4,242,763 remote requests are 4-byte reads of the filter
coefficients.  Input contributes 21,261,938 local reads and zero remote
requests; output contributes 1,024,859 local writes and zero remote requests;
history contributes 15 local reads and zero remote requests.  A further
1,249,108 local scalar-side requests are retained explicitly as
`unclassified`; the analyzer does not silently fold them into the four FIR
data objects.

The first preserved remote request is a 4-byte filter read by requester GPU 48
for flattened WG 514,382.  Virtual address `0x40002000` translates to physical
page `0x201000000`, whose authoritative page-table owner is physical GPU 1.
GPU 1 has zero remote registered-object requests.  GPUs 2--48 each direct all
of their remote registered-object requests to that same owner-1 filter page;
their measured remote fractions are approximately 16.2%.  The complete
requester-owner matrix and per-GPU ratios are preserved as CSV rather than
inferred from legacy RDMA counters.

## Why the filter is remote

FIR is launched on unified virtual GPU 49, not on a list containing the 48
physical GPU IDs.  The benchmark therefore allocates one 64-byte filter object
for device 49.  A trace-only allocation diagnostic reports:

```text
[FIR remote-origin] filter intended_gpu=49 vaddr=0x40002000
```

The unified device's one-page allocation is placed on physical GPU 1.  The
Driver then partitions the full kernel grid across all 48 physical GPUs while
preserving that same kernel-argument pointer.  Consequently, physical GPU 1
reads the coefficient page locally and GPUs 2--48 read it remotely.  In
contrast, the large input and output allocations are explicitly distributed
across the physical GPUs and are local for the naturally corresponding FIR
workgroups in the captured window.

Thus FIR's remote traffic is not a boundary halo and is not introduced by the
repaired max-WG stopper.  It is repeated access to the small shared coefficient
page exposed by the existing unified-GPU allocation and launch path.  This is
also why remote aggregation or requester-L2 reuse can affect FIR even though
its main input/output streams are partition-local.  The separate Baseline,
M2, M3, and Complete mapping audit remains the final gate before freezing V6;
it must report any naturally different observed WG sets without attempting to
equalize them.
