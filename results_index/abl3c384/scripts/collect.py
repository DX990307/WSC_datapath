import os,json,csv,re,socket
D=os.path.expanduser("~/akita-exp-test/v3c384")
done={l.split("\t")[0]:l.strip().split("\t")[1:] for l in open(D+"/done.tsv")} if os.path.exists(D+"/done.tsv") else {}
q={}
for l in open(D+"/queue.tsv"):
    p=l.rstrip("\n").split("\t"); q[p[2]]=p
for e,(rc,wall,t) in done.items():
    R=f"{D}/src/results/{e}"
    o=dict(exp=e,host=socket.gethostname(),rc=int(rc),wall_h=float(wall),end=t,workload=q[e][0],flags=q[e][4] if e in q else "")
    try:
        for r in csv.reader(open(R+"/summary.csv")):
            if r[0]=="total_time" and r[1]=="Driver": o["total_time"]=float(r[2])
    except Exception: pass
    inst=wg=0; fp=None
    try:
        for r in csv.reader(open(R+"/stats/metrics.csv")):
            if len(r)<4: continue
            n=r[2].strip()
            if n=="cu_inst_count": inst+=float(r[3])
            elif n=="cu_wg_count": wg+=float(r[3])
            elif n=="allocation_overall_requested_bytes" and r[1].strip()=="Driver": fp=float(r[3])/2**20
        o.update(inst=int(inst),wgs=int(wg),footprint_mb=fp)
    except Exception as ex: o["err"]=str(ex)
    try:
        log=open(f"{D}/runs/{e}.log",errors="replace").read()
        m=re.search(r"Maximum resident set size \(kbytes\): (\d+)",log); o["peak_rss_gb"]=round(int(m[1])/2**20,2) if m else None
        m=re.search(r"Elapsed \(wall clock\) time.*: (\S+)",log); o["wall_clock"]=m[1] if m else None
    except Exception: pass
    try: o["meta_exit"]=json.load(open(R+"/meta.json"))["exit_code"]
    except Exception: o["meta_exit"]=None
    print(json.dumps(o))
