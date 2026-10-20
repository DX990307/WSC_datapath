# W2 benchmarks: new Complete vs baseline (2026-10-20)

Code: `isca27/w2-complete` @ d09b18b (m1m2 + wport-a..d). The binary was `baseline_d09b18b` (linux/amd64, md5 6f9d3c7e...).
Runs: yss2 and yss3, `~/akita-exp-test/w2c/src/results/<id>/`. Configs: `mi200` (baseline), `complete_new` (M1 = T18 full/W1024/dist, M2 = old m2, M3 = remote reuse, dedup owner m1), and `complete_hpca_flags`.
Every run used `-magic-memory-copy=true`, checked in each `config_used.json`. No run used `-verify` or `-report-l2-source`.
Names follow `1020_abl_<item>_mi200[+completenew|+completehpca]_d09b18b`.

The data is in `w2complete.csv`: one row per benchmark, with base/new/hpca columns.

## Regression gate (passed)
| run | result | expected |
|---|---|---|
| fir -max-wg 4096, mi200 | 26.568 us, md5 d56a6814... | same |
| matrixmultiplication -max-wg 4096, mi200 | 345.433 us, md5 22a58b27... | same |
| floydwarshall -max-wg 32768, complete_new | 10.139 us | 10.139 us (m1m2) |

## Sizes
Each size was picked from the porting agents' tiny runs, scaling wall time with problem size, to aim for roughly 0.5–1 h of baseline wall time on loaded servers.

| benchmark | args | reason |
|---|---|---|
| npb_mg | `-mg-class W` (1 cycle) | S has only 128 WGs. W is the next class: 5.8k s wall, 127k WGs |
| rodinia_lud | `-lud-n 1024` | the tiny run at n1024 took 3.3k s. LUD has few WGs by nature |
| allreduce | `-ar-per-cu 2` | 2x the tiny size |
| sssp | `-sssp-scale 14` | 2x of s13 |
| ivfflat | defaults (N 16384, nlist 128, nprobe 8, Q 256) | the tiny run already took 2.6k s |
| w2-npbis | `-npbis-n 1048576 -npbis-iterations 1` | class-W key count. The 2-iteration baseline crashed (see below) |
| embbag | `-eb-batch 1024` | 8x batch, 13k WGs |
| npbcg | `-cg-class W` | 6x the nonzeros of S. The baseline took longer than estimated (6.1k s) |
| gups | `-gups-log-table 18 -gups-update-mult 8` | 2 MB table (inside L2 TLB reach), 2M updates, 1344 WGs. The default 2^22 table is TLB-bound and far too slow |
| w2-shocsort | `-shocsort-n 524288` | 8x the default |
| altis_cfd | `-cfd-cells 194000 -cfd-block 128` | 2x the upstream mesh. Block 128 gives 1516 WGs per kernel (block 256 would give 758) |
| lonestar_bh | `-bh-bodies 86016` | 1344 WGs, tested by wport-d |
| w2-atax | `-atax-nx 4096 -atax-ny 4096 -atax-rows-per-chunk 64` | 4x n2048 |
| w2-fft2d | `-fft2d-n 1024` | n2048 would be about 16x |
| bfs | `-bfs-scale 16` | 4x s14 |
| graphtc | `-tc-scale 13` | the size that was tested. It crashes (see below) |

## Results: baseline vs complete_new
Times are simulated `total_time`. M1 = T18 requester merges / baseline-equivalent remote reads. M2 = remote wire lines / logical remote reads, then average lines per packet. M3 = requester-L2 hits on remote lines.

| benchmark | category | base us | new us | speedup | insts (= in both) | WGs | M1 merged | M2 wire/logical, batch | M3 hits | wall s base/new | RSS GB base/new |
|---|---|---|---|---|---|---|---|---|---|---|---|
| mg-W | multigrid stencil | 2193.4 | 606.0 | 3.62 | 41.1M | 127377 | 12.3% | 0.51, 1.28 | 3.08M | 5757/4386 | 5.1/6.4 |
| lud-n1024 | dense LU | 4148.1 | 2736.6 | 1.52 | 40.4M | 89440 | 27.4% | 0.68, 1.12 | 171k | 3435/2707 | 3.5/4.5 |
| allreduce-pc2 | collective | 1008.4 | 235.7 | 4.28 | 10.1M | 34944 | 15.5% | 1.00, 1.18 | 0.9k | 3321/3300 | 5.8/7.2 |
| sssp-s14 | graph | 2777.0 | 673.9 | 4.12 | 30.4M | 45056 | 15.3% | 0.19, 1.03 | 2.62M | 3173/2123 | 3.2/3.9 |
| ivfflat | vector search | 777.6 | 263.3 | 2.95 | 35.0M | 2674 | 41.7% | 0.81, 1.62 | 296k | 2798/2714 | 3.4/4.8 |
| npbis-n1m-i1 | integer sort | 1144.5 | 298.7 | 3.83 | 3.34M | 5394 | 13.9% | 0.63, 1.11 | 355k | 1539/1146 | 3.2/4.1 |
| embbag-b1024 | ML embedding | 988.8 | 972.6 | 1.02 | 26.3M | 13312 | 6.4% | 0.66, 1.20 | 1.62M | 3159/3083 | 4.4/5.4 |
| npbcg-W | sparse CG | 5896.5 | 936.3 | 6.30 | 30.3M | 57214 | 18.4% | 0.07, 1.11 | 7.07M | 6104/3168 | 3.2/4.4 |
| gups-log18m8 | random RMW | 682.6 | 128.0 | 5.33 | 0.84M | 1344 | 1.7% | 0.98, 1.00 | 14k | 2261/2164 | 3.0/4.0 |
| shocsort-n512k | radix sort | 759.5 | 469.7 | 1.62 | 19.4M | 24840 | 47.5% | 0.52, 1.68 | 35k | 1173/1013 | 2.8/3.4 |
| cfd-c194k-b128 | unstructured CFD | 1002.3 | 152.0 | 6.60 | 8.34M | 12128 | 6.1% | 0.67, 1.32 | 864k | 2005/1512 | 2.8/3.7 |
| bh-n86k | n-body tree | 435.3 | 360.9 | 1.21 | 19.3M | 2688 | 18.6% | 0.22, 1.08 | 547k | 2151/2055 | 2.4/2.9 |
| atax-n4096 | dense LA | 831.1 | 288.1 | 2.88 | 10.1M | 5136 | 16.9% | 0.91, 1.20 | 176k | 1795/1511 | 3.7/4.7 |
| fft2d-n1024 | FFT | 840.3 | 360.4 | 2.33 | 14.5M | 20480 | 24.9% | 0.72, 2.09 | 106k | 1641/1391 | 3.4/4.1 |
| bfs-s16 | graph traversal | 1072.5 | 605.5 | 1.77 | 12.9M | 116480 | 11.9% | 0.50, 1.08 | 751k | 1825/1505 | 4.4/4.9 |
| graphtc-s13 | triangle count | crash | crash | – | – | 25524 | – | – | – | 1031/1033 | 4.7/4.5 |

GMEAN of complete_new over baseline:
- **2.82x** over the 15 benchmarks with valid results (all except graphtc).
- **3.04x** over the 14 non-trivial ones, which leaves out embbag (1.02x).

Complete changed the instruction count by 0.000% on every valid pair, and the WG counts match.

## Failures (no code was changed)
- **graphtc** fails under all three configs, with the same deterministic panic about 1030 s into the run: `emu.(*ALUImpl).runDSREAD2B32: slice bounds out of range [:1044] with capacity 1040`. The `tc_count` kernel has `__local uint red[4*65]` = 1040 B, and the LDS tree reduction reads 4 B past the end. This is a bug in the port or the emulator, not in Complete. The wport-a tiny run at 7f501c2 crashed the same way.
- **w2-npbis with 2 iterations**: the baseline crashed at 1638 s in `runDSREADB32` with a negative LDS address (`[:4294967164]`). complete_new finished (529.6 us). This crash depends on the data or the timing, which points to a stale or racy read in the second iteration, so the 2-iteration result is unreliable. The table uses the 1-iteration run instead.
- **gups** uses plain load/xor/store, so lost updates depend on timing. The 5.3x comes with only 1.7% M1 merges and M2 batch 1.0. It needs a mechanism breakdown before anyone cites it.

## complete_hpca_flags: partial, dropped by user
The user dropped HPCA-style Complete on 2026-10-20. Four runs were still going and were killed by recorded PID: mg, lud, embbag and npbcg. Eleven had already finished, and they stay in the CSV marked "partial, dropped by user":

allreduce 4.12x, sssp 3.39x, ivfflat 2.89x, npbis-i1 1.49x, gups 1.03x, shocsort 1.11x, cfd 2.77x, bh 1.19x, atax 2.61x, fft2d 1.18x, bfs 1.47x. graphtc crashed as above.

On these 11 benchmarks the GMEAN is 1.88x for hpca and 2.98x for complete_new.
