#!/bin/sh
set -eu

if [ -z "${API_KEY:-}" ]; then
	echo "API_KEY is required" >&2
	exit 1
fi

DATA_DIR="${DATA_DIR:-/app/data}"
PORT="${PORT:-8080}"

set -- /app/codex-load-balancer \
	--api-key "$API_KEY" \
	--data-dir "$DATA_DIR" \
	--port "$PORT"

if [ -n "${MIN_VALID_ACCOUNTS:-}" ]; then
	set -- "$@" --min-valid-accounts "$MIN_VALID_ACCOUNTS"
fi

if [ -n "${REGISTER_WORKERS:-}" ]; then
	set -- "$@" --register-workers "$REGISTER_WORKERS"
fi

exec "$@"
