import io
import json
import os
import tempfile
import unittest

import isca27_name as nm


class NameTest(unittest.TestCase):
    def test_make_and_parse(self):
        n = nm.make("1015", "abl", "wg32k", "mi200", "67f1c03")
        self.assertEqual(n, "1015_abl_wg32k_mi200_67f1c03")
        self.assertEqual(nm.parse(n)["config"], "mi200")
        self.assertEqual(nm.parse("1015_diag_hbm-read_mi200+mshr128_67f1c03")["config"], "mi200+mshr128")

    def test_rejects(self):
        for bad in ["1015_abl_wafer48_mi200_67f1c03_x", "1015_foo_x_mi200_67f1c03",
                    "1015_abl_wg_32k_mi200_67f1c03", "15_abl_x_mi200_67f1c03",
                    "1015_abl_x_mi200_67f1c0"]:
            self.assertTrue(nm.check(bad), bad)
        with self.assertRaises(ValueError):
            nm.make("1015", "abl", "a_b", "mi200", "67f1c03")

    def test_legacy(self):
        cases = {
            "1015_abl_wafer48_mi200_67f1c03": "1015_abl_wg32k_mi200_67f1c03",
            "1015_abl-spmv_wafer48_hpca_67f1c03": "1015_abl_spmv-wg1536_hpca_67f1c03",
            "1015_determinism_wafer48_mi200_67f1c03": "1015_det_wg32k_mi200_67f1c03",
            "1015_t14m2-bw-read_wafer48_mi200_67f1c03": "1015_plat_bw-read_mi200_67f1c03",
            "1013_t14-rbw-d2-16M_wafer48_hpca_0e38983": "1013_plat_rbw-d2-16M_hpca_0e38983",
            "1015_diag-place-fir_wafer48_mi200_67f1c03": "1015_place_fir_mi200_67f1c03",
            "1015_llm-smoke_wafer48_mi200_67f1c03": "1015_llm_wg8k_mi200_67f1c03",
            "1015_diaghbm-v1mshr": "1015_diag_hbm-read_mi200+mshr128_67f1c03",
            "1015_diagrc-bw-read_wafer48_mi200_67f1c03": "1015_diag_bw-read_mi200+rowcont_67f1c03",
            "1013_t13s4-main_wafer48_hpca_260ce73": "1013_abl_full_hpca_260ce73",
            "1013_t13s4-filter-audit_wafer48_mi300x_260ce73": "1013_audit_full_mi300x_260ce73",
            "1013_t13s4-probe-P3b-o2r1-read-pass1-wg1_wafer48_hpca_9220740":
                "1013_probe_P3b-o2r1-read-pass1-wg1_hpca_9220740",
            "1013_probe-P1-o5r5-read-pass2_wafer48_baseline_ca7ea0b":
                "1013_probe_P1-o5r5-read-pass2_baseline_ca7ea0b",
            "1013_t12-R2-fir-maxwg4096-pre-fix_wafer48_paper-nomagic_f444076":
                "1013_diag_t12-R2-fir-maxwg4096-pre-fix_paper-nomagic_f444076",
            "1013_t13-reg-fir-maxwg4096_wafer48_hpca_9c30a89": "1013_diag_reg-fir-maxwg4096_hpca_9c30a89",
            "1014_diag-attr_wafer48_mi300x_756f214": "1014_abl_attr_mi300x_756f214",
            "1014_diag-l2bw-l1mshr32_wafer48_hpca_756f214": "1014_diag_l2bw_hpca+l1mshr32_756f214",
            "1014_diag-rbw-d7-rdma256_wafer48_mi300x_756f214": "1014_diag_rbw-d7_mi300x+rdma256_756f214",
            "1014_diag-xlat-src1-dst2_wafer48_hpca_756f214": "1014_diag_xlat-src1-dst2_hpca_756f214",
            "1015_abl_wg32k_mi200_67f1c03": "1015_abl_wg32k_mi200_67f1c03",
        }
        for old, new in cases.items():
            self.assertEqual(nm.legacy_name(old), new, old)
        self.assertIsNone(nm.legacy_name("something-else"))

    def test_apply(self):
        with tempfile.TemporaryDirectory() as root:
            res = os.path.join(root, "results")
            old = os.path.join(res, "1015_abl_wafer48_mi200_67f1c03")
            os.makedirs(old)
            with open(os.path.join(old, "EXPERIMENT_METADATA.json"), "w") as f:
                json.dump({"output_dir": old}, f)
            with open(os.path.join(old, "baseline_fir_baseline_metrics.csv"), "w") as f:
                f.write(old)  # statistics files are left alone
            os.makedirs(os.path.join(res, "odd-name"))
            with open(os.path.join(root, ".exp_archive.json"), "w") as f:
                json.dump({"results": {"1015_abl_wafer48_mi200_67f1c03": {"by": ["x"]}}}, f)

            nm.apply(res, dry_run=True, out=io.StringIO())
            self.assertTrue(os.path.isdir(old))

            nm.apply(res, out=io.StringIO())
            new = os.path.join(res, "1015_abl_wg32k_mi200_67f1c03")
            self.assertFalse(os.path.exists(old))
            with open(os.path.join(new, "EXPERIMENT_METADATA.json")) as f:
                self.assertEqual(json.load(f)["output_dir"], new)
            with open(os.path.join(new, "baseline_fir_baseline_metrics.csv")) as f:
                self.assertEqual(f.read(), old)
            with open(os.path.join(root, ".exp_archive.json")) as f:
                self.assertIn("1015_abl_wg32k_mi200_67f1c03", json.load(f)["results"])
            self.assertTrue(os.path.isdir(os.path.join(res, "odd-name")))
            with open(os.path.join(new, "run.json")) as f:
                run = json.load(f)
            self.assertEqual(run["fields"]["item"], "wg32k")
            self.assertIn({"property": "Renamed From", "value": "1015_abl_wafer48_mi200_67f1c03"},
                          run["properties"])
            with open(os.path.join(res, "RENAMES.tsv")) as f:
                self.assertIn("\t1015_abl_wg32k_mi200_67f1c03", f.read())

    def test_record(self):
        with tempfile.TemporaryDirectory() as root:
            d = os.path.join(root, "1015_plat_bw-read_mi200_67f1c03")
            os.makedirs(d)
            with open(os.path.join(d, "meta.json"), "w") as f:
                json.dump({"commit": "67f1c03", "host": "yss2", "exit_code": 0}, f)
            nm.record(d, {"Command": "x"})
            run = nm.record(d, {"Host": "yss3"})
            props = {p["property"]: p["value"] for p in run["properties"]}
            self.assertEqual(props, {"Command": "x", "Git Revision": "67f1c03", "Host": "yss3", "Exit Code": "0"})
            self.assertEqual(run["fields"]["category"], "plat")
            with self.assertRaises(ValueError):
                nm.record(root)


if __name__ == "__main__":
    unittest.main()
