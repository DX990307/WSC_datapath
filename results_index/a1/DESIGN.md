# A1：M3 跨 kernel 保留只读远程 line——设计与正确性论证

Claude Code，2026-10-21。代码：worktree `wt-a1`，分支 `isca27/a1-retain`（从 `isca27/cupath-v3` @ `ad0dfe3` 开出）。

## 1. 现状（M3）和要解决的问题

- M3：请求方 RDMA 把远程 line 的干净副本填进请求方自己的 L2（`writeback.Cache.installRemoteDataFill`），之后同 GPM 再读这条 line 时 RDMA 先探本地 L2（RESIDENT/SEEN 过滤器 + LookupOnly 探测），命中就不走网络。准入是"两次触碰"：第一次只记 SEEN，第二次才填。
- `-remote-reuse-kernel-invalidate`：每个 GPM 的 CP 接受 kernel launch 时（`cp.KernelLaunchHooks`），本 GPM 所有 L2 slice 的远程副本整体失效（`InvalidateRemoteReplicas`），RDMA 里在途的远程 line 标成 stale（不再填 L2、不再接受合并）。
- 问题：只读数据（矩阵、图结构、权重）每个 kernel 都要重新取。dm 分析：npbcg 141 万次填入里 135 万次在边界被作废；Complete 之后仍走网络的 line 中"同 GPM 重复取"占 npbcg 0.82、KM 0.93、MG 0.62、CFD 0.60……

## 2. 机制

### 2.1 home 端写纪元（`akita/mem/cache/writeback/remote_retain.go`）

- 全局纪元号 `epoch`（从 0 开始）。按区域（默认 4 KiB 页，`-a1-region-log2`）记录"最后一次被写的纪元" `lastWrite[region]`。
- 每个真正改变物理内存内容的写都报告给 tracker（`NoteWrite(now, addr, size)`），时间点是写**完成**（对读者可见）之时或更晚：
  1. **home L2 完成的写**：L2 发出 WriteDoneRsp 的两个位置（`bankstage` 写命中完成、`mshrstage.respondWrite` 写缺失合并完成），另外在 `topparser` 接受写请求时也记一次（只会多失效）。所有写 home 内存的 GPU 写都经过 home 的 L2：本 GPM CU 的本地写（L1V write-around → L2）、其他 GPM 的远程写（请求方 RDMA → owner RDMA → owner L2）。
  2. **host 拷贝**：driver 在 magic H2D（`globalStorageMemoryCopyMiddleware`，直接写 global storage）每个块写完时报告；DMA H2D（`defaultMemoryCopyMiddleware`，CP 的 DMA 引擎直接写 DRAM，不经 L2）在发起时和每个 H2D 请求返回时各报告一次（返回时 DMA 的所有 DRAM 写已完成）。
  3. **页被重新分配**：`FreeMemory`、`Remap`、`Distribute` 在改变 VA→PA 映射之前，把当前背后的物理页标成已写。
- **kernel 边界**（`BeginBoundary(id, now)`，在 GPM `id` 的 CP 接受 launch 时调用）：如果当前纪元里发生过写，开一个新纪元；返回该 GPM **上一次边界时的纪元** `since`，并把它的记录改成当前纪元。同一 kernel 的 48 个 launch 之间没有写，就共用一个纪元。
- 请求方 L2 的每个 slice 遍历自己的远程副本（`InvalidateWrittenRemoteReplicas`），区域满足 `lastWrite[region] >= since` 的失效（同时移出 RESIDENT 过滤器），其余保留。
- 在途的远程 line 和在途 fill 的处理**不变**（保守）：RDMA 里在途 entry 仍全部标 stale；L2 仍丢弃 SendTime ≤ 边界时间的 fill。
- 硬件对应：每个 home GPM 维护"本纪元写过的本地区域集合"（精确表，或 `-a1-signature-bits` 位的 Bloom 签名），kernel 边界时广播给所有 GPM（或由请求方在边界时按需取）；请求方 L2 在 launch 时按 tag 扫一遍远程副本（和 M3 的整体失效一样是 flash 操作，模型里不计时间）。区域到 home 的映射就是物理地址的高位（每 GPM 8 GiB，`addr >> 33`），请求方不需要目录、不需要记录共享者。

### 2.2 可选：放宽准入（`-a1-first-touch-admission`）

- 这个选项只在 RDMA 端，和 `-a1-retain` 独立，可以单独加在 v4_complete 上。
- 0（默认）：两次触碰才填 L2，和 M3 一样。注意：SEEN 跨 kernel 保留，所以"kernel k 读一次、k+1 再读"的 line 在 k+1 的第二次触碰就会被保留下来，A1 只损失第一次。
- 1：第一次触碰也填，但只允许填进空闲 way（`RequireInvalidVictim`，不挤掉别的副本，也从不挤本地 line）。依据：L2 70–95% 空闲。
- 2：第一次触碰按第二次处理（可以替换别的远程副本，不挤本地 line）。

### 2.2b 可选：边界时清除"自己写过"的不可缓存标记（`-a1-reset-uncacheable`，10-22 追加）

- 现状：请求方 GPM 写一条远程 line 时（`noteLegacyRemoteReqSent`），该 line 被标成 `remoteUncacheable`，之后这个 GPM 读它一律走网络，而且这个标记**永远不清**（跨 kernel 也不清）。原因是自己的写绕过自己的 L2，L2 里可能还留着写之前的副本。
- 10-22 的统计（`m3_wire_breakdown.csv`）显示这是 KM（100%）、fft2d（79%）、FW（64%）、MG（53%）、CFD（49%）、gups（40%）、LUD（33%）仍走网络的 line 的主要来源，A1 本身管不到。
- 选项打开时，kernel 边界（`MarkRemoteReuseKernelBoundary`）清空这个 GPM 的 `remoteUncacheable`。正确性：标记只是为了防止本 GPM 读到"早于自己写"的副本；到边界时，该写已经完成并在 home 被记录（第 3 节），所以 M3 下所有副本都已失效，A1 下被写区域的副本都已失效，不存在早于该写的副本。之后再写会重新打标记。在途 entry 在边界一律作废（不变）。配置 `v4_complete_a1u.json` = v4_complete_a1 + 这个选项。

### 2.3 开关

- `-a1-retain`（默认 false）。打开时要求 `-remote-data-path-enable`、`-remote-data-path-l2-enable`、`-remote-reuse-kernel-invalidate`，且不能和 `-use-unified-memory`（页迁移）同用（runner 里 panic）。
- 关闭时：tracker 不创建，L2 的 `homeWriteObserver` 为 nil（`noteHomeWrite` 直接返回），driver 没有 hook，边界走原来的 `InvalidateRemoteReplicas`；`FirstTouchAdmission` 默认 0 不改变任何分支。所以 A1 关闭时和基线逐位一致（第 4 节的 fir / MM 对比）。
- 新配置 `akkalat/configs/v4_complete_a1.json` = v4_complete + `a1-retain`（精确写集合、4 KiB 区域、两次触碰准入）。

## 3. 正确性论证

**要保证的性质**：在任何 GPM `r` 的 kernel `k+1` 开始时，`r` 的 L2 里留下的每个远程副本都等于该地址在 home 的当前内容。kernel 内部的行为和 M3 一样：kernel 内另一个 GPM 写了同一地址而没有同步，读到旧值是 GPU 内存模型允许的（M3 在 kernel 内也会这样；模拟器没有跨 GPM 的原子操作 / 栅栏）。

记 `T_r` 为 `r` 的边界时刻，`T'_r` 为 `r` 的前一个边界。

1. **新装入的副本**：只有在 `T'_r` 之后创建的 RDMA entry 才能填 L2（在 `T'_r` 时在途的 entry 被标 stale，SendTime ≤ `T'_r` 的 fill 被丢弃）。所以副本的数据是在 home 于某时刻 `t_read > T'_r` 读出的，反映了所有在 `t_read` 之前完成的写。
2. **之后的写一定被记下**：一个在 `t_read` 之后完成的写，记录时间 ≥ 完成时间 > `t_read` > `T'_r`。在 `r` 的前一个边界时 `r` 的纪元记为 `since`，纪元 `since` 开始于 `T'_r` 或更早，所以这个写的纪元 ≥ `since`，在 `T_r` 被判为"写过"。前提是写在 `T_r` 之前**被记录**：kernel `k` 的写在 kernel `k` 结束前全部完成并得到 ack（远程写由 owner ack 之后 wavefront 才能结束；host 拷贝在下一个命令开始前完成），而 `T_r` 在 kernel `k` 全局结束之后；kernel `k+1` 里由较早启动的 GPM 做的写，如果在 `T_r` 之前被记录，就在这次失效（多失效，安全），如果在 `T_r` 之后被记录，纪元 ≥ `r` 新的 `since`，在 `r` 的下一个边界失效——而在 kernel `k+1` 里读到它之前的值是允许的（同 kernel 内无同步的竞争）。
3. **保留下来的旧副本**（归纳）：在 `T'_r` 时它等于 home 内容；在 `(T'_r, T_r]` 内它所在区域没有任何写被记录（否则会失效），而所有在这段时间内完成的写都会被记录（第 2 点），所以在 `T_r` 时仍等于 home 内容。
4. **记录时间不早于可见时间**：home L2 在发 WriteDoneRsp 时记录，此时数据已写进 L2 块（bankstage）或 MSHR 合并完成（mshrstage）；读在 home 的 L2 上串行化，所以一个读如果看到旧值，必然发生在写完成之前，即 `t_read` < 记录时间。DMA H2D 在请求返回时再记一次；magic H2D 在写 global storage 之后立即记。
5. **覆盖的写来源**：
   - 本 GPM 写自己的页：经本地 L2 → 记录。其他 GPM 持有的副本在它们的边界失效。
   - 远程写：经 owner L2 → 记录。写者自己：`noteLegacyRemoteReqSent` 已把该 line 标成 `remoteUncacheable`（之后不再从本地 L2 读它，原有机制），且边界时也会因写记录而失效。
   - host 拷贝（magic / DMA）：driver hook → 记录。注意：magic H2D 本来就不和 home L2 一致（home L2 里的本地 line 也可能变旧，这是基线的已有问题，和 A1 无关）；A1 至少不会让副本比基线更旧。走 driver flush 的拷贝会经 `flusher` 清掉所有副本（`resetRemoteReplicas`），和 M3 一样。
   - 页重新分配（free / remap / distribute）：副本按物理地址做 tag，物理内容只会被写改变；为保险，改映射前把旧物理页记为已写。
   - 页迁移（PMC，`-use-unified-memory`）：**不支持**，打开时 panic。v4 配置不用它。
6. **签名模式**：每个 (home, 纪元) 一个 M 位 Bloom 签名，写时置位，查询时对 `since..当前` 的每个纪元查签名；只可能多报（`a1_signature_false_positives`），代码里还对"精确判为写过但签名漏掉"做了 panic 断言。签名在所有 GPM 都越过之后回收。

## 4. 验证

- 单元测试（`writeback/remote_retain_test.go`）：窗口语义（两个 GPM、写落在两者 launch 之间、只报告一次）；签名只会多报；L2 只失效被写页的副本、保留的副本仍在 RESIDENT 过滤器里、命中计数；关闭时无影响。
- `a1check` 微基准（`mgpusim/benchmarks/microbench/a1check`，`-benchmark a1check -verify`）：只用 driver 的 D2D 拷贝 kernel。kernel k、k+1 读 A（第二次触碰后各 GPM 保留 A 的副本），k+2 由**另一个 work-group**（一般在另一个 GPM）写 A 的后半（部分写者就是 home GPM），k+3 写 A 前半里的一条 line，k+4 再读 A，必须看到新值；只读的 R 在之后的 kernel 里应命中保留副本。负对照：把"写过"的判断硬改成 false 的二进制，`a1check` 报 8208 个不匹配（8192 个后半元素 + 16 个单 line 元素），说明这个测试能抓到陈旧副本。
- 其余验证（关闭时逐位一致、`-verify` 回归）见 README。

## 5. 和已有工作的区别

| | 一致性怎么维持 | 需要的状态 | 和 A1 的区别 |
|---|---|---|---|
| HMG（HPCA'20） | 硬件目录记录共享者，写时向共享者发失效；acquire 只失效 L1，L2 副本跨 kernel 保留 | 每 GPU home 的 sharer 目录（目录驱逐也要失效） | A1 没有共享者目录，也没有写时失效消息；只在 kernel 边界按"home 的写集合"整体判定。代价是一次边界广播（每个 home 一份写集合 / 签名），不随共享者数增长 |
| CARVE（MICRO'18） | 远程数据缓存在本地 DRAM；硬件一致性版本在 home 的 ECC 位里存每行 2 位共享状态，读写共享的行发失效 | 每行状态位 + 失效消息 | A1 只按区域记写纪元，缓存在 L2（不是 DRAM 分区），不发逐行失效 |
| CPElide（MICRO'24） | CP 里按数据结构记每个 chiplet 是只读还是读写，只读时省掉边界的 acquire / release | 需要按 kernel 参数（数据结构）标注；粒度是整个 buffer | A1 由硬件在 home 观察实际的写，不需要标注，粒度是页（可到 64 B）；同一 buffer 里只被写了一部分的页之外的部分照样保留 |
| SelRep（MICRO'20）、MCM-GPU L1.5、AMD MI200 acquire | 每个 kernel 边界都刷掉复制的数据 | — | 就是现在的 M3，A1 要去掉的正是这部分 |
| Selective GPU Caches（HPCA'16） | kernel 启动时把页标只读，写就陷入 | 页表权限 + 陷入 | A1 不陷入，写照常进行，只在边界失效 |

可能的新角度（来自 related_work_ideas_1021.md）：不靠 sharer 目录的便宜一致性（home 的写纪元 / 写签名，成本只和每个 kernel 写的页数有关）；`a1_max_regions_owner_epoch` / `a1_max_regions_epoch` 统计给出每个 home 每个纪元的写集合大小，用来定签名的大小。"按省下的窗口位 × 往返时间决定保留和替换"没有实现（只实现了准入的放宽）。
