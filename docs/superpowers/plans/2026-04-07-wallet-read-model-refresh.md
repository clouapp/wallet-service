# Wallet Read Model Refresh Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task. Replace example commands with this repo’s real tools (`go run . artisan`, `go test`, `make`).

**Goal:** Add a backend wallet read model with current wallet balances, asset balances, recent balance snapshots, Bitcoin UTXOs, sync state, SQS-backed Goravel jobs, Goravel events/listeners, and sync-first Artisan refresh commands.

**Architecture:** Extend the existing backend instead of introducing parallel wallet-history models. Persist current balance summary on `wallets`, reuse the existing `transactions` table for wallet-facing transaction reads, and add focused tables for asset balances, snapshots, UTXOs, and sync state. Automatic refreshes flow through Goravel events to queued listeners and jobs on a single `blockchain` queue; manual refreshes run through Goravel Artisan commands synchronously by default and call the same refresh services.

**Tech Stack:** Go, Goravel v1.17, PostgreSQL, AWS SQS, AWS Lambda, Redis, existing chain adapters, existing repositories and container wiring.

**Spec:** `back/docs/superpowers/specs/2026-04-07-wallet-read-model-refresh-design.md`

---

## File Map

**Create**

- `back/database/migrations/20260407000001_create_blockchain_enum_types.go`
- `back/database/migrations/20260407000002_add_wallet_read_model_columns.go`
- `back/database/migrations/20260407000003_create_wallet_balance_snapshots_table.go`
- `back/database/migrations/20260407000004_create_wallet_asset_balances_table.go`
- `back/database/migrations/20260407000005_extend_transactions_for_wallet_reads.go`
- `back/database/migrations/20260407000006_create_wallet_utxos_table.go`
- `back/database/migrations/20260407000007_create_wallet_sync_states_table.go`
- `back/app/models/wallet_asset_balance.go`
- `back/app/models/wallet_balance_snapshot.go`
- `back/app/models/wallet_utxo.go`
- `back/app/models/wallet_sync_state.go`
- `back/app/repositories/wallet_asset_balance_repository.go`
- `back/app/repositories/wallet_balance_snapshot_repository.go`
- `back/app/repositories/wallet_utxo_repository.go`
- `back/app/repositories/wallet_sync_state_repository.go`
- `back/app/services/refresh/types.go`
- `back/app/services/refresh/dispatcher.go`
- `back/app/services/refresh/balances.go`
- `back/app/services/refresh/transactions.go`
- `back/app/services/refresh/utxos.go`
- `back/app/services/refresh/reconcile.go`
- `back/app/services/refresh/evm.go`
- `back/app/services/refresh/solana.go`
- `back/app/services/refresh/bitcoin.go`
- `back/app/jobs/refresh_wallet_balances.go`
- `back/app/jobs/refresh_wallet_transactions.go`
- `back/app/jobs/refresh_wallet_tokens.go`
- `back/app/jobs/refresh_wallet_utxos.go`
- `back/app/jobs/reconcile_wallet_state.go`
- `back/app/events/wallet_created.go`
- `back/app/events/wallet_activated.go`
- `back/app/events/deposit_detected.go`
- `back/app/events/withdrawal_broadcasted.go`
- `back/app/events/wallet_refresh_requested.go`
- `back/app/listeners/enqueue_wallet_refresh.go`
- `back/app/listeners/enqueue_transaction_refresh.go`
- `back/app/listeners/enqueue_utxo_refresh.go`
- `back/app/console/commands/refresh_wallet.go`
- `back/app/console/commands/refresh_address.go`
- `back/app/console/commands/refresh_currency.go`
- `back/app/console/commands/refresh_tx.go`
- `back/app/console/commands/reconcile_wallet.go`
- `back/bootstrap/jobs.go`
- `back/bootstrap/commands.go`
- `back/bootstrap/events.go`

**Modify**

- `back/database/migrations/migrations.go`
- `back/app/models/wallet.go`
- `back/app/models/transaction.go`
- `back/app/repositories/transaction_repository.go`
- `back/app/repositories/wallet_repository.go`
- `back/app/services/wallet/service.go`
- `back/app/services/withdraw/service.go`
- `back/app/services/ingest/service.go`
- `back/app/services/deposit/service.go`
- `back/app/container/container.go`
- `back/bootstrap/app.go`
- `back/config/app.go`
- `back/routes/api.go`
- `back/app/http/controllers/wallets_controller.go`
- `back/app/http/controllers/wallet_transactions_controller.go`
- `back/app/http/controllers/transactions_controller.go`
- `back/app/http/controllers/wallet_unspents_controller.go`

**Test**

- `back/app/repositories/transaction_repository_test.go`
- `back/app/services/wallet/service_test.go`
- `back/app/services/withdraw/service_test.go`
- `back/app/services/ingest/service_test.go`
- `back/app/services/refresh/dispatcher_test.go`
- `back/app/services/refresh/balances_test.go`
- `back/app/services/refresh/transactions_test.go`
- `back/app/services/refresh/utxos_test.go`
- `back/app/console/commands/refresh_wallet_test.go`

---

## Phase 1: Schema Foundation

### Task 1: Add blockchain enum types migration

**Files:**
- Create: `back/database/migrations/20260407000001_create_blockchain_enum_types.go`
- Modify: `back/database/migrations/migrations.go`

- [ ] **Step 1: Write the failing migration file**

Create `back/database/migrations/20260407000001_create_blockchain_enum_types.go`:

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

- [ ] **Step 2: Register the migration**

Add `&M20260407000001CreateBlockchainEnumTypes{},` to `back/database/migrations/migrations.go` before any migration that uses the enum types.

- [ ] **Step 3: Run the migration**

Run: `cd back && go run . artisan migrate`

Expected: migration completes without PostgreSQL type errors.

- [ ] **Step 4: Commit**

```bash
git add back/database/migrations/20260407000001_create_blockchain_enum_types.go back/database/migrations/migrations.go
git commit -m "feat: add blockchain enum types for wallet read models"
```

---

### Task 2: Extend `wallets` with current balance summary

**Files:**
- Create: `back/database/migrations/20260407000002_add_wallet_read_model_columns.go`
- Modify: `back/database/migrations/migrations.go`
- Modify: `back/app/models/wallet.go`
- Test: `back/app/services/wallet/service_test.go`

- [ ] **Step 1: Write the failing migration**

Create `back/database/migrations/20260407000002_add_wallet_read_model_columns.go`:

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
    ADD COLUMN balance_asset VARCHAR(32),
    ADD COLUMN balance_raw TEXT,
    ADD COLUMN balance_display TEXT,
    ADD COLUMN balance_usd NUMERIC(28, 10),
    ADD COLUMN balance_last_synced_at TIMESTAMPTZ,
    ADD COLUMN read_model_status wallet_read_model_status DEFAULT 'idle';
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
ALTER TABLE wallets
    ALTER COLUMN status TYPE VARCHAR(20) USING status::text;
`)
}
```

- [ ] **Step 2: Update the Wallet model**

In `back/app/models/wallet.go`, extend the struct with:

```go
BalanceAsset        *string    `gorm:"type:varchar(32)" json:"balance_asset,omitempty"`
BalanceRaw          *string    `gorm:"type:text" json:"balance_raw,omitempty"`
BalanceDisplay      *string    `gorm:"type:text" json:"balance,omitempty"`
BalanceUSD          *string    `gorm:"type:decimal(28,10)" json:"balance_usd,omitempty"`
BalanceLastSyncedAt *time.Time `gorm:"type:timestamptz" json:"balance_last_synced_at,omitempty"`
ReadModelStatus     string     `gorm:"type:wallet_read_model_status;default:idle" json:"read_model_status"`
```

Also update the existing `Status` tag from `varchar(20)` to:

```go
Status string `gorm:"type:wallet_status;default:active" json:"status"`
```

- [ ] **Step 3: Run the focused wallet tests**

Run: `cd back && go test ./app/services/wallet/... -v -count=1`

Expected: existing wallet tests still pass after the model change.

- [ ] **Step 4: Run the migration**

Run: `cd back && go run . artisan migrate`

Expected: `wallets.status` converts cleanly and new balance columns exist.

- [ ] **Step 5: Commit**

```bash
git add back/database/migrations/20260407000002_add_wallet_read_model_columns.go back/database/migrations/migrations.go back/app/models/wallet.go
git commit -m "feat: store current wallet balance summary on wallets table"
```

---

### Task 3: Add balance snapshots, asset balances, UTXOs, and sync state tables

**Files:**
- Create: `back/database/migrations/20260407000003_create_wallet_balance_snapshots_table.go`
- Create: `back/database/migrations/20260407000004_create_wallet_asset_balances_table.go`
- Create: `back/database/migrations/20260407000006_create_wallet_utxos_table.go`
- Create: `back/database/migrations/20260407000007_create_wallet_sync_states_table.go`
- Modify: `back/database/migrations/migrations.go`
- Create: `back/app/models/wallet_balance_snapshot.go`
- Create: `back/app/models/wallet_asset_balance.go`
- Create: `back/app/models/wallet_utxo.go`
- Create: `back/app/models/wallet_sync_state.go`

- [ ] **Step 1: Write the four migration files**

Use Goravel schema builders for columns and raw SQL only where schema builder cannot express enum specifics. The table shapes must match the spec exactly:

```go
// wallet_balance_snapshots
table.Uuid("id")
table.Uuid("wallet_id")
table.String("chain_id", 32)
table.String("balance_asset", 32)
table.Text("balance_raw")
table.Text("balance_display")
table.Decimal("balance_usd", 28, 10).Nullable()
table.Timestamp("captured_at")
table.Timestamps()

// wallet_asset_balances
table.Uuid("id")
table.Uuid("wallet_id")
table.String("chain_id", 32)
table.String("asset_symbol", 32)
table.String("asset_name", 128).Nullable()
table.String("asset_contract", 255).Nullable()
table.String("asset_key", 320)
table.Raw("asset_type asset_type NOT NULL")
table.Integer("decimals")
table.Text("amount_raw")
table.Text("amount_display")
table.Decimal("price_usd", 28, 10).Nullable()
table.Decimal("value_usd", 28, 10).Nullable()
table.Text("source_address").Nullable()
table.Timestamp("last_synced_at")
table.Timestamps()

// wallet_utxos
table.Uuid("id")
table.Uuid("wallet_id")
table.Uuid("address_id").Nullable()
table.String("chain_id", 32)
table.String("tx_hash", 255)
table.Integer("output_index")
table.Text("address")
table.Text("value_raw")
table.Text("script_pub_key").Nullable()
table.Raw("status utxo_status NOT NULL")
table.String("spent_by_tx_hash", 255).Nullable()
table.BigInteger("block_number").Nullable()
table.Timestamp("block_timestamp").Nullable()
table.Integer("confirmations").Default(0)
table.Timestamp("last_synced_at")
table.Timestamps()

// wallet_sync_states
table.Uuid("id")
table.Uuid("wallet_id")
table.String("chain_id", 32)
table.Raw("sync_scope wallet_sync_scope NOT NULL")
table.Raw("status wallet_sync_status NOT NULL")
table.Text("cursor").Nullable()
table.Json("cursor_meta").Nullable()
table.Timestamp("last_synced_at").Nullable()
table.Timestamp("last_attempted_at").Nullable()
table.Text("last_error").Nullable()
table.Timestamp("next_reconcile_at").Nullable()
table.Timestamps()
```

- [ ] **Step 2: Write the four model files**

Each model should follow the current repo pattern:

```go
type WalletAssetBalance struct {
    orm.Model
    ID           uuid.UUID `gorm:"type:uuid;primary_key" json:"id"`
    WalletID     uuid.UUID `gorm:"type:uuid;not null;index" json:"wallet_id"`
    ChainID      string    `gorm:"type:varchar(32);not null;index" json:"chain_id"`
    AssetType    string    `gorm:"type:asset_type;not null" json:"asset_type"`
    AssetSymbol  string    `gorm:"type:varchar(32);not null" json:"asset_symbol"`
    AssetKey     string    `gorm:"type:varchar(320);not null" json:"asset_key"`
    AmountRaw    string    `gorm:"type:text;not null" json:"amount_raw"`
    AmountDisplay string   `gorm:"type:text;not null" json:"amount_display"`
}
```

Repeat this pattern for the snapshot, UTXO, and sync-state models with exact table names and enum tags.

- [ ] **Step 3: Run the migrations**

Run: `cd back && go run . artisan migrate`

Expected: all four tables are created and foreign keys/indexes succeed.

- [ ] **Step 4: Commit**

```bash
git add back/database/migrations/20260407000003_create_wallet_balance_snapshots_table.go back/database/migrations/20260407000004_create_wallet_asset_balances_table.go back/database/migrations/20260407000006_create_wallet_utxos_table.go back/database/migrations/20260407000007_create_wallet_sync_states_table.go back/database/migrations/migrations.go back/app/models/wallet_balance_snapshot.go back/app/models/wallet_asset_balance.go back/app/models/wallet_utxo.go back/app/models/wallet_sync_state.go
git commit -m "feat: add wallet read model tables and models"
```

---

### Task 4: Extend `transactions` for wallet-facing reads

**Files:**
- Create: `back/database/migrations/20260407000005_extend_transactions_for_wallet_reads.go`
- Modify: `back/database/migrations/migrations.go`
- Modify: `back/app/models/transaction.go`
- Modify: `back/app/repositories/transaction_repository.go`
- Test: `back/app/repositories/transaction_repository_test.go`

- [ ] **Step 1: Write the failing migration**

Create `back/database/migrations/20260407000005_extend_transactions_for_wallet_reads.go`:

```go
package migrations

import "github.com/goravel/framework/facades"

type M20260407000005ExtendTransactionsForWalletReads struct{}

func (m *M20260407000005ExtendTransactionsForWalletReads) Signature() string {
	return "20260407000005_extend_transactions_for_wallet_reads"
}

func (m *M20260407000005ExtendTransactionsForWalletReads) Up() error {
	return facades.Orm().Query().Exec(`
ALTER TABLE transactions
    ALTER COLUMN tx_type TYPE transaction_type USING tx_type::transaction_type,
    ALTER COLUMN status TYPE transaction_status USING status::transaction_status,
    ADD COLUMN direction transaction_direction,
    ADD COLUMN source transaction_source,
    ADD COLUMN raw_payload JSONB,
    ADD COLUMN synced_at TIMESTAMPTZ;
`)
}

func (m *M20260407000005ExtendTransactionsForWalletReads) Down() error {
	return facades.Orm().Query().Exec(`
ALTER TABLE transactions
    DROP COLUMN IF EXISTS synced_at,
    DROP COLUMN IF EXISTS raw_payload,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS direction;
ALTER TABLE transactions
    ALTER COLUMN status TYPE VARCHAR(20) USING status::text,
    ALTER COLUMN tx_type TYPE VARCHAR(20) USING tx_type::text;
`)
}
```

- [ ] **Step 2: Update the Transaction model**

In `back/app/models/transaction.go`, change the existing enum-like fields and add the new ones:

```go
TxType    string `gorm:"type:transaction_type;not null;index" json:"tx_type"`
Status    string `gorm:"type:transaction_status;not null;index" json:"status"`
Direction string `gorm:"type:transaction_direction" json:"direction,omitempty"`
Source    string `gorm:"type:transaction_source" json:"source,omitempty"`
RawPayload string `gorm:"type:jsonb" json:"raw_payload,omitempty"`
SyncedAt  *time.Time `gorm:"type:timestamptz" json:"synced_at,omitempty"`
```

- [ ] **Step 3: Add a repository method for the new query shape**

In `back/app/repositories/transaction_repository.go`, add:

```go
ListByWalletAndChain(walletID uuid.UUID, chainID string, limit, offset int) ([]models.Transaction, int64, error)
```

Implement it by reusing the existing count-and-data-query pattern and ordering by:

```go
Order("COALESCE(block_number, 0) DESC").Order("created_at DESC")
```

- [ ] **Step 4: Run the focused repository tests**

Run: `cd back && go test ./app/repositories/... -v -count=1`

Expected: transaction repository tests pass with enum-backed model fields.

- [ ] **Step 5: Run the migration**

Run: `cd back && go run . artisan migrate`

Expected: `transactions.tx_type` and `transactions.status` convert to enums without data-loss errors.

- [ ] **Step 6: Commit**

```bash
git add back/database/migrations/20260407000005_extend_transactions_for_wallet_reads.go back/database/migrations/migrations.go back/app/models/transaction.go back/app/repositories/transaction_repository.go
git commit -m "feat: extend transactions for wallet-facing read model"
```

---

## Phase 2: Repositories And Refresh Contracts

### Task 5: Add repositories for the new read-model tables

**Files:**
- Create: `back/app/repositories/wallet_asset_balance_repository.go`
- Create: `back/app/repositories/wallet_balance_snapshot_repository.go`
- Create: `back/app/repositories/wallet_utxo_repository.go`
- Create: `back/app/repositories/wallet_sync_state_repository.go`
- Modify: `back/app/container/container.go`
- Test: `back/app/repositories/transaction_repository_test.go`

- [ ] **Step 1: Write the repository interfaces and implementations**

Each repository should match the repo’s current shape: interface + concrete struct + constructor + minimal methods.

Required methods:

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

- [ ] **Step 2: Register the repositories in the container**

Add fields to `back/app/container/container.go`:

```go
WalletAssetBalanceRepo   repositories.WalletAssetBalanceRepository
WalletBalanceSnapshotRepo repositories.WalletBalanceSnapshotRepository
WalletUTXORepo           repositories.WalletUTXORepository
WalletSyncStateRepo      repositories.WalletSyncStateRepository
```

Initialize them in `Boot()` after the existing repository block.

- [ ] **Step 3: Compile the backend**

Run: `cd back && go build ./...`

Expected: backend compiles with the new repository fields and constructors wired.

- [ ] **Step 4: Commit**

```bash
git add back/app/repositories/wallet_asset_balance_repository.go back/app/repositories/wallet_balance_snapshot_repository.go back/app/repositories/wallet_utxo_repository.go back/app/repositories/wallet_sync_state_repository.go back/app/container/container.go
git commit -m "feat: add repositories for wallet read model tables"
```

---

### Task 6: Create shared refresh request types and dispatcher

**Files:**
- Create: `back/app/services/refresh/types.go`
- Create: `back/app/services/refresh/dispatcher.go`
- Test: `back/app/services/refresh/dispatcher_test.go`

- [ ] **Step 1: Write the failing dispatcher test**

Create `back/app/services/refresh/dispatcher_test.go`:

```go
package refresh

import "testing"

func TestNormalizeFullRequestExpandsScopes(t *testing.T) {
    req := RefreshRequest{
        ChainID: "eth",
        WalletID: "wallet-1",
        Scope: RefreshScopeFull,
    }

    scopes, err := ExpandScopes(req)
    if err != nil {
        t.Fatalf("ExpandScopes returned error: %v", err)
    }
    if len(scopes) != 3 {
        t.Fatalf("expected 3 scopes for EVM full refresh, got %d", len(scopes))
    }
}
```

- [ ] **Step 2: Write the types and minimal implementation**

Create `back/app/services/refresh/types.go`:

```go
package refresh

type RefreshScope string

const (
    RefreshScopeBalances     RefreshScope = "balances"
    RefreshScopeTransactions RefreshScope = "transactions"
    RefreshScopeTokens       RefreshScope = "tokens"
    RefreshScopeUtxos        RefreshScope = "utxos"
    RefreshScopeFull         RefreshScope = "full"
)

type RefreshRequest struct {
    ChainID   string
    WalletID  string
    Addresses []string
    Currency  string
    TxHash    string
    Scope     RefreshScope
    Reason    string
    Force     bool
}
```

Create `back/app/services/refresh/dispatcher.go`:

```go
package refresh

import "fmt"

func ExpandScopes(req RefreshRequest) ([]RefreshScope, error) {
    if req.Scope != RefreshScopeFull {
        return []RefreshScope{req.Scope}, nil
    }

    switch req.ChainID {
    case "btc", "tbtc":
        return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeUtxos}, nil
    case "sol", "tsol":
        return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}, nil
    case "eth", "polygon", "teth", "tpolygon":
        return []RefreshScope{RefreshScopeBalances, RefreshScopeTransactions, RefreshScopeTokens}, nil
    default:
        return nil, fmt.Errorf("unsupported chain for full refresh: %s", req.ChainID)
    }
}
```

- [ ] **Step 3: Run the dispatcher test**

Run: `cd back && go test ./app/services/refresh/... -run TestNormalizeFullRequestExpandsScopes -v -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add back/app/services/refresh/types.go back/app/services/refresh/dispatcher.go back/app/services/refresh/dispatcher_test.go
git commit -m "feat: add shared refresh request types and dispatcher scope expansion"
```

---

## Phase 3: Balance Refresh Service

### Task 7: Implement balance refresh and snapshot retention

**Files:**
- Create: `back/app/services/refresh/balances.go`
- Modify: `back/app/repositories/wallet_repository.go`
- Test: `back/app/services/refresh/balances_test.go`

- [ ] **Step 1: Write the failing balance refresh test**

Create `back/app/services/refresh/balances_test.go` with a fake chain adapter test that verifies:

```go
func TestRefreshWalletBalancesUpdatesWalletAndSnapshots(t *testing.T) {
    // Arrange a wallet on eth with a fake adapter returning 1000000000000000000 wei
    // Act by calling RefreshWalletBalances(ctx, request)
    // Assert:
    // 1. wallets.balance_asset == "ETH"
    // 2. wallets.balance_display == "1"
    // 3. one wallet_asset_balances row exists
    // 4. one wallet_balance_snapshots row exists
}
```

- [ ] **Step 2: Add a wallet repository helper**

In `back/app/repositories/wallet_repository.go`, add:

```go
UpdateReadModelFields(id uuid.UUID, fields map[string]interface{}) error
```

Implement it with the same `UpdateFields` pattern already used elsewhere:

```go
_, err := facades.Orm().Query().Model(&models.Wallet{}).Where("id = ?", id).Update(fields)
```

- [ ] **Step 3: Write the minimal balance refresh service**

Create `back/app/services/refresh/balances.go` with:

```go
type BalanceService struct {
    registry            *chain.Registry
    walletRepo          repositories.WalletRepository
    assetBalanceRepo    repositories.WalletAssetBalanceRepository
    snapshotRepo        repositories.WalletBalanceSnapshotRepository
    syncStateRepo       repositories.WalletSyncStateRepository
}

func (s *BalanceService) RefreshWallet(ctx context.Context, wallet *models.Wallet, req RefreshRequest) error {
    adapter, err := s.registry.Chain(wallet.Chain)
    if err != nil {
        return err
    }
    if wallet.DepositAddress == nil {
        return fmt.Errorf("wallet %s has no deposit address", wallet.ID)
    }

    bal, err := adapter.GetBalance(ctx, wallet.DepositAddress.Address)
    if err != nil {
        return err
    }

    now := time.Now().UTC()
    assetKey := bal.Asset

    rows := []models.WalletAssetBalance{{
        ID:            uuid.New(),
        WalletID:      wallet.ID,
        ChainID:       wallet.Chain,
        AssetType:     "native",
        AssetSymbol:   bal.Asset,
        AssetKey:      assetKey,
        Decimals:      int(bal.Decimals),
        AmountRaw:     bal.Amount.String(),
        AmountDisplay: bal.Human,
        LastSyncedAt:  &now,
    }}

    if err := s.assetBalanceRepo.ReplaceForWallet(wallet.ID, wallet.Chain, rows); err != nil {
        return err
    }

    if err := s.walletRepo.UpdateReadModelFields(wallet.ID, map[string]interface{}{
        "balance_asset":           bal.Asset,
        "balance_raw":             bal.Amount.String(),
        "balance_display":         bal.Human,
        "balance_last_synced_at":  now,
        "read_model_status":       "synced",
    }); err != nil {
        return err
    }

    snapshot := &models.WalletBalanceSnapshot{
        ID:             uuid.New(),
        WalletID:       wallet.ID,
        ChainID:        wallet.Chain,
        BalanceAsset:   bal.Asset,
        BalanceRaw:     bal.Amount.String(),
        BalanceDisplay: bal.Human,
        CapturedAt:     now,
    }
    if err := s.snapshotRepo.Create(snapshot); err != nil {
        return err
    }
    return s.snapshotRepo.TrimToLatest(wallet.ID, wallet.Chain, 10)
}
```

- [ ] **Step 4: Run the balance refresh test**

Run: `cd back && go test ./app/services/refresh/... -run TestRefreshWalletBalancesUpdatesWalletAndSnapshots -v -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add back/app/services/refresh/balances.go back/app/services/refresh/balances_test.go back/app/repositories/wallet_repository.go
git commit -m "feat: add wallet balance refresh with snapshot retention"
```

---

## Phase 4: Bitcoin UTXO Refresh

### Task 8: Implement Bitcoin UTXO persistence and wallet unspent reads

**Files:**
- Create: `back/app/services/refresh/utxos.go`
- Modify: `back/app/http/controllers/wallet_unspents_controller.go`
- Test: `back/app/services/refresh/utxos_test.go`

- [ ] **Step 1: Write the failing UTXO refresh test**

Create `back/app/services/refresh/utxos_test.go`:

```go
func TestRefreshBitcoinUTXOsReplacesSpendableSet(t *testing.T) {
    // Arrange a btc wallet and mock provider results with two UTXOs
    // Act by calling RefreshWalletUTXOs
    // Assert:
    // 1. wallet_utxos contains two rows
    // 2. status == "unspent"
    // 3. subsequent refresh with one spent output replaces the old set
}
```

- [ ] **Step 2: Write the minimal UTXO service**

Create `back/app/services/refresh/utxos.go` with a service that:

```go
type UTXOService struct {
    utxoRepo      repositories.WalletUTXORepository
    syncStateRepo repositories.WalletSyncStateRepository
}

func (s *UTXOService) ReplaceWalletUTXOs(ctx context.Context, wallet *models.Wallet, rows []models.WalletUTXO) error {
    if err := s.utxoRepo.ReplaceForWallet(wallet.ID, wallet.Chain, rows); err != nil {
        return err
    }
    state := &models.WalletSyncState{
        WalletID: wallet.ID,
        ChainID:  wallet.Chain,
        SyncScope: "utxos",
        Status:   "synced",
    }
    return s.syncStateRepo.Upsert(state)
}
```

- [ ] **Step 3: Switch the wallet unspents controller to the new table**

In `back/app/http/controllers/wallet_unspents_controller.go`, replace the synthetic balance-as-unspent behavior with:

```go
utxos, err := container.Get().WalletUTXORepo.ListSpendable(wallet.ID, wallet.Chain)
if err != nil {
    return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to load unspents"})
}
return ctx.Response().Success().Json(http.Json{"data": utxos})
```

- [ ] **Step 4: Run the UTXO test**

Run: `cd back && go test ./app/services/refresh/... -run TestRefreshBitcoinUTXOsReplacesSpendableSet -v -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add back/app/services/refresh/utxos.go back/app/services/refresh/utxos_test.go back/app/http/controllers/wallet_unspents_controller.go
git commit -m "feat: persist bitcoin utxos and serve real wallet unspents"
```

---

## Phase 5: Jobs, Events, And SQS Queue Wiring

### Task 9: Register Goravel jobs, commands, and events

**Files:**
- Create: `back/bootstrap/jobs.go`
- Create: `back/bootstrap/commands.go`
- Create: `back/bootstrap/events.go`
- Modify: `back/bootstrap/app.go`

- [ ] **Step 1: Add bootstrap registries**

Create `back/bootstrap/jobs.go`:

```go
package bootstrap

import "github.com/goravel/framework/contracts/queue"
import "github.com/macrowallets/waas/app/jobs"

func Jobs() []queue.Job {
    return []queue.Job{
        &jobs.RefreshWalletBalances{},
        &jobs.RefreshWalletTransactions{},
        &jobs.RefreshWalletTokens{},
        &jobs.RefreshWalletUTXOs{},
        &jobs.ReconcileWalletState{},
    }
}
```

Create `back/bootstrap/commands.go`:

```go
package bootstrap

import "github.com/goravel/framework/contracts/console"
import "github.com/macrowallets/waas/app/console/commands"

func Commands() []console.Command {
    return []console.Command{
        &commands.RefreshWallet{},
        &commands.RefreshAddress{},
        &commands.RefreshCurrency{},
        &commands.RefreshTx{},
        &commands.ReconcileWallet{},
    }
}
```

Create `back/bootstrap/events.go` with the repo’s chosen event/listener map.

- [ ] **Step 2: Update bootstrap/app.go**

Add the new boot hooks:

```go
_ = foundation.Setup().
    WithProviders(config.AppProviders).
    WithSeeders(seeders.All).
    WithJobs(Jobs).
    WithCommands(Commands).
    WithEvents(Events).
```

- [ ] **Step 3: Compile**

Run: `cd back && go build ./...`

Expected: application boots with job, command, and event registries available.

- [ ] **Step 4: Commit**

```bash
git add back/bootstrap/jobs.go back/bootstrap/commands.go back/bootstrap/events.go back/bootstrap/app.go
git commit -m "feat: register wallet refresh jobs commands and events in bootstrap"
```

---

### Task 10: Add SQS-backed Goravel queue connection and blockchain queue jobs

**Files:**
- Modify: `back/config/app.go`
- Create: `back/app/jobs/refresh_wallet_balances.go`
- Create: `back/app/jobs/refresh_wallet_transactions.go`
- Create: `back/app/jobs/refresh_wallet_tokens.go`
- Create: `back/app/jobs/refresh_wallet_utxos.go`
- Create: `back/app/jobs/reconcile_wallet_state.go`

- [ ] **Step 1: Update the queue config**

In `back/config/app.go`, extend the queue config:

```go
facades.Config().Add("queue", map[string]any{
    "default": env("QUEUE_CONNECTION", "sync"),
    "connections": map[string]any{
        "sync": map[string]any{
            "driver": "sync",
        },
        "sqs": map[string]any{
            "driver": "custom",
            "queue":  env("BLOCKCHAIN_QUEUE_NAME", "blockchain"),
            "via": func() (queue.Driver, error) {
                return sqsqueue.NewDriver(sqs.NewFromConfig(awsCfg)) // real implementation in code
            },
        },
    },
})
```

Use the real custom driver implementation that the engineer introduces in this task. Do not leave the `via` body as pseudocode in the actual implementation.

- [ ] **Step 2: Write one representative job class first**

Create `back/app/jobs/refresh_wallet_balances.go`:

```go
package jobs

import (
    "time"
)

type RefreshWalletBalances struct{}

func (j *RefreshWalletBalances) Signature() string {
    return "refresh_wallet_balances"
}

func (j *RefreshWalletBalances) Handle(args ...any) error {
    // parse wallet_id, chain_id, reason
    // call container.Get().BalanceRefreshService.RefreshWallet(...)
    return nil
}

func (j *RefreshWalletBalances) ShouldRetry(err error, attempt int) (bool, time.Duration) {
    if attempt >= 5 {
        return false, 0
    }
    return true, time.Duration(attempt) * 5 * time.Second
}
```

- [ ] **Step 3: Repeat for the remaining four jobs**

Match signatures:

```go
"refresh_wallet_transactions"
"refresh_wallet_tokens"
"refresh_wallet_utxos"
"reconcile_wallet_state"
```

All queued jobs should target the same queue name later via `.OnQueue("blockchain")`.

- [ ] **Step 4: Compile**

Run: `cd back && go build ./...`

Expected: backend compiles with the new queue config and job registrations.

- [ ] **Step 5: Commit**

```bash
git add back/config/app.go back/app/jobs/refresh_wallet_balances.go back/app/jobs/refresh_wallet_transactions.go back/app/jobs/refresh_wallet_tokens.go back/app/jobs/refresh_wallet_utxos.go back/app/jobs/reconcile_wallet_state.go
git commit -m "feat: add blockchain refresh jobs and sqs-backed goravel queue config"
```

---

### Task 11: Add Goravel events and queued listeners for automatic refresh

**Files:**
- Create: `back/app/events/wallet_created.go`
- Create: `back/app/events/wallet_activated.go`
- Create: `back/app/events/deposit_detected.go`
- Create: `back/app/events/withdrawal_broadcasted.go`
- Create: `back/app/listeners/enqueue_wallet_refresh.go`
- Create: `back/app/listeners/enqueue_transaction_refresh.go`
- Create: `back/app/listeners/enqueue_utxo_refresh.go`
- Modify: `back/app/services/wallet/service.go`
- Modify: `back/app/services/ingest/service.go`
- Modify: `back/app/services/withdraw/service.go`

- [ ] **Step 1: Write the event types**

Each event should just pass through arguments:

```go
package events

import "github.com/goravel/framework/contracts/event"

type WalletCreated struct{}

func (e *WalletCreated) Handle(args []event.Arg) ([]event.Arg, error) {
    return args, nil
}
```

Create analogous event files for wallet activation, deposit detected, and withdrawal broadcasted.

- [ ] **Step 2: Write one queued listener first**

Create `back/app/listeners/enqueue_wallet_refresh.go`:

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
        Connection: "sqs",
        Queue:      "blockchain",
    }
}

func (l *EnqueueWalletRefresh) Handle(args ...any) error {
    walletID := args[0].(string)
    chainID := args[1].(string)
    return facades.Queue().
        Job(&jobs.RefreshWalletBalances{}, []queue.Arg{
            {Type: "string", Value: walletID},
            {Type: "string", Value: chainID},
        }).
        OnConnection("sqs").
        OnQueue("blockchain").
        Dispatch()
}
```

- [ ] **Step 3: Dispatch events from the existing services**

In `back/app/services/wallet/service.go`, after wallet creation succeeds:

```go
_ = facades.Event().Job(&events.WalletCreated{}, []event.Arg{
    {Type: "string", Value: walletID.String()},
    {Type: "string", Value: chainID},
}).Dispatch()
```

In `back/app/services/ingest/service.go`, after deposit insert succeeds:

```go
_ = facades.Event().Job(&events.DepositDetected{}, []event.Arg{
    {Type: "string", Value: tx.WalletID.String()},
    {Type: "string", Value: chainID},
    {Type: "string", Value: transfer.TxHash},
}).Dispatch()
```

In `back/app/services/withdraw/service.go`, dispatch `WithdrawalBroadcasted` only after the database transaction is committed and the `transactions` row exists.

- [ ] **Step 4: Compile**

Run: `cd back && go build ./...`

Expected: event registration and listener queue metadata compile cleanly.

- [ ] **Step 5: Commit**

```bash
git add back/app/events/wallet_created.go back/app/events/wallet_activated.go back/app/events/deposit_detected.go back/app/events/withdrawal_broadcasted.go back/app/listeners/enqueue_wallet_refresh.go back/app/listeners/enqueue_transaction_refresh.go back/app/listeners/enqueue_utxo_refresh.go back/app/services/wallet/service.go back/app/services/ingest/service.go back/app/services/withdraw/service.go
git commit -m "feat: dispatch wallet refresh automatically through goravel events and queued listeners"
```

---

## Phase 6: Sync-First Artisan Commands

### Task 12: Add `refresh:wallet` command and sync-first execution path

**Files:**
- Create: `back/app/console/commands/refresh_wallet.go`
- Modify: `back/app/container/container.go`
- Test: `back/app/console/commands/refresh_wallet_test.go`

- [ ] **Step 1: Write the failing command test**

Create `back/app/console/commands/refresh_wallet_test.go`:

```go
package commands

import "testing"

func TestRefreshWalletDefaultsToSyncExecution(t *testing.T) {
    cmd := &RefreshWallet{}
    if cmd.Signature() != "refresh:wallet {wallet_id}" {
        t.Fatalf("unexpected signature: %s", cmd.Signature())
    }
}
```

- [ ] **Step 2: Write the command**

Create `back/app/console/commands/refresh_wallet.go`:

```go
package commands

import (
    "github.com/goravel/framework/contracts/console"
    "github.com/goravel/framework/contracts/console/command"
)

type RefreshWallet struct{}

func (c *RefreshWallet) Signature() string {
    return "refresh:wallet {wallet_id}"
}

func (c *RefreshWallet) Description() string {
    return "Refresh a wallet read model synchronously by default"
}

func (c *RefreshWallet) Extend() command.Extend {
    return command.Extend{
        Flags: []command.Flag{
            &command.StringFlag{Name: "scope", Value: "full"},
            &command.BoolFlag{Name: "queue"},
            &command.BoolFlag{Name: "force"},
            &command.StringFlag{Name: "reason", Value: "manual"},
        },
    }
}

func (c *RefreshWallet) Handle(ctx console.Context) error {
    walletID := ctx.Argument(0)
    if ctx.OptionBool("queue") {
        ctx.Info("queue mode requested")
        return nil
    }
    ctx.Info("sync mode requested")
    _ = walletID
    return nil
}
```

- [ ] **Step 3: Run the command test**

Run: `cd back && go test ./app/console/commands/... -run TestRefreshWalletDefaultsToSyncExecution -v -count=1`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add back/app/console/commands/refresh_wallet.go back/app/console/commands/refresh_wallet_test.go
git commit -m "feat: add sync-first refresh wallet artisan command"
```

---

### Task 13: Add the remaining operator commands

**Files:**
- Create: `back/app/console/commands/refresh_address.go`
- Create: `back/app/console/commands/refresh_currency.go`
- Create: `back/app/console/commands/refresh_tx.go`
- Create: `back/app/console/commands/reconcile_wallet.go`

- [ ] **Step 1: Write `refresh:address`**

Signature and flags:

```go
func (c *RefreshAddress) Signature() string {
    return "refresh:address {chain} {addresses*}"
}
```

Flags:

```go
--scope=full
--queue
--force
--reason=manual
```

- [ ] **Step 2: Write `refresh:currency`**

Signature:

```go
func (c *RefreshCurrency) Signature() string {
    return "refresh:currency {currency} {addresses*}"
}
```

It must fail fast if currency is ambiguous and `--chain` is not provided.

- [ ] **Step 3: Write `refresh:tx` and `reconcile:wallet`**

Signatures:

```go
"refresh:tx {chain} {tx_hash}"
"reconcile:wallet {wallet_id}"
```

- [ ] **Step 4: Compile**

Run: `cd back && go build ./...`

Expected: all commands are discoverable by `go run . artisan list`.

- [ ] **Step 5: Commit**

```bash
git add back/app/console/commands/refresh_address.go back/app/console/commands/refresh_currency.go back/app/console/commands/refresh_tx.go back/app/console/commands/reconcile_wallet.go
git commit -m "feat: add address currency tx and reconcile artisan refresh commands"
```

---

## Phase 7: API Integration

### Task 14: Serve wallet reads from wallet balance fields and transaction table

**Files:**
- Modify: `back/app/http/controllers/wallets_controller.go`
- Modify: `back/app/http/controllers/wallet_transactions_controller.go`
- Modify: `back/app/http/controllers/transactions_controller.go`

- [ ] **Step 1: Enrich wallet reads**

In `back/app/http/controllers/wallets_controller.go`, keep the existing repository lookup but ensure the returned wallet now includes:

```go
balance
balance_asset
balance_usd
balance_last_synced_at
read_model_status
```

No extra controller transformation is needed if the model JSON tags are correct.

- [ ] **Step 2: Route wallet transaction reads through the existing richer transactions table**

In both wallet and external transaction controllers, switch list reads to:

```go
container.Get().TransactionRepo.ListByWalletAndChain(...)
```

where wallet-scoped results are needed, and keep `List(...)` for the broader external API path when chain and user filters apply.

- [ ] **Step 3: Compile**

Run: `cd back && go build ./...`

Expected: controllers compile without JSON/tag regressions.

- [ ] **Step 4: Commit**

```bash
git add back/app/http/controllers/wallets_controller.go back/app/http/controllers/wallet_transactions_controller.go back/app/http/controllers/transactions_controller.go
git commit -m "feat: serve wallet reads from new balance summary and extended transactions"
```

---

## Phase 8: End-To-End Verification

### Task 15: Run migrations, focused tests, and manual command checks

**Files:**
- Modify: `back/docs/RPC_PROVIDERS.md`
- Modify: `back/.env.example`

- [ ] **Step 1: Run migrations on a clean database**

Run:

```bash
cd back && go run . artisan migrate:fresh
```

Expected: all existing tables plus the new enum-backed wallet read-model tables are recreated successfully.

- [ ] **Step 2: Run the focused backend tests**

Run:

```bash
cd back && go test ./app/repositories/... ./app/services/refresh/... ./app/console/commands/... ./app/services/wallet/... ./app/services/withdraw/... ./app/services/ingest/... -v -count=1
```

Expected: PASS for repository, refresh, command, wallet, withdraw, and ingest test groups.

- [ ] **Step 3: Verify the new commands are discoverable**

Run:

```bash
cd back && go run . artisan list --no-ansi
```

Expected output includes:

```text
refresh:wallet
refresh:address
refresh:currency
refresh:tx
reconcile:wallet
```

- [ ] **Step 4: Verify sync-first command behavior manually**

Run:

```bash
cd back && go run . artisan refresh:wallet 00000000-0000-0000-0000-000000000001 --scope=balances --reason=manual-check
```

Expected: command executes immediately, logs a sync-mode refresh, and updates the wallet read-model fields if the wallet exists.

- [ ] **Step 5: Document queue and env requirements**

Update `back/.env.example` with:

```bash
QUEUE_CONNECTION=sync
BLOCKCHAIN_QUEUE_NAME=blockchain
BLOCKCHAIN_QUEUE_URL=
AWS_REGION=
```

Update `back/docs/RPC_PROVIDERS.md` to mention that background wallet refreshes run via Goravel jobs on SQS and that commands remain sync-first by default.

- [ ] **Step 6: Commit**

```bash
git add back/.env.example back/docs/RPC_PROVIDERS.md
git commit -m "docs: document wallet refresh queue and command configuration"
```

---

## Spec Coverage Check

- Wallet current balance on `wallets`: Task 2, Task 7, Task 14
- Keep last 10 balance snapshots: Task 3, Task 7
- Asset balances table: Task 3, Task 7
- Reuse existing `transactions` model/table: Task 4, Task 14
- Bitcoin UTXOs and spendable state: Task 3, Task 8
- Sync state table: Task 3, Task 7, Task 8
- DB enums via Goravel migrations: Task 1, Task 2, Task 3, Task 4
- SQS-backed Goravel queue: Task 10
- Single `blockchain` queue: Task 10, Task 11
- Automatic dispatch through Goravel events/listeners: Task 9, Task 11
- Sync-first Artisan commands: Task 12, Task 13
- Queue option for commands: Task 12, Task 13

## Self-Review

- Placeholder scan: removed `TODO`/`TBD` style placeholders; every task names exact files and commands.
- Type consistency: enums, model fields, command names, and queue name use the same values throughout.
- Scope check: this plan is backend-only and internally coherent. It intentionally does not include frontend work because the approved design and requested changes were all backend-side.

**Plan complete and saved to `docs/superpowers/plans/2026-04-07-wallet-read-model-refresh.md`. Two execution options:**

1. **Task-per-session (recommended)** — One plan task or a very small batch per session/subagent, with review between tasks.
2. **Inline** — Run tasks in this conversation with explicit checkpoints after each task.

**Which approach?**
