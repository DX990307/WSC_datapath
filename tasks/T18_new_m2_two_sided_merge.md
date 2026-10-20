# T18（论文里的 M1）：请求跟踪表规模化 + owner 端跨 GPM 合并多播

**状态**：实现完成（Claude Code，分支 `isca27/t18-merge` @ `ca8b831`，默认关闭，全部开关关闭时逐位一致）；正式评估运行中（mi200 141 个 + HPCA 规模 28 个，见 `../STATUS.md`）。10-20 Cowork 按"相对 HPCA 版 m2 新增了什么"重写本文件，统一术语、补 64 项的依据和"为什么对 wafer 独特"的实验。

> 🔒 **执行前必做**：按 `../CONFIG.md` 做配置确认。不改 mi200 / hpca 的任何参数；机制默认关闭，用开关打开。所有运行保留 `-magic-memory-copy`。
>
> **论文前提（用户 10-19 / 10-20）**：地址转换已经由 HDPAT（用户的 HPCA'26 论文）解决；ISCA'27 以 CuPath 为底，**只做通信**（远程数据通路），thread / data scheduling 留给下一篇（`../next_paper_scheduling.md`）。新机制不缓存、不复制、不迁移。baseline 用 mi200 原样、地址转换不变（用户 10-19）。
> 索引：`../README.md` · 进度：`../STATUS.md` · 同步：`../SYNC.md` · 机制图：`../figs/fig_t18_two_sided_merge.{pdf,svg,png}`（`../figs/paperfig.py` 生成）

## 术语（10-20 起，论文和文档统一）

| 代码 / MGPUSim | 论文和文档里的叫法 | 说明 |
|---|---|---|
| RDMA 引擎（`rdma`） | GPM 网络接口（network interface） | RDMA 是 MGPUSim 的术语，不是通用术语（老板 10-20）；数据中心的 RDMA 网卡是另一回事 |
| `-rdma-max-outstanding` | 远程请求跟踪表（remote request tracker，RRT）的项数 | 网络接口同时能挂的远程请求数 |
| `-t18-window` W | 跟踪表项数 W（网络在途上限） | |
| owner 端合并表 | owner merge table（OMT） | 表项 = 指纹 + 48 位请求者位图 + 状态 |
| HPCA 稿里的 "RDMA-In-Flight" filter 类型 | 请求方在途记录（HPCA 版已有） | |

代码里的 flag 名不改。

## 相对 HPCA 版 m2：哪些已有、哪些新增

| 部分 | HPCA 版 m2（design.tex "Remote Aggregation"） | T18 | 为什么只对 wafer 成立 |
|---|---|---|---|
| 请求方：在途记录 + 同 line 合并 + 同页打包 | **已有**：cuckoo filter 的 RDMA-In-Flight 类型、按页的位图包、owner 解码位图 | 只合并 64 B 整行读，不做同页打包 | —（不再算新贡献） |
| **新 1：请求跟踪表规模化** | 64 项 | 几百到几千项：cuckoo 结构做索引（实现：12 位指纹 + 完整地址，命中必须核对完整地址；表 = 2W），按索引匹配响应，不用全相联 CAM | 需要的项数 = 带宽 × 往返时间 ÷ 64 B，wafer 比 MCM 大一个数量级（见下一节） |
| **新 2：owner 端跨 GPM 合并 + 网内多播** | 只解开同一请求方的页内位图，逐条服务 | owner 在该 line 的 L2 读完成前合并别的 GPM 的同 line 读；返回时在 switch 里按维序路由树复制（实现是 YX）；多播响应只带地址，请求方用跟踪表认领；表满退回单播 | 同一条 line 最多 47 个读者（4 GPM 的 MCM 最多 3 个）；链路利用率 < 3%，多送副本可以接受 |
| （降级）按距离分 credit | — | 开关 `-t18-dist-credits`（按 84 + 71.4 ns × 跳数分）；W ≥ 1024 时和共享池逐位相同，只在 W = 64 / 256 评估 | 通用的队头阻塞问题，作为设计细节 |
| （候选，未实现）owner 故意攒批 | — | 对预测为多读者的 line 等 100–300 ns 攒齐再服务 | 排队占 80% 时可能划算；用户 10-20 暂不加"保留数据"的变体 |

用户 10-20：如果 T18 效果很好且相对复杂，考虑拆成两个机制（合并 + 多播消 incast；大跟踪表）。

**配置变化（用户 10-20，D23；当晚暂缓，仍用 28 CU 的 mi200，下面这段只是备用）**：论文配置改为每个 GPM 14 个 CU（`mi200_eighth`），网络接口跟踪表 baseline 改为 32 项（每个 CU 的项数和 64 项 / 28 CU 相同），扫描 32 / 128 / 512 / 2048；链路 384 GB/s。本文件里 64 项、768 GB/s 的论证按每个 CU 换算后不变（需要的表项 = 带宽 × 往返时间，带宽和表项一起减半，往返时间不变）。

**在论文里的位置（用户 10-20）**：**T18 = 新 M1**（主机制）；M2 = 旧 m2（HPCA 版 Remote Aggregation：请求方同 line 合并 + 同页打包）；M3 = 旧 m3（Remote Reuse，保留但不作为重点）；**Complete = M1 + M2 + M3**，Local Pairing 不进论文。现在 T18 和旧 m2 不能同时打开，要先集成：请求方只留一张表（M1 的规模化跟踪表做同 line 合并，M2 的同页打包接在后面），owner 端先解开 M2 的页位图包再进 OMT。论文里同 line 合并和同页打包算 M2，M1 只算跟踪表规模化和 owner 端合并多播；消融里加"M1 不开请求方合并"。M3 写论文前要补 kernel 边界失效、和 B1 对比。M1 和 M3 互补：M1 的多播把一条 line 同时送到多个请求方，M3 再把它留在各自的 L2。细节见 `../observations/README.md` 第 4 节和 `../prompts/P13_observation_data.md` C1。

## 64 项的依据（回应老板 10-20："真实硬件有没有这个限制或等效限制"）

**来源**：上游 MGPUSim 的 RDMA 引擎**没有**在途上限（`transactionsFromInside/Outside` 是无界切片，只有端口缓冲 1024）。`-rdma-max-outstanding=64`（连同 `-rdma-pipeline-width=8`、`-rdma-pipeline-latency=10`）是我们 fork 在 `a185a7e`（2026-07-11）和 CuPath 远程通路机制一起加的。HPCA 稿 background.tex 引用的 kalia2016design、wei2018deconstructing、mittal2018revisiting 是数据中心 RDMA 网卡的论文，不能支撑片上 GPM 端点的 64 项。

**真实硬件的等效限制**：任何芯片间接口都用有限的表跟踪在途请求（PCIe 的 tag、xGMI / NVLink 的 credit 和请求跟踪），厂商按"填满自己那条链路"来配置。

| | 在途上限 | 在途字节 | 填满自己链路需要（带宽 × 往返） |
|---|---|---|---|
| PCIe Gen4 ×16（约 32 GB/s，往返约 1 µs） | 768 个 tag × 最多 512 B | 约 384 KB | 约 32 KB：tag 富余 |
| MI250X 两个 GCD 之间（200 GB/s） | 厂商未公开 | — | 实测 kernel 直接读写 153 GB/s（77%），折合每 CU 约 1.4 GB/s（Pearson 等 2023） |
| 我们一个 GPM（链路 768 GB/s，往返 155–945 ns） | 64 × 64 B | 4 KB | 约 120–730 KB：差 30–180 倍 |

注意：PCIe 按 tag 个数比（768 对 64）没有意义，要按在途字节和自己链路的带宽 × 往返比。

**定位**：64 项时每 CU 远程带宽约 1.6 GB/s，和 MI250X 实测（约 1.4 GB/s）相当，即"沿用今天多芯片 GPU 的端点设计"。wafer 上链路宽约 4 倍、距离远好几倍，按 Little 定律需要的在途数（100 B/ns 远程带宽时：1 跳 242、平均 4.3 跳 611、6 跳 803、12 跳 1,477 项）大 10–100 倍，用全相联 CAM 做不下去。这就是新 1 的出发点，也是它对 wafer 独特的原因。

**实验写法**：64 作为 baseline 参数，扫 64 / 256 / 1024 / 4096（正式评估已包含）。可选：在真实 MI250X / MI210 上用 kernel 直接读对方显存的微基准，测每 CU 的远程带宽和饱和时的在途数（需要机器）。

**冒烟里的提醒**：W = 1024 和 4096 结果相同；Claude Code 报告 W ≥ 1024 时每 GPM 在途只有 40–75 个（统计口径待核对：是时间平均还是峰值、是否只算网络在途）。论文里说"几百到几千"，不要说"几千"。

## 证明"对 wafer 独特"的实验（未做，改 mesh 尺寸要先做配置确认；数据要求见 `../observations/README.md` 新 O4）

同样的 workload 和机制，在 3×3（8 个 GPM）、5×5（24 个）、7×7（48 个）的 mesh 上跑（CPU 都在中心；可选 2×2，相当于 4 个 GPM 的 MCM），每个 GPM 的工作量不变，画出 baseline 的远程排队占比、需要的跟踪表项数和 T18 的加速比随 GPM 数的变化。收益随规模变大，才能说是 wafer 独特；如果小 mesh 上一样大，就要重新考虑论点。另外做链路 / switch 的敏感性（`-bandwidth` 24 即 384 GB/s、`-switch-latency` 20），确认"端点受限、不是链路受限"不依赖链路参数。两项都改配置，需要用户确认。

## 已有结果

- 动机：远程带宽 = 64 × 64 B ÷ 往返时间（1 跳 26.4、6 跳 8.0 B/周期，T14），链路利用率 < 3%；T17 每个 GPM 有 270–400 个远程读在途，排队占远程延迟 80% 以上（闭环模型）；incast 时 owner 端封顶约 113 B/周期（T14 实测）。
- P12 M1 共享统计（`../results_index/p12/`）：请求方合并能去掉 MoE 15%、R-GAT 34%、FW 49% 的远程读，PagedAttention < 1%；跨 GPM 的重复到 owner 时大多已错开（基线时序下 owner 端合并只有 6–8%）；读同一 line 的 GPM 最近一对 1.5–2.6 跳。
- 冒烟（STATUS "T18 冒烟测试"）：全开 + W=1024 时 FW 8.56×（O1 上限的 77%）、PagedAttention 3.34×、R-GAT 2.49×；FW 只放大窗口 2.26×、只 owner 端几乎没用 → **合并和放大跟踪表要一起才有效**（不合并时 owner 端 incast 让往返停在 3–4 µs）；W=64 时 T18 不如旧 m2（FW 2.13× 对 2.51×，旧 m2 有同页打包）；FW 是截断运行，正式评估要按 T15 的规则核对。

## 还缺的实验

1. 正式评估结果（运行中）。
2. E1 远程延迟分段（只加统计）：等名额 / 去程 / owner 端 / 回程 / 回到 L1V，按跳数分桶；回答排队在请求方门口还是 owner 端，负载下每跳多出的 180–360 ns 在哪一段。
3. 规模实验和链路 / switch 敏感性（见上，需要配置确认）。
4. 跟踪表大小 / 面积：CACTI，W = 256 / 1024 / 4096；OMT 大小按 M1 统计里"每个 owner 同时有多少条 line 被多个 GPM 等着"定。
5. 可选：owner 攒批微基准；真实 MI250X / MI210 的端点测量。

## 需要的统计

合并率（请求方 / owner 端）、跟踪表占用（时间平均和峰值）、多播扇出、指纹误匹配次数、表满退回单播的次数和阻塞周期。

## 前人工作检查

可扩展 MSHR（Tuck 等 MICRO'06，Bloom filter）、credit 流控、NoC 多播（如 VCTM）和 combining network、Cuckoo Directory（Ferdman 等 HPCA'11）、HDPAT（区分点：HDPAT 缓存 PTE，T18 不缓存数据，只管在途请求）。论文 background 的端点限制改引 Pearson 等 2023（arXiv 2302.14827）、Schieffer 等 2024（arXiv 2410.00801，MI250X Infinity Fabric 数据移动）、Pal 等 HPCA'19（wafer-scale GPU，GPM 间带宽和延迟），见 `../ideas_from_T14.md` 10-20 一节。
