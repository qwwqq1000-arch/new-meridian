import { describe, it, expect } from "bun:test"
import { resolveRelayMode, shouldNativeForward } from "../proxy/relayMode"

describe("resolveRelayMode", () => {
  it("returns the feature value when no override", () => {
    expect(resolveRelayMode({ feature: "passthrough", envForceNative: false })).toBe("passthrough")
  })
  it("env force overrides the feature", () => {
    expect(resolveRelayMode({ feature: "auto", envForceNative: true })).toBe("native")
  })
  it("header 'sdk' forces auto, beating env force", () => {
    expect(resolveRelayMode({ feature: "native", envForceNative: true, headerOverride: "sdk" })).toBe("auto")
  })
  it("header 'native' forces native", () => {
    expect(resolveRelayMode({ feature: "internal", envForceNative: false, headerOverride: "native" })).toBe("native")
  })
})

describe("shouldNativeForward", () => {
  it("true for native + claude-max", () => {
    expect(shouldNativeForward("native", "claude-max")).toBe(true)
  })
  it("true for native + oauth-token", () => {
    expect(shouldNativeForward("native", "oauth-token")).toBe(true)
  })
  it("false for native + api (no usable OAuth token)", () => {
    expect(shouldNativeForward("native", "api")).toBe(false)
  })
  it("false for non-native modes", () => {
    expect(shouldNativeForward("passthrough", "claude-max")).toBe(false)
    expect(shouldNativeForward("auto", "claude-max")).toBe(false)
  })
})
