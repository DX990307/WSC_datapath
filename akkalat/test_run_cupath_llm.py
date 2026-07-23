import unittest
from types import SimpleNamespace

import run_cupath_llm


class RunCuPathLLMTest(unittest.TestCase):
    def test_default_campaign_uses_pasta_shape_and_formal_configs(self):
        args = run_cupath_llm.parse_args([
            "--models=gpt",
            "--configs=baseline,complete",
            "--layers=1",
            "--limit=1",
        ])
        exps, model_ops = run_cupath_llm.build_campaign(args)

        self.assertEqual(len(exps), 2)
        self.assertEqual(len(model_ops[0][1]), 1)
        self.assertIn("gpt_gpt-7b_000_embedding_baseline", exps[0]["config_name"])
        self.assertIn("gpt_gpt-7b_000_embedding_complete", exps[1]["config_name"])
        self.assertIn("-max-wg=78600", exps[0]["common_flags"])
        self.assertIn("-sampled", exps[0]["common_flags"])
        self.assertIn("-remote-data-path-enable=false", exps[0]["flags"])
        self.assertIn("-remote-data-path-enable=true", exps[1]["flags"])

    def test_no_photon_and_natural_completion(self):
        args = run_cupath_llm.parse_args([
            "--models=bert",
            "--configs=m1",
            "--layers=1",
            "--limit=1",
            "--max-wg=0",
            "--no-photon",
        ])
        exps, _ = run_cupath_llm.build_campaign(args)

        self.assertEqual(len(exps), 1)
        self.assertFalse(any(
            flag.startswith("-max-wg=")
            for flag in exps[0]["common_flags"]
        ))
        self.assertNotIn("-sampled", exps[0]["common_flags"])
        self.assertIn("-l2-adaptive-pair-enable=true", exps[0]["flags"])

    def test_one_gpt_layer_and_identical_ops_reconstruct_full_model(self):
        args = run_cupath_llm.parse_args([
            "--models=gpt",
            "--configs=baseline",
            "--layers=32",
        ])
        exps, model_ops = run_cupath_llm.build_campaign(args)
        model_args, ops, metadata, profile = model_ops[0]

        self.assertEqual(model_args.layers, 1)
        self.assertEqual(profile, "gpt-7b")
        self.assertEqual(len(ops), 11)
        self.assertEqual(len(exps), 11)
        self.assertEqual(
            sum(entry["logical_multiplicity"] for entry in metadata),
            1 + 15 * 32,
        )

        q_group = next(
            entry
            for entry in metadata
            if "layer00_attn_q" in entry["equivalent_labels"]
        )
        self.assertEqual(
            q_group["equivalent_labels"],
            [
                "layer00_attn_q",
                "layer00_attn_k",
                "layer00_attn_v",
                "layer00_attn_out",
            ],
        )
        self.assertEqual(q_group["one_layer_occurrences"], 4)
        self.assertEqual(q_group["logical_multiplicity"], 4 * 32)

    def test_dedup_requires_the_same_simulator_visible_shape(self):
        same = [
            "-op=split-linear",
            "-rows=512",
            "-input-dim=4096",
            "-output-dim=4096",
            "-split-k=4",
        ]
        different_output = [
            "-op=split-linear",
            "-rows=512",
            "-input-dim=4096",
            "-output-dim=512",
            "-split-k=4",
        ]
        ops, metadata = run_cupath_llm.deduplicate_representative_ops(
            [
                ("layer00_q", same),
                ("layer00_k", list(reversed(same))),
                ("layer00_score", different_output),
            ],
            32,
        )

        self.assertEqual(len(ops), 2)
        self.assertEqual(metadata[0]["logical_multiplicity"], 64)
        self.assertEqual(metadata[1]["logical_multiplicity"], 32)

    def test_weighted_summary_reconstructs_repeated_instances(self):
        def record(config, driver_total):
            return SimpleNamespace(
                result_id=SimpleNamespace(
                    model="gpt",
                    profile="gpt-7b",
                    config=config,
                    op_index=2,
                    op_name="layer00_attn_q",
                ),
                driver_total_time=driver_total,
                driver_kernel_time=driver_total / 2,
                command_processor_kernel_time=driver_total / 4,
                max_command_processor_kernel_time=driver_total / 8,
                stdout_elapsed_seconds=10.0,
                return_code="0",
            )

        weights = {
            ("gpt", "gpt-7b", 2, "layer00_attn_q"): {
                "logical_multiplicity": 128,
            },
        }
        groups = run_cupath_llm.weighted_groups(
            [record("baseline", 2.0), record("complete", 1.0)],
            weights,
        )

        baseline = groups[("gpt", "gpt-7b", "baseline")]
        complete = groups[("gpt", "gpt-7b", "complete")]
        self.assertEqual(baseline["logical_ops"], 128)
        self.assertEqual(baseline["driver_total_time"], 256.0)
        self.assertEqual(complete["driver_total_time"], 128.0)
        self.assertEqual(
            baseline["driver_total_time"] / complete["driver_total_time"],
            2.0,
        )

    def test_all_selects_five_formal_configs(self):
        self.assertEqual(
            run_cupath_llm.selected_configs("all"),
            list(run_cupath_llm.FORMAL_CONFIGS),
        )


if __name__ == "__main__":
    unittest.main()
