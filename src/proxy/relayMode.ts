/**
 * Relay-mode resolution and eligibility. Pure leaf module — no I/O, no imports
 * from server.ts or session/.
 *
 * Modes:
 *   auto        — existing behavior (transform pipeline decides internal vs passthrough)
 *   internal    — MCP tools, proxy executes (override pipeline passthrough=false)
 *   passthrough — SDK passthrough, client executes (override pipeline passthrough=true)
 *   native      — direct forward to api.anthropic.com, bypassing the Agent SDK
 */

export type RelayMode = "auto" | "internal" | "passthrough" | "native"

export function resolveRelayMode(input: {
  feature: RelayMode
  envForceNative: boolean
  headerOverride?: string
}): RelayMode {
  if (input.headerOverride === "sdk") return "auto"
  if (input.headerOverride === "native") return "native"
  if (input.envForceNative) return "native"
  return input.feature
}

export function shouldNativeForward(
  mode: RelayMode,
  profileType: "claude-max" | "api" | "oauth-token",
): boolean {
  if (mode !== "native") return false
  return profileType === "claude-max" || profileType === "oauth-token"
}

export function applyRelayModeToPassthrough(mode: RelayMode, pipelinePassthrough: boolean): boolean {
  if (mode === "internal") return false
  if (mode === "passthrough") return true
  return pipelinePassthrough
}
