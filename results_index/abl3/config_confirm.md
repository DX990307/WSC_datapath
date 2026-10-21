# P16 第 3 步：9 个配置的确认（⏸ 等用户确认后才开跑消融）

Claude Code，2026-10-21。分支 `isca27/cupath-v3`，二进制 `2784487`（md5 `57b60e1ffda2f9dbc9af433faa6b239c`）。所有文件都 `extends: mi200.json`，`mi200.json` 没有改；`-magic-memory-copy=true` 来自 `hpca.json`（mi200 继承），下面每个已跑运行的 config_used.json 里实际值都是 true。

## 1. 相对 `mi200.json` 的差异

配置 2–9 共同加的参数（24 个，和 `complete_new.json` 相同的 typed filter 参数、旧 m1 / L2 机制全部关）：

`-dram-row-continuation-enable=false`, `-l2-adaptive-pair-enable=false`, `-l2-fill-forwarding-enable=false`, `-l2-filter-prefetch-enable=false`, `-l2-granularity-adaptation-enable=false`, `-l2-granularity-always-expand=false`, `-l2-granularity-predictor-only=false`, `-l2-granularity-without-filter=false`, `-l2-prefetch-predictor-only=false`, `-l2-prefetch-ungated=false`, `-l2-resident-filter-enable=false`, `-prefetch-predictor-entries=256`, `-remote-data-path-batch-lines=8`, `-remote-data-path-batches=64`, `-remote-data-path-dedup-enable=false`, `-remote-data-path-enable=true`, `-t18-window=64`, `-typed-filter-fingerprint-bits=13`, `-typed-filter-lookup-latency=1`, `-typed-filter-lookup-width=16`, `-typed-filter-mode=cuckoo`, `-typed-filter-slots-per-bucket=4`, `-typed-filter-update-latency=1`, `-typed-filter-update-width=16`

各配置在共同部分之外的差异（机制开关）：

| # | 文件 | 组合 | `cupath-dedup-owner` | `remote-data-path-batching-enable` | `remote-data-path-l2-enable` | `remote-filter-prefetch-enable` | `remote-reuse-kernel-invalidate` | `t18-credit-policy` | `t18-near-hops` | `t18-near-reserve` | `t18-owner-merge` | `t18-owner-multicast` | `t18-req-merge` |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | `mi200.json` | baseline | — | — | — | — | — | — | — | — | — | — | — |
| 2 | `v3_m1.json` | M1：合并 + 打包 + 分配（rtt） | m1 | true | false | true | false | rtt | （默认） | （默认） | true | false | true |
| 3 | `v3_m1m2.json` | M1 + M2 | m1 | true | false | true | false | rtt | （默认） | （默认） | true | true | true |
| 4 | `v3_m1m3.json` | M1 + M3 | m1 | true | true | true | true | rtt | （默认） | （默认） | true | false | true |
| 5 | `v3_complete.json` | M1 + M2 + M3（rtt） | m1 | true | true | true | true | rtt | （默认） | （默认） | true | true | true |
| 6 | `v3_complete_nomerge.json` | Complete 去掉合并 | none | true | true | true | true | rtt | （默认） | （默认） | false | false | false |
| 7 | `v3_complete_nopack.json` | Complete 去掉打包 | m1 | false | true | false | true | rtt | （默认） | （默认） | true | true | true |
| 8 | `v3_complete_alloc_shared.json` | Complete，分配 = shared | m1 | true | true | true | true | shared | （默认） | （默认） | true | true | true |
| 9 | `v3_complete_alloc_near.json` | Complete，分配 = near（≤ 3 跳，近区 16 项） | m1 | true | true | true | true | near | 3 | 16 | true | true | true |

说明：`remote-data-path-dedup-enable` 在 2–9 全部是 false（旧 m2 的 filter 去重不用）；合并开的配置 `cupath-dedup-owner=m1`，去掉合并的配置是 `none`（M1 合并和 M2 去重都关）；没有一个配置设 `t18-req-entries` / `t18-owner-entries` / `t18-dist-credits`。

## 2. 短运行里的 `[Config]` 行和实际生效的开关

workload：`floydwarshall -max-wg 4096`（小规模，只为看开关和统计是否生效），加 `-report-cupath-v3-stats -report-gpm-trace`（统计开关，不改时序）。“运行时开关”取自 metrics 里每个 RDMA 引擎报的开关量（48 个引擎都一致才记“一致”）。

### 1. `mi200.json`（baseline）

运行 `1021_diag_v3cfg-floydwarshall_mi200_2784487`：退出码 0，total_time 10.812 µs。

```
[Config] config files: .../akkalat/configs/mi200.json
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
```
实际值（config_used.json）：`remote-data-path-enable=false`，`remote-data-path-dedup-enable=true`，`remote-data-path-batching-enable=true`，`remote-filter-prefetch-enable=false`，`remote-data-path-l2-enable=true`，`remote-reuse-kernel-invalidate=false`，`t18-req-merge=false`，`t18-owner-merge=false`，`t18-owner-multicast=true`，`cupath-dedup-owner=`，`t18-window=0`，`t18-dist-credits=false`，`t18-credit-policy=`，`t18-near-reserve=16`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=0`

### 2. `v3_m1.json`（M1：合并 + 打包 + 分配（rtt））

运行 `1021_diag_v3cfg-floydwarshall_mi200+v3m1_2784487`：退出码 0，total_time 5.271 µs。

```
[Config] config files: .../akkalat/configs/v3_m1.json
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
[Config] M1+M2: T18 composed with the remote data path (dedup owner "m1": M1 merge=true, M2 dedup=false)
[Config] T18 req-merge=true owner-merge=true multicast=false window=64 dist-credits=true owner-window=0 req-entries=0 owner-entries=0 ways=4 fp-bits=12 max-kicks=32 req-waiters=0 bypass-when-full=false
[Config] T18 credit-policy=rtt v3-stats=true
```
实际值（config_used.json）：`remote-data-path-enable=true`，`remote-data-path-dedup-enable=false`，`remote-data-path-batching-enable=true`，`remote-filter-prefetch-enable=true`，`remote-data-path-l2-enable=false`，`remote-reuse-kernel-invalidate=false`，`t18-req-merge=true`，`t18-owner-merge=true`，`t18-owner-multicast=false`，`cupath-dedup-owner=m1`，`t18-window=64`，`t18-dist-credits=false`，`t18-credit-policy=rtt`，`t18-near-reserve=16`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=1`，`remote_dedup_enabled=0`，`remote_batching_enabled=1`，`remote_requester_l2_enabled=0`，`remote_filter_prefetch_enabled=1`，`t18_req_engine=1`，`t18_m1m2_composed=1`，`t18_req_merge_enabled=1`，`t18_owner_merge_enabled=1`，`t18_multicast_enabled=0`，`t18_dist_credits=1`，`t18_window=64`，`t18_req_table_slots=1024`，`t18_owner_table_slots=128`，`t18_v3_credit_policy_rtt=1`

统计计数（48 个 RDMA 引擎求和；只为确认统计在走，不是结果）：`remote_bitmap_lines=9859`，`remote_bitmap_packets=3003`，`remote_l2_probe_hits=0`，`remote_two_touch_replica_hits=0`，`t18_owner_merged=933`，`t18_owner_multicast_packets=0`，`t18_req_merged=13882`，`t18_v3_far_wait_while_full_ns=9407`，`t18_v3_mc_packets=0`，`t18_v3_mc_responses_saved=0`，`t18_v3_mc_unicast_fanout_packets=173`，`t18_v3_mid_wait_while_full_ns=3929`，`t18_v3_near_wait_while_full_ns=5599`，`t18_v3_owner_full_line_reads=11336`，`t18_v3_owner_merged=933`，`t18_v3_owner_merged_entries=792`，`t18_v3_owner_table_full_fallbacks=391`，`t18_v3_owner_table_full_ns=28`，`t18_v3_req_merged_into_queued=6713`，`t18_v3_req_merged_into_sent=7169`，`t18_v3_window_full_ns=30466`

### 3. `v3_m1m2.json`（M1 + M2）

运行 `1021_diag_v3cfg-floydwarshall_mi200+v3m1m2_2784487`：退出码 0，total_time 5.228 µs。

```
[Config] config files: .../akkalat/configs/v3_m1m2.json
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
[Config] M1+M2: T18 composed with the remote data path (dedup owner "m1": M1 merge=true, M2 dedup=false)
[Config] T18 req-merge=true owner-merge=true multicast=true window=64 dist-credits=true owner-window=0 req-entries=0 owner-entries=0 ways=4 fp-bits=12 max-kicks=32 req-waiters=0 bypass-when-full=false
[Config] T18 credit-policy=rtt v3-stats=true
```
实际值（config_used.json）：`remote-data-path-enable=true`，`remote-data-path-dedup-enable=false`，`remote-data-path-batching-enable=true`，`remote-filter-prefetch-enable=true`，`remote-data-path-l2-enable=false`，`remote-reuse-kernel-invalidate=false`，`t18-req-merge=true`，`t18-owner-merge=true`，`t18-owner-multicast=true`，`cupath-dedup-owner=m1`，`t18-window=64`，`t18-dist-credits=false`，`t18-credit-policy=rtt`，`t18-near-reserve=16`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=1`，`remote_dedup_enabled=0`，`remote_batching_enabled=1`，`remote_requester_l2_enabled=0`，`remote_filter_prefetch_enabled=1`，`t18_req_engine=1`，`t18_m1m2_composed=1`，`t18_req_merge_enabled=1`，`t18_owner_merge_enabled=1`，`t18_multicast_enabled=1`，`t18_dist_credits=1`，`t18_window=64`，`t18_req_table_slots=1024`，`t18_owner_table_slots=128`，`t18_v3_credit_policy_rtt=1`

统计计数（48 个 RDMA 引擎求和；只为确认统计在走，不是结果）：`remote_bitmap_lines=9823`，`remote_bitmap_packets=2990`，`remote_l2_probe_hits=0`，`remote_two_touch_replica_hits=0`，`t18_owner_merged=955`，`t18_owner_multicast_packets=796`，`t18_req_merged=13719`，`t18_v3_far_wait_while_full_ns=7972`，`t18_v3_mc_packets=796`，`t18_v3_mc_responses_saved=943`，`t18_v3_mc_unicast_fanout_packets=0`，`t18_v3_mid_wait_while_full_ns=3806`，`t18_v3_near_wait_while_full_ns=4975`，`t18_v3_owner_full_line_reads=11437`，`t18_v3_owner_merged=955`，`t18_v3_owner_merged_entries=796`，`t18_v3_owner_table_full_fallbacks=423`，`t18_v3_owner_table_full_ns=36`，`t18_v3_req_merged_into_queued=6485`，`t18_v3_req_merged_into_sent=7234`，`t18_v3_window_full_ns=28534`

### 4. `v3_m1m3.json`（M1 + M3）

运行 `1021_diag_v3cfg-floydwarshall_mi200+v3m1m3_2784487`：退出码 0，total_time 6.399 µs。

```
[Config] config files: .../akkalat/configs/v3_m1m3.json
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
[Config] M1+M2: T18 composed with the remote data path (dedup owner "m1": M1 merge=true, M2 dedup=false)
[Config] T18 req-merge=true owner-merge=true multicast=false window=64 dist-credits=true owner-window=0 req-entries=0 owner-entries=0 ways=4 fp-bits=12 max-kicks=32 req-waiters=0 bypass-when-full=false
[Config] T18 credit-policy=rtt v3-stats=true
```
实际值（config_used.json）：`remote-data-path-enable=true`，`remote-data-path-dedup-enable=false`，`remote-data-path-batching-enable=true`，`remote-filter-prefetch-enable=true`，`remote-data-path-l2-enable=true`，`remote-reuse-kernel-invalidate=true`，`t18-req-merge=true`，`t18-owner-merge=true`，`t18-owner-multicast=false`，`cupath-dedup-owner=m1`，`t18-window=64`，`t18-dist-credits=false`，`t18-credit-policy=rtt`，`t18-near-reserve=16`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=1`，`remote_dedup_enabled=0`，`remote_batching_enabled=1`，`remote_requester_l2_enabled=1`，`remote_filter_prefetch_enabled=1`，`t18_req_engine=1`，`t18_m1m2_composed=1`，`t18_req_merge_enabled=1`，`t18_owner_merge_enabled=1`，`t18_multicast_enabled=0`，`t18_dist_credits=1`，`t18_window=64`，`t18_req_table_slots=1024`，`t18_owner_table_slots=128`，`t18_v3_credit_policy_rtt=1`

统计计数（48 个 RDMA 引擎求和；只为确认统计在走，不是结果）：`remote_bitmap_lines=5311`，`remote_bitmap_packets=1549`，`remote_l2_probe_hits=397`，`remote_two_touch_replica_hits=399`，`t18_owner_merged=636`，`t18_owner_multicast_packets=0`，`t18_req_merged=13464`，`t18_v3_far_wait_while_full_ns=24592`，`t18_v3_mc_packets=0`，`t18_v3_mc_responses_saved=0`，`t18_v3_mc_unicast_fanout_packets=631`，`t18_v3_mid_wait_while_full_ns=17894`，`t18_v3_near_wait_while_full_ns=12914`，`t18_v3_owner_full_line_reads=10398`，`t18_v3_owner_merged=636`，`t18_v3_owner_merged_entries=518`，`t18_v3_owner_table_full_fallbacks=9`，`t18_v3_owner_table_full_ns=0`，`t18_v3_req_merged_into_queued=6412`，`t18_v3_req_merged_into_sent=7052`，`t18_v3_window_full_ns=68615`

### 5. `v3_complete.json`（M1 + M2 + M3（rtt））

**短运行 `1021_diag_v3cfg-floydwarshall_mi200+v3complete_2784487` 还没跑**（见文末“未完成”）。下面先用回归运行 `1021_diag_v3reg-floydwarshall_mi200+v3complete_2784487` 的输出：`floydwarshall -max-wg 32768`，没有开统计开关；配置文件相同。

运行 `1021_diag_v3reg-floydwarshall_mi200+v3complete_2784487`：退出码 0，total_time 29.176 µs。

```
[Config] config files: .../akkalat/configs/v3_complete.json
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
[Config] M1+M2: T18 composed with the remote data path (dedup owner "m1": M1 merge=true, M2 dedup=false)
[Config] T18 req-merge=true owner-merge=true multicast=true window=64 dist-credits=true owner-window=0 req-entries=0 owner-entries=0 ways=4 fp-bits=12 max-kicks=32 req-waiters=0 bypass-when-full=false
[Config] T18 credit-policy=rtt v3-stats=false
```
实际值（config_used.json）：`remote-data-path-enable=true`，`remote-data-path-dedup-enable=false`，`remote-data-path-batching-enable=true`，`remote-filter-prefetch-enable=true`，`remote-data-path-l2-enable=true`，`remote-reuse-kernel-invalidate=true`，`t18-req-merge=true`，`t18-owner-merge=true`，`t18-owner-multicast=true`，`cupath-dedup-owner=m1`，`t18-window=64`，`t18-dist-credits=false`，`t18-credit-policy=rtt`，`t18-near-reserve=16`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=1`，`remote_dedup_enabled=0`，`remote_batching_enabled=1`，`remote_requester_l2_enabled=1`，`remote_filter_prefetch_enabled=1`，`t18_req_engine=1`，`t18_m1m2_composed=1`，`t18_req_merge_enabled=1`，`t18_owner_merge_enabled=1`，`t18_multicast_enabled=1`，`t18_dist_credits=1`，`t18_window=64`，`t18_req_table_slots=1024`，`t18_owner_table_slots=128`

统计计数（48 个 RDMA 引擎求和；只为确认统计在走，不是结果）：`remote_bitmap_lines=20923`，`remote_bitmap_packets=6250`，`remote_l2_probe_hits=2085`，`remote_two_touch_replica_hits=2089`，`t18_owner_merged=3129`，`t18_owner_multicast_packets=2865`，`t18_req_merged=30897`

### 6. `v3_complete_nomerge.json`（Complete 去掉合并）

**短运行 `1021_diag_v3cfg-floydwarshall_mi200+v3nomerge_2784487` 还没跑**（见文末“未完成”）；文件内容和差异见第 1 节和单元测试 `V3ConfigTest`。

### 7. `v3_complete_nopack.json`（Complete 去掉打包）

运行 `1021_diag_v3cfg-floydwarshall_mi200+v3nopack_2784487`：退出码 0，total_time 6.534 µs。

```
[Config] config files: .../akkalat/configs/v3_complete_nopack.json
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
[Config] M1+M2: T18 composed with the remote data path (dedup owner "m1": M1 merge=true, M2 dedup=false)
[Config] T18 req-merge=true owner-merge=true multicast=true window=64 dist-credits=true owner-window=0 req-entries=0 owner-entries=0 ways=4 fp-bits=12 max-kicks=32 req-waiters=0 bypass-when-full=false
[Config] T18 credit-policy=rtt v3-stats=true
```
实际值（config_used.json）：`remote-data-path-enable=true`，`remote-data-path-dedup-enable=false`，`remote-data-path-batching-enable=false`，`remote-filter-prefetch-enable=false`，`remote-data-path-l2-enable=true`，`remote-reuse-kernel-invalidate=true`，`t18-req-merge=true`，`t18-owner-merge=true`，`t18-owner-multicast=true`，`cupath-dedup-owner=m1`，`t18-window=64`，`t18-dist-credits=false`，`t18-credit-policy=rtt`，`t18-near-reserve=16`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=1`，`remote_dedup_enabled=0`，`remote_batching_enabled=0`，`remote_requester_l2_enabled=1`，`remote_filter_prefetch_enabled=0`，`t18_req_engine=1`，`t18_m1m2_composed=1`，`t18_req_merge_enabled=1`，`t18_owner_merge_enabled=1`，`t18_multicast_enabled=1`，`t18_dist_credits=1`，`t18_window=64`，`t18_req_table_slots=1024`，`t18_owner_table_slots=128`，`t18_v3_credit_policy_rtt=1`

统计计数（48 个 RDMA 引擎求和；只为确认统计在走，不是结果）：`remote_bitmap_lines=0`，`remote_bitmap_packets=0`，`remote_l2_probe_hits=2`，`remote_two_touch_replica_hits=2`，`t18_owner_merged=541`，`t18_owner_multicast_packets=469`，`t18_req_merged=12580`，`t18_v3_far_wait_while_full_ns=0`，`t18_v3_mc_packets=469`，`t18_v3_mc_responses_saved=539`，`t18_v3_mc_unicast_fanout_packets=0`，`t18_v3_mid_wait_while_full_ns=0`，`t18_v3_near_wait_while_full_ns=0`，`t18_v3_owner_full_line_reads=9846`，`t18_v3_owner_merged=541`，`t18_v3_owner_merged_entries=469`，`t18_v3_owner_table_full_fallbacks=0`，`t18_v3_owner_table_full_ns=0`，`t18_v3_req_merged_into_queued=6797`，`t18_v3_req_merged_into_sent=5783`，`t18_v3_window_full_ns=103149`

### 8. `v3_complete_alloc_shared.json`（Complete，分配 = shared）

**短运行 `1021_diag_v3cfg-floydwarshall_mi200+v3shared_2784487` 还没跑**（见文末“未完成”）；文件内容和差异见第 1 节和单元测试 `V3ConfigTest`。

### 9. `v3_complete_alloc_near.json`（Complete，分配 = near（≤ 3 跳，近区 16 项））

**短运行 `1021_diag_v3cfg-floydwarshall_mi200+v3near_2784487` 还没跑**（见文末“未完成”）。下面先用回归运行 `1021_diag_v3reg-floydwarshall_mi200+v3near+r0_2784487` 的输出：`floydwarshall -max-wg 32768 -t18-near-reserve=0`（命令行把近区改成 0，用来做 near-reserve=0 == rtt 的回归），所以 `[Config]` 里是 near-reserve=0；正式配置是 16。

运行 `1021_diag_v3reg-floydwarshall_mi200+v3near+r0_2784487`：退出码 0，total_time 29.176 µs。

```
[Config] config files: .../akkalat/configs/v3_complete_alloc_near.json
[Config] command line overrides config file: -t18-near-reserve=0
[Config] L1V MSHR entries=16 max-concurrent-transactions=16
[Config] L2 capacity=2 MiB adaptive-pair-region=2 cachelines
[Config] GPM preset=mi200 CUs=28 (14 SA x 2) L2 partitions=8
[Config] DRAM model: channels=2 ranks=2 cmd-queue=16 trans-queue=64 tRFC=423 tFAW=26 act-cycles=2 address-layout strip-slice-interleave dual-command-bus row-hold pipelined-columns all-bank-refresh cross-rank-tRRD hbm2-turnaround
[Config] M1+M2: T18 composed with the remote data path (dedup owner "m1": M1 merge=true, M2 dedup=false)
[Config] T18 req-merge=true owner-merge=true multicast=true window=64 dist-credits=true owner-window=0 req-entries=0 owner-entries=0 ways=4 fp-bits=12 max-kicks=32 req-waiters=0 bypass-when-full=false
[Config] T18 credit-policy=near near-reserve=0 near-hops=3 v3-stats=false
```
实际值（config_used.json）：`remote-data-path-enable=true`，`remote-data-path-dedup-enable=false`，`remote-data-path-batching-enable=true`，`remote-filter-prefetch-enable=true`，`remote-data-path-l2-enable=true`，`remote-reuse-kernel-invalidate=true`，`t18-req-merge=true`，`t18-owner-merge=true`，`t18-owner-multicast=true`，`cupath-dedup-owner=m1`，`t18-window=64`，`t18-dist-credits=false`，`t18-credit-policy=near`，`t18-near-reserve=0`，`t18-near-hops=3`，`t18-req-entries=0`，`t18-owner-entries=0`，`magic-memory-copy=true`，`rdma-max-outstanding=64`，`gpm-preset=mi200`

运行时开关（RDMA metrics，48 个引擎一致）：`remote_data_path_enabled=1`，`remote_dedup_enabled=0`，`remote_batching_enabled=1`，`remote_requester_l2_enabled=1`，`remote_filter_prefetch_enabled=1`，`t18_req_engine=1`，`t18_m1m2_composed=1`，`t18_req_merge_enabled=1`，`t18_owner_merge_enabled=1`，`t18_multicast_enabled=1`，`t18_dist_credits=1`，`t18_window=64`，`t18_req_table_slots=1024`，`t18_owner_table_slots=128`

统计计数（48 个 RDMA 引擎求和；只为确认统计在走，不是结果）：`remote_bitmap_lines=20923`，`remote_bitmap_packets=6250`，`remote_l2_probe_hits=2085`，`remote_two_touch_replica_hits=2089`，`t18_owner_merged=3129`，`t18_owner_multicast_packets=2865`，`t18_req_merged=30897`



## 3. P16 2(b) 三种情况的核对（不需要改代码）

| 情况 | 依据（代码） | 运行里看到的 |
|---|---|---|
| M1 去掉合并、保留打包和分配时，请求方仍走 T18 的窗口和分配器 | `RequesterActive()` = req-merge 或 window > 0 或分配不是 shared；v3 文件都显式写 `t18-window: 64`，所以合并关掉以后 T18 请求方仍在，并接在 M2 通路上（组合模式，`m1CanSend` 走同一个分配器） | 配置 6 的短运行还没跑（第 4 节）。代码路径和配置 2–5、7 相同：它们的 `[Config] M1+M2: T18 composed ...` 行和 `t18_req_engine=1`、`t18_m1m2_composed=1`、`t18_window=64` |
| 不开 M2 时，合并过的 owner 项退回逐个单播 | `t18_owner.go`：`OwnerMulticast=false` 时，合并项按请求方逐个发 `DataReadyRsp`，bitmap 子请求回到各自的 bitmap 响应 | 配置 2（M1）和 4（M1 + M3）：`t18_multicast_enabled=0`，`t18_v3_mc_packets=0`，`t18_v3_mc_unicast_fanout_packets` > 0；配置 3：多播包 > 0、单播扇出 0 |
| 不开 M3 时，请求方 L2 不探测、不回填 | `remote-data-path-l2-enable=false` → `DisableRequesterL2`：探测（`probeEligible`）、SEEN/RESIDENT 提示查询、两次访问准入、回填（`shouldFill`）和 kernel 边界失效全部跳过 | 配置 2、3：`remote_requester_l2_enabled=0`，`remote_l2_probe_hits=0`，`remote_two_touch_replica_hits=0`；配置 4、7：探测命中和副本命中 > 0 |

## 4. 需要用户知道的几点

1. **组合模式下请求方表不是 128 槽，是 1,024 槽。** 运行里 `t18_req_table_slots=1024`，`t18_owner_table_slots=128`。原因（`m1m2_compose.go` 的 `m1ComposeInit`，`1d0cce4` 起就是这样）：M1 和 M2 组合时，M1 的在途表同时是 M2 的 line 表，大小取 max(2W = 128, M2 的 2 × 64 包 × 8 line = 1,024)。网络在途上限仍是 W = 64 个事务（一个 bitmap 包最多带 8 line，所以 line 数可以比 64 多）。owner 表是 128 槽。`complete_new` 一直是这样，P15 和停掉的正式运行也是。如果要求请求方 line 表也是 128 槽，要改代码（或设 `-t18-req-entries`，但长期规则不许），而且会改变"rtt 配置 == complete_new@64"的等价，**需要用户决定**；这次没有改。
2. **`[Config]` 输出里没有打包 / M3 的开关行。** 现有 `[Config]` 只打印 T18（合并、多播、窗口、分配）和"M1+M2 composed / dedup owner"两行；打包、预取、请求方 L2 的实际值只在 `config_used.json` 和每个 RDMA 的 metrics（`remote_batching_enabled`、`remote_filter_prefetch_enabled`、`remote_requester_l2_enabled`）里，上面第 2 节都列了。要在 `[Config]` 里也打印，只需加一行 print（不影响 metrics），但二进制会变、回归要重跑，所以这次没加；需要的话告诉我。
3. **统计口径：组合模式下"排队"按待发的包算。** `t18_v3_<组>_queued_*` 在组合模式里数的是还没发出的 M2 包（collecting batch）。配置 7（去掉打包）没有包，单个 line 在 RDMA 入口等名额，不进这个计数，所以配置 7 的排队统计是 0，**不能用**；它的 `t18_v3_window_full_ns`（窗口满的时间）和各组在途占用是有效的，等名额的时间可以用 obs-stats 的 `-report-obs-latency`（E1 的 wait_rrt，按跳数分）。要补的话是只加统计的小改动（同样要换二进制）。
4. **短运行只为看开关。** `floydwarshall -max-wg 4096` 是截断运行，`-report-gpm-trace` 里 kernel 没结束，所以没有"48 个 GPM 都在用"的时间占比；而且这个规模下有 2–4 个 GPM 没分到 WG。规模校准（P16 第 4 步）不在这次范围内。

## 5. 未完成（服务器被占满）

下面 6 个运行没有跑：两台服务器上我自己的目录以外有另一个会话的大量模拟（02:40 UTC 前后 yss2 上 33 个、yss3 上 29 个，其中大部分是 `~/akita-exp-test/v3run/` 用 `baseline_2784487` 跑的 `1021_abl_v3-*`，另有 `baseline_29d41c7` 和 fixlds 的运行），都超过每台 8 个的上限。按规则我不能再起新运行，也不能停别人的进程，所以停掉了自己的调度器。

| 运行 | 目的 |
|---|---|
| `1021_diag_v3cfg-floydwarshall_mi200+v3complete_2784487` | 配置 5 带统计的短运行（`[Config]` 已由回归运行给出） |
| `1021_diag_v3cfg-floydwarshall_mi200+v3nomerge_2784487` | 配置 6 的 `[Config]` 和开关 |
| `1021_diag_v3cfg-floydwarshall_mi200+v3shared_2784487` | 配置 8 的 `[Config]` 和开关 |
| `1021_diag_v3cfg-floydwarshall_mi200+v3near_2784487` | 配置 9（近区 16 项）的 `[Config]`、开关和近区统计 |
| `1021_diag_v3cfg-floydwarshall_mi200+v3complete+nostats_2784487` | 统计开关开 / 关的 total_time 对比 |
| `1021_diag_v3reg-floydwarshall_mi200+v3shared_2784487` | shared 回归（对照 `8446d36` 的 complete_new + W64 + 关 dist-credits 已跑完：29.991 µs，md5 `e17fdca2ad5f`） |

命令在 `~/akita-exp-test/v3impl/` 下用 `launch.sh` 起（和已跑的同一套），服务器空出来以后补跑即可。
