package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

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
	bodyTmpl := NewBodyTemplateCache(10 * time.Minute)
	transport := NewUTLSTransport()

	go warmupLoop(claudePath, configDir, fpCache, bodyTmpl)

	deps := RelayDeps{
		Transport:    transport,
		FP:           fpCache,
		BodyTemplate: bodyTmpl,
		SessionID:    deriveSessionID,
		Now:          time.Now,
		Datadog:      NewDatadogEmitter(transport, bodyTmpl, fpCache),
		PrevReq:      NewPrevReqStore(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/warmup", func(w http.ResponseWriter, _ *http.Request) {
		TriggerWarmup()
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"message":"warmup triggered"}`))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		hasFP := false
		fpVersion := ""
		fpCache.mu.Lock()
		if e, ok := fpCache.entries["default"]; ok && len(e.fp) > 0 {
			hasFP = true
			fpVersion = ExtractVersionFromUA(e.fp["user-agent"])
		}
		fpCache.mu.Unlock()
		hasBT := false
		btTools := 0
		if tmpl := bodyTmpl.Get(); tmpl != nil {
			hasBT = true
			btTools = len(tmpl.Tools)
		}
		fmt.Fprintf(w, `{"fingerprint":%v,"fpVersion":"%s","bodyTemplate":%v,"btTools":%d}`, hasFP, fpVersion, hasBT, btTools)
	})
	mux.HandleFunc("/relay", relayHandler(deps))
	return mux
}

// resolveAddr determines the listen address. Priority:
//  1. NATIVE_EGRESS_ADDR env var (full addr override)
//  2. --port flag (binds 127.0.0.1:<port>)
//  3. default: 127.0.0.1:0 (random port, only useful in tests)
func resolveAddr(port int, envAddr string) string {
	if envAddr != "" {
		return envAddr
	}
	if port != 0 {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	return "127.0.0.1:0"
}

func main() {
	port := flag.Int("port", 0, "port to listen on (overridden by NATIVE_EGRESS_ADDR)")
	flag.Parse()

	addr := resolveAddr(*port, os.Getenv("NATIVE_EGRESS_ADDR"))
	fmt.Fprintf(os.Stderr, "native-egress listening on %s\n", addr)
	if err := http.ListenAndServe(addr, newServer()); err != nil {
		fmt.Fprintln(os.Stderr, "native-egress exited:", err)
		os.Exit(1)
	}
}
