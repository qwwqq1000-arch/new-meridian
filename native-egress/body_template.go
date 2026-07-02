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

	// System: template blocks ONLY. User's system is discarded — real CC never
	// has extra system blocks appended, and they are a fingerprint tell.
	result["system"] = append([]any{}, tmpl.System...)

	// output_config, thinking from template (includes display:"omitted")
	if tmpl.OutputConfig != nil {
		result["output_config"] = tmpl.OutputConfig
	}
	if tmpl.Thinking != nil {
		result["thinking"] = tmpl.Thinking
	}
	result["metadata"] = map[string]any{"user_id": userID}

	// Tools: template tools ONLY. User tools are discarded — real CC has a
	// fixed set (28 in 2.1.198); extra tools are a fingerprint tell.
	if len(tmpl.Tools) > 0 {
		result["tools"] = append([]any{}, tmpl.Tools...)
	}

	// FROM USER: only model, messages, max_tokens, tool_choice
	result["model"] = user["model"]
	result["messages"] = stripEmptyTextBlocks(user["messages"])

	model, _ := user["model"].(string)
	result["max_tokens"] = defaultMaxTokens(model)

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

// stripEmptyTextBlocks removes {"type":"text","text":""} from message content
// arrays. Some clients send these as placeholders; the API rejects them.
func stripEmptyTextBlocks(msgs any) any {
	arr, _ := msgs.([]any)
	if arr == nil {
		return msgs
	}
	for _, m := range arr {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		content, _ := mm["content"].([]any)
		if content == nil {
			continue
		}
		filtered := content[:0]
		for _, c := range content {
			block, _ := c.(map[string]any)
			if block != nil && block["type"] == "text" {
				text, _ := block["text"].(string)
				if text == "" {
					continue
				}
			}
			filtered = append(filtered, c)
		}
		if len(filtered) == 0 && len(content) > 0 {
			filtered = append(filtered, map[string]any{"type": "text", "text": "."})
		}
		mm["content"] = filtered
	}
	return msgs
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
