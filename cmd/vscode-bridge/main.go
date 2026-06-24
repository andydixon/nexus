// Command vscode-bridge exposes the Nexus backend to VS Code extensions over a
// localhost-only JSON-RPC and event stream.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/andydixon/nexus/internal/api"
	"github.com/andydixon/nexus/internal/audit"
	"github.com/andydixon/nexus/internal/config"
	"github.com/andydixon/nexus/internal/preferences"
	"github.com/andydixon/nexus/internal/recording"
	"github.com/andydixon/nexus/internal/secrets"
	"github.com/andydixon/nexus/internal/sessions"
	"github.com/andydixon/nexus/internal/storage"
)

type rpcRequest struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

type rpcResponse struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type runtimeEventRequest struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

const (
	bridgeReadHeaderTimeout = 5 * time.Second
	bridgeReadTimeout       = 15 * time.Second
	bridgeIdleTimeout       = 60 * time.Second
	bridgeMaxHeaderBytes    = 16 << 10
	bridgeMaxJSONBodyBytes  = 1 << 20
)

type eventBroker struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newEventBroker() *eventBroker {
	return &eventBroker{clients: make(map[chan []byte]struct{})}
}

func (b *eventBroker) publish(name string, payload any) {
	msg, err := json.Marshal(map[string]any{"name": name, "payload": payload})
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *eventBroker) subscribe() chan []byte {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *eventBroker) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	delete(b.clients, ch)
	close(ch)
	b.mu.Unlock()
}

type bridgeApp struct {
	service *api.Service
	manager *sessions.Manager
	store   *storage.Store
	secrets *secrets.SecretStore
	events  *eventBroker
}

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:0", "address for the localhost bridge")
	flag.Parse()

	bridge, err := newBridgeApp()
	if err != nil {
		log.Fatalf("initialise bridge: %v", err)
	}
	defer bridge.close()

	token, err := randomToken()
	if err != nil {
		log.Fatalf("create auth token: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", bridge.auth(token, bridge.handleHealth))
	mux.HandleFunc("/rpc", bridge.auth(token, bridge.handleRPC))
	mux.HandleFunc("/events", bridge.auth(token, bridge.handleEvents))
	mux.HandleFunc("/runtime/event", bridge.auth(token, bridge.handleRuntimeEvent))

	server := newBridgeHTTPServer(mux)
	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	fmt.Fprintf(os.Stdout, "{\"addr\":%q,\"token\":%q}\n", listener.Addr().String(), token)

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func newBridgeApp() (*bridgeApp, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.EnsureDirectories(); err != nil {
		return nil, err
	}
	store, err := storage.NewStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	secretStore := secrets.NewSecretStore(cfg.SecretPath)
	keychainStore := secrets.NewKeychainStore("NexusTerminal")
	auditLog := audit.NewLogger(store)
	prefs := preferences.NewManager(store)
	if err := prefs.Load(); err != nil {
		log.Printf("warning: failed to load preferences: %v", err)
	}

	recordStore := recording.NewStore(filepath.Join(cfg.DataDir, "recordings"))
	manager := sessions.NewManager(store, secretStore, keychainStore, auditLog, recordStore)
	service := api.NewService(store, secretStore, keychainStore, manager, auditLog, prefs)
	events := newEventBroker()
	manager.SetEventSink(events.publish)
	service.SetEventSink(events.publish)

	return &bridgeApp{
		service: service,
		manager: manager,
		store:   store,
		secrets: secretStore,
		events:  events,
	}, nil
}

func (a *bridgeApp) close() {
	if a.manager != nil {
		a.manager.CloseAll()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	if a.secrets != nil {
		a.secrets.Lock()
	}
}

func (a *bridgeApp) auth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (a *bridgeApp) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"ok": true})
}

func (a *bridgeApp) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rpcRequest
	if err := decodeLimitedJSON(w, r, &req); err != nil {
		writeJSON(w, rpcResponse{Error: err.Error()})
		return
	}
	result, err := callService(a.service, req.Method, req.Args)
	if err != nil {
		writeJSON(w, rpcResponse{Error: err.Error()})
		return
	}
	writeJSON(w, rpcResponse{Result: result})
}

func (a *bridgeApp) handleRuntimeEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runtimeEventRequest
	if err := decodeLimitedJSON(w, r, &req); err != nil {
		writeJSON(w, rpcResponse{Error: err.Error()})
		return
	}
	switch req.Name {
	case "session:secret:response":
		requestID, _ := req.Payload["requestID"].(string)
		value, _ := req.Payload["value"].(string)
		cancelled, _ := req.Payload["cancelled"].(bool)
		a.service.RespondSecret(requestID, value, cancelled)
		a.manager.RespondSecret(requestID, value)
	case "session:hostkey:response":
		requestID, _ := req.Payload["requestID"].(string)
		accepted, _ := req.Payload["accepted"].(bool)
		a.manager.RespondHostKey(requestID, accepted)
	default:
		writeJSON(w, rpcResponse{Error: "unsupported runtime event"})
		return
	}
	writeJSON(w, rpcResponse{Result: true})
}

func (a *bridgeApp) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := a.events.subscribe()
	defer a.events.unsubscribe(ch)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func callService(service *api.Service, methodName string, rawArgs []json.RawMessage) (any, error) {
	if strings.TrimSpace(methodName) == "" {
		return nil, errors.New("method is required")
	}
	method := reflect.ValueOf(service).MethodByName(methodName)
	if !method.IsValid() {
		return nil, fmt.Errorf("unknown service method %q", methodName)
	}
	mt := method.Type()
	if len(rawArgs) != mt.NumIn() {
		return nil, fmt.Errorf("%s expects %d args, got %d", methodName, mt.NumIn(), len(rawArgs))
	}

	args := make([]reflect.Value, mt.NumIn())
	for i := 0; i < mt.NumIn(); i++ {
		target := reflect.New(mt.In(i))
		if err := json.Unmarshal(rawArgs[i], target.Interface()); err != nil {
			return nil, fmt.Errorf("arg %d: %w", i, err)
		}
		args[i] = target.Elem()
	}

	values := method.Call(args)
	if len(values) == 0 {
		return true, nil
	}
	last := values[len(values)-1]
	if last.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) && !last.IsNil() {
		return nil, last.Interface().(error)
	}
	if len(values) == 1 {
		if last.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			return true, nil
		}
		return last.Interface(), nil
	}
	return values[0].Interface(), nil
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func newBridgeHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: bridgeReadHeaderTimeout,
		ReadTimeout:       bridgeReadTimeout,
		IdleTimeout:       bridgeIdleTimeout,
		MaxHeaderBytes:    bridgeMaxHeaderBytes,
	}
}

func decodeLimitedJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, bridgeMaxJSONBodyBytes)
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
