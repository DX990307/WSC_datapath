import unittest
from pathlib import Path
from types import SimpleNamespace

import run_cupath_sensitivity as sensitivity


def make_args(**overrides):
    values = {
        "benchmarks": "traditional",
        "dimensions": "filter,l2,row",
        "include_reference": False,
        "max_wg": 78600,
        "timeout_minutes": 0.0,
    }
    values.update(overrides)
    return SimpleNamespace(**values)


class CuPathSensitivityTest(unittest.TestCase):
    def test_filter_points_use_total_gpm_bucket_counts(self):
        expected_slots = {
            "filter-4k-buckets": 4096,
            "filter-8k-buckets": 8192,
            "filter-32k-buckets": 32768,
        }
        for point in sensitivity.POINTS:
            if point.dimension != "filter-size":
                continue
            self.assertEqual(
                point.filter_slots_per_partition,
                expected_slots[point.name],
            )
            self.assertEqual(
                point.filter_buckets_per_partition * 4,
                point.filter_buckets_per_gpm,
            )

    def test_all_points_run_baseline_and_complete(self):
        points = sensitivity.selected_points(make_args())
        experiments = sensitivity.build_experiments(
            make_args(), points, Path("/tmp/cupath-binary")
        )
        expected = {
            sensitivity.config_name(point, mechanism)
            for point in points
            for mechanism in sensitivity.MECHANISMS
        }
        observed = {experiment["config_name"] for experiment in experiments}
        self.assertEqual(observed, expected)
        self.assertEqual(
            len(experiments),
            14 * len(points) * len(sensitivity.MECHANISMS),
        )

    def test_points_are_round_robin_by_dimension(self):
        points = sensitivity.selected_points(make_args())
        self.assertEqual(
            [point.dimension for point in points[:6]],
            [
                "filter-size",
                "l2-size",
                "row-locality",
                "filter-size",
                "l2-size",
                "row-locality",
            ],
        )

    def test_global_schedule_interleaves_benchmarks_and_variants(self):
        points = sensitivity.selected_points(make_args())
        experiments = sensitivity.build_experiments(
            make_args(benchmarks="aes,fft,fir"),
            points,
            Path("/tmp/cupath-binary"),
        )
        first_wave = experiments[:3]
        self.assertEqual(
            [experiment["benchmark"] for experiment in first_wave],
            ["aes", "fft", "fir"],
        )
        self.assertEqual(
            len({experiment["config_name"] for experiment in first_wave}),
            3,
        )

    def test_each_cell_keeps_requested_wg_and_sensitivity_flags(self):
        point = sensitivity.POINTS[0]
        experiments = sensitivity.build_experiments(
            make_args(benchmarks="aes"),
            [point],
            Path("/tmp/cupath-binary"),
        )
        self.assertEqual(len(experiments), 2)
        for experiment in experiments:
            self.assertIn("-max-wg=78600", experiment["common_flags"])
            self.assertIn("-typed-filter-capacity=4096", experiment["flags"])
            self.assertIn("-l2-cache-size-mb=4", experiment["flags"])
            self.assertIn(
                "-l2-adaptive-pair-region-lines=2", experiment["flags"]
            )


if __name__ == "__main__":
    unittest.main()
