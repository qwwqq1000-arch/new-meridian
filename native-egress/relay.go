package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RelayDeps holds injectable dependencies for relayHandler, enabling unit tests
// to supply fake transports, fingerprint caches, and clocks.
type RelayDeps struct {
	Transport http.RoundTripper
	FP        *FPCache
	SessionID func(account string) string
	Now       func() time.Time
}

// relayHandler returns the POST /relay handler.
//
// The request body IS the verbatim client body (the exact bytes Meridian
// received). Metadata travels in headers so the body is NEVER re-serialized in
// transit — re-marshaling corrupts the cryptographic `signature` on assistant
// `thinking` blocks, which Anthropic then rejects ("thinking blocks ... cannot
// be modified"). Flow:
//  1. Read raw body + metadata headers
//  2. ReadToken (degrade if unavailable)
//  3. FP.Get (degrade if unavailable)
//  4. CloakBody (verbatim for genuine CC; surgical only when faking)
//  5. BuildHeaders → forward → non-2xx degrade, else stream back
func relayHandler(d RelayDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil || len(rawBody) == 0 {
			degrade(w, "bad_request")
			return
		}
		configDir := r.Header.Get("X-Native-Config-Dir")
		account := r.Header.Get("X-Native-Account")
		stream := r.Header.Get("X-Native-Stream") == "1"
		clientBeta := r.Header.Get("X-Native-Anthropic-Beta")

		token, _, _, err := ReadToken(configDir)
		if err != nil || token == "" {
			degrade(w, "no_token")
			return
		}

		fp, ok := d.FP.Get(account, configDir, d.Now())
		if !ok {
			degrade(w, "no_fingerprint")
			return
		}

		cloaked, err := CloakBody(rawBody, "user_"+account)
		if err != nil {
			degrade(w, "cloak_error")
			return
		}

		headers := BuildHeaders(fp, token, d.SessionID(account), uuid.NewString(), stream, clientBeta)

		upReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages?beta=true", bytesReader(cloaked))
		if err != nil {
			degrade(w, "build_request_error")
			return
		}
		upReq.Header = headers

		logRelay(account, headers, cloaked)

		resp, err := d.Transport.RoundTrip(upReq)
		if err != nil {
			degrade(w, "upstream_error")
			return
		}
		defer resp.Body.Close()

		// non-2xx → degrade so Node falls back to SDK
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			logUpstreamError(resp.StatusCode, errBody)
			degrade(w, fmt.Sprintf("upstream_%d", resp.StatusCode))
			return
		}

		for k, vs := range resp.Header {
			// Skip content-encoding and content-length: we always send
			// identity-encoded bodies and let the Node HTTP layer handle framing.
			// Forwarding these would cause double-decode or length mismatches.
			kl := strings.ToLower(k)
			if kl == "content-encoding" || kl == "content-length" {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		rc := http.NewResponseController(w)
		buf := make([]byte, 16*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					break
				}
				_ = rc.Flush()
			}
			if rerr != nil {
				break
			}
		}
	}
}

// degrade signals to the caller (Node proxy) that this request should be
// handled via the fallback SDK path. It always returns HTTP 200 with
// X-Degrade: 1 so that network errors are distinguishable from intentional
// degradation.
func degrade(w http.ResponseWriter, reason string) {
	w.Header().Set("X-Degrade", "1")
	w.Header().Set("X-Degrade-Reason", reason)
	w.WriteHeader(200)
}

// bytesReader wraps a byte slice in an io.ReadCloser, reusing bytes.NewReader.
func bytesReader(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}
