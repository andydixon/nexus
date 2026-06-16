package analysis

import (
	"context"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// Rule represents a lightweight YARA-like rule. A rule matches when
// Condition evaluates to true for the document. Phase 1 supports:
//   - "all of them" (every string pattern must appear)
//   - "any of them" (at least one pattern must appear)
//   - "<count> of them" (at least N patterns)
//   - named identifiers referencing specific strings with $x or !$x
type Rule struct {
	Name      string
	Tags      []string
	Strings   []RuleString
	Condition string
}

// RuleString is one string directive inside a rule.
type RuleString struct {
	ID    string
	Kind  string // "ascii", "hex", "regex"
	Value string // original source text
	// compiled:
	raw      []byte
	regex    *regexp.Regexp
	nocase   bool
	ascii    bool
	wide     bool
	fullword bool
}

// ParseRules parses a simplified YARA-like source:
//
//	rule Name : tag1 tag2 {
//	  strings:
//	    $a = "hello" nocase
//	    $b = { 4d 5a ?? ?? }
//	    $c = /foo[0-9]+/
//	  condition:
//	    any of them
//	}
//
// Hex patterns support `??` wildcards by being converted to a regex.
func ParseRules(src string) ([]Rule, error) {
	rules := []Rule{}
	s := strings.TrimSpace(src)
	if s == "" {
		return rules, nil
	}
	// Split on "rule " keywords from start of line.
	entries := splitRules(s)
	for _, e := range entries {
		r, err := parseRule(e)
		if err != nil {
			return rules, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func splitRules(src string) []string {
	out := []string{}
	i := 0
	for i < len(src) {
		idx := strings.Index(src[i:], "rule ")
		if idx < 0 {
			break
		}
		j := i + idx
		// find matching close brace (naïve but workable for phase 1)
		depth := 0
		start := j
		k := j
		for k < len(src) {
			c := src[k]
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					out = append(out, src[start:k+1])
					k++
					break
				}
			}
			k++
		}
		i = k
	}
	return out
}

var ruleHeaderRe = regexp.MustCompile(`(?s)^\s*rule\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s*:\s*([^{]*))?\s*\{(.*)\}\s*$`)

func parseRule(src string) (Rule, error) {
	m := ruleHeaderRe.FindStringSubmatch(src)
	if m == nil {
		return Rule{}, errors.New("malformed rule")
	}
	r := Rule{Name: m[1]}
	if tags := strings.TrimSpace(m[2]); tags != "" {
		r.Tags = strings.Fields(tags)
	}
	body := m[3]
	if i := strings.Index(body, "strings:"); i >= 0 {
		sEnd := strings.Index(body, "condition:")
		sect := body[i+len("strings:") : sEnd]
		for _, line := range strings.Split(sect, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			rs, err := parseRuleString(line)
			if err != nil {
				return r, err
			}
			r.Strings = append(r.Strings, rs)
		}
	}
	if i := strings.Index(body, "condition:"); i >= 0 {
		r.Condition = strings.TrimSpace(body[i+len("condition:"):])
	} else {
		return r, errors.New("rule missing condition")
	}
	return r, nil
}

var ruleStringRe = regexp.MustCompile(`^\s*(\$[A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+?)\s*$`)

func parseRuleString(line string) (RuleString, error) {
	m := ruleStringRe.FindStringSubmatch(line)
	if m == nil {
		return RuleString{}, errors.New("bad string line: " + line)
	}
	id := m[1]
	rest := strings.TrimSpace(m[2])
	value, modifiers, err := splitRuleValueModifiers(rest)
	if err != nil {
		return RuleString{}, err
	}
	rs := RuleString{ID: id, Value: value}
	for _, mod := range modifiers {
		switch strings.ToLower(mod) {
		case "nocase":
			rs.nocase = true
		case "ascii":
			rs.ascii = true
		case "wide":
			rs.wide = true
		case "fullword":
			rs.fullword = true
		default:
			return rs, errors.New("unknown string modifier: " + mod)
		}
	}
	switch {
	case strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) && len(value) >= 2:
		rs.Kind = "ascii"
		rs.raw = []byte(value[1 : len(value)-1])
		if !rs.ascii && !rs.wide {
			rs.ascii = true
		}
	case strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}"):
		rs.Kind = "hex"
		pattern := regexFromHexPattern(value[1 : len(value)-1])
		re, err := regexp.Compile("(?s)" + pattern)
		if err != nil {
			return rs, err
		}
		rs.regex = re
	case strings.HasPrefix(value, "/") && strings.HasSuffix(value, "/"):
		rs.Kind = "regex"
		pat := value[1 : len(value)-1]
		if rs.nocase {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return rs, err
		}
		rs.regex = re
	default:
		return rs, errors.New("unknown string form: " + value)
	}
	return rs, nil
}

func splitRuleValueModifiers(rest string) (string, []string, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", nil, errors.New("empty string value")
	}
	var end int
	switch rest[0] {
	case '"':
		escaped := false
		end = -1
		for i := 1; i < len(rest); i++ {
			c := rest[i]
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				end = i + 1
				break
			}
		}
		if end < 0 {
			return "", nil, errors.New("unterminated string literal")
		}
	case '{':
		idx := strings.IndexByte(rest, '}')
		if idx < 0 {
			return "", nil, errors.New("unterminated hex string")
		}
		end = idx + 1
	case '/':
		escaped := false
		end = -1
		for i := 1; i < len(rest); i++ {
			c := rest[i]
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '/' {
				end = i + 1
				break
			}
		}
		if end < 0 {
			return "", nil, errors.New("unterminated regex")
		}
	default:
		return "", nil, errors.New("unknown string form: " + rest)
	}
	return strings.TrimSpace(rest[:end]), strings.Fields(strings.TrimSpace(rest[end:])), nil
}

func regexFromHexPattern(hexPat string) string {
	var sb strings.Builder
	hexPat = strings.ReplaceAll(hexPat, " ", "")
	for i := 0; i < len(hexPat); {
		if i+1 >= len(hexPat) {
			break
		}
		a, b := hexPat[i], hexPat[i+1]
		if a == '?' && b == '?' {
			sb.WriteString(`[\x00-\xff]`)
		} else {
			decoded, err := hex.DecodeString(string([]byte{a, b}))
			if err != nil {
				// bail to literal to avoid panic
				sb.WriteString(regexp.QuoteMeta(string([]byte{a, b})))
			} else {
				sb.WriteString(regexp.QuoteMeta(string(decoded)))
			}
		}
		i += 2
	}
	return sb.String()
}

// RuleMatch is a per-rule result.
type RuleMatch struct {
	Name    string
	Tags    []string
	Matched bool
	Hits    map[string][]SearchHit
}

// RunRules scans rules against the document range and returns matches.
func RunRules(d *document.Document, start, length int64, rules []Rule, maxPerString int) ([]map[string]any, error) {
	return RunRulesContext(context.Background(), d, start, length, rules, maxPerString)
}

func RunRulesContext(ctx context.Context, d *document.Document, start, length int64, rules []Rule, maxPerString int) ([]map[string]any, error) {
	if maxPerString <= 0 {
		maxPerString = 1000
	}
	out := make([]map[string]any, 0, len(rules))
	for _, r := range rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hits := map[string][]SearchHit{}
		for _, s := range r.Strings {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			switch s.Kind {
			case "ascii":
				found, err := searchRuleTextContext(ctx, d, start, length, s, maxPerString)
				if err != nil {
					return nil, err
				}
				hits[s.ID] = found
			case "hex", "regex":
				found, err := SearchRegexContext(ctx, d, start, length, s.regex.String(), maxPerString)
				if err != nil {
					return nil, err
				}
				if s.fullword {
					found = filterFullWordHits(d, found, false)
				}
				hits[s.ID] = found
			}
		}
		matched := evalCondition(r, hits)
		var hitFlat []map[string]any
		for id, hs := range hits {
			for _, h := range hs {
				hitFlat = append(hitFlat, map[string]any{
					"id":     id,
					"offset": h.Offset,
					"length": h.Length,
					"sample": h.Sample,
				})
			}
		}
		out = append(out, map[string]any{
			"name":    r.Name,
			"tags":    toAnySlice(r.Tags),
			"matched": matched,
			"hits":    hitFlat,
		})
	}
	return out, nil
}

func searchRuleText(d *document.Document, start, length int64, s RuleString, max int) ([]SearchHit, error) {
	return searchRuleTextContext(context.Background(), d, start, length, s, max)
}

func searchRuleTextContext(ctx context.Context, d *document.Document, start, length int64, s RuleString, max int) ([]SearchHit, error) {
	out := []SearchHit{}
	if s.ascii {
		var (
			found []SearchHit
			err   error
		)
		if s.nocase {
			found, err = SearchRegexContext(ctx, d, start, length, "(?i)"+regexp.QuoteMeta(string(s.raw)), max)
		} else {
			found, err = SearchBytesContext(ctx, d, start, length, s.raw, max)
		}
		if err != nil {
			return nil, err
		}
		if s.fullword {
			found = filterFullWordHits(d, found, false)
		}
		out = append(out, found...)
	}
	if s.wide {
		wide := utf16LEBytes(s.raw)
		var (
			found []SearchHit
			err   error
		)
		if s.nocase {
			pat := wideNoCasePattern(s.raw)
			found, err = SearchRegexContext(ctx, d, start, length, pat, max)
		} else {
			found, err = SearchBytesContext(ctx, d, start, length, wide, max)
		}
		if err != nil {
			return nil, err
		}
		if s.fullword {
			found = filterFullWordHits(d, found, true)
		}
		out = append(out, found...)
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

func utf16LEBytes(in []byte) []byte {
	out := make([]byte, 0, len(in)*2)
	for _, b := range in {
		out = append(out, b, 0)
	}
	return out
}

func wideNoCasePattern(in []byte) string {
	var sb strings.Builder
	sb.WriteString("(?i)")
	for _, b := range in {
		sb.WriteString(regexp.QuoteMeta(string([]byte{b, 0})))
	}
	return sb.String()
}

func filterFullWordHits(d *document.Document, hits []SearchHit, wide bool) []SearchHit {
	out := hits[:0]
	for _, h := range hits {
		if isFullWordHit(d, h, wide) {
			out = append(out, h)
		}
	}
	return out
}

func isFullWordHit(d *document.Document, h SearchHit, wide bool) bool {
	if wide {
		beforeOff := h.Offset - 2
		afterOff := h.Offset + int64(h.Length)
		beforeWord := false
		afterWord := false
		if beforeOff >= 0 {
			if b, err := d.Range(beforeOff, 2); err == nil && len(b) == 2 && b[1] == 0 {
				beforeWord = isWordByte(b[0])
			}
		}
		if afterOff+1 < d.Size() {
			if b, err := d.Range(afterOff, 2); err == nil && len(b) == 2 && b[1] == 0 {
				afterWord = isWordByte(b[0])
			}
		}
		return !beforeWord && !afterWord
	}
	beforeWord := false
	afterWord := false
	if h.Offset > 0 {
		if b, err := d.At(h.Offset - 1); err == nil {
			beforeWord = isWordByte(b)
		}
	}
	if h.Offset+int64(h.Length) < d.Size() {
		if b, err := d.At(h.Offset + int64(h.Length)); err == nil {
			afterWord = isWordByte(b)
		}
	}
	return !beforeWord && !afterWord
}

func isWordByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func evalCondition(r Rule, hits map[string][]SearchHit) bool {
	cond := strings.TrimSpace(r.Condition)
	condLower := strings.ToLower(cond)
	present := 0
	for _, s := range r.Strings {
		if len(hits[s.ID]) > 0 {
			present++
		}
	}
	switch {
	case condLower == "any of them":
		return present > 0
	case condLower == "all of them":
		return present == len(r.Strings)
	case strings.HasSuffix(condLower, "of them"):
		// "<n> of them"
		parts := strings.Fields(condLower)
		if len(parts) >= 3 {
			n, ok := parseRuleCount(parts[0])
			return ok && present >= n
		}
	}
	if ok, matched := evalRuleSetCondition(cond, hits); ok {
		return matched
	}
	p := conditionParser{tokens: tokenizeCondition(cond), hits: hits}
	return p.parse()
}

func evalRuleSetCondition(cond string, hits map[string][]SearchHit) (bool, bool) {
	tokens := tokenizeCondition(cond)
	if len(tokens) < 4 || strings.ToLower(tokens[1]) != "of" || tokens[2] != "(" || tokens[len(tokens)-1] != ")" {
		return false, false
	}
	ids := []string{}
	for _, tok := range tokens[3 : len(tokens)-1] {
		if tok == "," {
			continue
		}
		if strings.HasPrefix(tok, "$") {
			ids = append(ids, tok)
		}
	}
	if len(ids) == 0 {
		return true, false
	}
	count := 0
	for _, id := range ids {
		if len(hits[id]) > 0 {
			count++
		}
	}
	switch strings.ToLower(tokens[0]) {
	case "any":
		return true, count > 0
	case "all":
		return true, count == len(ids)
	default:
		n, ok := parseRuleCount(strings.ToLower(tokens[0]))
		return true, ok && count >= n
	}
}

func parseRuleCount(s string) (int, bool) {
	switch s {
	case "1", "one":
		return 1, true
	case "2", "two":
		return 2, true
	case "3", "three":
		return 3, true
	default:
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
}

type conditionParser struct {
	tokens []string
	pos    int
	hits   map[string][]SearchHit
}

func (p *conditionParser) parse() bool {
	v := p.parseOr()
	return v
}

func (p *conditionParser) parseOr() bool {
	v := p.parseAnd()
	for strings.EqualFold(p.peek(), "or") {
		p.next()
		v = p.parseAnd() || v
	}
	return v
}

func (p *conditionParser) parseAnd() bool {
	v := p.parseNot()
	for strings.EqualFold(p.peek(), "and") {
		p.next()
		v = p.parseNot() && v
	}
	return v
}

func (p *conditionParser) parseNot() bool {
	if strings.EqualFold(p.peek(), "not") || p.peek() == "!" {
		p.next()
		return !p.parseNot()
	}
	return p.parsePrimary()
}

func (p *conditionParser) parsePrimary() bool {
	tok := p.next()
	switch {
	case tok == "":
		return false
	case tok == "(":
		v := p.parseOr()
		if p.peek() == ")" {
			p.next()
		}
		return v
	case strings.EqualFold(tok, "true"):
		return true
	case strings.EqualFold(tok, "false"):
		return false
	case strings.HasPrefix(tok, "$"):
		return len(p.hits[tok]) > 0
	default:
		return false
	}
}

func (p *conditionParser) peek() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *conditionParser) next() string {
	if p.pos >= len(p.tokens) {
		return ""
	}
	tok := p.tokens[p.pos]
	p.pos++
	return tok
}

func tokenizeCondition(cond string) []string {
	out := []string{}
	for i := 0; i < len(cond); {
		c := cond[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		if strings.ContainsRune("(),!", rune(c)) {
			out = append(out, string(c))
			i++
			continue
		}
		start := i
		for i < len(cond) {
			c = cond[i]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || strings.ContainsRune("(),!", rune(c)) {
				break
			}
			i++
		}
		out = append(out, cond[start:i])
	}
	return out
}
