import { describe, it, expect, mock, beforeEach, afterEach } from "bun:test"

// Track whether the SDK was invoked (proves native bypassed it, or that a
// rejected non-CC request fell through to the SDK). Declared before the mock
// block so the factory closure captures the initialized binding.
let sdkInvoked = false

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => { sdkInvoked = true; return (async function* () { /* empty: native should not reach here */ })() },
  createSdkMcpServer: () => ({ type: "sdk", name: "test", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

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

describe("native relay branch", () => {
  let savedEnv: string | undefined
  beforeEach(() => {
    clearSessionCache()
    sdkInvoked = false
    savedEnv = process.env.MERIDIAN_NATIVE_FORWARD
    process.env.MERIDIAN_NATIVE_FORWARD = "1" // server-side enable (no client header can enable native)
  })
  afterEach(() => {
    if (savedEnv === undefined) delete process.env.MERIDIAN_NATIVE_FORWARD
    else process.env.MERIDIAN_NATIVE_FORWARD = savedEnv
  })

  it("forwards a CC-shaped request natively (bypassing the SDK)", async () => {
    // No creds at the profile's config dir → forwardNative resolves no token and
    // returns its 400 native auth-error. That 400 (+ SDK never invoked) proves
    // the native branch ran the real forwardNative.
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(res.status).toBe(400)
    const j = await res.json() as { error?: { type?: string } }
    expect(j.error?.type).toBe("authentication_error")
    expect(sdkInvoked).toBe(false)
  })

  it("rejects a forged non-CC body (body check) and falls through to the SDK", async () => {
    // Same native-enabled env, but the body is not Claude-Code-shaped (OpenCode
    // identity + lowercase tools). The anti-forgery check must refuse native and
    // hand off to the SDK path → forwardNative NOT called, SDK invoked.
    const nonCcBody = {
      ...ccShapedBody,
      system: [{ type: "text", text: "You are OpenCode." }],
      tools: [{ name: "read" }, { name: "write" }, { name: "bash" }, { name: "edit" }],
    }
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0" },
      body: JSON.stringify(nonCcBody),
    }))
    expect(sdkInvoked).toBe(true)
    // And NOT the native 400 auth-error shape.
    const j = await res.json().catch(() => ({})) as { error?: { type?: string } }
    expect(j.error?.type).not.toBe("authentication_error")
  })

  it("client x-meridian-mode:sdk opts OUT of native even when enabled server-side", async () => {
    const res = await makeApp().fetch(new Request("http://localhost/v1/messages", {
      method: "POST",
      headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0", "x-meridian-mode": "sdk" },
      body: JSON.stringify(ccShapedBody),
    }))
    expect(sdkInvoked).toBe(true)
    const j = await res.json().catch(() => ({})) as { error?: { type?: string } }
    expect(j.error?.type).not.toBe("authentication_error")
  })
})
