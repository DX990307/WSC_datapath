package trace

import "sync"

const (
	defaultL2SourcePrefix    = "l2_source"
	defaultL2SourceTileWidth = 7
	defaultL2SourcePageSize  = 4096

	sourceBaseL2Cache       = "l2_cache"
	sourceBaseL2MSHR        = "l2_mshr"
	sourceBaseDRAM          = "dram"
	sourceBaseWriteAllocate = "write_allocate"
	sourceBaseUnknown       = "unknown"

	sourcePrefixLocal  = "local_"
	sourcePrefixRemote = "remote_"
)

const (
	summaryCSVHeader         = "source_tier,requester_gpm,provider_gpm,hops,component,access_type,accesses,bytes,avg_latency_ns"
	remoteMatrixCSVHeader    = "requester_gpm,provider_gpm,hops,access_type,accesses,bytes,avg_latency_ns"
	dataSourceCSVHeader      = "source,requester_gpm,provider_gpm,hops,component,access_type,has_vaddr,vaddr,has_paddr,paddr,accesses,bytes,avg_latency_ns"
	pageSourceCSVHeader      = "source,requester_gpm,provider_gpm,hops,is_neighbor,access_type,page_paddr,accesses,bytes,avg_latency_ns,first_time_ns,last_time_ns"
	remoteFillReuseCSVHeader = "requester_gpm,provider_gpm,hops,component,has_vaddr,vaddr,has_paddr,paddr,remote_dram_fills,remote_dram_fill_bytes,first_fill_time_ns,last_fill_time_ns,local_l2_hit_reuses,local_l2_hit_reuse_bytes,first_local_reuse_time_ns,last_local_reuse_time_ns"
)

type l2SourceCounter struct {
	accesses     uint64
	bytes        uint64
	latencySumNS uint64
	firstTimeNS  uint64
	lastTimeNS   uint64
}

// L2AccessInfo is carried in mem.AccessReq.Info after address translation.
// It keeps the original virtual address together with the translated physical
// address, and records whether the request crossed the RDMA path.
type L2AccessInfo struct {
	OriginalInfo interface{}

	HasVAddr bool
	VAddr    uint64
	HasPAddr bool
	PAddr    uint64

	IsRemote      bool
	RequesterName string
	ProviderName  string
	RequesterGPM  int
	ProviderGPM   int

	OriginalReqID     string
	PathID            string
	TranslationReqID  string
	TranslationTaskID string
	TranslatedReqID   string
}

// MemoryPathBatchInfo carries multiple original request identities for one
// lower-level memory transaction, such as an M1 DRAM access-unit batch.
type MemoryPathBatchInfo struct {
	Infos []interface{}
}

// WithMemoryPathBatchInfo builds an Info payload that fans lower-level path
// stages out to every request that shares the same memory transaction.
func WithMemoryPathBatchInfo(infos ...interface{}) interface{} {
	filtered := make([]interface{}, 0, len(infos))
	for _, info := range infos {
		if info != nil {
			filtered = append(filtered, info)
		}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return MemoryPathBatchInfo{Infos: filtered}
}

type l2LocalKey struct {
	gpm       int
	component string
	op        string
}

type l2RemoteKey struct {
	requesterGPM int
	providerGPM  int
	requester    string
	provider     string
	hops         int
	op           string
}

type l2DataSourceKey struct {
	source       string
	requesterGPM int
	providerGPM  int
	hops         int
	component    string
	op           string
	hasVAddr     bool
	vaddr        uint64
	hasPAddr     bool
	paddr        uint64
}

type l2PageSourceKey struct {
	source       string
	requesterGPM int
	providerGPM  int
	hops         int
	op           string
	pagePAddr    uint64
}

type l2RemoteFillKey struct {
	requesterGPM int
	providerGPM  int
	component    string
	hasVAddr     bool
	vaddr        uint64
	hasPAddr     bool
	paddr        uint64
}

type l2RemoteFillLookupKey struct {
	providerGPM int
	component   string
	hasPAddr    bool
	paddr       uint64
}

type l2RemoteFillCounter struct {
	fills                uint64
	bytes                uint64
	firstFillTimeNS      uint64
	lastFillTimeNS       uint64
	localL2HitReuses     uint64
	localL2HitReuseBytes uint64
	firstLocalReuseNS    uint64
	lastLocalReuseNS     uint64
}

type l2SourceStats struct {
	sync.Mutex

	enabled   bool
	prefix    string
	tileWidth int

	localDRAM       map[l2LocalKey]*l2SourceCounter
	remoteGPM       map[l2RemoteKey]*l2SourceCounter
	dataSource      map[l2DataSourceKey]*l2SourceCounter
	pageSource      map[l2PageSourceKey]*l2SourceCounter
	remoteFillReuse map[l2RemoteFillKey]*l2RemoteFillCounter
	remoteFillIndex map[l2RemoteFillLookupKey][]l2RemoteFillKey
}

var globalL2SourceStats = newL2SourceStats()

func newL2SourceStats() *l2SourceStats {
	stats := &l2SourceStats{
		tileWidth: defaultL2SourceTileWidth,
	}
	stats.resetMapsLocked()
	return stats
}

func (s *l2SourceStats) resetLocked(
	enabled bool,
	prefix string,
	tileWidth int,
) {
	if enabled && prefix == "" {
		prefix = defaultL2SourcePrefix
	}
	if tileWidth <= 0 {
		tileWidth = defaultL2SourceTileWidth
	}

	s.enabled = enabled
	s.prefix = prefix
	s.tileWidth = tileWidth
	s.resetMapsLocked()
}

func (s *l2SourceStats) resetMapsLocked() {
	s.localDRAM = make(map[l2LocalKey]*l2SourceCounter)
	s.remoteGPM = make(map[l2RemoteKey]*l2SourceCounter)
	s.dataSource = make(map[l2DataSourceKey]*l2SourceCounter)
	s.pageSource = make(map[l2PageSourceKey]*l2SourceCounter)
	s.remoteFillReuse = make(map[l2RemoteFillKey]*l2RemoteFillCounter)
	s.remoteFillIndex = make(map[l2RemoteFillLookupKey][]l2RemoteFillKey)
}

// EnableL2SourceStats turns on aggregate L2 source tracking.
func EnableL2SourceStats(prefix string, tileWidth int) {
	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	globalL2SourceStats.resetLocked(true, prefix, tileWidth)
}

// DisableL2SourceStats turns off L2 source tracking and drops accumulated data.
func DisableL2SourceStats() {
	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	globalL2SourceStats.resetLocked(
		false, "", globalL2SourceStats.tileWidth)
}

// L2SourceStatsEnabled reports whether L2 source tracking is active.
func L2SourceStatsEnabled() bool {
	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	return globalL2SourceStats.enabled
}

// WithL2AddressInfo attaches the original virtual address and translated
// physical address to an access request.
func WithL2AddressInfo(info interface{}, vaddr, paddr uint64) interface{} {
	accessInfo := cloneL2AccessInfo(info)
	accessInfo.HasVAddr = true
	accessInfo.VAddr = vaddr
	accessInfo.HasPAddr = true
	accessInfo.PAddr = paddr
	return accessInfo
}

// WithMemoryPathInfo connects translated L2 requests back to the original
// L1V request and translation task for request-level path tracing.
func WithMemoryPathInfo(
	info interface{},
	originalReqID string,
	translationReqID string,
	translationTaskID string,
	translatedReqID string,
) interface{} {
	accessInfo := cloneL2AccessInfo(info)
	accessInfo.OriginalReqID = originalReqID
	accessInfo.PathID = originalReqID
	accessInfo.TranslationReqID = translationReqID
	accessInfo.TranslationTaskID = translationTaskID
	accessInfo.TranslatedReqID = translatedReqID
	return accessInfo
}

// WithL2RemoteInfo marks an access request as crossing an RDMA path.
func WithL2RemoteInfo(info interface{}, requesterName, providerName string) interface{} {
	accessInfo := cloneL2AccessInfo(info)
	accessInfo.IsRemote = true
	accessInfo.RequesterName = requesterName
	accessInfo.ProviderName = providerName
	accessInfo.RequesterGPM = parseGPMID(requesterName)
	accessInfo.ProviderGPM = parseGPMID(providerName)
	return accessInfo
}

// GetL2AccessInfo extracts trace metadata from mem.AccessReq.Info.
func GetL2AccessInfo(info interface{}) (L2AccessInfo, bool) {
	switch info := info.(type) {
	case *L2AccessInfo:
		if info == nil {
			return L2AccessInfo{}, false
		}
		return *info, true
	case L2AccessInfo:
		return info, true
	default:
		return L2AccessInfo{}, false
	}
}

func cloneL2AccessInfo(info interface{}) *L2AccessInfo {
	switch info := info.(type) {
	case *L2AccessInfo:
		if info == nil {
			return &L2AccessInfo{}
		}
		clone := *info
		return &clone
	case L2AccessInfo:
		clone := info
		return &clone
	default:
		return &L2AccessInfo{OriginalInfo: info}
	}
}
