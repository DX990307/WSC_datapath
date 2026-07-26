# CuPath Filter Authoritative Validation Prompt

请开启 Goal Mode，持续完成以下实验，在完整性审计通过前不要停止。

1. Checkout 用户指定的 GitHub commit，并禁止修改任何代码。
2. 只运行 `traditional` 中的 14 个 benchmark 和 `Complete` 配置，不运行 Baseline 或其他 ablation。
3. 保持 `max-wg=78600`、L1 MSHR 为 16、L1 concurrency 为 16，不使用 sampling flags。
4. 禁止修改 workgroup 的生成、ID、内容、GPU 分配方式和终止逻辑。
5. 开启 `-typed-filter-authoritative-audit=true`。诊断 exact checks 不增加模拟 latency，发现 false negative 时必须 fail open。
6. `runall2.py` 只构建一次并冻结 binary，全部 14 个实验必须使用同一个 SHA-256。
7. 最多并行 14 个实验。初始运行 1 个。仅当 `MemAvailable` 大于 50 GiB 时增加实验，每 30 分钟检查一次。
8. 保存 `EXPERIMENT_METADATA.json`、`EXPERIMENT_BINARIES.json`、全部 result JSON 和 metrics CSV。
9. 完成后确认恰好有 14 个成功的 Complete metrics，且每个实验都完成 78,600 个 WG。
10. 运行 `plot_filter_effectiveness.py`。报告三个位置的 verified-safe bypass、exact access 和 authoritative false negative。禁止把原始 Filter-negative decision 当作 verified-safe bypass。
11. Requester-L2 authoritative unavailable 必须为 0。任何 false negative 都必须单独报告并从 safe bypass 分子中排除。
12. 最后给出执行命令、commit、binary SHA-256、结果目录、完整性审计和绘图文件路径。

```bash
cd /path/to/WSC_datapath
git fetch origin
git checkout cupath/filter-authoritative-validation-20260726
git pull --ff-only

python3 akkalat/runall2.py \
  --remote-ablation \
  --configs=complete \
  --benchmarks=traditional \
  --output-dir=akkalat/results/2026-07-26-filter-authoritative-validation \
  --max-workers=14 \
  --memory-reserve-gib=50 \
  --initial-workers=1 \
  --memory-scan-minutes=30 \
  --max-wg=78600 \
  --extra-benchmark-flags='-typed-filter-authoritative-audit=true'

python3 akkalat/plot_filter_effectiveness.py \
  akkalat/results/2026-07-26-filter-authoritative-validation
```
