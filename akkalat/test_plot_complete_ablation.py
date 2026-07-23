import csv
import json
import math
import tempfile
import unittest
from pathlib import Path

import plot_complete_ablation as plotter


def write_binary_manifest(root, digest="a" * 64):
    root.mkdir(parents=True, exist_ok=True)
    (root / "EXPERIMENT_BINARIES.json").write_text(json.dumps({
        "version": 1,
        "sha256_by_target": {"baseline": digest},
    }))


def write_driver_time(
    path,
    value,
    *,
    max_wg_reached=0,
    completed_stop=0,
    launch_limited=0,
    kernel_drained=0,
    max_wg_limit=None,
    max_wg_admitted=None,
    cu_inst_count=0,
    l1v_demand_requests=0,
    mechanism_config=None,
):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["", "where", "what", "value"])
        writer.writerow([0, "Driver", "total_time", value])
        if max_wg_reached:
            writer.writerow([1, "Driver", "max_wg_reached", max_wg_reached])
        if completed_stop:
            writer.writerow([
                2, "Driver", "max_wg_stop_completed", completed_stop
            ])
        if launch_limited:
            writer.writerow([
                3, "Driver", "max_wg_launch_limited", launch_limited
            ])
        if kernel_drained:
            writer.writerow([
                4, "Driver", "max_wg_kernel_drained", kernel_drained
            ])
        if max_wg_limit is not None:
            writer.writerow([5, "Driver", "max_wg_limit", max_wg_limit])
        if max_wg_admitted is not None:
            writer.writerow([
                6, "Driver", "max_wg_admitted", max_wg_admitted
            ])
        if cu_inst_count:
            writer.writerow([
                7, "GPU[0].SA[0].CU[0]", "cu_inst_count", cu_inst_count
            ])
        if l1v_demand_requests:
            writer.writerow([
                8, "GPU[0].SA[0].L1VCache[0]", "read-hit",
                l1v_demand_requests,
            ])
        if mechanism_config is not None:
            expected = plotter.EXPECTED_MECHANISM_CONFIGS[mechanism_config]
            for offset, (metric, metric_value) in enumerate(
                expected.items(), 20
            ):
                writer.writerow([
                    offset, "GPU[0].Config", metric, metric_value
                ])


def write_sampled_work_log(path, gpu_work, *, complete=True):
    stdout = plotter.stdout_path_for_metrics(path)
    lines = [
        "Executing /tmp/frozen -benchmark=bitonicsort -max-wg=78600 "
        "-sampled -branch-sampled -kernel-sampled"
    ]
    for gpu, kernel, total_wgs, total_wfs in gpu_work:
        if complete:
            lines.append(
                f"WG progress GPU {gpu} kernel {kernel}: 100% "
                f"(WG {total_wgs}/{total_wgs}, "
                f"WF {total_wfs}/{total_wfs})"
            )
        else:
            lines.append(
                f"WG progress GPU {gpu} kernel {kernel}: 50% "
                f"(WG {total_wgs // 2}/{total_wgs}, "
                f"WF {total_wfs // 2}/{total_wfs})"
            )
    stdout.write_text("\n".join(lines) + "\n")


class PartialAblationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        root = Path(self.temp.name)
        self.baseline = root / "baseline"
        self.ablation = root / "ablation"
        write_driver_time(
            plotter.metrics_path(self.baseline, "bitonicsort", "baseline"),
            2.0,
        )
        write_driver_time(
            plotter.metrics_path(
                self.ablation, "bitonicsort", "all_three"
            ),
            1.0,
        )

    def tearDown(self):
        self.temp.cleanup()

    def test_strict_mode_rejects_missing_results(self):
        with self.assertRaises(FileNotFoundError):
            plotter.load_separate(self.baseline, self.ablation)

    def test_partial_mode_keeps_missing_cells_blank(self):
        data, sources = plotter.load_separate(
            self.baseline, self.ablation, allow_partial=True
        )
        self.assertEqual(data["BT"]["Combined"], 2.0)
        self.assertTrue(math.isnan(data["AES"]["Combined"]))
        self.assertTrue(math.isnan(data["BT"]["Local Opt."]))
        self.assertEqual(data["GMEAN"]["Combined"], 2.0)
        self.assertEqual(len(sources), 1)
        self.assertEqual(sources[0]["baseline_driver_total_time_s"], 2.0)
        self.assertEqual(sources[0]["experiment_driver_total_time_s"], 1.0)
        self.assertEqual(sources[0]["l1v_demand_delta_pct"], 0.0)

    def test_partial_mode_can_quarantine_one_completed_benchmark(self):
        data, sources = plotter.load_separate(
            self.baseline,
            self.ablation,
            allow_partial=True,
            excluded_benchmarks={"bitonicsort"},
        )
        self.assertTrue(math.isnan(data["BT"]["Combined"]))
        self.assertFalse(any(source["benchmark"] == "BT" for source in sources))

    def test_partial_result_cannot_pass_paper_gate(self):
        data, _ = plotter.load_separate(
            self.baseline, self.ablation, allow_partial=True
        )
        summary = Path(self.temp.name) / "summary.csv"
        plotter.write_goal_summary(summary, data)
        with summary.open(newline="") as stream:
            rows = {row["series"]: row for row in csv.DictReader(stream)}
        combined = rows["Combined"]
        self.assertEqual(combined["completed_count"], "1")
        self.assertEqual(combined["expected_count"], "14")
        self.assertEqual(combined["geomean"], "2.000000000")
        self.assertEqual(combined["target_scope"], "all_except_SPMV")
        self.assertEqual(combined["target_completed_count"], "1")
        self.assertEqual(combined["target_expected_count"], "13")
        self.assertEqual(combined["target_required_one_pct_count"], "11")
        self.assertEqual(combined["meets_paper_goal"], "False")

    def test_speedup_csv_has_one_header(self):
        data, _ = plotter.load_separate(
            self.baseline, self.ablation, allow_partial=True
        )
        output = Path(self.temp.name) / "speedups.csv"
        plotter.write_csv(output, data)
        rows = output.read_text().splitlines()
        self.assertEqual(sum(row.startswith("benchmark,") for row in rows), 1)

    def test_rejects_legacy_mapped_wg_metrics(self):
        legacy = Path(self.temp.name) / "legacy.csv"
        write_driver_time(legacy, 1.0, max_wg_reached=1)

        with self.assertRaisesRegex(ValueError, "non-drained max-WG"):
            plotter.driver_time(legacy)

    def test_rejects_completed_stop_without_launch_limit(self):
        non_drained = Path(self.temp.name) / "non-drained.csv"
        write_driver_time(
            non_drained,
            1.0,
            max_wg_reached=1,
            completed_stop=1,
        )

        with self.assertRaisesRegex(ValueError, "non-drained max-WG"):
            plotter.driver_time(non_drained)

    def test_rejects_launch_limited_but_not_kernel_drained_metrics(self):
        non_drained = Path(self.temp.name) / "cu-completed.csv"
        write_driver_time(
            non_drained,
            1.0,
            max_wg_reached=1,
            completed_stop=1,
            launch_limited=1,
        )

        with self.assertRaisesRegex(ValueError, "non-drained max-WG"):
            plotter.driver_time(non_drained)

    def test_accepts_launch_limited_completed_wg_metrics(self):
        completed = Path(self.temp.name) / "completed.csv"
        write_driver_time(
            completed,
            1.0,
            max_wg_reached=1,
            completed_stop=1,
            launch_limited=1,
            kernel_drained=1,
        )

        self.assertEqual(plotter.driver_time(completed), 1.0)

    def test_validates_combined_row_off_diagnostic_boundary(self):
        row_off = Path(self.temp.name) / "row-off.csv"
        write_driver_time(
            row_off, 1.0, mechanism_config="all_three_row_off"
        )
        measurement = plotter.driver_measurement(row_off)

        plotter.validate_mechanism_config(
            measurement, row_off, "all_three_row_off"
        )
        with self.assertRaisesRegex(ValueError, "dram_row_continuation_enabled"):
            plotter.validate_mechanism_config(
                measurement, row_off, "all_three"
            )

    def test_rejects_paired_admitted_wg_mismatch(self):
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        common = {
            "max_wg_reached": 1,
            "completed_stop": 1,
            "launch_limited": 1,
            "kernel_drained": 1,
            "max_wg_limit": 78600,
        }
        write_driver_time(
            baseline_path, 2.0, max_wg_admitted=78600, **common
        )
        write_driver_time(
            experiment_path, 1.0, max_wg_admitted=78000, **common
        )

        with self.assertRaisesRegex(ValueError, "max_wg_admitted mismatch"):
            plotter.load_separate(
                self.baseline, self.ablation, allow_partial=True
            )

    def test_rejects_paired_instruction_count_mismatch(self):
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        write_driver_time(baseline_path, 2.0, cu_inst_count=100)
        write_driver_time(experiment_path, 1.0, cu_inst_count=99)

        with self.assertRaisesRegex(ValueError, "cu_inst_count mismatch"):
            plotter.load_separate(
                self.baseline, self.ablation, allow_partial=True
            )

    def test_explicit_sampled_mode_accepts_equal_complete_wg_signature(self):
        write_binary_manifest(self.baseline)
        write_binary_manifest(self.ablation)
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        common = {
            "max_wg_reached": 1,
            "completed_stop": 1,
            "launch_limited": 1,
            "kernel_drained": 1,
            "max_wg_limit": 78600,
            "max_wg_admitted": 78600,
        }
        write_driver_time(
            baseline_path, 2.0, cu_inst_count=100, **common
        )
        write_driver_time(
            experiment_path, 1.0, cu_inst_count=80, **common
        )
        work = [(1, 10, 40000, 80000), (2, 11, 38600, 77200)]
        write_sampled_work_log(baseline_path, work)
        write_sampled_work_log(experiment_path, work)

        data, sources = plotter.load_separate(
            self.baseline,
            self.ablation,
            allow_partial=True,
            allow_sampled_instruction_mismatch=True,
        )
        self.assertEqual(data["BT"]["Combined"], 2.0)
        source = next(
            item for item in sources
            if item["benchmark"] == "BT" and item["series"] == "Combined"
        )
        self.assertEqual(
            source["work_validation_mode"],
            "sampled_complete_per_gpu_wg_wf_signature",
        )
        self.assertEqual(source["sampled_work_signature_entries"], 2)
        self.assertEqual(source["sampled_work_total_wgs"], 78600)
        self.assertIn("cu_inst_count differs", source["validation_warning"])

    def test_sampled_mode_rejects_incomplete_wg_signature(self):
        write_binary_manifest(self.baseline)
        write_binary_manifest(self.ablation)
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        common = {
            "max_wg_reached": 1,
            "completed_stop": 1,
            "launch_limited": 1,
            "kernel_drained": 1,
            "max_wg_limit": 78600,
            "max_wg_admitted": 78600,
        }
        write_driver_time(
            baseline_path, 2.0, cu_inst_count=100, **common
        )
        write_driver_time(
            experiment_path, 1.0, cu_inst_count=80, **common
        )
        work = [(1, 10, 78600, 157200)]
        write_sampled_work_log(baseline_path, work)
        write_sampled_work_log(experiment_path, work, complete=False)

        with self.assertRaisesRegex(ValueError, "not fully drained"):
            plotter.load_separate(
                self.baseline,
                self.ablation,
                allow_partial=True,
                allow_sampled_instruction_mismatch=True,
            )

    def test_sampled_mode_rejects_unequal_wg_signature(self):
        write_binary_manifest(self.baseline)
        write_binary_manifest(self.ablation)
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        common = {
            "max_wg_reached": 1,
            "completed_stop": 1,
            "launch_limited": 1,
            "kernel_drained": 1,
            "max_wg_limit": 78600,
            "max_wg_admitted": 78600,
        }
        write_driver_time(
            baseline_path, 2.0, cu_inst_count=100, **common
        )
        write_driver_time(
            experiment_path, 1.0, cu_inst_count=80, **common
        )
        write_sampled_work_log(
            baseline_path,
            [(1, 10, 40000, 80000), (2, 11, 38600, 77200)],
        )
        write_sampled_work_log(
            experiment_path,
            [(1, 10, 39999, 79998), (2, 11, 38601, 77202)],
        )

        with self.assertRaisesRegex(ValueError, "sampled signature mismatch"):
            plotter.load_separate(
                self.baseline,
                self.ablation,
                allow_partial=True,
                allow_sampled_instruction_mismatch=True,
            )

    def test_reports_l1_demand_delta_without_rejecting_equal_work(self):
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        write_driver_time(
            baseline_path, 2.0, l1v_demand_requests=1000
        )
        write_driver_time(
            experiment_path, 1.0, l1v_demand_requests=989
        )

        data, sources = plotter.load_separate(
            self.baseline, self.ablation, allow_partial=True
        )
        self.assertEqual(data["BT"]["Combined"], 2.0)
        source = next(
            item for item in sources
            if item["benchmark"] == "BT" and item["series"] == "Combined"
        )
        self.assertAlmostEqual(source["l1v_demand_delta_pct"], -1.1)

    def test_accepts_one_pct_l1_demand_work_delta(self):
        baseline_path = plotter.metrics_path(
            self.baseline, "bitonicsort", "baseline"
        )
        experiment_path = plotter.metrics_path(
            self.ablation, "bitonicsort", "all_three"
        )
        write_driver_time(
            baseline_path, 2.0, l1v_demand_requests=1000
        )
        write_driver_time(
            experiment_path, 1.0, l1v_demand_requests=990
        )

        data, _ = plotter.load_separate(
            self.baseline, self.ablation, allow_partial=True
        )
        self.assertEqual(data["BT"]["Combined"], 2.0)

    def test_spmv_is_reported_but_not_required_by_primary_gate(self):
        series_names = [name for name, _, _ in plotter.SERIES]
        data = {
            label: {name: math.nan for name in series_names}
            for _, label in plotter.WORKLOADS
        }
        for label in plotter.OPTIMIZATION_TARGET_LABELS:
            data[label]["Combined"] = 1.4
        data["GMEAN"] = {name: math.nan for name in series_names}

        summary = Path(self.temp.name) / "target-summary.csv"
        plotter.write_goal_summary(summary, data)
        with summary.open(newline="") as stream:
            rows = {row["series"]: row for row in csv.DictReader(stream)}

        combined = rows["Combined"]
        self.assertEqual(combined["completed_count"], "13")
        self.assertEqual(combined["expected_count"], "14")
        self.assertEqual(combined["target_completed_count"], "13")
        self.assertEqual(combined["target_expected_count"], "13")
        self.assertEqual(combined["meets_paper_goal"], "True")

    def test_primary_coverage_gate_requires_eleven_of_thirteen(self):
        series_names = [name for name, _, _ in plotter.SERIES]
        data = {
            label: {name: math.nan for name in series_names}
            for _, label in plotter.WORKLOADS
        }
        labels = sorted(plotter.OPTIMIZATION_TARGET_LABELS)
        for label in labels[:10]:
            data[label]["Combined"] = 1.5
        for label in labels[10:]:
            data[label]["Combined"] = 1.005
        data["GMEAN"] = {name: math.nan for name in series_names}

        summary = Path(self.temp.name) / "ten-of-thirteen.csv"
        plotter.write_goal_summary(summary, data, primary_scope=True)
        with summary.open(newline="") as stream:
            rows = {row["series"]: row for row in csv.DictReader(stream)}
        combined = rows["Combined"]
        self.assertEqual(combined["target_one_pct_count"], "10")
        self.assertEqual(combined["target_required_one_pct_count"], "11")
        self.assertEqual(combined["target_meets_geomean_target"], "True")
        self.assertEqual(
            combined["target_meets_one_pct_coverage_target"], "False"
        )
        self.assertEqual(combined["meets_paper_goal"], "False")

        for label in labels[:11]:
            data[label]["Combined"] = 1.4
        for label in labels[11:]:
            data[label]["Combined"] = 0.9
        passing = Path(self.temp.name) / "eleven-of-thirteen.csv"
        plotter.write_goal_summary(passing, data, primary_scope=True)
        with passing.open(newline="") as stream:
            rows = {row["series"]: row for row in csv.DictReader(stream)}
        combined = rows["Combined"]
        self.assertEqual(combined["target_one_pct_count"], "11")
        self.assertEqual(combined["target_meets_geomean_target"], "True")
        self.assertEqual(
            combined["target_meets_one_pct_coverage_target"], "True"
        )
        self.assertEqual(combined["meets_paper_goal"], "True")

    def test_primary_scope_requires_every_non_spmv_result(self):
        write_binary_manifest(self.baseline)
        write_binary_manifest(self.ablation)
        with self.assertRaises(FileNotFoundError):
            plotter.load_separate(
                self.baseline,
                self.ablation,
                primary_scope=True,
            )

    def test_complete_primary_scope_omits_only_spmv_and_passes_gate(self):
        write_binary_manifest(self.baseline)
        write_binary_manifest(self.ablation)
        for benchmark, label in plotter.WORKLOADS:
            if label == "SPMV":
                continue
            write_driver_time(
                plotter.metrics_path(self.baseline, benchmark, "baseline"),
                1.4,
                mechanism_config="baseline",
            )
            for _, config, _ in plotter.SERIES:
                write_driver_time(
                    plotter.metrics_path(self.ablation, benchmark, config),
                    1.0,
                    mechanism_config=config,
                )

        data, sources = plotter.load_separate(
            self.baseline,
            self.ablation,
            primary_scope=True,
        )
        self.assertTrue(math.isnan(data["SPMV"]["Combined"]))
        self.assertEqual(len(sources), 13 * len(plotter.SERIES))
        self.assertTrue(all(
            source["baseline_binary_sha256"] == "a" * 64
            and source["experiment_binary_sha256"] == "a" * 64
            for source in sources
        ))

        summary = Path(self.temp.name) / "primary-summary.csv"
        plotter.write_goal_summary(summary, data, primary_scope=True)
        with summary.open(newline="") as stream:
            rows = {row["series"]: row for row in csv.DictReader(stream)}
        combined = rows["Combined"]
        self.assertEqual(combined["completed_count"], "13")
        self.assertEqual(combined["expected_count"], "13")
        self.assertEqual(combined["target_completed_count"], "13")
        self.assertEqual(combined["meets_paper_goal"], "True")

    def test_primary_scope_can_explicitly_use_kmeans_membership_phase(self):
        write_binary_manifest(self.baseline)
        write_binary_manifest(self.ablation)
        for benchmark, label in plotter.WORKLOADS:
            if label == "SPMV":
                continue
            metrics_benchmark = (
                "kmeans-reuse-smoke"
                if benchmark == "kmeans" else benchmark
            )
            write_driver_time(
                plotter.metrics_path(
                    self.baseline, metrics_benchmark, "baseline"
                ),
                1.4,
                mechanism_config="baseline",
            )
            for _, config, _ in plotter.SERIES:
                write_driver_time(
                    plotter.metrics_path(
                        self.ablation, metrics_benchmark, config
                    ),
                    1.0,
                    mechanism_config=config,
                )

        data, sources = plotter.load_separate(
            self.baseline,
            self.ablation,
            primary_scope=True,
            kmeans_membership_phase=True,
        )
        self.assertEqual(data["KM"]["Combined"], 1.4)
        km_sources = [
            source for source in sources if source["benchmark"] == "KM"
        ]
        self.assertEqual(len(km_sources), len(plotter.SERIES))
        self.assertTrue(all(
            source["execution_scope"] == "kmeans_membership_phase"
            and "kmeans-reuse-smoke" in source["baseline_metrics_file"]
            for source in km_sources
        ))
        self.assertTrue(all(
            source["execution_scope"] == "full_workload"
            for source in sources if source["benchmark"] != "KM"
        ))

    def test_primary_scope_rejects_different_binary_hashes(self):
        write_binary_manifest(self.baseline, "a" * 64)
        write_binary_manifest(self.ablation, "b" * 64)
        with self.assertRaisesRegex(ValueError, "different frozen binaries"):
            plotter.load_separate(
                self.baseline,
                self.ablation,
                primary_scope=True,
            )

    def test_primary_scope_rejects_mislabeled_mechanism_config(self):
        baseline = Path(self.temp.name) / "config-baseline.csv"
        experiment = Path(self.temp.name) / "config-experiment.csv"
        write_driver_time(
            baseline, 2.0, mechanism_config="baseline"
        )
        write_driver_time(
            experiment, 1.0, mechanism_config="remote_l2_only"
        )
        with self.assertRaisesRegex(ValueError, "mechanism config mismatch"):
            plotter.paired_evidence(
                baseline,
                experiment,
                experiment_config="all_three",
                verify_config=True,
            )


if __name__ == "__main__":
    unittest.main()
