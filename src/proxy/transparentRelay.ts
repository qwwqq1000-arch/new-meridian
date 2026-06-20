/**
 * Native passthrough: forward a request verbatim to api.anthropic.com using a
 * Max OAuth token, spoofing a genuine Claude Code fingerprint.
 *
 * Leaf module. Pure helpers here (identity + headers); the network forward is
 * added in a later task. No imports from server.ts or session/.
 */

import { createPlatformCredentialStore, refreshOAuthToken, type CredentialStore } from "./tokenRefresh"
import type { Fingerprint } from "./claudeEnvelope"

const ANTHROPIC_MESSAGES_URL = "https://api.anthropic.com/v1/messages"
type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

async function readToken(store: CredentialStore): Promise<string | null> {
  const creds = await store.read()
  return creds?.claudeAiOauth?.accessToken ?? null
}

export async function forwardNative(input: {
  body: { system?: unknown; [k: string]: unknown }
  clientHeaders: Record<string, string>
  fingerprint: Fingerprint
  profile: { type: string; env: Record<string, string> }
  deps?: { fetchImpl?: FetchLike; store?: CredentialStore }
}): Promise<Response> {
  const fetchImpl = input.deps?.fetchImpl ?? (globalThis.fetch as FetchLike)

  // Resolve token: oauth-token profile carries it in env; otherwise read the store.
  let token: string | null = null
  let store: CredentialStore | undefined = input.deps?.store
  if (input.profile.type === "oauth-token" && input.profile.env.CLAUDE_CODE_OAUTH_TOKEN) {
    token = input.profile.env.CLAUDE_CODE_OAUTH_TOKEN
  } else {
    store = store ?? createPlatformCredentialStore({ claudeConfigDir: input.profile.env.CLAUDE_CONFIG_DIR })
    token = await readToken(store)
  }
  if (!token) {
    return new Response(JSON.stringify({ type: "error", error: { type: "authentication_error", message: "No OAuth token available for native relay" } }), { status: 400, headers: { "content-type": "application/json" } })
  }

  const fingerprint = input.fingerprint
  const outBody = { ...input.body, system: ensureClaudeCodeIdentity(input.body.system) }
  const send = (tok: string) => fetchImpl(ANTHROPIC_MESSAGES_URL, {
    method: "POST",
    headers: buildRelayHeaders({ fingerprint, token: tok, clientHeaders: input.clientHeaders }),
    body: JSON.stringify(outBody),
  })

  let res = await send(token)
  if (res.status === 401 && store) {
    const refreshed = await refreshOAuthToken(store)
    if (refreshed) {
      const newToken = await readToken(store)
      if (newToken) res = await send(newToken)
    }
  }
  return res
}

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
