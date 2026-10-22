# pm：页迁移和 CuPath 的对比分析

Claude Code，2026-10-21 / 22。

- 主配置：384 GB/s，W=64，mi200，25 个 workload（abl3c384 的全部）。
- 数字是 T_baseline / T_variant，>1 表示更快。GMEAN 只算有结果的格子。
- 设计和参数见 `DESIGN.md`，运行情况见 `README.md`。
- 文末的表由 `scripts/analyze.py` 从 `pm.csv` 生成。
- 本文只用 W=64 的结果，不引用 W=1024 的旧结果。

## 0. 一句话结论

| 方案 | GMEAN（25 个 workload） |
|---|---|
| Complete | **1.73** |
| 页迁移最好的单一策略：counter 4K | 1.14 |
| fault 4K | 1.10 |
| 迁移的理想上限（fault 4K ideal） | 1.47 |
| 每个 workload 事后挑最好的 4K 变体（oracle 选择） | 1.28 |
| 每个 workload 事后挑最好的 4K 变体，并且用理想上限 | 1.53 |
| 2 MB 粒度（4 个 2M 变体） | 0.45–0.48，**全面变差** |

- 页迁移和 CuPath 解决的是两类不同的问题：
  - 页迁移只对"一个 GPM 稳定地使用另一个 GPM 上的私有数据"有效（fft2d、fw、i2c，理想下还有 cfd、moe、pa、npbis）；
  - 对"多个 GPM 共享读、集中在热点 owner 上"的 workload（pr、npbcg、mm、tc、atax），页迁移只能把热点挪个地方，远不如 Complete。
- 两者部分**互补**：
  - Complete + counter 4K 相对 Complete 的 GMEAN 是 COMPLC4K（COMPLC4KN 个 workload）；
  - 其中 fw 1.63、fft2d 1.61、cfd 1.38、mg 1.24、km 1.21、npbcg 1.14；
  - 每个 workload 取 Complete 和 Complete+迁移中较好的一个，GMEAN 是 MAXCOMPL。
  - Complete + fault 4K 平均更差（COMPLF4K）：缺页阻塞的代价抵消了收益。

## 1. 结果的可信度

1. **复用的 baseline 是对的**：25 个 workload 里有 23 个，observe（钩子都装上、从不迁移）的时间和 abl3c384 的 baseline 完全相同（表 T2）。不同的两个：
   - **bfs-s18e2**：observe 0.9936 ms，abl3c384 0.9369 ms。专门做了 4 个诊断运行（同一台机器，同样的网络参数）：

| 运行 | 二进制 | 统计开关 | 时间 |
|---|---|---|---|
| none-cheap | c74ef96，关闭迁移 | CHEAP | 0.94297 ms |
| old-cheap | 4235991 | CHEAP | 0.94215 ms |
| none-full | c74ef96，关闭迁移 | FULL（和 abl3c384 一样） | 1.03684 ms |
| obs-full | c74ef96，observe | FULL | 0.97673 ms |

     关闭迁移时两个二进制走的代码路径完全相同（fir、MM 逐位一致），同一条命令却在 0.937–1.037 ms 之间波动，WG 数和指令数每次都一样。所以这是 **bfs 自身的运行间不确定性**（±5%，最大差 10%），不是钩子扰动。bfs 的加速比要按 ±10% 的噪声来读：counter 4K 的 1.00、ideal 的 0.97 都应该看成"没有变化"。
   - **npbcg-W**：两者差 4×10⁻⁶，同样是运行间的微小波动，可以忽略。
2. **迁移不改变结果**：
   - 104 个服务器上的 `-verify` 运行全部正确，都开了 `-pm-check`；
   - 本地另有 54 个更小规模的压力组合，全部正确；
   - 迁移不变量（旧帧不再被访问、新帧在 remap 之前不在任何 cache 里）从来没有触发。
3. **4 个运行挂住**：mg-W pmf2m、mg-W pmc4kideal、shocsort pmc2m、npbis pmf2mideal。
   - 都已经复现，并查了现场：是已知的 L2TLB→IOMMU 丢唤醒 bug（若干 GPM 的 L2TLB TopPort 缓冲里卡着 28–56 条请求，MSHR 是空的，组件不再 tick）。迁移管理器没有进行中的迁移，也没有被挂起的访问。
   - 这 4 格在表里是"–"，不参与 GMEAN。
   - 迁移会增加 TLB 失效和 IOMMU 请求，所以更容易撞上这个 bug。这个 bug 本身没有修（用户的要求）。

## 2. 页迁移什么时候有用：看远程访问减少了多少（表 T5）

把 fault 4K 下"离开 L1 的远程访问"和 observe 相比，25 个 workload 可以清楚地分成三类。

**A 类：远程数据是"某个 GPM 私用"的，迁移以后就变成本地。**

| workload | fault 4K 后远程访问剩下的比例 |
|---|---|
| moe | 0.04 |
| pa | 0.05 |
| i2c | 0.11 |
| cfd | 0.19 |
| km | 0.19 |
| fw | 0.29 |
| mg | 0.37 |
| fft2d | 0.38 |

- 这类 workload 的 WG 连续切分和页的 48 等分放置错开了：一个 GPM 的 WG 主要访问另一个 GPM 上的一段数据，而且这个关系在 kernel 内、甚至跨 kernel 都是稳定的。
- 迁过去以后远程访问消失，迁移的一次性代价就能被摊掉。
- **页迁移只在这一类上能和 Complete 相当或者超过它**：

| workload | 迁移 | Complete |
|---|---|---|
| fft2d | c4k 1.88 | 1.34 |
| fw | f4k 2.05 | 1.87 |
| i2c | f4k 2.01 | 1.21 |
| pa | f4k 1.57 | 1.58 |
| cfd（理想上限） | f4k ideal 2.63 | 1.88 |
| moe（理想上限） | f4k ideal 4.13 | 2.13 |

- Complete 只是让远程访问变得更便宜（合并、打包、复用），并不消除远程访问，所以在这一类上不是最好的方案。

**B 类：多个 GPM 共享读，集中在热点 owner 上。**
- 包括 pr、mm、npbcg、tc、shocsort、atax。
- fault 4K 后远程访问剩下 0.96–1.64，没有减少；mm 反而增加到 1.64，因为页在 GPM 之间来回迁。
- 迁移能做的只是把热点 owner 打散。远程读里份额最大的那个 owner：

| workload | observe | fault 4K |
|---|---|---|
| pr | 1.00 | 0.15 |
| mm | 1.00 | 0.19 |
| shocsort | 1.00 | 0.17 |
| npbcg | 0.79 | 0.08 |
| tc | 0.61 | 0.09 |

  热点没有了，所以 pr 2.89×、tc 1.52×、mm 1.48×、npbcg 1.29× 都有收益；**但同一组 workload 上 Complete 是 13.3×、3.69×、2.67×、4.44×**。
- 原因：共享读的数据本来就该被很多 GPM 同时读。迁移只能决定"放在谁那里"，而 CuPath 的合并（M1）和复用（M3）真正减少了网络上的读。
- **这正是论文需要的论据：热点 owner 场景不是"换个放置就能解决"的。** 把放置随机打散（页迁移在这里起的作用正是这个），PR 也只能拿到 Complete 的 22%。

**C 类：细粒度共享，或者访问模式变化快。**
- 包括 ar、bfs、km、shocsort、bh、gups、ivf、embbag、sssp。
- 有的远程访问减少了（ar 0.49、bfs 0.58），但页在多个 GPM 之间来回迁移，访问被挂起的代价超过了收益。fault 4K 下：
  - ar 0.68、bfs 0.60、km 0.56、shocsort 0.53；
  - 例如 bfs 有 14 622 次迁移、806 次来回、2548 个 unit 被 pin 住，挂起的访问有 218 万个。
- counter 4K 在这一类上基本不触发（ar、bfs、gups、embbag 都是 1.00），不赚也不亏。

**热点 owner 的远程读份额是否下降**：下降了。B 类从 0.6–1.0 降到 0.08–0.19（fault 4K）或者 0.14–0.43（counter 4K）。但份额下降不等于远程读减少，所以收益有限。

## 3. 粒度：2 MB 全面变差（GMEAN 0.45–0.48）

- 2 MB 的组把相邻的很多 buffer 一起搬：小 buffer 在虚拟地址上相邻，大 buffer 的 48 等分块也会跨组。例如 pr fault 2M：48 次迁移就搬了 23 439 页（92 MB）。
- 每次迁移平均挂起访问 50–60 µs（4K 时是 2–7 µs），搬运时间占了大头。
- **远程访问几乎没有减少**（表 T5 的 2M 列在 0.9–1.2 之间，fw 甚至到 3.3）：一个 2 MB 组里同时有好几个 GPM 的数据，搬给谁，其它 GPM 都变成远程访问。来回迁移也多，直到被 pin 住。
- 只有 moe 例外（1.44–1.54），因为它的大块 expert 权重确实主要被一个 GPM 使用。
- 2 MB 粒度在这个平台上不适合：它的前提是"一个大区域只被一个处理器用"，在 GPU 之间按页 48 等分放置、WG 连续切分的晶圆上，这个前提不成立。
- 注意：我们的 2 MB 是"迁移组"，没有改变 TLB 的页大小（DESIGN.md 3.2），所以没有算大页带来的 TLB 收益。不过 baseline 的 TLB 缺失本来就不是瓶颈，这个简化不改变结论。

## 4. 策略和代价参数（表 T6）

**软件延迟（缺页或通知的服务时间）对 fault 策略是决定性的。**

fault 4K 在不同 sw 下：

| workload | 1 µs | 2 µs | 5 µs（默认） | 10 µs | 20 µs | ideal（0） |
|---|---|---|---|---|---|---|
| cfd | 2.29 | 1.83 | 1.16 | 0.68 | 0.38 | 2.63 |
| ar | 1.05 | 0.93 | 0.68 | 0.41 | 0.23 | 1.35 |
| pr | 3.24 | 2.88 | 2.89 | 2.97 | 2.89 | 2.89 |
| npbcg | 1.63 | 1.53 | 1.29 | 0.95 | 0.61 | 1.83 |

- 9 个 workload 在 20 µs 时的 GMEAN 只有 0.62。
- 依据：Zheng 等 HPCA'16 第 1 节测得 GPU 缺页端到端是 20–50 µs，作者把 5 µs 称为"不现实的低"。所以我们默认的 5 µs 已经是对迁移有利的取值；按文献的典型值（20 µs），fault 策略在大多数 workload 上是负收益。
- 只有 pr 对软件延迟不敏感：它总共只有 469 次迁移，热点被打散带来的收益远大于缺页延迟。
- 1–2 µs 只有片上硬件迁移引擎才可能做到（不经过主机 driver）。即使这样，cfd 也只有 2.29，仍然低于 Complete+c4k 的 2.58。

**access counter 对软件延迟不敏感**：它是异步的，触发以后访问不挂起，sw 从 1 µs 到 20 µs，结果几乎不变（pr 1.43–1.69，cfd 1.58–1.65）。它的效果主要由阈值决定：
- 阈值 64：9 个 workload 的 GMEAN 1.32（cfd 2.24、pr 1.91、npbcg 1.55）；
- 阈值 256（NVIDIA 默认）：1.19；
- 阈值 1024：1.10。
- 阈值越低越早迁，在 A 类和 B 类上都更好，在 C 类上来回迁移变多（ar 0.93）。
- 即使取最好的阈值 64，也远低于 Complete（同 9 个 workload 的 GMEAN 是 2.38）。

**max-migrations=1（第一次远程缺页就迁过去，然后 pin 住，即 first-touch）**：9 个 workload 的 GMEAN 1.07，和默认的 3 次（1.12）差不多。
- cfd 变好（1.92），因为来回迁移少了；
- pr 只有 1.16：热点页被第一个碰到它的 GPM 抢走，然后 pin 住，热点只是换了个地方。

**理想迁移（软件延迟和 shootdown 延迟都是 0）**：fault 4K ideal 的 GMEAN 1.47，是迁移的上限，仍然低于 Complete 的 1.73。
- 在 B 类上，理想迁移和非理想几乎一样（pr 2.89 对 2.89），说明 B 类的瓶颈不是迁移代价，而是共享本身。

## 5. 和 Complete 叠加（表 T4）

- **Complete + counter 4K**：相对 Complete 的 GMEAN 是 COMPLC4K。
  - 收益集中在 A 类：fw 1.63、fft2d 1.61、cfd 1.38、mg 1.24、km 1.21、npbcg 1.14、npbis 1.13；
  - 损失集中在热点 owner 类：pr 0.76、tc 0.83、mm 0.94。Complete 已经用 M3 解决了这些 workload 的热点，迁移把页挪走，反而打乱了 M3 的复用；另外在 Complete 下，access counter 会把 M3 命中的访问也算进去（DESIGN.md 第 5 节第 3 条），所以迁移更积极。
- **Complete + fault 4K**：GMEAN COMPLF4K。i2c 1.74、fw 1.23、sssp 1.11 有收益，但 ar 0.42、atax 0.40、km 0.49、shocsort 0.52 被缺页阻塞拖累。
- **结论**：两者是互补的，不是替代关系。
  - 对稳定的私有远程数据，迁移（或者更好的初始放置）能消除远程访问，CuPath 在这类数据上加不了多少；
  - 对共享数据和热点 owner，只有 CuPath 有效。
  - 论文里可以把"Complete + 异步 access-counter 迁移"作为一个正交组合来报告；也可以如实说明，在 fft2d、fw、i2c 这类 workload 上，单纯调整放置就能拿到和 CuPath 相当的收益。

## 6. 对论文的建议

1. 回答审稿人：
   - 用表 T1 和 T5：页迁移（取最好的 4K 策略、对它有利的参数）的平均加速 1.10–1.14，理想上限 1.47，Complete 1.73。
   - 差距集中在共享和热点 owner 类 workload 上：迁移把热点 owner 的份额从约 1.0 降到约 0.15，但远程读一次都没有少，PR 只拿到 Complete 的 22%。
2. 如实报告：
   - 有 3 个 workload（fft2d、fw、i2c），非理想的页迁移就比 Complete 好；
   - 理想迁移下再加上 cfd、moe、pa、npbis。
   - 这些 workload 的问题出在"放置和使用者错开"，CuPath 不处理这个问题。
   - Complete + counter 迁移能在 GMEAN 上再提高一些，单个 workload 最多 1.6×。
3. 代价参数的出处：Zheng HPCA'16（缺页 20–50 µs），NVIDIA UVM 源码（阈值 256、批大小 256、粒度 2 MB、thrashing 阈值 3）。软件延迟的敏感性见第 4 节：按文献的典型值，fault 策略是负收益。

## 7. 局限

- 2 MB 是迁移组，不是真正的大页（见第 3 节）。
- access counter 在 Complete 下会多计 M3 命中的访问（DESIGN.md 第 5 节第 3 条），这可能让 Complete+counter 的结果偏向多迁移。
- shootdown 没有走真实的网络消息，而是用一个固定延迟（1 µs）。由于 counter 策略对延迟不敏感，fault 策略的主要代价又是软件延迟，这个简化影响不大。
- 4 个挂住的格子缺数据，都是已知的模拟器 bug（第 1 节）。

---

# 表（scripts/analyze.py 生成）

