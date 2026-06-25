#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "Usage: $0 RESULTS_DIR [OUTPUT_CSV]" >&2
  exit 2
fi

results_dir=$1
output=${2:-"$results_dir/page_rw_modes.csv"}

printf "benchmark,read_only_pages,write_only_pages,read_write_pages,total_touched_pages,read_only_pct,write_only_pct,read_write_pct\n" > "$output"

for trace in "$results_dir"/*_sharing.csv.gz; do
  [[ -e "$trace" ]] || continue
  base=$(basename "$trace")
  benchmark=${base#baseline_}
  benchmark=${benchmark%_baseline_sharing.csv.gz}
  benchmark=${benchmark%_llm_mixed_sharing.csv.gz}

  echo "Analyzing $benchmark" >&2
  read -r read_only write_only read_write total < <(
    gzip -cd "$trace" | awk -F, '
      NR == 1 {
        for (i = 1; i <= NF; i++) {
          if ($i == "pid") pid_col = i
          if ($i == "page_id") page_col = i
          if ($i == "vaddr") vaddr_col = i
          if ($i == "op") op_col = i
        }
        next
      }
      {
        if (page_col) {
          page = $page_col
        } else {
          page = int($vaddr_col / 4096)
        }
        if (pid_col) {
          key = $pid_col ":" page
        } else {
          key = page
        }

        if ($op_col ~ /^[Rr]/) {
          mode[key] = or(mode[key], 1)
        } else if ($op_col ~ /^[Ww]/) {
          mode[key] = or(mode[key], 2)
        }
      }
      END {
        read_only = 0
        write_only = 0
        read_write = 0
        for (key in mode) {
          if (mode[key] == 1) {
            read_only++
          } else if (mode[key] == 2) {
            write_only++
          } else if (mode[key] == 3) {
            read_write++
          }
        }
        total = read_only + write_only + read_write
        print read_only, write_only, read_write, total
      }
    '
  )

  if [[ "$total" -gt 0 ]]; then
    read_only_pct=$(awk -v n="$read_only" -v t="$total" 'BEGIN { printf "%.2f", 100.0 * n / t }')
    write_only_pct=$(awk -v n="$write_only" -v t="$total" 'BEGIN { printf "%.2f", 100.0 * n / t }')
    read_write_pct=$(awk -v n="$read_write" -v t="$total" 'BEGIN { printf "%.2f", 100.0 * n / t }')
  else
    read_only_pct="0.00"
    write_only_pct="0.00"
    read_write_pct="0.00"
  fi

  printf "%s,%s,%s,%s,%s,%s,%s,%s\n" \
    "$benchmark" \
    "$read_only" \
    "$write_only" \
    "$read_write" \
    "$total" \
    "$read_only_pct" \
    "$write_only_pct" \
    "$read_write_pct" >> "$output"
done

echo "$output"
