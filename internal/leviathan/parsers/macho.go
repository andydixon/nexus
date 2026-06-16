package parsers

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// Mach-O magic values (32/64, LE/BE) plus fat (universal) magic.
const (
	machoMagic32    = 0xfeedface
	machoCigam32    = 0xcefaedfe
	machoMagic64    = 0xfeedfacf
	machoCigam64    = 0xcffaedfe
	machoFatMagic   = 0xcafebabe // also Java .class; disambiguate by position + nfat_arch
	machoFatCigam   = 0xbebafeca
	machoFatMagic64 = 0xcafebabf
	machoFatCigam64 = 0xbfbafeca
)

// IsMachO reports whether the given prefix looks like a Mach-O header.
// Fat magic 0xcafebabe is shared with Java class files; we accept it
// only when nfat_arch is plausible and the first arch's cputype points
// somewhere in-document.
func IsMachO(head []byte, size int64) bool {
	if len(head) < 8 {
		return false
	}
	m := binary.LittleEndian.Uint32(head[:4])
	switch m {
	case machoMagic32, machoMagic64, machoCigam32, machoCigam64:
		return true
	}
	mBE := binary.BigEndian.Uint32(head[:4])
	switch mBE {
	case machoFatMagic, machoFatMagic64:
		// nfat_arch is next BE u32; plausibly small.
		n := binary.BigEndian.Uint32(head[4:8])
		return n > 0 && n < 64 && size >= 8+int64(n)*20
	}
	_ = mBE
	return false
}

// ParseMachO returns a field tree for a Mach-O (fat or thin) file.
func ParseMachO(d *document.Document) (*Field, error) {
	head, err := d.Range(0, min64(8, d.Size()))
	if err != nil {
		return nil, err
	}
	if len(head) < 8 {
		return nil, errors.New("file too small for Mach-O")
	}
	mLE := binary.LittleEndian.Uint32(head[:4])
	mBE := binary.BigEndian.Uint32(head[:4])

	switch mBE {
	case machoFatMagic, machoFatMagic64:
		return parseMachOFat(d, mBE == machoFatMagic64)
	}

	// Thin header.
	switch mLE {
	case machoMagic32:
		return parseMachOThin(d, 0, binary.LittleEndian, false)
	case machoMagic64:
		return parseMachOThin(d, 0, binary.LittleEndian, true)
	case machoCigam32:
		return parseMachOThin(d, 0, binary.BigEndian, false)
	case machoCigam64:
		return parseMachOThin(d, 0, binary.BigEndian, true)
	}
	return nil, errors.New("not a Mach-O file")
}

func parseMachOFat(d *document.Document, is64 bool) (*Field, error) {
	root := &Field{Name: "Mach-O fat", Type: "container", Offset: 0}
	hdr, err := d.Range(0, 8)
	if err != nil {
		return nil, err
	}
	n := int64(binary.BigEndian.Uint32(hdr[4:8]))
	root.Children = append(root.Children, &Field{
		Name: "magic", Type: "u32", Offset: 0, Length: 4,
		Value: fmt.Sprintf("0x%08x", binary.BigEndian.Uint32(hdr[:4])),
	})
	root.Children = append(root.Children, &Field{
		Name: "nfat_arch", Type: "u32", Offset: 4, Length: 4,
		Value: fmt.Sprintf("%d", n),
	})

	archSize := int64(20)
	if is64 {
		archSize = 32
	}
	off := int64(8)
	for i := int64(0); i < n && off+archSize <= d.Size(); i++ {
		arch, err := d.Range(off, archSize)
		if err != nil {
			break
		}
		cpuType := binary.BigEndian.Uint32(arch[0:4])
		cpuSub := binary.BigEndian.Uint32(arch[4:8])
		var archOff, archLen int64
		if is64 {
			archOff = int64(binary.BigEndian.Uint64(arch[8:16]))
			archLen = int64(binary.BigEndian.Uint64(arch[16:24]))
		} else {
			archOff = int64(binary.BigEndian.Uint32(arch[8:12]))
			archLen = int64(binary.BigEndian.Uint32(arch[12:16]))
		}
		node := &Field{
			Name:   fmt.Sprintf("arch[%d]", i),
			Type:   "fat_arch",
			Offset: off,
			Length: archSize,
			Value:  fmt.Sprintf("cpu=%s sub=0x%x offset=%d size=%d", cpuTypeName(cpuType), cpuSub, archOff, archLen),
		}
		// Parse the nested Mach-O at archOff if it fits.
		if archOff > 0 && archOff+8 <= d.Size() {
			if nested, err := parseMachOInline(d, archOff); err == nil {
				node.Children = append(node.Children, nested)
			}
		}
		root.Children = append(root.Children, node)
		off += archSize
	}
	return root, nil
}

// parseMachOInline is like ParseMachO but anchored at a given offset
// (used for each arch inside a fat binary).
func parseMachOInline(d *document.Document, at int64) (*Field, error) {
	head, err := d.Range(at, 8)
	if err != nil || len(head) < 8 {
		return nil, errors.New("short arch payload")
	}
	mLE := binary.LittleEndian.Uint32(head[:4])
	switch mLE {
	case machoMagic32:
		return parseMachOThin(d, at, binary.LittleEndian, false)
	case machoMagic64:
		return parseMachOThin(d, at, binary.LittleEndian, true)
	case machoCigam32:
		return parseMachOThin(d, at, binary.BigEndian, false)
	case machoCigam64:
		return parseMachOThin(d, at, binary.BigEndian, true)
	}
	return nil, errors.New("arch payload is not Mach-O")
}

func parseMachOThin(d *document.Document, base int64, bo binary.ByteOrder, is64 bool) (*Field, error) {
	headerLen := int64(28)
	if is64 {
		headerLen = 32
	}
	if base+headerLen > d.Size() {
		return nil, errors.New("truncated Mach-O header")
	}
	hdr, err := d.Range(base, headerLen)
	if err != nil {
		return nil, err
	}

	magic := bo.Uint32(hdr[0:4])
	cpuType := bo.Uint32(hdr[4:8])
	cpuSub := bo.Uint32(hdr[8:12])
	fileType := bo.Uint32(hdr[12:16])
	ncmds := bo.Uint32(hdr[16:20])
	sizeOfCmds := bo.Uint32(hdr[20:24])
	flags := bo.Uint32(hdr[24:28])

	name := "Mach-O (32)"
	if is64 {
		name = "Mach-O (64)"
	}
	root := &Field{Name: name, Type: "container", Offset: base, Length: headerLen + int64(sizeOfCmds)}
	root.Children = append(root.Children,
		&Field{Name: "magic", Type: "u32", Offset: base, Length: 4, Value: fmt.Sprintf("0x%08x", magic)},
		&Field{Name: "cputype", Type: "u32", Offset: base + 4, Length: 4, Value: cpuTypeName(cpuType)},
		&Field{Name: "cpusubtype", Type: "u32", Offset: base + 8, Length: 4, Value: fmt.Sprintf("0x%x", cpuSub)},
		&Field{Name: "filetype", Type: "u32", Offset: base + 12, Length: 4, Value: machoFileType(fileType)},
		&Field{Name: "ncmds", Type: "u32", Offset: base + 16, Length: 4, Value: fmt.Sprintf("%d", ncmds)},
		&Field{Name: "sizeofcmds", Type: "u32", Offset: base + 20, Length: 4, Value: fmt.Sprintf("%d", sizeOfCmds)},
		&Field{Name: "flags", Type: "u32", Offset: base + 24, Length: 4, Value: fmt.Sprintf("0x%08x", flags)},
	)

	// Load commands.
	cmdOff := base + headerLen
	cmdEnd := cmdOff + int64(sizeOfCmds)
	if cmdEnd > d.Size() {
		cmdEnd = d.Size()
	}
	cmds := &Field{Name: "load_commands", Type: "list", Offset: cmdOff, Length: int64(sizeOfCmds)}
	for i := uint32(0); i < ncmds && cmdOff+8 <= cmdEnd; i++ {
		h, err := d.Range(cmdOff, 8)
		if err != nil {
			break
		}
		cmd := bo.Uint32(h[0:4])
		sz := int64(bo.Uint32(h[4:8]))
		if sz < 8 || cmdOff+sz > cmdEnd {
			break
		}
		buf, _ := d.Range(cmdOff, sz)
		node := decodeLoadCommand(base, cmdOff, cmd, sz, buf, bo, is64)
		cmds.Children = append(cmds.Children, node)
		cmdOff += sz
	}
	root.Children = append(root.Children, cmds)
	return root, nil
}

func decodeLoadCommand(base, off int64, cmd uint32, sz int64, buf []byte, bo binary.ByteOrder, is64 bool) *Field {
	name, mask := loadCommandName(cmd)
	node := &Field{
		Name: name, Type: "load_command", Offset: off, Length: sz,
		Value: fmt.Sprintf("cmd=0x%08x size=%d", cmd, sz),
	}
	if mask != 0 {
		node.Note = "required"
	}

	switch {
	case (cmd&^0x80000000) == 0x19 && is64 && sz >= 72:
		// LC_SEGMENT_64
		segName := cstring(buf[8:24])
		vmAddr := bo.Uint64(buf[24:32])
		vmSize := bo.Uint64(buf[32:40])
		fileOff := bo.Uint64(buf[40:48])
		fileSize := bo.Uint64(buf[48:56])
		nsects := bo.Uint32(buf[64:68])
		node.Value = fmt.Sprintf("name=%q vm=%#x+%#x file=%#x+%#x nsects=%d",
			segName, vmAddr, vmSize, fileOff, fileSize, nsects)
		sectOff := int64(72)
		for i := uint32(0); i < nsects && sectOff+80 <= sz; i++ {
			s := buf[sectOff:]
			sn := cstring(s[0:16])
			seg := cstring(s[16:32])
			addr := bo.Uint64(s[32:40])
			size := bo.Uint64(s[40:48])
			foff := bo.Uint32(s[48:52])
			node.Children = append(node.Children, &Field{
				Name: sn, Type: "section64", Offset: off + sectOff, Length: 80,
				Value: fmt.Sprintf("seg=%q addr=%#x size=%#x file=%#x", seg, addr, size, foff),
			})
			sectOff += 80
		}
	case cmd == 0x01 && sz >= 56:
		// LC_SEGMENT (32)
		segName := cstring(buf[8:24])
		vmAddr := bo.Uint32(buf[24:28])
		vmSize := bo.Uint32(buf[28:32])
		fileOff := bo.Uint32(buf[32:36])
		fileSize := bo.Uint32(buf[36:40])
		nsects := bo.Uint32(buf[48:52])
		node.Value = fmt.Sprintf("name=%q vm=%#x+%#x file=%#x+%#x nsects=%d",
			segName, vmAddr, vmSize, fileOff, fileSize, nsects)
	case cmd == 0x1b && sz >= 24:
		// LC_UUID
		u := buf[8:24]
		node.Value = fmt.Sprintf("UUID=%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	case (cmd&^0x80000000) == 0x0c && sz > 24:
		// LC_LOAD_DYLIB / similar
		strOff := bo.Uint32(buf[8:12])
		if int(strOff) < len(buf) {
			node.Value = fmt.Sprintf("lib=%q", cstring(buf[strOff:]))
		}
	case cmd == 0x0e && sz > 12:
		// LC_LOAD_DYLINKER
		strOff := bo.Uint32(buf[8:12])
		if int(strOff) < len(buf) {
			node.Value = fmt.Sprintf("dylinker=%q", cstring(buf[strOff:]))
		}
	case cmd == 0x21 && sz >= 16:
		// LC_ENCRYPTION_INFO / _64
		node.Value = "encryption info"
	case (cmd &^ 0x80000000) == 0x22:
		node.Value = "LC_DYLD_INFO"
	case cmd == 0x25:
		node.Value = "LC_VERSION_MIN_MACOSX"
	case cmd == 0x28 || cmd == 0x29:
		node.Value = "LC_MAIN / entry point"
	}
	return node
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func cpuTypeName(c uint32) string {
	switch c {
	case 0x7:
		return "x86"
	case 0x1000007:
		return "x86_64"
	case 0xc:
		return "arm"
	case 0x100000c:
		return "arm64"
	case 0x12:
		return "ppc"
	case 0x1000012:
		return "ppc64"
	}
	return fmt.Sprintf("0x%x", c)
}

func machoFileType(t uint32) string {
	switch t {
	case 1:
		return "OBJECT"
	case 2:
		return "EXECUTE"
	case 3:
		return "FVMLIB"
	case 4:
		return "CORE"
	case 5:
		return "PRELOAD"
	case 6:
		return "DYLIB"
	case 7:
		return "DYLINKER"
	case 8:
		return "BUNDLE"
	case 9:
		return "DYLIB_STUB"
	case 10:
		return "DSYM"
	case 11:
		return "KEXT_BUNDLE"
	}
	return fmt.Sprintf("%d", t)
}

// loadCommandName returns a human name and whether the required bit is set.
func loadCommandName(cmd uint32) (string, uint32) {
	req := cmd & 0x80000000
	v := cmd &^ 0x80000000
	names := map[uint32]string{
		0x01: "LC_SEGMENT",
		0x02: "LC_SYMTAB",
		0x03: "LC_SYMSEG",
		0x04: "LC_THREAD",
		0x05: "LC_UNIXTHREAD",
		0x06: "LC_LOADFVMLIB",
		0x07: "LC_IDFVMLIB",
		0x08: "LC_IDENT",
		0x09: "LC_FVMFILE",
		0x0a: "LC_PREPAGE",
		0x0b: "LC_DYSYMTAB",
		0x0c: "LC_LOAD_DYLIB",
		0x0d: "LC_ID_DYLIB",
		0x0e: "LC_LOAD_DYLINKER",
		0x0f: "LC_ID_DYLINKER",
		0x10: "LC_PREBOUND_DYLIB",
		0x11: "LC_ROUTINES",
		0x12: "LC_SUB_FRAMEWORK",
		0x13: "LC_SUB_UMBRELLA",
		0x14: "LC_SUB_CLIENT",
		0x15: "LC_SUB_LIBRARY",
		0x16: "LC_TWOLEVEL_HINTS",
		0x17: "LC_PREBIND_CKSUM",
		0x18: "LC_LOAD_WEAK_DYLIB",
		0x19: "LC_SEGMENT_64",
		0x1a: "LC_ROUTINES_64",
		0x1b: "LC_UUID",
		0x1c: "LC_RPATH",
		0x1d: "LC_CODE_SIGNATURE",
		0x1e: "LC_SEGMENT_SPLIT_INFO",
		0x1f: "LC_REEXPORT_DYLIB",
		0x20: "LC_LAZY_LOAD_DYLIB",
		0x21: "LC_ENCRYPTION_INFO",
		0x22: "LC_DYLD_INFO",
		0x23: "LC_LOAD_UPWARD_DYLIB",
		0x24: "LC_VERSION_MIN_MACOSX",
		0x25: "LC_VERSION_MIN_IPHONEOS",
		0x26: "LC_FUNCTION_STARTS",
		0x27: "LC_DYLD_ENVIRONMENT",
		0x28: "LC_MAIN",
		0x29: "LC_DATA_IN_CODE",
		0x2a: "LC_SOURCE_VERSION",
		0x2b: "LC_DYLIB_CODE_SIGN_DRS",
		0x2c: "LC_ENCRYPTION_INFO_64",
		0x2d: "LC_LINKER_OPTION",
		0x2e: "LC_LINKER_OPTIMIZATION_HINT",
		0x2f: "LC_VERSION_MIN_TVOS",
		0x30: "LC_VERSION_MIN_WATCHOS",
		0x31: "LC_NOTE",
		0x32: "LC_BUILD_VERSION",
		0x33: "LC_DYLD_EXPORTS_TRIE",
		0x34: "LC_DYLD_CHAINED_FIXUPS",
		0x35: "LC_FILESET_ENTRY",
	}
	if n, ok := names[v]; ok {
		return n, req
	}
	return fmt.Sprintf("LC_0x%x", v), req
}
