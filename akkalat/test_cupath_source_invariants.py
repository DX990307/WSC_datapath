import csv
import hashlib
import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
HOT_PATHS = (
    ROOT / "akita/mem/cache/writeback/local_filter_prefetch.go",
    ROOT / "akita/mem/cache/writeback/prefetch_predictor.go",
    ROOT / "akita/mem/cache/writeback/typed_filter.go",
    ROOT / "akita/mem/cache/writeback/remote_replica.go",
    ROOT / "mgpusim/timing/rdma/remote_datapath_types.go",
    ROOT / "mgpusim/timing/rdma/remote_requester.go",
    ROOT / "mgpusim/timing/rdma/remote_owner.go",
)


class CuPathSourceInvariantTest(unittest.TestCase):
    def test_frozen_m2_m3_sources_match_audited_hashes(self):
        rdma_files = sorted((ROOT / "mgpusim/timing/rdma").glob("*.go"))
        manifest = "".join(
            f"{hashlib.sha256(path.read_bytes()).hexdigest()}  "
            f"{path.relative_to(ROOT).as_posix()}\n"
            for path in rdma_files
        ).encode("utf-8")
        self.assertEqual(
            hashlib.sha256(manifest).hexdigest(),
            "55b2a7766520219f02254febf10b8a8b05417de7177c6b361c3b21009d283936",
        )
        for relative, expected in {
            "akita/mem/cache/writeback/remote_replica.go":
                "584af2a852619ec0b00421cc93f18cbd68cac44ac27b3059f42a4ccf619d8ba3",
            "akita/mem/cache/writeback/remote_replica_test.go":
                "db22eda8482b0c8681c0f6e6e478cbc7f9fbed60760f0c7eec5bded577109ff1",
        }.items():
            self.assertEqual(
                hashlib.sha256((ROOT / relative).read_bytes()).hexdigest(),
                expected,
            )

    def test_max_wg_counts_completed_wgs_without_changing_launch_mapping(self):
        driver_dir = ROOT / "mgpusim/driver"
        production = "\n".join(
            path.read_text(encoding="utf-8", errors="replace")
            for path in driver_dir.glob("*.go")
            if not path.name.endswith("_test.go")
        )
        for forbidden in (
            "workGroupLimit",
            "admitWorkGroups",
            "prefixWorkGroupFilter",
            "SetWorkGroupLimit",
            "limited from",
        ):
            self.assertNotIn(forbidden, production)

        runner = "\n".join(
            (ROOT / "akkalat/baseline/runner" / name).read_text(
                encoding="utf-8"
            )
            for name in (
                "tracers.go",
                "wgtracer.go",
                "flag.go",
                "runner.go",
                "report.go",
            )
        )
        self.assertIn('task.What != "*protocol.WGCompletionMsg"', runner)
        self.assertNotIn('task.What == "*protocol.MapWGReq"', runner)
        self.assertIn("r.maxWGStopper = newWGStopper(*maxWGCount)", runner)
        self.assertIn("[Runner] reached max-wg=", runner)
        self.assertIn("atexit.Exit(0)", runner)
        self.assertIn('"max_wg_launch_limited", 0', runner)
        self.assertIn('"max_wg_kernel_drained", 0', runner)

    def test_cacti_outputs_and_paper_cost_remain_consistent(self):
        cost_dir = ROOT / "akkalat/cost/cacti32"

        def read_cacti_row(name):
            with (cost_dir / name).open(newline="", encoding="utf-8") as stream:
                rows = list(csv.reader(stream))
            self.assertEqual(len(rows), 2)
            return {
                key.strip(): value.strip()
                for key, value in zip(rows[0], rows[1])
                if key.strip()
            }

        filter_row = read_cacti_row("cupath_filter_output.txt")
        l2_row = read_cacti_row("cupath_l2_slice_output.txt")
        self.assertEqual(int(filter_row["Capacity (bytes)"]), 32768 * 21 // 8)
        self.assertEqual(int(l2_row["Capacity (bytes)"]), 1024 * 1024)
        area_ratio = (
            float(filter_row["Area (mm2)"])
            / float(l2_row["Area (mm2)"])
            * 100
        )
        self.assertAlmostEqual(area_ratio, 4.3296, places=4)

        cost = (ROOT / "akkalat/cost/CUPATH_CACTI_AREA.md").read_text(
            encoding="utf-8"
        )
        paper = (
            ROOT
            / "weeklyreport/hpca2027-latex-template 2/sections/evaluation.tex"
        ).read_text(encoding="utf-8")
        self.assertIn("2.396247", cost)
        self.assertIn("4.40%", cost)
        self.assertIn(r"4.40\%", paper)

    def test_predictor_placement_and_local_early_issue_match_source(self):
        cache_builder = (
            ROOT / "akita/mem/cache/writeback/builder.go"
        ).read_text(encoding="utf-8")
        local_prefetch = (
            ROOT / "akita/mem/cache/writeback/local_filter_prefetch.go"
        ).read_text(encoding="utf-8")
        platform_builder = (
            ROOT / "akkalat/baseline/runner/r9nanobuilder.go"
        ).read_text(encoding="utf-8")
        requester = (
            ROOT / "mgpusim/timing/rdma/remote_requester.go"
        ).read_text(encoding="utf-8")
        predictor = (
            ROOT / "akita/mem/cache/writeback/prefetch_predictor.go"
        ).read_text(encoding="utf-8")
        coalescer = (
            ROOT / "mgpusim/timing/cu/defaultcoalescer.go"
        ).read_text(encoding="utf-8")
        granularity = (
            ROOT / "akita/mem/cache/writeback/granularity_adaptation.go"
        ).read_text(encoding="utf-8")
        rdma_production = "\n".join(
            path.read_text(encoding="utf-8", errors="replace")
            for path in (ROOT / "mgpusim/timing/rdma").glob("*.go")
            if not path.name.endswith("_test.go")
        )
        writeback = (
            ROOT / "akita/mem/cache/writeback/writebufferstage.go"
        ).read_text(encoding="utf-8")
        all_go = "\n".join(
            path.read_text(encoding="utf-8", errors="replace")
            for module in (ROOT / "akita", ROOT / "mgpusim")
            for path in module.rglob("*.go")
            if not path.name.endswith("_test.go")
        )
        self.assertIn(
            "cacheModule.filterPrefetcher = NewDemandStridePredictor(",
            cache_builder,
        )
        self.assertIn(
            "c.remotePrefetcher = writeback.NewPageLocalDemandStridePredictor(",
            requester,
        )
        # One local predictor is shared by the four L2 slices in each GPM.
        self.assertEqual(all_go.count("ConnectFilterCoupledPrefetchGroup("), 1)
        self.assertIn("writeback.ConnectFilterCoupledPrefetchGroup(", platform_builder)
        # Earlier candidate issue and exponential late convergence belong to
        # the quarantined independent-prefetch diagnostic. Requester-RDMA and
        # the formal paired-read M1 keep conservative/direct-sibling policy.
        self.assertIn(
            "predictor.EnableCandidateOnPatternEstablishment()", local_prefetch
        )
        self.assertIn("predictor.EnableExponentialLateLookahead()", local_prefetch)
        self.assertNotIn("EnableCandidateOnPatternEstablishment", requester)
        self.assertNotIn("EnableExponentialLateLookahead", requester)
        granularity_builder = cache_builder[
            cache_builder.index("if b.granularityAdaptation {"):
            cache_builder.index("cacheModule.granularityLeader = true")
        ]
        self.assertNotIn(
            "EnableExponentialLateLookahead", granularity_builder
        )
        # Ordinary requester-RDMA learning remains PC/position scoped. M1's
        # separate LocalStreamID groups the coalesced footprint by static PC,
        # and a stream cannot pipeline speculative training requests.
        self.assertIn("StreamID: read.StreamID", local_prefetch)
        self.assertIn("req.StreamID = memoryStreamID(inst.PC, position)", coalescer)
        self.assertIn(
            "req.LocalStreamID = localMemoryStreamID(wf, inst.PC, position)",
            coalescer,
        )
        local_stream_fn = coalescer[
            coalescer.index("func localMemoryStreamID("):
            coalescer.index("func (c defaultCoalescer) generateWriteTransactions")
        ]
        self.assertIn("return pc", local_stream_fn)
        self.assertNotIn("SIMDID", local_stream_fn)
        self.assertNotIn("VRegOffset", local_stream_fn)
        self.assertIn("streamID := read.LocalStreamID", granularity)
        self.assertNotIn("LocalStreamID", rdma_production)
        self.assertIn("pairProbeOnly := !c.granularityAlwaysExpand", granularity)
        self.assertIn("candidate.pairProbeBypass = true", granularity)
        self.assertIn("c.requestFilter.Query(TypedFilterKey{", granularity)
        self.assertIn("markLocalPairFollowers(reqs", coalescer)
        self.assertNotIn("LocalPairHint", rdma_production)
        self.assertIn("predictor.EnableFeedbackGatedIssue()", granularity)
        self.assertIn(
            "cacheModule.granularityPredictor.EnableFeedbackGatedIssue()",
            granularity_builder,
        )
        self.assertNotIn("EnableFeedbackGatedIssue", requester)
        self.assertIn("for position, req := range reqs", coalescer)
        self.assertIn("WithStreamID(accessReqStreamID", writeback)
        self.assertIn("func (p *DemandStridePredictor) CanIssue", predictor)
        self.assertIn("trainingPending", predictor)
        self.assertNotIn("EnableDemandValidationBeforeIssue", predictor)
        self.assertNotIn("RequireDemandValidatedLocalPrefetch", all_go)
        self.assertIn("localPrefetchOutstanding >= 1", local_prefetch)
        self.assertIn("predictor.EnablePageFrontierClamping()", local_prefetch)
        self.assertIn(
            "patternFilter, target.requestFilter, target.requestFilter",
            local_prefetch,
        )
        self.assertNotIn(
            "c.localPrefetchPatternFilters[observation.Token.Key] =\n"
            "\t\t\ttarget.requestFilter",
            local_prefetch,
        )
        self.assertIn("residentFilterChecked:  !candidate.ungated", local_prefetch)
        self.assertIn(
            "trans.prefetch && trans.residentFilterChecked && trans.residentFastMiss",
            (ROOT / "akita/mem/cache/writeback/directorystage.go").read_text(
                encoding="utf-8"
            ),
        )

    def test_fixed_l2_geometry_and_latency_remain_explicit(self):
        source = (ROOT / "akkalat/baseline/runner/r9nanobuilder.go").read_text(
            encoding="utf-8"
        )
        platform = (
            ROOT / "akkalat/baseline/runner/timingplatform.go"
        ).read_text(encoding="utf-8")
        for fragment in (
            "log2CacheLineSize:              6",
            "l2CacheSize:                    4 * mem.MB",
            "l1vMSHREntries:                 16",
            "byteSize := b.l2CacheSize / uint64(b.numMemoryBank)",
            "WithWayAssociativity(16)",
            "WithNumMSHREntry(64)",
            "WithNumReqPerCycle(16)",
            "WithDirectoryLatency(10)",
            "for i := 0; i < b.numMemoryBank; i++",
        ):
            self.assertIn(fragment, source)
        self.assertIn("l2CacheSize:               4 * mem.MB", platform)
        self.assertIn("WithL2CacheSize(b.l2CacheSize)", platform)
        self.assertIn("WithL2CacheSizeMB(*l2CacheSizeMBFlag)", (
            ROOT / "akkalat/baseline/runner/runner.go"
        ).read_text(encoding="utf-8"))

    def test_formal_m1_restores_one_aligned_128b_hbm_request(self):
        rdma_production = "\n".join(
            path.read_text(encoding="utf-8", errors="replace")
            for path in (ROOT / "mgpusim/timing/rdma").glob("*.go")
            if not path.name.endswith("_test.go")
        )
        source = "\n".join(
            path.read_text(encoding="utf-8", errors="replace")
            for directory in (
                ROOT / "akita/mem/cache/writeback",
                ROOT / "akita/mem/cache/writearound",
                ROOT / "akita/mem/dram",
                ROOT / "mgpusim/timing/rdma",
                ROOT / "akkalat/baseline/runner",
            )
            for path in directory.rglob("*.go")
            if not path.name.endswith("_test.go")
        )
        self.assertRegex(source, r"remoteLineBytes\s*=\s*uint64\(64\)")
        writebuffer = (
            ROOT / "akita/mem/cache/writeback/writebufferstage.go"
        ).read_text(encoding="utf-8")
        adapter = (
            ROOT / "akita/mem/cache/writeback/adaptive_pair_adapter.go"
        ).read_text(encoding="utf-8")
        self.assertIn("regionBytes := uint64(a.regionLines) * lineBytes", adapter)
        self.assertIn("WithByteSize(regionBytes)", adapter)
        self.assertIn("l2AdaptivePairRegionLines: 2", (
            ROOT / "akkalat/baseline/runner/timingplatform.go"
        ).read_text(encoding="utf-8"))
        self.assertIn("WithAddress(base)", adapter)
        self.assertIn("bottomSender.Send(read)", adapter)
        self.assertIn("samePairedReadAggregate", source)
        self.assertIn("dramBusWidth := 256", source)
        self.assertIn("WithBurstLength(4)", source)
        self.assertIn("WithAggregateContinuation", source)
        self.assertIn("FilterGranularityPending", source)
        self.assertIn("pairProbeOnly", source)
        self.assertIn("DemandPairFilterProbes", source)
        self.assertNotIn("pairProbeOnly", rdma_production)
        local_test = (
            ROOT / "akita/mem/cache/writeback/local_filter_prefetch_test.go"
        ).read_text(encoding="utf-8")
        self.assertIn("TestLocalFilterPrefetchNeedsPatternAndIssuesOne64BLine", local_test)

    def test_no_paper_benchmark_name_appears_in_memory_hot_paths(self):
        sources = []
        for directory in (
            ROOT / "akita/mem/cache/writeback",
            ROOT / "akita/mem/dram",
            ROOT / "mgpusim/timing/rdma",
            ROOT / "akkalat/baseline/runner",
        ):
            sources.extend(
                path.read_text(encoding="utf-8", errors="replace")
                for path in directory.rglob("*.go")
            )
        pattern = re.compile(
            r"\b(aes|bitonicsort|fastwalshtransform|fft|fir|floydwarshall|"
            r"kmeans|matrixmultiplication|matrixtranspose|pagerank|relu|"
            r"simpleconvolution|spmv|im2col)\b",
            re.IGNORECASE,
        )
        self.assertIsNone(pattern.search("\n".join(sources)))

    def test_rdma_reuses_l2_filters_and_existing_l2_data_storage(self):
        builder = (ROOT / "akkalat/baseline/runner/r9nanobuilder.go").read_text(
            encoding="utf-8"
        )
        rdma = (ROOT / "mgpusim/timing/rdma/comp.go").read_text(
            encoding="utf-8"
        )
        replica = (ROOT / "akita/mem/cache/writeback/remote_replica.go").read_text(
            encoding="utf-8"
        )
        self.assertIn("requestFilters = append(requestFilters, l2.RequestFilter())", builder)
        self.assertIn("b.rdmaEngine.SetRequestFilters(", builder)
        self.assertIn("It does not allocate a per-RDMA filter", rdma)
        self.assertNotIn("NewTypedCuckooFilter", rdma)
        self.assertNotIn("NewStorage", rdma + replica)
        self.assertIn("c.storage.Write", replica)

    def test_paper_uses_current_cupath_story_and_unambiguous_l2_name(self):
        paper_root = ROOT / "weeklyreport/hpca2027-latex-template 2"
        tex = "\n".join(
            path.read_text(encoding="utf-8", errors="replace")
            for path in (
                paper_root / "main.tex",
                *sorted((paper_root / "sections").glob("*.tex")),
            )
        )
        self.assertIn(
            "CuPath: Cuckoo-Filter-Guided Memory Request Transformation",
            tex,
        )
        self.assertIn("existing requester L2", tex)
        self.assertIn("does not add an L1.5 cache", tex)
        for stale in ("CuBatch", "HDPAT", "HLQ"):
            self.assertNotIn(stale, tex)
        self.assertNotIn("requester cache", tex.lower())
        # A 128-bit full-channel data bus is a hardware fact. Reject byte-sized
        # request/transaction claims without also rejecting that bus width.
        self.assertNotRegex(tex, r"(?i)(128\s*B\b|128-B(?!it)|128-byte)")

    def test_v5_formal_campaign_is_preserved_but_quarantined(self):
        formal = (
            ROOT
            / "akkalat/results/2026-07-17-filter-prefetch-v5-formal14"
        )
        if not formal.exists():
            self.skipTest("historical result directories are not tracked by Git")
        cells = list(formal.glob("baseline_*_metrics.csv"))
        self.assertEqual(len(cells), 70)
        quarantine = formal / "INVALID_FOR_FINAL_PAPER.md"
        self.assertTrue(quarantine.is_file())
        text = quarantine.read_text(encoding="utf-8")
        self.assertIn("INVALID FOR FINAL PAPER", text)
        self.assertIn("pre-dispatch", text)
        self.assertIn("70/70", text)
        historical_docs = {
            name: (ROOT / "akkalat/docs" / name).read_text(encoding="utf-8")
            for name in (
                "CUPATH_COMPLETION_AUDIT.md",
                "CUPATH_REMAINING_RISKS.md",
                "CUPATH_VALIDATION.md",
                "FILTER_COUPLED_PREFETCH_BASELINE.md",
            )
        }
        for name, historical in historical_docs.items():
            self.assertIn("HISTORICAL V5", historical, name)
            self.assertIn("INVALID FOR THE FINAL PAPER", historical, name)
            self.assertIn("CUPATH_V6_GOAL_AUDIT.md", historical, name)

    def test_old_controlled_runtime_is_diagnostic_only(self):
        runtime_report = (
            ROOT
            / "akkalat/results/2026-07-19-filter-prefetch-v5-runtime-screen-wg192"
            / "CUPATH_CONTROLLED_RUNTIME.md"
        )
        if not runtime_report.exists():
            return
        report = runtime_report.read_text(encoding="utf-8")
        self.assertIn("not a formal hardware-performance result", report)

    def test_completion_audit_references_existing_go_tests(self):
        audit = (ROOT / "akkalat/docs/CUPATH_COMPLETION_AUDIT.md").read_text(
            encoding="utf-8"
        )
        documented = set(re.findall(r"`(Test[A-Za-z0-9_]+)`", audit))
        existing = set()
        for module in (ROOT / "akita", ROOT / "mgpusim", ROOT / "akkalat"):
            for path in module.rglob("*_test.go"):
                existing.update(re.findall(
                    r"^func (Test[A-Za-z0-9_]+)\(",
                    path.read_text(encoding="utf-8", errors="replace"),
                    re.MULTILINE,
                ))
        self.assertGreaterEqual(len(documented), 20)
        self.assertEqual(sorted(documented - existing), [])


if __name__ == "__main__":
    unittest.main()
