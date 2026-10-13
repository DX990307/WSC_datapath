# T0 实验脚本基础设施

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]` · **周次**：W1（最先做） · **依赖**：无 · **被依赖**：所有需要跑实验的任务

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027，摘要约 11/9、全文约 11/16。平台是在 MGPUSim 上扩展的 48 GPM wafer-scale GPU（7×7 mesh）。
> **工作流（重复）**：本机改代码 → 推到两台服务器跑（GNU parallel）→ 用 rsync 把结果拉回本机的 `results/`。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 为什么做

接下来 5 周要跑几百个配置组合：机制 × workload × 配置 × placement，分布在两台服务器上。两周后再看到某个数字时，必须能查出它来自哪一版代码、哪套参数。如果做不到，审稿人追问时我们自己都答不上来。

## 要做什么

1. **`run_exp.sh <mechanism> <workload> <config> [placement]`**（在服务器上运行）
   - 第一步检查 `git status --porcelain`，输出非空就报错退出
   - 生成 `exp_id`，格式为 `<MMDD>_<workload>_<config>_<mechanism>_<短hash>`；如果 placement 不是默认值，在末尾再加上 placement
   - 创建 `results/<exp_id>/`，在里面写 `meta.json`、`summary.csv`、`stats/`、`stdout.log`、`stderr.log`
   - 机制、workload、配置都通过 MGPUSim 的命令行参数或配置文件选择，**不能靠改代码切换**
2. **`sync_servers.sh`**（在本机运行）：确认工作区干净 → 把当前 commit 同步到两台服务器（git push 后在服务器上 pull，或用 rsync）→ 在两台服务器上编译 → 打印两边的 commit hash 和 Go 版本，并确认两边一致
3. **`pull_results.sh`**（在本机运行）：用 rsync 把两台服务器的 `results/` 拉回本机，跳过已经存在的目录
4. **`launch.sh`**（在本机运行）：包装 GNU parallel：
   ```bash
   parallel --sshloginfile servers.txt --joblog runs.log --resume-failed \
     'cd ~/cupath && ./run_exp.sh {1} {2} {3} {4}' \
     ::: $MECHS ::: $WORKLOADS ::: $CONFIGS ::: $PLACEMENTS
   ```
   `servers.txt` 每行的格式是 `N/user@host`，N 表示这台机器同时跑几个任务。N 要根据内存来设：先量一次单个任务的峰值内存。
5. `servers.txt` 加进 `.gitignore`。`runs.log` 每批备份一份到 `results/_joblogs/`。

## `meta.json` 必须包含的字段

`exp_id, git_commit, git_dirty(必须为 false), hostname, start_time, end_time, go_version, mgpusim_args(完整命令行), config_name, config_params(展开后的全部参数), workload_name, workload_params(输入规模、数据集路径、随机种子), mechanism, placement, exit_status`

## `summary.csv` 的字段（每个实验一行，加字段只在末尾追加）

```
exp_id,git_commit,workload,config,mechanism,placement,cycles,kernels_total,kernels_completed,
wg_completed,touched_footprint_bytes,l2_local_hit_rate,remote_reads,remote_read_frac,
req_packets,resp_packets,net_bytes,hbm_bw_util_mean,ni_table_occ_mean,
m1_shared_ge2_frac,m1_region_shared_frac,m2_cross_kernel_reuse_frac
```
暂时还采集不到的字段留空，**不要填 0**。这些统计的来源见 T2。

## 验收标准

- [ ] 工作区不干净时，`run_exp.sh` 会拒绝运行
- [ ] 用一个现有的 benchmark（比如 MM）在两台服务器上各跑一次，得到的周期数完全一致（确定性检查）
- [ ] `pull_results.sh` 执行之后，本机的 `results/` 里有这两个实验的完整目录
- [ ] 在 `STATUS.md` 里记录单个任务的峰值内存、每台服务器建议的 N 值，以及 MM 跑一次需要的墙钟时间

## 注意

- **服务器上不允许改任何代码。**
- 不要把 `results/` 里的大文件提交进 git；`results/` 是否纳入版本管理由用户决定，先加进 `.gitignore`。
