/**
 * Claude Code request-shape detector.
 *
 * Decides whether an incoming `/v1/messages` body genuinely looks like a Claude
 * Code request. Used by the native-forward path as an anti-forgery gate: since
 * adapter detection is header-based (and therefore spoofable), a caller could
 * set `user-agent: claude-cli/…` to be classified as Claude Code and reach the
 * native path. This check ensures that what actually gets forwarded under the
 * operator's OAuth token genuinely resembles a Claude Code request — blocking a
 * non-CC body (which would both be off-purpose and, carrying a CC fingerprint,
 * risk flagging the account).
 *
 * Signals (both required):
 *   1. The first system text block begins with the Claude Code identity line.
 *   2. The tool set includes a quorum of Claude Code's PascalCase core tools.
 *      Non-CC clients use lowercase (`read`/`write`) or `mcp__…`-prefixed names,
 *      so they miss the quorum.
 *
 * Pure leaf module — no I/O, no imports from server.ts or session/.
 */

export const CC_IDENTITY = "You are Claude Code, Anthropic's official CLI for Claude."

/**
 * Strong, version-stable discriminators. Genuine Claude Code always ships these
 * PascalCase names; non-CC harnesses rename or MCP-prefix them. A quorum (not an
 * exact match) avoids false negatives when a CC version adds/removes a tool or
 * the user restricts the allowlist.
 */
const CC_QUORUM_TOOLS = new Set([
  "Bash", "Read", "Edit", "Write", "MultiEdit", "Glob", "Grep", "LS",
  "NotebookEdit", "WebFetch", "WebSearch", "TodoWrite", "Task", "ExitPlanMode",
])

const DEFAULT_MIN_TOOLS = 4

/** Extract the first system text block's text, or "" if none. */
function firstSystemText(system: unknown): string {
  if (typeof system === "string") return system
  if (Array.isArray(system)) {
    for (const b of system) {
      if (b && typeof b === "object" && (b as { type?: unknown }).type === "text") {
        const text = (b as { text?: unknown }).text
        if (typeof text === "string") return text
      }
    }
  }
  return ""
}

export function isClaudeCodeShaped(
  body: unknown,
  opts?: { minTools?: number },
): boolean {
  if (!body || typeof body !== "object") return false
  const b = body as { system?: unknown; tools?: unknown }

  // Signal 1: CC identity is the first system text block.
  if (!firstSystemText(b.system).trimStart().startsWith(CC_IDENTITY)) return false

  // Signal 2: quorum of CC PascalCase core tools.
  if (!Array.isArray(b.tools)) return false
  const minTools = opts?.minTools ?? DEFAULT_MIN_TOOLS
  let hits = 0
  for (const t of b.tools) {
    const name = t && typeof t === "object" ? (t as { name?: unknown }).name : undefined
    if (typeof name === "string" && CC_QUORUM_TOOLS.has(name)) hits++
  }
  return hits >= minTools
}
