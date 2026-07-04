/*
 * polybench_2mm.metal — Metal compute shader for PolyBench 2MM benchmark.
 *
 * Tiled matrix multiply: Out = alpha * P * Q + beta * Out
 * Uses threadgroup memory for 16x16 tiles.
 *
 * Buffer layout (row-major, square NxN):
 *   buffer(0): P          [N × N]  float  (read-only)
 *   buffer(1): Q          [N × N]  float  (read-only)
 *   buffer(2): Out        [N × N]  float  (read+write)
 *   buffer(3): params     { N, 0, 0, 0 }  (uint4)
 *   buffer(4): alpha_beta { alpha, beta }  (float2)
 */

#include <metal_stdlib>
using namespace metal;

#define TILE 16

kernel void mm_kernel(
    device const float*  P          [[ buffer(0) ]],
    device const float*  Q          [[ buffer(1) ]],
    device       float*  Out        [[ buffer(2) ]],
    constant     uint4&  params     [[ buffer(3) ]],
    constant     float2& alpha_beta [[ buffer(4) ]],
    uint2 tg_id   [[ threadgroup_position_in_grid ]],
    uint2 t_id    [[ thread_position_in_threadgroup ]])
{
    threadgroup float sP[TILE][TILE];
    threadgroup float sQ[TILE][TILE];

    uint N     = params.x;
    float alpha = alpha_beta.x;
    float beta  = alpha_beta.y;

    uint row = tg_id.y * TILE + t_id.y;
    uint col = tg_id.x * TILE + t_id.x;

    float sum = 0.0f;
    uint num_tiles = (N + TILE - 1) / TILE;

    for (uint t = 0; t < num_tiles; ++t) {
        uint pCol = t * TILE + t_id.x;
        uint qRow = t * TILE + t_id.y;

        sP[t_id.y][t_id.x] = (row < N && pCol < N) ? P[row * N + pCol] : 0.0f;
        sQ[t_id.y][t_id.x] = (qRow < N && col < N) ? Q[qRow * N + col] : 0.0f;

        threadgroup_barrier(mem_flags::mem_threadgroup);

        for (uint k = 0; k < TILE; ++k) {
            sum += sP[t_id.y][k] * sQ[k][t_id.x];
        }

        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (row < N && col < N) {
        Out[row * N + col] = alpha * sum + beta * Out[row * N + col];
    }
}
