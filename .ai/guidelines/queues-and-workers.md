# Queues and workers

> Status: HOLDS. One consumer per queue, thin jobs with typed payloads and no
> domain events are guarded by `tests/architecture` (`queue_runner_test.go`,
> `thin_jobs_test.go`, `thin_commands_test.go`).

## What runs where

| Mechanism | Used for | Consumer |
|---|---|---|
| Goravel queue (`QUEUE_CONNECTION`, default `sync`) | credential mail (`SendCredentialMailJob`, always `DispatchSync`) | runs in the dispatching process. A `database` connection (queue `blockchain`, `failed_jobs`) is configured, but nothing consumes it |
| `localworkers` balance loop | the wallet balance read model (`WalletRefresher.RefreshAll`, every `vault.local_workers` interval) | started from `main.go` only in local; the deployed Lambdas have no balance refresher yet |
| AWS SQS (`app/adapters/queue/sqs`) | outbound webhook delivery | the `webhook_worker` Lambda, or on a host without Lambda the `webhook_poller` process (same handler); with no queue configured, the `localworkers` outbox |
| `localworkers` | local stand-in for the deployed workers (goroutine tickers): withdrawal confirmations, the deposit event backfill of the locally scanned chains, and the outbox (delivered locally without a webhook queue, re-dispatched to it with one) | started from `main.go` only in local |

Deployment (Lambdas, `vault.lambda_mode`, EventBridge, SAM) is out of scope for
this guideline and is not changed by the reorganization; `main.go` only has to
keep compiling.

## Rules

- **One consumer per queue.** A second worker for the same queue, or a goroutine
  loop draining what the deployed worker drains, is refused (`tests/architecture`
  `queue_runner_test.go`).
- **The `webhook_poller` replaces the `webhook_worker` Lambda, never runs beside it.**
  It is its own process (`LAMBDA_MODE=webhook_poller`), long-polls
  `vault.queues.webhook` and hands each batch to `handleWebhookWorker`, so it is the
  same consumer with another trigger. Run it only where no Lambda consumes that queue.
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

## Delivery guarantees

Outbound webhooks are delivered **at least once**, never exactly once.

- **The outbox row comes first.** Every event is a `webhook_events` row written before
  the queue send; the queue message only carries ids (event, config) and the payload,
  never the signing secret. A scoped event is one row per config, type and subject
  (partial unique index, migration 260), so a repeated publish is a no-op. An event
  that reports progress puts its step in the subject: `deposit.confirming` uses
  `<transaction id>:<confirmations>`, one row per count (`CLAUDE.md` §7).
- **Receivers deduplicate on `X-Vault-Delivery-Id`** (the row id). Every resend — a
  queue retry, a re-dispatch, a redelivery — keeps it.
- **A status persisted before its event is published needs a backfill.** The tracker
  writes a deposit's status, then publishes; a failed publish is logged and repaired by
  `deposit.Service.BackfillDepositEvents` on the next tracker run (deposits confirmed in
  the last 48 h with a subscribed config, existing at the confirmation, that has no
  `deposit.confirmed` row). Withdrawals have the same repair
  (`withdrawalevents.Publisher.Backfill`). A new status-then-publish path adds its own
  backfill, keyed on the outbox row, and stamps the republished event with
  `ScopedEvent.OccurredAt` so a config created later is not sent history.
- **An on-chain failure publishes before it writes its status, and needs no backfill.**
  `withdrawal.failed` and `deposit.failed` (a deposit whose transaction failed on chain)
  are published first and the row is marked failed last, so a failed publish leaves the
  row as it was and the next tracker pass (or `deposits:verify-receipts` run) publishes
  again; the outbox row keeps that a no-op for the configs that already got it.
- **In queue mode a lost message is re-dispatched.** `webhook.Service.RedispatchStale`
  sends again the pending rows untouched for 5 minutes; each claim counts one attempt,
  so a row is sent at most `max_attempts` times and never more often than every 5
  minutes. It runs from the `confirmation_tracker` Lambda (every minute) and from the
  local workers when a queue is configured, never where the local outbox delivers.
- **A client can ask for a redelivery** (`POST /api/v1/webhooks/{webhookId}/redeliver`):
  at most 500 events of the last 7 days per request; an event already pending and queued
  in the last minute is left alone.

## Tests

- Each job: payload round-trip, the service call, and "payload carries no
  credential".
