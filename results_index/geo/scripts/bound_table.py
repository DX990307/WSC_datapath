import json, sys, re
rows=[json.loads(l) for l in open(sys.argv[1])]
order=["pr-g24","npbcg-W","mm-g12","tc-s12e16","sssp-s18e4-r1","bfs-s18e2"]
out=[]
print("| workload | 配置 | 网络读 | r1 1µs 可省读 | r1 10µs | r2 1µs | r2 10µs | 每次推送用到的邻居 r1/r2 (10µs) | 可省字节×跳 r1/r2 (10µs) | 推送成本/网络读字节×跳 r1/r2 | 无用副本 r1/r2 (10µs) | 零使用推送 r1/r2 (10µs) |")
print("|"+"---|"*12)
csvrows=[]
for w in order:
  for cfg in ["mi200","mi200+v3complete"]:
    d=[r for r in rows if r["exp"].startswith(f"1021_diag_geob-w64b384-{w}_{cfg}_")]
    if not d or d[0].get("exit")!=0: continue
    r=d[0]; g=lambda k: r.get("geo_bound_"+k,0)
    nr=g("net_reads"); nb=g("net_read_bytes_hop")
    f=lambda x:f"{x:.3f}"
    print(f"| {w} | {'baseline' if cfg=='mi200' else 'Complete'} | {nr:.0f} | {f(g('r1_1us_covered_frac'))} | {f(g('r1_10us_covered_frac'))} | {f(g('r2_1us_covered_frac'))} | {f(g('r2_10us_covered_frac'))} | {f(g('r1_10us_used_neighbors_per_push'))} / {f(g('r2_10us_used_neighbors_per_push'))} | {f(g('r1_10us_saved_frac_bytes_hop'))} / {f(g('r2_10us_saved_frac_bytes_hop'))} | {f(g('r1_push_cost_bytes_hop')/nb if nb else 0)} / {f(g('r2_push_cost_bytes_hop')/nb if nb else 0)} | {f(g('r1_10us_useless_copy_frac'))} / {f(g('r2_10us_useless_copy_frac'))} | {f(g('r1_10us_zero_use_push_frac'))} / {f(g('r2_10us_zero_use_push_frac'))} |")
    row={"workload":w,"config":"baseline" if cfg=="mi200" else "complete","exp":r["exp"],"total_time":r.get("total_time")}
    for k,v in r.items():
      if k.startswith("geo_bound_"): row[k[10:]]=v
    csvrows.append(row)
json.dump(csvrows,open(sys.argv[2],"w"))
