import csv
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from analyze_cupath_controller_balance import read_components, summarize


class ControllerBalanceTest(unittest.TestCase):
    def test_component_distribution_is_preserved(self):
        with TemporaryDirectory() as directory:
            path = Path(directory) / "metrics.csv"
            with path.open("w", newline="", encoding="utf-8") as stream:
                writer = csv.writer(stream)
                writer.writerow(["id", "where", "what", "value"])
                writer.writerow([0, "Driver", "total_time", 1e-6])
                writer.writerow([1, "GPU[0].DRAM[0]", "dram_row_column_commands", 100])
                writer.writerow([2, "GPU[0].DRAM[0]", "dram_row_max_queue_age_cycles", 7])
                writer.writerow([3, "GPU[0].DRAM[1]", "dram_row_column_commands", 300])
                writer.writerow([4, "GPU[0].DRAM[1]", "dram_row_max_queue_age_cycles", 9])
                writer.writerow([5, "GPU[0].L2[0]", "filter_prefetch_candidates", 10])
                writer.writerow([6, "GPU[0].L2[0]", "filter_prefetch_issued", 8])
                writer.writerow([7, "GPU[0].L2[1]", "filter_prefetch_candidates", 30])
            driver_time, components = read_components(path)
            result = summarize(driver_time, components)
            self.assertEqual(result["dram_instances"], 2)
            self.assertEqual(result["dram_column_commands"], 400)
            self.assertEqual(result["dram_max_queue_age_cycles"], 9)
            self.assertAlmostEqual(result["dram_column_command_cv"], 0.5)
            self.assertEqual(result["prefetch_candidates"], 40)
            self.assertEqual(result["prefetch_issued"], 8)
            self.assertAlmostEqual(result["prefetch_candidate_cv"], 0.5)


if __name__ == "__main__":
    unittest.main()
