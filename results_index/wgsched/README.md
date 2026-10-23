# 候选 3：WG 派到数据所在的 GPM（计算跟着数据走），两遍法

Claude Code，2026-10-23 凌晨。**未验证正确性**（没有跑 -verify、没有回归；只核对了指令数、WG 数和 pass 1 一致）。

- 代码：分支 `isca27/wg-to-data`（worktree `~/Desktop/CuPath/wt-wgsched`，基于 `isca27/quick-trials` @ `3543cd7`），`decef6e`（代码）+ `a739ac3`（加 `m3p.json`）。二进制 `baseline_a739ac3`。
  - `-report-wgaff`（pass 1，只加统计）：在地址转换器（L1 之前，所以包括 L1 命中）按 (kernel, 展平 WG id, owner GPM) 计数向量访问；kernel 号 = driver 统一启动的顺序号（`kernels.RegisterLaunchIndex`）。输出 `<metric>_wgaff.csv`、`<metric>_wglaunch.csv`。
  - `-wg-map <file>`（pass 2）：每行 `<kernel> <WG 数> <WG0 的 GPM> ...`；driver 的 WGFilter 按表派发；WG 数对不上或没有这一行的 kernel 走原来的连续分块。
- 场景：384 GB/s、W = 128；baseline = `mi200` + `-rdma-max-outstanding=128`（原版 RDMA）；v4m3 / M3+（`m3p`）另加 `-t18-window=128 -t18-compose-table-entries=128`；`-magic-memory-copy` 保留（mi200.json）。
- 服务器 `~/akita-exp-test/wgsched/`（yss2 12 个 workload、yss3 13 个），169 个运行全部 rc = 0。
- **pass 1 的时间和 P19 的 `qt-w128b384` baseline 逐位相同（25/25）**，所以下面的 baseline 用 P19 的。

## 映射规则（`scripts/gen_maps.py`）

每个 kernel：上限 cap = ⌈N/48 × 1.1⌉；WG 按"最大 owner 份额"从高到低排，依次派到它访问字节最多的 GPM，满了就派到有空位、Σ 字节 × 跳数最小的 GPM；没有访问记录的 WG 留在默认 GPM（满了给最空的）。

对照（都是 pass 2 方式，`scripts/`）：
- **只均衡**（`wgbal`，`gen_bal.py`）：每个 kernel 连续等分成 48 块，不看数据。只对默认派发不均（某 kernel 最大/平均 > 1.02）的 10 个 workload 跑。
- **随机均衡**（`wgshuf`，`gen_shuf.py`）：WG 随机打乱后等分，不看数据、不保持连续。11 个 workload。
- **保护**（`wgmapg`，`gen_gmap.py`）：离线预测读×跳省不到 20% 的 kernel 用默认派发。9 个 workload 全部 kernel 都不映射（= baseline，没跑），9 个全部映射（= WG 映射），7 个部分映射（跑了）。

## 结论

1. **整体**：baseline + WG 映射 GMEAN **1.15×**（25 个），保护变体 **1.23×**；v4m3 + WG 映射对 v4m3 **1.07×**（保护 1.19×），M3+ + WG 对 M3+ 1.07×。在 P20 的 14 个 workload 上：WG 1.22×、WG 保护 1.36×，对比 P-整页 1.36×、P-子块 1.63×、P-按类 1.81×、P-按类 + v4m3 2.24×、v4m3 + WG 1.73×、全部本地 2.83×。**WG 映射不如子块放置**，但它不搬数据、只改调度表。
2. **按类**（GMEAN，WG÷base / 保护÷base / v4m3+WG÷v4m3）：流式 1.93 / 1.93 / 2.07（MT、PA）；私有放错 1.55 / 1.58 / 1.24（fft2d 2.01、MoE 2.47、FW 1.24、KM 0.93）；亲和 1.19 / 1.35 / 0.95（MG 1.72、CFD 0.83）；随机共享 1.04 / 1.08 / 1.01（embbag 1.95、atax 1.47、i2c 0.60）；热点广播 1.00 / 1.09 / 0.96（IVF 1.77、npbis 1.57、LUD 1.18；MM 0.40、sssp 0.75）；写共享（gups）0.87。
3. **热点广播 workload 会变差**：热点 owner 只能放 ≤ 1.1× 平均的 WG，其余 WG 被挤到别处、离开了自己的私有数据（MM 本地字节 0.11 → 0.02，远程读 ×2.64；PR 0.12 → 0.02）。在 v4m3 / M3+ 上更明显（PR 0.42、MM 0.52、FW 0.71、CFD 0.69、i2c 0.49）：保留已经吃掉热点，再动 WG 只剩坏处。保护变体按读×跳预测，挡住了 MM / PR / sssp / gups / KM / CFD / npbcg / shocsort / spmv，但挡不住 i2c（0.60）、FW 在 v4m3 上（0.71）、tc（0.95 / 0.86）——**预测要按"保留之后剩下的远程读"算，不能按原始字节**。
4. **收益来源要分开看**（指令数、WG 数在所有核对过的 workload 上和 pass 1 相同）：
   - **真局部性**：PA（WG 1.84×；只均衡 1.10×、随机 1.05×；远程读 ×0.37；全部本地 1.96×）、IVF（1.77×；只均衡 1.00×、随机 1.06×；远程读 ×0.32；全部本地 2.42×）、MoE（2.47×；远程读 ×0.26；全部本地 3.06×、P-子块 2.51×）、FW（1.24×；随机 0.05×）、LUD（1.18×；只均衡 1.06×、随机 0.94×）、atax（1.47×；只均衡 1.10×、随机 0.98×）。
   - **主要是默认派发的不均衡 / 错位**（baseline 派发的问题，不是"计算跟数据走"）：fft2d（2.01×，只均衡就 1.57×：默认按 ⌈N/1344⌉ 分块让 308 个 CU 空闲、各 GPM 时间最大/平均 1.83，而且 WG 和数据错位，只均衡就把本地字节从 0.02 拉到 0.59；数据映射在只均衡之上再 1.28×）；npbis（1.57×，只均衡 1.64× 反而更好）；MG（1.72×，只均衡 1.00×、随机 0.82×，远程读 ×0.34——这个其实主要是局部性，默认不均只是一部分）。
   - **存疑，不能算局部性**：MT（2.02×）和 embbag（1.95×）。两者远程读都降了（×0.54、×0.64），随机均衡只有 1.20× / 0.97×，但**全部本地都是 1.00 / 1.01×**（`-force-local-data-access` 核对过：L1V 缺失走本地 L2，L2 缺失走本地 DRAM），P20 的各种放置也都是 1.00×——说明这两个 workload 的时间对数据在哪里不敏感，2× 应该来自 WG 并发组合的变化（MT 每次 DRAM 读都开新行：activate 数 = 读数 1.37 M；VMem CPI 减半）。需要单独查（例如按时间看 DRAM bank / L2 slice 的负载），**不要当作"计算跟数据走"的证据**。
5. **负载均衡**（每 GPM 的 WG 数最大 / 平均，按 WG 数加权的 kernel 平均）：映射后 1.00–1.21（大多 1.10–1.12，npbcg 1.21、LUD 1.17、MoE 1.16 是小 kernel 取整）。默认派发本身 1.00–3.76：npbcg 3.76、MoE 3.00、LUD 2.37、npbis 1.90、MG 1.61、shocsort 1.50、IVF 1.34、atax 1.32、fft2d 1.31、PA 1.21，其余 ≈ 1.00。
6. **在线近似没做**（时间不够）。从数据看，在线版首先要知道"这个 WG 的数据保留之后还剩多少远程读"和 owner 是否是热点，否则热点类必然倒退。

## 表

全表见 `table.md`（列：类别 = pgcls 的 top_class，`*` = 混合；K = kernel 数；本地字节 = 离线按 pass 1 统计预测的、访问字节落在 WG 所在 GPM 的比例；WG/GPM max/avg；各配置对 baseline 或对各自无映射版本的加速；P-整页 / P-子块 / P-按类 / P-按类 + v4m3 / 全部本地是 P20 的 `20f323e` 运行，相对 P19 baseline）。远程读 / 写比例和各 GPM 时间最大/平均在 `remote_traffic.tsv`（baseline + WG 对 pass 1）。离线映射摘要在 `maps_summary/`。运行列表 `runs.tsv`（`collect.py` 输出：子目录、exp_id、主机、rc、total_time）。

## 注意

- 未验证正确性：WG 只是换了 GPM，WG 内容不变；但没跑 -verify。
- 计数位置在地址转换器（L1 之前），所以主要 owner 是按全部访问（含 L1 命中）选的，不是按 L1 缺失。
- PR、MM 等的离线"本地字节"下降是规则本身的结果（最多字节 owner 优先 + 容量上限），按读×跳仍是 0.95；换成"读×跳最小"或者"按保留后的远程读"可能好一些，没试。
