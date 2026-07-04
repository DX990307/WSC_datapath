/*
 * polybench_3mm.metal — Metal compute shaders for the PolyBench 3mm benchmark.
 *
 * Three chained matrix multiplications (all N×N, single-precision):
 *   Kernel 1:  E = A * B
 *   Kernel 2:  F = C * D
 *   Kernel 3:  G = E * F
 *
 * Each kernel: one thread per output element, simple dot-product loop.
 *
 * Buffer layout for each kernel:
 *   buffer(0): left-hand matrix  (row-major float)
 *   buffer(1): right-hand matrix (row-major float)
 *   buffer(2): output matrix     (row-major float, write)
 *   buffer(3): dims { NI, NK, NJ, 0 } (uint4) — rows, inner, cols, padding
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Kernel 1: E[NI×NJ] = A[NI×NK] * B[NK×NJ]
//   dims.x = NI, dims.y = NK, dims.z = NJ
// ---------------------------------------------------------------------------
kernel void mm3_kernel1(
    device const float* A    [[ buffer(0) ]],
    device const float* B    [[ buffer(1) ]],
    device       float* E    [[ buffer(2) ]],
    constant     uint4& dims [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NI = dims.x;
    uint NK = dims.y;
    uint NJ = dims.z;

    uint j = gid.x;
    uint i = gid.y;

    if (i >= NI || j >= NJ) return;

    float sum = 0.0f;
    for (uint k = 0; k < NK; k++)
        sum += A[i * NK + k] * B[k * NJ + j];
    E[i * NJ + j] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 2: F[NJ×NL] = C[NJ×NM] * D[NM×NL]
//   dims.x = NJ, dims.y = NM, dims.z = NL
// ---------------------------------------------------------------------------
kernel void mm3_kernel2(
    device const float* C    [[ buffer(0) ]],
    device const float* D    [[ buffer(1) ]],
    device       float* F    [[ buffer(2) ]],
    constant     uint4& dims [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NJ = dims.x;
    uint NM = dims.y;
    uint NL = dims.z;

    uint l = gid.x;
    uint j = gid.y;

    if (j >= NJ || l >= NL) return;

    float sum = 0.0f;
    for (uint m = 0; m < NM; m++)
        sum += C[j * NM + m] * D[m * NL + l];
    F[j * NL + l] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 3: G[NI×NL] = E[NI×NJ] * F[NJ×NL]
//   dims.x = NI, dims.y = NJ, dims.z = NL
// ---------------------------------------------------------------------------
kernel void mm3_kernel3(
    device const float* E    [[ buffer(0) ]],
    device const float* F    [[ buffer(1) ]],
    device       float* G    [[ buffer(2) ]],
    constant     uint4& dims [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NI = dims.x;
    uint NJ = dims.y;
    uint NL = dims.z;

    uint l = gid.x;
    uint i = gid.y;

    if (i >= NI || l >= NL) return;

    float sum = 0.0f;
    for (uint j = 0; j < NJ; j++)
        sum += E[i * NJ + j] * F[j * NL + l];
    G[i * NL + l] = sum;
}
