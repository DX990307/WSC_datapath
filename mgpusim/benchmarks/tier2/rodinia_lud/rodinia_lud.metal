/*
 * rodinia_lud.metal — Metal compute shaders for the Rodinia LUD benchmark.
 *
 * Blocked LU decomposition (no pivoting) of a dense NxN matrix.
 * Three kernels mirror the HIP implementation:
 *   lud_diagonal_kernel   — factor the current diagonal 16×16 block
 *   lud_perimeter_kernel  — update adjacent row/column perimeter blocks
 *   lud_internal_kernel   — Schur-complement update for interior blocks
 *
 * Buffer layout for all kernels:
 *   buffer(0): float*   — NxN matrix (row-major, in-place LU result)
 *   buffer(1): LudParams — { n, offset, nhalf }
 *
 * lud_perimeter_kernel uses p.nhalf to split threadgroups:
 *   threadgroup index < nhalf  → row panel (right of diagonal)
 *   threadgroup index >= nhalf → column panel (below diagonal)
 */

#include <metal_stdlib>
using namespace metal;

#define BSIZE 16

struct LudParams {
    int n;       // matrix dimension
    int offset;  // current diagonal block index
    int nhalf;   // num_blocks - offset - 1  (used by perimeter kernel)
};

// ---------------------------------------------------------------------------
// Diagonal kernel
// Dispatch: threadgroups=(1,1,1), threadsPerThreadgroup=(BSIZE,BSIZE,1)
// ---------------------------------------------------------------------------
kernel void lud_diagonal_kernel(
    device float*         a [[ buffer(0) ]],
    constant LudParams&   p [[ buffer(1) ]],
    uint2 tid              [[ thread_position_in_threadgroup ]])
{
    threadgroup float s[BSIZE][BSIZE];
    int tx = (int)tid.x, ty = (int)tid.y;
    int n = p.n, off = p.offset;

    s[ty][tx] = a[(off * BSIZE + ty) * n + (off * BSIZE + tx)];
    threadgroup_barrier(mem_flags::mem_threadgroup);

    for (int k = 0; k < BSIZE - 1; k++) {
        if (ty > k && tx == k)
            s[ty][k] /= s[k][k];
        threadgroup_barrier(mem_flags::mem_threadgroup);
        if (ty > k && tx > k)
            s[ty][tx] -= s[ty][k] * s[k][tx];
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    a[(off * BSIZE + ty) * n + (off * BSIZE + tx)] = s[ty][tx];
}

// ---------------------------------------------------------------------------
// Perimeter kernel
// Dispatch: threadgroups=(2*nhalf,1,1), threadsPerThreadgroup=(BSIZE,BSIZE,1)
// ---------------------------------------------------------------------------
kernel void lud_perimeter_kernel(
    device float*         a    [[ buffer(0) ]],
    constant LudParams&   p    [[ buffer(1) ]],
    uint2 tgid                 [[ threadgroup_position_in_grid ]],
    uint2 tid                  [[ thread_position_in_threadgroup ]])
{
    threadgroup float dia [BSIZE][BSIZE];
    threadgroup float peri[BSIZE][BSIZE];

    int tx = (int)tid.x, ty = (int)tid.y;
    int n = p.n, off = p.offset, nhalf = p.nhalf;

    // is_row is uniform across the entire threadgroup (determined by tgid.x)
    bool is_row = ((int)tgid.x < nhalf);
    int  idx    = is_row ? (int)tgid.x : (int)tgid.x - nhalf;
    int  blk    = off + idx + 1;

    dia[ty][tx] = a[(off * BSIZE + ty) * n + (off * BSIZE + tx)];
    if (is_row)
        peri[ty][tx] = a[(off  * BSIZE + ty) * n + (blk * BSIZE + tx)];
    else
        peri[ty][tx] = a[(blk  * BSIZE + ty) * n + (off * BSIZE + tx)];
    threadgroup_barrier(mem_flags::mem_threadgroup);

    if (is_row) {
        // Forward substitution: unit-lower-triangular L * peri = peri
        for (int k = 0; k < BSIZE - 1; k++) {
            if (ty > k)
                peri[ty][tx] -= dia[ty][k] * peri[k][tx];
            threadgroup_barrier(mem_flags::mem_threadgroup);
        }
        a[(off * BSIZE + ty) * n + (blk * BSIZE + tx)] = peri[ty][tx];
    } else {
        // Back substitution: peri * U = peri  → compute L columns
        for (int k = 0; k < BSIZE; k++) {
            if (tx == k)
                peri[ty][k] /= dia[k][k];
            threadgroup_barrier(mem_flags::mem_threadgroup);
            if (tx > k)
                peri[ty][tx] -= peri[ty][k] * dia[k][tx];
            threadgroup_barrier(mem_flags::mem_threadgroup);
        }
        a[(blk * BSIZE + ty) * n + (off * BSIZE + tx)] = peri[ty][tx];
    }
}

// ---------------------------------------------------------------------------
// Internal kernel
// Dispatch: threadgroups=(intern,intern,1), threadsPerThreadgroup=(BSIZE,BSIZE,1)
//   where intern = num_blocks - offset - 1
// ---------------------------------------------------------------------------
kernel void lud_internal_kernel(
    device float*         a    [[ buffer(0) ]],
    constant LudParams&   p    [[ buffer(1) ]],
    uint2 tgid                 [[ threadgroup_position_in_grid ]],
    uint2 tid                  [[ thread_position_in_threadgroup ]])
{
    threadgroup float peri_row[BSIZE][BSIZE];  // U[offset, col_blk]
    threadgroup float peri_col[BSIZE][BSIZE];  // L[row_blk, offset]

    int tx = (int)tid.x, ty = (int)tid.y;
    int n = p.n, off = p.offset;
    int col_blk = off + (int)tgid.x + 1;
    int row_blk = off + (int)tgid.y + 1;

    peri_row[ty][tx] = a[(off     * BSIZE + ty) * n + (col_blk * BSIZE + tx)];
    peri_col[ty][tx] = a[(row_blk * BSIZE + ty) * n + (off     * BSIZE + tx)];
    threadgroup_barrier(mem_flags::mem_threadgroup);

    float sum = 0.0f;
    for (int k = 0; k < BSIZE; k++)
        sum += peri_col[ty][k] * peri_row[k][tx];
    a[(row_blk * BSIZE + ty) * n + (col_blk * BSIZE + tx)] -= sum;
}
