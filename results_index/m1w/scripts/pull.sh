#!/bin/bash
# pull.sh: copy summary/meta/m1w bound/metrics of finished m1w runs to ./raw/<host>/
cd "$(dirname "$0")/.."
for h in yss2 yss3; do
  mkdir -p raw/$h
  rsync -a --include='*/' --include='summary.csv' --include='meta.json' \
    --include='metrics_m1w_bound.csv' --exclude='*' \
    "${h}:akita-exp-test/m1w/src/results/" raw/$h/
  rsync -a "${h}:akita-exp-test/m1w/done.tsv" raw/$h/done.tsv 2>/dev/null
  rsync -a "${h}:akita-exp-test/m1w/hangs.tsv" raw/$h/hangs.tsv 2>/dev/null
done
