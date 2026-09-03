// Additional structure parsers that round out the structures panel into a
// forensic-grade set: gzip, tar, SQLite, TIFF, pcap/pcapng and the OLE/CFB
// compound-file container used by legacy Office documents and MSI installers.
//
// Each parser follows the same contract as the parsers in formats.go: it
// returns a *Field tree anchored at absolute document offsets, never panics on
// truncated input, and bounds every loop so a hostile file cannot wedge the UI.
package parsers

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/andydixon/link/internal/leviathan/document"
)

// leaf builds a terminal Field with no children.
func leaf(name, typ string, off, length int64, value string) *Field {
	return &Field{Name: name, Type: typ, Offset: off, Length: length, Value: value}
}

// ---------- gzip (RFC 1952) ----------

// ParseGZIP decodes the gzip member header, optional name/comment/extra fields
// and the trailing CRC-32 / ISIZE footer.
func ParseGZIP(d *document.Document) (*Field, error) {
	size := d.Size()
	hdr, err := d.Range(0, min64(10, size))
	if err != nil || len(hdr) < 10 {
		return nil, errors.New("gzip too short")
	}
	if hdr[0] != 0x1f || hdr[1] != 0x8b {
		return nil, errors.New("not gzip")
	}
	root := &Field{Name: "GZIP", Type: "container", Offset: 0, Length: size}
	method := hdr[2]
	methodName := "deflate"
	if method != 8 {
		methodName = fmt.Sprintf("unknown(%d)", method)
	}
	flg := hdr[3]
	mtime := binary.LittleEndian.Uint32(hdr[4:8])
	root.Children = append(root.Children,
		leaf("id", "bytes", 0, 2, "\\x1f\\x8b"),
		leaf("method", "u8", 2, 1, methodName),
		leaf("flags", "u8", 3, 1, gzipFlags(flg)),
		leaf("mtime", "u32le", 4, 4, gzipTime(mtime)),
		leaf("extra_flags", "u8", 8, 1, fmt.Sprintf("%d", hdr[8])),
		leaf("os", "u8", 9, 1, gzipOS(hdr[9])),
	)
	off := int64(10)
	// FEXTRA
	if flg&0x04 != 0 && off+2 <= size {
		xb, _ := d.Range(off, 2)
		xlen := int64(binary.LittleEndian.Uint16(xb))
		root.Children = append(root.Children, leaf("extra", "bytes", off, 2+xlen, fmt.Sprintf("%d bytes", xlen)))
		off += 2 + xlen
	}
	// FNAME
	if flg&0x08 != 0 {
		s, n := readCStringField(d, off, size)
		root.Children = append(root.Children, leaf("name", "asciiz", off, n, s))
		off += n
	}
	// FCOMMENT
	if flg&0x10 != 0 {
		s, n := readCStringField(d, off, size)
		root.Children = append(root.Children, leaf("comment", "asciiz", off, n, s))
		off += n
	}
	// FHCRC
	if flg&0x02 != 0 && off+2 <= size {
		hb, _ := d.Range(off, 2)
		root.Children = append(root.Children, leaf("header_crc16", "u16le", off, 2, fmt.Sprintf("0x%04x", binary.LittleEndian.Uint16(hb))))
		off += 2
	}
	if size >= off+8 {
		root.Children = append(root.Children, leaf("compressed_data", "bytes", off, size-off-8, fmt.Sprintf("%d bytes", size-off-8)))
		ft, _ := d.Range(size-8, 8)
		root.Children = append(root.Children,
			leaf("crc32", "u32le", size-8, 4, fmt.Sprintf("0x%08x", binary.LittleEndian.Uint32(ft[0:4]))),
			leaf("isize", "u32le", size-4, 4, fmt.Sprintf("%d bytes (mod 2^32)", binary.LittleEndian.Uint32(ft[4:8]))),
		)
	}
	return root, nil
}

func gzipFlags(f byte) string {
	var on []string
	for _, b := range []struct {
		mask byte
		name string
	}{{0x01, "TEXT"}, {0x02, "HCRC"}, {0x04, "EXTRA"}, {0x08, "NAME"}, {0x10, "COMMENT"}} {
		if f&b.mask != 0 {
			on = append(on, b.name)
		}
	}
	if len(on) == 0 {
		return fmt.Sprintf("0x%02x", f)
	}
	return fmt.Sprintf("0x%02x (%s)", f, strings.Join(on, "|"))
}

func gzipOS(b byte) string {
	names := map[byte]string{0: "FAT", 1: "Amiga", 2: "VMS", 3: "Unix", 6: "HPFS", 7: "Macintosh", 11: "NTFS", 255: "unknown"}
	if n, ok := names[b]; ok {
		return fmt.Sprintf("%d (%s)", b, n)
	}
	return fmt.Sprintf("%d", b)
}

func gzipTime(t uint32) string {
	if t == 0 {
		return "0 (no timestamp)"
	}
	return fmt.Sprintf("%d (%s)", t, time.Unix(int64(t), 0).UTC().Format("2006-01-02 15:04:05 UTC"))
}

// ---------- tar (POSIX ustar) ----------

// ParseTAR walks the 512-byte record headers of a ustar/GNU tar archive.
func ParseTAR(d *document.Document) (*Field, error) {
	size := d.Size()
	root := &Field{Name: "TAR", Type: "container", Offset: 0}
	off := int64(0)
	entries := 0
	for off+512 <= size && entries < 10000 {
		blk, err := d.Range(off, 512)
		if err != nil {
			break
		}
		if allZero(blk) {
			root.Children = append(root.Children, leaf("end_of_archive", "bytes", off, 512, "zero block"))
			off += 512
			break
		}
		name := trimZeroStr(blk[0:100])
		prefix := trimZeroStr(blk[345:500])
		full := name
		if prefix != "" {
			full = prefix + "/" + name
		}
		fsize := octal(blk[124:136])
		mtime := octal(blk[136:148])
		typ := blk[156]
		e := &Field{
			Name: full, Type: "entry", Offset: off,
			Length: 512 + roundUp(fsize, 512),
			Value:  fmt.Sprintf("%s size=%d", tarType(typ), fsize),
			Children: []*Field{
				leaf("name", "asciiz", off, 100, name),
				leaf("mode", "octal", off+100, 8, fmt.Sprintf("0%o", octal(blk[100:108]))),
				leaf("uid", "octal", off+108, 8, fmt.Sprintf("%d", octal(blk[108:116]))),
				leaf("gid", "octal", off+116, 8, fmt.Sprintf("%d", octal(blk[116:124]))),
				leaf("size", "octal", off+124, 12, fmt.Sprintf("%d", fsize)),
				leaf("mtime", "octal", off+136, 12, tarTime(mtime)),
				leaf("typeflag", "u8", off+156, 1, tarType(typ)),
				leaf("magic", "ascii", off+257, 8, trimZeroStr(blk[257:265])),
				leaf("uname", "asciiz", off+265, 32, trimZeroStr(blk[265:297])),
				leaf("gname", "asciiz", off+297, 32, trimZeroStr(blk[297:329])),
			},
		}
		if prefix != "" {
			e.Children = append(e.Children, leaf("prefix", "asciiz", off+345, 155, prefix))
		}
		root.Children = append(root.Children, e)
		off += 512 + roundUp(fsize, 512)
		entries++
	}
	root.Length = off
	if len(root.Children) == 0 {
		return nil, errors.New("no tar entries")
	}
	return root, nil
}

func tarType(t byte) string {
	switch t {
	case 0, '0':
		return "file"
	case '1':
		return "hardlink"
	case '2':
		return "symlink"
	case '3':
		return "char-device"
	case '4':
		return "block-device"
	case '5':
		return "directory"
	case '6':
		return "fifo"
	case '7':
		return "contiguous"
	case 'x':
		return "pax-header"
	case 'g':
		return "pax-global"
	case 'L':
		return "gnu-longname"
	case 'K':
		return "gnu-longlink"
	default:
		return fmt.Sprintf("type-%q", t)
	}
}

func tarTime(t int64) string {
	if t == 0 {
		return "0"
	}
	return fmt.Sprintf("%d (%s)", t, time.Unix(t, 0).UTC().Format("2006-01-02 15:04:05 UTC"))
}

// ---------- SQLite 3 ----------

// ParseSQLite decodes the 100-byte SQLite database header.
func ParseSQLite(d *document.Document) (*Field, error) {
	hdr, err := d.Range(0, min64(100, d.Size()))
	if err != nil || len(hdr) < 100 {
		return nil, errors.New("sqlite header too short")
	}
	if string(hdr[0:16]) != "SQLite format 3\x00" {
		return nil, errors.New("not a SQLite database")
	}
	be := binary.BigEndian
	pageSize := int64(be.Uint16(hdr[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	enc := map[uint32]string{1: "UTF-8", 2: "UTF-16le", 3: "UTF-16be"}[be.Uint32(hdr[56:60])]
	if enc == "" {
		enc = "unknown"
	}
	root := &Field{
		Name: "SQLite", Type: "container", Offset: 0, Length: d.Size(),
		Value: fmt.Sprintf("page=%d pages=%d %s", pageSize, be.Uint32(hdr[28:32]), enc),
		Children: []*Field{
			leaf("magic", "ascii", 0, 16, "SQLite format 3"),
			leaf("page_size", "u16be", 16, 2, fmt.Sprintf("%d", pageSize)),
			leaf("write_version", "u8", 18, 1, sqliteFmtVer(hdr[18])),
			leaf("read_version", "u8", 19, 1, sqliteFmtVer(hdr[19])),
			leaf("reserved_space", "u8", 20, 1, fmt.Sprintf("%d", hdr[20])),
			leaf("change_counter", "u32be", 24, 4, fmt.Sprintf("%d", be.Uint32(hdr[24:28]))),
			leaf("page_count", "u32be", 28, 4, fmt.Sprintf("%d", be.Uint32(hdr[28:32]))),
			leaf("freelist_head", "u32be", 32, 4, fmt.Sprintf("%d", be.Uint32(hdr[32:36]))),
			leaf("freelist_pages", "u32be", 36, 4, fmt.Sprintf("%d", be.Uint32(hdr[36:40]))),
			leaf("schema_cookie", "u32be", 40, 4, fmt.Sprintf("%d", be.Uint32(hdr[40:44]))),
			leaf("schema_format", "u32be", 44, 4, fmt.Sprintf("%d", be.Uint32(hdr[44:48]))),
			leaf("text_encoding", "u32be", 56, 4, enc),
			leaf("user_version", "u32be", 60, 4, fmt.Sprintf("%d", be.Uint32(hdr[60:64]))),
			leaf("vacuum_mode", "u32be", 64, 4, fmt.Sprintf("%d", be.Uint32(hdr[64:68]))),
			leaf("application_id", "u32be", 68, 4, fmt.Sprintf("0x%08x", be.Uint32(hdr[68:72]))),
			leaf("version_valid_for", "u32be", 92, 4, fmt.Sprintf("%d", be.Uint32(hdr[92:96]))),
			leaf("sqlite_version", "u32be", 96, 4, sqliteVersion(be.Uint32(hdr[96:100]))),
		},
	}
	return root, nil
}

func sqliteFmtVer(b byte) string {
	switch b {
	case 1:
		return "1 (legacy/rollback)"
	case 2:
		return "2 (WAL)"
	default:
		return fmt.Sprintf("%d", b)
	}
}

func sqliteVersion(v uint32) string {
	// SQLITE_VERSION_NUMBER = major*1_000_000 + minor*1000 + patch.
	return fmt.Sprintf("%d (%d.%d.%d)", v, v/1000000, (v/1000)%1000, v%1000)
}

// ---------- TIFF ----------

// ParseTIFF decodes the TIFF header and the entries of each image file
// directory (IFD), following the next-IFD chain.
func ParseTIFF(d *document.Document) (*Field, error) {
	size := d.Size()
	hdr, err := d.Range(0, min64(8, size))
	if err != nil || len(hdr) < 8 {
		return nil, errors.New("tiff too short")
	}
	var bo binary.ByteOrder
	switch string(hdr[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, errors.New("not TIFF")
	}
	if bo.Uint16(hdr[2:4]) != 42 {
		return nil, errors.New("bad TIFF magic")
	}
	order := "little-endian"
	if string(hdr[0:2]) == "MM" {
		order = "big-endian"
	}
	root := &Field{
		Name: "TIFF", Type: "container", Offset: 0, Length: size, Value: order,
		Children: []*Field{
			leaf("byte_order", "ascii", 0, 2, order),
			leaf("magic", "u16", 2, 2, "42"),
			leaf("first_ifd", "u32", 4, 4, fmt.Sprintf("0x%x", bo.Uint32(hdr[4:8]))),
		},
	}
	ifdOff := int64(bo.Uint32(hdr[4:8]))
	for n := 0; ifdOff > 0 && ifdOff+2 <= size && n < 64; n++ {
		cb, err := d.Range(ifdOff, 2)
		if err != nil {
			break
		}
		count := int64(bo.Uint16(cb))
		ifd := &Field{
			Name: fmt.Sprintf("IFD[%d]", n), Type: "directory", Offset: ifdOff,
			Length: 2 + count*12 + 4, Value: fmt.Sprintf("%d entries", count),
		}
		for i := int64(0); i < count && i < 4096; i++ {
			eoff := ifdOff + 2 + i*12
			eb, err := d.Range(eoff, 12)
			if err != nil || len(eb) < 12 {
				break
			}
			tag := bo.Uint16(eb[0:2])
			typ := bo.Uint16(eb[2:4])
			cnt := bo.Uint32(eb[4:8])
			val := bo.Uint32(eb[8:12])
			ifd.Children = append(ifd.Children, leaf(
				fmt.Sprintf("tag 0x%04x %s", tag, tiffTag(tag)), "ifd_entry", eoff, 12,
				fmt.Sprintf("type=%s count=%d value=%d", tiffType(typ), cnt, val)))
		}
		root.Children = append(root.Children, ifd)
		nb, err := d.Range(ifdOff+2+count*12, 4)
		if err != nil || len(nb) < 4 {
			break
		}
		ifdOff = int64(bo.Uint32(nb))
	}
	return root, nil
}

func tiffType(t uint16) string {
	names := map[uint16]string{1: "BYTE", 2: "ASCII", 3: "SHORT", 4: "LONG", 5: "RATIONAL", 6: "SBYTE", 7: "UNDEFINED", 8: "SSHORT", 9: "SLONG", 10: "SRATIONAL", 11: "FLOAT", 12: "DOUBLE"}
	if n, ok := names[t]; ok {
		return n
	}
	return fmt.Sprintf("type%d", t)
}

func tiffTag(t uint16) string {
	names := map[uint16]string{
		256: "ImageWidth", 257: "ImageLength", 258: "BitsPerSample", 259: "Compression",
		262: "PhotometricInterpretation", 273: "StripOffsets", 277: "SamplesPerPixel",
		278: "RowsPerStrip", 279: "StripByteCounts", 282: "XResolution", 283: "YResolution",
		296: "ResolutionUnit", 305: "Software", 306: "DateTime", 315: "Artist",
		271: "Make", 272: "Model", 274: "Orientation", 34665: "ExifIFD", 34853: "GPSIFD",
	}
	if n, ok := names[t]; ok {
		return n
	}
	return ""
}

// ---------- pcap / pcapng ----------

// ParsePCAP decodes a classic libpcap global header and walks packet records.
func ParsePCAP(d *document.Document) (*Field, error) {
	size := d.Size()
	hdr, err := d.Range(0, min64(24, size))
	if err != nil || len(hdr) < 24 {
		return nil, errors.New("pcap too short")
	}
	magic := binary.LittleEndian.Uint32(hdr[0:4])
	var bo binary.ByteOrder
	nano := false
	switch magic {
	case 0xa1b2c3d4:
		bo = binary.LittleEndian
	case 0xd4c3b2a1:
		bo = binary.BigEndian
	case 0xa1b23c4d:
		bo, nano = binary.LittleEndian, true
	case 0x4d3cb2a1:
		bo, nano = binary.BigEndian, true
	default:
		return nil, errors.New("not pcap")
	}
	res := "microsecond"
	if nano {
		res = "nanosecond"
	}
	linktype := bo.Uint32(hdr[20:24])
	root := &Field{
		Name: "PCAP", Type: "container", Offset: 0, Length: size,
		Value: fmt.Sprintf("%s, %s", res, pcapLinkType(linktype)),
		Children: []*Field{
			leaf("magic", "u32", 0, 4, fmt.Sprintf("0x%08x (%s)", magic, res)),
			leaf("version", "u16", 4, 4, fmt.Sprintf("%d.%d", bo.Uint16(hdr[4:6]), bo.Uint16(hdr[6:8]))),
			leaf("thiszone", "i32", 8, 4, fmt.Sprintf("%d", int32(bo.Uint32(hdr[8:12])))),
			leaf("sigfigs", "u32", 12, 4, fmt.Sprintf("%d", bo.Uint32(hdr[12:16]))),
			leaf("snaplen", "u32", 16, 4, fmt.Sprintf("%d", bo.Uint32(hdr[16:20]))),
			leaf("linktype", "u32", 20, 4, fmt.Sprintf("%d (%s)", linktype, pcapLinkType(linktype))),
		},
	}
	off := int64(24)
	for n := 0; off+16 <= size && n < 100000; n++ {
		rb, err := d.Range(off, 16)
		if err != nil {
			break
		}
		ts := bo.Uint32(rb[0:4])
		us := bo.Uint32(rb[4:8])
		inclLen := int64(bo.Uint32(rb[8:12]))
		origLen := bo.Uint32(rb[12:16])
		root.Children = append(root.Children, leaf(
			fmt.Sprintf("packet[%d]", n), "record", off, 16+inclLen,
			fmt.Sprintf("ts=%d.%06d incl=%d orig=%d", ts, us, inclLen, origLen)))
		off += 16 + inclLen
	}
	return root, nil
}

// ParsePCAPNG decodes the pcapng block structure (section header + block walk).
func ParsePCAPNG(d *document.Document) (*Field, error) {
	size := d.Size()
	hb, err := d.Range(0, min64(12, size))
	if err != nil || len(hb) < 12 {
		return nil, errors.New("pcapng too short")
	}
	if binary.LittleEndian.Uint32(hb[0:4]) != 0x0a0d0d0a {
		return nil, errors.New("not pcapng")
	}
	// Byte-order magic lives in the SHB body at offset 8.
	var bo binary.ByteOrder
	switch binary.LittleEndian.Uint32(hb[8:12]) {
	case 0x1a2b3c4d:
		bo = binary.LittleEndian
	case 0x4d3c2b1a:
		bo = binary.BigEndian
	default:
		return nil, errors.New("bad pcapng byte-order magic")
	}
	root := &Field{Name: "PCAPNG", Type: "container", Offset: 0, Length: size}
	off := int64(0)
	for n := 0; off+12 <= size && n < 100000; n++ {
		bh, err := d.Range(off, 12)
		if err != nil {
			break
		}
		btype := bo.Uint32(bh[0:4])
		blen := int64(bo.Uint32(bh[4:8]))
		if blen < 12 || off+blen > size {
			break
		}
		root.Children = append(root.Children, leaf(
			fmt.Sprintf("block[%d] %s", n, pcapngBlock(btype)), "block", off, blen,
			fmt.Sprintf("type=0x%08x length=%d", btype, blen)))
		off += blen
	}
	return root, nil
}

func pcapngBlock(t uint32) string {
	switch t {
	case 0x0a0d0d0a:
		return "SectionHeader"
	case 0x00000001:
		return "InterfaceDescription"
	case 0x00000003:
		return "SimplePacket"
	case 0x00000004:
		return "NameResolution"
	case 0x00000005:
		return "InterfaceStatistics"
	case 0x00000006:
		return "EnhancedPacket"
	default:
		return "block"
	}
}

func pcapLinkType(t uint32) string {
	names := map[uint32]string{0: "NULL", 1: "ETHERNET", 6: "TOKEN_RING", 9: "PPP", 101: "RAW", 105: "IEEE802_11", 113: "LINUX_SLL", 127: "IEEE802_11_RADIOTAP", 228: "IPV4", 229: "IPV6", 276: "LINUX_SLL2"}
	if n, ok := names[t]; ok {
		return n
	}
	return fmt.Sprintf("linktype%d", t)
}

// ---------- OLE / Compound File Binary (MS-CFB) ----------

var cfbMagic = []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}

// ParseCFB decodes the compound-file header and lists the entries in the first
// directory sector (legacy .doc/.xls/.ppt/.msi container).
func ParseCFB(d *document.Document) (*Field, error) {
	size := d.Size()
	hdr, err := d.Range(0, min64(512, size))
	if err != nil || len(hdr) < 76 {
		return nil, errors.New("cfb too short")
	}
	if string(hdr[0:8]) != string(cfbMagic) {
		return nil, errors.New("not a compound file")
	}
	le := binary.LittleEndian
	major := le.Uint16(hdr[26:28])
	sectorShift := le.Uint16(hdr[30:32])
	sectorSize := int64(1) << sectorShift
	firstDir := le.Uint32(hdr[48:52])
	root := &Field{
		Name: "CFB/OLE", Type: "container", Offset: 0, Length: size,
		Value: fmt.Sprintf("v%d.%d sector=%d", major, le.Uint16(hdr[24:26]), sectorSize),
		Children: []*Field{
			leaf("signature", "bytes", 0, 8, "D0 CF 11 E0 A1 B1 1A E1"),
			leaf("minor_version", "u16le", 24, 2, fmt.Sprintf("%d", le.Uint16(hdr[24:26]))),
			leaf("major_version", "u16le", 26, 2, fmt.Sprintf("%d", major)),
			leaf("byte_order", "u16le", 28, 2, fmt.Sprintf("0x%04x", le.Uint16(hdr[28:30]))),
			leaf("sector_shift", "u16le", 30, 2, fmt.Sprintf("%d (%d bytes)", sectorShift, sectorSize)),
			leaf("mini_sector_shift", "u16le", 32, 2, fmt.Sprintf("%d (%d bytes)", le.Uint16(hdr[32:34]), int64(1)<<le.Uint16(hdr[32:34]))),
			leaf("num_dir_sectors", "u32le", 40, 4, fmt.Sprintf("%d", le.Uint32(hdr[40:44]))),
			leaf("num_fat_sectors", "u32le", 44, 4, fmt.Sprintf("%d", le.Uint32(hdr[44:48]))),
			leaf("first_dir_sector", "u32le", 48, 4, fmt.Sprintf("%d", firstDir)),
			leaf("mini_stream_cutoff", "u32le", 56, 4, fmt.Sprintf("%d", le.Uint32(hdr[56:60]))),
			leaf("first_minifat_sector", "u32le", 60, 4, fmt.Sprintf("%d", le.Uint32(hdr[60:64]))),
			leaf("num_minifat_sectors", "u32le", 64, 4, fmt.Sprintf("%d", le.Uint32(hdr[64:68]))),
			leaf("first_difat_sector", "u32le", 68, 4, fmt.Sprintf("%d", le.Uint32(hdr[68:72]))),
			leaf("num_difat_sectors", "u32le", 72, 4, fmt.Sprintf("%d", le.Uint32(hdr[72:76]))),
		},
	}
	// First directory sector holds up to sectorSize/128 directory entries.
	dirOff := int64(firstDir+1) * sectorSize
	if dir := cfbDirectory(d, dirOff, sectorSize, size); dir != nil {
		root.Children = append(root.Children, dir)
	}
	return root, nil
}

func cfbDirectory(d *document.Document, off, sectorSize, size int64) *Field {
	if off <= 0 || off+128 > size {
		return nil
	}
	dir := &Field{Name: "directory", Type: "directory", Offset: off}
	per := sectorSize / 128
	count := int64(0)
	for i := int64(0); i < per; i++ {
		eoff := off + i*128
		eb, err := d.Range(eoff, 128)
		if err != nil || len(eb) < 128 {
			break
		}
		nameLen := int64(binary.LittleEndian.Uint16(eb[64:66]))
		objType := eb[66]
		if objType == 0 { // unallocated entry
			continue
		}
		name := ""
		if nameLen >= 2 && nameLen <= 64 {
			name = decodeUTF16LE(eb[0 : nameLen-2])
		}
		streamSize := binary.LittleEndian.Uint64(eb[120:128])
		dir.Children = append(dir.Children, leaf(
			cfbEntryLabel(name, count), "dir_entry", eoff, 128,
			fmt.Sprintf("%s size=%d", cfbObjType(objType), streamSize)))
		count++
	}
	dir.Length = sectorSize
	dir.Value = fmt.Sprintf("%d entries", count)
	if count == 0 {
		return nil
	}
	return dir
}

func cfbEntryLabel(name string, i int64) string {
	if name == "" {
		return fmt.Sprintf("entry[%d]", i)
	}
	// Strip the control prefix Office uses on some stream names for display.
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	if clean == "" {
		clean = name
	}
	return fmt.Sprintf("entry[%d] %s", i, clean)
}

func cfbObjType(t byte) string {
	switch t {
	case 1:
		return "storage"
	case 2:
		return "stream"
	case 5:
		return "root"
	default:
		return fmt.Sprintf("type%d", t)
	}
}

// ---------- shared helpers ----------

func readCStringField(d *document.Document, off, size int64) (string, int64) {
	var sb strings.Builder
	n := int64(0)
	for off+n < size && n < 4096 {
		b, err := d.At(off + n)
		if err != nil {
			break
		}
		n++
		if b == 0 {
			break
		}
		sb.WriteByte(b)
	}
	return sb.String(), n
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func trimZeroStr(b []byte) string {
	return strings.TrimRight(string(b), "\x00 ")
}

// octal parses a space/NUL-padded octal ASCII field (tar number encoding).
func octal(b []byte) int64 {
	s := strings.TrimSpace(strings.Trim(string(b), "\x00"))
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 8, 64)
	if err != nil {
		return 0
	}
	return v
}

func roundUp(n, to int64) int64 {
	if n <= 0 {
		return 0
	}
	return ((n + to - 1) / to) * to
}
