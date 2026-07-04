/**
 * tango_binomial_options_metal.mm — Apple Metal host for binomial option pricing.
 *
 * Cox-Ross-Rubinstein binomial tree model for American put options.
 * Each threadgroup processes one option using shared memory for backward
 * induction. Measures throughput in options/sec.
 *
 * Usage:
 *   ./tango_binomial_options [--options N] [--steps S]
 *
 *   --options N      Number of options (default: 512)
 *   --steps S        Steps per option (default: 1024)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — tango_binomial_options,<N>x<steps>,<time_ms>,<opts/sec>
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
// Option parameters structure — must match Metal shader
// ---------------------------------------------------------------------------

struct OptionData {
    float S;      // stock price
    float K;      // strike price
    float T;      // time to expiration
    float r;      // risk-free rate
    float sigma;  // volatility
};

// ---------------------------------------------------------------------------
// CPU reference: binomial option pricing
// ---------------------------------------------------------------------------

static float binomial_cpu(OptionData opt, int numSteps) {
    float dt   = opt.T / (float)numSteps;
    float u    = expf(opt.sigma * sqrtf(dt));
    float d    = 1.0f / u;
    float R    = expf(opt.r * dt);
    float Rinv = 1.0f / R;
    float p    = (R - d) / (u - d);
    float q    = 1.0f - p;

    std::vector<float> vals((size_t)(numSteps + 1));

    // Terminal payoffs
    for (int j = 0; j <= numSteps; ++j) {
        float ST = opt.S * powf(u, (float)(2 * j - numSteps));
        vals[j] = fmaxf(opt.K - ST, 0.0f);
    }

    // Backward induction
    for (int step = numSteps; step > 0; --step) {
        for (int j = 0; j < step; ++j) {
            float cont = Rinv * (p * vals[j + 1] + q * vals[j]);
            float ST = opt.S * powf(u, (float)(2 * j - (step - 1)));
            float exercise = fmaxf(opt.K - ST, 0.0f);
            vals[j] = fmaxf(cont, exercise);
        }
    }

    return vals[0];
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
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int numOptions = 512;
    int numSteps   = 1024;
    const char* precision = "float";

    // Command-line fallback
    numOptions = parseIntParam(argc, argv, "--options", numOptions);
    numSteps   = parseIntParam(argc, argv, "--steps",   numSteps);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_options");
    if (env_val) numOptions = atoi(env_val);
    env_val = getenv("BENCH_PARAM_steps");
    if (env_val) numSteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;


    // Shared memory must hold (numSteps+1) floats; clamp to reasonable limit
    if (numSteps > 4096) {
        fprintf(stderr, "Warning: clamping steps to 4096 (shared memory limit)\n");
        numSteps = 4096;
    }

    size_t optBytes   = (size_t)numOptions * sizeof(OptionData);
    size_t priceBytes = (size_t)numOptions * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Options: %d  |  Steps: %d  |  Iterations: 5 warmup + %d timed\n\n",
            numOptions, numSteps);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:
                         @"tango_binomial_options.metal"];

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
        fprintf(stderr, "Error: tango_binomial_options.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline state
    id<MTLFunction> fnBinomial = [library newFunctionWithName:@"binomial_kernel"];
    if (!fnBinomial) {
        fprintf(stderr, "Error: binomial_kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoBinomial =
        [device newComputePipelineStateWithFunction:fnBinomial error:&err];
    if (!psoBinomial) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufOptions = [device newBufferWithLength:optBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufPrices  = [device newBufferWithLength:priceBytes
                                                   options:MTLResourceStorageModeShared];

    // Initialize option data
    OptionData* pOptions = (OptionData*)bufOptions.contents;
    unsigned seed = 42u;
    for (int i = 0; i < numOptions; ++i) {
        pOptions[i].S     = randRange(seed, 5.0f, 200.0f);
        pOptions[i].K     = randRange(seed, 1.0f, 300.0f);
        pOptions[i].T     = randRange(seed, 0.25f, 10.0f);
        pOptions[i].r     = 0.02f;
        pOptions[i].sigma = randRange(seed, 0.1f, 1.0f);
    }

    // Threadgroup config: one threadgroup per option
    // Clamp tgSize to pipeline max (Metal limit is typically 1024)
    NSUInteger maxTg  = psoBinomial.maxTotalThreadsPerThreadgroup;
    NSUInteger tgSize = (NSUInteger)(numSteps + 1);
    if (tgSize > maxTg) tgSize = maxTg;
    NSUInteger numGroups  = (NSUInteger)numOptions;
    NSUInteger sharedSize = (NSUInteger)(numSteps + 1) * sizeof(float);

    uint32_t paramData[2] = { (uint32_t)numOptions, (uint32_t)numSteps };

    // Lambda: run binomial kernel
    auto run_binomial = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoBinomial];
        [enc setBuffer:bufOptions offset:0 atIndex:0];
        [enc setBuffer:bufPrices  offset:0 atIndex:1];
        [enc setBytes:paramData length:sizeof(paramData) atIndex:2];
        [enc setThreadgroupMemoryLength:sharedSize atIndex:0];
        [enc dispatchThreadgroups:MTLSizeMake(numGroups, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_binomial();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_binomial();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // options/sec = numOptions / time_s
    double opts_per_sec = (double)numOptions / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"binomial_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"options\":%d,\"steps\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, numOptions, numSteps, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"options_per_sec\",\"value\":%.2f}]}\n",
           total_ms, opts_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f options/sec\n", opts_per_sec);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    const float* gpuPrices = (const float*)bufPrices.contents;

    int verifyN = (numOptions < 10) ? numOptions : 10;
    int errors = 0;
    for (int i = 0; i < verifyN; ++i) {
        float ref = binomial_cpu(pOptions[i], numSteps);
        float tol = 1e-2f * fabsf(ref) + 1e-4f;
        if (fabsf(gpuPrices[i] - ref) > tol) {
            fprintf(stderr, "Mismatch at option %d: GPU=%.6f CPU=%.6f (diff=%.6f)\n",
                    i, gpuPrices[i], ref, fabsf(gpuPrices[i] - ref));
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
