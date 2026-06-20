/**
 * Native passthrough: forward a request verbatim to api.anthropic.com using a
 * Max OAuth token, spoofing a genuine Claude Code fingerprint.
 *
 * Leaf module. Pure helpers here (identity + headers); the network forward is
 * added in a later task. No imports from server.ts or session/.
 */

export const CLAUDE_CODE_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."

const OAUTH_BETA = "oauth-2025-04-20"

type TextBlock = { type: "text"; text: string }

/** Normalize `system` to a block array whose first block is the identity line. */
export function ensureClaudeCodeIdentity(system: unknown): TextBlock[] {
  const blocks: TextBlock[] = []
  if (typeof system === "string") {
    if (system.length > 0) blocks.push({ type: "text", text: system })
  } else if (Array.isArray(system)) {
    for (const b of system) {
      if (b && typeof b === "object" && (b as { type?: unknown }).type === "text" && typeof (b as { text?: unknown }).text === "string") {
        blocks.push({ type: "text", text: (b as { text: string }).text })
      }
    }
  }
  if (blocks.length > 0 && blocks[0]!.text === CLAUDE_CODE_IDENTITY) return blocks
  return [{ type: "text", text: CLAUDE_CODE_IDENTITY }, ...blocks]
}

const STRIP_HEADERS = new Set(["x-api-key", "host", "content-length", "authorization", "accept-encoding"])

export function buildRelayHeaders(input: {
  fingerprint: Record<string, string>
  token: string
  clientHeaders: Record<string, string>
}): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(input.fingerprint)) out[k.toLowerCase()] = v
  out["authorization"] = `Bearer ${input.token}`
  const beta = out["anthropic-beta"]
  if (!beta) {
    out["anthropic-beta"] = OAUTH_BETA
  } else if (!beta.split(",").map(s => s.trim()).includes(OAUTH_BETA)) {
    out["anthropic-beta"] = `${OAUTH_BETA},${beta}`
  }
  for (const k of STRIP_HEADERS) {
    if (k !== "authorization") delete out[k]
  }
  return out
}
