/*
 * cuda_nbody.metal — Metal compute shader for the N-body benchmark.
 *
 * All-pairs N-body gravitational simulation with threadgroup (tile-based)
 * shared memory optimization.
 *
 * One kernel:
 *   nbody_kernel — computes forces, updates velocity and position
 *
 * Buffer layout:
 *   buffer(0): pos — float4[N], position (xyz) + mass (w)
 *   buffer(1): vel — float4[N], velocity (xyz) + unused (w)
 *   buffer(2): params — uint N
 */

#include <metal_stdlib>
using namespace metal;

#define TILE_SIZE 256
#define SOFTENING 1e-5f
#define DT        0.01f

kernel void nbody_kernel(
    device float4*       pos     [[ buffer(0) ]],
    device float4*       vel     [[ buffer(1) ]],
    constant uint&       N       [[ buffer(2) ]],
    uint                 gid     [[ thread_position_in_grid ]],
    uint                 lid     [[ thread_position_in_threadgroup ]],
    uint                 tgSize  [[ threads_per_threadgroup ]])
{
    float4 myPos = (gid < N) ? pos[gid] : float4(0.0f);

    float3 acc = float3(0.0f);

    threadgroup float4 shPos[TILE_SIZE];

    for (uint tile = 0; tile < N; tile += TILE_SIZE) {
        uint idx = tile + lid;
        shPos[lid] = (idx < N) ? pos[idx] : float4(0.0f);

        threadgroup_barrier(mem_flags::mem_threadgroup);

        if (gid < N) {
            for (uint j = 0; j < TILE_SIZE; ++j) {
                float3 d = shPos[j].xyz - myPos.xyz;
                float distSqr = dot(d, d) + SOFTENING;
                float invDist  = rsqrt(distSqr);
                float invDist3 = invDist * invDist * invDist;
                float mass_j   = shPos[j].w;

                acc += d * invDist3 * mass_j;
            }
        }

        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (gid < N) {
        float4 myVel = vel[gid];
        myVel.xyz += acc * DT;
        myPos.xyz += myVel.xyz * DT;

        pos[gid] = myPos;
        vel[gid] = myVel;
    }
}
