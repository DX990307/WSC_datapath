# altis_gups — GUPS (Giga Updates Per Second)

## Algorithm

Measures random memory access throughput on GPU by performing random
read-modify-write operations on a large table of 64-bit unsigned integers.
Based on the HPCC RandomAccess (GUPS) benchmark from the Altis suite.

Each thread uses an xorshift64 PRNG to generate random table indices and
performs atomic XOR updates:

```
for each update:
    state = xorshift64(state)
    table[state % table_size] ^= state
```

This benchmark stresses the memory subsystem with random, irregular access
patterns that defeat caching and prefetching mechanisms.

## Parameters

| Parameter       | Default   | Description                          |
|----------------|-----------|--------------------------------------|
| `table_size`    | 1048576   | Number of 64-bit entries in table    |
| `block_size`    | 256       | Threads per block                    |
| `num_updates`   | 128       | Random updates per thread            |
| `num_threads`   | 65536     | Total threads launched               |
| `iterations`    | 5         | Number of timed iterations           |

Parameters are read from `BENCH_PARAM_*` environment variables.

## Build & Run

```bash
# Auto-detect platform
make
./altis_gups

# Explicit platform
make PLATFORM=metal
make PLATFORM=cuda
make PLATFORM=rocm

# Custom parameters
BENCH_PARAM_table_size=2097152 BENCH_PARAM_num_updates=256 ./altis_gups
```

## Output Format

**JSON-lines to stdout:**
```json
{"type":"kernel","name":"gups_kernel","time_ms":12.345,"params":{"table_size":1048576,...}}
{"type":"summary","total_time_ms":61.725,"metrics":[{"name":"gups","value":0.682}]}
```

**Human-readable to stderr:**
```
Device: Apple M1
GUPS benchmark  |  Table size: 1048576 (8.39 MB)  |  Updates/thread: 128  |  ...

Average time: 12.3450 ms
Throughput:   0.682000 GUPS (Giga Updates Per Second)
PASS
```

## Metric

**GUPS** (Giga Updates Per Second) = `total_updates / time_seconds / 1e9`

where `total_updates = actual_threads × num_updates`.

## Verification

Compares GPU table state against a sequential CPU reference implementation
for the first 1024 entries. Because atomic XOR is used, results are
deterministic and must match exactly.
