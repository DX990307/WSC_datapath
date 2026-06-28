
# Mechanism 1 Current Results Analysis

## Summary

Mechanism 1 is working on the datapath it targets, especially the L2 miss to
DRAM 128B access-unit path. The clearest evidence is that DRAM read transaction
count and DRAM read response time both decrease.

However, the current results do not yet support a strong end-to-end speedup
claim. For several benchmarks, the optimized DRAM latency is either hidden by
parallel execution or offset by increased DRAM read bytes. The current strongest
claim is:

> Mechanism 1 reduces DRAM command/response pressure and improves L2/DRAM
> datapath latency, but this does not always translate into end-to-end runtime
> improvement.

## Experiment Status

Two result directories were inspected:

- `akkalat/results/m1-baseline-vs-mechanism1`
- `akkalat/results/m1-dir-au-batch-no-trace`

Important caveat:

- `m1-baseline-vs-mechanism1` currently contains only baseline metrics for the
  completed benchmarks. It has 4 completed baseline metrics and 0 completed
  mechanism1 metrics.
- `m1-dir-au-batch-no-trace` contains runs with Mechanism 1 enabled, but those
  commands did not include `-disable-servers`, while the new baseline directory
  did include `-disable-servers`.

Therefore, the current cross-directory comparison is useful for datapath
evidence, but it is not a strict paired runtime comparison.

## Datapath Evidence

The table below compares baseline results from
`m1-baseline-vs-mechanism1` against Mechanism 1 results from
`m1-dir-au-batch-no-trace`.

| Benchmark | DRAM read trans | DRAM read response | L1V avg latency | L2 avg latency | 128B access-unit saved | L2 dir batch avg |
|---|---:|---:|---:|---:|---:|---:|
| bitonicsort | -50.69% | -40.35% | -23.79% | -24.88% | 49.98% | 1.00 |
| floydwarshall | -20.01% | -14.30% | -4.12% | -12.19% | 19.77% | 1.06 |
| im2col | -19.09% | -17.94% | -4.89% | -12.87% | 19.00% | 1.08 |
| relu | -50.14% | -38.29% | -23.43% | -26.37% | 49.96% | 1.00 |

This shows that Mechanism 1 is not a no-op. The DRAM path is visibly improved:

- DRAM transactions decrease.
- DRAM response time decreases.
- L1V and L2 observed request latency also decrease.

For relu specifically:

| Metric | Baseline | Mechanism 1 | Change |
|---|---:|---:|---:|
| DRAM read transactions | 336,673 | 167,872 | -50.14% |
| DRAM read response | 5.73e-08 | 3.54e-08 | -38.29% |
| Total time | 9.298e-06 | 9.253e-06 | +0.49% speedup |

So relu confirms that the datapath is optimized, but it also shows that the
datapath improvement is mostly hidden from end-to-end runtime.

## DRAM Bytes

The 128B access-unit mechanism can reduce transaction count while increasing
the number of bytes fetched. This matters because extra bytes can consume DRAM
bandwidth and offset the latency benefit.

| Benchmark | Baseline DRAM read MB | Mechanism DRAM read MB | Byte change | Runtime change |
|---|---:|---:|---:|---:|
| bitonicsort | 43.24 | 42.64 | -1.39% | +1.61% |
| floydwarshall | 38.51 | 61.62 | +59.99% | -0.62% |
| im2col | 19.83 | 32.09 | +61.81% | -0.31% |
| relu | 21.55 | 21.49 | -0.28% | +0.49% |

This explains why floydwarshall and im2col do not improve even though DRAM
transaction count and response time improve. The mechanism reduces command
count, but it also causes around 60% more DRAM read traffic for those workloads.

## Why Runtime Does Not Always Improve

Mechanism 1 improves a specific datapath:

1. L2 directory same-set request batching.
2. L2 miss to DRAM 128B access-unit coalescing.

The current results show that the second part is effective. The first part is
weak in these workloads because the average directory batch size is close to 1.
For relu, `l2_batch_dir_avg_size` is about `1.00`, which means directory
batching almost never combines multiple useful requests.

End-to-end runtime does not always improve for three reasons.

First, memory latency can be hidden. If there are enough independent wavefronts
or workgroups, shorter DRAM response time may not reduce the critical path.
This is likely what happens in relu: DRAM response improves by about 38%, but
total runtime improves by only about 0.5%.

Second, 128B access-unit coalescing can overfetch. If two useful 64B cachelines
are not both needed, a 128B access can reduce transaction count but increase
actual bytes moved. This appears to hurt floydwarshall and im2col.

Third, L2 directory batching is currently too weak to provide much runtime
effect. Most observed batches have size 1, so this part of Mechanism 1 is not
yet a strong contributor.

## Current Supported Conclusions

Supported:

- Mechanism 1 reduces DRAM read transaction count.
- Mechanism 1 reduces DRAM read response time.
- Mechanism 1 reduces observed L1V/L2 request latency.
- The 128B access-unit coalescing part is the main effective component.

Not yet supported:

- Mechanism 1 gives broad end-to-end speedup.
- L2 directory batching is a major contributor.
- relu is a strong positive runtime case.

Best current wording:

> Mechanism 1 improves the L2-to-DRAM datapath by reducing DRAM access-unit
> transactions and DRAM response time. Runtime speedup depends on whether this
> latency is on the critical path and whether the 128B access unit causes
> additional read traffic.

## Next Experiment

Run a clean paired comparison in one directory, with identical baseline and
Mechanism 1 settings:

```bash
python3 akkalat/run_m1_clean_compare.py \
  --arm baseline,mechanism1 \
  --benchmarks all \
  --configs sample_all \
  --output-root akkalat/results/m1-baseline-vs-mechanism1 \
  --max-workers 10 \
  --max-wg 78600 \
  --sampled-warmups 512 \
  --sampled-granularities 512 \
  --resume
```

After it finishes, regenerate the summary only:

```bash
python3 akkalat/run_m1_clean_compare.py \
  --arm baseline,mechanism1 \
  --output-root akkalat/results/m1-baseline-vs-mechanism1 \
  --compare-only
```

The clean paired run should be the only source used for final runtime claims.

## Relevant Code Snippets

This section records the most relevant implementation snippets for Mechanism 1.
The goal is to make the mechanism easy to inspect without jumping across the
repository.

### User-Facing Flags

Source: `akkalat/baseline/runner/flag.go`

```go
var l2DirBatchWindowFlag = flag.Int("l2-dir-batch-window", 0,
	"Maximum same-set L2 directory requests to batch into one lookup. 0 disables batching.")
var l2DramAccessUnitCoalesceFlag = flag.Bool("l2-dram-access-unit-coalesce", false,
	"Coalesce adjacent L2 cache-line fills into one DRAM access-unit read.")
```

Source: `akkalat/baseline/runner/runner.go`

```go
WithL2DirBatch(*l2DirBatchWindowFlag).
WithL2DramAccessUnitCoalescing(*l2DramAccessUnitCoalesceFlag).
WithForceLocalDataAccess(*forceLocalDataAccessFlag)
```

The clean comparison runner sets the Mechanism 1 flags only on the
`mechanism1` arm.

Source: `akkalat/run_m1_clean_compare.py`

```python
return argparse.Namespace(
    ...
    l2_dir_batch_window=args.l2_dir_batch_window if mechanism else 0,
    l2_dram_access_unit_coalesce=mechanism,
    ...
)
```

### L2 Directory Same-Set Batching

Source: `akita/mem/cache/writeback/directorystage.go`

The directory stage now accepts a batch of transactions into one pipeline item:

```go
first := item.(*transaction)
batch := ds.collectDirBatch(now, first)
ds.pipeline.Accept(now, &dirPipelineItem{
	transactions: batch,
})
```

The batch collector only groups adjacent pending transactions that map to the
same cache directory set. It stops when the next request is missing, maps to a
different set, or the configured window is full.

```go
func (ds *directoryStage) collectDirBatch(
	now sim.VTimeInSec,
	first *transaction,
) []*transaction {
	batch := []*transaction{first}
	ds.recordDirStart(now, first)
	ds.cache.dirStageBuffer.Pop()

	window := ds.cache.l2DirBatchWindow
	if window <= 1 {
		return batch
	}

	firstSet, ok := ds.cacheSetID(first)
	if !ok {
		ds.recordDirBatch(batch)
		return batch
	}

	for len(batch) < window {
		item := ds.cache.dirStageBuffer.Peek()
		if item == nil {
			break
		}

		next := item.(*transaction)
		nextSet, ok := ds.cacheSetID(next)
		if !ok || nextSet != firstSet {
			break
		}

		batch = append(batch, next)
		ds.recordDirStart(now, next)
		ds.cache.dirStageBuffer.Pop()
	}

	ds.recordDirBatch(batch)
	return batch
}
```

Batch statistics are collected here:

```go
func (ds *directoryStage) recordDirBatch(batch []*transaction) {
	if len(batch) == 0 {
		return
	}

	ds.cache.l2BatchStats.DirBatchGroups++
	ds.cache.l2BatchStats.DirBatchRequests += uint64(len(batch))
	if uint64(len(batch)) > ds.cache.l2BatchStats.DirMaxBatchSize {
		ds.cache.l2BatchStats.DirMaxBatchSize = uint64(len(batch))
	}
}
```

Interpretation:

- High `DirBatchRequests / DirBatchGroups` means directory batching found useful
  same-set request locality.
- Values close to 1 mean this part of Mechanism 1 is mostly inactive.

### L2 Miss to DRAM 128B Access-Unit Coalescing

Source: `akita/mem/cache/writeback/writebufferstage.go`

The write-buffer stage first tries to attach the current miss to an existing
in-flight 128B access-unit read. If that succeeds, the new transaction reuses
the existing DRAM read request.

```go
coalescing := wb.l2DramAccessUnitCoalesceEnabled()

var lowModulePort sim.Port
if coalescing {
	lowModulePort = wb.cache.lowModuleFinder.Find(trans.fetchAddress)
	if wb.coalesceAccessUnitFetch(trans, lowModulePort) {
		wb.cache.writeBufferBuffer.Pop()
		return true
	}
}
```

If no existing access-unit read can be reused, the mechanism sends a new DRAM
read at the 128B access-unit base address and counts it as an access-unit read.

```go
readAddress := trans.fetchAddress
readSize := uint64(1 << wb.cache.log2BlockSize)
if coalescing {
	readAddress = wb.l2DramAccessUnitBase(trans.fetchAddress)
	readSize = wb.cache.l2DramAccessUnitBytes
	wb.cache.l2BatchStats.AccessUnitReads++
}

read := mem.ReadReqBuilder{}.
	WithSrc(wb.cache.bottomPort).
	WithDst(lowModulePort).
	WithPID(trans.fetchPID).
	WithAddress(readAddress).
	WithByteSize(readSize).
	WithInfo(accessReqInfo(trans.accessReq())).
	Build()
```

Coalescing is enabled only when the configured DRAM access unit is larger than
the cacheline size.

```go
func (wb *writeBufferStage) l2DramAccessUnitCoalesceEnabled() bool {
	cacheLineBytes := uint64(1 << wb.cache.log2BlockSize)
	return wb.cache.l2DramAccessUnitCoalesce &&
		wb.cache.l2DramAccessUnitBytes > cacheLineBytes
}
```

The actual reuse path searches in-flight fetches for a matching access-unit
read to the same lower memory module. It rejects matching the exact same
cacheline address, so the coalescing represents a neighboring cacheline sharing
the same 128B unit.

```go
func (wb *writeBufferStage) coalesceAccessUnitFetch(
	trans *transaction,
	lowModulePort sim.Port,
) bool {
	if len(wb.inflightFetch) >= wb.maxInflightFetch {
		return false
	}

	read := wb.findAccessUnitRead(trans, lowModulePort)
	if read == nil {
		return false
	}

	trans.fetchReadReq = read
	wb.inflightFetch = append(wb.inflightFetch, trans)
	wb.cache.l2BatchStats.AccessUnitCoalesced++
	return true
}

func (wb *writeBufferStage) findAccessUnitRead(
	trans *transaction,
	lowModulePort sim.Port,
) *mem.ReadReq {
	unitBase := wb.l2DramAccessUnitBase(trans.fetchAddress)
	for _, inflight := range wb.inflightFetch {
		read := inflight.fetchReadReq
		if wb.accessUnitReadMatches(read, trans, lowModulePort, unitBase) &&
			inflight.fetchAddress != trans.fetchAddress {
			return read
		}
	}

	return nil
}
```

Interpretation:

- `AccessUnitReads` counts new 128B DRAM access-unit reads.
- `AccessUnitCoalesced` counts 64B L2 miss fills that reused an in-flight 128B
  access-unit read.
- This can reduce DRAM transaction count and DRAM response pressure.
- It can also overfetch if the 128B unit contains data that is not actually
  useful.

### Mechanism 1 Stats

Source: `akita/mem/cache/writeback/writebackcache.go`

```go
// L2BatchStats reports locality-aware batching inside the writeback L2 cache.
type L2BatchStats struct {
	DirBatchGroups      uint64
	DirBatchRequests    uint64
	DirMaxBatchSize     uint64
	AccessUnitReads     uint64
	AccessUnitCoalesced uint64
}

// L2BatchStats returns counters for locality-aware L2 batching.
func (c *Cache) L2BatchStats() L2BatchStats {
	return c.l2BatchStats
}
```

Source: `akkalat/baseline/runner/report.go`

```go
func (r *Runner) collectL2BatchStats(
	where string,
	stats writeback.L2BatchStats,
) {
	r.metricsCollector.Collect(
		where, "l2_batch_dir_groups", float64(stats.DirBatchGroups))
	r.metricsCollector.Collect(
		where, "l2_batch_dir_requests", float64(stats.DirBatchRequests))
	r.metricsCollector.Collect(
		where, "l2_batch_dir_max_size", float64(stats.DirMaxBatchSize))
	if stats.DirBatchGroups > 0 {
		avg := float64(stats.DirBatchRequests) /
			float64(stats.DirBatchGroups)
		r.metricsCollector.Collect(where, "l2_batch_dir_avg_size", avg)
	}
	r.metricsCollector.Collect(
		where, "l2_batch_access_unit_reads",
		float64(stats.AccessUnitReads))
	r.metricsCollector.Collect(
		where, "l2_batch_access_unit_coalesced",
		float64(stats.AccessUnitCoalesced))
}
```

The Driver row is the aggregate across all L2 caches, and per-L2 rows are also
emitted. For high-level analysis, the Driver row is usually enough.

## Possible Mechanism Improvement

The current 128B access-unit behavior is useful when both adjacent 64B
cachelines are demanded close together. It is less useful when it fetches extra
data that is not needed. A future version should distinguish:

- true coalescing: two demanded 64B cachelines share one 128B access unit;
- overfetch: one demanded 64B cacheline triggers a 128B access alone.

The mechanism should prefer true coalescing and avoid overfetch-heavy cases.
