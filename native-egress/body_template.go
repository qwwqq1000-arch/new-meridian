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

	// FROM TEMPLATE (the real CLI structure)
	result["system"] = tmpl.System
	result["stream"] = tmpl.Stream
	if tmpl.ContextManagement != nil {
		result["context_management"] = tmpl.ContextManagement
	}
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

	// Use template tools as default, user tools override
	if userTools, ok := user["tools"].([]any); ok && len(userTools) > 0 {
		result["tools"] = userTools
	} else if len(tmpl.Tools) > 0 {
		result["tools"] = tmpl.Tools
	}

	// FROM USER (content + preferences)
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
	// User can override stream
	if s, ok := user["stream"].(bool); ok {
		result["stream"] = s
	}
	// User can override thinking
	if th, ok := user["thinking"]; ok {
		result["thinking"] = th
	}

	// Never inflate user's max_tokens — shrink thinking to fit
	ensureThinkingFitsMaxTokens(result)
	ensureCacheControl(result)

	return marshalBody(result)
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
