package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// warmupPreloadJS intercepts globalThis.fetch inside the CC CLI process and
// writes the first large POST /v1/messages body (>10 KB = the main sonnet
// request, not the small haiku routing request) to a temp file, then restores
// the original fetch.  Loaded via NODE_OPTIONS=--require.
const warmupPreloadJS = `const _of=globalThis.fetch,_fs=require("fs");
globalThis.fetch=async function(u,o){
if(typeof u==="string"&&u.includes("/v1/messages")&&o&&typeof o.body==="string"&&o.body.length>10000){
try{_fs.writeFileSync(process.env._NE_BODY_PATH||"/tmp/ne_warmup_body.json",o.body)}catch(e){}
globalThis.fetch=_of}
return _of.apply(this,arguments)};
`

// warmupTemplate runs `claude -p "hi"` once at startup to learn the live
// fingerprint AND body template from a genuine CC request.  Runs in a
// background goroutine; failures are non-fatal (builtin fallbacks remain).
func warmupTemplate(claudePath, configDir string, fpCache *FPCache, btCache *BodyTemplateCache) {
	start := time.Now()
	tmpDir := os.TempDir()
	preloadPath := filepath.Join(tmpDir, "ne_warmup_preload.cjs")
	bodyPath := filepath.Join(tmpDir, "ne_warmup_body.json")

	os.Remove(bodyPath)
	if err := os.WriteFile(preloadPath, []byte(warmupPreloadJS), 0644); err != nil {
		logDD("warmup: write preload: %v", err)
		return
	}
	defer os.Remove(preloadPath)
	defer os.Remove(bodyPath)

	nodeOpts := "--require " + preloadPath
	if existing := os.Getenv("NODE_OPTIONS"); existing != "" {
		nodeOpts = existing + " " + nodeOpts
	}

	cmd := exec.Command(claudePath, "-p", "hi")
	cmd.Env = append(append([]string{}, osEnviron()...),
		"ANTHROPIC_LOG=debug",
		"CLAUDE_CONFIG_DIR="+resolveConfigDir(configDir),
		"NODE_OPTIONS="+nodeOpts,
		"_NE_BODY_PATH="+bodyPath,
	)

	out, _ := cmd.CombinedOutput()

	fp, ok := ParseFingerprint(string(out))
	if !ok {
		logDD("warmup: fingerprint parse failed (CC not logged in?)")
		return
	}

	fpCache.mu.Lock()
	fpCache.entries["default"] = fpEntry{fp: fp, capturedAt: time.Now()}
	fpCache.mu.Unlock()

	fpVersion := ExtractVersionFromUA(fp["user-agent"])
	fpBetas := fp["anthropic-beta"]
	fpNodeVer := fp["x-stainless-runtime-version"]
	logDD("warmup: fingerprint learned (CC %s, node %s)", fpVersion, fpNodeVer)

	bodyData, err := os.ReadFile(bodyPath)
	if err != nil || len(bodyData) == 0 {
		logDD("warmup: body dump not found (CC binary may not support NODE_OPTIONS)")
		return
	}

	btCache.LearnFromCC(bodyData, fpVersion, fpBetas, fpNodeVer)
	logDD("warmup: body template learned (%d bytes, %d tools) in %s",
		len(bodyData), countTemplateTools(bodyData), time.Since(start).Round(time.Millisecond))
}

func countTemplateTools(body []byte) int {
	var parsed struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		return len(parsed.Tools)
	}
	return -1
}
