package main

import "testing"

func TestBuildHeaders(t *testing.T) {
	fp := Fingerprint{"user-agent": "claude-cli/2.1.185", "x-app": "cli", "anthropic-beta": "claude-code-20250219"}
	h := BuildHeaders(fp, "tok123", "sess-1", "req-1", false)
	if h.Get("user-agent") != "claude-cli/2.1.185" {
		t.Fatalf("ua: %q", h.Get("user-agent"))
	}
	if h.Get("authorization") != "Bearer tok123" {
		t.Fatalf("auth: %q", h.Get("authorization"))
	}
	if h.Get("x-claude-code-session-id") != "sess-1" || h.Get("x-client-request-id") != "req-1" {
		t.Fatal("session/request id not set")
	}
	if h.Get("x-stainless-retry-count") != "0" {
		t.Fatal("retry-count")
	}
	if h.Get("accept") != "application/json" {
		t.Fatalf("non-stream accept: %q", h.Get("accept"))
	}
	if h.Get("accept-encoding") != "identity" {
		t.Fatalf("non-stream accept-encoding: %q", h.Get("accept-encoding"))
	}
}

func TestBuildHeadersStreamAccept(t *testing.T) {
	h := BuildHeaders(Fingerprint{"user-agent": "claude-cli/x"}, "t", "s", "r", true)
	if h.Get("accept") != "text/event-stream" {
		t.Fatalf("stream accept: %q", h.Get("accept"))
	}
}
