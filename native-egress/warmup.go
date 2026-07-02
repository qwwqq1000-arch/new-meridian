package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

func warmupLog(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[native-egress] "+format+"\n", args...)
}

func warmupTemplate(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) {
	start := time.Now()

	// Step 1: fingerprint — run with ANTHROPIC_LOG=debug to capture headers
	cmd := exec.Command(claudePath, "-p", "hi")
	cmd.Env = append(append([]string{}, osEnviron()...),
		"ANTHROPIC_LOG=debug",
		"CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir),
	)

	warmupLog("warmup: running %s -p hi ...", claudePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		warmupLog("warmup: claude exited with error: %v (output: %d bytes)", err, len(out))
	}

	fp, ok := ParseFingerprint(string(out))
	if !ok {
		warmupLog("warmup: fingerprint parse failed (CC not logged in?)")
		return
	}

	fpCache.mu.Lock()
	fpCache.entries["default"] = fpEntry{fp: fp, capturedAt: time.Now()}
	fpCache.mu.Unlock()

	fpVersion := ExtractVersionFromUA(fp["user-agent"])
	fpBetas := fp["anthropic-beta"]
	fpNodeVer := fp["x-stainless-runtime-version"]
	warmupLog("warmup: fingerprint learned (CC %s, node %s)", fpVersion, fpNodeVer)

	// Step 2: body — intercept via local dump server
	bodyData := captureBody(claudePath, configDir)
	if len(bodyData) == 0 {
		warmupLog("warmup: body capture returned empty")
		return
	}

	btCache.LearnFromCC(bodyData, fpVersion, fpBetas, fpNodeVer)
	warmupLog("warmup: body template learned (%d bytes, %d tools) in %s",
		len(bodyData), countTemplateTools(bodyData), time.Since(start).Round(time.Millisecond))
}

func captureBody(claudePath, configDir string) []byte {
	var mu sync.Mutex
	var captured []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if len(body) > len(captured) {
			captured = body
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg_warmup","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		warmupLog("warmup: listen error: %v", err)
		return nil
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()
	warmupLog("warmup: capture server on %s", addr)

	cmd := exec.Command(claudePath, "-p", "hi")
	cmd.Env = append(append([]string{}, osEnviron()...),
		"ANTHROPIC_BASE_URL=http://"+addr,
		"CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir),
	)
	cmd.CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	return captured
}

func countTemplateTools(body []byte) int {
	var parsed struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		return len(parsed.Tools)
	}
	return -1
}
