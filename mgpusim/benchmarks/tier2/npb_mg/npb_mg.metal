/*
 * npb_mg.metal — Metal compute shaders for NPB Multi-Grid.
 *
 * Kernels:
 *   1. smooth_kernel   — Jacobi smoothing on 3D grid
 *   2. restrict_kernel — Fine-to-coarse restriction (2:1)
 *   3. prolong_kernel  — Coarse-to-fine prolongation
 *   4. residual_kernel — Compute residual r = rhs - A*u
 *   5. norm_sq_kernel  — Squared norm with block reduction
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Parameter structs
// ---------------------------------------------------------------------------

struct MgParams {
    uint N;
    uint N_coarse;
    float weight;
};

// ---------------------------------------------------------------------------
// 3D indexing helper
// ---------------------------------------------------------------------------

static inline int idx3d(int x, int y, int z, int N) {
    return x + y * N + z * N * N;
}

// ---------------------------------------------------------------------------
// Kernel: Jacobi smooth
// ---------------------------------------------------------------------------

kernel void smooth_kernel(
    device const float* u       [[ buffer(0) ]],
    device const float* rhs     [[ buffer(1) ]],
    device float*       out     [[ buffer(2) ]],
    constant MgParams&  params  [[ buffer(3) ]],
    uint                gid     [[ thread_position_in_grid ]])
{
    int N = (int)params.N;
    int total = N * N * N;
    if ((int)gid >= total) return;

    int x = (int)gid % N;
    int y = ((int)gid / N) % N;
    int z = (int)gid / (N * N);

    if (x == 0 || x == N-1 || y == 0 || y == N-1 || z == 0 || z == N-1) {
        out[gid] = u[gid];
        return;
    }

    float s = u[idx3d(x-1,y,z,N)] + u[idx3d(x+1,y,z,N)]
            + u[idx3d(x,y-1,z,N)] + u[idx3d(x,y+1,z,N)]
            + u[idx3d(x,y,z-1,N)] + u[idx3d(x,y,z+1,N)];

    out[gid] = (s + params.weight * rhs[gid]) / 6.0f;
}

// ---------------------------------------------------------------------------
// Kernel: Restriction (fine -> coarse)
// ---------------------------------------------------------------------------

kernel void restrict_kernel(
    device const float* fine     [[ buffer(0) ]],
    device float*       coarse   [[ buffer(1) ]],
    constant MgParams&  params   [[ buffer(2) ]],
    uint                gid      [[ thread_position_in_grid ]])
{
    int N_fine   = (int)params.N;
    int N_coarse = (int)params.N_coarse;
    int total_coarse = N_coarse * N_coarse * N_coarse;
    if ((int)gid >= total_coarse) return;

    int cx = (int)gid % N_coarse;
    int cy = ((int)gid / N_coarse) % N_coarse;
    int cz = (int)gid / (N_coarse * N_coarse);

    int fx = cx * 2;
    int fy = cy * 2;
    int fz = cz * 2;

    float sum = 0.0f;
    for (int dz = 0; dz < 2; ++dz)
        for (int dy = 0; dy < 2; ++dy)
            for (int dx = 0; dx < 2; ++dx)
                sum += fine[idx3d(fx+dx, fy+dy, fz+dz, N_fine)];

    coarse[gid] = sum * 0.125f;
}

// ---------------------------------------------------------------------------
// Kernel: Prolongation (coarse -> fine, additive)
// ---------------------------------------------------------------------------

kernel void prolong_kernel(
    device const float* coarse   [[ buffer(0) ]],
    device float*       fine     [[ buffer(1) ]],
    constant MgParams&  params   [[ buffer(2) ]],
    uint                gid      [[ thread_position_in_grid ]])
{
    int N_fine   = (int)params.N;
    int N_coarse = (int)params.N_coarse;
    int total_fine = N_fine * N_fine * N_fine;
    if ((int)gid >= total_fine) return;

    int fx = (int)gid % N_fine;
    int fy = ((int)gid / N_fine) % N_fine;
    int fz = (int)gid / (N_fine * N_fine);

    int cx = min(fx / 2, N_coarse - 1);
    int cy = min(fy / 2, N_coarse - 1);
    int cz = min(fz / 2, N_coarse - 1);

    fine[gid] += coarse[idx3d(cx, cy, cz, N_coarse)];
}

// ---------------------------------------------------------------------------
// Kernel: Compute residual r = rhs - A*u
// ---------------------------------------------------------------------------

kernel void residual_kernel(
    device const float* u       [[ buffer(0) ]],
    device const float* rhs     [[ buffer(1) ]],
    device float*       r       [[ buffer(2) ]],
    constant MgParams&  params  [[ buffer(3) ]],
    uint                gid     [[ thread_position_in_grid ]])
{
    int N = (int)params.N;
    int total = N * N * N;
    if ((int)gid >= total) return;

    int x = (int)gid % N;
    int y = ((int)gid / N) % N;
    int z = (int)gid / (N * N);

    if (x == 0 || x == N-1 || y == 0 || y == N-1 || z == 0 || z == N-1) {
        r[gid] = 0.0f;
        return;
    }

    float laplacian = -6.0f * u[gid]
                    + u[idx3d(x-1,y,z,N)] + u[idx3d(x+1,y,z,N)]
                    + u[idx3d(x,y-1,z,N)] + u[idx3d(x,y+1,z,N)]
                    + u[idx3d(x,y,z-1,N)] + u[idx3d(x,y,z+1,N)];

    r[gid] = rhs[gid] - laplacian;
}

// ---------------------------------------------------------------------------
// Kernel: Norm squared (threadgroup reduction)
// ---------------------------------------------------------------------------

kernel void norm_sq_kernel(
    device const float* v          [[ buffer(0) ]],
    device float*       partial    [[ buffer(1) ]],
    constant MgParams&  params     [[ buffer(2) ]],
    uint                gid        [[ thread_position_in_grid ]],
    uint                lid        [[ thread_position_in_threadgroup ]],
    uint                gid_group  [[ threadgroup_position_in_grid ]],
    uint                tg_size    [[ threads_per_threadgroup ]])
{
    threadgroup float sdata[256];

    int N_total = (int)params.N;  // repurposed: total elements
    float val = 0.0f;
    if ((int)gid < N_total) val = v[gid] * v[gid];
    sdata[lid] = val;
    threadgroup_barrier(mem_flags::mem_threadgroup);

    for (uint s = tg_size / 2; s > 0; s >>= 1) {
        if (lid < s) sdata[lid] += sdata[lid + s];
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (lid == 0) partial[gid_group] = sdata[0];
}

// ---------------------------------------------------------------------------
// Kernel: Zero-fill a buffer
// ---------------------------------------------------------------------------

kernel void zero_kernel(
    device float*       buf     [[ buffer(0) ]],
    constant MgParams&  params  [[ buffer(1) ]],
    uint                gid     [[ thread_position_in_grid ]])
{
    if ((int)gid < (int)params.N) buf[gid] = 0.0f;
}
