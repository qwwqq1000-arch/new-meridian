import { describe, it, expect, mock, beforeEach } from "bun:test"

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => (async function* () { /* should NOT be called in native mode */ })(),
  createSdkMcpServer: () => ({ type: "sdk", name: "test", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

// Force native relay + a fake upstream so no real network/credential access happens.
mock.module("../proxy/transparentRelay", () => ({
  forwardNative: async () => new Response(JSON.stringify({ type: "message", relayed: true }), { status: 200, headers: { "content-type": "application/json" } }),
  ensureClaudeCodeIdentity: (s: string) => s,
  buildRelayHeaders: () => ({}),
  CLAUDE_CODE_IDENTITY: "x",
}))
// Fingerprint present → native branch proceeds. (A null return would degrade to passthrough.)
mock.module("../proxy/claudeEnvelope", () => ({
  getFingerprint: async () => ({ "user-agent": "claude-cli/2.1.0" }),
  filterFingerprintHeaders: (h: Record<string, string>) => h,
  getCachedFingerprint: () => null,
  setCachedFingerprint: () => {},
  resetEnvelopeCache: () => {},
}))

const { createProxyServer, clearSessionCache } = await import("../proxy/server")

describe("native relay branch", () => {
  beforeEach(() => clearSessionCache())

  it("routes to forwardNative (bypassing the SDK) when relayMode=native and profile has OAuth", async () => {
    // Use x-meridian-mode: native header to force native relay without mocking sdkFeatures,
    // avoiding module-level mock contamination of other test files in the same Bun worker.
    const { app } = createProxyServer({ profiles: [{ id: "p", type: "claude-max", claudeConfigDir: "/tmp/x" }], defaultProfile: "p" } as Parameters<typeof createProxyServer>[0])
    const res = await app.request("/v1/messages", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-meridian-profile": "p",
        "x-meridian-mode": "native",
      },
      body: JSON.stringify({ model: "claude-3", system: "s", messages: [{ role: "user", content: "hi" }], stream: false }),
    })
    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({ type: "message", relayed: true })
  })
})
