package profiler

import (
	"encoding/json"
	"os"
	//    "log"
	"flag"
	//	   "fmt"
	//		"github.com/sarchlab/akita/v3/mem/vm"
	//
	//		"github.com/sarchlab/akita/v3/sim"
	//		"github.com/sarchlab/mgpusim/v3/insts"
	//		"github.com/sarchlab/mgpusim/v3/utils"
	//
	//		"github.com/sarchlab/mgpusim/v3/samples/sampledrunner"
)

var InstsNumCollectFlag = flag.Bool("collect-instnum", false,
	"collect instnums in emulation.")

type InstsProfiler struct {
	Insts_num uint64  `json:"insts_num"`
	Wall_time float64 `json:"walltime"`
}

var Inst_profiler *InstsProfiler

func InitInstProfiler() {
	Inst_profiler = &InstsProfiler{
		Insts_num: 0,
	}
}

func (insts *InstsProfiler) Collect() {
	insts.Insts_num++
}
func ReportInstsNum(walltime float64) {
	if *InstsNumCollectFlag {
		Inst_profiler.Wall_time = walltime
		jsonStr, _ := json.MarshalIndent(Inst_profiler, "", " ")
		file, _ := os.Create("insnums.json")
		defer file.Close()
		file.Write(jsonStr)
	}
}
