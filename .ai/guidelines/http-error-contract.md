# HTTP Error Contract Guideline

> Status: DECIDED (B2.1, B2.2). Every failure on `/v1` and `/api/v1` is the
> envelope below, written by a writer of `app/http/responses`; no handler or
> middleware builds the map itself (`TestError_Bodies_GoThroughTheResponsesWriters`).
> Success bodies are written with `ctx.Response().Success().Json` /
> `Status(code).Json` (see `http-layer.md`); `responses` has no success writer. One refusal still carries a
> wrapped error's text in `message` (see "What a body may carry").

Status codes and error codes are a contract. `macro-wallets-front` branches on
them, and external integrators (Markets) consume `/api/v1`. A status or a code
that changes on the server breaks a client.

## The envelope

Every non-2xx answer on every surface — controllers and middleware alike — is
exactly one shape, written only by `app/http/responses`:

```json
{ "error": { "code": "insufficient_funds", "message": "insufficient funds" } }
```

**[DECISION B2.1]** Both surfaces use this object. There is no frozen
`{"error":"<string>"}` on `/api/v1`. Extra fields stay inside `error`.

Markets (`clouapp/back`) reads both this object and the old string, so the two
repos can deploy in either order. That dual-read is the Markets client's
decision, recorded on its PR; this service only emits the object.

Extra fields an action needs travel INSIDE the error object and are part of the
code's contract: `sweep_limit_exceeded` carries `limit_type` and
`retry_after_seconds`; `wallet_not_gas_ready` carries `action`.

**`code` is the contract; `message` is for humans.** A new message on an
existing failure is safe. A new code, or a different status for an outcome a
client already handles, is a spec change.

## Validation

**[DECISION B2.2]** A form-request failure is HTTP 422, not a single 400
message. The keys are the ones `parseApiErrorBody` on `origin/feat/forms` reads:

```json
{
  "error": { "code": "validation_failed", "message": "validation failed" },
  "errors": { "email": ["Email address is required"] }
}
```

`errors` values are string arrays, rule names sorted. A domain 422 (for example
`wallet_not_gas_ready`) has no `errors` map. `code` is snake_case;
`FEE_ESTIMATE_FAILED` stays as the explicit code the handler already sent.

## The writers

| Writer | Wire | Used by |
|---|---|---|
| `responses.Fail(ctx, status, code, message)`, `FailWith(…, fields)` | `ctx.Response().Json`: `application/json; charset=utf-8`, no trailing newline | every refusal that was a legacy `{"error":"text"}` map |
| `responses.FailMessage(ctx, status, message)` | same as `Fail` | a message known only at run time (a policy decision's sentence, a refusal the service picked, a sentinel's text) |
| `responses.Error`, `InternalError`, `ProviderError` | `responses.JSON`: `application/json`, trailing newline | the routes that were written on the envelope from the start |
| `responses.FieldsFailed`, `FieldError`, `ValidationFailed` | same as `Fail` | HTTP 422 with `errors` |

The two writers do not produce the same bytes, and `tests/contract` records
both. A route keeps the writer it has; moving one is a contract change. A
middleware ends the chain with the writer's `.Abort()`.

## The codes

`responses.Code*`, in `app/http/responses`. Each call names its code; the
generic ones are the status default below, and a domain code is spelled with
its constant (`responses.CodeSweepLimitExceeded`, …) or its string.
`FailMessage` is the one place a code is derived from a message, with
`responses.CodeFor`: these sentences map to `invalid_signature`: "missing
request signature", "invalid request signature", "invalid webhook signature";
a message that matches `^[a-z][a-z0-9_]*$` is the code and the message;
anything else takes the status default. Those are the rules the legacy maps
were wrapped with, so the codes did not move when the maps were replaced:
`"unauthenticated"` on a 401 is still its own code, not `unauthorized`.
Generic:

| Code | Status |
|---|---|
| `unauthorized` | 401 |
| `invalid_credentials`, `two_factor_required`, `invalid_code` | 401 on the auth surface |
| `invalid_signature` | 401 (external, bad/missing `X-Signature`) |
| `forbidden`, `account_suspended`, `wallet_frozen` | 403 |
| `not_found` | 404 |
| `conflict` | 409 |
| `invalid_request`, `invalid_json` | 400 |
| `request_too_large` | 413 |
| `too_many_requests` | 429 |
| `internal` | 500 (see below: `internal_error` too) |
| `provider_unavailable` | 502 |
| `unavailable` | 503 |
| `timeout` | 504 |

**Two 500 codes.** `MapInternalError` and 24 handlers answer 500
`{"code":"internal_error","message":"internal_error"}`; every other 500 is
`internal` (a sentence, `InternalError`, the encode failure). Both reach the
front and Markets. `internal_error` is also the persisted withdrawal
`failure_reason`. Unify only with a coordinated change: the front adds
`errorCodes.internal` to its three locales first, Markets is told, then the
backend moves the 25 `internal_error` sites to `internal`.

Domain codes that exist today and must survive the migration (inventory them
from `controllers/errors.go`, `controllers/withdrawal_errors.go`, `withdraw/failure.go` and the handlers before
changing anything): `sweep_limit_exceeded`, `wallet_not_gas_ready`,
`insufficient_funds`, `unsupported_chain`, `gas_estimate_failed`,
`FEE_ESTIMATE_FAILED` (rename to snake_case only if decided), and the persisted
public withdrawal failure codes (`too_many_attempts`, …).

## What each status means

| Status | When | Caller can |
|---|---|---|
| 400 | unparseable body, or a rule failed | fix the request |
| 401 | no valid session/token, bad signature, a factor is missing | authenticate / present the factor |
| 403 | authenticated but not permitted, account suspended, wallet frozen | nothing — ask for the grant |
| 404 | the resource does not exist, or is not the caller's | stop asking |
| 409 | the resource is in a state that refuses the action | wait or change state |
| 422 | a domain refusal the caller can act on (insufficient funds, below dust, not gas-ready) | change the request |
| 429 | a rate limit or a sweep/withdrawal quota refused | back off (`Retry-After` header, or `retry_after_seconds` in a quota body) |
| 500 | our fault | retry later |
| 502 | a chain node, RPC or provider failed | retry later |
| 503 | not configured, or a store we fail closed on is unreachable | retry later |
| 504 | the request outlived its deadline | retry later |

**A 4xx must be something the caller can act on.** Mapping every error of a call
to one 4xx (today: any wallet-creation error → 409 with `err.Error()`) hides an
outage behind a message about the caller.

## What a body may carry

- **Never a wrapped error's text.** `err.Error()` of a wrapped error can carry
  an RPC URL with its API key, a SQL fragment, or what the customer typed. A
  fixed sentinel's text (`settings.ErrViewForbidden.Error()`) is a sentence
  like any other and may be the message. Known exception: a withdrawal create
  refusal for an invalid amount or a spending-limit read carries the cause's
  text (`withdraw/create.go`, through `MapWithdrawalError`).
- **Never a provider's raw text** — `responses.ProviderError` answers the
  endpoint's own message and logs the cause.
- A secret field never comes back on a read: `"<field>Set": true|false`.
- `POST /v1/auth/recover` answers the same whether or not the address exists.

## Proving a refactor kept the contract

Record method, path, status and body for every request the suite makes — on the
base commit and on yours — and diff. Every difference must be one you meant,
and the intended ones are listed at the end of this file with the date.

The recording is `TestContract_HTTP_Contract` in `tests/contract` (`make contract`): a fixed
scenario whose normalized raw bodies are compared byte for byte with
`tests/contract/testdata/http_contract.txt`. A refactor keeps it green; a decided
change rewrites it (`make contract-update`) in the same PR that adds its row below.

## Rate limits (`middleware.Throttle`)

A request over a limit gets 429 `too_many_requests` in the envelope
(`{"error":{"code":"too_many_requests","message":"too many requests"}}`) with the
framework's `Retry-After`, `X-RateLimit-Limit`, `X-RateLimit-Remaining` and
`X-RateLimit-Reset` headers. The limiters live in `middleware.RegisterThrottles`;
their keys are built from `middleware.ClientIP` (so `TRUSTED_PROXIES` must match
the deployment) and the limits come from `http.throttle.*` (`THROTTLE_*` env, per
minute, `0` turns one off). The counters are in the default Cache (Redis); a cache
failure lets the request through.

| Limiter | Routes | Key | Default |
|---|---|---|---|
| `auth-login` | `POST /v1/auth/login` | client IP + email; client IP | 10/min; 60/min |
| `auth-recover` | `POST /v1/auth/recover` | client IP + email; client IP | 5/min; 60/min |
| `auth` | `/v1/auth/{2fa/verify,refresh,register,recover/confirm,invites/accept}` | client IP + path | 60/min |
| `api` | the whole `/api/v1` group | the bearer token (hashed), or the client IP without one | 600/min |
| `gas-check` | `POST .../wallets/{walletId}/gas-check` (both surfaces) | wallet | 1/min, body below |

`gas-check` keeps the body it always had, not the envelope code above:
`{"error":{"code":"rate_limited","message":"rate_limited","limit_type":"gas_check","retry_after_seconds":60}}`.

New tests for error paths assert the body: `s.AssertError(rec, status, code, message)`.

## Intended differences

Recorded with the error-envelope switch. Success bytes are unchanged except the
two bugfixes that landed in the commits before it.

| Method and path | Condition | Was | Is |
|---|---|---|---|
| `POST /v1/auth/register` | valid body, 2026-10-02, nil preferences inserted as NULL | 500 `failed to create user` (preferences NULL) | 201, the user, `preferences: {}` |
| `POST /v1/auth/register` | e-mail already registered, 2026-10-02, registration persists the user | 500 | 422 `validation_failed`, `errors.email` |
| `PATCH /v1/users/me`, `PATCH /v1/accounts/{id}` | body, request has no rules, 2026-10-02, empty Rules returned before binding | 200, body ignored | 200, the field is applied |
| any failure | 2026-10-02, one error envelope on every failure; string `error`, raw Goravel 422, or empty middleware body | those shapes | `{"error":{"code","message"}}`; validation also has `errors` |
| `GET /health` | 2026-10-03, scanner already on the branch | `{"status","version"}` | also `deposit_scanner` |
| `POST /v1/accounts/{id}/users` | invalid role, 2026-10-03, account roles | enum `owner admin viewer` | enum `owner admin auditor user` |
| `GET /v1/chains`, `GET /api/v1/chains` | 2026-10-03, base/arbitrum/bsc from #1 | eth, btc, polygon, sol and their testnets | also `base`, `tbase`, `arbitrum`, `tarbitrum`, `bsc`, `tbsc` |
| `GET /v1/chains`, `GET /api/v1/chains` | 2026-10-06, TRON and Litecoin (6704fa2), XRP Ledger (368b082) seeded | the chains above | also `tron`, `ltc`, `xrp` |
| `GET /v1/wallets/{id}/settings` | 2026-10-03, label already returned by the settings controller | no `label` | `label` plus the same fee fields (`fee_multiplier` stays JSON null) |
| `GET /v1/users/me` | 2026-10-05, active feature keys | no `features` | `features` lists the globally active flags |
| `GET /v1/accounts/{id}` | 2026-10-05, account feature keys | no `features` | `features` lists the active flags |
| `GET /v1/users/me/accounts` | 2026-10-05, caller role | no `role` | `role` |
| `POST /v1/auth/2fa/verify` | body names `challenge_token`, 2026-10-05, second-factor token rename | 422, `partial_token` required | 401 `unauthorized`, `invalid or expired partial token` |
| any route in the table above | 2026-10-08, rate limiting (H1) | no limit, never 429 | 429 `too_many_requests` over the limit (contract steps 69 and 70) |
| `POST /v1/auth/logout` | 2026-10-08, logout goes through the session watermark | the presented token only was refused afterwards (the other devices' access tokens lived until they expired) | every access and refresh token of the user is refused: 401 `unauthorized`, `session revoked`; activity `user.sessions_revoked` |
