package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var warmupKick = make(chan struct{}, 1)

func warmupLog(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[native-egress] "+format+"\n", args...)
}

// TriggerWarmup wakes up the warmup loop to retry immediately.
func TriggerWarmup() {
	select {
	case warmupKick <- struct{}{}:
	default:
	}
}

// warmupLoop runs warmupTemplate on startup, then retries every 30s if it
// failed (e.g. account not yet pushed). Once successful, re-runs every 10min
// to keep the template fresh (in case CLI is upgraded).
func warmupLoop(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) {
	attempt := 0
	for {
		attempt++
		ok := warmupTemplate(claudePath, configDir, fpCache, btCache)
		if ok {
			warmupLog("warmup: SUCCESS (attempt #%d) — fingerprint + body template captured from real CLI", attempt)
			break
		}
		warmupLog("warmup: FAILED (attempt #%d) — will retry in 30s (POST /warmup to retry now)", attempt)
		select {
		case <-warmupKick:
			warmupLog("warmup: kick received, retrying immediately")
		case <-time.After(30 * time.Second):
		}
	}

	// Periodic refresh: re-capture every 10 minutes to pick up CLI upgrades
	for {
		select {
		case <-warmupKick:
			warmupLog("warmup: kick received, refreshing")
		case <-time.After(10 * time.Minute):
		}
		attempt++
		ok := warmupTemplate(claudePath, configDir, fpCache, btCache)
		if ok {
			warmupLog("warmup: refresh SUCCESS (attempt #%d)", attempt)
		} else {
			warmupLog("warmup: refresh FAILED (attempt #%d) — keeping previous template", attempt)
		}
	}
}

// warmupTemplate intercepts a real CC CLI request via a local dump server to
// capture both fingerprint (headers) and body template in one shot. No real
// API call is made — ANTHROPIC_BASE_URL is pointed at the local server.
// Returns true on success.
func warmupTemplate(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) bool {
	start := time.Now()

	fp, bodyData := captureAll(claudePath, configDir)
	if fp == nil {
		warmupLog("warmup: capture failed (CC not logged in?)")
		return false
	}

	fpCache.mu.Lock()
	fpCache.entries["default"] = fpEntry{fp: fp, capturedAt: time.Now()}
	fpCache.mu.Unlock()

	fpVersion := ExtractVersionFromUA(fp["user-agent"])
	fpBetas := fp["anthropic-beta"]
	fpNodeVer := fp["x-stainless-runtime-version"]
	warmupLog("warmup: fingerprint learned (CC %s, node %s)", fpVersion, fpNodeVer)

	if len(bodyData) == 0 {
		warmupLog("warmup: body capture returned empty")
		return false
	}

	btCache.LearnFromCC(bodyData, fpVersion, fpBetas, fpNodeVer)
	warmupLog("warmup: body template learned (%d bytes, %d tools) in %s",
		len(bodyData), countTemplateTools(bodyData), time.Since(start).Round(time.Millisecond))
	return true
}

// captureAll runs `claude -p hi` with ANTHROPIC_BASE_URL pointing at a local
// HTTP server, capturing both the request headers (→ fingerprint) and the
// request body (→ template) from the same single request.
func captureAll(claudePath, configDir string) (Fingerprint, []byte) {
	var mu sync.Mutex
	var capturedBody []byte
	var capturedFP Fingerprint

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		if len(body) > len(capturedBody) {
			capturedBody = body
			// Extract fingerprint from request headers
			fp := Fingerprint{}
			for k, vals := range r.Header {
				kl := strings.ToLower(k)
				if excluded[kl] || len(vals) == 0 {
					continue
				}
				fp[kl] = vals[0]
			}
			if ua := fp["user-agent"]; ua != "" && strings.HasPrefix(ua, "claude-cli/") {
				capturedFP = fp
			}
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"msg_warmup","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}`))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		warmupLog("warmup: listen error: %v", err)
		return nil, nil
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
	return capturedFP, capturedBody
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
