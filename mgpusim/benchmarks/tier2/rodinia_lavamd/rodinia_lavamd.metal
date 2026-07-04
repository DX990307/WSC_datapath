/*
 * rodinia_lavamd.metal — Metal compute shader for the Rodinia LavaMD benchmark.
 *
 * Short-range molecular dynamics with cell-list decomposition.
 * One thread per particle, iterates over all neighbor box particles.
 *
 * Buffer layout:
 *   buffer(0):  pos_x           [total_particles] (read)
 *   buffer(1):  pos_y           [total_particles] (read)
 *   buffer(2):  pos_z           [total_particles] (read)
 *   buffer(3):  force_x         [total_particles] (write)
 *   buffer(4):  force_y         [total_particles] (write)
 *   buffer(5):  force_z         [total_particles] (write)
 *   buffer(6):  energy_out      [total_particles] (write)
 *   buffer(7):  neighbor_list   [total_boxes * 27] (read)
 *   buffer(8):  neighbor_count  [total_boxes] (read)
 *   buffer(9):  params          { particles_per_box, total_boxes, 0, 0 } (uint4)
 */

#include <metal_stdlib>
using namespace metal;

#define LJ_A  2.0f
#define LJ_B  1.0f

kernel void lavamd_kernel(
    device const float*  pos_x          [[ buffer(0) ]],
    device const float*  pos_y          [[ buffer(1) ]],
    device const float*  pos_z          [[ buffer(2) ]],
    device       float*  force_x        [[ buffer(3) ]],
    device       float*  force_y        [[ buffer(4) ]],
    device       float*  force_z        [[ buffer(5) ]],
    device       float*  energy_out     [[ buffer(6) ]],
    device const int*    neighbor_list   [[ buffer(7) ]],
    device const int*    neighbor_count  [[ buffer(8) ]],
    constant     uint4&  params          [[ buffer(9) ]],
    uint  tid         [[ thread_index_in_threadgroup ]],
    uint  tg_size     [[ threads_per_threadgroup ]],
    uint  box_id      [[ threadgroup_position_in_grid ]])
{
    uint particles_per_box = params.x;
    uint total_boxes       = params.y;

    if (box_id >= total_boxes) return;

    uint base_i = box_id * particles_per_box;

    for (uint p = tid; p < particles_per_box; p += tg_size) {
        uint i = base_i + p;
        float px = pos_x[i];
        float py = pos_y[i];
        float pz = pos_z[i];

        float fx = 0.0f, fy = 0.0f, fz = 0.0f;
        float pe = 0.0f;

        int n_neighbors = neighbor_count[box_id];

        for (int n = 0; n < n_neighbors; ++n) {
            int nbox = neighbor_list[box_id * 27 + n];
            uint base_j = (uint)nbox * particles_per_box;

            for (uint q = 0; q < particles_per_box; ++q) {
                uint j = base_j + q;

                float dx = px - pos_x[j];
                float dy = py - pos_y[j];
                float dz = pz - pos_z[j];

                float r2 = dx * dx + dy * dy + dz * dz;

                if (r2 > 1e-10f) {
                    float r2inv = 1.0f / r2;
                    float r6inv = r2inv * r2inv * r2inv;

                    float force = r2inv * r6inv * (LJ_A * r6inv - LJ_B);
                    pe += r6inv * (LJ_A * r6inv - LJ_B);

                    fx += force * dx;
                    fy += force * dy;
                    fz += force * dz;
                }
            }
        }

        force_x[i]    = fx;
        force_y[i]    = fy;
        force_z[i]    = fz;
        energy_out[i] = pe;
    }
}
