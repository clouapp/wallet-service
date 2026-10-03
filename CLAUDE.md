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
  recorded in `.ai/guidelines/http-error-contract.md`. — guarded by `TestHTTPContract`
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
  and `TestValidateExistingSeedWalletSecret*` (`database/seeds`).
- Key material (shares, passphrases, derived keys) is never returned after the one-time
  recovery material at creation, never logged, queued or cached. — guarded by
  `TestWalletRecoveryMaterialSuite` (`app/http/controllers/wallets_recovery_material_test.go`).
- Signing order (build → policy checks → fetch share B → combine → sign → broadcast →
  persist) is not changed "in passing". Any change to `mpc`, `wallet`, `withdraw` or
  `sweep` runs the testnet e2e (`scripts/e2e`, `tools/e2e-funder`: SOL, BTC, ETH, POL).
- `facades.Crypt()` (`APP_KEY`) seals TOTP secrets, RPC URLs and ingest signing secrets at
  rest; it is not the MPC share envelope. **KNOWN VIOLATION** S11: `webhook_configs.secret`
  is plain text.

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
  `000000000NNNN0_<name>.go`, registered in `migrations.All()` (`bootstrap/migrations.go`).
  A migration never edits an applied one; it adds the next number. — guarded by the
  migration tests beside them (e.g. `non_negative_amounts_test.go`).
- Amounts are base-unit integers/strings (`pkg/amount`, `numeric` columns), never floats,
  and never negative. — guarded by `TestEnforceNonNegativeAmounts*`.
- Seed data is test-only (`database/seeds`, `docs/DEV_SEED_DATA.md`).

### 9. Tests never touch live data

- The destructive suite migrates fresh only a dedicated `*_test` database
  (`TEST_DB_DATABASE`, default `vault_unit_test`); `vault` (dev) and `vault_test` (local
  e2e, holds MPC shares that cannot be recreated) are refused. — guarded by
  `tests/testenv/environment_test.go`
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
- Account flags reuse the settings permissions: list is `settings.view`, write
  is `settings.update`. An unknown key on write is 404 before the permission
  check. `GET|PATCH /v1/platform/features` is a platform admin
  (`platform_admins`), not an account owner. — guarded by
  `app/services/features/service_test.go` and `gate_test.go`.
- `withdrawals-enabled` and `sweep-enabled` default to on. `Gate` pauses the
  action when the account flag is off, and when the global row is an explicit
  false. A missing global row does not pause. The conflict codes are
  `withdrawals_paused` and `sweep_paused`.
- `user-2fa-required` and `account_security.require_2fa` are the two switches
  `TOTPEnrollment` reads on account and wallet routes.

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
| `make dev` / `make dev-back` / `make run` | full stack / backend with live reload / backend without |
| `make stop` | kill the dev processes |
| `make test` | all Go tests, `-p 1`, against `TEST_DB_DATABASE` (default `vault_unit_test`); architecture checks in ratchet mode |
| `make test-race` | the same suite with `-race`; architecture checks stay in report mode unless `ARCH_MODE` is set |
| `make lint` | golangci-lint v2 with `.golangci.yml` (report mode: lists findings, exits 0) |
| `make arch` | architecture checks with every finding listed (`ARCH_MODE=ratchet\|enforce` to block) |
| `make contract` / `make contract-update` | compare / rewrite the HTTP contract snapshot |
| `make docker-up` / `make docker-down` | Postgres, Redis, LocalStack |
| `make migrate` / `make migrate-status` / `make migrate-rollback` / `make migrate-fresh` | migrations |
| `make db-seed` / `make migrate-fresh-seed` | dev seed data |
| `make key-generate` / `make jwt-secret` | `APP_KEY` / `JWT_SECRET` in `.env.dev` |
| `make swagger-generate` | regenerate `docs/` (Swagger UI at `/swagger/index.html`) |

Docker (compose project `macro-wallets`, prefix `waas-`): `waas-postgres`, `waas-redis`,
`waas-localstack`; host ports come from `.env.dev` (`DB_PORT`, `REDIS_PORT`,
`LOCALSTACK_PORT`). LocalStack community has no native persistence: hooks in
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
| `app/console/`, `app/jobs/`, `app/events/`, `app/listeners/`, `app/mails/`, `app/rules/` | artisan commands, queue jobs, events, mail, validation rules |
| `database/` | migrations, seeders, seed logic |
| `pkg/` | `amount`, `types`, `httpclient` |
| `tests/` | `testenv`, `testutil`, hand-written `mocks`, `architecture` (machine-checked rules), `contract` (HTTP contract snapshot) |
| `docs/` | Swagger output and design notes (`GORAVEL_INTEGRATION.md`, `INTEGRATION_STATUS.md` are historical) |

Everything inside a `.go` file is English.