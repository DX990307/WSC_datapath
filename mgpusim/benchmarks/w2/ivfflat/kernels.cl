// IVF-Flat search (faiss IndexIVFFlat style), squared L2, top-k.
//
// The coarse quantizer runs on the host (centroids, inverted lists and the
// nprobe probed lists of every query); the database is stored list by list
// (base[pos] is the vector at position pos, ids[pos] its original id).
// A work item is (query, chunk of <= 256 positions of one probed list).
//
//   ivf_scan   WG per work item: distances of the chunk into LDS, then each
//              thread ranks its own distance against the chunk (rank < k
//              writes the candidate): per-item top-k, no atomics
//   ivf_merge  WG per query: the query's item candidates into LDS, rank by
//              (distance, id, slot), rank < k writes the result
//
// Simulator rules (T17): no atomics, no private arrays, only ds_read/write
// b32/b64 and read2/write2 (LDS loops use unroll 1 so no ds_read_b128).

#define D 128
#define CHUNK 256
#define MAXC 1024
#define PADW 17
#define INF 3.0e38f
#define UMUL(a, b) ((ulong)(uint)(a) * (ulong)(uint)(b))

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void ivf_scan(__global const float *base,
              __global const int *ids,
              __global const float *queries,
              __global const int *items,     // [n_items][3] query, pos, count
              __global float *cand_dist,     // [n_items][k]
              __global int *cand_id,         // [n_items][k]
              const int k,
              const int pad) {
  __local float part[16 * PADW];
  __local float dist[CHUNK];
  const int lid = get_local_id(0);
  const int item = get_group_id(0);
  const int q = items[3 * item];
  const int pos = items[3 * item + 1];
  const int cnt = items[3 * item + 2];
  const int v = lid >> 4;  // vector of the step
  const int s = lid & 15;  // 8-dim slice

  __global const float4 *qp = (__global const float4 *)(queries + UMUL(q, D) + s * 8);
  const float4 qa = qp[0];
  const float4 qb = qp[1];

  for (int step = 0; step < cnt; step += 16) {
    const int i = step + v;
    if (i < cnt) {
      __global const float4 *xp =
          (__global const float4 *)(base + UMUL(pos + i, D) + s * 8);
      const float4 da = xp[0] - qa;
      const float4 db = xp[1] - qb;
      float acc = da.x * da.x;
      acc += da.y * da.y;
      acc += da.z * da.z;
      acc += da.w * da.w;
      acc += db.x * db.x;
      acc += db.y * db.y;
      acc += db.z * db.z;
      acc += db.w * db.w;
      part[v * PADW + s] = acc;
    }
    barrier(CLK_LOCAL_MEM_FENCE);
    if (lid < 16 && step + lid < cnt) {
      float sum = 0.0f;
#pragma unroll 1
      for (int j = 0; j < 16; j++) {
        sum += part[lid * PADW + j];
      }
      dist[step + lid] = sum;
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  const uint out0 = (uint)item * (uint)k;
  if (lid < cnt) {
    const float my = dist[lid];
    int rank = 0;
#pragma unroll 1
    for (int j = 0; j < cnt; j++) {
      const float dj = dist[j];
      rank += (dj < my || (dj == my && j < lid)) ? 1 : 0;
    }
    if (rank < k) {
      cand_dist[out0 + rank] = my;
      cand_id[out0 + rank] = ids[pos + lid];
    }
  } else if (lid < k) {
    cand_dist[out0 + lid] = INF;
    cand_id[out0 + lid] = -1;
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void ivf_merge(__global const float *cand_dist,
               __global const int *cand_id,
               __global const int *qitems,    // [Q+1] first item of query
               __global float *out_dist,      // [Q][k]
               __global int *out_id,          // [Q][k]
               const int k,
               const int pad) {
  // Padded to MAXC + 1: arrays exactly 4 KB apart make clang pair the
  // cd/ci accesses into ds_*2st64_b32 (float2 pairs give ds_write_b64);
  // the simulator implements neither.
  __local float cd[MAXC + 1];
  __local int ci[MAXC + 1];
  const int lid = get_local_id(0);
  const int q = get_group_id(0);
  const int i0 = qitems[q];
  const int c = (qitems[q + 1] - i0) * k;
  const uint g0 = (uint)i0 * (uint)k;
#pragma unroll 1
  for (int i = lid; i < c; i += 256) {
    cd[i] = cand_dist[g0 + i];
    ci[i] = cand_id[g0 + i];
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  const uint o0 = (uint)q * (uint)k;
#pragma unroll 1
  for (int i = lid; i < c; i += 256) {
    const float my = cd[i];
    const int myid = ci[i];
    int rank = 0;
#pragma unroll 1
    for (int j = 0; j < c; j++) {
      const float dj = cd[j];
      const int idj = ci[j];
      rank += (dj < my || (dj == my && (idj < myid || (idj == myid && j < i)))) ? 1 : 0;
    }
    if (rank < k) {
      out_dist[o0 + rank] = my;
      out_id[o0 + rank] = myid;
    }
  }
  if (lid >= c && lid < k) {
    out_dist[o0 + lid] = INF;
    out_id[o0 + lid] = -1;
  }
}
