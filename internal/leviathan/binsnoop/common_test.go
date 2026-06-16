package binsnoop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileLimitedRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firmware.bin")
	if err := os.WriteFile(path, []byte("abcd"), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	_, err := ReadFileLimited(path, 3)
	if err == nil {
		t.Fatal("expected oversized file to be rejected")
	}
	if !strings.Contains(err.Error(), "file is too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}
