import csv
import json
import tempfile
import unittest
from pathlib import Path

import find_reusable_baselines as reuse


class BaselineReuseTest(unittest.TestCase):
    def test_normalized_config_ignores_binary_benchmark_and_metric_only(self):
        first = reuse.normalized_config([
            "/tmp/binary-a", "-benchmark=aes", "-timing",
            "-l1v-mshr-entries=16", "-metric-file-name=/tmp/a",
        ])
        second = reuse.normalized_config([
            "/tmp/binary-b", "-benchmark=fft", "-timing",
            "-l1v-mshr-entries=16", "-metric-file-name=/tmp/b",
        ])
        self.assertEqual(first, second)
        self.assertNotEqual(
            first,
            reuse.normalized_config([
                "/tmp/binary-c", "-benchmark=fft", "-timing",
                "-l1v-mshr-entries=160", "-metric-file-name=/tmp/c",
            ]),
        )

    def test_mapping_requires_the_exact_78600_protocol(self):
        mapping = {
            "max_wg": 78600,
            "observed_wg_count": 78600,
            "requested_total_wg": 100000,
            "stop_reason": "runner_map_wg_observed_limit",
        }
        self.assertTrue(reuse.valid_bounded_mapping(mapping))
        mapping["max_wg"] = 76800
        self.assertFalse(reuse.valid_bounded_mapping(mapping))

    def test_discovery_reuses_a_different_binary_with_same_config(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            campaign = root / "campaign"
            campaign.mkdir()
            command = [
                "/tmp/old-binary", "-benchmark=aes", "-timing",
                "-l1v-mshr-entries=16", "-max-wg=78600",
                "-metric-file-name=metrics.csv",
            ]
            (campaign / "EXPERIMENT_METADATA.json").write_text(json.dumps({
                "experiments": [{
                    "target": "baseline", "benchmark": "aes",
                    "configuration": "baseline", "command": command,
                }],
            }), encoding="utf-8")
            metrics = campaign / "metrics.csv"
            with metrics.open("w", newline="", encoding="utf-8") as stream:
                writer = csv.writer(stream)
                writer.writerow(["", "where", "what", "value"])
                writer.writerow([0, "Driver", "total_time", 1.25])
            (campaign / "cell_result.json").write_text(json.dumps({
                "target": "baseline", "benchmark": "aes",
                "configuration": "baseline", "success": True,
                "metrics": "metrics.csv",
                "wg_mapping": {
                    "max_wg": 78600, "observed_wg_count": 78600,
                    "completed_wg_count": 78600,
                    "requested_total_wg": 100000,
                    "stop_reason": "runner_map_wg_observed_limit",
                    "global_wg_set_sha256": "same-work",
                },
            }), encoding="utf-8")

            reference = reuse.normalized_config([
                "/tmp/new-binary", "-benchmark=fft", "-timing",
                "-l1v-mshr-entries=16", "-max-wg=78600",
                "-metric-file-name=other.csv",
            ])
            rows = reuse.discover(root, reference)
            self.assertEqual(len(rows), 1)
            self.assertEqual(rows[0]["benchmark"], "aes")
            self.assertEqual(rows[0]["driver_total_time"], 1.25)


if __name__ == "__main__":
    unittest.main()
