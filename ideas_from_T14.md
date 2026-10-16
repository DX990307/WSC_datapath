# 基于 T13 阶段 3 / T14 第一档数据的新想法（全部是 `[提案]`）

> Cowork 侧 10-13 整理。数据来源：STATUS 的“T13 阶段 3 校准”和“T14 第一档平台数据摘要”、`results_index/platform/`。**没有用户确认之前，这里的内容不要实现。**

## 数据里最关键的四个事实

1. **网络链路几乎是空的，瓶颈在端点。** 48 个 GPM 同时做 all-to-all、转置、环形通信时，推算的最大链路利用率都不到 3%；单对 GPM 的远程带宽只有 26 B/周期（1 跳），是链路带宽（768）的 3%。
2. **远程带宽受“在途请求窗口”限制，和往返延迟成反比。** 1 跳 26、6 跳 8 B/周期；推算的在途 line 数约 80–110。远程延迟 = 198 ns 固定开销 + 72 ns/跳（329 对拟合，残差 ≤ 5 ns）。
3. **热点 owner 是硬瓶颈，而且对共享读没有任何合并。** 多对一时，owner 端总带宽在约 113 B/周期封顶，k ≥ 8 之后不再增长；所有读者读同一批 line 和读不同的 line 结果一样。一个热点 owner 大约只能喂饱 4 个全速读者。
4. **远程页的 TLB miss 要多付 732 ns，而且和距离无关**（本地只多 104 ns），推测是去中心 IOMMU 做地址转换。这相当于 7–8 跳的距离。

另外两个**需要先查清**的异常：本地 L2 带宽在 16 个 CU 时见顶、之后反而下降，两套配置几乎一样（理论吞吐差 2 倍）；以及 mi300x 下 HBM 读比写低 24%。

## 想法

### I1. 重新定位问题：wafer-scale GPU 的远程访问是“端点受限”，不是“链路受限”
- 依据：事实 1、2、3。
- 含义：减少链路上的字节数（原 Remote Aggregation 的同 page 打包、各种压缩）几乎没用；真正有用的是 **(a) 减少每字节占用的事务数**，**(b) 缩短往返延迟**，**(c) 分散热点 owner 的负载**。
- 这可以成为论文特征分析部分的核心发现，比 O1–O6 更有 wafer-scale 的特色。
- ⚠️ **前提是端点模型是合理的**（见“风险”第 1 条）。

### I2. owner 端合并共享读（+ 可选的多播响应）
- 依据：事实 3。现在 owner 端没有合并来自不同 GPM 的同一条 line 的请求。
- 做法：在 owner 的 RDMA 入口（或远端 L2 的 MSHR）上，合并同一时间窗口内不同请求方对同一条 line 的读，一次读出，分别（或多播）回复。
- 价值：热点共享数据（模型权重、共享前缀的 KV、热门 embedding、图里的热点顶点）的远程带宽可以接近“读者数”倍提升。实现比 T7 简单，和 T7 互补：T7 把读者分流到副本，I2 让 owner 本身少干活。

### I3. 多 line 远程事务（让“打包”真正占用更少的窗口）
- 依据：事实 2。窗口按事务计数，不按字节计数。
- 做法：发起方把同一 page 里相邻的几条 line（例如 4–8 条）合成一个 RDMA 事务，**只占一个在途槽位**，响应按 line 依次返回。
- 价值：空间局部性好的远程流，带宽最多提高 k 倍。这也正面回应了审稿人 A 的批评（“打包只减少请求，响应仍是独立的”）。原 CuPath 的同 page 打包可以改造成这个机制。

### I4. 用数据给 T7（区域复制）定量
- 依据：事实 2、3。窗口受限时，**距离直接换成带宽**：1 跳的带宽是 6 跳的 3.3 倍。
- 一个分层的视角：本地 L2 副本（129 ns，0 跳）> 区域代理（平均约 1.3 跳，约 290 ns）> home（7×7 上平均约 4.7 跳，约 540 ns）。本地副本省得最多，但每个 GPM 一份；区域代理省大约 60% 的延迟，却只要每区域一份（3×3 区域时容量是本地副本的 1/9）。
- 按延迟算，代理的盈亏平衡命中率大约是 55%（命中省约 245 ns，未命中多付约 290 ns）；但真正的收益更可能来自带宽（缩短往返、分散热点 owner）。
- 可能的设计：**自适应选择副本层级**：私有的热点数据放本地副本，被很多 GPM 共享的数据放区域代理。

### I5. 远程地址转换是一阶开销
- 依据：事实 4。远程 TLB miss 多付 732 ns，比 1 跳远程访问（261 ns）还贵得多。
- 做法候选：在发起方缓存远程页的地址转换；随远程数据一起把转换带回来；使用大页。
- ⚠️ 这和组里的 HDPAT（HPCA'26）高度重合，**作为新机制有新颖性风险**；但作为特征分析（真实 workload 中远程转换占远程延迟的比例）很有价值，也可以说明 CuPath 和 HDPAT 是正交的、可以叠加。

## 风险和先决条件

1. **端点模型的合理性（最重要）。** MI300X 每个 XCD 到 IOD 的接口是 1 KB/周期（16 个 channel × 64 B，CDNA3 白皮书）；我们每个 GPM 的远程能力只有 26–113 B/周期，低了一个数量级。如果审稿人认为端点建得太弱，I1–I4 的结论都会被质疑成“baseline 太弱”。**建议先做一组端点敏感性**：把 `rdma-max-outstanding`、`rdma-pipeline-width` 放大 2×、4×，看“端点受限”的结论是否依然成立、成立到什么程度。这属于偏离论文配置的附加实验，需要用户确认。
2. **先查清瓶颈到底在哪里。** 远程带宽卡在哪个结构，现在只有推算没有证据（模拟器没有 RDMA 表占用率的统计）。需要加统计（T14 第二档第 9 项“远程延迟分解”提前做）。
3. **本地 L2 带宽异常没查清**，阶段 4 的全量重跑已经在跑。如果是模拟器的人为瓶颈（比如 L1 到 L2 的互联或者地址转换的争用），所有本地路径的结论都会受影响。建议**和阶段 4 并行做诊断**（只加统计，不改数值）。


---

## 10-16 补充：如果不用 Local Pairing，本地路径还能做什么（全部 `[提案]`）

背景：mi200 下 m1 只剩 1.03（hpca 的 1.19 主要来自 128 B 访问单位）；只有 spmv 有约 1.08–1.10 的延迟隐藏收益。Local Pairing 也是三个机制里最不“wafer 特有”的一个，审稿人 D、E 都攻击过它。

### L-A. 不再做本地路径的机制（推荐）
论文只保留 2–3 个和 wafer 直接相关的远程侧机制：多 line 远程事务（I3，由原 Remote Aggregation 演进而来）、owner 端合并（I2）、按距离分层的副本（I4/T7）。理由：机制少而有针对性，比“三个已知想法的组合”更能回应新颖性的批评；本地预取和 DRAM 调度已有大量工作，不是这篇论文能赢的地方。

### L-B. owner 端的服务优先级（把本地硬件用在 wafer 问题上）
- 依据：远程带宽受在途窗口限制，和往返延迟成反比；owner 端在多对一时约 113 B/周期封顶。owner GPM 的 L2 和 HBM 同时服务本地 CU 和远程请求方，而远程请求的往返时间是本地的 2–6 倍。
- 做法：在 owner 的 L2 和 DRAM 调度里给远程请求更高的优先级（或者按请求方的距离加权），缩短远程请求在 owner 端的排队时间；可以和 I2（owner 端合并）合成一个“owner 端服务”机制。
- 需要的数据：远程延迟分解里 owner 端排队占多少（T14 第二档第 9 项）。如果 owner 端排队占比很小，这个方向就不成立。

### L-C. 按重取代价管理 L2（距离感知的替换和分区）
- 依据：远端数据的重取代价随距离变化很大（本地 HBM 约 170 ns，12 跳约 1050 ns，相差 6 倍）；mi200 每 GPM 只有 2 MB L2，本地数据、m3/T7 的远程副本、以及作为 owner 服务别人的数据要抢同一块容量。
- 做法：替换时考虑重取代价（远的远程 line 更晚淘汰），或者按代价动态划分本地数据和远程副本的容量。这也回答了审稿人 B 的问题：为什么 residency region 是固定大小、能不能动态调整。
- 新颖性风险：CPU 的 NUCA 和代价感知替换已有工作，区分点是 wafer 上的代价差别更大、而且和副本层级的选择绑在一起。
- 需要的数据：L2 淘汰中远程副本和本地数据各占多少、被淘汰的远程 line 后来被重取的比例和距离分布。

### 不建议的方向
- 通用的本地预取器、DRAM 调度（FR-FCFS、开页）：属于把 baseline 做强，不是新贡献。
- spmv 那种延迟隐藏可以在论文里作为一个小观察提一句，不单独成为机制。


---

## 10-16 讨论：placement 很好、大部分访问在本地时，该优化本地还是 wafer 层？（提案）

用户的担心：现在的 page placement 让大部分访存发生在本地，所以要么不得不做本地优化；要么先把本地做好、再做 wafer 层优化，但担心 wafer 层的优化空间不够。

Cowork 的看法：
1. **要看“远程占用的时间”，不能只看“远程访问的个数”。** 远程访问的延迟是本地的 2–6 倍，单 GPM 远程带宽只有本地 HBM 的约 1/10，所以 5–10% 的远程访问可能占到一半的访存停顿时间。
2. **本地做得越好，wafer 层的空间反而越大**（Amdahl）：本地时间缩短后，远程时间的占比会上升。修 baseline 的本地问题（例如 mi200 HBM 读只到 31%、L1→L2 前端随 WG 数增加而下降）属于 baseline 的真实性，不算贡献，但会让 wafer 层的问题更突出。
3. **“大部分访问在本地”是旧 benchmark 的性质**：经典 kernel 按 WG 连续切分数据，天然适合分片放置。现代负载（张量并行、MoE 专家并行、GNN、embedding、共享前缀 attention）存在 placement 消除不了的跨 GPM 共享，这正是 T10 的出发点，也是审稿人 F 要求的。
4. **用 oracle 实验直接量出空间，再决定方向**（便宜、只改参数、短片段）：
   - **O1 全部本地**：`-force-local-data-access`（L1V miss 一律走本地 L2/DRAM；地址转换不变）。O1 相对 baseline 的加速比 = wafer 层优化的上限（不含地址转换）。
   - **O2 端点不受限**：把 L1V 每 CU 在途数、RDMA 在途上限放到很大，看上限里有多少来自端点窗口。
   - **O3 距离为 1 跳**（如果模拟器方便实现）：看上限里有多少来自距离。
   - 对 14 个现有 workload 和之后的现代负载各跑一次（mi200），给出每个 workload 的上限。
   - 判断标准（建议）：如果现代负载上 O1 的几何平均上限明显大于 1.5×，wafer 层的论文成立；如果连现代负载都只有 1.1–1.2×，就要认真考虑本地方向或者换题。
5. 纯本地优化的论文就不再是 wafer-scale 论文，要和大量 GPU 访存优化的工作竞争，新颖性风险更高。


---

## 10-16 讨论：能不能用更好的 thread（WG）placement 来利用 wafer 的特性？（提案）

**可以做，但要先过两关：新颖性和上限。**

### 已有工作（必须区分）
- Milic et al., MICRO'17 “Beyond the Socket: NUMA-Aware GPUs”：first-touch 页放置 + 按局部性的 CTA 调度。
- Khairy et al., MICRO'20 “Locality-Centric Data and Threadblock Management for Massive GPUs”（原论文 [16]）：数据和 threadblock 的协同放置。
- CODA（TACO'18，原论文 [17]）：计算和数据协同放置；Griffin（HPCA'20）：页迁移。
- 这些工作面对的是 4 个左右、距离差不多的模块。

### wafer 特有的角度（可能的新颖性来源）
1. **二维 mesh 上的距离差异大**（198 ns + 72 ns/跳，最远 12 跳）：把共享数据的 WG 组放到物理上相邻的 tile 上，是一个拓扑映射问题，而不只是“放在数据所在的模块”。例如：现在 GPM 编号按行优先排，相邻的数据段在换行处会相距 7 跳（行尾 x=6 到下一行 x=0）；按蛇形或 Hilbert 曲线排就能避免。
2. **端点和热点 owner 才是瓶颈**（owner 约 113 B/周期封顶）：placement 的目标不只是缩短距离，还要把读同一个 owner 的 WG 分散开，或者把热点数据分散到多个 owner。
3. **在 wafer 上搬计算比搬数据便宜得多**：kernel 边界只要约 1 µs，而跨 wafer 搬数据又慢又占端点窗口。迭代型和多 kernel 的负载可以让下一个 kernel 的 WG 跟着上一个 kernel 写出的数据走（producer–consumer 亲和调度）。

### 上限：T15 已经能给出来
- placement 最多把远程变成本地，所以收益不会超过 **O1**；它主要缩短距离，这部分的上限大约是 **O3**（`-switch-latency=1`）。如果 O1、O3 在现代负载上都很小，placement 方向就不值得做。

### 便宜的初步实验（建议加进 T15，需要改少量代码）
- **P1 对齐**：WG 的切分和页的切分按同一规则（现在 WG 按 CU 数切、页按 48 等分，CU 数一变两者就错开）。
- **P2 拓扑顺序**：GPM 按蛇形或 Hilbert 顺序编号，让相邻的数据段落在物理相邻的 tile 上。
- **P3 小 buffer 交错**：小于 48 页的 buffer 按页交错到多个 GPM，不再整块放在 GPM 1（KM 的热点）。
- 这三项本身是修 baseline 的放置问题，大概率不算新贡献，但能告诉我们 placement 这条路的空间有多大。

### 10-16 补充：定位、O4 和 placement 以外的路（提案）
- **哪些负载可能受益**：AES、BT 这类本来就几乎全在本地的负载，placement 和任何 wafer 级优化都帮不上（ablation 已经显示）。可能受益的只有远程占比高的：pagerank 87.9%、I2C 57.9%、MT 49.5%、SPMV 26.9%，以及 mi200 下的 KM。
- **只缩短距离，收益有限**：远程路径受窗口限制（64 个在途请求 ÷ 延迟），而缩短距离只能省掉每跳 72 ns，198 ns 的固定开销还在。实测单对带宽 1 跳 26、6 跳 8 B/周期，距离减半大约带来 1.5–2×。量级上的收益只来自把访问变成本地。
- **定位建议：把 placement 做成强 baseline，不当主贡献**。用 P1–P3 加亲和调度搭一个距离感知的放置 baseline，再证明剩下的远程流量是负载本身决定的（pagerank 的 gather、转置），而机制在强 baseline 上仍有收益。这样能直接回应“收益来自弱 baseline”（KM 热点、MM 不缓存远程数据）的质疑。
- **O4 放置 oracle**：离线从 trace 统计每个 WG 访问了哪些页，在“每个 GPM 分到的 WG 数相同”的约束下，把每个 WG 放到加权跳数最小的 GPM 上，作为 placement 能做到的上限。WG→页 的统计可以参考 `akkalat/analyze_wg_mapping.py`。
- **placement 以外，wafer 上能提性能的路**：按免费资源来想，链路有 97% 空闲、L2 有 60% 闲置；贵的是延迟和端点窗口。
  - a. **用带宽换延迟**：owner 端合并、多行事务、批量推送（I2、I3）。push 模式最近的已有工作是 GPS（MICRO'21）。
  - b. **远程翻译（I5）**：一次远程 TLB miss 多出 555–732 ns，比一次远程访问本身还贵，而且和到中心 IOMMU 的距离有关，这是 wafer 特有的问题。最近的已有工作是 Trans-FW（HPCA'23）。
  - c. **空间/流水线执行**：把 wafer 分成几个区域，各自跑不同的 kernel 或层，相邻区域之间流式传数据。这条最有 wafer 特色，但驱动要支持多个 kernel 并发跑在不同的 GPM 子集上，工程量大，在 abstract 截止前（约 3.5 周）风险高。
- **建议**：O4 和 P1–P3 跟 T15 的 O1、O3 一起跑，用数据来决定方向（等用户确认）。

## 10-16 讨论：placement 以外，还有哪些办法能利用 wafer 特性、提升性能？（提案）

思路：先找出 wafer 上**贵**的资源和**闲**的资源。贵的是远程延迟、每个 GPM 的在途窗口、热点 owner、中心 IOMMU 和全局同步；闲的是链路（97%）、L2 容量（60%），还有提前跑完的 GPM。

| # | 瓶颈（证据） | 办法 | wafer 特有吗 | 最近的已有工作 | 先要的数据 |
|---|---|---|---|---|---|
| W1 | **位置导致的负载不均**：角落 GPM 到其他 GPM 平均 6.1 跳（远程约 639 ns），靠近中心的只有 3.7 跳（约 465 ns），慢 1.37×；到中心 IOMMU 的距离也不同（+555 到 +732 ns）。kernel 时间由最慢的 GPM 决定 | 按位置分配 WG（角落少分），或者让邻居之间互相偷 WG；也可以按距离给在途窗口（Little's law：需要的窗口和延迟成正比） | **是**：MCM 只有 4 个模块，距离差不多，不会出现系统性的位置差 | NoC 文献里有“中心和边缘不公平”的观察；GPU WG 分配上是否有人做过，**需要查** | 每个 GPM 的结束时间和它在 mesh 上位置的关系；kernel 尾部的空闲比例 |
| W2 | **窗口受限**：远程带宽 ≈ 64 个在途请求 ÷ 延迟，链路几乎空着 | 多行事务和批量传输（I3）；按距离调整预取深度；owner 在 kernel 开始时把数据推给消费者 | 部分是：距离越大越需要 | GPS（MICRO'21，push）、AdCoalescer | 远程请求里同一 page 连续访问的比例；在途窗口满的时间占比 |
| W3 | **incast**：owner 约 113 B/周期封顶，共享读不合并 | owner 端合并 + 多播响应（I2），或者区域代理（T7/I4） | 部分是 | HMG、CARVE | 每个 owner 的并发读者数分布 |
| W4 | **远程翻译**：一次远程 TLB miss 多 555–732 ns，比一次远程访问还贵；48 个 GPM 都要走中心 IOMMU | 让 owner 自己做地址转换（请求带虚地址）、分区域的共享 TLB，或者对远程区域用大页 | **是**：中心 IOMMU 既是热点，代价也随距离变 | Trans-FW（HPCA'23）、HDPAT | 远程访问里 TLB miss 的比例；中心 IOMMU 的排队情况 |
| W5 | **全局同步**：kernel 边界约 1 µs，经过中心，迭代型和多 kernel 的负载会反复付这笔开销 | 区域级、按依赖的 kernel 边界：一个 GPM 等它的 producer（通常是邻居）完成后就开始下一个 kernel，不用等全局 | 是：全局同步的代价随直径增长 | BlockMaestro（ISCA'21） | kernel 边界占总时间的比例（截断运行下可能失真） |
| W6 | **空间/流水线执行**：小 kernel 撑不满 48 个 GPM | 把 wafer 分区并发跑不同的 kernel 或层，邻居之间流式传数据 | 是 | Cerebras 的 dataflow 和 layer pipelining | 驱动是否支持多 kernel 并发跑在不同的 GPM 子集上（工程量大） |

**baseline 本身的问题（先修，不算贡献）**：mi200 的 HBM 读只到峰值 31%（怀疑和每个 slice 32 个 MSHR 有关）；KM 的小 buffer 全放在 GPM 1；MM 不缓存远程数据。这些会同时扭曲“本地型/远程型”的分类和收益。

**可能的论文故事（提案）**：wafer-scale GPU 受限于**端点和位置**，而不是链路。三个机制分别对应：W2 窗口（多行事务）、W3 热点 owner（owner 端合并）、W1 位置不均（位置感知的 WG 分配）。W4 可以做成特征分析，或者做成第四个机制。每个机制都有 T14 的数据支撑，比 HPCA 版三个通用机制更贴 wafer。

**建议的下一步**：新建一个瓶颈分解任务（T16），**不改机制、只加统计**，和 T15 一起跑：
1. 每个 GPM 的结束时间和位置的关系（W1）
2. 远程延迟分解成翻译、窗口排队、网络、owner L2/DRAM 四部分（W2、W4）
3. 在途窗口满的时间占比，以及同一 page 连续远程访问的比例（W2）
4. 每个 owner 的并发读者数（W3）
5. kernel 边界占总时间的比例（W5）
6. HBM 只到 31% 的原因（baseline）

### 10-16 更新：W1 进入 T16（阶段 1 测量）
用户决定先试 W1。读代码时发现：`-max-wg` 是全局计数，到了就直接退出，所以现有的短片段测的是吞吐，**看不到 kernel 尾部**。W1 的收益只能在跑完整个 kernel 的运行里体现。详见 `tasks/T16_position_imbalance.md`。

### 10-16 更新：W1 的已有证据（T14 原始结果）
T14 的 all-to-all 每个 kernel 都跑完了，`metrics.csv` 里有每个 GPM 的 `kernel_time`：角落 108.5 µs、中心附近 75.9 µs，最慢/平均 1.23、最慢/最快 1.43，和平均跳数的 Spearman ρ = 0.93。环形模式按 tile 编号相连，行尾（7 跳）和回绕（12 跳）让整个 kernel 慢了约 4 倍。真实 workload 目前还没有数据：截断运行里每个 GPM 的 `kernel_time` 都是 0。详见 T16 的“已有的证据”。

### 10-16 讨论：W1 需不需要新 workload？（提案）
- **需要，理由有三**：
  1. 审稿人 F 认为 benchmark 太旧。
  2. W1 只在“远程多、目标分散、kernel 能跑完”的负载上才明显，而旧 benchmark 大多是本地型，远程型的又只能截断运行。
  3. 现代的分布式负载天然就是 all-to-all：MoE 的 dispatch/combine、embedding 查表、TP 的 all-gather/reduce-scatter、图计算的 gather。它们也天然由很多短 kernel 组成，每个 kernel 都要等最慢的 GPM。
- **写法要先定**：W1 的机制（M-a 加权切分、M-b 邻居偷 WG）都作用在 driver/CP 的 WG 分配上，只对“整个 wafer 当一个 GPU、一个 kernel 覆盖 48 个 GPM”的写法（T5 那种 unified）有效。T10 的 SPMD 写法里，每个 GPM 各自 launch，不均要靠应用自己重新分片，就不算体系结构贡献了。**建议用来评估 W1 的新 workload 用 unified 写法。**
- **建议的最小集合**（规模按“kernel 能在模拟预算内跑完”来选；形状用公开的标准配置，不为机制挑参数）：
  1. **MoE 层**：gating → dispatch（all-to-all，而且 expert 热度不均）→ expert GEMM → combine。
  2. **DLRM embedding 查表**：随机 gather，接近均匀的 all-to-all 读。
  3. **图计算 PR/BFS**：GAP Kronecker 图，不规则 gather。
  4. 可选：**TP 的 LLM 层**（all-gather/reduce-scatter，规则的集合通信）。
- **旧 benchmark 保留**：远程型的 PR、SPMV、MT、I2C 用小输入；AES 作为本地型对照，用来说明 W1 对本地型负载没有坏处。
- **时间**：每个 workload 1–3 天（Claude Code），4 个大约 1–1.5 周。可以和 T15、T16 并行，不用等 T16 的远程比例扫描。

### 10-16 讨论：找“最新的、page placement 帮不上忙”的 workload（提案）
**标准**：访问由数据决定（间接寻址、路由、采样），**任何静态的页放置都没法让它变成本地访问**。用 T15 的 O4 放置 oracle 来证明：即使用最优放置，远程比例仍然很高。来源要是 2023 年以后公认的应用或 benchmark，输入的分布用真实数据（长度分布、路由分布、真实图），只把规模缩小到模拟预算之内。

| 优先级 | workload | 来源 | 为什么放置帮不上 | 对应的机制 |
|---|---|---|---|---|
| 第一档 | **MoE 的 dispatch/combine + expert GEMM** | DeepSeek-V3 的配置（hidden 7168、top-8）、DeepEP；Mixtral | token 路由到哪个 expert 由数据决定，本质是 all-to-all；热门 expert 形成热点 owner | W1、W3、W2 |
| 第一档 | **PagedAttention decode** | vLLM（SOSP'23） | KV cache 按块从池里分配，同一个序列的块散落在各处，只能靠 block table 间接访问；序列长度不一，负载天然不均 | W1、W2 |
| 第一档 | **R-GAT / GNN 采样聚合** | MLPerf Inference v5.0 新增的 RGAT（IGBH 数据集） | 邻居采样是随机的，聚合时要从巨大的特征表里 gather | W2、W3 |
| 第二档 | **CAGRA 向量检索** | cuVS（RAPIDS）、arXiv 2308.15136 | 沿 kNN 图游走，随机读数据集里的向量；RAG 的核心算子 | W2、W1 |
| 第二档 | **DLRM embedding bag** | MLPerf 推荐任务 | 按幂律随机查表 | W3 |

- **和原计划的关系**：T5a（共享前缀的 LLM decode）改成 PagedAttention；T5b（GCN arxiv）改成 R-GAT 的采样聚合；T9 的 MoE 提到第一档；T5c（DLRM）放到第二档；T5d（SSB）先不做。
- **写法**：统一用 unified（一个 kernel 覆盖 48 个 GPM）；分配器用现有的 48 等分，不为 workload 特意调整。
- **相关工作提醒**：arXiv 2511.02132（Optimizing Attention on GPUs by Exploiting GPU Architectural NUMA Effects）在 MI300X 上把 attention 的 WG 按 head 映射到同一个 XCD。它和 W1/placement 相关，要引用并说明区别。
- **工作量**：第一档 3 个，每个 2–3 天，大约 1–1.5 周。

### 10-16 补充：模拟器能不能跑（Cowork 读代码，分支 `isca27/t13-mi300x`）
**模拟器的硬约束**：
1. **不支持任何 global atomic**：emu 的 `alu_flat.go` 只实现了 FLAT load/store（opcode 16–31），timing 的 `vectormemoryunit.go` 遇到其他 FLAT opcode 直接 panic。`flat_atomic_*` 只在解码表里出现，没有实现。存储层次和远程通路也都没有 atomic 语义，补上属于大改动。
2. **不支持 MUBUF/scratch**：寄存器溢出到 private memory 会 panic。kernel 不能用 private 数组，也不能有溢出，候选表、top-k 之类的缓冲要放在 LDS 里。
3. **LDS 只实现了 7 条 DS 指令**（opcode 13、14、54、55、78、118、119，也就是 b32/b64 的读写和 read2/write2）。没有 LDS atomic，没有 `ds_bpermute`，所以不能用 sub-group 函数，`ds_write_b64` 也没实现。
4. 已经实现的：`v_exp/log/rcp/rsq/sqrt_f32`。仓库里已有能跑的 gemm、row_softmax、layernorm、gelu、embedding 等算子（`benchmarks/LLMbenchmarks/operators/native/`），以及一个 kvcache 的访存代理 benchmark（`benchmarks/llm/kvcache`）。
5. 编译链：OpenCL C → `clang -target amdgcn-amd-amdhsa -mcpu=fiji -mcode-object-version=2`（见 `microbench/platbench/build_hsaco.sh`，在 yss2 上能编译）。HIP 路线没有验证过。
6. 预算：W1 要求 kernel 跑完，不能用 `-max-wg`。每个 workload 的总 WG 数要控制在现有短片段（约 3 万个 WG）的量级。

**由此得出的写法原则**：所有 kernel **不用 atomic，统一用 pull 方式**。需要 atomic 的索引结构（路由表、采样子图、KV 块表）**在 host 端预先算好**，GPU 只做 gather/计算/写回。这和真实系统里“小的索引 kernel + 大的数据 kernel”的分工一致，论文里要写明。代价是 scatter-add 这类写方向的 atomic 模式测不了，要列为局限。

| workload | 能不能跑 | 改写方式 | 风险 |
|---|---|---|---|
| **PagedAttention decode** | **高** | 一个 WG 对应 (序列, KV 头)；读 block table → gather K/V 块 → QK、softmax（exp）→ 加权求和 V；长序列用 split-K，再加一个 reduce kernel（FlashDecoding 的做法，不用 atomic）。块分配在 host 端模拟分配器反复分配释放后的状态 | 低：算子大多已有 |
| **MoE（DeepSeekMoE 16B 那样的配置缩小）** | **中高** | gating（GEMV + softmax + top-k 放在 LDS）在 GPU 上算；**按 expert 分组的排列索引在 host 端算**；dispatch = 按索引 gather；expert GEMM 按段做；combine = 每个 token 拉取自己 top-k 个 expert 的输出再加权（pull 方式） | expert 权重很大，要缩小 hidden 和 expert 数，只跑 1 层 |
| **R-GAT 风格的采样聚合** | **中** | 邻居采样在 host 端做（预先生成 mini-batch 子图的 CSR）；每个目标点由一个 WG 拉取邻居特征，算 attention 分数、softmax、加权求和；每种关系各跑一遍 | IGB 的小版本是否够大、能不能下载需要核实；异构图会增加实现量 |
| DLRM embedding bag（只做推理） | 高 | 每个 bag 一个 WG，pull 方式 gather 再求和；不做反向 | 低 |
| CAGRA 向量检索 | 中低 | 候选表和 visited 集合都要放在 LDS 里，不用 LDS atomic（原版用 shared memory 的 atomicCAS 做 hash） | 实现复杂，容易溢出，建议推后 |

**第一步（相当于 T1，一直没做）**：每个 workload 先写最小的 kernel，用小输入在 emu 和 timing 下各跑一次，把 panic 的 opcode 列出来。缺的如果只是 ALU 指令，补上的工作量小（属于改模拟器，需要用户批准，并做回归）；缺的如果是 atomic 或 scratch，就改写 kernel 绕开。

### 10-16 补充：模拟速度预算（用户要求每个实验 3–5 小时）
**已有的速度数据**（单个进程，yss2/yss3；wall 时间来自 meta.json 和 STATUS）：
- T14 微基准（hpca）：本地带宽测试约 25–30 MB/分钟（1.5–1.8 GB/小时）；全 wafer 的远程通信（all-to-all、环）约 3–4 MB/分钟（200–250 MB/小时）。每个进程启动有约 2–3 分钟的固定开销。**远程每字节的模拟代价大约是本地的 7 倍。** 和 Claude Code 的估计一致：all-to-all 总流量 577 MB 时约 3 小时。
- mi200 短片段（32768 个 WG）baseline：中位数 48 分钟；PR 1.5 小时、AES 1.7 小时、MM 1.9 小时、MT 3.8 小时；**KM 12.5 小时，超出预算**。同样的 WG 数，耗时能差 10 倍以上，所以不能按 WG 数定规模，要按访存量（尤其是远程访存量）来定。
- Complete 等变体可能比 baseline 慢（内存也涨得更快），oracle 的 O1 多半更快。

**粗略的估算模型（只能作为起点，要用试跑校准）**：wall 分钟 ≈ 3 + 远程 MB / 3.5 + 本地 MB / 27。baseline 目标 3 小时（给慢的变体留 1.5 倍余量，上限 5 小时）：大约是**远程流量 ≤ 0.5 GB，或者本地流量 ≤ 4 GB**（两者按比例折算）。

**新 workload 的初始规模建议**（fp32；要能完整跑完；footprint 要超过 wafer 的 L2 总量 96 MB）：
| workload | 初始规模 | 主要流量（估） |
|---|---|---|
| PagedAttention decode | Llama-3-8B 的注意力形状（32 个 Q 头、8 个 KV 头、d = 128），1 层；batch 32，长度按真实分布，平均约 1K token；KV 块 16 token | KV 约 256 MB，大部分是远程 |
| MoE | DeepSeekMoE 16B 的结构按比例缩小：hidden 1024、expert 中间维 704、64 个 expert、top-6；1 层；batch 256 个 token | expert 权重约 550 MB + dispatch/combine 约 10 MB（远程）。**注意：decode 时 MoE 主要在读权重，远程比例可能不高**。权重是不是本地，要看 expert GEMM 的 WG 区间和权重页对不对得上；expert 负载不均会让两者错开。用 O4 实测，结果不理想就改用 prefill（token 多一些，远程比例更高，但计算量也更大） |
| R-GAT 风格的聚合 | 512 个种子点，fanout (10, 5)，特征 1024 维 | 约 3 万个点 × 4 KB ≈ 120 MB 的随机 gather |
| DLRM embedding bag | 26 张表 × 100 万行 × 128 维；batch 2048，每次查表 pooling 约 20 行 | 约 500 MB 随机读 |

**流程**：每个 workload 先按 1/8 规模试跑，用心跳（`-heartbeat-file`）测出每分钟完成的 WG 数和远程/本地流量，线性外推出 baseline 约 3 小时的规模，再定下来。超过 5 小时的要缩小规模，不用 `-max-wg` 截断（W1 需要跑完整个 kernel）。旧 benchmark 里的 KM 也要按这个规则缩小。
