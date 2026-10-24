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
	// done (P21) replaces Manager.chunkDone when set.
	done func(now sim.VTimeInSec)
	// urgent (P21): pulled before the ordinary queue (an access waits for it).
	urgent bool
	// pre (P21): the page whose pre-placement this chunk copies.
	pre *adaptState
	// install (P21 cmig): the pulled data goes to this function (into the
	// destination GPM's L2) instead of DRAM.
	install func(now sim.VTimeInSec, data []byte)
}

// serving is one pull being served by the source engine.
type serving struct {
	req       *PullReq
	data      []byte
	linesLeft int
	// local (P21): a chunk this engine copies to itself (source and
	// destination on the same GPM, e.g. a 1 KB block that stays at its home
	// while its page moves to a new frame); no network messages.
	local *chunk
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
	urgentQ     []*chunk          // P21: chunks an access waits for
	pulling     map[string]*chunk // by PullReq ID
	inWindow    int
	inBytes     uint64 // bytes of the chunks in the window
	writes      []*mem.WriteReq
	writeChunks map[string]*chunk // by WriteReq ID

	// source side
	reads      []*mem.ReadReq
	readOwners map[string]lineRead // by ReadReq ID
	rsps       []*PullRsp
	invals     []*InvalMsg

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
	return len(e.pending) > 0 || len(e.urgentQ) > 0 || len(e.pulling) > 0 || len(e.writes) > 0 ||
		len(e.writeChunks) > 0 || len(e.reads) > 0 ||
		len(e.readOwners) > 0 || len(e.rsps) > 0 || len(e.invals) > 0
}

func (e *CopyEngine) enqueue(now sim.VTimeInSec, c *chunk) {
	if c.urgent {
		e.urgentQ = append(e.urgentQ, c)
	} else {
		e.pending = append(e.pending, c)
	}
	e.TickLater(now)
}

// promote (P21) moves the queued chunks for which f holds to the urgent
// queue; it returns how many it moved.
func (e *CopyEngine) promote(now sim.VTimeInSec, f func(c *chunk) bool) int {
	n := 0
	keep := e.pending[:0]
	for _, c := range e.pending {
		if f(c) {
			c.urgent = true
			e.urgentQ = append(e.urgentQ, c)
			n++
		} else {
			keep = append(keep, c)
		}
	}
	for i := len(keep); i < len(e.pending); i++ {
		e.pending[i] = nil
	}
	e.pending = keep
	if n > 0 {
		e.TickLater(now)
	}
	return n
}

// QueuedChunks is the number of chunks waiting to be pulled.
func (e *CopyEngine) QueuedChunks() int { return len(e.pending) + len(e.urgentQ) }

// windowOpen: the pull window holds at most PullWindow full chunks of
// bytes (chunks smaller than ChunkBytes, the P21 1 KB blocks, share it).
func (e *CopyEngine) windowOpen(c *chunk) bool {
	return e.inWindow == 0 || e.inBytes+c.size <= uint64(e.cfg.PullWindow)*e.cfg.ChunkBytes
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

// sendInval queues an invalidation message to another engine (P21).
func (e *CopyEngine) sendInval(now sim.VTimeInSec, dst *CopyEngine) {
	e.invals = append(e.invals, newInvalMsg(now, e.remotePort, dst.remotePort))
	e.TickLater(now)
}

func (e *CopyEngine) sendRsps(now sim.VTimeInSec) bool {
	progress := false
	for len(e.invals) > 0 {
		m := e.invals[0]
		m.SendTime = now
		if e.remotePort.Send(m) != nil {
			return progress
		}
		e.invals = e.invals[1:]
		progress = true
	}
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
	for {
		q := &e.pending
		if len(e.urgentQ) > 0 {
			q = &e.urgentQ
		}
		if len(*q) == 0 || !e.windowOpen((*q)[0]) {
			break
		}
		c := (*q)[0]
		if c.src == e {
			*q = (*q)[1:]
			e.inWindow++
			e.inBytes += c.size
			e.serveLines(now, &serving{data: make([]byte, c.size),
				linesLeft: int(c.size / e.cfg.LineBytes), local: c}, c.srcPAddr)
			progress = true
			continue
		}
		req := newPullReq(now, e.remotePort, c.src.remotePort, c.srcPAddr, c.size)
		if e.remotePort.Send(req) != nil {
			break
		}
		*q = (*q)[1:]
		e.pulling[req.ID] = c
		e.inWindow++
		e.inBytes += c.size
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
		case *InvalMsg:
		default:
			log.Panicf("copy engine cannot handle %s", reflect.TypeOf(msg))
		}
	}
}

func (e *CopyEngine) serve(now sim.VTimeInSec, req *PullReq) {
	e.serveLines(now, &serving{
		req:       req,
		data:      make([]byte, req.Size),
		linesLeft: int(req.Size / e.cfg.LineBytes),
	}, req.PAddr)
}

func (e *CopyEngine) serveLines(now sim.VTimeInSec, s *serving, base uint64) {
	line := e.cfg.LineBytes
	for off := uint64(0); off < uint64(len(s.data)); off += line {
		addr := base + off
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
	e.landChunk(now, c, rsp.Data)
}

func (e *CopyEngine) landChunk(now sim.VTimeInSec, c *chunk, data []byte) {
	if c.install != nil {
		c.install(now, data)
		e.inWindow--
		e.inBytes -= c.size
		if c.done != nil {
			c.done(now)
		}
		e.TickLater(now)
		return
	}
	line := e.cfg.LineBytes
	c.linesLeft = int(c.size / line)
	for off := uint64(0); off < c.size; off += line {
		addr := c.dstPAddr + off
		w := mem.WriteReqBuilder{}.
			WithSendTime(now).
			WithSrc(e.dramPort).
			WithDst(e.DRAMFinder.Find(addr)).
			WithAddress(addr).
			WithData(data[off : off+line]).
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
			if lr.s.linesLeft == 0 && lr.s.local != nil {
				e.landChunk(now, lr.s.local, lr.s.data)
			} else if lr.s.linesLeft == 0 {
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
				e.inBytes -= c.size
				if c.done != nil {
					c.done(now)
				} else {
					e.manager.chunkDone(now, c)
				}
			}
		default:
			log.Panicf("copy engine cannot handle %s", reflect.TypeOf(msg))
		}
	}
}
