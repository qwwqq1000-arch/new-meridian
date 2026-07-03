package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PrevReqStore tracks the last Anthropic request-id per session so we can
// populate cc_prev_req in the billing header (multi-turn chain signal).
type PrevReqStore struct {
	mu        sync.Mutex
	bySession map[string]string
}

func NewPrevReqStore() *PrevReqStore {
	return &PrevReqStore{bySession: make(map[string]string)}
}

func (s *PrevReqStore) Get(session string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bySession[session]
}

func (s *PrevReqStore) Set(session, reqID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bySession[session] = reqID
}

type RelayDeps struct {
	Transport    http.RoundTripper
	FP           *FPCache
	BodyTemplate *BodyTemplateCache
	SessionID    func(account string) string
	Now          func() time.Time
	Datadog      *DatadogEmitter
	PrevReq      *PrevReqStore
}

func relayHandler(d RelayDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relayStart := time.Now()
		rawBody, err := io.ReadAll(r.Body)
		if err != nil || len(rawBody) == 0 {
			degrade(w, "bad_request")
			return
		}
		configDir := r.Header.Get("X-Native-Config-Dir")
		account := r.Header.Get("X-Native-Account")
		stream := r.Header.Get("X-Native-Stream") == "1"
		clientBeta := r.Header.Get("X-Native-Anthropic-Beta")

		token, _, _, err := ReadToken(configDir)
		apiKey := r.Header.Get("X-Native-Api-Key") // sk-ant-* from client
		if (err != nil || token == "") && apiKey == "" {
			degrade(w, "no_token")
			return
		}
		useApiKey := token == "" && apiKey != ""

		fp, ok := d.FP.Get(account, configDir, d.Now())
		if !ok {
			degrade(w, "no_fingerprint")
			return
		}

		// ALL requests get wrapped in the CC template — no passthrough.
		var cloaked []byte
		tmpl := d.BodyTemplate.Get()
		if tmpl == nil {
			degrade(w, "no_template")
			return
		}
		sessionID := d.SessionID(account)
		bp := &BillingPatch{
			PrevReqID: d.PrevReq.Get(sessionID),
		}
		cloaked, err = MergeUserRequest(rawBody, tmpl, deriveUserID(account, readAccountUUID(configDir)), bp)
		if err != nil {
			degrade(w, "merge_error")
			return
		}

		if reason := ValidateBody(cloaked); reason != "" {
			rejectBody(w, reason)
			return
		}

		// Always stream from upstream — NE assembles to JSON for non-stream clients.
		clientRequestID := uuid.NewString()
		var headers http.Header
		if useApiKey {
			headers = BuildHeadersApiKey(fp, apiKey, sessionID, clientRequestID, true, clientBeta)
		} else {
			headers = BuildHeaders(fp, token, sessionID, clientRequestID, true, clientBeta)
		}

		upReq, err := http.NewRequestWithContext(r.Context(), "POST", "https://api.anthropic.com/v1/messages?beta=true", bytesReader(cloaked))
		if err != nil {
			degrade(w, "build_request_error")
			return
		}
		upReq.Header = headers

		logRelay(account, headers, cloaked)
		logMergeSummary(account, cloaked)

		// DEBUG: dump outbound request for comparison
		func() {
			hdrs := map[string]string{}
			for k := range headers {
				hdrs[strings.ToLower(k)] = headers.Get(k)
			}
			dump := map[string]any{"headers": hdrs}
			var bodyParsed map[string]any
			if json.Unmarshal(cloaked, &bodyParsed) == nil {
				sysSummary := []map[string]any{}
				if sysArr, ok := bodyParsed["system"].([]any); ok {
					for i, item := range sysArr {
						if b, ok := item.(map[string]any); ok {
							text, _ := b["text"].(string)
							start := text
							if len(start) > 120 { start = start[:120] }
							entry := map[string]any{"index": i, "type": b["type"], "len": len(text), "cache_control": b["cache_control"], "start": start}
							sysSummary = append(sysSummary, entry)
						}
					}
				}
				toolNames := []string{}
				if tools, ok := bodyParsed["tools"].([]any); ok {
					for _, t := range tools {
						if tm, ok := t.(map[string]any); ok {
							if n, ok := tm["name"].(string); ok { toolNames = append(toolNames, n) }
						}
					}
				}
				dump["model"] = bodyParsed["model"]
				dump["max_tokens"] = bodyParsed["max_tokens"]
				dump["stream"] = bodyParsed["stream"]
				dump["thinking"] = bodyParsed["thinking"]
				dump["context_management"] = bodyParsed["context_management"]
				dump["output_config"] = bodyParsed["output_config"]
				dump["metadata"] = bodyParsed["metadata"]
				dump["diagnostics"] = bodyParsed["diagnostics"]
				dump["tools_count"] = len(toolNames)
				dump["tools_names"] = toolNames
				dump["system_blocks"] = sysSummary
				if msgs, ok := bodyParsed["messages"].([]any); ok { dump["messages_count"] = len(msgs) }
			}
			dumpJSON, _ := json.MarshalIndent(dump, "", "  ")
			os.WriteFile("/tmp/relay-outbound.json", dumpJSON, 0644)
		}()

		resp, err := d.Transport.RoundTrip(upReq)
		if err != nil {
			degrade(w, "upstream_error")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			logUpstreamError(resp.StatusCode, errBody)

			// Auto-retry on expired thinking signature: strip thinking blocks and resend.
			if resp.StatusCode == 400 && (bytes.Contains(errBody, []byte("signature")) || bytes.Contains(errBody, []byte("cannot be modified"))) {
				resp.Body.Close()
				stripped := stripThinkingBlocks(cloaked)
				if stripped != nil {
					logDD("thinking signature expired, retrying without thinking blocks")
					retryReq, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(stripped))
					if err == nil {
						retryReq.Header = headers
						retryReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(stripped)))
						resp2, err2 := d.Transport.RoundTrip(retryReq)
						if err2 == nil {
							defer resp2.Body.Close()
							if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
								resp = resp2
								goto handleSuccess
							}
							errBody, _ = io.ReadAll(io.LimitReader(resp2.Body, 8192))
							logUpstreamError(resp2.StatusCode, errBody)
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(resp2.StatusCode)
							w.Write(errBody)
							return
						}
					}
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(errBody)
			return
		}
	handleSuccess:

		requestID := resp.Header.Get("Request-Id")
		if requestID != "" {
			d.PrevReq.Set(sessionID, requestID)
		}
		// Track upstream TTFB: time from relay start to first upstream byte.
		upstreamTTFB := time.Since(relayStart).Milliseconds()

		if !stream {
			ct := resp.Header.Get("Content-Type")
			isJSON := strings.HasPrefix(ct, "application/json")

			var assembled []byte
			if isJSON {
				// Upstream returned a non-streaming JSON response directly.
				assembled, _ = io.ReadAll(resp.Body)
			} else {
				// Client wants non-streaming: read full SSE, assemble final Message JSON.
				var assembleErr error
				assembled, assembleErr = assembleSSEToMessage(resp.Body)
				if assembleErr != nil {
					logDD("sse_assemble_error: %v", assembleErr)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(502)
					if assembled != nil {
						w.Write(assembled)
					} else {
						fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"SSE assembly failed: %s"}}`, assembleErr.Error())
					}
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Upstream-TTFB-Ms", fmt.Sprintf("%d", upstreamTTFB))
			if requestID != "" {
				w.Header().Set("Request-Id", requestID)
			}
			input, output, cached, cacheCreation, _, _ := extractResponseMeta(assembled)
			w.Header().Set("X-Usage-Input", fmt.Sprintf("%d", input))
			w.Header().Set("X-Usage-Output", fmt.Sprintf("%d", output))
			w.Header().Set("X-Usage-Cache-Read", fmt.Sprintf("%d", cached))
			w.Header().Set("X-Usage-Cache-Creation", fmt.Sprintf("%d", cacheCreation))
			w.WriteHeader(200)
			w.Write(assembled)

			if d.Datadog != nil {
				relayDuration := time.Since(relayStart).Milliseconds()
				model := extractModel(rawBody)
				input, output, cached, _, stopReason, toolCount := extractResponseMeta(assembled)
				d.Datadog.EmitAfterRelay(d.SessionID(account), model, requestID, stopReason,
					input, output, cached, toolCount, relayDuration, len(rawBody))
			}
			return
		}

		// Streaming: forward SSE events to client.
		for k, vs := range resp.Header {
			kl := strings.ToLower(k)
			if kl == "content-encoding" || kl == "content-length" {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("X-Upstream-TTFB-Ms", fmt.Sprintf("%d", upstreamTTFB))
		w.WriteHeader(resp.StatusCode)
		rc := http.NewResponseController(w)
		buf := make([]byte, 16*1024)
		var respCapture bytes.Buffer
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					break
				}
				_ = rc.Flush()
				if respCapture.Len() < 32*1024 {
					respCapture.Write(buf[:n])
				}
			}
			if rerr != nil {
				break
			}
		}

		if d.Datadog != nil {
			relayDuration := time.Since(relayStart).Milliseconds()
			model := extractModel(rawBody)
			input, output, cached, _, stopReason, toolCount := extractResponseMeta(respCapture.Bytes())
			d.Datadog.EmitAfterRelay(d.SessionID(account), model, requestID, stopReason,
				input, output, cached, toolCount, relayDuration, len(rawBody))
		}
	}
}

func bodyHasClaudeIdentity(raw []byte) bool {
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	return hasClaudeIdentity(body["system"])
}

func degrade(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(502)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"native-egress: %s"}}`, reason)
}

// rejectBody returns a 400 in Anthropic error format without hitting the API.
func rejectBody(w http.ResponseWriter, message string) {
	logDD("pre-validate reject: %s", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	resp := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func bytesReader(b []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(b))
}


func extractModel(body []byte) string {
	var m struct{ Model string `json:"model"` }
	if json.Unmarshal(body, &m) == nil && m.Model != "" {
		return m.Model
	}
	return "claude-sonnet-4-6"
}

func extractResponseMeta(respData []byte) (input, output, cached, cacheCreation int, stopReason string, toolCount int) {
	var msg struct {
		Usage struct {
			InputTokens               int `json:"input_tokens"`
			OutputTokens              int `json:"output_tokens"`
			CacheReadInputTokens      int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if json.Unmarshal(respData, &msg) == nil && (msg.Usage.InputTokens > 0 || msg.Usage.CacheReadInputTokens > 0) {
		for _, c := range msg.Content {
			if c.Type == "tool_use" {
				toolCount++
			}
		}
		return msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens, msg.StopReason, toolCount
	}
	for _, line := range bytes.Split(respData, []byte("\n")) {
		line = bytes.TrimPrefix(line, []byte("data: "))
		if json.Unmarshal(line, &msg) == nil {
			if msg.Usage.InputTokens > 0 || msg.Usage.CacheReadInputTokens > 0 {
				input = msg.Usage.InputTokens
				output = msg.Usage.OutputTokens
				cached = msg.Usage.CacheReadInputTokens
				cacheCreation = msg.Usage.CacheCreationInputTokens
			}
			if msg.StopReason != "" {
				stopReason = msg.StopReason
			}
			for _, c := range msg.Content {
				if c.Type == "tool_use" {
					toolCount++
				}
			}
		}
	}
	return
}
