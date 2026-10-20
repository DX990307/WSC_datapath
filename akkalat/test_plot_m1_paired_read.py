import unittest

import plot_m1_paired_read


class M1PairedReadPlotTest(unittest.TestCase):
    def test_terminal_sibling_composition_includes_resident_unused(self):
        row = {
            "granularity_sibling_fills": "10",
            "granularity_timely_sibling_lines": "4",
            "granularity_late_sibling_lines": "2",
            "granularity_terminal_unused_sibling_lines": "4",
        }
        self.assertEqual(
            plot_m1_paired_read.sibling_composition(row),
            (40.0, 20.0, 40.0),
        )

    def test_filter_contribution_separates_metadata_and_traffic(self):
        filtered = {
            "filter_exact_lookup_total": "20",
            "granularity_terminal_wasted_sibling_bytes": "640",
            "dram_physical_read_bytes": "6400",
        }
        unfiltered = {
            "filter_exact_lookup_total": "100",
            "granularity_terminal_wasted_sibling_bytes": "1280",
            "dram_physical_read_bytes": "6400",
        }
        self.assertEqual(
            plot_m1_paired_read.filter_contribution(filtered, unfiltered),
            (80.0, 50.0, 0.0),
        )


if __name__ == "__main__":
    unittest.main()
