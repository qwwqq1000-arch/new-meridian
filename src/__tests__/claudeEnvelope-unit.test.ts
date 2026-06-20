import { describe, it, expect, beforeEach } from "bun:test"
import {
  filterFingerprintHeaders,
  getCachedFingerprint,
  setCachedFingerprint,
  resetEnvelopeCache,
  BASELINE_FINGERPRINT,
} from "../proxy/claudeEnvelope"

describe("filterFingerprintHeaders", () => {
  it("keeps fingerprint headers and x-stainless-* statics", () => {
    const fp = filterFingerprintHeaders({
      "user-agent": "claude-cli/2.1.0",
      "anthropic-version": "2023-06-01",
      "anthropic-beta": "claude-code-20250219",
      "x-app": "cli",
      "x-stainless-lang": "js",
      "x-stainless-os": "MacOS",
    })
    expect(fp["user-agent"]).toBe("claude-cli/2.1.0")
    expect(fp["x-stainless-lang"]).toBe("js")
    expect(fp["x-app"]).toBe("cli")
  })

  it("drops auth and per-request headers", () => {
    const fp = filterFingerprintHeaders({
      "user-agent": "claude-cli/2.1.0",
      authorization: "Bearer secret",
      "x-api-key": "k",
      "content-length": "10",
      host: "api.anthropic.com",
      "x-stainless-retry-count": "0",
      "x-stainless-timeout": "60",
    })
    expect(fp["authorization"]).toBeUndefined()
    expect(fp["x-api-key"]).toBeUndefined()
    expect(fp["content-length"]).toBeUndefined()
    expect(fp["x-stainless-retry-count"]).toBeUndefined()
    expect(fp["x-stainless-timeout"]).toBeUndefined()
  })
})

describe("fingerprint cache", () => {
  beforeEach(() => resetEnvelopeCache())

  it("returns null before a capture and the value after", () => {
    expect(getCachedFingerprint("v2.1.0")).toBeNull()
    setCachedFingerprint("v2.1.0", { "user-agent": "claude-cli/2.1.0" })
    expect(getCachedFingerprint("v2.1.0")).toEqual({ "user-agent": "claude-cli/2.1.0" })
  })

  it("is keyed by version (different key → miss)", () => {
    setCachedFingerprint("v2.1.0", { "user-agent": "claude-cli/2.1.0" })
    expect(getCachedFingerprint("v2.2.0")).toBeNull()
  })
})

describe("BASELINE_FINGERPRINT", () => {
  it("has the mandatory static headers", () => {
    expect(BASELINE_FINGERPRINT["anthropic-version"]).toBe("2023-06-01")
    expect(BASELINE_FINGERPRINT["user-agent"]).toMatch(/^claude-cli\//)
    expect(BASELINE_FINGERPRINT["x-app"]).toBe("cli")
  })
})
