// Package pagemigration models runtime page migration between the GPMs of a
// wafer-scale GPU, as a baseline against which data-path mechanisms are
// compared.
//
// A Manager plays the role of the UVM driver (or an on-wafer migration
// controller). It decides what to migrate under one of two policies:
//
//   - on-fault: an access of a GPM that translates to a page on another GPM
//     is a fault. The faulting access waits; the fault is serviced in a
//     per-GPM batch after a software latency and the page (or the 2-MB group
//     holding it) moves to the faulting GPM.
//   - access counter (NVIDIA Volta+ style): every access that leaves a GPM's
//     L1 for remote memory (accepted by the RDMA engine from the L1; with M3
//     this includes accesses later served by a replica in the requester L2)
//     increments a per-(unit, GPM) counter; when a
//     counter reaches the threshold the unit migrates, asynchronously, to the
//     GPM with the largest count. Accesses continue remotely until the
//     shootdown.
//
// Both policies limit thrashing with a cooldown after each migration and a
// cap on the number of migrations per unit (after which the unit is pinned).
//
// A migration of a unit (a 4-KB page, or an aligned group of 4-KB pages for a
// 2-MB granularity; the page size and the TLBs are not changed) goes through:
//
//  1. software service (batched per destination GPM, SoftwareLatency);
//  2. shootdown: from here every access to the unit is held at its address
//     translator; the TLB entries of the unit are invalidated in every GPM
//     that accessed it and in the IOMMU TLB; ShootdownLatency elapses;
//  3. drain: wait until no access already translated to the old frames is
//     still in flight (counted at the address translators);
//  4. copy: the destination GPM's CopyEngine pulls each page in chunks from
//     the source GPM's CopyEngine over the wafer network; the source reads
//     through its L2 (so dirty lines are copied) and the destination writes
//     the new frame in its DRAM directly, around the L2 (a new frame was never
//     accessed, so no cache holds it; writing around the L2 leaves no dirty
//     lines that a functional host copy could later shadow);
//  5. remap: the page table points to the new frames and held accesses
//     resume with the new translation.
package pagemigration
