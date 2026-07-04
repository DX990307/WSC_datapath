/*
 * shoc_devicememory.metal — Metal compute shaders for SHOC DeviceMemory benchmark.
 *
 * Three memory access patterns:
 *   readOnly_kernel   — each thread reads input[], accumulates, writes one result
 *   writeOnly_kernel  — each thread writes a value to output[]
 *   readWrite_kernel  — each thread reads, scales, and writes back to same buffer
 *
 * Buffer layout:
 *   readOnly:   buffer(0)=input (read), buffer(1)=output (write), buffer(2)=params
 *   writeOnly:  buffer(0)=output (write), buffer(1)=params
 *   readWrite:  buffer(0)=data (read+write), buffer(1)=params
 */

#include <metal_stdlib>
using namespace metal;

// params.x = N (number of floats), params.y = totalThreads
kernel void readOnly_kernel(
    device const float*  input   [[ buffer(0) ]],
    device       float*  output  [[ buffer(1) ]],
    constant     uint2&  params  [[ buffer(2) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint N = params.x;
    uint totalThreads = params.y;

    float sum = 0.0f;
    for (uint i = gid; i < N; i += totalThreads) {
        sum += input[i];
    }
    if (gid < N) {
        output[gid] = sum;
    }
}

kernel void writeOnly_kernel(
    device       float*  output  [[ buffer(0) ]],
    constant     uint2&  params  [[ buffer(1) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint N = params.x;
    uint totalThreads = params.y;

    for (uint i = gid; i < N; i += totalThreads) {
        output[i] = (float)i * 0.001f;
    }
}

kernel void readWrite_kernel(
    device       float*  data    [[ buffer(0) ]],
    constant     uint2&  params  [[ buffer(1) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint N = params.x;
    uint totalThreads = params.y;

    for (uint i = gid; i < N; i += totalThreads) {
        data[i] = data[i] * 1.0001f;
    }
}
