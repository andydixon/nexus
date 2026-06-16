package githubclient

import (
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
