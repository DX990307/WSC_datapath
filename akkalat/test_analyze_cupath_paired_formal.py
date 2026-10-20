import hashlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import analyze_cupath_paired_formal as formal


class PairedFormalMetadataTest(unittest.TestCase):
    def make_campaign(self, root: Path):
        binary = root / "simulator"
        binary.write_bytes(b"paired-formal-binary")
        experiments = []
        for benchmark in formal.WORKLOADS:
            for config in formal.CONFIGS:
                metric = (
                    root / f"baseline_{benchmark}_{config}_metrics"
                ).resolve()
                command = [
                    str(binary.resolve()),
                    f"-benchmark={benchmark}",
                    *sorted(formal.FIXED_FLAGS),
                    *sorted(formal.expected_mechanism_tokens(config)),
                    f"-metric-file-name={metric}",
                ]
                experiments.append({
                    "target": "baseline",
                    "benchmark": benchmark,
                    "configuration": config,
                    "command": command,
                })
        (root / "EXPERIMENT_METADATA.json").write_text(json.dumps({
            "experiment_count": len(experiments),
            "launcher": {"max_workers": 12},
            "experiments": experiments,
        }), encoding="utf-8")
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        (root / "EXPERIMENT_BINARIES.json").write_text(json.dumps({
            "version": 1,
            "sha256_by_target": {"baseline": digest},
        }), encoding="utf-8")
        return experiments, digest

    def test_exact_full_workload_five_config_metadata_is_accepted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            _, digest = self.make_campaign(root)
            audit = formal.audit_experiment_metadata(root, digest)
            self.assertEqual(audit["cell_count"], 70)
            self.assertEqual(audit["binary_sha256"], digest)

    def test_positive_max_wg_and_old_prefetch_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            experiments, _ = self.make_campaign(root)
            experiments[0]["command"].append("-max-wg=1")
            metadata = json.loads(
                (root / "EXPERIMENT_METADATA.json").read_text()
            )
            metadata["experiments"] = experiments
            (root / "EXPERIMENT_METADATA.json").write_text(json.dumps(metadata))
            with self.assertRaisesRegex(ValueError, "max-wg"):
                formal.audit_experiment_metadata(root)

            experiments[0]["command"].remove("-max-wg=1")
            old = "-l2-filter-prefetch-enable=false"
            experiments[0]["command"].remove(old)
            experiments[0]["command"].append(
                "-l2-filter-prefetch-enable=true"
            )
            metadata["experiments"] = experiments
            (root / "EXPERIMENT_METADATA.json").write_text(json.dumps(metadata))
            with self.assertRaisesRegex(ValueError, "mechanism matrix"):
                formal.audit_experiment_metadata(root)

    def test_group_geomeans_use_all_declared_workloads(self):
        rows = []
        speedups = {
            "baseline": 1.0,
            "m1": 1.1,
            "m2": 1.2,
            "m3": 1.3,
            "complete": 1.5,
        }
        for benchmark in formal.WORKLOADS:
            for config in formal.CONFIGS:
                rows.append({
                    "benchmark": benchmark,
                    "config": config,
                    "speedup_vs_baseline": speedups[config],
                })
        groups = formal.group_rows(rows)
        overall = {
            row["config"]: row
            for row in groups if row["group"] == "Overall"
        }
        self.assertEqual(overall["complete"]["benchmark_count"], 14)
        self.assertAlmostEqual(overall["complete"]["geomean_speedup"], 1.5)
        self.assertEqual(overall["complete"]["positive_count"], 14)

    def test_retention_decision_never_confuses_valid_results_with_m1_success(self):
        decision, reasons = formal.retention_decision([])
        self.assertEqual(decision, "unproven")
        self.assertEqual(reasons, ["missing_m1_retention_evidence"])

        decision, reasons = formal.retention_decision([{
            "diagnostic_retain_gate_pass": 0,
            "diagnostic_retain_gate_failures": (
                "filter_did_not_reduce_wasted_bytes;"
                "no_positive_majority_on_applicable_workloads"
            ),
        }])
        self.assertEqual(decision, "reject")
        self.assertEqual(reasons, [
            "filter_did_not_reduce_wasted_bytes",
            "no_positive_majority_on_applicable_workloads",
        ])

        decision, reasons = formal.retention_decision([{
            "diagnostic_retain_gate_pass": 1,
            "diagnostic_retain_gate_failures": "",
        }])
        self.assertEqual(decision, "retain")
        self.assertEqual(reasons, [])

    def test_formal_retention_adds_suite_wide_performance_gates(self):
        rows = []
        for benchmark in formal.WORKLOADS:
            for config in formal.CONFIGS:
                speedup = 1.0
                pairs = 0.0
                if config == "m1":
                    speedup = 1.02
                    pairs = 10.0
                elif config == "complete":
                    speedup = 1.03
                rows.append({
                    "benchmark": benchmark,
                    "config": config,
                    "speedup_vs_baseline": speedup,
                    "granularity_frontend_paired_read_aggregates": pairs,
                })
        evidence = formal.formal_retention_evidence(rows)
        self.assertEqual(evidence["m1_formal_positive_majority"], 1)
        self.assertEqual(evidence["complete_no_systematic_regression"], 1)
        decision, reasons = formal.retention_decision([{
            "diagnostic_retain_gate_pass": 1,
            "diagnostic_retain_gate_failures": "",
        }], evidence)
        self.assertEqual((decision, reasons), ("retain", []))

        for row in rows:
            if row["config"] == "complete":
                row["speedup_vs_baseline"] = 0.9
        evidence = formal.formal_retention_evidence(rows)
        decision, reasons = formal.retention_decision([{
            "diagnostic_retain_gate_pass": 1,
            "diagnostic_retain_gate_failures": "",
        }], evidence)
        self.assertEqual(decision, "reject")
        self.assertIn("complete_has_systematic_regression", reasons)

    def test_strict_diagnostic_is_bound_to_formal_binary_hash(self):
        root = Path("/tmp/strict-diagnostic-fixture")
        expected_sha = "a" * 64
        retention = [{"diagnostic_retain_gate_pass": 1}]
        identity = {
            "cell_count": 35,
            "binary": "/tmp/frozen",
            "binary_sha256": expected_sha,
        }
        with (
            mock.patch.object(
                formal.paired, "summarize_directory", return_value=[]
            ),
            mock.patch.object(
                formal.paired, "audit_campaign_identity",
                return_value=identity,
            ) as audit,
            mock.patch.object(
                formal.paired, "full_workload_errors", return_value=[]
            ),
            mock.patch.object(
                formal.paired, "retention_summary", return_value=retention
            ),
            mock.patch.object(formal, "audit_wg_mapping") as wg_audit,
        ):
            rows, observed_retention, observed_identity = (
                formal.strict_diagnostic_rows(root, expected_sha)
            )
        self.assertEqual(rows, [])
        self.assertEqual(observed_retention, retention)
        self.assertEqual(observed_identity, identity)
        audit.assert_called_once_with(
            root,
            [],
            formal.DIAGNOSTIC_WORKLOADS,
            formal.DIAGNOSTIC_CONFIGS,
            expected_sha,
        )
        wg_audit.assert_called_once_with(
            root,
            list(formal.DIAGNOSTIC_WORKLOADS),
            formal.DIAGNOSTIC_CONFIGS,
            require_baseline_match=True,
        )


if __name__ == "__main__":
    unittest.main()
