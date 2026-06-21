import { describe, it, expect } from "bun:test"
import { forwardToNative } from "../proxy/nativeClient"

describe("forwardToNative", () => {
  it("returns degraded when Go responds X-Degrade:1", async () => {
    const fetchImpl = async () => new Response("", { status: 200, headers: { "X-Degrade": "1", "X-Degrade-Reason": "no_fingerprint" } })
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", body: {}, profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(true)
    expect(r.reason).toBe("no_fingerprint")
  })
  it("returns the response when not degraded", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ ok: true }), { status: 200 })
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", body: {}, profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(false)
    expect(r.response?.status).toBe(200)
  })
  it("degrades on connection error", async () => {
    const fetchImpl = async () => { throw new Error("ECONNREFUSED") }
    const r = await forwardToNative({ baseUrl: "http://127.0.0.1:9", body: {}, profile: { configDir: "/c", account: "a" }, stream: false, fetchImpl })
    expect(r.degraded).toBe(true)
  })
})
