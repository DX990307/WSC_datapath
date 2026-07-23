# CuPath typed metadata design

## Physical organization

CuPath uses one physical Typed Cuckoo Filter per L2 slice. The evaluated
wafer has 48 GPMs and four L2 slices per GPM, so it instantiates 192 Filters.
RDMA maps an address to the Filter beside the requester-L2 slice that owns the
line; it does not add an RDMA Filter or a cacheline-data array.

Five logical key types share each bucket array:

| Type | Meaning | Safe action |
|---|---|---|
| `PATTERN` | Real demands established a stable predictor relation | A positive qualifies one 64-B candidate |
| `RESIDENT` | The line may be in the existing requester L2 | A reliable negative eliminates a known-miss tag lookup or speculative candidate |
| `PENDING` | A remote line transaction may already exist | A positive suppresses a remote candidate after exact RDMA confirmation |
| `SEEN` | Real remote activity supplied recurrence evidence | A positive permits conservative requester-L2 admission |
| `GRANULARITY_PENDING` | A local line has an ordinary demand MSHR or is attached to a paired read | A reliable negative skips an exact sibling-MSHR lookup; a positive is confirmed exactly |

`RESIDENT`, `PENDING`, and `GRANULARITY_PENDING` have priority over `PATTERN`
and `SEEN`. The latter two share only the low-priority portion of the array
and cannot consume the critical reserve. The type participates in both hashes
and in the stored signature. Counted signatures permit deletion when
colliding live keys share a fingerprint.

The exact shadow map exists only to count false positives during simulation.
It never makes a data-path decision and is excluded from hardware cost.

## Unified predictor

The local L2 front end and requester RDMA use bounded real-demand-only
predictors with the same state format. Each entry records a source,
last real address, last real stride, minimal repeated-stride evidence, and a
generation token. A demand can produce at most one ordinary 64-B candidate.
Speculative requests never train the predictor, and stale feedback is rejected
by the generation token. M1 restricts its output to the direct sibling;
requester-RDMA additionally requires its remote PATTERN metadata before
candidate piggybacking.

The four L2 slices in one GPM share one 256-entry local predictor, and
requester RDMA has one 256-entry remote predictor per GPM. Each slice retains
its own Filter, tags, data, MSHRs, and fill path.

## Cost and timing

The nominal point uses 32K slots per 1-MB L2 slice, four slots per bucket, and
a 13-bit fingerprint. A slot contains a 13-bit fingerprint, three type bits,
one valid bit, and a four-bit reference count: 21 bits, or 84 KiB per slice.
Across 192 slices this is 15.75 MiB, 8.20% of the 192-MiB wafer L2 data
capacity. The local and requester-RDMA predictors contain 512 entries/GPM.
Charging a conservative 64 bytes per entry adds 32 KiB/GPM, or
1.50 MiB wafer-wide; Filter and predictor metadata together are 8.98% of
baseline L2 data capacity under this bound.

Lookup and update latency and width are modeled. The nominal point is one
cycle and 16 operations/cycle/slice for each port. A demand-side RESIDENT
lookup fails open if metadata is unavailable. A speculative lookup or update
that cannot acquire its port drops the candidate immediately. Thus Filter
contention cannot make a demand wait.

## Three request-lifecycle stages

### M1: Filter-guided paired-read aggregation

A real local read trains the shared local predictor and may propose its direct
sibling. Repeated real-demand evidence first installs a PATTERN key in the
same physical Filter.  The slice then queries PATTERN, RESIDENT, and
GRANULARITY_PENDING while the demand continues through the ordinary L2
lookup. A PATTERN positive must agree with the live predictor generation;
reliable state negatives skip the exact sibling tag/MSHR checks, while state
positives are confirmed exactly.  An unproven stream may have only one
training pair until timely feedback arrives.  The candidate must stay in the
same page, slice, controller stripe, and compatible DRAM mapping. It is
dropped if any Filter, MSHR, clean victim, fill-path, or DRAM-queue resource
is unavailable; the demand never waits.

On a demand miss, an admitted sibling and the demand form one internal
paired-read descriptor. The controller splits it into two physical 64-B reads.
Only those two related reads may preserve a common open row; no unrelated
request is reordered or delayed. Both lines fill through existing L2 blocks,
MSHRs, banks, and fill paths, and an unused sibling is charged as wasted
physical traffic.  Analysis also counts a sibling-only line still resident
after the drained workload as terminally unused; reporting does not require a
destructive final cache flush.

### M2: prefetch-aware remote aggregation

Real remote reads retain exact same-line waiter aggregation and bounded
owner/PID/page bitmap batching. A qualified remote candidate must map to the
same owner and 4-KB page as its triggering demand. After RESIDENT/PENDING and
exact RDMA checks, it can occupy only a free line position in an already
existing batch. It cannot create a packet, extend batch residence, or wait
for a future partner. A later demand for that PENDING line joins the exact
entry and receives the existing response.

### M3: Filter-coupled remote reuse

Remote responses use the existing requester L2; CuPath adds no L1.5 cache.
A response with multiple real waiters, prior real SEEN evidence, or a
predicted request subsequently consumed by a real waiter is eligible for a
clean fill. A speculative first-touch response without such evidence can
fill only an invalid victim, never displacing a valid local line. Successful
fill adds RESIDENT. A later real hit marks the retained line useful and keeps
its real-demand-established PATTERN eligible; an unused speculative-line
retirement removes PATTERN.  A candidate that retires unused
before installation also penalizes its bounded predictor generation.

## Safety and fallback

- A RESIDENT false positive performs an unnecessary ordinary tag lookup.
- A PATTERN false positive can only expose a candidate to the remaining exact
  checks; it cannot authorize data or replace a line.
- A PENDING false positive is confirmed by exact L2 MSHR or RDMA state.
- A GRANULARITY_PENDING false positive is confirmed by the exact L2 MSHR.
- A SEEN false positive can only attempt a conservative existing-L2 fill.
- Insert failure makes the affected critical class fail open; speculative
  metadata failure drops only speculative work.
- No approximate result returns data, merges different full addresses,
  discards a demand, or changes write/invalidation correctness.

All features are default-off. With the prefetch and Filter flags disabled,
the baseline demand path does not perform these lookups or allocate this
state.
