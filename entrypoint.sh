#!/bin/sh
exec /app/codex-load-balancer --api-key "$API_KEY"  --token-dir=/app/tokens --port 8080
