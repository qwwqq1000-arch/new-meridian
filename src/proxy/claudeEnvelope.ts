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

import { createServer } from "node:http"
import { execFile } from "node:child_process"
import { claudeLog } from "../logger"
import { resolveClaudeExecutableAsync, getResolvedClaudeExecutableInfo } from "./models"

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
const inflight = new Map<string, Promise<Fingerprint>>()

export function getCachedFingerprint(versionKey: string): Fingerprint | null {
  return cache.get(versionKey) ?? null
}

export function setCachedFingerprint(versionKey: string, fp: Fingerprint): void {
  cache.set(versionKey, fp)
}

export function resetEnvelopeCache(): void {
  cache.clear()
  inflight.clear()
}

/** Run the real claude binary against a loopback recorder; resolve its request headers. */
async function defaultSpawnCapture(): Promise<Record<string, string> | null> {
  const claudePath = await resolveClaudeExecutableAsync()
  return await new Promise<Record<string, string> | null>((resolve) => {
    let settled = false
    const finish = (headers: Record<string, string> | null) => {
      if (settled) return
      settled = true
      try {
        server.closeAllConnections?.()
        server.close()
      } catch { /* already closing */ }
      resolve(headers)
    }
    const server = createServer((req, res) => {
      const headers: Record<string, string> = {}
      for (const [k, v] of Object.entries(req.headers)) headers[k] = Array.isArray(v) ? v.join(",") : (v ?? "")
      // Minimal valid SSE so the CLI exits cleanly without retrying.
      res.writeHead(200, { "content-type": "text/event-stream" })
      res.write(`event: message_start\ndata: ${JSON.stringify({ type: "message_start", message: { id: "msg_x", type: "message", role: "assistant", model: "claude", content: [], stop_reason: "end_turn", usage: { input_tokens: 0, output_tokens: 0 } } })}\n\n`)
      res.write(`event: message_stop\ndata: ${JSON.stringify({ type: "message_stop" })}\n\n`)
      res.end()
      finish(headers)
    })
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address()
      const port = typeof addr === "object" && addr ? addr.port : 0
      const child = execFile(claudePath, ["-p", "hi"], {
        env: { ...process.env, ANTHROPIC_BASE_URL: `http://127.0.0.1:${port}`, ANTHROPIC_API_KEY: "x" },
        timeout: 15_000,
      }, (err) => { if (err && !settled) { claudeLog("envelope.capture_spawn_error", { error: err.message }); finish(null) } })
      child.on("error", (err) => { claudeLog("envelope.capture_child_error", { error: err.message }); finish(null) })
    })
    server.on("error", () => finish(null))
    setTimeout(() => finish(null), 16_000)
  })
}

function currentVersionKey(): string {
  const info = getResolvedClaudeExecutableInfo()
  return info ? `${info.path}` : "unknown"
}

export async function getFingerprint(deps?: {
  spawnCapture?: () => Promise<Record<string, string> | null>
  versionKey?: string
}): Promise<Fingerprint> {
  const versionKey = deps?.versionKey ?? currentVersionKey()
  const cached = getCachedFingerprint(versionKey)
  if (cached) return cached
  const existing = inflight.get(versionKey)
  if (existing) return existing

  const spawnCapture = deps?.spawnCapture ?? defaultSpawnCapture
  const promise = (async (): Promise<Fingerprint> => {
    try {
      const raw = await spawnCapture()
      if (!raw) return BASELINE_FINGERPRINT
      const fp = filterFingerprintHeaders(raw)
      if (Object.keys(fp).length === 0) return BASELINE_FINGERPRINT
      setCachedFingerprint(versionKey, fp)
      return fp
    } catch (err) {
      claudeLog("envelope.capture_failed", { error: err instanceof Error ? err.message : String(err) })
      return BASELINE_FINGERPRINT
    } finally {
      inflight.delete(versionKey)
    }
  })()
  inflight.set(versionKey, promise)
  return promise
}
