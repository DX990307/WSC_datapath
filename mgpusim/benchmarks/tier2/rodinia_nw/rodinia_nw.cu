// rodinia_nw.cu — Rodinia Needleman-Wunsch benchmark (CUDA, self-contained)
//
// Dynamic programming sequence alignment using the Needleman-Wunsch algorithm.
// The NxN scoring matrix is filled using block-based anti-diagonal wavefront
// parallelism with shared memory. Two kernels cover the upper-left and
// lower-right triangles of the block dependency graph.
//
// Native CUDA implementation.
//
// Usage:
//   ./rodinia_nw [--sequence_length N] [--block_size B] [--penalty P]
//
//   --sequence_length N  Sequence length (scoring matrix is NxN) (default: 2048)
//   --block_size B       Block size (threads per block)          (default: 16)
//   --penalty P          Gap penalty                             (default: 2)
//
// Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
// Output (stderr): GCUPS and effective GB/s

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined bench_common_cuda.h utilities
// ---------------------------------------------------------------------------

#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


static int parseIntParam(int argc, char** argv, const char* name, int defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defaultVal;
}


struct BenchmarkTimer {
    cudaEvent_t start, stop;
    BenchmarkTimer() {
        CUDA_CHECK(cudaEventCreate(&start));
        CUDA_CHECK(cudaEventCreate(&stop));
    }
    ~BenchmarkTimer() {
        (void)cudaEventDestroy(start);
        (void)cudaEventDestroy(stop);
    }
    void record_start(cudaStream_t stream = 0) {
        CUDA_CHECK(cudaEventRecord(start, stream));
    }
    void record_stop(cudaStream_t stream = 0) {
        CUDA_CHECK(cudaEventRecord(stop, stream));
        CUDA_CHECK(cudaEventSynchronize(stop));
    }
    float elapsed_ms() const {
        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, start, stop));
        return ms;
    }
};

template <typename Func>
static BenchResult runBenchmark(const char* kernel_name,
                                const char* problem_size,
                                int         iterations,
                                int         num_warmup,
                                Func        func) {
    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        func();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        func();
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / 1;

    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    BenchResult r;
    r.kernel_name  = kernel_name;
    r.problem_size = problem_size;
    r.iterations   = iterations;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;
    return r;
}

// ---------------------------------------------------------------------------
// NW scoring constants
// ---------------------------------------------------------------------------
#define MATCH_SCORE    1
#define MISMATCH_SCORE (-1)

__device__ static inline int nw_score(int a, int b) {
    return (a == b) ? MATCH_SCORE : MISMATCH_SCORE;
}

// ---------------------------------------------------------------------------
// Kernel 1: upper-left triangle of the block dependency graph
//
// For diagonal block_idx = 0..num_blocks-1, dispatches (block_idx+1) blocks.
// Block bx handles block position (row = block_idx - bx, col = bx).
// Each block uses shared memory to fill its (block_size x block_size) tile.
// ---------------------------------------------------------------------------
__global__ void nw_kernel1(int* __restrict__ d_seq,
                            int* __restrict__ d_matrix,
                            int cols,
                            int penalty,
                            int block_idx,
                            int block_size)
{
    int bx = blockIdx.x;
    int tx = threadIdx.x;

    // Filter out blocks beyond diagonal
    if (bx > block_idx) return;

    int b_row = block_idx - bx;
    int b_col = bx;

    // Global starting position (1-indexed within the matrix)
    int begin_row = b_row * block_size + 1;
    int begin_col = b_col * block_size + 1;

    // Shared memory layout:
    //   temp[0..(block_size+1)^2-1] : local DP tile (block_size+1 x block_size+1)
    //   ref_shared[0..block_size-1] : column sequence slice
    extern __shared__ int shared[];
    int* temp       = shared;
    int* ref_shared = &shared[(block_size + 1) * (block_size + 1)];

    // Load column sequence into shared memory
    if (tx < block_size) {
        ref_shared[tx] = d_seq[begin_col + tx];
    }
    __syncthreads();

    // Thread 0 loads the border row and column from global memory
    if (tx == 0) {
        // Left border column (from global matrix)
        for (int i = 0; i <= block_size; i++) {
            temp[i * (block_size + 1)] =
                d_matrix[(begin_row + i - 1) * cols + (begin_col - 1)];
        }
        // Top border row (from global matrix)
        for (int j = 0; j <= block_size; j++) {
            temp[j] = d_matrix[(begin_row - 1) * cols + (begin_col + j - 1)];
        }
    }
    __syncthreads();

    // Anti-diagonal sweep within the tile
    for (int diag = 0; diag < 2 * block_size - 1; diag++) {
        // Determine this thread's (i, j) within the tile [1..block_size]
        int i = (tx <= diag && tx < block_size && (diag - tx) < block_size)
                    ? tx + 1 : -1;
        int j = (i > 0) ? diag - tx + 1 : -1;

        if (i > 0 && j > 0 && i <= block_size && j <= block_size) {
            int idx_diag   = i * (block_size + 1) + j;
            int idx_up     = (i - 1) * (block_size + 1) + j;
            int idx_left   = i * (block_size + 1) + (j - 1);
            int idx_upleft = (i - 1) * (block_size + 1) + (j - 1);

            // Row sequence character is loaded directly from global memory
            int match = temp[idx_upleft] +
                        nw_score(d_seq[begin_row + i - 1], ref_shared[j - 1]);
            int del   = temp[idx_up]   - penalty;
            int ins   = temp[idx_left] - penalty;

            int val = match;
            if (del > val) val = del;
            if (ins > val) val = ins;
            temp[idx_diag] = val;
        }
        __syncthreads();
    }

    // Write computed tile back to global matrix
    if (tx < block_size) {
        for (int i = 1; i <= block_size; i++) {
            d_matrix[(begin_row + i - 1) * cols + (begin_col + tx)] =
                temp[i * (block_size + 1) + tx + 1];
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: lower-right triangle of the block dependency graph
//
// For diagonal block_idx = 0..num_blocks-2, dispatches (num_blocks-1-block_idx) blocks.
// Block bx handles block position (row = num_blocks-1-bx, col = block_idx+1+bx).
// ---------------------------------------------------------------------------
__global__ void nw_kernel2(int* __restrict__ d_seq,
                            int* __restrict__ d_matrix,
                            int cols,
                            int penalty,
                            int block_idx,
                            int num_blocks,
                            int block_size)
{
    int bx = blockIdx.x;
    int tx = threadIdx.x;

    int blocks_on_diag = num_blocks - 1 - block_idx;
    if (bx >= blocks_on_diag) return;

    int b_row = num_blocks - 1 - bx;
    int b_col = block_idx + 1 + bx;

    int begin_row = b_row * block_size + 1;
    int begin_col = b_col * block_size + 1;

    extern __shared__ int shared[];
    int* temp       = shared;
    int* ref_shared = &shared[(block_size + 1) * (block_size + 1)];

    if (tx < block_size) {
        ref_shared[tx] = d_seq[begin_col + tx];
    }
    __syncthreads();

    if (tx == 0) {
        for (int i = 0; i <= block_size; i++) {
            temp[i * (block_size + 1)] =
                d_matrix[(begin_row + i - 1) * cols + (begin_col - 1)];
        }
        for (int j = 0; j <= block_size; j++) {
            temp[j] = d_matrix[(begin_row - 1) * cols + (begin_col + j - 1)];
        }
    }
    __syncthreads();

    for (int diag = 0; diag < 2 * block_size - 1; diag++) {
        int i = (tx <= diag && tx < block_size && (diag - tx) < block_size)
                    ? tx + 1 : -1;
        int j = (i > 0) ? diag - tx + 1 : -1;

        if (i > 0 && j > 0 && i <= block_size && j <= block_size) {
            int idx_diag   = i * (block_size + 1) + j;
            int idx_up     = (i - 1) * (block_size + 1) + j;
            int idx_left   = i * (block_size + 1) + (j - 1);
            int idx_upleft = (i - 1) * (block_size + 1) + (j - 1);

            int match = temp[idx_upleft] +
                        nw_score(d_seq[begin_row + i - 1], ref_shared[j - 1]);
            int del   = temp[idx_up]   - penalty;
            int ins   = temp[idx_left] - penalty;

            int val = match;
            if (del > val) val = del;
            if (ins > val) val = ins;
            temp[idx_diag] = val;
        }
        __syncthreads();
    }

    if (tx < block_size) {
        for (int i = 1; i <= block_size; i++) {
            d_matrix[(begin_row + i - 1) * cols + (begin_col + tx)] =
                temp[i * (block_size + 1) + tx + 1];
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int seq_len    = 2048;
    int block_size = 16;
    int penalty    = 2;

    // Command-line fallback
    seq_len    = parseIntParam(argc, argv, "--sequence_length", seq_len);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    penalty    = parseIntParam(argc, argv, "--penalty", penalty);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_sequence_length");
    if (env_val) seq_len = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_penalty");
    if (env_val) penalty = atoi(env_val);
    int num_warmup = 0;

    // Round sequence length up to a multiple of block_size
    int num_blocks = (seq_len + block_size - 1) / block_size;
    int padded_len = num_blocks * block_size;

    // Matrix is (padded_len+1) x (padded_len+1); row 0 and col 0 are borders
    int cols = padded_len + 1;
    int rows = padded_len + 1;
    long matrix_elems = (long)rows * cols;

    // Allocate host arrays
    int* h_seq    = (int*)malloc((padded_len + 1) * sizeof(int));
    int* h_matrix = (int*)malloc(matrix_elems * sizeof(int));

    // Generate synthetic sequences (alphabet size 10, reproducible)
    srand(42);
    for (int i = 0; i <= padded_len; i++) {
        h_seq[i] = rand() % 10;
    }

    // Initialize DP border: row 0 and column 0
    for (int i = 0; i < rows; i++) {
        h_matrix[i * cols + 0] = -i * penalty;
    }
    for (int j = 0; j < cols; j++) {
        h_matrix[0 * cols + j] = -j * penalty;
    }

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Sequence length: %d  |  Block size: %d  |  Penalty: %d\n\n",
            seq_len, block_size, penalty);

    // Device allocations
    int *d_seq, *d_matrix;
    CUDA_CHECK(cudaMalloc(&d_seq,    (padded_len + 1) * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_matrix, matrix_elems * sizeof(int)));

    CUDA_CHECK(cudaMemcpy(d_seq, h_seq, (padded_len + 1) * sizeof(int), cudaMemcpyHostToDevice));

    // Shared memory per block: tile + ref slice
    size_t shared_mem = (size_t)(block_size + 1) * (block_size + 1) * sizeof(int)
                      + (size_t)block_size * sizeof(int);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%d", seq_len);

    BenchResult r = runBenchmark(
        "rodinia_nw", problemSize, 1, num_warmup, [&]() {
            // Reset matrix borders for each timed run
            CUDA_CHECK(cudaMemcpy(d_matrix, h_matrix,
                                matrix_elems * sizeof(int),
                                cudaMemcpyHostToDevice));

            // Phase 1: upper-left triangle
            for (int diag = 0; diag < num_blocks; diag++) {
                int n_blocks = diag + 1;
                nw_kernel1<<<dim3(n_blocks), dim3(block_size), shared_mem, 0>>>(d_seq, d_matrix, cols, penalty, diag, block_size);
            }

            // Phase 2: lower-right triangle
            for (int diag = 0; diag < num_blocks - 1; diag++) {
                int n_blocks = num_blocks - 1 - diag;
                nw_kernel2<<<dim3(n_blocks), dim3(block_size), shared_mem, 0>>>(d_seq, d_matrix, cols, penalty, diag, num_blocks, block_size);
            }

            CUDA_CHECK(cudaDeviceSynchronize());
        });

    // Performance metrics
    long cells     = (long)seq_len * seq_len;
    double gcups   = (double)cells / (r.avg_ms * 1e-3) / 1e9;
    double eff_bw  = (double)cells * 4 * sizeof(int) / (r.avg_ms * 1e-3) / 1e9;
    double total_ms = r.avg_ms;

    // JSON-lines kernel events (one per kernel name in params.json)
    printf("{\"type\":\"kernel\",\"name\":\"nw_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"sequence_length\":%d,\"block_size\":%d,\"penalty\":%d}}\n",
           r.avg_ms, seq_len, block_size, penalty);

    printf("{\"type\":\"kernel\",\"name\":\"nw_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"sequence_length\":%d,\"block_size\":%d,\"penalty\":%d}}\n",
           r.avg_ms, seq_len, block_size, penalty);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gcups\",\"value\":%.2f},{\"name\":\"effective_gbps\",\"value\":%.2f}]}\n",
           total_ms, gcups, eff_bw);

    fprintf(stderr, "GCUPS: %.2f  |  Effective GB/s: %.2f  (avg %.4f ms)\n",
            gcups, eff_bw, r.avg_ms);

    CUDA_CHECK(cudaFree(d_seq));
    CUDA_CHECK(cudaFree(d_matrix));
    free(h_seq);
    free(h_matrix);

    return 0;
}
