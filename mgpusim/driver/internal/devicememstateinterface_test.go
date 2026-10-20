package internal

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Implementation of regular DeviceMemoryState", func() {

	regularDMS := newDeviceRegularMemoryState(12)

	BeforeEach(func() {
		regularDMS.setStorageSize(0x1_0000_0000)
		regularDMS.setInitialAddress(0x0_0000_1000)
		rDMS := regularDMS.(*deviceMemoryStateImpl)
		rDMS.nextPAddr = rDMS.initialRangeEnd
		rDMS.availablePAddrs = rDMS.availablePAddrs[:0]
	})

	It("should lazily allocate the initial contiguous range before returned pages", func() {
		regularDMS.setStorageSize(4 * 4096)
		regularDMS.setInitialAddress(0x1000)
		rDMS := regularDMS.(*deviceMemoryStateImpl)

		Expect(rDMS.availablePAddrs).To(BeEmpty())
		Expect(regularDMS.popNextAvailablePAddrs()).To(Equal(uint64(0x1000)))
		regularDMS.addSinglePAddr(0x1000)
		Expect(regularDMS.popNextAvailablePAddrs()).To(Equal(uint64(0x2000)))
		Expect(regularDMS.popNextAvailablePAddrs()).To(Equal(uint64(0x3000)))
		Expect(regularDMS.popNextAvailablePAddrs()).To(Equal(uint64(0x4000)))
		Expect(regularDMS.popNextAvailablePAddrs()).To(Equal(uint64(0x1000)))
		Expect(regularDMS.noAvailablePAddrs()).To(BeTrue())
	})

	It("should add PAddrs to regular DMS", func() {
		regularDMS.addSinglePAddr(0x0_0000_1000)
		regularDMS.addSinglePAddr(0x0_0000_2000)
		regularDMS.addSinglePAddr(0x0_0000_3000)
		regularDMS.addSinglePAddr(0x0_0000_4000)

		rDMS := regularDMS.(*deviceMemoryStateImpl)

		Expect(rDMS.availablePAddrs).To(HaveLen(4))
	})

	It("should get next available PAddrs", func() {
		//if last test didn't work this probably won't either
		regularDMS.addSinglePAddr(0x0_0000_1000)
		regularDMS.addSinglePAddr(0x0_0000_2000)
		regularDMS.addSinglePAddr(0x0_0000_3000)
		regularDMS.addSinglePAddr(0x0_0000_4000)

		addr1 := regularDMS.popNextAvailablePAddrs()
		addr2 := regularDMS.popNextAvailablePAddrs()

		Expect(addr1).To(Equal(uint64(0x0_0000_1000)))
		Expect(addr2).To(Equal(uint64(0x0_0000_2000)))
		rDMS := regularDMS.(*deviceMemoryStateImpl)
		Expect(rDMS.availablePAddrs).To(HaveLen(2))
	})

	It("should allocate multiple PAddrs", func() {
		regularDMS.addSinglePAddr(0x0_0000_1000)
		regularDMS.addSinglePAddr(0x0_0000_2000)
		regularDMS.addSinglePAddr(0x0_0000_3000)
		regularDMS.addSinglePAddr(0x0_0000_4000)

		addrs := regularDMS.allocateMultiplePages(3)

		Expect(addrs).To(HaveLen(3))
		Expect(addrs[0]).To(Equal(uint64(0x0_0000_1000)))
		Expect(addrs[1]).To(Equal(uint64(0x0_0000_2000)))
		Expect(addrs[2]).To(Equal(uint64(0x0_0000_3000)))

		rDMS := regularDMS.(*deviceMemoryStateImpl)
		Expect(rDMS.availablePAddrs).To(HaveLen(1))
	})

	It("should have no available PAddrs", func() {
		ok := regularDMS.noAvailablePAddrs()
		Expect(ok).To(BeTrue())

		regularDMS.addSinglePAddr(0x0_0000_1000)
		ok = regularDMS.noAvailablePAddrs()
		Expect(ok).To(BeFalse())
	})

})
