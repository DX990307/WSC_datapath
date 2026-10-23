import os
D = os.path.expanduser("~/akita-exp-test/wgsched")
rows = [l.rstrip("\n").split("\t") for l in open(f"{D}/queue.tsv") if l.strip()]
have = {r[0] for r in rows}
new = []
for r in rows:
    e = r[0]
    if "_mi200+wgmap_" not in e and "_mi200+v4m3+wgmap_" not in e:
        continue
    wl = e.split("wgs-")[1].split("_mi200")[0]
    for tag, suf in (("wgbal", "bal"), ("wgmapg", "g")):
        m = f"{D}/maps/{wl}.{suf}.map"
        if not os.path.exists(m):
            continue
        ne = e.replace("+wgmap_", f"+{tag}_")
        if ne in have: continue
        nr = list(r); nr[0] = nr[2] = ne
        nr[4] = r[4].replace(f"maps/{wl}.map", f"maps/{wl}.{suf}.map"); nr[6] = m
        new.append(nr); have.add(ne)
# insert before first m3p row
i = next((j for j, r in enumerate(rows) if "+m3p" in r[0]), len(rows))
rows = rows[:i] + new + rows[i:]
open(f"{D}/queue.tsv.new", "w").write("\n".join("\t".join(r) for r in rows) + "\n")
os.replace(f"{D}/queue.tsv.new", f"{D}/queue.tsv")
print(len(new), "added")
