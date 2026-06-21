/**
 * Native passthrough: forward a Claude Code request VERBATIM to api.anthropic.com
 * using the account's Max OAuth token.
 *
 * Design: the incoming request from a genuine Claude Code client already carries
 * the authentic CLI fingerprint (user-agent `claude-cli/…`, the real
 * `anthropic-beta` for this request, `x-stainless-*`, and a `system` with
 * `cache_control`). The only thing it lacks is OAuth auth — it reached the proxy
 * in API-key mode with a placeholder key. So we mirror its own headers, swap the
 * placeholder auth for a real `Authorization: Bearer`, ensure the OAuth beta flag
 * is present, and forward the body unchanged. No fabricated fingerprint, no
 * system rewriting — we relay a real CC request through the OAuth channel.
 *
 * The caller gates this on a Claude-Code shape check (see ccShape.ts), so only
 * genuinely CC-shaped requests are ever forwarded here.
 *
 * Leaf module — no imports from server.ts or session/.
 */

import { createPlatformCredentialStore, refreshOAuthToken, type CredentialStore } from "./tokenRefresh"

const ANTHROPIC_MESSAGES_URL = "https://api.anthropic.com/v1/messages"
const OAUTH_BETA = "oauth-2025-04-20"
type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

/**
 * Headers that must NOT be mirrored to the upstream: the client's placeholder
 * auth, hop-by-hop / length headers (re-derived by fetch), and Meridian's own
 * internal routing headers.
 */
const STRIP_HEADERS = new Set([
  "x-api-key", "authorization", "host", "content-length",
  "accept-encoding", "content-encoding", "connection",
])

async function readToken(store: CredentialStore): Promise<string | null> {
  const creds = await store.read()
  return creds?.claudeAiOauth?.accessToken ?? null
}

/**
 * Build the upstream header set by mirroring the client's own (genuine CC)
 * headers, swapping in the real OAuth Bearer token, and ensuring the OAuth beta
 * flag is present (the client, in API-key mode, won't have sent it).
 */
export function buildRelayHeaders(input: {
  clientHeaders: Record<string, string>
  token: string
}): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(input.clientHeaders)) {
    const lk = k.toLowerCase()
    if (STRIP_HEADERS.has(lk) || lk.startsWith("x-meridian-")) continue
    out[lk] = v
  }
  out["authorization"] = `Bearer ${input.token}`
  const beta = out["anthropic-beta"]
  if (!beta) {
    out["anthropic-beta"] = OAUTH_BETA
  } else if (!beta.split(",").map(s => s.trim()).includes(OAUTH_BETA)) {
    out["anthropic-beta"] = `${OAUTH_BETA},${beta}`
  }
  return out
}

export async function forwardNative(input: {
  body: unknown
  clientHeaders: Record<string, string>
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
    return new Response(
      JSON.stringify({ type: "error", error: { type: "authentication_error", message: "No OAuth token available for native relay" } }),
      { status: 400, headers: { "content-type": "application/json" } },
    )
  }

  const payload = JSON.stringify(input.body)
  const send = (tok: string) => fetchImpl(ANTHROPIC_MESSAGES_URL, {
    method: "POST",
    headers: buildRelayHeaders({ clientHeaders: input.clientHeaders, token: tok }),
    body: payload,
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
