# Wafer-scale GPU 数据共享观测计划

## Motivation

Wafer-scale 或 multi-tile GPU 的关键问题不是“是否存在远程访问”这么简单，而是：远程访问中有多少来自真正被多个 tile 共享的数据，这些共享数据是否本来可以被本地访问，以及共享模式是否足够规则，值得硬件、运行时或编译器进一步优化。本节只做 observation，不提出调度机制；目标是证明数据共享是否是值得优化的一类现象。

本文建议从三个层次组织观察：页访问 locality、共享强度与热点页、共享距离与规则性。

## Observation 1: Page Access Locality

**问题。** 每个 workload 的 memory access 中，有多少访问命中本地 owner tile，有多少访问跨 tile 访问远程 owner tile？

**为什么重要。** Wafer-scale GPU 的片上网络距离和带宽代价随 tile 间距离变化。如果大多数访问已经是 local，那么共享优化空间有限；如果 remote access 占比高，则需要进一步判断这些 remote access 是否由共享数据、初始放置或迁移策略导致。

**采集指标。**

- `LocalAccessRatio`
- `RemoteAccessRatio`
- `LocalByteRatio`
- `RemoteByteRatio`
- read/write 分开的 local/remote byte ratio

**推荐图表。**

- 每个 workload 一根 stacked bar：local accesses vs remote accesses。
- 每个 workload 一根 stacked bar：local bytes vs remote bytes。
- 表格：按 read/write 拆分的 local/remote ratio。

## Observation 2: Sharing Intensity and Hot Shared Pages

**问题。** 有多少 page/data block 被多个 GPU/tile 访问？共享数据贡献了多少字节流量？共享是否由少量热点页主导？

**为什么重要。** 只看 remote ratio 会混淆“私有数据放错位置”和“共享数据被多个 tile 反复访问”。如果 `SharedByteRatio` 高，说明 workload 存在跨 tile 数据复用；如果 sharer count 呈 heavy-tail，说明少量热页可能支配网络流量和缓存压力。

**采集指标。**

- `SharedPageRatio`
- `SharedByteRatio`
- `SharerCount` distribution
- Top-K shared pages 的 byte/access 占比
- 每个 shared page 的 read/write bytes

**推荐图表。**

- 每个 workload 的 `SharedPageRatio` 与 `SharedByteRatio` 双柱图。
- `SharerCount` histogram 或 CDF。
- Top shared pages 表：`page_id, bytes, accesses, sharer_count, remote_bytes`。
- Hot page Pareto curve：Top-K shared pages 占总 bytes 的比例。

## Observation 3: Sharing Distance and Regularity

**问题。** 共享数据的访问距离有多远？共享访问集中在固定 requester-owner pair，还是在 tile 间无规律扩散？

**为什么重要。** 两个 workload 可以有相同的 `SharedByteRatio`，但优化难度完全不同。近距离、固定 pair 的共享更容易通过 placement 或 replication 改善；远距离、分散、时间变化大的共享更可能需要更复杂的 coherence、migration 或 network support。

**采集指标。**

- `SharedBytesByDistance`
- `WeightedSharingDistance`
- requester-owner pair byte matrix
- pair entropy / normalized entropy
- `RegularityScore`

**推荐图表。**

- `SharedBytesByDistance` histogram。
- requester-owner heatmap：x 轴 requester tile，y 轴 owner tile，颜色为 bytes。
- 每个 workload 的 `WeightedSharingDistance` bar chart。
- 每个 workload 的 regularity table：`PairEntropyNorm, RegularityScore, DominantPairByteRatio`。

## Metrics

设内存访问集合为 \(A\)。每个访问 \(a \in A\) 包含：

- requester tile：\(r(a)\)
- owner tile：\(o(a)\)
- page 或 data block：\(p(a)\)
- 字节数：\(b(a)\)
- 访问类型：\(type(a) \in \{R,W\}\)
- hop distance：\(d(a)\)，如果不可用则记为 unknown

总访问数与总字节数：

\[
N = |A|,\quad B = \sum_{a \in A} b(a)
\]

### Local/Remote Ratio

\[
LocalAccessRatio =
\frac{\sum_{a \in A} \mathbf{1}[r(a)=o(a)]}{N}
\]

\[
RemoteAccessRatio =
\frac{\sum_{a \in A} \mathbf{1}[r(a)\ne o(a)]}{N}
= 1 - LocalAccessRatio
\]

\[
LocalByteRatio =
\frac{\sum_{a \in A} b(a)\mathbf{1}[r(a)=o(a)]}{B}
\]

\[
RemoteByteRatio =
\frac{\sum_{a \in A} b(a)\mathbf{1}[r(a)\ne o(a)]}{B}
\]

### Shared Page/Byte Ratio

对每个 page \(p\)，定义 sharer set：

\[
S_p = \{r(a)\ |\ a \in A, p(a)=p\}
\]

共享页集合：

\[
P_{shared} = \{p\ |\ |S_p| \ge 2\}
\]

\[
SharedPageRatio =
\frac{|P_{shared}|}{|\{p(a)\ |\ a \in A\}|}
\]

\[
SharedByteRatio =
\frac{\sum_{a \in A} b(a)\mathbf{1}[p(a)\in P_{shared}]}{B}
\]

SharerCount distribution：

\[
H(k) = |\{p\ |\ |S_p| = k\}|
\]

### Distance Metrics

共享数据在距离 \(h\) 上贡献的字节：

\[
SharedBytesByDistance(h) =
\sum_{a \in A} b(a)\mathbf{1}[p(a)\in P_{shared}]\mathbf{1}[d(a)=h]
\]

按字节加权的共享距离：

\[
WeightedSharingDistance =
\frac{\sum_{a \in A} b(a)d(a)\mathbf{1}[p(a)\in P_{shared}]}
{\sum_{a \in A} b(a)\mathbf{1}[p(a)\in P_{shared}]}
\]

如果只关心远程共享访问，可以排除 \(d(a)=0\)：

\[
WeightedRemoteSharingDistance =
\frac{\sum_{a \in A} b(a)d(a)\mathbf{1}[p(a)\in P_{shared}]\mathbf{1}[r(a)\ne o(a)]}
{\sum_{a \in A} b(a)\mathbf{1}[p(a)\in P_{shared}]\mathbf{1}[r(a)\ne o(a)]}
\]

### Regularity / Irregularity

定义 requester-owner pair 的字节量：

\[
PairBytes(i,j)=\sum_{a \in A} b(a)\mathbf{1}[r(a)=i]\mathbf{1}[o(a)=j]
\]

将每个 pair 的占比记为 \(q_{ij}=PairBytes(i,j)/B\)。pair entropy：

\[
H_{pair} = -\sum_{i,j} q_{ij}\log_2 q_{ij}
\]

归一化熵：

\[
PairEntropyNorm =
\frac{H_{pair}}{\log_2 |\{(i,j)\ |\ PairBytes(i,j)>0\}|}
\]

规则性分数：

\[
RegularityScore = 1 - PairEntropyNorm
\]

直觉：`PairEntropyNorm` 越接近 1，访问越分散、越不规则；`RegularityScore` 越接近 1，访问越集中、越规则。

还可以报告 dominant pair：

\[
DominantPairByteRatio =
\frac{\max_{i,j} PairBytes(i,j)}{B}
\]

## Data Collection Methodology

推荐在 L1 vector memory address translator 拿到 translation response 后记录一条访问，因为此时同时可见：

- requester tile：translation request 的 `DeviceID`
- owner tile：page table 中 `Page.DeviceID`
- virtual page：`Page.VAddr >> log2PageSize`
- virtual address：原始 memory request address
- access type：`ReadReq` 或 `WriteReq`
- byte count：memory request byte size
- timestamp/order：sim time 与 trace sequence number
- hop distance：由 tile grid 坐标计算，或直接使用 simulator 提供的 hop distance

本仓库新增的 trace 字段如下：

```text
cycle,requester,owner,vaddr,op,distance
```

其中 `cycle` 是该 GPU 产生这次数据需求的时间，计算方式为 `round(time_s * 1e9)`；`requester` 是需要数据的 GPU/tile，`owner` 是当前持有该数据所在页的 GPU/tile，`vaddr` 是被访问的虚拟地址，`op` 是读写类型，`distance` 是 requester 到 owner 的 hop 距离。后处理时用 `requester == owner` 判断 local，否则为 remote；需要 page-level 统计时用 `page_id = vaddr >> log2_page_size` 推导。

为了避免 trace 过大，trace 默认写 gzip 压缩 CSV，并支持采样与最大记录数：

```bash
cd akkalat
python3 runall2.py --trace-sharing --trace-sharing-sample 10 --trace-sharing-max-records 200000
```

注意：采样 trace 适合快速判断趋势，但会低估 `SharedPageRatio` 和高 sharer-count page。论文中的最终数字建议使用 `-trace-sharing-sample=1`，或者明确报告采样率。

单个 benchmark 也可以直接打开：

```bash
cd akkalat/baseline
go build -buildvcs=false
./baseline -benchmark=spmv -timing -magic-memory-copy \
  -trace-sharing \
  -trace-sharing-file=../results/spmv_sharing.csv.gz \
  -trace-sharing-sample=1 \
  -trace-sharing-max-records=1000000
```

分析 trace：

```bash
cd akkalat
python3 analyze_sharing_trace.py results/spmv_sharing.csv.gz --head 10
```

脚本会先打印前几行原始 trace 样例，然后输出中文 summary，并生成：

- `*_summary_metrics.csv`
- `*_sharer_count_distribution.csv`
- `*_shared_sharer_count_distribution.csv`
- `*_shared_bytes_by_distance.csv`
- `*_all_bytes_by_distance.csv`
- `*_top_shared_pages.csv`
- `*_pair_bytes.csv`

## Possible Figures

1. Local/remote access ratio：每个 workload 一个 stacked bar。
2. Local/remote byte ratio：每个 workload 一个 stacked bar。
3. Shared page vs shared byte ratio：双柱图，展示“页数量共享”和“流量共享”的差异。
4. SharerCount CDF：观察共享页是 2-tile sharing 还是 many-tile sharing。
5. SharedBytesByDistance：观察共享流量是否集中在近邻。
6. Requester-owner heatmap：展示共享是否规则、是否集中在固定 tile pair。
7. Top shared pages table/Pareto curve：证明是否存在 hot shared pages。

## Expected Observations

- 某些 workload 可能 `SharedByteRatio` 高但 `LocalAccessRatio` 低，说明存在大量共享数据被远程访问，是后续优化的强动机。
- 某些 workload 可能 `LocalAccessRatio` 很高，即使有共享页，也主要由本地访问贡献，此时优化重点不应放在跨 tile 共享。
- 图算法或稀疏计算可能表现出高 `PairEntropyNorm`，共享 requester-owner pair 分散，说明共享模式不规则。
- stencil、matrix-like workload 可能在 `SharedBytesByDistance` 上集中于小 hop，表现出较规则的近邻共享。
- 少数 shared pages 可能拥有很高 `SharerCount` 和 byte share，形成 hot shared pages，适合在 observation 中单独展示。

## Takeaway

这一组 observation 应回答三个问题：第一，远程访问是不是显著；第二，远程访问是否与多 tile 数据共享相关；第三，共享是否有距离、热点或规则性结构。只有当这些现象在多个 workload 中足够明显，后续才有充分理由设计 placement、replication、migration 或 coherence 优化。
