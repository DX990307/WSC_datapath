import json,subprocess,sys,math,collections
rows=[]
for h in ("yss2","yss3"):
    out=subprocess.run(["ssh","-o","ControlMaster=no",h,"python3 ~/akita-exp-test/v3c384/collect.py"],capture_output=True,text=True).stdout
    for l in out.splitlines():
        try: o=json.loads(l); o["host"]=h; rows.append(o)
        except Exception: pass
R=collections.defaultdict(dict)
for o in rows:
    p=o["exp"].split("_"); item=p[2].replace("v3c-",""); cfg=p[3]
    R[item][cfg]=o
cfgs=["mi200+v3complete","mi200+v3m1","mi200+v3m1m2","mi200+v3m1m3","mi200+m3","mi200+v3c-nomerge","mi200+v3c-nopack","mi200+v3c-allocshared","mi200+v3c-allocnear"]
short=["C","M1","M1M2","M1M3","M3","-mrg","-pack","shar","near"]
print("item".ljust(22),"base_us".rjust(9)," ".join(s.rjust(6) for s in short))
g=collections.defaultdict(list)
for it in sorted(R):
    b=R[it].get("mi200")
    if not b or b["rc"]!=0 or "total_time" not in b: 
        continue
    line=[]
    for c in cfgs:
        o=R[it].get(c)
        if o and o["rc"]==0 and o.get("total_time"):
            s=b["total_time"]/o["total_time"]; g[c].append(s)
            flag="" if (o.get("wgs")==b.get("wgs") and abs((o.get("inst") or 0)-(b.get("inst") or 0))<=1e-3*(b.get("inst") or 1)) else "*"
            line.append(f"{s:.3f}{flag}".rjust(6))
        else: line.append("-".rjust(6))
    print(it.ljust(22),f"{b['total_time']*1e6:9.1f}"," ".join(line))
print("GMEAN".ljust(22)," "*9," ".join((f"{math.exp(sum(map(math.log,g[c]))/len(g[c])):.3f}" if g[c] else "-").rjust(6) for c in cfgs))
print("n".ljust(22)," "*9," ".join(str(len(g[c])).rjust(6) for c in cfgs))
bad=[o["exp"] for o in rows if o["rc"]!=0]
print("nonzero exit:",bad)
print("done total:",len(rows))
json.dump(rows,open(sys.argv[1] if len(sys.argv)>1 else "/dev/null","w"))
