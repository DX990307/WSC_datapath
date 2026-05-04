package cp

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type Builder struct {
	engine sim.Engine
	freq   sim.Freq

	numReqPerCycle int
	pageTable      *vm.PageTable
}

func MakeBuilder() Builder {
	return Builder{
		freq:           1 * sim.GHz,
		numReqPerCycle: 1024,
	}
}

func (b Builder) WithEngine(engine sim.Engine) Builder {
	b.engine = engine
	return b
}

func (b Builder) WithFreq(freq sim.Freq) Builder {
	b.freq = freq
	return b
}

func (b Builder) WithNumReqPerCycle(n int) Builder {
	b.numReqPerCycle = n
	return b
}

func (b Builder) WithPageTable(pageTable *vm.PageTable) Builder {
	b.pageTable = pageTable
	return b
}

func (b Builder) Build(name string) *CP {
	cp := &CP{}
	cp.Engine = b.engine
	cp.Freq = b.freq
	cp.NumReqPerCycle = b.numReqPerCycle

	cp.ToTLB = sim.NewLimitNumMsgPort(cp, 1024, name+".ToTLB")
	cp.AddPort("TLB", cp.ToTLB)

	cp.ToNetwork = sim.NewLimitNumMsgPort(cp, 1024, name+".ToNetwork")
	cp.AddPort("Network", cp.ToNetwork)
	return cp
}
