// Package pdf provides PDF surgery: object enumeration, full stream
// filter chain, cross-reference parsing (classical tables and xref
// streams), compressed object-stream decoding, and surfacing of hostile
// or otherwise-interesting constructs (JavaScript, embedded files, URI
// and launch actions, XFA, OpenAction triggers).
//
// It is not a rendering engine — it will not produce pixels. It is a
// structural tool for humans and detection pipelines.
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

const maxPDFParseBytes = 128 << 20

// Object is one PDF indirect object.
type Object struct {
	Num       int64
	Gen       int64
	Offset    int64
	Dict      string
	HasStream bool
	StreamOff int64
	StreamLen int64
	Filters   []string
	Type      string
	Subtype   string
	Source    string // "direct" or "objstm"
	Container int64  // when Source == "objstm", the ObjStm number
}

// Parse returns a summary of the PDF structure, including cross-
// reference entries, an object list (direct + recovered from ObjStms),
// surfaced suspicious constructs, and top-level flags.
func Parse(d *document.Document) (map[string]any, error) {
	size := d.Size()
	head, _ := d.Range(0, 16)
	if len(head) < 5 || string(head[:5]) != "%PDF-" {
		return nil, errors.New("not a PDF header")
	}
	if size > maxPDFParseBytes {
		return nil, fmt.Errorf("pdf parse skipped: file > %d MiB", maxPDFParseBytes>>20)
	}
	version := string(head[5:8])

	whole, err := d.Range(0, size)
	if err != nil {
		return nil, err
	}

	objs := scanIndirectObjects(whole)

	// Try xref first; fall back gracefully if it isn't parseable.
	xref, xrefMode, xrefErr := ParseXRef(d, whole)
	xrefNote := ""
	if xrefErr != nil {
		xrefNote = xrefErr.Error()
	}

	// Decode any ObjStm streams we find and append recovered children.
	recovered := recoverObjectStreams(whole, d, objs)
	objs = append(objs, recovered...)

	// Surface flags.
	surfaced := Surface(objs)
	encrypted := bytes.Contains(whole, []byte("/Encrypt"))
	linearised := HasLinearisation(whole)

	// Locate trailer text (for quick eyeball).
	trailerText := ""
	if tIdx := bytes.LastIndex(whole, []byte("trailer")); tIdx >= 0 {
		end := bytes.Index(whole[tIdx:], []byte("startxref"))
		if end >= 0 {
			trailerText = string(whole[tIdx : tIdx+end])
		}
	}

	return map[string]any{
		"version":    version,
		"size":       size,
		"encrypted":  encrypted,
		"linearised": linearised,
		"trailer":    trailerText,
		"objects":    objectsToMaps(objs),
		"xref":       xrefEntriesToMaps(xref),
		"xrefMode":   xrefMode,
		"xrefNote":   xrefNote,
		"suspicious": surfaceToMaps(surfaced),
	}, nil
}

func scanIndirectObjects(whole []byte) []Object {
	objRE := regexp.MustCompile(`(?m)(\d+)\s+(\d+)\s+obj\b`)
	locs := objRE.FindAllSubmatchIndex(whole, -1)
	objs := make([]Object, 0, len(locs))
	for _, loc := range locs {
		start := int64(loc[0])
		num, _ := strconv.ParseInt(string(whole[loc[2]:loc[3]]), 10, 64)
		gen, _ := strconv.ParseInt(string(whole[loc[4]:loc[5]]), 10, 64)
		o := Object{Num: num, Gen: gen, Offset: start, Source: "direct"}
		endIdx := bytes.Index(whole[loc[1]:], []byte("endobj"))
		if endIdx < 0 {
			continue
		}
		body := whole[loc[1] : loc[1]+endIdx]
		streamIdx := bytes.Index(body, []byte("stream"))
		var dict []byte
		if streamIdx >= 0 {
			dict = body[:streamIdx]
			s := streamIdx + len("stream")
			if s < len(body) && body[s] == '\r' {
				s++
			}
			if s < len(body) && body[s] == '\n' {
				s++
			}
			o.HasStream = true
			o.StreamOff = int64(loc[1]) + int64(s)
			endStreamIdx := bytes.LastIndex(body, []byte("endstream"))
			if endStreamIdx < 0 {
				continue
			}
			endStreamAbs := int64(loc[1]) + int64(endStreamIdx)
			// trim trailing newline/cr
			for k := 0; k < 2; k++ {
				if endStreamAbs > 0 && endStreamAbs-1 < int64(len(whole)) {
					if whole[endStreamAbs-1] == '\n' || whole[endStreamAbs-1] == '\r' {
						endStreamAbs--
					}
				}
			}
			o.StreamLen = endStreamAbs - o.StreamOff
		} else {
			dict = body
		}
		o.Dict = strings.TrimSpace(string(dict))
		o.Filters = detectFilters(o.Dict)
		o.Type = detectType(o.Dict)
		o.Subtype = detectSubtype(o.Dict)
		objs = append(objs, o)
	}
	return objs
}

func recoverObjectStreams(whole []byte, d *document.Document, objs []Object) []Object {
	out := []Object{}
	for _, o := range objs {
		if !o.HasStream {
			continue
		}
		if !strings.Contains(o.Dict, "/Type /ObjStm") && !strings.Contains(o.Dict, "/Type/ObjStm") {
			continue
		}
		raw, err := d.Range(o.StreamOff, o.StreamLen)
		if err != nil {
			continue
		}
		// Apply filters in order before handing to ObjStm decoder.
		children, err := DecodeObjStm(raw, o.Dict)
		if err != nil {
			continue
		}
		for num, body := range children {
			out = append(out, Object{
				Num:       num,
				Gen:       0,
				Offset:    o.StreamOff,
				Dict:      body,
				HasStream: false,
				Type:      detectType(body),
				Subtype:   detectSubtype(body),
				Source:    "objstm",
				Container: o.Num,
			})
		}
	}
	return out
}

func objectsToMaps(objs []Object) []map[string]any {
	out := make([]map[string]any, len(objs))
	for i, o := range objs {
		filters := make([]any, len(o.Filters))
		for j, f := range o.Filters {
			filters[j] = f
		}
		out[i] = map[string]any{
			"num":       o.Num,
			"gen":       o.Gen,
			"offset":    o.Offset,
			"dict":      o.Dict,
			"hasStream": o.HasStream,
			"streamOff": o.StreamOff,
			"streamLen": o.StreamLen,
			"filters":   filters,
			"type":      o.Type,
			"subtype":   o.Subtype,
			"source":    o.Source,
			"container": o.Container,
		}
	}
	return out
}

func xrefEntriesToMaps(entries []XRefEntry) []map[string]any {
	out := make([]map[string]any, len(entries))
	for i, e := range entries {
		out[i] = map[string]any{
			"type":   e.Type,
			"num":    e.ObjNum,
			"gen":    e.Gen,
			"offset": e.Offset,
			"objStm": e.ObjStmNum,
			"index":  e.Index,
		}
	}
	return out
}

func surfaceToMaps(hits []SuspiciousHit) []map[string]any {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = map[string]any{
			"kind":   h.Kind,
			"num":    h.ObjNum,
			"gen":    h.Gen,
			"offset": h.Offset,
			"detail": h.Detail,
		}
	}
	return out
}

var filtersRE = regexp.MustCompile(`/Filter\s*(?:\[([^\]]*)\]|/([A-Za-z0-9]+))`)

func detectFilters(dict string) []string {
	m := filtersRE.FindStringSubmatch(dict)
	if m == nil {
		return nil
	}
	if m[2] != "" {
		return []string{m[2]}
	}
	parts := strings.Fields(m[1])
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimPrefix(p, "/")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

var (
	typeRE    = regexp.MustCompile(`/Type\s*/([A-Za-z0-9]+)`)
	subtypeRE = regexp.MustCompile(`/Subtype\s*/([A-Za-z0-9]+)`)
)

func detectType(dict string) string {
	m := typeRE.FindStringSubmatch(dict)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func detectSubtype(dict string) string {
	m := subtypeRE.FindStringSubmatch(dict)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// DecodeStream reads the stream for object objNum and runs it through
// the declared filter chain (with predictor). Works for both direct
// objects and those recovered from ObjStm containers.
func DecodeStream(d *document.Document, objNum int64) (map[string]any, error) {
	summary, err := Parse(d)
	if err != nil {
		return nil, err
	}
	for _, o := range summary["objects"].([]map[string]any) {
		n, _ := o["num"].(int64)
		if n != objNum {
			continue
		}
		hasStream, _ := o["hasStream"].(bool)
		if !hasStream {
			return nil, fmt.Errorf("object %d has no stream", objNum)
		}
		off, _ := o["streamOff"].(int64)
		ln, _ := o["streamLen"].(int64)
		raw, err := d.Range(off, ln)
		if err != nil {
			return nil, err
		}
		dict, _ := o["dict"].(string)
		filters, _ := o["filters"].([]any)
		predictor, columns, colors, bits := parseDecodeParms(dict)

		decoded := append([]byte(nil), raw...)
		applied := []string{}
		var runErr error
		for _, f := range filters {
			name, _ := f.(string)
			decoded, runErr = ApplyFilter(name, decoded)
			if runErr != nil {
				return map[string]any{
					"objNum":  objNum,
					"raw":     raw,
					"decoded": nil,
					"error":   runErr.Error(),
					"applied": applied,
					"filters": filters,
				}, nil
			}
			applied = append(applied, name)
		}
		if predictor > 1 {
			decoded, runErr = ApplyPredictor(decoded, predictor, columns, colors, bits)
			if runErr != nil {
				return map[string]any{
					"objNum":  objNum,
					"raw":     raw,
					"decoded": nil,
					"error":   runErr.Error(),
					"applied": applied,
					"filters": filters,
				}, nil
			}
			applied = append(applied, fmt.Sprintf("predictor=%d", predictor))
		}
		return map[string]any{
			"objNum":  objNum,
			"raw":     raw,
			"decoded": decoded,
			"applied": applied,
			"filters": filters,
		}, nil
	}
	return nil, fmt.Errorf("object %d not found", objNum)
}
