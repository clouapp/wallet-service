# Macro Wallets — Backend (WaaS)

Multi-chain Wallet-as-a-Service API: module `github.com/macrowallets/waas`, Go 1.25,
[Goravel](https://goravel.dev) v1.17 (Gin HTTP driver, Postgres, Redis), AWS Lambda in
production. The front end (`macro-wallets-front`, port 2001) is a separate repository.

This file holds the **invariants of the system**: the things that must stay true whatever
the code looks like, each with the test that guards it. The **shape of the code** (layers,
handlers, services, repositories, errors, tests) lives in [`.ai/guidelines/`](.ai/guidelines/README.md).
If a rule and a test disagree, the test is the rule — fix the prose.

An invariant marked **UNGUARDED** has no test yet; one marked **KNOWN VIOLATION** is broken
today and has a fix tracked elsewhere. Do not read either as permission.

## Invariants

### 1. One binary, one composition

- The same binary serves every process; `vault.lambda_mode` (`LAMBDA_MODE`) picks the
  handler in `main.go`: empty → local Goravel HTTP server (+ `localworkers`), `api`,
  `deposit_scanner`, `confirmation_tracker`, `webhook_reconciler`, `webhook_worker`, and
  `webhook_poller` (a long-running process that stands in for the `webhook_worker` Lambda
  on a host without Lambda, see `.ai/guidelines/queues-and-workers.md`).
  Deploy (SAM `template.yaml`, EventBridge, the Lambda modes) is out of scope for any
  reorganization: `main.go` only has to keep compiling. — guarded by `go build ./...`.
- Goravel is assembled in `bootstrap/app.go` (`foundation.Setup()…Create()`): migrations,
  providers, seeders (`WithSeeders(seeders.All)`, from `database/seeders`), jobs, commands,
  rules, config, the global middleware chain, routing and the Gate callback. There is no
  `WithEvents` (the app has no events or listeners). The lists live in the skeleton's helper
  files (`bootstrap/migrations.go`, `providers.go`, `commands.go`, `jobs.go`, `rules.go`),
  which `artisan make:migration|provider|command|job|rule` append to. Config is code in
  `config/*.go`, read from env through `config/env.go`. — UNGUARDED; known exception:
  `app/models/chain_rpc_url.go` resolves `${ENV}` placeholders in RPC URLs with
  `os.LookupEnv`.
- Boot refuses an empty `JWT_SECRET`, an `APP_KEY` that is not 32 bytes
  (`config.ValidateSecrets`, called from `bootstrap.checkBootConfig`) and an unparsable
  `TRUSTED_PROXIES`, except that `artisan key:generate` and `artisan jwt:secret`, which
  create the first two, skip the secret check. A `JWT_SECRET` under 32 characters only
  warns. — guarded by `config/boot_check_test.go`.
- The cache has no `cache.prefix` (`config/cache.go` does not set it), so the framework
  builds every key as `"" + ":" + key`: every cache key starts with `:` (locks, counters,
  rate limits, settings,). Leave it: setting a prefix renames every key and
  resets the locks and counters in flight.
- Each service provider binds its own services under their type
  (`app.Singleton((*T)(nil), …)`), and each singleton is built once, on first
  resolution, by resolving its dependencies the same way. Everything else gets its
  dependencies through its constructor: `container.Make`/`MustMake` (the typed
  `facades.App().Make`) appear only in the composition root — `bootstrap/`,
  `routes/`, `app/providers/` and `main.go`. There is no container struct (see
  `.ai/guidelines/controllers-and-services.md`). — guarded by
  `TestComposition_Root_ResolvesEveryTypedBindingToOneInstance`
  (`bootstrap/composition_root_test.go`) and
  `TestNo_Service_LocatorOutsideTheCompositionRoot` (`tests/architecture`). `app/container`
  holds only the typed `Make[T]`/`MustMake[T]` helpers.

### 2. Layers point down

`routes → http (controllers, middleware, requests) → services → repositories → models`,
with adapters to external systems beside the repositories and `app/models` importing
nothing of the module. — guarded by `tests/architecture` (`TestImports_Import_Direction`,
`TestLayer_Covers_EveryZoneOfTheRepository` and the checks beside them). With
`ARCH_MODE` unset the checks stay in report mode: they log findings against
`tests/architecture/testdata/baseline/` and do not fail. `make test-unit` (and so
`make test`) sets `ARCH_MODE=ratchet`, so a violation absent from that baseline fails
and a known one does not. `ARCH_MODE=enforce` fails on every violation and is not the
default. A check moves to enforce when its baseline is empty. The checks read source
and the route table; they do not need a database.
Known violations include `app/models → app/services/mpc` and `config → app/models`.

### 3. Two surfaces, never mixed

| Surface | Prefix | Caller | Route file |
|---|---|---|---|
| dashboard | `/v1` | a `User` with a session | `routes/admin.go` |
| external API | `/api/v1` | an API token bound to ONE account | `routes/api.go` |
| inbound provider webhooks | `/v1/webhooks/ingest/{provider}/{chainID}` | a chain-data provider, authenticated by its signing secret | `routes/webhooks.go` |
| public | `/health`, `/swagger/*` | anyone | `routes/docs.go` |

- Every route outside the public, guest (`/v1/auth/*` session creation) and
  inbound-webhook rows is behind exactly one auth middleware: `middleware.SessionAuth()`
  on `/v1`, `middleware.APITokenAuth()` on `/api/v1`. — guarded by
  `tests/architecture/routesecurity` (`TestEvery_Route_IsInTheRouteTable`: closed table of
  every served route and its guard, both directions;
  `TestGuard_Chain_MatchesRegistration`: the ordered guard chain on that row,
  checked against the middleware the route files register, failing on any
  unlisted route or a chain that does not match; and
  `TestAuthenticated_Routes_RefuseAnAnonymousCaller`). The baseline is empty, so
  ratchet fails on any mismatch. A new route needs a row in `routeTable`.
- External integrators (Markets) consume `/api/v1`; the front consumes `/v1`. A change of
  status, error shape or success shape on either is a contract change, decided first and
  recorded in `.ai/guidelines/http-error-contract.md`. — guarded by `TestContract_HTTP_Contract`
  (`tests/contract`): a fixed scenario over both surfaces (success and error paths, no
  chain call, no fund movement) whose method, path, status, content type and raw body
  (uuids, timestamps and tokens normalized) must match `tests/contract/testdata/http_contract.txt`
  byte for byte. Rewrite it (`make contract-update`) only for a decided change.

### 4. Authentication

- **Session (dashboard and platform):** Goravel JWT guard (`config/auth.go`, `config/jwt.go`),
  refresh token rotation in `refresh_tokens`, optional TOTP second factor. A login with TOTP
  on answers a `challenge_token` (not a session JWT; it is spent on use and void after a
  revocation); the TOTP secret is stored sealed (`enc:v1:`) and opened before the check; a
  user must be `active` and not suspended for `SessionAuth` to admit the token. — guarded by
  `tests/feature/api/dashboard/auth` (`TestTwo_Factor_LoginSuite`, `TestAccess_Status_Suite`,
  `TestSession_Revocation_Suite`) and `tests/feature/api/dashboard/users` (`TestMfa_Seal_Suite`).
- **Ending a session:** logout, password change or reset, TOTP disable and a platform revoke all go
  through `auth.SessionRevoker.RevokeAll`: it moves `users.sessions_revoked_at` to the start of the
  next second and deletes the refresh tokens. `SessionAuth` refuses a token issued before the
  watermark (401 `session revoked`) and a new sign-in waits until the watermark has passed.
  **Logout therefore ends every session of the user**, on every device, immediately (the access
  tokens of the other devices stop working at once instead of living up to 15 minutes), and writes
  `user.sessions_revoked` to the activity log. — guarded by `TestSession_Revocation_Suite`
  and `app/services/auth/session_revocation_test.go`.
- **Rate limits:** `middleware.Throttle` on `/v1/auth/{login,register,2fa/verify,refresh,recover,
  recover/confirm,invites/accept}`, on the whole `/api/v1` group and on `gas-check`; over the limit is
  429 `too_many_requests` with `Retry-After`. Keys use `middleware.ClientIP`, which trusts
  `X-Forwarded-For` only from `TRUSTED_PROXIES`; the limits are `THROTTLE_*` (attempts per minute,
  `0` turns one off) and the counters live in the Cache (Redis); a cache failure lets the request
  through. Limiters and defaults: `.ai/guidelines/http-error-contract.md`. — guarded by
  `app/http/middleware/throttle_test.go` and the contract steps 69 and 70.
- **API token (external):** a JWT minted by `apitoken.Service.Mint` (claims and signing in
  `apitoken.Sign`; test fixtures sign through `middleware.MintAPIToken`) (`sub: api_token`,
  `jti` = `access_tokens` row, `account_id` claim). The row must exist and be active. When
  the token carries `require_signature`, an `X-Signature` HMAC over the body is mandatory
  and checked before the body is read. — guarded by `TestAPI_Token_AuthHMACSuite`
  (`app/http/middleware/api_token_auth_test.go`) and `TestCritical_Endpoints_Suite`
  (`tests/feature/api/external/critical/critical_api_endpoints_test.go`).
  `permissions` is a JSON array of the token catalog. `APIScope` enforces it on
  the routes that catalog names; a blank store keeps the previous access.
  `ip_cidr` is enforced against `ClientIP`; a blank allowlist keeps the previous
  access. `spending_limit` is a per-token daily USD cap enforced in
  `withdraw.Service`; a blank cap leaves withdrawal behavior unchanged. A newly
  minted token stores `sha256` of the random secret. — guarded by
  `TestAPI_Token_IP`, `app/http/middleware/api_scope_test.go`, and
  `app/policies/api_token_permissions_test.go`.
  API tokens are account-owned. Suspending a member leaves the tokens that
  member minted. The list shows `created_by`. An owner or admin with
  `tokens.write` revokes one token. Removing the member deletes the tokens
  that member minted for that account. — guarded by
  `TestSuspend_Keeps_MintedTokensAndBlocksMembership` and
  `TestRemove_Revokes_TokensCreatedByTheMember`
  (`tests/feature/api/dashboard/accounts/account_members_test.go`).

### 5. Scope: actor → account → wallet

- An API token acts only inside its own account. A wallet of another account answers
  **404** on the external API (the id is never confirmed). — guarded by
  `TestAPI_Wallet_ContextSuite` (`app/http/middleware/api_wallet_context_test.go`).
- On the dashboard the account comes from `X-Account-Id` (`AccountHeader`) or
  `{accountId}` (`AccountContext`) and must match a membership of the user; the wallet
  (`WalletContext`) must be reachable through the account membership or a `wallet_users`
  row. — UNGUARDED.
- The account or wallet id comes from the path, the header or the token — never from the
  body. — UNGUARDED.
- An account is `test` or `prod` (`accounts.environment`); a test account touches only
  testnet chains and a prod account only mainnet chains (`requestctx.AccountEnvironment`
  and the chains service). — UNGUARDED.
- A frozen or archived account refuses every mutation with 403 `account_frozen`
  (`middleware` account status check, `policies.AccountAllowsRequest`); reads stay allowed.
  — guarded by the `*_gate_test.go` suites in `tests/feature/api/dashboard/accounts`.
- The changes feed of `GET /api/v1/transactions` (`updated_since` / `cursor`) reads only the
  token's account, like the offset list; a cursor is a position, never a grant, so another
  account's cursor reads the caller's own rows. — guarded by
  `TestChanges_StaysInsideTheTokenAccount` (`tests/feature/api/external/transactions`) and
  `TestList_ChangedForAccount_StaysInsideTheAccount` (`tests/feature/repositories`).
- **KNOWN VIOLATION** S3: some mutating wallet handlers (withdraw, consolidate, generate
  address, activate wallet…) check membership only, not a permission on the dashboard. The
  permission of each handler is a product decision (`.ai/guidelines/authorization.md`).

### 6. Custody

- A wallet is a **2-of-2 MPC** wallet. Share A is the customer's, encrypted with the
  customer's passphrase (Argon2id → AES-GCM) on the wallet row; share B is the service's,
  in **AWS Secrets Manager** (LocalStack locally), referenced by `MPCSecretARN` — its
  value is never in the database. — guarded by `app/services/mpc/*_test.go`
  (`TestEncrypt_Decrypt_RoundTrip`, `TestDecrypt_Wrong_Passphrase`, keygen and signing tests)
  and `TestValidate_Existing_SeedWalletSecret*` (`database/seeders`).
- Share B is created sealed with the KMS key `MASTER_KEY_REF` names (`KmsKeyId`); a local
  reference leaves Secrets Manager on its default key. `app/services/sharemigration`
  (`secrets:migrate-shares`) is a custody service: it moves share B between two Secrets
  Managers, holds the bytes only in memory, zeroes them, never writes the source, and moves
  `MPCSecretARN` with a compare-and-swap only after the copy reads back equal. — guarded by
  `app/adapters/secretsmanager/wallet_store_test.go`, `app/services/sharemigration/service_test.go`
  and `TestWallet_Repository_Suite` (`TestSetMPCSecretARN_*`).
- Key material (shares, passphrases, derived keys) is never returned after the one-time
  recovery material at creation, never logged, queued or cached. — guarded by
  `TestWallet_Recovery_MaterialSuite` (`tests/feature/api/external/wallets/wallets_recovery_material_test.go`).
- secp256k1 keygen pre-parameters (the Paillier safe primes that become part of both
  shares) may come from `mpc.PreParamsPool` (`MPC_PREPARAMS_POOL_SIZE`, default 0; filled
  by the local workers): kept in process memory only, never persisted or logged, each set
  used by one party of one keygen. With no running pool a keygen searches its own inside
  the request (minutes on a two-core host). — guarded by
  `app/services/mpc/preparams_pool_test.go` and `TestKeygen_Secp256k1_UsesThePooledPreParams`.
- Signing order (build → policy checks → fetch share B → combine → sign → broadcast →
  persist) is not changed "in passing". Any change to `mpc`, `wallet`, `withdraw` or
  `sweep` runs the testnet e2e (`scripts/e2e`, `tools/e2e-funder`: SOL, BTC, ETH, POL).
- `facades.Crypt()` (`APP_KEY`) seals TOTP secrets, RPC URLs and ingest signing secrets at
  rest; it is not the MPC share envelope. One write format, `settings.Seal` (`enc:v1:` +
  the Crypt envelope); `settings.OpenStored` also opens the bare envelope that older
  `chains.rpc_url` rows hold. — guarded by `app/services/settings/seal_legacy_test.go`.
  **KNOWN VIOLATION** S11: `webhook_configs.secret` is plain text.

### 7. Queues and workers

| Mechanism | Carries | Consumer |
|---|---|---|
| Goravel queue, `sync` connection (`QUEUE_CONNECTION`, default `sync`; `config/queue.go`) | credential mail (`jobs.SendCredentialMailJob`, always `DispatchSync`) | runs in the dispatching process. The `database` connection (queue `blockchain`) is configured but nothing runs `queue:work` and no job uses it |
| AWS SQS (`app/adapters/queue/sqs`, port `queue.Sender`) | outbound webhook delivery | `webhook_worker` Lambda, or on a host without Lambda the `webhook_poller` process (same handler) |
| `app/services/localworkers` | local stand-in for `confirmation_tracker` and `webhook_worker` (with a webhook queue, the outbox re-dispatch instead of local delivery), the wallet balance refresh loop (and optional deposit scan, with the deposit event backfill of the scanned chains), and the MPC keygen pre-parameters pool | started from `main.go` in local mode only |

- Local webhook delivery from the outbox runs only when no SQS webhook queue is
  configured, so a message is never drained twice. — guarded by
  `TestStart_Skips_OutboxWhenAQueueDelivers` (`app/services/localworkers`) and
  `TestOne_Consumer_PerQueue` (`tests/architecture`).
- Webhook delivery is at least once. An event is a `webhook_events` row (one per config, type
  and subject: the partial unique index of migration 260) written before the queue send, and
  receivers deduplicate on `X-Vault-Delivery-Id` (the row id), which every resend keeps. Three
  repairs keep a row from being lost:
  - **Deposit events.** The tracker persists a deposit's status, then publishes; a failed publish
    is only logged, and the client credits a deposit only on `deposit.confirmed`. The
    `confirmation_tracker` (every chain) and the local confirmation loop (the locally scanned
    chains) run `deposit.Service.BackfillDepositEvents`: a deposit confirmed between 48 h and
    1 min ago is republished when an active config that subscribes to `deposit.confirmed`, may
    see the wallet and was created no later than the confirmation has no row for (config,
    `deposit.confirmed`, deposit id). The event carries `OccurredAt` = the confirmation, so a
    config created later is left out, and the unique index keeps reruns idempotent; at most 50
    deposits per run. `deposit.pending` and `deposit.confirming` are not backfilled. — guarded
    by `TestBackfill_DepositEvents_*`, `TestRun_ConfirmationCheck_RepairsADepositConfirmedWhosePublishFailed`
    (`app/services/deposit`) and `TestFind_ConfirmedDepositsMissingEvent_*` (`tests/feature/repositories`).
  - **Queue mode.** An outbox row whose SQS send failed, or whose message left the queue without
    a recorded attempt, stays `pending`. `webhook.Service.RedispatchStale` claims pending rows of
    active configs untouched for 5 minutes (`FOR UPDATE SKIP LOCKED`, 50 per run), counts the
    claim as one attempt (so a row is sent at most `max_attempts` times) and sends them again. It
    runs from the `confirmation_tracker` Lambda when `vault.queues.webhook` is set and from the
    local workers when a queue is configured (`RedispatchOutbox`), never beside the local outbox.
    — guarded by `TestRedispatch_Stale_*`, `TestWebhookEventRepository_ClaimStaleForRedispatch_*`
    and `TestStart_Redispatches_TheOutboxWhenAQueueDelivers`.
  - **On request.** `POST /api/v1/webhooks/{webhookId}/redeliver` (`webhooks.write`) requeues
    `event_ids`, or the events created from `since` to `until` of `types`, of one webhook the
    token's account owns: only events of the last 7 days, at most 500 per request, an event
    pending and queued in the last minute left alone. — guarded by
    `tests/feature/api/external/webhooks` (`TestRedeliver_Webhook_*`) and `TestRedeliver_*`
    (`app/services/webhook`).
- Deposit events: `deposit.pending`, `deposit.confirmed` and `deposit.failed` are sent once per
  deposit, with the transaction id as subject (the key the backfill looks up).
  `deposit.confirming` reports progress and may arrive several times, with increasing
  `confirmations`. A tracker or scan pass that leaves a deposit unconfirmed sends it when the
  deposit moves from pending to confirming, and again when its count rose above the stored one
  while the stored count was still below `required_confirmations`. That is at most one per pass,
  carrying the count of that pass (counts crossed between two passes are not sent one by one).
  None is sent once the count has reached `required_confirmations`: under `confirmations` the
  deposit is confirmed there, and under a native finality a final block that cannot be read
  would otherwise send one per pass while the count keeps rising. Its subject is
  `<transaction id>:<confirmations>` (at most 56 of the 64 characters of `subject_id`), so each
  count is its own row and delivery id, and a count a config already got is not sent to it again;
  the payload keeps the plain `transaction_id`. Receivers treat `confirmations` as monotonic
  (deliveries may arrive out of order). — guarded by
  `TestUpdate_Confirmations_PublishesDepositConfirmingEachTimeTheCountRises`,
  `TestUpdate_Confirmations_ACountAlreadySentIsNotSentAgain`,
  `TestFinality_StalledFinality_StopsTheConfirmingProgressAtTheRequiredCount`,
  `TestBackfill_DepositEvents_RepairsALostConfirmationBesideItsConfirmingRows`,
  `TestRedeliver_Requeues_EachConfirmingProgressRowUnderItsOwnID` (`app/services/deposit`) and
  `TestPublish_Confirming_*` (`app/services/depositevents`).
- `webhook_poller` replaces the `webhook_worker` Lambda where none runs; the two never consume
  one queue together. — **UNGUARDED**: `TestOne_Consumer_PerQueue` reads the consumers from
  `template.yaml` and cannot see where the poller runs; this rests on the deployment.
- No job payload carries a credential (tokens, passphrases, shares): a credential mail job
  carries `(subject_id, purpose)` and mints the token when it runs. — guarded by
  `app/jobs/send_credential_mail_test.go` and `TestJob_Payload_IsDecodedNotPositional`.
- The app registers no events or listeners; add an event only together with a listener.
- The deposit scan checkpoint of each chain lives in Postgres (`deposit_scan_checkpoints`)
  and never moves back; Redis (`vault:checkpoint:<chain>`) is a copy written after the
  database. A read falls back from Redis to the database on a miss or an error. Only a chain
  the database does not hold, with Redis answering a clean miss (or not configured), starts
  at the head; a Redis error with no row, or a database error, skips the cycle and moves
  nothing. — guarded by `app/services/deposit/checkpoint_test.go` and
  `TestDeposit_Scan_CheckpointRepositorySuite`.
- One scanner per chain: a `ScanLatestBlocks` cycle holds `vault:lock:deposit_scan:<chain>`
  (`cacheguard.Acquire`, TTL 2 min, above the 60 s `deposit_scanner` Lambda timeout). A held
  lock skips the cycle; a lock store that is down or missing fails it. `scan:deposits
  --block|--tx|--pending|--retry-pending` take no lock and never move the checkpoint. —
  guarded by `app/services/deposit/scan_lock_test.go`.
- A block is never marked scanned while its deposits are not all read and recorded: it is
  retried, then kept in the pending-blocks store. On every EVM chain a failed `eth_getLogs`
  fails the block. — guarded by `app/services/deposit/failure_test.go`,
  `TestScan_Block_FailsTheBlockWhenGetLogsFailsOnEveryEVMChain` and
  `TestRegister_ActiveChains_EVMBlockWhoseLogsFailGoesPending`.

### 8. Database and migrations

- Schema changes are Goravel migrations in `database/migrations/`, numbered
  `000000000NNNN0_<name>.go`, one migration per file, registered in the literal list of
  `Migrations()` in `bootstrap/migrations.go`. A migration never edits an applied one; it adds
  the next number. — guarded by the migration tests in `tests/migrations` (e.g.
  `non_negative_amounts_test.go`).
- A NEW migration uses the schema builder, `migrationSchema()` from
  `database/migrations/orm.go` (`Create`, `Table`, `DropIfExists`), unless it needs a Postgres
  feature the builder does not express (CHECK or partial-unique constraints, `USING` casts,
  enum types, data backfills): then it runs raw SQL. The applied migrations keep their raw
  SQL; do not rewrite them. A migration imports no app code (`app/models`, `app/services`,
  `pkg/*`, `app/facades`): copy what it needs into the migration, so a later change there
  cannot change what a fresh database gets. The database layer may not import `app/facades`
  (`TestImports_Import_Direction`), so `orm.go` is the one file that reaches the framework
  facades. `artisan make:migration` creates the file and appends it to `Migrations()`; replace
  the facades import it generates with `migrationSchema()`.
- Amounts are base-unit integers/strings (`pkg/amount`, `numeric` columns), never floats,
  and never negative. — guarded by `TestEnforce_Non_NegativeAmounts*`.
- Every write to a `transactions` row moves `updated_at` (the ORM stamps it on each
  `TransactionRepository` update: status, confirmations, failure, fee, block), and
  `transactions_updated_at_id_index` covers `(updated_at, id)`: the changes feed of
  `GET /api/v1/transactions` reads that order and returns a row again after each change.
  `updated_at` is `TIMESTAMP(0)` (whole seconds, UTC). — guarded by `TestUpdates_Move_UpdatedAt`
  (`tests/feature/repositories`) and
  `TestTransactions_UpdatedAtIndex_CoversTheChangesFeedAndBackfillsNullStamps` (`tests/migrations`).
- Seed data is test-only (`database/seeders`, registered by `WithSeeders(seeders.All)`;
  `docs/DEV_SEED_DATA.md`). Only `DatabaseSeeder` is registered, so `db:seed --seeder=ChainSeeder`
  is not available. The chain catalog that
  `chains:add-missing` and `chains:align-tokens` write in production is not seed data: it lives in
  `app/repositories/chaincatalog`.

### 9. Tests never touch live data and never share state

- The destructive suite migrates fresh only a dedicated `*_test` database
  (`TEST_DB_DATABASE`, default `vault_unit_test`); `vault` (dev) and `vault_test` (local
  e2e, holds MPC shares that cannot be recreated) are refused. — guarded by
  `tests/feature/support/testenv/environment_test.go`
  (`TestValidate_Configuration_RejectsProtectedDatabases`,
  `TestApply_Database_OverrideToE2EDatabaseIsRefused`).
- Tests use Redis index `REDIS_DB` (15) of `.env.testing`, never the live index 0, and
  never `FLUSHALL`/`FLUSHDB`; keys are namespaced per test. — guarded by
  `TestValidate_Redis_DatabaseRefusesTheLiveIndexAndBadInput`,
  `TestTesting_Environment_FileUsesANonLiveRedisIndex`.
- Every test starts from empty tables: `fixtures.TestDB(t)` (`tests/feature/support/fixtures`)
  truncates every table but `migrations` before and after the test, and also deletes the settings
  cache keys (`*settings:*`, in the worker's Redis index) because the settings service caches
  groups under `settings:platform:<group>` and `settings:account:<uuid>:<group>`, which would
  outlive the truncated rows and leak a value into the next test. A test that changes the schema
  uses `fixtures.TestDBFreshSchema(t)`. — guarded by `tests/feature/support/fixtures/reset_test.go`.

### 10. Settings

- Account settings are the registry in `app/services/settings`. Account-managed
  groups are `account_security` and `account_webhooks`. `account_sweep_limits`
  is platform-managed and inherited. The platform groups (`groups_platform.go`) are
  `deposit_scan`, `webhook_delivery`, `sweep_limits`, the mail groups (`mail_smtp`, `mail_delivery`,
  `mail_ses`, `mail_mailgun`, `mail_resend`, `mail_postmark`), the price groups (`price_lookup`,
  `price_coingecko`, `price_coinmarketcap`, `price_coinapi`) and the provider groups
  (`provider_alchemy`, `provider_helius`, `provider_quicknode`, `provider_etherscan`). A secret
  (a signing secret, an API key, a password) is written sealed and redacted on read
  (`<field>Set` tells whether one is stored).
- `GET /v1/accounts/{accountId}/settings` is `settings.view` (owner, admin,
  auditor). `PATCH /v1/accounts/{accountId}/settings/{group}` is
  `settings.update` (owner, admin). The user role holds neither. The service
  asks `policies.MayViewSettings` and `policies.MayUpdateSettings`. — guarded by
  `TestSettings_Permissions_FollowTheAccountRoles`.
- `POST /v1/accounts/{accountId}/settings/sections/{section}/reset` deletes
  the stored rows of every account-managed group on the page and writes
  `settings.section_reset` with the group and the field names.
- `POST /v1/accounts/{accountId}/settings/sections/{section}/cache` is
  `FlushSection`. It drops `settings:account:<uuid>:<group>` for every
  account-managed group on that page so an out-of-band edit is read next
  time. Stored rows stay, and no activity row is written. An unknown page,
  including a platform-only page, is 404 before the role check. A page that
  holds a platform-managed group is 403 and the cache is left in place.

### 11. Feature flags

- The catalog is `app/services/features`: `api-request-signature-required`,
  `deposit-scan-enabled`, `sweep-enabled`, `user-2fa-required`,
  `wallet-creation-enabled`, `webhook-delivery-enabled`, `withdrawals-enabled`.
  Each flag has an account row and a global row. A missing row is the catalog
  default and is not inserted. Nothing is cached, so the next read sees a write.
- Flags are platform-controlled rollout and kill switches. Customer-controlled
  toggles are account settings. "Require 2FA for my members" is
  `account_security.require_2fa`. "2FA enforcement rolled out to this account"
  is the `user-2fa-required` flag.
- Account flags reuse the settings permissions: list is `settings.view`, write
  is `settings.update`. An unknown key on write is 404 before the permission
  check. `GET|PATCH /v1/platform/features` is a platform admin
  (`platform_admins`), not an account owner; the whole `/v1/platform` group sits
  behind `middleware.PlatformAdmin`. — guarded by
  `app/services/features/service_test.go` and `gate_test.go`.
- `withdrawals-enabled`, `sweep-enabled`, `deposit-scan-enabled`,
  `wallet-creation-enabled`, and `webhook-delivery-enabled` default to on.
  A missing `deposit-scan-enabled` row allows scans. `Gate` pauses
  withdrawals and sweep when the account flag is off, and when the global
  row is an explicit false. A missing global row does not pause. The conflict
  codes are `withdrawals_paused` and `sweep_paused`. Wallet creation and
  webhook delivery stay ungated.
- `user-2fa-required` and `account_security.require_2fa` are the two switches
  `TOTPEnrollment` reads on account and wallet routes.
- `GET /v1/users/me` adds `features`: the globally active flag keys, in
  catalog order. A missing global row uses the catalog default, so
  `deposit-scan-enabled`, `sweep-enabled`, `wallet-creation-enabled`,
  `webhook-delivery-enabled`, and `withdrawals-enabled` are present until a
  platform row stores false. Account rows are not included.
  Login and `PATCH /v1/users/me`
  do not carry the field.

### 12. RBAC

- One role per membership: owner (3) > admin (2) > auditor = user (1). Unknown
  roles fail closed. `MayGrant` and `MayActOn` allow an equal or lower rank and
  refuse a higher one. Auditor and user share a rank and do not manage members.
  The ladder lives in `app/policies/member_rank.go`. — guarded by the policy
  tests beside it.
- `models.AccountRoleOutranks` may be called only from `app/policies`. The
  function is not declared on this branch. — guarded by
  `TestOnly_Policies_CallAccountRoleOutranks` (`tests/architecture`).
- The platform pivot tables `model_has_roles`, `role_has_permissions`, and
  `model_has_permissions` are not on this branch. Only `app/policies` may read
  them. A migration may create them. — guarded by
  `TestOnly_Policies_ReadRBACPivots`.
- Dashboard permissions decided on this branch: `tokens.read` / `tokens.write`
  on `/v1/accounts/{accountId}/tokens`, `settings.view` / `settings.update`,
  `activity.read` on `GET /v1/accounts/{accountId}/activity` (owner, admin,
  auditor). External token permissions are the catalog in
  `app/policies/api_token_permissions.go`, checked by `APIScope`. Minting
  refuses a permission the creator does not hold.
- `GET /v1/platform/activity` is a platform admin, the same gate as platform
  flags. There is no `audit.view` permission row on this branch.
- Invites (`account_invites`), user suspension (`users.suspended_at`) and the session
  watermark (`users.sessions_revoked_at`) are on this branch: see §4 and
  `.ai/guidelines/identity-and-scope.md`.

## Running it

```bash
cp .env.dev.example .env.dev
make dev            # Docker + backend (Air) + frontend
```

| Command | What it does |
|---|---|
| `make dev` / `make dev-back` / `make dev-front` / `make run` | full stack / backend with live reload / frontend / backend without |
| `make stop` | kill the dev processes |
| `make test` | `test-unit` then `test-integration` (both always run; fails if either fails) |
| `make test-unit` | packages whose tests need no PostgreSQL/Redis, all in parallel, no lock |
| `make test-integration` | packages whose tests import `tests/feature/support/testenv`, `tests/feature/support/testutil`, `tests` or `bootstrap`: under `~/.local/state/macro-e2e/locks/vault_unit_test.lock`, migrates `TEST_DB_DATABASE` (default `vault_unit_test`) fresh once as a template, each test binary clones it into `vault_unit_test_pN` with its own Redis index (`REDIS_DB` − N), `-p TEST_PARALLEL` (default 4, max 6); clones dropped on exit. Only names starting with `vault_unit_test` are accepted (`vault`, `vault_test` refused) |
| `make test-race` | the same suite with `-race`; architecture checks stay in report mode unless `ARCH_MODE` is set |
| `make lint` | golangci-lint v2 with `.golangci.yml` (report mode: lists findings, exits 0) |
| `make arch` | architecture checks with every finding listed (`ARCH_MODE=ratchet\|enforce` to block) |
| `make contract` / `make contract-update` | compare / rewrite the HTTP contract snapshot |
| `make docker-up` / `make docker-down` | Postgres, Redis, Mailpit, LocalStack |
| `make migrate` / `make migrate-status` / `make migrate-rollback` / `make migrate-fresh` | migrations |
| `make db-seed` / `make migrate-fresh-seed` | dev seed data |
| `make key-generate` / `make jwt-secret` | `APP_KEY` / `JWT_SECRET` in `.env.dev` |
| `make e2e-tools` | build the `macro-e2e` helper into `~/.local/state/macro-e2e/bin/` |
| `make localstack-hooks` | build the LocalStack `secrets-snapshot` binary (also run by `docker-up`/`dev`) |
| `make swagger-generate` | regenerate `docs/` (Swagger UI at `/swagger/index.html`) |

Docker (compose project `macro-wallets`, prefix `waas-`): `waas-postgres`, `waas-redis`,
`waas-mailpit`, `waas-localstack`; host ports come from `.env.dev` (`DB_PORT`, `REDIS_PORT`,
`MAILPIT_SMTP_PORT`, `MAILPIT_UI_PORT`, `LOCALSTACK_PORT`). Local mail uses Mailpit
(`MAIL_MAILER=smtp`, SMTP on port 1025) or `MAIL_MAILER=log`. The log driver is refused
when `APP_ENV` is production. LocalStack community has no native persistence: hooks in
`docker/localstack/` keep Secrets Manager (MPC share B) in encrypted, ARN-preserving
snapshots in the `localstack_data` volume; the snapshot key lives in
`~/.config/macro-wallets/localstack-seed/`, never in the repository.

## Where things are

| Path | Holds |
|---|---|
| `main.go` | boot, Lambda mode switch, local server |
| `bootstrap/` | Goravel setup (`app.go`) and the lists it takes: providers, migrations, commands, jobs, rules |
| `config/` | config as code, read from env |
| `routes/` | `admin.go` (`/v1`, `/v1/platform`), `api.go` (`/api/v1`), `webhooks.go` (ingest), `docs.go` (health, Swagger), `platform_refusals.go` |
| `app/http/` | `controllers/{dashboard,external,platform,ingest,health}/<feature>/` plus the handlers both surfaces share, `middleware` (+ `requestctx`), `requests` (FormRequests), `responses` (failure writers), `resources` (wire shapes), `pagination` |
| `app/services/` | business logic, one package per subject (`wallet`, `withdraw`, `sweep`, `account`, `auth`, `settings`, `features`, …); `chain`, `mpc`, `ingest` also hold chain/provider code that has not moved to `app/adapters` yet |
| `app/adapters/` | clients of outside systems: `chain/{evm,bitcoin,solana,tron,xrp,rpc}`, `ingest`, `price`, `blockheight`, `secretsmanager`, `queue/sqs`, `redis`, `webhook` |
| `app/repositories/`, `app/models/` | persistence (`internal/db` is the query seam) and schema types |
| `app/policies/`, `app/providers/`, `app/facades/`, `app/container/` | the one arbiter of who may do what (+ Gate abilities); service providers, each binding the services of its domain; typed re-exports of the framework facades; typed `Make`/`MustMake` |
| `app/console/`, `app/jobs/`, `app/mails/`, `app/rules/` | artisan commands, queue jobs, mail, validation rules (there is no `app/events/`, `app/listeners/` or `app/dtos/`) |
| `database/` | `migrations/` and `seeders/` (dev seed logic, `DatabaseSeeder`) |
| `pkg/`, `packages/` | `pkg/`: `amount`, `numeric`, `types`, `httpclient`, `pgerr`, `lifecycle`, `mpcshare`, `e2evault`. `packages/activitylog`: Goravel package (own ServiceProvider, listed in `bootstrap/providers.go`) |
| `tests/` | `feature/api/<surface>/<feature>` (HTTP suites), `feature/repositories`, `feature/support` (`HTTPSuite`, `fixtures`, `testenv`, `testutil`, `testdb`), `mocks`, `architecture` (machine-checked rules), `contract` (HTTP contract snapshot), `migrations` |
| `docs/` | Swagger output (`docs.go`, `swagger.json`, `swagger.yaml`, generated), `DEV_SEED_DATA.md`, and design notes (`GORAVEL_INTEGRATION.md`, `INTEGRATION_STATUS.md`, `alignment-decisions.md`, `superpowers/` are historical) |

Everything inside a `.go` file is English.

## E2E helpers (`tools/macro-e2e`)

Standalone binary (no Goravel boot: it targets `vault_test` through `~/.local/state/macro-e2e/wallets-api.environ`, never `.env`). Run from `back/` (`go run ./tools/macro-e2e ...`) or use the prebuilt `~/.local/state/macro-e2e/bin/macro-e2e` (`make e2e-tools`; the Markets recordings call it).

| Command | What it does |
|---------|--------------|
| `capture-env [--dry-run]` | Rebuild `wallets-api.environ` (0600, NUL-separated) from `.env.dev` + e2e overrides (vault_test, testnet, scan chains `sol,eth,btc,polygon,base,arbitrum,bsc,tron,ltc`, BTC testnet4, TRON Nile, Litecoin testnet); prints key names only. `--dry-run` writes nothing and compares with the current environ (differing key names + sha256 digest, never values) |
| `fee-estimate WALLET ASSET TO [--amount DECIMAL]` | Read-only `GET /api/v1/wallets/{id}/fee-estimate` with the Markets token (kept in memory); exit 1 unless HTTP 200 |
| `api [--build] [--status] [--no-start]` | Build/stop/start the e2e API binary; takes `locks/wallets-api-restart.lock` itself (do not wrap it in `flock`), refuses during a recording; stop = SIGTERM, 20 s grace, then SIGKILL |
| `preflight withdrawal WALLET ASSET BASE_UNITS TO` / `preflight consolidation WALLET ASSET` | Off-chain plan + MPC sign + verify; nothing broadcast |
| `send-from-base TAG WALLET ASSET BASE_UNITS DECIMALS TO EXT_USER [--chain C] [--recording-lock-held-by OWNER] [--apply]` | One guarded funding transfer (ledger, vault_test, claim, recording lock, pre-flight, UUIDv5 idempotency); dry run without `--apply` |
| `consolidate TAG WALLET ASSET [--chain C] [--apply]` | One guarded child-address sweep, same guards (`--chain` names the ledger chain of a token, e.g. USDT on `tron`) |
| `ledger-reconcile TAG... [--apply]` | Read-only: per-leg sweep tx hashes from vault_test (SELECT) checked on chain (TRON Nile, litecoinspace testnet, EVM receipt via the environ RPC); `--apply` (recording lock + `locks/funding-ledger.lock`) backs up `ledger.json.pre-reconcile-<UTC>` (0600) and writes `hash`, `sweeps[].tx_hash`, `status: confirmed`, `confirmedAt`; `consolidate --apply` now polls vault_test for the same hashes |

## Architecture

Single Go binary, multiple Lambda modes via `LAMBDA_MODE` env:

| Mode | Trigger |
|------|---------|
| **local** (default) | Goravel HTTP server |
| `api` | API Gateway (Lambda) |
| `deposit_scanner` | EventBridge cron |
| `confirmation_tracker` | EventBridge cron |
| `webhook_reconciler` | EventBridge cron |
| `webhook_worker` | SQS queue |

Locally, it runs as a standard Goravel HTTP server (`facades.Route().Run()`).
In production, the same binary is deployed to Lambda with `LAMBDA_MODE` selecting the handler.

Local mode shuts down gracefully (`pkg/lifecycle`): SIGTERM/SIGINT stops the HTTP server (in-flight requests drain) and the local scanner/refresher loops within `SHUTDOWN_TIMEOUT_SECONDS` (default 15, keep it below the 20 s grace of `macro-e2e api`); exit 0 when clean, 2 when the deadline expires, 1 on a second signal.

## Key export (`artisan wallets:export-keys`)

Reconstructs each selected wallet's private keys from share A (DB) + share B (Secrets Manager), checks them against every address, and writes them with both shares and an `INSTRUCOES.md` into a WinZip AES-256 zip (0600, default `~/.local/state/macro-wallets/exports/`, paths inside the repo refused).

```bash
go run . artisan wallets:export-keys --wallet <UUID> [--wallet <UUID>] [--out FILE.zip] [--passphrase-vault]
go run . artisan wallets:export-keys --all      # every wallet; never the default
```

Passwords are typed on the terminal only (zip password twice, ≥16 chars); never flags/env. `APP_ENV=production` needs `--allow-production` plus a typed confirmation. Open the zip with 7-Zip/WinZip (not Info-ZIP `unzip`). Solana genesis keys are raw scalars (not importable in Phantom); child keys are.

## Tests

```bash
make test                        # unit + integration
make test-unit                   # no database, seconds
make test-integration TEST_PARALLEL=6 TEST_FLAGS=-v
make test-integration TEST_FLAGS='-run TestWalletRepository'
```

- Unit vs integration is decided per package from its test imports (`go list`), no build tags: a package is integration when its tests import `tests/feature/support/testenv`, `tests/feature/support/testutil`, `tests` or `bootstrap`.
- `fixtures.TestDB(t)` (`tests/feature/support/fixtures`) migrates the schema fresh once per test binary (a worker clone already is), then truncates every table but `migrations` before and after each test and deletes the settings cache keys (`*settings:*`) that survive the rows. Tests that run migrations up/down use `fixtures.TestDBFreshSchema(t)` (fresh schema before and after). HTTP suites embed `support.HTTPSuite`; unreachable endpoints use `testutil.ClosedLocalURL`.
- `go run ./tests/feature/support/testdb prepare | drop-clones` are the template/cleanup steps `make test-integration` runs; both refuse names outside `vault_unit_test*`.
- Running `go test ./app/repositories` directly still works on `vault_unit_test` itself (no clone; take the lock with `flock ~/.local/state/macro-e2e/locks/vault_unit_test.lock …` when another run may be active).
- `TEST_TIMEOUT` (default 30m) is a safety net per `go test` invocation.
- Timings on the dev machine: `make test` ~2.5 min (was ~35 min with `-p 1` and `migrate:fresh` twice per test); `test-unit` ~1.5 min (mostly `app/services/mpc` keygen), `test-integration` ~40 s with 6 workers, ~1.7 min with `TEST_PARALLEL=1`.
- Unreachable endpoints in tests use a just-released local port (`testutil.ClosedLocalURL`), not port 1: on WSL2 `127.0.0.1:1` hangs until the client timeout instead of refusing.
- A worktree that runs the suite next to another run sets its own `TEST_DB_DATABASE` (`vault_unit_test_<name>`) and `TEST_DB_LOCK`, so the template databases and locks do not collide.

## Migrations

Migrations use Goravel's Schema builder. Files live in `database/migrations/`.

```bash
make migrate            # Run pending migrations
make migrate-status     # Show status
make migrate-rollback   # Rollback last batch
make migrate-fresh      # Drop all + re-migrate
make db-seed            # artisan db:seed
make migrate-fresh-seed # artisan migrate:fresh --seed
```

To create a new migration, run `go run . artisan make:migration <name>` (or add a file in `database/migrations/` implementing `Signature()`, `Up()`, and `Down()`) and register it in the literal list of `Migrations()` in `bootstrap/migrations.go` (see invariant 8).

See `docs/GORAVEL_INTEGRATION.md` for the full schema builder reference.

## API Routes

Two auth schemes, in `routes/admin.go` and `routes/api.go`:

- **Dashboard** (`/v1/*`) — JWT session auth via `middleware.SessionAuth`; `/v1/platform/*` additionally behind `middleware.PlatformAdmin`
- **External API** (`/api/v1/*`) — Bearer token auth via `middleware.APITokenAuth`

Swagger UI: http://localhost:2002/swagger/index.html. `docs/` is generated: `make swagger-install` (pins `swag@v1.8.12`) then `make swagger-generate`; regenerate in the change that edits an annotation (`.ai/guidelines/http-layer.md`, "Swagger").

## Key Env Vars

```bash
PORT=2002
APP_KEY=               # required at boot: 32 bytes (make key-generate); seals TOTP secrets, RPC URLs, signing secrets
JWT_SECRET=            # required at boot, >= 32 chars advised (make jwt-secret)
DB_HOST=localhost
DB_PORT=5432
DB_DATABASE=vault
DB_USERNAME=vault
DB_PASSWORD=vault
REDIS_HOST=localhost   # REDIS_HOST/PORT/PASSWORD/DB is the only Redis config (no REDIS_URL)
REDIS_PORT=6379
# REDIS_PASSWORD=
# REDIS_DB=0           # tests use index 15 (.env.testing)
TRUSTED_PROXIES=127.0.0.1/32,::1/128   # peers whose X-Forwarded-For is believed; set it to the proxy/LB of the deployment
THROTTLE_LOGIN_PER_IP_EMAIL=10   # per minute; 0 turns the limit off
THROTTLE_LOGIN_PER_IP=60
THROTTLE_RECOVER_PER_IP_EMAIL=5
THROTTLE_AUTH_PER_IP=60
THROTTLE_API_PER_MINUTE=600      # /api/v1, per API token
ETH_RPC_URL=https://eth-sepolia.public.blastapi.io
POLYGON_RPC_URL=https://rpc-amoy.polygon.technology
SOLANA_RPC_URL=https://api.devnet.solana.com
BTC_RPC_URL=https://blockstream.info/testnet/api
API_KEY_SECRET=dev-secret-key-not-for-production
```

Redis is the cache driver (rate limits, locks including the per-chain deposit scan lock, settings
cache, the deposit scanner's watched addresses and its copy of the scan checkpoint, which lives in
Postgres): the app needs it reachable. `.env.dev.example` / `.env.example` carry the full lists,
with the throttle variables.

### Bitcoin-family provider failover

BTC, TBTC, LTC and TLTC take fallback providers after `<PREFIX>_RPC_URL`:

```bash
LTC_FALLBACK_RPC_URL=                 # empty: the network's built-in list; "none": no fallback
LTC_FALLBACK_RPC_API_KEY=             # optional Tatum key (x-api-key, *.tatum.io hosts only)
LTC_TATUM_DATA_API_URL=               # optional, default https://api.tatum.io (used only with a key)
```

- Comma-separated, tried in order: an Esplora REST API, a bitcoind JSON-RPC node (`https://`), ElectrumX 1.4 (`electrum+ssl://`, pinned with `cert_sha256` when self-signed, verified by the system roots without it; `electrum+tcp://` loopback only), or a Tatum gateway (`https://*.tatum.io`). Each fallback's genesis block is checked once; a wrong network is refused.
- Built-in lists (`app/services/chain/bitcoin_fallback_defaults.go`, chosen by the network the record resolves to, so `ltc` on the testnet profile gets the testnet list):
  - Litecoin mainnet: ElectrumX `electrum-ltc.bysh.me:50002`, `backup.electrum-ltc.org:443`, `electrum1.cipig.net:20063` (Let's Encrypt, no pin), `electrum.ltc.xurious.com:50002`, then `https://litecoin-mainnet.gateway.tatum.io`.
  - Litecoin testnet: ElectrumX `electrum-ltc.bysh.me:51002`, `electrum.ltc.xurious.com:51002`, then `https://litecoin-testnet.gateway.tatum.io` (also what `macro-e2e capture-env` writes).
  - Bitcoin: none; set `BTC_FALLBACK_RPC_URL` / `TBTC_FALLBACK_RPC_URL` (e.g. `https://bitcoin-mainnet.gateway.tatum.io`) and the same key / Data API vars to use them.
- Pin rotation: a rotated self-signed certificate is refused ("does not match the pin") and the next provider serves. Re-read it with `openssl s_client -connect HOST:PORT -servername HOST </dev/null | openssl x509 -outform DER | sha256sum`, check `server.features` reports the network's genesis, then update the constant (or override the list by env).
- Reads (UTXOs, balance, tip, block scan, tx status, fee rate, paid fee) move to the next provider on transport errors, 5xx or rate limits; a provider failing 3 times in a row is skipped for 30 s, doubling up to 5 min. A JSON-RPC "method not found" (-32601) is "unsupported", not a failure.
- Broadcast sends the same signed bytes (never re-signed) to the primary, then to the next provider only when it was not accepted (transport error, 5xx, rate limit). A definite rejection stops there; "already known" counts as success with the locally computed txid.
- ElectrumX cannot list a block's transactions (block scans go to Tatum or the primary). Tatum keyless: 5 requests/min, tip, block scan, tx status, fee, paid fee and broadcast, no UTXOs or balance. With `<PREFIX>_FALLBACK_RPC_API_KEY`: the plan's limit (5 req/s on the free plan) on the gateway, and UTXOs/balance from the Data API `GET /v4/data/utxos` (100 credits per call; chains bitcoin, bitcoin-testnet, litecoin, litecoin-testnet; none for BTC testnet4). Every listed UTXO is checked with the gateway's `gettxout` (unspent, ≥ 1 confirmation, same value and address) before it is spent. The key is never logged and is redacted from error bodies (Tatum echoes it in its 401).
- Bitcoin-family JSON-RPC answers may be up to 32 MiB (a verbose `getblock` is ~7× the block size).

### RPC response limits

One node answer is read up to a per-chain limit and refused past it with an explicit `response larger than N bytes` error (never truncated): EVM 64 MiB (`evmRPCMaxResponseBytes`; a busy Base Sepolia block with full transactions is ~4.3 MB), Solana 32 MiB (`solanaRPCMaxResponseBytes`), Bitcoin family 32 MiB, TRON HTTP 64 MiB, the XRP `ledger` scan 32 MiB (`xrpLedgerMaxResponseBytes`; the other XRP calls keep 1 MiB). Other JSON-RPC clients (chain probes, `evmcall`) keep the 1 MiB default.

### Paid fees

The confirmation tracker stores the fee actually paid (native base units) in `transactions.fee` when a withdrawal, sweep or gas_seed confirms: EVM gasUsed × effectiveGasPrice + L1 fee, BTC/LTC inputs − outputs, Solana `meta.fee`, TRON `fee` (energy + bandwidth burned), XRP `Fee` (drops) of the transaction in a validated ledger. An XRP transaction is confirmed only when validated with `meta.TransactionResult` `tesSUCCESS`. XRP: a validated `tec…` result marks the transaction and its withdrawal `failed` with `onchain_<result in lowercase>` (e.g. `onchain_tecno_dst_insuf_xrp`), stores the fee it still burned, and sends `withdrawal.failed` once; a payment past its `LastLedgerSequence` (kept in `transactions.raw_payload`) that the node searched every ledger for (`tx` `min_ledger`/`max_ledger` → `searched_all`, inside `complete_ledgers`) is failed with `expired`, no fee. EVM: the block of an outbound row is read from its receipt (`eth_getTransactionReceipt`); a receipt with status `0x0` (mined out of gas or reverted: fee paid, value not moved) marks the row (withdrawal, sweep or gas_seed), and a withdrawal's `withdrawals` row, `failed` with `onchain_out_of_gas` (gasUsed reached the gas limit) or `onchain_reverted`, stores the fee it burned, and sends `withdrawal.failed` once; a failed sweep sends no `sweep.confirmed`. Only rows still at block 0 are read this way: a row whose block was recorded before this check (2026-10-18) is not re-read. TRON and Solana report an on-chain failure as an error (the row stays pending, never confirmed); other chains never mark an on-chain failure. The deposit scanner does not read receipts: a native EVM transfer mined with status `0x0` to a watched address is still recorded as a deposit (**KNOWN VIOLATION**, see "EVM native gas"). Older rows: `go run . artisan transactions:backfill-fees [--chain a,b] [--apply]` (dry run by default, idempotent, reads only).

### Confirmation and finality

The confirmation tracker (and the confirmation step of each scan cycle) confirms a deposit, withdrawal, sweep or gas_seed by the finality policy the row was recorded under: `transactions.finality`, copied from `chains.finality` when the scanner, the inbound ingest or the sweep executor writes the row, like `required_confs`, so changing a chain never re-reads an open transaction against a count it was not recorded with.

- `confirmations` (the default): confirmed when `tip − block + 1 >= required_confs` (the tip block counts one).
- `finalized_tag` (EVM only): confirmed when the transaction has at least 1 confirmation and its block is at or below the block `eth_getBlockByNumber("finalized")` returns.
- `solidified` (TRON only): the same against the solidified block (`/walletsolidity/getblock`, falling back to `getnowblock`). The TRON RPC URL must serve `/walletsolidity` (TronGrid does; a java-tron full node serves it on its solidity port).

`confirmations` keeps counting for display under every policy. The final block is read at most once per chain per pass; a read that fails (no finalized block, node error, an adapter that cannot read the policy) confirms nothing that pass, is logged and is retried on the next one: the count never stands in for it. Defaults (migration 640 and the seeds): `polygon`, `tpolygon`, `bsc`, `tbsc` → `finalized_tag`; `tron`, `ttron` → `solidified`; every other chain `confirmations`. Under a native policy `required_confirmations` is the finality lag plus the tip block measured on 2026-10-11 (Amoy finalized 1–2 blocks behind the tip: 3; BSC testnet 1: 2; Nile solidified 18: 19), kept for "n/N confirmations" displays only; `finality` is on `GET /api/v1/chains` and in the `deposit.*` webhook data. Ethereum, Base and Arbitrum keep counting: their finalized block trailed the tip by about 18 minutes (Sepolia 88 blocks, Base Sepolia 534, Arbitrum Sepolia 4438), slower than 12, 12 and 120 confirmations. `PATCH /v1/platform/chains/{chainId}` edits `finality` and `required_confirmations`: a policy the adapter cannot read is refused (also by the CHECK `chains_finality_fits_adapter`), and leaving `finalized_tag`/`solidified` for `confirmations` needs `required_confirmations` in the same body. The registry reads both at boot, so they apply to the transactions recorded after the next load. Neither policy re-checks that a recorded deposit is still in the block the scanner saw (a block reorganized away before it became final is not detected). — guarded by `app/services/deposit/finality_test.go` (`TestFinality_*`, `TestRun_ConfirmationCheck_ReadsEachChainFinalityOncePerPass`, `TestProcess_Transfer_RecordsTheChainFinalityPolicy`), `tests/migrations/add_finality_policy_test.go`, `app/adapters/chain/evm/finality_test.go`, `app/adapters/chain/tron/finality_test.go` and `TestUpdate_Thresholds_*` (`app/services/chains`).

### XRP deposits

`ScanBlock` on `xrp`/`txrp` reads one validated ledger (rippled `ledger`, `transactions` and `expand` true; API v1 `metaData` and API v2 `tx_json`/`meta` shapes) and reports, in ledger order (`TransactionIndex`), every `Payment` with `meta.TransactionResult` `tesSUCCESS` that delivered XRP to another account: the amount is `meta.delivered_amount` in drops (a partial payment credits what arrived, never `Amount`), `From` is `Account`, `To` is `Destination`, and `DestinationTag` is carried to `transactions.destination_tag` and to the `deposit.*` webhooks (`destination_tag`, omitted without a tag and on other chains). Not deposits: `tec…` payments, issued-currency or MPT deliveries (an object `delivered_amount`; the token registry does not model XRPL issued currencies), payments an account makes to itself, and every other type, including `CheckCash`, `EscrowFinish`, `PaymentChannelClaim` and `AccountDelete`, which can also deliver XRP. A ledger that is not validated, not closed, not the one asked for, without its transactions list, with a transaction not expanded or without metadata, or a payment whose `delivered_amount` is missing or `unavailable`, is an error, never an empty ledger, so the engine retries it and then keeps it pending. An XRP withdrawal is a `Payment` from the base address to its destination, so it is a deposit only when that destination is itself a watched address (as on every chain). The head is the validated ledger, so with 1 required confirmation a deposit is confirmed on the cycle that records it (tip − ledger + 1). A scanner runs only where one is scheduled for the chain: the local workers scan the chains in `LOCAL_DEPOSIT_SCAN_CHAINS` (`xrp`/`txrp` must be listed), the SAM `deposit_scanner` schedules (`template.yaml`) list no XRP chain, and `artisan scan:deposits` scans on demand; the first cycle starts at the head, without history. A ledger closes every 3–5 s, so the default windows (`DEPOSIT_SCAN_BATCH_BLOCKS` 50, `DEPOSIT_SCAN_CATCH_UP_BLOCKS` 500, 8 ledgers fetched at a time) are about 3–4 minutes and 30–40 minutes of ledgers; a ledger the load-balanced public servers do not have yet answers `lgrNotFound` and is retried. — guarded by `TestLive_ScanBlock_*` (`app/adapters/chain/xrp/scan_test.go`, altnet ledgers recorded in `testdata/xrpl`) and `TestXRP_Deposit_*` (`app/services/deposit/xrp_deposit_test.go`).

### Solana deposits

`ScanBlock` on `sol`/`tsol` reads one finalized slot with `getBlock` (`encoding` json,
`transactionDetails` `accounts`, `rewards` false, `maxSupportedTransactionVersion` 1): the account
keys, static and lookup-table loaded, in balance order, with the lamport and token balances before and
after each transaction, and no instructions. Devnet carries version-1 transactions since 2026-10 and
refuses (-32015 "Transaction version (1) is not supported") a block or transaction that holds one to a
request that allows only version 0; `getTransaction` (the paid fee) asks for the same version. Legacy,
v0 and v1 transactions read alike. A skipped slot (-32007, -32009) yields no transfers; every other error
fails the block, which is retried and then kept pending. Per successful transaction, in block order:

- **SOL**: every account whose lamports rose, except the fee payer, is credited from the fee payer;
  the log index is the account index. A transaction whose balances do not line up with its keys is
  logged and skipped.
- **SPL** (Token and Token-2022 accounts alike): `preTokenBalances`/`postTokenBalances` are netted per
  owner and mint; an owner whose total of a mint the chain's token registry lists rose is credited that
  increase (base units, `uiTokenAmount.amount`), with the token row (symbol, mint as contract, the row's
  decimals; a mint reporting other decimals is logged). The sender is the owner whose holding of that
  mint fell most (the fee payer when none fell, a mint). The log index is the number of account keys
  plus the credited owner's lowest token account index, so two mints or two owners in one transaction
  never share a log index with each other or with a lamport credit. Moving tokens between two accounts
  of one owner credits nothing. The deposit's `to_address` is the owner (the wallet address), never the
  token account. A registered mint that is not a public key fails every scan of the chain, and a
  registered balance without an owner, with an index past the keys or an amount that is not a base-unit
  integer fails the block, so it is retried instead of marked scanned. Unregistered mints are ignored.

The scanner records one deposit per chain and transaction (`ScannerDepositLogIndex`, the unique deposit
index): when one transaction pays watched addresses twice (two assets, or two addresses), only the first
of those transfers in that order is recorded. A public devnet endpoint allows 6 `getBlock` calls per 10 s per IP
(`x-ratelimit-method-limit: 6`, measured on 2026-10-18), about 0.6 slots per second against 2.5 produced:
it can never keep up, whatever the pace (see "Deposit scan pacing"); scan `sol` with a keyed RPC. —
guarded by `app/adapters/chain/solana/scan_spl_test.go` (devnet slots 512232469 and 512232636 recorded in
`testdata/solana`, with the -32015 answer to a version-0 request), `scan_test.go`,
`TestRegister_ActiveChains_SolanaScansTheRegisteredSPLTokens` and
`TestSolana_Deposit_RecordsAnSPLCreditWithItsSymbolAndMint`.

### Deposit scan pacing

Every scan cycle (Lambda and local) reads its window at the moment of use: `DEPOSIT_SCAN_BATCH_BLOCKS`
(default 50), `DEPOSIT_SCAN_CATCH_UP_BLOCKS` (500, while the checkpoint trails the head by more than the
batch) and `DEPOSIT_SCAN_CONCURRENCY` (8 blocks fetched at a time, at most 32), overridden by the platform
`deposit_scan` setting. Per chain, `DEPOSIT_SCAN_BATCH_BLOCKS_<CHAIN>`, `DEPOSIT_SCAN_CATCH_UP_BLOCKS_<CHAIN>`
and `DEPOSIT_SCAN_CONCURRENCY_<CHAIN>` (`vault.deposit_scan.chains.<chain>`, the chain id in capitals, e.g.
`DEPOSIT_SCAN_CONCURRENCY_SOL=1`, `DEPOSIT_SCAN_CONCURRENCY_BSC=2`) win over the global value for that
chain only; 0 or unset keeps it. A negative value, a concurrency above 32 or a catch-up below the batch is
logged and dropped field by field, and the scan runs. A chain id must be listed in `depositScanChainIDs`
(`config/vault.go`) to be overridden. The window of a cycle is local to it, so chains scanned at the same
time never see each other's values. The RPC client retries a 429 itself (6 attempts, 250 ms doubling to
8 s, or `Retry-After`); a block that still fails is retried by the engine and then kept pending. The local
workers scan `sol`/`tsol`, `base`/`tbase`, `arbitrum`/`tarbitrum`, `bsc`/`tbsc` and `tron`/`ttron` in loops
of their own, so a slow chain never holds up `eth`, `polygon` and `btc`. — guarded by
`app/services/deposit/scan_override_test.go`, `config/vault_test.go`,
`app/providers/deposit_scan_overrides_test.go` and `TestStart_Scans_FastChainsInLoopsOfTheirOwn`.

### Testnet tokens

The tokens of a primary record (`eth`, `polygon`, `sol`, `base`, `arbitrum`, `bsc`, `tron`) are the
catalog's list of the network the profile points it at (`app/repositories/chaincatalog`,
`primary_tokens.go`, `added_evm_chains.go`, `added_tron_ltc_chains.go`). A test network lists only the
issuer's own deployments, read on-chain on 2026-10-18:

| Record under `CHAIN_NETWORK_PROFILE=testnet` | Tokens |
|---|---|
| `eth` (Sepolia) | USDC `0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238` (Circle), LINK `0x779877A7B0D9E8603169DdbD7836e478b4624789` (Chainlink) |
| `polygon` (Amoy) | USDC `0x41E94Eb019C0762f9Bfcf9Fb1E58725BfB0e7582` (Circle), LINK `0x0Fd9e8d3aF1aaee056EB9e802c3A762a667b1904` (Chainlink) |
| `sol` (devnet) | USDC `4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU` (Circle), WSOL `So11111111111111111111111111111111111111112` (native mint) |
| `base` / `arbitrum` (Sepolia) | Circle's USDC |
| `bsc` (testnet) | none |
| `tron` (Nile) | USDT `TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf` (the nileex.io faucet's) |

USDT, DAI, WBTC, WETH, UNI, BONK, JUP and WIF publish no testnet deployment and are not listed on a test
network. The t-prefixed records (`teth`, `tpolygon`, `tsol`, …) keep their own lists under every profile.
`go run . artisan chains:align-tokens [--apply]` moves an existing database there: for each primary record
whose row (`network_id`, `is_testnet`) points at a test network it creates the network's missing tokens,
re-activates inactive ones and marks `inactive` the active tokens of the record's mainnet list. It never
deletes a row, reports and leaves alone a token no catalog list names, never touches a record on a mainnet
network or a t-prefixed record (a mainnet database is left as it is), prints only by default and changes
nothing on a second run. Only active tokens are registered on the adapters, read at boot: restart the API
and workers afterwards. Run it after `chains:align-network` flips a database to the testnet profile. —
guarded by `app/repositories/chaincatalog/token_alignment_test.go`, `primary_tokens_test.go` and
`app/services/chainregistry/network_tokens_test.go`.

### EVM native gas

Every native EVM transfer (withdrawal, native sweep, gas_seed, and the `tools/e2e-funder` funding transfer) encodes the node's `eth_estimateGas` of that transfer (real from, to and value) × 120 % (`evmNativeGasMarginPercent`; Arbitrum × 150 %, `evmArbitrumNativeGasMarginPercent`, because its L1 component follows the L1 price). There is no fixed 21000 on any network: since 2026-10 Sepolia prices state creation, so 0.002 ETH to an account that does not exist yet estimates 207,391 gas (21,000 to an existing one) and a transfer sent with 21000 is mined out of gas with its fee paid and its value not moved (withdrawal `0x235681c8…26cb`). Any network that adopts that pricing behaves the same way. The margin covers what can change between estimate and inclusion (a recipient whose code runs on receipt, estimators that stop short); unused gas is not charged, it only raises what the sender must hold.

- A failed estimate fails the build with `chain.ErrGasEstimateFailed` before anything is signed or broadcast, with no fallback, not even for a recipient that exists. An outage also matches `chain.ErrProviderUnavailable` / `chain.ErrRateLimited` (`chain.KnownFailure`), so it may be retried; a revert from a sender that holds the value is final.
- An amount not known yet (a native sweep sized before its fee, a gas_seed the planner sizes) is estimated with 1 wei: a positive value is what creates the recipient's account, its size does not change a plain transfer's gas. A sender that does not hold the value yet (the base of a multi_sweep before its sweeps land, an unfunded quote) is re-estimated with 1 wei from the node's default sender (the zero address, which holds burned funds on Sepolia, Amoy, BSC testnet, Base Sepolia and Arbitrum Sepolia, checked 2026-10-18; where it does not, the estimate fails closed).
- An unknown recipient (a preview or fee estimate without `to`, or the shared probe `0x1111…1111`, which anyone can fund) is sized as a fresh random address, the worst case for an externally owned account.
- The planner reserves per recipient (`NativeTransferReserveTo`): a native withdrawal keeps the fee of a transfer to its recipient, a sweep the fee of a transfer into the base address. The fee estimate endpoint, the plan's gas and the built transaction use the same estimate; a gas_seed to a fresh child is sized with the account-creation gas.
- **KNOWN VIOLATION**: `ScanBlock` reports every native transfer of a block with a value, without reading its receipt, so a native transfer mined with status `0x0` to a watched address (for example an external sender that hard-codes 21000 to a fresh deposit address) is recorded and confirmed as a deposit. ERC-20 deposits are not affected (a failed transaction emits no `Transfer` log).

— guarded by `app/adapters/chain/evm/native_gas_test.go`, `app/adapters/chain/evm/transaction_block_test.go`, `app/services/sweep/planner_native_reserve_test.go`, `app/services/deposit/evm_failed_receipt_test.go` and `tools/e2e-funder/gas_test.go`.

### TRON gas_seed

A TRC-20 sweep's gas_seed gives the child what it lacks to pay the estimated bandwidth plus the energy the child pays itself (the contract deployer's share, `consume_user_resource_percent` and its staked energy, is subtracted) + 20 % (`tronGasSeedMarginPercent`), capped at the transaction's fee_limit ceiling. Right before the sweep the executor re-prices it and sends a delta gas_seed (at most 2) when the child is still short; otherwise the sweep is not broadcast.

## Deploy

```bash
make deploy ENV=prod    # AWS Lambda via SAM
```
