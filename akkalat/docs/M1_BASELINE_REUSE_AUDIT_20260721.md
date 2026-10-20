# M1 Baseline Reuse Audit (2026-07-21)

## Reuse policy

A Baseline result is reusable across binaries.  Binary SHA is not a matching
condition.  The required conditions are the same benchmark input, hardware
configuration, sampling mode, L1V MSHR count, and executed workgroup set.

The current formal configuration requires:

- `-l1v-mshr-entries=16`
- `-max-wg=78600`
- `-sampled -branch-sampled -kernel-sampled`
- 4 memory banks, 48-GB/s configured bandwidth, 32-cycle switch latency
- RDMA width/latency/outstanding = 8/10/64
- MMU-TLB lookup latency = 80

## Repository-wide result

Scanning every `EXPERIMENT_METADATA.json` and successful Baseline
`*_result.json` under `akkalat/results` found:

- 42 planned Baseline cells with the exact formal flags and
  `-max-wg=78600`, but **zero successful results**.  The cells in
  `2026-07-21-m1-only-maxwg78600-v1` and `v2` are startup failures and contain
  no valid timing result.
- Successful same-hardware, same-sampling, L1V=16 Baselines exist at
  `-max-wg=76800` for AES, BSort, FWT, FFT, and FIR.  They are not formal reuse
  candidates because they execute 1,800 fewer observed workgroups and have a
  different mapped-WG hash.
- The two July 12 sampled-validation directories contain successful
  `-max-wg=78600` Baselines. Their recorded commands omit both
  `-l1v-mshr-entries=16` and the three sampling flags, however. The checkout's
  pre-change R9Nano builder default was 160 L1V MSHRs, so binary-independent
  config equivalence cannot be established from those files. They may be
  reported as historical/indicative reference times, but are not evidence for
  an equal-configuration 16-MSHR speedup.
- Other successful Baselines use small diagnostic limits such as 48, 192, or
  768 workgroups.  They are useful only for smoke/diagnostic comparisons.
- No natural-completion Baseline for the paper's 14 workloads was found with
  the complete formal configuration.

## Action

No Baseline is launched again.  M1 development compares mechanism counters
and same-limit candidate runs.  Formal speedup and geomean remain unproven
until an existing exact Baseline result is located or supplied.  A result with
a different binary is acceptable; a result with a different workgroup set is
not.

The reusable-cell scan is now executable rather than manual:

```bash
cd akkalat
python3 find_reusable_baselines.py
```

It removes only the executable path, benchmark identity, and metric-output
path when comparing commands; all modeled flags remain in the configuration
signature.  It then requires a successful Baseline result and valid 78,600-WG
mapping evidence.  Binary SHA is deliberately not a matching condition.  The
current output is `results/BASELINE_REUSE_MANIFEST_78600.csv`, with reference
configuration SHA-256
`967e4035865ecf1940cc64951fcb51899bbe2dd142c66f773b7a8dfb6d46e12e`.
The 2026-07-21 scan reports 0/14 reusable cells.
