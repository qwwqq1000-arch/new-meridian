package main

import (
	"encoding/json"
	"os"
)

func readAccountUUID(configDir string) string {
	data, err := os.ReadFile(configDir + "/.claude.json")
	if err != nil {
		return ""
	}
	var d map[string]any
	if json.Unmarshal(data, &d) != nil {
		return ""
	}
	if oa, ok := d["oauthAccount"].(map[string]any); ok {
		if uuid, ok := oa["accountUuid"].(string); ok {
			return uuid
		}
	}
	return ""
}
