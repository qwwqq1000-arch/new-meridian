package main

import (
	"regexp"
	"strings"
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
