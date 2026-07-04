/*
 * rodinia_hotspot.metal — Metal compute shader for the Rodinia Hotspot benchmark.
 *
 * Iterative 2D stencil thermal simulation:
 *   T_new[i][j] = T[i][j] + step_div_cap * (
 *       power[i][j]
 *       + (T[i-1][j] + T[i+1][j] - 2*T[i][j]) * Ry_1
 *       + (T[i][j-1] + T[i][j+1] - 2*T[i][j]) * Rx_1
 *       + (AMB_TEMP  - T[i][j]) * Rz_1
 *   )
 *
 * Buffer layout:
 *   buffer(0): temp_src  [grid_rows × grid_cols] (read)
 *   buffer(1): temp_dst  [grid_rows × grid_cols] (write)
 *   buffer(2): power     [grid_rows × grid_cols] (read)
 *   buffer(3): params    { grid_cols, grid_rows, step_div_cap_bits, Rx_1_bits,
 *                           Ry_1_bits, Rz_1_bits }
 *
 * Float constants are passed as their uint bit patterns via as_type<float>(uint).
 */

#include <metal_stdlib>
using namespace metal;

#define AMB_TEMP 80.0f

kernel void hotspot_kernel(
    device const float*  temp_src  [[ buffer(0) ]],
    device       float*  temp_dst  [[ buffer(1) ]],
    device const float*  power     [[ buffer(2) ]],
    constant     uint4&  dims      [[ buffer(3) ]],  // {grid_cols, grid_rows, -, -}
    constant     float4& therm     [[ buffer(4) ]],  // {step_div_cap, Rx_1, Ry_1, Rz_1}
    uint2 gid [[ thread_position_in_grid ]])
{
    uint grid_cols = dims.x;
    uint grid_rows = dims.y;

    uint col = gid.x;
    uint row = gid.y;

    if (col >= grid_cols || row >= grid_rows) return;

    float step_div_cap = therm.x;
    float Rx_1         = therm.y;
    float Ry_1         = therm.z;
    float Rz_1         = therm.w;

    uint idx = row * grid_cols + col;

    float temp_c = temp_src[idx];
    float temp_n = (row > 0)             ? temp_src[(row - 1) * grid_cols + col] : temp_c;
    float temp_s = (row < grid_rows - 1) ? temp_src[(row + 1) * grid_cols + col] : temp_c;
    float temp_w = (col > 0)             ? temp_src[row * grid_cols + (col - 1)] : temp_c;
    float temp_e = (col < grid_cols - 1) ? temp_src[row * grid_cols + (col + 1)] : temp_c;

    float delta = step_div_cap * (
        power[idx]
        + (temp_n + temp_s - 2.0f * temp_c) * Ry_1
        + (temp_w + temp_e - 2.0f * temp_c) * Rx_1
        + (AMB_TEMP - temp_c) * Rz_1
    );

    temp_dst[idx] = temp_c + delta;
}
