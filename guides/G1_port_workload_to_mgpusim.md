# G1 怎么把一个新 workload 移植进 MGPUSim（以 GCN / ogbn-arxiv 为例）

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认，**等用户确认**之后再运行。文中的 `cu8` 目前是 `[提案]`，没有批准之前一律用论文配置。

> 适用于：T5a–T5d、T9。以 T5b（GCN）作为完整示例，其他 workload 的差异见文末。
> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧，所以要加入现代 workload。平台是在 MGPUSim 上扩展的 48 GPM wafer（7×7 mesh），模拟的是 AMD GCN3 ISA。
> **通用约束**：kernel 一律用 fp32；输入规模要保证 kernel 能在模拟预算内跑完（见 T4 的 `cu8`）；所有统计都按 T2 的定义产出；**代码只在本机修改**。
> 索引：`../README.md` · 进度写入：`../STATUS.md`
>
> ⚠️ 下面给出的 Go API 名称和编译命令，来自上游 MGPUSim 的惯例和 CLAUDE.md。**CuPath 的代码版本可能不一样，一切以仓库里已有的 benchmark 为准。** 遇到不一致的地方，记进 `STATUS.md` 的“规格与代码的差异”。

---

## 整体流程

```
Python 准备数据 ─→ 写 kernel（.cl 或 .hip）─→ 编译成 kernels.hsaco ─→ 写 Go benchmark 包
      ─→ --tiny 冒烟测试（timing 模式，确认能跑通）─→ timing 模式（cu8）全量 ─→ results/ ─→ 用统计做合理性检查
```
**不跑 verify**（用户 10-12 的决定）：不需要生成 CPU 参考输出，也不需要 emulation 模式下的校验。

## 第 0 步：选模板

不要从零写，**复制一个最接近的现有 benchmark，在它的基础上改**：

| 需要的 kernel 类型 | 现有模板 |
|---|---|
| CSR 稀疏计算（SpMM、图计算） | SHOC 的 `spmv` |
| 稠密 GEMM | AMDAPPSDK 的 `matrixmultiplication` |
| 逐元素运算（ReLU 等） | DNNMark 的 `relu` |
| 多次迭代、多个 kernel | Hetero-Mark 的 `pagerank`、`kmeans` |

GCN = **新写的 SpMM kernel** + 复用 MM 的 GEMM kernel + 复用 ReLU，所以是第一批里工作量最小的一个。

## 第 1 步：用 Python 准备数据（在本机运行）

`scripts/prep_gcn_arxiv.py`（`pip install ogb scipy numpy`）：

```python
import json, sys, numpy as np, scipy.sparse as sp
from ogb.nodeproppred import NodePropPredDataset   # 纯 numpy 加载器，不需要 PyTorch

out = sys.argv[1]                                   # 例如 data/gcn-arxiv
ds = NodePropPredDataset('ogbn-arxiv', root='data/ogb')
g, _ = ds[0]
n, ei = g['num_nodes'], g['edge_index']
X = g['node_feat'].astype(np.float32)               # 169343 x 128

A = sp.coo_matrix((np.ones(ei.shape[1], np.float32), (ei[0], ei[1])), shape=(n, n)).tocsr()
A = ((A + A.T) > 0).astype(np.float32)              # arxiv 是有向图，先对称化
A.setdiag(1.0); A.eliminate_zeros()                 # 加自环
d = np.asarray(A.sum(1)).ravel()
Dm = sp.diags(1.0 / np.sqrt(d))
A = (Dm @ A @ Dm).tocsr().astype(np.float32)        # GCN 对称归一化
A.sort_indices()

rng = np.random.default_rng(0)
W1 = (rng.standard_normal((128, 128)) * 0.1).astype(np.float32)
W2 = (rng.standard_normal((128, 40)) * 0.1).astype(np.float32)

for name, arr in [('row_ptr', A.indptr.astype(np.int32)), ('col_idx', A.indices.astype(np.int32)),
                  ('val', A.data), ('x', X), ('w1', W1), ('w2', W2)]:
    arr.tofile(f'{out}/{name}.bin')
json.dump({'n': int(n), 'nnz': int(A.nnz), 'F': 128, 'H': 128, 'C': 40, 'seed': 0}, open(f'{out}/meta.json', 'w'))
```

另外加一个 `--tiny` 选项：生成一个约 2000 个点的随机小图，**只用来做冒烟测试**，确认程序能跑通、参数没有传错。

kernel 的计算顺序是先聚合再变换：第 1 层是 `relu((A·X)·W1)`，第 2 层是 `(A·H1)·W2`。这个顺序写进 meta。

## 第 2 步：写 kernel（OpenCL 版）

`kernels.cl`：

```c
// 一个 wavefront（64 lane）负责一行；F=128 时，每个 lane 负责 2 个特征维度
__kernel void spmm_csr(__global const int* row_ptr, __global const int* col_idx,
                       __global const float* val, __global const float* X,
                       __global float* Y, const int num_rows, const int F) {
  const int lane = get_local_id(0) & 63;
  const int row  = get_global_id(0) >> 6;
  if (row >= num_rows) return;
  const int start = row_ptr[row], end = row_ptr[row + 1];
  for (int f = lane; f < F; f += 64) {
    float acc = 0.0f;
    for (int e = start; e < end; ++e)
      acc += val[e] * X[(long)col_idx[e] * F + f];   // 邻居特征：不规则访问，可能跨 GPM
    Y[(long)row * F + f] = acc;
  }
}

__kernel void relu(__global float* A, const int n) {
  int i = get_global_id(0);
  if (i < n) A[i] = fmax(A[i], 0.0f);
}

// GEMM：把 AMDAPPSDK matrixmultiplication 的 kernel 原样拷贝过来，确认它支持非方阵（128x40）
```

- **launch 配置**：SpMM 的 WG 大小是 256，也就是一个 WG 处理 4 行；grid 大小 = `ceil(num_rows*64/256)*256`。arxiv 上大约有 4.2 万个 WG。
- **WG 到行的映射用最自然的线性方式，不要为了制造共享去调整。**

## 第 3 步：编译成 `kernels.hsaco`

**以仓库里现有 benchmark 的编译命令为准**，通常写在 `kernels.cl` 旁边的 Makefile、脚本或注释里。两条可能的路线如下（T1 负责确认哪条可用）：

- **OpenCL 路线**（老版本 MGPUSim 的常见做法）：用 ROCm 的 OpenCL 编译器，目标 `gfx803`，生成 code object V2 或 V3。
- **HIP 路线**（上游 MGPUSim 的 CLAUDE.md 推荐）：在 `rocm/dev-ubuntu-24.04:7.2.4` 容器里，用 hipcc/clang 只编译 device 代码，目标 `gfx803`，生成 HSACO（V5）。

编译完用 `llvm-readelf -s kernels.hsaco | grep -E 'spmm_csr|relu|mm'` 确认 kernel 的符号都在。

## 第 4 步：写 Go benchmark 包

位置是 `amd/benchmarks/<suite>/gcn/`，suite 可以新建一个 `gnn`。**结构直接照抄 spmv 模板**，下面只是示意：

```go
package gcn

import _ "embed"

//go:embed kernels.hsaco
var hsacoBytes []byte

// kernel 参数结构体必须和 kernel 的参数布局逐字节对齐：
// 指针 8 字节，int 4 字节，需要时补 padding；
// 末尾的 hidden 参数（比如 HiddenGlobalOffsetX/Y/Z）照抄模板
type SpMMArgs struct {
    RowPtr, ColIdx, Val, X, Y driver.Ptr
    NumRows, F                int32
    HiddenGlobalOffsetX, HiddenGlobalOffsetY, HiddenGlobalOffsetZ int64
}

type Benchmark struct {
    driver  *driver.Driver
    context *driver.Context
    gpus    []int
    DataDir string
    // GPU 端的缓冲区：dRowPtr, dColIdx, dVal, dX, dTmp, dH1, dW1, dW2, dOut ...
}

func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }
func (b *Benchmark) Run() {
    // 1. 从 DataDir 读 .bin 文件（数据太大，不要用 go:embed）
    // 2. AllocateMemory + MemCopyH2D；分配方式照抄模板（是否用 unified memory、如何分到多个 GPM）
    // 3. 第 1 层：spmm(X→Tmp) → gemm(Tmp·W1→H1) → relu(H1)
    //    第 2 层：spmm(H1→Tmp) → gemm(Tmp·W2→Out)
}
func (b *Benchmark) Verify() {
    // 留空：不做校验（用户 10-12 的决定）。接口要求必须有这个方法，就保留一个空实现
}
```

然后在 wafer 平台的 sample 或 runner 里注册这个 benchmark，加上命令行参数 `-data-dir`、`-layers`。**照抄你们现在用来跑 MM 的那个入口。**

## 第 5 步：冒烟测试 → 正式模拟 → 合理性检查

1. **`--tiny` 冒烟测试**：用 timing 模式跑小图，确认不崩溃、所有 kernel 都能跑完。几分钟就能出结果。
2. **正式模拟**：用 `run_exp.sh baseline gcn-arxiv cu8`。记录周期数、5 个 kernel 是否都跑完、墙钟时间，以及 T2 的 M1/M2/M3/F。
3. **合理性检查**（只看模拟统计，不额外跑任何东西）：
   - `kernels_completed` 等于 5
   - 实际访问的 footprint 和估算值相差不超过 20%。GCN 的估算值：X 约 87 MB，col_idx 加 val 约 19 MB，row_ptr 约 0.7 MB，中间结果 Tmp 和 H1 各约 87 MB，输出约 27 MB
   - 每个 SpMM 读 X 的字节数大约等于 nnz × 128 × 4（这是上限，cache 命中会让它变小）
   - 日志里没有越界地址或 page fault 警告
4. 结果写进 `STATUS.md`。**如果在预算内跑不完，先报告，不要擅自改输入。**

## 常见的坑

- **参数结构体对不齐**：kernel 读到的是垃圾数据，但不会报错。因为不跑 verify，**只能靠第 5 步的合理性检查发现**：footprint 异常小、地址越界、kernel 提前结束，都是典型的信号。
- **在 OpenCL 里用 `int` 做下标会溢出**：特征矩阵有 2,167 万个元素，目前还没超过 int 的范围；但 products 或更大的图会超，所以统一用 `long`。
- **WG 太多会拖慢模拟**：如果 timing 模式太慢，先看 T4 记下来的预算，再考虑让一个 wavefront 处理多行（只是 launch 参数的改动）。

---

## 其他 workload 和 GCN 的差异

| workload | 模板 | 和 GCN 的主要差异 |
|---|---|---|
| T5c DLRM embedding | spmv 加 relu | 需要 **atomic float add**（T1 确认是否支持，不支持就用 CAS 循环）；数据要用 PyTorch 读取 `.pt.gz` 再转换（只在本机预处理时用） |
| T5a LLM decode | matrixmultiplication 加 kmeans（多 kernel） | kernel 最多，大约 8 种；softmax 需要 `exp`，RMSNorm 需要 `rsqrt`；共享前缀的 KV **在内存里只能有一份** |
| T5d SSB join | pagerank（多阶段） | 从 Crystal（CUDA）移植，**最依赖 T1 的结论**：HIP 路线可用就用 hipify，否则手工改写成 OpenCL；build 阶段需要 atomic CAS |

**建议的移植顺序**：GCN → embedding → LLM → SSB。
