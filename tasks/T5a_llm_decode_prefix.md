# T5a 新 workload：共享前缀的 LLM 批量 decode（`llm-decode-prefix`）

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]` · **周次**：W1–W2 · **依赖**：T1（指令支持）、T0 · **工作量估计**：4–6 天

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧（2010 年代的 Hetero-Mark、SHOC 等），还指出原来的 ML 实验是冷 cache、逐个算子单独跑，结果只是复现了 MM。审稿人 A 指出 kernel 边界的失效会冲掉只读的权重。
> **新 workload 的三条原则**：①每个负载要能回应一条审稿意见，或者验证一个机制；②用真实的场景和数据；③所有负载都报告共享度（T2 的 M1），包括没有优化机会的负载。
> **通用约束**：kernel 一律用 fp32，不用 MFMA；编译流程沿用 T1；**输入规模要根据模拟预算来定，保证 kernel 能跑完**（见 T4）。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 为什么选它

- **共享前缀**是真实的部署场景：vLLM 的 prefix caching 中，多个请求共用同一段 system prompt，这段 prompt 的 KV 只存一份，被所有请求的 attention 读取。这是跨 GPM 共享最有说服力的例子，对应机制 1（T7）。
- **GQA**：每个 KV 头被 4 个 Q 头共享。如果这 4 个 Q 头的 WG 落在不同的 GPM 上，又多了一种共享。
- **FFN 的 GEMV 是纯流式访问**，权重在一个 token 里只读一次，正好作为“没有优化机会”的对照。
- 直接回应审稿人 F 对 ML 部分的批评。

## 规格

**模型配置**（1B 级，权重随机初始化，用 fp32）：hidden 2048，32 个 Q 头，8 个 KV 头，head 维度 64，FFN 中间层 8192（SwiGLU）。

**kernel 顺序**（一层）：
1. RMSNorm
2. QKV 投影（批量 GEMV，batch 行 × 权重）
3. RoPE（可选，可以先跳过）
4. Attention：对每个请求的每个 Q 头，计算 QK^T → softmax → ·V。KV 由两部分组成：共享前缀（所有请求共用同一块内存）和私有部分。
5. O 投影
6. 残差 + RMSNorm
7. FFN：gate 和 up 投影 → SiLU × → down 投影
8. 残差

**参数**（都可以配置）：`batch`（默认 16）、`prefix_len`（默认 2048）、`private_len`（默认 512）、`layers`（默认 1；能跑得动就用 2，用来观察跨层效果）。

**估计 footprint**：每层权重约 243 MB（fp32）。KV 每个 token 每层 4 KB：共享前缀 2048 个 token 约 8 MB，私有部分 16×512 个 token 约 32 MB。

## 要做什么

1. 写 OpenCL（或 HIP）kernel，可以参考 llm.c 或 llama2.c 里 kernel 的写法，但要改成 fp32 的简单实现。
2. 在 MGPUSim 的 benchmarks 下新建 `llmdecodeprefix`（目录名跟现有 benchmark 保持一致的风格），Go host 代码的结构参考现有的 benchmark。
3. 用模拟统计做合理性检查，**不单独跑 verify**（用户 10-12 的决定）：①`kernels_completed` 等于预期的 kernel 数；②实际访问的 footprint（T2 的 F）和按数据结构大小估算的值相差不超过 20%；③日志里没有越界地址或 page fault 警告。kernel 参数没对齐时，通常会出现 footprint 异常小或者地址越界，这三项能把它查出来。
4. 在 `cu8` 下跑 baseline，确认在模拟预算内能跑完全部 kernel。跑不完就先缩小 `prefix_len` 或 `batch`，并把调整记录下来。

## 验收标准

- [ ] 合理性检查通过（kernel 数、footprint、无越界）
- [ ] `cu8` 下能跑完一层，`STATUS.md` 里记录周期数和墙钟时间
- [ ] 产出 T2 的 M1、M2、M3、F，并且**分 kernel 报告**（重点看 attention 和 FFN 的对比）

## 注意

- 共享前缀的 KV 在内存里只能有一份，所有请求都指向它。否则就失去了共享的意义。
- WG 和 (请求, 头) 的映射方式会影响跨 GPM 的共享。用最自然的映射（比如按 头 × 请求 线性展开），并写进 meta，**不要为了制造共享去调这个映射**。

> **操作步骤**：见 `../guides/G1_port_workload_to_mgpusim.md`。建议的移植顺序是 GCN → embedding → LLM → SSB。


> **10-13 注**：文中提到的 `cu8` 目前是 `[提案]`（偏离论文配置，等用户确认）。在用户批准之前，**一律用论文配置（`../CONFIG.md`）**代替 `cu8`。
