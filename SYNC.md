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
