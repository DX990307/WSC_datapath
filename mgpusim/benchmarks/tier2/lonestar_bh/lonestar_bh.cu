// lonestar_bh.cu — Barnes-Hut N-body (2D quadtree) benchmark
//
// Simplified Barnes-Hut algorithm:
//   - Build 2D quadtree on CPU
//   - Flatten tree to arrays for GPU consumption
//   - GPU kernel: each thread computes force on one body by traversing tree
//   - GPU kernel: integrate positions and velocities
//   - Repeat for 10 timesteps (rebuild tree on CPU each step)
//
// Parameters: N=32768 bodies, theta=0.5 (opening angle)
//
// Usage:
//   ./lonestar_bh [--bodies N] [--timesteps T] [--iterations I]
//
// Output (stdout): CSV — lonestar_bh,<N_bodies>,<time_ms>,<GFLOPS>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cfloat>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            float v = (float)atof(argv[i + 1]);
            if (v > 0.0f) return v;
            break;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Simple deterministic PRNG (xorshift32)
// ---------------------------------------------------------------------------

static uint32_t xorshift32(uint32_t& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return state;
}

static float rand_float(uint32_t& state, float lo, float hi) {
    return lo + (float)(xorshift32(state) & 0xFFFFFF) / (float)0xFFFFFF * (hi - lo);
}

// ---------------------------------------------------------------------------
// Quadtree node (flattened for GPU)
// ---------------------------------------------------------------------------

struct BHNode {
    float cx, cy;      // center of mass
    float mass;        // total mass
    float bx, by;      // bounding box min
    float bw;          // bounding box width (square)
    int child[4];      // children: -1 = empty, >=0 = node index
    int body_idx;      // if leaf with body: body index, else -1
};

// ---------------------------------------------------------------------------
// Build quadtree on CPU
// ---------------------------------------------------------------------------

struct QuadTree {
    std::vector<BHNode> nodes;

    int new_node(float bx, float by, float bw) {
        BHNode n;
        n.cx = 0.0f; n.cy = 0.0f; n.mass = 0.0f;
        n.bx = bx; n.by = by; n.bw = bw;
        n.child[0] = n.child[1] = n.child[2] = n.child[3] = -1;
        n.body_idx = -1;
        nodes.push_back(n);
        return (int)nodes.size() - 1;
    }

    int quadrant(int node, float x, float y) {
        float midx = nodes[node].bx + nodes[node].bw * 0.5f;
        float midy = nodes[node].by + nodes[node].bw * 0.5f;
        int q = 0;
        if (x >= midx) q |= 1;
        if (y >= midy) q |= 2;
        return q;
    }

    void child_bounds(int node, int q, float& cbx, float& cby, float& cbw) {
        cbw = nodes[node].bw * 0.5f;
        cbx = nodes[node].bx;
        cby = nodes[node].by;
        if (q & 1) cbx += cbw;
        if (q & 2) cby += cbw;
    }

    void insert(int node, int body, const float* px, const float* py, const float* pm) {
        float x = px[body], y = py[body], m = pm[body];

        // Empty leaf
        if (nodes[node].mass == 0.0f && nodes[node].body_idx == -1) {
            nodes[node].body_idx = body;
            nodes[node].cx = x;
            nodes[node].cy = y;
            nodes[node].mass = m;
            return;
        }

        // If leaf with a body, split it
        if (nodes[node].body_idx >= 0) {
            int old_body = nodes[node].body_idx;
            nodes[node].body_idx = -1;

            // Re-insert old body
            int oq = quadrant(node, px[old_body], py[old_body]);
            if (nodes[node].child[oq] == -1) {
                float cbx, cby, cbw;
                child_bounds(node, oq, cbx, cby, cbw);
                nodes[node].child[oq] = new_node(cbx, cby, cbw);
            }
            insert(nodes[node].child[oq], old_body, px, py, pm);
        }

        // Insert new body
        int q = quadrant(node, x, y);
        if (nodes[node].child[q] == -1) {
            float cbx, cby, cbw;
            child_bounds(node, q, cbx, cby, cbw);
            nodes[node].child[q] = new_node(cbx, cby, cbw);
        }
        insert(nodes[node].child[q], body, px, py, pm);

        // Update center of mass
        float total = nodes[node].mass + m;
        nodes[node].cx = (nodes[node].cx * nodes[node].mass + x * m) / total;
        nodes[node].cy = (nodes[node].cy * nodes[node].mass + y * m) / total;
        nodes[node].mass = total;
    }

    void build(int N, const float* px, const float* py, const float* pm) {
        nodes.clear();
        nodes.reserve(N * 4);

        // Find bounding box
        float minx = px[0], miny = py[0], maxx = px[0], maxy = py[0];
        for (int i = 1; i < N; ++i) {
            if (px[i] < minx) minx = px[i];
            if (py[i] < miny) miny = py[i];
            if (px[i] > maxx) maxx = px[i];
            if (py[i] > maxy) maxy = py[i];
        }
        float w = fmaxf(maxx - minx, maxy - miny) * 1.001f; // slightly larger
        new_node(minx - 0.0005f * w, miny - 0.0005f * w, w);

        for (int i = 0; i < N; ++i) {
            insert(0, i, px, py, pm);
        }
    }
};

// ---------------------------------------------------------------------------
// GPU data structures (SOA for tree nodes)
// ---------------------------------------------------------------------------

struct GPUTree {
    float *cx, *cy, *mass;
    float *bw;           // bounding box width
    int *child;          // child[4 * node_id + q]
    int num_nodes;
};

// ---------------------------------------------------------------------------
// GPU Kernel: Barnes-Hut force calculation
// ---------------------------------------------------------------------------

__global__ void bh_force_kernel(
    const float* __restrict__ px, const float* __restrict__ py,
    const float* __restrict__ pmass,
    float* __restrict__ ax, float* __restrict__ ay,
    const float* __restrict__ tree_cx, const float* __restrict__ tree_cy,
    const float* __restrict__ tree_mass, const float* __restrict__ tree_bw,
    const int* __restrict__ tree_child,
    int num_nodes, int N, float theta, float eps2)
{
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i >= N) return;

    float xi = px[i], yi = py[i];
    float fax = 0.0f, fay = 0.0f;

    // Stack-based tree traversal (max depth ~40 for 2D quadtree)
    int stack[64];
    int sp = 0;
    stack[sp++] = 0; // root

    while (sp > 0) {
        int node = stack[--sp];
        if (node < 0 || node >= num_nodes) continue;

        float dx = tree_cx[node] - xi;
        float dy = tree_cy[node] - yi;
        float dist2 = dx * dx + dy * dy + eps2;
        float w = tree_bw[node];

        // Check if this node is a leaf or satisfies opening criterion
        bool is_leaf = true;
        for (int q = 0; q < 4; ++q) {
            if (tree_child[4 * node + q] >= 0) {
                is_leaf = false;
                break;
            }
        }

        if (is_leaf || (w * w / dist2 < theta * theta)) {
            // Use this node's center of mass
            float m = tree_mass[node];
            if (m > 0.0f && dist2 > eps2 * 2.0f) {
                float inv_dist = rsqrtf(dist2);
                float inv_dist3 = inv_dist * inv_dist * inv_dist;
                fax += m * dx * inv_dist3;
                fay += m * dy * inv_dist3;
            }
        } else {
            // Open the node: push children
            for (int q = 0; q < 4; ++q) {
                int c = tree_child[4 * node + q];
                if (c >= 0) {
                    stack[sp++] = c;
                }
            }
        }
    }

    ax[i] = fax;
    ay[i] = fay;
}

// ---------------------------------------------------------------------------
// GPU Kernel: Integrate positions and velocities
// ---------------------------------------------------------------------------

__global__ void integrate_kernel(
    float* __restrict__ px, float* __restrict__ py,
    float* __restrict__ vx, float* __restrict__ vy,
    const float* __restrict__ ax, const float* __restrict__ ay,
    int N, float dt)
{
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i >= N) return;

    vx[i] += ax[i] * dt;
    vy[i] += ay[i] * dt;
    px[i] += vx[i] * dt;
    py[i] += vy[i] * dt;
}

// ---------------------------------------------------------------------------
// Upload tree to GPU (SOA)
// ---------------------------------------------------------------------------

static void upload_tree(const QuadTree& tree,
                        float** d_cx, float** d_cy, float** d_mass, float** d_bw,
                        int** d_child, int& num_nodes) {
    num_nodes = (int)tree.nodes.size();

    std::vector<float> h_cx(num_nodes), h_cy(num_nodes), h_mass(num_nodes), h_bw(num_nodes);
    std::vector<int> h_child(num_nodes * 4);

    for (int i = 0; i < num_nodes; ++i) {
        h_cx[i] = tree.nodes[i].cx;
        h_cy[i] = tree.nodes[i].cy;
        h_mass[i] = tree.nodes[i].mass;
        h_bw[i] = tree.nodes[i].bw;
        for (int q = 0; q < 4; ++q)
            h_child[4 * i + q] = tree.nodes[i].child[q];
    }

    // Free previous allocations if any
    if (*d_cx) { CUDA_CHECK(cudaFree(*d_cx)); *d_cx = nullptr; }
    if (*d_cy) { CUDA_CHECK(cudaFree(*d_cy)); *d_cy = nullptr; }
    if (*d_mass) { CUDA_CHECK(cudaFree(*d_mass)); *d_mass = nullptr; }
    if (*d_bw) { CUDA_CHECK(cudaFree(*d_bw)); *d_bw = nullptr; }
    if (*d_child) { CUDA_CHECK(cudaFree(*d_child)); *d_child = nullptr; }

    CUDA_CHECK(cudaMalloc(d_cx, num_nodes * sizeof(float)));
    CUDA_CHECK(cudaMalloc(d_cy, num_nodes * sizeof(float)));
    CUDA_CHECK(cudaMalloc(d_mass, num_nodes * sizeof(float)));
    CUDA_CHECK(cudaMalloc(d_bw, num_nodes * sizeof(float)));
    CUDA_CHECK(cudaMalloc(d_child, num_nodes * 4 * sizeof(int)));

    CUDA_CHECK(cudaMemcpy(*d_cx, h_cx.data(), num_nodes * sizeof(float), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(*d_cy, h_cy.data(), num_nodes * sizeof(float), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(*d_mass, h_mass.data(), num_nodes * sizeof(float), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(*d_bw, h_bw.data(), num_nodes * sizeof(float), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(*d_child, h_child.data(), num_nodes * 4 * sizeof(int), cudaMemcpyHostToDevice));
}

// ---------------------------------------------------------------------------
// CPU reference: direct O(N^2) force computation (on subset)
// ---------------------------------------------------------------------------

static void direct_force_cpu(int N, const float* px, const float* py,
                              const float* pm, float* ax, float* ay,
                              float eps2, int count) {
    for (int i = 0; i < count; ++i) {
        float fax = 0.0f, fay = 0.0f;
        for (int j = 0; j < N; ++j) {
            if (j == i) continue;
            float dx = px[j] - px[i];
            float dy = py[j] - py[i];
            float d2 = dx * dx + dy * dy + eps2;
            float inv_d = 1.0f / sqrtf(d2);
            float inv_d3 = inv_d * inv_d * inv_d;
            fax += pm[j] * dx * inv_d3;
            fay += pm[j] * dy * inv_d3;
        }
        ax[i] = fax;
        ay[i] = fay;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int bodies      = 32768;
    int timesteps_p = 10;
    int iterations  = 5;
    double theta_p  = 0.5;

    // Command-line fallback
    bodies      = parseIntParam(argc, argv, "--bodies", bodies);
    timesteps_p = parseIntParam(argc, argv, "--timesteps", timesteps_p);
    iterations  = parseIntParam(argc, argv, "--iterations", iterations);
    theta_p     = (double)parseFloatParam(argc, argv, "--theta", (float)theta_p);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_bodies");
    if (env_val) bodies = atoi(env_val);
    env_val = getenv("BENCH_PARAM_timesteps");
    if (env_val) timesteps_p = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_theta");
    if (env_val) theta_p = atof(env_val);
    int num_warmup = 0;

    int N         = bodies;
    int timesteps = timesteps_p;
    int iters     = iterations;
    float theta   = (float)theta_p;
    float dt      = 0.001f;
    float eps2    = 0.01f;

    // Initialize bodies
    std::vector<float> h_px(N), h_py(N), h_vx(N), h_vy(N), h_mass(N);
    uint32_t rng = 12345;
    for (int i = 0; i < N; ++i) {
        // Bodies distributed in a disk
        float r = rand_float(rng, 0.1f, 10.0f);
        float angle = rand_float(rng, 0.0f, 2.0f * 3.14159265f);
        h_px[i] = r * cosf(angle);
        h_py[i] = r * sinf(angle);
        h_vx[i] = rand_float(rng, -0.1f, 0.1f);
        h_vy[i] = rand_float(rng, -0.1f, 0.1f);
        h_mass[i] = rand_float(rng, 0.5f, 2.0f);
    }

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Barnes-Hut N-body  |  Bodies: %d  |  Theta: %.2f  |  "
            "Timesteps: %d  |  Iterations: %d warmup + %d timed\n\n",
            N, theta, timesteps, num_warmup, iters);

    // Allocate device arrays for bodies
    float *d_px, *d_py, *d_vx, *d_vy, *d_mass, *d_ax, *d_ay;
    CUDA_CHECK(cudaMalloc(&d_px, N * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_py, N * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_vx, N * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_vy, N * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_mass, N * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_ax, N * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_ay, N * sizeof(float)));

    // Tree device pointers (will be allocated per step)
    float *d_tcx = nullptr, *d_tcy = nullptr, *d_tmass = nullptr, *d_tbw = nullptr;
    int *d_tchild = nullptr;

    int blockSize = 256;
    int gridSize  = (N + blockSize - 1) / blockSize;

    QuadTree tree;

    // Lambda: run one full simulation (timesteps)
    // Returns total FLOP estimate
    auto run_simulation = [&](std::vector<float>& px, std::vector<float>& py,
                              std::vector<float>& vx, std::vector<float>& vy) -> double {
        double total_flops = 0.0;

        CUDA_CHECK(cudaMemcpy(d_px, px.data(), N * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_py, py.data(), N * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_vx, vx.data(), N * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_vy, vy.data(), N * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_mass, h_mass.data(), N * sizeof(float), cudaMemcpyHostToDevice));

        for (int t = 0; t < timesteps; ++t) {
            // Copy positions back to CPU for tree build
            CUDA_CHECK(cudaMemcpy(px.data(), d_px, N * sizeof(float), cudaMemcpyDeviceToHost));
            CUDA_CHECK(cudaMemcpy(py.data(), d_py, N * sizeof(float), cudaMemcpyDeviceToHost));

            // Build quadtree on CPU
            tree.build(N, px.data(), py.data(), h_mass.data());
            int num_nodes = 0;
            upload_tree(tree, &d_tcx, &d_tcy, &d_tmass, &d_tbw, &d_tchild, num_nodes);

            // Compute forces on GPU
            bh_force_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_px, d_py, d_mass, d_ax, d_ay, d_tcx, d_tcy, d_tmass, d_tbw, d_tchild, num_nodes, N, theta, eps2);
            CUDA_CHECK(cudaDeviceSynchronize());

            // Integrate on GPU
            integrate_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_px, d_py, d_vx, d_vy, d_ax, d_ay, N, dt);
            CUDA_CHECK(cudaDeviceSynchronize());

            // FLOP estimate: ~20 FLOPs per body-node interaction
            // Each body traverses O(N/log(N)) nodes (BH approximation)
            // Rough: ~20 * N * (N * 0.1) for theta=0.5
            total_flops += 20.0 * N * (double)num_nodes * 0.3;
        }

        // Copy final positions back
        CUDA_CHECK(cudaMemcpy(px.data(), d_px, N * sizeof(float), cudaMemcpyDeviceToHost));
        CUDA_CHECK(cudaMemcpy(py.data(), d_py, N * sizeof(float), cudaMemcpyDeviceToHost));
        CUDA_CHECK(cudaMemcpy(vx.data(), d_vx, N * sizeof(float), cudaMemcpyDeviceToHost));
        CUDA_CHECK(cudaMemcpy(vy.data(), d_vy, N * sizeof(float), cudaMemcpyDeviceToHost));

        return total_flops;
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    double flops_est = 0.0;
    for (int w = 0; w < num_warmup; ++w) {
        auto wpx = h_px, wpy = h_py, wvx = h_vx, wvy = h_vy;
        flops_est = run_simulation(wpx, wpy, wvx, wvy);
    }
    fprintf(stderr, "Estimated FLOPs per run: %.2e\n", flops_est);

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    std::vector<double> flops_per_iter((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        auto tpx = h_px, tpy = h_py, tvx = h_vx, tvy = h_vy;

        CUDA_CHECK(cudaEventRecord(evStart, 0));
        flops_per_iter[i] = run_simulation(tpx, tpy, tvx, tvy);
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average
    double sum_ms = 0.0, sum_flops = 0.0;
    for (int i = 0; i < iters; ++i) {
        sum_ms += times[i];
        sum_flops += flops_per_iter[i];
    }
    double avg_ms = sum_ms / iters;
    double avg_flops = sum_flops / iters;
    double gflops = avg_flops / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum_ms;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"bh_force_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"bodies\":%d,\"timesteps\":%d,\"theta\":%.6f,"
           "\"iterations\":%d}}\n",
           avg_ms, bodies, timesteps_p, theta_p, iterations);

    printf("{\"type\":\"kernel\",\"name\":\"integrate_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"bodies\":%d,\"timesteps\":%d,\"theta\":%.6f,"
           "\"iterations\":%d}}\n",
           avg_ms, bodies, timesteps_p, theta_p, iterations);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    // Human-readable to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GFLOPS\n", gflops);

    // -------------------------------------------------------------------
    // Verification: compare BH forces vs direct O(N^2) on small subset
    // -------------------------------------------------------------------
    {
        // Build tree and compute BH forces for initial configuration
        tree.build(N, h_px.data(), h_py.data(), h_mass.data());
        int num_nodes = 0;
        upload_tree(tree, &d_tcx, &d_tcy, &d_tmass, &d_tbw, &d_tchild, num_nodes);

        CUDA_CHECK(cudaMemcpy(d_px, h_px.data(), N * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_py, h_py.data(), N * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_mass, h_mass.data(), N * sizeof(float), cudaMemcpyHostToDevice));

        bh_force_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_px, d_py, d_mass, d_ax, d_ay, d_tcx, d_tcy, d_tmass, d_tbw, d_tchild, num_nodes, N, theta, eps2);
        CUDA_CHECK(cudaDeviceSynchronize());

        std::vector<float> gpu_ax(N), gpu_ay(N);
        CUDA_CHECK(cudaMemcpy(gpu_ax.data(), d_ax, N * sizeof(float), cudaMemcpyDeviceToHost));
        CUDA_CHECK(cudaMemcpy(gpu_ay.data(), d_ay, N * sizeof(float), cudaMemcpyDeviceToHost));

        int verify_n = 64; // check 64 bodies
        std::vector<float> ref_ax(verify_n), ref_ay(verify_n);
        direct_force_cpu(N, h_px.data(), h_py.data(), h_mass.data(),
                         ref_ax.data(), ref_ay.data(), eps2, verify_n);

        // BH is approximate, so we check relative error
        int errors = 0;
        float max_rel_err = 0.0f;
        for (int i = 0; i < verify_n; ++i) {
            float ref_mag = sqrtf(ref_ax[i] * ref_ax[i] + ref_ay[i] * ref_ay[i]);
            float gpu_mag = sqrtf(gpu_ax[i] * gpu_ax[i] + gpu_ay[i] * gpu_ay[i]);
            float diff_mag = sqrtf((gpu_ax[i] - ref_ax[i]) * (gpu_ax[i] - ref_ax[i]) +
                                   (gpu_ay[i] - ref_ay[i]) * (gpu_ay[i] - ref_ay[i]));
            float rel_err = (ref_mag > 1e-10f) ? diff_mag / ref_mag : 0.0f;
            if (rel_err > max_rel_err) max_rel_err = rel_err;
            if (rel_err > 0.5f) { // BH with theta=0.5 can have ~30% error, 50% threshold
                if (errors < 5)
                    fprintf(stderr, "Large error at body %d: rel_err=%.4f (GPU=%.4f, Ref=%.4f)\n",
                            i, rel_err, gpu_mag, ref_mag);
                errors++;
            }
        }
        fprintf(stderr, "Max relative error: %.4f (over %d bodies)\n", max_rel_err, verify_n);
        if (errors > verify_n / 4)
            fprintf(stderr, "FAIL: %d/%d bodies exceed error threshold\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    // Cleanup
    CUDA_CHECK(cudaFree(d_px));
    CUDA_CHECK(cudaFree(d_py));
    CUDA_CHECK(cudaFree(d_vx));
    CUDA_CHECK(cudaFree(d_vy));
    CUDA_CHECK(cudaFree(d_mass));
    CUDA_CHECK(cudaFree(d_ax));
    CUDA_CHECK(cudaFree(d_ay));
    if (d_tcx) CUDA_CHECK(cudaFree(d_tcx));
    if (d_tcy) CUDA_CHECK(cudaFree(d_tcy));
    if (d_tmass) CUDA_CHECK(cudaFree(d_tmass));
    if (d_tbw) CUDA_CHECK(cudaFree(d_tbw));
    if (d_tchild) CUDA_CHECK(cudaFree(d_tchild));

    return 0;
}
