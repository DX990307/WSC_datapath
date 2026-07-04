/*
 * cuda_transpose.metal — Metal compute shaders for matrix transpose benchmark.
 *
 * Two kernels:
 *   1. transpose_naive      — direct read/write (coalesced read, strided write)
 *   2. transpose_optimized  — threadgroup memory tile with +1 padding
 *
 * Buffer layout:
 *   buffer(0): idata  [N × N float]  — input matrix (row-major)
 *   buffer(1): odata  [N × N float]  — output transposed matrix
 *   buffer(2): dims   { width, height } (uint2)
 */

#include <metal_stdlib>
using namespace metal;

#define TILE_DIM   32
#define BLOCK_ROWS  8

// ---------------------------------------------------------------------------
// Kernel 1: Naive transpose
// Each thread transposes TILE_DIM/BLOCK_ROWS elements.
// Coalesced reads from idata, strided writes to odata.
// ---------------------------------------------------------------------------

kernel void transpose_naive(
    device const float*  idata [[ buffer(0) ]],
    device       float*  odata [[ buffer(1) ]],
    constant     uint2&  dims  [[ buffer(2) ]],
    uint2 tg_id [[ threadgroup_position_in_grid ]],
    uint2 t_id  [[ thread_position_in_threadgroup ]])
{
    uint width  = dims.x;
    uint height = dims.y;

    uint xIndex = tg_id.x * TILE_DIM + t_id.x;
    uint yBase  = tg_id.y * TILE_DIM + t_id.y;

    for (uint j = 0; j < TILE_DIM; j += BLOCK_ROWS) {
        uint yIndex = yBase + j;
        if (xIndex < width && yIndex < height) {
            odata[xIndex * height + yIndex] = idata[yIndex * width + xIndex];
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: Optimized transpose with threadgroup memory (+1 padding)
// Coalesced read into threadgroup memory, barrier, coalesced write out.
// ---------------------------------------------------------------------------

kernel void transpose_optimized(
    device const float*  idata [[ buffer(0) ]],
    device       float*  odata [[ buffer(1) ]],
    constant     uint2&  dims  [[ buffer(2) ]],
    uint2 tg_id [[ threadgroup_position_in_grid ]],
    uint2 t_id  [[ thread_position_in_threadgroup ]])
{
    // +1 padding to avoid bank conflicts
    threadgroup float tile[TILE_DIM][TILE_DIM + 1];

    uint width  = dims.x;
    uint height = dims.y;

    // Load phase: coalesced read from idata
    uint xIndex = tg_id.x * TILE_DIM + t_id.x;
    uint yBase  = tg_id.y * TILE_DIM + t_id.y;

    for (uint j = 0; j < TILE_DIM; j += BLOCK_ROWS) {
        uint yIndex = yBase + j;
        if (xIndex < width && yIndex < height) {
            tile[t_id.y + j][t_id.x] = idata[yIndex * width + xIndex];
        }
    }

    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Store phase: coalesced write to odata (swapped block indices)
    xIndex = tg_id.y * TILE_DIM + t_id.x;
    yBase  = tg_id.x * TILE_DIM + t_id.y;

    for (uint j = 0; j < TILE_DIM; j += BLOCK_ROWS) {
        uint yIndex = yBase + j;
        if (xIndex < height && yIndex < width) {
            odata[yIndex * height + xIndex] = tile[t_id.x][t_id.y + j];
        }
    }
}
