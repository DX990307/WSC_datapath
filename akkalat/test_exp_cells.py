"""Tests for exp_cells.py (the `akita exp` campaign adapter).

Tiny fake runall2 campaign directories cover cell status, metrics, commands,
LLM folding and the sensitivity sweep. When the old monitor (exp_web.py of
the `exp` tool) is installed, the same directories are also read with its
campaign reader and the cells must agree.
"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import exp_cells

HERE = Path(__file__).resolve().parent
OLD_READER = Path(os.environ.get("EXP_WEB_PY", Path.home() / ".local/share/exp/exp_web.py"))


def write_cell(root, benchmark, configuration, total_time=1e-4, wgs=512,
               success=True, returncode=0, simulator_returncode=0,
               result=True, metrics=True, stdout=None, wg_key="max_wg_completed"):
    stem = Path(root) / f"baseline_{benchmark}_{configuration}"
    if result:
        Path(f"{stem}_result.json").write_text(json.dumps({
            "success": success, "returncode": returncode,
            "simulator_returncode": simulator_returncode}))
    if metrics:
        lines = [", where, what, value", "0, Driver, kernel_time, 0.0"]
        if total_time is not None:
            lines.append(f"1, Driver, total_time, {total_time:.12f}")
        lines.append("2, GPU[0].SA[0].CU[0], cu_wg_count, 51.0")
        if wgs is not None:
            lines.append(f"3, Driver, {wg_key}, {wgs:.1f}")
        Path(f"{stem}_metrics.csv").write_text("\n".join(lines) + "\n")
    if stdout is not None:
        Path(f"{stem}_out.stdout").write_text(stdout)


def write_metadata(root, cells, flags=None):
    experiments = [{
        "target": "baseline", "benchmark": b, "configuration": c,
        "command": [f"{root}/baseline", f"-benchmark={b}",
                    f"-metric-file-name={root}/baseline_{b}_{c}_metrics",
                    "-heartbeat-file=/tmp/hb", "-timing", *(flags or {}).get(c, [])],
    } for b, c in cells]
    Path(root, "EXPERIMENT_METADATA.json").write_text(json.dumps({
        "experiment_count": len(experiments), "experiments": experiments,
        "created_at": "2026-10-15T00:00:00", "git_revision": "abc1234"}))


def sensitivity_manifest(reference):
    def point(dimension, name, filter_buckets=16384, l2=4, row=2):
        return {"dimension": dimension, "name": name, "filter_buckets_per_gpm": filter_buckets,
                "l2_size_mb": l2, "row_region_lines": row,
                "configurations": [f"{name}_baseline", f"{name}_complete"]}
    specs = [point("filter-size", "filter-8k-buckets", 8192),
             point("l2-size", "l2-1mb", l2=1), point("l2-size", "l2-2mb", l2=2),
             point("row-locality", "row-4-lines", row=4)]
    if reference:
        specs.insert(0, point("reference", "main-16k-4mb-2-lines"))
    return {"kind": "cupath_sensitivity_campaign", "mechanisms_per_point": ["baseline", "complete"],
            "benchmarks": ["fir", "aes"], "points": specs}


def cell_map(doc):
    return {(c["key"]["workload"], c["key"]["variant"], c["key"]["config"]): c for c in doc["cells"]}


class Campaigns(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def campaign(self, name):
        root = Path(self.tmp.name, "results", name)
        root.mkdir(parents=True)
        return root

    def make_main(self):
        root = self.campaign("1015_abl_wg32k_mi200_67f1c03")
        cells = [(b, c) for b in ("fir", "aes", "kmeans", "spmv") for c in ("baseline", "m1", "complete")]
        cells.append(("fft", "baseline"))
        write_metadata(root, cells, {"complete": ["-complete"]})
        write_cell(root, "fir", "baseline", 4e-4)
        write_cell(root, "fir", "m1", 3e-4)
        write_cell(root, "fir", "complete", 2e-4, wgs=500)
        write_cell(root, "aes", "baseline", 1e-4, wg_key="total_wg_count")
        write_cell(root, "aes", "m1", 1e-4, success=False, returncode=1)
        write_cell(root, "aes", "complete", None)                       # bad: no total_time
        write_cell(root, "kmeans", "baseline", 5e-4, wgs=None)          # bad: no WG count
        write_cell(root, "kmeans", "m1", result=False, metrics=False, stdout="sim...\n")
        write_cell(root, "kmeans", "complete", result=False, metrics=False,
                   stdout="sim...\nReturn code: 2\n")
        write_cell(root, "spmv", "baseline", 1e-3)
        write_cell(root, "spmv", "m1", 5e-4, result=False)              # metrics only: ok
        # spmv complete and fft baseline: missing
        Path(root, "EXPERIMENT_BINARIES.json").write_text(json.dumps(
            {"sha256_by_target": {"baseline": "feb881a1621e0123456789"}}))
        return root

    def make_llm(self):
        root = self.campaign("1015_llm-smoke_wafer48_mi200_67f1c03")
        ops = ("llmop_bert_bert-7b-proxy_001_layer00_attn_q", "embed")
        cells = [("bert", f"{op}_{m}") for op in ops for m in ("baseline", "complete")]
        write_metadata(root, cells)
        for b, c in cells:
            write_cell(root, b, c, 4e-4 if c.endswith("baseline") else 1e-4)
        return root

    def make_sensitivity(self, reference):
        root = self.campaign("1015_sens_wafer48_hpca_67f1c03" + ("-ref" if reference else ""))
        manifest = sensitivity_manifest(reference)
        Path(root, "SENSITIVITY_CAMPAIGN.json").write_text(json.dumps(manifest))
        cells = []
        for spec in manifest["points"]:
            for b in ("aes", "fir"):
                for mode, t in (("baseline", 4e-4), ("complete", 2e-4)):
                    cells.append((b, f"{spec['name']}_{mode}"))
                    if not (spec["name"] == "row-4-lines" and b == "fir"):
                        write_cell(root, b, f"{spec['name']}_{mode}", t)
        cells.append(("aes", "bogus_complete"))
        write_metadata(root, cells, {f"{p['name']}_{m}": [f"-l2-cache-size-mb={p['l2_size_mb']}"]
                                     for p in manifest["points"] for m in ("baseline", "complete")})
        return root

    # ---------- main campaign ----------

    def test_main_status_and_metrics(self):
        doc = exp_cells.campaign_cells(str(self.make_main()))
        self.assertEqual(doc["schema"], "exp-cells/1")
        self.assertEqual(doc["dimensions"], ["workload", "variant", "config"])
        c = cell_map(doc)
        st = {k: v["status"] for k, v in c.items()}
        self.assertEqual(st[("fir", "", "baseline")], "ok")
        self.assertEqual(c[("fir", "", "baseline")]["metrics"], {"total_time": 4e-4, "wgs": 512})
        self.assertEqual(c[("fir", "", "complete")]["metrics"]["wgs"], 500)
        self.assertEqual(c[("aes", "", "baseline")]["metrics"]["wgs"], 512)  # total_wg_count
        self.assertEqual(st[("aes", "", "m1")], "failed")
        self.assertIn("returncode=1", c[("aes", "", "m1")]["reason"])
        self.assertEqual(st[("aes", "", "complete")], "bad")
        self.assertEqual(st[("kmeans", "", "baseline")], "bad")
        self.assertEqual(st[("kmeans", "", "m1")], "running")
        self.assertEqual(st[("kmeans", "", "complete")], "failed")
        self.assertEqual(st[("spmv", "", "m1")], "ok")
        self.assertEqual(st[("spmv", "", "complete")], "missing")
        self.assertEqual(st[("fft", "", "baseline")], "missing")
        self.assertEqual(c[("spmv", "", "complete")]["metrics"], {})
        self.assertEqual(doc["total"], 13)
        self.assertEqual(doc["binary"], "feb881a1621e")
        self.assertIsNone(doc["sweep"])
        self.assertEqual(doc["meta"]["kind"], "main")
        self.assertEqual(doc["meta"]["git_revision"], "abc1234")

    def test_main_layout_and_commands(self):
        doc = exp_cells.campaign_cells(str(self.make_main()))
        self.assertEqual(doc["order"]["workload"], ["aes", "fft", "fir", "kmeans", "spmv"])
        self.assertEqual(doc["order"]["config"], ["baseline", "m1", "complete"])
        self.assertEqual(doc["order"]["variant"], [""])
        self.assertEqual(doc["labels"]["workload"]["kmeans"], "KM")
        self.assertEqual(doc["labels"]["config"]["m1"], "Local Pairing")
        # Commands of one benchmark (aes when it ran), output paths dropped.
        c = cell_map(doc)
        self.assertEqual(c[("aes", "", "complete")]["command"], ["-benchmark=aes", "-timing", "-complete"])
        self.assertEqual(c[("aes", "", "baseline")]["command"], ["-benchmark=aes", "-timing"])
        self.assertNotIn("command", c[("fir", "", "complete")])

    def test_cli_from_stdin(self):
        root = self.make_main()
        src = Path(HERE, "exp_cells.py").read_text()
        out = subprocess.run([sys.executable, "-", str(root)], input=src, text=True,
                             capture_output=True, check=True, cwd=self.tmp.name)
        self.assertEqual(json.loads(out.stdout), exp_cells.campaign_cells(str(root)))

    def test_inferred_without_metadata(self):
        root = self.campaign("x")
        write_cell(root, "fir", "baseline")
        doc = exp_cells.campaign_cells(str(root))
        self.assertEqual([c["key"] for c in doc["cells"]],
                         [{"workload": "fir", "variant": "", "config": "baseline"}])
        self.assertEqual(len(doc["warnings"]), 1)

    def test_llm_folding(self):
        doc = exp_cells.campaign_cells(str(self.make_llm()))
        self.assertEqual(doc["meta"]["kind"], "llm")
        ws = doc["order"]["workload"]
        self.assertIn("bert_llmop_bert_bert-7b-proxy_001_layer00_attn_q", ws)
        self.assertEqual(doc["labels"]["workload"]["bert_embed"], "bert_embed")
        self.assertTrue(all("command" not in c for c in doc["cells"]))
        self.assertEqual(doc["order"]["config"], ["baseline", "complete"])

    # ---------- sensitivity ----------

    def test_sensitivity_sweep(self):
        doc = exp_cells.campaign_cells(str(self.make_sensitivity(reference=True)))
        self.assertEqual(doc["meta"]["kind"], "sensitivity")
        dims = doc["sweep"]["dimensions"]
        self.assertEqual([d["name"] for d in dims], ["filter-size", "l2-size", "row-locality"])
        l2 = dims[1]
        self.assertEqual(l2["unit"], "MiB")
        self.assertEqual([(p["value"], p["label"], p["reference"]) for p in l2["points"]],
                         [(1, "1", False), (2, "2", False), (4, "4", True)])
        self.assertEqual(l2["points"][-1]["cell"], {"variant": "main-16k-4mb-2-lines"})
        self.assertEqual(dims[0]["points"][0]["label"], "8K")
        self.assertEqual(doc["order"]["variant"][0], "main-16k-4mb-2-lines")
        self.assertEqual(len(doc["warnings"]), 1)  # bogus_complete
        st = {k: v["status"] for k, v in cell_map(doc).items()}
        self.assertEqual(st[("fir", "row-4-lines", "complete")], "missing")
        self.assertEqual(st[("aes", "l2-1mb", "complete")], "ok")

    def test_sensitivity_without_reference(self):
        doc = exp_cells.campaign_cells(str(self.make_sensitivity(reference=False)))
        ref = doc["sweep"]["dimensions"][1]["points"][-1]
        self.assertEqual((ref["value"], ref["cell"], ref["reference"]), (4, None, True))

    # ---------- the old reader ----------

    @unittest.skipUnless(OLD_READER.exists(), "old exp_web.py not installed")
    def test_same_cells_as_the_old_reader(self):
        old = load_old_reader()
        roots = [self.make_main(), self.make_llm(), self.make_sensitivity(True), self.make_sensitivity(False)]
        for root in roots:
            raw = old.read(str(root))
            norm = old.module.normalize_campaign(raw)
            doc = exp_cells.campaign_cells(str(root))
            new = {"%s|%s|%s" % k: c for k, c in cell_map(doc).items()}
            self.assertEqual(list(norm["cells"]), list(new), root.name)
            for k, o in norm["cells"].items():
                n = new[k]
                self.assertEqual(o["status"], n["status"], k)
                self.assertEqual(o["t"], n["metrics"].get("total_time"), k)
                self.assertEqual(o["wgs"], n["metrics"].get("wgs"), k)
            self.assertEqual(norm["workloads"], doc["order"]["workload"])
            self.assertEqual(norm["labels"], doc["labels"]["workload"])
            self.assertEqual(norm["modes"], doc["order"]["config"])
            self.assertEqual(norm["variants"], doc["order"]["variant"])
            self.assertEqual(norm["kind"], doc["meta"]["kind"])
            self.assertEqual(len(norm["warnings"]), len(doc["warnings"]))
            for vk, cmd in norm["commands"].items():
                v, m = vk.split("|")
                self.assertEqual(new["%s|%s|%s" % (raw["cmd_bench"], v, m)]["command"], cmd)
            if norm["sens"]:
                self.assertEqual(norm["sens"]["reference"], next(
                    (p["cell"]["variant"] for d in doc["sweep"]["dimensions"] for p in d["points"]
                     if p["reference"] and p["cell"]), None))
                for od, nd in zip(norm["sens"]["dims"], doc["sweep"]["dimensions"]):
                    self.assertEqual(od["name"], nd["name"])
                    pts = [p for p in nd["points"] if not p["reference"]]
                    self.assertEqual([(p["name"], p["value"], p["tick"]) for p in od["points"]],
                                     [(p["cell"]["variant"], p["value"], p["label"]) for p in pts])
                    ref = nd["points"][-1]
                    self.assertEqual(od["ref_value"], ref["value"])
                    self.assertEqual(od["ref_tick"], ref["label"])


class _OldReader:
    def __init__(self, module):
        self.module = module

    def read(self, campaign_dir):
        root, name = os.path.split(campaign_dir)
        proj = os.path.dirname(root)
        args = {"root": os.path.dirname(proj), "project": os.path.basename(proj), "name": name}
        script = self.module.campaign_script(args["project"], name, args["root"])
        out = subprocess.run([sys.executable, "-"], input=script, text=True,
                             capture_output=True, check=True)
        return json.loads(out.stdout)


def load_old_reader():
    import importlib.util
    spec = importlib.util.spec_from_file_location("exp_web_old", OLD_READER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return _OldReader(module)


if __name__ == "__main__":
    unittest.main()
