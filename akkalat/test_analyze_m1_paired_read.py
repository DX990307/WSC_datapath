import csv
import hashlib
import json
import tempfile
import unittest
from pathlib import Path

import analyze_m1_paired_read


class M1PairedReadAnalysisTest(unittest.TestCase):
    def test_failed_cell_without_metrics_is_preserved_for_audit(self):
        with tempfile.TemporaryDirectory() as directory:
            result = Path(directory) / "baseline_fir_m1_result.json"
            result.write_text(json.dumps({
                "target": "baseline", "benchmark": "fir",
                "configuration": "m1", "success": False,
                "returncode": -15,
            }), encoding="utf-8")
            row = analyze_m1_paired_read.summarize_cell(result)
            self.assertEqual(row["success"], 0)
            self.assertEqual(row["driver_total_time"], 0)
            self.assertEqual(row["granularity_accepted_aggregates"], 0)

    def test_pair_is_two_frontend_and_two_physical_64b_reads(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            metrics = root / "baseline_relu_m1_metrics.csv"
            with metrics.open("w", newline="", encoding="utf-8") as stream:
                writer = csv.writer(stream)
                writer.writerow(["", "where", "what", "value"])
                writer.writerows([
                    [0, "Driver", "total_time", 2.0],
                    [1, "GPU[0].L2[0]", "granularity_frontend_paired_read_aggregates", 3],
                    [2, "GPU[0].DRAM[0]", "dram_frontend_read_requests", 13],
                    [3, "GPU[0].DRAM[0]", "dram_physical_read_accesses", 13],
                    [4, "GPU[0].DRAM[0]", "dram_aggregate_auto_precharge_stops", 3],
                    [5, "GPU[0].L2[0]", "typed_filter_storage_bits", 688128],
                    [6, "GPU[0].DRAM[0]", "dram_physical_access_bytes", 64],
                    [7, "GPU[0].DRAM[0]", "dram_frontend_read_bytes", 13 * 64],
                    [8, "GPU[0].DRAM[0]", "dram_paired_read_descriptors", 3],
                    [9, "GPU[0].DRAM[0]", "dram_paired_read_members", 6],
                    [10, "GPU[0].DRAM[0]", "dram_paired_read_demand_members", 3],
                    [11, "GPU[0].DRAM[0]", "dram_paired_read_sibling_members", 3],
                    [12, "GPU[0].L2[0]", "granularity_sibling_fills", 2],
                    [13, "GPU[0].L2[0]", "granularity_useful_sibling_lines", 1],
                    [14, "GPU[0].L2[0]", "granularity_timely_sibling_lines", 1],
                    [15, "GPU[0].L2[0]", "granularity_unused_sibling_lines", 1],
                    [16, "GPU[0].L2[0]", "granularity_current_sibling_only_lines", 0],
                    [17, "GPU[0].L2[0]", "granularity_wasted_sibling_bytes", 64],
                    [18, "GPU[0].DRAM[0]", "dram_row_continuation_enabled", 0],
                    [19, "GPU[0].DRAM[0]", "dram_aggregate_continuation_enabled", 1],
                    [20, "GPU[0].L2[0]", "granularity_adaptation_enabled", 1],
                    [21, "GPU[0].L2[0]", "granularity_expansion_attempts", 4],
                    [22, "GPU[0].L2[0]", "granularity_predicted_candidates", 2],
                    [23, "GPU[0].L2[0]", "granularity_real_read_demands", 8],
                    [24, "GPU[0].L2[0]", "granularity_sibling_candidates", 2],
                    [25, "GPU[0].L2[0]", "l2_fill_forwarding_enabled", 1],
                    [26, "GPU[0].L2[0]", "l2_fill_forwarding_eligible_read_entries", 10],
                    [27, "GPU[0].L2[0]", "l2_fill_forwarding_forwarded_read_entries", 9],
                    [28, "GPU[0].L2[0]", "l2_fill_forwarding_forwarded_reads", 12],
                    [29, "GPU[0].L2[0]", "l2_fill_forwarding_buffer_fallbacks", 1],
                    [30, "GPU[0].L2[0]", "l2_resident_filter_queries", 20],
                    [31, "GPU[0].L2[0]", "l2_resident_filter_negatives", 12],
                    [32, "GPU[0].L2[0]", "l2_resident_filter_read_bypasses", 9],
                    [33, "GPU[0].L2[0]", "l2_resident_filter_read_busy_fallbacks", 1],
                    [34, "GPU[0].L2[0]", "l2_miss_to_dram_issue_samples", 10],
                    [35, "GPU[0].L2[0]", "l2_miss_to_dram_issue_total_ns", 80],
                    [36, "GPU[0].L2[0]", "l2_fast_miss_to_dram_issue_samples", 6],
                    [37, "GPU[0].L2[0]", "l2_fast_miss_to_dram_issue_total_ns", 18],
                    [38, "GPU[0].L2[0]", "l2_demand_read_latency_samples", 5],
                    [39, "GPU[0].L2[0]", "l2_demand_read_latency_total_ns", 200],
                    [40, "GPU[0].DRAM[0]", "dram_aggregate_immediate_continuations", 2],
                    [41, "GPU[0].L2[0]", "granularity_accepted_aggregates", 2],
                    [42, "GPU[0].L2[0]", "granularity_demand_pair_ready_opportunities", 1],
                    [43, "GPU[0].L2[0]", "granularity_demand_pair_aggregates", 1],
                ])
            result = root / "baseline_relu_m1_result.json"
            result.write_text(json.dumps({
                "success": True,
                "metrics": str(metrics),
                "exp": {
                    "target": "baseline", "benchmark": "relu",
                    "config_name": "m1", "common_flags": [],
                },
            }), encoding="utf-8")
            row = analyze_m1_paired_read.summarize_cell(result)
            self.assertEqual(row["physical_read_relation_ok"], 1)
            self.assertEqual(row["aggregate_row_reuse_bounded"], 1)
            self.assertEqual(
                row["aggregate_immediate_continuation_bounded"], 1
            )
            self.assertEqual(row["typed_filter_count"], 1)
            self.assertEqual(row["typed_filter_total_storage_bits"], 688128)
            self.assertEqual(row["dram_physical_access_unit_bytes"], 64)
            self.assertEqual(row["dram_physical_read_bytes"], 13 * 64)
            self.assertEqual(row["dram_physical_access_unit_consistent"], 1)
            self.assertEqual(row["physical_read_byte_relation_ok"], 1)
            self.assertEqual(row["paired_member_relation_ok"], 1)
            self.assertEqual(row["paired_work_partition_ok"], 1)
            self.assertEqual(row["demand_pair_accounting_bounded"], 1)
            self.assertEqual(row["granularity_terminal_unused_sibling_lines"], 1)
            self.assertEqual(row["granularity_terminal_wasted_sibling_bytes"], 64)
            self.assertAlmostEqual(row["sibling_accuracy"], 1 / 2)
            self.assertAlmostEqual(row["sibling_waste_fraction"], 1 / 2)
            self.assertEqual(row["sibling_timeliness_partition_ok"], 1)
            self.assertEqual(row["sibling_terminal_partition_ok"], 1)
            self.assertEqual(row["dram_controller_count"], 1)
            self.assertEqual(row["general_row_continuation_disabled"], 1)
            self.assertEqual(row["aggregate_continuation_mode_ok"], 1)
            self.assertEqual(row["granularity_runtime_mode_ok"], 1)
            self.assertAlmostEqual(row["predictor_candidate_coverage"], 0.25)
            self.assertAlmostEqual(row["sibling_candidate_coverage"], 0.25)
            self.assertAlmostEqual(row["aggregate_acceptance_rate"], 0.75)
            self.assertEqual(row["fill_forwarding_runtime_mode_ok"], 1)
            self.assertAlmostEqual(row["fill_forwarding_entry_coverage"], 0.9)
            self.assertAlmostEqual(
                row["fill_forwarding_buffer_fallback_fraction"], 0.1
            )
            self.assertAlmostEqual(
                row["resident_negative_read_bypass_coverage"], 0.9
            )
            self.assertAlmostEqual(row["resident_filter_negative_fraction"], 0.6)
            self.assertAlmostEqual(row["fast_miss_issue_fraction"], 0.6)
            self.assertAlmostEqual(row["l2_miss_to_dram_issue_avg_ns"], 8.0)
            self.assertAlmostEqual(
                row["l2_fast_miss_to_dram_issue_avg_ns"], 3.0
            )
            self.assertAlmostEqual(row["l2_demand_read_latency_avg_ns"], 40.0)

    def test_speedup_and_geomean_are_derived_from_matching_baseline(self):
        rows = [
            {"target": "baseline", "benchmark": "a", "config": "baseline",
             "success": 1, "driver_total_time": 10.0},
            {"target": "baseline", "benchmark": "a", "config": "new_m1",
             "success": 1, "driver_total_time": 5.0},
            {"target": "baseline", "benchmark": "b", "config": "baseline",
             "success": 1, "driver_total_time": 8.0},
            {"target": "baseline", "benchmark": "b", "config": "new_m1",
             "success": 1, "driver_total_time": 8.0},
        ]
        analyze_m1_paired_read.add_baseline_speedups(rows)
        self.assertEqual(rows[1]["speedup_vs_baseline"], 2.0)
        geomeans = analyze_m1_paired_read.geomean_rows(rows)
        m1 = next(row for row in geomeans if row["config"] == "new_m1")
        self.assertAlmostEqual(m1["geomean_speedup"], 2 ** 0.5)
        self.assertEqual(m1["benchmark_count"], 2)

    def test_filter_contribution_does_not_conflate_lookup_and_traffic(self):
        common = {
            "target": "baseline", "benchmark": "a", "success": 1,
            "speedup_vs_baseline": 1.0,
            "dram_physical_read_bytes": 6400.0,
            "granularity_terminal_wasted_sibling_bytes": 64.0,
        }
        rows = [
            {**common, "config": "new_m1", "driver_total_time": 5.0,
             "filter_exact_lookup_total": 20.0,
             "dram_physical_read_accesses": 100.0},
            {**common, "config": "paired_read_without_filter",
             "driver_total_time": 5.0, "filter_exact_lookup_total": 100.0,
             "dram_physical_read_accesses": 100.0},
        ]
        contribution = analyze_m1_paired_read.filter_contribution_rows(rows)[0]
        self.assertEqual(contribution["filter_exact_lookups_avoided"], 80.0)
        self.assertEqual(contribution["filter_exact_lookup_reduction"], 0.8)
        self.assertEqual(contribution["physical_read_access_delta"], 0.0)
        self.assertEqual(contribution["physical_read_byte_delta"], 0.0)

    def test_full_workload_audit_rejects_prefix_and_cross_config_mismatch(self):
        rows = [
            {"benchmark": "a", "config": "baseline", "success": 1,
             "wg_mapping_present": 1, "diagnostic_prefix_wg": 0,
             "wg_stop_reason": "natural_completion", "wg_observed_count": 10,
             "wg_completed_count": 10, "wg_requested_count": 10,
             "wg_kernel_count": 1, "wg_launch_signature": "launch-base",
             "wg_observed_sampling_coverage": 1.0,
             "wg_completed_sampling_coverage": 1.0,
             "wg_global_set_sha256": "base"},
            {"benchmark": "a", "config": "new_m1", "success": 1,
             "wg_mapping_present": 1, "diagnostic_prefix_wg": 4,
             "wg_stop_reason": "runner_limit", "wg_observed_count": 4,
             "wg_completed_count": 3, "wg_requested_count": 10,
             "wg_kernel_count": 1, "wg_launch_signature": "launch-m1",
             "wg_observed_sampling_coverage": 0.4,
             "wg_completed_sampling_coverage": 0.3,
             "wg_global_set_sha256": "m1"},
        ]
        errors = analyze_m1_paired_read.full_workload_errors(rows)
        self.assertTrue(any("identity differs" in error for error in errors))
        self.assertTrue(any("positive max-WG" in error for error in errors))
        self.assertTrue(any("observed 4 of 10" in error for error in errors))

    def test_bounded_workload_audit_requires_same_wg_set(self):
        common = {
            "benchmark": "a", "success": 1, "wg_mapping_present": 1,
            "diagnostic_prefix_wg": 78600,
            "wg_stop_reason": "runner_map_wg_observed_limit",
            "wg_observed_count": 78600, "wg_requested_count": 100000,
            "wg_kernel_count": 1, "wg_launch_signature": "launch",
            "wg_global_set_sha256": "same-set",
        }
        rows = [
            {**common, "config": "baseline"},
            {**common, "config": "m1"},
        ]
        self.assertEqual(
            analyze_m1_paired_read.bounded_workload_errors(rows, 78600), []
        )
        rows[1]["wg_global_set_sha256"] = "different-set"
        errors = analyze_m1_paired_read.bounded_workload_errors(rows, 78600)
        self.assertTrue(any("identity differs" in error for error in errors))

    def test_bounded_workload_audit_accepts_short_natural_completion(self):
        row = {
            "benchmark": "short", "config": "m1", "success": 1,
            "wg_mapping_present": 1, "diagnostic_prefix_wg": 78600,
            "wg_stop_reason": "natural_completion",
            "wg_observed_count": 24000, "wg_requested_count": 24000,
            "wg_kernel_count": 1, "wg_launch_signature": "launch",
            "wg_global_set_sha256": "short-set",
        }
        self.assertEqual(
            analyze_m1_paired_read.bounded_workload_errors([row], 78600), []
        )

    def test_retention_summary_reports_failed_filter_traffic_gate(self):
        common = {
            "benchmark": "a", "success": 1, "speedup_vs_baseline": 1.1,
            "granularity_frontend_paired_read_aggregates": 10.0,
            "granularity_useful_sibling_lines": 6.0,
            "granularity_terminal_unused_sibling_lines": 4.0,
            "dram_aggregate_auto_precharge_stops": 10.0,
            "granularity_terminal_wasted_sibling_bytes": 256.0,
            "dram_physical_read_bytes": 6400.0,
        }
        rows = [
            {**common, "config": "new_m1"},
            {**common, "config": "paired_read_without_filter"},
        ]
        summary = analyze_m1_paired_read.retention_summary(rows)[0]
        self.assertEqual(summary["useful_exceeds_unused"], 1)
        self.assertEqual(summary["filter_reduces_wasted_bytes"], 0)
        self.assertEqual(summary["filter_reduces_physical_read_bytes"], 0)
        self.assertEqual(summary["paired_row_reuse_rate"], 1.0)
        self.assertEqual(summary["applicable_positive_majority"], 1)
        self.assertEqual(summary["diagnostic_retain_gate_pass"], 0)
        self.assertIn(
            "filter_did_not_reduce_wasted_bytes",
            summary["diagnostic_retain_gate_failures"],
        )

    def test_campaign_identity_requires_exact_grid_and_frozen_binary(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            binary = root / "simulator"
            binary.write_bytes(b"frozen")
            digest = hashlib.sha256(binary.read_bytes()).hexdigest()
            rows = [{
                "benchmark": "aes", "config": "baseline", "success": 1,
            }]
            (root / "EXPERIMENT_METADATA.json").write_text(json.dumps({
                "experiment_count": 1,
                "experiments": [{
                    "benchmark": "aes",
                    "configuration": "baseline",
                    "command": [
                        str(binary), "-benchmark=aes",
                        "-dram-row-continuation-enable=false",
                    ],
                }],
            }))
            (root / "EXPERIMENT_BINARIES.json").write_text(json.dumps({
                "sha256_by_target": {"baseline": digest},
            }))
            audit = analyze_m1_paired_read.audit_campaign_identity(
                root, rows, ("aes",), ("baseline",), digest,
            )
            self.assertEqual(audit["cell_count"], 1)
            self.assertEqual(audit["binary_sha256"], digest)
            metadata = json.loads(
                (root / "EXPERIMENT_METADATA.json").read_text()
            )
            metadata["experiments"][0]["command"][-1] = (
                "-dram-row-continuation-enable=true"
            )
            (root / "EXPERIMENT_METADATA.json").write_text(
                json.dumps(metadata)
            )
            with self.assertRaisesRegex(
                ValueError, "general-row-continuation mismatch"
            ):
                analyze_m1_paired_read.audit_campaign_identity(
                    root, rows, ("aes",), ("baseline",), digest,
                )
            metadata["experiments"][0]["command"][-1] = (
                "-dram-row-continuation-enable=false"
            )
            (root / "EXPERIMENT_METADATA.json").write_text(
                json.dumps(metadata)
            )
            with self.assertRaisesRegex(ValueError, "grid mismatch"):
                analyze_m1_paired_read.audit_campaign_identity(
                    root, rows, ("aes", "fft"), ("baseline",), digest,
                )
            with self.assertRaisesRegex(ValueError, "expected SHA-256"):
                analyze_m1_paired_read.audit_campaign_identity(
                    root, rows, ("aes",), ("baseline",), "0" * 64,
                )


if __name__ == "__main__":
    unittest.main()
