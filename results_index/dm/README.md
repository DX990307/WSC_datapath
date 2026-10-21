# dm：数据搬运方向的机会上限（只做离线分析）

Claude Code，2026-10-21。这里只用已有的运行结果做离线分析：没有改模拟器代码，没有跑新的模拟，没有动服务器上的调度器或任何进程，没有提交，也没有推送。分析见 `analysis.md`。

## 数据来源

| 场景 | 含义 | 运行 | 服务器目录（只读） |
|---|---|---|---|
| `w64b384` | 主配置：W=64，每条链路单向 384 GB/s（`-bandwidth=24`） | 25 个 workload 的 baseline 和 Complete | `~/akita-exp-test/v3c384/src/results/1021_abl_v3c-*` |
| `w256b48m` | 网络紧张、表带宽匹配：W=256，48 GB/s，MSHR 16 | 6 个 workload 的 baseline 和 Complete | `~/akita-exp-test/v3m2w/src/results/1021_sens_m2w-w256b48m-*` |
| `w2048b48` | W=2048，48 GB/s | 6 个 workload 的 baseline 和 Complete | `~/akita-exp-test/v3m2w/src/results/1021_sens_m2w-w2048b48-*` |

- 二进制都是 `baseline_4235991`。
- 用的统计开关：`-report-obs-all -report-m1-sharing -report-gpm-trace -report-cupath-v3-stats`。
- `w256b48m` 的 npbcg baseline 是在 m2w.csv 汇总之后才跑完的（18:07 UTC，exit 0，total_time 16.07 ms），这里直接从服务器读的。
- 不引用 W=1024 的结果。

## 统计文件里有什么（已经读代码确认）

所有 `-report-obs-*`、`-report-m1-sharing`、`-report-gpm-trace` 都是在线聚合的结果，**没有逐请求的 trace，也没有数据内容**。

| 文件 | 内容 | 编号 |
|---|---|---|
| `metrics_obs_latency.csv` | 按请求方→owner 跳数分组的逻辑读数：网络 / 请求方合并 / 本地命中 / 多播叶子；各段延迟 | — |
| `metrics_obs_links.csv` | 168 条有向交换机链路各自的 flit 数、字节数、平均利用率和 1 µs 窗口内的最大利用率 | tile |
| `metrics_obs_occupancy.csv` / `_pairs.csv` | 每个请求方 GPM 每 1 µs 的在途采样（RRT 占用），以及按目的 GPM 分开的平均在途数 | tile |
| `metrics_obs_summary.csv` | owner 端同一条 line 的并发、请求方聚合机会、同一 GPM 的重用（唯一的 (GPM,line,写纪元) 数、重用间隔直方图）、多播再读、mesh 字节总数 | — |
| `metrics_m1_windows.csv` / `_pairhops.csv` / `_inflight.csv` | 1 µs 和 10 µs 对齐窗口里每条 line 的共享者数直方图（含 owner 跳数之和、先 Y 后 X 多播树的链路数），以及 L1V 层面的在途重复（同 GPM / 其他 GPM / 最近共享者的跳数） | — |
| `metrics_gpm_trace.csv` | 每个 kernel × GPM 的本地/远程读写数和延迟 | 同时有 `gpm_index`、`driver_id`、`gpm_name`、`tile`、`x`、`y` |
| `metrics.csv` | 每个组件的计数器，包括每个 GPM 的 `t18_owner_reads`、`t18_hopNN_sent`、`incoming_trans_count`、打包大小直方图和 M3 的计数 | 组件名 `GPU[tile]` |

- **GPM 编号**：统计里的 GPM 都是 tile id = x + 7y（`obsTileOf` / `GPU[%d]` 用的是 gpuid），不是 index。
  - index = 创建顺序 + 1 = driver_id；gpm_index = driver_id − 1。
  - tile < 24 时 gpm_index = tile，tile > 24 时 gpm_index = tile − 1。
  - tile 24 是中心的 driver/IOMMU，不是 GPM。
- **路由**：mesh 是**先 Y 后 X**（`akita/noc/networking/mesh/mesh_routing_table.go` 里 FindPort 先比较 Y），不是先 X 后 Y。`p12tracer.go` 的注释也写着 "the mesh routes Y first"。
- **字节模型**（`switching/endpoint.go` 的 `msgToFlits`：编码开销 0.25，16 B flit）：
  - 读请求 12 B → 1 flit；数据响应 68 B → 6 flit，所以每次远程读每跳 112 B；
  - 写 76 B → 6 flit，写确认 1 flit；
  - 位图请求 20 B → 2 flit；位图响应 4 + 64n B；多播响应 74 B → 6 flit。

## 文件

| 文件 | 内容 |
|---|---|
| `raw/{yss2,yss3}.jsonl.gz` | `scripts/srv_extract.py` 在服务器上只读提取的每个运行的统计，74 个运行 |
| `bytehop_summary.csv` | 第 1 项：mesh 字节×跳数（精确）；读的字节×跳数估计；热点 owner 及其位置、份额、平均跳数；最忙的链路；热点 owner 各条出链路的份额 |
| `bytehop_by_hop.csv` | 按跳数分的逻辑读、网络 line、请求方合并、本地命中，以及读的字节×跳数 |
| `bytehop_by_ownerpos.csv` | 按 owner 位置（角落 4 个 / 边 20 个 / 中间 24 个）分的读份额、字节×跳数份额和平均跳数 |
| `links_class.csv` | 角落链路（16 条）、中心 tile 链路（8 条）、中部链路（行/列 2–4 之间，56 条）的字节份额、平均利用率和最大利用率 |
| `bytehop_savings.csv` | Complete 相对 baseline 省下的字节×跳数，按来源分解：本地命中（M3 + 预取命中）、请求方合并、打包、多播、预取的额外字节、残差 |
| `hotspot_whatif.csv` | 把估计的读矩阵按先 Y 后 X 路由到链路上（和实测链路字节的相关系数中位数 0.97），并估算热点 owner 搬到最优 tile、或者做 4 份副本之后的效果 |
| `dup_same_gpm.csv` | 第 2(a) 项：同一 GPM 的重复取；M3 之后仍然走网络的重复取；M3 填入有没有用上 |
| `sharing_windows.csv` | 第 2(b) 项：1 µs 和 10 µs 窗口里的跨 GPM 共享 |
| `push_bound.csv` | 第 2(c) 项：按共享者主动推送的上限（K = 1, 2, 4, 8, ∞；T = 1 µs, 10 µs），以及无用推送的比例 |
| `owner_wait.csv` | 第 2(d) 项：owner 等待 100/200/500 ns 能多合并多少；最近共享者的跳数 |
| `ranking.csv` | 各个方向叠加在 Complete 之上的上限汇总 |
| `scripts/` | `srv_extract.py`（服务器上只读提取）、`runs_{yss2,yss3}.txt`、`load.py`、`a1_bytehop.py`、`a2_sharing.py`、`a3_hotspot.py`、`a4_rank.py` |

重新生成：

```bash
for h in yss2 yss3; do
  ssh $h "python3 - $(tr '\n' ' ' < scripts/runs_$h.txt)" < scripts/srv_extract.py | gzip > raw/$h.jsonl.gz
done
python3 scripts/a1_bytehop.py
python3 scripts/a2_sharing.py
python3 scripts/a3_hotspot.py
python3 scripts/a4_rank.py
```
