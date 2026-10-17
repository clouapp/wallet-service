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
| **Cache** | Redis (ElastiCache) | Address lookup O(1) + cópia dos checkpoints (o checkpoint fica no PostgreSQL) |
| **Database** | PostgreSQL (RDS) | ACID, JSONB |
| **Secrets** | AWS KMS | Envelope encryption |

### Por que SQS em vez de Redis pra filas

- DLQ nativa sem código — mensagens que falham N vezes vão pro dead letter queue
- Visibility timeout — se Lambda crashar, mensagem volta pra fila sozinha
- SQS trigga Lambda diretamente, sem polling code
- `ReportBatchItemFailures` — falha itens individuais sem perder o batch
- Custo zero no volume de POC

Redis permanece para: SET de endereços (address matching O(1)), cópia em cache dos block checkpoints e o lock de varredura por chain. O checkpoint durável de cada chain fica no PostgreSQL (`deposit_scan_checkpoints`).

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
# Com Postgres local; cria e usa somente vault_test
make test

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

**`tests/mocks/testdb.go`** — Usa a conexão isolada definida em `.env.testing`, recusa bancos sem sufixo `_test`, limpa e recria somente o schema de testes e fornece fixtures `InsertWallet`, `InsertAddress`, `InsertTransaction`, `InsertWebhookConfig`.

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
| `GET` | `/v1/transactions` | Listar transações (filtros); com `updated_since`/`cursor`, feed de alterações (ver "Reconciliação") |
| `GET` | `/v1/transactions/:id` | Detalhes da transação |
| `GET` | `/v1/users/:ext_id/transactions` | Transações de um usuário |
| `POST` | `/api/v1/webhooks` | Registrar webhook endpoint da conta do token |
| `GET` | `/api/v1/webhooks` | Listar webhooks da conta do token |
| `PATCH` | `/api/v1/webhooks/:webhookId` | Alterar eventos / `is_active` / reivindicar config legada |

---

## Webhooks de depósito

`deposit.pending`, `deposit.confirming`, `deposit.confirmed` e `deposit.failed` usam a mesma assinatura e o
mesmo escopo dos saques: cada config só recebe depósitos de wallets da própria conta (ou da própria wallet);
configs legadas sem dono recebem tudo. Deduplicação por (config, tipo, transação); em `deposit.confirming`, por
(config, transação, número de confirmações).

```json
{
  "id": "<delivery uuid>",
  "type": "deposit.confirmed",
  "created_at": "2026-10-01T12:00:00Z",
  "data": {
    "id": "<id da transação>",
    "transaction_id": "<id da transação>",
    "wallet_id": "cd8435dc-...",
    "chain": "polygon",
    "tx_hash": "0x...",
    "to_address": "0x...",
    "asset": "USDC",
    "token_contract": "0x...",
    "amount": "25",
    "amount_format": "decimal",
    "amount_base_units": "25000000",
    "decimals": 6,
    "confirmations": 3,
    "required_confirmations": 3,
    "finality": "finalized_tag",
    "status": "confirmed"
  }
}
```

- `deposit.pending`, `deposit.confirmed` e `deposit.failed` chegam uma vez por depósito. `deposit.confirming`
  informa o progresso e pode chegar várias vezes, cada uma com `confirmations` maior: é emitido quando o depósito
  passa de `pending` a `confirming` e de novo sempre que a contagem gravada sobe numa passagem do rastreador de
  confirmações (ou da etapa de confirmação de uma varredura), desde que a contagem anterior ainda estivesse abaixo
  de `required_confirmations`. No máximo um por passagem, com a contagem dessa passagem (as contagens
  intermediárias entre duas passagens não são enviadas), e nenhum depois que a contagem atinge
  `required_confirmations`: em `finalized_tag`/`solidified` o depósito pode continuar em `confirming` acima desse
  número até o bloco ficar final, e o evento seguinte é `deposit.confirmed`. Cada contagem é uma entrega distinta,
  com `id` e `X-Vault-Delivery-Id` próprios, e a mesma contagem não é enviada duas vezes à mesma config; a ordem
  de chegada, porém, não é garantida. O consumidor deve tratar `confirmations` como monotônico (ignorar um valor
  menor ou igual ao já registrado), não notificar o jogador a cada evento e creditar apenas em
  `deposit.confirmed`. `data.id` e `data.transaction_id` continuam sendo o id da transação em todos os eventos.
- `finality` diz qual regra decide o `status`: `confirmations` (confirmado quando `confirmations` atinge
  `required_confirmations`), `finalized_tag` (EVM: confirmado quando o bloco do depósito está no bloco `finalized`
  do nó ou abaixo dele, com pelo menos 1 confirmação) ou `solidified` (TRON: o mesmo com o bloco solidificado).
  Nas duas últimas, `required_confirmations` é apenas a defasagem típica da finalidade, para exibir "n/N
  confirmações"; `confirmations` continua a contar e pode passar desse número antes da confirmação (ou o
  depósito pode confirmar antes dele). Padrões: `polygon`, `tpolygon`, `bsc`, `tbsc` em `finalized_tag`
  (3, 3, 2, 2); `tron`, `ttron` em `solidified` (19); as demais chains em `confirmations`. O mesmo campo está em
  `GET /api/v1/chains`. Cada depósito guarda a regra com que foi registrado; payloads de depósitos anteriores
  trazem `confirmations`.
- Contrato antigo (até 2026-10-01): `data` era a linha `transactions` crua e `amount` vinha em **unidades base**
  sem `decimals` — ambíguo. Agora `amount` é sempre decimal (`amount_format: "decimal"`) e o valor exato está em
  `amount_base_units` + `decimals`. O consumidor deve converter a partir de `amount_base_units`/`decimals` com
  aritmética decimal exata e rejeitar payloads sem esses campos.
- Se os decimais do ativo não forem conhecidos, o evento não é emitido (fica no log) em vez de sair ambíguo.
- XRP (`xrp`/`txrp`): `destination_tag` (inteiro 0..4294967295) vem ao lado de `to_address` quando o pagamento
  recebido tinha tag, e também fica em `transactions.destination_tag`; é omitido sem tag e nas demais chains. Um
  depósito XRP é um `Payment` com resultado `tesSUCCESS` num ledger validado, pelo valor de `delivered_amount` em drops
  (um pagamento parcial credita o que chegou, não o `Amount` pedido); pagamentos `tec…`, moedas emitidas (IOU),
  pagamentos de uma conta para si mesma e os demais tipos de transação não são depósitos. A varredura periódica só roda com
  `xrp`/`txrp` em `LOCAL_DEPOSIT_SCAN_CHAINS`.

## Reconciliação: feed de alterações de `GET /api/v1/transactions`

Para recuperar webhooks perdidos, `GET /api/v1/transactions` aceita `updated_since` (RFC 3339, com `Z` ou
offset codificado na URL) e `cursor`. Com qualquer um dos dois, a lista deixa de ser paginada por offset:

- ordem `(updated_at, id)` crescente, a partir de `updated_since` (inclusive) ou estritamente depois do
  `cursor` (vale o que vier depois), com os mesmos filtros `chain`, `type`, `status` e `user_id`;
- `limit` padrão 50, máximo 200 (acima disso é limitado); `offset` diferente de 0 é recusado;
- o envelope continua `{"data","limit","offset","total"}` (com `offset: 0` e `total` contando as linhas a
  partir do início da página) e ganha `next_cursor`, `null` na última página. Para a página seguinte,
  repita a chamada com `cursor=<next_cursor>` e os mesmos filtros;
- sem `updated_since` e sem `cursor`, nada muda (offset, mais recentes primeiro, sem `next_cursor`);
- erros: 400 `invalid_request` com `updated_since must be an RFC 3339 timestamp`, `cursor is invalid`,
  `limit must be a positive integer` ou `offset cannot be combined with updated_since or cursor`.

Toda escrita numa transação (status, confirmações, falha, taxa, bloco) move `updated_at`, então a mesma
transação reaparece depois de cada mudança: o crédito precisa ser idempotente por `transaction_id`.
`updated_at` tem precisão de segundo e é gravado quando a escrita roda, não quando ela é confirmada no banco;
uma mudança pode ficar visível depois que o leitor já passou daquele segundo. Por isso o reconciliador não
deve guardar o cursor entre execuções: cada execução começa em `updated_since` = início da última execução
completa menos alguns minutos e segue `next_cursor` até `null`.

Cada item (também em `GET /api/v1/transactions/{id}` e `GET /api/v1/users/{external_id}/transactions`) traz,
com os mesmos nomes e formatos do `data` dos webhooks `deposit.*`: `transaction_id`, `amount_base_units`,
`decimals` e `required_confirmations`, além de `wallet_id`, `external_user_id`, `chain`, `tx_type`, `tx_hash`,
`log_index`, `to_address`, `asset`, `confirmations`, `status` e `destination_tag` (XRP). Diferenças em relação
ao webhook: `amount` continua em unidades base (não há `amount_format`), então quem reaproveita o parser do
webhook remove `amount` antes de validar; `token_contract` vem `""` em vez de `null` quando não há contrato;
`amount_base_units` e `decimals` são omitidos quando o valor não é um inteiro de unidades base ou os decimais
do ativo não são conhecidos (o item não deve ser creditado).

```json
{
  "data": [
    {
      "id": "7d3c9f3e-2b1a-4c5d-8e6f-0a1b2c3d4e5f",
      "transaction_id": "7d3c9f3e-2b1a-4c5d-8e6f-0a1b2c3d4e5f",
      "wallet_id": "cd8435dc-...",
      "external_user_id": "user-42",
      "chain": "polygon",
      "tx_type": "deposit",
      "tx_hash": "0x...",
      "log_index": 3,
      "to_address": "0x...",
      "asset": "USDC",
      "amount": "25000000",
      "amount_base_units": "25000000",
      "decimals": 6,
      "confirmations": 128,
      "required_confs": 128,
      "required_confirmations": 128,
      "status": "confirmed",
      "updated_at": "2026-10-11 12:00:07"
    }
  ],
  "limit": 200,
  "next_cursor": null,
  "offset": 0,
  "total": 1
}
```

## Webhooks de saque

Assinatura e entrega iguais às de `deposit.confirmed`: corpo JSON assinado com HMAC-SHA256 do corpo bruto
no header `X-Vault-Signature` (hex), mais `X-Vault-Event`, `X-Vault-Delivery-Id` e `X-Vault-Timestamp`.
Cada config só recebe eventos de wallets da própria conta (configs legadas sem dono recebem tudo).

| Evento | Quando |
|---|---|
| `withdrawal.broadcast` | O saque foi assinado e transmitido; `tx_hash` presente |
| `withdrawal.confirmed` | A transação foi confirmada pela mesma regra do depósito na chain: `required_confirmations` em `confirmations`, bloco final em `finalized_tag`/`solidified` |
| `withdrawal.failed` | O saque falhou antes de transmitir; só traz o código público em `failure_code` |

`withdrawal.broadcasting` continua existindo como evento interno legado do sweep e não é assinável.

```json
{
  "id": "<delivery uuid, igual ao X-Vault-Delivery-Id>",
  "type": "withdrawal.confirmed",
  "created_at": "2026-09-30T12:00:00Z",
  "data": {
    "withdrawal_id": "1c629829-...",
    "idempotency_key": "1c629829-...",
    "wallet_id": "cd8435dc-...",
    "transaction_id": "<id da transação no Macro Wallets ou null>",
    "chain": "polygon",
    "asset": "USDC",
    "token_contract": "0x...",
    "amount": "3",
    "amount_base_units": "3000000",
    "decimals": 6,
    "destination_address": "0x...",
    "tx_hash": "0x...",
    "confirmations": 128,
    "required_confirmations": 128,
    "status": "confirmed",
    "failure_code": null,
    "occurred_at": "2026-09-30T12:00:00Z"
  }
}
```

- `amount` é decimal (string); `amount_base_units` é inteiro na menor unidade do ativo (string), com `decimals`.
- `failure_code` ∈ `insufficient_funds`, `wallet_not_gas_ready`, `unsupported_chain`, `sweep_limit_exceeded`,
  `invalid_passphrase`, `passphrase_too_short`, `concurrent_withdrawal`, `too_many_attempts`, `internal_error`,
  e no XRP `destination_below_reserve` (destino inexistente recebendo menos que a reserva base),
  `destination_tag_required` (destino exige destination tag e o saque não enviou `destination_tag`),
  `destination_tag_not_supported` (`destination_tag` numa chain que não é XRP), `expired` (pagamento passou do
  `LastLedgerSequence` sem entrar num ledger validado) e `onchain_<resultado em minúsculas>` (pagamento validado com
  resultado `tec…`, ex. `onchain_tecno_dst_insuf_xrp`; a fee foi paga). Nas chains EVM, uma transação minerada com
  recibo de status `0x0` termina com `onchain_out_of_gas` (consumiu todo o limite de gas) ou `onchain_reverted`
  (revertida); a fee foi paga e o valor não foi transferido.
- `destination_tag` (inteiro 0..4294967295, só XRP) vem ao lado de `destination_address` quando o saque tem tag;
  omitido nos demais.
- Deduplicação: no máximo um evento por (config, tipo, saque) — índice único `webhook_events_subject_dedup_unique`.
- Retentativas: até `max_attempts` (10) com backoff exponencial (10s → 10min no modo local; SQS em produção).
- Reparo: o confirmation tracker também reprocessa saques `broadcast` cuja transação já está `confirmed`,
  então uma confirmação perdida é emitida na próxima rodada.

### Modo local

Sem `WEBHOOK_QUEUE_URL`, o servidor HTTP local roda dois workers (`LOCAL_WORKERS_ENABLED`, padrão `true`):
um tracker só de saques (`LOCAL_CONFIRMATION_INTERVAL_SECONDS`, padrão 30s — depósitos continuam esperando o
`confirmation_tracker` real) e um entregador da fila `webhook_events` (`LOCAL_WEBHOOK_DELIVERY_INTERVAL_SECONDS`,
padrão 5s). Os modos Lambda não mudam.

---

## Chains Suportadas

| Chain | ID | Native | Tokens | Confs | Finalidade | Scan Interval |
|---|---|---|---|---|---|---|
| Ethereum | `eth` | ETH | USDT, USDC | 12 | `confirmations` | 15s |
| Polygon | `polygon` | POL | USDT, USDC | 3 (defasagem) | `finalized_tag` | 5s |
| Solana | `sol` | SOL | USDT, USDC | 1 | `confirmations` | 5s |
| Bitcoin | `btc` | BTC | — | 6 | `confirmations` | 60s |
| BNB Smart Chain | `bsc` | BNB | USDT, USDC | 2 (defasagem) | `finalized_tag` | — |
| TRON | `tron` | TRX | USDT | 19 (defasagem) | `solidified` | — |

Com `finalized_tag`/`solidified` a transação é confirmada quando o bloco é final na própria chain (medido em
2026-10-11: o bloco `finalized` da Amoy fica 1 a 2 blocos atrás do topo, o da BSC testnet 1 bloco, o solidificado
da Nile 18 blocos); Ethereum, Base e Arbitrum seguem contando confirmações porque o bloco `finalized` delas fica
cerca de 18 minutos atrás do topo.

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
