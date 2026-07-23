import unittest

import CuPathLLM
import run_cupath_llm


class CuPathLLMTest(unittest.TestCase):
    def test_resnet_matches_the_pasta_full_model_cell(self):
        args = run_cupath_llm.parse_args([
            "--models=resnet",
            "--configs=baseline,complete",
        ])
        exps, model_ops = CuPathLLM.build_campaign(args)

        self.assertEqual(len(exps), 2)
        self.assertEqual(model_ops, [])
        self.assertEqual(exps[0]["benchmark"], "resnet")
        self.assertEqual(
            exps[0]["config_name"],
            "resnet_resnet-50-full_000_full_baseline",
        )
        self.assertIn("-resnet-mode=full", exps[0]["flags"])
        self.assertIn("-resnet-depth=50", exps[0]["flags"])
        self.assertIn("-resnet-batch-size=4", exps[0]["flags"])
        self.assertIn("-resnet-image-size=224", exps[0]["flags"])

    def test_default_model_set_contains_all_three_workloads(self):
        self.assertEqual(
            CuPathLLM.selected_models(CuPathLLM.DEFAULT_MODELS),
            ["resnet", "bert", "gpt"],
        )


if __name__ == "__main__":
    unittest.main()
