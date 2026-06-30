# M1 + M2 Critical Path Study

Updated: 2026-06-30

This note is for benchmarks where M1 + M2 has little or negative end-to-end speedup. The goal is to separate two cases:

1. M1/M2 did not effectively optimize the intended datapath.
2. M1/M2 optimized the intended datapath, but another path is on the critical path.

## Current Evidence

Primary run:

`akkalat/results/2026-06-29-00-33-57-runall`

Important caveat: this run is incomplete. `aes` and `kmeans` have no usable metrics, and some mechanisms are missing for `fir`, `matrixtranspose`, and `spmv`. Treat this as a diagnosis run, not a final ablation table.

## Decision Tree

Use three levels of evidence for every benchmark.

### 1. Opportunity

Check whether the mechanism had work to do.

- M1 opportunity: nonzero `l2_batch_au_coalesced`.
- M2 opportunity: nonzero `m2_rdma_batchable_requests`.

If the opportunity counter is zero, lack of speedup is not surprising. The mechanism was enabled, but the access pattern did not expose that batching opportunity.

### 2. Datapath Effect

Check whether the optimized path actually improved.

Useful counters:

- `CPIStack.VMem`
- L1V/L2 cache `read_avg_latency`
- L1V/L2 cache miss and MSHR-hit counts
- M1 AU coalescing counters
- M2 batch packets, batched lines, bypassed requests, timeout/capacity flush counts

If M1/M2 opportunity is high but `CPIStack.VMem` or cache/RDMA latency does not improve, then the mechanism is probably not reducing the real memory wait seen by CUs.

### 3. Criticality

Check whether memory was the end-to-end bottleneck.

If `CPIStack.VMem` improves but total time does not improve, then the optimized datapath is probably hidden by another critical path, such as VALU, scalar memory, synchronization, scheduling, or non-batched memory traffic.

## Current Classification

| benchmark | M1+M2 speedup | current diagnosis |
|---|---:|---|
| `bitonicsort` | +1.23% | M1 has opportunity, M2 has none. VMem is a small part of baseline CPI, so the optimized path is probably not the main critical path. |
| `fft` | +0.38% | M1 has opportunity, M2 has none. VMem is very small, so memory batching is mostly hidden. |
| `im2col` | +0.43% | M1 and M2 have opportunity, but baseline is mostly compute/VALU. Need confirm whether VMem latency actually drops. |
| `matrixtranspose` | +0.02% | Very suspicious: VMem is large, and M1+M2 has opportunity, but speedup is near zero. This is the best candidate for "mechanism does not hit the critical memory path." |
| `pagerank` | -8.02% | M2 alone helps slightly, but M1+M2 hurts. This likely means batching overhead or queueing interferes with the actual critical path. |
| `relu` | +0.05% | M1 has opportunity, M2 has none. VMem is large, but M1+M2 does not reduce it. This suggests M1 AU coalescing is not the latency source that stalls CUs. |
| `spmv` | -1.06% | M2 has huge opportunity, but M1+M2 is worse. This is the best candidate for M2 queue wait, batch flush policy, or remote response serialization becoming harmful. |

Positive controls:

| benchmark | M1+M2 speedup | why useful |
|---|---:|---|
| `matrixmultiplication` | +40.33% | Confirms M2 can help when remote batching aligns with the bottleneck. |
| `simpleconvolution` | +36.03% | Confirms M1/M2 can produce visible speedup, though this should be rerun because the workload is short. |
| `floydwarshall` | +6.88% | Smaller but clean positive signal; useful as a moderate-memory case. |

## Benchmark-Specific Hypotheses

### matrixtranspose

This should be studied first. It has a large memory CPI share, but M1+M2 barely changes total time. The likely explanations are:

- The memory stall is from L1V/L2 bank pressure or outstanding-request limits, not from AU coalescing or requester-side RDMA packets.
- Batched accesses reduce packet count but do not shorten the return path that gates the wavefront.
- The access pattern creates many independent misses, so the CU waits on a different subset of requests than the ones being batched.

Needed data:

- L1V and L2 `read_avg_latency` and read transaction counts.
- `CPIStack.VMem` before/after M1+M2.
- M2 `batch_packets`, `batched_lines`, `flush_timeout`, `flush_capacity`.
- Per-stage memory path summary for one smaller run.

### relu

M2 has no opportunity, so this is mainly an M1 question. If VMem stays almost unchanged despite high AU coalescing, then AU coalescing is not on the critical return path.

Needed data:

- Compare baseline vs M1 only.
- L2 read miss latency and DRAM access counters.
- DRAM useful bytes vs requested bytes, to see whether AU coalescing reduces real DRAM work or only merges bookkeeping.

### spmv

M2 has extremely high batchable-request count, but M1+M2 is worse. This points to policy overhead rather than lack of opportunity.

Needed data:

- Sweep `--m2-rdma-max-wait-ns` with 0, 10, 25, 50, 100.
- Sweep `--m2-rdma-max-batch-lines` with 2, 4, 8.
- Track timeout flushes vs capacity flushes.
- Track batchable requests per packet. If the average batch is small while wait time is nonzero, the queue is adding delay without enough packet reduction.

### pagerank

M2 alone helps a little, but M1+M2 hurts. This suggests M1 and M2 are interacting through queueing or changed ordering.

Needed data:

- Compare baseline, M1, M2, M1+M2 with repeat runs.
- Check whether M1 increases M2 timeout flushes or changes average batch size.
- Check whether L2/DRAM latency improves while RDMA or VMem worsens.

## Experiments To Run Next

### A. Complete The Table

Rerun missing combinations first so the ablation table is complete.

Important missing combinations from the current run:

- `aes`: baseline, M1, M2, M1+M2
- `kmeans`: baseline, M1, M2, M1+M2
- `matrixtranspose`: M1, M2
- `spmv`: M1
- `fir`: M2

### B. No-Effect Focus Rerun

Run only the suspicious benchmarks with all mechanisms:

```bash
python3 akkalat/runall2.py \
  --benchmarks bitonicsort,fft,im2col,matrixtranspose,pagerank,relu,spmv \
  --configs sample_all \
  --mechanisms all \
  --photon \
  --max-wg 76800 \
  --sampled-warmups 512 \
  --sampled-granularities 512 \
  --max-workloads 10 \
  --min-free-ram-gb 40 \
  --memory-scan-interval-minutes 1 \
  --output-dir akkalat/results/m1-m2-critical-path-rerun
```

### C. M2 Policy Sweep

For `spmv`, `pagerank`, and `matrixtranspose`, sweep M2 wait and batch size. The key question is whether M2 is adding queue wait without enough batch compression.

Suggested configs:

- `--m2-rdma-max-wait-ns 0`
- `--m2-rdma-max-wait-ns 10`
- `--m2-rdma-max-wait-ns 25`
- `--m2-rdma-max-batch-lines 2`
- `--m2-rdma-max-batch-lines 4`
- `--m2-rdma-max-batch-lines 8`

### D. Oracle-Style Criticality Tests

Use aggressive simulator parameters to see which path has headroom.

- Lower switch latency: if speedup appears, remote network latency is critical.
- Increase bandwidth / memory banks: if speedup appears, DRAM throughput is critical.
- Lower MMU/TLB latency: if speedup appears, translation is critical.
- Compare with M1/M2 enabled: if oracle helps but M1/M2 does not, M1/M2 is not optimizing the actual critical segment.

## Expected Conclusions

The current data likely supports a nuanced conclusion:

- M1 and M2 are implemented and can produce speedup on some workloads.
- For several workloads, the batching opportunity exists but is not enough evidence by itself.
- For `bitonicsort`, `fft`, and `im2col`, lack of speedup is likely because memory batching is not the dominant critical path.
- For `matrixtranspose`, `relu`, and `spmv`, lack of speedup is more interesting: the benchmark appears memory-sensitive, but M1/M2 does not reduce the memory stall that controls end-to-end time.
- The next strongest evidence should come from CPIStack plus L1V/L2/RDMA/DRAM latency counters, not from total runtime alone.
