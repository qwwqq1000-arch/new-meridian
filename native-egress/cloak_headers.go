package main

import "net/http"

func BuildHeaders(fp Fingerprint, token, sessionID, clientRequestID string, stream bool) http.Header {
	h := http.Header{}
	for k, v := range fp {
		h.Set(k, v)
	}
	h.Set("authorization", "Bearer "+token)
	h.Set("content-type", "application/json")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-claude-code-session-id", sessionID)
	h.Set("x-client-request-id", clientRequestID)
	h.Set("connection", "keep-alive")
	if stream {
		h.Set("accept", "text/event-stream")
		h.Set("accept-encoding", "identity")
	} else {
		h.Set("accept", "application/json")
		h.Set("accept-encoding", "gzip, deflate, br, zstd")
	}
	return h
}
