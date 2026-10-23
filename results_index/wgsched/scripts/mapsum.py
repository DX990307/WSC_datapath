import csv,sys,glob,os
for f in sorted(glob.glob(os.path.expanduser("~/akita-exp-test/wgsched/maps/*.summary.csv"))):
    ks=list(csv.DictReader(open(f)))
    if not ks: print(f, "empty"); continue
    tb=sum(float(k["bytes"] or 0) for k in ks) or 1
    ld=sum(float(k["bytes"])*float(k["local_default"] or 0) for k in ks)/tb
    lm=sum(float(k["bytes"])*float(k["local_map"] or 0) for k in ks)/tb
    hr=sum(float(k["bytes"])*float(k["byte_hops_ratio"] or 1) for k in ks)/tb
    nw=sum(int(k["total_wg"]) for k in ks)
    md=sum(float(k["max_over_avg_default"])*int(k["total_wg"]) for k in ks)/nw
    mm=sum(float(k["max_over_avg_map"])*int(k["total_wg"]) for k in ks)/nw
    print("%-20s K=%-4d WG=%-7d local %.2f->%.2f  bytehops %.2f  max/avg %.2f->%.2f" % (os.path.basename(f)[:-16], len(ks), nw, ld, lm, hr, md, mm))
