# P12 测量（T18 / T19）：结果索引

代码：分支 `isca27/p12-measure`（从 `isca27/t17-workloads` `c9cdf64` 开出）。测量用的二进制 `ed1aca0`（`1929100` tracer、`ed1aca0` benchmark 参数；`0b85bef`、`d32520a` 只加分析脚本）。
所有运行：`akkalat/isca27_run.sh` + `configs/mi200.json`（无配置差异，保留 `-magic-memory-copy`，不 verify）+ 统计开关 `-report-wg-footprint -report-m1-sharing -report-gpm-trace`（+ benchmark 参数）。服务器上的原始输出在 `~/akita-exp-test/p12/src/results/<exp_id>/`（yss2 或 yss3，见下表），WG 足迹原始文件（gzip，7–30 MB）不进 git。

## 运行

| exp_id | 主机 | 规模 / 参数 | 模拟时间 | 墙钟 | 峰值内存 |
|---|---|---|---|---|---|
| `1019_diag_p12-pagedattn-b33-s3_mi200_ed1aca0` | yss2 | `-pa-batch=33 -pa-steps=3`（T17 的一半 batch，连续 3 个 decode step） | 7.069 ms | 5:13 | 5.8 GB |
| `1019_diag_p12-pagedattn-b66-s2_mi200_ed1aca0` | yss3 | `-pa-batch=66 -pa-steps=2`（T17 规模，2 步） | 见 STATUS | 见 STATUS | 见 STATUS |
| `1019_diag_p12-moe-skew1.75_mi200_ed1aca0` | yss2 | T17 规模（256 token、hidden 1024、ffn 704、top-6、skew 1.75） | 5.980 ms | 3:04 | 5.9 GB |
| `1019_diag_p12-rgat-s320_mi200_ed1aca0` | yss2 | T17 规模（IGBH-tiny、320 种子、fanout 10,5、hidden 128、1 头） | 15.129 ms | 3:12 | 4.3 GB |
| `1019_diag_p12-pagerank-small-i3_mi200_ed1aca0` | yss3 | `pagerank-t16small`（64,512 节点、816 万边）`-t16-pagerank-iters=3` | 4.982 ms | 4:12 | 5.6 GB |
| `1019_diag_p12-kmeans-small_mi200_ed1aca0` | yss3 | `kmeans-t16small`（344,064 点，transpose + 2 轮） | 1.964 ms | 1:52 | 4.0 GB |
| `1019_diag_p12-floydwarshall-n2048-p8_mi200_ed1aca0` | yss3 | `floydwarshall-p12small`（2048 节点、k = 0..7 共 8 个 pass，每个 65,536 WG） | 1.071 ms | 1:02 | 5.5 GB |

回归和复现检查的运行（`1019_diag_p12reg-*`、`1020_det_p12-*`，都在 yss2）见 STATUS。

## 文件

**T19（WG 足迹，`akkalat/p12_footprint.py`、`akkalat/p12_tables.py`）**。足迹 = 每个 WG 的向量 load 读过的 4 KB 虚拟页集合（在 CU 发出时记录，L1V 命中也算；不含标量 load 和写）。每个 WG 最多记 4096 页（`-wg-footprint-max-pages`），这次所有运行都没有被截断。

- `<workload>_footprint_size.csv`：每个 kernel 一行：WG 数、每个 WG 的页数（均值/中位/最大）、每个 WG 的读请求数、被截断的 WG 数、kernel 读过的不同页数。
- `<workload>_affinity.csv`：每个 kernel × 配对类别一行。类别：`adjacent`（WG i 和 i+1）、`random`（同一 kernel 里随机两个 WG，基线）、按组：PagedAttention `same_seq`（同一序列，组内相邻成员）、`same_seq_head`（同一序列同一 KV 头）；MoE `same_expert`（moe_up/moe_down 同一 expert）、`same_expert_col`（同一 expert 同一列块）。每类最多 20,000 对（固定种子随机抽）。列：Jaccard 的均值/中位/p10/p90、≥0.5 / ≥0.9 / =0 的比例；containment = |A∩B|/|B|（“B 读的页里 A 也读过的比例”）。
- `<workload>_stability.csv`：同名 kernel 两次启动之间（lag 1 = 相邻两次，lag 2 = 隔一次）同一个 WG 的足迹重叠。`same_wg_index`：按 WG 编号配对（硬件只知道编号）；PagedAttention 另有 `same_work_item`：按（序列、分段、KV 头）配对，`same_wg_index_item_moved`：编号相同但工作项变了的 WG。`same_gpm_frac`：两次都在同一个 GPM 上的比例。
- `affinity_summary.csv`、`stability_summary.csv`：上面两类按 workload × kernel × 类别（× lag × 配对方式）汇总（按对数加权）。
- 注意：R-GAT 的 `rgat_proj` / `rgat_agg` 每次启动是不同的层/关系（不同数据），不是同一个 kernel 重复执行，它们的 stability 行不代表“历史稳定性”；PagedAttention `pa_reduce` 的 stability 为 0 是因为每个 step 用各自的 partial/out buffer（为了避开模拟器 kernel 之间不失效 L1 的问题），不代表真实情况。

**T18（M1 共享，`akkalat/p12_m1.py`）**。远程读 = L1V miss 发往本 GPM RDMA 的读（物理 64 B line；owner = 地址 / 8 GB − 1）。

- `m1_summary.csv`：每个 workload × 时间窗（1 µs、10 µs，按 L1V 发出时间对齐到 0）一行：`shared_req_frac`（所在（窗口，line）被 ≥2 个 GPM 读的远程读比例）、`mean_sharers_per_req`、`req_merge_saving`（每个 GPM 在窗口内每条 line 只发一次能省掉的远程读比例）、`owner_merge_saving`（owner 每个窗口每条 line 只服务一次能省掉的比例；含时间上的复用，不等于“同时在途”）、`mean_pair_hops` / `mean_min_pair_hops`（读同一 line 的 GPM 两两之间的跳数 / 最近一对）、`mean_owner_hops_shared`、`multicast_link_saving`（共享 line 上 YX 多播树链路数相对单播跳数之和省掉的比例）、`multicast_link_saving_all`（相对全部远程响应跳数）。
- `m1_inflight.csv`：在途重复（基线时序下）。请求方：一个远程读发出时，同一条 line 已有本 GPM（别的 CU 的 L1V）在途的读 → `dup_same_gpm_frac`；只有别的 GPM 在途 → `dup_other_gpm_only_frac`（及与最近在途 GPM 的跳数）。在途 = L1V 发出到 L1V 收到响应（包含 RDMA 窗口前的排队）。owner 端：owner 的 RDMA 把请求转给本地 L2 时，同一 line 已有请求在 owner 端在途 → `owner_dup_frac`（来自别的请求方 → `owner_dup_other_requester_frac`）。
- `m1_sharers.csv`：workload × 窗口 × 读同一 line 的 GPM 数：（窗口，line）个数和请求数。
- `m1_pairhops.csv`：workload × 窗口 × 跳数：读同一 line 的 GPM 对数。
