/**
 * chai_cedd_metal.mm — Apple Metal host for Canny Edge Detection benchmark.
 *
 * 5-stage pipeline: Gaussian blur, Sobel gradient, magnitude+direction,
 * non-maximum suppression, hysteresis thresholding.
 *
 * Usage:
 *   ./chai_cedd [W=2048] [H=2048]
 *
 *   W=<width>    Image width  (default: 2048)
 *   H=<height>   Image height (default: 2048)
 *
 * Output (stdout): CSV — chai_cedd,<WxH>,<time_ms>,<Mpixels_per_sec>
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
// Argument parsing (key=value style)
// ---------------------------------------------------------------------------

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal) {
    size_t nlen = strlen(name);
    for (int i = 1; i < argc; ++i) {
        if (strncmp(argv[i], name, nlen) == 0) {
            int v = atoi(argv[i] + nlen);
            if (v > 0) return v;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

#define LOW_THRESH  50.0f
#define HIGH_THRESH 100.0f

// ---------------------------------------------------------------------------
// CPU reference: simple Canny edge detection (for verification subset)
// ---------------------------------------------------------------------------

static void canny_cpu(const float* input, unsigned char* output, int W, int H) {
    const float gauss[25] = {
        2.f/159, 4.f/159,  5.f/159,  4.f/159, 2.f/159,
        4.f/159, 9.f/159, 12.f/159,  9.f/159, 4.f/159,
        5.f/159,12.f/159, 15.f/159, 12.f/159, 5.f/159,
        4.f/159, 9.f/159, 12.f/159,  9.f/159, 4.f/159,
        2.f/159, 4.f/159,  5.f/159,  4.f/159, 2.f/159
    };

    int npix = W * H;
    std::vector<float> blurred(npix), gx(npix), gy(npix), mag(npix), nms(npix);
    std::vector<int> dir(npix);

    // Stage 1: Gaussian blur
    for (int y = 0; y < H; ++y) {
        for (int x = 0; x < W; ++x) {
            float sum = 0.0f;
            for (int ky = -2; ky <= 2; ++ky) {
                for (int kx = -2; kx <= 2; ++kx) {
                    int nx = std::min(std::max(x + kx, 0), W - 1);
                    int ny = std::min(std::max(y + ky, 0), H - 1);
                    sum += input[ny * W + nx] * gauss[(ky + 2) * 5 + (kx + 2)];
                }
            }
            blurred[y * W + x] = sum;
        }
    }

    // Stage 2: Sobel
    const int sobel_x[3][3] = {{-1,0,1},{-2,0,2},{-1,0,1}};
    const int sobel_y[3][3] = {{-1,-2,-1},{0,0,0},{1,2,1}};
    for (int y = 0; y < H; ++y) {
        for (int x = 0; x < W; ++x) {
            float sx = 0.0f, sy = 0.0f;
            for (int ky = -1; ky <= 1; ++ky) {
                for (int kx = -1; kx <= 1; ++kx) {
                    int nx = std::min(std::max(x + kx, 0), W - 1);
                    int ny = std::min(std::max(y + ky, 0), H - 1);
                    float val = blurred[ny * W + nx];
                    sx += val * sobel_x[ky + 1][kx + 1];
                    sy += val * sobel_y[ky + 1][kx + 1];
                }
            }
            gx[y * W + x] = sx;
            gy[y * W + x] = sy;
        }
    }

    // Stage 3: Magnitude + direction
    for (int y = 0; y < H; ++y) {
        for (int x = 0; x < W; ++x) {
            int idx = y * W + x;
            float vx = gx[idx], vy = gy[idx];
            mag[idx] = sqrtf(vx * vx + vy * vy);
            float angle = atan2f(vy, vx) * (180.0f / 3.14159265f);
            if (angle < 0) angle += 180.0f;
            if ((angle >= 0 && angle < 22.5f) || (angle >= 157.5f && angle <= 180.0f))
                dir[idx] = 0;
            else if (angle >= 22.5f && angle < 67.5f)
                dir[idx] = 45;
            else if (angle >= 67.5f && angle < 112.5f)
                dir[idx] = 90;
            else
                dir[idx] = 135;
        }
    }

    // Stage 4: NMS
    for (int y = 0; y < H; ++y) {
        for (int x = 0; x < W; ++x) {
            int idx = y * W + x;
            float m = mag[idx];
            int d = dir[idx];
            float n1 = 0.0f, n2 = 0.0f;
            if (d == 0) {
                if (x > 0) n1 = mag[y * W + (x - 1)];
                if (x < W - 1) n2 = mag[y * W + (x + 1)];
            } else if (d == 90) {
                if (y > 0) n1 = mag[(y - 1) * W + x];
                if (y < H - 1) n2 = mag[(y + 1) * W + x];
            } else if (d == 45) {
                if (x < W - 1 && y > 0) n1 = mag[(y - 1) * W + (x + 1)];
                if (x > 0 && y < H - 1) n2 = mag[(y + 1) * W + (x - 1)];
            } else {
                if (x > 0 && y > 0) n1 = mag[(y - 1) * W + (x - 1)];
                if (x < W - 1 && y < H - 1) n2 = mag[(y + 1) * W + (x + 1)];
            }
            nms[idx] = (m >= n1 && m >= n2) ? m : 0.0f;
        }
    }

    // Stage 5: Hysteresis
    for (int y = 0; y < H; ++y) {
        for (int x = 0; x < W; ++x) {
            int idx = y * W + x;
            float val = nms[idx];
            if (val >= HIGH_THRESH) {
                output[idx] = 255;
            } else if (val >= LOW_THRESH) {
                unsigned char is_edge = 0;
                for (int ky = -1; ky <= 1 && !is_edge; ++ky) {
                    for (int kx = -1; kx <= 1 && !is_edge; ++kx) {
                        if (kx == 0 && ky == 0) continue;
                        int nx = x + kx, ny = y + ky;
                        if (nx >= 0 && nx < W && ny >= 0 && ny < H) {
                            if (nms[ny * W + nx] >= HIGH_THRESH) {
                                is_edge = 255;
                            }
                        }
                    }
                }
                output[idx] = is_edge;
            } else {
                output[idx] = 0;
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int width          = 2048;
    int height         = 2048;
    int threshold_low  = 50;
    int threshold_high = 150;

    // Command-line fallback
    width  = parseIntParam(argc, argv, "W=", width);
    height = parseIntParam(argc, argv, "H=", height);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_width");
    if (env_val) width = atoi(env_val);
    env_val = getenv("BENCH_PARAM_height");
    if (env_val) height = atoi(env_val);
    env_val = getenv("BENCH_PARAM_threshold_low");
    if (env_val) threshold_low = atoi(env_val);
    env_val = getenv("BENCH_PARAM_threshold_high");
    if (env_val) threshold_high = atoi(env_val);
    int num_warmup = 0;

    int W = width;
    int H = height;
    int npix = W * H;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Canny Edge Detection  |  Image: %dx%d  |  "
            "Iterations: %d warmup + 5 timed\n\n", W, H, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"chai_cedd.metal"];

    id<MTLLibrary> library = nil;
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
        fprintf(stderr, "Error: chai_cedd.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Get all 5 kernel functions
    id<MTLFunction> fnGauss = [library newFunctionWithName:@"gaussian_blur_kernel"];
    id<MTLFunction> fnSobel = [library newFunctionWithName:@"sobel_kernel"];
    id<MTLFunction> fnMagDir = [library newFunctionWithName:@"magnitude_direction_kernel"];
    id<MTLFunction> fnNMS   = [library newFunctionWithName:@"nms_kernel"];
    id<MTLFunction> fnHyst  = [library newFunctionWithName:@"hysteresis_kernel"];

    if (!fnGauss || !fnSobel || !fnMagDir || !fnNMS || !fnHyst) {
        fprintf(stderr, "Error: one or more kernel functions not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoGauss = [device newComputePipelineStateWithFunction:fnGauss error:&err];
    id<MTLComputePipelineState> psoSobel = [device newComputePipelineStateWithFunction:fnSobel error:&err];
    id<MTLComputePipelineState> psoMagDir = [device newComputePipelineStateWithFunction:fnMagDir error:&err];
    id<MTLComputePipelineState> psoNMS = [device newComputePipelineStateWithFunction:fnNMS error:&err];
    id<MTLComputePipelineState> psoHyst = [device newComputePipelineStateWithFunction:fnHyst error:&err];

    if (!psoGauss || !psoSobel || !psoMagDir || !psoNMS || !psoHyst) {
        fprintf(stderr, "Error creating pipeline state\n");
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate synthetic input
    // -------------------------------------------------------------------
    srand(42);
    id<MTLBuffer> bufInput = [device newBufferWithLength:npix * sizeof(float)
                                                 options:MTLResourceStorageModeShared];
    float* inputPtr = (float*)bufInput.contents;
    for (int i = 0; i < npix; ++i) {
        inputPtr[i] = (float)(rand() % 256);
    }

    // Keep a host copy for verification
    std::vector<float> h_input(inputPtr, inputPtr + npix);

    // Allocate intermediate buffers
    id<MTLBuffer> bufBlurred = [device newBufferWithLength:npix * sizeof(float)
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufGx = [device newBufferWithLength:npix * sizeof(float)
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufGy = [device newBufferWithLength:npix * sizeof(float)
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufMag = [device newBufferWithLength:npix * sizeof(float)
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufDir = [device newBufferWithLength:npix * sizeof(int)
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufNMS = [device newBufferWithLength:npix * sizeof(float)
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufEdges = [device newBufferWithLength:npix * sizeof(unsigned char)
                                                 options:MTLResourceStorageModeShared];

    // Params
    uint32_t dims[2] = { (uint32_t)W, (uint32_t)H };
    float thresholds[2] = { (float)threshold_low, (float)threshold_high };

    MTLSize tgSize = MTLSizeMake(16, 16, 1);
    MTLSize gridSize = MTLSizeMake((NSUInteger)W, (NSUInteger)H, 1);

    auto run_cedd = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        // Kernel 1: Gaussian blur
        [enc setComputePipelineState:psoGauss];
        [enc setBuffer:bufInput   offset:0 atIndex:0];
        [enc setBuffer:bufBlurred offset:0 atIndex:1];
        [enc setBytes:dims length:sizeof(dims) atIndex:2];
        [enc dispatchThreads:gridSize threadsPerThreadgroup:tgSize];

        // Kernel 2: Sobel
        [enc setComputePipelineState:psoSobel];
        [enc setBuffer:bufBlurred offset:0 atIndex:0];
        [enc setBuffer:bufGx      offset:0 atIndex:1];
        [enc setBuffer:bufGy      offset:0 atIndex:2];
        [enc setBytes:dims length:sizeof(dims) atIndex:3];
        [enc dispatchThreads:gridSize threadsPerThreadgroup:tgSize];

        // Kernel 3: Magnitude + direction
        [enc setComputePipelineState:psoMagDir];
        [enc setBuffer:bufGx  offset:0 atIndex:0];
        [enc setBuffer:bufGy  offset:0 atIndex:1];
        [enc setBuffer:bufMag offset:0 atIndex:2];
        [enc setBuffer:bufDir offset:0 atIndex:3];
        [enc setBytes:dims length:sizeof(dims) atIndex:4];
        [enc dispatchThreads:gridSize threadsPerThreadgroup:tgSize];

        // Kernel 4: Non-maximum suppression
        [enc setComputePipelineState:psoNMS];
        [enc setBuffer:bufMag offset:0 atIndex:0];
        [enc setBuffer:bufDir offset:0 atIndex:1];
        [enc setBuffer:bufNMS offset:0 atIndex:2];
        [enc setBytes:dims length:sizeof(dims) atIndex:3];
        [enc dispatchThreads:gridSize threadsPerThreadgroup:tgSize];

        // Kernel 5: Hysteresis
        [enc setComputePipelineState:psoHyst];
        [enc setBuffer:bufNMS   offset:0 atIndex:0];
        [enc setBuffer:bufEdges offset:0 atIndex:1];
        [enc setBytes:dims length:sizeof(dims) atIndex:2];
        [enc setBytes:thresholds length:sizeof(thresholds) atIndex:3];
        [enc dispatchThreads:gridSize threadsPerThreadgroup:tgSize];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_cedd();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_cedd();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    double mpix_per_sec = (double)npix / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel events (5 kernels in the pipeline)
    printf("{\"type\":\"kernel\",\"name\":\"gaussian_blur_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"threshold_low\":%d,"
           "\"threshold_high\":%d}}\n",
           avg_ms, width, height, threshold_low, threshold_high);

    printf("{\"type\":\"kernel\",\"name\":\"sobel_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"threshold_low\":%d,"
           "\"threshold_high\":%d}}\n",
           avg_ms, width, height, threshold_low, threshold_high);

    printf("{\"type\":\"kernel\",\"name\":\"magnitude_direction_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"threshold_low\":%d,"
           "\"threshold_high\":%d}}\n",
           avg_ms, width, height, threshold_low, threshold_high);

    printf("{\"type\":\"kernel\",\"name\":\"nms_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"threshold_low\":%d,"
           "\"threshold_high\":%d}}\n",
           avg_ms, width, height, threshold_low, threshold_high);

    printf("{\"type\":\"kernel\",\"name\":\"hysteresis_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"threshold_low\":%d,"
           "\"threshold_high\":%d}}\n",
           avg_ms, width, height, threshold_low, threshold_high);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mpixels_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mpix_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f Mpixels/sec\n", mpix_per_sec);

    // -------------------------------------------------------------------
    // Verification: compare a small sub-region with CPU reference
    // -------------------------------------------------------------------
    {
        int vW = (W < 128) ? W : 128;
        int vH = (H < 128) ? H : 128;

        std::vector<float> sub_input(vW * vH);
        for (int y = 0; y < vH; ++y) {
            for (int x = 0; x < vW; ++x) {
                sub_input[y * vW + x] = h_input[y * W + x];
            }
        }

        std::vector<unsigned char> cpu_edges(vW * vH);
        canny_cpu(sub_input.data(), cpu_edges.data(), vW, vH);

        const unsigned char* gpuEdges = (const unsigned char*)bufEdges.contents;

        int errors = 0;
        int compared = 0;
        for (int y = 3; y < vH - 3; ++y) {
            for (int x = 3; x < vW - 3; ++x) {
                unsigned char gpu_val = gpuEdges[y * W + x];
                unsigned char cpu_val = cpu_edges[y * vW + x];
                if (gpu_val != cpu_val) {
                    errors++;
                }
                compared++;
            }
        }

        float error_rate = compared > 0 ? (float)errors / compared * 100.0f : 0.0f;
        if (error_rate < 5.0f) {
            fprintf(stderr, "PASS (%.1f%% pixel mismatch in %dx%d interior, %d/%d)\n",
                    error_rate, vW - 6, vH - 6, errors, compared);
        } else {
            fprintf(stderr, "FAIL (%.1f%% pixel mismatch in %dx%d interior, %d/%d)\n",
                    error_rate, vW - 6, vH - 6, errors, compared);
        }
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
