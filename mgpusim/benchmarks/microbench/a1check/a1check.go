// Package a1check is a correctness microbenchmark for A1 (cross-kernel
// retention of read-only remote replica lines). It uses only the driver's
// device-to-device copy kernel. Buffer A is read by every GPM in kernels k and
// k+1 (the second touch admits its lines into the requester L2s), written in
// kernel k+2 (second half, by GPMs other than the ones that read each line;
// partly by its home GPM) and k+3 (one line of another page), and read again
// in kernel k+4, which must see the new data. Buffer R is only read, so with A1 its
// replicas stay valid across the boundaries (see the a1_retained_hits
// metrics).
package a1check

import (
	"fmt"
	"log"

	"github.com/sarchlab/mgpusim/v3/driver"
)

// Benchmark is the A1 check.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	Bytes  int // size of A and R (multiple of 4096)
	Rounds int // read-only rounds of R after the write

	a, c, c2, r       driver.Ptr
	b1, b2, d, s1, s2 driver.Ptr
	s                 []driver.Ptr
	wantA             []uint32
	wantR             []uint32
}

// NewBenchmark returns the benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	return &Benchmark{driver: d, context: d.Init(), Bytes: 64 << 10, Rounds: 2}
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("a1check: no unified memory") }

func (b *Benchmark) alloc(name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(b.Bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(b.Bytes), name)
	return p
}

func pattern(n int, seed uint32) []uint32 {
	v := make([]uint32, n)
	for i := range v {
		v[i] = seed<<28 ^ uint32(i)*2654435761
	}
	return v
}

// Run runs the kernel sequence.
func (b *Benchmark) Run() {
	if b.Bytes%4096 != 0 || b.Bytes < 8192 {
		log.Panic("a1check: -a1check-bytes must be a multiple of 4096, >= 8192")
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	n := b.Bytes / 4
	b.a, b.c, b.r = b.alloc("a1_a"), b.alloc("a1_c"), b.alloc("a1_r")
	b.c2 = b.alloc("a1_c2")
	b.b1, b.b2, b.d = b.alloc("a1_b1"), b.alloc("a1_b2"), b.alloc("a1_d")
	b.s1, b.s2 = b.alloc("a1_s1"), b.alloc("a1_s2")
	pa, pc, pc2, pr := pattern(n, 1), pattern(n, 2), pattern(n, 3), pattern(n, 4)
	b.driver.MemCopyH2D(b.context, b.a, pa)
	b.driver.MemCopyH2D(b.context, b.c, pc)
	b.driver.MemCopyH2D(b.context, b.c2, pc2)
	b.driver.MemCopyH2D(b.context, b.r, pr)
	fmt.Printf("A1CHECK,config,bytes=%d,rounds=%d\n", b.Bytes, b.Rounds)

	q := b.driver.CreateCommandQueue(b.context)
	copyAll := func(dst, src driver.Ptr) {
		b.driver.EnqueueMemCopyD2D(q, dst, src, b.Bytes)
	}
	copyAll(b.b1, b.a) // k:   first touch of A
	copyAll(b.s1, b.r) //      first touch of R
	copyAll(b.b2, b.a) // k+1: second touch, A admitted
	copyAll(b.s2, b.r) //      R admitted
	// k+2: the second half of A is overwritten. The copy kernel's work-item
	// t now writes A[half+t]; in kernels k and k+1 that element was read by
	// work-item half/4+t, i.e. by a different work-group and in general a
	// different GPM, so the readers' replicas become stale (a GPM that
	// writes a remote line itself never reads it from its L2 again).
	half := driver.Ptr(b.Bytes / 2)
	b.driver.EnqueueMemCopyD2D(q, b.a+half, b.c+half, b.Bytes/2)
	// k+3: one 64-B line of A (in its first half) from C2, written by the
	// first work-group; the rest of that page keeps A's data.
	off := driver.Ptr(100 * 256) // read by work-group 100 in k, k+1
	b.driver.EnqueueMemCopyD2D(q, b.a+off, b.c2+off, 64)
	copyAll(b.d, b.a) // k+4: must see the writes
	for i := 0; i < b.Rounds; i++ {
		s := b.alloc(fmt.Sprintf("a1_s%d", i+3))
		b.s = append(b.s, s)
		copyAll(s, b.r) // read-only R, retained under A1
	}
	b.driver.DrainCommandQueue(q)

	b.wantA = append([]uint32(nil), pa...)
	copy(b.wantA[n/2:], pc[n/2:])
	copy(b.wantA[100*64:100*64+16], pc2[100*64:100*64+16])
	b.wantR = pr
}

func (b *Benchmark) check(name string, p driver.Ptr, want []uint32) int {
	got := make([]uint32, len(want))
	b.driver.MemCopyD2H(b.context, got, p)
	bad := 0
	for i := range want {
		if got[i] != want[i] {
			if bad < 3 {
				log.Printf("a1check: %s[%d] = %#x, want %#x", name, i, got[i], want[i])
			}
			bad++
		}
	}
	return bad
}

// Verify checks every output buffer.
func (b *Benchmark) Verify() {
	b.driver.SetMagicCopyCoherent(true)
	n := b.Bytes / 4
	pa := pattern(n, 1)
	bad := b.check("b1", b.b1, pa) + b.check("b2", b.b2, pa)
	badD := b.check("d", b.d, b.wantA)
	bad += badD
	bad += b.check("s1", b.s1, b.wantR) + b.check("s2", b.s2, b.wantR)
	for i, s := range b.s {
		bad += b.check(fmt.Sprintf("s%d", i+3), s, b.wantR)
	}
	fmt.Printf("A1CHECK,verify,bytes=%d,mismatches=%d,read_after_write_mismatches=%d\n",
		b.Bytes, bad, badD)
	if bad > 0 {
		log.Fatalf("a1check: verification failed")
	}
	log.Printf("Passed!")
}
