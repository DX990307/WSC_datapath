/*
 * heteromark_pagerank.metal — Metal compute shader for PageRank benchmark.
 *
 * One kernel:
 *   pagerank_kernel — one PageRank iteration (gather-based)
 *     PR_new[v] = (1-d)/N + d * sum(PR_old[u]/degree[u]) for all u→v
 *
 * Buffer layout:
 *   buffer(0): rowPtr    — int[N+1], CSR row pointers
 *   buffer(1): colIdx    — int[nnz], CSR column indices (in-neighbors)
 *   buffer(2): outDegree — int[N],   out-degree of each vertex
 *   buffer(3): prOld     — float[N], current PageRank values
 *   buffer(4): prNew     — float[N], updated PageRank values
 *   buffer(5): params    — uint N, float damping
 */

#include <metal_stdlib>
using namespace metal;

struct PageRankParams {
    uint  N;
    float damping;
};

kernel void pagerank_kernel(
    device const int*    rowPtr    [[ buffer(0) ]],
    device const int*    colIdx    [[ buffer(1) ]],
    device const int*    outDegree [[ buffer(2) ]],
    device const float*  prOld     [[ buffer(3) ]],
    device float*        prNew     [[ buffer(4) ]],
    constant PageRankParams& params [[ buffer(5) ]],
    uint                 v         [[ thread_position_in_grid ]])
{
    uint N        = params.N;
    float damping = params.damping;

    if (v >= N) return;

    float sum = 0.0f;
    int start = rowPtr[v];
    int end   = rowPtr[v + 1];

    for (int e = start; e < end; ++e) {
        int u = colIdx[e];
        sum += prOld[u] / float(outDegree[u]);
    }

    prNew[v] = (1.0f - damping) / float(N) + damping * sum;
}
