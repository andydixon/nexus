package analysis

import (
	"context"
	"math"

	"github.com/andydixon/link/internal/leviathan/document"
)

// Entropy computes Shannon entropy per sliding window over the range.
//
// The returned map has:
//
//	windowSize:   int
//	step:         int
//	samples:      []float64  — entropy per window (0..8)
//	offsets:      []int64    — start offset of each window
//	mean/min/max: summary stats
//	highCount:    number of windows with entropy >= 7.5 (candidate compressed/encrypted)
func Entropy(d *document.Document, start, length, windowSize, step int64) (map[string]any, error) {
	return EntropyContext(context.Background(), d, start, length, windowSize, step)
}

func EntropyContext(ctx context.Context, d *document.Document, start, length, windowSize, step int64) (map[string]any, error) {
	if windowSize <= 0 {
		windowSize = 4096
	}
	if step <= 0 {
		step = windowSize
	}
	if length < 0 {
		length = d.Size() - start
	}
	if length <= 0 {
		return map[string]any{
			"windowSize": windowSize, "step": step,
			"samples": []float64{}, "offsets": []int64{},
			"mean": 0.0, "min": 0.0, "max": 0.0, "highCount": 0,
		}, nil
	}

	// We run in a streaming fashion: advance through chunks, refill
	// a rolling window, and emit a sample whenever the cursor hits
	// a step boundary.
	samples := make([]float64, 0, length/step+1)
	offsets := make([]int64, 0, length/step+1)

	// For bounded memory we read the document range chunk by chunk
	// but we still need a window of size `windowSize`. Windows can be
	// as small as 256 or as large as 1MB; we cap to length.
	if windowSize > length {
		windowSize = length
	}

	// Simple approach: iterate in step-sized hops, reading window
	// bytes at each hop. This scales O(n * (w/step)) but window and
	// step are usually the same for UI.
	mean := 0.0
	minE := math.Inf(1)
	maxE := math.Inf(-1)
	high := 0

	for off := start; off < start+length; off += step {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		winLen := windowSize
		if off+winLen > start+length {
			winLen = start + length - off
		}
		buf, err := d.Range(off, winLen)
		if err != nil {
			return nil, err
		}
		e := shannon(buf)
		samples = append(samples, e)
		offsets = append(offsets, off)
		mean += e
		if e < minE {
			minE = e
		}
		if e > maxE {
			maxE = e
		}
		if e >= 7.5 {
			high++
		}
	}
	if len(samples) > 0 {
		mean /= float64(len(samples))
	} else {
		minE, maxE = 0, 0
	}

	return map[string]any{
		"windowSize": windowSize,
		"step":       step,
		"samples":    samples,
		"offsets":    offsets,
		"mean":       mean,
		"min":        minE,
		"max":        maxE,
		"highCount":  high,
	}, nil
}

func shannon(buf []byte) float64 {
	if len(buf) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range buf {
		counts[b]++
	}
	n := float64(len(buf))
	e := 0.0
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		e -= p * math.Log2(p)
	}
	return e
}
