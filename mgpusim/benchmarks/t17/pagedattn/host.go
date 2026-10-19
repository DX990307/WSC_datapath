package pagedattn

import (
	// embed the trace
	_ "embed"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
)

// Model shape constants compiled into kernels.cl.
const (
	HeadDim   = 128 // D
	GQA       = 4   // query heads per KV head
	BlockSize = 16  // tokens per KV block (vLLM default)
	MaxPart   = 512 // largest partition the kernel's LDS holds
)

// LenDist is a log-normal context-length distribution clipped to
// [Min, Max]. Mean is the mean of the unclipped distribution and Sigma the
// standard deviation of ln(length); Mu = ln(Mean) - Sigma^2/2.
type LenDist struct {
	Mean  float64
	Sigma float64
	Min   int
	Max   int
}

// Mu returns the location parameter of the log-normal distribution.
func (d LenDist) Mu() float64 { return math.Log(d.Mean) - d.Sigma*d.Sigma/2 }

// SampleSeqLens draws n context lengths.
func SampleSeqLens(rng *rand.Rand, n int, d LenDist) []int {
	if d.Mean <= 0 || d.Sigma < 0 || d.Min < 1 || d.Max < d.Min {
		panic(fmt.Sprintf("pagedattn: invalid length distribution %+v", d))
	}
	mu := d.Mu()
	out := make([]int, n)
	for i := range out {
		l := int(math.Round(math.Exp(mu + d.Sigma*rng.NormFloat64())))
		if l < d.Min {
			l = d.Min
		}
		if l > d.Max {
			l = d.Max
		}
		out[i] = l
	}
	return out
}

// LenSource draws the context length of a sequence in the decode batch
// (total = prompt + tokens generated so far, prompt < total) and the final
// length of a background request (for the allocator history).
type LenSource interface {
	Sample(rng *rand.Rand) (total, prompt int)
	Request(rng *rand.Rand) int
	Describe() string
}

// Sample implements LenSource: a log-normal total, prompt = a U(0.2, 0.8)
// fraction of it.
func (d LenDist) Sample(rng *rand.Rand) (total, prompt int) {
	total = SampleSeqLens(rng, 1, d)[0]
	prompt = int(math.Round((0.2 + 0.6*rng.Float64()) * float64(total)))
	return total, clampPrompt(prompt, total)
}

// Request implements LenSource.
func (d LenDist) Request(rng *rand.Rand) int { return SampleSeqLens(rng, 1, d)[0] }

// Describe implements LenSource.
func (d LenDist) Describe() string {
	return fmt.Sprintf("lognormal(mean=%g,sigma=%g,min=%d,max=%d)", d.Mean, d.Sigma, d.Min, d.Max)
}

func clampPrompt(prompt, total int) int {
	if prompt >= total {
		prompt = total - 1
	}
	if prompt < 1 {
		prompt = 1
	}
	return prompt
}

// TraceRequest is one request of a serving trace.
type TraceRequest struct{ Context, Generated int }

// TraceDist samples requests uniformly from a trace (Azure LLM inference
// trace 2023, conversation service, by default). A request caught in the
// middle of its decode phase has context Context + g tokens, with g uniform
// in 1..Generated (1 if Generated is 0); the prompt is Context. Lengths are
// clipped to [Min, Max] (Max = the model context, 8192 for Llama-3-8B).
type TraceDist struct {
	Name     string
	Reqs     []TraceRequest
	Min, Max int
}

func (d TraceDist) clip(l int) int {
	if l < d.Min {
		l = d.Min
	}
	if l > d.Max {
		l = d.Max
	}
	return l
}

// Sample implements LenSource.
func (d TraceDist) Sample(rng *rand.Rand) (total, prompt int) {
	r := d.Reqs[rng.Intn(len(d.Reqs))]
	g := 1
	if r.Generated > 1 {
		g = 1 + rng.Intn(r.Generated)
	}
	total = d.clip(r.Context + g)
	return total, clampPrompt(r.Context, total)
}

// Request implements LenSource: the request's final length.
func (d TraceDist) Request(rng *rand.Rand) int {
	r := d.Reqs[rng.Intn(len(d.Reqs))]
	return d.clip(r.Context + r.Generated)
}

// Describe implements LenSource.
func (d TraceDist) Describe() string {
	return fmt.Sprintf("trace(%s,requests=%d,min=%d,max=%d)", d.Name, len(d.Reqs), d.Min, d.Max)
}

//go:embed azure_conv_tokens.csv
var azureConvTokens string

// AzureConvTrace returns the (ContextTokens, GeneratedTokens) pairs of the
// Azure LLM inference trace 2023, conversation service (19,366 requests,
// CC-BY 4.0; see ../DATASETS.md).
func AzureConvTrace() []TraceRequest {
	var out []TraceRequest
	for _, line := range strings.Split(azureConvTokens, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ",")
		c, err1 := strconv.Atoi(f[0])
		g, err2 := strconv.Atoi(f[1])
		if len(f) != 2 || err1 != nil || err2 != nil {
			panic("pagedattn: bad trace line " + line)
		}
		out = append(out, TraceRequest{c, g})
	}
	return out
}

// BlockPool models vLLM's free KV-block queue (v1 FreeKVCacheBlockQueue
// without prefix caching): allocation pops from the head, a finished
// request's blocks are appended to the tail in reverse order.
type BlockPool struct {
	free []int // FIFO; free[head:] are free
	head int
	n    int
}

// NewBlockPool returns a pool whose free queue is 0..n-1.
func NewBlockPool(n int) *BlockPool {
	p := &BlockPool{free: make([]int, n), n: n}
	for i := range p.free {
		p.free[i] = i
	}
	return p
}

// NumBlocks returns the pool size.
func (p *BlockPool) NumBlocks() int { return p.n }

// NumFree returns the number of free blocks.
func (p *BlockPool) NumFree() int { return len(p.free) - p.head }

// Alloc pops the block at the head of the free queue.
func (p *BlockPool) Alloc() int {
	if p.NumFree() == 0 {
		panic("pagedattn: KV block pool exhausted")
	}
	b := p.free[p.head]
	p.head++
	if p.head > len(p.free)/2 && p.head > 1024 {
		p.free = append([]int(nil), p.free[p.head:]...)
		p.head = 0
	}
	return b
}

// Free returns a request's blocks to the tail, last block first.
func (p *BlockPool) Free(blocks []int) {
	for i := len(blocks) - 1; i >= 0; i-- {
		p.free = append(p.free, blocks[i])
	}
}

// ChurnConfig describes the serving history simulated before the measured
// batch is allocated.
type ChurnConfig struct {
	Rounds    int     // admit/finish rounds
	Occupancy float64 // admit background requests until this pool fraction is used
	FinishP   float64 // probability that a live background request finishes per round
}

func blocksFor(tokens int) int { return (tokens + BlockSize - 1) / BlockSize }

// BuildBlockTables simulates the allocator and returns, for every sequence
// of the batch, its physical blocks in logical order.
//
// History: Rounds rounds in which background requests (final lengths from src)
// are admitted until Occupancy of the pool is in use, then each live one
// finishes with probability FinishP; finally all background requests finish.
// This leaves the free queue in a serving-like scrambled order.
//
// Batch: every sequence first allocates its prompt blocks at once (prefill
// of prompts[i] tokens, sequences admitted in random order), then the
// decode tokens are generated one step
// at a time for all sequences, a sequence taking a new block whenever it
// crosses a block boundary (vLLM appends one block per BlockSize tokens).
func BuildBlockTables(
	rng *rand.Rand, pool *BlockPool, lens, prompts []int, src LenSource,
	churn ChurnConfig,
) [][]int {
	var live [][]int
	for r := 0; r < churn.Rounds; r++ {
		target := int(churn.Occupancy * float64(pool.NumBlocks()))
		for pool.NumBlocks()-pool.NumFree() < target {
			nb := blocksFor(src.Request(rng))
			if nb > pool.NumFree() {
				break
			}
			req := make([]int, nb)
			for i := range req {
				req[i] = pool.Alloc()
			}
			live = append(live, req)
		}
		kept := live[:0]
		for _, req := range live {
			if rng.Float64() < churn.FinishP {
				pool.Free(req)
			} else {
				kept = append(kept, req)
			}
		}
		live = kept
	}
	rng.Shuffle(len(live), func(i, j int) { live[i], live[j] = live[j], live[i] })
	for _, req := range live {
		pool.Free(req)
	}

	n := len(lens)
	tables := make([][]int, n)
	cur := make([]int, n)
	for _, i := range rng.Perm(n) {
		prompt := prompts[i]
		for j := 0; j < blocksFor(prompt); j++ {
			tables[i] = append(tables[i], pool.Alloc())
		}
		cur[i] = prompt
	}
	for {
		progressed := false
		for i := 0; i < n; i++ {
			if cur[i] >= lens[i] {
				continue
			}
			progressed = true
			cur[i]++
			if blocksFor(cur[i]) > len(tables[i]) {
				tables[i] = append(tables[i], pool.Alloc())
			}
		}
		if !progressed {
			break
		}
	}
	return tables
}

// WorkList returns the (sequence, partition) items of pa_partial in launch
// order (sequence-major) and the largest partition count.
func WorkList(lens []int, partTokens int) (work []int32, maxParts int) {
	for s, l := range lens {
		np := (l + partTokens - 1) / partTokens
		if np > maxParts {
			maxParts = np
		}
		for p := 0; p < np; p++ {
			work = append(work, int32(s), int32(p))
		}
	}
	return work, maxParts
}

// Problem is a fully generated PagedAttention decode input.
type Problem struct {
	Batch, KVHeads, PartTokens int
	NumBlocks, MaxBlocks       int
	MaxParts                   int
	Lens                       []int
	Prompts                    []int
	Tables                     [][]int
	Work                       []int32 // pairs
	BlockTable                 []int32 // [Batch][MaxBlocks], -1 = unused
	Q                          []float32
	K, V                       []float32 // [NumBlocks][KVHeads][BlockSize][HeadDim]

	// Decode steps (Params.Steps > 1): step s runs with StepLens[s] =
	// Lens + s and StepWork[s]; step 0 is Lens and Work. A sequence takes a
	// new block from the pool when it crosses a block boundary, so Tables
	// and BlockTable hold the blocks of the last step (earlier steps read a
	// prefix). Nil when Steps <= 1.
	StepLens [][]int
	StepWork [][]int32
}

// QHeads returns the number of query heads.
func (p *Problem) QHeads() int { return p.KVHeads * GQA }

// BlockFloats returns the floats of one physical block (all KV heads).
func (p *Problem) BlockFloats() int { return p.KVHeads * BlockSize * HeadDim }

// NumWork returns the number of (sequence, partition) items.
func (p *Problem) NumWork() int { return len(p.Work) / 2 }

// Params collects every knob of the generator.
type Params struct {
	Batch      int
	KVHeads    int
	PartTokens int
	PoolFactor float64 // pool blocks = PoolFactor * blocks the batch needs
	Dist       LenDist // used when LenSource is "lognormal"
	// LenSource: "azure" (Azure LLM inference trace 2023, conversation) or
	// "lognormal" (Dist).
	LenSource string
	Churn     ChurnConfig
	Seed      int64
	// Steps is the number of consecutive decode steps (P12); 0 or 1 = one
	// step, the T17 input unchanged.
	Steps int
}

// Source returns the length source selected by LenSource.
func (pr Params) Source() LenSource {
	switch pr.LenSource {
	case "", "azure":
		return TraceDist{Name: "azure-conv-2023", Reqs: AzureConvTrace(), Min: pr.Dist.Min, Max: pr.Dist.Max}
	case "lognormal":
		return pr.Dist
	}
	panic(fmt.Sprintf("pagedattn: unknown length source %q (azure, lognormal)", pr.LenSource))
}

// Generate builds the lengths, block tables, work list and the Q values.
// K and V are only filled for the blocks the batch uses (and only if
// withKV is set; otherwise the caller fills them block by block).
func Generate(pr Params, withKV bool) *Problem {
	if pr.PartTokens <= 0 || pr.PartTokens%BlockSize != 0 || pr.PartTokens > MaxPart {
		panic(fmt.Sprintf("pagedattn: partition must be a multiple of %d and <= %d, got %d",
			BlockSize, MaxPart, pr.PartTokens))
	}
	rng := rand.New(rand.NewSource(pr.Seed))
	p := &Problem{Batch: pr.Batch, KVHeads: pr.KVHeads, PartTokens: pr.PartTokens}
	src := pr.Source()
	p.Lens = make([]int, pr.Batch)
	p.Prompts = make([]int, pr.Batch)
	for i := range p.Lens {
		p.Lens[i], p.Prompts[i] = src.Sample(rng)
	}
	need := 0
	for _, l := range p.Lens {
		need += blocksFor(l)
		if blocksFor(l) > p.MaxBlocks {
			p.MaxBlocks = blocksFor(l)
		}
	}
	p.NumBlocks = int(math.Ceil(pr.PoolFactor * float64(need)))
	if p.NumBlocks < need {
		p.NumBlocks = need
	}
	pool := NewBlockPool(p.NumBlocks)
	p.Tables = BuildBlockTables(rng, pool, p.Lens, p.Prompts, src, pr.Churn)
	if pr.Steps > 1 {
		p.growSteps(pool, pr.Steps)
	}
	p.BlockTable = make([]int32, pr.Batch*p.MaxBlocks)
	for i := range p.BlockTable {
		p.BlockTable[i] = -1
	}
	for s, t := range p.Tables {
		for j, b := range t {
			p.BlockTable[s*p.MaxBlocks+j] = int32(b)
		}
	}
	p.Work, p.MaxParts = WorkList(p.Lens, pr.PartTokens)
	if pr.Steps > 1 {
		p.StepWork = make([][]int32, pr.Steps)
		for st := range p.StepWork {
			w, mp := WorkList(p.StepLens[st], pr.PartTokens)
			p.StepWork[st] = w
			if mp > p.MaxParts {
				p.MaxParts = mp
			}
		}
	}

	p.Q = make([]float32, pr.Batch*p.QHeads()*HeadDim)
	for i := range p.Q {
		p.Q[i] = rng.Float32()*2 - 1
	}
	if withKV {
		bf := p.BlockFloats()
		p.K = make([]float32, p.NumBlocks*bf)
		p.V = make([]float32, p.NumBlocks*bf)
		for _, t := range p.Tables {
			for _, b := range t {
				FillBlock(pr.Seed, b, p.K[b*bf:(b+1)*bf], p.V[b*bf:(b+1)*bf])
			}
		}
	}
	return p
}

// growSteps continues the decode for steps-1 more tokens per sequence (all
// sequences step together, in sequence order), taking a block from the pool
// at every block boundary, and sets StepLens and MaxBlocks. It uses no
// random numbers, so the first step is the same input as Steps = 1 (except
// for the wider block table when a sequence grows past MaxBlocks).
func (p *Problem) growSteps(pool *BlockPool, steps int) {
	p.StepLens = make([][]int, steps)
	for st := 0; st < steps; st++ {
		p.StepLens[st] = make([]int, len(p.Lens))
		for i, l := range p.Lens {
			cur := l + st
			p.StepLens[st][i] = cur
			if blocksFor(cur) > len(p.Tables[i]) {
				p.Tables[i] = append(p.Tables[i], pool.Alloc())
			}
			if blocksFor(cur) > p.MaxBlocks {
				p.MaxBlocks = blocksFor(cur)
			}
		}
	}
}

// NumSteps returns the number of decode steps.
func (p *Problem) NumSteps() int {
	if len(p.StepLens) == 0 {
		return 1
	}
	return len(p.StepLens)
}

// StepInput returns the lengths and work list of step st.
func (p *Problem) StepInput(st int) ([]int, []int32) {
	if len(p.StepLens) == 0 {
		return p.Lens, p.Work
	}
	return p.StepLens[st], p.StepWork[st]
}

// FillBlock writes the deterministic K and V values of physical block b.
func FillBlock(seed int64, b int, k, v []float32) {
	rng := rand.New(rand.NewSource(seed*1000003 + int64(b)))
	for i := range k {
		k[i] = rng.Float32()*2 - 1
	}
	for i := range v {
		v[i] = rng.Float32()*2 - 1
	}
}

// CheckBounds verifies every index the kernels dereference: the work list,
// the block-table entries of every touched logical block, and the largest
// element offsets into each buffer.
func (p *Problem) CheckBounds() error {
	for st := 0; st < p.NumSteps(); st++ {
		lens, work := p.StepInput(st)
		if err := p.checkWork(lens, work); err != nil {
			if p.NumSteps() > 1 {
				return fmt.Errorf("step %d: %w", st, err)
			}
			return err
		}
	}
	return p.checkTable()
}

func (p *Problem) checkWork(lens []int, work []int32) error {
	nq := p.QHeads()
	for i := 0; i < len(work)/2; i++ {
		s, part := int(work[2*i]), int(work[2*i+1])
		if s < 0 || s >= p.Batch {
			return fmt.Errorf("work %d: sequence %d out of range", i, s)
		}
		if part < 0 || part >= p.MaxParts || part*p.PartTokens >= lens[s] {
			return fmt.Errorf("work %d: partition %d out of range", i, part)
		}
		blk0 := part * p.PartTokens / BlockSize
		ntok := lens[s] - part*p.PartTokens
		if ntok > p.PartTokens {
			ntok = p.PartTokens
		}
		for b := 0; b < blocksFor(ntok); b++ {
			idx := s*p.MaxBlocks + blk0 + b
			if blk0+b >= p.MaxBlocks {
				return fmt.Errorf("work %d: logical block %d >= max %d", i, blk0+b, p.MaxBlocks)
			}
			phys := int(p.BlockTable[idx])
			if phys < 0 || phys >= p.NumBlocks {
				return fmt.Errorf("work %d: physical block %d out of pool %d", i, phys, p.NumBlocks)
			}
			last := ((phys*p.KVHeads+p.KVHeads-1)*BlockSize+BlockSize-1)*HeadDim + HeadDim - 1
			if last >= p.NumBlocks*p.BlockFloats() {
				return fmt.Errorf("work %d: K/V offset %d out of buffer", i, last)
			}
		}
		o := ((s*nq+nq-1)*p.MaxParts + part)
		if o >= p.Batch*nq*p.MaxParts {
			return fmt.Errorf("work %d: partial-output index %d out of range", i, o)
		}
	}
	return nil
}

func (p *Problem) checkTable() error {
	seen := make(map[int32]bool)
	for _, b := range p.BlockTable {
		if b < 0 {
			continue
		}
		if seen[b] {
			return fmt.Errorf("physical block %d used twice", b)
		}
		seen[b] = true
	}
	return nil
}

// Footprint returns the bytes the two kernels touch (read + written, each
// byte counted once per buffer).
type Footprint struct {
	KV, Q, Index, Partial, Out uint64
}

// Total returns the total footprint.
func (f Footprint) Total() uint64 { return f.KV + f.Q + f.Index + f.Partial + f.Out }

// Footprint computes the touched bytes.
func (p *Problem) Footprint() Footprint {
	var f Footprint
	tokens := 0
	blocks := 0
	for _, l := range p.Lens {
		tokens += l
		blocks += blocksFor(l)
	}
	f.KV = uint64(tokens) * uint64(p.KVHeads*HeadDim*4*2)
	f.Q = uint64(len(p.Q) * 4)
	f.Index = uint64(blocks*4 + p.Batch*4 + len(p.Work)*4)
	nParts := p.NumWork()
	f.Partial = uint64(nParts*p.QHeads()) * uint64(HeadDim*4+8)
	f.Out = uint64(p.Batch * p.QHeads() * HeadDim * 4)
	return f
}

// Reference computes the decode attention output [Batch][Hq][D] in float64
// with the textbook softmax (no partitioning).
func (p *Problem) Reference() []float32 {
	nq := p.QHeads()
	out := make([]float32, p.Batch*nq*HeadDim)
	scale := 1 / math.Sqrt(HeadDim)
	bf := p.BlockFloats()
	for s := 0; s < p.Batch; s++ {
		l := p.Lens[s]
		for qh := 0; qh < nq; qh++ {
			kvh := qh / GQA
			q := p.Q[(s*nq+qh)*HeadDim : (s*nq+qh+1)*HeadDim]
			scores := make([]float64, l)
			m := math.Inf(-1)
			for t := 0; t < l; t++ {
				b := p.Tables[s][t/BlockSize]
				k := p.K[b*bf+(kvh*BlockSize+t%BlockSize)*HeadDim:]
				dot := 0.0
				for d := 0; d < HeadDim; d++ {
					dot += float64(q[d]) * float64(k[d])
				}
				scores[t] = dot * scale
				m = math.Max(m, scores[t])
			}
			sum := 0.0
			acc := make([]float64, HeadDim)
			for t := 0; t < l; t++ {
				w := math.Exp(scores[t] - m)
				sum += w
				b := p.Tables[s][t/BlockSize]
				v := p.V[b*bf+(kvh*BlockSize+t%BlockSize)*HeadDim:]
				for d := 0; d < HeadDim; d++ {
					acc[d] += w * float64(v[d])
				}
			}
			for d := 0; d < HeadDim; d++ {
				out[(s*nq+qh)*HeadDim+d] = float32(acc[d] / sum)
			}
		}
	}
	return out
}
