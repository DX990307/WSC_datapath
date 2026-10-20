"""Tests for isca27_plot.py.

Synthetic campaign directories cover the data model (cell status, speedup and
WG checks, layering, sensitivity reference handling) without matplotlib.  A
smoke test reproduces the published July sensitivity and ablation numbers when
those result directories are present, and the drawing test runs only where
matplotlib is installed.
"""

import csv
import contextlib
import io
import json
import math
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import isca27_plot as plot

HERE = Path(__file__).resolve().parent
RESULTS = HERE / "results"
JULY_SENSITIVITY = RESULTS / "2026-07-25-19-41-19-cupath-sensitivity"
JULY_OBSERVATION = RESULTS / "2026-07-22-baseline-complete-observation"
JULY_M1M2M3 = RESULTS / "2026-07-22-m1-m2-m3-rerun"
JULY_SAMPLED = RESULTS / "2026-07-23-23-41-05-sampled-validation"

try:
    import matplotlib  # noqa: F401
    HAVE_MATPLOTLIB = True
except ImportError:
    HAVE_MATPLOTLIB = False


def write_cell(root, benchmark, configuration, total_time=1e-4, wgs=78600,
               success=True, returncode=0, simulator_returncode=0,
               result=True, metrics=True, stdout=None,
               wg_key="max_wg_completed", target="baseline"):
    stem = Path(root) / f"{target}_{benchmark}_{configuration}"
    if result:
        payload = {"benchmark": benchmark, "configuration": configuration,
                   "success": success, "returncode": returncode, "target": target}
        if simulator_returncode is not None:
            payload["simulator_returncode"] = simulator_returncode
        Path(f"{stem}_result.json").write_text(json.dumps(payload))
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


def write_metadata(root, cells, command_flags=None):
    experiments = []
    for benchmark, configuration in cells:
        flags = list((command_flags or {}).get(configuration, []))
        experiments.append({
            "target": "baseline", "benchmark": benchmark,
            "configuration": configuration,
            "command": [f"{root}/frozen-binaries/baseline-abc",
                        f"-benchmark={benchmark}",
                        f"-metric-file-name={root}/baseline_{benchmark}_{configuration}_metrics",
                        *flags],
        })
    Path(root, "EXPERIMENT_METADATA.json").write_text(json.dumps({
        "experiment_count": len(experiments), "experiments": experiments,
        "git_revision": "abc1234"}))


def read_rows(path):
    with Path(path).open(newline="", encoding="utf-8") as stream:
        return list(csv.DictReader(stream))


def quiet(function, *args, **kwargs):
    with contextlib.redirect_stdout(io.StringIO()), \
            contextlib.redirect_stderr(io.StringIO()):
        return function(*args, **kwargs)


class TempDirTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def campaign_dir(self, name):
        path = self.tmp / name
        path.mkdir()
        return path


class ImportTest(unittest.TestCase):
    def test_module_import_does_not_load_matplotlib(self):
        output = subprocess.run(
            [sys.executable, "-c",
             "import sys, isca27_plot; print('matplotlib' in sys.modules)"],
            cwd=HERE, capture_output=True, text=True, check=True).stdout
        self.assertEqual(output.strip(), "False")


class CellTest(TempDirTest):
    def cell(self, root, benchmark="fir", configuration="complete"):
        variant, mode = plot.split_configuration(configuration)
        return plot.evaluate_cell(plot.Cell(
            root=root, target="baseline", benchmark=benchmark,
            configuration=configuration, workload=benchmark,
            variant=variant, mode=mode))

    def test_driver_metrics_and_completed_wgs(self):
        root = self.campaign_dir("c")
        write_cell(root, "fir", "complete", total_time=2.5e-4, wgs=1234)
        values = plot.read_driver_metrics(root / "baseline_fir_complete_metrics.csv")
        self.assertAlmostEqual(values["total_time"], 2.5e-4)
        self.assertEqual(plot.completed_wgs(values), 1234)
        self.assertNotIn("cu_wg_count", values)
        self.assertEqual(plot.completed_wgs({"total_wg_count": 77.0}), 77)
        self.assertEqual(
            plot.completed_wgs({"total_wg_count": 5.0, "max_wg_completed": 4.0}), 4)
        self.assertIsNone(plot.completed_wgs({}))

    def test_status_classification(self):
        root = self.campaign_dir("c")
        write_cell(root, "ok", "complete")
        write_cell(root, "nores", "complete", result=False)
        write_cell(root, "fail", "complete", success=False, returncode=2,
                   simulator_returncode=None)
        write_cell(root, "simrc", "complete", simulator_returncode=1)
        write_cell(root, "run", "complete", result=False, metrics=False,
                   stdout="WG progress 6%\n")
        write_cell(root, "died", "complete", result=False, metrics=False,
                   stdout="...\nReturn code: -9\nEnd time: x\n")
        write_cell(root, "notime", "complete", total_time=None)
        write_cell(root, "zerotime", "complete", total_time=0.0)
        write_cell(root, "nowg", "complete", wgs=None)
        write_cell(root, "nometrics", "complete", metrics=False)
        expected = {
            "ok": plot.OK, "nores": plot.OK, "fail": plot.FAILED,
            "simrc": plot.FAILED, "run": plot.RUNNING, "died": plot.FAILED,
            "notime": plot.BAD, "zerotime": plot.BAD, "nowg": plot.BAD,
            "nometrics": plot.BAD, "absent": plot.MISSING,
        }
        for benchmark, status in expected.items():
            with self.subTest(benchmark=benchmark):
                self.assertEqual(self.cell(root, benchmark).status, status)
        self.assertIn("returncode=-9", self.cell(root, "died").reason)

    def test_split_configuration(self):
        self.assertEqual(plot.split_configuration("m1"), ("", "m1"))
        self.assertEqual(plot.split_configuration("l2-1mb_complete"),
                         ("l2-1mb", "complete"))
        self.assertEqual(
            plot.split_configuration("bert_bert-7b-proxy_000_embedding_baseline"),
            ("bert_bert-7b-proxy_000_embedding", "baseline"))
        self.assertEqual(plot.split_configuration("custom"), ("", "custom"))


class MainCampaignMixin(TempDirTest):
    def make_main(self, name="main", times=None, wgs=None,
                  configs=("baseline", "m1", "m2", "m3", "complete")):
        root = self.campaign_dir(name)
        times = times or {}
        wgs = wgs or {}
        cells = []
        for benchmark in ("aes", "fir", "kmeans"):
            for config in configs:
                cells.append((benchmark, config))
                time = times.get((benchmark, config), 1e-4 if config == "baseline" else 5e-5)
                if time == "missing":
                    continue
                if time == "failed":
                    write_cell(root, benchmark, config, success=False, returncode=1)
                    continue
                write_cell(root, benchmark, config, total_time=time,
                           wgs=wgs.get((benchmark, config), 78600))
        write_metadata(root, cells)
        return root


class SpeedupTest(MainCampaignMixin):
    def test_speedup_matches_paper_definition(self):
        root = self.make_main(times={("fir", "baseline"): 3e-4, ("fir", "m1"): 2e-4})
        campaign = plot.load_campaign(root)
        series = plot.ablation_series(campaign, ["baseline", "m1"], campaign.workloads)
        pair = series[1].pairs[campaign.workloads.index("fir")]
        self.assertTrue(pair.ok)
        self.assertAlmostEqual(pair.speedup, 3e-4 / 2e-4)
        self.assertEqual(series[0].values, [1.0, 1.0, 1.0])

    def test_wg_mismatch_and_failures_are_flagged_not_fatal(self):
        root = self.make_main(
            times={("kmeans", "complete"): "failed", ("aes", "m2"): "missing",
                   ("fir", "baseline"): "failed"},
            wgs={("aes", "complete"): 73728})
        campaign = plot.load_campaign(root)
        self.assertEqual(campaign.workloads, ["aes", "fir", "kmeans"])
        series = {s.key: s for s in plot.ablation_series(
            campaign, ["baseline", "m2", "complete"], campaign.workloads)}
        complete = [p.flag for p in series["complete"].pairs]
        self.assertEqual(complete, [plot.WG_MISMATCH, plot.NO_BASELINE, plot.FAILED])
        self.assertIsNone(series["complete"].gmean)
        self.assertEqual([p.flag for p in series["m2"].pairs],
                         [plot.MISSING, plot.NO_BASELINE, plot.OK])
        self.assertEqual(series["m2"].n_ok, 1)
        self.assertAlmostEqual(series["m2"].gmean, 2.0)
        self.assertIn("baseline=78600 result=73728", series["complete"].pairs[0].note)

    def test_geomean(self):
        self.assertAlmostEqual(plot.geomean([2.0, 8.0]), 4.0)
        self.assertAlmostEqual(plot.geomean([2.0, None, 8.0]), 4.0)
        self.assertIsNone(plot.geomean([None]))

    def test_layering_prefers_later_ok_cells(self):
        old = self.make_main("old", times={("fir", "complete"): 4e-5,
                                           ("aes", "complete"): 4e-5})
        new = self.make_main("new", times={("fir", "complete"): 2e-5,
                                           ("aes", "complete"): "failed"})
        merged = plot.merge_campaigns([plot.load_campaign(old), plot.load_campaign(new)])
        self.assertEqual(merged.get("fir", "", "complete").root.name, "new")
        self.assertEqual(merged.get("aes", "", "complete").root.name, "old")
        self.assertEqual(len(merged.sources), 2)

    def test_external_baseline_dir(self):
        base = self.make_main("base", configs=("baseline",),
                              times={("fir", "baseline"): 6e-4})
        audit = self.make_main("audit", configs=("complete",),
                               times={("fir", "complete"): 2e-4})
        series = plot.ablation_series(
            plot.load_campaign(audit), ["baseline", "complete"],
            ["fir"], baseline=plot.load_campaign(base))
        self.assertAlmostEqual(series[1].pairs[0].speedup, 3.0)
        self.assertEqual(series[0].pairs[0].speedup, 1.0)

    def test_command_differences_ignore_paths(self):
        a = ["/x/bin", "-sampled", "-metric-file-name=/a", "-l2-cache-size-mb=1"]
        b = ["/y/bin", "-metric-file-name=/b", "-bandwidth=48"]
        self.assertEqual(plot.command_differences(a, b, plot.POINT_FLAGS),
                         (["-sampled"], ["-bandwidth=48"]))


class LlmTest(TempDirTest):
    def test_llm_operators_become_workloads(self):
        root = self.campaign_dir("llm")
        cells = []
        for op in ("000_embedding", "001_layer00_attn_q"):
            for mode, time in (("baseline", 4e-4), ("complete", 1e-4)):
                configuration = f"bert_bert-7b-proxy_{op}_{mode}"
                cells.append(("llmop", configuration))
                write_cell(root, "llmop", configuration, total_time=time)
        write_metadata(root, cells)
        campaign = plot.load_campaign(root)
        self.assertEqual(campaign.kind, "llm")
        self.assertEqual(campaign.workloads, [
            "llmop_bert_bert-7b-proxy_000_embedding",
            "llmop_bert_bert-7b-proxy_001_layer00_attn_q"])
        self.assertEqual([plot.workload_label(w) for w in campaign.workloads],
                         ["BERT embedding", "BERT attn_q"])
        series = plot.ablation_series(campaign, ["complete"], campaign.workloads)
        self.assertAlmostEqual(series[0].gmean, 4.0)
        self.assertEqual(plot.workload_label("resnet_resnet_resnet-50-full_000_full"),
                         "ResNet-50")


def sensitivity_manifest(points, reference=False):
    def point(dimension, name, filter_buckets=16384, l2=4, row=2):
        return {"dimension": dimension, "name": name,
                "filter_buckets_per_gpm": filter_buckets, "l2_size_mb": l2,
                "row_region_lines": row,
                "configurations": [f"{name}_baseline", f"{name}_complete"]}
    specs = [point(*p) for p in points]
    if reference:
        specs.insert(0, point("reference", "main-16k-4mb-2-lines"))
    return {"kind": "cupath_sensitivity_campaign",
            "mechanisms_per_point": ["baseline", "complete"],
            "benchmarks": ["fir", "aes"], "points": specs}


class SensitivityMixin(TempDirTest):
    POINTS = [("l2-size", "l2-1mb", 16384, 1, 2), ("l2-size", "l2-2mb", 16384, 2, 2),
              ("row-locality", "row-4-lines", 16384, 4, 4),
              ("row-locality", "row-8-lines", 16384, 4, 8)]

    def make_sensitivity(self, reference=False, missing=()):
        root = self.campaign_dir("sens")
        manifest = sensitivity_manifest(self.POINTS, reference)
        Path(root, "SENSITIVITY_CAMPAIGN.json").write_text(json.dumps(manifest))
        cells, flags = [], {}
        for spec in manifest["points"]:
            for benchmark in ("aes", "fir"):
                for mode, time in (("baseline", 4e-4), ("complete", 2e-4)):
                    configuration = f"{spec['name']}_{mode}"
                    cells.append((benchmark, configuration))
                    flags[configuration] = ["-sampled"]
                    if (benchmark, configuration) in missing:
                        continue
                    if spec["name"] == "l2-1mb" and mode == "complete":
                        time = 4e-4  # no speedup at 1 MiB
                    write_cell(root, benchmark, configuration, total_time=time)
        write_metadata(root, cells, flags)
        return root

    def make_main(self):
        root = self.campaign_dir("main")
        cells = []
        for benchmark in ("aes", "fir"):
            for mode, time in (("baseline", 8e-4), ("complete", 2e-4)):
                cells.append((benchmark, mode))
                write_cell(root, benchmark, mode, total_time=time)
        write_metadata(root, cells)
        return root


class SensitivityTest(SensitivityMixin):
    def test_reference_values_inferred_from_other_dimensions(self):
        manifest = sensitivity_manifest(self.POINTS)
        values = plot.reference_values(manifest)
        self.assertEqual(values["l2_size_mb"], 4)
        self.assertEqual(values["row_region_lines"], 2)
        self.assertEqual(values["filter_buckets_per_gpm"], 16384)

    def test_reference_point_from_main_campaign(self):
        sens = plot.load_campaign(self.make_sensitivity())
        main = plot.load_campaign(self.make_main())
        dims, notes = plot.build_sensitivity(sens, main)
        self.assertEqual([d.name for d in dims], ["l2-size", "row-locality"])
        l2, row = dims
        self.assertEqual([p.tick() for p in l2.points], ["1", "2", "4"])
        self.assertEqual([p.reference for p in l2.points], [False, False, True])
        self.assertEqual([p.tick() for p in row.points], ["2", "4", "8"])
        self.assertEqual([p.legend() for p in row.points],
                         ["2 lines", "4 lines", "8 lines"])
        self.assertEqual([s.gmean for s in l2.series], [1.0, 2.0, 4.0])
        self.assertTrue(any("-sampled" in note and note.startswith("WARNING")
                            for note in notes))

    def test_fixed_normalization_uses_main_baseline(self):
        sens = plot.load_campaign(self.make_sensitivity(reference=True))
        main = plot.load_campaign(self.make_main())
        dims, notes = plot.build_sensitivity(sens, main, normalize="fixed")
        l2 = dims[0]
        # complete 2e-4 over main baseline 8e-4; l2-1mb complete 4e-4.
        self.assertEqual([round(s.gmean, 9) for s in l2.series], [2.0, 4.0, 4.0])
        self.assertEqual(l2.points[-1].source.name, "sens")
        self.assertTrue(any("fixed baseline from main" in n for n in notes))

    def test_own_reference_matched(self):
        sens = plot.load_campaign(self.make_sensitivity(reference=True))
        dims, _ = plot.build_sensitivity(sens)
        self.assertEqual([p.name for p in dims[0].points],
                         ["l2-1mb", "l2-2mb", "main-16k-4mb-2-lines"])
        self.assertEqual([s.gmean for s in dims[0].series], [1.0, 2.0, 2.0])
        with self.assertRaises(plot.CampaignError):
            plot.build_sensitivity(sens, dimensions=["filter"])

    def test_partial_point_is_reported(self):
        sens = plot.load_campaign(self.make_sensitivity(
            missing={("fir", "row-8-lines_complete")}))
        dims, _ = plot.build_sensitivity(sens, dimensions=["row"])
        series = dims[0].series[-1]
        self.assertEqual(series.n_ok, 1)
        self.assertFalse(series.complete)
        self.assertEqual(series.pairs[1].flag, plot.MISSING)

    def test_cli_writes_tables_without_matplotlib(self):
        sens = self.make_sensitivity()
        main = self.make_main()
        out = self.tmp / "out"
        code = quiet(plot.main, ["sensitivity", str(sens), "--baseline-dir", str(main),
                                 "--out", str(out), "--no-plot"])
        self.assertEqual(code, 0)
        rows = read_rows(out / "sensitivity_summary.csv")
        self.assertEqual(len(rows), 6)
        self.assertEqual(sum(r["reference"] == "True" for r in rows), 2)
        sources = json.loads((out / "sensitivity_sources.json").read_text())
        self.assertEqual(sources["normalize"], "matched")


class TableAndCliTest(MainCampaignMixin):
    def test_table_markdown_and_csv(self):
        root = self.make_main(times={("kmeans", "m3"): "failed"},
                              wgs={("aes", "complete"): 1})
        out = self.tmp / "table"
        code = quiet(plot.main, ["table", str(root), "--out", str(out)])
        self.assertEqual(code, 0)
        text = (out / "table.md").read_text()
        self.assertIn("WG!=", text)
        self.assertIn("fail", text)
        self.assertIn("(2/3)", text)
        rows = read_rows(out / "table_cells.csv")
        self.assertEqual(len(rows), 15)
        aes = [r for r in rows if r["workload"] == "aes" and r["config"] == "complete"][0]
        self.assertEqual(aes["wg_match"], "False")
        self.assertEqual(aes["flag"], plot.WG_MISMATCH)

    def test_ablation_strict_and_partial(self):
        root = self.make_main(times={("kmeans", "m1"): "missing"})
        out = self.tmp / "abl"
        self.assertEqual(quiet(plot.main, ["ablation", str(root), "--out", str(out),
                                           "--no-plot"]), 0)
        rows = read_rows(out / "ablation.csv")
        gmean = [r for r in rows if r["workload"] == "GMEAN"
                 and r["config"] == "m1"][0]
        self.assertTrue(gmean["note"].startswith("2/3 workloads OK in all 5 series"),
                        gmean["note"])
        self.assertIn("excluded: kmeans", gmean["note"])
        self.assertEqual(gmean["flag"], "partial")
        # m3 has all three workloads, but its GMEAN bar covers the same two.
        m3 = [r for r in rows if r["workload"] == "GMEAN" and r["config"] == "m3"][0]
        self.assertAlmostEqual(float(m3["speedup"]), 2.0)
        self.assertAlmostEqual(float(m3["geomean_available"]), 2.0)
        sources = json.loads((out / "ablation_sources.json").read_text())
        self.assertEqual(sources["gmean"]["excluded"], ["kmeans"])
        self.assertEqual(quiet(plot.main, ["ablation", str(root), "--out", str(out),
                                           "--no-plot", "--strict"]), 2)

    def test_compare_two_campaigns(self):
        a = self.make_main("a", times={("fir", "complete"): 2.5e-5})
        b = self.make_main("b")
        out = self.tmp / "cmp"
        code = quiet(plot.main, ["compare", str(a), str(b), "--labels", "hpca,mi200",
                                 "--out", str(out), "--no-plot",
                                 "--workloads", "FIR,aes"])
        self.assertEqual(code, 0)
        rows = read_rows(out / "compare_complete.csv")
        fir = {r["series"]: float(r["speedup"]) for r in rows if r["workload"] == "fir"}
        self.assertEqual(fir, {"hpca": 4.0, "mi200": 2.0})
        self.assertEqual([r["workload"] for r in rows if r["series"] == "hpca"],
                         ["fir", "aes", "GMEAN"])

    def test_slim_copy_keeps_results(self):
        root = self.make_main(times={("fir", "m2"): "failed"})
        slim = self.tmp / "slim"
        quiet(plot.main, ["slim", str(root), str(slim)])
        full = plot.load_campaign(root)
        small = plot.load_campaign(slim)
        for key, cell in full.cells.items():
            self.assertEqual(small.cells[key].status, cell.status)
            self.assertEqual(small.cells[key].total_time, cell.total_time)
        self.assertEqual(quiet(plot.main, ["slim", str(root), str(slim)]), 2)

    def test_missing_directory_is_an_error(self):
        self.assertEqual(quiet(plot.main, ["table", str(self.tmp / "nope"),
                                           "--out", str(self.tmp / "o")]), 2)


class SharedGmeanTest(MainCampaignMixin):
    """Bars drawn side by side share one GMEAN workload set (as the monitor)."""

    def test_compare_partial_campaigns_use_common_workloads(self):
        # a: fir 4x, aes 2x, kmeans 2x.  b: only fir finished (2x).
        a = self.make_main("a", times={("fir", "complete"): 2.5e-5})
        b = self.make_main("b", times={("aes", "complete"): "missing",
                                       ("kmeans", "complete"): "failed"})
        out = self.tmp / "cmp"
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            code = plot.main(["compare", str(a), str(b), "--labels", "hpca,mi200",
                              "--out", str(out), "--no-plot"])
        self.assertEqual(code, 0)
        rows = read_rows(out / "compare_complete.csv")
        gm = {r["series"]: r for r in rows if r["workload"] == "GMEAN"}
        self.assertAlmostEqual(float(gm["hpca"]["speedup"]), 4.0)   # fir only
        self.assertAlmostEqual(float(gm["hpca"]["geomean_available"]),
                               (4.0 * 2.0 * 2.0) ** (1 / 3))
        self.assertAlmostEqual(float(gm["mi200"]["speedup"]), 2.0)
        self.assertEqual(gm["hpca"]["flag"], "partial")
        self.assertIn("1/3 workloads", gm["hpca"]["note"])
        self.assertIn("excluded: aes kmeans", gm["hpca"]["note"])
        sources = json.loads((out / "compare_complete_sources.json").read_text())
        self.assertEqual(sources["gmean"]["common"], ["fir"])
        self.assertEqual(sources["gmean"]["excluded"], ["aes", "kmeans"])
        text = stdout.getvalue()
        self.assertIn("GMEAN over 1/3 workloads OK in all 2 series with results; "
                      "excluded: AES, KM", text)
        self.assertIn("GMEAN hpca: 4.0000 (1/3); over its own 3/3: 2.5198", text)

    def test_series_without_results_is_left_out(self):
        root = self.make_main(times={(b, "complete"): "missing"
                                     for b in ("aes", "fir", "kmeans")}
                              | {("kmeans", "m1"): "failed"})
        campaign = plot.load_campaign(root)
        series = plot.ablation_series(campaign, ["baseline", "m1", "m2", "complete"],
                                       campaign.workloads)
        scope = plot.share_gmean(series)
        self.assertEqual(scope.common, ["aes", "fir"])
        self.assertEqual(scope.empty, ["Complete"])
        self.assertTrue(scope.partial)
        by_key = {s.key: s for s in series}
        self.assertIsNone(by_key["complete"].gmean)
        self.assertAlmostEqual(by_key["m2"].gmean, 2.0)
        self.assertEqual(plot.BarChart(campaign.workloads, series).group_labels()[-1],
                         "GMEAN*")

    def test_complete_campaign_label_has_no_star(self):
        campaign = plot.load_campaign(self.make_main())
        series = plot.ablation_series(campaign, ["baseline", "complete"],
                                      campaign.workloads)
        self.assertFalse(plot.share_gmean(series).partial)
        self.assertEqual(plot.BarChart(campaign.workloads, series).group_labels()[-1],
                         "GMEAN")


class SensitivityGmeanTest(SensitivityMixin):
    def test_dimension_gmeans_cover_the_same_workloads(self):
        # aes missing at l2-2mb: every l2 point's GMEAN is over fir only, while
        # row-locality keeps both workloads.
        sens = plot.load_campaign(self.make_sensitivity(
            missing={("aes", "l2-2mb_complete")}))
        main = plot.load_campaign(self.make_main())
        dims, _ = plot.build_sensitivity(sens, main)
        l2, row = dims
        self.assertEqual(l2.scope.common, ["fir"])
        self.assertEqual(l2.scope.excluded, ["aes"])
        self.assertEqual(row.scope.common, ["aes", "fir"])
        for series in l2.series:
            fir = [p for p in series.pairs if p.workload == "fir"][0]
            self.assertAlmostEqual(series.gmean, fir.speedup)
        out = self.tmp / "out"
        quiet(plot.main, ["sensitivity", str(sens.root), "--baseline-dir",
                          str(main.root), "--out", str(out), "--no-plot"])
        summary = read_rows(out / "sensitivity_summary.csv")
        l2_rows = [r for r in summary if r["dimension"] == "l2-size"]
        self.assertEqual({r["workloads"] for r in l2_rows}, {"1"})
        self.assertEqual({r["excluded"] for r in l2_rows}, {"aes"})
        sources = json.loads((out / "sensitivity_sources.json").read_text())
        self.assertEqual(sources["gmean"]["l2-size"]["excluded"], ["aes"])
        self.assertEqual(sources["gmean"]["row-locality"]["excluded"], [])


class Stage4SplitTest(TempDirTest):
    """isca27_stage4.sh: sens-filter (with --include-reference) on one machine,
    sens-l2-row (no reference) on the other; the reference comes from the first."""

    FILTER = [("filter-size", "filter-4k-buckets", 4096, 4, 2),
              ("filter-size", "filter-8k-buckets", 8192, 4, 2)]
    L2ROW = [("l2-size", "l2-1mb", 16384, 1, 2), ("l2-size", "l2-2mb", 16384, 2, 2),
             ("row-locality", "row-4-lines", 16384, 4, 4),
             ("row-locality", "row-8-lines", 16384, 4, 8)]
    REF = "main-16k-4mb-2-lines"

    def make(self, name, points, reference, flags=("-sampled",), times=None,
             metadata=True):
        root = self.campaign_dir(name)
        manifest = sensitivity_manifest(points, reference)
        Path(root, "SENSITIVITY_CAMPAIGN.json").write_text(json.dumps(manifest))
        cells, command_flags = [], {}
        for spec in manifest["points"]:
            for benchmark in ("aes", "fir"):
                for mode, time in (("baseline", 4e-4), ("complete", 2e-4)):
                    configuration = f"{spec['name']}_{mode}"
                    cells.append((benchmark, configuration))
                    command_flags[configuration] = list(flags)
                    time = (times or {}).get(configuration, time)
                    write_cell(root, benchmark, configuration, total_time=time)
        if metadata:
            write_metadata(root, cells, command_flags)
        return plot.load_campaign(root)

    def make_pair(self, **kwargs):
        sens_filter = self.make("sens-filter", self.FILTER, True, times={
            f"{self.REF}_baseline": 8e-4, f"{self.REF}_complete": 1e-4}, **kwargs)
        sens_l2row = self.make("sens-l2-row", self.L2ROW, False)
        return sens_filter, sens_l2row

    def test_reference_from_other_sensitivity_campaign(self):
        sens_filter, sens_l2row = self.make_pair()
        dims, notes = plot.build_sensitivity(sens_l2row, sens_filter)
        self.assertEqual([d.name for d in dims], ["l2-size", "row-locality"])
        for dim in dims:
            ref = [p for p in dim.points if p.reference]
            self.assertEqual(len(ref), 1)
            self.assertEqual(ref[0].source.name, "sens-filter")
            self.assertEqual(ref[0].variant, self.REF)
            self.assertEqual(ref[0].name, self.REF)
            series = dim.series[dim.points.index(ref[0])]
            self.assertTrue(series.complete)
            self.assertAlmostEqual(series.gmean, 8.0)   # 8e-4 / 1e-4
        self.assertEqual([p.tick() for p in dims[0].points], ["1", "2", "4"])
        self.assertEqual([p.tick() for p in dims[1].points], ["2", "4", "8"])
        self.assertTrue(any("from sensitivity campaign sens-filter "
                            f"({self.REF})" in n for n in notes), notes)
        # Same flags on both sides: no warning, nothing skipped silently.
        self.assertFalse([n for n in notes if n.startswith("WARNING")], notes)

    def test_fixed_normalization_uses_reference_baseline_of_other_campaign(self):
        sens_filter, sens_l2row = self.make_pair()
        dims, notes = plot.build_sensitivity(sens_l2row, sens_filter, "fixed")
        l2 = dims[0]
        # every point's complete (2e-4; reference 1e-4) over the reference's 8e-4
        self.assertEqual([round(s.gmean, 9) for s in l2.series], [4.0, 4.0, 8.0])
        for series in l2.series:
            for pair in series.pairs:
                self.assertEqual(pair.base.source, "sens-filter")
                self.assertEqual(pair.base.variant, self.REF)
        self.assertTrue(any(n == f"normalization: fixed baseline from sens-filter ({self.REF})"
                            for n in notes), notes)

    def test_flag_differences_are_reported(self):
        sens_filter, sens_l2row = self.make_pair(flags=("-sampled", "-extra-flag"))
        _, notes = plot.build_sensitivity(sens_l2row, sens_filter)
        self.assertTrue(any(n.startswith("WARNING reference differs")
                            and "-extra-flag" in n for n in notes), notes)

    def test_missing_commands_are_a_warning_not_silence(self):
        sens_filter, sens_l2row = self.make_pair(metadata=False)
        _, notes = plot.build_sensitivity(sens_l2row, sens_filter, "fixed")
        self.assertTrue(any(n.startswith("WARNING reference flags not checked")
                            for n in notes), notes)

    def test_sensitivity_campaign_without_reference_is_rejected(self):
        _, sens_l2row = self.make_pair()
        other = self.make("other", self.FILTER, False)
        with self.assertRaises(plot.CampaignError):
            plot.build_sensitivity(sens_l2row, other)

    def test_cli_split(self):
        sens_filter, sens_l2row = self.make_pair()
        out = self.tmp / "out"
        code = quiet(plot.main, ["sensitivity", str(sens_l2row.root), "--baseline-dir",
                                 str(sens_filter.root), "--out", str(out), "--no-plot"])
        self.assertEqual(code, 0)
        rows = [r for r in read_rows(out / "sensitivity_summary.csv")
                if r["reference"] == "True"]
        self.assertEqual(len(rows), 2)
        for row in rows:
            self.assertEqual((row["ok"], row["expected"], row["source"]),
                             ("2", "2", "sens-filter"))
            self.assertAlmostEqual(float(row["geomean"]), 8.0)


class LayoutTest(unittest.TestCase):
    def assert_no_overlap(self, positions, widths, gap=0.0):
        items = sorted(zip(positions, widths))
        for (x0, w0), (x1, w1) in zip(items, items[1:]):
            self.assertGreaterEqual(x1 - w1 / 2 - (x0 + w0 / 2), gap - 1e-9)

    def test_spread_keeps_free_labels_in_place(self):
        self.assertEqual(plot.spread_positions([0.0, 5.0], [1.0, 1.0]), [0.0, 5.0])

    def test_spread_resolves_clusters_symmetrically(self):
        positions = plot.spread_positions([1.0, 1.0, 1.1], [0.4, 0.4, 0.4], 0.05)
        self.assert_no_overlap(positions, [0.4] * 3, 0.05)
        self.assertAlmostEqual(sum(positions) / 3, 3.1 / 3)
        self.assertLess(positions[0], positions[1])

    def test_spread_respects_bounds(self):
        positions = plot.spread_positions([0.0, 0.05, 9.9], [0.5, 0.5, 0.5], 0.0,
                                          lower=0.0, upper=10.0)
        self.assertGreaterEqual(min(positions) - 0.25, -1e-9)
        self.assertLessEqual(max(positions) + 0.25, 10.0 + 1e-9)
        self.assert_no_overlap(positions, [0.5] * 3)

    def test_ticks_and_limits(self):
        self.assertEqual(plot.y_ticks(2.0), [0, 1, 2])
        self.assertEqual(plot.y_ticks(1.5), [0, 0.5, 1.0, 1.5])
        self.assertEqual(plot.auto_ymax([6.18, 1.0]), 7.5)
        self.assertGreaterEqual(plot.auto_ymax([1.2]), 1.2 * 1.08)

    def test_sequential_colors(self):
        self.assertEqual(plot.sequential_colors(3), list(plot.SENSITIVITY_COLORS[:3]))
        colors = plot.sequential_colors(6)
        self.assertEqual(len(set(colors)), 6)
        lightness = [sum(plot._hex_to_rgb(c)) for c in colors]
        self.assertEqual(lightness, sorted(lightness, reverse=True))

    def test_legend_rows_and_width(self):
        self.assertEqual(plot.legend_rows(5), [[0, 1, 2], [3, 4]])
        self.assertEqual(plot.legend_rows(4), [[0, 1, 2, 3]])
        self.assertEqual(plot.figure_width(15), plot.SINGLE_COLUMN)
        self.assertEqual(plot.figure_width(40), plot.DOUBLE_COLUMN)


@unittest.skipUnless(HAVE_MATPLOTLIB, "matplotlib not installed")
class DrawingTest(SensitivityMixin):
    def test_all_figures_render(self):
        sens = self.make_sensitivity(missing={("fir", "row-8-lines_complete")})
        main = self.make_main()
        out = self.tmp / "fig"
        self.assertEqual(quiet(plot.main, [
            "sensitivity", str(sens), "--baseline-dir", str(main), "--out", str(out)]), 0)
        self.assertEqual(quiet(plot.main, [
            "ablation", str(main), "--out", str(out), "--ymax", "auto"]), 0)
        self.assertEqual(quiet(plot.main, [
            "compare", str(main), str(main), "--labels", "a,b", "--out", str(out)]), 0)
        for stem in ("sensitivity_l2", "sensitivity_row", "sensitivity_workloads",
                     "sensitivity_geomean", "ablation", "compare_complete"):
            for suffix in (".pdf", ".png"):
                path = out / f"{stem}{suffix}"
                self.assertTrue(path.is_file() and path.stat().st_size > 1000, path)


@unittest.skipUnless(JULY_SENSITIVITY.is_dir(), "July sensitivity results absent")
class JulySmokeTest(unittest.TestCase):
    """The tool must reproduce the numbers behind the July paper figures."""

    @classmethod
    def setUpClass(cls):
        cls.campaign = plot.load_campaign(JULY_SENSITIVITY)

    def test_campaign_is_complete(self):
        self.assertEqual(self.campaign.kind, "sensitivity")
        self.assertEqual(self.campaign.status_counts()[plot.OK], 252)
        self.assertEqual(len(self.campaign.workloads), 14)

    def test_matched_geomeans_match_published_summary(self):
        dims, _ = plot.build_sensitivity(self.campaign)
        mine = {(d.name, p.name): s for d in dims for p, s in zip(d.points, d.series)}
        path = JULY_SENSITIVITY / "cupath_sensitivity_summary.csv"
        if not path.is_file():
            self.skipTest("published summary absent")
        rows = list(read_rows(path))
        self.assertEqual(len(rows), len(mine))
        for row in rows:
            series = mine[(row["dimension"], row["point"])]
            self.assertTrue(series.complete)
            self.assertTrue(math.isclose(series.gmean, float(row["paper_geomean"]),
                                         rel_tol=1e-12), row)

    def test_per_benchmark_speedups_match_published_detail(self):
        path = JULY_SENSITIVITY / "cupath_sensitivity_detail.csv"
        if not path.is_file():
            self.skipTest("published detail absent")
        dims, _ = plot.build_sensitivity(self.campaign)
        mine = {(d.name, p.name, pair.workload): pair.speedup
                for d in dims for p, s in zip(d.points, d.series) for pair in s.pairs}
        for row in read_rows(path):
            key = (row["dimension"], row["point"], row["benchmark"])
            self.assertTrue(math.isclose(mine[key], float(row["speedup"]),
                                         rel_tol=1e-12), row)

    @unittest.skipUnless(JULY_OBSERVATION.is_dir(), "July observation results absent")
    def test_fixed_old_baseline_matches_paper_script(self):
        """plot_cupath_sensitivity_old_baseline_bars.py: every point's complete,
        the reference point's included, over the 07-22 observation baseline."""
        def driver_time(path):
            with path.open(newline="", encoding="utf-8") as stream:
                for row in csv.reader(stream):
                    if len(row) >= 4 and row[1].strip() == "Driver" \
                            and row[2].strip() == "total_time":
                        return float(row[3])
            raise AssertionError(path)

        old = plot.load_campaign(JULY_OBSERVATION)
        dims, notes = plot.build_sensitivity(self.campaign, old, normalize="fixed")
        self.assertFalse([n for n in notes if "flags not checked" in n], notes)
        base = {b: driver_time(JULY_OBSERVATION / f"baseline_{b}_baseline_metrics.csv")
                for b in self.campaign.workloads}
        checked = 0
        for dim in dims:
            self.assertEqual(len(dim.scope.common), 14)
            for point, series in zip(dim.points, dim.series):
                self.assertEqual(point.source.name, self.campaign.name)
                for pair in series.pairs:
                    complete = driver_time(
                        JULY_SENSITIVITY
                        / f"baseline_{pair.workload}_{point.variant}_complete_metrics.csv")
                    self.assertTrue(pair.ok, pair)
                    self.assertTrue(math.isclose(pair.speedup, base[pair.workload] / complete,
                                                 rel_tol=1e-12))
                    checked += 1
                if point.reference:
                    self.assertTrue(math.isclose(series.gmean, 1.470328360413,
                                                 rel_tol=1e-11), series.gmean)
        self.assertEqual(checked, 14 * (8 + 3))   # 8 swept points + the reference in 3 panels

    @unittest.skipUnless(
        JULY_OBSERVATION.is_dir() and JULY_M1M2M3.is_dir() and JULY_SAMPLED.is_dir(),
        "July ablation results absent")
    def test_layered_ablation_matches_latest_paper_table(self):
        path = JULY_SAMPLED / "cupath_latest_ablation.csv"
        if not path.is_file():
            self.skipTest("published ablation table absent")
        merged = plot.merge_campaigns([plot.load_campaign(d) for d in
                                       (JULY_OBSERVATION, JULY_M1M2M3, JULY_SAMPLED)])
        series = plot.ablation_series(merged, list(plot.MODES), merged.workloads)
        published = {row["benchmark"]: row for row in read_rows(path)}
        for item in series:
            for pair in item.pairs:
                expected = float(published[pair.workload][f"{item.key}_speedup"])
                self.assertTrue(math.isclose(pair.speedup, expected, rel_tol=1e-12))
            self.assertTrue(math.isclose(
                item.gmean, float(published["geomean_14"][f"{item.key}_speedup"]),
                rel_tol=1e-12))


if __name__ == "__main__":
    unittest.main()
