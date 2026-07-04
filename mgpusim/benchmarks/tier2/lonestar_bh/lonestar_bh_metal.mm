/**
 * lonestar_bh_metal.mm — Apple Metal host for Barnes-Hut N-body benchmark.
 *
 * Simplified Barnes-Hut: build 2D quadtree on CPU, compute forces on GPU.
 *
 * Usage:
 *   ./lonestar_bh [--bodies N] [--timesteps T] [--iterations I]
 *
 * Output (stdout): CSV — lonestar_bh,<N_bodies>,<time_ms>,<GFLOPS>
 * Output (stderr): human-readable results
 *
 * Build:
 *   make PLATFORM=metal
 */

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Timing helpers
// ---------------------------------------------------------------------------

static double ticks_to_ms(uint64_t ticks) {
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-6;
}

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
// Quadtree node
// ---------------------------------------------------------------------------

struct BHNode {
    float cx, cy;
    float mass;
    float bx, by;
    float bw;
    int child[4]; // -1 = empty
    int body_idx; // -1 if internal node
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

        if (nodes[node].mass == 0.0f && nodes[node].body_idx == -1) {
            nodes[node].body_idx = body;
            nodes[node].cx = x;
            nodes[node].cy = y;
            nodes[node].mass = m;
            return;
        }

        if (nodes[node].body_idx >= 0) {
            int old_body = nodes[node].body_idx;
            nodes[node].body_idx = -1;

            int oq = quadrant(node, px[old_body], py[old_body]);
            if (nodes[node].child[oq] == -1) {
                float cbx, cby, cbw;
                child_bounds(node, oq, cbx, cby, cbw);
                nodes[node].child[oq] = new_node(cbx, cby, cbw);
            }
            insert(nodes[node].child[oq], old_body, px, py, pm);
        }

        int q = quadrant(node, x, y);
        if (nodes[node].child[q] == -1) {
            float cbx, cby, cbw;
            child_bounds(node, q, cbx, cby, cbw);
            nodes[node].child[q] = new_node(cbx, cby, cbw);
        }
        insert(nodes[node].child[q], body, px, py, pm);

        float total = nodes[node].mass + m;
        nodes[node].cx = (nodes[node].cx * nodes[node].mass + x * m) / total;
        nodes[node].cy = (nodes[node].cy * nodes[node].mass + y * m) / total;
        nodes[node].mass = total;
    }

    void build(int N, const float* px, const float* py, const float* pm) {
        nodes.clear();
        nodes.reserve(N * 4);

        float minx = px[0], miny = py[0], maxx = px[0], maxy = py[0];
        for (int i = 1; i < N; ++i) {
            if (px[i] < minx) minx = px[i];
            if (py[i] < miny) miny = py[i];
            if (px[i] > maxx) maxx = px[i];
            if (py[i] > maxy) maxy = py[i];
        }
        float w = fmaxf(maxx - minx, maxy - miny) * 1.001f;
        new_node(minx - 0.0005f * w, miny - 0.0005f * w, w);

        for (int i = 0; i < N; ++i) {
            insert(0, i, px, py, pm);
        }
    }
};

// ---------------------------------------------------------------------------
// GPU param structs (must match .metal)
// ---------------------------------------------------------------------------

struct ForceParams {
    uint32_t num_nodes;
    uint32_t N;
    float theta_sq;
    float eps2;
};

struct IntegrateParams {
    uint32_t N;
    float dt;
};

// ---------------------------------------------------------------------------
// CPU reference: direct O(N^2) force computation
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
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
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
        float r = rand_float(rng, 0.1f, 10.0f);
        float angle = rand_float(rng, 0.0f, 2.0f * 3.14159265f);
        h_px[i] = r * cosf(angle);
        h_py[i] = r * sinf(angle);
        h_vx[i] = rand_float(rng, -0.1f, 0.1f);
        h_vy[i] = rand_float(rng, -0.1f, 0.1f);
        h_mass[i] = rand_float(rng, 0.5f, 2.0f);
    }

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Barnes-Hut N-body  |  Bodies: %d  |  Theta: %.2f  |  "
            "Timesteps: %d  |  Iterations: %d warmup + %d timed\n\n",
            N, theta, timesteps, num_warmup, iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shaders
    // -------------------------------------------------------------------
    NSError* err = nil;
    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"lonestar_bh.metal"];

    id<MTLLibrary> library = nil;
    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString* metalSrc = [NSString stringWithContentsOfFile:srcPath
                                                       encoding:NSUTF8StringEncoding
                                                          error:nil];
        MTLCompileOptions* opts = [MTLCompileOptions new];
        library = [device newLibraryWithSource:metalSrc options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: lonestar_bh.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnForce = [library newFunctionWithName:@"bh_force_kernel"];
    id<MTLFunction> fnInteg = [library newFunctionWithName:@"integrate_kernel"];
    if (!fnForce || !fnInteg) {
        fprintf(stderr, "Error: kernel functions not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoForce =
        [device newComputePipelineStateWithFunction:fnForce error:&err];
    id<MTLComputePipelineState> psoInteg =
        [device newComputePipelineStateWithFunction:fnInteg error:&err];
    if (!psoForce || !psoInteg) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate body buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufPx   = [device newBufferWithLength:N * sizeof(float)
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufPy   = [device newBufferWithLength:N * sizeof(float)
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufVx   = [device newBufferWithLength:N * sizeof(float)
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufVy   = [device newBufferWithLength:N * sizeof(float)
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufMass = [device newBufferWithBytes:h_mass.data()
                                                length:N * sizeof(float)
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufAx   = [device newBufferWithLength:N * sizeof(float)
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufAy   = [device newBufferWithLength:N * sizeof(float)
                                                options:MTLResourceStorageModeShared];

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    QuadTree tree;

    // Lambda: run simulation
    auto run_simulation = [&](std::vector<float>& px, std::vector<float>& py,
                              std::vector<float>& vx, std::vector<float>& vy) -> double {
        double total_flops = 0.0;

        memcpy(bufPx.contents, px.data(), N * sizeof(float));
        memcpy(bufPy.contents, py.data(), N * sizeof(float));
        memcpy(bufVx.contents, vx.data(), N * sizeof(float));
        memcpy(bufVy.contents, vy.data(), N * sizeof(float));

        for (int t = 0; t < timesteps; ++t) {
            // Copy positions back for CPU tree build
            memcpy(px.data(), bufPx.contents, N * sizeof(float));
            memcpy(py.data(), bufPy.contents, N * sizeof(float));

            // Build quadtree on CPU
            tree.build(N, px.data(), py.data(), h_mass.data());
            int num_nodes = (int)tree.nodes.size();

            // Upload tree to GPU buffers
            std::vector<float> tcx(num_nodes), tcy(num_nodes), tmass(num_nodes), tbw(num_nodes);
            std::vector<int> tchild(num_nodes * 4);
            for (int n = 0; n < num_nodes; ++n) {
                tcx[n] = tree.nodes[n].cx;
                tcy[n] = tree.nodes[n].cy;
                tmass[n] = tree.nodes[n].mass;
                tbw[n] = tree.nodes[n].bw;
                for (int q = 0; q < 4; ++q)
                    tchild[4 * n + q] = tree.nodes[n].child[q];
            }

            id<MTLBuffer> bufTcx   = [device newBufferWithBytes:tcx.data()
                                                          length:num_nodes * sizeof(float)
                                                         options:MTLResourceStorageModeShared];
            id<MTLBuffer> bufTcy   = [device newBufferWithBytes:tcy.data()
                                                          length:num_nodes * sizeof(float)
                                                         options:MTLResourceStorageModeShared];
            id<MTLBuffer> bufTmass = [device newBufferWithBytes:tmass.data()
                                                          length:num_nodes * sizeof(float)
                                                         options:MTLResourceStorageModeShared];
            id<MTLBuffer> bufTbw   = [device newBufferWithBytes:tbw.data()
                                                          length:num_nodes * sizeof(float)
                                                         options:MTLResourceStorageModeShared];
            id<MTLBuffer> bufTchild = [device newBufferWithBytes:tchild.data()
                                                           length:num_nodes * 4 * sizeof(int)
                                                          options:MTLResourceStorageModeShared];

            ForceParams fp;
            fp.num_nodes = (uint32_t)num_nodes;
            fp.N = (uint32_t)N;
            fp.theta_sq = theta * theta;
            fp.eps2 = eps2;

            // Force kernel
            {
                id<MTLCommandBuffer> cb = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:psoForce];
                [enc setBuffer:bufPx     offset:0 atIndex:0];
                [enc setBuffer:bufPy     offset:0 atIndex:1];
                [enc setBuffer:bufMass   offset:0 atIndex:2];
                [enc setBuffer:bufAx     offset:0 atIndex:3];
                [enc setBuffer:bufAy     offset:0 atIndex:4];
                [enc setBuffer:bufTcx    offset:0 atIndex:5];
                [enc setBuffer:bufTcy    offset:0 atIndex:6];
                [enc setBuffer:bufTmass  offset:0 atIndex:7];
                [enc setBuffer:bufTbw    offset:0 atIndex:8];
                [enc setBuffer:bufTchild offset:0 atIndex:9];
                [enc setBytes:&fp length:sizeof(fp) atIndex:10];
                [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
                [enc endEncoding];
                [cb commit];
                [cb waitUntilCompleted];
            }

            // Integrate kernel
            {
                IntegrateParams ip;
                ip.N = (uint32_t)N;
                ip.dt = dt;

                id<MTLCommandBuffer> cb = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:psoInteg];
                [enc setBuffer:bufPx offset:0 atIndex:0];
                [enc setBuffer:bufPy offset:0 atIndex:1];
                [enc setBuffer:bufVx offset:0 atIndex:2];
                [enc setBuffer:bufVy offset:0 atIndex:3];
                [enc setBuffer:bufAx offset:0 atIndex:4];
                [enc setBuffer:bufAy offset:0 atIndex:5];
                [enc setBytes:&ip length:sizeof(ip) atIndex:6];
                [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
                [enc endEncoding];
                [cb commit];
                [cb waitUntilCompleted];
            }

            total_flops += 20.0 * N * (double)num_nodes * 0.3;
        }

        memcpy(px.data(), bufPx.contents, N * sizeof(float));
        memcpy(py.data(), bufPy.contents, N * sizeof(float));
        memcpy(vx.data(), bufVx.contents, N * sizeof(float));
        memcpy(vy.data(), bufVy.contents, N * sizeof(float));

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
    std::vector<double> times((size_t)iters);
    std::vector<double> flops_per_iter((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        auto tpx = h_px, tpy = h_py, tvx = h_vx, tvy = h_vy;

        uint64_t t0 = mach_absolute_time();
        flops_per_iter[i] = run_simulation(tpx, tpy, tvx, tvy);
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

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
        tree.build(N, h_px.data(), h_py.data(), h_mass.data());
        int num_nodes = (int)tree.nodes.size();

        std::vector<float> tcx(num_nodes), tcy(num_nodes), tmass_v(num_nodes), tbw(num_nodes);
        std::vector<int> tchild(num_nodes * 4);
        for (int n = 0; n < num_nodes; ++n) {
            tcx[n] = tree.nodes[n].cx;
            tcy[n] = tree.nodes[n].cy;
            tmass_v[n] = tree.nodes[n].mass;
            tbw[n] = tree.nodes[n].bw;
            for (int q = 0; q < 4; ++q)
                tchild[4 * n + q] = tree.nodes[n].child[q];
        }

        memcpy(bufPx.contents, h_px.data(), N * sizeof(float));
        memcpy(bufPy.contents, h_py.data(), N * sizeof(float));

        id<MTLBuffer> bufTcx   = [device newBufferWithBytes:tcx.data()
                                                      length:num_nodes * sizeof(float)
                                                     options:MTLResourceStorageModeShared];
        id<MTLBuffer> bufTcy   = [device newBufferWithBytes:tcy.data()
                                                      length:num_nodes * sizeof(float)
                                                     options:MTLResourceStorageModeShared];
        id<MTLBuffer> bufTmass2 = [device newBufferWithBytes:tmass_v.data()
                                                      length:num_nodes * sizeof(float)
                                                     options:MTLResourceStorageModeShared];
        id<MTLBuffer> bufTbw   = [device newBufferWithBytes:tbw.data()
                                                      length:num_nodes * sizeof(float)
                                                     options:MTLResourceStorageModeShared];
        id<MTLBuffer> bufTchild = [device newBufferWithBytes:tchild.data()
                                                       length:num_nodes * 4 * sizeof(int)
                                                      options:MTLResourceStorageModeShared];

        ForceParams fp;
        fp.num_nodes = (uint32_t)num_nodes;
        fp.N = (uint32_t)N;
        fp.theta_sq = theta * theta;
        fp.eps2 = eps2;

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:psoForce];
        [enc setBuffer:bufPx     offset:0 atIndex:0];
        [enc setBuffer:bufPy     offset:0 atIndex:1];
        [enc setBuffer:bufMass   offset:0 atIndex:2];
        [enc setBuffer:bufAx     offset:0 atIndex:3];
        [enc setBuffer:bufAy     offset:0 atIndex:4];
        [enc setBuffer:bufTcx    offset:0 atIndex:5];
        [enc setBuffer:bufTcy    offset:0 atIndex:6];
        [enc setBuffer:bufTmass2 offset:0 atIndex:7];
        [enc setBuffer:bufTbw    offset:0 atIndex:8];
        [enc setBuffer:bufTchild offset:0 atIndex:9];
        [enc setBytes:&fp length:sizeof(fp) atIndex:10];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        float* gpu_ax = (float*)bufAx.contents;
        float* gpu_ay = (float*)bufAy.contents;

        int verify_n = 64;
        std::vector<float> ref_ax(verify_n), ref_ay(verify_n);
        direct_force_cpu(N, h_px.data(), h_py.data(), h_mass.data(),
                         ref_ax.data(), ref_ay.data(), eps2, verify_n);

        int errors = 0;
        float max_rel_err = 0.0f;
        for (int i = 0; i < verify_n; ++i) {
            float ref_mag = sqrtf(ref_ax[i] * ref_ax[i] + ref_ay[i] * ref_ay[i]);
            float diff_mag = sqrtf((gpu_ax[i] - ref_ax[i]) * (gpu_ax[i] - ref_ax[i]) +
                                   (gpu_ay[i] - ref_ay[i]) * (gpu_ay[i] - ref_ay[i]));
            float rel_err = (ref_mag > 1e-10f) ? diff_mag / ref_mag : 0.0f;
            if (rel_err > max_rel_err) max_rel_err = rel_err;
            if (rel_err > 0.5f) {
                if (errors < 5)
                    fprintf(stderr, "Large error at body %d: rel_err=%.4f\n", i, rel_err);
                errors++;
            }
        }
        fprintf(stderr, "Max relative error: %.4f (over %d bodies)\n", max_rel_err, verify_n);
        if (errors > verify_n / 4)
            fprintf(stderr, "FAIL: %d/%d bodies exceed error threshold\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
