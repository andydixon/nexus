package parsers

import (
	"encoding/binary"
	"errors"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// ImportSym is one resolved PE import (a function name, or an ordinal).
type ImportSym struct {
	DLL       string `json:"dll"`
	Func      string `json:"func"`
	Ordinal   int    `json:"ordinal"`
	ByOrdinal bool   `json:"byOrdinal"`
}

// PEImports walks the import directory of a PE file and returns the ordered
// list of imported symbols. It reuses the same RVA mapping and header parsing
// as ParsePE but yields a clean typed result (for imphash, capability
// analysis, etc.) rather than a display tree.
func PEImports(d *document.Document) ([]ImportSym, error) {
	sections, dataDirs, is64, err := peLayout(d)
	if err != nil {
		return nil, err
	}
	if len(dataDirs) < 2 || dataDirs[1].RVA == 0 {
		return nil, errors.New("no import directory")
	}
	off, ok := peRVAToOffset(sections, dataDirs[1].RVA)
	if !ok {
		return nil, errors.New("import RVA unmapped")
	}
	var out []ImportSym
	for i := 0; i < 1024; i++ {
		at := int64(off) + int64(i*20)
		buf, err := d.Range(at, 20)
		if err != nil || len(buf) < 20 {
			break
		}
		oft := binary.LittleEndian.Uint32(buf[0:4])
		nameRVA := binary.LittleEndian.Uint32(buf[12:16])
		firstThunk := binary.LittleEndian.Uint32(buf[16:20])
		if oft == 0 && nameRVA == 0 && firstThunk == 0 {
			break
		}
		dll := ""
		if nameOff, ok := peRVAToOffset(sections, nameRVA); ok {
			dll = readCString(d, int64(nameOff), 256)
		}
		if dll == "" {
			continue
		}
		out = append(out, peThunkSyms(d, sections, dll, oft, firstThunk, is64)...)
		if len(out) > 100000 {
			break
		}
	}
	return out, nil
}

func peThunkSyms(d *document.Document, sections []peSectionInfo, dll string, oft, firstThunk uint32, is64 bool) []ImportSym {
	thunkRVA := oft
	if thunkRVA == 0 {
		thunkRVA = firstThunk
	}
	thunkOff, ok := peRVAToOffset(sections, thunkRVA)
	if !ok {
		return nil
	}
	width := int64(4)
	ordinalMask := uint64(0x80000000)
	if is64 {
		width = 8
		ordinalMask = 0x8000000000000000
	}
	var out []ImportSym
	for i := 0; i < 8192; i++ {
		at := int64(thunkOff) + int64(i)*width
		buf, err := d.Range(at, width)
		if err != nil || int64(len(buf)) < width {
			break
		}
		var thunk uint64
		if is64 {
			thunk = binary.LittleEndian.Uint64(buf)
		} else {
			thunk = uint64(binary.LittleEndian.Uint32(buf))
		}
		if thunk == 0 {
			break
		}
		if thunk&ordinalMask != 0 {
			out = append(out, ImportSym{DLL: dll, Ordinal: int(thunk & 0xffff), ByOrdinal: true})
		} else if nameOff, ok := peRVAToOffset(sections, uint32(thunk)); ok {
			name := readImportByName(d, int64(nameOff))
			if name != "" {
				out = append(out, ImportSym{DLL: dll, Func: name})
			}
		}
	}
	return out
}

// peLayout extracts the section table, data directories and 32/64-bitness from
// a PE without building the display field tree.
func peLayout(d *document.Document) ([]peSectionInfo, []peDataDirectory, bool, error) {
	hdr, err := d.Range(0, 64)
	if err != nil || len(hdr) < 64 {
		return nil, nil, false, errors.New("too short")
	}
	lfanew := int64(binary.LittleEndian.Uint32(hdr[0x3c:0x40]))
	peHdr, err := d.Range(lfanew, 24)
	if err != nil || len(peHdr) < 24 || string(peHdr[:4]) != "PE\x00\x00" {
		return nil, nil, false, errors.New("no PE signature")
	}
	numSections := int(binary.LittleEndian.Uint16(peHdr[6:8]))
	sizeOptHdr := int64(binary.LittleEndian.Uint16(peHdr[20:22]))
	optOff := lfanew + 24

	var dataDirs []peDataDirectory
	is64 := false
	if sizeOptHdr > 0 {
		opt, err := d.Range(optOff, sizeOptHdr)
		if err == nil && len(opt) >= 2 {
			_, dirs := parsePEOptionalHeader(opt, optOff)
			dataDirs = dirs
			is64 = binary.LittleEndian.Uint16(opt[0:2]) == 0x20b
		}
	}

	sectOff := lfanew + 24 + sizeOptHdr
	sections := make([]peSectionInfo, 0, numSections)
	for i := 0; i < numSections; i++ {
		sh, err := d.Range(sectOff, 40)
		if err != nil || len(sh) < 40 {
			break
		}
		sections = append(sections, peSectionInfo{
			Name:            string(trimZero(sh[0:8])),
			VirtualSize:     binary.LittleEndian.Uint32(sh[8:12]),
			VirtualAddress:  binary.LittleEndian.Uint32(sh[12:16]),
			RawSize:         binary.LittleEndian.Uint32(sh[16:20]),
			RawOffset:       binary.LittleEndian.Uint32(sh[20:24]),
			Characteristics: binary.LittleEndian.Uint32(sh[36:40]),
		})
		sectOff += 40
	}
	return sections, dataDirs, is64, nil
}
