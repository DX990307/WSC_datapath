// altis_gups.cu — GUPS (Giga Updates Per Second) random memory access benchmark
//
// Measures random memory access throughput by performing random read-modify-write
// operations on a large table of 64-bit entries. Each thread uses an xorshift
// PRNG to generate random indices and XOR-updates the table.
//
// Native CUDA implementation.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Error-checking macro
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
// GUPS kernel: each thread performs num_updates random XOR updates on the table
// ---------------------------------------------------------------------------

__global__ void gups_kernel(unsigned long long* __restrict__ table,
                            int table_size,
                            int num_updates,
                            unsigned long long seed_base) {
    int tid = blockIdx.x * blockDim.x + threadIdx.x;
    int total_threads = gridDim.x * blockDim.x;

    // Per-thread xorshift64 state seeded uniquely
    unsigned long long state = seed_base + (unsigned long long)tid * 6364136223846793005ULL + 1;
    if (state == 0) state = 1;

    for (int u = 0; u < num_updates; ++u) {
        // xorshift64
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;

        int idx = (int)(state % (unsigned long long)table_size);
        // Atomic XOR to avoid race conditions (matches HPCC GUPS spec)
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
    int table_size   = 1048576;   // number of 64-bit entries
    int block_size   = 256;
    int num_updates  = 128;       // updates per thread
    int num_threads  = 65536;     // total threads launched

    // Environment variable overrides (primary)
    const char* env_val;
    env_val = getenv("BENCH_PARAM_table_size");
    if (env_val) table_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_updates");
    if (env_val) num_updates = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_threads");
    if (env_val) num_threads = atoi(env_val);
    int num_warmup = 0;

    int gridSize   = (num_threads + block_size - 1) / block_size;
    int actual_threads = gridSize * block_size;
    long long total_updates = (long long)actual_threads * num_updates;
    unsigned long long seed_base = 42ULL;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "GUPS benchmark  |  Table size: %d (%.2f MB)  |  "
            "Updates/thread: %d  |  Threads: %d  |  Total updates: %lld  |  "
            "Iterations: %d warmup + %d timed\n\n",
            table_size, (double)table_size * 8.0 / 1e6,
            num_updates, actual_threads, total_updates, num_warmup);

    // -----------------------------------------------------------------------
    // Allocate table on device
    // -----------------------------------------------------------------------
    size_t table_bytes = (size_t)table_size * sizeof(unsigned long long);

    unsigned long long* d_table;
    CUDA_CHECK(cudaMalloc(&d_table, table_bytes));

    // Initialize table: each entry = index (synthetic data)
    std::vector<unsigned long long> h_table(table_size);
    for (int i = 0; i < table_size; ++i) {
        h_table[i] = (unsigned long long)i;
    }

    // Lambda: reset table on device
    auto reset_table = [&]() {
        CUDA_CHECK(cudaMemcpy(d_table, h_table.data(), table_bytes, cudaMemcpyHostToDevice));
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        reset_table();
        gups_kernel<<<dim3(gridSize), dim3(block_size), 0, 0>>>(
            d_table, table_size, num_updates, seed_base);
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        reset_table();

        CUDA_CHECK(cudaEventRecord(evStart, 0));
        gups_kernel<<<dim3(gridSize), dim3(block_size), 0, 0>>>(
            d_table, table_size, num_updates, seed_base);
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    // GUPS = total_updates / time_s / 1e9
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

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.6f GUPS (Giga Updates Per Second)\n", gups);

    // -----------------------------------------------------------------------
    // Verification: compare GPU result with CPU reference (small subset)
    // -----------------------------------------------------------------------
    {
        // Reset and run on GPU once
        reset_table();
        gups_kernel<<<dim3(gridSize), dim3(block_size), 0, 0>>>(
            d_table, table_size, num_updates, seed_base);
        CUDA_CHECK(cudaDeviceSynchronize());

        std::vector<unsigned long long> gpu_table(table_size);
        CUDA_CHECK(cudaMemcpy(gpu_table.data(), d_table, table_bytes, cudaMemcpyDeviceToHost));

        // CPU reference
        std::vector<unsigned long long> cpu_table(h_table.begin(), h_table.end());
        gups_cpu_ref(cpu_table.data(), table_size, actual_threads, num_updates, seed_base);

        // Compare (GUPS uses atomics so results should match exactly)
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

    CUDA_CHECK(cudaFree(d_table));
    return 0;
}
