package cu

import (
	"path/filepath"
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/timing/wavefront"
)

func TestVectorRequestCarriesWGOriginOnlyWhenAuditEnabled(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "origin")
	memtrace.EnableRemoteOriginTrace(prefix, 1, 4096)
	defer memtrace.DisableRemoteOriginTrace()

	packet := &kernels.HsaKernelDispatchPacket{
		GridSizeX: 64, GridSizeY: 2, GridSizeZ: 1,
		WorkgroupSizeX: 1, WorkgroupSizeY: 1, WorkgroupSizeZ: 1,
	}
	rawWG := kernels.NewWorkGroup()
	rawWG.Packet = packet
	rawWG.IDX, rawWG.IDY = 3, 1
	wg := wavefront.NewWorkGroup(rawWG, nil)
	wave := wavefront.NewWavefront(kernels.NewWavefront())
	wave.WG = wg
	wave.SetPID(vm.PID(9))
	read := mem.ReadReqBuilder{}.WithAddress(0x1000).WithByteSize(64).Build()

	attachWGOriginToVectorRequest(
		VectorMemAccessInfo{Read: read}, wave, 5)
	info, ok := memtrace.GetL2AccessInfo(read.Info)
	if !ok || !info.HasWGOrigin {
		t.Fatalf("request is missing WG origin: %+v", info)
	}
	if info.WGOrigin.RequesterGPU != 5 || info.WGOrigin.PID != 9 ||
		info.WGOrigin.FlattenedWGID != 67 {
		t.Fatalf("unexpected WG origin: %+v", info.WGOrigin)
	}
	scalar := mem.ReadReqBuilder{}.
		WithAddress(0x1800).WithByteSize(16).Build()
	attachWGOriginToScalarRequest(scalar, wave, 6)
	scalarInfo, ok := memtrace.GetL2AccessInfo(scalar.Info)
	if !ok || !scalarInfo.HasWGOrigin ||
		scalarInfo.WGOrigin.RequesterGPU != 6 ||
		scalarInfo.WGOrigin.FlattenedWGID != 67 {
		t.Fatalf("scalar request is missing WG origin: %+v", scalarInfo)
	}

	memtrace.DisableRemoteOriginTrace()
	readWithoutAudit := mem.ReadReqBuilder{}.
		WithAddress(0x2000).WithByteSize(64).Build()
	attachWGOriginToVectorRequest(
		VectorMemAccessInfo{Read: readWithoutAudit}, wave, 5)
	if _, ok := memtrace.GetL2AccessInfo(readWithoutAudit.Info); ok {
		t.Fatal("disabled audit must not add request metadata")
	}
}
