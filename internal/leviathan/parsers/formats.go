// Package parsers provides lightweight structure parsers for common
// binary formats used in Leviathan's structures / templates panel.
// Each parser returns a tree of Field values anchored at absolute
// offsets into the document.
package parsers

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andydixon/link/internal/leviathan/document"
)

// Field is one node of the parsed structure.
type Field struct {
	Name     string
	Type     string
	Offset   int64
	Length   int64
	Value    string
	Note     string
	Children []*Field
}

// ToMap serialises a Field tree to JS-friendly maps.
func (f *Field) ToMap() map[string]any {
	m := map[string]any{
		"name":   f.Name,
		"type":   f.Type,
		"offset": f.Offset,
		"length": f.Length,
		"value":  f.Value,
	}
	if f.Note != "" {
		m["note"] = f.Note
	}
	if len(f.Children) > 0 {
		kids := make([]map[string]any, len(f.Children))
		for i, c := range f.Children {
			kids[i] = c.ToMap()
		}
		m["children"] = kids
	}
	return m
}

// DetectAndParse picks the first matching parser for the document.
// It returns the parser name used and the tree.
func DetectAndParse(d *document.Document) (string, *Field, error) {
	// Read enough to cover the tar ustar magic at offset 257.
	head, err := d.Range(0, min64(265, d.Size()))
	if err != nil {
		return "", nil, err
	}
	switch {
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		f, err := ParsePNG(d)
		return "png", f, err
	case len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		f, err := ParseJPEG(d)
		return "jpeg", f, err
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		f, err := ParseGIF(d)
		return "gif", f, err
	case len(head) >= 2 && string(head[:2]) == "BM":
		f, err := ParseBMP(d)
		return "bmp", f, err
	case len(head) >= 4 && string(head[:4]) == "PK\x03\x04":
		f, err := ParseZIP(d)
		return "zip", f, err
	case len(head) >= 4 && string(head[:4]) == "\x7fELF":
		f, err := ParseELF(d)
		return "elf", f, err
	case len(head) >= 2 && string(head[:2]) == "MZ":
		f, err := ParsePE(d)
		return "pe", f, err
	case len(head) >= 4 && string(head[:4]) == "RIFF":
		f, err := ParseRIFF(d)
		return "riff", f, err
	case len(head) >= 16 && string(head[:16]) == "SQLite format 3\x00":
		f, err := ParseSQLite(d)
		return "sqlite", f, err
	case len(head) >= 8 && string(head[:8]) == string(cfbMagic):
		f, err := ParseCFB(d)
		return "cfb", f, err
	case len(head) >= 3 && head[0] == 0x1f && head[1] == 0x8b && head[2] == 8:
		f, err := ParseGZIP(d)
		return "gzip", f, err
	case len(head) >= 4 && string(head[:4]) == "\x0a\x0d\x0d\x0a":
		f, err := ParsePCAPNG(d)
		return "pcapng", f, err
	case len(head) >= 4 && isPCAPMagic(head):
		f, err := ParsePCAP(d)
		return "pcap", f, err
	case len(head) >= 4 && (string(head[:4]) == "II\x2a\x00" || string(head[:4]) == "MM\x00\x2a"):
		f, err := ParseTIFF(d)
		return "tiff", f, err
	case len(head) >= 265 && (string(head[257:262]) == "ustar"):
		f, err := ParseTAR(d)
		return "tar", f, err
	}
	// Mach-O (including fat/universal) — detection needs more than 8 bytes.
	if IsMachO(head, d.Size()) {
		f, err := ParseMachO(d)
		return "macho", f, err
	}
	return "", nil, errors.New("no parser matched")
}

func isPCAPMagic(head []byte) bool {
	m := binary.LittleEndian.Uint32(head[:4])
	return m == 0xa1b2c3d4 || m == 0xd4c3b2a1 || m == 0xa1b23c4d || m == 0x4d3cb2a1
}

// ParsePNG walks chunks sequentially and returns a Field tree.
func ParsePNG(d *document.Document) (*Field, error) {
	root := &Field{Name: "PNG", Type: "container", Offset: 0}
	hdr, err := d.Range(0, 8)
	if err != nil {
		return nil, err
	}
	root.Children = append(root.Children, &Field{
		Name: "signature", Type: "bytes", Offset: 0, Length: 8,
		Value: fmt.Sprintf("% x", hdr),
	})
	off := int64(8)
	size := d.Size()
	for off+8 <= size {
		hdr, err := d.Range(off, 8)
		if err != nil {
			return root, err
		}
		ln := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		chunk := &Field{
			Name:   typ + "@" + fmt.Sprintf("0x%x", off),
			Type:   "chunk",
			Offset: off,
			Length: 12 + ln,
			Value:  fmt.Sprintf("length=%d", ln),
		}
		chunk.Children = []*Field{
			{Name: "length", Type: "u32be", Offset: off, Length: 4, Value: fmt.Sprintf("%d", ln)},
			{Name: "type", Type: "ascii", Offset: off + 4, Length: 4, Value: typ},
			{Name: "data", Type: "bytes", Offset: off + 8, Length: ln, Value: fmt.Sprintf("%d bytes", ln)},
			{Name: "crc", Type: "u32be", Offset: off + 8 + ln, Length: 4, Value: "crc32"},
		}
		root.Children = append(root.Children, chunk)
		off += 12 + ln
		if typ == "IEND" {
			break
		}
	}
	root.Length = off
	return root, nil
}

// ParseJPEG walks marker segments.
func ParseJPEG(d *document.Document) (*Field, error) {
	root := &Field{Name: "JPEG", Type: "container", Offset: 0}
	off := int64(0)
	size := d.Size()
	for off < size {
		b, err := d.Range(off, 2)
		if err != nil || len(b) < 2 {
			break
		}
		if b[0] != 0xff {
			return root, errors.New("lost sync")
		}
		marker := b[1]
		mark := &Field{
			Name:   jpegMarkerName(marker),
			Type:   "marker",
			Offset: off,
			Value:  fmt.Sprintf("0x%02x", marker),
		}
		off += 2
		if marker == 0xd8 || marker == 0xd9 || marker == 0x00 || (marker >= 0xd0 && marker <= 0xd7) {
			mark.Length = 2
			root.Children = append(root.Children, mark)
			if marker == 0xd9 {
				break
			}
			continue
		}
		lb, err := d.Range(off, 2)
		if err != nil || len(lb) < 2 {
			break
		}
		ln := int64(binary.BigEndian.Uint16(lb))
		mark.Length = 2 + ln
		mark.Children = []*Field{
			{Name: "length", Type: "u16be", Offset: off, Length: 2, Value: fmt.Sprintf("%d", ln)},
			{Name: "data", Type: "bytes", Offset: off + 2, Length: ln - 2, Value: fmt.Sprintf("%d bytes", ln-2)},
		}
		root.Children = append(root.Children, mark)
		off += ln
		if marker == 0xda { // SOS
			// scan until next marker
			for off < size {
				bb, err := d.Range(off, 2)
				if err != nil || len(bb) < 2 {
					break
				}
				if bb[0] == 0xff && bb[1] != 0x00 && !(bb[1] >= 0xd0 && bb[1] <= 0xd7) {
					break
				}
				off++
			}
		}
	}
	root.Length = off
	return root, nil
}

func jpegMarkerName(m byte) string {
	switch m {
	case 0xd8:
		return "SOI"
	case 0xd9:
		return "EOI"
	case 0xda:
		return "SOS"
	case 0xdb:
		return "DQT"
	case 0xc4:
		return "DHT"
	case 0xe0:
		return "APP0"
	case 0xe1:
		return "APP1"
	case 0xfe:
		return "COM"
	}
	if m >= 0xc0 && m <= 0xcf {
		return fmt.Sprintf("SOF%d", int(m-0xc0))
	}
	if m >= 0xe0 && m <= 0xef {
		return fmt.Sprintf("APP%d", int(m-0xe0))
	}
	return fmt.Sprintf("MRK%02x", m)
}

// ParseGIF walks the logical screen descriptor, global colour table,
// extension blocks, image descriptors and trailer.
func ParseGIF(d *document.Document) (*Field, error) {
	size := d.Size()
	if size < 13 {
		return nil, errors.New("gif too short")
	}
	hdr, err := d.Range(0, 13)
	if err != nil {
		return nil, err
	}
	version := string(hdr[0:6])
	if version != "GIF87a" && version != "GIF89a" {
		return nil, errors.New("bad gif signature")
	}
	width := binary.LittleEndian.Uint16(hdr[6:8])
	height := binary.LittleEndian.Uint16(hdr[8:10])
	packed := hdr[10]
	gctFlag := packed&0x80 != 0
	colorRes := ((packed >> 4) & 0x07) + 1
	sortFlag := packed&0x08 != 0
	gctEntries := 0
	if gctFlag {
		gctEntries = 1 << ((packed & 0x07) + 1)
	}
	root := &Field{
		Name:   "GIF",
		Type:   "container",
		Offset: 0,
		Value:  fmt.Sprintf("%s %dx%d", version, width, height),
		Children: []*Field{
			{Name: "signature", Type: "ascii", Offset: 0, Length: 6, Value: version},
			{Name: "width", Type: "u16le", Offset: 6, Length: 2, Value: fmt.Sprintf("%d", width)},
			{Name: "height", Type: "u16le", Offset: 8, Length: 2, Value: fmt.Sprintf("%d", height)},
			{Name: "packed", Type: "u8", Offset: 10, Length: 1, Value: fmt.Sprintf("gct=%v colorRes=%d sort=%v entries=%d", gctFlag, colorRes, sortFlag, gctEntries)},
			{Name: "backgroundColorIndex", Type: "u8", Offset: 11, Length: 1, Value: fmt.Sprintf("%d", hdr[11])},
			{Name: "pixelAspectRatio", Type: "u8", Offset: 12, Length: 1, Value: fmt.Sprintf("%d", hdr[12])},
		},
	}
	off := int64(13)
	if gctEntries > 0 {
		ln := int64(gctEntries * 3)
		if off+ln > size {
			return root, errors.New("gif global colour table exceeds file")
		}
		root.Children = append(root.Children, &Field{Name: "globalColorTable", Type: "palette", Offset: off, Length: ln, Value: fmt.Sprintf("%d RGB entries", gctEntries)})
		off += ln
	}
	for off < size {
		tag, err := d.Range(off, 1)
		if err != nil || len(tag) < 1 {
			break
		}
		switch tag[0] {
		case 0x3b:
			root.Children = append(root.Children, &Field{Name: "trailer", Type: "byte", Offset: off, Length: 1, Value: "0x3b"})
			off++
			root.Length = off
			return root, nil
		case 0x21:
			block, next, err := parseGIFExtension(d, off, size)
			root.Children = append(root.Children, block)
			if err != nil {
				root.Length = next
				return root, err
			}
			off = next
		case 0x2c:
			block, next, err := parseGIFImage(d, off, size)
			root.Children = append(root.Children, block)
			if err != nil {
				root.Length = next
				return root, err
			}
			off = next
		default:
			root.Children = append(root.Children, &Field{Name: "unknown", Type: "byte", Offset: off, Length: 1, Value: fmt.Sprintf("0x%02x", tag[0])})
			root.Length = off + 1
			return root, fmt.Errorf("unknown gif block 0x%02x at 0x%x", tag[0], off)
		}
	}
	root.Length = off
	return root, nil
}

func parseGIFExtension(d *document.Document, off, size int64) (*Field, int64, error) {
	hdr, err := d.Range(off, 2)
	if err != nil || len(hdr) < 2 {
		return &Field{Name: "extension", Type: "block", Offset: off}, off, errors.New("short gif extension")
	}
	label := hdr[1]
	name := gifExtensionName(label)
	next, subBlocks, err := skipGIFSubBlocks(d, off+2, size)
	f := &Field{
		Name:   name,
		Type:   "extension",
		Offset: off,
		Length: next - off,
		Value:  fmt.Sprintf("label=0x%02x subBlocks=%d", label, subBlocks),
		Children: []*Field{
			{Name: "introducer", Type: "u8", Offset: off, Length: 1, Value: "0x21"},
			{Name: "label", Type: "u8", Offset: off + 1, Length: 1, Value: fmt.Sprintf("0x%02x", label)},
		},
	}
	return f, next, err
}

func parseGIFImage(d *document.Document, off, size int64) (*Field, int64, error) {
	hdr, err := d.Range(off, 10)
	if err != nil || len(hdr) < 10 {
		return &Field{Name: "image", Type: "image", Offset: off}, off, errors.New("short gif image descriptor")
	}
	left := binary.LittleEndian.Uint16(hdr[1:3])
	top := binary.LittleEndian.Uint16(hdr[3:5])
	width := binary.LittleEndian.Uint16(hdr[5:7])
	height := binary.LittleEndian.Uint16(hdr[7:9])
	packed := hdr[9]
	lctEntries := 0
	if packed&0x80 != 0 {
		lctEntries = 1 << ((packed & 0x07) + 1)
	}
	next := off + 10 + int64(lctEntries*3)
	if next >= size {
		return nil, next, errors.New("gif image data missing")
	}
	lzw, err := d.Range(next, 1)
	if err != nil || len(lzw) < 1 {
		return nil, next, errors.New("gif lzw minimum code size missing")
	}
	dataStart := next + 1
	next, subBlocks, err := skipGIFSubBlocks(d, dataStart, size)
	f := &Field{
		Name:   fmt.Sprintf("image@0x%x", off),
		Type:   "image",
		Offset: off,
		Length: next - off,
		Value:  fmt.Sprintf("%dx%d at %d,%d subBlocks=%d", width, height, left, top, subBlocks),
		Children: []*Field{
			{Name: "left", Type: "u16le", Offset: off + 1, Length: 2, Value: fmt.Sprintf("%d", left)},
			{Name: "top", Type: "u16le", Offset: off + 3, Length: 2, Value: fmt.Sprintf("%d", top)},
			{Name: "width", Type: "u16le", Offset: off + 5, Length: 2, Value: fmt.Sprintf("%d", width)},
			{Name: "height", Type: "u16le", Offset: off + 7, Length: 2, Value: fmt.Sprintf("%d", height)},
			{Name: "packed", Type: "u8", Offset: off + 9, Length: 1, Value: fmt.Sprintf("lctEntries=%d interlace=%v", lctEntries, packed&0x40 != 0)},
			{Name: "lzwMinimumCodeSize", Type: "u8", Offset: dataStart - 1, Length: 1, Value: fmt.Sprintf("%d", lzw[0])},
			{Name: "imageData", Type: "subblocks", Offset: dataStart, Length: next - dataStart, Value: fmt.Sprintf("%d sub-blocks", subBlocks)},
		},
	}
	if lctEntries > 0 {
		f.Children = append(f.Children, &Field{Name: "localColorTable", Type: "palette", Offset: off + 10, Length: int64(lctEntries * 3), Value: fmt.Sprintf("%d RGB entries", lctEntries)})
	}
	return f, next, err
}

func skipGIFSubBlocks(d *document.Document, off, size int64) (int64, int, error) {
	count := 0
	for off < size {
		b, err := d.Range(off, 1)
		if err != nil || len(b) < 1 {
			return off, count, errors.New("short gif sub-block")
		}
		off++
		ln := int64(b[0])
		if ln == 0 {
			return off, count, nil
		}
		if off+ln > size {
			return off, count, errors.New("gif sub-block exceeds file")
		}
		off += ln
		count++
	}
	return off, count, errors.New("unterminated gif sub-block chain")
}

func gifExtensionName(label byte) string {
	switch label {
	case 0xf9:
		return "graphicControlExtension"
	case 0xfe:
		return "commentExtension"
	case 0xff:
		return "applicationExtension"
	case 0x01:
		return "plainTextExtension"
	}
	return fmt.Sprintf("extension%02x", label)
}

// ParseBMP reads the BMP file header and DIB header metadata.
func ParseBMP(d *document.Document) (*Field, error) {
	size := d.Size()
	if size < 26 {
		return nil, errors.New("bmp too short")
	}
	hdr, err := d.Range(0, min64(size, 138))
	if err != nil {
		return nil, err
	}
	if len(hdr) < 26 || string(hdr[0:2]) != "BM" {
		return nil, errors.New("bad bmp signature")
	}
	fileSize := binary.LittleEndian.Uint32(hdr[2:6])
	pixelOff := binary.LittleEndian.Uint32(hdr[10:14])
	dibSize := binary.LittleEndian.Uint32(hdr[14:18])
	root := &Field{
		Name:   "BMP",
		Type:   "container",
		Offset: 0,
		Length: int64(fileSize),
		Value:  fmt.Sprintf("fileSize=%d pixelOffset=0x%x dib=%s", fileSize, pixelOff, bmpDIBName(dibSize)),
		Children: []*Field{
			{Name: "signature", Type: "ascii", Offset: 0, Length: 2, Value: "BM"},
			{Name: "fileSize", Type: "u32le", Offset: 2, Length: 4, Value: fmt.Sprintf("%d", fileSize)},
			{Name: "pixelDataOffset", Type: "u32le", Offset: 10, Length: 4, Value: fmt.Sprintf("0x%x", pixelOff)},
			{Name: "dibHeaderSize", Type: "u32le", Offset: 14, Length: 4, Value: fmt.Sprintf("%d (%s)", dibSize, bmpDIBName(dibSize))},
		},
	}
	if dibSize == 12 {
		if len(hdr) < 26 {
			return root, nil
		}
		width := binary.LittleEndian.Uint16(hdr[18:20])
		height := binary.LittleEndian.Uint16(hdr[20:22])
		planes := binary.LittleEndian.Uint16(hdr[22:24])
		bpp := binary.LittleEndian.Uint16(hdr[24:26])
		root.Children = append(root.Children,
			&Field{Name: "width", Type: "u16le", Offset: 18, Length: 2, Value: fmt.Sprintf("%d", width)},
			&Field{Name: "height", Type: "u16le", Offset: 20, Length: 2, Value: fmt.Sprintf("%d", height)},
			&Field{Name: "planes", Type: "u16le", Offset: 22, Length: 2, Value: fmt.Sprintf("%d", planes)},
			&Field{Name: "bitsPerPixel", Type: "u16le", Offset: 24, Length: 2, Value: fmt.Sprintf("%d", bpp)},
		)
	} else if dibSize >= 40 && len(hdr) >= 54 {
		width := int32(binary.LittleEndian.Uint32(hdr[18:22]))
		height := int32(binary.LittleEndian.Uint32(hdr[22:26]))
		planes := binary.LittleEndian.Uint16(hdr[26:28])
		bpp := binary.LittleEndian.Uint16(hdr[28:30])
		compression := binary.LittleEndian.Uint32(hdr[30:34])
		imageSize := binary.LittleEndian.Uint32(hdr[34:38])
		xppm := int32(binary.LittleEndian.Uint32(hdr[38:42]))
		yppm := int32(binary.LittleEndian.Uint32(hdr[42:46]))
		coloursUsed := binary.LittleEndian.Uint32(hdr[46:50])
		root.Children = append(root.Children,
			&Field{Name: "width", Type: "i32le", Offset: 18, Length: 4, Value: fmt.Sprintf("%d", width)},
			&Field{Name: "height", Type: "i32le", Offset: 22, Length: 4, Value: fmt.Sprintf("%d", height)},
			&Field{Name: "planes", Type: "u16le", Offset: 26, Length: 2, Value: fmt.Sprintf("%d", planes)},
			&Field{Name: "bitsPerPixel", Type: "u16le", Offset: 28, Length: 2, Value: fmt.Sprintf("%d", bpp)},
			&Field{Name: "compression", Type: "u32le", Offset: 30, Length: 4, Value: fmt.Sprintf("%d (%s)", compression, bmpCompressionName(compression))},
			&Field{Name: "imageSize", Type: "u32le", Offset: 34, Length: 4, Value: fmt.Sprintf("%d", imageSize)},
			&Field{Name: "xPixelsPerMeter", Type: "i32le", Offset: 38, Length: 4, Value: fmt.Sprintf("%d", xppm)},
			&Field{Name: "yPixelsPerMeter", Type: "i32le", Offset: 42, Length: 4, Value: fmt.Sprintf("%d", yppm)},
			&Field{Name: "coloursUsed", Type: "u32le", Offset: 46, Length: 4, Value: fmt.Sprintf("%d", coloursUsed)},
		)
	}
	if int64(pixelOff) < size {
		root.Children = append(root.Children, &Field{Name: "pixelData", Type: "bytes", Offset: int64(pixelOff), Length: size - int64(pixelOff), Value: fmt.Sprintf("%d bytes", size-int64(pixelOff))})
	}
	return root, nil
}

func bmpDIBName(size uint32) string {
	switch size {
	case 12:
		return "BITMAPCOREHEADER"
	case 40:
		return "BITMAPINFOHEADER"
	case 52:
		return "BITMAPV2INFOHEADER"
	case 56:
		return "BITMAPV3INFOHEADER"
	case 108:
		return "BITMAPV4HEADER"
	case 124:
		return "BITMAPV5HEADER"
	}
	return "unknown"
}

func bmpCompressionName(v uint32) string {
	switch v {
	case 0:
		return "BI_RGB"
	case 1:
		return "BI_RLE8"
	case 2:
		return "BI_RLE4"
	case 3:
		return "BI_BITFIELDS"
	case 4:
		return "BI_JPEG"
	case 5:
		return "BI_PNG"
	}
	return "unknown"
}

// ParseZIP walks local file headers by seeking forward.
func ParseZIP(d *document.Document) (*Field, error) {
	root := &Field{Name: "ZIP", Type: "container", Offset: 0, Length: d.Size()}
	size := d.Size()

	// 1) Local file headers, walked from the front.
	off := int64(0)
	entries := 0
	for off+30 <= size && entries < 5000 {
		sig, err := d.Range(off, 4)
		if err != nil || string(sig) != "PK\x03\x04" {
			break // reached the central directory / EOCD region
		}
		hdr, err := d.Range(off, 30)
		if err != nil {
			break
		}
		flags := binary.LittleEndian.Uint16(hdr[6:8])
		method := binary.LittleEndian.Uint16(hdr[8:10])
		dosTime := binary.LittleEndian.Uint16(hdr[10:12])
		dosDate := binary.LittleEndian.Uint16(hdr[12:14])
		crc := binary.LittleEndian.Uint32(hdr[14:18])
		compSize := int64(binary.LittleEndian.Uint32(hdr[18:22]))
		uncompSize := int64(binary.LittleEndian.Uint32(hdr[22:26]))
		nameLen := int64(binary.LittleEndian.Uint16(hdr[26:28]))
		extraLen := int64(binary.LittleEndian.Uint16(hdr[28:30]))
		nameB, _ := d.Range(off+30, nameLen)
		e := &Field{
			Name:   string(nameB),
			Type:   "entry",
			Offset: off,
			Length: 30 + nameLen + extraLen + compSize,
			Value:  fmt.Sprintf("%s comp=%d uncomp=%d", zipMethod(method), compSize, uncompSize),
			Children: []*Field{
				{Name: "signature", Type: "bytes", Offset: off, Length: 4, Value: "PK\\x03\\x04"},
				{Name: "version_needed", Type: "u16le", Offset: off + 4, Length: 2, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint16(hdr[4:6]))},
				{Name: "flags", Type: "u16le", Offset: off + 6, Length: 2, Value: zipFlags(flags)},
				{Name: "method", Type: "u16le", Offset: off + 8, Length: 2, Value: zipMethod(method)},
				{Name: "mod_time", Type: "dostime", Offset: off + 10, Length: 4, Value: zipDOSDateTime(dosDate, dosTime)},
				{Name: "crc32", Type: "u32le", Offset: off + 14, Length: 4, Value: fmt.Sprintf("0x%08x", crc)},
				{Name: "compressed_size", Type: "u32le", Offset: off + 18, Length: 4, Value: fmt.Sprintf("%d", compSize)},
				{Name: "uncompressed_size", Type: "u32le", Offset: off + 22, Length: 4, Value: fmt.Sprintf("%d", uncompSize)},
				{Name: "name", Type: "ascii", Offset: off + 30, Length: nameLen, Value: string(nameB)},
				{Name: "data", Type: "bytes", Offset: off + 30 + nameLen + extraLen, Length: compSize, Value: fmt.Sprintf("%d bytes", compSize)},
			},
		}
		root.Children = append(root.Children, e)
		off += 30 + nameLen + extraLen + compSize
		entries++
	}

	// 2) Central directory + end-of-central-directory, located from the back.
	if cd := zipCentralDirectory(d); cd != nil {
		root.Children = append(root.Children, cd...)
	}
	return root, nil
}

// zipCentralDirectory finds the EOCD record, decodes it, and walks the central
// directory it points at. Returns the EOCD field plus a "central_directory"
// container, or nil if no EOCD is present.
func zipCentralDirectory(d *document.Document) []*Field {
	size := d.Size()
	// EOCD is at most 22 + 65535 bytes from the end; scan a bounded tail.
	scan := int64(22 + 65535)
	if scan > size {
		scan = size
	}
	tail, err := d.Range(size-scan, scan)
	if err != nil {
		return nil
	}
	eocdRel := int64(-1)
	for i := len(tail) - 22; i >= 0; i-- {
		if tail[i] == 'P' && tail[i+1] == 'K' && tail[i+2] == 0x05 && tail[i+3] == 0x06 {
			eocdRel = int64(i)
			break
		}
	}
	if eocdRel < 0 {
		return nil
	}
	eocdOff := size - scan + eocdRel
	e, _ := d.Range(eocdOff, 22)
	if len(e) < 22 {
		return nil
	}
	total := binary.LittleEndian.Uint16(e[10:12])
	cdSize := int64(binary.LittleEndian.Uint32(e[12:16]))
	cdOff := int64(binary.LittleEndian.Uint32(e[16:20]))
	commentLen := int64(binary.LittleEndian.Uint16(e[20:22]))
	eocd := &Field{
		Name: "end_of_central_directory", Type: "eocd", Offset: eocdOff, Length: 22 + commentLen,
		Value: fmt.Sprintf("%d entries, cd@0x%x size=%d", total, cdOff, cdSize),
		Children: []*Field{
			{Name: "total_entries", Type: "u16le", Offset: eocdOff + 10, Length: 2, Value: fmt.Sprintf("%d", total)},
			{Name: "cd_size", Type: "u32le", Offset: eocdOff + 12, Length: 4, Value: fmt.Sprintf("%d", cdSize)},
			{Name: "cd_offset", Type: "u32le", Offset: eocdOff + 16, Length: 4, Value: fmt.Sprintf("0x%x", cdOff)},
			{Name: "comment_length", Type: "u16le", Offset: eocdOff + 20, Length: 2, Value: fmt.Sprintf("%d", commentLen)},
		},
	}
	out := []*Field{}

	// ZIP64: a 0xffffffff offset or 0xffff count signals a ZIP64 EOCD locator.
	if cdOff == 0xffffffff || total == 0xffff {
		if eocdOff-20 >= 0 {
			loc, _ := d.Range(eocdOff-20, 20)
			if len(loc) == 20 && string(loc[0:4]) == "PK\x06\x07" {
				z64Off := int64(binary.LittleEndian.Uint64(loc[8:16]))
				eocd.Children = append(eocd.Children, &Field{Name: "zip64_locator", Type: "bytes", Offset: eocdOff - 20, Length: 20, Value: fmt.Sprintf("zip64 EOCD @0x%x", z64Off)})
				if z64, _ := d.Range(z64Off, 56); len(z64) == 56 && string(z64[0:4]) == "PK\x06\x06" {
					cdOff = int64(binary.LittleEndian.Uint64(z64[48:56]))
					cdSize = int64(binary.LittleEndian.Uint64(z64[40:48]))
					out = append(out, &Field{Name: "zip64_eocd", Type: "zip64", Offset: z64Off, Length: 56, Value: fmt.Sprintf("entries=%d cd@0x%x", binary.LittleEndian.Uint64(z64[32:40]), cdOff)})
				}
			}
		}
	}

	// Walk central-directory file headers.
	if cdOff >= 0 && cdOff < size {
		cd := &Field{Name: "central_directory", Type: "directory", Offset: cdOff, Length: cdSize}
		off := cdOff
		end := size
		if cdSize > 0 && cdOff+cdSize <= size {
			end = cdOff + cdSize
		}
		for n := 0; off+46 <= end && n < 5000; n++ {
			h, err := d.Range(off, 46)
			if err != nil || string(h[0:4]) != "PK\x01\x02" {
				break
			}
			method := binary.LittleEndian.Uint16(h[10:12])
			crc := binary.LittleEndian.Uint32(h[16:20])
			compSize := int64(binary.LittleEndian.Uint32(h[20:24]))
			nameLen := int64(binary.LittleEndian.Uint16(h[28:30]))
			extraLen := int64(binary.LittleEndian.Uint16(h[30:32]))
			commLen := int64(binary.LittleEndian.Uint16(h[32:34]))
			lho := binary.LittleEndian.Uint32(h[42:46])
			nameB, _ := d.Range(off+46, nameLen)
			cd.Children = append(cd.Children, &Field{
				Name: string(nameB), Type: "cd_entry", Offset: off, Length: 46 + nameLen + extraLen + commLen,
				Value: fmt.Sprintf("%s crc=0x%08x comp=%d lfh@0x%x", zipMethod(method), crc, compSize, lho),
			})
			off += 46 + nameLen + extraLen + commLen
		}
		out = append(out, cd)
	}
	out = append(out, eocd)
	return out
}

func zipMethod(m uint16) string {
	names := map[uint16]string{0: "store", 1: "shrink", 6: "implode", 8: "deflate", 9: "deflate64", 12: "bzip2", 14: "lzma", 95: "xz", 96: "jpeg", 97: "wavpack", 98: "ppmd", 99: "aes"}
	if n, ok := names[m]; ok {
		return fmt.Sprintf("%d (%s)", m, n)
	}
	return fmt.Sprintf("%d", m)
}

func zipFlags(f uint16) string {
	var on []string
	if f&0x0001 != 0 {
		on = append(on, "encrypted")
	}
	if f&0x0008 != 0 {
		on = append(on, "data-descriptor")
	}
	if f&0x0800 != 0 {
		on = append(on, "utf8")
	}
	if len(on) == 0 {
		return fmt.Sprintf("0x%04x", f)
	}
	return fmt.Sprintf("0x%04x (%s)", f, strings.Join(on, "|"))
}

// zipDOSDateTime decodes the MS-DOS packed date/time used by ZIP entries.
func zipDOSDateTime(date, t uint16) string {
	if date == 0 && t == 0 {
		return "0"
	}
	year := 1980 + int(date>>9)
	month := int(date>>5) & 0x0f
	day := int(date) & 0x1f
	hour := int(t >> 11)
	minute := int(t>>5) & 0x3f
	second := (int(t) & 0x1f) * 2
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", year, month, day, hour, minute, second)
}

// ParseELF returns header, program header, and section header fields.
func ParseELF(d *document.Document) (*Field, error) {
	hdr, err := d.Range(0, 64)
	if err != nil || len(hdr) < 52 {
		return nil, errors.New("elf too short")
	}
	class := hdr[4]
	data := hdr[5]
	kind := "32-bit"
	if class == 2 {
		kind = "64-bit"
	}
	endian := "little-endian"
	if data == 2 {
		endian = "big-endian"
	}
	root := &Field{
		Name:   "ELF",
		Type:   "container",
		Offset: 0,
		Length: d.Size(),
		Value:  kind + " " + endian,
		Children: []*Field{
			{Name: "ei_mag", Type: "bytes", Offset: 0, Length: 4, Value: "\\x7fELF"},
			{Name: "ei_class", Type: "u8", Offset: 4, Length: 1, Value: kind},
			{Name: "ei_data", Type: "u8", Offset: 5, Length: 1, Value: endian},
			{Name: "ei_version", Type: "u8", Offset: 6, Length: 1, Value: fmt.Sprintf("%d", hdr[6])},
			{Name: "ei_osabi", Type: "u8", Offset: 7, Length: 1, Value: fmt.Sprintf("%d", hdr[7])},
		},
	}
	var e_type, e_machine uint16
	var e_entry uint64
	var e_phoff, e_shoff uint64
	var e_phentsize, e_phnum, e_shentsize, e_shnum, e_shstrndx uint16
	bo := elfByteOrder(data)
	if class == 2 {
		e_type = bo.Uint16(hdr[16:18])
		e_machine = bo.Uint16(hdr[18:20])
		e_entry = bo.Uint64(hdr[24:32])
		e_phoff = bo.Uint64(hdr[32:40])
		e_shoff = bo.Uint64(hdr[40:48])
		e_phentsize = bo.Uint16(hdr[54:56])
		e_phnum = bo.Uint16(hdr[56:58])
		e_shentsize = bo.Uint16(hdr[58:60])
		e_shnum = bo.Uint16(hdr[60:62])
		e_shstrndx = bo.Uint16(hdr[62:64])
	} else {
		e_type = bo.Uint16(hdr[16:18])
		e_machine = bo.Uint16(hdr[18:20])
		e_entry = uint64(bo.Uint32(hdr[24:28]))
		e_phoff = uint64(bo.Uint32(hdr[28:32]))
		e_shoff = uint64(bo.Uint32(hdr[32:36]))
		e_phentsize = bo.Uint16(hdr[42:44])
		e_phnum = bo.Uint16(hdr[44:46])
		e_shentsize = bo.Uint16(hdr[46:48])
		e_shnum = bo.Uint16(hdr[48:50])
		e_shstrndx = bo.Uint16(hdr[50:52])
	}
	root.Children = append(root.Children,
		&Field{Name: "e_type", Value: fmt.Sprintf("%d (%s)", e_type, elfType(e_type))},
		&Field{Name: "e_machine", Value: fmt.Sprintf("%d (%s)", e_machine, elfMachine(e_machine))},
		&Field{Name: "e_entry", Value: fmt.Sprintf("0x%x", e_entry)},
		&Field{Name: "e_phoff", Value: fmt.Sprintf("0x%x", e_phoff)},
		&Field{Name: "e_shoff", Value: fmt.Sprintf("0x%x", e_shoff)},
		&Field{Name: "e_phentsize", Value: fmt.Sprintf("%d", e_phentsize)},
		&Field{Name: "e_phnum", Value: fmt.Sprintf("%d", e_phnum)},
		&Field{Name: "e_shentsize", Value: fmt.Sprintf("%d", e_shentsize)},
		&Field{Name: "e_shnum", Value: fmt.Sprintf("%d", e_shnum)},
		&Field{Name: "e_shstrndx", Value: fmt.Sprintf("%d", e_shstrndx)},
	)
	root.Children = append(root.Children, elfProgramHeaders(d, bo, class, e_phoff, e_phentsize, e_phnum)...)
	root.Children = append(root.Children, elfSectionHeaders(d, bo, class, e_shoff, e_shentsize, e_shnum, e_shstrndx)...)
	return root, nil
}

func elfByteOrder(data byte) binary.ByteOrder {
	if data == 2 {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

func elfProgramHeaders(d *document.Document, bo binary.ByteOrder, class byte, off uint64, entsize, count uint16) []*Field {
	if off == 0 || entsize == 0 || count == 0 {
		return nil
	}
	size := d.Size()
	out := make([]*Field, 0, count)
	for i := uint16(0); i < count; i++ {
		at := int64(off) + int64(i)*int64(entsize)
		if at < 0 || at+int64(entsize) > size {
			break
		}
		buf, err := d.Range(at, int64(entsize))
		if err != nil {
			break
		}
		var typ, flags uint32
		var pOffset, vaddr, filesz, memsz, align uint64
		if class == 2 {
			if len(buf) < 56 {
				break
			}
			typ = bo.Uint32(buf[0:4])
			flags = bo.Uint32(buf[4:8])
			pOffset = bo.Uint64(buf[8:16])
			vaddr = bo.Uint64(buf[16:24])
			filesz = bo.Uint64(buf[32:40])
			memsz = bo.Uint64(buf[40:48])
			align = bo.Uint64(buf[48:56])
		} else {
			if len(buf) < 32 {
				break
			}
			typ = bo.Uint32(buf[0:4])
			pOffset = uint64(bo.Uint32(buf[4:8]))
			vaddr = uint64(bo.Uint32(buf[8:12]))
			filesz = uint64(bo.Uint32(buf[16:20]))
			memsz = uint64(bo.Uint32(buf[20:24]))
			flags = bo.Uint32(buf[24:28])
			align = uint64(bo.Uint32(buf[28:32]))
		}
		out = append(out, &Field{
			Name:   fmt.Sprintf("program[%d]", i),
			Type:   "program_header",
			Offset: at,
			Length: int64(entsize),
			Value:  fmt.Sprintf("type=%s offset=0x%x vaddr=0x%x filesz=%d memsz=%d flags=%s align=0x%x", elfProgramType(typ), pOffset, vaddr, filesz, memsz, elfProgramFlags(flags), align),
		})
	}
	return out
}

type elfSectionInfo struct {
	nameOff          uint32
	typ              uint32
	flags, addr      uint64
	fileOff, secSize uint64
	link, info       uint32
	align, entrySize uint64
	at               int64
	length           int64
}

func elfSectionHeaders(d *document.Document, bo binary.ByteOrder, class byte, off uint64, entsize, count, strIndex uint16) []*Field {
	if off == 0 || entsize == 0 || count == 0 {
		return nil
	}
	size := d.Size()
	sections := make([]elfSectionInfo, 0, count)
	for i := uint16(0); i < count; i++ {
		at := int64(off) + int64(i)*int64(entsize)
		if at < 0 || at+int64(entsize) > size {
			break
		}
		buf, err := d.Range(at, int64(entsize))
		if err != nil {
			break
		}
		var s elfSectionInfo
		s.at = at
		s.length = int64(entsize)
		if class == 2 {
			if len(buf) < 64 {
				break
			}
			s.nameOff = bo.Uint32(buf[0:4])
			s.typ = bo.Uint32(buf[4:8])
			s.flags = bo.Uint64(buf[8:16])
			s.addr = bo.Uint64(buf[16:24])
			s.fileOff = bo.Uint64(buf[24:32])
			s.secSize = bo.Uint64(buf[32:40])
			s.link = bo.Uint32(buf[40:44])
			s.info = bo.Uint32(buf[44:48])
			s.align = bo.Uint64(buf[48:56])
			s.entrySize = bo.Uint64(buf[56:64])
		} else {
			if len(buf) < 40 {
				break
			}
			s.nameOff = bo.Uint32(buf[0:4])
			s.typ = bo.Uint32(buf[4:8])
			s.flags = uint64(bo.Uint32(buf[8:12]))
			s.addr = uint64(bo.Uint32(buf[12:16]))
			s.fileOff = uint64(bo.Uint32(buf[16:20]))
			s.secSize = uint64(bo.Uint32(buf[20:24]))
			s.link = bo.Uint32(buf[24:28])
			s.info = bo.Uint32(buf[28:32])
			s.align = uint64(bo.Uint32(buf[32:36]))
			s.entrySize = uint64(bo.Uint32(buf[36:40]))
		}
		sections = append(sections, s)
	}
	var names []byte
	if int(strIndex) < len(sections) {
		str := sections[strIndex]
		if str.fileOff <= uint64(size) && str.fileOff+str.secSize <= uint64(size) && str.secSize <= 1<<20 {
			names, _ = d.Range(int64(str.fileOff), int64(str.secSize))
		}
	}
	sectionNames := make([]string, len(sections))
	for i, s := range sections {
		name := elfStringAt(names, s.nameOff)
		if name == "" {
			name = fmt.Sprintf("section[%d]", i)
		}
		sectionNames[i] = name
	}
	out := make([]*Field, 0, len(sections))
	for i, s := range sections {
		name := sectionNames[i]
		children := []*Field{}
		if s.typ == 6 {
			children = elfDynamicEntries(d, bo, class, sections, s)
		} else if s.typ == 2 || s.typ == 11 {
			children = elfSymbolEntries(d, bo, class, sections, s)
		}
		out = append(out, &Field{
			Name:     name,
			Type:     "section_header",
			Offset:   s.at,
			Length:   s.length,
			Value:    fmt.Sprintf("type=%s offset=0x%x size=%d addr=0x%x flags=%s link=%d info=%d align=0x%x entsize=%d", elfSectionType(s.typ), s.fileOff, s.secSize, s.addr, elfSectionFlags(s.flags), s.link, s.info, s.align, s.entrySize),
			Children: children,
		})
	}
	return out
}

func elfSymbolEntries(d *document.Document, bo binary.ByteOrder, class byte, sections []elfSectionInfo, symtab elfSectionInfo) []*Field {
	entrySize := symtab.entrySize
	if entrySize == 0 {
		if class == 2 {
			entrySize = 24
		} else {
			entrySize = 16
		}
	}
	if entrySize == 0 || symtab.secSize == 0 || symtab.fileOff+symtab.secSize > uint64(d.Size()) {
		return nil
	}
	var strtab []byte
	if int(symtab.link) < len(sections) {
		linked := sections[symtab.link]
		if linked.fileOff+linked.secSize <= uint64(d.Size()) && linked.secSize <= 8<<20 {
			strtab, _ = d.Range(int64(linked.fileOff), int64(linked.secSize))
		}
	}
	count := int(symtab.secSize / entrySize)
	if count > 4096 {
		count = 4096
	}
	out := []*Field{}
	for i := 0; i < count; i++ {
		at := int64(symtab.fileOff + uint64(i)*entrySize)
		buf, err := d.Range(at, int64(entrySize))
		if err != nil || len(buf) < int(entrySize) {
			break
		}
		var nameOff uint32
		var info, other byte
		var shndx uint16
		var value, size uint64
		if class == 2 {
			nameOff = bo.Uint32(buf[0:4])
			info = buf[4]
			other = buf[5]
			shndx = bo.Uint16(buf[6:8])
			value = bo.Uint64(buf[8:16])
			size = bo.Uint64(buf[16:24])
		} else {
			nameOff = bo.Uint32(buf[0:4])
			value = uint64(bo.Uint32(buf[4:8]))
			size = uint64(bo.Uint32(buf[8:12]))
			info = buf[12]
			other = buf[13]
			shndx = bo.Uint16(buf[14:16])
		}
		name := elfStringAt(strtab, nameOff)
		if name == "" {
			name = fmt.Sprintf("<unnamed:%d>", i)
		}
		out = append(out, &Field{
			Name:   fmt.Sprintf("symbol[%d] %s", i, name),
			Type:   "symbol",
			Offset: at,
			Length: int64(entrySize),
			Value: fmt.Sprintf("name=%s bind=%s type=%s vis=%s shndx=%s value=0x%x size=%d",
				name, elfSymbolBind(info>>4), elfSymbolType(info&0xf), elfSymbolVisibility(other&0x3), elfSymbolSection(shndx), value, size),
		})
	}
	return out
}

func elfDynamicEntries(d *document.Document, bo binary.ByteOrder, class byte, sections []elfSectionInfo, dyn elfSectionInfo) []*Field {
	entrySize := dyn.entrySize
	if entrySize == 0 {
		if class == 2 {
			entrySize = 16
		} else {
			entrySize = 8
		}
	}
	if entrySize == 0 || dyn.secSize == 0 || dyn.fileOff+dyn.secSize > uint64(d.Size()) {
		return nil
	}
	var strtab []byte
	if int(dyn.link) < len(sections) {
		linked := sections[dyn.link]
		if linked.fileOff+linked.secSize <= uint64(d.Size()) && linked.secSize <= 8<<20 {
			strtab, _ = d.Range(int64(linked.fileOff), int64(linked.secSize))
		}
	}
	out := []*Field{}
	count := int(dyn.secSize / entrySize)
	if count > 4096 {
		count = 4096
	}
	for i := 0; i < count; i++ {
		at := int64(dyn.fileOff + uint64(i)*entrySize)
		buf, err := d.Range(at, int64(entrySize))
		if err != nil || len(buf) < int(entrySize) {
			break
		}
		var tag int64
		var val uint64
		if class == 2 {
			tag = int64(bo.Uint64(buf[0:8]))
			val = bo.Uint64(buf[8:16])
		} else {
			tag = int64(int32(bo.Uint32(buf[0:4])))
			val = uint64(bo.Uint32(buf[4:8]))
		}
		value := fmt.Sprintf("%s=0x%x", elfDynamicTag(tag), val)
		if tag == 1 && len(strtab) > 0 {
			if s := elfStringAt(strtab, uint32(val)); s != "" {
				value = "NEEDED=" + s
			}
		}
		out = append(out, &Field{
			Name:   fmt.Sprintf("dynamic[%d]", i),
			Type:   "dynamic_entry",
			Offset: at,
			Length: int64(entrySize),
			Value:  value,
		})
		if tag == 0 {
			break
		}
	}
	return out
}

func elfSymbolBind(bind byte) string {
	switch bind {
	case 0:
		return "LOCAL"
	case 1:
		return "GLOBAL"
	case 2:
		return "WEAK"
	}
	return fmt.Sprintf("0x%x", bind)
}

func elfSymbolType(typ byte) string {
	switch typ {
	case 0:
		return "NOTYPE"
	case 1:
		return "OBJECT"
	case 2:
		return "FUNC"
	case 3:
		return "SECTION"
	case 4:
		return "FILE"
	case 5:
		return "COMMON"
	case 6:
		return "TLS"
	}
	return fmt.Sprintf("0x%x", typ)
}

func elfSymbolVisibility(vis byte) string {
	switch vis {
	case 0:
		return "DEFAULT"
	case 1:
		return "INTERNAL"
	case 2:
		return "HIDDEN"
	case 3:
		return "PROTECTED"
	}
	return fmt.Sprintf("0x%x", vis)
}

func elfSymbolSection(idx uint16) string {
	switch idx {
	case 0:
		return "UND"
	case 0xfff1:
		return "ABS"
	case 0xfff2:
		return "COMMON"
	}
	return fmt.Sprintf("%d", idx)
}

func elfDynamicTag(tag int64) string {
	switch tag {
	case 0:
		return "NULL"
	case 1:
		return "NEEDED"
	case 2:
		return "PLTRELSZ"
	case 3:
		return "PLTGOT"
	case 4:
		return "HASH"
	case 5:
		return "STRTAB"
	case 6:
		return "SYMTAB"
	case 7:
		return "RELA"
	case 8:
		return "RELASZ"
	case 9:
		return "RELAENT"
	case 10:
		return "STRSZ"
	case 11:
		return "SYMENT"
	case 12:
		return "INIT"
	case 13:
		return "FINI"
	case 14:
		return "SONAME"
	case 15:
		return "RPATH"
	case 16:
		return "SYMBOLIC"
	case 17:
		return "REL"
	case 18:
		return "RELSZ"
	case 19:
		return "RELENT"
	case 20:
		return "PLTREL"
	case 21:
		return "DEBUG"
	case 22:
		return "TEXTREL"
	case 23:
		return "JMPREL"
	case 29:
		return "RUNPATH"
	}
	return fmt.Sprintf("0x%x", tag)
}

func elfStringAt(buf []byte, off uint32) string {
	if int(off) >= len(buf) {
		return ""
	}
	end := int(off)
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	return string(buf[off:end])
}

func elfProgramType(t uint32) string {
	switch t {
	case 0:
		return "NULL"
	case 1:
		return "LOAD"
	case 2:
		return "DYNAMIC"
	case 3:
		return "INTERP"
	case 4:
		return "NOTE"
	case 5:
		return "SHLIB"
	case 6:
		return "PHDR"
	case 7:
		return "TLS"
	}
	return fmt.Sprintf("0x%x", t)
}

func elfProgramFlags(flags uint32) string {
	var out string
	if flags&4 != 0 {
		out += "R"
	}
	if flags&2 != 0 {
		out += "W"
	}
	if flags&1 != 0 {
		out += "X"
	}
	if out == "" {
		return "-"
	}
	return out
}

func elfSectionType(t uint32) string {
	switch t {
	case 0:
		return "NULL"
	case 1:
		return "PROGBITS"
	case 2:
		return "SYMTAB"
	case 3:
		return "STRTAB"
	case 4:
		return "RELA"
	case 5:
		return "HASH"
	case 6:
		return "DYNAMIC"
	case 7:
		return "NOTE"
	case 8:
		return "NOBITS"
	case 9:
		return "REL"
	case 11:
		return "DYNSYM"
	case 14:
		return "INIT_ARRAY"
	case 15:
		return "FINI_ARRAY"
	case 17:
		return "GROUP"
	case 18:
		return "SYMTAB_SHNDX"
	}
	return fmt.Sprintf("0x%x", t)
}

func elfSectionFlags(flags uint64) string {
	var out string
	if flags&0x1 != 0 {
		out += "W"
	}
	if flags&0x2 != 0 {
		out += "A"
	}
	if flags&0x4 != 0 {
		out += "X"
	}
	if flags&0x10 != 0 {
		out += "M"
	}
	if flags&0x20 != 0 {
		out += "S"
	}
	if flags&0x40 != 0 {
		out += "I"
	}
	if flags&0x400 != 0 {
		out += "T"
	}
	if out == "" {
		return "-"
	}
	return out
}

func elfType(t uint16) string {
	switch t {
	case 1:
		return "REL"
	case 2:
		return "EXEC"
	case 3:
		return "DYN"
	case 4:
		return "CORE"
	}
	return "UNKNOWN"
}

func elfMachine(m uint16) string {
	switch m {
	case 0x03:
		return "x86"
	case 0x28:
		return "ARM"
	case 0x3e:
		return "x86_64"
	case 0xb7:
		return "AArch64"
	case 0x08:
		return "MIPS"
	case 0xf3:
		return "RISC-V"
	}
	return "other"
}

// ParsePE walks the DOS header, PE header, and section headers.
func ParsePE(d *document.Document) (*Field, error) {
	hdr, err := d.Range(0, 64)
	if err != nil || len(hdr) < 64 {
		return nil, errors.New("too short")
	}
	lfanew := int64(binary.LittleEndian.Uint32(hdr[0x3c:0x40]))
	root := &Field{Name: "PE", Type: "container", Offset: 0}
	root.Children = append(root.Children,
		&Field{Name: "e_magic", Offset: 0, Length: 2, Value: "MZ"},
		&Field{Name: "e_lfanew", Offset: 0x3c, Length: 4, Value: fmt.Sprintf("0x%x", lfanew)},
	)
	peHdr, err := d.Range(lfanew, 24)
	if err != nil || len(peHdr) < 24 || string(peHdr[:4]) != "PE\x00\x00" {
		return root, errors.New("no PE signature")
	}
	machine := binary.LittleEndian.Uint16(peHdr[4:6])
	numSections := binary.LittleEndian.Uint16(peHdr[6:8])
	timestamp := binary.LittleEndian.Uint32(peHdr[8:12])
	sizeOptHdr := binary.LittleEndian.Uint16(peHdr[20:22])
	characteristics := binary.LittleEndian.Uint16(peHdr[22:24])
	root.Children = append(root.Children,
		&Field{Name: "Signature", Offset: lfanew, Length: 4, Value: "PE\\x00\\x00"},
		&Field{Name: "Machine", Offset: lfanew + 4, Length: 2, Value: fmt.Sprintf("0x%04x (%s)", machine, peMachine(machine))},
		&Field{Name: "NumberOfSections", Offset: lfanew + 6, Length: 2, Value: fmt.Sprintf("%d", numSections)},
		&Field{Name: "TimeDateStamp", Offset: lfanew + 8, Length: 4, Value: fmt.Sprintf("%d", timestamp)},
		&Field{Name: "SizeOfOptionalHeader", Offset: lfanew + 20, Length: 2, Value: fmt.Sprintf("%d", sizeOptHdr)},
		&Field{Name: "Characteristics", Offset: lfanew + 22, Length: 2, Value: fmt.Sprintf("0x%04x", characteristics)},
	)
	sections := make([]peSectionInfo, 0, numSections)
	optOff := lfanew + 24
	var dataDirs []peDataDirectory
	peIs64 := false
	if sizeOptHdr > 0 {
		opt, err := d.Range(optOff, int64(sizeOptHdr))
		if err == nil && len(opt) >= 2 {
			optional, dirs := parsePEOptionalHeader(opt, optOff)
			root.Children = append(root.Children, optional)
			dataDirs = dirs
			peIs64 = binary.LittleEndian.Uint16(opt[0:2]) == 0x20b
		}
	}
	sectOff := lfanew + 24 + int64(sizeOptHdr)
	for i := 0; i < int(numSections); i++ {
		sh, err := d.Range(sectOff, 40)
		if err != nil || len(sh) < 40 {
			break
		}
		name := string(trimZero(sh[0:8]))
		vsize := binary.LittleEndian.Uint32(sh[8:12])
		vaddr := binary.LittleEndian.Uint32(sh[12:16])
		rsize := binary.LittleEndian.Uint32(sh[16:20])
		raddr := binary.LittleEndian.Uint32(sh[20:24])
		flags := binary.LittleEndian.Uint32(sh[36:40])
		sec := &Field{
			Name: name, Type: "section", Offset: sectOff, Length: 40,
			Value: fmt.Sprintf("vaddr=0x%x vsize=%d raw=0x%x rsize=%d flags=0x%x", vaddr, vsize, raddr, rsize, flags),
		}
		root.Children = append(root.Children, sec)
		sections = append(sections, peSectionInfo{Name: name, VirtualAddress: vaddr, VirtualSize: vsize, RawOffset: raddr, RawSize: rsize, Characteristics: flags})
		sectOff += 40
	}
	if len(dataDirs) > 0 && dataDirs[0].RVA != 0 && dataDirs[0].Size != 0 {
		root.Children = append(root.Children, parsePEExports(d, sections, dataDirs[0])...)
	}
	if len(dataDirs) > 1 && dataDirs[1].RVA != 0 && dataDirs[1].Size != 0 {
		root.Children = append(root.Children, parsePEImports(d, sections, dataDirs[1], peIs64)...)
	}
	if len(dataDirs) > 2 && dataDirs[2].RVA != 0 && dataDirs[2].Size != 0 {
		root.Children = append(root.Children, parsePEResources(d, sections, dataDirs[2])...)
	}
	return root, nil
}

type peDataDirectory struct {
	Name string
	RVA  uint32
	Size uint32
}

type peSectionInfo struct {
	Name            string
	VirtualAddress  uint32
	VirtualSize     uint32
	RawOffset       uint32
	RawSize         uint32
	Characteristics uint32
}

func parsePEOptionalHeader(opt []byte, off int64) (*Field, []peDataDirectory) {
	magic := binary.LittleEndian.Uint16(opt[0:2])
	is64 := magic == 0x20b
	name := "PE32"
	if is64 {
		name = "PE32+"
	}
	field := &Field{Name: "optional_header", Type: "optional_header", Offset: off, Length: int64(len(opt)), Value: name}
	if len(opt) >= 28 {
		field.Children = append(field.Children,
			&Field{Name: "Magic", Type: "u16le", Offset: off, Length: 2, Value: fmt.Sprintf("0x%04x (%s)", magic, name)},
			&Field{Name: "AddressOfEntryPoint", Type: "u32le", Offset: off + 16, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[16:20]))},
			&Field{Name: "BaseOfCode", Type: "u32le", Offset: off + 20, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[20:24]))},
		)
	}
	if is64 && len(opt) >= 112 {
		field.Children = append(field.Children,
			&Field{Name: "ImageBase", Type: "u64le", Offset: off + 24, Length: 8, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint64(opt[24:32]))},
			&Field{Name: "SectionAlignment", Type: "u32le", Offset: off + 32, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[32:36]))},
			&Field{Name: "FileAlignment", Type: "u32le", Offset: off + 36, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[36:40]))},
			&Field{Name: "SizeOfImage", Type: "u32le", Offset: off + 56, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[56:60]))},
			&Field{Name: "Subsystem", Type: "u16le", Offset: off + 68, Length: 2, Value: peSubsystem(binary.LittleEndian.Uint16(opt[68:70]))},
		)
		return field, peDataDirectories(opt, off, 112)
	}
	if !is64 && len(opt) >= 96 {
		field.Children = append(field.Children,
			&Field{Name: "ImageBase", Type: "u32le", Offset: off + 28, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[28:32]))},
			&Field{Name: "SectionAlignment", Type: "u32le", Offset: off + 32, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[32:36]))},
			&Field{Name: "FileAlignment", Type: "u32le", Offset: off + 36, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[36:40]))},
			&Field{Name: "SizeOfImage", Type: "u32le", Offset: off + 56, Length: 4, Value: fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(opt[56:60]))},
			&Field{Name: "Subsystem", Type: "u16le", Offset: off + 68, Length: 2, Value: peSubsystem(binary.LittleEndian.Uint16(opt[68:70]))},
		)
		return field, peDataDirectories(opt, off, 96)
	}
	return field, nil
}

func peDataDirectories(opt []byte, optOff int64, dirOff int) []peDataDirectory {
	if len(opt) < dirOff {
		return nil
	}
	names := []string{"export", "import", "resource", "exception", "certificate", "base_reloc", "debug", "architecture", "global_ptr", "tls", "load_config", "bound_import", "iat", "delay_import", "clr_runtime", "reserved"}
	count := (len(opt) - dirOff) / 8
	if count > len(names) {
		count = len(names)
	}
	out := make([]peDataDirectory, 0, count)
	for i := 0; i < count; i++ {
		at := dirOff + i*8
		rva := binary.LittleEndian.Uint32(opt[at : at+4])
		size := binary.LittleEndian.Uint32(opt[at+4 : at+8])
		out = append(out, peDataDirectory{Name: names[i], RVA: rva, Size: size})
	}
	return out
}

func parsePEImports(d *document.Document, sections []peSectionInfo, dir peDataDirectory, is64 bool) []*Field {
	off, ok := peRVAToOffset(sections, dir.RVA)
	if !ok {
		return nil
	}
	out := []*Field{}
	for i := 0; i < 256; i++ {
		at := int64(off) + int64(i*20)
		buf, err := d.Range(at, 20)
		if err != nil || len(buf) < 20 {
			break
		}
		oft := binary.LittleEndian.Uint32(buf[0:4])
		timestamp := binary.LittleEndian.Uint32(buf[4:8])
		forwarder := binary.LittleEndian.Uint32(buf[8:12])
		nameRVA := binary.LittleEndian.Uint32(buf[12:16])
		firstThunk := binary.LittleEndian.Uint32(buf[16:20])
		if oft == 0 && timestamp == 0 && forwarder == 0 && nameRVA == 0 && firstThunk == 0 {
			break
		}
		name := ""
		if nameOff, ok := peRVAToOffset(sections, nameRVA); ok {
			name = readCString(d, int64(nameOff), 256)
		}
		if name == "" {
			name = fmt.Sprintf("rva_0x%x", nameRVA)
		}
		out = append(out, &Field{
			Name:     fmt.Sprintf("import[%d]", i),
			Type:     "import_descriptor",
			Offset:   at,
			Length:   20,
			Value:    fmt.Sprintf("dll=%s oft=0x%x first_thunk=0x%x timestamp=0x%x", name, oft, firstThunk, timestamp),
			Children: parsePEImportThunks(d, sections, oft, firstThunk, is64),
		})
	}
	return out
}

func parsePEExports(d *document.Document, sections []peSectionInfo, dir peDataDirectory) []*Field {
	off, ok := peRVAToOffset(sections, dir.RVA)
	if !ok {
		return nil
	}
	buf, err := d.Range(int64(off), 40)
	if err != nil || len(buf) < 40 {
		return nil
	}
	nameRVA := binary.LittleEndian.Uint32(buf[12:16])
	ordinalBase := binary.LittleEndian.Uint32(buf[16:20])
	numberOfFunctions := binary.LittleEndian.Uint32(buf[20:24])
	numberOfNames := binary.LittleEndian.Uint32(buf[24:28])
	functionsRVA := binary.LittleEndian.Uint32(buf[28:32])
	namesRVA := binary.LittleEndian.Uint32(buf[32:36])
	ordinalsRVA := binary.LittleEndian.Uint32(buf[36:40])
	module := ""
	if nameOff, ok := peRVAToOffset(sections, nameRVA); ok {
		module = readCString(d, int64(nameOff), 512)
	}
	if module == "" {
		module = fmt.Sprintf("rva_0x%x", nameRVA)
	}
	root := &Field{
		Name:   "exports",
		Type:   "export_directory",
		Offset: int64(off),
		Length: 40,
		Value:  fmt.Sprintf("module=%s ordinal_base=%d functions=%d names=%d", module, ordinalBase, numberOfFunctions, numberOfNames),
	}
	if numberOfNames > 4096 {
		numberOfNames = 4096
	}
	namesOff, namesOK := peRVAToOffset(sections, namesRVA)
	ordinalsOff, ordOK := peRVAToOffset(sections, ordinalsRVA)
	functionsOff, funcsOK := peRVAToOffset(sections, functionsRVA)
	if namesOK && ordOK && funcsOK {
		for i := uint32(0); i < numberOfNames; i++ {
			namePtrAt := int64(namesOff) + int64(i)*4
			namePtr, err := d.Range(namePtrAt, 4)
			if err != nil || len(namePtr) < 4 {
				break
			}
			symNameRVA := binary.LittleEndian.Uint32(namePtr)
			symName := ""
			if symNameOff, ok := peRVAToOffset(sections, symNameRVA); ok {
				symName = readCString(d, int64(symNameOff), 512)
			}
			if symName == "" {
				symName = fmt.Sprintf("rva_0x%x", symNameRVA)
			}
			ordAt := int64(ordinalsOff) + int64(i)*2
			ordBuf, err := d.Range(ordAt, 2)
			if err != nil || len(ordBuf) < 2 {
				break
			}
			index := uint32(binary.LittleEndian.Uint16(ordBuf))
			if index >= numberOfFunctions {
				continue
			}
			funcAt := int64(functionsOff) + int64(index)*4
			funcBuf, err := d.Range(funcAt, 4)
			if err != nil || len(funcBuf) < 4 {
				break
			}
			funcRVA := binary.LittleEndian.Uint32(funcBuf)
			value := fmt.Sprintf("name=%s ordinal=%d rva=0x%x", symName, ordinalBase+index, funcRVA)
			if funcOff, ok := peRVAToOffset(sections, funcRVA); ok {
				value += fmt.Sprintf(" file=0x%x", funcOff)
			}
			root.Children = append(root.Children, &Field{
				Name:   fmt.Sprintf("export[%d] %s", i, symName),
				Type:   "export",
				Offset: namePtrAt,
				Length: 4,
				Value:  value,
			})
		}
	}
	return []*Field{root}
}

func parsePEResources(d *document.Document, sections []peSectionInfo, dir peDataDirectory) []*Field {
	baseOff, ok := peRVAToOffset(sections, dir.RVA)
	if !ok {
		return nil
	}
	root := &Field{
		Name:   "resources",
		Type:   "resource_directory",
		Offset: int64(baseOff),
		Length: int64(dir.Size),
		Value:  fmt.Sprintf("rva=0x%x size=%d", dir.RVA, dir.Size),
	}
	seen := map[uint32]bool{}
	root.Children = parsePEResourceDirectory(d, sections, dir.RVA, baseOff, 0, 0, seen, "")
	return []*Field{root}
}

func parsePEResourceDirectory(d *document.Document, sections []peSectionInfo, baseRVA, baseOff, rel uint32, depth int, seen map[uint32]bool, path string) []*Field {
	if depth > 8 || seen[rel] {
		return nil
	}
	seen[rel] = true
	defer delete(seen, rel)

	at := int64(baseOff + rel)
	buf, err := d.Range(at, 16)
	if err != nil || len(buf) < 16 {
		return nil
	}
	named := binary.LittleEndian.Uint16(buf[12:14])
	ids := binary.LittleEndian.Uint16(buf[14:16])
	count := int(named) + int(ids)
	if count > 1024 {
		count = 1024
	}
	header := &Field{
		Name:   fmt.Sprintf("directory depth=%d", depth),
		Type:   "resource_directory_header",
		Offset: at,
		Length: 16,
		Value:  fmt.Sprintf("named=%d ids=%d timestamp=0x%x version=%d.%d", named, ids, binary.LittleEndian.Uint32(buf[4:8]), binary.LittleEndian.Uint16(buf[10:12]), binary.LittleEndian.Uint16(buf[8:10])),
	}
	out := []*Field{header}
	for i := 0; i < count; i++ {
		entryAt := at + 16 + int64(i)*8
		ent, err := d.Range(entryAt, 8)
		if err != nil || len(ent) < 8 {
			break
		}
		nameRaw := binary.LittleEndian.Uint32(ent[0:4])
		targetRaw := binary.LittleEndian.Uint32(ent[4:8])
		name := peResourceName(d, baseOff, nameRaw, depth)
		targetRel := targetRaw & 0x7fffffff
		nextPath := path
		if nextPath == "" {
			nextPath = name
		} else {
			nextPath += "/" + name
		}
		entry := &Field{
			Name:   fmt.Sprintf("entry[%d] %s", i, name),
			Type:   "resource_entry",
			Offset: entryAt,
			Length: 8,
			Value:  fmt.Sprintf("name=0x%x target=0x%x path=%s", nameRaw, targetRaw, nextPath),
		}
		if targetRaw&0x80000000 != 0 {
			entry.Children = parsePEResourceDirectory(d, sections, baseRVA, baseOff, targetRel, depth+1, seen, nextPath)
		} else {
			entry.Children = parsePEResourceDataEntry(d, sections, baseOff, targetRel, nextPath)
		}
		out = append(out, entry)
	}
	return out
}

func parsePEResourceDataEntry(d *document.Document, sections []peSectionInfo, baseOff, rel uint32, path string) []*Field {
	at := int64(baseOff + rel)
	buf, err := d.Range(at, 16)
	if err != nil || len(buf) < 16 {
		return nil
	}
	dataRVA := binary.LittleEndian.Uint32(buf[0:4])
	size := binary.LittleEndian.Uint32(buf[4:8])
	codePage := binary.LittleEndian.Uint32(buf[8:12])
	dataOff, mapped := peRVAToOffset(sections, dataRVA)
	value := fmt.Sprintf("data_rva=0x%x size=%d codepage=%d", dataRVA, size, codePage)
	if mapped {
		value += fmt.Sprintf(" file=0x%x", dataOff)
	}
	entry := &Field{Name: "data", Type: "resource_data", Offset: at, Length: 16, Value: value}
	if mapped && size > 0 {
		entry.Children = append(entry.Children, peResourcePayloadFields(d, int64(dataOff), size, path, codePage)...)
	}
	return []*Field{entry}
}

func peResourcePayloadFields(d *document.Document, off int64, size uint32, path string, codePage uint32) []*Field {
	if size > 16<<20 {
		return []*Field{{Name: "payload", Type: "resource_payload", Offset: off, Length: int64(size), Value: "too large to decode"}}
	}
	buf, err := d.Range(off, int64(size))
	if err != nil {
		return nil
	}
	out := []*Field{{Name: "payload", Type: "resource_payload", Offset: off, Length: int64(size), Value: fmt.Sprintf("%d bytes", size)}}
	parts := strings.Split(path, "/")
	typ := ""
	if len(parts) > 0 {
		typ = parts[0]
	}
	switch {
	case strings.HasPrefix(typ, "RT_VERSION"):
		out = append(out, decodePEVersionResource(buf, off)...)
	case strings.HasPrefix(typ, "RT_STRING"):
		out = append(out, decodePEStringTable(buf, off)...)
	case strings.HasPrefix(typ, "RT_MANIFEST"):
		out = append(out, &Field{Name: "manifest", Type: "manifest", Offset: off, Length: int64(size), Value: peResourceTextPreview(buf, codePage)})
	default:
		if txt := peResourceTextPreview(buf, codePage); txt != "" {
			out = append(out, &Field{Name: "text_preview", Type: "text", Offset: off, Length: int64(size), Value: txt})
		}
	}
	return out
}

func peResourceName(d *document.Document, baseOff, raw uint32, depth int) string {
	if raw&0x80000000 != 0 {
		rel := raw & 0x7fffffff
		buf, err := d.Range(int64(baseOff+rel), 2)
		if err != nil || len(buf) < 2 {
			return fmt.Sprintf("name@0x%x", rel)
		}
		n := int(binary.LittleEndian.Uint16(buf))
		if n <= 0 || n > 512 {
			return fmt.Sprintf("name@0x%x", rel)
		}
		rawName, err := d.Range(int64(baseOff+rel+2), int64(n*2))
		if err != nil || len(rawName) < n*2 {
			return fmt.Sprintf("name@0x%x", rel)
		}
		return decodeUTF16LE(rawName)
	}
	id := raw & 0xffff
	if depth == 0 {
		return peResourceTypeName(id)
	}
	if depth == 2 {
		return peLanguageName(id)
	}
	return fmt.Sprintf("#%d", id)
}

func peResourceTypeName(id uint32) string {
	names := map[uint32]string{
		1: "RT_CURSOR", 2: "RT_BITMAP", 3: "RT_ICON", 4: "RT_MENU", 5: "RT_DIALOG", 6: "RT_STRING",
		7: "RT_FONTDIR", 8: "RT_FONT", 9: "RT_ACCELERATOR", 10: "RT_RCDATA", 11: "RT_MESSAGETABLE",
		12: "RT_GROUP_CURSOR", 14: "RT_GROUP_ICON", 16: "RT_VERSION", 17: "RT_DLGINCLUDE",
		19: "RT_PLUGPLAY", 20: "RT_VXD", 21: "RT_ANICURSOR", 22: "RT_ANIICON", 23: "RT_HTML", 24: "RT_MANIFEST",
	}
	if name, ok := names[id]; ok {
		return fmt.Sprintf("%s(%d)", name, id)
	}
	return fmt.Sprintf("#%d", id)
}

func peLanguageName(id uint32) string {
	switch id {
	case 0:
		return "LANG_NEUTRAL"
	case 0x0409:
		return "en-US"
	case 0x0809:
		return "en-GB"
	}
	return fmt.Sprintf("lang-0x%04x", id)
}

func decodePEStringTable(buf []byte, off int64) []*Field {
	out := []*Field{}
	pos := 0
	for i := 0; i < 16 && pos+2 <= len(buf); i++ {
		n := int(binary.LittleEndian.Uint16(buf[pos : pos+2]))
		pos += 2
		if pos+n*2 > len(buf) {
			break
		}
		if n > 0 {
			out = append(out, &Field{Name: fmt.Sprintf("string[%d]", i), Type: "resource_string", Offset: off + int64(pos), Length: int64(n * 2), Value: decodeUTF16LE(buf[pos : pos+n*2])})
		}
		pos += n * 2
	}
	return out
}

func decodePEVersionResource(buf []byte, off int64) []*Field {
	if len(buf) < 6 {
		return nil
	}
	key, keyEnd := readPEVersionKey(buf, 6)
	out := []*Field{{Name: "version_info", Type: "version_info", Offset: off, Length: int64(len(buf)), Value: fmt.Sprintf("key=%s length=%d valueLength=%d type=%d", key, binary.LittleEndian.Uint16(buf[0:2]), binary.LittleEndian.Uint16(buf[2:4]), binary.LittleEndian.Uint16(buf[4:6]))}}
	valueOff := align4(keyEnd)
	if key == "VS_VERSION_INFO" && valueOff+52 <= len(buf) {
		fixed := buf[valueOff : valueOff+52]
		if binary.LittleEndian.Uint32(fixed[0:4]) == 0xfeef04bd {
			fileMS := binary.LittleEndian.Uint32(fixed[8:12])
			fileLS := binary.LittleEndian.Uint32(fixed[12:16])
			prodMS := binary.LittleEndian.Uint32(fixed[16:20])
			prodLS := binary.LittleEndian.Uint32(fixed[20:24])
			out = append(out, &Field{Name: "fixed_file_info", Type: "version_fixed", Offset: off + int64(valueOff), Length: 52, Value: fmt.Sprintf("file=%d.%d.%d.%d product=%d.%d.%d.%d flags=0x%x type=0x%x subtype=0x%x",
				fileMS>>16, fileMS&0xffff, fileLS>>16, fileLS&0xffff,
				prodMS>>16, prodMS&0xffff, prodLS>>16, prodLS&0xffff,
				binary.LittleEndian.Uint32(fixed[28:32]), binary.LittleEndian.Uint32(fixed[36:40]), binary.LittleEndian.Uint32(fixed[40:44]))})
		}
	}
	if strings.Contains(string(buf), "StringFileInfo") || strings.Contains(string(buf), "VarFileInfo") {
		text := decodeUTF16LEASCII(buf)
		for _, label := range []string{"CompanyName", "FileDescription", "FileVersion", "ProductName", "ProductVersion", "OriginalFilename"} {
			if strings.Contains(text, label) {
				out = append(out, &Field{Name: label, Type: "version_string_hint", Offset: off, Length: int64(len(buf)), Value: label})
			}
		}
	}
	return out
}

func readPEVersionKey(buf []byte, pos int) (string, int) {
	start := pos
	for pos+1 < len(buf) {
		if buf[pos] == 0 && buf[pos+1] == 0 {
			return decodeUTF16LE(buf[start:pos]), pos + 2
		}
		pos += 2
	}
	return "", pos
}

func align4(n int) int {
	return (n + 3) &^ 3
}

func peResourceTextPreview(buf []byte, codePage uint32) string {
	if len(buf) == 0 {
		return ""
	}
	if len(buf) >= 2 {
		if buf[0] == 0xff && buf[1] == 0xfe {
			return cleanResourcePreview(decodeUTF16LE(buf[2:]))
		}
		if buf[0] == 0xfe && buf[1] == 0xff {
			return cleanResourcePreview(decodeUTF16BE(buf[2:]))
		}
	}
	if codePage == 1200 {
		return cleanResourcePreview(decodeUTF16LE(buf))
	}
	if utf8.Valid(buf) {
		return cleanResourcePreview(string(buf))
	}
	return ""
}

func cleanResourcePreview(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
	if len(s) > 512 {
		s = s[:512] + "..."
	}
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x09 }) >= 0 {
		return ""
	}
	return s
}

func decodeUTF16LE(buf []byte) string {
	u := make([]uint16, 0, len(buf)/2)
	for i := 0; i+1 < len(buf); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(buf[i:i+2]))
	}
	return string(utf16.Decode(u))
}

func decodeUTF16BE(buf []byte) string {
	u := make([]uint16, 0, len(buf)/2)
	for i := 0; i+1 < len(buf); i += 2 {
		u = append(u, binary.BigEndian.Uint16(buf[i:i+2]))
	}
	return string(utf16.Decode(u))
}

func decodeUTF16LEASCII(buf []byte) string {
	var b strings.Builder
	for i := 0; i+1 < len(buf); i += 2 {
		r := binary.LittleEndian.Uint16(buf[i : i+2])
		if r >= 0x20 && r < 0x7f {
			b.WriteByte(byte(r))
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func parsePEImportThunks(d *document.Document, sections []peSectionInfo, oft, firstThunk uint32, is64 bool) []*Field {
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
	out := []*Field{}
	for i := 0; i < 4096; i++ {
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
		value := ""
		if thunk&ordinalMask != 0 {
			value = fmt.Sprintf("ordinal=%d", thunk&0xffff)
		} else if nameOff, ok := peRVAToOffset(sections, uint32(thunk)); ok {
			name := readImportByName(d, int64(nameOff))
			if name != "" {
				value = "name=" + name
			} else {
				value = fmt.Sprintf("name_rva=0x%x", thunk)
			}
		} else {
			value = fmt.Sprintf("thunk=0x%x", thunk)
		}
		out = append(out, &Field{
			Name:   fmt.Sprintf("thunk[%d]", i),
			Type:   "import_thunk",
			Offset: at,
			Length: width,
			Value:  value,
		})
	}
	return out
}

func readImportByName(d *document.Document, off int64) string {
	buf, err := d.Range(off, 258)
	if err != nil || len(buf) < 3 {
		return ""
	}
	return readCStringFrom(buf[2:])
}

func peRVAToOffset(sections []peSectionInfo, rva uint32) (uint32, bool) {
	for _, s := range sections {
		span := s.VirtualSize
		if s.RawSize > span {
			span = s.RawSize
		}
		if span == 0 {
			continue
		}
		if rva >= s.VirtualAddress && rva < s.VirtualAddress+span {
			return s.RawOffset + (rva - s.VirtualAddress), true
		}
	}
	return 0, false
}

func readCString(d *document.Document, off int64, max int64) string {
	buf, err := d.Range(off, max)
	if err != nil {
		return ""
	}
	return readCStringFrom(buf)
}

func readCStringFrom(buf []byte) string {
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func peSubsystem(v uint16) string {
	switch v {
	case 1:
		return "native"
	case 2:
		return "windows_gui"
	case 3:
		return "windows_cui"
	case 7:
		return "posix_cui"
	case 9:
		return "windows_ce_gui"
	case 10:
		return "efi_application"
	case 11:
		return "efi_boot_service_driver"
	case 12:
		return "efi_runtime_driver"
	}
	return fmt.Sprintf("0x%x", v)
}

func peMachine(m uint16) string {
	switch m {
	case 0x14c:
		return "i386"
	case 0x8664:
		return "x86_64"
	case 0x1c0:
		return "ARM"
	case 0xaa64:
		return "ARM64"
	case 0x200:
		return "IA64"
	}
	return "?"
}

func trimZero(b []byte) []byte {
	for i, c := range b {
		if c == 0 {
			return b[:i]
		}
	}
	return b
}

// ParseRIFF walks RIFF chunks.
func ParseRIFF(d *document.Document) (*Field, error) {
	size := d.Size()
	if size < 12 {
		return nil, errors.New("too short")
	}
	hdr, err := d.Range(0, 12)
	if err != nil {
		return nil, err
	}
	total := int64(binary.LittleEndian.Uint32(hdr[4:8]))
	form := string(hdr[8:12])
	end := total + 8
	if end > size {
		end = size
	}
	root := &Field{
		Name: "RIFF", Type: "container", Offset: 0, Length: total + 8,
		Value: fmt.Sprintf("form=%s (%s)", form, riffFormName(form)),
	}
	root.Children = riffChunks(d, 12, end, 0)
	return root, nil
}

// riffChunks walks the chunks in [off,end), recursing into LIST containers and
// decoding the common WAV/AVI chunks.
func riffChunks(d *document.Document, off, end int64, depth int) []*Field {
	var out []*Field
	for off+8 <= end && depth < 16 {
		ch, err := d.Range(off, 8)
		if err != nil || len(ch) < 8 {
			break
		}
		id := string(ch[0:4])
		ln := int64(binary.LittleEndian.Uint32(ch[4:8]))
		body := off + 8
		chunkEnd := body + ln
		if chunkEnd > end {
			chunkEnd = end
		}
		f := &Field{Name: riffChunkLabel(id), Type: "chunk", Offset: off, Length: 8 + ln, Value: fmt.Sprintf("length=%d", ln)}
		switch id {
		case "LIST", "RIFF":
			if ln >= 4 {
				lb, _ := d.Range(body, 4)
				lt := string(lb)
				f.Type = "list"
				f.Value = fmt.Sprintf("type=%s (%s)", lt, riffFormName(lt))
				f.Children = riffChunks(d, body+4, chunkEnd, depth+1)
			}
		case "fmt ":
			if fb, e := d.Range(body, min64(16, ln)); e == nil && len(fb) >= 16 {
				f.Children = []*Field{
					{Name: "audio_format", Type: "u16le", Offset: body, Length: 2, Value: wavFormat(binary.LittleEndian.Uint16(fb[0:2]))},
					{Name: "channels", Type: "u16le", Offset: body + 2, Length: 2, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint16(fb[2:4]))},
					{Name: "sample_rate", Type: "u32le", Offset: body + 4, Length: 4, Value: fmt.Sprintf("%d Hz", binary.LittleEndian.Uint32(fb[4:8]))},
					{Name: "byte_rate", Type: "u32le", Offset: body + 8, Length: 4, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint32(fb[8:12]))},
					{Name: "block_align", Type: "u16le", Offset: body + 12, Length: 2, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint16(fb[12:14]))},
					{Name: "bits_per_sample", Type: "u16le", Offset: body + 14, Length: 2, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint16(fb[14:16]))},
				}
			}
		case "avih":
			if ab, e := d.Range(body, min64(40, ln)); e == nil && len(ab) >= 16 {
				f.Children = []*Field{
					{Name: "microsec_per_frame", Type: "u32le", Offset: body, Length: 4, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint32(ab[0:4]))},
					{Name: "max_bytes_per_sec", Type: "u32le", Offset: body + 4, Length: 4, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint32(ab[4:8]))},
					{Name: "total_frames", Type: "u32le", Offset: body + 16, Length: 4, Value: fmt.Sprintf("%d", binary.LittleEndian.Uint32(ab[16:20]))},
				}
			}
		}
		out = append(out, f)
		off = chunkEnd
		if ln%2 != 0 {
			off++ // chunks are word-aligned
		}
	}
	return out
}

func riffFormName(f string) string {
	switch f {
	case "WAVE":
		return "audio/wav"
	case "AVI ":
		return "video/avi"
	case "WEBP":
		return "image/webp"
	case "ANI ":
		return "animated cursor"
	case "RMID":
		return "MIDI"
	case "INFO":
		return "metadata"
	case "movi":
		return "movie data"
	case "hdrl":
		return "header list"
	case "strl":
		return "stream list"
	default:
		return "?"
	}
}

func riffChunkLabel(id string) string {
	return strings.TrimRight(id, " ")
}

func wavFormat(f uint16) string {
	names := map[uint16]string{1: "PCM", 2: "ADPCM", 3: "IEEE float", 6: "A-law", 7: "mu-law", 0xfffe: "extensible"}
	if n, ok := names[f]; ok {
		return fmt.Sprintf("%d (%s)", f, n)
	}
	return fmt.Sprintf("%d", f)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
