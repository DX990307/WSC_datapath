/*
 * chai_cedd.metal — Metal compute shaders for Canny Edge Detection.
 *
 * 5 kernels launched sequentially:
 *   1. gaussian_blur_kernel    — 5×5 Gaussian smoothing
 *   2. sobel_kernel            — Sobel Gx/Gy gradient computation
 *   3. magnitude_direction_kernel — gradient magnitude and quantized direction
 *   4. nms_kernel              — non-maximum suppression
 *   5. hysteresis_kernel       — double-threshold hysteresis
 *
 * Buffer layouts documented per-kernel below.
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Gaussian blur 5×5 kernel
// buffer(0): input  — float[W*H]
// buffer(1): output — float[W*H]
// buffer(2): params — uint2 { W, H }
// ---------------------------------------------------------------------------

constant float gauss_kernel[25] = {
    2.0/159.0, 4.0/159.0,  5.0/159.0,  4.0/159.0, 2.0/159.0,
    4.0/159.0, 9.0/159.0, 12.0/159.0,  9.0/159.0, 4.0/159.0,
    5.0/159.0,12.0/159.0, 15.0/159.0, 12.0/159.0, 5.0/159.0,
    4.0/159.0, 9.0/159.0, 12.0/159.0,  9.0/159.0, 4.0/159.0,
    2.0/159.0, 4.0/159.0,  5.0/159.0,  4.0/159.0, 2.0/159.0
};

kernel void gaussian_blur_kernel(
    device const float*  input    [[ buffer(0) ]],
    device float*        output   [[ buffer(1) ]],
    constant uint2&      dims     [[ buffer(2) ]],
    uint2                gid      [[ thread_position_in_grid ]])
{
    uint W = dims.x;
    uint H = dims.y;
    if (gid.x >= W || gid.y >= H) return;

    float sum = 0.0;
    for (int ky = -2; ky <= 2; ++ky) {
        for (int kx = -2; kx <= 2; ++kx) {
            uint nx = (uint)clamp((int)gid.x + kx, 0, (int)W - 1);
            uint ny = (uint)clamp((int)gid.y + ky, 0, (int)H - 1);
            sum += input[ny * W + nx] * gauss_kernel[(ky + 2) * 5 + (kx + 2)];
        }
    }
    output[gid.y * W + gid.x] = sum;
}

// ---------------------------------------------------------------------------
// Sobel gradient kernel
// buffer(0): blurred — float[W*H]
// buffer(1): gx      — float[W*H]
// buffer(2): gy      — float[W*H]
// buffer(3): dims    — uint2 { W, H }
// ---------------------------------------------------------------------------

kernel void sobel_kernel(
    device const float*  blurred  [[ buffer(0) ]],
    device float*        gx       [[ buffer(1) ]],
    device float*        gy       [[ buffer(2) ]],
    constant uint2&      dims     [[ buffer(3) ]],
    uint2                gid      [[ thread_position_in_grid ]])
{
    uint W = dims.x;
    uint H = dims.y;
    if (gid.x >= W || gid.y >= H) return;

    // Sobel X: [[-1,0,1],[-2,0,2],[-1,0,1]]
    // Sobel Y: [[-1,-2,-1],[0,0,0],[1,2,1]]
    const int sobel_x[9] = {-1,0,1,-2,0,2,-1,0,1};
    const int sobel_y[9] = {-1,-2,-1,0,0,0,1,2,1};

    float sx = 0.0, sy = 0.0;
    for (int ky = -1; ky <= 1; ++ky) {
        for (int kx = -1; kx <= 1; ++kx) {
            uint nx = (uint)clamp((int)gid.x + kx, 0, (int)W - 1);
            uint ny = (uint)clamp((int)gid.y + ky, 0, (int)H - 1);
            float val = blurred[ny * W + nx];
            int ki = (ky + 1) * 3 + (kx + 1);
            sx += val * (float)sobel_x[ki];
            sy += val * (float)sobel_y[ki];
        }
    }
    uint idx = gid.y * W + gid.x;
    gx[idx] = sx;
    gy[idx] = sy;
}

// ---------------------------------------------------------------------------
// Magnitude + direction kernel
// buffer(0): gx   — float[W*H]
// buffer(1): gy   — float[W*H]
// buffer(2): mag  — float[W*H]
// buffer(3): dir  — int[W*H]
// buffer(4): dims — uint2 { W, H }
// ---------------------------------------------------------------------------

kernel void magnitude_direction_kernel(
    device const float*  gx       [[ buffer(0) ]],
    device const float*  gy       [[ buffer(1) ]],
    device float*        mag      [[ buffer(2) ]],
    device int*          dir      [[ buffer(3) ]],
    constant uint2&      dims     [[ buffer(4) ]],
    uint2                gid      [[ thread_position_in_grid ]])
{
    uint W = dims.x;
    uint H = dims.y;
    if (gid.x >= W || gid.y >= H) return;

    uint idx = gid.y * W + gid.x;
    float vx = gx[idx], vy = gy[idx];
    mag[idx] = sqrt(vx * vx + vy * vy);

    float angle = atan2(vy, vx) * (180.0 / 3.14159265);
    if (angle < 0.0) angle += 180.0;

    int d;
    if ((angle >= 0.0 && angle < 22.5) || (angle >= 157.5 && angle <= 180.0))
        d = 0;
    else if (angle >= 22.5 && angle < 67.5)
        d = 45;
    else if (angle >= 67.5 && angle < 112.5)
        d = 90;
    else
        d = 135;

    dir[idx] = d;
}

// ---------------------------------------------------------------------------
// Non-maximum suppression kernel
// buffer(0): mag     — float[W*H]
// buffer(1): dir     — int[W*H]
// buffer(2): nms_out — float[W*H]
// buffer(3): dims    — uint2 { W, H }
// ---------------------------------------------------------------------------

kernel void nms_kernel(
    device const float*  mag      [[ buffer(0) ]],
    device const int*    dir      [[ buffer(1) ]],
    device float*        nms_out  [[ buffer(2) ]],
    constant uint2&      dims     [[ buffer(3) ]],
    uint2                gid      [[ thread_position_in_grid ]])
{
    uint W = dims.x;
    uint H = dims.y;
    uint x = gid.x, y = gid.y;
    if (x >= W || y >= H) return;

    uint idx = y * W + x;
    float m = mag[idx];
    int d = dir[idx];

    float n1 = 0.0, n2 = 0.0;
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

    nms_out[idx] = (m >= n1 && m >= n2) ? m : 0.0;
}

// ---------------------------------------------------------------------------
// Hysteresis thresholding kernel
// buffer(0): nms      — float[W*H]
// buffer(1): edges    — uchar[W*H]
// buffer(2): dims     — uint2 { W, H }
// buffer(3): thresholds — float2 { low, high }
// ---------------------------------------------------------------------------

kernel void hysteresis_kernel(
    device const float*  nms        [[ buffer(0) ]],
    device uchar*        edges      [[ buffer(1) ]],
    constant uint2&      dims       [[ buffer(2) ]],
    constant float2&     thresholds [[ buffer(3) ]],
    uint2                gid        [[ thread_position_in_grid ]])
{
    uint W = dims.x;
    uint H = dims.y;
    uint x = gid.x, y = gid.y;
    if (x >= W || y >= H) return;

    float low_thresh = thresholds.x;
    float high_thresh = thresholds.y;

    uint idx = y * W + x;
    float val = nms[idx];

    if (val >= high_thresh) {
        edges[idx] = 255;
    } else if (val >= low_thresh) {
        uchar is_edge = 0;
        for (int ky = -1; ky <= 1; ++ky) {
            for (int kx = -1; kx <= 1; ++kx) {
                if (kx == 0 && ky == 0) continue;
                int nx = (int)x + kx, ny = (int)y + ky;
                if (nx >= 0 && (uint)nx < W && ny >= 0 && (uint)ny < H) {
                    if (nms[(uint)ny * W + (uint)nx] >= high_thresh) {
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
