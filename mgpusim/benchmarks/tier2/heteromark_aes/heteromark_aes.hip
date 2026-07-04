// heteromark_aes.hip — HeteroMark AES-256 ECB encryption benchmark (HIP)
//
// Encrypts N 16-byte blocks using AES-256 in ECB mode on the GPU.
// Each thread encrypts one 16-byte block (14 rounds).
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./heteromark_aes [--size N]
//
//   --size N         Number of 16-byte blocks (default: 1048576)
//
// Output (stdout): CSV row — heteromark_aes,<N>,<time_ms>,<GBs>
// Output (stderr): human-readable results

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyAsync          cudaMemcpyAsync
#define hipMemset               cudaMemset
#define hipMemsetAsync          cudaMemsetAsync
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
#define hipMemcpyToSymbol       cudaMemcpyToSymbol
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetLastError         cudaGetLastError
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipStream_t             cudaStream_t
#define hipStreamCreate         cudaStreamCreate
#define hipStreamDestroy        cudaStreamDestroy
#define hipStreamSynchronize    cudaStreamSynchronize
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#define hipHostMalloc           cudaMallocHost
#define hipHostFree             cudaFreeHost
#define hipHostMallocDefault    cudaHostAllocDefault
#define hipBlockIdx_x           blockIdx.x
#define hipBlockIdx_y           blockIdx.y
#define hipBlockIdx_z           blockIdx.z
#define hipThreadIdx_x          threadIdx.x
#define hipThreadIdx_y          threadIdx.y
#define hipThreadIdx_z          threadIdx.z
#define hipBlockDim_x           blockDim.x
#define hipBlockDim_y           blockDim.y
#define hipBlockDim_z           blockDim.z
#define hipGridDim_x            gridDim.x
#define hipGridDim_y            gridDim.y
#define hipGridDim_z            gridDim.z
#define __HIP_PLATFORM_AMD__    0
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define HIP_CHECK(cmd)                                                         \
    do {                                                                       \
        hipError_t _e = (cmd);                                                 \
        if (_e != hipSuccess) {                                                \
            fprintf(stderr, "HIP error %s at %s:%d\n",                        \
                    hipGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// AES S-Box and round constants
// ---------------------------------------------------------------------------

__constant__ static const unsigned char d_sbox[256] = {
    0x63,0x7c,0x77,0x7b,0xf2,0x6b,0x6f,0xc5,0x30,0x01,0x67,0x2b,0xfe,0xd7,0xab,0x76,
    0xca,0x82,0xc9,0x7d,0xfa,0x59,0x47,0xf0,0xad,0xd4,0xa2,0xaf,0x9c,0xa4,0x72,0xc0,
    0xb7,0xfd,0x93,0x26,0x36,0x3f,0xf7,0xcc,0x34,0xa5,0xe5,0xf1,0x71,0xd8,0x31,0x15,
    0x04,0xc7,0x23,0xc3,0x18,0x96,0x05,0x9a,0x07,0x12,0x80,0xe2,0xeb,0x27,0xb2,0x75,
    0x09,0x83,0x2c,0x1a,0x1b,0x6e,0x5a,0xa0,0x52,0x3b,0xd6,0xb3,0x29,0xe3,0x2f,0x84,
    0x53,0xd1,0x00,0xed,0x20,0xfc,0xb1,0x5b,0x6a,0xcb,0xbe,0x39,0x4a,0x4c,0x58,0xcf,
    0xd0,0xef,0xaa,0xfb,0x43,0x4d,0x33,0x85,0x45,0xf9,0x02,0x7f,0x50,0x3c,0x9f,0xa8,
    0x51,0xa3,0x40,0x8f,0x92,0x9d,0x38,0xf5,0xbc,0xb6,0xda,0x21,0x10,0xff,0xf3,0xd2,
    0xcd,0x0c,0x13,0xec,0x5f,0x97,0x44,0x17,0xc4,0xa7,0x7e,0x3d,0x64,0x5d,0x19,0x73,
    0x60,0x81,0x4f,0xdc,0x22,0x2a,0x90,0x88,0x46,0xee,0xb8,0x14,0xde,0x5e,0x0b,0xdb,
    0xe0,0x32,0x3a,0x0a,0x49,0x06,0x24,0x5c,0xc2,0xd3,0xac,0x62,0x91,0x95,0xe4,0x79,
    0xe7,0xc8,0x37,0x6d,0x8d,0xd5,0x4e,0xa9,0x6c,0x56,0xf4,0xea,0x65,0x7a,0xae,0x08,
    0xba,0x78,0x25,0x2e,0x1c,0xa6,0xb4,0xc6,0xe8,0xdd,0x74,0x1f,0x4b,0xbd,0x8b,0x8a,
    0x70,0x3e,0xb5,0x66,0x48,0x03,0xf6,0x0e,0x61,0x35,0x57,0xb9,0x86,0xc1,0x1d,0x9e,
    0xe1,0xf8,0x98,0x11,0x69,0xd9,0x8e,0x94,0x9b,0x1e,0x87,0xe9,0xce,0x55,0x28,0xdf,
    0x8c,0xa1,0x89,0x0d,0xbf,0xe6,0x42,0x68,0x41,0x99,0x2d,0x0f,0xb0,0x54,0xbb,0x16
};

static const unsigned char h_sbox[256] = {
    0x63,0x7c,0x77,0x7b,0xf2,0x6b,0x6f,0xc5,0x30,0x01,0x67,0x2b,0xfe,0xd7,0xab,0x76,
    0xca,0x82,0xc9,0x7d,0xfa,0x59,0x47,0xf0,0xad,0xd4,0xa2,0xaf,0x9c,0xa4,0x72,0xc0,
    0xb7,0xfd,0x93,0x26,0x36,0x3f,0xf7,0xcc,0x34,0xa5,0xe5,0xf1,0x71,0xd8,0x31,0x15,
    0x04,0xc7,0x23,0xc3,0x18,0x96,0x05,0x9a,0x07,0x12,0x80,0xe2,0xeb,0x27,0xb2,0x75,
    0x09,0x83,0x2c,0x1a,0x1b,0x6e,0x5a,0xa0,0x52,0x3b,0xd6,0xb3,0x29,0xe3,0x2f,0x84,
    0x53,0xd1,0x00,0xed,0x20,0xfc,0xb1,0x5b,0x6a,0xcb,0xbe,0x39,0x4a,0x4c,0x58,0xcf,
    0xd0,0xef,0xaa,0xfb,0x43,0x4d,0x33,0x85,0x45,0xf9,0x02,0x7f,0x50,0x3c,0x9f,0xa8,
    0x51,0xa3,0x40,0x8f,0x92,0x9d,0x38,0xf5,0xbc,0xb6,0xda,0x21,0x10,0xff,0xf3,0xd2,
    0xcd,0x0c,0x13,0xec,0x5f,0x97,0x44,0x17,0xc4,0xa7,0x7e,0x3d,0x64,0x5d,0x19,0x73,
    0x60,0x81,0x4f,0xdc,0x22,0x2a,0x90,0x88,0x46,0xee,0xb8,0x14,0xde,0x5e,0x0b,0xdb,
    0xe0,0x32,0x3a,0x0a,0x49,0x06,0x24,0x5c,0xc2,0xd3,0xac,0x62,0x91,0x95,0xe4,0x79,
    0xe7,0xc8,0x37,0x6d,0x8d,0xd5,0x4e,0xa9,0x6c,0x56,0xf4,0xea,0x65,0x7a,0xae,0x08,
    0xba,0x78,0x25,0x2e,0x1c,0xa6,0xb4,0xc6,0xe8,0xdd,0x74,0x1f,0x4b,0xbd,0x8b,0x8a,
    0x70,0x3e,0xb5,0x66,0x48,0x03,0xf6,0x0e,0x61,0x35,0x57,0xb9,0x86,0xc1,0x1d,0x9e,
    0xe1,0xf8,0x98,0x11,0x69,0xd9,0x8e,0x94,0x9b,0x1e,0x87,0xe9,0xce,0x55,0x28,0xdf,
    0x8c,0xa1,0x89,0x0d,0xbf,0xe6,0x42,0x68,0x41,0x99,0x2d,0x0f,0xb0,0x54,0xbb,0x16
};

// AES-256 round constants (only first 7 used for 14-round AES-256)
__constant__ static const unsigned char d_rcon[7] = {
    0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40
};

static const unsigned char h_rcon[7] = {
    0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40
};

// ---------------------------------------------------------------------------
// AES helper functions (device)
// ---------------------------------------------------------------------------

__device__ static void sub_bytes(unsigned char state[16]) {
    for (int i = 0; i < 16; ++i)
        state[i] = d_sbox[state[i]];
}

__device__ static void shift_rows(unsigned char state[16]) {
    unsigned char tmp;
    // Row 1: shift left by 1
    tmp = state[1]; state[1] = state[5]; state[5] = state[9];
    state[9] = state[13]; state[13] = tmp;
    // Row 2: shift left by 2
    tmp = state[2]; state[2] = state[10]; state[10] = tmp;
    tmp = state[6]; state[6] = state[14]; state[14] = tmp;
    // Row 3: shift left by 3 (= right by 1)
    tmp = state[15]; state[15] = state[11]; state[11] = state[7];
    state[7] = state[3]; state[3] = tmp;
}

__device__ static unsigned char xtime(unsigned char a) {
    return (unsigned char)((a << 1) ^ (((a >> 7) & 1) * 0x1b));
}

__device__ static void mix_columns(unsigned char state[16]) {
    for (int c = 0; c < 4; ++c) {
        int i = c * 4;
        unsigned char a0 = state[i], a1 = state[i+1], a2 = state[i+2], a3 = state[i+3];
        unsigned char t = a0 ^ a1 ^ a2 ^ a3;
        state[i]   = a0 ^ xtime(a0 ^ a1) ^ t;
        state[i+1] = a1 ^ xtime(a1 ^ a2) ^ t;
        state[i+2] = a2 ^ xtime(a2 ^ a3) ^ t;
        state[i+3] = a3 ^ xtime(a3 ^ a0) ^ t;
    }
}

__device__ static void add_round_key(unsigned char state[16],
                                      const unsigned char* round_key) {
    for (int i = 0; i < 16; ++i)
        state[i] ^= round_key[i];
}

// ---------------------------------------------------------------------------
// AES-256 key expansion (host)
// ---------------------------------------------------------------------------

static void key_expansion_256(const unsigned char key[32],
                               unsigned char expanded[240]) {
    // Copy original key
    memcpy(expanded, key, 32);

    int bytes_generated = 32;
    int rcon_idx = 0;

    while (bytes_generated < 240) {
        unsigned char tmp[4];
        memcpy(tmp, expanded + bytes_generated - 4, 4);

        if (bytes_generated % 32 == 0) {
            // RotWord
            unsigned char t = tmp[0];
            tmp[0] = tmp[1]; tmp[1] = tmp[2]; tmp[2] = tmp[3]; tmp[3] = t;
            // SubWord
            for (int i = 0; i < 4; ++i) tmp[i] = h_sbox[tmp[i]];
            // XOR rcon
            tmp[0] ^= h_rcon[rcon_idx++];
        } else if (bytes_generated % 32 == 16) {
            // SubWord only (AES-256 extra)
            for (int i = 0; i < 4; ++i) tmp[i] = h_sbox[tmp[i]];
        }

        for (int i = 0; i < 4; ++i) {
            expanded[bytes_generated] = expanded[bytes_generated - 32] ^ tmp[i];
            bytes_generated++;
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel: AES-256 ECB encrypt one 16-byte block per thread
// ---------------------------------------------------------------------------

__global__ void aes256_encrypt_kernel(unsigned char* __restrict__ data,
                                       const unsigned char* __restrict__ round_keys,
                                       int num_blocks) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= num_blocks) return;

    unsigned char state[16];
    // Load block
    for (int i = 0; i < 16; ++i)
        state[i] = data[idx * 16 + i];

    // Initial round key addition
    add_round_key(state, round_keys);

    // Rounds 1-13
    for (int r = 1; r <= 13; ++r) {
        sub_bytes(state);
        shift_rows(state);
        mix_columns(state);
        add_round_key(state, round_keys + r * 16);
    }

    // Final round (no MixColumns)
    sub_bytes(state);
    shift_rows(state);
    add_round_key(state, round_keys + 14 * 16);

    // Store result
    for (int i = 0; i < 16; ++i)
        data[idx * 16 + i] = state[i];
}

// ---------------------------------------------------------------------------
// CPU reference: AES-256 ECB encrypt
// ---------------------------------------------------------------------------

static unsigned char h_xtime(unsigned char a) {
    return (unsigned char)((a << 1) ^ (((a >> 7) & 1) * 0x1b));
}

static void aes256_encrypt_cpu(unsigned char* block,
                                const unsigned char* round_keys) {
    // Initial AddRoundKey
    for (int i = 0; i < 16; ++i) block[i] ^= round_keys[i];

    for (int r = 1; r <= 13; ++r) {
        // SubBytes
        for (int i = 0; i < 16; ++i) block[i] = h_sbox[block[i]];
        // ShiftRows
        unsigned char tmp;
        tmp = block[1]; block[1] = block[5]; block[5] = block[9];
        block[9] = block[13]; block[13] = tmp;
        tmp = block[2]; block[2] = block[10]; block[10] = tmp;
        tmp = block[6]; block[6] = block[14]; block[14] = tmp;
        tmp = block[15]; block[15] = block[11]; block[11] = block[7];
        block[7] = block[3]; block[3] = tmp;
        // MixColumns
        for (int c = 0; c < 4; ++c) {
            int i = c * 4;
            unsigned char a0 = block[i], a1 = block[i+1], a2 = block[i+2], a3 = block[i+3];
            unsigned char t = a0 ^ a1 ^ a2 ^ a3;
            block[i]   = a0 ^ h_xtime(a0 ^ a1) ^ t;
            block[i+1] = a1 ^ h_xtime(a1 ^ a2) ^ t;
            block[i+2] = a2 ^ h_xtime(a2 ^ a3) ^ t;
            block[i+3] = a3 ^ h_xtime(a3 ^ a0) ^ t;
        }
        // AddRoundKey
        for (int i = 0; i < 16; ++i) block[i] ^= round_keys[r * 16 + i];
    }
    // Final round (no MixColumns)
    for (int i = 0; i < 16; ++i) block[i] = h_sbox[block[i]];
    unsigned char tmp;
    tmp = block[1]; block[1] = block[5]; block[5] = block[9];
    block[9] = block[13]; block[13] = tmp;
    tmp = block[2]; block[2] = block[10]; block[10] = tmp;
    tmp = block[6]; block[6] = block[14]; block[14] = tmp;
    tmp = block[15]; block[15] = block[11]; block[11] = block[7];
    block[7] = block[3]; block[3] = tmp;
    for (int i = 0; i < 16; ++i) block[i] ^= round_keys[14 * 16 + i];
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 1048576;
    int block_size = 256;
    const char* key_length = "256";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    key_length = parseStrParam(argc, argv, "--key_length", key_length);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_key_length");
    if (env_val) key_length = env_val;
    int num_warmup = 5;

    int N     = size;

    size_t data_bytes = (size_t)N * 16;
    size_t key_bytes  = 240;  // 15 round keys * 16 bytes

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "AES-256 ECB  |  Blocks: %d  |  Data: %.2f MB  |  "
            "Iterations: %d warmup + %d timed\n\n",
            N, (double)data_bytes / (1024.0 * 1024.0), num_warmup);

    // Generate a fixed 256-bit key
    unsigned char key[32];
    for (int i = 0; i < 32; ++i) key[i] = (unsigned char)(i * 0x13 + 0x37);

    // Key expansion
    unsigned char expanded_key[240];
    key_expansion_256(key, expanded_key);

    // Host allocation + initialization
    unsigned char* h_data = (unsigned char*)malloc(data_bytes);
    unsigned char* h_orig = (unsigned char*)malloc(data_bytes);
    for (size_t i = 0; i < data_bytes; ++i) {
        h_data[i] = (unsigned char)(i & 0xFF);
        h_orig[i] = h_data[i];
    }

    // Device allocation
    unsigned char* d_data;
    unsigned char* d_keys;
    HIP_CHECK(hipMalloc(&d_data, data_bytes));
    HIP_CHECK(hipMalloc(&d_keys, key_bytes));

    // Copy round keys to device
    HIP_CHECK(hipMemcpy(d_keys, expanded_key, key_bytes, hipMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = block_size;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // Lambda: run AES encryption
    auto run_aes = [&]() {
        hipLaunchKernelGGL(aes256_encrypt_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0, d_data, d_keys, N);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_data, h_orig, data_bytes, hipMemcpyHostToDevice));
        run_aes();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipMemcpy(d_data, h_orig, data_bytes, hipMemcpyHostToDevice));

        HIP_CHECK(hipEventRecord(evStart, 0));
        run_aes();
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s = N * 16 / time_s / 1e9
    double gbs = (double)N * 16.0 / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"aes256_encrypt_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"key_length\":\"%s\"}}\n",
           avg_ms, size, block_size, key_length);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification (compare first 256 blocks with CPU reference)
    // -------------------------------------------------------------------
    {
        int verify_n = (N < 256) ? N : 256;
        HIP_CHECK(hipMemcpy(h_data, d_data, data_bytes, hipMemcpyDeviceToHost));

        unsigned char* h_ref = (unsigned char*)malloc((size_t)verify_n * 16);
        memcpy(h_ref, h_orig, (size_t)verify_n * 16);

        for (int b = 0; b < verify_n; ++b) {
            aes256_encrypt_cpu(h_ref + b * 16, expanded_key);
        }

        int errors = 0;
        for (int b = 0; b < verify_n; ++b) {
            for (int j = 0; j < 16; ++j) {
                if (h_data[b * 16 + j] != h_ref[b * 16 + j]) {
                    if (errors < 10) {
                        fprintf(stderr, "Mismatch at block %d byte %d: "
                                "GPU=0x%02x CPU=0x%02x\n",
                                b, j, h_data[b * 16 + j], h_ref[b * 16 + j]);
                    }
                    errors++;
                }
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d byte errors in first %d blocks\n",
                    errors, verify_n);
        else
            fprintf(stderr, "PASS\n");

        free(h_ref);
    }

    HIP_CHECK(hipFree(d_data));
    HIP_CHECK(hipFree(d_keys));
    free(h_data);
    free(h_orig);

    return 0;
}
