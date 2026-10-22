WL = {
 "pr-g24": "-benchmark pagerank-t16small -t16-wg-per-cu 24",
 "npbcg-W": "-benchmark npbcg -cg-class W",
 "mm-g12": "-benchmark matrixmultiplication-t16small",
 "tc-s12e16": "-benchmark graphtc -tc-scale 12 -tc-edge-factor 16",
 "sssp-s18e4-r1": "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1",
 "bfs-s18e2": "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2",
 "cfd-c172k-b128": "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128",
 "ar-pc1": "-benchmark allreduce -ar-per-cu 1",
}
NET = {
 "w64b384": "-bandwidth=24 -switch-buffer-flits=16",
 "w256b48m": "-bandwidth=3 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512",
 "w256b384": "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512",
 "w256b48m-base": "-rdma-max-outstanding=256 -bandwidth=3 -switch-buffer-flits=16",
}
