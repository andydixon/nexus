package analysis

import (
	"context"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// StringHit represents one extracted string.
type StringHit struct {
	Offset   int64  `json:"offset"`
	Length   int    `json:"length"`
	Encoding string `json:"encoding"` // ascii | utf16le | utf16be
	Value    string `json:"value"`
}

// ExtractStrings finds printable runs in the specified range.
// Supports ASCII and UTF-16 LE/BE. minLen is in characters.
func ExtractStrings(d *document.Document, start, length int64, minLen int, encodings []string, max int) ([]map[string]any, error) {
	return ExtractStringsContext(context.Background(), d, start, length, minLen, encodings, max)
}

func ExtractStringsContext(ctx context.Context, d *document.Document, start, length int64, minLen int, encodings []string, max int) ([]map[string]any, error) {
	if minLen <= 0 {
		minLen = 4
	}
	if max <= 0 {
		max = 50_000
	}
	if length < 0 {
		length = d.Size() - start
	}

	doASCII, doLE, doBE := false, false, false
	if len(encodings) == 0 {
		doASCII, doLE, doBE = true, true, true
	} else {
		for _, e := range encodings {
			switch e {
			case "ascii":
				doASCII = true
			case "utf16le":
				doLE = true
			case "utf16be":
				doBE = true
			}
		}
	}

	out := make([]map[string]any, 0, 256)

	// ASCII
	if doASCII {
		if err := appendASCII(ctx, d, start, length, minLen, &out, max); err != nil {
			return nil, err
		}
	}
	// UTF-16
	if len(out) < max && doLE {
		if err := appendUTF16(ctx, d, start, length, minLen, true, &out, max); err != nil {
			return nil, err
		}
	}
	if len(out) < max && doBE {
		if err := appendUTF16(ctx, d, start, length, minLen, false, &out, max); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func appendASCII(ctx context.Context, d *document.Document, start, length int64, minLen int, out *[]map[string]any, max int) error {
	// Chunked scan with a carry for runs crossing chunk boundaries.
	var runStart int64 = -1
	var runBuf []byte
	emit := func() {
		if runStart >= 0 && len(runBuf) >= minLen {
			*out = append(*out, map[string]any{
				"offset":   runStart,
				"length":   len(runBuf),
				"encoding": "ascii",
				"value":    string(runBuf),
			})
		}
		runStart = -1
		runBuf = runBuf[:0]
	}

	if err := d.ForEachChunk(start, length, func(off int64, chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i, b := range chunk {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if isPrintableASCII(b) {
				if runStart < 0 {
					runStart = off + int64(i)
					runBuf = make([]byte, 0, 32)
				}
				runBuf = append(runBuf, b)
			} else {
				if len(*out) >= max {
					return nil
				}
				emit()
			}
		}
		return nil
	}); err != nil {
		return err
	}
	emit()
	return nil
}

func appendUTF16(ctx context.Context, d *document.Document, start, length int64, minLen int, le bool, out *[]map[string]any, max int) error {
	// Step by 2. Produce one string for each contiguous run of printable code units.
	if length < 2 {
		return nil
	}
	// Read chunks aligned to 2 bytes so pairs don't split.
	const chunkSize = 1 << 20
	end := start + length
	var runStart int64 = -1
	var runBuf []rune

	enc := "utf16le"
	if !le {
		enc = "utf16be"
	}

	emit := func() {
		if runStart >= 0 && len(runBuf) >= minLen {
			*out = append(*out, map[string]any{
				"offset":   runStart,
				"length":   len(runBuf) * 2,
				"encoding": enc,
				"value":    string(runBuf),
			})
		}
		runStart = -1
		runBuf = runBuf[:0]
	}

	for off := start; off+1 < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		winEnd := off + chunkSize
		if winEnd > end {
			winEnd = end
		}
		if (winEnd-off)%2 != 0 {
			winEnd-- // keep pair alignment
		}
		buf, err := d.Range(off, winEnd-off)
		if err != nil {
			return err
		}
		for i := 0; i+1 < len(buf); i += 2 {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			var cu uint16
			if le {
				cu = uint16(buf[i]) | uint16(buf[i+1])<<8
			} else {
				cu = uint16(buf[i])<<8 | uint16(buf[i+1])
			}
			if cu >= 0x20 && cu <= 0x7e {
				if runStart < 0 {
					runStart = off + int64(i)
					runBuf = make([]rune, 0, 32)
				}
				runBuf = append(runBuf, rune(cu))
			} else {
				if len(*out) >= max {
					emit()
					return nil
				}
				emit()
			}
		}
		off = winEnd
	}
	emit()
	return nil
}

func isPrintableASCII(b byte) bool {
	return b == '\t' || (b >= 0x20 && b <= 0x7e)
}
