package trace

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func obsT(ns float64) sim.VTimeInSec { return sim.VTimeInSec(ns * 1e-9) }

func readObsCSV(t *testing.T, path string) []map[string]string {
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
	var out []map[string]string
	for _, r := range rows[1:] {
		m := map[string]string{}
		for i, h := range rows[0] {
			m[h] = r[i]
		}
		out = append(out, m)
	}
	return out
}

func summaryOf(t *testing.T, path string) map[string]string {
	m := map[string]string{}
	for _, r := range readObsCSV(t, path) {
		m[r["metric"]] = r["value"]
	}
	return m
}

func TestObsLatencySegmentsAndMerge(t *testing.T) {
	EnableObsStats(ObsConfig{Latency: true, ReqAgg: true, Reuse: true,
		TileWidth: 7, CycleNS: 1})
	defer DisableObsStats()
	req, own := "GPU[0].RDMA", "GPU[2].RDMA.ToOutside" // 2 hops
	const addr = 0x1000040
	// leader: t0=100, t1=110, t2=200, t3=210, L2 hit, t4=300, send 305,
	// t5=400, t6=402
	ObsAdmit(req, own, "a", addr, 64, 1, true, obsT(100))
	// follower in flight: t0=150, completes at 403
	ObsNetSend(req, own, "net1", addr, 1, obsT(110))
	ObsAdmit(req, own, "b", addr+4, 4, 1, true, obsT(150))
	ObsOwnerRecv("net1", addr, obsT(200))
	ObsOwnerL2("net1", addr, obsT(210), "l2a", false)
	ObsL2Result("l2a", "read-hit")
	ObsOwnerL2Rsp("l2a", obsT(300))
	ObsOwnerSend("net1", addr, obsT(305), false)
	ObsReqArrive(req, "net1", addr, obsT(400))
	ObsL1Done("a", obsT(402))
	ObsL1Done("b", obsT(403))
	// a later re-read of the same line, served by a new packet
	ObsAdmit(req, own, "c", addr, 64, 1, true, obsT(1000))

	dir := t.TempDir()
	prefix := filepath.Join(dir, "x")
	if err := ObsWrite(prefix, "w", 2000); err != nil {
		t.Fatal(err)
	}
	rows := readObsCSV(t, prefix+"_latency.csv")
	var hop2 map[string]string
	for _, r := range rows {
		if r["hops"] == "2" {
			hop2 = r
		}
	}
	if hop2 == nil {
		t.Fatalf("no 2-hop row: %v", rows)
	}
	want := map[string]string{
		"n": "2", "n_net": "1", "n_req_merged_inflight": "1",
		"n_owner_hit": "1", "wait_rrt_mean": "10", "fwd_mean": "90",
		"owner_queue_mean": "10", "owner_service_hit_mean": "90",
		"owner_rsp_wait_mean": "5", "ret_net_mean": "95", "ret_mean": "100",
		"merge_wait_mean": "250", "rtt_mean": "290", "to_l1v_n": "2",
		"to_l1v_mean": "2.5", "total_mean": "277.5",
	}
	for k, v := range want {
		if hop2[k] != v {
			t.Errorf("%s = %s, want %s", k, hop2[k], v)
		}
	}
	s := summaryOf(t, prefix+"_summary.csv")
	for k, v := range map[string]string{
		"agg_remote_reads": "3", "agg_inflight_same_line_same_gpm": "1",
		"reuse_unique_lines": "1", "reuse_retouch_reads": "2",
		"reuse_retouch_reads_after_complete": "1", "owner_reads": "1",
		"open_logical_reads": "1", "open_network_lines": "0",
	} {
		if s[k] != v {
			t.Errorf("summary %s = %s, want %s", k, s[k], v)
		}
	}
}

func TestObsOwnerMergeMulticastAndSamePage(t *testing.T) {
	EnableObsStats(ObsConfig{Latency: true, ReqAgg: true, Reuse: true,
		TileWidth: 7, CycleNS: 1})
	defer DisableObsStats()
	own := "GPU[24].RDMA.ToOutside"
	const addr = 0x2000000
	ObsAdmit("GPU[0].RDMA", own, "a", addr, 64, 1, true, obsT(0))
	ObsAdmit("GPU[1].RDMA", own, "b", addr, 64, 1, true, obsT(0))
	// same page, other line, 3 cycles later at GPU[0]: same-page <= 4 cycles
	ObsAdmit("GPU[0].RDMA", own, "c", addr+64, 64, 1, true, obsT(3))
	ObsNetSend("GPU[0].RDMA", own, "n0", addr, 1, obsT(10))
	ObsNetSend("GPU[1].RDMA", own, "n1", addr, 1, obsT(10))
	ObsOwnerRecv("n0", addr, obsT(100))
	ObsOwnerL2("n0", addr, obsT(110), "l2", false)
	ObsOwnerRecv("n1", addr, obsT(120))
	ObsOwnerL2("n1", addr, obsT(130), "l2", true)
	ObsL2Result("l2", "read-miss")
	ObsOwnerL2Rsp("l2", obsT(500))
	ObsOwnerSend("n0", addr, obsT(510), true)
	ObsOwnerSend("n1", addr, obsT(510), true)
	ObsReqArrive("GPU[0].RDMA", "n0", addr, obsT(600))
	ObsReqArrive("GPU[1].RDMA", "n1", addr, obsT(650))
	ObsL1Done("a", obsT(601))
	ObsL1Done("b", obsT(651))
	ObsAdmit("GPU[1].RDMA", own, "d", addr, 64, 1, true, obsT(700))

	dir := t.TempDir()
	prefix := filepath.Join(dir, "x")
	if err := ObsWrite(prefix, "w", 1000); err != nil {
		t.Fatal(err)
	}
	var all map[string]string
	for _, r := range readObsCSV(t, prefix+"_latency.csv") {
		if r["hops"] == "all" {
			all = r
		}
	}
	for k, v := range map[string]string{
		"n": "2", "n_owner_miss": "1", "n_owner_merged": "1",
		"n_multicast_leaf": "2", "owner_service_miss_mean": "390",
		"owner_service_merged_mean": "370",
	} {
		if all[k] != v {
			t.Errorf("%s = %s, want %s", k, all[k], v)
		}
	}
	s := summaryOf(t, prefix+"_summary.csv")
	for k, v := range map[string]string{
		"owner_same_line_concurrent": "1",
		"owner_same_line_concurrent_other_gpm": "1",
		"agg_same_page_le_2cyc": "0", "agg_same_page_le_4cyc": "1",
		"multicast_deliveries": "2", "multicast_reread_lines": "1",
		"agg_sent_lines": "2", "agg_same_cycle_same_page_lines": "0",
	} {
		if s[k] != v {
			t.Errorf("summary %s = %s, want %s", k, s[k], v)
		}
	}
}

func TestObsL2FreeAndLinks(t *testing.T) {
	EnableObsStats(ObsConfig{Reuse: true, Links: true, TileWidth: 2,
		WindowNS: 100, LinkBytesPerNS: 16, FlitBytes: 16})
	defer DisableObsStats()
	id := ObsL2Register("GPU[0].L2[0]", 1, 2)
	ObsL2Fill(id, 0, 0, obsT(100))    // way0 invalid 0..100
	ObsL2ReadHit(id, 0, 0, obsT(300)) // read at 300
	ObsL2Fill(id, 0, 0, obsT(500))    // dead 300..500
	// way1 never used: invalid 0..1000; way0 tail 500..1000
	l := ObsLinkRegister("SW[0][0][0].Right", "SW[1][0][0].Left")
	for i := 0; i < 50; i++ {
		ObsLinkFlit(l, obsT(10))
	}
	dir := t.TempDir()
	prefix := filepath.Join(dir, "x")
	if err := ObsWrite(prefix, "w", 1000); err != nil {
		t.Fatal(err)
	}
	r := readObsCSV(t, prefix+"_l2free.csv")[0]
	for k, v := range map[string]string{
		"invalid_frac": "0.55", "dead_frac": "0.1", "tail_frac": "0.25",
		"free_frac": "0.65",
	} {
		if r[k] != v {
			t.Errorf("l2free %s = %s, want %s", k, r[k], v)
		}
	}
	lr := readObsCSV(t, prefix+"_links.csv")[0]
	if lr["bytes"] != "800" || lr["util"] != "0.05" ||
		lr["max_window_util"] != "0.5" {
		t.Errorf("link row %v", lr)
	}
}
