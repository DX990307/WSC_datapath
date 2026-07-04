/**
 * shoc_fft_metal.mm — Apple Metal host for the SHOC FFT benchmark.
 *
 * Cooley-Tukey radix-2 iterative FFT on N complex elements (float2).
 * Measures throughput in GFLOPS = 5*N*log2(N) / time_s / 1e9.
 *
 * Usage:
 *   ./shoc_fft [--size N]
 *
 *   --size N         FFT size (must be power of 2, default: 1048576)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): JSON-lines protocol
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
#include <cfloat>
#include <vector>
#include <mach/mach_time.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

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
// Utility
// ---------------------------------------------------------------------------

static bool isPowerOfTwo(int n) { return n > 0 && (n & (n - 1)) == 0; }

static int ilog2(int n) {
    int r = 0;
    while ((1 << r) < n) ++r;
    return r;
}

// ---------------------------------------------------------------------------
// CPU reference FFT for verification
// ---------------------------------------------------------------------------

struct cpx { float x, y; };

static void fft_cpu(cpx* data, int N) {
    int log2N = ilog2(N);

    // Bit-reversal permutation
    for (int i = 0; i < N; ++i) {
        int rev = 0, temp = i;
        for (int b = 0; b < log2N; ++b) {
            rev = (rev << 1) | (temp & 1);
            temp >>= 1;
        }
        if (rev > i) {
            cpx tmp = data[i];
            data[i] = data[rev];
            data[rev] = tmp;
        }
    }

    // Butterfly stages
    for (int s = 0; s < log2N; ++s) {
        int m      = 1 << (s + 1);
        int half_m = 1 << s;
        for (int k = 0; k < N; k += m) {
            for (int j = 0; j < half_m; ++j) {
                float angle = -2.0f * (float)M_PI * (float)j / (float)m;
                float w_re = cosf(angle);
                float w_im = sinf(angle);

                cpx u = data[k + j];
                cpx v = data[k + j + half_m];

                float t_re = w_re * v.x - w_im * v.y;
                float t_im = w_re * v.y + w_im * v.x;

                data[k + j].x              = u.x + t_re;
                data[k + j].y              = u.y + t_im;
                data[k + j + half_m].x     = u.x - t_re;
                data[k + j + half_m].y     = u.y - t_im;
            }
        }
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    int N     = parseIntParam(argc, argv, "--size", 1048576);
    const char* precision = "float";
    int batch_count = 1;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    env_val = getenv("BENCH_PARAM_batch_count");
    if (env_val) batch_count = atoi(env_val);
    int num_warmup = 0;

    if (!isPowerOfTwo(N)) {
        fprintf(stderr, "Error: N=%d must be a power of 2\n", N);
        return EXIT_FAILURE;
    }

    int log2N = ilog2(N);
    size_t bytes = (size_t)N * sizeof(float) * 2;  // float2 = 2 floats

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "FFT size: %d (2^%d) warmup + %d timed\n\n",
            N, log2N, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"shoc_fft.metal"];

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
        fprintf(stderr, "Error: shoc_fft.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states for both kernels
    id<MTLFunction> fnBitRev = [library newFunctionWithName:@"bit_reverse_kernel"];
    id<MTLFunction> fnButterfly = [library newFunctionWithName:@"fft_butterfly_kernel"];
    if (!fnBitRev || !fnButterfly) {
        fprintf(stderr, "Error: kernel function(s) not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoBitRev =
        [device newComputePipelineStateWithFunction:fnBitRev error:&err];
    if (!psoBitRev) {
        fprintf(stderr, "Error creating bit_reverse pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoButterfly =
        [device newComputePipelineStateWithFunction:fnButterfly error:&err];
    if (!psoButterfly) {
        fprintf(stderr, "Error creating butterfly pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufData = [device newBufferWithLength:bytes
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOrig = [device newBufferWithLength:bytes
                                               options:MTLResourceStorageModeShared];

    // Initialize original data
    float* origPtr = (float*)bufOrig.contents;
    for (int i = 0; i < N; ++i) {
        origPtr[i * 2]     = (float)(i % 1024) * 0.001f;  // real
        origPtr[i * 2 + 1] = 0.0f;                         // imag
    }

    // Threadgroup size
    NSUInteger tgSize = 256;
    NSUInteger gridN      = (NSUInteger)N;
    NSUInteger gridNhalf  = (NSUInteger)(N / 2);

    // Helper: run bit-reverse step
    auto run_bit_reverse = [&]() {
        uint32_t bitRevParams[2] = { (uint32_t)N, (uint32_t)log2N };
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:psoBitRev];
        [enc setBuffer:bufData offset:0 atIndex:0];
        [enc setBytes:bitRevParams length:sizeof(bitRevParams) atIndex:1];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Helper: run all butterfly stages
    auto run_butterfly = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        for (int s = 0; s < log2N; ++s) {
            uint32_t bflyParams[2] = { (uint32_t)N, (uint32_t)s };
            [enc setComputePipelineState:psoButterfly];
            [enc setBuffer:bufData offset:0 atIndex:0];
            [enc setBytes:bflyParams length:sizeof(bflyParams) atIndex:1];
            [enc dispatchThreads:MTLSizeMake(gridNhalf, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
        }
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Lambda: run full FFT
    auto run_fft = [&]() {
        memcpy(bufData.contents, bufOrig.contents, bytes);
        run_bit_reverse();
        run_butterfly();
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_fft();
    }

    // -------------------------------------------------------------------
    // Timed iterations — time bit_reverse and butterfly separately
    // -------------------------------------------------------------------
    std::vector<double> br_times(1);
    std::vector<double> bf_times(1);
    for (int i = 0; i < 1; ++i) {
        memcpy(bufData.contents, bufOrig.contents, bytes);

        uint64_t t0 = mach_absolute_time();
        run_bit_reverse();
        uint64_t t1 = mach_absolute_time();
        run_butterfly();
        uint64_t t2 = mach_absolute_time();

        br_times[i] = ticks_to_ms(t1 - t0);
        bf_times[i] = ticks_to_ms(t2 - t1);
    }

    double br_sum = 0.0, bf_sum = 0.0;
    for (int i = 0; i < 1; ++i) {
        br_sum += br_times[i];
        bf_sum += bf_times[i];
    }
    double br_avg_ms = br_sum / 1;
    double bf_avg_ms = bf_sum / 1;
    double total_avg_ms = br_avg_ms + bf_avg_ms;

    // GFLOPS = 5 * N * log2(N) / time_s / 1e9
    double flops  = 5.0 * (double)N * (double)log2N;
    double gflops = flops / (total_avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"bit_reverse_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"precision\":\"%s\",\"batch_count\":%d}}\n",
           br_avg_ms, N, precision, batch_count);
    printf("{\"type\":\"kernel\",\"name\":\"fft_butterfly_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"precision\":\"%s\",\"batch_count\":%d}}\n",
           bf_avg_ms, N, precision, batch_count);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.4f}]}\n",
           total_avg_ms, gflops);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms (bit_reverse: %.4f ms, butterfly: %.4f ms)\n",
            total_avg_ms, br_avg_ms, bf_avg_ms);
    fprintf(stderr, "Performance:  %.4f GFLOPS\n", gflops);

    // -------------------------------------------------------------------
    // Verification (only for small sizes)
    // -------------------------------------------------------------------
    if (N <= 8192) {
        const float* gpuData = (const float*)bufData.contents;

        // CPU reference
        std::vector<cpx> ref((size_t)N);
        const float* origData = (const float*)bufOrig.contents;
        for (int i = 0; i < N; ++i) {
            ref[i].x = origData[i * 2];
            ref[i].y = origData[i * 2 + 1];
        }
        fft_cpu(ref.data(), N);

        int errors = 0;
        for (int i = 0; i < N; ++i) {
            float diff_re = fabsf(gpuData[i * 2]     - ref[i].x);
            float diff_im = fabsf(gpuData[i * 2 + 1] - ref[i].y);
            float tol = 1e-2f * (fabsf(ref[i].x) + fabsf(ref[i].y)) + 1e-4f;
            if (diff_re > tol || diff_im > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=(%.6f,%.6f) CPU=(%.6f,%.6f)\n",
                            i, gpuData[i * 2], gpuData[i * 2 + 1],
                            ref[i].x, ref[i].y);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
        else
            fprintf(stderr, "PASS\n");
    } else {
        // Parseval's check
        const float* gpuData = (const float*)bufData.contents;

        double energy_time = 0.0;
        for (int i = 0; i < N; ++i) {
            energy_time += (double)origPtr[i * 2]     * origPtr[i * 2]
                         + (double)origPtr[i * 2 + 1] * origPtr[i * 2 + 1];
        }

        double energy_freq = 0.0;
        for (int i = 0; i < N; ++i) {
            energy_freq += (double)gpuData[i * 2]     * gpuData[i * 2]
                         + (double)gpuData[i * 2 + 1] * gpuData[i * 2 + 1];
        }

        double ratio = energy_freq / ((double)N * energy_time);
        fprintf(stderr, "Parseval ratio: %.6f (expected ~1.0)\n", ratio);
        if (fabs(ratio - 1.0) < 0.01)
            fprintf(stderr, "PASS (Parseval)\n");
        else
            fprintf(stderr, "FAIL (Parseval): ratio = %.6f\n", ratio);
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
