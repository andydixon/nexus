// Package patch implements the simple Leviathan patch script language.
//
//	set OFFSET HEX...         — write hex bytes at OFFSET
//	fill START LEN BYTE       — fill a range with a single byte
//	xor START LEN KEY         — XOR a range with an ASCII or hex: key
//	copy SRC LEN DST          — copy bytes from SRC to DST
//	# comments and blank lines are allowed.
package patch

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// Step is one parsed instruction.
type Step struct {
	Op     string
	Args   []string
	Line   int
	Source string
}

// Parse parses the script into steps.
func Parse(src string) ([]Step, error) {
	out := []Step{}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		op := strings.ToLower(fields[0])
		out = append(out, Step{
			Op: op, Args: fields[1:], Line: i + 1, Source: raw,
		})
	}
	return out, nil
}

// Plan validates all steps against document size without applying.
// Returns per-step info or an error on validation failure.
func Plan(d *document.Document, steps []Step) ([]map[string]any, error) {
	size := d.Size()
	info := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		switch s.Op {
		case "set":
			if len(s.Args) < 2 {
				return info, fmt.Errorf("line %d: set needs OFFSET and HEX", s.Line)
			}
			off, err := parseOffset(s.Args[0])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			b, err := parseHexRest(s.Args[1:])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			if !validRange(size, off, int64(len(b))) {
				return info, fmt.Errorf("line %d: out of range", s.Line)
			}
			info = append(info, map[string]any{
				"op": "set", "line": s.Line, "offset": off, "length": len(b),
			})
		case "fill":
			if len(s.Args) != 3 {
				return info, fmt.Errorf("line %d: fill needs START LEN BYTE", s.Line)
			}
			off, err := parseOffset(s.Args[0])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			n, err := parseOffset(s.Args[1])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			by, err := parseHexByte(s.Args[2])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			if !validRange(size, off, n) {
				return info, fmt.Errorf("line %d: out of range", s.Line)
			}
			info = append(info, map[string]any{
				"op": "fill", "line": s.Line, "offset": off, "length": n, "byte": by,
			})
		case "xor":
			if len(s.Args) != 3 {
				return info, fmt.Errorf("line %d: xor needs START LEN KEY", s.Line)
			}
			off, err := parseOffset(s.Args[0])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			n, err := parseOffset(s.Args[1])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			key, err := parseKey(s.Args[2])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			if !validRange(size, off, n) {
				return info, fmt.Errorf("line %d: out of range", s.Line)
			}
			info = append(info, map[string]any{
				"op": "xor", "line": s.Line, "offset": off, "length": n, "keyLen": len(key),
			})
		case "copy":
			if len(s.Args) != 3 {
				return info, fmt.Errorf("line %d: copy needs SRC LEN DST", s.Line)
			}
			src, err := parseOffset(s.Args[0])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			n, err := parseOffset(s.Args[1])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			dst, err := parseOffset(s.Args[2])
			if err != nil {
				return info, fmt.Errorf("line %d: %v", s.Line, err)
			}
			if !validRange(size, src, n) || !validRange(size, dst, n) {
				return info, fmt.Errorf("line %d: out of range", s.Line)
			}
			info = append(info, map[string]any{
				"op": "copy", "line": s.Line, "src": src, "dst": dst, "length": n,
			})
		default:
			return info, fmt.Errorf("line %d: unknown op %q", s.Line, s.Op)
		}
	}
	return info, nil
}

// Apply runs the script against the document. Callers may want to
// first snapshot a checkpoint so apply can be reverted.
func Apply(d *document.Document, steps []Step) error {
	if _, err := Plan(d, steps); err != nil {
		return err
	}
	for _, s := range steps {
		switch s.Op {
		case "set":
			off, _ := parseOffset(s.Args[0])
			b, _ := parseHexRest(s.Args[1:])
			for i, bb := range b {
				if _, err := d.Set(off+int64(i), bb); err != nil {
					return err
				}
			}
		case "fill":
			off, _ := parseOffset(s.Args[0])
			n, _ := parseOffset(s.Args[1])
			by, _ := parseHexByte(s.Args[2])
			if err := d.Fill(off, n, by); err != nil {
				return err
			}
		case "xor":
			off, _ := parseOffset(s.Args[0])
			n, _ := parseOffset(s.Args[1])
			key, _ := parseKey(s.Args[2])
			if err := d.XOR(off, n, key); err != nil {
				return err
			}
		case "copy":
			src, _ := parseOffset(s.Args[0])
			n, _ := parseOffset(s.Args[1])
			dst, _ := parseOffset(s.Args[2])
			if err := d.Copy(src, n, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func validRange(size, start, length int64) bool {
	return start >= 0 && start <= size && length >= 0 && length <= size-start
}

func parseOffset(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseInt(s[2:], 16, 64)
		return v, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseHexRest(args []string) ([]byte, error) {
	joined := strings.Join(args, "")
	joined = strings.ReplaceAll(joined, " ", "")
	joined = strings.ReplaceAll(joined, ",", "")
	return hex.DecodeString(joined)
}

func parseHexByte(s string) (byte, error) {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, err := strconv.ParseUint(s[2:], 16, 8)
		return byte(v), err
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		v, err := strconv.ParseUint(s, 10, 8)
		if err != nil {
			return 0, errors.New("not a byte")
		}
		return byte(v), nil
	}
	if len(b) != 1 {
		return 0, errors.New("expected one byte")
	}
	return b[0], nil
}

func parseKey(s string) ([]byte, error) {
	if strings.HasPrefix(s, "hex:") {
		return hex.DecodeString(s[4:])
	}
	if s == "" {
		return nil, errors.New("empty key")
	}
	return []byte(s), nil
}
