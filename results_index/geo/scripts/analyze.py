#!/usr/bin/env python3
"""analyze.py raw.jsonl geo.csv -> csv + markdown tables on stdout."""
import json, sys, csv, re, math
rows=[json.loads(l) for l in open(sys.argv[1])]
order=["pr-g24","npbcg-W","mm-g12","tc-s12e16","sssp-s18e4-r1","bfs-s18e2","cfd-c172k-b128","ar-pc1"]
def parse(e):
    m=re.match(r"1021_(abl_v3c|sens_m2w-w256b48m|abl_geo-(w64b384|w256b48m)|diag_geob-w64b384)-(.+?)_(mi200(?:\+\w+)?)_(\w{7})$",e)
    if not m: return None
    kind,net,wl,cfg,c=m.groups()
    if kind=="abl_v3c": net="w64b384"
    elif kind.startswith("sens"): net="w256b48m"
    elif kind.startswith("diag"): return None
    cfg={"mi200":"baseline","mi200+v3complete":"v3complete"}.get(cfg,cfg.replace("mi200+",""))
    return net,wl,cfg,c
runs={}
for r in rows:
    p=parse(r["exp"])
    if not p or r.get("exit")!=0 or not r.get("total_time"): continue
    net,wl,cfg,c=p
    if cfg in ("baseline","v3complete") and c!="4235991": continue
    runs[(net,wl,cfg)]=r
cfgs=["baseline","v3complete","v4complete","v4geo1","v4geo1s","v4geo2","v3geo1","v3geo1s","v3geo2"]
out=[]
for net in ["w64b384","w256b48m"]:
  for wl in order:
    b=runs.get((net,wl,"baseline")); v4=runs.get((net,wl,"v4complete")); v3=runs.get((net,wl,"v3complete"))
    for cfg in cfgs:
      r=runs.get((net,wl,cfg))
      if not r: continue
      ctl=v4 if cfg.startswith("v4") else v3
      g=lambda k: r.get(k,0.0)
      lines=g("geo_push_owner_push_lines")
      used=g("geo_push_l2_useful")+g("geo_push_catch_inflight")+g("geo_push_catch_queued")
      d=dict(scenario=net,workload=wl,config=cfg,exp=r["exp"],host=r["host"],total_time_us=r["total_time"]*1e6,
        speedup_vs_baseline=(b["total_time"]/r["total_time"]) if b else "",
        speedup_vs_control=(ctl["total_time"]/r["total_time"]) if ctl and cfg not in("baseline",) else "",
        control=("v4complete" if cfg.startswith("v4") else "v3complete") if cfg!="baseline" else "",
        net_lines=g("remote_wire_lines"), net_lines_vs_control=(g("remote_wire_lines")/ctl["remote_wire_lines"]) if ctl and ctl.get("remote_wire_lines") else "",
        push_packets=g("geo_push_owner_push_packets"), push_line_copies=lines,
        push_fills_installed=g("geo_push_l2_installed"), push_l2_useful=g("geo_push_l2_useful"),
        push_catch_entries=g("geo_push_catch_inflight")+g("geo_push_catch_queued"), push_catch_waiters=g("geo_push_catch_waiters"),
        push_used_frac=(used/lines) if lines else "",
        push_extra_link_bytes=g("geo_push_owner_extra_link_bytes"),
        mesh_bytes=g("mesh_bytes"), mesh_bytes_vs_control=(g("mesh_bytes")/ctl["mesh_bytes"]) if ctl and ctl.get("mesh_bytes") else "",
        link_max_util=g("link_max_util"), link_max=r.get("link_max",""), link_max_window_util=g("link_max_window_util"),
        hot_0to7_util=r.get("link_0->7_util",""), hot_48to41_util=r.get("link_48->41_util",""),
        read_lat_ns=(g("remote_logical_read_latency_total_ns")/g("remote_logical_read_latency_samples")) if g("remote_logical_read_latency_samples") else "",
        window_full_ns=g("t18_v3_window_full_ns"))
      out.append(d)
w=csv.DictWriter(open(sys.argv[2],"w"),fieldnames=list(out[0].keys())); w.writeheader(); w.writerows(out)
def gm(xs):
    xs=[x for x in xs if isinstance(x,float)]
    return math.exp(sum(map(math.log,xs))/len(xs)) if xs else float("nan")
for net in ["w64b384","w256b48m"]:
  print(f"\n### {net}\n\n| workload | v4 / base | geo1 / v4 | geo1s / v4 | geo2 / v4 | geo1 used | geo1s used | geo2 used | mesh B geo1/geo1s/geo2 vs v4 | 网络 line geo1/geo1s/geo2 vs v4 |")
  print("|"+"---|"*10)
  cols={c:[] for c in ["v4","geo1","geo1s","geo2"]}
  for wl in order:
    f=lambda c,k: next((d[k] for d in out if d["scenario"]==net and d["workload"]==wl and d["config"]==c),"")
    s=lambda x: f"{x:.3f}" if isinstance(x,float) else "–"
    v4b=f("v4complete","speedup_vs_baseline"); cols["v4"].append(v4b)
    for c in ["geo1","geo1s","geo2"]: cols[c].append(f("v4"+c,"speedup_vs_control"))
    print(f"| {wl} | {s(v4b)} | {s(f('v4geo1','speedup_vs_control'))} | {s(f('v4geo1s','speedup_vs_control'))} | {s(f('v4geo2','speedup_vs_control'))} | {s(f('v4geo1','push_used_frac'))} | {s(f('v4geo1s','push_used_frac'))} | {s(f('v4geo2','push_used_frac'))} | {s(f('v4geo1','mesh_bytes_vs_control'))} / {s(f('v4geo1s','mesh_bytes_vs_control'))} / {s(f('v4geo2','mesh_bytes_vs_control'))} | {s(f('v4geo1','net_lines_vs_control'))} / {s(f('v4geo1s','net_lines_vs_control'))} / {s(f('v4geo2','net_lines_vs_control'))} |")
  print(f"| **GMEAN** | {gm(cols['v4']):.3f} | {gm(cols['geo1']):.3f} | {gm(cols['geo1s']):.3f} | {gm(cols['geo2']):.3f} | | | | | |")
