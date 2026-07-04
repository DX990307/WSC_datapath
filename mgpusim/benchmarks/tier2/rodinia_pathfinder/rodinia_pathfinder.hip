// rodinia_pathfinder.hip — Rodinia PathFinder benchmark (HIP, self-contained)
//
// Computes the minimum-cost path through a 2D grid from top to bottom.
// Each row is processed by one GPU kernel launch (double-buffered).
//
// Compiles with hipcc (AMD ROCm) or nvcc -x cu (CUDA).
//
// Usage:
//   ./rodinia_pathfinder [--rows R] [--cols C]
//
//   --rows R        Grid rows                  (default: 100000)
//   --cols C        Grid columns               (default: 100)
//
// Output (stdout): CSV — benchmark,rows,cols,time_ms,GBs
// Output (stderr): Verification result and device info

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyAsync          cudaMemcpyAsync
#define hipMemset               cudaMemset
#define hipMemsetAsync          cudaMemsetAsync
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
#define hipMemcpyToSymbol       cudaMemcpyToSymbol
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetLastError         cudaGetLastError
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipStream_t             cudaStream_t
#define hipStreamCreate         cudaStreamCreate
#define hipStreamDestroy        cudaStreamDestroy
#define hipStreamSynchronize    cudaStreamSynchronize
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#define hipHostMalloc           cudaMallocHost
#define hipHostFree             cudaFreeHost
#define hipHostMallocDefault    cudaHostAllocDefault
#define hipBlockIdx_x           blockIdx.x
#define hipBlockIdx_y           blockIdx.y
#define hipBlockIdx_z           blockIdx.z
#define hipThreadIdx_x          threadIdx.x
#define hipThreadIdx_y          threadIdx.y
#define hipThreadIdx_z          threadIdx.z
#define hipBlockDim_x           blockDim.x
#define hipBlockDim_y           blockDim.y
#define hipBlockDim_z           blockDim.z
#define hipGridDim_x            gridDim.x
#define hipGridDim_y            gridDim.y
#define hipGridDim_z            gridDim.z
#define __HIP_PLATFORM_AMD__    0
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <climits>
#include <vector>
#include <algorithm>

// ---------------------------------------------------------------------------
// HIP error check
// ---------------------------------------------------------------------------
#define HIP_CHECK(cmd)                                                         \
    do {                                                                       \
        hipError_t _e = (cmd);                                                 \
        if (_e != hipSuccess) {                                                \
            fprintf(stderr, "HIP error %s at %s:%d\n",                        \
                    hipGetErrorString(_e), __FILE__, __LINE__);                \
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
    int num_warmup = 5;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
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
        HIP_CHECK(hipMalloc(&gv_wall, (size_t)vcells * sizeof(int)));
        HIP_CHECK(hipMalloc(&gv_src,  (size_t)vcols  * sizeof(int)));
        HIP_CHECK(hipMalloc(&gv_dst,  (size_t)vcols  * sizeof(int)));
        HIP_CHECK(hipMemcpy(gv_wall, v_wall, (size_t)vcells * sizeof(int), hipMemcpyHostToDevice));

        // Initialize src = row 0
        HIP_CHECK(hipMemcpy(gv_src, v_wall, (size_t)vcols * sizeof(int), hipMemcpyHostToDevice));

        int* gsrc = gv_src;
        int* gdst = gv_dst;
        int nblocks = (vcols + BLOCK_SIZE - 1) / BLOCK_SIZE;

        for (int t = 1; t < vrows; t++) {
            hipLaunchKernelGGL(dynproc_kernel, nblocks, BLOCK_SIZE, 0, 0,
                               gv_wall, gsrc, gdst, vcols, t);
            int* tmp = gsrc; gsrc = gdst; gdst = tmp;
        }
        HIP_CHECK(hipDeviceSynchronize());

        std::vector<int> gpu_result(vcols);
        HIP_CHECK(hipMemcpy(gpu_result.data(), gsrc, (size_t)vcols * sizeof(int), hipMemcpyDeviceToHost));

        bool ok = true;
        for (int c = 0; c < vcols && ok; c++) {
            if (cpu_result[c] != gpu_result[c]) {
                fprintf(stderr, "VERIFY FAIL at col %d: cpu=%d gpu=%d\n",
                        c, cpu_result[c], gpu_result[c]);
                ok = false;
            }
        }
        if (ok) fprintf(stderr, "Verification PASSED (small grid %dx%d)\n", vrows, vcols);

        HIP_CHECK(hipFree(gv_wall));
        HIP_CHECK(hipFree(gv_src));
        HIP_CHECK(hipFree(gv_dst));
        free(v_wall);
    }

    // -----------------------------------------------------------------
    // Device allocations for full benchmark
    // -----------------------------------------------------------------
    int *d_wall, *d_src, *d_dst;
    HIP_CHECK(hipMalloc(&d_wall, wall_bytes));
    HIP_CHECK(hipMalloc(&d_src,  row_bytes));
    HIP_CHECK(hipMalloc(&d_dst,  row_bytes));
    HIP_CHECK(hipMemcpy(d_wall, h_wall, wall_bytes, hipMemcpyHostToDevice));

    int nblocks = (cols + BLOCK_SIZE - 1) / BLOCK_SIZE;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_src, h_src, row_bytes, hipMemcpyHostToDevice));
        int* src = d_src;
        int* dst = d_dst;
        for (int t = 1; t < rows; t++) {
            hipLaunchKernelGGL(dynproc_kernel, nblocks, BLOCK_SIZE, 0, 0,
                               d_wall, src, dst, cols, t);
            int* tmp = src; src = dst; dst = tmp;
        }
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -----------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------
    hipEvent_t ev_start, ev_stop;
    HIP_CHECK(hipEventCreate(&ev_start));
    HIP_CHECK(hipEventCreate(&ev_stop));

    std::vector<double> times(1);
    for (int iter = 0; iter < 1; iter++) {
        HIP_CHECK(hipMemcpy(d_src, h_src, row_bytes, hipMemcpyHostToDevice));

        int* src = d_src;
        int* dst = d_dst;

        HIP_CHECK(hipEventRecord(ev_start, 0));

        for (int t = 1; t < rows; t++) {
            hipLaunchKernelGGL(dynproc_kernel, nblocks, BLOCK_SIZE, 0, 0,
                               d_wall, src, dst, cols, t);
            int* tmp = src; src = dst; dst = tmp;
        }

        HIP_CHECK(hipEventRecord(ev_stop, 0));
        HIP_CHECK(hipEventSynchronize(ev_stop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, ev_start, ev_stop));
        times[iter] = (double)ms;

        // Copy result back on last iteration
        if (iter == 1 - 1) {
            HIP_CHECK(hipMemcpy(h_result, src, row_bytes, hipMemcpyDeviceToHost));
        }
    }

    HIP_CHECK(hipEventDestroy(ev_start));
    HIP_CHECK(hipEventDestroy(ev_stop));

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
           "\"params\":{\"cols\":%d,\"rows\":%d,\"block_size\":%d}}\n",
           avg_ms, cols, rows, block_size_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.4f}]}\n",
           total_ms, gb_s);

    // Cleanup
    HIP_CHECK(hipFree(d_wall));
    HIP_CHECK(hipFree(d_src));
    HIP_CHECK(hipFree(d_dst));
    free(h_wall);
    free(h_src);
    free(h_result);

    return 0;
}
