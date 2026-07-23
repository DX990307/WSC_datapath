import csv
import hashlib
import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from plot_cupath_typed_ablation import (
    CONFIGS,
    REQUIRED_BASELINE_METRICS,
    REQUIRED_COMPLETE_METRICS,
    REQUIRED_EXECUTION_METRICS,
    M1_ADAPTIVE_PAIR_METRICS,
    M1_PREFETCH_METRICS,
    REQUIRED_M1_METRICS,
    REQUIRED_M2_METRICS,
    REQUIRED_M3_METRICS,
    WORKLOADS,
    read_elapsed_seconds,
    read_return_code,
    validate_complete_metric_coverage,
    write_config_attribution_table,
    write_m1_attribution_table,
    write_prefetch_table,
    write_runtime_table,
    write_workload_footprint_table,
    write_work_table,
)
from plot_cupath_work_reduction import reductions
from analyze_cupath_formal import (
    audit_completed_results,
    audit_experiment_metadata,
    audit_runtime_stop_values,
)


class WorkReductionDenominatorTest(unittest.TestCase):
    def test_remote_stages_use_complete_logical_requests(self):
        values, counts = reductions({
            "l2_tag_lookups_skipped": "25",
            "l2_resident_filter_queries": "100",
            # This deliberately differs from the Complete request stream. It
            # must not be the denominator for a stage-local Complete counter.
            "baseline_remote_reads": "10",
            "complete_remote_logical_reads": "100",
            "remote_duplicate_reads_eliminated": "20",
            "complete_remote_wire_lines": "40",
            "complete_remote_packets": "10",
            "repeated_wafer_traversals_eliminated": "70",
        })

        self.assertEqual(values, (25.0, 20.0, 75.0, 70.0))
        self.assertEqual(counts[1], (20.0, 100.0))
        self.assertEqual(counts[3], (70.0, 100.0))

    def test_double_counted_traversals_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "traversal reduction"):
            reductions({
                "complete_remote_logical_reads": "100",
                "repeated_wafer_traversals_eliminated": "101",
            })

    def test_zero_remote_exposure_is_not_missing_data(self):
        values, _ = reductions({
            "complete_remote_logical_reads": "0",
            "remote_duplicate_reads_eliminated": "0",
            "complete_remote_wire_lines": "0",
            "complete_remote_packets": "0",
            "repeated_wafer_traversals_eliminated": "0",
        })
        self.assertEqual(values[1:], (0.0, 0.0, 0.0))


class SimulatorRuntimeParsingTest(unittest.TestCase):
    def test_elapsed_time_is_converted_to_seconds(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            metric = root / "baseline_aes_complete_metrics.csv"
            metric.touch()
            (root / "baseline_aes_complete_out.stdout").write_text(
                "Return code: 0\nElapsed time: 1 day, 2:03:04.5\n",
                encoding="utf-8",
            )
            self.assertEqual(read_elapsed_seconds(metric), 93784.5)
            self.assertEqual(read_return_code(metric), 0)

    def test_runtime_table_normalizes_wall_time_by_simulated_time(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            paths = {}
            for config, elapsed in (("baseline", 10), ("m1", 15)):
                metric = root / f"baseline_aes_{config}_metrics.csv"
                metric.touch()
                metric.with_name(
                    f"baseline_aes_{config}_out.stdout"
                ).write_text(
                    f"Return code: 0\nElapsed time: 0:00:{elapsed}\n",
                    encoding="utf-8",
                )
                paths[("aes", config)] = metric
            campaign = {
                ("aes", "baseline"): {"__driver_total_time": 2.0},
                ("aes", "m1"): {"__driver_total_time": 1.0},
            }
            output = root / "runtime.csv"
            write_runtime_table(output, paths, campaign)
            with output.open(newline="", encoding="utf-8") as stream:
                aes = next(csv.DictReader(stream))
            self.assertEqual(aes["m1_wall_over_baseline"], "1.5")
            self.assertEqual(aes["baseline_wall_per_simulated_s"], "5.0")
            self.assertEqual(aes["m1_wall_per_simulated_s"], "15.0")
            self.assertEqual(
                aes["m1_normalized_host_cost_over_baseline"], "3.0"
            )


class WorkloadFootprintTest(unittest.TestCase):
    def test_footprint_comes_from_reported_page_count(self):
        with TemporaryDirectory() as directory:
            output = Path(directory) / "footprints.csv"
            write_workload_footprint_table(output, {
                ("aes", "baseline"): {
                    "total_wg_count": 78600.0,
                    "allocation_page_size": 4096.0,
                    "allocation_workload_allocated_pages": 262146.0,
                    "allocation_overall_allocated_pages": 262290.0,
                },
            })
            with output.open(newline="", encoding="utf-8") as stream:
                aes = next(csv.DictReader(stream))
            self.assertEqual(aes["observed_workgroups"], "78600.0")
            self.assertEqual(aes["workload_allocated_pages"], "262146.0")
            self.assertAlmostEqual(
                float(aes["workload_footprint_mib"]), 1024.0078125
            )


class PhysicalDRAMWorkExportTest(unittest.TestCase):
    def test_complete_table_preserves_reads_and_writes(self):
        with TemporaryDirectory() as directory:
            output = Path(directory) / "prefetch.csv"
            write_prefetch_table(output, {
                ("aes", "baseline"): {
                    "dram_physical_read_accesses": 10.0,
                    "dram_physical_write_accesses": 4.0,
                },
                ("aes", "complete"): {
                    "dram_physical_read_accesses": 8.0,
                    "dram_physical_write_accesses": 3.0,
                    "filter_prefetch_timely": 7.0,
                    "filter_prefetch_outstanding": 2.0,
                    "filter_prefetch_fills": 11.0,
                },
            })
            with output.open(newline="", encoding="utf-8") as stream:
                aes = next(csv.DictReader(stream))
            self.assertEqual(aes["baseline_dram_physical_read_accesses"], "10.0")
            self.assertEqual(aes["complete_dram_physical_read_accesses"], "8.0")
            self.assertEqual(aes["baseline_dram_physical_write_accesses"], "4.0")
            self.assertEqual(aes["complete_dram_physical_write_accesses"], "3.0")
            self.assertEqual(aes["filter_prefetch_timely"], "7.0")
            self.assertEqual(aes["filter_prefetch_outstanding"], "2.0")
            self.assertEqual(aes["filter_prefetch_fills"], "11.0")


class RequiredMetricCoverageTest(unittest.TestCase):
    def complete_campaign(self):
        campaign = {
            (benchmark, config): {
                name: 0.0 for name in REQUIRED_EXECUTION_METRICS
            }
            for benchmark, _, _ in WORKLOADS
            for config, _, _ in CONFIGS
        }
        for benchmark, _, _ in WORKLOADS:
            campaign[(benchmark, "baseline")].update({
                name: 0.0 for name in REQUIRED_BASELINE_METRICS
            })
            campaign[(benchmark, "m1")].update({
                name: 0.0 for name in REQUIRED_M1_METRICS
            })
            campaign[(benchmark, "m2")].update({
                name: 0.0 for name in REQUIRED_M2_METRICS
            })
            campaign[(benchmark, "m3")].update({
                name: 0.0 for name in REQUIRED_M3_METRICS
            })
            campaign[(benchmark, "complete")].update({
                name: 0.0 for name in REQUIRED_COMPLETE_METRICS
            })
        return campaign

    def test_partial_campaign_does_not_require_formal_metric_coverage(self):
        validate_complete_metric_coverage({})

    def test_complete_campaign_rejects_missing_raw_reporter(self):
        campaign = self.complete_campaign()
        del campaign[("aes", "complete")]["remote_prefetch_piggyback_lines"]
        with self.assertRaisesRegex(
            ValueError, "remote_prefetch_piggyback_lines"
        ):
            validate_complete_metric_coverage(campaign)

    def test_complete_campaign_rejects_missing_workload_footprint(self):
        campaign = self.complete_campaign()
        del campaign[("aes", "m2")]["allocation_workload_allocated_pages"]
        with self.assertRaisesRegex(ValueError, "execution metrics"):
            validate_complete_metric_coverage(campaign)

    def test_complete_campaign_rejects_missing_m2_reporter(self):
        campaign = self.complete_campaign()
        del campaign[("aes", "m2")]["remote_prefetch_piggyback_lines"]
        with self.assertRaisesRegex(
            ValueError, "remote_prefetch_piggyback_lines"
        ):
            validate_complete_metric_coverage(campaign)

    def test_complete_campaign_rejects_missing_m3_reporter(self):
        campaign = self.complete_campaign()
        del campaign[("aes", "m3")]["remote_requester_l2_hits"]
        with self.assertRaisesRegex(ValueError, "remote_requester_l2_hits"):
            validate_complete_metric_coverage(campaign)

    def test_complete_campaign_accepts_full_raw_metric_coverage(self):
        validate_complete_metric_coverage(self.complete_campaign())

    def test_feedback_width_and_pollution_reporters_are_required(self):
        self.assertTrue(set(M1_PREFETCH_METRICS) <= REQUIRED_COMPLETE_METRICS)
        self.assertTrue({
            "remote_logical_reads",
            "remote_wire_lines",
            "remote_demand_wire_lines",
            "remote_prefetch_wire_lines",
            "remote_duplicate_reads",
            "remote_l2_logical_responses",
            "remote_logical_read_latency_samples",
            "remote_batch_queue_wait_samples",
            "remote_pre_network_wait_samples",
            "remote_probe_latency_samples",
            "dram_row_column_commands",
            "filter_prefetch_candidate_busy_drops",
            "filter_prefetch_wrong_slice_drops",
            "filter_prefetch_demand_priority_drops",
            "remote_prefetch_predictor_evidence_one",
            "remote_prefetch_stride_negative",
            "remote_requester_issue_width_stalls",
            "remote_response_fanout_width_stalls",
            "remote_owner_issue_width_stalls",
            "remote_owner_response_width_stalls",
            "remote_requester_l2_unused_fills",
            "remote_requester_l2_current_lines",
            "remote_requester_l2_unused_pattern_retirements",
            "remote_speculative_invalid_only_drops",
        } <= REQUIRED_COMPLETE_METRICS)
        self.assertTrue(set(M1_ADAPTIVE_PAIR_METRICS) <= REQUIRED_M1_METRICS)
        self.assertTrue({
            "adaptive_pair_predictions",
            "adaptive_pair_inflight_hits",
            "adaptive_pair_buffer_hits",
            "adaptive_pair_prefetch_unused",
            "adaptive_pair_wide_128b_reads",
            "adaptive_pair_filter_candidates",
            "adaptive_pair_filter_lookups",
            "adaptive_pair_resident_exact_suppressions",
            "adaptive_pair_pending_exact_suppressions",
            "l2_to_dram_64b_requests",
            "l2_demand_read_latency_samples",
            "l2_demand_read_latency_total_ns",
            "l2_demand_read_latency_max_ns",
        } <= REQUIRED_M1_METRICS)
        self.assertTrue({
            "remote_logical_reads",
            "remote_duplicate_reads",
            "remote_prefetch_no_existing_batch_drops",
            "remote_prefetch_piggyback_lines",
            "remote_pre_send_merges",
            "remote_requester_issue_width_stalls",
            "remote_owner_response_width_stalls",
        } <= REQUIRED_M2_METRICS)
        self.assertTrue({
            "remote_l2_one_touch_probe_bypasses",
            "remote_l2_probe_hits",
            "remote_l2_probe_misses",
            "remote_requester_l2_hits",
            "remote_requester_l2_unused_fills",
            "remote_fill_displaced_local_clean",
            "remote_requester_l2_peak_lines",
        } <= REQUIRED_M3_METRICS)


class RemoteWireAccountingTest(unittest.TestCase):
    def test_prefetch_wire_lines_are_not_demand_dedup_denominator(self):
        with TemporaryDirectory() as directory:
            output = Path(directory) / "work.csv"
            campaign = {
                ("aes", "baseline"): {
                    "__driver_total_time": 1.0,
                    "rdma_observed_remote_reads": 100.0,
                    "l2_miss_to_dram_issue_samples": 2.0,
                    "l2_miss_to_dram_issue_total_ns": 20.0,
                    "l2_miss_to_dram_issue_max_ns": 15.0,
                },
                ("aes", "complete"): {
                    "__driver_total_time": 1.0,
                    "remote_logical_reads": 100.0,
                    "remote_demand_wire_lines": 80.0,
                    "remote_prefetch_wire_lines": 30.0,
                    "remote_wire_lines": 110.0,
                    "remote_duplicate_reads": 20.0,
                    "remote_l2_one_touch_probe_bypasses": 40.0,
                    "remote_l2_probe_hits": 50.0,
                    "remote_l2_probe_misses": 10.0,
                    "remote_logical_read_latency_samples": 4.0,
                    "remote_logical_read_latency_total_ns": 100.0,
                    "remote_logical_read_latency_max_ns": 40.0,
                    "remote_batch_queue_wait_samples": 2.0,
                    "remote_batch_queue_wait_total_ns": 6.0,
                    "remote_requester_l2_peak_lines": 7.0,
                    "remote_fill_displaced_local_clean": 0.0,
                    "l2_miss_to_dram_issue_samples": 1.0,
                    "l2_miss_to_dram_issue_total_ns": 9.0,
                    "l2_miss_to_dram_issue_max_ns": 9.0,
                    "l2_fast_miss_to_dram_issue_samples": 3.0,
                    "l2_fast_miss_to_dram_issue_total_ns": 6.0,
                    "l2_fast_miss_to_dram_issue_max_ns": 3.0,
                },
            }
            write_work_table(output, campaign)
            with output.open(newline="", encoding="utf-8") as stream:
                rows = {
                    row["benchmark"]: row for row in csv.DictReader(stream)
                }
            aes = rows["aes"]
            self.assertEqual(aes["complete_remote_wire_lines"], "110.0")
            self.assertEqual(aes["complete_remote_demand_wire_lines"], "80.0")
            self.assertEqual(aes["complete_remote_prefetch_wire_lines"], "30.0")
            self.assertEqual(aes["remote_cacheline_transactions_eliminated"], "20.0")
            self.assertEqual(aes["remote_wire_lines_reduction_pct"], "20.0")
            self.assertEqual(aes["remote_l2_probe_bypasses"], "40.0")
            self.assertEqual(aes["remote_l2_probe_hits"], "50.0")
            self.assertEqual(aes["remote_l2_probe_misses"], "10.0")
            self.assertEqual(aes["remote_l2_probe_elimination_pct"], "40.0")
            self.assertEqual(aes["remote_logical_read_latency_avg_ns"], "25.0")
            self.assertEqual(aes["remote_logical_read_latency_max_ns"], "40.0")
            self.assertEqual(aes["remote_batch_queue_wait_avg_ns"], "3.0")
            self.assertEqual(
                aes["baseline_l2_miss_to_dram_issue_avg_ns"], "10.0"
            )
            self.assertEqual(aes["complete_l2_combined_issue_samples"], "4.0")
            self.assertEqual(aes["complete_l2_combined_issue_avg_ns"], "3.75")
            self.assertEqual(aes["complete_l2_combined_issue_max_ns"], "9.0")


class M1AttributionTest(unittest.TestCase):
    def test_m1_counters_are_not_taken_from_complete(self):
        with TemporaryDirectory() as directory:
            output = Path(directory) / "m1.csv"
            campaign = {
                ("aes", "baseline"): {
                    "dram_physical_read_accesses": 10.0,
                    "l2_to_dram_64b_requests": 9.0,
                    "l2_demand_read_latency_samples": 2.0,
                    "l2_demand_read_latency_total_ns": 20.0,
                    "l2_demand_read_latency_max_ns": 15.0,
                    "l2_mshr_full_stall_cycles": 20.0,
                },
                ("aes", "m1"): {
                    "dram_physical_read_accesses": 11.0,
                    "l2_to_dram_64b_requests": 10.0,
                    "l2_demand_read_latency_samples": 2.0,
                    "l2_demand_read_latency_total_ns": 24.0,
                    "l2_demand_read_latency_max_ns": 18.0,
                    "l2_mshr_full_stall_cycles": 21.0,
                    "filter_prefetch_issued": 3.0,
                    "filter_prefetch_outstanding": 0.0,
                    "filter_prefetch_redundant_races": 1.0,
                    "filter_prefetch_fills": 2.0,
                    "filter_prefetch_additional_dram_reads": 2.0,
                    "filter_prefetch_controller_busy_drops": 7.0,
                    "filter_prefetch_predictor_evidence_two": 5.0,
                },
                ("aes", "complete"): {
                    "dram_physical_read_accesses": 99.0,
                    "l2_mshr_full_stall_cycles": 999.0,
                },
            }
            write_m1_attribution_table(output, campaign)
            with output.open(newline="", encoding="utf-8") as stream:
                rows = {
                    row["benchmark"]: row for row in csv.DictReader(stream)
                }
            aes = rows["aes"]
            self.assertEqual(aes["baseline_dram_physical_read_accesses"], "10.0")
            self.assertEqual(aes["m1_dram_physical_read_accesses"], "11.0")
            self.assertEqual(aes["baseline_l2_to_dram_64b_requests"], "9.0")
            self.assertEqual(aes["m1_l2_to_dram_64b_requests"], "10.0")
            self.assertEqual(aes["baseline_l2_demand_read_latency_total_ns"], "20.0")
            self.assertEqual(aes["m1_l2_demand_read_latency_total_ns"], "24.0")
            self.assertEqual(aes["m1_l2_mshr_full_stall_cycles"], "21.0")
            self.assertEqual(aes["m1_filter_prefetch_issued"], "3.0")
            self.assertEqual(aes["m1_filter_prefetch_outstanding"], "0.0")
            self.assertEqual(aes["m1_filter_prefetch_redundant_races"], "1.0")
            self.assertEqual(aes["m1_filter_prefetch_fills"], "2.0")
            self.assertEqual(
                aes["m1_filter_prefetch_additional_dram_reads"], "2.0"
            )
            self.assertEqual(
                aes["m1_filter_prefetch_controller_busy_drops"], "7.0"
            )
            self.assertEqual(
                aes["m1_filter_prefetch_predictor_evidence_two"], "5.0"
            )

    def test_standalone_stage_table_does_not_take_complete_counters(self):
        with TemporaryDirectory() as directory:
            output = Path(directory) / "m2.csv"
            campaign = {
                ("aes", "m2"): {
                    "remote_prefetch_piggyback_lines": 7.0,
                    "remote_duplicate_reads": 11.0,
                },
                ("aes", "complete"): {
                    "remote_prefetch_piggyback_lines": 99.0,
                    "remote_duplicate_reads": 101.0,
                },
            }
            write_config_attribution_table(
                output,
                campaign,
                "m2",
                (
                    "remote_prefetch_piggyback_lines",
                    "remote_duplicate_reads",
                ),
            )
            with output.open(newline="", encoding="utf-8") as stream:
                aes = next(csv.DictReader(stream))
            self.assertEqual(aes["remote_prefetch_piggyback_lines"], "7.0")
            self.assertEqual(aes["remote_duplicate_reads"], "11.0")


class FormalMetadataAuditTest(unittest.TestCase):
    def make_campaign(self, root: Path):
        binary = root / "frozen"
        binary.write_bytes(b"one frozen binary")
        common = [
            "-timing", "-num-memory-banks=4", "-l1v-mshr-entries=16",
            "-l1v-max-concurrent-trans=16",
            "-bandwidth=48", "-switch-latency=32",
            "-rdma-pipeline-width=8", "-rdma-pipeline-latency=10",
            "-rdma-max-outstanding=64", "-max-wg=76800", "-sampled",
            "-branch-sampled", "-kernel-sampled", "-typed-filter-mode=cuckoo",
            "-typed-filter-slots-per-bucket=4",
            "-typed-filter-fingerprint-bits=13",
            "-typed-filter-lookup-latency=1", "-typed-filter-lookup-width=16",
            "-typed-filter-update-latency=1", "-typed-filter-update-width=16",
            "-prefetch-predictor-entries=256",
            "-remote-data-path-batch-lines=8", "-remote-data-path-batches=64",
            "-l2-fill-forwarding-enable=false",
            "-dram-row-continuation-enable=false",
            "-l2-prefetch-predictor-only=false", "-l2-prefetch-ungated=false",
            "-magic-memory-copy", "-report-all", "-disable-servers",
            "-mmutlb-lookup-latency=80",
        ]
        mechanisms = {
            "baseline": (False, False, False, False, False, False, False),
            "m1": (False, True, False, False, False, False, False),
            "m2": (False, False, True, True, True, False, True),
            "m3": (False, False, True, False, False, True, False),
            "complete": (False, True, True, True, True, True, True),
        }
        fields = (
            "l2-resident-filter-enable", "l2-filter-prefetch-enable",
            "remote-data-path-enable", "remote-data-path-dedup-enable",
            "remote-data-path-batching-enable", "remote-data-path-l2-enable",
            "remote-filter-prefetch-enable",
        )
        benchmarks = (
            "aes", "bitonicsort", "fastwalshtransform", "fft", "fir", "relu",
            "simpleconvolution", "floydwarshall", "kmeans",
            "matrixmultiplication", "pagerank", "im2col", "matrixtranspose",
            "spmv",
        )
        experiments = []
        for benchmark in benchmarks:
            for config, values in mechanisms.items():
                command = [str(binary), f"-benchmark={benchmark}", *common]
                command.extend(
                    f"-{field}={str(value).lower()}"
                    for field, value in zip(fields, values)
                )
                command.append(
                    f"-metric-file-name={root}/baseline_{benchmark}_{config}_metrics"
                )
                experiments.append({
                    "benchmark": benchmark,
                    "configuration": config,
                    "target": "baseline",
                    "command": command,
                })
        (root / "EXPERIMENT_METADATA.json").write_text(
            json.dumps({
                "experiment_count": 70,
                "launcher": {"max_workers": 14},
                "experiments": experiments,
            }),
            encoding="utf-8",
        )
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        (root / "EXPERIMENT_BINARIES.json").write_text(
            json.dumps({"sha256_by_target": {"baseline": digest}}),
            encoding="utf-8",
        )
        return binary, digest, experiments

    def test_fixed_14_by_5_grid_and_binary_hash_are_accepted(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            binary, digest, _ = self.make_campaign(root)
            self.assertEqual(
                audit_experiment_metadata(root), (str(binary), digest)
            )

    def test_changed_fixed_flag_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            _, _, experiments = self.make_campaign(root)
            experiments[0]["command"].remove("-l1v-mshr-entries=16")
            experiments[0]["command"].append("-l1v-mshr-entries=32")
            (root / "EXPERIMENT_METADATA.json").write_text(
                json.dumps({
                    "experiment_count": 70,
                    "launcher": {"max_workers": 14},
                    "experiments": experiments,
                }),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(SystemExit, "missing fixed flags"):
                audit_experiment_metadata(root)

    def test_unexpected_frozen_hash_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_campaign(root)
            with self.assertRaisesRegex(SystemExit, "unexpected formal binary"):
                audit_experiment_metadata(
                    root, expected_sha256="0" * 64
                )

    def test_formal_memory_reserve_is_audited(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_campaign(root)
            metadata_path = root / "EXPERIMENT_METADATA.json"
            metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
            metadata["launcher"].update({
                "max_workers": 8,
                "memory_reserve_gib": 50.0,
                "memory_per_worker_gib": 24.5,
                "mem_available_gib_at_launch": 247.0,
                "memory_worker_cap": 8,
            })
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            audit_experiment_metadata(
                root,
                expected_workers=8,
                expected_memory_reserve_gib=50.0,
                expected_memory_per_worker_gib=24.5,
            )
            metadata["launcher"]["memory_reserve_gib"] = 40.0
            metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "memory-reserve mismatch"):
                audit_experiment_metadata(
                    root,
                    expected_workers=8,
                    expected_memory_reserve_gib=50.0,
                    expected_memory_per_worker_gib=24.5,
                )

    def test_force_local_formal_cell_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            _, _, experiments = self.make_campaign(root)
            experiments[0]["command"].append("-force-local-data-access")
            (root / "EXPERIMENT_METADATA.json").write_text(
                json.dumps({
                    "experiment_count": 70,
                    "launcher": {"max_workers": 14},
                    "experiments": experiments,
                }),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(SystemExit, "forbidden mechanism"):
                audit_experiment_metadata(root)

    def test_unexpected_benchmark_specific_formal_flag_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            _, _, experiments = self.make_campaign(root)
            experiments[0]["command"].append("-aes-special-case=1")
            (root / "EXPERIMENT_METADATA.json").write_text(
                json.dumps({
                    "experiment_count": 70,
                    "launcher": {"max_workers": 14},
                    "experiments": experiments,
                }),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(SystemExit, "unexpected formal flags"):
                audit_experiment_metadata(root)


class FormalCompletionAuditTest(unittest.TestCase):
    def make_results(self, root: Path):
        paths = []
        for benchmark, _, _ in WORKLOADS:
            for config, _, _ in CONFIGS:
                prefix = f"baseline_{benchmark}_{config}_metrics"
                metrics = (root / f"{prefix}.csv").resolve()
                mapping = (root / f"{prefix}_wg_mapping.json").resolve()
                metrics.touch()
                mapping.write_text("{}", encoding="utf-8")
                result_path = root / f"baseline_{benchmark}_{config}_result.json"
                result_path.write_text(
                    json.dumps({
                        "target": "baseline",
                        "benchmark": benchmark,
                        "configuration": config,
                        "metrics": str(metrics),
                        "wg_mapping": {"path": str(mapping)},
                        "returncode": 0,
                        "simulator_returncode": 0,
                        "success": True,
                    }),
                    encoding="utf-8",
                )
                paths.append(result_path)
        return paths

    def test_exact_successful_70_cell_grid_is_accepted(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_results(root)
            audit_completed_results(root)

    def test_missing_result_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            paths = self.make_results(root)
            paths[0].unlink()
            with self.assertRaisesRegex(SystemExit, "69/70 result JSONs"):
                audit_completed_results(root)

    def test_nonzero_simulator_return_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            paths = self.make_results(root)
            result = json.loads(paths[0].read_text(encoding="utf-8"))
            result["success"] = False
            result["simulator_returncode"] = 2
            paths[0].write_text(json.dumps(result), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "failed formal result"):
                audit_completed_results(root)

    def test_misdirected_metric_artifact_is_rejected(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            paths = self.make_results(root)
            result = json.loads(paths[0].read_text(encoding="utf-8"))
            result["metrics"] = str((root / "wrong.csv").resolve())
            paths[0].write_text(json.dumps(result), encoding="utf-8")
            with self.assertRaisesRegex(SystemExit, "metric artifact mismatch"):
                audit_completed_results(root)


class FormalRuntimeStopAuditTest(unittest.TestCase):
    def test_accepts_natural_completion_below_max_wg(self):
        audit_runtime_stop_values(
            "matrixtranspose", "complete",
            32400, 76800, 32400, 1, 0, 32400, 0,
        )

    def test_rejects_partial_natural_completion(self):
        with self.assertRaisesRegex(SystemExit, "expected=32400"):
            audit_runtime_stop_values(
                "matrixtranspose", "complete",
                30000, 76800, 30000, 1, 0, 32400, 0,
            )

    def test_accepts_runtime_limit_for_larger_grid(self):
        audit_runtime_stop_values(
            "aes", "complete",
            76800, 76800, 76800, 1, 1, 1048576, 0,
        )


if __name__ == "__main__":
    unittest.main()
