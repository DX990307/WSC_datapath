package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

type Builder struct {
	name                   string
	engine                 sim.Engine
	freq                   sim.Freq
	localModules           mem.LowModuleFinder
	RemoteRDMAAddressTable mem.LowModuleFinder
	bufferSize             int
	remoteDataPath         RemoteDataPathConfig
	pipelineWidth          int
	pipelineLatency        int
	maxOutstanding         int
}

// MakeBuilder creates a new builder with default configuration values.
func MakeBuilder() Builder {
	return Builder{
		freq:       1 * sim.GHz,
		bufferSize: 128,
	}
}

// WithEngine sets the even-driven simulation engine to use.
func (b Builder) WithEngine(engine sim.Engine) Builder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the Command Processor works at.
func (b Builder) WithFreq(freq sim.Freq) Builder {
	b.freq = freq
	return b
}

// WithBufferSize sets the number of transactions that the buffer can handle.
func (b Builder) WithBufferSize(n int) Builder {
	b.bufferSize = n
	return b
}

// WithPipelineWidth sets the maximum number of messages that each RDMA input
// path can process in one cycle. A non-positive value keeps the legacy
// unlimited-width behavior.
func (b Builder) WithPipelineWidth(n int) Builder {
	b.pipelineWidth = n
	return b
}

// WithPipelineLatency sets the fixed number of RDMA processing cycles paid by
// every request or response when it enters an RDMA endpoint.
func (b Builder) WithPipelineLatency(n int) Builder {
	b.pipelineLatency = n
	return b
}

// WithMaxOutstanding sets the maximum number of distinct operations tracked
// independently by the requester and owner directions of one RDMA endpoint.
// A non-positive value keeps the legacy unlimited behavior.
func (b Builder) WithMaxOutstanding(n int) Builder {
	b.maxOutstanding = n
	return b
}

// WithLocalModules sets the local modules.
func (b Builder) WithLocalModules(m mem.LowModuleFinder) Builder {
	b.localModules = m
	return b
}

// WithRemoteModules sets the remote modules.
func (b Builder) WithRemoteModules(m mem.LowModuleFinder) Builder {
	b.RemoteRDMAAddressTable = m
	return b
}

// WithRemoteDataPath configures requester-side filtered exact deduplication,
// work-conserving page batching, and requester-L2 admission.
func (b Builder) WithRemoteDataPath(c RemoteDataPathConfig) Builder {
	b.remoteDataPath = normalizeRemoteDataPathConfig(c)
	return b
}

// Build creates a RDMA with the given parameters.
func (b Builder) Build(name string) *Comp {
	rdma := &Comp{}

	rdma.TickingComponent = sim.NewTickingComponent(name, b.engine, b.freq, rdma)

	rdma.localModules = b.localModules
	rdma.RemoteRDMAAddressTable = b.RemoteRDMAAddressTable
	rdma.pipelineWidth = b.pipelineWidth
	rdma.pipelineLatency = b.pipelineLatency
	rdma.maxOutstanding = b.maxOutstanding
	rdma.ConfigureRemoteDataPath(b.remoteDataPath)
	// rdma.SetFreq(b.freq)

	rdma.ToL1 = sim.NewLimitNumMsgPort(rdma, b.bufferSize, name+".ToL1")
	rdma.ToL2 = sim.NewLimitNumMsgPort(rdma, b.bufferSize, name+".ToL2")
	rdma.CtrlPort = sim.NewLimitNumMsgPort(rdma, b.bufferSize, name+".CtrlPort")
	rdma.ToOutside = sim.NewLimitNumMsgPort(rdma, b.bufferSize, name+".ToOutside")

	rdma.AddPort("ToL1", rdma.ToL1)
	rdma.AddPort("ToL2", rdma.ToL2)
	rdma.AddPort("CtrlPort", rdma.CtrlPort)
	rdma.AddPort("ToOutside", rdma.ToOutside)

	return rdma
}
