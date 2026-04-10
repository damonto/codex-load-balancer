# Codex Load Balancer

Codex load balancer is a pragmatic reverse proxy and load balancer for Codex. It aggregates multiple ChatGPT auth tokens, keeps usage in memory, and selects the best token per request to avoid rate limits.

## Features

- Token directory scan on startup and hot reload (polling).
- Usage sync at startup and every minute.
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
cp config.toml.example config.toml
# edit config.toml
./codex-load-balancer --config ./config.toml
```

Startup flag:

- `--config` (optional): TOML config path (default `./config.toml`).

`config.toml` keys:

- `api_key` (required): API key for protected interfaces.
- `data_dir` (required): Directory containing active `*.json` auth files.
- `server.port` (required): Listen port.
- `top_up.enabled` (required): Whether to auto register replacement accounts for startup top-up and post-removal top-up.
- `top_up.min_tracked_accounts` (required): Background top-up target based on active account count.
- `top_up.register_workers` (required): Concurrent registration workers for startup/runtime top-up.
- `top_up.register_timeout_seconds` (required): Per-registration timeout.
- `sync.usage_sync_interval_seconds` (required): Usage sync interval.
- `sync.usage_sync_concurrency` (required): Usage sync concurrency.
- `account.registration_proxy_pool` (required): Registration proxy pool for account top-up.
- `account.purchase.enabled` (required): Whether account registration should run the RevenueCat purchase step.
- `account.purchase.revenuecat_bearer_key` (required when `account.purchase.enabled = true`): Bearer key used for `POST https://api.revenuecat.com/v1/receipts`.

Current example:

```toml
api_key = "your-api-key"
data_dir = "/app/data"

[server]
port = 8080

[top_up]
enabled = true
min_tracked_accounts = 0
register_workers = 5
register_timeout_seconds = 360

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = [
  "http://user-session-%s:pass@proxy.example.com:7777",
]

[account.purchase]
enabled = false
# revenuecat_bearer_key = "goog_your_revenuecat_key"
```

Notes:

- Unknown config keys cause startup failure.
- `top_up.enabled = false` disables both startup top-up and replacement account registration after `401` / downgraded accounts are removed.
- `account.registration_proxy_pool` must contain at least one non-empty proxy URL.
- `account.purchase.enabled = false` skips purchase, still completes Codex OAuth, writes the Codex credential JSON, and usage sync no longer removes accounts just because `plan_type=free`.
- When `account.purchase.enabled = true`, the service reuses `data/clb.db`, creates the `purchase_tokens` table there, leases one queued `fetch_token` before signup, posts the RevenueCat purchase after ChatGPT account creation, and only then continues to Codex OAuth.
- RevenueCat purchase retries only on HTTP `5xx`. HTTP `4xx`, transport errors, and timeouts mark that token as dead because one-time-use state is ambiguous after send.
- When `data/tokens.txt` exists, the service imports it into the `purchase_tokens` table inside `data/clb.db` at startup and every 10 seconds. Use one `fetch_token` per line; blank lines and `#` comments are ignored.
- If a proxy entry contains `%s`, each registration attempt replaces it with a fresh random `session_id`.

Manual `fetch_token` queue management:

```bash
sqlite3 data/clb.db "INSERT INTO purchase_tokens (fetch_token, status) VALUES ('FETCH_TOKEN_HERE', 'available');"
sqlite3 data/clb.db "SELECT id, status, attempt_count, account_id, response_status_code, last_error, created_at_unix FROM purchase_tokens ORDER BY id;"
```

File-based `fetch_token` queue input:

```bash
cat >> data/tokens.txt <<'EOF'
FETCH_TOKEN_1
FETCH_TOKEN_2
EOF
```

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

- Allowed paths: `/responses` and `/v1/responses` only.
- `/v1/responses` is normalized to `/responses` upstream.
- All request headers are preserved; only `Authorization` is replaced.
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
- On `401`, Codex load balancer refreshes once and retries. If the token still stays unauthorized during usage sync, it removes the credential file, evicts the token from memory, and tops up the same count with new registrations.
- If usage reports `plan_type=free`, Codex load balancer removes the account only when `account.purchase.enabled = true`.

## Dashboard

Endpoints:

```
GET /stats
GET /stats/overview?q=<search>
GET /stats/account?account_key=<account_id>
```

Auth:

- No auth on `/stats*` (intended for trusted internal network only).

Dashboard data:

- Overview cards: `today`, `recent_7_days`, `recent_30_days`, `total` with `input_tokens`, `cached_tokens`, `output_tokens`, `reasoning_tokens`.
- Account table: `email`, `plan_type`, totals, and 5-hour / weekly quota usage from usage sync (`/backend-api/wham/usage`).
- Detail view: daily / weekly / monthly token trends.

## Account Key Migration

If old `usage_events.account_key` rows were written as `user_xx`, migrate them to `account_id` with:

```bash
./migrate_usage_account_keys.sh --db data/clb.db --data-dir data
./migrate_usage_account_keys.sh --apply --db data/clb.db --data-dir data
```

The script reads only `.tokens.account_id` from credential JSON files. It does not parse JWTs.

## Logs

Codex load balancer logs structured events via `log/slog` to stdout.
