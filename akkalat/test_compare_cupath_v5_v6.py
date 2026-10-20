import csv
import tempfile
import unittest
from pathlib import Path

from compare_cupath_v5_v6 import compare, write_outputs


class V5V6ComparisonTest(unittest.TestCase):
    def test_reports_methodological_excess(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            paths = []
            for name, complete in (("v5", 3.0), ("v6", 1.5)):
                path = root / f"{name}.csv"
                paths.append(path)
                with path.open("w", newline="", encoding="utf-8") as stream:
                    writer = csv.writer(stream)
                    writer.writerow([
                        "benchmark", "label", "m1_speedup", "m2_speedup",
                        "m3_speedup", "complete_speedup",
                    ])
                    for index in range(14):
                        benchmark = "fir" if index == 0 else f"b{index}"
                        label = "FIR" if index == 0 else f"B{index}"
                        writer.writerow([
                            benchmark, label, 1.0, 1.0, 1.0,
                            complete,
                        ])
            rows = compare(*paths)
            complete = next(
                row for row in rows
                if row["benchmark"] == "fir"
                and row["configuration"] == "complete"
            )
            self.assertEqual(complete["invalid_v5_excess_speedup"], 1.5)
            self.assertEqual(
                complete["invalid_excess_fraction_of_v5_gain"], 0.75
            )
            output = root / "output"
            write_outputs(output, rows)
            self.assertTrue(output.is_dir())
            report = (output / "CUPATH_V5_V6_COMPARISON.md").read_text(
                encoding="utf-8"
            )
            self.assertIn("Fourteen-workload geomean", report)
            self.assertIn("| COMPLETE | 3.0000x | 1.5000x | 0.5000x |", report)
            self.assertIn("Largest invalid-V5 excesses", report)


if __name__ == "__main__":
    unittest.main()
