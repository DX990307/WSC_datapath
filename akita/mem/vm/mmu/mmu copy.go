package mmu

// import (
// 	"fmt"
// 	"log"
// 	"math"
// 	"reflect"

// 	"github.com/sarchlab/akita/v3/mem/mem"
// 	"github.com/sarchlab/akita/v3/mem/vm"
// 	"github.com/sarchlab/akita/v3/sim"
// 	"github.com/sarchlab/akita/v3/tracing"
// )

// type transaction struct {
// 	req       *vm.TranslationReq
// 	page      vm.Page
// 	cycleLeft int
// 	migration *vm.PageMigrationReqToDriver
// }

// type translatedTableEntry struct {
// 	vaddr          uint64
// 	pid            vm.PID
// 	innerLayerPort sim.Port
// 	predict        bool
// 	repectTimes    int
// }

// // MMU is the default mmu implementation. It is also an akita Component.
// type MMU struct {
// 	sim.TickingComponent

// 	topPort       sim.Port
// 	migrationPort sim.Port

// 	MigrationServiceProvider sim.Port

// 	topSender sim.BufferedSender

// 	pageTable           vm.PageTable
// 	latency             int
// 	maxRequestsInFlight int

// 	walkingTranslations      []transaction
// 	migrationQueue           []transaction
// 	migrationQueueSize       int
// 	currentOnDemandMigration transaction
// 	isDoingMigration         bool

// 	toRemoveFromPTW        []int
// 	PageAccessedByDeviceID map[uint64][]uint64

// 	InnerLoop  map[uint64]uint64
// 	MiddleLoop map[uint64]uint64
// 	OutLoop    map[uint64]uint64

// 	PWqueue         []sim.Msg
// 	translatedTable []translatedTableEntry
// 	// GMMUCacheTable map[uint64]sim.Port
// 	GMMUCacheTable *mem.MultiPageFinder
// }

// // Tick defines how the MMU update state each cycle
// func (mmu *MMU) Tick(now sim.VTimeInSec) bool {
// 	madeProgress := false

// 	madeProgress = mmu.parseFromTop(now) || madeProgress
// 	madeProgress = mmu.processTranslationReqs(now) || madeProgress
// 	madeProgress = mmu.walkPageTable(now) || madeProgress
// 	madeProgress = mmu.sendMigrationToDriver(now) || madeProgress
// 	madeProgress = mmu.processMigrationReturn(now) || madeProgress

// 	for i := 0; i < 48; i++ {
// 		madeProgress = mmu.topSender.Tick(now) || madeProgress
// 	}

// 	return madeProgress
// }

// func (mmu *MMU) walkPageTable(now sim.VTimeInSec) bool {
// 	madeProgress := false
// 	for i := 0; i < len(mmu.walkingTranslations); i++ {
// 		if mmu.walkingTranslations[i].cycleLeft > 0 {
// 			mmu.walkingTranslations[i].cycleLeft--
// 			madeProgress = true
// 			continue
// 		}

// 		madeProgress = mmu.finalizePageWalk(now, i) || madeProgress
// 	}

// 	tmp := mmu.walkingTranslations[:0]
// 	for i := 0; i < len(mmu.walkingTranslations); i++ {
// 		if !mmu.toRemove(i) {
// 			tmp = append(tmp, mmu.walkingTranslations[i])
// 		}
// 	}
// 	mmu.walkingTranslations = tmp
// 	mmu.toRemoveFromPTW = nil

// 	return madeProgress
// }

// func (mmu *MMU) finalizePageWalk(
// 	now sim.VTimeInSec,
// 	walkingIndex int,
// ) bool {
// 	req := mmu.walkingTranslations[walkingIndex].req
// 	page, found := mmu.pageTable.Find(req.PID, req.VAddr)

// 	// fmt.Printf("%0.9f,%s,GetReq,%s\n",
// 	// 	float64(now), mmu.topPort.Name(), req.TaskID)

// 	if !found {
// 		panic("page not found")
// 	}

// 	mmu.walkingTranslations[walkingIndex].page = page

// 	if page.IsMigrating {
// 		return mmu.addTransactionToMigrationQueue(walkingIndex)
// 	}

// 	if mmu.pageNeedMigrate(mmu.walkingTranslations[walkingIndex]) {
// 		return mmu.addTransactionToMigrationQueue(walkingIndex)
// 	}

// 	return mmu.doPageWalkHit(now, walkingIndex)
// }

// func (mmu *MMU) addTransactionToMigrationQueue(walkingIndex int) bool {
// 	if len(mmu.migrationQueue) >= mmu.migrationQueueSize {
// 		return false
// 	}

// 	mmu.toRemoveFromPTW = append(mmu.toRemoveFromPTW, walkingIndex)
// 	mmu.migrationQueue = append(mmu.migrationQueue,
// 		mmu.walkingTranslations[walkingIndex])

// 	page := mmu.walkingTranslations[walkingIndex].page
// 	page.IsMigrating = true
// 	mmu.pageTable.Update(page)

// 	return true
// }

// func (mmu *MMU) pageNeedMigrate(walking transaction) bool {
// 	if walking.req.DeviceID == walking.page.DeviceID {
// 		return false
// 	}

// 	if !walking.page.Unified {
// 		return false
// 	}

// 	if walking.page.IsPinned {
// 		return false
// 	}

// 	return true
// }

// func (mmu *MMU) doPageWalkHit(
// 	now sim.VTimeInSec,
// 	walkingIndex int,
// ) bool {
// 	madeProgress := false

// 	if !mmu.topSender.CanSend(1) {
// 		return false
// 	}
// 	walking := mmu.walkingTranslations[walkingIndex]

// 	// dist := calculateDistance(0, int(walking.req.DeviceID))
// 	// fmt.Printf("%f \n", dist)

// 	rsp := vm.TranslationRspBuilder{}.
// 		WithSendTime(now).
// 		WithSrc(mmu.topPort).
// 		WithDst(walking.req.Src).
// 		WithRspTo(walking.req.ID).
// 		WithPage(walking.page).
// 		WithTaskID(walking.req.TaskID).
// 		Build()

// 	fmt.Printf("%f, PageWalkHit %s, %d, %t %d\n", now, walking.req.Src.Name(), walking.req.VAddr, walking.req.NeedTranslate, walking.req.PID)

// 	Hop := mmu.GetHops(walking.req)

// 	fmt.Printf("Hops %d\n", Hop)
// 	if !mmu.topSender.CanSend(1) {
// 		return true
// 	}

// 	mmu.topSender.Send(rsp)

// 	vaddr := walking.req.VAddr
// 	madeProgress = true
// 	mmu.pageTable.UpdateAccessCounts(walking.req.PID, vaddr)

// 	// gpuInnerLayerID := mmu.getNextLayerGPUID(vaddr, 8)
// 	gpuInnerLayerID := mmu.getNextLayerGPUID(vaddr, uint64(len(mmu.InnerLoop)))
// 	gpuID := mmu.InnerLoop[gpuInnerLayerID]
// 	port := mmu.GMMUCacheTable.Find(gpuID)
// 	ininnerLayer := false

// 	if port == walking.req.Src {
// 		ininnerLayer = true
// 	}
// 	// madeProgress = mmu.updateTranslatedTable(vaddr, port, true) || madeProgress
// 	if mmu.portInInLoopAndMidLoop(walking.req.Src) {
// 		madeProgress = mmu.updateTranslatedTable(vaddr, walking.req.PID, walking.req.Src, false) || madeProgress
// 	}

// 	madeProgress = mmu.sendToGMMU(now, walking, ininnerLayer) || madeProgress
// 	madeProgress = mmu.revisitPWQueue(now, vaddr, walking.page) || madeProgress
// 	mmu.toRemoveFromPTW = append(mmu.toRemoveFromPTW, walkingIndex)

// 	tracing.TraceReqComplete(walking.req, mmu)

// 	return madeProgress
// }

// func (mmu *MMU) sendMigrationToDriver(
// 	now sim.VTimeInSec,
// ) (madeProgress bool) {
// 	if len(mmu.migrationQueue) == 0 {
// 		return false
// 	}

// 	trans := mmu.migrationQueue[0]
// 	req := trans.req
// 	page, found := mmu.pageTable.Find(req.PID, req.VAddr)
// 	if !found {
// 		panic("page not found")
// 	}
// 	trans.page = page

// 	if req.DeviceID == page.DeviceID || page.IsPinned {
// 		mmu.sendTranlationRsp(now, trans)
// 		mmu.migrationQueue = mmu.migrationQueue[1:]
// 		mmu.markPageAsNotMigratingIfNotInTheMigrationQueue(page)

// 		return true
// 	}

// 	if mmu.isDoingMigration {
// 		return false
// 	}

// 	migrationInfo := new(vm.PageMigrationInfo)
// 	migrationInfo.GPUReqToVAddrMap = make(map[uint64][]uint64)
// 	migrationInfo.GPUReqToVAddrMap[trans.req.DeviceID] =
// 		append(migrationInfo.GPUReqToVAddrMap[trans.req.DeviceID],
// 			trans.req.VAddr)

// 	mmu.PageAccessedByDeviceID[page.VAddr] =
// 		append(mmu.PageAccessedByDeviceID[page.VAddr], page.DeviceID)

// 	migrationReq := vm.NewPageMigrationReqToDriver(
// 		now, mmu.migrationPort, mmu.MigrationServiceProvider)
// 	migrationReq.PID = page.PID
// 	migrationReq.PageSize = page.PageSize
// 	migrationReq.CurrPageHostGPU = page.DeviceID
// 	migrationReq.MigrationInfo = migrationInfo
// 	migrationReq.CurrAccessingGPUs = unique(mmu.PageAccessedByDeviceID[page.VAddr])
// 	migrationReq.RespondToTop = true

// 	err := mmu.migrationPort.Send(migrationReq)
// 	if err != nil {
// 		return false
// 	}

// 	trans.page.IsMigrating = true
// 	mmu.pageTable.Update(trans.page)
// 	trans.migration = migrationReq
// 	mmu.isDoingMigration = true
// 	mmu.currentOnDemandMigration = trans
// 	mmu.migrationQueue = mmu.migrationQueue[1:]

// 	return true
// }

// func (mmu *MMU) markPageAsNotMigratingIfNotInTheMigrationQueue(
// 	page vm.Page,
// ) vm.Page {
// 	inQueue := false
// 	for _, t := range mmu.migrationQueue {
// 		if page.PAddr == t.page.PAddr {
// 			inQueue = true
// 			break
// 		}
// 	}

// 	if !inQueue {
// 		page.IsMigrating = false
// 		mmu.pageTable.Update(page)
// 		return page
// 	}

// 	return page
// }

// func (mmu *MMU) sendTranlationRsp(
// 	now sim.VTimeInSec,
// 	trans transaction,
// ) (madeProgress bool) {
// 	req := trans.req
// 	page := trans.page

// 	rsp := vm.TranslationRspBuilder{}.
// 		WithSendTime(now).
// 		WithSrc(mmu.topPort).
// 		WithDst(req.OriginPort).
// 		WithRspTo(req.ID).
// 		WithPage(page).
// 		Build()

// 	if !mmu.topSender.CanSend(1) {
// 		return false
// 	}

// 	mmu.topSender.Send(rsp)

// 	return true
// }

// func (mmu *MMU) processMigrationReturn(now sim.VTimeInSec) bool {
// 	item := mmu.migrationPort.Peek()
// 	if item == nil {
// 		return false
// 	}

// 	if !mmu.topSender.CanSend(1) {
// 		return false
// 	}

// 	req := mmu.currentOnDemandMigration.req
// 	page, found := mmu.pageTable.Find(req.PID, req.VAddr)
// 	if !found {
// 		panic("page not found")
// 	}

// 	rsp := vm.TranslationRspBuilder{}.
// 		WithSendTime(now).
// 		WithSrc(mmu.topPort).
// 		WithDst(req.OriginPort).
// 		WithRspTo(req.ID).
// 		WithPage(page).
// 		Build()
// 	mmu.topSender.Send(rsp)

// 	mmu.isDoingMigration = false

// 	page = mmu.markPageAsNotMigratingIfNotInTheMigrationQueue(page)
// 	page.IsPinned = true
// 	mmu.pageTable.Update(page)

// 	// fmt.Printf("%f, MigrationRsp %d, to %s\n", now, page.VAddr, req.OriginPort.Name())

// 	mmu.migrationPort.Retrieve(now)

// 	return true
// }

// func (mmu *MMU) parseFromTop(now sim.VTimeInSec) bool {
// 	madeProgress := false

// 	if mmu.topPort.Peek() == nil {
// 		return false
// 	}

// 	for len(mmu.PWqueue) < 64 {
// 		req := mmu.topPort.Peek()
// 		if req == nil {
// 			break
// 		}

// 		switch req := req.(type) {
// 		case *vm.TranslationReq:
// 			if !mmu.visitTranslatedTable(req, now) || req.NeedTranslate {
// 				mmu.PWqueue = append(mmu.PWqueue, req)

// 				fmt.Printf("%f, AddToPWQueue %s, %d, %t %d\n", now, req.Src.Name(), req.VAddr, req.NeedTranslate, req.PID)
// 				mmu.topPort.Retrieve(now)
// 			}
// 		default:
// 			log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
// 		}

// 		tracing.TraceReqReceive(req, mmu)

// 		madeProgress = true
// 	}

// 	return madeProgress
// }

// func (mmu *MMU) processTranslationReqs(now sim.VTimeInSec) bool {
// 	madeProgress := false

// 	for i := 0; i < len(mmu.PWqueue); i++ {
// 		if mmu.PWqueue[i] == nil {
// 			break
// 		}

// 		if mmu.inWalkingTransaction(mmu.PWqueue[i].(*vm.TranslationReq).VAddr,
// 			mmu.PWqueue[i].(*vm.TranslationReq).PID) {
// 			continue
// 		}

// 		if len(mmu.walkingTranslations) > mmu.maxRequestsInFlight {
// 			break
// 		}

// 		// if mmu.visitTranslatedTable(mmu.PWqueue[i].(*vm.TranslationReq), now) {
// 		// 	continue
// 		// }

// 		req := mmu.PWqueue[i]
// 		mmu.PWqueue = append(mmu.PWqueue[:i], mmu.PWqueue[i+1:]...)

// 		tracing.TraceReqReceive(req, mmu)

// 		switch req := req.(type) {
// 		case *vm.TranslationReq:
// 			// fmt.Printf("%d\n", req.VAddr)
// 			mmu.startWalking(req)
// 		default:
// 			// log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
// 		}

// 		madeProgress = true
// 	}
// 	return madeProgress
// }

// func (mmu *MMU) startWalking(req *vm.TranslationReq) {
// 	translationInPipeline := transaction{
// 		req:       req,
// 		cycleLeft: mmu.latency,
// 	}

// 	mmu.walkingTranslations = append(mmu.walkingTranslations, translationInPipeline)
// }

// func (mmu *MMU) toRemove(index int) bool {
// 	for i := 0; i < len(mmu.toRemoveFromPTW); i++ {
// 		remove := mmu.toRemoveFromPTW[i]
// 		if remove == index {
// 			return true
// 		}
// 	}
// 	return false
// }

// func unique(intSlice []uint64) []uint64 {
// 	keys := make(map[int]bool)
// 	list := []uint64{}
// 	for _, entry := range intSlice {
// 		if _, value := keys[int(entry)]; !value {
// 			keys[int(entry)] = true
// 			list = append(list, entry)
// 		}
// 	}
// 	return list
// }

// func (mmu *MMU) sendToGMMU(now sim.VTimeInSec, walking transaction, inInnerLayer bool) bool {
// 	madeProgress := false

// 	for i := 0; i < 4; i++ {
// 		pfn := walking.req.VAddr >> 12
// 		newPFN := pfn + uint64(i)
// 		newVAddr := newPFN << 12

// 		page, found := mmu.pageTable.Find(walking.req.PID, newVAddr)
// 		if !found {
// 			continue
// 		}

// 		if mmu.isInTranslatedTable(newVAddr, page.PID) {
// 			continue
// 		}

// 		mmu.revisitAndRspPWQueue(now, newVAddr, page)

// 		gpuInnerLayerID := mmu.getNextLayerGPUID(newVAddr, uint64(len(mmu.InnerLoop)))
// 		gpuID := mmu.InnerLoop[gpuInnerLayerID]
// 		port := mmu.GMMUCacheTable.Find(gpuID)
// 		if port == walking.req.Src {
// 			continue
// 		}
// 		taskID := sim.GetIDGenerator().Generate()
// 		pageLoadMsg := vm.PageLoadMsgBuilder{}.
// 			WithSendTime(now).
// 			WithSrc(mmu.topPort).
// 			WithDst(mmu.GMMUCacheTable.Find(gpuID)).
// 			WithPage(page).
// 			WithTaskID(taskID).
// 			Build()

// 		if !mmu.topSender.CanSend(1) {
// 			return false
// 		}
// 		mmu.updateTranslatedTable(page.VAddr, page.PID, mmu.GMMUCacheTable.Find(gpuID), false)
// 		mmu.topSender.Send(pageLoadMsg)
// 		madeProgress = true

// 		gpuMiddleLayerID := mmu.getNextLayerGPUID(newVAddr, uint64(len(mmu.MiddleLoop)))
// 		gpuID = mmu.MiddleLoop[gpuMiddleLayerID]
// 		taskID = sim.GetIDGenerator().Generate()
// 		port = mmu.GMMUCacheTable.Find(gpuID)
// 		if port == walking.req.Src {
// 			continue
// 		}
// 		pageLoadMsg = vm.PageLoadMsgBuilder{}.
// 			WithSendTime(now).
// 			WithSrc(mmu.topPort).
// 			WithDst(mmu.GMMUCacheTable.Find(gpuID)).
// 			WithPage(page).
// 			WithTaskID(taskID).
// 			Build()

// 		if !mmu.topSender.CanSend(1) {
// 			return false
// 		}

// 		mmu.topSender.Send(pageLoadMsg)
// 		madeProgress = true

// 	}
// 	return madeProgress
// }

// func (mmu *MMU) getNextLayerGPUID(vaddr uint64, totalID uint64) uint64 {
// 	// vpn := vaddr >> 12
// 	// gpuid := mmu.GetGPUIndex(int(vpn), int(totalID))
// 	// return uint64(gpuid)
// 	return vaddr >> 12 % totalID
// }

// // func (mmu *MMU) GetGPUIndex(vpn, N int) int {
// // 	base := (vpn % 4) * (N / 4) // 基地址部分
// // 	// hash := hashFunction(vpn)   // 哈希值
// // 	// offset := vpn % (N / 4)
// // 	offset := (vpn / 4) % (N / 4) // 组内偏移部分
// // 	return base + offset
// // }

// func (mmu *MMU) updateTranslatedTable(vaddr uint64, pid vm.PID, innerLayerPort sim.Port, predict bool) bool {
// 	_, found, _ := mmu.traverseTranslatedTable(vaddr, pid) // Assuming PID is not used here, adjust as necessary

// 	if found {
// 		for i := 0; i < len(mmu.translatedTable); i++ {
// 			if mmu.translatedTable[i].vaddr == vaddr && mmu.translatedTable[i].pid == pid {
// 				mmu.translatedTable[i].innerLayerPort = innerLayerPort
// 				mmu.translatedTable[i].pid = pid
// 				tempEntry := mmu.translatedTable[i]
// 				oldTable := append(mmu.translatedTable[:i], mmu.translatedTable[i+1:]...)
// 				mmu.translatedTable = oldTable
// 				mmu.translatedTable = append(mmu.translatedTable, tempEntry)
// 				return true
// 			}
// 		}
// 	}

// 	if len(mmu.translatedTable) >= 512 {
// 		// mmu.kickoutMinRepeats()
// 		mmu.translatedTable = mmu.translatedTable[1:]
// 	}

// 	translatedTableEntry := translatedTableEntry{
// 		vaddr:          vaddr,
// 		innerLayerPort: innerLayerPort,
// 		predict:        predict,
// 		pid:            pid,
// 		repectTimes:    1,
// 	}

// 	mmu.translatedTable = append(mmu.translatedTable, translatedTableEntry)
// 	return true
// }

// func (mmu *MMU) traverseTranslatedTable(vaddr uint64, pid vm.PID) (sim.Port, bool, bool) {
// 	for i := 0; i < len(mmu.translatedTable); i++ {
// 		if mmu.translatedTable[i].vaddr == vaddr && mmu.translatedTable[i].pid == pid {
// 			return mmu.translatedTable[i].innerLayerPort, true, mmu.translatedTable[i].predict
// 		}
// 	}
// 	return nil, false, false
// }

// func (mmu *MMU) visitTranslatedTable(req *vm.TranslationReq, now sim.VTimeInSec) bool {
// 	if req == nil {
// 		return false
// 	}

// 	vaddr := req.VAddr
// 	pid := req.PID

// 	dstPort, found, _ := mmu.traverseTranslatedTable(vaddr, pid)

// 	if !found {
// 		return false
// 	}

// 	if req.NeedTranslate {
// 		return true
// 	}

// 	// fmt.Printf("VisitTranslatedTable %s, %d\n", dstPort.Name(), req.VAddr)

// 	if dstPort == req.OriginPort {
// 		newReq := vm.TranslationReqBuilder{}.
// 			WithSendTime(now).
// 			WithSrc(req.OriginPort).
// 			WithDst(dstPort).
// 			WithVAddr(vaddr).
// 			WithPID(req.PID).
// 			WithDeviceID(req.DeviceID).
// 			WithOriginPort(req.OriginPort).
// 			WithTaskID(req.TaskID).
// 			WithNeedTranslate(true).
// 			Build()

// 		newReq.Hops = mmu.GetHops(req)
// 		newReq.StartGPUID = int(0)
// 		fmt.Printf("%f, VisitTranslatedTable, %s, %s, %d, %t %d\n", now, req.OriginPort.Name(), dstPort.Name(), newReq.VAddr, req.NeedTranslate, req.PID)

// 		err := mmu.topPort.Send(newReq)
// 		if err != nil {
// 			return false
// 		}
// 		mmu.topPort.Retrieve(now)
// 		return true
// 	} else {

// 		// fmt.Printf("%f, VisitTranslatedTable %s, %d, %t\n", now, dstPort.Name(), req.VAddr, predict)

// 		newReq := vm.TranslationReqBuilder{}.
// 			WithSendTime(now).
// 			WithSrc(req.OriginPort).
// 			WithDst(dstPort).
// 			WithVAddr(vaddr).
// 			WithPID(req.PID).
// 			WithDeviceID(req.DeviceID).
// 			WithOriginPort(req.OriginPort).
// 			WithTaskID(req.TaskID).
// 			WithNeedTranslate(true).
// 			Build()

// 		newReq.Hops = mmu.GetHops(req)
// 		newReq.StartGPUID = int(0)

// 		err := mmu.topPort.Send(newReq)
// 		if err != nil {
// 			return false
// 		}
// 		fmt.Printf("%f, VisitTranslatedTable %s, %s, %d, %t %d\n", now, req.Src.Name(), dstPort.Name(), req.VAddr, req.NeedTranslate, req.PID)

// 		mmu.topPort.Retrieve(now)
// 		return true
// 	}
// }

// func (mmu *MMU) revisitPWQueue(now sim.VTimeInSec, vaddr uint64, page vm.Page) bool {
// 	madeProgress := true

// 	// port, found, _ := mmu.traverseTranslatedTable(vaddr)
// 	// if !found {
// 	// 	return false
// 	// }

// 	newQueue := make([]sim.Msg, 0)

// 	for i := 0; i < len(mmu.PWqueue); i++ {
// 		req := mmu.PWqueue[i]
// 		if req == nil {
// 			break
// 		}

// 		if req.(*vm.TranslationReq).VAddr == vaddr && req.(*vm.TranslationReq).PID == page.PID {
// 			newRsp := vm.TranslationRspBuilder{}.
// 				WithSendTime(now).
// 				WithSrc(mmu.topPort).
// 				WithDst(req.(*vm.TranslationReq).OriginPort).
// 				WithRspTo(req.(*vm.TranslationReq).ID).
// 				WithPage(page).
// 				WithTaskID(req.(*vm.TranslationReq).TaskID).
// 				WithOriginPort(req.(*vm.TranslationReq).OriginPort).
// 				Build()

// 			fmt.Printf("%f, RevisitPWQueue %s, %d, %t %d\n", now, req.(*vm.TranslationReq).OriginPort.Name(), req.(*vm.TranslationReq).VAddr, req.(*vm.TranslationReq).NeedTranslate, req.(*vm.TranslationReq).PID)
// 			Hop := mmu.GetHops(req.(*vm.TranslationReq))

// 			fmt.Printf("Hops %d\n", Hop)

// 			err := mmu.topPort.Send(newRsp)
// 			if err != nil {
// 				newQueue = append(newQueue, req)
// 			}
// 			// newQueue = append(mmu.PWqueue[:i], mmu.PWqueue[i+1:]...)

// 			madeProgress = true
// 			// fmt.Printf("%f, VisitPWQueue %s, %d\n", now, req.(*vm.TranslationReq).OriginPort.Name(), req.(*vm.TranslationReq).VAddr)
// 		} else {
// 			newQueue = append(newQueue, req)
// 		}
// 	}
// 	mmu.PWqueue = newQueue
// 	return madeProgress
// }

// func (mmu *MMU) revisitAndRspPWQueue(now sim.VTimeInSec, vaddr uint64, page vm.Page) bool {
// 	madeProgress := false

// 	newQueue := make([]sim.Msg, 0)

// 	for i := 0; i < len(mmu.PWqueue); i++ {
// 		req := mmu.PWqueue[i]
// 		if req == nil {
// 			break
// 		}

// 		if req.(*vm.TranslationReq).VAddr == vaddr && req.(*vm.TranslationReq).PID == page.PID {
// 			// fmt.Printf("Predict Hit\n")
// 			newRsp := vm.TranslationRspBuilder{}.
// 				WithSendTime(now).
// 				WithSrc(mmu.topPort).
// 				WithDst(req.(*vm.TranslationReq).OriginPort).
// 				WithRspTo(req.(*vm.TranslationReq).ID).
// 				WithPage(page).
// 				WithTaskID(req.(*vm.TranslationReq).TaskID).
// 				Build()

// 			fmt.Printf("%f, RevisitAndRspPWQueue %s, %d, %t %d\n", now, req.(*vm.TranslationReq).OriginPort.Name(), req.(*vm.TranslationReq).VAddr, req.(*vm.TranslationReq).NeedTranslate, req.(*vm.TranslationReq).PID)

// 			Hop := mmu.GetHops(req.(*vm.TranslationReq))
// 			fmt.Printf("Hops %d\n", Hop)

// 			err := mmu.topPort.Send(newRsp)
// 			if err != nil {
// 				newQueue = append(newQueue, req)
// 			}
// 			madeProgress = true
// 		} else {
// 			newQueue = append(newQueue, req)
// 		}
// 	}
// 	mmu.PWqueue = newQueue
// 	return madeProgress
// }

// func (mmu *MMU) isInTranslatedTable(vaddr uint64, pid vm.PID) bool {
// 	for i := 0; i < len(mmu.translatedTable); i++ {
// 		if mmu.translatedTable[i].vaddr == vaddr && pid == mmu.translatedTable[i].pid {
// 			return true
// 		}
// 	}
// 	return false
// }

// func (mmu *MMU) portInInLoopAndMidLoop(port sim.Port) bool {
// 	for i := 0; i < len(mmu.InnerLoop); i++ {
// 		gpuID := mmu.InnerLoop[uint64(i)]
// 		Inport := mmu.GMMUCacheTable.Find(gpuID)
// 		if Inport == port {
// 			return true
// 		}
// 	}
// 	for i := 0; i < len(mmu.MiddleLoop); i++ {
// 		gpuID := mmu.MiddleLoop[uint64(i)]
// 		Inport := mmu.GMMUCacheTable.Find(gpuID)
// 		if Inport == port {
// 			return true
// 		}
// 	}
// 	return false
// }

// func (mmu *MMU) inWalkingTransaction(vaddr uint64, pid vm.PID) bool {
// 	for i := 0; i < len(mmu.walkingTranslations); i++ {
// 		if mmu.walkingTranslations[i].req.VAddr == vaddr && mmu.walkingTranslations[i].req.PID == pid {
// 			return true
// 		}
// 	}
// 	return false
// }

// func getCoordinates(id int) (int, int) {
// 	if id == 0 {
// 		return 3, 3 // 特殊处理 0 的坐标
// 	}

// 	if id >= 25 {
// 		id = id + 1
// 	}

// 	row := (id - 1) / 7
// 	col := (id - 1) % 7
// 	return row, col
// }

// func calculateDistance(id1, id2 int) float64 {
// 	x1, y1 := getCoordinates(id1)
// 	x2, y2 := getCoordinates(id2)

// 	// 计算曼哈顿距离
// 	distance := math.Abs(float64(x1-x2)) + math.Abs(float64(y1-y2))

// 	return distance
// }

// func (mmu *MMU) GetHops(req *vm.TranslationReq) int {
// 	hops := req.Hops

// 	Distance := calculateDistance(int(0), int(req.StartGPUID))

// 	return hops + int(Distance)
// }
