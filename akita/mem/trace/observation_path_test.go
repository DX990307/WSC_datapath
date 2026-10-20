package trace

import (
	"compress/gzip"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func TestObservationPathStagesAreExclusiveAndStateIsReleased(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "trace")
	if err := EnableObservationTrace(prefix, 0, 10, false, nil); err != nil {
		t.Fatal(err)
	}

	StartObservationPath(ObservationPathStart{
		PathID: "path-1", CacheName: "GPU[1].L1V[2]", Address: 0x1000,
		ByteSize: 64, PID: 7, Operation: "read", StartTime: ns(1),
		Parents: []ObservationParent{
			{ID: "parent-a", Address: 0x1000, ByteSize: 4},
			{ID: "parent-b", Address: 0x1004, ByteSize: 4},
		},
	})
	LinkObservationRequest("path-1", "lower-1", "l1_bottom_read")
	ObservationTransition("path-1", "coalesce", "l1_directory_queue", ns(2))
	ObservationTransition("path-1", "directory", "l1_lookup", ns(3))
	MarkObservationL1Result("path-1", "read-hit")
	ObservationTransition("path-1", "lookup", "l1_bank", ns(4))
	ObservationTransition("path-1", "bank", "l1_fill_response", ns(5))
	ObservationParentResponded("path-1", "parent-a", ns(6))
	ObservationParentResponded("path-1", "parent-b", ns(7))

	if err := DumpObservationTrace(); err != nil {
		t.Fatal(err)
	}
	rows := readObservationGZIP(t, prefix+"_paths.csv.gz")
	if len(rows) != 2 {
		t.Fatalf("got %d CSV rows, want header + one path", len(rows))
	}
	row := csvMap(rows[0], rows[1])
	if row["path_id"] != "path-1" || row["parent_count"] != "2" {
		t.Fatalf("unexpected identity row: %#v", row)
	}
	if row["total_ps"] != "6000" || row["accounted_ps"] != "6000" ||
		row["residual_ps"] != "0" {
		t.Fatalf("non-exclusive accounting: total=%s accounted=%s residual=%s",
			row["total_ps"], row["accounted_ps"], row["residual_ps"])
	}
	if len(globalObservationStats.paths) != 0 ||
		len(globalObservationStats.messageToPaths) != 0 {
		t.Fatal("completed path aliases were not released")
	}
}

func TestObservationL1MSHRFollowerOwnsOnlyWait(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "follower")
	if err := EnableObservationTrace(prefix, 1, 1, false, nil); err != nil {
		t.Fatal(err)
	}
	StartObservationPath(ObservationPathStart{
		PathID: "leader", CacheName: "GPU[1].L1V[0]", Address: 0x2000,
		ByteSize: 64, Operation: "read", StartTime: 0,
		Parents: []ObservationParent{{ID: "leader-parent", Address: 0x2000, ByteSize: 4}},
	})
	LinkObservationRequest("leader", "leader-source", "l2_request")
	MarkObservationSource("leader-source", "l2", "GPU[1].L2[0]")
	MarkObservationRemote("leader-source")
	StartObservationPath(ObservationPathStart{
		PathID: "follower", CacheName: "GPU[1].L1V[0]", Address: 0x2000,
		ByteSize: 64, Operation: "read", StartTime: 0,
		Parents: []ObservationParent{{ID: "parent", Address: 0x2000, ByteSize: 4}},
	})
	ObservationTransition("follower", "coalesce", "l1_directory_queue", 0)
	ObservationTransition("follower", "directory", "l1_lookup", ns(1))
	MarkObservationL1MSHRFollower("follower", "leader", ns(2))
	ObservationTransition("follower", "wakeup", "l1_fill_response", ns(5))
	ObservationParentResponded("follower", "parent", ns(6))
	if err := DumpObservationTrace(); err != nil {
		t.Fatal(err)
	}

	rows := readObservationGZIP(t, prefix+"_paths.csv.gz")
	row := csvMap(rows[0], rows[1])
	if row["l1_role"] != "mshr_follower" || row["leader_path_id"] != "leader" {
		t.Fatalf("missing follower relationship: %#v", row)
	}
	if row["source"] != "l2" {
		t.Fatalf("follower did not inherit leader source: %#v", row)
	}
	if row["remote"] != "true" {
		t.Fatalf("follower did not inherit leader remote route: %#v", row)
	}
	if row["l1_mshr_wait_ps"] != "3000" {
		t.Fatalf("got MSHR wait %s ps, want 3000", row["l1_mshr_wait_ps"])
	}
	for _, stage := range []string{
		"l2_queue_ps", "l2_lookup_ps", "l2_to_dram_ps",
		"dram_queue_service_ps", "remote_request_network_ps",
	} {
		if row[stage] != "0" {
			t.Fatalf("follower incorrectly owns %s=%s", stage, row[stage])
		}
	}
	validation := readObservationCSV(t, prefix+"_validation.csv")
	for _, values := range validation[1:] {
		item := csvMap(validation[0], values)
		if item["invariant"] == "remote_path_missing_network" &&
			item["value"] != "0" {
			t.Fatalf("remote L1 follower incorrectly required network stages: %#v", item)
		}
	}
}

func TestObservationLocalL2FollowerDoesNotInheritRemoteLeaderRoute(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "l2-local-follower")
	if err := EnableObservationTrace(prefix, 0, 2, false, nil); err != nil {
		t.Fatal(err)
	}

	StartObservationPath(ObservationPathStart{
		PathID: "remote-leader", CacheName: "GPU[4].L1V[0]",
		Address: 0x4000, ByteSize: 64, Operation: "read", StartTime: 0,
		Parents: []ObservationParent{{ID: "remote-parent", Address: 0x4000, ByteSize: 4}},
	})
	LinkObservationRequest("remote-leader", "leader-l2", "owner_l2_request")
	MarkObservationRemote("leader-l2")
	MarkObservationSource("leader-l2", "dram", "GPU[4].L2[0]")

	StartObservationPath(ObservationPathStart{
		PathID: "local-follower", CacheName: "GPU[4].L1V[1]",
		Address: 0x4000, ByteSize: 64, Operation: "read", StartTime: ns(1),
		Parents: []ObservationParent{{ID: "local-parent", Address: 0x4000, ByteSize: 4}},
	})
	LinkObservationRequest("local-follower", "follower-l2", "l1_bottom_read")
	ObservationTransition("local-follower", "coalesce", "l1_directory_queue", ns(2))
	ObservationTransition("local-follower", "directory", "l1_lookup", ns(3))
	MarkObservationL1Result("local-follower", "read-miss")
	ObservationTransition("local-follower", "l1-miss", "l1_downstream", ns(4))
	MarkObservationL2MSHRFollower("follower-l2", "leader-l2", ns(5))
	ObservationTransition("local-follower", "l2-response", "l1_fill_response", ns(8))
	ObservationParentResponded("local-follower", "local-parent", ns(9))

	if err := DumpObservationTrace(); err != nil {
		t.Fatal(err)
	}
	rows := readObservationGZIP(t, prefix+"_paths.csv.gz")
	var follower map[string]string
	for _, values := range rows[1:] {
		row := csvMap(rows[0], values)
		if row["path_id"] == "local-follower" {
			follower = row
			break
		}
	}
	if follower == nil {
		t.Fatal("local L2 follower was not emitted")
	}
	if follower["remote"] != "false" || follower["route"] != "local" {
		t.Fatalf("local L2 follower inherited remote route: %#v", follower)
	}
	if follower["source"] != "dram" || follower["l2_role"] != "mshr_follower" {
		t.Fatalf("local L2 follower lost shared-source metadata: %#v", follower)
	}
	validation := readObservationCSV(t, prefix+"_validation.csv")
	for _, values := range validation[1:] {
		item := csvMap(validation[0], values)
		if (item["invariant"] == "remote_path_missing_network" ||
			item["invariant"] == "missing_read_source") && item["value"] != "0" {
			t.Fatalf("valid L2 follower failed validation: %#v", item)
		}
	}
}

func TestObservationRemotePathUsesExclusiveWireAndEndpointStages(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "remote-path")
	if err := EnableObservationTrace(prefix, 0, 1, false, nil); err != nil {
		t.Fatal(err)
	}
	StartObservationPath(ObservationPathStart{
		PathID: "remote-path", CacheName: "GPU[1].SA[0].L1V[0]",
		Address: 0x3000, ByteSize: 64, Operation: "read", StartTime: ns(1),
		Parents: []ObservationParent{{ID: "parent", Address: 0x3000, ByteSize: 4}},
	})
	LinkObservationRequest("remote-path", "l1-lower", "l1_bottom_read")
	ObservationTransition("remote-path", "coalesce", "l1_directory_queue", ns(2))
	ObservationTransition("remote-path", "l1-dir", "l1_lookup", ns(3))
	ObservationTransition("remote-path", "l1-miss", "l1_downstream", ns(4))
	MarkObservationRemote("l1-lower")
	ObservationTransitionByRequest(
		"l1-lower", "requester-receive", "requester_rdma", ns(5))
	LinkObservationRequestFromRequest(
		"l1-lower", "forward-wire", "remote_forward_request")
	ObservationTransitionByRequest(
		"l1-lower", "requester-send", "remote_request_network", ns(15))
	ObservationTransitionByRequest(
		"forward-wire", "owner-receive", "owner_rdma_request", ns(45))
	LinkObservationRequestFromRequest(
		"forward-wire", "owner-l2", "owner_l2_request")
	ObservationTransitionByRequest(
		"forward-wire", "owner-send", "owner_l2_link", ns(55))
	ObservationTransitionByRequest(
		"owner-l2", "l2-receive", "l2_queue", ns(56))
	ObservationTransitionByRequest(
		"owner-l2", "l2-dir", "l2_lookup", ns(57))
	MarkObservationL2Result("owner-l2", "GPU[4].L2[0]", "read-hit")
	ObservationTransitionByRequest(
		"owner-l2", "l2-hit", "l2_bank", ns(67))
	ObservationTransitionByRequest(
		"owner-l2", "l2-response", "l2_response_link", ns(77))
	ObservationTransitionByRequest(
		"owner-l2", "owner-response-receive", "owner_rdma_response", ns(78))
	LinkObservationRequestFromRequest(
		"owner-l2", "return-wire", "remote_return_response")
	ObservationTransitionByRequest(
		"owner-l2", "owner-response-send", "remote_response_network", ns(88))
	ObservationTransitionByRequest(
		"return-wire", "requester-response-receive",
		"requester_rdma_response", ns(118))
	ObservationTransitionByRequest(
		"l1-lower", "requester-l1-send", "requester_l1_link", ns(128))
	ObservationTransitionByRequest(
		"l1-lower", "l1-bottom-response", "l1_fill_response", ns(129))
	ObservationParentResponded("remote-path", "parent", ns(130))
	if err := DumpObservationTrace(); err != nil {
		t.Fatal(err)
	}

	rows := readObservationGZIP(t, prefix+"_paths.csv.gz")
	row := csvMap(rows[0], rows[1])
	if row["route"] != "remote" || row["remote"] != "true" ||
		row["source"] != "l2" {
		t.Fatalf("unexpected remote classification: %#v", row)
	}
	for _, stage := range []string{
		"requester_rdma_ps", "remote_request_network_ps",
		"owner_rdma_request_ps", "owner_l2_link_ps",
		"owner_rdma_response_ps", "remote_response_network_ps",
		"requester_rdma_response_ps", "requester_l1_link_ps",
	} {
		if row[stage] == "0" {
			t.Fatalf("remote stage %s was not attributed", stage)
		}
	}
	if row["residual_ps"] != "0" || row["total_ps"] != row["accounted_ps"] {
		t.Fatalf("remote path is not exclusive: %#v", row)
	}
}

func ns(value float64) sim.VTimeInSec {
	return sim.VTimeInSec(value * 1e-9)
}

func readObservationGZIP(t *testing.T, path string) [][]string {
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

func readObservationCSV(t *testing.T, path string) [][]string {
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
	return rows
}

func csvMap(header, values []string) map[string]string {
	row := make(map[string]string, len(header))
	for i, key := range header {
		if i < len(values) {
			row[key] = values[i]
		}
	}
	return row
}

func mustUint(t *testing.T, value string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
