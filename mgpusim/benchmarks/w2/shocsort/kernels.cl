// SHOC Sort (CUDPP 4-bit radix sort), ported from gpu_benchmarks
// tier2/shoc_sort/shoc_sort.hip (verbatim SHOC sort_kernel.cu kernels) to
// OpenCL C for MGPUSim. A full sort of 32-bit keys = 8 passes of 4 bits; each
// pass runs radixSortBlocks -> findRadixOffsets -> recursive scan
// (scan + vectorAddUniform4) -> reorderData. No atomics in the original.
//
// Simulator rules (T17): no atomics, no private arrays, only the DS opcodes
// ds_write_b32/write2_b32/read_b32/read2_b32/write2_b64/read_b64/read2_b64.
// Changes for the DS rule (same results):
//   - localSize stays a run-time value (get_local_size) so LDS offsets that
//     are multiples of 64 dwords are not folded into ds_*2st64_b32;
//   - reorderData keeps keys/values of the 2-element (uint2) layout in LDS
//     as two planes (even elements, odd elements) padded by one dword, so no
//     64-bit LDS store (ds_write_b64) is generated;
//   - bool arguments of scan are ints;
// and for ALU opcodes the emulator lacks: the 4-bit digit mask is a kernel
// argument (radixMask = 0xF), so (x >> s) & mask is not folded into
// v_bfe_u32; the bit loop of radixSortBlocks uses a signed counter (no
// s_cmp_le_u32).

__kernel void radixSortBlocks(__global uint4 *keysOut, __global uint4 *valuesOut,
                              __global const uint4 *keysIn,
                              __global const uint4 *valuesIn,
                              const int nbits, const uint startbit) {
  __local uint sMem[512];
  __local uint numtrue;

  const uint tid = get_local_id(0);
  const uint localSize = get_local_size(0);
  const uint i = tid + get_group_id(0) * localSize;

  uint4 key = keysIn[i];
  uint4 value = valuesIn[i];

  // signed loop counter: an unsigned bound compiles to s_cmp_le_u32,
  // which the emulator lacks
  for (int b = 0; b < nbits; ++b) {
    const uint shift = startbit + b;
    uint4 lsb;
    lsb.x = !((key.x >> shift) & 0x1);
    lsb.y = !((key.y >> shift) & 0x1);
    lsb.z = !((key.z >> shift) & 0x1);
    lsb.w = !((key.w >> shift) & 0x1);

    // scan4 + scanLSB (exclusive scan of the 4-element sums over the WG)
    uint sx = lsb.x;
    uint sy = lsb.y + sx;
    uint sz = lsb.z + sy;
    uint val = lsb.w + sz;
    {
      uint idx = tid;
      sMem[idx] = 0;
      barrier(CLK_LOCAL_MEM_FENCE);
      idx += localSize;
      uint t;
      sMem[idx] = val;      barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 1];    barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 2];    barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 4];    barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 8];    barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 16];   barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 32];   barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      t = sMem[idx - 64];   barrier(CLK_LOCAL_MEM_FENCE);
      sMem[idx] += t;       barrier(CLK_LOCAL_MEM_FENCE);
      val = sMem[idx] - val;
    }
    uint4 address;
    address.x = val;
    address.y = val + sx;
    address.z = val + sy;
    address.w = val + sz;

    if (tid == localSize - 1) {
      numtrue = address.w + lsb.w;
    }
    barrier(CLK_LOCAL_MEM_FENCE);

    uint4 rank;
    const uint idx = tid * 4;
    rank.x = lsb.x ? address.x : numtrue + idx - address.x;
    rank.y = lsb.y ? address.y : numtrue + idx + 1 - address.y;
    rank.z = lsb.z ? address.z : numtrue + idx + 2 - address.z;
    rank.w = lsb.w ? address.w : numtrue + idx + 3 - address.w;

    sMem[(rank.x & 3) * localSize + (rank.x >> 2)] = key.x;
    sMem[(rank.y & 3) * localSize + (rank.y >> 2)] = key.y;
    sMem[(rank.z & 3) * localSize + (rank.z >> 2)] = key.z;
    sMem[(rank.w & 3) * localSize + (rank.w >> 2)] = key.w;
    barrier(CLK_LOCAL_MEM_FENCE);

    key.x = sMem[tid];
    key.y = sMem[tid + localSize];
    key.z = sMem[tid + 2 * localSize];
    key.w = sMem[tid + 3 * localSize];
    barrier(CLK_LOCAL_MEM_FENCE);

    sMem[(rank.x & 3) * localSize + (rank.x >> 2)] = value.x;
    sMem[(rank.y & 3) * localSize + (rank.y >> 2)] = value.y;
    sMem[(rank.z & 3) * localSize + (rank.z >> 2)] = value.z;
    sMem[(rank.w & 3) * localSize + (rank.w >> 2)] = value.w;
    barrier(CLK_LOCAL_MEM_FENCE);

    value.x = sMem[tid];
    value.y = sMem[tid + localSize];
    value.z = sMem[tid + 2 * localSize];
    value.w = sMem[tid + 3 * localSize];
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  keysOut[i] = key;
  valuesOut[i] = value;
}

__kernel void findRadixOffsets(__global const uint2 *keys,
                               __global uint *counters,
                               __global uint *blockOffsets,
                               const uint startbit, const uint totalBlocks,
                               const uint radixMask) {
  __local uint sStartPointers[16];
  __local uint sRadix1[512];

  const uint groupId = get_group_id(0);
  const uint localId = get_local_id(0);
  const uint groupSize = get_local_size(0);

  uint2 radix2 = keys[localId + groupId * groupSize];

  sRadix1[2 * localId] = (radix2.x >> startbit) & radixMask;
  sRadix1[2 * localId + 1] = (radix2.y >> startbit) & radixMask;

  if (localId < 16) {
    sStartPointers[localId] = 0;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  if ((localId > 0) && (sRadix1[localId] != sRadix1[localId - 1])) {
    sStartPointers[sRadix1[localId]] = localId;
  }
  if (sRadix1[localId + groupSize] != sRadix1[localId + groupSize - 1]) {
    sStartPointers[sRadix1[localId + groupSize]] = localId + groupSize;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  if (localId < 16) {
    blockOffsets[groupId * 16 + localId] = sStartPointers[localId];
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  if ((localId > 0) && (sRadix1[localId] != sRadix1[localId - 1])) {
    sStartPointers[sRadix1[localId - 1]] =
        localId - sStartPointers[sRadix1[localId - 1]];
  }
  if (sRadix1[localId + groupSize] != sRadix1[localId + groupSize - 1]) {
    sStartPointers[sRadix1[localId + groupSize - 1]] =
        localId + groupSize - sStartPointers[sRadix1[localId + groupSize - 1]];
  }
  if (localId == groupSize - 1) {
    sStartPointers[sRadix1[2 * groupSize - 1]] =
        2 * groupSize - sStartPointers[sRadix1[2 * groupSize - 1]];
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  if (localId < 16) {
    counters[localId * totalBlocks + groupId] = sStartPointers[localId];
  }
}

// Element j (0..511) of the block's uint2 layout lives at plane (j & 1),
// slot (j >> 1); the odd plane starts at PLANE (one dword of padding).
#define PLANE 257
#define LSLOT(j) (((j) & 1) * PLANE + ((j) >> 1))

__kernel void reorderData(__global uint *outKeys, __global uint *outValues,
                          __global const uint2 *keys,
                          __global const uint2 *values,
                          __global const uint *blockOffsets,
                          __global const uint *offsets,
                          const uint startbit, const uint totalBlocks,
                          const uint radixMask) {
  __local uint sKeys1[2 * PLANE];
  __local uint sValues1[2 * PLANE];
  __local uint sOffsets[16];
  __local uint sBlockOffsets[16];

  const uint GROUP_SIZE = get_local_size(0);
  const uint blockId = get_group_id(0);
  const uint tid = get_local_id(0);
  const uint i = blockId * GROUP_SIZE + tid;

  uint2 k2 = keys[i];
  uint2 v2 = values[i];
  sKeys1[tid] = k2.x;
  sKeys1[PLANE + tid] = k2.y;
  sValues1[tid] = v2.x;
  sValues1[PLANE + tid] = v2.y;

  if (tid < 16) {
    sOffsets[tid] = offsets[tid * totalBlocks + blockId];
    sBlockOffsets[tid] = blockOffsets[blockId * 16 + tid];
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  uint j = tid;
  uint k = sKeys1[LSLOT(j)];
  uint radix = (k >> startbit) & radixMask;
  uint globalOffset = sOffsets[radix] + j - sBlockOffsets[radix];
  outKeys[globalOffset] = k;
  outValues[globalOffset] = sValues1[LSLOT(j)];

  j = tid + GROUP_SIZE;
  k = sKeys1[LSLOT(j)];
  radix = (k >> startbit) & radixMask;
  globalOffset = sOffsets[radix] + j - sBlockOffsets[radix];
  outKeys[globalOffset] = k;
  outValues[globalOffset] = sValues1[LSLOT(j)];
}

__kernel void scan(__global uint *g_odata, __global const uint *g_idata,
                   __global uint *g_blockSums, const int n,
                   const int fullBlock, const int storeSum) {
  __local uint s_data[512];

  uint4 tempData;
  uint4 threadScanT;
  uint res;
  __global const uint4 *inData = (__global const uint4 *)g_idata;

  const int gid = get_global_id(0);
  const int tid = get_local_id(0);
  const int bdim = get_local_size(0);
  const int i = gid * 4;

  if (fullBlock || i + 3 < n) {
    tempData = inData[gid];
    threadScanT.x = tempData.x;
    threadScanT.y = tempData.y + threadScanT.x;
    threadScanT.z = tempData.z + threadScanT.y;
    threadScanT.w = tempData.w + threadScanT.z;
    res = threadScanT.w;
  } else {
    threadScanT.x = (i < n) ? g_idata[i] : 0;
    threadScanT.y = ((i + 1 < n) ? g_idata[i + 1] : 0) + threadScanT.x;
    threadScanT.z = ((i + 2 < n) ? g_idata[i + 2] : 0) + threadScanT.y;
    threadScanT.w = ((i + 3 < n) ? g_idata[i + 3] : 0) + threadScanT.z;
    res = threadScanT.w;
  }

  // scanLocalMem
  {
    int idx = tid;
    s_data[idx] = 0;
    barrier(CLK_LOCAL_MEM_FENCE);
    idx += bdim;
    uint t;
    s_data[idx] = res;     barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 1];   barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 2];   barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 4];   barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 8];   barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 16];  barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 32];  barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 64];  barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    t = s_data[idx - 128]; barrier(CLK_LOCAL_MEM_FENCE);
    s_data[idx] += t;      barrier(CLK_LOCAL_MEM_FENCE);
    res = s_data[idx - 1];
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  if (storeSum && tid == bdim - 1) {
    g_blockSums[get_group_id(0)] = res + threadScanT.w;
  }

  __global uint4 *outData = (__global uint4 *)g_odata;
  tempData.x = res;
  tempData.y = res + threadScanT.x;
  tempData.z = res + threadScanT.y;
  tempData.w = res + threadScanT.z;

  if (fullBlock || i + 3 < n) {
    outData[gid] = tempData;
  } else {
    if (i < n) {
      g_odata[i] = tempData.x;
      if ((i + 1) < n) {
        g_odata[i + 1] = tempData.y;
        if ((i + 2) < n) {
          g_odata[i + 2] = tempData.z;
        }
      }
    }
  }
}

__kernel void vectorAddUniform4(__global uint *d_vector,
                                __global const uint *d_uniforms, const int n) {
  __local uint uni[1];
  if (get_local_id(0) == 0) {
    uni[0] = d_uniforms[get_group_id(0)];
  }
  uint address = get_local_id(0) + get_group_id(0) * get_local_size(0) * 4;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int i = 0; i < 4 && address < (uint)n; i++) {
    d_vector[address] += uni[0];
    address += get_local_size(0);
  }
}
