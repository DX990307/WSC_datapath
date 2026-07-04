# lonestar_bh — Barnes-Hut N-body Simulation

**Suite:** Lonestar GPU  
**Tier:** 2 — Established Benchmark Suites

## Algorithm

The Barnes-Hut algorithm is an O(N log N) approximation to the N-body gravitational
force calculation. Instead of computing all N² pairwise interactions, it builds a
spatial tree (quadtree in 2D) and approximates distant groups of bodies as a single
point mass.

### Implementation

This is a simplified 2D Barnes-Hut implementation:

1. **Tree build (CPU):** Construct a 2D quadtree from body positions. Each leaf
   holds a single body; internal nodes store center-of-mass and total mass.
2. **Force calculation (GPU):** Each thread computes the force on one body by
   traversing the quadtree with a stack. The opening criterion uses θ = 0.5:
   if `(node_width / distance) < θ`, the node's center-of-mass approximation
   is used; otherwise, the node's children are opened.
3. **Integration (GPU):** Update velocities and positions using Euler integration.
4. **Repeat** for 10 timesteps (tree is rebuilt on CPU each step).

### Parameters

| Parameter    | Default | Description                     |
|--------------|---------|---------------------------------|
| `--bodies`   | 32768   | Number of bodies                |
| `--timesteps`| 10      | Simulation timesteps per run    |
| `--theta`    | 0.5     | Opening angle for BH criterion  |
| `--iterations`| 5      | Timed iterations                |

## Build

```bash
make                    # auto-detect (Metal on macOS)
make PLATFORM=cuda      # NVIDIA CUDA
make PLATFORM=rocm      # AMD ROCm
make PLATFORM=metal     # Apple Metal
```

## Run

```bash
./lonestar_bh
./lonestar_bh --bodies 65536 --timesteps 20
```

## Output

**CSV (stdout):**
```
lonestar_bh,32768,<time_ms>,<GFLOPS>
```

**Human-readable (stderr):**
```
Device: Apple M2
Barnes-Hut N-body  |  Bodies: 32768  |  Theta: 0.50  |  Timesteps: 10
Estimated FLOPs per run: 1.23e+10
Average time: 45.1234 ms
Throughput:   12.3456 GFLOPS
Max relative error: 0.1234 (over 64 bodies)
PASS
```

## Verification

Forces computed by the BH approximation are compared against direct O(N²)
computation on a subset of 64 bodies. Since BH is an approximation, a relative
error threshold of 50% is used (typical BH error with θ=0.5 is ~10-30%).

## References

- Barnes, J., Hut, P. (1986). "A hierarchical O(N log N) force-calculation algorithm"
- Burtscher, M., Pingali, K. (2011). "An Efficient CUDA Implementation of the Tree-Based Barnes Hut n-Body Algorithm" (Lonestar GPU)
