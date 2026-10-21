#!/bin/bash
# vrun.sh <name> <cfg> <args...> : background verify/smoke run with the v3c384 binary; records PIDs
D=$HOME/akita-exp-test/v3c384
B=$D/bin/baseline_4235991
N=$1; CFG=$2; shift 2
mkdir -p $D/verify/runs/$N; cd $D/verify/runs/$N
nohup /usr/bin/time -v ${TO:+timeout $TO} $B -config $D/src/akkalat/configs/$CFG.json -magic-memory-copy=true "$@" -metric-file-name $D/verify/runs/$N/metrics >stdout.log 2>stderr.log &
P=$!
sleep 1
C=$(pgrep -P $P | head -1); G=$(pgrep -P "$C" 2>/dev/null | head -1); [ -n "$G" ] && C="$C,$G"
echo "$P $N $(date -Is) sim=$C" >> $D/verify/pids.txt
echo started $N $P sim=$C
