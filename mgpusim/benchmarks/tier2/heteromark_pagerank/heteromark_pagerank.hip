// heteromark_pagerank.hip — HeteroMark PageRank benchmark (HIP, self-contained)
//
// Iterative PageRank (power method) on a synthetic Erdős–Rényi graph in CSR
// format.  PR[v] = (1-d)/N + d * sum(PR[u]/degree[u]) for all u→v.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./heteromark_pagerank [--vertices N] [--pr-iterations P]
//
//   --vertices N       Number of vertices (default: 65536)
//   --pr-iterations P  PageRank iterations per run (default: 20)
//
// Output (stdout): CSV row — heteromark_pagerank,<N_vertices>,<time_ms>,<edges_per_sec>
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
// Argument parsing
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Simple deterministic PRNG (xorshift32)
// ---------------------------------------------------------------------------

static uint32_t xorshift32(uint32_t& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return state;
}

// ---------------------------------------------------------------------------
// Generate synthetic Erdős–Rényi graph in CSR format
// ---------------------------------------------------------------------------

static void generateCSR(int N, int avgDegree, uint32_t seed,
                         std::vector<int>& rowPtr,
                         std::vector<int>& colIdx,
                         std::vector<int>& outDegree) {
    // Build adjacency lists using deterministic PRNG
    // For each vertex u, randomly pick ~avgDegree target vertices
    std::vector<std::vector<int>> adj(N);
    uint32_t rng = seed;

    for (int u = 0; u < N; ++u) {
        int deg = avgDegree;
        for (int d = 0; d < deg; ++d) {
            int v = (int)(xorshift32(rng) % (uint32_t)N);
            adj[v].push_back(u);  // edge u→v stored in v's in-neighbor list
        }
    }

    // Out-degree of each vertex
    outDegree.resize(N);
    for (int u = 0; u < N; ++u) {
        outDegree[u] = avgDegree;  // each vertex has exactly avgDegree out-edges
    }

    // Build CSR for incoming edges (for PageRank gather)
    rowPtr.resize(N + 1);
    rowPtr[0] = 0;
    for (int v = 0; v < N; ++v) {
        rowPtr[v + 1] = rowPtr[v] + (int)adj[v].size();
    }

    int nnz = rowPtr[N];
    colIdx.resize(nnz);
    for (int v = 0; v < N; ++v) {
        int offset = rowPtr[v];
        for (int j = 0; j < (int)adj[v].size(); ++j) {
            colIdx[offset + j] = adj[v][j];
        }
    }
}

// ---------------------------------------------------------------------------
// PageRank kernel: one iteration
//   PR_new[v] = (1-d)/N + d * sum(PR_old[u]/degree[u]) for all u→v
// ---------------------------------------------------------------------------

__global__ void pagerank_kernel(const int* __restrict__ rowPtr,
                                 const int* __restrict__ colIdx,
                                 const int* __restrict__ outDegree,
                                 const float* __restrict__ prOld,
                                 float* __restrict__ prNew,
                                 int N, float damping) {
    int v = blockIdx.x * blockDim.x + threadIdx.x;
    if (v >= N) return;

    float sum = 0.0f;
    int start = rowPtr[v];
    int end   = rowPtr[v + 1];

    for (int e = start; e < end; ++e) {
        int u = colIdx[e];
        sum += prOld[u] / (float)outDegree[u];
    }

    prNew[v] = (1.0f - damping) / (float)N + damping * sum;
}

// ---------------------------------------------------------------------------
// CPU reference PageRank for verification
// ---------------------------------------------------------------------------

static void pagerank_cpu(const std::vector<int>& rowPtr,
                          const std::vector<int>& colIdx,
                          const std::vector<int>& outDegree,
                          std::vector<float>& pr,
                          int N, int prIters, float damping) {
    std::vector<float> prNew(N);
    for (int i = 0; i < N; ++i) pr[i] = 1.0f / (float)N;

    for (int iter = 0; iter < prIters; ++iter) {
        for (int v = 0; v < N; ++v) {
            float sum = 0.0f;
            for (int e = rowPtr[v]; e < rowPtr[v + 1]; ++e) {
                int u = colIdx[e];
                sum += pr[u] / (float)outDegree[u];
            }
            prNew[v] = (1.0f - damping) / (float)N + damping * sum;
        }
        pr.swap(prNew);
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

static double parseDoubleParam(int argc, char** argv, const char* name, double defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            double v = atof(argv[i + 1]);
            return v;
        }
    }
    return defaultVal;
}

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int vertices     = 65536;
    int pr_iterations = 20;
    double damping_factor = 0.85;

    // Command-line fallback
    vertices      = parseIntParam(argc, argv, "--vertices", vertices);
    pr_iterations = parseIntParam(argc, argv, "--pr-iterations", pr_iterations);
    damping_factor = parseDoubleParam(argc, argv, "--damping_factor", damping_factor);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_vertices");
    if (env_val) vertices = atoi(env_val);
    env_val = getenv("BENCH_PARAM_pr_iterations");
    if (env_val) pr_iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_damping_factor");
    if (env_val) damping_factor = atof(env_val);
    int num_warmup = 5;

    int N       = vertices;
    int prIters = pr_iterations;
    float damping = (float)damping_factor;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Vertices: %d  |  PR iterations: %d  |  Warmup: %d\n\n",
            N, prIters, num_warmup);

    // Generate synthetic graph
    std::vector<int> rowPtr, colIdx, outDegree;
    generateCSR(N, 16, 42, rowPtr, colIdx, outDegree);
    int numEdges = rowPtr[N];
    fprintf(stderr, "Graph: %d vertices, %d edges (avg in-degree: %.1f)\n",
            N, numEdges, (double)numEdges / N);

    // Host PageRank arrays
    std::vector<float> h_pr(N, 1.0f / (float)N);
    std::vector<float> h_prNew(N);

    // Device allocations
    int *d_rowPtr, *d_colIdx, *d_outDegree;
    float *d_prOld, *d_prNew;

    HIP_CHECK(hipMalloc(&d_rowPtr, (N + 1) * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_colIdx, numEdges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_outDegree, N * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_prOld, N * sizeof(float)));
    HIP_CHECK(hipMalloc(&d_prNew, N * sizeof(float)));

    // Copy graph to device (constant across runs)
    HIP_CHECK(hipMemcpy(d_rowPtr, rowPtr.data(), (N + 1) * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_colIdx, colIdx.data(), numEdges * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_outDegree, outDegree.data(), N * sizeof(int), hipMemcpyHostToDevice));

    int blockSize = 256;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // Lambda: run full PageRank (prIters iterations)
    auto run_pagerank = [&]() {
        // Initialize PR to 1/N
        for (int i = 0; i < N; ++i) h_pr[i] = 1.0f / (float)N;
        HIP_CHECK(hipMemcpy(d_prOld, h_pr.data(), N * sizeof(float), hipMemcpyHostToDevice));

        for (int it = 0; it < prIters; ++it) {
            hipLaunchKernelGGL(pagerank_kernel, dim3(gridSize), dim3(blockSize),
                               0, 0,
                               d_rowPtr, d_colIdx, d_outDegree,
                               d_prOld, d_prNew, N, damping);

            // Swap pointers
            float* tmp = d_prOld;
            d_prOld = d_prNew;
            d_prNew = tmp;
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_pagerank();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        run_pagerank();
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

    // edges_per_sec = numEdges * prIters / time_s
    double edges_per_sec = (double)numEdges * (double)prIters / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"pagerank_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"vertices\":%d,\"pr_iterations\":%d,"
           "\"damping_factor\":%.6f}}\n",
           avg_ms, vertices, pr_iterations, damping_factor);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"edges_per_sec\",\"value\":%.2f}]}\n",
           total_ms, edges_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4e edges/sec\n", edges_per_sec);

    // -------------------------------------------------------------------
    // Verification: compare GPU result against CPU reference
    // -------------------------------------------------------------------
    HIP_CHECK(hipMemcpy(h_pr.data(), d_prOld, N * sizeof(float), hipMemcpyDeviceToHost));

    std::vector<float> refPr(N);
    pagerank_cpu(rowPtr, colIdx, outDegree, refPr, N, prIters, damping);

    int errors = 0;
    for (int v = 0; v < N; ++v) {
        float diff = fabsf(h_pr[v] - refPr[v]);
        float tol  = 1e-4f * fabsf(refPr[v]) + 1e-7f;
        if (diff > tol) {
            if (errors < 10) {
                fprintf(stderr, "Mismatch at v=%d: GPU=%.8f CPU=%.8f\n",
                        v, h_pr[v], refPr[v]);
            }
            errors++;
        }
    }
    if (errors > 0)
        fprintf(stderr, "FAIL: %d errors out of %d vertices\n", errors, N);
    else
        fprintf(stderr, "PASS\n");

    // Cleanup
    HIP_CHECK(hipFree(d_rowPtr));
    HIP_CHECK(hipFree(d_colIdx));
    HIP_CHECK(hipFree(d_outDegree));
    HIP_CHECK(hipFree(d_prOld));
    HIP_CHECK(hipFree(d_prNew));

    return 0;
}
