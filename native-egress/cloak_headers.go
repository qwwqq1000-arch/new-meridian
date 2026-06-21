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
	// Always request identity encoding so upstream never compresses the body.
	// This avoids double-decode on the non-stream path (undici already decompresses)
	// and keeps the stream path consistent.
	h.Set("accept-encoding", "identity")
	if stream {
		h.Set("accept", "text/event-stream")
	} else {
		h.Set("accept", "application/json")
	}
	return h
}
