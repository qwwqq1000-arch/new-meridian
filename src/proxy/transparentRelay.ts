/**
 * Native passthrough: forward a Claude Code request to api.anthropic.com using
 * the account's Max OAuth token, carrying a GENUINE Claude Code fingerprint.
 *
 * The incoming request may have reached the proxy through an upstream gateway
 * (e.g. new-api) that rewrote `user-agent` to its own (Go-http-client) and
 * dropped the `x-stainless-*` headers — so the genuine CLI fingerprint can't be
 * read off the incoming request. Instead we mirror the incoming request's
 * surviving headers (anthropic-beta/version, content-type) and OVERRIDE the
 * fingerprint headers (`user-agent`, `x-app`, `x-stainless-*`) with one captured
 * from the real local CLI (see cliFingerprint.ts). Result: api.anthropic.com
 * sees an authentic CLI fingerprint regardless of what the gateway did.
 *
 * Leaf module — no imports from server.ts or session/.
 */

import { createPlatformCredentialStore, refreshOAuthToken, type CredentialStore } from "./tokenRefresh"
import type { Fingerprint } from "./cliFingerprint"

// Genuine Claude Code posts to /v1/messages with the ?beta=true query param.
const ANTHROPIC_MESSAGES_URL = "https://api.anthropic.com/v1/messages?beta=true"
const OAUTH_BETA = "oauth-2025-04-20"
type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

/**
 * Headers never mirrored upstream: the client's placeholder auth, hop-by-hop /
 * length headers (re-derived by fetch), Meridian's internal routing headers, and
 * proxy-chain headers (which would leak the gateway topology / real IP).
 */
const STRIP_HEADERS = new Set([
  "x-api-key", "authorization", "host", "content-length",
  "accept-encoding", "content-encoding", "connection",
  "forwarded", "x-real-ip", "via",
])

async function readToken(store: CredentialStore): Promise<string | null> {
  const creds = await store.read()
  return creds?.claudeAiOauth?.accessToken ?? null
}

/**
 * Build the upstream header set: mirror the incoming headers (minus the strip
 * list / x-forwarded-* / x-meridian-*), override the fingerprint headers with
 * the captured genuine CLI fingerprint, set a fresh per-request retry count,
 * inject the OAuth Bearer, and ensure the OAuth beta flag is present.
 */
export function buildRelayHeaders(input: {
  clientHeaders: Record<string, string>
  fingerprint: Fingerprint
  token: string
}): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(input.clientHeaders)) {
    const lk = k.toLowerCase()
    if (STRIP_HEADERS.has(lk) || lk.startsWith("x-meridian-") || lk.startsWith("x-forwarded-")) continue
    out[lk] = v
  }
  // Override with the genuine captured CLI fingerprint (UA / x-app / x-stainless-*).
  for (const [k, v] of Object.entries(input.fingerprint)) out[k.toLowerCase()] = v
  out["x-stainless-retry-count"] = "0" // per-request; first attempt
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
    return new Response(
      JSON.stringify({ type: "error", error: { type: "authentication_error", message: "No OAuth token available for native relay" } }),
      { status: 400, headers: { "content-type": "application/json" } },
    )
  }

  const payload = JSON.stringify(input.body)
  const send = (tok: string) => fetchImpl(ANTHROPIC_MESSAGES_URL, {
    method: "POST",
    headers: buildRelayHeaders({ clientHeaders: input.clientHeaders, fingerprint: input.fingerprint, token: tok }),
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
