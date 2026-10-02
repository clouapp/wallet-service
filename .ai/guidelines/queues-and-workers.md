# Queues and workers

> Status: PARTLY HOLDS (the split exists). TARGET: one consumer per queue,
> thin jobs with typed payloads, no dead events, the split written down and
> tested. Migration: alignment prompt (Part 1) §3.10.

## What runs where

| Mechanism | Used for | Consumer |
|---|---|---|
| Goravel queue (`database` connection, queue `blockchain`) | wallet refresh jobs (balances, transactions, tokens, UTXOs), reconcile | the framework's queue runner |
| AWS SQS (`app/adapters/queue/sqs`) | outbound webhook delivery | the webhook worker (local: `localworkers`) |
| `localworkers` | local stand-in for the deployed workers (goroutine tickers) | started from `main.go` only in local |

Deployment (Lambdas, `vault.lambda_mode`, EventBridge, SAM) is out of scope for
this guideline and is not changed by the reorganization; `main.go` only has to
keep compiling.

## Rules

- **One consumer per queue.** A second worker for the same queue, or a goroutine
  loop draining what the deployed worker drains, is refused (`tests/architecture`
  `queue_runner_test.go`).
- **Jobs are thin**: decode a typed payload → call one service method. Payload
  validation is a `decode` in the job, not positional `args ...any` checked by
  hand.
- **No job payload carries a credential** (tokens, passphrases, shares, secrets,
  rendered credential e-mails). Pass ids; the worker loads and mints.
- Jobs are idempotent: a redelivery must not double-broadcast, double-credit a
  deposit or double-send a webhook (idempotency keys / state checks by
  constraint).
- `ShouldRetry` distinguishes a known failure (retry) from an unknown outcome
  (do not retry; e.g. a broadcast whose result is unknown is reconciled, not
  re-sent).
- **Events**: every registered event has a dispatcher and a listener. Remove
  events nobody dispatches and listeners nobody registers. If an event only
  enqueues one job, the service dispatches the job through a `Dispatcher` port.
- Services never call `facades.Queue()` / `facades.Event()` directly; they get a
  dispatcher port.
- Artisan commands are thin (resolve a typed service → one call), end through one
  `fail(ctx, err)` helper with a non-zero exit, and contain no query.

## Tests

- Each job: payload round-trip, the service call, and "payload carries no
  credential".
