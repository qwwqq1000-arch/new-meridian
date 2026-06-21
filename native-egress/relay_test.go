package main

import (
	"bytes"
	"encoding/json"
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

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestRelayDegradesWhenNoFingerprint(t *testing.T) {
	deps := RelayDeps{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) { t.Fatal("must not forward"); return nil, nil }),
		FP:        NewFPCache(time.Minute, func(string) (string, error) { return "", errAlways }),
		SessionID: func(string) string { return "s" },
		Now:       time.Now,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(mustJSON(map[string]any{"configDir": "/x", "account": "a", "body": map[string]any{"messages": []any{}}})))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade, got %d", rec.Code)
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
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(mustJSON(map[string]any{"configDir": dir, "account": "a", "body": map[string]any{"messages": []any{}}})))
	relayHandler(deps)(rec, req)
	if gotAuth != "Bearer tok-abc" {
		t.Fatalf("auth: %q", gotAuth)
	}
	if gotUA == "Go-http-client/1.1" || gotUA == "" {
		t.Fatalf("ua not cloaked: %q", gotUA)
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
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(mustJSON(map[string]any{"configDir": dir, "account": "a", "body": map[string]any{"messages": []any{}}})))
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
	req := httptest.NewRequest("POST", "/relay", bytes.NewReader(mustJSON(map[string]any{"configDir": dir, "account": "a", "body": map[string]any{"messages": []any{}}})))
	relayHandler(deps)(rec, req)
	if rec.Header().Get("X-Degrade") != "1" {
		t.Fatalf("expected degrade on missing token, got code %d", rec.Code)
	}
}
