import json
import tempfile
import unittest
from pathlib import Path

from analyze_wg_mapping import audit


class WGMappingAuditTest(unittest.TestCase):
    def test_full_workload_natural_completion_is_supported(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            report = {
                "max_wg": 0,
                "requested_total_wg": 4,
                "observed_wg_count": 4,
                "completed_wg_count": 4,
                "executed_kernel_count": 1,
                "observed_sampling_coverage": 1.0,
                "completed_sampling_coverage": 1.0,
                "stop_reason": "natural_completion",
                "stop_time_ns": 0,
                "any_wg_filter": True,
                "max_wg_specific_wg_filter": False,
                "global_wg_set_sha256": "same",
                "launches": [{
                    "requested_total_wg": 4,
                    "unified": True,
                    "partitions": [
                        {"gpu_id": 1, "begin": 0, "end": 4},
                    ],
                    "wg_filter_kind": "original_unified_partition",
                }],
                "per_gpu": [{
                    "gpu_id": 1,
                    "observed_wg_count": 4,
                    "flattened_wg_id_min": 0,
                    "flattened_wg_id_max": 3,
                    "wg_set_sha256": "gpu-same",
                }],
            }
            for config in ("baseline", "m1"):
                prefix = root / f"baseline_aes_{config}"
                Path(str(prefix) + "_metrics_wg_mapping.json").write_text(
                    json.dumps(report), encoding="utf-8"
                )
                Path(str(prefix) + "_out.stdout").write_text(
                    "complete\n", encoding="utf-8"
                )

            rows = audit(root, ["aes"], ("baseline", "m1"))
            self.assertEqual(len(rows), 2)
            self.assertTrue(all(
                row["stop_reason"] == "natural_completion" for row in rows
            ))
            self.assertTrue(all(
                row["completed_sampling_coverage"] == 1.0 for row in rows
            ))
            self.assertTrue(all(
                row["strict_identity_matches_baseline"] for row in rows
            ))

    def test_reports_natural_set_difference_without_rejecting_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for config, wg_hash, gpu in (
                ("baseline", "aaa", 1),
                ("m2", "bbb", 2),
            ):
                report = {
                    "max_wg": 2,
                    "requested_total_wg": 8,
                    "observed_wg_count": 2,
                    "stop_reason": "runner_map_wg_observed_limit",
                    "stop_time_ns": 10,
                    "any_wg_filter": True,
                    "max_wg_specific_wg_filter": False,
                    "global_wg_set_sha256": wg_hash,
                    "launches": [{
                        "requested_total_wg": 8,
                        "unified": True,
                        "partitions": [
                            {"gpu_id": 1, "begin": 0, "end": 4},
                            {"gpu_id": 2, "begin": 4, "end": 8},
                        ],
                        "wg_filter_kind": "original_unified_partition",
                    }],
                    "per_gpu": [{
                        "gpu_id": gpu,
                        "observed_wg_count": 2,
                        "flattened_wg_id_min": 0,
                        "flattened_wg_id_max": 1,
                        "wg_set_sha256": wg_hash,
                    }],
                }
                prefix = root / f"baseline_fir_{config}"
                Path(str(prefix) + "_metrics_wg_mapping.json").write_text(
                    json.dumps(report), encoding="utf-8"
                )
                Path(str(prefix) + "_out.stdout").write_text(
                    "total WG: 8 WG Per CU 1\n"
                    "[Runner] reached max-wg=2 observed_wg=2\n",
                    encoding="utf-8",
                )

            rows = audit(root, ["fir"], ("baseline", "m2"))
            self.assertEqual(len(rows), 2)
            self.assertTrue(rows[1]["partition_matches_baseline"])
            self.assertFalse(rows[1]["wg_set_matches_baseline"])
            self.assertFalse(rows[1]["per_gpu_matches_baseline"])

            with self.assertRaisesRegex(
                ValueError, "formal workload identity differs"
            ):
                audit(
                    root, ["fir"], ("baseline", "m2"),
                    require_baseline_match=True,
                )

    def test_formal_identity_checks_launch_descriptor(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for config, packet_address in (("baseline", 100), ("m1", 200)):
                report = {
                    "max_wg": 0,
                    "requested_total_wg": 4,
                    "observed_wg_count": 4,
                    "completed_wg_count": 4,
                    "executed_kernel_count": 1,
                    "observed_sampling_coverage": 1.0,
                    "completed_sampling_coverage": 1.0,
                    "stop_reason": "natural_completion",
                    "stop_time_ns": 0,
                    "any_wg_filter": False,
                    "max_wg_specific_wg_filter": False,
                    "global_wg_set_sha256": "same",
                    "launches": [{
                        "requested_total_wg": 4,
                        "unified": False,
                        "packet_addresses": [packet_address],
                        "partitions": [
                            {"gpu_id": 1, "begin": 0, "end": 4},
                        ],
                        "wg_filter_kind": "none",
                    }],
                    "per_gpu": [{
                        "gpu_id": 1,
                        "observed_wg_count": 4,
                        "flattened_wg_id_min": 0,
                        "flattened_wg_id_max": 3,
                        "wg_set_sha256": "gpu-same",
                    }],
                }
                prefix = root / f"baseline_aes_{config}"
                Path(str(prefix) + "_metrics_wg_mapping.json").write_text(
                    json.dumps(report), encoding="utf-8"
                )
                Path(str(prefix) + "_out.stdout").write_text(
                    "complete\n", encoding="utf-8"
                )

            rows = audit(root, ["aes"], ("baseline", "m1"))
            self.assertFalse(rows[1]["launch_matches_baseline"])
            with self.assertRaisesRegex(
                ValueError, "formal workload identity differs"
            ):
                audit(
                    root, ["aes"], ("baseline", "m1"),
                    require_baseline_match=True,
                )

    def test_accepts_natural_completion_below_max_wg(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            report = {
                "max_wg": 10,
                "requested_total_wg": 4,
                "observed_wg_count": 4,
                "stop_reason": "runner_map_wg_observed_limit",
                "stop_time_ns": 0,
                "any_wg_filter": True,
                "max_wg_specific_wg_filter": False,
                "global_wg_set_sha256": "aaa",
                "launches": [{
                    "requested_total_wg": 4,
                    "unified": True,
                    "partitions": [
                        {"gpu_id": 1, "begin": 0, "end": 4},
                    ],
                    "wg_filter_kind": "original_unified_partition",
                }],
                "per_gpu": [{
                    "gpu_id": 1,
                    "observed_wg_count": 4,
                    "flattened_wg_id_min": 0,
                    "flattened_wg_id_max": 3,
                    "wg_set_sha256": "aaa",
                }],
            }
            prefix = root / "baseline_matrixtranspose_complete"
            Path(str(prefix) + "_metrics_wg_mapping.json").write_text(
                json.dumps(report), encoding="utf-8"
            )
            Path(str(prefix) + "_out.stdout").write_text(
                "total WG: 4 WG Per CU 1\n", encoding="utf-8"
            )

            rows = audit(root, ["matrixtranspose"], ("complete",))
            self.assertEqual(
                rows[0]["stop_reason"],
                "workload_completed_before_max_wg",
            )


if __name__ == "__main__":
    unittest.main()
