package trace

import (
	"compress/gzip"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func pgT(ns float64) sim.VTimeInSec { return sim.VTimeInSec(ns * 1e-9) }

func TestPgclsReaderStats(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "m_pgcls")
	if err := EnablePgcls(PgclsConfig{Prefix: prefix}); err != nil {
		t.Fatal(err)
	}
	defer DisablePgcls()
	own := "GPU[5].RDMA"
	base := uint64(5<<33) + 0x1000
	PgclsKernelLaunch(1, pgT(0))
	PgclsKernelLaunch(2, pgT(0))
	PgclsAccess("GPU[1].RDMA", own, "", base, 64, true, "v", pgT(10))          // first
	PgclsAccess("GPU[1].RDMA", own, "", base, 64, true, "v", pgT(20))          // intra
	PgclsAccess("GPU[2].RDMA", own, "", base+512, 64, true, "v", pgT(30))      // other reader, sub 1
	PgclsAccess("GPU[5].L1VCache[0]", "", "", base+64, 64, true, "v", pgT(40)) // owner local
	PgclsKernelLaunch(1, pgT(100))                                             // new global kernel
	PgclsKernelLaunch(2, pgT(100))
	PgclsAccess("GPU[1].RDMA", own, "", base, 64, true, "v", pgT(110)) // cross
	PgclsAccess("GPU[2].RDMA", own, "", base+512, 4, false, "v", pgT(120))
	PgclsAccess("GPU[1].RDMA", own, "", base+512, 64, true, "v", pgT(130)) // other written same k
	PgclsKernelLaunch(1, pgT(200))
	PgclsAccess("GPU[1].RDMA", own, "", base+512, 64, true, "v", pgT(210)) // cross (line 8 read in the previous kernel after the write)
	if err := PgclsWrite(300); err != nil {
		t.Fatal(err)
	}
	read := func(name string) [][]string {
		f, err := os.Open(prefix + name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		g, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(g).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	pages := read("_pages.csv.gz")
	if len(pages) != 2 {
		t.Fatalf("pages %v", pages)
	}
	p := pages[1]
	// rd_remote, rd_local, wr_remote, rd_other_written, same_k
	if p[4] != "6" || p[5] != "1" || p[6] != "1" || p[10] != "2" || p[11] != "1" {
		t.Fatalf("page row %v", p)
	}
	rd := read("_readers.csv.gz")
	// reader 1: reads 5? -> 4 reads, intra 1, cross 2 (line0 in k2, line8 in k3)
	var r1 []string
	for _, r := range rd[1:] {
		if r[1] == "1" {
			r1 = r
		}
	}
	if r1 == nil || r1[3] != "5" || r1[10] != "1" || r1[11] != "2" {
		t.Fatalf("reader 1 row %v", r1)
	}
}
