package vncclient

import "testing"

func TestNewProxyTokenIsRandomHex(t *testing.T) {
	first, err := newProxyToken()
	if err != nil {
		t.Fatalf("newProxyToken() error = %v", err)
	}
	second, err := newProxyToken()
	if err != nil {
		t.Fatalf("newProxyToken() second error = %v", err)
	}

	if len(first) != 32 {
		t.Fatalf("token length = %d, want 32", len(first))
	}
	if first == second {
		t.Fatal("expected separate tokens to differ")
	}
	for _, ch := range first {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			t.Fatalf("token contains non-hex character %q", ch)
		}
	}
}
