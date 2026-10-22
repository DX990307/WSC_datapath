# pm：页迁移对比基线（page migration baseline）

Claude Code，2026-10-21。

这组实验回答审稿人的问题："为什么不直接改数据放置，或者把页迁过去？"
- 设计、代价依据、正确性论证见 `DESIGN.md`。
- 结果和解释见 `analysis.md`。
- 每个运行一行的数据在 `pm.csv`。

## 1. 代码和二进制

- 分支 `isca27/page-migration`（worktree `~/Desktop/CuPath/wt-pm`），从 `isca27/cupath-v3` @ `4235991` 开出，**已推送到 origin**。
  - 提交：`9546fd9` → `c5e3c78` → `be68777` → `5f20aed` → `2a2197b` → `b36f6c7` → `c74ef96` → 之后只改了注释和文档的提交（见 `git log`）。
- **所有正式运行都用 `baseline_c74ef96`**：
  - 用 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 编译，md5 `c847ed84848f8d1d6d4dfc75e3956f16`；
  - 两台服务器的 `~/akita-exp-test/pm/bin/` 下各有一份。
  - c74ef96 之后的提交只改注释和文档，不影响二进制的行为。
- 开关：`-page-migration none|observe|fault|counter`，以及 `-pm-*` 参数（DESIGN.md 3.3）。
  - 配置叠加文件：`akkalat/configs/pm_{observe,fault_4k,fault_2m,counter_4k,counter_2m,ideal}.json`。
  - 默认 `none`：什么都不建，什么都不挂。

## 2. 验证

| 项目 | 结果 |
|---|---|
| 关闭迁移时的回归（768 GB/s 默认网络，mi200，`-max-wg 4096`） | fir 26.568 µs，md5 `d56a681494ae`；MM 345.433 µs，md5 `22a58b272d89`。be68777、2a2197b、c74ef96 三个二进制都和 4235991 **逐位一致**（`verify.csv` 的 `reg-*` 行） |
| observe（只装钩子）对时序的影响 | c74ef96 上，fir 和 MM 的 metrics 去掉 PageMigration 那几行后，md5 和关闭时相同；bfs/sssp/atax 小规模也逐位一致。<br>384 GB/s 正式运行中，25 个 workload 里有 23 个的 observe 时间和 abl3c384 的 baseline 完全相同。不同的两个：<br>- bfs：差 6%，这是 bfs 自身的运行间波动，同一个命令重跑在 0.937–1.037 ms 之间（analysis.md 第 1 节）；<br>- npbcg：差 4×10⁻⁶ |
| 早期版本 observe 的偏差（已修） | c74ef96 之前（be68777、2a2197b），observe 会建拷贝引擎，在 mesh 和连接上多一个端口，改变了轮转仲裁：fir 26.550 µs，MM 344.738 µs。c74ef96 起 observe 不建引擎。迁移模式下这个端口是硬件的一部分，带来约 0.1–0.2% 的扰动 |
| `-verify`（迁移不能改变结果） | 104 个服务器运行（`verify.csv` 的 `chk-*`、`vfy-*` 行）**全部 exit 0，结果全对**，都开了 `-pm-check`。<br>8 个 workload：bfs、atax、sssp、graphtc、npbcg、npb_mg、lud（就地写）、fft2d（就地写）。<br>配置：4 个变体、ideal、Complete+迁移，以及压力配置（阈值 16、冷却 0、不限次数），最多的一个跑了 1.7 万次迁移、1.5 万次来回。<br>本地另有 54 个更小规模的压力组合，全部通过 |
| 单元测试 | `mgpusim/timing/pagemigration`：9 个；`addresstranslator/migrationhook_test.go`、`tlb/invalidatepage_test.go`：各 1 个；runner 的已有测试全部通过 |

## 3. 实验

- 服务器：yss2 和 yss3 的 `~/akita-exp-test/pm/`。
  - `src/`：`wt-pm` 的副本；
  - `src/results/<exp>/`：每个运行的结果；
  - `queue.tsv`、`launched.tsv`、`done.tsv`、`hangs.tsv`、`sched.log`、`pids.txt`：调度器文件；
  - `verify/runs/`：验证、回归、诊断运行；
  - `void_*`：中途作废的运行，不进入任何统计：
    - `void_dupsched`：yss3 上误起了两个调度器，第一批全部停掉；
    - `void_obs_ports_*`：observe 修正之前用 2a2197b 跑的第一批；
    - `verify/void_*`：二进制没有同步成功的那批，以及 -pm-check 修正之前的那批。
- 调度器 `scripts/sched.py`：沿用 v3m2w 的版本，加了一个功能——重启时能接管仍在跑的运行。
  - 30 GB 余量规则不变，看门狗不变（模拟时间 60 min 不动并且 CPU < 2% 才杀）。
  - est：mi200 的运行先是 9，后改 5，最后改 4（实测峰值 RSS 3.5–5.1 GB）；Complete+迁移的运行是 8。
- 主配置：384 GB/s（`-bandwidth=24 -switch-buffer-flits=16`），W=64，`-magic-memory-copy`，统计用 `-report-cupath-v3-stats`。
- 命名：`1021_abl_pm-<workload>_<mi200|v3complete>+<tag>_c74ef96`。完整命令行见 `scripts/commands.tsv`，由 `scripts/mkqueue.py` 生成。
- baseline 和 Complete **复用 abl3c384**（4235991，同样的网络参数）。复用是否成立，由 observe 交叉核对（上面第 2 节）。

| 批次 | 内容 | 运行数 |
|---|---|---|
| 主批次 | 25 个 workload × 9 个配置：observe，fault/counter × 4K/2M，以及这 4 个的 ideal | 225 |
| 敏感性（9 个 workload） | fault 4K 的 sw 20 µs；counter 4K 的 sw 20 µs；counter 4K 的阈值 64 和 1024；fault 4K 的 max-migrations=1 | 45 |
| sw 敏感性 | cfd、ar、npbcg、pr × {fault 4K, counter 4K} × sw {1, 2, 10} µs（5 µs 和 20 µs 已经在上面两批里） | 24 |
| Complete+迁移 | 25 个 workload × {Complete+fault 4K, Complete+counter 4K} | 50 |

workload 和参数：
- 9 个主 workload 的参数和 m2w、abl3c384 完全相同；
- 另外 16 个（用户 10-21 追加）照 `~/akita-exp-test/v3c384/queue.tsv` 的 mi200 行。

## 4. 运行情况

- 只用 exit 0 的运行的 summary.csv 和 stats，WG 数和指令数都和 baseline 核对过（`pm.csv` 的 `inst_wg_match_baseline` 列）。
- **hang**：看门狗判为挂住并结束的运行，在 `pm.csv` 里 `status=hang`：mg-W pmf2m、mg-W pmc4kideal、shocsort pmc2m、npbis pmf2mideal。
  - 前 3 个已经用同一个命令在同一个模拟时间复现，并用 monitor 查过现场：
    - 卡住的是若干 GPM 的 L2TLB：TopPort 缓冲里有 35–56 条请求，MSHR 和 respondingMSHREntry 都是空的，组件不再 tick；
    - 迁移管理器这边没有进行中的迁移，AT 上没有被迁移挂起的访问。
  - 这就是 abl3c384 第 3 节记录的 L2TLB→IOMMU 丢唤醒 bug，不是迁移引起的。按要求**没有修**。
  - 迁移（特别是 2 MB 和 ideal）会产生更多 TLB 失效和 IOMMU 请求，所以更容易撞上这个 bug。
  - 诊断记录在 `verify/runs/diag-*-hang/DIAGNOSIS.txt`。

## 5. 本目录的文件

| 文件 | 内容 |
|---|---|
| `DESIGN.md` | 设计说明：为什么不用原流程、架构、时序、参数和出处、正确性、局限、用法 |
| `analysis.md` | 结果和分析 |
| `pm.csv` | 每个运行一行：时间、相对 baseline 和相对 Complete 的加速比、迁移统计、热点 owner 份额、状态 |
| `verify.csv` | 回归和 `-verify` 运行 |
| `scripts/` | `mkqueue.py`、`sched.py`、`ext_pm.py`（在服务器上逐个运行抽取统计）、`collect.py`（生成 `pm.csv`）、`analyze.py`（生成 markdown 表） |

没有改 STATUS 和 SYNC。

## 6. 新基线（384 GB/s，W=256）重跑：已暂停（10-22）

用户 10-22 决定先停掉这一轮。主会话在两台服务器的 `~/akita-exp-test/pm/` 放了 `PAUSE`，并结束了正在跑的运行（记录在 `KILLED_priority.txt`）。pm 调度器我也已经停掉，没有再排运行。已经完成的部分如下。

1. **合并**：
   - `origin/isca27/fix-l2tlb-wakeup` @ `c08698b`（L2TLB 丢唤醒修复、IOMMU 放到中心 tile (3,3)、driver drain 同步、bfs 修复、v4_complete 配置）用 `--no-ff` merge 进 `isca27/page-migration`，得到 **`15beab2`**，已推送（c74ef96..15beab2，fast-forward 推送）。
   - 冲突有两处，都是"保留新基线的写法，再加上迁移那一行"：
     - `r9nanobuilder.go`：`L2TLBToIOMMU` 改用 L2TLB 自己的 `ToIOMMU` 端口；`PageMigrationDMA` 端口仍然只在迁移模式下加。
     - `timingplatform.go`：先 `checkIOMMUTopology`，再 `buildPageMigrationManager`。
   - 单测通过：pagemigration、rdma、driver、addresstranslator、l2tlb（含新的唤醒测试）、mesh、runner。
   - 二进制：`baseline_15beab2`，用 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 编译，md5 `029bca22d82e52b0f105e0a5142af9bb`，两台服务器的 `~/akita-exp-test/pm/bin/` 下各有一份。
2. **逐位一致检查**（yss2，`~/akita-exp-test/pm/verify4/run.sh`）：
   - 命令和 `~/akita-exp-test/pgcls/verify2/` 相同：npbcg S，`-timing -bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256`；v4 另加 `-t18-window=256 -t18-compose-table-entries=512`。

| 运行 | 时间 | md5（全部行） | md5（去掉 PageMigration 行） | 参考（c08698b） |
|---|---|---|---|---|
| mi200，`-page-migration none` | 669.364 µs | `8bffefbb720c` | `8bffefbb720c` | 669.364 µs，`8bffefbb720c` |
| mi200，observe | 669.364 µs | `799b8d71251a` | `8bffefbb720c` | 同上 |
| v4_complete，none | 517.305 µs | `87dd0b3a1552` | `87dd0b3a1552` | 517.305 µs，`87dd0b3a1552` |
| v4_complete，observe | 517.305 µs | `2e4ce03dd183` | `87dd0b3a1552` | 同上 |

   结论：none 逐位一致；observe 只多了 PageMigration 那几行统计，时序和其它统计也逐位一致。
3. **排好的队列**（`scripts/mkqueue_w256.py` 生成，完整命令见 `scripts/commands_w256.tsv`，调度器是 `scripts/sched_w256.py`）：
   - 25 个 workload × 6 个配置 = 150 个运行：c4k、f4k、f4k-ideal、c4k 阈值 64、v4_complete+c4k、v4_complete+f4k；
   - 命名 `1022_abl_pm-w256b384-<wl>_<mi200|v4complete>+<tag>_15beab2`；
   - est：迁移运行 5，v4+迁移 8。
4. **运行情况**：04:30 UTC 开始，启动了 11 个，都是 c4k：npbcg、sssp、cfd、mm、atax、moe、pr、bfs、ar、tc、mg。**全部在完成前被结束（rc=−15），没有可用的结果**。其余 139 个还没有启动，队列文件仍在服务器上（`queue.tsv`）。
   - 旧一轮（c74ef96，W=64）的调度文件移到了 `~/akita-exp-test/pm/old_c74ef96/`，结果仍在 `src/results/`。
5. 恢复的方法：去掉 `PAUSE`，把被结束的 11 个运行的结果目录移走（`isca27_run.sh` 遇到已有的 `meta.json` 会拒绝运行），再从 `done.tsv` 和 `launched.tsv` 里删掉对应的行，然后在两台机器上各启动一个 `python3 sched.py`（`sched.py` 里的 COMMIT 已经是 15beab2）。
