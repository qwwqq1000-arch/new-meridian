package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// BodyTemplate holds the complete request body structure captured from a genuine
// Claude Code CLI request. Non-CC requests are merged with this template so every
// outgoing request matches a real CLI request exactly.
type BodyTemplate struct {
	System            []any  `json:"system"`
	ContextManagement any    `json:"context_management"`
	OutputConfig      any    `json:"output_config"`
	Diagnostics       any    `json:"diagnostics"`
	Tools             []any  `json:"tools"`
	Thinking          any    `json:"thinking"`
	Stream            bool   `json:"stream"`
	MaxTokens         int    `json:"max_tokens"`
	Version           string `json:"-"`
	Betas             string `json:"-"`
	NodeVersion       string `json:"-"`
	BuildTime         string `json:"-"`
}

type BodyTemplateCache struct {
	mu         sync.RWMutex
	tmpl       *BodyTemplate
	capturedAt time.Time
	ttl        time.Duration
}

func NewBodyTemplateCache(ttl time.Duration) *BodyTemplateCache {
	return &BodyTemplateCache{ttl: ttl}
}

func (c *BodyTemplateCache) Get() *BodyTemplate {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.tmpl == nil {
		return nil
	}
	if time.Since(c.capturedAt) > c.ttl {
		return c.tmpl
	}
	return c.tmpl
}

// LearnFromCC extracts a body template from a genuine Claude Code request body.
// Called on every CC-shaped request that passes through relay.
func (c *BodyTemplateCache) LearnFromCC(rawBody []byte, fpVersion, fpBetas, fpNodeVer string) {
	if c == nil {
		return
	}
	var body map[string]any
	if json.Unmarshal(rawBody, &body) != nil {
		return
	}
	if !hasClaudeIdentity(body["system"]) {
		return
	}

	tmpl := &BodyTemplate{
		Stream:  true,
		Version: fpVersion,
		Betas:   fpBetas,
	}

	if sys, ok := body["system"].([]any); ok {
		tmpl.System = sys
	}
	if cm, ok := body["context_management"]; ok {
		tmpl.ContextManagement = cm
	}
	if oc, ok := body["output_config"]; ok {
		if ocMap, ok := oc.(map[string]any); ok {
			if ocMap["effort"] == "xhigh" {
				ocMap["effort"] = "high"
			}
		}
		tmpl.OutputConfig = oc
	}
	if diag, ok := body["diagnostics"]; ok {
		tmpl.Diagnostics = diag
	}
	if tools, ok := body["tools"].([]any); ok {
		tmpl.Tools = tools
	}
	if th, ok := body["thinking"]; ok {
		tmpl.Thinking = th
	}
	if s, ok := body["stream"].(bool); ok {
		tmpl.Stream = s
	}
	if mt, ok := body["max_tokens"].(float64); ok && mt > 0 {
		tmpl.MaxTokens = int(mt)
	}
	if fpNodeVer != "" {
		tmpl.NodeVersion = fpNodeVer
	}

	c.mu.Lock()
	c.tmpl = tmpl
	c.capturedAt = time.Now()
	c.mu.Unlock()
	logDD("body template learned: system=%d blocks, tools=%d, version=%s",
		len(tmpl.System), len(tmpl.Tools), tmpl.Version)
}

// MergeUserRequest takes a user's bare API request and merges it with the
// captured CLI template. Only messages, model, and user-specified overrides
// are kept from the user; everything else comes from the template.
func MergeUserRequest(userBody []byte, tmpl *BodyTemplate, userID string) ([]byte, error) {
	var user map[string]any
	if err := json.Unmarshal(userBody, &user); err != nil {
		return nil, err
	}

	result := make(map[string]any, 16)

	// System: CC template blocks, user's system appended as CLAUDE.md-style
	// content. Strip cache_control from user blocks to stay within Anthropic's
	// 4-block cache_control limit (the template already uses all 4 slots).
	sysBlocks := append([]any{}, tmpl.System...)
	if userSys := mergeUserSystem(user["system"]); len(userSys) > 0 {
		for _, blk := range userSys {
			if m, ok := blk.(map[string]any); ok {
				delete(m, "cache_control")
			}
			sysBlocks = append(sysBlocks, blk)
		}
	}
	result["system"] = sysBlocks
	result["stream"] = tmpl.Stream
	if tmpl.OutputConfig != nil {
		result["output_config"] = tmpl.OutputConfig
	}
	if tmpl.Diagnostics != nil {
		result["diagnostics"] = tmpl.Diagnostics
	}
	if tmpl.Thinking != nil {
		result["thinking"] = tmpl.Thinking
	}
	result["metadata"] = map[string]any{"user_id": userID}

	// Tools: CC template tools ONLY. User tools are dropped to match real CC
	// fingerprint (real CC has a fixed set of ~10 tools).
	if len(tmpl.Tools) > 0 {
		result["tools"] = tmpl.Tools
	}

	// FROM USER: only model, messages, max_tokens, tool_choice, temperature
	result["model"] = user["model"]
	result["messages"] = user["messages"]

	if tc, ok := user["tool_choice"]; ok {
		result["tool_choice"] = tc
	}
	if mt, ok := user["max_tokens"].(float64); ok && mt > 0 {
		result["max_tokens"] = int(mt)
	} else if tmpl.MaxTokens > 0 {
		model, _ := user["model"].(string)
		result["max_tokens"] = defaultMaxTokens(model)
	}
	if temp, ok := user["temperature"]; ok {
		result["temperature"] = temp
	}
	if s, ok := user["stream"].(bool); ok {
		result["stream"] = s
	}

	// context_management with clear_thinking requires thinking to be enabled
	if tmpl.ContextManagement != nil {
		if _, hasThinking := result["thinking"]; hasThinking {
			result["context_management"] = tmpl.ContextManagement
		}
	}

	ensureThinkingFitsMaxTokens(result)
	ensureCacheControl(result)

	return marshalBody(result)
}

// mergeUserSystem converts the user's "system" field (string or block array)
// into []any text blocks suitable for appending to the template system.
func mergeUserSystem(sys any) []any {
	switch v := sys.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "text": v}}
	case []any:
		if len(v) == 0 {
			return nil
		}
		return v
	}
	return nil
}

// ExtractVersionFromUA parses version from "claude-cli/2.1.187 (...)" user-agent.
func ExtractVersionFromUA(ua string) string {
	if !strings.HasPrefix(ua, "claude-cli/") {
		return ""
	}
	v := strings.TrimPrefix(ua, "claude-cli/")
	if idx := strings.IndexByte(v, ' '); idx >= 0 {
		v = v[:idx]
	}
	return v
}
