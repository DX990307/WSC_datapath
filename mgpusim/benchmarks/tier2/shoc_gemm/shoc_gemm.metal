/*
 * shoc_gemm.metal — Metal compute shader for the SHOC GEMM benchmark.
 *
 * Dense matrix multiply: C = alpha * A * B + beta * C
 * Uses tiled shared-memory (threadgroup memory) multiplication for efficiency.
 *
 * Each threadgroup computes a TILE_SIZE×TILE_SIZE tile of the output matrix C.
 * Buffer layout (row-major, square N×N):
 *   buffer(0): A  [M × K]
 *   buffer(1): B  [K × N]
 *   buffer(2): C  [M × N]  (read+write)
 *   buffer(3): dims      { M, N, K, 0 }  (uint4)
 *   buffer(4): alpha_beta { alpha, beta } (float2)
 */

#include <metal_stdlib>
using namespace metal;

#define TILE 16

kernel void gemm_kernel(
    device const float*  A          [[ buffer(0) ]],
    device const float*  B          [[ buffer(1) ]],
    device       float*  C          [[ buffer(2) ]],
    constant     uint4&  dims       [[ buffer(3) ]],
    constant     float2& alpha_beta [[ buffer(4) ]],
    uint2 tg_id   [[ threadgroup_position_in_grid ]],
    uint2 t_id    [[ thread_position_in_threadgroup ]])
{
    // Threadgroup (shared) memory tiles — declared as local variables
    threadgroup float sA[TILE][TILE];
    threadgroup float sB[TILE][TILE];

    uint M = dims.x;
    uint N = dims.y;
    uint K = dims.z;
    float alpha = alpha_beta.x;
    float beta  = alpha_beta.y;

    uint row = tg_id.y * TILE + t_id.y;
    uint col = tg_id.x * TILE + t_id.x;

    float sum = 0.0f;
    uint num_tiles = (K + TILE - 1) / TILE;

    for (uint t = 0; t < num_tiles; ++t) {
        uint aCol = t * TILE + t_id.x;
        uint bRow = t * TILE + t_id.y;

        sA[t_id.y][t_id.x] = (row < M && aCol < K) ? A[row * K + aCol] : 0.0f;
        sB[t_id.y][t_id.x] = (bRow < K && col < N) ? B[bRow * N + col] : 0.0f;

        threadgroup_barrier(mem_flags::mem_threadgroup);

        for (uint k = 0; k < TILE; ++k) {
            sum += sA[t_id.y][k] * sB[k][t_id.x];
        }

        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (row < M && col < N) {
        C[row * N + col] = alpha * sum + beta * C[row * N + col];
    }
}
