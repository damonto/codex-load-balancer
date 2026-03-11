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
- `data_dir` (required): Directory containing active `*.json` auth files; in-flight purchases are stored under `data_dir/pending`.
- `server.port` (optional): Listen port (default 8080).
- `top_up.min_tracked_accounts` (optional): Background top-up target. The balancer currently uses `active + parseable pending` to decide whether more registrations are needed (0 disables startup top-up).
- `top_up.register_workers` (optional): Concurrent registration workers for startup/runtime top-up (default 5).
- `top_up.register_timeout_seconds` (optional): Per-registration timeout (default 360).
- `sync.usage_sync_interval_seconds` (optional): Usage sync interval (default 300).
- `sync.usage_sync_concurrency` (optional): Usage sync concurrency (default 8).
- `telegram.bot_token` (optional): Telegram bot token used to push the checkout URL. If omitted together with `telegram.chat_id`, the checkout URL is only written into `data_dir/pending/*.json`.
- `telegram.chat_id` (optional): Telegram target chat ID or channel username; keep it as a string in TOML. `telegram.bot_token` and `telegram.chat_id` must be configured together.
- `account.registration_proxy_pool` (required): Registration proxy pool for account top-up.

Current example:

```toml
api_key = "your-api-key"
data_dir = "/app/data"

[server]
port = 8080

[top_up]
min_tracked_accounts = 0
register_workers = 5
register_timeout_seconds = 360

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[telegram]
bot_token = "123456:telegram-bot-token"
chat_id = "123456789"

[account]
registration_proxy_pool = [
  "http://user:pass@proxy.example.com:7777",
]
```

Notes:

- Unknown config keys cause startup failure.
- `account.registration_proxy_pool` must contain at least one non-empty proxy URL.
- Telegram 推送使用官方 `sendMessage` 接口，实际会发送 `chat_id` 和支付链接文本；如果未配置 Telegram，checkout URL 只会保存在 pending 凭证文件里。
- 新注册账号会先写入 `data_dir/pending/*.json`；后台轮询 `usage` 发现 `plan_type=plus` 后，再自动提升到 `data_dir/*.json`。

## Token File Format

Codex load balancer expects Codex-style `auth.json` files. Only `tokens.access_token` is required. If `refresh_token` and `last_refresh` exist, Codex load balancer can refresh tokens every 8 days.

Example:

```json
{
  "tokens": {
    "access_token": "...",
    "refresh_token": "...",
    "account_id": "..."
  },
  "last_refresh": "2026-01-01T00:00:00Z"
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
- On `401` during sync, Codex load balancer first attempts one forced refresh; if usage still returns `401`, it removes the token file, evicts the token from memory, and tops up the same count with new registrations.
- If usage reports `plan_type=free`, Codex load balancer treats the account as downgraded, removes it, and tops up a replacement account.

## Dashboard

Endpoints:

```
GET /stats
GET /stats/overview?q=<search>
GET /stats/account?account_key=<id>
```

Auth:

- No auth on `/stats*` (intended for trusted internal network only).

Dashboard data:

- Overview cards: `today`, `recent_7_days`, `recent_30_days`, `total` with `input_tokens`, `cached_tokens`, `output_tokens`, `reasoning_tokens`.
- Account table: `email`, `plan_type`, totals, and 5-hour / weekly quota usage from usage sync (`/backend-api/wham/usage`).
- Detail view: daily / weekly / monthly token trends.

## Logs

Codex load balancer logs structured events via `log/slog` to stdout.
