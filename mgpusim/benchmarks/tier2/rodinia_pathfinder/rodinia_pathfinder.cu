// rodinia_pathfinder.cu — Rodinia PathFinder benchmark (CUDA, self-contained)
//
// Computes the minimum-cost path through a 2D grid from top to bottom.
// Each row is processed by one GPU kernel launch (double-buffered).
//
// Native CUDA implementation.
//
// Usage:
//   ./rodinia_pathfinder [--rows R] [--cols C]
//
//   --rows R        Grid rows                  (default: 100000)
//   --cols C        Grid columns               (default: 100)
//
// Output (stdout): CSV — benchmark,rows,cols,time_ms,GBs
// Output (stderr): Verification result and device info

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <climits>
#include <vector>
#include <algorithm>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// CUDA error check
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

// ---------------------------------------------------------------------------
// PathFinder kernel
//   - gpuWall: ROWS x COLS cost grid (row-major)
//   - gpuSrc:  COLS minimum-cost values from previous row
//   - gpuDst:  COLS minimum-cost values for current row (output)
//   - cols:    number of columns
//   - t:       current row index (1-based)
// ---------------------------------------------------------------------------
#define BLOCK_SIZE 256

__global__ void dynproc_kernel(const int* __restrict__ gpuWall,
                               const int* __restrict__ gpuSrc,
                               int* __restrict__       gpuDst,
                               int cols, int t) {
    int col = blockIdx.x * blockDim.x + threadIdx.x;
    if (col >= cols) return;

    int left  = (col > 0)      ? gpuSrc[col - 1] : INT_MAX;
    int above = gpuSrc[col];
    int right = (col < cols-1) ? gpuSrc[col + 1] : INT_MAX;

    int min3 = min(min(left, above), right);
    gpuDst[col] = gpuWall[(long long)t * cols + col] + min3;
}

// ---------------------------------------------------------------------------
// Argument parsing helpers
// ---------------------------------------------------------------------------
static int parseIntParam(int argc, char** argv, const char* name, int def) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
        }
    }
    return def;
}

// ---------------------------------------------------------------------------
// CPU reference for verification (small grid only)
// ---------------------------------------------------------------------------
static void cpu_pathfinder(const int* wall, int* result, int rows, int cols) {
    std::vector<int> src(cols), dst(cols);
    // Initialize with row 0
    for (int c = 0; c < cols; c++) src[c] = wall[c];
    // Sweep rows
    for (int t = 1; t < rows; t++) {
        for (int c = 0; c < cols; c++) {
            int left  = (c > 0)      ? src[c - 1] : INT_MAX;
            int above = src[c];
            int right = (c < cols-1) ? src[c + 1] : INT_MAX;
            dst[c] = wall[(long long)t * cols + c] + std::min({left, above, right});
        }
        std::swap(src, dst);
    }
    for (int c = 0; c < cols; c++) result[c] = src[c];
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int cols  = 100;
    int rows  = 100000;
    int block_size_param = 256;

    // Command-line fallback
    rows  = parseIntParam(argc, argv, "--rows", rows);
    cols  = parseIntParam(argc, argv, "--cols", cols);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_cols");
    if (env_val) cols = atoi(env_val);
    env_val = getenv("BENCH_PARAM_rows");
    if (env_val) rows = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    int num_warmup = 0;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d rows x %d cols\n\n",
            rows, cols);

    long long total_cells = (long long)rows * cols;
    size_t wall_bytes = (size_t)total_cells * sizeof(int);
    size_t row_bytes  = (size_t)cols * sizeof(int);

    // Host allocations
    int* h_wall = (int*)malloc(wall_bytes);
    int* h_src  = (int*)malloc(row_bytes);
    int* h_result = (int*)malloc(row_bytes);

    // Initialize wall with random values in [0, 10)
    srand(42);
    for (long long i = 0; i < total_cells; i++) {
        h_wall[i] = rand() % 10;
    }
    // Initialize src = row 0 costs
    for (int c = 0; c < cols; c++) {
        h_src[c] = h_wall[c];
    }

    // -----------------------------------------------------------------
    // Verification with small grid (avoid OOM for large grids)
    // -----------------------------------------------------------------
    {
        int vrows = (rows  < 1000) ? rows  : 1000;
        int vcols = (cols  < 100)  ? cols  : 100;
        long long vcells = (long long)vrows * vcols;

        int* v_wall = (int*)malloc((size_t)vcells * sizeof(int));
        srand(123);
        for (long long i = 0; i < vcells; i++) v_wall[i] = rand() % 10;

        // CPU reference
        std::vector<int> cpu_result(vcols);
        cpu_pathfinder(v_wall, cpu_result.data(), vrows, vcols);

        // GPU reference (small grid)
        int *gv_wall, *gv_src, *gv_dst;
        CUDA_CHECK(cudaMalloc(&gv_wall, (size_t)vcells * sizeof(int)));
        CUDA_CHECK(cudaMalloc(&gv_src,  (size_t)vcols  * sizeof(int)));
        CUDA_CHECK(cudaMalloc(&gv_dst,  (size_t)vcols  * sizeof(int)));
        CUDA_CHECK(cudaMemcpy(gv_wall, v_wall, (size_t)vcells * sizeof(int), cudaMemcpyHostToDevice));

        // Initialize src = row 0
        CUDA_CHECK(cudaMemcpy(gv_src, v_wall, (size_t)vcols * sizeof(int), cudaMemcpyHostToDevice));

        int* gsrc = gv_src;
        int* gdst = gv_dst;
        int nblocks = (vcols + BLOCK_SIZE - 1) / BLOCK_SIZE;

        for (int t = 1; t < vrows; t++) {
            dynproc_kernel<<<nblocks, BLOCK_SIZE, 0, 0>>>(gv_wall, gsrc, gdst, vcols, t);
            int* tmp = gsrc; gsrc = gdst; gdst = tmp;
        }
        CUDA_CHECK(cudaDeviceSynchronize());

        std::vector<int> gpu_result(vcols);
        CUDA_CHECK(cudaMemcpy(gpu_result.data(), gsrc, (size_t)vcols * sizeof(int), cudaMemcpyDeviceToHost));

        bool ok = true;
        for (int c = 0; c < vcols && ok; c++) {
            if (cpu_result[c] != gpu_result[c]) {
                fprintf(stderr, "VERIFY FAIL at col %d: cpu=%d gpu=%d\n",
                        c, cpu_result[c], gpu_result[c]);
                ok = false;
            }
        }
        if (ok) fprintf(stderr, "Verification PASSED (small grid %dx%d)\n", vrows, vcols);

        CUDA_CHECK(cudaFree(gv_wall));
        CUDA_CHECK(cudaFree(gv_src));
        CUDA_CHECK(cudaFree(gv_dst));
        free(v_wall);
    }

    // -----------------------------------------------------------------
    // Device allocations for full benchmark
    // -----------------------------------------------------------------
    int *d_wall, *d_src, *d_dst;
    CUDA_CHECK(cudaMalloc(&d_wall, wall_bytes));
    CUDA_CHECK(cudaMalloc(&d_src,  row_bytes));
    CUDA_CHECK(cudaMalloc(&d_dst,  row_bytes));
    CUDA_CHECK(cudaMemcpy(d_wall, h_wall, wall_bytes, cudaMemcpyHostToDevice));

    int nblocks = (cols + BLOCK_SIZE - 1) / BLOCK_SIZE;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        CUDA_CHECK(cudaMemcpy(d_src, h_src, row_bytes, cudaMemcpyHostToDevice));
        int* src = d_src;
        int* dst = d_dst;
        for (int t = 1; t < rows; t++) {
            dynproc_kernel<<<nblocks, BLOCK_SIZE, 0, 0>>>(d_wall, src, dst, cols, t);
            int* tmp = src; src = dst; dst = tmp;
        }
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -----------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------
    cudaEvent_t ev_start, ev_stop;
    CUDA_CHECK(cudaEventCreate(&ev_start));
    CUDA_CHECK(cudaEventCreate(&ev_stop));

    std::vector<double> times(1);
    for (int iter = 0; iter < 1; iter++) {
        CUDA_CHECK(cudaMemcpy(d_src, h_src, row_bytes, cudaMemcpyHostToDevice));

        int* src = d_src;
        int* dst = d_dst;

        CUDA_CHECK(cudaEventRecord(ev_start, 0));

        for (int t = 1; t < rows; t++) {
            dynproc_kernel<<<nblocks, BLOCK_SIZE, 0, 0>>>(d_wall, src, dst, cols, t);
            int* tmp = src; src = dst; dst = tmp;
        }

        CUDA_CHECK(cudaEventRecord(ev_stop, 0));
        CUDA_CHECK(cudaEventSynchronize(ev_stop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, ev_start, ev_stop));
        times[iter] = (double)ms;

        // Copy result back on last iteration
        if (iter == 1 - 1) {
            CUDA_CHECK(cudaMemcpy(h_result, src, row_bytes, cudaMemcpyDeviceToHost));
        }
    }

    CUDA_CHECK(cudaEventDestroy(ev_start));
    CUDA_CHECK(cudaEventDestroy(ev_stop));

    // Compute stats
    double sum = 0.0;
    for (int i = 0; i < 1; i++) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s: ROWS * COLS * sizeof(int) * 3 (2 reads: src+wall, 1 write: dst)
    double total_bytes_accessed = (double)rows * cols * sizeof(int) * 3.0;
    double gb_s = total_bytes_accessed / (avg_ms * 1e-3) / 1e9;

    // Find min of result row
    int min_cost = h_result[0];
    for (int c = 1; c < cols; c++) {
        if (h_result[c] < min_cost) min_cost = h_result[c];
    }
    fprintf(stderr, "Min cost in final row: %d\n", min_cost);

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"dynproc_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"cols\":%d,\"rows\":%d\"block_size\":%d}}\n",
           avg_ms, cols, rows, block_size_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.4f}]}\n",
           total_ms, gb_s);

    // Cleanup
    CUDA_CHECK(cudaFree(d_wall));
    CUDA_CHECK(cudaFree(d_src));
    CUDA_CHECK(cudaFree(d_dst));
    free(h_wall);
    free(h_src);
    free(h_result);

    return 0;
}
