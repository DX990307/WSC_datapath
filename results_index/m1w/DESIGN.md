# M1-wafer（m1w）：和 wafer 拓扑有关的 RDMA 端机制，设计说明

Claude Code，2026-10-22。代码在 worktree `~/Desktop/CuPath/wt-m1w`，分支 `isca27/m1-wafer`。先从 `ad0dfe3` 开出，后来 rebase 到新基线 `c08698b`（`isca27/fix-l2tlb-wakeup`）。所有开关默认关；关闭时模拟器不变（回归见 README）。

| 提交（rebase 后） | 内容 |
|---|---|
| `da5a59e` | 上限统计 `-report-m1w-bound`（只读） |
| `436c717` | 原型 1：合并 hub `-m1w-hub-mode region|turn` |
| `859a6a1` | 原型 2：按距离 hold 批 `-m1w-hold-ns` / `-m1w-hold-alpha`；上限统计补上 T18 独立请求方路径的钩子，并在合并点核实时间 |
| `c26d97e` | 配置描述修正（hold 实验用这个二进制） |
| `493a59c` | hub 的时延分解统计 |
| `bce2b0a` | hub 使用独立的网络端口 `M1WHub` |
| `97fdca5` | hub 转发单 line 时用普通的 line 读（修 bug，见 analysis 第 4 节；hub 实验用这个二进制） |
| `6edd8fc` | 自适应 hub `-m1w-hub-adaptive`（响应里带合并反馈） |
| `04115a1` | 自适应权重改为 +8；`-m1w-hold-min-occ`（hold 只在窗口占用达到一定比例时才生效） |

主要文件：`mgpusim/timing/rdma/m1w_bound.go`（拓扑、上限统计）、`m1w_hooks.go`（统计钩子）、`m1w_hub.go`（hub）、`m1w_hold.go`（hold）；`akkalat/baseline/runner/m1w.go`（开关、接线、输出）；配置 `akkalat/configs/v4_complete_hubturn.json`、`v4_complete_hubturna.json`（自适应）、`v4_complete_hubq2.json`、`v4_complete_hold100.json`、`v4_complete_hold100o.json`（窗口半满以上才 hold）、`v4_complete_holdrtt25.json`，最终推荐组合 `v4_complete_hubturna,v4_complete_hold100o`（都 extends `v4_complete.json`）。

拓扑约定：7×7 mesh，中心 tile (3,3) 是 driver / IOMMU，没有 GPM；GPM 编号按行优先、跳过中心（tile = x + 7y）；路由先 Y 后 X；每跳交换机 32 cycle（1 GHz）；往返模型 RTT(h) = 84 + 71.4 ns × h（T14）。

---

## 1. 上限统计 `-report-m1w-bound`

输出 `<metric-file-name>_m1w_bound.csv`，metrics 文件不变。

**输入**：请求方的每个网络事务，包括单 line 读、位图批，以及 baseline 路径的读（legacy 路径，或者 W=256 时的 T18 独立请求方）。记录的内容：请求方 r、owner o、携带的 line（需求还是预取；需求 line 记第一次被需要的时间）、事务创建时间（批的创建时间，或者第一个等待者到达的时间）、发出时间、到达 owner 的时间、完成时间（窗口位释放）。另外记录远程写（作废该 line 的记录）和 kernel 边界（全局；边界之前创建的记录都不再算）。

### (a) 合并

| 变体 | 合并点 | 条件 |
|---|---|---|
| `q2/q3_d{2,3,4}_ret{0,1000,10000}` | 区域 hub：把 mesh 切成 2×2 或 3×3 个区域（7 = 4+3 或 2+3+2），每个区域选一个 hub GPM（到区域内各 GPM 跳数之和最小；平手时选离中心近的） | 读的跳数 > d，并且 owner 不在本区域。hub 的表项从第一个读发出时开始，到它完成时结束；之后再保留 ret ns |
| `turn_ret{0,1000}` | 路由拐点 hub：(x_r, y_o) 处的 GPM。请求 r→H 只走 Y，H→o 只走 X；响应 o→H 只走 X，H→r 只走 Y，所以总跳数和直连一样，没有绕路 | 拐点存在（不是中心 tile），并且不等于 r 或 o。同一列的请求方读同一 owner 行时共用这个 hub |
| `path` | 理想的网内合并：沿 r 的请求路径找第一个交换机，它也在另一个同 line、还在途的请求的路径上（假设响应沿请求路径原路返回） | — |
| oracle r1 / r2 / any | 1 跳内 / 2 跳内 / 任意另一个 GPM 在这条 line 被需要时正在取它 | — |

- **覆盖**（`covered_send` / `covered_need`）：同一 line 的后一个读在发出时（或第一次被需要时）找到前一个读还有效的表项，并且两者不是同一个请求方。
- **核实**（`covered_verified`，`859a6a1` 起）：后一个读要在前一个读的响应经过合并点之前到达合并点：t_send + 35.7 ns × d(r, 合并点) ≤ t_end − 35.7 ns × d(合并点, 前一个请求方)。保留的表项则以响应到达合并点的时间加上 ret 为准。旧二进制（`e714d47`）没有这一项，`path` 因此高估得很多：交汇点靠近 owner 时，真实的合并窗口只有 owner 的服务时间加上几跳。
- **字节×跳**：每条 line 按 112 B/跳计（1 个请求 flit + 6 个响应 flit），和 geo 的统计一致。被覆盖的读只算 r↔hub 的部分；没被覆盖的远读按 r→H→o 计（包含绕路）。
- **链路负载**：每条有向链路上的响应 line 数，取最大值，和直连（先 Y 后 X）比较。
- **窗口位时间**：被完全覆盖的事务，估计的新占用时间 = RTT(d(r,H)) + 10 ns + 前一个表项剩余的在途时间，节省量是它和实际占用时间的差。没被覆盖的远事务要加上绕路的 71.4 ns × 绕路跳数。`slot_net_frac` = （节省 − 绕路代价）÷ 总窗口位时间。

### (b) 按距离 hold 批

对每个事务模拟：同一个（请求方、owner、页）上，一个虚拟批从创建起开放 T 时间（发出时间 = max(实际发出时间, 创建时间 + T)），在它发出之前创建的后续事务并进来，最多 8 条 line。T 有两种：固定的 100 / 300 / 1000 ns（所有 owner），或者 α × RTT(h)，α = 0.25 / 0.5 / 1，跳数 ≤ 2 的 owner 不 hold。统计并掉的事务、每包 line 数、被并掉的事务原本占用的窗口位时间，以及每条需求 line 多等的时间（max(0, 创建时间 + T − 实际发出时间)）。

### (c) 窗口占用按 RTT 加权

按跳数统计事务的窗口位时间和请求腿（发出 → 到达 owner）。如果改为 owner 收到请求就释放窗口位，等效窗口 = 总窗口位时间 ÷ 请求腿时间；如果靠 owner 回 ack 来释放，再除以 2。

---

## 2. 原型 1：合并 hub（`-m1w-hub-mode`）

**请求方**：组合 M1 路径（v4）上的事务（单 line 或位图批）发出时，如果 owner 有对应的 hub（`hubFor[owner]`），并且这批 line 都不是本 GPM 写过的（M3 的 `remoteUncacheable`），消息的 Dst 就改成 hub 的 RDMA；否则直连 owner。窗口位、credit、合并表和统计仍然记在 owner 名下，hub 的响应用原来的请求 ID（`RespondTo`），所以请求方其余的代码不用改（部分位图响应本来就支持）。

**hub**（`m1w_hub.go`）：每个 RDMA 多一个网络端口 `M1WHub`（只在开启时创建，挂在同一个 tile 的 endpoint 上），hub 的请求、转发和响应都走这个端口，不和 RDMA 的 owner / 请求方流量共用 FIFO。端口上收到的读交给 hub 处理：
1. 按 line 查 hub 表：
   - 在途（另一个请求方先来过）→ 挂成等待者；
   - 保留中（`-m1w-hub-retain-ns` > 0，并且还没过期）→ 直接回数据；
   - 都没有 → 新建在途项（表满就不进表，只跟踪这一次转发），并放进转发位图。
2. 缺的 line 由 hub 自己发给 owner（不占 hub 自己的窗口）：只缺一条时用普通的 line 读，缺多条时合成一个位图请求。只缺一条也发位图的话，owner 要走拆包路径，而拆包期间整个入口都被挡住，`c26d97e` 就是因为这个问题变慢的。
3. owner 的 line 回来时，交给所有等待者；每个请求方每次到达回一个响应（单 line 请求回 `DataReadyRsp`，位图请求回部分位图 `BitmapReadRsp`）。
4. 不保留时，line 回来就删表项；保留时，到期或 kernel 边界时删。
5. 输出队列每周期按 RDMA 宽度发送；hub 从不阻塞输入（表满的 line 不进表），所以不会和自己的响应互相等待而死锁。

**hub 的选择**：
- `turn`（主推）：hub = (x_r, y_o) 处的 GPM。拐点是中心 tile、是 r 或 o 本身（同行、同列），或者跳数 ≤ `-m1w-hub-min-hops` 时，直连。
- `region`（对照）：`-m1w-hub-part` × part 个区域，每个区域一个 hub；owner 在本区域内或者跳数 ≤ min-hops 时直连；hub 自己的读也直连。

**自适应**（`-m1w-hub-adaptive`）：hub 的响应带上"这次有几条 line 是合并来的"：位图响应用 `BitmapReadRsp.HubMerged`，单 line 响应用 `M1WHubDataRsp.Merged`（相当于响应头里多 1 位）。请求方对每个 owner 有一个 4 位计数器，有合并就加 8，没有就减 1，低于 8 时直连；直连期间每 16 个事务仍然走一次 hub，用来重新探测。存储代价是 48 × 4 位计数器加 48 × 4 位探测计数。

**一致性**（和 M3 的假设相同）：
- kernel 内无数据竞争；
- 请求方写过的 line 不走 hub；
- 每个 GPM 的 kernel 启动钩子（M3 的 `MarkRemoteReuseKernelBoundary`）会清掉本 hub 的保留项；在途项标为 stale，不再接受新的合并，但已挂上的等待者照常拿到数据。kernel 是全局顺序执行的，到边界时上一个 kernel 的需求读都已经回来了。
- 小规模 `-verify`（bfs s12、sssp s12 r4、npbcg S、graphtc 默认、MM pipeline-smoke 加 `-magic-copy-coherent`）在 turn、q2、hold100、holdrtt25、turn + holdrtt25（`c26d97e`），turn 自适应（`6edd8fc`），以及 turn 自适应 + hold100o（`04115a1`）下全部通过；`97fdca5` 的 turn 测了 bfs、sssp、MM，也都通过（见 `verify.csv`）。

**硬件代价**：每个 GPM 的 RDMA 加一张 hub 表（默认 1024 个 line 项，每项包括地址标签、等待者链头、在途 / 保留状态；保留时还要 64 B 数据），加一个输出队列。请求方不加任何状态，只多一张 48 项的 owner→hub 端口表（静态）。

## 3. 原型 2：按距离 hold 批（`-m1w-hold-*`）

`m1ProcessRemoteBatches`（窗口位空出来时按批的先后发）里，发往跳数 > `-m1w-hold-min-hops` 的 owner 的批，如果还没满 8 条，并且从创建起还不到 T，就跳过，继续看后面的批。T = `-m1w-hold-ns`，或者 `-m1w-hold-alpha` × RTT(h)。drain 时不 hold。近处的批照常立即发。`-m1w-hold-min-occ` > 0 时，只有在途数达到窗口的这个比例才 hold：窗口空闲时 hold 只会增加延迟（sssp 就是这种情况）。只改发送条件，不加存储。

---

## 4. 和相关工作的区别（Cowork 10-21 的文献检查，`isca27/related_work_ideas_1021.md` A2）

- **HMG（HPCA'20）的 GPU home**：每个地址在每个 GPU 内有一个 home GPM（按地址交错），读按"本地 L2 → GPU home → system home"走，GPU home 的 L2 缓存这条 line，并用目录跟踪共享者。我们的 `region` 加保留，本质上就是"区域 = GPU、home = 区域 hub"的 HMG（区别只是 hub 是单点，不按地址交错，而且没有目录），新颖性低，只作对照。上限统计里它的大部分收益来自保留（10 µs），而不是在途合并。
- **Proxies（Euro-Par'96–00）、Virtual Hierarchies（ISCA'07）、DASH RAC**：都是按地址或区域固定的代理 / 一级 home，并且缓存数据。
- **In-Network Coherence（MICRO'06）、Delegated Replies（HPCA'22）**：前者靠交换机里的目录，后者由 LLC 把回复交给最近访问过的核，都需要一致性状态。
- **HDPAT（HPCA'26，同一个 7×7 wafer）**：按象限分簇、地址取模选 GPM，对象是翻译（PTE），不是数据。
- **我们的 `turn` 的不同点**：
  1. 合并点由路由决定（先 Y 后 X 的拐点），对每个（请求方列、owner 行）对是唯一的，请求和响应都不绕路；HMG / Proxies 的 home 由地址决定，通常要绕路。
  2. 只合并在途的请求，不缓存数据，不要目录，不需要一致性协议（只靠 M3 已有的 kernel 边界规则）；表项的寿命就是一次远往返（µs 级），所以"不要求同时到达"：两次读只要相隔不超过一次远往返就能合并。owner 端合并做不到这一点，它的窗口只有 10–20 ns。
  3. 论点是 Little 定律：被合并的读把一次远往返（d(r,o) 跳）换成一次近往返（d(r,H) 跳加上剩下的在途时间），窗口位占用时间缩短，等于放大了在途能力；同时角落 owner 的出口链路少了重复的响应。
- **Kurian 等（HPCA'14）"cluster 大小 1 最优"**：他们比较的是 NUCA 里复制数据的 cluster 大小，代价是容量（每个 cluster 一份副本）和 cluster 内的查找跳数。`turn` 不复制数据，没有容量代价；查找也不在关键路径上额外绕路（hub 本来就在路由路径上），所以他们的权衡不适用。`region` 加保留则正好落在他们的权衡里（要回应的话用 `region` 的绕路代价数据）。
- **按距离 hold 批**：批处理和超时合并本身不新（网卡中断合并、Nagle 算法、M2 自己的打包）。新的是按跳数设定 hold 时间：远 owner 的往返本来就长，多等 α × RTT 只占它的一小部分，换来更少的窗口位、更少的 owner 事务和更少的热点出口包。
