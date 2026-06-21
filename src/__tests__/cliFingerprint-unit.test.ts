import { describe, it, expect, beforeEach } from "bun:test"
import {
  parseTtlMs,
  parseFingerprintFromDebugLog,
  getCachedCliFingerprint,
  setCachedCliFingerprint,
  resetCliFingerprintCache,
} from "../proxy/cliFingerprint"

// A representative slice of `ANTHROPIC_LOG=debug claude -p hi` output.
const SAMPLE_DEBUG = `
[log_2dca67] sending request {
  method: "post",
  url: "https://api.anthropic.com/v1/messages?beta=true",
  options: {
    headers: {
      "anthropic-beta": "claude-code-20250219,oauth-2025-04-20,effort-2025-11-24",
      "anthropic-version": "2023-06-01",
      "user-agent": "claude-cli/2.1.185 (external, cli, agent-sdk/0.3.183)",
      "x-app": "cli",
      "x-stainless-arch": "arm64",
      "x-stainless-lang": "js",
      "x-stainless-os": "Linux",
      "x-stainless-package-version": "0.94.0",
      "x-stainless-retry-count": "0",
      "x-stainless-runtime": "node",
      "x-stainless-runtime-version": "v24.3.0",
      "x-stainless-timeout": "600"
    }
  }
}
`

describe("parseTtlMs", () => {
  it("parses h/m/s suffixes", () => {
    expect(parseTtlMs("1h")).toBe(3_600_000)
    expect(parseTtlMs("5m")).toBe(300_000)
    expect(parseTtlMs("30s")).toBe(30_000)
  })
  it("defaults to 5m when unset, empty, or malformed", () => {
    expect(parseTtlMs(undefined)).toBe(300_000)
    expect(parseTtlMs("")).toBe(300_000)
    expect(parseTtlMs("garbage")).toBe(300_000)
  })
})

describe("parseFingerprintFromDebugLog", () => {
  it("extracts the genuine CLI fingerprint headers", () => {
    const fp = parseFingerprintFromDebugLog(SAMPLE_DEBUG)
    expect(fp).not.toBeNull()
    expect(fp!["user-agent"]).toBe("claude-cli/2.1.185 (external, cli, agent-sdk/0.3.183)")
    expect(fp!["x-app"]).toBe("cli")
    expect(fp!["x-stainless-os"]).toBe("Linux")
    expect(fp!["x-stainless-arch"]).toBe("arm64")
    expect(fp!["x-stainless-package-version"]).toBe("0.94.0")
    expect(fp!["x-stainless-runtime"]).toBe("node")
    expect(fp!["x-stainless-runtime-version"]).toBe("v24.3.0")
  })
  it("drops the per-request retry-count (set fresh per relay, not replayed)", () => {
    const fp = parseFingerprintFromDebugLog(SAMPLE_DEBUG)
    expect(fp!["x-stainless-retry-count"]).toBeUndefined()
  })
  it("returns null when no claude-cli user-agent is present", () => {
    expect(parseFingerprintFromDebugLog('headers: { "user-agent": "Go-http-client/1.1" }')).toBeNull()
    expect(parseFingerprintFromDebugLog("no headers here")).toBeNull()
  })
})

describe("fingerprint cache (TTL)", () => {
  beforeEach(() => resetCliFingerprintCache())
  it("returns a cached fingerprint within the TTL and null after it expires", () => {
    const fp = { "user-agent": "claude-cli/2.1.185" }
    setCachedCliFingerprint("k", fp, 1000, 100)            // capturedAt=100, ttl=1000
    expect(getCachedCliFingerprint("k", 100)).toEqual(fp)  // same instant
    expect(getCachedCliFingerprint("k", 1099)).toEqual(fp) // within TTL
    expect(getCachedCliFingerprint("k", 1101)).toBeNull()  // expired
  })
  it("is keyed (different key → miss)", () => {
    setCachedCliFingerprint("k1", { "user-agent": "claude-cli/x" }, 1000, 0)
    expect(getCachedCliFingerprint("k2", 0)).toBeNull()
  })
})
