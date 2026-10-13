#!/usr/bin/env bash
# T11: run one timing-mode probe experiment into results/<exp_id>/.
# Run from the repo root on a server (the repo is synced without .git, so the
# caller passes the commit it checked to be clean).
#
#   akkalat/t11_run_probe.sh <exp_id> <commit> [-probe-* flags ...]
#
# The configuration flags come from runall2's build_common_flags() via
# akkalat/t11_probe_flags.py; nothing configuration-related is written here.
# Output: results/<exp_id>/{meta.json,summary.csv,stats/,stdout.log,stderr.log}
set -euo pipefail

EXP_ID="$1"
COMMIT="$2"
shift 2

ROOT="$(pwd)"
OUT="$ROOT/results/$EXP_ID"
BIN="$HOME/t11bin/baseline_$COMMIT"

if [ -e "$OUT/meta.json" ]; then
  echo "results/$EXP_ID already exists" >&2
  exit 1
fi
mkdir -p "$OUT/stats"

if [ ! -x "$BIN" ]; then
  mkdir -p "$(dirname "$BIN")"
  (cd akkalat/baseline && go build -buildvcs=false -o "$BIN" .)
fi

mapfile -t COMMON < <(python3 akkalat/t11_probe_flags.py --list)
BENCH=(-benchmark probe "$@")
DEVIATION="$(python3 akkalat/t11_probe_flags.py --deviation "${BENCH[@]}")"
ARGS=("${COMMON[@]}" "${BENCH[@]}"
  -metric-file-name "$OUT/stats/metrics"
  -l2-source-file "$OUT/stats/l2")

START="$(date -Is)"
set +e
"$BIN" "${ARGS[@]}" >"$OUT/stdout.log" 2>"$OUT/stderr.log"
RC=$?
set -e
END="$(date -Is)"

{
  echo "pass,start_ns,end_ns,elapsed_ns"
  sed -n 's/^PROBE_PASS pass=\([0-9]*\) start_ns=\([0-9.]*\) end_ns=\([0-9.]*\) elapsed_ns=\([0-9.]*\)$/\1,\2,\3,\4/p' \
    "$OUT/stdout.log"
} >"$OUT/summary.csv"

python3 - "$OUT/meta.json" "$EXP_ID" "$COMMIT" "$(hostname)" \
  "$(go version)" "$START" "$END" "$RC" "$DEVIATION" "${ARGS[@]}" <<'EOF'
import json, sys
path, exp_id, commit, host, gover, start, end, rc, deviation, *args = sys.argv[1:]
json.dump({
    "exp_id": exp_id, "task": "T11", "commit": commit, "host": host,
    "go_version": gover, "mode": "timing", "verify": False,
    "config": "paper (95b4f46 BASE_COMMON_FLAGS via runall2 build_common_flags)",
    "config_deviation": json.loads(deviation),
    "start": start, "end": end, "exit_code": int(rc), "args": args,
}, open(path, "w"), indent=2, ensure_ascii=False)
EOF

exit "$RC"
