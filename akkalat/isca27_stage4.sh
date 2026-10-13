#!/usr/bin/env bash
# T13 stage 4: rerun the paper's experiments with the paper's own run scripts
# (runall2.py, CuPathLLM.py, run_cupath_sensitivity.py) plus a GPM preset.
# Run from the repo root on a server; the repo is synced without .git, so the
# caller passes the commit it checked to be clean.
#
#   akkalat/isca27_stage4.sh <commit> <plan>
#
# Plans (one per server, 16 concurrent simulations, 30 GiB memory reserve):
#   yss2: hpca T11 probes -> hpca main + filter audit -> sensitivity (filter)
#   yss3: mi300x T11 probes -> mi300x main + filter audit -> LLM
#         -> sensitivity (l2, row)
# The main campaign (12 workers) and the filter audit (4 workers) run side by
# side so that their long kmeans runs overlap. Results go to
# results/1013_t13s4-<part>_wafer48_<preset>_<commit>/.
set -euo pipefail

# Many simulations share this session; a shared heartbeat file would be
# overwritten by all of them, so progress is watched from the results instead.
unset EXP_HEARTBEAT

COMMIT="$1"
PLAN="$2"
ROOT="$(pwd)"
RESERVE=30
SCAN=10
# Longest first (paper campaign runtimes), so long runs do not become the tail.
BENCHMARKS=kmeans,matrixtranspose,matrixmultiplication,aes,pagerank,fir,fft,floydwarshall,im2col,spmv,simpleconvolution,bitonicsort,relu,fastwalshtransform

out() { echo "$ROOT/results/1013_t13s4-$1_wafer48_$2_$COMMIT"; }
log() { echo "[stage4 $(date -Is)] $*"; }

build() {
  log "build baseline binary"
  (cd akkalat/baseline && go build -buildvcs=false -o baseline .)
}

probes() { # config
  local cfg=$1 label=$2
  local common=(-benchmark probe -report-l2-source -l2-source-tile-width=7
    -report-rdma-transaction-count -report-dram-transaction-count
    -report-inst-count -report-cache-hit-rate)
  local runs=(
    "P1-o5r5-read-pass2-wg1|-probe-owner 5 -probe-runner 5 -probe-mode read -probe-passes 2 -probe-threads 256"
    "P1-o5r5-read-pass2|-probe-owner 5 -probe-runner 5 -probe-mode read -probe-passes 2"
    "P3a-o5r1-read-pass1|-probe-owner 5 -probe-runner 1 -probe-mode read -probe-passes 1"
    "P3a-o5r1-read-pass2-wg1|-probe-owner 5 -probe-runner 1 -probe-mode read -probe-passes 2 -probe-threads 256"
    "P3a-o5r1-read-pass2|-probe-owner 5 -probe-runner 1 -probe-mode read -probe-passes 2"
    "P3b-o2r1-read-pass1-wg1|-probe-owner 2 -probe-runner 1 -probe-mode read -probe-passes 1 -probe-threads 256"
    "P3b-o2r1-read-pass1|-probe-owner 2 -probe-runner 1 -probe-mode read -probe-passes 1"
    "P3b-o4r1-read-pass1-wg1|-probe-owner 4 -probe-runner 1 -probe-mode read -probe-passes 1 -probe-threads 256"
    "P3b-o4r1-read-pass1|-probe-owner 4 -probe-runner 1 -probe-mode read -probe-passes 1"
    "P3b-o7r1-read-pass1-wg1|-probe-owner 7 -probe-runner 1 -probe-mode read -probe-passes 1 -probe-threads 256"
    "P3b-o7r1-read-pass1|-probe-owner 7 -probe-runner 1 -probe-mode read -probe-passes 1"
    "P3b-o48r1-read-pass1-wg1|-probe-owner 48 -probe-runner 1 -probe-mode read -probe-passes 1 -probe-threads 256"
    "P3b-o48r1-read-pass1|-probe-owner 48 -probe-runner 1 -probe-mode read -probe-passes 1"
    "P3c-o5r1-write-pass1|-probe-owner 5 -probe-runner 1 -probe-mode write -probe-passes 1"
    "P3c-o5r1-write-pass2|-probe-owner 5 -probe-runner 1 -probe-mode write -probe-passes 2"
    "P4-all48-read-pass2|-probe-all-gpms -probe-mode read -probe-passes 2"
  )
  log "T11 probes ($cfg): ${#runs[@]} runs"
  local pids=() r name args
  for r in "${runs[@]}"; do
    name="${r%%|*}"
    args="${r#*|}"
    # shellcheck disable=SC2086
    akkalat/isca27_run.sh "1013_t13s4-probe-${name}_wafer48_${label}_$COMMIT" \
      "$COMMIT" T13-stage4 "$cfg" "${common[@]}" $args &
    pids+=($!)
  done
  local rc=0 p
  for p in "${pids[@]}"; do wait "$p" || rc=1; done
  log "T11 probes ($cfg) done, rc=$rc"
}

main_and_audit() { # preset label
  local preset=$1 label=$2
  log "main ($preset) + filter audit, side by side"
  python3 akkalat/runall2.py --remote-ablation --configs=all \
    --benchmarks="$BENCHMARKS" --max-wg=78600 --gpm-preset="$preset" \
    --max-workers=12 --initial-workers=12 --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" --skip-build \
    --output-dir="$(out main "$label")" &
  local main=$!
  python3 akkalat/runall2.py --remote-ablation --configs=complete \
    --benchmarks="$BENCHMARKS" --max-wg=78600 --gpm-preset="$preset" \
    --extra-benchmark-flags='-typed-filter-authoritative-audit=true' \
    --max-workers=4 --initial-workers=4 --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" --skip-build \
    --output-dir="$(out filter-audit "$label")" &
  local audit=$!
  local rc=0
  wait "$main" || rc=1
  wait "$audit" || rc=1
  log "main + filter audit ($preset) done, rc=$rc"
}

llm() {
  log "LLM (mi300x-eighth)"
  python3 akkalat/CuPathLLM.py --configs=baseline,complete \
    --gpm-preset=mi300x-eighth --cu-per-gpu=38 \
    --max-workers=16 --initial-workers=16 --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" --skip-build \
    --output-dir="$(out llm mi300x)" || log "LLM rc=$?"
}

sensitivity() { # dimensions label-suffix [--include-reference]
  local dims=$1 part=$2
  shift 2
  log "sensitivity ($dims)"
  python3 akkalat/run_cupath_sensitivity.py --dimensions="$dims" "$@" \
    --gpm-preset=mi300x-eighth --binary="$ROOT/akkalat/baseline/baseline" \
    --max-workers=16 --initial-workers=16 --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" \
    --output-dir="$(out "sens-$part" mi300x)" || log "sensitivity rc=$?"
}

build
case "$PLAN" in
  yss2)
    probes hpca hpca
    main_and_audit hpca hpca
    sensitivity filter filter --include-reference
    ;;
  yss3)
    probes mi300x_eighth mi300x
    main_and_audit mi300x-eighth mi300x
    llm
    sensitivity l2,row l2-row
    ;;
  *)
    echo "unknown plan: $PLAN" >&2
    exit 1
    ;;
esac
log "plan $PLAN finished"
