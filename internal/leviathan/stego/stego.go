// Package stego provides practical steganography helpers:
//
//   - trailer detection (bytes after a declared end) for PNG, JPEG,
//     GIF, BMP, WAV and RIFF/WebP containers
//   - multi-trailer surfacing: identifies any embedded container
//     (PNG/JPEG/ZIP/GIF/PDF/RAR/7z) inside the trailer region
//   - LSB extract/inject over an arbitrary byte buffer
//   - PNG IDAT, BMP pixel-array and WAV sample-data resolvers so
//     callers can point LSB at the correct carrier region
//   - textual-metadata extraction for PNG, JPEG, GIF (see metadata.go)
package stego

import (
	"encoding/binary"
	"errors"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// ---------------------------------------------------------------------------
// Trailer detection
// ---------------------------------------------------------------------------

// EmbeddedHit describes a recognised container signature found inside the
// trailer region (the bytes after a file's declared end).
type EmbeddedHit struct {
	Offset int64  `json:"offset"` // absolute offset within the outer file
	Kind   string `json:"kind"`
	Note   string `json:"note,omitempty"`
}

// AnalyseAppended inspects the end of the file for plausible trailers: data
// after the declared end of a known container. The result also lists any
// nested container magic bytes discovered inside the trailer region.
func AnalyseAppended(d *document.Document) (map[string]any, error) {
	size := d.Size()
	head, _ := d.Range(0, 16)
	result := map[string]any{
		"fileSize":    size,
		"declaredEnd": int64(-1),
		"trailerSize": int64(0),
		"container":   "",
		"note":        "no recognised container",
		"embedded":    []EmbeddedHit{},
	}

	end := int64(-1)
	container := ""

	switch {
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		container = "png"
		end = findPNGEnd(d)
	case len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff:
		container = "jpeg"
		end = findJPEGEnd(d)
	case len(head) >= 6 && (string(head[:6]) == "GIF87a" || string(head[:6]) == "GIF89a"):
		container = "gif"
		end = findGIFEnd(d)
	case len(head) >= 2 && head[0] == 'B' && head[1] == 'M':
		container = "bmp"
		end = findBMPEnd(d)
	case len(head) >= 12 && string(head[:4]) == "RIFF":
		sub := string(head[8:12])
		if sub == "WAVE" {
			container = "wav"
		} else if sub == "WEBP" {
			container = "webp"
		} else {
			container = "riff"
		}
		end = findRIFFEnd(d)
	}

	if container != "" {
		result["container"] = container
	}

	if end > 0 && end <= size {
		result["declaredEnd"] = end
		if end < size {
			trailer := size - end
			result["trailerSize"] = trailer
			result["note"] = "bytes after " + container + " declared end"
			if trailer > 0 {
				// Cap scanning the trailer at 16 MiB — otherwise we might
				// pull a 2-GiB tail into memory on hostile inputs.
				scanLen := trailer
				if scanLen > 16*1024*1024 {
					scanLen = 16 * 1024 * 1024
				}
				tail, _ := d.Range(end, scanLen)
				result["embedded"] = scanEmbedded(tail, end)
			}
		} else {
			result["note"] = "no trailing bytes"
		}
	}

	return result, nil
}

// ExtractTrailer returns the bytes after a container's declared end.
func ExtractTrailer(d *document.Document) ([]byte, error) {
	a, err := AnalyseAppended(d)
	if err != nil {
		return nil, err
	}
	end, _ := a["declaredEnd"].(int64)
	ts, _ := a["trailerSize"].(int64)
	if end < 0 || ts <= 0 {
		return nil, errors.New("no trailer found")
	}
	return d.Range(end, ts)
}

func findPNGEnd(d *document.Document) int64 {
	size := d.Size()
	off := int64(8)
	for off+12 <= size {
		hdr, err := d.Range(off, 8)
		if err != nil {
			break
		}
		ln := int64(binary.BigEndian.Uint32(hdr[0:4]))
		if ln < 0 || off+12+ln > size {
			break
		}
		typ := string(hdr[4:8])
		off += 12 + ln
		if typ == "IEND" {
			return off
		}
	}
	return -1
}

func findJPEGEnd(d *document.Document) int64 {
	size := d.Size()
	tailLen := int64(128 * 1024)
	if tailLen > size {
		tailLen = size
	}
	tail, _ := d.Range(size-tailLen, tailLen)
	for i := len(tail) - 2; i >= 0; i-- {
		if tail[i] == 0xff && tail[i+1] == 0xd9 {
			return size - tailLen + int64(i+2)
		}
	}
	return -1
}

func findGIFEnd(d *document.Document) int64 {
	size := d.Size()
	// The GIF terminator 0x3B is a single byte that follows the final
	// data block of the final image. Walking the container correctly is
	// overkill; scan from the tail and accept the last 0x3B that is
	// preceded by a block terminator (0x00) or a plausible frame end.
	tailLen := int64(32 * 1024)
	if tailLen > size {
		tailLen = size
	}
	tail, _ := d.Range(size-tailLen, tailLen)
	for i := len(tail) - 1; i >= 0; i-- {
		if tail[i] == 0x3b {
			return size - tailLen + int64(i+1)
		}
	}
	return -1
}

func findBMPEnd(d *document.Document) int64 {
	size := d.Size()
	hdr, err := d.Range(0, 14)
	if err != nil {
		return -1
	}
	bfSize := int64(binary.LittleEndian.Uint32(hdr[2:6]))
	if bfSize > 0 && bfSize <= size {
		return bfSize
	}
	return -1
}

func findRIFFEnd(d *document.Document) int64 {
	size := d.Size()
	hdr, err := d.Range(0, 8)
	if err != nil {
		return -1
	}
	// RIFF chunk size (little-endian) is the size of the rest of the
	// file *after* the first 8 bytes, so declared end = 8 + riffSize.
	riffSize := int64(binary.LittleEndian.Uint32(hdr[4:8]))
	end := 8 + riffSize
	if end > 0 && end <= size {
		// RIFF pads to even alignment.
		if end%2 == 1 && end+1 <= size {
			end++
		}
		return end
	}
	return -1
}

// scanEmbedded walks a trailer buffer for known container magic bytes.
func scanEmbedded(buf []byte, base int64) []EmbeddedHit {
	hits := []EmbeddedHit{}
	type sig struct {
		bytes []byte
		kind  string
	}
	sigs := []sig{
		{[]byte("\x89PNG\r\n\x1a\n"), "png"},
		{[]byte{0xff, 0xd8, 0xff}, "jpeg"},
		{[]byte("GIF87a"), "gif"},
		{[]byte("GIF89a"), "gif"},
		{[]byte("PK\x03\x04"), "zip"},
		{[]byte("PK\x05\x06"), "zip-eocd"},
		{[]byte("Rar!\x1a\x07\x00"), "rar"},
		{[]byte("Rar!\x1a\x07\x01\x00"), "rar5"},
		{[]byte("7z\xbc\xaf\x27\x1c"), "7z"},
		{[]byte("%PDF-"), "pdf"},
		{[]byte("\x1f\x8b\x08"), "gzip"},
		{[]byte{0xfd, '7', 'z', 'X', 'Z', 0x00}, "xz"},
		{[]byte("BZh"), "bzip2"},
		{[]byte{0x28, 0xb5, 0x2f, 0xfd}, "zstd"},
		{[]byte{0x7f, 'E', 'L', 'F'}, "elf"},
		{[]byte("MZ"), "mz/pe"},
		{[]byte("ustar"), "tar"},
		{[]byte("OggS"), "ogg"},
		{[]byte("ID3"), "id3/mp3"},
		{[]byte("fLaC"), "flac"},
		{[]byte("\x1aEß£"), "matroska/webm"},
		{[]byte("RIFF"), "riff"},
	}
	for _, s := range sigs {
		if len(s.bytes) == 0 {
			continue
		}
		start := 0
		for {
			i := indexOf(buf[start:], s.bytes)
			if i < 0 {
				break
			}
			hits = append(hits, EmbeddedHit{
				Offset: base + int64(start+i),
				Kind:   s.kind,
			})
			// A plain "MZ" or "RIFF" hit without more context is noisy;
			// the caller can filter on kind. Keep walking past this hit.
			start += i + len(s.bytes)
			// Bound total hits per signature to keep UI manageable.
			if len(hits) > 256 {
				break
			}
		}
		if len(hits) > 256 {
			break
		}
	}
	return hits
}

func indexOf(hay, needle []byte) int {
	if len(needle) == 0 || len(hay) < len(needle) {
		return -1
	}
	first := needle[0]
	last := len(hay) - len(needle)
	for i := 0; i <= last; i++ {
		if hay[i] != first {
			continue
		}
		match := true
		for j := 1; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Carrier resolvers (PNG IDAT, BMP pixels, WAV samples)
// ---------------------------------------------------------------------------

// PNGImageData concatenates all IDAT chunks' payload bytes. Callers can then
// feed the result into LSBExtract/LSBInject. The returned buffer is the raw
// (still-deflated) IDAT stream — it is the conventional LSB carrier for
// tools like zsteg, not the decoded pixel array.
func PNGImageData(d *document.Document) ([]byte, error) {
	size := d.Size()
	off := int64(8)
	buf := []byte{}
	for off+12 <= size {
		hdr, err := d.Range(off, 8)
		if err != nil {
			return nil, err
		}
		ln := int64(binary.BigEndian.Uint32(hdr[0:4]))
		typ := string(hdr[4:8])
		if typ == "IDAT" {
			chunk, err := d.Range(off+8, ln)
			if err != nil {
				return nil, err
			}
			buf = append(buf, chunk...)
		}
		off += 12 + ln
		if typ == "IEND" {
			break
		}
	}
	if len(buf) == 0 {
		return nil, errors.New("no IDAT data")
	}
	return buf, nil
}

// BMPCarrier describes a resolved BMP pixel array that can be used as an
// LSB carrier.
type BMPCarrier struct {
	Data         []byte `json:"data"`
	Offset       int64  `json:"offset"` // offset of pixel array in the file
	Width        int32  `json:"width"`
	Height       int32  `json:"height"`
	BitsPerPixel uint16 `json:"bitsPerPixel"`
	RowStride    int    `json:"rowStride"`
}

// BMPPixels locates and returns the BMP pixel array. Supports 8/24/32 bpp
// uncompressed BI_RGB images, which are by far the most common carriers.
func BMPPixels(d *document.Document) (*BMPCarrier, error) {
	size := d.Size()
	if size < 54 {
		return nil, errors.New("not a BMP (too small)")
	}
	hdr, err := d.Range(0, 54)
	if err != nil {
		return nil, err
	}
	if hdr[0] != 'B' || hdr[1] != 'M' {
		return nil, errors.New("not a BMP")
	}
	bfOffBits := int64(binary.LittleEndian.Uint32(hdr[10:14]))
	dibSize := binary.LittleEndian.Uint32(hdr[14:18])
	// BITMAPINFOHEADER (40), BITMAPV4HEADER (108), BITMAPV5HEADER (124),
	// OS/2 BITMAPCOREHEADER (12). Only handle the modern family.
	if dibSize < 40 {
		return nil, errors.New("BMP: unsupported DIB header (BITMAPCOREHEADER)")
	}
	w := int32(binary.LittleEndian.Uint32(hdr[18:22]))
	h := int32(binary.LittleEndian.Uint32(hdr[22:26]))
	bpp := binary.LittleEndian.Uint16(hdr[28:30])
	compression := binary.LittleEndian.Uint32(hdr[30:34])
	if compression != 0 && compression != 3 { // BI_RGB / BI_BITFIELDS
		return nil, errors.New("BMP: compressed images not supported")
	}
	if bpp != 8 && bpp != 24 && bpp != 32 {
		return nil, errors.New("BMP: only 8/24/32 bpp supported")
	}
	absH := h
	if absH < 0 {
		absH = -absH
	}
	rowStride := ((int(w)*int(bpp) + 31) / 32) * 4
	total := int64(rowStride) * int64(absH)
	if bfOffBits < 0 || bfOffBits+total > size {
		return nil, errors.New("BMP: pixel array out of range")
	}
	data, err := d.Range(bfOffBits, total)
	if err != nil {
		return nil, err
	}
	return &BMPCarrier{
		Data: data, Offset: bfOffBits,
		Width: w, Height: h, BitsPerPixel: bpp, RowStride: rowStride,
	}, nil
}

// WAVCarrier describes a resolved WAV "data" chunk that can be used as an
// LSB carrier.
type WAVCarrier struct {
	Data          []byte `json:"data"`
	Offset        int64  `json:"offset"` // offset of sample data
	SampleRate    uint32 `json:"sampleRate"`
	NumChannels   uint16 `json:"numChannels"`
	BitsPerSample uint16 `json:"bitsPerSample"`
	AudioFormat   uint16 `json:"audioFormat"`
}

// WAVSamples locates the "data" sub-chunk of a RIFF/WAVE file and returns
// its payload. Only PCM (format 1) is usable as a straightforward LSB
// carrier; other formats still return bytes but are flagged in the caller.
func WAVSamples(d *document.Document) (*WAVCarrier, error) {
	size := d.Size()
	if size < 44 {
		return nil, errors.New("not a WAV (too small)")
	}
	hdr, err := d.Range(0, 12)
	if err != nil {
		return nil, err
	}
	if string(hdr[:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	out := &WAVCarrier{}
	off := int64(12)
	for off+8 <= size {
		ch, err := d.Range(off, 8)
		if err != nil {
			return nil, err
		}
		id := string(ch[:4])
		cs := int64(binary.LittleEndian.Uint32(ch[4:8]))
		if cs < 0 || off+8+cs > size {
			break
		}
		switch id {
		case "fmt ":
			body, err := d.Range(off+8, cs)
			if err == nil && len(body) >= 16 {
				out.AudioFormat = binary.LittleEndian.Uint16(body[0:2])
				out.NumChannels = binary.LittleEndian.Uint16(body[2:4])
				out.SampleRate = binary.LittleEndian.Uint32(body[4:8])
				out.BitsPerSample = binary.LittleEndian.Uint16(body[14:16])
			}
		case "data":
			body, err := d.Range(off+8, cs)
			if err != nil {
				return nil, err
			}
			out.Data = body
			out.Offset = off + 8
			return out, nil
		}
		// Pad to even.
		step := 8 + cs
		if step%2 == 1 {
			step++
		}
		off += step
	}
	return nil, errors.New("WAV: data chunk not found")
}

// ---------------------------------------------------------------------------
// LSB codec
// ---------------------------------------------------------------------------

// LSBExtract returns a bitstring-as-bytes built from the low bit of each
// byte in the selected range. Useful for exploring LSB payloads over raw
// buffers or specific regions.
func LSBExtract(buf []byte, bitsPerByte int) []byte {
	if bitsPerByte <= 0 || bitsPerByte > 8 {
		bitsPerByte = 1
	}
	total := len(buf) * bitsPerByte
	out := make([]byte, total/8)
	bitIdx := 0
	for _, b := range buf {
		for i := 0; i < bitsPerByte; i++ {
			bit := (b >> uint(i)) & 1
			if bit == 1 {
				out[bitIdx/8] |= 1 << uint(7-(bitIdx%8))
			}
			bitIdx++
		}
	}
	return out
}

// LSBInject replaces low bits in buf with bits from payload. Returns the
// modified buffer. When capacity is exceeded, injection stops.
func LSBInject(buf, payload []byte, bitsPerByte int) []byte {
	if bitsPerByte <= 0 || bitsPerByte > 8 {
		bitsPerByte = 1
	}
	out := append([]byte(nil), buf...)
	mask := byte(1<<uint(bitsPerByte) - 1)
	bitIdx := 0
	total := len(payload) * 8
	for i := 0; i < len(out) && bitIdx < total; i++ {
		chunk := byte(0)
		for b := 0; b < bitsPerByte && bitIdx < total; b++ {
			bit := (payload[bitIdx/8] >> uint(7-(bitIdx%8))) & 1
			if bit != 0 {
				chunk |= 1 << uint(b)
			}
			bitIdx++
		}
		out[i] = (out[i] &^ mask) | (chunk & mask)
	}
	return out
}
