# BaseAddress + Sweep Intra-Wallet (BitGo-style sobre MPC 2-of-2) — Design Spec

**Date:** 2026-04-18
**Status:** Draft
**Scope:** Backend (Go/Goravel) + Frontend (React/vinext)

---

## 1. Overview

Introduz o modelo **BitGo-like** sobre a infra MPC 2-of-2 existente: cada wallet passa a ter um **BaseAddress canônico** (= `wallet.deposit_address_id` atual) e múltiplos **child addresses** derivados por `external_user_id` para depósitos. Saques sempre consolidam em Base via um mecanismo de sweep **lazy** (só dispara quando o saque precisa). Um endpoint manual de consolidação é exposto para clientes que queiram antecipar custo de gas.

**A infra MPC 2-of-2 permanece inalterada** (ShareA encriptada com passphrase do cliente + ShareB no AWS Secrets Manager). O sweep herda as restrições de custódia: qualquer sweep precisa da passphrase do cliente — disparada pelo mesmo request que solicita saque ou pela chamada manual ao `/consolidate`.

### 1.1 Por que esse modelo

- Resolve fragmentação de saldo entre child addresses (problema aberto no modelo atual: saques só saíam do endereço passado na request, ignorando saldo de outros children).
- Mantém a promessa de segurança do MPC 2-of-2 intacta: **nada é automatizado em background** que requeira a passphrase do cliente.
- Preserva a semântica WaaS — `external_user_id` continua sendo tag em addresses; atribuição de saldo per-user é responsabilidade do cliente integrador (via webhooks).
- Alinha com o modelo BitGo de **baseAddress + consolidate manual + velocity limits**.

### 1.3 Escopo de chains — AMENDMENT 2026-04-18

**Descoberta durante implementação:** os adapters `SolanaLive` e `BitcoinLive` em `app/services/chain/` são **POC-level**. Métodos críticos retornam `"not implemented"` (SOL: `DeriveAddress`, `GetTokenBalance`, `SignTransaction`, `BroadcastTransaction`; BTC: `DeriveAddress`, `SignTransaction`; ambos: `BuildTransfer` retorna apenas metadata map, sem instruções/PSBT reais).

**Decisão:** a v1 deste spec cobre **apenas EVM (ETH + Polygon, incluindo testnets teth + tpolygon)**. SOL e BTC saem do escopo da v1. Ganham uma épica dedicada que cobre:
- `DeriveAddress` real por chain
- `BuildTransfer` com instruções SPL (SOL) / PSBT (BTC)
- `GetTokenBalance` com ATA resolution (SOL)
- `SignTransaction` + `BroadcastTransaction` reais
- Depois disso, `BuildSweep` para essas chains

**Como isso aparece no código:**
- `sweep.Service.PlanForWithdrawal` e `ConsolidateAll` retornam `ErrUnsupportedChain` para SOL/BTC wallets.
- `SolanaLive.BuildSweep` / `BitcoinLive.BuildSweep` retornam `ErrUnsupportedChain` (existem só pra satisfazer a interface `Chain`).
- `sweep.RefreshGasStatus` retorna `GasStatusSeeded` estático para BTC (sem gas concept) e `ErrUnsupportedChain` para SOL (até épica separada).
- Withdraw pre-existente continua funcionando como está para SOL/BTC (direct-from-child flow que o código atual já implementa em modo POC — nada muda).
- Frontend: gas-funding / consolidate / withdraw preview **só aparecem para wallets EVM**. Wallets SOL/BTC mantêm a UX atual.

### 1.2 Decisões-chave

| Decisão | Escolha | Racional |
|---|---|---|
| Modelo de chaves | Híbrido: mantém MPC 2-of-2 + adiciona BaseAddress e sweep intra-wallet | Caminho A. Sem backup key / recovery externo; zero ruptura do que está pronto. |
| Trigger de sweep | Sweep-on-withdraw (lazy) + endpoint manual de consolidação | Respeita MPC: nunca armazena passphrase server-side. |
| Gas funding | Base-funded puro; onboarding guia gas seed; estado `gas_ready` visível na UI | Zero subsídio da plataforma; UX compensada por guidance explícito. |
| Mecânica de sweep | **v1 EVM-only** (ETH + Polygon). SOL/BTC deferred — ver §1.3. | Adapters SOL/BTC hoje são POC (signing/build stubs); sweep pressupõe base funcional. |
| Política de saque | Opportunistic + dust threshold (policy #4) | Economiza $2-6/saque em ETH mainnet vs always-from-Base; evita dust-sweep em BTC. |
| Per-user balance | Não há atribuição server-side | Cliente WaaS monta ledger dele via webhooks `deposit.*`, `sweep.*`, `withdrawal.*`. |
| Migração | DB migration Goravel pura; sistema ainda não lançado | Sem feature flag, sem legacy mode, sem branching. |
| Rate limiting | Modelo BitGo: serialização por wallet + daily quota; sem req/min sliding window | Confirmação on-chain é o throttle natural. |

---

## 2. Arquitetura conceitual

```
Wallet (MPC 2-of-2, inalterado)
│
├── BaseAddress (== deposit_address_id, index 0)
│   ├── saldo native (gas)         → alimentado pelo cliente (gas seed UI)
│   └── saldo tokens consolidados  → crescimento via sweeps + destino final dos saques
│
└── Child[N] (index N, external_user_id="user_X")
    ├── recebe depósitos do end-user
    ├── não mantém gas permanente
    └── sweep → BaseAddress:
        • sob demanda durante withdraw (se Base isolado não cobre)
        • ou via endpoint /v1/wallets/:id/consolidate
```

### 2.1 Fluxos principais

**Depósito (inalterado conceitualmente):**

```
end-user → tx on-chain → child[N]
           scanner detecta + webhook deposit.pending/confirmed (com external_user_id)
```

**Saque (novo):**

```
cliente → POST /v1/wallets/:id/withdraw  (passphrase, amount, asset)
             │
             ▼
         PlanForWithdrawal(asset, amount)
             │
             ├── Base cobre isolado?        → Strategy=direct_from_base (1 tx)
             ├── Um child cobre isolado?    → Strategy=direct_from_child (1 tx)
             └── Senão:                     → Strategy=multi_sweep
                   1. greedy: menor subset de children que cobre
                   2. ignora children com saldo < dust_threshold
                   3. para cada child selecionado:
                      - EVM: tx gas_seed + tx sweep
                      - SOL: 1 tx (fee_payer=Base, closes ATA)
                      - BTC: PSBT consolidando UTXOs do child
                   4. tx final: Base → destino

         Todas as txs co-assinadas com ShareA (mesma passphrase),
         ShareA em memória só durante o request.
```

**Consolidação manual:**

```
cliente → POST /v1/wallets/:id/consolidate  (passphrase, asset)
         sweepa TODOS children com saldo >= dust_threshold → Base
         útil antes de saque grande, ou pra higiene operacional
```

---

## 3. Data model

Todas as mudanças de schema são expressas em **Goravel Schema builder** (`facades.Schema()`), nunca SQL cru. Os arquivos ficam em `database/migrations/` seguindo o padrão existente do projeto (struct com `Signature()`, `Up()`, `Down()`; registrada em `app/providers/migrations_service_provider.go`).

### 3.1 Migration: adicionar gas-readiness em `wallets`

Arquivo: `database/migrations/20260418000001_wallets_add_gas_status.go`

```go
func (r *M20260418000001WalletsAddGasStatus) Up() error {
    return facades.Schema().Table("wallets", func(table schema.Blueprint) {
        table.String("gas_status", 16).Default("unseeded").
            Comment("unseeded | seeded | low")
        table.Timestamp("gas_last_checked_at").Nullable()
        table.Integer("sweep_policy_version").Default(1)
        table.Index("gas_status")
    })
}
func (r *M20260418000001WalletsAddGasStatus) Down() error {
    return facades.Schema().Table("wallets", func(table schema.Blueprint) {
        table.DropColumn("gas_status")
        table.DropColumn("gas_last_checked_at")
        table.DropColumn("sweep_policy_version")
    })
}
```

Model delta em `app/models/wallet.go`:

```go
type Wallet struct {
    orm.Model
    // ... campos existentes ...
    GasStatus            string     `gorm:"type:varchar(16);not null;default:unseeded;index" json:"gas_status"`
    GasLastCheckedAt     *time.Time `gorm:"type:timestamptz" json:"gas_last_checked_at,omitempty"`
    SweepPolicyVersion   int        `gorm:"not null;default:1" json:"sweep_policy_version"`
}

const (
    GasStatusUnseeded = "unseeded"
    GasStatusSeeded   = "seeded"
    GasStatusLow      = "low"
)
```

- `gas_status` → exposto na API; UI usa para banners/badges e bloqueio de saque multi-sweep.
- `gas_last_checked_at` → evita hammering de RPC; refresh manual rate-limitado.
- `sweep_policy_version` → permite evoluir algoritmo sem breaking change pra wallets em produção.

### 3.2 Migration: parent/origin em `transactions`

Arquivo: `database/migrations/20260418000002_transactions_add_sweep_fields.go`

```go
func (r *M20260418000002TransactionsAddSweepFields) Up() error {
    return facades.Schema().Table("transactions", func(table schema.Blueprint) {
        table.Uuid("parent_transaction_id").Nullable().
            Comment("Withdrawal tx id if this row is a sweep or gas_seed triggered by a withdrawal")
        table.String("origin", 24).Nullable().
            Comment("user_request | sweep | gas_seed | manual_consolidation")
        table.Index("parent_transaction_id")
        table.Index("origin")
    })
}
func (r *M20260418000002TransactionsAddSweepFields) Down() error {
    return facades.Schema().Table("transactions", func(table schema.Blueprint) {
        table.DropColumn("parent_transaction_id")
        table.DropColumn("origin")
    })
}
```

Model delta em `app/models/transaction.go`:

```go
ParentTransactionID *uuid.UUID `gorm:"type:uuid;index" json:"parent_transaction_id,omitempty"`
Origin              string     `gorm:"type:varchar(24);index" json:"origin,omitempty"`

const (
    TxOriginUserRequest    = "user_request"
    TxOriginSweep          = "sweep"
    TxOriginGasSeed        = "gas_seed"
    TxOriginManualConsolid = "manual_consolidation"
)
```

Sweep txs apontam `parent_transaction_id` para o withdrawal que as disparou (ou NULL se vieram de `/consolidate` manual). `origin` rotula natureza da tx — webhooks e consumers filtram por este campo.

### 3.3 Migration: extensão de enum `tx_type`

Hoje o enum `transaction_type` contém `deposit`, `withdrawal`. Adicionar `sweep` e `gas_seed`.

Arquivo: `database/migrations/20260418000003_transactions_extend_tx_type_enum.go`

```go
func (r *M20260418000003TransactionsExtendTxTypeEnum) Up() error {
    // O enum Postgres 'transaction_type' é adicionado via SQL raw porque Goravel
    // Schema builder não expõe ALTER TYPE ADD VALUE. Padrão já usado no projeto
    // para enums (ver migrations 20260327000006_create_transactions_table).
    _, err := facades.DB().Exec(`ALTER TYPE transaction_type ADD VALUE IF NOT EXISTS 'sweep'`)
    if err != nil { return err }
    _, err = facades.DB().Exec(`ALTER TYPE transaction_type ADD VALUE IF NOT EXISTS 'gas_seed'`)
    return err
}
func (r *M20260418000003TransactionsExtendTxTypeEnum) Down() error {
    // Postgres não suporta DROP VALUE em tipos enum. Down é no-op; revert manual se necessário.
    return nil
}
```

Constantes em `app/models/transaction.go`:

```go
const (
    TxTypeDeposit    = "deposit"
    TxTypeWithdrawal = "withdrawal"
    TxTypeSweep      = "sweep"
    TxTypeGasSeed    = "gas_seed"
)
```

Sweeps e gas seeds são transações on-chain com `tx_hash` real e confirmações — aparecem no histórico. Cliente filtra por `origin` ou `tx_type` se quiser esconder no dashboard dele.

### 3.4 Migration: `sweep_limits` em `accounts`

Arquivo: `database/migrations/20260418000004_accounts_add_sweep_limits.go`

```go
func (r *M20260418000004AccountsAddSweepLimits) Up() error {
    return facades.Schema().Table("accounts", func(table schema.Blueprint) {
        table.Json("sweep_limits").Nullable().
            Comment("Per-account overrides for sweep rate limits and velocity caps")
    })
}
func (r *M20260418000004AccountsAddSweepLimits) Down() error {
    return facades.Schema().Table("accounts", func(table schema.Blueprint) {
        table.DropColumn("sweep_limits")
    })
}
```

Estrutura esperada do JSON (todos os campos opcionais; defaults globais aplicam quando ausentes):

```json
{
  "max_addresses_per_request": { "evm": 100, "sol": 25, "btc": 100 },
  "max_consolidate_requests_per_day": 50,
  "daily_withdraw_cap_usd": "50000.00"
}
```

### 3.5 Migration: thresholds de gas + dust em `chains`

**Correção importante:** a tabela `chain_resources` existente é dedicada a URLs (RPC/explorer/etc) — não é um store de config genérica. Thresholds ficam em colunas novas na tabela `chains`.

Arquivo: `database/migrations/20260418000005_chains_add_sweep_thresholds.go`

```go
func (r *M20260418000005ChainsAddSweepThresholds) Up() error {
    return facades.Schema().Table("chains", func(table schema.Blueprint) {
        table.Text("gas_readiness_threshold_raw").Nullable().
            Comment("Min native balance (raw units) on BaseAddress to consider wallet gas-ready")
        table.Text("dust_threshold_native_raw").Nullable().
            Comment("Min native balance on a child to be considered sweepable (raw units)")
        table.Decimal("dust_threshold_usd").Places(4).Total(16).Nullable().
            Comment("Min USD-equivalent for token dust filter; resolved via price service")
    })
}
func (r *M20260418000005ChainsAddSweepThresholds) Down() error {
    return facades.Schema().Table("chains", func(table schema.Blueprint) {
        table.DropColumn("gas_readiness_threshold_raw")
        table.DropColumn("dust_threshold_native_raw")
        table.DropColumn("dust_threshold_usd")
    })
}
```

### 3.6 Seeders

O seeder existente `database/seeds/chains_seeder.go` (ou equivalente) deve ser atualizado para popular os novos campos com os defaults da §10. Novo arquivo se a granularidade for fina demais: `database/seeds/sweep_thresholds_seeder.go` e registrar no `DatabaseSeeder` (ver §12 rollout).

### 3.7 Tabelas não alteradas

`addresses`, `wallet_asset_balance`, `wallet_utxo`, `wallet_sync_state`: sem mudanças de schema. A agregação de saldo por wallet em `wallet_asset_balance` continua somando Base + children (já faz isso via sync).

### 3.8 Registro das migrations

Todas as migrations novas precisam ser registradas em `app/providers/migrations_service_provider.go` seguindo o padrão atual. Consulta final desse arquivo deve incluir os 5 novos structs em ordem cronológica.

---

## 4. Service layer

Convenções Goravel que **todas as alterações de código** neste spec seguem:

- **ORM queries**: `facades.Orm().Query().Where(...).Find(&model)` — nunca GORM puro, nunca SQL cru (exceto o `ALTER TYPE` da §3.3 por limitação do framework).
- **Cache / Redis**: o projeto já usa `go-redis` diretamente em serviços críticos (locks, counters). Mantemos esse padrão pra sweep (consistency com `withdraw/service.go` e `deposit/service.go`).
- **Config**: `facades.Config().GetString/GetInt/...` em `config/security.go`, nunca hardcoded.
- **Events**: emissão via `facades.Event().Job(...).Dispatch()` pros novos eventos (§7).
- **Logging**: `slog` (já é o padrão atual) com context fields (`chain`, `wallet_id`, `tx_id`).
- **Errors**: sentinel errors no pacote (ex: `sweep.ErrWalletNotGasReady`) + mapeamento HTTP no controller, como já acontece em `withdraw/service.go`.
- **Testing**: tabela-driven tests + mocks em `tests/` (ex: `tests/chain_mock.go`), seguindo padrão existente.
- **Repositories**: toda interação com DB passa pelas interfaces em `app/repositories/` (novos métodos adicionados às interfaces existentes; nenhum `facades.Orm()` direto no service layer).

### 4.1 Novo: `app/services/sweep/`

```
app/services/sweep/
├── service.go          // interface + construtor
├── planner.go          // PlanForWithdrawal + greedy selection
├── executor.go         // executa plano (assina, broadcasta, persiste)
├── limits.go           // carrega/aplica sweep_limits da account
├── gas_readiness.go    // cálculo + persistência de gas_status
└── service_test.go
```

Interface pública:

```go
type Service interface {
    PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int) (*Plan, error)
    ExecutePlan(ctx context.Context, plan *Plan, shareA []byte, withdrawalTxID uuid.UUID) (*Result, error)
    ConsolidateAll(ctx context.Context, walletID uuid.UUID, asset string, passphrase string) (*Result, error)
    RefreshGasStatus(ctx context.Context, walletID uuid.UUID) (*GasStatus, error)
}

type Plan struct {
    WalletID       uuid.UUID
    Chain          string
    Asset          string
    Amount         *big.Int
    Strategy       Strategy              // direct_from_base | direct_from_child | multi_sweep | insufficient
    SourceAddress  *models.Address       // para direct_*
    Sweeps         []PlannedSweep        // para multi_sweep
    EstimatedGas   *big.Int              // total estimado em native
    ReachesTarget  bool
    DustIgnored    []AddressBalance      // transparência para UI
    BaseBalance    *big.Int
}

type PlannedSweep struct {
    From        models.Address
    Amount      *big.Int                 // pode ser parcial no último child selecionado
    NeedsGas    bool                     // true → pré-tx de gas_seed em EVM
    GasAmount   *big.Int                 // quanto seed de gas enviar, se aplicável
}

type Result struct {
    WithdrawalTxID  uuid.UUID
    Sweeps          []CompletedSweep
    FinalWithdrawTx *models.Transaction  // nil se operação era só consolidate
    FailedStep      *FailedStep          // nil se tudo OK; senão carrega contexto para retry idempotente
}
```

**Planner — algoritmo:**

```
func PlanForWithdrawal(wallet, asset, amount):
    base_balance  := balance(wallet.Base, asset)
    children      := addresses_with_balance(wallet, asset, >= dust_threshold)
    dust_ignored  := addresses_with_balance(wallet, asset, < dust_threshold)

    if base_balance >= amount:
        return Plan{Strategy: direct_from_base, SourceAddress: Base}

    for child in children sorted desc by balance:
        if child.balance >= amount:
            return Plan{Strategy: direct_from_child, SourceAddress: child}

    # nenhum endereço sozinho cobre → multi-sweep greedy
    remaining := amount - base_balance
    sweeps    := []
    total     := base_balance
    for child in children sorted desc by balance:
        sweep_amount := min(child.balance, remaining)
        sweeps.append(PlannedSweep{From: child, Amount: sweep_amount, NeedsGas: chain_needs_gas_seed(wallet.Chain, child)})
        total      += sweep_amount
        remaining  -= sweep_amount
        if remaining <= 0: break

    if total < amount:
        return Plan{Strategy: insufficient, ReachesTarget: false, DustIgnored: dust_ignored}

    return Plan{Strategy: multi_sweep, Sweeps: sweeps, DustIgnored: dust_ignored}
```

**Executor — fluxo:**

```
func ExecutePlan(plan, shareA, withdrawalTxID):
    if plan.Strategy in {direct_from_base, direct_from_child}:
        return broadcast_single(plan.SourceAddress, plan.Amount, shareA)

    # multi_sweep
    for sweep in plan.Sweeps:
        if sweep.NeedsGas:
            persist_tx(origin=gas_seed, tx_type=gas_seed, parent_transaction_id=withdrawalTxID)
            broadcast(Base → child, sweep.GasAmount, shareA)

        persist_tx(origin=sweep, tx_type=sweep, parent_transaction_id=withdrawalTxID)
        broadcast(child → Base, sweep.Amount, shareA)

        # se falhar, FailedStep guarda índice; idempotência do withdraw reusa
        if err: return Result{FailedStep: step_info}

    # todos sweeps OK; broadcast do withdraw final
    final_tx := broadcast(Base → destination, plan.Amount, shareA)
    final_tx.ID = withdrawalTxID
    final_tx.Origin = user_request
    return Result{Sweeps: ..., FinalWithdrawTx: final_tx}
```

**Limites (BitGo-like):**

```
func checkLimits(account, wallet, chain, operation):
    limits := load_limits(account)                     # merge accounts.sweep_limits + defaults
    # 1. Serialização por wallet (in-flight consolidation)
    if redis.SetNX("vault:lock:consolidate:"+wallet.ID, "1", 60s) == false:
        return err "another consolidation in flight for this wallet"
    # 2. Daily quota por account
    key := "vault:quota:consolidate:"+account.ID+":"+today_utc
    if redis.Incr(key, ttl=24h) > limits.MaxConsolidateReqPerDay:
        return err "daily consolidation quota exceeded"
    # 3. Max addresses no plan
    if len(plan.Sweeps) > limits.MaxAddressesPerRequest[chain]:
        return err "exceeds max addresses per request; split operation"
```

**Gas readiness:**

```
func RefreshGasStatus(wallet):
    native_balance := chain_adapter.GetBalance(Base, native_asset)
    threshold      := chain_resource.gas_readiness_threshold(wallet.Chain)

    switch:
        native_balance == 0:            status = unseeded
        native_balance < threshold:     status = low
        else:                           status = seeded

    if status != wallet.gas_status:
        emit webhook wallet.gas_status.changed
    persist wallet.gas_status = status; wallet.gas_last_checked_at = now()
```

### 4.2 Alterado: `app/services/withdraw/service.go`

Remove a lógica atual de "lê `from_address` do request, assina daquele endereço específico". O novo fluxo delega ao `sweep.Service`:

```go
func (s *Service) Request(ctx context.Context, req WithdrawRequest) (*models.Transaction, error) {
    if len(req.Passphrase) < 12 { return nil, ErrPassphraseTooShort }

    // 1. Idempotency — retenta recupera estado
    if req.IdempotencyKey != "" {
        if existing := findByIdempotencyKey(req.IdempotencyKey); existing != nil {
            return s.resumeOrReturn(ctx, existing, req.Passphrase)
        }
    }

    // 2. Lock de withdrawal por wallet (evita dois withdraws concorrentes)
    lock := acquireRedisLock("vault:lock:withdrawal:"+req.WalletID, 60s)
    defer lock.release()

    wallet := walletRepo.FindByID(req.WalletID)
    amount := new(big.Int).SetString(req.Amount, 10)

    // 3. Plan
    plan, err := s.sweepSvc.PlanForWithdrawal(ctx, req.WalletID, req.Asset, amount)
    if err != nil { return nil, err }
    if !plan.ReachesTarget {
        return nil, InsufficientFundsErr(plan)
    }
    if plan.Strategy == MultiSweep && wallet.gas_status != "seeded" {
        return nil, ErrWalletNotGasReady
    }

    // 4. Rate limit
    if err := s.sweepSvc.CheckLimits(account, wallet, plan); err != nil { return nil, err }

    // 5. Decrypt ShareA (uma vez; reusada para todos os sweeps + withdraw final)
    shareA, err := decryptShareWithRateLimit(wallet, req.Passphrase)
    if err != nil { return nil, mapDecryptErr(err) }
    defer zero(shareA)

    // 6. Execute — persist withdrawalTxID up front para linkar sweeps via parent_transaction_id
    withdrawalTxID := uuid.New()
    createPendingWithdrawal(withdrawalTxID, req, plan)

    result, err := s.sweepSvc.ExecutePlan(ctx, plan, shareA, withdrawalTxID)
    if err != nil || result.FailedStep != nil {
        markPartial(withdrawalTxID, result)
        return nil, PartialFailureErr(result)   // cliente reusa idempotency key para retry
    }

    // 7. Webhooks emitidos por ExecutePlan: sweep.broadcast (N vezes) + withdrawal.broadcast

    return result.FinalWithdrawTx, nil
}
```

**Retry (`resumeOrReturn`):** se existing tem `FailedStep` marcado, re-plan e executa só o delta; retorna Response com status agregado (ver §6.3).

### 4.3 Alterado: `app/services/chain/` — extensão da interface `Chain`

```go
// pkg/types/chain.go
type Chain interface {
    // ... métodos existentes inalterados ...

    // BuildSweep constrói as txs para sweepar asset de `from` para `to` dentro da mesma wallet.
    // Retorna slice porque EVM pode precisar de [gas_seed, sweep]; SOL/BTC retornam 1 tx.
    BuildSweep(ctx context.Context, req SweepRequest) ([]UnsignedTx, error)

    // GasReadinessThreshold é o mínimo native que BaseAddress deve manter para operação saudável.
    // Valores carregados de chain_resources; este método retorna o cache em memória.
    GasReadinessThreshold() *big.Int

    // DustThreshold é o saldo mínimo (no próprio asset) para um child ser considerado sweepável.
    DustThreshold(asset string) *big.Int
}

type SweepRequest struct {
    From          string
    To            string
    Asset         string
    Amount        *big.Int          // para sweep parcial; nil = sweep total (saldo - gas_buffer)
    NativeBalance *big.Int          // saldo native do From; adapter decide se precisa gas_seed
    FeePayer      *string           // apenas SOL: quem paga a fee (typically Base)
}
```

**Implementações (mudanças principais):**

- `app/services/chain/evm.go` — `BuildSweep`: se `NativeBalance < estimated_gas`, retorna `[gas_seed_tx, sweep_tx]`; senão `[sweep_tx]`. Usa `eth_estimateGas` + 20% buffer.
- `app/services/chain/solana.go` — `BuildSweep`: **1 tx** com `feePayer=Base`, instruções = `[create_idempotent_ata(base)?, spl_transfer(child_ata, base_ata, amount), close_account(child_ata)?]`. `close_account` só se `Amount == total_balance_child` (recupera rent para Base).
- `app/services/chain/bitcoin.go` — `BuildSweep`: PSBT com todos UTXOs do child (filtra dust via `DustThreshold`), 1 output para Base, fee estimado via `estimatesmartfee` RPC do node próprio.

### 4.4 Inalterados

- `app/services/mpc/` — lógica de sign/decrypt continua como está.
- `app/services/wallet/service.go` — `CreateWallet`, `GenerateAddress`, etc., inalterados.
- `app/services/deposit/` — scanner detecta deposits em qualquer address (Base ou child) da mesma forma.
- `app/services/webhook/` — ganha tipos novos de evento (§6), mas delivery pipeline inalterado.

---

## 5. Mecânica de sweep por chain

### 5.1 EVM (ETH, POL)

**Nativo (ETH, POL nativo):**

```
1 tx:
  from:  child
  to:    Base
  value: child_balance - (21000 × gasPrice)   # deixa exatamente o buffer de fee
```

**Token (ERC-20):**

```
step 1 — gas seed (só se child.native < gas_necessário_para_tx_erc20):
  tx: Base → child, value = estimate_gas(erc20.transfer) × gasPrice × 1.2

step 2 — sweep:
  tx: child → erc20.transfer(Base, token_balance)
```

- Gas estimation: `eth_estimateGas` + 20% buffer. Se `eth_estimateGas` falhar, usa defaults hardcoded (50k para ERC-20, 21k nativo).
- `sweep_amount = child_token_balance` (sempre total em ERC-20; não faz sentido sweep parcial — gas é igual).
- Para multi-sweep onde só precisamos de uma parte do último child: ainda sweepa total; excesso fica no Base pro próximo saque.

### 5.2 Solana

**Vantagem do `fee_payer`:** Base paga a fee da tx do child → **1 tx só**.

```
Transaction:
  signers:       [child, Base]                  # ambos assinam (MPC co-sign para cada)
  fee_payer:     Base                           # gas sai do Base
  instructions:
    [create_ata_idempotent(base_ata, base, mint)]    # se Base ainda não tem ATA do token
    spl_token.transfer(
      source      = child_ata,
      destination = base_ata,
      owner       = child,
      amount      = child_spl_balance
    )
    [spl_token.close_account(child_ata, rent_dest=Base, owner=child)]  # se sweep total
```

- **ATA lazy creation**: `create_idempotent` é idempotente; nunca falha se já existir.
- **Close empty ATA**: só quando `Amount == total_balance`; recupera ~0.002 SOL de rent para Base. Configurável via `sweep_limits.sol.close_empty_ata` (default true).
- **Rent do child SOL account**: se child tinha SOL nativo e está sendo sweepado, precisa manter rent mínimo (~0.002 SOL) OU fechar o account. Estratégia v1: **manter rent**; sweep de SOL nativo do child leva `balance - rent_minimum`.
- Child ATAs para tokens sweepados são fechadas; child SOL account permanece ativa.

### 5.3 Bitcoin

**Sweep** = PSBT consolidando todos UTXOs elegíveis do child em um output para Base.

```
PSBT:
  inputs:  todos UTXOs do child onde utxo.value >= dust_threshold_btc
  outputs: [{ address: Base, value: sum(inputs) - fee }]
  fee:     estimated_fee_rate × estimated_vbytes
  sign:    MPC co-sign para cada input (um signing round por UTXO)
```

- **Fee rate source**: `estimatesmartfee` RPC do node próprio (Bitcoin Core ou Electrum).
- **Dust threshold BTC**: **10000 sats** (~$4 a $60k BTC). UTXOs menores são ignorados — sweepar custa mais em fee que o valor.
- **Múltiplos UTXOs por sweep**: cada UTXO é um input separado; BitGo limita a 200 UTXOs por tx (limite da rede). Respeitamos o mesmo limite.
- **Seleção para withdrawal multi-sweep**: algoritmo greedy por child (não por UTXO — cada child vira um sweep separado). UTXOs de dust dentro de um child elegível continuam sendo ignorados.

### 5.4 Matriz resumo

| Chain | Txs por sweep | Gas source | Especificidade |
|---|---|---|---|
| ETH / POL (nativo) | 1 | child paga | Leave gas buffer |
| ETH / POL (ERC-20) | 1 ou 2 (gas_seed condicional) | Base seeds, child paga | `eth_estimateGas` + 20% |
| SOL (SPL ou nativo) | 1 | **Base paga (fee_payer)** | `create_ata_idempotent`, optional close |
| BTC | 1 (PSBT com N UTXOs) | consumido do próprio input | Dust threshold 10k sats |

---

## 6. API surface

### 6.1 Endpoints novos

#### `POST /v1/wallets/:id/consolidate`

Dispara consolidação manual. Precisa de passphrase. Respeita rate limits.

```http
POST /v1/wallets/{id}/consolidate
Content-Type: application/json

{
  "passphrase": "...",
  "asset": "USDT",
  "idempotency_key": "optional"
}

200 OK:
{
  "plan_summary": {
    "children_swept": 3,
    "dust_ignored": 1,
    "total_amount": "450.0",
    "estimated_gas_cost": "0.0085 ETH"
  },
  "transactions": [
    { "tx_hash": "0xabc...", "origin": "gas_seed", "status": "confirming" },
    { "tx_hash": "0xdef...", "origin": "sweep",    "status": "confirming" },
    ...
  ]
}

422 wallet_not_gas_ready / insufficient_funds / sweep_limit_exceeded
```

#### `GET /v1/wallets/:id/gas-status`

```json
{
  "gas_status": "unseeded",
  "base_address": "0xABC...",
  "native_asset": "ETH",
  "native_balance_raw": "0",
  "native_balance_display": "0.0",
  "threshold_raw": "5000000000000000",
  "threshold_display": "0.005 ETH",
  "last_checked_at": "2026-04-18T12:00:00Z"
}
```

#### `POST /v1/wallets/:id/gas-check`

Force refresh (rate-limited — 1 por minuto por wallet).

#### `POST /v1/wallets/:id/withdraw/preview`

Retorna o plano sem executar — para UI mostrar custo/complexidade antes de pedir passphrase.

```json
Request:
{ "asset": "USDT", "amount": "1000.0" }

Response 200:
{
  "strategy": "multi_sweep",
  "reaches_target": true,
  "base_balance": "300.0",
  "sweeps_required": 2,
  "dust_ignored": [
    { "address": "0x...", "balance": "0.5", "reason": "below_dust_threshold" }
  ],
  "estimated_gas_total_native": "0.012 ETH",
  "estimated_gas_total_usd": "24.00"
}
```

### 6.2 Endpoints alterados

#### `POST /v1/wallets/:id/withdraw`

Request body inalterado (mantém `passphrase`, `to_address`, `amount`, `asset`, `idempotency_key`). O campo `from_address_id` **é removido** — a política de origem é determinística (ver §4.2). Tentativas de passar `from_address_id` são rejeitadas com `400 bad_request`.

Response ganha novos campos:

```json
{
  "transaction_id": "...",
  "tx_hash": "0xaaa...",
  "status": "confirming",
  "origin": "user_request",
  "strategy": "multi_sweep",
  "sweeps": [
    { "tx_id": "...", "tx_hash": "0xbbb...", "from": "child_1", "origin": "sweep" },
    { "tx_id": "...", "tx_hash": "0xccc...", "from": "child_2", "origin": "sweep" }
  ]
}
```

### 6.3 Resposta em retry com mesmo `idempotency_key`

Quando cliente retenta saque que falhou parcialmente (ex: sweep 3/5 falhou):

```json
200 OK:
{
  "transaction_id": "...",
  "status": "partial_failure_recoverable",
  "completed_sweeps": [
    { "from": "child_1", "tx_hash": "0xabc...", "confirmed": true },
    { "from": "child_2", "tx_hash": "0xdef...", "confirmed": true }
  ],
  "pending_sweeps": [
    { "from": "child_3", "last_attempt_error": "rpc timeout", "retry_ready": true }
  ],
  "withdrawal_step": "awaiting_sweep_completion",
  "next_action": "retry_same_idempotency_key"
}
```

O planner no retry recalcula do estado atual — children já sweepados não reaparecem no plano. Idempotência em dois níveis: do withdraw final (mesmo `tx_hash` não rebroadcast) e de cada sweep (não re-sweepa child já vazio).

### 6.4 Códigos de erro novos

```
422 wallet_not_gas_ready
    { "error": "wallet_not_gas_ready",
      "base_address": "0x...",
      "threshold": "0.005 ETH",
      "current": "0 ETH",
      "action": "fund_base_address" }

422 insufficient_funds
    { "error": "insufficient_funds",
      "requested": "1000",
      "wallet_total": "950",
      "base_balance": "300",
      "sweepable_children_total": "650",
      "dust_ignored_total": "0.5" }

429 sweep_limit_exceeded
    { "error": "sweep_limit_exceeded",
      "limit_type": "daily_quota" | "addresses_per_request" | "in_flight_consolidation",
      "retry_after_seconds": 60,
      "current": 50, "limit": 50 }
```

---

## 7. Webhooks

### 7.1 Eventos novos

| Event | Quando dispara | Payload chave |
|---|---|---|
| `sweep.broadcast` | sweep tx transmitida on-chain | `tx_id`, `tx_hash`, `from_address`, `to_address`, `amount`, `asset`, `parent_transaction_id`, `origin` |
| `sweep.confirmed` | sweep tx confirmada | idem + `confirmations`, `block_number` |
| `withdrawal.sweep_required` | withdrawal disparou multi-sweep — UI pode mostrar progresso | `transaction_id`, `sweeps_planned`, `strategy` |
| `wallet.gas_status.changed` | `gas_status` transicionou entre valores | `wallet_id`, `old_status`, `new_status`, `base_address`, `balance`, `threshold` |

### 7.2 Campos adicionados nos eventos existentes

`withdrawal.broadcast` e `withdrawal.confirmed` ganham:

```json
{
  "origin": "user_request",
  "strategy": "direct_from_base" | "direct_from_child" | "multi_sweep",
  "sweep_tx_ids": ["..."],       // empty se strategy != multi_sweep
  "external_user_id": "user_123" // já existia
}
```

`deposit.*` — nada muda (já carrega `external_user_id`).

---

## 8. Frontend (vinext)

### 8.1 Componentes novos/alterados

**`/dashboard/wallets/[id]/gas-funding/` — nova página:**
- Exibida como step automático pós-criação de wallet quando `gas_status=unseeded`.
- Mostra: BaseAddress + QR code + threshold em native + conversão USD.
- Poll via SWR no endpoint `GET /v1/wallets/:id/gas-status` (30s) — transiciona para `seeded` automaticamente.
- Botão "Skip for now" permite continuar sem fundear, mas mantém banners de warning ativos.

**Badge de `gas_status` na listagem de wallets** (`/dashboard/wallets/`):
- `seeded`: nenhum badge (estado normal)
- `low`: badge amarelo "Low gas"
- `unseeded`: badge vermelho "Not gas-funded" + link pro fluxo de funding

**Banner no dashboard da wallet** (`/dashboard/wallets/[id]`):
- Se `gas_status != seeded`: banner persistente no topo com CTA para gas funding.

**Modal de withdraw (`/components/Modals/Withdraw`):**
- Antes de pedir passphrase, chama `POST /v1/wallets/:id/withdraw/preview`.
- Mostra strategy prevista:
  - `direct_from_base`: "Will execute 1 on-chain transaction"
  - `direct_from_child`: "Will execute 1 on-chain transaction (from user address X)"
  - `multi_sweep`: "Will consolidate 3 addresses first (~0.012 ETH gas) then send. Total 4 transactions."
- Warning dedicado se `gas_status != seeded` e strategy for multi_sweep → bloqueia o submit com CTA pro gas funding.

**Botão "Consolidate" (`/dashboard/wallets/[id]`):**
- Dispara `POST /v1/wallets/:id/consolidate`.
- Útil para limpar dust/preparar wallet antes de saques grandes.
- Mostra progresso baseado em webhooks `sweep.broadcast` + `sweep.confirmed`.

### 8.2 i18n

Novas chaves em `src/i18n/locales/en/` e `src/i18n/locales/pt/`:

- `wallets.gas.unseeded_banner_title` / `_body`
- `wallets.gas.low_banner_title` / `_body`
- `wallets.gas.funding_page_title`
- `wallets.consolidate.button`, `wallets.consolidate.confirm_modal.*`
- `withdraw.preview.strategy.direct_from_base` / `direct_from_child` / `multi_sweep`
- `errors.wallet_not_gas_ready`, `errors.insufficient_funds`, `errors.sweep_limit_exceeded`

---

## 9. Rate limiting / anti-abuse

Modelo BitGo-like, três camadas:

### 9.1 Serialização por wallet

Redis lock `vault:lock:consolidate:<wallet_id>` (TTL 60s, auto-refresh enquanto operação roda) — impede duas consolidações concorrentes na mesma wallet. Saques em paralelo com consolidation também são bloqueados pelo mesmo lock (ou por lock irmão `vault:lock:withdrawal:<wallet_id>` com chave coordenada).

### 9.2 Daily quota por account

Redis counter `vault:quota:consolidate:<account_id>:<YYYY-MM-DD>` com TTL 24h. Cada consolidação ou multi-sweep-withdrawal incrementa; `> max_consolidate_requests_per_day` rejeita.

### 9.3 Max addresses per request

Cap por chain: EVM/BTC = 100, SOL = 25 (limite de instrução por tx). Override via `accounts.sweep_limits.max_addresses_per_request`.

### 9.4 Velocity limit (opcional)

`accounts.sweep_limits.daily_withdraw_cap_usd` define teto de valor USD somado em 24h. Default NULL = sem cap. Implementado como Redis sorted set (timestamp → amount_usd); consulta soma os últimos 24h antes de aceitar withdraw.

### 9.5 Defaults globais

`config/security.go` adiciona:

```go
SweepDefaults: SweepLimits{
    MaxAddressesPerRequest: map[string]int{"evm": 100, "sol": 25, "btc": 100},
    MaxConsolidateRequestsPerDay: 50,
    DailyWithdrawCapUSD: nil, // disabled by default
}
```

---

## 10. Thresholds default (config)

**Fonte primária:** colunas `gas_readiness_threshold_raw`, `dust_threshold_native_raw`, `dust_threshold_usd` na tabela `chains` (migration §3.5). Popular via seeder (§3.6).

**Fallback / override:** `facades.Config().GetString(...)` lê de env/`config/security.go` se valor no DB estiver NULL. Útil em dev/staging sem rodar seed.

| Chain | `gas_readiness_threshold` | `dust_threshold_native` | `dust_threshold_usd` (tokens) |
|---|---|---|---|
| ETH | `0.005 ETH` (5 × 10¹⁵ wei) | $1 equivalente em wei | $1.00 |
| POL | `0.5 POL` (5 × 10¹⁷ wei) | `0.1 POL` equiv | $0.10 |
| BTC | N/A (fee nativo) | `10000 sats` | N/A (só nativo no BTC) |
| SOL | `0.01 SOL` (10 × 10⁶ lamports) | `0.001 SOL` (~$0.20) | $1.00 |

### 10.1 ENV override

```
ETH_GAS_READINESS_THRESHOLD_WEI=5000000000000000
ETH_DUST_THRESHOLD_USD=1.00
BTC_DUST_THRESHOLD_SATS=10000
SOL_GAS_READINESS_THRESHOLD_LAMPORTS=10000000
# ... etc
```

Estes vêm via `config/security.go` como campos `SweepThresholds map[string]ChainSweepThresholds`, lidos pelo service `sweep.Service` na inicialização e usados como fallback quando o valor no DB está NULL.

### 10.2 Valor USD dos dust thresholds de tokens

Dust threshold para tokens é em USD (porque o valor unitário varia muito). Requer cotação resolvida via `app/services/price/`. No momento do plan, service consulta cotação (cached em Redis com TTL curto) e converte para unidades raw do token antes de comparar com saldos.

Fallback quando cotação indisponível: **assume dust_threshold = 0** (sweepa tudo) — fail-safe para não ignorar erroneamente saldos válidos. Loga WARN pra monitoramento alertar.

---

## 11. Testing

### 11.1 Unit tests — `sweep/service_test.go`

- `PlanForWithdrawal`:
  - Base cobre → `direct_from_base`
  - Um child cobre → `direct_from_child`
  - Nenhum sozinho cobre, soma cobre → `multi_sweep` com subset greedy
  - Soma não cobre → `insufficient`, `ReachesTarget=false`
  - Dust ignorado não entra no plan, aparece em `DustIgnored`
- `ExecutePlan`:
  - Multi_sweep sucesso end-to-end
  - Falha no step 3/5 → `FailedStep` preenchido, sweeps prévios persistidos
  - Retry após falha → plan recalculado sem children já sweepados
- `RefreshGasStatus`:
  - Transições `unseeded → seeded → low → seeded` emitem webhook apropriado
- `CheckLimits`:
  - Lock in-flight → erro
  - Quota diária estourada → erro
  - Max addresses > limit → erro

### 11.2 Integration tests — mock RPC + Redis real

- EVM multi_sweep completo (3 children com USDT; 1 child sem ETH disparando gas_seed)
- SOL sweep com `fee_payer=Base` + close_ata
- BTC sweep de 2 UTXOs
- Retry de withdraw após falha parcial (usa mesmo `idempotency_key`)

### 11.3 E2E — Playwright

- Criar wallet → ver banner `unseeded` → fundear → banner some → criar deposit address → receber depósito → withdraw → observar webhooks de sweep

---

## 12. Rollout

Sistema ainda não lançado em produção. Rollout idiomático Goravel:

1. **Aplicar migrations** em ordem: `make migrate` roda as 5 migrations da §3 sequencialmente.
2. **Popular thresholds**: `make db-seed` executa seeder novo/atualizado que preenche `chains.gas_readiness_threshold_raw`, `chains.dust_threshold_native_raw`, `chains.dust_threshold_usd` com os defaults da §10.
3. **Deploy do código** com o novo `sweep` service, endpoints novos, webhooks novos.
4. Dev/staging: `make migrate-fresh-seed` deixa tudo limpo com wallets recém-criadas em `gas_status=unseeded`.
5. Smoke tests manuais por chain (ETH, POL, BTC, SOL) usando a UI ou Postman → cobre criar wallet, gas funding, gerar child address, depositar, withdraw (cada strategy), consolidate manual.
6. Go-live sem período de transição — novo modelo é o único modelo.

### 12.1 Observabilidade

Novas métricas (exporte pra CloudWatch):
- `sweep.plan.strategy_total{strategy="direct_from_base|direct_from_child|multi_sweep|insufficient"}`
- `sweep.execute.success_total{chain}`
- `sweep.execute.failed_total{chain, step}`
- `sweep.gas_status_total{status}` — gauge de wallets por status
- `sweep.limit_hit_total{limit_type}`
- Histograma `sweep.execute.duration_seconds{chain, strategy}`

Dashboards críticos:
- % de saques que requerem multi_sweep (se > 50%, UI deve empurrar consolidação manual mais forte).
- Falha de sweep por chain (se > 5%, incident).
- Wallets estuck em `unseeded` há > 7 dias.

---

## 13. Open items (para o plano de implementação)

Estas são decisões de execução, não de arquitetura. Resolvidas no plan ou no próprio código:

- **SOL close_account** default true ou false? Proposta: **true**, configurável via `sweep_limits.sol.close_empty_ata`. Recupera rent, mas surpreende apps que assumem ATA persistente.
- **Ordem do Redis lock** entre `vault:lock:withdrawal:<id>` e `vault:lock:consolidate:<id>` — usar prefixo único `vault:lock:wallet_ops:<id>` pra simplificar (ambos peguem o mesmo lock).
- **Gas seed exato em EVM** — quanto fundear? Proposta: `estimated_gas × gasPrice × 1.2`. Se fundear menos, sweep falha; se fundear mais, sobra no child (não é problema).
- **`gas_check` rate limit** — 1/min por wallet é razoável? Ajustar se UI precisar mais.
- **`wallet_asset_balance` sync** — continua funcionando como está? Precisa revisar se o sync considera saldo consolidado no Base separadamente das child addresses para a UI mostrar per-address.

---

## 14. Fora de escopo (para specs futuros)

- **Transferências internas entre users** (ledger completo C da decisão de §1.2).
- **Recovery via backup key** (exige pivô do modelo MPC para multisig 2-of-3 real, caminho B).
- **Warm / Cold tier** — esse spec só cobre Hot. Segregação Warm/Cold é um produto diferente.
- **Account abstraction / EIP-4337** em EVM.
- **Squads Protocol** para multisig real em SOL.
- **Proof of Reserves** públicas — não aplicável nesse modelo (cada wallet é do cliente, saldo verificável on-chain individualmente).

---

## 15. Self-review

**Placeholders:** nenhum TBD/TODO pendente em decisões de arquitetura. `§13 Open items` lista decisões de execução com proposta em cada uma.

**Consistência:**
- `§4.1 Plan.Strategy` e `§6.4 strategy` em responses usam o mesmo vocabulário.
- `gas_status` (`unseeded|seeded|low`) usado consistentemente em DB, API, webhook, UI.
- Idempotência do retry descrita em `§4.2 resumeOrReturn`, `§6.3`, e `§11.2`.
- Todas as migrations usam Goravel Schema builder (`facades.Schema()`) — padrão do projeto, nenhuma SQL cru exceto o `ALTER TYPE` da §3.3 (limitação documentada do framework).
- Storage de thresholds: colunas em `chains` (não em `chain_resources`, que é dedicada a URLs).

**Escopo:** um único spec coeso. Ledger interno, multisig real e tiering Warm/Cold explicitamente fora de escopo (§14).

**Ambiguidade resolvida:**
- "Always-from-Base vs opportunistic" decidido como **opportunistic + threshold** (policy #4) com racional de custo em §1.2.
- "Per-user balance" decidido como **sem server-side tracking**; cliente consome webhooks.
- "Sweep trigger" decidido como **lazy + manual endpoint**; sem background automation.
- "Onde guardar thresholds" decidido como **novas colunas em `chains`**, não em `chain_resources`.

**Alinhamento com padrões Goravel** (§4 preamble): `facades.Orm()`, `facades.Schema()`, `facades.Config()`, `facades.Event()` usados em todas as camadas; repositories nas interfaces existentes; slog como logger; tests tabela-driven.
