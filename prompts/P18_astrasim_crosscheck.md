# P18：用 ASTRA-sim 交叉验证网络结论，排除 MGPUSim/akita 网络模型的问题

> 用户 10-21："我想用 astrasim 试试，看看是不是模拟器的问题"。本文件给新开的会话用（原会话窗口太长）。
> 背景：`../README.md` 的"当前进展与 idea 汇总（10-21）"、`../STATUS.md` 10-21 各节、`../results_index/{abl3c384,m2w,dm}/`。

---
继续 CuPath 的 ISCA 2027 工作。全程用中文汇报；任何结果一出来就告诉用户。这个任务是**独立的交叉验证**：不改 MGPUSim / akita 代码，不碰服务器上正在跑的实验。

**背景（要验证的结论）**
平台：wafer-scale GPU，48 个 GPM 在 7×7 mesh 上（中心 tile (3,3) 是 driver/IOMMU，GPM 编号 tile = x + 7y），先 Y 后 X 路由，mesh 1 GHz，16 B flit，每跳交换机延迟 32 cycle（`-switch-latency 32`），交换机缓冲 16 flit（`-switch-buffer-flits=16`），链路 `-bandwidth=24`（384 GB/s 单向）或 `=3`（48 GB/s）。远程读：请求 1 flit、响应 64 B 数据 + 包头 ≈ 6 flit（编码开销 0.25）。每 GPM 的 RDMA 在途窗口 W（默认 64），每 GPM 在途读还被 L1V 封顶在 28 × 16 = 448。我们从 MGPUSim 得到的结论：
1. 384 GB/s 下网络不是瓶颈：baseline 链路平均利用率 1–5%（p99 ≤ 0.30），往返 310–900 ns；吞吐 ≈ W × 64 B ÷ RTT（Little 定律），瓶颈是请求方等窗口位。
2. W 从 64 → 256，baseline 快约 3 倍；W = 2048 时被 448 封顶，384 GB/s 的 mesh 仍然填不满（对分带宽只用到 20–32%）。
3. 48 GB/s 下回程网络排队到 µs 级（例：allreduce 往返 2.3 µs、PR 14.5 µs），链路 1 µs 窗口内到 100%。
4. 热点在角落：PR 的远程读 100% 来自 tile 0，先 Y 后 X 路由让 87% 的出流量走 0→7 一条链路；48 GB/s 下这条链路平均利用率 0.87。
5. 用户的判断：带宽大概率不是瓶颈，和 wafer 相关的是 hop。

**0. 同步**：按 SYNC.md 第 2 节同步文档仓库（`git status`/`git diff` 读未提交的改动，按规则提交、推送 `isca27/docs`）。`results_index/pm/` 下可能有 page-migration 会话未提交的文件，别替它改内容。

**1. 先调研，⏸ 再动手**
- 读 ASTRA-sim（github.com/astra-sim/astra-sim）当前版本的文档和代码：它的定位（分布式训练 / 集合通信 + Chakra ET）、可选的网络后端（analytical congestion-unaware / congestion-aware、Garnet、ns-3）、支不支持 2D mesh、XY/YX 路由、点对点 send/recv、限制在途消息数、链路利用率和排队延迟统计。
- 判断它能验证上面哪几条、不能验证哪几条（例如它不建 GPU cache/MSHR/RDMA 表，可能要用 send/recv + 依赖来模拟"每 GPM 最多 W 个在途请求"）。
- 同时列出更合适的替代或补充：BookSim2、gem5 Garnet standalone（synthetic traffic）、或者一个独立的简单排队模型，说明各自能验证什么、搭建成本。
- 把方案写进 STATUS（工具、版本、怎么建 7×7 mesh 和路由、怎么从我们的数据生成流量、要比较哪些量），**⏸ 停下等用户确认**再安装和跑。

**2. 用户确认后：搭建和校准**
- 装在本机或服务器的独立目录（例如 `~/astrasim-test/`），**不要碰 `~/exp` 和 `~/akita-exp-test/`**，也不要影响正在跑的调度器（服务器内存按 MemAvailable 留 30 GB 余量）。
- 记录版本 / commit、编译命令、配置文件，放进 `results_index/astra/`。
- 先做微基准校准：单对 GPM 在 1…12 跳下的空载往返时间；单链路饱和带宽；和 MGPUSim 的同一测量（`results_index/platform/` 或 T14 的平台数据）对比，差异写清楚并解释来源（包头、flit、交换机流水、仲裁）。

**3. 交叉验证实验**
- 流量：用 `results_index/dm/` 已有的 owner→requester 流量矩阵（`dm/scripts/load.py` 读 `dm/raw/*.jsonl.gz` 的 `pairs` 字段，按 owner 的精确计数缩放；说明这是在途采样估计）和读次数，生成 6 个代表 workload（pr-g24、npbcg-W、sssp-s18e4-r1、bfs-s18e2、ar-pc1、cfd-c172k-b128）的流量；再加均匀 all-to-all 和"47 个 GPM 读角落 tile 0"两个合成模式。
- 条件：384 GB/s 与 48 GB/s；每 GPM 在途上限 W = 64 / 256 / 448（闭环，限制在途数）和不限（开环注入率扫描）。
- 比较：链路平均 / 峰值利用率、往返时间分布、每 GPM 吞吐、热点链路（0→7）利用率、饱和点，和上面 1–4 条的 MGPUSim 结果逐项对照。
- 结论要回答：MGPUSim 的"网络不是瓶颈、瓶颈是 W × RTT"和"48 GB/s 下排队到 µs 级"是否被独立模拟器复现；如果不一致，差异来自哪里（是 akita mesh 模型的问题，还是两边建模假设不同）。

**4. 产出**
- `results_index/astra/{README.md, analysis.md, *.csv, configs/, scripts/}`；STATUS 加一节；SYNC 追加 `[claude-code]` 条目；推送 `isca27/docs`（只推 `isca27/...` 分支，不 force）。不要引用 W = 1024 的旧结果。
---
