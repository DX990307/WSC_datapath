# T3 强 baseline（B1–B4）

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]` · **周次**：W1 · **依赖**：T0 · **被依赖**：所有结果对比、go/no-go 关口

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。**6 位审稿人都批评只和朴素 baseline 比较。** 原 CuPath 有三个机制：Remote Aggregation（在 NI 合并请求）、Remote Reuse（把远程 line 保留在 requester 的 L2）、Local Pairing（预取相邻的 64B line）。原结果：GMEAN 1.47×，其中 MM 只开 Remote Reuse 就有 5.451×，BT 和 FWT 约 1.64×（几乎全部来自 Local Pairing）。
> 平台：48 GPM，每 GPM 4 MB L2，地址按 128B 在 partition 之间交错。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 为什么做

这 4 个 baseline 用来检验：原 CuPath 的收益，有多少是一个更简单的做法就能拿到的。

| baseline | 回应谁 | 要回答的问题 |
|---|---|---|
| B1 | A、C、E | 原论文一边说“保留所有远程数据会浪费容量”，一边又说“L2 有 60.1% 是空的”，审稿人会问：那为什么不把远程数据全部缓存进 L2？MM 的 5.45× 里有多少是这个简单做法也能拿到的？ |
| B2 | D | 一次取 128B 加 sector promotion，能不能涵盖 Local Pairing？ |
| B3 | C、E | 和 MCM-GPU 的 L1.5 相比怎么样？ |
| B4 | A、E | 不用 Cuckoo Filter，只用精确合并，效果怎么样？ |

## 要做什么

**全部做成配置开关**，名字就用下面这些。默认行为保持不变。

1. **B1 `remote-l2-always`**：远程响应**总是**填进 requester 的 L2，用普通 LRU 替换，可以挤掉本地数据。一致性规则和原 CuPath 的 Remote Reuse 一样：在 kernel 边界和同步点失效。
2. **B2 `fetch-128b`**：L2 miss 时，一次取对齐 128B 区域内的两条 64B line，并开启 sector promotion。具体实现见 Rhu et al. MICRO'13，以及 Li et al. ASPLOS'17 中关于 sector promotion 的描述。在 meta 里写清楚实现方式。
3. **B3 `mcm-l15`**：按 MCM-GPU（Arunkumar et al. ISCA'17）的做法，在 L2 前面加一个只存远程数据的 L1.5。原论文把大部分 L2 容量划给 L1.5，只给 L2 留 32 KB 左右。**先按原论文的划分实现一个版本，再实现一个“L1.5 和 L2 总容量相同、各占一半”的版本**，两个都跑。
4. **B4 `ni-exact-merge`**：在 NI 用精确的 CAM 查找 in-flight 请求，合并同 line 的请求，不使用 Cuckoo Filter。CAM 大小等于 NI 的 outstanding 数（64）。

## 验收标准

- [ ] 4 个开关都能单独打开，也能和原 CuPath 的各个机制组合
- [ ] 合理性检查（不跑 verify）：在 MM 和 KM 上，打开每个开关之后，`kernels_completed` 和实际访问的 footprint 都和 baseline 一致
- [ ] 用现有的 14 个 workload，在 `cu8` 下跑 B1–B4 和原 CuPath，在 `STATUS.md` 里贴出各 workload 的加速比表，**重点看 MM、FWS、KM、BT、FWT**

## 注意

- 如果 B1 在 MM 上拿到的收益接近原 CuPath，**这是一个重要发现**，要立刻写进 `STATUS.md` 的“需要用户决定”。
- B3 要占用 L2 容量，实现方式和容量划分写进 meta。


> **10-13 注**：文中提到的 `cu8` 目前是 `[提案]`（偏离论文配置，等用户确认）。在用户批准之前，**一律用论文配置（`../CONFIG.md`）**代替 `cu8`。
