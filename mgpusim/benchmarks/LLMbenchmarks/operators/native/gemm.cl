// GEMM kernels used by decomposed LLM linear operators.
//
// The LLM benchmark now owns the GPU tensor operator and invokes these kernels
// from the local LLMbenchmarks/operators package. This copy keeps the full
// native LLM operator set together for inspection and rebuilds.

__kernel void gemm(int m, int n, int k, float alpha, float beta,
                   const __global float *a, const __global float *b,
                   const __global float *c, __global float *d) {
  const int TILE_SIZE = 16;
  __local float subTileM[TILE_SIZE][TILE_SIZE];
  __local float subTileN[TILE_SIZE][TILE_SIZE];

  int bx = get_global_id(0) / get_local_size(0);
  int by = get_global_id(1) / get_local_size(1);
  int tx = get_local_id(0);
  int ty = get_local_id(1);

  int Row = by * TILE_SIZE + ty;
  int Col = bx * TILE_SIZE + tx;

  d[Row * n + Col] = 0;
  float Pvalue = 0;
  for (int i = 0; i < ((k - 1) / TILE_SIZE + 1); i++) {
    int curL = Row * k + i * TILE_SIZE + tx;
    int curR = (i * TILE_SIZE + ty) * n + Col;

    if (i * TILE_SIZE + tx < k && Row < m) {
      subTileM[ty][tx] = a[curL];
    } else {
      subTileM[ty][tx] = 0.0;
    }

    if (i * TILE_SIZE + ty < k && Col < n) {
      subTileN[ty][tx] = b[curR];
    } else {
      subTileN[ty][tx] = 0.0;
    }

    barrier(CLK_LOCAL_MEM_FENCE);
    for (int j = 0; j < TILE_SIZE; j++) {
      if (j + TILE_SIZE * i < k) {
        Pvalue += subTileM[ty][j] * subTileN[j][tx];
      }
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  if (Row < m && Col < n) {
    d[Row * n + Col] = alpha * Pvalue + beta * c[Row * n + Col];
  }
}

__kernel void gemm_old(int m, int n, int k, float alpha, float beta,
                       const __global float *a, const __global float *b,
                       const __global float *c, __global float *d) {
  int x = get_global_id(0);
  int y = get_global_id(1);

  if (y >= m || x >= n) {
    return;
  }

  float acc = 0;
  for (int z = 0; z < k; z++) {
    acc += alpha * a[y * k + z] * b[z * n + x];
  }

  d[y * n + x] = acc + beta * c[y * n + x];
}

__kernel void gemm_split_k(int m, int n, int k, int split_k, float alpha,
                           const __global float *a,
                           const __global float *b,
                           __global float *partials) {
  int x = get_global_id(0);
  int y = get_global_id(1);
  int split = get_global_id(2);

  if (y >= m || x >= n || split >= split_k) {
    return;
  }

  int k_start = (k * split) / split_k;
  int k_end = (k * (split + 1)) / split_k;

  float acc = 0;
  for (int z = k_start; z < k_end; z++) {
    acc += alpha * a[y * k + z] * b[z * n + x];
  }

  partials[(split * m + y) * n + x] = acc;
}

__kernel void gemm_split_k_reduce(int m, int n, int split_k, float beta,
                                  const __global float *partials,
                                  const __global float *c,
                                  __global float *d) {
  int x = get_global_id(0);
  int y = get_global_id(1);

  if (y >= m || x >= n) {
    return;
  }

  int idx = y * n + x;
  int matrix_size = m * n;
  float acc = 0;

  for (int split = 0; split < split_k; split++) {
    acc += partials[split * matrix_size + idx];
  }

  d[idx] = acc + beta * c[idx];
}
