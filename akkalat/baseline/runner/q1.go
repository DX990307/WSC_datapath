package runner

// ISCA'27 P19 Q1: hop-dependent prefetch depth (mgpusim/timing/rdma/q1_depth.go).
// Off by default (all depths 0).

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"

	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var q1NearFlag = flag.Int("q1-depth-near", 0,
	"P19 Q1: extra same-page lines queued with a network demand read to an "+
		"owner <= 2 hops away (riding the demand's batch only; 0 = off).")
var q1MidFlag = flag.Int("q1-depth-mid", 0,
	"P19 Q1: extra same-page lines for owners 3-5 hops away.")
var q1FarFlag = flag.Int("q1-depth-far", 0,
	"P19 Q1: extra same-page lines for owners >= 6 hops away.")
var q1HotShareFlag = flag.Float64("q1-hot-share", 0.3,
	"P19 Q1: no extra lines to an owner that receives at least this share of "+
		"the requester's recent network lines (0 = no hot-owner rule).")
var q1RecentFlag = flag.Int("q1-recent-lines", 8192,
	"P19 Q1: exact recent-line set per requester (kernel scoped).")

func (r *Runner) addQ1() {
	cfg := rdma.Q1Config{Near: *q1NearFlag, Mid: *q1MidFlag, Far: *q1FarFlag,
		HotShare: *q1HotShareFlag, RecentLines: *q1RecentFlag}
	if !cfg.Enabled() || !r.Timing {
		return
	}
	if !*remoteDataPathEnableFlag || *t18WindowFlag <= 0 {
		panic("-q1-depth-* needs the remote data path with T18 (v4_complete / v4m3)")
	}
	for _, gpu := range r.platform.GPUs {
		gpu.RDMAEngine.ConfigureQ1(cfg)
	}
	fmt.Printf("[Config] q1 depth near=%d mid=%d far=%d hot-share=%g recent=%d\n",
		cfg.Near, cfg.Mid, cfg.Far, cfg.HotShare, cfg.RecentLines)
}

func (r *Runner) writeQ1() {
	if !r.Timing {
		return
	}
	var rows [][]string
	for i, gpu := range r.platform.GPUs {
		s := gpu.RDMAEngine.Q1Result()
		if s == nil {
			continue
		}
		rows = append(rows, []string{fmt.Sprint(i), fmt.Sprint(s.Demands),
			fmt.Sprint(s.Queued), fmt.Sprint(s.Useful), fmt.Sprint(s.Unused),
			fmt.Sprint(s.SkipInflight), fmt.Sprint(s.SkipRecent),
			fmt.Sprint(s.SkipUncacheable), fmt.Sprint(s.SkipHot),
			fmt.Sprint(s.SkipCapacity), fmt.Sprint(s.SkipFilter)})
	}
	if rows == nil {
		return
	}
	f, err := os.Create(*filenameFlag + "_q1.csv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"gpu", "demands", "queued", "useful", "unused",
		"skip_inflight", "skip_recent", "skip_uncacheable", "skip_hot",
		"skip_capacity", "skip_filter"})
	_ = w.WriteAll(rows)
}
