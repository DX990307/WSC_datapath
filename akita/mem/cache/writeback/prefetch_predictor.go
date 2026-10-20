package writeback

import "github.com/sarchlab/akita/v3/mem/vm"

// DemandStreamKey identifies one real-demand stream. Source is a stable hash
// of the request source and StreamID carries the issuing instruction context.
type DemandStreamKey struct {
	PID      vm.PID
	Source   uint64
	StreamID uint64
}

// PatternToken identifies one live direct-mapped predictor entry. Generation
// prevents feedback from an older prefetched line from modifying a replacement
// stream that later occupies the same bounded slot.
type PatternToken struct {
	Index      int
	Generation uint64
	Key        TypedFilterKey
	Lookahead  uint64
}

// PredictorObservation describes the state changes caused by one real demand.
// Once two equal real-demand strides establish a pattern, the same observation
// may install PATTERN and propose the next line. The caller still admits that
// candidate only after the typed Filter accepts and validates PATTERN.
type PredictorObservation struct {
	Candidate      uint64
	HasCandidate   bool
	Token          PatternToken
	InstallPattern *TypedFilterKey
	DeletePattern  *TypedFilterKey
}

// DemandStridePredictorStats contains implementation-independent counters used
// by both the local L2 and requester RDMA instances.
type DemandStridePredictorStats struct {
	Capacity                 uint64
	RealDemands              uint64
	CandidatesGenerated      uint64
	PatternsEstablished      uint64
	PatternReplacements      uint64
	PatternInsertFails       uint64
	UsefulFeedback           uint64
	UnusedFeedback           uint64
	StaleFeedbackIgnored     uint64
	TimelyFeedback           uint64
	LateFeedback             uint64
	LateDistanceIncreases    uint64
	DemandCoveredFeedback    uint64
	CoveredDistanceIncreases uint64
	HorizonDistanceIncreases uint64
	PageFrontierClamps       uint64
	StaleDistanceFeedback    uint64
	CandidateLookaheadTotal  uint64
	CandidateLookaheadMax    uint64
	EvidenceOne              uint64
	EvidenceTwo              uint64
	CandidateStrideOne       uint64
	CandidateStrideSmall     uint64
	CandidateStrideMedium    uint64
	CandidateStrideLarge     uint64
	CandidateStrideNegative  uint64
}

type demandStrideEntry struct {
	valid             bool
	stream            DemandStreamKey
	lastLine          uint64
	lastStride        int64
	evidence          uint8
	patternInstalled  bool
	patternKey        TypedFilterKey
	generation        uint64
	lookahead         uint64
	timelyProven      bool
	trainingPending   bool
	trainingLookahead uint64
}

// DemandStridePredictor is a bounded direct-mapped candidate generator. It
// learns only from real demands and proposes at most one cache-line candidate.
// Approximate PATTERN membership, not this table, admits the speculation.
type DemandStridePredictor struct {
	lineBytes    uint64
	pageBytes    uint64
	maxLookahead uint64
	// candidateOnEstablish is enabled only by the local M1 prefetch path.
	// Requester-RDMA keeps the conservative next-demand validation policy.
	candidateOnEstablish bool
	// exponentialLateLookahead is also a local-M1-only policy. The shared
	// requester-RDMA predictor retains its conservative linear feedback.
	exponentialLateLookahead bool
	// feedbackGatedIssue is enabled only by M1. It permits at most one
	// speculative sibling per stream until that sibling receives real-demand
	// feedback. Real-demand pairs never consume this state.
	feedbackGatedIssue   bool
	pageFrontierClamping bool
	entry                []demandStrideEntry
	stats                DemandStridePredictorStats
}

// EnableCandidateOnPatternEstablishment lets the local M1 path use the first
// candidate as soon as two equal real-demand strides establish PATTERN. The
// candidate remains subject to the caller's Filter and resource admission.
func (p *DemandStridePredictor) EnableCandidateOnPatternEstablishment() {
	if p != nil {
		p.candidateOnEstablish = true
	}
}

// EnableExponentialLateLookahead lets a repeatedly late local stream converge
// from one to two, four, and then eight strides of lead. The page-derived
// maxLookahead remains the architectural bound.
func (p *DemandStridePredictor) EnableExponentialLateLookahead() {
	if p != nil {
		p.exponentialLateLookahead = true
	}
}

// EnableFeedbackGatedIssue requires every speculative candidate from this
// predictor to receive demand feedback before the same stream can issue
// another one. The rule has no timeout or tunable credit count: one observed
// useful/late outcome releases one next opportunity, while an unused line
// naturally prevents more speculative work until eviction feedback retires
// the pattern. M1 uses this mode to bound traffic after a single lucky hit;
// requester-RDMA predictors deliberately retain their existing behavior.
func (p *DemandStridePredictor) EnableFeedbackGatedIssue() {
	if p != nil {
		p.feedbackGatedIssue = true
	}
}

// EnablePageFrontierClamping lets a local candidate whose learned horizon
// reaches beyond the current physical page target the farthest stride-aligned
// line still inside that page. Requester-RDMA retains strict rejection.
func (p *DemandStridePredictor) EnablePageFrontierClamping() {
	if p != nil {
		p.pageFrontierClamping = true
	}
}

// NewDemandStridePredictor creates a predictor with fixed hardware capacity.
func NewDemandStridePredictor(capacity int, lineBytes uint64) *DemandStridePredictor {
	return newDemandStridePredictor(capacity, lineBytes, 0)
}

// NewPageLocalDemandStridePredictor creates a predictor whose candidates stay
// in the triggering demand's physical page. The page boundary is also the
// architectural bound for feedback-directed lookahead, avoiding an arbitrary
// fixed prefetch distance.
func NewPageLocalDemandStridePredictor(
	capacity int,
	lineBytes uint64,
	pageBytes uint64,
) *DemandStridePredictor {
	if pageBytes < lineBytes || pageBytes&(pageBytes-1) != 0 ||
		pageBytes%lineBytes != 0 {
		panic("prefetch predictor page size must be a power-of-two multiple of the line size")
	}
	return newDemandStridePredictor(capacity, lineBytes, pageBytes)
}

func newDemandStridePredictor(
	capacity int,
	lineBytes uint64,
	pageBytes uint64,
) *DemandStridePredictor {
	if capacity < 1 {
		capacity = 1
	}
	if lineBytes == 0 || lineBytes&(lineBytes-1) != 0 {
		panic("prefetch predictor line size must be a power of two")
	}
	maxLookahead := uint64(1)
	if pageBytes > lineBytes {
		maxLookahead = pageBytes/lineBytes - 1
	}
	return &DemandStridePredictor{
		lineBytes:    lineBytes,
		pageBytes:    pageBytes,
		maxLookahead: maxLookahead,
		entry:        make([]demandStrideEntry, capacity),
		stats: DemandStridePredictorStats{
			Capacity: uint64(capacity),
		},
	}
}

// ObserveRealDemand updates one stream and may establish or use a pattern.
func (p *DemandStridePredictor) ObserveRealDemand(
	stream DemandStreamKey,
	address uint64,
) PredictorObservation {
	return p.observeRealDemand(stream, address, 1)
}

// ObserveRealDemandWithMinimumLookahead uses a resource-derived demand
// horizon as the smallest candidate distance. It remains real-demand trained,
// page bounded, and proposes at most one line. The ordinary entry point keeps
// the conservative one-line minimum used by requester-RDMA.
func (p *DemandStridePredictor) ObserveRealDemandWithMinimumLookahead(
	stream DemandStreamKey,
	address uint64,
	minimumLookahead uint64,
) PredictorObservation {
	return p.observeRealDemand(stream, address, minimumLookahead)
}

func (p *DemandStridePredictor) observeRealDemand(
	stream DemandStreamKey,
	address uint64,
	minimumLookahead uint64,
) PredictorObservation {
	var observation PredictorObservation
	if p == nil || len(p.entry) == 0 {
		return observation
	}
	p.stats.RealDemands++
	line := address & ^(p.lineBytes - 1)
	index := p.index(stream)
	entry := &p.entry[index]
	if !entry.valid || entry.stream != stream {
		if entry.valid && entry.patternInstalled {
			key := entry.patternKey
			observation.DeletePattern = &key
			p.stats.PatternReplacements++
		}
		generation := entry.generation + 1
		*entry = demandStrideEntry{
			valid:      true,
			stream:     stream,
			lastLine:   line,
			generation: generation,
			lookahead:  1,
		}
		return observation
	}

	stride, valid := signedLineStride(entry.lastLine, line)
	entry.lastLine = line
	if !valid || stride == 0 {
		return observation
	}
	if stride != entry.lastStride {
		if entry.patternInstalled {
			key := entry.patternKey
			observation.DeletePattern = &key
		}
		entry.lastStride = stride
		entry.evidence = 1
		p.stats.EvidenceOne++
		entry.patternInstalled = false
		entry.patternKey = TypedFilterKey{}
		entry.generation++
		entry.lookahead = 1
		entry.timelyProven = false
		entry.trainingPending = false
		entry.trainingLookahead = 0
		return observation
	}
	if entry.evidence < 2 {
		entry.evidence++
		if entry.evidence == 2 {
			p.stats.EvidenceTwo++
		}
	}
	if minimumLookahead < 1 {
		minimumLookahead = 1
	}
	if minimumLookahead > p.maxLookahead {
		minimumLookahead = p.maxLookahead
	}
	if minimumLookahead > entry.lookahead {
		entry.lookahead = minimumLookahead
		p.stats.HorizonDistanceIncreases++
	}
	patternKey := stridePatternKey(stream, stride)
	token := PatternToken{
		Index: index, Generation: entry.generation, Key: patternKey,
		Lookahead: entry.lookahead,
	}
	if !entry.patternInstalled {
		entry.patternKey = patternKey
		observation.Token = token
		observation.InstallPattern = &observation.Token.Key
		if !p.candidateOnEstablish {
			return observation
		}
	}

	candidateLookahead := entry.lookahead
	if p.pageFrontierClamping && p.pageBytes != 0 {
		bounded := p.pageBoundedLookahead(line, stride, candidateLookahead)
		if bounded == 0 {
			return observation
		}
		if bounded < candidateLookahead {
			candidateLookahead = bounded
			p.stats.PageFrontierClamps++
		}
	}
	token.Lookahead = candidateLookahead
	observation.Token = token
	scaledStride, valid := scaleSignedStride(stride, candidateLookahead)
	if !valid {
		return observation
	}
	candidate, valid := addSignedStride(line, scaledStride)
	if !valid || candidate%p.lineBytes != 0 {
		return observation
	}
	if p.pageBytes != 0 &&
		candidate/p.pageBytes != line/p.pageBytes {
		return observation
	}
	observation.Candidate = candidate
	observation.HasCandidate = true
	observation.Token = token
	p.stats.CandidatesGenerated++
	p.stats.CandidateLookaheadTotal += candidateLookahead
	if candidateLookahead > p.stats.CandidateLookaheadMax {
		p.stats.CandidateLookaheadMax = candidateLookahead
	}
	p.recordCandidateStride(stride)
	return observation
}

func (p *DemandStridePredictor) pageBoundedLookahead(
	line uint64,
	stride int64,
	desired uint64,
) uint64 {
	if desired == 0 || p.pageBytes == 0 || stride == 0 {
		return desired
	}
	pageStart := line & ^(p.pageBytes - 1)
	var available uint64
	if stride > 0 {
		step := uint64(stride)
		pageEnd := pageStart + p.pageBytes
		if pageEnd < pageStart || line >= pageEnd {
			return 0
		}
		available = (pageEnd - 1 - line) / step
	} else {
		step := uint64(-(stride + 1)) + 1
		available = (line - pageStart) / step
	}
	if available < desired {
		return available
	}
	return desired
}

// CanIssue reports whether a candidate may enter the physical prefetch path.
// A stream that has not yet demonstrated a timely hit is allowed one training
// request at a time. This prevents a newly detected but late stream from
// filling DRAM and MSHR queues with many copies of the same bad lead distance.
func (p *DemandStridePredictor) CanIssue(token PatternToken) bool {
	entry, valid := p.entryForToken(token)
	if !valid {
		return false
	}
	if p.feedbackGatedIssue {
		return !entry.trainingPending
	}
	return entry.timelyProven || !entry.trainingPending
}

// HasTimelyProof reports whether this exact predictor generation has already
// delivered a line before its demand arrived.  Callers can use this stronger
// evidence to distinguish conservative training from steady-state prefetches.
func (p *DemandStridePredictor) HasTimelyProof(token PatternToken) bool {
	entry, valid := p.entryForToken(token)
	return valid && entry.timelyProven
}

// MarkIssued reserves the single training request for an unproven stream.
func (p *DemandStridePredictor) MarkIssued(token PatternToken) bool {
	entry, valid := p.entryForToken(token)
	if !valid {
		return false
	}
	if p.feedbackGatedIssue {
		if entry.trainingPending {
			return false
		}
		entry.trainingPending = true
		entry.trainingLookahead = token.Lookahead
		return true
	}
	if !entry.timelyProven && entry.trainingPending {
		return false
	}
	if !entry.timelyProven {
		entry.trainingPending = true
		entry.trainingLookahead = token.Lookahead
	}
	return true
}

func (p *DemandStridePredictor) recordCandidateStride(stride int64) {
	if stride < 0 {
		p.stats.CandidateStrideNegative++
	}
	abs := stride
	if abs < 0 {
		abs = -abs
	}
	lines := uint64(abs) / p.lineBytes
	switch {
	case lines == 1:
		p.stats.CandidateStrideOne++
	case lines <= 4:
		p.stats.CandidateStrideSmall++
	case lines <= 16:
		p.stats.CandidateStrideMedium++
	default:
		p.stats.CandidateStrideLarge++
	}
}

// SetPatternInstalled records whether the shared Filter accepted a PATTERN
// update. A failed update leaves the stream trained but unable to speculate.
func (p *DemandStridePredictor) SetPatternInstalled(
	token PatternToken,
	installed bool,
) {
	entry, valid := p.entryForToken(token)
	if !valid {
		return
	}
	entry.patternInstalled = installed
	if installed {
		entry.patternKey = token.Key
		p.stats.PatternsEstablished++
	} else {
		p.stats.PatternInsertFails++
	}
}

// InvalidatePattern makes a negative PATTERN result visible to the exact
// predictor state so a later real-demand sequence may retrain it.
func (p *DemandStridePredictor) InvalidatePattern(token PatternToken) {
	entry, valid := p.entryForToken(token)
	if !valid {
		return
	}
	entry.patternInstalled = false
	entry.evidence = 1
	entry.generation++
	entry.lookahead = 1
	entry.timelyProven = false
	entry.trainingPending = false
	entry.trainingLookahead = 0
}

// Reward records a real demand that consumed a predicted line.
func (p *DemandStridePredictor) Reward(token PatternToken) {
	if _, valid := p.entryForToken(token); !valid {
		p.stats.StaleFeedbackIgnored++
		return
	}
	p.stats.UsefulFeedback++
}

// ResolvePairedRead records whether one demand-attached sibling arrived before
// its real demand. M1 keeps the direct-sibling relation and uses the ordinary
// one-training-request rule to bound an unproven stream; unlike the remote
// predictor, it never manufactures a farther candidate after late feedback.
func (p *DemandStridePredictor) ResolvePairedRead(
	token PatternToken,
	late bool,
) {
	entry, valid := p.entryForToken(token)
	if !valid {
		p.stats.StaleFeedbackIgnored++
		return
	}
	p.stats.UsefulFeedback++
	p.resolveTraining(entry, token)
	if late {
		p.stats.LateFeedback++
		entry.timelyProven = false
		return
	}
	p.stats.TimelyFeedback++
	entry.timelyProven = true
}

// RewardWithTimeliness records a prediction that a real demand consumed. A
// late prediction exponentially advances the next candidate. This reaches a
// latency-covering distance in logarithmic, rather than linear, feedback
// rounds without a benchmark-specific distance. The configured page boundary
// is the architectural cap. Timely feedback keeps the smallest distance
// already proven to cover the stream.
func (p *DemandStridePredictor) RewardWithTimeliness(
	token PatternToken,
	late bool,
) {
	entry, valid := p.entryForToken(token)
	if !valid {
		p.stats.StaleFeedbackIgnored++
		return
	}
	p.stats.UsefulFeedback++
	if !late {
		p.stats.TimelyFeedback++
		p.resolveTraining(entry, token)
		if token.Lookahead == entry.lookahead {
			entry.timelyProven = true
		}
		return
	}
	p.stats.LateFeedback++
	p.resolveTraining(entry, token)
	if token.Lookahead == entry.lookahead {
		entry.timelyProven = false
	}
	if p.increaseLookahead(token, entry) {
		p.stats.LateDistanceIncreases++
	}
}

// RewardAfterObservedLate records that a prediction later supplied a real
// demand after its late feedback had already advanced the stream at demand
// arrival. It must not count or apply the same timeliness evidence twice.
func (p *DemandStridePredictor) RewardAfterObservedLate(token PatternToken) {
	if _, valid := p.entryForToken(token); !valid {
		p.stats.StaleFeedbackIgnored++
		return
	}
	p.stats.UsefulFeedback++
}

// ObserveLate records a correct candidate that lost the race to the demand
// before providing data. It changes distance but is deliberately not counted
// as useful prefetch work.
func (p *DemandStridePredictor) ObserveLate(token PatternToken) {
	entry, valid := p.entryForToken(token)
	if !valid {
		p.stats.StaleFeedbackIgnored++
		return
	}
	p.stats.LateFeedback++
	p.resolveTraining(entry, token)
	if token.Lookahead == entry.lookahead {
		entry.timelyProven = false
	}
	if p.increaseLookahead(token, entry) {
		p.stats.LateDistanceIncreases++
	}
}

// ObserveDemandCoveredCandidate records that the predicted line is already
// being fetched by an ordinary demand. This is free lead-distance evidence:
// issuing another request would be redundant, but leaving the predictor at the
// same distance would repeat the same late prediction. The next candidate is
// therefore advanced without generating speculative traffic.
func (p *DemandStridePredictor) ObserveDemandCoveredCandidate(token PatternToken) {
	entry, valid := p.entryForToken(token)
	if !valid {
		p.stats.StaleFeedbackIgnored++
		return
	}
	p.stats.DemandCoveredFeedback++
	if token.Lookahead == entry.lookahead {
		entry.timelyProven = false
	}
	if p.increaseLookahead(token, entry) {
		p.stats.CoveredDistanceIncreases++
	}
}

func (p *DemandStridePredictor) resolveTraining(
	entry *demandStrideEntry,
	token PatternToken,
) {
	if entry.trainingPending && entry.trainingLookahead == token.Lookahead {
		entry.trainingPending = false
		entry.trainingLookahead = 0
	}
}

func (p *DemandStridePredictor) increaseLookahead(
	token PatternToken,
	entry *demandStrideEntry,
) bool {
	if entry.lookahead == 0 {
		entry.lookahead = 1
	}
	// Multiple predictions may be outstanding at the same learned distance.
	// Only the first late result is evidence for advancing that distance. Late
	// feedback from an older distance must not repeatedly inflate lookahead.
	if token.Lookahead != entry.lookahead {
		p.stats.StaleDistanceFeedback++
		return false
	}
	if entry.lookahead >= p.maxLookahead {
		return false
	}
	next := entry.lookahead + 1
	if p.exponentialLateLookahead {
		// A late local stream needs substantially more lead, not one more line
		// per feedback round. Doubling is parameter-free, converges quickly for
		// long DRAM latency, and remains bounded by the physical page.
		next = entry.lookahead * 2
	}
	if next < entry.lookahead || next > p.maxLookahead {
		next = p.maxLookahead
	}
	entry.lookahead = next
	return true
}

// Penalize retires a pattern after a prefetched line is discarded unused.
// The returned key must be deleted from the shared Filter by the caller.
func (p *DemandStridePredictor) Penalize(
	token PatternToken,
) (TypedFilterKey, bool) {
	entry, valid := p.entryForToken(token)
	if !valid {
		p.stats.StaleFeedbackIgnored++
		return TypedFilterKey{}, false
	}
	key := entry.patternKey
	entry.patternInstalled = false
	entry.evidence = 1
	entry.generation++
	entry.lookahead = 1
	entry.timelyProven = false
	entry.trainingPending = false
	entry.trainingLookahead = 0
	p.stats.UnusedFeedback++
	return key, true
}

// Reset clears bounded predictor state without retaining speculative history.
func (p *DemandStridePredictor) Reset() {
	if p == nil {
		return
	}
	for i := range p.entry {
		p.entry[i] = demandStrideEntry{}
	}
}

// Stats returns a counter snapshot.
func (p *DemandStridePredictor) Stats() DemandStridePredictorStats {
	if p == nil {
		return DemandStridePredictorStats{}
	}
	return p.stats
}

func (p *DemandStridePredictor) entryForToken(
	token PatternToken,
) (*demandStrideEntry, bool) {
	if p == nil || token.Index < 0 || token.Index >= len(p.entry) {
		return nil, false
	}
	entry := &p.entry[token.Index]
	return entry, entry.valid && entry.generation == token.Generation &&
		entry.patternKey == token.Key
}

func (p *DemandStridePredictor) index(stream DemandStreamKey) int {
	hash := typedFilterMix(uint64(stream.PID) ^
		typedFilterMix(stream.Source+0x9e3779b97f4a7c15) ^
		typedFilterMix(stream.StreamID+0xd1b54a32d192ed03))
	return int(hash % uint64(len(p.entry)))
}

func stridePatternKey(
	stream DemandStreamKey,
	stride int64,
) TypedFilterKey {
	return TypedFilterKey{
		PID: stream.PID,
		Owner: typedFilterMix(stream.Source ^
			typedFilterMix(stream.StreamID+0x94d049bb133111eb)),
		Address: encodeSignedStride(stride),
		Type:    FilterPattern,
	}
}

func encodeSignedStride(stride int64) uint64 {
	return uint64(stride<<1) ^ uint64(stride>>63)
}

func signedLineStride(previous, current uint64) (int64, bool) {
	if current >= previous {
		delta := current - previous
		if delta > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(delta), true
	}
	delta := previous - current
	if delta > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return -int64(delta), true
}

func addSignedStride(address uint64, stride int64) (uint64, bool) {
	if stride >= 0 {
		delta := uint64(stride)
		if ^uint64(0)-address < delta {
			return 0, false
		}
		return address + delta, true
	}
	delta := uint64(-stride)
	if address < delta {
		return 0, false
	}
	return address - delta, true
}

func scaleSignedStride(stride int64, multiplier uint64) (int64, bool) {
	if multiplier == 0 {
		return 0, false
	}
	if stride >= 0 {
		value := uint64(stride)
		if value != 0 && multiplier > uint64(^uint64(0)>>1)/value {
			return 0, false
		}
		return int64(value * multiplier), true
	}
	value := uint64(-(stride + 1)) + 1
	maxNegative := uint64(^uint64(0)>>1) + 1
	if value != 0 && multiplier > maxNegative/value {
		return 0, false
	}
	scaled := value * multiplier
	if scaled == maxNegative {
		return -int64(^uint64(0)>>1) - 1, true
	}
	return -int64(scaled), true
}
