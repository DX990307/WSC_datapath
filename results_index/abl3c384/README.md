# abl3c384：修好两个 timing bug 以后，在 384 GB/s 上跑 v3 消融

Claude Code，2026-10-21。

- 代码：worktree `wt-v3`，分支 `isca27/cupath-v3`，commit `4235991`（父 `5952d19`），**没有推送**。
- 二进制：`baseline_4235991`，用 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 编译，md5 `058680dfb58ca2166e62c365fe7328e2`。所有运行（包括 baseline）都用这一个二进制。
- 服务器目录：yss2 / yss3 的 `~/akita-exp-test/v3c384/`。
  - `src/`：`wt-v3` 去掉 `.git` 和 `results` 后的副本；
  - `src/results/<exp_id>/`：每个运行的结果；
  - `queue.tsv`、`launched.tsv`、`done.tsv`、`pids.txt`、`sched.log`：调度器的队列、启动记录、完成记录、PID 和日志；
  - `verify/`：第 1 步的验证运行；
  - `obs/`：`obs_collect.py` 的输出。

本目录的文件：
- `abl3c384.csv`：每个运行一行，包括时间、加速比、WG 和指令核对、M1 / M2 / M3 的统计，以及 48 个 GPM 同时忙的时间比例（busy48）；
- `mechanism_stats.csv`：每个 workload 一行，Complete 的机制统计，加上 baseline 和 Complete 的 obs 统计；
- `obs_raw/`：baseline 和 Complete 的 obs CSV（延迟分段、占用、链路、共享、请求方聚合、复用）；
- `mechanism_analysis.md`：每个部件为什么有用或没用，以及改进机制的提案；
- `verify.csv`：第 1 步的验证运行。

## 1. 第 1 步：两个 bug 的修复（`4235991`）

### Bug A：barrier 缓冲区满了以后，PC 被推进两次
- 位置：`mgpusim/timing/cu/scheduler.go`。
- 原来的行为：
  1. 每个 CU 的 barrier 缓冲区有 16 个位置。缓冲区满的时候，wave 的状态已经被改成 `WfAtBarrier`，但它还留在 `internalExecuting` 里；
  2. 等它所在的 WG 通过 barrier，`passBarrier` 把它的 PC 推进一次；
  3. 下一个周期它又被当成 s_barrier 求值一次，重新进了 barrier；
  4. 到下一个 barrier 放行时，它的 PC 又被推进一次，于是跳过了 s_barrier 后面的那条指令。
- 修法：
  - 进不了缓冲区的 wave 仍然保持 `WfAtBarrier`，这样它的 WG 照样能通过 barrier，不会死锁；它留在 `internalExecuting` 里每个周期重试进缓冲区；
  - `passBarrier` 记下本周期放行的 WG（`barrierReleased`），`EvaluateInternalInst` 把这些 WG 中还挂在 s_barrier 上的 wave 移出 `internalExecuting`，不再求值。这样每个 wave 的 PC 只推进一次。
  - 没有加回退开关：修复前的结果用旧二进制就能复现。
- 单元测试：`scheduler_test.go` 的 "when the barrier buffer is full"，3 个测试：
  - 放行发生在后一个周期；
  - 放行发生在同一个周期里，而且这个 wave 排在放行的 wave 前面；
  - 一直没放行时保持等待，缓冲区有空位后能进去。
  
  在旧代码上，前两个测试失败（复现了 PC 推进两次），第三个通过。

### Bug B：kernel 启动时不失效 L1
- 原来 timing 模式下 kernel 启动时不失效 L1，后一个 kernel 会读到前一个 kernel 留在 L1 里的旧数据。
- 修法：
  - 每个 GPM 的 CP 在 `KernelLaunchHooks` 里（和 M3 kernel 边界失效用的是同一个机制，`installKernelLaunchL1Invalidation`），在第一个 WG 发出之前，失效本 GPM 所有的 L1V、L1S、L1I。
  - 新加的 `writearound.Cache.InvalidateAll`、`writethrough.Cache.InvalidateAll` 只失效空闲的 line，正在填充或正在被读的 line 不动（kernel 边界上不会有这种 line）。
  - 失效不花模拟时间，之后的缺失照常付填充代价。
  - 默认开启。`-legacy-no-kernel-l1-invalidate` 恢复旧行为，只用来复现旧结果。
- 测试：
  - `invalidate_test.go`（writearound、writethrough 各一个）：只失效空闲的 line；
  - `runner/kernel_l1_invalidate_test.go`：默认装上这个 hook，加了 legacy 开关就不装；
  - 集成测试：下面的 `-verify` 运行。

### 组合模式的请求方表改成 128 项（用户 10-21 的决定）
- 新参数 `-t18-compose-table-entries`：
  - 设成 N 时，组合路径（M1 + 打包）的 line 表、也就是 M1 的在途表，正好是 N 项；
  - 等待池默认 4N（和单独 M1 一样；设了 `-t18-req-waiters` 就用它）；
  - 同时打开的批数上限 = min(max(64, W), N)。N = 128 时仍然是 64。
- 默认 0，保持原来的 max(2W, M2 的 line 数) = 1024，所以 `complete_new` 这类旧配置不变。
- 表满时，新的 line 停在 RDMA 入口等待（bypass 开关保持关闭）。计入 `t18_req_table_full_stalls`（不重复计数的请求数）和原来的 `remote_line_entry_full_stalls`（每次重试计一次）。
- 所有 `v3_*.json`（8 个）都设成 128，包括没有合并的 `v3_complete_nomerge`。这样 M1 内部的消融用的都是同样大的表。
- **因此 `v3_complete` 不再等于 W = 64 的 `complete_new`**（后者是 1024 项）。
- 单元测试 `TestM1M2ComposeTableEntriesBoundsLineTable`：
  - 128 项时，600 条不同的 line 进来，最多同时持有 128 条，cuckoo 索引的容量是 128，有表满停顿，多出的请求留在 L1 侧，等待池 512，批数上限 64；
  - 默认设置下容量是 1024。

### 验证运行（timing 模式，`-magic-memory-copy`，mi200.json，768 GB/s 默认网络；详见 `verify.csv`）

| 运行 | 修复前（`timing_verify_sweep.csv` / `lds_crash_diagnosis.csv`） | 修复后 |
|---|---|---|
| w2-shocsort 64k `-verify` | mismatches 65473/65536 | exit 0，mismatches=0 |
| npb_mg（默认）`-verify` | mismatches 70240/78608 | exit 0，mismatches=0，final_l2 和参考的相对误差 6.6e-08 |
| rodinia_lud（n=256）`-verify` | residual 2.9e-02 | exit 0，residual 1.256e-07 |
| w2-fft2d（n=512）`-verify` | roundtrip err 56.2 | exit 0，roundtrip err 5.96e-07 |
| w2-npbis 2 次迭代 `-verify` | LDS 越界 panic | exit 0，key/rank mismatches=0 |
| graphtc（默认）`-verify` | LDS 越界 panic | exit 0，triangles 和 CPU 一致（1437651） |
| w2-atax（默认）、sssp s12 r4 `-verify` | 通过 | exit 0，mismatches=0 |
| sssp s19 r1 `-max-wg=6554` | LDS 越界 panic | exit 0（13.5 min） |
| w2-atax 12096² `-max-wg=1061` | LDS 越界 panic | exit 0（1 h 08 min） |
| ivfflat 2M / 4096 / 4 / 480 `-max-wg=350` | LDS 越界 panic | exit 0（1 h 42 min） |
| graphtc s14 `-max-wg=2666` | LDS 越界 panic | exit 0（43 min） |
| w2-shocsort 6,193,152 `-max-wg=14667` | LDS 越界 panic | exit 0（14 min） |
| fir `-max-wg 4096` | 26.568 µs，md5 `d56a6814` | **26.568 µs，md5 `d56a6814`，逐位一致** |
| MM `-max-wg 4096` | 345.433 µs，md5 `22a58b27` | **345.433 µs，md5 `22a58b27`，逐位一致** |

fir 和 MM 没有变化：fir 只有一个 kernel，MM 在这个规模下 barrier 缓冲区没满过，所以两个修复都碰不到它们。别的 workload 的时间会变。按 `lds_crash_diagnosis.csv` 和 SYNC 10-21 的记录，修复前碰到这两个 bug 的结果都不可信。

## 2. 第 2 步：384 GB/s 战役

- 每个运行的命令行都带 `-bandwidth=24 -switch-buffer-flits=16`（每条链路单向 384 GB/s）。保留 `-magic-memory-copy`，没有 `-verify`。
- 统计：
  - baseline 和 Complete：`-report-cupath-v3-stats -report-obs-all -report-m1-sharing -report-gpm-trace`；
  - 其余配置：`-report-cupath-v3-stats`；
  - 都没有 `-report-l2-source`。
- 命名：`1021_abl_v3c-<item>_mi200[+<cfg>]_4235991`。
- 调度器沿用 v3net 的 `sched.py`：
  - 启动后 MemAvailable 要 ≥ 30 GB；低于 15 GB 时按记录的 PID 暂停，高于 40 GB 时恢复；从不因为内存杀进程。这次一直没有触发暂停。
  - 顺序：先 baseline + Complete 成对，然后 M1 / M1+M2 / M1+M3，然后 M3，最后 4 个 M1 内部消融。
  - 只用记录下的 PID，没有按名字 pkill。
  - 中途把 yss3 上 5 个还没启动的运行移到了 yss2 的队列（yss3 的 `moved_to_yss2.tsv`）。
- **结果：250 个运行全部 exit 0**，每一对的 WG 数和指令数都和 baseline 一致。

### 配置确认（来自运行日志的 `[Config]` 行和 `config_used.json`）

| 配置 | 文件 | 关键开关 | 组合表 |
|---|---|---|---|
| baseline | `mi200.json` | 不开远程数据路径，不开 T18 | – |
| Complete | `v3_complete.json` | 合并（请求方 + owner，dedup owner m1）、打包 + 预取、rtt、多播、M3 + kernel 边界失效 | entries=128，waiters=512 |
| M1 | `v3_m1.json` | 同上，但多播关，M3（L2 和失效）关 | 128 / 512 |
| M1+M2 | `v3_m1m2.json` | 多播开，M3 关 | 128 / 512 |
| M1+M3 | `v3_m1m3.json` | 多播关，M3 开 | 128 / 512 |
| M3 | `m3.json` | 远程数据路径开，但 batching、预取、dedup 关，L2 + kernel 失效开，T18 关（`t18-window` 0） | 不适用（不是组合路径） |
| −merge | `v3_complete_nomerge.json` | 请求方合并、owner 合并、多播都关，dedup owner none | 128 / 512 |
| −pack | `v3_complete_nopack.json` | batching 和预取都关 | 128 / 512 |
| alloc shared | `v3_complete_alloc_shared.json` | credit-policy shared | 128 / 512 |
| alloc near | `v3_complete_alloc_near.json` | credit-policy near，near-reserve 16，near-hops 3 | 128 / 512 |

所有配置的 `bandwidth`=24、`switch-buffer-flits`=16、`magic-memory-copy`=true、`legacy-no-kernel-l1-invalidate`=false。组合表的项数也在统计里核对过：`t18_req_table_slots` = 128，`t18_req_waiter_capacity` = 512（每个 GPM）。

用户列的是"11 个配置"，实际点名的是 10 个（上表），这次跑的就是这 10 个。

### workload 和规模

| item | 参数 | 来源 |
|---|---|---|
| ar-pc1 | `-benchmark allreduce -ar-per-cu 1` | sizes.csv |
| atax-2688x8192-r64 | `-benchmark w2-atax -atax-nx 2688 -atax-ny 8192 -atax-rows-per-chunk 64` | sizes.csv |
| bfs-s18e2 | `-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2` | sizes.md 的备选（edge factor 2，因为 e4 可能超过 1 h） |
| bh-n86k | `-benchmark lonestar_bh -bh-bodies 86016` | sizes.csv |
| cfd-c172k-b128 | `-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128` | sizes.csv |
| embbag-b512 | `-benchmark embbag -eb-batch 512` | sizes.csv |
| fft2d-n1024 | `-benchmark w2-fft2d -fft2d-n 1024` | 首波 |
| fw-n2048-p4 | `-benchmark floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4` | sizes.csv |
| gups-L18m8 | `-benchmark gups -gups-log-table 18 -gups-update-mult 8` | sizes.csv |
| i2c-g64 | `-benchmark im2col-t16small -t16-wg-per-cu 64` | sizes.csv |
| ivf-n8k-q328 | `-benchmark ivfflat -ivf-n 8192 -ivf-queries 328` | sizes.csv |
| km-g1 | `-benchmark kmeans-t16small -t16-wg-per-cu 1` | sizes.csv |
| lud-n1024 | `-benchmark rodinia_lud -lud-n 1024` | 首波 |
| mg-W | `-benchmark npb_mg -mg-class W` | 首波（W2 class W） |
| mm-g12 | `-benchmark matrixmultiplication-t16small` | sizes.csv |
| moe-t64 | `-benchmark moe -moe-tokens=64 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75` | 首波（T17 参数，64 tokens） |
| mt-g4 | `-benchmark matrixtranspose-t16small` | sizes.csv |
| npbcg-W | `-benchmark npbcg -cg-class W` | 首波（class W） |
| npbis-n1m-i1 | `-benchmark w2-npbis -npbis-n 1048576 -npbis-iterations 1` | 首波（2^20，1 次迭代） |
| pa-b14s7p32 | `-benchmark pagedattn -pa-batch=14 -pa-seed=7 -pa-part-tokens=32` | sizes.md 的候选 (b) |
| pr-g24 | `-benchmark pagerank-t16small -t16-wg-per-cu 24` | sizes.csv |
| shocsort-n688k | `-benchmark w2-shocsort -shocsort-n 688128` | sizes.csv |
| spmv-g32 | `-benchmark spmv-t16small -t16-wg-per-cu 32` | sizes.csv |
| sssp-s18e4-r1 | `-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1` | sizes.csv 解析规模（这次第一次跑通） |
| tc-s12e16 | `-benchmark graphtc -tc-scale 12 -tc-edge-factor 16` | sizes.csv 解析规模（这次第一次跑通） |

不跑的：R-GAT（方案 B 要用户决定）。另外，本地型 7 个也没有跑（这次的任务里没有要求）。

墙钟（`/usr/bin/time` 量的整个包装脚本）：
- 单个运行最长 2.83 h（MoE baseline），其次 npbcg baseline 1.60 h、MG baseline 1.49 h；其余都 ≤ 1.1 h。
- sssp 和 graphtc 按 sizes.csv 的解析规模，0.5–0.9 h 就跑完了，不需要缩小。
- 249 个跑完的运行墙钟加起来 161 h。两台机器同时跑 30–40 个，从 04:11 到 09:29 UTC，约 5.3 h。

## 3. 一个挂住的运行：MG class W 的 M1+M2（原来就有的 L2TLB 丢唤醒 bug）

- **现象**：`1021_abl_v3c-mg-W_mi200+v3m1m2_4235991`（yss2，sim PID 2415098，wrapper 2415090，都记在 `pids.txt`）。
  - 模拟时间从 05:04 UTC 起就停在 542.614 µs，进程 CPU 0%；
  - 同一个 MG 的另外 9 个配置都在 1.3–1.5 h 内正常结束。
  - 按协调方的要求，**没有杀、没有修**，留给用户决定。可以用 monitor 端口 44185 查看（`/api/hangdetector/buffers`、`/api/component/...`）。
- **定位**（来自 monitor）：
  - 全系统唯一不空的缓冲区是 `GPU[37].L2TLB.TopPort.Buf`，里面有 35 条翻译请求；
  - 这个 L2TLB 的 MSHR 是空的，`respondingMSHREntry` 也是空的，`isPaused=false`，但它已经不再 tick；
  - 它的计数：`downstreamReqCount=265`、`iommuReqCount=250`，说明这个 GPM 的大部分翻译缺失都发往 IOMMU；
  - 手动 `/api/tick` 以后时间仍然不动（引擎已经没有事件）。
- **可能的原因**（读代码得出，没有用补丁验证）：
  - `akita/mem/vm/l2tlb/l2tlb.go` 的 `sendDownstream` / `sendToIOMMU`，直接调用 `tlb.IOMMUPort.Send(...)`；而 `IOMMUPort` 是 builder 传进来的 `IOMMUCache` 的 "Top" 端口（`r9nanobuilder.go:937`），不属于 L2TLB。
  - 这个 Send 失败（连接忙）时，L2TLB 本周期没有进展，就停止 tick；
  - 连接腾出空位时，`NotifyAvailable` 通知的是端口的主人 IOMMUCache，不是 L2TLB。L2TLB 的 TopPort 也不会再收到新消息去唤醒它（发请求的 L1 TLB 都在等它），于是永远停住。
- **复现条件**：
  - 需要一个 GPM 的 L2TLB 往 IOMMU 发的翻译请求恰好在连接忙的那一拍失败，而且之后 TopPort 上没有新消息到达。
  - 模拟是确定性的，所以同一个命令（`isca27_run.sh ... v3_m1m2 -benchmark npb_mg -mg-class W -bandwidth=24 -switch-buffer-flits=16 -report-cupath-v3-stats`）重跑会在同一处挂住，约 1 h 后挂住。
  - 别的配置时序不同，没有碰到。这个 bug 和本次的 3 个改动无关：barrier 只在 CU 内部，L1 失效只在 kernel 启动时做，组合表只在 RDMA 里。
- **可能的修法**：
  - (a) 给 L2TLB 一个自己的 `ToIOMMU` 端口，像 bottom 端口一样连到 IOMMUCache 的 Top 端口，这样 NotifyAvailable 会回到 L2TLB；
  - (b) 只要 TopPort 不空，L2TLB 就在 Tick 里返回 true，保持 tick。
- **修了以后对时序的影响**：
  - (a) 只在"发往 IOMMU 失败"的那一拍以后改变 L2TLB 的唤醒时间，没碰到这种情况的运行逐位不变。所以本次 249 个完成的运行，以及 fir、MM 回归，预期都不受影响。不过要用回归确认：Send 的源端口从 IOMMUCache.Top 换成 L2TLB 自己的端口，可能改变连接仲裁的顺序。
  - (b) 会让 L2TLB 在等待时一直 tick，模拟变慢，但时序结果不变。

  修好以后要补跑 MG M1+M2 这一格。
- **在表里的处理**：
  - `abl3c384.csv` 里这一行 `rc = "hang (...)"`；
  - T2 里 MG 的 M1+M2 一格标 "hang"，T2 的 GMEAN 只算 5 个配置都有的 24 个 workload；
  - T3 里 MG 的 −M3 一格标 "hang"，那一列的 GMEAN 是 24 个 workload；
  - T5 里 MG 的 M3 一列是 "–"。

## 4. 结果

下面的表是 `analyze.py` 从 `abl3c384.csv` 生成的。加速比 = T_baseline / T_配置。T3 和 T4 用的是 T_变体 / T_Complete，大于 1 说明去掉（或者换掉）的那个部件有用。所有对比的 WG 数和指令数都和 baseline 一致（"inst/WG match" 一列）。

busy48 是"48 个 GPM 同时有 WG 在跑"的时间占 kernel 总时间的比例。fft2d、LUD、MG、MoE、npbcg、npbis 是 0，因为它们的每个 kernel 都用不满 48 个 GPM（见 `../abl3/sizes.md`）。

### T1 Complete vs baseline

| workload | baseline µs | Complete µs | speedup | inst/WG match | busy48 base | busy48 Complete |
|---|---|---|---|---|---|---|
| ar-pc1 | 476.2 | 256.4 | 1.86 | True | 0.60 | 0.61 |
| atax-2688x8192-r64 | 828.9 | 306.0 | 2.71 | True | 0.26 | 0.57 |
| bfs-s18e2 | 936.9 | 839.3 | 1.12 | True | 0.18 | 0.16 |
| bh-n86k | 435.3 | 367.9 | 1.18 | True | 0.59 | 0.49 |
| cfd-c172k-b128 | 504.4 | 268.5 | 1.88 | True | 0.67 | 0.66 |
| embbag-b512 | 496.4 | 490.3 | 1.01 | True | 0.53 | 0.53 |
| fft2d-n1024 | 1129.4 | 843.3 | 1.34 | True | 0.00 | 0.00 |
| fw-n2048-p4 | 549.2 | 293.1 | 1.87 | True | 0.22 | 0.14 |
| gups-L18m8 | 692.8 | 679.8 | 1.02 | True | 0.58 | 0.58 |
| i2c-g64 | 193.8 | 160.6 | 1.21 | True | 0.18 | 0.22 |
| ivf-n8k-q328 | 470.7 | 271.2 | 1.74 | True | 0.59 | 0.54 |
| km-g1 | 524.9 | 405.0 | 1.30 | True | 0.67 | 0.73 |
| lud-n1024 | 4144.2 | 3337.1 | 1.24 | True | 0.00 | 0.00 |
| mg-W | 2225.5 | 1311.0 | 1.70 | True | 0.00 | 0.00 |
| mm-g12 | 412.6 | 154.8 | 2.67 | True | 0.35 | 0.93 |
| moe-t64 | 5090.3 | 2391.9 | 2.13 | True | 0.00 | 0.00 |
| mt-g4 | 1836.6 | 1837.4 | 1.00 | True | 0.54 | 0.55 |
| npbcg-W | 5915.9 | 1333.9 | 4.44 | True | 0.00 | 0.00 |
| npbis-n1m-i1 | 1169.8 | 882.4 | 1.33 | True | 0.00 | 0.00 |
| pa-b14s7p32 | 736.7 | 467.7 | 1.58 | True | 0.14 | 0.13 |
| pr-g24 | 1971.2 | 148.4 | 13.28 | True | 0.01 | 0.23 |
| shocsort-n688k | 640.4 | 546.8 | 1.17 | True | 0.46 | 0.39 |
| spmv-g32 | 1175.3 | 1174.3 | 1.00 | True | 0.95 | 0.95 |
| sssp-s18e4-r1 | 669.5 | 419.5 | 1.60 | True | 0.36 | 0.31 |
| tc-s12e16 | 402.2 | 109.0 | 3.69 | True | 0.17 | 0.53 |
| **GMEAN** (25) | | | **1.730** | | | |

### T2 cumulative (speedup vs baseline)

| workload | M1 | M1+M2 | Complete | M1+M3 | M3 |
|---|---|---|---|---|---|
| ar-pc1 | 1.91 | 1.92 | 1.86 | 1.86 | 1.01 |
| atax-2688x8192-r64 | 2.68 | 2.66 | 2.71 | 2.71 | 1.33 |
| bfs-s18e2 | 1.05 | 0.98 | 1.12 | 1.15 | 1.06 |
| bh-n86k | 1.02 | 1.02 | 1.18 | 1.18 | 1.15 |
| cfd-c172k-b128 | 1.74 | 1.73 | 1.88 | 1.89 | 1.09 |
| embbag-b512 | 1.02 | 1.01 | 1.01 | 1.01 | 1.01 |
| fft2d-n1024 | 1.34 | 1.33 | 1.34 | 1.32 | 1.00 |
| fw-n2048-p4 | 1.66 | 1.66 | 1.87 | 1.84 | 1.20 |
| gups-L18m8 | 1.02 | 1.01 | 1.02 | 1.02 | 1.00 |
| i2c-g64 | 1.10 | 1.09 | 1.21 | 1.20 | 1.11 |
| ivf-n8k-q328 | 1.59 | 1.59 | 1.74 | 1.74 | 1.01 |
| km-g1 | 1.30 | 1.30 | 1.30 | 1.30 | 1.00 |
| lud-n1024 | 1.25 | 1.25 | 1.24 | 1.24 | 1.02 |
| mg-W | 1.48 | hang | 1.70 | 1.70 | 1.19 |
| mm-g12 | 2.18 | 2.23 | 2.67 | 2.67 | 1.54 |
| moe-t64 | 2.13 | 2.13 | 2.13 | 2.10 | 1.05 |
| mt-g4 | 1.00 | 1.00 | 1.00 | 1.00 | 1.00 |
| npbcg-W | 2.23 | 2.23 | 4.44 | 4.46 | 2.87 |
| npbis-n1m-i1 | 1.24 | 1.24 | 1.33 | 1.32 | 1.07 |
| pa-b14s7p32 | 1.59 | 1.60 | 1.58 | 1.56 | 1.01 |
| pr-g24 | 1.99 | 1.99 | 13.28 | 13.26 | 8.27 |
| shocsort-n688k | 1.17 | 1.17 | 1.17 | 1.16 | 0.98 |
| spmv-g32 | 1.00 | 1.00 | 1.00 | 1.00 | 1.00 |
| sssp-s18e4-r1 | 1.10 | 0.97 | 1.60 | 1.69 | 1.52 |
| tc-s12e16 | 1.87 | 1.88 | 3.69 | 3.71 | 2.37 |
| **GMEAN** (24 with all 5) | 1.439 | 1.427 | 1.731 | 1.734 | 1.278 |

### T3 leave-one-out: slowdown of Complete when one mechanism is removed (T_without / T_Complete; >1 = the mechanism helps)

| workload | −M1 (= M3 only) | −M2 (= M1+M3) | −M3 (= M1+M2) |
|---|---|---|---|
| ar-pc1 | 1.841 | 0.999 | 0.969 |
| atax-2688x8192-r64 | 2.030 | 1.001 | 1.017 |
| bfs-s18e2 | 1.056 | 0.967 | 1.140 |
| bh-n86k | 1.027 | 1.000 | 1.160 |
| cfd-c172k-b128 | 1.721 | 0.995 | 1.084 |
| embbag-b512 | 1.005 | 0.999 | 1.001 |
| fft2d-n1024 | 1.338 | 1.016 | 1.011 |
| fw-n2048-p4 | 1.560 | 1.016 | 1.127 |
| gups-L18m8 | 1.019 | 1.001 | 1.006 |
| i2c-g64 | 1.090 | 1.006 | 1.104 |
| ivf-n8k-q328 | 1.724 | 0.995 | 1.095 |
| km-g1 | 1.297 | 0.999 | 0.998 |
| lud-n1024 | 1.220 | 0.998 | 0.996 |
| mg-W | 1.430 | 1.001 | hang |
| mm-g12 | 1.731 | 1.000 | 1.196 |
| moe-t64 | 2.035 | 1.012 | 0.997 |
| mt-g4 | 1.000 | 1.000 | 1.000 |
| npbcg-W | 1.544 | 0.995 | 1.993 |
| npbis-n1m-i1 | 1.237 | 1.002 | 1.065 |
| pa-b14s7p32 | 1.566 | 1.010 | 0.985 |
| pr-g24 | 1.606 | 1.002 | 6.664 |
| shocsort-n688k | 1.196 | 1.007 | 1.000 |
| spmv-g32 | 1.000 | 0.999 | 0.998 |
| sssp-s18e4-r1 | 1.052 | 0.947 | 1.641 |
| tc-s12e16 | 1.559 | 0.994 | 1.959 |
| **GMEAN** | 1.358 | 0.998 | 1.213 |

### T4 M1 internal (T_variant / T_Complete; >1 = removing / changing hurts)

| workload | −merge | −pack | alloc shared | alloc near |
|---|---|---|---|---|
| ar-pc1 | 1.132 | 1.826 | 0.990 | 1.005 |
| atax-2688x8192-r64 | 1.383 | 1.493 | 1.005 | 1.009 |
| bfs-s18e2 | 0.981 | 1.025 | 1.005 | 1.005 |
| bh-n86k | 1.009 | 1.022 | 0.998 | 1.004 |
| cfd-c172k-b128 | 1.054 | 1.661 | 1.004 | 1.083 |
| embbag-b512 | 0.998 | 1.005 | 1.003 | 0.997 |
| fft2d-n1024 | 1.147 | 1.214 | 0.997 | 1.181 |
| fw-n2048-p4 | 1.350 | 1.133 | 1.000 | 1.238 |
| gups-L18m8 | 1.002 | 1.016 | 1.001 | 1.177 |
| i2c-g64 | 1.038 | 1.018 | 1.002 | 1.170 |
| ivf-n8k-q328 | 1.197 | 1.637 | 0.999 | 1.012 |
| km-g1 | 1.003 | 1.291 | 1.000 | 1.007 |
| lud-n1024 | 1.090 | 1.116 | 1.001 | 1.075 |
| mg-W | 1.075 | 1.343 | 1.004 | 1.136 |
| mm-g12 | 1.611 | 1.005 | 1.000 | 1.010 |
| moe-t64 | 1.116 | 1.740 | 1.003 | 1.040 |
| mt-g4 | 0.999 | 0.999 | 0.999 | 0.999 |
| npbcg-W | 1.218 | 1.303 | 0.994 | 1.021 |
| npbis-n1m-i1 | 1.034 | 1.202 | 1.001 | 1.179 |
| pa-b14s7p32 | 1.013 | 1.656 | 1.009 | 1.041 |
| pr-g24 | 1.257 | 1.177 | 0.999 | 0.978 |
| shocsort-n688k | 1.178 | 1.000 | 1.000 | 1.140 |
| spmv-g32 | 0.998 | 0.999 | 1.000 | 1.000 |
| sssp-s18e4-r1 | 0.954 | 1.006 | 0.941 | 1.041 |
| tc-s12e16 | 1.166 | 1.295 | 0.996 | 0.934 |
| **GMEAN** | 1.111 | 1.222 | 0.998 | 1.056 |

### T5 largest contributor (largest T_without/T_Complete among merge, pack, alloc(rtt vs shared), M2, M3)

| workload | merge | pack | alloc | M2 | M3 | largest | M1 as a whole (−M1) |
|---|---|---|---|---|---|---|---|
| ar-pc1 | 1.132 | 1.826 | 0.990 | 0.999 | 0.969 | pack | 1.841 |
| atax-2688x8192-r64 | 1.383 | 1.493 | 1.005 | 1.001 | 1.017 | pack | 2.030 |
| bfs-s18e2 | 0.981 | 1.025 | 1.005 | 0.967 | 1.140 | M3 | 1.056 |
| bh-n86k | 1.009 | 1.022 | 0.998 | 1.000 | 1.160 | M3 | 1.027 |
| cfd-c172k-b128 | 1.054 | 1.661 | 1.004 | 0.995 | 1.084 | pack | 1.721 |
| embbag-b512 | 0.998 | 1.005 | 1.003 | 0.999 | 1.001 | none (<2%) | 1.005 |
| fft2d-n1024 | 1.147 | 1.214 | 0.997 | 1.016 | 1.011 | pack | 1.338 |
| fw-n2048-p4 | 1.350 | 1.133 | 1.000 | 1.016 | 1.127 | merge | 1.560 |
| gups-L18m8 | 1.002 | 1.016 | 1.001 | 1.001 | 1.006 | none (<2%) | 1.019 |
| i2c-g64 | 1.038 | 1.018 | 1.002 | 1.006 | 1.104 | M3 | 1.090 |
| ivf-n8k-q328 | 1.197 | 1.637 | 0.999 | 0.995 | 1.095 | pack | 1.724 |
| km-g1 | 1.003 | 1.291 | 1.000 | 0.999 | 0.998 | pack | 1.297 |
| lud-n1024 | 1.090 | 1.116 | 1.001 | 0.998 | 0.996 | pack | 1.220 |
| mg-W | 1.075 | 1.343 | 1.004 | 1.001 | – | pack | 1.430 |
| mm-g12 | 1.611 | 1.005 | 1.000 | 1.000 | 1.196 | merge | 1.731 |
| moe-t64 | 1.116 | 1.740 | 1.003 | 1.012 | 0.997 | pack | 2.035 |
| mt-g4 | 0.999 | 0.999 | 0.999 | 1.000 | 1.000 | none (<2%) | 1.000 |
| npbcg-W | 1.218 | 1.303 | 0.994 | 0.995 | 1.993 | M3 | 1.544 |
| npbis-n1m-i1 | 1.034 | 1.202 | 1.001 | 1.002 | 1.065 | pack | 1.237 |
| pa-b14s7p32 | 1.013 | 1.656 | 1.009 | 1.010 | 0.985 | pack | 1.566 |
| pr-g24 | 1.257 | 1.177 | 0.999 | 1.002 | 6.664 | M3 | 1.606 |
| shocsort-n688k | 1.178 | 1.000 | 1.000 | 1.007 | 1.000 | merge | 1.196 |
| spmv-g32 | 0.998 | 0.999 | 1.000 | 0.999 | 0.998 | none (<2%) | 1.000 |
| sssp-s18e4-r1 | 0.954 | 1.006 | 0.941 | 0.947 | 1.641 | M3 | 1.052 |
| tc-s12e16 | 1.166 | 1.295 | 0.996 | 0.994 | 1.959 | M3 | 1.559 |



### 主要结论（机制解释和改进提案见 `mechanism_analysis.md`）

- **Complete 对 baseline：GMEAN 1.73×**（25 个 workload）。
  - 最大的几个：PR 13.3×、npbcg 4.4×、graphtc 3.7×、atax 2.7×、MM 2.7×、MoE 2.1×；
  - 约 1.0× 的：MT、SPMV、embbag、gups。
  - 大加速主要出现在"远程读集中在一个 owner 上"的 workload（PR、MM、graphtc、npbcg、atax 的热点份额是 0.61–1.00）。原因是放置规则把小 buffer 放在 GPM 0，论文里要说明。
- **累加**（24 个 workload）：
  - M1 1.44×，M1+M2 1.43×，M1+M3 1.73×，Complete 1.73×；只开 M3 1.28×。
- **去掉一个机制**（T_without / T_Complete）：
  - −M1 1.358（M1 最重要）；
  - −M3 1.213；
  - −M2 0.998：384 GB/s 下 M2 没有作用，在 sssp（0.947）和 bfs（0.967）上还是负的。
- **M1 内部**：
  - −pack 1.222，是 M1 里最重要的部件；
  - −merge 1.111；
  - 分配 shared 对 rtt 0.998，一样；near 1.056，平均更慢。
- **每个 workload 贡献最大的部件**：打包 11 个，M3 7 个，合并 3 个（MM、FW、shocsort）；4 个所有部件都 < 2%（embbag、gups、MT、SPMV）。M2 和分配方式在任何 workload 上都不是最大的贡献者。这一点要在论文里如实写出，回应"堆技术"的质疑。
- 只作参考：首波 768 GB/s（修复前的二进制、1024 项组合表）时，17 个 workload 的 Complete GMEAN 是 1.82×。这次的设置（384 GB/s、修复后、128 项）和它不能直接比较。

### 没做的 / 要注意的

- 没有跑 R-GAT。
- MG 的 M1+M2 挂住（第 3 节），这一格没有结果。
- 这次的数字都来自修复后的二进制。修复前的结果（v3 各种网络带宽、W2、可能还有 T15–T18）中，凡是碰到这两个 bug 的都不可信。fir 和 MM 的回归逐位一致，只能说明这两个没受影响。
- 没有动 STATUS、SYNC，没有推送。分析和调度脚本复制在 `scripts/`（`analyze.py`、`mech.py`、`ext.py`、`sched.py`、`collect.py`、`mkqueue.py`、`quick.py`、`vrun.sh`）；`ext.py` 在服务器的 `~/akita-exp-test/v3c384/` 下运行。
