// Command opcheck lists the instructions of a kernel that the simulator
// cannot execute, without running it (T17 step 0).
//
// Input: llvm-objdump -d --mcpu=fiji output (the instruction encodings in the
// trailing "// ADDR: XXXXXXXX [YYYYYYYY]" comments). Each instruction is
// decoded with MGPUSim's own disassembler (insts.Disassembler) and its
// (format, opcode) is checked against the opcodes the emulator's ALU
// dispatch functions handle (the `case` labels of runSOP1, runVOP3A, ... in
// mgpusim/emu, read from the source tree given by -emu), plus the opcodes
// the CUs handle themselves (s_endpgm, s_barrier) and the timing units'
// limits (vector memory: FLAT 16-31; scalar unit: SMEM 0-3). Instructions
// MGPUSim cannot even decode are reported too.
//
// Usage:
//
//	llvm-objdump -d --mcpu=fiji kernels.hsaco > k.dis
//	go run ./benchmarks/t17/opcheck -emu emu k.dis
//
// Exit status 1 if anything is unsupported.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sarchlab/mgpusim/v3/insts"
)

var dispatch = map[insts.FormatType]string{
	insts.SOP1:  "runSOP1",
	insts.SOP2:  "runSOP2",
	insts.SOPC:  "runSOPC",
	insts.SOPK:  "runSOPK",
	insts.SOPP:  "runSOPP",
	insts.SMEM:  "runSMEM",
	insts.VOP1:  "runVOP1",
	insts.VOP2:  "runVOP2",
	insts.VOP3a: "runVOP3A",
	insts.VOP3b: "runVOP3B",
	insts.VOPC:  "runVOPC",
	insts.FLAT:  "runFlat",
	insts.DS:    "runDS",
}

var caseRe = regexp.MustCompile(`(?m)^\s*case\s+([0-9][0-9a-fA-FxX,\s]*):`)

// implemented reads the case labels of every dispatch function.
func implemented(emuDir string) map[string]map[int]bool {
	files, err := filepath.Glob(filepath.Join(emuDir, "*.go"))
	if err != nil || len(files) == 0 {
		panic(fmt.Sprintf("no emu sources in %s", emuDir))
	}
	out := map[string]map[int]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			panic(err)
		}
		text := string(src)
		for _, fn := range dispatch {
			head := "func (u *ALUImpl) " + fn + "(state InstEmuState) {"
			i := strings.Index(text, head)
			if i < 0 {
				continue
			}
			body := text[i:]
			if j := strings.Index(body, "\n}\n"); j >= 0 {
				body = body[:j]
			}
			set := map[int]bool{}
			for _, m := range caseRe.FindAllStringSubmatch(body, -1) {
				for _, n := range strings.Split(m[1], ",") {
					v, err := strconv.ParseInt(strings.TrimSpace(n), 0, 32)
					if err == nil {
						set[int(v)] = true
					}
				}
			}
			out[fn] = set
		}
	}
	return out
}

var encRe = regexp.MustCompile(`//\s*[0-9A-Fa-f]+:\s*([0-9A-Fa-f]{8})(?:\s+([0-9A-Fa-f]{8}))?`)

type key struct {
	format string
	opcode int
	name   string
	why    string
}

func main() {
	emuDir := flag.String("emu", "emu", "path to mgpusim/emu")
	flag.Parse()
	impl := implemented(*emuDir)
	// Handled by the compute units, not the ALU.
	impl["runSOPP"][1] = true  // s_barrier
	impl["runSOPP"][10] = true // s_endpgm

	d := insts.NewDisassembler()
	bad := map[key]int{}
	total := 0
	for _, path := range flag.Args() {
		f, err := os.Open(path)
		if err != nil {
			panic(err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			m := encRe.FindStringSubmatch(line)
			if m == nil || !strings.HasPrefix(line, "\t") {
				continue
			}
			mnem := strings.Fields(line)[0]
			if strings.HasPrefix(mnem, ".") { // data (.long padding)
				continue
			}
			buf := make([]byte, 0, 12)
			for _, w := range m[1:] {
				if w == "" {
					continue
				}
				b, _ := hex.DecodeString(w)
				buf = binary.LittleEndian.AppendUint32(buf,
					binary.BigEndian.Uint32(b))
			}
			buf = append(buf, 0, 0, 0, 0, 0, 0, 0, 0) // room for a literal
			total++
			inst, err := d.Decode(buf)
			if err != nil {
				bad[key{"?", -1, mnem, "MGPUSim cannot decode: " + err.Error()}]++
				continue
			}
			fn, ok := dispatch[inst.FormatType]
			if !ok {
				bad[key{inst.FormatName, int(inst.Opcode), mnem, "format not executed"}]++
				continue
			}
			if !impl[fn][int(inst.Opcode)] {
				bad[key{inst.FormatName, int(inst.Opcode), mnem, "emu/" + fn + " has no case"}]++
				continue
			}
			if inst.FormatType == insts.FLAT && (inst.Opcode < 16 || inst.Opcode > 31) {
				bad[key{inst.FormatName, int(inst.Opcode), mnem, "timing VMU: FLAT 16-31 only"}]++
			}
			if inst.FormatType == insts.SMEM && inst.Opcode > 3 {
				bad[key{inst.FormatName, int(inst.Opcode), mnem, "timing scalar unit: SMEM 0-3 only"}]++
			}
		}
		f.Close()
	}
	var keys []key
	for k := range bad {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].name < keys[j].name })
	fmt.Printf("opcheck: %d instructions, %d unsupported kinds\n", total, len(keys))
	for _, k := range keys {
		fmt.Printf("  %-24s %-6s opcode %4d  x%-4d %s\n", k.name, k.format, k.opcode, bad[k], k.why)
	}
	if len(keys) > 0 {
		os.Exit(1)
	}
}
