# CuPath retained-mechanism audit

| Removed or replaced | Retained V5 mechanism |
|---|---|
| Independent adjacent-line issue and pair buffering | One bounded real-demand stride predictor proposes at most one ordinary cacheline |
| Blind local speculation | `PATTERN` qualifies the relation; `RESIDENT`/`PENDING` and exact MSHR/resource checks suppress redundant or blocking work |
| Separate remote inflight filter | Shared per-slice `PENDING` plus the exact RDMA line table |
| Standalone remote prefetch packets | Candidate may occupy only a free position in an existing owner/page batch |
| Separate reuse table and page policy | Shared per-slice `SEEN` plus exact response/use evidence |
| L1.5 or remote-only cache | Best-effort clean fill into the existing requester L2 |
| Timeout collection, HLQ, and row scheduler | Work-conserving exact dedup and owner/page packet formation; formal row reordering is off |

Each of the 192 L2 slices owns one typed Cuckoo Filter whose shared bucket
array carries `PATTERN`, `RESIDENT`, `PENDING`, and `SEEN`. M1 creates and
filters local candidates, M2 aggregates surviving remote work, and M3 feeds
real use or unused retirement back to the same prediction lifecycle. The
Filter stores no data, and correctness-sensitive identities are always
confirmed by exact structures.
