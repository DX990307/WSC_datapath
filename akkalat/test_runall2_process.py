import io
import json
import tempfile
import unittest
from contextlib import redirect_stdout
from datetime import timedelta
from pathlib import Path

import runall2_process


class CellRuntimeResultTest(unittest.TestCase):
    def test_failed_cell_preserves_host_elapsed_seconds(self):
        exp = {"timeout_seconds": 0}
        with redirect_stdout(io.StringIO()):
            result = runall2_process.exp_result(
                exp,
                "simulator command",
                "/missing/metrics",
                timedelta(seconds=12, microseconds=500000),
                False,
                3,
            )
        self.assertFalse(result["success"])
        self.assertEqual(result["returncode"], 3)
        self.assertEqual(result["host_elapsed_seconds"], 12.5)


class BinaryManifestTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name) / "root"
        self.results = Path(self.temp.name) / "results"
        self.binary = self.root / "baseline" / "baseline"
        self.binary.parent.mkdir(parents=True)
        self.results.mkdir()
        self.binary.write_bytes(b"frozen binary")
        self.exps = [{"target": "baseline"}]

    def tearDown(self):
        self.temp.cleanup()

    def test_writes_and_verifies_same_binary(self):
        path = runall2_process.verify_or_write_binary_manifest(
            self.exps,
            results_dir=str(self.results),
            root_dir=str(self.root),
        )
        recorded = json.loads(Path(path).read_text())
        self.assertEqual(recorded["version"], 1)
        self.assertEqual(
            set(recorded["sha256_by_target"]), {"baseline"}
        )
        runall2_process.verify_or_write_binary_manifest(
            self.exps,
            require_existing=True,
            results_dir=str(self.results),
            root_dir=str(self.root),
        )

    def test_resume_rejects_changed_binary(self):
        runall2_process.verify_or_write_binary_manifest(
            self.exps,
            results_dir=str(self.results),
            root_dir=str(self.root),
        )
        self.binary.write_bytes(b"different binary")
        with self.assertRaisesRegex(RuntimeError, "hash mismatch"):
            runall2_process.verify_or_write_binary_manifest(
                self.exps,
                require_existing=True,
                results_dir=str(self.results),
                root_dir=str(self.root),
            )

    def test_resume_rejects_missing_manifest(self):
        with self.assertRaisesRegex(RuntimeError, "missing EXPERIMENT_BINARIES"):
            runall2_process.verify_or_write_binary_manifest(
                self.exps,
                require_existing=True,
                results_dir=str(self.results),
                root_dir=str(self.root),
            )

    def test_new_run_rejects_unverifiable_existing_metrics(self):
        (self.results / "baseline_aes_baseline_metrics.csv").write_text(
            "old metrics"
        )
        with self.assertRaisesRegex(RuntimeError, "existing metrics"):
            runall2_process.verify_or_write_binary_manifest(
                self.exps,
                results_dir=str(self.results),
                root_dir=str(self.root),
            )

    def test_manifest_and_command_use_frozen_binary_override(self):
        frozen = Path(self.temp.name) / "candidate"
        frozen.write_bytes(b"candidate binary")
        frozen.chmod(0o755)
        exps = [{
            "target": "baseline",
            "benchmark": "aes",
            "config_name": "baseline",
            "common_flags": [],
            "flags": [],
            "binary_path": str(frozen),
        }]
        path = runall2_process.verify_or_write_binary_manifest(
            exps,
            results_dir=str(self.results),
            root_dir=str(self.root),
        )
        recorded = json.loads(Path(path).read_text())
        self.assertNotEqual(
            recorded["sha256_by_target"]["baseline"],
            runall2_process.target_binary_hashes(
                self.exps, root_dir=str(self.root)
            )["baseline"],
        )
        runall2_process.set_output_dir(str(self.results))
        self.assertEqual(
            runall2_process.experiment_command(exps[0])[0], str(frozen)
        )

    def test_campaign_binary_snapshot_survives_later_rebuild(self):
        exps = [{
            "target": "baseline",
            "benchmark": "aes",
            "config_name": "baseline",
            "common_flags": [],
            "flags": [],
            "binary_path": str(self.binary),
        }]
        original_digest = runall2_process.target_binary_hashes(exps)[
            "baseline"
        ]
        frozen = runall2_process.freeze_experiment_binaries(
            exps, results_dir=self.results,
        )
        frozen_path = Path(frozen["baseline"])
        self.assertEqual(Path(exps[0]["binary_path"]), frozen_path)
        self.assertTrue(frozen_path.is_file())
        self.binary.write_bytes(b"later rebuild")
        self.assertEqual(
            runall2_process.target_binary_hashes(exps)["baseline"],
            original_digest,
        )
        self.assertNotEqual(
            runall2_process.target_binary_hashes([{
                "target": "baseline", "binary_path": str(self.binary),
            }])["baseline"],
            original_digest,
        )

    def test_resume_metadata_does_not_overwrite_original_campaign(self):
        runall2_process.set_output_dir(str(self.results))
        exp = {
            "target": "baseline",
            "benchmark": "kmeans",
            "config_name": "complete",
            "common_flags": ["-l1v-mshr-entries=16"],
            "flags": ["-typed-filter-mode=cuckoo"],
            "binary_path": str(self.binary),
        }
        primary = Path(runall2_process.write_experiment_metadata(
            [exp], launcher={"max_workers": 1, "timeout_minutes": 60}
        ))
        original = primary.read_bytes()

        rerun = Path(runall2_process.write_experiment_metadata(
            [exp], preserve_existing=True
        ))

        self.assertEqual(primary.name, "EXPERIMENT_METADATA.json")
        self.assertEqual(primary.read_bytes(), original)
        self.assertRegex(
            rerun.name,
            r"^EXPERIMENT_RERUN_METADATA_\d{8}-\d{6}-\d{6}\.json$",
        )
        self.assertNotEqual(rerun, primary)
        self.assertEqual(
            json.loads(primary.read_text())["launcher"]["max_workers"], 1
        )
        self.assertEqual(json.loads(rerun.read_text())["experiment_count"], 1)

    def test_resume_reconstructs_exact_recorded_campaign(self):
        runall2_process.set_output_dir(str(self.results))
        exp = {
            "target": "baseline",
            "benchmark": "spmv",
            "config_name": "m1",
            "common_flags": ["-l1v-mshr-entries=16", "-max-wg=48"],
            "flags": ["-l2-fill-forwarding-enable=true"],
            "binary_path": str(self.binary),
        }
        runall2_process.write_experiment_metadata([exp])

        loaded = runall2_process.load_experiments_from_metadata(
            str(self.results)
        )

        self.assertEqual(len(loaded), 1)
        self.assertEqual(loaded[0]["benchmark"], "spmv")
        self.assertEqual(loaded[0]["config_name"], "m1")
        self.assertEqual(loaded[0]["binary_path"], str(self.binary))
        self.assertIn("-l1v-mshr-entries=16", loaded[0]["common_flags"])
        self.assertIn(
            "-l2-fill-forwarding-enable=true", loaded[0]["common_flags"]
        )
        self.assertFalse(any(
            flag.startswith("-metric-file-name=")
            for flag in loaded[0]["common_flags"]
        ))

    def test_resume_rejects_mismatched_recorded_benchmark(self):
        metadata = {
            "experiments": [{
                "target": "baseline",
                "benchmark": "aes",
                "configuration": "baseline",
                "command": [
                    str(self.binary),
                    "-benchmark=fft",
                    "-metric-file-name=/tmp/metrics",
                ],
            }],
        }
        (self.results / "EXPERIMENT_METADATA.json").write_text(
            json.dumps(metadata)
        )
        with self.assertRaisesRegex(RuntimeError, "benchmark mismatch"):
            runall2_process.load_experiments_from_metadata(str(self.results))

    def test_runtime_max_wg_mapping_is_audited_and_recorded(self):
        runall2_process.set_output_dir(str(self.results))
        exp = {
            "target": "baseline",
            "benchmark": "fir",
            "config_name": "baseline",
            "common_flags": ["-max-wg=2"],
            "flags": [],
        }
        metric_stem = self.results / "baseline_fir_baseline_metrics"
        mapping = {
            "stop_reason": "runner_map_wg_observed_limit",
            "max_wg": 2,
            "requested_total_wg": 524288,
            "observed_wg_count": 2,
            "max_wg_specific_wg_filter": False,
            "global_wg_set_sha256": "abc",
            "per_gpu": [{"gpu_id": 1, "observed_wg_count": 2}],
            "launches": [{
                "requested_total_wg": 524288,
                "unified": True,
                "partitions": [
                    {"gpu_id": 1, "begin": 0, "end": 262144},
                    {"gpu_id": 2, "begin": 262144, "end": 524288},
                ],
                "wg_filter_kind": "original_unified_partition",
            }],
        }
        (Path(str(metric_stem) + "_wg_mapping.json")).write_text(
            json.dumps(mapping), encoding="utf-8"
        )

        audited = runall2_process.audit_wg_mapping(exp, str(metric_stem))
        self.assertEqual(audited["observed_wg_count"], 2)
        self.assertEqual(audited["requested_total_wg"], 524288)

        path = runall2_process.write_cell_result(exp, {
            "returncode": 0,
            "success": True,
            "wg_mapping": audited,
        })
        recorded = json.loads(Path(path).read_text(encoding="utf-8"))
        self.assertTrue(recorded["success"])
        self.assertEqual(recorded["wg_mapping"]["stop_reason"],
                         "runner_map_wg_observed_limit")

    def test_full_workload_mapping_requires_complete_identity_evidence(self):
        exp = {
            "target": "baseline",
            "benchmark": "fft",
            "config_name": "new_m1",
            "common_flags": ["-sampled", "-max-wg=0"],
            "flags": [],
        }
        metric_stem = self.results / "full_metrics"
        mapping = {
            "version": 2,
            "stop_reason": "natural_completion",
            "max_wg": 0,
            "requested_total_wg": 4,
            "observed_wg_count": 4,
            "completed_wg_count": 4,
            "executed_kernel_count": 1,
            "observed_sampling_coverage": 1.0,
            "completed_sampling_coverage": 1.0,
            "stop_time_ns": 0,
            "max_wg_specific_wg_filter": False,
            "global_wg_set_sha256": "full",
            "per_gpu": [{"gpu_id": 1, "observed_wg_count": 4}],
            "launches": [{
                "requested_total_wg": 4,
                "unified": True,
                "partitions": [{"gpu_id": 1, "begin": 0, "end": 4}],
                "wg_filter_kind": "original_unified_partition",
            }],
        }
        Path(str(metric_stem) + "_wg_mapping.json").write_text(
            json.dumps(mapping), encoding="utf-8"
        )

        audited = runall2_process.audit_wg_mapping(exp, str(metric_stem))
        self.assertEqual(audited["stop_reason"], "natural_completion")
        self.assertEqual(audited["completed_wg_count"], 4)
        self.assertEqual(audited["executed_kernel_count"], 1)
        self.assertEqual(audited["completed_sampling_coverage"], 1.0)

        mapping["completed_wg_count"] = 3
        Path(str(metric_stem) + "_wg_mapping.json").write_text(
            json.dumps(mapping), encoding="utf-8"
        )
        with self.assertRaisesRegex(RuntimeError, "completion mismatch"):
            runall2_process.audit_wg_mapping(exp, str(metric_stem))

    def test_mapping_audit_rejects_max_wg_specific_filter(self):
        exp = {
            "target": "baseline",
            "benchmark": "fir",
            "config_name": "baseline",
            "common_flags": ["-max-wg=1"],
            "flags": [],
        }
        metric_stem = self.results / "bad_metrics"
        mapping = {
            "stop_reason": "runner_map_wg_observed_limit",
            "max_wg": 1,
            "observed_wg_count": 1,
            "max_wg_specific_wg_filter": True,
        }
        Path(str(metric_stem) + "_wg_mapping.json").write_text(
            json.dumps(mapping), encoding="utf-8"
        )
        with self.assertRaisesRegex(RuntimeError, "WGFilter"):
            runall2_process.audit_wg_mapping(exp, str(metric_stem))

    def test_runtime_max_wg_accepts_complete_grid_smaller_than_limit(self):
        exp = {
            "target": "baseline",
            "benchmark": "matrixtranspose",
            "config_name": "complete",
            "common_flags": ["-max-wg=10"],
            "flags": [],
        }
        metric_stem = self.results / "natural_metrics"
        mapping = {
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
        }
        Path(str(metric_stem) + "_wg_mapping.json").write_text(
            json.dumps(mapping), encoding="utf-8"
        )

        audited = runall2_process.audit_wg_mapping(exp, str(metric_stem))
        self.assertEqual(audited["observed_wg_count"], 4)
        self.assertEqual(
            audited["stop_reason"], "workload_completed_before_max_wg"
        )
        self.assertEqual(
            audited["report_stop_reason"], "runner_map_wg_observed_limit"
        )

    def test_runtime_max_wg_rejects_early_exit_below_available_limit(self):
        exp = {
            "target": "baseline",
            "benchmark": "fir",
            "config_name": "baseline",
            "common_flags": ["-max-wg=10"],
            "flags": [],
        }
        metric_stem = self.results / "early_exit_metrics"
        mapping = {
            "stop_reason": "runner_map_wg_observed_limit",
            "max_wg": 10,
            "requested_total_wg": 20,
            "observed_wg_count": 4,
        }
        Path(str(metric_stem) + "_wg_mapping.json").write_text(
            json.dumps(mapping), encoding="utf-8"
        )
        with self.assertRaisesRegex(RuntimeError, "expected=10"):
            runall2_process.audit_wg_mapping(exp, str(metric_stem))


if __name__ == "__main__":
    unittest.main()
