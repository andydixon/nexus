package vncclient

import (
	"net/http"
	"testing"
)

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

func TestAllowProxyOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "missing origin", origin: "", want: true},
		{name: "wails origin", origin: "wails://wails", want: true},
		{name: "localhost http", origin: "http://localhost:34115", want: true},
		{name: "loopback ipv4", origin: "http://127.0.0.1:34115", want: true},
		{name: "loopback ipv6", origin: "http://[::1]:34115", want: true},
		{name: "remote http", origin: "http://example.com", want: false},
		{name: "lookalike host", origin: "http://127.0.0.1.example.com", want: false},
		{name: "bad origin", origin: "://bad", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/ws", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := allowProxyOrigin(req); got != tt.want {
				t.Fatalf("allowProxyOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}
