"""Result directory names for ISCA'27 runs (user decision 2026-10-15).

Every results/<name>/ is named

    <MMDD>_<category>_<item>_<config>_<version>

with no "_" inside a field, so the monitor can split a name into fields,
filter by category and config, and line up runs whose category and item
match but whose config differs.

    MMDD      launch date (UTC)
    category  one of CATEGORIES
    item      what in the category: a plan ("full", "wg32k"), a workload
              ("fir"), a platbench test ("bw-read"), a probe ("P1-o5r5-...")
    config    GPM preset, plus "+<change>" for each temporary change
              ("mi200", "mi200+mshr128")
    version   the 7-hex-digit commit of the code

    python3 akkalat/isca27_name.py check NAME...
        Exit 1 if a name does not follow the rule.
    python3 akkalat/isca27_name.py plan RESULTS_DIR
        Old name -> new name for every directory (tab separated); names no
        rule knows are listed as "?".
    python3 akkalat/isca27_name.py apply RESULTS_DIR [--dry-run]
        Rename the directories of `plan`, rewrite their recorded paths, move
        their marks in ../.exp_archive.json, and append to RENAMES.tsv.
        Refuses while a process uses a directory.
"""

import json
import os
import re
import subprocess
import sys
import time

CATEGORIES = {
    "abl": "ablation: baseline, m1, m2, m3, Complete",
    "audit": "typed-filter authoritative audit",
    "det": "determinism: the same runs again",
    "diag": "diagnostic run",
    "llm": "LLM and CNN operators",
    "place": "data placement (-report-l2-source)",
    "plat": "platform measurement (platbench, T14)",
    "probe": "T11 probes",
    "sens": "sensitivity",
}

_FIELD = r"[A-Za-z0-9][A-Za-z0-9.-]*"
NAME_RE = re.compile(
    r"^(?P<date>\d{4})_(?P<category>[a-z]+)_(?P<item>%s)_"
    r"(?P<config>[a-z0-9][a-z0-9-]*(?:\+[A-Za-z0-9.-]+)*)_(?P<version>[0-9a-f]{7})$" % _FIELD)


def make(date, category, item, config, version):
    """The name of these fields; ValueError if it breaks the rule."""
    name = "_".join([date, category, item, config, version])
    problem = check(name)
    if problem:
        raise ValueError(problem)
    return name


def parse(name):
    """The fields of a name as a dict, or None."""
    if check(name):
        return None
    return NAME_RE.match(name).groupdict()


def check(name):
    """Why a name breaks the rule, or "" if it follows it."""
    m = NAME_RE.match(name)
    if not m:
        return ("%s: 不符合 <MMDD>_<类别>_<项目>_<配置>_<7 位 commit>，"
                "每段内部不能有下划线" % name)
    if m.group("category") not in CATEGORIES:
        return "%s: 类别 %s 不在 %s 中" % (name, m.group("category"), ", ".join(sorted(CATEGORIES)))
    if m.group("item") == "wafer48":
        return "%s: 项目不能是 wafer48（那是旧命名的固定段）" % name
    return ""


# ---- old names -------------------------------------------------------------

# Temporary builds of the 10-15 HBM bandwidth diagnosis (not committed; each
# changed the mi200 preset of 67f1c03).
_HBM_VARIANTS = {
    "v0": "localbuild",
    "v1mshr": "mshr128",
    "v2queue": "queue64-256",
    "v3noref": "norefresh",
    "v4norowhold": "norowhold",
    "v5bus": "nocrossrank-nodualbus",
    "v6": "mshr128-queue64-256",
    "v7": "mshr128-queue64-256-norefresh",
}

# (pattern on the old "kind" field, category, item template, config suffix)
_LEGACY_RULES = [
    (r"t13s4-main", "abl", "full", ""),
    (r"t13s4-filter-audit", "audit", "full", ""),
    (r"t13s4-probe-(.+)", "probe", r"\1", ""),
    (r"probe-(.+)", "probe", r"\1", ""),
    (r"t14(?:m2)?-(.+)", "plat", r"\1", ""),
    (r"t12-(.+)", "diag", r"t12-\1", ""),
    (r"t13-(reg|cfgcheck)-(.+)", "diag", r"\1-\2", ""),
    (r"diag-place-(.+)", "place", r"\1", ""),
    (r"diag-attr", "abl", "attr", ""),
    (r"diag-l2bw-(l1mshr\d+)", "diag", "l2bw", r"+\1"),
    (r"diag-rbw-(d\d+)-(rdma\d+)", "diag", r"rbw-\1", r"+\2"),
    (r"diag-xlat-(.+)", "diag", r"xlat-\1", ""),
    (r"diagrc-(.+)", "diag", r"\1", "+rowcont"),
    (r"abl", "abl", "wg32k", ""),
    (r"abl-spmv", "abl", "spmv-wg1536", ""),
    (r"determinism", "det", "wg32k", ""),
    (r"llm-smoke", "llm", "wg8k", ""),
]
_OLD_RE = re.compile(r"^(\d{4})_(.+)_wafer48_([a-z0-9-]+)_([0-9a-f]{7})$")


def legacy_name(old):
    """The new name of an old-style directory name, or None if no rule fits."""
    m = re.match(r"^(\d{4})_diaghbm-(v\d+[a-z]*)$", old)
    if m and m.group(2) in _HBM_VARIANTS:
        return make(m.group(1), "diag", "hbm-read", "mi200+" + _HBM_VARIANTS[m.group(2)], "67f1c03")
    m = _OLD_RE.match(old)
    if not m:
        return old if parse(old) else None
    date, kind, config, version = m.groups()
    for pattern, category, item, suffix in _LEGACY_RULES:
        k = re.fullmatch(pattern, kind)
        if k:
            return make(date, category, k.expand(item), config + k.expand(suffix), version)
    return None


def plan(results):
    """[(old, new or None)] for the directories in results, sorted by old name."""
    names = sorted(n for n in os.listdir(results) if os.path.isdir(os.path.join(results, n)))
    return [(n, legacy_name(n)) for n in names]


# ---- renaming ----------------------------------------------------------------

_TEXT_SUFFIXES = (".json", ".stdout", ".log", ".txt", ".csv", ".tsv")


def _in_use(paths):
    """Directories some process has open or names on its command line."""
    busy = set()
    out = subprocess.run(["ps", "-eo", "args"], capture_output=True, text=True).stdout
    for p in paths:
        if p in out:
            busy.add(p)
    return busy


def _rewrite(directory, old_path, new_path):
    """Replace old_path with new_path in the small text files under directory."""
    changed = 0
    for root, _dirs, files in os.walk(directory):
        for f in files:
            p = os.path.join(root, f)
            if not f.endswith(_TEXT_SUFFIXES) or os.path.getsize(p) > 64 << 20:
                continue
            if f.endswith("_metrics.csv") or f == "metrics.csv":
                continue  # statistics only, never paths
            with open(p, "rb") as fh:
                data = fh.read()
            new = data.replace(old_path.encode(), new_path.encode())
            if new != data:
                with open(p, "wb") as fh:
                    fh.write(new)
                changed += 1
    return changed


def apply(results, dry_run=False, out=sys.stdout):
    results = os.path.abspath(results)
    pairs = [(o, n) for o, n in plan(results) if n and n != o]
    unknown = [o for o, n in plan(results) if n is None]
    news = [n for _, n in plan(results) if n]
    dups = sorted({n for n in news if news.count(n) > 1})
    if dups:
        raise SystemExit("改名后会重名，没有做任何改动：%s" % ", ".join(dups))
    busy = _in_use([os.path.join(results, o) for o, _ in pairs])
    if busy:
        raise SystemExit("这些目录还有进程在用，没有做任何改动：\n  " + "\n  ".join(sorted(busy)))
    for o, n in pairs:
        print("%s\t%s" % (o, n), file=out)
    for o in unknown:
        print("%s\t?" % o, file=out)
    if dry_run:
        return pairs

    archive = os.path.join(os.path.dirname(results), ".exp_archive.json")
    db = None
    if os.path.exists(archive):
        with open(archive) as f:
            db = json.load(f)
    log = os.path.join(results, "RENAMES.tsv")
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    for o, n in pairs:
        src, dst = os.path.join(results, o), os.path.join(results, n)
        os.rename(src, dst)
        _rewrite(dst, src, dst)
        if db and o in db.get("results", {}):
            db["results"][n] = db["results"].pop(o)
        with open(log, "a") as f:
            f.write("%s\t%s\t%s\n" % (now, o, n))
    if db is not None:
        tmp = archive + ".tmp"
        with open(tmp, "w") as f:
            json.dump(db, f, ensure_ascii=False, indent=1)
        os.replace(tmp, archive)
    return pairs


def main(argv):
    if len(argv) >= 2 and argv[0] == "check":
        problems = [p for p in map(check, argv[1:]) if p]
        print("\n".join(problems) or "ok")
        return 1 if problems else 0
    if len(argv) == 2 and argv[0] == "plan":
        for o, n in plan(argv[1]):
            print("%s\t%s" % (o, n or "?"))
        return 0
    if len(argv) >= 2 and argv[0] == "apply":
        apply(argv[1], dry_run="--dry-run" in argv[2:])
        return 0
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
