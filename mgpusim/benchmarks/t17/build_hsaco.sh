#!/usr/bin/env bash
# Rebuild <dir>/kernels.hsaco from <dir>/kernels.cl for the T17 workloads and
# check each result with check_hsaco.sh (fails on scratch, atomics and
# unimplemented flat/ds opcodes). Needs clang with the amdgcn target, ld.lld,
# llvm-objdump, llvm-readelf and the ROCm device libs (yss2, not macOS).
#
# Usage: build_hsaco.sh [dir ...]      (default: every subdirectory with a
#                                       kernels.cl)
#
# check_hsaco.sh covers memory, LDS, SMEM and scratch. ALU opcodes the
# emulator lacks are found by opcheck (needs Go; run it where the simulator
# sources are):
#   llvm-objdump -d --mcpu=fiji <dir>/kernels.hsaco > k.dis
#   go run ./benchmarks/t17/opcheck -emu emu k.dis      (from mgpusim/)
#
# Patterns that clang 14 turns into instructions the simulator lacks, and
# the source-level way around them used in these kernels:
#   >= 64 contiguous used kernarg bytes -> s_load_dwordx16: unused int gap
#   signed (long)int * int indexing     -> v_mad_i64_i32: unsigned/32-bit
#   64-bit shifts of get_local_id(0)    -> v_alignbit_b32: workitem_id_x
#   16-bit constant adds/compares       -> s_addk_i32 / s_cmpk_eq_i32:
#                                          LDS base 0, unrolled const loops
#   LDS arrays/strides of 64 dwords     -> ds_*2st64_b32: interleave/pad
#   4 contiguous aligned LDS loads      -> ds_read_b128: strided layout
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLANG="${CLANG:-clang}"
LD_LLD="${LD_LLD:-ld.lld}"
GPU="${GPU:-fiji}"
ROCM_DEVICE_LIB_PATH="${ROCM_DEVICE_LIB_PATH:-/usr/lib/x86_64-linux-gnu/amdgcn/bitcode}"

dirs=("$@")
if ((${#dirs[@]} == 0)); then
  for d in "$SCRIPT_DIR"/*/; do
    [[ -f "$d/kernels.cl" ]] && dirs+=("$(basename "$d")")
  done
fi

OBJ_DIR="$(mktemp -d "${TMPDIR:-/tmp}/t17-hsaco.XXXXXX")"
trap 'rm -rf "$OBJ_DIR"' EXIT

for d in "${dirs[@]}"; do
  [[ "$d" = /* ]] || d="$SCRIPT_DIR/$d"
  name="$(basename "$d")"
  out="$OBJ_DIR/$name.hsaco"
  "$CLANG" -x cl -target amdgcn-amd-amdhsa -mcpu="$GPU" -mcode-object-version=2 \
    -cl-std=CL2.0 --rocm-device-lib-path="$ROCM_DEVICE_LIB_PATH" \
    -c "$d/kernels.cl" -o "$OBJ_DIR/$name.o"
  "$LD_LLD" -shared "$OBJ_DIR/$name.o" -o "$out"
  # Only install the code object if it passes the check.
  GPU="$GPU" "$SCRIPT_DIR/check_hsaco.sh" "$out"
  cp "$out" "$d/kernels.hsaco"
  echo "built $d/kernels.hsaco"
done
