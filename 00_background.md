# 背景：论文现状、审稿意见和决策理由

> 这份是参考资料，说明“为什么”这样做；具体任务见 `tasks/`。最后更新：2026-10-12。
> 状态标记：`[已定]` 用户已拍板；`[提案]` 等用户确认；`[待验证]` 要看实验数据。

## 1. 论文现状

- **论文**：CuPath: Cuckoo-Guided Memory Request Optimization for Wafer-Scale GPUs（HPCA 2027 #1329，第一轮被拒）。
- **原设计**：每个 L2 partition 配一个 typed Cuckoo Filter，充当三个机制共用的“控制平面”：
  - **Remote Aggregation**：在 requester 的 NI 合并同 line 的重复请求，并把同一 page 的请求打包
  - **Remote Reuse**：发现复用之后，把远程 line 保留在 requester 的 L2
  - **Local Pairing**：预测相邻的 64B line 并提前发出 HBM 读
- **平台**：在 MGPUSim 上扩展。48 个 GPM，7×7 mesh（中心是 CPU tile）。
  - 每个 GPM：32 个 CU，4 MB L2（4 个 1 MB partition，16 路，每个 partition 64 个 MSHR），8 GB HBM，0.375 TB/s
  - mesh 链路：768 GB/s，32 周期
  - NI：每周期 8 个 transfer，64 个 outstanding
  - 地址按 128B 在 partition 之间交错
- **关键结果**：
  - 整体 GMEAN 1.47×；三个机制单独开启分别为 1.209×、1.156×、1.108×
  - MM 6.18×、FWS 4.34×、KM 2.50×；MM 只开 Remote Reuse 就有 5.451×
  - BT 和 FWT 约 1.64×，几乎全部来自 Local Pairing
  - 有 7–8 个 workload 的加速比约等于 1.0
- **动机数据**：
  - O1：HBM 和 L2 占本地路径延迟的 96.8%；远程通信占远程路径延迟的 97.0%
  - O2：远程距离 3.0–6.6 跳，延迟 465–7,787 周期
  - O3：同 line 合并机会 25.9%，同 page 打包机会 23.1%（16 周期窗口）
  - O4：17.7% 的远程 line 被复用
  - O5：L2 平均空闲 60.1%
  - O6：5–50% 的本地 miss 在 16 ns 内成对出现
- **硬件开销**：每个 filter 16K 个 21-bit 的槽，每 GPM 合计 168 KiB；全部结构合计 365 KiB/GPM，相对 L2 为 4.70%。
- **workload**：14 个，来自 Hetero-Mark、AMDAPPSDK、SHOC、DNNMark。模拟预算有限，大 benchmark 只能截断运行，每个 CU 只执行前几个 WG。
- **HDPAT [34]**（HPCA 2026）是本组的前作，研究的是地址翻译，同样用了 Cuckoo Filter 和这套 wafer 平台。审稿人 E 把它当成需要对比的 SOTA，新版论文里要讲清楚两者的区别。

## 2. 审稿意见（1 WA + 5 WR）

| 审稿人 | 分数 / 专业度 | 核心意见 |
|---|---|---|
| A | 3 / 了解该领域 | 缺少简单 baseline：远程数据直接缓存进 L2、精确合并、next-line 预取；kernel 边界把所有远程 line 失效，会冲掉只读的权重；缺 CF 和 CBF 的对比；缺规模、网络、placement 的敏感性 |
| B | 2 / 了解该领域 | 新颖性低；读写并发下的一致性没讲；只给了端到端加速比，没有 breakdown（其实有消融，B 可能没看到）；residency region 为什么是固定大小 |
| C | 2 / **领域专家** | ①动机测量和机制对不上：16 周期窗口 vs 实际只合并同一周期；Fig 7 没区分 invalid way 和 dead way；②CF 的语义：既然有假阴性，negative 就不能说“一定不在”；插入失败、bucket 冲突、4-bit 计数溢出怎么处理；每周期 16 次查找加 16 次更新需要多少端口，面积有没有算全；③MI100 配比不对：4 个 GPM 是 128 CU、16 MB L2、1.5 TB/s，而真实 MI100 是 120 CU、8 MB L2、1.228 TB/s；④Fig 15 把 interleaving 宽度和预取行数两个变量混在一起；⑤缺和 DualOpt、MCM-GPU、SAC、selective replication 的对比 |
| D | 2 / 了解该领域 | 用 CF 做预测早有先例（Agarwal HPCA'16、Li MICRO'21、Barre Chord）；**同 line 的请求为什么没在 L2 MSHR 合并掉？**（远程请求从 L1 直接进 NI，绕过了 requester 的 L2，论文里要写清楚）；**一次取 128B 加 sector promotion 就能涵盖 Local Pairing** |
| E | 2 / 了解该领域 | 三个机制都是已知思路；L2-Resident 可以用 bitmap，In-Flight 只有 64 项可以用 CAM，为什么非用 CF；要报告 FPR；缺 MCM-GPU L1.5、AdCoalescer、HDPAT 的对比；需要一张 O→机制的映射图 |
| F | 2 / 比较熟悉 | 只有仿真，参数也是沿用下来的；缺 mesh 带宽和延迟的敏感性；benchmark 是 2010 年代的；ML 实验冷 cache，等于只是复现了 MM 的结果 |

## 3. 决策和理由

| 编号 | 内容 | 状态 | 理由 |
|---|---|---|---|
| D1 | 转投 ISCA 2027，不走 ISPASS | `[已定]` | 用户判断原设计新颖性不足，选择冲顶会，同时改进机制 |
| D2 | 主线从“typed CF 控制平面”换成 wafer-scale 特有的问题 | `[提案]` | 原设计里没有任何东西是 wafer-scale 才有的问题。wafer-scale 的特点是远程代价随距离变化很大，以及大量 L2 闲置、多个 GPM 共享数据 |
| D3 | 机制 1：按距离分区域的远程复制（T7） | `[待验证]` | 把闲置的 L2 变成分布式远程缓存；GPM 内的合并升级为跨 GPM 合并；回应 MCM-GPU L1.5、SAC 的对比要求 |
| D4 | 机制 2：基于写签名的选择性失效（T8） | `[待验证]` | 回应 A 和 B；Bloom filter 没有假阴性，假阳性只会多失效一些行，所以正确性由结构本身保证 |
| D5 | 删除 Local Pairing，baseline 改用 128B 取数 + sector promotion | `[提案]` | D 的问题基本否定了 Local Pairing；主动把 baseline 做强 |
| D6 | 近似结构只用在出错方向安全的地方；In-Flight 改用精确 CAM | `[提案]` | 回应 C 和 E |
| D7 | 冷 cache 的 ML 实验换成多 kernel、迭代型的负载 | `[提案]` | 回应 A 和 F |
| D8 | W1 设为 go/no-go 关口；不通过就考虑 MICRO 2027 | `[提案]` | 新主线依赖跨 GPM 的共享，要先用数据证明这种共享确实存在 |
| D9 | 不使用 Photon 采样 | `[已定]` | 移植难度太大；改用“按预算定规模”加“每 GPM 少放 CU”，并做截断验证 |
| D10 | 按预算决定输入规模（已定）；新增 `cu8` 配置（**10-13 改为提案**：偏离论文配置） | 部分 `[提案]` | 用户规定论文配置不可修改；见 T4 和 CONFIG.md |
| D11 | 第一批 workload：LLM、GCN、DLRM、SSB | `[已定]` | 用户回复“可以试试”；见 T5a–T5d |
| D12 | 工作流：本机改代码，GNU parallel 分发到两台服务器，rsync 拉回结果 | `[已定]` | 只有一份代码，避免版本不一致 |
| D13 | 新 workload 不跑 verify，直接做 timing 模拟；改用模拟统计做合理性检查（kernel 数、footprint、无越界） | `[已定]` | 用户 10-12 的决定：重点是模拟，不做功能校验 |
| D14 | **论文配置不可修改；每个 task 执行前都要做配置确认，并等用户确认** | `[已定]` | 用户 10-13 的决定；见 CONFIG.md |
| D15 | **GPM 改成 1/8 MI300X**：38 CU、32 KB L1、4 MB L2（16 × 256 KB）、16 个内存 channel、每 GPM 约 315 B/周期的 HBM 带宽；1 GHz 下每周期等效；cache line 保持 64B，容量保持 8 GB，不建模 Infinity Cache；全量重跑 | `[已定]` | 用户 10-13 的决定；回应审稿人 C 对 MI100 配比的批评；见 T13 |

## 4. 需要引用并区分的相关工作

| 工作 | 有多接近 | 区分点 |
|---|---|---|
| HMG（Ren et al., HPCA'20） | **最接近** | HMG 的层级由封装边界固定；wafer 上没有封装边界，区域按距离划分，是否走代理也随距离自适应 |
| R-NUCA（ISCA'09）、victim replication | CPU 的 NUCA 复制 | GPU 是 scoped consistency；失效以 kernel 为粒度 |
| CARVE（MICRO'18）、HALCONE、MGPU-TSM | multi-GPU 的远程缓存和一致性 | 都没有考虑距离；失效要么全量，要么基于时间戳 |
| Bulk（ISCA'06） | 用签名做一致性 | 用在 GPU kernel 边界，配合区域副本 |
| MCM-GPU L1.5、SAC、selective replication | 远程缓存 | 按模块或按 requester，不考虑距离；需要实际跑对比实验 |
| Barre Chord、HDPAT、Agarwal HPCA'16 | 用 CF 或 Bloom 做预测 | 不再把 CF 当作卖点 |

## 5. 待决问题

1. 是否接受机制 1 + 机制 2 作为新主线？
2. 区域怎么划（固定大小还是自适应），代理用什么规则选？
3. GPM 内的 Remote Aggregation 是否保留？
4. W1 没通过关口时，退到 MICRO 2027 还是 ISPASS 2027？
5. 单次模拟的预算是多少（周期数或小时数）？
6. 有几个人可以并行移植 workload？
