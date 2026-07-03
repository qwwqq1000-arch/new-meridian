package main

import "testing"

func TestBuildHeaders(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.198", "x-app": "cli"}
	h := BuildHeaders(fp, "tok123", "sess-1", "rid-1", false, "")
	if h.Get("user-agent") != "claude-cli/2.1.198" {
		t.Fatalf("ua: %q", h.Get("user-agent"))
	}
	if h.Get("authorization") != "Bearer tok123" {
		t.Fatalf("auth: %q", h.Get("authorization"))
	}
	if h.Get("x-claude-code-session-id") != "sess-1" {
		t.Fatal("session id not set")
	}
	if h.Get("x-client-request-id") != "rid-1" {
		t.Fatal("client-request-id not set")
	}
	if h.Get("x-stainless-retry-count") != "0" {
		t.Fatal("retry-count")
	}
}

func TestBuildHeadersUnionsClientBeta(t *testing.T) {
	fp := Fingerprint{"anthropic-beta": "base-flag-123"}
	h := BuildHeaders(fp, "t", "s", "r", false, "structured-outputs-2025-12-15,base-flag-123")
	got := h.Get("anthropic-beta")
	if !contains(got, "structured-outputs-2025-12-15") {
		t.Fatalf("client beta should be unioned in, got: %q", got)
	}
	if !contains(got, "base-flag-123") {
		t.Fatalf("fp beta should be preserved, got: %q", got)
	}
}

func TestUnionAnthropicBeta(t *testing.T) {
	if got := unionAnthropicBeta("a, b", "b ,c", ""); got != "a,b,c" {
		t.Fatalf("union: %q", got)
	}
	if got := unionAnthropicBeta("", "x"); got != "x" {
		t.Fatalf("union empty fp: %q", got)
	}
}

func contains(s, sub string) bool {
	for _, p := range splitBeta(s) {
		if p == sub {
			return true
		}
	}
	return false
}

func splitBeta(s string) []string {
	var out []string
	for _, p := range splitComma(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			p := s[start:i]
			for len(p) > 0 && p[0] == ' ' {
				p = p[1:]
			}
			out = append(out, p)
			start = i + 1
		}
	}
	return out
}
