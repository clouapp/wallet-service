# Vault — Custody-as-a-Service (POC)

## Números do Projeto

| Métrica | Valor |
|---|---|
| Arquivos totais | 37 |
| Linhas totais | 5.723 |
| Linhas de source | ~3.450 |
| Linhas de teste | 2.269 |
| Funções de teste | 111 |
| Ratio teste/source | ~66% |

---

## Stack

| Componente | Escolha | Motivo |
|---|---|---|
| **Runtime** | Go 1.22 | Performance, ecosystem crypto |
| **API** | AWS Lambda + API Gateway | Serverless, pay-per-use |
| **Deploy** | SAM CLI (`template.yaml`) | IaC nativo AWS, `sam deploy` |
| **Filas** | SQS + DLQ nativa | Retry automático, sem Redis pra filas |
| **Scheduling** | EventBridge → Lambda | Block scanning periódico por chain |
| **Cache** | Redis (ElastiCache) | Address lookup O(1) + checkpoints |
| **Database** | PostgreSQL (RDS) | ACID, JSONB |
| **Secrets** | AWS KMS | Envelope encryption |

### Por que SQS em vez de Redis pra filas

- DLQ nativa sem código — mensagens que falham N vezes vão pro dead letter queue
- Visibility timeout — se Lambda crashar, mensagem volta pra fila sozinha
- SQS trigga Lambda diretamente, sem polling code
- `ReportBatchItemFailures` — falha itens individuais sem perder o batch
- Custo zero no volume de POC

Redis permanece para: SET de endereços (address matching O(1)) e block checkpoints.

---

## Arquitetura

```
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  ┌──────────┐     ┌─────────────┐     ┌────────────────────┐    │
│  │ API      │◄────│ API Gateway │◄────│ Client (seu backend)│    │
│  │ Lambda   │     └─────────────┘     └────────────────────┘    │
│  └────┬─────┘                                                    │
│       │ enqueue                                                  │
│       ▼                                                          │
│  ┌──────────────────────────────────────────────────────────┐    │
│  │                     SQS Queues                           │    │
│  │  ┌───────────────┐         ┌────────────────────┐        │    │
│  │  │ Webhook Queue │         │ Withdrawal Queue   │        │    │
│  │  │ → 10 retries  │         │ → 3 retries        │        │    │
│  │  │ → DLQ (14d)   │         │ → DLQ (14d)        │        │    │
│  │  └──────┬────────┘         └─────────┬──────────┘        │    │
│  └─────────┼────────────────────────────┼───────────────────┘    │
│            ▼                            ▼                        │
│  ┌──────────────────┐     ┌──────────────────────┐               │
│  │ Webhook Worker   │     │ Withdrawal Worker    │               │
│  │ Lambda           │     │ Lambda               │               │
│  │ • HMAC sign      │     │ • Build TX           │               │
│  │ • HTTP POST      │     │ • Sign (KMS)         │               │
│  └──────────────────┘     │ • Broadcast          │               │
│                           └──────────────────────┘               │
│  ┌──────────────────────────────────────────────────┐            │
│  │ Deposit Scanner Lambda                           │            │
│  │ • EventBridge schedule (5-60s per chain)         │            │
│  │ • Scans blocks, matches addresses via Redis SET  │            │
│  └──────────────────────────────────────────────────┘            │
│                                                                  │
│  ┌──────────────┐   ┌──────────────┐                             │
│  │ PostgreSQL   │   │ Redis        │                             │
│  └──────────────┘   └──────────────┘                             │
└──────────────────────────────────────────────────────────────────┘
```

### Lambda Functions

| Função | Trigger | Timeout | Descrição |
|---|---|---|---|
| `vault-api` | API Gateway | 29s | Todas as rotas REST |
| `vault-deposit-scanner` | EventBridge | 60s | Scanneia blocos por chain |
| `vault-webhook-worker` | SQS (batch=5) | 30s | Entrega webhooks com HMAC |
| `vault-withdrawal-worker` | SQS (batch=1) | 60s | Build → Sign → Broadcast |

### Padrão: Single Binary, Multiple Modes

```go
switch os.Getenv("LAMBDA_MODE") {
case "deposit_scanner":   → EventBridge handler
case "webhook_worker":    → SQS handler
case "withdrawal_worker": → SQS handler
default:                  → API Gateway handler (Gin)
}
```

---

## Estrutura do Projeto

```
vault/
├── main.go                              # Lambda entrypoint (single binary, multi-mode)
├── template.yaml                        # SAM — toda infra como código
├── Makefile                             # build, deploy, logs, dlq-check
├── go.mod
│
├── app/
│   ├── http/
│   │   ├── controllers/
│   │   │   ├── controller.go            # HTTP handlers (thin)
│   │   │   └── controller_test.go       # 18 tests — full HTTP cycle
│   │   └── middleware/
│   │       ├── auth.go                  # HMAC auth + logger
│   │       └── auth_test.go             # 8 tests — auth edge cases
│   │
│   ├── models/
│   │   └── models.go                    # DB models (sqlx tags)
│   │
│   ├── providers/
│   │   └── container.go                 # DI container (boot per cold start)
│   │
│   └── services/
│       ├── chain/
│       │   ├── registry.go              # Chain + token registry
│       │   ├── registry_test.go         # 7 tests
│       │   ├── rpc.go                   # Generic JSON-RPC 2.0 client
│       │   ├── rpc_test.go              # 8 tests — httptest mock RPC
│       │   ├── evm.go                   # Shared ETH/Polygon adapter
│       │   ├── evm_test.go              # 6 tests — validation, hex, encoding
│       │   ├── solana.go                # SOL adapter
│       │   ├── bitcoin.go               # BTC adapter
│       │   └── adapters_test.go         # 5 tests — SOL/BTC validation
│       │
│       ├── queue/
│       │   ├── sqs.go                   # SQS client wrapper
│       │   └── sqs_test.go              # 5 tests
│       │
│       ├── wallet/
│       │   ├── service.go               # Wallet + address generation
│       │   └── service_test.go          # 11 tests — CRUD, derivation, atomicity
│       │
│       ├── deposit/
│       │   ├── service.go               # Block scanning + confirmations
│       │   └── service_test.go          # 6 tests — matching, dedup, tokens, confs
│       │
│       ├── webhook/
│       │   ├── service.go               # Enqueue (SQS) + Deliver (HTTP+HMAC)
│       │   └── service_test.go          # 8 tests — deliver, HMAC verify, failures
│       │
│       └── withdraw/
│           ├── service.go               # Request (API) + Execute (worker)
│           └── service_test.go          # 8 tests — idempotency, validation, filters
│
├── pkg/types/
│   ├── types.go                         # Chain interface + shared types
│   └── types_test.go                    # 5 tests
│
├── config/
│   └── config.go                        # Env config
│
├── database/migrations/
│   └── 001_init.sql                     # Full schema
│
├── tests/mocks/
│   ├── chain.go                         # MockChain + MockSQS + helpers
│   └── testdb.go                        # Real Postgres test DB + fixtures
│
├── samconfig.toml.example
└── .gitignore
```

---

## Testes

### Como rodar

```bash
# Com Postgres local
TEST_DATABASE_URL=postgres://vault:vault@localhost:5432/vault_test \
  go test ./... -v -race -count=1

# Testes que não precisam de DB (chain, types, middleware, rpc, sqs)
go test ./app/services/chain/... ./app/http/middleware/... ./pkg/types/... -v

# Coverage report
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Suíte Completa — 111 Test Functions

| Pacote | Testes | O que cobre |
|---|---|---|
| **chain/registry** | 7 | Register, get, not found, overwrite, tokens por chain, find token, AllTokens |
| **chain/evm** | 6 | Address validation (10 cases), identity ETH vs Polygon, ERC-20 encoding, hexToBigInt, fmtUnits |
| **chain/adapters** | 5 | SOL validation (10 cases com base58), BTC validation (9 cases bc1/1/3), identity, BTC token error |
| **chain/rpc** | 8 | Call success, params, RPC error, basic auth, connection refused, invalid JSON, nil output, incrementing IDs |
| **wallet/service** | 11 | Create, duplicate chain, unknown chain, list, get, not found, generate address, index increment, unique addresses, lookup, user list |
| **deposit/service** | 6 | No new blocks, unknown chain, address match → insert TX + user mapping, ignore unknown address, dedup by tx_hash, token deposits, confirmation state machine (pending → confirming → confirmed) |
| **withdraw/service** | 8 | Request success, idempotency (same ID returned), invalid address, wallet not found, token withdrawal + contract, unknown token, list with filters + limit, execute skip confirmed |
| **webhook/service** | 8 | Config CRUD (create, list, delete), deliver success + HMAC verification, deliver 500 failure + attempt increment, deliver unreachable, pgArray, enqueue with no configs, payload structure |
| **queue/sqs** | 5 | Send with empty URL (skip), nil client, WebhookMessage serialization, WithdrawalMessage serialization, QueueURLs |
| **middleware/auth** | 8 | Valid request, missing headers (4 combos), wrong API key, wrong signature, expired timestamp (-10min), future timestamp (+10min), invalid timestamp, tampered body |
| **controllers** | 18 | Health, list chains, create wallet, duplicate wallet, missing chain, list wallets, generate address, missing user_id, create withdrawal, missing fields, list transactions, filters, create webhook, list webhooks, wallet not found, invalid UUID, address not found, unauthenticated request |
| **types** | 5 | Event types uniqueness (9 events), status constants (4), DepositScanEvent, WebhookMessage fields, WithdrawalMessage fields |

### Test Infrastructure

**`tests/mocks/chain.go`** — MockChain implementa `types.Chain` com function fields overridáveis e call counters. MockSQS captura mensagens enviadas. Helpers `MakeTransfer` e `MakeTokenTransfer` para construir test data.

**`tests/mocks/testdb.go`** — Conecta a um Postgres real via `TEST_DATABASE_URL`, limpa e recria o schema por teste, fornece fixtures `InsertWallet`, `InsertAddress`, `InsertTransaction`, `InsertWebhookConfig`. Se DB indisponível, `t.Skip()`.

**`middleware/auth_test.go`** — Testa o HMAC auth end-to-end: computa signatures reais, valida que tampered body rejeita, valida janela de timestamp de 5 min em ambas direções.

**`controllers/controller_test.go`** — Integration tests completos: monta Gin router com middleware + controller + services, faz requests HTTP reais com auth assinado, valida status codes e response bodies.

**`webhook/service_test.go`** — Usa `httptest.NewServer` pra simular endpoints de webhook, verifica que o HMAC signature recebido bate com o esperado.

---

## API Endpoints

| Method | Path | Descrição |
|---|---|---|
| `GET` | `/health` | Health check (sem auth) |
| `GET` | `/v1/chains` | Listar chains suportadas |
| `POST` | `/v1/wallets` | Criar wallet para uma chain |
| `GET` | `/v1/wallets` | Listar wallets |
| `GET` | `/v1/wallets/:id` | Detalhes da wallet |
| `POST` | `/v1/wallets/:id/addresses` | Gerar endereço para usuário |
| `GET` | `/v1/wallets/:id/addresses` | Listar endereços da wallet |
| `GET` | `/v1/addresses/:address` | Buscar endereço (qual user?) |
| `GET` | `/v1/users/:ext_id/addresses` | Endereços de um usuário |
| `POST` | `/v1/wallets/:id/withdrawals` | Solicitar saque |
| `GET` | `/v1/transactions` | Listar transações (filtros) |
| `GET` | `/v1/transactions/:id` | Detalhes da transação |
| `GET` | `/v1/users/:ext_id/transactions` | Transações de um usuário |
| `POST` | `/v1/webhooks` | Registrar webhook endpoint |
| `GET` | `/v1/webhooks` | Listar webhooks |

---

## Chains Suportadas

| Chain | ID | Native | Tokens | Confs | Scan Interval |
|---|---|---|---|---|---|
| Ethereum | `eth` | ETH | USDT, USDC | 12 | 15s |
| Polygon | `polygon` | MATIC | USDT, USDC | 128 | 5s |
| Solana | `sol` | SOL | USDT, USDC | 1 | 5s |
| Bitcoin | `btc` | BTC | — | 3 | 60s |

### Adicionar chain EVM

```go
// container.go — uma instância de NewEVMLive com config diferente
registry.RegisterChain(chainpkg.NewEVMLive(chainpkg.EVMConfig{
    ChainIDStr: "arbitrum", ChainName: "Arbitrum One",
    NativeSymbol: "eth", NativeDecimal: 18,
    NetworkID: 42161, RPCURL: os.Getenv("ARBITRUM_RPC_URL"),
    Confirmations: 1,
}))
```

### Adicionar token

```go
// registry.go → AllTokens()
{Symbol: "dai", Name: "Dai", Contract: "0x6B175474E89...", Decimals: 18, ChainID: "eth"},
```

---

## Deploy

```bash
sam build && sam deploy --guided   # primeira vez
sam deploy                         # subsequentes
make logs-api                      # logs em real-time
make dlq-check                     # profundidade das DLQs
make dlq-replay-webhooks           # replay mensagens falhadas
```

### Custos (POC)

| Recurso | Custo/mês |
|---|---|
| Lambda (4 functions) | ~$5-20 |
| API Gateway | ~$3-10 |
| SQS (4 queues) | ~$1 |
| RDS Postgres (t4g.micro) | ~$15 |
| ElastiCache Redis (t4g.micro) | ~$12 |
| RPC Providers (free tier) | $0 |
| **Total** | **~$35-60/mês** |

---

## Implementado vs Stubbed

### Funcional

- Arquitetura Lambda + SQS + EventBridge (template.yaml completo)
- Single binary multi-mode
- DI Container com boot por cold start
- Chain registry + EVM adapter compartilhado (ETH + Polygon = 0 duplicação)
- Adapters com RPC real (block scanning, balance, ERC-20 log parsing)
- Wallet service com derivação atômica (FOR UPDATE lock)
- Deposit scanner stateless (Lambda-friendly, max 50 blocos por invocação)
- Webhook: enqueue → SQS → Lambda → HTTP POST com HMAC-SHA256
- Withdrawal: API → SQS → Lambda → build/sign/broadcast
- HMAC auth middleware com timestamp window
- SQL schema com indexes, constraints, CHECK clauses
- **111 testes unitários + integração com 66% ratio test/source**

### Stubbed (próximos passos)

1. HD Key Derivation — `DeriveAddress()` precisa de `go-ethereum/crypto` + `hdkeychain`
2. TX Signing — `SignTransaction()` precisa de RLP/secp256k1 (EVM), PSBT (BTC), ed25519 (SOL)
3. KMS Integration — master key fetch + envelope encryption
4. Hot wallet resolution — selecionar "from" address com saldo suficiente
5. Withdrawal policies — schema existe, engine não implementada
6. Nonce manager — tracking persistente para EVM chains
