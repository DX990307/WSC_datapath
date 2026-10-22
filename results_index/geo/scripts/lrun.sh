#!/bin/bash
# lrun.sh <bin> <name> <cfg> args... : local run from wt-geo root
SP=/private/tmp/claude-502/-Users-daoxuan-Desktop-CuPath/7605b201-be1b-453e-be50-1e538079f94d/scratchpad
B=$1; N=$2; CFG=$3; shift 3
d=$SP/geo/local/$N; mkdir -p $d; cd /Users/daoxuan/Desktop/CuPath/wt-geo
$B -config akkalat/configs/$CFG.json -magic-memory-copy=true "$@" -metric-file-name $d/metrics > $d/stdout.log 2> $d/stderr.log
echo "rc=$?" >> $d/stdout.log
