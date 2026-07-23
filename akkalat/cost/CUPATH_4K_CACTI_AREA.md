# CuPath 4K-bucket CACTI area estimate

## Tool and common assumptions

- CACTI source: `topanitanw/Cacti-7.0`, commit
  `9144cf2dbdcab7f40c6a6453daf13214305f6c11`.
- CACTI 7.0 UCA SRAM model, 32-nm ITRS-HP cells and peripherals, 360 K.
- All ratios compare structures modeled with the same CACTI version, process,
  cell type, wire assumptions, and optimization objective.
- Reported areas are the top-level CACTI height-times-width values.

## One GPM

### Baseline L2

The complete 4-MiB L2 is modeled as four 1-MiB partitions. Each partition is
a 16-way, 64-B-line cache with 16 banks and one read/write port per bank.
CACTI's cache model includes the data array, tag array, decoders, sense
amplifiers, comparators, and H-trees. It does not model the L2 MSHRs, queues,
or cache-control logic.

### Typed Filters

Each L2 partition has one Filter with 4,096 buckets and four slots per bucket.
The implemented typed slot contains a 13-bit fingerprint, three type bits, one
valid bit, and a four-bit reference count. Therefore, one Filter stores

`4096 * 4 * 21 bits = 344,064 bits = 42 KiB`.

The Filter is modeled as a 16-bank scratch RAM. Each bank has two read ports
and one write port, and the output width is one 84-bit four-slot bucket. In the
conflict-free case, the 16 banks provide 32 bucket reads and 16 bucket writes
per cycle, corresponding to 16 two-bucket lookups and 16 updates per cycle.
This is an array-level lower bound because CACTI does not model hash
generation, fingerprint comparators, arbitration, or bank conflicts.

### Remote predictor

The requester-side predictor has 256 entries. Because the paper does not yet
specify an encoded entry width, this estimate retains the existing conservative
64-B-per-entry bound, or 16 KiB/GPM. It is modeled as a one-bank scratch RAM
with one read/write port and a 512-bit entry interface.

## CACTI results

| Structure | Count/GPM | Area each (mm^2) | Area/GPM (mm^2) |
|---|---:|---:|---:|
| 1-MiB L2 partition, data and tag | 4 | 13.627624 | 54.510494 |
| 4K-bucket typed Filter | 4 | 0.358314 | 1.433256 |
| 256-entry remote predictor | 1 | 0.036154 | 0.036154 |
| **CuPath total** | -- | -- | **1.469411** |

The resulting array-area overhead is

`1.469411 / 54.510494 = 2.6956%`, rounded to **2.70% of the complete L2 area**.

The corresponding raw metadata capacity is 184 KiB/GPM, or 4.49% of the
4-MiB L2 data capacity. The area ratio is smaller because the full L2 estimate
also includes tags and substantial cache-array peripheral and interconnect
overhead.

CACTI reports a 0.523-ns Filter access and a 0.241-ns predictor access, both
below the modeled 1-ns cycle. The 1-MiB L2 partition reports a 6.327-ns access,
which fits within the simulator's ten-cycle L2 lookup at 1 GHz.

## Fingerprint-only lower bound

If each Filter slot is charged only for the 13-bit fingerprint and incorrectly
omits type, valid, and reference-count state, one Filter contains 26 KiB and
occupies 0.233851 mm^2. Four such Filters plus the predictor occupy 0.971559
mm^2, or 1.78% of the L2 area. This value is a storage lower bound and should
not be used as the primary typed-Filter estimate.

## Scope

The estimate includes only the four typed Filters and one remote predictor
requested for CuPath. It excludes new cache levels, cacheline data stores,
RDMA tables already present in the baseline, simulator-only exact shadows, and
M1/M2/M3 control logic. The 2.70% result should therefore be described as a
CACTI array-area estimate rather than a post-layout total-GPM area result.
