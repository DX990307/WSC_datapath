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
#   - s_load/s_store/s_buffer_* other than s_load_dword/x2/x4/x8/x16
#     (emu/alu.go runSMEM and timing/cu/scalarunit.go implement SMEM 0-4);
#   - a kernel whose private segment (scratch) size is not 0;
#   - an unsigned 32-bit vector compare (v_cmp*_u32) with a negative inline
#     constant: the emulator reads the constant as a sign-extended 64-bit
#     value and the VGPR zero-extended, so e.g. "v_cmp_ne_u32 vcc, -2, v0"
#     is always true (found in the W2 altis_cfd port: clang emits it for
#     "x == -2"); rewrite the test (e.g. signed compares or bit tests);
#   - a scheduler-internal SOPP op other than s_nop, s_endpgm, s_barrier,
#     s_waitcnt inside a kernel body: emu/alu.go runSOPP has no case for
#     them (emulation panics) and the timing CU treats them as no-ops
#     (s_sethalt/s_setkill/s_trap/s_set_gpr_idx_* are not modeled). s_nop is
#     accepted: the timing scheduler used to complete it without advancing
#     the PC (hang), fixed in "timing/cu: advance PC for scheduler-internal
#     SOPP instructions". Padding after s_endpgm is never executed and is
#     ignored.
# Needs llvm-objdump and llvm-readelf (yss2). Usage: check_hsaco.sh a.hsaco...
set -euo pipefail

OBJDUMP="${LLVM_OBJDUMP:-llvm-objdump}"
READELF="${LLVM_READELF:-llvm-readelf}"
MCPU="${GPU:-fiji}"

ALLOWED_DS='^(ds_write_b32|ds_write2_b32|ds_read_b32|ds_read2_b32|ds_write2_b64|ds_read_b64|ds_read2_b64)$'
ALLOWED_SMEM='^(s_load_dword|s_load_dwordx2|s_load_dwordx4|s_load_dwordx8|s_load_dwordx16)$'
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

  # scheduler-internal ops in a kernel body (labels start a kernel,
  # s_endpgm ends its body; the padding after it is never executed)
  internal="$("$OBJDUMP" -d --mcpu="$MCPU" "$f" | awk '
    /^[0-9a-f]+ <.*>:$/ { body = 1; next }
    { m = $1 }
    m == "s_endpgm" { body = 0; next }
    body && m ~ /^s_(setkill|sethalt|sleep|setprio|trap|icache_inv|incperflevel|decperflevel|ttracedata|set_gpr_idx_off|set_gpr_idx_mode)$/ {
      n[m]++ }
    END { for (k in n) printf "  %s in a kernel body (%d): not implemented\n", k, n[k] }')"
  bad+="${internal:+$internal$'\n'}"

  negcmp="$("$OBJDUMP" -d --mcpu="$MCPU" "$f" |
    grep -E '^[[:space:]]+v_cmpx?_[a-z]+_u32(_e32|_e64)?[[:space:]][^/]*, -[0-9]' |
    sed -E 's/^[[:space:]]+//; s/[[:space:]]*\/\/.*$//' | sort | uniq -c || true)"
  if [[ -n "$negcmp" ]]; then
    bad+="  unsigned compare with a negative inline constant (emulator bug):"$'\n'"$negcmp"$'\n'
  fi

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
