package main

import (
	"net/http"
	"strings"
)

// unionAnthropicBeta merges two comma-separated anthropic-beta lists, preserving
// order and dropping duplicates.
func unionAnthropicBeta(lists ...string) string {
	seen := map[string]bool{}
	out := make([]string, 0, 8)
	for _, list := range lists {
		for _, p := range strings.Split(list, ",") {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return strings.Join(out, ",")
}

// modelBetaFlags returns the beta flags that real CC sends per model.
// Captured from real CC 2.1.198 on Linux via dump server.
//
// Rules from cross-comparison:
//   - haiku: no effort, no mid-conversation
//   - old models (sonnet-4-6, opus-4-6): has effort, no mid-conversation
//   - new models (opus-4-8, sonnet-5, fable-5): has effort + mid-conversation
func modelBetaFlags(model string) string {
	base := "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14," +
		"thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05," +
		"advisor-tool-2026-03-01,extended-cache-ttl-2025-04-11"

	if strings.Contains(model, "haiku") {
		return base
	}

	base += ",effort-2025-11-24"

	if isNewModel(model) {
		base += ",mid-conversation-system-2026-04-07"
	}

	return base
}

func BuildHeadersApiKey(fp Fingerprint, apiKey, sessionID string, stream bool, model string, clientBeta string) http.Header {
	h := BuildHeaders(fp, "", sessionID, stream, model, clientBeta)
	h.Del("authorization")
	h.Set("x-api-key", apiKey)
	return h
}

func BuildHeaders(fp Fingerprint, token, sessionID string, stream bool, model string, clientBeta string) http.Header {
	h := http.Header{}
	for k, v := range fp {
		h.Set(k, v)
	}

	// Override beta flags per model (FP captures a generic set from warmup)
	baseBeta := modelBetaFlags(model)
	if beta := unionAnthropicBeta(baseBeta, clientBeta); beta != "" {
		h.Set("anthropic-beta", beta)
	}

	h.Set("authorization", "Bearer "+token)
	h.Set("content-type", "application/json")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-claude-code-session-id", sessionID)
	h.Set("accept", "application/json")

	// Real CC: stream request → timeout=600, non-stream → timeout=300
	if stream {
		h.Set("x-stainless-timeout", "600")
	} else {
		h.Set("x-stainless-timeout", "300")
	}

	return h
}
