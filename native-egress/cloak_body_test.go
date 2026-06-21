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

func TestCloakBodyStringSystem(t *testing.T) {
	out, _ := CloakBody([]byte(`{"system":"you are X","messages":[]}`), "u")
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	sys := m["system"].([]any)
	if sys[0].(map[string]any)["text"] != ClaudeCodeIdentity || sys[1].(map[string]any)["text"] != "you are X" {
		t.Fatalf("string system not normalized: %#v", sys)
	}
}
