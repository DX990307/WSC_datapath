import csv
import json
import tempfile
import unittest
from pathlib import Path

from analyze_fir_remote_origin import analyze


class FIRRemoteOriginAnalysisTest(unittest.TestCase):
    def test_aggregates_objects_pairs_and_per_gpu_ratios(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            mapping = {
                "max_wg": 2,
                "requested_total_wg": 524288,
                "observed_wg_count": 2,
                "stop_reason": "runner_map_wg_observed_limit",
                "global_wg_set_sha256": "abc",
                "stop_time_ns": 7,
                "any_wg_filter": True,
                "max_wg_specific_wg_filter": False,
                "launches": [{
                    "requested_total_wg": 524288,
                    "unified": True,
                    "packet_addresses": [1],
                    "partitions": [
                        {"gpu_id": 1, "begin": 0, "end": 524288},
                    ],
                    "wg_filter_kind": "original_unified_partition",
                }],
                "per_gpu": [{
                    "gpu_id": 1,
                    "observed_wg_count": 2,
                    "flattened_wg_id_min": 0,
                    "flattened_wg_id_max": 1,
                    "wg_set_sha256": "abc",
                }],
            }
            (root / "baseline_fir_baseline_metrics_wg_mapping.json").write_text(
                json.dumps(mapping), encoding="utf-8"
            )
            (root / "baseline_fir_baseline_out.stdout").write_text(
                "total WG: 524288\n", encoding="utf-8"
            )
            with (root / "baseline_fir_baseline_metrics.csv").open(
                "w", newline="", encoding="utf-8"
            ) as stream:
                writer = csv.writer(stream)
                writer.writerow(["", "where", "what", "value"])
                writer.writerow([0, "GPU[1].RDMA", "outgoing_trans_count", 1])
            with (root / "baseline_fir_baseline_remote_origin_summary.csv").open(
                "w", newline="", encoding="utf-8"
            ) as stream:
                writer = csv.writer(stream)
                writer.writerow([
                    "requester_gpu", "owner_gpu", "page_owner_gpu",
                    "is_remote", "object", "operation", "requests", "bytes",
                ])
                writer.writerow([1, 1, 1, "false", "input", "read", 3, 192])
                writer.writerow([1, 2, 2, "true", "history", "read", 1, 64])
                writer.writerow([1, 1, 1, "false", "unclassified", "read", 7, 56])
            with (root / "baseline_fir_baseline_remote_origin_raw.csv").open(
                "w", newline="", encoding="utf-8"
            ) as stream:
                writer = csv.DictWriter(stream, fieldnames=[
                    "sequence", "request_id", "operation", "bytes",
                    "requester_gpu", "owner_gpu", "page_owner_gpu",
                    "is_remote", "flattened_wg_id", "wg_x", "wg_y", "wg_z",
                    "pid", "vaddr", "paddr", "page_paddr", "object",
                    "requester_component", "owner_component",
                ])
                writer.writeheader()
                writer.writerow({
                    "sequence": 1, "request_id": "r", "operation": "read",
                    "bytes": 64, "requester_gpu": 1, "owner_gpu": 2,
                    "page_owner_gpu": 2, "is_remote": "true",
                    "flattened_wg_id": 9, "wg_x": 9, "wg_y": 0, "wg_z": 0,
                    "pid": 1, "vaddr": 64, "paddr": 128,
                    "page_paddr": 0, "object": "history",
                    "requester_component": "GPU[1].L1VTLB",
                    "owner_component": "GPU[2].PageOwner",
                })

            _, objects, matrix, per_gpu, first = analyze(root, ("baseline",))
            input_row = next(row for row in objects if row["object"] == "input" and row["operation"] == "read")
            self.assertEqual(input_row["local_requests"], 3)
            self.assertTrue(any(row["is_remote"] for row in matrix))
            self.assertEqual(per_gpu[0]["remote_fraction"], 0.25)
            self.assertEqual(per_gpu[0]["auxiliary_unclassified_requests"], 7)
            self.assertEqual(first[0]["flattened_wg_id"], "9")
            self.assertEqual(first[0]["object"], "history")


if __name__ == "__main__":
    unittest.main()
