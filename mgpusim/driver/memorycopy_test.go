package driver

import (
	"github.com/golang/mock/gomock"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/protocol"
)

var _ = ginkgo.Describe("Defaultmemorycopymiddleware", func() {
	var (
		mockCtrl *gomock.Controller
		toGPUs   *MockPort
		driver   *Driver
		mw       *defaultMemoryCopyMiddleware
		cmdQueue *CommandQueue
	)

	ginkgo.BeforeEach(func() {
		mockCtrl = gomock.NewController(ginkgo.GinkgoT())
		toGPUs = NewMockPort(mockCtrl)
		toGPUs.EXPECT().Retrieve(gomock.Any()).AnyTimes()
		memAllocator := NewMockMemoryAllocator(mockCtrl)
		memAllocator.EXPECT().RegisterDevice(gomock.Any()).AnyTimes()

		driver = MakeBuilder().
			WithEngine(NewMockEngine(mockCtrl)).
			WithLog2PageSize(12).
			WithD2HCycles(1).
			WithH2DCycles(1).
			Build("Driver")
		driver.gpuPort = toGPUs
		driver.memAllocator = memAllocator
		driver.RegisterGPU(NewMockPort(mockCtrl),
			DeviceProperties{CUCount: 4, DRAMSize: 4 * mem.GB})

		mw = &defaultMemoryCopyMiddleware{driver: driver}
		context := driver.Init()
		cmdQueue = driver.CreateCommandQueue(context)
	})

	ginkgo.AfterEach(func() {
		mockCtrl.Finish()
	})

	respond := func(req sim.Msg) {
		rsp := sim.GeneralRspBuilder{}.WithOriginalReq(req).Build()
		Expect(mw.processGeneralRsp(10, rsp)).To(BeTrue())
	}

	// A copy that overlaps a dirty buffer sends a flush first; both the flush
	// and the copy request belong to the command. The command must complete
	// whichever response arrives last.
	ginkgo.Context("H2D preceded by a flush", func() {
		var flush, copyReq sim.Msg

		ginkgo.BeforeEach(func() {
			flush = protocol.NewFlushReq(9, toGPUs, nil)
			copyReq = protocol.NewMemCopyH2DReq(9, toGPUs, nil,
				make([]byte, 4), 0x100)
			cmdQueue.Enqueue(&MemCopyH2DCommand{
				Dst:  Ptr(0x100),
				Src:  uint32(1),
				Reqs: []sim.Msg{flush, copyReq},
			})
			cmdQueue.IsRunning = true
		})

		ginkgo.It("should complete when the flush returns last", func() {
			respond(copyReq)
			Expect(cmdQueue.NumCommand()).To(Equal(1))
			Expect(cmdQueue.IsRunning).To(BeTrue())

			respond(flush)
			Expect(cmdQueue.NumCommand()).To(Equal(0))
			Expect(cmdQueue.IsRunning).To(BeFalse())
		})

		ginkgo.It("should complete when the copy returns last", func() {
			respond(flush)
			Expect(cmdQueue.NumCommand()).To(Equal(1))
			Expect(cmdQueue.IsRunning).To(BeTrue())

			respond(copyReq)
			Expect(cmdQueue.NumCommand()).To(Equal(0))
			Expect(cmdQueue.IsRunning).To(BeFalse())
		})
	})

	ginkgo.Context("D2H preceded by a flush", func() {
		var (
			flush, copyReq sim.Msg
			dst            uint32
		)

		ginkgo.BeforeEach(func() {
			dst = 0
			flush = protocol.NewFlushReq(9, toGPUs, nil)
			copyReq = protocol.NewMemCopyD2HReq(9, toGPUs, nil,
				0x100, []byte{7, 0, 0, 0})
			cmdQueue.Enqueue(&MemCopyD2HCommand{
				Dst:     &dst,
				Src:     Ptr(0x100),
				RawData: []byte{7, 0, 0, 0},
				Reqs:    []sim.Msg{flush, copyReq},
			})
			cmdQueue.IsRunning = true
		})

		ginkgo.It("should complete and decode data when the flush returns last", func() {
			respond(copyReq)
			Expect(cmdQueue.NumCommand()).To(Equal(1))
			Expect(dst).To(Equal(uint32(0)))

			respond(flush)
			Expect(cmdQueue.NumCommand()).To(Equal(0))
			Expect(cmdQueue.IsRunning).To(BeFalse())
			Expect(dst).To(Equal(uint32(7)))
		})

		ginkgo.It("should complete and decode data when the copy returns last", func() {
			respond(flush)
			Expect(cmdQueue.NumCommand()).To(Equal(1))
			Expect(dst).To(Equal(uint32(0)))

			respond(copyReq)
			Expect(cmdQueue.NumCommand()).To(Equal(0))
			Expect(cmdQueue.IsRunning).To(BeFalse())
			Expect(dst).To(Equal(uint32(7)))
		})
	})
})
