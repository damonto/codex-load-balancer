#!/bin/sh
set -eu

if [ "$#" -gt 0 ]; then
	exec /app/codex-load-balancer "$@"
fi

exec /app/codex-load-balancer \
	--api-key "${CLB_API_KEY:-}" \
	--data-dir "${CLB_DATA_DIR:-/app/data}" \
	--port "${CLB_LISTEN_PORT:-8080}" \
	--sync-interval "${CLB_SYNC_INTERVAL:-5m}" \
	--sync-concurrency "${CLB_SYNC_CONCURRENCY:-8}"
