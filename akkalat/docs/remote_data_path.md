# Current Filter-coupled memory path

Every logical data request is one 64-B cacheline. There is no pair adapter,
batching timeout, HLQ, L1.5 cache, or speculative waiting queue.
CuPath reuses the existing L2 MSHRs, RDMA line/batch/waiter structures, and
requester-L2 fill path.

## Shared control plane

Each L2 slice owns one physical Typed Cuckoo Filter containing PATTERN,
RESIDENT, PENDING, and SEEN keys. The local predictor beside each L2 slice and
the predictor in requester RDMA use the same bounded real-demand-only stride
design. They generate candidates; the Filter decides whether compact path
state justifies exposing a candidate to exact resource checks.

The safe policy is asymmetric:

- a demand-side metadata uncertainty falls back to the ordinary path;
- a speculative metadata/resource uncertainty drops the candidate; and
- a positive approximate result never substitutes for exact identity.

## M1: predict, filter, and issue local work

```text
real local read -> RESIDENT in parallel with normal front end
                -> known miss skips only tag lookup
                -> ordinary demand path continues immediately

real-demand predictor -> one 64-B candidate at most
                      -> PATTERN positive
                      -> RESIDENT/PENDING negative
                      -> exact MSHR + invalid victim + idle resources
                      -> insert PENDING
                      -> ordinary 64-B miss/fill path
                      -> existing L2
```

Real demands always win at the observable L2 injection boundary. A candidate
is discarded if the top demand port, L2 write-buffer path, controller-facing
output, MSHR, invalid victim, or Filter update port is not immediately
available. This does not inspect the DRAM command queue. It cannot evict a
valid line. Demand arrival for the same in-flight line joins the exact MSHR and
makes the prefetch useful; a late arrival is counted separately.

## M2: aggregate remote work already visible together

Requester RDMA retains bounded fixed-width processing, exact same-line waiter
aggregation, and owner/PID/4-KB-page bitmap packets. A remote candidate is
allowed only when it has the same owner and page as its triggering demand,
passes PATTERN/RESIDENT/PENDING and exact line-table checks, and finds a free
position in an existing batch.

```text
remote candidate -> no matching batch: drop
                 -> matching batch full: drop
                 -> matching batch has space: insert PENDING + piggyback line
```

It never creates its own packet and never extends a batch's residence time.
A later real request for an already piggybacked or in-flight line joins the
exact speculative entry, so one owner access and response serve all waiters.
A demand may also take over a candidate before it enters a batch; that event
is useful predictor feedback but does not prove an avoided remote request.
Metrics therefore report demand-consumed predictions and actual piggyback
lines separately, along with no-batch/full drops, exact demand merges, extra
response bytes, and extra owner reads.

## M3: retain proven remote value in the existing L2

On return, requester RDMA removes PENDING and fanouts only to exact waiters.
Multiple real waiters, a prediction subsequently consumed by a real demand,
or a real SEEN recurrence makes the line eligible for requester-L2 admission.
A speculative response without real reuse evidence can use only an invalid
victim. The clean fill never displaces a local clean line, dirty line, locked
block, or active MSHR.

A successful fill creates normal RESIDENT state in the existing L2. A later
real hit records useful reuse, keeps the associated real-demand-established
PATTERN eligible, and avoids a repeated remote traversal. If a speculative
line is never used before eviction, CuPath deletes its PATTERN. A candidate
consumed or discarded while its RDMA entry is still live also rewards or
penalizes the bounded predictor generation directly. Remote writes use exact
epochs and clear stale reuse metadata.

## Formal and diagnostic configurations

- Baseline: all CuPath paths off.
- M1: RESIDENT fast miss plus Filter-coupled local prefetch.
- M2: exact remote dedup/batching plus existing-batch candidate piggyback;
  requester-L2 reuse off.
- M3: remote recurrence/admission into existing requester L2; batching and
  remote prefetch off.
- Complete: M1 + M2 + M3.

The diagnostic screen additionally includes Filter only, Predictor only,
Ungated Prefetch, and exact-versus-Cuckoo metadata. All formal configurations
use 16 L1V MSHRs, four 1-MB L2 slices per GPM, 64 MSHRs/slice, 16
requests/cycle/slice, and a 10-cycle L2 lookup.
