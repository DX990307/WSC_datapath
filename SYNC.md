# 同步与备份（Cowork ↔ 本地 Claude Code ↔ GitHub）

> 用户 10-13 的要求：**每完成一个任务就备份到 GitHub；每次用户和 Cowork 聊完、或者和 Claude Code 聊完，另一方都要先读一遍被改动的文本再继续工作。**
> 本文件分两部分：上半部分是规则，下半部分是**只追加、不删除**的变更日志。

## 1. 三方分工

| 角色 | 能做什么 | 备份方式 |
|---|---|---|
| **Cowork 侧 Claude** | **只读代码，不改代码**；改 `isca27/` 下的文档（README、CONFIG、background、tasks、guides、prompts、site）；维护记录网页 | 不能 commit/push（它的 shell 里 git 写操作不可靠）；改完之后在下面的日志里追加一条，由 Claude Code 代为提交 |
| **本地 Claude Code** | **唯一负责修改代码**（模拟器、benchmark、运行脚本、分析脚本）；跑实验；写 `STATUS.md` | 负责所有 git 提交和推送 |
| **GitHub** | 只用一个仓库 `DX990307/WSC_datapath`：代码在 `isca27/...` 任务分支上；文档（本目录，本地分支 `main`）推送到同一仓库的分支 **`isca27/docs`**（用户 10-13 决定，不新建仓库） | — |

## 2. 同步流程

**Claude Code 每次开始工作（每个新会话、每个新任务、每次从“⏸”恢复）之前：**
1. 读本文件的日志，找出自己上一条之后的所有条目（尤其是 `[cowork]` 条目）。
2. 在 `isca27/` 里运行 `git status` 和 `git diff`，**逐个读完 Cowork 改过但还没提交的文件**。
3. 把这些改动用 `cowork: <摘要>` 作为提交信息提交，然后推送（这样 Cowork 的改动也有备份）。
4. 读完之后，如果发现任务规格变了，先在 STATUS 里写明理解到了什么变化，再继续。

**Claude Code 每完成一个任务（或一个阶段的⏸停点）：**
1. 更新 `STATUS.md`。
2. 在本文件日志里追加一条 `[claude-code]`：改了哪些文件、代码 commit、结果目录。
3. **代码仓库**：提交，然后 `git push origin <任务分支>`。**只推送任务分支**（`isca27/...`）；不推送 `main`/`mechanism_1` 等已有分支，不 force push。
4. **文档仓库**：提交 `isca27/`，并推送到 `origin` 的 `isca27/docs` 分支。
5. **实验结果**：`results/` 很大，不进 git；把每个实验的 `meta.json` 和 `summary.csv` 复制到 `isca27/results_index/<exp_id>/`，随文档仓库一起推送。
6. 在日志条目里写上两个仓库推送后的 commit hash。

**Cowork 侧每次被用户叫回来（“看一下进度”等）时：**
1. 读本文件日志里自己上一条之后的条目。
2. 读 `STATUS.md` 的改动，以及 `isca27/` 的 `git log` 和 `git diff`。
3. 如果 Claude Code 改了任务文档（需要用户同意才能改），读这些改动。
4. 根据读到的内容更新记录网页；改完文档之后在日志里追加一条 `[cowork]`。

## 3. 文档仓库（10-13 用户决定：作为代码仓库的一个分支）

- `~/Desktop/CuPath/isca27/` 是独立的 git 仓库（本地分支 `main`），推送到 `DX990307/WSC_datapath` 的分支 `isca27/docs`。这个分支和代码分支的历史互不相关，不影响任何已有分支。
- 首次推送需要用户在终端执行一次：`cd ~/Desktop/CuPath/isca27 && git remote add origin git@github.com:DX990307/WSC_datapath.git && git push -u origin main:isca27/docs`。之后 Claude Code 每个停点推送 `git push origin main:isca27/docs`。
- 不要把 `isca27/` 搬进代码仓库的工作区。

---

## 4. 变更日志（只追加）

格式：`### <日期 时间> [cowork|claude-code] <一句话>`，下面列出改动的文件和要点；Claude Code 的条目还要写上两个仓库的 commit。

### 2026-10-13 [cowork] 补记：10-12 至 10-13 Cowork 侧建立和修改的全部文档
- 新建：`README.md`、`00_background.md`、`STATUS.md`（模板）、`CONFIG.md`、`SYNC.md`（本文件）、`tasks/T0–T13`、`guides/G1`、`prompts/P01–P05`、`site/`（记录网页的源文件备份）。
- 要点：论文配置不可修改（D14）；GPM 改成 1/8 MI300X（D15，T13）；所有运行保留 `-magic-memory-copy`（D16）；T12 driver 修复（D17）；新增同步与备份规则（本文件）。
- 本次另外修改：`tasks/T12_driver_flush_fix.md` 给“10-13 用户决定”一节加注：R2/R3 在这个决定之前已经按用户当时的确认跑完，只用于 T12 验证，不进论文。

### 2026-10-13 [cowork] 明确分工：代码只由 Claude Code 修改
- 用户 10-13：Claude Code 负责修改代码。`SYNC.md` 第 1 节写明：Claude Code 是唯一修改代码的一方；Cowork 只读代码，只改 `isca27/` 下的文档。`README.md` 同步加了这一句。

### 2026-10-13 [cowork] 新增 prompts/P06（取代 P05）
- P06 合并了三部分：建立同步与 GitHub 备份（原 P05）、T13 阶段 1 三条待决事项的答复，以及核实两处论文和代码的不一致（HBM 带宽、filter 容量）、继续 T13 阶段 2。

### 2026-10-13 [claude-code] 按 P06 同步，建立文档仓库和结果索引；GitHub 推送待用户放行
- 读了 SYNC.md、README“同步与备份”、T12 文档新增的 magic copy 决定和加注、site/、P05、P06。新的规格：所有运行保留 `-magic-memory-copy`；T12 R3 改为带 magic copy 跑一次（待配置确认）。
- 文档仓库：`git init`（分支 `main`），`.gitignore`；提交 `fb0eacd` “cowork: initial import of isca27 docs”、`3332d37` 结果索引（`results_index/`，T11 16 个 + T12 10 个），以及本条日志所在的提交。**还没有 remote**：本机没有 `gh`，没法建 GitHub 仓库。
- 代码仓库：任务分支都已提交、工作区干净：`isca27/t11-probe-placement-comm` `d6b765c`、`isca27/t12-driver-flush-fix` `36bde23`、`isca27/t13-mi300x` `f444076`（T13 阶段 2 的实现已全部提交）。**`git push` 被 Claude Code 的权限规则拦下，没有推送。**
- STATUS：新增“需要用户决定”两条（T13 阶段 2 / T12 R3 的配置确认；GitHub 推送放行）。

### 2026-10-13 [claude-code] 用户决定：文档备份改为代码仓库的分支 `isca27/docs`
- 用户 10-13：不新建 `DX990307/cupath-isca27`，在当前代码仓库 `DX990307/WSC_datapath` 里新建分支来备份。文档仓库（本目录，分支 `main`）推送到 `origin` 的 `isca27/docs`；它和代码分支的历史互不相关，不影响任何已有分支。
- 这和本文件第 1、3 节写的“文档仓库是单独的私有仓库”不一致，需要 Cowork 侧更新第 1、3 节（我只追加日志，不改规则部分）。
- 推送仍被 Claude Code 的权限规则拦下，代码的三个任务分支和 `isca27/docs` 都还没有推送。

### 2026-10-13 [claude-code] 代码仓库的三个任务分支已推送；文档分支待用户推送
- 用户同意放行推送，在 `~/Desktop/CuPath/.claude/settings.local.json` 加了只针对 `isca27/*` 分支的 `git push` 规则。
- **代码仓库 `DX990307/WSC_datapath` 已推送**（都是新分支，没有 force push，没有碰已有分支）：`isca27/t11-probe-placement-comm` → `d6b765c`，`isca27/t12-driver-flush-fix` → `36bde23`，`isca27/t13-mi300x` → `f444076`。
- **文档仓库还没有推送**：在 `isca27/` 里加 remote 并推送 `main:isca27/docs` 的操作被 Claude Code 的自动模式拦下（判为数据外传），没有执行；需要用户在终端里自己推一次（命令见 STATUS）。本地 `main` 的最新提交见本条日志所在的提交。

### 2026-10-13 [claude-code] 配置文件 `-config`；不再用 `-disable-servers`
- 用户决定：配置放进专门的配置文件（`akkalat/configs/*.json`，`-config` 读取，可叠加、可继承），命令行只写配置名和 benchmark 参数；不用 `-disable-servers`。
- 代码 commit `1b1e829`，已推送 `isca27/t13-mi300x`。CONFIG.md 第 2、3 节写的“完整参数要从 runall2_constants.py 导入”的流程，现在对我的任务脚本变成“读 `configs/hpca.json`”（两者由测试保证一致），需要 Cowork 侧更新 CONFIG.md 的说明。
- 启动了两组运行（yss3）：配置文件 + 开着服务的 fir 验证、T12 R3（带 magic copy）。

### 2026-10-13 [claude-code] T13 阶段 2 完成（⏸）
- 旧配置回归 fir、matrixmultiplication 修改前后完全一致；配置文件 + 开着服务的 fir 和写全参数的结果完全一致；T12 R3（带 magic copy）通过。详见 STATUS。
- 代码：`isca27/t13-mi300x` 在 `1b1e829`（已推送）。结果索引新增 4 个目录。
- 文档仓库：分支 `isca27/docs` 仍未推送（需要用户手动推一次，见 STATUS）。


### 2026-10-13 [cowork] 同步 Claude Code 的改动：文档备份方式、配置文件、T13 阶段 2；确认投稿版 Table II
- 读了：SYNC 日志里 [claude-code] 的 6 条；STATUS 中 T13 阶段 2、配置文件、(a)(b) 核实的全部内容；`configs/hpca.json`、`configs/mi300x_eighth.json`。
- 改了：`SYNC.md` 第 1、3 节（文档备份改为 WSC_datapath 的 `isca27/docs` 分支）；`CONFIG.md`（新增 10-13 更新说明：配置文件、不用 `-disable-servers`、新的基准 commit `1b1e829` 和 blob 表、两条检查方法）；`00_background.md`（投稿版 Table II 的事实，见下）。
- **(a)(b) 的补充事实**：Cowork 手上有用户上传的投稿 PDF。投稿版 Table II 写的是“HBM 8 GB/GPM, four controllers, **0.375 TB/s**, BL4”和“Typed Filter: One shared array/L2 partition, **4K buckets**, four slots/bucket”；正文写每个 filter 42 KiB、每 GPM 168 KiB、规则阵列 2.561 mm²/GPM、占 L2 面积 **4.70%**。所以：投稿版的 HBM 带宽（0.375）和代码（0.128 TB/s/GPM）不一致；投稿版的 filter 容量和面积都是按 4K bucket 算的，而实际运行用的是 8K bucket（面积应按 336 KiB/GPM、6.86% 计）。新论文的 Table II 和面积数字要从代码和 meta 自动生成。

### 2026-10-13 [cowork] 新增 T14（wafer 平台特征数据）和 prompt P07
- 新建 `tasks/T14_wafer_platform_data.md`：第一档 7 项（延迟阶梯、带宽—CU 数、跳数延迟矩阵、单对远程带宽—在途请求数、incast、典型通信模式、kernel 边界开销）和 T13 阶段 3 一起跑，两套配置都测；第二、三档是提案。写明了 footprint 和测量方法的规则（指针追逐只走固定步数、先热身、页内/跨页两组、带宽测量的 footprint 规则）。
- 改了：`README.md`（索引加 T14）、`tasks/T13_mi300x_config.md`（末尾补充“阶段 3 和 T14 一起跑”）。
- 新建 `prompts/P07_T13_stage3_T14.md`。
- 另外：`site/` 的 3 个页面更新为最新版（上一次同步时没能写到电脑上）。

### 2026-10-13 [claude-code] 同步 P07；实现 platbench；T13 阶段 3 + T14 配置确认（⏸）
- 读了 Cowork 的 2 条日志和全部改动（SYNC、CONFIG、00_background、README、T13、T14、site、P07），提交 `eecffb4`（cowork: ...）。
- 代码 `0e38983`（已推送 `isca27/t13-mi300x`）：微基准集 `platbench`（T14 第一档 7 项）。`gpm_preset.go` 新增 `CUsPerGPM()`，blob 变成 `4a149c51b45a`（配置数值没变），需要更新 CONFIG.md 的 blob 表。
- STATUS：配置确认和运行计划，等用户确认。

### 2026-10-13 [claude-code] T13 阶段 3 + T14 第一档运行完成；校准偏差超过 10%（⏸）
- 代码：`isca27/t13-mi300x` 推到 `ca7ea0b`（新增 `a4738c8` 汇总脚本 `akkalat/t14_analyze.py`；`ca7ea0b` 修正 platbench 的指针追逐：原来被编译成标量加载，测的是 L1 标量缓存）。模拟器和配置没有改动。
- 文档：本条所在的提交（推到 `isca27/docs`）。只改了 `STATUS.md`、`SYNC.md`；`results_index/` 新增 58 个 `1013_t14-*` 目录和 `platform/` 汇总，README 补了两行索引说明。
- STATUS：T13 阶段 3 校准表、T14 平台数据摘要表、“和预期不一样的现象”8 条；“需要用户决定”新增一条：mi300x HBM 读 −23%、L2 带宽 −69%（两套配置都一样），按规则停下来，推荐先做诊断（只加统计、不改数值）。T13 阶段 4 没有开始。
- 给 Cowork：CONFIG.md 的 blob 表里 `gpm_preset.go` 需要改成 `4a149c51b45a`；T14 第一档验收标准的 5 条都已完成，只有一处缺口：摘要表里单对远程带宽的“瓶颈”没有 RDMA 表满占比的统计证据（模拟器没有这项统计，见“规格与代码的差异”）。

### 2026-10-13 [claude-code] T13 阶段 4 启动（用户确认）
- 代码：`isca27/t13-mi300x` 推到 `9220740`（敏感性脚本去掉 `-disable-servers`；新增 `akkalat/isca27_stage4.sh`）。
- 文档：本条所在的提交（推到 `isca27/docs`）。只改了 `STATUS.md`、`SYNC.md`。
- STATUS：T13 阶段 3 的偏差按用户决定暂时接受实测值；阶段 4 的配置确认、运行计划和启动记录见日志。

### 2026-10-13 [cowork] 同步 T13 阶段 3、T14 第一档结果和阶段 4 启动；新增 ideas_from_T14.md
- 读了：SYNC 日志里 [claude-code] 的 3 条；STATUS 的“T13 阶段 3 校准”“T14 第一档平台数据摘要”“和预期不一样的现象”8 条、阶段 3/4 的日志（用户决定暂时接受实测值、阶段 4 已在 19:14 UTC 启动）。
- 改了：`CONFIG.md` 的 blob 表（`gpm_preset.go` → `4a149c51b45a`）。
- 新建：`ideas_from_T14.md`（全部 `[提案]`）：I1 端点受限的问题重定位；I2 owner 端合并共享读；I3 多 line 远程事务；I4 用数据给 T7 定量（距离换带宽、分层副本）；I5 远程地址转换。以及三条先决条件：端点模型合理性（建议端点敏感性实验）、瓶颈统计、本地 L2 带宽异常的诊断（建议和阶段 4 并行）。

### 2026-10-13 [claude-code] T13 阶段 4 重启（每台 16 个位置一直占满）
- 代码：`isca27/t13-mi300x` 推到 `260ce73`（`slot_pool.py` 共享位置；`runall2.py` 调度器可选接入；`runall2_process.py` 在没有 `.git` 时接受调用方的 commit）。
- 文档：本条所在的提交（推到 `isca27/docs`）。只改了 `STATUS.md`、`SYNC.md`。

### 2026-10-14 [claude-code] 修复不确定性和内存泄漏；阶段 4 停止
- 代码：`isca27/t13-mi300x` 推到 `756f214`（`f1ef3f0` 远程批量读按顺序处理；`756f214` 四处内存泄漏）。
- 文档：本条所在的提交（推到 `isca27/docs`）。只改了 `STATUS.md`、`SYNC.md`。
- STATUS：阶段 4 卡死约 13 小时的经过、修复、停止；重启前待定事项。另外 STATUS 里有内存控制器审查和 Local Pairing 相关发现，涉及论文 Table II、DRAM 模型和 Local Pairing 的论述，可能需要更新 CONFIG.md 和任务文档。

### 2026-10-16 [cowork] 同步 10-14 到 10-15 的全部进展；发现同步规则有两处没执行
- 读了：SYNC 日志 10-13 到 10-14 的 [claude-code] 条目；STATUS 10-13 19:40 到 10-15 20:00 的全部日志（阶段 4 内存问题和停止、不确定性和内存泄漏修复、内存控制器审查、Local Pairing 实测、改用 mi200、27 人瓶颈审查、诊断实验 A–D、yss2 硬件故障、mi200 实现和验证、mi200 HBM 读只到 31%）；代码仓库 `configs/mi200.json`。
- 改了：`00_background.md`（新增 D19–D22，D15 标为被取代）；`CONFIG.md`（顶部加 10-16 说明：目标配置改为 mi200，冻结前 hpca 规则仍有效）。
- **同步规则没执行的两处**：① 10-14 之后 STATUS 和 git 有十几次提交，但 SYNC 日志只到 10-14 的第一条；② Cowork 10-13 的改动（`ideas_from_T14.md`、`CONFIG.md`、`site/` 三页）一直没被提交。另外 10-14、10-15 的新结果（诊断 A–D、mi200 平台测量、mi200/hpca 短片段 ablation、可复现性检查）都不在 `results_index/` 里，STATUS 里也只有部分数字，Cowork 看不到。
- 新建 `prompts/P08_sync_and_results_review.md`。

### 2026-10-16 [cowork] 记录改用 MI200 的原因；补充“不用 Local Pairing 时的替代方向”
- 改了：`00_background.md` D19 的理由（用户 10-16：MI300X 有很多新架构，模拟器没法实现）；`ideas_from_T14.md` 末尾新增“10-16 补充”：L-A 不再做本地机制（推荐）、L-B owner 端服务优先级、L-C 按重取代价管理 L2，以及不建议的方向。全部是提案。

### 2026-10-16 [cowork] 记录“本地还是 wafer 层”的讨论和 oracle 实验建议
- 改了：`ideas_from_T14.md` 末尾新增一节：看远程时间占比而不是访问个数；本地越好 wafer 层空间越大；“大部分在本地”是旧 benchmark 的性质；建议用 oracle 实验（O1 `-force-local-data-access`、O2 端点不受限、O3 距离 1 跳）量出每个 workload 的上限后再决定方向。全部是提案。

### 2026-10-16 [cowork] 新增 T15（oracle 实验）和 prompt P09
- 新建 `tasks/T15_headroom_oracle.md`：mi200 短片段，14 个 workload × 6 组（B、O1 `-force-local-data-access`、O2a L1V 窗口放大、O2b RDMA 窗口放大、O2 两者都放大、O3 `-switch-latency=1`），全部加 `-report-l2-source`；有效性检查（指令数差异 > 1% 标为无效）；输出每个 workload 的上限、远程访问和延迟占比、m2/Complete 占上限的比例。
- 改了：`README.md`（索引加 T15）。新建 `prompts/P09_T15_headroom_oracle.md`。

### 2026-10-16 [claude-code] 补记 10-14 到 10-16 的提交；P08 完成；T15 停在第 1 步（⏸）
- **补记（10-14 之后没写进 SYNC 的提交）**：代码分支 `isca27/t13-mi300x`：`3aa570e`（DRAM 建模：pseudo channel、行保持、refresh、tFAW、HBM2 写时序，默认全关）、`0b60da8`（mi200 预设和 `configs/mi200.json`）、`67f1c03`（runall2 启动间隔、不在子进程暂停时启动、STALL 提示）、`313a141`（画图脚本）、`c0d59d7`、`d9160e6`（结果目录命名 `isca27_name.py`）、`7b786a2`（`exp.json` 命名规则、`run.json`）、`21fddba`（`exp.json` 的 simulators/campaigns/plots 和 `akkalat/exp_cells.py`）。已推到 `21fddba`。**没有打标签**（`isca27-mi200-v1` 等用户确认冻结）。文档：`6b3b5be`、`5492a61`（STATUS 10-15 两条）。
- **本轮**：Cowork 改动以 `cowork:` 提交（`86bf7f7`）。P08：`results_index/` 新增 10-14 到 10-16 的结果（新命名）和 `platform_mi200/`；STATUS 新增 P08 七张表、mi200 验证清单、复核意见 (a)–(d)（(a) m3 在 kernel 边界不失效，和论文 IV-C 不一致，代码位置见“规格与代码的差异”）。改了 `STATUS.md`、`SYNC.md`、`results_index/`。
- **结果目录**：yss2 已改为新命名（`1015_abl_wg32k_hpca_67f1c03` 等，见 `results/RENAMES.tsv`）；yss3 待改。
- **⏸ 等用户**：mi200 是否冻结、每 slice MSHR 是否改为 128（影响 T15 是否要等重跑）；复核意见 (a)–(c) 的方案选择。
- 文档推送 commit：见本条所在的提交。

### 2026-10-16 [claude-code] mi200 冻结（isca27-mi200-v1）；T15 配置确认（⏸）
- 用户确认冻结（MSHR 保持 32）。代码 `isca27/t13-mi300x` 推到 `61b6ae8`（偏差基准改为 mi200.json、`akkalat/configs/MI200_V1.md`）。标签 `isca27-mi200-v1` = `67f1c03`，**只在本地**，等用户决定是否推送。
- **请 Cowork 更新 `CONFIG.md`**：有效论文配置改为 `configs/mi200.json`（冻结版），`config_deviation` 相对它计算；`hpca.json` 是投稿版；mi200 有意弱化的清单见 `MI200_V1.md`（D20、D21）。
- T15：配置确认写在 STATUS“需要用户决定”，等用户确认后运行。改了 `STATUS.md`、`SYNC.md`。

### 2026-10-16 [cowork] 记录用户贴出的 mi200 短片段 ablation；T15 增加三项检查
- 用户贴出 `1015_abl_wafer48_mi200_67f1c03` 的图：GMEAN RA 1.24、RR 1.10、LP 1.03、Complete 1.30；收益集中在 FWS、KM、MM（和 I2C）；FWT 的 LP 和 Complete 约 0.88。
- 改了：`tasks/T15_headroom_oracle.md` 末尾新增“10-16 补充”：按 O1 上限给 workload 分类（事先定好标准）、检查 KM/MM/FWS 三个大收益是否来自 baseline 的人为因素（放置热点、baseline 不缓存远程数据）、查 FWT 的退化。

### 2026-10-16 [cowork] 记录“用 thread placement 利用 wafer 特性”的讨论
- 改了：`ideas_from_T14.md` 末尾新增一节：已有工作（Milic MICRO'17、Khairy MICRO'20、CODA、Griffin）；wafer 特有的三个角度（二维拓扑映射、避开热点 owner、搬计算比搬数据便宜）；上限由 T15 的 O1、O3 给出；建议的便宜实验 P1 对齐、P2 拓扑顺序、P3 小 buffer 交错（需要改少量代码，等用户决定是否加进 T15）。全部是提案。

- 2026-10-16 · cowork · ideas_from_T14.md 的 placement 一节新增“10-16 补充”：哪些负载可能受益；只缩短距离为什么收益有限；建议把 placement 做成强 baseline；新增 O4 放置 oracle；placement 以外的三条路（a 用带宽换延迟、b 远程翻译、c 空间/流水线执行）。另外把行优先编号换行处的距离从 6 跳改为 7 跳。等用户决定 O4 和 P1–P3 是否加进 T15。

- 2026-10-16 · cowork · ideas_from_T14.md 新增一节“placement 以外的办法”，列了 W1–W6：位置导致的负载不均（角落 GPM 远程延迟约 639 ns，靠近中心约 465 ns）、窗口受限、incast、远程翻译、全局同步、空间/流水线执行。另外记了 baseline 本身的三个问题、可能的论文故事（受限于端点和位置），以及 T16 瓶颈分解的建议（只加统计）。等用户决定。

- 2026-10-16 · cowork · 用户决定试 W1（位置导致的负载不均）。新建 `tasks/T16_position_imbalance.md`：阶段 1 已定，只测量（per-GPM tracer、截断运行里的速率不均、跑完整个 kernel 的 all-to-all 微基准、少量 workload 的小输入）；阶段 2 是提案（M-a 加权切分、M-b 只向邻居偷 WG、M-c 按距离分配在途窗口）。新建 `prompts/P10`；README 索引加了 T16。读代码时发现：WG 静态连续切分；页按 48 等分；**`-max-wg` 是全局计数，到了就退出，所以截断运行看不到 kernel 尾部**，W1 必须用跑完整个 kernel 的运行来评估。

- 2026-10-16 · cowork · 从 T14 的原始结果（`results/1013_t14-pat-*/stats/metrics.csv`）读出每个 GPM 的 kernel_time，作为 W1 的已有证据：all-to-all 最慢/平均 1.23、最慢/最快 1.43、ρ = 0.93；环形的行尾和回绕让 kernel 慢约 4 倍。新增 `figs/t14_alltoall_per_gpm.png`。T16 加了“已有的证据”一节；1c 改为复用 platbench，并加远程比例扫描；P10 同步修改。确认截断运行里每个 GPM 的 kernel_time 都是 0。

- 2026-10-16 · cowork · 讨论 W1 是否需要新 workload（`ideas_from_T14.md` 新增一节，提案）：需要；用 unified 写法（W1 的机制作用在 driver/CP 的 WG 分配上）；最小集合是 MoE、DLRM embedding、图计算 PR/BFS，可选 TP 的 LLM 层；旧的远程型 benchmark 用小输入，AES 作为对照。等用户决定。

- 2026-10-16 · cowork · 用户希望找“最新的、page placement 帮不上忙”的 workload。`ideas_from_T14.md` 新增一节（提案）：第一档是 MoE dispatch/combine、PagedAttention decode、R-GAT 采样聚合；第二档是 CAGRA 向量检索、DLRM embedding；用 O4 放置 oracle 证明“放置帮不上”；T5a/T5b/T9 相应调整；补了一条相关工作 arXiv 2511.02132。等用户决定是否开 T17。

- 2026-10-16 · cowork · 读代码确认模拟器的约束（`ideas_from_T14.md` 新增“模拟器能不能跑”一节）：没有 global atomic、没有 scratch、LDS 只实现了 7 条 DS 指令、编译链是 OpenCL→fiji。由此定下写法原则：不用 atomic、统一 pull、索引结构在 host 端算。可行性排序：PagedAttention 和 DLRM 高，MoE 中高，R-GAT 中，CAGRA 中低。T1 要作为新 workload 的第一步。

- 2026-10-16 · cowork · 用户要求每个实验 3–5 小时。`ideas_from_T14.md` 新增“模拟速度预算”一节：T14 实测本地约 1.5–1.8 GB/小时、远程约 200–250 MB/小时（远程每字节约贵 7 倍）；mi200 短片段中位数 48 分钟，KM 12.5 小时超出预算。粗估模型：wall 分钟 ≈ 3 + 远程 MB/3.5 + 本地 MB/27。给出了 4 个新 workload 的初始规模；规定先按 1/8 规模试跑、再外推定规模，不用 `-max-wg`。

- 2026-10-16 · cowork · 用户同意 T17（“可以试试吧”）。新建 `tasks/T17_latest_workloads.md`（PagedAttention、MoE、R-GAT，DLRM 备选；第 0 步就是原来的 T1；写法原则是 unified、不用 atomic、pull、索引在 host 端算；先 1/8 试跑，再外推到 baseline 约 3 小时，上限 5 小时）和 `prompts/P11`。README 里 T5a/T5b/T5c/T5d/T9 标为被取代或暂停，这些文档的开头也加了说明。

### 2026-10-17 [claude-code] T16 阶段 1 开发完成，配置确认（⏸）；T17 开发中
- 代码：`isca27/t16-position` 推到 `2e09bc9`（per-GPM tracer `-report-gpm-trace`、platbench `remotefrac`、`*-t16small` 输入、`akkalat/t16_heatmap.py`）；回归通过（fir、MM，tracer 关/开和改动前完全一致）。`isca27/t17-workloads` 在本地开发中（没推送）：第 0 步完成、补了 6 条指令（用户批准）、W-A/W-B/W-C 已实现、数据集已下载到 yss2 测试目录。
- 文档：STATUS 新增 T16 配置确认、T17 进度。改了 `STATUS.md`、`SYNC.md`。
- ⏸ 等用户：T16 配置确认（1b 跑不跑）；T17 的三个问题（`v_mad_u64_u32`、W-C 简化、kmeans/kmsc 的 coherent 开关）。

### 2026-10-17 [claude-code] T17 第 2 步规模确认（⏸）
- 代码：`isca27/t17-workloads` 推到 `db47df3`（16 个提交：编译检查、emu 启动修复、pagedattn/moe/rgat、6 条指令（用户批准）、opt-in coherent D2H 拷回及其修复（PID 不匹配）、`-rgat-heads`、回归记录）。回归：fir、MM 改动前后 total_time 和全部统计（排序后）完全一致。
- 文档：STATUS 新增 T17 规模确认和三个发现（`s_nop` 死循环、kernel 之间不失效 L1、`v_mad_u64_u32` 扫描）。
- ⏸ 等用户：T17 规模；`s_nop` 是否在模拟器里修；`v_mad_u64_u32` 是否修。

### 2026-10-17 [claude-code] T17 第一轮 baseline 完成（⏸）；`s_nop`、`v_mad_u64_u32` 修好
- 代码：`isca27/t17-workloads` 推到 `c9cdf64`（`3a94d26` SOPP 指令推进 PC、`b5cff1b` 488 改 VOP3b、`c9cdf64` 编译检查）；回归 fir、MM、llmop gemm 完全一致。
- 结果：`results_index/t17/`（summary.csv、每个运行的 gpm_trace、l2_summary、l2_remote_matrix、meta、10 张热力图）。远程读 95–97%、远程延迟占 99.9%；PagedAttention、MoE 的 owner 均匀，R-GAT 一个 owner 占 16%；尾部比 1.2–1.75，但和位置相关性弱。
- 文档：STATUS 更新 T17 状态、结果摘要、两条新的待决事项、日志；改了 `STATUS.md`、`SYNC.md`，新增 `results_index/t17/`。
- ⏸ 等用户：R-GAT 的 GPM 利用率（A 保持 / B 改 kernel / C 改 WG 分配）；W1 用 T17 再判断的结论。

- 2026-10-17 · cowork · 读了 Claude Code 10-17 的 4 条日志、STATUS 里 T16 阶段 1 和 T17 第一轮的结果（T15 的结果还没写进 STATUS），并用 `results_index/t17/*.gpm_trace.csv` 补算了三件事：每个 GPM 的忙碌时间由它的远程排队延迟决定（ρ 0.87–0.99）；速率 × 延迟 ≈ 448 个在途（L1V 槽位全满）；粗略的均衡上限（PagedAttention 1.75×、MoE 约 3×、R-GAT 约 6×）。`ideas_from_T14.md` 新增一节“10-17 Cowork 读 T16/T17 结果”：W1 不作为主线；W2（端点排队）证据变强；提出 C′ 均衡连续切分（修 baseline，同时修好 kmeans 的 WG 段和页段错开）；建议 T15 的 oracle 用在 T17 上，并加延迟分解统计。等用户决定。

- 2026-10-17 · cowork · 读了 `~/Desktop/CuPath/findings_T15_T16_T17_2026-10-17.md`（T15 82/84 格）。`ideas_from_T14.md` 新增“补充”一节：O2a 无效、O2b 有效 → 瓶颈在 RDMA 窗口，m2 已经基本拿到；FW 剩下的空间（2.5× 对 11×）假设来自第 k 行的 incast → owner 端合并/多播是候选机制（要先统计 owner 集中度和跨 GPM 共享度 M1）；KM 和 MM 的收益有放置造成的人为因素；截断运行的规则改用每条指令的时间，远程多的 workload 用 T16 的完整小输入；PR、MT 先查 O1 的远程计数；R-GAT/MoE 选 B；数据导致的不均不作为贡献；论文故事更新为“端点受限：请求方窗口 + owner incast + 复用”。等用户决定。

- 2026-10-17 · cowork · 讨论“除了机制还有哪些贡献”：`ideas_from_T14.md` 新增 C1–C7（端点受限的特征分析、Little's law 墙、wafer roofline 模型、placement 帮不上忙的现代负载、截断模拟的方法学问题、baseline 人为因素、位置效应），以及建议的组合（ISCA = C1 + C2 + 机制 + C4 + C5/C6；备用是特征分析 + 方法学的论文）。等用户决定。

- 2026-10-17 · cowork · **更正 C2**：之前说“每个 GPM 需要 6,000–30,000 条 line 同时在途”有误：用了负载下的延迟（包含窗口前面的排队），也没扣包头。重新计算（链路 768 B/ns、读响应 80 B 的 wire 开销、RDMA RTT ≈ 84 + 71.4 × 跳数 ns，从 T14 反推）：平均距离下约 3,700 条（1 跳约 1,500，12 跳约 9,000）；64 个在途只能用到链路的约 1.4%，L1V 的 448 个槽位约 9.5%。计算写在 `ideas_from_T14.md` 的“C2 的计算”。

- 2026-10-17 · cowork · `ideas_from_T14.md` 新增“10-17 汇总：现有数据能下什么结论”，分为 A（证据充分：端点受限、旧 benchmark 的空间集中在少数几个、看时间不看个数、现代负载远程主导且排队受限、方法学、Local Pairing 是人为因素）、B（有迹象：m2 拿到窗口收益、FW 的 incast、L1V–RDMA 之间排队、位置效应、I2C）、C（被否定）、D（缺的数据，按优先级）。

- 2026-10-17 · cowork · 用户问“端点受限会不会是仿真精度不对称造成的”。读了 akita 的 mesh 和 switch：链路是 flit 级模型（32 级流水线、浅缓冲、crossbar 仲裁、反压），不是理想链路。但证据有三处薄弱：链路利用率是推算的、从来没压满过链路、结论依赖 RDMA 64 和每跳 32 周期这两个没有出处的参数。A1 降为“待补”。提出 E1（用现成的 `-trace-memory-path` 做远程延迟分解）、E2（链路压满测试）、E3（RDMA × 每跳延迟 × 链路带宽的敏感性扫描）、E4（找参数的硬件出处）。结论的表述改成和参数无关的 scaling 论点。

- 2026-10-18 · cowork · 为 10-19 给老师的汇报做了一个网页（Artifact “CuPath 十月进展”，源文件备份在 `site/presentation_1019.html`），共 12 页，内容是 HPCA 审稿意见、改了什么、新实验、新结果（T14–T17）、正在形成的论点和风险（E1–E4）、下一步和需要讨论的问题。只用了 STATUS、results_index 和 findings 文件里已有的数字，没有新结论。

- 2026-10-18 · cowork · 读了 `isca27/t17-workloads` 里 T17 三个 workload 的代码（pagedattn、moe、rgat 的 kernels.cl、host.go、benchmark.go），在汇报网页里加了一页“新 workload 的实现与 GPM 映射”。**发现一个需要处理的问题**：MoE 现在 95% 的远程读，主要是因为 expert 权重（按 48 等分放置）和计算它的 WG（tile 按 expert 排序，但小 kernel 只挤在前 26–41 个 GPM）没对齐，不是“placement 帮不上”。真实系统用 expert parallel，权重读取是本地的，只有 dispatch/combine 是远程。下一轮 MoE 应该改成 EP 式映射。PagedAttention 理论上可以用知道 WG 映射的块分配器把块放到本地，要用 O4 来量化。

- 2026-10-18 · cowork · 在汇报网页里加了子页面 `t17_design.html`（备份在 `site/t17_design.html`）：按 `isca27/t17-workloads` @ c9cdf64 的源代码整理了三个 workload 的设计，包括模拟器限制和绕开方法、WG/页到 GPM 的规则、每个 kernel 的线程分工、GPM 映射、和真实系统的差距，并附完整源代码（kernels.cl、host/graph/sample/benchmark.go、build/check 脚本）。新发现：R-GAT 的热点 owner 是 tile 48（16.7%，每个 buffer 的余数页）和 tile 0（7.1%，不足 48 页的 buffer），是分配器造成的。

- 2026-10-18 · cowork · 汇报网页每个结果页都加了图，数据直接取自 results_index（platform_mi200、t16、t17 的 CSV）和 STATUS/findings：wafer 编号与远程路径示意图；hpca 和 mi200 下各机制 GMEAN 的对比；T14 的本地延迟阶梯、远程延迟和跳数（329 对，拟合 198 + 71.8 ns/跳）、单对带宽和 WG 数、incast；T15 远程访问的个数占比和时间占比；T16 的 kmeans、pagerank、MM 小输入热力图；T17 的 GPM 占用时间线、热力图、远程延迟和忙碌时间的散点图；Little's law 所需在途数。

- 2026-10-19 · cowork · 确认 10-17 之后没有新的实验结果。用已有数据做了一次核对（`ideas_from_T14.md` 新增“闭环排队模型”一节）：L1V 观测延迟 ≈ (448/64) × RDMA 往返（84 + 71.4 ns/跳），PagedAttention 预测 2.73 µs、实测 2.87 µs，反推在途请求 472 个（饱和），MoE 约 400 个，R-GAT 约 330 个。可检验的预测是：T17 的 O2b 下远程延迟会降到约 0.4–0.5 µs。汇报网页的论点页也补了这条证据。

- 2026-10-19 · cowork · 新增子页面“T15–T17 结果分析”（`site/analysis_1019.html`，从汇报网页导航栏和论点页进入），共 7 节 11 张图：闭环排队模型示意；预测和实测延迟对比；延迟构成（排队占 80–86%、距离占 10–16%）；用 Little's law 从 trace 还原每个 GPM 的在途数（中位 213–408）和吞吐（5–21 B/ns，贴着 64 窗口的上限）；T15 四个 workload 的 oracle 拆分；其他发现；放大窗口后的吞吐预测和判读表；局限。没有新跑实验。

- 2026-10-19 · cowork · 汇报网页在标题页后面加了“工作总览”一页（12 项工作，每项列出时间、关键数据、状态），并新增子页面“工作总览”（`site/worklog_1019.html`），每项一张详细数据表：HPCA 意见、T11–T17、数据放置审查、mi200 消融、模拟器修复、分析和建模。

- 2026-10-19 · cowork · 汇报网页新增 7 个详细页（site/ 下已备份）：config_1019（配置文件化做了什么、验证、三份 json、hpca / mi300x-eighth / mi200 逐项对比、实测 vs 理论、mi200 和真实 MI200 的偏差）、placement_1019（诊断 C 全表、WG 段 / 页段错开的公式示意、6 类原因、T16/T17 里的放置问题、P1–P3）、t14_1019（三套配置全部 7 项测量 + 诊断 A/B，图）、ablation_1019（14 个 workload × 两套配置 × 4 组 + baseline 时间）、t15_1019（实验介绍 + 结果 + 截断有效性问题）、t16_1019（实验介绍 + 13 个运行的表和热力图）、t17_1019（逐个 kernel、按跳数、owner、每 GPM 的数据）。worklog_1019 和 s-work 幻灯片加了链接。只读数据，没有新实验。
- 2026-10-19 · cowork · **新观察（给 Claude Code / 讨论）**：T17 的 l2_summary 里远程读延迟随跳数增长远快于空载——PagedAttention 1 跳 1,879 ns → 10 跳 5,149 ns（约 360 ns/跳）；同一个请求方 GPM 内部按跳数回归，斜率中位数约 180（PA）/ 210（MoE）/ 214（R-GAT）ns/跳，是空载 71 ns/跳的 2.5–3 倍。说明有一部分排队随距离增长（switch、owner 端或时间混杂），analysis_1019 里“距离只占 10–16%”是按空载每跳算的，可能低估。需要 E1 分段延迟来拆；另需核对 l2_summary 的延迟起止点（组件 RDMA→RDMA）。

- 2026-10-19 · cowork · 汇报网页 T14 页的“说明”里加了两条：本地延迟阶梯不够陡（L1V 115 / L2 129 / HBM 198 ns；公开的 MI210 向量 L1 约 85 ns、L2 约 200–300 周期即约 120–175 ns）。原因：向量路径固定开销偏大，L1 偏慢约 30 ns；MGPUSim 的 L1V→L2 是 DirectConnection（约 1 周期，没有片上互连），L2 目录 + bank 只有 20 周期，所以 L2 偏快；HBM 只比 L2 多约 70 ns，可能也偏快。三套配置都沿用 MGPUSim 原值。影响：Remote Reuse / B1 的收益可能被高估，远程排队的结论不受影响。建议：论文写明偏差，另做敏感性实验（L1→L2 加延迟，使 L2 命中约 150 ns），改配置的数值需要用户确认；MI200_V1.md 的偏差表还没加这一条。来源：Chips and Cheese MI210 文章、RRZE gpu-benches。

- 2026-10-19 · cowork · 按用户要求，T14 页的两条说明改成一句：“L2 命中延迟和真实芯片相比偏小（129 ns，只比 L1V 多 14 ns）”（原因分析仍记在上一条 SYNC 里）。汇报网页右下角加了“笔记”按钮：按页记笔记，自动保存在网页的数据库里（collection `notes`，每页一个文档），Cowork 之后可以读取同步。

- 2026-10-19 · cowork · 核查地址转换会不会是远程慢的原因（只读代码和 T16/T17 trace，没有新实验）。配置：页 4 KB；L1V TLB 每 CU 32 项全相联、4 个 MSHR（覆盖 128 KB）；L2 TLB 每 GPM 32 组 × 16 路 = 512 项、32 MSHR（覆盖 2 MB）；本地页走 GMMU（页表遍历 100 周期，16 个在途）；远程页由 L2 TLB 经 mesh 发到中心 tile 的 IOMMUTLB（32 × 64 = 2,048 项，全 wafer 共享，覆盖 8 MB；查找 80 周期 = `-mmutlb-lookup-latency`）→ IOMMUCache（32 组直接映射）→ MMU（页表遍历 100 周期，全 wafer 最多 256 个在途）。trace 结果：T17 三个 workload 的远程 L2 TLB 缺失只占远程读的 1.3–1.6%，每次约 630–765 ns，平摊到每个远程读约 8–12 ns，对 2.2–2.9 µs 的远程延迟占 0.3–0.5%；T16 的 kmeans / pagerank / MM / im2col 小输入 ≤ 0.5%。T15 的 O1 保留了远程地址转换仍有 1.9–11×。结论：真实 workload 的远程时间主要在数据访问，不在地址转换。例外：remotefrac f = 1 远程 TLB 缺失 6%、平均 4.46 µs，翻译总延迟相当于数据延迟的 43%——中心 IOMMU 在排队，可能是 T16 remotefrac 位置效应很弱的原因之一（待查）。trace 只记 L2 TLB 缺失，L1 TLB（每 CU 4 个 MSHR）造成的发射阻塞没有直接统计。

- 2026-10-19 · cowork · **方向（用户决定）**：论文前提是地址转换已经由 HDPAT（用户的 HPCA'26 论文）解决，CuPath 解决数据访问。新增 `tasks/T18_new_m2_two_sided_merge.md`（m2 升级为两端合并：请求方改造的 cuckoo 在途表 + 按距离分配窗口 + owner 端请求者位图表和 mesh 多播；不缓存、不复制、不迁移）和 `tasks/T19_filter_guided_wg_stealing.md`（cuckoo filter 指导的就近 WG 窃取；T17 trace 估算的完美均衡上限 PA 1.74×、MoE 1.61×/2.94×、R-GAT 1.16×/6.18×），两者都是 `[提案]`。新增 `prompts/P12_measurements_for_T18_T19.md`：WG 足迹 tracer → 亲和性、历史稳定性；M1 共享统计；T17 的 O1/O2b/O3（oracle 要配置确认）。用户明确不做页迁移和复制。`ideas_from_T14.md` 新增三节（L2 偏小、地址转换核查、下一步方向）；README 加 T18、T19。汇报网页“下一步”页重写，新增子页 `site/next_1019.html`，导航加“新机制”；“想讨论的问题”第 1 条改成新主线。

- 2026-10-19 · cowork · T18、T19 的论文风格机制图（英文，矢量）：`figs/fig_t18_two_sided_merge.{pdf,svg,png}`、`figs/fig_t19_neighbor_stealing.{pdf,svg,png}`，生成脚本 `figs/paperfig.py`（Python + cairosvg，改文字或布局后重新运行即可）。图里的表项名（IFT、OMT、footprint filter）是暂定名。汇报网页的 `site/next_1019.html` 已换成这两张图。

### 2026-10-20 [claude-code] P12：T18/T19 的测量 1–4 完成，第 5 步停在配置确认（⏸）
- 改了 `STATUS.md`（任务总览加 P12 一行；“需要用户决定”加 3 条：第 5 步 O1/O2b/O3 配置确认、R-GAT 运行间不确定、`azure_conv_tokens.csv` 没进 git；结果摘要加“P12 测量（T18/T19）”；日志）、本文件；新增 `results_index/p12/`（README、runs.csv、M1 汇总、每个 workload 的足迹亲和性 / 稳定性 CSV）。
- 代码：分支 `isca27/p12-measure`（从 `c9cdf64` 开出）：`1929100`（WG 足迹和 M1 共享两个 tracer，默认关）、`ed1aca0`（`-pa-steps` 等 benchmark 参数，默认不变）、`0b85bef`、`d32520a`（分析脚本）。回归：fir、MM、PagedAttention 默认小输入，改动前后 / tracer 关开 total_time 和排序后统计的 md5 完全一致。
- 结果目录：yss2、yss3 的 `~/akita-exp-test/p12/src/results/`（`1019_diag_p12-*`、`1019_diag_p12reg-*`、`1020_det_p12-*`）。
- **两个仓库都没有推送**（这次的要求是由用户审阅后自己推送）：代码 `isca27/p12-measure` 头 `d32520a`；文档是本条日志所在的提交。

### 2026-10-20 [claude-code] T18 实现完成（⏸ 评估配置确认）；P12 推送
- 用户 10-19 决定开始实现 T18、T19（真实版，同时做）。
- 代码：`isca27/p12-measure` 推到 `d32520a`；`isca27/t18-merge` 推到 `ca8b831`（请求方 cuckoo 在途表、按 RTT 分 credit、owner 端合并 + switch 网内多播，默认关闭，回归逐位一致）。`isca27/t19-steal` 在本地开发中。
- 文档：STATUS 加 T18、T19 行，T18 冒烟结果，T18 评估配置确认和 4 个问题，日志。
- ⏸ 等用户：P12 第 5 步 oracle 配置确认；T18 评估配置确认（121 个运行）和 4 个问题。

### 2026-10-20 [claude-code] T19 实现完成（⏸ 方向问题 + 评估配置确认）
- 代码：`isca27/t19-steal` 推到 `eaa9c73`（就近 WG 窃取，mesh 上的真实偷取消息，cuckoo filter 指导，可选均衡初始切分 C′，默认关闭，回归逐位一致）。
- 文档：STATUS 更新 T19 行、T19 冒烟结果、两条待决事项。
- ⏸ 等用户：T19 方向（窃取 / C′ + 窃取 / C′ 当 baseline 修正 / 改细 WG）；T19 评估计划（90–96 个运行）。

### 2026-10-20 [claude-code] 用户确认 T18 评估和 P12 第 5 步，开始运行
- T18：141 个运行（10 个配置 + 只放大窗口的对照 × 远程型 7 + T17 三个；本地型 7 个 × 3 个配置），二进制 `ca8b831`；P12 第 5 步：T17 三个 × O1/O2b/O3 + 3 个 B，二进制 `ed1aca0`。在 yss2、yss3 的 `~/akita-exp-test/` 下，每台最多 15 个。
- 用户：T18 如果效果很好且相对复杂，考虑拆成两个机制。

### 2026-10-20 [claude-code] 并行数改为按内存；加一批 HPCA 规模的 T18 对比
- 用户：服务器没用满，并行数改成按每个 workload 第一个跑完的运行的峰值内存来定（总内存 − 30 GB 以内、少于 120 个、可用内存 < 30 GB 时不启动），取代每台 15 个的上限。
- 用户：T18 在 HPCA 论文规模（hpca 配置、`-max-wg 78600`）下和 HPCA 的 baseline / Complete 比较，只用远程型 7 个，共 28 个运行。


### 2026-10-20 [claude-code] 按用户要求新建代码仓库的 main 分支
- 用户 10-20：把最新代码推到 main。WSC_datapath 原来没有 main（默认分支是 `mechanism_1` @ `0be4bdf`，HPCA 版本，未改动）。新建 `main` = `isca27/t18-merge` @ `ca8b831`（mi200 配置、T16 tracer、T17 workload、P12 tracer 到 `0b85bef`、T18），不含 T19 和 P12 最后一个分析脚本提交。之后只在用户要求时更新 main；其余规则不变（任务分支 `isca27/*`，不 force push）。

- 2026-10-20 · cowork · **范围决定（用户）：ISCA'27 以 CuPath 为底，只做通信（远程数据通路）；thread / data scheduling 留给下一篇。** 新文件 `next_paper_scheduling.md` 收集调度相关的 idea（compute-to-data、多 kernel 空间并发、WG 窃取、中心度放置、incast 去同步）和支持数据；T19 标为 `[下一篇论文]`，README 同步。T18 加了论文贡献结构草案、owner 端"故意攒批"候选、对"按距离分配名额"的更正（只在热点时有用、通用的队头阻塞、现有数据不明显，先测再定）。P12 改版：删掉 WG 足迹 / 亲和性 / 历史稳定性，改为 T17 的 O1/O2b/O3、E1 延迟分段、M1 共享统计、各目的地名额占用时间序列、两个 platbench 微基准（incast + 背景流量；owner 攒批 T = 0/100/300/1000 ns）。`ideas_from_T14.md` 加了 10-20 范围决定一节。网页"下一步"页和 `site/next_1019.html` 按新范围重写（机制 2 改成"留给下一篇"）。待用户决定：m3 去留、baseline 怎么接 HDPAT。

- 2026-10-20 · cowork · **核查"RDMA 在途上限 64"的来源（只读代码和 HPCA 稿）**：(1) 上游 MGPUSim 的 RDMA 引擎**没有**在途上限（`transactionsFromInside/Outside` 是无界切片，只有端口缓冲 1024）；`-rdma-max-outstanding=64`（连同 `-rdma-pipeline-width=8`、`-rdma-pipeline-latency=10`）是我们 fork 在 `a185a7e`（2026-07-11，"Add adaptive memory and remote data path optimizations"）和 CuPath 远程通路机制一起加进来的。(2) HPCA 稿 background.tex 的"RDMA Request Processing"引用的是数据中心 RDMA 网卡的论文（kalia2016design、wei2018deconstructing、mittal2018revisiting），不能直接支撑片上 GPM 端点的 64 项请求表。老板 10-20 指出：RDMA 是 MGPUSim 术语、需要说明真实硬件有这个限制或等效限制、需要回答为什么对 wafer 独特。(3) HPCA 版 m2（design.tex "Remote Aggregation"）**已经**有请求方 cuckoo filter 的 RDMA-In-Flight 类型、同 line 合并、按页的 bitmap 分组包、owner 解码 bitmap；所以 T18 的"请求方在途表"相对 HPCA 版不新，T18 的新部分只有：跟踪表规模化（窗口做大）和 owner 端跨 GPM 合并 + mesh 多播。T18 文档需要相应修改。(4) 校准依据：Pearson（arXiv 2302.14827）MI250X 同封装 GCD 间 kernel load/store 153 GB/s（77%），折合每 CU 约 1.4 GB/s；我们 64 项时每 CU 约 1.6 GB/s。

- 2026-10-20 · cowork · **按用户"改一下"：术语、64 项依据、T18 按"相对 HPCA 版新增了什么"重写；同步 Claude Code 10-20 的 T18 / T19 / P12 结果。**
  - 术语：汇报网页全部页面和 isca27 新文档里 RDMA → "GPM 网络接口"，`-rdma-max-outstanding` → "远程请求跟踪表（RRT）项数"，owner 端表叫 OMT；`<code>` 里的 flag 名不改。
  - `tasks/T18_new_m2_two_sided_merge.md` 重写：状态改为"实现完成（`ca8b831`），正式评估运行中"；HPCA 版已有（请求方在途记录、同 line 合并、同页打包）和 T18 新增（新 1 跟踪表规模化、新 2 owner 端跨 GPM 合并 + 网内多播）分开写；64 项的依据（fork 加的、数据中心 RDMA 引用不能用、PCIe 按字节比、MI250X 校准）；"为什么对 wafer 独特"的规模实验 2×2 / 4×4 / 7×7（未做，要配置确认）；冒烟和 P12 M1 结果摘要；还缺的实验（E1 分段、规模实验、链路 / switch 敏感性、CACTI）。
  - `ideas_from_T14.md` 新增 10-20 一节：论文文字要改的地方（术语、64 项依据、引用替换：去掉 kalia2016design / wei2018deconstructing / mittal2018revisiting 作为端点限制的依据，改引 Pearson 2023 arXiv 2302.14827、Schieffer 2024 arXiv 2410.00801、Pal HPCA'19）。
  - **`prompts/P12_measurements_for_T18_T19.md` 恢复成已执行的版本（= `HEAD`）**：Cowork 10-20 曾改写 P12（"只做测量、不实现"），但 P12 已经由 Claude Code 执行、T18 也已实现，改写版和现状冲突，所以撤回。改写版里还没做的项目（E1 分段、各目的地占用时间序列、incast + 背景流量 / owner 攒批微基准）并入 T18 文档"还缺的实验"，等用户决定是否写成 P13。
  - `tasks/T19_filter_guided_wg_stealing.md`：状态改为"`[下一篇论文]`，代码已实现（`eaa9c73`），本篇不再推进"。**给 Claude Code**：用户 10-20 定了 ISCA'27 只做通信，STATUS 里 T19 的方向问题和评估计划（90–96 个运行）先不用等答复，除非用户另有指示。README 的 T18、T19 两行同步。
  - 机制图 `figs/fig_t18_two_sided_merge.*`：标注 HPCA 已有 / 新增；跟踪表写成 "64 → 100s–1000s"（冒烟里 W=1024 和 4096 相同）；多播树写成 routing tree / DOR（实现是 YX，之前图里写 XY）；新增 "Why the tracker must grow on a wafer" 小图。
  - 汇报网页（Artifact 第 27 版，`site/` 已备份）：`next_1019.html` 第 3 节加"实现和冒烟结果"表，第 5 节改成"测量：已做的和还缺的"并加 M1 结果表，时间表更新；主页"下一步"把 P12 框换成 T18 冒烟结果；baseline 写成"mi200 原样，地址转换不变（用户 10-19）"。
  - **问 Claude Code**：冒烟里"W ≥ 1024 时每 GPM 只有 40–75 个在途"是时间平均还是峰值、是否只算网络在途？PagedAttention 3.34× 时在途却比 64 少，口径需要说明。
  - 本条和上面的 Cowork 改动都还没提交，请 Claude Code 同步时以 `cowork: ...` 提交。

### 2026-10-20 [claude-code] 用户：T18 作为新 M1，旧 m2（Remote Aggregation）作为 M2
- 用户 10-20：两个设计分开。新 M1 = T18（在途合并 + owner 端合并多播 + 按距离分窗口），新 M2 = 旧 m2（同页打包 + 预取）。两者都在请求方去重，**去重归谁等消融结果再定**：如果某个机制效果太好，就把去重归到相对弱的那个。旧 m1（Local Pairing）不再作为机制。
- 实现：分支 `isca27/m1m2`（从 `ca8b831`）让 M1、M2 能同时打开，去重的归属做成开关；关闭时逐位一致。正式消融等用户确认计划。

### 2026-10-20 [claude-code] 用户确认：先跑新 Complete，消融放最后
- 新 Complete = 新 M1（T18 全开 W=1024 + 按距离分 credit）+ M2（旧 m2）+ 旧 m3（Remote Reuse），去掉旧 m1；去重暂时两边都做。保留 HPCA 版 Complete 作参照。
- 计划（用户 10-20 确认）：整合分支 `isca27/m1m2` 跑通 → 和 4 个移植分支合并、回归、推送 → 16 个新 benchmark × {baseline、新 Complete、HPCA Complete}，中小规模（每个约 0.5–1 小时），共 48 个。注意旧 m3 在 kernel 边界不失效（P08 a），多 kernel 的结果可能偏乐观，结果里标明。

### 2026-10-20 [claude-code] 16 个新 benchmark 移植完成；新 Complete 整合完成
- 代码：`isca27/wport-a..d`（16 个 benchmark）、`isca27/m1m2`（新 M1 + M2 + M3 组合，`-cupath-dedup-owner`，`complete_new`，去重归 M1）、合并分支 `isca27/w2-complete` @ `d09b18b`，都已推送。回归都和 `ca8b831` 逐位一致。
- STATUS 新增“W2 移植”结果表、地址转换（TLB 覆盖）问题、emulator bug 清单。
- 进行中：新 Complete 在 16 个新 benchmark 上的 48 个运行；T18 评估。

### 2026-10-20 [claude-code] 用户：T17 暂停；T18 改成消融实验
- T17：队列里剩下的 T17 运行（T18 评估里的 PA/MoE/R-GAT、T17 oracle 其余）撤出，正在跑的跑完，已完成的结果保留；以后需要再跑。新 benchmark 里的 DLRM、向量检索、allreduce 先承担“现代 workload”。
- T18 消融（mi200，不开 l2-source）：累加 C0 baseline → C1 +窗口 1024 → C2 +请求方合并 → C3 +owner 合并多播 → C4 +按距离分 credit（= 完整 T18 / M1）；去掉一个 L1 无大窗口、L2 无请求方合并、L3 无 owner 合并（L4 无距离 = C3）；窗口扫描 W = 64/256/1024/4096。workload：远程型 7 个（`ca8b831`）+ 16 个新 benchmark（`d09b18b`）；已完成且参数相同的运行直接复用；本地型检查和 HPCA 规模对比不变。

- 2026-10-20 · cowork · **用户决定：T18 保留（"有用"）；T19 放弃（本篇不做，代码留在 `isca27/t19-steal`）。** 给 Claude Code：T19 的方向问题和评估计划不用再等答复，不要启动 T19 的评估运行。用户下一步要改论文的 observation 一节：删掉 M1（Local Pairing）的 observation（HPCA 稿 O6），换成支撑 T18 的新 observation；Cowork 在对话里提了新 observation 的列表和要收集的数据，等用户确认后再写进文档 / 提示词。

- 2026-10-20 · cowork · 新增 `observations/README.md`（用户要求）：ISCA'27 observation 一节要收集的数据。删 HPCA 稿 O6（Local Pairing）和 O1 的本地部分，换掉旧 M2 的 O2、O3；新 O1–O6（远程是主要代价、排队在请求方网络接口、受跟踪表不受链路限制、需要的表项随规模增长、跨 GPM 共享和读者相邻、只放大跟踪表瓶颈转到 owner 端），每条写了论点、已有数据、要新加的统计（E1 分段、跟踪表 / owner 占用、链路利用率、共享统计补齐）、CSV 格式；运行清单 R1–R6（R1、R2 只加统计不需确认；oracle 补跑和 3×3 / 5×5 / 7×7 规模实验需要确认，模拟器还没有 mesh 尺寸的 flag）。状态 `[提案]`，Claude Code 先不要开始。README 索引加了一行。

- 2026-10-20 · cowork · **用户决定：m3（Remote Reuse）保留，但不作为重点。** m3 只出现在消融和 Complete（= T18 + m3）里；observation 里 HPCA 稿的 O4、O5 合成新 O7（次要），在 mi200 上重算（L2 每 GPM 2 MB）。`observations/README.md` 加了新 O7 的数据要求（远程复用比例、复用间隔、L2 空余；放进 R1，只加统计），并记下 m3 写论文前要补的：kernel 边界失效、和 B1 对比、L2 命中延迟偏小可能高估 m3。T18 文档加"和 m3 的关系"；`ideas_from_T14.md` 贡献结构加第 (4) 条。规模实验的尺寸统一改成 3×3 / 5×5 / 7×7（可选 2×2）。汇报网页（"下一步"页和 `next_1019.html`）同步。

- 2026-10-20 · cowork · `observations/README.md` 加了第 5 节"论文图表清单"：Motivation 图 F1–F7（对应新 O1–O7）、Design 小表、Evaluation 表 A–D 和图 E1–E8，每个写了内容、数据来源、状态、优先级和收集先后；运行清单 R3 改为必须，加了 R6 扩展（带宽随表项数扫到 4096）、R7（链路 / switch / L2 延迟敏感性，要确认）、R8（m3 单独和 T18 + m3，先修 m3 的 kernel 边界失效）。

- 2026-10-20 · cowork · 新增 `prompts/P13_observation_data.md`（用户要求，用户自己交给 Claude Code）：按 `observations/README.md` 收集第一批数据——新统计（E1 分段、RRT / owner 占用、链路利用率、远程复用和 L2 空余）+ R1–R3（baseline、只放大跟踪表 W=4096、T18 全开 W=1024，10 个 workload，规模同 T18 评估）；并行准备：m3 kernel 边界失效修复 + R8（m3、T18 + m3）、mesh 尺寸 flag（只准备 3×3 / 5×5 的配置确认）、CACTI（RRT 对比同容量 CAM、OMT）、R4 / R6 / R7 的配置确认。README 索引加了一行。

### 2026-10-20 [claude-code] 新 Complete 在 16 个新 benchmark 上完成；HPCA 版 Complete 放弃；其他队列冻结
- 结果：15 个有效，GMEAN 2.82×（去掉 embbag 3.04×）；graphtc 三个配置都崩溃（kernel LDS 越界），npbis 2 次迭代的 baseline 崩溃。更正了之前根据崩溃运行汇报的 TC、npbis 数字。CSV：`results_index/w2complete/`。
- 用户：HPCA 版 Complete 不再测（正在跑的按 PID 停掉）；跑完这组后不跑别的实验，T18 消融 / HPCA 规模 / 本地型检查的队列冻结（`~/akita-exp-test/t18eval/queue.tsv` 保留，可恢复），冻结前已在跑的运行照常跑完。
- 读到 Cowork 10-20 的条目：T18 保留、T19 放弃（本篇不做）；m3 保留但不作重点；新增 observations/ 和 P13（等用户交给 Claude Code）。注意：Cowork 写的“Complete = T18 + m3”和本会话 10-20 用户定的新 Complete（M1 = T18 + M2 = 旧 m2 + M3）不一致，待用户确认。
