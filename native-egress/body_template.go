package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

type BillingPatch struct {
	CCH           string // 5-char hex, random per request
	PrevReqID     string // req_xxx from last response (empty on first turn)
	VersionSuffix string // 3-char hex, SHA256-derived from first user message
}

const versionSuffixSalt = "59cf53e54c78"

func ComputeVersionSuffix(firstUserMsg, version string) string {
	charAt := func(s string, i int) byte {
		if i < len(s) {
			return s[i]
		}
		return '0'
	}
	chars := string([]byte{charAt(firstUserMsg, 4), charAt(firstUserMsg, 7), charAt(firstUserMsg, 20)})
	h := sha256.Sum256([]byte(versionSuffixSalt + chars + version))
	return hex.EncodeToString(h[:])[:3]
}

// patchBillingHeader rewrites the x-anthropic-billing-header system block to
// add cch, cc_prev_req, and the version sub-build suffix that real CLI sends.
func patchBillingHeader(system []any, bp *BillingPatch) {
	if bp == nil {
		return
	}
	for _, block := range system {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		if !strings.Contains(text, "x-anthropic-billing-header:") {
			continue
		}

		// Inject version suffix: cc_version=2.1.198; → cc_version=2.1.198.a3f;
		if bp.VersionSuffix != "" {
			text = injectVersionSuffix(text, bp.VersionSuffix)
		}
		// Append cch
		if bp.CCH != "" && !strings.Contains(text, "cch=") {
			text = strings.TrimRight(text, " ;") + "; cch=" + bp.CCH + ";"
		}
		// Append cc_prev_req
		if bp.PrevReqID != "" && !strings.Contains(text, "cc_prev_req=") {
			text = strings.TrimRight(text, " ;") + "; cc_prev_req=" + bp.PrevReqID + ";"
		}

		m["text"] = text
		return
	}
}

// injectVersionSuffix turns "cc_version=2.1.198;" into "cc_version=2.1.198.a3f;"
func injectVersionSuffix(text, suffix string) string {
	const prefix = "cc_version="
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return text
	}
	start := idx + len(prefix)
	end := strings.IndexByte(text[start:], ';')
	if end < 0 {
		return text
	}
	end += start
	ver := text[start:end]
	// Only add suffix if not already present (no dot after base version)
	if strings.Count(ver, ".") < 3 {
		return text[:end] + "." + suffix + text[end:]
	}
	return text
}

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
func MergeUserRequest(userBody []byte, tmpl *BodyTemplate, userID string, billingPatch *BillingPatch) ([]byte, error) {
	var user map[string]any
	if err := json.Unmarshal(userBody, &user); err != nil {
		return nil, err
	}

	model, _ := user["model"].(string)
	result := make(map[string]any, 16)

	// ── ① TEMPLATE FIXED (always overwrite) ──
	sysCopy := append([]any{}, tmpl.System...)
	patchBillingHeader(sysCopy, billingPatch)
	result["system"] = sysCopy
	result["metadata"] = map[string]any{"user_id": userID}
	if tmpl.ContextManagement != nil {
		result["context_management"] = tmpl.ContextManagement
	}

	// Tools: template base + user extras (MCP tools, ToolSearch-loaded, etc.)
	if len(tmpl.Tools) > 0 {
		tmplNames := make(map[string]bool, len(tmpl.Tools))
		for _, t := range tmpl.Tools {
			if tm, ok := t.(map[string]any); ok {
				if n, ok := tm["name"].(string); ok {
					tmplNames[n] = true
				}
			}
		}
		merged := append([]any{}, tmpl.Tools...)
		if userTools, ok := user["tools"].([]any); ok {
			for _, t := range userTools {
				if tm, ok := t.(map[string]any); ok {
					if n, ok := tm["name"].(string); ok && !tmplNames[n] {
						delete(tm, "cache_control")
						merged = append(merged, t)
					}
				}
			}
		}
		result["tools"] = merged
	}

	// ── ② MODEL-DERIVED (auto from model name) ──
	result["max_tokens"] = modelMaxTokens(model)
	result["thinking"] = modelThinking(model)
	if oc := modelOutputConfig(model); oc != nil {
		result["output_config"] = oc
	}

	// ── ③ USER PASSTHROUGH ──
	result["model"] = user["model"]
	result["messages"] = stripEmptyTextBlocks(user["messages"])
	if s, ok := user["stream"].(bool); ok && s {
		result["stream"] = true
	}

	ensureThinkingFitsMaxTokens(result)
	ensureCacheControl(result)

	return marshalBody(result)
}

func modelMaxTokens(model string) int {
	if isNewModel(model) {
		return 64000
	}
	return 32000
}

// isNewModel returns true for models that use 64000 max_tokens and mid-conversation beta.
// Old models (sonnet-4-6, opus-4-6, haiku) use 32000.
func isNewModel(model string) bool {
	if strings.Contains(model, "haiku") {
		return false
	}
	if strings.Contains(model, "sonnet-4") {
		return false
	}
	if strings.Contains(model, "opus-4-6") {
		return false
	}
	return true
}

func modelThinking(model string) map[string]any {
	if strings.Contains(model, "haiku") {
		return map[string]any{
			"type":          "enabled",
			"budget_tokens": 31999,
			"display":       "omitted",
		}
	}
	return map[string]any{
		"type":    "adaptive",
		"display": "omitted",
	}
}

func modelOutputConfig(model string) map[string]any {
	if strings.Contains(model, "haiku") {
		return nil
	}
	return map[string]any{"effort": "high"}
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

func ExtractVersionFromBilling(text string) string {
	const prefix = "cc_version="
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	s := text[idx+len(prefix):]
	if semi := strings.IndexByte(s, ';'); semi >= 0 {
		s = s[:semi]
	}
	s = strings.TrimRight(s, " ")
	parts := strings.SplitN(s, ".", 4)
	if len(parts) >= 3 {
		return parts[0] + "." + parts[1] + "." + parts[2]
	}
	return s
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
