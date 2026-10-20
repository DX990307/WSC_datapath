#!/usr/bin/env python3

import csv
import json
import tempfile
import unittest
from pathlib import Path

import build_baseline_library as library


class BaselineLibraryTest(unittest.TestCase):
    def write_metrics(self, directory, benchmark, value):
        path = directory / f"baseline_{benchmark}_baseline_metrics.csv"
        with path.open("w", newline="", encoding="utf-8") as stream:
            writer = csv.DictWriter(stream, fieldnames=("where", "what", "value"))
            writer.writeheader()
            writer.writerow({"where": "Driver", "what": "total_time", "value": value})

    def test_builds_canonical_fourteen_cell_library_with_fallback(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            primary = root / "primary"
            fallback = root / "fallback"
            output = root / "library"
            primary.mkdir()
            fallback.mkdir()
            for index, benchmark in enumerate(library.BENCHMARKS):
                target = fallback if benchmark == "spmv" else primary
                source_name = "kmeans-reuse-smoke" if benchmark == "kmeans" else benchmark
                self.write_metrics(target, source_name, index + 1)

            rows = library.build_library([primary, fallback], output)

            self.assertEqual(len(rows), 14)
            self.assertTrue(
                (output / "baseline_spmv_baseline_metrics.csv").is_file()
            )
            self.assertTrue(
                (output / "baseline_kmeans_baseline_metrics.csv").is_file()
            )
            self.assertTrue(
                (output / "baseline_kmeans-reuse-smoke_baseline_metrics.csv").is_file()
            )
            manifest = json.loads(
                (output / "BASELINE_LIBRARY.json").read_text(encoding="utf-8")
            )
            self.assertEqual(manifest["benchmark_count"], 14)
            self.assertEqual(
                manifest["formal_compatibility"],
                "unverified_missing_command_metadata",
            )


if __name__ == "__main__":
    unittest.main()
