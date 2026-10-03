# HTTP Error Contract Guideline

> Status: DECIDED (B2.1, B2.2). Every failure on `/v1` and `/api/v1` is the
> envelope below, written by `app/http/responses`. Success bodies stay on
> `ctx.Response().Json` until the resources migration; this file does not claim
> that migration is done. Some handlers still put `err.Error()` in `message`.

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

## The codes

`responses.Code*`, in `app/http/responses`. The resources package is not part of
this change. A legacy string that matches `^[a-z][a-z0-9_]*$` is the code and
the message. These sentences map to `invalid_signature`: "missing request
signature", "invalid request signature", "invalid webhook signature". Anything
else takes the status default (`invalid_request`, `unauthorized`, `forbidden`,
`not_found`, `conflict`, `unprocessable`, `too_many_requests`, `internal`, …).
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
| `internal` | 500 |
| `provider_unavailable` | 502 |
| `unavailable` | 503 |
| `timeout` | 504 |

Domain codes that exist today and must survive the migration (inventory them
from `controllers/errors.go`, `withdrawal_failure.go` and the handlers before
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
| 429 | a rate limit or a sweep/withdrawal quota refused | back off (`retry_after_seconds`) |
| 500 | our fault | retry later |
| 502 | a chain node, RPC or provider failed | retry later |
| 503 | not configured, or a store we fail closed on is unreachable | retry later |
| 504 | the request outlived its deadline | retry later |

**A 4xx must be something the caller can act on.** Mapping every error of a call
to one 4xx (today: any wallet-creation error → 409 with `err.Error()`) hides an
outage behind a message about the caller.

## What a body may carry

- **Never `err.Error()`.** A wrapped error can carry an RPC URL with its API key,
  a SQL fragment, or what the customer typed. The envelope switch did not do
  that redaction: a handler that already sent `err.Error()` still does, now as
  `message`.
- **Never a provider's raw text** — `responses.ProviderError` answers the
  endpoint's own message and logs the cause.
- A secret field never comes back on a read: `"<field>Set": true|false`.
- `/forgot-password` answers the same whether or not the address exists.

## Proving a refactor kept the contract

Record method, path, status and body for every request the suite makes — on the
base commit and on yours — and diff. Every difference must be one you meant,
and the intended ones are listed at the end of this file with the date.

The recording is `TestHTTPContract` in `tests/contract` (`make contract`): a fixed
scenario whose normalized raw bodies are compared byte for byte with
`tests/contract/testdata/http_contract.txt`. A refactor keeps it green; a decided
change rewrites it (`make contract-update`) in the same PR that adds its row below.

New tests for error paths assert the body: `s.AssertError(rec, status, code, message)`.

## Intended differences

Recorded with the error-envelope switch. Success bytes are unchanged except the
two bugfixes that landed in the commits before it.

| Method and path | Condition | Was | Is |
|---|---|---|---|
| `POST /v1/auth/register` | valid body | 500 `failed to create user` (preferences NULL) | 201, the user, `preferences: {}` |
| `POST /v1/auth/register` | e-mail already registered | 500 | 422 `validation_failed`, `errors.email` |
| `PATCH /v1/users/me`, `PATCH /v1/accounts/{id}` | body, request has no rules | 200, body ignored | 200, the field is applied |
| any failure | string `error`, raw Goravel 422, or empty middleware body | those shapes | `{"error":{"code","message"}}`; validation also has `errors` |
| `GET /health` | 2026-10-03, scanner already on the branch | `{"status","version"}` | also `deposit_scanner` |
| `POST /v1/accounts/{id}/users` | invalid role, 2026-10-03, account roles | enum `owner admin viewer` | enum `owner admin auditor user` |
| `GET /v1/chains`, `GET /api/v1/chains` | 2026-10-03, base/arbitrum/bsc from #1 | eth, btc, polygon, sol and their testnets | also `base`, `tbase`, `arbitrum`, `tarbitrum`, `bsc`, `tbsc` |
| `GET /v1/wallets/{id}/settings` | 2026-10-03, label already returned by the settings controller | no `label` | `label` plus the same fee fields (`fee_multiplier` stays JSON null) |
