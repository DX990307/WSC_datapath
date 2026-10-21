# astra：用 ASTRA-sim 交叉验证网络结论（P18）

Claude Code，2026-10-21。用户 10-21 决定用 ASTRA-sim（"很多 wafer-scale 论文都用它"），不用 BookSim2。
这里**没有改 MGPUSim / akita 的代码**，没有碰 `~/exp`、`~/akita-exp-test/` 和别的调度器。ASTRA-sim 装在 yss3 的 `~/astrasim-test/`。
结论见 `analysis.md`。

## 工具和版本

| 项 | 版本 |
|---|---|
| ASTRA-sim | `astra-sim/astra-sim` master `4ce9ecf`（2026-09-19）+ 本目录 `patches/astra-sim/` |
| analytical 后端 | submodule `astra-network-analytical` `e8c5119`（v2.2.1）+ `patches/astra-network-analytical/` |
| Chakra | submodule `astra-sim/chakra` `0e3cd40` |
| 后端 | analytical **congestion-aware**（事件驱动，每条链路一个 FIFO，逐跳存储转发，队列无限长、无反压） |
| 编译 | yss3，Ubuntu 22.04，g++ 11.4，系统 protobuf 3.12，venv 里的 cmake 3.30.5：`protoc et_def.proto ...`，`cmake .. -DBUILDTARGET=congestion_aware && cmake --build . -j 8`（`build/astra_analytical/build/`） |
| ET 生成 | venv 里 `protobuf==3.20.3`，`et_def_pb2.py` 由同一个 protoc 生成 |

本地克隆在 `~/Desktop/CuPath/astrasim`（分支 `cupath-p18`，两个仓库都没有推送到任何地方）。

## 补丁（`patches/`，git format-patch）

原版 ASTRA-sim 不能直接做这件事（调研见 STATUS 10-21 P18）：analytical 后端没有 2D mesh，congestion-aware 只支持 1 维，每个 NPU 同一时刻只能有 1 个在途 send，没有链路统计。补丁都是可选开启的，默认行为不变：

1. **`Mesh2D` 拓扑**（`congestion_aware/basic-topology/Mesh2D.cpp`）：k×k mesh，不回绕；每个 tile 一个 NPU（id = tile = x + 7y）+ 一个路由器（id = 49 + tile），NPU↔路由器是端点链路（带宽同链路，延迟由 `ASTRA_MESH_EP_LATENCY` 给出）；**先 Y 后 X**。网络配置里写 `topology: [ Mesh2D ]`。
2. **链路统计**（`Link.cpp`）：每条有向链路的 chunk 数、字节、忙时间、排队等待、最大队列长度、最忙的窗口（窗口长度 `ASTRA_LINK_WINDOW`）；`ASTRA_LINK_STATS=<csv>` 在结束时输出。
3. **事件队列**（`EventQueue.cpp`）：有序 `std::list`（每次插入线性扫描，开环运行是平方级）换成 `std::map`。同一时刻的事件顺序不变；两个运行改前改后 `msgs.csv`、`links.csv` 逐字节一致，16.7 万次读 176 s → 32 s。
4. **点对点 send 不限在途数**（`HardwareResource.cc`）：`ASTRA_P2P_UNLIMITED=1` 时 SEND 和 RECV 一样不占"每 NPU 1 个通信操作"的位置；集合通信不变。窗口 W 用 ET 的依赖链实现。
5. **SEND 的延迟属性**（`Workload.cc`）：节点属性 `send_delay`（tick）传给原生的 `front_end_sim_send(delay, ...)`，用于 owner 服务时间和开环注入时刻。
6. **逐条消息日志**（`CommonNetworkApi.cc`）：`ASTRA_MSG_LOG=<csv>` 记录 `src,dst,tag,bytes,send_time,arrival_time`。

## 建模

- **时间单位**：交叉验证 1 tick = 1/24 ns（384 GB/s 下 1 flit 正好 1 tick，避免 ASTRA-sim 整数 ns 截断）；LLM 试验 1 tick = 1 ns。带宽字段按 ASTRA-sim 的 1 GB = 2^30 B 换算成精确值（`configs/mesh7_*.yml`）。
- **akita 网络的对应**：`-bandwidth=24` = 每 ns 24 个 16 B flit = 384 B/ns（`=3` 是 48 B/ns）；每跳 32 周期是不占缓冲的全流水线，没有 credit（`akita/noc/networking/switching/switch.go:583-585`、`mesh/mesh.go:408`）。ASTRA-sim 里每跳延迟 863 tick（35.96 ns）、端点链路 115 tick（4.79 ns），由 akita 的零负载数据（sssp 主配置的 fwd / ret 分段）定出，见 `calibration_hops.csv`。
- **消息**：读 = 16 B 请求（1 flit）+ 96 B 响应（6 flit，68 B × 1.25 编码）；写 = 96 B + 16 B 确认；打包的 k 条 line = 32 B 请求 + 16·⌈(4 + 64k)·1.25/16⌉ B 响应。
- **owner 服务时间**：每个 workload 取 akita 的 `rtt − fwd − ret − (owner_queue − 10)`（零负载下的 owner 排队 + 服务 + 等回程，45–100 ns），作为响应 SEND 的 `send_delay`。**不建 owner 的处理能力上限**（akita 里 RDMA / L2 的排队）。
- **流量**（`scripts/traffic.py`）：`dm/raw` 的 `pairs`（请求方→owner 的平均在途数）÷ 该跳数的 akita RTT（Little 定律），再 IPF 到每个请求方精确的远程读数（`rdma_observed_remote_reads`）和每个 owner 的读数（`incoming_trans_count` 按读占比缩放）；写按请求方的读分布分摊。按先 Y 后 X 路由预测的每条链路 flit 数和 akita 实测的相关系数 0.94–0.9998（`traffic_link_check.csv`；只算读时总量差的 2–26% 正好是写）。
- **Complete 流量**（`scripts/traffic_complete.py`）：用 akita Complete 运行自己的 pairs、每个请求方的打包直方图（`remote_batch_size_0k_packets`）、逻辑读数和写数，IPF 到 owner 端服务的 line 数（`t18_owner_reads`）。预测的链路 flit 和 akita Complete 实测的相关系数 0.95–0.9999，总量差 0.3–5%（`traffic_link_check_complete.csv`）。M3 命中和请求方合并不进网络；**owner 合并 + mesh 内多播没有建**（每个包都回单播响应，保守；akita 里被合并的 owner 读 ≤ 7%）。

## 实验

| 组 | 内容 | 运行数 |
|---|---|---|
| 校准 | tile 0 读 1–12 跳外的 GPM，W = 1，384 / 48 GB/s | 2 |
| E1 闭环 | 8 种流量（6 个 workload + 均匀 all-to-all + 47 个 GPM 读 tile 0）× 384 / 48 GB/s × W = 64 / 256 / 448；每个活跃请求方 max(10W, 3000) 次读写；统计窗口 = [10%, 100%] × 第一个请求方做完的时刻 | 48 |
| E2a 同负载开环 | 6 个 workload，按 akita 三个场景（`w64b384`、`w256b48m`、`w2048b48`）的平均速率泊松注入 100 µs | 18 |
| E2b 开环扫描 | 8 种流量 × 2 种带宽 × 预测最热链路 ρ = 0.1 / 0.3 / 0.5 / 0.7 / 0.9 / 1.1（每个运行最多 100 万次请求） | 96 |
| E3 机制（选项 A） | 6 个 workload 的 Complete 流量，同 E1 的 6 组 (带宽, W) | 36 |
| LLM 试验 | Llama-7B 推理，TP 8 × PP 6，相邻 / 随机放置 × 384 / 48 GB/s | 4 |

198 + 4 个运行全部 exit 0。akita 侧的对照数据都来自已有结果（`dm/raw`、`m2w/m2w.csv`、二进制 `4235991`），没有新跑 MGPUSim。

## 文件

| 文件 | 内容 |
|---|---|
| `analysis.md` | 结论 |
| `calibration_hops.csv` | 零负载单程延迟：ASTRA-sim 384 / 48 GB/s 对 akita |
| `traffic_link_check.csv`、`traffic_link_check_complete.csv` | 流量重建的检查（预测 vs akita 每链路 flit） |
| `runs.csv`、`hops.csv` | 每个运行的汇总；每个运行 × 跳数的 fwd / ret / RTT |
| `compare_E1_closed.csv`、`compare_E2a_matched.csv`、`compare_E2b_sweep.csv`、`compare_E3_mechanisms.csv` | 和 akita 的逐项对照 |
| `llm/llama7b_tp8pp6.csv` | LLM 试验 |
| `configs/` | 网络 / 系统配置 |
| `scripts/` | `gen_et.py`（闭环 / 开环 ET）、`traffic.py`、`traffic_complete.py`、`mk_runs.py`、`mk_runs_complete.py`、`run_one.sh`、`run_all.py`（≤ 8 并发，MemAvailable 留 30 GB）、`analyze.py`、`compare.py`、`hop_table.py`、`gen_llm.py`、`run_llm.sh` |
| `patches/` | ASTRA-sim 补丁 |

逐条消息日志（`msgs.csv.gz`，共约 1 GB）留在 yss3 的 `~/astrasim-test/p18/runs/*/`，不进仓库。

## 复现

```
# 本机
python3 scripts/mk_runs.py <runs_dir>; python3 scripts/mk_runs_complete.py <runs_dir>
rsync -a scripts configs yss3:astrasim-test/p18/; rsync -a <runs_dir>/ yss3:astrasim-test/p18/runs/
# yss3
cd ~/astrasim-test/p18 && nohup ../venv/bin/python scripts/run_all.py &
JOBS=8 ../venv/bin/python scripts/analyze.py      # -> runs.csv, hops.csv
# 本机
python3 scripts/compare.py
```
