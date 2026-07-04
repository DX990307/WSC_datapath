// parboil_spmv.cu — Parboil SpMV benchmark (CUDA, self-contained)
//
// CSR Sparse Matrix-Vector Multiplication: y = A * x
//   - Matrix A is in Compressed Sparse Row (CSR) format
//   - Each GPU thread computes one output row:
//       y[row] = sum(values[j] * x[col_idx[j]]) for j in [row_ptr[row], row_ptr[row+1])
//
// Native CUDA implementation.
//
// Usage:
//   ./parboil_spmv [--num_rows N] [--avg_nnz_per_row K] [--block_size B]
//
//   --num_rows N         Number of matrix rows          (default: 65536)
//   --avg_nnz_per_row K  Average non-zeros per row      (default: 10)
//   --block_size B       GPU thread-block size          (default: 256)
//
// Output (stdout): CSV row —
//   kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
// Output (stderr): GFLOPS, verification PASS/FAIL

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

static int g_num_warmup = 0;

template <typename Func>
static BenchResult runBenchmark(const char* kernel_name,
                                const char* problem_size,
                                int         iterations,
                                Func        func) {
    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < g_num_warmup; ++w) {
        func();
    }
    CUDA_CHECK(cudaDeviceSynchronize());

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
// CSR SpMV kernel: each thread computes one row
// ---------------------------------------------------------------------------

__global__ void spmv_csr(const int*   __restrict__ row_ptr,
                          const int*   __restrict__ col_idx,
                          const float* __restrict__ values,
                          const float* __restrict__ x,
                          float*                    y,
                          int                       num_rows)
{
    int row = blockIdx.x * blockDim.x + threadIdx.x;
    if (row < num_rows) {
        float sum = 0.0f;
        int row_start = row_ptr[row];
        int row_end   = row_ptr[row + 1];
        for (int j = row_start; j < row_end; ++j) {
            sum += values[j] * x[col_idx[j]];
        }
        y[row] = sum;
    }
}

// ---------------------------------------------------------------------------
// Sparse matrix generation (CSR, random)
// ---------------------------------------------------------------------------

static void generate_csr_matrix(int num_rows, int avg_nnz, int seed,
                                 int**   h_row_ptr_out,
                                 int**   h_col_idx_out,
                                 float** h_values_out,
                                 int*    total_nnz_out)
{
    srand(seed);

    // Determine nnz per row (uniform around avg_nnz, at least 1)
    int* nnz_per_row = (int*)malloc((size_t)num_rows * sizeof(int));
    int  total_nnz   = 0;
    for (int i = 0; i < num_rows; ++i) {
        int nnz = 1 + (rand() % (2 * avg_nnz - 1));
        if (nnz > num_rows) nnz = num_rows;
        nnz_per_row[i] = nnz;
        total_nnz += nnz;
    }

    int*   row_ptr = (int*)  malloc((size_t)(num_rows + 1) * sizeof(int));
    int*   col_idx = (int*)  malloc((size_t)total_nnz      * sizeof(int));
    float* values  = (float*)malloc((size_t)total_nnz      * sizeof(float));

    // Build row_ptr
    row_ptr[0] = 0;
    for (int i = 0; i < num_rows; ++i)
        row_ptr[i + 1] = row_ptr[i] + nnz_per_row[i];

    // Fill column indices and values; distribute columns roughly evenly
    for (int i = 0; i < num_rows; ++i) {
        int start = row_ptr[i];
        int nnz   = nnz_per_row[i];
        int step  = (num_rows > nnz) ? (num_rows / nnz) : 1;
        for (int j = 0; j < nnz; ++j) {
            col_idx[start + j] = ((long long)j * step + (rand() % step)) % num_rows;
            values [start + j] = (float)(rand() % 100) / 100.0f + 0.01f;
        }
        // Insertion sort to keep columns ordered (improves cache behaviour)
        for (int j = 1; j < nnz; ++j) {
            int   kc = col_idx[start + j];
            float kv = values [start + j];
            int k = j - 1;
            while (k >= 0 && col_idx[start + k] > kc) {
                col_idx[start + k + 1] = col_idx[start + k];
                values [start + k + 1] = values [start + k];
                --k;
            }
            col_idx[start + k + 1] = kc;
            values [start + k + 1] = kv;
        }
    }

    free(nnz_per_row);
    *h_row_ptr_out = row_ptr;
    *h_col_idx_out = col_idx;
    *h_values_out  = values;
    *total_nnz_out = total_nnz;
}

// ---------------------------------------------------------------------------
// CPU reference (verify a sample of output rows)
// ---------------------------------------------------------------------------

static float cpu_spmv_row(const int* row_ptr, const int* col_idx,
                           const float* values, const float* x, int row)
{
    float sum = 0.0f;
    for (int j = row_ptr[row]; j < row_ptr[row + 1]; ++j)
        sum += values[j] * x[col_idx[j]];
    return sum;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int num_rows   = 65536;
    int avg_nnz    = 10;
    int block_size = 256;
    int seed       = 42;

    // Command-line args as fallback
    num_rows   = parseIntParam(argc, argv, "--num_rows",         num_rows);
    avg_nnz    = parseIntParam(argc, argv, "--avg_nnz_per_row",  avg_nnz);
    block_size = parseIntParam(argc, argv, "--block_size",       block_size);
    seed       = parseIntParam(argc, argv, "--seed",             seed);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_rows");
    if (env_val) num_rows = atoi(env_val);
    env_val = getenv("BENCH_PARAM_avg_nnz_per_row");
    if (env_val) avg_nnz = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    // Generate sparse matrix
    int*   h_row_ptr  = nullptr;
    int*   h_col_idx  = nullptr;
    float* h_values   = nullptr;
    int    total_nnz  = 0;
    generate_csr_matrix(num_rows, avg_nnz, seed,
                        &h_row_ptr, &h_col_idx, &h_values, &total_nnz);

    // Generate dense input vector x (reuse seed offset)
    float* h_x = (float*)malloc((size_t)num_rows * sizeof(float));
    float* h_y = (float*)malloc((size_t)num_rows * sizeof(float));
    srand(seed + 1);
    for (int i = 0; i < num_rows; ++i)
        h_x[i] = (float)(rand() % 100) / 100.0f + 0.01f;

    // Device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Rows: %d  |  NNZ: %d  |  Block: %d\n\n",
            num_rows, total_nnz, block_size);

    // Device allocations
    int*   d_row_ptr;
    int*   d_col_idx;
    float* d_values;
    float* d_x;
    float* d_y;
    CUDA_CHECK(cudaMalloc(&d_row_ptr, (size_t)(num_rows + 1) * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_col_idx, (size_t)total_nnz      * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_values,  (size_t)total_nnz      * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_x,       (size_t)num_rows       * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_y,       (size_t)num_rows       * sizeof(float)));

    CUDA_CHECK(cudaMemcpy(d_row_ptr, h_row_ptr,
                        (size_t)(num_rows + 1) * sizeof(int), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_col_idx, h_col_idx,
                        (size_t)total_nnz * sizeof(int), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_values, h_values,
                        (size_t)total_nnz * sizeof(float), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_x, h_x,
                        (size_t)num_rows * sizeof(float), cudaMemcpyHostToDevice));

    dim3 block(block_size);
    dim3 grid((num_rows + block_size - 1) / block_size);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%d", num_rows);

    g_num_warmup = num_warmup;
    BenchResult r = runBenchmark(
        "spmv_csr", problemSize, 1, [&]() {
        spmv_csr<<<grid, block>>>(d_row_ptr, d_col_idx, d_values, d_x, d_y, num_rows);
    });

    // GFLOPS = 2 * total_nnz (one multiply + one add per NNZ)
    double gflops = 2.0 * (double)total_nnz / (r.avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"spmv_csr\",\"time_ms\":%.6f,"
           "\"params\":{\"num_rows\":%d,\"avg_nnz_per_row\":%d,\"block_size\":%d,"
           "}}\n",
           r.avg_ms, num_rows, avg_nnz, block_size);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           r.avg_ms, gflops);

    fprintf(stderr, "GFLOPS: %.4f  (avg %.4f ms)\n", gflops, r.avg_ms);

    // Verification: copy result back and check a sample of rows
    CUDA_CHECK(cudaMemcpy(h_y, d_y, (size_t)num_rows * sizeof(float),
                        cudaMemcpyDeviceToHost));

    int check_rows = (num_rows < 100) ? num_rows : 100;
    int step       = num_rows / check_rows;
    int errors     = 0;
    for (int ri = 0; ri < check_rows; ++ri) {
        int row  = ri * step;
        float ref = cpu_spmv_row(h_row_ptr, h_col_idx, h_values, h_x, row);
        float got = h_y[row];
        float thresh = 1e-4f * fabsf(ref) + 1e-5f;
        if (fabsf(got - ref) > thresh) {
            if (errors < 5)
                fprintf(stderr, "Mismatch row %d: got %.6f expected %.6f\n",
                        row, got, ref);
            ++errors;
        }
    }
    if (errors > 0)
        fprintf(stderr, "FAIL: %d mismatches in %d sampled rows\n", errors, check_rows);
    else
        fprintf(stderr, "PASS\n");

    // Cleanup
    CUDA_CHECK(cudaFree(d_row_ptr));
    CUDA_CHECK(cudaFree(d_col_idx));
    CUDA_CHECK(cudaFree(d_values));
    CUDA_CHECK(cudaFree(d_x));
    CUDA_CHECK(cudaFree(d_y));
    free(h_row_ptr);
    free(h_col_idx);
    free(h_values);
    free(h_x);
    free(h_y);

    return (errors > 0) ? EXIT_FAILURE : EXIT_SUCCESS;
}
