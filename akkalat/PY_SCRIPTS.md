# Python Script Index

This directory has a few generations of experiment scripts. For current
Mechanism 1, use the clean runner first and treat older M1 reorder scripts as
legacy unless you are explicitly reproducing old trace/reorder experiments.

## Current Mechanism 1

- `run_m1_clean_compare.py`
  Runs a clean baseline vs current Mechanism 1 comparison. Mechanism 1 is only:
  `--l2-dir-batch-window` and `--l2-dram-access-unit-coalesce`.
  It reuses `runall2.py`'s experiment engine, writes both arms into one output
  directory with arm-prefixed metric names, and emits `summary.csv`.

## Main Runners

- `runall2.py`
  Self-contained benchmark runner. It owns benchmark/config presets, command
  construction, build, logging, timeout, dry-run, and process cleanup.
- `runllm.py`
  Wrapper for LLM-style workload presets through `runall2.py`.
- `runllm_decomposed.py`
  Runs decomposed BERT/GPT operator workloads.
- `run_l2source_collection.py`
  Runs L2-source collection sweeps.

## Legacy M1 / Reorder Tools

- `run_m1_baseline_vs_hlq.py`
  Legacy baseline vs L1V-bottom HLQ runner.
- `run_m1_overnight_sweep.py`
  Legacy overnight sweep for L1V-bottom reorder configs.
- `run_m1_large_trace_collection.py`
  Legacy large trace collection plus offline M1 analysis.
- `run_m1_experiments.py`
  Offline trace reorder experiments.
- `m1_parse_trace.py`
  Trace parsing helpers for offline M1 experiments.
- `m1_policies.py`
  Offline reorder policies.
- `m1_metrics.py`
  Offline reorder metrics.
- `compare_m1_sim_datapath.py`
  Compares baseline vs simulator-side datapath evidence from trace/metrics.

## Analysis Scripts

- `analyze_dram_saturation.py`
  Summarizes DRAM pressure from metrics CSVs.
- `analyze_hidden_latency.py`
  Estimates hidden/unhidden memory latency from cache metrics.
- `analyze_l2_home_evidence.py`
  Joins page-sharing traces with L2-source stats.
- `analyze_pipeline_costs.py`
  Aggregates data-pipeline costs from metrics.
- `analyze_sharing_trace.py`
  Analyzes compressed page-sharing traces.
- `summarize_results.py`
  Summarizes metrics files by benchmark/config.
- `summarize_runall2.py`
  Summarizes `runall2.py` result directories.
- `summarize_llm_decomposed.py`
  Sums decomposed BERT/GPT operator metrics.

## Workload Configs

- `bertconfig.py`
  Decomposed BERT workload profile.
- `gptconfig.py`
  Decomposed GPT workload profile.
- `gpt.py`
  Compatibility wrapper that re-exports `gptconfig.py`.

## Other Repository Python Files

These are outside top-level `akkalat/` and are not part of the current
Mechanism 1 runner path.

- `akita/mem/acceptance_test.py`
  Akita memory acceptance test helper.
- `akita/noc/acceptance/acceptance_test.py`
  Akita NoC acceptance test helper.
- `mgpusim/tests/deterministic/test.py`
  Deterministic mgpusim test helper.
- `mgpusim/samples/sampledrunner/testengine.py`
  Shared sampled benchmark test engine.
- `mgpusim/samples/sampledrunner/sampledanalysis.py`
  Sampled benchmark analysis helper.
- `mgpusim/samples/sampledrunner/sampledipc.py`
  Sampled IPC helper.
- `mgpusim/samples/sampledrunner/testallbench.py`
  Runs sampled all-benchmark tests.
- `mgpusim/samples/sampledrunner/testdlapps.py`
  Runs sampled DNN application tests.
- `mgpusim/samples/sampledrunner/testpagerank.py`
  Runs sampled PageRank tests.
- `mgpusim/samples/sampledrunner/clusterallvgg.py`
  VGG cluster runner helper.
- `mgpusim/samples/sampledrunner/to_excel.py`
  Exports sampled results to Excel.
- `mgpusim/samples/sampledrunner/my_utils.py`
  Shared sampled runner utilities.
- `mgpusim/samples/sampledrunner/bertconfig.py`
  BERT sampled runner config.
- `mgpusim/samples/sampledrunner/gptconfig.py`
  GPT sampled runner config.
- `mgpusim/samples/sampledrunner/resnet18config.py`
  ResNet-18 sampled runner config.
- `mgpusim/samples/sampledrunner/resnet34config.py`
  ResNet-34 sampled runner config.
- `mgpusim/samples/sampledrunner/resnet50config.py`
  ResNet-50 sampled runner config.
- `mgpusim/samples/sampledrunner/vgg16config.py`
  VGG-16 sampled runner config.
- `mgpusim/samples/sampledrunner/vgg19config.py`
  VGG-19 sampled runner config.

## Notes

- `bottleneck analysis/akkalat/` is a historical copy of many scripts. Prefer
  the top-level `akkalat/` scripts unless reproducing an old run.
- Syntax check used while cleaning:
  `python3 -m py_compile $(rg --files -g '*.py')`
