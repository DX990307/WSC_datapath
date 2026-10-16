# T5c 新 workload：DLRM embedding bag（`dlrm-emb`）

> ⚠️ **10-16 更新：降为 T17 的备选 W-D**（见 `T17_latest_workloads.md`）。下面的内容保留作参考，不要执行。

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]` · **周次**：W1–W2 · **依赖**：T1（**需要 atomic**）、T0 · **工作量估计**：2–3 天

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧。审稿人 B 要求给出**读写并发下的一致性**方案，以及混合读写负载的实验。新主线 `[提案]` 中，机制 1（T7）在区域内复制共享的远程数据，机制 2（T8）用写签名做选择性失效。
> **新 workload 的三条原则**：①每个负载要能回应一条审稿意见，或者验证一个机制；②用真实的场景和数据；③所有负载都报告共享度（T2 的 M1）。
> **通用约束**：kernel 一律用 fp32；编译流程沿用 T1；输入规模要保证 kernel 能在模拟预算内跑完（见 T4）。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 为什么选它

- 推荐系统是数据中心 GPU 的主要负载之一（MLPerf 里就有）。
- **前向**：热门 embedding 行会被所有 GPM 读取，这是机制 1 最理想的场景。
- **反向**：scatter-add 用 atomic 写入，直接回应审稿人 B 要的读写混合实验，也能检验机制 2 在写路径上是否正确。

## 规格

- **表**：8 张，每张 1M 行，64 维，fp32（每行 256B，即 4 条 cache line）。表的数量和行数都可以配置。
- **索引分布**：优先使用 Meta 公开的 embedding lookup trace（facebookresearch/dlrm_datasets；**先确认能不能获取、格式是什么**）。拿不到的话，用 zipf 分布生成，α 可以配置，默认 1.05，随机种子写进 meta。
- **batch** 4096，**pooling factor** 20（每个样本在每张表里查 20 行）。
- **kernel**：
  1. **前向 `emb_fwd`**：对每个（样本，表），gather 对应的行并求和，输出 batch × 表数 × 64 的结果
  2. **反向 `emb_bwd`**：对每个（样本，表），把梯度用 atomic 加回对应的行（scatter-add）
- **估计 footprint**：分配约 2 GB；每个 batch 实际访问约 100–200 MB，视分布的偏斜程度而定。

## 要做什么

1. 写索引生成脚本（或 trace 转换脚本），输出二进制文件。
2. 写两个 kernel。float atomic 不支持的话，按 T1 的结论用 CAS 循环代替。
3. 用模拟统计做合理性检查，**不单独跑 verify**（用户 10-12 的决定）：①`kernels_completed` 等于预期的 kernel 数；②实际访问的 footprint（T2 的 F）和按数据结构大小估算的值相差不超过 20%；③日志里没有越界地址或 page fault 警告。kernel 参数没对齐时，通常会出现 footprint 异常小或者地址越界，这三项能把它查出来。
4. 在 `cu8` 下跑 baseline：先前向，再反向，一共两个 kernel。

## 验收标准

- [ ] 合理性检查通过（kernel 数、footprint、无越界）
- [ ] `cu8` 下能跑完前向和反向
- [ ] 产出 M1（**前向的区域共享占比**）、M2（前向读过的行，在反向中被写的比例）、M3、F
- [ ] 报告 zipf 参数对 M1 的影响：α 取 0.8、1.05、1.2 各跑一次 baseline

## 注意

- 如果用 zipf，论文里要能说明这个参数的出处。所以要优先尝试使用真实 trace，并把尝试的过程记录在 `STATUS.md`。

## 调研补充（10-12，Cowork 侧）

- `facebookresearch/dlrm_datasets`（MIT 许可，**2023-10-31 已归档**）提供的是**合成的** embedding lookup 输入，用来复现 Meta 生产环境推荐模型的访存复用模式，数据由脱敏后的生产数据派生而来。
- 格式：`embedding_bag/2021/*.pt.gz`，用 `torch.load` 加载后得到 `indices`、`offsets`、`lengths` 三个张量，是 FBGEMM TBE 的格式。文件名里包含表数和 batch 大小，例如 `fbgemm_t856_bs65536.pt.gz` 表示 856 张表、batch 为 65,536。
- 这个规模远远超过我们的模拟预算。做法是**从中选出 8 张表、截取 4096 个样本**，转换成二进制文件；选哪些表、怎么选，写进 meta。
- 页面上没有下载说明。先确认 `.pt.gz` 文件能不能拿到（在仓库里，或者用 git LFS）；拿不到的话，回退到 zipf 分布，并在 `STATUS.md` 里记录尝试的过程。
- 加载这个文件需要 PyTorch，只在本机的预处理脚本里用，不影响模拟。

> **操作步骤**：见 `../guides/G1_port_workload_to_mgpusim.md`。建议的移植顺序是 GCN → embedding → LLM → SSB。


> **10-13 注**：文中提到的 `cu8` 目前是 `[提案]`（偏离论文配置，等用户确认）。在用户批准之前，**一律用论文配置（`../CONFIG.md`）**代替 `cu8`。
