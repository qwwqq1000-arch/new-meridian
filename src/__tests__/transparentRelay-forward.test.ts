import { describe, it, expect } from "bun:test"
import { forwardNative } from "../proxy/transparentRelay"
import type { CredentialStore } from "../proxy/tokenRefresh"

const fakeStore = (token: string | null): CredentialStore => ({
  read: async () => token ? ({ claudeAiOauth: { accessToken: token, refreshToken: "r", expiresAt: Date.now() + 1e9 } }) : null,
  write: async () => true,
})
const fixedFingerprint = { "user-agent": "claude-cli/2.1.0", "anthropic-version": "2023-06-01" }

describe("forwardNative", () => {
  it("forwards to api.anthropic.com with Bearer token and identity-prefixed system", async () => {
    let capturedUrl = ""
    let capturedInit: RequestInit = {}
    const fetchImpl = async (url: string, init?: RequestInit) => {
      capturedUrl = url
      capturedInit = init ?? {}
      return new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "content-type": "application/json" } })
    }
    const res = await forwardNative({
      body: { model: "claude", system: "You are OpenCode.", messages: [{ role: "user", content: "hi" }] },
      clientHeaders: { "x-api-key": "placeholder" },
      fingerprint: fixedFingerprint,
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl, store: fakeStore("tok-abc") },
    })
    expect(res.status).toBe(200)
    expect(capturedUrl).toBe("https://api.anthropic.com/v1/messages")
    const headers = capturedInit.headers as Record<string, string>
    expect(headers["authorization"]).toBe("Bearer tok-abc")
    expect(headers["x-api-key"]).toBeUndefined()
    const sentBody = JSON.parse(capturedInit.body as string)
    expect(sentBody.system[0].text).toBe("You are Claude Code, Anthropic's official CLI for Claude.")
    expect(sentBody.system[1].text).toBe("You are OpenCode.")
    expect(sentBody.messages).toEqual([{ role: "user", content: "hi" }])
  })

  it("returns 400 when no OAuth token is available", async () => {
    const res = await forwardNative({
      body: { messages: [] },
      clientHeaders: {},
      fingerprint: fixedFingerprint,
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl: async () => new Response("{}"), store: fakeStore(null) },
    })
    expect(res.status).toBe(400)
  })

  it("uses the oauth-token profile env token directly", async () => {
    let auth = ""
    const fetchImpl = async (_url: string, init?: RequestInit) => {
      auth = (init?.headers as Record<string, string>)["authorization"]
      return new Response("{}", { status: 200 })
    }
    await forwardNative({
      body: { messages: [], system: "x" },
      clientHeaders: {},
      fingerprint: fixedFingerprint,
      profile: { type: "oauth-token", env: { CLAUDE_CODE_OAUTH_TOKEN: "env-tok" } },
      deps: { fetchImpl },
    })
    expect(auth).toBe("Bearer env-tok")
  })

  it("returns a non-2xx upstream response as-is (no fallback)", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ error: "OAuth not supported" }), { status: 403 })
    const res = await forwardNative({
      body: { messages: [], system: "x" },
      clientHeaders: {},
      fingerprint: fixedFingerprint,
      profile: { type: "claude-max", env: {} },
      deps: { fetchImpl, store: fakeStore("t") },
    })
    expect(res.status).toBe(403)
  })
})
