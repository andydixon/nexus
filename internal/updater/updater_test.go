package updater

import (
	"strings"
	"testing"
)

func TestNormaliseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{" v0.5.0 ", "0.5.0"},
		{"dev", "dev"},
	}
	for _, tt := range tests {
		got := normaliseVersion(tt.input)
		if got != tt.want {
			t.Errorf("normaliseVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{"1.1.0", "1.0.0", true},
		{"2.0.0", "1.9.9", true},
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"1.0.0", "dev", false},    // dev builds ignored
		{"1.0.0", "", false},       // empty current ignored
		{"1.2", "1.1.0", true},     // fewer parts
		{"1.0.0", "1.0.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.latest+" vs "+tt.current, func(t *testing.T) {
			got := isNewer(tt.latest, tt.current)
			if got != tt.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestTruncateNotes(t *testing.T) {
	short := "Short note"
	if got := truncateNotes(short, 100); got != short {
		t.Errorf("expected %q, got %q", short, got)
	}

	long := strings.Repeat("x", 600)
	got := truncateNotes(long, 500)
	if !strings.HasSuffix(got, "…") {
		t.Error("expected truncated notes to end with ellipsis")
	}
	if len([]rune(got)) != 501 { // 500 runes + "…"
		t.Errorf("expected 501 runes, got %d", len([]rune(got)))
	}
}
