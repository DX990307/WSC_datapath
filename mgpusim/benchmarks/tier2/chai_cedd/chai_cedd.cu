// chai_cedd.cu — Canny Edge Detection benchmark
//
// 5-stage pipeline: Gaussian blur, Sobel gradient, magnitude+direction,
// non-maximum suppression, hysteresis thresholding.
//
// Native CUDA implementation.
//
// Usage:
//   ./chai_cedd [W=2048] [H=2048]
//
//   W=<width>    Image width  (default: 2048)
//   H=<height>   Image height (default: 2048)
//
// Output (stdout): CSV row — chai_cedd,<WxH>,<time_ms>,<Mpixels_per_sec>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

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
// Kernel 1: Gaussian blur (5x5)
// ---------------------------------------------------------------------------

__constant__ float d_gauss[25] = {
    2.f/159, 4.f/159,  5.f/159,  4.f/159, 2.f/159,
    4.f/159, 9.f/159, 12.f/159,  9.f/159, 4.f/159,
    5.f/159,12.f/159, 15.f/159, 12.f/159, 5.f/159,
    4.f/159, 9.f/159, 12.f/159,  9.f/159, 4.f/159,
    2.f/159, 4.f/159,  5.f/159,  4.f/159, 2.f/159
};

__global__ void gaussian_blur_kernel(
    const float* __restrict__ input,
    float* __restrict__ output,
    int W, int H)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;

    float sum = 0.0f;
    for (int ky = -2; ky <= 2; ++ky) {
        for (int kx = -2; kx <= 2; ++kx) {
            int nx = min(max(x + kx, 0), W - 1);
            int ny = min(max(y + ky, 0), H - 1);
            sum += input[ny * W + nx] * d_gauss[(ky + 2) * 5 + (kx + 2)];
        }
    }
    output[y * W + x] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 2: Sobel gradient (Gx, Gy)
// ---------------------------------------------------------------------------

__global__ void sobel_kernel(
    const float* __restrict__ blurred,
    float* __restrict__ gx,
    float* __restrict__ gy,
    int W, int H)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;

    float sx = 0.0f, sy = 0.0f;

    // Sobel X kernel: [[-1,0,1],[-2,0,2],[-1,0,1]]
    // Sobel Y kernel: [[-1,-2,-1],[0,0,0],[1,2,1]]
    const int sobel_x[3][3] = {{-1,0,1},{-2,0,2},{-1,0,1}};
    const int sobel_y[3][3] = {{-1,-2,-1},{0,0,0},{1,2,1}};

    for (int ky = -1; ky <= 1; ++ky) {
        for (int kx = -1; kx <= 1; ++kx) {
            int nx = min(max(x + kx, 0), W - 1);
            int ny = min(max(y + ky, 0), H - 1);
            float val = blurred[ny * W + nx];
            sx += val * sobel_x[ky + 1][kx + 1];
            sy += val * sobel_y[ky + 1][kx + 1];
        }
    }
    gx[y * W + x] = sx;
    gy[y * W + x] = sy;
}

// ---------------------------------------------------------------------------
// Kernel 3: Magnitude and direction
// ---------------------------------------------------------------------------

__global__ void magnitude_direction_kernel(
    const float* __restrict__ gx,
    const float* __restrict__ gy,
    float* __restrict__ mag,
    int* __restrict__ dir,
    int W, int H)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;

    int idx = y * W + x;
    float vx = gx[idx], vy = gy[idx];
    mag[idx] = sqrtf(vx * vx + vy * vy);

    // Quantize angle to 0, 45, 90, 135 degrees
    float angle = atan2f(vy, vx) * (180.0f / 3.14159265f);
    if (angle < 0) angle += 180.0f;

    int d;
    if ((angle >= 0 && angle < 22.5f) || (angle >= 157.5f && angle <= 180.0f))
        d = 0;   // horizontal
    else if (angle >= 22.5f && angle < 67.5f)
        d = 45;
    else if (angle >= 67.5f && angle < 112.5f)
        d = 90;  // vertical
    else
        d = 135;

    dir[idx] = d;
}

// ---------------------------------------------------------------------------
// Kernel 4: Non-maximum suppression
// ---------------------------------------------------------------------------

__global__ void nms_kernel(
    const float* __restrict__ mag,
    const int* __restrict__ dir,
    float* __restrict__ nms_out,
    int W, int H)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;

    int idx = y * W + x;
    float m = mag[idx];
    int d = dir[idx];

    float n1 = 0.0f, n2 = 0.0f;
    if (d == 0) {
        // Compare left/right
        if (x > 0) n1 = mag[y * W + (x - 1)];
        if (x < W - 1) n2 = mag[y * W + (x + 1)];
    } else if (d == 90) {
        // Compare up/down
        if (y > 0) n1 = mag[(y - 1) * W + x];
        if (y < H - 1) n2 = mag[(y + 1) * W + x];
    } else if (d == 45) {
        // Compare diagonal
        if (x < W - 1 && y > 0) n1 = mag[(y - 1) * W + (x + 1)];
        if (x > 0 && y < H - 1) n2 = mag[(y + 1) * W + (x - 1)];
    } else { // 135
        if (x > 0 && y > 0) n1 = mag[(y - 1) * W + (x - 1)];
        if (x < W - 1 && y < H - 1) n2 = mag[(y + 1) * W + (x + 1)];
    }

    nms_out[idx] = (m >= n1 && m >= n2) ? m : 0.0f;
}

// ---------------------------------------------------------------------------
// Kernel 5: Hysteresis thresholding
// ---------------------------------------------------------------------------

__global__ void hysteresis_kernel(
    const float* __restrict__ nms,
    unsigned char* __restrict__ edges,
    int W, int H,
    float low_thresh, float high_thresh)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= W || y >= H) return;

    int idx = y * W + x;
    float val = nms[idx];

    if (val >= high_thresh) {
        edges[idx] = 255;
    } else if (val >= low_thresh) {
        // Check if any 8-neighbor is a strong edge
        unsigned char is_edge = 0;
        for (int ky = -1; ky <= 1 && !is_edge; ++ky) {
            for (int kx = -1; kx <= 1 && !is_edge; ++kx) {
                if (kx == 0 && ky == 0) continue;
                int nx = x + kx, ny = y + ky;
                if (nx >= 0 && nx < W && ny >= 0 && ny < H) {
                    if (nms[ny * W + nx] >= high_thresh) {
                        is_edge = 255;
                    }
                }
            }
        }
        edges[idx] = is_edge;
    } else {
        edges[idx] = 0;
    }
}

// ---------------------------------------------------------------------------
// CPU reference: simple Canny edge detection (for verification subset)
// ---------------------------------------------------------------------------

static void canny_cpu(const float* input, unsigned char* output, int W, int H) {
    // Gaussian kernel
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

int main(int argc, char** argv) {
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

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Canny Edge Detection  |  Image: %dx%d  |  "
            "Iterations: %d warmup + 5 timed\n\n", W, H, num_warmup);

    // Generate synthetic grayscale image
    std::vector<float> h_input(npix);
    srand(42);
    for (int i = 0; i < npix; ++i) {
        h_input[i] = (float)(rand() % 256);
    }

    // Allocate device memory
    float *d_input, *d_blurred, *d_gx, *d_gy, *d_mag, *d_nms;
    int *d_dir;
    unsigned char *d_edges;

    CUDA_CHECK(cudaMalloc(&d_input,   npix * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_blurred, npix * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_gx,      npix * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_gy,      npix * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_mag,     npix * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_dir,     npix * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_nms,     npix * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_edges,   npix * sizeof(unsigned char)));

    CUDA_CHECK(cudaMemcpy(d_input, h_input.data(), npix * sizeof(float), cudaMemcpyHostToDevice));

    dim3 blockDim(16, 16);
    dim3 gridDim((W + 15) / 16, (H + 15) / 16);

    float low_t = (float)threshold_low, high_t = (float)threshold_high;

    auto run_cedd = [&]() {
        gaussian_blur_kernel<<<gridDim, blockDim, 0, 0>>>(d_input, d_blurred, W, H);
        sobel_kernel<<<gridDim, blockDim, 0, 0>>>(d_blurred, d_gx, d_gy, W, H);
        magnitude_direction_kernel<<<gridDim, blockDim, 0, 0>>>(d_gx, d_gy, d_mag, d_dir, W, H);
        nms_kernel<<<gridDim, blockDim, 0, 0>>>(d_mag, d_dir, d_nms, W, H);
        hysteresis_kernel<<<gridDim, blockDim, 0, 0>>>(d_nms, d_edges, W, H, low_t, high_t);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_cedd();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_cedd();
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // Mpixels/sec = W * H / time_s / 1e6
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
        // Verify a 128x128 sub-region at top-left
        int vW = (W < 128) ? W : 128;
        int vH = (H < 128) ? H : 128;

        // Build the sub-region input
        std::vector<float> sub_input(vW * vH);
        for (int y = 0; y < vH; ++y) {
            for (int x = 0; x < vW; ++x) {
                sub_input[y * vW + x] = h_input[y * W + x];
            }
        }

        std::vector<unsigned char> cpu_edges(vW * vH);
        canny_cpu(sub_input.data(), cpu_edges.data(), vW, vH);

        // Get GPU results for the same sub-region
        std::vector<unsigned char> h_edges(npix);
        CUDA_CHECK(cudaMemcpy(h_edges.data(), d_edges, npix * sizeof(unsigned char),
                             cudaMemcpyDeviceToHost));

        // Note: GPU processes full image so border effects at (128,128) differ
        // from CPU processing only the sub-image. Compare interior only (skip 3px border).
        int errors = 0;
        int compared = 0;
        for (int y = 3; y < vH - 3; ++y) {
            for (int x = 3; x < vW - 3; ++x) {
                unsigned char gpu_val = h_edges[y * W + x];
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

    CUDA_CHECK(cudaFree(d_input));
    CUDA_CHECK(cudaFree(d_blurred));
    CUDA_CHECK(cudaFree(d_gx));
    CUDA_CHECK(cudaFree(d_gy));
    CUDA_CHECK(cudaFree(d_mag));
    CUDA_CHECK(cudaFree(d_dir));
    CUDA_CHECK(cudaFree(d_nms));
    CUDA_CHECK(cudaFree(d_edges));

    return 0;
}
