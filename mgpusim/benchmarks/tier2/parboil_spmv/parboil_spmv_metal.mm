/**
 * parboil_spmv_metal.mm — Apple Metal host for the Parboil SpMV benchmark.
 *
 * CSR Sparse Matrix-Vector Multiplication: y = A * x
 * Each GPU thread computes one row of the output vector.
 *
 * The Metal compute kernel lives in parboil_spmv.metal and is compiled at
 * runtime from source (loaded from the same directory as the binary).
 *
 * Usage:
 *   ./parboil_spmv [--num_rows N] [--avg_nnz_per_row K]
 *
 *   --num_rows N         Number of matrix rows           (default: 65536)
 *   --avg_nnz_per_row K  Average non-zeros per row       (default: 10)
 *   --iterations I       Timed kernel launches           (default: 10)
 *
 * Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
 * Output (stderr): GFLOPS, verification PASS/FAIL
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
#include <cfloat>
#include <vector>
#include <mach/mach_time.h>

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

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal)
{
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
// CSV output
// ---------------------------------------------------------------------------

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


// ---------------------------------------------------------------------------
// Sparse matrix generation (CSR)
// ---------------------------------------------------------------------------

static void generate_csr_matrix(int num_rows, int avg_nnz, int seed,
                                  std::vector<uint32_t>& row_ptr,
                                  std::vector<uint32_t>& col_idx,
                                  std::vector<float>&    values)
{
    srand(seed);

    // Determine nnz per row
    std::vector<int> nnz_per_row(num_rows);
    int total_nnz = 0;
    for (int i = 0; i < num_rows; ++i) {
        int nnz = 1 + (rand() % (2 * avg_nnz - 1));
        if (nnz > num_rows) nnz = num_rows;
        nnz_per_row[i] = nnz;
        total_nnz += nnz;
    }

    row_ptr.resize((size_t)(num_rows + 1));
    col_idx.resize((size_t)total_nnz);
    values .resize((size_t)total_nnz);

    // Build row_ptr
    row_ptr[0] = 0;
    for (int i = 0; i < num_rows; ++i)
        row_ptr[i + 1] = row_ptr[i] + (uint32_t)nnz_per_row[i];

    // Fill column indices and values
    for (int i = 0; i < num_rows; ++i) {
        int start = (int)row_ptr[i];
        int nnz   = nnz_per_row[i];
        int step  = (num_rows > nnz) ? (num_rows / nnz) : 1;
        for (int j = 0; j < nnz; ++j) {
            col_idx[start + j] = (uint32_t)(((long long)j * step + (rand() % step)) % num_rows);
            values [start + j] = (float)(rand() % 100) / 100.0f + 0.01f;
        }
        // Insertion sort to keep columns ordered
        for (int j = 1; j < nnz; ++j) {
            uint32_t kc = col_idx[start + j];
            float    kv = values [start + j];
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
}

// ---------------------------------------------------------------------------
// CPU reference (single row)
// ---------------------------------------------------------------------------

static float cpu_spmv_row(const std::vector<uint32_t>& row_ptr,
                           const std::vector<uint32_t>& col_idx,
                           const std::vector<float>&    values,
                           const std::vector<float>&    x,
                           int row)
{
    float sum = 0.0f;
    for (uint32_t j = row_ptr[row]; j < row_ptr[row + 1]; ++j)
        sum += values[j] * x[col_idx[j]];
    return sum;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int num_rows   = 65536;
    int avg_nnz    = 10;
    int block_size = 256;
    int seed       = 42;

    // Command-line args as fallback
    num_rows = parseIntParam(argc, argv, "--num_rows",         num_rows);
    avg_nnz  = parseIntParam(argc, argv, "--avg_nnz_per_row",  avg_nnz);
    block_size = parseIntParam(argc, argv, "--block_size",     block_size);
    seed     = parseIntParam(argc, argv, "--seed",             seed);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_rows");
    if (env_val) num_rows = atoi(env_val);
    env_val = getenv("BENCH_PARAM_avg_nnz_per_row");
    if (env_val) avg_nnz = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    // -------------------------------------------------------------------
    // Generate sparse matrix
    // -------------------------------------------------------------------
    std::vector<uint32_t> h_row_ptr;
    std::vector<uint32_t> h_col_idx;
    std::vector<float>    h_values;
    generate_csr_matrix(num_rows, avg_nnz, seed, h_row_ptr, h_col_idx, h_values);
    int total_nnz = (int)h_col_idx.size();

    // Generate dense x vector
    std::vector<float> h_x((size_t)num_rows);
    srand(seed + 1);
    for (int i = 0; i < num_rows; ++i)
        h_x[i] = (float)(rand() % 100) / 100.0f + 0.01f;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Rows: %d  |  NNZ: %d\n\n",
            num_rows, total_nnz);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"parboil_spmv.metal"];

    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString* metalSrc = [NSString stringWithContentsOfFile:srcPath
                                                       encoding:NSUTF8StringEncoding
                                                          error:nil];
        MTLCompileOptions* opts = [MTLCompileOptions new];
        library = [device newLibraryWithSource:metalSrc options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: parboil_spmv.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Build compute pipeline
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
    // Allocate GPU buffers (Shared storage for easy CPU init/readback)
    // -------------------------------------------------------------------
    size_t row_ptr_bytes = (size_t)(num_rows + 1) * sizeof(uint32_t);
    size_t col_idx_bytes = (size_t)total_nnz      * sizeof(uint32_t);
    size_t val_bytes     = (size_t)total_nnz      * sizeof(float);
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

    // Copy data to shared buffers
    memcpy(buf_row_ptr.contents, h_row_ptr.data(), row_ptr_bytes);
    memcpy(buf_col_idx.contents, h_col_idx.data(), col_idx_bytes);
    memcpy(buf_values.contents,  h_values.data(),  val_bytes);
    memcpy(buf_x.contents,       h_x.data(),       vec_bytes);

    uint32_t num_rows_u = (uint32_t)num_rows;

    // Threadgroup size
    NSUInteger tg_size = pso.maxTotalThreadsPerThreadgroup;
    if (tg_size > 256) tg_size = 256;

    // Lambda: encode and commit one SpMV dispatch
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
    }

    // Compute statistics
    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum_t / 1;
    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    char size_str[64];
    snprintf(size_str, sizeof(size_str), "%d", num_rows);

    BenchResult r;
    r.kernel_name  = "parboil_spmv";
    r.problem_size = size_str;
    r.iterations   = 1;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

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

    // -------------------------------------------------------------------
    // Verification: compare GPU output to CPU reference for sample rows
    // -------------------------------------------------------------------
    const float* h_y_gpu = (const float*)buf_y.contents;

    int check_rows = (num_rows < 100) ? num_rows : 100;
    int step       = num_rows / check_rows;
    int errors     = 0;
    for (int ri = 0; ri < check_rows; ++ri) {
        int   row   = ri * step;
        float ref   = cpu_spmv_row(h_row_ptr, h_col_idx, h_values, h_x, row);
        float got   = h_y_gpu[row];
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

    return (errors > 0) ? EXIT_FAILURE : EXIT_SUCCESS;

    } // @autoreleasepool
}
