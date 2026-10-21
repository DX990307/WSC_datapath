#!/bin/bash
# usage: run_llm.sh <run_dir with et/, comm_groups.json> <network.yml>   (1 tick = 1 ns)
R=$(cd "$1" && pwd); NET=$(realpath "$2")
H=$HOME/astrasim-test
BIN=$H/astra-sim/build/astra_analytical/build/bin/AstraSim_Analytical_Congestion_Aware
cd "$R"
ASTRA_P2P_UNLIMITED=1 ASTRA_MESH_EP_LATENCY=5 ASTRA_LINK_WINDOW=1000 ASTRA_LINK_STATS=$R/links.csv \
/usr/bin/time -v "$BIN" --workload-configuration=$R/et/wl \
  --comm-group-configuration=$R/comm_groups.json \
  --system-configuration=$H/p18/configs/system_llm.json \
  --remote-memory-configuration=$H/p18/configs/remote_memory.json \
  --network-configuration=$NET > stdout.log 2> time.log
echo "exit=$?"; grep -E "Elapsed|Maximum resident" time.log
