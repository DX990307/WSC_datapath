"""Paper-configuration flags for ISCA'27 task runs (T12 onward).

The configuration flags are never written by hand: they come from runall2's
own build_common_flags(), exactly as the paper's run scripts build them.
Any difference from the paper configuration (CONFIG.md section 2) is
reported by --deviation for meta.json's config_deviation.

    python3 akkalat/isca27_flags.py [--runall2 ARG]... [--drop FLAG=REASON]...
        --list
    python3 akkalat/isca27_flags.py [same options] --deviation [bench args ...]

--runall2 passes one argument to runall2's parser (e.g. --runall2=--max-wg
--runall2=4096); --disable-servers is always passed. --drop removes a flag
from the generated list and records REASON.
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import runall2_args  # noqa: E402
import runall2_config  # noqa: E402
from isca27_config import PRESET_REASON, UNKNOWN_REASON, preset_keys  # noqa: E402
from runall2_constants import (  # noqa: E402
    BASE_COMMON_FLAGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
)

KNOWN_REASONS = {
    "-report-l2-source": "统计开关：L2 数据来源（本地/远程 × L2/DRAM）",
    "-max-wg=": "benchmark 截断参数（不算配置修改）",
    "-report-gpm-trace": "统计开关（T16）：每个 kernel × GPM 的 WG 数、完成时间、本地/远程访问",
    "-gpm-trace-file=": "统计开关的参数：T16 per-GPM CSV 的文件名",
}


def reason_for(flag):
    for prefix, reason in KNOWN_REASONS.items():
        if flag.startswith(prefix):
            return reason
    if flag.startswith("-gpm-preset="):
        return PRESET_REASON.get(flag.split("=", 1)[1], UNKNOWN_REASON)
    if flag.startswith("-l2-source-tile-width="):
        return "统计开关的参数：7×7 mesh 宽度，用来算跳数"
    return UNKNOWN_REASON


def common_flags(runall2_extra, drops):
    saved = sys.argv
    sys.argv = ["runall2.py", "--disable-servers"] + runall2_extra
    try:
        flags = runall2_config.build_common_flags(runall2_args.parse_args())
    finally:
        sys.argv = saved
    missing = [d for d in drops if d not in flags]
    if missing:
        sys.exit(f"cannot drop flags that are not generated: {missing}")
    return [f for f in flags if f not in drops]


def paper_flags():
    return BASE_COMMON_FLAGS + [
        "-disable-servers",
        f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}",
    ]


def deviation(common, drops, bench):
    paper = paper_flags()
    out = []
    preset = "hpca"
    for f in common:
        if f.startswith("-gpm-preset="):
            preset = f.split("=", 1)[1]
    preset_on = preset != "hpca"
    preset_reason = PRESET_REASON.get(preset, UNKNOWN_REASON)
    keys = {"-" + k for k in preset_keys(preset)}
    replaced = {f.split("=", 1)[0] for f in common if "=" in f}
    for flag in paper:
        if flag not in common:
            name = flag.split("=", 1)[0]
            if flag in drops:
                reason = drops[flag]
            elif preset_on and "=" in flag and name in replaced:
                reason = preset_reason
            else:
                reason = UNKNOWN_REASON
            out.append({"flag": flag, "change": "removed", "reason": reason})
    for flag in common:
        if flag not in paper:
            name = flag.split("=", 1)[0]
            reason = reason_for(flag)
            if reason == UNKNOWN_REASON and name in keys:
                reason = preset_reason
            out.append({"flag": flag, "change": "added", "reason": reason})
    for flag in bench:
        if flag.startswith("-"):
            out.append({"flag": flag, "change": "added",
                        "reason": "benchmark 参数（不算配置修改）"})
        elif out:
            out[-1]["flag"] += " " + flag
    return out


def main():
    ap = argparse.ArgumentParser(usage=__doc__)
    ap.add_argument("--runall2", action="append", default=[])
    ap.add_argument("--drop", action="append", default=[])
    mode = ap.add_mutually_exclusive_group(required=True)
    mode.add_argument("--list", action="store_true")
    mode.add_argument("--deviation", action="store_true")
    args, bench = ap.parse_known_args()

    drops = {}
    for d in args.drop:
        flag, _, reason = d.partition("=REASON:")
        drops[flag] = reason or "未说明"

    common = common_flags(args.runall2, drops)
    if args.list:
        print("\n".join(common))
    else:
        print(json.dumps(deviation(common, drops, bench), ensure_ascii=False))


if __name__ == "__main__":
    main()
