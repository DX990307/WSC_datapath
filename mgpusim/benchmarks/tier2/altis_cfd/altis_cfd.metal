/*
 * altis_cfd.metal — Metal compute shaders for CFD Euler solver.
 *
 * Solves compressible Euler equations on an unstructured mesh using a
 * finite-volume method with Rusanov (local Lax-Friedrichs) flux scheme.
 *
 * Kernel 1 (compute_flux_kernel): Computes flux at each cell from neighbors.
 * Kernel 2 (rk_update_kernel): Runge-Kutta time update.
 */

#include <metal_stdlib>
using namespace metal;

#define GAMMA    1.4f
#define GAMMA_M1 0.4f
#define NUM_NEIGHBORS 4

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

static inline float compute_pressure(float rho, float mx, float my,
                                      float mz, float e) {
    float ke = 0.5f * (mx * mx + my * my + mz * mz) / max(rho, 1e-10f);
    return GAMMA_M1 * (e - ke);
}

static inline float compute_speed_of_sound(float rho, float p) {
    return sqrt(max(GAMMA * p / max(rho, 1e-10f), 1e-10f));
}

// ---------------------------------------------------------------------------
// CFD parameters struct
// ---------------------------------------------------------------------------

struct CfdParams {
    uint N;
    float dt;
    float rk_coeff;
};

// ---------------------------------------------------------------------------
// Kernel: compute flux contributions for each cell
// ---------------------------------------------------------------------------

kernel void compute_flux_kernel(
    device const float*  rho         [[ buffer(0)  ]],
    device const float*  mx          [[ buffer(1)  ]],
    device const float*  my          [[ buffer(2)  ]],
    device const float*  mz          [[ buffer(3)  ]],
    device const float*  energy      [[ buffer(4)  ]],
    device const int*    neighbors   [[ buffer(5)  ]],
    device const float*  normals     [[ buffer(6)  ]],
    device const float*  areas       [[ buffer(7)  ]],
    device float*        flux_rho    [[ buffer(8)  ]],
    device float*        flux_mx     [[ buffer(9)  ]],
    device float*        flux_my     [[ buffer(10) ]],
    device float*        flux_mz     [[ buffer(11) ]],
    device float*        flux_energy [[ buffer(12) ]],
    constant CfdParams&  params      [[ buffer(13) ]],
    uint                 idx         [[ thread_position_in_grid ]])
{
    if (idx >= params.N) return;

    float rho_i = rho[idx];
    float mx_i  = mx[idx];
    float my_i  = my[idx];
    float mz_i  = mz[idx];
    float e_i   = energy[idx];
    float p_i   = compute_pressure(rho_i, mx_i, my_i, mz_i, e_i);
    float a_i   = compute_speed_of_sound(rho_i, p_i);

    float inv_rho_i = 1.0f / max(rho_i, 1e-10f);
    float vx_i = mx_i * inv_rho_i;
    float vy_i = my_i * inv_rho_i;
    float vz_i = mz_i * inv_rho_i;

    float f_rho = 0.0f, f_mx = 0.0f, f_my = 0.0f, f_mz = 0.0f, f_e = 0.0f;

    for (int f = 0; f < NUM_NEIGHBORS; ++f) {
        int j = neighbors[idx * NUM_NEIGHBORS + f];
        int nbase = (idx * NUM_NEIGHBORS + f) * 3;
        float nx_f = normals[nbase + 0];
        float ny_f = normals[nbase + 1];
        float nz_f = normals[nbase + 2];
        float area = areas[idx * NUM_NEIGHBORS + f];

        float rho_j = rho[j];
        float mx_j  = mx[j];
        float my_j  = my[j];
        float mz_j  = mz[j];
        float e_j   = energy[j];
        float p_j   = compute_pressure(rho_j, mx_j, my_j, mz_j, e_j);
        float a_j   = compute_speed_of_sound(rho_j, p_j);

        float inv_rho_j = 1.0f / max(rho_j, 1e-10f);
        float vx_j = mx_j * inv_rho_j;
        float vy_j = my_j * inv_rho_j;
        float vz_j = mz_j * inv_rho_j;

        float vn_i = vx_i * nx_f + vy_i * ny_f + vz_i * nz_f;
        float vn_j = vx_j * nx_f + vy_j * ny_f + vz_j * nz_f;

        float f_rho_i = rho_i * vn_i;
        float f_rho_j = rho_j * vn_j;
        float f_mx_i = mx_i * vn_i + p_i * nx_f;
        float f_mx_j = mx_j * vn_j + p_j * nx_f;
        float f_my_i = my_i * vn_i + p_i * ny_f;
        float f_my_j = my_j * vn_j + p_j * ny_f;
        float f_mz_i = mz_i * vn_i + p_i * nz_f;
        float f_mz_j = mz_j * vn_j + p_j * nz_f;
        float f_e_i = (e_i + p_i) * vn_i;
        float f_e_j = (e_j + p_j) * vn_j;

        float lambda = max(abs(vn_i) + a_i, abs(vn_j) + a_j);

        f_rho += area * (0.5f * (f_rho_i + f_rho_j) - 0.5f * lambda * (rho_j - rho_i));
        f_mx  += area * (0.5f * (f_mx_i  + f_mx_j)  - 0.5f * lambda * (mx_j  - mx_i));
        f_my  += area * (0.5f * (f_my_i  + f_my_j)  - 0.5f * lambda * (my_j  - my_i));
        f_mz  += area * (0.5f * (f_mz_i  + f_mz_j)  - 0.5f * lambda * (mz_j  - mz_i));
        f_e   += area * (0.5f * (f_e_i   + f_e_j)   - 0.5f * lambda * (e_j   - e_i));
    }

    flux_rho[idx]    = f_rho;
    flux_mx[idx]     = f_mx;
    flux_my[idx]     = f_my;
    flux_mz[idx]     = f_mz;
    flux_energy[idx] = f_e;
}

// ---------------------------------------------------------------------------
// Kernel: Runge-Kutta time update
// ---------------------------------------------------------------------------

kernel void rk_update_kernel(
    device float*        rho         [[ buffer(0) ]],
    device float*        mx          [[ buffer(1) ]],
    device float*        my          [[ buffer(2) ]],
    device float*        mz          [[ buffer(3) ]],
    device float*        energy      [[ buffer(4) ]],
    device const float*  rho_old     [[ buffer(5) ]],
    device const float*  mx_old      [[ buffer(6) ]],
    device const float*  my_old      [[ buffer(7) ]],
    device const float*  mz_old      [[ buffer(8) ]],
    device const float*  energy_old  [[ buffer(9) ]],
    device const float*  flux_rho    [[ buffer(10) ]],
    device const float*  flux_mx     [[ buffer(11) ]],
    device const float*  flux_my     [[ buffer(12) ]],
    device const float*  flux_mz     [[ buffer(13) ]],
    device const float*  flux_energy [[ buffer(14) ]],
    device const float*  volumes     [[ buffer(15) ]],
    constant CfdParams&  params      [[ buffer(16) ]],
    uint                 idx         [[ thread_position_in_grid ]])
{
    if (idx >= params.N) return;

    float inv_vol = 1.0f / volumes[idx];
    float factor  = params.dt * inv_vol;
    float rk = params.rk_coeff;

    float r = rho_old[idx]    - factor * flux_rho[idx];
    float x = mx_old[idx]     - factor * flux_mx[idx];
    float y = my_old[idx]     - factor * flux_my[idx];
    float z = mz_old[idx]     - factor * flux_mz[idx];
    float e = energy_old[idx] - factor * flux_energy[idx];

    rho[idx]    = rk * rho_old[idx]    + (1.0f - rk) * r;
    mx[idx]     = rk * mx_old[idx]     + (1.0f - rk) * x;
    my[idx]     = rk * my_old[idx]     + (1.0f - rk) * y;
    mz[idx]     = rk * mz_old[idx]     + (1.0f - rk) * z;
    energy[idx] = rk * energy_old[idx] + (1.0f - rk) * e;
}
