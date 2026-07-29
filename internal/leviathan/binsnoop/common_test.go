package binsnoop

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestCarveUsesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "private", "carved.bin")
	if err := Carve(path, []byte("secret"), 0, 6); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("carved file permissions = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("carve directory permissions = %04o, want 0700", got)
	}
}
