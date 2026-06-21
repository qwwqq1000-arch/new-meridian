package main

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// sessionStore provides stable per-account session IDs (generated once, reused).
var sessionStore = struct {
	mu  sync.Mutex
	ids map[string]string
}{ids: map[string]string{}}

func stableSessionID(account string) string {
	sessionStore.mu.Lock()
	defer sessionStore.mu.Unlock()
	if id, ok := sessionStore.ids[account]; ok {
		return id
	}
	id := uuid.NewString()
	sessionStore.ids[account] = id
	return id
}

func newServer() http.Handler {
	claudePath := os.Getenv("CLAUDE_PATH")
	if claudePath == "" {
		claudePath = "claude"
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = home + "/.claude"
	}

	fpCache := NewFPCache(10*time.Minute, defaultCapture(claudePath, configDir))

	deps := RelayDeps{
		Transport: NewUTLSTransport(),
		FP:        fpCache,
		SessionID: stableSessionID,
		Now:       time.Now,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/relay", relayHandler(deps))
	return mux
}

func main() {
	addr := os.Getenv("NATIVE_EGRESS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	fmt.Fprintf(os.Stderr, "native-egress listening on %s\n", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		fmt.Fprintln(os.Stderr, "native-egress exited:", err)
		os.Exit(1)
	}
}
