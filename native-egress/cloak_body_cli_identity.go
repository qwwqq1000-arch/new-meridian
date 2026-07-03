package main

import "strings"

// hasCLIIdentity checks whether the system field carries any known CLI identity
// (Claude Code or Agent SDK). Used by warmup to accept captured bodies from
// either SDK mode. normalizeSystem still uses hasClaudeIdentity exclusively to
// control CC identity injection during relay.
func hasCLIIdentity(sys any) bool {
	if hasClaudeIdentity(sys) {
		return true
	}
	switch v := sys.(type) {
	case string:
		return strings.HasPrefix(strings.TrimLeft(v, " \t\r\n"), agentSDKIdentityPrefix)
	case []any:
		for _, item := range v {
			if b, ok := item.(map[string]any); ok {
				if text, ok := b["text"].(string); ok && strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), agentSDKIdentityPrefix) {
					return true
				}
			}
		}
	}
	return false
}
