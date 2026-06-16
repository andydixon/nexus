package githubclient

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadLimitedGitHubBodyRejectsOversizedResponse(t *testing.T) {
	_, err := readLimitedGitHubBody(strings.NewReader("abcd"), 3)
	if err == nil {
		t.Fatal("expected oversized response to be rejected")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadLimitedGitHubBodyAllowsResponseAtLimit(t *testing.T) {
	got, err := readLimitedGitHubBody(strings.NewReader("abc"), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("body = %q, want abc", string(got))
	}
}

func TestEndpointURLEscapesRepoPathSegments(t *testing.T) {
	client := New("https://ghe.example.com/api/v3", "")

	got, err := client.endpointURL("repos", "owner", "repo", "commits", "feature/a b")
	if err != nil {
		t.Fatalf("endpointURL() error = %v", err)
	}
	want := "https://ghe.example.com/api/v3/repos/owner/repo/commits/feature%2Fa%20b"
	if got != want {
		t.Fatalf("endpointURL() = %q, want %q", got, want)
	}
}

func TestSplitRepoFullNameRejectsExtraSegments(t *testing.T) {
	if _, _, err := splitRepoFullName("owner/repo/../../user"); err == nil {
		t.Fatal("expected malformed repository name to be rejected")
	}
}

func TestCopyLimitedArchiveRejectsOversizedStream(t *testing.T) {
	var dst bytes.Buffer
	n, err := copyLimitedArchive(&dst, strings.NewReader("abcd"), 3)
	if err == nil {
		t.Fatal("expected oversized archive stream to be rejected")
	}
	if n != 4 {
		t.Fatalf("copied = %d, want 4", n)
	}
	if !strings.Contains(err.Error(), "archive exceeds") {
		t.Fatalf("error = %q, want archive exceeds", err.Error())
	}
}

func TestCopyLimitedArchiveAllowsStreamAtLimit(t *testing.T) {
	var dst bytes.Buffer
	n, err := copyLimitedArchive(&dst, strings.NewReader("abc"), 3)
	if err != nil {
		t.Fatalf("copyLimitedArchive() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("copied = %d, want 3", n)
	}
	if got, err := io.ReadAll(&dst); err != nil || string(got) != "abc" {
		t.Fatalf("dst = %q, err = %v; want abc", string(got), err)
	}
}
