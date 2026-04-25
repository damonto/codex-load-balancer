# Codex Load Balancer

Codex load balancer is a pragmatic reverse proxy and load balancer for Codex. It aggregates multiple ChatGPT auth tokens, keeps usage in memory, and selects the best token per request to avoid rate limits.

## Features

- Token directory scan on startup and hot reload (polling).
- Usage sync at startup and every 5 minutes by default.
- Load balancing with weekly limit priority and 5-hour health degradation.
- Session stickiness via common headers.
- Automatic failover on rate limit responses.
- WebSocket upgrade proxy support.
- Per-request token usage persistence (`input` / `cached` / `output`) to SQLite.
- Built-in web dashboard for global/account usage and quota status.
- Stats dashboard for internal usage.

## Requirements

- Go 1.25+

## Build

```bash
go build -o codex-load-balancer .
```

## Run

```bash
./codex-load-balancer \
  --api-key your-api-key \
  --data-dir ./data \
  --port 8080 \
  --sync-interval 5m \
  --sync-concurrency 8
```

Flags:

- `--api-key` (required): API key for protected proxy endpoints.
- `--data-dir` (required): Directory containing active `*.json` auth files.
- `--port` (optional): Listen port. Default `8080`.
- `--sync-interval` (optional): Usage sync interval. Default `5m`.
- `--sync-concurrency` (optional): Usage sync concurrency. Default `8`.

## Docker Compose

Put credential `*.json` files in `./data`, then start the service:

```bash
CLB_API_KEY=your-api-key docker compose up -d --build
```

By default, Compose publishes `8080:8080`. Override the host port when needed:

```bash
CLB_API_KEY=your-api-key CLB_PORT=9090 docker compose up -d --build
```

Compose passes runtime settings through environment variables:

- `CLB_API_KEY` (required): API key for protected proxy endpoints.
- `CLB_PORT` (optional): Host port to publish. Default `8080`.
- `CLB_LISTEN_PORT` (optional): Container listen port. Default `8080`.
- `CLB_DATA_DIR` (optional): Container data directory. Default `/app/data`.
- `CLB_SYNC_INTERVAL` (optional): Usage sync interval. Default `5m`.
- `CLB_SYNC_CONCURRENCY` (optional): Usage sync concurrency. Default `8`.

Notes:

- Usage sync and dashboard state are stored in `data-dir/clb.db`.
- The service no longer reads a TOML config file.

## Token File Format

Codex load balancer stores Codex credential JSON. The proxy reads `.tokens.access_token`, `.tokens.account_id`, `.tokens.refresh_token`, and `.last_refresh` from each `*.json` file.

Example:

```json
{
  "auth_mode": "chatgpt",
  "last_refresh": "2026-03-30T16:00:00Z",
  "created_at": "2026-03-30T16:00:00Z",
  "tokens": {
    "id_token": "...",
    "access_token": "...",
    "refresh_token": "...",
    "account_id": "account_123"
  }
}
```

## Proxy Behavior

- Allowed paths: `/responses`, `/v1/responses`, `/models`, and `/v1/models` only.
- `/v1/responses` and `/v1/models` are normalized by stripping `/v1` upstream.
- Most request headers are preserved; `Authorization` is replaced and `Accept-Encoding` is removed so the proxy can inspect upstream response bodies.
- For WebSocket upstream requests, `Sec-WebSocket-Extensions` is stripped so usage frames stay observable as plain JSON (no per-message compression).
- Upstream base URL: `https://chatgpt.com/backend-api/codex`.
- WebSocket (`Upgrade: websocket`) requests are proxied through the selected token.

## Session Stickiness

If a request includes one of the following headers, Codex load balancer binds that session to a token:

- `session_id`

If the bound token hits a limit error, Codex load balancer unbinds and reselects.

## Load Balancing Rules

1. Filter out invalid, cooled down, or exhausted tokens.
2. Prefer higher `weekly_limit`.
3. If the top token has <30% 5-hour remaining and another token has higher 5-hour remaining, pick the healthier token.
4. If weekly limits tie, pick higher 5-hour remaining.

## Rate Limit Handling

If the upstream responds with status `429` or contains `"You've hit your usage limit"`, the current token is cooled down and the request is retried with another token.

## Usage Sync

- Syncs at startup and every 5 minutes.
- Uses `https://chatgpt.com/backend-api/wham/usage`.
- Account metadata shown in the dashboard, including `email` and `plan_type`, comes from the usage response in real time.
- Before proxying or syncing usage, Codex load balancer refreshes stale access tokens from the stored refresh token.
- On `401`, Codex load balancer refreshes once and retries. If the token still stays unauthorized during usage sync, it removes the credential file and evicts the token from memory.

## Dashboard

Endpoints:

```
GET /stats
GET /stats/overview
GET /stats/accounts/details
GET /stats/account?account_key=<account_id>
```

Auth:

- No auth on `/stats*` (intended for trusted internal network only).

Dashboard data:

- Overview cards: `today`, `recent_7_days`, `recent_30_days`, `total` with `input_tokens`, `cached_tokens`, `output_tokens`, `reasoning_tokens`.
- Current dashboard page load uses only `/stats/overview`.
- Account table: `email`, `plan_type`, totals, and 5-hour / weekly quota usage from usage sync (`/backend-api/wham/usage`).
- Future detail view can use `/stats/accounts/details` for batched `daily` / `weekly` / `monthly` token trends.
- `/stats/account` remains available for single-account drill-down.

## Logs

Codex load balancer logs structured events via `log/slog` to stdout.
