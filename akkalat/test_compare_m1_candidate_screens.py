import unittest

import compare_m1_candidate_screens as compare


class CompareM1CandidateScreensTest(unittest.TestCase):
    def test_ratio_requires_identical_nonempty_wg_hash_and_count(self):
        rows = [
            {
                "label": "old", "benchmark": "fwt", "success": 1,
                "wg_set_sha256": "same", "wg_observed_count": 78600,
                "driver_time_us": 10.0,
            },
            {
                "label": "new", "benchmark": "fwt", "success": 1,
                "wg_set_sha256": "same", "wg_observed_count": 78600,
                "driver_time_us": 8.0,
            },
        ]
        compare.add_reference_ratios(rows, "old")
        self.assertEqual(rows[1]["formal_time_comparison"], 1)
        self.assertEqual(rows[1]["time_ratio_vs_reference"], 1.25)
        rows[1]["wg_set_sha256"] = "different"
        compare.add_reference_ratios(rows, "old")
        self.assertEqual(rows[1]["formal_time_comparison"], 0)
        self.assertEqual(rows[1]["time_ratio_vs_reference"], "")

    def test_candidate_spec_requires_label(self):
        with self.assertRaises(Exception):
            compare.parse_spec("missing-label-separator")


if __name__ == "__main__":
    unittest.main()
