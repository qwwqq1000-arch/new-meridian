package main

import (
	"fmt"
	"net/http"
	"os"
)

// RedactAuth returns a clone of h with the authorization header masked.
// The last 4 characters of the token are preserved for debugging; the rest
// is replaced with "Bearer ***<last4>".
// SECURITY: the raw token is NEVER logged — only the last 4 chars are shown.
func RedactAuth(h http.Header) http.Header {
	out := h.Clone()
	if a := out.Get("authorization"); a != "" {
		last := a
		if len(a) > 4 {
			last = a[len(a)-4:]
		}
		out.Set("authorization", "Bearer ***"+last)
	}
	return out
}

// logRelay logs the outgoing headers (with auth redacted) and the cloaked body
// to stderr. Gated by MERIDIAN_NATIVE_DEBUG (default: enabled — set to "0"
// or "false" to silence).
func logRelay(account string, headers http.Header, body []byte) {
	if v := os.Getenv("MERIDIAN_NATIVE_DEBUG"); v == "0" || v == "false" {
		return
	}
	safe := RedactAuth(headers)
	fmt.Fprintf(os.Stderr, "[native-egress] relay account=%s headers=%v body=%s\n",
		account, safe, body)
}

// logUpstreamError logs a non-2xx upstream response body (truncated) so native
// failures (e.g. 400 invalid_request) are diagnosable. Gated like logRelay.
func logUpstreamError(status int, body []byte) {
	if v := os.Getenv("MERIDIAN_NATIVE_DEBUG"); v == "0" || v == "false" {
		return
	}
	fmt.Fprintf(os.Stderr, "[native-egress] upstream_non2xx status=%d body=%s\n", status, body)
}
