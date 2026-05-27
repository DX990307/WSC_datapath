package profiler

import (
	//    "encoding/json"
	//    "os"
	"log"
	//    "flag"
	"fmt"
	//	"github.com/sarchlab/akita/v3/mem/vm"

	"github.com/sarchlab/akita/v3/sim"
	//	"github.com/sarchlab/mgpusim/v3/utils"

	//	"github.com/sarchlab/mgpusim/v3/samples/sampledrunner"
	"github.com/sarchlab/mgpusim/v3/insts"
	// "github.com/sarchlab/mgpusim/v3/utils"
)

type InstDataType struct {
	format insts.FormatType
	opcode insts.Opcode
}
type InstDataFeature struct {
	intervalsum sim.VTimeInSec
	intervalnum int
}

type OnlineInstFeature struct {
	startTime        sim.VTimeInSec
	instid2starttime map[int]sim.VTimeInSec
	inst2predicttime map[InstDataType]*InstDataFeature
}

var Global_inst_feature *OnlineInstFeature

func InitGlobalInstFeature() {
	Global_inst_feature = &OnlineInstFeature{
		instid2starttime: make(map[int]sim.VTimeInSec),
		inst2predicttime: make(map[InstDataType]*InstDataFeature),
	}
}

func ReportInstFeature() {
	log.Printf("inst feature: ")
	for instdatatype, instdatafeature := range Global_inst_feature.inst2predicttime {
		fmt.Printf("format %d opcode %d instnum %d avginsttime %.2f\n", instdatatype.format, instdatatype.opcode, instdatafeature.intervalnum, instdatafeature.intervalsum/sim.VTimeInSec(instdatafeature.intervalnum)*1e9)
	}
	fmt.Printf("\n")
}

func (instfeature *OnlineInstFeature) Predict(inst *insts.Inst) (sim.VTimeInSec, bool) {
	if instfeature == nil || inst == nil {
		return sim.VTimeInSec(0), false
	}

	opcode := inst.Opcode
	format := inst.FormatType
	insttype := InstDataType{
		opcode: opcode,
		format: format,
	}
	instdatafeatureptr, found := instfeature.inst2predicttime[insttype]
	if !found {
		return sim.VTimeInSec(0), found
	}
	instnum := instdatafeatureptr.intervalnum
	if instnum > 1024 {
		intervalnum := sim.VTimeInSec(instnum)
		intervalsum := instdatafeatureptr.intervalsum
		intervalavg := intervalsum / intervalnum
		return intervalavg, found
	} else {
		return sim.VTimeInSec(0), false
	}
}
func (instfeature *OnlineInstFeature) InstIssue(wfid string, now sim.VTimeInSec, inst *insts.Inst) {
	if inst.FormatType == insts.FLAT || inst.FormatType == insts.SMEM {
		instid := inst.ID
		_, found := instfeature.instid2starttime[instid]
		if !found {
			instfeature.instid2starttime[instid] = now
		}
	}
}

func (instfeature *OnlineInstFeature) InstRetired(wfid string, now sim.VTimeInSec, inst *insts.Inst) {
	if inst.FormatType == insts.FLAT || inst.FormatType == insts.SMEM {

		instid := inst.ID
		issuetime, found := instfeature.instid2starttime[instid]
		if found {
			executetime := now - issuetime
			opcode := inst.Opcode
			format := inst.FormatType
			insttype := InstDataType{
				opcode: opcode,
				format: format,
			}
			instdatafeatureptr, found := instfeature.inst2predicttime[insttype]
			if found {
				instdatafeatureptr.intervalnum++
				instdatafeatureptr.intervalsum += executetime

			} else {
				instfeature.inst2predicttime[insttype] = &InstDataFeature{
					intervalsum: executetime,
					intervalnum: 1,
				}

			}
			delete(instfeature.instid2starttime, instid)
		}
	}
}
