package analysis

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/andydixon/link/internal/leviathan/document"
)

// ScanTimestamps walks the range looking for plausible timestamps in
// common encodings. This is a heuristic: it reports any 4/8-byte
// window whose decoded time falls in [1990-01-01, 2050-12-31].
func ScanTimestamps(d *document.Document, start, length int64, maxHits int) (map[string]any, error) {
	if length < 0 {
		length = d.Size() - start
	}
	if maxHits <= 0 {
		maxHits = 20_000
	}

	minT := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	maxT := time.Date(2050, 12, 31, 0, 0, 0, 0, time.UTC).Unix()
	ok := func(sec int64) bool { return sec >= minT && sec <= maxT }

	hits := make([]map[string]any, 0, 256)
	counts := map[string]int{}

	const chunk = 1 << 20
	end := start + length
	overlap := int64(7) // enough for 8-byte types
	for off := start; off < end && len(hits) < maxHits; {
		winEnd := off + chunk
		if winEnd > end {
			winEnd = end
		}
		readEnd := winEnd + overlap
		if readEnd > end {
			readEnd = end
		}
		buf, err := d.Range(off, readEnd-off)
		if err != nil {
			return nil, err
		}
		limit := int(winEnd - off)
		for i := 0; i < limit && len(hits) < maxHits; i += 4 {
			if i+4 <= len(buf) {
				vLE := int64(int32(binary.LittleEndian.Uint32(buf[i:])))
				if ok(vLE) {
					hits = append(hits, hit("unix_le", off+int64(i), 4, time.Unix(vLE, 0).UTC()))
					counts["unix_le"]++
					continue
				}
				vBE := int64(int32(binary.BigEndian.Uint32(buf[i:])))
				if ok(vBE) {
					hits = append(hits, hit("unix_be", off+int64(i), 4, time.Unix(vBE, 0).UTC()))
					counts["unix_be"]++
					continue
				}
			}
			if i+8 <= len(buf) {
				v := binary.LittleEndian.Uint64(buf[i:])
				// Windows FILETIME
				sec := int64(v/10_000_000) - filetimeEpochDelta
				if ok(sec) {
					hits = append(hits, hit("filetime_le", off+int64(i), 8, time.Unix(sec, 0).UTC()))
					counts["filetime_le"]++
					continue
				}
				// WebKit (microseconds since 1601)
				sec = int64(v/1_000_000) - filetimeEpochDelta
				if ok(sec) {
					hits = append(hits, hit("webkit_le", off+int64(i), 8, time.Unix(sec, 0).UTC()))
					counts["webkit_le"]++
					continue
				}
				// OLE automation date
				fdays := math.Float64frombits(v)
				if fdays > 2 && fdays < 75000 {
					base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
					t := base.Add(time.Duration(fdays*86400) * time.Second)
					if t.Unix() >= minT && t.Unix() <= maxT {
						hits = append(hits, hit("ole_le", off+int64(i), 8, t))
						counts["ole_le"]++
						continue
					}
				}
			}
		}
		off = winEnd
	}
	return map[string]any{
		"hits":   hits,
		"counts": counts,
	}, nil
}

func hit(kind string, off int64, size int, t time.Time) map[string]any {
	return map[string]any{
		"kind":   kind,
		"offset": off,
		"length": size,
		"iso":    t.Format(time.RFC3339),
		"unix":   t.Unix(),
	}
}
