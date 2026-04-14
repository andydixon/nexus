package rawclient

import (
	"bytes"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseHexString
// ---------------------------------------------------------------------------

func TestParseHexString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []byte
		wantErr bool
	}{
		{
			name:  "contiguous hex for Hello",
			input: "48656c6c6f",
			want:  []byte("Hello"),
		},
		{
			name:  "space-separated hex",
			input: "48 65 6c 6c 6f",
			want:  []byte("Hello"),
		},
		{
			name:  "0x prefix",
			input: "0x48 0x65 0x6c 0x6c 0x6f",
			want:  []byte("Hello"),
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid characters",
			input:   "ZZZZ",
			wantErr: true,
		},
		{
			name:  "null bytes",
			input: "0000",
			want:  []byte{0x00, 0x00},
		},
		{
			name:  "0xff bytes",
			input: "ff ff",
			want:  []byte{0xff, 0xff},
		},
		{
			name:    "odd number of digits",
			input:   "abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHexString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseHexString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !bytes.Equal(got, tt.want) {
				t.Errorf("ParseHexString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ParseEscaped
// ---------------------------------------------------------------------------

func TestParseEscaped(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []byte
		wantErr bool
	}{
		{name: "plain text", input: "hello", want: []byte("hello")},
		{name: "newline escape", input: `\n`, want: []byte{'\n'}},
		{name: "carriage return + newline", input: `\r\n`, want: []byte{'\r', '\n'}},
		{name: "tab escape", input: `\t`, want: []byte{'\t'}},
		{name: "null byte", input: `\0`, want: []byte{0x00}},
		{name: "hex escape 0xff", input: `\xff`, want: []byte{0xff}},
		{name: "escaped backslash", input: `\\`, want: []byte{'\\'}},
		{name: "mixed AT command", input: `AT\r\n`, want: []byte{'A', 'T', '\r', '\n'}},
		{name: "hex escape 0x00", input: `\x00`, want: []byte{0x00}},
		{name: "trailing backslash", input: `hello\`, wantErr: true},
		{name: "unknown escape", input: `\q`, wantErr: true},
		{name: "incomplete hex escape", input: `\x0`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEscaped(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEscaped(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && !bytes.Equal(got, tt.want) {
				t.Errorf("ParseEscaped(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatHexDump
// ---------------------------------------------------------------------------

func TestFormatHexDump(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		got := FormatHexDump(nil)
		if got != "" {
			t.Errorf("FormatHexDump(nil) = %q, want empty", got)
		}
	})

	t.Run("small data", func(t *testing.T) {
		got := FormatHexDump([]byte("Hi"))
		if !strings.Contains(got, "48 69") {
			t.Errorf("expected hex for 'Hi', got:\n%s", got)
		}
		// Should contain the ASCII display column.
		if !strings.Contains(got, "|Hi") {
			t.Errorf("expected ASCII column with 'Hi', got:\n%s", got)
		}
	})

	t.Run("16+ bytes spans multiple lines", func(t *testing.T) {
		data := []byte("ABCDEFGHIJKLMNOPQRST") // 20 bytes
		got := FormatHexDump(data)
		lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
		if len(lines) < 2 {
			t.Errorf("expected at least 2 lines for 20 bytes, got %d", len(lines))
		}
		// First line should start with offset 00000000.
		if !strings.HasPrefix(lines[0], "00000000") {
			t.Errorf("first line should start with 00000000, got %q", lines[0])
		}
		// Second line should start with offset 00000010.
		if !strings.HasPrefix(lines[1], "00000010") {
			t.Errorf("second line should start with 00000010, got %q", lines[1])
		}
	})
}

// ---------------------------------------------------------------------------
// FormatHex
// ---------------------------------------------------------------------------

func TestFormatHex(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{name: "empty", input: nil, want: ""},
		{name: "single byte", input: []byte{0x42}, want: "42"},
		{name: "Hello", input: []byte("Hello"), want: "48 65 6c 6c 6f"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatHex(tt.input)
			if got != tt.want {
				t.Errorf("FormatHex() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ToDisplayEncoding
// ---------------------------------------------------------------------------

func TestToDisplayEncoding(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		encoding string
		contains string
	}{
		{
			name:     "utf-8 passthrough",
			data:     []byte("Hello, World!"),
			encoding: "utf-8",
			contains: "Hello, World!",
		},
		{
			name:     "ascii replaces non-printable",
			data:     []byte{0x80, 0x41},
			encoding: "ascii",
			contains: ".A",
		},
		{
			name:     "latin-1 decodes high bytes",
			data:     []byte{0xe9}, // é in ISO 8859-1
			encoding: "latin-1",
			contains: "é",
		},
		{
			name:     "iso-8859-1 alias",
			data:     []byte{0xe9},
			encoding: "iso-8859-1",
			contains: "é",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToDisplayEncoding(tt.data, tt.encoding)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("ToDisplayEncoding() = %q, expected to contain %q", got, tt.contains)
			}
		})
	}
}
