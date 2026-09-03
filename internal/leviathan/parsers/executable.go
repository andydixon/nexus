package parsers

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/andydixon/link/internal/leviathan/document"
)

// ExecutableRegion is a file-backed executable code range.
type ExecutableRegion struct {
	Name    string `json:"name"`
	Offset  int64  `json:"offset"`
	Size    int64  `json:"size"`
	Address uint64 `json:"address"`
}

// FunctionSeed is a named function entry mapped to a file offset.
type FunctionSeed struct {
	Name    string `json:"name"`
	Offset  int64  `json:"offset"`
	Address uint64 `json:"address"`
	Size    uint64 `json:"size"`
	Source  string `json:"source"`
}

// ExecutableInfo contains disassembly-oriented metadata for an ELF or PE.
type ExecutableInfo struct {
	Format  string             `json:"format"`
	Arch    string             `json:"arch"`
	Entry   int64              `json:"entry"`
	Regions []ExecutableRegion `json:"regions"`
	Seeds   []FunctionSeed     `json:"seeds"`
}

// InspectExecutable extracts executable ranges, entry point and named function
// seeds without scraping the display-oriented parser tree.
func InspectExecutable(d *document.Document) (*ExecutableInfo, error) {
	head, err := d.Range(0, min64(64, d.Size()))
	if err != nil {
		return nil, err
	}
	if len(head) >= 4 && string(head[:4]) == "\x7fELF" {
		return inspectELFExecutable(d, head)
	}
	if len(head) >= 2 && string(head[:2]) == "MZ" {
		return inspectPEExecutable(d)
	}
	return nil, errors.New("not a supported ELF or PE executable")
}

func inspectELFExecutable(d *document.Document, hdr []byte) (*ExecutableInfo, error) {
	if len(hdr) < 52 || (hdr[4] != 1 && hdr[4] != 2) || (hdr[5] != 1 && hdr[5] != 2) {
		return nil, errors.New("invalid ELF header")
	}
	class, data := hdr[4], hdr[5]
	bo := elfByteOrder(data)
	var machine uint16
	var entry, phoff, shoff uint64
	var phentsize, phnum, shentsize, shnum uint16
	if class == 2 {
		if len(hdr) < 64 {
			return nil, errors.New("ELF64 header too short")
		}
		machine = bo.Uint16(hdr[18:20])
		entry = bo.Uint64(hdr[24:32])
		phoff = bo.Uint64(hdr[32:40])
		shoff = bo.Uint64(hdr[40:48])
		phentsize, phnum = bo.Uint16(hdr[54:56]), bo.Uint16(hdr[56:58])
		shentsize, shnum = bo.Uint16(hdr[58:60]), bo.Uint16(hdr[60:62])
	} else {
		machine = bo.Uint16(hdr[18:20])
		entry = uint64(bo.Uint32(hdr[24:28]))
		phoff = uint64(bo.Uint32(hdr[28:32]))
		shoff = uint64(bo.Uint32(hdr[32:36]))
		phentsize, phnum = bo.Uint16(hdr[42:44]), bo.Uint16(hdr[44:46])
		shentsize, shnum = bo.Uint16(hdr[46:48]), bo.Uint16(hdr[48:50])
	}
	sections, _ := executableELFSections(d, bo, class, shoff, shentsize, shnum)
	info := &ExecutableInfo{Format: "elf", Arch: elfDisasmArch(machine, data), Entry: -1}
	if info.Arch == "" {
		return nil, fmt.Errorf("unsupported ELF machine 0x%x", machine)
	}
	for i, section := range sections {
		if section.flags&0x4 != 0 && validFileRange(d, section.fileOff, section.secSize) {
			info.Regions = append(info.Regions, ExecutableRegion{
				Name: fmt.Sprintf("section[%d]", i), Offset: int64(section.fileOff),
				Size: int64(section.secSize), Address: section.addr,
			})
		}
	}
	if len(info.Regions) == 0 {
		info.Regions = executableELFSegments(d, bo, class, phoff, phentsize, phnum)
	}
	if off, ok := elfAddressToOffset(sections, entry); ok {
		info.Entry = off
		info.Seeds = append(info.Seeds, FunctionSeed{Name: "entry", Offset: off, Address: entry, Source: "header"})
	} else if off, ok := regionAddressToOffset(info.Regions, entry); ok {
		info.Entry = off
		info.Seeds = append(info.Seeds, FunctionSeed{Name: "entry", Offset: off, Address: entry, Source: "header"})
	}
	for _, symtab := range sections {
		if symtab.typ == 2 || symtab.typ == 11 {
			info.Seeds = append(info.Seeds, executableELFFunctions(d, bo, class, sections, symtab)...)
		}
	}
	finishExecutableInfo(info)
	return info, nil
}

func executableELFSegments(d *document.Document, bo binary.ByteOrder, class byte, off uint64, entrySize, count uint16) []ExecutableRegion {
	if off == 0 || entrySize == 0 || count == 0 || count > 8192 {
		return nil
	}
	regions := make([]ExecutableRegion, 0)
	for i := uint16(0); i < count; i++ {
		buf, err := d.Range(int64(off)+int64(i)*int64(entrySize), int64(entrySize))
		if err != nil {
			break
		}
		var typ, flags uint32
		var fileOff, address, fileSize uint64
		if class == 2 {
			if len(buf) < 56 {
				break
			}
			typ, flags = bo.Uint32(buf[0:4]), bo.Uint32(buf[4:8])
			fileOff, address, fileSize = bo.Uint64(buf[8:16]), bo.Uint64(buf[16:24]), bo.Uint64(buf[32:40])
		} else {
			if len(buf) < 32 {
				break
			}
			typ, flags = bo.Uint32(buf[0:4]), bo.Uint32(buf[24:28])
			fileOff, address, fileSize = uint64(bo.Uint32(buf[4:8])), uint64(bo.Uint32(buf[8:12])), uint64(bo.Uint32(buf[16:20]))
		}
		if typ == 1 && flags&0x1 != 0 && validFileRange(d, fileOff, fileSize) {
			regions = append(regions, ExecutableRegion{
				Name: fmt.Sprintf("PT_LOAD[%d]", i), Offset: int64(fileOff), Size: int64(fileSize), Address: address,
			})
		}
	}
	return regions
}

func executableELFSections(d *document.Document, bo binary.ByteOrder, class byte, off uint64, entrySize, count uint16) ([]elfSectionInfo, error) {
	if off == 0 || entrySize == 0 || count == 0 || count > 8192 {
		return nil, errors.New("ELF has no usable section table")
	}
	sections := make([]elfSectionInfo, 0, count)
	for i := uint16(0); i < count; i++ {
		buf, err := d.Range(int64(off)+int64(i)*int64(entrySize), int64(entrySize))
		if err != nil {
			return nil, err
		}
		var section elfSectionInfo
		if class == 2 {
			if len(buf) < 64 {
				return nil, errors.New("truncated ELF64 section")
			}
			section.typ, section.flags = bo.Uint32(buf[4:8]), bo.Uint64(buf[8:16])
			section.addr, section.fileOff, section.secSize = bo.Uint64(buf[16:24]), bo.Uint64(buf[24:32]), bo.Uint64(buf[32:40])
			section.link, section.entrySize = bo.Uint32(buf[40:44]), bo.Uint64(buf[56:64])
		} else {
			if len(buf) < 40 {
				return nil, errors.New("truncated ELF32 section")
			}
			section.typ, section.flags = bo.Uint32(buf[4:8]), uint64(bo.Uint32(buf[8:12]))
			section.addr = uint64(bo.Uint32(buf[12:16]))
			section.fileOff, section.secSize = uint64(bo.Uint32(buf[16:20])), uint64(bo.Uint32(buf[20:24]))
			section.link, section.entrySize = bo.Uint32(buf[24:28]), uint64(bo.Uint32(buf[36:40]))
		}
		sections = append(sections, section)
	}
	return sections, nil
}

func executableELFFunctions(d *document.Document, bo binary.ByteOrder, class byte, sections []elfSectionInfo, symtab elfSectionInfo) []FunctionSeed {
	entrySize := symtab.entrySize
	if entrySize == 0 {
		entrySize = 16
		if class == 2 {
			entrySize = 24
		}
	}
	if !validFileRange(d, symtab.fileOff, symtab.secSize) || int(symtab.link) >= len(sections) {
		return nil
	}
	strsec := sections[symtab.link]
	if !validFileRange(d, strsec.fileOff, strsec.secSize) || strsec.secSize > 8<<20 {
		return nil
	}
	strtab, err := d.Range(int64(strsec.fileOff), int64(strsec.secSize))
	if err != nil {
		return nil
	}
	count := int(symtab.secSize / entrySize)
	if count > 16384 {
		count = 16384
	}
	out := make([]FunctionSeed, 0)
	for i := 0; i < count; i++ {
		buf, err := d.Range(int64(symtab.fileOff)+int64(i)*int64(entrySize), int64(entrySize))
		if err != nil || len(buf) < int(entrySize) {
			break
		}
		var nameOff uint32
		var info byte
		var sectionIndex uint16
		var value, size uint64
		if class == 2 {
			nameOff, info, sectionIndex = bo.Uint32(buf[0:4]), buf[4], bo.Uint16(buf[6:8])
			value, size = bo.Uint64(buf[8:16]), bo.Uint64(buf[16:24])
		} else {
			nameOff, value, size = bo.Uint32(buf[0:4]), uint64(bo.Uint32(buf[4:8])), uint64(bo.Uint32(buf[8:12]))
			info, sectionIndex = buf[12], bo.Uint16(buf[14:16])
		}
		if info&0xf != 2 || sectionIndex == 0 || value == 0 {
			continue
		}
		name := elfStringAt(strtab, nameOff)
		offset, ok := elfAddressToOffset(sections, value)
		if name == "" || !ok {
			continue
		}
		source := "symtab"
		if symtab.typ == 11 {
			source = "dynsym"
		}
		out = append(out, FunctionSeed{Name: name, Offset: offset, Address: value, Size: size, Source: source})
	}
	return out
}

func inspectPEExecutable(d *document.Document) (*ExecutableInfo, error) {
	sections, dirs, is64, err := peLayout(d)
	if err != nil {
		return nil, err
	}
	hdr, _ := d.Range(0, 64)
	lfanew := int64(binary.LittleEndian.Uint32(hdr[0x3c:0x40]))
	peHdr, _ := d.Range(lfanew, 24)
	machine := binary.LittleEndian.Uint16(peHdr[4:6])
	optSize := int64(binary.LittleEndian.Uint16(peHdr[20:22]))
	opt, err := d.Range(lfanew+24, optSize)
	if err != nil || len(opt) < 20 {
		return nil, errors.New("PE optional header too short")
	}
	entryRVA := binary.LittleEndian.Uint32(opt[16:20])
	info := &ExecutableInfo{Format: "pe", Arch: peDisasmArch(machine, is64), Entry: -1}
	if info.Arch == "" {
		return nil, fmt.Errorf("unsupported PE machine 0x%x", machine)
	}
	for _, section := range sections {
		if section.Characteristics&0x20000000 != 0 && validFileRange(d, uint64(section.RawOffset), uint64(section.RawSize)) {
			info.Regions = append(info.Regions, ExecutableRegion{
				Name: section.Name, Offset: int64(section.RawOffset), Size: int64(section.RawSize), Address: uint64(section.VirtualAddress),
			})
		}
	}
	if off, ok := peRVAToOffset(sections, entryRVA); ok {
		info.Entry = int64(off)
		info.Seeds = append(info.Seeds, FunctionSeed{Name: "entry", Offset: int64(off), Address: uint64(entryRVA), Source: "header"})
	}
	if len(dirs) > 0 && dirs[0].RVA != 0 {
		info.Seeds = append(info.Seeds, executablePEExports(d, sections, dirs[0])...)
	}
	finishExecutableInfo(info)
	return info, nil
}

func executablePEExports(d *document.Document, sections []peSectionInfo, dir peDataDirectory) []FunctionSeed {
	off, ok := peRVAToOffset(sections, dir.RVA)
	if !ok {
		return nil
	}
	buf, err := d.Range(int64(off), 40)
	if err != nil || len(buf) < 40 {
		return nil
	}
	functionCount, nameCount := binary.LittleEndian.Uint32(buf[20:24]), binary.LittleEndian.Uint32(buf[24:28])
	functionsOff, funcsOK := peRVAToOffset(sections, binary.LittleEndian.Uint32(buf[28:32]))
	namesOff, namesOK := peRVAToOffset(sections, binary.LittleEndian.Uint32(buf[32:36]))
	ordinalsOff, ordOK := peRVAToOffset(sections, binary.LittleEndian.Uint32(buf[36:40]))
	if !funcsOK || !namesOK || !ordOK {
		return nil
	}
	if nameCount > 16384 {
		nameCount = 16384
	}
	out := make([]FunctionSeed, 0, nameCount)
	for i := uint32(0); i < nameCount; i++ {
		namePtr, nameErr := d.Range(int64(namesOff)+int64(i)*4, 4)
		ordBuf, ordErr := d.Range(int64(ordinalsOff)+int64(i)*2, 2)
		if nameErr != nil || ordErr != nil || len(namePtr) < 4 || len(ordBuf) < 2 {
			break
		}
		index := uint32(binary.LittleEndian.Uint16(ordBuf))
		if index >= functionCount {
			continue
		}
		funcBuf, err := d.Range(int64(functionsOff)+int64(index)*4, 4)
		if err != nil || len(funcBuf) < 4 {
			break
		}
		funcRVA := binary.LittleEndian.Uint32(funcBuf)
		if funcRVA >= dir.RVA && funcRVA < dir.RVA+dir.Size {
			continue
		}
		nameOff, nameOK := peRVAToOffset(sections, binary.LittleEndian.Uint32(namePtr))
		funcOff, funcOK := peRVAToOffset(sections, funcRVA)
		if !nameOK || !funcOK {
			continue
		}
		name := readCString(d, int64(nameOff), 512)
		if name != "" {
			out = append(out, FunctionSeed{Name: name, Offset: int64(funcOff), Address: uint64(funcRVA), Source: "export"})
		}
	}
	return out
}

func validFileRange(d *document.Document, offset, size uint64) bool {
	return size > 0 && offset <= uint64(d.Size()) && size <= uint64(d.Size())-offset
}

func elfAddressToOffset(sections []elfSectionInfo, address uint64) (int64, bool) {
	for _, section := range sections {
		if section.secSize > 0 && address >= section.addr && address < section.addr+section.secSize {
			return int64(section.fileOff + address - section.addr), true
		}
	}
	return 0, false
}

func regionAddressToOffset(regions []ExecutableRegion, address uint64) (int64, bool) {
	for _, region := range regions {
		if address >= region.Address && address < region.Address+uint64(region.Size) {
			return region.Offset + int64(address-region.Address), true
		}
	}
	return 0, false
}

func elfDisasmArch(machine uint16, data byte) string {
	switch machine {
	case 3:
		return "x86"
	case 8:
		if data == 1 {
			return "mipsle"
		}
		return "mips"
	case 40:
		return "arm32"
	case 62:
		return "x86_64"
	case 183:
		return "aarch64"
	}
	return ""
}

func peDisasmArch(machine uint16, _ bool) string {
	switch machine {
	case 0x14c:
		return "x86"
	case 0x8664:
		return "x86_64"
	case 0x1c0, 0x1c4:
		return "arm32"
	case 0xaa64:
		return "aarch64"
	case 0x166, 0x266, 0x366, 0x466:
		return "mipsle"
	}
	return ""
}

func finishExecutableInfo(info *ExecutableInfo) {
	sort.Slice(info.Regions, func(i, j int) bool { return info.Regions[i].Offset < info.Regions[j].Offset })
	sort.Slice(info.Seeds, func(i, j int) bool {
		if info.Seeds[i].Offset != info.Seeds[j].Offset {
			return info.Seeds[i].Offset < info.Seeds[j].Offset
		}
		return info.Seeds[i].Name < info.Seeds[j].Name
	})
	unique := info.Seeds[:0]
	for _, seed := range info.Seeds {
		if seed.Offset < 0 || !offsetInRegions(info.Regions, seed.Offset) {
			continue
		}
		if len(unique) > 0 && unique[len(unique)-1].Offset == seed.Offset {
			if unique[len(unique)-1].Name == "entry" && seed.Name != "entry" {
				unique[len(unique)-1] = seed
			}
			continue
		}
		unique = append(unique, seed)
	}
	info.Seeds = unique
}

func offsetInRegions(regions []ExecutableRegion, offset int64) bool {
	for _, region := range regions {
		if offset >= region.Offset && offset < region.Offset+region.Size {
			return true
		}
	}
	return false
}
