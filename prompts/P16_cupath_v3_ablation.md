# P16：按新结构实现 M1 / M2 / M3，跑小 footprint 的消融

> 用户 10-20 晚："按照新的版本重新 implement 吧……之后跑一个小的 memory footprint 的 ablation study，要求每个 benchmark 都能用到所有的 GPU"。
> 设计依据：`../tasks/T18_new_m2_two_sided_merge.md` 顶部 🧭 说明、`../observations/README.md` 顶部 🧭 说明、讲解图 `../figs/advisor_1021/CuPath_M1M2M3_figs_v3.pdf`。

---
继续 CuPath 的 ISCA 2027 工作。

**0. 先同步**（SYNC.md 第 2 节）
- 读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目（10-20 晚：表保持 64 项；M1 (b)(c) 改回 T18；P15；机制按硬件位置重新分；本文件），用 `git diff` 读改过的文件。
- 以 `cowork: ...` 提交，推送到 `isca27/docs`。
- **P15 怎么处理**：还没开跑就不跑了（本文件的 baseline 和 Complete 覆盖它）；已经在跑或跑完了，就跑完并保留结果，作为"同规模下 W = 64 对 W = 1024"的对照。

**1. 新的机制定义（用户 10-20 晚，覆盖之前所有定义）**

RDMA 两端的表都保持 64 项：`t18-window` 固定 64；表大小用默认的 2W = 128 槽（用户："128 槽就行"）。

| 机制 | 部件 | 现有代码 |
|---|---|---|
| **M1 RDMA 端** | 合并：请求方在途表合并 + owner 合并表 | `-t18-req-merge`、`-t18-owner-merge`，`cupath-dedup-owner = m1` |
| | 打包：同页位图包 + 预取捎带 | `remote-data-path-batching-enable`、`remote-filter-prefetch-enable`（旧 m2 的参数） |
| | 分配：64 项按距离分 | 按往返时间分 = `-t18-dist-credits`（已有）；**近处多留（新，见 2）**；不分 = 共享池 |
| **M2 网络** | 网内多播（建立在 M1 的 owner 合并上，不能单独开） | `-t18-owner-multicast` |
| **M3 保留** | 请求方 L2 留远程副本 | `remote-data-path-l2-enable` |

- 旧 m2 的 filter 去重不再用（`remote-data-path-dedup-enable` 在所有新配置里都关，去重只由在途表做）。typed filter 本身保留（预取用 PATTERN，M3 用 RESIDENT / SEEN）。
- 代码和 flag 的名字不改；论文里的叫法按上表。

**2. 要实现的**

先选基线分支：从最近的集成分支开出 `isca27/cupath-v3`。候选是 `isca27/main-run`（`8446d36`，停掉的正式运行用的：W2 benchmark + Complete + 统计）。先列出它和 `isca27/w2-complete`（`d09b18b`）、`isca27/obs-stats`、`isca27/fix-lds-m3`、`isca27/obs-m3-inval` 的关系：哪些已经包含、哪些已经做完并回归过、哪些要合并。没做完的不要合并，写进汇报。

(a) **分配：加"近处多留"的份额方式**（`t18_credits.go`）
- 新 flag：`-t18-credit-policy = shared | rtt | near`。默认值要保证现有配置的行为不变：现有的 `-t18-dist-credits=true` 等于 `rtt`，`false` 等于 `shared`；两个 flag 同时设而且矛盾时报错。
- `near` 的语义：
  - W = 64 项里先留出**近区 16 项**（`-t18-near-reserve=16`），只给**跳数 ≤ 3** 的目的地用（`-t18-near-hops=3`）。远的目的地**任何时候都不能用近区**。
  - 其余 48 项按现有的 `rtt` 规则分给**所有**目的地：每个目的地有按往返时间分的保底份额，有请求在等的留着保底，空闲的可以借，不抢回。
  - 近的目的地先用近区；近区满了，再按 `rtt` 规则用其余 48 项。
- 单元测试：份额加起来等于 W；远的不会占近区；借用和保留规则和 `rtt` 一致；`near-reserve=0` 时逐位等于 `rtt`。
- 组合模式（M1 + 打包，`m1m2_compose.go` 的 `m1CanSend`）也要走同一个分配器，确认生效。

(b) **每个机制组合都要能单独开**（不改代码最好，用配置文件拼）：确认下面每个配置在 `[Config]` 输出里看到的开关和预期一致，尤其是：
- M1 去掉合并、但保留打包和分配时，请求方仍走 T18 的窗口和分配器；
- 不开 M2 时，合并过的 owner 项退回逐个单播；
- 不开 M3 时，请求方 L2 不探测、不回填。
需要改代码才能做到的，先写清楚改什么，再改。

(c) **统计**（只加统计，默认关；`isca27/obs-stats` 已有的就复用）：
- ① 分配：请求方表按距离分组（近 ≤ 3 跳 / 中 4–6 / 远 ≥ 7）的时间加权占用；表满时近的请求在门口等的时间；`near` 时近区的占用和"近区满后去借其余 48 项"的次数。
- ② 合并：请求方合并数（分"挂到已发出的项"和"挂到还在排队的项"）；owner 收到的整行读、合并数、owner 表满退回单播的次数和时间。
- ③ M2：多播包数、平均目的地数、退回单播的比例、owner 少发的响应数。
- 打包：平均每包 line 数、预取捎带数和被用上的比例。M3：请求方 L2 命中远程 line 的次数。
- 每个 GPM 的 WG 数和活跃时间（用来检查"用到了所有 GPM"，可用 `-report-gpm-trace`）。

(d) **回归**：全部新开关关闭时 fir、MM（`-max-wg 4096`）逐位一致（26.568 µs / md5 `d56a6814`、345.433 µs / md5 `22a58b27`）；`rtt` 配置和现有 `complete_new` 改成 W = 64 后逐位一致；把结果写进 STATUS。

**3. 配置**（都 `extends: mi200.json`，`t18-window: 64` 显式写出，保留 `-magic-memory-copy`，`mi200.json` 不改）

| # | 配置文件 | 内容 |
|---|---|---|
| 1 | `mi200.json` | baseline |
| 2 | `v3_m1.json` | M1：合并 + 打包 + 分配（`rtt`） |
| 3 | `v3_m1m2.json` | M1 + M2 |
| 4 | `v3_m1m3.json` | M1 + M3 |
| 5 | `v3_complete.json` | M1 + M2 + M3（分配 `rtt`） |
| 6 | `v3_complete_nomerge.json` | Complete 去掉合并（M2 没有合并的项可以多播，相当于也关了） |
| 7 | `v3_complete_nopack.json` | Complete 去掉打包（预取捎带一起关） |
| 8 | `v3_complete_alloc_shared.json` | Complete，分配 = `shared`（不分） |
| 9 | `v3_complete_alloc_near.json` | Complete，分配 = `near`（≤ 3 跳，近区 16 项） |

- 每个文件的 `_description` 写清楚它是哪个机制组合。
- **按长期规则，开跑前把 9 个配置相对 `mi200.json` 的差异、以及运行日志里的 `[Config]` 行给用户确认。**

**4. workload 和规模（小 footprint，每个 benchmark 都要用到全部 48 个 GPM）**

- workload（用户选的，共 26 个）：W2 新 16 个（mg、lud、allreduce、sssp、ivfflat、npbis、embbag、npbcg、gups、shocsort、cfd、bh、atax、fft2d、bfs、graphtc）、旧远程型 7 个（FW、I2C、PR、MT、SPMV、KM、MM）、T17 三个（PagedAttention、MoE、R-GAT）。
- 规模要求：
  - **baseline 墙钟 ≤ 1 小时**（用户定的；按服务器平时的负载估）。
  - **用到全部 48 个 GPM**：
    - 主要 kernel（合计占 baseline 模拟时间 ≥ 80%）每个都要给 48 个 GPM 都发 WG，每个 GPM 至少 28 个 WG（每个 CU 至少 1 个）；
    - 数据按现在的放置规则分到全部 48 个 GPM 上（不改放置规则）；
    - 报告每个主要 kernel 里"48 个 GPM 都有 WG 在跑"的时间占比。
  - 尽量跑完整的 kernel，不截断。必须截断（`-max-wg`）的按 T15 的规则核对 kernel 数、WG 数、指令数。
- 已知的问题，先报告、不要自己定：
  - **R-GAT**：现在的版本 85% 的时间只用到 ≤ 13 个 GPM，不满足要求；之前提过的方案 B（改成铺满 48 个 GPM）是 benchmark 改动，要用户同意。
  - **PagedAttention**：batch 小了以后，reduce kernel 可能有 GPM 空着，要检查。
  - **graphtc、npbis 2 次迭代，以及 6 小时规模下崩溃的那 5 个**：LDS 越界修好了才进；没修好的列出来。
  - 达不到要求的 benchmark 单独列出原因，问用户怎么处理，不要擅自去掉或改 benchmark。
- **先做规模校准**（短时间试跑外推），给用户一张表：每个 benchmark 的参数、每个主要 kernel 的 WG 数和每 GPM 的 WG 数、footprint（MB）、预计 baseline 墙钟、48 个 GPM 都在用的时间占比。**用户确认后再开跑。**

**5. 运行**
- 26 × 9 = **234 个运行**；另外本地型 7 个只跑 baseline 和 Complete（14 个），确认不变慢。
- 所有配置用同一个新二进制（包括 baseline）。命名：`<MMDD>_abl3_<item>_mi200[+<配置名>]_<bin>`。
- 先跑 baseline 和 Complete，再跑其余；yss2、yss3 按内存规则分配。

**6. 结果**（`results_index/abl3/`：`abl3.csv` + `README.md`）
- `abl3.csv`：每个 workload × 配置一行；时间、相对 baseline 的加速比、指令数和 WG 数核对、48 个 GPM 都在用的时间占比，以及 2(c) 的全部统计。
- `README.md`：
  - 主表：每个 workload 的 M1、M1 + M2、M1 + M3、Complete 加速比和 GMEAN；
  - M1 内部：Complete 去掉合并 / 去掉打包 / 分配 = `shared` 相对 Complete 的损失；
  - 分配方式：`shared` / `rtt` / `near` 三种的比较，加上统计 ①；
  - 互补性：每个 workload 上哪个机制（或 M1 的哪个部件）贡献最大，列出每个部件"是主要贡献者"的 workload。用户要用这张表回应"堆技术"的质疑；某个部件在所有 workload 上都贡献很小，要明确写出来。
  - 和 W = 1024 的上限参考对比（`w2complete` 的 2.82×），只作参考。

**7. 不要做**
- 不跑 W > 64；不改表大小（不设 `-t18-req-entries` / `-t18-owner-entries`）；不改 `mi200.json`；不做 14 CU；冻结的队列保持冻结。
- 不擅自改 benchmark（R-GAT 方案 B 等要用户同意）。

**8. 收尾**：STATUS 加一行和结果摘要；SYNC 写 `[claude-code]` 条目；分支和 `isca27/docs` 推送（只推 `isca27/*`，不要 force push）。每完成一步（实现 + 回归、规模校准、运行、结果）都提交备份一次。
