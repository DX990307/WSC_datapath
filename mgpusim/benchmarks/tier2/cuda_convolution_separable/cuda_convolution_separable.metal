/*
 * cuda_convolution_separable.metal — Metal compute shaders for separable 2D convolution.
 *
 * Two-pass separable 2D convolution: row pass + column pass.
 * Each pass reads from an input buffer and writes to an output buffer.
 *
 * Kernels:
 *   1. convolution_row_kernel — horizontal convolution pass
 *   2. convolution_col_kernel — vertical convolution pass
 *
 * Buffer layout:
 *   buffer(0): input  — float[W*H]
 *   buffer(1): output — float[W*H]
 *   buffer(2): kernel_weights — float[2*radius+1]
 *   buffer(3): params — { width, height, radius } as uint3
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Row convolution kernel (horizontal pass)
//   Each thread computes one output pixel.
//   Uses threadgroup memory to cache row segments with halo.
// ---------------------------------------------------------------------------

constant int ROW_TILE_W = 128;
constant int BLOCK_ROWS_CONST = 8;

kernel void convolution_row_kernel(
    device const float*   input          [[ buffer(0) ]],
    device float*         output         [[ buffer(1) ]],
    device const float*   kernel_weights [[ buffer(2) ]],
    constant uint3&       params         [[ buffer(3) ]],
    uint2                 gid            [[ thread_position_in_grid ]],
    uint2                 tid            [[ thread_position_in_threadgroup ]],
    uint2                 tgid           [[ threadgroup_position_in_grid ]])
{
    uint width  = params.x;
    uint height = params.y;
    int  radius = (int)params.z;

    int col = (int)(tgid.x * ROW_TILE_W + tid.x);
    int row = (int)gid.y;

    // Shared memory for row tile with halo
    threadgroup float smem[BLOCK_ROWS_CONST * (ROW_TILE_W + 2 * 32)];
    int smem_width = ROW_TILE_W + 2 * radius;
    int smem_col   = (int)tid.x + radius;

    if (row < (int)height) {
        // Center
        smem[tid.y * smem_width + smem_col] =
            (col >= 0 && col < (int)width) ? input[row * (int)width + col] : 0.0f;

        // Left halo
        if ((int)tid.x < radius) {
            int halo_col = col - radius;
            smem[tid.y * smem_width + (int)tid.x] =
                (halo_col >= 0) ? input[row * (int)width + halo_col] : 0.0f;
        }

        // Right halo
        if ((int)tid.x >= ROW_TILE_W - radius) {
            int halo_col = col + radius;
            smem[tid.y * smem_width + smem_col + radius] =
                (halo_col < (int)width) ? input[row * (int)width + halo_col] : 0.0f;
        }
    } else {
        smem[tid.y * smem_width + smem_col] = 0.0f;
        if ((int)tid.x < radius)
            smem[tid.y * smem_width + (int)tid.x] = 0.0f;
        if ((int)tid.x >= ROW_TILE_W - radius)
            smem[tid.y * smem_width + smem_col + radius] = 0.0f;
    }

    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Compute convolution
    if (row < (int)height && col >= 0 && col < (int)width) {
        float sum = 0.0f;
        for (int k = -radius; k <= radius; ++k) {
            sum += smem[tid.y * smem_width + smem_col + k] * kernel_weights[radius + k];
        }
        output[row * (int)width + col] = sum;
    }
}

// ---------------------------------------------------------------------------
// Column convolution kernel (vertical pass)
//   Each thread computes one output pixel.
// ---------------------------------------------------------------------------

kernel void convolution_col_kernel(
    device const float*   input          [[ buffer(0) ]],
    device float*         output         [[ buffer(1) ]],
    device const float*   kernel_weights [[ buffer(2) ]],
    constant uint3&       params         [[ buffer(3) ]],
    uint2                 gid            [[ thread_position_in_grid ]])
{
    uint width  = params.x;
    uint height = params.y;
    int  radius = (int)params.z;

    int col = (int)gid.x;
    int row = (int)gid.y;

    if (col >= (int)width || row >= (int)height) return;

    float sum = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int rr = row + k;
        float val = (rr >= 0 && rr < (int)height) ? input[rr * (int)width + col] : 0.0f;
        sum += val * kernel_weights[radius + k];
    }
    output[row * (int)width + col] = sum;
}
