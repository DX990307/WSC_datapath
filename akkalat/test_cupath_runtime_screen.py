import hashlib
import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from analyze_cupath_runtime_screen import (
    BENCHMARKS,
    MECHANISMS,
    MECHANISM_FIELDS,
    REQUIRED_FLAGS,
    analyze,
    audit_protocol,
)
from plot_cupath_typed_ablation import (
    CONFIGS,
    REQUIRED_BASELINE_METRICS,
    REQUIRED_COMPLETE_METRICS,
    REQUIRED_EXECUTION_METRICS,
    REQUIRED_M1_METRICS,
    REQUIRED_M2_METRICS,
    REQUIRED_M3_METRICS,
)


class ControlledRuntimeProtocolTest(unittest.TestCase):
    def make_protocol(self, root: Path, workers: int = 1):
        binary = root / "frozen"
        binary.write_bytes(b"frozen runtime binary")
        experiments = []
        flags = sorted(REQUIRED_FLAGS)
        for benchmark in BENCHMARKS:
            for config, _, _ in CONFIGS:
                mechanism_flags = [
                    f"-{field}={str(value).lower()}"
                    for field, value in zip(
                        MECHANISM_FIELDS, MECHANISMS[config]
                    )
                ]
                experiments.append({
                    "benchmark": benchmark,
                    "configuration": config,
                    "target": "baseline",
                    "command": [
                        str(binary), f"-benchmark={benchmark}", *flags,
                        *mechanism_flags,
                        (
                            "-metric-file-name="
                            f"{root / f'baseline_{benchmark}_{config}_metrics'}"
                        ),
                    ],
                })
        (root / "EXPERIMENT_METADATA.json").write_text(
            json.dumps({
                "experiment_count": 35,
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

    def test_single_worker_fixed_protocol_is_accepted(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            binary, digest = self.make_protocol(root)
            self.assertEqual(audit_protocol(root), (str(binary), digest))

    def test_parallel_screen_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root, workers=2)
            with self.assertRaisesRegex(SystemExit, "worker mismatch"):
                audit_protocol(root)

    def test_declared_parallel_screen_is_accepted(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            binary, digest = self.make_protocol(root, workers=7)
            self.assertEqual(
                audit_protocol(
                    root, expected_workers=7, expected_sha256=digest
                ),
                (str(binary), digest),
            )

    def test_wrong_expected_binary_hash_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            with self.assertRaisesRegex(SystemExit, "unexpected frozen binary"):
                audit_protocol(root, expected_sha256="0" * 64)

    def test_wrong_mechanism_matrix_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            experiment = next(
                exp for exp in metadata["experiments"]
                if exp["configuration"] == "m3"
            )
            experiment["command"].remove("-remote-data-path-l2-enable=true")
            experiment["command"].append(
                "-remote-data-path-l2-enable=false"
            )
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "mechanism flags"):
                audit_protocol(root)

    def test_changed_filter_configuration_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            experiment = metadata["experiments"][0]
            experiment["command"].remove(
                "-typed-filter-fingerprint-bits=13"
            )
            experiment["command"].append(
                "-typed-filter-fingerprint-bits=16"
            )
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "missing flags"):
                audit_protocol(root)

    def test_unexpected_benchmark_specific_flag_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["experiments"][0]["command"].append(
                "-aes-special-case=1"
            )
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "unexpected flags"):
                audit_protocol(root)

    def test_duplicate_metadata_cell_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_protocol(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["experiments"].append(metadata["experiments"][0])
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "36 commands"):
                audit_protocol(root)

    def make_campaign(self, root: Path):
        required = {
            "baseline": REQUIRED_BASELINE_METRICS,
            "m1": REQUIRED_M1_METRICS,
            "m2": REQUIRED_M2_METRICS,
            "m3": REQUIRED_M3_METRICS,
            "complete": REQUIRED_COMPLETE_METRICS,
        }
        campaign = {}
        paths = {}
        for benchmark in BENCHMARKS:
            for config, _, _ in CONFIGS:
                metrics = {
                    key: 0.0
                    for key in REQUIRED_EXECUTION_METRICS | required[config]
                }
                metrics.update({
                    "__driver_total_time": (
                        50.0 if config == "complete" else 100.0
                    ),
                    "total_wg_count": 76800.0,
                    "max_wg_limit": 76800.0,
                    "max_wg_observed": 76800.0,
                    "max_wg_runtime_stopper": 1.0,
                    "max_wg_reached": 1.0,
                    "wg_requested_total": 100000.0,
                    "wg_max_wg_specific_filter": 0.0,
                    "allocation_page_size": 4096.0,
                    "allocation_workload_allocated_pages": 100.0,
                    "allocation_overall_allocated_pages": 200.0,
                })
                key = (benchmark, config)
                campaign[key] = metrics
                metrics_path = root / f"baseline_{benchmark}_{config}_metrics.csv"
                metrics_path.touch()
                paths[key] = metrics_path
                mapping_path = metrics_path.with_name(
                    metrics_path.name.removesuffix(".csv")
                    + "_wg_mapping.json"
                ).resolve()
                mapping_path.write_text("{}", encoding="utf-8")
                result_path = root / f"baseline_{benchmark}_{config}_result.json"
                result_path.write_text(
                    json.dumps({
                        "target": "baseline",
                        "benchmark": benchmark,
                        "configuration": config,
                        "metrics": str(metrics_path.resolve()),
                        "wg_mapping": {"path": str(mapping_path)},
                        "success": True,
                        "returncode": 0,
                        "simulator_returncode": 0,
                    }),
                    encoding="utf-8",
                )
        return campaign, paths

    def test_analysis_accepts_complete_consistent_screen(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            campaign, paths = self.make_campaign(root)
            with (
                patch(
                    "analyze_cupath_runtime_screen.load_campaign",
                    return_value=(campaign, paths),
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_return_code",
                    return_value=0,
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_elapsed_seconds",
                    return_value=10.0,
                ),
            ):
                rows, aggregate = analyze(root)
            self.assertEqual(len(rows), 35)
            self.assertEqual(aggregate["complete_simulated_speedup"], 2.0)
            self.assertEqual(rows[0]["workload_allocated_pages"], 100.0)

    def test_analysis_accepts_natural_completion_below_max_wg(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            campaign, paths = self.make_campaign(root)
            for config, _, _ in CONFIGS:
                metrics = campaign[("matrixtranspose", config)]
                metrics["total_wg_count"] = 32400.0
                metrics["max_wg_observed"] = 32400.0
                metrics["max_wg_reached"] = 0.0
                metrics["wg_requested_total"] = 32400.0
            with (
                patch(
                    "analyze_cupath_runtime_screen.load_campaign",
                    return_value=(campaign, paths),
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_return_code",
                    return_value=0,
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_elapsed_seconds",
                    return_value=10.0,
                ),
            ):
                rows, _ = analyze(root)
            mt_rows = [row for row in rows if row["benchmark"] == "matrixtranspose"]
            self.assertEqual(len(mt_rows), 5)
            self.assertTrue(all(row["observed_wg"] == 32400 for row in mt_rows))

    def test_analysis_rejects_misdirected_result_artifact(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            campaign, paths = self.make_campaign(root)
            result_path = root / f"baseline_{BENCHMARKS[0]}_baseline_result.json"
            result = json.loads(result_path.read_text(encoding="utf-8"))
            result["metrics"] = str((root / "wrong.csv").resolve())
            result_path.write_text(json.dumps(result), encoding="utf-8")
            with (
                patch(
                    "analyze_cupath_runtime_screen.load_campaign",
                    return_value=(campaign, paths),
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_return_code",
                    return_value=0,
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_elapsed_seconds",
                    return_value=10.0,
                ),
                self.assertRaisesRegex(SystemExit, "metric artifact mismatch"),
            ):
                analyze(root)

    def test_analysis_rejects_configuration_dependent_allocation(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            campaign, paths = self.make_campaign(root)
            campaign[(BENCHMARKS[0], "m2")][
                "allocation_workload_allocated_pages"
            ] = 101.0
            with (
                patch(
                    "analyze_cupath_runtime_screen.load_campaign",
                    return_value=(campaign, paths),
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_return_code",
                    return_value=0,
                ),
                patch(
                    "analyze_cupath_runtime_screen.read_elapsed_seconds",
                    return_value=10.0,
                ),
                self.assertRaisesRegex(
                    SystemExit, "configuration-dependent allocation"
                ),
            ):
                analyze(root)


if __name__ == "__main__":
    unittest.main()
