/*
 * lonestar_bh.metal — Metal compute shaders for Barnes-Hut N-body.
 *
 * Two kernels:
 *   1. bh_force_kernel — Compute approximate gravitational forces via tree traversal
 *   2. integrate_kernel — Update positions and velocities (leapfrog)
 *
 * Buffer layout for bh_force_kernel:
 *   buffer(0): px        — float[N], x positions
 *   buffer(1): py        — float[N], y positions
 *   buffer(2): pmass     — float[N], body masses
 *   buffer(3): ax        — float[N], output x accelerations
 *   buffer(4): ay        — float[N], output y accelerations
 *   buffer(5): tree_cx   — float[num_nodes], tree node center-of-mass x
 *   buffer(6): tree_cy   — float[num_nodes], tree node center-of-mass y
 *   buffer(7): tree_mass — float[num_nodes], tree node total mass
 *   buffer(8): tree_bw   — float[num_nodes], tree node bounding box width
 *   buffer(9): tree_child — int[num_nodes*4], child pointers (4 per node)
 *   buffer(10): params   — { num_nodes, N, theta_sq, eps2 }
 *
 * Buffer layout for integrate_kernel:
 *   buffer(0): px — float[N]
 *   buffer(1): py — float[N]
 *   buffer(2): vx — float[N]
 *   buffer(3): vy — float[N]
 *   buffer(4): ax — float[N]
 *   buffer(5): ay — float[N]
 *   buffer(6): params — { N, dt }
 */

#include <metal_stdlib>
using namespace metal;

struct ForceParams {
    uint num_nodes;
    uint N;
    float theta_sq;
    float eps2;
};

kernel void bh_force_kernel(
    device const float*  px         [[ buffer(0) ]],
    device const float*  py         [[ buffer(1) ]],
    device const float*  pmass      [[ buffer(2) ]],
    device float*        ax         [[ buffer(3) ]],
    device float*        ay         [[ buffer(4) ]],
    device const float*  tree_cx    [[ buffer(5) ]],
    device const float*  tree_cy    [[ buffer(6) ]],
    device const float*  tree_mass  [[ buffer(7) ]],
    device const float*  tree_bw    [[ buffer(8) ]],
    device const int*    tree_child [[ buffer(9) ]],
    constant ForceParams& params    [[ buffer(10) ]],
    uint                 i          [[ thread_position_in_grid ]])
{
    if (i >= params.N) return;

    float xi = px[i];
    float yi = py[i];
    float fax = 0.0f;
    float fay = 0.0f;
    float theta_sq = params.theta_sq;
    float eps2 = params.eps2;
    uint num_nodes = params.num_nodes;

    // Stack-based tree traversal
    int stack_arr[64];
    int sp = 0;
    stack_arr[sp++] = 0; // root

    while (sp > 0) {
        int node = stack_arr[--sp];
        if (node < 0 || (uint)node >= num_nodes) continue;

        float dx = tree_cx[node] - xi;
        float dy = tree_cy[node] - yi;
        float dist2 = dx * dx + dy * dy + eps2;
        float w = tree_bw[node];

        // Check if leaf or satisfies opening criterion
        bool is_leaf = true;
        for (int q = 0; q < 4; ++q) {
            if (tree_child[4 * node + q] >= 0) {
                is_leaf = false;
                break;
            }
        }

        if (is_leaf || (w * w / dist2 < theta_sq)) {
            float m = tree_mass[node];
            if (m > 0.0f && dist2 > eps2 * 2.0f) {
                float inv_dist = rsqrt(dist2);
                float inv_dist3 = inv_dist * inv_dist * inv_dist;
                fax += m * dx * inv_dist3;
                fay += m * dy * inv_dist3;
            }
        } else {
            for (int q = 0; q < 4; ++q) {
                int c = tree_child[4 * node + q];
                if (c >= 0 && sp < 63) {
                    stack_arr[sp++] = c;
                }
            }
        }
    }

    ax[i] = fax;
    ay[i] = fay;
}

struct IntegrateParams {
    uint N;
    float dt;
};

kernel void integrate_kernel(
    device float*        px     [[ buffer(0) ]],
    device float*        py     [[ buffer(1) ]],
    device float*        vx     [[ buffer(2) ]],
    device float*        vy     [[ buffer(3) ]],
    device const float*  ax     [[ buffer(4) ]],
    device const float*  ay     [[ buffer(5) ]],
    constant IntegrateParams& params [[ buffer(6) ]],
    uint                 i      [[ thread_position_in_grid ]])
{
    if (i >= params.N) return;

    float ddt = params.dt;
    vx[i] += ax[i] * ddt;
    vy[i] += ay[i] * ddt;
    px[i] += vx[i] * ddt;
    py[i] += vy[i] * ddt;
}
