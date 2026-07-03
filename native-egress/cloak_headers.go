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

func BuildHeadersApiKey(fp Fingerprint, apiKey, sessionID, clientRequestID string, stream bool, clientBeta string) http.Header {
	h := BuildHeaders(fp, "", sessionID, clientRequestID, stream, clientBeta)
	h.Del("authorization")
	h.Set("x-api-key", apiKey)
	return h
}

func BuildHeaders(fp Fingerprint, token, sessionID, clientRequestID string, stream bool, clientBeta string) http.Header {
	h := http.Header{}
	for k, v := range fp {
		h.Set(k, v)
	}

	if beta := unionAnthropicBeta(h.Get("anthropic-beta"), clientBeta); beta != "" {
		h.Set("anthropic-beta", beta)
	}

	h.Set("authorization", "Bearer "+token)
	h.Set("content-type", "application/json")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-claude-code-session-id", sessionID)
	h.Set("x-client-request-id", clientRequestID)
	h.Set("accept", "application/json")

	return h
}
