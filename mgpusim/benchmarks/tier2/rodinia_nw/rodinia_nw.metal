/*
 * rodinia_nw.metal — Metal compute shader for the Rodinia Needleman-Wunsch benchmark.
 *
 * Implements block-based anti-diagonal wavefront DP for sequence alignment.
 * The scoring matrix is (padded_len+1) x (padded_len+1); border row/column
 * are initialized on the CPU.  Two kernel functions cover the upper-left and
 * lower-right triangles of the block dependency graph respectively.
 *
 * Buffer layout (both kernels):
 *   buffer(0): d_seq    [(padded_len+1)] int — merged row+col sequence
 *   buffer(1): d_matrix [(rows * cols)]  int — DP matrix (read + write)
 *   buffer(2): params   NWParams struct
 *
 * Shared-memory equivalent: threadgroup int[] of size
 *   (block_size+1)*(block_size+1) + block_size  elements.
 *
 * NW scoring: match=+1, mismatch=-1, gap=-penalty.
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Parameters passed from host
// ---------------------------------------------------------------------------
struct NWParams {
    int cols;        // full matrix width  (padded_len + 1)
    int penalty;     // gap penalty (positive integer)
    int block_idx;   // which anti-diagonal of blocks is being processed
    int num_blocks;  // total blocks per dimension
    int block_size;  // threads per block (tile dimension)
    int phase;       // 0 = upper-left kernel, 1 = lower-right kernel
    int pad0;        // padding to 32-byte alignment
    int pad1;
};

// ---------------------------------------------------------------------------
// Scoring function
// ---------------------------------------------------------------------------
static inline int nw_score(int a, int b) {
    return (a == b) ? 1 : -1;
}

// ---------------------------------------------------------------------------
// Kernel 1 — upper-left triangle
//
// Each threadgroup handles one block-tile on the current block-diagonal.
// threadgroup_position_in_grid.x = bx  (0-indexed block on the diagonal)
// thread_position_in_threadgroup.x = tx (0-indexed thread within the tile)
// ---------------------------------------------------------------------------
kernel void nw_kernel1(
    device const int*    d_seq    [[ buffer(0) ]],
    device       int*    d_matrix [[ buffer(1) ]],
    constant     NWParams& params [[ buffer(2) ]],
    threadgroup  int*    shared   [[ threadgroup(0) ]],
    uint tg_pos [[ threadgroup_position_in_grid ]],
    uint  tx     [[ thread_position_in_threadgroup ]])
{
    int bx         = (int)tg_pos;
    int block_idx  = params.block_idx;
    int block_size = params.block_size;
    int cols       = params.cols;
    int penalty    = params.penalty;

    if (bx > block_idx) return;

    int b_row = block_idx - bx;
    int b_col = bx;

    int begin_row = b_row * block_size + 1;
    int begin_col = b_col * block_size + 1;

    // Shared memory layout:
    //   temp[0 .. (block_size+1)^2 - 1]
    //   ref_shared[0 .. block_size-1]  (starts after temp)
    threadgroup int* temp       = shared;
    threadgroup int* ref_shared = shared + (block_size + 1) * (block_size + 1);

    // Load column sequence slice into shared memory
    if ((int)tx < block_size) {
        ref_shared[tx] = d_seq[begin_col + (int)tx];
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Thread 0 loads border column and border row
    if (tx == 0) {
        for (int i = 0; i <= block_size; i++) {
            temp[i * (block_size + 1)] =
                d_matrix[(begin_row + i - 1) * cols + (begin_col - 1)];
        }
        for (int j = 0; j <= block_size; j++) {
            temp[j] = d_matrix[(begin_row - 1) * cols + (begin_col + j - 1)];
        }
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Anti-diagonal sweep within the tile
    for (int diag = 0; diag < 2 * block_size - 1; diag++) {
        int i_t = ((int)tx <= diag && (int)tx < block_size && (diag - (int)tx) < block_size)
                      ? (int)tx + 1 : -1;
        int j_t = (i_t > 0) ? diag - (int)tx + 1 : -1;

        if (i_t > 0 && j_t > 0 && i_t <= block_size && j_t <= block_size) {
            int idx_diag   = i_t * (block_size + 1) + j_t;
            int idx_up     = (i_t - 1) * (block_size + 1) + j_t;
            int idx_left   = i_t * (block_size + 1) + (j_t - 1);
            int idx_upleft = (i_t - 1) * (block_size + 1) + (j_t - 1);

            int match = temp[idx_upleft] +
                        nw_score(d_seq[begin_row + i_t - 1], ref_shared[j_t - 1]);
            int del   = temp[idx_up]   - penalty;
            int ins   = temp[idx_left] - penalty;

            int val = match;
            if (del > val) val = del;
            if (ins > val) val = ins;
            temp[idx_diag] = val;
        }
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    // Write tile back to global matrix
    if ((int)tx < block_size) {
        for (int i = 1; i <= block_size; i++) {
            d_matrix[(begin_row + i - 1) * cols + (begin_col + (int)tx)] =
                temp[i * (block_size + 1) + (int)tx + 1];
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel 2 — lower-right triangle
// ---------------------------------------------------------------------------
kernel void nw_kernel2(
    device const int*    d_seq    [[ buffer(0) ]],
    device       int*    d_matrix [[ buffer(1) ]],
    constant     NWParams& params [[ buffer(2) ]],
    threadgroup  int*    shared   [[ threadgroup(0) ]],
    uint tg_pos [[ threadgroup_position_in_grid ]],
    uint  tx     [[ thread_position_in_threadgroup ]])
{
    int bx         = (int)tg_pos;
    int block_idx  = params.block_idx;
    int num_blocks = params.num_blocks;
    int block_size = params.block_size;
    int cols       = params.cols;
    int penalty    = params.penalty;

    int blocks_on_diag = num_blocks - 1 - block_idx;
    if (bx >= blocks_on_diag) return;

    int b_row = num_blocks - 1 - bx;
    int b_col = block_idx + 1 + bx;

    int begin_row = b_row * block_size + 1;
    int begin_col = b_col * block_size + 1;

    threadgroup int* temp       = shared;
    threadgroup int* ref_shared = shared + (block_size + 1) * (block_size + 1);

    if ((int)tx < block_size) {
        ref_shared[tx] = d_seq[begin_col + (int)tx];
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    if (tx == 0) {
        for (int i = 0; i <= block_size; i++) {
            temp[i * (block_size + 1)] =
                d_matrix[(begin_row + i - 1) * cols + (begin_col - 1)];
        }
        for (int j = 0; j <= block_size; j++) {
            temp[j] = d_matrix[(begin_row - 1) * cols + (begin_col + j - 1)];
        }
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    for (int diag = 0; diag < 2 * block_size - 1; diag++) {
        int i_t = ((int)tx <= diag && (int)tx < block_size && (diag - (int)tx) < block_size)
                      ? (int)tx + 1 : -1;
        int j_t = (i_t > 0) ? diag - (int)tx + 1 : -1;

        if (i_t > 0 && j_t > 0 && i_t <= block_size && j_t <= block_size) {
            int idx_diag   = i_t * (block_size + 1) + j_t;
            int idx_up     = (i_t - 1) * (block_size + 1) + j_t;
            int idx_left   = i_t * (block_size + 1) + (j_t - 1);
            int idx_upleft = (i_t - 1) * (block_size + 1) + (j_t - 1);

            int match = temp[idx_upleft] +
                        nw_score(d_seq[begin_row + i_t - 1], ref_shared[j_t - 1]);
            int del   = temp[idx_up]   - penalty;
            int ins   = temp[idx_left] - penalty;

            int val = match;
            if (del > val) val = del;
            if (ins > val) val = ins;
            temp[idx_diag] = val;
        }
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if ((int)tx < block_size) {
        for (int i = 1; i <= block_size; i++) {
            d_matrix[(begin_row + i - 1) * cols + (begin_col + (int)tx)] =
                temp[i * (block_size + 1) + (int)tx + 1];
        }
    }
}
