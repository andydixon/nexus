package analysis

import (
	"bytes"
	"context"
	"errors"
	"regexp"

	"github.com/andydixon/link/internal/leviathan/document"
)

// SearchHit describes one search result.
type SearchHit struct {
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
	Sample string `json:"sample"` // short hex preview
}

// SearchBytes finds `needle` in the document range and returns hit
// offsets. Scans chunk by chunk with an overlap equal to len(needle)-1
// so cross-chunk matches are not missed.
func SearchBytes(d *document.Document, start, length int64, needle []byte, max int) ([]SearchHit, error) {
	return SearchBytesContext(context.Background(), d, start, length, needle, max)
}

func SearchBytesContext(ctx context.Context, d *document.Document, start, length int64, needle []byte, max int) ([]SearchHit, error) {
	if len(needle) == 0 {
		return nil, errors.New("empty needle")
	}
	if max <= 0 {
		max = 10_000
	}
	if length < 0 {
		length = d.Size() - start
	}

	hits := make([]SearchHit, 0, 64)
	overlap := int64(len(needle) - 1)

	// We chunk in windows of chunkSize + overlap.
	const chunkSize = 1 << 20 // 1MB
	off := start
	endExclusive := start + length

	for off < endExclusive && len(hits) < max {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		winEnd := off + chunkSize
		if winEnd > endExclusive {
			winEnd = endExclusive
		}
		readEnd := winEnd + overlap
		if readEnd > endExclusive {
			readEnd = endExclusive
		}
		buf, err := d.Range(off, readEnd-off)
		if err != nil {
			return nil, err
		}
		// Limit search to bytes whose start lies in [0, winEnd-off)
		searchLimit := winEnd - off
		scan := 0
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			idx := bytes.Index(buf[scan:], needle)
			if idx < 0 {
				break
			}
			abs := int64(scan+idx) + off
			relStart := abs - off
			if relStart >= searchLimit {
				break
			}
			hits = append(hits, SearchHit{
				Offset: abs,
				Length: len(needle),
				Sample: hexPreview(buf[scan+idx:], len(needle)),
			})
			if len(hits) >= max {
				break
			}
			scan = scan + idx + 1
			if scan >= len(buf) {
				break
			}
		}
		off = winEnd
	}
	return hits, nil
}

// SearchRegex runs a Go regexp across the document range, matching
// byte-wise. It decodes matches chunk by chunk with overlap, bounded
// by a generous max hit count.
func SearchRegex(d *document.Document, start, length int64, pattern string, max int) ([]SearchHit, error) {
	return SearchRegexContext(context.Background(), d, start, length, pattern, max)
}

func SearchRegexContext(ctx context.Context, d *document.Document, start, length int64, pattern string, max int) ([]SearchHit, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		max = 10_000
	}
	if length < 0 {
		length = d.Size() - start
	}

	// For regex, a conservative overlap is 4 KiB so matches spanning
	// chunk boundaries are still caught. Very long matches may still
	// be missed in extreme cases.
	const chunkSize = 1 << 20
	const overlap = 4096

	hits := make([]SearchHit, 0, 64)
	off := start
	endExclusive := start + length

	for off < endExclusive && len(hits) < max {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		winEnd := off + chunkSize
		if winEnd > endExclusive {
			winEnd = endExclusive
		}
		readEnd := winEnd + overlap
		if readEnd > endExclusive {
			readEnd = endExclusive
		}
		buf, err := d.Range(off, readEnd-off)
		if err != nil {
			return nil, err
		}
		searchLimit := winEnd - off
		locs := re.FindAllIndex(buf, max-len(hits))
		for _, loc := range locs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			relStart := int64(loc[0])
			if relStart >= searchLimit {
				continue
			}
			abs := relStart + off
			hits = append(hits, SearchHit{
				Offset: abs,
				Length: loc[1] - loc[0],
				Sample: hexPreview(buf[loc[0]:loc[1]], loc[1]-loc[0]),
			})
		}
		off = winEnd
	}
	return hits, nil
}

func hexPreview(b []byte, n int) string {
	if n > 32 {
		n = 32
	}
	if n > len(b) {
		n = len(b)
	}
	const hexchars = "0123456789abcdef"
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		out[i*2] = hexchars[b[i]>>4]
		out[i*2+1] = hexchars[b[i]&0x0f]
	}
	return string(out)
}

// HitsToMaps converts hits for bridge return.
func HitsToMaps(hits []SearchHit) []map[string]any {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = map[string]any{
			"offset": h.Offset,
			"length": h.Length,
			"sample": h.Sample,
		}
	}
	return out
}
