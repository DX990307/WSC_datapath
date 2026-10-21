package switching

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/noc/networking/arbitration"
	"github.com/sarchlab/akita/v3/noc/networking/routing"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("SwitchPortAdder buffer depth", func() {
	build := func(bufSize int) portComplex {
		engine := sim.NewSerialEngine()
		sw := SwitchBuilder{}.
			WithEngine(engine).
			WithFreq(1 * sim.GHz).
			WithRoutingTable(routing.NewTable()).
			WithArbiter(arbitration.NewXBarArbiter()).
			Build("SW")
		local := sim.NewLimitNumMsgPort(sw, 3, "SW.Local")
		remote := sim.NewLimitNumMsgPort(nil, 3, "Remote")
		MakeSwitchPortAdder(sw).
			WithPorts(local, remote).
			WithNumInputChannel(3).
			WithNumOutputChannel(3).
			WithBufferSize(bufSize).
			AddPort()
		return sw.portToComplexMapping[local]
	}

	It("keeps buffers equal to the channel count by default", func() {
		pc := build(0)
		Expect(pc.routeBuffer.Capacity()).To(Equal(3))
		Expect(pc.forwardBuffer.Capacity()).To(Equal(3))
		Expect(pc.sendOutBuffer.Capacity()).To(Equal(3))
		Expect(pc.numInputChannel).To(Equal(3))
		Expect(pc.numOutputChannel).To(Equal(3))
	})

	It("deepens buffers without changing channel counts", func() {
		pc := build(16)
		Expect(pc.routeBuffer.Capacity()).To(Equal(16))
		Expect(pc.forwardBuffer.Capacity()).To(Equal(16))
		Expect(pc.sendOutBuffer.Capacity()).To(Equal(16))
		Expect(pc.numInputChannel).To(Equal(3))
		Expect(pc.numOutputChannel).To(Equal(3))
	})

	It("never shrinks buffers below the channel count", func() {
		pc := build(2)
		Expect(pc.forwardBuffer.Capacity()).To(Equal(3))
	})
})
