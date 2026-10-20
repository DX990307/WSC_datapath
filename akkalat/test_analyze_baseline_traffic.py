import hashlib
import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from analyze_baseline_traffic import (
    audit_completed_result,
    audit_profile_protocol,
    traffic_class,
)
from analyze_cupath_runtime_screen import (
    MECHANISM_FIELDS,
    MECHANISMS,
    REQUIRED_FLAGS,
)
from plot_cupath_typed_ablation import WORKLOADS


class BaselineTrafficClassificationTest(unittest.TestCase):
    def test_thresholds_are_fixed_and_boundary_inclusive(self):
        self.assertEqual(traffic_class(0.0), "Exact-local")
        self.assertEqual(traffic_class(0.00001), "Local-dominant")
        self.assertEqual(traffic_class(0.25), "Local-dominant")
        self.assertEqual(traffic_class(0.25001), "Mixed")
        self.assertEqual(traffic_class(0.75), "Mixed")
        self.assertEqual(traffic_class(0.75001), "Remote-dominant")

    def make_protocol(self, root: Path, workers: int = 14):
        binary = root / "frozen"
        binary.write_bytes(b"frozen provenance binary")
        mechanisms = [
            f"-{field}={str(value).lower()}"
            for field, value in zip(
                MECHANISM_FIELDS, MECHANISMS["baseline"]
            )
        ]
        experiments = []
        for benchmark, _, _ in WORKLOADS:
            prefix = (root / f"baseline_{benchmark}_baseline_remote_origin").resolve()
            experiments.append({
                "benchmark": benchmark,
                "configuration": "baseline",
                "target": "baseline",
                "command": [
                    str(binary),
                    f"-benchmark={benchmark}",
                    *sorted(REQUIRED_FLAGS),
                    *mechanisms,
                    f"-metric-file-name={root / f'baseline_{benchmark}_baseline_metrics'}",
                    "-trace-remote-origin",
                    f"-trace-remote-origin-file={prefix}",
                    "-trace-remote-origin-max-records=100000",
                ],
            })
        (root / "EXPERIMENT_METADATA.json").write_text(
            json.dumps({
                "experiment_count": len(experiments),
                "launcher": {"max_workers": workers},
                "experiments": experiments,
            }),
            encoding="utf-8",
        )
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        (root / "EXPERIMENT_BINARIES.json").write_text(
            json.dumps({"sha256_by_target": {"baseline": digest}}),
            encoding="utf-8",
        )
        return binary, digest

    def test_frozen_baseline_profile_protocol_is_accepted(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            binary, digest = self.make_protocol(root)
            self.assertEqual(
                audit_profile_protocol(root, expected_sha256=digest),
                (str(binary), digest),
            )

    def test_missing_remote_origin_trace_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["experiments"][0]["command"].remove(
                "-trace-remote-origin"
            )
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "missing profile flags"):
                audit_profile_protocol(root)

    def test_profile_worker_count_is_audited(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root, workers=7)
            with self.assertRaisesRegex(SystemExit, "worker mismatch"):
                audit_profile_protocol(root)

    def test_profile_memory_reserve_is_audited(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root, workers=9)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["launcher"].update({
                "memory_reserve_gib": 50.0,
                "memory_per_worker_gib": 21.5,
                "mem_available_gib_at_launch": 247.0,
                "memory_worker_cap": 9,
            })
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            audit_profile_protocol(
                root,
                expected_workers=9,
                expected_memory_reserve_gib=50.0,
                expected_memory_per_worker_gib=21.5,
            )
            metadata["launcher"]["memory_worker_cap"] = 8
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "exceeds recorded memory cap"):
                audit_profile_protocol(
                    root,
                    expected_workers=9,
                    expected_memory_reserve_gib=50.0,
                    expected_memory_per_worker_gib=21.5,
                )

    def test_unexpected_profile_flag_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["experiments"][0]["command"].append(
                "-force-local-data-access"
            )
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "unexpected profile flags"):
                audit_profile_protocol(root)

    def test_duplicate_profile_flag_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["experiments"][0]["command"].append("-sampled")
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "duplicate flags"):
                audit_profile_protocol(root)

    def test_completed_result_requires_both_zero_return_codes(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            metrics = (root / "baseline_aes_baseline_metrics.csv").resolve()
            mapping = (
                root / "baseline_aes_baseline_metrics_wg_mapping.json"
            ).resolve()
            metrics.touch()
            mapping.write_text("{}", encoding="utf-8")
            result_path = root / "baseline_aes_baseline_result.json"
            result = {
                "target": "baseline",
                "benchmark": "aes",
                "configuration": "baseline",
                "metrics": str(metrics),
                "wg_mapping": {"path": str(mapping)},
                "success": True,
                "returncode": 0,
                "simulator_returncode": 0,
            }
            result_path.write_text(json.dumps(result), encoding="utf-8")
            audit_completed_result(root, "aes")

            result["simulator_returncode"] = 1
            result_path.write_text(json.dumps(result), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "failed Baseline"):
                audit_completed_result(root, "aes")

    def test_completed_result_rejects_misdirected_metric_artifact(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            result_path = root / "baseline_aes_baseline_result.json"
            result_path.write_text(json.dumps({
                "target": "baseline",
                "benchmark": "aes",
                "configuration": "baseline",
                "metrics": str((root / "wrong.csv").resolve()),
                "wg_mapping": {
                    "path": str(
                        (root / "baseline_aes_baseline_metrics_wg_mapping.json").resolve()
                    )
                },
                "success": True,
                "returncode": 0,
                "simulator_returncode": 0,
            }), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "metric artifact mismatch"):
                audit_completed_result(root, "aes")


if __name__ == "__main__":
    unittest.main()
