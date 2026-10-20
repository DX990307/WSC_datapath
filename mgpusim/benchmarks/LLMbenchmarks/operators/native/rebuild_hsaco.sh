#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MGPUSIM_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd)"
VANILLA_ROOT="$(cd "$MGPUSIM_ROOT/.." && pwd)"

CLANG="${CLANG:-clang}"
LD_LLD="${LD_LLD:-ld.lld}"
TARGET="${TARGET:-amdgcn-amd-amdhsa}"
GPU="${GPU:-fiji}"
CODE_OBJECT_VERSION="${CODE_OBJECT_VERSION:-2}"
TMPDIR="${TMPDIR:-/tmp}"
BUILD_GO=0

usage() {
  cat <<EOF
Usage: $(basename "$0") [--go]

Rebuild all LLM native OpenCL kernels into .hsaco files.

Environment overrides:
  CLANG                 default: clang
  LD_LLD                default: ld.lld
  ROCM_DEVICE_LIB_PATH  default: auto-detect
  TARGET                default: amdgcn-amd-amdhsa
  GPU                   default: fiji
  CODE_OBJECT_VERSION   default: 2

Options:
  --go                  also rebuild mgpusim LLM packages and akkalat/baseline
  -h, --help            show this help
EOF
}

while (($#)); do
  case "$1" in
    --go)
      BUILD_GO=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

detect_rocm_device_lib_path() {
  if [[ -n "${ROCM_DEVICE_LIB_PATH:-}" ]]; then
    echo "$ROCM_DEVICE_LIB_PATH"
    return
  fi

  local candidates=(
    /usr/lib/x86_64-linux-gnu/amdgcn/bitcode
    /opt/rocm/amdgcn/bitcode
    /opt/rocm/lib/bitcode
  )
  local path
  for path in "${candidates[@]}"; do
    if [[ -d "$path" ]]; then
      echo "$path"
      return
    fi
  done

  echo "could not find ROCm device lib path; set ROCM_DEVICE_LIB_PATH" >&2
  exit 1
}

require_tool() {
  local tool="$1"
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "missing required tool: $tool" >&2
    exit 1
  fi
}

require_tool "$CLANG"
require_tool "$LD_LLD"

ROCM_DEVICE_LIB_PATH="$(detect_rocm_device_lib_path)"
OBJ_DIR="$(mktemp -d "$TMPDIR/llm-hsaco.XXXXXX")"
trap 'rm -rf "$OBJ_DIR"' EXIT

kernels=(
  operator
  repeat
  im2col
  maxpooling
  avgpooling
  cross_entropy
  residual_add
  gelu
  layernorm
  embedding_synthetic
  batchnorm2d_inference
  causal_mask
  row_softmax
  gemm
  transfer_copy
  kernels
)

echo "ROCm device libs: $ROCM_DEVICE_LIB_PATH"
echo "Output directory: $SCRIPT_DIR"

for kernel in "${kernels[@]}"; do
  src="$SCRIPT_DIR/$kernel.cl"
  obj="$OBJ_DIR/$kernel.o"
  out="$SCRIPT_DIR/$kernel.hsaco"

  if [[ ! -f "$src" ]]; then
    echo "missing source: $src" >&2
    exit 1
  fi

  echo "CC $kernel.cl"
  "$CLANG" \
    -x cl \
    -target "$TARGET" \
    -mcpu="$GPU" \
    -mcode-object-version="$CODE_OBJECT_VERSION" \
    -cl-std=CL2.0 \
    --rocm-device-lib-path="$ROCM_DEVICE_LIB_PATH" \
    -I "$SCRIPT_DIR" \
    -c "$src" \
    -o "$obj"

  echo "LD $kernel.hsaco"
  "$LD_LLD" -shared "$obj" -o "$out"
done

if ((BUILD_GO)); then
  echo "GO mgpusim LLM packages"
  (
    cd "$MGPUSIM_ROOT"
    GOCACHE="${GOCACHE:-/tmp/gocache}" go build -buildvcs=false \
      ./benchmarks/LLMbenchmarks/operators \
      ./benchmarks/LLMbenchmarks/llmop \
      ./benchmarks/LLMbenchmarks/bert \
      ./benchmarks/LLMbenchmarks/gpt \
      ./benchmarks/LLMbenchmarks/resnet \
      ./samples/llmop
  )

  if [[ -d "$VANILLA_ROOT/akkalat/baseline" ]]; then
    echo "GO akkalat/baseline"
    (
      cd "$VANILLA_ROOT/akkalat/baseline"
      GOCACHE="${GOCACHE:-/tmp/gocache}" go build -buildvcs=false
    )
  fi
fi

echo "done"
