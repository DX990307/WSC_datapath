/*
 * polybench_3dconv.metal — Metal compute shader for 3D Convolution benchmark.
 *
 * 3D convolution over an NxNxN volume with a small 3D filter.
 * Each output point is the weighted sum of its neighborhood.
 *
 * Buffer layout:
 *   buffer(0): input   [N * N * N]  float
 *   buffer(1): filter  [FS * FS * FS]  float
 *   buffer(2): output  [N * N * N]  float
 *   buffer(3): params  { N, filter_size, 0, 0 }  uint4
 */

#include <metal_stdlib>
using namespace metal;

kernel void conv3d_kernel(
    device const float*  input   [[ buffer(0) ]],
    device const float*  filter  [[ buffer(1) ]],
    device       float*  output  [[ buffer(2) ]],
    constant     uint4&  params  [[ buffer(3) ]],
    uint3 gid [[ thread_position_in_grid ]])
{
    uint N           = params.x;
    uint filter_size = params.y;

    uint i = gid.z;
    uint j = gid.y;
    uint k = gid.x;

    if (i >= N || j >= N || k >= N) return;

    int half_f = (int)filter_size / 2;
    float sum = 0.0f;

    for (uint fi = 0; fi < filter_size; ++fi) {
        int ii = (int)i - half_f + (int)fi;
        if (ii < 0 || ii >= (int)N) continue;
        for (uint fj = 0; fj < filter_size; ++fj) {
            int jj = (int)j - half_f + (int)fj;
            if (jj < 0 || jj >= (int)N) continue;
            for (uint fk = 0; fk < filter_size; ++fk) {
                int kk = (int)k - half_f + (int)fk;
                if (kk < 0 || kk >= (int)N) continue;
                sum += input[((uint)ii * N + (uint)jj) * N + (uint)kk]
                     * filter[(fi * filter_size + fj) * filter_size + fk];
            }
        }
    }

    output[(i * N + j) * N + k] = sum;
}
