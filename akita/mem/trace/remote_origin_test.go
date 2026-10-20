package trace

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteOriginTraceRecordsWGObjectAndPageOwner(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "fir")
	EnableRemoteOriginTrace(prefix, 8, 4096)
	defer DisableRemoteOriginTrace()
	RegisterRemoteOriginObject(7, 0x1000, 0x1000, "input")

	info := WithWGOriginInfo(nil, WGOriginInfo{
		PID: 7, RequesterGPU: 2, FlattenedWGID: 19, WGX: 19,
	})
	info = WithL2AddressInfo(info, 0x1080, 0x5080)
	access, ok := GetL2AccessInfo(info)
	if !ok || !access.HasWGOrigin || access.WGOrigin.FlattenedWGID != 19 {
		t.Fatalf("WG origin was not preserved: %+v", access)
	}

	RecordRemoteOrigin(RemoteOriginRecord{
		RequestID: "r1", Operation: "read", ByteSize: 64,
		RequesterGPU: 2, OwnerGPU: 3, PID: 7, FlattenedWG: 19,
		WGX: 19, VAddr: 0x1080, PAddr: 0x5080,
		Requester: "GPU[2].L1VTLB", Owner: "GPU[3].PageOwner",
	})
	if err := DumpRemoteOriginTrace(); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(prefix + "_raw.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("raw rows = %d, want header plus one record", len(rows))
	}
	want := map[string]string{
		"requester_gpu":   "2",
		"owner_gpu":       "3",
		"page_owner_gpu":  "3",
		"is_remote":       "true",
		"flattened_wg_id": "19",
		"vaddr":           "4224",
		"paddr":           "20608",
		"page_paddr":      "20480",
		"object":          "input",
	}
	for column, value := range want {
		index := -1
		for i, header := range rows[0] {
			if header == column {
				index = i
				break
			}
		}
		if index < 0 {
			t.Fatalf("missing column %s", column)
		}
		if rows[1][index] != value {
			t.Fatalf("%s = %q, want %q", column, rows[1][index], value)
		}
	}
}

func TestRemoteOriginTraceRetainsFirstLateRemoteExample(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "late")
	EnableRemoteOriginTrace(prefix, 1, 4096)
	defer DisableRemoteOriginTrace()
	RecordRemoteOrigin(RemoteOriginRecord{
		RequestID: "local", Operation: "read", ByteSize: 64,
		RequesterGPU: 1, OwnerGPU: 1,
	})
	RecordRemoteOrigin(RemoteOriginRecord{
		RequestID: "late-remote", Operation: "read", ByteSize: 64,
		RequesterGPU: 1, OwnerGPU: 2,
	})
	if err := DumpRemoteOriginTrace(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(prefix + "_raw.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[2][1] != "late-remote" {
		t.Fatalf("late first remote was not retained: %v", rows)
	}
}
