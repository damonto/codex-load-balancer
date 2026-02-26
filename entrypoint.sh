#!/bin/sh
exec /app/codex-load-balancer --api-key "$API_KEY" --data-dir=/app/data --port 8080
