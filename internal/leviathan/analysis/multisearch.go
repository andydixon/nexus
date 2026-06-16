package analysis

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

type MultiSearchQuery struct {
	Mode  string
	Query string
}

func ParseMultiSearchSpec(spec string) ([]MultiSearchQuery, error) {
	spec = strings.ReplaceAll(spec, ";", "\n")
	out := []MultiSearchQuery{}
	for _, line := range strings.Split(spec, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, errors.New("multi-search lines require: text|hex|regex <query>")
		}
		mode := strings.ToLower(parts[0])
		query := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
		switch mode {
		case "text", "hex", "regex":
		default:
			return nil, fmt.Errorf("unknown multi-search mode %q", mode)
		}
		if query == "" {
			return nil, errors.New("empty multi-search query")
		}
		out = append(out, MultiSearchQuery{Mode: mode, Query: query})
	}
	if len(out) == 0 {
		return nil, errors.New("no multi-search queries")
	}
	return out, nil
}

func MultiSearch(d *document.Document, start, length int64, spec string, maxPerQuery int) (map[string]any, error) {
	queries, err := ParseMultiSearchSpec(spec)
	if err != nil {
		return nil, err
	}
	if maxPerQuery <= 0 {
		maxPerQuery = 1000
	}
	results := make([]map[string]any, 0, len(queries))
	all := []map[string]any{}
	for i, q := range queries {
		var hits []SearchHit
		switch q.Mode {
		case "text":
			hits, err = SearchBytes(d, start, length, []byte(q.Query), maxPerQuery)
		case "hex":
			var needle []byte
			needle, err = parseMultiSearchHex(q.Query)
			if err == nil {
				hits, err = SearchBytes(d, start, length, needle, maxPerQuery)
			}
		case "regex":
			if _, err = regexp.Compile(q.Query); err == nil {
				hits, err = SearchRegex(d, start, length, q.Query, maxPerQuery)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("query %d: %w", i+1, err)
		}
		maps := HitsToMaps(hits)
		name := fmt.Sprintf("%s:%s", q.Mode, q.Query)
		for _, h := range maps {
			h["mode"] = q.Mode
			h["query"] = q.Query
			h["name"] = name
			all = append(all, h)
		}
		results = append(results, map[string]any{
			"mode":  q.Mode,
			"query": q.Query,
			"count": len(hits),
			"hits":  maps,
		})
	}
	return map[string]any{
		"queries": results,
		"all":     all,
		"total":   len(all),
	}, nil
}

func parseMultiSearchHex(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', ':', '-':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if len(s)%2 != 0 {
		s = "0" + s
	}
	return hex.DecodeString(s)
}
