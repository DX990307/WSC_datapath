"""Shared loader for dm/ analysis: reads raw/*.jsonl.gz (from srv_extract.py)."""
import gzip, json, os, re
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SIX = ["pr-g24", "npbcg-W", "sssp-s18e4-r1", "bfs-s18e2", "ar-pc1", "cfd-c172k-b128"]
W = 7
CENTER = 24
CORNERS = {0, 6, 42, 48}
def pos(t):
    x, y = t % W, t // W
    if t in CORNERS: return "corner"
    if x in (0, W - 1) or y in (0, W - 1): return "edge"
    return "interior"
def hops(a, b):
    return abs(a % W - b % W) + abs(a // W - b // W)
def parse(exp):
    m = re.match(r"1021_abl_v3c-(.+)_mi200(\+v3complete)?_4235991$", exp)
    if m: return "w64b384", m[1], "Complete" if m[2] else "baseline"
    m = re.match(r"1021_sens_m2w-(w256b48m|w2048b48)-(.+)_mi200(\+v3complete)?_4235991$", exp)
    if m: return m[1], m[2], "Complete" if m[3] else "baseline"
    return None
def load():
    R = {}
    for h in ("yss2", "yss3"):
        for l in gzip.open(os.path.join(HERE, "raw", h + ".jsonl.gz"), "rt"):
            o = json.loads(l)
            k = parse(o["exp"])
            if k: o["host"] = h; o["gpm"] = {int(t): v for t, v in o["gpm"].items()}; o["gpm_trace"] = {int(t): v for t, v in (o.get("gpm_trace") or {}).items()}; R[k] = o
    return R
def F(x):
    try: return float(x)
    except Exception: return 0.0
SW = re.compile(r"SW\[(\d+)\]\[(\d+)\]\[0\]")
def link_tiles(r):
    a = SW.search(r["link_src"]); b = SW.search(r["link_dst"])
    return int(a[1]) + W * int(a[2]), int(b[1]) + W * int(b[2])
