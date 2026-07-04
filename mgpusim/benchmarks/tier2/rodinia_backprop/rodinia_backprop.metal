/**
 * rodinia_backprop.metal — Metal shaders for the Backpropagation benchmark.
 *
 * Six compute kernels:
 *   1. forward_hidden      — hidden layer activation
 *   2. forward_output      — output layer activation
 *   3. backward_output_delta — output error signal
 *   4. backward_hidden_delta — hidden error signal
 *   5. update_w1           — weight update (input→hidden), 2D dispatch
 *   6. update_w2           — weight update (hidden→output), 1D dispatch
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------
static inline float sigmoid_fn(float x)
{
    return 1.0f / (1.0f + exp(-x));
}

// ---------------------------------------------------------------------------
// 1. forward_hidden
//    dims = { input_n, hidden_n }
// ---------------------------------------------------------------------------
kernel void forward_hidden(
    device const float* input   [[ buffer(0) ]],
    device const float* w1      [[ buffer(1) ]],
    device const float* b1      [[ buffer(2) ]],
    device       float* hidden  [[ buffer(3) ]],
    constant     uint2& dims    [[ buffer(4) ]],  // { input_n, hidden_n }
    uint gid [[ thread_position_in_grid ]])
{
    uint input_n  = dims.x;
    uint hidden_n = dims.y;
    if (gid >= hidden_n) return;

    float sum = b1[gid];
    for (uint i = 0; i < input_n; i++)
        sum += input[i] * w1[i * hidden_n + gid];
    hidden[gid] = sigmoid_fn(sum);
}

// ---------------------------------------------------------------------------
// 2. forward_output
//    dims = { hidden_n, output_n }
// ---------------------------------------------------------------------------
kernel void forward_output(
    device const float* hidden  [[ buffer(0) ]],
    device const float* w2      [[ buffer(1) ]],
    device const float* b2      [[ buffer(2) ]],
    device       float* output  [[ buffer(3) ]],
    constant     uint2& dims    [[ buffer(4) ]],  // { hidden_n, output_n }
    uint gid [[ thread_position_in_grid ]])
{
    uint hidden_n = dims.x;
    uint output_n = dims.y;
    if (gid >= output_n) return;

    float sum = b2[gid];
    for (uint j = 0; j < hidden_n; j++)
        sum += hidden[j] * w2[j * output_n + gid];
    output[gid] = sigmoid_fn(sum);
}

// ---------------------------------------------------------------------------
// 3. backward_output_delta
//    delta_out[k] = output[k]*(1-output[k])*(target[k]-output[k])
// ---------------------------------------------------------------------------
kernel void backward_output_delta(
    device const float* output    [[ buffer(0) ]],
    device const float* target    [[ buffer(1) ]],
    device       float* delta_out [[ buffer(2) ]],
    constant     uint&  output_n  [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    if (gid >= output_n) return;
    float o = output[gid];
    delta_out[gid] = o * (1.0f - o) * (target[gid] - o);
}

// ---------------------------------------------------------------------------
// 4. backward_hidden_delta
//    delta_hid[j] = hidden[j]*(1-hidden[j])*sum_k(w2[j*output_n+k]*delta_out[k])
//    dims = { hidden_n, output_n }
// ---------------------------------------------------------------------------
kernel void backward_hidden_delta(
    device const float* hidden    [[ buffer(0) ]],
    device const float* w2        [[ buffer(1) ]],
    device const float* delta_out [[ buffer(2) ]],
    device       float* delta_hid [[ buffer(3) ]],
    constant     uint2& dims      [[ buffer(4) ]],  // { hidden_n, output_n }
    uint gid [[ thread_position_in_grid ]])
{
    uint hidden_n = dims.x;
    uint output_n = dims.y;
    if (gid >= hidden_n) return;

    float h   = hidden[gid];
    float sum = 0.0f;
    for (uint k = 0; k < output_n; k++)
        sum += w2[gid * output_n + k] * delta_out[k];
    delta_hid[gid] = h * (1.0f - h) * sum;
}

// ---------------------------------------------------------------------------
// 5. update_w1  (2D dispatch: x→i (input), y→j (hidden))
//    w1[i*hidden_n+j] += lr * input[i] * delta_hid[j]
//    dims = { input_n, hidden_n }
// ---------------------------------------------------------------------------
kernel void update_w1(
    device       float* w1        [[ buffer(0) ]],
    device const float* input     [[ buffer(1) ]],
    device const float* delta_hid [[ buffer(2) ]],
    constant     uint2& dims      [[ buffer(3) ]],  // { input_n, hidden_n }
    constant     float& lr        [[ buffer(4) ]],
    uint2 gid2 [[ thread_position_in_grid ]])
{
    uint input_n  = dims.x;
    uint hidden_n = dims.y;
    uint i = gid2.x;
    uint j = gid2.y;
    if (i >= input_n || j >= hidden_n) return;
    w1[i * hidden_n + j] += lr * input[i] * delta_hid[j];
}

// ---------------------------------------------------------------------------
// 6. update_w2  (1D dispatch: gid→j)
//    w2[j*output_n+k] += lr * hidden[j] * delta_out[k]
//    dims = { hidden_n, output_n }
// ---------------------------------------------------------------------------
kernel void update_w2(
    device       float* w2        [[ buffer(0) ]],
    device const float* hidden    [[ buffer(1) ]],
    device const float* delta_out [[ buffer(2) ]],
    constant     uint2& dims      [[ buffer(3) ]],  // { hidden_n, output_n }
    constant     float& lr        [[ buffer(4) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint hidden_n = dims.x;
    uint output_n = dims.y;
    if (gid >= hidden_n) return;
    for (uint k = 0; k < output_n; k++)
        w2[gid * output_n + k] += lr * hidden[gid] * delta_out[k];
}
