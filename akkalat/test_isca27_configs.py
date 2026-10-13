"""The config files must stay identical to the flags the paper scripts pass."""

import os
import unittest

import isca27_config
from runall2_config import apply_gpm_preset
from runall2_constants import BASE_COMMON_FLAGS, DEFAULT_MMUTLB_LOOKUP_LATENCY

CONFIGS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "configs")


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

    def test_configs_keep_magic_memory_copy_and_servers(self):
        for name in os.listdir(CONFIGS):
            if name.endswith(".json"):
                values = isca27_config.load(os.path.join(CONFIGS, name))
                self.assertEqual(values.get("magic-memory-copy"), "true", name)
                self.assertNotIn("disable-servers", values, name)

    def test_paper_config_has_no_deviation(self):
        self.assertEqual(isca27_config.deviation(os.path.join(CONFIGS, "hpca.json"), []), [])


if __name__ == "__main__":
    unittest.main()
