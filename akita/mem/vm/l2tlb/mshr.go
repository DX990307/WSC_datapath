package l2tlb

import (
	"log"

	"github.com/sarchlab/akita/v3/mem/vm"
)

type mshrEntry struct {
	pid         vm.PID
	vAddr       uint64
	Requests    []*vm.TranslationReq
	reqToBottom *vm.TranslationReq
	page        vm.Page
}

func newMSHREntry() *mshrEntry {
	return &mshrEntry{}
}

type mshr interface {
	Query(pid vm.PID, addr uint64) *mshrEntry
	Add(pid vm.PID, addr uint64) *mshrEntry
	Remove(pid vm.PID, addr uint64) *mshrEntry
	AllEntries() []*mshrEntry
	IsFull() bool
	IsEntryFull(pid vm.PID, vAddr uint64) bool
	Reset()
	GetEntry(pid vm.PID, vAddr uint64) *mshrEntry
	IsEntryPresent(pid vm.PID, vAddr uint64) bool
	PrintStats() (uint64, uint64, uint64)
	UpdatePage(pid vm.PID, vAddr uint64, page vm.Page) bool
}

type mshrImpl struct {
	capacity   int
	entryDepth int
	entries    []*mshrEntry
}

func newMSHR(capacity int, entryDepth int) mshr {
	m := new(mshrImpl)
	m.capacity = capacity
	m.entryDepth = entryDepth
	return m
}

func (m *mshrImpl) Add(pid vm.PID, vAddr uint64) *mshrEntry {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			panic("entry already in mshr")
		}
	}

	if len(m.entries) >= m.capacity {
		log.Panic("MSHR is full")
	}

	entry := newMSHREntry()
	entry.pid = pid
	entry.vAddr = vAddr
	m.entries = append(m.entries, entry)
	return entry
}

func (m *mshrImpl) Query(pid vm.PID, vAddr uint64) *mshrEntry {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			return e
		}
	}
	return nil
}

func (m *mshrImpl) Remove(pid vm.PID, vAddr uint64) *mshrEntry {
	for i, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			return e
		}
	}
	panic("trying to remove an non-exist entry")
}

func (m *mshrImpl) AllEntries() []*mshrEntry {
	return m.entries
}

func (m *mshrImpl) IsFull() bool {
	return len(m.entries) >= m.capacity
}

func (m *mshrImpl) IsEntryFull(pid vm.PID, vAddr uint64) bool {
	for _, e := range m.entries {
		if e.pid == pid && e.vAddr == vAddr {
			return len(e.Requests) >= m.entryDepth
		}
	}
	return false
}

func (m *mshrImpl) Reset() {
	m.entries = nil
}

func (m *mshrImpl) GetEntry(pid vm.PID, vAddr uint64) *mshrEntry {
	return m.Query(pid, vAddr)
}

func (m *mshrImpl) IsEntryPresent(pid vm.PID, vAddr uint64) bool {
	return m.Query(pid, vAddr) != nil
}

func (m *mshrImpl) PrintStats() (uint64, uint64, uint64) {
	var numEntries uint64
	var numReqs uint64
	var maxNumReqsPerEntry uint64

	for _, e := range m.entries {
		numEntries++
		numReqs += uint64(len(e.Requests))
		if uint64(len(e.Requests)) > maxNumReqsPerEntry {
			maxNumReqsPerEntry = uint64(len(e.Requests))
		}
	}

	return numEntries, numReqs, maxNumReqsPerEntry
}

func (m *mshrImpl) UpdatePage(pid vm.PID, vAddr uint64, page vm.Page) bool {
	entry := m.Query(pid, vAddr)
	if entry == nil {
		return false
	}

	entry.page = page
	return true
}
