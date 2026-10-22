#!/bin/bash
# Run on a server in ~/akita-exp-test/pgcls: per-run page analysis of finished runs.
#   baseline (mi200)      -> analysis/<item>.json, analysis/<item>_pages.csv.gz, analysis/lists/<item>__<cls>.txt
#   v4_complete           -> analysis/<item>_v4.json  (needs the baseline of the same item)
#   every run             -> analysis/runs.tsv (exp, exit, total_time, inst, wg)
D=~/akita-exp-test/pgcls; A=$D/analysis; S=$D/scripts; mkdir -p $A/lists
: > $A/runs.tsv
for R in $D/src/results/1022_diag_pgcls-*; do
  e=$(basename $R); [ -f $R/meta.json ] || continue
  rc=$(python3 -c "import json;print(json.load(open('$R/meta.json'))['exit_code'])")
  m=$R/stats/metrics.csv
  t=$(grep -m1 "Driver, total_time" $m 2>/dev/null | cut -d, -f4)
  inst=$(awk -F', ' '$3=="cu_inst_count"{s+=$4}END{printf "%d", s}' $m 2>/dev/null)
  wg=$(awk -F', ' '$3=="cu_wg_count"{s+=$4}END{printf "%d", s}' $m 2>/dev/null)
  printf "%s\t%s\t%s\t%s\t%s\n" "$e" "$rc" "$t" "$inst" "$wg" >> $A/runs.tsv
done
for R in $D/src/results/1022_diag_pgcls-w256b384-*_mi200_*; do
  e=$(basename $R); case $e in *-or-*) continue;; esac
  item=${e#1022_diag_pgcls-w256b384-}; item=${item%_mi200_*}
  P=$R/stats/metrics_pgcls; [ -f ${P}_totals.csv ] || continue
  [ -f $A/$item.json ] || { python3 $S/pgcls_page.py $P $item $A && python3 $S/pgcls_oracle_lists.py $A/${item}_pages.csv.gz $A/lists $item > $A/lists/$item.txt; }
done
for R in $D/src/results/1022_diag_pgcls-w256b384-*_mi200+v4complete_*; do
  e=$(basename $R); case $e in *-or-*) continue;; esac
  item=${e#1022_diag_pgcls-w256b384-}; item=${item%_mi200+v4complete_*}
  P=$R/stats/metrics_pgcls; [ -f ${P}_totals.csv ] && [ -f $A/${item}_pages.csv.gz ] || continue
  [ -f $A/${item}_v4.json ] || python3 $S/pgcls_v4.py $P $A/${item}_pages.csv.gz $item $A
done
echo analyzed $(ls $A/*.json | wc -l) json
