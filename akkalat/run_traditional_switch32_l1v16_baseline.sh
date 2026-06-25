#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

MAX_WORKERS="${MAX_WORKERS:-1}"
MAX_WG="${MAX_WG:-78600}"
TRACE_WARMUP="${TRACE_WARMUP:-600000}"
TRACE_RECORDS="${TRACE_RECORDS:-500000}"

python3 akkalat/runall2.py \
  --benchmarks traditional \
  --configs baseline \
  --switch-latency 32 \
  --l1v-mshr-entries 16 \
  --l1v-max-concurrent-trans 16 \
  --max-wg "${MAX_WG}" \
  --trace-memory-path \
  --trace-memory-path-warmup-accesses "${TRACE_WARMUP}" \
  --trace-memory-path-max-records "${TRACE_RECORDS}" \
  --trace-memory-path-exit-on-complete \
  --report-l2-source \
  --max-workers "${MAX_WORKERS}"
