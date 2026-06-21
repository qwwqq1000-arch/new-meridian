import { describe, it, expect, mock, beforeEach, afterEach } from "bun:test"

// Track whether the SDK was invoked (proves native bypassed it, or that a
// degraded/rejected request fell through to the SDK). Declared before mock blocks
// so the factory closures capture the initialized binding.
let sdkInvoked = false

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => { sdkInvoked = true; return (async function* () {})() },
  createSdkMcpServer: () => ({ type: "sdk", name: "t", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))
mock.module("../proxy/nativeSupervisor", () => ({
  CircuitBreaker: class {
    isOpen() { return false }
    recordFailure() {}
    recordSuccess() {}
  },
  getNativeBaseUrl: () => "http://127.0.0.1:65500",
}))

let degradeNext = false
mock.module("../proxy/nativeClient", () => ({
  forwardToNative: async () => degradeNext
    ? { degraded: true, reason: "upstream_429" }
    : { degraded: false, response: new Response(JSON.stringify({ relayed: true }), { status: 200 }) },
}))

const { createProxyServer, clearSessionCache } = await import("../proxy/server")

const CC_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."
const ccShapedBody = {
  model: "claude-3",
  system: [{ type: "text", text: `${CC_IDENTITY}\n\nYou are an interactive CLI tool.` }],
  tools: [{ name: "Bash" }, { name: "Read" }, { name: "Edit" }, { name: "Write" }],
  messages: [{ role: "user", content: "hi" }],
  stream: false,
}

function makeApp() {
  return createProxyServer({
    profiles: [{ id: "p", type: "claude-max", claudeConfigDir: "/tmp/meridian-native-test-nocreds" }],
    defaultProfile: "p",
  } as Parameters<typeof createProxyServer>[0]).app
}

describe("native relay branch (Go sidecar delegation)", () => {
  let savedEnv: string | undefined
  beforeEach(() => {
    clearSessionCache()
    sdkInvoked = false
    degradeNext = false
    savedEnv = process.env.MERIDIAN_NATIVE_FORWARD
    process.env.MERIDIAN_NATIVE_FORWARD = "1"
  })
  afterEach(() => {
    if (savedEnv === undefined) delete process.env.MERIDIAN_NATIVE_FORWARD
    else process.env.MERIDIAN_NATIVE_FORWARD = savedEnv
  })

  it("(a) not-degraded: returns native response, SDK not invoked", async () => {
    degradeNext = false
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(res.status).toBe(200)
    const j = await res.json() as { relayed?: boolean }
    expect(j.relayed).toBe(true)
    expect(sdkInvoked).toBe(false)
  })

  it("(b) degraded: SDK invoked (falls through to SDK path)", async () => {
    degradeNext = true
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(sdkInvoked).toBe(true)
    // Response from SDK (not the native response)
    const j = await res.json().catch(() => ({})) as { relayed?: boolean }
    expect(j.relayed).toBeUndefined()
  })
})
