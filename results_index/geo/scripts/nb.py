#!/usr/bin/env python3
"""nb.py OUTDIR: collect new-baseline (819ed43) geo runs + pgcls controls; write nb.csv, print table."""
import json, subprocess, sys, csv, math
OUT=sys.argv[1]
PAT="~/akita-exp-test/geo/src/results/1022_abl_geo-w256b384-*_819ed43 ~/akita-exp-test/pgcls/src/results/1022_diag_pgcls-w256b384-*_mi200_149dff0 ~/akita-exp-test/pgcls/src/results/1022_diag_pgcls-w256b384-*_mi200+v4complete_149dff0"
R=[]
for h in ["yss2","yss3"]:
    r=subprocess.run(["ssh",h,f"python3 ~/akita-exp-test/geo/ext_geo.py $(ls -d {PAT} 2>/dev/null)"],capture_output=True,text=True)
    for l in r.stdout.splitlines():
        d=json.loads(l); d["host"]=h; R.append(d)
open(f"{OUT}/nb_raw.jsonl","w").write("\n".join(json.dumps(d) for d in R)+"\n")
T={}
for d in R:
    if d.get("exit")!=0 or not d.get("total_time"): continue
    e=d["exp"]
    wl=e.split("w256b384-")[1].split("_")[0]
    cfg=e.split("_")[-2].replace("mi200+","")
    if cfg=="mi200": cfg="baseline"
    T[(wl,cfg)]=d
order=["pr-g24","npbcg-W","mm-g12","tc-s12e16","sssp-s18e4-r1","bfs-s18e2","cfd-c172k-b128","ar-pc1"]
cfgs=["v4geo1","v4geo1s","v4aff1","v4aff2s"]
rows=[]
print("| workload | v4 / base | "+" | ".join(c[2:] for c in cfgs)+" | 用到比例 "+"/".join(c[2:] for c in cfgs)+" | 网络 line / v4 | 每次推送副本 aff1/aff2s | 伙伴 ≤2 跳比例 aff1/aff2s |")
print("|"+"---|"*7)
sp={c:[] for c in cfgs+["v4"]}
for w in order:
    b=T.get((w,"baseline")); v=T.get((w,"v4complete"))
    def f(x): return f"{x:.3f}" if isinstance(x,float) else "–"
    vb=b["total_time"]/v["total_time"] if b and v else None
    if vb: sp["v4"].append(vb)
    s=[];u=[];nl=[];cp=[];nh=[]
    for c in cfgs:
        r=T.get((w,c))
        x=v["total_time"]/r["total_time"] if r and v else None
        if x: sp[c].append(x)
        s.append(f(x))
        if r:
            L=r.get("geo_push_owner_push_lines",0); used=r.get("geo_push_l2_useful",0)+r.get("geo_push_catch_inflight",0)+r.get("geo_push_catch_queued",0)
            u.append(f(used/L) if L else "–")
            nl.append(f(r.get("remote_wire_lines",0)/v["remote_wire_lines"]) if v and v.get("remote_wire_lines") else "–")
            hops={int(k[-2:]):vv for k,vv in r.items() if k.startswith("geo_push_aff_partner_hops")}
            if hops:
                cp.append(f(L/max(1,r.get("geo_push_owner_push_packets",1))))
                nh.append(f(sum(vv for k,vv in hops.items() if k<=2)/sum(hops.values())))
            rows.append(dict(workload=w,config=c,exp=r["exp"],total_time_us=r["total_time"]*1e6,speedup_vs_v4=x or "",speedup_vs_base=(b["total_time"]/r["total_time"]) if b else "",
                push_line_copies=L,push_used_frac=(used/L) if L else "",net_lines=r.get("remote_wire_lines",0),net_lines_vs_v4=(r.get("remote_wire_lines",0)/v["remote_wire_lines"]) if v and v.get("remote_wire_lines") else "",
                mesh_bytes=r.get("mesh_bytes",""),link_max_util=r.get("link_max_util",""),hot_0to7=r.get("link_0->7_util",""),hot_48to41=r.get("link_48->41_util",""),
                aff_no_partner=r.get("geo_push_aff_no_partner_rsps",""),aff_partner_hops=json.dumps(hops) if hops else ""))
        else: u.append("–"); nl.append("–")
    print(f"| {w} | {f(vb)} | "+" | ".join(s)+" | "+" / ".join(u)+" | "+" / ".join(nl)+" | "+" / ".join(cp)+" | "+" / ".join(nh)+" |")
gm=lambda xs: math.exp(sum(map(math.log,xs))/len(xs)) if xs else float('nan')
print(f"| **GMEAN** | {gm(sp['v4']):.3f} | "+" | ".join(f"{gm(sp[c]):.3f}" for c in cfgs)+" | | | | |")
if rows:
    w=csv.DictWriter(open(f"{OUT}/nb.csv","w"),fieldnames=list(rows[0].keys())); w.writeheader(); w.writerows(rows)
