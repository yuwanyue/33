#!/bin/sh
set -eu

if [ -n "${TM_COMMAND:-}" ]; then
  exec /bin/sh -lc "$TM_COMMAND"
fi

if [ -z "${TM_TOKEN:-}" ]; then
  echo "TM_TOKEN is required" >&2
  exit 1
fi

if [ -n "${TM_ARGS:-}" ]; then
  # TM_ARGS is user-provided shell words such as: start accept
  set -- ${TM_ARGS}
fi

exec /usr/local/bin/cli "$@" --token "$TM_TOKEN"
