"""Paper-configuration flags for T11 probe runs.

The flags are never written by hand: they come from runall2's own
build_common_flags() (BASE_COMMON_FLAGS + -disable-servers +
-mmutlb-lookup-latency), exactly as the paper's run scripts build them.

    python3 akkalat/t11_probe_flags.py --list
        One common flag per line, for the run script.
    python3 akkalat/t11_probe_flags.py --deviation [run-specific flags ...]
        JSON list of differences from the paper configuration, for meta.json.
"""

import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import runall2_args  # noqa: E402
import runall2_config  # noqa: E402
from runall2_constants import (  # noqa: E402
    BASE_COMMON_FLAGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
)

# Probe kernels are tiny, so no -max-wg truncation (0 = no limit).
RUNALL2_ARGS = ["--disable-servers", "--max-wg", "0", "--report-l2-source"]

STAT_FLAGS = {
    "-report-l2-source": "统计开关：L2 数据来源（本地/远程 × L2/DRAM）",
    "-l2-source-tile-width=7": "统计开关的参数：7×7 mesh 宽度，用来算跳数",
}


def common_flags():
    saved = sys.argv
    sys.argv = ["runall2.py"] + RUNALL2_ARGS
    try:
        return runall2_config.build_common_flags(runall2_args.parse_args())
    finally:
        sys.argv = saved


def paper_flags():
    return BASE_COMMON_FLAGS + [
        "-disable-servers",
        f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}",
    ]


def deviation(extra):
    common, paper = common_flags(), paper_flags()
    out = []
    for flag in paper:
        if flag not in common:
            out.append({"flag": flag, "change": "removed", "reason": "未知，需要检查"})
    for flag in common:
        if flag not in paper:
            out.append({"flag": flag, "change": "added",
                        "reason": STAT_FLAGS.get(flag, "未知，需要检查")})
    for flag in extra:
        if flag.startswith("-"):
            out.append({"flag": flag, "change": "added",
                        "reason": "benchmark 参数（不算配置修改）"})
        elif out:
            out[-1]["flag"] += " " + flag
    return out


def main():
    if len(sys.argv) >= 2 and sys.argv[1] == "--list":
        print("\n".join(common_flags()))
    elif len(sys.argv) >= 2 and sys.argv[1] == "--deviation":
        print(json.dumps(deviation(sys.argv[2:]), ensure_ascii=False))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
