# M1: Cuckoo-Filter-Guided L2 Bypass and DRAM Batch Helper

## 0. One-Sentence Goal

M1 adds a Cuckoo-filter-based L2 residency predictor between L1 and L2 so that L1 misses predicted to be absent from L2 can bypass the normal L2 access path and enter a DRAM-friendly batch helper earlier, while requests predicted to be L2-resident still use the L2 path.

The intended paper claim is not "we add another prefetcher" or "we add another memory scheduler". The claim is:

> Enhanced L1V exposes a large cacheline request stream, but FIFO L1-to-L2 handling wastes time and destroys DRAM-row locality for requests that are very unlikely to hit in L2. A no-false-negative L2 residency filter lets the datapath split this stream before L2 into L2-likely and DRAM-likely substreams, enabling earlier DRAM batching without changing L1 correctness.

## 1. Target Placement

This mechanism sits after L1/L1V miss detection and before the ordinary L2 access queue.

```text
L1 / L1V
  |
  | confirmed L1 miss
  v
Cuckoo L2 Residency Filter
  |
  +-- filter hit  -----------------> L2 access queue
  |                                      |
  |                                      +-- L2 hit  -> return data
  |                                      +-- L2 miss -> DRAM batch helper
  |
  +-- filter miss -----------------> L2 bypass path
                                         |
                                         v
                                  DRAM batch helper
```

The filter is only queried for confirmed L1/L1V misses. If a future implementation wants to collect metadata before L1 hit/miss resolution, that should be a separate shadow-batching optimization and should not issue real memory requests until L1 miss is confirmed.

## 2. Main Design Choice

The Cuckoo filter is used as an **L2 residency negative filter**:

- `filter hit`: the line may be in L2, so the request must go to L2.
- `filter miss`: the line is predicted not to be in L2, so the request may bypass the normal L2 lookup path and go to the DRAM batch helper.

This design relies on a critical invariant:

> The filter must not produce false negatives for any line that is currently resident in L2 and could contain the most recent copy of the data.

False positives are acceptable. They only send a DRAM-bound request to L2 first, reducing performance but not breaking correctness.

False negatives are dangerous. They may cause the datapath to bypass an L2 line that actually exists, possibly reading stale data from DRAM or missing a dirty/coherent copy.

## 3. Safety Modes

Implement the mechanism with an explicit mode knob. This keeps experiments honest and makes the paper easier to defend.

### Mode A: Strict Correct Bypass

Use this as the default paper mechanism if the simulator can maintain the filter exactly enough.

In this mode, a filter miss is allowed to bypass L2 only if all of the following are true:

1. Every L2-resident cacheline is inserted into the Cuckoo filter before it becomes visible as an L2 hit.
2. A cacheline is deleted from the filter only after the L2 line has been invalidated or evicted safely.
3. Dirty L2 lines are included in the filter.
4. Coherence-owned or transient L2 lines are included in the filter or are marked as non-bypassable.
5. Cuckoo insertion failure disables bypass until the filter is repaired or rebuilt.
6. Store, atomic, acquire/release, fence, and other strongly ordered requests do not bypass L2 unless the simulator already proves it is safe.

If these conditions hold, filter misses can be treated as no-false-negative negative evidence and can enter the DRAM batch helper directly.

### Mode B: Speculative Early DRAM With L2 Validation

Use this as a safe fallback if no-false-negative maintenance is hard.

In this mode, a filter miss may issue an early DRAM read, but the returned data cannot be consumed until a real L2 validation confirms that the line was not in L2.

```text
filter miss
  |
  +-- early DRAM batch request
  |
  +-- delayed/parallel L2 validation
          |
          +-- L2 miss -> accept DRAM data
          +-- L2 hit  -> discard DRAM data, serve L2 data
```

This is not a true L2 bypass in the strict sense, but it is useful as an ablation:

- It proves how much benefit comes from early DRAM scheduling.
- It avoids correctness arguments about false negatives.
- It gives a performance lower bound for the strict bypass version.

### Mode C: Unsafe Oracle / Research Debug Only

This mode assumes the filter is perfect or ignores false-negative correctness. It should only be used for opportunity studies, not as the main reported mechanism.

Use labels such as:

- `oracle_no_fn_filter`
- `unsafe_perf_upper_bound`
- `ideal_l2_absence_oracle`

Do not present this mode as a real design.

## 4. Cuckoo Filter State

Each filter entry stores a compact fingerprint of the cacheline address.

Recommended key:

```text
line_key = physical_line_address
```

If the L2 is physically sliced or banked, include enough bits to avoid ambiguous ownership:

```text
line_key = { l2_slice_id, physical_line_address }
```

Recommended Cuckoo filter fields:

```text
struct L2ResidencyCuckooFilter {
    num_buckets
    slots_per_bucket
    fingerprint_bits
    max_kicks
    occupancy
    lookup_ports
    insert_ports
    delete_ports
    bypass_enabled
    rebuild_pending
}
```

Recommended initial parameters:

```text
slots_per_bucket      = 4
fingerprint_bits      = 12 or 16
max_kicks             = 8
target_load_factor    = 0.80 to 0.90
lookup_latency        = 1 to 2 cycles
```

Run sensitivity for:

- fingerprint bits: `8 / 12 / 16`
- load factor: `0.60 / 0.75 / 0.90`
- lookup latency: `0 / 1 / 2 / 4 cycles`
- max kicks: `4 / 8 / 16`

## 5. Filter Maintenance Rules

The filter must track L2 residency events.

### On L2 Fill

When a line is installed into L2:

```text
insert(line_key)
if insert fails:
    bypass_enabled = false
    rebuild_pending = true
```

Important ordering:

> Insert into the filter before the line can be observed as a valid L2 line.

This avoids a short window where the line exists in L2 but the filter says absent.

### On L2 Eviction / Invalidation

When a line is removed from L2:

```text
invalidate_or_evict_l2_line(line_key)
delete(line_key)
```

Important ordering:

> Delete from the filter only after the L2 line is no longer valid.

This avoids deleting too early and creating a false-negative window.

### On Dirty Eviction

For dirty lines:

```text
writeback_dirty_line_to_memory_or_owner()
invalidate_l2_line()
delete(line_key)
```

Do not delete the filter entry before the dirty data is safely handled.

### On Cuckoo Insert Failure

Insertion failure is a correctness event, not just a performance event.

Recommended policy:

```text
if cuckoo_insert_failed:
    disable_l2_bypass()
    continue_using_filter_hit_path_only()
    schedule_filter_rebuild()
```

While bypass is disabled:

- filter hits may still route to L2;
- filter misses must not bypass L2;
- all requests use the ordinary L2 path or the Mode B validation path.

## 6. Request Classes

Start with a narrow safe scope.

### Allowed Initially

Enable L2 bypass only for:

- read-only load requests;
- normal cacheline reads;
- requests with known physical addresses;
- requests not marked as fence/atomic/store;
- requests whose memory type is cacheable and coherent under the existing simulator rules;
- local L1-to-L2 requests, not remote owner-side requests.

### Disabled Initially

Do not bypass L2 for:

- stores;
- atomics;
- acquire/release operations;
- fences;
- page-table walks;
- MMIO;
- uncached accesses;
- requests with unresolved translation or permission state;
- requests already merged in an L2 MSHR;
- requests targeting a line with transient coherence state.

These can be added later after the read-only path is stable.

## 7. Bypass Path and DRAM Batch Helper

The bypass path should not send each request immediately to DRAM. Its value comes from forming a DRAM-friendly batch.

Recommended DRAM batch key:

```text
batch_key = {
    memory_partition,
    dram_channel,
    dram_bank,
    dram_row,
    row_window_id,
    access_type
}
```

Each batch entry tracks:

```text
struct DRAMBypassBatchEntry {
    batch_key
    line_bitmap_or_line_list
    original_request_ids
    requester_ids
    first_arrival_cycle
    last_arrival_cycle
    oldest_deadline_cycle
    byte_mask
}
```

Flush conditions:

```text
flush if line_count >= max_batch_lines
flush if oldest_wait >= max_wait_cycles
flush if queue_pressure >= high_watermark
flush if ordering_conflict_detected
flush if DRAM row/window changes and no merge candidate exists
```

Recommended initial values:

```text
max_batch_lines = 2 / 4 / 8
max_wait_cycles = 2 / 4 / 8 / 16
queue_entries = 16 / 32 / 64
```

## 8. L2 Path for Filter Hits

Filter hits are "maybe in L2", not guaranteed L2 hits.

For filter hits:

```text
send_to_l2_access_queue(request)
if l2_hit:
    return_data()
else:
    send_to_dram_batch_helper()
```

Optional optimization:

- batch filter-hit requests by L2 slice/set/bank before probing L2;
- preserve per-warp or per-core ordering constraints;
- keep a bounded wait window to avoid increasing L2 hit latency too much.

Recommended L2 batch key:

```text
l2_batch_key = {
    l2_slice,
    l2_bank,
    l2_set,
    access_type,
    ordering_class
}
```

## 9. Interaction With MSHRs

The bypass path must still respect existing miss merging.

Before sending a bypass request to DRAM, check whether an equivalent miss is already outstanding.

```text
if outstanding_mshr_contains(line_key):
    merge_request_with_existing_mshr()
else:
    allocate_bypass_mshr_or_dram_batch_entry()
```

If the simulator has L2 MSHRs only inside the L2 module, add one of:

1. a small bypass MSHR table before the DRAM batch helper; or
2. a global miss table shared by L2 and the bypass path; or
3. a conservative policy that disables bypass when an L2 MSHR conflict is possible.

For the first implementation, option 1 is usually easiest.

## 10. Correctness Invariants

These invariants should be asserted in debug mode.

### Invariant 1: No Resident-Line False Negative

For every valid L2 line:

```text
assert(cuckoo_filter.lookup(line_key) == true)
```

Run this assertion periodically or on every L2 fill/evict event in debug mode.

### Invariant 2: No Dirty-Line Bypass

If a bypassed request reads from DRAM:

```text
assert(no_valid_dirty_l2_copy(line_key))
```

If checking the whole L2 is expensive, enable this only in debug builds.

### Invariant 3: Ordered Requests Do Not Bypass

```text
assert(!request.is_store)
assert(!request.is_atomic)
assert(!request.is_fence)
assert(!request.requires_strong_ordering)
```

### Invariant 4: Insert Before Visibility

```text
on_l2_fill(line_key):
    insert_filter(line_key)
    set_l2_line_valid(line_key)
```

### Invariant 5: Delete After Invalidation

```text
on_l2_evict(line_key):
    clear_l2_line_valid(line_key)
    delete_filter(line_key)
```

## 11. Statistics and Counters

Add counters before evaluating speedup.

### Filter Counters

```text
m1_cf_lookup_count
m1_cf_hit_count
m1_cf_miss_count
m1_cf_false_positive_count
m1_cf_false_negative_count_debug
m1_cf_insert_count
m1_cf_delete_count
m1_cf_insert_failure_count
m1_cf_rebuild_count
m1_cf_bypass_disabled_cycles
```

False positive definition:

```text
filter hit but real L2 lookup misses
```

False negative definition:

```text
filter miss but debug oracle finds valid L2 line
```

In strict mode, false negatives must be zero.

### Bypass Counters

```text
m1_bypass_candidate_count
m1_bypass_issued_count
m1_bypass_blocked_ordering_count
m1_bypass_blocked_filter_unreliable_count
m1_bypass_blocked_mshr_conflict_count
m1_bypass_saved_l2_lookup_count
m1_bypass_dram_batch_count
m1_bypass_dram_batch_avg_lines
m1_bypass_dram_batch_avg_wait_cycles
```

### Performance Counters

```text
l1_miss_to_l2_queue_latency
l1_miss_to_dram_issue_latency
l2_lookup_count
l2_hit_count
l2_miss_count
dram_transaction_count
dram_row_buffer_hit_count
dram_bytes
dram_average_latency
p95_request_wait
p99_request_wait
```

### Quality Counters

```text
predicted_l2_absent_actual_l2_miss_rate
predicted_l2_present_actual_l2_hit_rate
dram_batch_same_row_fraction
dram_batch_line_density
batchability_fifo
batchability_filter_guided
batchability_oracle
```

## 12. Config Knobs

Use explicit command-line or config options.

```text
m1_cf_bypass_enable = true/false
m1_cf_mode = strict_bypass / validate / oracle
m1_cf_fingerprint_bits = 8/12/16
m1_cf_slots_per_bucket = 2/4/8
m1_cf_num_buckets = N
m1_cf_lookup_latency = 0/1/2/4
m1_cf_max_kicks = 4/8/16
m1_cf_rebuild_enable = true/false

m1_bypass_reads_only = true
m1_bypass_disable_on_insert_failure = true
m1_bypass_check_mshr = true

m1_dram_batch_enable = true/false
m1_dram_batch_max_lines = 1/2/4/8
m1_dram_batch_max_wait_cycles = 0/2/4/8/16
m1_dram_batch_key = row / row_window / bank / partition
```

## 13. Experiment Plan

### Experiment 1: Filter Accuracy

Goal:

> Prove the Cuckoo filter is a useful L2-residency predictor and safe enough for strict bypass.

Report:

- filter hit rate;
- filter miss rate;
- false positive rate;
- debug false negative count;
- predicted-absent actual-L2-miss rate;
- filter occupancy;
- insertion failure rate.

Expected useful result:

```text
filter miss mostly corresponds to actual L2 miss
false negative count = 0 in strict mode
insert failure rate low or bypass-disabled time small
```

### Experiment 2: Bypass Opportunity

Goal:

> Show how many L2 lookups can be avoided without correctness loss.

Compare:

```text
Baseline FIFO
No filter + ordinary L2 path
Cuckoo filter + strict bypass
Cuckoo filter + validation mode
Oracle L2 absence filter
```

Report:

- saved L2 lookups;
- L2 queue occupancy;
- L2 hit latency impact;
- DRAM issue latency;
- total speedup.

### Experiment 3: DRAM Batch Quality

Goal:

> Show that bypassing predicted-L2-miss requests earlier improves DRAM row/window locality.

Report:

- DRAM batch average lines;
- DRAM row-buffer hit rate;
- DRAM transaction count;
- DRAM bytes;
- DRAM latency;
- batch wait p95/p99.

Important comparison:

```text
FIFO L2-miss-to-DRAM stream
Filter-guided bypass-to-DRAM stream
Oracle DRAM-row batch stream
```

### Experiment 4: Sensitivity

Vary:

- Cuckoo fingerprint bits;
- filter size;
- lookup latency;
- max batch wait;
- max batch lines;
- L2 hit rate;
- L2 dirty-line rate.

Key question:

> How much filter capacity and wait window are needed before the mechanism becomes useful?

### Experiment 5: Negative Cases

Use workloads where M1 previously regressed, such as sparse or irregular workloads.

Report:

- whether bypass increases DRAM bytes;
- whether bypass increases p95/p99 wait;
- whether filter false positives send too many requests to L2;
- whether adaptive disabling recovers performance.

## 14. Adaptive Policy

Add a simple runtime controller after the basic version works.

Disable or reduce bypass when:

```text
predicted_absent_actual_miss_rate < threshold
dram_batch_avg_lines < threshold
p95_wait_cycles > threshold
dram_bytes_increase > threshold
filter_insert_failures > 0
```

Suggested initial policy:

```text
if m1_cf_insert_failure_count > 0:
    disable_bypass_until_rebuild()

if predicted_absent_actual_l2_miss_rate < 0.80:
    use validation mode or disable bypass

if dram_batch_avg_lines < 1.5 and p95_wait increases:
    set max_wait_cycles = 0

if dram_bytes increases by more than 10% with no speedup:
    disable DRAM access-unit expansion
```

## 15. Paper Framing

Do not frame this as:

> We add a Cuckoo filter to skip L2.

Frame it as:

> We introduce a no-false-negative L2 residency filter that splits the Enhanced-L1V request stream before L2 into L2-likely and DRAM-likely substreams. This pre-L2 split preserves DRAM-row locality earlier than memory-controller scheduling and avoids wasting L2 bandwidth on requests that are unlikely to hit.

The novelty defense:

- memory schedulers see requests after L2 miss and after locality has already been reshaped by FIFO queues;
- this mechanism acts before L2, using L2-residency prediction to select the datapath;
- L2 correctness is preserved by no-false-negative filter maintenance or by validation mode;
- DRAM batching is applied only to the predicted-L2-absent stream, not blindly to all requests.

## 16. Implementation Phases

### Phase 1: Instrumentation Only

Add an ideal L2-residency oracle and collect:

- how often L1 misses hit in L2;
- how often L1 misses miss in L2;
- DRAM batchability of true L2 misses;
- speedup upper bound if true L2 misses bypass L2.

No behavior change yet.

### Phase 2: Cuckoo Filter Shadow Mode

Maintain the Cuckoo filter on L2 fills/evictions but do not use it to route requests.

Collect:

- filter hit/miss;
- false positives;
- debug false negatives;
- insertion failures;
- occupancy.

Exit criteria:

```text
debug false negatives == 0
insertion failures are rare or handled by bypass disable
```

### Phase 3: Validation Mode

Use filter misses to issue early DRAM batch requests, but validate with L2 before consuming data.

Exit criteria:

```text
no correctness errors
positive DRAM latency or transaction improvement
no severe p95/p99 wait regression
```

### Phase 4: Strict Bypass Mode

Allow filter misses to skip L2 lookup and enter the DRAM batch helper directly.

Exit criteria:

```text
debug false negatives == 0
dirty-line bypass assertion passes
speedup improves over validation mode
```

### Phase 5: Adaptive Mode

Add runtime gating to avoid negative cases.

Exit criteria:

```text
keeps most wins on dense workloads
reduces or removes regressions on sparse/irregular workloads
```

## 17. Minimal Pseudocode

```cpp
void handle_l1_miss(Request* req) {
    if (!m1_cf_bypass_enable || !is_bypass_eligible(req)) {
        send_to_l2(req);
        return;
    }

    LineKey key = make_line_key(req);
    bool maybe_in_l2 = l2_residency_filter.lookup(key);

    stats.m1_cf_lookup_count++;
    if (maybe_in_l2) {
        stats.m1_cf_hit_count++;
        send_to_l2(req);
        return;
    }

    stats.m1_cf_miss_count++;

    if (!l2_residency_filter.bypass_enabled()) {
        stats.m1_bypass_blocked_filter_unreliable_count++;
        send_to_l2(req);
        return;
    }

    if (has_ordering_or_mshr_conflict(req)) {
        stats.m1_bypass_blocked_mshr_conflict_count++;
        send_to_l2(req);
        return;
    }

    if (m1_cf_mode == VALIDATE_MODE) {
        issue_early_dram_batch(req);
        send_to_l2_validation(req);
        return;
    }

    if (m1_cf_mode == STRICT_BYPASS_MODE) {
        debug_assert_no_valid_l2_copy(key);
        stats.m1_bypass_issued_count++;
        enqueue_dram_batch(req);
        return;
    }

    if (m1_cf_mode == ORACLE_MODE) {
        enqueue_dram_batch(req);
        return;
    }
}
```

## 18. Success Criteria

The mechanism is worth keeping if it shows all of the following:

1. A significant fraction of L1 misses are predicted L2-absent.
2. Filter misses have high actual L2-miss precision.
3. Strict mode has zero debug false negatives.
4. L2 lookup traffic or L2 queue pressure decreases.
5. DRAM batch quality improves over FIFO L2-miss ordering.
6. Speedup is positive on locality-rich workloads.
7. Adaptive mode prevents large regressions on sparse/irregular workloads.

If only item 6 is true but items 1-5 are weak, the mechanism will look like an accidental optimization. If items 1-5 are strong, the paper can argue that the mechanism exposes a real datapath opportunity even if some workloads need adaptive gating.

