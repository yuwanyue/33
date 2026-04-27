#!/bin/sh
set -eu

PORT="${PORT:-8080}"
UUID="${VLESS_UUID:-10974d1a-cbd6-4b6f-db1d-38d78b3fb109}"
WS_PATH="${VLESS_WS_PATH:-/ws}"

mkdir -p /run/xray
ESC_WS_PATH=$(printf '%s' "$WS_PATH" | sed 's/[\/&]/\\&/g')
sed \
  -e "s/__PORT__/${PORT}/g" \
  -e "s/__UUID__/${UUID}/g" \
  -e "s/__WS_PATH__/${ESC_WS_PATH}/g" \
  /etc/xray/config.json > /run/xray/config.json

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

echo "[entrypoint] starting Xray VLESS+WS on :$PORT path=$WS_PATH"
exec /usr/local/bin/xray run -config /run/xray/config.json
