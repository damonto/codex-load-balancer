#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT_DIR"

export WS_SMOKE_ENABLE=1
export WS_SMOKE_URL="${WS_SMOKE_URL:-ws://127.0.0.1:8080/v1/responses}"
export WS_SMOKE_MODEL="${WS_SMOKE_MODEL:-gpt-5.4}"
export WS_SMOKE_PROMPT="${WS_SMOKE_PROMPT:-Reply with exactly: websocket-ok}"
export WS_SMOKE_TIMEOUT_SECONDS="${WS_SMOKE_TIMEOUT_SECONDS:-45}"
export WS_SMOKE_CHECK_STATS="${WS_SMOKE_CHECK_STATS:-1}"
export WS_SMOKE_SESSION_ID="${WS_SMOKE_SESSION_ID:-ws-smoke-$(date +%s)}"

if [[ -z "${WS_SMOKE_API_KEY:-}" ]]; then
  echo "WS_SMOKE_API_KEY is required" >&2
  exit 1
fi

go test -run '^TestWebsocketSmoke$' -v .
