# LLM-Like Matrix Multiplication and Conv2D Workload Shapes

These standalone `matrixmultiplication-*` aliases map the AMDAPP SDK matrix
multiplication benchmark onto LLM linear-layer shapes.

## Dimension Mapping

The matrix benchmark computes:

```text
A [Y x X] * B [X x Z] -> C [Y x Z]
```

For an LLM linear layer, this corresponds to:

```text
rows x input_dim  *  input_dim x output_dim  ->  rows x output_dim
```

So:

```text
Y = rows
X = input_dim
Z = output_dim
```

## GPT-7B-Style Shapes

The aliases use:

```text
hidden       = 4096
intermediate = 11008
prefill rows = 1920
decode rows  = 192
```

`decode rows = 192` models batched decode, not a single token. The current
multi-GPU AMDAPP SDK matrix kernel partitions rows across 48 GPUs and needs
enough rows for each GPU to receive work.

| Alias | LLM phase | LLM op shape | X | Y | Z |
|---|---|---|---:|---:|---:|
| `matrixmultiplication-llm-prefill-attn` | prefill | hidden -> hidden | 4096 | 1920 | 4096 |
| `matrixmultiplication-llm-decode-attn` | decode | hidden -> hidden | 4096 | 192 | 4096 |
| `matrixmultiplication-llm-prefill-mlp-up` | prefill | hidden -> intermediate | 4096 | 1920 | 11008 |
| `matrixmultiplication-llm-decode-mlp-up` | decode | hidden -> intermediate | 4096 | 192 | 11008 |
| `matrixmultiplication-llm-prefill-mlp-down` | prefill | intermediate -> hidden | 11008 | 1920 | 4096 |
| `matrixmultiplication-llm-decode-mlp-down` | decode | intermediate -> hidden | 11008 | 192 | 4096 |

## Conv2D LLM-Like Shapes

The TensorParallelismSample conv2d benchmark uses:

```text
input  [N, C, H, W]
filter [KernelChannel, C, KernelHeight, KernelWidth]
output [N, KernelChannel, outputH, outputW]
```

The new aliases use a token-grid interpretation:

```text
tokens = H * W
C = input feature dimension
KernelChannel = output feature dimension
```

Two variants are useful:

- `pointwise`: 1x1 conv, a direct proxy for an LLM linear layer over tokens.
- `local`: 3x3 conv, still high-channel and token-grid shaped, but keeps
  spatial-neighbor reuse visible for conv2d-specific behavior.

These conv2d shapes are scaled LLM-like shapes rather than exact GPT-7B
dimensions because this conv2d benchmark currently executes as a heavier
standalone layer and internally repeats the forward path.

| Alias | Phase | Variant | N | C | H | W | KernelChannel | KH | KW | Tokens |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `conv2d-llm-prefill-pointwise` | prefill | 1x1 linear proxy | 1 | 1024 | 16 | 32 | 4096 | 1 | 1 | 512 |
| `conv2d-llm-decode-pointwise` | decode | 1x1 linear proxy | 1 | 1024 | 8 | 16 | 4096 | 1 | 1 | 128 |
| `conv2d-llm-prefill-local` | prefill | 3x3 local reuse | 1 | 1024 | 16 | 32 | 1024 | 3 | 3 | 512 |
| `conv2d-llm-decode-local` | decode | 3x3 local reuse | 1 | 1024 | 8 | 16 | 1024 | 3 | 3 | 128 |

## RunAll Preset

The `runall2.py` preset `llm-like-bottleneck` expands to all matrix
multiplication LLM aliases and all conv2d LLM-like aliases above.

```sh
python3 runall2.py \
  --benchmarks llm-like-bottleneck \
  --configs baseline \
  --report-l2-source \
  --trace-sharing \
  --trace-sharing-sample 1 \
  --trace-sharing-max-records 1000000 \
  --disable-servers \
  --max-workers 2
```

## Example Pipeline Trace Command

```sh
./baseline/baseline \
  -benchmark=matrixmultiplication-llm-decode-attn \
  -timing \
  -num-memory-banks=16 \
  -bandwidth=48 \
  -switch-latency=32 \
  -magic-memory-copy \
  -report-all \
  -disable-servers \
  -report-l2-source \
  -l2-source-tile-width=7 \
  -mmutlb-lookup-latency=80 \
  -metric-file-name=/path/to/results/matmul_llm_decode_attn_metrics \
  -trace-sharing \
  -trace-sharing-file=/path/to/results/matmul_llm_decode_attn_sharing.csv.gz \
  -trace-sharing-sample=1 \
  -trace-sharing-max-records=1000000
```
