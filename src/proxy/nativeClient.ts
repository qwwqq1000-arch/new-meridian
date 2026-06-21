type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export async function forwardToNative(input: {
  baseUrl: string
  /**
   * The VERBATIM client request body (the exact text Meridian received). It is
   * sent as the raw POST body — never re-serialized — so assistant `thinking`
   * block signatures survive intact. Re-parsing/re-stringifying would corrupt
   * them and Anthropic rejects the forward with a 400.
   */
  rawBody: string
  profile: { configDir: string; account: string }
  stream: boolean
  fetchImpl?: FetchLike
}): Promise<{ degraded: boolean; reason?: string; response?: Response }> {
  const fetchImpl = input.fetchImpl ?? (globalThis.fetch as FetchLike)
  try {
    const res = await fetchImpl(`${input.baseUrl}/relay`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-native-config-dir": input.profile.configDir,
        "x-native-account": input.profile.account,
        "x-native-stream": input.stream ? "1" : "0",
      },
      body: input.rawBody,
    })
    if (res.headers.get("X-Degrade") === "1") {
      return { degraded: true, reason: res.headers.get("X-Degrade-Reason") ?? "unknown" }
    }
    return { degraded: false, response: res }
  } catch (err) {
    return { degraded: true, reason: err instanceof Error ? err.message : "connection_error" }
  }
}
