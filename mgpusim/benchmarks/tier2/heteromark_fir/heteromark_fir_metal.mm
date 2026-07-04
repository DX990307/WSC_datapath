/**
 * heteromark_fir_metal.mm — Apple Metal host for FIR filter benchmark.
 *
 * 1D FIR (Finite Impulse Response) convolution:
 *   output[i] = sum(coeff[k] * input[i - k]) for k = 0..NUM_TAPS-1
 *
 * Each thread computes one output sample. Filter coefficients loaded
 * into threadgroup memory for performance.
 *
 * Usage:
 *   ./heteromark_fir [--size N]
 *
 *   --size N         Number of input samples (default: 1048576)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — heteromark_fir,<N>,<time_ms>,<GBs>
 * Output (stderr): human-readable results
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
// Constants
// ---------------------------------------------------------------------------

#define NUM_TAPS 128

// ---------------------------------------------------------------------------
// CPU reference: FIR filter
// ---------------------------------------------------------------------------

static void fir_cpu(const float* input, float* output, const float* coeff,
                     int num_samples, int num_taps) {
    for (int i = 0; i < num_samples; ++i) {
        float sum = 0.0f;
        for (int k = 0; k < num_taps; ++k) {
            int in_idx = i - k;
            if (in_idx >= 0) {
                sum += coeff[k] * input[in_idx];
            }
        }
        output[i] = sum;
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 1048576;
    int num_taps   = 64;
    int block_size = 256;

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    num_taps   = parseIntParam(argc, argv, "--num_taps", num_taps);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_taps");
    if (env_val) num_taps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    int N     = size;
    int taps  = num_taps;

    size_t input_bytes  = (size_t)N * sizeof(float);
    size_t output_bytes = (size_t)N * sizeof(float);
    size_t coeff_bytes  = (size_t)taps * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "FIR filter  |  Samples: %d  |  Taps: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            N, taps, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"heteromark_fir.metal"];

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
        fprintf(stderr, "Error: heteromark_fir.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnFIR = [library newFunctionWithName:@"fir_filter_kernel"];
    if (!fnFIR) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoFIR =
        [device newComputePipelineStateWithFunction:fnFIR error:&err];
    if (!psoFIR) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufInput  = [device newBufferWithLength:input_bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOutput = [device newBufferWithLength:output_bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufCoeff  = [device newBufferWithLength:coeff_bytes
                                                  options:MTLResourceStorageModeShared];

    // Initialize input signal
    float* inputPtr = (float*)bufInput.contents;
    for (int i = 0; i < N; ++i) {
        inputPtr[i] = sinf((float)i * 0.01f) + 0.5f;
    }

    // Initialize filter coefficients (simple low-pass-like)
    float* coeffPtr = (float*)bufCoeff.contents;
    float coeff_sum = 0.0f;
    for (int k = 0; k < taps; ++k) {
        coeffPtr[k] = 1.0f / (float)(k + 1);
        coeff_sum += coeffPtr[k];
    }
    for (int k = 0; k < taps; ++k) {
        coeffPtr[k] /= coeff_sum;
    }

    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)N;

    // Lambda: run FIR filter
    auto run_fir = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        uint32_t firParams[2] = { (uint32_t)N, (uint32_t)taps };
        [enc setComputePipelineState:psoFIR];
        [enc setBuffer:bufInput  offset:0 atIndex:0];
        [enc setBuffer:bufOutput offset:0 atIndex:1];
        [enc setBuffer:bufCoeff  offset:0 atIndex:2];
        [enc setBytes:firParams length:sizeof(firParams) atIndex:3];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_fir();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_fir();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s = (input_size + output_size) * sizeof(float) / time_s / 1e9
    double total_bytes = (double)(input_bytes + output_bytes);
    double gbs = total_bytes / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"fir_filter_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"num_taps\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, size, num_taps, block_size);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification (compare first 1024 samples with CPU reference)
    // -------------------------------------------------------------------
    {
        int verify_n = (N < 1024) ? N : 1024;
        const float* gpuOutput = (const float*)bufOutput.contents;

        float* h_ref = (float*)malloc(output_bytes);
        fir_cpu(inputPtr, h_ref, coeffPtr, verify_n, taps);

        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            float diff = fabsf(gpuOutput[i] - h_ref[i]);
            float tol = 1e-4f * fabsf(h_ref[i]) + 1e-6f;
            if (diff > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=%.6f CPU=%.6f diff=%.2e\n",
                            i, gpuOutput[i], h_ref[i], diff);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in first %d samples\n",
                    errors, verify_n);
        else
            fprintf(stderr, "PASS\n");

        free(h_ref);
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
