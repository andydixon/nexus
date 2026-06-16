package pdf

import (
	"bytes"
	"regexp"
	"strings"
)

// SuspiciousHit marks a dictionary fragment worth a closer look. It is
// surfaced to the UI with its containing object number so a user can
// jump straight to it.
type SuspiciousHit struct {
	Kind   string `json:"kind"`
	ObjNum int64  `json:"objNum"`
	Gen    int64  `json:"gen"`
	Offset int64  `json:"offset"`
	Detail string `json:"detail,omitempty"`
}

// Surface walks the enumerated object list and flags anything routinely
// abused by hostile PDFs: embedded files, JavaScript, launch actions,
// URI actions, form-submission targets, OpenAction / AA triggers.
func Surface(objects []Object) []SuspiciousHit {
	hits := []SuspiciousHit{}
	for _, o := range objects {
		dict := o.Dict
		push := func(kind, detail string) {
			hits = append(hits, SuspiciousHit{
				Kind: kind, ObjNum: o.Num, Gen: o.Gen,
				Offset: o.Offset, Detail: detail,
			})
		}
		// Key markers. Order matters only for readability of the UI.
		if containsName(dict, "JavaScript") || containsName(dict, "JS") {
			push("javascript", firstLine(dict, 240))
		}
		if containsName(dict, "EmbeddedFile") || containsName(dict, "Filespec") {
			push("embedded-file", extractName(dict, "/F"))
		}
		if containsName(dict, "Launch") {
			push("launch-action", extractLaunchTarget(dict))
		}
		if containsName(dict, "URI") {
			if uri := extractURI(dict); uri != "" {
				push("uri-action", uri)
			}
		}
		if containsName(dict, "SubmitForm") {
			push("submit-form", "")
		}
		if containsName(dict, "OpenAction") {
			push("open-action", "")
		}
		if strings.Contains(dict, "/AA") {
			push("additional-action", "")
		}
		if strings.Contains(dict, "/Type /XFA") || strings.Contains(dict, "/XFA ") {
			push("xfa-form", "")
		}
		if strings.Contains(dict, "/RichMedia") {
			push("rich-media", "")
		}
		if strings.Contains(dict, "/Type /Metadata") && strings.Contains(dict, "/Subtype /XML") {
			push("xmp-metadata", "")
		}
	}
	return hits
}

func containsName(dict, name string) bool {
	// Match `/Name` as a whole token — not inside /Names/ or /FooJavaScript.
	pat := regexp.MustCompile(`/` + regexp.QuoteMeta(name) + `\b`)
	return pat.MatchString(dict)
}

// Go's regexp caps bounded repetitions at 1000 — do not raise this.
var uriRE = regexp.MustCompile(`/URI\s*\(([^)]{1,1000})\)`)

func extractURI(dict string) string {
	m := uriRE.FindStringSubmatch(dict)
	if m == nil {
		return ""
	}
	return m[1]
}

var fnameRE = regexp.MustCompile(`/F\s*\(([^)]{1,512})\)`)

func extractName(dict, key string) string {
	_ = key
	m := fnameRE.FindStringSubmatch(dict)
	if m == nil {
		return ""
	}
	return m[1]
}

var launchRE = regexp.MustCompile(`/F\s*\(([^)]{1,512})\)`)

func extractLaunchTarget(dict string) string {
	m := launchRE.FindStringSubmatch(dict)
	if m == nil {
		return ""
	}
	return m[1]
}

func firstLine(s string, max int) string {
	nl := strings.IndexByte(s, '\n')
	if nl > 0 {
		s = s[:nl]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// HasLinearisation reports whether a PDF is linearised (Fast Web View).
// A linearised PDF has an obj dict near the start containing /Linearized.
func HasLinearisation(whole []byte) bool {
	n := 4096
	if n > len(whole) {
		n = len(whole)
	}
	return bytes.Contains(whole[:n], []byte("/Linearized"))
}
