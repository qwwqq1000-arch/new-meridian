type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export async function forwardToNative(input: {
  baseUrl: string
  body: unknown
  profile: { configDir: string; account: string }
  stream: boolean
  fetchImpl?: FetchLike
}): Promise<{ degraded: boolean; reason?: string; response?: Response }> {
  const fetchImpl = input.fetchImpl ?? (globalThis.fetch as FetchLike)
  try {
    const res = await fetchImpl(`${input.baseUrl}/relay`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ configDir: input.profile.configDir, account: input.profile.account, stream: input.stream, body: input.body }),
    })
    if (res.headers.get("X-Degrade") === "1") {
      return { degraded: true, reason: res.headers.get("X-Degrade-Reason") ?? "unknown" }
    }
    return { degraded: false, response: res }
  } catch (err) {
    return { degraded: true, reason: err instanceof Error ? err.message : "connection_error" }
  }
}
