# tier2/parboil_cutcp — Coulombic Potential with Cutoff (Parboil)

Benchmarks direct summation of electrostatic charge interactions on a 3D grid with a distance cutoff. Each GPU thread computes the potential at one grid point by iterating over all atoms within the cutoff radius, computing q/r contributions. Measures throughput in Ginteractions/sec.

Derived from the [Parboil benchmark suite](http://impact.crhc.illinois.edu/parboil/parboil.aspx) CUTCP workload.

## Algorithm

- **num_atoms** charged atoms (default: 10,000) randomly placed in the domain
- Fixed 64×64×64 grid (262,144 grid points) with configurable spacing
- For each grid point, iterate over all atoms and accumulate Coulomb potential (q/r) for atoms within cutoff distance
- Cutoff radius limits the interaction range (default: 12.0 Å)
- Ginteractions/sec = (num_atoms × grid_points) / time_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `parboil_cutcp.cu` | CUDA source — Coulombic potential kernel with cutoff |
| `parboil_cutcp.hip` | HIP source — portable ROCm/CUDA implementation |
| `parboil_cutcp.metal` | Metal compute shader — potential kernel |
| `parboil_cutcp_metal.mm` | ObjC++ Metal host code |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |
| `params.json` | Parameter specification |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./parboil_cutcp                                    # defaults
BENCH_PARAM_num_atoms=50000 ./parboil_cutcp        # more atoms
BENCH_PARAM_cutoff_radius=6.0 ./parboil_cutcp      # smaller cutoff
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"cutcp_kernel","time_ms":12.3456,"params":{"num_atoms":10000,"block_size":128,"grid_spacing":0.50,"cutoff_radius":12.0}}
{"type":"summary","total_time_ms":61.7280,"metrics":[{"name":"ginteractions_per_sec","value":0.2123}]}
```

**stderr** — human-readable:
```
Device: Apple M1 Max
CUTCP  |  Atoms: 10000  |  Grid: 64^3 = 262144  |  Cutoff: 12.0  |  Spacing: 0.50  |  Iterations: 5 warmup + 5 timed

Average time: 12.3456 ms
Throughput:   0.2123 Ginteractions/sec
PASS
```

## Verification

- 64 randomly-selected grid points are compared against CPU reference computation
- Reports PASS/FAIL with mismatch details (relative tolerance 0.1%)

## Platform Notes

- **CUDA/ROCm**: Uses float4 SoA for atom data for coalesced memory access
- **Metal**: Loads `parboil_cutcp.metal` shader at runtime from the same directory as the binary
