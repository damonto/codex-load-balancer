# Project Instructions

This repository is `github.com/damonto/codex-load-balancer`: a small, pragmatic Go 1.25 reverse proxy/load balancer for Codex traffic. Keep changes boring, local, and easy to reason about.

## Repository Shape

- Keep the project as a flat `package main` application. Do not introduce `cmd/`, `pkg/`, `internal/`, `common`, `shared`, `utils`, or `base` packages unless the user explicitly asks for a larger restructure.
- Prefer the Go standard library. The current direct runtime dependency is `modernc.org/sqlite`; do not add routers, websocket frameworks, ORMs, or helper libraries for work the standard library already handles well.
- If a dependency is truly needed, verify its API from official docs or Context7, then run `go mod tidy`.
- Keep endpoint constants in `endpoints.go` unless the user explicitly asks for runtime configurability. This service intentionally has a small config surface.

## Proxy Contract

- Preserve the allowed proxy paths unless the requested change explicitly expands the product surface:
    - `/responses`
    - `/v1/responses` normalized upstream to `/responses`
    - `/models`
    - `/v1/models` normalized upstream to `/models`
- Incoming proxy requests are authenticated with `Authorization: Bearer <api-key>`. Upstream requests must replace `Authorization` with the selected Codex token.
- Preserve `ChatGPT-Account-ID` behavior: set it from token metadata when available, but do not overwrite a caller-provided header.
- Keep `session_id` as the only stickiness key. Clear sticky sessions when a token returns unauthorized, hits a limit, is removed, or is no longer available.
- Retry behavior is intentionally small: retry at most once with another token after `401`, `429`, or the known Codex usage-limit body. If no alternate token exists, return the original upstream response.
- Do not forward hop-by-hop headers. Keep `Accept-Encoding` removed for HTTP proxying so response bodies stay inspectable for retry and usage accounting.

## Token, Refresh, and Usage Rules

- Token files are `*.json` files in `--data-dir`. Preserve unknown JSON fields when updating credentials.
- Refresh stale tokens before proxying and before usage sync. On `401`, force one refresh and retry. Permanent refresh failures mark the token invalid; unauthorized usage sync removes the credential file and evicts the token.
- Keep usage sync pointed at `/backend-api/wham/usage`; map both 5-hour and weekly quota windows defensively because upstream payloads are not fully stable.
- Persist per-request usage to `data-dir/clb.db`. Keep SQLite schema changes additive and backward-compatible through migration helpers such as `ensureColumn`.
- Keep `db.SetMaxOpenConns(1)` for SQLite. It avoids writer contention between the usage sink and dashboard reads.
- Do not log access tokens, refresh tokens, API keys, full auth files, or full request/response bodies.

## WebSocket Rules

- The WebSocket proxy deliberately uses `net/http` hijacking and raw frame handling. Do not replace it with a framework unless requested.
- Keep `Sec-WebSocket-Extensions` stripped upstream. Usage capture and request tool injection assume observable, uncompressed JSON frames.
- WebSocket tunnels must shut down on request cancellation or server shutdown, and must stay tracked by `Server.websocketWG`.
- Preserve frame correctness: client-to-upstream frames must stay masked, upstream-to-client frames must not be rewritten except for observation, fragmentation must remain supported, and payload limits must respect `defaultMaxRequestBody`.
- Tool injection for WebSocket requests only applies to client-to-upstream text messages on `/responses`.

## Response Tool Injection

- Default response tool injection currently adds `{"type":"image_generation","output_format":"png"}`.
- Injection must be idempotent: never duplicate an existing tool type.
- Malformed or non-JSON request bodies should pass through unchanged.
- Only inject into likely Responses API creation requests; do not mutate `/models` traffic or unrelated JSON envelopes.

## Dashboard and Static Assets

- Dashboard routes live under `/stats*`, are intentionally unauthenticated, and are meant for trusted internal networks only.
- Only `GET`/`HEAD` should succeed for dashboard routes; other methods should continue returning `405`.
- Dashboard files are embedded from `web/*`. If `web/tailwind.input.css` changes, rebuild `web/tailwind.css` with `cd web && npm run tailwind:build`; do not hand-edit generated CSS.
- Keep dashboard API responses stable unless tests and README are updated with the contract change.

## Go Code Requirements

- Keep code idiomatic Go 1.25. Use standard-library tools such as `slices`, `maps`, `cmp`, `min`, `max`, and `range int` when they make the code clearer.
- Keep concrete project types concrete. Prefer `*TokenStore`, `*UsageDB`, `*UsageSink`, and `*Server` over invented abstractions.
- Define interfaces at the consumer boundary, not beside the producer. If a function only needs `ExecContext`, a small local interface like `usageExecer` is fine; do not create broad provider-side interfaces such as `TokenStoreInterface`.
- Delete interfaces with a single implementation unless they are required by a standard-library boundary or a very narrow test seam.
- Accept narrow interfaces when they reduce coupling; return concrete structs so callers can use the real project type.
- Do not use `any` or `interface{}` except at real dynamic boundaries such as JSON, SQL driver APIs, or standard-library signatures.
- Do not merge structs just because their fields currently match. API payloads, DB records, dashboard responses, and in-memory token state should remain separate when they represent different contracts.
- Avoid `reflect` and `unsafe`.

## Naming and Shape

- Avoid vague names: no `Manager`, `Helper`, `Base`, or `Common`. Use `Handler` only for HTTP handlers.
- Prefer Go names already used in the repo: `ctx`, `err`, `req`, `resp`, `mu`, `cfg`, `rec`, `ref`.
- Prefer behavior names over `Get` prefixes. Use `ID()` rather than `GetID()` when adding methods.
- If a function needs more than 3 non-context parameters, introduce a small config/request struct. Use the options pattern only for constructors with many optional settings.
- Keep comments focused on why a proxy, concurrency, SQLite, or upstream-compatibility decision exists. Do not comment obvious line-by-line behavior.

## Error and Concurrency Rules

- Use guard clauses and return early. Avoid deep `else` nesting in proxy and sync control flow.
- Wrap errors with operation context using `%w`; use `errors.New` for fixed sentinel-style errors.
- Use `errors.Is` and `errors.As`; never match errors with `err.Error() == "..."`.
- Error messages should describe the action directly, for example `refresh token: %w`, not start with `failed to`, `unable to`, or `error`.
- Use `context.Context` as the first parameter. Do not store contexts in structs.
- Every goroutine must have a clear exit path through context, shutdown signal, channel close, or a `sync.WaitGroup`.
- Use `sync.RWMutex` for shared in-memory token/session state. Do not use channels for simple state protection.

## Tests and Verification

- Run `gofmt` on changed Go files.
- Run `go test ./...` before finishing Go changes. Use targeted `go test -run TestName ./...` while iterating.
- Add or update table-driven tests for behavior changes, especially around routing, auth, token selection, refresh, usage extraction, retry behavior, SQLite migrations, WebSocket framing, and dashboard JSON.
- Prefer `httptest`, `net.Pipe`, temp directories, and in-memory test fixtures. Default tests must not require live OpenAI/ChatGPT network access or real credentials.
- Live WebSocket smoke coverage must stay opt-in through `WS_SMOKE_ENABLE=1`.
