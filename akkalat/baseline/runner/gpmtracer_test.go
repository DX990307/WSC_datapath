package runner

import (
	"bytes"
	"encoding/csv"
	"math"
	"strconv"
	"testing"
)

func wafer48() []gpmTraceGPM {
	var names []string
	var tiles []int
	var cus []int
	for t := 0; t < 49; t++ {
		if t == 24 {
			continue
		}
		names = append(names, "GPU["+strconv.Itoa(t)+"]")
		tiles = append(tiles, t)
		cus = append(cus, 28)
	}
	return newGPMTraceGPMs(names, tiles, cus)
}

func TestGPMTracePositions(t *testing.T) {
	g := wafer48()
	if len(g) != 48 {
		t.Fatalf("got %d GPMs", len(g))
	}
	// Driver ID 24 is tile 23 (x=2, y=3); driver ID 25 is tile 25.
	if g[23].Tile != 23 || g[23].X != 2 || g[23].Y != 3 || g[23].DriverID != 24 {
		t.Errorf("index 23: %+v", g[23])
	}
	if g[24].Tile != 25 || g[24].X != 4 || g[24].Y != 3 {
		t.Errorf("index 24: %+v", g[24])
	}
	if g[0].HopsToCenter != 6 || g[23].HopsToCenter != 1 {
		t.Errorf("hops to center: %d %d", g[0].HopsToCenter, g[23].HopsToCenter)
	}
	// Corner: (294 - 6) / 47 = 6.128 hops; T16 quotes 6.1.
	if math.Abs(g[0].MeanHops-288.0/47) > 1e-9 {
		t.Errorf("corner mean hops %f", g[0].MeanHops)
	}
	if g[23].MeanHops > 4 {
		t.Errorf("near-centre mean hops %f", g[23].MeanHops)
	}
}

func TestAssignWGs(t *testing.T) {
	cus := make([]int, 48)
	for i := range cus {
		cus[i] = 28
	}
	// 1344 CUs; 2000 WGs -> 2 per CU -> 56 per GPM; 35 full GPMs, one with
	// 40, and 12 GPMs without work.
	a := assignWGs(2000, cus)
	sum, busy := 0, 0
	for _, n := range a {
		sum += n
		if n > 0 {
			busy++
		}
	}
	if sum != 2000 || a[0] != 56 || a[34] != 56 || a[35] != 40 || a[36] != 0 || busy != 36 {
		t.Errorf("assign: sum=%d a0=%d a34=%d a35=%d busy=%d", sum, a[0], a[34], a[35], busy)
	}
	if a := assignWGs(0, cus); a[0] != 0 {
		t.Errorf("zero WGs: %v", a)
	}
}

func TestGPMTraceCSV(t *testing.T) {
	agg := newGPMTraceAgg(wafer48())

	// Events before any launch are ignored.
	agg.wgStart("early", 0, 1)
	agg.l1vIssue("e", 0, true, false, 1)

	agg.kernelLaunch("cmd0", "req0_req_out", 0, 2000, true, 100)
	agg.kernelLaunch("cmd0", "req1_req_out", 1, 2000, true, 101)

	agg.wgStart("wgA", 0, 110)
	agg.wgStart("wgB", 0, 120)
	agg.wgStart("wgC", 1, 115)
	agg.wgEnd("wgA", 0, 210) // 100 ns
	agg.wgEnd("wgB", 0, 320) // 200 ns

	agg.l1vIssue("r1", 0, true, false, 130)
	agg.l1vIssue("r2", 0, false, false, 130)
	agg.l1vIssue("r3", 0, true, false, 140)
	agg.l1vIssue("w1", 0, true, true, 150)
	agg.l1vDone("r1", 730) // 600
	agg.l1vDone("r2", 230) // 100
	agg.l1vDone("r3", 940) // 800
	agg.l1vDone("w1", 650)
	agg.l1vDone("unknown", 999)

	agg.tlbMiss("t1", 0, true, 160)
	agg.tlbMiss("t2", 0, false, 160)
	agg.tlbDone("t1", 1160)
	agg.tlbDone("t2", 300)

	agg.driverTaskEnd("req0_req_out", 400)
	agg.driverTaskEnd("cmd0", 500) // GPM 1 never answered: truncated

	agg.kernelLaunch("cmd1", "req2_req_out", 0, 10, true, 600)
	agg.wgStart("wgD", 0, 610)
	agg.wgEnd("wgD", 0, 640)

	var buf bytes.Buffer
	if err := agg.writeCSV(&buf); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1+2*48 {
		t.Fatalf("rows=%d", len(rows))
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	get := func(r int, name string) string { return rows[r][col[name]] }

	r0 := 1 // kernel 0, GPM 0
	checks := map[string]string{
		"kernel": "0", "wg_assigned": "56", "wg_started": "2", "wg_completed": "2",
		"first_wg_start_ns": "110.000", "last_wg_end_ns": "320.000",
		"gpm_kernel_end_ns": "400.000", "mean_wg_duration_ns": "150.000",
		"kernel_end_ns":  "500.000",
		"l1v_read_local": "1", "l1v_read_remote": "2", "l1v_read_remote_frac": "0.6667",
		"l1v_read_remote_mean_lat_ns": "700.000", "l1v_read_local_mean_lat_ns": "100.000",
		"l1v_write_remote": "1", "l1v_write_local": "0",
		"l2tlb_miss_remote": "1", "l2tlb_miss_local": "1",
		"l2tlb_miss_remote_lat_sum_ns": "1000.000", "l2tlb_miss_remote_mean_lat_ns": "1000.000",
		"tile": "0", "hops_to_center": "6",
	}
	for k, v := range checks {
		if got := get(r0, k); got != v {
			t.Errorf("kernel 0 GPM 0 %s = %q, want %q", k, got, v)
		}
	}
	r1 := 2 // kernel 0, GPM 1: started one WG, never finished
	if get(r1, "wg_started") != "1" || get(r1, "wg_completed") != "0" ||
		get(r1, "gpm_kernel_end_ns") != "" || get(r1, "last_wg_end_ns") != "" ||
		get(r1, "mean_wg_duration_ns") != "" {
		t.Errorf("kernel 0 GPM 1: %v", rows[r1])
	}
	r47 := 48 // kernel 0, last GPM: no work
	if get(r47, "wg_assigned") != "0" || get(r47, "gpm_launch_ns") != "" ||
		get(r47, "tile") != "48" {
		t.Errorf("kernel 0 GPM 47: %v", rows[r47])
	}
	k1 := 1 + 48 // kernel 1, GPM 0
	if get(k1, "kernel") != "1" || get(k1, "wg_assigned") != "10" ||
		get(k1, "wg_completed") != "1" || get(k1, "kernel_end_ns") != "" ||
		get(k1, "mean_wg_duration_ns") != "30.000" {
		t.Errorf("kernel 1 GPM 0: %v", rows[k1])
	}
	if get(k1+1, "wg_assigned") != "0" {
		t.Errorf("kernel 1 GPM 1 assigned %s", get(k1+1, "wg_assigned"))
	}
}

func TestGPMTraceSinglePhases(t *testing.T) {
	agg := newGPMTraceAgg(wafer48())
	// Phase 0: GPMs 0 and 5 each run a 224-WG kernel; phase 1: GPM 0 again.
	agg.kernelLaunch("c0", "l0", 0, 224, false, 10)
	agg.kernelLaunch("c1", "l1", 5, 224, false, 11)
	agg.driverTaskEnd("l0", 500)
	agg.driverTaskEnd("c0", 500) // a single-GPM command end is not a phase end
	agg.kernelLaunch("c2", "l2", 0, 224, false, 600)
	agg.driverTaskEnd("l1", 700)

	if len(agg.kernels) != 2 {
		t.Fatalf("kernels=%d", len(agg.kernels))
	}
	k0 := agg.kernels[0]
	if !k0.Single || k0.TotalWG != 448 || k0.PerGPM[0].Assigned != 224 ||
		k0.PerGPM[5].Assigned != 224 || k0.PerGPM[1].Assigned != 0 {
		t.Errorf("phase 0: %+v", k0)
	}
	if end, ok := k0.end(); !ok || end != 700 {
		t.Errorf("phase 0 end %v %v", end, ok)
	}
	if _, ok := agg.kernels[1].end(); ok {
		t.Errorf("phase 1 has not ended")
	}
}
