#!/usr/bin/env python3
"""Convert a Claude.ai sessionKey cookie to OAuth tokens via PKCE flow.

Usage: python3 session-to-oauth.py <sessionKey>
Output: JSON with access_token, refresh_token, expires_in on success.
Exit 0 on success, 1 on error (error JSON on stdout).

Requires curl_cffi (TLS fingerprint impersonation to bypass Cloudflare).
Must run from a host/container with a clean exit IP (residential/datacenter proxy).
"""
import sys, json, hashlib, base64, secrets
from urllib.parse import urlparse, parse_qs

def main():
    if len(sys.argv) < 2:
        print(json.dumps({"ok": False, "error": "Usage: session-to-oauth.py <sessionKey>"}))
        sys.exit(1)

    session_key = sys.argv[1].strip()
    if not session_key.startswith("sk-ant-sid"):
        print(json.dumps({"ok": False, "error": "Invalid sessionKey format (expected sk-ant-sid*)"}))
        sys.exit(1)

    try:
        from curl_cffi import requests
    except ImportError:
        print(json.dumps({"ok": False, "error": "curl_cffi not installed"}))
        sys.exit(1)

    s = requests.Session(impersonate="chrome131")
    cookies = {"sessionKey": session_key}

    # Step 1: get org UUID
    try:
        r = s.get("https://claude.ai/api/organizations", cookies=cookies, timeout=15)
    except Exception as e:
        print(json.dumps({"ok": False, "error": f"Failed to reach claude.ai: {e}"}))
        sys.exit(1)

    if r.status_code != 200:
        print(json.dumps({"ok": False, "error": f"claude.ai returned {r.status_code} (CF block or invalid session)"}))
        sys.exit(1)

    try:
        orgs = r.json()
        org_uuid = orgs[0]["uuid"]
    except Exception:
        print(json.dumps({"ok": False, "error": "Failed to parse organizations response"}))
        sys.exit(1)

    # Step 2: PKCE parameters
    code_verifier = base64.urlsafe_b64encode(secrets.token_bytes(32)).rstrip(b"=").decode()
    code_challenge = base64.urlsafe_b64encode(hashlib.sha256(code_verifier.encode()).digest()).rstrip(b"=").decode()
    state = base64.urlsafe_b64encode(secrets.token_bytes(32)).rstrip(b"=").decode()

    # Step 3: authorize
    try:
        r2 = s.post(
            f"https://claude.ai/v1/oauth/{org_uuid}/authorize",
            cookies=cookies,
            json={
                "response_type": "code",
                "client_id": "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
                "organization_uuid": org_uuid,
                "redirect_uri": "https://console.anthropic.com/oauth/code/callback",
                "scope": "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
                "state": state,
                "code_challenge": code_challenge,
                "code_challenge_method": "S256",
            },
            timeout=15,
        )
    except Exception as e:
        print(json.dumps({"ok": False, "error": f"Authorize request failed: {e}"}))
        sys.exit(1)

    if r2.status_code != 200:
        print(json.dumps({"ok": False, "error": f"Authorize returned {r2.status_code}: {r2.text[:200]}"}))
        sys.exit(1)

    try:
        data = r2.json()
        redirect_uri = data["redirect_uri"]
        auth_code = parse_qs(urlparse(redirect_uri).query)["code"][0]
    except Exception:
        print(json.dumps({"ok": False, "error": f"Failed to extract auth code from: {r2.text[:200]}"}))
        sys.exit(1)

    # Step 4: exchange code for tokens (plain request, no CF bypass needed)
    token_url = "https://platform.claude.com/v1/oauth/token"
    try:
        r3 = requests.post(
            token_url,
            headers={
                "Content-Type": "application/x-www-form-urlencoded",
                "User-Agent": "claude-cli/2.1.198 (external, cli)",
            },
            data=f"grant_type=authorization_code&code={auth_code}&redirect_uri=https://console.anthropic.com/oauth/code/callback&client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&code_verifier={code_verifier}&state={state}",
            timeout=15,
        )
    except Exception as e:
        print(json.dumps({"ok": False, "error": f"Token exchange failed: {e}"}))
        sys.exit(1)

    if r3.status_code != 200:
        print(json.dumps({"ok": False, "error": f"Token exchange returned {r3.status_code}: {r3.text[:200]}"}))
        sys.exit(1)

    try:
        tokens = r3.json()
    except Exception:
        print(json.dumps({"ok": False, "error": "Token response was invalid JSON"}))
        sys.exit(1)

    if not tokens.get("access_token") or not tokens.get("refresh_token"):
        print(json.dumps({"ok": False, "error": "Token response missing access_token or refresh_token"}))
        sys.exit(1)

    print(json.dumps({
        "ok": True,
        "access_token": tokens["access_token"],
        "refresh_token": tokens["refresh_token"],
        "expires_in": tokens.get("expires_in", 28800),
        "scope": tokens.get("scope", ""),
        "email": orgs[0].get("name", "").replace("'s Organization", ""),
    }))

if __name__ == "__main__":
    main()
