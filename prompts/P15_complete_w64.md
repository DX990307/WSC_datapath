# P15：Complete 不用 1024 窗口（W = 64）——baseline 和 M1 + M2 + M3

> 10-20 晚，用户要求："那就不要搞这个 1024 window 试试"。只跑这一批，其他队列照旧冻结。

---
继续 CuPath 的 ISCA 2027 工作。

**0. 先同步**（SYNC.md 第 2 节）
- 读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目（10-20 晚：RDMA 表保持 64 项；M1 的新重心；讲解图 v2；M1 (b)(c) 改回 T18 原版；本文件）。
- 用 `git diff` 读 Cowork 新增和改过的文件：`figs/advisor_1021/`（v2 的 9 张图、`CuPath_M1M2M3_figs_v2.pdf`、`explain2.py`、`old_v1_1024/`）、`tasks/T18_new_m2_two_sided_merge.md`（顶部说明）、`SYNC.md`、`README.md`、本文件。
- 以 `cowork: ...` 提交，推送到 `isca27/docs`。

**1. 用户 10-20 晚的决定**
- RDMA 两端的表保持 64 项，不作为机制放大；**不要启动任何 W > 64 的运行**（这条仍然有效）。
- 本批的 Complete = `complete_new.json` 只改一处：`t18-window` 1024 → **64**。其余照旧：
  - `t18-dist-credits` **保持开**（用户选的）；
  - 表大小用默认值（请求方在途表 2W = 128 槽、owner 合并表 128 槽、等待者池 4 × 128），**不要**设 `-t18-req-entries` / `-t18-owner-entries`（用户："128 槽就行"）；
  - M2（旧 m2 的参数）、M3（`remote-data-path-l2-enable`）、`cupath-dedup-owner = m1` 不变；
  - 保留 `-magic-memory-copy`，`mi200.json` 不改。
- 用户要跑 **baseline 和 M1 + M2 + M3** 两个配置。

**2. 配置文件**
- 在 `isca27/w2-complete`（`d09b18b`）上开分支 `isca27/complete-w64`，新增 `akkalat/configs/complete_w64.json`：内容和 `complete_new.json` 相同，只有 `"t18-window": 64`；`_description` 写明"用户 10-20 晚要求，W = 64，其余同 complete_new"。显式写 64，不要靠删掉这一项走默认值。
- **按长期规则，开跑前先确认配置**：把 `config_used.json` 和 `mi200.json`、`complete_new.json` 的差异各列一遍（预期和 `complete_new` 只差 `t18-window`），再加上运行日志里 `[Config] T18 ...` 那一行（window=64、owner-window=64、req-entries=0 → 128 槽、dist-credits=true），给用户看，用户同意后再跑。

**3. 运行**
- 二进制：`d09b18b`（和 `results_index/w2complete/` 同一个，Complete 的代码相同）。如果必须换二进制，baseline 和 Complete 必须用同一个，并说明原因和回归结果。
- workload：W2 的 16 个新 benchmark，规模和参数**完全照** `results_index/w2complete/README.md` 的 Sizes 表。
  - graphtc 在这个规模下三种配置都崩溃（LDS 越界）：跳过，除非同一个二进制里已经修好；
  - npbis 用 1 次迭代（`-npbis-iterations 1`）；
  - 所以是 15 个 workload × 2 个配置 = **30 个运行**。
- baseline 也重跑（用户要求）。同一个二进制、同样的参数，结果应该和 `w2complete` 的 baseline 逐位一致；这也顺便检查运行间波动（之前 bfs-s16 同一二进制三次差 2–3%）。不一致的要列出来（time、insts、md5）。
- 命名：`<MMDD>_abl_<item>_mi200_d09b18b`、`<MMDD>_abl_<item>_mi200+completew64_d09b18b`（`<MMDD>` = 启动日期）。
- 参数和 `w2complete` 一样：不加 `-verify`，不加 `-report-l2-source`。T18 的统计（`t18_*`）会自动输出，要收进 CSV。
- 放置：yss2、yss3，按内存规则分配（每个 3–7 GB，墙钟 0.5–1.7 小时，参考 `w2complete.csv`）。

**4. 结果**（`results_index/w2complete64/`：`w2complete64.csv` + `README.md`）
- 每个 benchmark 一行：baseline 和 Complete@64 的 time、insts、WG 数、墙钟、RSS；把 `w2complete.csv` 里 W = 1024 的 Complete 加速比也抄进来对比。
- 机制统计：
  - M1：请求方合并比例（分 `req_merged_inflight` / `req_merged_queued`）、窗口满的时间比例（`ReqWindowFullNS` ÷ 总时间）、owner 合并数 / owner 收到的整行读、owner 表满退回单播的次数、多播包数和平均目的地数；
  - M2：wire / logical、平均每包 line 数；
  - M3：请求方 L2 命中远程 line 的次数。
- GMEAN：15 个有效的；去掉 embbag 的 14 个；和 W = 1024 的 2.82× / 3.04× 放在一起。
- gups 照旧标注（只有 1.7% 合并、M2 每包 1.0 line，引用前要拆分机制）。

**5. 不要做**
- 不要跑 W > 64、严格 64 项（`-t18-req-entries=64` 等）、14 CU、6 小时规模或其他任何实验；冻结的队列保持冻结。
- 不要改 `mi200.json` 和 `complete_new.json`。

**6. 收尾**：STATUS 加一行和结果摘要；SYNC 写 `[claude-code]` 条目；分支和 `isca27/docs` 推送（只推 `isca27/*`，不要 force push）。
