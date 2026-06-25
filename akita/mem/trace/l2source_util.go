package trace

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/sarchlab/akita/v3/sim"
)

var gpuIDPattern = regexp.MustCompile(`GPU\[(\d+)\]`)

func parseGPMID(name string) int {
	match := gpuIDPattern.FindStringSubmatch(name)
	if len(match) != 2 {
		return -1
	}

	id, err := strconv.Atoi(match[1])
	if err != nil {
		return -1
	}
	return id
}

func manhattanHops(a, b, tileWidth int) int {
	if a < 0 || b < 0 || tileWidth <= 0 {
		return -1
	}

	ax := a % tileWidth
	ay := a / tileWidth
	bx := b % tileWidth
	by := b / tileWidth

	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func normalizeOp(op string) string {
	if op == "" {
		return "unknown"
	}
	return op
}

func normalizeSourceBase(sourceBase string) string {
	switch sourceBase {
	case sourceBaseL2Cache,
		sourceBaseL2MSHR,
		sourceBaseDRAM,
		sourceBaseWriteAllocate:
		return sourceBase
	case "":
		return sourceBaseUnknown
	default:
		return sourceBase
	}
}

func isL2CacheName(name string) bool {
	return strings.Contains(name, ".L2[")
}

func timeToNS(t sim.VTimeInSec) uint64 {
	if t <= 0 {
		return 0
	}
	return uint64(math.Round(float64(t) * 1e9))
}

func pageBase(paddr uint64) uint64 {
	return (paddr / defaultL2SourcePageSize) * defaultL2SourcePageSize
}

func isNeighborHop(hops int) bool {
	return hops == 1
}
