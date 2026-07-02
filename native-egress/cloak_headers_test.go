package main

import "testing"

func TestBuildHeaders(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.198", "x-app": "cli"}
	h := BuildHeaders(fp, "tok123", "sess-1", false, "claude-sonnet-4-6", "")
	if h.Get("user-agent") != "claude-cli/2.1.198" {
		t.Fatalf("ua: %q", h.Get("user-agent"))
	}
	if h.Get("authorization") != "Bearer tok123" {
		t.Fatalf("auth: %q", h.Get("authorization"))
	}
	if h.Get("x-claude-code-session-id") != "sess-1" {
		t.Fatal("session id not set")
	}
	if h.Get("x-stainless-retry-count") != "0" {
		t.Fatal("retry-count")
	}
	// Non-stream → timeout=300
	if h.Get("x-stainless-timeout") != "300" {
		t.Fatalf("timeout should be 300 for non-stream, got %q", h.Get("x-stainless-timeout"))
	}
}

func TestBuildHeadersStreamTimeout(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.198"}
	h := BuildHeaders(fp, "t", "s", true, "claude-sonnet-4-6", "")
	if h.Get("x-stainless-timeout") != "600" {
		t.Fatalf("timeout should be 600 for stream, got %q", h.Get("x-stainless-timeout"))
	}
}

func TestBuildHeadersModelBeta(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.198"}

	// Sonnet-5 (new) should have mid-conversation-system and effort
	hSon5 := BuildHeaders(fp, "t", "s", false, "claude-sonnet-5", "")
	beta5 := hSon5.Get("anthropic-beta")
	if !contains(beta5, "mid-conversation-system-2026-04-07") {
		t.Fatalf("sonnet-5 beta should have mid-conversation-system, got: %s", beta5)
	}
	if !contains(beta5, "effort-2025-11-24") {
		t.Fatalf("sonnet-5 beta should have effort, got: %s", beta5)
	}

	// Opus-4-6 (old) should NOT have mid-conversation-system
	hOp := BuildHeaders(fp, "t", "s", false, "claude-opus-4-6", "")
	betaOp := hOp.Get("anthropic-beta")
	if contains(betaOp, "mid-conversation-system-2026-04-07") {
		t.Fatalf("opus-4-6 beta should NOT have mid-conversation-system, got: %s", betaOp)
	}

	// Opus-4-8 (new) SHOULD have mid-conversation-system
	hOp8 := BuildHeaders(fp, "t", "s", false, "claude-opus-4-8", "")
	betaOp8 := hOp8.Get("anthropic-beta")
	if !contains(betaOp8, "mid-conversation-system-2026-04-07") {
		t.Fatalf("opus-4-8 beta should have mid-conversation-system, got: %s", betaOp8)
	}

	// Sonnet-4-6 (old) should NOT have mid-conversation-system
	hSon46 := BuildHeaders(fp, "t", "s", false, "claude-sonnet-4-6", "")
	betaSon46 := hSon46.Get("anthropic-beta")
	if contains(betaSon46, "mid-conversation-system-2026-04-07") {
		t.Fatalf("sonnet-4-6 beta should NOT have mid-conversation-system, got: %s", betaSon46)
	}

	// Haiku should NOT have effort or mid-conversation-system
	hHa := BuildHeaders(fp, "t", "s", false, "claude-haiku-4-5", "")
	betaHa := hHa.Get("anthropic-beta")
	if contains(betaHa, "effort-2025-11-24") {
		t.Fatalf("haiku beta should NOT have effort, got: %s", betaHa)
	}
	if contains(betaHa, "mid-conversation-system-2026-04-07") {
		t.Fatalf("haiku beta should NOT have mid-conversation-system, got: %s", betaHa)
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

func TestBuildHeadersUnionsClientBeta(t *testing.T) {
	fp := Fingerprint{}
	h := BuildHeaders(fp, "t", "s", false, "claude-sonnet-4-6", "structured-outputs-2025-12-15,oauth-2025-04-20")
	got := h.Get("anthropic-beta")
	if !contains(got, "structured-outputs-2025-12-15") {
		t.Fatalf("client beta should be unioned in, got: %q", got)
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
