# CuPath CACTI 7.0 area estimate

## Evaluated configuration

The estimate uses `topanitanw/Cacti-7.0` commit
`9144cf2dbdcab7f40c6a6453daf13214305f6c11`, the 32-nm ITRS-HP SRAM model,
UCA arrays, and a 360-K operating temperature. The latest formal metrics report
8,192 buckets, 32,768 slots, and four slots per bucket for every physical
Filter array. Four such arrays are present in one GPM.

Each implemented slot contains one valid bit, a 13-bit fingerprint, three type
bits, and a four-bit reference count. One Filter array therefore stores

`8192 * 4 * 21 bits = 688,128 bits = 84 KiB`.

The Filter is modeled as a 16-bank scratch RAM with an 84-bit bucket interface,
two read ports, and one write port per bank. The 256-entry remote predictor is
conservatively modeled as a 16-KiB, one-bank scratch RAM with one read/write
port and a 512-bit entry interface. The baseline L2 is modeled as four 1-MiB,
16-way, 64-B-line cache arrays, each with 16 banks and one read/write port per
bank.

## CACTI results

| Structure | Count/GPM | Capacity each | Area each (mm^2) | Area/GPM (mm^2) |
|---|---:|---:|---:|---:|
| 1-MiB L2 array including data and tags | 4 | 1 MiB | 13.627600 | 54.510400 |
| 8K-bucket typed Filter array | 4 | 84 KiB | 0.590023 | 2.360092 |
| 256-entry remote predictor | 1 | 16 KiB | 0.036155 | 0.036155 |
| **CuPath arrays** | -- | **352 KiB/GPM** | -- | **2.396247** |

CuPath metadata capacity is 8.59% of the 4-MiB L2 data capacity. Its CACTI
array area is 4.40% of the complete L2 area. CACTI reports 0.661 ns for the
Filter and 0.241 ns for the predictor, both below the modeled 1-ns cycle.

## 4K-bucket what-if point

If a future experiment explicitly sets 16,384 slots per physical Filter array,
the array contains 4,096 buckets and 42 KiB. CACTI reports 0.358314 mm^2 per
Filter. Four Filters plus the predictor occupy 1.469411 mm^2, equal to 2.70%
of the modeled L2 area. This smaller point does not describe the current formal
performance results.

## Scope

CACTI includes the SRAM arrays and modeled ports. The estimate excludes hash
logic, fingerprint comparators, arbitration, bank conflicts, adaptive-pair
history and returned-data buffering, and other CuPath control logic. The result
is therefore an array-area estimate rather than a post-layout total area.
