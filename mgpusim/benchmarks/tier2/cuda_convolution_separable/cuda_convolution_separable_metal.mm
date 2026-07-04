/**
 * cuda_convolution_separable_metal.mm — Apple Metal host for separable 2D convolution.
 *
 * Two-pass separable 2D convolution: row pass + column pass.
 * Measures throughput in GB/s = 2 * W * H * sizeof(float) / time_s / 1e9.
 *
 * Usage:
 *   ./cuda_convolution_separable [--width W] [--height H] [--radius R]
 *
 *   --width W         Image width  (default: 4096)
 *   --height H        Image height (default: 4096)
 *   --radius R        Kernel radius (default: 8, filter width = 2*R+1)
 *   --iterations I    Timed iterations (default: 5)
 *
 * Output (stdout): CSV — cuda_convolution_separable,<WxH>,<time_ms>,<GBs>
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
// CPU reference: separable 2D convolution
// ---------------------------------------------------------------------------

static void conv_separable_cpu(const float* input, float* output,
                               int width, int height,
                               const float* kernel_weights, int radius) {
    std::vector<float> temp((size_t)width * height, 0.0f);

    // Row pass
    for (int r = 0; r < height; ++r) {
        for (int c = 0; c < width; ++c) {
            float sum = 0.0f;
            for (int k = -radius; k <= radius; ++k) {
                int cc = c + k;
                float val = (cc >= 0 && cc < width) ? input[r * width + cc] : 0.0f;
                sum += val * kernel_weights[radius + k];
            }
            temp[r * width + c] = sum;
        }
    }

    // Column pass
    for (int r = 0; r < height; ++r) {
        for (int c = 0; c < width; ++c) {
            float sum = 0.0f;
            for (int k = -radius; k <= radius; ++k) {
                int rr = r + k;
                float val = (rr >= 0 && rr < height) ? temp[rr * width + c] : 0.0f;
                sum += val * kernel_weights[radius + k];
            }
            output[r * width + c] = sum;
        }
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int width  = 4096;
    int height = 4096;
    int radius = 8;

    // Command-line fallback
    width  = parseIntParam(argc, argv, "--width",  width);
    height = parseIntParam(argc, argv, "--height", height);
    radius = parseIntParam(argc, argv, "--radius", radius);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_width");
    if (env_val) width = atoi(env_val);
    env_val = getenv("BENCH_PARAM_height");
    if (env_val) height = atoi(env_val);
    env_val = getenv("BENCH_PARAM_radius");
    if (env_val) radius = atoi(env_val);
    int num_warmup = 0;


    int klen = 2 * radius + 1;
    size_t imageBytes = (size_t)width * height * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Image: %dx%d  |  Kernel radius: %d (%d-tap)  |  "
                    "Iterations: 5 warmup + %d timed\n\n",
            width, height, radius, klen);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:
                         @"cuda_convolution_separable.metal"];

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
        fprintf(stderr, "Error: cuda_convolution_separable.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states
    id<MTLFunction> fnRow = [library newFunctionWithName:@"convolution_row_kernel"];
    id<MTLFunction> fnCol = [library newFunctionWithName:@"convolution_col_kernel"];
    if (!fnRow || !fnCol) {
        fprintf(stderr, "Error: kernel function(s) not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoRow =
        [device newComputePipelineStateWithFunction:fnRow error:&err];
    if (!psoRow) {
        fprintf(stderr, "Error creating row pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoCol =
        [device newComputePipelineStateWithFunction:fnCol error:&err];
    if (!psoCol) {
        fprintf(stderr, "Error creating col pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate Gaussian kernel weights
    // -------------------------------------------------------------------
    std::vector<float> h_kernel((size_t)klen);
    {
        float sigma = (float)radius / 3.0f;
        float sum = 0.0f;
        for (int i = 0; i < klen; ++i) {
            float x = (float)(i - radius);
            h_kernel[i] = expf(-x * x / (2.0f * sigma * sigma));
            sum += h_kernel[i];
        }
        for (int i = 0; i < klen; ++i) h_kernel[i] /= sum;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufInput   = [device newBufferWithLength:imageBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufTemp    = [device newBufferWithLength:imageBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOutput  = [device newBufferWithLength:imageBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufKernel  = [device newBufferWithLength:klen * sizeof(float)
                                                   options:MTLResourceStorageModeShared];

    // Initialize input data
    float* inputPtr = (float*)bufInput.contents;
    for (int i = 0; i < width * height; ++i) {
        inputPtr[i] = (float)(i % 256) / 255.0f;
    }

    // Copy kernel weights
    memcpy(bufKernel.contents, h_kernel.data(), klen * sizeof(float));

    // Threadgroup and grid sizes
    // Row kernel: threadgroup(128, 8), grid(width, height)
    MTLSize rowTG   = MTLSizeMake(128, 8, 1);
    MTLSize rowGrid = MTLSizeMake((NSUInteger)width, (NSUInteger)height, 1);

    // Col kernel: threadgroup(16, 16), grid(width, height)
    MTLSize colTG   = MTLSizeMake(16, 16, 1);
    MTLSize colGrid = MTLSizeMake((NSUInteger)width, (NSUInteger)height, 1);

    uint32_t params[3] = { (uint32_t)width, (uint32_t)height, (uint32_t)radius };

    // Lambda: run full separable convolution
    auto run_conv = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        // Row pass: input -> temp
        [enc setComputePipelineState:psoRow];
        [enc setBuffer:bufInput  offset:0 atIndex:0];
        [enc setBuffer:bufTemp   offset:0 atIndex:1];
        [enc setBuffer:bufKernel offset:0 atIndex:2];
        [enc setBytes:params length:sizeof(params) atIndex:3];
        [enc dispatchThreads:rowGrid threadsPerThreadgroup:rowTG];

        // Col pass: temp -> output
        [enc setComputePipelineState:psoCol];
        [enc setBuffer:bufTemp   offset:0 atIndex:0];
        [enc setBuffer:bufOutput offset:0 atIndex:1];
        [enc setBuffer:bufKernel offset:0 atIndex:2];
        [enc setBytes:params length:sizeof(params) atIndex:3];
        [enc dispatchThreads:colGrid threadsPerThreadgroup:colTG];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_conv();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_conv();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double tsum = 0.0;
    for (int i = 0; i < 1; ++i) tsum += times[i];
    double avg_ms = tsum / 1;

    // GB/s = 2 * W * H * sizeof(float) / time_s / 1e9
    double data_bytes = 2.0 * (double)width * (double)height * sizeof(float);
    double gbs = data_bytes / (avg_ms * 1e-3) / 1e9;

    double total_ms = tsum;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"convolution_row_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"radius\":%d}}\n",
           avg_ms, width, height, radius);

    printf("{\"type\":\"kernel\",\"name\":\"convolution_col_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"radius\":%d}}\n",
           avg_ms, width, height, radius);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    int verify_w = (width  <= 512) ? width  : 512;
    int verify_h = (height <= 512) ? height : 512;

    {
        const float* gpuOutput = (const float*)bufOutput.contents;

        std::vector<float> ref((size_t)width * height);
        conv_separable_cpu(inputPtr, ref.data(), width, height,
                           h_kernel.data(), radius);

        int errors = 0;
        for (int r = 0; r < verify_h; ++r) {
            for (int c = 0; c < verify_w; ++c) {
                int idx = r * width + c;
                float diff = fabsf(gpuOutput[idx] - ref[idx]);
                float tol = 1e-3f * fabsf(ref[idx]) + 1e-5f;
                if (diff > tol) {
                    if (errors < 10) {
                        fprintf(stderr, "Mismatch at (%d,%d): GPU=%.6f CPU=%.6f diff=%.6f\n",
                                r, c, gpuOutput[idx], ref[idx], diff);
                    }
                    errors++;
                }
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in %dx%d verification region\n",
                    errors, verify_w, verify_h);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
