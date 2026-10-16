#!/usr/bin/env bash
# Check that a code object only uses memory instructions the simulator
# implements (T17 step 0). Fails (exit 1) on:
#   - MUBUF/MTBUF/scratch instructions (buffer_*, tbuffer_*, scratch_*);
#     the simulator has no MUBUF/scratch support (emu/alu.go Run panics on the
#     format, timing/cu/scratchpadpreparer.go too);
#   - any atomic (flat_atomic_*, global_atomic_*, ds_*atomic-like ops);
#   - flat_* opcodes the emulator does not implement (emu/alu_flat.go
#     implements load ubyte/ushort/dword/x2/x4 and store dword/x2/x3/x4);
#   - ds_* outside the 7 implemented opcodes (emu/aluds.go);
#   - s_load/s_store/s_buffer_* other than s_load_dword/x2/x4/x8 (emu/alu.go
#     runSMEM and timing/cu/scalarunit.go implement SMEM opcodes 0-3 only);
#   - a kernel whose private segment (scratch) size is not 0.
# Needs llvm-objdump and llvm-readelf (yss2). Usage: check_hsaco.sh a.hsaco...
set -euo pipefail

OBJDUMP="${LLVM_OBJDUMP:-llvm-objdump}"
READELF="${LLVM_READELF:-llvm-readelf}"
MCPU="${GPU:-fiji}"

ALLOWED_DS='^(ds_write_b32|ds_write2_b32|ds_read_b32|ds_read2_b32|ds_write2_b64|ds_read_b64|ds_read2_b64)$'
ALLOWED_SMEM='^(s_load_dword|s_load_dwordx2|s_load_dwordx4|s_load_dwordx8)$'
ALLOWED_FLAT='^(flat_load_ubyte|flat_load_ushort|flat_load_dword|flat_load_dwordx2|flat_load_dwordx4|flat_store_dword|flat_store_dwordx2|flat_store_dwordx3|flat_store_dwordx4)$'

status=0
for f in "$@"; do
  mnems="$("$OBJDUMP" -d --mcpu="$MCPU" "$f" |
    grep -oE '^[[:space:]]+[a-z][a-z0-9_]*' | sed -E 's/^[[:space:]]+//' | sort | uniq -c)"
  bad=""
  while read -r count m; do
    [[ -z "$m" ]] && continue
    case "$m" in
      buffer_* | tbuffer_* | scratch_* | global_* | *atomic*)
        bad+="  $m ($count)"$'\n' ;;
      flat_*)
        [[ "$m" =~ $ALLOWED_FLAT ]] || bad+="  $m ($count)"$'\n' ;;
      ds_*)
        [[ "$m" =~ $ALLOWED_DS ]] || bad+="  $m ($count)"$'\n' ;;
      s_load_* | s_store_* | s_buffer_* | s_dcache_* | s_memtime | s_memrealtime)
        [[ "$m" =~ $ALLOWED_SMEM ]] || bad+="  $m ($count)"$'\n' ;;
    esac
  done <<<"$mnems"

  priv="$("$READELF" --notes "$f" | grep -E 'PrivateSegmentFixedSize' |
    awk '$2 != 0' || true)"
  if [[ -n "$priv" ]]; then
    bad+="  private segment (scratch) in use: ${priv//$'\n'/; }"$'\n'
  fi

  if [[ -n "$bad" ]]; then
    echo "check_hsaco: $f uses instructions the simulator does not implement:" >&2
    printf '%s' "$bad" >&2
    status=1
  else
    echo "check_hsaco: $f OK"
  fi
done
exit "$status"
