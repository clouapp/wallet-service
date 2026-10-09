# Queues and workers

> Status: HOLDS. One consumer per queue, thin jobs with typed payloads and no
> domain events are guarded by `tests/architecture` (`queue_runner_test.go`,
> `thin_jobs_test.go`, `thin_commands_test.go`).

## What runs where

| Mechanism | Used for | Consumer |
|---|---|---|
| Goravel queue (`QUEUE_CONNECTION`, default `sync`) | credential mail (`SendCredentialMailJob`, always `DispatchSync`) | runs in the dispatching process. A `database` connection (queue `blockchain`, `failed_jobs`) is configured, but nothing consumes it |
| `localworkers` balance loop | the wallet balance read model (`WalletRefresher.RefreshAll`, every `vault.local_workers` interval) | started from `main.go` only in local; the deployed Lambdas have no balance refresher yet |
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
- **No domain events.** The app registers none (`bootstrap/app.go` has no
  `WithEvents`): Goravel's `Dispatch` fails with `EventListenerNotBind` for an
  event without a listener. Add an event only together with a listener that
  does something.
- **No queue without a consumer.** The wallet refresh jobs were removed because
  nothing ran `queue:work` and there is no `jobs` table; a wallet's balances
  are refreshed by the `localworkers` loop and by the `refresh:*` /
  `reconcile:wallet` commands, which run in process. Add a queue only together
  with its worker and its table.
- Services never call `facades.Queue()` directly; they get a
  dispatcher port.
- Artisan commands are thin (resolve a typed service → one call), end through one
  `fail(ctx, err)` helper with a non-zero exit, and contain no query.

## Tests

- Each job: payload round-trip, the service call, and "payload carries no
  credential".
