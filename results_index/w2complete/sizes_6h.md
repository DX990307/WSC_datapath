# W2 尺寸选择：baseline ≤ 6 h、WG 铺满 wafer、footprint 尽量大（2026-10-20）

代码：`isca27/w2-complete` @ d09b18b，binary `baseline_d09b18b`（`~/akita-exp-test/w2c/bin`），mi200，magic copy，无 verify。
所有试跑都在 `~/akita-exp-test/sizing/runs/<name>/`（yss2 和 yss3）。没有跑完整仿真，没有改代码，没有提交。
CSV：`sizes_6h.csv`，每个 benchmark 一行，比本表多几列。

## 方法
- **WG 数**：取自试跑 stdout 的 config 行和 driver 的 `total WG: N WG Per CU k` 行。graph、IVF、BH 这类和数据有关的数（层数、轮数、边数、树节点数）都是 host 端真实生成的。
- **GPM 覆盖**：按静态切分，wgPerCU = ceil(N/1344)，用到的 GPM 数 = min(48, ceil(N/(28·wgPerCU)))。注意 driver 打印的 `Active GPUs` 就是 `len(actualGPUs)`，永远是 48，不能拿来判断覆盖。
- **占用（fill）**：每个 CU 有 4 个 SIMD × 10 个 wave slot = 40 个 wavefront（`cubuilder.go: NewWavefrontPool(10)`）。按 slot 算，每个 CU 能同时放的 WG 数是：WG 为 256 线程时 10 个，128 线程时 20 个，64 线程时 40 个。这是没算 VGPR/LDS 限制的上限。fill 数 = WG per CU ÷ 每 CU 同时能放的 WG 数。
- **墙钟估计**：按 medium 规模的 baseline 墙钟（w2c）线性外推，s/WG 或 s/工作量。各 benchmark 的 s/WG：bfs 0.0157，sssp 0.070，mg 0.045，lud 0.038，shocsort 0.047，cfd 0.165，fft2d 0.080，atax 0.35（k1 的工作量随 NY 增长，所以按 NX·NY 算），bh 0.80（再乘 log n 因子），allreduce 每个 PerCU 1660 s，ivfflat 每千个被扫描向量 7.5 s，npbcg 每千个 SpMV nnz 0.46 s，npbis 按 key 数。
- **校准**：每个 benchmark 跑一次 timing 模式，`-max-wg` 设为总 WG 数的 5%，16 个并行（每台 8 个），超时 40 min，进程只按记录的 PID 停。
  - **这种校准方式本身有偏差**，下面会写清楚。
  - 一种情况是 kernel 的 WG 全部能同时驻留（≤ 1 fill，比如 gups、allreduce 的 RS、bh、npbcg 的 spmv、fft512）。这时最早完成的那 5% WG 已经接近 kernel 末尾，外推 ×20 没有意义，只能得到“≥ 40 min”。
  - 另一种情况是 5% 落在一个很便宜的首个 kernel 里（bfs 第 0 层、cfd_copy），外推会偏低。
  - 只有 lud 和 npbis 的校准比较可信。

## 主要发现（先读这里）
1. **5 个 benchmark 在新尺寸的 timing 模式下崩溃**，都是 LDS 读越界的 panic，和 graphtc 那个是同一类：
   - sssp s19：`[:1044] cap 1040`，2 min 时
   - atax 12096：`[:1060] cap 1056`，8.7 min 时
   - shocsort 6.19M：`[:2116] cap 2112`，12.6 min 时
   - ivfflat Q640：`[:8708] cap 2112`，26.5 min 时
   - graphtc s14：`[:1044] cap 1040`，10 min 时

   报错位置都在 `emu.(*ALUImpl).runDSREADB32/2B32`，越界的上限正好等于各 kernel 自己声明的 `__local` 大小（sssp 和 graphtc 是 `4*65` 个 int，atax 是 `WG+WG/32` 个 float）。sssp 和 atax 的源码按活跃 lane 看是不会越界的，所以更像是模拟器的问题：可能对 inactive lane 也做了地址检查，或者多个 WG 共享一个 CU 时 LDS 基址用错了。这些 benchmark 在 medium 尺寸下跑过没有出事，到了每 CU 有更多 WG 的尺寸就会触发。**修好之前，这 5 个的 baseline 跑不完。** w2c 里那次 npbis 2 次迭代的崩溃（`runDSREADB32`）很可能也是同一个问题。
2. **能达到 ~1 GB 而且不超过 6 h 的**：allreduce（`-ar-per-cu 8`，实测 1068 MB）、embbag（`-eb-rows 75000`，实测 1014 MB，工作量和 medium 一样）、ivfflat（N=2M，少查询，但被 bug 挡住）。其余 benchmark 要么工作量随数据一起涨，要么受 class 或 N 的取值限制，只能缩小 footprint。
3. **5× 门槛**：
   - 按“每 CU ≥ 5 个 WG”算，除了 npbis（count/scatter/rank 每 CU 只有 4 个）和 lud 的后半段以外，主 kernel 都能过。
   - 按“占用 ≥ 5 次 fill”这个更严的读法，在 6 h 内只有 bfs、sssp 能过（9.8 次）。
   - 其余要过这一条，要么工作量涨到 5–10×，超出 6 h；要么要改代码，让每个 WG 干的活更少（见文末提案）。

## 推荐尺寸

| benchmark | 推荐 flags | 类型 | footprint MB | launches / 总 WG | 主 kernel：WG（每 CU，GPM） | 同时驻留/CU → fill 数 | ≥5/CU | ≥5 fill | 估计 h（解析 / 校准） | RSS GB | TLB 风险 | 状态 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| graphtc | `-tc-scale 14` | 自然 | 4.3 | 1 / 53,328 | tc_count 53,328（40，48） | 10 → 4.0 | ✓ | ✗ | 未知 / 崩溃 | 4.9 | 低 | **BLOCKED** LDS panic |
| bfs | `-bfs-scale 19` | 自然 | 114 | 14 / 931,840 | expand ×7：131,072（98，48） | 10 → 9.8 | ✓ | ✓ | 4.1 / 1.2*（偏低） | 4.2 | 高 | ok |
| sssp | `-sssp-scale 19 -sssp-max-rounds 1` | 有界（1 轮 pull） | 130 | 1 / 131,072 | sssp_pull 131,072（98，48） | 10 → 9.8 | ✓ | ✓ | 2.6 / 崩溃 | 4.4 | 高 | **BLOCKED** |
| npbcg | `-cg-class B -cg-inner 1` | 有界（2 次 SpMV） | 113 | 18 / 49,226 | spmv ×2：18,750（14，48） | 10 → 1.4 | ✓ | ✗ | 3.5 / ≥40 min* | 5.9 | 低 | ok（未校准） |
| embbag | `-eb-rows 75000 -eb-batch 1024` | 大数据，工作量同 medium | 1014（实测） | 1 / 13,312 | 13,312（10，48） | 10 → 1.0 | ✓ | ✗ | 0.9（不计 TLB）/ b3072 时 ≥40 min | 8.8 | **高** | ok，会受 TLB 限制 |
| ivfflat | `-ivf-n 2000000 -ivf-nlist 4096 -ivf-nprobe 4 -ivf-queries 480` | 大数据，少查询 | ~1035 | 2 / ~9,020 | scan ~8,540（7，44） | 10 → 0.7 | ✓ | ✗ | 4.0 / Q640 时崩溃 | 9.3 | 中 | **BLOCKED** |
| gups | `-gups-log-table 22 -gups-update-mult 1 -gups-wgs 6720` | 自然（加 WG） | 47 | 1 / 6,720 | 6,720（5，48） | 10 → 0.5 | ✓ | ✗ | 1.3（不计 TLB）/ ≥40 min* | 5.1 | **高** | ok，会受 TLB 限制 |
| allreduce | `-ar-per-cu 8` | 自然 | 1068（实测） | 2 / 139,776 | RS 10,752（8，48）；AG 129,024（96，48） | 10 → 0.8 / 9.6 | ✓ | RS ✗ | 3.7 / ≥40 min* | 6.6 | 低 | ok |
| w2-shocsort | `-shocsort-n 6193152` | 自然 | 104 | 48 / 293,336 | radix/find/reorder 12,096（9，48） | 20/10 → 0.45/0.9 | ✓ | ✗ | 3.8 / 崩溃 | 4.5 | 中 | **BLOCKED** |
| w2-npbis | `-npbis-n 8388608 -npbis-iterations 1` | 自然（class A） | 170 | 7 / 42,050 | count/scatter 4,096（4，37）；gather 32,768（25，47） | 10 → 0.4 / 2.5 | **✗** | ✗ | 3.4 / 4.9 | 4.0 | 中 | 不满足 5×（要改代码） |
| w2-fft2d | `-fft2d-n 4096` | 自然（N 最大值） | 268 | 12 / 229,376 | fft512 32,768（25，47）；radix 8,192（7，42）；transpose 16,384（13，46） | 40/10/10 → 0.6/0.7/1.3 | ✓ | ✗ | 5.1 / ≥40 min* | 4.6 | 中 | ok 但余量很小；N=2048（1.5 h）不满足 5× |
| w2-atax | `-atax-nx 12096 -atax-ny 12096 -atax-rows-per-chunk 64` | 自然 | 595 | 3 / 21,216 | k1 12,096（9，48）；k2 9,072（7，47） | 10 → 0.9/0.7 | ✓ | ✗ | 4.3 / 崩溃 | 7.1 | 低 | **BLOCKED** |
| lonestar_bh | `-bh-bodies 516096` | 自然 | 32 | 2 / 16,128 | force 8,064（6，48） | 40 → 0.15 | ✓ | ✗ | 4.2 / ≥40 min* | 3.1 | 中 | ok（未校准） |
| altis_cfd | `-cfd-cells 1548288 -cfd-block 128` | 自然 | 204 | 8 / 96,768 | flux ×3 等：12,096（9，48） | 20 → 0.45 | ✓ | ✗ | 4.4 / 2.8（偏低） | 4.4 | 中 | ok |
| rodinia_lud | `-lud-n 1664` | 自然 | 11 | 310 / 380,380 | internal 10,609 递减到 1 | 10 → ≤0.8 | 部分（k≤21，占 50% WG） | ✗ | 4.1 / 4.2 | 5.0 | 低 | ok（LU 尾部本来就窄） |
| npb_mg | `-mg-class W`（不变） | 自然 | 29 | 98 / 127,377 | 顶层 resid/psinv 15,876（12，48） | 13 → 0.9 | 仅顶层 | ✗ | 1.6（实测 5757 s） | 5.1 | 低 | ok；class A 约 13 h，太长 |

\* 表示校准结果没有参考价值：主 kernel 的 WG 全部能同时驻留，或者 5% 落在便宜的首个 kernel 里。只能当下界看，估计以解析值为准。
小 helper kernel（可以接受）：bfs_update 7×2048（占 WG 的 1.5%，37 GPM），cg 向量 kernel 10×1172（42 GPM，属于短的流式 kernel），cg_reduce 6×1，ivf_merge 480，shocsort 每趟的 scan/vectorAdd 189 WG（总共占 1%），npbis 的 modify/bucketscan 各 1 和 colscan 64，atax_k2red 48，lud_diagonal 104×1，mg 的 comm3 和粗层。

## ~1 GB 尺寸（第一轮 emu 数据，供参考）
| benchmark | ~1 GB flags | footprint MB | 总 WG | 估计 baseline |
|---|---|---|---|---|
| allreduce | `-ar-per-cu 8` | 1068（实测） | 139,776 | 3.7 h ✓ |
| embbag | `-eb-rows 75000 -eb-batch 1024` | 1014（实测） | 13,312 | ~1 h 起，主要受 TLB 影响 |
| ivfflat | 如上，N=2M | ~1035 | ~9k | 4 h（崩溃） |
| gups | `-gups-log-table 27 -gups-update-mult 1` | 1076（实测） | 1,344 | ~40 h 以上，而且受 TLB 限制 |
| atax | `-atax-nx 16384 -atax-ny 16384` | 1091 | 32,832 | ~8 h |
| shocsort | `-shocsort-n 58720256` | 984 | 2,781,224 | ~36 h |
| cfd | `-cfd-cells 7500000 -cfd-block 128` | 990 | 468,752 | ~21 h |
| bfs | `-bfs-scale 22` | 933（7 层） | 7,454,720 | ~32 h |
| sssp | `-sssp-scale 22 -sssp-edge-factor 10` | 901（13 轮） | 13,631,488 | ~266 h |
| graphtc | `-tc-scale 22 -tc-edge-factor 13` | 1065 | 13,106,159 | ≫100 h |
| bh | `-bh-bodies 16000000` | ~1005（2762 万个节点） | 500,000 | ~160 h |
| lud | `-lud-n 16384` | 1074 | 358,438,400 | 以月计 |
| npb_mg | `-mg-class C` | 1787（没有接近 1 GB 的 class；A/B 都是 226） | 2,069,127 | ~100 h |
| npbis | `-npbis-n 33554432`（合法上限） | 680（实测；MAX_KEY 限制 N ≤ 2^25） | 164,930 | ~13 h |
| npbcg | `-cg-class C`（最大的 class） | ~354（nnz 36.1M） | 1,223,518 | 100 h 以上 |
| fft2d | `-fft2d-n 4096`（最大值） | 268 | 229,376 | 5.1 h |

## 不改代码就达不到要求的，以及最小改动提案（只是提案，没有改）
- **LDS panic**（graphtc/sssp/atax/shocsort/ivfflat）：先修模拟器的 DS 读越界检查。要么只检查 active lane，要么查清多个 WG 同驻一个 CU 时 LDS 的基址。这是最优先的事。
- **gups**：已有的 `-gups-wgs` 就能做到每 CU ≥ 5 个 WG（6720）。要做到 ≥ 5 次 fill，需要 ≥ 67,200 个 WG，而且每个线程至少 1 次 update，也就是 updates ≥ 17M（L24 m1，约 5 h 以上，不计 TLB）。建议加一个 `-gups-updates`（绝对 update 数），把表大小和工作量分开。
- **npbis**：count/scatter 每个 WG 处理 2048 个 key，class A 只有 4096 个 WG。建议加 `-npbis-keys-per-thread`（8 → 2，WG 数变成 4 倍，16,384 个），或者 `-npbis-max-key`，允许 N = 1344 的整数倍 × 2048。
- **embbag、ivfflat、fft2d、atax、cfd、bh、shocsort、npbcg**：每 CU ≥ 5 个 WG 已经满足，但只有 0.15–1.4 次 fill。要达到 5 次 fill，就得把工作量放大 5–30 倍（超过 6 h），或者让每个 WG 干更少的活。可以在 kernel 里把 WG 缩小或拆分：ivf 的 Chunk 256→64，bh/cfd 用更大的 block，fft512 每个 WG 处理一行的一部分。这些都要改代码。
- **npb_mg**：class A 约 13 h。需要一个限制工作量的 flag，比如只跑顶层的 resid/psinv，或者 `-mg-max-level`。
- **lud**：后段步骤 h² 小于 6720，这是 LU 本身的结构，没有便宜的改法。
- **sssp**：`-sssp-max-rounds 1` 是已有的 flag。pull kernel 每一轮都会读所有入边，所以访存模式有代表性，但只覆盖第 1 轮。自然的替代方案是 s16 跑满所有轮（约 19 MB，单 kernel 16,384 WG，用到 46 个 GPM）。
