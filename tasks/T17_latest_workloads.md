# T17 新 workload：最新的、page placement 帮不上忙的负载（PagedAttention、MoE、R-GAT，DLRM 备选）

**状态**：`[已定]`（用户 10-16：“可以试试吧”）· **依赖**：mi200 冻结版 `isca27-mi200-v1`；第 0 步就是原来的 T1 · **取代**：T5a、T5b、T9 的 MoE（T5c 的 DLRM 降为备选，T5d 暂停）· **目标时间**：10-27 之前，三个 workload 都能在 mi200 上跑出 baseline · **工作量估计**：每个 2–3 天

> 🔒 **执行前必做**：按 `../CONFIG.md` 做配置确认，等用户确认之后才能运行。**不改 mi200 的任何参数**，所有运行保留 `-magic-memory-copy`，不跑 verify（D13），用合理性检查代替。
>
> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027，baseline 改为 mi200（D19）。审稿人 F 批评 benchmark 太旧。用户的判断：现有 benchmark 的 page placement 太好，大部分访存都在本地，wafer 层没有优化空间。新的主线候选是 W1（GPM 在 mesh 上的位置导致负载不均，见 T16）和远程侧机制（W2/W3）。T14 的 all-to-all 已经显示：kernel 完整跑完时，角落 GPM 比中心慢 1.43×。所以新 workload 需要同时满足：
> 1. **最新**：2023 年以后公认的应用或 benchmark；
> 2. **page placement 帮不上忙**：访问由数据决定（间接寻址、路由、采样），任何静态的页放置都没法让它变成本地访问；
> 3. **模拟器能跑**；
> 4. **每个实验 3–5 小时，而且 kernel 要完整跑完**。
>
> 讨论记录在 `../ideas_from_T14.md` 的最后几节。索引：`../README.md` · 进度写入：`../STATUS.md` · 同步：`../SYNC.md` · 移植指南：`../guides/G1_port_workload_to_mgpusim.md`

## 模拟器的约束（Cowork 读代码得到的，Claude Code 第 0 步核实）

- **不支持 global atomic**：`emu/alu_flat.go` 只实现了 FLAT load/store（opcode 16–31），`timing/cu/vectormemoryunit.go` 遇到其他 FLAT opcode 直接 panic。
- **不支持 MUBUF/scratch**：寄存器一旦溢出到 private memory 就会 panic。kernel 里不要用 private 数组，候选表、top-k 之类的缓冲放进 LDS。
- **LDS 只实现了 7 条 DS 指令**（`emu/aluds.go`：opcode 13、14、54、55、78、118、119）：没有 LDS atomic，没有 `ds_bpermute`，所以不能用 sub-group 函数。
- 已实现：`v_exp/log/rcp/rsq/sqrt_f32`。可以复用的现成算子：`benchmarks/LLMbenchmarks/operators/native/`（gemm、row_softmax、layernorm、gelu、embedding），以及 `benchmarks/llm/kvcache`（只是访存代理）。
- 编译链：OpenCL C → `clang -target amdgcn-amd-amdhsa -mcpu=fiji -mcode-object-version=2`（参考 `microbench/platbench/build_hsaco.sh`，在 yss2 上编译）。

## 写法原则

1. **unified 写法**：一个 kernel 覆盖 48 个 GPM，buffer 用现有的分配器（按 48 等分），不为 workload 专门调整放置。原因：W1 的机制作用在 driver/CP 的 WG 分配上。
2. **不用 atomic，统一 pull**：每个 WG 自己去读需要的数据，写只写自己负责的输出。需要 atomic 才能建的索引结构（MoE 的排列索引、GNN 的采样子图、KV 的块表）**在 host 端预先算好**，用 Go 生成，或者用 Python 生成后由 Go 读入。论文里要写明这一点；写方向的 scatter-add 测不了，列为局限。
3. **输入分布要用真实的**（长度分布、路由的不均、真实的图），只缩小规模。需要下载数据集时，先报告大小和来源，等用户确认。
4. fp32；多 kernel 的 workload 在 kernel 之间不需要 host 介入（除了 launch）。

## 三个 workload（加一个备选）

### W-A PagedAttention decode（`pagedattn`，vLLM SOSP'23）
- 形状：Llama-3-8B 的注意力（32 个 Q 头、8 个 KV 头、d = 128，GQA 4:1），1 层，只做 decode 的 attention（QKV 投影和 FFN 不做，它们是本地型 GEMV）。
- KV 块 16 个 token；**块表在 host 端模拟 vLLM 的分配器**：先随机地分配、释放若干轮，再给当前 batch 分配，使每个序列的块分散在整个池里。
- 序列长度按真实分布（优先 ShareGPT 或 LMSYS-Chat 公开统计的长度分布；拿不到就用参数写明的对数正态分布），batch 32，平均约 1K token。
- kernel：一个 WG 对应 (序列, KV 头, 分段)，读块表 → gather K 块 → QK 点积 → 分段内 softmax（exp）→ gather V 块加权求和；第二个 kernel 做分段间的 reduce（FlashDecoding 的做法，不用 atomic）。
- 预估流量：KV 约 256 MB，大部分是远程。

### W-B MoE 层（`moe`，DeepSeekMoE/DeepSeek-V3、DeepEP）
- 形状：DeepSeekMoE 16B 的结构按比例缩小：hidden 1024、expert 中间维 704、64 个 routed expert、top-6（是否加 shared expert 写在 STATUS 里）；1 层，256 个 token（decode batch）。
- kernel：gating（GEMV + softmax + top-k，top-k 放在 LDS）→ **排列索引在 host 端算**（gating 的结果拷回 host 后算按 expert 分组的排列，再拷回 GPU；这一步的拷贝用 magic copy，不计时，在 STATUS 里注明）→ dispatch（按索引 gather token）→ expert FFN（按 expert 分段的 GEMM + SwiGLU）→ combine（每个 token 拉取自己 top-k 个 expert 的输出并加权，pull 方式）。
- **expert 负载的不均**：gating 权重是随机的，路由会接近均匀，不真实。给每个 expert 加一个偏置，使 expert 负载的最大/平均约为 1.5–2（⏸ 具体分布等用户定；另跑一个均匀版作为对照）。
- **风险**：decode 时主要在读权重（约 550 MB），远程比例可能不高。先跑小规模、看 `-report-l2-source` 的远程比例，在 STATUS 里报告；太低的话，再讨论要不要改成 prefill。

### W-C R-GAT 风格的采样聚合（`rgat`，MLPerf Inference v5.0 的 RGAT，IGBH 数据集）
- 数据：IGB/IGBH 的小规模版本（先核实规模、能不能下载、许可，⏸ 报告用户）。拿不到就用 ogbn-products，并在 STATUS 里写明“不是 MLPerf 的数据”。
- **采样在 host 端做**：512 个种子点，fanout (10, 5)，生成每一层的子图 CSR。
- kernel：每层、每种关系各一个 kernel；每个目标点一个 WG，拉取邻居特征 → 线性投影（可以先投影再 gather）→ attention 分数（点积 + LeakyReLU）→ 邻居间 softmax → 加权求和；关系之间求和由下一个 kernel 做。
- 预估流量：约 3 万个点 × 4 KB ≈ 120 MB 的随机 gather。

### W-D（备选）DLRM embedding bag（`embbag`，只做推理）
- 26 张表 × 100 万行 × 128 维；batch 2048，每次查表 pooling 约 20 行，下标按幂律分布（参数写明）。每个 (样本, 表) 一个 WG，gather 后求和。
- 只有在 W-A、W-B、W-C 中有一个卡住时才做。

## 步骤

**第 0 步：指令检查（原 T1）**
- 每个 workload 先写最小的 kernel（只保留访存和关键算子），用很小的输入在 emu 模式和 timing 模式下各跑一次。
- 把 panic 的 opcode 列成表写进 STATUS。
- 缺的如果是 ALU 指令：⏸ 报告用户，批准后再补（属于改模拟器，补完要做回归：mi200 下 fir 和 MM 的统计完全一致）。
- 缺的如果是 atomic、scratch 或 DS：改写 kernel 绕开，不改模拟器。
- 另外用 `llvm-objdump` 检查生成的代码里没有 `buffer_`/`scratch_`、`flat_atomic`、`ds_` 中没实现的指令，并把这一步做成编译脚本的一部分，发现就报错。

**第 1 步：合理性检查**（代替 verify）
- kernel 数、WG 数是否符合预期。
- footprint：实际访问到的字节数，要超过 wafer 的 L2 总量 96 MB。
- 没有越界：所有 gather 的下标都在 buffer 范围内，host 端检查。
- 再用 emu 模式跑一个极小的输入，和 host 端的参考实现（Go 或 numpy）比对输出。只做一次，不放进正式实验。

**第 2 步：定规模（每个实验 3–5 小时）**
- 先按 1/8 规模试跑 mi200 baseline，用心跳（`-heartbeat-file`）测出每分钟完成的 WG 数，以及远程和本地流量。
- 线性外推出 baseline 约 3 小时的规模，就用这个规模。
- 参考模型：wall 分钟 ≈ 3 + 远程 MB / 3.5 + 本地 MB / 27（T14 实测，粗略）。
- 超过 5 小时就缩小输入，**不用 `-max-wg`**（W1 需要 kernel 完整跑完）。
- ⏸ 把每个 workload 定下的规模、预计耗时和内存占用给用户确认。

**第 3 步：第一轮正式运行**（mi200，规模确认之后）
- 每个 workload 跑 baseline，加 `-report-l2-source`；如果 T16 的 per-GPM tracer 已经有了，就一起打开。
- 报告：远程访问比例、远程延迟占比、远程访问的 owner 分布（集中度）、每个 GPM 的结束时间和 7×7 热力图（有 tracer 的话）、wall 时间、峰值内存。
- 结果写进 STATUS 的“结果摘要”，CSV 放进 `results_index/t17/`。

**之后（`[提案]`，不要开始）**：T15 的 6 组 oracle 和 O4 放置 oracle 用在这些 workload 上；m2/Complete；W1 的机制。

## 需要用户决定的事（遇到时停下来）
1. 第 0 步发现缺 ALU 指令时，是否补到模拟器里。
2. MoE 的 expert 负载不均程度。
3. R-GAT 的数据集（IGB 的小版本，还是 ogbn-products）。
4. 每个 workload 的最终规模。
