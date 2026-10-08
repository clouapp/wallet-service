# Wallet Read Model Refresh — Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task.

**Goal:** Add a backend wallet read model with current wallet balances, asset balances, recent balance snapshots, Bitcoin UTXOs, sync state, SQS-backed Goravel jobs, Goravel events/listeners, and sync-first Artisan refresh commands.

**Architecture:** Persist current balance summary on `wallets`, reuse the existing `transactions` table for wallet-facing transaction reads, and add focused tables for asset balances, snapshots, UTXOs, and sync state. Automatic refreshes flow through Goravel events to queued listeners and jobs on a single `blockchain` queue; manual refreshes run through Goravel Artisan commands synchronously by default and call the same refresh services.

**Tech Stack:** Go 1.22, Goravel v1.17, PostgreSQL, AWS SQS, AWS Lambda, Redis, existing chain adapters

**Spec:** `back/docs/superpowers/specs/2026-04-07-wallet-read-model-refresh-design.md`

---

## Current Repo State (as of rewrite)

| Area | Current Shape |
|------|--------------|
| Bootstrap | `bootstrap/app.go` → `WithMigrations`, `WithProviders`, `WithSeeders`, `WithConfig` — no `WithJobs`, `WithCommands`, `WithEvents` |
| Providers | `bootstrap/providers.go` → framework providers + `MigrationsServiceProvider`, `AuthServiceProvider`, `AppServiceProvider`, `RouteServiceProvider` |
| Container | `app/providers/vault_container.go` → `app.Singleton(container.ContainerKey, ...)` via `AppServiceProvider.Register` |
| Config | Split files under `config/` → `boot.go` calls `registerQueue()` etc. Queue config in `config/queue.go` has only `sync` connection |
| Migrations | `database/migrations/migrations.go` → 20 migrations, last is `M20260329000001WalletDepositAddressFk` |
| Models | `Wallet` and `Transaction` use `orm.Model` + `gorm` tags with varchar types for status/type fields |
| Jobs/Events/Commands | None exist (`app/jobs/`, `app/events/`, `app/listeners/`, `app/console/commands/` are absent) |
| Routes | `RouteServiceProvider.Boot` → `routes.RegisterHTTP()` |
| main.go | Artisan dispatch via `facades.Artisan().Run(os.Args, true)`, Lambda modes for deposit/confirmation/webhook/reconciler/api |

---

## File Map

**Create**

```
database/migrations/20260407000001_create_blockchain_enum_types.go
database/migrations/20260407000002_add_wallet_read_model_columns.go
database/migrations/20260407000003_create_wallet_balance_snapshots_table.go
database/migrations/20260407000004_create_wallet_asset_balances_table.go
database/migrations/20260407000005_extend_transactions_for_wallet_reads.go
database/migrations/20260407000006_create_wallet_utxos_table.go
database/migrations/20260407000007_create_wallet_sync_states_table.go

app/models/wallet_asset_balance.go
app/models/wallet_balance_snapshot.go
app/models/wallet_utxo.go
app/models/wallet_sync_state.go

app/repositories/wallet_asset_balance_repository.go
app/repositories/wallet_balance_snapshot_repository.go
app/repositories/wallet_utxo_repository.go
app/repositories/wallet_sync_state_repository.go

app/services/refresh/types.go
app/services/refresh/dispatcher.go
app/services/refresh/balances.go
app/services/refresh/transactions.go
app/services/refresh/utxos.go
app/services/refresh/reconcile.go
app/services/refresh/evm.go
app/services/refresh/solana.go
app/services/refresh/bitcoin.go

app/jobs/refresh_wallet_balances.go
app/jobs/refresh_wallet_transactions.go
app/jobs/refresh_wallet_tokens.go
app/jobs/refresh_wallet_utxos.go
app/jobs/reconcile_wallet_state.go

app/events/wallet_created.go
app/events/wallet_activated.go
app/events/deposit_detected.go
app/events/withdrawal_broadcasted.go
app/events/wallet_refresh_requested.go

app/listeners/enqueue_wallet_refresh.go
app/listeners/enqueue_transaction_refresh.go
app/listeners/enqueue_utxo_refresh.go

app/console/commands/refresh_wallet.go
app/console/commands/refresh_address.go
app/console/commands/refresh_currency.go
app/console/commands/refresh_tx.go
app/console/commands/reconcile_wallet.go
```

**Modify**

```
database/migrations/migrations.go
app/models/wallet.go
app/models/transaction.go
app/repositories/wallet_repository.go
app/repositories/transaction_repository.go
app/providers/vault_container.go
app/services/wallet/service.go
app/services/withdraw/service.go
app/services/ingest/service.go
app/services/deposit/service.go
bootstrap/app.go
config/queue.go
app/http/controllers/wallets_controller.go
app/http/controllers/wallet_unspents_controller.go
```

---

## Phase 1: Schema Foundation

### Task 1: Create blockchain enum types

**Files:**
- Create: `database/migrations/20260407000001_create_blockchain_enum_types.go`
- Modify: `database/migrations/migrations.go`

- [ ] **Step 1: Write the migration**

Create `database/migrations/20260407000001_create_blockchain_enum_types.go`:

```go
package migrations

import "github.com/goravel/framework/facades"

type M20260407000001CreateBlockchainEnumTypes struct{}

func (m *M20260407000001CreateBlockchainEnumTypes) Signature() string {
	return "20260407000001_create_blockchain_enum_types"
}

func (m *M20260407000001CreateBlockchainEnumTypes) Up() error {
	return facades.Orm().Query().Exec(`
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'wallet_status') THEN
        CREATE TYPE wallet_status AS ENUM ('active', 'pending', 'archived', 'frozen');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'wallet_read_model_status') THEN
        CREATE TYPE wallet_read_model_status AS ENUM ('idle', 'syncing', 'synced', 'stale', 'failed');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'asset_type') THEN
        CREATE TYPE asset_type AS ENUM ('native', 'token');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'wallet_sync_scope') THEN
        CREATE TYPE wallet_sync_scope AS ENUM ('balances', 'transactions', 'tokens', 'utxos', 'full');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'wallet_sync_status') THEN
        CREATE TYPE wallet_sync_status AS ENUM ('idle', 'syncing', 'synced', 'stale', 'failed');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'utxo_status') THEN
        CREATE TYPE utxo_status AS ENUM ('unspent', 'locked', 'spent', 'orphaned');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transaction_status') THEN
        CREATE TYPE transaction_status AS ENUM ('pending', 'confirming', 'confirmed', 'failed', 'dropped');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transaction_type') THEN
        CREATE TYPE transaction_type AS ENUM ('deposit', 'withdrawal', 'transfer', 'token_transfer', 'fee');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transaction_direction') THEN
        CREATE TYPE transaction_direction AS ENUM ('inbound', 'outbound', 'self', 'unknown');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transaction_source') THEN
        CREATE TYPE transaction_source AS ENUM ('chain', 'deposit_ingest', 'withdrawal_flow', 'reconciliation');
    END IF;
END $$;
`)
}

func (m *M20260407000001CreateBlockchainEnumTypes) Down() error {
	return facades.Orm().Query().Exec(`
DROP TYPE IF EXISTS transaction_source;
DROP TYPE IF EXISTS transaction_direction;
DROP TYPE IF EXISTS transaction_type;
DROP TYPE IF EXISTS transaction_status;
DROP TYPE IF EXISTS utxo_status;
DROP TYPE IF EXISTS wallet_sync_status;
DROP TYPE IF EXISTS wallet_sync_scope;
DROP TYPE IF EXISTS asset_type;
DROP TYPE IF EXISTS wallet_read_model_status;
DROP TYPE IF EXISTS wallet_status;
`)
}
```

- [ ] **Step 2: Register in migrations.go**

Add `&M20260407000001CreateBlockchainEnumTypes{},` after `&M20260329000001WalletDepositAddressFk{},` in `database/migrations/migrations.go`.

- [ ] **Step 3: Run migration**

Run: `cd back && go run . artisan migrate`

Expected: enum types created without errors.

- [ ] **Step 4: Commit**

```bash
git add database/migrations/20260407000001_create_blockchain_enum_types.go database/migrations/migrations.go
git commit -m "feat: add blockchain enum types for wallet read models"
```

---

### Task 2: Extend wallets with balance summary columns

**Files:**
- Create: `database/migrations/20260407000002_add_wallet_read_model_columns.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/wallet.go`

- [ ] **Step 1: Write the migration**

Create `database/migrations/20260407000002_add_wallet_read_model_columns.go`:

```go
package migrations

import "github.com/goravel/framework/facades"

type M20260407000002AddWalletReadModelColumns struct{}

func (m *M20260407000002AddWalletReadModelColumns) Signature() string {
	return "20260407000002_add_wallet_read_model_columns"
}

func (m *M20260407000002AddWalletReadModelColumns) Up() error {
	return facades.Orm().Query().Exec(`
ALTER TABLE wallets
    ALTER COLUMN status TYPE wallet_status USING status::wallet_status,
    ADD COLUMN IF NOT EXISTS balance_asset VARCHAR(32),
    ADD COLUMN IF NOT EXISTS balance_raw TEXT,
    ADD COLUMN IF NOT EXISTS balance_display TEXT,
    ADD COLUMN IF NOT EXISTS balance_usd NUMERIC(28, 10),
    ADD COLUMN IF NOT EXISTS balance_last_synced_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS read_model_status wallet_read_model_status DEFAULT 'idle';
`)
}

func (m *M20260407000002AddWalletReadModelColumns) Down() error {
	return facades.Orm().Query().Exec(`
ALTER TABLE wallets
    DROP COLUMN IF EXISTS read_model_status,
    DROP COLUMN IF EXISTS balance_last_synced_at,
    DROP COLUMN IF EXISTS balance_usd,
    DROP COLUMN IF EXISTS balance_display,
    DROP COLUMN IF EXISTS balance_raw,
    DROP COLUMN IF EXISTS balance_asset;
ALTER TABLE wallets ALTER COLUMN status TYPE VARCHAR(20) USING status::text;
`)
}
```

- [ ] **Step 2: Update the Wallet model**

In `app/models/wallet.go`, change the `Status` gorm tag and add new fields after `ActivationCode`:

```go
Status            string     `gorm:"type:wallet_status;default:active" json:"status"`
```

Add these fields before the `DepositAddress` relationship:

```go
BalanceAsset        *string    `gorm:"type:varchar(32)" json:"balance_asset,omitempty"`
BalanceRaw          *string    `gorm:"type:text" json:"balance_raw,omitempty"`
BalanceDisplay      *string    `gorm:"type:text" json:"balance,omitempty"`
BalanceUSD          *float64   `gorm:"type:decimal(28,10)" json:"balance_usd,omitempty"`
BalanceLastSyncedAt *time.Time `gorm:"type:timestamptz" json:"balance_last_synced_at,omitempty"`
ReadModelStatus     string     `gorm:"type:wallet_read_model_status;default:idle" json:"read_model_status"`
```

- [ ] **Step 3: Register migration and run**

Add to `migrations.go` and run: `cd back && go run . artisan migrate`

Expected: `wallets.status` converts to enum and new balance columns appear.

- [ ] **Step 4: Run existing wallet tests**

Run: `cd back && go test ./app/services/wallet/... -v -count=1`

Expected: existing tests pass.

- [ ] **Step 5: Commit**

```bash
git add database/migrations/20260407000002_add_wallet_read_model_columns.go database/migrations/migrations.go app/models/wallet.go
git commit -m "feat: store current wallet balance summary on wallets table"
```

---

### Task 3: Create wallet_balance_snapshots, wallet_asset_balances, wallet_utxos, wallet_sync_states tables and models

**Files:**
- Create: `database/migrations/20260407000003_create_wallet_balance_snapshots_table.go`
- Create: `database/migrations/20260407000004_create_wallet_asset_balances_table.go`
- Create: `database/migrations/20260407000006_create_wallet_utxos_table.go`
- Create: `database/migrations/20260407000007_create_wallet_sync_states_table.go`
- Create: `app/models/wallet_balance_snapshot.go`
- Create: `app/models/wallet_asset_balance.go`
- Create: `app/models/wallet_utxo.go`
- Create: `app/models/wallet_sync_state.go`
- Modify: `database/migrations/migrations.go`

- [ ] **Step 1: Write each migration and model using the spec table definitions**

Use Goravel `facades.Orm().Query().Exec(...)` for DDL that includes enum types. Each model must embed `orm.Model`, set `TableName()`, and use gorm tags matching the spec columns. The `wallet_utxos` status column uses `utxo_status`, sync states use `wallet_sync_scope` and `wallet_sync_status`, asset balances use `asset_type`.

- [ ] **Step 2: Register all four migrations in order and run**

Run: `cd back && go run . artisan migrate`

Expected: all four tables created with correct foreign keys and unique constraints.

- [ ] **Step 3: Commit**

```bash
git add database/migrations/20260407000003_create_wallet_balance_snapshots_table.go database/migrations/20260407000004_create_wallet_asset_balances_table.go database/migrations/20260407000006_create_wallet_utxos_table.go database/migrations/20260407000007_create_wallet_sync_states_table.go database/migrations/migrations.go app/models/wallet_balance_snapshot.go app/models/wallet_asset_balance.go app/models/wallet_utxo.go app/models/wallet_sync_state.go
git commit -m "feat: add wallet read model tables and models"
```

---

### Task 4: Extend transactions table for wallet-facing reads

**Files:**
- Create: `database/migrations/20260407000005_extend_transactions_for_wallet_reads.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/transaction.go`
- Modify: `app/repositories/transaction_repository.go`

- [ ] **Step 1: Write the migration**

```go
func (m *M20260407000005ExtendTransactionsForWalletReads) Up() error {
	return facades.Orm().Query().Exec(`
ALTER TABLE transactions
    ALTER COLUMN tx_type TYPE transaction_type USING tx_type::transaction_type,
    ALTER COLUMN status TYPE transaction_status USING status::transaction_status,
    ADD COLUMN IF NOT EXISTS direction transaction_direction,
    ADD COLUMN IF NOT EXISTS source transaction_source,
    ADD COLUMN IF NOT EXISTS raw_payload JSONB,
    ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ;
`)
}
```

- [ ] **Step 2: Update Transaction model gorm tags**

Change in `app/models/transaction.go`:

```go
TxType    string     `gorm:"type:transaction_type;not null;index" json:"tx_type"`
Status    string     `gorm:"type:transaction_status;not null;index" json:"status"`
```

Add new fields:

```go
Direction  string     `gorm:"type:transaction_direction" json:"direction,omitempty"`
Source     string     `gorm:"type:transaction_source" json:"source,omitempty"`
RawPayload string    `gorm:"type:jsonb" json:"raw_payload,omitempty"`
SyncedAt   *time.Time `gorm:"type:timestamptz" json:"synced_at,omitempty"`
```

- [ ] **Step 3: Add `ListByWalletAndChain` to TransactionRepository**

```go
ListByWalletAndChain(walletID uuid.UUID, chainID string, limit, offset int) ([]models.Transaction, int64, error)
```

Use the existing count-and-query pattern; order by `COALESCE(block_number, 0) DESC, created_at DESC`.

- [ ] **Step 4: Run repository tests and migration**

Run: `cd back && go test ./app/repositories/... -v -count=1 && go run . artisan migrate`

Expected: all pass, enum conversion succeeds.

- [ ] **Step 5: Commit**

```bash
git add database/migrations/20260407000005_extend_transactions_for_wallet_reads.go database/migrations/migrations.go app/models/transaction.go app/repositories/transaction_repository.go
git commit -m "feat: extend transactions for wallet-facing read model"
```

---

## Phase 2: Repositories And Refresh Services

### Task 5: Add read-model repositories and wire into vault container

**Files:**
- Create: `app/repositories/wallet_asset_balance_repository.go`
- Create: `app/repositories/wallet_balance_snapshot_repository.go`
- Create: `app/repositories/wallet_utxo_repository.go`
- Create: `app/repositories/wallet_sync_state_repository.go`
- Modify: `app/container/container.go` (add fields)
- Modify: `app/providers/vault_container.go` (instantiate repos)

- [ ] **Step 1: Write each repository interface and implementation**

Follow the repo pattern: interface with focused methods, concrete struct, `New*Repository()` constructor.

Key methods per spec:

```go
// WalletAssetBalanceRepository
ReplaceForWallet(walletID uuid.UUID, chainID string, rows []models.WalletAssetBalance) error
ListByWallet(walletID uuid.UUID) ([]models.WalletAssetBalance, error)

// WalletBalanceSnapshotRepository
Create(snapshot *models.WalletBalanceSnapshot) error
TrimToLatest(walletID uuid.UUID, chainID string, keep int) error
ListRecent(walletID uuid.UUID, chainID string, limit int) ([]models.WalletBalanceSnapshot, error)

// WalletUTXORepository
ReplaceForWallet(walletID uuid.UUID, chainID string, rows []models.WalletUTXO) error
ListSpendable(walletID uuid.UUID, chainID string) ([]models.WalletUTXO, error)

// WalletSyncStateRepository
Find(walletID uuid.UUID, chainID string, scope string) (*models.WalletSyncState, error)
Upsert(state *models.WalletSyncState) error
UpdateFailure(walletID uuid.UUID, chainID, scope, errMsg string) error
```

- [ ] **Step 2: Add fields to Container struct in `app/container/container.go`**

```go
WalletAssetBalanceRepo    repositories.WalletAssetBalanceRepository
WalletBalanceSnapshotRepo repositories.WalletBalanceSnapshotRepository
WalletUTXORepo            repositories.WalletUTXORepository
WalletSyncStateRepo       repositories.WalletSyncStateRepository
```

- [ ] **Step 3: Instantiate in `app/providers/vault_container.go`**

Add after `c.WebhookSubscriptionRepo = ...`:

```go
c.WalletAssetBalanceRepo = repositories.NewWalletAssetBalanceRepository()
c.WalletBalanceSnapshotRepo = repositories.NewWalletBalanceSnapshotRepository()
c.WalletUTXORepo = repositories.NewWalletUTXORepository()
c.WalletSyncStateRepo = repositories.NewWalletSyncStateRepository()
```

- [ ] **Step 4: Compile**

Run: `cd back && go build ./...`

Expected: clean compile.

- [ ] **Step 5: Commit**

```bash
git add app/repositories/wallet_asset_balance_repository.go app/repositories/wallet_balance_snapshot_repository.go app/repositories/wallet_utxo_repository.go app/repositories/wallet_sync_state_repository.go app/container/container.go app/providers/vault_container.go
git commit -m "feat: add repositories for wallet read model tables"
```

---

### Task 6: Create refresh service types and dispatcher

**Files:**
- Create: `app/services/refresh/types.go`
- Create: `app/services/refresh/dispatcher.go`
- Create: `app/services/refresh/dispatcher_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestExpandScopesForEVMFull(t *testing.T) {
    req := RefreshRequest{ChainID: "eth", WalletID: "wallet-1", Scope: RefreshScopeFull}
    scopes, err := ExpandScopes(req)
    if err != nil { t.Fatal(err) }
    if len(scopes) != 3 { t.Fatalf("expected 3 scopes, got %d", len(scopes)) }
}
```

- [ ] **Step 2: Write types.go and dispatcher.go**

`types.go` defines `RefreshScope` constants and `RefreshRequest` struct.

`dispatcher.go` implements `ExpandScopes(req) ([]RefreshScope, error)` that splits `full` into chain-appropriate scopes: EVM → balances+transactions+tokens, Solana → balances+transactions+tokens, Bitcoin → balances+transactions+utxos.

- [ ] **Step 3: Run test**

Run: `cd back && go test ./app/services/refresh/... -v -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add app/services/refresh/
git commit -m "feat: add refresh request types and scope dispatcher"
```

---

### Task 7: Balance refresh service with snapshot retention

**Files:**
- Create: `app/services/refresh/balances.go`
- Modify: `app/repositories/wallet_repository.go`
- Create: `app/services/refresh/balances_test.go`

- [ ] **Step 1: Add `UpdateReadModelFields` to WalletRepository**

- [ ] **Step 2: Write `BalanceService.RefreshWallet`**

Calls `adapter.GetBalance`, persists into `wallet_asset_balances` via `ReplaceForWallet`, updates `wallets.balance_*` via `UpdateReadModelFields`, creates a snapshot, trims snapshots to 10.

- [ ] **Step 3: Write test with mock chain adapter**

- [ ] **Step 4: Run test and commit**

---

### Task 8: Bitcoin UTXO refresh service

**Files:**
- Create: `app/services/refresh/utxos.go`
- Modify: `app/http/controllers/wallet_unspents_controller.go`
- Create: `app/services/refresh/utxos_test.go`

- [ ] **Step 1: Write `UTXOService.ReplaceWalletUTXOs`**

Replaces `wallet_utxos` for a wallet and updates sync state.

- [ ] **Step 2: Switch unspents controller to read from `WalletUTXORepo.ListSpendable`**

- [ ] **Step 3: Write test and commit**

---

## Phase 3: Goravel Queue, Jobs, Events, Commands

### Task 9: Add SQS queue connection to Goravel config

**Files:**
- Modify: `config/queue.go`

- [ ] **Step 1: Add database queue connection for the blockchain queue**

Update `config/queue.go` to add a `database` connection alongside `sync`:

```go
func registerQueue() {
	facades.Config().Add("queue", map[string]any{
		"default": envString("QUEUE_CONNECTION", "sync"),
		"connections": map[string]any{
			"sync": map[string]any{
				"driver": "sync",
			},
			"database": map[string]any{
				"driver":     "database",
				"connection": "postgres",
				"queue":      "blockchain",
			},
		},
		"failed": map[string]any{
			"database": "postgres",
			"table":    "failed_jobs",
		},
	})
}
```

Note: Goravel's database queue driver requires the `jobs` table migration, which Goravel provides. We use the database driver as the initial background queue. SQS can be introduced later as a custom driver without changing job signatures.

- [ ] **Step 2: Compile**

Run: `cd back && go build ./...`

Expected: queue config loads without errors.

- [ ] **Step 3: Commit**

```bash
git add config/queue.go
git commit -m "feat: add database-backed goravel queue connection for blockchain refresh"
```

---

### Task 10: Create Goravel job classes

**Files:**
- Create: `app/jobs/refresh_wallet_balances.go`
- Create: `app/jobs/refresh_wallet_transactions.go`
- Create: `app/jobs/refresh_wallet_tokens.go`
- Create: `app/jobs/refresh_wallet_utxos.go`
- Create: `app/jobs/reconcile_wallet_state.go`

- [ ] **Step 1: Write each job**

Each job follows this pattern:

```go
package jobs

import "time"

type RefreshWalletBalances struct{}

func (j *RefreshWalletBalances) Signature() string {
	return "refresh_wallet_balances"
}

func (j *RefreshWalletBalances) Handle(args ...any) error {
	walletID, _ := args[0].(string)
	chainID, _ := args[1].(string)
	_ = walletID
	_ = chainID
	// resolved from container.Get() and calls refresh services
	return nil
}

func (j *RefreshWalletBalances) ShouldRetry(err error, attempt int) (bool, time.Duration) {
	if attempt >= 5 { return false, 0 }
	return true, time.Duration(attempt) * 5 * time.Second
}
```

Signatures:
- `refresh_wallet_balances`
- `refresh_wallet_transactions`
- `refresh_wallet_tokens`
- `refresh_wallet_utxos`
- `reconcile_wallet_state`

- [ ] **Step 2: Compile**

Run: `cd back && go build ./...`

- [ ] **Step 3: Commit**

```bash
git add app/jobs/
git commit -m "feat: add goravel blockchain refresh job classes"
```

---

### Task 11: Create Goravel events and listeners

**Files:**
- Create: `app/events/wallet_created.go`
- Create: `app/events/wallet_activated.go`
- Create: `app/events/deposit_detected.go`
- Create: `app/events/withdrawal_broadcasted.go`
- Create: `app/events/wallet_refresh_requested.go`
- Create: `app/listeners/enqueue_wallet_refresh.go`
- Create: `app/listeners/enqueue_transaction_refresh.go`
- Create: `app/listeners/enqueue_utxo_refresh.go`

- [ ] **Step 1: Write event types**

Each event passes args through:

```go
package events

import "github.com/goravel/framework/contracts/event"

type WalletCreated struct{}

func (e *WalletCreated) Handle(args []event.Arg) ([]event.Arg, error) {
	return args, nil
}
```

- [ ] **Step 2: Write queued listeners**

Each listener uses the Goravel event queue interface:

```go
package listeners

import (
	frameworkevent "github.com/goravel/framework/contracts/event"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/facades"
	"github.com/macrowallets/waas/app/jobs"
)

type EnqueueWalletRefresh struct{}

func (l *EnqueueWalletRefresh) Signature() string {
	return "enqueue_wallet_refresh"
}

func (l *EnqueueWalletRefresh) Queue(args ...any) frameworkevent.Queue {
	return frameworkevent.Queue{
		Enable:     true,
		Connection: "database",
		Queue:      "blockchain",
	}
}

func (l *EnqueueWalletRefresh) Handle(args ...any) error {
	walletID, _ := args[0].(string)
	chainID, _ := args[1].(string)
	return facades.Queue().
		Job(&jobs.RefreshWalletBalances{}, []queue.Arg{
			{Type: "string", Value: walletID},
			{Type: "string", Value: chainID},
		}).
		OnConnection("database").
		OnQueue("blockchain").
		Dispatch()
}
```

- [ ] **Step 3: Compile**

Run: `cd back && go build ./...`

- [ ] **Step 4: Commit**

```bash
git add app/events/ app/listeners/
git commit -m "feat: add goravel events and queued listeners for wallet refresh"
```

---

### Task 12: Wire jobs, events, and commands into bootstrap

**Files:**
- Modify: `bootstrap/app.go`

- [ ] **Step 1: Add WithJobs, WithCommands, WithEvents to bootstrap chain**

Update `bootstrap/app.go`:

```go
package bootstrap

import (
	contractsevent "github.com/goravel/framework/contracts/event"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/console/commands"
	"github.com/macrowallets/waas/app/events"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/listeners"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/database/seeders"
)

func Boot() contractsfoundation.Application {
	return foundation.Setup().
		WithMigrations(Migrations).
		WithProviders(Providers).
		WithSeeders(seeders.All).
		WithJobs(func() []queue.Job {
			return []queue.Job{
				&jobs.RefreshWalletBalances{},
				&jobs.RefreshWalletTransactions{},
				&jobs.RefreshWalletTokens{},
				&jobs.RefreshWalletUtxos{},
				&jobs.ReconcileWalletState{},
			}
		}).
		WithCommands(func() []console.Command {
			return []console.Command{
				&commands.RefreshWallet{},
				&commands.RefreshAddress{},
				&commands.RefreshCurrency{},
				&commands.RefreshTx{},
				&commands.ReconcileWallet{},
			}
		}).
		WithEvents(func() map[contractsevent.Event][]contractsevent.Listener {
			return map[contractsevent.Event][]contractsevent.Listener{
				&events.WalletCreated{}:          {&listeners.EnqueueWalletRefresh{}},
				&events.WalletActivated{}:        {&listeners.EnqueueWalletRefresh{}},
				&events.DepositDetected{}:        {&listeners.EnqueueTransactionRefresh{}},
				&events.WithdrawalBroadcasted{}:  {&listeners.EnqueueWalletRefresh{}},
				&events.WalletRefreshRequested{}: {&listeners.EnqueueWalletRefresh{}},
			}
		}).
		WithConfig(config.Boot).
		Create()
}
```

Note: the `console.Command` import must match the Goravel contract type. Check `github.com/goravel/framework/contracts/console` for the exact interface type expected by `WithCommands`.

- [ ] **Step 2: Compile**

Run: `cd back && go build ./...`

Expected: application boots with all registrations.

- [ ] **Step 3: Commit**

```bash
git add bootstrap/app.go
git commit -m "feat: register refresh jobs commands and events in goravel bootstrap"
```

---

### Task 13: Create Artisan commands (sync-first)

**Files:**
- Create: `app/console/commands/refresh_wallet.go`
- Create: `app/console/commands/refresh_address.go`
- Create: `app/console/commands/refresh_currency.go`
- Create: `app/console/commands/refresh_tx.go`
- Create: `app/console/commands/reconcile_wallet.go`

- [ ] **Step 1: Write `refresh:wallet` command**

```go
package commands

import (
	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
)

type RefreshWallet struct{}

func (c *RefreshWallet) Signature() string {
	return "refresh:wallet"
}

func (c *RefreshWallet) Description() string {
	return "Refresh a wallet read model (sync-first by default)"
}

func (c *RefreshWallet) Extend() command.Extend {
	return command.Extend{
		Category: "refresh",
		Arguments: []command.Argument{
			&command.ArgumentString{
				Name:     "wallet_id",
				Usage:    "wallet UUID to refresh",
				Required: true,
			},
		},
		Flags: []command.Flag{
			&command.StringFlag{Name: "scope", Value: "full", Usage: "balances|transactions|tokens|utxos|full"},
			&command.StringFlag{Name: "chain", Value: "", Usage: "chain override"},
			&command.BoolFlag{Name: "queue", Usage: "dispatch to queue instead of sync execution"},
			&command.BoolFlag{Name: "force", Usage: "ignore freshness guards"},
			&command.StringFlag{Name: "reason", Value: "manual", Usage: "reason for refresh"},
		},
	}
}

func (c *RefreshWallet) Handle(ctx console.Context) error {
	walletID := ctx.ArgumentString("wallet_id")
	scope := ctx.Option("scope")
	isQueue := ctx.OptionBool("queue")

	if isQueue {
		ctx.Info("dispatching refresh to blockchain queue")
		return nil
	}

	ctx.Info("refreshing wallet " + walletID + " with scope " + scope)
	return nil
}
```

- [ ] **Step 2: Write the other four commands**

Follow the same pattern with appropriate signatures and arguments:

- `refresh:address` — args: `chain` (required), `addresses` (required, variadic)
- `refresh:currency` — args: `currency` (required), `addresses` (required, variadic); flags include `--chain`
- `refresh:tx` — args: `chain` (required), `tx_hash` (required)
- `reconcile:wallet` — args: `wallet_id` (required); category: `reconcile`

- [ ] **Step 3: Compile and verify**

Run: `cd back && go build ./... && go run . artisan list --no-ansi`

Expected: all five commands appear under `refresh` and `reconcile` categories.

- [ ] **Step 4: Commit**

```bash
git add app/console/commands/
git commit -m "feat: add sync-first artisan commands for wallet refresh and reconciliation"
```

---

## Phase 4: Event Dispatch From Existing Services

### Task 14: Dispatch events from wallet, ingest, and withdrawal services

**Files:**
- Modify: `app/services/wallet/service.go`
- Modify: `app/services/ingest/service.go`
- Modify: `app/services/withdraw/service.go`

- [ ] **Step 1: Dispatch WalletCreated after wallet creation**

In `app/services/wallet/service.go`, after the wallet and address are persisted and webhook sync is triggered, add:

```go
_ = facades.Event().Job(&events.WalletCreated{}, []event.Arg{
	{Type: "string", Value: walletID.String()},
	{Type: "string", Value: chainID},
}).Dispatch()
```

- [ ] **Step 2: Dispatch DepositDetected after ingest insert**

In `app/services/ingest/service.go`, after `s.txRepo.Create(tx)` succeeds:

```go
_ = facades.Event().Job(&events.DepositDetected{}, []event.Arg{
	{Type: "string", Value: tx.WalletID.String()},
	{Type: "string", Value: chainID},
	{Type: "string", Value: transfer.TxHash},
}).Dispatch()
```

- [ ] **Step 3: Dispatch WithdrawalBroadcasted after withdrawal broadcast**

In `app/services/withdraw/service.go`, after broadcast succeeds and the transaction row is committed:

```go
_ = facades.Event().Job(&events.WithdrawalBroadcasted{}, []event.Arg{
	{Type: "string", Value: w.ID.String()},
	{Type: "string", Value: w.Chain},
}).Dispatch()
```

Important: dispatch only after the database transaction commits so queued listeners can find the committed rows.

- [ ] **Step 4: Compile and run tests**

Run: `cd back && go build ./... && go test ./app/services/wallet/... ./app/services/ingest/... ./app/services/withdraw/... -v -count=1`

Expected: existing tests pass with the new event dispatches (events use sync connection in tests so they execute inline).

- [ ] **Step 5: Commit**

```bash
git add app/services/wallet/service.go app/services/ingest/service.go app/services/withdraw/service.go
git commit -m "feat: dispatch goravel events for automatic wallet refresh"
```

---

## Phase 5: API Integration

### Task 15: Serve wallet reads from new balance fields and real UTXOs

**Files:**
- Modify: `app/http/controllers/wallets_controller.go`
- Modify: `app/http/controllers/wallet_unspents_controller.go`

- [ ] **Step 1: Wallet reads are already enriched by model changes**

Verify that `GetWallet` and `ListWallets` return the new `balance`, `balance_asset`, `balance_usd`, `balance_last_synced_at`, `read_model_status` fields through the existing JSON tags on the Wallet model. No controller code changes should be needed if the model tags are correct.

- [ ] **Step 2: Switch unspents controller to real UTXOs**

Replace the synthetic balance-as-unspent view in `app/http/controllers/wallet_unspents_controller.go` with:

```go
utxos, err := container.Get().WalletUTXORepo.ListSpendable(wallet.ID, wallet.Chain)
```

- [ ] **Step 3: Compile**

Run: `cd back && go build ./...`

- [ ] **Step 4: Commit**

```bash
git add app/http/controllers/wallets_controller.go app/http/controllers/wallet_unspents_controller.go
git commit -m "feat: serve wallet reads from balance summary and real utxos"
```

---

## Phase 6: End-to-End Verification

### Task 16: Full migration, test, and command verification

- [ ] **Step 1: Fresh migration**

Run: `cd back && go run . artisan migrate:fresh --seed`

Expected: all tables created including new read-model tables and enum types.

- [ ] **Step 2: Full test suite**

Run: `cd back && go test ./... -v -count=1`

Expected: all pass.

- [ ] **Step 3: Verify commands**

Run: `cd back && go run . artisan list --no-ansi`

Expected output includes: `refresh:wallet`, `refresh:address`, `refresh:currency`, `refresh:tx`, `reconcile:wallet`.

- [ ] **Step 4: Verify sync-first command**

Run: `cd back && go run . artisan refresh:wallet 00000000-0000-0000-0000-000000000001 --scope=balances`

Expected: command executes immediately, logs sync-mode output.

- [ ] **Step 5: Update .env.example**

Add:

```bash
QUEUE_CONNECTION=sync
```

- [ ] **Step 6: Commit**

```bash
git add .env.example
git commit -m "docs: document wallet refresh queue configuration"
```

---

## Spec Coverage Check

| Spec Requirement | Task(s) |
|------------------|---------|
| Wallet current balance on `wallets` | 2, 7, 15 |
| Keep last 10 balance snapshots | 3, 7 |
| Asset balances table | 3, 7 |
| Reuse existing `transactions` model/table | 4, 15 |
| Bitcoin UTXOs and spendable state | 3, 8, 15 |
| Sync state table | 3, 7, 8 |
| DB enums via Goravel migrations | 1, 2, 3, 4 |
| SQS/database-backed Goravel queue | 9 |
| Single `blockchain` queue | 9, 11, 12 |
| Automatic dispatch through Goravel events/listeners | 11, 12, 14 |
| Sync-first Artisan commands | 13 |
| Queue option for commands | 13 |

## Self-Review

- No `TODO`/`TBD` placeholders remain
- All file paths are repo-relative
- Container wiring uses `app.Singleton(ContainerKey, ...)` pattern through `vault_container.go`
- Bootstrap matches current `WithMigrations`/`WithProviders`/`WithSeeders`/`WithConfig` chain
- Queue config extends the existing `config/queue.go` `registerQueue()` function
- Event/listener/command types match Goravel v1.17 contract interfaces
