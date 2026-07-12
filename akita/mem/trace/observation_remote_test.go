package trace

import (
	"compress/gzip"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func TestObservationRemoteTraceStreamsLogicalRequests(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "observation")
	err := EnableObservationRemoteTrace(ObservationRemoteConfig{
		Prefix: prefix, WarmupRequests: 1, MaxRequests: 2,
		L2SampleMax: 2, TileWidth: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "warmup", PID: 7, Operation: "read",
		Address: 0x1000, ByteSize: 64,
		RequesterName: "GPU[0].RDMA", OwnerName: "GPU[1].RDMA",
		ArrivalTime: remoteTestNS(1),
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "warmup", IssueTime: remoteTestNS(1),
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "warmup", CompletionTime: remoteTestNS(2),
	})

	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "write", PID: 7, Operation: "store",
		Address: 0x101c, ByteSize: 8,
		RequesterName: "GPU[1].SA[0].RDMA",
		OwnerName:     "GPU[6].RDMA",
		ArrivalTime:   remoteTestNS(10),
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "write", IssueTime: remoteTestNS(12),
		ForwardWireID: "fwd-write", ForwardTrafficBytes: 24,
	})
	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "read", PID: 7, Operation: "READ",
		Address: 0x1030, ByteSize: 16,
		RequesterName: "GPU[1].SA[1].RDMA",
		OwnerName:     "GPU[6].RDMA",
		ArrivalTime:   remoteTestNS(11),
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "read", IssueTime: remoteTestNS(13),
		ForwardWireID: "fwd-read", ForwardTrafficBytes: 24,
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "read", CompletionTime: remoteTestNS(18),
		ReturnWireID: "ret-read", ReturnTrafficBytes: 80,
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "write", CompletionTime: remoteTestNS(20),
		ReturnWireID: "ret-write", ReturnTrafficBytes: 16,
	})

	// The measured window is full. This request and its later completion are
	// intentionally ignored without allocating another request record.
	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "after-max", PID: 7, Operation: "read",
		Address: 0x2000, ByteSize: 4,
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "after-max", IssueTime: remoteTestNS(20),
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "after-max", CompletionTime: remoteTestNS(21),
	})

	RecordObservationL2Sample(
		"GPU[6].L2[0]", remoteTestNS(14), 32, 64, 4, 2, 3)
	RecordObservationL2Sample(
		"GPU[6].L2[0]", remoteTestNS(15), 16, 64, 2, 1, 1)
	RecordObservationL2Sample(
		"GPU[6].L2[0]", remoteTestNS(16), 8, 64, 1, 0, 0)

	if err := DumpObservationRemoteTrace(); err != nil {
		t.Fatal(err)
	}

	remoteRows := readRemoteGZIP(t, prefix+"_remote_requests.csv.gz")
	if len(remoteRows) != 3 {
		t.Fatalf("got %d remote rows, want header + 2", len(remoteRows))
	}
	byID := make(map[string]map[string]string)
	for _, values := range remoteRows[1:] {
		row := remoteCSVMap(remoteRows[0], values)
		byID[row["logical_request_id"]] = row
	}
	read := byID["read"]
	if read["status"] != "complete" || read["write_epoch"] != "1" ||
		read["same_line_inflight_at_arrival"] != "0" {
		t.Fatalf("missing reuse metadata: %#v", read)
	}
	if read["requester_gpu"] != "1" || read["owner_gpu"] != "6" ||
		read["manhattan_hops"] != "2" {
		t.Fatalf("bad topology metadata: %#v", read)
	}
	if read["queue_wait_ps"] != "2000" || read["service_ps"] != "5000" ||
		read["total_ps"] != "7000" {
		t.Fatalf("bad latency decomposition: %#v", read)
	}
	if read["forward_wire_id"] != "fwd-read" ||
		read["return_wire_id"] != "ret-read" ||
		read["total_traffic_bytes"] != "104" {
		t.Fatalf("bad physical traffic metadata: %#v", read)
	}
	if byID["write"]["write_epoch"] != "1" {
		t.Fatalf("write did not advance its line epoch: %#v", byID["write"])
	}

	l2Rows := readRemoteGZIP(t, prefix+"_l2_utilization.csv.gz")
	if len(l2Rows) != 3 {
		t.Fatalf("got %d L2 rows, want header + bounded 2", len(l2Rows))
	}
	l2 := remoteCSVMap(l2Rows[0], l2Rows[1])
	if l2["occupancy_ppm"] != "500000" || l2["free_blocks"] != "32" ||
		l2["gpu_id"] != "6" || l2["status"] != "complete" {
		t.Fatalf("unexpected L2 sample: %#v", l2)
	}

	summary := remoteMetricFile(t, prefix+"_remote_summary.csv")
	if summary["measured_requests"] != "2" ||
		summary["same_line_inflight_arrivals"] != "0" ||
		summary["total_traffic_bytes"] != "144" ||
		summary["l2_samples_written"] != "2" {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	validation := remoteMetricFile(t, prefix+"_remote_validation.csv")
	if validation["l2_samples_dropped"] != "1" ||
		validation["dropped_after_max"] != "1" ||
		validation["ignored_issues_after_max"] != "1" ||
		validation["ignored_completions_after_max"] != "1" {
		t.Fatalf("unexpected validation counts: %#v", validation)
	}

	if len(globalObservationRemoteStats.active) != 0 ||
		len(globalObservationRemoteStats.inflightByLine) != 0 ||
		len(globalObservationRemoteStats.writeEpoch) != 0 {
		t.Fatal("dump did not release observation state")
	}
}

func TestObservationRemoteInflightUsesArrivalReadRequesterAndEpoch(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "arrival-exact")
	if err := EnableObservationRemoteTrace(ObservationRemoteConfig{
		Prefix: prefix, MaxRequests: 7, L2SampleMax: 1, TileWidth: 4,
	}); err != nil {
		t.Fatal(err)
	}

	starts := []ObservationRemoteRequestStart{
		{
			LogicalRequestID: "leader", PID: 9, Operation: "read",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[0].RDMA", OwnerName: "GPU[6].RDMA",
			ArrivalTime: remoteTestNS(1),
		},
		{
			LogicalRequestID: "arrival-follower", PID: 9, Operation: "read",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[0].RDMA", OwnerName: "GPU[6].RDMA",
			ArrivalTime: remoteTestNS(2),
		},
		{
			LogicalRequestID: "other-requester", PID: 9, Operation: "read",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[1].RDMA", OwnerName: "GPU[6].RDMA",
			ArrivalTime: remoteTestNS(3),
		},
		{
			LogicalRequestID: "write", PID: 9, Operation: "write",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[0].RDMA", OwnerName: "GPU[6].RDMA",
			ArrivalTime: remoteTestNS(4),
		},
		{
			LogicalRequestID: "post-write", PID: 9, Operation: "read",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[0].RDMA", OwnerName: "GPU[6].RDMA",
			ArrivalTime: remoteTestNS(5),
		},
		{
			LogicalRequestID: "post-write-follower", PID: 9, Operation: "load",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[0].RDMA", OwnerName: "GPU[6].RDMA",
			ArrivalTime: remoteTestNS(6),
		},
		{
			LogicalRequestID: "other-owner", PID: 9, Operation: "read",
			Address: 0x8000, ByteSize: 64,
			RequesterName: "GPU[0].RDMA", OwnerName: "GPU[7].RDMA",
			ArrivalTime: remoteTestNS(7),
		},
	}
	for _, start := range starts {
		StartRemoteRequest(start)
	}

	// Deliberately issue in reverse arrival order. The overlap values must
	// remain those captured above at admission, not follow this issue order.
	for i := len(starts) - 1; i >= 0; i-- {
		IssueRemoteRequest(ObservationRemoteRequestIssue{
			LogicalRequestID:    starts[i].LogicalRequestID,
			IssueTime:           remoteTestNS(float64(20 + len(starts) - i)),
			ForwardWireID:       "wire-" + starts[i].LogicalRequestID,
			ForwardTrafficBytes: 16,
		})
	}
	for i, start := range starts {
		CompleteRemoteRequest(ObservationRemoteRequestCompletion{
			LogicalRequestID:   start.LogicalRequestID,
			CompletionTime:     remoteTestNS(float64(40 + i)),
			ReturnTrafficBytes: 80,
		})
	}
	if err := DumpObservationRemoteTrace(); err != nil {
		t.Fatal(err)
	}

	rows := readRemoteGZIP(t, prefix+"_remote_requests.csv.gz")
	byID := make(map[string]map[string]string)
	for _, values := range rows[1:] {
		row := remoteCSVMap(rows[0], values)
		byID[row["logical_request_id"]] = row
	}
	wants := map[string]string{
		"leader": "0", "arrival-follower": "1", "other-requester": "0",
		"write": "0", "post-write": "0", "post-write-follower": "1",
		"other-owner": "0",
	}
	for id, want := range wants {
		if got := byID[id]["same_line_inflight_at_arrival"]; got != want {
			t.Errorf("%s inflight-at-arrival = %s, want %s: %#v",
				id, got, want, byID[id])
		}
	}
	if byID["leader"]["issue_ps"] != "27000" ||
		byID["arrival-follower"]["issue_ps"] != "26000" {
		t.Fatalf("reverse issue metadata was not preserved: leader=%#v follower=%#v",
			byID["leader"], byID["arrival-follower"])
	}
	if byID["post-write"]["write_epoch"] != "1" ||
		byID["other-requester"]["write_epoch"] != "0" {
		t.Fatalf("write epoch was not global and arrival ordered: %#v",
			byID)
	}
	summary := remoteMetricFile(t, prefix+"_remote_summary.csv")
	if summary["same_line_inflight_arrivals"] != "2" {
		t.Fatalf("unexpected exact-overlap summary: %#v", summary)
	}
	validation := remoteMetricFile(t, prefix+"_remote_validation.csv")
	for _, metric := range []string{
		"duplicate_starts", "duplicate_issues", "unknown_issues",
		"completions_before_issue", "time_regressions", "inflight_underflows",
	} {
		if validation[metric] != "0" {
			t.Fatalf("%s = %s, want 0: %#v", metric, validation[metric], validation)
		}
	}
}

func TestObservationRemoteTraceDumpsIncompleteAndInvalidRows(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "incomplete")
	err := EnableObservationRemoteTrace(ObservationRemoteConfig{
		Prefix: prefix, MaxRequests: 3, L2SampleMax: 3, TileWidth: 8,
	})
	if err != nil {
		t.Fatal(err)
	}

	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "regression", PID: 1, Operation: "read",
		Address: 0x3000, ByteSize: 4,
		RequesterName: "GPU[0].RDMA", OwnerName: "GPU[8].RDMA",
		ArrivalTime: remoteTestNS(5),
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "regression", IssueTime: remoteTestNS(4),
	})
	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "pending", PID: 1, Operation: "read",
		Address: 0x3008, ByteSize: 4,
		RequesterName: "GPU[0].RDMA", OwnerName: "GPU[8].RDMA",
		ArrivalTime: remoteTestNS(6),
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "pending", IssueTime: remoteTestNS(6),
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "regression", CompletionTime: remoteTestNS(3),
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "unknown", CompletionTime: remoteTestNS(7),
	})
	RecordObservationL2Sample(
		"not-a-gpu.L2[0]", remoteTestNS(7), 11, 10, 12, -1, -1)

	if err := DumpObservationRemoteTrace(); err != nil {
		t.Fatal(err)
	}
	rows := readRemoteGZIP(t, prefix+"_remote_requests.csv.gz")
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want two requests", len(rows)-1)
	}
	byID := make(map[string]map[string]string)
	for _, values := range rows[1:] {
		row := remoteCSVMap(rows[0], values)
		byID[row["logical_request_id"]] = row
	}
	if byID["regression"]["time_regression"] != "true" ||
		byID["regression"]["total_ps"] != "" {
		t.Fatalf("regressed timestamps were treated as latency: %#v",
			byID["regression"])
	}
	if byID["pending"]["status"] != "incomplete" ||
		byID["pending"]["completion_ps"] != "" {
		t.Fatalf("incomplete request is not explicit: %#v", byID["pending"])
	}
	l2Rows := readRemoteGZIP(t, prefix+"_l2_utilization.csv.gz")
	l2 := remoteCSVMap(l2Rows[0], l2Rows[1])
	if l2["status"] != "invalid" || l2["occupancy_ppm"] != "1100000" {
		t.Fatalf("invalid L2 sample was hidden: %#v", l2)
	}
	validation := remoteMetricFile(t, prefix+"_remote_validation.csv")
	if validation["time_regressions"] != "1" ||
		validation["unknown_completions"] != "1" ||
		validation["incomplete_at_dump"] != "1" ||
		validation["l2_invalid_samples"] != "1" {
		t.Fatalf("validation failed to expose bad events: %#v", validation)
	}
}

func TestFreezeObservationRemoteTraceDrainsOnlyAdmittedRequests(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "freeze")
	if err := EnableObservationRemoteTrace(ObservationRemoteConfig{
		Prefix: prefix, MaxRequests: 4, L2SampleMax: 4, TileWidth: 4,
	}); err != nil {
		t.Fatal(err)
	}
	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "admitted", PID: 1, Operation: "read",
		Address: 0x4000, ByteSize: 64,
		RequesterName: "GPU[0].RDMA", OwnerName: "GPU[1].RDMA",
		ArrivalTime: remoteTestNS(1),
	})
	drained := FreezeObservationRemoteTrace()
	select {
	case <-drained:
		t.Fatal("freeze reported drained while an admitted request was active")
	default:
	}
	if ObservationRemoteTraceEnabled() {
		t.Fatal("frozen observer still advertises request admission")
	}
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "admitted", IssueTime: remoteTestNS(2),
		ForwardWireID: "admitted-wire", ForwardTrafficBytes: 16,
	})

	StartRemoteRequest(ObservationRemoteRequestStart{
		LogicalRequestID: "after-freeze", PID: 1, Operation: "read",
		Address: 0x4040, ByteSize: 64,
	})
	IssueRemoteRequest(ObservationRemoteRequestIssue{
		LogicalRequestID: "after-freeze", IssueTime: remoteTestNS(2),
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "after-freeze", CompletionTime: remoteTestNS(3),
	})
	CompleteRemoteRequest(ObservationRemoteRequestCompletion{
		LogicalRequestID: "admitted", CompletionTime: remoteTestNS(4),
	})
	select {
	case <-drained:
	default:
		t.Fatal("drain channel did not close after the admitted tail completed")
	}
	if err := DumpObservationRemoteTrace(); err != nil {
		t.Fatal(err)
	}
	rows := readRemoteGZIP(t, prefix+"_remote_requests.csv.gz")
	if len(rows) != 2 {
		t.Fatalf("got %d frozen-tail rows, want 1", len(rows)-1)
	}
	admitted := remoteCSVMap(rows[0], rows[1])
	if admitted["logical_request_id"] != "admitted" ||
		admitted["issue_ps"] != "2000" ||
		admitted["forward_wire_id"] != "admitted-wire" ||
		admitted["forward_traffic_bytes"] != "16" {
		t.Fatalf("post-freeze issue update was lost: %#v", admitted)
	}
	validation := remoteMetricFile(t, prefix+"_remote_validation.csv")
	if validation["unknown_completions"] != "0" ||
		validation["ignored_issues_after_max"] != "1" ||
		validation["ignored_completions_after_max"] != "1" ||
		validation["incomplete_at_dump"] != "0" {
		t.Fatalf("unexpected frozen-tail validation: %#v", validation)
	}
}

func remoteTestNS(value float64) sim.VTimeInSec {
	return sim.VTimeInSec(value * 1e-9)
}

func readRemoteGZIP(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	rows, err := csv.NewReader(gz).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func remoteCSVMap(header, values []string) map[string]string {
	row := make(map[string]string, len(header))
	for i, key := range header {
		if i < len(values) {
			row[key] = values[i]
		}
	}
	return row
}

func remoteMetricFile(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]string)
	for _, row := range rows[1:] {
		if len(row) >= 2 {
			metrics[row[0]] = row[1]
		}
	}
	return metrics
}
