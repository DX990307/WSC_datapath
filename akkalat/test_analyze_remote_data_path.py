#!/usr/bin/env python3

import unittest

import analyze_remote_data_path as analysis


class L1VDemandRequestTest(unittest.TestCase):
    def test_counts_only_l1v_demand_outcomes(self):
        rows = [
            {"where": "GPU[0].SA[1].L1VCache[2]", "what": "read-hit", "value": "7"},
            {"where": "GPU[0].SA[1].L1VCache[2]", "what": "write-miss", "value": "3"},
            {"where": "GPU[0].SA[1].L1VCache[2]", "what": "req_average_latency", "value": "5e-9"},
            {"where": "GPU[0].L2[2]", "what": "read-hit", "value": "100"},
        ]

        self.assertEqual(analysis.l1v_demand_requests(rows), 10.0)

    def test_counts_mshr_outcomes_as_demands(self):
        rows = [
            {"where": "GPU[48].SA[7].L1VCache[3]", "what": "read-mshr-hit", "value": "11"},
            {"where": "GPU[48].SA[7].L1VCache[3]", "what": "write-mshr-hit", "value": "2"},
        ]

        self.assertEqual(analysis.l1v_demand_requests(rows), 13.0)


class CUInstructionCountTest(unittest.TestCase):
    def test_sums_instruction_counts_across_compute_units(self):
        rows = [
            {"where": "GPU[0].SA[0].CU[0]", "what": "cu_inst_count", "value": "17"},
            {"where": "GPU[1].SA[2].CU[3]", "what": "cu_inst_count", "value": "25"},
            {"where": "Driver", "what": "total_time", "value": "1"},
        ]

        self.assertEqual(analysis.cu_inst_count(rows), 42.0)


class CachePressureMetricsTest(unittest.TestCase):
    def test_separates_l1v_and_l2_outcomes(self):
        rows = [
            {"where": "GPU[0].SA[0].L1VCache[0]", "what": "read-hit", "value": "30"},
            {"where": "GPU[0].SA[0].L1VCache[0]", "what": "read-miss", "value": "10"},
            {"where": "GPU[0].SA[0].L1VCache[0]", "what": "read-mshr-hit", "value": "10"},
            {"where": "GPU[0].L2[0]", "what": "read-hit", "value": "4"},
            {"where": "GPU[0].L2[0]", "what": "read-miss", "value": "6"},
        ]

        metrics = analysis.cache_pressure_metrics(rows)
        self.assertEqual(metrics["l1v_read_total"], 50.0)
        self.assertEqual(metrics["l1v_read_hit_pct"], 60.0)
        self.assertEqual(metrics["l1v_read_miss_pct"], 20.0)
        self.assertEqual(metrics["l1v_read_mshr_hit_pct"], 20.0)
        self.assertEqual(metrics["l2_read_total"], 10.0)
        self.assertEqual(metrics["l2_read_hit_pct"], 40.0)
        self.assertEqual(metrics["l2_read_miss_pct"], 60.0)


class DRAMRowMetricsTest(unittest.TestCase):
    def test_uses_column_and_all_command_denominators(self):
        metrics = {
            "dram_row_commands_issued": 200,
            "dram_row_column_commands": 100,
            "dram_row_reuse_hits": 75,
            "dram_row_auto_precharge_stops": 30,
            "dram_row_activate_commands": 40,
            "dram_row_precharge_commands": 60,
        }

        derived = analysis.dram_row_metrics(metrics)
        self.assertEqual(derived["dram_row_reuse_pct"], 75.0)
        self.assertEqual(derived["dram_row_auto_precharge_stop_pct"], 30.0)
        self.assertEqual(derived["dram_row_activates_per_column"], 0.4)
        self.assertEqual(derived["dram_row_precharges_per_column"], 0.6)
        self.assertEqual(derived["dram_row_management_command_pct"], 50.0)
        self.assertEqual(derived["dram_row_commands_per_column"], 2.0)

    def test_empty_counters_do_not_divide_by_zero(self):
        metrics = {
            "dram_row_commands_issued": 0,
            "dram_row_column_commands": 0,
            "dram_row_reuse_hits": 0,
            "dram_row_auto_precharge_stops": 0,
            "dram_row_activate_commands": 0,
            "dram_row_precharge_commands": 0,
        }

        derived = analysis.dram_row_metrics(metrics)
        self.assertTrue(all(value == "" for value in derived.values()))


class DRAMAdapterMetricsTest(unittest.TestCase):
    def test_redundant_prediction_avoidance_sums_across_l2_slices(self):
        metrics = (
            "dram_adapter_redundant_predictions_avoided",
            "dram_adapter_unused_predictions",
            "dram_adapter_pending_predictions",
            "dram_adapter_buffer_bank_stalls",
        )
        for metric in metrics:
            self.assertIn(metric, analysis.SUM_METRICS)
            self.assertNotIn(metric, analysis.MAX_METRICS)
            self.assertNotIn(metric, analysis.CONFIG_METRICS)

    def test_prediction_outcomes_balance_with_pending_state(self):
        metrics = {
            "dram_adapter_predictions": 20,
            "dram_adapter_inflight_hits": 4,
            "dram_adapter_buffer_hits": 7,
            "dram_adapter_unused_predictions": 6,
            "dram_adapter_pending_predictions": 3,
        }

        self.assertEqual(
            analysis.dram_adapter_prediction_accounting(metrics, True), 0
        )
        self.assertEqual(
            analysis.dram_adapter_prediction_accounting(metrics, False), ""
        )


class RemotePageShadowMetricsTest(unittest.TestCase):
    def test_bounded_shadow_uses_capacity_peak_and_eviction_reductions(self):
        self.assertIn(
            "remote_reuse_page_shadow_capacity", analysis.CONFIG_METRICS
        )
        self.assertIn(
            "remote_reuse_page_shadow_peak_entries", analysis.MAX_METRICS
        )
        self.assertIn(
            "remote_reuse_page_shadow_evictions", analysis.SUM_METRICS
        )


class StrictWarningClassificationTest(unittest.TestCase):
    def test_bounded_window_and_no_remote_are_informational(self):
        warnings = [
            "bounded max-wg window; application phase may be incomplete",
            "no remote read opportunity",
        ]

        self.assertFalse(analysis.has_fatal_warning(warnings))

    def test_validated_sampled_instruction_difference_is_informational(self):
        self.assertFalse(analysis.has_fatal_warning([
            "sampled detailed instruction-count mismatch; complete per-GPU "
            "WG/WF signatures match"
        ]))

    def test_balance_or_work_mismatch_is_fatal(self):
        self.assertTrue(analysis.has_fatal_warning(["fanout_balance=1"]))
        self.assertTrue(
            analysis.has_fatal_warning(["CU instruction-count mismatch"])
        )


class RDMAPipelineMetricsTest(unittest.TestCase):
    def test_line_capacity_stalls_sum_while_occupancy_takes_peak(self):
        self.assertIn("remote_line_entry_full_stalls", analysis.SUM_METRICS)
        self.assertNotIn("remote_line_entry_full_stalls", analysis.MAX_METRICS)
        self.assertIn("remote_waiter_entry_full_stalls", analysis.SUM_METRICS)
        self.assertIn("remote_peak_waiter_entries", analysis.MAX_METRICS)
        self.assertIn("remote_config_waiter_entries", analysis.CONFIG_METRICS)
        self.assertIn("remote_peak_line_entries", analysis.MAX_METRICS)
        self.assertIn("remote_owner_peak_child_lines", analysis.MAX_METRICS)
        self.assertIn(
            "remote_owner_child_line_full_stalls", analysis.SUM_METRICS
        )
        self.assertIn(
            "remote_config_owner_child_lines", analysis.CONFIG_METRICS
        )

    def test_reports_capacity_pressure_without_relabeling_traffic(self):
        metrics = {
            "rdma_max_outstanding": 64,
            "rdma_peak_outstanding": 32,
            "rdma_requester_peak_outstanding": 16,
            "rdma_owner_peak_outstanding": 8,
            "rdma_outstanding_full_stalls": 200,
            "rdma_requester_outstanding_full_stalls": 150,
            "rdma_owner_outstanding_full_stalls": 50,
            "rdma_pipeline_wait_cycles": 300,
            "remote_logical_reads": 100,
        }

        derived = analysis.rdma_pipeline_metrics(metrics)
        self.assertEqual(
            derived["rdma_peak_outstanding_utilization_pct"], 50.0
        )
        self.assertEqual(
            derived["rdma_requester_peak_outstanding_utilization_pct"], 25.0
        )
        self.assertEqual(
            derived["rdma_owner_peak_outstanding_utilization_pct"], 12.5
        )
        self.assertEqual(derived["rdma_requester_full_stall_share_pct"], 75.0)
        self.assertEqual(derived["rdma_owner_full_stall_share_pct"], 25.0)

    def test_disabled_or_legacy_metrics_stay_empty(self):
        metrics = {
            "rdma_max_outstanding": "",
            "rdma_peak_outstanding": 0,
            "rdma_requester_peak_outstanding": 0,
            "rdma_owner_peak_outstanding": 0,
            "rdma_outstanding_full_stalls": 0,
            "rdma_requester_outstanding_full_stalls": 0,
            "rdma_owner_outstanding_full_stalls": 0,
            "rdma_pipeline_wait_cycles": 0,
            "remote_logical_reads": 0,
        }

        derived = analysis.rdma_pipeline_metrics(metrics)
        self.assertTrue(all(value == "" for value in derived.values()))


class RemoteReuseSafetyMetricsTest(unittest.TestCase):
    def test_sums_page_false_positives_and_uncacheable_history_skips(self):
        self.assertIn(
            "remote_reuse_page_filter_false_positives",
            analysis.SUM_METRICS,
        )
        self.assertIn(
            "remote_reuse_write_uncacheable_skips",
            analysis.SUM_METRICS,
        )
        self.assertNotIn(
            "remote_reuse_page_filter_false_positives",
            analysis.MAX_METRICS,
        )
        self.assertNotIn(
            "remote_reuse_write_uncacheable_skips",
            analysis.MAX_METRICS,
        )


class ComponentLatencyMetricsTest(unittest.TestCase):
    def test_weights_component_averages_by_request_count(self):
        rows = [
            {
                "where": "GPU[0].SA[0].L1VCache[0]",
                "what": "req_average_latency",
                "value": "1e-8",
            },
            {
                "where": "GPU[0].SA[0].L1VCache[0]",
                "what": "read-hit",
                "value": "90",
            },
            {
                "where": "GPU[0].SA[0].L1VCache[1]",
                "what": "req_average_latency",
                "value": "3e-8",
            },
            {
                "where": "GPU[0].SA[0].L1VCache[1]",
                "what": "read-miss",
                "value": "10",
            },
        ]

        metrics = analysis.component_latency_metrics(rows)
        self.assertAlmostEqual(metrics["l1v_req_avg_latency_ns"], 12.0)
        self.assertEqual(metrics["l2_req_avg_latency_ns"], "")

    def test_weights_dram_and_keeps_rdma_generic(self):
        rows = [
            {
                "where": "GPU[0].DRAM[0]",
                "what": "read_avg_latency",
                "value": "4e-8",
            },
            {
                "where": "GPU[0].DRAM[0]",
                "what": "read_trans_count",
                "value": "5",
            },
            {
                "where": "GPU[0].RDMA",
                "what": "req_average_latency",
                "value": "2e-8",
            },
            {
                "where": "GPU[0].RDMA",
                "what": "incoming_trans_count",
                "value": "3",
            },
            {
                "where": "GPU[0].RDMA",
                "what": "outgoing_trans_count",
                "value": "2",
            },
        ]

        metrics = analysis.component_latency_metrics(rows)
        self.assertAlmostEqual(metrics["dram_read_avg_latency_ns"], 40.0)
        self.assertAlmostEqual(
            metrics["rdma_generic_req_avg_latency_ns"], 20.0
        )


if __name__ == "__main__":
    unittest.main()
