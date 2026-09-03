package analysis

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"

	"github.com/andydixon/link/internal/leviathan/document"
)

// Signature describes a carveable object. `validate` is run against
// the candidate start and returns the object length if it is a real
// object, or 0 to reject. `trailer`, if present, must appear before
// the object ends (for simple trailer-based terminations).
type Signature struct {
	Kind      string
	Magic     []byte
	Extension string
	// Anchor is how many bytes into the object the magic appears. It is 0 for
	// almost every format (magic at offset 0), but a few — notably ISO-BMFF
	// (MP4/MOV), whose `ftyp` magic follows a 4-byte box size — carry it.
	Anchor   int64
	Validate func(d *document.Document, start, max int64) (length int64, confidence float64)
}

// carveSignatures is the phase-1 signature set. We stay conservative
// and only declare a carve when we can determine length reliably.
var carveSignatures = []Signature{
	{Kind: "png", Magic: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, Extension: "png", Validate: validatePNG},
	{Kind: "jpeg", Magic: []byte{0xff, 0xd8, 0xff}, Extension: "jpg", Validate: validateJPEG},
	{Kind: "gif87a", Magic: []byte("GIF87a"), Extension: "gif", Validate: validateGIF},
	{Kind: "gif89a", Magic: []byte("GIF89a"), Extension: "gif", Validate: validateGIF},
	{Kind: "zip", Magic: []byte{'P', 'K', 0x03, 0x04}, Extension: "zip", Validate: validateZIP},
	{Kind: "pdf", Magic: []byte("%PDF-"), Extension: "pdf", Validate: validatePDF},
	{Kind: "elf", Magic: []byte{0x7f, 'E', 'L', 'F'}, Extension: "elf", Validate: validateELF},
	{Kind: "mz", Magic: []byte{'M', 'Z'}, Extension: "exe", Validate: validatePE},
	{Kind: "gzip", Magic: []byte{0x1f, 0x8b, 0x08}, Extension: "gz", Validate: validateGZIP},
	{Kind: "bzip2", Magic: []byte{'B', 'Z', 'h'}, Extension: "bz2", Validate: validateBzip2},
	{Kind: "sevenz", Magic: []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, Extension: "7z", Validate: validate7z},
	{Kind: "rar4", Magic: []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00}, Extension: "rar", Validate: validateRAR},
	{Kind: "rar5", Magic: []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x01, 0x00}, Extension: "rar", Validate: validateRAR},
	{Kind: "wav", Magic: []byte("RIFF"), Extension: "wav", Validate: validateRIFF},
	{Kind: "mach-o-le32", Magic: []byte{0xce, 0xfa, 0xed, 0xfe}, Extension: "macho", Validate: validateMachO},
	{Kind: "mach-o-le64", Magic: []byte{0xcf, 0xfa, 0xed, 0xfe}, Extension: "macho", Validate: validateMachO},
	{Kind: "mach-o-be32", Magic: []byte{0xfe, 0xed, 0xfa, 0xce}, Extension: "macho", Validate: validateMachO},
	{Kind: "mach-o-be64", Magic: []byte{0xfe, 0xed, 0xfa, 0xcf}, Extension: "macho", Validate: validateMachO},
	{Kind: "sqlite", Magic: []byte("SQLite format 3\x00"), Extension: "sqlite", Validate: validateSQLite},
	{Kind: "mp3", Magic: []byte("ID3"), Extension: "mp3", Validate: validateID3},
	{Kind: "ogg", Magic: []byte("OggS"), Extension: "ogg", Validate: validateOgg},
	{Kind: "matroska", Magic: []byte{0x1a, 0x45, 0xdf, 0xa3}, Extension: "mkv", Validate: validateMatroska},
	{Kind: "flac", Magic: []byte("fLaC"), Extension: "flac", Validate: validateFLAC},
	{Kind: "bmp", Magic: []byte{'B', 'M'}, Extension: "bmp", Validate: validateBMP},
	{Kind: "mp4", Magic: []byte("ftyp"), Anchor: 4, Extension: "mp4", Validate: validateMP4},
}

// Carve scans the document for known signatures and returns a list of
// discovered objects with offset, length, kind, and confidence.
func Carve(d *document.Document, start, length int64, maxHits int) ([]map[string]any, error) {
	return CarveContext(context.Background(), d, start, length, maxHits)
}

func CarveContext(ctx context.Context, d *document.Document, start, length int64, maxHits int) ([]map[string]any, error) {
	if length < 0 {
		length = d.Size() - start
	}
	if maxHits <= 0 {
		maxHits = 10_000
	}
	results := make([]map[string]any, 0, 64)

	// Build a flat list of (magicBytes, sigIndex) for a simple first-pass scan.
	// Phase 1 uses per-signature naive scan chunks.
	for idx, sig := range carveSignatures {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hits, err := SearchBytesContext(ctx, d, start, length, sig.Magic, maxHits)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// The object begins Anchor bytes before the magic (0 for most formats).
			objStart := h.Offset - sig.Anchor
			if objStart < start {
				continue
			}
			ln, conf := int64(0), 0.4
			if sig.Validate != nil {
				ln, conf = sig.Validate(d, objStart, start+length)
			}
			if ln <= 0 {
				// record low-confidence magic hit only for kinds where length is not determinable
				if sig.Kind == "mz" || sig.Kind == "elf" || sig.Kind == "mach-o-le32" ||
					sig.Kind == "mach-o-le64" || sig.Kind == "mach-o-be32" || sig.Kind == "mach-o-be64" ||
					sig.Kind == "sqlite" || sig.Kind == "mp3" || sig.Kind == "matroska" ||
					sig.Kind == "flac" || sig.Kind == "bzip2" || sig.Kind == "rar4" || sig.Kind == "rar5" {
					results = append(results, map[string]any{
						"kind":       sig.Kind,
						"extension":  sig.Extension,
						"offset":     objStart,
						"length":     int64(0),
						"confidence": 0.3,
						"status":     "magic-only",
						"sigIndex":   idx,
					})
				}
				continue
			}
			results = append(results, map[string]any{
				"kind":       sig.Kind,
				"extension":  sig.Extension,
				"offset":     objStart,
				"length":     ln,
				"confidence": conf,
				"status":     "valid",
				"sigIndex":   idx,
			})
			if len(results) >= maxHits {
				return results, nil
			}
		}
	}
	return results, nil
}

// --- validators ---

func validatePNG(d *document.Document, start, max int64) (int64, float64) {
	// Walk chunks until IEND
	off := start + 8
	for off < max {
		hdr, err := d.Range(off, 8)
		if err != nil || len(hdr) < 8 {
			return 0, 0
		}
		size := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		// Skip payload + CRC (4 bytes)
		off += 8 + size + 4
		if typ == "IEND" {
			return off - start, 0.95
		}
		if off > max {
			return 0, 0
		}
	}
	return 0, 0
}

func validateJPEG(d *document.Document, start, max int64) (int64, float64) {
	// Walk segments until EOI (0xFFD9)
	off := start + 2
	for off < max-1 {
		buf, err := d.Range(off, 2)
		if err != nil || len(buf) < 2 {
			return 0, 0
		}
		if buf[0] != 0xff {
			return 0, 0
		}
		marker := buf[1]
		off += 2
		// No-length markers
		if marker == 0xd9 { // EOI
			return off - start, 0.95
		}
		if marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x00 {
			continue
		}
		// Read length
		lb, err := d.Range(off, 2)
		if err != nil || len(lb) < 2 {
			return 0, 0
		}
		ln := int64(binary.BigEndian.Uint16(lb))
		off += ln
		if marker == 0xda { // SOS, scan until next 0xff!=0
			for off < max {
				b, err := d.Range(off, 2)
				if err != nil || len(b) < 2 {
					return 0, 0
				}
				if b[0] == 0xff && b[1] != 0x00 && !(b[1] >= 0xd0 && b[1] <= 0xd7) {
					break
				}
				off++
			}
		}
	}
	return 0, 0
}

func validateGIF(d *document.Document, start, max int64) (int64, float64) {
	// Minimal: locate trailer 0x3B
	buf, err := d.Range(start, min64(max-start, 1<<20))
	if err != nil {
		return 0, 0
	}
	idx := bytes.IndexByte(buf[13:], 0x3b)
	if idx < 0 {
		return 0, 0
	}
	return int64(idx + 13 + 1), 0.7
}

func validateZIP(d *document.Document, start, max int64) (int64, float64) {
	// Search forward for End of Central Directory record 0x06054b50.
	const sig = "\x50\x4b\x05\x06"
	limit := min64(max-start, 64<<20)
	buf, err := d.Range(start, limit)
	if err != nil {
		return 0, 0
	}
	idx := bytes.LastIndex(buf, []byte(sig))
	if idx < 0 {
		return 0, 0
	}
	// EOCD record is at least 22 bytes; read comment length.
	if idx+22 > len(buf) {
		return 0, 0
	}
	commentLen := int(binary.LittleEndian.Uint16(buf[idx+20 : idx+22]))
	end := int64(idx + 22 + commentLen)
	return end, 0.9
}

func validatePDF(d *document.Document, start, max int64) (int64, float64) {
	limit := min64(max-start, 64<<20)
	buf, err := d.Range(start, limit)
	if err != nil {
		return 0, 0
	}
	idx := bytes.LastIndex(buf, []byte("%%EOF"))
	if idx < 0 {
		return 0, 0
	}
	return int64(idx + 5), 0.85
}

func validateELF(d *document.Document, start, max int64) (int64, float64) {
	hdr, err := d.Range(start, 64)
	if err != nil || len(hdr) < 52 {
		return 0, 0
	}
	if hdr[4] != 1 && hdr[4] != 2 {
		return 0, 0
	}
	is64 := hdr[4] == 2
	little := hdr[5] == 1
	var end int64
	if is64 {
		if len(hdr) < 64 {
			return 0, 0
		}
		// e_shoff at 40, e_shentsize at 58, e_shnum at 60
		off := binary.LittleEndian.Uint64(hdr[40:48])
		entSz := uint64(binary.LittleEndian.Uint16(hdr[58:60]))
		num := uint64(binary.LittleEndian.Uint16(hdr[60:62]))
		if !little {
			off = binary.BigEndian.Uint64(hdr[40:48])
			entSz = uint64(binary.BigEndian.Uint16(hdr[58:60]))
			num = uint64(binary.BigEndian.Uint16(hdr[60:62]))
		}
		end = int64(off + entSz*num)
	} else {
		off := binary.LittleEndian.Uint32(hdr[32:36])
		entSz := uint32(binary.LittleEndian.Uint16(hdr[46:48]))
		num := uint32(binary.LittleEndian.Uint16(hdr[48:50]))
		if !little {
			off = binary.BigEndian.Uint32(hdr[32:36])
			entSz = uint32(binary.BigEndian.Uint16(hdr[46:48]))
			num = uint32(binary.BigEndian.Uint16(hdr[48:50]))
		}
		end = int64(off) + int64(entSz)*int64(num)
	}
	if end <= 0 || start+end > max {
		return 0, 0.5
	}
	return end, 0.9
}

func validatePE(d *document.Document, start, max int64) (int64, float64) {
	// e_lfanew at offset 0x3C points to PE header
	hdr, err := d.Range(start, 64)
	if err != nil || len(hdr) < 64 {
		return 0, 0
	}
	peOff := int64(binary.LittleEndian.Uint32(hdr[0x3c:0x40]))
	if peOff < 0x40 || start+peOff+4 > max {
		return 0, 0
	}
	sig, err := d.Range(start+peOff, 4)
	if err != nil || string(sig) != "PE\x00\x00" {
		return 0, 0.3
	}
	coff, err := d.Range(start+peOff+4, 20)
	if err != nil || len(coff) < 20 {
		return 0, 0.4
	}
	numSections := int(binary.LittleEndian.Uint16(coff[2:4]))
	sizeOpt := int64(binary.LittleEndian.Uint16(coff[16:18]))
	if numSections <= 0 || numSections > 96 || sizeOpt < 0 || sizeOpt > 4096 {
		return 0, 0.5
	}
	sectOff := start + peOff + 24 + sizeOpt
	end := peOff + 24 + sizeOpt + int64(numSections*40)
	for i := 0; i < numSections; i++ {
		sh, err := d.Range(sectOff+int64(i*40), 40)
		if err != nil || len(sh) < 40 {
			return 0, 0.5
		}
		rawSize := int64(binary.LittleEndian.Uint32(sh[16:20]))
		rawPtr := int64(binary.LittleEndian.Uint32(sh[20:24]))
		if rawSize < 0 || rawPtr < 0 {
			return 0, 0.5
		}
		if rawSize > 0 && rawPtr+rawSize > end {
			end = rawPtr + rawSize
		}
	}
	if end <= peOff || start+end > max {
		return 0, 0.5
	}
	return end, 0.85
}

func validateGZIP(d *document.Document, start, max int64) (int64, float64) {
	limit := min64(max-start, 64<<20)
	buf, err := d.Range(start, limit)
	if err != nil || len(buf) < 18 {
		return 0, 0
	}
	br := bytes.NewReader(buf)
	zr, err := gzip.NewReader(br)
	if err != nil {
		return 0, 0
	}
	zr.Multistream(false)
	if _, err := io.Copy(io.Discard, io.LimitReader(zr, 512<<20)); err != nil {
		_ = zr.Close()
		return 0, 0
	}
	if err := zr.Close(); err != nil {
		return 0, 0
	}
	consumed := int64(len(buf)) - int64(br.Len())
	if consumed <= 10 || start+consumed > max {
		return 0, 0
	}
	return consumed, 0.9
}

// validateBzip2 locates the bit-aligned end-of-stream marker (0x177245385090,
// the "sqrt(pi)" magic) and returns the stream length including the trailing
// 32-bit combined CRC. bzip2 carries no length in its header, so this is the
// only reliable way to bound the object.
func validateBzip2(d *document.Document, start, max int64) (int64, float64) {
	if start+4 > max {
		return 0, 0.4
	}
	hdr, err := d.Range(start, 4)
	if err != nil || len(hdr) < 4 || hdr[0] != 'B' || hdr[1] != 'Z' || hdr[2] != 'h' || hdr[3] < '1' || hdr[3] > '9' {
		return 0, 0
	}
	limit := min64(max-start, 64<<20)
	buf, err := d.Range(start, limit)
	if err != nil || len(buf) < 14 {
		return 0, 0.4
	}
	const eos uint64 = 0x177245385090
	const mask48 uint64 = (1 << 48) - 1
	var w uint64
	for i := 0; i < len(buf); i++ {
		w = (w << 8) | uint64(buf[i])
		// Check all eight bit alignments ending at or before this byte.
		for s := 0; s < 8; s++ {
			if (w>>uint(s))&mask48 == eos {
				endBit := (int64(i)+1)*8 - int64(s) + 32 // include stream CRC
				endByte := (endBit + 7) / 8
				if endByte > 14 && start+endByte <= max {
					return endByte, 0.85
				}
			}
		}
	}
	return 0, 0.4
}

func validate7z(d *document.Document, start, max int64) (int64, float64) {
	hdr, err := d.Range(start, 32)
	if err != nil || len(hdr) < 32 {
		return 0, 0
	}
	nextHdrOff := binary.LittleEndian.Uint64(hdr[12:20])
	nextHdrSz := binary.LittleEndian.Uint64(hdr[20:28])
	end := int64(32 + nextHdrOff + nextHdrSz)
	if end <= 0 || start+end > max {
		return 0, 0.5
	}
	return end, 0.85
}

// validateRAR walks the block headers of a RAR4 or RAR5 archive to the
// end-of-archive marker. The two formats differ structurally, so the version
// is detected from the signature byte at offset 6.
func validateRAR(d *document.Document, start, max int64) (int64, float64) {
	sig, err := d.Range(start, 8)
	if err != nil || len(sig) < 8 {
		return 0, 0
	}
	if sig[6] == 0x01 {
		return validateRAR5(d, start, max)
	}
	return validateRAR4(d, start, max)
}

func validateRAR4(d *document.Document, start, max int64) (int64, float64) {
	pos := start + 7 // 7-byte signature
	for n := 0; n < 100000; n++ {
		h, err := d.Range(pos, 11)
		if err != nil || len(h) < 7 {
			return 0, 0.4
		}
		flags := binary.LittleEndian.Uint16(h[3:5])
		headSize := int64(binary.LittleEndian.Uint16(h[5:7]))
		if headSize < 7 {
			return 0, 0.4
		}
		blockSize := headSize
		if flags&0x8000 != 0 { // ADD_SIZE present
			if len(h) < 11 {
				return 0, 0.4
			}
			blockSize += int64(binary.LittleEndian.Uint32(h[7:11]))
		}
		pos += blockSize
		if h[2] == 0x7b { // terminator block
			if pos > start && pos <= max {
				return pos - start, 0.85
			}
			return 0, 0.4
		}
		if pos > max {
			return 0, 0.4
		}
	}
	return 0, 0.4
}

func validateRAR5(d *document.Document, start, max int64) (int64, float64) {
	pos := start + 8 // 8-byte signature
	for n := 0; n < 100000; n++ {
		// Each block: CRC32(4) + vint(header_size) + header_size bytes [+ data].
		h, err := d.Range(pos, 4+11)
		if err != nil || len(h) < 5 {
			return 0, 0.4
		}
		headerSize, vlen := rar5Vint(h, 4)
		if vlen == 0 || headerSize == 0 {
			return 0, 0.4
		}
		hdrStart := pos + 4 + int64(vlen)
		body, err := d.Range(hdrStart, int64(headerSize))
		if err != nil || int64(len(body)) < int64(headerSize) {
			return 0, 0.4
		}
		htype, t1 := rar5Vint(body, 0)
		hflags, t2 := rar5Vint(body, t1)
		p := t1 + t2
		var dataSize uint64
		if hflags&0x0001 != 0 { // extra area present
			_, te := rar5Vint(body, p)
			p += te
		}
		if hflags&0x0002 != 0 { // data area present
			dataSize, _ = rar5Vint(body, p)
		}
		pos = hdrStart + int64(headerSize) + int64(dataSize)
		if htype == 5 { // end-of-archive header
			if pos > start && pos <= max {
				return pos - start, 0.85
			}
			return 0, 0.4
		}
		if pos > max {
			return 0, 0.4
		}
	}
	return 0, 0.4
}

// rar5Vint decodes a RAR5 little-endian base-128 variable integer.
func rar5Vint(buf []byte, pos int) (uint64, int) {
	var v uint64
	shift := uint(0)
	for n := 0; pos+n < len(buf) && n < 10; n++ {
		b := buf[pos+n]
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, n + 1
		}
		shift += 7
	}
	return 0, 0
}

func validateRIFF(d *document.Document, start, max int64) (int64, float64) {
	hdr, err := d.Range(start, 12)
	if err != nil || len(hdr) < 12 {
		return 0, 0
	}
	size := int64(binary.LittleEndian.Uint32(hdr[4:8]))
	return size + 8, 0.9
}

func validateMachO(d *document.Document, start, max int64) (int64, float64) {
	hdr, err := d.Range(start, 32)
	if err != nil || len(hdr) < 28 {
		return 0, 0
	}
	var (
		bo      binary.ByteOrder
		is64    bool
		hdrSize int64 = 28
	)
	switch binary.BigEndian.Uint32(hdr[0:4]) {
	case 0xfeedface:
		bo = binary.BigEndian
	case 0xfeedfacf:
		bo = binary.BigEndian
		is64 = true
		hdrSize = 32
	case 0xcefaedfe:
		bo = binary.LittleEndian
	case 0xcffaedfe:
		bo = binary.LittleEndian
		is64 = true
		hdrSize = 32
	default:
		return 0, 0
	}
	if is64 && len(hdr) < 32 {
		return 0, 0
	}
	ncmds := bo.Uint32(hdr[16:20])
	sizeofcmds := bo.Uint32(hdr[20:24])
	if ncmds == 0 || ncmds > 10000 || sizeofcmds > 128<<20 {
		return 0, 0
	}
	cmdBytes, err := d.Range(start+hdrSize, int64(sizeofcmds))
	if err != nil || len(cmdBytes) < int(sizeofcmds) {
		return 0, 0
	}
	end := hdrSize + int64(sizeofcmds)
	off := 0
	for i := uint32(0); i < ncmds; i++ {
		if off+8 > len(cmdBytes) {
			return 0, 0
		}
		cmd := bo.Uint32(cmdBytes[off : off+4])
		cmdSize := int(bo.Uint32(cmdBytes[off+4 : off+8]))
		if cmdSize < 8 || off+cmdSize > len(cmdBytes) {
			return 0, 0
		}
		switch cmd {
		case 0x1: // LC_SEGMENT
			if cmdSize >= 56 {
				fileOff := int64(bo.Uint32(cmdBytes[off+32 : off+36]))
				fileSize := int64(bo.Uint32(cmdBytes[off+36 : off+40]))
				if fileOff >= 0 && fileSize >= 0 && fileOff+fileSize > end {
					end = fileOff + fileSize
				}
			}
		case 0x19: // LC_SEGMENT_64
			if cmdSize >= 72 {
				fileOff := int64(bo.Uint64(cmdBytes[off+40 : off+48]))
				fileSize := int64(bo.Uint64(cmdBytes[off+48 : off+56]))
				if fileOff >= 0 && fileSize >= 0 && fileOff+fileSize > end {
					end = fileOff + fileSize
				}
			}
		}
		off += cmdSize
	}
	if end <= hdrSize || start+end > max {
		return 0, 0
	}
	return end, 0.85
}

func validateID3(d *document.Document, start, max int64) (int64, float64) {
	// ID3v2 header: "ID3" + ver(2) + flags(1) + size(4, synchsafe).
	hdr, err := d.Range(start, 10)
	if err != nil || len(hdr) < 10 {
		return 0, 0.3
	}
	if hdr[3] == 0xff || hdr[4] == 0xff {
		return 0, 0.3
	}
	// Synchsafe 28-bit size (7 bits per byte).
	sz := (int64(hdr[6]&0x7f) << 21) | (int64(hdr[7]&0x7f) << 14) |
		(int64(hdr[8]&0x7f) << 7) | int64(hdr[9]&0x7f)
	if sz < 0 || sz > 32*1024*1024 {
		return 0, 0.4
	}
	// Skip the ID3v2 tag (header + synchsafe body, plus a 10-byte footer if the
	// footer-present flag is set), then walk MPEG audio frames to the end.
	audioStart := start + 10 + sz
	if hdr[5]&0x10 != 0 {
		audioStart += 10
	}
	if end := walkMP3Frames(d, audioStart, max); end > audioStart {
		return end - start, 0.85
	}
	return 0, 0.7
}

// walkMP3Frames follows MPEG-1/2/2.5 Layer I/II/III frame headers from start
// until a byte that is not a valid frame sync, returning the offset just past
// the last frame. APE/Lyrics3 trailers are not stripped — the audio extent is
// what matters for carving.
func walkMP3Frames(d *document.Document, start, max int64) int64 {
	pos := start
	frames := 0
	for pos+4 <= max && frames < 5_000_000 {
		h, err := d.Range(pos, 4)
		if err != nil || len(h) < 4 {
			break
		}
		ln := mp3FrameLen(h)
		if ln <= 0 {
			break
		}
		pos += ln
		frames++
	}
	if frames == 0 {
		return 0
	}
	if pos > max {
		return max
	}
	return pos
}

var mp3BitrateV1 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
var mp3BitrateV2 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
var mp3SampleRate = map[int][3]int{ // [version]{rate index 0,1,2}
	3: {44100, 48000, 32000}, // MPEG1
	2: {22050, 24000, 16000}, // MPEG2
	0: {11025, 12000, 8000},  // MPEG2.5
}

// mp3FrameLen returns the byte length of the MPEG audio frame whose 4-byte
// header is h, or 0 if h is not a valid frame header.
func mp3FrameLen(h []byte) int64 {
	if h[0] != 0xff || h[1]&0xe0 != 0xe0 {
		return 0
	}
	version := int(h[1] >> 3 & 0x03) // 3=MPEG1, 2=MPEG2, 0=MPEG2.5
	layer := int(h[1] >> 1 & 0x03)   // 3=I, 2=II, 1=III
	if version == 1 || layer == 0 {
		return 0 // reserved
	}
	brIndex := int(h[2] >> 4 & 0x0f)
	srIndex := int(h[2] >> 2 & 0x03)
	padding := int(h[2] >> 1 & 0x01)
	if brIndex == 0 || brIndex == 15 || srIndex == 3 {
		return 0
	}
	var bitrate int
	if version == 3 {
		bitrate = mp3BitrateV1[brIndex]
	} else {
		bitrate = mp3BitrateV2[brIndex]
	}
	rates, ok := mp3SampleRate[version]
	if !ok || bitrate == 0 {
		return 0
	}
	sampleRate := rates[srIndex]
	if sampleRate == 0 {
		return 0
	}
	bps := bitrate * 1000
	if layer == 3 { // Layer I
		return int64(((12 * bps / sampleRate) + padding) * 4)
	}
	// Layer II & III. MPEG2/2.5 Layer III uses 72 samples-per-byte factor.
	coef := 144
	if version != 3 && layer == 1 {
		coef = 72
	}
	return int64(coef*bps/sampleRate + padding)
}

func validateOgg(d *document.Document, start, max int64) (int64, float64) {
	off := start
	first := true
	for off+27 <= max {
		hdr, err := d.Range(off, 27)
		if err != nil || len(hdr) < 27 || string(hdr[:4]) != "OggS" || hdr[4] != 0 {
			return 0, 0
		}
		headerType := hdr[5]
		if first && headerType&0x02 == 0 {
			return 0, 0.4
		}
		segCount := int(hdr[26])
		lacing, err := d.Range(off+27, int64(segCount))
		if err != nil || len(lacing) < segCount {
			return 0, 0
		}
		payload := int64(0)
		for _, v := range lacing {
			payload += int64(v)
		}
		pageLen := int64(27 + segCount)
		if payload < 0 || off+pageLen+payload > max {
			return 0, 0
		}
		off += pageLen + payload
		if headerType&0x04 != 0 {
			return off - start, 0.9
		}
		first = false
	}
	return 0, 0.6
}

// validateMatroska reads the EBML header element and the following Segment
// element's size to bound a Matroska/WebM stream. A Segment with "unknown size"
// (all size bits set, as live-streamed files use) cannot be bounded this way.
func validateMatroska(d *document.Document, start, max int64) (int64, float64) {
	buf, err := d.Range(start, min64(max-start, 4096))
	if err != nil || len(buf) < 8 {
		return 0, 0
	}
	// EBML header: ID 0x1A45DFA3, then a vint size.
	if !(buf[0] == 0x1a && buf[1] == 0x45 && buf[2] == 0xdf && buf[3] == 0xa3) {
		return 0, 0
	}
	hsz, n, unknown := ebmlSize(buf, 4)
	if n == 0 || unknown {
		return 0, 0.6
	}
	pos := 4 + n + int(hsz) // start of the Segment element
	if pos+4 > len(buf) {
		return 0, 0.6
	}
	// Segment: ID 0x18538067, then a vint size.
	if !(buf[pos] == 0x18 && buf[pos+1] == 0x53 && buf[pos+2] == 0x80 && buf[pos+3] == 0x67) {
		return 0, 0.6
	}
	segSz, sn, segUnknown := ebmlSize(buf, pos+4)
	if sn == 0 || segUnknown {
		return 0, 0.6 // live/streamed segment without a known size
	}
	end := int64(pos+4+sn) + segSz
	if end <= 0 || start+end > max {
		return 0, 0.6
	}
	return end, 0.9
}

// ebmlSize decodes an EBML variable-length integer (data size form). The length
// is the number of leading zero bits in the first byte plus one; the value is
// the remaining bits. unknown is true when every value bit is set.
func ebmlSize(buf []byte, pos int) (val int64, n int, unknown bool) {
	if pos >= len(buf) || buf[pos] == 0 {
		return 0, 0, false
	}
	first := buf[pos]
	mask := byte(0x80)
	length := 1
	for first&mask == 0 {
		mask >>= 1
		length++
	}
	if length > 8 || pos+length > len(buf) {
		return 0, 0, false
	}
	low := mask - 1
	v := int64(first & low)
	allOnes := (first & low) == low
	for i := 1; i < length; i++ {
		b := buf[pos+i]
		v = (v << 8) | int64(b)
		if b != 0xff {
			allOnes = false
		}
	}
	return v, length, allOnes
}

func validateFLAC(d *document.Document, start, max int64) (int64, float64) {
	return 0, 0.8
}

// validateBMP reads the BITMAPFILEHEADER size field (bfSize), sanity-checking
// the embedded pixel-data offset against the declared length.
func validateBMP(d *document.Document, start, max int64) (int64, float64) {
	hdr, err := d.Range(start, 14)
	if err != nil || len(hdr) < 14 {
		return 0, 0
	}
	size := int64(binary.LittleEndian.Uint32(hdr[2:6]))
	dataOff := int64(binary.LittleEndian.Uint32(hdr[10:14]))
	if size < 26 || dataOff < 26 || dataOff > size || start+size > max {
		return 0, 0
	}
	return size, 0.8
}

// validateMP4 walks the top-level boxes of an ISO base-media file (MP4/MOV/M4A)
// from the file start (the ftyp box). 64-bit box sizes are honoured.
func validateMP4(d *document.Document, start, max int64) (int64, float64) {
	pos := start
	boxes := 0
	for pos+8 <= max && boxes < 100000 {
		h, err := d.Range(pos, 16)
		if err != nil || len(h) < 8 {
			break
		}
		size := int64(binary.BigEndian.Uint32(h[0:4]))
		switch {
		case size == 1: // 64-bit largesize follows the type
			if len(h) < 16 {
				return 0, 0.4
			}
			size = int64(binary.BigEndian.Uint64(h[8:16]))
		case size == 0: // box extends to end of file
			return max - start, 0.8
		}
		if size < 8 {
			break
		}
		pos += size
		boxes++
		if pos > max {
			return 0, 0.4
		}
	}
	if pos > start && pos <= max && boxes > 0 {
		return pos - start, 0.85
	}
	return 0, 0.4
}

func validateSQLite(d *document.Document, start, max int64) (int64, float64) {
	hdr, err := d.Range(start, 32)
	if err != nil || len(hdr) < 32 {
		return 0, 0
	}
	pageSize := int64(binary.BigEndian.Uint16(hdr[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	pages := int64(binary.BigEndian.Uint32(hdr[28:32]))
	if pages == 0 || pageSize == 0 {
		return 0, 0.5
	}
	return pageSize * pages, 0.9
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
