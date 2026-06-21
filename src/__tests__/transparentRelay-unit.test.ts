import { describe, it, expect } from "bun:test"
import { buildRelayHeaders } from "../proxy/transparentRelay"

const fingerprint = {
  "user-agent": "claude-cli/2.1.185 (external, cli, agent-sdk/0.3.183)",
  "x-app": "cli",
  "x-stainless-os": "Linux",
  "x-stainless-arch": "arm64",
  "x-stainless-runtime": "node",
}

describe("buildRelayHeaders", () => {
  it("overrides UA / x-app / x-stainless with the captured genuine fingerprint", () => {
    const h = buildRelayHeaders({
      token: "tok123",
      fingerprint,
      clientHeaders: {
        "user-agent": "Go-http-client/1.1", // new-api's rewritten UA — must be replaced
        "anthropic-version": "2023-06-01",
        "anthropic-beta": "claude-code-20250219",
        "content-type": "application/json",
      },
    })
    expect(h["user-agent"]).toBe("claude-cli/2.1.185 (external, cli, agent-sdk/0.3.183)")
    expect(h["x-app"]).toBe("cli")
    expect(h["x-stainless-os"]).toBe("Linux")
    expect(h["x-stainless-retry-count"]).toBe("0")
    // surviving client headers preserved
    expect(h["anthropic-version"]).toBe("2023-06-01")
    expect(h["content-type"]).toBe("application/json")
    expect(h["authorization"]).toBe("Bearer tok123")
  })

  it("ensures the oauth beta flag, keeping the client's other betas", () => {
    const merged = buildRelayHeaders({ token: "t", fingerprint, clientHeaders: { "anthropic-beta": "claude-code-20250219,effort-2025-11-24" } })["anthropic-beta"]
    expect(merged).toContain("oauth-2025-04-20")
    expect(merged).toContain("claude-code-20250219")
    expect(merged).toContain("effort-2025-11-24")
  })

  it("strips placeholder auth, hop-by-hop, meridian-internal, and proxy-chain headers", () => {
    const h = buildRelayHeaders({
      token: "tok123",
      fingerprint,
      clientHeaders: {
        "x-api-key": "placeholder",
        authorization: "Bearer client",
        host: "23.237.28.170:3010",
        "content-length": "42",
        "x-forwarded-for": "::ffff:216.227.134.146",
        "x-forwarded-host": "23.237.28.170:3010",
        "x-real-ip": "216.227.134.146",
        via: "1.1 newapi",
        "x-meridian-profile": "p",
      },
    })
    for (const k of ["x-api-key", "host", "content-length", "x-forwarded-for", "x-forwarded-host", "x-real-ip", "via", "x-meridian-profile"]) {
      expect(h[k]).toBeUndefined()
    }
    expect(h["authorization"]).toBe("Bearer tok123")
  })
})
