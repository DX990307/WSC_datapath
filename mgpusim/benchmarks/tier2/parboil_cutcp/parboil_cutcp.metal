/*
 * parboil_cutcp.metal — Metal compute shader for CUTCP benchmark.
 *
 * Computes Coulombic potential with distance cutoff at each 3D grid point.
 * Each thread handles one grid point and iterates over all atoms within
 * the cutoff radius.
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Parameters struct
// ---------------------------------------------------------------------------

struct CutcpParams {
    uint   num_atoms;
    uint   grid_side;
    float  grid_spacing;
    float  cutoff;
    float  cutoff2;
};

// ---------------------------------------------------------------------------
// Kernel: compute Coulombic potential at each grid point
// ---------------------------------------------------------------------------

kernel void cutcp_kernel(
    device const float4*  atoms     [[ buffer(0) ]],
    device float*         potential [[ buffer(1) ]],
    constant CutcpParams& params    [[ buffer(2) ]],
    uint                  gid       [[ thread_position_in_grid ]])
{
    uint grid_side = params.grid_side;
    uint total_points = grid_side * grid_side * grid_side;
    if (gid >= total_points) return;

    uint gz = gid / (grid_side * grid_side);
    uint gy = (gid / grid_side) % grid_side;
    uint gx = gid % grid_side;

    float px = float(gx) * params.grid_spacing;
    float py = float(gy) * params.grid_spacing;
    float pz = float(gz) * params.grid_spacing;

    float pot = 0.0f;
    float cutoff2 = params.cutoff2;

    for (uint i = 0; i < params.num_atoms; ++i) {
        float4 atom = atoms[i];
        float dx = px - atom.x;
        float dy = py - atom.y;
        float dz = pz - atom.z;
        float r2 = dx * dx + dy * dy + dz * dz;

        if (r2 < cutoff2 && r2 > 1e-12f) {
            float r = sqrt(r2);
            pot += atom.w / r;
        }
    }

    potential[gid] = pot;
}
