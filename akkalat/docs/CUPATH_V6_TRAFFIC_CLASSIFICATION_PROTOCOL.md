# CuPath V6 baseline traffic classification protocol

This protocol was frozen before inspecting any CuPath V6 performance result.
It replaces the historical, hard-coded `All Local / Mixed / Remote` labels.

The classification input is a separate **Baseline-only passive profiling
run** made with the V6 frozen binary, the fixed paper hardware flags, the
same full workload launch, and the same runner-side observed-WG limit.  The
profiling trace is not used for speedup because its provenance accounting has
host-simulator overhead.

For every translated data demand entering the requester memory hierarchy,
including vector and scalar global-memory reads/writes but excluding page-table
walks and speculative prefetch candidates:

```
remote_fraction = requests whose page owner differs from requester GPU
                  / all translated read and write requests
```

The owner is the authoritative virtual-memory page owner (`page.DeviceID`),
not an inferred network destination.  Reads and writes are both included;
speculative prefetch candidates are excluded.

The thresholds are fixed as follows:

| Class | Baseline remote fraction |
|---|---:|
| Exact-local | exactly 0% |
| Local-dominant | greater than 0% and at most 25% |
| Mixed | greater than 25% and at most 75% |
| Remote-dominant | greater than 75% |

All fourteen workloads remain in the ungrouped overall geomean regardless
of class.  The report also preserves the raw fraction and an empirical CDF;
the class boundaries must not be moved after observing the results.
