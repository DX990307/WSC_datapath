/*
 * altis_gups.metal — Metal compute shader for GUPS benchmark.
 *
 * Each thread performs num_updates random XOR updates on a table of
 * 32-bit entries using xorshift32 PRNG.  Metal does not support 64-bit
 * atomics (atomic_fetch_xor on ulong), so we use uint (32-bit) table
 * entries with atomic_fetch_xor_explicit on atomic_uint.
 *
 * Buffer layout:
 *   buffer(0): table      — atomic_uint[table_size], the data table
 *   buffer(1): params     — { uint table_size, uint num_updates, uint seed_base }
 */

#include <metal_stdlib>
using namespace metal;

struct GUPSParams {
    uint   table_size;
    uint   num_updates;
    uint   seed_base;
};

kernel void gups_kernel(
    device atomic_uint*     table   [[ buffer(0) ]],
    constant GUPSParams&    params  [[ buffer(1) ]],
    uint                    tid     [[ thread_position_in_grid ]])
{
    // Per-thread xorshift32 state seeded uniquely
    uint state = params.seed_base + tid * 2654435761u + 1u;
    if (state == 0u) state = 1u;

    uint tsize = params.table_size;

    for (uint u = 0; u < params.num_updates; ++u) {
        // xorshift32
        state ^= state << 13;
        state ^= state >> 17;
        state ^= state << 5;

        uint idx = state % tsize;
        atomic_fetch_xor_explicit(&table[idx], state, memory_order_relaxed);
    }
}
