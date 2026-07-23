import csv
import subprocess
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory


ROOT = Path(__file__).resolve().parent
SCRIPT = ROOT / "analyze_pure64_local_frontier.py"


def write_metrics(path: Path, driver_time: float, metrics: dict[str, float]):
    with path.open("w", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=("where", "what", "value"))
        writer.writeheader()
        writer.writerow({"where": "Driver", "what": "total_time", "value": driver_time})
        for name, value in metrics.items():
            writer.writerow({"where": "System", "what": name, "value": value})


def append_metric(path: Path, where: str, what: str, value: float):
    with path.open("a", newline="", encoding="utf-8") as stream:
        writer = csv.DictWriter(stream, fieldnames=("where", "what", "value"))
        writer.writerow({"where": where, "what": what, "value": value})


class Pure64LocalFrontierAnalysisTest(unittest.TestCase):
    def test_reports_net_work_latency_and_pollution_separately(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            write_metrics(
                root / "baseline_aes_baseline_metrics.csv",
                100,
                {
                    "dram_physical_read_accesses": 1000,
                    "dram_physical_write_accesses": 500,
                    "l2_to_dram_64b_requests": 100,
                    "l2_demand_read_latency_samples": 10,
                    "l2_demand_read_latency_total_ns": 100,
                    "l2_demand_read_latency_max_ns": 20,
                    "l2_mshr_full_stall_cycles": 50,
                },
            )
            write_metrics(
                root / "baseline_aes_m1_metrics.csv",
                80,
                {
                    "dram_physical_read_accesses": 900,
                    "dram_physical_write_accesses": 500,
                    "l2_to_dram_64b_requests": 95,
                    "l2_demand_read_latency_samples": 10,
                    "l2_demand_read_latency_total_ns": 80,
                    "l2_demand_read_latency_max_ns": 15,
                    "l2_mshr_full_stall_cycles": 40,
                    "typed_filter_slots": 1000,
                    "typed_filter_resident_peak_occupancy": 10,
                    "l2_resident_filter_queries": 100,
                    "l2_resident_filter_negatives": 80,
                    "l2_resident_filter_read_bypasses": 80,
                    "filter_prefetch_candidates": 40,
                    "filter_prefetch_real_demands": 100,
                    "filter_prefetch_issued": 20,
                    "filter_prefetch_outstanding": 2,
                    "filter_prefetch_fills": 14,
                    "filter_prefetch_redundant_races": 4,
                    "filter_prefetch_useful": 12,
                    "filter_prefetch_timely": 9,
                    "filter_prefetch_late": 3,
                    "filter_prefetch_late_after_dram_issue": 2,
                    "filter_prefetch_demand_won_races": 4,
                    "filter_prefetch_additional_dram_reads": 20,
                    "filter_prefetch_unused_evictions": 3,
                    "filter_prefetch_unused_reset_retirements": 2,
                    "filter_prefetch_peak_prefetch_only_lines": 4,
                    "filter_prefetch_predictor_lookahead_total": 60,
                    "filter_prefetch_predictor_lookahead_max": 3,
                },
            )
            append_metric(
                root / "baseline_aes_baseline_metrics.csv",
                "L2[1]",
                "l2_demand_read_latency_max_ns",
                18,
            )
            append_metric(
                root / "baseline_aes_m1_metrics.csv",
                "L2[1]",
                "l2_demand_read_latency_max_ns",
                14,
            )
            append_metric(
                root / "baseline_aes_m1_metrics.csv",
                "L2[1]",
                "filter_prefetch_predictor_lookahead_max",
                2,
            )

            completed = subprocess.run(
                [sys.executable, str(SCRIPT), str(root), "--benchmarks=aes"],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertEqual(completed.returncode, 0, completed.stderr)
            with (root / "analysis/pure64_local_frontier.csv").open(
                newline="", encoding="utf-8"
            ) as stream:
                row = next(csv.DictReader(stream))

            self.assertEqual(float(row["m1_speedup"]), 1.25)
            self.assertEqual(float(row["l2_to_dram_demand_reads_m1"]), 75)
            self.assertEqual(float(row["l2_to_dram_prefetch_reads_m1"]), 20)
            self.assertEqual(float(row["l2_to_dram_total_reads_m1"]), 95)
            self.assertEqual(
                float(row["l2_to_dram_demand_reads_removed_pct"]), 25
            )
            self.assertEqual(
                float(row["l2_to_dram_total_reads_removed_pct"]), 5
            )
            self.assertEqual(
                float(row["l2_demand_read_latency_avg_ns_baseline"]), 10
            )
            self.assertEqual(float(row["l2_demand_read_latency_avg_ns_m1"]), 8)
            self.assertEqual(float(row["l2_demand_read_latency_delta_pct"]), -20)
            self.assertEqual(
                float(row["l2_demand_read_latency_max_ns_baseline"]), 20
            )
            self.assertEqual(float(row["l2_demand_read_latency_max_ns_m1"]), 15)
            self.assertEqual(float(row["prefetch_max_lookahead"]), 3)
            self.assertEqual(float(row["prefetch_outstanding"]), 2)
            self.assertEqual(
                float(row["prefetch_candidate_coverage_pct"]), 40
            )
            self.assertEqual(float(row["prefetch_issue_coverage_pct"]), 20)
            self.assertEqual(float(row["prefetch_useful_coverage_pct"]), 12)
            self.assertEqual(float(row["prefetch_timely_coverage_pct"]), 9)
            self.assertEqual(float(row["prefetch_late_before_dram_issue"]), 1)
            self.assertEqual(float(row["prefetch_late_after_dram_issue"]), 2)
            self.assertEqual(float(row["prefetch_unused_evictions"]), 3)
            self.assertEqual(float(row["prefetch_peak_l2_capacity_pct"]), 0.8)

            report = (root / "analysis/PURE64_LOCAL_FRONTIER.md").read_text(
                encoding="utf-8"
            )
            self.assertIn("75 demand reads plus 20 prefetch reads", report)
            self.assertIn("3 runtime evictions", report)
            self.assertIn("2 remain outstanding", report)
            self.assertIn("40.000%/20.000%/12.000%/9.000%", report)
            self.assertIn("1 demand arrivals before prefetch DRAM issue", report)
            self.assertIn("10.000 ns to 8.000 ns", report)

    def test_rejects_prefetch_reads_exceeding_total_l2_dram_reads(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            write_metrics(
                root / "baseline_aes_baseline_metrics.csv",
                1,
                {"l2_to_dram_64b_requests": 1},
            )
            write_metrics(
                root / "baseline_aes_m1_metrics.csv",
                1,
                {
                    "l2_to_dram_64b_requests": 1,
                    "filter_prefetch_additional_dram_reads": 2,
                },
            )
            completed = subprocess.run(
                [sys.executable, str(SCRIPT), str(root), "--benchmarks=aes"],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn(
                "prefetch DRAM reads exceed total L2-to-DRAM reads",
                completed.stderr + completed.stdout,
            )

    def test_rejects_unbalanced_runtime_stop_prefetch_lifecycle(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            write_metrics(
                root / "baseline_aes_baseline_metrics.csv",
                1,
                {"l2_to_dram_64b_requests": 1},
            )
            write_metrics(
                root / "baseline_aes_m1_metrics.csv",
                1,
                {
                    "l2_to_dram_64b_requests": 1,
                    "filter_prefetch_issued": 5,
                    "filter_prefetch_fills": 2,
                    "filter_prefetch_redundant_races": 1,
                    "filter_prefetch_outstanding": 1,
                },
            )
            completed = subprocess.run(
                [sys.executable, str(SCRIPT), str(root), "--benchmarks=aes"],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(completed.returncode, 0)
            self.assertIn(
                "issued prefetches do not partition",
                completed.stderr + completed.stdout,
            )


if __name__ == "__main__":
    unittest.main()
