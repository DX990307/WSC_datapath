"""Configuration files for ISCA'27 task runs (akkalat/configs/*.json).

A config file maps simulator flag names to values; "extends" names a base
file and keys starting with "_" are comments. The simulator reads them with
-config a.json,b.json (later files override earlier ones; flags given on the
command line override every file).

    python3 akkalat/isca27_config.py --show configs/mi300x_eighth.json
        The merged flag values.
    python3 akkalat/isca27_config.py --deviation CONFIGS [-- benchmark args ...]
        JSON list of differences from the paper configuration
        (configs/hpca.json), for meta.json's config_deviation.
"""

import json
import os
import sys

ROOT = os.path.dirname(os.path.abspath(__file__))
PAPER_CONFIG = os.path.join(ROOT, "configs", "hpca.json")

PRESET_REASON = ("T13 预设 mi300x-eighth：GPM 内部改为 1/8 MI300X（38 CU、16 个 L2/HBM channel）；"
                 "带宽按 2.1 GHz 每周期等效，延迟保持纳秒值、不做每周期等效缩放")
STAT_FLAGS = {
    "-report-l2-source": "统计开关：L2 数据来源（本地/远程 × L2/DRAM）",
    "-l2-source-tile-width": "统计开关的参数：7×7 mesh 宽度，用来算跳数",
    "-heartbeat-file": "运行监控：心跳文件",
}


def load(path, seen=()):
    path = os.path.abspath(path)
    if path in seen:
        raise ValueError(f"extends cycle at {path}")
    with open(path) as f:
        raw = json.load(f)
    values = {}
    if "extends" in raw:
        values = load(os.path.join(os.path.dirname(path), raw["extends"]), seen + (path,))
    for name, value in raw.items():
        if name == "extends" or name.startswith("_"):
            continue
        values[name] = value_string(value)
    return values


def value_string(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, float) and v.is_integer():
        return str(int(v))
    return str(v)


def merged(config_list):
    values = {}
    for path in [p for p in config_list.split(",") if p.strip()]:
        values.update(load(path.strip()))
    return values


def deviation(config_list, bench):
    paper, used = load(PAPER_CONFIG), merged(config_list)
    preset_on = used.get("gpm-preset", "hpca") != "hpca"
    preset_keys = {"gpm-preset", "num-memory-banks"}
    out = []
    for name in sorted(set(paper) | set(used)):
        if paper.get(name) == used.get(name):
            continue
        reason = PRESET_REASON if preset_on and name in preset_keys else f"配置文件 {config_list}"
        out.append({"flag": f"-{name}", "change": "changed" if name in paper and name in used
                    else ("added" if name in used else "removed"),
                    "paper": paper.get(name), "value": used.get(name), "reason": reason})
    i = 0
    while i < len(bench):
        arg = bench[i]
        if not arg.startswith("-"):
            i += 1
            continue
        name, _, value = arg.lstrip("-").partition("=")
        if not value and i + 1 < len(bench) and not bench[i + 1].startswith("-"):
            value = bench[i + 1]
            i += 1
        flag = f"-{name}" + (f" {value}" if value else "")
        if name in used or name in paper:
            reason = "命令行覆盖配置文件"
        else:
            reason = STAT_FLAGS.get(f"-{name}", "benchmark 参数（不算配置修改）")
        out.append({"flag": flag, "change": "added", "reason": reason})
        i += 1
    return out


def main():
    args = sys.argv[1:]
    if len(args) >= 2 and args[0] == "--show":
        print(json.dumps(merged(args[1]), indent=2, sort_keys=True))
    elif len(args) >= 2 and args[0] == "--deviation":
        bench = args[3:] if len(args) > 2 and args[2] == "--" else args[2:]
        print(json.dumps(deviation(args[1], bench), ensure_ascii=False))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
