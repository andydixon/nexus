package analysis

import (
	"context"
	"regexp"

	"github.com/andydixon/link/internal/leviathan/document"
)

// ioc regexes. They run against printable ASCII strings extracted
// from the document, which is the standard approach for IOC hunting
// and avoids matching noise in raw binary data.
var iocRegexes = map[string]*regexp.Regexp{
	"url":      regexp.MustCompile(`(?i)\b(?:https?|ftp)://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`),
	"domain":   regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9\-]{0,61}[a-z0-9])?\.)+(?:com|net|org|io|co|uk|de|fr|ru|cn|info|xyz|app|dev|sh|me|gov|mil|edu|biz)\b`),
	"email":    regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`),
	"ipv4":     regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`),
	"ipv6":     regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`),
	"guid":     regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`),
	"md5":      regexp.MustCompile(`\b[a-fA-F0-9]{32}\b`),
	"sha1":     regexp.MustCompile(`\b[a-fA-F0-9]{40}\b`),
	"sha256":   regexp.MustCompile(`\b[a-fA-F0-9]{64}\b`),
	"winpath":  regexp.MustCompile(`[A-Za-z]:\\(?:[^\\/:*?"<>|\r\n]+\\)*[^\\/:*?"<>|\r\n]*`),
	"unc":      regexp.MustCompile(`\\\\[A-Za-z0-9._\-]+(?:\\[^\\/:*?"<>|\r\n]+)+`),
	"unixpath": regexp.MustCompile(`(?:/(?:etc|var|usr|opt|home|tmp|root|srv|bin|sbin|lib|lib64)(?:/[A-Za-z0-9._\-]+)+)`),
	"regkey":   regexp.MustCompile(`(?i)\b(?:HKLM|HKCU|HKCR|HKU|HKCC|HKEY_[A-Z_]+)\\[^\s"<>]+`),
	"onion":    regexp.MustCompile(`(?i)\b[a-z2-7]{16,56}\.onion\b`),
	"btc":      regexp.MustCompile(`\b(?:[13][a-km-zA-HJ-NP-Z1-9]{25,34}|bc1[a-z0-9]{20,90})\b`),
}

// ExtractIOCs runs the IOC regexes over ASCII strings first and then
// UTF-16 LE/BE strings. Returns a map of category -> slice of hits
// with offsets into the original file.
func ExtractIOCs(d *document.Document, start, length int64, minLen int) (map[string]any, error) {
	return ExtractIOCsContext(context.Background(), d, start, length, minLen)
}

func ExtractIOCsContext(ctx context.Context, d *document.Document, start, length int64, minLen int) (map[string]any, error) {
	if length < 0 {
		length = d.Size() - start
	}
	if minLen <= 0 {
		minLen = 5
	}

	strings, err := ExtractStringsContext(ctx, d, start, length, minLen, []string{"ascii", "utf16le", "utf16be"}, 200_000)
	if err != nil {
		return nil, err
	}

	result := map[string]any{}
	totals := map[string]int{}
	for category, re := range iocRegexes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hits := make([]map[string]any, 0)
		for _, s := range strings {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			str, _ := s["value"].(string)
			base, _ := s["offset"].(int64)
			for _, loc := range re.FindAllStringIndex(str, -1) {
				hits = append(hits, map[string]any{
					"category": category,
					"offset":   base + int64(loc[0]),
					"length":   loc[1] - loc[0],
					"value":    str[loc[0]:loc[1]],
				})
			}
		}
		result[category] = hits
		totals[category] = len(hits)
	}
	// flat list for UI convenience
	all := []map[string]any{}
	for _, v := range result {
		if s, ok := v.([]map[string]any); ok {
			all = append(all, s...)
		}
	}
	return map[string]any{
		"byCategory":   result,
		"all":          all,
		"totals":       totals,
		"totalStrings": len(strings),
	}, nil
}
