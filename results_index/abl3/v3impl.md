# P16 第 2 步：`isca27/cupath-v3` 的实现和回归

Claude Code，2026-10-21。worktree `wt-v3`，分支 `isca27/cupath-v3`，**未推送**。服务器目录 `~/akita-exp-test/v3impl/`（yss2、yss3）。

- 实现 commit：`2784487`（父 `29d41c7`）
- 二进制：`baseline_2784487`，`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`，md5 `57b60e1ffda2f9dbc9af433faa6b239c`
- 配置确认：`config_confirm.md`（同目录）

## 1. 分支关系

`isca27/cupath-v3` @ `29d41c7` = `isca27/main-run`（`8446d36`）+ `isca27/obs-m3-inval`（`29d41c7`，只改统计口径）。`8446d36` 本身由以下三部分组成：

| 分支 | 提交 | 在 cupath-v3 里 | 回归 |
|---|---|---|---|
| `isca27/w2-complete` | `d09b18b`（16 个 W2 benchmark + M1/M2/M3 组合 `complete_new`） | 全部包含 | 4 个移植分支各自的 fir、MM（`-max-wg 4096`）和 `ca8b831` 逐位一致（STATUS“W2 移植”） |
| `isca27/fix-lds-m3` | **只有** `5643c95`（M3 kernel 边界失效，`-remote-reuse-kernel-invalidate`，默认关） | 包含 | `m3fix_regression.csv`（同目录）：开关关时 baseline / m1 / m2_old / m3 / complete_new 的 fir、MM 和 `d09b18b` 逐位一致；开关开时只多出 `remote_reuse_boundary_*` 统计行，模拟时间不变 |
| `isca27/obs-stats` | `29bec8b`、`e22c52f`（P13 统计，默认关） | 包含 | SYNC 10-20 晚：统计关时逐位一致，开时模拟时间不变 |
| `isca27/obs-m3-inval` | `29d41c7`（统计口径修正） | 包含（cupath-v3 的基线就是它） | 只改统计；本次 fir / MM 回归覆盖 |

**没有合并的**：LDS 越界崩溃的修复还没做完（另一个会话在 `isca27/fix-lds-m3` 上做，`origin/isca27/fix-lds-m3` 现在仍停在 `5643c95`），所以 cupath-v3 不包含，修完再合。受影响的是 graphtc、npbis 2 次迭代，以及 6 小时规模下崩溃的 sssp、atax、shocsort、ivfflat（graphtc 两处都在）。

## 2. 改了什么（`2784487`）

- **(a) 分配**：`-t18-credit-policy = shared | rtt | near`，`-t18-near-reserve`（默认 16），`-t18-near-hops`（默认 3），实现在 `mgpusim/timing/rdma/t18_credits.go`。
  - 默认（空）跟随 `-t18-dist-credits`：true = rtt，false = shared，所以现有配置不变。
  - 两个 flag 都显式给出而且矛盾时报错（dist-credits=true 对应 rtt 或 near，false 对应 shared）。只在 near 时才能设 near 的两个参数，否则也报错。要求 near-reserve < W。
  - near 的规则：R 项近区只给跳数 ≤ H 的目的地。其余 W − R 项按 rtt 规则分给所有目的地，包括按往返时间的保底份额、借用条件（空位要多于正在等的目的地还没用的保底），并且不抢回；rtt 规则里的量只在这 W − R 项里算。近的目的地先用近区，近区满了再按 rtt 规则用其余 W − R 项。每个事务记下自己占的是哪个区，响应回来时还到同一个区。远的目的地任何时候都进不了近区。
  - 独立 T18 请求方（`t18ReqIssue`）和 M1 + M2 组合（`m1CanSend` / `m1Acquire` / `m1Release`）用的是同一个分配器。
  - 单元测试（`t18_credits_near_test.go`）：
    - 48 个位置上近区加 rtt 份额正好等于 W，rtt 份额和 rtt(W − R) 相同；
    - 远的目的地用不到近区，近区空出来远的也进不去；
    - 随机序列下，通用区的发送 / 借用判断和 rtt(W − R) 逐次一致；
    - near-reserve = 0 时和 rtt 逐次一致；
    - flag 解析和矛盾报错（runner 的 `t18_policy_test.go`）；
    - 组合模式的 `m1CanSend` 确实用到近区（`TestM1M2ComposedUsesNearAllocator`）。
- **(b)** 9 个配置都只靠配置文件拼出来，没有改代码。核对见 `config_confirm.md` 第 3 节。
- **(c) 统计**：`-report-cupath-v3-stats`，默认关，输出 `t18_v3_*`。另外 `-report-gpm-trace` 加了两列：`gpm_active_ns`（这个 GPM 上有 WG 在跑的时间）和 `kernel_all_gpm_active_ns`（所有 GPM 同时都有 WG 在跑的时间）。已有的统计直接复用：
  - 请求方合并分"挂到已发出的项 / 挂到还在排队的项"；
  - owner 的整行读、合并数、表满退回单播的次数；
  - 多播包数和目的地数；
  - 每包 line 数（`remote_bitmap_lines / remote_bitmap_packets`）；
  - 预取捎带数和被用上的数（`piggyback_lines`、`useful`）；
  - M3 的请求方 L2 命中（`remote_l2_probe_hits`、`remote_two_touch_replica_hits`）；
  - 每个 GPM 的 WG 数（gpm trace）。

  新加的统计：
  - 按距离分组（近 ≤ 3 跳 / 中 4–6 / 远 ≥ 7）的时间加权在途数和排队数；
  - 表满时各组有请求在等的时间；
  - 近区的占用、近区满的时间比例、在近区发出的次数、近区满后借用其余 48 项的次数；
  - owner 表的时间加权占用和表满时间；
  - 退回单播的比例、owner 少发的响应数（= 多播目的地数 − 多播包数）。

  组合模式下的排队口径有一个缺口，见 `config_confirm.md` 第 4 节第 3 点。
- **第 3 步**：`akkalat/configs/v3_*.json` 共 8 个文件，加上 `mi200.json`。测试在 `test_isca27_configs.py` 的 `V3ConfigTest`（28 个测试全过），说明在 `configs/M1M2M3.md`。

## 3. 回归（全部退出码 0）

md5 = `cut -d, -f2- stats/metrics.csv | sort | md5sum`，和 STATUS 用的方法一样。"旧"指 `baseline_8446d36`，也就是停掉的正式运行用的二进制；它的分配器和 T18 代码与 `29d41c7` 相同，`29d41c7` 只多改了统计。

| 比较 | 运行（主机） | total_time | md5 | 结论 |
|---|---|---|---|---|
| 新开关全关：fir `-max-wg 4096`，mi200 | 新（yss2） | 26.568 µs | `d56a681494ae` | 和参考 `d56a6814` 逐位一致 |
| 新开关全关：MM `-max-wg 4096`，mi200 | 新（yss2） | 345.433 µs | `22a58b272d89` | 和参考 `22a58b27` 逐位一致 |
| FW `-max-wg 32768`：complete_new + W64 | 旧 / 新 | 29.176 / 29.176 µs | `efdf9401e1f2` / `efdf9401e1f2` | rtt 分配器改写前后逐位一致 |
| FW：`v3_complete`（rtt）对 complete_new + W64 | 新 | 29.176 µs | `efdf9401e1f2` | 逐位一致 |
| FW：`v3_complete_alloc_near` + `-t18-near-reserve=0` 对 rtt | 新 | 29.176 µs | `efdf9401e1f2` | near-reserve=0 等于 rtt，逐位一致 |
| PagedAttention 默认 batch：`v3_complete` 对旧的 complete_new + W64 | 新 / 旧 | 141.644 / 141.644 µs | `5e3801ff3774` / `5e3801ff3774` | 逐位一致 |
| FW：独立 T18（`t18_full,t18_dist`，W = 64，非组合路径） | 旧 / 新 | 43.112 / 43.112 µs | `f31e3620e1ac` / `f31e3620e1ac` | 逐位一致 |
| FW：shared，`v3_complete_alloc_shared`（新）对 complete_new + W64 + 关 dist-credits（旧） | 旧已跑 | 29.991 µs | `e17fdca2ad5f` | **新的那个没跑**（服务器满，见下） |

complete_new + W64 用的是临时文件 `p16tmp/w64.json`（只有 `t18-window: 64`），只放在服务器的 `src/` 下，没有进仓库。墙钟：fir 11.9 分钟，MM 41.9 分钟，FW 6.5–8.4 分钟，PA 5.0–5.4 分钟；峰值内存 3–11.8 GB。

## 4. 没做完的和要注意的

- 6 个运行没跑，包括 3 个配置的短运行和 shared 回归，清单见 `config_confirm.md` 第 5 节。原因：从 02:20 UTC 起两台服务器都被另一个会话的模拟占满（yss2 33 个，yss3 29 个）。其中大部分是 `~/akita-exp-test/v3run/` 用本次二进制 `baseline_2784487` 跑的 `1021_abl_v3-*`，看起来是消融或规模运行，**不是我起的**，也不是按 PID 记录在我目录下的，所以我没有动。另外：
  - 我第一次在 yss3 起第二批时，没先核对数量就起了 4 个，那时另一个会话刚起了 7 个 `baseline_29d41c7`，加起来超过了 8 个。我按记录的 PID 把这 4 个连同子进程停掉，删掉了它们的半成品结果（`pids.txt` 里有记录），后来用"空出位子才起"的调度器重新跑了 3 个。
  - yss2 上我的 fir、MM 起得最早；之后另一个会话起了 8 个，在一段时间里超过了上限。
- 组合模式下请求方表是 1,024 槽，不是 128 槽（owner 表是 128 槽）。complete_new 原来就是这样，需要用户决定，见 `config_confirm.md` 第 4 节。
- 没有动 STATUS / SYNC，没有提交 docs 仓库，没有推送，也没有启动 234 个运行的消融。
