# A1：M3 跨 kernel 保留只读远程 line（实现 + 评估）

Claude Code，2026-10-21 / 22。设计和正确性论证见 [`DESIGN.md`](DESIGN.md)，结果分析见 [`analysis.md`](analysis.md)，数据见 [`a1.csv`](a1.csv)。

## 1. 代码

- worktree `~/Desktop/CuPath/wt-a1`，分支 `isca27/a1-retain`（已推送，未 force）：
  - `0c39bab` A1 本体（从 `isca27/cupath-v3` @ `ad0dfe3` 开出）；
  - `c4082ae` merge `isca27/fix-l2tlb-wakeup` @ `c08698b`（新基线：L2TLB 丢唤醒、IOMMU 拓扑、driver drain 同步、bfs 修复）；
  - `1335def` 可选的 `-a1-reset-uncacheable`（10-22 追加，见 DESIGN 2.2b）。
- 主要文件：`akita/mem/cache/writeback/remote_retain.go`（home 写纪元 tracker、选择性失效）、`writeback/{topparser,bankstage,mshrstage}.go`（home 写报告）、`mgpusim/driver/physicalwritehook.go` + `memorycopy*.go` / `api.go`（host 拷贝、free/remap 报告）、`mgpusim/timing/rdma/remote_requester.go`（首次触碰准入）、`remote_reuse_boundary.go`（清除 uncacheable）、`akkalat/baseline/runner/*`（flag、接线、统计）、`mgpusim/benchmarks/microbench/a1check`（专门的正确性测试）。
- 新 flag（默认全关）：`-a1-retain`、`-a1-region-log2`（12）、`-a1-signature-bits`（0 = 精确）、`-a1-signature-hashes`（2）、`-a1-first-touch-admission`（0/1/2，独立于 A1）、`-a1-reset-uncacheable`。
- 新配置：`akkalat/configs/v4_complete_a1.json`（extends v4_complete，A1 打开）、`v4_complete_a1u.json`（再加 reset-uncacheable）。
- 新统计：每个 L2 slice 的 `a1_lines_checked / invalidated / retained / retained_hits / retained_lines_used / unused`；全局 `A1` 行的 `a1_epochs / a1_region_epoch_writes / a1_max_regions_owner_epoch / a1_max_regions_epoch / a1_signature_false_positives`；RDMA 的 `remote_first_touch_admissions`、`a1_uncacheable_resets`。

## 2. 验证（全部在 yss2 / yss3 的 `~/akita-exp-test/a1/` 下跑）

| 项目 | 结果 |
|---|---|
| 单元测试（writeback、rdma、driver、cp、runner） | 通过 |
| A1 关闭逐位一致（旧基线）：fir、MM（`-max-wg=4096`）× mi200 / v4_complete，`0c39bab` 对 `ad0dfe3` | metrics.csv 排序后 16–19 万行全同 |
| A1 关闭逐位一致（新基线）：fir、npbcg S、MG × mi200 / v4_complete，`c4082ae` 对 `c08698b`；npbcg S v4 `1335def` 对 `c4082ae`；embbag 主规模 v4_complete 对 pgcls 的 `149dff0` | 全同（total_time 相同；前几组 metrics 全同） |
| `-verify`，主配置，旧基线：a1check、bfs12、sssp12、npbcg S、MG、CFD、LUD、fft2d、MM-t16small（`-magic-copy-coherent`）、shocsort64k、npbis×2，v4 / A1 / A1+ft2 / A1+ft1 / A1+1 Kbit 签名 | 46 个运行全部通过 |
| `-verify`，新基线，A1（`c4082ae`）同上 11 个 | 全部通过 |
| `-verify`，新基线，A1+reset（`1335def`）10 个 | 全部通过 |
| 完整 Floyd-Warshall（256 节点、256 个 kernel，`-magic-copy-coherent`），mi200 / v4 / A1 / A1+reset | 全部通过（A1+reset：19.9 万行失效、102 万次跨 kernel 命中） |
| KM `-verify` | 所有配置（含 baseline）都失败且错误值相同，benchmark 问题，见 analysis.md 第 4 节 |
| a1check 负对照（把"写过"的判断硬改成 false） | 8208 个不匹配——测试能抓住陈旧副本 |

原始数据：`phase1_verify_raw.csv`（旧基线）、`verify_newbase_raw.csv`（新基线）。

## 3. 实验

- 条件：384 GB/s、W = 256（`-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512`），25 个 workload，参数和 `~/akita-exp-test/pgcls/` 的行完全相同（去掉 pgcls 统计 flag）。用户 10-22：不跑 48 GB/s。
- 对照：baseline（mi200）和 v4_complete 复用 `~/akita-exp-test/pgcls/` 的结果（二进制 `149dff0` = `c08698b` + 统计；pgcls 的 verify2 证明统计开 / 关和 `c08698b` 逐位一致；本目录又证明 A1 关闭的 `c4082ae` / `1335def` 和 `c08698b` 逐位一致）。注意 pgcls 的 baseline 行没有加 `-t18-window` / `-t18-compose-table-entries`（对 baseline 无关）。
- 配置：v4+A1（`c4082ae`）、v4+ft2（A1 关，首次触碰准入 2）、v4+A1+ft2、v4+A1+ft1（7 个 workload）、v4+A1+reset（`1335def`）、v4+A1+reset+ft2（`1335def`）。
- exp_id：`1022_sens_a1-w256b384-<workload>_<配置>_<commit>`；验证运行 `1021_diag_a1*`、`1022_diag_a1*`。
- 服务器文件：`~/akita-exp-test/a1/{sched.py, queue.tsv, done.tsv, launched.tsv, sched.log, ext_a1.py, m3why.py, src/results/…, src_0c39bab/results/…（旧基线验证）}`。
- 本目录 `scripts/`：`sched.py`（v3m2w 的副本，加了每行 commit 列）、`mkqueue_*.py`、`queue_*.tsv`、`ext_a1.py`、`m3why.py`、`collect2.py`（生成 a1.csv）、`analyze.py`（生成 analysis.md 里的表）。
- `m3_wire_breakdown.csv`：新基线 v4 运行的网络 line 按原因分解（两次触碰、自己写过、驱逐、边界失效）。
- 取消的运行：两个 `1022_diag_a1v4-km-n2k-*`（我误排的大输入，手动终止，rc = −15，见服务器 `CANCELLED.txt`）。

## 4. 结论（摘要，详见 analysis.md）

GMEAN（25 个 workload，384 GB/s、W = 256，对 v4_complete；括号内为对 baseline）：

| 配置 | 对 v4 | 对 baseline |
|---|---|---|
| v4 | 1.000 | 1.315 |
| v4+A1 | 1.005 | 1.322 |
| v4+ft2（A1 关） | 1.017 | 1.337 |
| v4+A1+ft2 | 1.020 | 1.341 |
| v4+A1+reset | 1.021 | 1.343 |
| v4+A1+reset+ft2 | **1.049** | **1.379** |

- A1 单独只有 +0.5%：9 个 workload 只有 1 个 kernel；npbcg 网络 line −74% 但时间不变（W = 256 下不受窗口限制）。
- 单 kernel workload 的重复取来自 M3 的两次触碰准入（第二次取占网络 line 的 13–48%），不是驱逐或在途重复；KM、fft2d、FW、MG、CFD、gups、LUD 的重复取主要来自"本 GPM 写过的远程 line 永久不可缓存"。
- 边界清除该标记（reset）：KM 1.39×、FW 1.12×；全部合用：KM 1.65×、FW 1.27×、CFD 1.10×、TC 1.09×；sssp −2.4%、fft2d −1.3%。
