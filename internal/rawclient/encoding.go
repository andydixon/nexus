package rawclient

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// ParseHexString converts a hex string (with optional spaces and 0x prefixes)
// to raw bytes.
//
// Accepted formats:
//
//	"48 65 6c 6c 6f"
//	"48656c6c6f"
//	"0x48 0x65 0x6c 0x6c 0x6f"
func ParseHexString(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty hex string")
	}

	// Strip 0x/0X prefixes.
	s = strings.ReplaceAll(s, "0x", "")
	s = strings.ReplaceAll(s, "0X", "")

	// Collect only hex digit characters.
	var cleaned strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if !isHexDigit(r) {
			return nil, fmt.Errorf("invalid character in hex string: %q", r)
		}
		cleaned.WriteRune(r)
	}

	if cleaned.Len()%2 != 0 {
		return nil, fmt.Errorf("hex string has odd number of digits")
	}

	return hex.DecodeString(cleaned.String())
}

// FormatHex formats bytes as a space-separated hex string.
// Example output: "48 65 6c 6c 6f"
func FormatHex(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	parts := make([]string, len(data))
	for i, b := range data {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, " ")
}

// FormatHexDump formats bytes as a traditional hex dump with offset, hex,
// and ASCII columns. Each line shows 16 bytes.
//
// Example output:
//
//	00000000  48 65 6c 6c 6f 20 57 6f  72 6c 64 21              |Hello World!    |
func FormatHexDump(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var sb strings.Builder
	for offset := 0; offset < len(data); offset += 16 {
		fmt.Fprintf(&sb, "%08x  ", offset)

		end := offset + 16
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]

		// Hex columns (two groups of eight bytes).
		for i := 0; i < 16; i++ {
			if i == 8 {
				sb.WriteByte(' ')
			}
			if i < len(chunk) {
				fmt.Fprintf(&sb, "%02x ", chunk[i])
			} else {
				sb.WriteString("   ")
			}
		}

		// ASCII column.
		sb.WriteString(" |")
		for _, b := range chunk {
			if b >= 0x20 && b <= 0x7e {
				sb.WriteByte(b)
			} else {
				sb.WriteByte('.')
			}
		}
		for i := len(chunk); i < 16; i++ {
			sb.WriteByte(' ')
		}
		sb.WriteString("|\n")
	}
	return sb.String()
}

// ParseEscaped processes C-style escape sequences in a string.
// Supported sequences: \r \n \t \\ \0 \xNN
func ParseEscaped(s string) ([]byte, error) {
	var result []byte
	i := 0
	for i < len(s) {
		if s[i] != '\\' {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size <= 1 {
				result = append(result, s[i])
				i++
			} else {
				result = append(result, s[i:i+size]...)
				i += size
			}
			continue
		}

		if i+1 >= len(s) {
			return nil, fmt.Errorf("trailing backslash at position %d", i)
		}

		next := s[i+1]
		switch next {
		case 'r':
			result = append(result, '\r')
			i += 2
		case 'n':
			result = append(result, '\n')
			i += 2
		case 't':
			result = append(result, '\t')
			i += 2
		case '\\':
			result = append(result, '\\')
			i += 2
		case '0':
			result = append(result, 0)
			i += 2
		case 'x', 'X':
			if i+3 >= len(s) {
				return nil, fmt.Errorf("incomplete \\x escape at position %d", i)
			}
			hexStr := s[i+2 : i+4]
			b, err := strconv.ParseUint(hexStr, 16, 8)
			if err != nil {
				return nil, fmt.Errorf("invalid \\x escape at position %d: %s", i, hexStr)
			}
			result = append(result, byte(b))
			i += 4
		default:
			return nil, fmt.Errorf("unknown escape sequence \\%c at position %d", next, i)
		}
	}
	return result, nil
}

// ToDisplayEncoding converts raw bytes to a display string using the
// specified encoding. Supported encodings: "utf-8" (default), "ascii",
// "iso-8859-1" (also "latin1").
func ToDisplayEncoding(data []byte, encoding string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "ascii":
		return toASCII(data)
	case "iso-8859-1", "latin1", "latin-1":
		return toLatin1(data)
	default:
		return toUTF8(data)
	}
}

// toASCII replaces non-ASCII bytes with '.', preserving common control chars.
func toASCII(data []byte) string {
	var sb strings.Builder
	sb.Grow(len(data))
	for _, b := range data {
		if b >= 0x20 && b <= 0x7e {
			sb.WriteByte(b)
		} else if b == '\n' || b == '\r' || b == '\t' {
			sb.WriteByte(b)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

// toLatin1 decodes bytes as ISO 8859-1.
func toLatin1(data []byte) string {
	decoded, err := charmap.ISO8859_1.NewDecoder().Bytes(data)
	if err != nil {
		return string(data)
	}
	return string(decoded)
}

// toUTF8 ensures the output is valid UTF-8, replacing invalid bytes with
// the Unicode replacement character.
func toUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var sb strings.Builder
	sb.Grow(len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		sb.WriteRune(r)
		data = data[size:]
	}
	return sb.String()
}

// isHexDigit reports whether r is a valid hexadecimal digit.
func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
