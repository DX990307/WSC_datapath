"""Configuration files for ISCA'27 task runs (akkalat/configs/*.json).

A config file maps simulator flag names to values; "extends" names a base
file and keys starting with "_" are comments. The simulator reads them with
-config a.json,b.json (later files override earlier ones; flags given on the
command line override every file).

    python3 akkalat/isca27_config.py --show configs/mi300x_eighth.json
        The merged flag values.
    python3 akkalat/isca27_config.py [--base FILE] --deviation CONFIGS [-- benchmark args ...]
        JSON list of differences from the base configuration, for
        meta.json's config_deviation. The base is configs/mi200.json, the
        frozen ISCA'27 baseline (isca27-mi200-v1, user 10-16); --base
        configs/hpca.json gives the differences from the HPCA submission
        as before.
"""

import json
import os
import sys

ROOT = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, ROOT)

from runall2_constants import GPM_PRESET_FLAGS  # noqa: E402

PAPER_CONFIG = os.path.join(ROOT, "configs", "hpca.json")  # the HPCA submission
BASE_CONFIG = os.path.join(ROOT, "configs", "mi200.json")  # isca27-mi200-v1

# Why a GPM preset changes the paper configuration, keyed by -gpm-preset.
# isca27_flags.py reports the same reasons.
PRESET_REASON = {
    "hpca": "HPCA 投稿版的 GPM（hpca 预设：32 CU、4 个 L2/DRAM 分区）",
    "mi300x-eighth": ("T13 预设 mi300x-eighth：GPM 内部改为 1/8 MI300X（38 CU、16 个 L2/HBM channel）；"
                      "带宽按 2.1 GHz 每周期等效，延迟保持纳秒值、不做每周期等效缩放"),
    "mi200": ("T13 预设 mi200：GPM 内部改为 1/4 MI250X GCD（28 CU、8 个 L2 slice/HBM2e channel，"
              "每个 channel 2 个 pseudo-channel）；带宽按 1.7 GHz 每周期等效，延迟保持纳秒值、"
              "不做每周期等效缩放"),
}
UNKNOWN_REASON = "未知，需要检查"
STAT_FLAGS = {
    "-report-l2-source": "统计开关：L2 数据来源（本地/远程 × L2/DRAM）",
    "-l2-source-tile-width": "统计开关的参数：7×7 mesh 宽度，用来算跳数",
    "-heartbeat-file": "运行监控：心跳文件",
    "-report-gpm-trace": "统计开关（T16）：每个 kernel × GPM 的 WG 数、完成时间、本地/远程访问",
    "-gpm-trace-file": "统计开关的参数：T16 per-GPM CSV 的文件名",
    "-report-wg-footprint": "统计开关（P12）：每个 WG 读过的 4 KB 页集合",
    "-wg-footprint-file": "统计开关的参数：P12 WG 足迹文件名",
    "-wg-footprint-max-pages": "统计开关的参数：P12 每个 WG 最多记录的页数",
    "-report-m1-sharing": "统计开关（P12）：远程 line 跨 GPM 共享和在途重复请求",
    "-m1-sharing-file": "统计开关的参数：P12 M1 统计文件名前缀",
}


# Oracle switches (T15 O1; T17 O1 in P12): intentional deviations for analysis
# runs, not benchmark parameters.
ORACLE_FLAGS = {
    "-force-local-data-access": "oracle（有意偏离 mi200.json 的分析实验）：L1V miss 一律走本地 L2/DRAM",
}

# Mesh size (P13 C2, scaling runs R5/R6): an intentional configuration
# deviation that the user must confirm, not a benchmark parameter.
MESH_FLAGS = {
    "-mesh-size": "规模实验（有意偏离 mi200.json，需要用户确认）：N×N mesh，CPU/IOMMU 在中心，N²−1 个 GPM",
}


def preset_keys(preset):
    """Flag names the preset sets (gpm-preset, num-memory-banks, ...)."""
    return {f.lstrip("-").split("=", 1)[0] for f in GPM_PRESET_FLAGS.get(preset, [])}


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


def deviation(config_list, bench, base=None):
    """Differences of config_list (plus bench args) from the base config.

    The "paper" key of each entry holds the base config's value (the name is
    kept for the readers of existing meta.json files)."""
    paper, used = load(base or BASE_CONFIG), merged(config_list)
    preset = used.get("gpm-preset", "hpca")
    # A key that either the run's preset or the base's preset sets differs
    # because of the preset (e.g. hpca against the mi200 base).
    keys = preset_keys(preset) | preset_keys(paper.get("gpm-preset", "hpca"))
    out = []
    for name in sorted(set(paper) | set(used)):
        if paper.get(name) == used.get(name):
            continue
        if name in keys:
            reason = PRESET_REASON.get(preset, UNKNOWN_REASON)
        else:
            reason = f"配置文件 {config_list}"
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
            reason = (STAT_FLAGS.get(f"-{name}") or ORACLE_FLAGS.get(f"-{name}")
                      or MESH_FLAGS.get(f"-{name}", "benchmark 参数（不算配置修改）"))
        out.append({"flag": flag, "change": "added", "reason": reason})
        i += 1
    return out


def main():
    args = sys.argv[1:]
    base = None
    if len(args) >= 2 and args[0] == "--base":
        base, args = args[1], args[2:]
    if len(args) >= 2 and args[0] == "--show":
        print(json.dumps(merged(args[1]), indent=2, sort_keys=True))
    elif len(args) >= 2 and args[0] == "--deviation":
        bench = args[3:] if len(args) > 2 and args[2] == "--" else args[2:]
        print(json.dumps(deviation(args[1], bench, base), ensure_ascii=False))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
