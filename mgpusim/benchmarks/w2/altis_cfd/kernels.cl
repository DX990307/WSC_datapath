// Altis euler3d (unstructured-grid Euler solver), gpu_benchmarks
// tier2/altis_cfd, W2 port. Kernel bodies follow the pinned Altis
// euler3d.cu (via the tier2 port): copy, step factor, flux (4 face
// neighbours read through the neighbour list), RK update. SoA layout:
// variable v of cell i at variables[i + v * nelr]. The upstream __constant__
// far-field values are read from a small global buffer ff (17 floats):
// ff[0..4] = far-field variables, ff[5..7] momentum_x flux contribution,
// ff[8..10] momentum_y, ff[11..13] momentum_z, ff[14..16] density energy.

#define GAMMA 1.4f
#define NNB 4
#define VAR_DENSITY 0
#define VAR_MOMENTUM 1
#define VAR_DENSITY_ENERGY 4
#define RK 3

__kernel void cfd_copy(const int nelr, __global const float *state,
                       __global float *old) {
  const int i = get_global_id(0);
  for (int k = 0; k < 5; ++k) old[i + k * nelr] = state[i + k * nelr];
}

__kernel void cfd_step_factor(const int nelr, __global const float *variables,
                              __global const float *areas,
                              __global float *step_factors) {
  const int i = get_global_id(0);
  const float density = variables[i + VAR_DENSITY * nelr];
  const float mx = variables[i + (VAR_MOMENTUM + 0) * nelr];
  const float my = variables[i + (VAR_MOMENTUM + 1) * nelr];
  const float mz = variables[i + (VAR_MOMENTUM + 2) * nelr];
  const float de = variables[i + VAR_DENSITY_ENERGY * nelr];
  const float vx = mx / density, vy = my / density, vz = mz / density;
  const float speed_sqd = vx * vx + vy * vy + vz * vz;
  const float pressure = (GAMMA - 1.0f) * (de - 0.5f * density * speed_sqd);
  const float sos = sqrt(GAMMA * pressure / density);
  step_factors[i] = 0.5f / (sqrt(areas[i]) * (sqrt(speed_sqd) + sos));
}

__kernel void cfd_flux(const int nelr, __global const int *nbrs,
                       __global const float *normals,
                       __global const float *variables,
                       __global float *fluxes, __global const float *ff) {
  const float smoothing_coefficient = 0.2f;
  const int i = get_global_id(0);

  const float density_i = variables[i + VAR_DENSITY * nelr];
  const float mix = variables[i + (VAR_MOMENTUM + 0) * nelr];
  const float miy = variables[i + (VAR_MOMENTUM + 1) * nelr];
  const float miz = variables[i + (VAR_MOMENTUM + 2) * nelr];
  const float de_i = variables[i + VAR_DENSITY_ENERGY * nelr];
  const float vix = mix / density_i, viy = miy / density_i, viz = miz / density_i;
  const float speed_sqd_i = vix * vix + viy * viy + viz * viz;
  const float speed_i = sqrt(speed_sqd_i);
  const float pressure_i = (GAMMA - 1.0f) * (de_i - 0.5f * density_i * speed_sqd_i);
  const float sos_i = sqrt(GAMMA * pressure_i / density_i);
  // flux contributions of cell i
  const float fxi_x = vix * mix + pressure_i, fxi_y = vix * miy, fxi_z = vix * miz;
  const float fyi_x = fxi_y, fyi_y = viy * miy + pressure_i, fyi_z = viy * miz;
  const float fzi_x = fxi_z, fzi_y = fyi_z, fzi_z = viz * miz + pressure_i;
  const float dep_i = de_i + pressure_i;
  const float fei_x = vix * dep_i, fei_y = viy * dep_i, fei_z = viz * dep_i;

  float flux_d = 0.0f, flux_mx = 0.0f, flux_my = 0.0f, flux_mz = 0.0f, flux_e = 0.0f;

  for (int j = 0; j < NNB; j++) {
    const int nb = nbrs[i + j * nelr];
    const float nx = normals[i + (j + 0 * NNB) * nelr];
    const float ny = normals[i + (j + 1 * NNB) * nelr];
    const float nz = normals[i + (j + 2 * NNB) * nelr];
    const float normal_len = sqrt(nx * nx + ny * ny + nz * nz);
    if (nb >= 0) {
      const float density_nb = variables[nb + VAR_DENSITY * nelr];
      const float mnx = variables[nb + (VAR_MOMENTUM + 0) * nelr];
      const float mny = variables[nb + (VAR_MOMENTUM + 1) * nelr];
      const float mnz = variables[nb + (VAR_MOMENTUM + 2) * nelr];
      const float de_nb = variables[nb + VAR_DENSITY_ENERGY * nelr];
      const float vnx = mnx / density_nb, vny = mny / density_nb, vnz = mnz / density_nb;
      const float speed_sqd_nb = vnx * vnx + vny * vny + vnz * vnz;
      const float pressure_nb = (GAMMA - 1.0f) * (de_nb - 0.5f * density_nb * speed_sqd_nb);
      const float sos_nb = sqrt(GAMMA * pressure_nb / density_nb);
      const float fxn_x = vnx * mnx + pressure_nb, fxn_y = vnx * mny, fxn_z = vnx * mnz;
      const float fyn_x = fxn_y, fyn_y = vny * mny + pressure_nb, fyn_z = vny * mnz;
      const float fzn_x = fxn_z, fzn_y = fyn_z, fzn_z = vnz * mnz + pressure_nb;
      const float dep_nb = de_nb + pressure_nb;
      const float fen_x = vnx * dep_nb, fen_y = vny * dep_nb, fen_z = vnz * dep_nb;

      // artificial viscosity
      float factor = -normal_len * smoothing_coefficient * 0.5f *
                     (speed_i + sqrt(speed_sqd_nb) + sos_i + sos_nb);
      flux_d += factor * (density_i - density_nb);
      flux_e += factor * (de_i - de_nb);
      flux_mx += factor * (mix - mnx);
      flux_my += factor * (miy - mny);
      flux_mz += factor * (miz - mnz);

      factor = 0.5f * nx;
      flux_d += factor * (mnx + mix);
      flux_e += factor * (fen_x + fei_x);
      flux_mx += factor * (fxn_x + fxi_x);
      flux_my += factor * (fyn_x + fyi_x);
      flux_mz += factor * (fzn_x + fzi_x);

      factor = 0.5f * ny;
      flux_d += factor * (mny + miy);
      flux_e += factor * (fen_y + fei_y);
      flux_mx += factor * (fxn_y + fxi_y);
      flux_my += factor * (fyn_y + fyi_y);
      flux_mz += factor * (fzn_y + fzi_y);

      factor = 0.5f * nz;
      flux_d += factor * (mnz + miz);
      flux_e += factor * (fen_z + fei_z);
      flux_mx += factor * (fxn_z + fxi_z);
      flux_my += factor * (fyn_z + fyi_z);
      flux_mz += factor * (fzn_z + fzi_z);
    } else if (nb == -1) {  // wing (slip wall) boundary
      flux_mx += nx * pressure_i;
      flux_my += ny * pressure_i;
      flux_mz += nz * pressure_i;
    } else if (nb == -2) {  // far field boundary
      float factor = 0.5f * nx;
      flux_d += factor * (ff[VAR_MOMENTUM + 0] + mix);
      flux_e += factor * (ff[14] + fei_x);
      flux_mx += factor * (ff[5] + fxi_x);
      flux_my += factor * (ff[8] + fyi_x);
      flux_mz += factor * (ff[11] + fzi_x);

      factor = 0.5f * ny;
      flux_d += factor * (ff[VAR_MOMENTUM + 1] + miy);
      flux_e += factor * (ff[15] + fei_y);
      flux_mx += factor * (ff[6] + fxi_y);
      flux_my += factor * (ff[9] + fyi_y);
      flux_mz += factor * (ff[12] + fzi_y);

      factor = 0.5f * nz;
      flux_d += factor * (ff[VAR_MOMENTUM + 2] + miz);
      flux_e += factor * (ff[16] + fei_z);
      flux_mx += factor * (ff[7] + fxi_z);
      flux_my += factor * (ff[10] + fyi_z);
      flux_mz += factor * (ff[13] + fzi_z);
    }
  }

  fluxes[i + VAR_DENSITY * nelr] = flux_d;
  fluxes[i + (VAR_MOMENTUM + 0) * nelr] = flux_mx;
  fluxes[i + (VAR_MOMENTUM + 1) * nelr] = flux_my;
  fluxes[i + (VAR_MOMENTUM + 2) * nelr] = flux_mz;
  fluxes[i + VAR_DENSITY_ENERGY * nelr] = flux_e;
}

__kernel void cfd_rk_update(const int j, const int nelr,
                            __global const float *old_variables,
                            __global float *variables,
                            __global const float *step_factors,
                            __global const float *fluxes) {
  const int i = get_global_id(0);
  const float factor = step_factors[i] / (float)(RK + 1 - j);
  for (int k = 0; k < 5; ++k)
    variables[i + k * nelr] = old_variables[i + k * nelr] + factor * fluxes[i + k * nelr];
}
