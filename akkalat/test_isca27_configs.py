"""The config files must stay identical to the flags the paper scripts pass."""

import os
import re
import unittest

import isca27_config
import isca27_flags
from runall2_config import apply_gpm_preset
from runall2_constants import (
    BASE_COMMON_FLAGS,
    DEFAULT_MMUTLB_LOOKUP_LATENCY,
    GPM_PRESET_FLAGS,
    MI200_FLAGS,
)

HERE = os.path.dirname(os.path.abspath(__file__))
CONFIGS = os.path.join(HERE, "configs")
MI200_REASON = isca27_config.PRESET_REASON["mi200"]


def go_preset_fields(name):
    """Integer fields of the gpm_preset.go preset whose Name is `name`."""
    with open(os.path.join(HERE, "baseline", "runner", "gpm_preset.go")) as f:
        text = f.read()
    start = text.index(f'Name:       "{name}",')
    body = text[start:text.index("\n}\n", start)]
    return {k: int(v) for k, v in re.findall(r"^\t(\w+):\s+(\d+),", body, re.M)}


def stage4_llm_parts():
    """(preset, cu-per-gpu) of every LLM part in isca27_stage4.sh."""
    with open(os.path.join(HERE, "isca27_stage4.sh")) as f:
        return re.findall(r"^\s*part \d+ llm (\S+) \S+ (\d+)$", f.read(), re.M)


def as_flags(values):
    flags = set()
    for name, value in values.items():
        if name == "gpm-preset" and value == "hpca":
            continue  # the simulator default; BASE_COMMON_FLAGS does not pass it
        flags.add(f"-{name}" if value == "true" else f"-{name}={value}")
    return flags


class ISCA27ConfigTest(unittest.TestCase):
    def test_hpca_config_equals_paper_flags(self):
        paper = set(BASE_COMMON_FLAGS) | {
            f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}"}
        self.assertEqual(as_flags(isca27_config.load(os.path.join(CONFIGS, "hpca.json"))), paper)

    def test_mi300x_config_equals_preset_flags(self):
        preset = set(apply_gpm_preset(BASE_COMMON_FLAGS[:], "mi300x-eighth")) | {
            f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}"}
        self.assertEqual(
            as_flags(isca27_config.load(os.path.join(CONFIGS, "mi300x_eighth.json"))), preset)

    def test_mi200_config_equals_preset_flags(self):
        preset = set(apply_gpm_preset(BASE_COMMON_FLAGS[:], "mi200")) | {
            f"-mmutlb-lookup-latency={DEFAULT_MMUTLB_LOOKUP_LATENCY}"}
        self.assertEqual(
            as_flags(isca27_config.load(os.path.join(CONFIGS, "mi200.json"))), preset)
        # -num-memory-banks replaces the paper's 4; -l2-cache-size-mb is new.
        self.assertIn("-num-memory-banks=8", preset)
        self.assertNotIn("-num-memory-banks=4", preset)
        self.assertIn("-l2-cache-size-mb=2", preset)

    def test_mi200_flags_match_go_preset(self):
        go = go_preset_fields("mi200")
        self.assertEqual(GPM_PRESET_FLAGS["mi200"], MI200_FLAGS)
        self.assertEqual(MI200_FLAGS, [
            "-gpm-preset=mi200",
            f"-num-memory-banks={go['NumMemoryBanks']}",
            f"-l2-cache-size-mb={go['L2CacheSizeMB']}",
        ])

    def test_stage4_llm_cu_count_matches_preset(self):
        parts = stage4_llm_parts()
        self.assertIn(("mi200", "28"), parts)
        for preset, cu in parts:
            go = go_preset_fields(preset)
            self.assertEqual(int(cu), go["NumSA"] * go["NumCUPerSA"], preset)

    def test_every_preset_has_a_reason(self):
        for preset in GPM_PRESET_FLAGS:
            if preset != "hpca":
                self.assertIn(preset, isca27_config.PRESET_REASON)

    def test_mi200_deviation_is_attributed_to_the_preset(self):
        out = isca27_config.deviation(os.path.join(CONFIGS, "mi200.json"), ["-max-wg=64"],
                                      base=isca27_config.PAPER_CONFIG)
        by_flag = {d["flag"]: d for d in out}
        self.assertEqual(set(by_flag),
                         {"-gpm-preset", "-num-memory-banks", "-l2-cache-size-mb", "-max-wg 64"})
        for flag in ("-gpm-preset", "-num-memory-banks", "-l2-cache-size-mb"):
            self.assertEqual(by_flag[flag]["reason"], MI200_REASON, flag)
        self.assertEqual(by_flag["-l2-cache-size-mb"]["change"], "added")
        self.assertEqual(by_flag["-l2-cache-size-mb"]["value"], "2")

    def test_l2_size_is_a_preset_key_only_for_mi200(self):
        self.assertEqual(isca27_config.preset_keys("hpca"), set())
        self.assertEqual(isca27_config.preset_keys("mi300x-eighth"),
                         {"gpm-preset", "num-memory-banks"})
        self.assertEqual(isca27_config.preset_keys("mi200"),
                         {"gpm-preset", "num-memory-banks", "l2-cache-size-mb"})

    def test_mi200_flags_deviation_is_attributed_to_the_preset(self):
        common = isca27_flags.common_flags(["--gpm-preset=mi200"], {})
        out = isca27_flags.deviation(common, {}, [])
        reasons = {d["flag"]: d["reason"] for d in out}
        for flag in ("-num-memory-banks=4", "-num-memory-banks=8",
                     "-gpm-preset=mi200", "-l2-cache-size-mb=2"):
            self.assertEqual(reasons.get(flag), MI200_REASON, flag)
        self.assertNotIn(isca27_config.UNKNOWN_REASON, reasons.values())

    def test_mi300x_flags_deviation_is_unchanged(self):
        common = isca27_flags.common_flags(["--gpm-preset=mi300x-eighth"], {})
        reason = isca27_config.PRESET_REASON["mi300x-eighth"]
        self.assertEqual(isca27_flags.deviation(common, {}, []), [
            {"flag": "-num-memory-banks=4", "change": "removed", "reason": reason},
            {"flag": "-num-memory-banks=16", "change": "added", "reason": reason},
            {"flag": "-gpm-preset=mi300x-eighth", "change": "added", "reason": reason},
            {"flag": "-max-wg=78600", "change": "added", "reason": "benchmark 截断参数（不算配置修改）"},
        ])

    def test_configs_keep_magic_memory_copy_and_servers(self):
        for name in os.listdir(CONFIGS):
            if name.endswith(".json"):
                values = isca27_config.load(os.path.join(CONFIGS, name))
                self.assertEqual(values.get("magic-memory-copy"), "true", name)
                self.assertNotIn("disable-servers", values, name)

    def test_paper_config_has_no_deviation_from_itself(self):
        self.assertEqual(isca27_config.deviation(os.path.join(CONFIGS, "hpca.json"), [],
                                                 base=isca27_config.PAPER_CONFIG), [])

    def test_mi200_is_the_default_base(self):
        # isca27-mi200-v1 (user 10-16): new runs are compared with mi200.json.
        self.assertEqual(isca27_config.deviation(os.path.join(CONFIGS, "mi200.json"), []), [])

    def test_hpca_against_the_mi200_base_is_attributed_to_the_preset(self):
        out = isca27_config.deviation(os.path.join(CONFIGS, "hpca.json"), [])
        by_flag = {d["flag"]: d for d in out}
        self.assertEqual(set(by_flag), {"-gpm-preset", "-num-memory-banks", "-l2-cache-size-mb"})
        for d in out:
            self.assertEqual(d["reason"], isca27_config.PRESET_REASON["hpca"], d)
        self.assertEqual(by_flag["-num-memory-banks"]["paper"], "8")

    def test_t19_configs_only_add_mechanism_flags(self):
        # T19: mi200 + T19 flags; every difference from mi200.json is a T19
        # mechanism flag (added), no mi200/hpca parameter changes.
        for name in ("t19_dist.json", "t19_filter.json"):
            out = isca27_config.deviation(os.path.join(CONFIGS, name), [])
            self.assertTrue(out, name)
            for d in out:
                self.assertTrue(d["flag"].startswith("-t19-"), d)
                self.assertEqual(d["change"], "added", d)
                self.assertEqual(d["reason"], isca27_config.MECH_PREFIXES["t19-"], d)
        on = isca27_config.merged(os.path.join(CONFIGS, "t19_filter.json"))
        self.assertEqual(on["t19-steal"], "true")
        self.assertEqual(on["t19-filter"], "true")
        dist = isca27_config.merged(os.path.join(CONFIGS, "t19_dist.json"))
        self.assertNotIn("t19-filter", dist)


if __name__ == "__main__":
    unittest.main()
