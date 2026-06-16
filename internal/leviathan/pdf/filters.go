package pdf

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/andydixon/nexus/internal/leviathan/transforms"
)

const MaxDecodedStreamOutput = 64 << 20

// ApplyFilter decodes one PDF stream filter. When a predictor is in use
// it must be applied separately after this function returns raw filter
// output; see ApplyPredictor.
func ApplyFilter(name string, in []byte) ([]byte, error) {
	switch name {
	case "FlateDecode", "Fl":
		if out, err := transforms.InflateZlib(in); err == nil {
			return out, nil
		}
		return transforms.InflateRaw(in)
	case "ASCIIHexDecode", "AHx":
		return decodeASCIIHex(in)
	case "ASCII85Decode", "A85":
		return decodeASCII85(in)
	case "RunLengthDecode", "RL":
		return decodeRunLength(in)
	case "LZWDecode", "LZW":
		return decodeLZW(in)
	case "CCITTFaxDecode", "CCF":
		return nil, errors.New("CCITTFaxDecode not supported (returns raw bytes)")
	case "JBIG2Decode":
		return nil, errors.New("JBIG2Decode not supported")
	case "DCTDecode", "DCT":
		// JPEG payload — return as-is, caller can save as .jpg.
		return in, nil
	case "JPXDecode":
		return in, nil
	case "Crypt":
		return nil, errors.New("Crypt filter not supported")
	}
	return nil, fmt.Errorf("unknown filter %q", name)
}

// ApplyPredictor undoes the PNG-family predictor used with Flate/LZW. If
// predictor <= 1 the data is returned unchanged.
func ApplyPredictor(in []byte, predictor, columns, colors, bits int) ([]byte, error) {
	if predictor <= 1 {
		return in, nil
	}
	if len(in) > MaxDecodedStreamOutput {
		return nil, fmt.Errorf("predictor input exceeds %d byte limit", MaxDecodedStreamOutput)
	}
	bytesPerPixel, rowLen, err := predictorGeometry(columns, colors, bits)
	if err != nil {
		return nil, err
	}

	switch {
	case predictor == 2:
		// TIFF predictor — apply left delta per sample.
		out := make([]byte, len(in))
		copy(out, in)
		step := bytesPerPixel
		for i := step; i < len(out); i++ {
			if i%rowLen < step {
				continue
			}
			out[i] = out[i] + out[i-step]
		}
		return out, nil
	case predictor >= 10 && predictor <= 15:
		// PNG predictors — each row is prefixed with a filter-type byte.
		stride := rowLen + 1
		out := make([]byte, 0, len(in))
		var prev []byte
		for i := 0; i < len(in); i += stride {
			if i+stride > len(in) {
				break
			}
			filterType := in[i]
			row := make([]byte, rowLen)
			copy(row, in[i+1:i+stride])
			switch filterType {
			case 0: // None
			case 1: // Sub
				for j := bytesPerPixel; j < rowLen; j++ {
					row[j] += row[j-bytesPerPixel]
				}
			case 2: // Up
				if prev != nil {
					for j := 0; j < rowLen; j++ {
						row[j] += prev[j]
					}
				}
			case 3: // Average
				for j := 0; j < rowLen; j++ {
					left := byte(0)
					if j >= bytesPerPixel {
						left = row[j-bytesPerPixel]
					}
					up := byte(0)
					if prev != nil {
						up = prev[j]
					}
					row[j] += byte((int(left) + int(up)) / 2)
				}
			case 4: // Paeth
				for j := 0; j < rowLen; j++ {
					a := 0
					if j >= bytesPerPixel {
						a = int(row[j-bytesPerPixel])
					}
					b := 0
					if prev != nil {
						b = int(prev[j])
					}
					c := 0
					if prev != nil && j >= bytesPerPixel {
						c = int(prev[j-bytesPerPixel])
					}
					p := a + b - c
					pa := abs(p - a)
					pb := abs(p - b)
					pc := abs(p - c)
					pred := 0
					switch {
					case pa <= pb && pa <= pc:
						pred = a
					case pb <= pc:
						pred = b
					default:
						pred = c
					}
					row[j] += byte(pred)
				}
			default:
				return nil, fmt.Errorf("PNG predictor: invalid filter type %d", filterType)
			}
			out = append(out, row...)
			prev = row
		}
		return out, nil
	}
	return nil, fmt.Errorf("predictor %d not supported", predictor)
}

func predictorGeometry(columns, colors, bits int) (int, int, error) {
	if columns <= 0 || colors <= 0 {
		return 0, 0, errors.New("predictor columns and colors must be positive")
	}
	switch bits {
	case 1, 2, 4, 8, 16:
	default:
		return 0, 0, fmt.Errorf("predictor bits per component %d is invalid", bits)
	}
	pixelBits := int64(colors) * int64(bits)
	rowBits := int64(columns) * pixelBits
	if pixelBits <= 0 || rowBits <= 0 || rowBits > int64(MaxDecodedStreamOutput)*8 {
		return 0, 0, errors.New("predictor dimensions exceed resource limits")
	}
	return int((pixelBits + 7) / 8), int((rowBits + 7) / 8), nil
}

func ensureDecodedSize(current, additional int) error {
	if additional < 0 || current > MaxDecodedStreamOutput-additional {
		return fmt.Errorf("decoded stream exceeds %d byte limit", MaxDecodedStreamOutput)
	}
	return nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---------------------------------------------------------------------------
// ASCIIHexDecode
// ---------------------------------------------------------------------------

func decodeASCIIHex(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in)/2)
	hi := -1
	for _, b := range in {
		switch {
		case b >= '0' && b <= '9':
			b = b - '0'
		case b >= 'a' && b <= 'f':
			b = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			b = b - 'A' + 10
		case b == '>':
			if hi >= 0 {
				out = append(out, byte(hi)<<4)
			}
			return out, nil
		case isPDFWhitespace(b):
			continue
		default:
			continue
		}
		if hi < 0 {
			hi = int(b)
		} else {
			out = append(out, byte(hi)<<4|b)
			hi = -1
		}
	}
	if hi >= 0 {
		out = append(out, byte(hi)<<4)
	}
	return out, nil
}

func isPDFWhitespace(b byte) bool {
	return b == 0 || b == '\t' || b == '\n' || b == '\f' || b == '\r' || b == ' '
}

// ---------------------------------------------------------------------------
// ASCII85Decode
// ---------------------------------------------------------------------------

func decodeASCII85(in []byte) ([]byte, error) {
	// Strip optional <~ ... ~> framing and whitespace.
	start := 0
	if bytes.HasPrefix(in, []byte("<~")) {
		start = 2
	}
	out := make([]byte, 0, len(in))
	var group [5]byte
	n := 0
	for i := start; i < len(in); i++ {
		c := in[i]
		if isPDFWhitespace(c) {
			continue
		}
		if c == '~' {
			break
		}
		if c == 'z' && n == 0 {
			if err := ensureDecodedSize(len(out), 4); err != nil {
				return nil, err
			}
			out = append(out, 0, 0, 0, 0)
			continue
		}
		if c < '!' || c > 'u' {
			return nil, fmt.Errorf("ASCII85: invalid byte 0x%02x", c)
		}
		group[n] = c - '!'
		n++
		if n == 5 {
			v := uint32(group[0])*85*85*85*85 +
				uint32(group[1])*85*85*85 +
				uint32(group[2])*85*85 +
				uint32(group[3])*85 +
				uint32(group[4])
			if err := ensureDecodedSize(len(out), 4); err != nil {
				return nil, err
			}
			out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
			n = 0
		}
	}
	if n > 0 {
		for i := n; i < 5; i++ {
			group[i] = 84
		}
		v := uint32(group[0])*85*85*85*85 +
			uint32(group[1])*85*85*85 +
			uint32(group[2])*85*85 +
			uint32(group[3])*85 +
			uint32(group[4])
		full := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		if err := ensureDecodedSize(len(out), n-1); err != nil {
			return nil, err
		}
		out = append(out, full[:n-1]...)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// RunLengthDecode
// ---------------------------------------------------------------------------

func decodeRunLength(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	i := 0
	for i < len(in) {
		b := in[i]
		i++
		switch {
		case b == 128:
			return out, nil
		case b < 128:
			n := int(b) + 1
			if i+n > len(in) {
				return nil, errors.New("RunLength: truncated literal")
			}
			if err := ensureDecodedSize(len(out), n); err != nil {
				return nil, err
			}
			out = append(out, in[i:i+n]...)
			i += n
		default:
			n := 257 - int(b)
			if i >= len(in) {
				return nil, errors.New("RunLength: truncated run")
			}
			if err := ensureDecodedSize(len(out), n); err != nil {
				return nil, err
			}
			for k := 0; k < n; k++ {
				out = append(out, in[i])
			}
			i++
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// LZWDecode (variable-width, early-change = 1)
// ---------------------------------------------------------------------------

func decodeLZW(in []byte) ([]byte, error) {
	const (
		clearCode = 256
		eodCode   = 257
	)
	reader := &bitReader{data: in}
	table := make([][]byte, 258)
	for i := 0; i < 256; i++ {
		table[i] = []byte{byte(i)}
	}
	codeWidth := 9
	out := []byte{}
	prev := []byte(nil)
	for {
		c, err := reader.read(codeWidth)
		if err != nil {
			return out, nil
		}
		if c == eodCode {
			return out, nil
		}
		if c == clearCode {
			table = table[:258]
			codeWidth = 9
			prev = nil
			continue
		}
		var entry []byte
		switch {
		case c < len(table):
			entry = table[c]
		case c == len(table) && prev != nil:
			entry = append([]byte{}, prev...)
			entry = append(entry, prev[0])
		default:
			return out, errors.New("LZW: invalid code")
		}
		if err := ensureDecodedSize(len(out), len(entry)); err != nil {
			return nil, err
		}
		out = append(out, entry...)
		if prev != nil {
			ne := append([]byte{}, prev...)
			ne = append(ne, entry[0])
			table = append(table, ne)
			// PDF LZW uses "early change": widen one code before full.
			switch len(table) {
			case 511, 1023, 2047:
				codeWidth++
			case 4096:
				// Implicit CLEAR expected; some encoders omit it.
				table = table[:258]
				codeWidth = 9
				prev = nil
				continue
			}
		}
		prev = entry
	}
}

type bitReader struct {
	data []byte
	bi   int // bit offset into data
}

func (r *bitReader) read(n int) (int, error) {
	if n <= 0 || n > 32 {
		return 0, errors.New("bitReader: bad width")
	}
	v := 0
	for i := 0; i < n; i++ {
		byteIdx := r.bi / 8
		if byteIdx >= len(r.data) {
			return 0, errors.New("bitReader: EOF")
		}
		bit := (r.data[byteIdx] >> uint(7-(r.bi%8))) & 1
		v = (v << 1) | int(bit)
		r.bi++
	}
	return v, nil
}
