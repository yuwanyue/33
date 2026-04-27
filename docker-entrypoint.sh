#!/bin/sh
set -eu

PORT="${PORT:-8080}"
XRAY_PORT="${XRAY_PORT:-10000}"
UUID="${VLESS_UUID:-10974d1a-cbd6-4b6f-db1d-38d78b3fb109}"
WS_PATH="${VLESS_WS_PATH:-/ws}"

mkdir -p /run/xray
cat > /run/xray/config.json <<EOF
{
  "log": {
    "loglevel": "warning"
  },
  "inbounds": [
    {
      "listen": "0.0.0.0",
      "port": ${XRAY_PORT},
      "protocol": "vless",
      "settings": {
        "clients": [
          {
            "id": "${UUID}",
            "flow": ""
          }
        ],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "ws",
        "security": "none",
        "wsSettings": {
          "path": "${WS_PATH}"
        }
      }
    }
  ],
  "outbounds": [
    {
      "protocol": "freedom",
      "tag": "direct"
    },
    {
      "protocol": "blackhole",
      "tag": "blocked"
    }
  ]
}
EOF

find_tm_cli() {
  for p in "/Cli" "/cli" "/tm" "/traffmonetizer" "/app/Cli" "/app/cli" "/usr/local/bin/Cli" "/usr/local/bin/cli" "/usr/local/bin/traffmonetizer" "/usr/bin/Cli" "/usr/bin/cli" "/usr/bin/traffmonetizer"; do
    if [ -x "$p" ]; then
      echo "$p"
      return 0
    fi
  done

  for cmd in Cli cli traffmonetizer; do
    if command -v "$cmd" >/dev/null 2>&1; then
      command -v "$cmd"
      return 0
    fi
  done

  return 1
}

start_traffmonetizer() {
  if [ -z "${TM_TOKEN:-}" ]; then
    echo "[entrypoint] TM_TOKEN is empty, skip Traffmonetizer"
    return 0
  fi

  if [ -n "${TM_COMMAND:-}" ]; then
    echo "[entrypoint] starting Traffmonetizer by TM_COMMAND"
    sh -lc "$TM_COMMAND" &
    return 0
  fi

  TM_BIN="$(find_tm_cli || true)"
  if [ -z "$TM_BIN" ]; then
    echo "[entrypoint] Traffmonetizer binary not found, skip"
    return 0
  fi

  TM_ARGS_TEXT="${TM_ARGS:-start accept}"
  echo "[entrypoint] starting Traffmonetizer: $TM_BIN $TM_ARGS_TEXT --token ****"
  # shellcheck disable=SC2086
  "$TM_BIN" $TM_ARGS_TEXT --token "$TM_TOKEN" &
}

start_traffmonetizer

echo "[entrypoint] starting Xray VLESS+WS on :$XRAY_PORT path=$WS_PATH"
/usr/local/bin/xray run -config /run/xray/config.json &

echo "[entrypoint] starting edge HTTP on :$PORT (healthz + ws proxy)"
exec /usr/local/bin/edge-proxy
