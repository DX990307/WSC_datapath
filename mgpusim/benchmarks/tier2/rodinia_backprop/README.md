# rodinia_backprop — Backpropagation Benchmark

A GPU compute benchmark implementing a two-layer fully-connected neural network
forward and backward pass, based on the Rodinia benchmark suite.

## Algorithm

The benchmark trains a simple two-layer neural network for one iteration:

```
input (INPUT_N) ──[w1, b1]──> hidden (HIDDEN_N) ──[w2, b2]──> output (OUTPUT_N)
```

### Forward Pass
1. **forward_hidden**: `hidden[j] = sigmoid(Σᵢ input[i]·w1[i·HIDDEN_N+j] + b1[j])`
2. **forward_output**: `output[k] = sigmoid(Σⱼ hidden[j]·w2[j·OUTPUT_N+k] + b2[k])`

### Backward Pass
3. **backward_output_delta**: `delta_out[k] = output[k]·(1-output[k])·(target[k]-output[k])`
4. **backward_hidden_delta**: `delta_hid[j] = hidden[j]·(1-hidden[j])·Σₖ w2[j·OUTPUT_N+k]·delta_out[k]`
5. **update_w1**: `w1[i·HIDDEN_N+j] += lr·input[i]·delta_hid[j]`  (2D grid)
6. **update_w2**: `w2[j·OUTPUT_N+k] += lr·hidden[j]·delta_out[k]`

Where `sigmoid(x) = 1 / (1 + e^(-x))` and `lr = 0.1`.

## Default Network Dimensions

| Parameter | Default |
|-----------|---------|
| INPUT_N   | 65536   |
| HIDDEN_N  | 1024    |
| OUTPUT_N  | 1       |
| Iterations| 5       |

> The w1 weight matrix is `INPUT_N × HIDDEN_N = 65536 × 1024 = 64M floats = 256 MB`.

## Files

| File | Description |
|------|-------------|
| `rodinia_backprop.hip` | HIP/CUDA kernel + host (NVIDIA & AMD) |
| `rodinia_backprop.metal` | Metal compute shaders (Apple) |
| `rodinia_backprop_metal.mm` | Metal Objective-C++ host (Apple) |
| `Makefile` | Build system with auto-detection |
| `README.md` | This file |

## Build

```bash
# Auto-detect platform
make

# Explicit platform
make PLATFORM=cuda    # NVIDIA CUDA
make PLATFORM=rocm    # AMD ROCm/HIP
make PLATFORM=metal   # Apple Metal

# Clean
make clean
```

### Requirements

| Platform | Requirement |
|----------|-------------|
| CUDA     | NVIDIA GPU + CUDA Toolkit, `nvcc` |
| ROCm     | AMD GPU + ROCm, `hipcc` (at `/opt/rocm`) |
| Metal    | Apple Silicon or Intel Mac + macOS 10.14+ |

## Run

```bash
./rodinia_backprop                              # defaults
./rodinia_backprop --input 65536 --hidden 1024 # explicit sizes
./rodinia_backprop --iterations 10             # more iterations
```

## Output Format

**stderr** — device info + performance details:
```
Device: Apple M2 Pro
Network: 65536 → 1024 → 1  |  Iterations: 5

GFLOPS: 42.35  (avg 6.4231 ms, min 6.3980 ms, max 6.4890 ms)
```

**stdout** — CSV result:
```
backprop,65536,1024,6.4231,42.35
```

CSV columns: `benchmark, input_n, hidden_n, time_ms, GFLOPS`

## GFLOPS Formula

```
FLOPs per iteration ≈ 4 × INPUT_N × HIDDEN_N + 4 × HIDDEN_N × OUTPUT_N
                     (forward + backward, 2 FLOPs per multiply-add)

GFLOPS = FLOPs / (time_s × 10⁹)
```

## Notes

- Weights (`w1`, `w2`) initialized uniformly in `[-0.1, 0.1]`
- Biases (`b1`, `b2`) initialized to `0`
- Input initialized as `input[i] = (i % 256) / 256.0`
- Target set to `1.0` for all output units
- One warmup iteration before timing begins
