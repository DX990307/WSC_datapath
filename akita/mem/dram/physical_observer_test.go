package dram

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

type collectingPhysicalDRAMObserver struct {
	events []PhysicalDRAMEvent
}

func (o *collectingPhysicalDRAMObserver) ObservePhysicalDRAM(
	event PhysicalDRAMEvent,
) {
	o.events = append(o.events, event)
}

func TestPhysicalDRAMObserverUsesConvertedAndMappedAddresses(t *testing.T) {
	controller := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithInterleavingAddrConversion(4096, 2, 1, 0, 1<<30).
		Build("GPU[3].DRAM[7]")
	observer := &collectingPhysicalDRAMObserver{}
	controller.SetPhysicalDRAMObserver(observer, "GPU[3]", 7)

	read := mem.ReadReqBuilder{}.
		WithAddress(0x1000).
		WithByteSize(64).
		Build()
	transaction := &signal.Transaction{Read: read, InternalAddress: 0}
	st := &signal.SubTransaction{
		ID: "sub-0", Transaction: transaction, Address: 0x1000,
	}
	transaction.SubTransactions = []*signal.SubTransaction{st}

	event := controller.makePhysicalDRAMEvent(
		PhysicalDRAMSubtransactionArrive, sim.VTimeInSec(2e-9), st)
	if event.ExternalAddress != 0x1000 {
		t.Fatalf("external address %#x", event.ExternalAddress)
	}
	if event.InternalSubtransactionAddress != 0 {
		t.Fatalf("converted subtransaction address %#x",
			event.InternalSubtransactionAddress)
	}
	if event.Owner != "GPU[3]" || event.Controller != 7 {
		t.Fatalf("identity %+v", event)
	}
	if event.PhysicalBytes != 64 { // default 64-bit bus * burst length 8
		t.Fatalf("physical bytes %d", event.PhysicalBytes)
	}
	if event.TimePS != 2000 {
		t.Fatalf("time %d ps", event.TimePS)
	}
}

func TestPhysicalDRAMObserverCapturesControllerLifecycle(t *testing.T) {
	mockController := gomock.NewController(t)
	defer mockController.Finish()
	engine := sim.NewSerialEngine()
	controller := MakeBuilder().WithEngine(engine).Build("GPU[0].DRAM[0]")
	source := NewMockPort(mockController)
	connection := sim.NewDirectConnection("Conn", engine, sim.GHz)
	source.EXPECT().SetConnection(connection)
	connection.PlugIn(controller.topPort, 1)
	connection.PlugIn(source, 1)

	observer := &collectingPhysicalDRAMObserver{}
	controller.SetPhysicalDRAMObserver(observer, "GPU[0]", 0)
	read := mem.ReadReqBuilder{}.
		WithAddress(0x40).
		WithByteSize(4).
		WithSrc(source).
		WithDst(controller.topPort).
		WithSendTime(0).
		Build()
	controller.topPort.Recv(read)
	source.EXPECT().Recv(gomock.Any()).Return(nil)
	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}

	wantKinds := []PhysicalDRAMEventKind{
		PhysicalDRAMSubtransactionArrive,
		PhysicalDRAMCommandEnqueue,
		PhysicalDRAMCommandIssue,    // ACT
		PhysicalDRAMCommandComplete, // ACT
		PhysicalDRAMCommandIssue,    // column read
		PhysicalDRAMCommandComplete, // column read
		PhysicalDRAMTransactionComplete,
	}
	if len(observer.events) != len(wantKinds) {
		t.Fatalf("got %d lifecycle events, want %d: %+v",
			len(observer.events), len(wantKinds), observer.events)
	}
	for i, want := range wantKinds {
		if got := observer.events[i].Kind; got != want {
			t.Fatalf("event %d kind %q, want %q", i, got, want)
		}
	}
	if got := observer.events[1].QueueDepthAfter; got != 1 {
		t.Fatalf("enqueue queue depth %d, want 1", got)
	}
	columnIssue := observer.events[4]
	if columnIssue.CommandKind != "ReadPrecharge" {
		t.Fatalf("column command %q", columnIssue.CommandKind)
	}
	if !columnIssue.ColumnRowHit || !columnIssue.OpenRowValid {
		t.Fatalf("column issue did not capture the open-row hit: %+v",
			columnIssue)
	}
	if observer.events[5].Cycle < columnIssue.Cycle {
		t.Fatal("column command completed before issue")
	}
}
