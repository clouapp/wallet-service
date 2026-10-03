# Macro Wallets — Backend (WaaS)

Multi-chain Wallet-as-a-Service API built on **Goravel** (Go 1.22) with PostgreSQL, Redis, and AWS Lambda.

## Goravel Framework

This project uses [Goravel v1.17](https://goravel.dev) as its core framework. Goravel is a Laravel-inspired Go framework providing:

- **Routing** — `facades.Route()` with groups, middleware, prefixes (`routes/api.go`)
- **ORM** — `facades.Orm().Query()` for database operations, models embed `orm.Model`
- **Migrations** — Schema builder via `facades.Schema()` (`database/migrations/`)
- **Artisan CLI** — `go run . artisan migrate`, `migrate:status`, `migrate:rollback`, `migrate:fresh`, `db:seed`, `migrate:fresh --seed` ([seeding](https://www.goravel.dev/database/seeding.html))
- **Auth** — JWT guards via `facades.Auth()` (`config/app.go`)
- **Cache** — Redis driver via `facades.Cache()` (goravel/redis)
- **Mail** — SMTP via `facades.Mail()` (`app/mails/`)
- **Validation** — Request validation via Goravel contracts
- **HTTP driver** — Gin via `goravel/gin`
- **Database driver** — PostgreSQL via `goravel/postgres`

**Goravel docs**: https://goravel.dev/getting-started/installation
**ORM**: https://goravel.dev/orm/getting-started
**Migrations**: https://goravel.dev/database/migrations
**Routing**: https://goravel.dev/the-basics/routing
**Auth**: https://goravel.dev/security/authentication

## Monorepo

```
macro-wallets/
├── back/   ← you are here (Go API, port 2002)
└── front/  ← vinext frontend (port 2001)
```

## Quick Start

```bash
cp .env.dev.example .env.dev
make dev          # starts Docker + backend + frontend
```

| Command | What it does |
|---------|--------------|
| `make dev` | Full stack (Docker + back + front) |
| `make dev-back` | Backend only (Air live reload) |
| `make dev-front` | Frontend only (vinext) |
| `make run` | Backend without live reload |
| `make stop` | Kill all dev processes |
| `make test` | Run Go tests (migrates fresh `TEST_DB_DATABASE`, default `vault_unit_test`; `vault` and `vault_test` are refused) |
| `make docker-up` | Start Docker services |
| `make docker-down` | Stop Docker services |
| `make migrate` | Run pending migrations |
| `make migrate-fresh` | Drop all + re-migrate |
| `make db-seed` | `artisan db:seed` (dev data) |
| `make migrate-fresh-seed` | `artisan migrate:fresh --seed` |
| `make key-generate` | `artisan key:generate` on `.env.dev` (also runs automatically when `APP_KEY` is empty) |
| `make e2e-tools` | Build the `macro-e2e` helper into `~/.local/state/macro-e2e/bin/` |
| `make localstack-hooks` | Build the static LocalStack `secrets-snapshot` binary (also run by `docker-up`/`dev`) |

## Docker (project: `macro-wallets`, prefix: `waas-`)

| Container | Port |
|-----------|------|
| `waas-postgres` | 5432 |
| `waas-redis` | 6379 |
| `waas-localstack` | 4566 |

LocalStack community has no native persistence: hooks in `docker/localstack/` run the Go binary `secrets-snapshot` (`tools/localstack-secrets-snapshot`, built by `make localstack-hooks`, mounted from `docker/localstack/bin/`) to keep Secrets Manager (MPC share B) in encrypted, ARN-preserving snapshots in the `localstack_data` volume (restore on start, export every 60 s and on stop; key in `~/.config/macro-wallets/localstack-seed/`, never in the repo). The container is created with `--env-file .env.dev`. Checks: `docker exec waas-localstack /etc/localstack/secrets-snapshot/secrets-snapshot verify`; host-side safety net: `wallet-vault.py localstack-export | localstack-check | localstack-restore`. See `docs/LOCALSTACK_SECRETS_SNAPSHOT.md`.

## E2E helpers (`tools/macro-e2e`)

Standalone binary (no Goravel boot: it targets `vault_test` through `~/.local/state/macro-e2e/wallets-api.environ`, never `.env`). Run from `back/` (`go run ./tools/macro-e2e ...`) or use the prebuilt `~/.local/state/macro-e2e/bin/macro-e2e` (`make e2e-tools`; the Markets recordings call it).

| Command | What it does |
|---------|--------------|
| `capture-env [--dry-run]` | Rebuild `wallets-api.environ` (0600, NUL-separated) from `.env.dev` + e2e overrides (vault_test, testnet, scan chains `sol,eth,btc,polygon,base,arbitrum,bsc`, BTC testnet4); prints key names only. `--dry-run` writes nothing and compares with the current environ (differing key names + sha256 digest, never values) |
| `fee-estimate WALLET ASSET TO [--amount DECIMAL]` | Read-only `GET /api/v1/wallets/{id}/fee-estimate` with the Markets token (kept in memory); exit 1 unless HTTP 200 |
| `api [--build] [--status] [--no-start]` | Build/stop/start the e2e API binary; takes `locks/wallets-api-restart.lock` itself (do not wrap it in `flock`), refuses during a recording; stop = SIGTERM, 20 s grace, then SIGKILL |
| `preflight withdrawal WALLET ASSET BASE_UNITS TO` / `preflight consolidation WALLET ASSET` | Off-chain plan + MPC sign + verify; nothing broadcast |
| `send-from-base TAG WALLET ASSET BASE_UNITS DECIMALS TO EXT_USER [--chain C] [--recording-lock-held-by OWNER] [--apply]` | One guarded funding transfer (ledger, vault_test, claim, recording lock, pre-flight, UUIDv5 idempotency); dry run without `--apply` |
| `consolidate TAG WALLET ASSET [--apply]` | One guarded child-address sweep, same guards |

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
│   ├── seeders/             # Artisan seeders (DatabaseSeeder → `db:seed`)
│   └── seeds/               # Seed logic (chains, users, wallets, …)
│
├── pkg/
│   ├── types/               # Shared types (WebhookMessage, etc.)
│   ├── security/            # Input sanitization
│   ├── pyjson/              # Python-compatible JSON (ledger/snapshot formats)
│   └── e2evault/            # Verified wallet passphrases from the e2e vault
├── docs/                    # Swagger specs + design docs
├── tools/                   # Standalone binaries: macro-e2e, localstack-secrets-snapshot
└── tests/                   # Mocks + test utilities
```

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

## Deploy

```bash
make deploy ENV=prod    # AWS Lambda via SAM
```
