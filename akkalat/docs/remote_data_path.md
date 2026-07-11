# DRAM batching, remote-request reduction, and requester-L2 replicas

The design contains three independently controlled mechanisms, all disabled
by default:

1. confirmed-L2-miss DRAM batching;
2. remote-request reduction, which combines exact same-line RDMA deduplication
   with 4KiB-page bitmap batching;
3. per-slice Cuckoo Filter lookup and two-touch requester-L2 replicas.

The DRAM and requester-RDMA batching points are independently bounded. When
the remote data path is enabled, the legacy per-L1 FIFO/HLQ is forced off.
The experiment treats exact deduplication and remote bitmap batching as one
`remote request` mechanism. Dedup, bitmap batching, and requester L2 retain
lower-level switches so each standalone configuration can exclude the other
two mechanisms.

## Confirmed-miss DRAM batching

Each L2 slice batches read misses only after the L2 lookup and MSHR handling
have confirmed that a DRAM fetch is required. The key is
`(PID, low module, aligned 128B window)`. With 64B cache lines, two demanded
adjacent lines in that window share one 128B low-module request. A singleton
falls back to an ordinary 64B request, so this mechanism does not fetch an
undemanded mate line.

This path accepts both locally generated L2 misses and owner-side misses
created after RDMA expands a bitmap request. Therefore remote data can benefit
first from requester-side packet batching and then, after the owner-L2 lookup,
from owner-side DRAM access-unit batching.

## Demand path

1. A full, aligned 64B remote read uses an exact
   `(owner, PID, 64B line, read epoch)` entry. The entry remains live through
   collection, network flight, response, and fanout. All matching L1/SA
   waiters share one remote request and receive independent data copies.
2. When requester L2 is enabled, the read sends a lookup-only request to the
   requester-local L2 slice selected by the normal 128B slice interleave.
   Each slice owns one Cuckoo Filter containing only installed clean remote
   replicas. A filter negative returns immediately. A filter positive performs
   a normal directory/bank lookup. A lookup miss never allocates an MSHR and
   never accesses requester-local DRAM.
3. When requester L2 is disabled, or after a lookup-only miss, unique lines
   enter a requester-RDMA batch keyed by `(owner, PID, 4KiB page)`. Batching
   is work-conserving: requests admitted in the current RDMA scheduling cycle
   are grouped, and ready batches issue immediately up to the RDMA pipeline
   width. Output backpressure can naturally accumulate more matching lines,
   but the requester never waits for a future request. A one-line batch uses
   a normal read packet; a multi-line batch uses one bitmap request. The owner
   expands the bitmap and sends every line through its normal L2 path.
   Confirmed owner-L2 read misses then enter the same 128B DRAM batching
   mechanism described above.
4. Outstanding logical reads and owner-side unpacked lines are bounded; a full
   table applies backpressure instead of consuming more upstream requests.

## L2 admission

- Demand accesses use two-touch admission. The first remote return is not
  installed. If the same `(owner, PID, line)` has at least two logical demand
  accesses, its return is clean-filled into requester L2.
- `-remote-data-path-prefetch=true` optionally adds the other 64B line in the
  same 128B access unit to an existing bitmap batch. A returned prefetch-only
  line is clean-filled immediately and inserted into that L2 slice's filter.
  If demand reaches that line before the packet is sent, it is converted back
  to a demand line and no longer counted as speculative wire traffic. Demand
  arriving after send merges with the in-flight prefetch request.
- A fill is best effort: it is dropped on an MSHR conflict, resident-line
  conflict, locked block, dirty victim, invalid size/alignment, or filter-full
  condition.
- Each lookup returns the L2 slice's flush generation. A later clean fill must
  carry the same generation, so a response delayed across a cache flush cannot
  reinstall stale data. Every fill is acknowledged, including dropped fills,
  and RDMA drain waits for those acknowledgements.

## Correctness boundary

Remote replicas are clean read-only replicas. The current simulator has no
cross-GPU cache-coherence invalidation protocol, so the replica path must be
used only for immutable/read-only remote data, or within an externally managed
epoch ending in the existing RDMA drain and cache flush. A requester that
issues a remote write permanently bypasses its local replica for that line.

## Main controls

```text
-dram-batch-enable=true
-dram-batch-entries=16
-dram-batch-lines=2
-dram-batch-wait-ns=0

-remote-data-path-enable=true
-remote-data-path-dedup-enable=true
-remote-data-path-batching-enable=true
-remote-data-path-l2-enable=true
-remote-data-path-batch-lines=8
-remote-data-path-wait-ns=0
-remote-data-path-batches=64
-remote-data-path-reuse-entries=4096
-remote-data-path-prefetch=false
```

## What is measured

The metrics deliberately separate benefit from cost:

- DRAM batching: confirmed miss lines, created/drained batches, lines per
  batch, 128B multi-line reads versus 64B single-line reads, singleton
  fallbacks, queue wait, maximum batch size, and full/timeout/capacity/drain
  reasons;
- wire benefit: logical reads, exact dedup, demand/prefetch wire lines, packet
  size histogram, request/response bytes, and exact flit count for the chosen
  flit size;
- latency cost: L2 probe latency, scheduler/backpressure queue residence, complete
  pre-network wait, and logical read latency observed when RDMA injects the
  response toward L1;
- replica value: two-touch/prefetch fill attempts, installs, first-use count,
  later probe hits, unused retirements, and current/peak occupancy;
- pressure proxy: the number of successful remote fills that displaced a
  valid local clean line. This is not called a pollution miss because proving
  that would require a shadow cache and materially more simulator state;
- diagnosis: merge stage, flush reason, Cuckoo Filter false positives, and
  balance checks for packets, bytes, fills, and completed logical requests.

`remote_wire_lines` includes speculative lines. `remote_demand_wire_lines`
and `remote_prefetch_wire_lines` split it without ambiguity.

The raw DRAM-batch metric names are:

```text
dram_batch_enabled
dram_batch_miss_lines
dram_batches_created
dram_batches_drained
dram_batch_lines
dram_batch_singleton_fallbacks
dram_batch_full_drains
dram_batch_timeout_drains
dram_batch_capacity_drains
dram_batch_drain_drains
dram_batch_max_lines
dram_batch_wait_total_ns
dram_batch_wait_samples
dram_batch_multiline_reads
dram_batch_singleline_reads
```

Average DRAM-batch residence is
`dram_batch_wait_total_ns / dram_batch_wait_samples`. A useful configuration
should increase `dram_batch_multiline_reads` and reduce DRAM transactions
without paying excessive singleton waits.

## Standalone ablation experiment

For the baseline, three standalone mechanisms, and their combination, run:

```bash
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks traditional \
  --output-dir akkalat/results/independent_ablation

python3 akkalat/analyze_remote_data_path.py \
  akkalat/results/independent_ablation
```

Use a fresh output directory. The standalone filenames intentionally differ
from the earlier cumulative experiment and should not be mixed with it.

The five configurations have unique filenames and use the same workload and
common simulator flags:

1. `baseline`: all three mechanisms off, with L1 reorder/HLQ explicitly
   disabled;
2. `baseline_dram_batch_only`: only confirmed-miss `2x64B -> 1x128B` DRAM
   batching is enabled;
3. `baseline_remote_request_only`: only exact same-line RDMA dedup, response
   fanout, and work-conserving 4KiB-page bitmap batching are enabled;
4. `baseline_remote_l2_only`: only per-slice Cuckoo lookup and two-touch
   requester-L2 replicas are enabled. Dedup and bitmap batching are explicitly
   disabled, and misses use ordinary one-line remote requests;
5. `baseline_all_three`: all three mechanisms are enabled together.

This is a **standalone ablation plus combination** study. Each `only` result is
compared directly with the same baseline, rather than with the preceding row.
The analyzer also reports a combined interaction term:

```text
combined runtime reduction - sum(three standalone runtime reductions)
```

A negative interaction means that standalone benefits overlap; a positive
interaction indicates synergy. Add `--remote-ablation-include-prefetch` for a
sixth `baseline_all_three_prefetch` sensitivity run; prefetch is not one of
the three core mechanisms. DRAM batch size, wait, and capacity can be changed
with `--dram-batch-{lines,wait-ns,entries}`. Remote batch size, capacity, and
reuse-table size use `--remote-data-path-{batch-lines,batches,reuse-entries}`.
`--remote-data-path-wait-ns` remains accepted for old scripts but is forced to
zero and has no runtime effect. The analyzer
automatically pairs all configs with the same-directory `baseline` file and
reports every standalone/combined speedup versus that baseline.

For a conventional remote-off/on A/B in separate directories, use the
following commands.

Use separate result directories. `--extra-benchmark-flags` does not become
part of the output filename, so using one directory would overwrite the
remote-off result.

```bash
python3 akkalat/runall2.py --configs baseline --benchmarks traditional \
  --output-dir akkalat/results/remote_off

python3 akkalat/runall2.py --configs baseline --benchmarks traditional \
  --output-dir akkalat/results/remote_on \
  --extra-benchmark-flags='-dram-batch-enable=true -dram-batch-entries=16 -dram-batch-lines=2 -dram-batch-wait-ns=0 -remote-data-path-enable=true -remote-data-path-dedup-enable=true -remote-data-path-batching-enable=true -remote-data-path-l2-enable=true -remote-data-path-prefetch=false -remote-data-path-batch-lines=8 -remote-data-path-wait-ns=0 -remote-data-path-batches=64 -remote-data-path-reuse-entries=4096'

python3 akkalat/analyze_remote_data_path.py \
  akkalat/results/remote_on \
  --baseline-dir akkalat/results/remote_off
```

The analyzer writes `remote_data_path_analysis.csv` plus a compact Markdown
table. Runtime speedup comes from the real remote-off A/B pair. Packet, byte,
and flit savings compare the enabled run against one ordinary 64B remote
transaction per logical read; those savings are allowed to be negative, which
exposes an ineffective prefetch or batching policy.

The most useful first sweep keeps prefetch off and varies remote maximum batch
lines and RDMA outstanding capacity. The confirmed-miss DRAM
mechanism now combines two adjacent demanded 64B misses into one 128B
transaction; it falls back to 64B for a singleton. Enable AU prefetch only
after the non-speculative mechanisms show a runtime or congestion benefit,
then require both a useful-prefetch rate and an acceptable
local-clean-displacement rate.
