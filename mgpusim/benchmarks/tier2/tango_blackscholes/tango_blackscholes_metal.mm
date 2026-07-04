/**
 * tango_blackscholes_metal.mm — Apple Metal host for Black-Scholes pricing.
 *
 * Computes call and put option prices for N options using the
 * Black-Scholes formula. Measures throughput in M options/sec.
 *
 * Usage:
 *   ./tango_blackscholes [--size N]
 *
 *   --size N         Number of options (default: 4194304 = 4M)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — tango_blackscholes,<N>,<time_ms>,<Moptions_per_sec>
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
// CPU reference CND + Black-Scholes for verification
// ---------------------------------------------------------------------------

static float cnd_cpu(float d) {
    const float A1 = 0.31938153f;
    const float A2 = -0.356563782f;
    const float A3 = 1.781477937f;
    const float A4 = -1.821255978f;
    const float A5 = 1.330274429f;
    const float RSQRT2PI = 0.39894228040143267793994605993438f;

    float K = 1.0f / (1.0f + 0.2316419f * fabsf(d));
    float cnd_val = RSQRT2PI * expf(-0.5f * d * d) *
                    (K * (A1 + K * (A2 + K * (A3 + K * (A4 + K * A5)))));

    if (d > 0.0f) cnd_val = 1.0f - cnd_val;
    return cnd_val;
}

static void blackscholes_cpu(
    const float* S, const float* K, const float* T, const float* sigma,
    float r, float* callPrice, float* putPrice, int N)
{
    for (int i = 0; i < N; ++i) {
        float s     = S[i];
        float k     = K[i];
        float t     = T[i];
        float v     = sigma[i];
        float sqrtT = sqrtf(t);
        float d1    = (logf(s / k) + (r + 0.5f * v * v) * t) / (v * sqrtT);
        float d2    = d1 - v * sqrtT;
        float expRT = expf(-r * t);
        float cd1   = cnd_cpu(d1);
        float cd2   = cnd_cpu(d2);
        callPrice[i] = s * cd1 - k * expRT * cd2;
        putPrice[i]  = k * expRT * (1.0f - cd2) - s * (1.0f - cd1);
    }
}

// ---------------------------------------------------------------------------
// Simple deterministic pseudo-random in range [lo, hi]
// ---------------------------------------------------------------------------

static float randRange(unsigned& seed, float lo, float hi) {
    seed = seed * 1103515245u + 12345u;
    float t = (float)(seed & 0x7fffffffu) / (float)0x7fffffffu;
    return lo + t * (hi - lo);
}

// ---------------------------------------------------------------------------
// Parameters struct — matches Metal shader BSParams
// ---------------------------------------------------------------------------

struct BSParams {
    uint32_t N;
    float    r;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 4194304;
    int block_size = 256;
    const char* precision = "float";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int N     = size;

    const float r = 0.02f;
    size_t bytes = (size_t)N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Options: %d  |  Iterations: 5 warmup + %d timed\n\n",
            N);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:
                         @"tango_blackscholes.metal"];

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
        fprintf(stderr, "Error: tango_blackscholes.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline state
    id<MTLFunction> fnBS = [library newFunctionWithName:@"blackscholes_kernel"];
    if (!fnBS) {
        fprintf(stderr, "Error: blackscholes_kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoBS =
        [device newComputePipelineStateWithFunction:fnBS error:&err];
    if (!psoBS) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufS     = [device newBufferWithLength:bytes
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufK     = [device newBufferWithLength:bytes
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufT     = [device newBufferWithLength:bytes
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufSigma = [device newBufferWithLength:bytes
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufCall  = [device newBufferWithLength:bytes
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufPut   = [device newBufferWithLength:bytes
                                                 options:MTLResourceStorageModeShared];

    // Initialize data
    float* pS     = (float*)bufS.contents;
    float* pK     = (float*)bufK.contents;
    float* pT     = (float*)bufT.contents;
    float* pSigma = (float*)bufSigma.contents;

    unsigned seed = 42u;
    for (int i = 0; i < N; ++i) {
        pS[i]     = randRange(seed, 5.0f, 200.0f);
        pK[i]     = randRange(seed, 1.0f, 300.0f);
        pT[i]     = randRange(seed, 0.25f, 10.0f);
        pSigma[i] = randRange(seed, 0.1f, 1.0f);
    }

    // Threadgroup size
    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)N;

    BSParams params;
    params.N = (uint32_t)N;
    params.r = r;

    // Lambda: run Black-Scholes kernel
    auto run_bs = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoBS];
        [enc setBuffer:bufS     offset:0 atIndex:0];
        [enc setBuffer:bufK     offset:0 atIndex:1];
        [enc setBuffer:bufT     offset:0 atIndex:2];
        [enc setBuffer:bufSigma offset:0 atIndex:3];
        [enc setBuffer:bufCall  offset:0 atIndex:4];
        [enc setBuffer:bufPut   offset:0 atIndex:5];
        [enc setBytes:&params length:sizeof(BSParams) atIndex:6];
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
        run_bs();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_bs();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // M options/sec = N / time_s / 1e6
    double mopts = (double)N / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"blackscholes_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"moptions_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mopts);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f M options/sec\n", mopts);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    const float* gpuCall = (const float*)bufCall.contents;
    const float* gpuPut  = (const float*)bufPut.contents;

    int verifyN = (N < 10000) ? N : 10000;
    std::vector<float> refCall((size_t)verifyN);
    std::vector<float> refPut((size_t)verifyN);
    blackscholes_cpu(pS, pK, pT, pSigma, r, refCall.data(), refPut.data(), verifyN);

    int errors = 0;
    for (int i = 0; i < verifyN; ++i) {
        float tol = 1e-3f * (fabsf(refCall[i]) + fabsf(refPut[i])) + 1e-5f;
        if (fabsf(gpuCall[i] - refCall[i]) > tol ||
            fabsf(gpuPut[i]  - refPut[i])  > tol) {
            if (errors < 10) {
                fprintf(stderr,
                    "Mismatch at %d: GPU call=%.6f put=%.6f, CPU call=%.6f put=%.6f\n",
                    i, gpuCall[i], gpuPut[i], refCall[i], refPut[i]);
            }
            errors++;
        }
    }

    if (errors > 0)
        fprintf(stderr, "FAIL: %d errors out of %d verified\n", errors, verifyN);
    else
        fprintf(stderr, "PASS\n");

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
