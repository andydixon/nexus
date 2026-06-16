package stego

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// MetaEntry is one piece of textual/EXIF metadata recovered from an image.
type MetaEntry struct {
	Offset    int64  `json:"offset"`
	Length    int64  `json:"length"`
	Container string `json:"container"`
	Kind      string `json:"kind"`
	Keyword   string `json:"keyword"`
	Value     string `json:"value"`
	Note      string `json:"note,omitempty"`
}

// ScanMetadata walks PNG chunks or JPEG segments (APPn / COM) and decodes
// human-readable metadata. Output is best-effort — malformed containers
// return whatever has been parsed up to the failure point.
func ScanMetadata(d *document.Document) ([]MetaEntry, error) {
	size := d.Size()
	if size < 8 {
		return nil, errors.New("file too small")
	}
	head, err := d.Range(0, minInt64(16, size))
	if err != nil {
		return nil, err
	}
	switch {
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return scanPNGMeta(d)
	case len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		return scanJPEGMeta(d)
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		return scanGIFMeta(d)
	}
	return nil, errors.New("unsupported container (PNG, JPEG and GIF handled)")
}

func scanPNGMeta(d *document.Document) ([]MetaEntry, error) {
	size := d.Size()
	out := []MetaEntry{}
	off := int64(8)
	for off+12 <= size {
		hdr, err := d.Range(off, 8)
		if err != nil {
			break
		}
		ln := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		if ln < 0 || off+12+ln > size {
			break
		}
		if typ == "tEXt" || typ == "zTXt" || typ == "iTXt" {
			data, err := d.Range(off+8, ln)
			if err == nil {
				entry := decodePNGTextChunk(typ, data)
				entry.Offset = off
				entry.Length = 12 + ln
				entry.Container = "png"
				out = append(out, entry)
			}
		}
		off += 12 + ln
		if typ == "IEND" {
			break
		}
	}
	return out, nil
}

func decodePNGTextChunk(typ string, data []byte) MetaEntry {
	e := MetaEntry{Kind: typ}
	i := bytes.IndexByte(data, 0)
	if i < 0 {
		e.Note = "no keyword separator"
		e.Value = safeText(data)
		return e
	}
	e.Keyword = string(data[:i])
	rest := data[i+1:]
	switch typ {
	case "tEXt":
		e.Value = safeText(rest)
	case "zTXt":
		if len(rest) < 1 {
			e.Note = "truncated"
			return e
		}
		// rest[0] = compression method (0 = zlib)
		method := rest[0]
		raw, err := zlibDecompress(rest[1:])
		if err != nil {
			e.Note = fmt.Sprintf("zlib fail (method=%d): %v", method, err)
			return e
		}
		e.Value = safeText(raw)
	case "iTXt":
		// compression flag, compression method, language tag\0, translated keyword\0, text
		if len(rest) < 2 {
			e.Note = "truncated"
			return e
		}
		compressed := rest[0] == 1
		rest = rest[2:]
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			e.Value = safeText(rest)
			return e
		}
		lang := string(rest[:j])
		rest = rest[j+1:]
		k := bytes.IndexByte(rest, 0)
		if k < 0 {
			e.Value = safeText(rest)
			return e
		}
		trk := string(rest[:k])
		rest = rest[k+1:]
		if compressed {
			if raw, err := zlibDecompress(rest); err == nil {
				rest = raw
			} else {
				e.Note = "zlib fail: " + err.Error()
			}
		}
		v := safeText(rest)
		if lang != "" || trk != "" {
			v = fmt.Sprintf("[%s/%s] %s", lang, trk, v)
		}
		e.Value = v
	}
	return e
}

func scanJPEGMeta(d *document.Document) ([]MetaEntry, error) {
	size := d.Size()
	out := []MetaEntry{}
	off := int64(2) // skip SOI
	for off+4 <= size {
		hdr, err := d.Range(off, 2)
		if err != nil || hdr[0] != 0xff {
			break
		}
		marker := hdr[1]
		// Standalone markers (no payload): SOI/EOI/RSTn
		if marker == 0xd9 || marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) {
			off += 2
			continue
		}
		// Read segment length (big-endian, includes the length bytes themselves)
		lnBytes, err := d.Range(off+2, 2)
		if err != nil {
			break
		}
		segLen := int64(binary.BigEndian.Uint16(lnBytes))
		if segLen < 2 || off+2+segLen > size {
			break
		}
		payload, err := d.Range(off+4, segLen-2)
		if err != nil {
			break
		}
		switch {
		case marker == 0xfe: // COM
			out = append(out, MetaEntry{
				Offset: off, Length: 2 + segLen,
				Container: "jpeg", Kind: "COM",
				Value: safeText(payload),
			})
		case marker == 0xe1: // APP1 — EXIF / XMP
			entries := decodeJPEGApp1(payload)
			for _, e := range entries {
				e.Offset = off
				e.Length = 2 + segLen
				e.Container = "jpeg"
				out = append(out, e)
			}
		case marker == 0xe0: // APP0 — JFIF
			out = append(out, MetaEntry{
				Offset: off, Length: 2 + segLen,
				Container: "jpeg", Kind: "APP0",
				Keyword: sniffMarker(payload),
				Value:   fmt.Sprintf("%d bytes", len(payload)),
			})
		case marker >= 0xe2 && marker <= 0xef: // APP2..APP15
			out = append(out, MetaEntry{
				Offset: off, Length: 2 + segLen,
				Container: "jpeg", Kind: fmt.Sprintf("APP%d", marker-0xe0),
				Keyword: sniffMarker(payload),
				Value:   fmt.Sprintf("%d bytes", len(payload)),
			})
		}
		off += 2 + segLen
		// SOS (Start of Scan) — entropy-coded data follows, stop walking.
		if marker == 0xda {
			break
		}
	}
	return out, nil
}

func sniffMarker(p []byte) string {
	i := bytes.IndexByte(p, 0)
	if i < 0 || i > 16 {
		if len(p) > 16 {
			return string(p[:16])
		}
		return ""
	}
	return string(p[:i])
}

func decodeJPEGApp1(p []byte) []MetaEntry {
	out := []MetaEntry{}
	if len(p) >= 6 && string(p[:6]) == "Exif\x00\x00" {
		tiff := p[6:]
		entries, err := decodeEXIF(tiff)
		if err == nil {
			out = append(out, entries...)
		} else {
			out = append(out, MetaEntry{Kind: "APP1/EXIF", Note: err.Error()})
		}
		return out
	}
	if len(p) >= 28 && string(p[:28]) == "http://ns.adobe.com/xap/1.0/" {
		xmp := p[29:]
		out = append(out, MetaEntry{
			Kind: "APP1/XMP", Keyword: "xmp", Value: safeText(xmp),
		})
		return out
	}
	out = append(out, MetaEntry{
		Kind: "APP1", Keyword: sniffMarker(p), Value: fmt.Sprintf("%d bytes", len(p)),
	})
	return out
}

// EXIF reader: TIFF header + IFD0 + IFD1 + Exif SubIFD + GPS SubIFD +
// Interoperability IFD. Handles ASCII/BYTE/SHORT/LONG/RATIONAL/SRATIONAL
// values, plus UNDEFINED as hex preview. Detects MakerNote presence.
func decodeEXIF(tiff []byte) ([]MetaEntry, error) {
	if len(tiff) < 8 {
		return nil, errors.New("short TIFF header")
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, errors.New("bad TIFF byte-order mark")
	}
	if bo.Uint16(tiff[2:4]) != 0x002a {
		return nil, errors.New("bad TIFF magic")
	}
	ifd0 := int(bo.Uint32(tiff[4:8]))
	out := []MetaEntry{}
	ptrs := ifdPointers{}
	nextIFD := 0
	out = append(out, readIFD(tiff, ifd0, bo, "IFD0", &ptrs, &nextIFD)...)
	// IFD1 (thumbnail) if present.
	if nextIFD > 0 {
		out = append(out, readIFD(tiff, nextIFD, bo, "IFD1", nil, nil)...)
	}
	if ptrs.exif > 0 {
		out = append(out, readIFD(tiff, ptrs.exif, bo, "ExifIFD", &ptrs, nil)...)
	}
	if ptrs.gps > 0 {
		out = append(out, readIFD(tiff, ptrs.gps, bo, "GPSIFD", nil, nil)...)
	}
	if ptrs.interop > 0 {
		out = append(out, readIFD(tiff, ptrs.interop, bo, "InteropIFD", nil, nil)...)
	}
	return out, nil
}

type ifdPointers struct {
	exif    int
	gps     int
	interop int
}

func readIFD(tiff []byte, off int, bo binary.ByteOrder, name string, ptrs *ifdPointers, nextPtr *int) []MetaEntry {
	out := []MetaEntry{}
	if off < 0 || off+2 > len(tiff) {
		return out
	}
	n := int(bo.Uint16(tiff[off : off+2]))
	base := off + 2
	// Cap directory count to head off hostile input.
	if n > 512 {
		n = 512
	}
	for i := 0; i < n; i++ {
		p := base + i*12
		if p+12 > len(tiff) {
			break
		}
		tag := bo.Uint16(tiff[p : p+2])
		typ := bo.Uint16(tiff[p+2 : p+4])
		count := int(bo.Uint32(tiff[p+4 : p+8]))
		valRaw := tiff[p+8 : p+12]
		// Sub-IFD pointers never appear as regular entries in the output.
		if ptrs != nil {
			switch tag {
			case 0x8769: // Exif SubIFD pointer
				ptrs.exif = int(bo.Uint32(valRaw))
				continue
			case 0x8825: // GPS IFD pointer
				ptrs.gps = int(bo.Uint32(valRaw))
				continue
			case 0xa005: // Interoperability IFD pointer
				ptrs.interop = int(bo.Uint32(valRaw))
				continue
			}
		}
		e := MetaEntry{Kind: name, Keyword: exifTagName(tag, name)}
		if tag == 0x927c { // MakerNote
			e.Value = fmt.Sprintf("%d bytes (vendor-specific, not decoded)", count)
			out = append(out, e)
			continue
		}
		e.Value = readEXIFValue(tiff, bo, typ, count, valRaw)
		out = append(out, e)
	}
	// "Next IFD" pointer follows the directory entries (4 bytes).
	if nextPtr != nil {
		tail := base + n*12
		if tail+4 <= len(tiff) {
			*nextPtr = int(bo.Uint32(tiff[tail : tail+4]))
		}
	}
	return out
}

func readEXIFValue(tiff []byte, bo binary.ByteOrder, typ uint16, count int, valRaw []byte) string {
	typeSize := map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 7: 1, 9: 4, 10: 8}
	sz, ok := typeSize[typ]
	if !ok {
		return fmt.Sprintf("type=%d count=%d", typ, count)
	}
	total := sz * count
	var data []byte
	if total <= 4 {
		data = valRaw[:total]
	} else {
		off := int(bo.Uint32(valRaw))
		if off < 0 || off+total > len(tiff) {
			return fmt.Sprintf("type=%d count=%d (oob)", typ, count)
		}
		data = tiff[off : off+total]
	}
	switch typ {
	case 2: // ASCII (null-terminated)
		return safeText(bytes.TrimRight(data, "\x00"))
	case 1, 7: // BYTE, UNDEFINED — show as hex preview
		if len(data) > 32 {
			return fmt.Sprintf("%x… (%d bytes)", data[:32], len(data))
		}
		return fmt.Sprintf("%x", data)
	case 3: // SHORT
		parts := []string{}
		for i := 0; i < count && i < 8; i++ {
			parts = append(parts, fmt.Sprintf("%d", bo.Uint16(data[i*2:i*2+2])))
		}
		if count > 8 {
			parts = append(parts, "…")
		}
		return strings.Join(parts, ", ")
	case 4: // LONG
		parts := []string{}
		for i := 0; i < count && i < 6; i++ {
			parts = append(parts, fmt.Sprintf("%d", bo.Uint32(data[i*4:i*4+4])))
		}
		if count > 6 {
			parts = append(parts, "…")
		}
		return strings.Join(parts, ", ")
	case 5, 10: // RATIONAL / SRATIONAL
		parts := []string{}
		for i := 0; i < count && i < 4; i++ {
			n := bo.Uint32(data[i*8 : i*8+4])
			dn := bo.Uint32(data[i*8+4 : i*8+8])
			if dn == 0 {
				parts = append(parts, fmt.Sprintf("%d/0", n))
			} else {
				parts = append(parts, fmt.Sprintf("%d/%d", n, dn))
			}
		}
		return strings.Join(parts, ", ")
	case 9: // SLONG
		parts := []string{}
		for i := 0; i < count && i < 6; i++ {
			parts = append(parts, fmt.Sprintf("%d", int32(bo.Uint32(data[i*4:i*4+4]))))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("type=%d count=%d", typ, count)
}

func exifTagName(tag uint16, ifd string) string {
	if ifd == "GPSIFD" {
		if n, ok := gpsTags[tag]; ok {
			return n
		}
		return fmt.Sprintf("GPS tag 0x%04x", tag)
	}
	if n, ok := exifTags[tag]; ok {
		return n
	}
	return fmt.Sprintf("tag 0x%04x", tag)
}

// exifTags covers the standard TIFF/Exif tags routinely seen in
// JPEG/TIFF images produced by consumer cameras and phones.
var exifTags = map[uint16]string{
	// TIFF / IFD0
	0x0100: "ImageWidth", 0x0101: "ImageLength",
	0x0102: "BitsPerSample", 0x0103: "Compression",
	0x0106: "PhotometricInterpretation", 0x010a: "FillOrder",
	0x010d: "DocumentName", 0x010e: "ImageDescription",
	0x010f: "Make", 0x0110: "Model", 0x0111: "StripOffsets",
	0x0112: "Orientation", 0x0115: "SamplesPerPixel",
	0x0116: "RowsPerStrip", 0x0117: "StripByteCounts",
	0x011a: "XResolution", 0x011b: "YResolution",
	0x011c: "PlanarConfiguration", 0x0128: "ResolutionUnit",
	0x012d: "TransferFunction", 0x0131: "Software", 0x0132: "DateTime",
	0x013b: "Artist", 0x013c: "HostComputer",
	0x013e: "WhitePoint", 0x013f: "PrimaryChromaticities",
	0x0211: "YCbCrCoefficients", 0x0212: "YCbCrSubSampling",
	0x0213: "YCbCrPositioning", 0x0214: "ReferenceBlackWhite",
	0x02bc: "XMP",
	0x8298: "Copyright", 0x83bb: "IPTCNAA",
	0x8769: "ExifIFDPointer", 0x8773: "InterColorProfile",
	0x8825: "GPSIFDPointer",
	// Exif SubIFD
	0x829a: "ExposureTime", 0x829d: "FNumber",
	0x8822: "ExposureProgram", 0x8824: "SpectralSensitivity",
	0x8827: "ISOSpeedRatings", 0x8828: "OECF",
	0x9000: "ExifVersion",
	0x9003: "DateTimeOriginal", 0x9004: "DateTimeDigitized",
	0x9010: "OffsetTime", 0x9011: "OffsetTimeOriginal", 0x9012: "OffsetTimeDigitized",
	0x9101: "ComponentsConfiguration", 0x9102: "CompressedBitsPerPixel",
	0x9201: "ShutterSpeedValue", 0x9202: "ApertureValue",
	0x9203: "BrightnessValue", 0x9204: "ExposureBiasValue",
	0x9205: "MaxApertureValue", 0x9206: "SubjectDistance",
	0x9207: "MeteringMode", 0x9208: "LightSource", 0x9209: "Flash",
	0x920a: "FocalLength",
	0x9214: "SubjectArea", 0x927c: "MakerNote", 0x9286: "UserComment",
	0x9290: "SubSecTime", 0x9291: "SubSecTimeOriginal", 0x9292: "SubSecTimeDigitized",
	0xa000: "FlashpixVersion", 0xa001: "ColorSpace",
	0xa002: "PixelXDimension", 0xa003: "PixelYDimension",
	0xa004: "RelatedSoundFile", 0xa005: "InteropIFDPointer",
	0xa20b: "FlashEnergy", 0xa20c: "SpatialFrequencyResponse",
	0xa20e: "FocalPlaneXResolution", 0xa20f: "FocalPlaneYResolution",
	0xa210: "FocalPlaneResolutionUnit",
	0xa214: "SubjectLocation", 0xa215: "ExposureIndex",
	0xa217: "SensingMethod", 0xa300: "FileSource", 0xa301: "SceneType",
	0xa302: "CFAPattern", 0xa401: "CustomRendered",
	0xa402: "ExposureMode", 0xa403: "WhiteBalance",
	0xa404: "DigitalZoomRatio", 0xa405: "FocalLengthIn35mmFilm",
	0xa406: "SceneCaptureType", 0xa407: "GainControl",
	0xa408: "Contrast", 0xa409: "Saturation", 0xa40a: "Sharpness",
	0xa40b: "DeviceSettingDescription", 0xa40c: "SubjectDistanceRange",
	0xa420: "ImageUniqueID", 0xa430: "OwnerName", 0xa431: "SerialNumber",
	0xa432: "LensSpecification", 0xa433: "LensMake", 0xa434: "LensModel",
	0xa435: "LensSerialNumber",
	// Thumbnail IFD1 re-uses IFD0 tags; additional ones:
	0x0201: "JPEGInterchangeFormat", 0x0202: "JPEGInterchangeFormatLength",
}

var gpsTags = map[uint16]string{
	0x0000: "GPSVersionID", 0x0001: "GPSLatitudeRef", 0x0002: "GPSLatitude",
	0x0003: "GPSLongitudeRef", 0x0004: "GPSLongitude",
	0x0005: "GPSAltitudeRef", 0x0006: "GPSAltitude",
	0x0007: "GPSTimeStamp", 0x0008: "GPSSatellites", 0x0009: "GPSStatus",
	0x000a: "GPSMeasureMode", 0x000b: "GPSDOP", 0x000c: "GPSSpeedRef",
	0x000d: "GPSSpeed", 0x000e: "GPSTrackRef", 0x000f: "GPSTrack",
	0x0010: "GPSImgDirectionRef", 0x0011: "GPSImgDirection",
	0x0012: "GPSMapDatum", 0x0013: "GPSDestLatitudeRef", 0x0014: "GPSDestLatitude",
	0x0015: "GPSDestLongitudeRef", 0x0016: "GPSDestLongitude",
	0x0017: "GPSDestBearingRef", 0x0018: "GPSDestBearing",
	0x0019: "GPSDestDistanceRef", 0x001a: "GPSDestDistance",
	0x001b: "GPSProcessingMethod", 0x001c: "GPSAreaInformation",
	0x001d: "GPSDateStamp", 0x001e: "GPSDifferential", 0x001f: "GPSHPositioningError",
}

func zlibDecompress(b []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	// 2 MiB cap — textual metadata should never be near this.
	lim := &io.LimitedReader{R: r, N: 2 * 1024 * 1024}
	return io.ReadAll(lim)
}

func safeText(b []byte) string {
	// Preserve text but trim extreme size and strip control chars other than tab/newline.
	if len(b) > 4096 {
		b = b[:4096]
	}
	sb := strings.Builder{}
	sb.Grow(len(b))
	for _, c := range b {
		if c == '\t' || c == '\n' || c == '\r' || (c >= 0x20 && c <= 0x7e) {
			sb.WriteByte(c)
			continue
		}
		if c >= 0x80 {
			// Pass through; browser will render as UTF-8 if valid.
			sb.WriteByte(c)
			continue
		}
		// Drop other controls.
	}
	return sb.String()
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// scanGIFMeta walks a GIF file and surfaces every extension block that
// can carry human-readable data: Comment (0x21 0xFE), Plain Text
// (0x21 0x01), and Application (0x21 0xFF — XMP, Netscape loop, etc.).
func scanGIFMeta(d *document.Document) ([]MetaEntry, error) {
	size := d.Size()
	out := []MetaEntry{}
	// Skip header (6) + logical screen descriptor (7 fixed) and the
	// optional global colour table.
	hdr, err := d.Range(0, 13)
	if err != nil {
		return nil, err
	}
	off := int64(13)
	packed := hdr[10]
	if packed&0x80 != 0 {
		gctSize := int64(3) * (int64(1) << uint((packed&0x07)+1))
		off += gctSize
	}
	for off < size {
		b, err := d.Range(off, 1)
		if err != nil {
			break
		}
		switch b[0] {
		case 0x3b: // trailer
			return out, nil
		case 0x2c: // Image Descriptor: skip descriptor + optional LCT + LZW data
			next, ok := skipGIFImage(d, off)
			if !ok {
				return out, nil
			}
			off = next
		case 0x21: // Extension Introducer
			if off+2 > size {
				return out, nil
			}
			lbl, _ := d.Range(off+1, 1)
			start := off
			payload, next, ok := readGIFSubBlocks(d, off+2)
			if !ok {
				return out, nil
			}
			switch lbl[0] {
			case 0xfe:
				out = append(out, MetaEntry{
					Offset: start, Length: next - start,
					Container: "gif", Kind: "Comment",
					Value: safeText(payload),
				})
			case 0x01:
				// Plain Text: first 12 bytes are text-grid parameters.
				text := payload
				if len(text) > 12 {
					text = text[12:]
				} else {
					text = nil
				}
				out = append(out, MetaEntry{
					Offset: start, Length: next - start,
					Container: "gif", Kind: "PlainText",
					Value: safeText(text),
				})
			case 0xff:
				// Application Extension: first sub-block is an 11-byte
				// identifier like "NETSCAPE2.0" or "XMP DataXMP".
				if len(payload) >= 11 {
					appID := string(bytes.TrimRight(payload[:11], "\x00 "))
					rest := payload[11:]
					out = append(out, MetaEntry{
						Offset: start, Length: next - start,
						Container: "gif", Kind: "Application",
						Keyword: appID,
						Value:   safeText(rest),
					})
				} else {
					out = append(out, MetaEntry{
						Offset: start, Length: next - start,
						Container: "gif", Kind: "Application",
						Value: safeText(payload),
					})
				}
			default:
				// Graphic control (0xf9) and others — skip.
			}
			off = next
		default:
			// Unknown byte; bail.
			return out, nil
		}
	}
	return out, nil
}

// readGIFSubBlocks reads a chain of GIF sub-blocks: each starts with a
// length byte (0 terminates). Returns (concatenated payload, offset of
// next byte after terminator, ok).
func readGIFSubBlocks(d *document.Document, off int64) ([]byte, int64, bool) {
	size := d.Size()
	out := []byte{}
	for off < size {
		ln, err := d.Range(off, 1)
		if err != nil {
			return nil, off, false
		}
		n := int64(ln[0])
		off++
		if n == 0 {
			return out, off, true
		}
		if off+n > size {
			return nil, off, false
		}
		block, err := d.Range(off, n)
		if err != nil {
			return nil, off, false
		}
		out = append(out, block...)
		off += n
		// Hard cap to prevent OOM on malformed input.
		if len(out) > 16*1024*1024 {
			return out, off, true
		}
	}
	return nil, off, false
}

// skipGIFImage walks an Image Descriptor block starting at off (the 0x2c
// byte), skipping the descriptor, any local colour table, the LZW
// minimum-code-size byte and the image data sub-blocks. Returns the
// offset of the next block, or (0, false) on truncation.
func skipGIFImage(d *document.Document, off int64) (int64, bool) {
	size := d.Size()
	if off+10 > size {
		return 0, false
	}
	desc, err := d.Range(off, 10)
	if err != nil {
		return 0, false
	}
	packed := desc[9]
	off += 10
	if packed&0x80 != 0 {
		lctSize := int64(3) * (int64(1) << uint((packed&0x07)+1))
		off += lctSize
	}
	// LZW minimum code size byte, then sub-blocks.
	if off+1 > size {
		return 0, false
	}
	off++
	_, next, ok := readGIFSubBlocks(d, off)
	if !ok {
		return 0, false
	}
	return next, true
}
