package rdma

// ISCA'27 P19 Q2: reader-bitmap push of hot-broadcast pages (geo-push mode
// "readers"; off by default).
//
// Owner. For every page it serves remote reads of, the owner keeps a 48-bit
// reader bitmap for the current and the previous window of WindowNS (at most
// MaxPages pages, LRU). When it returns a read response for lines of a hot
// page, it also pushes those lines (same GeoMulticast packet along the mesh
// multicast tree as geo-push) to the GPMs that read the page in the current
// or previous window, except the requesters and itself. A page is hot when
// it is in the oracle list Pages (pgcls "hot broadcast", reported as an
// oracle classification), or, without a list, when the previous or current
// window had at least MinReaders distinct readers.
//
// Receivers handle the push exactly like a geo-push leaf (geoReceiveLine):
// give the data to waiting reads, else install it into an invalid L2 way as
// an M3 replica line (never evicts; dropped when stale, written by the
// receiver, already present). Consistency rules are M3's (and A1's).

import (
	"container/list"
	"math/bits"

	"github.com/sarchlab/akita/v3/sim"
)

type q2Page struct {
	key      geoLineKey // line = page address
	win      int64
	cur, pre uint64
	el       *list.Element
}

type q2Readers struct {
	windowNS   float64
	minReaders int
	maxPages   int
	oracle     map[uint64]bool
	pages      map[geoLineKey]*q2Page
	lru        *list.List
	// statistics
	hotRsps, coldRsps, evictions uint64
}

func newQ2Readers(cfg GeoPushConfig) *q2Readers {
	q := &q2Readers{windowNS: cfg.ReaderWindowNS, minReaders: cfg.ReaderMin,
		maxPages: cfg.ReaderMaxPages, oracle: cfg.ReaderPages,
		pages: map[geoLineKey]*q2Page{}, lru: list.New()}
	if q.windowNS <= 0 {
		q.windowNS = 5000
	}
	if q.minReaders <= 0 {
		q.minReaders = 4
	}
	if q.maxPages <= 0 {
		q.maxPages = 1024
	}
	return q
}

func (q *q2Readers) page(pid uint32, addr uint64, now sim.VTimeInSec) *q2Page {
	k := geoLineKey{pid, addr &^ (remotePageBytes - 1)}
	w := int64(float64(now) * 1e9 / q.windowNS)
	p := q.pages[k]
	if p == nil {
		if len(q.pages) >= q.maxPages {
			old := q.lru.Back().Value.(*q2Page)
			q.lru.Remove(old.el)
			delete(q.pages, old.key)
			q.evictions++
		}
		p = &q2Page{key: k, win: w}
		p.el = q.lru.PushFront(p)
		q.pages[k] = p
	} else {
		q.lru.MoveToFront(p.el)
	}
	switch {
	case w == p.win+1:
		p.pre, p.cur, p.win = p.cur, 0, w
	case w > p.win+1:
		p.pre, p.cur, p.win = 0, 0, w
	}
	return p
}

func (q *q2Readers) record(pid uint32, addr uint64, reader int, now sim.VTimeInSec) {
	if reader < 0 || reader >= 64 {
		return
	}
	p := q.page(pid, addr, now)
	p.cur |= 1 << uint(reader)
}

// targets returns the push targets for a response of line addr (nil = cold).
func (q *q2Readers) targets(pid uint32, addr uint64, now sim.VTimeInSec,
	exclude []int) []int {
	pageAddr := addr &^ (remotePageBytes - 1)
	p := q.page(pid, addr, now)
	set := p.cur | p.pre
	hot := false
	if q.oracle != nil {
		hot = q.oracle[pageAddr]
	} else {
		hot = bits.OnesCount64(p.pre) >= q.minReaders ||
			bits.OnesCount64(p.cur) >= q.minReaders
	}
	if !hot {
		q.coldRsps++
		return nil
	}
	q.hotRsps++
	for _, e := range exclude {
		if e >= 0 && e < 64 {
			set &^= 1 << uint(e)
		}
	}
	var out []int
	for set != 0 {
		i := bits.TrailingZeros64(set)
		out = append(out, i)
		set &^= 1 << uint(i)
	}
	return out
}
