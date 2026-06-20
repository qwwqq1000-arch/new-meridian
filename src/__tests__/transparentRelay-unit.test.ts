import { describe, it, expect } from "bun:test"
import { ensureClaudeCodeIdentity, buildRelayHeaders, CLAUDE_CODE_IDENTITY } from "../proxy/transparentRelay"

describe("ensureClaudeCodeIdentity", () => {
  it("prepends identity when system is a plain string", () => {
    const out = ensureClaudeCodeIdentity("You are OpenCode, do X.")
    expect(out[0]).toEqual({ type: "text", text: CLAUDE_CODE_IDENTITY })
    expect(out[1]).toEqual({ type: "text", text: "You are OpenCode, do X." })
  })

  it("leaves a system whose first block is already the identity untouched", () => {
    const input = [{ type: "text", text: CLAUDE_CODE_IDENTITY }, { type: "text", text: "rest" }]
    const out = ensureClaudeCodeIdentity(input)
    expect(out).toEqual(input)
  })

  it("returns just the identity block when system is undefined", () => {
    expect(ensureClaudeCodeIdentity(undefined)).toEqual([{ type: "text", text: CLAUDE_CODE_IDENTITY }])
  })

  it("does not corrupt body text that merely mentions opencode (#17828)", () => {
    const out = ensureClaudeCodeIdentity("edit /src/opencode/config.ts")
    expect(out[1]).toEqual({ type: "text", text: "edit /src/opencode/config.ts" })
  })

  it("does not mutate the input array", () => {
    const input = [{ type: "text" as const, text: "hi" }]
    ensureClaudeCodeIdentity(input)
    expect(input).toEqual([{ type: "text", text: "hi" }])
  })
})

describe("buildRelayHeaders", () => {
  const fingerprint = { "user-agent": "claude-cli/2.1.0", "anthropic-version": "2023-06-01", "anthropic-beta": "claude-code-20250219" }

  it("injects the Bearer token and oauth beta flag", () => {
    const h = buildRelayHeaders({ fingerprint, token: "tok123", clientHeaders: {} })
    expect(h["authorization"]).toBe("Bearer tok123")
    expect(h["anthropic-beta"]).toContain("oauth-2025-04-20")
    expect(h["anthropic-beta"]).toContain("claude-code-20250219")
    expect(h["user-agent"]).toBe("claude-cli/2.1.0")
  })

  it("strips the client's placeholder auth and hop-by-hop headers", () => {
    const h = buildRelayHeaders({
      fingerprint,
      token: "tok123",
      clientHeaders: { "x-api-key": "placeholder", host: "127.0.0.1:3456", "content-length": "42", authorization: "Bearer client" },
    })
    expect(h["x-api-key"]).toBeUndefined()
    expect(h["host"]).toBeUndefined()
    expect(h["content-length"]).toBeUndefined()
    expect(h["authorization"]).toBe("Bearer tok123")
  })
})
