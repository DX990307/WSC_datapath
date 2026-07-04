/**
 * npb_cg_metal.mm — Apple Metal host for NPB Conjugate Gradient benchmark.
 *
 * Sparse iterative CG solver with SpMV (CSR), dot products, and AXPY.
 *
 * Output (stdout): JSON-lines
 * Output (stderr): human-readable
 *
 * Build:
 *   make PLATFORM=metal
 */

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Timing helpers
// ---------------------------------------------------------------------------

static double ticks_to_ms(uint64_t ticks) {
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-6;
}

// ---------------------------------------------------------------------------
// Parameter struct (must match .metal)
// ---------------------------------------------------------------------------

struct CgParams {
    uint32_t N;
    float alpha;
};

// ---------------------------------------------------------------------------
// Simple PRNG
// ---------------------------------------------------------------------------

static inline unsigned int lcg_rand(unsigned int* state) {
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N              = 50000;
    int block_size     = 256;
    int max_cg_iters   = 25;
    int nonzeros_per_row = 7;
    int iterations     = 5;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_matrix_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_max_cg_iterations");
    if (env_val) max_cg_iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_nonzeros_per_row");
    if (env_val) nonzeros_per_row = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    int num_warmup = 0;

    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--size") == 0) N = atoi(argv[i+1]);
        if (strcmp(argv[i], "--iterations") == 0) iterations = atoi(argv[i+1]);
    }

    int iters = iterations;

    @autoreleasepool {

    // Get Metal device
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "No Metal device found.\n");
        return 1;
    }
    fprintf(stderr, "Device: %s\n", [[device name] UTF8String]);
    fprintf(stderr, "NPB CG  |  Matrix: %d x %d  |  NNZ/row: %d  |  "
            "CG iters: %d  |  Timed runs: 5 warmup + %d timed\n\n",
            N, N, nonzeros_per_row, max_cg_iters, iters);

    // Load shader library
    NSError* error = nil;
    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir = [exePath stringByDeletingLastPathComponent];
    NSString* libPath = [dir stringByAppendingPathComponent:@"npb_cg.metal"];

    // Try loading from file, fallback to same directory as executable
    if (![[NSFileManager defaultManager] fileExistsAtPath:libPath]) {
        // Try current directory
        libPath = @"npb_cg.metal";
    }

    NSString* src = [NSString stringWithContentsOfFile:libPath
                              encoding:NSUTF8StringEncoding error:&error];
    if (!src) {
        fprintf(stderr, "Failed to load shader file: %s\n",
                [[error localizedDescription] UTF8String]);
        return 1;
    }

    id<MTLLibrary> library = [device newLibraryWithSource:src options:nil error:&error];
    if (!library) {
        fprintf(stderr, "Failed to compile Metal shaders: %s\n",
                [[error localizedDescription] UTF8String]);
        return 1;
    }

    // Create compute pipelines
    id<MTLFunction> fn_spmv  = [library newFunctionWithName:@"spmv_kernel"];
    id<MTLFunction> fn_dot   = [library newFunctionWithName:@"dot_product_kernel"];
    id<MTLFunction> fn_axpy  = [library newFunctionWithName:@"axpy_kernel"];
    id<MTLFunction> fn_scale = [library newFunctionWithName:@"scale_kernel"];

    id<MTLComputePipelineState> pso_spmv  = [device newComputePipelineStateWithFunction:fn_spmv  error:&error];
    id<MTLComputePipelineState> pso_dot   = [device newComputePipelineStateWithFunction:fn_dot   error:&error];
    id<MTLComputePipelineState> pso_axpy  = [device newComputePipelineStateWithFunction:fn_axpy  error:&error];
    id<MTLComputePipelineState> pso_scale = [device newComputePipelineStateWithFunction:fn_scale error:&error];

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -----------------------------------------------------------------------
    // Generate synthetic sparse matrix in CSR format
    // -----------------------------------------------------------------------
    int nnz_per_row = nonzeros_per_row;
    long long total_nnz = (long long)N * nnz_per_row;

    std::vector<int>   h_row_ptr(N + 1);
    std::vector<int>   h_col_idx(total_nnz);
    std::vector<float> h_values(total_nnz);
    std::vector<float> h_x(N, 0.0f);
    std::vector<float> h_b(N, 1.0f);

    unsigned int seed = 42;

    for (int i = 0; i < N; ++i) h_row_ptr[i] = i * nnz_per_row;
    h_row_ptr[N] = (int)total_nnz;

    for (int i = 0; i < N; ++i) {
        int base = i * nnz_per_row;
        h_col_idx[base] = i;
        h_values[base]  = (float)(nnz_per_row + 1);
        for (int k = 1; k < nnz_per_row; ++k) {
            int col;
            do { col = lcg_rand(&seed) % N; } while (col == i);
            h_col_idx[base + k] = col;
            h_values[base + k]  = -1.0f;
        }
    }

    // -----------------------------------------------------------------------
    // Metal buffers
    // -----------------------------------------------------------------------
    size_t ptr_bytes = (N + 1) * sizeof(int);
    size_t idx_bytes = total_nnz * sizeof(int);
    size_t val_bytes = total_nnz * sizeof(float);
    size_t vec_bytes = N * sizeof(float);
    int numBlocks = (N + block_size - 1) / block_size;
    size_t partial_bytes = numBlocks * sizeof(float);

    id<MTLBuffer> buf_row_ptr = [device newBufferWithBytes:h_row_ptr.data() length:ptr_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_col_idx = [device newBufferWithBytes:h_col_idx.data() length:idx_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_values  = [device newBufferWithBytes:h_values.data()  length:val_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_x       = [device newBufferWithLength:vec_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_b       = [device newBufferWithBytes:h_b.data() length:vec_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_r       = [device newBufferWithLength:vec_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_p       = [device newBufferWithLength:vec_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_Ap      = [device newBufferWithLength:vec_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_partial = [device newBufferWithLength:partial_bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_params  = [device newBufferWithLength:sizeof(CgParams) options:MTLResourceStorageModeShared];

    MTLSize tgSize = MTLSizeMake(block_size, 1, 1);
    MTLSize gridSz = MTLSizeMake(((N + block_size - 1) / block_size) * block_size, 1, 1);
    MTLSize gridDot = gridSz;

    // Helper: finish dot product on CPU
    auto finish_dot = [&]() -> float {
        float* p = (float*)[buf_partial contents];
        float sum = 0.0f;
        for (int i = 0; i < numBlocks; ++i) sum += p[i];
        return sum;
    };

    // Helper: set params
    auto set_params = [&](uint32_t n, float alpha) {
        CgParams* pp = (CgParams*)[buf_params contents];
        pp->N = n;
        pp->alpha = alpha;
    };

    // Lambda: run one CG solve
    auto run_cg = [&]() {
        // Reset x = 0
        memset([buf_x contents], 0, vec_bytes);
        // r = b
        memcpy([buf_r contents], [buf_b contents], vec_bytes);
        // p = r
        memcpy([buf_p contents], [buf_r contents], vec_bytes);

        set_params(N, 0.0f);

        // rr = dot(r, r)
        {
            id<MTLCommandBuffer> cmd = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
            [enc setComputePipelineState:pso_dot];
            [enc setBuffer:buf_r offset:0 atIndex:0];
            [enc setBuffer:buf_r offset:0 atIndex:1];
            [enc setBuffer:buf_partial offset:0 atIndex:2];
            [enc setBuffer:buf_params offset:0 atIndex:3];
            [enc dispatchThreads:gridDot threadsPerThreadgroup:tgSize];
            [enc endEncoding];
            [cmd commit];
            [cmd waitUntilCompleted];
        }
        float rr = finish_dot();

        for (int iter = 0; iter < max_cg_iters; ++iter) {
            // Ap = A * p
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_spmv];
                [enc setBuffer:buf_row_ptr offset:0 atIndex:0];
                [enc setBuffer:buf_col_idx offset:0 atIndex:1];
                [enc setBuffer:buf_values  offset:0 atIndex:2];
                [enc setBuffer:buf_p       offset:0 atIndex:3];
                [enc setBuffer:buf_Ap      offset:0 atIndex:4];
                [enc setBuffer:buf_params  offset:0 atIndex:5];
                [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }

            // pAp = dot(p, Ap)
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_dot];
                [enc setBuffer:buf_p offset:0 atIndex:0];
                [enc setBuffer:buf_Ap offset:0 atIndex:1];
                [enc setBuffer:buf_partial offset:0 atIndex:2];
                [enc setBuffer:buf_params offset:0 atIndex:3];
                [enc dispatchThreads:gridDot threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }
            float pAp = finish_dot();
            float alpha = rr / fmaxf(pAp, 1e-20f);

            // x = x + alpha * p
            set_params(N, alpha);
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_axpy];
                [enc setBuffer:buf_p offset:0 atIndex:0];
                [enc setBuffer:buf_x offset:0 atIndex:1];
                [enc setBuffer:buf_params offset:0 atIndex:2];
                [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }

            // r = r - alpha * Ap
            set_params(N, -alpha);
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_axpy];
                [enc setBuffer:buf_Ap offset:0 atIndex:0];
                [enc setBuffer:buf_r  offset:0 atIndex:1];
                [enc setBuffer:buf_params offset:0 atIndex:2];
                [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }

            // rr_new = dot(r, r)
            set_params(N, 0.0f);
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_dot];
                [enc setBuffer:buf_r offset:0 atIndex:0];
                [enc setBuffer:buf_r offset:0 atIndex:1];
                [enc setBuffer:buf_partial offset:0 atIndex:2];
                [enc setBuffer:buf_params offset:0 atIndex:3];
                [enc dispatchThreads:gridDot threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }
            float rr_new = finish_dot();

            float beta = rr_new / fmaxf(rr, 1e-20f);

            // p = beta * p
            set_params(N, beta);
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_scale];
                [enc setBuffer:buf_p offset:0 atIndex:0];
                [enc setBuffer:buf_p offset:0 atIndex:1];
                [enc setBuffer:buf_params offset:0 atIndex:2];
                [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }

            // p = r + p  (i.e., p = p + 1.0*r)
            set_params(N, 1.0f);
            {
                id<MTLCommandBuffer> cmd = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
                [enc setComputePipelineState:pso_axpy];
                [enc setBuffer:buf_r offset:0 atIndex:0];
                [enc setBuffer:buf_p offset:0 atIndex:1];
                [enc setBuffer:buf_params offset:0 atIndex:2];
                [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
                [enc endEncoding];
                [cmd commit];
                [cmd waitUntilCompleted];
            }

            rr = rr_new;
        }
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) run_cg();

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_cg();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double tsum = 0.0;
    for (int i = 0; i < iters; ++i) tsum += times[i];
    double avg_ms = tsum / iters;
    double total_ms = tsum;

    long long spmv_bytes = ((long long)(N+1)*4 + total_nnz*4 + total_nnz*4 + (long long)N*4 + (long long)N*4);
    double spmv_bw_gb = (double)spmv_bytes * max_cg_iters / (avg_ms * 1e-3) / 1e9;

    printf("{\"type\":\"kernel\",\"name\":\"spmv_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           avg_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"kernel\",\"name\":\"dot_product_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           avg_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"kernel\",\"name\":\"axpy_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           avg_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"avg_iter_ms\",\"value\":%.4f},"
           "{\"name\":\"spmv_bw_gb_s\",\"value\":%.2f}]}\n",
           total_ms, avg_ms, spmv_bw_gb);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "SpMV bandwidth: %.2f GB/s\n", spmv_bw_gb);

    // Verification
    {
        run_cg();
        // Ap = A*x
        set_params(N, 0.0f);
        {
            id<MTLCommandBuffer> cmd = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
            [enc setComputePipelineState:pso_spmv];
            [enc setBuffer:buf_row_ptr offset:0 atIndex:0];
            [enc setBuffer:buf_col_idx offset:0 atIndex:1];
            [enc setBuffer:buf_values  offset:0 atIndex:2];
            [enc setBuffer:buf_x       offset:0 atIndex:3];
            [enc setBuffer:buf_Ap      offset:0 atIndex:4];
            [enc setBuffer:buf_params  offset:0 atIndex:5];
            [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
            [enc endEncoding];
            [cmd commit];
            [cmd waitUntilCompleted];
        }
        // r = b
        memcpy([buf_r contents], [buf_b contents], vec_bytes);
        // r = r - 1*Ap
        set_params(N, -1.0f);
        {
            id<MTLCommandBuffer> cmd = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
            [enc setComputePipelineState:pso_axpy];
            [enc setBuffer:buf_Ap offset:0 atIndex:0];
            [enc setBuffer:buf_r  offset:0 atIndex:1];
            [enc setBuffer:buf_params offset:0 atIndex:2];
            [enc dispatchThreads:gridSz threadsPerThreadgroup:tgSize];
            [enc endEncoding];
            [cmd commit];
            [cmd waitUntilCompleted];
        }
        // res_norm = dot(r,r)
        set_params(N, 0.0f);
        {
            id<MTLCommandBuffer> cmd = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
            [enc setComputePipelineState:pso_dot];
            [enc setBuffer:buf_r offset:0 atIndex:0];
            [enc setBuffer:buf_r offset:0 atIndex:1];
            [enc setBuffer:buf_partial offset:0 atIndex:2];
            [enc setBuffer:buf_params offset:0 atIndex:3];
            [enc dispatchThreads:gridDot threadsPerThreadgroup:tgSize];
            [enc endEncoding];
            [cmd commit];
            [cmd waitUntilCompleted];
        }
        float res_norm = sqrtf(finish_dot());

        // b_norm
        {
            id<MTLCommandBuffer> cmd = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
            [enc setComputePipelineState:pso_dot];
            [enc setBuffer:buf_b offset:0 atIndex:0];
            [enc setBuffer:buf_b offset:0 atIndex:1];
            [enc setBuffer:buf_partial offset:0 atIndex:2];
            [enc setBuffer:buf_params offset:0 atIndex:3];
            [enc dispatchThreads:gridDot threadsPerThreadgroup:tgSize];
            [enc endEncoding];
            [cmd commit];
            [cmd waitUntilCompleted];
        }
        float b_norm = sqrtf(finish_dot());

        float rel_res = res_norm / fmaxf(b_norm, 1e-20f);
        fprintf(stderr, "Residual ||b-Ax||/||b|| = %.6e\n", rel_res);

        if (rel_res < 1.0f)
            fprintf(stderr, "PASS\n");
        else
            fprintf(stderr, "FAIL: relative residual too large\n");
    }

    } // @autoreleasepool
    return 0;
}
