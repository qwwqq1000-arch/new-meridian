package main

import (
	"encoding/json"
	"strings"
)

// Full identity used when INJECTING (only on the body-check-off path, faking a
// non-CC body as CC). Detection uses the version-stable prefix below, since the
// real first line varies across CLI versions ("…for Claude." vs "…for Claude,
// running within the Claude Agent SDK.").
const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."
const ccIdentityPrefix = "You are Claude Code, Anthropic's official CLI for Claude"

func CloakBody(raw []byte, userID string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	body["system"] = normalizeSystem(body["system"])
	sanitizeCacheTTL(body)
	meta, _ := body["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta["user_id"].(string); !ok || meta["user_id"] == "" {
		meta["user_id"] = userID
	}
	body["metadata"] = meta
	return json.Marshal(body)
}

func normalizeSystem(sys any) []any {
	identity := map[string]any{"type": "text", "text": ClaudeCodeIdentity}
	switch v := sys.(type) {
	case nil:
		return []any{identity}
	case string:
		return []any{identity, map[string]any{"type": "text", "text": v}}
	case []any:
		// Genuine Claude Code may carry the identity in a non-first block (recent
		// CLI prepends a `x-anthropic-billing-header` block). If ANY block already
		// bears the identity, forward verbatim — prepending a duplicate would be a
		// forgery tell. Only inject when the identity is absent entirely.
		for _, item := range v {
			if b, ok := item.(map[string]any); ok {
				if text, ok := b["text"].(string); ok && strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), ccIdentityPrefix) {
					return v
				}
			}
		}
		return append([]any{identity}, v...)
	default:
		return []any{identity}
	}
}

func sanitizeCacheTTL(body map[string]any) {
	var walk func(any)
	fix := func(b map[string]any) {
		if cc, ok := b["cache_control"].(map[string]any); ok {
			if ttl, has := cc["ttl"]; has && ttl != "5m" && ttl != "1h" {
				cc["ttl"] = "5m"
			}
		}
	}
	walk = func(node any) {
		arr, ok := node.([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			if b, ok := item.(map[string]any); ok {
				fix(b)
				walk(b["content"])
			}
		}
	}
	walk(body["system"])
	walk(body["tools"])
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok {
				walk(mm["content"])
			}
		}
	}
}
