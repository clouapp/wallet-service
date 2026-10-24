# Production runtime on AWS

Status: **PROPOSED.** Nothing here is deployed. The owner decides the open points in
[§14](#14-decisions-for-the-owner) before any production stack is written. Checked against
`origin/main` at `75ee3c5b`. Every claim names the file or config key it rests on; a claim
about AWS behaviour is marked as such.

## Summary

- **The service is a set of long-running processes.** The deposit scan loops pace each chain
  in seconds. The MPC pre-parameters pool lives in the memory of the process that serves the
  keygen. The webhook poller long-polls SQS. A future XRPL listener holds a WebSocket open.
  None of them fits a Lambda invocation (§3).
- **`template.yaml` cannot run the service as it stands**, independently of this design (§1).
  This change fixes what was objectively wrong in it: the sub-minute schedules, the
  `withdrawal_worker` mode, and the duplicated `Architectures`. It is not made into a runtime.
- **Recommendation: Option A, ECS Fargate behind an ALB** (§4). One image runs three services:
  `api` (HTTP and the pre-parameters pool), `worker` (exactly one task: the scan, confirmation,
  re-dispatch and balance loops) and `webhook-poller`. Nothing needs to stay on Lambda. The
  daily webhook reconciler may stay there if provider ingest is used (D3).
- **Option B, EC2 behind an ALB** (§10), is the dev host's layout moved into AWS. It costs less
  at low load and changes less, but we would own the hosts.
- **Before production:** a few small code changes (§11). One of them (F5) gates running more
  than one API task: the EVM nonce floor is per process.

## 1. Where we are

**Dev** runs on one host outside AWS (Forge), under supervisor (`docs/AWS_DEV_STACK.md`):

- the API in local mode: `LAMBDA_MODE` empty → `runLocal` in `main.go`, with the local workers;
- the `webhook_poller` daemon: `LAMBDA_MODE=webhook_poller`, `stopwaitsecs` ≥ 70;
- the AWS stateful resources of the stack `waas-dev` (`template.dev.yaml`, `make deploy ENV=dev`):
  queues, the master KMS key, and an IAM user whose key `make server-credentials` installs on
  the host. `make verify-aws` proves them from the host.

**Staging and prod** point at `template.yaml` (`Makefile`: `TEMPLATE_FILE`, `make deploy
ENV=staging|prod`), the Lambda layout of the same binary.

`sam validate --lint -t template.yaml` failed on `main` (SAM CLI 1.134.0, bundled cfn-lint
1.22.2, run offline):

- `E3027`: `seconds` is not a rate unit;
- `E3032` and `E3037`: `Architectures` is `[arm64, arm64]` on two functions.

The plain `sam validate` that `make validate` ran for staging and prod accepted the template.
This change fixes or decides these points:

| # | Problem in `template.yaml` | In this change |
|---|---|---|
| T1 | Scan schedules `rate(15 seconds)` (eth), `rate(5 seconds)` (polygon, sol). EventBridge rates start at `rate(1 minute)` (AWS rule: minutes, hours or days), so the stack would fail to create | **Removed.** Every scan schedule goes, btc's `rate(1 minute)` too. The worker service scans every chain in its own loops (§4). `DepositScannerFunction` stays, without events, for one-off invocations |
| T2 | base, arbitrum, bsc, tron, ltc, xrp (and the t-prefixed records) had no schedule | Moot after T1: no chain is scheduled on Lambda |
| T3 | `WithdrawalWorkerFunction` sets `LAMBDA_MODE: withdrawal_worker`, which `main.go` does not handle. An unknown mode falls to `runLocal`, so the function never calls `lambda.Start`: every invocation times out, and in the meantime the process starts the local workers | **Removed**, with `make logs-withdrawal`. `WithdrawalQueue` and its DLQ stay, unchanged and unused (nothing produces to or consumes them), matching `template.dev.yaml` (D8) |
| T4 | `Runtime` and `Architectures` repeated on `ConfirmationTrackerFunction` and `WebhookReconcilerFunction`. SAM appends `Globals` lists, so the result is `[arm64, arm64]` (E3032, E3037) | **Removed**: `Globals` sets both |
| T5 | `make validate` linted only `template.dev.yaml`, and the staging/prod deploy ran no validation | `validate` runs `sam validate --lint` for every `ENV`, and the staging/prod deploy runs it first |

New guards in `tests/architecture/lambda_template_test.go`:

- `TestTemplate_LambdaModes_AreHandledByMain`: every `LAMBDA_MODE` in `template.yaml` is a
  case of `main`'s switch;
- `TestTemplate_Schedules_AreValidEventBridgeExpressions`: every `Schedule` and
  `ScheduleExpression` in both templates is a valid EventBridge expression.

Both fail on `main`'s template (checked) and pass after the change.

**Still wrong in `template.yaml`, left as it is.** These belong to the stack that replaces it
(§8):

| # | Problem | Evidence |
|---|---|---|
| T6 | The database is passed as `DATABASE_URL`. The app reads `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD` and `DB_SSLMODE`, and sets no `dsn` | `config/database.go`; only `tools/macro-e2e` reads `DATABASE_URL` |
| T7 | `APP_KEY` and `JWT_SECRET` are not passed, so every function panics at boot | `config.ValidateSecrets`, called from `bootstrap.checkBootConfig` |
| T8 | `VaultApi` is `AWS::Serverless::Api` (REST API, payload 1.0), but `handleAPIGateway` decodes `events.APIGatewayV2HTTPRequest` (`httpadapter.NewV2`, HTTP API payload 2.0) | `main.go` |
| T9 | No `VpcConfig`, so no function reaches a private RDS or ElastiCache. No function may read or write `vault/wallet/*` or use the KMS key, so wallet creation cannot store share B | Compare `ServerPolicy` in `template.dev.yaml` (`WalletSecrets`, `MasterKeyUse`) |
| T10 | The `MasterKeyRef` default is `local:dev-master-key`. A local reference leaves share B on the Secrets Manager default key | `app/adapters/secretsmanager/kms_key.go` |
| T11 | `API_KEY_SECRET` and `WEBHOOK_INGEST_BASE_URL` are passed but read by no code (`vault.api_key_secret` is declared and never read); `ENV` is read only to keep `APP_ENV=testing` from allowing loopback webhook receivers on a prod or staging deployment (`vault.deployment.env`) | `config/vault.go` |
| T12 | In every Lambda mode the pre-parameters pool never runs, so a secp256k1 keygen searches its own primes inside a 29 s API invocation | §3, §5.3 |

## 2. The processes

All of them are the same binary (`main.go`, CLAUDE.md §1). "Local workers" means
`app/services/localworkers`, which `runLocal` starts when `LOCAL_WORKERS_ENABLED` is true
(default true).

| Process | Entry | Cadence | Writes | Single writer? |
|---|---|---|---|---|
| HTTP API (`/v1`, `/api/v1`, ingest, `/health`) | `runLocal` | requests; hard cut at `HTTP_REQUEST_TIMEOUT` (30 s, `bootstrap.RequestTimeoutHandler`) | everything a request writes; signs and broadcasts withdrawals and sweeps inside the request (`withdraw.Service.Request` → `sweep.ExecutePlan`) | no, but see §5.4 (nonce floor) |
| MPC pre-parameters pool | local workers (`mpc.PreParamsPool.Run`) | refills one set at a time, on one goroutine (`GeneratePreParamsWithContext(ctx, 1)`) | process memory only (CLAUDE.md §6) | per process, by design |
| Deposit scan loops | local workers, `LOCAL_DEPOSIT_SCAN_CHAINS` | every `LOCAL_DEPOSIT_SCAN_INTERVAL_SECONDS` (5). One shared loop for eth, polygon, btc, ltc, xrp and their t-records; one loop each for sol, base, arbitrum, bsc and tron (`dedicatedScanChains`) | deposits, checkpoints (Postgres + Redis copy), pending blocks (Redis + `DEPOSIT_PENDING_DIR`), deposit webhooks; confirmations of the chain's pending rows after each successful cycle (`updateConfirmations`) | **yes**, per chain (§5.1) |
| Deposit signature sync loop | local workers, the chains of `LOCAL_DEPOSIT_SCAN_CHAINS` that the platform `deposit_signatures` setting lists (`DEPOSIT_SIGNATURE_CHAINS` without a stored row; none by default) | ticks every 15 s; syncs a listed chain once `interval_seconds` (`DEPOSIT_SIGNATURE_INTERVAL_SECONDS`, 600) elapsed since its last sync started, read on each tick. RPC cost in steady state: three calls per account a cycle (`getSlot`, a `getSignaturesForAddress` page, then the confirmation `getSignaturesForAddress` or `getFirstAvailableBlock`), plus one `getTokenAccountsByOwner` per address and token program and two `getSlot`: about 18 400 calls a day for 36 accounts at 600 s, against about 350 000 `getBlock` for the block scan; a backfill or gap read adds one `getSignatureStatuses`, and while a transaction of the chain is pending the scan loop's confirmation step adds a `getSlot` every 5 s tick, up to about 17 300 a day (CLAUDE.md "Solana deposits", RPC cost) | deposits (the scanner's path), `deposit_signature_cursors` (saved every 50 signatures and when an account stops, so a cut cycle keeps its work), pending blocks the histories cover (dropped), deposit webhooks; the chain's confirmation step at the end of each cycle. The chain's block reads pause meanwhile; its scan loop keeps running the confirmation step and the pending retry, and records the mode for `/health` (CLAUDE.md "Solana deposits"). The `deposit_scanner` Lambda syncs a listed chain once its interval elapsed since the last completed cycle | **yes**, per chain: the same lock as the scan, renewed (§5.1) |
| Confirmation loop | local workers | every `LOCAL_CONFIRMATION_INTERVAL_SECONDS` (30) | withdrawals only (`RunWithdrawalConfirmationCheck`), plus the deposit event backfill of the scanned chains | should be one |
| Outbox re-dispatch | local workers, when `WEBHOOK_QUEUE_URL` is set | same interval as confirmations | claims stale `webhook_events` rows (`FOR UPDATE SKIP LOCKED`) and sends them to SQS | safe concurrently |
| Local outbox delivery | local workers, only when `WEBHOOK_QUEUE_URL` is empty | `LOCAL_WEBHOOK_DELIVERY_INTERVAL_SECONDS` (5) | posts webhooks | never beside a queue consumer (`main.go` `DeliverOutbox`) |
| Balance refresh | local workers | `LOCAL_BALANCE_REFRESH_INTERVAL_SECONDS` (60; 0 turns it off), `LOCAL_BALANCE_REFRESH_SPACING_MS` (500) between wallets | wallet balance read model | should be one (two double the RPC load) |
| Webhook poller | `LAMBDA_MODE=webhook_poller` | long poll of 20 s, batches of 5, batch deadline 55 s (`app/adapters/queue/sqs/poller.go`) | delivery status of `webhook_events` | one *kind* of consumer per queue; several pollers compete safely (at-least-once delivery, `X-Vault-Delivery-Id`) |
| Webhook reconciler | `LAMBDA_MODE=webhook_reconciler` (Lambda only, no artisan command) | `rate(1 day)` in `template.yaml` | provider address subscriptions (`webhooksync.Service.RunReconciliation`) | yes (provider-side state) |
| Periodic artisan commands | `price:check-update`, `activity:prune` | **nothing in the repository schedules them** | prices (`currencies`; the USD spending limit reads them, `withdraw/spending_limit.go`), activity rows | one run at a time |
| Optional listeners | `price:websocket` (CoinAPI), a future XRPL WebSocket | long-running | prices, deposits | one per stream |

The Lambda modes `deposit_scanner`, `confirmation_tracker` and `webhook_worker` are the same
bodies with another trigger. The `confirmation_tracker` runs `RunConfirmationCheck`: every
chain and every transaction type, and the outbox re-dispatch when a queue is set.

**Gap:** in the local layout, deposits, sweeps and gas seeds are confirmed only inside a scan
cycle of their chain that read new blocks without error (`deposit.Service.scanLatestBlocks` →
`updateConfirmations`). A chain left out of `LOCAL_DEPOSIT_SCAN_CHAINS`, or one whose scan
keeps failing, therefore stops confirming them. F1 (§11) closes this gap.

## 3. What fits Lambda, and what does not

- **Scanning:** an EventBridge rate starts at one minute (AWS rule). The loops pace chains in
  seconds: an XRP ledger closes every 3–5 s, and Solana produces about 2.5 slots/s (CLAUDE.md
  "XRP deposits", "Solana deposits"). A one-minute Lambda scan means detection latency of a
  minute or more, and each cold start rebuilds the registry, the RPC health and the address
  cache.
- **Pre-parameters pool:** no Lambda mode starts the local workers (`main.go`), and
  `PreParamsPool.Take` returns nil when `Run` is not running. Each secp256k1 keygen then searches
  its own safe primes inside the request: "minutes on a two-core host" (`config/vault.go`,
  `vault.mpc.preparams_pool_size`). That exceeds the 29 s `ApiFunction` timeout and the
  API Gateway integration limit (29 s REST, 30 s HTTP API; AWS limits). A Lambda environment is
  also frozen between invocations (AWS behaviour), so a background fill would not progress.
- **Webhook poller and listeners** are processes by definition.
- **May stay on Lambda:**
  - `webhook_worker`, as the queue's consumer **instead of** the poller, never beside it
    (CLAUDE.md §7). It needs `VpcConfig` (it writes `webhook_events`) and a NAT (it posts to
    receivers).
  - `webhook_reconciler`, daily, if provider ingest is used (D3).
  - `confirmation_tracker` fits technically, but beside the worker it would be a second tracker.
  - `api` does not fit.

## 4. Option A (recommended): ECS Fargate behind an ALB

```
                 Internet
                    │ HTTPS (ACM certificate)
            ┌───────▼────────┐  public subnets, 2 AZs
            │      ALB       │  (+ NAT gateway per AZ)
            └───────┬────────┘
                    │ :PORT, target group health /health
   ┌────────────────▼─────────────────┐  private subnets, 2 AZs
   │ ECS service api (1 task, then N) │
   │   runLocal + pre-params pool     │
   ├──────────────────────────────────┤
   │ ECS service worker (exactly 1)   │──── not in the target group
   │   runLocal + every local loop    │
   ├──────────────────────────────────┤
   │ ECS service webhook-poller (1+)  │──── no port
   │   LAMBDA_MODE=webhook_poller     │
   ├──────────────────────────────────┤
   │ RDS PostgreSQL (Multi-AZ)        │
   │ ElastiCache Redis (primary +     │
   │   replica, cluster mode off)     │
   └──────────────────────────────────┘
   egress: NAT → RPC providers, webhook receivers, mail
   AWS APIs: Secrets Manager, KMS, SQS, ECR, CloudWatch Logs (NAT or VPC endpoints, D7)
   EventBridge Scheduler → ECS RunTask (artisan commands)
```

All three services use one image and one tag (the git sha). They differ only in environment:

| Key | `api` | `worker` | `webhook-poller` |
|---|---|---|---|
| `LAMBDA_MODE` | empty | empty | `webhook_poller` |
| `LOCAL_WORKERS_ENABLED` | `true` (the pool runs only with the local workers) | `true` | n/a |
| `LOCAL_DEPOSIT_SCAN_CHAINS` | empty | every active chain | n/a |
| `LOCAL_BALANCE_REFRESH_INTERVAL_SECONDS` | `0` | `60` (default) | n/a |
| `LOCAL_CONFIRMATION_INTERVAL_SECONDS` | `86400` until F1: the loop cannot be turned off (`localworkers.Config.validate` wants ≥ 1 s) and `every` waits one interval before its first run. The outbox re-dispatch shares this interval; the worker runs both | `30` (default) | n/a |
| `MPC_PREPARAMS_POOL_SIZE` | > 0 (§5.3) | `0` | `0` |
| `WEBHOOK_QUEUE_URL` | set, so the outbox is re-dispatched, never delivered locally | set | set |
| `LOG_CHANNEL` | `stdout` | `stdout` | default (`stdout` whenever `LAMBDA_MODE` is set) |
| `LOG_LEVEL` | `info` | `info` | `info` |
| `APP_ENV` | `production` | `production` | `production` |
| Desired count | 1 until F5, then ≥ 2 across AZs | **exactly 1** | 1 (more only if the queue age grows) |
| Deployment | min 100 % / max 200 %, circuit breaker with rollback | **min 0 % / max 100 %** (stop, then start: two scanners never overlap) | min 0 % / max 100 % or 100/200 |
| `stopTimeout` | ≥ `SHUTDOWN_TIMEOUT_SECONDS` + 5 (default 15 → 30 s) | same | **90–120 s**: a batch runs to its end, up to 55 s (Fargate maximum 120 s) |
| Size (start, to measure) | 2 vCPU / 4 GB (the pool fill holds one core) | 1 vCPU / 2 GB | 0.25 vCPU / 0.5 GB |

Two keys need a value of their own:

- **`LOG_CHANNEL=stdout` is required on `api` and `worker`.** With `LAMBDA_MODE` empty, the
  default channel is `stack` → `daily`, a file under `storage/logs/`
  (`app/providers/log_redaction.go`, `defaultLogChannel`). Inside a container that file never
  reaches CloudWatch. `LOG_LEVEL` defaults to `debug`.
- **`TRUSTED_PROXIES` = the ALB subnets' CIDRs.** The peer the API sees is an ALB node, and
  `middleware.ClientIP` believes `X-Forwarded-For` only from listed peers. With the default
  (loopback), every client would be the ALB: the rate limits and the token `ip_cidr` check
  would key on it (CLAUDE.md §4, `.env.prod.example`). Never list `0.0.0.0/0`.

**Scheduled tasks.** EventBridge Scheduler runs ECS `RunTask` with the same task definition,
overriding the command to `["/usr/local/bin/vault-server", "artisan", "<command>"]` (`main` runs
artisan when it has arguments):

- `price:check-update`, `activity:prune`, cadence D12;
- `migrate`, run by the deploy, not on a schedule (§8);
- the webhook reconciliation: the `webhook_reconciler` mode only calls `lambda.Start`, so a task
  needs F2. Otherwise keep `WebhookReconcilerFunction` as the one Lambda (D3).

The ECS services run tasks for as long as they live; the scheduled tasks run once and exit.

## 5. Single-writer and scaling constraints

### 5.1 Per-chain scan lock

- **The lock.** `vault:lock:deposit_scan:<chain>` (`app/services/deposit/scan_lock.go`) is
  `cacheguard.Acquire`, the cache driver's `Add` (Redis) with a 2 min TTL. Release is `Forget`. It has no owner
  token and no renewal, and a local scan cycle has no deadline (`localworkers.scanChain` passes
  the worker context).
- **What can go wrong.** A cycle longer than 2 minutes (a 500-block catch-up behind a slow RPC)
  loses the lock. A second scanner can then take the chain, and the first scanner's release
  deletes the second scanner's lock.
- **Why the data still holds:**
  - the unique deposit index rejects a second insert of a deposit (`processTransfer`);
  - the outbox unique index makes a repeated publish a no-op (migration 260);
  - the checkpoint never moves back (CLAUDE.md §7).
- **What two scanners still cost:** double RPC load, and events in a different order.
- **Hence:** the lock is a guard, not a leader election. **Exactly one process scans:** the
  `worker` service, desired count 1, deployed stop-then-start. F4 adds an owner token
  (compare-and-delete release) so that a lapsed lock cannot be deleted by its former holder.
- **On stop.** SIGTERM stops the loops within `SHUTDOWN_TIMEOUT_SECONDS`. A cycle cut there
  leaves its lock until the TTL ends, and the next task logs `deposit scan skipped, another
  scanner holds the chain` for at most 2 minutes. That gap is accepted.
- **The signature sync** (`deposit.Service.SyncSignatures`) takes the same key under an owner
  token and renews it while its cycle runs (`cacheguard.AcquireHold`, every 40 s), so a long first
  sync (the history since the scan stalled) keeps the chain, and a release never frees another
  holder's lock. A held lock skips the sync cycle (`deposit signature sync skipped, another scan
  holds the chain`). A renewal that finds another token, or that the cache refuses, loses the hold,
  and so does a check before an account or a signature that finds another token: the cycle stops at
  once (`ErrScanLockLost`), keeps the cursors it saved, and drops and confirms nothing. It runs where
  the chain is in `LOCAL_DEPOSIT_SCAN_CHAINS`, so the one scanning process is also the one syncing.
  When the chain leaves `deposit_signatures`, the block scan resumes after the slot the sync's last
  cycle read every account up to, instead of the checkpoint it stalled at, and from that checkpoint
  when the last cycle did not read them all. In the Lambda, a cycle stops 20 s before the invocation's
  deadline and cuts its reads 6 s before it, so its cursors are saved in time.

### 5.2 One consumer per queue

- The webhook queue is consumed by the poller service **or** by the `webhook_worker` Lambda,
  never both (`.ai/guidelines/queues-and-workers.md`). This rests on the deployment: CLAUDE.md
  §7 marks it UNGUARDED, and `TestOne_Consumer_PerQueue` reads only `template.yaml`.
- An ECS stack that runs the poller must not also deploy `WebhookWorkerFunction`.
  `template.yaml` and the ECS stack are never deployed into one environment.
- Local outbox delivery is off whenever `WEBHOOK_QUEUE_URL` is set (`main.go`, `DeliverOutbox`),
  so every ECS service sets it.
- Several poller tasks are competing consumers of one queue (AWS SQS semantics). That is safe:
  delivery is at least once, and receivers deduplicate on the row id.

### 5.3 Pre-parameters pool and keygen latency

- Each process has its own pool. A secp256k1 keygen takes two sets (`keygenSecp256k1` calls
  `Take` twice); ed25519 takes none.
- While the pool runs and is empty, `Take` waits until the request context ends. Within
  `HTTP_REQUEST_TIMEOUT` (30 s), a drained pool therefore turns secp256k1 wallet creation into
  a 504 until it refills.
- The pool is empty after every task start (deploy, scale-out, replacement). Each task warms
  up on its own.
- **Sizing:** a task serves `MPC_PREPARAMS_POOL_SIZE / 2` back-to-back wallets, then about one
  wallet per `2 × T_gen`. `T_gen` is the time to generate one set on one core of the chosen task
  size: measure it in staging (D4).
- A set is a few KB (Paillier key and Ntilde parameters), so memory does not matter. CPU does:
  the fill keeps one core busy, so the `api` task should have 2 vCPU.
- `/health` does not report the pool level today; F7 adds it.
- **Timeouts, outermost first:**
  - the ALB idle timeout: 60 s by default, configurable (AWS);
  - the app's hard cut: `HTTP_REQUEST_TIMEOUT` (30 s);
  - inner waits: a sweep whose child must see its gas seed in a block waits for it inside the
    request (`sweep.broadcastGasSeed`).

  Keep the ALB idle timeout above `HTTP_REQUEST_TIMEOUT`. If keygen or gas-seed waits need more
  than 30 s, raise both together. Behind API Gateway, the 29–30 s integration limit could not
  be raised that way.

### 5.4 More than one API task

- **EVM nonce floor.** The floor is per process: `app/adapters/chain/evm/nonce.go` keeps the
  next nonce of each sender this process broadcast for, for 10 minutes. It exists for a fallback
  endpoint that has not seen the last broadcast yet (CLAUDE.md "Broadcasts").
- **Locks across tasks.** The withdrawal lock `vault:lock:withdrawal:<wallet>` and the sweep
  lock `vault:lock:wallet_ops:<wallet>` are in Redis (60 s TTL), so they hold across tasks. They
  are separate keys, and both are released when the request ends.
- **The risk.** Two transfers from one EVM sender, a few seconds apart, can land on two
  different tasks. The second task then has only the endpoints' pending nonce to go by.
- **Hence:** **one `api` task until F5** shares the floor through Redis. A rolling deploy
  overlaps two tasks for a moment; stop-then-start would avoid that at the price of a short
  outage (D1).

### 5.5 Database and Redis

- **Connections.** `DB_MAX_OPEN_CONNS` defaults to 100 per process. The sum over every task,
  scheduled tasks included, must stay below the RDS `max_connections` with room for operators.
  Start with api 20, worker 20, poller 5.
- **RDS TLS.** `DB_SSLMODE` defaults to `disable`, and RDS PostgreSQL 15+ enforces TLS by
  default (`rds.force_ssl`, AWS). Set `DB_SSLMODE=require`. `verify-full` would need a CA file
  path the config does not expose.
- **Redis client.**
  - It takes one host and a DB index (`REDIS_HOST`/`PORT`/`PASSWORD`/`DB`, `config/database.go`).
    That means ElastiCache with **cluster mode disabled**. Serverless and cluster mode enabled
    do not fit.
  - The config has no TLS, although `goravel/redis` reads `database.redis.<connection>.tls` as
    a `*tls.Config` (`client.go`). In-transit encryption and an AUTH token therefore need F3,
    or the owner accepts plaintext inside the VPC (D5).
- **Redis memory policy:** use `noeviction`, plus a memory alarm.
  - Redis holds the locks, the rate limits, the settings cache, the scanner's address set and
    checkpoint copy, the pending blocks copy and the RPC health.
  - An evicted lock silently lets a second writer in. A full cache under `noeviction` fails the
    writes, so a lock refuses (`cacheguard.Acquire` → `ErrUnavailable`) instead of being lost.
- **Pending blocks.**
  - They live in Redis and in an append-only file under `DEPOSIT_PENDING_DIR` (`buildPendingDepositStore`).
  - On Fargate that file is task-local and lost when the task is replaced, so Redis becomes the
    only durable copy.
  - Either mount EFS on the worker for `DEPOSIT_PENDING_DIR`, or accept Redis with a replica and
    snapshots (D6). The dev host sets the directory explicitly because its daemon has no HOME
    (`docs/AWS_DEV_STACK.md`).
- **Balance refresh.** A pass over `W` wallets takes at least `W × LOCAL_BALANCE_REFRESH_SPACING_MS`.
  At 500 ms, 120 wallets already fill the 60 s interval. The ticker skips the runs it misses,
  so a large wallet count stretches the refresh rather than stacking runs.

## 6. Secrets and custody

**Share B layout (unchanged):**

- Each wallet has one secret, `vault/wallet/<wallet-id>/share-b` (`wallet.Service`,
  `app/services/wallet/service.go`).
- It is created with `KmsKeyId = MASTER_KEY_REF` when that names a KMS key
  (`secretsmanager.KMSKeyID`: an ARN, an `alias/…`, or a key id).
- Its ARN is stored in `wallets.mpc_secret_arn`. The value never enters the database
  (CLAUDE.md §6).
- Share A is on the wallet row, encrypted with the customer's passphrase.

**KMS:** one symmetric key per environment, as `MasterKey` in `template.dev.yaml`:

- yearly rotation, `DeletionPolicy: Retain`, alias `alias/waas-<env>-master`;
- `MASTER_KEY_REF` is its ARN (output `MasterKeyArn`);
- the key policy allows use only to the task role that writes and reads shares, and to a
  break-glass role;
- CloudTrail records every `GetSecretValue` on `vault/wallet/*` as a read management event
  (AWS behaviour): keep read events in the trail for the audit.

**Application secrets** are one Secrets Manager secret per environment, e.g. `waas/<env>/app`
(JSON). ECS injects them through the task definition's `secrets` (`valueFrom`
`<arn>:<json-key>::`), never as plain environment. The keys:

- `APP_KEY` (32 bytes, never rotated in place: it seals TOTP secrets, RPC URLs and ingest
  signing secrets at rest; `.env.prod.example`), `JWT_SECRET`. It also seals the signed bytes a
  withdrawal row keeps until its send is answered (`signed_tx` in `transactions.raw_payload`):
  after a rotation they no longer open, and a same-key retry of such a row records an unknown
  outcome that the tracker's proofs settle (nothing is signed again);
- `DB_PASSWORD`, `REDIS_PASSWORD`;
- mail credentials;
- the provider and price keys: `TRON_API_KEY`, `ALCHEMY_AUTH_TOKEN`, `HELIUS_API_KEY`,
  `QUICKNODE_API_KEY`, `ETHERSCAN_API_KEY`, `COINGECKO_API_KEY`, `COINMARKETCAP_API_KEY`,
  `COINAPI_KEY`, and the `<PREFIX>_FALLBACK_RPC_API_KEY` values.

RPC URLs live sealed in `chains.rpc_url` and `chain_rpc_endpoints`, not in the environment;
an `env:NAME` reference must resolve in every process that uses it (CLAUDE.md "RPC endpoints").

**IAM:** no IAM user and no access key. `CreateServerUser=false`: the user in
`template.dev.yaml` exists only for a host outside AWS. Each service gets a task role with
least privilege. `AWS_PROFILE` and `AWS_ENDPOINT_URL` stay empty, so the SDK takes the task
role (`vault.aws.*`).

| Role | Grants |
|---|---|
| `api` task role | `secretsmanager:CreateSecret`, `GetSecretValue`, `DescribeSecret`, `TagResource` on `vault/wallet/*` (as `WalletSecrets` in `template.dev.yaml`; **no `DeleteSecret`**: losing a share loses the wallet); `kms:Encrypt`, `Decrypt`, `ReEncrypt*`, `GenerateDataKey*`, `DescribeKey` on the master key; `sqs:SendMessage` on the webhook queue |
| `worker` task role | `sqs:SendMessage` on the webhook queue. **No share access:** signing needs the customer's passphrase, so it happens only in requests (`withdraw.Service.Request`, `sweep` consolidate) |
| `webhook-poller` task role | `sqs:ReceiveMessage`, `DeleteMessage`, `ChangeMessageVisibility`, `GetQueueAttributes` on the webhook queue |
| execution role (all) | ECR pull, CloudWatch Logs, `secretsmanager:GetSecretValue` on `waas/<env>/app` only |
| operator role (break-glass) | ECS Exec, or a one-off task, for operator artisan commands (`scan:deposits`, `withdrawals:reconcile`, `chains:*`), with MFA and CloudTrail. A command that reads share B (`secrets:migrate-shares`, `wallets:export-keys`) needs the `api` role and is a custody action: logged and reviewed |

## 7. Networking

- **Topology.** A VPC over two AZs:
  - public subnets: the ALB and the NAT gateways;
  - private subnets: the tasks, RDS (Multi-AZ) and ElastiCache (primary and replica in two AZs).
- **Security groups:**
  - ALB → `api` on `PORT`;
  - `api`, `worker`, `poller` and the scheduled tasks → RDS 5432 and Redis 6379;
  - nothing inbound to `worker` or `poller`;
  - RDS and Redis accept only those groups;
  - no public IP on any task.
- **Egress** through NAT: RPC providers, chain APIs, webhook receivers, mail and price APIs.
  - The AWS APIs (Secrets Manager, KMS, SQS, ECR, CloudWatch Logs) go either through NAT or
    through interface VPC endpoints, with the S3 gateway endpoint for ECR layers.
  - Endpoints keep custody calls off the Internet path and cut NAT data, but cost a fixed amount
    per AZ (§12, D7).
- **The ALB:**
  - serves HTTPS with an ACM certificate;
  - redirects HTTP to HTTPS;
  - keeps its idle timeout above `HTTP_REQUEST_TIMEOUT`;
  - uses a deregistration delay of about 30 s (the default 300 s only slows deploys);
  - serves the provider ingest routes `/v1/webhooks/ingest/{provider}/{chainID}` like any other.
- **CloudFront or WAF in front (D13)** changes who the peer is: `TRUSTED_PROXIES` must then
  cover that hop too.
- **Other settings:** `CORS_ALLOWED_ORIGINS` names the dashboard origin, and `APP_URL` the
  public base URL.

## 8. Deploy and rollback

- **Image (prerequisite, F6):** one arm64 image per commit to ECR, tagged with the git sha,
  immutable tags (`make ecr-push` exists). The `Dockerfile` needs these fixes first:
  - the builder is `golang:1.22-alpine` while `go.mod` says `go 1.25.0` (official images set
    `GOTOOLCHAIN=local`, so the build stops);
  - the runtime stage copies only the amd64 binary;
  - `HEALTHCHECK` targets 8080 whatever `PORT` is;
  - `-X main.Version` sets a variable that does not exist (`/health` reports the constant
    `0.1.0` of `app/http/resources/health`);
  - the base image is `alpine:latest`, unpinned.
- **Order of one release:**
  1. Build and push the image.
  2. Run `artisan migrate` as a one-off task with the new image, and stop on failure.
  3. Roll the `worker` (stop, then start).
  4. Roll `api` and `webhook-poller`.

  Migrations are forward-only in practice (CLAUDE.md §8: never edit an applied one). The
  previous release must keep running on the new schema (expand, then contract): both run during
  a rolling deploy, and a code rollback then never needs a schema rollback.
- **Rollback:**
  - the ECS deployment circuit breaker rolls back a release whose tasks fail their health checks;
  - otherwise, `update-service` to the previous task definition revision;
  - a configuration or secret change is a new revision too, rolled back the same way;
  - `APP_KEY` is never part of a rollback: it seals data at rest.
- **Restarts after operator commands.** The registry reads the chains, tokens, finality and RPC
  lists at boot (CLAUDE.md "RPC endpoints", "Testnet tokens"). After a `chains:*` command, force
  a new deployment of all three services.
- **IaC:** a new stack (D9) for the VPC, ALB, ECS cluster and services, RDS, ElastiCache, KMS
  key, queues (same names and settings as `template.dev.yaml`) and roles.
  - `make deploy ENV=staging|prod` then points `TEMPLATE_FILE` at it, keeping `check-account`
    and `validate`.
  - `template.yaml` is not deployed beside it (§5.2). It is retired, or kept only for the
    reconciler Lambda (D3).

## 9. Health checks and alarms

**`GET /health`** (`routes/docs.go`, `app/http/resources/health/health_resource.go`) answers
200 with:

- `status`: always `ok`;
- `version`: constant;
- `deposit_scanner`:
  - `status`: `ok`, `pending_blocks` or `pending_store_unavailable`;
  - `pending`: blocks waiting per chain, and `pending_total`;
  - `error`;
- `confirmation_tracker`:
  - `status`: `ok`, `stale_transactions` or `transactions_store_unavailable`;
  - `stale`: unsettled outbound transfers per chain, and `stale_total`;
  - `stale_after_hours` (`WITHDRAWAL_STALE_AFTER_HOURS`);
  - `error`.

The pending read has a 2 s timeout, and the stale count is cached for one minute
(`pendingHealthTimeout`, `staleOutboundCacheFor`). The HTTP status is 200 even when Postgres
or Redis is down, so `/health` is a liveness check, not a readiness check (F7).

**Health checks:**

- **ALB target group** (`api`): `/health`, matcher 200, interval 15 s, timeout 6 s (two
  2-second reads), 2 to become healthy, 3 to become unhealthy.
- **`worker` container health check:** `curl -fsS http://127.0.0.1:${PORT}/health` (the image
  has `curl`). The task serves HTTP but is in no target group.
- **`webhook-poller`:** no port. ECS restarts it when it exits. The poller exits 1 without
  `WEBHOOK_QUEUE_URL` or when its run fails. Watch it through the queue alarms below.

**Alarms** (the target; the observability work owns the metrics, and the webhook DLQ alarm is
being added to the templates separately):

| Signal | Alarm |
|---|---|
| ECS `RunningTaskCount` per service | below desired for 5 min; `worker` ≠ 1 |
| ALB | `HTTPCode_Target_5XX_Count`, `UnHealthyHostCount` > 0, p99 `TargetResponseTime` near `HTTP_REQUEST_TIMEOUT` |
| SQS webhook queue | `ApproximateAgeOfOldestMessage` > 300 s (the poller is stopped or behind); DLQ `ApproximateNumberOfMessagesVisible` > 0 |
| `/health` (synthetic probe or a scheduled task) | `deposit_scanner.pending_total` > 0 for 15 min; `confirmation_tracker.stale_total` > 0; any `*_unavailable` status |
| Log metric filters | `local deposit scan failed`, `deposit scan checkpoint is ahead of the chain head` (`ErrCheckpointAheadOfHead`), `rpc endpoint keeps failing`, `outbound transaction still unsettled`, `mpc pre-params generation failed`, `deposit scan lock not released` |
| RDS | CPU, `FreeStorageSpace`, `DatabaseConnections` near the §5.5 budget |
| ElastiCache | `DatabaseMemoryUsagePercentage` > 70 % (`noeviction`), `Evictions` > 0, `CurrConnections` |
| KMS and Secrets Manager | throttling (`ThrottlingException` in CloudTrail or the SDK errors) |

One limit on what the records tell apart: the RPC health record `vault:rpc-health:<chain>` stores
the writer's `LAMBDA_MODE`, which is empty for both `api` and `worker`.

## 10. Option B: EC2 behind an ALB

The dev host's layout, moved into AWS. Graviton instances run in an Auto Scaling group in
private subnets behind the same ALB, RDS and ElastiCache. Each instance runs the processes of
`docs/AWS_DEV_STACK.md` under systemd or supervisor:

- the API with the local workers;
- the `webhook_poller`.

How it is run:

- an instance profile replaces the IAM user and `make server-credentials`;
- deploys go through SSM Run Command or CodeDeploy, the way they are done on Forge today: a
  release directory, the `current` link, SIGTERM to both daemons;
- `DEPOSIT_PENDING_DIR` is on EBS, durable across restarts.

| | Option A: Fargate | Option B: EC2 |
|---|---|---|
| Change from today | new runtime (containers, task roles) | smallest: same daemons and runbook |
| Single writer | a service with desired count 1 and stop-then-start, declared in the stack | one instance carries the workers: two instance roles, or one instance |
| HA | tasks in two AZs, replaced in about a minute | an ASG of 1 replaces an instance in minutes; two instances need the role split anyway |
| Deploy and rollback | rolling, circuit breaker, previous revision | scripts we own (release dirs, health gates) |
| Patching | AWS patches the platform | OS patching, AMI baking, agents |
| Custody surface | ECS Exec, audited, per task | SSH or SSM sessions on a host whose role reads share B |
| Pending-blocks file | task-local (EFS or Redis only, D6) | EBS, kept across restarts |
| Cost at low load | roughly equal (§12); no idle host | one always-on instance; cheaper with a Savings Plan |

Pick B only if the owner wants the smallest change now and accepts operating hosts. The single
writer constraints of §5 apply the same way.

## 11. Recommendation and follow-ups

**Option A.** It makes the single-writer rules declarative: one service runs the scans, and
the poller is the only queue consumer. It removes the static AWS key, keeps share access on
the `api` role alone, and gives rolling deploys with automatic rollback. All of it with the
binary as it is. Lambda is not needed. It remains an option for `webhook_worker` (instead of the
poller) and for the daily reconciler.

**Code follow-ups**, small and each with tests:

| # | Change | Why | Gate |
|---|---|---|---|
| F1 | Local worker roles: `api` runs only the pre-parameters pool; `worker` runs `RunConfirmationCheck` (every chain and type) instead of the withdrawal-only check | Removes the `86400` workaround. Confirmations then no longer depend on a chain's scan succeeding (§2 gap) | production |
| F2 | `artisan webhooks:reconcile` (the `webhook_reconciler` body) | Lets an ECS scheduled task replace the Lambda | only if D3 keeps it outside Lambda |
| F3 | Redis TLS: `REDIS_TLS` → `database.redis.default.tls` | ElastiCache in-transit encryption and AUTH | D5 |
| F4 | Scan lock with an owner token (compare-and-delete release) | A lapsed lock is not deleted by its former holder (§5.1) | production |
| F5 | EVM nonce floor shared through Redis | More than one `api` task (§5.4) | scaling `api` |
| F6 | `Dockerfile`: Go 1.25 builder, arm64 binary, `HEALTHCHECK` on `$PORT`, pinned base, version stamped where `/health` reads it | The image cannot be built today (§8) | production |
| F7 | `/health` readiness (DB and Redis reachable) and pool level | A readiness signal for the ALB and the alarms (coordinate with the observability work) | production |

## 12. Cost notes

Monthly list prices in us-east-1, on demand, 730 h, as known when this was written. These are
orders of magnitude for comparing the options; check them in the AWS Pricing Calculator before
deciding.

| Item | Size | ≈ per month |
|---|---|---|
| Fargate arm64 `api` | 2 vCPU / 4 GB × 1 (× 2 after F5) | $58 ($116) |
| Fargate arm64 `worker` | 1 vCPU / 2 GB | $29 |
| Fargate arm64 `webhook-poller` | 0.25 vCPU / 0.5 GB | $7 |
| ALB | 1, low traffic | $22 |
| NAT gateway | one per AZ × 2, plus $0.045/GB | $66 + data |
| Interface VPC endpoints (D7) | 6 services × 2 AZs | $88 + data |
| RDS PostgreSQL | db.t4g.medium Multi-AZ, plus gp3 storage | $95 + storage |
| ElastiCache Redis | cache.t4g.small, primary + replica | $47 |
| Secrets Manager | **$0.40 per secret, one per wallet**, plus $0.05 per 10k calls | 1,000 wallets: $400; 10,000: $4,000 |
| KMS | one key, plus $0.03 per 10k requests | $1 + requests |
| CloudWatch Logs | $0.50/GB ingested (`LOG_LEVEL=info`) | by volume |
| Option B instead of Fargate | t4g.large (2 vCPU / 8 GB) or c7g.large, plus EBS | $50–55 per instance |

What drives the cost:

- **Share B, at scale.** One secret per wallet becomes the largest line past a few hundred
  wallets. Changing the layout is a custody change (CLAUDE.md §6: share B never in the
  database), so it is listed as decision D15, not proposed here.
- **Fixed network cost.** Below that, NAT and endpoints cost more than compute. A single NAT
  gateway halves the NAT line, but loses egress when its AZ fails (D7).

## 13. Migration path from the dev setup

The dev host stays as it is: it is the whitelabel's test environment. Production is a new
environment, not a move of dev.

1. **This change:** the design, a template that validates, the template guards.
2. **Code:** F1, F4, F6 and F7, plus F3 if D5 asks for TLS. Each one merges with its tests. A
   change to `mpc`, `wallet`, `withdraw` or `sweep` runs the testnet e2e (CLAUDE.md §6).
3. **Stack (D9):** the ECS stack and `make deploy ENV=staging`. The account follows D10; a
   separate account for production keeps the custody keys apart.
4. **Staging (testnet):**
   1. Bring up the stack with `CHAIN_NETWORK_PROFILE=testnet`, a new KMS key, an empty RDS
      database, and `artisan migrate` as a task.
   2. Run `chains:add-missing`, then `chains:align-network --apply` and
      `chains:align-tokens --apply` (dry runs first).
   3. Set keyed RPC endpoints (`chains:rpc-endpoints`, `docs/RPC_PROVIDERS.md`). The dev host
      shows that public endpoints rate-limit `eth_getLogs` and `getBlock`.
   4. Set the worker's `LOCAL_DEPOSIT_SCAN_CHAINS` to every active chain, and force a new
      deployment.
   5. Run the testnet e2e (`scripts/e2e`, `tools/e2e-funder`). Measure `T_gen` (D4).
   6. Prove the webhooks to a test receiver; `make dlq-check` stays at 0.
5. **Production (mainnet):** the same steps, with the mainnet profile and its own key, database
   and API tokens. Integrators (Markets, the whitelabel back) move to the new base URL with new
   tokens. No wallet moves from dev, which holds testnet wallets only.
6. **If wallets ever move between environments or accounts**, follow the dev cutover runbook
   (`docs/AWS_DEV_STACK.md`):
   - freeze wallet data;
   - audit the process environment;
   - `secrets:migrate-shares` (dry run, then `--apply`; copies are read back before a row
     moves);
   - drain the outbox before switching the webhook consumer, so only one consumer runs at a time;
   - run `scan:deposits <chain> --retry-pending` until no pending block is left, because the
     task-local pending file does not move.

## 14. Decisions for the owner

| # | Decision | Recommendation |
|---|---|---|
| D1 | Option A (Fargate) or B (EC2); one `api` task until F5, or accept the nonce-floor risk of §5.4 | A, one task until F5 |
| D2 | Webhook consumer: the poller service or the `webhook_worker` Lambda (exactly one) | Poller: one runtime, already proven on dev |
| D3 | Webhook reconciler: is provider ingest used in production? If not, drop it. If yes: Lambda (as now) or a scheduled task after F2 | Decide from ingest use |
| D4 | `MPC_PREPARAMS_POOL_SIZE` and the `api` task size, after measuring `T_gen` in staging | Start with 2 vCPU and a pool of 6 (3 wallets in a burst); adjust |
| D5 | Redis TLS and AUTH (F3), or plaintext inside the VPC | TLS |
| D6 | Pending-blocks durability: EFS for `DEPOSIT_PENDING_DIR`, or Redis only (replica + snapshots) | EFS on the worker |
| D7 | VPC endpoints for the AWS APIs, or NAT only; one NAT or one per AZ | Endpoints for Secrets Manager and KMS at least; NAT per AZ in production |
| D8 | `WithdrawalQueue`: delete it (both templates) or design async withdrawals | Delete when convenient: nothing produces to it |
| D9 | IaC for the new stack: CloudFormation (consistent with `make deploy` and `check-account`), CDK or Terraform | CloudFormation |
| D10 | AWS account and region for production | A separate account |
| D11 | Multi-AZ RDS and ElastiCache in staging too, or only in production | Production only |
| D12 | Cadence of `price:check-update` and `activity:prune`; whether `price:websocket` runs | Decide with the price owner |
| D13 | CloudFront or WAF in front of the ALB | WAF on the ALB: no extra hop, so `TRUSTED_PROXIES` stays the ALB subnets |
| D14 | Whether dev ever leaves Forge for the same stack | Not needed for production |
| D15 | Share B storage cost at scale (one secret per wallet, §12) | Revisit before 1,000 wallets |
