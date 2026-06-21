/**
 * Genuine Claude Code fingerprint capture, from the local CLI.
 *
 * The CLI emits its real outgoing request headers when run with
 * `ANTHROPIC_LOG=debug` — including `user-agent: claude-cli/<ver> …`, `x-app`,
 * and the full `x-stainless-*` set. We run one real (authenticated) `claude -p`
 * probe, parse those headers from the debug log, and cache them. The native
 * relay then injects this genuine fingerprint over whatever an upstream gateway
 * (e.g. new-api) stripped/rewrote, so the request that reaches api.anthropic.com
 * carries the authentic CLI fingerprint — which auto-follows CLI version bumps
 * because it is captured from the real binary, not fabricated.
 *
 * Lifecycle: lazy. The first native request with no fresh cache returns null
 * (caller degrades to passthrough) and kicks off a background capture; once
 * cached, subsequent requests go native. Cache TTL is configurable
 * (MERIDIAN_NATIVE_FINGERPRINT_TTL, e.g. "1h"/"30s"), default 5m.
 *
 * Leaf module — no imports from server.ts or session/.
 */

import { execFile } from "node:child_process"
import { claudeLog } from "../logger"
import { resolveClaudeExecutableAsync, getResolvedClaudeExecutableInfo } from "./models"

export type Fingerprint = Record<string, string>

/**
 * Static fingerprint headers to lift from the debug log. `x-stainless-retry-count`
 * is per-request (0 on the first attempt) — set fresh at relay time, never replayed.
 */
const FINGERPRINT_HEADERS = [
  "user-agent",
  "x-app",
  "x-stainless-lang",
  "x-stainless-package-version",
  "x-stainless-os",
  "x-stainless-arch",
  "x-stainless-runtime",
  "x-stainless-runtime-version",
  "x-stainless-timeout",
]

const DEFAULT_TTL_MS = 300_000 // 5m

/** Parse a duration like "1h" / "5m" / "30s" to ms; default 5m on unset/malformed. */
export function parseTtlMs(raw: string | undefined): number {
  if (!raw) return DEFAULT_TTL_MS
  const m = raw.trim().match(/^(\d+)\s*([hms])$/i)
  if (!m) return DEFAULT_TTL_MS
  const n = Number.parseInt(m[1]!, 10)
  const unit = m[2]!.toLowerCase()
  const mult = unit === "h" ? 3_600_000 : unit === "m" ? 60_000 : 1000
  return n * mult
}

/** Extract the genuine CLI fingerprint headers from `ANTHROPIC_LOG=debug` output. */
export function parseFingerprintFromDebugLog(log: string): Fingerprint | null {
  const fp: Fingerprint = {}
  for (const name of FINGERPRINT_HEADERS) {
    const m = log.match(new RegExp(`"${name}"\\s*:\\s*"([^"]*)"`, "i"))
    if (m) fp[name] = m[1]!
  }
  // Must carry a genuine claude-cli UA, else it's not a usable CC fingerprint.
  if (!fp["user-agent"] || !fp["user-agent"].startsWith("claude-cli/")) return null
  return fp
}

interface CacheEntry { fp: Fingerprint; capturedAt: number; ttl: number }
const cache = new Map<string, CacheEntry>()
const inflight = new Map<string, Promise<Fingerprint | null>>()

export function getCachedCliFingerprint(key: string, now: number): Fingerprint | null {
  const e = cache.get(key)
  if (!e) return null
  if (now - e.capturedAt > e.ttl) return null
  return e.fp
}

export function setCachedCliFingerprint(key: string, fp: Fingerprint, ttl: number, capturedAt: number): void {
  cache.set(key, { fp, ttl, capturedAt })
}

export function resetCliFingerprintCache(): void {
  cache.clear()
  inflight.clear()
}

/**
 * Test-only: force `getCliFingerprint` to return this value directly (bypassing
 * the cache/capture/lazy-miss). Pass a fingerprint to make native fire, `null`
 * to exercise the degrade-to-passthrough path, or `undefined` to clear.
 */
let _resultOverride: Fingerprint | null | undefined
export function __setCliFingerprintForTest(fp: Fingerprint | null | undefined): void {
  _resultOverride = fp
}

/** Test-only override of the capture step. */
let _captureOverride: ((env: Record<string, string | undefined>) => Promise<Fingerprint | null>) | null = null
export function __setCliFingerprintCaptureOverride(
  fn: ((env: Record<string, string | undefined>) => Promise<Fingerprint | null>) | null,
): void { _captureOverride = fn }

/** Run `ANTHROPIC_LOG=debug claude -p hi` with the profile env; parse the headers. */
async function defaultCapture(env: Record<string, string | undefined>): Promise<Fingerprint | null> {
  const claudePath = await resolveClaudeExecutableAsync()
  return await new Promise<Fingerprint | null>((resolve) => {
    execFile(
      claudePath,
      ["-p", "hi"],
      { env: { ...process.env, ...env, ANTHROPIC_LOG: "debug" }, timeout: 30_000, maxBuffer: 16 * 1024 * 1024 },
      (_err, stdout, stderr) => {
        const fp = parseFingerprintFromDebugLog(`${stdout ?? ""}\n${stderr ?? ""}`)
        if (!fp) claudeLog("cli_fingerprint.parse_failed", {})
        resolve(fp)
      },
    )
  })
}

function cacheKey(): string {
  const info = getResolvedClaudeExecutableInfo()
  return info ? info.path : "unknown"
}

/**
 * Return a fresh cached fingerprint, or null. On a cache miss, kick off a
 * (deduplicated) background capture and return null immediately — the caller
 * degrades this request to passthrough; the next request uses the cached value.
 */
export async function getCliFingerprint(opts: {
  ttlMs: number
  env: Record<string, string | undefined>
  now?: number
}): Promise<Fingerprint | null> {
  if (_resultOverride !== undefined) return _resultOverride
  const now = opts.now ?? Date.now()
  const key = cacheKey()
  const cached = getCachedCliFingerprint(key, now)
  if (cached) return cached

  if (!inflight.has(key)) {
    const capture = _captureOverride ?? defaultCapture
    const promise = (async (): Promise<Fingerprint | null> => {
      try {
        const fp = await capture(opts.env)
        if (fp) setCachedCliFingerprint(key, fp, opts.ttlMs, Date.now())
        return fp
      } catch (err) {
        claudeLog("cli_fingerprint.capture_failed", { error: err instanceof Error ? err.message : String(err) })
        return null
      } finally {
        inflight.delete(key)
      }
    })()
    inflight.set(key, promise)
  }
  return null
}
