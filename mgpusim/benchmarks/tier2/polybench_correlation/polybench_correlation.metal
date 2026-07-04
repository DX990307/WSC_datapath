/*
 * polybench_correlation.metal — Metal compute shaders for Correlation benchmark.
 *
 * Steps: mean, stddev, normalize, correlation (matmul-like).
 *
 * Buffer layout for all kernels:
 *   buffer(0): data    [M * N] float  (read or read+write depending on kernel)
 *   buffer(1): varies per kernel (mean, stddev, or corr output)
 *   buffer(2): varies per kernel
 *   buffer(3): params  { M, N, 0, 0 }  uint4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Kernel 1: Column means
// mean[j] = sum_i(data[i*N + j]) / M
// ---------------------------------------------------------------------------
kernel void mean_kernel(
    device const float*  data   [[ buffer(0) ]],
    device       float*  mean   [[ buffer(1) ]],
    constant     uint4&  params [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint M = params.x;
    uint N = params.y;
    uint j = gid;
    if (j >= N) return;

    float sum = 0.0f;
    for (uint i = 0; i < M; ++i) {
        sum += data[i * N + j];
    }
    mean[j] = sum / (float)M;
}

// ---------------------------------------------------------------------------
// Kernel 2: Column standard deviations
// ---------------------------------------------------------------------------
kernel void stddev_kernel(
    device const float*  data   [[ buffer(0) ]],
    device const float*  mean   [[ buffer(1) ]],
    device       float*  sd     [[ buffer(2) ]],
    constant     uint4&  params [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint M = params.x;
    uint N = params.y;
    uint j = gid;
    if (j >= N) return;

    float m = mean[j];
    float sum = 0.0f;
    for (uint i = 0; i < M; ++i) {
        float diff = data[i * N + j] - m;
        sum += diff * diff;
    }
    float s = sqrt(sum / (float)M);
    sd[j] = (s < 1e-12f) ? 1.0f : s;
}

// ---------------------------------------------------------------------------
// Kernel 3: Normalize
// data[idx] = (data[idx] - mean[j]) / (sqrt(M) * stddev[j])
// ---------------------------------------------------------------------------
kernel void normalize_kernel(
    device       float*  data   [[ buffer(0) ]],
    device const float*  mean   [[ buffer(1) ]],
    device const float*  sd     [[ buffer(2) ]],
    constant     uint4&  params [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint M = params.x;
    uint N = params.y;
    if (gid >= M * N) return;

    uint j = gid % N;
    float sqrt_m = sqrt((float)M);
    data[gid] = (data[gid] - mean[j]) / (sqrt_m * sd[j]);
}

// ---------------------------------------------------------------------------
// Kernel 4: Correlation matrix (tiled data^T * data)
// corr[row][col] = sum_k(data[k][row] * data[k][col]), diagonal = 1.0
// ---------------------------------------------------------------------------
#define TILE 16

kernel void correlation_kernel(
    device const float*  data   [[ buffer(0) ]],
    device       float*  corr   [[ buffer(1) ]],
    constant     uint4&  params [[ buffer(3) ]],
    uint2 tg_id [[ threadgroup_position_in_grid ]],
    uint2 t_id  [[ thread_position_in_threadgroup ]])
{
    uint M = params.x;
    uint N = params.y;

    threadgroup float sA[TILE][TILE];
    threadgroup float sB[TILE][TILE];

    uint row = tg_id.y * TILE + t_id.y;
    uint col = tg_id.x * TILE + t_id.x;

    float sum = 0.0f;
    uint num_tiles = (M + TILE - 1) / TILE;

    for (uint t = 0; t < num_tiles; ++t) {
        uint k_a = t * TILE + t_id.x;
        uint k_b = t * TILE + t_id.y;

        sA[t_id.y][t_id.x] = (row < N && k_a < M) ? data[k_a * N + row] : 0.0f;
        sB[t_id.y][t_id.x] = (k_b < M && col < N) ? data[k_b * N + col] : 0.0f;

        threadgroup_barrier(mem_flags::mem_threadgroup);

        for (uint k = 0; k < TILE; ++k) {
            sum += sA[t_id.y][k] * sB[k][t_id.x];
        }

        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (row < N && col < N) {
        if (row == col)
            corr[row * N + col] = 1.0f;
        else
            corr[row * N + col] = sum;
    }
}
