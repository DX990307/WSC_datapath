# P16 第 4 步：26 个 workload 的小 footprint 规模（2026-10-21，只做了校准，等用户确认）

二进制：`baseline_29d41c7`（`isca27/cupath-v3` @ 29d41c7，从 `wt-v3` 交叉编译，md5 `733b0d7d…`；baseline 的时序和 8446d36 / d09b18b 相同）。配置 `mi200.json`（md5 `25825b90…`，和 w2c 用的相同），`-magic-memory-copy=true`，timing，统计只开 `-report-gpm-trace`（只记录，不影响时序）。和论文配置的差异：无（`config_deviation = []`）。
试跑目录：yss2 / yss3 的 `~/akita-exp-test/abl3size/runs/<name>/`。一共 33 个运行，每个都用 `timeout 1200` 包起来（≤ 20 分钟），每台机器最多 8 个同时跑，MemAvailable 一直 > 170 GB。PID 记在 `abl3size/pids.txt` 里，现在两台机器上都没有残留进程。
完整表见 `sizes.csv`，每个 workload 一行。

## 判定规则（按 P16 第 4 步）
- 一个 kernel 有 N 个 WG。driver 先算每个 CU 分几个：k = ceil(N/1344)；再按顺序给每个 GPM 发 28k 个，用到的 GPM 数 = ceil(N/(28k))。要满足"48 个 GPM 都有、每个 GPM ≥ 28 个 WG"，需要 **1316k + 28 ≤ N ≤ 1344k**。只要 N ≥ 64,513（k ≥ 49），这个条件一定成立。所以 WG 数是 2 的幂的 kernel，比如 16384（46 个 GPM）、32768（47 个），在 2^16 以下都不满足。
- **48 个 GPM 同时忙的时间占比**（busy48）：对每个 kernel 算 (min_GPM last_wg_end − max_GPM first_wg_start) / kernel 时长，数据来自 `-report-gpm-trace`。这个 CSV 只在正常退出时才写（包括 `-max-wg` 截断退出）。20 分钟内跑不完的规模就量不到，这时用同一结构的小一号输入（proxy），或者 T16 / T17 在同规模下已有的 trace 代替，表里都标了来源。
- **墙钟**：先看负载。这次校准时每台机器 8 到 15 个模拟，load 10–14。拿两个和 w2complete 完全同规模的运行对比（gups L18m8、bh 86016），这次的模拟速度和 w2complete 当时差不多：gups 每秒 0.28 µs 对 0.30 µs，bh 每秒 0.16 µs 对 0.20 µs。所以下表的墙钟直接按这次的速度估，可以看作"平时负载"。跑完的给实测值；没跑完的按心跳（每 30 s 一次的模拟时间和已完成 WG 数）外推，或者用 T16 / w2complete 在同规模下的实测值，误差约 ±20%。
- **数据放置**（`allocatePageWithDistributePolicy`）：每个 buffer 按 4 KB 页连续切成 48 段，每个 GPM 分 floor(页数/48) 页，余数全放最后一个 GPM。**不到 48 页（192 KB）的 buffer 整个放在 GPM 0**。下表里各 workload 的主数据都 ≥ 192 KB，都分到了 48 个 GPM。例外是一些小的只读 buffer，它们集中在 GPM 0：MM 的 B 矩阵（64 KB）、ivfflat 的 queries（168 KB）、atax 的 x、shocsort 的 counters。另外，页数少的 buffer 余数会堆到最后一个 GPM：bh 的 body 数组 84 页里有 37 页在最后一个 GPM；gups 的 2 MB 表，最后一个 GPM 有 42 页，其余每个 10 页。

## 建议规模（满足要求，或者基本满足）

主 kernel 一栏格式：WG 数 / 每 CU 几个 / 用到的 GPM 数 / 每 GPM 最少几个 WG。

| workload | flags | 主 kernel（WG / 每 CU / GPM / 每 GPM 最少） | 满足 48 GPM 的时间占比 | footprint MB | 预计 baseline 墙钟 | busy48（来源） | 备注 |
|---|---|---|---|---|---|---|---|
| allreduce | `-ar-per-cu 1` | RS 1344/1/48/28；AG 16128/12/48/336 | 100% | 132 | **20 min（实测）** | RS 0.59，AG 0.65（本规模） | |
| altis_cfd | `-cfd-cells 172032 -cfd-block 128` | 8 个 kernel，各 1344/1/48/28 | 100% | 22.7 | 25–27 min | 0.67（前 4 个 kernel，`-max-wg 5376`） | w2complete 那组（194k）只用到 28 个 GPM |
| w2-atax | `-atax-nx 2688 -atax-ny 8192 -atax-rows-per-chunk 64` | k1 2688/2/48/56；k2 1344/1/48/28 | 约 88%（估） | 88 | 约 37 min（估） | 0.21（1344×8192 r32 的 proxy） | **没有按这个规模跑过**，是按 proxy 的 2 倍估的。k2red 只有 ceil(NY/256) = 32 个 WG（2 个 GPM），估计占 12%（proxy 里占 21%） |
| embbag | `-eb-batch 512` | 6656/5/48/76 | 100% | 56 | 28–32 min | 0.84（batch 206 的 proxy） | |
| gups | `-gups-log-table 18 -gups-update-mult 8` | 1344/1/48/28 | 100% | 2.1 | 38–40 min | 0.36（update-mult 2 的 proxy） | 和 w2complete 同规模 |
| ivfflat | `-ivf-n 8192 -ivf-queries 328` | scan 2675/2/48/43 | 约 92% | 约 5 | 27–30 min | scan 0.47（N=4096、Q=168 的 proxy） | merge 只有 328 个 WG（12 个 GPM），约占 8% |
| w2-shocsort | `-shocsort-n 688128` | 24 个 kernel，各 1344/1/48/28 | 87% | 11.5 | **18 min（实测）** | 0.47（本规模） | scan 类小 kernel 在 1 个 GPM 上，占 13% |
| lonestar_bh | `-bh-bodies 86016` | force 和 integrate 各 1344/1/48/28 | 100% | 约 6 | 约 45 min | 没量到 | 和 w2complete 同规模。WG 数是 bodies/64，要保持 1344 就没有更小的输入可当 proxy |
| bfs | `-bfs-scale 18 -bfs-edge-factor 4` | expand 7 × 65536/49/48/1052 | expand 预计 > 90% | 32 | **45–65 min（临界）** | 没量到（截断运行超时） | 7 层，第 3 层最重。s16、s17 的 expand 只用到 46、47 个 GPM。如果太长，可以换 `-bfs-edge-factor 2` |
| floydwarshall | `floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4` | 4 个 pass，各 65536/49/48/1052 | 100% | 34 | 38–40 min | 没量到 | 每个 pass 都是完整的 kernel |
| im2col | `im2col-t16small -t16-wg-per-cu 64` | 85700/64/48/1476 | 100% | 21 | 28–35 min | 0.18（g16 proxy）；T16 g1024 是 0.20 | H=W=416 |
| pagerank | `pagerank-t16small -t16-wg-per-cu 24` | 32256/24/48/672 | 100% | 约 35 | 25–40 min | g8 0.04；T16 g48 0.70 | busy48 随规模变化很大，g24 本身没量到 |
| matrixtranspose | `matrixtranspose-t16small`（g = 4） | 5329/4/48/65 | 100% | 175 | 33–35 min | 0.55（T16 同规模） | |
| spmv | `spmv-t16small -t16-wg-per-cu 32` | 43008/32/48/896 | 100% | 约 70 | 20–33 min | g8 0.90；T16 g256 0.95 | |
| kmeans | `kmeans-t16small -t16-wg-per-cu 1` | 3 个 kernel，各 1344/1/48/28 | 100% | 22 | 约 35 min | 0.67（T16 g4） | g2 约 70 min，太长 |
| matrixmultiplication | `matrixmultiplication-t16small`（g = 12） | 16128/12/48/336 | 100% | 132 | 约 45 min | 0.35（T16 同规模）；g4 proxy 0.09 | |
| pagedattn | `-pa-batch=17 -pa-seed=5` | partial 2688/2/48/56 | 约 95% | 178 | **50–95 min（不确定）** | 0.17（T17 b66） | 见下面"要用户决定"第 3 条 |

## 达不到要求的（先报告，没有改 benchmark，等用户决定）

| workload | 原因 | 最接近的选择 |
|---|---|---|
| **sssp** | LDS 崩溃：s18 e4、1 轮的规模跑到 95 s 就 panic（`runDSREAD [:1044] cap 1040`），和已知的问题一样。另外，这个规模下随机选到的源点第 1 轮只能到达 3 个点（pull kernel 照样会读所有入边） | 按解析估计：`-sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1`，65536/49/48/1052，约 25 min。s17 只用到 47 个 GPM。等 LDS 修好再校准 |
| **graphtc** | LDS 崩溃：s12 e16 在 815 s 时 panic，和 s13 是同一个错误 | 解析：`-tc-scale 12 -tc-edge-factor 16`，12052/9/48/208，footprint 1.0 MB；墙钟未知 |
| **w2-npbis** | N 必须是 2 的幂（MAX_KEY = N/16 也得是 2 的幂），所以每个 kernel 的 WG 数都是 2 的幂；要 48 个 GPM 得 N/2048 ≥ 2^17。现在 1M 规模 count/scatter 是 512 个 WG，只用到 19 个 GPM；gather 用到 37 个 | 要改 benchmark（比如改每线程 key 数）。2 次迭代的 LDS 崩溃也还没解决 |
| **w2-fft2d** | WG 数都是 2 的幂。N=1024 只用到 37 个 GPM；N=2048 是 42/37/37；最大的 N=4096 是 47/42/46，而且要约 5 h | 要改 benchmark |
| **npbcg** | NAS 的 class 把规模定死了。spmv 在 S/W/A 下是 350/1750/3500 个 WG（13/32/42 个 GPM）；只有 class B（18750 个 WG）能满足，但即使 `-cg-inner 1` 也要 ≥ 3.5 h | 改 benchmark，或者放宽规则 |
| **npb_mg** | class W 的顶层有 15876 个 WG，能用到 48 个 GPM，但 1 个 V-cycle 就要 96 min（w2complete 实测 5757 s）。class S 的顶层只用到 33 个 GPM | 放宽"≤ 1 h"，或者加一个"只跑顶层"之类的 flag（要改 benchmark） |
| **rodinia_lud** | internal kernel 的 WG 数每一步都在缩小（n=1024 时是 63² 一直到 1）。n=1024 没有一步能满足；n=2048 只有 6 步满足，占 WG 的 10% | LU 本身就是这个结构 |
| **MoE** | 扫了 tokens 96–1344 × hidden {256, 512, 1024} × ffn {192…704}：没有任何组合能让 up 和 down 都用到 48 个 GPM。up 能满足的最便宜组合是 T=1024、H=256、F=384，约 1.8 h，而且 down 只有 32 个 GPM。现在的 T17 规模（T=256）：up 28 个、down 41 个、gate 10 个 GPM | 要改 benchmark（比如 TileN 或者 tile 的切法），或者放宽规则 |
| **R-GAT** | 已知问题：85% 的时间花在只用到 ≤ 13 个 GPM 的 kernel 上 | 方案 B 是改 benchmark，要用户同意 |

## 要用户决定的
1. **9 个 workload 达不到要求**（上面那张表）。按 P16 的要求，我没有去掉，也没有改任何 benchmark。
2. **busy48 普遍偏低。** 量到的主 kernel 里，只有 spmv（0.90–0.95）和 embbag（0.84）比较高，大多数在 0.2–0.7，MM g4 和 pagerank g8 只有 0.04–0.09。原因是各个 GPM 开始和结束的时间不一致（T16 看到的位置不平衡），而且在小规模下更明显。P16 要求报告这个比例，但没有给门槛，要不要设门槛请用户定。
3. **PagedAttention 的规模。** 默认 seed 1、part 64 时，batch 14–22 都凑不出满足条件的 WG 数，所以改用 `-pa-seed=5`（只换了抽样）。墙钟估计在 50–95 min 之间，不确定。候选：
   - (a) 维持 b17 / seed 5，先跑一次长一点的校准；
   - (b) `-pa-batch=14 -pa-seed=7 -pa-part-tokens=32`（15,729 个 token，约 38–70 min）；
   - (c) `-pa-batch=6 -pa-part-tokens=16`（seed 1，7,922 个 token）。

   不管选哪个，reduce kernel 都只用到 18–21 个 GPM（b17 是 544 个 WG、20 个 GPM），占时间约 4%。
4. **bfs 的墙钟在 1 h 上下**（45–65 min）。可以换 `-bfs-edge-factor 2`，但没有校准。
5. **atax 选的规模没有直接跑过**，k2red 的占比（约 12%）是估的。
6. **还没量到 busy48 的**：bh、bfs、floydwarshall、pagerank g24。正式运行开 `-report-gpm-trace` 就能拿到。

## proxy 和截断运行（只用来量 busy48，不是建议规模）
`px-atax-1344x8192-r32`（1111 s，完整跑完）、`px-cfd-maxwg5376`（814 s）、`px-embbag-b206`（596 s）、`px-gups-L18m2`（677 s）、`px-ivf-n4k-l64-q168`（885 s）、`px-i2c-g16`（336 s）、`px-spmv-g8`（285 s）、`px-pr-g8`（836 s）、`px-mm-g4`（1092 s），都是 exit 0。`px-bfs-s18e4-maxwg199680` 和 `px-fw-n2048-maxwg131072` 超时了，没有拿到 trace。
用 `-max-wg` 截断的运行只是为了让 trace 能写出来，没有拿它们的时间做任何结论，所以没有按 T15 去核对指令数。建议规模全部是**不截断的完整 kernel**。
