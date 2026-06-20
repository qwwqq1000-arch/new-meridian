import { describe, it, expect, beforeEach } from "bun:test"
import { getFingerprint, resetEnvelopeCache, BASELINE_FINGERPRINT } from "../proxy/claudeEnvelope"

describe("getFingerprint", () => {
  beforeEach(() => resetEnvelopeCache())

  it("captures, filters, and caches on first call", async () => {
    let calls = 0
    const spawnCapture = async () => {
      calls++
      return { "user-agent": "claude-cli/9.9.9", "anthropic-beta": "real-beta", authorization: "Bearer x" }
    }
    const fp1 = await getFingerprint({ spawnCapture, versionKey: "v9.9.9" })
    expect(fp1["user-agent"]).toBe("claude-cli/9.9.9")
    expect(fp1["authorization"]).toBeUndefined() // filtered out
    const fp2 = await getFingerprint({ spawnCapture, versionKey: "v9.9.9" })
    expect(fp2).toEqual(fp1)
    expect(calls).toBe(1) // cached, not re-captured
  })

  it("falls back to baseline when capture returns null and does not cache it", async () => {
    let calls = 0
    const spawnCapture = async () => { calls++; return null }
    const fp = await getFingerprint({ spawnCapture, versionKey: "vX" })
    expect(fp).toEqual(BASELINE_FINGERPRINT)
    await getFingerprint({ spawnCapture, versionKey: "vX" })
    expect(calls).toBe(2) // not cached → re-attempted
  })

  it("falls back to baseline when capture throws", async () => {
    const spawnCapture = async () => { throw new Error("spawn failed") }
    const fp = await getFingerprint({ spawnCapture, versionKey: "vErr" })
    expect(fp).toEqual(BASELINE_FINGERPRINT)
  })

  it("dedupes concurrent captures for the same version key", async () => {
    let calls = 0
    const spawnCapture = async () => { calls++; await new Promise(r => setTimeout(r, 10)); return { "user-agent": "claude-cli/1" } }
    const [a, b] = await Promise.all([
      getFingerprint({ spawnCapture, versionKey: "vDup" }),
      getFingerprint({ spawnCapture, versionKey: "vDup" }),
    ])
    expect(a).toEqual(b)
    expect(calls).toBe(1)
  })
})
