# 26 个 workload 的 6–8 h 规模（baseline + observation 统计，2026-10-20）

二进制 `baseline_d09b18b`（`~/akita-exp-test/w2c/bin`，md5 6f9d3c7e），mi200，`-magic-memory-copy=true`，timing，无 verify。
试跑目录：`~/akita-exp-test/size8h/runs/<name>/`（yss2 8 个、yss3 8 个，每个 45 min 上限，用 `timeout` 自动结束；PID 在 `size8h/pids.txt`）。完整表：`sizes_8h.csv`。

## 假设和方法
- **统计开销按 +15% 计**（obs-stats 还没报开销）。表里"带统计"= 不带统计 × 1.15，目标约 7 h。
- 主 kernel 用掉的 GPM 和 WG Per CU 取自 driver 的 `total WG: N WG Per CU k`。
- 校准：用 heartbeat（30 s）记录模拟时间和完成的 WG 数，取最后 8 分钟的稳态速度外推。只有 FW、I2C、SPMV 是在主 kernel 里按 WG 数外推的，比较可靠；KM 用模拟时间速度和旧的 `-max-wg 8192` 结果核对，两者一致。
- WG 全部能同时驻留的情况（PR、MM、AR、BH、embbag、gups、npbcg）：45 分钟内几乎没有 WG 完成，按模拟时间速度和旧规模外推，属于粗估。
- **两台服务器负载很高**（其他 agent 有 20 多个 sim）。T17 三个 workload 的校准速度比按 T17 第一轮线性外推慢 1.7–2 倍，所以这三个给的是区间。

## 结论
- **满足 6–8 h（带统计约 6.7–7 h）**：im2col（`-max-wg 1048576`）、spmv（`-max-wg 557056`）、kmeans（8192，不变）、floydwarshall（p12small 9216×2）、bfs s19e24、npbcg B inner2、allreduce pc12、bh 688k、cfd 2.06M、lud 1888、mg W 4 cycles。
- **达不到 6 h，原因是规模写死或已经到上限**：
  - matrixtranspose 不截断 4.1 h（width 写死）；
  - fft2d N=4096 是最大值，5.1 h；
  - npbis 2^23 是 4.9 h，下一个合法的 N 约 9.8 h。
  - FW 的自然规模（11584×1）是 4.6 h。如果要优先 footprint（1.07 GB），可以接受这个比较短的时间。
- **需要第二轮校准**：pagerank、MM（WG 全部同时驻留）、PA / MoE / R-GAT（区间太宽）、embbag、gups（TLB 受限，44 分钟里一个 WG 都没完成，规模还没定）。
- **等 LDS 修好后再校准**（这次只做了解析估计）：sssp s20 1 轮、atax 14336、shocsort 9.63M、ivfflat Q720、graphtc s14。
- **不满足"每 CU ≥ 5 个 WG"**：MoE（3/CU）、R-GAT（1/CU，85% 的时间只用到 ≤ 13 个 GPM）、npbis 的 count/scatter（4/CU）。要满足，就得把时间拉到 12 h 以上或者改代码。

## 没完成的部分（权限被拒）
- 协调方（coordinator）转来的要求是：每台机器提到 14 个并发，补跑 MT、fft2d、npbis 2^24、lud 1968、mg W×4、FW p12small 10240×2。这次启动被 auto-mode 拒绝（Interfere With Workloads），我没有绕开。
- 所以 MT、fft2d、lud、mg 用的是旧数据或解析估计，第二轮校准也没有启动。
