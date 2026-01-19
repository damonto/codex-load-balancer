# Synapse (Codex Load Balancer)

Synapse is a pragmatic reverse proxy and load balancer for Codex. It aggregates multiple ChatGPT auth tokens, keeps usage in memory, and selects the best token per request to avoid rate limits.

## Features

- Token directory scan on startup and hot reload (polling).
- Usage sync at startup and every minute.
- Load balancing with weekly limit priority and 5-hour health degradation.
- Session stickiness via common headers.
- Automatic failover on rate limit responses.
- Admin stats endpoint protected by an API key.

## Requirements

- Go 1.25+

## Build

```bash
go build -o synapse .
```

## Run

```bash
./synapse --api-key=dadada --token-dir=/data --port=8080
```

Flags:

- `--api-key` (required): API key for the admin stats endpoint.
- `--token-dir` (required): Directory containing `*.json` auth files.
- `--port` (optional): Listen port (default 8080).

## Token File Format

Synapse expects Codex-style `auth.json` files. Only `tokens.access_token` is required. If `refresh_token` and `last_refresh` exist, Synapse can refresh tokens every 8 days.

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
- Upstream base URL: `https://chatgpt.com/backend-api/codex`.

## Session Stickiness

If a request includes one of the following headers, Synapse binds that session to a token:

- `session_id`

If the bound token hits a limit error, Synapse unbinds and reselects.

## Load Balancing Rules

1. Filter out invalid, cooled down, or exhausted tokens.
2. Prefer higher `weekly_limit`.
3. If the top token has <30% 5-hour remaining and another token has higher 5-hour remaining, pick the healthier token.
4. If weekly limits tie, pick higher 5-hour remaining.

## Rate Limit Handling

If the upstream responds with status `429` or contains `"You've hit your usage limit"`, the current token is cooled down and the request is retried with another token.

## Usage Sync

- Syncs at startup and every minute.
- Uses `https://chatgpt.com/backend-api/wham/usage`.
- On `401` during sync, Synapse attempts a refresh; if that fails permanently, the token is marked invalid.

## Admin Stats

Endpoint:

```
GET /admin/stats
```

Authentication uses the `Authorization` header with the API key:

```
Authorization: Bearer <api-key>
```

Response fields per token:

- `id`
- `status` (active/invalid/cooldown)
- `five_hour_limit`
- `five_hour_remaining`
- `five_hour_reset_at`
- `five_hour_reset_after_seconds`
- `weekly_limit`
- `weekly_remaining`
- `weekly_reset_at`
- `weekly_reset_after_seconds`
- `last_sync`

## Logs

Synapse logs structured events via `log/slog` to stdout.
