package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var errAlways = &alwaysErr{}

type alwaysErr struct{}

func (e *alwaysErr) Error() string { return "always error" }

// writeTempCreds writes a .credentials.json with the given access token
// into a temp dir (mirrors the Task 6 oauth.go credsFile format) and returns
// the dir path.
func writeTempCreds(t *testing.T, token string) string {
	t.Helper()
	dir := t.TempDir()
	creds := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  token,
			"refreshToken": "refresh-xyz",
			"expiresAt":    time.Now().Add(time.Hour).UnixMilli(),
		},
	}
	b, _ := json.Marshal(creds)
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// relayReqRaw builds a /relay request the way Node does: the raw client body as
// the POST body, with metadata in headers.
func relayReqRaw(dir, account string, body []byte) *http.Request {
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(body))
	req.Header.Set("X-Native-Config-Dir", dir)
	req.Header.Set("X-Native-Account", account)
	return req
}

func TestRelayDegradesWhenNoFingerprint(t *testing.T) {
	// Use a real creds file so ReadToken succeeds; with an always-error FP fetcher,
	// the handler must degrade at the fingerprint step — NOT at no_token.
	dir := writeTempCreds(t, "tok-fp-test")
	deps := RelayDeps{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) { t.Fatal("must not forward"); return nil, nil }),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return "", errAlways }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade, got code %d", rec.Code)
	}
	if rec.Header().Get("X-Degrade-Reason") != "no_fingerprint" {
		t.Fatalf("expected no_fingerprint reason, got %q", rec.Header().Get("X-Degrade-Reason"))
	}
}

func TestRelayForwardsWithCloak(t *testing.T) {
	var gotAuth, gotUA string
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("authorization")
			gotUA = r.Header.Get("user-agent")
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	// token comes from a creds file the handler reads; point configDir at a temp dir
	dir := writeTempCreds(t, "tok-abc")
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("auth: %q", gotAuth)
	}
	if gotUA == "Go-http-client/1.1" || gotUA == "" {
		t.Fatalf("ua not cloaked: %q", gotUA)
	}
}

// The relay must forward the client body BYTE-FOR-BYTE. A thinking block's
// signature is computed over the original text (including `<`,`>` chars that a
// JSON re-marshal would HTML-escape), so any re-serialization breaks it.
func TestRelayForwardsBodyVerbatim(t *testing.T) {
	raw := []byte(`{"model":"x","system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"a<b>c&d","signature":"SIG=="}]}]}`)
	var sent []byte
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			sent, _ = io.ReadAll(r.Body)
			return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := writeTempCreds(t, "tok")
	rec := httptest.NewRecorder()
	relayHandler(deps)(rec, relayReqRaw(dir, "a", raw))
	if string(sent) != string(raw) {
		t.Fatalf("body must be verbatim.\n got: %s\nwant: %s", sent, raw)
	}
}

func TestRedactAuth(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer supersecrettoken1234")
	h.Set("Content-Type", "application/json")

	redacted := RedactAuth(h)
	if redacted.Get("Authorization") != "Bearer ***1234" {
		t.Fatalf("unexpected redaction: %q", redacted.Get("Authorization"))
	}
	// original must be unchanged
	if h.Get("Authorization") != "Bearer supersecrettoken1234" {
		t.Fatal("original header was mutated")
	}
	if redacted.Get("Content-Type") != "application/json" {
		t.Fatal("other headers must be preserved")
	}
}

func TestRelayDegradesOnUpstreamNon2xx(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 401, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	dir := writeTempCreds(t, "tok-xyz")
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade on 401, got code %d", rec.Code)
	}
	if rec.Header().Get("X-Degrade-Reason") != "upstream_401" {
		t.Fatalf("expected upstream_401 reason, got %q", rec.Header().Get("X-Degrade-Reason"))
	}
}

func TestRelayDegradesOnMissingToken(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) { t.Fatal("must not forward"); return nil, nil }),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return sampleDebug, nil }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	// configDir with no credentials file
	dir := t.TempDir()
	rec := httptest.NewRecorder()
	req := relayReqRaw(dir, "a", []byte(`{"messages":[]}`))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade on missing token, got code %d", rec.Code)
	}
}
