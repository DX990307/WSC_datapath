#!/usr/bin/env python3
"""collect.py OUT.jsonl : run ext_geo.py on both servers over geo + reused runs."""
import json, subprocess, sys
PAT = [
  "~/akita-exp-test/geo/src/results/1021_*geo*",
  "~/akita-exp-test/v3c384/src/results/1021_abl_v3c-*_mi200_4235991",
  "~/akita-exp-test/v3c384/src/results/1021_abl_v3c-*_mi200+v3complete_4235991",
  "~/akita-exp-test/v3m2w/src/results/1021_sens_m2w-w256b48m-*_mi200_4235991",
  "~/akita-exp-test/v3m2w/src/results/1021_sens_m2w-w256b48m-*_mi200+v3complete_4235991",
]
out = open(sys.argv[1], "w")
for h in ["yss2", "yss3"]:
    cmd = "python3 ~/akita-exp-test/geo/ext_geo.py $(ls -d " + " ".join(PAT) + " 2>/dev/null)"
    r = subprocess.run(["ssh", h, cmd], capture_output=True, text=True)
    for line in r.stdout.splitlines():
        d = json.loads(line); d["host"] = h
        out.write(json.dumps(d) + "\n")
