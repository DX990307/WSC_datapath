# T9 第二批 workload：MoE、GAP 图计算、stencil

> ⚠️ **10-16 更新：MoE 移到 T17，其余暂停**（见 `T17_latest_workloads.md`）。下面的内容保留作参考，不要执行。

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[提案]` · **在用户确认之前不要开始** · **周次**：W2 之后，等第一批（T5a–T5d）完成再做

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧。第一批的 4 个新负载（LLM、GCN、DLRM、SSB）覆盖了 ML 推理、图学习、推荐系统和数据库。第二批是补充，人力和时间允许才做。
> **新 workload 的三条原则**：①每个负载要能回应一条审稿意见，或者验证一个机制；②用真实的场景和数据；③所有负载都报告共享度（T2 的 M1）。
> **通用约束**：kernel 一律用 fp32；编译流程沿用 T1；输入规模要保证 kernel 能在模拟预算内跑完（见 T4）。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 候选

| workload | 规格 | 为什么选它 | 工作量 |
|---|---|---|---|
| **MoE 层** `moe` | 在 T5a 的基础上扩展：gating → top-k → token permute → grouped GEMM → combine。Mixtral 风格（8 选 2）或 DeepSeek 风格（64 选 6），二选一 | 热门 expert 的权重被很多 GPM 读；permute 是近似 all-to-all 的 scatter。这是 2024 年以后的主流架构 | 在 T5a 的基础上 +2–3 天 |
| **BFS / PR** `gap-bfs`、`gap-pr` | GAP 或 Graph500 的 Kronecker 图，scale 20（约 100 万个点、3,200 万条有向边）；BFS 用 top-down 方式，PR 用 pull 方式，跑 3 轮 | 访问不规则；每轮迭代是一个 kernel，图结构只读，适合验证机制 2。输入是公认的标准图 | 3 天 |
| **3D Jacobi stencil** `stencil3d` | 7 点 stencil，256³，fp32，迭代 3 轮 | halo 交换只发生在相邻 GPM 之间，属于“近距离远程”，用来检验机制 1 的距离阈值。这是**对照组**，预期收益很小 | 1–2 天 |

## 需要用户决定

- 做哪几个？
- MoE 用哪种风格？
