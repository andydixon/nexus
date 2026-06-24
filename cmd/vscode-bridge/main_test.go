package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewBridgeHTTPServerSetsResourceLimits(t *testing.T) {
	server := newBridgeHTTPServer(http.NewServeMux())

	if server.ReadHeaderTimeout != bridgeReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, bridgeReadHeaderTimeout)
	}
	if server.ReadTimeout != bridgeReadTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", server.ReadTimeout, bridgeReadTimeout)
	}
	if server.IdleTimeout != bridgeIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.IdleTimeout, bridgeIdleTimeout)
	}
	if server.MaxHeaderBytes != bridgeMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, bridgeMaxHeaderBytes)
	}
}

func TestHandleRPCRejectsOversizedJSONBody(t *testing.T) {
	app := &bridgeApp{}
	largeBody := `{"method":"ListProfiles","args":[],"pad":"` + strings.Repeat("x", bridgeMaxJSONBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(largeBody))
	rec := httptest.NewRecorder()

	app.handleRPC(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !strings.Contains(resp.Error, "request body too large") {
		t.Fatalf("error = %q, want request body limit error", resp.Error)
	}
}

func TestHandleRuntimeEventRejectsOversizedJSONBody(t *testing.T) {
	app := &bridgeApp{}
	largeBody := `{"name":"session:secret:response","payload":{"pad":"` + strings.Repeat("x", bridgeMaxJSONBodyBytes) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/runtime/event", strings.NewReader(largeBody))
	rec := httptest.NewRecorder()

	app.handleRuntimeEvent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !strings.Contains(resp.Error, "request body too large") {
		t.Fatalf("error = %q, want request body limit error", resp.Error)
	}
}
