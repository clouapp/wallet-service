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
| `make test` | Run Go tests |
| `make docker-up` | Start Docker services |
| `make docker-down` | Stop Docker services |
| `make migrate` | Run pending migrations |
| `make migrate-fresh` | Drop all + re-migrate |
| `make db-seed` | `artisan db:seed` (dev data) |
| `make migrate-fresh-seed` | `artisan migrate:fresh --seed` |
| `make key-generate` | `artisan key:generate` on `.env.dev` (also runs automatically when `APP_KEY` is empty) |

## Docker (project: `macro-wallets`, prefix: `waas-`)

| Container | Port |
|-----------|------|
| `waas-postgres` | 5432 |
| `waas-redis` | 6379 |
| `waas-localstack` | 4566 |

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
│   └── security/            # Input sanitization
├── docs/                    # Swagger specs + design docs
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
