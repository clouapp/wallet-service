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
  `deposit_scanner`, `confirmation_tracker`, `webhook_reconciler`, `webhook_worker`.
  Deploy (SAM `template.yaml`, EventBridge, the Lambda modes) is out of scope for any
  reorganization: `main.go` only has to keep compiling. — guarded by `go build ./...`.
- Goravel is assembled in `bootstrap/app.go` (`foundation.Setup()…Create()`): migrations,
  providers, seeders, jobs, commands, events, rules and config. Config is code in
  `config/*.go`, read from env through `config/env.go`. — UNGUARDED; known exception:
  `app/models/chain_rpc_url.go` resolves `${ENV}` placeholders in RPC URLs with
  `os.LookupEnv`.
- Boot refuses an empty `JWT_SECRET` and an `APP_KEY` that is not 32 bytes
  (`config.ValidateSecrets`, called from `bootstrap.checkBootConfig`), except for
  `artisan key:generate` and `artisan jwt:secret`, which create them. A
  `JWT_SECRET` under 32 characters only warns. — guarded by `config/boot_check_test.go`.
- Services are built once in `app/providers/vault_container.go` and reached through
  `app/container` (the "god struct", being replaced by typed bindings — see
  `.ai/guidelines/controllers-and-services.md`). — UNGUARDED.

### 2. Layers point down

`routes → http (controllers, middleware, requests) → services → repositories → models`,
with adapters to external systems beside the repositories and `app/models` importing
nothing of the module. — guarded by `tests/architecture` (`TestImportDirection`,
`TestLayerCoversEveryZoneOfTheRepository` and the checks beside them). With
`ARCH_MODE` unset the checks stay in report mode: they log findings against
`tests/architecture/testdata/baseline/` and do not fail. `make test` sets
`ARCH_MODE=ratchet`, so a violation absent from that baseline fails and a known
one does not. `ARCH_MODE=enforce` fails on every violation and is not the
default. A check moves to enforce when the migration phase that owns it empties
its baseline. The checks read source and the route table; they do not need a
database.
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
  `tests/architecture/routesecurity` (`TestEveryRouteIsInTheRouteTable`: closed table of
  every served route and its guard, both directions;
  `TestGuardChainMatchesRegistration`: the ordered guard chain on that row,
  checked against the middleware the route files register, failing on any
  unlisted route or a chain that does not match; and
  `TestAuthenticatedRoutesRefuseAnAnonymousCaller`). The baseline is empty, so
  ratchet fails on any mismatch. A new route needs a row in `routeTable`.
- External integrators (Markets) consume `/api/v1`; the front consumes `/v1`. A change of
  status, error shape or success shape on either is a contract change, decided first and
  recorded in `.ai/guidelines/http-error-contract.md`. — guarded by `TestContract_HTTP_Contract`
  (`tests/contract`): a fixed scenario over both surfaces (success and error paths, no
  chain call, no fund movement) whose method, path, status, content type and raw body
  (uuids, timestamps and tokens normalized) must match `tests/contract/testdata/http_contract.txt`
  byte for byte. Rewrite it (`make contract-update`) only for a decided change.

### 4. Authentication

- **Session (dashboard):** Goravel JWT guard (`config/auth.go`, `config/jwt.go`), refresh
  token rotation in `refresh_tokens`, optional TOTP second factor. — partly guarded by
  `app/http/controllers/auth_controller_test.go`.
  **KNOWN VIOLATION** (fixes in progress on their own branches, alignment plan §0.4):
  S1 — the pre-2FA `partial_token` is a full session JWT; S2 — login TOTP is checked
  against the still-encrypted secret; S4 — password change/reset and TOTP disable do not
  revoke sessions; S5 — `users.status` is not enforced.
- **API token (external):** a JWT minted by `middleware.MintAPIToken` (`sub: api_token`,
  `jti` = `access_tokens` row, `account_id` claim). The row must exist and be active. When
  the token carries `require_signature`, an `X-Signature` HMAC over the body is mandatory
  and checked before the body is read. — guarded by `TestAPITokenAuthHMACSuite`
  (`app/http/middleware/api_token_auth_test.go`) and `criticalEndpointsSuite`
  (`app/http/controllers/critical_api_endpoints_test.go`).
  `permissions` is a JSON array of the token catalog. `APIScope` enforces it on
  the routes that catalog names; a blank store keeps the previous access.
  `ip_cidr` is enforced against `ClientIP`; a blank allowlist keeps the previous
  access. `spending_limit` is a per-token daily USD cap enforced in
  `withdraw.Service`; a blank cap leaves withdrawal behavior unchanged. A newly
  minted token stores `sha256` of the random secret. — guarded by
  `TestAPITokenIP`, `app/http/middleware/api_scope_test.go`, and
  `app/policies/api_token_permissions_test.go`.
  API tokens are account-owned. Suspending a member leaves the tokens that
  member minted. The list shows `created_by`. An owner or admin with
  `tokens.write` revokes one token. Removing the member deletes the tokens
  that member minted for that account. — guarded by
  `TestSuspend_KeepsMintedTokensAndBlocksMembership` and
  `TestRemove_RevokesTokensCreatedByTheMember`
  (`app/http/controllers/account_members_test.go`).

### 5. Scope: actor → account → wallet

- An API token acts only inside its own account. A wallet of another account answers
  **404** on the external API (the id is never confirmed). — guarded by
  `TestAPIWalletContextSuite` (`app/http/middleware/api_wallet_context_test.go`).
- On the dashboard the account comes from `X-Account-Id` (`AccountHeader`) or
  `{accountId}` (`AccountContext`) and must match a membership of the user; the wallet
  (`WalletContext`) must be reachable through the account membership or a `wallet_users`
  row. — UNGUARDED.
- The account or wallet id comes from the path, the header or the token — never from the
  body. — UNGUARDED.
- An account is `test` or `prod` (`accounts.environment`); a test account touches only
  testnet chains and a prod account only mainnet chains. Today the check is spread over
  handlers (`ctx.Value("account_environment")`). — UNGUARDED.
- **KNOWN VIOLATION** S3/S5/S6: mutating wallet handlers (withdraw, consolidate, generate
  address, create wallet…) check membership only, not a permission; suspended
  memberships and frozen/archived accounts are not refused. The permission of each
  handler is a product decision (`.ai/guidelines/authorization.md`).

### 6. Custody

- A wallet is a **2-of-2 MPC** wallet. Share A is the customer's, encrypted with the
  customer's passphrase (Argon2id → AES-GCM) on the wallet row; share B is the service's,
  in **AWS Secrets Manager** (LocalStack locally), referenced by `MPCSecretARN` — its
  value is never in the database. — guarded by `app/services/mpc/*_test.go`
  (`TestEncryptDecryptRoundTrip`, `TestDecryptWrongPassphrase`, keygen and signing tests)
  and `TestValidateExistingSeedWalletSecret*` (`database/seeders`).
- Key material (shares, passphrases, derived keys) is never returned after the one-time
  recovery material at creation, never logged, queued or cached. — guarded by
  `TestWalletRecoveryMaterialSuite` (`app/http/controllers/wallets_recovery_material_test.go`).
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
| Goravel queue, `database` connection, queue `blockchain` (`config/queue.go`) | wallet refresh / reconcile jobs (`app/jobs`) | the framework queue runner |
| AWS SQS (`app/services/queue`) | outbound webhook delivery | `webhook_worker` Lambda |
| `app/services/localworkers` | local stand-in for `confirmation_tracker` and `webhook_worker` (and optional deposit scan) | started from `main.go` in local mode only |

- Local webhook delivery from the outbox runs only when no SQS webhook queue is
  configured, so a message is never drained twice. — guarded by
  `TestStart_SkipsOutboxWhenAQueueDelivers` (`app/services/localworkers`).
- No job payload carries a credential (tokens, passphrases, shares). — UNGUARDED.

### 8. Database and migrations

- Schema changes are Goravel migrations in `database/migrations/`, numbered
  `000000000NNNN0_<name>.go`, one migration per file, registered in the literal list of
  `Migrations()` in `bootstrap/migrations.go`. A migration never edits an applied one; it adds
  the next number. — guarded by the migration tests in `tests/migrations` (e.g.
  `non_negative_amounts_test.go`).
- A NEW migration uses the schema builder, `facades.Schema()` from `app/facades`
  (`Create`, `Table`, `DropIfExists`), unless it needs a Postgres feature the builder does not
  express (CHECK or partial-unique constraints, `USING` casts, enum types, data backfills):
  then it runs raw SQL. The applied migrations keep their raw SQL; do not rewrite them. A
  migration imports no app code (`app/models`, `app/services`, `pkg/*`): copy what it needs
  into the migration, so a later change there cannot change what a fresh database gets.
  `artisan make:migration` creates the file and appends it to `Migrations()`, but the
  framework drops the host and organisation of the module path, so the generated import
  reads `waas/app/facades`: change it to `github.com/macrowallets/waas/app/facades`.
- Amounts are base-unit integers/strings (`pkg/amount`, `numeric` columns), never floats,
  and never negative. — guarded by `TestEnforceNonNegativeAmounts*`.
- Seed data is test-only (`database/seeders`, `docs/DEV_SEED_DATA.md`). Only `DatabaseSeeder` is
  registered, so `db:seed --seeder=ChainSeeder` is not available. The chain catalog that
  `chains:add-missing` writes in production is not seed data: it lives in
  `app/repositories/chaincatalog`.

### 9. Tests never touch live data

- The destructive suite migrates fresh only a dedicated `*_test` database
  (`TEST_DB_DATABASE`, default `vault_unit_test`); `vault` (dev) and `vault_test` (local
  e2e, holds MPC shares that cannot be recreated) are refused. — guarded by
  `tests/feature/support/testenv/environment_test.go`
  (`TestValidateConfigurationRejectsProtectedDatabases`,
  `TestApplyDatabaseOverrideToE2EDatabaseIsRefused`).
- Tests use Redis index `REDIS_DB` (15) of `.env.testing`, never the live index 0, and
  never `FLUSHALL`/`FLUSHDB`; keys are namespaced per test. — guarded by
  `TestTestRedisURLRefusesTheLiveIndexAndBadInput`,
  `TestTestingEnvironmentFileUsesANonLiveRedisIndex`.

### 10. Settings

- Account settings are the registry in `app/services/settings`. Account-managed
  groups are `account_security` and `account_webhooks`. `account_sweep_limits`
  is platform-managed and inherited. The platform group on this branch is
  `deposit_scan`. A secret (the webhook signing secret) is written and then
  redacted on read.
- `GET /v1/accounts/{accountId}/settings` is `settings.view` (owner, admin,
  auditor). `PATCH /v1/accounts/{accountId}/settings/{group}` is
  `settings.update` (owner, admin). The user role holds neither. The service
  asks `policies.MayViewSettings` and `policies.MayUpdateSettings`. — guarded by
  `TestSettingsPermissionsFollowTheAccountRoles`.
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
  `TestOnlyPoliciesCallAccountRoleOutranks` (`tests/architecture`).
- The platform pivot tables `model_has_roles`, `role_has_permissions`, and
  `model_has_permissions` are not on this branch. Only `app/policies` may read
  them. A migration may create them. — guarded by
  `TestOnlyPoliciesReadRBACPivots`.
- Dashboard permissions decided on this branch: `tokens.read` / `tokens.write`
  on `/v1/accounts/{accountId}/tokens`, `settings.view` / `settings.update`,
  `activity.read` on `GET /v1/accounts/{accountId}/activity` (owner, admin,
  auditor). External token permissions are the catalog in
  `app/policies/api_token_permissions.go`, checked by `APIScope`. Minting
  refuses a permission the creator does not hold.
- `GET /v1/platform/activity` is a platform admin, the same gate as platform
  flags. There is no `audit.view` permission row on this branch.
- Invites, user suspension, and the session watermark are not on this branch.

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
| `bootstrap/` | Goravel setup: providers, migrations, rules |
| `config/` | config as code, read from env |
| `routes/` | `admin.go` (`/v1`), `api.go` (`/api/v1`), `webhooks.go` (ingest), `docs.go` (health, Swagger) |
| `app/http/` | `controllers`, `middleware`, `requests` (FormRequests), `pagination` |
| `app/services/` | business logic and, for now, the chain/provider/AWS adapters |
| `app/repositories/`, `app/models/` | persistence and schema types |
| `app/policies/`, `app/providers/` | Gate policies; service providers and the container wiring |
| `app/console/`, `app/jobs/`, `app/mails/`, `app/rules/` | artisan commands, queue jobs, mail, validation rules (there is no `app/events/` or `app/listeners/`) |
| `database/` | migrations, seeders, seed logic |
| `pkg/`, `packages/` | `pkg/`: `amount`, `numeric`, `types`, `httpclient`, `pgerr`, `lifecycle`, `mpcshare`, `e2evault`. `packages/activitylog`: Goravel package (own ServiceProvider, listed in `bootstrap/providers.go`) |
| `tests/` | `feature/support` (suite, docker reuse, fixtures, request signing), hand-written `mocks`, `architecture` (machine-checked rules), `contract` (HTTP contract snapshot) |
| `docs/` | Swagger output and design notes (`GORAVEL_INTEGRATION.md`, `INTEGRATION_STATUS.md` are historical) |

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

## Project Structure

```
back/
├── main.go                  # Entry point: Goravel boot → Lambda or local HTTP
├── bootstrap/               # Goravel Setup (WithProviders, WithSeeders, WithRouting, WithConfig)
├── Makefile                 # Dev, build, test, deploy commands
├── docker-compose.yml       # Postgres, Redis, LocalStack
├── .env.dev.example         # Dev env template
│
├── config/
│   ├── app.go               # Goravel config: DB, cache, auth, mail, HTTP driver
│   └── security.go          # Security config
│
├── routes/
│   └── api.go               # All route definitions (Goravel routing)
│
├── app/
│   ├── container/           # DI container (boots all services)
│   ├── providers/           # Goravel service providers (migrations, auth)
│   ├── models/              # Goravel ORM models (embed orm.Model)
│   ├── repositories/        # Database queries via facades.Orm().Query()
│   ├── http/
│   │   ├── controllers/     # HTTP handlers + Swagger annotations
│   │   ├── middleware/       # Session auth, API token auth, CORS, UTXO-only, etc.
│   │   ├── pagination/      # Pagination helpers
│   │   └── requests/        # Request validation DTOs
│   ├── services/
│   │   ├── account/         # Account management
│   │   ├── auth/            # Auth (register, login, 2FA, JWT)
│   │   ├── blockheight/     # Block height tracking
│   │   ├── chain/           # Blockchain adapters (EVM, Solana, Bitcoin)
│   │   ├── deposit/         # Deposit scanning + confirmation tracking
│   │   ├── ingest/          # Webhook ingest from chain providers
│   │   ├── mpc/             # Multi-party computation
│   │   ├── queue/           # SQS client
│   │   ├── wallet/          # Wallet + address derivation
│   │   ├── webhook/         # Webhook delivery
│   │   ├── webhooksync/     # Webhook reconciliation
│   │   └── withdraw/        # Withdrawal execution
│   ├── mails/               # Goravel mail templates
│   └── policies/            # Authorization policies
│
├── database/
│   ├── migrations/          # Goravel schema migrations (*.go)
│   └── seeders/             # Dev seed logic (DatabaseSeeder → `db:seed`)
│
├── pkg/
│   ├── types/               # Shared types (WebhookMessage, etc.)
│   └── e2evault/            # Verified wallet passphrases from the e2e vault
├── docs/                    # Swagger specs + design docs
├── tools/                   # Standalone binaries: macro-e2e, localstack-secrets-snapshot (`internal/pyjson`: Python-compatible JSON for their ledger/snapshot formats)
└── tests/                   # Mocks + test utilities
```

## Tests

```bash
make test                        # unit + integration
make test-unit                   # no database, seconds
make test-integration TEST_PARALLEL=6 TEST_FLAGS=-v
make test-integration TEST_FLAGS='-run TestWalletRepository'
```

- Unit vs integration is decided per package from its test imports (`go list`), no build tags: a package is integration when its tests import `tests/testenv`, `tests/testutil`, `tests` or `bootstrap`.
- `mocks.TestDB(t)` migrates the schema fresh once per test binary (a worker clone already is), then truncates every table but `migrations` before and after each test. Tests that run migrations up/down use `mocks.TestDBFreshSchema(t)` (fresh schema before and after).
- `go run ./tests/feature/support/testdb prepare | drop-clones` are the template/cleanup steps `make test-integration` runs; both refuse names outside `vault_unit_test*`.
- Running `go test ./app/repositories` directly still works on `vault_unit_test` itself (no clone; take the lock with `flock ~/.local/state/macro-e2e/locks/vault_unit_test.lock …` when another run may be active).
- `TEST_TIMEOUT` (default 30m) is a safety net per `go test` invocation.
- Timings on the dev machine: `make test` ~2.5 min (was ~35 min with `-p 1` and `migrate:fresh` twice per test); `test-unit` ~1.5 min (mostly `app/services/mpc` keygen), `test-integration` ~40 s with 6 workers, ~1.7 min with `TEST_PARALLEL=1`.
- Unreachable endpoints in tests use a just-released local port (`testutil.ClosedLocalURL`), not port 1: on WSL2 `127.0.0.1:1` hangs until the client timeout instead of refusing.

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

To create a new migration, add a file in `database/migrations/` implementing `Signature()`, `Up()`, and `Down()`, then register it in the migrations provider.

See `docs/GORAVEL_INTEGRATION.md` for the full schema builder reference.

## API Routes

Two auth schemes defined in `routes/api.go`:

- **Dashboard** (`/v1/*`) — JWT session auth via `middleware.SessionAuth`
- **External API** (`/api/v1/*`) — Bearer token auth via `middleware.APITokenAuth`

Swagger UI: http://localhost:2002/swagger/index.html

## Key Env Vars

```bash
PORT=2002
DB_HOST=localhost
DB_PORT=5432
DB_DATABASE=vault
DB_USERNAME=vault
DB_PASSWORD=vault
REDIS_HOST=localhost
REDIS_PORT=6379
ETH_RPC_URL=https://eth-sepolia.public.blastapi.io
POLYGON_RPC_URL=https://rpc-amoy.polygon.technology
SOLANA_RPC_URL=https://api.devnet.solana.com
BTC_RPC_URL=https://blockstream.info/testnet/api
API_KEY_SECRET=dev-secret-key-not-for-production
```

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

One node answer is read up to a per-chain limit and refused past it with an explicit `response larger than N bytes` error (never truncated): EVM 64 MiB (`evmRPCMaxResponseBytes`; a busy Base Sepolia block with full transactions is ~4.3 MB), Solana 32 MiB (`solanaRPCMaxResponseBytes`), Bitcoin family 32 MiB, TRON HTTP 64 MiB. Other JSON-RPC clients (chain probes, `evmcall`) keep the 1 MiB default.

### Paid fees

The confirmation tracker stores the fee actually paid (native base units) in `transactions.fee` when a withdrawal, sweep or gas_seed confirms: EVM gasUsed × effectiveGasPrice + L1 fee, BTC/LTC inputs − outputs, Solana `meta.fee`, TRON `fee` (energy + bandwidth burned). Older rows: `go run . artisan transactions:backfill-fees [--chain a,b] [--apply]` (dry run by default, idempotent, reads only).

### TRON gas_seed

A TRC-20 sweep's gas_seed gives the child what it lacks to pay the estimated bandwidth plus the energy the child pays itself (the contract deployer's share, `consume_user_resource_percent` and its staked energy, is subtracted) + 20 % (`tronGasSeedMarginPercent`), capped at the transaction's fee_limit ceiling. Right before the sweep the executor re-prices it and sends a delta gas_seed (at most 2) when the child is still short; otherwise the sweep is not broadcast.

## Deploy

```bash
make deploy ENV=prod    # AWS Lambda via SAM
```
