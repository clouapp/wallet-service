# HTTP Error Contract Guideline

> Status: TARGET, pending DECISION B2.1/B2.2 of the alignment prompt. Today at least
> five error shapes coexist and 28 handlers return `err.Error()`. Migration: §3.5.

Status codes and error codes are a contract. `macro-wallets-front` branches on
them, and external integrators (Markets) consume `/api/v1`. A status or a code
that changes on the server breaks a client.

## The envelope

Every non-2xx answer on every surface — controllers and middleware alike — is
exactly one shape, written only by `app/http/responses`:

```json
{ "error": { "code": "insufficient_funds", "message": "insufficient funds" } }
```

> **[DECISION 2.1]** The block above is the recommended shape (slotkit). If the
> decision is xip's `{"error":"<string>"}`, or a transition (coded envelope on
> `/v1`, current shape frozen on `/api/v1` until `/api/v2`), replace this
> section and record it in the README's "Deliberate choices".

Extra fields an action needs travel INSIDE the error object and are part of the
code's contract: `sweep_limit_exceeded` carries `limit_type` and
`retry_after_seconds`; `wallet_not_gas_ready` carries `action`.

**`code` is the contract; `message` is for humans.** A new message on an
existing failure is safe. A new code, or a different status for an outcome a
client already handles, is a spec change.

## Validation

> **[DECISION 2.2]** Recommended: 400 `invalid_request`, ONE message (the first
> field of `FieldOrder()`). Today the API answers 422 with Goravel's raw error
> bag; if the front renders per-field errors, keep 422 with a declared
> `errors` map instead and say so here.

## The codes

`resources.Code*`, and nothing outside `app/http/resources/error_resource.go`.
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
  a SQL fragment, or what the customer typed.
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

(empty — append a table row per intended change: method, path, condition, was, is)
