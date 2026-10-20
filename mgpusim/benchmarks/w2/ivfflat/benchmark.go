// Package ivfflat is the W2 vector-search workload: IVF-Flat (faiss
// IndexIVFFlat) top-k search, the retrieval step of a RAG pipeline (after
// gpu_benchmarks tier5/rag_pipeline, whose Retriever is a brute-force
// cosine top-k; here the standard IVF index of production retrievers).
//
// Host (untimed): synthetic clustered database, coarse quantizer (random
// centroids, nearest-centroid assignment), inverted lists stored list by
// list, each query's nprobe nearest lists, work items of <= 256 positions.
// GPU (kernels.cl, no atomics): ivf_scan (WG per work item, distances and
// per-item top-k in LDS), ivf_merge (WG per query, top-k of the candidates).
// Squared L2 in fp32.
package ivfflat

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// HiddenArgs are the hidden kernel arguments.
type HiddenArgs struct {
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// ScanArgs are the arguments of ivf_scan.
type ScanArgs struct {
	Base, IDs, Queries, Items, CandDist, CandID driver.Ptr
	K, Pad                                      int32
	HiddenArgs
}

// MergeArgs are the arguments of ivf_merge.
type MergeArgs struct {
	CandDist, CandID, QItems, OutDist, OutID driver.Ptr
	K, Pad                                   int32
	HiddenArgs
}

// DefaultParams is a small smoke size (~2000 scan work-groups).
func DefaultParams() Params {
	return Params{N: 16384, NList: 128, NProbe: 8, Q: 256, K: 10, Seed: 1}
}

// Benchmark is the IVF-Flat benchmark.
type Benchmark struct {
	driver      *driver.Driver
	context     *driver.Context
	gpus        []int
	scan, merge *insts.HsaCo

	Params Params
	index  *Index

	dOutDist, dOutID driver.Ptr
}

// NewBenchmark returns an IVF-Flat benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.scan = kernels.LoadProgramFromMemory(hsacoBytes, "ivf_scan")
	b.merge = kernels.LoadProgramFromMemory(hsacoBytes, "ivf_merge")
	if b.scan == nil || b.merge == nil {
		log.Panic("ivfflat: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("ivfflat does not support unified memory") }

// EnableVerification is a no-op.
func (b *Benchmark) EnableVerification() {}

func (b *Benchmark) upload(name string, data interface{}, bytes int) driver.Ptr {
	if bytes == 0 {
		bytes = 4
	}
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(bytes), name)
	if data != nil {
		b.driver.MemCopyH2D(b.context, p, data)
	}
	return p
}

// Run builds the index on the host and launches the two kernels.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	x, err := Build(b.Params)
	if err != nil {
		log.Panic(err)
	}
	b.index = x
	p := x.Params
	n := x.NumItems()
	dBase := b.upload("ivf_base", x.Base, 4*len(x.Base))
	dIDs := b.upload("ivf_ids", x.IDs, 4*len(x.IDs))
	dQ := b.upload("ivf_queries", x.Queries, 4*len(x.Queries))
	dItems := b.upload("ivf_items", x.Items, 4*len(x.Items))
	dQItems := b.upload("ivf_qitems", x.QItems, 4*len(x.QItems))
	dCD := b.upload("ivf_cand_dist", nil, 4*n*p.K)
	dCI := b.upload("ivf_cand_id", nil, 4*n*p.K)
	b.dOutDist = b.upload("ivf_out_dist", nil, 4*p.Q*p.K)
	b.dOutID = b.upload("ivf_out_id", nil, 4*p.Q*p.K)

	minL, maxL := p.N, 0
	for l := 0; l < p.NList; l++ {
		s := int(x.ListStart[l+1] - x.ListStart[l])
		minL = int(math.Min(float64(minL), float64(s)))
		maxL = int(math.Max(float64(maxL), float64(s)))
	}
	scanned := x.ScannedVectors()
	fmt.Printf("IVFFLAT,config,n=%d,dim=%d,nlist=%d,nprobe=%d,queries=%d,k=%d,seed=%d\n",
		p.N, Dim, p.NList, p.NProbe, p.Q, p.K, p.Seed)
	fmt.Printf("IVFFLAT,lists,mean=%.1f,min=%d,max=%d\n", float64(p.N)/float64(p.NList), minL, maxL)
	fmt.Printf("IVFFLAT,kernels,count=2,scan_wgs=%d,merge_wgs=%d\n", n, p.Q)
	fmt.Printf("IVFFLAT,footprint,base_mb=%.1f,scanned_vectors=%d,scan_mb=%.1f\n",
		float64(len(x.Base))*4/1e6, scanned, float64(scanned)*Dim*4/1e6)

	q := b.driver.CreateCommandQueue(b.context)
	sa := ScanArgs{Base: dBase, IDs: dIDs, Queries: dQ, Items: dItems, CandDist: dCD,
		CandID: dCI, K: int32(p.K)}
	b.driver.EnqueueLaunchKernel(q, b.scan, [3]uint32{uint32(n * 256), 1, 1},
		[3]uint16{256, 1, 1}, &sa)
	ma := MergeArgs{CandDist: dCD, CandID: dCI, QItems: dQItems, OutDist: b.dOutDist,
		OutID: b.dOutID, K: int32(p.K)}
	b.driver.EnqueueLaunchKernel(q, b.merge, [3]uint32{uint32(p.Q * 256), 1, 1},
		[3]uint16{256, 1, 1}, &ma)
	b.driver.DrainCommandQueue(q)
}

// Verify compares with the CPU scan of the same probed lists. A result
// slot passes if its id matches, or its distance is within 1e-4 relative
// (near-ties may swap ids under different fp rounding).
func (b *Benchmark) Verify() {
	x := b.index
	ref := x.Reference()
	gd := make([]float32, x.Q*x.K)
	gi := make([]int32, x.Q*x.K)
	b.driver.MemCopyD2H(b.context, gd, b.dOutDist)
	b.driver.MemCopyD2H(b.context, gi, b.dOutID)
	bad, idDiff, maxRel := 0, 0, 0.0
	for q := 0; q < x.Q; q++ {
		for i := 0; i < x.K; i++ {
			g, w := float64(gd[q*x.K+i]), float64(ref[q].Dist[i])
			if gi[q*x.K+i] != ref[q].ID[i] {
				idDiff++
			}
			if math.IsInf(w, 1) {
				if g < 1e38 {
					bad++
				}
				continue
			}
			rel := math.Abs(g-w) / math.Max(w, 1e-30)
			maxRel = math.Max(maxRel, rel)
			if rel > 1e-4 {
				if bad < 5 {
					log.Printf("ivfflat: q %d slot %d: got (%d, %g) want (%d, %g)",
						q, i, gi[q*x.K+i], g, ref[q].ID[i], w)
				}
				bad++
			}
		}
	}
	fmt.Printf("IVFFLAT,verify,slots=%d,id_diffs=%d,max_rel_dist_err=%.3g,mismatches=%d\n",
		x.Q*x.K, idDiff, maxRel, bad)
	if bad > 0 {
		log.Fatalf("ivfflat: verification failed")
	}
	log.Printf("Passed!")
}
