/*
 * parboil_lbm.metal — Metal compute shader for LBM D3Q19 benchmark.
 *
 * Implements the Lattice Boltzmann Method with D3Q19 velocity set,
 * BGK collision operator, and bounce-back boundary conditions.
 * Each thread handles one lattice node.
 */

#include <metal_stdlib>
using namespace metal;

#define Q 19

// ---------------------------------------------------------------------------
// Parameters struct
// ---------------------------------------------------------------------------

struct LbmParams {
    uint  Nx;
    uint  Ny;
    uint  Nz;
    float omega;
};

// ---------------------------------------------------------------------------
// D3Q19 velocity set (compile-time constants)
// ---------------------------------------------------------------------------

constant int c_ex[Q] = { 0, 1,-1, 0, 0, 0, 0, 1,-1, 1,-1, 1,-1, 1,-1, 0, 0, 0, 0};
constant int c_ey[Q] = { 0, 0, 0, 1,-1, 0, 0, 1, 1,-1,-1, 0, 0, 0, 0, 1,-1, 1,-1};
constant int c_ez[Q] = { 0, 0, 0, 0, 0, 1,-1, 0, 0, 0, 0, 1, 1,-1,-1, 1, 1,-1,-1};

constant float c_w[Q] = {
    1.0f/3.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f
};

constant int c_opp[Q] = {0, 2,1, 4,3, 6,5, 10,9,8,7, 14,13,12,11, 18,17,16,15};

// ---------------------------------------------------------------------------
// Kernel: Fused collide-stream with BGK collision and bounce-back
// ---------------------------------------------------------------------------

kernel void lbm_collide_stream_kernel(
    device const float*  f_src  [[ buffer(0) ]],
    device float*        f_dst  [[ buffer(1) ]],
    constant LbmParams&  params [[ buffer(2) ]],
    uint                 gid    [[ thread_position_in_grid ]])
{
    uint Nx = params.Nx;
    uint Ny = params.Ny;
    uint Nz = params.Nz;
    uint N  = Nx * Ny * Nz;
    float omega = params.omega;

    if (gid >= N) return;

    uint iz = gid / (Nx * Ny);
    uint iy = (gid / Nx) % Ny;
    uint ix = gid % Nx;

    bool is_boundary = (ix == 0 || ix == Nx-1 ||
                        iy == 0 || iy == Ny-1 ||
                        iz == 0 || iz == Nz-1);

    // Load distributions
    float f[Q];
    for (int q = 0; q < Q; ++q) {
        f[q] = f_src[q * N + gid];
    }

    // Compute macroscopic quantities
    float rho = 0.0f;
    float ux = 0.0f, uy = 0.0f, uz = 0.0f;
    for (int q = 0; q < Q; ++q) {
        rho += f[q];
        ux += f[q] * float(c_ex[q]);
        uy += f[q] * float(c_ey[q]);
        uz += f[q] * float(c_ez[q]);
    }
    float inv_rho = 1.0f / max(rho, 1e-10f);
    ux *= inv_rho;
    uy *= inv_rho;
    uz *= inv_rho;

    // BGK collision
    float u2 = ux * ux + uy * uy + uz * uz;
    float f_post[Q];
    for (int q = 0; q < Q; ++q) {
        float eu = float(c_ex[q]) * ux + float(c_ey[q]) * uy + float(c_ez[q]) * uz;
        float f_eq = c_w[q] * rho * (1.0f + 3.0f * eu + 4.5f * eu * eu - 1.5f * u2);
        f_post[q] = f[q] + omega * (f_eq - f[q]);
    }

    // Stream
    for (int q = 0; q < Q; ++q) {
        int nx = int(ix) + c_ex[q];
        int ny = int(iy) + c_ey[q];
        int nz = int(iz) + c_ez[q];

        if (is_boundary) {
            f_dst[c_opp[q] * N + gid] = f_post[q];
        } else if (nx >= 0 && nx < int(Nx) && ny >= 0 && ny < int(Ny) && nz >= 0 && nz < int(Nz)) {
            uint nidx = uint(nz) * Nx * Ny + uint(ny) * Nx + uint(nx);
            f_dst[q * N + nidx] = f_post[q];
        } else {
            f_dst[c_opp[q] * N + gid] = f_post[q];
        }
    }
}
