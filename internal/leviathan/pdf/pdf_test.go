package pdf

import (
	"os"
	"strings"
	"testing"

	"github.com/andydixon/link/internal/leviathan/document"
)

func TestParseRejectsOversizedPDFBeforeWholeRange(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "huge-*.pdf")
	if err != nil {
		t.Fatalf("CreateTemp() failed: %v", err)
	}
	if _, err := file.WriteString("%PDF-1.7\n"); err != nil {
		t.Fatalf("WriteString() failed: %v", err)
	}
	if err := file.Truncate(maxPDFParseBytes + 1); err != nil {
		t.Fatalf("Truncate() failed: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	doc, err := document.Open(file.Name())
	if err != nil {
		t.Fatalf("document.Open() failed: %v", err)
	}
	_, err = Parse(doc)
	if err == nil {
		t.Fatal("expected oversized PDF to be rejected")
	}
	if !strings.Contains(err.Error(), "pdf parse skipped") {
		t.Fatalf("unexpected error: %v", err)
	}
}
