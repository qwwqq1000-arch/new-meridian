import { describe, it, expect, mock, beforeEach, afterEach } from "bun:test"

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => {
    sdkInvoked = true
    return (async function* () { /* should NOT be called in native mode */ })()
  },
  createSdkMcpServer: () => ({ type: "sdk", name: "test", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

// sdkInvoked is flipped by the SDK mock above to verify the native branch bypasses the SDK.
let sdkInvoked = false

const { createProxyServer, clearSessionCache } = await import("../proxy/server")
const { __setFingerprintOverride } = await import("../proxy/claudeEnvelope")

describe("native relay branch", () => {
  beforeEach(() => {
    clearSessionCache()
    sdkInvoked = false
    // Provide a fake fingerprint so the native code path proceeds.
    __setFingerprintOverride(async () => ({ "user-agent": "claude-cli/test" }))
  })

  afterEach(() => {
    __setFingerprintOverride(null)
  })

  it("routes to forwardNative (bypassing the SDK) when relayMode=native", async () => {
    // Use a claudeConfigDir with NO stored credentials so forwardNative resolves
    // no OAuth token and returns its 400 auth-error Response. This proves the
    // native branch was taken and the SDK was never invoked — without globally
    // mocking our own modules (which would leak across Bun's parallel test workers).
    const { app } = createProxyServer({
      profiles: [{ id: "p", type: "claude-max", claudeConfigDir: "/tmp/meridian-native-test-nocreds" }],
      defaultProfile: "p",
    } as Parameters<typeof createProxyServer>[0])

    const res = await app.request("/v1/messages", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-meridian-profile": "p",
        "x-meridian-mode": "native",
      },
      body: JSON.stringify({ model: "claude-3", system: "s", messages: [{ role: "user", content: "hi" }], stream: false }),
    })

    expect(res.status).toBe(400)
    const body = await res.json() as { error?: { type?: string } }
    expect(body.error?.type).toBe("authentication_error")
    expect(sdkInvoked).toBe(false)
  })
})
