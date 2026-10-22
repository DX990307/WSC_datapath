# geo-push（地理广播响应）设计

Claude Code，2026-10-21。代码在 worktree `~/Desktop/CuPath/wt-geo`，分支 `isca27/geo-push`（从 `4235991` 开出）：

- `8a30511`：上限统计 `-report-geo-push-bound`（只读，默认关）；
- `6b44a5a`：原型 `-geo-push-radius`、`-geo-push-filter`（默认关）。

主要文件：`mgpusim/timing/rdma/geo_topo.go`（拓扑、邻居、多播树）、`geo_bound.go`（上限统计）、`geo_push.go`（机制）、`geo_comp.go`（钩子）；`akita/mem/cache/writeback/remote_replica.go`（L2 的推送回填）；`akkalat/baseline/runner/geo.go`（开关、接线、输出）；配置 `akkalat/configs/v3_complete_geo1.json`、`v3_complete_geo1s.json`、`v3_complete_geo2.json`（都 extends `v3_complete.json`）。

## 1. 想法

owner 回复请求方 R 时，把这条 line 顺带推给 R 周围 1–2 跳内的 GPM，存进它们的 L2。依据是 `results_index/dm/` 的区域性共享：读同一条 line 的 GPM 多数是邻居。邻居之后要读这条 line 时，就在本地 L2 命中，省掉一次远程往返（平均约 11 跳往返）和一个窗口位 × RTT。代价是推送多走的链路。

## 2. 触发（owner 端）

- **哪些响应**：owner 发出的所有读响应都是候选，包括
  1. M1 owner 表的单播响应（`t18OwnerSend`，`DataReadyRsp`）；
  2. M1 合并后的多播响应（`T18MulticastRsp`，多个 R）；
  3. M2 位图响应（`BitmapReadRsp`，一页里的多条 line）；
  4. 走 baseline 路径的整行读响应（owner 表满或同一请求方重复时的回退）。
- **推给谁**：每个 R 周围 `radius`（1 或 2）跳内的 GPM 的并集，去掉所有 R、owner 自己，以及中心 tile（不是 GPM）。7×7 网格上 1 跳平均约 3.4 个邻居，2 跳约 9.3 个。
- **过滤** `-geo-push-filter`：
  - `none`：每个响应都推；
  - `shared`：owner 用一张小表（`-geo-push-filter-entries`，默认 1024 项、4 路组相联、LRU）记录每条 line 第一个读者，以及之后有没有另一个不同的 GPM 读过。只推表里标记为"≥2 个不同 GPM 读过"的 line。所有到达 owner 的远程读（单行、位图里的每条 line）都更新这张表，在进入 owner 处理之前更新。
  - 位图响应里只推通过过滤的 line；但包的大小沿用原响应（flit 数按原包算，偏保守）。

## 3. 多播树

- 一个响应和它的推送作为**一个包**（`GeoMulticast`）发出，复用 M2 的网内多播：交换机按每个目的地的单播路由（先 Y 后 X）把目的地按输出口分组，在分叉处复制 flit（`switch.go` 的 `routeMulticastFlit` / `splitMulticast`），所以树上每条链路只走一次。
- `GeoMulticast.SplitForDsts`：
  - 只剩请求方 → 还原成原来的响应（原来是 `T18MulticastRsp` 的继续按它自己的规则拆）；
  - 只剩一个邻居 → `GeoPushMsg` 叶子；
  - 混合 → 更小的 `GeoMulticast`。
- 包大小 = 原响应 + 6 B（48 位目的地位图；原来已经是 T18 多播的不再加）。
- 额外链路 = 树（owner → R ∪ 邻居）的链路数 − 树（owner → R）的链路数，记在 `geo_push_owner_extra_links`。

## 4. 邻居收到推送（`GeoPushMsg`）

每条 line 按顺序判断：

1. 本 GPM 没有 M3（请求方 L2 不保留远程 line）→ 丢弃；
2. **过期**：数据在 owner 读 L2 的时间（`Origin`，取包里最早一条 line 的 owner L2 读发出时间）不晚于本 GPM 最近一次 kernel 边界 → 丢弃；
3. 本 GPM 写过这条 line（M3 的 `remoteUncacheable`）→ 丢弃；
4. **和自己在途请求的交互**：本 GPM 有这条 line 的表项（当前写纪元）时
   - 表项已经有数据（`Ready`）→ 丢弃（已经有了）；
   - 表项跨过了 kernel 边界（`boundaryStale`）→ 丢弃；
   - 表项正在探测自己的 L2 → 丢弃；
   - 表项没有等待者（只是预取）→ 丢弃；
   - 否则（在途或还在排队等窗口，有 L1 等待者）→ **"推送赶上在途请求"**：把推送的数据直接交给这些等待者（回复 L1），统计 `catch_inflight` / `catch_queued` / `catch_waiters`。表项本身不动：它照常发出（若还没发）并保留窗口位，直到自己的响应回来后照常完成（回填 L2 等）。期间新合并进来的读等自己的响应。`processRemoteReady` 对"已提前交付但还没收到自己响应"的表项只出队、不回填、不删除（只有 `geoEarly` 的表项走这个分支，geo 关闭时不变）。
5. 没有表项 → 放进回填队列（`-geo-push-fill-queue`，默认 64，满了丢弃），经 M3 的 L2 回填路径写入本地 L2：`RemoteDataFill` 带 `Push` 标记，**不占窗口位、不回复 L1**。

L2 收到推送回填（`installRemoteDataFill`）：
- 不检查 generation（没有先前的 lookup），改为：`OriginTime` 不晚于本 slice 最近一次 flush 或 kernel 边界的推送丢弃；
- line 已在 L2 或在 MSHR 里 → 丢弃（"已在 L2 的直接丢弃"）；
- **只用空闲的 way**，从不替换有效 line（包括 M3 副本），所以推送不会挤掉有用的数据；
- 装入后它就是一个 M3 副本：更新 RESIDENT 过滤器，之后本 GPM 的读会探测 L2 并命中；在下一个 kernel 边界和 M3 副本一起失效。
- 推送单独计数（`PushFills / PushInstalled / PushDropped* / PushUseful / PushHits / PushUnusedRetirements`），不混进 M3 的统计。

## 5. 一致性

和 M3 相同的假设：
- 同一个 kernel 内，被多个 GPM 读的数据不会被别的 GPM 写（数据无竞争）；
- 请求方自己写过的 line 不缓存（`remoteUncacheable`），推送也不收；
- kernel 边界清掉所有副本（推送副本在 L2 里就是 M3 副本）；在途的推送用 `Origin` 时间判过期，RDMA 和 L2 两处都检查；
- flush 之后也按 `Origin` 判过期。

`-verify` 小规模结果（geo1、geo2）：bfs s12、sssp s12 r4、MM（`matrixmultiplication-pipeline-smoke`，加 `-magic-copy-coherent`，不加时连 baseline 都不过，是原有问题）、npbcg class S、graphtc 默认，全部通过，见 README。

## 6. 只在 v3 上启用

`-geo-push-radius > 0` 要求 `-remote-data-path-enable`、`-remote-data-path-l2-enable` 和 T18；否则启动时报错。所有 GPM 都配置同样的半径。

## 7. 上限统计（`-report-geo-push-bound`，只读）

- **推送候选**：请求方 R 每收到 owner O 的一条 line（单行、位图里的每条、多播叶子、baseline 路径的读响应），记为一次"送达"，时间 = R 收到的时间，同时记 owner 发出的时间。
- **可省的读**：之后某个 GPM N 对同一条 line 发出了**真正走网络**的 demand 读（本地 L2 没命中、也没合并进自己的在途项；baseline 下就是每个远程读），N 是 R 的 1 跳 / 2 跳邻居（N ≠ R、N ≠ O），且 N 需要这条 line 的时间（第一个等待者到达 RDMA 的时间）落在 [送达, 送达 + T]，T = 1 µs / 10 µs，并且
  - N 在送达之后没有过 kernel 边界（否则推送副本已经失效）；
  - 中间没有任何 GPM 对这条 line 发过远程写。
  一次读只记给**最早**的那次合格送达（真实机制里装进去的是第一份推送）。
- **late**：N 需要的时间在 [owner 发出, 送达) 之间（推送会在 N 自己的读在途时到达），单独计数，不算可省。
- **按半径汇总**：每次推送平均被用到的邻居数（每次送达被记入的不同邻居数）、可省的远程读比例（可省读 / 全部网络读）、可省的字节×跳数（读按请求 1 flit + 响应 6 flit = 112 B/跳）、推送成本（多播树多出的链路 × 112 B，按任务要求）、无用副本比例（1 − 用到的副本 / 推送的副本）、零使用推送比例。
- 输出 `stats/metrics_geo_bound.csv` 和 `geo_bound_*` 指标（where = GeoBound）。
- 开关打开后 timing 不变（bfs s12 的 baseline、v3_complete 开关前后 metrics 完全一致）。

## 8. 没做 / 局限

- 推送包的 flit 数按原响应算；`shared` 过滤掉部分 line 时没有缩小包。
- 推送不占 owner 的窗口，也不受 owner 出口队列以外的限流；网络紧张时可能加重拥塞（这正是实验要看的）。
- 早交付的在途表项仍然发出、仍然占窗口位直到自己的响应回来；没有实现"取消还没发出的请求"。
- 只在 7×7（`gpmTraceMeshWidth`）上测试。

## 9. affinity-push（按亲和性推送，`106dc0d`）

动机：geo-push 推给几何邻居，82–97% 的副本用不上；同一 line 的读者可能是固定的 1–2 个"亲和对象"。WG 是按 GPM 编号连续分配的，而 tile = x + 7y，所以换行处编号相邻的一对，例如 (6, y) 和 (0, y+1)，在 mesh 上相距 5–7 跳。

- **开关**：
  - `-geo-push-mode affinity`（默认 neighbor）；
  - `-affinity-k 1|2`；
  - `-affinity-decay-hints`（默认 512）；
  - `-affinity-min-count`（默认 2）。
  - 可叠加 `-geo-push-filter shared`。
  - 配置：`v4_complete_aff1`（K=1）、`v4_complete_aff2`（K=2）、`v4_complete_aff2s`（K=2 + shared），都 extends `v4_complete.json`。
- **owner**：共享表（1024 项，4 路 LRU）的每项另外记该 line 的最近一个读者 `last` 和之前一个不同的读者 `prev`；所有到达的远程读都更新。回复 R 时，每条 line 捎带"除 R 以外最近的读者"P（6 bit）。只对单个请求方的响应捎带：v4 没有 owner 合并，所以所有响应都是单个请求方。
- **请求方**：48 项 4 位饱和计数器。
  - 收到 P 就给 cnt[P] 加 1；
  - 每收到 512 个 hint，所有计数器减半（衰减，跟随阶段变化）；
  - top-K 中计数 ≥ 2 的 GPM 是"伙伴"。
- **请求头**：每个远程读请求都带上请求方当前的伙伴列表（K × 6 bit）；owner 为每个请求方保存它最近一次带来的列表（48 × K × 6 bit）。
- **推送**：owner 把响应多播给 R ∪ R 的伙伴（去掉 owner 和请求方），走和 geo-push 相同的多播树和邻居处理：交给在途/排队请求的等待者，或写入空闲的 L2 way，按 kernel 边界判过期。
- **开销建模**：hint 和伙伴列表算作头部的空闲位，不加字节（6–12 bit）。模拟器里这两项靠两张以消息 ID 为键的旁路表传递：发送方写，接收方读完就删。这样做是因为请求和响应的消息类型和 baseline 共用。
- **统计**：
  - owner 端：`geo_push_aff_hints_sent`、每个推送目的地离 R 的跳数分布 `geo_push_aff_partner_hops_NN`、没有伙伴的响应数 `geo_push_aff_no_partner_rsps`；
  - 请求方：`geo_push_aff_hints_learned`、`geo_push_aff_decays`、`geo_push_aff_hdr_sent`。

### 共读矩阵（`-report-coread-matrix`，只读，`3b85824`）

- 每次真正走网络的 demand 读（和上限统计同一组事件），统计在 |Δt| ≤ T（T = 1 / 10 µs）内也读过同一条 line 的其他 GPM。每个伙伴每次读最多计一次。
- 跨任一方的 kernel 边界、或中间有远程写的不算。
- 矩阵按两个方向对称累加，输出 `stats/metrics_coread.csv`。
