#!/bin/bash
# usage: run_one.sh <run_dir> <network.yml> <ep_latency_ticks>
# run_dir holds et/<prefix>.*.et; writes msgs.csv, links.csv, stdout.log there.
set -e
R=$(cd "$1" && pwd); NET=$(realpath "$2"); EP=$3
H=$HOME/astrasim-test
BIN=$H/astra-sim/build/astra_analytical/build/bin/AstraSim_Analytical_Congestion_Aware
C=$H/p18/configs
cd "$R"
ASTRA_P2P_UNLIMITED=1 ASTRA_MESH_EP_LATENCY=$EP ASTRA_LINK_WINDOW=24000 \
ASTRA_MSG_LOG=$R/msgs.csv ASTRA_LINK_STATS=$R/links.csv \
/usr/bin/time -v "$BIN" --workload-configuration=$R/et/wl \
  --system-configuration=$C/system.json \
  --remote-memory-configuration=$C/remote_memory.json \
  --network-configuration=$NET > stdout.log 2> time.log
echo "exit=$?"; grep -E "Maximum resident|Elapsed" time.log
