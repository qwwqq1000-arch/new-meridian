package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Full identity used when INJECTING (only on the body-check-off path, faking a
// non-CC body as CC). Detection uses the version-stable prefix below, since the
// real first line varies across CLI versions ("…for Claude." vs "…for Claude,
// running within the Claude Agent SDK.").
const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."
const ccIdentityPrefix = "You are Claude Code, Anthropic's official CLI for Claude"

func CloakBody(raw []byte, userID string) ([]byte, error) {
	// Applies to EVERY native path (genuine-CC verbatim AND the non-CC faking
	// path): Anthropic rejects thinking together with a forced tool_choice (400).
	// Surgical (sjson), a no-op unless tool_choice forces a tool — so a verbatim
	// CC body is untouched otherwise.
	raw = disableThinkingIfToolChoiceForced(raw)

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// Genuine Claude Code: forward the body VERBATIM. Re-marshaling via
	// map[string]any reorders keys and reformats numbers, which corrupts the
	// cryptographic `signature` on assistant `thinking` blocks — Anthropic then
	// rejects with "thinking blocks ... must remain as they were in the original
	// response" (400). Native is a passthrough; touch nothing when the body is
	// already CC-shaped. The transforms below exist only to fake a non-CC body
	// as CC on the body-check-off path.
	if hasClaudeIdentity(body["system"]) {
		// Add a default 5m cache breakpoint only when none was sent (a
		// client-supplied ttl:1h is preserved as-is). Surgical — signatures hold.
		return ensureCacheControl5m(raw), nil
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

// disableThinkingIfToolChoiceForced removes the request's top-level thinking
// CONFIG (and output_config.effort) when tool_choice forces tool use
// ("any"/"tool"). Anthropic rejects thinking combined with a forced tool_choice
// (400). This touches only the request's thinking configuration — NOT historical
// thinking blocks in messages — and edits surgically via sjson, so block
// signatures are never altered. Mirrors CPA's transform of the same name.
func disableThinkingIfToolChoiceForced(raw []byte) []byte {
	tc := gjson.GetBytes(raw, "tool_choice.type").String()
	if tc != "any" && tc != "tool" {
		return raw
	}
	out := raw
	if gjson.GetBytes(out, "thinking").Exists() {
		out, _ = sjson.DeleteBytes(out, "thinking")
	}
	if gjson.GetBytes(out, "output_config.effort").Exists() {
		out, _ = sjson.DeleteBytes(out, "output_config.effort")
	}
	return out
}

// ensureCacheControl5m adds a default 5m cache breakpoint when the client sent
// no cache_control anywhere. "ephemeral" with no ttl IS Anthropic's 5m default.
// If any cache_control is already present the body is returned untouched, so a
// client-supplied ttl (e.g. "1h") is preserved verbatim. Edit is surgical
// (sjson) — it never re-serializes the whole body, so thinking signatures hold.
func ensureCacheControl5m(raw []byte) []byte {
	if bytes.Contains(raw, []byte(`"cache_control"`)) {
		return raw
	}
	system := gjson.GetBytes(raw, "system")
	if !system.IsArray() {
		return raw
	}
	n := len(system.Array())
	if n == 0 {
		return raw
	}
	out, err := sjson.SetRawBytes(raw, fmt.Sprintf("system.%d.cache_control", n-1), []byte(`{"type":"ephemeral"}`))
	if err != nil {
		return raw
	}
	return out
}

// hasClaudeIdentity reports whether the system field already carries the Claude
// Code identity (in any text block, or as a plain string). Genuine CC may put it
// in a non-first block (recent CLI prepends an `x-anthropic-billing-header`
// block), so we scan all blocks and match the version-stable prefix.
func hasClaudeIdentity(sys any) bool {
	switch v := sys.(type) {
	case string:
		return strings.HasPrefix(strings.TrimLeft(v, " \t\r\n"), ccIdentityPrefix)
	case []any:
		for _, item := range v {
			if b, ok := item.(map[string]any); ok {
				if text, ok := b["text"].(string); ok && strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), ccIdentityPrefix) {
					return true
				}
			}
		}
	}
	return false
}

func normalizeSystem(sys any) []any {
	identity := map[string]any{"type": "text", "text": ClaudeCodeIdentity}
	switch v := sys.(type) {
	case nil:
		return []any{identity}
	case string:
		return []any{identity, map[string]any{"type": "text", "text": v}}
	case []any:
		// If the identity is already present anywhere, forward verbatim —
		// prepending a duplicate would be a forgery tell. Only inject when absent.
		if hasClaudeIdentity(v) {
			return v
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
