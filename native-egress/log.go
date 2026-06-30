package main

import (
	"encoding/json"
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
	v := os.Getenv("MERIDIAN_NATIVE_DEBUG")
	if v != "1" && v != "true" {
		return
	}
	safe := RedactAuth(headers)
	fmt.Fprintf(os.Stderr, "[native-egress] relay account=%s headers=%v body=%s\n",
		account, safe, body)
}

// dumpRequest writes the full outgoing request (headers + body) to /tmp for
// one-shot debugging. Writes to /tmp/ne_dump_cc.json or /tmp/ne_dump_bare.json.
// Remove after debugging.
func dumpRequest(headers http.Header, body []byte, isCC bool) {
	tag := "bare"
	if isCC {
		tag = "cc"
	}
	safe := RedactAuth(headers)
	hmap := make(map[string]string, len(safe))
	for k := range safe {
		hmap[k] = safe.Get(k)
	}
	out := map[string]any{"headers": hmap, "body": json.RawMessage(body)}
	data, _ := json.MarshalIndent(out, "", "  ")
	path := "/tmp/ne_dump_" + tag + ".json"
	os.WriteFile(path, data, 0o644)
	fmt.Fprintf(os.Stderr, "[native-egress] dumped %s request to %s (%d bytes)\n", tag, path, len(data))
}

// logUpstreamError logs a non-2xx upstream response body (truncated) so native
// failures (e.g. 400 invalid_request) are diagnosable. Gated like logRelay.
func logUpstreamError(status int, body []byte) {
	v := os.Getenv("MERIDIAN_NATIVE_DEBUG")
	if v != "1" && v != "true" {
		return
	}
	fmt.Fprintf(os.Stderr, "[native-egress] upstream_non2xx status=%d body=%s\n", status, body)
}
