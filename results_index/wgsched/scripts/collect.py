import os, json, csv, re
host = os.uname()[1]
for sub in ["qt", "place", "wgsched"]:
    R = os.path.expanduser(f"~/akita-exp-test/{sub}/src/results")
    if not os.path.isdir(R):
        continue
    for e in sorted(os.listdir(R)):
        if sub == "qt" and not re.search(r"qt-w128(c128)?b384-", e):
            continue
        if sub == "place" and "w128b384" not in e:
            continue
        if sub == "wgsched" and "_place_wgs" not in e:
            continue
        d = f"{R}/{e}"
        if not os.path.exists(f"{d}/meta.json"):
            print("\t".join([sub, e, host, "running", ""])); continue
        m = json.load(open(f"{d}/meta.json"))
        tt = ""
        if os.path.exists(f"{d}/summary.csv"):
            for r in csv.reader(open(f"{d}/summary.csv")):
                if r and r[0] == "total_time": tt = r[2]
        print("\t".join([sub, e, host, str(m.get("exit_code")), tt]))
