package analysis

import (
	"context"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// DiffRegion describes a contiguous run of differing bytes.
type DiffRegion struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
	// kinds: "edit" (both sides have bytes), "add" (only B), "remove" (only A)
	Kind string `json:"kind"`
}

type DiffBlock struct {
	Offset       int64   `json:"offset"`
	Length       int64   `json:"length"`
	ChangedBytes int64   `json:"changedBytes"`
	EditBytes    int64   `json:"editBytes"`
	AddBytes     int64   `json:"addBytes"`
	RemoveBytes  int64   `json:"removeBytes"`
	Similarity   float64 `json:"similarity"`
}

// Diff compares two documents by byte. It handles documents of
// different sizes by treating the tail of the longer as add/remove.
// Chunks at both sides. Returns a compact list of regions.
func Diff(a, b *document.Document, start, length int64) (map[string]any, error) {
	return DiffContext(context.Background(), a, b, start, length)
}

func DiffContext(ctx context.Context, a, b *document.Document, start, length int64) (map[string]any, error) {
	aSize := a.Size()
	bSize := b.Size()
	maxSize := aSize
	if bSize > maxSize {
		maxSize = bSize
	}
	if length < 0 {
		length = maxSize - start
	}
	end := start + length
	if end > maxSize {
		end = maxSize
	}

	const chunk = 1 << 20
	regions := make([]DiffRegion, 0, 64)

	var inRun bool
	var runStart int64
	var runKind string

	flush := func(off int64) {
		if inRun {
			regions = append(regions, DiffRegion{Offset: runStart, Length: off - runStart, Kind: runKind})
			inRun = false
		}
	}

	for off := start; off < end; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		winEnd := off + chunk
		if winEnd > end {
			winEnd = end
		}
		// Read both sides, tolerating short reads for the shorter file.
		var ab, bb []byte
		if off < aSize {
			n := winEnd - off
			if off+n > aSize {
				n = aSize - off
			}
			var err error
			ab, err = a.Range(off, n)
			if err != nil {
				return nil, err
			}
		}
		if off < bSize {
			n := winEnd - off
			if off+n > bSize {
				n = bSize - off
			}
			var err error
			bb, err = b.Range(off, n)
			if err != nil {
				return nil, err
			}
		}
		for i := int64(0); i < winEnd-off; i++ {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			curOff := off + i
			var av, bv byte
			var av_ok, bv_ok bool
			if curOff < aSize && int64(len(ab)) > i {
				av = ab[i]
				av_ok = true
			}
			if curOff < bSize && int64(len(bb)) > i {
				bv = bb[i]
				bv_ok = true
			}
			kind := ""
			if av_ok && bv_ok {
				if av != bv {
					kind = "edit"
				}
			} else if av_ok && !bv_ok {
				kind = "remove"
			} else if !av_ok && bv_ok {
				kind = "add"
			}
			if kind == "" {
				flush(curOff)
				continue
			}
			if !inRun {
				inRun = true
				runStart = curOff
				runKind = kind
			} else if kind != runKind {
				flush(curOff)
				inRun = true
				runStart = curOff
				runKind = kind
			}
		}
		off = winEnd
	}
	flush(end)

	// Convert to JS-friendly output + summary
	out := make([]map[string]any, len(regions))
	editBytes := int64(0)
	addBytes := int64(0)
	removeBytes := int64(0)
	for i, r := range regions {
		out[i] = map[string]any{
			"offset": r.Offset,
			"length": r.Length,
			"kind":   r.Kind,
		}
		switch r.Kind {
		case "edit":
			editBytes += r.Length
		case "add":
			addBytes += r.Length
		case "remove":
			removeBytes += r.Length
		}
	}
	const blockSize int64 = 4096
	blocks := diffBlocksFromRegions(regions, start, end, blockSize)
	blockOut := make([]map[string]any, len(blocks))
	for i, b := range blocks {
		blockOut[i] = map[string]any{
			"offset":       b.Offset,
			"length":       b.Length,
			"changedBytes": b.ChangedBytes,
			"editBytes":    b.EditBytes,
			"addBytes":     b.AddBytes,
			"removeBytes":  b.RemoveBytes,
			"similarity":   b.Similarity,
		}
	}
	return map[string]any{
		"regions":     out,
		"regionCount": len(regions),
		"blocks":      blockOut,
		"blockCount":  len(blocks),
		"blockSize":   blockSize,
		"editBytes":   editBytes,
		"addBytes":    addBytes,
		"removeBytes": removeBytes,
		"aSize":       aSize,
		"bSize":       bSize,
	}, nil
}

func diffBlocksFromRegions(regions []DiffRegion, start, end, blockSize int64) []DiffBlock {
	if blockSize <= 0 || end <= start || len(regions) == 0 {
		return nil
	}
	out := []DiffBlock{}
	for alignedStart := start - start%blockSize; alignedStart < end; alignedStart += blockSize {
		blockStart := alignedStart
		blockEnd := alignedStart + blockSize
		if blockEnd > end {
			blockEnd = end
		}
		if blockEnd <= start {
			continue
		}
		if blockStart < start {
			blockStart = start
		}
		b := DiffBlock{Offset: blockStart, Length: blockEnd - blockStart}
		for _, r := range regions {
			rEnd := r.Offset + r.Length
			if rEnd <= blockStart {
				continue
			}
			if r.Offset >= blockEnd {
				break
			}
			ovStart := r.Offset
			if ovStart < blockStart {
				ovStart = blockStart
			}
			ovEnd := rEnd
			if ovEnd > blockEnd {
				ovEnd = blockEnd
			}
			changed := ovEnd - ovStart
			if changed <= 0 {
				continue
			}
			b.ChangedBytes += changed
			switch r.Kind {
			case "edit":
				b.EditBytes += changed
			case "add":
				b.AddBytes += changed
			case "remove":
				b.RemoveBytes += changed
			}
		}
		if b.ChangedBytes == 0 {
			continue
		}
		b.Similarity = 1 - float64(b.ChangedBytes)/float64(b.Length)
		out = append(out, b)
	}
	return out
}
