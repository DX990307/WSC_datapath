# T5d 新 workload：SSB hash join（`ssb-join`）

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]` · **周次**：W1–W2 · **依赖**：T1（**需要 atomic CAS**）、T0 · **工作量估计**：3–4 天

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧；审稿人 B 要求说明读写并发下的一致性。新主线 `[提案]` 中，机制 1（T7）在区域内复制共享的远程数据，机制 2（T8）用写签名做选择性失效，让只读数据跨 kernel 保留。
> **新 workload 的三条原则**：①每个负载要能回应一条审稿意见，或者验证一个机制；②用真实的场景和数据；③所有负载都报告共享度（T2 的 M1）。
> **通用约束**：kernel 一律用 fp32 或 int32；编译流程沿用 T1；输入规模要保证 kernel 能在模拟预算内跑完（见 T4）。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 为什么选它

- GPU 数据库分析是现代负载，Star Schema Benchmark（SSB）是这个领域的标准 benchmark。Crystal（Shanbhag et al., SIGMOD'20）给出了简单的 GPU kernel 写法，可以直接参考。
- **维度表的哈希表很小，被所有 GPM 读取**，适合机制 1。
- **build 阶段写哈希表，probe 阶段读哈希表，中间跨越 kernel 边界**。这正好检验机制 2：build 阶段写过的数据必须被正确失效，或者根本不应该被保留。这也回应了 B 的一致性问题。

## 规格

- **数据**：用 SSB 的 dbgen 生成，SF 1（lineorder 约 600 万行）。用脚本把需要的列转成二进制 int32 文件。
- **查询**：先做 **Q2.1**（lineorder 和 part、supplier、date 做 join，然后按年份和品牌分组聚合）。有余力再做 Q3.1。
- **kernel**（仿照 Crystal 的结构）：
  1. 每张维度表一个 **build** kernel：先做过滤，再用 atomic CAS 插入开放寻址的哈希表
  2. **probe + 聚合** kernel：扫描 lineorder 的相关列，依次 probe 各个哈希表，然后用 atomic 做分组聚合
- **估计 footprint**：lineorder 每列约 24 MB，用到 4–5 列，共约 100–120 MB；哈希表一共几 MB。

## 要做什么

1. 准备 dbgen 的生成脚本和列转换脚本，输出路径写进 meta。
2. 写 kernel，参考 Crystal 的写法（Crystal 是 CUDA 实现，要改写成 OpenCL 或 HIP）。
3. 用模拟统计做合理性检查，**不单独跑 verify**（用户 10-12 的决定）：①`kernels_completed` 等于预期的 kernel 数；②实际访问的 footprint（T2 的 F）和按数据结构大小估算的值相差不超过 20%；③日志里没有越界地址或 page fault 警告。kernel 参数没对齐时，通常会出现 footprint 异常小或者地址越界，这三项能把它查出来。
4. 在 `cu8` 下跑 baseline。

## 验收标准

- [ ] 合理性检查通过（kernel 数、footprint、无越界）
- [ ] `cu8` 下能跑完 build 和 probe
- [ ] 产出 M1（**probe 阶段哈希表的区域共享占比**）、M2（**哈希表从 build 到 probe：被写过之后又被读的比例**）、M3、F

## 注意

- SF 1 如果在模拟预算内跑不完，可以用 SF 0.5（dbgen 支持小数 SF），把调整记录下来。

## 调研补充（10-12，Cowork 侧）

- Crystal（`anilshanbhag/crystal`）：MIT 许可，CUDA 实现（`crystal/*.cuh` 里是 block 级的 device 函数），`src/ssb/` 下实现了 SSB 的 13 个查询（比如 `make bin/ssb/q11`）。**Q2.1 对应 q21。**
- 数据准备：在 `test/` 下编译 `ssb/dbgen` 和 `ssb/loader`，然后运行 `python util.py ssb <SF> gen` 和 `python util.py ssb <SF> transform`，生成列存格式的数据。SF 和数据路径在 `src/ssb/ssb_utils.h` 里设置。
- 移植路线：如果 T1 确认 HIP 路线可用，就用 hipify 转换后小改；否则手工改写成 OpenCL。Crystal 大量使用模板和 shared memory，**手工改写成 OpenCL 的工作量会明显增加**，所以先等 T1 的结论。

> **操作步骤**：见 `../guides/G1_port_workload_to_mgpusim.md`。建议的移植顺序是 GCN → embedding → LLM → SSB。


> **10-13 注**：文中提到的 `cu8` 目前是 `[提案]`（偏离论文配置，等用户确认）。在用户批准之前，**一律用论文配置（`../CONFIG.md`）**代替 `cu8`。
