package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// ErrCCBodyConflict signals that a CC-shaped body had parameter conflicts
// (e.g. thinking+temperature) that were resolved in-memory, but re-marshaling
// would corrupt thinking block signatures. Relay should degrade to SDK path.
var ErrCCBodyConflict = errors.New("cc_body_conflict")

// marshalBody serializes body without escaping HTML characters (<, >, &).
// Go's json.Marshal escapes these by default, which corrupts thinking block
// signatures that contain HTML-like content.
func marshalBody(body map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	// Encode appends a newline; strip it
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// Full identity used when INJECTING (only on the body-check-off path, faking a
// non-CC body as CC). Detection uses the version-stable prefix below, since the
// real first line varies across CLI versions ("…for Claude." vs "…for Claude,
// running within the Claude Agent SDK.").
const ClaudeCodeIdentity = "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."
const ccIdentityPrefix = "You are Claude Code, Anthropic's official CLI for Claude"

// deriveUserID produces a deterministic user_id that matches the real CC
// format: "user-" + 28-char base32-like string (e.g. "user-2vS9cBh5DkNMq8wFcKfQ2s1hKgB").
func deriveUserID(account string) string {
	h := sha256.Sum256([]byte("meridian-uid:" + account))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:21])
	return "user-" + enc[:28]
}

func CloakBody(raw []byte, userID string) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	// Fixes operate on parsed body — gjson/sjson mis-match nested keys on
	// large bodies. Returns true if any field was actually changed.
	c1 := fixToolChoiceThinkingConflict(body)
	c2 := fixThinkingContextConflicts(body)
	c3 := fixInvalidEffort(body)
	changed := c1 || c2 || c3
	logDD("CloakBody: c1=%v c2=%v c3=%v changed=%v thinking=%v temp=%v", c1, c2, c3, changed, body["thinking"], body["temperature"])

	if hasClaudeIdentity(body["system"]) {
		if changed {
			// CC body had conflicts that were fixed in the parsed map, but
			// re-marshaling would corrupt thinking block signatures. Return
			// ErrCCBodyConflict so relay can pre-reject with a clear message.
			return nil, ErrCCBodyConflict
		}
		// Never re-marshal CC bodies — signatures are fragile.
		// cache_control is an optimization; skip if absent.
		return raw, nil
	}

	fillDefaults(body)

	body["system"] = normalizeSystem(body["system"])
	sanitizeCacheTTL(body)
	meta, _ := body["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta["user_id"].(string); !ok || meta["user_id"] == "" {
		meta["user_id"] = userID
	}
	// Strip non-standard metadata fields — Anthropic only allows user_id.
	for k := range meta {
		if k != "user_id" {
			delete(meta, k)
		}
	}
	body["metadata"] = meta
	return marshalBody(body)
}

// fillDefaults adds missing fields that a real CLI always sends.
// Without these the request either 400s (max_tokens) or looks non-CLI.
func fillDefaults(body map[string]any) {
	model, _ := body["model"].(string)

	// max_tokens — required by API, real CLI always sends it
	if _, ok := body["max_tokens"].(float64); !ok {
		body["max_tokens"] = defaultMaxTokens(model)
	}

	// stream — real CLI always sends true
	if _, ok := body["stream"].(bool); !ok {
		body["stream"] = true
	}

	// thinking — only inject when absent AND max_tokens can accommodate it.
	// If user explicitly set a small max_tokens, respect it — don't inject
	// thinking that would force us to silently inflate their budget.
	userSetMaxTokens := body["max_tokens"] != nil
	if _, ok := body["thinking"]; !ok {
		if thinkingModel(model) {
			mt := toInt(body["max_tokens"])
			budget := defaultThinkingBudget(model)
			if !userSetMaxTokens {
				// We set max_tokens ourselves — use full defaults
				body["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": budget,
				}
			} else if mt > budget+1024 {
				// User's max_tokens has room for thinking
				body["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": budget,
				}
			} else if mt > 2048 {
				// User's max_tokens is small — fit thinking budget inside it
				body["thinking"] = map[string]any{
					"type":          "enabled",
					"budget_tokens": mt / 2,
				}
			}
			// else: max_tokens too small for any thinking, skip injection
		}
	}

	// If user set both thinking AND max_tokens and they conflict, shrink
	// the thinking budget rather than inflating max_tokens.
	ensureThinkingFitsMaxTokens(body)
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// ensureThinkingFitsMaxTokens shrinks thinking.budget_tokens to fit within
// max_tokens. Never inflates max_tokens — that would silently increase the
// user's cost. If max_tokens is too small for any thinking, remove thinking.
func ensureThinkingFitsMaxTokens(body map[string]any) {
	th, ok := body["thinking"].(map[string]any)
	if !ok {
		return
	}
	budget := toInt(th["budget_tokens"])
	if budget == 0 {
		return
	}
	mt := toInt(body["max_tokens"])
	if mt == 0 {
		return
	}
	if mt > budget {
		return // already valid
	}
	if mt > 2048 {
		th["budget_tokens"] = mt / 2
	} else {
		delete(body, "thinking")
	}
}

func defaultMaxTokens(model string) int {
	switch {
	case strings.Contains(model, "haiku"):
		return 16384
	case strings.Contains(model, "sonnet"):
		return 32000
	default:
		return 64000
	}
}

func thinkingModel(model string) bool {
	return strings.Contains(model, "opus") ||
		strings.Contains(model, "sonnet-4") ||
		strings.Contains(model, "haiku-4")
}

func defaultThinkingBudget(model string) int {
	switch {
	case strings.Contains(model, "opus"):
		return 32000
	case strings.Contains(model, "sonnet"):
		return 16000
	default:
		return 10000
	}
}

// fixThinkingContextConflicts resolves conflicts between thinking config and
// context_management/temperature. Operates on parsed body, re-serialized via json.Marshal.
//  1. thinking:disabled + clear_thinking in context_management → remove clear_thinking edits
//  2. temperature ≠ 1 + thinking enabled/adaptive → remove thinking (user chose temperature)
// fixToolChoiceThinkingConflict removes thinking when tool_choice forces a tool.
func fixToolChoiceThinkingConflict(body map[string]any) bool {
	tc, _ := body["tool_choice"].(map[string]any)
	if tc == nil {
		return false
	}
	tcType, _ := tc["type"].(string)
	if tcType != "any" && tcType != "tool" {
		return false
	}
	_, hadThinking := body["thinking"]
	delete(body, "thinking")
	if oc, ok := body["output_config"].(map[string]any); ok {
		delete(oc, "effort")
	}
	return hadThinking
}

// fixThinkingContextConflicts resolves:
//  1. thinking:disabled + clear_thinking → remove clear_thinking edits
//  2. temperature ≠ 1 + thinking enabled/adaptive → remove thinking (user chose temp)
func fixThinkingContextConflicts(body map[string]any) bool {
	changed := false
	th, _ := body["thinking"].(map[string]any)
	thinkingType := ""
	if th != nil {
		thinkingType, _ = th["type"].(string)
	}

	if thinkingType == "enabled" || thinkingType == "adaptive" {
		if temp, ok := body["temperature"].(float64); ok && temp != 1.0 {
			delete(body, "thinking")
			thinkingType = ""
			changed = true
		}
	}

	if thinkingType != "disabled" && thinkingType != "" {
		return changed
	}
	cm, _ := body["context_management"].(map[string]any)
	if cm == nil {
		return changed
	}
	edits, _ := cm["edits"].([]any)
	filtered := make([]any, 0, len(edits))
	for _, e := range edits {
		if em, ok := e.(map[string]any); ok {
			if t, _ := em["type"].(string); strings.HasPrefix(t, "clear_thinking") {
				changed = true
				continue
			}
		}
		filtered = append(filtered, e)
	}
	cm["edits"] = filtered
	return changed
}

// fixInvalidEffort removes unrecognized effort values.
func fixInvalidEffort(body map[string]any) bool {
	oc, _ := body["output_config"].(map[string]any)
	if oc == nil {
		return false
	}
	effort, _ := oc["effort"].(string)
	switch effort {
	case "", "low", "medium", "high", "max":
		return false
	default:
		delete(oc, "effort")
		return true
	}
}

func hasCacheControl(body map[string]any) bool {
	sys, _ := body["system"].([]any)
	for _, s := range sys {
		if m, ok := s.(map[string]any); ok {
			if _, has := m["cache_control"]; has {
				return true
			}
		}
	}
	return false
}

// ensureCacheControl adds a default ephemeral cache breakpoint to the last
// system block if no cache_control exists anywhere in system blocks.
func ensureCacheControl(body map[string]any) {
	sys, _ := body["system"].([]any)
	if len(sys) == 0 {
		return
	}
	for _, s := range sys {
		if m, ok := s.(map[string]any); ok {
			if _, has := m["cache_control"]; has {
				return
			}
		}
	}
	if last, ok := sys[len(sys)-1].(map[string]any); ok {
		last["cache_control"] = map[string]any{"type": "ephemeral"}
	}
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

// ValidateBody checks the cloaked body for conditions that will definitely be
// rejected by the API. Returns a non-empty error message if the request should
// be rejected early (saves a round-trip). Empty string means OK to send.
func ValidateBody(cloaked []byte) string {
	var body map[string]any
	if json.Unmarshal(cloaked, &body) != nil {
		return ""
	}

	model, _ := body["model"].(string)

	// 1. temperature ≠ 1 on a thinking model without thinking:disabled
	//    API auto-enables thinking on thinking-capable models via beta flag,
	//    then rejects temperature ≠ 1.
	if temp, ok := body["temperature"].(float64); ok && temp != 1.0 {
		th, _ := body["thinking"].(map[string]any)
		thType := ""
		if th != nil {
			thType, _ = th["type"].(string)
		}
		if thType != "disabled" && thinkingModel(model) {
			return "`temperature` may only be set to 1 when thinking is enabled. Set temperature to 1 or disable thinking."
		}
	}

	// 2. clear_thinking with thinking disabled or absent on thinking model
	{
		th, _ := body["thinking"].(map[string]any)
		thType := ""
		if th != nil {
			thType, _ = th["type"].(string)
		}
		if thType == "disabled" || (thType == "" && !thinkingModel(model)) {
			if cm, ok := body["context_management"].(map[string]any); ok {
				if edits, ok := cm["edits"].([]any); ok {
					for _, e := range edits {
						if em, ok := e.(map[string]any); ok {
							if t, _ := em["type"].(string); strings.HasPrefix(t, "clear_thinking") {
								return "`" + t + "` strategy requires `thinking` to be enabled or adaptive"
							}
						}
					}
				}
			}
		}
	}

	// 3. effort level not recognized
	if oc, ok := body["output_config"].(map[string]any); ok {
		if effort, ok := oc["effort"].(string); ok && effort != "" {
			switch effort {
			case "low", "medium", "high", "max":
			default:
				return "Unsupported effort level '" + effort + "'. Supported: low, medium, high, max."
			}
		}
	}

	// 3. thinking blocks in non-assistant messages + empty text
	msgs, _ := body["messages"].([]any)
	for mi, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		role, _ := m["role"].(string)
		content, _ := m["content"].([]any)
		for ci, c := range content {
			block, _ := c.(map[string]any)
			if block == nil {
				continue
			}
			if block["type"] == "thinking" && role != "assistant" {
				return "messages." + itoa(mi) + ".content: thinking blocks may only be in `assistant` messages"
			}
			if block["type"] == "text" {
				if text, ok := block["text"].(string); ok && text == "" {
					return "messages." + itoa(mi) + ".content." + itoa(ci) + ": text content blocks must be non-empty"
				}
			}
		}
	}

	// 4. base64 image > 10 MB
	for mi, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		content, _ := m["content"].([]any)
		for ci, c := range content {
			block, _ := c.(map[string]any)
			if block == nil || block["type"] != "image" {
				continue
			}
			src, _ := block["source"].(map[string]any)
			if src == nil || src["type"] != "base64" {
				continue
			}
			data, _ := src["data"].(string)
			// base64 decode size ≈ len * 3/4
			if len(data)*3/4 > 10*1024*1024 {
				return "messages." + itoa(mi) + ".content." + itoa(ci) + ".image.source.base64: image exceeds 10 MB maximum"
			}
		}
	}

	// 5. cache_control blocks > 4
	cacheCount := 0
	if sys, ok := body["system"].([]any); ok {
		for _, s := range sys {
			if sm, ok := s.(map[string]any); ok {
				if _, has := sm["cache_control"]; has {
					cacheCount++
				}
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				if _, has := tm["cache_control"]; has {
					cacheCount++
				}
			}
		}
	}
	for _, msg := range msgs {
		m, _ := msg.(map[string]any)
		if m == nil {
			continue
		}
		content, _ := m["content"].([]any)
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block == nil {
				continue
			}
			if _, has := block["cache_control"]; has {
				cacheCount++
			}
		}
	}
	if cacheCount > 4 {
		return "A maximum of 4 blocks with cache_control may be provided. Found " + itoa(cacheCount) + "."
	}

	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }
