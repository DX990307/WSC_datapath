# 主实验（2026-10-20 晚启动）：baseline vs Complete

用户 2026-10-20 ~19:00 EDT 要求：明早至少有 baseline 和 Complete 的对比。

## 设置
- 二进制：`8446d36`（分支 isca27/main-run = 5643c95 [w2-complete d09b18b + M3 kernel-boundary invalidation] + isca27/obs-stats）。
  两个配置用同一个二进制。源码：`/Users/daoxuan/Desktop/CuPath/wt-main-run`，服务器上 `~/akita-exp-test/main/{src,bin}`。
- baseline = `configs/mi200.json` + `-report-obs-all -report-m1-sharing`（观测统计只记数，不改时序；输出在 `results/<exp_id>/stats/metrics_obs*`、`metrics_m1*`）。
- Complete = `configs/complete_new.json`（M1 T18 full W=1024 dist + M2 + M3 remote reuse + `-remote-reuse-kernel-invalidate=true`），不开统计。
- 两边都有 `-magic-memory-copy`（hpca.json 继承），无 `-verify`，无 `-report-l2-source`。
- 规模：`isca27/results_index/obs/sizes_8h.csv` 的 flags 列；gups 用 fallback `-gups-log-table 22 -gups-update-mult 1 -gups-wgs 6720`；
  sssp、w2-atax、w2-shocsort、ivfflat、graphtc 跳过（LDS crash）。
- 命名：`1020_abl_main-<workload>_mi200_8446d36` / `1020_abl_main-<workload>_mi200+complete_8446d36`（类别 abl；item 中下划线换成连字符）。
- 调度：每台机器一个 `~/akita-exp-test/main/sched.py`（queue.tsv，按估计时长从长到短，baseline/Complete 成对启动；
  内存规则：MemAvailable − 预留（估计峰值×1.3 − 当前 RSS）− 新对 ≥ 30 GB 才启动；<15 GB SIGSTOP 最新的，>40 GB SIGCONT）。
  PID 在 `~/akita-exp-test/main/pids.txt`，完成记录在 `done.tsv`（exit code、墙钟小时）。

## 文件
- `main.csv`：每个完成的 pair 一行（total_time、speedup = baseline/Complete、指令数与 WG 数是否一致、墙钟、峰值 RSS、exit code、host）。

## 状态
（见下方，随运行更新）

### 统计开销（启动后 ~37 min 观察）
- 同一时刻 baseline(+统计) 与 Complete(无统计) 的墙钟进度相当：floydwarshall 221.8k vs 218.5k WG，bfs 371k vs 368k WG，im2col 26.2k vs 22.5k；
  npb-mg / rodinia-lud 的 Complete 领先 10–25%（Complete 本身模拟时间更短，不全是统计开销）。
- 峰值 RSS：各 baseline(+统计) 都不高于对应 Complete；所有 sim CPU ≈ 105–112%。
- 参考（另一 agent 的 obsovh 对照，29bec8b，同配置开/关统计）：bfs-s16 32:49 → 38:23（+17%，RSS +7%）；pagedattn 小规模无差异。
- 结论：开销 < 1.5×、内存无问题，所有 baseline 保留 `-report-obs-all`（含 latency）。
