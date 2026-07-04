// altis_gups.hip — GUPS (Giga Updates Per Second) random memory access benchmark (HIP)
//
// Measures random memory access throughput by performing random read-modify-write
// operations on a large table of 64-bit entries. Each thread uses an xorshift
// PRNG to generate random indices and XOR-updates the table.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Output (stdout): JSON-lines protocol
// Output (stderr): human-readable results

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
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>

// ---------------------------------------------------------------------------
// Error-checking macro
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
// GUPS kernel: each thread performs num_updates random XOR updates on the table
// ---------------------------------------------------------------------------

__global__ void gups_kernel(unsigned long long* __restrict__ table,
                            int table_size,
                            int num_updates,
                            unsigned long long seed_base) {
    int tid = blockIdx.x * blockDim.x + threadIdx.x;

    // Per-thread xorshift64 state seeded uniquely
    unsigned long long state = seed_base + (unsigned long long)tid * 6364136223846793005ULL + 1;
    if (state == 0) state = 1;

    for (int u = 0; u < num_updates; ++u) {
        // xorshift64
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;

        int idx = (int)(state % (unsigned long long)table_size);
        atomicXor(&table[idx], state);
    }
}

// ---------------------------------------------------------------------------
// CPU reference: perform random XOR updates
// ---------------------------------------------------------------------------

static void gups_cpu_ref(unsigned long long* table, int table_size,
                         int total_threads, int num_updates,
                         unsigned long long seed_base) {
    for (int tid = 0; tid < total_threads; ++tid) {
        unsigned long long state = seed_base + (unsigned long long)tid * 6364136223846793005ULL + 1;
        if (state == 0) state = 1;

        for (int u = 0; u < num_updates; ++u) {
            state ^= state << 13;
            state ^= state >> 7;
            state ^= state << 17;

            int idx = (int)(state % (unsigned long long)table_size);
            table[idx] ^= state;
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int table_size   = 1048576;
    int block_size   = 256;
    int num_updates  = 128;
    int num_threads  = 65536;

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_table_size");
    if (env_val) table_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_updates");
    if (env_val) num_updates = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_threads");
    if (env_val) num_threads = atoi(env_val);
    int num_warmup = 5;

    int gridSize   = (num_threads + block_size - 1) / block_size;
    int actual_threads = gridSize * block_size;
    long long total_updates = (long long)actual_threads * num_updates;
    unsigned long long seed_base = 42ULL;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "GUPS benchmark  |  Table size: %d (%.2f MB)  |  "
            "Updates/thread: %d  |  Threads: %d  |  Total updates: %lld  |  "
            "Iterations: 5 warmup + %d timed\n\n",
            table_size, (double)table_size * 8.0 / 1e6,
            num_updates, actual_threads, total_updates);

    // -----------------------------------------------------------------------
    // Allocate table on device
    // -----------------------------------------------------------------------
    size_t table_bytes = (size_t)table_size * sizeof(unsigned long long);

    unsigned long long* d_table;
    HIP_CHECK(hipMalloc(&d_table, table_bytes));

    // Initialize table: each entry = index
    std::vector<unsigned long long> h_table(table_size);
    for (int i = 0; i < table_size; ++i) {
        h_table[i] = (unsigned long long)i;
    }

    auto reset_table = [&]() {
        HIP_CHECK(hipMemcpy(d_table, h_table.data(), table_bytes, hipMemcpyHostToDevice));
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        reset_table();
        hipLaunchKernelGGL(gups_kernel, dim3(gridSize), dim3(block_size),
                           0, 0, d_table, table_size, num_updates, seed_base);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        reset_table();

        HIP_CHECK(hipEventRecord(evStart, 0));
        hipLaunchKernelGGL(gups_kernel, dim3(gridSize), dim3(block_size),
                           0, 0, d_table, table_size, num_updates, seed_base);
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    double gups = (double)total_updates / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"gups_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"table_size\":%d,\"block_size\":%d,\"num_updates\":%d,"
           "\"num_threads\":%d}}\n",
           avg_ms, table_size, block_size, num_updates, num_threads);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gups\",\"value\":%.6f}]}\n",
           total_ms, gups);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.6f GUPS (Giga Updates Per Second)\n", gups);

    // -----------------------------------------------------------------------
    // Verification
    // -----------------------------------------------------------------------
    {
        reset_table();
        hipLaunchKernelGGL(gups_kernel, dim3(gridSize), dim3(block_size),
                           0, 0, d_table, table_size, num_updates, seed_base);
        HIP_CHECK(hipDeviceSynchronize());

        std::vector<unsigned long long> gpu_table(table_size);
        HIP_CHECK(hipMemcpy(gpu_table.data(), d_table, table_bytes, hipMemcpyDeviceToHost));

        std::vector<unsigned long long> cpu_table(h_table.begin(), h_table.end());
        gups_cpu_ref(cpu_table.data(), table_size, actual_threads, num_updates, seed_base);

        int verify_n = (table_size < 1024) ? table_size : 1024;
        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            if (gpu_table[i] != cpu_table[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at index %d: GPU=%llu CPU=%llu\n",
                            i, gpu_table[i], cpu_table[i]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d mismatches in first %d entries\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    HIP_CHECK(hipFree(d_table));
    return 0;
}
