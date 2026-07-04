/*
 * rodinia_srad.metal — Metal compute shader for the Rodinia SRAD benchmark.
 *
 * Speckle-Reducing Anisotropic Diffusion: two-kernel per iteration.
 *
 * Kernel 1 (srad1_kernel): Compute directional gradients dN/dS/dW/dE and
 *   the Perona-Malik diffusion coefficient c for each pixel.
 *
 * Kernel 2 (srad2_kernel): Update the image J using the diffusion coefficients.
 *
 * Buffer layout (srad1_kernel):
 *   buffer(0): J     [rows × cols] float — input image (read)
 *   buffer(1): dN    [rows × cols] float — north gradient (write)
 *   buffer(2): dS    [rows × cols] float — south gradient (write)
 *   buffer(3): dW    [rows × cols] float — west  gradient (write)
 *   buffer(4): dE    [rows × cols] float — east  gradient (write)
 *   buffer(5): c     [rows × cols] float — diffusion coefficient (write)
 *   buffer(6): params { rows, cols, _, _ } uint4
 *   buffer(7): floatParams { q0sqr, lambda, _, _ } float4
 *
 * Buffer layout (srad2_kernel):
 *   buffer(0): J     [rows × cols] float — image (read+write)
 *   buffer(1): dN    [rows × cols] float — north gradient (read)
 *   buffer(2): dS    [rows × cols] float — south gradient (read)
 *   buffer(3): dW    [rows × cols] float — west  gradient (read)
 *   buffer(4): dE    [rows × cols] float — east  gradient (read)
 *   buffer(5): c     [rows × cols] float — diffusion coefficient (read)
 *   buffer(6): params { rows, cols, _, _ } uint4
 *   buffer(7): floatParams { q0sqr, lambda, _, _ } float4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Phase 1: Compute gradients and diffusion coefficient
// ---------------------------------------------------------------------------
kernel void srad1_kernel(
    device const float*  J      [[ buffer(0) ]],
    device       float*  dN     [[ buffer(1) ]],
    device       float*  dS     [[ buffer(2) ]],
    device       float*  dW     [[ buffer(3) ]],
    device       float*  dE     [[ buffer(4) ]],
    device       float*  c      [[ buffer(5) ]],
    constant     uint4&  dims   [[ buffer(6) ]],   // {rows, cols, 0, 0}
    constant     float4& fparams[[ buffer(7) ]],   // {q0sqr, lambda, 0, 0}
    uint2 gid [[ thread_position_in_grid ]])
{
    uint cols   = dims.y;
    uint rows   = dims.x;
    uint col    = gid.x;
    uint row    = gid.y;

    if (col >= cols || row >= rows) return;

    float q0sqr = fparams.x;
    uint  idx   = row * cols + col;

    // Clamped neighbor indices
    uint iN = (row > 0)        ? (row - 1) : 0;
    uint iS = (row < rows - 1) ? (row + 1) : (rows - 1);
    uint jW = (col > 0)        ? (col - 1) : 0;
    uint jE = (col < cols - 1) ? (col + 1) : (cols - 1);

    float Jc = J[idx];

    float dn = J[iN * cols + col] - Jc;
    float ds = J[iS * cols + col] - Jc;
    float dw = J[row * cols + jW] - Jc;
    float de = J[row * cols + jE] - Jc;

    dN[idx] = dn;
    dS[idx] = ds;
    dW[idx] = dw;
    dE[idx] = de;

    float G2   = (dn*dn + ds*ds + dw*dw + de*de) / (Jc * Jc);
    float L    = (dn + ds + dw + de) / Jc;
    float num  = (0.5f * G2) - ((1.0f / 16.0f) * (L * L));
    float den  = 1.0f + (0.25f * L);
    float qsqr = num / (den * den);

    // Perona-Malik diffusion coefficient
    den = (qsqr - q0sqr) / (q0sqr * (1.0f + q0sqr));
    float ci = 1.0f / (1.0f + den);
    ci = clamp(ci, 0.0f, 1.0f);
    c[idx] = ci;
}

// ---------------------------------------------------------------------------
// Phase 2: Update image using diffusion coefficients
// ---------------------------------------------------------------------------
kernel void srad2_kernel(
    device       float*  J      [[ buffer(0) ]],
    device const float*  dN     [[ buffer(1) ]],
    device const float*  dS     [[ buffer(2) ]],
    device const float*  dW     [[ buffer(3) ]],
    device const float*  dE     [[ buffer(4) ]],
    device const float*  c      [[ buffer(5) ]],
    constant     uint4&  dims   [[ buffer(6) ]],   // {rows, cols, 0, 0}
    constant     float4& fparams[[ buffer(7) ]],   // {q0sqr, lambda, 0, 0}
    uint2 gid [[ thread_position_in_grid ]])
{
    uint cols   = dims.y;
    uint rows   = dims.x;
    uint col    = gid.x;
    uint row    = gid.y;

    if (col >= cols || row >= rows) return;

    float lambda = fparams.y;
    uint  idx    = row * cols + col;

    // South and East neighbor indices (clamped)
    uint iS = (row < rows - 1) ? (row + 1) : (rows - 1);
    uint jE = (col < cols - 1) ? (col + 1) : (cols - 1);

    float cN = c[idx];
    float cS = c[iS * cols + col];
    float cW = c[idx];
    float cE = c[row * cols + jE];

    float D = cN * dN[idx] + cS * dS[idx] + cW * dW[idx] + cE * dE[idx];

    J[idx] += 0.25f * lambda * D;
}
