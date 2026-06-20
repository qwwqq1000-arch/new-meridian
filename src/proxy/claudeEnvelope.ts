/**
 * Dynamic Claude Code fingerprint capture for native passthrough.
 *
 * Captures the real HTTP fingerprint the bundled `claude` binary emits (so
 * forwarded requests match the genuine CLI and follow version bumps), and
 * caches it by binary version. Pure cache/filter helpers live here; the
 * subprocess capture itself is added in a later task.
 *
 * Leaf module — no imports from server.ts or session/.
 */

export type Fingerprint = Record<string, string>

/** Conservative fallback used when live capture fails. */
export const BASELINE_FINGERPRINT: Fingerprint = {
  "user-agent": "claude-cli/2.1.0 (external, cli)",
  "anthropic-version": "2023-06-01",
  "anthropic-beta": "oauth-2025-04-20,claude-code-20250219",
  "x-app": "cli",
}

const KEEP_PREFIXES = ["x-stainless-"]
const KEEP_EXACT = new Set(["user-agent", "anthropic-version", "anthropic-beta", "x-app"])
const DROP_PER_REQUEST = new Set(["x-stainless-retry-count", "x-stainless-timeout"])

export function filterFingerprintHeaders(raw: Record<string, string>): Fingerprint {
  const out: Fingerprint = {}
  for (const [rawKey, value] of Object.entries(raw)) {
    const key = rawKey.toLowerCase()
    if (DROP_PER_REQUEST.has(key)) continue
    if (KEEP_EXACT.has(key) || KEEP_PREFIXES.some(p => key.startsWith(p))) {
      out[key] = value
    }
  }
  return out
}

const cache = new Map<string, Fingerprint>()

export function getCachedFingerprint(versionKey: string): Fingerprint | null {
  return cache.get(versionKey) ?? null
}

export function setCachedFingerprint(versionKey: string, fp: Fingerprint): void {
  cache.set(versionKey, fp)
}

export function resetEnvelopeCache(): void {
  cache.clear()
}
