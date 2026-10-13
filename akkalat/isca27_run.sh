#!/usr/bin/env bash
# Run one timing simulation for an ISCA'27 task into results/<exp_id>/.
# Run from the repo root on a server (the repo is synced without .git, so the
# caller passes the commit it checked to be clean).
#
#   akkalat/isca27_run.sh <exp_id> <commit> <task> [isca27_flags options] -- <benchmark args>
#
# Configuration flags come from akkalat/isca27_flags.py (runall2's
# build_common_flags); nothing configuration-related is written here.
# Output: results/<exp_id>/{meta.json,summary.csv,stats/,stdout.log,stderr.log}
set -euo pipefail

EXP_ID="$1"
COMMIT="$2"
TASK="$3"
shift 3

FLAG_OPTS=()
while [ $# -gt 0 ] && [ "$1" != "--" ]; do
  FLAG_OPTS+=("$1")
  shift
done
[ "${1:-}" = "--" ] && shift
BENCH=("$@")

ROOT="$(pwd)"
OUT="$ROOT/results/$EXP_ID"
BIN="$HOME/isca27bin/baseline_$COMMIT"

if [ -e "$OUT/meta.json" ]; then
  echo "results/$EXP_ID already exists" >&2
  exit 1
fi
mkdir -p "$OUT/stats"

if [ ! -x "$BIN" ]; then
  mkdir -p "$(dirname "$BIN")"
  (cd akkalat/baseline && go build -buildvcs=false -o "$BIN" .)
fi

mapfile -t COMMON < <(python3 akkalat/isca27_flags.py "${FLAG_OPTS[@]}" --list)
DEVIATION="$(python3 akkalat/isca27_flags.py "${FLAG_OPTS[@]}" --deviation "${BENCH[@]}")"
ARGS=("${COMMON[@]}" "${BENCH[@]}" -metric-file-name "$OUT/stats/metrics")
for f in "${COMMON[@]}"; do
  if [ "$f" = "-report-l2-source" ]; then
    ARGS+=(-l2-source-file "$OUT/stats/l2")
  fi
done

START="$(date -Is)"
set +e
"$BIN" "${ARGS[@]}" >"$OUT/stdout.log" 2>"$OUT/stderr.log"
RC=$?
set -e
END="$(date -Is)"

python3 - "$OUT" "$EXP_ID" "$COMMIT" "$TASK" "$(hostname)" "$(go version)" \
  "$START" "$END" "$RC" "$DEVIATION" "${ARGS[@]}" <<'EOF'
import csv, json, os, re, sys
out, exp_id, commit, task, host, gover, start, end, rc, deviation, *args = sys.argv[1:]

rows = [("metric", "where", "value")]
metrics = os.path.join(out, "stats", "metrics.csv")
if os.path.exists(metrics):
    with open(metrics) as f:
        next(f, None)
        for line in f:
            parts = [p.strip() for p in line.split(",")]
            if len(parts) >= 4 and parts[1] == "Driver" and parts[2] in ("kernel_time", "total_time"):
                rows.append((parts[2], parts[1], parts[3]))
with open(os.path.join(out, "stdout.log"), errors="replace") as f:
    for line in f:
        m = re.match(r"PROBE_PASS pass=(\d+) .*elapsed_ns=([0-9.]+)", line)
        if m:
            rows.append((f"probe_pass{m.group(1)}_ns", "probe", m.group(2)))
with open(os.path.join(out, "summary.csv"), "w", newline="") as f:
    csv.writer(f).writerows(rows)

preset = next((a.split("=", 1)[1] for a in args if a.startswith("-gpm-preset=")), "hpca")
notes = []
if preset == "mi300x-eighth":
    notes.append("GPM 内部按 1/8 MI300X：L2、HBM 带宽按 MI300X 在 2.1 GHz 下的每周期等效值设置；"
                 "延迟（DRAM 时序等）保持纳秒值，不做每周期等效缩放。HBM channel：64-bit、BL8、"
                 "每次访问 64 B、DRAM 时钟 1.23 GHz。")
json.dump({
    "exp_id": exp_id, "task": task, "commit": commit, "host": host,
    "go_version": gover, "mode": "timing", "verify": False,
    "config": "paper (95b4f46 BASE_COMMON_FLAGS via runall2 build_common_flags)",
    "gpm_preset": preset,
    "notes": notes,
    "config_deviation": json.loads(deviation),
    "start": start, "end": end, "exit_code": int(rc), "args": args,
}, open(os.path.join(out, "meta.json"), "w"), indent=2, ensure_ascii=False)
EOF

exit "$RC"
