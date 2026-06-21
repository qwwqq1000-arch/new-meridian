package main

import (
	"encoding/json"
	"testing"
)

func TestCloakBodyInjectsIdentityAndUserID(t *testing.T) {
	out, err := CloakBody([]byte(`{"system":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}],"messages":[]}`), "user_fake")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity {
		t.Fatal("identity not first")
	}
	if sys[1].(map[string]any)["cache_control"] == nil {
		t.Fatal("cache_control must be preserved")
	}
	if m["metadata"].(map[string]any)["user_id"] != "user_fake" {
		t.Fatal("user_id not set")
	}
}

// Genuine Claude Code carries the identity in a non-first block (a billing-header
// block comes first). CloakBody must forward verbatim, NOT prepend a duplicate.
func TestCloakBodyIdentityInNonFirstBlockNotDuplicated(t *testing.T) {
	in := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.148.902"},{"type":"text","text":"` + ClaudeCodeIdentity + `\n\nmore"}],"messages":[]}`
	out, err := CloakBody([]byte(in), "u")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("expected 2 system blocks (verbatim), got %d: %#v", len(sys), sys)
	}
	count := 0
	for _, b := range sys {
		if bm, ok := b.(map[string]any); ok {
			if text, ok := bm["text"].(string); ok && len(text) >= len(ClaudeCodeIdentity) && text[:len(ClaudeCodeIdentity)] == ClaudeCodeIdentity {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("identity must appear exactly once (no duplicate), got %d", count)
	}
}

func TestCloakBodyStringSystem(t *testing.T) {
	out, _ := CloakBody([]byte(`{"system":"you are X","messages":[]}`), "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity || sys[1].(map[string]any)["text"] != "you are X" {
		t.Fatalf("string system not normalized: %#v", sys)
	}
}
