import { describe, it, expect } from "bun:test"
import { forwardNative } from "../proxy/transparentRelay"
import type { CredentialStore } from "../proxy/tokenRefresh"

const fakeStore = (token: string | null): CredentialStore => ({
  read: async () => token ? { claudeAiOauth: { accessToken: token, refreshToken: "r", expiresAt: Date.now() + 1e9 } } : null,
  write: async () => true,
})
const fingerprint = { "user-agent": "claude-cli/2.1.185 (external, cli)", "x-app": "cli", "x-stainless-os": "Linux" }

describe("forwardNative", () => {
  it("forwards verbatim body to ?beta=true with Bearer + genuine fingerprint over the gateway's headers", async () => {
    let capturedUrl = ""
    let capturedInit: RequestInit = {}
    const fetchImpl = async (url: string, init?: RequestInit) => {
      capturedUrl = url
      capturedInit = init ?? {}
      return new Response(JSON.stringify({ ok: true }), { status: 200 })
    }
    const body = { model: "claude-opus-4-8", system: [{ type: "text", text: "You are Claude Code...", cache_control: { type: "ephemeral" } }], tools: [{ name: "Bash" }], messages: [{ role: "user", content: "hi" }] }
    const res = await forwardNative({
      body,
      fingerprint,
      clientHeaders: { "user-agent": "Go-http-client/1.1", "x-api-key": "placeholder", "anthropic-version": "2023-06-01" },
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl, store: fakeStore("tok-abc") },
    })
    expect(res.status).toBe(200)
    expect(capturedUrl).toBe("https://api.anthropic.com/v1/messages?beta=true")
    const headers = capturedInit.headers as Record<string, string>
    expect(headers["authorization"]).toBe("Bearer tok-abc")
    expect(headers["user-agent"]).toBe("claude-cli/2.1.185 (external, cli)") // not Go-http-client
    expect(headers["x-api-key"]).toBeUndefined()
    expect(JSON.parse(capturedInit.body as string)).toEqual(body) // body byte-for-byte
  })

  it("returns 400 when no OAuth token is available", async () => {
    const res = await forwardNative({
      body: {}, fingerprint, clientHeaders: {},
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl: async () => new Response("{}"), store: fakeStore(null) },
    })
    expect(res.status).toBe(400)
  })

  it("uses the oauth-token profile env token directly", async () => {
    let auth = ""
    const fetchImpl = async (_url: string, init?: RequestInit) => { auth = (init?.headers as Record<string, string>)["authorization"] ?? ""; return new Response("{}", { status: 200 }) }
    await forwardNative({ body: {}, fingerprint, clientHeaders: {}, profile: { type: "oauth-token", env: { CLAUDE_CODE_OAUTH_TOKEN: "env-tok" } }, deps: { fetchImpl } })
    expect(auth).toBe("Bearer env-tok")
  })

  it("returns a non-2xx upstream response as-is (no fallback)", async () => {
    const res = await forwardNative({
      body: {}, fingerprint, clientHeaders: {},
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl: async () => new Response(JSON.stringify({ error: "x" }), { status: 403 }), store: fakeStore("t") },
    })
    expect(res.status).toBe(403)
  })
})
