/*
 * polybench_syr2k.metal — Metal compute shader for the PolyBench SYR2K benchmark.
 *
 * Symmetric rank-2k update: C = alpha*A*B^T + alpha*B*A^T + beta*C
 * Uses tiled threadgroup memory for efficient matrix operations.
 *
 * A is [N x M], B is [N x M], C is [N x N]
 *
 * C[row][col] = alpha * sum_k(A[row][k]*B[col][k])
 *             + alpha * sum_k(B[row][k]*A[col][k])
 *             + beta  * C[row][col]
 *
 * Buffer layout (row-major):
 *   buffer(0): A          [N × M]  float
 *   buffer(1): B          [N × M]  float
 *   buffer(2): C          [N × N]  float  (read+write)
 *   buffer(3): params     { N, M, 0, 0 }  (uint4)
 *   buffer(4): alpha_beta { alpha, beta }  (float2)
 */

#include <metal_stdlib>
using namespace metal;

#define TILE 16

kernel void syr2k_kernel(
    device const float*  A          [[ buffer(0) ]],
    device const float*  B          [[ buffer(1) ]],
    device       float*  C          [[ buffer(2) ]],
    constant     uint4&  params     [[ buffer(3) ]],
    constant     float2& alpha_beta [[ buffer(4) ]],
    uint2 tg_id   [[ threadgroup_position_in_grid ]],
    uint2 t_id    [[ thread_position_in_threadgroup ]])
{
    // Threadgroup (shared) memory tiles
    threadgroup float sA_row[TILE][TILE];
    threadgroup float sB_row[TILE][TILE];
    threadgroup float sA_col[TILE][TILE];
    threadgroup float sB_col[TILE][TILE];

    uint N     = params.x;
    uint M     = params.y;
    float alpha = alpha_beta.x;
    float beta  = alpha_beta.y;

    uint row = tg_id.y * TILE + t_id.y;
    uint col = tg_id.x * TILE + t_id.x;

    float sum = 0.0f;
    uint num_tiles = (M + TILE - 1) / TILE;

    for (uint t = 0; t < num_tiles; ++t) {
        uint k = t * TILE + t_id.x;

        // Load A and B rows for the row-block
        sA_row[t_id.y][t_id.x] = (row < N && k < M) ? A[row * M + k] : 0.0f;
        sB_row[t_id.y][t_id.x] = (row < N && k < M) ? B[row * M + k] : 0.0f;

        // Load A and B rows for the col-block
        uint col_row = tg_id.x * TILE + t_id.y;
        uint k2 = t * TILE + t_id.x;
        sA_col[t_id.y][t_id.x] = (col_row < N && k2 < M) ? A[col_row * M + k2] : 0.0f;
        sB_col[t_id.y][t_id.x] = (col_row < N && k2 < M) ? B[col_row * M + k2] : 0.0f;

        threadgroup_barrier(mem_flags::mem_threadgroup);

        for (uint kk = 0; kk < TILE; ++kk) {
            // A*B^T: A[row][k]*B[col][k]  +  B*A^T: B[row][k]*A[col][k]
            sum += sA_row[t_id.y][kk] * sB_col[t_id.x][kk]
                 + sB_row[t_id.y][kk] * sA_col[t_id.x][kk];
        }

        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (row < N && col < N) {
        C[row * N + col] = alpha * sum + beta * C[row * N + col];
    }
}
