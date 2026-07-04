// parboil_histogram.metal — Metal compute shader for the Parboil Histogram benchmark
//
// Computes a 256-bin histogram of N uint32 values using atomic operations.
// Each thread reads one element, computes bin = val & 0xFF, and atomically
// increments the corresponding histogram bin.
//
// Buffer layout:
//   buffer(0): device const uint*        data  — input array of N elements
//   buffer(1): device atomic_uint*       hist  — 256-bin histogram (zeroed before each run)
//   buffer(2): constant uint&            n     — number of elements
//
// Launch: one thread per element (gid in [0, n))

#include <metal_stdlib>
using namespace metal;

kernel void histogram_kernel(
    device const uint*        data [[ buffer(0) ]],
    device       atomic_uint* hist [[ buffer(1) ]],
    constant     uint&        n    [[ buffer(2) ]],
    uint gid [[ thread_position_in_grid ]])
{
    if (gid >= n) return;
    uint bin = data[gid] & 0xFFu;
    atomic_fetch_add_explicit(&hist[bin], 1u, memory_order_relaxed);
}
