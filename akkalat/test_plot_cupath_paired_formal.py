import csv
import tempfile
import unittest
from pathlib import Path

import plot_cupath_paired_formal as plotter


class PairedFormalPlotTest(unittest.TestCase):
    def write_grid(self, path, omit=None, duplicate=None):
        with path.open("w", newline="", encoding="utf-8") as stream:
            writer = csv.DictWriter(
                stream,
                fieldnames=("benchmark", "config", "speedup_vs_baseline"),
            )
            writer.writeheader()
            for benchmark, _ in plotter.WORKLOADS:
                for config, _, _ in plotter.CONFIGS:
                    cell = (benchmark, config)
                    if cell == omit:
                        continue
                    writer.writerow({
                        "benchmark": benchmark,
                        "config": config,
                        "speedup_vs_baseline": (
                            1.0 if config == "baseline" else 1.1
                        ),
                    })
                    if cell == duplicate:
                        writer.writerow({
                            "benchmark": benchmark,
                            "config": config,
                            "speedup_vs_baseline": 1.1,
                        })

    def test_reads_exact_grid_and_computes_geomean(self):
        with tempfile.TemporaryDirectory() as directory:
            summary = Path(directory) / "summary.csv"
            self.write_grid(summary)
            grid = plotter.read_strict_grid(summary)
            self.assertEqual(len(grid), 70)
            values = plotter.series_with_overall_geomean(grid, "complete")
            self.assertEqual(len(values), 15)
            self.assertAlmostEqual(values[-1], 1.1)

    def test_rejects_missing_or_duplicate_cells(self):
        with tempfile.TemporaryDirectory() as directory:
            summary = Path(directory) / "summary.csv"
            self.write_grid(summary, omit=("aes", "m1"))
            with self.assertRaisesRegex(ValueError, "exact 14x5"):
                plotter.read_strict_grid(summary)
            self.write_grid(summary, duplicate=("aes", "m1"))
            with self.assertRaisesRegex(ValueError, "duplicate"):
                plotter.read_strict_grid(summary)

    def test_writes_single_column_raster(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            summary = root / "summary.csv"
            output = root / "overall.png"
            self.write_grid(summary)
            plotter.plot(plotter.read_strict_grid(summary), output)
            self.assertTrue(output.is_file())
            self.assertGreater(output.stat().st_size, 1000)


if __name__ == "__main__":
    unittest.main()
