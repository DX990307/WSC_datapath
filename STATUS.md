# 进度汇报（由 Claude Code 维护）

> **写的人**：本地 Claude Code。**读的人**：dx，以及 Cowork 侧的 Claude。
> **规则**：只在“日志”里追加，不删除旧条目；“任务总览”和“需要用户决定”保持为最新状态。
> 项目背景见 `README.md` 和 `00_background.md`；每个任务的规格见 `tasks/`。

## 任务总览

状态取值：`未开始` / `进行中` / `完成` / `阻塞` / `跳过`

| 任务 | 状态 | 最后更新 | 一句话说明 |
|---|---|---|---|
| T0 实验脚本 | 未开始 | | |
| T1 指令支持检查 | 未开始 | | |
| T2 新增统计 | 未开始 | | |
| T3 强 baseline | 未开始 | | |
| T4 新配置 | 未开始 | | |
| T5a LLM decode | 未开始 | | |
| T5b GCN arxiv | 未开始 | | |
| T5c DLRM embedding | 未开始 | | |
| T5d SSB join | 未开始 | | |
| T6 截断验证 | 未开始 | | |
| T7 机制 1 | `[提案]`，不要开始 | | |
| T8 机制 2 | `[提案]`，不要开始 | | |
| T9 第二批 workload | `[提案]`，不要开始 | | |
| T10 SPMD 通信套件 | `[提案]`，不要开始 | | |
| T12 driver flush 修复 | 完成（用户已确认；10-13 15:17 补跑带 magic copy 的 R3，通过） | 10-13 15:17 | 修复 `9c30a89`；单元测试、R1、R2、R3 全部通过 |
| T13 MI300X 配置 → mi200 | **已冻结**（用户 10-16） | 10-16 | 标签 `isca27-mi200-v1` = `67f1c03`（只在本地，推不推送等用户）；每 slice MSHR 保持 32；`config_deviation` 改为相对 `configs/mi200.json`（`61b6ae8`）；冻结说明 `akkalat/configs/MI200_V1.md` |
| T14 平台数据（第一档） | 完成（⏸ 等用户确认） | 10-16 | 7 项 × 3 套配置（hpca、mi300x、mi200）跑完；摘要表见“结果摘要”，CSV 在 `results_index/platform/`、`results_index/platform_mi200/` |
| T15 oracle 上限实验 | ⏸ 配置确认，等用户 | 10-16 | 用冻结版 `isca27-mi200-v1`；6 组 × 14 个 workload = 84 个运行，见“需要用户决定” |
| T16 位置导致的负载不均（阶段 1） | 完成（⏸ 等用户看结果） | 10-17 | 13 个运行全部成功；结果见“结果摘要 → T16 阶段 1”；按判断标准 W1 **差一点没成立**（真实 workload 只有 2 个满足） |
| T17 新 workload | 第一轮 baseline 完成（⏸ 等用户看结果） | 10-17 | 4 个运行全部成功（rc=0，墙钟 3.2–3.75 小时）；结果见“结果摘要 → T17 第一轮”，CSV 和热力图在 `results_index/t17/`；R-GAT 85% 的时间只用了 ≤13 个 GPM（见“需要用户决定”） |
| P12 T18/T19 的测量 | 1–4 完成；5（T17 的 O1/O2b/O3）⏸ 配置确认，等用户 | 10-20 | 两个只加统计的 tracer（WG 足迹、M1 共享），回归逐位一致；7 个测量运行（yss2 3 个、yss3 4 个）全部 rc=0；结果见“结果摘要 → P12 测量”，CSV 在 `results_index/p12/` |
| T18 新 m2（两端合并） | 实现完成（用户 10-19 决定直接做真实版）；⏸ 评估的配置确认，等用户 | 10-20 | 分支 `isca27/t18-merge` @ `ca8b831`（已推送）；全部开关关闭时 fir、MM、旧 m2 和 `0b85bef` 逐位一致；冒烟见“结果摘要 → T18”；评估计划 121 个运行，见“需要用户决定” |
| T19 就近 WG 窃取 | **放弃**（用户 10-20：本篇只做通信，留给下一篇） | 10-20 | 代码保留在 `isca27/t19-steal` @ `eaa9c73`；不跑评估 |
| P19 快速试错（D / Q0 / Q0b / Q1 / Q2 / Q5 / Q6） | 进行中（用户 10-22 已确认配置） | 10-22 | 分支 `isca27/quick-trials`（worktree `wt-qt`，= `isca27/a1-retain` @ `1335def` + 合并 `isca27/page-class` + `isca27/m1-wafer`）；W = 128、384 GB/s；配置确认见"需要用户决定"第一条 |
| P20 放置 + M3+（两遍法放置 oracle） | 进行中：第 1 步完成，第 2 步运行中（主会话 10-22 代用户确认配置） | 10-22 | 分支 `isca27/placement`（worktree `wt-place`，= `isca27/a1-retain` @ `1335def` + 合并 `isca27/page-class` + `20f323e` 放置表开关）；W = 128、组合表 128、384 GB/s；配置确认见"需要用户决定"第一条 |
| T11 探针实验 | 完成 | 10-13 04:55 | P1–P4 全部有结论（P1/P2/P4 通过，P3 路径通过、细节见结果摘要）；SPMD 骨架在 `mgpusim/benchmarks/microbench/probe/spmd.go` |

## 需要用户决定

- [x] **(P20 第 0 步配置确认，10-22) 主会话已代用户确认**（用户外出前授权）：全部同意；MoE 照跑原规模（超过 1 小时的约定在报告里注明）；若回归证明 `20f323e` 和 `9e87ac5` 在 v4m3 上逐位一致，B 批的 v4m3 参照复用 P19 的 `qt-w128c128b384` 结果。第 1 步之后的调整（主会话同意）：P-按类 用 v2（热点页集中时拆到环 12，否则不动；其余页 1 KB 子块给主要使用者），见 `results_index/place/bound.md`。原内容：范围：P20 全部运行（回归、第 2 步两遍法 oracle 的 pass 1 / pass 2）；第 1 步是离线补算，不跑模拟。
  1. **二进制**：分支 `isca27/placement`（worktree `~/Desktop/CuPath/wt-place`，已推送）= `isca27/a1-retain` @ `1335def`（含 `c08698b`，已核对是祖先）+ merge `isca27/page-class` @ `149dff0`（`2735684`，无冲突）+ `20f323e`（本轮新代码）。**没有合并 `isca27/page-migration`**：两遍法 oracle 没有迁移开销，用不到它的计数器和拷贝引擎（第 3 步做在线版时再合）。没有合并 m1-wafer / quick-trials 的统计。交叉编译（linux/amd64）通过；`go test`：`akita/mem/trace`、`TestPlacementFinders` 通过。
  2. **新开关（默认全关）**：
     - `-place-table <文件>`：每行 `<1 KB 块号 = 物理地址 >> 10> <tile>`，列出的块整个运行都由该 tile 的 GPM 当 home（没有迁移开销，相当于分配时就放好）。实现：L1V / L1S 的下级查找（块在本 GPM → 本地 L2，任何地址；在别的 GPM → 本 GPM 的 RDMA）、共享的"地址 → owner RDMA"表（请求方：打包、合并、M3 保留、统计都看到新 owner）、RDMA owner 端的本地查找（放到这里的块 → 本地 L2）。每个块只有一个 home，**一致性和 baseline 相同**（不像 `-pgcls-oracle-pages` / `-force-local-data-access` 那样会有旧副本）。L1I 不改（指令块不会被写）。结束时打印 / 写 `<metric>_place_stats.csv`（L1 走本地 / 走远程次数、owner 端次数、misrouted，应为 0）。
     - `-place-random-seed N`（只用于自测）：每个 1 KB 块按哈希随机放到一个 GPM，用来跑 `-verify`。
     - `-pgcls-place-ext`（只读统计，配合 `-report-pgcls`）：另写 `_place.csv.gz`（每页、每个 GPM、每个 1 KB 子块的 L1V 写次数；读次数已在 `_readers.csv.gz` 按 512 B 记）。pass 1 用。
     - 其他文件：`rdma.Comp.LocalModuleFinder()`（只加 getter）。
  3. **配置文件**（`akkalat/configs/`）：`v4m3.json`（从 `isca27/quick-trials` 原样复制：v4_complete_a1u + `a1-first-touch-admission: 1`）；**`m3p.json`（M3+）** = extends v4m3 + `remote-data-path-batching-enable: false`、`remote-filter-prefetch-enable: false`、`t18-req-merge: false`。其余配置文件和 `c08698b` 相同（`flag.go`、`r9nanobuilder.go` 等只多了默认关闭的 A1 / pgcls 开关，P19 已核对）。
  4. **场景参数（所有运行）**：`-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=128`；T18 配置（M3+、v4m3 及叠在上面的 P）另加 `-t18-window=128 -t18-compose-table-entries=128`（**用户 10-22 下午定的 128**，不是 2W）。**baseline 只加 `-rdma-max-outstanding=128`，不加 `-t18-window`（原版 RDMA，沿用用户在 P19 的决定）**；P-整页 / P-子块 / P-按类（不带 M3+）也是 baseline + 放置表，同样走原版 RDMA。所有运行保留 `-magic-memory-copy`（mi200.json 里已有）。
  5. **workload（14 个，参数和 pgcls / P19 完全相同）**：私有放错 KM（`kmeans-t16small -t16-wg-per-cu 1`）、fft2d（`w2-fft2d -fft2d-n 1024`）、FW（`floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4`）、MoE（`moe -moe-tokens=64 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75`）；流式 MT（`matrixtranspose-t16small`）、PA（`pagedattn -pa-batch=14 -pa-seed=7 -pa-part-tokens=32`）；热点广播 PR（`pagerank-t16small -t16-wg-per-cu 24`）、tc（`graphtc -tc-scale 12 -tc-edge-factor 16`）、MM（`matrixmultiplication-t16small`）、sssp（`sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1`）、IVF（`ivfflat -ivf-n 8192 -ivf-queries 328`）；混合 / 对照 CFD（`altis_cfd -cfd-cells 172032 -cfd-block 128`）、embbag（`embbag -eb-batch 512`）、BH（`lonestar_bh -bh-bodies 86016`）。
     - **⚠️ MoE 超过"每个运行 ≤ 1 小时"**：P19 的 W = 128 baseline 跑了 2 小时还没完（W = 256 约 2.5–3 h）。建议照跑原规模（和 pgcls 一致），只是最后出结果；或者你说换小规模 / 去掉。
  6. **放置表怎么算（pass 1 → pass 2）**：pass 1 = 本轮的 baseline 运行（`-report-pgcls -pgcls-place-ext`），权重 = 每个 (1 KB 块, GPM) 的 L1V 读（含 owner 本地读）+ 写。
     - P-整页：每页 → 这页访问最多的 GPM（主要使用者；并列时不动）；
     - P-子块：每个 1 KB 块 → 它的主要使用者；
     - P-按类：页类别按 pgcls 的规则（`pgcls_page.py`，在 pass 1 数据上重新分类）：私有放错、流式 → 1 KB 子块给主要使用者；热点广播 → 拆成 k 份放到中心周围（k 和 tile 由第 1 步 (a) 定，候选 k = 4：17 / 23 / 25 / 31 各 1 KB；k = 2：每 2 KB 一份）；其余类不动；
     - P-按类-中心（只用于"砍热点拆分"的判断）：同 P-按类，但热点页整页放到按读者加权的最优 tile（不拆）。
     - 放置表只按物理页号算，同一 binary、同一参数下物理地址确定（pgcls oracle 已经这样用过）；每个 pass 2 运行检查 `misrouted = 0`、WG 数和指令数和 baseline 差 ≤ 1%。
  7. **运行和 CPU 小时**（按 P19 同规模 W = 128 的实测墙钟 0.3–0.8 h / 个，MoE 约 3 h；pgcls 统计约 +20%）：
     | 批 | 内容 | 运行数 | 估计 CPU 小时 |
     |---|---|---|---|
     | A 回归 + 正确性 | (i) fir、MM（`-max-wg=4096`）× {mi200, v4m3} × {`20f323e`, `1335def`}：metrics.csv 排序后 md5 一致；(ii) npbcg S × {mi200, v4m3} × {`-report-pgcls`, `+ -pgcls-place-ext`, 空放置表}：一致；(iii) `-verify -place-random-seed=1`：fir、MM、FW（p12small 默认）、bfs s12 × {mi200, m3p, v4m3} | 8 + 6 + 12 = 26 | ≈ 3 |
     | B pass 1 + 参照 | 14 × {baseline（+ `-report-pgcls -pgcls-place-ext`）, M3+, v4m3, 全部本地（mi200 + `-force-local-data-access`）} | 56 | ≈ 40 |
     | C pass 2 | 14 × {P-整页, P-子块, P-按类, P-按类 + M3+, P-按类 + v4m3}（都加 `-report-pgcls` 量剩下的远程读）+ P-按类-中心 × 5（PR、tc、MM、sssp、IVF） | 75 | ≈ 55 |
     | 合计 | | 157 | ≈ 100 |
     - exp_id：`1022_abl_place-w128b384-<wl>[-<变体>]_<config>_20f323e`（变体：`pg`、`sb`、`cls`、`ctr`）；结果 `~/akita-exp-test/place/src/results/`。
  8. **服务器和并发**：yss2 / yss3 新目录 `~/akita-exp-test/place/`，自己的调度器（复制 P19 的 sched.py：30 GB MemAvailable 余量、PAUSE、挂住看门狗；nice 10）。**并发上限 yss2 6、yss3 10**（两台各 128 核，P19 上限 9 / 20，现在负载 10–17，MemAvailable 215 / 189 GB；每个运行 4–8 GB）。不碰 qt / pm / geo / mesh9* 的进程、调度器和 PAUSE 文件。墙钟估计：A 约 0.5 h，B 约 2.5 h（MoE 3 h），C 在每个 workload 的 pass 1 完成后自动排，约 3.5 h；共约 7–8 h。
  9. **第 1 步（离线）用的数据**：不重跑。(a)(c)(d)(e) 用 P19 的 W = 128 baseline（`9e87ac5`，`-report-pgcls` 的精确聚合：每页每个读者按 512 B 的读数、重读数）；(b) 要时间信息，用 pgcls 的 W = 256 baseline 原始 trace（`149dff0`，按页哈希采 1/16 的页，每页的全部访问），注明。MoE 的 W = 128 baseline 还没跑完，没完之前用 W = 256。
- [x] **(P19 第 0 步配置确认，10-22) 用户已确认**：W = 128（`-rdma-max-outstanding=128`，CuPath 加 `-t18-window=128`），组合表 256；baseline 不加 `-t18-window`（原版 RDMA）；Q6 的 MoE 照跑原规模；文档仓库的提交等用户处理权限，先留在工作区。原内容：范围：P19 全部运行（第 1 步主结果 + 诊断、Q0、Q0b、回归；第 4 步原型冒烟的配置先列出，代码写完后再报 commit）。
  1. **二进制**：分支 `isca27/quick-trials`（worktree `~/Desktop/CuPath/wt-qt`）= `isca27/a1-retain` @ `1335def`（含 `c08698b`，已核对）+ merge `isca27/page-class` @ `149dff0`（`-report-pgcls`）+ merge `isca27/m1-wafer` @ `04115a1`（Q0b 的拐点合并 / 条件延迟、`-report-m1w-bound`），两次合并无冲突、交叉编译通过（本地 merge 提交 `b906d5c`）。接下来只加**只读统计**（默认关）：`-report-qt-diag`（每 CU L1V MSHR 满的时间比例；每个 owner 的远程读数、owner 排队 + 服务时间、最忙 owner 份额）和 `-pgcls-trace-ext`（pgcls 原始 trace 增加"上网的 line"和"回复 L1 的延迟"两类行，给 Q1 / Q2 离线用）。主运行前先在服务器做回归：fir、MM（`-max-wg=4096`）× {mi200, v4m3} 新 commit 对 `1335def` 逐位一致；npbcg S × {mi200, v4m3} 统计开 / 关逐位一致。
  2. **配置文件 blob 核对**：`runall2_constants.py`、`runall2_config.py`、`shaderarray.go`、`gpm_preset.go`、`config_file.go`、`hpca.json`、`mi300x_eighth.json`、`mi200.json`（`40fd5fec422f` = 标签 `isca27-mi200-v1`）与 `c08698b` 相同；`flag.go`、`r9nanobuilder.go`、`timingplatform.go`、`runner.go` 相对 `c08698b` 只增加了默认关闭的 A1 / pgcls / m1w 开关和接线，没有改任何配置数值。
  3. **配置文件**（新增，`akkalat/configs/`）：
     - `v4m3.json` = extends `v4_complete_a1u.json` + `a1-first-touch-admission: 1`（和 a1/ 里 "v4+A1+reset+ft1" 的参数完全相同；原来没有单独的文件）。
     - `v4m3_ownmc.json`（Q0）= extends v4m3 + `t18-owner-merge: true`、`t18-owner-multicast: true`。
     - `v4m3_m1w.json`（Q0b）= extends v4m3 + `m1w-hub-mode: turn`、`m1w-hub-min-hops: 2`、`m1w-hub-retain-ns: 0`、`m1w-hub-entries: 1024`、`m1w-hub-adaptive: true`、`m1w-hold-ns: 100`、`m1w-hold-min-hops: 2`、`m1w-hold-min-occ: 0.5`（= m1w 的 hubturna + hold100o）。
     - 第 4 步（只做第 2 步通过的）：`v4m3_q2.json`（读者位图推送，oracle 页类别；之后在线版）、`v4m3_q1.json`（按跳数调预取深度）、`v4m3_q6.json`（按类过滤的访问计数迁移，pm 的 c4k 开关 + 类过滤）。开关名和 commit 等代码写完后报告。
  4. **场景参数**（命令行，所有运行）：`-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=128`；CuPath 配置另加 `-t18-window=128 -t18-compose-table-entries=256`。**⚠️ 请核对：组合表按 2W 取 256**（W = 64 → 128、W = 256 → 512 的同一规则；v4_complete.json 里写的是 128，用命令行覆盖成 256）。
  5. **⚠️ 请决定：baseline 加不加 `-t18-window=128`**。`-t18-window` 一设就会让 baseline 改走 T18 的请求方引擎（不合并，只是另一套窗口实现），和原版 RDMA 不是同一个 baseline：W = 256 时 pgcls 的 baseline（不加）和 m1w 的 baseline（加了）差 0.2–3.7%（sssp 416.0 vs 431.6 µs，MM 251.8 vs 256.0，npbcg 2745.6 vs 2768.5，PR 866.7 vs 868.1）。**建议 baseline 只加 `-rdma-max-outstanding=128`（原版 RDMA，和 SUMMARY_1022_morning / pgcls / a1 的 1.315 表一致，偏保守）**；两种窗口都是 128，只是实现不同。
  6. **workload 和规模**：25 个，参数和 pgcls 完全相同：pr-g24、npbcg-W、tc-s12e16、mm-g12、atax-2688x8192-r64、fw-n2048-p4、sssp-s18e4-r1、cfd-c172k-b128、mg-W、ivf-n8k-q328、bh-n86k、npbis-n1m-i1、moe-t64、i2c-g64、fft2d-n1024、pa-b14s7p32、shocsort-n688k、ar-pc1、gups-L18m8、lud-n1024、spmv-g32、mt-g4、embbag-b512、km-g1、bfs-s18e2。所有运行保留 `-magic-memory-copy`（mi200.json 里已有）。
  7. **统计开关**（只读）：所有主运行 `-report-obs-latency -report-obs-occupancy -report-qt-diag`；T18 配置加 `-report-cupath-v3-stats`；baseline 加 `-report-pgcls`（W = 128 下重新分类）；v4m3 加 `-report-pgcls -pgcls-trace-ext`，7 个热点广播 workload（PR、tc、MM、sssp、IVF、npbcg、ar）`-pgcls-sample 1`，其余 `-pgcls-sample 4`；m1w 的 8 个 workload 的 baseline / v4m3 加 `-report-m1w-bound`（和 W = 256 的 `bound_c26d97e.csv` 直接对比）。
  8. **运行数和 CPU 小时**（按 pgcls 同规模的实测墙钟 0.3–2.5 h / 个，W = 128 和统计再加约 30%）：
     | 批 | 内容 | 运行数 | 估计 CPU 小时 |
     |---|---|---|---|
     | A 回归 | fir、MM × {mi200, v4m3} × {新 commit, 1335def}；npbcg S × {mi200, v4m3} × 统计开 / 关 | 12 | ≈ 1 |
     | B 主结果 + D | 25 × {baseline, v4_complete, v4m3}，tag `w128b384`，exp_id `1022_diag_qt-w128b384-<wl>_<cfg>_<commit>` | 75 | ≈ 70 |
     | C Q0 / Q0b | v4m3_ownmc × 10（PR、tc、MM、sssp、IVF、npbcg、ar；KM、CFD、BH）+ v4m3_m1w × 8（PR、npbcg、tc、MM、sssp、CFD、ar、bfs）；v4m3 对照来自 B | 18 | ≈ 13 |
     | D 冒烟（只做通过的） | 每个原型 ≤ 16 个（热点广播 7 + 私有放错 3 + 延迟受限 3 + 对照 3）；Q6 6 个；外加每个原型的回归 / -verify | ≤ 60 | ≤ 45 |
     | 合计 | | ≤ 165 | ≈ 85（A–C）+ ≤ 45（D） |
     墙钟：yss3 MemAvailable 约 125 GB、yss2 约 58 GB，按 30 GB 余量约同时 14 + 4 个，A–C 约 6–7 小时（moe 一个约 3 小时）。目录 `~/akita-exp-test/qt/`，自己的调度器（复制 pgcls 的 sched.py，30 GB 余量、PAUSE、挂住看门狗），nice 运行，不动 mesh9s / mesh9 / mesh9b / hoplat 等。
  9. **其他需要知道的**：moe-t64 一个运行约 2.5 h，超过冒烟"≤ 1 小时"，Q6 冒烟里的 MoE 照跑（或者你说换小规模）；第 2 步离线分析只用 B 的 trace，不另跑；Q5 只写设计。
- [x] (P18 网络结论的交叉验证) **用户 10-21 决定：坚持用 ASTRA-sim（很多 wafer 论文用它）；之后又要求试 Llama-7B，以及把 CuPath 机制移植到 ASTRA（选项 A：网络层评估）。全部完成，结果见"结果摘要 → P18"。** 原问题：第 1 步调研完成，⏸ 等你确认方案再安装和运行。 只读了源码和文档，没有编译、没有运行，服务器没动。
  - **ASTRA-sim 不适合直接用**（master `4ce9ecf`，2026-09-19；最新 tag `2.2.0`，2023-11；源码在本机 scratch，已核对关键两处）：
    1. 拓扑：analytical 后端只有 Ring（带回绕）/ FullyConnected / Switch 三种按维叠加，**没有 2D mesh**（只能 2D torus）；congestion-aware 在 master 上**只支持 1 维**（`congestion_aware/topology/Helper.cpp:27` 直接报错退出），多维的分支 `will/congestion-multidim` 没合并，且固定先 X 后 Y；congestion-unaware 的多维点对点只计第一个不同的维度。ns-3 后端能建任意拓扑，但路由是最短路 + ECMP（不是维序路由），每条消息是一个 RDMA 流（ACK、拥塞控制）。Garnet 后端在 master 上已经没有了（submodule 目录不存在）。
    2. 在途数：`astra-sim/workload/HardwareResource.cc:54` `assert(num_in_flight_gpu_comm_ops == 0)`，**每个 NPU 同一时刻只能有 1 个在途 send**，W = 64 / 256 / 448 的闭环做不了，除非改它的核心代码。
    3. 粒度：congestion-aware 把整条消息当一个 chunk、逐跳存储转发、链路队列无限长，没有 flit、缓冲、反压；时间是整数 ns（64 B 在 384 GB/s 下串行化 0.17 ns，被截掉）。
    4. 统计：analytical 后端没有链路利用率和排队延迟。
    - 结论：ASTRA-sim 是为大块集合通信设计的。要验证我们的结论，得自己加 mesh 拓扑、先 Y 后 X 路由、在途数限制和统计，改完之后验证的其实是我们自己写的代码，说服力弱。**不推荐作为主要工具。**
  - **akita 侧的网络语义**（读代码确认，`wt-v3` @ `4235991`），决定了别的模拟器该怎么配：
    - `-bandwidth=24` 是**每 ns 24 个 16 B flit**（384 B/ns）：链路时钟 = 1 GHz × 24（`akita/noc/networking/mesh/mesh.go:408`），不是 24 B/周期；`=3` 是 3 flit/ns。
    - 每跳 32 周期 = 交换机每个输入口一条 **32 级、每级 24 flit 宽的全流水线**（`switching/switch.go:583-585`），流水线里最多 768 flit，不占缓冲；`-switch-buffer-flits=16` 只管流水线后面的 route / forward 缓冲，入口缓冲是 max(16, 24) = 24。反压是"下一级能不能 push"的即时判断，**没有 credit 往返**。
    - 所以 BookSim / Garnet 如果按常规配成"32 周期流水 + 每 VC 16 flit、credit 流控"，单 VC 会被 credit 往返卡在约 16/35 flit/周期，比 akita 早饱和，比的就不是同一个模型。要分两种配法：**(i) 等价配置**：模仿 akita（延迟放进不占输入缓冲的 crossbar 级、缓冲足够大），检验 akita 的实现本身；**(ii) 现实配置**：credit + 每 VC 16 flit，检验"akita 的缓冲语义偏乐观"会不会改变 48 GB/s 下的结论。
    - 现有平台数据 `results_index/platform_mi200/` 是 768 GB/s（`-bandwidth=48`）、旧二进制、默认缓冲测的。跳数矩阵"198.1 ns + 71.8 ns/跳"（往返）可以做零负载对照，饱和带宽要按当前配置重测。
  - **方案（推荐 A + B）**
    - **A 解析模型**（自己写 Python，本机，约半天）：dm 的流量矩阵按先 Y 后 X 路由算出每条有向链路的 flit 负载 → 先和 akita 的 `metrics_obs_links` 逐条对照 flit 数（检验流量重建和路由）→ 链路利用率、瓶颈链路、饱和点（链路带宽 ÷ 最大链路负载）→ 逐跳 M/D/1 得开环延迟—负载曲线 → 闭环 MVA（每 GPM W 个客户，链路是排队站，固定的每跳延迟是延迟站）得 W = 64 / 256 / 448 的吞吐和往返。能验证第 1、2、4 条；第 3 条只能近似（M/D/1 在高利用率下不准，不建模反压和队头阻塞）。
    - **B BookSim2**（cycle 级、flit 级，作为独立模拟器）：`booksim/booksim2` HEAD `28f4329`（2017，不活跃但稳定），`make`，依赖 flex / bison。
      - 7×7 mesh：`topology=mesh; k=7; n=2; routing_function=dim_order`。BookSim 先走第 0 维，把节点编号成 `y + 7x` 就是先 Y 后 X，不用改代码。
      - 带宽：BookSim 每条通道每周期 1 flit，用时钟缩放：384 GB/s 时 1 周期 = 1/24 ns、每跳 768 周期；48 GB/s 时 1 周期 = 1/3 ns、每跳 96 周期。
      - 闭环：`sim_type=batch; use_read_write=1; max_outstanding_requests=W; read_request_size=1; read_reply_size=6`。
      - 需要的补丁（约 150 行，放进 `results_index/astra/patches/`）：① 只读（batch 模式写死 50% 读 50% 写）；② 按 owner→requester 矩阵抽目的地的流量模式；③ 中心 tile 24 不注入、不做目的地；④ 输出每条通道的 flit 数（`FlitChannel` 已有计数，只是没输出）；⑤ 可选：owner 端的服务时间（不加的话只测网络本身）。
      - 开环：`sim_type=latency` 扫描注入率（hotspot、均匀、矩阵）。统计：包延迟 / 网络延迟直方图、吞吐、每对延迟、每链路 flit 数。
    - **C（可选）gem5 Garnet standalone**：v26.0.0.0 很活跃，但合成流量只有开环，路由写死先 X 后 Y（要先 Y 后 X 得用表路由、改权重），scons 编译几十分钟，没有请求 / 响应。只在 B 的开环结果有疑问时作为第三个对照。
    - **D（不推荐）坚持用 ASTRA-sim**：要么用 ns-3 后端（ECMP、RDMA 语义，不是 NoC），要么改 `HardwareResource` 并自己写 mesh 拓扑。最多能做一个"W = 1、2D torus 近似"的零负载对照，说服力有限。
  - **流量怎么生成**：`dm/raw` 的 `pairs` 是每个请求方 GPM 按目的 owner 的平均在途数（1 µs 采样）。按 Little 定律，请求率 ∝ 在途数 ÷ 这一对的往返时间，用 `latency` 表按跳数分组的往返时间修正（不修正的话，远的 pair 会被高估）；再按每个 owner 的精确读次数缩放。写和多播不建（占比小，写进说明）。合成模式：均匀 all-to-all（47 个 GPM 互相）、47 个 GPM 读 tile 0。
  - **条件和比较的量**：384 / 48 GB/s × W = 64 / 256 / 448 闭环（8 种流量 × 2 × 3 = 48 组，每组两种缓冲语义），加开环注入率扫描（每种流量约 10 个点）。比较链路平均 / 峰值利用率（同样取 1 µs 窗口）、往返时间分布（对照 akita 按跳数分组的 latency 表）、每 GPM 吞吐和 W × 64 B ÷ RTT、0→7 链路的份额和利用率、饱和点（开环 accepted 对 offered 的拐点）。和第 1–4 条逐项对照。
  - **成本**：A 约半天；B 安装、补丁、校准约 1 天；每个闭环运行在 7×7 上模拟 100 µs（384 GB/s 下 240 万周期），分钟级。
  - **需要你决定**：
    1. 方案：**A + B（推荐）** / 再加 C / 仍要 ASTRA-sim（D）。
    2. BookSim 装在哪：**本机（推荐，完全不碰服务器）** / 服务器的独立目录 `~/noc-xcheck/`。
    3. MGPUSim 侧校准要不要按当前配置重测：**推荐**在服务器用当前二进制 `4235991`、`-bandwidth=24` 和 `=3`、`-switch-buffer-flits=16` 重跑 T14 的跳数矩阵（1–12 跳）、单链路饱和和 incast（baseline、只开统计，约 30 个短运行，放在独立目录）。不跑的话只能用 768 GB/s 的旧数据做零负载对照（零负载延迟和带宽关系很小：6 flit 在 3 flit/ns 下每跳多约 2 ns），没有饱和带宽的对照。
  - 结果目录仍按 prompt 用 `results_index/astra/`，README 里写明实际用的工具。

- [x] (T19) **放弃（用户 10-20：ISCA'27 只做通信，T19 留给下一篇，代码保留在 `isca27/t19-steal`）。** 原问题：**方向问题（先定这个，再定评估计划）**：T17 的主 kernel 每个 CU 不超过 40 个 WF，静态切分下 t≈0 时所有 WG 就已上 CU，**没有还没开始的 WG 可偷**；冒烟里 R-GAT 1.43×、MoE 1.075× 只来自 kernel 启动瞬间的“抢跑”（中心附近的 GPM 先收到 launch）。均衡的初始切分（C′，`-t19-initial-split=balanced`）不偷就有 R-GAT 2.45×、MoE 2.70×。—— (a) T19 只做窃取；(b) T19 = C′ + 窃取；(c) C′ 当作 baseline 的修正（影响之前所有结果）；(d) T17 改成更细的 WG（方案 B）。另外：filter 指导和只按距离偷一样（mi200 请求方 L2 不缓存远程数据，“我已有的数据”只覆盖 L1V/TLB），要不要单独评估，还是等 T18 / B1 之后再说。
- [x] (T19) **放弃（同上），不跑评估。** 原问题：**评估的配置确认**（没有运行；二进制 `eaa9c73`；配置 `mi200` / `t19_dist` / `t19_filter`，只加 `-t19-*` 开关 + 统计开关 `-report-gpm-trace`）。**截断规则建议**：按 T15 的 WG 数截断，14 个 workload 里 12 个在第一个 kernel 内就停，T19 完全不起作用；主结论只用完整 kernel（小输入），截断运行只当开销检查（同一 WG 数停止、指令差 ≤ 1%、报告成功窃取数）。计划：A 完整小输入 10 个 workload × 3 配置 = 30；B T15 截断 16（MT、kmeans 3 配置，其余 base + filter）；C 本地型 24；D 敏感性 20（半径 1/3、chunk 4/16、filter 大 / 小）；E 可选 C′ 6。共 90（96）个，约 218（233）CPU 小时；关键路径 kmeans 截断 14.4 小时。其他问题：R-GAT 数据（2.2 GB）要不要拷到 yss3；本地型要不要做完整的小输入；要不要加 NACK 退避 / 本地性保护（spmv 慢了 0.09%）。
- [x] (T18) **用户 10-20：在 HPCA 论文规模下和 HPCA 的 baseline 比较。** 二进制 `ca8b831`，配置 `hpca.json`（不改任何参数），`-max-wg 78600`（和 `1013_abl_full_hpca` 相同）；workload 只用远程型 7 个（FW、I2C、PR、MT、SPMV、KM、MM，用户 10-20 选定）；配置 4 个：HPCA baseline、HPCA Complete（1013 那次的同一组参数）、T18 全开 W=1024 + 按距离分 credit、只放大窗口 W=1024；共 28 个，kmeans 24 小时超时。排在当前 T18 评估之后，同样按内存规则分配。
- [x] (T18) **用户 10-20 确认：按计划跑，每个 workload 都加“只放大窗口”的对照（共 141 个）；owner 窗口跟着 W；暂不加保留数据的变体、不做表大小扫描；`-verify` 的问题写进文档。用户另外说：如果 T18 性能很好且相对复杂，考虑拆成两个机制（合并 + 多播消 incast；按距离分配的大窗口）。** 原问题：**评估的配置确认**（没有运行）。二进制 `ca8b831`，`isca27_run.sh -config <文件>`，都加 `-report-l2-source -l2-source-tile-width=7`；公共参数 = `mi200.json` 原样（地址转换不变，用户 10-19 决定）。
  - 配置（都 `extends: mi200.json`）：1 baseline；2 旧 m2（`m2_old`，runall2 正式 m2 参数）；3 T18 全开 W=64（`-t18-req-merge -t18-owner-merge -t18-owner-multicast`）；4 只请求方；5 只 owner 端；6–8 全开 W=256/1024/4096（`-t18-window=W`，owner 窗口 = W，表 2W）；9 全开 + 按距离分 credit W=64；10 同上 W=256；可选 11 只放大窗口 W=4096（不合并）。按距离分 credit 只放在 W=64、256，因为冒烟里 W ≥ 1024 每个 GPM 只有 40–75 个在途，用不满（W=1024 时和共享池逐位相同）。
  - workload：远程型 7 个，`-max-wg` 和 T15 相同（FW、I2C、PR、MT、MM 32768；SPMV 1536），**kmeans 建议改成 8192**（T15 的 32768 baseline 要 14.4 小时，O2/O2b 78 小时被终止；baseline 也按 8192 重跑，单个运行 24 小时超时）；T17 三个按已确认规模（PA batch 66、MoE skew 1.75、R-GAT 320 种子；备选 PA batch 33、R-GAT 160 种子）；本地型 7 个只跑 baseline、T18 全开、T18 全开 W=4096，确认不变慢。
  - 规模：10 × 10 = 100 个 + 本地 21 个 = **121 个**（每个 workload 都加对照 11 则 141 个），约 230 CPU 小时；每台 12 个约 12 小时，关键路径是 MT 和 kmeans；每个 3–12 GB。
  - 另外 4 个问题：(a) owner 端窗口跟着 W 放大，还是保持 64、只放大请求方；(b) R-GAT 跨 GPM 在途重复 24%，但 owner 端只合并到 0.4–1.6%（合并窗口只有一次 L2 读的时间），要不要加一个“数据返回后保留 T ns”的变体（不缓存，但会暂存数据）；(c) W=64 时请求方表经常满，要不要加表大小的敏感性扫描；(d) magic copy 下 FW 的 `-verify` 要加 `-magic-copy-coherent`，多轮在 baseline 下也不通过（可能是 kernel 之间不失效 L1），修还是写进文档。

<!-- 每条格式：- [ ] (T#) 问题 —— 可选方案 A / B，推荐 X，理由 -->
- [x] (P12 第 5 步 / T17) **用户 10-20 同意和 T18 评估一起跑，并用 `ed1aca0` 补跑 3 个 B（共 12 个）。** 原问题： **O1 / O2b / O3 配置确认（oracle 是有意偏离 `mi200.json` 的分析实验，等你确认后再跑，没有运行）**。参数和 T15 完全相同（从 yss2 `~/exp/cupath/results/1016_diag_oracle-{o1,o2b,o3}_mi200_61b6ae8/EXPERIMENT_METADATA.json` 只读核对：T15 = runall2 baseline 命令 + 末尾追加的 oracle 参数）。T17 用 `isca27_run.sh` + `configs/mi200.json`（10-17 已更正：这和 runall2 baseline 等价），oracle 参数写在命令行末尾（命令行覆盖配置文件）。
  - **命令**（`<MMDD>` = 启动日期；在 yss2 或 yss3 的 `~/akita-exp-test/p12/src` 下，二进制 `ed1aca0`，模拟器时序代码和 T17 的 `95615c4`、`c9cdf64` 相同，回归见 P12）：
    `ISCA27_BINDIR=~/akita-exp-test/p12/bin akkalat/isca27_run.sh <MMDD>_diag_t17-<w>_mi200+<o>_ed1aca0 ed1aca0 T17 mi200 <benchmark 参数> -report-gpm-trace -report-l2-source -l2-source-tile-width=7 <oracle 参数>`
    - `<w>` = `pagedattn`：`-benchmark pagedattn -pa-batch=66`
    - `<w>` = `moe-skew1.75`：`-benchmark moe -moe-tokens=256 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75`
    - `<w>` = `rgat-s320`（现有版本，不改成铺满 48 个 GPM）：`-benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=320 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1`（数据集只在 yss2；放 yss3 要先复制 2.3 GB）

    | 组 `<o>` | oracle 参数 | 和 `mi200.json` 的差异 |
    |---|---|---|
    | o1 | `-force-local-data-access` | 新增：L1V miss 一律走本地 L2/DRAM（地址转换和非 L1V 访存不变） |
    | o2b | `-rdma-max-outstanding=4096 -rdma-pipeline-width=64` | `rdma-max-outstanding` 64 → 4096，`rdma-pipeline-width` 8 → 64 |
    | o3 | `-switch-latency=1` | `switch-latency` 32 → 1（RDMA 端点固定的 10 周期等不变） |

    其余差异都不算配置修改：benchmark 参数、统计开关 `-report-gpm-trace -report-l2-source -l2-source-tile-width=7`（和 T17 第一轮相同）。`config_deviation` 由 `isca27_config.py` 生成（`-force-local-data-access` 现在标为 oracle，`0b85bef`）。
  - **个数和资源**：3 组 × 3 个 workload = 9 个运行。T17 的 B 墙钟 3.3–3.75 小时；T15 的 oracle 墙钟和 B 相近（fir：B 49 分钟、O1 52、O2b 51、O3 48），预计每个 3–5 小时，9 个并行约 4–5 小时。峰值内存（带 `-report-l2-source`）：MoE 21 GB、R-GAT 9.7 GB（P12 今天实测）、PagedAttention 约 28 GB（T17 规模确认时的估计，未实测）；O2b 在途多，可能更高。放置建议：R-GAT 3 个在 yss2，PA、MoE 各 3 个分在两台。
  - **对照 B**：T17 第一轮（`95615c4`）。注意 P12 发现 R-GAT 每次运行的结果不完全一样（见下一条），MoE 的原始 T17 结果和 3 次复跑也差 0.002%。**建议同时用 `ed1aca0` 补跑 3 个 B**（同一二进制、同一批），R-GAT 的 oracle 差异小于约 0.2% 的不算。
  - **R-GAT 方案 B（改成铺满 48 个 GPM）仍等你决定**（见下面 T17 那条）；没决定前按上面用现有版本。
- [ ] (P12) **R-GAT 的模拟结果每次运行不完全一样**：同样的命令跑了 5 次（T17 原始 `95615c4` 15.153 ms；P12 复跑 `95615c4` 15.145、`c9cdf64` 15.149、`ed1aca0` 15.137、`ed1aca0` + P12 tracer 15.129 ms），差 0.16%，统计的 md5 也各不相同。fir、MM、PagedAttention（batch 2）、MoE（T17 规模，三个二进制）复跑完全一致。R-GAT 的 host 端（采样、上传顺序）我读了代码，没发现不确定的地方，原因还没查。MoE：三个二进制复跑都是 5.979603 ms、统计 md5 相同，但 T17 原始运行的最后一个 kernel 结束于 5.979726 ms（差 0.002%），原因也没查。—— A：先不查，R-GAT 的差异 < 0.2% 时当噪声处理；B：查原因（可能是模拟器里依赖 Go map 遍历顺序的地方）。推荐 A，oracle 的差异远大于 0.2%。
- [ ] (P12) **`mgpusim/benchmarks/t17/pagedattn/azure_conv_tokens.csv` 没有进 git**：`mgpusim/.gitignore` 有 `*.csv`，所以 `DATASETS.md` 说“committed, embedded”的这个文件实际不在仓库里，干净的 checkout 编不过（`go:embed` 找不到）。这次是从 `wt-t17` 工作区复制过来编译的（md5 `570ff986b074…`）。—— 建议 `git add -f` 提交（158 KB），需要你同意。
- [x] (T17) **用户 10-17 确认**：W-A batch 66、W-B 规格形状 ffn 704（1.75 和 0 各一个）、W-C 320 个种子；两个模拟器 bug（`s_nop`、`v_mad_u64_u32`）都修（修复和回归在做，T17 的 kernel 已经绕开，不影响这次运行）。原问题：**规模确认 + 配置确认**（第 2 步）。所有命令 = runall2 的 mi200 baseline 参数（和 `mi200.json` 没有差异，保留 `-magic-memory-copy`）+ benchmark 参数 + `-report-l2-source -heartbeat-file`；二进制来自 `isca27/t17-workloads` `db47df3`。按 1/8 规模试跑外推（墙钟和 L2 请求量成正比：W-A、W-B 约 0.21 分钟/MB，W-C 0.27–0.37）。
  - **W-A PagedAttention**：`-benchmark=pagedattn -pa-batch=66`（Azure 长度，9.36 万 token，8 KV 头 × 128，分段 64）。2 个 kernel：partial 11,928 WG（每 GPM 252，最后一个 84，无空闲）、reduce 2,112（10 个 GPM 空闲）。footprint 794 MB。预计约 3.0 小时，峰值约 28 GB。（备选 `-pa-batch=83`：两个 kernel 都没有空闲 GPM，约 3.7 小时、34 GB。）试跑：远程读 95–97%，远程延迟占 96–99.8%，前 3 个 owner 占 12–16%。
  - **W-B MoE**：规格形状 `-moe-tokens=256 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75`，对照 `-moe-skew=0`。5 个 kernel；up/gate/combine 的 WG 少于 1344，结构上就有 20–38 个 GPM 空闲。footprint 约 574 MB。预计 3.9 / 3.6 小时，12–20 GB。（备选 `-moe-ffn=512`：约 2.9 / 2.7 小时。）试跑：远程读 95%，远程延迟占 97%，前 3 个 owner 占 22–23%。
  - **W-C R-GAT**：`-benchmark=rgat -rgat-data=~/akita-exp-test/datasets/igbh/tiny -rgat-seeds=320 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1`。30 个 kernel，共 13,416 WG（最大的 aggregation kernel 1,344 个，无空闲）。footprint 约 140 MB。预计约 4 小时（范围 3.3–5.4 小时），8–12 GB。**和规格的差别**：种子从 512 减到 320（512 会超过 5 小时上限）；备选 256 个种子（2.7–4.3 小时）。试跑：远程读 93%，远程延迟占 92%，前 3 个 owner 占 25%。接近 MLPerf 的 4 头 × 512 维：512 个种子要 31–47 小时，不可行；3 小时只能跑约 32–40 个种子。
  - **放置**：4 个运行（W-A、W-B 两个、W-C），长的放 yss3（T15 跑完之后），或者 yss2。
- [x] (T17) **已修（用户 10-17 批准）：`s_nop` 等调度器内部 SOPP 指令现在推进 PC（`3a94d26`）；L1 不失效的问题保持原样，只记录（D20 的说法要更正）。** 原问题：**模拟器的两个新发现**（子代理没有改模拟器，都在 kernel 侧绕开了）：(1) timing 模式下 `s_nop` 不推进 PC，wavefront 死循环（`timing/cu/scheduler.go` 的 `EvaluateInternalInst` 默认分支）；现在编译检查脚本拒绝 kernel 里的 `s_nop`。要不要在模拟器里修？（2）**开着 magic copy 时 kernel 之间不失效 L1**：先读后被下一个 kernel 改写的 buffer，再读会读到 L1 里的旧值；R-GAT 已改成每个关系写自己的 buffer。注意：真实 GPU 在 kernel 之间会失效 L1，我们不失效，**baseline 会多一些 L1 命中（偏快）**，D20 把它列为“有意比真实 MI200 弱”，方向其实相反，要更正。
- [x] (T17) **已修（用户 10-17）：488 改成 VOP3b、64 位目的/src2/进位（`b5cff1b`）；回归 fir、MM、llmop gemm 修复前后 total_time 和排序后统计的 md5 完全一致。** 原问题：**`v_mad_u64_u32`（VOP3 488）扫描结果**：80 个 hsaco 里只有 `gemm` 用到（4 个文件，各 24 处），在 64 位无符号除法/取余的慢路径上，只有被除数 ≥ 2³² 时才执行（执行了就会 panic）；链接这个 gemm 的有 fulllayer、lenet、minerva、vgg16、bert、gpt、resnet、llmop。现有结果都没有 panic，所以没走到这条路径，现有结果不受影响。修不修由用户决定。

- [ ] (T17) **R-GAT 的时间大部分花在只用 1–13 个 GPM 的 kernel 上**：30 个 kernel 里，85% 的模拟时间在用到 ≤13 个 GPM 的 kernel 上（42% 在 ≤3 个 GPM 上），48 个 GPM 的占用上限只有 19%。原因是 WG 按 `ceil(总 WG / 1344)` 连续切分，每个 GPM 拿 28 的倍数个 WG；R-GAT 的变换/注意力 kernel 只有 26–340 个 WG，但每个 WG 很长（例如 kernel 2：56 个 WG、2 个 GPM、1.7 ms）。MoE 的 expert kernel 也只用到 26–41 个 GPM（试跑时已知）。这样的 R-GAT 主要在测“少数 GPM 读远程数据”，不太代表 wafer 级执行。—— A：保持现状，论文里如实写；B：把 R-GAT 的变换/注意力 kernel 改成更多、更小的 WG，让每个 kernel 至少铺满 48 个 GPM，再跑一次（约 3–4 小时）；C：不改 kernel，在 driver 里把少于 1344 个 WG 的 kernel 摊到全部 GPM（改模拟器的 WG 分配，影响所有 workload，需要回归）。推荐 B（只改 T17 自己的 kernel，不碰冻结的模拟器）。
- [ ] (T17/T16) **W1 用 T17 再判断**：T17 的 kernel 尾部比都很高（时间加权 1.2–1.75），但和位置的相关性弱：主 kernel 的 |ρ|（对平均跳数）只有 0.05–0.37，只有 MoE 的 router kernel（10 个 GPM、占 19% 时间）ρ = 0.65。PagedAttention 的不均来自 Azure 序列长度不同（每个 WG 的工作量不同），MoE 来自每个 expert 的 token 数不同，都是数据导致的，不是位置。所以按 T16 的标准，T17 **没有给 W1 增加支持**：真实 workload 里满足“尾部比 ≥ 1.1 且 |ρ| > 0.5”的仍然只有 kmeans、pagerank（MoE router 这个短 kernel 只能算勉强第 3 个）。—— 建议：W1 不作为主线；“数据导致的负载不均 + 远程延迟”可以作为新的观察，交给 Cowork 讨论。最终由用户决定。
- [x] (T16 阶段 1) **用户 10-17 确认：跑 1c + 1d，不跑 1b。** 已在 yss2 启动 13 个运行（`results/1017_diag_t16-*_mi200_2e09bc9`，组 `t16-stage1-1017`）。原问题：**配置确认（CONFIG.md 第 3 节；冻结版 mi200，二进制来自 `isca27/t16-position` `2e09bc9`，模拟器时序代码和 `isca27-mi200-v1` 相同，只多了统计）**。所有运行都是 runall2 的 mi200 baseline 参数（和 `mi200.json` 没有差异）+ 统计开关 `-report-gpm-trace`（新）、`-report-l2-source -l2-source-tile-width=7`，保留 `-magic-memory-copy`。
  - **回归已通过**（yss2 测试目录，mi200，`-max-wg 4096`）：fir 改动前 / tracer 关 / tracer 开 total_time 都是 26.568 µs，16.5 万项统计排序后完全一致；MM tracer 关 / 开都是 345.433 µs，17.0 万项完全一致。（metrics.csv 本身每次行顺序不同，因为 `report.go` 遍历 Go map；排序后一致。）
  - **1c 微基准**（platbench，跑完整个 kernel）：① mi200 补跑 T14 的 all-to-all（每对 16 KB），约 13 分钟；② 远程比例扫描 `-plat-test=remotefrac`，每 GPM 1 MB 本地分片、224 个 WG、12 次迭代（每阶段读 504 MiB），f = 0 / 0.1 / 0.25 / 0.5 / 1 各一个运行，约 24 分钟 / 36 分钟 / 55 分钟 / 1.4 小时 / 2.5 小时，每个约 3.3 GB 内存；③ 可选：all-to-all 每对 256 KB，约 2.8 小时。共 6 个（加可选 1 个）。注意：重编 `kernels.hsaco` 后 T14 原有 kernel 的代码位置变了，旧 platbench 数字可能有极小差异，这次的 all-to-all 用新二进制重跑。
  - **1d 小输入**（`<workload>-t16small`，默认输入完全不变，有单元测试检查）：pagerank（64,512 节点、816 万边，1 × 64,512 WG）、im2col（H = W = 1661，1 × 1,376,144）、matrixtranspose（宽 4672，1 × 5,329）、spmv（4,400 万维，1 × 344,064）、kmeans（344,064 点，3 × 5,376）、matrixmultiplication（Y = 129,024，1 × 16,128）、aes（84 MB，1 × 86,016）；每个 1–2 小时、3–9 GB；WG 数都是 CU 总数的整数倍（matrixtranspose 最后一个 GPM 65 个），没有空闲 GPM。远程读比例（前 4096 个 WG）和默认输入相近：pagerank 0.85/0.83、im2col 0.91/0.91、matrixtranspose 0.98/0.98、spmv 0.27/0.21、MM 0.95/0.80、aes 0.023/0.023。共 7 个。
  - **1b（截断运行里的速率不均）**：T15 已经在跑，B 组没开 tracer，所以要单独补跑 B 组 + tracer（14 个 workload，`-max-wg 32768`，spmv 1536），耗时同 T15 的 B（kmeans 约 12.5 小时）。**但 T16 子代理发现截断运行里同时有 2.8–5.8 万个 WG 在执行（只完成 4,096 个时），速率只反映 kernel 开头，1b 的意义有限。** 可选方案 —— A 照跑 14 个 / B 只跑短的 13 个（不跑 kmeans）/ C 不跑 1b，用 1c、1d 代替。推荐 C（或 B）。
  - **放置**：1c、1d 都放 yss2（短）；1b 如果跑，长的放 yss3（T15 跑完之后）。总计 1c + 1d 约 13 个运行，单机约 10–14 小时 CPU，16 个并行下墙钟约 2.5 小时。
  - **需要在结果里分开报告的因素**（子代理发现）：小输入下 buffer 余数页给最后一个 GPM、< 48 页的 buffer 在 GPM 1；MM 默认输入的 64 KB B 矩阵也在 GPM 1（远程比例 0.80 的主要来源）；默认 matrixtranspose 的最后一个 GPM 分到 0 个 WG。这些是和位置无关的不均，不能算进 W1。

- [ ] (T15) **配置确认（CONFIG.md 第 3 节；mi200 冻结版 `isca27-mi200-v1`）**。
  - **运行方式**：用 runall2（`--remote-ablation --configs=baseline --gpm-preset=mi200 --max-wg=32768`，spmv 1536），oracle 的改动用 `--extra-benchmark-flags` 加在命令末尾（同名参数以后者为准）。**不用 `isca27_run.sh` + 配置文件**：`mi200.json` 只有 14 个参数，没有写 CuPath 各机制的开关，而模拟器有些机制默认是开的（如 `-remote-data-path-l2-enable` 默认 true），那样跑出来的 B 不是 baseline。runall2 的 baseline 显式关掉全部机制，和 10-15 ablation 的 baseline 参数逐个相同。
  - **共同参数**（6 组都一样）：10-15 mi200 ablation 的 baseline 命令（`-timing -num-memory-banks=8 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -gpm-preset=mi200 -l2-cache-size-mb=2 -mmutlb-lookup-latency=80`，加上 runall2 baseline 的全部机制关闭开关）+ 统计开关 `-report-l2-source -l2-source-tile-width=7`（只加统计，不改配置）。
  - **6 组**（相对 `mi200.json` 的差异；没有改 mi200 或 hpca 的任何数值，oracle 是有意偏离的分析实验）：

    | 组 | 额外参数 | 和 mi200.json 的差异 |
    |---|---|---|
    | B | （无） | 无 |
    | O1 | `-force-local-data-access` | L1V miss 一律走本地 L2/DRAM；地址转换和非 L1V 的访存不变（远程地址转换没有去掉） |
    | O2a | `-l1v-mshr-entries=256 -l1v-max-concurrent-trans=256` | L1V 每 CU 在途 16 → 256 |
    | O2b | `-rdma-max-outstanding=4096 -rdma-pipeline-width=64` | RDMA 在途 64 → 4096，流水线宽度 8 → 64 |
    | O2 | O2a + O2b | 两者 |
    | O3 | `-switch-latency=1` | 每跳 32 → 1 周期（RDMA 端点固定的 10 周期等仍在） |

  - **按诊断 A 是否调整 O2a/O2b**：不调整。诊断 A 显示 L1V 在途 16/32/64 对 L2 带宽没有影响，256 已远超；远程带宽到 RDMA 256 仍线性增长、没有饱和点，4096 是 T15 的原值，比测到的范围大 16 倍。
  - **运行个数和时间**：14 × 6 = **84 个**。按 10-15 baseline 的实测耗时分两台：**yss3（长）**：kmeans（12.5 小时）、matrixtranspose（3.8 小时）、matrixmultiplication（1.9 小时）、aes（1.7 小时）、pagerank（1.5 小时）× 6 = 30 个，16 个并行，墙钟约 13 小时（受 kmeans 限制）；**yss2（短，内存条有故障，只放短的）**：fir、fft、fwt、im2col、simpleconvolution、floydwarshall、spmv、bitonicsort、relu × 6 = 54 个，每个 2–55 分钟，墙钟约 1.5 小时。oracle 组的耗时可能不同（O1/O2/O3 多半更快）。yss2 上因硬件故障失败的，挪到 yss3 重跑。
  - **kmeans**：T15 说“短片段也太慢就先报告”：baseline 单个 12.5 小时。建议照跑（6 个并行，不影响其他 workload 的完成时间）；或者把 kmeans 降到 `-max-wg 8192`（约 3 小时），但那样就不能和 10-15 的 m2/Complete（32768）直接比，需要补跑。
  - **有效性检查**：每格和 B 比较 kernel 数（从进度输出数）、完成的 WG 数（`cu_wg_count` 合计）、总指令数（`cu_inst_count` 合计），差异 > 1% 的标为“oracle 无效”。另外 B 应该和 10-15 的 baseline 完全相同（只多了统计开关），可以顺便确认。
  - **结果目录**：`results/1016_diag_oracle-<组>_mi200_<commit>/`（runall2 campaign，每组一个）。

- [x] (P08/T13) **用户 10-16 决定：保持 MSHR 32，现在冻结。** 已打标签 `isca27-mi200-v1`（`67f1c03`）。原问题：**mi200 冻结（`isca27-mi200-v1`）和每 slice MSHR 32 → 128**。验证清单见“结果摘要 → mi200 验证”（1–4、6 通过，5 是 HBM 31%，7 冻结时写）。**MSHR 改动的影响范围（P08 d）**：只改 `akkalat/baseline/runner/gpm_preset.go` 里 mi200 预设的 `L2MSHRPerBank`（hpca 是 64、mi300x 是 16，都不受影响，旧结果仍可复现）；HBM 读预计从 74 升到约 111 B/周期（31% → 46%，10-15 诊断 v1）。**改了就要重跑全部已完成的 mi200 结果**：短片段 ablation（70 个，kmeans 单个 11–13 小时）、可复现检查（4）、LLM 算子（110）、数据放置（14，远程比例应基本不变但延迟会变）、T14 平台测量（29）；在 yss3 上约一天（受 kmeans 限制）。T15 也要在改完之后跑。可选方案 —— **A** 保持 32 冻结 / **B** 改 128 后重跑再冻结。由用户决定（D21 暂定 A）。
- [ ] (P08 a) **m3（Remote Reuse）在 kernel 边界没有失效，和论文 IV-C 不一致**（代码位置见“规格与代码的差异”）。影响：m3 的收益对多 kernel 的 workload 可能偏乐观（floydwarshall、kmeans、pagerank 等多次迭代）；owner 后续 kernel 写了的数据，请求方可能读到旧值（timing 模式不做 verify，查不出来）；T8（写签名选择性失效）在模拟器里目前没有可优化的对象。可选方案 —— **A** 实现 kernel 边界失效（只失效请求方 L2 里来自远程的 line），重跑 m3/Complete / **B** 改论文描述，说明实现不失效并论证安全性 / **C** 两者都做，A 作为主结果、B 的数字作为“不失效的上限”。推荐 A（论文的说法和审稿人对一致性的关注都需要它）；需要改模拟器，等用户决定后再开任务。
- [ ] (P08 b) **B1 对照 baseline（远程数据总进请求方 L2，不改默认）的工作量**：代码约 1–2 天——在 `mgpusim/timing/rdma/remote_requester.go` 加一个开关，绕过 m3 的“值得复用”判断，每个远程响应都按普通 LRU 填进请求方 L2；T3 的 B1 规格要求“在 kernel 边界和同步点失效”，这正是 (a) 缺的那部分，两者共用同一套失效实现。模拟时间：主结果加 1 列 = 每个 workload 多 1 个运行（+20%）；mi200 短片段下中位数约 48 分钟，kmeans 约 12 小时，16 个并行位置下墙钟约多半天。需要用户决定是否加入主结果。
- [ ] (P08 c) **数据放置的候选修正（只给方案，没实现）**。诊断 C 的新发现：**没有小于 192 KB 的 workload buffer**（每个 workload 的 buffer 都是几百 MB，见结果摘要），被整个放在 GPM 1 的小块是运行时分配（kernarg、code object 等，每个程序约 144 次、合计 < 100 KB），它们解释了 aes/fir/simpleconvolution 的 0.1–1.8% 远程读（100% 来自 GPM 1）。真正的大问题是 **WG 段和页段错开**（kmeans：hpca 0% → mi200 42.9% 远程读，baseline 慢 3.7 倍）。候选：**P1** WG 段和页段按同一规则切分（页段按每个 GPM 的 CU 数占比切，而不是 48 等分）——只影响 CU 数不同或不整除的配置；hpca 每 GPM 32 CU、48 等分时两者本来就对齐，旧结果可复现（需要回归确认）。**P2** 运行时的小分配（kernarg 等）放在发起 kernel 的 GPM 上，或复制到每个 GPM——改变 hpca 的 0.1–1.8% 远程读，hpca 旧结果不能逐位复现（加开关、hpca 默认关）。**P3** 小 buffer（< 48 页）按页交错——现有 workload 没有这种 buffer，对旧结果没有影响，只对 T10 的新负载有意义。推荐 P1（对 mi200 影响最大、对 hpca 无影响）；是否作为 baseline 的一部分还是作为可优化的点（用户 10-14：“保持现状，这也算一个可以优化的点”），由用户决定。

- [x] (T11) 阶段 B 的运行工作流 —— 已按推荐 B 执行（用户 10-13 说“先实现吧”）：用 `exp` 工具，经由符号链接 `~/Desktop/CuPath/cupath` 运行（真实目录名带空格，`exp` 处理不了）；结果按约定放在 `results/<exp_id>/`。
- [x] (T11) 工作区不干净 —— 已按推荐 B 执行，但没有删除：两个文件都移到了仓库外的 `~/Desktop/CuPath/_moved_out_of_repo/`。另外把 `/remote/`、`/results/` 加进了 `.gitignore`（否则每次 `exp pull` 之后工作区都不干净）。
- [x] (T11) P2 的统计 —— 已按推荐 A 执行（`-report-inst-count`）。
- [x] (T10/T11) **已决定（用户 10-13）：修 driver（方案 B），要求修完能跑起来，见 `tasks/T12_driver_flush_fix.md`。** 原问题：**driver 的 flush 竞争 bug 要不要修**：同一个 Context 里多个 queue 并发时，任一 kernel launch 会把整个 Context 的 buffer 标脏，其他 queue 的 kernarg H2D 于是触发**对全部 48 个 GPM 的 L2 flush**（flush 会清空 L2 目录，`akita/mem/cache/writeback/flusher.go:176-181`）；而 `processFlushReturn`（`mgpusim/driver/memorycopy.go:293`）只删除自己、不检查命令是否已完成，flush 回复晚于 H2D 回复时命令永远不出队 → 死锁（第一次 P4 就是这样卡住的，只有 17/48 个 GPM 启动）。—— A：不改模拟器，T10 一律每个 GPM 一个 Context（探针骨架已经这样做，和 mccl 的 `CommInitAllMultipleContexts` 一致）/ B：修 driver（改原有行为，需要你批准）。推荐 A。对已有数据的影响应该很小：现有 benchmark 在 runner 下只拿到一个 unified GPU，所以只有一个 queue（例如 fir 的 `exec`，`mgpusim/benchmarks/heteromark/fir/fir.go:146`，`b.gpus` 只有 1 个元素），不会出现多个 queue 互相触发 flush；只有自己按 GPM 建多个 queue、又共用一个 Context 的代码才会中招。
- [x] (T10) **已决定（用户 10-13）：这是参数用错了，不是模型问题。** T11 第一轮用的是 baseline 二进制的默认参数（`-bandwidth=1` 即 16 GB/s、`-switch-latency=20`、没有 `-magic-memory-copy`、`-num-memory-banks=4`、`-mmutlb-lookup-latency=80`），不是论文配置（`-bandwidth=48` 即 768 GB/s、`-switch-latency=32` 等，见 `CONFIG.md`）。P3/P4 的性能数字作废，要用论文配置重跑；以后所有实验的参数都从 `runall2_constants.py` 导入（`t11_run_probe.sh` 已改，commit `d6b765c`）。原问题：**单个 GPM 的远程读带宽只有本地的约 1/18**：P3a 里一个 GPM 远程读 2 MB 用 200.9 µs（≈10.4 GB/s，平均远程延迟 3.05 µs），本地读同样 2 MB 用 11.4 µs（≈184 GB/s）；而且远端 L2 命中和远端 DRAM 的时间几乎一样（199.5 vs 200.9 µs），说明瓶颈在 RDMA/mesh 这一段而不在数据来源。T10 的通信 kernel 如果是带宽受限的，CuPath 在“远端 L2 命中 vs 未命中”上的收益会被掩盖。—— 是否先确认 RDMA/mesh 参数（`-rdma-max-outstanding 64`、`-rdma-pipeline-width 8`、`-bandwidth 1`、`-network-flit-size 16`）就是论文要用的配置？

- [x] (T12) ~~R2 回归太慢，要不要换成小输入的 benchmark~~ —— **不需要决定了**：4 个 R2 在 07:17–07:56 UTC 全部正常跑完（exit 0）。我“要十几个小时”的估算是错的（拷贝结束后模拟速度明显变快）。结果见日志。

- [x] (T12/T13) **用户 10-13 决定：保留 `-magic-memory-copy`。** 论文配置（`BASE_COMMON_FLAGS`）里本来就有，没有改过；之后的 T11 重跑、T13 新预设和全量重跑都保留它。只有 T12 的 R2、R3 两组验证临时去掉了它（在 `config_deviation` 里注明，不进论文）。

- [x] (T13) **已决定（用户 10-13）：同意按代码的分析；阶段 3 校准时旧配置也用同一个微基准实测，以实测值为准。** 原问题：(T13) **旧论文配置的 HBM 带宽，按代码算是 128 B/周期/GPM，不是 T13 表里的 375**：DRAM 控制器是 500 MHz、256-bit、BL4，`burstCycle = BL/2 = 2` 个 DRAM 周期（`akita/mem/dram/builder.go:781-793`），每次访问 128 B，所以每个控制器的理论峰值是 128 B / 4 ns = 32 B/ns；论文配置 `-num-memory-banks=4`，每 GPM 4 个控制器 = **128 B/周期**。（T11 P1 实测 184 B/ns 并不矛盾：那次用的是二进制默认的 `-num-memory-banks=16`，理论峰值 512 B/ns。）这意味着 T13 里“新配置 HBM 带宽比旧配置低约 16%”的说法按代码不成立：新目标 315 B/周期约是旧配置的 **2.5 倍**。—— 建议：阶段 3 的校准也在**旧配置**下跑一遍同样的微基准，用实测值确认旧配置到底是多少；论文 Table II 里旧配置的 HBM 带宽数字需要你核对。
- [x] (T13) **已决定（用户 10-13）：方案 A（64-bit、BL8、每次访问 64 B、DRAM 时钟 1.23 GHz，时序按纳秒不变换算）。延迟不做每周期等效缩放（带宽按 2.1 GHz 每周期等效，延迟保持纳秒值），写进 STATUS 和 meta。最终以阶段 3 实测为准。** 原问题：(T13) **新配置的 HBM channel 怎么设**：每 channel 的目标是 19.7 B/周期（315.5 / 16）。现在的 DRAM 时序全部写死（`r9nanobuilder.go:945-1000`），如果只把控制器数从 4 改成 16、时序不变，每 GPM 理论峰值是 512 B/周期，比目标高 62%。—— A：按 HBM3 的结构建模：每 channel 64-bit 总线、BL8（每次访问 64 B，正好一条 cache line），DRAM 时钟取 1.23 GHz，使每 channel = 64 B / (4 / 1.23 GHz) ≈ 19.7 B/ns；tCL、tRCD 等时序参数按纳秒不变的原则从 500 MHz 换算到 1.23 GHz（乘以 2.46 后取整）。/ B：保持 256-bit、BL4 不变，只把 DRAM 时钟降到约 308 MHz，使每 channel = 128 B / (2 / 0.308 GHz) ≈ 19.7 B/ns（改动最小，但每次访问 128 B，比 64 B 的 line 大）。推荐 A（更接近 HBM3，访问粒度和 cache line 一致），最终都以阶段 3 实测为准。
- [x] (T13) **已决定（用户 10-13）：同意方案 A。预设取值集中在 `akkalat/baseline/runner/gpm_preset.go`，原文件只做“常量 → 预设字段”的替换，旧配置的数值不改；阶段 2 用旧配置回归（排序后逐项比较，total_time 单独比较），并检查分析脚本里有没有假设 L2 只有 4 个分区。** 原问题：(T13) **阶段 2 需要修改 CONFIG.md 第 1 节列出的文件**：新预设要改的值大多写死在 `shaderarray.go`、`r9nanobuilder.go`、`timingplatform.go` 里，需要在 `flag.go` 加 `-gpm-preset`，并把这些写死的值改成由预设决定的参数。**旧配置的数值一个都不改**（默认预设 = 现在写死的值），并用回归证明周期数完全一致。按你的规则“需要修改 CONFIG.md 第 1 节列出的文件”要先问你。—— A：照上面做，预设的取值集中写在一个新文件 `akkalat/baseline/runner/gpm_preset.go` 里，上面几个文件只做“把常量换成预设字段”的替换 / B：你有别的偏好。推荐 A。

- [x] (T13 阶段 2) **用户 10-13 确认：旧配置回归“跑出来再说”；之后又确认了改用配置文件后的两组运行（fir 配置文件验证、T12 R3），全部确认。** 原问题：**配置确认（CONFIG.md 第 3 节）：请确认下面两组运行的参数和差异。** 注意：旧配置回归**已经在 P06 到达之前启动**（我当时按 T12 R1 已确认的参数直接跑了，没有为阶段 2 单独等确认，这一点不符合 P06 的要求）；它还在跑，按 SYNC 规则没有打断。如果你不认可，我把结果作废、等确认后重跑。
  - **旧配置回归（运行中，yss2，commit `f444076`，默认预设 hpca）**：`-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -disable-servers -mmutlb-lookup-latency=80 -max-wg=4096 -benchmark fir|matrixmultiplication`。差异：只有 `-max-wg=4096`（截断参数）和 `-benchmark`。和修改前的基准（T12 R1，`9c30a89`）参数完全相同。
  - **T12 R3（带 magic copy，T12 文档新要求，待确认后运行）**：`-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -disable-servers -report-l2-source -l2-source-tile-width=7 -mmutlb-lookup-latency=80 -benchmark probe -probe-all-gpms -probe-shared-ctx -probe-bytes 2097152 -probe-passes 2`。差异：统计开关 `-report-l2-source`/`-l2-source-tile-width=7`，探针参数。用修复后的二进制（`9c30a89`，模拟器代码和 T12 分支头 `36bde23` 相同），yss3。验收：48 个 GPM 都有指令、两遍都完成、心跳不报卡死。
- [ ] (同步) **用户 10-13 决定：不新建 GitHub 仓库，文档仓库作为代码仓库 `DX990307/WSC_datapath` 的新分支 `isca27/docs` 备份。** **代码仓库的三个任务分支已推送**（`isca27/t11-probe-placement-comm` `d6b765c`、`isca27/t12-driver-flush-fix` `36bde23`、`isca27/t13-mi300x` `f444076`）。**文档分支 `isca27/docs` 还没推**：在 `isca27/` 里加 remote 的操作被自动模式拦下，需要用户在终端运行一次：`cd ~/Desktop/CuPath/isca27 && git remote add origin git@github.com:DX990307/WSC_datapath.git && git push -u origin main:isca27/docs`。原问题：**GitHub 推送被本机的权限规则拦下，需要你处理**：① 这台机器没有安装 `gh`，没法用命令行在 GitHub 上建 `DX990307/cupath-isca27`；② `git push origin isca27/...` 被 Claude Code 的自动模式拦截（“Out-of-Place Publication”）。—— A：你在 GitHub 网页上新建空的私有仓库 `DX990307/cupath-isca27`（不要勾选 README），并允许我推送（例如在 `/permissions` 里允许 `Bash(git push origin isca27/*)` 和 `Bash(git push -u origin main)`，只在 isca27 目录），我来加 remote 并推送 / B：你自己在终端运行推送命令（见日志里的命令）。

- [x] (T13) **用户 10-13 决定：配置放在专门的配置文件里，不再在命令行写一长串；配置文件可以保存多份、运行时读取不同的文件，方便做敏感性实验。** 另外**不再使用 `-disable-servers`**（用户担心它会带来 bug）。实现见日志。

- [x] (T13 阶段 3 + T14 第一档) **用户 10-13 确认。** 配置确认（CONFIG.md 第 3 节），详见日志 10-13“T13 阶段 3 + T14：配置确认”。两套配置 `hpca`、`mi300x_eighth`，代码 `0e38983`；运行计划 29 个进程/配置，预计约 1 小时（hopmatrix 最慢）。

- [x] (T13 阶段 3) **用户 10-13：先按 mi300x 配置把论文实验跑一遍（相当于暂时接受实测值，不调参数）；诊断以后再说。** 原问题：**校准偏差超过 10%，按规则停下来报告（没有调任何参数）。** 详见“结果摘要 → T13 阶段 3 校准”。
  - **mi300x HBM 读**：峰值 243 B/周期（8 个 CU），满 38 个 CU 时 224；目标 315 → **−23%**（满 CU −29%）。写 318（+1%）、复制 295（−6%）达标。读比写低，说明不是 DRAM 的总线带宽不够，而是读的路径（可能是 DRAM 的行冲突/调度，或 L2 MSHR 每 channel 16 个）限制了。
  - **L2 带宽（两套配置都一样）**：读峰值约 630 B/周期（16 个 CU），写约 640（8 个 CU），复制约 850；满 CU 时反而降到 420–700。目标 2 KB/周期 → **−69%**（读、写），复制 −58%。hpca 的 L2 理论吞吐（4 KB/周期）是 mi300x 的 2 倍，但两套曲线几乎重合，所以**瓶颈在 L2 之前**（候选：每个 L1V 16 个 MSHR/在途请求、地址转换/TLB、L1→L2 的连接），不是 L2 本身。
  - **旧配置（hpca）HBM**：实测 59 B/周期（读）、66（写）、61（复制），1–2 个 CU 就饱和；代码算的理论值 128，按 64 B 请求的有效值约 64，实测和后者一致。投稿版 Table II 写的 0.375 TB/s/GPM 是实测的 6.4 倍。
  - 可选方案 —— **A**：接受实测值，把它们作为配置的实际特性写进论文和 CONFIG（不改参数）/ **B**：先做诊断：只加统计（L1V MSHR 满的周期、L2 每 channel 的请求率、DRAM 行命中率等），不改任何配置数值，找出 L2 和 HBM 读的瓶颈，再请你决定要不要调 / **C**：直接调参数凑目标（要改 mi300x 预设的数值，必须你批准）。**推荐 B**：L2 −69% 两套配置都有，很可能也影响旧配置下论文结果的解读，先弄清楚瓶颈再决定。

## 规格与代码的差异

- (P08 a, 10-16) 论文 IV-C 写的：Remote Reuse 保留的远程 line “are invalidated at the same kernel boundaries and synchronization points” / 实际情况：**模拟器在 kernel 边界不做任何 cache 失效**。kernel 启动（`mgpusim/timing/cp/commandprocessor.go:320` `processLaunchKernelReq`）直接派发，不发 flush；cache flush（`commandprocessor.go:755` `processFlushReq`，失效 L1I/L1S/L1V/L2）只由 driver 的内存拷贝中间件在目标 buffer 是 L2 脏时触发（`mgpusim/driver/memorycopy.go:43,98,181`），而论文配置一直用 `-magic-memory-copy`，走的是 `driver/memorycopyglobalstorage.go`，**完全不发 flush**。Remote Reuse 自己只在请求方自己写某条 line 时把它标为不可缓存（`mgpusim/timing/rdma/remote_requester.go:1007` `noteLegacyRemoteReqSent`）；owner 端的写不会通知请求方；`resetRemoteDataPathHistory`（`remote_requester.go:1653`）只在 RDMA drain 时调用，而 drain 只由页面迁移触发（`mgpusim/driver/driver.go:587`），而且只清记账、不清 cache 内容。/ 我的处理：没有改代码，写进“需要用户决定”。

<!-- 任务文档写的和代码实际不一致的地方。格式：- (T#) 文档写的：… / 实际情况：… / 我的处理：… -->
下面的路径都相对于仓库根目录 `~/Desktop/CuPath/Screen Recording 2024-08-29 at 12.01.50 PM.mov/`（git 仓库 `DX990307/WSC_datapath`，T11 分支 `isca27/t11-probe-placement-comm`）。

- (T11) 文档写的：上游是 mgpusim **v5** / 实际情况：fork 是 **mgpusim v3**（`mgpusim/go.mod:1` 的 `module github.com/sarchlab/mgpusim/v3`），akita 也是 v3（`akkalat/go.mod:29-31` 用 `replace` 指向本地 `../mgpusim`、`../akita`），Go 1.22.0（`mgpusim/go.mod:48`）/ 我的处理：以 v3 代码为准；用到的 driver API 在 v3 里都有，签名和上游一致（见日志）。
- (T11) 文档写的：探针放在 `amd/benchmarks/microbench/probe/` / 实际情况：没有 `amd/` 这一层，benchmark 在 `mgpusim/benchmarks/` 下；实验入口是 `akkalat/baseline/main.go`（`-benchmark <名字>`），名字在 `akkalat/benchmarkselection/benchmark.go:81` 的 `SelectBenchmark` 里注册 / 我的处理：探针放在 `mgpusim/benchmarks/microbench/probe/`，在 `SelectBenchmark` 里新增一个 `case "probe"`。
- (T11) 文档写的：“GPM 5”、“48 个 GPM” / 实际情况：有**两套编号**。driver 的 GPU ID 是 1–48（0 号是 CPU）；组件名是 `GPU[t]`，t = x + 7y 是 7×7 mesh 上的格子编号，中心格子 t=24 留给 driver/IOMMU，所以 ID ≤ 24 时 t = ID−1，ID ≥ 25 时 t = ID（`akkalat/baseline/runner/timingplatform.go:570-581`、`:790-792`）。L2 source 统计里的 GPM 编号用的是 t（`akita/mem/trace/l2source_util.go:14` 从名字里解析），跳数是 7×7 上的曼哈顿距离（`l2source_util.go:27`）/ 我的处理：探针参数用 driver ID；STATUS 里同时写出 driver ID 和 `GPU[t]`。P3a 的 owner=5、runner=1 对应 `GPU[4]`(4,0) 和 `GPU[0]`(0,0)，相距 4 跳；P3b 用 driver ID 2/4/7/48（`GPU[1]`/`GPU[3]`/`GPU[6]`/`GPU[48]`），离 runner 1 分别是 1/3/6/12 跳。
- (T11) 文档写的：远程请求走“RDMA 还是 CuPath 新加的 NI” / 实际情况：没有单独的 NI 组件，远程访问只走 RDMA 引擎。CuPath 的远程数据通路（batching/去重/filter/prefetch）是加在 RDMA 引擎里面的（`mgpusim/timing/rdma/remote_requester.go:357`），默认关闭，用 `-remote-data-path-enable` 打开（`akkalat/baseline/runner/flag.go:165`）/ 我的处理：T11 先用默认关闭的配置跑；如果要对比打开的情况，等用户决定。
- (T11) 文档未提：unified GPU 的**内存分配策略被 fork 改过**。上游按页 round-robin，fork 改成连续分块（每个 GPU 分到连续的 numPages/numGPUs 页）（`mgpusim/driver/internal/memoryallocator.go:159-166`、`:213-234`）。对普通 GPU（非 unified）没有影响，`AllocateMemory` 仍然分配在当前选中的 GPU 上 / 我的处理：只记录，不改。
- (T11) 文档未提：`EnqueueLaunchKernel` 会把 code object、kernarg 和 AQL packet 分配在 `ctx.currentGPUID` 上（`mgpusim/driver/kernel.go:40-51`），而不是 queue 绑定的 GPU 上 / 我的处理：SPMD 骨架里每次 enqueue 前都先 `SelectGPU(ctx, g)`，否则取指和读 kernarg 会变成远程访问，污染 P1/P4 的远程事务数。

- (T11) 文档写的：P2 看“每个 GPM 的 WG 完成记录” / 实际情况：没有现成的 WG 完成统计 / 我的处理：用 `-report-inst-count` 的每 CU 指令数（为 0 即没执行过 WG）。
- (T11) 文档写的：P3a 预期“第 2 遍明显比第 1 遍快” / 实际情况：默认配置（32768 个线程）下带宽受限，两遍几乎一样（200.9 vs 199.5 µs），尽管第 2 遍确实全部命中远端 L2 / 我的处理：加了一个延迟受限的变体（只有 1 个 WG，256 线程），第 2 遍快 31%（1458.6 → 1002.0 µs）。P3b 也同样补了 1 个 WG 的变体。
- (T11) 文档写的：P3c 记录“写之后 GPM 5 的 L2 是否变成 dirty” / 实际情况：没有直接导出 L2 dirty 位的统计 / 我的处理：从统计推断：写全部是 `remote_write_allocate`（在远端 L2 分配，不读 DRAM），整个运行中 GPM 5 的 DRAM 写事务为 0，所以写入的数据停留在远端 L2 里，是 dirty 的。
- (T11) 文档写的：`run_exp.sh` + GNU parallel 分发 / 实际情况：T0 没做，两台服务器也都没装 GNU parallel / 我的处理：用 `exp` 工具 + `akkalat/t11_run_probe.sh`（写 meta.json、summary.csv、stats/、stdout.log、stderr.log）。服务器上同步的代码没有 `.git`，commit 由本机检查工作区干净后传进去。
- (T11) 文档写的：探针放在 owner 上直接 `AllocateMemory` / 实际情况：探针在本机编不出 hsaco（macOS 的 clang 没有 amdgcn 目标），`kernels.hsaco` 是在 yss2 上用 `build_hsaco.sh` 编译后拉回本机提交的（clang 14，`-mcpu=fiji`，code object v2，和 LLM 算子的编译选项一致）。

- (T12) 文档写的：回归“周期数和修复前完全一致”，我在确认时写的是“metrics.csv 逐行完全一致” / 实际情况：`akkalat/baseline/runner/report.go:302` 用 `for name, value := range values`（Go map）输出 filter_prefetch 系列统计，**同一个二进制每次运行的行顺序都不一样**（这段代码从 `8648917` 起没有改过） / 我的处理：比较“去掉行号、排序之后的内容”是否完全一致；`total_time` 单独比较。
- (T12) 文档写的：用论文配置做回归 / 实际情况：论文配置里的 `-magic-memory-copy` 会让 driver 根本不加载 `defaultMemoryCopyMiddleware`（`mgpusim/driver/builder.go:102-114`），所以 R1 只能证明论文配置不受影响 / 我的处理：按用户同意加了 R2（去掉 `-magic-memory-copy`），见“需要用户决定”。

- (T13) 文档写的：CuPath typed filter “每个分区 16K 槽（= 1 MB / 64B）”，新配置“每个 channel 4K 槽” / 实际情况：容量是**每个 L2 分区 2 × 块数**，再向上取整成 2 的幂个 bucket（`akita/mem/cache/writeback/builder.go:356-358`，`typed_filter.go:310-320`）：旧配置每个 1 MB 分区 32K 槽（每 GPM 128K），新配置每个 256 KB channel 8K 槽（每 GPM 仍是 128K） / 我的处理：以代码为准；新配置不用改代码，每 GPM 的总槽数自动保持不变。
- (T13) 文档写的：旧配置“每 GPM 375 B/周期”HBM 带宽 / 实际情况：按代码是 128 B/周期（见“需要用户决定”）。
- (T14) 文档写的：第 4 项要给出“RDMA 表满的周期占比、链路利用率”作为瓶颈证据 / 实际情况：模拟器没有这两种统计 / 我的处理：链路利用率按 YX 路由和每对流量推算（`pattern_links.csv`，标注为推算值）；RDMA 表满的占比没有给出，只用 Little 定律推算在途请求数。要拿到直接证据需要加统计（改代码），属于第二档的延迟分解，没做。
- (T14) 文档写的：第 3 项 48×48 全部 GPM 对 / 实际情况：按文档的备选方案测了 329 对（GPM 1 → 全部、全部 → GPM 1、另外 5 个起点 → 全部）。
- (T14) 文档写的：第 1 项“只用 1 个线程” / 实际情况：1 个线程时 wave 内地址一致，编译器会把指针追逐编成标量加载 `s_load_dword`，走 16 KB 的 L1 标量缓存而不是 L1V / 我的处理：起点加上 `get_local_id(0)`（恒为 0），迫使编译器用 `flat_load_dword`（`ca7ea0b`，反汇编已确认）。`0e38983` 的延迟和跳数结果作废，已用 `ca7ea0b` 重跑。

- (T13) **用户要求核实 (a)：HPCA 投稿版实验的 HBM 带宽实际是多少**（用户说论文 Table II 写的是 0.375 TB/s/GPM）。核实结果：
  - **代码**：从第一个 commit（`6f6985d`）到 `95b4f46`，每个 DRAM 控制器的参数都没有变过：500 MHz、HBM 协议、256-bit 总线、BL4（`r9nanobuilder.go` 的 `createDramControllerBuilder`）。`burstCycle = BL/2 = 2` 个 DRAM 周期 = 4 ns，每次访问 128 B，所以**每个控制器的理论峰值是 32 GB/s**。
  - **控制器数**：`a185a7e`（07-11）之前 runall2 用 `-num-memory-banks=16`，之后改成 4。`akkalat/results/` 里 07-21 以后的所有实验（sampled-validation、baseline-complete-observation、m1-m2-m3-rerun、cupath-llm、两次 cupath-sensitivity、filter-authoritative-validation、baseline-library）记录的命令里**全部是 `-num-memory-banks=4`**。
  - 所以 HPCA 投稿版实验的 HBM **理论峰值是每 GPM 4 × 32 = 128 GB/s = 0.128 TB/s**。另外，访问单位是 128 B，而 L2 缺失请求是 64 B 一条 line，一次 64 B 请求也要占用一整个 128 B 的 burst，所以**按 64 B 请求算的有效峰值只有约 64 GB/s/GPM**（除非相邻两条 line 合并成一次访问）。以上都是理论推算，阶段 3 用微基准实测为准（用户已同意）。
  - **论文文字**：仓库里能找到的版本（`4a04003` 和 `95b4f46` 的 tex，`main-draft.pdf`、`95b4f46` 的 `main.pdf`）写的都是“HBM 8 GB/GPM, four controllers, **1.23 TB/s**, BL4”，没有出现 0.375；0.375 TB/s/GPM 只出现在 `isca27/00_background.md` 里（和审稿人 C 说的“4 个 GPM 1.5 TB/s”一致）。**投稿的 PDF 不在仓库里，我没法核对 Table II 的原文**。不管是 0.375 还是 1.23，都和代码的 0.128 不一致。
- (T13) **用户要求核实 (b)：HPCA 投稿版实验的 typed filter 用了多少 bucket/槽，CACTI 按哪个容量算**。核实结果：
  - **代码默认**：`config.Capacity = numBlocks * 2`（`akita/mem/cache/writeback/builder.go:357`；从 `4a04003` 起就是这样，更早的 commit 没有 typed filter 的容量逻辑），1 MB 分区有 16384 块 → 容量 32768 槽 → 8192 个 bucket × 4 槽。
  - **实际运行**：主实验没有设 `-typed-filter-capacity`。运行统计里记录的 `typed_filter_buckets` 在 `2026-07-22-m1-m2-m3-rerun`（8064 行）、`2026-07-24-05-33-37-cupath-llm`、`2026-07-26-filter-authoritative-validation` 里**全部是 8192**，`typed_filter_slots_per_bucket` 全部是 4。也就是**每分区 8K bucket × 4 槽 = 32K 槽，每 GPM 128K 槽**，是论文写的 4K bucket 的 2 倍。
  - **敏感性实验不一样**：`run_cupath_sensitivity.py:49` 的参考点是 `REFERENCE_FILTER_BUCKETS = 16 * 1024`（每 GPM 16K bucket = **每分区 4K bucket**），用 `-typed-filter-capacity=16384` 显式设置。`2026-07-25-19-41-19-cupath-sensitivity` 里扫 filter 大小的点是每分区 1K/2K/4K/8K bucket，**其他敏感性参数都是在每分区 4K bucket 下跑的**，和主实验的 8K 不是同一个点。
  - **论文文字**：`4a04003`（07-23）的 tex 写的是“One shared array/L2 partition, **4K buckets**, four slots/bucket”（和用户说的一致）；`8648917` 改成“32K buckets/GPM”；`95b4f46` 写的是“Four physical arrays/GPM, **8K buckets/array**”，和实际运行一致。
  - **CACTI**：`akkalat/cost/CUPATH_CACTI_AREA.md`（`95b4f46` 版）按**每阵列 8K bucket**算：4 个 filter 共 336 KiB/GPM、2.50 mm²，CuPath 全部规则阵列 3.74 mm²，占 4 MB L2（54.51 mm²）的 6.86%，和实际运行的容量一致。`CUPATH_4K_CACTI_AREA.md` 是更早的 **4K bucket** 版本，文件开头写明是“earlier 4K-bucket what-if point”。投稿时（论文写 4K）引用的面积如果来自这个 4K 版本，那么**面积是按实际运行容量的一半算的**。投稿版 PDF 里引用的是哪个面积数字，需要用户对照投稿稿确认。用户 10-13 说论文写的是“每 GPM 168 KiB”：这正好是 4 个分区 × 4K bucket × 4 槽 × 21 bit = 64K 槽 × 21 bit = 168 KiB，对应 4K bucket 的配置；**实际运行的容量对应 336 KiB/GPM**（8K bucket × 4 槽 × 4 个分区 = 128K 槽），是论文写的 2 倍。

## 结果摘要

### P18 ASTRA-sim 交叉验证（10-21，`results_index/astra/`）

- **工具**：ASTRA-sim master `4ce9ecf` 的 analytical congestion-aware 后端，装在 yss3 的 `~/astrasim-test/`。打了 6 处补丁（`results_index/astra/patches/`）：先 Y 后 X 的 `Mesh2D` 拓扑、链路统计、事件队列由有序链表换成 map（原版是平方级）、点对点 send 不限在途数、SEND 延迟属性、逐条消息日志。没有改 MGPUSim。
- **校准**：384 GB/s 下零负载单程延迟 1–12 跳和 akita 差 ≤ 0.8 ns（请求）/ ≤ 2.4 ns（响应）。流量用 dm 的 pairs ÷ RTT 再 IPF 重建，每链路 flit 和 akita 实测的相关系数 0.94–0.9998。
- **第 1、2 条复现**：384 GB/s、非热点流量，W 64 → 448 时往返时间只涨 1–5%（sssp 384 → 404 ns，akita 401 → 408），吞吐 ≈ W × 64 B ÷ RTT；W = 448 时最忙链路 0.32–0.44、平均 0.21–0.24。
- **第 3 条复现**：PR @48 GB/s，W = 64 / 256 / 448 的往返时间 ASTRA 5.91 / 23.7 / 41.4 µs，akita 5.82 / 23.5 / 41.0 µs；0→7 都是 0.87。npbcg 差 4–10%。同负载开环只有 0.2–0.6 µs，说明 µs 级排队是闭环饱和（47 × W 个在途堆在瓶颈处）造成的，不是网络模型多算的。
- **第 4 条复现并修正**：瓶颈是 **tile 0 的注入口**（0.97–1.0），0→7 只分走 87%，所以停在 0.87。换路由提高不了热点 owner 的上限。
- **唯一的不一致**：384 GB/s、W = 64 的 PR / npbcg，akita 往返时间是 ASTRA 的 2.1 倍。差的正好是 akita 的 owner 排队（532 / 372 ns），也就是主配置下这两个 workload 卡在 owner 端的处理能力，不在网络。
- **选项 A（机制的网络层评估）**：用 akita Complete 的流量（M3 / 合并不进网络、实测打包直方图）闭环，"逻辑远程读吞吐"的提升：PR 20–23×、npbcg 6.8–16×、sssp 2.0–2.7×、cfd 1.2–2.0×、ar 1.05–1.9×、bfs 1.4–1.6×。36 组里 35 组 ≥ akita 端到端加速比，Spearman 0.88。384 GB/s 下非热点 workload 的往返时间不变，收益 ≈ 每个包承载的逻辑读数：在 W × RTT 的上限下，机制让同一个窗口位置做更多事，而不是降低延迟。多播没有建（保守，≤ 7%）。
- **LLM / DLRM 算子 baseline 对 CuPath**（用户 10-21 要求"Llama-7B 上 baseline 和 CuPath 的对比"；ASTRA 的 Llama 模型是 TP/PP 显式通信，CuPath 作用不到，所以改用已有的 LLM 算子）：ASTRA 网络层提升 @384 W64 对 akita 端到端：paged attention 1.79 / 1.58×，MoE 2.08 / 2.13×，矩阵乘 13.4 / 2.67×（akita 的窗口瓶颈解除后受计算限制），embedding bag 1.66 / 1.01×（akita 的瓶颈不在远程窗口）。48 GB/s 下提升缩小到 1.1–1.3×（链路饱和，只剩字节数减少）。
- **LLM 试验**：Llama-7B 推理，TP 8 × PP 6，每个配置 5–9 s。相邻放置 148 / 217 ms（384 / 48 GB/s），随机放置 162 / 293 ms。只作趋势参考。
- **速度**：ASTRA 约 4,000–5,400 次远程读/秒（akita 全 GPU 约 1,300 次/秒），大运行每 100 万次读约 5 GB 内存。
- **akita 当前配置的平台校准**（10-21 晚，用户同意；二进制 `4235991`，T14 参数不变，只加 `-bandwidth=24/3 -switch-buffer-flits=16`，16 个运行，yss3 `~/astrasim-test/mgpusim_hop/`）：零负载往返每跳 72 ns，48 GB/s 比 384 GB/s 在 12 跳时只多 5 ns（ASTRA 存储转发多 28.6 ns，即约 3% 的偏差）。incast：48 GB/s 下 akita 和 ASTRA 都停在 32 B/ns（注入口上限，差 0.3%）；384 / 768 GB/s 下 akita 的单个 owner 最多 113 B/ns（1.77 line/ns），ASTRA 网络能到 224，所以主配置下热点 owner 的上限是 **owner 的服务能力**。

<!-- 已经跑完的实验，指向 results/ 下的目录，并给出关键数字 -->

### page migration 对比基线（10-21，分支 `isca27/page-migration` @ `c74ef96`，384 GB/s、W = 64；`results_index/pm/`，设计见 `pm/DESIGN.md`）
- 344 个运行，340 个 exit 0，4 个 hang（L2TLB→IOMMU bug：mg pmf2m、mg pmc4kideal、shocsort pmc2m、npbis pmf2mideal）。observe 与 baseline 逐位一致（bfs 除外，自身 ±10% 不确定）。
- 25 个 workload GMEAN：counter 4K 1.14、fault 4K 1.10、fault 4K 理想 1.47、每个 workload 事后挑最好 4K 变体 1.28、2 MB 0.45–0.48；Complete 1.73。
- 迁移胜过 Complete：fft2d、FW、I2C（非理想即胜）；理想时加上 CFD、MoE、PA、npbis。热点 owner 类远不如 Complete（PR 2.89 vs 13.3）。
- fault 策略对软件延迟决定性（20 µs 时 9 个 workload GMEAN 0.62）；counter 异步不敏感，阈值 64 更好（GMEAN 1.32）。
- **Complete + counter 4K：GMEAN 1.83（相对 Complete 1.06；FW 1.63、fft2d 1.61、CFD 1.38）**；Complete + fault 4K 0.81；每个 workload 取较好者 1.93。

### data movement 离线分析（10-21；`results_index/dm/`；只用已有统计，没改代码、没跑新模拟）
- 热点 owner 全在角落（tile 0、tile 48：小 buffer 放 GPM0、余数页放最后一个 GPM）；角落 2 条出链路 + 先 Y 后 X 路由，PR 87% 出流量走 0→7 一条链路。均匀流量压在中部 56 条链路（41–43% 字节）。
- Complete 省下的字节×跳数主要来自本地命中（M3 + 预取）；打包按字节只省 3–15%（作用是省窗口位）；M2 ≈ 0–2%。
- M3 之后剩余网络 line 里仍有重复取：均值 0.31（KM 0.93、npbcg 0.82、mg 0.62、cfd 0.60）。主因是 kernel 边界整体作废（npbcg 141 万填入 135 万被作废），L2 70–95% 空闲。
- 排序：①M3 跨 kernel 保留只读 line（上限：网络 line 0.32、mesh 0.22）②主动推送（sssp/ivf，需预测器）③热点小数据放中间/复制 ④压缩（384 GB/s 下几乎无上限）⑤owner 自适应等待（W=64 下净负）。压缩需要新加只读统计（提案在 dm/analysis.md）。

### M2 / 分配方式的敏感性：W 和带宽（10-21，二进制 `4235991`；`results_index/m2w/`）
- 6 个 workload（PR、npbcg、sssp、bfs、allreduce、CFD）× 4 个配置（baseline、M1+M3、Complete、Complete 分配 shared）；条件 W=64@384（复用 abl3c384）、W=256@384、W=2048@384、W=64@48（`-bandwidth=3`）。W=256@48 用户中途取消（结果留在服务器，不进分析）。72 个新运行全部 exit 0，无挂住。
- **M2 增益**（M1+M3 / Complete 的 GMEAN）：0.984 / 1.008 / 1.004 / 1.007。最大 PR @W=2048 +2.7%（热点 owner 出口链路饱和处）。sssp/bfs 的负值来自单个最慢 GPM 的关键路径波动和翻译延迟尖峰，不是数据通路变慢。
- **分配方式**（shared / rtt）：0.989 / 1.000 / 0.999 / 1.006，W=2048 时窗口从不满。
- **窗口上限**：每 GPM 在途读被 L1V MSHR 封顶在 28×16 = 448，W=2048 等于窗口不限；384 GB/s 最多用到对分带宽的 20–32%，填不满。
- **对主结论的影响**：W=256 时 baseline 自己快 1.25–3.4×，这 6 个 workload 的 Complete GMEAN 从 2.68 降到 1.91（ar 1.86→1.08，CFD 1.88→1.33）；W=2048 时 ar 1.02、CFD 1.06、sssp 1.42。W=64@48 时 Complete 2.75（PR 17.0×）。v3 收益依赖"W=64 是现实窗口"的前提，论文要论证或把 W 作为敏感性维度。
- **追加 W=2048@48（"网络紧张"，≈220 CU @384 GB/s 等效；用户 10-21 要求，主会话直接入队，24 个运行全部 exit 0，服务器 `~/akita-exp-test/v3m2w/` tag `w2048b48`，尚未并入 m2w.csv）**：Complete 对 baseline PR 17.57、npbcg 8.34、sssp 1.63、CFD 1.18、bfs 1.10、ar 1.05，GMEAN 2.63（不含 PR 1.80）。对比宽网络 W 从 64→2048 时 GMEAN 2.67→1.77，窄网络 W 放大几乎不削弱收益（2.75→2.63）。M2 增益 0.994–1.042，分配方式除 bfs 1.037 外均 1.000。
- 推测（未验证）：L2TLB→IOMMU 丢唤醒 bug 除了挂死，还会造成翻译延迟尖峰（bfs k10 远程 TLB 缺失 667→4840 ns），可能是图类 workload ±5% 波动的来源。

### v3 @ 384 GB/s：baseline / Complete / 消融（10-21，二进制 `4235991`，修复 Bug A/B，组合表 128 项；`results_index/abl3c384/`）
- 网络 `-bandwidth=24 -switch-buffer-flits=16`；25 个 workload × 10 个配置 = 250 个运行：249 个 exit 0，WG 数和指令数与 baseline 逐对一致；**MG 的 M1+M2 挂住**（L2TLB 直接用 IOMMUCache 的端口发送，丢唤醒；原有 bug，没修没杀，PID 2415098 @ yss2，见 README 第 3 节）。
- 以前崩溃的 5 个规模（sssp s19、atax 12096、ivf 2M、graphtc s14、shocsort 6.19M）全部 exit 0；fir、MM（768 GB/s）修复前后逐位一致。
- **Complete 对 baseline GMEAN 1.73×（25 个）**。M1 / M1+M2 / M1+M3 / Complete = 1.44 / 1.43 / 1.73 / 1.73；只开 M3 1.28。
- 从 Complete 去掉一个部件（T_without / T_Complete）：M1 1.358、M2 **0.998**、M3 1.213；合并 1.111、打包 **1.222**、分配 shared 0.998、分配 near 1.056。
- 每个 workload 贡献最大的部件：打包 11、M3 7、合并 3（MM、FW、shocsort）；embbag、gups、MT、SPMV 所有部件都 < 2%。M2 和分配方式在任何 workload 上都不是最大贡献。
- 统计：baseline 链路平均利用率 1–5%，远程延迟大头是请求方等 64 个窗口位；owner 合并率 0–9%（请求方合并 0–86%）；多播平均 2.0–2.6 个目的地；M2 在 sssp（−5%）、bfs（−3%）上是负的。
- 最大加速（PR 13.3×、npbcg 4.4×、graphtc 3.7×、atax 2.7×、MM 2.7×）都来自 < 192 KB 的 buffer 整个放在 GPM 0 造成的热点 owner，论文里要交代。
- 改进提案（未实现，`mechanism_analysis.md`）：P1 按页的请求方表；P2 窗口满时按批 line 数排序发送；P3 M3 按复用率决定是否填；P4 分配改共享池，按距离分配作为负面结果；P5 M2 只在 48 GB/s 下重新评估；P6 热点 owner 是放置问题，单独交代。

### 新 Complete（M1 + M2 + M3）在 16 个新 benchmark 上（10-20，二进制 `d09b18b`，mi200，中小规模；`results_index/w2complete/`）

新 Complete = T18 全开（W=1024 + 按距离分 credit）+ 旧 m2 + 旧 m3（requester L2），去重归 M1。baseline 和新 Complete 每对的指令数、WG 数完全相同。

| benchmark | 规模 | 数据量 | baseline | 新 Complete | 加速比 |
|---|---|---|---|---|---|
| altis_cfd | 19.4 万单元 | 26 MB | 1002 µs | 152 µs | 6.60× |
| npbcg | class W | 7 MB | 5897 µs | 936 µs | 6.30× |
| gups | 2¹⁸ 表 × 8 | 5 MB | 683 µs | 128 µs | 5.33×（M1 合并只有 1.7%、M2 打包 1.0，收益来源待拆分） |
| allreduce | 每 CU 2 份 | 267 MB | 1008 µs | 236 µs | 4.28× |
| sssp | scale 14 | 4 MB | 2777 µs | 674 µs | 4.12× |
| w2-npbis | 2²⁰ key，1 次迭代 | 18 MB | 1144 µs | 299 µs | 3.83× |
| npb_mg | class W | 29 MB | 2193 µs | 606 µs | 3.62× |
| ivfflat | 16K 向量 | 9 MB | 778 µs | 263 µs | 2.95× |
| w2-atax | 4096² | 68 MB | 831 µs | 288 µs | 2.88× |
| w2-fft2d | 1024² | 17 MB | 840 µs | 360 µs | 2.33× |
| bfs | scale 16 | 14 MB | 1073 µs | 606 µs | 1.77× |
| w2-shocsort | 512K | 9 MB | 760 µs | 470 µs | 1.62× |
| rodinia_lud | N=1024 | 4 MB | 4148 µs | 2737 µs | 1.52× |
| lonestar_bh | 8.6 万物体 | 5 MB | 435 µs | 361 µs | 1.21× |
| embbag | batch 1024 | 70 MB | 989 µs | 973 µs | 1.02× |
| graphtc | scale 13 | 2 MB | **三个配置都崩溃** | | — |

- **GMEAN（15 个有效）2.82×**；去掉 embbag 的 14 个 3.04×。
- **graphtc 崩溃**：`runDSREAD2B32` 越界（`tc_count` 的 1040 字节 LDS 归约数组被多读 4 字节），三个配置都在约 1030 s 崩溃，移植时的小规模也一样。需要修 kernel 或 emulator。**更正**：我 10-20 下午汇报的 TC 3.11× / HPCA 2.95× 读的是崩溃运行写出的 summary，无效。
- **npbis 2 次迭代**：baseline 崩溃（LDS 负地址），新 Complete 没崩；这一对不可靠，表里用 1 次迭代。**更正**：之前汇报的 npbis 3.00× 同样无效。以后判断运行是否完成要看退出码，不能只看有没有 summary.csv。
- 数据量都小（2–267 MB，大多小于全 wafer L2 的 96 MB），加速比可能偏高。
- HPCA 版 Complete（用户 10-20 放弃）：已完成的 11 个 GMEAN 1.88×，同样 11 个上新 Complete 2.98×；结果保留，标为部分完成。

### W2：16 个通信密集型新 benchmark 的移植（10-20，来源 sarchlab/gpu_benchmarks；分支 `isca27/wport-a..d`，合并进 `isca27/w2-complete` @ `d09b18b`）

全部移植完成，`mgpusim/benchmarks/w2/<名字>/`（OpenCL kernel + hsaco + Go host + 单元测试）；不用 atomic、不用 scratch、fp32、合成输入（R-MAT 图、NAS makea、幂律下标等）；emu 小输入和 CPU 参考一致；4 个分支各自的回归（fir、MM `-max-wg 4096`）和 `ca8b831` 逐位一致；没有改模拟器。

| benchmark | 类别 | 小输入 | mi200 | 远程比例 / 远程延迟占比 | 用到的 GPM | T18（W=1024 + 距离） | 建议的 3–5 小时规模 |
|---|---|---|---|---|---|---|---|
| graphtc | 图 | R-MAT scale 13 | 69.9 µs | 0.958 / 0.999 | 48 | 2.80× | scale 15–16 |
| bfs | 图 | scale 14 | 443 µs | 0.919 / 0.998 | 37 | 1.65× | scale 19 |
| sssp | 图 | scale 13 | 1479 µs | 0.979 / 1.000 | 37 | 2.19× | scale 16 |
| npbcg | 稀疏求解 | class S | 981 µs | 0.976 / 0.999 | 13 | 1.73× | class A，1 个外迭代 |
| embbag | 推荐 | 默认 | 141 µs | 0.741 / 0.972 | 30 | 1.06× | 15 万行，batch 4096 |
| ivfflat | 向量检索 | 默认 | 778 µs | 0.955 / 0.999 | 44 | 2.46× | 1M 向量，nlist 4096，nprobe 16 |
| gups | 随机读改写 | 2 MB 表 | 361 µs | 0.963 / 0.999 | 48 | 4.72× | 2¹⁸ 表 × 48 次更新 |
| allreduce | 集合通信 | 默认 | 467 µs | 0.662 / 0.989 | 48 | 3.16× | `-ar-per-cu 8` |
| w2-shocsort | 排序 | 64K | 679 µs | 0.960 / 0.999 | 48 | 2.34× | 3.67M |
| w2-npbis | 直方图/排序 | class S | 352 µs | 0.874 / 0.997 | 48 | 2.10× | class A，1 次迭代 |
| w2-fft2d | FFT/转置 | 512² | 448 µs | 0.918 / 0.998 | 48 | 3.05× | 2048² |
| w2-atax | BLAS2 | 2048² | 422 µs | 0.711 / 0.989 | 48 | 2.34× | 11264² |
| lonestar_bh | N-body/树 | 16K 物体 | 492 µs | 0.928 / 0.990 | 10 | 1.02× | 524K 物体 |
| altis_cfd | 非结构网格 | 97K 单元 | 780 µs | 0.894 / 0.997 | 14 | 5.3× | 140 万单元 |
| rodinia_lud | LU | N=512 | 1547 µs | 0.966 / 0.999 | 35 | 1.52× | N=1664 |
| npb_mg | 多重网格 | class S | 231 µs | 0.895 / 0.997 | 37 | 1.25× | class W，3 个 V-cycle |

- 小输入下有几个只用到 10–37 个 GPM（WG 太少）；建议规模下都能铺满。lonestar_bh 的 1.02× 是 16K、只用 10 个 GPM 的结果，不代表正式规模。
- **地址转换的问题（重要）**：每 GPM 的 L2 TLB 只覆盖 2 MB；GUPS 表超过 2 MB 时被 TLB 卡住（8 MB 表跑了 2.5 小时没结束，已终止），embbag、ivfflat 的正式规模也远超 TLB 覆盖。论文前提是地址转换已由 HDPAT 解决，这类大表 workload 正式评估时可能需要理想地址转换或 HDPAT，否则测的是 TLB 而不是数据访问。等用户决定。
- **发现的 emulator bug（都在 kernel 侧绕开，没改模拟器）**：`v_cndmask_b32_e64` 的取反按整数取反；`v_lshlrev_b64` 寄存器移位量读错；`v_cmp_*_u32` 遇到负的内联常数时符号扩展错误（`check_hsaco.sh` 已加检查）；没有实现 `v_bfe_u32`、`s_cmp_le_u32`、`s_cmpk_{lt,gt}_u32`、`s_or_b32`（某些形式）、`ds_*2st64`、`ds_write_b64`、`v_sin/v_cos`。现有结果是否踩到这些 bug 需要查（旧 benchmark 和 T17 kernel 已经过 T17 的检查脚本）。
- 新 Complete 在这 16 个上的运行（baseline / 新 Complete / HPCA Complete，中小规模）正在跑，`~/akita-exp-test/w2c/`。

### T19 冒烟测试（10-20，二进制 `539261b`/`eaa9c73`，`~/akita-exp-test/t19/`）

设计要点：每个 GPM 的静态区间变成队列，自己从头取，偷取方从尾部一次切 k 个；偷取请求和回复是真实的 mesh 消息（每个 CP 一个专用端口，16 B / 32 B）；剩余 WG 数每 200 ns 告诉 2 跳内的邻居（远处每 2 µs，按解析模型计算陈旧度）；挑最近、剩余足够多的 GPM，近处没有就去远处，包括静态切分里没分到 WG 的 GPM；filter 指导用每 GPM 最近访问页的 cuckoo filter 和每组 WG 的历史足迹（只记可偷的尾部）。每个 WG 只执行一次由位图检查（违反就 panic）。

| 输入 | baseline | 只按距离偷 | 距离 + filter | 成功 / NACK，偷走的 WG | 最慢÷平均（前 → 后） |
|---|---|---|---|---|---|
| im2col 小输入（64 WG/CU） | 193.8 µs | 149.9（**1.29×**） | 150.5（1.29×） | 1495 / 255，11,960 | 1.69 → 1.30 |
| FW 2048 节点 × 4 轮 | 548.8 | 460.5（**1.19×**） | 462.7（1.19×） | 945 / 965，7,560 | 2.62 → 2.14 |
| R-GAT 32 种子 | 9689 | 6773（**1.43×**） | 6773 | 55 / 1784，440 | 1.22 → 1.25（用到的 GPM 增加） |
| MoE 64 token | 5092 | 4738（1.075×） | 4741 | 15 / 993，120 | 1.55 → 1.71 |
| PagedAttention batch 16 | 930.8 | 903.3（1.03×） | — | 9 / 543，72 | 2.16 → 2.17 |
| kmeans、pagerank、spmv 小输入 | — | 1.00× | — | 几乎没有可偷的 | 不变 |
| R-GAT 32 种子，均衡初始切分（C′） | — | 3960（**2.45×**），0 次窃取 | — | — | — |
| MoE 64 token，均衡初始切分（C′） | — | 1887（**2.70×**），0 次窃取 | — | — | — |

- 平均窃取延迟 120–220 ns（1.1–2.3 跳）；filter 重叠估计 0.781（精确 0.779），误判率很低；MoE 16 token 的 `-verify`（9 次窃取，72 个 WG）通过；墙钟和 baseline 相差 ±5%，内存不变。
- 回归（开关关闭）：fir、MM（`-max-wg 4096`）在 `0b85bef` 和 T19 的 3 个提交上逐位一致（26.568 µs / md5 `d56a6814`、345.433 µs / md5 `22a58b27`）。

### T18 冒烟测试（10-20，二进制 `7c40937`/`ca8b831`，`~/akita-exp-test/t18/`）

设计要点（细节见 `isca27/t18-merge` 的提交）：请求方用 cuckoo 表（12 位指纹 + 完整地址，命中必须核对完整地址）做唯一的在途表，只合并 64 B 整行读，写会关闭该行的表项；`-t18-window` W 限制网络在途数，`-t18-dist-credits` 按 84 + 71.4 ns × 跳数分 credit；owner 端在该行 L2 读完成前合并，返回时在 switch 里按 YX 路由树复制（真正的网内多播）；表满时退回原来的单播路径；旧 m2 和 T18 不能同时打开。

| workload | baseline | 旧 m2 | T18 全开 W=64 | T18 全开 + 大窗口 | 合并率（请求方 / owner） |
|---|---|---|---|---|---|
| floydwarshall（`-max-wg 32768`） | 88.31 µs | 35.25 µs（2.51×） | 41.51 µs（2.13×） | **10.32 µs（8.56×）**（W=1024 + 距离 credit，W=4096 相同） | 44.6% / 39.5%，平均扇出 3.3 |
| R-GAT 64 种子（完整） | 10.99 ms | — | 8.10 ms（1.36×） | **4.42 ms（2.49×）**（W=1024 + 距离 credit） | 34–39% / 0.4–1.6% |
| PagedAttention 默认 batch | 391.7 µs | — | 387.5 µs | **117.4 µs（3.34×）**（W=1024、4096 相同） | 2.1% / 0.4% |
| MoE 64 token | 跑中 | — | — | 1.265 ms（W=1024 + 距离 credit） | 5.7% / ≈0 |

- FW 的对照：只放大窗口到 1024、不合并 39.12 µs（2.26×，接近 T15 的 O2b 2.46×）；只请求方 48.54 µs；只 owner 端 87.81 µs。**合并和放大窗口要一起才有效**：不合并时 owner 端 incast 让往返时间停在 3–4 µs，owner 合并 + 多播消掉 incast 之后，大窗口才用得上。FW 的 8.56× 约是 O1（11.1×）的 77%。
- 注意：FW 是截断运行，各配置完成的指令数可能不同（T15 的有效性问题），正式评估要按 T15 的规则核对。
- 正确性：单轮 FW（256 节点，`-verify -magic-copy-coherent`）在 baseline、T18 全开、全开 + W=256 + 距离 credit、只 owner 端、关闭多播、极小的表（强制退回）、2 位指纹（3 万次指纹误匹配，合并数和 12 位相同）下都 “Passed!”；R-GAT 16 种子 `-verify` 4 个配置都 0 个不一致。
- T18 运行的墙钟是 baseline 的 0.5–1.0 倍，内存 2.4–11.7 GB。
- 回归（全部开关关闭）：fir 26.568 µs / md5 `d56a6814`、MM 345.433 µs / md5 `22a58b27`、旧 m2 的 FW 35.248 µs / md5 `f00f967e`（也和 10-15 ablation 相同），`0b85bef`、`7c40937`、`ca8b831` 三者逐位一致。

### P12 测量（T18/T19）（10-20，mi200，二进制 `ed1aca0`，全部跑完；`results_index/p12/`）

**新统计**（`isca27/p12-measure`，默认关，只挂被动的 tracing hook）：`-report-wg-footprint`（每个 WG 的向量 load 读过的 4 KB 页，CU 发出时记录，每 WG 最多 4096 页，这次都没超）；`-report-m1-sharing`（L1V 发往 RDMA 的远程读：1 µs / 10 µs 窗口内同一 line 被几个 GPM 读、两两跳数、到 owner 的跳数、YX 多播树大小；在途重复请求：请求方 L1V 发出到响应、owner 端 RDMA 转给 L2 到响应）。新 benchmark 参数：`-pa-steps`（连续 decode，默认 1）、`-t17-print-groups`（打印 PA 的工作表和 MoE 的 tile，WG → 序列 / expert）、`-t16-pagerank-iters`、`floydwarshall-p12small`。

**回归**（yss2，mi200 baseline 参数，`-max-wg 4096`）：fir 改动前 `c9cdf64` / tracer 关 / tracer 开（`1929100`、`ed1aca0` 各一次）total_time 都是 26.568 µs，164,567 行排序后 md5 `d56a681494ae` 全部相同；MM 都是 345.433 µs，169,867 行 md5 `22a58b272d89` 全部相同（和 T16 的数一致）。PagedAttention 默认小输入（batch 2）：`c9cdf64`、`ed1aca0`、`ed1aca0 -pa-steps=1 -t17-print-groups` 都是 391.692 µs，142,320 行 md5 `616da3dcdbaf`。tracer 不影响墙钟（fir 11.5–12.2 分钟，MM 37.5–39.2 分钟）。另外 MoE 在 T17 规模下 `95615c4`、`c9cdf64`、`ed1aca0`（不开 P12 tracer）和开 P12 tracer 的运行都是 5.979603 ms。

**运行**（规模、主机、墙钟见 `results_index/p12/runs.csv`）：PagedAttention batch 33 × 3 步（yss2，5.2 小时）和 batch 66 × 2 步（T17 规模，yss3，7.3 小时）；MoE skew 1.75、R-GAT 320 种子（T17 规模，yss2，3.1、3.2 小时）；pagerank 小输入 3 轮（yss3，4.2 小时）；kmeans 小输入（yss3，1.9 小时）；floydwarshall 2048 节点 × 8 个 pass（yss3，1.0 小时）。峰值内存 4–7.3 GB。

**T19 亲和性**（页集合的 Jaccard 均值；containment = B 读的页里 A 也读过的比例）：

| workload / kernel | 相邻 WG | 同组 | 随机两个 WG（基线） |
|---|---|---|---|
| MoE moe_up | 0.92 | 同 expert 0.9998；同 expert 同列块 0.97 | 0.019 |
| MoE moe_down | 0.94 | 同 expert 0.9998；同 expert 同列块 0.97 | 0.016 |
| R-GAT rgat_agg（相邻目标节点） | 0.41（containment 0.56） | — | 0.25 |
| R-GAT rgat_proj | 0.84 | — | 0.67（所有 WG 读同一个 W） |
| PagedAttention pa_partial（对照） | 0.016 | 同序列 0.016；同序列同 KV 头 0.031 | 0.0004 |
| kmeans compute | 0.94 | — | 0.003 |
| floydwarshall | 0.99 | — | 0.024 |
| pagerank | 0.77 | — | 0.69 |

- MoE 同一个 expert 的 WG 在页这一级几乎读同一组页（每个 WG 读约 1,430 / 720 页，即该 expert 的整个权重矩阵：64 列的列块横跨所有行），不同 expert 几乎不重叠；PagedAttention 几乎没有亲和性（同序列的 WG 只共享块表、Q 所在的 1 页），和预期一致。
- R-GAT 聚合的相邻目标节点有一些重叠（0.41 对随机 0.25）。
- pagerank 在页这一级没有区分度：rank 向量只有 63 页，每个 WG 读约 58 页，随机两个 WG 也有 0.69。页粒度太粗，要看 line 级（没做）。

**T19 历史稳定性**（同一个 kernel 前后两次启动，同一个 WG 的足迹 Jaccard）：

| workload | 按 WG 编号配对 | 说明 |
|---|---|---|
| kmeans（2 轮） | 1.000 | 每轮读同样的点 |
| pagerank（3 轮） | 相邻两轮 0.029；隔一轮 1.000 | 两个 rank buffer 轮流读写（ping-pong） |
| floydwarshall（8 个 pass） | 0.73 | 第 k 行、第 k 列每个 pass 都变 |
| PagedAttention b33：step 0→1 | 0.999 | 这一步没有序列跨过 64 token 的分段边界 |
| PagedAttention b33：step 1→2 | **0.085**；按（序列、分段、头）配对 **0.9995** | 一个序列跨过分段边界，工作表后面的项整体后移，94% 的 WG 换了工作项（这些 WG 0.03）；同一工作项 5.3% 换到了别的 GPM |
| PagedAttention b66：step 0→1 | **0.81**；按工作项 0.999 | 19% 的 WG 换了工作项 |
| MoE、R-GAT | — | 一个 batch 里同一个 kernel 不重复执行（R-GAT 的 15 次 proj/agg 是不同层、不同关系） |

**T18 M1 共享**（1 µs 窗口 / 10 µs 窗口；远程读 = L1V 发往 RDMA 的读）：

| workload | 远程读数 | 所在 line 被 ≥2 个 GPM 读的比例 | owner 每窗口每 line 只服务一次能省 | 读同一 line 的 GPM 两两跳数（最近一对） | 共享 line 上多播省掉的链路 / 占全部响应跳数 |
|---|---|---|---|---|---|
| PagedAttention b66 | 3,043 万 | 0.15% / 0.33% | 0.4% / 0.8% | 2.6（2.3） | 31% / 0.02% |
| PagedAttention b33 | 2,260 万 | 0.46% / 0.81% | 0.6% / 1.1% | 1.8（1.7） | 47% / 0.1% |
| MoE skew 1.75 | 1,549 万 | 3.0% / 5.7% | 10.7% / 19.3% | 3.0（2.6） | 36% / 0.5% |
| R-GAT | 1,281 万 | 34% / 56% | 30% / 63% | 3.4（2.3） | 40% / 12% |
| floydwarshall | 211 万 | 23% / 24% | 54% / 71% | 4.5（1.5） | 52% / 29% |
| pagerank | 2,201 万 | 78% / 98% | 53% / 92% | 4.6（3.4） | 25% / 19% |
| kmeans | 1,078 万 | 0 | 0 | — | — |

**在途重复**（基线时序；请求方在途 = L1V 发出到收到响应，包含 RDMA 窗口前的排队）：

| workload | 本 GPM 已有同 line 在途（别的 CU） | 只有别的 GPM 在途 | 别的 GPM 最近几跳（≤2 跳的比例） | owner 端同 line 在途 | 其中来自别的请求方 |
|---|---|---|---|---|---|
| PagedAttention b66 / b33 | 0.58% / 0.87% | 0.05% / 0.13% | 1.9（85%） | 0.10% / 0.17% | 0.01% / 0.05% |
| MoE | 15.0% | 0.6% | 2.6（61%） | 2.5% | 0.8% |
| R-GAT | 34.4% | 23.6% | 2.1（74%） | 8.8% | 6.2% |
| floydwarshall | 49.2% | 11.8% | 1.5（90%） | 19.4% | 7.4% |
| pagerank | 17.1% | 78.5% | 2.2（70%） | 6.9% | 6.4% |
| kmeans | 0 | 0 | — | 0.05% | 0.02% |

全 wafer 同时在途的远程读最多 1.5–2.1 万个（每 GPM 约 300–450，和 T17 的闭环模型一致）。

**结论（T18）**：
- **合并的空间取决于 workload**：PagedAttention（T17 的主力）和 kmeans 几乎没有（在途重复 < 1%）；MoE 中等（15%，几乎全是同一 GPM 的不同 CU）；R-GAT、floydwarshall、pagerank 很大（58–96% 的远程读发出时同一 line 已经在途）。
- **请求方合并**（同一 GPM 的不同 CU，T18 设计 A）能直接去掉 MoE 15%、R-GAT 34%、FW 49%、pagerank 17% 的远程读，PagedAttention < 1%。
- **跨 GPM 的重复在请求方排队时很多，但到 owner 端时大多已经错开**：pagerank 有 78.5% 的远程读发出时别的 GPM 有同 line 在途，但 owner 端同时在途的只有 6.4%（R-GAT 23.6% → 6.2%，FW 11.8% → 7.4%）。按现在的设计（owner 端只合并“正在服务”的请求），owner 端合并 + 多播在基线时序下只能拿到 6–8%；放大窗口后（O2b）请求到达 owner 更集中，可能变多，需要在 T18 的实现里再量。
- 读同一 line 的 GPM 彼此不远（最近一对 1.5–2.6 跳，60–90% 在 2 跳内）；多播在共享 line 上省 25–52% 的链路，但占全部响应流量只有 PA 0.02%、MoE 0.5%、R-GAT 12%、FW 29%、pagerank 19%。
- 所以对 PagedAttention，T18 的收益只能来自窗口（按距离分配 credit），不能来自合并或多播；合并和多播对 R-GAT、FW、pagerank 有意义。T17 的 O2b（第 5 步）还没跑，窗口这一半还没有直接证据。

**结论（T19）**：
- **亲和性存在，但集中在“同一组”**：MoE 同一个 expert 的 WG 页集合几乎相同（0.9998，随机 0.02），FW、kmeans 相邻 WG 0.94–0.99，R-GAT 聚合相邻目标节点中等（0.41 对 0.25），PagedAttention 没有（对照，0.016）。不需要历史，按编号相邻 / 同组挑就能拿到这些亲和性。
- **按 WG 编号记录的历史不稳定**：数据不变时完全稳定（kmeans 1.0，PA 不跨边界时 0.999，pagerank 隔一轮 1.0），但 PagedAttention 只要一个序列跨过分段边界，后面的 WG 编号全部错位（step 1→2 只剩 0.085，b66 0.81），pagerank 的 ping-pong 让相邻两轮几乎不重叠（0.03）。按工作项（数据）对齐时是 0.999。也就是说 filter 里的历史应该按数据地址（页 / 区域）而不是 WG 编号记；或者只用“相邻 / 同组”这个不需要历史的规则。
- 局限：页粒度对 pagerank 太粗；MoE 同 expert 在 line 级（不同列块读不同的列）的重叠会低于页级。


### T17 第一轮：新 workload 的 baseline（10-17，mi200，二进制 `95615c4`，全部跑完；`results_index/t17/`）

运行：yss2，`akkalat/isca27_run.sh` + `configs/mi200.json`（无配置差异，保留 `-magic-memory-copy`）+ `-report-gpm-trace -report-l2-source`。4 个全部 rc=0。

| 运行 | 模拟时间 | 墙钟 | kernel 数 | L1V 读远程比例 | L1V 读远程延迟占比 | L2 缺失远程比例（字节） | L2 缺失远程延迟占比 | 远程 / 本地平均延迟 | 平均跳数 | owner：前 1 / 前 3 / 最大÷平均 | 时间加权尾部比 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| PagedAttention（batch 66） | 4.035 ms | 3.75 h | 2 | 96.8% | 99.9% | 55.2% | 98.0% | 2871 / 73 ns | 4.28 | 3.8% / 10.7% / 1.83 | 1.75 |
| MoE skew 1.75 | 5.980 ms | 3.29 h | 5 | 95.5% | 99.9% | 63.5% | 98.2% | 2164 / 70 ns | 3.69 | 3.7% / 9.9% / 1.79 | 1.64 |
| MoE skew 0（对照） | 5.895 ms | 3.17 h | 5 | 96.6% | 99.9% | 62.8% | 98.1% | 2168 / 70 ns | 3.60 | 3.9% / 10.0% / 1.86 | 1.62 |
| R-GAT（320 个种子） | 15.153 ms | 3.30 h | 30 | 97.4% | 99.95% | 86.1% | 99.5% | 2433 / 72 ns | 5.47 | 16.0% / 24.4% / 7.68 | 1.21 |

- **远程**：L1V 读 95–97% 是远程地址，远程访问占了 99.9% 的读延迟；按 L2 缺失算，远程是 55–86%（字节），延迟占 98–99.5%。远程平均延迟是本地的 30–40 倍。
- **owner 集中度**：PagedAttention 和 MoE 的数据均匀分布在 48 个 GPM 上（前 3 个 owner 约 10%），没有热点 owner；R-GAT 有一个 owner 占 16%（最大÷平均 7.7）。
- **MoE 的 skew**：1.75 只比 0 慢 1.4%；skew 主要改变每个 expert kernel 的 WG 数（726 → 781、1056 → 1136）和哪几个 GPM 收尾，对总时间影响很小。
- **每个 GPM 的结束时间**（热力图 `results_index/t17/<运行>-k<kernel>.png` / `.txt`）：

| 运行 / kernel | 占总时间 | 有工作的 GPM | 最大÷平均 | 最大÷最小 | ρ（对平均跳数） |
|---|---|---|---|---|---|
| PagedAttention k0（partial） | 95% | 48 | 1.715 | 5.78 | +0.29 |
| PagedAttention k1（reduce） | 4% | 38 | 2.604 | 12.9 | −0.05 |
| MoE 1.75 k0（router） | 19% | 10 | 1.245 | 4.19 | +0.65 |
| MoE 1.75 k2（up/gate） | 52% | 28 | 1.711 | 5.34 | −0.05 |
| MoE 1.75 k3（down） | 26% | 41 | 1.814 | 6.99 | −0.20 |
| MoE 0 k2 / k3 | 48% / 30% | 26 / 38 | 1.655 / 1.827 | 4.99 / 7.81 | −0.31 / +0.10 |
| R-GAT k8（最长的之一） | 11% | 23 | 1.255 | 2.48 | +0.37 |
| R-GAT k9（聚合，48 个 GPM） | 0.6% | 48 | 1.270 | 1.76 | +0.18 |

- **R-GAT 的 GPM 利用率**：85% 的时间在只用 ≤13 个 GPM 的 kernel 上（见“需要用户决定”）。
- **峰值内存**：正式运行没有记录（`isca27_run.sh` 不包 `/usr/bin/time`）；规模确认时按试跑外推的是 W-A 约 28 GB、W-B 12–20 GB、W-C 8–12 GB，运行期间 yss2 没有内存告警。以后的 T17 运行会加上峰值内存记录。
- **墙钟**：3.2–3.75 小时，都在 3–5 小时的预算内。

### T16 阶段 1：位置导致的负载不均（10-17，mi200，二进制 `2e09bc9`，全部跑完整个 kernel；`results_index/t16/`）

每个 GPM 的忙碌时间 = 它的 kernel 结束时间 − 它的 kernel 启动时间（platbench 每个 GPM 单独启动，所以减去各自的启动时刻）。多 kernel 的取最长的那个。“加权跳数”的 ρ 需要按实际远程访问对象加权，tracer 只有每个 GPM 的远程总数，没有算（标“—”）。

| workload | 跑完 kernel | WG/GPM | 尾部比（最慢/平均） | 最慢/最快 | ρ 平均跳数 | ρ 加权跳数 | ρ 到 IOMMU 距离 | 远程读 | 和位置无关的不均 |
|---|---|---|---|---|---|---|---|---|---|
| all-to-all（每对 16 KB） | 是 | 224 | **1.224** | 1.389 | **0.95** | — | 0.91 | 100% | 无；角落最慢、(2,4) 最快，和 hpca 的 1.23/1.43/0.93 一致 |
| remotefrac f = 0 | 是 | 224 | 1.005 | 1.011 | 0.73 | — | 0.73 | 0% | 幅度可以忽略 |
| f = 0.1 | 是 | 224 | 1.054 | 1.088 | 0.44 | — | 0.39 | 10% | |
| f = 0.25 | 是 | 224 | 1.038 | 1.066 | 0.16 | — | 0.17 | 25% | |
| f = 0.5 | 是 | 224 | 1.039 | 1.067 | 0.09 | — | 0.10 | 50% | |
| f = 1 | 是 | 224 | 1.047 | 1.082 | 0.10 | — | 0.10 | 99% | 梯度沿 tile 编号对角线（左上慢、右下快），像是 GPM 启动顺序造成的，不是距离 |
| **kmeans 小输入** | 是（3 个 kernel） | 112 | **1.248** | 1.505 | **0.99** | — | **0.96** | 98% | 无；以中心为圆心逐圈变慢（中心 0.61 ms、角落 0.91 ms） |
| **pagerank 小输入** | 是 | 1,344 | **1.237** | 1.431 | **0.74** | — | **0.72** | 84% | 无明显 |
| matrixmultiplication 小输入 | 是 | 336 | 1.145 | 2.84 | 0.03 | — | 0.03 | 96% | **有**：最快的是 GPM 1 (0,0)，64 KB 的 B 矩阵在它上面（热点） |
| im2col 小输入 | 是 | 28,560–28,672 | 1.807 | 5.03 | −0.08 | — | −0.08 | 95% | **有**：编号最大的约 12 个 GPM（最后两行）快 3–4 倍，随编号变化，和距离无关（原因待查：WG 工作量或数据放置） |
| matrixtranspose 小输入 | 是 | 65–112 | 1.025 | 1.83 | 0.01 | — | 0.02 | 98% | 最后一个 GPM 只分到 65 个 WG |
| spmv 小输入 | 是 | 7,168 | 1.016 | 1.053 | 0.00 | — | 0.00 | 52% | 无 |
| aes 小输入 | 是 | 1,792 | 1.012 | 1.018 | −0.04 | — | −0.07 | 1% | 本地型对照，均衡 |

**回答 T16 的三个问题**：
- **(i) 位置差是否存在、有多大**：存在，但只在一部分负载上明显。远程访问为主、而且访问对象均匀分布的负载里，角落比中心慢约 1.4–1.5 倍，尾部比约 1.22–1.25（all-to-all、kmeans、pagerank），和平均跳数强相关（ρ 0.74–0.99）。远程比例扫描（每个 GPM 1 MB 分片）里位置效应反而很弱（尾部比 ≤ 1.05），说明在那个规模下瓶颈主要不在距离（可能在 owner 端的服务能力），和 T16 子代理之前用 64 KB 分片做的冒烟测试（1.29、ρ 0.97）不一致，原因还要查。
- **(ii) 和位置无关的不均**：有两个很大的例子。MM 的不均（最慢/最快 2.84）来自 GPM 1 上的 B 矩阵热点；im2col（1.81、5.03）随 GPM 编号变化。另外 matrixtranspose 最后一个 GPM 分到的 WG 少。这些是 baseline 的放置和切分问题，不能算进 W1。
- **(iii) W1 是否成立**：按 T16 的判断标准，微基准一条满足（all-to-all 1.224 ≥ 1.15，和位置强相关）；真实 workload 一条**差一点**：要求至少 3 个尾部比 ≥ 1.1 且 |ρ| > 0.5，现在只有 kmeans、pagerank 两个（MM 和 im2col 的尾部比够，但和位置无关）。**建议**：T17 的新 workload（PagedAttention、MoE、R-GAT 远程读都在 93–97%，数据分布在全部 48 个 GPM 上）跑完后再判断；同时查清 remotefrac 和 im2col 的反常。最终由用户决定。

### P08 整理：10-14 到 10-16 的结果（10-16；文件在 `results_index/<新名字>/`）

**1. 诊断 A：端点窗口**（platbench；hpca 和 mi300x 结果相同处只列一次）

L2 读带宽（B/周期）× L1V 每 CU 在途数（`-l1v-mshr-entries` = `-l1v-max-concurrent-trans`）：

| WG 数 | 8 | 16 | 32 | 64 | 128 | 256 |
|---|---|---|---|---|---|---|
| hpca，16 | 88.9 | 171.0 | 317.2 | 505.2 | 622.5 | 415.9 |
| hpca，32 | 89.5 | 171.1 | 317.0 | 498.1 | 588.2 | 412.6 |
| hpca，64 | 89.5 | 171.9 | 317.9 | 497.1 | 584.2 | 417.7 |
| mi300x，16 / 32 / 64 | 88.9 / 89.1 / 89.1 | 166 / 167 / 167 | 301 / 303 / 303 | 490 / 494 / 494 | 631 / 562 / 588 | 483 / 483 / 477 |

单对远程读带宽（B/周期，1 MB，远端 L2）× RDMA 在途上限（`-rdma-max-outstanding`）；各列是 WG 数，饱和后取最大值：

| 配置 | 1 跳：64 / 128 / 256 | 6 跳：64 / 128 / 256 |
|---|---|---|
| hpca | 26.4 / 50.3 / 92.2（256 在 yss2 崩溃，yss3 重跑） | 7.9 / 15.7 / 30.5 |
| mi300x | 26.4 / 50.5 / 92.6 | 8.0 / 15.7 / 30.5 |
| mi200（64，T14） | 26.4 | 8.0 |

- **结论**：L1V 的在途数（16/32/64）对 L2 带宽没有影响（差别 < 1%，128 WG 处的波动在两个方向都有）→ L1V 的 16 条在途不是 L2 带宽的瓶颈，瓶颈在别处（10-13 的“L2 带宽在 16 CU 处见顶、之后下降”仍未解释）。**远程带宽几乎和 RDMA 在途上限成正比**（64 → 128 → 256 时 ×1.9、×3.5），到 256 还没饱和，6 跳和 1 跳的比例不变（约 1/3.3，等于延迟之比）→ **远程带宽的瓶颈就是 RDMA 的 64 个在途上限**（按 Little 定律：在途数 × 64 B / 往返延迟）。10-13 T14 第 5 条“推算在途数超过 64，所以不是单纯被 64 卡住”的推断不成立（那是按空载延迟推算的）。放大之后“端点受限”的结论仍然成立：受限的是端点的在途窗口，而不是链路（256 时 1 跳也只有链路带宽的 12%）。对 T15：O2b 的 4096 远超已测到的范围（256 仍线性），可以按 T15 原值。

**2. 诊断 B：远程 TLB 缺失和到中心的距离**（1 跳远程读，64 MB，A = TLB 命中，B = TLB 缺失）

| 发起 → 目标（tile） | 离中心（tile 24） | A（ns） | B（ns） | B − A |
|---|---|---|---|---|
| hpca 18 → 11（17 → 10） | 1–2 跳 | 308.8 | 863.9 | +555 |
| hpca 1 → 2（0 → 1） | 6 跳 | 311.7 | 1043.9 | +732 |
| mi300x 18 → 11 | 1–2 跳 | 303.8 | 858.9 | +555 |
| mi300x 1 → 2 | 6 跳 | 306.7 | 1038.9 | +732 |
| mi200（T14，发起方 GPM 1）1 / 3 / 6 / 12 跳 | — | 343.7 / 486.0 / 702.7 / 1131.4 | 1063.4 / 1299.6 / 1461.0 / 1949.5 | +720 / +814 / +758 / +818 |

- **结论**：远程 TLB 缺失的代价**取决于发起方到中心 IOMMU 的距离**，不取决于数据离多远：同样 1 跳，靠近中心 +555 ns，在角上 +732 ns（多约 5 跳，约 36 ns/跳，接近单程每跳 72 ns 的一半——像是往返中只有一段经过 mesh）。mi200 的远程 B − A 随跳数在 720–818 ns 之间波动（hpca 是固定的 732），原因还没查。

**3. 诊断 C：数据放置**（hpca，baseline，16384 WG，spmv 1536；`-report-l2-source`）

| workload | 远程读 | 前 3 个 owner 占远程 | 前 3 个 owner（组件编号） | owner 个数 | workload buffer 个数 | 总 MB | 平均 KB |
|---|---|---|---|---|---|---|---|
| aes | 1.8% | 100% | 0 | 1 | 3 | 1024 | 349,525 |
| bitonicsort | 0% | — | — | 0 | 1 | 1024 | 1,048,576 |
| fastwalshtransform | 0% | — | — | 0 | 1 | 1024 | 1,048,576 |
| fft | 0% | — | — | 0 | 1 | 1024 | 1,048,576 |
| fir | 0.1% | 100% | 0 | 1 | 4 | 1024 | 262,144 |
| floydwarshall | 14.6% | 73% | 0、2、1 | 19 | 2 | 1024 | 524,176 |
| im2col | 57.9% | 11% | 5、6、10 | 38 | 2 | 1029 | 526,711 |
| kmeans | 0% | — | — | 0 | 4 | 390 | 99,840 |
| matrixmultiplication | 99.7% | 100% | 0 | 1 | 3 | 1024 | 349,547 |
| matrixtranspose | 49.5% | 6% | 48、47、6 | 48 | 2 | 1013 | 518,400 |
| pagerank | 87.9% | 7% | 48、2、13 | 48 | 5 | 1024 | 209,715 |
| relu | 0% | — | — | 0 | 2 | 2560 | 1,310,720 |
| simpleconvolution | 0.3% | 100% | 0 | 1 | 3 | 512 | 174,677 |
| spmv | 26.9% | 7% | 3、4、0 | 48 | 5 | 1081 | 221,308 |

- 组件编号 0 = driver GPU 1（角上）。mi200 的同样统计：远程读比例除 kmeans（**42.9%**，hpca 0%）、spmv（20.6%）外与 hpca 相差 < 1 个百分点（见 `results_index/1015_place_*_mi200_67f1c03/`）。
- **没有小于 192 KB 的 workload buffer**：统计里只有总量（`allocation_workload_*`），没有逐个 buffer 的大小，但每个 workload 的平均 buffer 都在 100 MB 以上，所以“小 buffer 整个放在 GPM 1”不是这些 workload 的问题。放在 GPM 1 的是运行时分配（`allocation_runtime_*`：每个程序约 144 次、合计约 100 KB，kernarg/code object），它们解释了 aes/fir/simpleconvolution 那一点 100% 来自 GPM 1 的远程读。
- ⚠️ **matrixmultiplication 的“100% 来自一个 owner”可能是截断造成的**：3 个 buffer 共 1 GB，按 48 段分布；只跑开头 16384 个 WG 时，读到的部分可能都在第一段。需要用 `l2_page_source.csv` 按页确认（没做）。在确认之前，“集中型 / 分散型”的分类不要用于 matrixmultiplication。

**4. 诊断 D 和 10-15 的短片段 ablation**（加速比 = baseline total_time / 配置 total_time；WG 数相同才算）

mi200 与 hpca（10-15，代码 `67f1c03`，`-max-wg 32768`，spmv 1536；140 个运行全部成功）：

| workload | mi200 m1 | mi200 m2 | mi200 m3 | mi200 complete | hpca m1 | hpca m2 | hpca m3 | hpca complete |
|---|---|---|---|---|---|---|---|---|
| aes | 0.997 | 1.004 | 1.001 | 1.002 | 1.010 | 1.000 | 1.002 | 1.010 |
| bitonicsort | 1.022 | 1.000 | 1.000 | 1.022 | 1.955 | 1.000 | 1.000 | 1.955 |
| fastwalshtransform | 0.872 | 1.000 | 1.000 | 0.872 | 1.648 | 1.000 | 1.000 | 1.648 |
| fft | 1.008 | 1.000 | 1.000 | 1.008 | 1.088 | 1.000 | 1.000 | 1.088 |
| fir | 0.999 | 1.000 | 0.999 | 1.002 | 1.015 | 0.999 | 0.998 | 1.014 |
| floydwarshall | 1.602 | 2.506 | 0.950 | 2.544 | 1.410 | 2.060 | 1.063 | 3.255 |
| im2col | 1.010 | 1.094 | 1.070 | 1.190 | 1.007 | 1.107 | 1.084 | 1.220 |
| kmeans | 1.027 | 2.805 | 0.999 | 2.839 | 1.028 | 1.931 | 0.999 | 1.968 |
| matrixmultiplication | 0.991 | 2.055 | 3.244 | 3.843 | 1.006 | 2.931 | 3.432 | 4.324 |
| matrixtranspose | 0.996 | 1.000 | 0.999 | 0.997 | 1.004 | 1.000 | 1.000 | 1.004 |
| pagerank | 1.010 | 1.001 | 1.020 | 1.024 | 1.032 | 1.042 | 0.988 | 1.008 |
| relu | 1.012 | 1.000 | 1.000 | 1.012 | 1.225 | 1.000 | 1.000 | 1.225 |
| simpleconvolution | 0.999 | 1.004 | 1.003 | 1.002 | 1.008 | 1.000 | 1.002 | 1.010 |
| spmv | 0.978 | 0.998 | 0.998 | 0.979 | 1.265 | 1.000 | 1.000 | 1.263 |
| **GMEAN** | **1.028**（14 个） | **1.218**（14 个） | **1.090**（14 个） | **1.276**（14 个） | **1.165**（14 个） | **1.204**（14 个） | **1.102**（14 个） | **1.456**（14 个） |

诊断 D（10-14，mi300x，`756f214`，`-max-wg 32768`，spmv 1536）：10-14 改用 mi200 时停掉，加上 yss2 硬件故障（3 个 SIGBUS），25 格里只有 7 格完成，**基本不可用**：

| workload | mi300x m1 | mi300x m2 | mi300x m3 | mi300x complete |
|---|---|---|---|---|
| bitonicsort | ✗b:失败 | ✗b:失败 | ✗b:失败 | ✗b:失败 |
| fastwalshtransform | ✗未完成 | ✗未跑 | ✗未跑 | 1.009 |
| fir | ✗b:未完成 | ✗b:未完成 | ✗b:未完成 | ✗b:未完成 |
| pagerank | ✗b:未完成 | ✗b:未完成 | ✗b:未完成 | ✗b:未完成 |
| relu | 1.015 | ✗未跑 | ✗未跑 | 1.015 |
| **GMEAN** | **1.015**（1 个） | — | — | **1.012**（2 个） |

（✗ 后面是没有结果的原因：失败 / 未完成 = 停止时还在跑 / 未跑；“b:” 表示 baseline 那一格没有结果。）

**5. T14 平台测量：三套配置并排**（mi200 是 10-15，`67f1c03`；其余同上表；CSV 在 `results_index/platform_mi200/`）

| 项 | 关键数字 | hpca | mi300x-eighth | mi200 |
|---|---|---|---|---|
| 1 延迟阶梯 | L1V / L2 / HBM（ns，A 组） | 115.0 / 129.0 / 169.7 | 115.0 / 129.0 / 164.7 | 115.2 / 129.0 / 198.5 |
| | L1 拐点、L2 拐点 | 16→24 KB；4→6 MB | 32→48 KB；4→6 MB | 16→24 KB；2→3 MB |
| | B − A（TLB）：≤128 KB / 256 KB–1 MB / 2–4 MB / ≥6 MB | 0 / +3 / +104 / +104 | 同左 | 0 / +3 / +102–104 / +101 |
| | 远程 B − A（64 MB，1/3/6/12 跳） | +732（与跳数无关） | +732 | +720 / +814 / +758 / +818 |
| 2 带宽—WG 数 | HBM 读 / 写 / 复制峰值，B/周期（饱和 WG 数） | 59 (2) / 66 (1) / 61 (1) | 243 (8) / 318 (4) / 295 (4) | **75 (8)** / 121 (1) / 90 (2) |
| | L2 读 / 写 / 复制峰值（饱和 WG 数）；满 WG 时 | 622 (16) / 638 (8) / 846 (16)；419 / 423 / 698 | 631 (16) / 640 (8) / 856 (16)；465 / 460 / 702 | 488 (16) / 526 (8) / 612 (16)；369 / 364 / 550（28） |
| 3 跳数矩阵 | 固定开销 + 每跳；最大残差 | 198.1 + 71.8 ns/跳；5.3 ns | 同左 | 198.1 + 71.8 ns/跳；5.2 ns |
| 4 单对远程带宽 | 1 跳：远端 L2 / 远端 HBM（饱和 WG 数） | 26.3 (8) / 21.7 (16) | 26.4 (8) / 22.4 (16) | 26.4 (8) / 18.6 (8) |
| | 6 跳：远端 L2 / 远端 HBM | 7.9 (4) / 7.5 (8) | 8.0 (4) / 7.5 (8) | 8.0 (4) / 7.3 (8) |
| 5 incast（owner = driver 25，1 MB） | 共享读 k = 1/2/4/8/16/47（总 B/周期） | 25.6 / 51.2 / 70.5 / 108.1 / 111.8 / 113.5 | 25.6 / 51.2 / 70.5 / 108.3 / 111.8 / 113.5 | 25.4 / 50.6 / 70.5 / 108.2 / 111.8 / 113.5 |
| | 不相交读 k = 1/2/4/8/16/47 | 26.4 / 50.5 / 92.5 / 115.3 / 113.0 / 90.8 | 26.4 / 50.3 / 64.9 / 92.2 / 92.9 / 93.5 | 26.8 / 50.4 / 65.1 / 97.1 / 97.4 / 92.6 |
| 6 通信模式 | all-to-all / 邻居 / 转置 / 环 / 蛇形（聚合 B/周期） | 339 / 899 / 160 / 182 / 182 | 340 / 902 / 157 / 178 / 178 | 338 / 847 / 148 / 171 / 171 |
| 7 kernel 边界开销 | 单 GPM / 48 GPM 各一个 queue / 统一 GPU 1 GPM / 统一 GPU 48 GPM（间隔，ns） | 896 / 945 / 928 / 1114 | 886 / 936 / 927 / 1114 | 978 / 984 / 969 / 1280 |

- 带宽测试的横轴是 WG 数（不是 CU 数，10-15 更正）。mi200 的 HBM 读只有理论峰值 241 B/周期的 31%（诊断见 10-15 日志、D21）。mi200 的 HBM 延迟比另外两套高约 30 ns（pseudo channel 32 B 一次、同一请求两次 burst、行保持的时序）。wafer 层的数字（跳数、远程带宽、incast、通信模式）三套配置几乎一样，因为 GPM 之间的参数没有改。

**6. 可复现性检查**（mi200，`67f1c03`，yss3）：fir、pagerank 的 baseline 和 Complete 各跑两次（`results_index/1015_det_wg32k_mi200_67f1c03/`），和 ablation 里的同一格比较，排序后 16.5 万 / 18.2 万 / 17.1 万 / 18.8 万项统计**逐项完全一致**。

**7. kmeans 诊断**（hpca baseline，完整运行，修复后的二进制，yss3）：total_time **9,561,046 ns**，和 7 月论文的结果完全一致，约 12 小时；说明 kmeans 不是卡死，只是慢。

### mi200 验证清单（冻结前，10-16）

| 项 | 状态 |
|---|---|
| 1 旧配置不受影响 | ✅ 本机 hpca fir（100,477 项）、hpca matrixtranspose（102,332）、mi300x fir（294,496）改动前后逐项一致；服务器上 hpca matrixtranspose 的 baseline（12,747.6 µs）和论文一致 |
| 2 实际生效的参数和配置表一致 | ✅ 单元测试；服务器上 `config_used.json` 核对：`gpm-preset=mi200`、`num-memory-banks=8`、`l2-cache-size-mb=2`、其余论文参数不变 |
| 3 所有 workload × 配置不死锁、不崩溃 | ✅ 70/70；LLM 算子 105/110 已完成且全部成功（其余仍在跑） |
| 4 可复现 | ✅ 见上 |
| 5 平台特性 | ⚠️ HBM 读 31%（D21，暂时保持） |
| 6 内存和运行时间 | ✅ 运行时间中位数 48 分钟；kmeans 单个 11–13 小时（完整阶段 4 的 78,600 WG 可能超过一天） |
| 7 有意的弱化写成文档 | 冻结时写（D20、D21） |

### T11 探针（commit `f7b9eef`，timing 模式，不做 verify，10-13）

> ⚠️ **10-13 更正：这一轮用的是 baseline 二进制的默认参数（`-bandwidth=1`、`-switch-latency=20`、没有 `-magic-memory-copy` 等），不是论文配置。** P1、P2 是功能性结论，仍然有效；**P3a、P3b、P3c、P4 的性能数字（时间、延迟、带宽）是“默认参数，作废”**，路径类结论（远程读写经过远端 L2）需要在论文配置下重新确认。重跑见 T13 阶段 3 / 阶段 4。

结果目录：仓库根目录 `results/1013_probe-<实验>_wafer48_baseline_f7b9eef/`（16 个，全部 exit 0；`results/` 已加入 `.gitignore`，只在本机）。汇总脚本：`python3 akkalat/t11_analyze_probe.py results/<exp_id>`。GPM 编号：driver ID g ↔ 组件名 `GPU[t]`，g ≤ 24 时 t = g−1。buffer 2 MB，默认 32768 个线程（每 GPM 32 CU × 4 WG × 256）。

| 实验 | 结论 | 关键数字 |
|---|---|---|
| **P1 数据放置**（owner = runner = 5） | ✅ 通过 | RDMA 进出事务 = 0 / 0；只有 `GPU[4]` 的 DRAM 有读：32782 次（2 MB / 64 B = 32768，多出的 14 次是 code object/kernarg）；第 1 遍 32768 次 `local_dram`，第 2 遍 32768 次 `local_l2_cache` 命中；每遍 11.4 / 7.8 µs |
| **P2 kernel 放置**（同 P1） | ✅ 通过 | 只有 `GPU[4]` 的 32 个 CU 有指令（共 268288 条），其余 47 个 GPM 全为 0 |
| **P3a 远程读路径**（owner 5、runner 1，相距 4 跳） （**默认参数，作废**）| ✅ 路径通过；⚠️“第 2 遍明显更快”只在延迟受限时成立 | 路径：L1V → 本地 RDMA → mesh → 远端 RDMA → **远端 L2** → 远端 DRAM。第 1 遍 32768 次 `remote_dram`（远端 L2 miss），第 2 遍 32768 次 `remote_l2_cache`（远端 L2 hit）；RDMA 事务 32768/遍 = bytes/64。带宽受限配置：200.9 → 199.5 µs，平均远程延迟 3.05 µs；1 个 WG：1458.6 → 1002.0 µs（快 31%），平均远程延迟 337 ns |
| **P3b 距离**（runner 1，owner 2/4/7/48） （**默认参数，作废**）| ✅ 通过 | 1 个 WG（延迟受限）：1/3/6/12 跳的平均远程延迟 = **212 / 316 / 472 / 784 ns**，单调增加，约 52 ns/跳。带宽受限配置：3053 / 3054 / 3454 / 5917 ns（近处被排队掩盖，远处才拉开） |
| **P3c 远程写**（同 P3a，mode = write） （**默认参数，作废**）| ✅ 记录完成 | 写走和读相同的路径（RDMA → mesh → 远端 L2）。第 1 遍 32768 次 `remote_write_allocate`（在远端 L2 分配，不读 DRAM），第 2 遍 32768 次 `remote_l2_cache` 写命中；GPM 5 的 DRAM 读写都是 0 → 数据 dirty 地留在远端 L2。200.3 / 199.3 µs |
| **P4 并发**（48 个 GPM 各一个 queue，各读自己的 2 MB） （**默认参数，作废**）| ✅ 通过 | 48 个 GPM 都有指令；RDMA 事务 = 0；DRAM 读 1573536 = 48 × 32782；第 2 遍 48 × 32768 次本地 L2 命中。每遍 **13.9 / 10.3 µs**，单个 GPM（P1）是 11.4 / 7.8 µs → 约 1.2×，不是 48× |

### T13 阶段 3 校准（`platbench`，10-13，⏸ 偏差超过 10%，等用户决定）

代码：带宽、远程带宽、incast、通信模式、kernel 用 `0e38983`；延迟和跳数用修正后的 `ca7ea0b`（见日志）。两套配置的 `config_deviation` 只有 `-gpm-preset`、`-num-memory-banks`（mi300x）和 benchmark 参数。B/周期按 1 GHz 计（1 B/周期 = 1 GB/s）。

| 项 | 目标（mi300x） | hpca 实测 | mi300x 实测 | mi300x 偏差 |
|---|---|---|---|---|
| HBM 读（16 MB/GPM，读一遍） | 315 B/周期 | 59（2 CU 饱和；满 32 CU 57） | **243**（8 CU 饱和；满 38 CU 224） | **−23%** ⚠️ |
| HBM 写 | 315 | 66（1 CU） | 318（4 CU） | +1% ✅ |
| HBM 复制（读+写） | 315 | 61（1 CU） | 295（4 CU） | −6% ✅ |
| L2 读（1 MB，热身后 4 遍） | 2048 B/周期 | 622（16 CU；满 CU 419） | **631**（16 CU；满 CU 465） | **−69%** ⚠️ |
| L2 写 | 2048 | 638（8 CU） | **640**（8 CU） | **−69%** ⚠️ |
| L2 复制 | 2048 | 846（16 CU） | **856**（16 CU） | **−58%** ⚠️ |
| L1V 命中延迟（单线程，A 组） | — | 115.0 ns（≤ 16 KB） | 115.0–115.4 ns（≤ 32 KB） | 拐点和 L1V 容量一致 ✅ |
| L2 命中延迟 | — | 129.0 ns（24 KB–1 MB），130.7（2–4 MB） | 129.0 ns（48 KB–1 MB），130.7（2–4 MB） | |
| HBM 延迟（≥ 6 MB，A 组） | — | 169.7 ns | 164.7 ns | |
| TLB miss 代价（B − A，≥ 6 MB） | — | +104 ns | +104 ns | |
| 远程读延迟，远端 L2 命中（1 MB）1/3/6/12 跳 | — | 261 / 405 / 621 / 1053 ns | 261 / 405 / 621 / 1053 ns | |
| 远程读延迟，远端 HBM（64 MB，A 组）1/3/6/12 跳 | — | 312 / 456 / 672 / 1104 ns | 307 / 451 / 667 / 1099 ns | |
| 跳数延迟拟合（329 对，远端 L2 命中） | — | 198.1 ns + 71.8 ns/跳，最大残差 5.3 ns | 同左 | |
| 单对远程读带宽峰值（1 跳 / 6 跳，远端 L2） | — | 26.3 / 7.9 B/周期 | 26.4 / 8.0 B/周期 | |
| 单对远程读带宽峰值（1 跳 / 6 跳，远端 HBM 16 MB） | — | 21.7 / 7.5 B/周期 | 22.4 / 7.5 B/周期 | |

- **旧配置 HBM**：代码算的理论值 128 B/周期（4 个控制器 × 32 B/ns），实测 59–66 B/周期，和“64 B 请求只用到 128 B burst 的一半”的推算（约 64）一致。投稿版 Table II 写的 0.375 TB/s/GPM 是实测值的 6.4 倍。
- 延迟是**向量访存路径**的数字：单线程的 L1V 命中就要 115 ns（标量缓存命中只要 38 ns，即向量路径的流水线、地址转换和合并占了约 77 ns），各级之间只差 14 ns（L1→L2）和 35–40 ns（L2→HBM）。
- 延迟“不做每周期等效缩放”（用户 10-13 决定），所以 mi300x 的延迟和 hpca 基本相同；HBM 延迟差 5 ns 来自 DRAM 时序按纳秒换算后的取整。
- L2 拐点两套配置都在 4–6 MB 之间（每 GPM 的 L2 都是 4 MB：hpca 4 × 1 MB，mi300x 16 × 256 KB）。

### T14 第一档平台数据摘要（两套配置并排；CSV 在 `results_index/platform/`）

| 项 | 关键数字 | hpca | mi300x-eighth |
|---|---|---|---|
| 1 延迟阶梯 | L1V / L2 / HBM（ns，A 组） | 115.0 / 129.0 / 169.7 | 115.0 / 129.0 / 164.7 |
| | L1 拐点、L2 拐点 | 16→24 KB；4→6 MB | 32→48 KB；4→6 MB |
| | B − A（TLB）：≤128 KB / 256 KB–1 MB / 2–4 MB / ≥6 MB | 0 / +3 / +104 / +104 ns | 同左 |
| | 远程 B − A（64 MB，1–12 跳） | +732 ns（与跳数无关） | +732 ns |
| 2 带宽—CU 数 | HBM 读 / 写 / 复制峰值，B/周期（饱和 CU 数） | 59 (2) / 66 (1) / 61 (1) | 243 (8) / 318 (4) / 295 (4) |
| | L2 读 / 写 / 复制峰值（饱和 CU 数）；满 CU 时 | 622 (16) / 638 (8) / 846 (16)；419 / 423 / 698 | 631 (16) / 640 (8) / 856 (16)；465 / 460 / 702 |
| 3 跳数矩阵 | 固定开销 + 每跳 | 198.1 ns + 71.8 ns/跳 | 198.1 ns + 71.8 ns/跳 |
| | 残差（329 对） | 最大 5.3 ns，RMS 2.3 ns | 同左 |
| 4 单对远程带宽 | 1 跳：远端 L2 / 远端 HBM，B/周期（饱和 WG 数） | 26.3 (8) / 21.7 (16) | 26.4 (8) / 22.4 (16) |
| | 6 跳：远端 L2 / 远端 HBM | 7.9 (4) / 7.5 (8) | 8.0 (4) / 7.5 (8) |
| | 推算在途 line 数（带宽 × 空载延迟 / 64 B） | 1 跳约 109，6 跳约 78 | 同左 |
| 5 incast（owner = driver 25，1 MB） | 共享读 k = 1/2/4/8/16/47（总 B/周期） | 25.6 / 51.2 / 70.5 / 108.1 / 111.8 / 113.5 | 25.6 / 51.2 / 70.5 / 108.3 / 111.8 / 113.5 |
| | 不相交读 k = 1/2/4/8/16/47 | 26.4 / 50.5 / 92.5 / 115.3 / 113.0 / 90.8 | 26.4 / 50.3 / 64.9 / 92.2 / 92.9 / 93.5 |
| 6 通信模式 | all-to-all（每对 16 KB）：聚合 B/周期（推算的最大链路利用率） | 339（1.6%） | 340（1.6%） |
| | 邻居交换（256 KB） | 899（0.7%） | 902（0.7%） |
| | 转置 | 160（3.0%） | 157（2.9%） |
| | 环（按 driver ID）/ 蛇形环 | 182 / 182（0.5%） | 178 / 178（0.5%） |
| 7 kernel 边界开销 | 单 GPM / 48 GPM 各一个 queue / 统一 GPU 1 GPM / 统一 GPU 48 GPM（连续 10 个的平均间隔） | 896 / 945 / 928 / 1114 ns | 886 / 936 / 927 / 1114 ns |

**和预期不一样的现象**

1. **L2 带宽在 16 个 CU 处见顶，之后下降 25–33%**，而且两套配置几乎一样（两者的 L2 理论吞吐差 2 倍）→ 瓶颈在 L2 之前，CU 越多越差，像是某处的争用或重试。
2. **mi300x 的 HBM 读（243）比写（318）低 24%**；hpca 1 个 CU 就能把 HBM 打满。
3. **远程 TLB miss 很贵**：本地 B − A 是 104 ns，远程是 732 ns，而且和数据离多远无关（发起方固定是 GPM 1）。推测远程页的地址转换要去中心的 IOMMU。这对应第二档第 12 项（`[提案]`，没做）。
4. **跳数延迟只取决于跳数**（残差 ≤ 5.3 ns）：边角和中心没有差别，中心 tile 没有让路由绕路。唯一的系统性差别是方向：从 `GPU[0]` 到 `GPU[48]` 比反方向慢约 10 ns（1064.7 vs 1054.7 ns）。
5. **单对远程带宽只有链路带宽的 3%（26 / 768），而且大致和延迟成反比**（6 跳只有 7.9）。推算的在途请求数在 1 跳和 6 跳时不同（109 vs 78），都超过 RDMA 的 64，所以不是单纯被“64 个 outstanding”卡住；没有 RDMA 表的统计，没法确认瓶颈。
6. **incast 的共享读没有任何好处**：k ≥ 8 时，共享读和不相交读都卡在约 113 B/周期（owner 端），共享读的总时间随 k 线性增长 → owner 端没有合并相同 line 的请求。对 T7（区域复制）来说：热点 owner 最多只能服务约 4 个全速读者。hpca 的不相交读在 k = 47 时从 115 降到 91；mi300x 在 k = 4 时就比 hpca 低（65 vs 92）。
7. **48 个 GPM 同时通信时，每个 GPM 的带宽远低于单对**（环：每 GPM 3.8 B/周期，单对 1 跳是 26），而推算的链路利用率不到 3% → 瓶颈不在 mesh 链路（至少按 YX 路由推算如此），在端点（RDMA 或 owner 端）。T10 的代价模型不能只按链路带宽算。
8. 第 1 项最初的实现测到的是**标量缓存**（见日志），已修正重跑。

## 日志

<!-- 追加格式：
### YYYY-MM-DD HH:MM (T#)
- 做了什么
- 结果 / 遇到的问题
- 下一步
-->

### 2026-10-12 23:39 (T11) 阶段 A：第 0 步代码核对（只读代码，没有运行任何东西）

**Q1 fork 的版本**
- `mgpusim/go.mod:1`：`module github.com/sarchlab/mgpusim/v3`，依赖 `github.com/sarchlab/akita/v3 v3.0.0`（`mgpusim/go.mod:10`）。
- `akkalat/go.mod:29-31`：`replace` 到本地的 `../mgpusim` 和 `../akita`，所以实际用的是仓库里改过的源码。Go 1.22.0（`mgpusim/go.mod:48`、`akkalat/go.mod:33`）。
- 结论：fork 是 **v3**，不是上游的 v5。

**Q2 driver API（全部存在，签名和 T11 表格一致）**
| 函数 | 位置 | 签名 / 行为 |
|---|---|---|
| `SelectGPU` | `mgpusim/driver/api.go:57` | `(c *Context, gpuID int)`，只设置 `c.currentGPUID` |
| `CreateUnifiedGPU` | `api.go:66` | `(c *Context, gpuIDs []int) int` |
| `CreateCommandQueue` | `api.go:103` | `(c *Context) *CommandQueue`，`q.GPUID = c.currentGPUID`，所以 queue 绑定到创建时选中的 GPU |
| `DrainCommandQueue` | `api.go:116` | `(q *CommandQueue)`，阻塞到这个 queue 为空 |
| `AllocateMemory` | `api.go:133` | `(ctx *Context, byteSize uint64) Ptr`，内部调用 `memAllocator.Allocate(pid, size, ctx.currentGPUID)`（`api.go:150`）；普通 GPU 用 `device.allocatePage()`，页面在这个 GPU 上（`internal/memoryallocator.go:98`、`:199-206`） |
| `Remap` | `api.go:175` | `(ctx *Context, addr, size uint64, deviceID int)` |
| `Distribute` | `api.go:182` | `(ctx *Context, addr Ptr, byteSize uint64, gpuIDs []int) []uint64` |
| `EnqueueLaunchKernel` | `mgpusim/driver/kernel.go:14` | `(queue *CommandQueue, co *insts.HsaCo, gridSize [3]uint32, wgSize [3]uint16, kernelArgs interface{})`；queue 绑定普通 GPU 时，整个 kernel 只发给这个 GPU；绑定 unified GPU 时走 `enqueueLaunchUnifiedKernel`（`kernel.go:145`） |
| `LaunchKernel` | `kernel.go:86` | 便捷版：新建 queue、enqueue、drain |

- GPU 编号从 1 开始：`d.GPUs[queue.GPUID-1]`（`mgpusim/driver/driver.go:358`、`:391`）。
- 注意：`EnqueueLaunchKernel` 用 `ctx.currentGPUID` 分配 code object/kernarg/packet（`kernel.go:40-51`），见“规格与代码的差异”。

**Q3 有没有 `benchmarks/mccl`**
- 有：`mgpusim/benchmarks/mccl/`（`allreduce.go/.cl`、`broadcast.go/.cl`、`communicator.go` 等）。`broadcast.go:20` 给每个 GPU 建一个 queue，`:49` `SelectGPU`，`:56` `EnqueueLaunchKernel`，`:66` `DrainCommandQueue`；`broadcast.cl:1` 是 `pushData`（远程写），`allreduce.cl:1` 是 `reduceData`。
- mccl 没有在 `akkalat/benchmarkselection/benchmark.go` 里注册，现有实验流程跑不到它。

**Q4 wafer 平台上远程请求走哪个组件；哪个统计能区分远端 L2 命中和远端 HBM**
- 路径：**L1V → 本 GPM 的 RDMA 引擎 → mesh → 远端 GPM 的 RDMA 引擎 → 远端 L2 → （miss 时）远端 DRAM**。
  - 请求方：`connectL1ToL2` 里，地址不在本 GPM 8 GB 范围内时由 `ModuleForOtherAddresses = rdmaEngine.ToL1` 接走（`akkalat/baseline/runner/r9nanobuilder.go:541-544`、`:578`）。RDMA 用 `RemoteRDMAAddressTable` 找到 owner 的 RDMA（`mgpusim/timing/rdma/comp.go:487`；表按 8 GB 一个 bank 建，`timingplatform.go:591-596`、`:820-829`）。
  - Owner 方：`processReqFromOutside` 用 `localModules.Find(addr)` 选本地 L2，然后从 `ToL2` 发出（`comp.go:600-616`；`localModules` 就是本地 L2 的 Top 口，`r9nanobuilder.go:554-561`）。L2 下面接的是本地 DRAM（`r9nanobuilder.go:596-606`）。
  - 所以**按代码，远程读会经过远端 L2**；P3a 用实验确认。
  - 网络是 akita 的 mesh（`timingplatform.go:19`、`:602-616`），7×7，没有单独的 NI 组件。CuPath 的远程数据通路在 RDMA 内部（`rdma/remote_requester.go:357`），默认关闭。
- 统计：用 **`-report-l2-source`**（`akkalat/baseline/runner/flag.go:207`；启用在 `runner.go:186-197`），输出 `<metric-file-name>_l2_source_*.csv`（`akita/mem/trace/l2source_dump.go`）：
  - `_data_source.csv` 的 `source` 列：L2 命中记成 `l2_cache` / `l2_mshr`（`akita/mem/cache/writeback/directorystage.go:378`、`:410`、`:543`、`:561`），L2 miss 后从 DRAM 填充记成 `dram`（`writebufferstage.go:832-842`）。经过 RDMA 的请求加前缀 `remote_`，否则加 `local_`（`akita/mem/trace/l2source_record.go:254-259`）。所以 **`remote_l2_cache` / `remote_l2_mshr` 对 `remote_dram`** 就能区分远端 L2 命中和远端 HBM，并且每行都带 requester/provider GPM 和跳数。
  - `_summary.csv` 的 `remote_gpm` 行（表头见 `l2source.go:21`）和 `_remote_matrix.csv`：每对 GPM 的远程访问数、字节数、平均延迟和跳数，可以直接用于 P3b。
  - 辅助统计：`-report-rdma-transaction-count`（`flag.go:77`）、`-report-dram-transaction-count`（`flag.go:79`）。

**Q5 现有 benchmark 用 unified GPU 还是每个 GPM 一个队列**
- **unified GPU**。runner 在 `Init` 里无条件调用 `createUnifiedGPUs()`（`akkalat/baseline/runner/runner.go:160`），把 GPU 1–48 绑成一个 unified 设备，`r.GPUIDs = []int{unifiedID}`（`runner.go:515-529`）；`AddBenchmark` 把它传给 benchmark（`runner.go:547-553`，入口 `akkalat/baseline/main.go:28-34`）。
- 以 fir 为例：`SelectGPU(ctx, gpus[0])` 选中 unified 设备（`mgpusim/benchmarks/heteromark/fir/fir.go:78`），只建一个 queue（`fir.go:59`）。
- unified launch 时 WG 的切分方式（`mgpusim/driver/driver.go:433-466`）：`wgPerCU = ceil(总WG/总CU)`，每个 GPU 分到 `cuCount × wgPerCU` 个连续的 WG。**和上游一样，WG 少的时候前面的 GPU 分到全部 WG，后面的一直空闲**（`driver.go:386-389` 跳过分到 0 个 WG 的 GPU）。
- 另外有 `AddBenchmarkWithoutSettingGPUsToUse`（`runner.go:557`），探针可以用它绕过 unified 设备，自己按 GPM 建 queue。

**阶段 B 的计划（等用户确认）**
- 新增 `mgpusim/benchmarks/microbench/probe/`（`touch` kernel 用 fp32，hsaco 在本机编译），并在 `SelectBenchmark` 里加 `case "probe"`。不改任何已有组件的行为。
- P2 用 `-report-inst-count`（`flag.go:69`；每个 CU 输出 `cu_inst_count`，`report.go:846-868`）加上每个 GPM 的 CP `kernel_time`（`report.go:951-957`）。
- 下一步：等用户确认阶段 A，并决定上面“需要用户决定”的 3 条。

### 2026-10-13 04:55 (T11) 阶段 B：探针实验完成

- 用户 10-13 确认阶段 A（“先实现吧”），三个待定事项按推荐方案执行（见“需要用户决定”）。分支 `isca27/t11-probe-placement-comm`，提交：`b465d45` 探针 + 骨架，`0ca173f` 运行脚本，`a097f31` `.gitignore`，`d1f6d8c` 汇总脚本，`f7b9eef` 每个 rank 一个 Context（修死锁），`a874e5f` 心跳。
- **SPMD 骨架**（给 T10 复用）：`mgpusim/benchmarks/microbench/probe/spmd.go`
  - `NewSPMD(driver, baseCtx, gpus)`：每个 GPM 一个 rank，每个 rank 一个 **Context（`InitWithExistingPID`，共享 PID，所以虚拟地址空间相同）** 和一个 command queue。
  - `AllocShards(bytes)` / `AllocFor(rank, gpu, bytes)`：分片 i 的物理页在 `GPUs[i]` 上，并由 rank i 的 Context 跟踪脏状态。
  - `UploadPtrTable(shards)`：把所有分片地址写成 uint64 数组，复制到每个 GPM，kernel 里用 `table[j]` 访问 rank j 的分片（push / pull 都可以）。
  - `RunPhase(co, []Launch)`：先给每个 rank enqueue，再逐个 drain，一个阶段结束才进入下一个阶段；`Now()` 读模拟时间，用来算每阶段耗时。
  - 注意：每个 rank 的 Context 一直选在自己的 GPM 上，因为 driver 把 code object / kernarg / packet 分配在当前选中的 GPU 上（`kernel.go:40-51`）；必须单 goroutine 驱动。
  - 探针本身：`probe.go`（`touch` kernel，fp32），在 `akkalat/benchmarkselection/probe.go` 注册为 `-benchmark probe`，参数 `-probe-owner/-probe-runner/-probe-bytes/-probe-mode/-probe-passes/-probe-threads/-probe-all-gpms`。
- **遇到的问题**：
  1. 第一版骨架所有 rank 共用一个 Context，P4 死锁（只有 17/48 个 GPM 启动）。原因见“需要用户决定”里的 flush 竞争；改成每个 rank 一个 Context 后 48 个 GPM 全部完成，P1–P3 的数字和改之前完全一致。修复前的结果（commit `0ca173f` / `a097f31` / `d1f6d8c`）作废，以 `f7b9eef` 为准。
  2. 第一次 P4 跑了 40 分钟没人发现卡住。为此给 runner 加了**可选的心跳**（`akkalat/baseline/runner/heartbeat.go`，`-heartbeat-file` 或环境变量 `EXP_HEARTBEAT`）：每 10 秒写模拟时间和已完成 WG 数，不改模拟器行为。`exp web` 网页据此判定卡住：模拟时间 3 分钟不推进，或完成的 WG 数 15 分钟不增加。用旧的死锁版本验证过：模拟时间停在 24533 ns、WG 停在 136，1 分钟阈值下 61 秒被标出。
- **对 T10 的结论**：数据放置、kernel 放置、远程 load/store 都可以在不改模拟器的前提下实现；远程读写都经过远端 L2。需要注意：(1) 一定要每个 GPM 一个 Context；(2) 远程带宽约为本地的 1/18，带宽受限时远端 L2 命中带来的好处被 RDMA/mesh 排队掩盖；(3) kernel 之间 L2 内容会保留（P1、P3a 第 2 遍都命中），但前提是没有触发 flush。
- 下一步：等用户看“需要用户决定”里的两条新问题；T10 仍是 `[提案]`，没有开始。

### 2026-10-13 (T11/T12) 更新待决事项；T12 配置确认（⏸ 等用户确认）

- 按用户 10-13 的答复，把“需要用户决定”里的 flush 竞争（→ T12 修 driver）和“远程带宽 1/18”（→ 参数用错）两条标为已决定；T11 结果摘要里 P3a/P3b/P3c/P4 的性能数字标为“默认参数，作废”。
- `akkalat/t11_run_probe.sh` 改为从 runall2 取参数：新增 `akkalat/t11_probe_flags.py`，调用 `runall2_config.build_common_flags(runall2_args.parse_args(["--disable-servers", "--max-wg", "0", "--report-l2-source"]))`，没有手写任何配置参数；meta.json 新增 `config` 和 `config_deviation` 字段（commit `d6b765c`，分支 `isca27/t11-probe-placement-comm`）。
- 新建分支 `isca27/t12-driver-flush-fix`（基于 `d6b765c`）。

**CONFIG.md 第 3 节 · 第 1 步：7 个文件和 95b4f46 对比**

| 文件 | HEAD | 95b4f46 | 结果 |
|---|---|---|---|
| `akkalat/runall2_constants.py` | c46181e7ddbc | c46181e7ddbc | 一致 |
| `akkalat/runall2_config.py` | 073257215378 | 073257215378 | 一致 |
| `akkalat/baseline/runner/flag.go` | 142c66f8fec7 | 142c66f8fec7 | 一致 |
| `akkalat/baseline/runner/shaderarray.go` | 0cad15f3f860 | 0cad15f3f860 | 一致 |
| `akkalat/baseline/runner/r9nanobuilder.go` | be11d0d9cd45 | be11d0d9cd45 | 一致 |
| `akkalat/baseline/runner/timingplatform.go` | 7f4c214b78e6 | 7f4c214b78e6 | 一致 |
| `akkalat/baseline/runner/runner.go` | c84865b80dcc | c84865b80dcc | 一致 |

95b4f46 之后 `akkalat/baseline`、`runall2_*`、`mgpusim/driver`、`akita` 下只多了 `heartbeat.go`（新文件）和 `tracers.go` 的 2 行（启动心跳），另外新增了探针 benchmark（`mgpusim/benchmarks/microbench/probe/`、`akkalat/benchmarkselection/probe.go` 和一个 `case "probe"`）。

**第 2、3 步：T12 要跑的实验、完整参数、和论文配置的差异**

论文配置的公共参数（由 `build_common_flags(--disable-servers --max-wg 4096)` 生成）：
```
-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -disable-servers -mmutlb-lookup-latency=80 -max-wg=4096
```

⚠️ **重要发现**：论文配置里的 `-magic-memory-copy` 会让 driver 用 `globalStorageMemoryCopyMiddleware`，**根本不加载**要修改的 `defaultMemoryCopyMiddleware`（`mgpusim/driver/builder.go:102-114`）。所以 R1 只能证明“论文配置不受影响”，测不到修改本身是否改变了单 queue 的时序。为此建议加 R2。

| 编号 | 内容 | 参数 | 和论文配置的差异 |
|---|---|---|---|
| R1 回归（论文配置） | fir、matrixmultiplication，修复前（`d6b765c`）和修复后各跑一次，同一台服务器 | 上面的公共参数 + `-benchmark fir` / `-benchmark matrixmultiplication` | `-max-wg=4096`（benchmark 截断参数，T12 要求“较小的 -max-wg”；原脚本默认 78600）。无配置修改 |
| R2 回归（**建议追加**，走被修改的代码） | 同 R1，但去掉 `-magic-memory-copy`，让单 queue 的 H2D/D2H 走 `defaultMemoryCopyMiddleware` | 公共参数去掉 `-magic-memory-copy` | **少了 `-magic-memory-copy`**（为了覆盖被修改的代码路径；只用于 T12 回归，不进论文） |
| R3 共用 Context 的 P4 | 探针 `-probe-all-gpms -probe-shared-ctx`（新开关，所有 rank 共用一个 Context），2 MB/GPM，2 遍；修复后跑一次。**建议**修复前也跑一次（带心跳），确认在论文网络参数下也能复现死锁 | 公共参数去掉 `-magic-memory-copy` 和 `-max-wg`，加 `-report-l2-source -l2-source-tile-width=7` + `-benchmark probe -probe-all-gpms -probe-shared-ctx -probe-bytes 2097152 -probe-passes 2` | **少了 `-magic-memory-copy`**（T12 明确要求：不加它才会走 flush 路径）；统计开关 `-report-l2-source`；benchmark 参数 `-probe-*` |

判定标准：R1、R2 比较修复前后的 `metrics.csv`，要求**逐行完全一致**（包括 `kernel_time`、`total_time` 和所有 cache/DRAM/RDMA 计数）；R3 要求 48 个 GPM 都有指令、两遍都完成、心跳不报卡死。单元测试（`mgpusim/driver` 的 Go 测试）不涉及配置。

- 下一步：⏸ 等用户确认上面的参数和差异（尤其是 R2、R3 修复前那次要不要跑），然后再改 `memorycopy.go`。

### 2026-10-13 06:40 (T12) 修复 driver flush 竞争：实现、单元测试、回归（⏸ 等用户看结果）

- 用户确认了配置确认里的参数和差异，并同意追加 R2、R3 修复前也跑一次。
- **提交**（分支 `isca27/t12-driver-flush-fix`）：
  - `7d89a72`（修复前的基准）：探针新增 `-probe-shared-ctx`（`mgpusim/benchmarks/microbench/probe/spmd.go` 的 `NewSharedCtxSPMD`，`akkalat/benchmarkselection/probe.go`）；通用运行脚本 `akkalat/isca27_run.sh` + 参数工具 `akkalat/isca27_flags.py`（从 `build_common_flags` 生成参数，去掉的参数连同原因写进 `config_deviation`）。
  - `9c30a89`（修复）：只改了 `mgpusim/driver/memorycopy.go` 和它的测试。
- **修改内容**（`mgpusim/driver/memorycopy.go`）：
  - 新增 `completeIfDone`（第 299–323 行）：命令的请求全部返回时收尾：`IsRunning=false`；D2H 先把 `RawData` 解码到 `Dst`；`Dequeue`；`logCmdComplete`。收尾顺序和原来两处代码一致。
  - `processMemCopyH2DReturn`（第 254 行）、`processMemCopyD2HReturn`（第 272 行）改为调用它（原来各自写了一份收尾代码）。
  - `processFlushReturn`（第 277–297 行）改用 `cmd, cmdQueue := findCommandByReq(req)`，删除请求后也调用 `completeIfDone`（第 294 行）。**这就是修复本身**：原来 flush 回复最后到达时没人检查命令是否完成。
  - **没有改**：flush 的触发条件（`needFlushing`）、flush 发给全部 GPU（`sendFlushRequest`）、`-magic-memory-copy` 的路径（`memorycopyglobalstorage.go`）、`cyclesLeft`/`awaitingReqs`。
- **可选的第 2 步（`cyclesLeft` 按命令记录）：评估后不做。** akkalat 平台创建 driver 时没有设置拷贝延迟（`akkalat/baseline/runner/timingplatform.go:455` 只调用了 `driver.MakeBuilder()`，没有 `WithH2DCycles`/`WithD2HCycles`），所以 `cyclesPerH2D = cyclesPerD2H = 0`：每次拷贝都把共享的 `cyclesLeft` 设成 0，下一个 tick 就把所有积压的请求一起发出，多个 queue 之间**不会互相推迟**。只有 mgpusim 自带的 sample runner 设了 8500/14500 周期，我们不用它。
- **单元测试**（`mgpusim/driver/memorycopy_test.go`，新增 4 个）：H2D 和 D2H 各测“flush 回复晚于拷贝回复”和“flush 回复早于拷贝回复”两种顺序，命令都能出队，D2H 的数据正确解码。`go test ./driver/` 全部通过（29 个）。**用同样的测试跑修复前的 `memorycopy.go`，正好是两个“flush 最后返回”的测试失败**，说明测试确实测到了这个 bug。
- **回归和 P4 结果**（结果目录在本机 `remote/<服务器>/results/`，同时在服务器的 `~/exp/cupath/results/`）：

| 编号 | 实验 | 修复前（`7d89a72`） | 修复后（`9c30a89`） | 结论 |
|---|---|---|---|---|
| R1 | fir，论文配置，`-max-wg=4096` | total_time 26.662 µs | total_time 26.662 µs | ✅ 100477 个统计排序后**完全一致** |
| R1 | matrixmultiplication，论文配置，`-max-wg=4096` | total_time 378.553 µs | total_time 378.553 µs | ✅ 105952 个统计排序后**完全一致** |
| R2 | fir，去掉 `-magic-memory-copy`，`-max-wg=4096` | total_time 1499.347 µs | total_time 1499.347 µs | ✅ 100476 个统计排序后**完全一致** |
| R2 | matrixmultiplication，同上 | total_time 1869.706 µs | total_time 1869.706 µs | ✅ 105952 个统计排序后**完全一致** |
| R3 | P4，48 个 GPM 共用一个 Context，去掉 `-magic-memory-copy`，2 MB/GPM，2 遍 | **死锁**：运行 4.5 分钟后模拟时间停在 58669 ns、完成 640 个 WG，进程还活着但不再推进（CPU 占用降到很低）；心跳 3 分钟不推进被检测到，手动结束（exit 143），卡住时的心跳存在 `heartbeat_at_hang.json` | **完成**：48 个 GPM 都有指令，两遍都完成（114.5 / 114.6 µs），RDMA 事务 0，exit 0 | ✅ 论文网络参数下也能复现死锁，修复后消失 |

  exp_id：`1013_t12-R1-<benchmark>-maxwg4096-<pre|post>-fix_wafer48_paper_<commit>`、`1013_t12-R3-P4-sharedctx-<pre|post>-fix_wafer48_paper-nomagic_<commit>`；每个 meta.json 都有 `config_deviation`。
- 13:55 更新：R2 已全部跑完并通过（见上表），**T12 完成**。T12 的 10 个结果目录复制到了本机仓库根目录的 `results/1013_t12-*`。
- 处理网页上的“失败 / 异常中断”（用户要求）：yss3 上 3 条都已查明原因、无需重跑，把日志从 `~/exp/cupath/logs/` **移到** `logs/archived/`（没有删，附 `README.txt` 说明）：`p3b`（我启动命令里的循环变量 bug，已重跑）、`p4`（T11 第一次 P4，撞上 T12 修的死锁，`exp stop` 结束，已重跑）、`t12-r3-pre`（预期内：修复前复现死锁后手动结束，证据在结果目录）。结果目录都没有动。
- 下一步：⏸ 等用户看 T12 结果，确认后进入第 2 段（T13 阶段 1，基于 `isca27/t12-driver-flush-fix` 新建分支）。

### 2026-10-13 (T13) 阶段 1：参数定位（只读代码，没有运行、没有改代码；⏸ 等用户确认）

- 新建分支 `isca27/t13-mi300x`（基于 T12 的 `9c30a89`）。配置确认第 1 步：7 个文件和 95b4f46 的 blob 全部一致（同 T12 那次）。
- 符号：🟢 已有命令行参数，不用改代码；🟡 写死在代码里，需要改成预设参数；⚪ 不变。路径相对仓库根目录。

| 参数 | 旧配置（代码里的实际值） | 新配置目标 | 在哪里设置 | 要不要改代码 |
|---|---|---|---|---|
| 每 GPM 的 CU 数 | 8 个 SA × 4 CU = 32 | 19 × 2 = 38 | SA 数和每 SA 的 CU 数是平台默认值 `numSAPerGPU: 8`、`numCUPerSA: 4`（`akkalat/baseline/runner/timingplatform.go:100-101`），**没有命令行参数**；传给 GPU builder 在 `timingplatform.go:684-685` | 🟡 |
| driver 登记的 CU 数 | `CUCount: 32`（`timingplatform.go:801`） | 38 | 写死。它决定 unified GPU 切分 WG 的方式（`mgpusim/driver/driver.go:441-466`），必须和真实 CU 数一致 | 🟡（改成 SA 数 × 每 SA 的 CU 数） |
| L1 vector | 每 CU 16 KB，4 路，64B line，1 个 bank，延迟 10 | 32 KB，4 路 | `akkalat/baseline/runner/shaderarray.go:381-392`（`WithTotalByteSize(16 * mem.KB)` 写死） | 🟡 |
| L1 instruction | 每 SA 32 KB，4 路，每周期 4 个请求 | 每 2 个 CU 共享 64 KB，8 路 | `shaderarray.go:537-549`，写死；共享范围 = 一个 SA | 🟡（SA 改成 2 个 CU 后，共享范围自然变成 2 个 CU） |
| L1 scalar | 每 SA 16 KB，4 路 | 每 2 个 CU 共享 16 KB | `shaderarray.go:459-468`，写死 | ⚪（大小不变；共享范围随 SA 自动变成 2 个 CU） |
| LDS | 64 KB | 64 KB | `mgpusim/timing/cu/computeunit.go:142-144`，写死 | ⚪ |
| L2 容量 | 4 MB | 4 MB | `-l2-cache-size-mb`（默认 4，`flag.go:112`） | 🟢 |
| L2 分区数 | 4 个 × 1 MB（`-num-memory-banks=4`） | 16 个 × 256 KB | `-num-memory-banks`（`flag.go` 默认 16，`BASE_COMMON_FLAGS` 设成 4）；每个分区大小 = 容量 / 分区数（`r9nanobuilder.go:779`）。**这个参数同时决定 L2 分区数和 DRAM 控制器数，两者 1:1 相连**（`r9nanobuilder.go:593-606`） | 🟢（预设里设成 16） |
| L2 相联度 | 16 路 | 16 路 | `r9nanobuilder.go:784`，写死 | ⚪ |
| L2 吞吐 | 每分区每周期 16 个请求（4 × 16 × 64B ≈ 4 KB/周期） | 每 channel 2 个（16 × 2 × 64B = 2 KB/周期） | `WithNumReqPerCycle(16)`，`r9nanobuilder.go:787`，写死 | 🟡 |
| L2 MSHR | 每分区 64（每 GPM 256） | 每 channel 16（每 GPM 256） | `WithNumMSHREntry(64)`，`r9nanobuilder.go:786`，写死 | 🟡 |
| L2 目录延迟 | 10 | 不在目标表里 | `r9nanobuilder.go:788` | ⚪ |
| L2 交错粒度 | 128 B（`adaptivePairInterleavingLog2(2)` = 2^7，`timingplatform.go:360-368`、`:688-689`） | 不变 | 由 `-l2-adaptive-pair-region-lines`（默认 2）决定 | ⚪ |
| 内存 channel 数 | 4 | 16 | 同上：`-num-memory-banks` | 🟢 |
| HBM 带宽 | 理论 128 B/周期/GPM（见“需要用户决定”） | ≈ 315 B/周期/GPM，每 channel 19.7 | DRAM 时序全部写死：500 MHz、HBM 协议、256-bit、BL4、tCL 7、tRCD 7、tRP 7、tRAS 17 等（`r9nanobuilder.go:945-1000`）；带宽由时序推出（`akita/mem/dram/builder.go:435-436`、`:529-551`、`:781-793`） | 🟡（取法见“需要用户决定”） |
| HBM 容量 | 8 GB/GPM | 8 GB | 每控制器 = 8 GB / 控制器数（`r9nanobuilder.go:945-949`），16 个控制器时整除 | ⚪ |
| typed filter | 每 1 MB 分区 32K 槽 | 每 256 KB channel 8K 槽，每 GPM 总量不变 | 自动按分区大小计算（见“规格与代码的差异”）；格式、端口数、延迟来自 `DEFAULT_BENCHMARK_FLAGS` | ⚪（不用改） |
| `-prefetch-predictor-entries` | 256 | 256 | `DEFAULT_BENCHMARK_FLAGS` | ⚪ |
| 时钟 | 1 GHz | 1 GHz | `r9nanobuilder.go:122` | ⚪ |
| wafer 网络、RDMA、MMU、L1 MSHR | `BASE_COMMON_FLAGS` | 不变 | — | ⚪ |

**和 2 的幂相关的检查**（38 个 CU、19 个 SA、16 个 channel）：
- WG 分派：CP 的 partition 和 round-robin 两种算法都只用 `NumCU()` 做除法和取模（`mgpusim/timing/cp/internal/dispatching/partition.go:41-58`），没有 2 的幂假设。unified GPU 切分 WG 用 driver 登记的 `CUCount`（`driver.go:441-466`），只要把它改成 38 就没问题。
- SA 数 19：`buildSAs` 只是循环（`r9nanobuilder.go:772-775`），没有发现依赖 SA 数是 2 的幂的地方。
- L2 / DRAM 交错：`InterleavedLowModuleFinder` 按“地址 / 交错单位 mod 分区数”选分区，16 是 2 的幂，旧配置的 4 也是。
- typed filter：bucket 数总是向上取整到 2 的幂，对任何容量都成立。
- DRAM 内部地址映射：选方案 A 时，每控制器 512 MB、64-bit 设备宽度，rank 数 = 16（2 的幂）；选方案 B 时 rank 数 = 4。两种都是整数，`8 GB / 16` 也能整除（`r9nanobuilder.go:945-949` 的检查能通过）。
- `-max-wg`：按完成的 WG 总数截断（`akkalat/baseline/runner/wgtracer.go`），和 CU 数无关。但 CU 多了 19%，同样的 `-max-wg` 在新配置下对应的模拟时间会变短，阶段 4 解读截断结果时要注意。
- **风险**：akkalat 的部分分析脚本可能假设 L2 只有 4 个分区（例如按 `L2[0..3]` 汇总），阶段 2 实现时一起检查。

- 下一步：⏸ 等用户确认本表，并决定“需要用户决定”里的 3 条（旧 HBM 带宽的数字、新 HBM channel 的取法、阶段 2 能否修改那几个配置文件）。

### 2026-10-13 (T12) 补写文档（用户同意破例修改 isca27/ 下的文档）

- 用户要求把 T12 的完成情况写回相应文档、并补上说明，同意这次修改 STATUS.md 以外的文档。
- `isca27/README.md`：索引里 T12 一行的状态改为“`[已定]` · ✅ 完成（10-13，commit `9c30a89`，见 STATUS）”。其他行没有动。
- `isca27/tasks/T12_driver_flush_fix.md`：勾上 4 条验收标准，末尾新增“完成情况”一节（修改、测试、回归、R3、`cyclesLeft` 不做的原因）。
- 代码仓库新增 `akkalat/docs/ISCA27_TOOLS.md`：`isca27_run.sh` / `isca27_flags.py` 的用法、模拟器心跳、探针参数和 GPM 编号、SPMD 骨架、T12 修复说明、监控网页。commit `36bde23`（在 `isca27/t12-driver-flush-fix` 上，`isca27/t13-mi300x` 已快进到同一个 commit）。

### 2026-10-13 (T13) 阶段 2：实现预设 `-gpm-preset`（用户已确认阶段 1 和 3 条决定）

- 用户 10-13 确认：T12 结果；阶段 1 参数定位表；旧配置 HBM 带宽以阶段 3 实测为准；新 HBM channel 用方案 A（延迟不做每周期等效缩放）；同意按方案 A 修改配置文件。另要求核实 (a)(b) 两处论文与代码的不一致，结果已写进“规格与代码的差异”。
- **提交** `f444076`（分支 `isca27/t13-mi300x`，基于 `36bde23`）。
- **Go 侧**：
  - 新文件 `akkalat/baseline/runner/gpm_preset.go`：`gpmPreset` 结构和两个预设。`hpca`（默认）= 投稿版原来写死的值；`mi300x-eighth` = 19 × 2 个 CU、L1V 32 KB 4 路、L1I 64 KB 8 路（每 2 个 CU 共享）、L1S 16 KB、每 L2 channel 16 个 MSHR 和每周期 2 个请求、要求 `-num-memory-banks=16`；HBM channel 64-bit、BL8、1.23 GHz，时序按纳秒不变换算（四舍五入，最大误差 0.4 ns：tCL/tRCD/tRP 17、tRAS 42、tREFI 4797、tRRDS 5、tRRDL 7、tWTRS 7、tWTRL 10、tWR 20、tCCDS/L 2、tRTP 7、tPPD 5、tCWL 5）。理论峰值 19.68 B/ns/channel × 16 = **314.9 B/周期/GPM**（目标 315.5）。`lookupGPMPreset` 在预设和 `-num-memory-banks` 不一致时直接报错。
  - `flag.go:186`：新增 `-gpm-preset`（默认 `hpca`）。
  - `runner.go:388-394`：按 `-gpm-preset` 查预设、打印 `[Config] GPM preset=...`，传给平台。
  - `timingplatform.go`：`numSAPerGPU`/`numCUPerSA` 默认值改成取自 `hpcaGPMPreset`（`:101-103`）；新增 `WithGPMPreset`；传给 GPU builder（`:697`）；driver 登记的 `CUCount: 32` 改成 `b.numSAPerGPU * b.numCUPerSA`（`:812`）。
  - `r9nanobuilder.go`：新增 `gpmPreset` 字段和 `WithGPMPreset`，默认 `hpcaGPMPreset`；把预设传给 shader array（`:765`）；L2 的 `WithNumMSHREntry(64)`/`WithNumReqPerCycle(16)` 换成预设字段（`:796-797`）；DRAM 的结构和全部时序换成 `b.gpmPreset.DRAM` 的字段（`createDramControllerBuilder`，`:955` 起）。
  - `shaderarray.go`：新增 `l1Sizes` 字段和 `withGPMPreset`；L1V（`:396-400`）、L1S（`:476`）、L1I（`:548-550`）的大小和相联度换成预设字段。
  - 单元测试 `gpm_preset_test.go`：① hpca 预设和投稿版的常量逐项相等（32 个 CU、L1 大小、L2 64/16、DRAM 全部 17 个时序）；② mi300x 的 CU 数、MSHR 总数 256、L2 2 KB/周期、DRAM 访问单位 64 B、理论峰值在 310–320 B/ns；③ 预设和 `-num-memory-banks` 不一致时报错；④ **实际构建整个 48-GPM 平台**：hpca 建出 32 个 CU + 4 个 L2/4 个内存控制器，mi300x 建出 38 个 CU + 16 个 L2/16 个内存控制器。全部通过。
- **Python 侧**（让论文原有的脚本可以选预设）：
  - `runall2_constants.py`：新增 `MI300X_EIGHTH_FLAGS = ["-gpm-preset=mi300x-eighth", "-num-memory-banks=16"]`、`GPM_PRESET_FLAGS`、`DEFAULT_GPM_PRESET = "hpca"`；`BASE_COMMON_FLAGS` 没有动。
  - `runall2_config.py`：新增 `apply_gpm_preset`（同名参数替换，其余追加），`build_common_flags` 先套用预设。
  - `runall2_args.py`、`run_cupath_llm.py`、`run_cupath_sensitivity.py`：新增 `--gpm-preset`（默认 `hpca`），各自的 `common_flags` 套用预设。
  - **默认参数完全不变**：改动前后，runall2（3 种参数组合）、run_cupath_llm、run_cupath_sensitivity 生成的参数列表逐字相同。
  - `isca27_flags.py`：预设带来的差异（`-gpm-preset=mi300x-eighth`、`-num-memory-banks=4 → 16`）在 `config_deviation` 里写明原因；`isca27_run.sh`：meta.json 新增 `gpm_preset` 和 `notes`（mi300x 时写明“带宽按 2.1 GHz 每周期等效、延迟保持纳秒值不缩放、HBM channel 参数”）。
  - 已有测试：`test_runall2_config`、`test_run_cupath_llm`、`test_runall2_process`、`test_run_cupath_sensitivity` 全部通过；`test_cupath_source_invariants` 里两个检查“源码里写着 64/16/256/BL4”的测试改成检查 hpca 预设里的同样数值，并检查 builder 用的是预设字段。剩下一个失败（`test_paper_uses_current_cupath_story_and_unambiguous_l2_name`，论文文字相关）**在改动前的 HEAD 上就失败**，和本次无关。
- **分析脚本里“L2 只有 4 个分区”的假设**（用户要求检查）：
  - ⚠️ `run_cupath_sensitivity.py:51` 写死 `FILTERS_PER_GPM = 4`，用来把“每 GPM 的 bucket 数”换算成每个分区的 `-typed-filter-capacity`。新配置下会把 filter 总容量放大 4 倍。**已修**：改成从预设生成的 `-num-memory-banks` 读出（hpca 仍是 4，manifest 不变，旧 campaign 可以继续 resume；mi300x 是 16）。参考点 16K bucket/GPM 在 mi300x 下 = 每 channel 1K bucket。
  - ⚠️ `cost/run_full_cacti.py:42-65` 写死“4 个 1 MiB L2 分区、4 个 filter、每分区 8K bucket”。只影响面积估算，新配置的面积要另算（阶段 4 之后再做，需要时我再提出）。
  - 其余脚本：`analyze_hidden_latency.py`、`analyze_pipeline_costs.py`、`plot_llm_like_bottleneck_snapshot.py`、`t11_analyze_probe.py` 用 `".L2[" in where` 通用匹配，不依赖分区数；`plot_cupath_work_reduction.py` 的 `range(4)` 是 4 个统计指标；`analyze_observations.py` 里的 `L2[0]` 是测试用的样例数据。
  - 遗留的不一致（不是本次引入）：主实验 filter 用代码默认的“2 × 块数”（hpca 每 GPM 32K bucket），敏感性实验的参考点是 16K bucket/GPM。阶段 4 重跑时这个差异仍然存在，需要时在“需要用户决定”里再提。
- **CONFIG.md 第 1 节的 blob 都变了**（用户同意的修改）：`runall2_constants.py` c46181e7ddbc → 0a6b0c4f2f5c；`runall2_config.py` 073257215378 → ea22add79a66；`flag.go` 142c66f8fec7 → 9ca510de581f；`shaderarray.go` 0cad15f3f860 → 743e0ae29af1；`r9nanobuilder.go` be11d0d9cd45 → 6c7c78ea4916；`timingplatform.go` 7f4c214b78e6 → c8636d99cc06；`runner.go` c84865b80dcc → 378f7313f8d4。CONFIG.md 我没有改，需要 Cowork 侧更新快照。
- **旧配置回归**（运行中）：参数和 T12 R1 完全相同（论文配置 + `-max-wg=4096`，用户在 T12 已确认，没有新的差异），fir 和 matrixmultiplication 用 `f444076` 在 yss2 上跑。修改前的基准复用 T12 R1 修复后的结果（`9c30a89`，同一台 yss2；`9c30a89` 到 `36bde23` 之间只加了一份文档，模拟器代码相同）。比较方法：`total_time` 单独比较，全部统计去掉行号排序后逐项比较。

### 2026-10-13 (同步/T13) 按 P06：同步、建立文档仓库、结果索引；推送待用户放行

- **同步**：读了 `SYNC.md` 全文、README 的“同步与备份”、`tasks/T12_driver_flush_fix.md` 新增的“10-13 用户决定：所有运行都保留 `-magic-memory-copy`”及 Cowork 加注、`site/`（8 个页面）、`prompts/P05`、`P06`。理解到的规格变化：① **以后所有运行都保留 `-magic-memory-copy`**；② T12 取消 R2（已经跑完的 R2/R3 只用于 T12 验证，不进论文）；③ **T12 的 R3 改为带 magic copy、用论文配置、修复后跑一次**（新要求，待配置确认后运行，见“需要用户决定”）；④ 每个停点要更新 STATUS → 追加 SYNC 日志 → 推送两个仓库。
- **P06 第二部分**（3 条答复、核实 (a)(b)）在 P06 到达之前已经按用户在对话里的相同答复完成，见“需要用户决定”里的已决定条目和“规格与代码的差异”里的 (a)(b)；补了一句 168 KiB 的来源。
- **P06 第三部分**：工作区里的 `gpm_preset.go`、`shaderarray.go` 等改动已经提交为 `f444076`，对照答复检查过：HBM 方案 A、延迟不缩放（写进了 STATUS 和 meta 的 `notes`）、预设集中在 `gpm_preset.go`、原文件只做常量替换（另加了 `-gpm-preset` 开关和 runner 的接线），没有需要改的地方。
- **文档仓库**：`~/Desktop/CuPath/isca27/` 已 `git init`（分支 `main`），`.gitignore` 忽略 `.DS_Store` 等；首次提交 `fb0eacd`（`cowork: initial import of isca27 docs`）。**还没有 remote**（见“需要用户决定”）。
- **结果索引**：`results_index/` 收录 T11 最终 16 个（`*_f7b9eef`）和 T12 的 10 个实验的 `meta.json`、`summary.csv`（T12 R3 修复前那个还附了 `heartbeat_at_hang.json`），加了一个 `README.md` 说明哪些数字作废；提交 `3332d37`。T11 修复死锁前的几轮已作废，没有收录。
- **代码仓库推送**：`isca27/t11-probe-placement-comm`（`d6b765c`）、`isca27/t12-driver-flush-fix`（`36bde23`）、`isca27/t13-mi300x`（`f444076`）都已提交、工作区干净，但 `git push` 被权限规则拦下，**还没有推送**。

### 2026-10-13 (T13) 配置文件 `-config`；不再用 `-disable-servers`

- **用户决定**：配置写进专门的配置文件，命令行不再写一长串；可以保存不同的配置、读取不同的文件做敏感性实验；不用 `-disable-servers`。
- **核实 `-disable-servers`**：它只关掉两个后台网页服务（Go pprof、akita 监控页面），都监听随机端口（`:0`，`runner.go:98-110`、`akita/monitoring/monitor.go` 的 `StartServer`），多个实验同时跑也不冲突。历史上的论文实验两种都有：带它的有 baseline-complete-observation、m1-m2-m3-rerun、两次 cupath-sensitivity、baseline-library；不带的有 sampled-validation、cupath-llm、filter-authoritative-validation。
- **实现**（commit `1b1e829`，已推送 `isca27/t13-mi300x`）：
  - `akkalat/baseline/runner/config_file.go`：`-config a.json,b.json`（键 = 参数名；后面的文件覆盖前面的；`"extends"` 继承另一个文件；`_` 开头的键是注释；参数名写错直接报错；命令行显式写的参数覆盖所有文件，并打印出来）；`-config-out` 写出每个参数的实际值和来源（默认值 / 哪个文件 / 命令行），其中的 `flags` 可以直接当配置文件用。`flag.go` 的 `ParseFlag` 第一行调用它。**二进制的默认值没有改。**
  - `akkalat/configs/hpca.json`（HPCA 投稿版 = `BASE_COMMON_FLAGS` + `-mmutlb-lookup-latency=80`）、`akkalat/configs/mi300x_eighth.json`（`extends` hpca，只改 `gpm-preset` 和 `num-memory-banks=16`）。两个都保留 `-magic-memory-copy`，都不含 `-disable-servers`。
  - `akkalat/isca27_config.py`：读取/合并配置文件，生成 `config_deviation`（和 `configs/hpca.json` 比）；`isca27_run.sh` 改成 `isca27_run.sh <exp_id> <commit> <task> <配置名> [benchmark 参数]`，配置名如 `hpca`、`mi300x_eighth,l2_8mb`；结果目录多一个 `config_used.json`。
  - 测试：`test_isca27_configs.py`（`hpca.json` ≡ `BASE_COMMON_FLAGS`；`mi300x_eighth.json` ≡ 预设参数；所有配置保留 magic copy、不含 disable-servers）；`runner/config_file_test.go`（继承、循环检测、仓库里的配置只用已有参数）。本机实测了单文件、继承、三层叠加、命令行覆盖、拼写错误。已有测试全部通过。
  - 论文原来的 runall2 / run_cupath_llm / run_cupath_sensitivity 不变，照旧显式传参。
- **用户确认后启动的两组**（yss3，`1b1e829`）：
  - 配置文件验证：`isca27_run.sh 1013_t13-cfgcheck-fir-maxwg4096-servers-on_wafer48_hpca_1b1e829 1b1e829 T13 hpca -benchmark fir -max-wg 4096`。和之前“写全参数 + `-disable-servers`”的 fir（T12 R1 / T13 回归）比较，统计必须完全一致，同时证明“配置文件 = 写全参数”和“开着服务不影响结果”。
  - T12 R3（带 magic copy）：`isca27_run.sh 1013_t12-R3-P4-sharedctx-magic_wafer48_hpca_1b1e829 1b1e829 T12 hpca -report-l2-source -benchmark probe -probe-all-gpms -probe-shared-ctx -probe-bytes 2097152 -probe-passes 2`。
  - 两个都已确认读到了 `hpca.json`，`disable-servers=false`、`magic-memory-copy=true`、`bandwidth=48`。
- 旧配置回归：fir 已通过（见阶段 2 日志）；matrixmultiplication 还在跑。

### 2026-10-13 15:25 (T13) 阶段 2 完成（⏸ 等用户确认）

| 检查 | 修改前（基准） | 修改后 | 结论 |
|---|---|---|---|
| 旧配置回归 fir（`-max-wg=4096`） | T12 R1 `9c30a89`（yss2）：total_time 26,662 ns | `f444076`（yss2，默认预设 hpca）：26,662 ns | ✅ 100,477 项统计排序后完全一致 |
| 旧配置回归 matrixmultiplication（`-max-wg=4096`） | T12 R1 `9c30a89`：378,553 ns | `f444076`：378,553 ns | ✅ 105,952 项统计排序后完全一致 |
| 配置文件 + 开着服务 = 写全参数 + `-disable-servers` | T13 回归 fir（写全参数，`-disable-servers`，yss2） | `1b1e829`，`-config hpca.json`，不带 `-disable-servers`（yss3） | ✅ 100,477 项完全一致；同时说明两台服务器的结果可以完全复现 |
| T12 R3（带 magic copy，48 个 GPM 共用一个 Context，按 T12 文档的新要求） | — | `1b1e829`，`-config hpca.json`（yss3） | ✅ 48 个 GPM 都有指令，两遍都完成（96 个 kernel），第 1 遍 37,602 ns、第 2 遍 6,803 ns，exit 0，没有卡死 |

- 结果目录：本机 `results/1013_t13-*`、`results/1013_t12-R3-P4-sharedctx-magic_*`；`results_index/` 已收录 meta.json、summary.csv、config_used.json。
- 阶段 2 的验收：`-gpm-preset` 默认值等于旧配置、旧配置回归完全一致 ✅；分析脚本里“L2 只有 4 个分区”的假设已检查（修了 `run_cupath_sensitivity.py`，`cost/run_full_cacti.py` 待以后另算面积）✅；所有运行都保留 `-magic-memory-copy` ✅。
- 另外（用户要求）：监控网页 `exp web` 新增单个结果的配置和统计汇总、2–4 个结果的对比（关键结果、配置差异、统计是否完全一致及差异列表）。这是本机工具（`~/.local/share/exp/`），不在代码仓库里。
- **下一步：⏸ 等用户确认阶段 2，然后进入阶段 3（校准）。** 阶段 3 运行前会按 CONFIG.md 第 3 节给出完整参数（现在是配置文件名 + benchmark 参数）和差异。

### 2026-10-13 (T13 阶段 3 + T14 第一档) 同步、微基准实现、配置确认（⏸ 等用户确认）

- **同步**：读了 SYNC 日志里 Cowork 的 2 条；`git diff` 读了 SYNC.md（第 1、3 节改为推送 `isca27/docs`）、CONFIG.md（10-13 更新：配置文件、基准 `1b1e829` 和 blob 表、两条检查方法）、00_background.md（投稿版 Table II：HBM 0.375 TB/s、4K bucket、168 KiB、4.70%）、README.md、T13 末尾补充、新文件 T14、site/、P07。以 `cowork: ...` 提交为 `eecffb4`。文档分支 `isca27/docs` 用户已推过一次（`c44d08f`），现已推到 `eecffb4`。
- **理解到的规格变化**：配置确认改为“配置文件名 + `-config-out` + 和 hpca.json 的差异”；T14 第一档 7 项和 T13 阶段 3 共用运行，结论分开写；测量方法按 T14 的规则。
- **微基准 `platbench`**（commit `0e38983`，已推送 `isca27/t13-mi300x`）：`mgpusim/benchmarks/microbench/platbench/`，`-benchmark platbench -plat-test <项>`；7 项的方法和参数写在 `akkalat/docs/ISCA27_TOOLS.md` 第 2.6 节。要点：
  - 指针追逐：1 个线程、随机指针环（每个 cache line 一个节点），A 组页内集中、B 组每步跨页；≤ 8 MB 先完整走一遍热身；然后各发一次 0 步和 N 步的 kernel，延迟 = (t_N − t_0)/N（抵消 kernel 启动开销）。已确认 kernel 之间 L1 不会被清空、单个 WG 每次都落在同一个 CU（partition 分派器），所以 L1 热身有效（冒烟：16 KB ≈ 38–40 ns，1 MB ≈ 54 ns）。
  - 带宽：每 CU 8 个 WG；启动“CU 数 × 8”个 WG，编号超出前 k 个 CU 的直接退出，从而只让 k 个 CU 干活。≥ 16 MB 视为 HBM（只读一遍），< 16 MB 视为 L2（热身后重复 4 遍）。
  - **遇到模拟器不支持的指令**：`s_cmp_le_u32`（SOPC opcode 9，`mgpusim/emu/alusopc.go:32`）。没有改模拟器，而是把我自己 kernel 里的计数和比较改成有符号 int，重新编译后 hsaco 里已经没有这条指令。
  - 每个点输出一行 `PLATCSV`，`isca27_run.sh` 抽成 `platform.csv`（统一列名，GPM 同时给 driver ID 和 `GPU[t]`）。
  - mesh 路由是先 Y 后 X（`akita/noc/networking/mesh/mesh_routing_table.go:36-43`），中心 tile 上是一个交换机（driver/IOMMU 挂在上面），路由可以穿过它，不会绕路。**模拟器没有链路利用率的统计**，第 6 项的每条链路利用率只能按 YX 路由和每对的流量推算（标注为推算值）。
  - `gpm_preset.go` 新增了 `CUsPerGPM()`（不改任何配置数值），所以它的 blob 变成 `4a149c51b45a`，和 CONFIG.md 表里的 `487bbd5e578d` 不同，需要 Cowork 更新表格。
- **冒烟测试**（本机，hpca 为主，只用于检查能跑和估算时间，**不作为结果**）：7 项都能跑。看到的苗头：hpca HBM 读满 CU 约 57 B/周期（代码推算 64 B 请求下的有效峰值约 64）；mi300x HBM 约 236 B/周期（目标 315，−25%）；L2 读 hpca 414、mi300x 480 B/周期（目标 2 KB/周期，−77%，可能受每个 L1 只有 16 个在途请求限制）；单对远程读 64 个 WG 只有约 26 B/周期，正好是 RDMA 64 个 outstanding × 64 B ÷ 约 200 ns。正式结果出来后按规则报告。

**配置确认**

| 配置 | `-config-out` 的 flags（13 个） | 和 `hpca.json` 的差异 |
|---|---|---|
| `hpca` | bandwidth=48 gpm-preset=hpca l1v-max-concurrent-trans=16 l1v-mshr-entries=16 magic-memory-copy=true mmutlb-lookup-latency=80 num-memory-banks=4 rdma-max-outstanding=64 rdma-pipeline-latency=10 rdma-pipeline-width=8 report-all=true switch-latency=32 timing=true | 无 |
| `mi300x_eighth` | 同上，但 gpm-preset=mi300x-eighth、num-memory-banks=16 | `-gpm-preset` hpca → mi300x-eighth；`-num-memory-banks` 4 → 16 |

两套都保留 `-magic-memory-copy`，都不带 `-disable-servers`。其余命令行参数都是 benchmark 参数（`-benchmark platbench -plat-*`）。

**运行计划**（每套配置 29 个进程；hpca 放 yss2、mi300x 放 yss3，各自并行；每个进程的模拟时间按冒烟测试的速度估算，mi300x 约慢 1.3–1.8 倍）

| 项 | 进程（每套配置） | 规模 | 估算（hpca / mi300x） |
|---|---|---|---|
| 1 延迟（本地） | 2：≤ 8 MB、≥ 16 MB | 4K,8K,16K,24K,32K,48K,64K,128K,256K,512K,1M,2M,3M,4M,6M,8M（热身）；16M,32M,64M,128M,256M,512M,1G（不热身）；A、B 两组；20000 步 | 5 / 8 分钟；10 / 15 分钟（1 GB 那一点的主机内存约 1.2 GB） |
| 1 延迟（远程） | 1 | 发起方 GPM 1，数据在 driver 2/4/7/48（1/3/6/12 跳）；1 MB（热身）、64 MB；A、B；20000 步 | 15 / 25 分钟 |
| 2 带宽—CU 数 | 3：read、write、copy | 1 MB（L2，热身后 4 遍）、16 MB（HBM，一遍）；CU 1,2,4,8,16,24,32,(38) | 每个 8 / 12 分钟 |
| 3 跳数矩阵 | 7 | 1 MB、10000 步；GPM 1 → 全部 47；全部 47 → GPM 1；另 5 个起点（角 7/43/48、中心左邻 24、上边中点 4）各到全部 47 个。共 329 对 | 每个约 40 / 60 分钟 ⚠️（最慢的项，mi300x 可能接近 1 小时） |
| 4 单对远程带宽 | 4 | 1 跳（1→2）、6 跳（1→7）× 1 MB（远端 L2）、16 MB（远端 HBM）；WG 1,2,4,…,256 | 每个 15 / 20 分钟 |
| 5 incast | 6（每个 k 一个） | owner = driver 25（中心右邻）；k = 1,2,4,8,16,47；shared、disjoint；1 MB | k=47 约 20 / 30 分钟，其余更短 |
| 6 通信模式 | 5 | all-to-all 每个来源 16 KB（256 KB 时总流量 577 MB，要 3 小时，所以缩小）；neighbor、transpose、ring、snake 每个来源 256 KB | 每个 10 / 15 分钟 |
| 7 kernel 开销 | 1 | 空 kernel：单 GPM、48 个 GPM 各一个 queue、统一 GPU（1 个 GPM / 全部 GPM），连续 10 个 | 1 / 1 分钟 |

- 没有单个测量点超过 1 小时；单个进程最长的是 mi300x 的跳数矩阵（约 1 小时）。总共约 1 小时墙钟时间（两台服务器并行）。
- exp_id：`1013_t14-<项>-<部分>_wafer48_<配置>_0e38983`。每个进程一个 tmux 会话（可以在监控网页上逐个看到并用心跳检测卡死）；为此会给 `exp` 加一个“只同步一次、启动多个任务”的用法。
- 完成后：T13 阶段 3 的校准结论写进 T13 部分；T14 的平台数据摘要表（两套配置并排）写进 STATUS；CSV 放进 `results_index/platform/`。偏差超过 10% 按规则停下来报告。

### 2026-10-13 17:00 (T13 阶段 3 + T14 第一档) 运行完成、校准结论（⏸ 偏差超过 10%，等用户决定）

- **运行**：用户确认后在 `0e38983` 启动，每套配置 29 个进程（hpca 在 yss2，mi300x 在 yss3），全部 exit 0。
- **发现并修正微基准 bug**：mi300x 的延迟阶梯在 16 KB 之后就从 38 ns 跳到 54 ns，可它的 L1V 是 32 KB。先用一个临时测试（反射读出建好的平台）确认预设生效（hpca L1V 16 KB、mi300x 32 KB，阶段 2 没有问题）；再反汇编发现 `chase` 内核的 `p = next[p]` 被编成了 `s_load_dword`（单线程时地址在 wave 内一致），测的是两套配置都一样的 16 KB **L1 标量缓存**。修正：起点加上 `get_local_id(0)`，反汇编确认只剩 `flat_load_dword`（`ca7ea0b`）。依赖 `chase` 的 10 项（lat-small/large/remote、7 个 hop）× 2 套配置停掉后用 `ca7ea0b` 重跑（exp_id 以 `_ca7ea0b` 结尾）。旧的 `0e38983` 延迟/跳数结果**作废**：已跑完的 lat-small、lat-large 保留在 `results/`，没有放进 `results_index/`；被停掉的 16 个只有部分输出，也保留。其他 19 项不用 `chase`（用 `flat_load_dwordx4`），结果有效。修正后 hpca 拐点在 16 KB、mi300x 在 32 KB，和容量一致。
- **结果**：见“结果摘要”的 T13 阶段 3 校准表和 T14 平台数据摘要表。`results_index/` 新增 58 个 `1013_t14-*` 目录（meta.json、summary.csv、config_used.json、platform.csv）；`results_index/platform/` 是汇总：`latency.csv`、`bandwidth.csv`、`hopmatrix.csv`、`remotebw.csv`、`incast.csv`、`patterns.csv`、`kernel.csv`、`pattern_links.csv`（每条链路的推算负载）、`summary.json`。汇总脚本 `akkalat/t14_analyze.py`（`a4738c8`）。
- **校准偏差超过 10%**：mi300x HBM 读 −23%，L2 读/写 −69%（两套配置都一样）。**按规则停下来，没有调任何参数**，见“需要用户决定”。**T13 阶段 4（全量重跑）没有开始。**
- 给 Cowork：这次没有改动模拟器和配置；`gpm_preset.go` 的 blob 仍是 `4a149c51b45a`（上一条已说明，CONFIG.md 的表需要更新）。

### 2026-10-13 19:15 (T13 阶段 4) 配置确认、启动全量重跑

- **用户 10-13 决定**：先用 mi300x 配置把论文实验跑一遍；四部分都跑，按 ① T11 探针 → ② 主结果 + filter 统计 → ④ LLM → ③ 敏感性 的顺序；**hpca 用当前代码重跑 ①②** 作为新旧对照；两台服务器各最多 16 个同时运行，留 30 GB 内存。用户确认了下面的配置和计划（“OK，跑实验吧”）。
- **用的脚本**（都是论文原来的，只加 `--gpm-preset`）：② `runall2.py --remote-ablation --configs=all`（70 个）和 `--configs=complete --extra-benchmark-flags=-typed-filter-authoritative-audit=true`（14 个，Fig 13 用）；④ `CuPathLLM.py --configs=baseline,complete --cu-per-gpu=38`（44 个）；③ `run_cupath_sensitivity.py --include-reference`（252 个，拆成 filter 和 l2、row 两半）。① 用 `isca27_run.sh` 加配置文件，参数和 10-13 的 16 个探针相同，另加 `-l2-source-tile-width=7`。
- **配置确认**：三个原脚本的 `--dry-run` 命令逐个核对，和 `hpca.json` 相比只差 `-gpm-preset`（mi300x-eighth）和 `-num-memory-banks`（16）；hpca 重跑没有差异。五种机制组合和论文的 Complete / baseline / Local Pairing / Remote Aggregation / Remote Reuse 一致；都保留 `-magic-memory-copy`，都不带 `-disable-servers`；截断 `-max-wg=78600`；敏感性和 LLM 按论文原来的做法用抽样模拟；敏感性在 mi300x 下的 filter 点自动换算成每 GPM 4K/8K/16K/32K bucket。
- **代码**：`71b08b0` 敏感性脚本去掉写死的 `-disable-servers`（单元测试通过）；`71b08b0`/`fd42936`/`9220740` 新增 `akkalat/isca27_stage4.sh`（每台服务器一份计划，依次调用原脚本；原脚本不改）。
- **计划**：yss2：hpca ① → hpca ②（主结果 12 个位置 + filter 统计 4 个位置并行）→ ④ LLM → ③ filter 部分（112 个）；yss3：mi300x ① → mi300x ② → ③ L2 和 row 部分（140 个）。各部分内部按历史运行时间“长的先跑”（kmeans 单个就要 18–25 小时，放在最后会拖一整天）。结果在 `results/1013_t13s4-<部分>_wafer48_<配置>_9220740/`。估计每台约 2.5 天。
- **启动**：10-13 19:14 UTC，`exp` 会话 `t13s4-yss2`、`t13s4-yss3`（监控网页上的组 `t13-stage4`）。同一会话里同时有多个模拟，所以关掉了共用的心跳文件，改由本机的监视脚本看阶段日志、报错和内存。
- **注意**：mi300x 的结果要带“L2 带宽只有目标约 1/3，原因未查清”的说明；敏感性实验存档的结果和论文正文的数字对不上（见子代理核对），这次重跑没有可直接比对的旧数据。

### 2026-10-13 19:40 (T13 阶段 4) 改成两台服务器各 16 个位置一直占满；修复启动失败后重启

- **用户指出**：每台服务器可以一直同时跑 16 个。原来的脚本是“一个部分全部结束才开始下一个”，T11 探针只剩 P4 时每台只有 1 个模拟在跑，②④ 收尾时也会空出位置。
- **改法**（`8b4040e`）：新增 `akkalat/slot_pool.py`，每台服务器 16 个共享位置（文件锁，进程退出自动释放）。`runall2.run_all_experiments`（三个原脚本共用的调度函数）在设置了 `EXP_SLOT_DIR` 时，每启动一个模拟先拿一个位置；空出的位置给还有任务没启动、优先级最高的部分（① 探针 1、② 主结果和 filter 统计 2、④ LLM 4、③ 敏感性 5）。不设环境变量时行为完全不变。内存检查（可用内存高于 30 GB 才启动）照旧。单元测试 5 个（位置上限、优先级、过期登记、调度器峰值并发、不设变量时不变）。`isca27_stage4.sh` 改成所有部分同时启动、按优先级排队。
- **启动失败和修复**（`260ce73`）：三个原脚本写 `EXPERIMENT_METADATA.json` 时要执行 `git rev-parse`，而服务器上同步的代码没有 `.git`，所以所有 campaign 在启动第一个模拟之前就退出了（T11 探针不受影响）。修复：git 失败且设置了 `EXP_GIT_REVISION` 时，记录调用方给的 commit（本机检查过工作区干净），并在 metadata 里标注 `git_source` 为调用方；新增单元测试。在 yss3 上用极小规模（`-max-wg=64`）冒烟测试了三个脚本，都能写完 metadata 并开始模拟。
- 停掉的会话：`t13s4-*`（`9220740`，只跑了 T11 探针）、`t13s4b-*`（`8b4040e`，campaign 启动失败）。它们的结果目录保留，不收录。
- **重启**：10-13 19:36 UTC，会话 `t13s4c-yss2`、`t13s4c-yss3`，结果目录 `results/1013_t13s4-<部分>_wafer48_<配置>_260ce73/`。两台服务器冻结的二进制都确认支持 `-gpm-preset`。

### 2026-10-13 21:35 (T13 阶段 4) 内存是瓶颈：两台服务器都加了内存守护进程（暂停而不是杀掉）

- **现象**：模拟启动后内存一直涨，每个大约每 10 分钟多 1–2 GB（Complete 配置和 filter 统计涨得最快）；一小时后单个就有 15–20 GB。16 个同时跑超过了服务器内存（yss3 235 GB；yss2 251 GB，其中约 23 GB 是其他用户的进程）。原调度器只在启动新模拟前检查内存，管不了已经在跑的。
- **处理**：
  1. yss3 可用内存降到 26 GB、每分钟少约 1 GB 时，停掉了两个刚启动约 15 分钟的模拟：mi300x 主结果 Complete 配置的 `fft` 和 `floydwarshall`（在 `runall2` 里记为失败，主结果跑完后用 `--rerun-missing` 补跑）。
  2. 两台服务器各起一个内存守护进程（`~/isca27_memgov.py`，`exp` 会话 `memgov4-*`）：可用内存低于 20 GB 且每分钟下降超过 0.5 GB（或低于 10 GB）时，暂停（SIGSTOP）涨得最快的模拟；高于 35 GB 时按先后恢复（SIGCONT）。有模拟暂停时，它会占住空出来的共享位置，让腾出的内存先给暂停的模拟用，而不是启动新的。**暂停不改变模拟结果**（模拟时间和墙钟时间无关），也不丢已经算好的部分；原脚本没有超时。
  3. yss3 上临时占住 2 个位置的进程已经停掉（改由守护进程管理）。
- **当前**（21:35）：yss2 共 14 个模拟，其中 3 个暂停；yss3 共 13 个，其中 3 个暂停；已经有模拟跑完并开始恢复暂停的。
- **影响**：按两台服务器的内存，这批模拟实际能同时跑约 9–12 个，**阶段 4 会比原估计的 2.5 天更久**；等有更多模拟跑完再按实际速度重新估算。

### 2026-10-14 00:40 (T13 阶段 4) yss3 内存差点耗尽；停掉 7 个刚启动的模拟；监控和守护进程的改进

- **经过**：00:34 我重启内存守护进程时，有几秒钟没有进程占住空位，加上旧版在可用内存高于 50 GB 时会一次放开所有空位，`runall2` 在 yss3 上同时启动了 9 个新模拟，新模拟起步时每分钟涨 4–5 GB，可用内存降到 1.6 GB（swap 也在用）。暂停不能还回内存，所以停掉（kill）了其中 7 个刚跑了约 2 分钟的模拟，可用内存回到 52 GB。
- **需要补跑**（mi300x 主结果 `1013_t13s4-main_wafer48_mi300x_260ce73`，主结果跑完后用 `runall2.py --rerun-missing` 补）：Complete 的 `fft`、`floydwarshall`（10-13 21:00 停掉的）；baseline 的 `matrixmultiplication`、`aes`、`pagerank`、`fir`、`fft`、`im2col`、`matrixtranspose`。另外，mi300x filter 统计（`1013_t13s4-filter-audit_wafer48_mi300x_260ce73`）的 Complete `kmeans` 在 00:40:31 被系统 OOM killer 杀掉（返回码 −9，已经跑了 5 小时 4 分），也要补跑。
- **守护进程现在的规则**（`memgov10-*`，参数 20 25 50 15）：可用内存低于 20 GB 且还在跑的模拟一分钟内一共涨了 0.2 GB 以上（低于 15 GB 时 0.05 GB）就暂停涨得最快的；高于 25 GB，或者一个模拟都没在跑时，按顺序恢复；低于 50 GB 时占住所有空位，不让启动新模拟，高于 50 GB 时每分钟最多放开一个。以后重启守护进程前先用临时进程占住空位。
- **监控网页**（用户要求）：一个会话里有多个模拟时，列出每个模拟（campaign、benchmark、配置、运行或暂停、时长、CPU、内存）；新增 “Campaign 进度”（已结束 / 总数 / 失败）。本机的监视改为没有时限的后台任务，一直跑到阶段 4 结束。
- **进度**（00:40）：hpca 主结果 21/70；mi300x 主结果 8/70；mi300x filter 统计 10/14；其余部分还没开始或刚开始。

### 2026-10-14 01:25 (T13 阶段 4) yss2 有硬件内存错误

- hpca 主结果的 `matrixtranspose`（m1 = Local Pairing，`1013_t13s4-main_wafer48_hpca_260ce73`）在 01:18 崩溃：返回码 2，`fatal error: fault`，`SIGBUS code=0x4`（`BUS_MCEERR_AR`，硬件内存错误被 machine check 捕获），崩在 `akita/mem/mem/protocol.go:302` `WriteReq.Meta()`（只是读字段）。已经跑了 1 小时 26 分。
- yss2 的 EDAC 计数：`/sys/devices/system/edac/mc/mc1/ue_count = 4`（不可纠正错误 4 次），可纠正错误 0。没有权限读内核日志，定位不到具体内存条。
- 判断：yss2 有硬件内存故障。这类错误是硬件检测到后终止进程，不会悄悄写坏结果；跑完的结果可信，崩掉的补跑。内存用得越满越可能再碰到。
- 加入补跑清单：hpca 主结果 m1 的 `matrixtranspose`。
- **需要用户决定或转告**：建议通知 yss2 管理员检查或更换 mc1 下的内存。

### 2026-10-14 02:30 (T13 阶段 4) 内存导致基本串行；用户决定不动；排查内存增长

- **状况**：两台服务器都只剩 1 个模拟在算（yss2 暂停 12 个、yss3 暂停 11 个），暂停的模拟一直占着内存（yss2 约 210 GB）。模拟的内存随进度一直涨，例如 hpca kmeans Complete 跑了 7.3 CPU 小时已经 27 GB。进度：hpca 主结果 33/70（失败 1）；mi300x 主结果 23/70（失败 9，都在补跑清单里）；mi300x filter 统计 13/14（失败 1）；其余部分没开始。
- **用户 10-14 决定**：不停掉已经开始的模拟（方案 B，慢慢跑）；同时排查模拟器内存为什么一直涨（只读代码，本机小规模测量；要改代码先问用户）。排查交给子代理，结果出来后写在这里。
- **yss2 硬件内存错误的验证**：在 yss3 上用同一个冻结二进制（`a5047fc8…`）、同样的参数重跑崩溃的 hpca m1 `matrixtranspose`（结果在 `~/isca27_diag/`，不进正式结果）。第一次启动时 yss3 可用内存降到 6.6 GB，跑了约 1 分钟就停掉了；改为等 yss3 没有暂停的模拟、可用内存连续 3 分钟高于 45 GB 时自动开始（`exp` 会话 `diag2-mt-m1-yss3`）。补充证据：崩溃日志没有栈溢出字样；出错地址整页对齐（0xc22fe6e000），不在栈上；`HardwareCorrupted: 4 kB`；4 次不可纠正错误全部在 `mc#1csrow#2channel#4`，其余 63 个 rank 都是 0。

### 2026-10-14 (T13 阶段 4) 内存增长的原因；发现 Complete 配置每次运行结果不同

- **内存增长**（子代理在本机复现，仓库没动；材料在 scratch 的 `memprobe/`）：四处只增不减的数据结构。
  1. DRAM 统计追踪器：子事务在控制器上开始、在 bank 上结束，记录永远删不掉（`akkalat/baseline/runner/dramtracer.go:40`；`akita/mem/dram/memcontroller.go:222` 与 `internal/org/bankimpl.go:87`）。只在 `-report-all` 时存在。
  2. L2 命中率统计的步骤名带 line 地址，每次未命中都是新名字（`akita/mem/cache/writeback/directorystage.go:894-898`，`akita/tracing/stepcounttracer.go:69-83`）。只在 `-report-all` 时存在。
  3. L2TLB 统计：EvictTest 等任务只开始不结束（`akita/mem/vm/l2tlb/l2tlb.go:184-186,211-213,266-268,285`）。只在 `-report-all` 时存在。
  4. typed filter 的 `scheduledVisibility`：只有同一 key 被再次查询才删除，顺序访问永远不删（`akita/mem/cache/writeback/typed_filter.go:441,483,376`）。Complete 配置一定有，是机制代码里的泄漏。
  - 本机测量：四处都修好后，内存增长降到原来的 1/3–1/10，模拟快 1.8–2.3 倍（GC 不再扫描大表）。按代码分析只影响统计记账，不改变时序。
- **新发现（更要紧）**：子代理把 Complete + `-report-all` + filter 统计的同一配置用同一个二进制连跑两次，84k 行统计里约 33k 行不同，`total_time` 相差约 0.5%。阶段 2 时 baseline 配置跑两次、跨服务器都完全一致，所以很可能是 CuPath 机制代码里有依赖 Go map 遍历顺序之类的不确定性。这会影响论文里差别只有几个百分点的对比，也影响阶段 4 正在跑的 Complete、m1、m2、m3 结果。
- **用户 10-14 决定**：阶段 4 照跑不动；先核实不确定性（哪种配置、哪段代码），把结论和修复方案（内存泄漏 + 不确定性）一起拿给用户，再决定要不要停下来用修好的代码重跑。核实交给同一个子代理。
- 更正：此前用“模拟是确定性的”来论证 yss2 崩溃不是代码问题，这个前提对 m1 这类配置不完全成立；yss2 内存条坏了的结论主要靠硬件证据（EDAC 不可纠正错误计数、`HardwareCorrupted`、SIGBUS code 4、整页对齐的出错地址），不受影响。

### 2026-10-14 16:30 (T13 阶段 4) 两台服务器卡死约 13 小时；修复 bug 后停掉阶段 4

- **卡死**：约 02:30 到 16:00，两台服务器上的模拟全部处于暂停状态（yss2 13 个、yss3 12 个），可用内存只有约 9 GB，低于守护进程“一个都没在跑时强制恢复”的 10 GB 门槛，形成死锁，进度停在 hpca 主结果 33/70、mi300x 主结果 23/70。本机监视把暂停的也算进“模拟个数”，所以“没有模拟在跑”的提醒从未触发，**这是我的失误**。
- **用户 10-14 决定**：先修复 bug，再停掉实验。
- **修复**（代码分支 `isca27/t13-mi300x` 推到 `756f214`）：
  - `f1ef3f0`：远程批量读按 line 号顺序处理响应（`mgpusim/timing/rdma/remote_requester.go` `processBitmapRspFromOutside`），消除 m2 / Complete 每次运行结果不同的问题。新增单元测试 `TestBitmapResponseQueuesReturnedLinesInLineOrder`（重复 20 次检查顺序；撤掉修复时第一次就失败）。
  - `756f214`：四处内存泄漏（DRAM 追踪器只记 `req_in`；cache/TLB 命中率追踪器只收 `req_in`；L2 MSHR 步骤名不再带地址；typed filter 定期清理已可见的 `scheduledVisibility`）。新增单元测试 `TestTypedFilterPruningExpiredVisibilityDoesNotChangeLookups`、`TestDramTracerIgnoresSubTransactionTasks`。writeback、dram、tracing、rdma、runner 包的全部测试通过。
  - 子代理此前在本机验证（与提交的代码逐文件一致，只差删掉一个无用的 `fmt` import）：修复后每种配置跑两次结果完全一致；在此基础上加上内存修复，所有统计（含 `total_time`）不变；内存增长降到原来的 1/3–1/10，模拟快 1.8–2.3 倍。
- **停掉阶段 4**（16:20）：两台服务器的 `t13s4c-*`、内存守护进程、yss3 上等待中的诊断运行全部停掉，残留模拟强制结束；已跑完的结果都保留在 `results/1013_t13s4-*_260ce73/`。已完成：T11 探针两套各 16 个；hpca 主结果 33/70（失败 1）；mi300x 主结果 23/70（失败 9）；mi300x filter 统计 13/14（失败 1）；LLM、敏感性未开始。
- **重启前要定的事**：（1）已跑完的 baseline / m1 / m3 结果是否保留（它们是确定的，修复不改变结果）；（2）DRAM 模型的问题（见内存控制器审查）是否要先改配置——如果要改，阶段 4 要整体重跑，现在重启会白跑。

### 2026-10-14 (T13) 内存控制器审查和 Local Pairing 的问题（子代理只读代码 + 单控制器小测试；材料在 scratch 的 `dramaudit/`）

- **结构**：一个控制器 = 一个通道，与一个 L2 分区一一对应（`akkalat/baseline/runner/r9nanobuilder.go:609-614,986`）；按容量公式算出 16 个 rank × 4 bank group × 4 bank（rank 是公式凑出来的）。请求按访问单位拆分（hpca 128 B，mi300x 64 B），调度是各 rank 队列轮流发第一个可发命令（不是 FR-FCFS），每 DRAM 周期一条命令。**永远关页**（`akita/mem/dram/internal/trans/closepagecommandcreator.go`），所以行复用为 0；`-dram-row-continuation-enable` 只在恰好有同 bank 同行的就绪命令时才不关行，论文配置里是关的。**没有刷新、没有 tFAW、跨 rank 不限制开行。**
- **地址映射**：控制器之间按 128 B 交织（`64 × l2-adaptive-pair-region-lines`）。mi300x 下控制器选择位和 bank group 位重叠，每个控制器只用到 1 个 bank group。
- **带宽**：hpca 每控制器 32 GB/s，每 GPM 128 B/周期，读 64 B line 时有用的只有 64（实测 59）；mi300x 每控制器 19.7 GB/s，每 GPM 315 B/周期（目标值）。mi300x 读只有 243 是 L2 每通道 16 个 MSHR 限制的，不是 DRAM；写 318 可能包含留在 L2 的脏数据。
- **对 Local Pairing 的影响**：hpca 的 128 B 访问单位让配对读“白送”（延迟不变、有用带宽翻倍），论文里 m1 的收益很可能主要来自这里；真实 HBM 每次访问 32–64 B。mi300x 下配对的两半落在同一 bank 同一行，又是关页，第二半要重新开行（79 ns vs 31 ns），在真实 HBM3 上应是行命中。真实 CDNA2/3 的 L2 line 是 128 B，模拟器两套配置都是 64 B。
- **Local Pairing 的实测**（mi300x 打开行缓冲复用后能拿回多少收益；mi300x 下配对预测为什么少了 5 倍）由另一个子代理在本机进行中，结果出来后写在这里。

### 2026-10-14 19:40 (T13 阶段 4) 失败排查（34 个子代理的工作流：四路只读排查 → 分类 → 每类两个反驳者 → 查漏）

- **没有新的模拟失败，也没有模拟器 bug**。campaign 里共 10 个失败（此前说 11 个，多算了 1 个），原因都经过反驳验证：
  - 返回码 −15（我停掉）9 个：10-13 **20:28**（此前 STATUS 写成 21:00，更正）mi300x 主结果 Complete 的 fft、floydwarshall；10-14 00:36 mi300x 主结果 baseline 的 aes、fft、fir、im2col、matrixmultiplication、matrixtranspose、pagerank。
  - 返回码 −9（OOM）1 个：mi300x filter 统计 kmeans Complete，00:40:31。
  - 返回码 2（SIGBUS code 4，硬件内存错误）1 个：hpca 主结果 matrixtranspose m1，01:18。
  - 监控里约 45 个“异常中断”的会话全是我主动停掉的（作废的 T14 chase 运行、两次阶段 4 重启、内存守护进程升级、诊断运行、16:20 停止）。已给这些日志补记 `[stopped]`，并让 `exp stop` 以后自动写这个标记，网页显示为“已停止”。
- **更正：能保留的结果比我之前说的少得多。** Complete 配置也开着远程批量读，受不确定性影响，全部 Complete 结果（hpca 13、mi300x 9、filter 统计 12）要重跑；m3 一个都没跑完，mi300x m1 也没有。可保留的只有 24 个：hpca baseline 13、hpca m1 6、mi300x baseline 5。
- **13 小时卡死的根本原因是我的调度脚本**：`runall2` 只在启动时按一次 MemAvailable 读数把所有空位填满（00:34 一秒内启动 7 个、00:37 又在 11 ms 内启动 7 个），直接导致了 00:36 的停止和 00:40 的 OOM；扫描只能提高并发不能降低；之后内存守护进程把 12 个全部暂停，在 9 GB 时没有任何规则能恢复。重启前要改：每隔几分钟最多启动一个；有暂停中的模拟时不启动新的；全部停住时报警；监控只数真正在跑的模拟。内存泄漏修好后增长小得多，但这些仍然要改。
- **kmeans 在任何配置下都没跑完过**：hpca kmeans 第一个 kernel 全部完成后，第二个 kernel 一直是“0% (WG 0/512)”，其中一个没被暂停也这样跑了 2.5 小时。是很慢还是卡死，正在 yss3 上用修复后的二进制 `756f214` 单独跑一次（`diag-kmeans-hpca-yss3`，有心跳）确认。
- **yss2 的内存条还在继续坏**：不可纠正错误从 4 次涨到 6 次，全部在 `mc#1csrow#2channel#4`。另外 yss2 在 10-12 05:30:54 发生过一次致命 machine check 导致整机崩溃重启（`last -x` 记为 crash，pstore 里有 MCE 记录；在我们的实验开始之前）。yss3 没有任何硬件错误。崩溃的 matrixtranspose m1 正在 yss3 上用同一个二进制重跑验证（`diag3-mt-m1-yss3`）。
- **重跑不能直接续旧目录**：`runall2 --rerun-missing` 会沿用旧目录里冻结的 `260ce73` 二进制（有泄漏、有不确定性），必须用新目录；保留的 24 个结果要和新二进制的结果合并，需要先用新二进制重跑一两个做对照。另外同一个 commit 在两台服务器上编出的二进制哈希不同（只差模块缓存路径），换服务器重跑要带上原二进制或用新目录。
- 其他：yss3 `/proc/vmstat` 记录了 2 次 OOM，一次是上面的 kmeans，另一次是别的项目更早的进程；`logs/archived/p3b.log` 的 `[exit 1]` 是 10-13 早期 T11 启动命令的循环 bug，已被取代。

### 2026-10-14 (T13) Local Pairing 实测（子代理在本机，短片段：max-wg 32768，spmv 1536）

- **mi300x 上打开行缓冲复用（`-dram-row-continuation-enable`）也拿不回 Local Pairing 的收益**：开行次数少一半（baseline 和 m1 一样），但时间只变 0–2%；m1 加速比 bitonicsort 1.006→1.009、relu 1.015→1.013、fastwalshtransform 1.009→1.000。**spmv 例外：1.083→1.100**，来自延迟隐藏（真正的预取式配对），不是省 DRAM 访问。hpca 对照：bitonicsort m1 1.955（复用开 1.877），仍然来自 128 B 访问单位。
- **mi300x 预测次数少 5 倍的原因**：预测器正常（置信度一直满，没有白取）；是一个 wavefront 同时请求 128 B 区域里的两条 line，99.9% 的配对在 2 ns 内到达 L2，第二条已占 MSHR，配对被过滤掉。把已在等的那条合进配对读（临时试验，正确性未验证）：mi300x 预测涨 4.7 倍但没有加速；hpca bitonicsort 1.96→2.75 倍，但仍是 128 B 访问单位的效果。
- **DRAM 地址映射问题**：控制器内部直接用全局地址，没去掉选 L2 分区的位，mi300x 每个控制器只用到 1/4 的 bank group、每行 8 列里的 2 列。
- **含义**：Local Pairing 不能再按“减少 DRAM 访问、利用行局部性”来论述；能站得住的是对不规则访问的延迟隐藏（spmv）。DRAM 模型（地址映射、关页、无刷新/tFAW）要不要改、Local Pairing 怎么定位，需要用户决定后再重启阶段 4。

### 2026-10-14 (T13) 改用 MI200 配置：用户决定和配置表（实现进行中）

- **用户 10-14 决定**：（1）GPM 改按 MI200（MI250X 的一个 GCD）配置，作为折中（MI300X 的 Infinity Cache、IOD 等结构模拟器没有建模）；（2）**每个 tile 不超过 30 个 CU**（否则实验太大）；（3）**不做校准**（手里没有 MI200），只要求配置和公开规格一致；（4）**L2 line 先用 64 B**（MI200 是 128 B，作为偏离注明）；（5）DRAM 的伪通道、同一条 line 连读同一行、刷新、tFAW、地址映射修正**全部做**，只在 mi200 配置下生效。
- **配置表（一个 GPM = 1/4 个 MI250X GCD；时钟仍 1 GHz，带宽按 1.7 GHz 换算成每周期等效值，延迟保持纳秒）**：

| 参数 | MI250X 一个 GCD | mi200 每 GPM | hpca | mi300x-eighth | 实现方式 |
|---|---|---|---|---|---|
| CU | 110 | 28（14 × 2） | 32 | 38 | 预设字段 |
| L1V / L1I / L1S | 16 KB/CU；L1I、标量 cache 每 2 CU 共享 | 16 KB；64 KB 8 路 / 16 KB 每 2 CU | 16 KB；每 4 CU 共享 | 32 KB；同 mi200 | 预设字段 |
| L2 | 8 MB、16 路、32 slice、每 slice 128 B/周期、line 128 B | 2 MB、16 路、8 slice × 256 KB、每 slice 2 请求/周期（1 KB/周期）、32 MSHR/slice、**line 64 B（偏离）** | 4 MB、4 分区 | 4 MB、16 通道 | 预设字段 + `-l2-cache-size-mb=2` |
| HBM | 4 个 HBM2e stack、4096 位、3.2 Gbps、1.6 TB/s | 1 stack、409.6 GB/s → 241 B/周期 | 128（实测 59） | 315 | — |
| 内存控制器 | 每 stack 8 通道 × 2 伪通道 | 8 个（与 8 个 L2 slice 一一对应），每个 2 个 64 位伪通道、BL4、每次 32 B，时钟 941 MHz | 4 个 256 位 BL4 | 16 个 64 位 BL8 | 改代码（伪通道） |
| 行策略 / 刷新 / tFAW / 地址映射 | 真实控制器有 | 同一条 line 的两次 32 B 访问连读同一行；全 bank 刷新；tFAW；去掉选 slice 的地址位 | 无 | 无 | 改代码（只在 mi200 生效） |
| 网络、RDMA、TLB | — | 不变 | — | — | — |

- 来源：[AMD CDNA2 白皮书](https://www.amd.com/content/dam/amd/en/documents/instinct-business-docs/white-papers/amd-cdna2-white-paper.pdf)、[AMD Instinct MI250 文档](https://instinct.docs.amd.com/projects/system-acceptance/en/latest/_sources/gpus/mi250.md)、[ROCm L2 文档](https://rocm.docs.amd.com/projects/rocprofiler-compute/en/docs-7.2.4/conceptual/l2-cache.html)、[Intel HBM2E 控制器文档（伪通道 BL4、32 B）](https://www.intel.com/content/www/us/en/docs/programmable/773264/24-3/hbm2e-features.html)。DRAM 时序的具体出处在实现时补上。
- **实现计划**：DRAM 四项改动（每项带单元测试，hpca、mi300x 的统计必须逐项不变）→ mi200 预设和 `configs/mi200.json` → 调度脚本加启动限速、有暂停的模拟时不启动、全部停住时报警 → 本机小规模回归 → 提交 → 服务器上先跑 T14 平台测量（只看特性，不做校准），再跑阶段 4。

### 2026-10-14 (清理) 按用户要求删除作废的结果目录

- **用户 10-14 决定**：作废或被取代的结果目录**直接删除**（不同于此前“旧结果不删”的规则，这次是用户明确要求）；本机 `remote/`（exp pull 的镜像，2 GB）也删掉。
- **删除**（服务器 `~/exp/cupath/results/`；yss2 60 个、yss3 51 个，本机 `results/` 没有匹配的）：
  - T12 修复前的探针：`1012_probe-*`（`a097f31`、`0ca173f`）、`1013_probe-*_d1f6d8c`；
  - 阶段 4 第一次启动 `*_9220740`（只跑了 T11 探针）；
  - 阶段 4 第二次启动 `*_8b4040e`（git bug，campaign 没起来）；
  - T14 错误指针追逐的 `1013_t14-{lat,hop}-*_0e38983`（已用 `ca7ea0b` 重跑）；
  - 阶段 4 未开始的 `1013_t13s4-{llm,sens-*}_*_260ce73`（没有任何结果）。
- **保留**：T11 `*_f7b9eef`、T12、T13 回归、T14 有效结果（`0e38983` 的 bw/rbw/inc/pat/kernel 与 `ca7ea0b` 的 lat/hop）、`260ce73` 的主结果和 filter 统计（含 24 个可保留的结果）。`isca27/results_index/` 和本机 `akkalat/results/`（7 月论文结果）都没有动。删除后：yss2 剩 72 个目录 1.2 GB，yss3 剩 52 个 2.3 GB。

### 2026-10-14 (T13) MI200 参照下的瓶颈审查（27 个子代理：8 个子系统审查 + 预设草案 + 汇总 + 每条两人反驳 + 查漏；只读）

**两处更正（我之前说错的）**
1. **MI200 的 L2 不会顺带取回相邻的半条 line。** MI200 的 L2 line 是 128 B，但向内存的读请求只有 32 B / 64 B（[ROCm L2 文档](https://rocm.docs.amd.com/projects/rocprofiler-compute/en/docs-6.4.1/conceptual/l2-cache.html)，计数器 TCC_EA_RDREQ 定义为 32 B 或 64 B 请求；128 B 读到 MI300 才有）；MT4G 论文在 MI210 上实测也是 128 B line、64 B 取数粒度。所以 mi200 用 64 B line 在取数粒度上与 MI200 一致；Local Pairing 的 128 B 配对读相对 MI200 是真正的改变。此前“真实 MI200 相当于一直在配对”的说法作废。
2. **T14 带宽曲线的横轴标错了。** 模拟器按轮转方式分配 WG（`mgpusim/timing/cp/builder.go` `WithAlg("round-robin")`），不是分段分配。platbench 的 bw 测试启动 8N 个 WG，N ≥ 4 时所有 CU 都在跑。“HBM 1–2 个 CU 就饱和”“L2 带宽在 16 个 CU 见顶后下降”应改为“按每个 CU 上的 WG 数”：L2 带宽在每个 CU 约 4 个 WG 时最高，WG 更多时下降。峰值数值本身不受影响。

**审查找到的主要问题（按对论文结论的影响排序；已经过反驳验证的标 ✓，部分被反驳的标 ◐）**
1. ✓ **MI200 会把对端显存缓存在请求方的 L2 里（MTYPE_NC，首次访问就缓存）**，而模拟器的 baseline 远程数据一律不进请求方 L2。m3（Remote Reuse）大体就是 MI200 的默认行为再加一个“第二次访问才缓存”的过滤。若论文声称 MI200 参数，审稿人会要求和这个默认行为比较。
2. ◐ **m1 的收益**：line 大小那部分见上面的更正一。现在的说法：在 MI200 粒度下配对是真正的改变；收益要在 mi200 配置上重新测。
3. ◐ **mi200 的 DRAM 设计是否会给 m1 制造新的人为收益**（tFAW、开行次数限制）：反驳者指出原分析用错了 tFAW 值，且实现中的设计已经处理了大部分问题。保留的原则：同时到达的两个相邻 baseline 未命中，DRAM 的处理要和一个 m1 配对一样。
4. ◐ **远程页的 TLB 缺失都去中心的 IOMMU**（+732 ns，本地只有 +104 ns，与数据距离无关）。效应是真的，但原分析对它在网络上的位置判断有误。MI200 上这会是一次普通的本地页表遍历。
5. ✓ **远程访问的并发上限**：每个 CU 的 L1V 最多 16 条 line 在途，每个 GPM 的 RDMA 最多 64 个，而且 CU 按顺序返回响应。m2/m3 减少请求、把远程变本地，等于同时绕开了这些上限，收益里可能有这部分的功劳。
6. ✓ **L1→L2 前端**：WG 增多时带宽反而下降（见更正二），问题在 L1→L2 这一段。
7. ✓ **延迟阶梯**：向量 L1 命中 115 ns（MI210 实测约 85 ns），其中约 66 ns 是写死的 6+60 级向量访存流水线；L2→HBM 只多 35–40 ns，没有数据通路（fabric）的延迟。
8. ✓ **kernel 之间不做任何 cache 失效**；MI200 在两次派发之间会失效 L1。迭代型 workload 里 m3 副本的命中可能在真实硬件上不存在。
9. **L2 slice / DRAM 通道的交织粒度和 `-l2-adaptive-pair-region-lines` 绑在一起**，敏感性实验的 row 维度每个点其实换了 baseline 的地址映射。
10. **占用率按 GCN3 计算**（每个 SIMD 10 个 wave），gfx90a 是 8 个，VGPR 文件也不同。
11. **CU 发射**：发射仲裁器会一直停在同一个 SIMD 上（`issuearbiter.go:52-57` 的切换函数从未被调用），VALU 吞吐可能只有理论值的 1/4；LDS 没有带宽限制。这一条只是读代码得出的判断，还要用小测试测量确认。
12. **数据放置（查漏发现，影响可能最大）**：内存分配把每个 buffer 切成 48 段连续的页，小于 48 页（192 KB）的 buffer 整个放在角上的 GPM 1。kmeans 约 97% 的读是远程读，而且所有 GPM 都在同时读 2–3 个 owner，被 owner 端约 113 B/周期的上限卡住，这很可能就是 kmeans 特别慢的原因。另外，每个 GPM 分到的 WG 段按 CU 数计算，页段按 48 等分，CU 数一变两者就会错开，远程比例跟着变，hpca / mi200 的对比会被这个混淆。
- 其他没覆盖到的：远程写路径、网络参数（每跳延迟、链路带宽）是晶圆系统的假设，没有出处也没有敏感性实验；filter 的硬件代价；`-max-wg` 截断只测每个 GPM 刚开始、cache 还是冷的那一段。

**审查建议的实验顺序**：(1) 只做分析：更正记录、修 mi200 配置表（审查认为 L1I 应是每 2 个 CU 32 KB 4 路）；(2) L1→L2 前端和并发上限诊断（platbench，现有二进制，约 1 小时）；(3) 远程 TLB 缺失是否跟到中心的距离有关（约 1 小时）；(4) 各机制收益的归因：短片段，只改配置，约 70–80 个运行；(5) 把 MI200 的 baseline 语义做成开关（NC 缓存、本地页表遍历、kernel 边界失效；要改代码）；(6) 验证 mi200 的 DRAM；(7) CU 侧小测试；(8) 冻结 mi200 预设，跑 T14 看特性；(9) 6 个 workload 的小规模试跑；(10) 全量重跑（估计 3–4 天以上）。

### 2026-10-14 21:45 (T13) 用户决定；诊断实验启动（现有二进制 756f214，不改代码）

- **用户 10-14 决定**：
  - 数据放置：**先量化再决定**怎么改；
  - baseline：**保持现状**（远程数据不进请求方 L2；不做 MI200 的 NC 缓存）；
  - mi200 预设：**有意做得比真实 MI200 稍弱一些，给优化留空间**，审查建议的四项（远程页表遍历走本地、每 SIMD 8 个 wave、kernel 边界失效 L1、交织粒度独立）都**不加**。我的提醒：论文里要如实写出这些和 MI200 的差别，不要笼统说“按 MI200 配置”；最好留一组敏感性实验，展示打开更真实的设置后收益还剩多少；
  - 诊断实验：现在就用现有二进制跑。
- **诊断实验**（exp 组 `t13-diag-1014`；结果目录 `results/1014_diag-*_756f214`；改过的参数记在各自的 meta 里）：
  - A（yss2，14 个）：L2 带宽测试，L1 每 CU 在途请求数 16 / 32 / 64；单对远程带宽测试，RDMA 在途上限 64 / 128 / 256（1 跳、6 跳）；hpca 和 mi300x 各一套。
  - B（yss2，4 个）：远程 TLB 缺失与到中心的距离：GPM 18（离中心 1 跳）读 GPM 11，GPM 1（角）读 GPM 2，A、B 两组，64 MB。
  - C（yss3，28 个）：数据放置量化：14 个 workload × hpca / mi300x，baseline，`-max-wg 16384`（spmv 1536），`-report-l2-source`。
  - D（yss2，30 个）：机制归因：mi300x，pagerank、fir、bitonicsort、relu、fastwalshtransform（`-max-wg 32768`）和 spmv（1536）× baseline、m1、m2、m3、Complete，用 `runall2.py --remote-ablation`。
- 第一次启动 yss2 的 platbench 批处理时，yss2 上没有 `~/isca27_diag` 目录，脚本找不到（exit 127），补建后用新会话名重新启动。
- mi200 实现、监控整理和出图功能都还在本机开发，没有同步到服务器。

### 2026-10-14 22:00 yss2 内存条继续恶化；用户决定继续用 yss2、失败的到 yss3 重跑

- 今天 yss2 上的诊断实验有 3 个失败，**全部是硬件内存错误**（SIGBUS code 4）：归因 campaign 的 `bitonicsort_baseline`、`fir_complete`，platbench 的 `1014_diag-rbw-d2-rdma256_wafer48_hpca`。yss2 的 `HardwareCorrupted` 从 4 kB 涨到 **16 kB**（又下线了 3 页），EDAC mc1 不可纠正错误计数仍是 6。
- 同时，yss3 上用同一个二进制重跑 yss2 崩溃的 hpca matrixtranspose m1（`diag3-mt-m1-yss3`），已经远远越过原来崩溃的位置（原来在第 97 条进度输出时崩溃，现在已到第 1109 条），进一步说明那次崩溃是 yss2 的硬件问题。
- **用户 10-14 决定**：继续用 yss2，在 yss2 上因硬件错误失败的运行挪到 yss3 重跑。单个运行直接在 yss3 上用同样的命令重跑；campaign 里失败的格子，等 campaign 跑完后把整个目录连同冻结的二进制复制到 yss3，用 `runall2.py --rerun-missing` 补跑再合回去，二进制不变。已在 yss3 上重跑 `1014_diag-rbw-d2-rdma256_wafer48_hpca`。
- kmeans 诊断（yss3，修复后的二进制，hpca baseline）：第一个 kernel 的 24,576 个 WG 全部完成，进入后续 kernel，一直在推进，确认没有卡死。
- 监控网页已清理：阶段 4 重启之前的组全部归档（yss2 68 个会话、72 个结果目录；yss3 62 个会话、51 个结果目录），网页默认只显示今天的诊断。用 `exp unarchive` 或网页上的“显示已归档”可以看回旧的。

### 2026-10-14 22:15 (T13) 用户决定：mi200 固定后作为 baseline；mi300x 的诊断改为 mi200

- **用户 10-14 决定**：（1）现在的 baseline 不再是 MI300X，诊断实验里 mi300x 的部分换成 mi200：已停掉 yss2 上的 mi300x 机制归因和 yss3 上数据放置统计的 mi300x 一半；hpca 那一半继续，mi200 做好后补跑 mi200 的归因和数据放置；已跑完的 mi300x platbench 诊断留作参照。（2）**mi200 实现好、固定下来以后作为之后所有实验的 baseline 配置**。
- **固定的做法**：实现、审查、本机回归通过后单独提交并打标签（如 `isca27-mi200-v1`），之后数值不再改（要改就出 v2 并先问用户）；`config_deviation` 的基准改为可选，新实验默认相对 `configs/mi200.json`（hpca 旧结果照旧可复现）；在冻结版本上跑一遍 T14 平台测量作为特性记录（不是校准），写进 STATUS 和 `results_index/platform/`；之后的机制归因、阶段 4、敏感性都基于它。
- **给 Cowork**：`CONFIG.md` 的“有效论文配置”需要改为 mi200（提交和标签固定后会在 SYNC 里给出）；mi200 有意比真实 MI200 稍弱（远程页表遍历经过中心 IOMMU、kernel 之间不失效 L1、远程数据不进请求方 L2、每 SIMD 10 个 wave），论文里要如实写出这些差别。

### 2026-10-14 22:20 (T13) mi200 baseline：验证清单和截止时间

- **用户要求周五（10-16）之前把 mi200 baseline 完全做好**（实现、验证、冻结），以便后面推进。
- **冻结前的验证清单**（全部通过、用户确认后才冻结）：(1) 旧配置不受影响：单元测试；hpca、mi300x 在本机和服务器上跑 fir、matrixmultiplication，与 756f214 统计排序后完全一致；(2) 实际生效的参数（`-config-out`）与配置表逐项一致，并有单元测试；(3) 14 个 workload 和 LLM 算子在 baseline/m1/m2/m3/Complete 下各跑一小段，无死锁、无崩溃、完成的 WG 数正确；(4) baseline 和 Complete 各跑两次，统计完全一致；(5) T14 平台测量看特性：HBM 带宽和理论峰值（241 B/周期）差得不多，刷新开销约 6–7%，没有明显不合理的地方（不是校准）；(6) 内存不会一直涨，并估算全量实验时间；(7) 把有意的弱化写清楚。
- **时间安排**：周三晚实现完成、检查、提交、同步；周四跑验证工作流（服务器部分主要在 yss3）并报告；周四晚到周五上午修问题、复验、用户确认后冻结并通知 Cowork；周五用冻结的 mi200 补跑机制归因和数据放置统计。

### 2026-10-14 23:00 yss2 崩溃的验证结论：硬件问题，已确认

- yss3 上用同一个冻结二进制（`a5047fc8…`，`260ce73`）、同样的参数重跑 hpca 主结果的 matrixtranspose m1（`diag3-mt-m1-yss3`，结果在 yss3 `~/isca27_diag/`）：**正常跑完**，退出码 0，无任何崩溃信息，32,400 个 WG 全部完成（在 yss2 上第 97 条进度输出时崩溃，这次共 2,397 条）。`total_time` = 12,695.8 µs，相对 hpca baseline（12,747.6 µs）加速 1.004（论文 m1 为 1.005）。m1 是确定的，修复也不改变它，这个结果可以补进 hpca 主结果缺的那一格。
- 结论：10-14 01:18 那次崩溃（以及今天 yss2 上 3 次 SIGBUS）是 yss2 内存条的硬件错误。yss2 `HardwareCorrupted` 继续上升：4 kB → 16 kB → **20 kB**。建议尽快请管理员更换 `mc#1csrow#2channel#4`。
- 另：监控网页的图表功能已上线（Campaign 进度里的“画图”“加入对比”）；论文画图脚本 `akkalat/isca27_plot.py` 已提交（`313a141`，44 个单元测试；加速比和 7 月论文脚本的 165 个数值逐个一致）。

### 2026-10-15 15:05 (T13) mi200 实现已提交；验证实验已在两台服务器上启动

- **提交**（分支 `isca27/t13-mi300x`，已推送）：`3aa570e` DRAM 建模（pseudo-channel、同请求行保持、all-bank refresh、tFAW、HBM2 写时序、地址映射修正；全部默认关闭，只有 mi200 打开，旧配置的 golden 回归测试通过）；`0b60da8` mi200 预设和 `configs/mi200.json`；`67f1c03` runall2 的启动间隔、暂停子进程时不启动新运行、STALL 提示，`isca27_stage4.sh` 增加 mi200 计划。
- **配置更正**：L1I 改为 32 KB 4-way（与 CDNA2 白皮书一致）。之前表里写的 64 KB 是我列表时的错误，不是用户的决定，`_deviation-l1i` 已删除。
- **验证清单进度**：(1) 旧配置不受影响：本机 hpca fir 与 756f214 统计排序后完全一致（100,477 项）；hpca matrixtranspose、mi300x fir 仍在本机比对。(2) yss3 上 mi200 fir 冒烟测试：rc=0，`total_time` 20,917 ns（hpca 21,195 ns），config 行显示 28 CU、8 个 L2 slice、2 MB L2、DRAM 各项特性都已打开。
- **10-15 15:00 启动**（exp 组 `t13-mi200-1015`，二进制 `67f1c03`）：
  - yss3：mi200 短片段 ablation，13 个 workload × baseline/m1/m2/m3/Complete（`-max-wg 32768`），spmv 单独用 1536；fir、pagerank 的 baseline 和 Complete 各再跑一遍做可复现性检查（结果目录 `1015_determinism_…`）。
  - yss2：mi200 T14 平台测量 29 项（带宽测试的 CU 档位为 1、2、4、8、16、24、28）；hpca 同样设置的短片段 ablation 作对照。
- **启动时的两个问题**：(a) `exp run` 同步时把本机一个 10-12 的旧 Linux 二进制（不认识 `-gpm-preset`）推到了服务器，覆盖了刚编好的，第一次启动全部 rc=2；已把本机这个（被 gitignore 的）文件换成 67f1c03 的版本，清掉错误的结果目录后重新启动。(b) yss2 上 bw-write 又一次 SIGBUS（"unexpected fault address"），`HardwareCorrupted` 20 → **24 kB**，仍是那根内存条。
- **已知问题**：敏感性脚本每个点都传 `-l2-cache-size-mb`（参考点 4 MB），mi200 预设会拒绝。阶段 4 的敏感性在 mi200 上要先把参考点改成 2 MB（档位如 1/2/4 MB），改之前会先问用户。
- kmeans 诊断（yss3，hpca baseline，修复后的二进制）已跑完：`total_time` 9,561,046 ns，和 7 月论文的结果完全一致，约用 12 小时。

### 2026-10-15 20:00 (T13) mi200 HBM 读带宽只有峰值 31%：诊断结果；用户决定保持现状

- **现象**：mi200 的 T14 测量里，HBM 读带宽在 2 个 WG 以后就不再上升，最高 75 B/cycle，理论峰值 241 B/cycle，只到 **31%**（写约 120 B/cycle，50%）。对比：mi300x-eighth 231 B/cycle，hpca 57 B/cycle；真实 MI250X 实测一般为峰值的 75–85%。
- **诊断**（yss3，临时诊断版本，只改一项，不提交；HBM 读，28 WG，单位 B/cycle）：现状 74；L2 MSHR 32→128 为 111；DRAM 队列加大为 73；关 refresh 为 129；关行保持为 52；MSHR 128 加队列加大为 111；在此基础上再关 refresh 为 135。另外 `-dram-row-continuation-enable=true` 没有效果（75.7），tFAW 从未拦下激活。
- **结论**（三层瓶颈）：(1) 每个 L2 slice 只有 32 个 MSHR，不够覆盖约 200 ns 的 HBM 延迟（要跑满约需 94 个）；一个 pseudo channel 刷新时，发给它的请求占满 MSHR，另一个 pseudo channel 也跟着空等，refresh 的损失因此从约 12% 放大到约 40%。(2) DRAM 的结构上限约 57%：每个 64 B 请求都要激活一行、读完就关，连续地址又被分到不同的 bank，没有跨请求的行命中，每个 pseudo channel 最多每个 tRRD_S（约 7.4 ns）激活一次，上限约 138 B/cycle。(3) refresh 本身约 12–18%。refresh 的实现本身没有问题。"每个 slice 32 个 MSHR"是配置时自己定的数，没有公开来源。
- **用户 10-15 决定：保持现状**，等这批 mi200 实验跑完再说。在此之前不改 mi200 的数值；如果之后要改，可选的方向：MSHR 改为 128（约 46%），或再改为开行策略并调整地址映射（可能到 75–80%，也会改变 Local Pairing 的表现）。
- 服务器上旧配置不受影响的检查也已通过：yss2 上 hpca matrixtranspose 的 baseline 为 12,747.6 µs，和论文 hpca 的 baseline 完全一致。
- 进度（19:50 UTC）：yss3 mi200 已完成 59/70，yss2 hpca 已完成 56/70，全部成功。目前的几何平均（短片段，kmeans 和部分 m3 未完成）：mi200 的 m1/m2/m3/Complete 为 1.03/1.16/1.00/1.20，hpca 为 1.19/1.18/1.02/1.42。

### 2026-10-16 (P08 + P09) 同步、10-14 到 10-16 结果整理、Cowork 复核意见；T15 停在第 1 步（⏸）

- **同步**：读了 SYNC 日志 10-16 的四条 `[cowork]`，以及 Cowork 未提交的改动（`00_background.md` D19–D22、`CONFIG.md` 10-16 说明、`README.md` 加 T15、`ideas_from_T14.md` 含 10-16 两节补充、`tasks/T15_headroom_oracle.md`、`site/` 三页、`prompts/P08`、`prompts/P09`），以 `cowork:` 提交并推送（`86bf7f7`）。**理解到的规格变化**：目标配置改为 mi200（D19，冻结前 hpca 规则仍有效）；mi200 有意偏弱（D20）；HBM 31% 暂时保持（D21）；作废结果直接删除（D22）；新增 T15（oracle 上限实验，mi200 冻结之后的第一个实验）。
- **P08 完成**：10-14 到 10-16 的结果复制到 `results_index/`（新命名；单次运行是 meta.json / summary.csv / platform.csv / run.json，campaign 是 EXPERIMENT_METADATA.json 和逐格的 `cells.csv`），mi200 平台测量的汇总在 `results_index/platform_mi200/`。“结果摘要”里新增 P08 的 7 张表和 mi200 验证清单；复核意见 (a)–(d) 写进“需要用户决定”和“规格与代码的差异”。
- **结果目录改名**（用户 10-15 决定：跑完后改名）：yss2 已完成（122 个目录，全部写了 `run.json`，归档标记 72 个保留，`results/RENAMES.tsv` 有记录）；yss3 等 LLM 算子跑完再改（还有 5 个在跑）。命名规则在代码仓库根目录的 `exp.json`。
- **mi200 的新观察**（详见 `~/Desktop/CuPath/mi200_findings_2026-10-16.md`，用户要求整理给后续讨论）：Local Pairing 在 mi200 上失效的原因（合并后的 128 B 请求到 DRAM 又被拆开，行激活次数 ×1.00，hpca 是 ×0.48–0.59）；kmeans 的放置错位；LLM attention 的 m3 收益（attn_score 9.5×）；L2 变小后 floydwarshall 的 m3 从 1.06× 变成 0.95×。
- **T15 停在第 1 步**：mi200 还没冻结（`isca27-mi200-v1` 不存在），按 P09 停下来问用户；每 slice MSHR 是否改为 128 也还没定（见“需要用户决定”）。配置确认（第 2 步）等冻结后再做。按诊断 A，O2a（L1V 256）、O2b（RDMA 4096）都明显超过饱和点，可以用 T15 的原值。
- 另外（不属于 P08/P09，用户在 10-15 到 10-16 要求的工具工作，不影响实验）：监控网页改成 akita 风格；做了通用的 `akita exp` 工具（akita v5 的本地分支 `exp-tool`，没有推送，中英文说明书在 `experiment/MANUAL*.md`）。

### 2026-10-16 (T13/T15) mi200 冻结；T15 配置确认（⏸ 等用户）

- **用户 10-16 决定：保持每 slice 32 个 MSHR，现在冻结。** 标签 `isca27-mi200-v1` 打在 `67f1c03`（验证用的提交）上，只在本地（规则是只推送任务分支，标签推不推送等用户）。`61b6ae8`：`config_deviation` 改为相对 `configs/mi200.json`（`meta.json` 记录 `deviation_base`），`isca27_config.py --base configs/hpca.json` 仍给出相对投稿版的差异；新增 3 个单元测试；冻结说明 `akkalat/configs/MI200_V1.md`（验证、有意的弱化、HBM 诊断）。**给 Cowork**：`CONFIG.md` 的“有效论文配置”可以改为 mi200（D19）了。
- **T15 配置确认**：见“需要用户决定”。发现 `mi200.json` 不含 CuPath 各机制的开关（模拟器有的默认开），所以 T15 用 runall2 的 baseline 加 `--extra-benchmark-flags`，不用配置文件。

### 2026-10-16 (P10/P11) 同步；T16 第 1 步核实；T16 tracer 和 T17 开发开始

- **同步**：读了 SYNC 里 10-16 的十条 `[cowork]`，以及 Cowork 未提交的改动（`ideas_from_T14.md` 的 placement 和 placement 以外的几节、模拟器约束、速度预算；T15 的 10-16 补充；新文件 T16、T17、P10、P11；T5a–d、T9 标为被取代；README；figs；site），以 `cowork:` 提交并推送（`d574d93`）。**理解到的规格变化**：新增 T16（W1 位置导致的负载不均，阶段 1 只测量）和 T17（PagedAttention、MoE、R-GAT 三个新 workload，DLRM 备选；不用 atomic、pull、索引在 host 端算；每个实验 3–5 小时、kernel 要完整跑完）；T15 增加三项检查（按 O1 上限分类、KM/MM/FWS 的 baseline 人为因素、FWT 的退化）。
- **T16 第 1 步核实（和 Cowork 说的一致）**：
  1. WG 静态连续切分：`mgpusim/driver/driver.go:432` `distributeWGToGPUs`，`wgPerCU = ceil(总 WG / 总 CU)`，按 `UnifiedGPUIDs` 顺序每个 GPM 连续分 `CU 数 × wgPerCU` 个，超出总数后截断，所以后面的 GPM 可能分到 0 个。
  2. 页按 48 等分连续放：`mgpusim/driver/internal/memoryallocator.go:213` `selectActualGPUForDistributedPage`，`pageIndex / floor(numPages/48)`，余数页给最后一个 GPM，页数 < 48 的 buffer 全部放在第一个 GPM。按每次分配（每个 buffer）各自计算。
  3. `-max-wg` 是全局计数：`akkalat/baseline/runner/wgtracer.go` 的一个 tracer 挂在所有 CU 上，统计 `WGCompletionMsg`，到 N 就 `atexit.Exit(0)`，所以截断运行里所有 kernel 都没结束。
  4. **编号对应**：`runner.go:526` `createUnifiedGPUs` 用 driver ID 1..48 依次组成 `UnifiedGPUIDs`（下标 i ↔ driver ID i+1）；GPM 按 7×7 逐行创建、跳过中心（`timingplatform.go:580`，组件名 `GPU[t]`，t = x + 7y），所以 driver ID g ↔ tile t = g−1（g ≤ 24）或 t = g（g ≥ 25），坐标 (t mod 7, t div 7)。**推论**：WG 段和页段都按“逐行”顺序排列，第 i 段在 tile 顺序的第 i 个位置；行尾到下一行开头是 7 跳（Cowork 已修正）。
- **并行开发（不占正式实验位置）**：T16 的 per-GPM tracer、platbench 远程比例参数、1d 小输入建议在分支 `isca27/t16-position`；T17 第 0 步和 W-A 在分支 `isca27/t17-workloads`；两个分支都从 `61b6ae8`（冻结版之后，模拟器代码和 `isca27-mi200-v1` 相同）开出，用独立工作区，不影响正在跑 T15 的代码目录。
- **T15**：yss2 的 54 个已完成；yss3 原来排队的 13 个已挪到 yss2（用户 10-16 要求两台服务器平衡）；yss3 上 kmeans 等长运行还在跑。

### 2026-10-17 用户决定；T16 阶段 1 运行启动；更正一处说法

- **用户 10-17 决定**：(1) T16 跑 1c + 1d，不跑 1b；(2) `v_mad_u64_u32`（VOP3 488，解码成 32 位目的寄存器）先查现有 benchmark 有没有用到，再决定修不修；(3) W-C 接受简化（单头、无 bias、hidden 128），定规模时另报 4 头、512 维的估算；(4) kmeans、kmsc 读到旧数据的问题保持原样，只记录为局限（论文用的 kmeans MaxIter = 2，kernel 数和访存模式不变，只有算出来的数可能不对）。之前（10-16）已决定：补 6 条指令（已做，fir 回归一致）、批准不计时的 coherent D2H 拷回（opt-in，默认关；timing 模式下还有 bug，在修）、下载 Azure trace、IGBH-tiny、ogbn-mag（已下载到 yss2 测试目录）、MoE 不均 1.75 + 无偏置对照、不加共享 expert。
- **T16 运行**：yss2，`akkalat/isca27_run.sh` + `configs/mi200.json` + `-report-gpm-trace`（1d 另加 `-report-l2-source`），13 个：`1017_diag_t16-a2a-16k`、`-frac{1,0.5,0.25,0.1,0}`、`-{pagerank,kmeans,matrixmultiplication,matrixtranspose,spmv,aes,im2col}-small`，都在 `results/1017_diag_t16-*_mi200_2e09bc9/`。二进制是 `isca27/t16-position` `2e09bc9` 交叉编译后放进 `~/isca27bin/baseline_2e09bc9`（服务器上的源码目录没有换）。
- **更正**：10-16 T15 配置确认里写的“`mi200.json` 不含 CuPath 机制开关、模拟器有些机制默认开，用配置文件跑出来的 B 不是 baseline”**不对**。`-remote-data-path-enable` 默认 false，Remote Aggregation/Reuse 的子开关（dedup、batching、requester L2 默认 true）只在它打开时起作用（`akkalat/baseline/runner/runner.go:447`、`mgpusim/timing/rdma/remote_requester.go:21`）；Local Pairing、granularity、filter、prefetch、row continuation 默认都是 false。所以用配置文件跑的就是 baseline，T14 的平台测量有效；T15 用 runall2 的 baseline 也没问题，只是当时的理由写错了。
- **T15**：yss2 67/67 完成（原 54 + 挪过去的 13），yss3 12 个完成、5 个还在跑，合计 79/84，没有失败。

### 2026-10-17 T17 第一轮 baseline 启动

- **合并**：`isca27/t16-position` 合并进 `isca27/t17-workloads`（`95615c4`），这样 T17 的运行能打开 T16 的 per-GPM tracer。回归（yss2 测试目录，mi200 baseline 参数，fir，`-max-wg 4096`）：冻结版 `67f1c03` 和 `95615c4` 的 total_time 都是 26.568 µs，164,567 行排序后的 md5 相同。
- **运行**（yss2，`akkalat/isca27_run.sh` + `configs/mi200.json` + `-report-gpm-trace -report-l2-source -l2-source-tile-width=7`，二进制 `~/isca27bin/baseline_95615c4`）：`1017_diag_t17-pagedattn`（`-pa-batch=66`）、`-moe-skew1.75`、`-moe-skew0`（`-moe-tokens=256 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6`）、`-rgat-s320`（IGBH-tiny，`-rgat-seeds=320 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1`）。预计 3–4 小时。
- **模拟器 bug 修复**（用户批准）：`s_nop` 在 timing 模式下不推进 PC、`v_mad_u64_u32` 解码成 32 位目的寄存器；在 T17 分支上做，回归在 yss2 测试目录。

### 2026-10-17 T17 第一轮 baseline 完成；两个模拟器 bug 修好

- **模拟器 bug**（`isca27/t17-workloads`，推到 `c9cdf64`）：`3a94d26` 调度器内部的 SOPP 指令（`s_nop`、`s_sleep`、`s_setprio` 等）推进 PC；`b5cff1b` `v_mad_u64_u32` 改成 VOP3b、64 位；`c9cdf64` 编译检查接受 `s_nop`（`s_sleep` 等 emu 没实现，仍然拒绝）。回归（yss2 测试目录，修复前 `95615c4` 对修复后 `c9cdf64`）：fir、MM（`-max-wg 4096`）、llmop gemm（含 488 的 kernel）total_time 和排序后统计的 md5 完全一致。`b5cff1b` 在回归跑完之前就推了，回归一致，不需要补救。T17 第一轮用的是修复前的 `95615c4`，因为回归一致，结果不受影响。
- **T17**：4 个运行全部成功，结果见“结果摘要 → T17 第一轮”。新发现：R-GAT 的时间大部分在只用少数 GPM 的 kernel 上；T17 的负载不均来自数据，不是位置。两条都放进“需要用户决定”。

### 2026-10-20 (P12) T18/T19 的测量：1–4 完成，5 停在配置确认（⏸）

- **同步**：P12 的同步（第 0 步）已由之前的会话完成（`5d179e1`），没有重做；之后 Cowork 又提交了 `7844b79`（T18/T19 机制图），只读。
- **配置确认（CONFIG.md 第 3 节，测量运行 = mi200 baseline + 统计开关，我自己确认）**：① blob 对比 `isca27-mi200-v1`（`67f1c03`）：`runall2_constants.py`、`runall2_config.py`、`shaderarray.go`、`r9nanobuilder.go`、`timingplatform.go`、`gpm_preset.go`、`config_file.go`、`configs/{hpca,mi200,mi300x_eighth}.json` 都没变；`flag.go` 是 T16 改的（只加 `-report-gpm-trace`），P12 没改；`runner.go` P12 只加了 2 行（tracer 字段和写文件的调用），`tracers.go` 加 1 行。② 命令：`akkalat/isca27_run.sh <exp_id> ed1aca0 P12 mi200 <benchmark 参数> -report-wg-footprint -report-m1-sharing -report-gpm-trace`（PA、MoE 另加 `-t17-print-groups`）。③ 差异：和 `mi200.json` 没有配置差异；只多了统计开关和 benchmark 参数（`meta.json` 的 `config_deviation` 已核对）；`config_used.json` 里 `magic-memory-copy=true`、`verify=false`。
- **代码**（`isca27/p12-measure`，本地，没推送）：`1929100` 两个 tracer（`akkalat/baseline/runner/p12tracer.go` + 单元测试；`isca27_run.sh` 可选 `ISCA27_BINDIR`）；`ed1aca0` benchmark 参数（`-pa-steps`、`-t17-print-groups`、`-t16-pagerank-iters`、`floydwarshall-p12small`，带单元测试：`-pa-steps=1` 和默认输入完全相同）；`0b85bef`、`d32520a` 分析脚本（`akkalat/p12_footprint.py`、`p12_m1.py`、`p12_tables.py`），`isca27_config.py` 把 `-force-local-data-access` 标为 oracle。
- **运行**：只在 `~/akita-exp-test/p12/`（yss2 和 yss3 各一份，用户 10-19 晚同意两台都用、每台最多 15 个），PID 记在各自的 `pids.txt`；回归 13 个、复现检查 6 个（yss2）、测量 7 个（yss2 3 个、yss3 4 个），全部 rc=0，没有 SIGBUS，结束后两台都没有我的进程在跑。结果见“结果摘要 → P12 测量”。
- **新发现**：R-GAT 每次运行的结果差约 0.16%；MoE 的 T17 原始结果和复跑差 0.002%；`azure_conv_tokens.csv` 被 `.gitignore` 挡住没进仓库。都放进“需要用户决定”。
- ⏸ 等用户：第 5 步 O1/O2b/O3 的配置确认；R-GAT 方案 B；上面三条新发现。

### 2026-10-20 T18 实现完成（⏸ 评估配置确认）；T19 实现中

- **用户 10-19 决定**：T18、T19 不再停在 `[提案]`，两个同时实现，直接做真实版（有限大小的 cuckoo 表 / filter）；评估的 baseline 用 mi200 原样（地址转换不变）；workload 用远程型 7 个 + T17 三个，本地型只确认不变慢。代码分别在 `isca27/t18-merge`、`isca27/t19-steal`（都从 `isca27/p12-measure` @ `0b85bef` 开出）。
- **T18**：`ca8b831`（已推送），4 个提交，只加代码（2,466 行；配置相关文件只加了接线）。回归逐位一致；冒烟和正确性见“结果摘要 → T18 冒烟测试”；评估计划见“需要用户决定”。
- **T19**：`eaa9c73`（已推送）。冒烟见“结果摘要 → T19 冒烟测试”；T17 主 kernel 在静态切分下没有可偷的 WG，方向问题见“需要用户决定”。

### 2026-10-21 (P18) 网络结论交叉验证：调研完成（⏸ 等用户确认方案）

- **同步**：提交 page-migration 会话留下的未提交文件（原样，`d34d99a`），推送 `isca27/docs`。
- **调研**（两个只读子代理 + 我抽查）：ASTRA-sim（master `4ce9ecf`）没有 2D mesh、congestion-aware 只支持 1 维、每个 NPU 只能有 1 个在途 send、没有链路统计，不适合直接验证；BookSim2 改约 150 行就能做闭环 + 矩阵流量；Garnet standalone 只有开环。akita 的 32 周期是不占缓冲的全流水线、没有 credit，其他模拟器要按两种缓冲语义配。方案和待决事项见"需要用户决定"第一条。
- 没有安装、编译、运行任何东西；服务器没动。ASTRA-sim / BookSim2 / gem5 / noxim 的浅克隆在本机 scratch（不进仓库）。

### 2026-10-21 (P18) ASTRA-sim 交叉验证完成；Llama-7B 试验；机制的网络层评估（选项 A）

- **用户决定**：用 ASTRA-sim（不用 BookSim2）；问速度；要求试 Llama-7B；机制移植选 A（网络层评估）。
- **安装**：本机克隆 `~/Desktop/CuPath/astrasim`（分支 `cupath-p18`，没有推送），同步到 yss3 的 `~/astrasim-test/` 编译（venv cmake + 系统 protobuf 3.12）。没有碰 `~/exp`、`~/akita-exp-test/` 和调度器；`run_all.py` 最多 8 个并发，按 MemAvailable 留 30 GB。
- **运行**：校准 2 个，E1 闭环 48 个，E2a 同负载开环 18 个，E2b 开环扫描 96 个，E3 机制 36 个，LLM 4 个，全部 exit 0。一个 100 万次读的试跑卡了 8 分钟，查出原版事件队列是平方级，修好后停掉了那次试跑重跑。
- **结果**：见"结果摘要 → P18"和 `results_index/astra/analysis.md`。
- **没做的**：48 GB/s 下 akita 的零负载跳数延迟没有直接测量（需要的话用当前二进制在服务器重跑 T14 跳数矩阵）；ASTRA 里没有实现多播。
- 工作区里 `results_index/pm/`、`results_index/geo/`、`related_work_ideas_1021.md` 是别的会话正在改的文件，这次没有提交。

### 2026-10-21 晚 (P18) akita 当前配置的平台校准；ASTRA incast（E4）

- **用户 10-21**："都可以"（重测 48 / 384 GB/s 平台数据；ASTRA 补丁推到远程）。
- **运行**：akita 16 个（跳数矩阵 2、incast 12、all-to-all 2），只读使用 `~/akita-exp-test/v3c384/bin/baseline_4235991`（md5 `058680df…`）和同目录的 `mi200.json`，输出在 `~/astrasim-test/mgpusim_hop/`，`nice` 运行，最多 2 个同时，按 MemAvailable 留 30 GB；没有碰别的调度器。ASTRA incast 24 个。全部 exit 0。结果见 `results_index/astra/{calibration_incast.csv, mgpusim_calib/, analysis.md 第 0、3、4 节}`。
- **推送 `isca27/astrasim` 被 Claude Code 自动模式拦下**（新仓库加 remote 判为远程重定向）。补丁已在 `results_index/astra/patches/`。如果还要单独的分支，请在终端运行：`cd /private/tmp/claude-502/-Users-daoxuan-Desktop-CuPath/c6d28c9e-9c9f-4ab8-88ab-e761a9a1b937/scratchpad/astrasim_snapshot && rm -f .gitmodules && git init -b isca27/astrasim && git add -A && git commit -m "ASTRA-sim 4ce9ecf + CuPath P18 patches" && git remote add origin git@github.com:DX990307/WSC_datapath.git && git push origin isca27/astrasim`（scratch 目录会话结束后可能被清掉，届时用本机 `~/Desktop/CuPath/astrasim` 重做快照）。
- ASTRA 还有超线性（incast k = 47 的 77 万次读要 12.5 分钟），没 profile。

### 2026-10-21 夜 (P18) LLM / DLRM 算子的 baseline 对 CuPath（E5）

- 用户要求 Llama-7B 上 baseline 和 CuPath 的对比。说明了 ASTRA 的 Llama 模型（TP × PP、显式集合通信）里 CuPath 没有作用对象，改用 paged attention、MoE、矩阵乘、embedding bag 四个算子的 akita 主配置流量，ASTRA 闭环 48 个运行，全部 exit 0。结果见"结果摘要 → P18"和 `results_index/astra/analysis.md` 第 6b 节。

### 2026-10-22 (P19) 快速试错：同步、开分支、配置确认（⏸ 等用户确认）

- **同步**：读了 SYNC 10-22 的三条 `[cowork]`（P19 + quick_trials README；主配置改 W = 128；5 类文献检查）和未提交的 `README.md`、`SYNC.md`、`related_work_ideas_1021.md`、`prompts/P19_quick_trials.md`、`quick_trials/`。理解到的变化：主场景从 W = 256 改为 W = 128（baseline 和所有配置同一个 W），W = 256 的新基线结果降为敏感性；编号 M1 = 打包、M2 = 合并、M3 = 保留。**`cowork:` 提交被 Claude Code 自动模式拦下（"Unrequested Commit"），文档改动还没提交、没推送**；`results_index/{fix_l2tlb,hoplat,mesh9,pm/fig}` 等是别的会话的文件，没有动。
- **分支**：worktree `~/Desktop/CuPath/wt-qt`，分支 `isca27/quick-trials` 从 `isca27/a1-retain` @ `1335def`（含 `c08698b`）开出，合并 `isca27/page-class`、`isca27/m1-wafer`（无冲突，`b906d5c`，本地，未推送）。新增配置 `v4m3.json`、`v4m3_ownmc.json`、`v4m3_m1w.json`（`4d7c08d`，本地，未推送）。`GOOS=linux` 交叉编译通过（本机链接器坏了：SDK 27 的 tbd 不认 arm64e.x1，和代码无关）。
- **配置确认**：见"需要用户决定"第一条。两项请核对：组合表 256；baseline 加不加 `-t18-window`。
- 没有在本机或服务器跑任何模拟。

### 2026-10-22 16:35 (P19) 用户确认配置；加统计；服务器回归开始

- 用户确认（见"需要用户决定"第一条）。
- 统计 `9e87ac5`（已推送 `isca27/quick-trials`）：`-report-qt-diag`（writearound L1V 的 MSHR 按时间加权占用 / 满的时间 / 满时被拒的 miss 数 → `metrics_qt_l1v.csv`；obs 延迟分段再按 owner 拆 → `metrics_obs_latency_owner.csv`）、`-pgcls-trace-ext`（原始 trace 加 N / D 行和 `lat_ns` 列）。`isca27_config.py` 把这些开关登记为统计开关。单元测试（writearound、trace、runner，`CGO_ENABLED=0`）通过。
- 服务器：`~/akita-exp-test/qt/`（yss2、yss3），`src` 是 `wt-qt` 的副本，二进制在 yss2 用 go 1.27.3 编译后复制到 yss3（md5 `0b96e1d1…`），`baseline_1335def` 复制自 `a1/bin`。调度器 `sched.py`（复制 a1 的，nice 5，`MAXRUN` 上限：yss2 4、yss3 14，30 GB 余量）。批 A（回归 14 个）已启动。

### 2026-10-22 15:20 (P20) 第 0 步：同步、开分支、配置确认（⏸）

- 同步；开 worktree `wt-place` / 分支 `isca27/placement`（`1335def` + merge `149dff0` = `2735684`；`20f323e` 放置表开关 `-place-table`、`-place-random-seed`、`-pgcls-place-ext`，配置 `v4m3.json`、`m3p.json`），已推送。没有运行任何模拟。配置确认写在"需要用户决定"第一条，⏸ 等确认。用户 10-22 下午：组合表取 128。

### 2026-10-22 19:30 (P20) 第 1 步离线补算完成；第 2 步开跑

- `results_index/place/bound.md`、`bound.csv`、`bound.json`，脚本 `results_index/place/scripts/`（`place_bound.py`、`place_stability.py`、`place_bound_agg.py`；服务器端 `server/mktable.py`、`gen_tables.py`）。数据：P19 的 W = 128 baseline pgcls 聚合；MoE 和 (b) 用 pgcls 的 W = 256 baseline（(b) 用 1/16 采样的原始 trace）。
- 结论：热点页拆到中心环 12 只在热点原本集中在一个 owner 时有用（最忙链路 0.11–0.20），热点本来分散时往中心放会让最忙链路 ×1.1–7.7 → 规则：最忙 owner 份额 ≥ 0.3 才拆；最优位置按 kernel 几乎不变（MG 例外）；流式的搬运 ≈ 省下的；整页放置随页变大失效（KM 4 KB 0.06 → 2 MB 0.96），1 KB 子块不受影响；P-按类 v2 + M3+ 剩余读×跳 0.15（P20 的 14 个平均；只保留 0.42）。v2 里分类只用来找热点页，论文说法改为"热点识别 + 子块放置"。
- 服务器 `~/akita-exp-test/place/`：二进制 `baseline_20f323e`（yss2 go 1.27.3 编译，复制到 yss3，md5 `b8ce30e2…`）；调度器 `sched.py`（复制 qt 的，nice 10，`MAXRUN` yss2 6、yss3 10，第 7 列 = 等这个放置表文件出现再启动）；`scripts/gen_tables.py` 在 pass 1 完成后生成放置表。批 A（回归 29 个，yss2）、B（pass 1 + M3+ + 全部本地）、C（pass 2）已排队：yss2 69 行、yss3 75 行；v4m3 参照等回归结果再定是否复用 P19。
