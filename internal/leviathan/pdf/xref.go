package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/andydixon/link/internal/leviathan/document"
)

// XRefEntry describes one cross-reference entry after parsing either a
// classical xref table or a modern xref stream.
//
//	Type 0 — free object
//	Type 1 — in-use object; Offset is the byte offset in the file
//	Type 2 — object inside an object stream; ObjStmNum identifies the
//	         container ObjStm and Index is the 0-based position within it
type XRefEntry struct {
	Type      int   `json:"type"`
	ObjNum    int64 `json:"objNum"`
	Gen       int64 `json:"gen"`
	Offset    int64 `json:"offset"`
	ObjStmNum int64 `json:"objStmNum,omitempty"`
	Index     int64 `json:"index,omitempty"`
}

const maxObjectStreamEntries = 1 << 20

// ParseXRef attempts to reconstruct the cross-reference map. It handles
// both classical "xref\n0 N\n..." tables and xref streams (/Type /XRef).
// Returns a flat slice of entries covering every object it could resolve.
func ParseXRef(d *document.Document, whole []byte) ([]XRefEntry, string, error) {
	mode := ""
	entries := map[int64]XRefEntry{}

	// Find "startxref" near the end.
	idx := bytes.LastIndex(whole, []byte("startxref"))
	if idx < 0 {
		return nil, "", errors.New("no startxref")
	}
	after := whole[idx+len("startxref"):]
	after = bytes.TrimLeft(after, " \r\n\t")
	var xrefPos int64
	fmt.Sscanf(string(after), "%d", &xrefPos)
	if xrefPos <= 0 || xrefPos >= int64(len(whole)) {
		return nil, "", fmt.Errorf("bad startxref offset %d", xrefPos)
	}

	// Walk the xref chain via /Prev pointers.
	visited := map[int64]bool{}
	pos := xrefPos
	for pos > 0 && !visited[pos] {
		visited[pos] = true
		// Classical table if the first non-whitespace token is "xref".
		tail := whole[pos:]
		trimmed := bytes.TrimLeft(tail, " \r\n\t")
		if bytes.HasPrefix(trimmed, []byte("xref")) {
			if mode == "" {
				mode = "table"
			} else if mode != "table" {
				mode = "hybrid"
			}
			nextPrev, err := parseClassicalXRef(trimmed, entries)
			if err != nil {
				return mapToSlice(entries), mode, err
			}
			pos = nextPrev
			continue
		}
		// Otherwise an xref stream: an indirect object at `pos`.
		if mode == "" {
			mode = "stream"
		} else if mode != "stream" {
			mode = "hybrid"
		}
		nextPrev, err := parseXRefStream(d, whole, pos, entries)
		if err != nil {
			return mapToSlice(entries), mode, err
		}
		pos = nextPrev
	}
	return mapToSlice(entries), mode, nil
}

func mapToSlice(m map[int64]XRefEntry) []XRefEntry {
	out := make([]XRefEntry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	return out
}

var xrefRangeRE = regexp.MustCompile(`(?m)^\s*(\d+)\s+(\d+)\s*$`)
var xrefRowRE = regexp.MustCompile(`^(\d{10})\s+(\d{5})\s+([nf])`)
var prevRE = regexp.MustCompile(`/Prev\s+(\d+)`)

func parseClassicalXRef(block []byte, entries map[int64]XRefEntry) (int64, error) {
	// Skip "xref" line.
	nl := bytes.IndexByte(block, '\n')
	if nl < 0 {
		return 0, errors.New("xref: no newline after keyword")
	}
	cursor := block[nl+1:]
	for {
		m := xrefRangeRE.FindSubmatchIndex(cursor)
		if m == nil {
			break
		}
		if m[0] > 0 {
			// Subsection must begin at the start of the cursor.
			break
		}
		start, _ := strconv.ParseInt(string(cursor[m[2]:m[3]]), 10, 64)
		count, _ := strconv.ParseInt(string(cursor[m[4]:m[5]]), 10, 64)
		// Advance past the subsection line.
		nl2 := bytes.IndexByte(cursor, '\n')
		if nl2 < 0 {
			break
		}
		cursor = cursor[nl2+1:]
		for i := int64(0); i < count; i++ {
			if len(cursor) < 20 {
				return 0, errors.New("xref: truncated subsection")
			}
			row := cursor[:20]
			rm := xrefRowRE.FindSubmatch(row)
			if rm != nil {
				off, _ := strconv.ParseInt(string(rm[1]), 10, 64)
				gen, _ := strconv.ParseInt(string(rm[2]), 10, 64)
				typ := 1
				if rm[3][0] == 'f' {
					typ = 0
				}
				num := start + i
				if _, seen := entries[num]; !seen {
					entries[num] = XRefEntry{
						Type: typ, ObjNum: num, Gen: gen, Offset: off,
					}
				}
			}
			cursor = cursor[20:]
		}
	}
	// Locate trailer and /Prev.
	tIdx := bytes.Index(block, []byte("trailer"))
	if tIdx < 0 {
		return 0, nil
	}
	trailer := block[tIdx:]
	if e := bytes.Index(trailer, []byte("startxref")); e > 0 {
		trailer = trailer[:e]
	}
	if pm := prevRE.FindSubmatch(trailer); pm != nil {
		prev, _ := strconv.ParseInt(string(pm[1]), 10, 64)
		return prev, nil
	}
	return 0, nil
}

func parseXRefStream(d *document.Document, whole []byte, pos int64, entries map[int64]XRefEntry) (int64, error) {
	// Locate "obj" after pos, then "stream"/"endstream".
	if pos < 0 || int(pos) >= len(whole) {
		return 0, errors.New("xref stream: bad offset")
	}
	body := whole[pos:]
	objIdx := bytes.Index(body, []byte("obj"))
	if objIdx < 0 {
		return 0, errors.New("xref stream: no obj")
	}
	dictEnd := bytes.Index(body[objIdx:], []byte("stream"))
	if dictEnd < 0 {
		return 0, errors.New("xref stream: no stream")
	}
	dict := string(body[objIdx : objIdx+dictEnd])
	streamStart := objIdx + dictEnd + len("stream")
	if streamStart < len(body) && body[streamStart] == '\r' {
		streamStart++
	}
	if streamStart < len(body) && body[streamStart] == '\n' {
		streamStart++
	}
	endIdx := bytes.Index(body[streamStart:], []byte("endstream"))
	if endIdx < 0 {
		return 0, errors.New("xref stream: no endstream")
	}
	raw := body[streamStart : streamStart+endIdx]
	// trim trailing newline.
	if len(raw) > 0 && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	}
	if len(raw) > 0 && raw[len(raw)-1] == '\r' {
		raw = raw[:len(raw)-1]
	}

	// Decode the xref-stream dictionary fields.
	filters := parseDictNameList(dict, "/Filter")
	w := parseDictIntArray(dict, "/W")
	index := parseDictIntArray(dict, "/Index")
	sizeN := parseDictInt(dict, "/Size")
	predictor, columns, colors, bits := parseDecodeParms(dict)

	// Apply filters.
	data := append([]byte(nil), raw...)
	var err error
	for _, f := range filters {
		data, err = ApplyFilter(f, data)
		if err != nil {
			return 0, fmt.Errorf("xref stream: filter %s: %w", f, err)
		}
	}
	if predictor > 1 {
		data, err = ApplyPredictor(data, predictor, columns, colors, bits)
		if err != nil {
			return 0, err
		}
	}
	if len(w) != 3 {
		return 0, fmt.Errorf("xref stream: bad /W array %v", w)
	}
	entryLen := 0
	for _, width := range w {
		if width < 0 || width > 8 {
			return 0, fmt.Errorf("xref stream: invalid field width %d", width)
		}
		entryLen += width
	}
	if entryLen <= 0 {
		return 0, errors.New("xref stream: zero-length entries")
	}
	if len(index) == 0 {
		if sizeN < 0 {
			return 0, errors.New("xref stream: negative /Size")
		}
		index = []int{0, sizeN}
	}
	if len(index)%2 != 0 {
		return 0, errors.New("xref stream: /Index must contain start/count pairs")
	}
	// Walk subsections.
	pi := 0
	for s := 0; s+1 < len(index); s += 2 {
		first := int64(index[s])
		count := int64(index[s+1])
		if first < 0 || count < 0 {
			return 0, errors.New("xref stream: negative /Index value")
		}
		for i := int64(0); i < count; i++ {
			if pi+entryLen > len(data) {
				break
			}
			row := data[pi : pi+entryLen]
			pi += entryLen
			t := readBE(row[:w[0]])
			f2 := readBE(row[w[0] : w[0]+w[1]])
			f3 := readBE(row[w[0]+w[1] : w[0]+w[1]+w[2]])
			if w[0] == 0 {
				t = 1 // default Type 1 per spec
			}
			num := first + i
			if _, seen := entries[num]; seen {
				continue
			}
			switch t {
			case 0:
				entries[num] = XRefEntry{Type: 0, ObjNum: num, Gen: f3}
			case 1:
				entries[num] = XRefEntry{Type: 1, ObjNum: num, Gen: f3, Offset: f2}
			case 2:
				entries[num] = XRefEntry{
					Type: 2, ObjNum: num, ObjStmNum: f2, Index: f3,
				}
			}
		}
	}
	if prev := parseDictInt(dict, "/Prev"); prev > 0 {
		return int64(prev), nil
	}
	return 0, nil
}

func readBE(b []byte) int64 {
	v := int64(0)
	for _, c := range b {
		v = (v << 8) | int64(c)
	}
	return v
}

// ---------------------------------------------------------------------------
// ObjStm — compressed object streams
// ---------------------------------------------------------------------------

// DecodeObjStm decodes an /ObjStm stream and returns a map of
// object-number → raw object body (text, never with "stream").
func DecodeObjStm(raw []byte, dict string) (map[int64]string, error) {
	filters := parseDictNameList(dict, "/Filter")
	predictor, columns, colors, bits := parseDecodeParms(dict)
	data := append([]byte(nil), raw...)
	var err error
	for _, f := range filters {
		data, err = ApplyFilter(f, data)
		if err != nil {
			return nil, err
		}
	}
	if predictor > 1 {
		data, err = ApplyPredictor(data, predictor, columns, colors, bits)
		if err != nil {
			return nil, err
		}
	}
	n := parseDictInt(dict, "/N")
	first := parseDictInt(dict, "/First")
	if n <= 0 || first <= 0 || first >= len(data) {
		return nil, fmt.Errorf("ObjStm: bad /N=%d /First=%d", n, first)
	}
	header := strings.Fields(string(data[:first]))
	if n > maxObjectStreamEntries || n > len(header)/2 {
		return nil, fmt.Errorf("ObjStm: header has %d tokens, need %d", len(header), n*2)
	}
	out := map[int64]string{}
	offsets := make([]int, n)
	nums := make([]int64, n)
	for i := 0; i < n; i++ {
		num, _ := strconv.ParseInt(header[i*2], 10, 64)
		off, _ := strconv.Atoi(header[i*2+1])
		nums[i] = num
		offsets[i] = off
	}
	for i := 0; i < n; i++ {
		start := first + offsets[i]
		end := len(data)
		if i+1 < n {
			end = first + offsets[i+1]
		}
		if start < 0 || end > len(data) || start > end {
			continue
		}
		out[nums[i]] = strings.TrimSpace(string(data[start:end]))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Small dictionary helpers
// ---------------------------------------------------------------------------

var decodeParmRE = regexp.MustCompile(`/DecodeParms\s*<<([^>]*)>>`)

func parseDictInt(dict, key string) int {
	pat := regexp.MustCompile(regexp.QuoteMeta(key) + `\s+(-?\d+)\b`)
	m := pat.FindStringSubmatch(dict)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func parseDictIntArray(dict, key string) []int {
	pat := regexp.MustCompile(regexp.QuoteMeta(key) + `\s*\[([^\]]*)\]`)
	m := pat.FindStringSubmatch(dict)
	if m == nil {
		return nil
	}
	parts := strings.Fields(m[1])
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func parseDictNameList(dict, key string) []string {
	// Handles both `/Filter /FlateDecode` and `/Filter [/A85 /Flate]`.
	pat1 := regexp.MustCompile(regexp.QuoteMeta(key) + `\s*/([A-Za-z0-9]+)`)
	if m := pat1.FindStringSubmatch(dict); m != nil {
		return []string{m[1]}
	}
	pat2 := regexp.MustCompile(regexp.QuoteMeta(key) + `\s*\[([^\]]*)\]`)
	if m := pat2.FindStringSubmatch(dict); m != nil {
		parts := strings.Fields(m[1])
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.TrimPrefix(p, "/"))
		}
		return out
	}
	return nil
}

func parseDecodeParms(dict string) (predictor, columns, colors, bits int) {
	m := decodeParmRE.FindStringSubmatch(dict)
	if m == nil {
		return 1, 1, 1, 8
	}
	inner := m[1]
	predictor = parseDictInt(inner, "/Predictor")
	if predictor == 0 {
		predictor = 1
	}
	columns = parseDictInt(inner, "/Columns")
	if columns == 0 {
		columns = 1
	}
	colors = parseDictInt(inner, "/Colors")
	if colors == 0 {
		colors = 1
	}
	bits = parseDictInt(inner, "/BitsPerComponent")
	if bits == 0 {
		bits = 8
	}
	return
}
