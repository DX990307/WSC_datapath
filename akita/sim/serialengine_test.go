package sim

import (
	"math/rand"
	"time"

	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega/gmeasure"
	// . "github.com/onsi/gomega"
)

var _ = Describe("SerialEngine", func() {
	var (
		mockCtrl *gomock.Controller
		engine   *SerialEngine
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		engine = NewSerialEngine()
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should schedule events", func() {
		handler1 := NewMockHandler(mockCtrl)
		handler2 := NewMockHandler(mockCtrl)
		evt1 := NewMockEvent(mockCtrl)
		evt2 := NewMockEvent(mockCtrl)
		evt3 := NewMockEvent(mockCtrl)
		evt4 := NewMockEvent(mockCtrl)

		evt1.EXPECT().Time().Return(VTimeInSec(4.0)).AnyTimes()
		evt1.EXPECT().Handler().Return(handler1).AnyTimes()
		evt1.EXPECT().IsSecondary().Return(false).AnyTimes()
		evt2.EXPECT().Time().Return(VTimeInSec(2.0)).AnyTimes()
		evt2.EXPECT().Handler().Return(handler2).AnyTimes()
		evt2.EXPECT().IsSecondary().Return(false).AnyTimes()
		evt3.EXPECT().Time().Return(VTimeInSec(3.0)).AnyTimes()
		evt3.EXPECT().Handler().Return(handler1).AnyTimes()
		evt3.EXPECT().IsSecondary().Return(false).AnyTimes()
		evt4.EXPECT().Time().Return(VTimeInSec(5.0)).AnyTimes()
		evt4.EXPECT().Handler().Return(handler1).AnyTimes()
		evt4.EXPECT().IsSecondary().Return(false).AnyTimes()
		handleEvt2 := handler2.EXPECT().Handle(evt2).Do(func(e Event) {
			engine.Schedule(evt3)
			engine.Schedule(evt4)
		})
		handleEvt3 := handler1.EXPECT().
			Handle(evt3).Do(func(e Event) {}).After(handleEvt2)
		handleEvt1 := handler1.EXPECT().
			Handle(evt1).Do(func(e Event) {}).After(handleEvt3)
		handler1.EXPECT().
			Handle(evt4).Do(func(e Event) {}).After(handleEvt1)

		engine.Schedule(evt1)
		engine.Schedule(evt2)

		_ = engine.Run()
	})

	It("should consider secondary events", func() {
		handler1 := NewMockHandler(mockCtrl)
		handler2 := NewMockHandler(mockCtrl)
		handler3 := NewMockHandler(mockCtrl)
		evt1 := NewMockEvent(mockCtrl)
		evt2 := NewMockEvent(mockCtrl)
		evt3 := NewMockEvent(mockCtrl)

		evt1.EXPECT().Time().Return(VTimeInSec(2.0)).AnyTimes()
		evt1.EXPECT().Handler().Return(handler1).AnyTimes()
		evt1.EXPECT().IsSecondary().Return(true).AnyTimes()
		evt2.EXPECT().Time().Return(VTimeInSec(2.0)).AnyTimes()
		evt2.EXPECT().Handler().Return(handler2).AnyTimes()
		evt2.EXPECT().IsSecondary().Return(false).AnyTimes()
		evt3.EXPECT().Time().Return(VTimeInSec(2.0)).AnyTimes()
		evt3.EXPECT().Handler().Return(handler3).AnyTimes()
		evt3.EXPECT().IsSecondary().Return(false).AnyTimes()

		handleEvt2 := handler2.EXPECT().Handle(evt2)
		handleEvt3 := handler3.EXPECT().Handle(evt3)
		handler1.EXPECT().
			Handle(evt1).Do(func(e Event) {}).
			After(handleEvt2).
			After(handleEvt3)

		engine.Schedule(evt1)
		engine.Schedule(evt2)
		engine.Schedule(evt3)

		_ = engine.Run()
	})

	It("should schedule sampled events by time", func() {
		handler1 := NewMockHandler(mockCtrl)
		handler2 := NewMockHandler(mockCtrl)

		sampledEvt := NewSampledEventBase(1.0, handler1)
		evt := NewEventBase(2.0, handler2)

		handleSampledEvt := handler1.EXPECT().Handle(sampledEvt)
		handler2.EXPECT().Handle(evt).After(handleSampledEvt)

		engine.Schedule(evt)
		engine.Schedule(sampledEvt)

		_ = engine.Run()
	})

	It("should remove sampled events when disabled", func() {
		handler1 := NewMockHandler(mockCtrl)
		handler2 := NewMockHandler(mockCtrl)

		sampledEvt := NewSampledEventBase(1.0, handler1)
		evt := NewEventBase(1.0, handler2)

		handler2.EXPECT().Handle(evt)

		engine.Schedule(sampledEvt)
		engine.Schedule(evt)
		engine.DisabledSampled()

		_ = engine.Run()
	})

	It("measure triggering speed", func() {
		experiment := gmeasure.NewExperiment("Serial Engine Triggering Speed")
		AddReportEntry(experiment.Name, experiment)

		experiment.MeasureDuration("runtime", func() {
			handler := NewMockHandler(mockCtrl)
			handler.EXPECT().Handle(gomock.Any()).Do(func(e Event) {
				time.Sleep(time.Duration(rand.Uint64()%10) * time.Millisecond)
			}).AnyTimes()

			for i := 0; i < 10000; i++ {
				evt := NewMockEvent(mockCtrl)
				time := VTimeInSec(float64(rand.Uint64()%10) * 0.01)
				evt.EXPECT().Time().Return(time).AnyTimes()
				evt.EXPECT().Handler().Return(handler).AnyTimes()
				evt.EXPECT().IsSecondary().Return(rand.Uint32()%2 == 0).AnyTimes()
				engine.Schedule(evt)
			}
		})
	})
})
