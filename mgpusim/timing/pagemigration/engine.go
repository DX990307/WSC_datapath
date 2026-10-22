package pagemigration

import (
	"log"
	"reflect"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// chunk is one pull of a page copy, seen from the destination engine.
type chunk struct {
	u         *unit
	src       *CopyEngine
	srcPAddr  uint64
	dstPAddr  uint64
	size      uint64
	linesLeft int
}

// serving is one pull being served by the source engine.
type serving struct {
	req       *PullReq
	data      []byte
	linesLeft int
}

type lineRead struct {
	s      *serving
	offset uint64
}

// CopyEngine is the per-GPM DMA engine that moves migrating pages. As a
// destination it pulls chunks from source engines over the network (at most
// PullWindow chunks in flight) and writes them into its GPM's DRAM (the new
// frames are not cached anywhere, and writing around the L2 leaves no dirty
// copies that a later functional host copy could shadow); as a source it
// serves pulls by reading through its GPM's L2, so dirty lines are copied.
// Each side keeps at most L2Window line accesses in flight to memory.
type CopyEngine struct {
	*sim.TickingComponent

	remotePort sim.Port
	l2Port     sim.Port
	dramPort   sim.Port

	// L2Finder finds the L2 slice of a local physical address, DRAMFinder its
	// memory controller.
	L2Finder   mem.LowModuleFinder
	DRAMFinder mem.LowModuleFinder

	cfg     Config
	manager *Manager

	// destination side
	pending     []*chunk
	pulling     map[string]*chunk // by PullReq ID
	inWindow    int
	writes      []*mem.WriteReq
	writeChunks map[string]*chunk // by WriteReq ID

	// source side
	reads      []*mem.ReadReq
	readOwners map[string]lineRead // by ReadReq ID
	rsps       []*PullRsp

	l2Inflight   int
	dramInflight int

	// statistics
	PulledBytes uint64
	ServedBytes uint64
}

// NewCopyEngine creates a copy engine.
func NewCopyEngine(name string, engine sim.Engine, cfg Config) *CopyEngine {
	e := &CopyEngine{
		cfg:         cfg,
		pulling:     make(map[string]*chunk),
		writeChunks: make(map[string]*chunk),
		readOwners:  make(map[string]lineRead),
	}
	e.TickingComponent = sim.NewTickingComponent(name, engine, 1*sim.GHz, e)
	e.remotePort = sim.NewLimitNumMsgPort(e, 64, name+".Remote")
	e.AddPort("Remote", e.remotePort)
	e.l2Port = sim.NewLimitNumMsgPort(e, 2*cfg.L2Window, name+".L2")
	e.AddPort("L2", e.l2Port)
	e.dramPort = sim.NewLimitNumMsgPort(e, 2*cfg.L2Window, name+".DRAM")
	e.AddPort("DRAM", e.dramPort)
	return e
}

// DRAMPort is the port that writes new frames to the GPM's memory
// controllers (destination side), around the L2.
func (e *CopyEngine) DRAMPort() sim.Port { return e.dramPort }

// RemotePort is the port on the wafer network.
func (e *CopyEngine) RemotePort() sim.Port { return e.remotePort }

// L2Port is the port that reads the GPM's L2 slices (source side).
func (e *CopyEngine) L2Port() sim.Port { return e.l2Port }

// Busy reports whether the engine has work left.
func (e *CopyEngine) Busy() bool {
	return len(e.pending) > 0 || len(e.pulling) > 0 || len(e.writes) > 0 ||
		len(e.writeChunks) > 0 || len(e.reads) > 0 ||
		len(e.readOwners) > 0 || len(e.rsps) > 0
}

func (e *CopyEngine) enqueue(now sim.VTimeInSec, c *chunk) {
	e.pending = append(e.pending, c)
	e.TickLater(now)
}

// Tick runs one cycle.
func (e *CopyEngine) Tick(now sim.VTimeInSec) bool {
	progress := false
	progress = e.sendRsps(now) || progress
	progress = e.sendPulls(now) || progress
	progress = e.issueL2(now) || progress
	progress = e.recvRemote(now) || progress
	progress = e.recvMem(now, e.l2Port) || progress
	progress = e.recvMem(now, e.dramPort) || progress
	return progress
}

func (e *CopyEngine) sendRsps(now sim.VTimeInSec) bool {
	progress := false
	for len(e.rsps) > 0 {
		r := e.rsps[0]
		r.SendTime = now
		if e.remotePort.Send(r) != nil {
			break
		}
		e.ServedBytes += uint64(len(r.Data))
		e.rsps = e.rsps[1:]
		progress = true
	}
	return progress
}

func (e *CopyEngine) sendPulls(now sim.VTimeInSec) bool {
	progress := false
	for e.inWindow < e.cfg.PullWindow && len(e.pending) > 0 {
		c := e.pending[0]
		req := newPullReq(now, e.remotePort, c.src.remotePort, c.srcPAddr, c.size)
		if e.remotePort.Send(req) != nil {
			break
		}
		e.pending = e.pending[1:]
		e.pulling[req.ID] = c
		e.inWindow++
		progress = true
	}
	return progress
}

func (e *CopyEngine) issueL2(now sim.VTimeInSec) bool {
	progress := false
	for e.dramInflight < e.cfg.L2Window && len(e.writes) > 0 {
		w := e.writes[0]
		w.SendTime = now
		if e.dramPort.Send(w) != nil {
			break
		}
		e.writes = e.writes[1:]
		e.dramInflight++
		progress = true
	}
	for e.l2Inflight < e.cfg.L2Window && len(e.reads) > 0 {
		r := e.reads[0]
		r.SendTime = now
		if e.l2Port.Send(r) != nil {
			return progress
		}
		e.reads = e.reads[1:]
		e.l2Inflight++
		progress = true
	}
	return progress
}

func (e *CopyEngine) recvRemote(now sim.VTimeInSec) bool {
	progress := false
	for {
		msg := e.remotePort.Retrieve(now)
		if msg == nil {
			return progress
		}
		progress = true
		switch msg := msg.(type) {
		case *PullReq:
			e.serve(now, msg)
		case *PullRsp:
			e.land(now, msg)
		default:
			log.Panicf("copy engine cannot handle %s", reflect.TypeOf(msg))
		}
	}
}

func (e *CopyEngine) serve(now sim.VTimeInSec, req *PullReq) {
	line := e.cfg.LineBytes
	s := &serving{
		req:       req,
		data:      make([]byte, req.Size),
		linesLeft: int(req.Size / line),
	}
	for off := uint64(0); off < req.Size; off += line {
		addr := req.PAddr + off
		r := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(e.l2Port).
			WithDst(e.L2Finder.Find(addr)).
			WithAddress(addr).
			WithByteSize(line).
			WithPID(0).
			Build()
		e.reads = append(e.reads, r)
		e.readOwners[r.ID] = lineRead{s: s, offset: off}
	}
}

func (e *CopyEngine) land(now sim.VTimeInSec, rsp *PullRsp) {
	c, ok := e.pulling[rsp.RespondTo]
	if !ok {
		log.Panicf("copy engine %s: unknown pull %s", e.Name(), rsp.RespondTo)
	}
	delete(e.pulling, rsp.RespondTo)
	e.PulledBytes += uint64(len(rsp.Data))
	line := e.cfg.LineBytes
	c.linesLeft = int(c.size / line)
	for off := uint64(0); off < c.size; off += line {
		addr := c.dstPAddr + off
		w := mem.WriteReqBuilder{}.
			WithSendTime(now).
			WithSrc(e.dramPort).
			WithDst(e.DRAMFinder.Find(addr)).
			WithAddress(addr).
			WithData(rsp.Data[off : off+line]).
			WithPID(0).
			Build()
		e.writes = append(e.writes, w)
		e.writeChunks[w.ID] = c
	}
}

func (e *CopyEngine) recvMem(now sim.VTimeInSec, port sim.Port) bool {
	progress := false
	for {
		msg := port.Retrieve(now)
		if msg == nil {
			return progress
		}
		progress = true
		switch msg := msg.(type) {
		case *mem.DataReadyRsp:
			e.l2Inflight--
			lr, ok := e.readOwners[msg.RespondTo]
			if !ok {
				log.Panicf("copy engine %s: unknown read %s", e.Name(), msg.RespondTo)
			}
			delete(e.readOwners, msg.RespondTo)
			copy(lr.s.data[lr.offset:], msg.Data)
			lr.s.linesLeft--
			if lr.s.linesLeft == 0 {
				e.rsps = append(e.rsps, newPullRsp(now, e.remotePort,
					lr.s.req.Src, lr.s.req.ID, lr.s.data))
			}
		case *mem.WriteDoneRsp:
			e.dramInflight--
			c, ok := e.writeChunks[msg.RespondTo]
			if !ok {
				log.Panicf("copy engine %s: unknown write %s", e.Name(), msg.RespondTo)
			}
			delete(e.writeChunks, msg.RespondTo)
			c.linesLeft--
			if c.linesLeft == 0 {
				e.inWindow--
				e.manager.chunkDone(now, c)
			}
		default:
			log.Panicf("copy engine cannot handle %s", reflect.TypeOf(msg))
		}
	}
}
