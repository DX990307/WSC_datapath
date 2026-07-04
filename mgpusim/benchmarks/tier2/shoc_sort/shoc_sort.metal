// shoc_sort.metal — Metal compute shader for SHOC Sort (bitonic sort)
//
// Each thread performs one compare-and-swap step in the bitonic sort network.
// Dispatched repeatedly by the host (shoc_sort_metal.mm) for each (j, k) pass.
//
// Buffer layout (bound by host):
//   [[buffer(0)]] — data[]   : array of uint to sort (in-place)
//   [[buffer(1)]] — params[] : {j, k} as uint[2]

#include <metal_stdlib>
using namespace metal;

kernel void bitonic_step(device uint* data     [[buffer(0)]],
                         device const uint* params [[buffer(1)]],
                         uint gid [[thread_position_in_grid]])
{
    uint i   = gid;
    uint j   = params[0];
    uint k   = params[1];
    uint ixj = i ^ j;

    if (ixj > i) {
        bool ascending = ((i & k) == 0u);
        uint a = data[i];
        uint b = data[ixj];
        if (ascending ? (a > b) : (a < b)) {
            data[i]   = b;
            data[ixj] = a;
        }
    }
}
