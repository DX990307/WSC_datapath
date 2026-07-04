/*
 * rodinia_pathfinder.metal — Metal compute shader for Rodinia PathFinder.
 *
 * Computes one row of the minimum-cost path dynamic programming sweep.
 * Each thread handles one column.
 *
 * Algorithm:
 *   dst[col] = wall[row_t * cols + col] + min(src[col-1], src[col], src[col+1])
 *
 * Buffer layout:
 *   buffer(0): src   [COLS] (int32) — min costs from previous row
 *   buffer(1): dst   [COLS] (int32) — min costs for current row (output)
 *   buffer(2): wall  [ROWS * COLS] (int32) — grid costs (row-major)
 *   buffer(3): params {cols, row_t} (uint2) — grid columns and current row index
 */

#include <metal_stdlib>
using namespace metal;

kernel void dynproc_kernel(
    device const int*   src    [[ buffer(0) ]],
    device       int*   dst    [[ buffer(1) ]],
    device const int*   wall   [[ buffer(2) ]],
    constant     uint2& params [[ buffer(3) ]],
    uint tid [[ thread_position_in_grid ]])
{
    uint cols  = params.x;
    uint row_t = params.y;

    if (tid >= cols) return;

    // INT_MAX sentinel for out-of-bounds neighbors
    const int INF = 0x7fffffff;

    int left  = (tid > 0)       ? src[tid - 1] : INF;
    int above = src[tid];
    int right = (tid < cols-1)  ? src[tid + 1] : INF;

    int min3 = min(min(left, above), right);
    dst[tid] = wall[row_t * cols + tid] + min3;
}
