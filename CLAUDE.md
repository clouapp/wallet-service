# Vault Custody Service - Claude Code Documentation

**Project**: Multi-chain Cryptocurrency Custody Service
**Repository**: https://github.com/clouapp/wallet-service
**Language**: Go 1.22
**Architecture**: AWS Lambda + API Gateway (serverless), with local dev support
**Last Updated**: March 17, 2026

---

## Table of Contents

1. [Project Overview](#project-overview)
2. [Architecture](#architecture)
3. [Project Structure](#project-structure)
4. [Core Components](#core-components)
5. [Development Setup](#development-setup)
6. [API Documentation](#api-documentation)
7. [Testing](#testing)
8. [Deployment](#deployment)
9. [Recent Changes](#recent-changes)
10. [Known Issues](#known-issues)
11. [Next Steps](#next-steps)

---

## Project Overview

Vault is a **serverless multi-chain cryptocurrency custody service** that provides:

- **Wallet Management**: Create and manage HD wallets for multiple blockchains
- **Deposit Scanning**: Automated blockchain monitoring for incoming deposits
- **Withdrawal Processing**: Secure transaction broadcasting with queue-based processing
- **Webhooks**: Event-driven notifications for deposits, withdrawals, and transactions
- **Multi-Chain Support**: Ethereum, Polygon, Solana, and Bitcoin

### Key Features

- ✅ Serverless AWS Lambda architecture (4 Lambda modes)
- ✅ PostgreSQL for persistence
- ✅ Redis for caching
- ✅ SQS message queues for async processing
- ✅ EventBridge for scheduled deposit scanning
- ✅ HMAC-based API authentication
- ✅ Swagger/OpenAPI documentation
- ✅ Docker Compose for local development
- ✅ Comprehensive unit tests

---

## Architecture

### Deployment Modes

The service runs as a **single binary** with multiple execution modes controlled by `LAMBDA_MODE` environment variable:

| Mode | Purpose | Trigger | Handler Function |
|------|---------|---------|------------------|
| **API** (default) | REST API | API Gateway (HTTP) | `handleAPIGateway` |
| `deposit_scanner` | Blockchain scanning | EventBridge (cron) | `handleDepositScan` |
| `webhook_worker` | Webhook delivery | SQS queue | `handleWebhookWorker` |
| `withdrawal_worker` | Transaction signing & broadcast | SQS queue | `handleWithdrawalWorker` |

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         API Gateway                              │
│                    (HMAC Authentication)                         │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Lambda: API Mode                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │   Gin HTTP   │  │ Controllers  │  │   Services   │          │
│  │    Router    │──│  (Wallet,    │──│  (Business   │          │
│  │  (Swagger)   │  │  Withdrawal) │  │    Logic)    │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
└───────────────────────────┬─────────────────────────────────────┘
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  PostgreSQL  │    │    Redis     │    │  SQS Queues  │
│   (Wallets,  │    │   (Cache)    │    │  (Webhook,   │
│    Txns)     │    │              │    │  Withdrawal) │
└──────────────┘    └──────────────┘    └──────┬───────┘
                                               │
        ┌──────────────────────────────────────┼─────────────┐
        │                                      │             │
        ▼                                      ▼             ▼
┌─────────────────┐                 ┌──────────────────────────┐
│  EventBridge    │                 │  Lambda: Workers         │
│  (Schedule)     │                 │  ┌─────────────────────┐ │
│   */5 * * * *   │────────────────▶│  │ Webhook Worker      │ │
└─────────────────┘                 │  │ Withdrawal Worker   │ │
        │                           │  └─────────────────────┘ │
        │                           └──────────────────────────┘
        ▼
┌─────────────────────────┐
│ Lambda: Deposit Scanner │
│  ┌──────────────────┐   │         ┌──────────────────────┐
│  │ Chain RPC Calls  │───┼────────▶│  Blockchain Nodes    │
│  │ (ETH, SOL, BTC)  │   │         │  (Alchemy, Helius)   │
│  └──────────────────┘   │         └──────────────────────┘
└─────────────────────────┘
```

### Technology Stack

- **Language**: Go 1.22
- **Web Framework**: Gin (lightweight HTTP router)
- **Database**: PostgreSQL 15 (via `sqlx`)
- **Cache**: Redis 7
- **Message Queue**: AWS SQS
- **Scheduler**: AWS EventBridge
- **Blockchain Clients**:
  - Ethereum/Polygon: `go-ethereum` (Geth)
  - Solana: `gagliardetto/solana-go`
  - Bitcoin: `btcsuite/btcd`
- **AWS SDK**: `aws-sdk-go-v2`
- **Testing**: `testify/assert`, `testify/mock`
- **Documentation**: Swagger/OpenAPI (swaggo v1.8.12)

---

## Project Structure

```
wallet-service/
├── main.go                      # Entry point (4 Lambda handlers)
├── go.mod                       # Dependencies
├── go.sum
├── Makefile                     # Build, test, deploy commands (50+ targets)
├── Dockerfile                   # Multi-stage: builder, production, Lambda
├── docker-compose.yml           # Local dev (Postgres, Redis, PgAdmin)
├── .env.dev                     # Local environment (mock RPC URLs)
├── .env.dev.example
├── .env.prod.example
├── CLAUDE.md                    # This file
│
├── app/
│   ├── providers/
│   │   └── container.go         # Dependency injection container
│   ├── models/
│   │   └── models.go            # Database models (Wallet, Address, Transaction)
│   ├── http/
│   │   ├── controllers/
│   │   │   ├── controller.go    # HTTP handlers (Swagger annotations)
│   │   │   └── controller_test.go
│   │   └── middleware/
│   │       ├── auth.go          # HMAC authentication
│   │       └── auth_test.go
│   └── services/
│       ├── chain/               # Blockchain adapters
│       │   ├── registry.go      # Chain config registry
│       │   ├── rpc.go           # RPC client manager
│       │   ├── evm.go           # Ethereum/Polygon adapter
│       │   ├── solana.go        # Solana adapter
│       │   ├── bitcoin.go       # Bitcoin adapter
│       │   └── *_test.go
│       ├── wallet/              # Wallet creation, address derivation
│       │   ├── service.go
│       │   └── service_test.go
│       ├── deposit/             # Deposit scanning logic
│       │   ├── service.go
│       │   └── service_test.go
│       ├── withdraw/            # Withdrawal execution
│       │   ├── service.go
│       │   └── service_test.go
│       ├── webhook/             # Webhook delivery
│       │   ├── service.go
│       │   └── service_test.go
│       └── queue/
│           ├── sqs.go           # SQS client wrapper
│           └── sqs_test.go
│
├── pkg/
│   └── types/
│       ├── types.go             # Shared types (WebhookMessage, etc.)
│       └── types_test.go
│
├── tests/
│   └── mocks/
│       ├── chain.go             # Mock blockchain adapter
│       └── testdb.go            # Test database helpers
│
├── docs/
│   ├── swagger.json             # Generated Swagger spec
│   ├── swagger.yaml
│   ├── docs.go                  # Generated Swagger metadata
│   ├── RPC_PROVIDERS.md         # RPC endpoint guide (Alchemy, Helius, etc.)
│   └── ARCHITECTURE.md          # (TODO: detailed architecture)
│
└── migrations/                  # SQL migration files (TODO)
```

---

## Core Components

### 1. Main Entry Point (`main.go`)

**Lines 77-92**: Mode detection and handler routing
```go
func main() {
    mode := os.Getenv("LAMBDA_MODE")
    switch mode {
    case "deposit_scanner":
        lambda.Start(handleDepositScan)
    case "webhook_worker":
        lambda.Start(handleWebhookWorker)
    case "withdrawal_worker":
        lambda.Start(handleWithdrawalWorker)
    default:
        // API mode — HTTP server behind API Gateway
        ginLambda = ginadapter.NewV2(setupRouter())
        lambda.Start(handleAPIGateway)
    }
}
```

**Lines 102-124**: HTTP router setup with Swagger
- Health check endpoint (no auth): `/health`
- Swagger UI (no auth): `/swagger/*`
- API v1 endpoints (HMAC auth): `/v1/*`

### 2. Dependency Injection (`app/providers/container.go`)

**Container Pattern**: All services initialized once in `Boot()` and reused across Lambda invocations.

Key services:
- `ChainRegistry`: Blockchain adapters (ETH, SOL, BTC)
- `WalletService`: Wallet/address creation
- `DepositService`: Block scanning
- `WithdrawalService`: Transaction signing
- `WebhookService`: Event delivery
- `SQSClient`: Message queue

### 3. Blockchain Adapters (`app/services/chain/`)

**Interface**: `ChainAdapter`
```go
type ChainAdapter interface {
    GetLatestBlock(ctx context.Context) (uint64, error)
    GetTransactions(ctx context.Context, blockNumber uint64) ([]Transaction, error)
    BroadcastTransaction(ctx context.Context, signedTx string) (string, error)
    // ... more methods
}
```

**Implementations**:
- `EVMAdapter`: Ethereum & Polygon (app/services/chain/evm.go)
- `SolanaAdapter`: Solana (app/services/chain/solana.go)
- `BitcoinAdapter`: Bitcoin (app/services/chain/bitcoin.go)

### 4. Authentication (`app/http/middleware/auth.go`)

HMAC-SHA256 signature verification:
```http
X-API-Key: client-id
X-API-Signature: hmac-sha256(request_body, secret)
```

Validates signatures for all `/v1/*` endpoints.

### 5. Message Queue (`app/services/queue/sqs.go`)

**Interface**: `Sender`
```go
type Sender interface {
    SendWebhook(ctx context.Context, msg types.WebhookMessage) error
    SendWithdrawal(ctx context.Context, msg types.WithdrawalMessage) error
}
```

**Implementation**: `SQSClient` wraps AWS SQS SDK with JSON marshaling and batching support.

### 6. Workers (Lambda Handlers)

**Webhook Worker** (`handleWebhookWorker`):
- Consumes SQS messages
- Calls `WebhookService.Deliver()`
- Returns failed messages for SQS retry

**Withdrawal Worker** (`handleWithdrawalWorker`):
- Consumes withdrawal requests
- Signs transactions with master key
- Broadcasts to blockchain
- Updates database status

**Deposit Scanner** (`handleDepositScan`):
- Triggered by EventBridge (e.g., every 5 minutes)
- Scans latest blocks for deposit addresses
- Emits webhook events on new deposits

---

## Development Setup

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- AWS CLI (optional for deployment)
- Make

### Quick Start (Local Development)

1. **Clone the repository**:
   ```bash
   git clone https://github.com/clouapp/wallet-service.git
   cd wallet-service
   ```

2. **Start dependencies**:
   ```bash
   make docker-up
   ```
   This starts PostgreSQL, Redis, and PgAdmin.

3. **Copy environment file**:
   ```bash
   cp .env.dev .env
   ```

4. **Run migrations** (TODO):
   ```bash
   make migrate-up
   ```

5. **Install dependencies**:
   ```bash
   make deps
   ```

6. **Run the API server**:
   ```bash
   make run
   ```

7. **Access Swagger UI**:
   Open http://localhost:8080/swagger/index.html

### Makefile Commands

Key commands (see `make help` for full list):

| Command | Description |
|---------|-------------|
| `make help` | Show all available commands |
| `make build` | Compile binary to `bin/vault` |
| `make run` | Run API server locally |
| `make test` | Run all tests |
| `make test-coverage` | Generate coverage report |
| `make docker-up` | Start Docker services |
| `make docker-down` | Stop Docker services |
| `make swagger-generate` | Generate Swagger docs |
| `make lint` | Run golangci-lint |
| `make clean` | Clean build artifacts |

### Environment Variables

**Required for local development** (`.env.dev`):

```bash
# Application
ENV=dev
PORT=8080

# Database
DATABASE_URL=postgres://vault:vault@localhost:5432/vault?sslmode=disable

# Redis
REDIS_URL=redis://localhost:6379

# Blockchain RPC URLs (see docs/RPC_PROVIDERS.md)
ETH_RPC_URL=https://eth-sepolia.public.blastapi.io
POLYGON_RPC_URL=https://rpc-amoy.polygon.technology
SOLANA_RPC_URL=https://api.devnet.solana.com
BTC_RPC_URL=https://blockstream.info/testnet/api

# Security
API_KEY_SECRET=dev-secret-key-change-in-production
MASTER_KEY_REF=local:dev-master-key
```

**Production variables** (`.env.prod.example`):
- Replace RPC URLs with dedicated providers (Alchemy, Helius, QuickNode)
- Set real AWS credentials and queue URLs
- Use AWS Secrets Manager for `MASTER_KEY_REF`

---

## API Documentation

### Swagger/OpenAPI

- **Swagger UI**: http://localhost:8080/swagger/index.html
- **OpenAPI Spec**: `docs/swagger.json`
- **Generate Docs**: `make swagger-generate`

### Current Endpoints (Partial Implementation)

| Method | Path | Description | Auth | Status |
|--------|------|-------------|------|--------|
| GET | `/health` | Health check | No | ✅ Implemented |
| GET | `/v1/chains` | List supported chains | Yes | ✅ Implemented |
| GET | `/v1/wallets` | List all wallets | Yes | ✅ Implemented |
| POST | `/v1/wallets` | Create new wallet | Yes | ✅ Implemented |
| GET | `/v1/wallets/{id}` | Get wallet details | Yes | 🚧 TODO |
| POST | `/v1/wallets/{id}/addresses` | Generate new address | Yes | 🚧 TODO |
| GET | `/v1/addresses/{address}` | Lookup address | Yes | 🚧 TODO |
| POST | `/v1/withdrawals` | Create withdrawal | Yes | 🚧 TODO |
| GET | `/v1/withdrawals/{id}` | Get withdrawal status | Yes | 🚧 TODO |
| GET | `/v1/transactions` | List transactions | Yes | 🚧 TODO |
| POST | `/v1/webhooks` | Register webhook | Yes | 🚧 TODO |
| GET | `/v1/webhooks` | List webhooks | Yes | 🚧 TODO |
| DELETE | `/v1/webhooks/{id}` | Delete webhook | Yes | 🚧 TODO |

**Note**: Controller methods exist in `app/http/controllers/controller.go` but Swagger annotations are incomplete. Only 3 endpoints have full Swagger docs currently.

### Adding Swagger Annotations

Example from `controller.go:132`:

```go
// createWallet godoc
// @Summary Create a new wallet
// @Description Create a new wallet for a specific blockchain
// @Tags Wallets
// @Security ApiKeyAuth
// @Security SignatureAuth
// @Accept json
// @Produce json
// @Param request body object{chain=string,label=string} true "Wallet creation request"
// @Success 201 {object} map[string]interface{} "Created wallet"
// @Failure 400 {object} map[string]string "Bad request"
// @Failure 409 {object} map[string]string "Wallet already exists"
// @Router /wallets [post]
func (ctrl *Controller) createWallet(c *gin.Context) {
    // Implementation...
}
```

After adding annotations, run:
```bash
make swagger-generate
```

---

## Testing

### Unit Tests

All services have unit tests using `testify`:

```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Run specific package
go test ./app/services/wallet/...

# Run with verbose output
make test-verbose
```

### Test Structure

- **Mocks**: Located in `tests/mocks/`
  - `chain.go`: Mock blockchain adapter
  - `testdb.go`: In-memory test database helpers
- **Test Files**: Co-located with source (e.g., `service.go` → `service_test.go`)

### Example Test

From `app/services/wallet/service_test.go`:

```go
func TestCreateWallet(t *testing.T) {
    db := testdb.NewTestDB(t)
    defer db.Close()

    svc := NewWalletService(db, chainRegistry)

    wallet, err := svc.Create(context.Background(), "ethereum", "test-wallet")

    assert.NoError(t, err)
    assert.NotEmpty(t, wallet.ID)
    assert.Equal(t, "ethereum", wallet.Chain)
}
```

### Current Test Coverage

Run `make test-coverage` to generate HTML coverage report:
```bash
# Output: coverage.html
```

**Target**: 80%+ coverage for all services.

---

## Deployment

### AWS Lambda Deployment

**Architecture**:
- 4 separate Lambda functions (same binary, different `LAMBDA_MODE`)
- API Gateway for HTTP routing
- EventBridge for deposit scanner scheduling
- SQS queues for async workers

**Deployment Steps** (TODO - SAM template incomplete):

1. Build Lambda binary:
   ```bash
   make build-lambda
   ```

2. Package with SAM:
   ```bash
   sam build
   sam package --s3-bucket your-deployment-bucket
   ```

3. Deploy:
   ```bash
   sam deploy --guided
   ```

### Docker Deployment (Alternative)

For non-Lambda deployments (e.g., ECS, Kubernetes):

```bash
# Build production image
docker build --target production -t vault-api:latest .

# Run
docker run -p 8080:8080 --env-file .env.prod vault-api:latest
```

### Environment-Specific Configs

- **Development**: `.env.dev` (local, mock RPC URLs)
- **Staging**: `.env.staging` (testnet RPC URLs)
- **Production**: `.env.prod` (mainnet RPC URLs, AWS Secrets Manager)

---

## Recent Changes

### March 17, 2026 - Swagger Integration

**Changes Made**:

1. **Added Swagger Dependencies** (go.mod):
   - `github.com/swaggo/swag v1.8.12`
   - `github.com/swaggo/gin-swagger v1.5.3`
   - `github.com/swaggo/files v1.0.0`

2. **Modified main.go**:
   - Added Swagger imports and annotations
   - Added Swagger UI endpoint: `/swagger/*any`
   - Added API metadata annotations (title, version, security definitions)

3. **Modified app/http/controllers/controller.go**:
   - Added Swagger annotations to 3 methods:
     - `health` (GET /health)
     - `listChains` (GET /v1/chains)
     - `createWallet` (POST /v1/wallets)
     - `listWallets` (GET /v1/wallets)

4. **Generated Swagger Docs**:
   - `docs/docs.go`
   - `docs/swagger.json`
   - `docs/swagger.yaml`

5. **Created .env.dev**:
   - Added working mock RPC URLs for local testing
   - ETH Sepolia: `https://eth-sepolia.public.blastapi.io`
   - Polygon Amoy: `https://rpc-amoy.polygon.technology`
   - Solana Devnet: `https://api.devnet.solana.com`
   - Bitcoin Testnet: `https://blockstream.info/testnet/api`

6. **Created docs/RPC_PROVIDERS.md**:
   - Comprehensive guide to RPC providers
   - Alchemy, Infura, QuickNode, Helius, Blockstream
   - Pricing, rate limits, quick start instructions

7. **Bug Fixes**:
   - Fixed `queue.Sender` interface definition (app/services/queue/sqs.go)
   - Fixed `events.SQSBatchItemFailure` type name in main.go

8. **Updated Makefile**:
   - Added `swagger-install` target
   - Added `swagger-generate` target
   - Added `swagger-fmt` target

**Build Status**: ✅ Project compiles successfully

---

### Previous Work (Earlier in Project)

1. **Infrastructure Setup**:
   - Created comprehensive Makefile (50+ commands)
   - Added Docker Compose (PostgreSQL, Redis, PgAdmin)
   - Created multi-stage Dockerfile
   - Added environment file templates

2. **Initial Commit**:
   - Full project scaffolding
   - All core services implemented
   - Unit tests for all services
   - HMAC authentication middleware

3. **Goravel Rollback** (Rejected Approach):
   - Initially attempted Goravel framework integration
   - User chose to keep lightweight architecture instead
   - Rolled back all Goravel dependencies
   - Deleted unused config/ directory

---

## Known Issues

### 1. Incomplete Swagger Annotations

**Status**: 🚧 In Progress
**Description**: Only 4 of ~15 controller methods have Swagger annotations.
**Impact**: Swagger UI shows incomplete API documentation.
**Fix**: Add godoc comments to remaining methods in `app/http/controllers/controller.go`.

### 2. Missing Database Migrations

**Status**: ❌ Blocked
**Description**: No SQL migration files exist for database schema.
**Impact**: Cannot initialize database schema locally.
**Fix**: Create migration files in `migrations/` directory.
**Example**:
```sql
-- migrations/001_initial_schema.up.sql
CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id VARCHAR(50) NOT NULL,
    label VARCHAR(255),
    master_public_key TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);
```

### 3. Missing SAM Template

**Status**: ❌ Blocked
**Description**: No `template.yaml` for AWS SAM deployment.
**Impact**: Cannot deploy to AWS Lambda.
**Fix**: Create SAM template defining 4 Lambda functions, API Gateway, EventBridge, and SQS.

### 4. RPC URL Validation

**Status**: 🐛 Bug
**Description**: Application doesn't validate RPC URLs at startup.
**Impact**: Silent failures if RPC endpoints are unreachable.
**Fix**: Add health check in `app/services/chain/rpc.go` to verify connectivity.

---

## Next Steps

### High Priority

- [ ] **Complete Swagger Annotations**
  - Add godoc comments to all controller methods
  - Define request/response schemas
  - Regenerate docs: `make swagger-generate`

- [ ] **Create Database Migrations**
  - Write SQL migration files
  - Add migration commands to Makefile
  - Document migration workflow

- [ ] **Test Local Development Flow**
  - Start Docker services: `make docker-up`
  - Run migrations: `make migrate-up`
  - Start API: `make run`
  - Test Swagger UI: http://localhost:8080/swagger/index.html
  - Run tests: `make test`

- [ ] **Create SAM Template**
  - Define all 4 Lambda functions
  - Configure API Gateway integration
  - Set up EventBridge schedule
  - Define SQS queues with DLQs

### Medium Priority

- [ ] **Add Integration Tests**
  - End-to-end API tests
  - Blockchain adapter integration tests
  - Database transaction tests

- [ ] **Implement Remaining Endpoints**
  - Wallet details (GET /v1/wallets/{id})
  - Address generation (POST /v1/wallets/{id}/addresses)
  - Withdrawal creation (POST /v1/withdrawals)
  - Webhook management

- [ ] **Add Logging & Monitoring**
  - Structured logging with slog
  - AWS CloudWatch integration
  - Error tracking (Sentry)
  - Metrics (Prometheus/CloudWatch)

- [ ] **Security Hardening**
  - Rate limiting middleware
  - IP whitelisting
  - Request validation
  - Secrets rotation

### Low Priority

- [ ] **Performance Optimization**
  - Redis caching for chain data
  - Connection pooling
  - Batch RPC requests

- [ ] **Documentation**
  - API usage examples
  - Postman collection
  - Architecture diagrams
  - Deployment guide

- [ ] **CI/CD Pipeline**
  - GitHub Actions workflow
  - Automated testing
  - Docker image builds
  - SAM deployments

---

## Troubleshooting

### Build Errors

**Error**: `command not found: swag`
**Fix**: Run `make swagger-install` or use:
```bash
go run github.com/swaggo/swag/cmd/swag@v1.8.12 init -g main.go --output ./docs
```

**Error**: `undefined: events.SQSBatchResponseFailure`
**Fix**: Already fixed in main.go (use `SQSBatchItemFailure` instead).

### Runtime Errors

**Error**: `database connection failed`
**Fix**: Ensure PostgreSQL is running: `make docker-up`

**Error**: `RPC connection timeout`
**Fix**: Check RPC URLs in `.env.dev` are reachable:
```bash
curl https://eth-sepolia.public.blastapi.io
```

### Swagger Not Updating

**Error**: Changes to godoc comments not reflected in Swagger UI
**Fix**: Regenerate docs:
```bash
make swagger-generate
# Restart the server
make run
```

---

## Contributing

### Code Style

- Follow [Effective Go](https://golang.org/doc/effective_go)
- Run `make lint` before committing
- Write unit tests for all new features
- Add Swagger annotations to all API endpoints

### Commit Message Format

```
<type>: <short description>

<detailed description>

Examples:
- feat: add withdrawal cancellation endpoint
- fix: resolve race condition in deposit scanner
- docs: update RPC provider guide
- test: add integration tests for webhook service
```

### Pull Request Checklist

- [ ] Code compiles: `make build`
- [ ] Tests pass: `make test`
- [ ] Linter passes: `make lint`
- [ ] Swagger docs updated: `make swagger-generate`
- [ ] CLAUDE.md updated if architecture changed

---

## Resources

### Documentation

- **Swagger UI**: http://localhost:8080/swagger/index.html
- **RPC Providers Guide**: [docs/RPC_PROVIDERS.md](docs/RPC_PROVIDERS.md)
- **Go Ethereum Docs**: https://geth.ethereum.org/docs
- **Solana Go SDK**: https://github.com/gagliardetto/solana-go
- **AWS Lambda Go**: https://github.com/aws/aws-lambda-go

### External Services

- **Alchemy** (ETH/Polygon RPC): https://www.alchemy.com
- **Helius** (Solana RPC): https://www.helius.dev
- **QuickNode** (Multi-chain RPC): https://www.quicknode.com

### Team

- **Repository**: https://github.com/clouapp/wallet-service
- **Issues**: https://github.com/clouapp/wallet-service/issues
- **Contact**: support@vault.dev

---

**Document Version**: 1.0
**Last Updated**: March 17, 2026
**Maintained By**: Claude Code Assistant
