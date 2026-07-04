# tier2/heteromark_aes — AES-256 ECB Encryption (HeteroMark)

Benchmarks AES-256 encryption in ECB mode on the GPU. Each thread encrypts one 16-byte block through 14 rounds. Measures throughput in GB/s.

Derived from the [HeteroMark benchmark suite](https://github.com/NUCAR-DEV/Hetero-Mark) AES workload.

## Algorithm

- **N** 16-byte plaintext blocks (default: N=1,048,576 = 1M blocks = 16 MB)
- AES-256 ECB encryption (14 rounds per block):
  1. **SubBytes** — S-box substitution for each byte
  2. **ShiftRows** — cyclic row shifts in the 4×4 state matrix
  3. **MixColumns** — column mixing via GF(2⁸) multiplication (rounds 1–13 only)
  4. **AddRoundKey** — XOR state with round key
- 256-bit key → 15 round keys (240 bytes) via key expansion
- Each GPU thread processes one independent block (embarrassingly parallel)
- GB/s = N × 16 / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `heteromark_aes.hip` | HIP source — AES-256 ECB kernel, key expansion, verification |
| `heteromark_aes.metal` | Metal compute shader — AES-256 ECB kernel |
| `heteromark_aes_metal.mm` | ObjC++ Metal host code |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./heteromark_aes                              # defaults: N=1048576, 5 iterations
./heteromark_aes --size 65536                 # fewer blocks
./heteromark_aes --size 1048576 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
heteromark_aes,1048576,8.5432,1.9600
```

Format: `heteromark_aes,<N_blocks>,<time_ms>,<GBs>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
AES-256 ECB  |  Blocks: 1048576  |  Data: 16.00 MB  |  Iterations: 5 warmup + 5 timed

Average time: 8.5432 ms
Throughput:   1.9600 GB/s
PASS
```

## Verification

- First 256 blocks are compared byte-by-byte against a CPU reference implementation
- Reports PASS/FAIL with mismatch details

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `heteromark_aes.hip`; S-box in `__constant__` memory
- **Metal**: compiled with `clang++` from `heteromark_aes_metal.mm`; the Metal shader (`heteromark_aes.metal`) is loaded and compiled at runtime from the same directory as the binary; S-box in `constant` address space
