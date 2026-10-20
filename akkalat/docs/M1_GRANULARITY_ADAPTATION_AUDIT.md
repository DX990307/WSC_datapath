# M1 Granularity-Adaptation Audit

This file records authoritative state before implementing the
Filter-Guided HBM Granularity Adaptation goal.  It is deliberately separate
from historical result narratives so that later measurements can be tied to
one source state and one binary.

## Initial state (2026-07-20 UTC)

- Starting branch: `observation`.
- Starting commit: `dc1dac768de285851662b14e168fc1f8545db794`.
- Goal branch: `m1-granularity-adaptation`, created without cleaning or
  rewriting the dirty worktree.
- The starting worktree contains the accumulated local M1/M2/M3, tracing,
  runner, benchmark, analysis, and paper changes.  These changes belong to
  the user and must not be reset or replaced wholesale.
- No `runall2.py`, benchmark, or CuPath experiment process was active at the
  initial audit.
- Free filesystem space was approximately 201 GiB.
- Existing `akkalat/baseline/baseline`:
  - build time: `2026-07-19 15:11:30 UTC`;
  - SHA-256: `4dfe9ee4e49a98f6a006a48e0a730d268c389598eee70af3968091ff4b375957`.
- The newest completed four-workload current-M1 directory is
  `akkalat/results/2026-07-20-cupath-v27-timely-mlp-screen4-wg192` and contains
  paired Baseline/M1 artifacts for AES, FWT, FFT, and SpMV.
- `BASE_COMMON_FLAGS` explicitly selects four memory controllers/slices and
  16 L1V MSHRs.  The formal ablation builder independently forces resident
  fast-miss, fill forwarding, and DRAM row continuation off in its current M1.

## Frozen mechanism boundary

M2 and M3 source behavior is frozen at the starting worktree.  Changes to
`mgpusim/timing/rdma` and requester-L2 remote-reuse policy are forbidden unless
the new compound local read exposes a concrete interface correctness bug.  Any
such compatibility change must be documented and must produce identical M2
and M3 behavior with M1 disabled.

## Starting M1

The current formal M1 enables only the L2 Filter-coupled independent 64-byte
prefetch path.  It does not enable ordinary-demand RESIDENT fast miss, fill
forwarding, DRAM row continuation, logical 128-byte requests, or remote
mechanisms.  The shared predictor learns real-demand strides; PATTERN,
RESIDENT, and PENDING typed Filter queries gate an independently allocated
64-byte L2/DRAM request.  This is the Old-M1 diagnostic after the new formal
M1 is implemented.

## HBM model at the starting point

The pre-correction evaluated runner configured:

- protocol: HBM;
- controller/model bus width: 256 bits;
- burst length: 4;
- device width: 256 bits;
- four memory controllers selected by `BASE_COMMON_FLAGS`;
- four banks and four bank groups inside each configured controller model.

Akita computes the subtransaction access unit as
`BusWidth / 8 * BurstLength`, or 128 bytes for this configuration.  Its
splitter aligns every request to that unit.  Consequently a 64-byte request
and a 128-byte request contained in one aligned region both initially create
one modeled DRAM subtransaction.  This is not yet a defensible separation of
controller-request granularity from a native HBM pseudo-channel burst and
must not be used to claim that one native HBM command returns 128 bytes.

The hardware-document audit corrected this configuration. AMD documents
32-B or 64-B HBM read transactions and 64-B writes. Micron's `DQ[127:0]`
describes a 128-bit full-channel data bus, not a 128-byte transaction. With
BL4, a full channel transfers 64 B (two 32-B pseudo-channel bursts). The
corrected simulator therefore uses a 128-bit bus and a 64-B physical access
unit. Every M1 paired-read descriptor must split into two 64-B DRAM
transactions. M1 may preserve their shared row activation but cannot delete
either column transaction. The paired descriptor is an internal controller
scheduling object, not a hardware transaction-size claim.

Primary evidence:

- AMD PG313, *AXI Considerations*, states that HBM-NMU read transactions may
  be 32 B or 64 B, writes are 64 B, and the physical pseudo-channel burst is
  64 DQs times BL4 = 32 B:
  <https://docs.amd.com/r/en-US/pg313-network-on-chip/AXI-Considerations>.
- Micron, *Integrating and Operating HBM2E Memory*, lists `DQ[127:0]` for a
  full channel and explains that each channel contains two 64-bit pseudo
  channels whose BL4 access transfers 256 bits (32 B):
  <https://assets.micron.com/adobe/assets/urn:aaid:aem:275edf31-79e3-4b6c-8bbd-a233babe9281/renditions/original/as/micron-hbm2e-memory-wp.pdf>.

## Historical evidence retained, not reused as formal proof

- The old adjacent 64/128-byte adapter's strongest short-prefix FWT screen
  reported 1.149731x and 15.3783% fewer modeled physical reads, with 65.0148%
  of predicted siblings consumed.  It was a 5,000-WG direction screen, not a
  current formal result.
- The later pure-64-byte physical-unit coalescing experiment added 1.0148x
  geomean across fourteen 48-WG screens but depended on the fixed modeled
  128-byte physical unit and was removed.
- Current independent-prefetch screens show that useful predictions are often
  late and can add DRAM/L2/MSHR pressure.  They motivate changing the request
  formation point, not relabeling the old mechanism.

## Evidence still required

Before the new M1 can be retained, the goal requires:

1. explicit aggregate/64-B-transaction/32-B-pseudo-channel accounting;
2. demand-coupled single/paired 64-B read selection with no independent prefetch;
3. existing-L2 sibling allocation and exact inflight fanout;
4. safe Filter fail-open behavior and no speculative writes;
5. same-binary diagnostic and formal ablations;
6. equal-work representative and fourteen-workload results;
7. an evidence-based retain/reject decision before paper claims change.
