# m2w：W 和网络带宽变化时，M2（网内多播）和 M1 的分配方式什么时候开始有用

Claude Code，2026-10-21。

- 代码：worktree `wt-v3`，分支 `isca27/cupath-v3` @ `4235991`。没有改代码，没有推送。
- 二进制：`baseline_4235991`，md5 `058680dfb58ca2166e62c365fe7328e2`，和 abl3c384 用的是同一个文件（从 `v3c384/bin` 复制过来）。
- 服务器目录：yss2 / yss3 的 `~/akita-exp-test/v3m2w/`。
  - `src/`：`v3c384/src` 去掉 `results` 后的副本（configs 和 `isca27_run.sh` 的 md5 已经和本地 `wt-v3` 核对过）；
  - `src/results/<exp_id>/`：每个运行的结果；
  - `queue.tsv`、`launched.tsv`、`done.tsv`、`pids.txt`、`sched.log`、`hangs.tsv`：调度器的文件；
  - `commands.tsv`：72 个运行的完整命令行；
  - `verify/`：冒烟验证运行。
- 本目录：
  - `m2w.csv`：每个运行一行（包括复用的 W=64 / 384 GB/s 结果）；
  - `analysis.md`：分析；
  - `scripts/`：
    - `mkqueue.py`（`mkqueue.py w2048` 生成追加的那 24 行）、`sched.py`（加了看门狗）；
    - `commands.tsv`：96 条，包括已经取消的 w256b48；
    - `ext.py`：在服务器上跑，逐个运行提取统计；
    - `collect.py`：拉取结果，生成 `m2w.csv` 和 `obs_raw/`；
    - `analyze.py`：生成 markdown 表；
  - `obs_raw/`：baseline、Complete 和 sssp/bfs 的 M1+M3 的 obs CSV，按机器分目录（yss2 / yss3）。

## 1. 实验矩阵

- **workload**（规模和 abl3c384 完全相同）：
  - pr-g24：`-benchmark pagerank-t16small -t16-wg-per-cu 24`
  - npbcg-W：`-benchmark npbcg -cg-class W`
  - sssp-s18e4-r1：`-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1`
  - bfs-s18e2：`-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2`
  - ar-pc1：`-benchmark allreduce -ar-per-cu 1`
  - cfd-c172k-b128：`-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128`
- **配置**：baseline（`mi200`）、M1+M3（`v3_m1m3`）、Complete（`v3_complete`）、alloc shared（`v3_complete_alloc_shared`）。
- **条件**（switch-buffer-flits 都是 16）：

| 条件 | W | 每条链路单向带宽 | 来源 |
|---|---|---|---|
| w64b384 | 64 | 384 GB/s（`-bandwidth=24`） | 复用 abl3c384 的结果（`1021_abl_v3c-*`） |
| w256b384 | 256 | 384 GB/s | 新跑 |
| w64b48 | 64 | 48 GB/s（`-bandwidth=3`） | 新跑 |
| w2048b384 | 2048 | 384 GB/s | 新跑（用户 10-21 中途追加，目的是把 384 GB/s 填满） |
| ~~w256b48~~ | 256 | 48 GB/s | **用户要求取消**（12:03 UTC），结果不进 `m2w.csv`，也不进分析 |

- 新跑的运行一共 6 × 4 × 3 = 72 个（w256b384、w64b48、w2048b384），命名为 `1021_sens_m2w-<条件>-<workload>_<配置>_4235991`。
- w256b48 原来排了 24 个：
  - 取消时有 15 个正在跑，被主会话 SIGTERM（`done.tsv` 里 rc = −15）；
  - 1 个（PR Complete）已经 rc = 0 跑完，留在服务器上，但不进分析；
  - 其余 8 个还在队列里，没有启动，从队列里删掉了。
  - 两台机器上都有 `CANCELLED_w256b48.txt`。

## 2. 控制窗口 W 的参数（读代码确认）

- **baseline**：`-rdma-max-outstanding`（`runner/flag.go`，默认 64；`hpca.json` 里也是 64）。
  - 它是每个 RDMA 请求方方向和 owner 方向各自能同时跟踪的不同操作数（`rdma/comp.go` 的 `canAcceptRequesterOutstanding` / `canAcceptOwnerOutstanding`）。
- **v3（T18 / M1 组合路径）**：`-t18-window`（`runner/t18.go`）。
  - 0 表示沿用 `-rdma-max-outstanding`；v3 的配置文件里设成 64。
  - 组合路径用 `m1CanSend` 和信用分配器（`newT18CreditAllocatorPolicy(c.t18Window)`）控制在途量，不再走 `canAcceptRequesterOutstanding`；
  - owner 窗口 = `-t18-owner-window`，为 0 时等于 `-t18-window`；
  - 同时打开的批数上限 = min(max(`remote-data-path-batches`=64, W), 组合表项数)。
- **W=256 的设置**：
  - baseline 加 `-rdma-max-outstanding=256`；
  - v3 加 `-rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512`。
  - `-rdma-max-outstanding` 在组合路径里只影响统计里的 limit 一栏，但为了和 baseline 一致，也一起设了。
- 随之自动变化的量：
  - 等待池 = 4 × 512 = 2048（规则和 128 项时一样）；
  - 批数上限 = min(max(64, 256), 512) = 256；
  - owner 窗口 256。

  其余不变。
- **W=2048 的设置**：
  - baseline 加 `-rdma-max-outstanding=2048`；
  - v3 加 `-rdma-max-outstanding=2048 -t18-window=2048 -t18-compose-table-entries=4096`；
  - 随之自动变化：等待池 16384，批数上限 2048。
- **注意**：每个 GPM 有 28 个 CU，每个 L1V 有 16 个 MSHR（`l1v-mshr-entries` 16、`l1v-max-concurrent-trans` 16），所以远程读的在途数实际上被封顶在约 448。W=2048 实际上等于"窗口不限"（见第 4 节和 `analysis.md` 第 4 节）。

## 3. 每个条件的完整命令行

所有命令都在 `~/akita-exp-test/v3m2w/src` 下运行，环境变量 `ISCA27_BINDIR=~/akita-exp-test/v3m2w/bin`，外面包一层 `/usr/bin/time -v`。下面以 pr-g24 为例，其它 workload 只是换掉 `-benchmark ...` 部分。72 条命令全文见 `scripts/commands.tsv`。

统计开关：
- `FULL` = `-report-cupath-v3-stats -report-obs-all -report-m1-sharing -report-gpm-trace`，用于 baseline、Complete，以及 sssp 和 bfs 的 M1+M3；
- `CHEAP` = `-report-cupath-v3-stats`，用于其余运行。

sssp/bfs 的 M1+M3 也开 FULL 有两个原因：
- obs latency 分段里有 owner 端合并后的等待（`owner_service_merged`）、返回网络时间（`ret_net`）、多播叶子数和各分段的 p95；
- 打开 gpm trace 可以看每个 kernel 的尾部。

这样就能和 Complete 一一对比，看出 M2 为什么是负的。

**w256b384**
```
akkalat/isca27_run.sh 1021_sens_m2w-w256b384-pr-g24_mi200_4235991 4235991 M2W mi200 -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -bandwidth=24 -switch-buffer-flits=16 FULL
akkalat/isca27_run.sh 1021_sens_m2w-w256b384-pr-g24_mi200+v3complete_4235991 4235991 M2W v3_complete -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512 -bandwidth=24 -switch-buffer-flits=16 FULL
akkalat/isca27_run.sh 1021_sens_m2w-w256b384-pr-g24_mi200+v3m1m3_4235991 4235991 M2W v3_m1m3 -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512 -bandwidth=24 -switch-buffer-flits=16 CHEAP   (sssp/bfs: FULL)
akkalat/isca27_run.sh 1021_sens_m2w-w256b384-pr-g24_mi200+v3c-allocshared_4235991 4235991 M2W v3_complete_alloc_shared -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512 -bandwidth=24 -switch-buffer-flits=16 CHEAP
```
**w64b48**
```
akkalat/isca27_run.sh 1021_sens_m2w-w64b48-pr-g24_mi200_4235991 4235991 M2W mi200 -benchmark pagerank-t16small -t16-wg-per-cu 24 -bandwidth=3 -switch-buffer-flits=16 FULL
akkalat/isca27_run.sh 1021_sens_m2w-w64b48-pr-g24_mi200+v3complete_4235991 4235991 M2W v3_complete -benchmark pagerank-t16small -t16-wg-per-cu 24 -bandwidth=3 -switch-buffer-flits=16 FULL
akkalat/isca27_run.sh 1021_sens_m2w-w64b48-pr-g24_mi200+v3m1m3_4235991 4235991 M2W v3_m1m3 -benchmark pagerank-t16small -t16-wg-per-cu 24 -bandwidth=3 -switch-buffer-flits=16 CHEAP   (sssp/bfs: FULL)
akkalat/isca27_run.sh 1021_sens_m2w-w64b48-pr-g24_mi200+v3c-allocshared_4235991 4235991 M2W v3_complete_alloc_shared -benchmark pagerank-t16small -t16-wg-per-cu 24 -bandwidth=3 -switch-buffer-flits=16 CHEAP
```
**w2048b384**：和 w256b384 一样，只是把 256 / 512 换成 2048 / 4096。

**w256b48**（已取消，下面只作记录）
```
akkalat/isca27_run.sh 1021_sens_m2w-w256b48-pr-g24_mi200_4235991 4235991 M2W mi200 -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -bandwidth=3 -switch-buffer-flits=16 FULL
akkalat/isca27_run.sh 1021_sens_m2w-w256b48-pr-g24_mi200+v3complete_4235991 4235991 M2W v3_complete -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512 -bandwidth=3 -switch-buffer-flits=16 FULL
akkalat/isca27_run.sh 1021_sens_m2w-w256b48-pr-g24_mi200+v3m1m3_4235991 4235991 M2W v3_m1m3 -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512 -bandwidth=3 -switch-buffer-flits=16 CHEAP   (sssp/bfs: FULL)
akkalat/isca27_run.sh 1021_sens_m2w-w256b48-pr-g24_mi200+v3c-allocshared_4235991 4235991 M2W v3_complete_alloc_shared -benchmark pagerank-t16small -t16-wg-per-cu 24 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512 -bandwidth=3 -switch-buffer-flits=16 CHEAP
```
**w64b384**（复用）：abl3c384 的命令，即 `-bandwidth=24 -switch-buffer-flits=16`，配置文件里 W=64、表 128。

## 4. 参数生效的核对

1. **冒烟运行**（`verify/runs/`，bfs `-bfs-scale 12 -bfs-edge-factor 2`，同一个二进制）：

| 运行 | 核对结果 |
|---|---|
| alloc shared，W=256，384 GB/s | 48 个 GPM 的 `t18_window` = 256，`t18_req_table_slots` = 512，`t18_req_waiter_capacity` = 2048，`t18_owner_window` = 256，`t18_v3_credit_policy_shared` = 1；`t18_req_peak_inflight` 最大 115（> 64，说明窗口确实放大了） |
| baseline，W=256，384 GB/s | `rdma_max_outstanding` = 256；`rdma_requester_peak_outstanding` 最大 194；请求方窗口满停顿 0；obs occupancy 的 limit 列 = 256 |
| baseline，W=64，384 GB/s（对照） | `rdma_max_outstanding` = 64；峰值 64；窗口满停顿 11,862 次；limit = 64 |
| Complete，W=256，48 GB/s；M1+M3，W=64，48 GB/s | `[Config]` 行分别是 `window=256 ... entries=512 waiters=2048` 和 `window=64 ... entries=128 waiters=512`；obs 的链路容量 `link=48B/ns` |

2. **W=2048 的冒烟运行**：
   - bfs s12：
     - Complete 和 M1+M3 的 `t18_window` = 2048，`t18_req_table_slots` = 4096，waiter = 16384；
     - 但峰值在途只有 115（baseline 194），规模太小，没有超过 256。
   - bfs s15 e4：
     - Complete 的峰值在途是 448，表满停顿 0，exit 0，峰值 RSS 4.2 GB；
     - baseline 跑到 1 h 的 timeout 被结束（exit 124），这是冒烟运行的超时，不是 hang。
   - 正式运行里：
     - ar baseline 每个 GPM 的峰值都是 448，Complete 最大 448（平均 407），PR Complete 最大 417；
     - 4096 项的表峰值约 480，从来没满过。
     - 证实了在途数被 L1V MSHR 封顶在 448。
   - 内存：W=2048 的峰值 RSS 和 W=64 / 256 相当，全部运行里最大的是 7.0 GB（ar Complete）。所以没有调整并发。
   - W=2048 的 24 行由主会话插到两台机器 `queue.tsv` 的最前面，和本地 `mkqueue.py w2048` 生成的实验名、参数逐行一致。

3. **正式运行**：抽查了 `config_used.json` 的 `all_flags`。
   - w256b48 baseline：bandwidth 3、switch-buffer-flits 16、rdma-max-outstanding 256、t18-window 0；
   - w256b48 Complete：rdma-max-outstanding 256、t18-window 256、compose 512、policy rtt；
   - w64b48 Complete：64 / 64 / 128。

   `stdout.log` 的 `[Config]` 行和上面一致。汇总脚本还会对每个运行检查统计里的 `t18_window`、`t18_req_table_slots` 和 `rdma_max_outstanding`，结果在 `m2w.csv` 的 `win_stat`、`table_stat` 两列。

## 5. 调度和看门狗

- 调度器是 `scripts/sched.py`，沿用 abl3c384 的逻辑：
  - MemAvailable 减去预留后要 ≥ 30 GB 才启动，每个运行预留 1.3 × 估计值（FULL 8 GB，CHEAP 7 GB；上一轮这些 workload 的峰值 RSS 是 4.0–6.8 GB）；
  - MemAvailable 低于 15 GB 时，暂停最后启动的那个运行（SIGSTOP），高于 40 GB 时恢复；从不因为内存杀进程；
  - 启动顺序：先成对启动 baseline 和 Complete，然后是 M1+M3 和 alloc shared。条件的顺序是 w256b48、w64b48、w256b384。
- **看门狗**（新加的）：
  - 每 5 分钟读一次每个 sim 的 monitor `/api/now`，以及 `/proc/<pid>/stat` 的 CPU 时间；
  - 模拟时间 ≥ 60 分钟没有前进，并且上一个检查周期里 CPU 低于 2% 时，记为 hang，写进 `hangs.tsv`，只对这个 sim 发 SIGKILL。它的 wrapper 会写 `meta.json`（exit code 不是 0）后退出；180 s 后 wrapper 还在，就杀掉 wrapper 的进程组；
  - 被暂停的 sim 不参与判断；
  - 模拟时间不动但 CPU 在忙的运行，只记 `STUCK-BUT-BUSY`，不杀。
- yss2 上的旧进程 2415098（v3c384 的 MG M1+M2）不归这个调度器管，没有碰。任务说明里写的是 yss3，实际在 yss2。
- 只统计 `meta.json` 里 exit_code = 0 的运行的 `summary.csv` 和 stats。

## 6. 运行情况

- **72 个运行全部 exit 0**，没有 hang，看门狗一次也没有触发（`hangs.tsv` 为空）；也没有触发过内存暂停。
- 每个运行的 WG 数和指令数都和同条件的 baseline 一致。统计里的窗口和表大小也逐个核对过：W = 64 / 256 / 2048，表 = 128 / 512 / 4096（`m2w.csv` 的 `win_stat`、`table_stat` 两列）。
- 时间：
  - yss2 从 11:44 跑到 13:37 UTC，yss3 从 11:44 跑到 13:16 UTC；
  - 并发 18–21 个；
  - 最长的单个运行是 npbcg baseline：W64@48 1.64 h，W2048 1.58 h；
  - 所有运行的墙钟加起来 44 h。
- 峰值 RSS 最大 7.0 GB。
- yss2 上的旧进程 2415098 没有碰过，现在仍然在。

## 7. 结果

见 `analysis.md`。`m2w.csv` 一共 96 行：
- 4 个条件（w64b384 复用自 abl3c384，w256b384、w2048b384、w64b48 是新跑的）× 6 个 workload × 4 个配置；
- 列包括时间、加速比、WG 和指令的核对、M1 / M2 / M3 的统计，以及 obs 的列（`links:*`、`lat:*`、`occ:*`、`owner_shift:*`、`sharing:*`；baseline、Complete 和 sssp/bfs 的 M1+M3 才有）。
- obs 的原始 CSV 在 `obs_raw/`。
