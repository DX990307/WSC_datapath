# T19 cuckoo filter 指导的就近 WG 窃取

**状态**：`[下一篇论文]`（用户 10-20：ISCA'27 只做通信，调度留给下一篇；素材见 `../next_paper_scheduling.md`）。代码已由 Claude Code 实现（`isca27/t19-steal` @ `eaa9c73`，默认关闭），冒烟结果在 STATUS；**本篇不再推进，T19 的评估和方向问题留给下一篇**· **依赖**：P12；T18 的在途表（可共用）· **工作量估计**：要改 driver / dispatcher 的 WG 分配，约 1–1.5 周

> 🔒 **执行前必做**：按 `../CONFIG.md` 做配置确认。WG 分配是所有 workload 共用的，改动必须用开关打开、默认关闭，并回归证明关闭时结果逐位不变。
> 索引：`../README.md` · 进度写入：`../STATUS.md` · 同步：`../SYNC.md`

## 动机（已有数据）

- WG 是静态、连续切分的（每个 GPM 28 × ⌈WG 数 / 1344⌉ 个），kernel 时间由最慢的 GPM 决定。
- T17 的负载不均来自数据（序列长度、每个 expert 的 token 数），不是位置（T16 已排除）。按 trace 估算的完美均衡上限（假设每个 GPM 速度不变）：

| workload | 只在用到的 GPM 之间均衡 | 均衡到全部 48 个 GPM |
|---|---|---|
| PagedAttention | 1.74× | 1.75× |
| MoE skew 1.75 | 1.61× | 2.94× |
| R-GAT | 1.16× | 6.18× |

## 设计（草案）

- 每个 GPM 把剩余 WG 数告诉 1–2 跳内的邻居；先跑完的 GPM 从邻居的 WG 区间**尾部**偷还没开始的 WG。优先近处：远处的 WG 偷过来要多付 5–10 跳的远程延迟。
- **cuckoo filter 的作用：挑“数据我已经有了”的 WG（亲和性）**
  - 历史足迹：同一个 kernel 第一次运行时，用 cuckoo filter 记录每组 WG 读过哪些页；模式变化时删除旧记录（cuckoo 可删除）。
  - 偷的时候，拿候选 WG 组的足迹和偷取方的在途表（T18）、L2 内容比，挑重叠最多的。和 T18 共用同一张在途表：“一张表、两个用途”。
  - 没有历史时，按编号相邻 / 同一组（如 MoE 同一个 expert）挑。
- 预期受益：MoE（同 expert 的 WG 共用权重）> R-GAT（相邻目标节点共享邻居特征）> PagedAttention（几乎不共享，只能按距离偷）。

## 实现要点

`mgpusim/driver/driver.go` 的 `distributeWGToGPUs` 和 CP 的 `WGFilter` 现在在 launch 时把每个 GPM 的 WG 范围定死，需要改成可动态领取的队列；kernel 结束的判断要跟着改。

## 风险 / 前人工作

MCM-GPU 上的分布式 CTA 调度和窃取；新意靠“就近 + 亲和性（filter）+ wafer 规模”。如果 P12 显示亲和性很小，就退回“按距离就近偷”，filter 部分放进讨论。
