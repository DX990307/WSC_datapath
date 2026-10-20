package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// newM1M2TestComp builds a requester RDMA with M2 (dedup per m2Dedup,
// batching on, no requester L2) and M1 (window W, requester merge per
// m1Merge) on top of it, with one remote destination one hop away.
func newM1M2TestComp(
	t *testing.T,
	m1Merge, m2Dedup bool,
	window int,
) (*Comp, *remoteTestPort, *remoteTestPort, *remoteTestPort) {
	t.Helper()
	c, toL1, _, toOutside, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableDedup:       !m2Dedup,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	c.ConfigureT18(T18Config{ReqMerge: m1Merge, Window: window})
	c.SetT18Topology([2]int{0, 0},
		[]sim.Port{c.ToOutside, remoteGPU}, [][2]int{{0, 0}, {1, 0}})
	if !c.m1Composed() {
		t.Fatal("M1 did not compose with M2")
	}
	return c, toL1, toOutside, remoteGPU
}

func lineData(b byte) []byte {
	d := make([]byte, remoteLineBytes)
	for i := range d {
		d[i] = b
	}
	return d
}

// drainReady runs the response fan-out until nothing is left.
func drainReady(c *Comp, now sim.VTimeInSec) {
	for i := 0; i < 1000 && c.processRemoteReady(now); i++ {
	}
}

func checkAnswered(t *testing.T, toL1 *remoteTestPort, reqs []*mem.ReadReq) {
	t.Helper()
	got := map[string]int{}
	for _, m := range toL1.sent {
		rsp, ok := m.(*mem.DataReadyRsp)
		if !ok {
			t.Fatalf("unexpected L1 response %T", m)
		}
		got[rsp.RespondTo]++
	}
	for _, r := range reqs {
		if got[r.ID] != 1 {
			t.Fatalf("request %s answered %d times, want 1", r.ID, got[r.ID])
		}
	}
	if len(toL1.sent) != len(reqs) {
		t.Fatalf("L1 responses = %d, want %d", len(toL1.sent), len(reqs))
	}
}

func TestM1M2BatchTrackedInM1TableWakesAllWaiters(t *testing.T) {
	for _, mode := range []struct {
		name            string
		m1Merge, m2Dedu bool
	}{{"dedupM1", true, false}, {"dedupBoth", true, true}} {
		t.Run(mode.name, func(t *testing.T) {
			c, toL1, toOutside, _ := newM1M2TestComp(
				t, mode.m1Merge, mode.m2Dedu, 4)
			l1 := &remoteTestPort{name: "L1"}
			reqs := []*mem.ReadReq{
				remoteTestRead(l1, 0x1000), remoteTestRead(l1, 0x1040),
				remoteTestRead(l1, 0x1000), remoteTestRead(l1, 0x1040),
			}
			for _, r := range reqs {
				toL1.inbox = append(toL1.inbox, r)
			}
			c.processFromL1(1e-9)
			if len(toL1.inbox) != 0 || len(c.remoteLines) != 2 {
				t.Fatalf("accepted %d, line entries %d",
					4-len(toL1.inbox), len(c.remoteLines))
			}
			st := c.GetT18Stats()
			if st.ReqMerged != 2 || st.ReqNewEntries != 2 ||
				c.t18.m1Index.Len() != 2 {
				t.Fatalf("M1 merged %d new %d indexed %d",
					st.ReqMerged, st.ReqNewEntries, c.t18.m1Index.Len())
			}
			for c.processRemotePendingBatches(2e-9) {
			}
			c.m1ProcessRemoteBatches(2e-9, false)
			if len(toOutside.sent) != 1 {
				t.Fatalf("network packets = %d, want 1", len(toOutside.sent))
			}
			bitmap, ok := toOutside.sent[0].(*BitmapReadReq)
			if !ok || bitmap.LineBitmap != 0x3 {
				t.Fatalf("want one 2-line bitmap, got %#v", toOutside.sent[0])
			}
			if c.t18.credits.total != 1 {
				t.Fatalf("window slots = %d, want 1 per batch", c.t18.credits.total)
			}
			// A read arriving while the batch is in flight joins its line.
			late := remoteTestRead(l1, 0x1040)
			reqs = append(reqs, late)
			toL1.inbox = append(toL1.inbox, late)
			c.processFromL1(3e-9)
			if c.GetT18Stats().ReqMergedInflight != 1 || len(toOutside.sent) != 1 {
				t.Fatal("in-flight line did not absorb the late read")
			}
			// Partial bitmap responses: the slot is held until the last line.
			rsp1 := &BitmapReadRsp{RespondTo: bitmap.ID,
				LineData: map[uint64][]byte{1: lineData(1)}}
			rsp1.ID = "rsp1"
			c.ToOutside.(*remoteTestPort).inbox = append(
				c.ToOutside.(*remoteTestPort).inbox, rsp1)
			c.processFromOutside(4e-9)
			if c.t18.credits.total != 1 {
				t.Fatal("partial bitmap response released the window slot")
			}
			rsp2 := &BitmapReadRsp{RespondTo: bitmap.ID,
				LineData: map[uint64][]byte{0: lineData(0)}}
			rsp2.ID = "rsp2"
			c.ToOutside.(*remoteTestPort).inbox = append(
				c.ToOutside.(*remoteTestPort).inbox, rsp2)
			c.processFromOutside(5e-9)
			if c.t18.credits.total != 0 || len(c.t18.composedSent) != 0 {
				t.Fatal("completed bitmap kept its window slot")
			}
			drainReady(c, 6e-9)
			checkAnswered(t, toL1, reqs)
			if c.t18.m1Index.Len() != 0 || len(c.remoteLines) != 0 ||
				c.remoteOutstandingReads != 0 {
				t.Fatalf("leftover state: index %d lines %d waiters %d",
					c.t18.m1Index.Len(), len(c.remoteLines),
					c.remoteOutstandingReads)
			}
			for _, m := range toL1.sent {
				r := m.(*mem.DataReadyRsp)
				for _, q := range reqs {
					if q.ID == r.RespondTo && r.Data[0] !=
						byte((q.Address-0x1000)/remoteLineBytes) {
						t.Fatal("waiter got the wrong line")
					}
				}
			}
		})
	}
}

func TestM1M2WindowGatesBatches(t *testing.T) {
	c, toL1, toOutside, _ := newM1M2TestComp(t, true, false, 1)
	l1 := &remoteTestPort{name: "L1"}
	// Two pages: two batches, window 1.
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x1000), remoteTestRead(l1, 0x2000))
	c.processFromL1(1e-9)
	for c.processRemotePendingBatches(2e-9) {
	}
	c.m1ProcessRemoteBatches(2e-9, false)
	if len(toOutside.sent) != 1 || len(c.remoteBatchOrder) != 1 {
		t.Fatalf("sent %d with window 1, waiting batches %d",
			len(toOutside.sent), len(c.remoteBatchOrder))
	}
	first := toOutside.sent[0].(*mem.ReadReq)
	rsp := mem.DataReadyRspBuilder{}.WithRspTo(first.ID).
		WithData(lineData(7)).Build()
	c.ToOutside.(*remoteTestPort).inbox = append(
		c.ToOutside.(*remoteTestPort).inbox, rsp)
	c.processFromOutside(3e-9)
	c.m1ProcessRemoteBatches(4e-9, false)
	if len(toOutside.sent) != 2 {
		t.Fatal("released slot did not admit the waiting batch")
	}
}

// Without any requester dedup, two reads of one line must not share a batch
// slot (M2 batching alone used to overwrite the first entry).
func TestM1M2NoDedupSameLineDoesNotLoseWaiter(t *testing.T) {
	c, toL1, toOutside, _ := newM1M2TestComp(t, false, false, 4)
	l1 := &remoteTestPort{name: "L1"}
	reqs := []*mem.ReadReq{remoteTestRead(l1, 0x1000), remoteTestRead(l1, 0x1000)}
	toL1.inbox = append(toL1.inbox, reqs[0], reqs[1])
	c.processFromL1(1e-9)
	now := sim.VTimeInSec(2e-9)
	for i := 0; i < 10 && len(toOutside.sent) < 2; i++ {
		for c.processRemotePendingBatches(now) {
		}
		c.m1ProcessRemoteBatches(now, false)
		now += 1e-9
	}
	if len(toOutside.sent) != 2 {
		t.Fatalf("network packets = %d, want 2", len(toOutside.sent))
	}
	for _, m := range toOutside.sent {
		r := m.(*mem.ReadReq)
		rsp := mem.DataReadyRspBuilder{}.WithRspTo(r.ID).
			WithData(lineData(0)).Build()
		c.ToOutside.(*remoteTestPort).inbox = append(
			c.ToOutside.(*remoteTestPort).inbox, rsp)
	}
	c.processFromOutside(now)
	drainReady(c, now+1e-9)
	checkAnswered(t, toL1, reqs)
}

func TestM1M2WriteClosesM1Entry(t *testing.T) {
	c, toL1, _, _ := newM1M2TestComp(t, true, false, 4)
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox, remoteTestRead(l1, 0x1000))
	c.processFromL1(1e-9)
	for c.processRemotePendingBatches(2e-9) {
	}
	c.m1ProcessRemoteBatches(2e-9, false)
	toL1.inbox = append(toL1.inbox, remoteTestWrite(l1, 0x1000),
		remoteTestRead(l1, 0x1000))
	c.processFromL1(3e-9)
	if c.GetT18Stats().ReqMerged != 0 || c.GetT18Stats().ReqClosedByWrite != 1 {
		t.Fatal("a read after a write joined the read issued before it")
	}
}

func TestM1M2OwnerMergesBitmapChildIntoMulticast(t *testing.T) {
	owner, _, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, DisableRequesterL2: true,
			MaxBatchLines: 8, MaxBatches: 8})
	owner.SetLocalModuleFinder(&mem.SingleLowModuleFinder{
		LowModule: &remoteTestPort{name: "Owner.L2"}})
	owner.ConfigureT18(T18Config{OwnerMerge: true, OwnerMulticast: true})
	x := &remoteTestPort{name: "X.RDMA"}
	y := &remoteTestPort{name: "Y.RDMA"}
	single := remoteTestRead(x, 0x1000)
	bitmap := &BitmapReadReq{PagePAddr: 0x1000, LineBitmap: 0x3}
	bitmap.ID = "bitmap"
	bitmap.Src = y
	in := owner.ToOutside.(*remoteTestPort)
	in.inbox = append(in.inbox, single, bitmap)
	owner.processFromOutside(1e-9)
	for owner.processRemoteOwnerPendingReqs(2e-9) {
	}
	st := owner.GetT18Stats()
	if len(toL2.sent) != 2 || st.OwnerBatchChildMerged != 1 ||
		st.OwnerBatchChildReads != 1 {
		t.Fatalf("L2 reads %d, child merged %d, child reads %d",
			len(toL2.sent), st.OwnerBatchChildMerged, st.OwnerBatchChildReads)
	}
	for i, m := range toL2.sent {
		r := m.(*mem.ReadReq)
		rsp := mem.DataReadyRspBuilder{}.WithRspTo(r.ID).
			WithData(lineData(byte(i))).Build()
		toL2.inbox = append(toL2.inbox, rsp)
	}
	owner.processFromL2(3e-9)
	owner.t18OwnerSend(4e-9)
	for owner.processRemoteOwnerPendingRsps(4e-9) {
	}
	if len(toOutside.sent) != 2 {
		t.Fatalf("owner packets = %d, want multicast + bitmap", len(toOutside.sent))
	}
	var mc *T18MulticastRsp
	var bm *BitmapReadRsp
	for _, m := range toOutside.sent {
		switch m := m.(type) {
		case *T18MulticastRsp:
			mc = m
		case *BitmapReadRsp:
			bm = m
		}
	}
	if mc == nil || bm == nil || len(bm.LineData) != 1 || bm.LineData[1] == nil {
		t.Fatalf("want a multicast and a one-line bitmap response: %v", toOutside.sent)
	}
	leafX := mc.SplitForDsts([]sim.Port{x}).(*mem.DataReadyRsp)
	leafY := mc.SplitForDsts([]sim.Port{y}).(*BitmapReadRsp)
	if leafX.RespondTo != single.ID || leafY.RespondTo != "bitmap" ||
		leafY.LineData[0] == nil {
		t.Fatal("multicast leaves do not answer the merged requests")
	}
	if len(owner.remoteOwnerBatches) != 0 || len(owner.remoteOwnerSubReqs) != 0 ||
		owner.t18.ownerLive != 0 {
		t.Fatal("owner kept state after all lines were answered")
	}
}
