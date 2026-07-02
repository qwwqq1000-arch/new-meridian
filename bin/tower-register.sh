#!/bin/sh
# Auto-register this meridian node with the Tower control panel.
#
# Env vars:
#   TOWER_URL        — Tower base URL (e.g. http://23.237.28.170:8088)
#   TOWER_PUSH_TOKEN — long-lived push token for /api/admin/nodes/push
#   MERIDIAN_API_KEY — this node's API key (also used for auth)
#   CLAUDE_PROXY_PORT— listen port (default 3456)
#
# Skips silently if TOWER_URL or TOWER_PUSH_TOKEN is unset.
# Retries up to 5 times on failure (tower or network may not be ready).

log() { echo "[tower-register] $*"; }

if [ -z "$TOWER_URL" ] || [ -z "$TOWER_PUSH_TOKEN" ]; then
  log "SKIP — TOWER_URL or TOWER_PUSH_TOKEN not set"
  exit 0
fi

PORT="${CLAUDE_PROXY_PORT:-3456}"

# Detect own public IP. Prefer TOWER_NODE_IP env if set (for NAT/custom scenarios).
if [ -n "$TOWER_NODE_IP" ]; then
  OWN_IP="$TOWER_NODE_IP"
else
  OWN_IP=$(wget -q -O - --timeout=5 https://ifconfig.me 2>/dev/null \
        || wget -q -O - --timeout=5 https://api.ipify.org 2>/dev/null)
fi
if [ -z "$OWN_IP" ]; then
  log "FAILED — cannot detect own IP"
  exit 1
fi

NODE_ADDR="${OWN_IP}:${PORT}"
API_KEY="${MERIDIAN_API_KEY:-}"

log "Registering ${NODE_ADDR} with Tower (${TOWER_URL})..."

MAX_RETRIES=5
RETRY=0
while [ $RETRY -lt $MAX_RETRIES ]; do
  RETRY=$((RETRY + 1))

  RESP=$(wget -q -O - --timeout=10 \
    --header="Content-Type: application/json" \
    --header="Authorization: Bearer ${TOWER_PUSH_TOKEN}" \
    --post-data="{\"kind\":\"meridian\",\"url\":\"${NODE_ADDR}\",\"apiKey\":\"${API_KEY}\"}" \
    "${TOWER_URL}/api/admin/nodes/push" 2>&1) && break

  log "Attempt ${RETRY}/${MAX_RETRIES} failed: ${RESP}"
  sleep 3
done

if echo "$RESP" | grep -q '"ok":true'; then
  REGISTERED=$(echo "$RESP" | grep -o '"registered":[0-9]*' | cut -d: -f2)
  log "SUCCESS — registered with Tower (accounts discovered: ${REGISTERED:-0})"
else
  log "FAILED after ${MAX_RETRIES} attempts — last response: ${RESP}"
fi
