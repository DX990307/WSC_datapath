package trace

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sarchlab/akita/v3/sim"
)

const (
	l1vPathHopsHeader = "path_id,sequence,parent_req_id,parent_count,access_type,pid,vaddr,paddr,page_paddr,cacheline_addr,bytes,requester_gpm,owner_gpm,is_remote,route,final_source,segment,from_component,from_port,to_component,to_port,request_msg_id,response_msg_id,start_time_ns,end_time_ns,latency_ns,l1v_result,l2_result,notes"
)

var l1vPathStageColumns = []string{
	"at_to_l1v_top",
	"l1v_coalesce_wait",
	"l1v_batch_wait",
	"l1v_dir_lookup",
	"l1v_dir_stall_post_pipeline_buffer",
	"l1v_dir_stall_victim_locked",
	"l1v_dir_stall_mshr_full",
	"l1v_dir_stall_bottom_blocked",
	"l1v_dir_stall_bank_buffer_full",
	"l1v_bank_hit",
	"l1v_bottom_send_to_local_l2",
	"l1v_bottom_send_to_local_rdma",
	"local_rdma_request_output_wait",
	"local_rdma_to_remote_rdma_request",
	"cross_gpu_request_endpoint_queue",
	"cross_gpu_request_endpoint_inject_wait",
	"cross_gpu_request_channel_transfer",
	"cross_gpu_request_switch_input_queue",
	"cross_gpu_request_switch_pipeline",
	"cross_gpu_request_switch_route_wait",
	"cross_gpu_request_switch_arb_wait",
	"cross_gpu_request_switch_output_wait",
	"cross_gpu_request_endpoint_assemble_wait",
	"cross_gpu_request_endpoint_deliver_wait",
	"remote_rdma_request_output_wait",
	"remote_rdma_to_remote_l2",
	"l2_top_to_dir",
	"l2_dir_lookup",
	"l2_bank_hit",
	"l2_mshr_wait",
	"l2_writebuffer_wait",
	"l2_bottom_send_to_dram",
	"dram_queue_and_service",
	"dram_to_l2_response",
	"l2_fill_and_response",
	"remote_l2_to_remote_rdma_response",
	"remote_rdma_response_output_wait",
	"remote_rdma_to_local_rdma_response",
	"cross_gpu_return_endpoint_queue",
	"cross_gpu_return_endpoint_inject_wait",
	"cross_gpu_return_channel_transfer",
	"cross_gpu_return_switch_input_queue",
	"cross_gpu_return_switch_pipeline",
	"cross_gpu_return_switch_route_wait",
	"cross_gpu_return_switch_arb_wait",
	"cross_gpu_return_switch_output_wait",
	"cross_gpu_return_endpoint_assemble_wait",
	"cross_gpu_return_endpoint_deliver_wait",
	"local_rdma_response_output_wait",
	"local_rdma_to_l1v_response",
	"local_l2_to_l1v_response",
	"l1v_bottom_response_parse",
	"l1v_mshr_wakeup",
	"l1v_fill_parent_done",
}

var l1vDirStallStages = []string{
	"l1v_dir_stall_post_pipeline_buffer",
	"l1v_dir_stall_victim_locked",
	"l1v_dir_stall_mshr_full",
	"l1v_dir_stall_bottom_blocked",
	"l1v_dir_stall_bank_buffer_full",
}

var criticalPathComponentColumns = []string{
	"total_request",
	"address_translation",
	"address_translation_tlb",
	"at_to_l1v_top",
	"data_access_total",
	"l1_cache_handle",
	"local_l2_cache",
	"local_dram",
	"network",
	"remote_l2_cache",
	"remote_dram",
	"other_data_access",
	"data_access_over_accounted",
}

type criticalPathBreakdown struct {
	totalRequestNS          uint64
	addressTranslationNS    uint64
	addressTranslationTLBNS uint64
	atToL1VTopNS            uint64
	dataAccessTotalNS       uint64
	l1CacheHandleNS         uint64
	localL2CacheNS          uint64
	localDRAMNS             uint64
	networkNS               uint64
	remoteL2CacheNS         uint64
	remoteDRAMNS            uint64
	otherDataAccessNS       uint64
	dataAccessOverAccounted uint64
	dataAccessAccountedNS   uint64
}

type criticalPathComponentAggregate struct {
	paths       uint64
	remotePaths uint64
	sumNS       uint64
	minNS       uint64
	maxNS       uint64
	valuesNS    []uint64
}

type criticalPathAggregate struct {
	records        uint64
	remoteRecords  uint64
	totalRequestNS uint64
	components     map[string]*criticalPathComponentAggregate
}

func newCriticalPathAggregate() criticalPathAggregate {
	return criticalPathAggregate{
		components: make(map[string]*criticalPathComponentAggregate),
	}
}

type l1vPathHop struct {
	parentReqID   string
	parentCount   int
	segment       string
	fromComponent string
	fromPort      string
	toComponent   string
	toPort        string
	requestMsgID  string
	responseMsgID string
	startNS       uint64
	endNS         uint64
	notes         string
}

type l1vPathStageAggregate struct {
	paths   map[string]struct{}
	pathCnt uint64
	hops    uint64
	sumNS   uint64
	minNS   uint64
	maxNS   uint64
	totalNS uint64
}

type specificDataKey struct {
	granularity string
	dataAddr    uint64
	pageAddr    uint64
	pid         uint64
	accessType  string
}

type specificDataAggregate struct {
	key specificDataKey

	paths          uint64
	remotePaths    uint64
	localPaths     uint64
	bytes          uint64
	parentRequests uint64

	latenciesNS     []uint64
	totalLatencyNS  uint64
	crossReqNS      uint64
	crossReturnNS   uint64
	remoteServiceNS uint64

	hopsSum uint64
	hopsCnt uint64

	firstCompletionNS uint64
	lastCompletionNS  uint64

	requesterCounts map[int]uint64
	ownerCounts     map[int]uint64
	sourceCounts    map[string]uint64
	routeCounts     map[string]uint64
}

// RecordMemoryPathL1VParentAccess records the time from a translated parent
// memory request leaving the address-translation side until it reaches L1V.
func RecordMemoryPathL1VParentAccess(
	cacheName string,
	pathID string,
	parentReqID string,
	info interface{},
	address uint64,
	bytes uint64,
	pid uint64,
	op string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	if accessInfo, ok := GetL2AccessInfo(info); ok {
		globalMemoryPathStats.applyAccessInfoLocked(rec, accessInfo)
	}
	rec.hasVAddr = true
	rec.vaddr = address
	rec.pid = pid
	rec.bytes = bytes
	rec.accessType = normalizeOp(op)
	if rec.parentCount == 0 {
		rec.parentCount = 1
	}

	startNS := timeToNS(send)
	endNS := timeToNS(receive)
	if endNS >= startNS && endNS > 0 {
		latency := endNS - startNS
		rec.atToL1VSumNS += latency
		rec.atToL1VCnt++
		if rec.atToL1VMinNS == 0 || latency < rec.atToL1VMinNS {
			rec.atToL1VMinNS = latency
		}
		if latency > rec.atToL1VMaxNS {
			rec.atToL1VMaxNS = latency
		}
	}

	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = cacheName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		parentReqID:   parentReqID,
		segment:       "at_to_l1v_top",
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		requestMsgID:  parentReqID,
		startNS:       startNS,
		endNS:         endNS,
	})
}

func RecordMemoryPathL1VCoalesce(
	cacheName string,
	pathID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
	parentCount int,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	rec.parentCount = parentCount
	rec.l1vCoalesceStartNS = timeToNS(start)
	rec.l1vCoalesceEmitNS = timeToNS(end)
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		parentCount:   parentCount,
		segment:       "l1v_coalesce_wait",
		fromComponent: cacheName,
		toComponent:   cacheName,
		requestMsgID:  pathID,
		startNS:       rec.l1vCoalesceStartNS,
		endNS:         rec.l1vCoalesceEmitNS,
	})
}

func RecordMemoryPathL1VBatchWait(
	cacheName string,
	pathID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       "l1v_batch_wait",
		fromComponent: cacheName,
		toComponent:   cacheName,
		requestMsgID:  pathID,
		startNS:       timeToNS(start),
		endNS:         timeToNS(end),
	})
}

func RecordMemoryPathL1VDirStart(cacheName, pathID string, now sim.VTimeInSec) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	rec.l1vDirStartNS = timeToNS(now)
	_ = cacheName
}

func RecordMemoryPathL1VDirStall(
	cacheName string,
	pathID string,
	segment string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	startNS := timeToNS(start)
	endNS := timeToNS(end)
	if endNS <= startNS {
		return
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       segment,
		fromComponent: cacheName,
		toComponent:   cacheName,
		requestMsgID:  pathID,
		startNS:       startNS,
		endNS:         endNS,
	})
}

func RecordMemoryPathL2TopReceive(
	cacheName string,
	reqID string,
	info interface{},
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, reqID)
	if rec == nil {
		return
	}
	accessInfo, _ := GetL2AccessInfo(info)
	segment := "l1v_bottom_send_to_local_l2"
	route := "local"
	if accessInfo.IsRemote {
		segment = "remote_rdma_to_remote_l2"
		route = "remote"
	}
	rec.route = route
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = cacheName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       segment,
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		requestMsgID:  reqID,
		startNS:       timeToNS(send),
		endNS:         timeToNS(receive),
	})
}

func RecordMemoryPathL2DirStart(cacheName string, reqID string, info interface{}, now sim.VTimeInSec) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, reqID)
	if rec == nil {
		return
	}
	nowNS := timeToNS(now)
	if rec.l2CacheStartNS > 0 && nowNS >= rec.l2CacheStartNS {
		globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
			segment:       "l2_top_to_dir",
			fromComponent: cacheName,
			toComponent:   cacheName,
			requestMsgID:  reqID,
			startNS:       rec.l2CacheStartNS,
			endNS:         nowNS,
		})
	}
	rec.l2DirStartNS = nowNS
	_ = cacheName
}

func RecordMemoryPathL2WriteBufferSend(
	cacheName string,
	info interface{},
	requestID string,
	send sim.VTimeInSec,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	sendNS := timeToNS(send)
	if rec.l2DirResultNS > 0 && sendNS >= rec.l2DirResultNS {
		globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
			segment:       "l2_writebuffer_wait",
			fromComponent: cacheName,
			toComponent:   cacheName,
			requestMsgID:  requestID,
			startNS:       rec.l2DirResultNS,
			endNS:         sendNS,
		})
	}
	rec.l2WriteBufferNS = sendNS
}

func RecordMemoryPathDRAMRequestReceive(
	dramName string,
	info interface{},
	requestID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	rec.dramReceiveNS = timeToNS(receive)
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = dramName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       "l2_bottom_send_to_dram",
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		requestMsgID:  requestID,
		startNS:       timeToNS(send),
		endNS:         rec.dramReceiveNS,
	})
}

func RecordMemoryPathDRAMResponse(
	dramName string,
	info interface{},
	requestID string,
	responseID string,
	now sim.VTimeInSec,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	nowNS := timeToNS(now)
	if rec.dramReceiveNS > 0 && nowNS >= rec.dramReceiveNS {
		globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
			segment:       "dram_queue_and_service",
			fromComponent: dramName,
			toComponent:   dramName,
			requestMsgID:  requestID,
			responseMsgID: responseID,
			startNS:       rec.dramReceiveNS,
			endNS:         nowNS,
		})
	}
}

func RecordMemoryPathL2DRAMResponse(
	cacheName string,
	info interface{},
	requestID string,
	responseID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	receiveNS := timeToNS(receive)
	rec.l2DramReturnNS = receiveNS
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = cacheName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       "dram_to_l2_response",
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		requestMsgID:  requestID,
		responseMsgID: responseID,
		startNS:       timeToNS(send),
		endNS:         receiveNS,
	})
}

func RecordMemoryPathRDMARequestFromL1(
	rdmaName string,
	info interface{},
	requestID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	rec.route = "remote"
	rec.isRemote = true
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = rdmaName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       "l1v_bottom_send_to_local_rdma",
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		requestMsgID:  requestID,
		startNS:       timeToNS(send),
		endNS:         timeToNS(receive),
	})
}

func RecordMemoryPathRDMALocalToRemoteRequest(
	rdmaName string,
	info interface{},
	requestID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	recordRDMAHop(
		"local_rdma_to_remote_rdma_request",
		rdmaName, info, requestID, "", send, receive, src, dst)
}

func RecordMemoryPathRDMAResponseFromL2(
	rdmaName string,
	info interface{},
	requestID string,
	responseID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	recordRDMAHop(
		"remote_l2_to_remote_rdma_response",
		rdmaName, info, requestID, responseID, send, receive, src, dst)
}

func RecordMemoryPathRDMARemoteToLocalResponse(
	rdmaName string,
	info interface{},
	requestID string,
	responseID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	recordRDMAHop(
		"remote_rdma_to_local_rdma_response",
		rdmaName, info, requestID, responseID, send, receive, src, dst)
}

func RecordMemoryPathRDMALocalRequestOutputWait(
	rdmaName string,
	info interface{},
	requestID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
) {
	recordRDMAInternalHop(
		"local_rdma_request_output_wait",
		rdmaName, info, requestID, "", start, end,
		"local RDMA waiting to send request to remote RDMA")
}

func RecordMemoryPathRDMARemoteRequestOutputWait(
	rdmaName string,
	info interface{},
	requestID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
) {
	recordRDMAInternalHop(
		"remote_rdma_request_output_wait",
		rdmaName, info, requestID, "", start, end,
		"remote RDMA waiting to send request to remote L2")
}

func RecordMemoryPathRDMARemoteResponseOutputWait(
	rdmaName string,
	info interface{},
	requestID string,
	responseID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
) {
	recordRDMAInternalHop(
		"remote_rdma_response_output_wait",
		rdmaName, info, requestID, responseID, start, end,
		"remote RDMA waiting to send response to local RDMA")
}

func RecordMemoryPathRDMALocalResponseOutputWait(
	rdmaName string,
	info interface{},
	requestID string,
	responseID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
) {
	recordRDMAInternalHop(
		"local_rdma_response_output_wait",
		rdmaName, info, requestID, responseID, start, end,
		"local RDMA waiting to send response to L1V")
}

// RegisterMemoryPathNetworkMessage links an RDMA-generated cross-GPU NoC
// message back to the original L1V cache-line transaction.
func RegisterMemoryPathNetworkMessage(
	info interface{},
	requestID string,
	networkMsgID string,
	direction string,
) {
	if networkMsgID == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	rec.route = "remote"
	rec.isRemote = true
	globalMemoryPathStats.networkMsgToOriginal[networkMsgID] = rec.originalReqID
	globalMemoryPathStats.networkMsgDirection[networkMsgID] =
		normalizeNetworkPathDirection(direction)
}

// RecordMemoryPathNetworkMessageStage records a message-level cross-GPU NoC
// stage, such as endpoint queueing or final delivery wait.
func RecordMemoryPathNetworkMessageStage(
	component string,
	stage string,
	msgID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
	notes string,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec, direction := globalMemoryPathStats.recordForNetworkMessageLocked(msgID)
	if rec == nil || stage == "" {
		return
	}
	startNS := timeToNS(start)
	endNS := timeToNS(end)
	if endNS < startNS {
		return
	}
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if fromComponent == "" {
		fromComponent = component
	}
	if toComponent == "" {
		toComponent = component
	}
	hop := l1vPathHop{
		segment:       direction + "_" + stage,
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		startNS:       startNS,
		endNS:         endNS,
		notes:         notes,
	}
	if direction == "cross_gpu_return" {
		hop.requestMsgID = rec.originalReqID
		hop.responseMsgID = msgID
	} else {
		hop.requestMsgID = msgID
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, hop)
}

// RecordMemoryPathNetworkFlitStage records a flit-level cross-GPU NoC stage.
// These rows expose where a cross-GPU request/return spends time inside the
// network. Per-request summaries sum these flit-hop latencies, so detailed
// plots should use them instead of the coarse RDMA-to-RDMA segment.
func RecordMemoryPathNetworkFlitStage(
	component string,
	stage string,
	msgID string,
	flitID string,
	seqID int,
	numFlits int,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	notes := fmt.Sprintf(
		"component=%s flit_id=%s seq=%d/%d",
		component, flitID, seqID+1, numFlits)
	RecordMemoryPathNetworkMessageStage(
		component, stage, msgID, start, end, src, dst, notes)
}

func RecordMemoryPathL1VBottomResponse(
	cacheName string,
	pathID string,
	responseID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	segment := "local_l2_to_l1v_response"
	if rec.route == "remote" || rec.isRemote {
		segment = "local_rdma_to_l1v_response"
	}
	rec.l1vBottomResponseNS = timeToNS(receive)
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = cacheName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       segment,
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		responseMsgID: responseID,
		startNS:       timeToNS(send),
		endNS:         rec.l1vBottomResponseNS,
	})
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       "l1v_bottom_response_parse",
		fromComponent: cacheName,
		toComponent:   cacheName,
		responseMsgID: responseID,
		startNS:       rec.l1vBottomResponseNS,
		endNS:         rec.l1vBottomResponseNS,
	})
}

func RecordMemoryPathL1VMSHRWakeup(cacheName string, pathID string, now sim.VTimeInSec) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordByOriginalLocked(pathID)
	if rec == nil {
		return
	}
	nowNS := timeToNS(now)
	if rec.l1vBottomResponseNS > 0 && nowNS >= rec.l1vBottomResponseNS {
		globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
			segment:       "l1v_mshr_wakeup",
			fromComponent: cacheName,
			toComponent:   cacheName,
			startNS:       rec.l1vBottomResponseNS,
			endNS:         nowNS,
		})
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       "l1v_fill_parent_done",
		fromComponent: cacheName,
		toComponent:   cacheName,
		startNS:       nowNS,
		endNS:         nowNS,
	})
}

func recordRDMAHop(
	segment string,
	rdmaName string,
	info interface{},
	requestID string,
	responseID string,
	send sim.VTimeInSec,
	receive sim.VTimeInSec,
	src sim.Port,
	dst sim.Port,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	rec.route = "remote"
	rec.isRemote = true
	fromComponent, fromPort := componentAndPort(src)
	toComponent, toPort := componentAndPort(dst)
	if toComponent == "" {
		toComponent = rdmaName
	}
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       segment,
		fromComponent: fromComponent,
		fromPort:      fromPort,
		toComponent:   toComponent,
		toPort:        toPort,
		requestMsgID:  requestID,
		responseMsgID: responseID,
		startNS:       timeToNS(send),
		endNS:         timeToNS(receive),
	})
}

func recordRDMAInternalHop(
	segment string,
	rdmaName string,
	info interface{},
	requestID string,
	responseID string,
	start sim.VTimeInSec,
	end sim.VTimeInSec,
	notes string,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}
	rec := globalMemoryPathStats.recordForPathInfoLocked(info, requestID)
	if rec == nil {
		return
	}
	rec.route = "remote"
	rec.isRemote = true
	globalMemoryPathStats.appendL1VPathHopLocked(rec, l1vPathHop{
		segment:       segment,
		fromComponent: rdmaName,
		toComponent:   rdmaName,
		requestMsgID:  requestID,
		responseMsgID: responseID,
		startNS:       timeToNS(start),
		endNS:         timeToNS(end),
		notes:         notes,
	})
}

func (s *memoryPathStats) noteCacheResultLocked(
	tier string,
	cacheName string,
	rec *memoryPathRecord,
	result string,
	nowNS uint64,
) {
	switch tier {
	case "l1v":
		rec.l1vDirResultNS = nowNS
		if rec.l1vDirStartNS > 0 && nowNS >= rec.l1vDirStartNS {
			elapsed := nowNS - rec.l1vDirStartNS
			stallNS := sumStages(rec, l1vDirStallStages)
			activeNS := uint64(0)
			if elapsed > stallNS {
				activeNS = elapsed - stallNS
			}
			s.appendL1VPathHopLocked(rec, l1vPathHop{
				segment:       "l1v_dir_lookup",
				fromComponent: cacheName,
				toComponent:   cacheName,
				startNS:       rec.l1vDirStartNS,
				endNS:         rec.l1vDirStartNS + activeNS,
				notes:         "active_only_excludes_dir_stalls",
			})
		}
		if result == "mshr-hit" || result == "miss" {
			rec.l1vMSHRStartNS = nowNS
		}
	case "l2":
		rec.l2DirResultNS = nowNS
		if rec.l2DirStartNS > 0 && nowNS >= rec.l2DirStartNS {
			s.appendL1VPathHopLocked(rec, l1vPathHop{
				segment:       "l2_dir_lookup",
				fromComponent: cacheName,
				toComponent:   cacheName,
				startNS:       rec.l2DirStartNS,
				endNS:         nowNS,
			})
		}
		if result == "mshr-hit" {
			rec.l2MSHRStartNS = nowNS
		}
	}
}

func (s *memoryPathStats) noteCacheCompleteLocked(
	tier string,
	cacheName string,
	rec *memoryPathRecord,
	nowNS uint64,
) {
	switch tier {
	case "l1v":
		if rec.l1vCacheResult == "hit" && rec.l1vDirResultNS > 0 && nowNS >= rec.l1vDirResultNS {
			s.appendL1VPathHopLocked(rec, l1vPathHop{
				segment:       "l1v_bank_hit",
				fromComponent: cacheName,
				toComponent:   cacheName,
				startNS:       rec.l1vDirResultNS,
				endNS:         nowNS,
			})
			s.appendL1VPathHopLocked(rec, l1vPathHop{
				segment:       "l1v_fill_parent_done",
				fromComponent: cacheName,
				toComponent:   cacheName,
				startNS:       nowNS,
				endNS:         nowNS,
			})
		}
	case "l2":
		switch rec.l2CacheResult {
		case "hit":
			if rec.l2DirResultNS > 0 && nowNS >= rec.l2DirResultNS {
				s.appendL1VPathHopLocked(rec, l1vPathHop{
					segment:       "l2_bank_hit",
					fromComponent: cacheName,
					toComponent:   cacheName,
					startNS:       rec.l2DirResultNS,
					endNS:         nowNS,
				})
			}
		case "mshr-hit":
			if rec.l2DirResultNS > 0 && nowNS >= rec.l2DirResultNS {
				s.appendL1VPathHopLocked(rec, l1vPathHop{
					segment:       "l2_mshr_wait",
					fromComponent: cacheName,
					toComponent:   cacheName,
					startNS:       rec.l2DirResultNS,
					endNS:         nowNS,
				})
			}
		case "miss":
			start := rec.l2DramReturnNS
			if start == 0 {
				start = rec.l2DirResultNS
			}
			if start > 0 && nowNS >= start {
				s.appendL1VPathHopLocked(rec, l1vPathHop{
					segment:       "l2_fill_and_response",
					fromComponent: cacheName,
					toComponent:   cacheName,
					startNS:       start,
					endNS:         nowNS,
				})
			}
		}
	}
}

func (s *memoryPathStats) recordForPathInfoLocked(info interface{}, fallbackReqID string) *memoryPathRecord {
	pathID := ""
	if accessInfo, ok := GetL2AccessInfo(info); ok {
		if accessInfo.PathID != "" {
			pathID = accessInfo.PathID
		}
		if pathID == "" && accessInfo.OriginalReqID != "" {
			pathID = accessInfo.OriginalReqID
		}
	}
	if pathID == "" {
		pathID = s.originalIDFromInfoOrReqLocked(info, fallbackReqID)
	}
	if pathID == "" {
		return nil
	}
	rec := s.recordByOriginalLocked(pathID)
	if accessInfo, ok := GetL2AccessInfo(info); ok {
		s.applyAccessInfoLocked(rec, accessInfo)
	}
	return rec
}

func (s *memoryPathStats) recordForNetworkMessageLocked(
	msgID string,
) (*memoryPathRecord, string) {
	if msgID == "" {
		return nil, ""
	}
	pathID := s.networkMsgToOriginal[msgID]
	if pathID == "" {
		return nil, ""
	}
	rec := s.recordByOriginalLocked(pathID)
	if rec == nil {
		return nil, ""
	}
	rec.route = "remote"
	rec.isRemote = true
	direction := s.networkMsgDirection[msgID]
	if direction == "" {
		direction = "cross_gpu_request"
	}
	return rec, direction
}

func (s *memoryPathStats) appendL1VPathHopLocked(rec *memoryPathRecord, hop l1vPathHop) {
	if rec == nil || hop.segment == "" {
		return
	}
	if hop.endNS < hop.startNS {
		return
	}
	if rec.l1vPathStageSumNS == nil {
		rec.l1vPathStageSumNS = make(map[string]uint64)
		rec.l1vPathStageCnt = make(map[string]uint64)
	}
	if hop.parentCount == 0 {
		hop.parentCount = rec.parentCount
	}
	rec.l1vPathHops = append(rec.l1vPathHops, hop)
	latency := hop.endNS - hop.startNS
	rec.l1vPathStageSumNS[hop.segment] += latency
	rec.l1vPathStageCnt[hop.segment]++
}

func (s *memoryPathStats) openL1VPathStreamLocked() error {
	hopsPath := s.prefix + "_l1v_path_hops_raw.csv.gz"
	if dir := filepath.Dir(hopsPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	hopsFile, err := os.Create(hopsPath)
	if err != nil {
		return err
	}
	s.l1vHopsFile = hopsFile
	s.l1vHopsGzip = gzip.NewWriter(hopsFile)
	s.l1vHopsCSV = csv.NewWriter(s.l1vHopsGzip)
	if err := s.l1vHopsCSV.Write(strings.Split(l1vPathHopsHeader, ",")); err != nil {
		return err
	}

	summaryPath := s.prefix + "_l1v_path_summary.csv"
	if dir := filepath.Dir(summaryPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	summaryFile, err := os.Create(summaryPath)
	if err != nil {
		return err
	}
	s.l1vSummaryFile = summaryFile
	s.l1vSummaryCSV = csv.NewWriter(summaryFile)
	return s.l1vSummaryCSV.Write(l1vPathSummaryHeader())
}

func (s *memoryPathStats) openCriticalPathStreamLocked() error {
	path := s.prefix + "_critical_path_breakdown.csv"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	s.cpFile = file
	s.cpCSV = csv.NewWriter(file)
	return s.cpCSV.Write(criticalPathBreakdownHeader())
}

func l1vPathSummaryHeader() []string {
	header := []string{
		"path_id", "sequence", "completion_time_ns", "access_type", "pid",
		"vaddr", "paddr", "page_paddr", "cacheline_addr", "bytes",
		"requester_gpm", "owner_gpm", "hops", "is_remote", "route",
		"final_source", "l1v_result", "l2_result", "parent_count",
		"total_l1v_path_latency_ns",
		"at_to_l1v_top_min_ns", "at_to_l1v_top_avg_ns", "at_to_l1v_top_max_ns",
	}
	for _, stage := range l1vPathStageColumns {
		header = append(header, stage+"_ns")
	}
	return header
}

func (s *memoryPathStats) streamL1VPathRecordLocked(rec *memoryPathRecord) {
	if rec == nil || s.streamErr != nil {
		return
	}
	if s.l1vHopsCSV == nil || s.l1vSummaryCSV == nil {
		s.streamErr = fmt.Errorf("l1v path stream is not open")
		return
	}
	for i, hop := range rec.l1vPathHops {
		if err := s.l1vHopsCSV.Write(s.l1vPathHopRow(rec, hop, i+1)); err != nil {
			s.streamErr = err
			return
		}
	}
	if err := s.l1vSummaryCSV.Write(s.l1vPathSummaryRow(rec)); err != nil {
		s.streamErr = err
		return
	}
	if err := s.writeCriticalPathBreakdownRecordLocked(rec); err != nil {
		s.streamErr = err
		return
	}
	s.aggregateL1VPathRecordLocked(rec)
	s.aggregateCriticalPathRecordLocked(rec)
	if s.written%4096 == 0 {
		s.l1vHopsCSV.Flush()
		if err := s.l1vHopsCSV.Error(); err != nil && s.streamErr == nil {
			s.streamErr = err
		}
		if s.l1vHopsGzip != nil {
			if err := s.l1vHopsGzip.Flush(); err != nil && s.streamErr == nil {
				s.streamErr = err
			}
		}
		s.l1vSummaryCSV.Flush()
		if err := s.l1vSummaryCSV.Error(); err != nil && s.streamErr == nil {
			s.streamErr = err
		}
		if s.cpCSV != nil {
			s.cpCSV.Flush()
			if err := s.cpCSV.Error(); err != nil && s.streamErr == nil {
				s.streamErr = err
			}
		}
	}
}

func (s *memoryPathStats) aggregateL1VPathRecordLocked(rec *memoryPathRecord) {
	seenStages := make(map[string]struct{})
	for _, hop := range rec.l1vPathHops {
		latency := hop.endNS - hop.startNS
		agg := s.l1vPathStageAggregates[hop.segment]
		if agg == nil {
			agg = &l1vPathStageAggregate{}
			s.l1vPathStageAggregates[hop.segment] = agg
		}
		agg.hops++
		agg.sumNS += latency
		agg.totalNS += latency
		if agg.minNS == 0 || latency < agg.minNS {
			agg.minNS = latency
		}
		if latency > agg.maxNS {
			agg.maxNS = latency
		}
		if _, ok := seenStages[hop.segment]; !ok {
			agg.pathCnt++
			seenStages[hop.segment] = struct{}{}
		}
	}
}

func (s *memoryPathStats) dumpL1VPathTraceLocked() error {
	if s.streaming {
		if err := s.dumpL1VPathStageSummaryLocked(); err != nil {
			return err
		}
		return s.dumpSpecificDataSummariesLocked()
	}
	if err := s.dumpL1VPathHopsLocked(); err != nil {
		return err
	}
	if err := s.dumpL1VPathSummaryLocked(); err != nil {
		return err
	}
	if err := s.dumpL1VPathStageSummaryLocked(); err != nil {
		return err
	}
	return s.dumpSpecificDataSummariesLocked()
}

func (s *memoryPathStats) selectedL1VPathRecordsLocked() []*memoryPathRecord {
	records := make([]*memoryPathRecord, 0, len(s.records))
	for _, rec := range s.records {
		if rec == nil || !rec.counted || rec.sequence == 0 {
			continue
		}
		if rec.traceSeq == 0 {
			continue
		}
		if !s.inRawWindow(rec.traceSeq) {
			continue
		}
		records = append(records, rec)
	}
	sort.SliceStable(records, func(i, j int) bool {
		return s.outputSequence(records[i]) < s.outputSequence(records[j])
	})
	return records
}

func (s *memoryPathStats) dumpL1VPathHopsLocked() error {
	path := s.prefix + "_l1v_path_hops_raw.csv.gz"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	csvWriter := csv.NewWriter(gzipWriter)
	defer csvWriter.Flush()

	if err := csvWriter.Write(strings.Split(l1vPathHopsHeader, ",")); err != nil {
		return err
	}
	for _, rec := range s.selectedL1VPathRecordsLocked() {
		for i, hop := range rec.l1vPathHops {
			row := s.l1vPathHopRow(rec, hop, i+1)
			if err := csvWriter.Write(row); err != nil {
				return err
			}
		}
	}
	return csvWriter.Error()
}

func (s *memoryPathStats) dumpL1VPathSummaryLocked() error {
	path := s.prefix + "_l1v_path_summary.csv"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)
	defer csvWriter.Flush()

	if err := csvWriter.Write(l1vPathSummaryHeader()); err != nil {
		return err
	}
	for _, rec := range s.selectedL1VPathRecordsLocked() {
		row := s.l1vPathSummaryRow(rec)
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	return csvWriter.Error()
}

func (s *memoryPathStats) dumpL1VPathStageSummaryLocked() error {
	path := s.prefix + "_l1v_path_stage_summary.csv"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)
	defer csvWriter.Flush()

	if err := csvWriter.Write([]string{
		"stage", "paths", "hops", "avg_latency_ns", "min_latency_ns",
		"max_latency_ns", "total_latency_ns",
	}); err != nil {
		return err
	}
	aggregates := s.l1vPathStageAggregates
	if !s.streaming {
		aggregates = make(map[string]*l1vPathStageAggregate)
		for _, rec := range s.selectedL1VPathRecordsLocked() {
			for _, hop := range rec.l1vPathHops {
				latency := hop.endNS - hop.startNS
				agg := aggregates[hop.segment]
				if agg == nil {
					agg = &l1vPathStageAggregate{paths: make(map[string]struct{})}
					aggregates[hop.segment] = agg
				}
				agg.paths[rec.originalReqID] = struct{}{}
				agg.hops++
				agg.sumNS += latency
				agg.totalNS += latency
				if agg.minNS == 0 || latency < agg.minNS {
					agg.minNS = latency
				}
				if latency > agg.maxNS {
					agg.maxNS = latency
				}
			}
		}
	}

	stages := append([]string{}, l1vPathStageColumns...)
	for stage := range aggregates {
		if !stringInSlice(stage, stages) {
			stages = append(stages, stage)
		}
	}
	for _, stage := range stages {
		agg := aggregates[stage]
		if agg == nil || agg.hops == 0 {
			continue
		}
		paths := agg.pathCnt
		if agg.paths != nil {
			paths = uint64(len(agg.paths))
		}
		row := []string{
			stage,
			strconv.FormatUint(paths, 10),
			strconv.FormatUint(agg.hops, 10),
			strconv.FormatUint(agg.sumNS/agg.hops, 10),
			strconv.FormatUint(agg.minNS, 10),
			strconv.FormatUint(agg.maxNS, 10),
			strconv.FormatUint(agg.totalNS, 10),
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	return csvWriter.Error()
}

func (s *memoryPathStats) dumpCriticalPathTraceLocked() error {
	if s.streaming {
		return s.dumpCriticalPathStageSummaryLocked(s.criticalPathAggregate)
	}
	records := s.selectedL1VPathRecordsLocked()
	if err := s.dumpCriticalPathBreakdownLocked(records); err != nil {
		return err
	}
	agg := newCriticalPathAggregate()
	for _, rec := range records {
		agg.addRecord(rec)
	}
	return s.dumpCriticalPathStageSummaryLocked(agg)
}

func criticalPathBreakdownHeader() []string {
	return []string{
		"path_id", "sequence", "completion_time_ns", "access_type", "pid",
		"vaddr", "paddr", "page_paddr", "cacheline_addr", "bytes",
		"requester_gpm", "owner_gpm", "hops", "is_remote", "route",
		"final_source", "l1v_result", "l2_result", "parent_count",
		"total_request_ns", "address_translation_ns",
		"address_translation_tlb_ns", "at_to_l1v_top_ns",
		"data_access_total_ns", "data_access_accounted_ns",
		"l1_cache_handle_ns", "local_l2_cache_ns", "local_dram_ns",
		"network_ns", "remote_l2_cache_ns", "remote_dram_ns",
		"other_data_access_ns", "data_access_over_accounted_ns",
		"l1v_tlb_latency_ns", "l2tlb_latency_ns",
	}
}

func (s *memoryPathStats) dumpCriticalPathBreakdownLocked(
	records []*memoryPathRecord,
) error {
	path := s.prefix + "_critical_path_breakdown.csv"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)
	defer csvWriter.Flush()

	if err := csvWriter.Write(criticalPathBreakdownHeader()); err != nil {
		return err
	}
	for _, rec := range records {
		row := s.criticalPathBreakdownRow(rec)
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	return csvWriter.Error()
}

func (s *memoryPathStats) writeCriticalPathBreakdownRecordLocked(
	rec *memoryPathRecord,
) error {
	if s.cpCSV == nil {
		return nil
	}
	return s.cpCSV.Write(s.criticalPathBreakdownRow(rec))
}

func (s *memoryPathStats) criticalPathBreakdownRow(
	rec *memoryPathRecord,
) []string {
	breakdown := criticalPathBreakdownForRecord(rec)
	return []string{
		rec.originalReqID,
		strconv.FormatUint(s.outputSequence(rec), 10),
		strconv.FormatUint(rec.completionTimeNS, 10),
		rec.accessType,
		strconv.FormatUint(rec.pid, 10),
		strconv.FormatUint(rec.vaddr, 10),
		strconv.FormatUint(rec.paddr, 10),
		strconv.FormatUint(pageBaseForLog(rec.paddr, s.log2Page), 10),
		strconv.FormatUint(cachelineAddr(rec), 10),
		strconv.FormatUint(rec.bytes, 10),
		strconv.Itoa(rec.requesterGPM),
		strconv.Itoa(rec.providerGPM),
		strconv.Itoa(rec.hops),
		strconv.FormatBool(rec.isRemote),
		routeForRecord(rec),
		finalSourceForRecord(rec),
		rec.l1vCacheResult,
		rec.l2CacheResult,
		strconv.Itoa(rec.parentCount),
		strconv.FormatUint(breakdown.totalRequestNS, 10),
		strconv.FormatUint(breakdown.addressTranslationNS, 10),
		strconv.FormatUint(breakdown.addressTranslationTLBNS, 10),
		strconv.FormatUint(breakdown.atToL1VTopNS, 10),
		strconv.FormatUint(breakdown.dataAccessTotalNS, 10),
		strconv.FormatUint(breakdown.dataAccessAccountedNS, 10),
		strconv.FormatUint(breakdown.l1CacheHandleNS, 10),
		strconv.FormatUint(breakdown.localL2CacheNS, 10),
		strconv.FormatUint(breakdown.localDRAMNS, 10),
		strconv.FormatUint(breakdown.networkNS, 10),
		strconv.FormatUint(breakdown.remoteL2CacheNS, 10),
		strconv.FormatUint(breakdown.remoteDRAMNS, 10),
		strconv.FormatUint(breakdown.otherDataAccessNS, 10),
		strconv.FormatUint(breakdown.dataAccessOverAccounted, 10),
		strconv.FormatUint(rec.l1vTLBLatencyNS, 10),
		strconv.FormatUint(rec.l2TLBLatencyNS, 10),
	}
}

func (s *memoryPathStats) aggregateCriticalPathRecordLocked(
	rec *memoryPathRecord,
) {
	s.criticalPathAggregate.addRecord(rec)
}

func (a *criticalPathAggregate) addRecord(rec *memoryPathRecord) {
	if rec == nil {
		return
	}
	breakdown := criticalPathBreakdownForRecord(rec)
	a.records++
	if rec.isRemote {
		a.remoteRecords++
	}
	a.totalRequestNS += breakdown.totalRequestNS
	values := criticalPathComponentValues(breakdown)
	for _, component := range criticalPathComponentColumns {
		value := values[component]
		if value == 0 && component != "total_request" {
			continue
		}
		agg := a.components[component]
		if agg == nil {
			agg = &criticalPathComponentAggregate{}
			a.components[component] = agg
		}
		agg.paths++
		if rec.isRemote {
			agg.remotePaths++
		}
		agg.sumNS += value
		if agg.minNS == 0 || value < agg.minNS {
			agg.minNS = value
		}
		if value > agg.maxNS {
			agg.maxNS = value
		}
		agg.valuesNS = append(agg.valuesNS, value)
	}
}

func (s *memoryPathStats) dumpCriticalPathStageSummaryLocked(
	agg criticalPathAggregate,
) error {
	path := s.prefix + "_critical_path_stage_summary.csv"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)
	defer csvWriter.Flush()

	if err := csvWriter.Write([]string{
		"component", "paths", "remote_paths", "avg_latency_ns",
		"p50_latency_ns", "p90_latency_ns", "p99_latency_ns",
		"min_latency_ns", "max_latency_ns", "total_latency_ns",
		"avg_fraction_of_total_request",
		"total_fraction_of_total_request",
	}); err != nil {
		return err
	}

	components := append([]string{}, criticalPathComponentColumns...)
	for component := range agg.components {
		if !stringInSlice(component, components) {
			components = append(components, component)
		}
	}
	for _, component := range components {
		componentAgg := agg.components[component]
		if componentAgg == nil || componentAgg.paths == 0 {
			continue
		}
		sort.Slice(componentAgg.valuesNS, func(i, j int) bool {
			return componentAgg.valuesNS[i] < componentAgg.valuesNS[j]
		})
		avgFraction := 0.0
		if agg.records > 0 && componentAgg.paths > 0 && agg.totalRequestNS > 0 {
			avgComponent := float64(componentAgg.sumNS) /
				float64(componentAgg.paths)
			avgRequest := float64(agg.totalRequestNS) / float64(agg.records)
			if avgRequest > 0 {
				avgFraction = avgComponent / avgRequest
			}
		}
		totalFraction := 0.0
		if agg.totalRequestNS > 0 {
			totalFraction = float64(componentAgg.sumNS) /
				float64(agg.totalRequestNS)
		}
		row := []string{
			component,
			strconv.FormatUint(componentAgg.paths, 10),
			strconv.FormatUint(componentAgg.remotePaths, 10),
			strconv.FormatUint(avg(componentAgg.sumNS, componentAgg.paths), 10),
			strconv.FormatUint(percentileUint64(componentAgg.valuesNS, 0.50), 10),
			strconv.FormatUint(percentileUint64(componentAgg.valuesNS, 0.90), 10),
			strconv.FormatUint(percentileUint64(componentAgg.valuesNS, 0.99), 10),
			strconv.FormatUint(componentAgg.minNS, 10),
			strconv.FormatUint(componentAgg.maxNS, 10),
			strconv.FormatUint(componentAgg.sumNS, 10),
			fmt.Sprintf("%.6f", avgFraction),
			fmt.Sprintf("%.6f", totalFraction),
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	return csvWriter.Error()
}

func (s *memoryPathStats) dumpSpecificDataSummariesLocked() error {
	records := s.selectedL1VPathRecordsLocked()
	if err := s.dumpSpecificDataSummaryLocked(
		records,
		"page",
		s.prefix+"_specific_page_summary.csv",
	); err != nil {
		return err
	}
	return s.dumpSpecificDataSummaryLocked(
		records,
		"cacheline",
		s.prefix+"_specific_cacheline_summary.csv",
	)
}

func (s *memoryPathStats) dumpSpecificDataSummaryLocked(
	records []*memoryPathRecord,
	granularity string,
	path string,
) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	csvWriter := csv.NewWriter(file)
	defer csvWriter.Flush()

	header := []string{
		"granularity", "data_addr", "page_paddr", "pid", "access_type",
		"paths", "remote_paths", "remote_ratio", "local_paths",
		"bytes", "parent_requests", "avg_parent_count",
		"avg_total_l1v_path_latency_ns", "p50_total_l1v_path_latency_ns",
		"p90_total_l1v_path_latency_ns", "p99_total_l1v_path_latency_ns",
		"avg_cross_gpu_request_ns", "avg_cross_gpu_return_ns",
		"avg_remote_l2_dram_service_ns",
		"avg_hops", "requester_gpu_count", "owner_gpu_count",
		"requester_gpus", "owner_gpus", "top_requester_gpm",
		"top_requester_paths", "top_owner_gpm", "top_owner_paths",
		"top_source", "top_source_paths", "top_route", "top_route_paths",
		"first_completion_ns", "last_completion_ns",
	}
	if err := csvWriter.Write(header); err != nil {
		return err
	}

	aggregates := make(map[specificDataKey]*specificDataAggregate)
	for _, rec := range records {
		key, ok := s.specificDataKeyForRecord(rec, granularity)
		if !ok {
			continue
		}
		agg := aggregates[key]
		if agg == nil {
			agg = &specificDataAggregate{
				key:             key,
				requesterCounts: make(map[int]uint64),
				ownerCounts:     make(map[int]uint64),
				sourceCounts:    make(map[string]uint64),
				routeCounts:     make(map[string]uint64),
			}
			aggregates[key] = agg
		}
		agg.addRecord(rec)
	}

	rows := make([]*specificDataAggregate, 0, len(aggregates))
	for _, agg := range aggregates {
		rows = append(rows, agg)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].remotePaths != rows[j].remotePaths {
			return rows[i].remotePaths > rows[j].remotePaths
		}
		if rows[i].paths != rows[j].paths {
			return rows[i].paths > rows[j].paths
		}
		return rows[i].key.dataAddr < rows[j].key.dataAddr
	})

	for _, agg := range rows {
		if err := csvWriter.Write(agg.csvRow()); err != nil {
			return err
		}
	}
	return csvWriter.Error()
}

func (s *memoryPathStats) specificDataKeyForRecord(
	rec *memoryPathRecord,
	granularity string,
) (specificDataKey, bool) {
	if rec == nil {
		return specificDataKey{}, false
	}
	pageAddr := pageBaseForLog(rec.paddr, s.log2Page)
	var dataAddr uint64
	switch granularity {
	case "page":
		dataAddr = pageAddr
	case "cacheline":
		dataAddr = cachelineAddr(rec)
	default:
		return specificDataKey{}, false
	}
	return specificDataKey{
		granularity: granularity,
		dataAddr:    dataAddr,
		pageAddr:    pageAddr,
		pid:         rec.pid,
		accessType:  rec.accessType,
	}, true
}

func (a *specificDataAggregate) addRecord(rec *memoryPathRecord) {
	a.paths++
	if rec.isRemote {
		a.remotePaths++
	} else {
		a.localPaths++
	}
	a.bytes += rec.bytes
	if rec.parentCount > 0 {
		a.parentRequests += uint64(rec.parentCount)
	}
	a.totalLatencyNS += rec.l1vCacheLatencyNS
	a.latenciesNS = append(a.latenciesNS, rec.l1vCacheLatencyNS)
	a.crossReqNS += l1vPathCrossGPURequestNS(rec)
	a.crossReturnNS += l1vPathCrossGPUReturnNS(rec)
	a.remoteServiceNS += l1vPathRemoteServiceNS(rec)
	if rec.hops >= 0 {
		a.hopsSum += uint64(rec.hops)
		a.hopsCnt++
	}
	if a.firstCompletionNS == 0 || rec.completionTimeNS < a.firstCompletionNS {
		a.firstCompletionNS = rec.completionTimeNS
	}
	if rec.completionTimeNS > a.lastCompletionNS {
		a.lastCompletionNS = rec.completionTimeNS
	}
	if rec.requesterGPM >= 0 {
		a.requesterCounts[rec.requesterGPM]++
	}
	if rec.providerGPM >= 0 {
		a.ownerCounts[rec.providerGPM]++
	}
	if source := finalSourceForRecord(rec); source != "" {
		a.sourceCounts[source]++
	}
	if route := routeForRecord(rec); route != "" {
		a.routeCounts[route]++
	}
}

func (a *specificDataAggregate) csvRow() []string {
	sort.Slice(a.latenciesNS, func(i, j int) bool {
		return a.latenciesNS[i] < a.latenciesNS[j]
	})
	topRequester, topRequesterCount := topIntCount(a.requesterCounts)
	topOwner, topOwnerCount := topIntCount(a.ownerCounts)
	topSource, topSourceCount := topStringCount(a.sourceCounts)
	topRoute, topRouteCount := topStringCount(a.routeCounts)
	return []string{
		a.key.granularity,
		strconv.FormatUint(a.key.dataAddr, 10),
		strconv.FormatUint(a.key.pageAddr, 10),
		strconv.FormatUint(a.key.pid, 10),
		a.key.accessType,
		strconv.FormatUint(a.paths, 10),
		strconv.FormatUint(a.remotePaths, 10),
		fmt.Sprintf("%.6f", ratio(a.remotePaths, a.paths)),
		strconv.FormatUint(a.localPaths, 10),
		strconv.FormatUint(a.bytes, 10),
		strconv.FormatUint(a.parentRequests, 10),
		fmt.Sprintf("%.3f", floatAvg(a.parentRequests, a.paths)),
		strconv.FormatUint(avg(a.totalLatencyNS, a.paths), 10),
		strconv.FormatUint(percentileUint64(a.latenciesNS, 0.50), 10),
		strconv.FormatUint(percentileUint64(a.latenciesNS, 0.90), 10),
		strconv.FormatUint(percentileUint64(a.latenciesNS, 0.99), 10),
		strconv.FormatUint(avg(a.crossReqNS, a.paths), 10),
		strconv.FormatUint(avg(a.crossReturnNS, a.paths), 10),
		strconv.FormatUint(avg(a.remoteServiceNS, a.paths), 10),
		fmt.Sprintf("%.3f", floatAvg(a.hopsSum, a.hopsCnt)),
		strconv.Itoa(len(a.requesterCounts)),
		strconv.Itoa(len(a.ownerCounts)),
		intCountList(a.requesterCounts),
		intCountList(a.ownerCounts),
		strconv.Itoa(topRequester),
		strconv.FormatUint(topRequesterCount, 10),
		strconv.Itoa(topOwner),
		strconv.FormatUint(topOwnerCount, 10),
		topSource,
		strconv.FormatUint(topSourceCount, 10),
		topRoute,
		strconv.FormatUint(topRouteCount, 10),
		strconv.FormatUint(a.firstCompletionNS, 10),
		strconv.FormatUint(a.lastCompletionNS, 10),
	}
}

func l1vPathCrossGPURequestNS(rec *memoryPathRecord) uint64 {
	fine := sumStagePrefix(rec, "cross_gpu_request_")
	if fine > 0 {
		return fine
	}
	return rec.l1vPathStageSumNS["local_rdma_to_remote_rdma_request"]
}

func l1vPathCrossGPUReturnNS(rec *memoryPathRecord) uint64 {
	fine := sumStagePrefix(rec, "cross_gpu_return_")
	if fine > 0 {
		return fine
	}
	return rec.l1vPathStageSumNS["remote_rdma_to_local_rdma_response"]
}

func l1vPathRemoteServiceNS(rec *memoryPathRecord) uint64 {
	if rec == nil || !rec.isRemote {
		return 0
	}
	var sum uint64
	for _, stage := range []string{
		"remote_rdma_to_remote_l2",
		"l2_top_to_dir",
		"l2_dir_lookup",
		"l2_bank_hit",
		"l2_mshr_wait",
		"l2_writebuffer_wait",
		"l2_bottom_send_to_dram",
		"dram_queue_and_service",
		"dram_to_l2_response",
		"l2_fill_and_response",
		"remote_l2_to_remote_rdma_response",
	} {
		sum += rec.l1vPathStageSumNS[stage]
	}
	return sum
}

func criticalPathBreakdownForRecord(rec *memoryPathRecord) criticalPathBreakdown {
	if rec == nil {
		return criticalPathBreakdown{}
	}

	atToL1V := rec.l1vPathStageSumNS["at_to_l1v_top"]
	translationTLB := rec.l1vTLBLatencyNS
	if translationTLB == 0 || rec.l2TLBLatencyNS > translationTLB {
		translationTLB = rec.l2TLBLatencyNS
	}
	addressTranslation := translationTLB + atToL1V

	l1CacheHandle := sumStages(rec, []string{
		"l1v_coalesce_wait",
		"l1v_batch_wait",
		"l1v_dir_lookup",
		"l1v_dir_stall_post_pipeline_buffer",
		"l1v_dir_stall_victim_locked",
		"l1v_dir_stall_mshr_full",
		"l1v_dir_stall_bottom_blocked",
		"l1v_dir_stall_bank_buffer_full",
		"l1v_bank_hit",
		"l1v_bottom_response_parse",
		"l1v_mshr_wakeup",
		"l1v_fill_parent_done",
	})

	l2Cache := sumStages(rec, []string{
		"l2_top_to_dir",
		"l2_dir_lookup",
		"l2_bank_hit",
		"l2_mshr_wait",
		"l2_writebuffer_wait",
		"l2_fill_and_response",
	})
	dram := sumStages(rec, []string{
		"l2_bottom_send_to_dram",
		"dram_queue_and_service",
		"dram_to_l2_response",
	})

	var localL2, localDRAM, remoteL2, remoteDRAM, network uint64
	if rec.isRemote {
		remoteL2 = l2Cache
		remoteDRAM = dram
		network = sumStages(rec, []string{
			"l1v_bottom_send_to_local_rdma",
			"local_rdma_request_output_wait",
			"remote_rdma_request_output_wait",
			"remote_rdma_to_remote_l2",
			"remote_l2_to_remote_rdma_response",
			"remote_rdma_response_output_wait",
			"local_rdma_response_output_wait",
			"local_rdma_to_l1v_response",
		})
		network += criticalPathCrossGPURequestNS(rec)
		network += criticalPathCrossGPUReturnNS(rec)
	} else {
		l1CacheHandle += sumStages(rec, []string{
			"l1v_bottom_send_to_local_l2",
		})
		localL2 = l2Cache
		localDRAM = dram
	}

	dataAccessTotal := rec.l1vCacheLatencyNS
	accounted := l1CacheHandle + localL2 + localDRAM +
		network + remoteL2 + remoteDRAM
	var other, over uint64
	if dataAccessTotal > accounted {
		other = dataAccessTotal - accounted
	} else if accounted > dataAccessTotal {
		over = accounted - dataAccessTotal
	}

	return criticalPathBreakdown{
		totalRequestNS:          addressTranslation + dataAccessTotal,
		addressTranslationNS:    addressTranslation,
		addressTranslationTLBNS: translationTLB,
		atToL1VTopNS:            atToL1V,
		dataAccessTotalNS:       dataAccessTotal,
		l1CacheHandleNS:         l1CacheHandle,
		localL2CacheNS:          localL2,
		localDRAMNS:             localDRAM,
		networkNS:               network,
		remoteL2CacheNS:         remoteL2,
		remoteDRAMNS:            remoteDRAM,
		otherDataAccessNS:       other,
		dataAccessOverAccounted: over,
		dataAccessAccountedNS:   accounted,
	}
}

func sumStages(rec *memoryPathRecord, stages []string) uint64 {
	if rec == nil {
		return 0
	}
	var sum uint64
	for _, stage := range stages {
		sum += rec.l1vPathStageSumNS[stage]
	}
	return sum
}

func criticalPathCrossGPURequestNS(rec *memoryPathRecord) uint64 {
	coarse := rec.l1vPathStageSumNS["local_rdma_to_remote_rdma_request"]
	if coarse > 0 {
		return coarse
	}
	return sumStagePrefix(rec, "cross_gpu_request_")
}

func criticalPathCrossGPUReturnNS(rec *memoryPathRecord) uint64 {
	coarse := rec.l1vPathStageSumNS["remote_rdma_to_local_rdma_response"]
	if coarse > 0 {
		return coarse
	}
	return sumStagePrefix(rec, "cross_gpu_return_")
}

func criticalPathComponentValues(b criticalPathBreakdown) map[string]uint64 {
	return map[string]uint64{
		"total_request":              b.totalRequestNS,
		"address_translation":        b.addressTranslationNS,
		"address_translation_tlb":    b.addressTranslationTLBNS,
		"at_to_l1v_top":              b.atToL1VTopNS,
		"data_access_total":          b.dataAccessTotalNS,
		"l1_cache_handle":            b.l1CacheHandleNS,
		"local_l2_cache":             b.localL2CacheNS,
		"local_dram":                 b.localDRAMNS,
		"network":                    b.networkNS,
		"remote_l2_cache":            b.remoteL2CacheNS,
		"remote_dram":                b.remoteDRAMNS,
		"other_data_access":          b.otherDataAccessNS,
		"data_access_over_accounted": b.dataAccessOverAccounted,
	}
}

func sumStagePrefix(rec *memoryPathRecord, prefix string) uint64 {
	var sum uint64
	for stage, value := range rec.l1vPathStageSumNS {
		if strings.HasPrefix(stage, prefix) {
			sum += value
		}
	}
	return sum
}

func (s *memoryPathStats) l1vPathHopRow(rec *memoryPathRecord, hop l1vPathHop, hopSequence int) []string {
	return []string{
		rec.originalReqID,
		strconv.Itoa(hopSequence),
		hop.parentReqID,
		strconv.Itoa(rec.parentCount),
		rec.accessType,
		strconv.FormatUint(rec.pid, 10),
		strconv.FormatUint(rec.vaddr, 10),
		strconv.FormatUint(rec.paddr, 10),
		strconv.FormatUint(pageBaseForLog(rec.paddr, s.log2Page), 10),
		strconv.FormatUint(cachelineAddr(rec), 10),
		strconv.FormatUint(rec.bytes, 10),
		strconv.Itoa(rec.requesterGPM),
		strconv.Itoa(rec.providerGPM),
		strconv.FormatBool(rec.isRemote),
		routeForRecord(rec),
		finalSourceForRecord(rec),
		hop.segment,
		hop.fromComponent,
		hop.fromPort,
		hop.toComponent,
		hop.toPort,
		hop.requestMsgID,
		hop.responseMsgID,
		strconv.FormatUint(hop.startNS, 10),
		strconv.FormatUint(hop.endNS, 10),
		strconv.FormatUint(hop.endNS-hop.startNS, 10),
		rec.l1vCacheResult,
		rec.l2CacheResult,
		hop.notes,
	}
}

func (s *memoryPathStats) l1vPathSummaryRow(rec *memoryPathRecord) []string {
	avgAtToL1V := uint64(0)
	if rec.atToL1VCnt > 0 {
		avgAtToL1V = rec.atToL1VSumNS / rec.atToL1VCnt
	}
	row := []string{
		rec.originalReqID,
		strconv.FormatUint(s.outputSequence(rec), 10),
		strconv.FormatUint(rec.completionTimeNS, 10),
		rec.accessType,
		strconv.FormatUint(rec.pid, 10),
		strconv.FormatUint(rec.vaddr, 10),
		strconv.FormatUint(rec.paddr, 10),
		strconv.FormatUint(pageBaseForLog(rec.paddr, s.log2Page), 10),
		strconv.FormatUint(cachelineAddr(rec), 10),
		strconv.FormatUint(rec.bytes, 10),
		strconv.Itoa(rec.requesterGPM),
		strconv.Itoa(rec.providerGPM),
		strconv.Itoa(rec.hops),
		strconv.FormatBool(rec.isRemote),
		routeForRecord(rec),
		finalSourceForRecord(rec),
		rec.l1vCacheResult,
		rec.l2CacheResult,
		strconv.Itoa(rec.parentCount),
		strconv.FormatUint(rec.l1vCacheLatencyNS, 10),
		strconv.FormatUint(rec.atToL1VMinNS, 10),
		strconv.FormatUint(avgAtToL1V, 10),
		strconv.FormatUint(rec.atToL1VMaxNS, 10),
	}
	for _, stage := range l1vPathStageColumns {
		row = append(row, strconv.FormatUint(rec.l1vPathStageSumNS[stage], 10))
	}
	return row
}

func componentAndPort(port sim.Port) (componentName, portName string) {
	if port == nil {
		return "", ""
	}
	portName = port.Name()
	componentName = componentNameFromPort(portName)
	return componentName, portName
}

func componentNameFromPort(name string) string {
	if name == "" {
		return ""
	}
	if idx := strings.LastIndex(name, "."); idx > 0 {
		return name[:idx]
	}
	return name
}

func cachelineAddr(rec *memoryPathRecord) uint64 {
	if rec.hasPAddr {
		return rec.paddr
	}
	return rec.vaddr
}

func routeForRecord(rec *memoryPathRecord) string {
	if rec.route != "" {
		return rec.route
	}
	if rec.isRemote {
		return "remote"
	}
	return "local"
}

func finalSourceForRecord(rec *memoryPathRecord) string {
	if rec.finalSource != "" {
		return rec.finalSource
	}
	if rec.source != "" {
		return rec.source
	}
	return ""
}

func stringInSlice(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func normalizeNetworkPathDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "return", "response", "rsp", "cross_gpu_return", "cross-gpu-return",
		"cross_gpu_response", "cross-gpu-response":
		return "cross_gpu_return"
	default:
		return "cross_gpu_request"
	}
}

func percentileUint64(values []uint64, percentile float64) uint64 {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return values[0]
	}
	if percentile >= 1 {
		return values[len(values)-1]
	}
	index := int(percentile * float64(len(values)-1))
	return values[index]
}

func floatAvg(sum uint64, count uint64) float64 {
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}

func topIntCount(counts map[int]uint64) (int, uint64) {
	topKey := -1
	var topCount uint64
	for key, count := range counts {
		if count > topCount || (count == topCount && (topKey < 0 || key < topKey)) {
			topKey = key
			topCount = count
		}
	}
	return topKey, topCount
}

func topStringCount(counts map[string]uint64) (string, uint64) {
	topKey := ""
	var topCount uint64
	for key, count := range counts {
		if count > topCount || (count == topCount && (topKey == "" || key < topKey)) {
			topKey = key
			topCount = count
		}
	}
	return topKey, topCount
}

func intCountList(counts map[int]uint64) string {
	keys := make([]int, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d:%d", key, counts[key]))
	}
	return strings.Join(parts, ";")
}
