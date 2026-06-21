package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Fingerprint map[string]string

var excluded = map[string]bool{
	"authorization": true, "x-claude-code-session-id": true,
	"x-stainless-retry-count": true, "content-length": true,
	"host": true, "connection": true, "accept-encoding": true,
}

var (
	headersBlockRe = regexp.MustCompile(`(?s)headers:\s*\{(.*?)\}`)
	headerPairRe   = regexp.MustCompile(`(?:"([^"\n]+)"|([A-Za-z0-9-]+))\s*:\s*"([^"]*)"`)
)

func osEnviron() []string { return os.Environ() }

// ParseFingerprint extracts the complete header set from ANTHROPIC_LOG=debug
// output, dropping per-request/transport headers. ok=false unless a genuine
// claude-cli user-agent is present.
func ParseFingerprint(debugLog string) (Fingerprint, bool) {
	block := headersBlockRe.FindStringSubmatch(debugLog)
	if block == nil {
		return nil, false
	}
	fp := Fingerprint{}
	for _, m := range headerPairRe.FindAllStringSubmatch(block[1], -1) {
		key := strings.ToLower(m[1])
		if key == "" {
			key = strings.ToLower(m[2])
		}
		if key == "" || excluded[key] {
			continue
		}
		fp[key] = m[3]
	}
	if ua := fp["user-agent"]; ua == "" || !strings.HasPrefix(ua, "claude-cli/") {
		return nil, false
	}
	return fp, true
}

type fpEntry struct {
	fp         Fingerprint
	capturedAt time.Time
}

type FPCache struct {
	ttl     time.Duration
	capture func(configDir string) (string, error)
	mu      sync.Mutex
	entries map[string]fpEntry
}

func NewFPCache(ttl time.Duration, capture func(string) (string, error)) *FPCache {
	return &FPCache{ttl: ttl, capture: capture, entries: map[string]fpEntry{}}
}

func (c *FPCache) Get(account, configDir string, now time.Time) (Fingerprint, bool) {
	c.mu.Lock()
	if e, ok := c.entries[account]; ok && now.Sub(e.capturedAt) <= c.ttl {
		c.mu.Unlock()
		return e.fp, true
	}
	c.mu.Unlock()

	log, err := c.capture(configDir)
	if err != nil {
		return nil, false
	}
	fp, ok := ParseFingerprint(log)
	if !ok {
		return nil, false
	}
	c.mu.Lock()
	c.entries[account] = fpEntry{fp: fp, capturedAt: now}
	c.mu.Unlock()
	return fp, true
}

// defaultCapture returns a capture func that runs the real CLI with
// ANTHROPIC_LOG=debug to surface its outgoing headers.
func defaultCapture(claudePath, configDir string) func(string) (string, error) {
	return func(string) (string, error) {
		cmd := exec.Command(claudePath, "-p", "hi")
		cmd.Env = append(append([]string{}, osEnviron()...),
			"ANTHROPIC_LOG=debug", "CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir))
		out, _ := cmd.CombinedOutput() // headers are logged before any non-2xx; ignore exit code
		return string(out), nil
	}
}
