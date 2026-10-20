# CuPath M1 Goal-Mode Prompt

请开启 Goal Mode，持续完成以下代码审计、M1 重构、正确性验证、实验、结果分析和论文更新。在所有要求完成并经过最终审计前不要停止。若实验较慢，应持续监控并推进，不得用截断 workload、平均分配 work-group 或挑选结果代替正式实验。

## 1. 总目标

保留并冻结现有 M2 和 M3，将主要精力集中在重新设计 M1。

新的 M1 是 **Filter-Guided Paired-Read Aggregation**：一个真实的 64B demand 到达 L2/DRAM 边界后，系统根据真实 demand 学到的访问模式，以及每个 L2 slice 的 Cuckoo Filter 所提供的 pattern/resident/pending 信息，决定：

- 只发送原始 64B demand；或
- 将它与同一相邻 cacheline pair 内的 sibling 组成一个内部 paired-read aggregate；memory controller 必须把该 aggregate 拆成两个 64B DRAM transactions，并把 sibling 返回到现有 L2。

这里的 paired-read aggregate 只表示 **L2 到 memory-controller 前端的内部 aggregate descriptor**，不表示 AMD HBM 支持 128B transaction。AMD 文档给出的 read transaction 是 32B 或 64B，write 是 64B；Micron 的 `DQ[127:0]` 是每 channel 的 128-bit 数据总线，不是 128B transaction。BL4 在完整 128-bit channel 上传输 64B，在 64-bit pseudo channel 上传输 32B。必须明确区分：

- L2-controller aggregate descriptor 数量和大小；
- 64B HBM transaction 数量；
- 实际传输的字节；
- DRAM model 中的物理 access unit；
- HBM native burst 数量。

禁止把一个 paired-read aggregate 等价为一个 HBM transaction 或 native burst。一个 aggregate 必须产生两个 64B channel transactions（或等价的四个 32B pseudo-channel bursts）；性能收益只能来自共享 row activation、降低上游管理开销和后续 sibling reuse，不能来自删除物理事务。

## 2. 机制边界

### M1：Filter-Guided Paired-Read Aggregation

M1 必须满足以下 workflow：

1. L1 miss 按原有路径形成一个真实的 64B read demand。
2. L2 观察真实 demand stream，用小型、page-local predictor 判断相邻 cacheline 是否可能很快被访问。
3. 每个 L2 slice 使用现有的 Cuckoo Filter 查询候选 sibling：
   - `resident`：候选 line 是否可能已经在本 slice 的 L2 中；
   - `pending/inflight`：候选 line 是否已经在获取途中；
   - 必要时使用 pattern metadata 过滤低置信度扩展。
4. 只有以下条件同时满足时，才把真实 64B demand 与 sibling 组成 internal paired-read aggregate：
   - predictor 由真实 demand 训练，并对紧邻的 sibling 给出稳定预测；
   - sibling 与 demand 是同一自然对齐 cacheline pair 中的相邻两行；
   - 不跨 4KiB page；
   - 不跨 L2 slice、memory controller 或不兼容的地址映射边界；
   - Cuckoo Filter 没有表明 sibling 已 resident 或 pending；
   - 精确 tag/MSHR 检查没有发现重复；
   - L2 MSHR、fill path 和 DRAM queue 有安全余量。
5. memory controller 将 aggregate 拆成两个 64B transaction；仅允许同一 aggregate 的两个 transaction 在同一 row 时共享 activation，不得借机对无关请求做全局 reorder。
6. 若任意条件不满足，必须立即发送原始 64B demand。不得等待 timeout，不得阻塞真实 demand，不得设置全局 scheduler。
7. aggregate 返回后：
   - demand half 完成原始请求；
   - sibling half clean-fill 到现有 requester L2；
   - 更新 resident/pending filter 状态；
   - 后续真实 demand 可以命中该 L2 line，或合并到其现有 MSHR。
8. M1 只作用于 read。不得扩展 partial write，不得进行 speculative write，不得新增 cache、sibling buffer、L1.5 cache 或隐藏的数据存储结构。

M1 不是一个独立发送 64B 请求的传统 prefetcher。虽然 sibling 具有 speculative fetch 的性质，但它必须依附于一个已经需要访问 DRAM 的真实 demand，以 aggregate 形式进入 controller，再被诚实拆成两个 64B transaction，并由 Cuckoo Filter 避免无用或重复扩展。

### M2：Remote Request Aggregation

冻结当前 M2 的功能和主要参数，不重新设计：

- 同 remote cacheline 的 inflight deduplication；
- 按 owner 聚合 remote requests；
- 有限 request/response width、pipeline latency 和 outstanding capacity；
- 一个 owner transaction 完成后向所有 requester fanout。

除非发现会导致错误、死锁或与新 M1 不兼容的具体 bug，否则不得修改 M2 的策略、阈值和性能行为。若必须修改，先记录问题、最小补丁和前后统计。

### M3：Remote Data Reuse in Existing L2

冻结当前 M3 的功能和主要参数，不重新设计：

- remote data 只在出现重复访问证据时保存在 requester 的现有 L2；
- 使用 filter 避免不必要的 requester-L2 lookup，并跟踪 remote recurrence；
- 不新增 requester cache、replica cache 或 L1.5 cache。

除非发现正确性或兼容性 bug，否则不得修改 M3。论文中统一称为 requester L2，不使用容易造成新 cache 歧义的名称。

## 3. Cuckoo Filter 的角色

Cuckoo Filter 不是装饰性结构，也不单独创造 locality。它在 M1 中必须有可测量的独立作用：

- 阻止 sibling 已在 L2 时的重复扩展；
- 阻止 sibling 已 pending/inflight 时的重复扩展；
- 让稳定 pattern 才能触发宽粒度访问；
- 在可靠 negative 时减少不必要的精确 lookup，但 positive 必须由精确 tag/MSHR 检查确认。

优先复用每个 L2 slice 已有的 typed Cuckoo Filter，而不是再增加一个 filter。若为避免 M1 与 M2/M3 的状态清理冲突需要增加 logical type，可以增加 type tag，但不能复制整套 filter storage。必须报告 filter 数量、每个 filter 的容量、总存储开销、查询端口/吞吐和估算 lookup latency。

## 4. 禁止事项

- 不恢复旧的独立 64B prefetch queue 作为正式 M1。
- 不设置 fixed batching timeout、HLQ 或 magic `max wait`。
- 不因为等待 sibling 而延迟真实 demand。
- 不增加新的 cache、L1.5 cache、全局 reorder buffer 或 sibling data buffer。
- 不修改 M2/M3 来掩盖 M1 的问题。
- 不针对单个 benchmark 写特殊判断或硬编码参数。
- 不以 simulator 当前恰好按 128B 计时为由，宣称 HBM 天然一次完成 128B。
- 不以 `--max-wg` 截取 workload 前缀作为正式结果，也不将固定 WG 数平均分配给 kernel。pilot 必须明确标注，不得与 formal result 混用。
- 不以达到预设 speedup 为由篡改模型。1.5x 只能是实验结果，不能是实现约束。

## 5. 先完成模型审计

实现前必须记录并验证：

- L1/L2 cacheline 大小；
- L2 slice address mapping；
- memory-controller interleaving；
- GPU 数、每 GPU controller/bank 数；
- DRAM bus width、burst length、row/bank mapping；
- simulator 如何把单行 request 和 paired-read aggregate 拆成物理访问；
- 单行与 paired-read aggregate 在当前模型中的 command、queue、data-bus 和 row-activation 成本。

当前正式模型必须使用 128-bit full-channel bus 和 BL4，因此一个物理
access unit 是 64B。paired descriptor 必须在 controller 前端原子暴露、
随后拆成两个独立的 64B requests；frontend read count、physical read
count 和 transferred bytes 必须保持一致。收益只能来自提前填入以后
真正使用的 sibling、相邻请求连续调度和同 bank/row 的 activation reuse，
不能来自错误地删除物理传输。

## 6. 实现要求

1. 在独立 branch 上工作，保留用户已有修改，不覆盖无关 dirty files。
2. 正式配置增加独立 M1 flag，例如 `-l2-granularity-adaptation-enable`；旧 M1 只保留为 diagnostic config，不得与正式 M1 混淆。
3. M1 predictor 只由真实 demand 训练，默认只预测直接 sibling，不做多级 lookahead。
4. expansion decision 应尽可能晚地发生在 L2 向 DRAM 发出 read 之前，但不得等待 filter 结果；filter 未及时返回时直接 fallback 64B。
5. paired-read response 必须按地址正确拆成两个 64B line，各自进入现有 L2 block/MSHR。真实 sibling demand 若在途中到达，应合并到对应 MSHR，而不是再次访问 DRAM。
6. eviction、flush、reset 和 cancellation 必须正确删除 resident/pending 状态，不能产生 false negative 或 stale pending。
7. 保持 L1 为 16 MSHRs；不得为了提高 M1 而扩大 cache、MSHR 或 DRAM bandwidth。
8. 所有新统计必须线程安全、可重置，并输出到 benchmark stdout/CSV summary。

## 7. 必须增加的统计

至少报告：

- real read demands；
- pattern observations / stable predictions；
- sibling candidates；
- expansion attempts / accepted expansions；
- 单行 descriptor 和 paired-read aggregate descriptors、bytes；
- 64B HBM transactions、32B pseudo-channel bursts / transferred bytes；
- suppression：resident-filter、pending-filter、exact-tag、exact-MSHR、page、slice/controller mapping、MSHR pressure、DRAM pressure、filter-not-ready；
- sibling fills；
- sibling demand merged while inflight；
- sibling demand hits after fill；
- useful sibling lines/bytes；
- unused sibling evictions/bytes；
- coverage、accuracy、timeliness、lateness；
- L2 demand lookup/hit/miss；
- DRAM queue occupancy、bank utilization、row hit/conflict；
- execution/driver time。

定义必须清晰：

- `useful`：一个扩展得到的 sibling 后来被真实 demand 使用；
- `late`：真实 sibling demand 到达时数据尚未完成，但能够合并 inflight；
- `wasted`：sibling 在 eviction/reset 前未被真实 demand 使用；
- `accuracy = useful sibling / filled sibling`；
- `coverage = demands served by sibling fill or inflight merge / eligible sibling demands`。

## 8. 必须提供的诊断配置

诊断阶段至少能运行：

1. Baseline / always-64B；
2. Old M1 independent-64B-prefetch；
3. Cuckoo Filter only，不扩展；
4. Always-expand，受 correctness/resource boundary 约束；
5. Predictor-only expansion，不做 filter gate；
6. Adaptive expansion without Cuckoo Filter；
7. New M1：predictor + Cuckoo Filter + resource gate；
8. M2 only；
9. M3 only；
10. Complete = New M1 + frozen M2 + frozen M3。

其中 4–6 只用于解释因果，不能替代正式 ablation。

## 9. 正确性与构建验证

先完成：

- `gofmt`；
- 相关 Akita cache/DRAM 单元测试；
- MGPUSim RDMA 回归测试；
- runner build；
- Python config/process/invariant tests；
- targeted tests：地址对齐、page/slice/controller boundary、response split、MSHR merge、filter reset/eviction、write 不扩展、资源不足立即 fallback；
- race/deadlock audit；
- 确认 M2/M3 独立配置的 counters 和行为与冻结版本一致。

## 10. 实验流程

### 阶段 A：小规模 correctness smoke test

只验证能结束、数据正确、counter 合理，不用来报告 speedup。

### 阶段 B：代表性 M1 screen

选择能够覆盖 sequential、streaming、irregular、local-only 和 remote/mixed 的少量 benchmark。比较第 8 节的诊断配置，回答：

- paired descriptor 是否被诚实计为两个 controller/physical 64B reads；
- 是否减少 row activation、关键路径等待或后续 demand latency；
- 是否只是增加传输字节；
- Cuckoo Filter 比 predictor-only 少了多少重复/无用 expansion；
- useful sibling 是 L2 hit 还是 inflight merge；
- speedup 是否与 removed work 一致。

若 M1 没有稳定收益，先根据统计修复通用根因；不得逐 benchmark 调参。

### 阶段 C：正式论文实验

只运行论文采用的 14 个 benchmark，使用完整、统一且可复现的 workload 设置。正式 ablation 为：

- M1 only；
- M2 only；
- M3 only；
- Complete。

Baseline 可以复用，但必须证明 binary、workload、sample policy、GPU/config 和 measurement definition 完全兼容。MM、MT、SPMV 等复用结果也必须经过同样审计。

不得用 workload 前缀截断或平均 WG 分配。若必须 sampled execution，应按已有 kernel/branch sampling 语义运行，并记录每个 benchmark 实际执行的 kernel、WG 和 coverage。

## 11. 决策标准

M1 是否保留，不只看 speedup，还必须同时满足：

- correctness 通过；
- controller request 或关键路径工作量真实减少；
- useful sibling 明显高于 wasted sibling；
- DRAM/L2/MSHR 压力没有系统性恶化；
- Cuckoo Filter 相比无 filter 的 adaptive expansion 有可测量的独立贡献；
- 对大多数适用 benchmark 有效，不依赖单一 benchmark；
- M2/M3 和 Complete 没有回归或死锁。

目标是 14 个 benchmark 中约 10–12 个获得正收益，但不得为实现该数量而进行 benchmark-specific optimization。若达不到，应诚实报告机制适用条件和负收益原因。

## 12. 论文更新

只有正式证据完成后才更新论文。M1 应诚实描述为：

> CuPath uses demand-trained spatial prediction and per-slice membership information to form a paired read aggregate. A real 64B miss carries its sibling only when that line is predicted useful, absent from the L2, not already pending, and resource-safe; the memory controller still executes two 64B HBM transactions but can preserve their common row activation. Otherwise, the demand proceeds immediately as one 64B transaction.

论文必须强调：

- Cuckoo Filter 决定“不应重复获取什么”，predictor 决定“什么可能有用”；二者共同门控 granularity adaptation；
- M1 不等待 batch，不新增 cache，不发送独立的 speculative request；
- sibling 保存在现有 L2；
- M2/M3 是冻结的 remote aggregation 和 existing-L2 reuse；
- 与 MCM-GPU 的 L1.5/new-cache 方案对比时，明确 CuPath 不增加新的 cache level；
- 报告 removed work → traffic → latency → performance 的证据链；
- 不把 paired-read aggregate 写成单个 HBM transaction 或 native burst。

## 13. 最终交付

完成后交付：

- branch、commit 和 clean/known-dirty 状态；
- 关键代码文件和 workflow；
- M2/M3 freeze audit；
- 测试与构建结果；
- 诊断实验表；
- 14 benchmark 的 baseline/M1/M2/M3/Complete driver time 与 speedup；
- geomean、正/负收益 benchmark 数量；
- M1 工作量、filter 独立贡献和 DRAM pressure 图；
- 失败/回归原因；
- 更新后的论文 Design、Evaluation 和 figures；
- 最终声明：哪些结论由数据支持，哪些仍是限制。

在所有交付完成前保持 Goal Active；只有代码、实验、分析和论文全部完成且经过最终审计后，才能标记 Complete。
