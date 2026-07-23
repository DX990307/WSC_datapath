# Filter-coupled metadata state transitions

The Filter is approximate metadata. Exact cache tags, MSHRs, RDMA line
entries, waiter lists, and packet descriptors remain authoritative.

## Predictor and PATTERN

| Event | State action | Result |
|---|---|---|
| First real demand in a bounded stream entry | Record last 64-B line | No candidate |
| A real demand establishes a stride | Record stride and first evidence | No candidate |
| A later real demand repeats that stride | Insert `PATTERN` | Prediction becomes eligible only if insertion succeeds |
| A subsequent real demand repeats the validated relation | Query `PATTERN`; generate at most one 64-B candidate | Candidate continues through RESIDENT/PENDING/exact checks |
| Candidate is consumed by a real demand | Retain/reward generation | Useful feedback |
| Speculative fill retires unused | Delete/penalize `PATTERN` | Future candidates require new real evidence |
| Predictor entry is replaced or its stride changes | Delete old `PATTERN`; increment generation | Late feedback cannot revive stale state |

Speculative requests and responses never call the real-demand training path.

## Local request lifecycle

| Event | Metadata/exact action | Consequence |
|---|---|---|
| Real read enters L2 | Prime `RESIDENT`; check exact MSHR | Possible hit uses normal tag path; reliable negative skips only the certain miss |
| Predictor emits a candidate | Query `PATTERN`, `RESIDENT`, and `PENDING` | Any failed qualification drops it |
| Candidate passes structural checks | Confirm exact MSHR and invalid victim; insert `PENDING` | One low-priority ordinary 64-B request enters the existing miss path |
| Real demand reaches the candidate while pending | Join exact MSHR; mark useful/late as applicable | No duplicate DRAM request |
| Candidate is canceled or loses a race | Delete `PENDING` | No stale in-flight hint |
| Fill completes | Delete `PENDING`; insert `RESIDENT` | Existing L2 stores the line |
| Demand consumes the line | Reward its predictor generation | PATTERN remains eligible |
| Line is evicted/flushes unused | Delete `RESIDENT` and associated `PATTERN` | Unused speculation cannot train itself |

The candidate never reserves a demand-facing resource while waiting. If a
top-port demand, L2 write-buffer entry, controller-facing output, MSHR, or
invalid victim is unavailable at its issue check, it is discarded. This gate
does not inspect the DRAM controller's internal command queue; after injection,
the candidate follows ordinary DRAM scheduling.

## Remote request lifecycle

| Event | Metadata/exact action | Consequence |
|---|---|---|
| Real remote read reaches requester RDMA | Train the same predictor format; query `PENDING`/exact line table | Existing same-line work aggregates exact waiters |
| Remote candidate is qualified | Query `PATTERN`, requester `RESIDENT`, `PENDING`, and `SEEN` | It remains speculative until exact checks pass |
| Same owner/page batch already exists with space | Insert `PENDING`; add one bitmap line | No standalone packet and no extra batch waiting |
| No existing batch or batch is full | Drop candidate | No speculative packet or queueing |
| Later real demand names speculative in-flight line | Add exact waiter; mark useful and admission-eligible | One lower-level access serves both |
| Remote response arrives | Delete `PENDING`; bounded fanout to real waiters | Response handling remains exact |
| Response has multiple real waiters, a demand-consumed prediction, or prior `SEEN` | Attempt clean fill into existing requester L2 | Success inserts `RESIDENT` |
| First speculative response has no evidence | Fill only if an invalid victim exists | Valid local lines are protected |
| Requester-L2 demand hit | Record useful reuse; retain real-demand-established `PATTERN` | Repeated wafer traversal is avoided |
| Installed speculative line retires unused | Delete `PATTERN` | Future prediction requires renewed real evidence |
| Candidate retires unused before installation | Delete `PATTERN`; penalize generation | RDMA-stage feedback suppresses future waste |
| Real remote line completes without admission | Insert `SEEN` | A subsequent real demand has recurrence evidence |
| Remote write/invalidation | Advance exact epoch; delete stale `SEEN`/`RESIDENT` | A late read cannot restore stale data |

`SEEN` is established only by real activity. A speculative request by itself
cannot create recurrence evidence.

## Reliability

Lookup/update port pressure on speculative work causes an immediate drop.
Critical metadata insertion failure marks only that type unreliable:
RESIDENT then keeps the normal tag lookup, PENDING uses exact state directly,
and SEEN does not authorize reuse. Reset clears all four types, outstanding
visibility tickets, predictor mappings, and exact speculative bookkeeping
after the request path is drained.
