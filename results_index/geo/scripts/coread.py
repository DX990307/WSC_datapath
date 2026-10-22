#!/usr/bin/env python3
"""coread.py DIR (files <wl>.csv) -> markdown summary + coread_summary.csv"""
import csv, sys, os, statistics as st, collections
D=sys.argv[1]
order=["pr-g24","npbcg-W","mm-g12","tc-s12e16","sssp-s18e4-r1","bfs-s18e2","cfd-c172k-b128","ar-pc1"]
out=[]
print("| workload | 窗口 | 有共读的 GPM | top-1 份额 中位/均值 | top-2 份额 中位/均值 | top-1 伙伴：编号差≤2 且同行 / 竖直 1 跳 / 编号差≤2 但跨行 / 其他 | top-1 伙伴平均跳数 | 共读加权平均跳数 | 随机期望跳数 | 换行对强度（中位，相对该 GPM 的 top-1） |")
print("|"+"---|"*10)
for wl in order:
    f=f"{D}/{wl}.csv"
    if not os.path.exists(f): continue
    P={1000:collections.defaultdict(dict),10000:collections.defaultdict(dict)}
    tile={}; hops={}
    for r in csv.DictReader(open(f)):
        i,j=int(r["gpm_i"]),int(r["gpm_j"]); tile[i]=int(r["tile_i"])
        if j<0: continue
        w=int(float(r["window_ns"])); P[w][i][j]=int(r["count"]); hops[(i,j)]=int(r["hops"])
    coords={g:(t%7,t//7) for g,t in tile.items()}
    def h(a,b): return abs(coords[a][0]-coords[b][0])+abs(coords[a][1]-coords[b][1])
    rnd=st.mean(h(a,b) for a in coords for b in coords if a!=b)
    for w in (1000,10000):
        t1=[];t2=[];kinds=collections.Counter();th=[];wsum=0;wh=0;wrap=[]
        for i,row in P[w].items():
            tot=sum(row.values())
            if tot<50: continue
            s=sorted(row.items(),key=lambda x:-x[1])
            t1.append(s[0][1]/tot); t2.append((s[0][1]+(s[1][1] if len(s)>1 else 0))/tot)
            j=s[0][0]; hh=h(i,j); th.append(hh)
            if abs(i-j)<=2 and hh==abs(i-j): kinds["both"]+=1
            elif abs(i-j)<=2: kinds["wrap"]+=1
            elif hh==1: kinds["mesh"]+=1
            else: kinds["other"]+=1
            for jj,v in row.items(): wsum+=v; wh+=v*h(i,jj)
            # row-wrap partner: GPM at x=6 <-> next index at x=0
            for jj in (i-1,i+1):
                if jj in coords and abs(coords[i][0]-coords[jj][0])==6:
                    wrap.append(row.get(jj,0)/s[0][1])
        n=len(t1)
        if not n: continue
        out.append(dict(workload=wl,window_ns=w,gpms=n,top1_med=st.median(t1),top1_mean=st.mean(t1),top2_med=st.median(t2),top2_mean=st.mean(t2),
            top1_idx_and_mesh_adj=kinds["both"],top1_wrap=kinds["wrap"],top1_mesh_only=kinds["mesh"],top1_other=kinds["other"],
            top1_hops_mean=st.mean(th),coread_hops_mean=wh/wsum,random_hops=rnd,wrap_rel_median=st.median(wrap) if wrap else ""))
        o=out[-1]
        print(f"| {wl} | {w//1000} µs | {n} | {o['top1_med']:.2f} / {o['top1_mean']:.2f} | {o['top2_med']:.2f} / {o['top2_mean']:.2f} | {kinds['both']} / {kinds['mesh']} / {kinds['wrap']} / {kinds['other']} | {o['top1_hops_mean']:.1f} | {o['coread_hops_mean']:.2f} | {rnd:.2f} | {o['wrap_rel_median'] if wrap=='' else f'{st.median(wrap):.2f}'} |")
w=csv.DictWriter(open(f"{D}/coread_summary.csv","w"),fieldnames=list(out[0].keys())); w.writeheader(); w.writerows(out)
