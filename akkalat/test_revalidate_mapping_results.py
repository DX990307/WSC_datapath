import json
import tempfile
import unittest
from pathlib import Path

from revalidate_mapping_results import revalidate


class MappingResultRevalidationTest(unittest.TestCase):
    def make_cell(self, root: Path, simulator_returncode: int = 0) -> Path:
        stem = root / "baseline_matrixtranspose_complete"
        metric_stem = Path(str(stem) + "_metrics")
        command = [
            "/tmp/frozen",
            "-benchmark=matrixtranspose",
            "-max-wg=10",
            f"-metric-file-name={metric_stem}",
        ]
        (root / "EXPERIMENT_METADATA.json").write_text(json.dumps({
            "experiments": [{
                "target": "baseline",
                "benchmark": "matrixtranspose",
                "configuration": "complete",
                "command": command,
            }],
        }), encoding="utf-8")
        Path(str(metric_stem) + ".csv").write_text("metrics", encoding="utf-8")
        Path(str(metric_stem) + "_wg_mapping.json").write_text(json.dumps({
            "stop_reason": "runner_map_wg_observed_limit",
            "max_wg": 10,
            "requested_total_wg": 4,
            "observed_wg_count": 4,
            "stop_time_ns": 0,
            "max_wg_specific_wg_filter": False,
            "global_wg_set_sha256": "abc",
            "per_gpu": [{"gpu_id": 1, "observed_wg_count": 4}],
            "launches": [{
                "requested_total_wg": 4,
                "unified": True,
                "partitions": [{"gpu_id": 1, "begin": 0, "end": 4}],
                "wg_filter_kind": "original_unified_partition",
            }],
        }), encoding="utf-8")
        Path(str(stem) + "_out.stdout").write_text(
            f"Return code: {simulator_returncode}\n", encoding="utf-8"
        )
        result = Path(str(stem) + "_result.json")
        result.write_text(json.dumps({
            "returncode": -2,
            "simulator_returncode": simulator_returncode,
            "success": False,
            "mapping_error": "old auditor required observed == max-wg",
        }), encoding="utf-8")
        return result

    def test_repairs_only_mapping_rejection_after_simulator_success(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            result_path = self.make_cell(root)
            repaired = revalidate(root, {"matrixtranspose"}, {"complete"})
            self.assertEqual(repaired, [result_path])
            result = json.loads(result_path.read_text(encoding="utf-8"))
            self.assertTrue(result["success"])
            self.assertEqual(result["returncode"], 0)
            self.assertEqual(
                result["wg_mapping"]["stop_reason"],
                "workload_completed_before_max_wg",
            )

    def test_does_not_repair_simulator_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            result_path = self.make_cell(root, simulator_returncode=1)
            self.assertEqual(
                revalidate(root, {"matrixtranspose"}, {"complete"}), []
            )
            result = json.loads(result_path.read_text(encoding="utf-8"))
            self.assertFalse(result["success"])


if __name__ == "__main__":
    unittest.main()
