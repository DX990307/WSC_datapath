package writearound

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type bankActionType int

const (
	bankActionInvalid bankActionType = iota
	bankActionReadHit
	bankActionWrite
	bankActionWriteFetched
	bankActionRemoteDataHit
)

type transaction struct {
	id string

	read         *mem.ReadReq
	readToBottom *mem.ReadReq

	write         *mem.WriteReq
	writeToBottom *mem.WriteReq

	preCoalesceTransactions []*transaction

	bankAction            bankActionType
	block                 *cache.Block
	data                  []byte
	writeFetchedDirtyMask []bool
	remoteDataHitData     []byte

	fetchAndWrite    bool
	done             bool
	startTime        sim.VTimeInSec
	remoteBottom     bool
	remoteDataFill   bool
	directDramBypass bool
	directDramPort   sim.Port
	l2FillPort       sim.Port

	l1vDirStart             sim.VTimeInSec
	l1vDirFirstAttempt      sim.VTimeInSec
	l1vDirFirstAttemptValid bool
	l1vDirStallReason       string
	l1vDirStallStart        sim.VTimeInSec

	m1DirectDRAMBatchArrival sim.VTimeInSec
	m1DirectDRAMBatchID      uint64
}

func (t *transaction) Address() uint64 {
	if t.read != nil {
		return t.read.Address
	}
	return t.write.Address
}

func (t *transaction) PID() vm.PID {
	if t.read != nil {
		return t.read.PID
	}
	return t.write.PID
}

func (t *transaction) accessReq() mem.AccessReq {
	if t.read != nil {
		return t.read
	}
	if t.write != nil {
		return t.write
	}
	return nil
}

func accessReqInfo(req mem.AccessReq) interface{} {
	switch req := req.(type) {
	case *mem.ReadReq:
		return req.Info
	case *mem.WriteReq:
		return req.Info
	default:
		return nil
	}
}

func accessReqOp(req mem.AccessReq) string {
	switch req.(type) {
	case *mem.ReadReq:
		return "read"
	case *mem.WriteReq:
		return "write"
	default:
		return "unknown"
	}
}
