package analysis

import (
	"github.com/andydixon/link/internal/leviathan/document"
)

// Overview returns a compact first-pass report intended for both UI
// surfaces. It favours fast, bounded summaries over exhaustive output.
func Overview(d *document.Document) (map[string]any, error) {
	size := d.Size()
	hashes, err := Hashes(d, 0, -1)
	if err != nil {
		return nil, err
	}
	entropy, err := Entropy(d, 0, min64(size, 8<<20), 4096, 0)
	if err != nil {
		return nil, err
	}
	stringsOut, err := ExtractStrings(d, 0, min64(size, 16<<20), 4, []string{"ascii", "utf16le", "utf16be"}, 5000)
	if err != nil {
		return nil, err
	}
	iocs, err := ExtractIOCs(d, 0, min64(size, 16<<20), 5)
	if err != nil {
		return nil, err
	}
	carves, err := Carve(d, 0, min64(size, 64<<20), 128)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":    d.Name(),
		"size":    size,
		"hashes":  hashes,
		"entropy": overviewEntropy(entropy),
		"strings": overviewStrings(stringsOut),
		"iocs":    iocs["totals"],
		"carve":   overviewCarve(carves),
		"limits": map[string]any{
			"entropyBytes": min64(size, 8<<20),
			"stringsBytes": min64(size, 16<<20),
			"iocBytes":     min64(size, 16<<20),
			"carveBytes":   min64(size, 64<<20),
		},
	}, nil
}

func overviewEntropy(in map[string]any) map[string]any {
	return map[string]any{
		"windowSize": in["windowSize"],
		"step":       in["step"],
		"mean":       in["mean"],
		"min":        in["min"],
		"max":        in["max"],
		"highCount":  in["highCount"],
	}
}

func overviewStrings(items []map[string]any) map[string]any {
	counts := map[string]int{}
	for _, item := range items {
		if enc, ok := item["encoding"].(string); ok {
			counts[enc]++
		}
	}
	return map[string]any{
		"total":      len(items),
		"byEncoding": counts,
	}
}

func overviewCarve(items []map[string]any) map[string]any {
	counts := map[string]int{}
	valid := 0
	for _, item := range items {
		if kind, ok := item["kind"].(string); ok {
			counts[kind]++
		}
		if status, ok := item["status"].(string); ok && status == "valid" {
			valid++
		}
	}
	return map[string]any{
		"total":  len(items),
		"valid":  valid,
		"byKind": counts,
	}
}
