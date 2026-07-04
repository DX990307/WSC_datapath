# Data-Path Mechanism Redesign

## Current clean state

The previous M1 and M2 mechanisms have been removed from the simulator path.
The clean baseline keeps the existing L1V, L2, DRAM, address translation, and
RDMA behavior, plus the tracing infrastructure that is useful for critical-path
analysis.

The run script now supports only the `baseline` mechanism arm. This is
intentional: the next mechanism should be added back as a small, measurable
change instead of mixing several reorder, prefetch, and bypass policies.

## Why the previous design was hard to interpret

The old design changed several places at the same time:

- L1V directory ordering.
- L1V bottom reorder/prefetch.
- L2 directory batching.
- L2-to-DRAM access-unit coalescing and prefetch.
- DRAM queue reorder.
- RDMA request batching.
- Address-translator-to-RDMA bypass.

This made end-to-end results difficult to explain. If a benchmark did not speed
up, it was unclear whether the mechanism had too few eligible requests, created
extra traffic, waited too long, optimized a non-critical path, or was hidden by
another bottleneck.

## New design principle

The next mechanism should optimize only demand requests that are already on the
critical data path.

Do not add speculative prefetch first. Do not bypass the normal L1/L2 path first.
Do not reorder independently at L1, L2, DRAM, and RDMA at the same time.

The first version should have one insertion point, one correctness rule, and one
main saved-work metric.

## Proposed mechanism: demand-only data-access merge queue

### Local L2-to-DRAM path

Insertion point: L2 write-buffer fetch path, where an L2 miss issues a DRAM read.

Rule:

- Keep a small pending queue of demand L2 miss fetches.
- Merge only requests that are already pending.
- Merge only 64B cache-line reads that fall in the same 128B DRAM access unit.
- Do not issue a prefetch for a line that has no demand request.
- Preserve writes, flushes, and ordering barriers.
- Bound waiting with a small max-age threshold.

Expected benefit:

- Reduce duplicate DRAM timing work for adjacent cache lines.
- Improve useful bytes per DRAM access unit.
- Keep traffic predictable because every returned cache line has a real demand
  consumer.

Required metrics:

- Eligible local L2 misses.
- Merged local L2 misses.
- DRAM read packets issued.
- Useful DRAM read bytes.
- Issued DRAM read bytes.
- Merge wait cycles.
- Demand response latency before and after merge.

### Remote RDMA path

Insertion point: RDMA requester side after a normal L1/L2 miss has been routed to
remote memory.

Rule:

- Keep a small per-remote-GPU, per-page demand queue.
- Merge only remote 64B read requests that are already pending.
- Send a compact request that names multiple lines in the same remote page.
- Do not bypass address translation.
- Do not bypass L1V.
- Do not wait if the queue is empty or the max-age threshold is reached.

Expected benefit:

- Reduce request-side network packets for remote demand reads.
- Reduce per-packet endpoint/switch overhead.
- Keep remote memory correctness simple because the owner side still performs
  normal local L2/DRAM reads for each demanded line.

Required metrics:

- Eligible remote reads.
- Merged remote reads.
- Batch size distribution.
- Request network bytes before and after merge.
- Return network bytes before and after merge.
- Queue wait cycles.
- Remote data response latency.

## Experiment order

1. Run baseline with critical-path trace only.
2. Identify whether each benchmark is dominated by local L2/DRAM, remote network,
   remote L2/DRAM, or address translation.
3. Enable only the local L2-to-DRAM merge queue.
4. Enable only the remote RDMA merge queue.
5. Enable both only after the two isolated studies are understood.

## Success criteria

A mechanism should be considered useful only if it shows both:

- Direct path improvement, such as fewer DRAM packets or fewer network packets.
- End-to-end improvement, or a clear explanation showing which remaining
  critical path hides the direct improvement.
