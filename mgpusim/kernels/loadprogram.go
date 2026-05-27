package kernels

import (
	"bytes"
	"debug/elf"
	"log"

	"github.com/sarchlab/mgpusim/v3/insts"
)

// LoadProgram loads program
func LoadProgram(filePath, kernelName string) *insts.HsaCo {
	executable, err := elf.Open(filePath)
	if err != nil {
		log.Fatal(err)
	}

	symbols, err := executable.Symbols()
	if err != nil {
		log.Fatal(err)
	}

	textSection := executable.Section(".text")
	if textSection == nil {
		log.Fatal(".text section is not found")
	}

	textSectionData, err := textSection.Data()
	if err != nil {
		log.Fatal(err)
	}

	// An empty kernel name is for the case where the symbol is not generated.
	// Use the whole text section in this case.
	if kernelName == "" {
		hsaco := insts.NewHsaCoFromData(textSectionData)
		return hsaco
	}

	for _, symbol := range symbols {
		if symbol.Name == kernelName {
			offset := symbolOffsetInSection(symbol, textSection, len(textSectionData))
			hsacoData := textSectionData[offset : offset+symbol.Size]
			hsaco := insts.NewHsaCoFromData(hsacoData)
			hsaco.Symbol = &symbol

			//fmt.Println(hsaco.Info())

			return hsaco
		}
	}

	return nil
}

// LoadProgramFromMemory loads program
func LoadProgramFromMemory(data []byte, kernelName string) *insts.HsaCo {
	reader := bytes.NewReader(data)
	executable, err := elf.NewFile(reader)
	if err != nil {
		log.Fatal(err)
	}

	symbols, err := executable.Symbols()
	if err != nil {
		log.Fatal(err)
	}

	textSection := executable.Section(".text")
	if textSection == nil {
		log.Fatal(".text section is not found")
	}

	textSectionData, err := textSection.Data()
	if err != nil {
		log.Fatal(err)
	}

	// An empty kernel name is for the case where the symbol is not generated.
	// Use the whole text section in this case.
	if kernelName == "" {
		hsaco := insts.NewHsaCoFromData(textSectionData)
		return hsaco
	}

	for _, symbol := range symbols {
		if symbol.Name == kernelName {
			offset := symbolOffsetInSection(symbol, textSection, len(textSectionData))
			hsacoData := textSectionData[offset : offset+symbol.Size]
			hsaco := insts.NewHsaCoFromData(hsacoData)
			symbolCopy := symbol
			hsaco.Symbol = &symbolCopy

			//fmt.Println(hsaco.Info())

			return hsaco
		}
	}

	return nil
}

func symbolOffsetInSection(
	symbol elf.Symbol,
	section *elf.Section,
	sectionDataLen int,
) uint64 {
	if symbol.Value >= section.Addr {
		offset := symbol.Value - section.Addr
		if offset+symbol.Size <= uint64(sectionDataLen) {
			return offset
		}
	}

	if symbol.Value >= section.Offset {
		offset := symbol.Value - section.Offset
		if offset+symbol.Size <= uint64(sectionDataLen) {
			return offset
		}
	}

	if symbol.Value+symbol.Size <= uint64(sectionDataLen) {
		return symbol.Value
	}

	log.Panicf(
		"symbol %s is outside section %s: value=%#x size=%#x addr=%#x offset=%#x len=%#x",
		symbol.Name, section.Name, symbol.Value, symbol.Size,
		section.Addr, section.Offset, sectionDataLen)
	return 0
}
