# 实验结果索引

每个实验一个目录，名字就是 exp_id，里面只有 `meta.json`（运行参数、commit、`config_deviation`、开始/结束时间、退出码）和 `summary.csv`（关键数字）。完整结果（`stats/`、`stdout.log` 等）很大，不进 git，在代码仓库根目录的 `results/<exp_id>/`（本机）和服务器的 `~/exp/cupath/results/<exp_id>/`。

- `1013_probe-*_f7b9eef`：T11 探针（16 个）。**用的是 baseline 二进制的默认参数，不是论文配置**：P1、P2 的功能性结论有效，P3a/P3b/P3c/P4 的性能数字作废（见 STATUS）。修复死锁之前的几轮（`0ca173f`、`a097f31`、`d1f6d8c`）已作废，没有收录。
- `1013_t12-*`：T12 driver 修复的回归和验证（10 个）。R2、R3 去掉了 `-magic-memory-copy`，只用于 T12 验证，不进论文（见 `config_deviation`）。`R3-...-pre-fix` 是修复前复现的死锁，exit 143 是手动结束，`heartbeat_at_hang.json` 是卡住时的心跳。
- `1013_t14-*`：T14 第一档 + T13 阶段 3 校准（58 个，两套配置 `hpca`、`mi300x`，全部 exit 0），多了一个 `platform.csv`（该运行的全部测量点）。延迟和跳数（`lat-*`、`hop-*`）用 `ca7ea0b`；`0e38983` 的延迟和跳数结果测的是标量缓存，已作废、没有收录。
- `platform/`：T14 第一档汇总（`akkalat/t14_analyze.py` 生成）：每个测量项一个 CSV（两套配置都在里面），`pattern_links.csv` 是按 YX 路由推算的每条链路负载，`summary.json` 是关键数字（拟合、饱和点等）。
