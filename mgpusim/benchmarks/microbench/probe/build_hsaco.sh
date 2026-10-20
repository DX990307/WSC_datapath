#!/usr/bin/env bash
# Rebuild kernels.hsaco from kernels.cl. Needs clang with the amdgcn target,
# ld.lld, and the ROCm device libs (available on yss2; not on macOS).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLANG="${CLANG:-clang}"
LD_LLD="${LD_LLD:-ld.lld}"
ROCM_DEVICE_LIB_PATH="${ROCM_DEVICE_LIB_PATH:-/usr/lib/x86_64-linux-gnu/amdgcn/bitcode}"
OBJ="$(mktemp "${TMPDIR:-/tmp}/probe.XXXXXX.o")"
trap 'rm -f "$OBJ"' EXIT

"$CLANG" -x cl -target amdgcn-amd-amdhsa -mcpu=fiji -mcode-object-version=2 \
  -cl-std=CL2.0 --rocm-device-lib-path="$ROCM_DEVICE_LIB_PATH" \
  -c "$SCRIPT_DIR/kernels.cl" -o "$OBJ"
"$LD_LLD" -shared "$OBJ" -o "$SCRIPT_DIR/kernels.hsaco"
echo "built $SCRIPT_DIR/kernels.hsaco"
