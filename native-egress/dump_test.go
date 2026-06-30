package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestDumpMergedRequest(t *testing.T) {
	tmpl := builtinTemplate()
	if tmpl == nil {
		t.Fatal("no builtin template")
	}

	userReq := `{"model":"claude-sonnet-4-6","max_tokens":100,"messages":[{"role":"user","content":"say hi"}]}`
	merged, err := MergeUserRequest([]byte(userReq), tmpl, deriveUserID("default"))
	if err != nil {
		t.Fatal(err)
	}

	// Pretty print
	var pretty map[string]any
	json.Unmarshal(merged, &pretty)
	out, _ := json.MarshalIndent(pretty, "", "  ")
	os.WriteFile("/tmp/server_request.json", out, 0644)
	fmt.Printf("Written to /tmp/server_request.json (%d bytes)\n", len(out))
}
