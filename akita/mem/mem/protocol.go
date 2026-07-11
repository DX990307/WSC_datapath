package mem

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

var accessReqByteOverhead = 12
var accessRspByteOverhead = 4
var controlMsgByteOverhead = 4

// AccessReq abstracts read and write requests that are sent to the
// cache modules or memory controllers.
type AccessReq interface {
	sim.Msg
	GetAddress() uint64
	GetByteSize() uint64
	GetPID() vm.PID
}

// A AccessRsp is a respond in the memory system.
type AccessRsp interface {
	sim.Msg
	sim.Rsp
}

// A ReadReq is a request sent to a memory controller to fetch data
type ReadReq struct {
	sim.MsgMeta

	Address            uint64
	AccessByteSize     uint64
	PID                vm.PID
	CanWaitForCoalesce bool
	// LookupOnly asks a cache to return either a cache hit or a cache miss
	// without allocating an MSHR or accessing the lower memory module.
	LookupOnly bool
	Info       interface{}
}

// Meta returns the message meta.
func (r *ReadReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetByteSize returns the number of byte that the request is accessing.
func (r *ReadReq) GetByteSize() uint64 {
	return r.AccessByteSize
}

// GetAddress returns the address that the request is accessing
func (r *ReadReq) GetAddress() uint64 {
	return r.Address
}

// GetPID returns the process ID that the request is working on.
func (r *ReadReq) GetPID() vm.PID {
	return r.PID
}

// ReadReqBuilder can build read requests.
type ReadReqBuilder struct {
	sendTime           sim.VTimeInSec
	src, dst           sim.Port
	pid                vm.PID
	address, byteSize  uint64
	canWaitForCoalesce bool
	lookupOnly         bool
	info               interface{}
}

// WithSendTime sets the send time of the request to build.
func (b ReadReqBuilder) WithSendTime(t sim.VTimeInSec) ReadReqBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b ReadReqBuilder) WithSrc(src sim.Port) ReadReqBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b ReadReqBuilder) WithDst(dst sim.Port) ReadReqBuilder {
	b.dst = dst
	return b
}

// WithPID sets the PID of the request to build.
func (b ReadReqBuilder) WithPID(pid vm.PID) ReadReqBuilder {
	b.pid = pid
	return b
}

// WithInfo sets the Info of the request to build.
func (b ReadReqBuilder) WithInfo(info interface{}) ReadReqBuilder {
	b.info = info
	return b
}

// WithAddress sets the address of the request to build.
func (b ReadReqBuilder) WithAddress(address uint64) ReadReqBuilder {
	b.address = address
	return b
}

// WithByteSize sets the byte size of the request to build.
func (b ReadReqBuilder) WithByteSize(byteSize uint64) ReadReqBuilder {
	b.byteSize = byteSize
	return b
}

// CanWaitForCoalesce allow the request to build to wait for coalesce.
func (b ReadReqBuilder) CanWaitForCoalesce() ReadReqBuilder {
	b.canWaitForCoalesce = true
	return b
}

// WithLookupOnly makes the request a cache-only lookup. A miss must not be
// forwarded to the cache's lower module.
func (b ReadReqBuilder) WithLookupOnly() ReadReqBuilder {
	b.lookupOnly = true
	return b
}

// Build creates a new ReadReq
func (b ReadReqBuilder) Build() *ReadReq {
	r := &ReadReq{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.TrafficBytes = accessReqByteOverhead
	r.Address = b.address
	r.PID = b.pid
	r.Info = b.info
	r.AccessByteSize = b.byteSize
	r.CanWaitForCoalesce = b.canWaitForCoalesce
	r.LookupOnly = b.lookupOnly
	return r
}

// A WriteReq is a request sent to a memory controller to write data
type WriteReq struct {
	sim.MsgMeta

	Address            uint64
	Data               []byte
	DirtyMask          []bool
	PID                vm.PID
	CanWaitForCoalesce bool
	Info               interface{}
}

// Meta returns the meta data attached to a request.
func (r *WriteReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetByteSize returns the number of byte that the request is writing.
func (r *WriteReq) GetByteSize() uint64 {
	return uint64(len(r.Data))
}

// GetAddress returns the address that the request is accessing
func (r *WriteReq) GetAddress() uint64 {
	return r.Address
}

// GetPID returns the PID of the read address
func (r *WriteReq) GetPID() vm.PID {
	return r.PID
}

// WriteReqBuilder can build read requests.
type WriteReqBuilder struct {
	sendTime           sim.VTimeInSec
	src, dst           sim.Port
	pid                vm.PID
	info               interface{}
	address            uint64
	data               []byte
	dirtyMask          []bool
	canWaitForCoalesce bool
}

// WithSendTime sets the send time of the message to build.
func (b WriteReqBuilder) WithSendTime(t sim.VTimeInSec) WriteReqBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b WriteReqBuilder) WithSrc(src sim.Port) WriteReqBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b WriteReqBuilder) WithDst(dst sim.Port) WriteReqBuilder {
	b.dst = dst
	return b
}

// WithPID sets the PID of the request to build.
func (b WriteReqBuilder) WithPID(pid vm.PID) WriteReqBuilder {
	b.pid = pid
	return b
}

// WithInfo sets the information attached to the request to build.
func (b WriteReqBuilder) WithInfo(info interface{}) WriteReqBuilder {
	b.info = info
	return b
}

// WithAddress sets the address of the request to build.
func (b WriteReqBuilder) WithAddress(address uint64) WriteReqBuilder {
	b.address = address
	return b
}

// WithData sets the data of the request to build.
func (b WriteReqBuilder) WithData(data []byte) WriteReqBuilder {
	b.data = data
	return b
}

// WithDirtyMask sets the dirty mask of the request to build.
func (b WriteReqBuilder) WithDirtyMask(mask []bool) WriteReqBuilder {
	b.dirtyMask = mask
	return b
}

// CanWaitForCoalesce allow the request to build to wait for coalesce.
func (b WriteReqBuilder) CanWaitForCoalesce() WriteReqBuilder {
	b.canWaitForCoalesce = true
	return b
}

// Build creates a new WriteReq
func (b WriteReqBuilder) Build() *WriteReq {
	r := &WriteReq{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.PID = b.pid
	r.Info = b.info
	r.Address = b.address
	r.Data = b.data
	r.TrafficBytes = len(r.Data) + accessReqByteOverhead
	r.DirtyMask = b.dirtyMask
	r.CanWaitForCoalesce = b.canWaitForCoalesce
	return r
}

// A DataReadyRsp is the respond sent from the lower module to the higher
// module that carries the data loaded.
type DataReadyRsp struct {
	sim.MsgMeta

	RespondTo string // The ID of the request it replies
	Data      []byte
}

// Meta returns the meta data attached to each message.
func (r *DataReadyRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetRspTo returns the ID if the request that the respond is responding to.
func (r *DataReadyRsp) GetRspTo() string {
	return r.RespondTo
}

// DataReadyRspBuilder can build data ready responds.
type DataReadyRspBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	rspTo    string
	data     []byte
}

// WithSendTime sets the send time of the request to build.
func (b DataReadyRspBuilder) WithSendTime(
	t sim.VTimeInSec,
) DataReadyRspBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b DataReadyRspBuilder) WithSrc(src sim.Port) DataReadyRspBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b DataReadyRspBuilder) WithDst(dst sim.Port) DataReadyRspBuilder {
	b.dst = dst
	return b
}

// WithRspTo sets ID of the request that the respond to build is replying to.
func (b DataReadyRspBuilder) WithRspTo(id string) DataReadyRspBuilder {
	b.rspTo = id
	return b
}

// WithData sets the data of the request to build.
func (b DataReadyRspBuilder) WithData(data []byte) DataReadyRspBuilder {
	b.data = data
	return b
}

// Build creates a new DataReadyRsp
func (b DataReadyRspBuilder) Build() *DataReadyRsp {
	r := &DataReadyRsp{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.TrafficBytes = len(b.data) + accessRspByteOverhead
	r.RespondTo = b.rspTo
	r.Data = b.data
	return r
}

// CacheLookupRsp reports the result of a lookup-only cache request. A miss
// carries no data and is never forwarded to lower memory.
type CacheLookupRsp struct {
	sim.MsgMeta

	RespondTo  string
	Hit        bool
	Data       []byte
	Generation uint64
}

// Meta returns the metadata attached to the response.
func (r *CacheLookupRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetRspTo returns the request ID that this response completes.
func (r *CacheLookupRsp) GetRspTo() string {
	return r.RespondTo
}

// CacheLookupRspBuilder builds lookup-only cache responses.
type CacheLookupRspBuilder struct {
	sendTime   sim.VTimeInSec
	src, dst   sim.Port
	rspTo      string
	hit        bool
	data       []byte
	generation uint64
}

// WithSendTime sets the send time.
func (b CacheLookupRspBuilder) WithSendTime(
	t sim.VTimeInSec,
) CacheLookupRspBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source port.
func (b CacheLookupRspBuilder) WithSrc(src sim.Port) CacheLookupRspBuilder {
	b.src = src
	return b
}

// WithDst sets the destination port.
func (b CacheLookupRspBuilder) WithDst(dst sim.Port) CacheLookupRspBuilder {
	b.dst = dst
	return b
}

// WithRspTo sets the completed request ID.
func (b CacheLookupRspBuilder) WithRspTo(id string) CacheLookupRspBuilder {
	b.rspTo = id
	return b
}

// WithHit sets the lookup result.
func (b CacheLookupRspBuilder) WithHit(hit bool) CacheLookupRspBuilder {
	b.hit = hit
	return b
}

// WithData sets the returned cache-line data.
func (b CacheLookupRspBuilder) WithData(data []byte) CacheLookupRspBuilder {
	b.data = data
	return b
}

// WithGeneration records the L2 remote-replica generation observed by the
// lookup.
func (b CacheLookupRspBuilder) WithGeneration(
	generation uint64,
) CacheLookupRspBuilder {
	b.generation = generation
	return b
}

// Build creates a CacheLookupRsp.
func (b CacheLookupRspBuilder) Build() *CacheLookupRsp {
	r := &CacheLookupRsp{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.TrafficBytes = len(b.data) + accessRspByteOverhead
	r.RespondTo = b.rspTo
	r.Hit = b.hit
	r.Data = b.data
	r.Generation = b.generation
	return r
}

// RemoteDataFill is a best-effort clean fill of a line fetched from a remote
// GPU. It is not a demand response and may be dropped by a busy cache.
type RemoteDataFill struct {
	sim.MsgMeta

	Address    uint64
	PID        vm.PID
	Data       []byte
	Info       interface{}
	Generation uint64
	Prefetch   bool
}

// Meta returns the metadata attached to the fill.
func (r *RemoteDataFill) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// RemoteDataFillBuilder builds remote clean-fill messages.
type RemoteDataFillBuilder struct {
	sendTime   sim.VTimeInSec
	src, dst   sim.Port
	pid        vm.PID
	address    uint64
	data       []byte
	info       interface{}
	generation uint64
	prefetch   bool
}

// WithSendTime sets the send time.
func (b RemoteDataFillBuilder) WithSendTime(
	t sim.VTimeInSec,
) RemoteDataFillBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source port.
func (b RemoteDataFillBuilder) WithSrc(src sim.Port) RemoteDataFillBuilder {
	b.src = src
	return b
}

// WithDst sets the destination port.
func (b RemoteDataFillBuilder) WithDst(dst sim.Port) RemoteDataFillBuilder {
	b.dst = dst
	return b
}

// WithPID sets the process ID.
func (b RemoteDataFillBuilder) WithPID(pid vm.PID) RemoteDataFillBuilder {
	b.pid = pid
	return b
}

// WithAddress sets the aligned cache-line address.
func (b RemoteDataFillBuilder) WithAddress(addr uint64) RemoteDataFillBuilder {
	b.address = addr
	return b
}

// WithData sets the cache-line data.
func (b RemoteDataFillBuilder) WithData(data []byte) RemoteDataFillBuilder {
	b.data = data
	return b
}

// WithInfo sets optional tracing metadata.
func (b RemoteDataFillBuilder) WithInfo(info interface{}) RemoteDataFillBuilder {
	b.info = info
	return b
}

// WithGeneration sets the L2 generation returned by the preceding lookup.
func (b RemoteDataFillBuilder) WithGeneration(
	generation uint64,
) RemoteDataFillBuilder {
	b.generation = generation
	return b
}

// WithPrefetch marks a fill whose line was fetched speculatively as the
// adjacent line of an RDMA access-unit batch.
func (b RemoteDataFillBuilder) WithPrefetch(
	prefetch bool,
) RemoteDataFillBuilder {
	b.prefetch = prefetch
	return b
}

// Build creates a RemoteDataFill.
func (b RemoteDataFillBuilder) Build() *RemoteDataFill {
	r := &RemoteDataFill{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.TrafficBytes = len(b.data) + accessRspByteOverhead
	r.Address = b.address
	r.PID = b.pid
	r.Data = b.data
	r.Info = b.info
	r.Generation = b.generation
	r.Prefetch = b.prefetch
	return r
}

// RemoteDataFillRsp acknowledges that a best-effort remote fill was consumed.
// Installed is false when the cache safely dropped the fill.
type RemoteDataFillRsp struct {
	sim.MsgMeta
	RespondTo string
	Installed bool
}

// Meta returns the response metadata.
func (r *RemoteDataFillRsp) Meta() *sim.MsgMeta { return &r.MsgMeta }

// GetRspTo returns the completed fill ID.
func (r *RemoteDataFillRsp) GetRspTo() string { return r.RespondTo }

// RemoteDataFillRspBuilder builds fill acknowledgements.
type RemoteDataFillRspBuilder struct {
	sendTime  sim.VTimeInSec
	src, dst  sim.Port
	rspTo     string
	installed bool
}

// WithSendTime sets the send time.
func (b RemoteDataFillRspBuilder) WithSendTime(
	t sim.VTimeInSec,
) RemoteDataFillRspBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source port.
func (b RemoteDataFillRspBuilder) WithSrc(src sim.Port) RemoteDataFillRspBuilder {
	b.src = src
	return b
}

// WithDst sets the destination port.
func (b RemoteDataFillRspBuilder) WithDst(dst sim.Port) RemoteDataFillRspBuilder {
	b.dst = dst
	return b
}

// WithRspTo sets the fill ID.
func (b RemoteDataFillRspBuilder) WithRspTo(id string) RemoteDataFillRspBuilder {
	b.rspTo = id
	return b
}

// WithInstalled records whether the line was installed.
func (b RemoteDataFillRspBuilder) WithInstalled(
	installed bool,
) RemoteDataFillRspBuilder {
	b.installed = installed
	return b
}

// Build creates a RemoteDataFillRsp.
func (b RemoteDataFillRspBuilder) Build() *RemoteDataFillRsp {
	r := &RemoteDataFillRsp{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.TrafficBytes = accessRspByteOverhead
	r.RespondTo = b.rspTo
	r.Installed = b.installed
	return r
}

// A WriteDoneRsp is a respond sent from the lower module to the higher module
// to mark a previous requests is completed successfully.
type WriteDoneRsp struct {
	sim.MsgMeta

	RespondTo string
}

// Meta returns the meta data associated with the message.
func (r *WriteDoneRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetRspTo returns the ID of the request that the respond is responding to.
func (r *WriteDoneRsp) GetRspTo() string {
	return r.RespondTo
}

// WriteDoneRspBuilder can build data ready responds.
type WriteDoneRspBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	rspTo    string
}

// WithSendTime sets the send time of the message to build.
func (b WriteDoneRspBuilder) WithSendTime(
	t sim.VTimeInSec,
) WriteDoneRspBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b WriteDoneRspBuilder) WithSrc(src sim.Port) WriteDoneRspBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b WriteDoneRspBuilder) WithDst(dst sim.Port) WriteDoneRspBuilder {
	b.dst = dst
	return b
}

// WithRspTo sets ID of the request that the respond to build is replying to.
func (b WriteDoneRspBuilder) WithRspTo(id string) WriteDoneRspBuilder {
	b.rspTo = id
	return b
}

// Build creates a new WriteDoneRsp
func (b WriteDoneRspBuilder) Build() *WriteDoneRsp {
	r := &WriteDoneRsp{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.TrafficBytes = accessRspByteOverhead
	r.SendTime = b.sendTime
	r.RespondTo = b.rspTo
	return r
}

// ControlMsg is the commonly used message type for controlling the components
// on the memory hierarchy. It is also used for resonpding the original
// requester with the Done field.
type ControlMsg struct {
	sim.MsgMeta

	DiscardTransations bool
	Restart            bool
	NotifyDone         bool
}

// Meta returns the meta data assocated with the ControlMsg.
func (m *ControlMsg) Meta() *sim.MsgMeta {
	return &m.MsgMeta
}

// A ControlMsgBuilder can build control messages.
type ControlMsgBuilder struct {
	sendTime            sim.VTimeInSec
	src, dst            sim.Port
	discardTransactions bool
	restart             bool
	notifyDone          bool
}

// WithSendTime sets the send time of the message to build.
func (b ControlMsgBuilder) WithSendTime(
	t sim.VTimeInSec,
) ControlMsgBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b ControlMsgBuilder) WithSrc(src sim.Port) ControlMsgBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b ControlMsgBuilder) WithDst(dst sim.Port) ControlMsgBuilder {
	b.dst = dst
	return b
}

// ToDiscardTransactions sets the discard transactions bit of the control
// messages to 1.
func (b ControlMsgBuilder) ToDiscardTransactions() ControlMsgBuilder {
	b.discardTransactions = true
	return b
}

// ToRestart sets the restart bit of the control messages to 1.
func (b ControlMsgBuilder) ToRestart() ControlMsgBuilder {
	b.restart = true
	return b
}

// ToNotifyDone sets the "notify done" bit of the control messages to 1.
func (b ControlMsgBuilder) ToNotifyDone() ControlMsgBuilder {
	b.notifyDone = true
	return b
}

// Build creates a new ControlMsg.
func (b ControlMsgBuilder) Build() *ControlMsg {
	m := &ControlMsg{}
	m.ID = sim.GetIDGenerator().Generate()
	m.Src = b.src
	m.Dst = b.dst
	m.TrafficBytes = controlMsgByteOverhead
	m.SendTime = b.sendTime

	m.DiscardTransations = b.discardTransactions
	m.Restart = b.restart
	m.NotifyDone = b.notifyDone

	return m
}

// GL0InvalidateReq is a request that invalidates the L0 cache.
type GL0InvalidateReq struct {
	sim.MsgMeta
	PID vm.PID
}

// Meta returns the meta data associated with the message.
func (r *GL0InvalidateReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetByteSize returns the number of byte that the request is accessing.
func (r *GL0InvalidateReq) GetByteSize() uint64 {
	return 0
}

// GetAddress returns the address that the request is accessing
func (r *GL0InvalidateReq) GetAddress() uint64 {
	return 0
}

// GetPID returns the process ID that the request is working on.
func (r *GL0InvalidateReq) GetPID() vm.PID {
	return r.PID
}

// GL0InvalidateReqBuilder can build new GL0InvalidReq.
type GL0InvalidateReqBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	PID      vm.PID
}

// WithSendTime sets the send time of the request to build.
func (b GL0InvalidateReqBuilder) WithSendTime(
	t sim.VTimeInSec,
) GL0InvalidateReqBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b GL0InvalidateReqBuilder) WithSrc(src sim.Port) GL0InvalidateReqBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b GL0InvalidateReqBuilder) WithDst(dst sim.Port) GL0InvalidateReqBuilder {
	b.dst = dst
	return b
}

// WithPID sets the PID of the request to build.
func (b GL0InvalidateReqBuilder) WithPID(pid vm.PID) GL0InvalidateReqBuilder {
	b.PID = pid
	return b
}

// Build creates a new GL0InvalidateReq
func (b GL0InvalidateReqBuilder) Build() *GL0InvalidateReq {
	r := &GL0InvalidateReq{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	return r
}

// GL0InvalidateRsp is a response to a GL0InvalidateReq.
type GL0InvalidateRsp struct {
	sim.MsgMeta
	PID       vm.PID
	RespondTo string
}

// Meta returns the meta data associated with the message.
func (r *GL0InvalidateRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetByteSize returns the number of byte that the request is accessing.
func (r *GL0InvalidateRsp) GetByteSize() uint64 {
	return 0
}

// GetAddress returns the address that the request is accessing
func (r *GL0InvalidateRsp) GetAddress() uint64 {
	return 0
}

// GetPID returns the process ID that the request is working on.
func (r *GL0InvalidateRsp) GetPID() vm.PID {
	return r.PID
}

// GetRspTo returns the ID of the request that this response is responding to.
func (r *GL0InvalidateRsp) GetRspTo() string {
	return r.RespondTo
}

// GL0InvalidateRspBuilder can build new GL0 Invalid Rsp Builder
type GL0InvalidateRspBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	PID      vm.PID
	rspTo    string
}

// WithSendTime sets the send time of the request to build.:w
func (b GL0InvalidateRspBuilder) WithSendTime(
	t sim.VTimeInSec,
) GL0InvalidateRspBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b GL0InvalidateRspBuilder) WithSrc(src sim.Port) GL0InvalidateRspBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b GL0InvalidateRspBuilder) WithDst(dst sim.Port) GL0InvalidateRspBuilder {
	b.dst = dst
	return b
}

// WithPID sets the PID of the request to build.
func (b GL0InvalidateRspBuilder) WithPID(pid vm.PID) GL0InvalidateRspBuilder {
	b.PID = pid
	return b
}

// WithRspTo sets ID of the request that the respond to build is replying to.
func (b GL0InvalidateRspBuilder) WithRspTo(id string) GL0InvalidateRspBuilder {
	b.rspTo = id
	return b
}

// GetRespondTo returns the ID if the request that the respond is responding to.
func (r *GL0InvalidateRsp) GetRespondTo() string {
	return r.RespondTo
}

// Build creates a new CUPipelineRestartReq
func (b GL0InvalidateRspBuilder) Build() *GL0InvalidateRsp {
	r := &GL0InvalidateRsp{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.RespondTo = b.rspTo
	return r
}
