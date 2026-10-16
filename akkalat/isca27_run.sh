#!/usr/bin/env bash
# Run one timing simulation for an ISCA'27 task into results/<exp_id>/.
# Run from the repo root on a server (the repo is synced without .git, so the
# caller passes the commit it checked to be clean).
#
#   akkalat/isca27_run.sh <exp_id> <commit> <task> <configs> [benchmark args ...]
#
# <configs> is a comma-separated list of config files; a bare name such as
# "hpca", "mi300x_eighth" or "mi200" means akkalat/configs/<name>.json. The
# simulator reads them with -config (later files override earlier ones), so
# no configuration flag is written here.
# <exp_id> follows akkalat/isca27_name.py: <MMDD>_<category>_<item>_<config>_<commit>.
# Output: results/<exp_id>/{meta.json,run.json,summary.csv,config_used.json,stats/,stdout.log,stderr.log}
set -euo pipefail

EXP_ID="$1"
COMMIT="$2"
TASK="$3"
CONFIG_NAMES="$4"
shift 4
BENCH=("$@")

python3 akkalat/isca27_name.py check "$EXP_ID" >&2 || exit 1

ROOT="$(pwd)"
OUT="$ROOT/results/$EXP_ID"
BIN="$HOME/isca27bin/baseline_$COMMIT"

CONFIGS=()
IFS=',' read -ra NAMES <<<"$CONFIG_NAMES"
for name in "${NAMES[@]}"; do
  if [ -f "$name" ]; then
    CONFIGS+=("$ROOT/$name")
  elif [ -f "akkalat/configs/$name.json" ]; then
    CONFIGS+=("$ROOT/akkalat/configs/$name.json")
  else
    echo "config not found: $name" >&2
    exit 1
  fi
done
CONFIG_LIST="$(IFS=','; echo "${CONFIGS[*]}")"

if [ -e "$OUT/meta.json" ]; then
  echo "results/$EXP_ID already exists" >&2
  exit 1
fi
mkdir -p "$OUT/stats"

if [ ! -x "$BIN" ]; then
  mkdir -p "$(dirname "$BIN")"
  (cd akkalat/baseline && go build -buildvcs=false -o "$BIN" .)
fi

# config_deviation is relative to configs/mi200.json, the frozen baseline
# (isca27-mi200-v1); meta.json records the base as deviation_base.
DEVIATION="$(python3 akkalat/isca27_config.py --deviation "$CONFIG_LIST" -- "${BENCH[@]}")"
ARGS=(-config "$CONFIG_LIST" -config-out "$OUT/config_used.json" "${BENCH[@]}"
  -metric-file-name "$OUT/stats/metrics")
for f in "${BENCH[@]}"; do
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
  "$START" "$END" "$RC" "$DEVIATION" "$CONFIG_NAMES" "${ARGS[@]}" <<'EOF'
import csv, json, os, re, sys
(out, exp_id, commit, task, host, gover, start, end, rc, deviation,
 config_names, *args) = sys.argv[1:]

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

# platbench rows (T14): "PLATCSV,header,..." and "PLATCSV,row,..." -> platform.csv
plat = []
with open(os.path.join(out, "stdout.log"), errors="replace") as f:
    for line in f:
        if line.startswith("PLATCSV,header,") and not plat:
            plat.append(line.rstrip("\n").split(",")[2:])
        elif line.startswith("PLATCSV,row,"):
            plat.append(line.rstrip("\n").split(",")[2:])
if plat:
    with open(os.path.join(out, "platform.csv"), "w", newline="") as f:
        csv.writer(f).writerows(plat)

preset = "hpca"
used = os.path.join(out, "config_used.json")
if os.path.exists(used):
    preset = json.load(open(used))["all_flags"]["gpm-preset"]["value"]
notes = []
if preset == "mi300x-eighth":
    notes.append("GPM 内部按 1/8 MI300X：L2、HBM 带宽按 MI300X 在 2.1 GHz 下的每周期等效值设置；"
                 "延迟（DRAM 时序等）保持纳秒值，不做每周期等效缩放。HBM channel：64-bit、BL8、"
                 "每次访问 64 B、DRAM 时钟 1.23 GHz。")
elif preset == "mi200":
    notes.append("GPM 内部按 1/4 MI250X GCD（按公开规格设置，未校准）：28 CU，8 个 256 KB L2 slice"
                 "（64 B 行，MI200 实际为 128 B），每个 slice 对应一个 HBM2e channel；L2、HBM2e 带宽按 "
                 "MI250X 在 1.7 GHz 下的每周期等效值设置（每 GPM 241 B/cycle）；访问延迟（tCL、tRCD、"
                 "tRP、tRAS、tRFC 等）保持纳秒值，限制带宽的间隔（tCCD、tRRD、tFAW、tWTR）保持 1.6 GHz "
                 "器件的时钟数。HBM2e channel：2 个 pseudo-channel，各 64-bit、BL4、每次访问 32 B、"
                 "DRAM 时钟 1.6 GHz / 1.7 = 941 MHz；含 all-bank refresh、tFAW、同请求行保持。")
json.dump({
    "exp_id": exp_id, "task": task, "commit": commit, "host": host,
    "go_version": gover, "mode": "timing", "verify": False,
    "config": config_names, "gpm_preset": preset, "notes": notes,
    "config_deviation": json.loads(deviation), "deviation_base": "configs/mi200.json",
    "start": start, "end": end, "exit_code": int(rc), "args": args,
}, open(os.path.join(out, "meta.json"), "w"), indent=2, ensure_ascii=False)
EOF
# The run record the experiment monitor reads (name fields + properties).
python3 akkalat/isca27_name.py record "$OUT" "Command=$BIN ${ARGS[*]}" "Working Directory=$ROOT" ||
  echo "warning: could not write $OUT/run.json" >&2

exit "$RC"
