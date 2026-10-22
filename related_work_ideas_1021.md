# 新 idea 的文献检查（10-21，Cowork）

用户 10-21："先确定有没有类似的已经做出来的论文，idea 是否新颖"。范围：README"当前进展与 idea 汇总（10-21）"里的 A1–A4。
标注：[V] = 本次打开页面或 PDF 核对过；[R] = 凭记忆，没有重新核对。

## 结论

| idea | 是否新颖 | 最接近的工作 | 还能算新的角度 |
|---|---|---|---|
| A1 跨 kernel 保留只读远程 line | **作为机制不新** | HMG（HPCA'20）、CARVE（MICRO'18）、CPElide（MICRO'24） | 不靠 sharer 目录的便宜一致性（home 端按页的写纪元）；按"省下的窗口位 × 往返时间"决定保留和替换；按跨 kernel 重用距离放宽准入 |
| A2 从邻居取 / 区域代理 | **作为机制不新** | HMG 的 GPU home、Virtual Hierarchies（ISCA'07）、Proxies（Euro-Par'96/98/00）、DASH RAC；就近共享者：In-Network Coherence（MICRO'06）、Delegated Replies（HPCA'22）；HDPAT（HPCA'26）在同一个 7×7 wafer 上对翻译做过"区域 + 地址哈希" | 用 Little 定律讲：代理把远往返变成近往返，等于放大了在途能力；共享在时间上错开时也能合并；区域大小按跳数代价和先 Y 后 X 路由定；要反驳 Kurian（HPCA'14）"cluster 大小 1 最优" |
| A3 子页放置 | **部分新** | Affinity Alloc（MICRO'23，最接近：64 B–4 KB 按区域交错、按跳数选 bank，但是片上 cache）、Micro-Pages（ASPLOS'10，1 KB 子页 + 内存控制器重映射表）、CDCS（HPCA'15）、Pal 等（HPCA'19，页级、按访问 × 跳数）、Gaud（ATC'14，拆大页）、CCL（arXiv 2026，软件布局） | 按观察到的使用者集合放置每个子页的 DRAM home，避免页迁移的来回搬；7×7 曼哈顿距离下最优位置就是加权中位数。要证明：页的使用者真的按子页分组；比 128 B–1 KB 交错（CODA）、哈希和软件布局更好 |
| A4 热点放中间 / 复制 | **单独不新** | Carrefour（ASPLOS'13）、R-NUCA（ISCA'09）、SelRep / Adaptive LLC（MICRO'20 / ISCA'19）、NDPExt（MICRO'24）、GPS（MICRO'21）、GRIT / OASIS（HPCA'24/'25）、CUDA read-mostly | wafer 特有的诊断：连续切分让小 buffer 落在角落 GPM 0，先 Y 后 X 让角落两条链路饱和。建议并进 A3，或者作为 baseline 放置的修正 |

**整体判断**：四个 idea 作为机制都有先例。论文的新意要放在 wafer 特有的论点上：远程代价由跳数决定，GPM 的在途窗口（64 项）跟不上 wafer 的直径，所以减少往返次数、缩短每次往返比省字节更重要。机制按这个论点组织，并用 HMG、CPElide、Affinity Alloc 这类工作做对比或讨论。

## A1 的相关工作
- **HMG: Extending Cache Coherence Protocols Across Modern Hierarchical Multi-GPU Systems** — Ren, Lustig, Bolotin, Jaleel, Villa, Nellans, HPCA 2020 [V]。远程 line 缓存在请求方 L2 和 GPU home 的 L2；L2 由硬件保持一致，acquire 只失效 L1；只在读写共享或目录驱逐时发失效，所以没写过的远程 line 跨 kernel 保留。结果上和 A1 很接近，机制是跟踪共享者。https://research.nvidia.com/publication/2020-02_hmg-extending-cache-coherence-protocols-across-modern-hierarchical-multi-gpu
- **CARVE（Combining HW/SW Mechanisms to Improve NUMA Performance of Multi-GPU Systems）** — Young, Jaleel, Bolotin, Ebrahimi, Nellans, Villa, MICRO 2018 [V]。远程数据缓存在本地 DRAM 的一块里；硬件一致性版本在 home 的 ECC 位里存每行 2 位共享状态，kernel 边界不刷，只对读写共享的行发失效。https://memlab.ece.gatech.edu/papers/MICRO_2018_3.pdf
- **CPElide: Efficient Multi-Chiplet GPU Implicit Synchronization** — Dalmia, Kumar, Sinclair, MICRO 2024 [V]。CP 里的表按数据结构记每个 chiplet 是只读还是读写，只读时跳过 kernel 边界的 acquire / release；需要标注，粒度粗，不缓存远程读。https://pages.cs.wisc.edu/~sinclair/papers/pdalmia-cpelide-micro24.pdf
- **Selective Replication in Memory-Side GPU Caches** — Zhao, Jahre, Eeckhout, MICRO 2020 [V]。复制只读数据，但每个 kernel 边界都刷（"一个 kernel 里只读的数据在另一个 kernel 里可能是读写的"），可以用来支持 A1 的动机。https://folk.idi.ntnu.no/jahre/papers/selrep-llc-micro20-preprint.pdf
- **Selective GPU Caches to Eliminate CPU–GPU HW Cache Coherence** — Agarwal et al., HPCA 2016 [V]。kernel 启动时把页标成只读，写就陷入。https://www.cs.utexas.edu/~skeckler/pubs/HPCA_2016_Coherence.pdf
- **MCM-GPU** — Arunkumar et al., ISCA 2017 [V]：L1.5 只缓存远程数据，kernel 边界整体失效（就是现在 M3 的做法）。LADM（MICRO 2020）[V]、Belayneh 等（PACT 2022）[V]：都在 kernel 边界整体失效。
- GPU 一致性：DeNovo for GPUs（MICRO 2015，有只读区域的变体）[V/R]；Temporal Coherence（HPCA 2013）、G-TSC（HPCA 2018）、HALCONE（arXiv 2020）[R 机制]；Spandex（ISCA 2018）[R]；Ashby 等（IEEE TC 2011，用 Bloom filter 让自失效有选择）[V]。
- 产品：AMD MI200 / MI300 的 acquire 失效 remote / NC 的 L2 行（LLVM SIMemoryLegalizer）[V]，就是 M3；MI300 跨 chiplet 用 probe filter [V]；NVIDIA Hopper 远程数据在两个 L2 分区都有副本，硬件传播写（arXiv 2608.22602）[V]。
- wafer：Pal 等 HPCA 2019、Exploring the Wafer-Scale GPU（GPGPU'25）、WaferTrans、HDPAT 都没有跨 kernel 保留远程 line [V]。

## A2 的相关工作
- **HMG**（同上）[V]：每个 GPU 内每个地址有一个 GPU home GPM，读按"本地 L2 → GPU home → system home"走，GPU home 缓存这条 line。等于区域 = 一个物理 GPU 的 A2。https://ericrxw.github.io/xiaoweiren/docs/HPCA2020/HMG-HPCA2020.pdf
- **Virtual Hierarchies to Support Server Consolidation** — Marty, Hill, ISCA 2007 [V，读的是 IEEE Micro 2008 版]：平面 64 tile 网格上，每组 tile 按地址有一个一级 home，没有再去全局二级。
- **Proxies**（Bennett, Kelly 等，Euro-Par 1996 / 1998 / 2000）[V 2000 版]：读先到该 line 的代理节点，代理可以按网络位置成组，缓存数据并合并请求；动机是 home 拥塞。https://www.doc.ic.ac.uk/~phjk/Publications/EuroPar00-Proxies.pdf
- **DASH Remote Access Cache** — Lenoski 等，ISCA 1990 [V]。
- NUCA：R-NUCA（ISCA 2009，4 tile 簇内复制指令，共享数据不复制）[V]；**Kurian, Devadas, Khan, HPCA 2014**（试了 cluster 大小 1/4/16/64，"1 最优"——要反驳）[V]；Nexus（PACT 2017，按地址标签就近的 bank）[V]；DCC（ICS 2009）、DAPSCO（TACO 2012）[V]；Victim Replication、ASR、Cooperative Caching [R]。
- 就近共享者 / 网内服务：In-Network Cache Coherence（MICRO 2006）[V]；In-Network Caching for CMPs（HiPEAC 2009）[V]；Proximity Coherence（PACT 2010）[V 摘要]；Ibrahim 等（PACT 2019，GPU 核先探别的核的 L1）[V]；Delegated Replies（HPCA 2022，LLC 把回复交给最近访问过的核）[V]；Trans-FW（HPCA 2023，只针对翻译）[V]。
- wafer：**HDPAT（HPCA 2026，sarchlab）** 在同一个 7×7、48 GPM 的 wafer 上按象限分簇、地址取模选 GPM、轮转映射，缓存的是 PTE [V]。审稿人很可能把 A2 看成"数据版的 HDPAT + HMG"。

## A3 / A4 的相关工作
- 细粒度放置：MCM-GPU（细粒度交错是 baseline；只用 first-touch 慢 4.7%）[V]；Beyond the Socket（MICRO 2017，子页交错让约 75% 访问变远程）[V]；CARVE（页迁移在多 GPU 共享一页时失效）[V]；Griffin（HPCA 2020）[V 摘要]；GRIT（HPCA 2024）、OASIS（HPCA 2025）：按页 / 按对象选迁移或复制，最小 4 KB [V]；CODA（arXiv 1710.09517，按对象选 128 B 交错或 4 KB 连续）[V]；CLAP（MICRO 2025）[V]；**CCL：Making Locality-aware GEMM Compatible with Page-Granularity Placement on Chiplet GPUs（arXiv 2606.11718）**：说需要比页更细的放置，用软件布局解决 [V]；Gaud 等 ATC 2014 [V]。
- 重映射表：**Micro-Pages（ASPLOS 2010）**：1 KB 子页 + 内存控制器 4K 项映射表，不改页大小和 TLB [V]；**Affinity Alloc（MICRO 2023）**：按区域 64 B–4 KB 交错，16 项表，按跳数 + 负载选 bank——和 A3 最接近 [V]；Awasthi 等（HPCA 2009，共享页移到使用者的加权中心）[V]；CDCS（HPCA 2015）[V]；PoM（MICRO 2014）[V]；Impulse（HPCA 1999）[V]；AFM（DATE 2026，A100 分区间按 line 迁移）[V]。
- 复制和热点：**Carrefour（ASPLOS 2013）**：拥塞比局部性重要，失衡时交错页、复制多节点用的只读页——基本就是 CPU 上的 A4 [V]；R-NUCA [V]；Adaptive LLC（ISCA 2019）、SelRep（MICRO 2020，每个 epoch 选 2/4/8/16 份）[V]；NUBA（ASPLOS 2023）[R]；NDPExt（MICRO 2024，按距离放置 + 复制只读，并指出质心放置会偏向中心）[V]；GPS（MICRO 2021）[V]；CUDA `SET_READ_MOSTLY` [V]；Abts 等 ISCA 2009（内存控制器放置）[V]。
- wafer：Pal 等 HPCA 2019：按 thread block / 页的访问图划分，按访问 × 跳数映射到 GPM，页粒度、不复制、不处理多 GPM 共享的页 [V]；HDPAT 的环形缓存和重定向表 [V]——A4 的"放中间 / 复制"和"重定向表"这个名字都和它重叠。

## 对论文的建议
1. 主论点放在 wafer 特有的部分：跳数 → 往返时间 → 64 项窗口限制吞吐；需要的在途能力随直径增长（规模扫描 5×5 / 7×7 / 9×9 是关键证据）。
2. A1 的写法：便宜的写纪元一致性 + 按窗口位价值保留，把 HMG、CPElide 作为对比（最好实现成 baseline）。
3. A2 的写法：如果做，要讲"把远往返换成近往返 = 放大在途能力"和"不要求同时到达的合并"，并和 HMG、HDPAT 明确区分；要回应 Kurian 的结论。
4. A3 先看访问模式分析（页内是否按区域分组），并和 128 B–1 KB 交错比；A4 并进 A3 或作为 baseline 放置的修正。
