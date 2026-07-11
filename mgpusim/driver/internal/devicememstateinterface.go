package internal

// A DeviceMemoryState handles the internal memory allocation algorithms
type DeviceMemoryState interface {
	setInitialAddress(addr uint64)
	getInitialAddress() uint64
	setStorageSize(size uint64)
	getStorageSize() uint64
	addSinglePAddr(addr uint64)
	popNextAvailablePAddrs() uint64
	noAvailablePAddrs() bool
	allocateMultiplePages(numPages int) []uint64
}

// NewDeviceMemoryState creates a new device memory state based on allocator type.
func NewDeviceMemoryState(log2pagesize uint64) DeviceMemoryState {
	switch MemoryAllocatorType {
	case AllocatorTypeDefault:
		return newDeviceRegularMemoryState(log2pagesize)
	case AllocatorTypeBuddy:
		return newDeviceBuddyMemoryState(log2pagesize)
	default:
		panic("Invalid memory allocator type")
	}
}

func newDeviceRegularMemoryState(log2pagesize uint64) DeviceMemoryState {
	return &deviceMemoryStateImpl{
		log2PageSize: log2pagesize,
	}
}

// original implementation of DeviceMemoryState holding free addresses in array
type deviceMemoryStateImpl struct {
	log2PageSize    uint64
	initialAddress  uint64
	storageSize     uint64
	nextPAddr       uint64
	initialRangeEnd uint64
	// availablePAddrs contains returned pages after the never-allocated
	// contiguous range. Keeping returned pages separate preserves the legacy
	// FIFO allocation order without materializing every physical page at
	// device registration time.
	availablePAddrs []uint64
}

func (dms *deviceMemoryStateImpl) setInitialAddress(addr uint64) {
	dms.initialAddress = addr
	dms.nextPAddr = addr
	dms.initialRangeEnd = addr + dms.storageSize
	dms.availablePAddrs = dms.availablePAddrs[:0]
}

func (dms *deviceMemoryStateImpl) getInitialAddress() uint64 {
	return dms.initialAddress
}

func (dms *deviceMemoryStateImpl) setStorageSize(size uint64) {
	dms.storageSize = size
}

func (dms *deviceMemoryStateImpl) getStorageSize() uint64 {
	return dms.storageSize
}

func (dms *deviceMemoryStateImpl) addSinglePAddr(addr uint64) {
	dms.availablePAddrs = append(dms.availablePAddrs, addr)
}

func (dms *deviceMemoryStateImpl) popNextAvailablePAddrs() uint64 {
	if dms.nextPAddr < dms.initialRangeEnd {
		nextPAddr := dms.nextPAddr
		dms.nextPAddr += uint64(1) << dms.log2PageSize
		return nextPAddr
	}

	nextPAddr := dms.availablePAddrs[0]
	dms.availablePAddrs = dms.availablePAddrs[1:]
	return nextPAddr
}

func (dms *deviceMemoryStateImpl) noAvailablePAddrs() bool {
	return dms.nextPAddr >= dms.initialRangeEnd &&
		len(dms.availablePAddrs) == 0
}

func (dms *deviceMemoryStateImpl) allocateMultiplePages(
	numPages int,
) []uint64 {
	pAddrs := make([]uint64, numPages)
	for i := 0; i < numPages; i++ {
		pAddrs[i] = dms.popNextAvailablePAddrs()
	}
	return pAddrs
}
