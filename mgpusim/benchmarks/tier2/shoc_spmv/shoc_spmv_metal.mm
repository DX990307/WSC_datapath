/**
 * shoc_spmv_metal.mm — Apple Metal host for the SHOC CSR SpMV benchmark.
 *
 * Computes: y = A * x
 *   where A is a synthetic CSR sparse matrix with exactly nnz_per_row
 *   non-zeros per row.
 *
 * Parameters are read from BENCH_PARAM_* environment variables,
 * with command-line --flags as fallback.
 *
 * Output (stdout): JSON-lines protocol
 * Output (stderr): Human-readable diagnostics
 */

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Embedded Metal shader source
// ---------------------------------------------------------------------------

static const char* kMetalSource = R"METAL(
#include <metal_stdlib>
using namespace metal;

kernel void spmv_csr_kernel(
    device const int*   row_ptr  [[ buffer(0) ]],
    device const int*   col_idx  [[ buffer(1) ]],
    device const float* values   [[ buffer(2) ]],
    device const float* x        [[ buffer(3) ]],
    device       float* y        [[ buffer(4) ]],
    constant     uint&  num_rows [[ buffer(5) ]],
    uint gid [[ thread_position_in_grid ]])
{
    if (gid >= num_rows) return;
    float sum = 0.0f;
    int start = row_ptr[gid];
    int end   = row_ptr[gid + 1];
    for (int k = start; k < end; ++k)
        sum += values[k] * x[col_idx[k]];
    y[gid] = sum;
}
)METAL";

// ---------------------------------------------------------------------------
// Timing helpers
// ---------------------------------------------------------------------------

static double ticks_to_seconds(uint64_t ticks)
{
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-9;
}

static double ticks_to_ms(uint64_t ticks)
{
    return ticks_to_seconds(ticks) * 1e3;
}

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

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------

static void cpu_spmv(const int* row_ptr, const int* col_idx,
                     const float* values, const float* x,
                     float* y, int num_rows)
{
    for (int row = 0; row < num_rows; ++row) {
        float sum = 0.0f;
        for (int k = row_ptr[row]; k < row_ptr[row + 1]; ++k)
            sum += values[k] * x[col_idx[k]];
        y[row] = sum;
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int num_rows    = 16384;
    int nnz_per_row = 32;
    const char* precision = "float";

    // Command-line fallback
    num_rows    = parseIntParam(argc, argv, "--num_rows", num_rows);
    nnz_per_row = parseIntParam(argc, argv, "--nnz_per_row", nnz_per_row);
    precision   = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_rows");
    if (env_val) num_rows = atoi(env_val);
    env_val = getenv("BENCH_PARAM_nnz_per_row");
    if (env_val) nnz_per_row = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int num_cols = num_rows;
    int nnz      = num_rows * nnz_per_row;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Rows: %d  |  NNZ_per_row: %d  |  Total NNZ: %d\n\n",
            num_rows, nnz_per_row, nnz);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from embedded source string
    // -------------------------------------------------------------------
    NSError* err = nil;
    NSString* metalSrc = [NSString stringWithUTF8String:kMetalSource];
    MTLCompileOptions* opts = [MTLCompileOptions new];
    id<MTLLibrary> library = [device newLibraryWithSource:metalSrc options:opts error:&err];
    if (!library) {
        fprintf(stderr, "Metal compile error: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"spmv_csr_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'spmv_csr_kernel' not found in library\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso =
        [device newComputePipelineStateWithFunction:fn error:&err];
    if (!pso) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    size_t row_ptr_bytes = (size_t)(num_rows + 1) * sizeof(int);
    size_t col_idx_bytes = (size_t)nnz            * sizeof(int);
    size_t val_bytes     = (size_t)nnz            * sizeof(float);
    size_t vec_bytes     = (size_t)num_rows        * sizeof(float);

    id<MTLBuffer> buf_row_ptr = [device newBufferWithLength:row_ptr_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_col_idx = [device newBufferWithLength:col_idx_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_values  = [device newBufferWithLength:val_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_x       = [device newBufferWithLength:vec_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_y       = [device newBufferWithLength:vec_bytes
                                                    options:MTLResourceStorageModeShared];

    // -------------------------------------------------------------------
    // Initialize CSR matrix and input vector
    // -------------------------------------------------------------------
    int*   h_row_ptr = (int*)  buf_row_ptr.contents;
    int*   h_col_idx = (int*)  buf_col_idx.contents;
    float* h_values  = (float*)buf_values.contents;
    float* h_x       = (float*)buf_x.contents;

    srand(42);
    for (int i = 0; i <= num_rows; ++i)
        h_row_ptr[i] = i * nnz_per_row;

    for (int k = 0; k < nnz; ++k) {
        h_col_idx[k] = rand() % num_cols;
        h_values[k]  = (float)rand() / (float)RAND_MAX;
    }
    for (int i = 0; i < num_rows; ++i)
        h_x[i] = (float)rand() / (float)RAND_MAX;

    uint32_t num_rows_u = (uint32_t)num_rows;

    NSUInteger tg_size = pso.maxTotalThreadsPerThreadgroup;
    if (tg_size > 256) tg_size = 256;

    auto dispatch_spmv = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_row_ptr offset:0 atIndex:0];
        [enc setBuffer:buf_col_idx offset:0 atIndex:1];
        [enc setBuffer:buf_values  offset:0 atIndex:2];
        [enc setBuffer:buf_x       offset:0 atIndex:3];
        [enc setBuffer:buf_y       offset:0 atIndex:4];
        [enc setBytes:&num_rows_u length:sizeof(num_rows_u) atIndex:5];
        [enc dispatchThreads:MTLSizeMake((NSUInteger)num_rows, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_spmv();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_spmv();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
        fprintf(stderr, "  iter %d: %.4f ms\n", i, times[i]);
    }

    // -------------------------------------------------------------------
    // Correctness check
    // -------------------------------------------------------------------
    std::vector<float> h_y_ref((size_t)num_rows);
    cpu_spmv(h_row_ptr, h_col_idx, h_values, h_x, h_y_ref.data(), num_rows);

    const float* h_y_gpu = (const float*)buf_y.contents;
    bool pass = true;
    int check_n = 32;
    for (int i = 0; i < check_n && i < num_rows; ++i) {
        float ref = h_y_ref[i];
        float rel = fabsf(h_y_gpu[i] - ref) / (fabsf(ref) + 1e-6f);
        if (rel > 1e-4f) {
            fprintf(stderr, "MISMATCH y[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    i, h_y_gpu[i], ref, rel);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (first %d elements): %s\n\n",
            check_n, pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;
    double total_ms = sum_ms;

    double data_bytes = (double)nnz * (sizeof(float) + sizeof(int))
                 + (double)(num_rows + 1) * sizeof(int)
                 + (double)num_rows * sizeof(float)
                 + (double)num_rows * sizeof(float);
    double gbps   = data_bytes / (avg_ms * 1e-3) / 1e9;
    double gflops = 2.0 * (double)nnz / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "GB/s: %.2f  GFLOPS: %.2f  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gbps, gflops, avg_ms, mn, mx);

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"spmv_csr_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_rows\":%d,\"nnz_per_row\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, num_rows, nnz_per_row, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f},"
           "{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gbps, gflops);

    return pass ? EXIT_SUCCESS : EXIT_FAILURE;

    } // @autoreleasepool
}
