/*
 * parboil_sgemm.metal — Metal compute shader for the Parboil SGEMM benchmark.
 *
 * Tiled single-precision GEMM: C = alpha * A * B + beta * C
 * Uses tiled threadgroup memory (16x16) for improved memory access efficiency.
 *
 * Buffer layout (row-major, square N×N):
 *   buffer(0): A          [N × N] float
 *   buffer(1): B          [N × N] float
 *   buffer(2): C          [N × N] float  (read+write)
 *   buffer(3): params     { N, pad0, pad1, pad2 }  (uint4)
 *   buffer(4): alpha_beta { alpha, beta }           (float2)
 */

#include <metal_stdlib>
using namespace metal;

#define TILE 16

kernel void sgemm_kernel(
    device const float*  A          [[ buffer(0) ]],
    device const float*  B          [[ buffer(1) ]],
    device       float*  C          [[ buffer(2) ]],
    constant     uint4&  params     [[ buffer(3) ]],
    constant     float2& alpha_beta [[ buffer(4) ]],
    uint2 tg_id [[ threadgroup_position_in_grid ]],
    uint2 t_id  [[ thread_position_in_threadgroup ]])
{
    threadgroup float tA[TILE][TILE];
    threadgroup float tB[TILE][TILE];

    uint N     = params.x;
    float alpha = alpha_beta.x;
    float beta  = alpha_beta.y;

    uint row = tg_id.y * TILE + t_id.y;
    uint col = tg_id.x * TILE + t_id.x;

    float sum = 0.0f;
    uint num_tiles = (N + TILE - 1) / TILE;

    for (uint t = 0; t < num_tiles; ++t) {
        uint aCol = t * TILE + t_id.x;
        uint bRow = t * TILE + t_id.y;

        tA[t_id.y][t_id.x] = (row < N && aCol < N) ? A[row * N + aCol] : 0.0f;
        tB[t_id.y][t_id.x] = (bRow < N && col < N) ? B[bRow * N + col] : 0.0f;

        threadgroup_barrier(mem_flags::mem_threadgroup);

        for (uint k = 0; k < TILE; ++k) {
            sum += tA[t_id.y][k] * tB[k][t_id.x];
        }

        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (row < N && col < N) {
        C[row * N + col] = alpha * sum + beta * C[row * N + col];
    }
}
