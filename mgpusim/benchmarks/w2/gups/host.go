package gups

import "fmt"

// Params is the input size.
type Params struct {
	LogTable int // table of 2^LogTable 64-bit words
	WGs      int // work-groups of 256 threads
	// UpdateMult: updates = UpdateMult * table words (HPCC and upstream: 4).
	// Larger values lengthen the run without growing the table (e.g. to keep
	// it within the L2 TLB reach: 512 entries x 4 KB = 2 MB per GPM).
	UpdateMult int
}

// Validate checks the parameters.
func (p Params) Validate() error {
	if p.LogTable < 4 || p.LogTable > 30 || p.WGs <= 0 || p.UpdateMult <= 0 ||
		p.Updates() >= 1<<32 {
		return fmt.Errorf("gups: bad parameters %+v", p)
	}
	return nil
}

// TableSize is 2^LogTable.
func (p Params) TableSize() int { return 1 << p.LogTable }

// Updates is UpdateMult * TableSize.
func (p Params) Updates() uint64 { return uint64(p.UpdateMult) * uint64(p.TableSize()) }

// Threads is WGs * 256.
func (p Params) Threads() int { return p.WGs * 256 }

// Step is one LFSR step (polynomial 7, upstream gups_step).
func Step(s uint64) uint64 {
	if s>>63 != 0 {
		return s<<1 ^ 7
	}
	return s << 1
}

// Partition returns q, r: thread t applies q updates, plus one if t < r.
func (p Params) Partition() (q, r uint32) {
	u, n := p.Updates(), uint64(p.Threads())
	return uint32(u / n), uint32(u % n)
}

// Starts returns each thread's stream state before its first update
// (stream seed 1; computed by walking the stream on the host).
func (p Params) Starts() []uint64 {
	q, r := p.Partition()
	out := make([]uint64, p.Threads())
	s := uint64(1)
	for t := range out {
		out[t] = s
		n := q
		if uint32(t) < r {
			n++
		}
		for i := uint32(0); i < n; i++ {
			s = Step(s)
		}
	}
	return out
}

// InitialTable is table[i] = i.
func (p Params) InitialTable() []uint64 {
	t := make([]uint64, p.TableSize())
	for i := range t {
		t[i] = uint64(i)
	}
	return t
}

// Reference applies the serial stream (upstream gups_cpu_reference).
func (p Params) Reference() []uint64 {
	t := p.InitialTable()
	mask := uint64(p.TableSize() - 1)
	s := uint64(1)
	for i := uint64(0); i < p.Updates(); i++ {
		s = Step(s)
		t[s&mask] ^= s
	}
	return t
}
