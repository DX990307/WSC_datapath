#!/usr/bin/env python3

import json
import tempfile
import threading
import unittest
from unittest import mock
from pathlib import Path
from types import SimpleNamespace

import runall2_config
import runall2
from runall2_args import DEFAULT_MAX_WG
from runall2_constants import (
    BASE_COMMON_FLAGS,
    BENCHMARK_ALIASES,
    CONFIGS,
    DEFAULT_BENCHMARK_FLAGS,
    TRADITIONAL_BENCHMARKS,
    TRADITIONAL_PRIMARY_BENCHMARKS,
)


class BenchmarkPresetTest(unittest.TestCase):
    def test_default_max_wg_matches_github_runner(self):
        self.assertEqual(DEFAULT_MAX_WG, 78600)

    def test_paper_scope_contains_all_fourteen_traditional_workloads(self):
        self.assertEqual(
            TRADITIONAL_PRIMARY_BENCHMARKS,
            TRADITIONAL_BENCHMARKS,
        )
        self.assertEqual(
            BENCHMARK_ALIASES["traditional-primary"],
            TRADITIONAL_PRIMARY_BENCHMARKS,
        )
        self.assertEqual(len(TRADITIONAL_PRIMARY_BENCHMARKS), 14)
        self.assertIn("spmv", TRADITIONAL_PRIMARY_BENCHMARKS)

    def test_formal_common_flags_explicitly_fix_l1v_mshr_to_sixteen(self):
        self.assertIn("-l1v-mshr-entries=16", BASE_COMMON_FLAGS)

    def test_formal_common_flags_fix_l1v_transaction_window_to_sixteen(self):
        self.assertIn("-l1v-max-concurrent-trans=16", BASE_COMMON_FLAGS)

    def test_formal_filter_targets_point_one_percent_fpr(self):
        self.assertIn("-typed-filter-fingerprint-bits=13", DEFAULT_BENCHMARK_FLAGS)

    def test_resume_requires_success_metrics_and_result(self):
        exp = {
            "target": "baseline",
            "benchmark": "fir",
            "config_name": "baseline",
            "common_flags": ["-max-wg=2"],
            "flags": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertEqual(
                runall2_config.filter_missing_metric_exps([exp], root),
                [exp],
            )
            stem = root / "baseline_fir_baseline"
            Path(str(stem) + "_metrics.csv").write_text("metrics")
            Path(str(stem) + "_result.json").write_text(json.dumps({
                "success": True,
                "returncode": 0,
                "simulator_returncode": 0,
            }))
            self.assertEqual(
                runall2_config.filter_missing_metric_exps([exp], root),
                [],
            )

    def test_resume_ignores_obsolete_runtime_mapping_sidecar(self):
        exp = {
            "target": "baseline",
            "benchmark": "fir",
            "config_name": "baseline",
            "common_flags": ["-max-wg=2"],
            "flags": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stem = root / "baseline_fir_baseline"
            Path(str(stem) + "_metrics.csv").write_text("metrics")
            Path(str(stem) + "_result.json").write_text(json.dumps({
                "success": True,
                "returncode": 0,
                "simulator_returncode": 0,
            }))
            Path(str(stem) + "_metrics_wg_mapping.json").write_text("{}")
            self.assertEqual(
                runall2_config.filter_missing_metric_exps([exp], root),
                [],
            )

    def test_resume_does_not_require_runtime_mapping_sidecar(self):
        exp = {
            "target": "baseline",
            "benchmark": "fft",
            "config_name": "m1",
            "common_flags": [],
            "flags": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stem = root / "baseline_fft_m1"
            Path(str(stem) + "_metrics.csv").write_text("metrics")
            Path(str(stem) + "_result.json").write_text(json.dumps({
                "success": True,
                "returncode": 0,
                "simulator_returncode": 0,
            }))
            self.assertEqual(
                runall2_config.filter_missing_metric_exps([exp], root), []
            )

    def test_resume_accepts_naturally_completed_grid_below_max_wg(self):
        exp = {
            "target": "baseline",
            "benchmark": "matrixtranspose",
            "config_name": "complete",
            "common_flags": ["-max-wg=10"],
            "flags": [],
        }
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stem = root / "baseline_matrixtranspose_complete"
            Path(str(stem) + "_metrics.csv").write_text("metrics")
            Path(str(stem) + "_result.json").write_text(json.dumps({
                "success": True,
                "returncode": 0,
                "simulator_returncode": 0,
            }))
            Path(str(stem) + "_metrics_wg_mapping.json").write_text(
                json.dumps({
                    "stop_reason": "runner_map_wg_observed_limit",
                    "max_wg": 10,
                    "requested_total_wg": 4,
                    "observed_wg_count": 4,
                    "stop_time_ns": 0,
                    "max_wg_specific_wg_filter": False,
                    "global_wg_set_sha256": "abc",
                    "per_gpu": [{
                        "gpu_id": 1,
                        "observed_wg_count": 4,
                    }],
                    "launches": [{
                        "requested_total_wg": 4,
                        "unified": True,
                    }],
                })
            )
            self.assertEqual(
                runall2_config.filter_missing_metric_exps([exp], root),
                [],
            )

    def test_formal_predictor_capacity_is_explicit_and_bounded(self):
        self.assertIn("-prefetch-predictor-entries=256", DEFAULT_BENCHMARK_FLAGS)


class HostMemoryWorkerLimitTest(unittest.TestCase):
    @staticmethod
    def args(**overrides):
        values = {
            "max_workers": 14,
            "extra_benchmark_flags": "",
            "sampled_sweep": False,
            "balanced_sweep": False,
            "sampled_warmups": "",
            "sampled_granularities": "",
            "sampled_parallel_limit": 0,
            "memory_reserve_gib": 50.0,
            "memory_per_worker_gib": 32.0,
            "initial_workers": 6,
            "memory_scan_minutes": 30.0,
        }
        values.update(overrides)
        return SimpleNamespace(**values)

    def test_dynamic_memory_admission_keeps_max_and_starts_at_six(self):
        args = self.args()
        self.assertEqual(
            runall2.choose_max_workers(
                args, exp_count=70, mem_available_gib=251.0
            ),
            14,
        )
        self.assertEqual(runall2.choose_initial_workers(args, 14), 6)
        self.assertEqual(args.memory_worker_cap, 14)
        self.assertEqual(args.mem_available_gib_at_launch, 251.0)

    def test_low_launch_memory_waits_instead_of_reducing_maximum(self):
        args = self.args()
        self.assertEqual(
            runall2.choose_max_workers(
                args, exp_count=70, mem_available_gib=40.0
            ),
            14,
        )

    def test_memory_scan_interval_must_be_positive(self):
        with self.assertRaisesRegex(ValueError, "memory-scan-minutes"):
            runall2.choose_max_workers(
                self.args(memory_scan_minutes=0.0),
                exp_count=70,
                mem_available_gib=251.0,
            )


class DynamicAdmissionSchedulerTest(unittest.TestCase):
    def test_successful_scans_grow_concurrency_one_slot_at_a_time(self):
        gate = threading.Event()
        lock = threading.Lock()
        active = 0
        peak = 0

        def fake_run(exp):
            del exp
            nonlocal active, peak
            with lock:
                active += 1
                peak = max(peak, active)
            gate.wait(timeout=1)
            with lock:
                active -= 1
            return {"success": True}

        release = threading.Timer(0.08, gate.set)
        release.start()
        try:
            with mock.patch.object(runall2, "run_exp", side_effect=fake_run), \
                    mock.patch.object(runall2, "terminate_all_processes"):
                runall2.run_all_experiments(
                    [{"id": i} for i in range(3)],
                    max_workers=3,
                    initial_workers=1,
                    memory_reserve_gib=50,
                    memory_scan_seconds=0.01,
                    mem_available_fn=lambda: 100,
                )
        finally:
            gate.set()
            release.cancel()
        self.assertEqual(peak, 3)


class LocalComponentConfigTest(unittest.TestCase):
    def test_local_components_use_only_64b_paths(self):
        configs = dict(CONFIGS)
        self.assertEqual(
            configs["local_cf"],
            ["-l2-resident-filter-enable=true"],
        )
        complete = configs["local_optimization"]
        self.assertIn("-l2-resident-filter-enable=true", complete)
        self.assertIn("-l2-fill-forwarding-enable=true", complete)
        self.assertIn("-dram-row-continuation-enable=true", complete)
        for _, flags in CONFIGS:
            self.assertFalse(any("dram-batch" in flag for flag in flags))
            self.assertFalse(any("dram-prefetch" in flag for flag in flags))
            self.assertFalse(any("dram-read-coalescing" in flag for flag in flags))


class RemoteAblationIsolationTest(unittest.TestCase):
    def make_args(self):
        return SimpleNamespace(
            configs="",
            sampled_sweep=False,
            balanced_sweep=False,
            sampled_warmups="",
            sampled_granularities="",
            remote_data_path_batch_lines=8,
            remote_data_path_batches=64,
        )

    @staticmethod
    def bool_flags(flags):
        names = (
            "-l2-resident-filter-enable=",
            "-l2-fill-forwarding-enable=",
            "-dram-row-continuation-enable=",
            "-l2-filter-prefetch-enable=",
            "-l2-prefetch-predictor-only=",
            "-l2-prefetch-ungated=",
            "-l2-granularity-adaptation-enable=",
            "-l2-adaptive-pair-enable=",
            "-l2-granularity-without-filter=",
            "-l2-granularity-always-expand=",
            "-l2-granularity-predictor-only=",
            "-remote-data-path-enable=",
            "-remote-data-path-dedup-enable=",
            "-remote-data-path-batching-enable=",
            "-remote-data-path-l2-enable=",
            "-remote-filter-prefetch-enable=",
        )
        return {
            prefix: next(
                flag.removeprefix(prefix)
                for flag in flags
                if flag.startswith(prefix)
            )
            for prefix in names
        }

    def test_each_standalone_enables_only_its_mechanism(self):
        configs = dict(
            runall2_config.build_remote_data_path_ablation_configs(
                self.make_args()
            )
        )
        self.assertEqual(
            set(configs),
            {"baseline", "m1", "m2", "m3", "complete"},
        )

        expected = {
            "baseline": (
                "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false"
            ),
            "m1": (
                "true", "true", "false", "false", "false", "false", "false", "true", "false", "false", "false", "false", "false", "false", "false", "false"
            ),
            "m2": (
                "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "true", "true", "true", "false", "true"
            ),
            "m3": (
                "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "false", "true", "false", "false", "true", "false"
            ),
            "complete": (
                "true", "true", "false", "false", "false", "false", "false", "true", "false", "false", "false", "true", "true", "true", "true", "true"
            ),
        }
        prefixes = (
            "-l2-resident-filter-enable=",
            "-l2-fill-forwarding-enable=",
            "-dram-row-continuation-enable=",
            "-l2-filter-prefetch-enable=",
            "-l2-prefetch-predictor-only=",
            "-l2-prefetch-ungated=",
            "-l2-granularity-adaptation-enable=",
            "-l2-adaptive-pair-enable=",
            "-l2-granularity-without-filter=",
            "-l2-granularity-always-expand=",
            "-l2-granularity-predictor-only=",
            "-remote-data-path-enable=",
            "-remote-data-path-dedup-enable=",
            "-remote-data-path-batching-enable=",
            "-remote-data-path-l2-enable=",
            "-remote-filter-prefetch-enable=",
        )
        for name, values in expected.items():
            actual = self.bool_flags(configs[name])
            self.assertEqual(tuple(actual[prefix] for prefix in prefixes), values)

    def test_explicit_m1_diagnostic_configs_are_available_but_not_formal(self):
        formal = dict(
            runall2_config.build_remote_data_path_ablation_configs(
                self.make_args()
            )
        )
        self.assertEqual(
            set(formal), {"baseline", "m1", "m2", "m3", "complete"}
        )

        args = self.make_args()
        args.configs = (
            "old_m1_independent_prefetch,cuckoo_filter_only,always_pair,"
            "predictor_only,paired_read_without_filter,new_m1,"
            "m1_bypass_fill_only,m1_bypass_fill_predictor_only,"
            "m1_without_cuckoo"
        )
        diagnostics = dict(
            runall2_config.build_remote_data_path_ablation_configs(args)
        )
        self.assertEqual(set(diagnostics), {
            "old_m1_independent_prefetch", "cuckoo_filter_only",
            "always_pair", "predictor_only", "paired_read_without_filter",
            "new_m1", "m1_bypass_fill_only",
            "m1_bypass_fill_predictor_only", "m1_without_cuckoo",
        })
        self.assertIn(
            "-l2-filter-prefetch-enable=true",
            diagnostics["old_m1_independent_prefetch"],
        )
        self.assertIn(
            "-l2-resident-filter-enable=true",
            diagnostics["cuckoo_filter_only"],
        )
        self.assertIn(
            "-l2-resident-filter-enable=true",
            diagnostics["m1_bypass_fill_only"],
        )
        self.assertIn(
            "-l2-fill-forwarding-enable=true",
            diagnostics["m1_bypass_fill_only"],
        )
        self.assertIn(
            "-l2-granularity-adaptation-enable=false",
            diagnostics["m1_bypass_fill_only"],
        )
        paired_isolation = diagnostics["m1_bypass_fill_predictor_only"]
        self.assertIn("-l2-resident-filter-enable=true", paired_isolation)
        self.assertIn("-l2-fill-forwarding-enable=true", paired_isolation)
        self.assertIn(
            "-l2-granularity-predictor-only=true", paired_isolation
        )
        self.assertIn(
            "-l2-granularity-adaptation-enable=false", paired_isolation
        )
        self.assertIn(
            "-l2-granularity-always-expand=true",
            diagnostics["always_pair"],
        )
        self.assertIn(
            "-l2-granularity-predictor-only=true",
            diagnostics["predictor_only"],
        )
        self.assertIn(
            "-l2-granularity-without-filter=true",
            diagnostics["paired_read_without_filter"],
        )
        self.assertEqual(diagnostics["new_m1"], formal["m1"])
        self.assertIn(
            "-typed-filter-mode=disabled",
            diagnostics["m1_without_cuckoo"],
        )
        self.assertIn(
            "-l2-adaptive-pair-enable=true",
            diagnostics["m1_without_cuckoo"],
        )

    def test_fixed_capacities_are_identical_across_configs(self):
        configs = runall2_config.build_remote_data_path_ablation_configs(
            self.make_args()
        )
        configs = [
            (name, DEFAULT_BENCHMARK_FLAGS + flags)
            for name, flags in configs
        ]
        fixed_prefixes = (
            "-remote-data-path-batch-lines=",
            "-remote-data-path-batches=",
            "-typed-filter-mode=",
            "-typed-filter-slots-per-bucket=",
            "-typed-filter-fingerprint-bits=",
            "-typed-filter-lookup-latency=",
            "-typed-filter-lookup-width=",
            "-typed-filter-update-latency=",
            "-typed-filter-update-width=",
        )
        references = {
            prefix: next(
                flag for flag in configs[0][1] if flag.startswith(prefix)
            )
            for prefix in fixed_prefixes
        }
        for _, flags in configs[1:]:
            for prefix, reference in references.items():
                self.assertIn(reference, flags)

    def test_configs_selects_remote_ablation_cells_without_changing_flags(self):
        all_configs = dict(
            runall2_config.build_remote_data_path_ablation_configs(
                self.make_args()
            )
        )
        args = self.make_args()
        args.configs = "baseline,m2,m3,complete"
        selected = runall2_config.build_remote_data_path_ablation_configs(args)

        self.assertEqual(
            [name for name, _ in selected],
            ["baseline", "m2", "m3", "complete"],
        )
        for name, flags in selected:
            self.assertEqual(flags, all_configs[name])

    def test_configs_rejects_unknown_remote_ablation_cell(self):
        args = self.make_args()
        args.configs = "baseline,not-a-config"
        with self.assertRaisesRegex(
            ValueError, "unknown remote-ablation configs"
        ):
            runall2_config.build_remote_data_path_ablation_configs(args)


class SafeLayerNormRerunTest(unittest.TestCase):
    def test_removes_only_layernorm_loop_sampling(self):
        layernorm = {
            "benchmark": "llmop",
            "common_flags": [
                "-sampled",
                "-loop-sampled",
                "-loop-sampled-warmup=8",
            ],
            "flags": ["-op=layernorm", "-hidden=4096"],
        }
        softmax = {
            "benchmark": "llmop",
            "common_flags": ["-sampled", "-loop-sampled"],
            "flags": ["-op=row-softmax"],
        }

        updated = runall2_config.disable_layernorm_loop_sampling(
            [layernorm, softmax]
        )

        self.assertEqual(updated, 1)
        self.assertEqual(layernorm["common_flags"], ["-sampled"])
        self.assertIn("-loop-sampled", softmax["common_flags"])


class ReusableBaselineLibraryTest(unittest.TestCase):
    def test_removes_only_baseline_cells_after_manifest_validation(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "BASELINE_LIBRARY.json").write_text(json.dumps({
                "kind": "reusable_baseline_library",
                "version": 1,
            }))
            (directory / "baseline_aes_baseline_metrics.csv").write_text(
                "where,what,value\nDriver,total_time,1\n"
            )
            args = SimpleNamespace(reuse_baseline_dir=str(directory))
            exps = [
                {"benchmark": "aes", "config_name": "baseline"},
                {"benchmark": "aes", "config_name": "m1"},
            ]
            self.assertEqual(
                runall2.apply_reusable_baseline(args, exps),
                [{"benchmark": "aes", "config_name": "m1"}],
            )

    def test_rejects_incomplete_library(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "BASELINE_LIBRARY.json").write_text(json.dumps({
                "kind": "reusable_baseline_library",
            }))
            args = SimpleNamespace(reuse_baseline_dir=str(directory))
            with self.assertRaisesRegex(ValueError, "missing selected benchmarks"):
                runall2.apply_reusable_baseline(args, [{
                    "benchmark": "aes", "config_name": "baseline",
                }])


if __name__ == "__main__":
    unittest.main()
