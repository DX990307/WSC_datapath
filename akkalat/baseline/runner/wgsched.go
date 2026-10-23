package runner

// ISCA27 wg-to-data: pass-1 statistics (-report-wgaff) and pass-2 dispatch
// map (-wg-map). Both off by default.

import (
	"flag"
	"fmt"
	"os"
	"strings"

	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var wgAffFlag = flag.Bool("report-wgaff", false,
	"wg-to-data pass 1: count translated vector accesses per "+
		"(kernel, WG, owner GPM). Statistics only.")
var wgAffFileFlag = flag.String("wgaff-file", "",
	"Output prefix of -report-wgaff / -wg-map launch stats "+
		"(default <metric-file-name>).")
var wgMapFlag = flag.String("wg-map", "",
	"wg-to-data pass 2: per-kernel WG -> GPM dispatch map file.")

func wgAffPrefix() string {
	if *wgAffFileFlag != "" {
		return *wgAffFileFlag
	}
	return *filenameFlag
}

func (r *Runner) addWGSched() {
	if *wgAffFlag {
		memtrace.EnableWGAffinity()
		fmt.Printf("[Config] wgaff: prefix=%s\n", wgAffPrefix())
	}
	if *wgMapFlag != "" {
		m, err := driver.LoadWGMap(*wgMapFlag)
		if err != nil {
			panic(err)
		}
		r.platform.Driver.SetWGMap(m)
		fmt.Printf("[Config] wg-map: %s (%d kernels)\n", *wgMapFlag, len(m))
	}
}

func (r *Runner) writeWGSched() {
	if !*wgAffFlag && *wgMapFlag == "" {
		return
	}
	prefix := wgAffPrefix()
	if *wgAffFlag {
		if err := memtrace.WriteWGAffinity(prefix + "_wgaff.csv"); err != nil {
			fmt.Fprintln(os.Stderr, "wgaff:", err)
		}
	}
	f, err := os.Create(prefix + "_wglaunch.csv")
	if err != nil {
		fmt.Fprintln(os.Stderr, "wglaunch:", err)
		return
	}
	defer f.Close()
	fmt.Fprintln(f, "kernel,total_wg,mapped,per_gpm")
	for _, l := range r.platform.Driver.UnifiedLaunches() {
		s := make([]string, len(l.PerGPM))
		for i, n := range l.PerGPM {
			s[i] = fmt.Sprint(n)
		}
		mapped := 0
		if l.Mapped {
			mapped = 1
		}
		fmt.Fprintf(f, "%d,%d,%d,%s\n", l.Index, l.TotalWG, mapped,
			strings.Join(s, " "))
	}
}
