import { describe, it, expect, mock, beforeEach, afterEach } from "bun:test"

let sdkInvoked = false

mock.module("@anthropic-ai/claude-agent-sdk", () => ({
  query: () => { sdkInvoked = true; return (async function* () { /* empty: native should not reach here */ })() },
  createSdkMcpServer: () => ({ type: "sdk", name: "test", instance: {} }),
  tool: () => ({}),
}))
mock.module("../logger", () => ({ claudeLog: () => {}, withClaudeLogContext: (_c: unknown, fn: () => unknown) => fn() }))

const { createProxyServer, clearSessionCache } = await import("../proxy/server")
const { __setCliFingerprintForTest } = await import("../proxy/cliFingerprint")

const CC_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."
const ccShapedBody = {
  model: "claude-opus-4-8",
  system: [{ type: "text", text: `${CC_IDENTITY}\n\nInteractive CLI tool.` }],
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

function post(app: { fetch: (r: Request) => Response | Promise<Response> }, body: unknown, extraHeaders: Record<string, string> = {}) {
  return app.fetch(new Request("http://localhost/v1/messages", {
    method: "POST",
    headers: { "content-type": "application/json", "x-meridian-profile": "p", "user-agent": "claude-cli/2.1.0", ...extraHeaders },
    body: JSON.stringify(body),
  }))
}

describe("native relay branch", () => {
  let savedEnv: string | undefined
  beforeEach(() => {
    clearSessionCache()
    sdkInvoked = false
    __setCliFingerprintForTest({ "user-agent": "claude-cli/2.1.185 (external, cli)", "x-app": "cli", "x-stainless-os": "Linux" })
    savedEnv = process.env.MERIDIAN_NATIVE_FORWARD
    process.env.MERIDIAN_NATIVE_FORWARD = "1"
  })
  afterEach(() => {
    __setCliFingerprintForTest(undefined)
    if (savedEnv === undefined) delete process.env.MERIDIAN_NATIVE_FORWARD
    else process.env.MERIDIAN_NATIVE_FORWARD = savedEnv
  })

  it("forwards a CC-shaped request natively (real forwardNative, no creds → 400; SDK bypassed)", async () => {
    const res = await post(makeApp(), ccShapedBody)
    expect(res.status).toBe(400)
    const j = await res.json() as { error?: { type?: string } }
    expect(j.error?.type).toBe("authentication_error")
    expect(sdkInvoked).toBe(false)
  })

  it("rejects a forged non-CC body (body check) and falls through to the SDK", async () => {
    const nonCc = { ...ccShapedBody, system: [{ type: "text", text: "You are OpenCode." }], tools: [{ name: "read" }, { name: "write" }] }
    await post(makeApp(), nonCc)
    expect(sdkInvoked).toBe(true)
  })

  it("client x-meridian-mode:sdk opts OUT of native even when enabled server-side", async () => {
    await post(makeApp(), ccShapedBody, { "x-meridian-mode": "sdk" })
    expect(sdkInvoked).toBe(true)
  })
})
