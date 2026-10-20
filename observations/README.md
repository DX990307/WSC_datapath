# ISCA'27 Observation 一节：要收集的数据

**状态**：`[提案]`（Cowork 10-20 起草，用户确认后再写成给 Claude Code 的提示词；**不要直接开始**）· 相关：`../tasks/T18_new_m2_two_sided_merge.md`、`../tasks/T15_headroom_oracle.md`、`../tasks/T14_wafer_platform_data.md`、`../results_index/p12/`

> 🔒 按 `../CONFIG.md` 做配置确认：论文配置是 `mi200.json`（`isca27-mi200-v1`），不改任何参数；所有运行保留 `-magic-memory-copy`。新统计默认关闭，回归逐位一致。oracle 和规模实验属于有意的配置偏离，必须先给用户确认。

## 1. 为什么要改

- 用户 10-20：ISCA'27 只做通信，机制是 T18（请求跟踪表规模化 + owner 端跨 GPM 合并多播），T19 放弃。
- HPCA 稿的 Motivation 有 O1–O6，要改的：
  - **删**：O6（本地 L2 缺失成对出现）——M1 Local Pairing 的 observation；O1 里"HBM 和 L2 占本地延迟 96.8%"的本地部分（只支撑 Local Pairing）。
  - **换**：O2（远程延迟随距离增长）、O3（请求方两种聚合机会）——旧 M2 的 observation。T18 不做同页打包，请求方合并在 HPCA 版已有，所以 O3 压成新 O5 里的一句。
  - **压缩**：O4（远程数据有时间复用）、O5（请求方 L2 有空闲容量）——Remote Reuse（m3）的 observation。用户 10-20：**m3 保留，但不作为重点**，所以两条合成一条放在最后（新 O7），按本文件的统一要求在 mi200 上重算（mi200 的 L2 是每 GPM 2 MB，HPCA 稿是 4 MB，空闲容量会变）。
- 图要换：HPCA 稿图 2（全路径延迟分解）、图 3（L2 缺失率）、图 5（聚合机会）、图 8（相邻缺失）删或重做；图 1、图 2 里的 "RDMA" 改成 network interface。
- 命名注意：P12 的"M1 共享统计"（`-report-m1-sharing`）和机制 M1（Local Pairing）同名，下面一律叫**共享统计**。

## 2. 统一要求（所有 observation 都适用）

- **配置**：`mi200.json` 原样（地址转换不变，用户 10-19 定的 baseline）。
- **workload**：和 T18 正式评估相同的集合和规模，observation 的数字才能和评估对上。
  - 远程型 7 个：FW、I2C、PR、MT、MM（`-max-wg 32768`）、SPMV（1536）、KM（8192）。
  - T17 三个：PagedAttention batch 66、MoE skew 1.75、R-GAT 320 种子。
  - 本地型 7 个不进 observation（论文里只报"不变慢"）。
- **截断有效性**：截断运行按 T15 的规则核对 kernel 数、WG 数、总指令数，差超过 1% 的格子不用。
- **R-GAT**：运行间有约 0.16% 的不确定（STATUS），小于 0.2% 的差异当噪声。
- **输出**：`../results_index/obs/`，里面放 `README.md`、`runs.csv`（运行、二进制、命令、墙钟、峰值内存）和下面每个 observation 的 CSV。

## 3. 新 observation 和对应的数据

### 新 O1：远程访问是主要代价（替换旧 O1）

- **论点**：在远程型 workload 上，远程读的平均延迟是本地的几十倍，占内存时间的大部分；如果数据全在本地（oracle O1），能快很多。
- **图**：每个 workload 一组柱：远程读占比（个数）、远程延迟占比（时间加权）、远程 / 本地平均延迟比；另一张图是 O1 oracle 加速比。
- **数据**：
  - 已有：T15 的 O1（旧 benchmark，几何平均 1.48×，FW 11.1×），T15 的远程个数占比和时间占比。
  - 在跑：T17 三个的 O1 / O2b / O3（P12 第 5 步）。
  - **可能要补**：KM 改成 `-max-wg 8192` 后的 B 和 O1 / O2b / O3（T15 用的是 32768）；T15 和评估规模不一致的格子。oracle 要配置确认。
- **CSV**：`obs1_remote_cost.csv`：`workload, B_cycles, remote_read_frac_count, remote_lat_frac_time, remote_mean_ns, local_mean_ns, O1_speedup, valid`

### 新 O2：远程延迟主要在请求方网络接口门口排队

- **论点**：一次远程读的时间大部分花在等请求跟踪表的空位，而不是在 mesh 上走或在 owner 端服务。
- **现状**：80–86% 的排队占比是闭环模型推出来的，没有直接测量。T17 的 `l2_summary` 只记网络接口到网络接口这一段的延迟（起止点待核对）。
- **要新加的统计**（E1 分段，只加统计，默认关）。对每个远程读记下这些时间点：
  - t0：L1V 把远程读交给本地网络接口；
  - t1：拿到跟踪表项、注入 mesh；
  - t2：到达 owner 网络接口；
  - t3：owner 网络接口转给 L2；
  - t4：L2 / HBM 的响应回到 owner 网络接口；
  - t5：响应回到请求方网络接口；
  - t6：L1V 收到数据。
- **分段**：等表项 = t1 − t0；去程 = t2 − t1；owner 入口排队 = t3 − t2；owner 服务 = t4 − t3（分 L2 命中 / 缺失）；回程 = t5 − t4；回到 L1V = t6 − t5。
- **报告**：每个 workload 各段的平均值和占比；按跳数分桶的平均值、p50、p95；和 T14 的空载值（远程 198.1 + 71.8 ns/跳）对比。
- **顺带回答**：负载下每跳多出的 180–360 ns（T17 观察）落在哪一段。
- **CSV**：`obs2_latency_segments.csv`：`workload, hops, n, wait_rrt, fwd, owner_queue, owner_service_hit, owner_service_miss, ret, to_l1v, total`（各段给 mean / p50 / p95）

### 新 O3：远程带宽受跟踪表限制，不受链路限制

- **论点**：跟踪表大部分时间是满的，链路却很闲；带宽和表项数成正比；去掉表项限制（O2b）比让距离几乎免费（O3）有用得多。
- **数据**：
  - 已有：T14 诊断——远程带宽随表项数 64 → 128 → 256 增长 ×1.9、×3.5，256 项时 1 跳链路也只用 12%；单对带宽 = 64 × 64 B ÷ 往返时间。T15 的 O2b、O3 加速比（旧 benchmark）；T17 的在跑。
  - **要新加的统计**：
    - 跟踪表占用：每个 GPM 每 1 µs 采样一次已用表项数、在等表项的请求数，以及按目的 GPM 分的在途数（看热点 owner）。owner 端同样记网络接口收到的在途数。
    - 报告：跟踪表占满（= 64）的时间比例、平均等待请求数、每 GPM 实际远程带宽（B/ns）。
    - 链路利用率：每条有向链路的字节数 ÷（时间 × 48 B/周期），报所有链路的平均、最大和 p99，以及时间序列上的最大值。
- **CSV**：`obs3_occupancy.csv`（`workload, gpm, frac_time_full, mean_used, mean_waiting, remote_bw, owner_in_flight_mean, top_dest_share`）、`obs3_links.csv`（`workload, link_mean, link_p99, link_max`）、`obs3_oracle.csv`（`workload, O1, O2b, O3, valid`）

### 新 O4：需要的表项随 wafer 规模增长（回答"为什么对 wafer 独特"）

- **论点**：填满一个 GPM 的链路需要的在途数 = 带宽 × 往返时间，往返时间随跳数增长。4 个 GPM 的 MCM 上 64 项够用，wafer 上不够；baseline 的排队占比和 T18 的收益都随 GPM 数增长。
- **已有**：T14 的往返时间和跳数（网络接口往返约 84 + 71.4 ns/跳）。按 100 B/ns 远程带宽算需要的表项：1 跳 242、平均 4.3 跳 611、6 跳 803、12 跳 1,477。
- **要新做：规模实验**。
  - mesh 3×3（8 个 GPM）、5×5（24 个）、7×7（48 个）。用奇数尺寸，CPU / IO 都在中心 tile，布局和现在一致；可选 2×2（4 个 GPM 的 MCM，CPU 不在 mesh 上），看模拟器是否方便支持。
  - **弱缩放**：每个 GPM 的工作量和数据量不变，workload 规模和 `-max-wg` 按 GPM 数等比例缩放；页放置规则不变（按 GPM 数等分）。
  - 配置：baseline、T18 全开 W=1024，可选再加"只放大跟踪表"。
  - workload：先用 5 个（FW、PR、MT、R-GAT、PagedAttention），覆盖"合并多"和"几乎不合并"两类。
  - 报告：平均跳数、空载 / 负载往返时间、按 Little 定律算的需要在途数、排队占比（用 E1）、链路利用率、T18 加速比。
  - **注意**：模拟器现在没有 mesh 尺寸的 flag（7×7 写死，待 Claude Code 确认），要先加 flag（默认 7×7，回归逐位一致）。新尺寸是配置偏离，要用户确认。
- **CSV**：`obs4_scale.csv`：`mesh, n_gpm, workload, config, avg_hops, rtt_mean, needed_inflight, queue_frac, link_max, speedup_vs_base`

### 新 O5：多个 GPM 在短时间内读同一条 line，而且读者彼此靠得近

- **论点**：owner 端的 incast 是真实存在的。同一条 line 在 1–10 µs 内被多个 GPM 读，最多 47 个；这些读者彼此只隔 1.5–2.6 跳，所以一次读、沿路由树多播能省掉大部分重复的 owner 工作和链路。incast 时 owner 端封顶约 113 B/周期（T14）。
- **请求方部分**（原 O3 压成一句）：同一 GPM 不同 CU 的重复远程读（MoE 15%、R-GAT 34%、FW 49%、PR 17%，P12）——HPCA 版已有，只作背景。
- **数据**：
  - 已有（P12，`../results_index/p12/m1_*.csv`）：PagedAttention b33 / b66、MoE、R-GAT、FW 2048 × 8 pass、PR 小输入、KM 小输入。
  - **要补**：I2C、MT、SPMV、MM，以及把 FW、PR、KM 换成评估规模重跑（`-report-m1-sharing`，只加统计）。
- **报告**：共享 line 占远程读的比例（1 µs / 10 µs 窗口）；每条 line 的读者数分布（fan-in 直方图）；读者之间的跳数（最近一对 / 平均）；多播省的链路（共享 line 上 / 占全部响应）；owner 端同时在途的同 line 请求比例。
- **CSV**：`obs5_sharing.csv`、`obs5_fanin_hist.csv`

### 新 O6：只放大跟踪表，瓶颈会转到 owner 端（说明为什么两部分要一起做）

- **论点**：去掉请求方的表项限制后，来自不同 GPM 的同 line 请求更集中地到达 owner，owner 端排队和 incast 让往返时间停在微秒级；只有加上 owner 端合并 + 多播，大跟踪表才用得上。
- **冒烟证据**（STATUS "T18 冒烟测试"）：FW 只放大到 1024 是 2.26×，全开 8.56×；基线时序下 owner 端同 line 并发只有 6–8%（P12），放大后 FW 的 owner 合并率到 39.5%。
- **要新跑**：T18 评估里已有的"只放大跟踪表 W=4096、不合并"配置（不是新配置），加上 E1 分段、跟踪表 / owner 端占用、共享统计，10 个 workload。和 baseline（同样的统计）比较：owner 入口排队、owner 端同 line 并发比例、往返时间、链路利用率。
- **CSV**：`obs6_owner_shift.csv`：`workload, config, owner_queue_mean, owner_same_line_concurrent_frac, rtt_mean, remote_bw, link_max`

### 新 O7（次要，m3）：一部分远程数据会被再次读，请求方 L2 有空余容量放它

- **论点**：合并 HPCA 稿的 O4、O5，一段话 + 一张小图。只用来引出 m3，不展开。
- **数据**（R1 里一起收集，只加统计；HPCA 版的统计代码如果还在，直接复用）：
  - 远程复用：同一个请求方 GPM 读过的不同远程 line 里，被读不止一次的比例；第二次及以后的读占全部远程读的比例；两次读之间的时间间隔分布（决定 L2 能不能留住）。
  - L2 空余容量：mi200 的 L2（每 GPM 2 MB）里无效或直到被替换都没再被读的 way 的平均比例。
  - 和 T18 的关系：T18 的多播会把同一条 line 同时送到多个请求方，m3 再把它留在各自的 L2，两者是互补的。可以顺带统计"多播送达后又被同一 GPM 再读"的比例（需要 T18 打开，放进 R3）。
- **m3 本身要补的**（不是 observation，但写论文前要做）：kernel 边界失效（现在的实现在 kernel 边界没有失效，和论文描述不一致）；和 B1（真实 MI200 把远程数据缓存在请求方 L2）对比；我们的 L2 命中延迟偏小（129 ns），m3 的收益可能被高估，论文里要写明或做 L2 延迟的敏感性。
- **评估里的位置**：m3 只出现在消融和 Complete（= T18 + m3）里，主图和主要论点都用 T18。
- **CSV**：`obs7_remote_reuse.csv`：`workload, uniq_remote_lines, frac_lines_reused, frac_reads_retouch, reuse_interval_p50, reuse_interval_p90, l2_free_frac`

## 4. 运行清单（估计）

| 编号 | 内容 | 个数 | 配置确认 |
|---|---|---|---|
| R1 | baseline + E1 + 占用 + 链路 + 共享统计 + 远程复用 / L2 空余（O7），10 个 workload | 10 | 不需要（只加统计） |
| R2 | T18 只放大跟踪表（W=4096）+ 同样的统计 | 10 | 不需要（T18 已确认的配置 + 统计开关） |
| R3 | T18 全开 W=1024 + 同样的统计（评估图 E3、E5 用：排队被消掉了多少） | 10 | 不需要 |
| R4 | 补 oracle：KM 8192 等规模不一致的格子的 O1 / O2b / O3 | 约 3–6 | **需要** |
| R5 | 规模实验：3×3 / 5×5 / 7×7 × baseline / T18 × 5 个 workload（7×7 可以复用 R1、R3） | 约 20 | **需要**（还要先加 mesh 尺寸的 flag） |
| R6 | 微基准：3×3 / 5×5 上的跳数延迟、带宽和表项数（T14 的方法）；7×7 上远程带宽随表项数扫到 1024 / 4096（T14 诊断只到 256） | 约 10 | **需要** |
| R7 | 敏感性：链路 384 GB/s（`-bandwidth=24`）、每跳 20 周期（`-switch-latency=20`）、L2 命中延迟加长（m3 用）× {baseline, T18 全开} × 5 个 workload | 约 30 | **需要** |
| R8 | m3 单独、T18 + m3（Complete），10 个 workload。先修 m3 的 kernel 边界失效，并确认 T18 和 m3 能同时打开 | 20 | 不需要（机制开关），但 m3 的修复要回归 |

R1 和 R2 先跑：不需要确认，能直接支撑新 O2、O3、O5、O6、O7。统计全开如果太慢或内存太大，拆成两次运行（E1 一次，其余一次）。

## 5. 论文图表清单（Motivation + Evaluation）

所有图用 mi200 和第 2 节的 10 个 workload，最后一列是 GMEAN。"必须"的先做。

**Motivation**

| 图 | 内容 | 数据 | 状态 | 优先级 |
|---|---|---|---|---|
| F1（O1） | (a) 每个 workload 内存时间里远程 / 本地的占比（堆叠柱，柱上标远程 / 本地平均延迟比）；(b) 全本地 oracle（O1）加速比 | T15、T17 oracle（在跑）、R4 | 部分已有 | 必须 |
| F2（O2） | 远程延迟分段：每个 workload 一根 100% 堆叠柱（等表项 / 去程 / owner 排队 / owner 服务 / 回程 / 回到 L1V）；旁边小图：延迟和跳数（T14 空载线 vs 负载实测点） | R1 的 E1 分段 | 要新跑 | 必须 |
| F3（O3） | (a) 跟踪表占满的时间比例 vs 最忙链路的利用率（每个 workload 两根柱）；(b) 微基准：远程带宽 vs 表项数 64–4096，1 跳和 6 跳两条线，水平线是链路带宽；(c) O2b（跟踪表不受限）vs O3（距离几乎免费）的加速比 | R1；T14 诊断 + R6；T15、T17 + R4 | (b)(c) 部分已有 | 必须（(c) 可并进 F1） |
| F4（O4） | 横轴 3×3 / 5×5 / 7×7（8 / 24 / 48 个 GPM）：平均跳数、按 Little 定律需要的在途数（画 64 的水平线）、baseline 的排队占比 | R5、R6 | 要新跑 | 必须（回答"为什么 wafer 独特"） |
| F5（O5） | (a) 1 µs / 10 µs 窗口内读共享 line 的远程读比例；(b) 每条共享 line 的读者数 CDF；(c) 读者之间最近跳数 vs 到 owner 的跳数 CDF；(d) incast：k = 1 … 47 个 GPM 读同一 owner 时的总带宽（封顶 113 B/周期） | R1 的共享统计、P12；T14 | 部分已有 | (a)(d) 必须，(b)(c) 可选 |
| F6（O6） | 瓶颈转移：FW、R-GAT、PagedAttention × {baseline、只放大跟踪表、T18 全开} 的延迟分段堆叠柱（绝对 ns） | R1、R2、R3 | 要新跑 | 必须 |
| F7（O7，次要） | 远程 line 被再读的比例 + L2 空余比例（一张小图） | R1 | 要新跑 | 次要 |

**Design**：机制图已有（`../figs/fig_t18_two_sided_merge.*`）；加一张小表：RRT、OMT 的表项格式和位数。

**Evaluation**

| 图 / 表 | 内容 | 数据 | 状态 | 优先级 |
|---|---|---|---|---|
| 表 A | 平台配置（mi200，替换 HPCA 稿表 II；网络接口一行写 RRT 64 项和引用） | `mi200.json` | 已有 | 必须 |
| 表 B | workload：领域、输入、WG 数、footprint、远程读占比、O1 上限 | T17、T18 评估、F1 | 部分已有 | 必须 |
| E1 | 主结果：每个 workload 的旧 m2（HPCA 版）、T18、Complete（T18 + m3）加速比，用点标出 O1 上限；本地型 7 个"不变慢"放表里 | T18 评估（在跑）、R8 | 在跑 | 必须 |
| E2 | 消融：只请求方合并、只 owner 端、只放大跟踪表、T18 全开、全开但关多播 | T18 评估（在跑） | 在跑 | 必须 |
| E3 | 延迟分段 before / after：每个 workload baseline vs T18 的堆叠柱 | R1、R3 | 要新跑 | 必须 |
| E4 | 跟踪表大小 W = 64 / 256 / 1024 / 4096：GMEAN 加速比（加每个 workload 的细线），右轴 CACTI 面积 | T18 评估、CACTI | 在跑 | 必须 |
| E5 | 合并效果：请求方合并率、owner 合并率、平均多播扇出、mesh 总字节数相对 baseline 的变化 | T18 评估的统计（确认有没有记）、R3 | 在跑 | 必须 |
| E6 | 规模：T18 加速比 vs mesh 尺寸（GMEAN + 每个 workload） | R5 | 要新跑 | 必须 |
| E7 | 和 HPCA 版比较：hpca 配置、`-max-wg 78600` 下 HPCA baseline / HPCA Complete / T18 / 只放大跟踪表 | 28 个运行（在跑） | 在跑 | 必须（回答"相对 HPCA 新在哪"） |
| E8 | 敏感性：链路 384 vs 768、每跳 20 vs 32 周期、按距离分 credit（W = 64 / 256）、L2 命中延迟加长（m3） | R7、T18 评估 | 要确认 | 建议 |
| 表 C | 硬件开销：RRT（W = 256 / 1024 / 4096，cuckoo 索引 + 旁路完整地址）**和同容量全相联 CAM 对比**、OMT、switch 复制逻辑；面积、访问时间、功耗（CACTI 7，和 HPCA 稿同样的方法） | CACTI | 不用模拟 | 必须 |
| 表 D | 稳健性：指纹误匹配次数、表满退回单播的比例、`-verify` 结果 | T18 评估、冒烟 | 部分已有 | 可选 |

**收集的先后**：
1. 已经在跑的：T18 评估、HPCA 规模比较、T17 的 oracle → E1、E2、E4、E7。
2. R1、R2、R3（只加统计，不用确认）→ F2、F3(a)、F5、F6、F7、E3、E5。
3. CACTI → 表 C、E4 的面积轴。
4. R8（先修 m3）→ E1 的 Complete。
5. R5、R6（先加 mesh 尺寸的 flag，要确认）→ F4、E6、F3(b)。
6. R4、R7（要确认）→ 补齐 F1、F3(c)、E8。

## 6. 待用户决定

1. ~~m3 去留~~ 已定（用户 10-20）：m3 保留，但不作为重点，旧 O4、O5 合成新 O7。
2. Local Pairing 是否从论文里完全去掉（按"只做通信"，默认去掉）。
3. 规模实验的尺寸（3×3 / 5×5 / 7×7，要不要加 2×2）和 workload。
4. R4、R5、R6、R7 的配置确认。

## 7. 论文里相关的文字改动（提醒）

- 图 1、图 2 和正文里的 "RDMA" 一律改成 network interface，"request table" 改成 remote request tracker。
- 表 II "64 outstanding/direction" 现在引的是 [8]（Belayneh 等 PACT'22），要核对原文确实是 64。可以加引 AMD NoC2（PG406）：NMU 最多 64 个在途读、读重排序缓冲 64 × 64 B，NSU 32 个。64 项的完整依据见 `../tasks/T18_new_m2_two_sided_merge.md` 和 `../ideas_from_T14.md` 10-20 一节。
