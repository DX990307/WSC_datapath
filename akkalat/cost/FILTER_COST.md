# CuPath typed-filter storage, area, and energy estimate

## Modeled organization

The formal 13-bit design has 32,768 slots per L2 slice. Each slot stores a
13-bit fingerprint, three type bits, one valid bit, and a four-bit saturating
reference count: 21 bits/slot, or 688,128 bits (86,016 bytes) per slice. Across
192 slices this is 15.75 MiB, equal to 8.20% of the 192 MiB wafer L2 data
capacity. No cacheline data, full address, waiter, batch payload, pattern
history, or per-block provenance bit is included in the Filter.

The evaluated Complete implementation has one 256-entry local predictor
shared by the four L2 slices of a GPM and one 256-entry requester-RDMA
predictor per GPM. Charging a conservative 64 bytes per entry gives 32
KiB/GPM, or 1.50 MiB across 48 GPMs. The 15.75-MiB Filter arrays plus this
bound occupy 8.98% of the wafer's 192-MiB L2 data capacity.

## CACTI estimate

We use the official Hewlett Packard CACTI repository at commit
`1ffd8dfb10303d306ecd8d215320aea07651e878` and CACTI's 32-nm ITRS-HP SRAM
model. The exact inputs and generated outputs are preserved in `cacti32/`;
the tracked `*_output.txt` files duplicate CACTI's globally ignored `.out`
files so the reported numbers remain part of the handoff.
The Filter is modeled as an 86,016-byte scratch RAM with 16 banks, 16-byte
bucket transfers, two read ports, one write port, and an 84-bit output (one
four-slot bucket). Two read ports approximate the two candidate-bucket reads;
16 banks provide the conflict-free lower-bound bandwidth for 16 lookups/cycle.
For a like-for-like reference, one 1-MiB, 16-way L2 slice uses the same 16
banks and port counts.

| Quantity, per slice | Typed Filter | 1-MiB L2 reference | Filter/L2 |
|---|---:|---:|---:|
| SRAM capacity | 86,016 B | 1,048,576 B data | 8.20% |
| Array area | 0.5900 mm² | 45.8082 mm² data+tag | 1.29% |
| Access time | 0.6611 ns | 12.1368 ns | -- |
| Dynamic read energy/access | 0.03457 nJ | 4.56281 nJ | 0.76% |
| Two-bucket Filter lookup | 0.06914 nJ | 4.56281 nJ | 1.52% |
| Search+write Filter update | 0.07166 nJ | 4.56571 nJ write | 1.57% |

The array-only wafer estimate is 113.3 mm² at 32 nm. This absolute number is
not portable to a modern wafer-scale process, so the paper should emphasize
the matched 1.29% array-area and approximately 1.5--1.6% dynamic-access-energy
ratios rather than the absolute area.

## Limitations

This is a lower-bound SRAM-array estimate. CACTI does not include the typed
hash functions, fingerprint comparators, exact simulator-only analysis shadow,
or bank conflicts between the two candidate buckets. The simulation explicitly
models aggregate lookup/update width and port stalls, but not individual bank
conflicts. The exact structures that already exist in L2 and RDMA are not
charged to the Filter. Sensitivity results must therefore accompany this table;
if a smaller width preserves performance, that lower-bandwidth implementation
should be preferred over extrapolating the conflict-free 16-wide estimate.

Source: [Hewlett Packard CACTI](https://github.com/HewlettPackard/cacti).
