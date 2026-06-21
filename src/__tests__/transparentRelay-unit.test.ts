import { describe, it, expect } from "bun:test"
import { buildRelayHeaders } from "../proxy/transparentRelay"

describe("buildRelayHeaders", () => {
  it("mirrors the client's genuine CC headers and swaps in the Bearer token", () => {
    const h = buildRelayHeaders({
      token: "tok123",
      clientHeaders: {
        "user-agent": "claude-cli/2.1.0",
        "anthropic-version": "2023-06-01",
        "anthropic-beta": "claude-code-20250219",
        "x-stainless-retry-count": "0",
        "content-type": "application/json",
      },
    })
    expect(h["user-agent"]).toBe("claude-cli/2.1.0")
    expect(h["anthropic-version"]).toBe("2023-06-01")
    expect(h["x-stainless-retry-count"]).toBe("0")
    expect(h["content-type"]).toBe("application/json")
    expect(h["authorization"]).toBe("Bearer tok123")
  })

  it("ensures the oauth beta flag is present without duplicating it", () => {
    expect(buildRelayHeaders({ token: "t", clientHeaders: {} })["anthropic-beta"]).toBe("oauth-2025-04-20")
    const merged = buildRelayHeaders({ token: "t", clientHeaders: { "anthropic-beta": "claude-code-20250219" } })["anthropic-beta"]
    expect(merged).toContain("oauth-2025-04-20")
    expect(merged).toContain("claude-code-20250219")
    const already = buildRelayHeaders({ token: "t", clientHeaders: { "anthropic-beta": "oauth-2025-04-20,foo" } })["anthropic-beta"]
    expect(already).toBe("oauth-2025-04-20,foo")
  })

  it("strips the client's placeholder auth, hop-by-hop, and meridian-internal headers", () => {
    const h = buildRelayHeaders({
      token: "tok123",
      clientHeaders: {
        "x-api-key": "placeholder",
        authorization: "Bearer client-placeholder",
        host: "127.0.0.1:3456",
        "content-length": "42",
        "accept-encoding": "gzip",
        "x-meridian-profile": "p",
        "x-meridian-mode": "native",
        "user-agent": "claude-cli/2.1.0",
      },
    })
    expect(h["x-api-key"]).toBeUndefined()
    expect(h["host"]).toBeUndefined()
    expect(h["content-length"]).toBeUndefined()
    expect(h["accept-encoding"]).toBeUndefined()
    expect(h["x-meridian-profile"]).toBeUndefined()
    expect(h["x-meridian-mode"]).toBeUndefined()
    expect(h["authorization"]).toBe("Bearer tok123")
    expect(h["user-agent"]).toBe("claude-cli/2.1.0")
  })
})
