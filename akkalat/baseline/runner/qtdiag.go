package runner

// ISCA'27 P19 quick trials, diagnosis D: statistics only, off by default.
//   - every L1V cache: time-weighted MSHR occupancy and the time all entries
//     are in use -> <metric-file-name>_qt_l1v.csv;
//   - with -report-obs-latency: the remote-read latency segments split by
//     owner tile as well -> <obs prefix>_latency_owner.csv.

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"

	"github.com/sarchlab/akita/v3/mem/cache/writearound"
)

var qtDiagFlag = flag.Bool("report-qt-diag", false,
	"Statistics only (ISCA'27 P19 diagnosis): per-CU L1V MSHR occupancy and "+
		"full time (<metric-file-name>_qt_l1v.csv); with "+
		"-report-obs-latency also the latency segments per owner tile "+
		"(_obs_latency_owner.csv). Does not change timing.")

var pgclsTraceExtFlag = flag.Bool("pgcls-trace-ext", false,
	"With -report-pgcls and -pgcls-sample: the raw trace also has op N "+
		"(remote read line sent to the network) and op D (reply to the L1V, "+
		"column lat_ns) rows. Statistics only.")

func (r *Runner) addQtDiag() {
	if !*qtDiagFlag || !r.Timing {
		return
	}
	if !*obsLatencyFlag && !*obsAllFlag {
		panic("-report-qt-diag needs -report-obs-latency")
	}
	for _, gpu := range r.platform.GPUs {
		for _, c := range gpu.L1VCaches {
			l1, ok := c.(*writearound.Cache)
			if !ok {
				panic("-report-qt-diag: L1V is not a writearound cache")
			}
			l1.EnableMSHRStats()
		}
	}
	fmt.Printf("[Config] qt-diag: L1V MSHR occupancy, obs latency per owner\n")
}

func (r *Runner) writeQtDiag() {
	if !*qtDiagFlag || !r.Timing {
		return
	}
	endNS := float64(r.platform.Engine.CurrentTime()) * 1e9
	f, err := os.Create(*filenameFlag + "_qt_l1v.csv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"gpu", "l1v", "capacity", "time_ns", "full_ns",
		"full_frac", "avg_used", "full_stalls"})
	for gi, gpu := range r.platform.GPUs {
		for _, c := range gpu.L1VCaches {
			l1 := c.(*writearound.Cache)
			s := l1.MSHRStatsResult(endNS)
			if s == nil {
				continue
			}
			t := s.LastNS - s.StartNS
			ff, avg := 0.0, 0.0
			if t > 0 {
				ff, avg = s.FullNS/t, s.AreaNS/t
			}
			_ = w.Write([]string{fmt.Sprint(gi), l1.Name(),
				fmt.Sprint(s.Capacity), fmt.Sprintf("%.1f", t),
				fmt.Sprintf("%.1f", s.FullNS), fmt.Sprintf("%.6f", ff),
				fmt.Sprintf("%.4f", avg), fmt.Sprint(s.FullStalls)})
		}
	}
	w.Flush()
}
