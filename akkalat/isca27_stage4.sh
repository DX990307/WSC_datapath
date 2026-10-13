#!/usr/bin/env bash
# T13 stage 4: rerun the paper's experiments with the paper's own run scripts
# (runall2.py, CuPathLLM.py, run_cupath_sensitivity.py) plus a GPM preset.
# Run from the repo root on a server; the repo is synced without .git, so the
# caller passes the commit it checked to be clean.
#
#   akkalat/isca27_stage4.sh <commit> <plan>
#
# Plans (one per server):
#   yss2: hpca T11 probes, hpca main + filter audit, LLM (mi300x),
#         sensitivity (filter, with the reference point)
#   yss3: mi300x T11 probes, mi300x main + filter audit,
#         sensitivity (l2, row)
# The mi300x LLM campaign runs on yss2 so both servers finish at about the
# same time (yss3 carries the longer mi300x main campaign).
# All parts start at once and share 16 simulation slots per server through
# slot_pool.py; a free slot goes to the part with the lowest priority number
# that still has runs to launch (probes 1, main and audit 2, LLM 4,
# sensitivity 5), so later parts fill the slots left by the long tail of
# earlier ones. Launches also wait while MemAvailable is below 30 GiB.
# Results go to results/1013_t13s4-<part>_wafer48_<preset>_<commit>/.
set -euo pipefail

# Many simulations share this session; a shared heartbeat file would be
# overwritten by all of them, so progress is watched from the results instead.
unset EXP_HEARTBEAT

COMMIT="$1"
PLAN="$2"
ROOT="$(pwd)"
RESERVE=30
SCAN=10
WORKERS=16
export EXP_SLOT_DIR="$HOME/.isca27_slots" EXP_SLOT_COUNT=$WORKERS
mkdir -p "$EXP_SLOT_DIR"
# Longest first (paper campaign runtimes), so long runs do not become the tail.
BENCHMARKS=kmeans,matrixtranspose,matrixmultiplication,aes,pagerank,fir,fft,floydwarshall,im2col,spmv,simpleconvolution,bitonicsort,relu,fastwalshtransform

out() { echo "$ROOT/results/1013_t13s4-$1_wafer48_$2_$COMMIT"; }
log() { echo "[stage4 $(date -Is)] $*"; }

build() {
  log "build baseline binary"
  (cd akkalat/baseline && go build -buildvcs=false -o baseline .)
  # isca27_run.sh (T11 probes) uses this cache; filling it here keeps 16
  # parallel probe runs from building it at the same time.
  mkdir -p "$HOME/isca27bin"
  [ -x "$HOME/isca27bin/baseline_$COMMIT" ] ||
    cp akkalat/baseline/baseline "$HOME/isca27bin/baseline_$COMMIT"
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
    python3 akkalat/slot_pool.py run -- \
      akkalat/isca27_run.sh "1013_t13s4-probe-${name}_wafer48_${label}_$COMMIT" \
      "$COMMIT" T13-stage4 "$cfg" "${common[@]}" $args &
    pids+=($!)
  done
  local rc=0 p
  for p in "${pids[@]}"; do wait "$p" || rc=1; done
  log "T11 probes ($cfg) done, rc=$rc"
}

main() { # preset label
  local preset=$1 label=$2
  log "main ($preset)"
  python3 akkalat/runall2.py --remote-ablation --configs=all \
    --benchmarks="$BENCHMARKS" --max-wg=78600 --gpm-preset="$preset" \
    --max-workers=$WORKERS --initial-workers=$WORKERS --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" --skip-build \
    --output-dir="$(out main "$label")" || log "main ($preset) rc=$?"
  log "main ($preset) done"
}

audit() { # preset label
  local preset=$1 label=$2
  log "filter audit ($preset)"
  python3 akkalat/runall2.py --remote-ablation --configs=complete \
    --benchmarks="$BENCHMARKS" --max-wg=78600 --gpm-preset="$preset" \
    --extra-benchmark-flags='-typed-filter-authoritative-audit=true' \
    --max-workers=$WORKERS --initial-workers=$WORKERS --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" --skip-build \
    --output-dir="$(out filter-audit "$label")" || log "filter audit ($preset) rc=$?"
  log "filter audit ($preset) done"
}

llm() {
  log "LLM (mi300x-eighth)"
  python3 akkalat/CuPathLLM.py --configs=baseline,complete \
    --gpm-preset=mi300x-eighth --cu-per-gpu=38 \
    --max-workers=$WORKERS --initial-workers=$WORKERS --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" --skip-build \
    --output-dir="$(out llm mi300x)" || log "LLM rc=$?"
  log "LLM done"
}

sensitivity() { # dimensions label-suffix [--include-reference]
  local dims=$1 part=$2
  shift 2
  log "sensitivity ($dims)"
  python3 akkalat/run_cupath_sensitivity.py --dimensions="$dims" "$@" \
    --gpm-preset=mi300x-eighth --binary="$ROOT/akkalat/baseline/baseline" \
    --max-workers=$WORKERS --initial-workers=$WORKERS --memory-reserve-gib="$RESERVE" \
    --memory-scan-minutes="$SCAN" \
    --output-dir="$(out "sens-$part" mi300x)" || log "sensitivity rc=$?"
  log "sensitivity ($dims) done"
}

part() { # priority function args...
  local priority=$1
  shift
  (export EXP_SLOT_PRIORITY=$priority; "$@") &
}

build
case "$PLAN" in
  yss2)
    part 1 probes hpca hpca
    part 2 main hpca hpca
    part 2 audit hpca hpca
    part 4 llm
    part 5 sensitivity filter filter --include-reference
    ;;
  yss3)
    part 1 probes mi300x_eighth mi300x
    part 2 main mi300x-eighth mi300x
    part 2 audit mi300x-eighth mi300x
    part 5 sensitivity l2,row l2-row
    ;;
  *)
    echo "unknown plan: $PLAN" >&2
    exit 1
    ;;
esac
wait
log "plan $PLAN finished"
