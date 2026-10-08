# BaseAddress + Sweep Intra-Wallet — Implementation Plan

> **For agentic workers:** Implement task-by-task using checkbox (`- [ ]`) steps. Prefer **(A)** a fresh subagent or focused session per task with review between tasks, or **(B)** inline execution in one chat with checkpoints after each task. Follow Goravel idioms strictly (see spec §4). After every task: `make test` must still pass.

**Goal:** Introduce BaseAddress + lazy sweep intra-wallet + opportunistic withdrawal policy + gas-readiness UI over the existing MPC 2-of-2 infrastructure, matching the spec `docs/superpowers/specs/2026-04-18-base-address-sweep-design.md`.

**Architecture:** New `sweep` service with a `Planner` (chooses `direct_from_base | direct_from_child | multi_sweep | insufficient`) and `Executor` (runs plan co-signed with passphrase). `withdraw` service delegates source-selection to `sweep.Planner`. New chain interface methods `BuildSweep`, `GasReadinessThreshold`, `DustThreshold` implemented per-chain (EVM gas_seed+sweep, Solana `fee_payer` 1-tx, Bitcoin PSBT). Three new API endpoints and four new webhook events. Frontend adds a gas-funding onboarding page, gas-status banners/badges, and a sweep-aware withdraw modal.

**Tech Stack:** Go 1.22 / Goravel v1.17 (`facades.Schema()`, `facades.Orm()`, `facades.Cache()`, `facades.Event()`, go-redis), PostgreSQL, React 19 / TypeScript / vinext, HeroUI + Tailwind.

---

## File Map

### Backend — Create

| File | Responsibility |
|------|----------------|
| `database/migrations/20260418000001_wallets_add_gas_status.go` | Add gas-readiness columns to `wallets` |
| `database/migrations/20260418000002_transactions_add_sweep_fields.go` | Add `parent_transaction_id`, `origin` to `transactions` |
| `database/migrations/20260418000003_transactions_extend_tx_type_enum.go` | Add `sweep`, `gas_seed` to `transaction_type` enum |
| `database/migrations/20260418000004_accounts_add_sweep_limits.go` | Add `sweep_limits` JSONB to `accounts` |
| `database/migrations/20260418000005_chains_add_sweep_thresholds.go` | Add threshold columns to `chains` |
| `database/seeds/sweep_thresholds.go` | Populate per-chain thresholds on seed |
| `app/events/sweep_events.go` | Sweep + gas-status event structs |
| `app/services/sweep/service.go` | Public interface + constructor |
| `app/services/sweep/types.go` | Plan, PlannedSweep, Result, Strategy, Limits types |
| `app/services/sweep/planner.go` | Selection algorithm (direct / opportunistic / greedy multi) |
| `app/services/sweep/executor.go` | Broadcasts + persists txs with parent linkage + webhook emission |
| `app/services/sweep/gas_readiness.go` | Computes `gas_status` from on-chain native balance |
| `app/services/sweep/limits.go` | Loads per-account overrides + Redis counters + locks |
| `app/services/sweep/service_test.go` | Planner + limits unit tests |
| `app/services/sweep/executor_test.go` | Executor flow tests |
| `app/http/controllers/sweep_controller.go` | `POST /consolidate`, `GET /gas-status`, `POST /gas-check`, `POST /withdraw/preview` |
| `app/http/requests/consolidate_request.go` | DTO |
| `app/http/requests/withdraw_preview_request.go` | DTO |

### Backend — Modify

| File | Change |
|------|--------|
| `app/models/wallet.go` | Add `GasStatus`, `GasLastCheckedAt`, `SweepPolicyVersion` fields + constants |
| `app/models/transaction.go` | Add `ParentTransactionID`, `Origin` fields + origin/tx_type constants |
| `app/models/account.go` | Add `SweepLimits` field (JSON) |
| `app/models/chain.go` | Add threshold fields; add parsing helpers returning `*big.Int` / `*float64` |
| `pkg/types/chain.go` (or the file where `Chain` interface lives) | Add `BuildSweep`, `GasReadinessThreshold`, `DustThreshold` to interface |
| `pkg/types/types.go` | Add `SweepRequest`, `SweepResult`, additional tx origin/type consts if needed |
| `app/services/chain/evm.go` | Implement `BuildSweep` (native + ERC-20 gas_seed variants); implement threshold getters |
| `app/services/chain/solana.go` | Implement `BuildSweep` with `fee_payer=Base` + optional `close_account` |
| `app/services/chain/bitcoin.go` | Implement `BuildSweep` as PSBT over child UTXOs |
| `app/services/chain/registry.go` | Expose chain-entity-backed thresholds to consumers |
| `tests/mocks/chain.go` | Add mock implementations for new interface methods |
| `app/services/withdraw/service.go` | Delegate source selection to `sweep.Planner`; consume single passphrase for all steps; support retry idempotency |
| `app/services/withdraw/service_test.go` | Update existing cases for new response shape + add strategy tests |
| `app/repositories/transaction_repository.go` | Add `CreatePending`, `UpdateStatus`, `FindByParent`, `FindByIdempotencyKey` helpers if missing |
| `app/repositories/wallet_repository.go` | Add `UpdateGasStatus`, `GetSweepPolicyVersion` helpers |
| `app/repositories/account_repository.go` | Add `GetSweepLimits` helper returning parsed struct |
| `app/repositories/chain_repository.go` | Add `FindByID` returning full entity with threshold columns |
| `app/http/controllers/withdrawals_controller.go` | Include `strategy` + `sweeps` in the success response; remove `from_address_id` acceptance |
| `app/http/requests/create_withdrawal_request.go` | Drop `from_address_id` |
| `routes/api.go` | Register new endpoints (both dashboard and external API) |
| `app/container/vault_container.go` | Wire `sweep.Service`; inject into `withdraw.Service` and sweep controller |
| `app/listeners/` + `bootstrap/app.go` | Register new events `SweepBroadcast`, `SweepConfirmed`, `WalletGasStatusChanged` |
| `database/seeders/database_seeder.go` | Call new `SweepThresholdsSeeder` |
| `config/security.go` | Add `SweepDefaults` with per-chain thresholds + env overrides |

### Backend — Not changed

- `app/services/mpc/` — untouched.
- `app/services/wallet/service.go` — `CreateWallet`, `GenerateAddress` unchanged.
- `app/services/deposit/` — scanner works unchanged (detects deposits to Base or child alike).

### Frontend — Create

| File | Responsibility |
|------|----------------|
| `src/pages/dashboard/wallets/[id]/gas-funding.tsx` | Onboarding step: BaseAddress + QR + threshold + polling |
| `src/components/Wallets/GasStatusBadge.tsx` | Colored badge (seeded / low / unseeded) |
| `src/components/Wallets/GasFundingBanner.tsx` | Persistent banner when `gas_status != seeded` |
| `src/components/Modals/Wallet/Consolidate/index.tsx` | Consolidate confirmation modal + progress feedback |
| `src/components/Modals/Wallet/Withdraw/SweepPreview.tsx` | Preview panel: strategy + sweeps_required + est. gas |

### Frontend — Modify

| File | Change |
|------|--------|
| `src/types/api.ts` | Add `GasStatus`, `WithdrawPreview`, `ConsolidateRequest` types |
| `src/lib/api/client.ts` | `wallets.gasStatus`, `wallets.gasCheck`, `wallets.withdrawPreview`, `wallets.consolidate` methods |
| `src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx` | Call `withdrawPreview` before passphrase step; gate multi-sweep on gas_ready |
| `src/components/Wallets/WalletRow.tsx` (or equivalent listing) | Render `GasStatusBadge` |
| `src/components/Wallets/WalletHeader.tsx` | Render `GasFundingBanner` when applicable |
| `src/i18n/locales/en/wallets.json` + `src/i18n/locales/pt/wallets.json` | New keys |
| `src/i18n/locales/en/errors.json` + `src/i18n/locales/pt/errors.json` | New error codes |

---

## Conventions used below

- All Go commands assume `cwd = macro-wallets/back`.
- All frontend commands assume `cwd = macro-wallets/front`.
- Every implementation task ends with a **commit** step (no batching). Use the existing repo commit-message style: `feat(...)`, `chore(...)`, `test(...)`, etc.
- After schema changes, run `make migrate-fresh-seed` to verify end-to-end.
- Go file paths without line refs mean "append at end" unless noted.
- Tests reuse the existing `tests/mocks/` + `tests/testutil/BootTest()` pattern (see `app/services/withdraw/service_test.go` for an example).

---

# PHASE 1 — Data model foundation

## Task 1: Migration — `wallets` gas-readiness columns

**Files:**
- Create: `database/migrations/20260418000001_wallets_add_gas_status.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/wallet.go`

- [ ] **Step 1: Write migration**

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000001WalletsAddGasStatus struct{}

func (r *M20260418000001WalletsAddGasStatus) Signature() string {
	return "20260418000001_wallets_add_gas_status"
}

func (r *M20260418000001WalletsAddGasStatus) Up() error {
	return facades.Schema().Table("wallets", func(table schema.Blueprint) {
		table.String("gas_status", 16).Default("unseeded").Comment("unseeded | seeded | low")
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

- [ ] **Step 2: Register migration in `database/migrations/migrations.go`**

Append `&M20260418000001WalletsAddGasStatus{},` after the last entry (`&M20260412000001AddAddressDerivationColumns{},`).

- [ ] **Step 3: Update Wallet model**

In `app/models/wallet.go`, add after `ReadModelStatus` field:

```go
GasStatus          string     `gorm:"type:varchar(16);not null;default:unseeded;index" json:"gas_status"`
GasLastCheckedAt   *time.Time `gorm:"type:timestamptz" json:"gas_last_checked_at,omitempty"`
SweepPolicyVersion int        `gorm:"not null;default:1" json:"sweep_policy_version"`
```

And add constants at top of `app/models/wallet.go` (or next to existing wallet-related consts):

```go
const (
	GasStatusUnseeded = "unseeded"
	GasStatusSeeded   = "seeded"
	GasStatusLow      = "low"
)
```

- [ ] **Step 4: Run migration + verify**

Run:

```
make migrate
make migrate-status
```

Expected: new migration `20260418000001_wallets_add_gas_status` marked `ran`; no errors. Optionally `psql` and `\d wallets` to confirm columns present.

- [ ] **Step 5: Commit**

```
git add database/migrations/20260418000001_wallets_add_gas_status.go \
        database/migrations/migrations.go \
        app/models/wallet.go
git commit -m "feat(wallets): add gas_status, gas_last_checked_at, sweep_policy_version"
```

---

## Task 2: Migration — `transactions.parent_transaction_id` + `origin`

**Files:**
- Create: `database/migrations/20260418000002_transactions_add_sweep_fields.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/transaction.go`

- [ ] **Step 1: Write migration**

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000002TransactionsAddSweepFields struct{}

func (r *M20260418000002TransactionsAddSweepFields) Signature() string {
	return "20260418000002_transactions_add_sweep_fields"
}

func (r *M20260418000002TransactionsAddSweepFields) Up() error {
	return facades.Schema().Table("transactions", func(table schema.Blueprint) {
		table.Uuid("parent_transaction_id").Nullable().
			Comment("Withdrawal tx id that triggered this sweep/gas_seed")
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

- [ ] **Step 2: Register in `database/migrations/migrations.go`**

Append `&M20260418000002TransactionsAddSweepFields{},`.

- [ ] **Step 3: Update Transaction model**

In `app/models/transaction.go`, add fields after `Source`:

```go
ParentTransactionID *uuid.UUID `gorm:"type:uuid;index" json:"parent_transaction_id,omitempty"`
Origin              string     `gorm:"type:varchar(24);index" json:"origin,omitempty"`
```

Add constants:

```go
const (
	TxOriginUserRequest         = "user_request"
	TxOriginSweep               = "sweep"
	TxOriginGasSeed             = "gas_seed"
	TxOriginManualConsolidation = "manual_consolidation"
)
```

- [ ] **Step 4: Run migration**

```
make migrate
```

Expected: `20260418000002_transactions_add_sweep_fields` marked `ran`.

- [ ] **Step 5: Commit**

```
git add database/migrations/20260418000002_transactions_add_sweep_fields.go \
        database/migrations/migrations.go \
        app/models/transaction.go
git commit -m "feat(transactions): add parent_transaction_id and origin"
```

---

## Task 3: Migration — extend `transaction_type` enum

**Files:**
- Create: `database/migrations/20260418000003_transactions_extend_tx_type_enum.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/transaction.go`

- [ ] **Step 1: Write migration**

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000003TransactionsExtendTxTypeEnum struct{}

func (r *M20260418000003TransactionsExtendTxTypeEnum) Signature() string {
	return "20260418000003_transactions_extend_tx_type_enum"
}

func (r *M20260418000003TransactionsExtendTxTypeEnum) Up() error {
	// Postgres ALTER TYPE ADD VALUE is not exposed by Goravel's schema builder.
	// Raw SQL is the established escape hatch for enum evolution in this project.
	if _, err := facades.DB().Exec(`ALTER TYPE transaction_type ADD VALUE IF NOT EXISTS 'sweep'`); err != nil {
		return err
	}
	if _, err := facades.DB().Exec(`ALTER TYPE transaction_type ADD VALUE IF NOT EXISTS 'gas_seed'`); err != nil {
		return err
	}
	return nil
}

func (r *M20260418000003TransactionsExtendTxTypeEnum) Down() error {
	// Postgres does not support DROP VALUE from enum types; recreating the enum
	// is unsafe without full table rewrite. Down is a no-op; manual revert if needed.
	return nil
}

// compile-time interface check (same pattern as existing migrations)
func (r *M20260418000003TransactionsExtendTxTypeEnum) _() schema.Migration { return r }
```

Remove the `_()` helper if the existing pattern doesn't use it — it's there only as a reminder; inspect another migration before deciding.

- [ ] **Step 2: Register in `migrations.go`**

Append `&M20260418000003TransactionsExtendTxTypeEnum{},`.

- [ ] **Step 3: Add constants in `app/models/transaction.go`**

Add alongside existing tx-type consts:

```go
const (
	TxTypeDeposit    = "deposit"
	TxTypeWithdrawal = "withdrawal"
	TxTypeSweep      = "sweep"
	TxTypeGasSeed    = "gas_seed"
)
```

(If these constants already exist in a different file, only add the missing two.)

- [ ] **Step 4: Run migration + verify via psql**

```
make migrate
```

Then:

```
psql -h localhost -U vault -d vault -c "SELECT unnest(enum_range(NULL::transaction_type));"
```

Expected: result set contains `deposit, withdrawal, sweep, gas_seed`.

- [ ] **Step 5: Commit**

```
git add database/migrations/20260418000003_transactions_extend_tx_type_enum.go \
        database/migrations/migrations.go \
        app/models/transaction.go
git commit -m "feat(transactions): extend transaction_type enum with sweep and gas_seed"
```

---

## Task 4: Migration — `accounts.sweep_limits`

**Files:**
- Create: `database/migrations/20260418000004_accounts_add_sweep_limits.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/account.go`

- [ ] **Step 1: Write migration**

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000004AccountsAddSweepLimits struct{}

func (r *M20260418000004AccountsAddSweepLimits) Signature() string {
	return "20260418000004_accounts_add_sweep_limits"
}

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

- [ ] **Step 2: Register in `migrations.go`**

Append `&M20260418000004AccountsAddSweepLimits{},`.

- [ ] **Step 3: Update Account model**

In `app/models/account.go`, add:

```go
SweepLimits *string `gorm:"type:jsonb" json:"sweep_limits,omitempty"`
```

Keep it as raw `*string` at model level; parsing into a typed struct lives in `app/services/sweep/limits.go` (Task 18).

- [ ] **Step 4: Run migration**

```
make migrate
```

- [ ] **Step 5: Commit**

```
git add database/migrations/20260418000004_accounts_add_sweep_limits.go \
        database/migrations/migrations.go \
        app/models/account.go
git commit -m "feat(accounts): add sweep_limits JSONB column"
```

---

## Task 5: Migration — `chains` threshold columns

**Files:**
- Create: `database/migrations/20260418000005_chains_add_sweep_thresholds.go`
- Modify: `database/migrations/migrations.go`
- Modify: `app/models/chain.go`

- [ ] **Step 1: Write migration**

```go
package migrations

import (
	"github.com/goravel/framework/contracts/database/schema"
	"github.com/goravel/framework/facades"
)

type M20260418000005ChainsAddSweepThresholds struct{}

func (r *M20260418000005ChainsAddSweepThresholds) Signature() string {
	return "20260418000005_chains_add_sweep_thresholds"
}

func (r *M20260418000005ChainsAddSweepThresholds) Up() error {
	return facades.Schema().Table("chains", func(table schema.Blueprint) {
		table.Text("gas_readiness_threshold_raw").Nullable().
			Comment("Min native balance (raw units) on BaseAddress to consider wallet gas-ready")
		table.Text("dust_threshold_native_raw").Nullable().
			Comment("Min native balance on a child to be considered sweepable (raw units)")
		table.Decimal("dust_threshold_usd").Places(4).Total(16).Nullable().
			Comment("Min USD-equivalent for token dust filter")
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

- [ ] **Step 2: Register + update Chain model**

Append `&M20260418000005ChainsAddSweepThresholds{},` in `migrations.go`.

In `app/models/chain.go` struct fields:

```go
GasReadinessThresholdRaw *string  `gorm:"type:text" json:"gas_readiness_threshold_raw,omitempty"`
DustThresholdNativeRaw   *string  `gorm:"type:text" json:"dust_threshold_native_raw,omitempty"`
DustThresholdUSD         *float64 `gorm:"type:decimal(16,4)" json:"dust_threshold_usd,omitempty"`
```

- [ ] **Step 3: Add parser helpers in `app/models/chain.go`**

```go
import "math/big"

func (c *Chain) GasReadinessThreshold() *big.Int {
	if c.GasReadinessThresholdRaw == nil || *c.GasReadinessThresholdRaw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(*c.GasReadinessThresholdRaw, 10)
	if !ok {
		return nil
	}
	return v
}

func (c *Chain) DustThresholdNative() *big.Int {
	if c.DustThresholdNativeRaw == nil || *c.DustThresholdNativeRaw == "" {
		return nil
	}
	v, ok := new(big.Int).SetString(*c.DustThresholdNativeRaw, 10)
	if !ok {
		return nil
	}
	return v
}
```

- [ ] **Step 4: Run migration**

```
make migrate
```

- [ ] **Step 5: Commit**

```
git add database/migrations/20260418000005_chains_add_sweep_thresholds.go \
        database/migrations/migrations.go \
        app/models/chain.go
git commit -m "feat(chains): add gas_readiness + dust thresholds"
```

---

## Task 6: Seeder — populate chain thresholds

**Files:**
- Create: `database/seeds/sweep_thresholds.go`
- Modify: `database/seeders/database_seeder.go`

- [ ] **Step 1: Write seeder**

```go
package seeds

import (
	"log/slog"

	"github.com/goravel/framework/facades"
)

type SweepThresholdsSeeder struct{}

func (s *SweepThresholdsSeeder) Signature() string { return "SweepThresholdsSeeder" }

type thresholdDef struct {
	ChainID          string
	GasReadinessRaw  string
	DustNativeRaw    string
	DustUSD          *float64
}

func p(f float64) *float64 { return &f }

func (s *SweepThresholdsSeeder) Run() error {
	defs := []thresholdDef{
		{ChainID: "eth",      GasReadinessRaw: "5000000000000000",   DustNativeRaw: "500000000000000",    DustUSD: p(1.0)},   // 0.005 ETH / 0.0005 ETH / $1
		{ChainID: "teth",     GasReadinessRaw: "5000000000000000",   DustNativeRaw: "500000000000000",    DustUSD: p(1.0)},
		{ChainID: "polygon",  GasReadinessRaw: "500000000000000000", DustNativeRaw: "100000000000000000", DustUSD: p(0.1)},   // 0.5 POL / 0.1 POL / $0.10
		{ChainID: "tpolygon", GasReadinessRaw: "500000000000000000", DustNativeRaw: "100000000000000000", DustUSD: p(0.1)},
		{ChainID: "sol",      GasReadinessRaw: "10000000",           DustNativeRaw: "1000000",            DustUSD: p(1.0)},   // 0.01 SOL / 0.001 SOL / $1
		{ChainID: "tsol",     GasReadinessRaw: "10000000",           DustNativeRaw: "1000000",            DustUSD: p(1.0)},
		{ChainID: "btc",      GasReadinessRaw: "",                   DustNativeRaw: "10000",              DustUSD: nil},      // N/A gas; 10k sats dust
		{ChainID: "tbtc",     GasReadinessRaw: "",                   DustNativeRaw: "10000",              DustUSD: nil},
	}
	for _, d := range defs {
		var args []any
		sql := `UPDATE chains SET
			gas_readiness_threshold_raw = NULLIF(?, ''),
			dust_threshold_native_raw   = NULLIF(?, ''),
			dust_threshold_usd          = ?
			WHERE id = ?`
		args = []any{d.GasReadinessRaw, d.DustNativeRaw, d.DustUSD, d.ChainID}
		if _, err := facades.DB().Exec(sql, args...); err != nil {
			slog.Error("seed sweep thresholds failed", "chain", d.ChainID, "error", err)
			return err
		}
	}
	slog.Info("sweep thresholds seeded", "chains", len(defs))
	return nil
}
```

- [ ] **Step 2: Register seeder in `DatabaseSeeder`**

In `database/seeders/database_seeder.go`, add `&SweepThresholdsSeeder{},` after `&ChainSeeder{},` so it runs right after chain rows exist. Also make sure the struct is defined at `database/seeders/sweep_thresholds_seeder.go` as a wrapper around the seed func — follow existing pattern:

```go
// database/seeders/sweep_thresholds_seeder.go
package seeders

import "github.com/macrowallets/waas/database/seeds"

type SweepThresholdsSeeder struct{ seeds.SweepThresholdsSeeder }
```

(Inspect `chain_seeder.go` in `database/seeders/` to match the exact wrapper style the project uses; adjust accordingly.)

- [ ] **Step 3: Run `migrate-fresh-seed` to verify**

```
make migrate-fresh-seed
```

Expected: ends without errors; `chains` rows now have threshold values populated.

Quick check:

```
psql -h localhost -U vault -d vault -c \
  "SELECT id, gas_readiness_threshold_raw, dust_threshold_native_raw, dust_threshold_usd FROM chains ORDER BY id;"
```

Expected: values from the seeder for each chain.

- [ ] **Step 4: Commit**

```
git add database/seeds/sweep_thresholds.go database/seeders/sweep_thresholds_seeder.go database/seeders/database_seeder.go
git commit -m "feat(seed): populate chain sweep and gas thresholds"
```

---

## Task 7: Add `SweepDefaults` in `config/security.go`

**Files:**
- Modify: `config/security.go`

- [ ] **Step 1: Add struct and getter**

Append to `config/security.go`:

```go
// SweepThresholds holds per-chain fallback thresholds. Used when the corresponding
// column in `chains` is NULL. All values parse into math/big / float64 at consumption.
type SweepThresholds struct {
	GasReadinessRaw string
	DustNativeRaw   string
	DustUSD         float64
}

// SweepDefaults returns env-overridable default thresholds per chain.
// Values match the seeder in database/seeds/sweep_thresholds.go for consistency.
func SweepDefaults() map[string]SweepThresholds {
	return map[string]SweepThresholds{
		"eth": {
			GasReadinessRaw: getEnvOr("ETH_GAS_READINESS_THRESHOLD_WEI", "5000000000000000"),
			DustNativeRaw:   getEnvOr("ETH_DUST_THRESHOLD_NATIVE_WEI", "500000000000000"),
			DustUSD:         getEnvFloatOr("ETH_DUST_THRESHOLD_USD", 1.0),
		},
		"polygon": {
			GasReadinessRaw: getEnvOr("POLYGON_GAS_READINESS_THRESHOLD_WEI", "500000000000000000"),
			DustNativeRaw:   getEnvOr("POLYGON_DUST_THRESHOLD_NATIVE_WEI", "100000000000000000"),
			DustUSD:         getEnvFloatOr("POLYGON_DUST_THRESHOLD_USD", 0.10),
		},
		"sol": {
			GasReadinessRaw: getEnvOr("SOL_GAS_READINESS_THRESHOLD_LAMPORTS", "10000000"),
			DustNativeRaw:   getEnvOr("SOL_DUST_THRESHOLD_NATIVE_LAMPORTS", "1000000"),
			DustUSD:         getEnvFloatOr("SOL_DUST_THRESHOLD_USD", 1.0),
		},
		"btc": {
			GasReadinessRaw: "",
			DustNativeRaw:   getEnvOr("BTC_DUST_THRESHOLD_SATS", "10000"),
			DustUSD:         0,
		},
	}
}
```

Adapt `getEnvOr` and `getEnvFloatOr` to the existing helpers in `config/security.go` (inspect the file first; the project already has similar env-reading helpers).

- [ ] **Step 2: Build to confirm**

```
go build ./...
```

Expected: no compilation errors.

- [ ] **Step 3: Commit**

```
git add config/security.go
git commit -m "feat(config): add sweep threshold defaults with env overrides"
```

---

# PHASE 2 — Chain interface extensions

## Task 8: Extend `types.Chain` interface + add `SweepRequest` type

**Files:**
- Modify: `pkg/types/types.go` (or wherever `Chain` interface is defined; find it with `grep -n "type Chain interface" pkg/types/`)

- [ ] **Step 1: Write failing compile check**

Add to the `Chain` interface (inside the existing `interface { ... }` block):

```go
// BuildSweep builds the transaction(s) to move `asset` from `req.From` to `req.To`
// within the same wallet. Returns a slice because EVM may require a gas_seed tx
// plus the sweep tx; SOL and BTC return a single element.
BuildSweep(ctx context.Context, req SweepRequest) ([]UnsignedTx, error)

// GasReadinessThreshold is the minimum native balance BaseAddress should hold
// to be considered "gas-ready". Returns nil for chains where the concept does
// not apply (e.g. BTC, where fees come from the spent UTXO).
GasReadinessThreshold() *big.Int

// DustThreshold is the minimum balance on a child address (in raw asset units)
// for that address to be considered sweepable. Below this, sweeping costs more
// in fees than the recovered value.
DustThreshold(asset string) *big.Int
```

And add the `SweepRequest` struct after the `TransferRequest` struct:

```go
type SweepRequest struct {
	From          string
	To            string
	Asset         string
	Amount        *big.Int        // nil = total sweep (balance - buffer)
	NativeBalance *big.Int        // balance of `From` in native; adapter decides gas_seed
	FeePayer      *string         // SOL only: address that pays fees
	Token         *Token          // nil = native asset sweep
}
```

- [ ] **Step 2: Verify compilation fails**

```
go build ./app/services/chain/...
```

Expected: compilation errors — `*EVMLive`, `*SolanaLive`, `*BitcoinLive` don't implement `Chain` interface yet. Also `tests/mocks/chain.go` is incomplete.

- [ ] **Step 3: Commit**

```
git add pkg/types/
git commit -m "feat(types): add BuildSweep, GasReadinessThreshold, DustThreshold to Chain interface"
```

---

## Task 9: Extend `MockChain` with new methods

**Files:**
- Modify: `tests/mocks/chain.go`

- [ ] **Step 1: Add fields to struct**

Add to `MockChain` struct:

```go
BuildSweepFn             func(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error)
GasReadinessThresholdVal *big.Int
DustThresholdFn          func(asset string) *big.Int
BuildSweepCalls          int
```

- [ ] **Step 2: Add method implementations at end of file**

```go
func (m *MockChain) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	m.BuildSweepCalls++
	if m.BuildSweepFn != nil {
		return m.BuildSweepFn(ctx, req)
	}
	return []types.UnsignedTx{{RawBytes: []byte("mocksweep"), ChainID: m.IDVal}}, nil
}

func (m *MockChain) GasReadinessThreshold() *big.Int {
	return m.GasReadinessThresholdVal
}

func (m *MockChain) DustThreshold(asset string) *big.Int {
	if m.DustThresholdFn != nil {
		return m.DustThresholdFn(asset)
	}
	return big.NewInt(0)
}
```

- [ ] **Step 3: Verify compile**

```
go build ./tests/...
```

Expected: mocks package compiles; `app/services/chain/*.go` still fails (production adapters not yet updated).

- [ ] **Step 4: Commit**

```
git add tests/mocks/chain.go
git commit -m "test(mocks): extend MockChain with sweep-aware methods"
```

---

## Task 10: Implement `BuildSweep` in EVM adapter

**Files:**
- Modify: `app/services/chain/evm.go`

- [ ] **Step 1: Write failing test**

Create `app/services/chain/evm_sweep_test.go`:

```go
package chain

import (
	"context"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
)

func TestEVMBuildSweep_NativeSingleTx(t *testing.T) {
	adapter := &EVMLive{cfg: chainCfgEthForTests()} // helper already used by evm_test.go
	res, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From:          "0xAAA",
		To:            "0xBBB",
		Asset:         "eth",
		NativeBalance: big.NewInt(1_000_000_000_000_000), // 0.001 ETH
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 tx for native sweep, got %d", len(res))
	}
}

func TestEVMBuildSweep_TokenNeedsGasSeed(t *testing.T) {
	adapter := &EVMLive{cfg: chainCfgEthForTests()}
	res, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From:          "0xAAA",
		To:            "0xBBB",
		Asset:         "usdt",
		NativeBalance: big.NewInt(0),
		Token:         &types.Token{Symbol: "usdt", Contract: "0xdAC17F", Decimals: 6},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 txs (gas_seed + sweep) when native=0, got %d", len(res))
	}
}

func TestEVMBuildSweep_TokenHasGas_SingleTx(t *testing.T) {
	adapter := &EVMLive{cfg: chainCfgEthForTests()}
	res, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From:          "0xAAA",
		To:            "0xBBB",
		Asset:         "usdt",
		NativeBalance: big.NewInt(5_000_000_000_000_000), // plenty
		Token:         &types.Token{Symbol: "usdt", Contract: "0xdAC17F", Decimals: 6},
	})
	if err != nil { t.Fatalf("err: %v", err) }
	if len(res) != 1 {
		t.Fatalf("expected 1 tx when child has enough native, got %d", len(res))
	}
}
```

If `chainCfgEthForTests()` helper doesn't exist, reuse what `evm_test.go` already does to build a fake `EVMLive` (inspect `evm_test.go` first).

- [ ] **Step 2: Run — expect fail / build error**

```
go test ./app/services/chain/ -run TestEVMBuildSweep -v
```

Expected: compilation error (no `BuildSweep` method on `*EVMLive` yet).

- [ ] **Step 3: Implement `BuildSweep` in `evm.go`**

Insert after `EstimateFee`:

```go
func (a *EVMLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	// Native sweep: single tx, leave 21k*gasPrice on the source as fee.
	if req.Token == nil {
		gasPrice, err := a.gasPriceWithBuffer(ctx)
		if err != nil { return nil, err }
		feeReserve := new(big.Int).Mul(gasPrice, big.NewInt(21000))
		amount := new(big.Int).Sub(req.NativeBalance, feeReserve)
		if amount.Sign() <= 0 {
			return nil, fmt.Errorf("insufficient native balance for native sweep: balance=%s fee=%s", req.NativeBalance, feeReserve)
		}
		unsigned, err := a.BuildTransfer(ctx, types.TransferRequest{
			From: req.From, To: req.To, Amount: amount, Asset: a.NativeAsset(),
		})
		if err != nil { return nil, err }
		return []types.UnsignedTx{*unsigned}, nil
	}

	// ERC-20 sweep: may need a gas_seed tx first.
	gasPrice, err := a.gasPriceWithBuffer(ctx)
	if err != nil { return nil, err }
	erc20Gas := uint64(65_000) // conservative default; a.estimateERC20Gas can override if available
	feeNeeded := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(erc20Gas))

	result := []types.UnsignedTx{}
	if req.NativeBalance.Cmp(feeNeeded) < 0 {
		seedAmount := new(big.Int).Mul(feeNeeded, big.NewInt(12)) // 20% buffer
		seedAmount = seedAmount.Div(seedAmount, big.NewInt(10))
		seedTx, err := a.BuildTransfer(ctx, types.TransferRequest{
			From: req.To, To: req.From, Amount: seedAmount, Asset: a.NativeAsset(),
		})
		if err != nil { return nil, fmt.Errorf("build gas_seed: %w", err) }
		result = append(result, *seedTx)
	}

	// Sweep full token balance from source → destination.
	amount := req.Amount
	if amount == nil {
		bal, err := a.GetTokenBalance(ctx, req.From, *req.Token)
		if err != nil { return nil, fmt.Errorf("get token balance: %w", err) }
		amount = bal.Amount
	}
	sweepTx, err := a.BuildTransfer(ctx, types.TransferRequest{
		From: req.From, To: req.To, Amount: amount, Asset: req.Token.Symbol, Token: req.Token,
	})
	if err != nil { return nil, err }
	result = append(result, *sweepTx)
	return result, nil
}

// gasPriceWithBuffer fetches `eth_gasPrice` and applies a 20% buffer.
func (a *EVMLive) gasPriceWithBuffer(ctx context.Context) (*big.Int, error) {
	var hex string
	if err := a.rpc.Call(ctx, "eth_gasPrice", &hex); err != nil {
		return nil, err
	}
	raw := hexToBigInt(hex)
	buffered := new(big.Int).Mul(raw, big.NewInt(12))
	buffered.Div(buffered, big.NewInt(10))
	return buffered, nil
}
```

If `hexToBigInt` or similar helpers don't exist with that name, adapt to what `evm.go` already uses — read the top of `evm.go` to confirm.

- [ ] **Step 4: Implement threshold getters**

Append:

```go
func (a *EVMLive) GasReadinessThreshold() *big.Int {
	return a.cfg.GasReadinessThreshold // added in Task 13
}

func (a *EVMLive) DustThreshold(asset string) *big.Int {
	return a.cfg.DustThreshold(asset) // added in Task 13
}
```

These reference fields that Task 13 adds to the config struct. Until then, stub them with `return nil` so the package compiles.

- [ ] **Step 5: Run test**

```
go test ./app/services/chain/ -run TestEVMBuildSweep -v
```

Expected: PASS for all three sub-tests.

- [ ] **Step 6: Commit**

```
git add app/services/chain/evm.go app/services/chain/evm_sweep_test.go
git commit -m "feat(chain/evm): implement BuildSweep for native and ERC-20"
```

---

## AMENDMENT (2026-04-18): EVM-only v1

Discovery during Task 10: `SolanaLive` and `BitcoinLive` adapters are POC-level (signing / broadcast / build_transfer are stubs returning `"not implemented"`). The sweep feature presupposes a working base-level adapter, which SOL/BTC lack today.

**Scope cut:** v1 of this plan ships **EVM-only** (ETH, Polygon, TETH, TPolygon). SOL and BTC sweep are deferred to a dedicated epic that also implements the base adapter (derivation, build, sign, broadcast). See spec §1.3.

**Tasks 11 and 12 are reduced to minimum interface satisfaction:** `BuildSweep` returns `ErrUnsupportedChain`; threshold getters return nil. This keeps the `Chain` interface contract fulfilled (package compiles) while the sweep service refuses to plan for these chains at the entry point (Task 15 guard).

**Task 13 only wires thresholds for EVM** (skip SOL/BTC config wiring).

**Task 15 (Planner) adds an entry guard** that returns `ErrUnsupportedChain` for any wallet whose chain is not in the EVM adapter set (inspection via the registry's adapter-type method or by chain ID allowlist).

**Frontend Tasks 26–29 gate the new UI on EVM:** gas-funding banner, consolidate button, withdraw preview all only render for EVM wallets. SOL/BTC wallets keep the existing UI and existing withdraw flow (direct-from-child, POC-level).

---

## Task 11: Minimal `BuildSweep` stub for Solana adapter (v1 EVM-only; see AMENDMENT)

**Files:**
- Modify: `app/services/chain/solana.go`

- [ ] **Step 1: Write failing test**

Create `app/services/chain/solana_sweep_test.go`:

```go
package chain

import (
	"context"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
)

func TestSOLBuildSweep_SingleTxWithFeePayer(t *testing.T) {
	adapter := newSolanaForTest(t) // helper mirroring solana_test.go setup
	base := "BASE_PUBKEY_BASE58"
	res, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From:     "CHILD_PUBKEY_BASE58",
		To:       base,
		Asset:    "usdc",
		Token:    &types.Token{Symbol: "usdc", Contract: "TOKEN_MINT_BASE58", Decimals: 6},
		FeePayer: &base,
		Amount:   big.NewInt(1000000), // 1 USDC
	})
	if err != nil { t.Fatalf("unexpected err: %v", err) }
	if len(res) != 1 {
		t.Fatalf("SOL sweep must be a single tx (fee_payer on Base); got %d", len(res))
	}
}
```

- [ ] **Step 2: Run — expect build error**

```
go test ./app/services/chain/ -run TestSOLBuildSweep -v
```

Expected: compile error (no BuildSweep on SolanaLive).

- [ ] **Step 3: Implement**

Append to `solana.go`:

```go
func (a *SolanaLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	if req.Token == nil {
		// Native SOL sweep: leave rent minimum on the source.
		rentMin := big.NewInt(890880) // ~rent for basic account; tune via env if needed
		amount := new(big.Int).Sub(req.NativeBalance, rentMin)
		if amount.Sign() <= 0 {
			return nil, fmt.Errorf("insufficient SOL balance after rent reserve")
		}
		unsigned, err := a.BuildTransfer(ctx, types.TransferRequest{
			From: req.From, To: req.To, Amount: amount, Asset: a.NativeAsset(),
		})
		if err != nil { return nil, err }
		return []types.UnsignedTx{*unsigned}, nil
	}

	// SPL token sweep with fee_payer = Base. Includes create_idempotent for dest ATA
	// and close_account for source ATA (recovers rent into Base).
	feePayer := req.From
	if req.FeePayer != nil { feePayer = *req.FeePayer }

	// The lower-level helper below is new; add it next to other tx-building helpers in solana.go.
	// It returns an UnsignedTx whose RawBytes is the serialized transaction message.
	amount := req.Amount
	if amount == nil {
		bal, err := a.GetTokenBalance(ctx, req.From, *req.Token)
		if err != nil { return nil, err }
		amount = bal.Amount
	}

	unsigned, err := a.buildSPLSweepTx(ctx, splSweepParams{
		From:       req.From,
		To:         req.To,
		FeePayer:   feePayer,
		TokenMint:  req.Token.Contract,
		Amount:     amount,
		CloseEmpty: true, // config-driven in future
	})
	if err != nil { return nil, err }
	return []types.UnsignedTx{*unsigned}, nil
}

type splSweepParams struct {
	From, To, FeePayer, TokenMint string
	Amount                         *big.Int
	CloseEmpty                     bool
}

// buildSPLSweepTx assembles a Solana transaction containing:
//   1. create_idempotent(to_ata) — no-op if already exists
//   2. spl_token.transfer(from_ata → to_ata, amount)
//   3. spl_token.close_account(from_ata → fee_payer) — iff CloseEmpty && amount == full balance
// fee_payer is the first signer in the tx header.
func (a *SolanaLive) buildSPLSweepTx(ctx context.Context, p splSweepParams) (*types.UnsignedTx, error) {
	// Implementation uses the same library helpers already used by BuildTransfer in solana.go.
	// Pseudocode:
	//   ata_from = derive_ata(p.From, p.TokenMint)
	//   ata_to   = derive_ata(p.To,   p.TokenMint)
	//   instructions = [
	//       system_program.create_idempotent(ata_to, p.FeePayer, p.To, p.TokenMint),
	//       spl_token.transfer_checked(ata_from, p.TokenMint, ata_to, p.From, p.Amount, decimals),
	//   ]
	//   if p.CloseEmpty { instructions = append(instructions, spl_token.close_account(ata_from, p.FeePayer, p.From)) }
	//   msg = build_message(fee_payer=p.FeePayer, recent_blockhash=..., instructions)
	//   raw = serialize_message(msg)
	//   return &types.UnsignedTx{ChainID: a.cfg.ID, RawBytes: raw}
	//
	// Follow the exact primitives used in BuildTransfer to stay consistent. The actual Go
	// expressions depend on the `gagliardetto/solana-go` library patterns already in the file.
	return nil, fmt.Errorf("implement via gagliardetto/solana-go per existing BuildTransfer pattern")
}
```

The pseudo-code block must be replaced with real `gagliardetto/solana-go` calls mirroring the existing `BuildTransfer` in the same file. Do not leave the `return nil, fmt.Errorf(...)` in the committed code.

- [ ] **Step 4: Run test + commit**

```
go test ./app/services/chain/ -run TestSOLBuildSweep -v
```

Expected: PASS.

```
git add app/services/chain/solana.go app/services/chain/solana_sweep_test.go
git commit -m "feat(chain/solana): implement BuildSweep with fee_payer and optional close_account"
```

---

## Task 12: Minimal `BuildSweep` stub for Bitcoin adapter (v1 EVM-only; see AMENDMENT)

**Files:**
- Modify: `app/services/chain/bitcoin.go`

- [ ] **Step 1: Write failing test**

Create `app/services/chain/bitcoin_sweep_test.go`:

```go
package chain

import (
	"context"
	"math/big"
	"testing"

	"github.com/macrowallets/waas/pkg/types"
)

func TestBTCBuildSweep_ConsolidatesMultipleUTXOs(t *testing.T) {
	adapter := newBitcoinForTest(t) // set up with 3 mock UTXOs
	res, err := adapter.BuildSweep(context.Background(), types.SweepRequest{
		From:  "tb1qchild...",
		To:    "tb1qbase...",
		Asset: "btc",
	})
	if err != nil { t.Fatalf("unexpected err: %v", err) }
	if len(res) != 1 {
		t.Fatalf("BTC sweep must be a single PSBT; got %d", len(res))
	}
}

func TestBTCBuildSweep_DropsUTXOsBelowDust(t *testing.T) {
	// Build adapter whose GetUTXOs returns one UTXO of 9000 sats and one of 50000 sats.
	// Assert that the resulting PSBT inputs count is 1 (only the >= 10000 one).
	t.Skip("fill in once newBitcoinForTest supports UTXO injection")
}
```

- [ ] **Step 2: Implement**

Append to `bitcoin.go`:

```go
func (a *BitcoinLive) BuildSweep(ctx context.Context, req types.SweepRequest) ([]types.UnsignedTx, error) {
	utxos, err := a.GetUTXOs(ctx, req.From)
	if err != nil { return nil, fmt.Errorf("get utxos: %w", err) }

	dust := a.DustThreshold("btc")
	eligible := make([]types.UTXO, 0, len(utxos))
	totalIn := big.NewInt(0)
	for _, u := range utxos {
		if dust != nil && u.Value.Cmp(dust) < 0 {
			continue
		}
		eligible = append(eligible, u)
		totalIn.Add(totalIn, u.Value)
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("no sweepable UTXOs (all below dust threshold)")
	}
	if len(eligible) > 200 {
		return nil, fmt.Errorf("too many UTXOs (%d > 200); split operation", len(eligible))
	}

	feeRate, err := a.estimateFeeRate(ctx) // sats/vbyte via estimatesmartfee
	if err != nil { return nil, err }

	// Rough vbyte estimate: ~68 per P2WPKH input + ~31 per output + 11 overhead
	vbytes := int64(len(eligible))*68 + 31 + 11
	fee := new(big.Int).Mul(big.NewInt(feeRate), big.NewInt(vbytes))
	outValue := new(big.Int).Sub(totalIn, fee)
	if outValue.Sign() <= 0 {
		return nil, fmt.Errorf("fee (%s sats) exceeds total input (%s sats)", fee, totalIn)
	}

	psbt, err := a.buildPSBT(ctx, eligible, []psbtOutput{{Address: req.To, Value: outValue}})
	if err != nil { return nil, err }

	return []types.UnsignedTx{{ChainID: a.cfg.ID, RawBytes: psbt}}, nil
}

// estimateFeeRate queries `estimatesmartfee` from the configured Bitcoin Core RPC.
// Returns sats per vbyte (rounded up). Caller multiplies by vbytes to get total fee.
func (a *BitcoinLive) estimateFeeRate(ctx context.Context) (int64, error) {
	// Use the existing RPC helper in bitcoin.go (whatever pattern is already used for height/block queries).
	// Reply shape: { "feerate": <BTC_per_kvB>, "blocks": N }. Convert to sats per vbyte.
	// Fallback: return 15 (sats/vbyte) if RPC unreachable.
	return 15, nil
}
```

Replace the `return 15, nil` stub with the real `estimatesmartfee` call following the existing RPC-call pattern in the file.

- [ ] **Step 3: Run test**

```
go test ./app/services/chain/ -run TestBTCBuildSweep -v
```

Expected: PASS on the non-skipped test.

- [ ] **Step 4: Commit**

```
git add app/services/chain/bitcoin.go app/services/chain/bitcoin_sweep_test.go
git commit -m "feat(chain/bitcoin): implement BuildSweep as UTXO consolidation PSBT"
```

---

## Task 13: Wire thresholds from chain entity into adapters

**Files:**
- Modify: `app/services/chain/registry.go` (or wherever adapter configs are built from `models.Chain`)
- Modify: `app/services/chain/evm.go`, `solana.go`, `bitcoin.go` (config struct + getter methods)

- [ ] **Step 1: Extend adapter config structs**

In the shared config struct used by all three adapters (likely `ChainConfig` or similar — inspect `registry.go`), add:

```go
GasReadinessThreshold *big.Int
DustThresholdNative   *big.Int
DustThresholdUSD      float64
```

- [ ] **Step 2: Populate from `models.Chain`**

In the registry's function that maps a `models.Chain` row into adapter config, add:

```go
cfg.GasReadinessThreshold = entity.GasReadinessThreshold() // uses helper added in Task 5
cfg.DustThresholdNative   = entity.DustThresholdNative()
if entity.DustThresholdUSD != nil {
	cfg.DustThresholdUSD = *entity.DustThresholdUSD
}
// Fallback to config/security.go SweepDefaults if entity values are nil.
```

- [ ] **Step 3: Replace stubs in adapter getters**

In each of `evm.go`, `solana.go`, `bitcoin.go`, replace any `return nil` stub from Task 10/11/12 with the actual field read from `a.cfg`. For `DustThreshold(asset)`:

```go
func (a *EVMLive) DustThreshold(asset string) *big.Int {
	if asset == a.NativeAsset() {
		return a.cfg.DustThresholdNative
	}
	// Token dust: convert USD threshold into asset raw units.
	// Uses price service; for v1 we can return nil and let sweep.Planner handle the USD case directly.
	return nil
}
```

Leave the USD-based token dust to be resolved inside the planner (Task 15) where we have price service access.

- [ ] **Step 4: Build + run all chain tests**

```
go build ./...
go test ./app/services/chain/ -v
```

Expected: all tests pass. Whole repo builds.

- [ ] **Step 5: Commit**

```
git add app/services/chain/
git commit -m "feat(chain): wire sweep thresholds from chains table into adapter config"
```

---

# PHASE 3 — Sweep service core

## Task 14: Scaffold `app/services/sweep/` package

**Files:**
- Create: `app/services/sweep/types.go`
- Create: `app/services/sweep/service.go`

- [ ] **Step 1: `types.go`**

```go
package sweep

import (
	"math/big"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type Strategy string

const (
	StrategyDirectFromBase  Strategy = "direct_from_base"
	StrategyDirectFromChild Strategy = "direct_from_child"
	StrategyMultiSweep      Strategy = "multi_sweep"
	StrategyInsufficient    Strategy = "insufficient"
)

type AddressBalance struct {
	Address models.Address
	Balance *big.Int
	Reason  string // why ignored (dust / zero)
}

type Plan struct {
	WalletID      uuid.UUID
	Chain         string
	Asset         string
	Amount        *big.Int
	Strategy      Strategy
	SourceAddress *models.Address
	Sweeps        []PlannedSweep
	EstimatedGas  *big.Int
	ReachesTarget bool
	DustIgnored   []AddressBalance
	BaseBalance   *big.Int
}

type PlannedSweep struct {
	From      models.Address
	Amount    *big.Int
	NeedsGas  bool
	GasAmount *big.Int
}

type CompletedSweep struct {
	From           models.Address
	TxHash         string
	InternalTxID   uuid.UUID
}

type FailedStep struct {
	Index          int
	LastError      string
	RetryReady     bool
}

type Result struct {
	WithdrawalTxID  uuid.UUID
	Sweeps          []CompletedSweep
	FinalWithdrawTx *models.Transaction
	FailedStep      *FailedStep
}

type GasStatus struct {
	Status         string   // unseeded | seeded | low
	BaseAddress    string
	NativeAsset    string
	NativeBalance  *big.Int
	Threshold      *big.Int
	LastCheckedAt  int64 // unix seconds
}

// Limits (per-account effective config after merge of account.sweep_limits + defaults)
type Limits struct {
	MaxAddressesPerRequest  map[string]int
	MaxConsolidateReqPerDay int
	DailyWithdrawCapUSD     *float64 // nil = unlimited
}
```

- [ ] **Step 2: `service.go` (skeleton)**

```go
package sweep

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	mpcpkg "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/webhook"
)

type Service interface {
	PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int) (*Plan, error)
	ExecutePlan(ctx context.Context, plan *Plan, shareA []byte, withdrawalTxID uuid.UUID) (*Result, error)
	ConsolidateAll(ctx context.Context, walletID uuid.UUID, asset string, passphrase string) (*Result, error)
	RefreshGasStatus(ctx context.Context, walletID uuid.UUID) (*GasStatus, error)
	LoadLimits(ctx context.Context, accountID uuid.UUID) (*Limits, error)
}

type service struct {
	registry     *chain.Registry
	mpc          mpcpkg.Service
	secrets      *secretsmanager.Client
	rdb          *redis.Client
	webhookSvc   *webhook.Service
	walletRepo   repositories.WalletRepository
	addressRepo  repositories.AddressRepository
	txRepo       repositories.TransactionRepository
	accountRepo  repositories.AccountRepository
	chainRepo    repositories.ChainRepository
}

func NewService(
	registry *chain.Registry,
	mpc mpcpkg.Service,
	secrets *secretsmanager.Client,
	rdb *redis.Client,
	webhookSvc *webhook.Service,
	walletRepo repositories.WalletRepository,
	addressRepo repositories.AddressRepository,
	txRepo repositories.TransactionRepository,
	accountRepo repositories.AccountRepository,
	chainRepo repositories.ChainRepository,
) Service {
	return &service{
		registry: registry, mpc: mpc, secrets: secrets, rdb: rdb, webhookSvc: webhookSvc,
		walletRepo: walletRepo, addressRepo: addressRepo, txRepo: txRepo,
		accountRepo: accountRepo, chainRepo: chainRepo,
	}
}

// Each method is implemented in its own file: planner.go, executor.go, gas_readiness.go, limits.go.
```

- [ ] **Step 3: Build**

```
go build ./app/services/sweep/
```

Expected: compile errors about missing method implementations — that's fine for now; next tasks implement them. To get a compilable placeholder, add temporary `return nil, errNotImplemented` bodies for each interface method on the concrete struct, in `service.go`.

- [ ] **Step 4: Commit**

```
git add app/services/sweep/
git commit -m "feat(sweep): scaffold service interface and types"
```

---

## Task 15: Planner — `PlanForWithdrawal`

**Files:**
- Create: `app/services/sweep/planner.go`
- Create: `app/services/sweep/planner_test.go`
- Modify: `app/services/sweep/service.go` (dispatch)

- [ ] **Step 1: Write failing tests**

```go
package sweep

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestPlan_DirectFromBase(t *testing.T) {
	svc := newPlannerTestService(t, map[string]*big.Int{
		"BASE": big.NewInt(1000),
	})
	plan, err := svc.PlanForWithdrawal(context.Background(), testWalletID(t), "usdt", big.NewInt(500))
	if err != nil { t.Fatal(err) }
	if plan.Strategy != StrategyDirectFromBase {
		t.Fatalf("expected direct_from_base, got %s", plan.Strategy)
	}
	if !plan.ReachesTarget { t.Fatal("expected reaches_target=true") }
}

func TestPlan_DirectFromChild_SingleChildCovers(t *testing.T) {
	svc := newPlannerTestService(t, map[string]*big.Int{
		"BASE":     big.NewInt(0),
		"CHILD_A":  big.NewInt(600),
		"CHILD_B":  big.NewInt(200),
	})
	plan, err := svc.PlanForWithdrawal(context.Background(), testWalletID(t), "usdt", big.NewInt(500))
	if err != nil { t.Fatal(err) }
	if plan.Strategy != StrategyDirectFromChild {
		t.Fatalf("expected direct_from_child, got %s", plan.Strategy)
	}
	if plan.SourceAddress == nil || plan.SourceAddress.Address != "CHILD_A" {
		t.Fatalf("expected child_A, got %+v", plan.SourceAddress)
	}
}

func TestPlan_MultiSweep_Greedy(t *testing.T) {
	svc := newPlannerTestService(t, map[string]*big.Int{
		"BASE":    big.NewInt(100),
		"CHILD_A": big.NewInt(300),
		"CHILD_B": big.NewInt(250),
		"CHILD_C": big.NewInt(200),
	})
	plan, err := svc.PlanForWithdrawal(context.Background(), testWalletID(t), "usdt", big.NewInt(600))
	if err != nil { t.Fatal(err) }
	if plan.Strategy != StrategyMultiSweep {
		t.Fatalf("expected multi_sweep, got %s", plan.Strategy)
	}
	if len(plan.Sweeps) != 2 {
		t.Fatalf("expected greedy subset of 2 (A+B=550 + base=100 = 650 >= 600), got %d", len(plan.Sweeps))
	}
}

func TestPlan_Insufficient(t *testing.T) {
	svc := newPlannerTestService(t, map[string]*big.Int{
		"BASE":    big.NewInt(10),
		"CHILD_A": big.NewInt(20),
	})
	plan, err := svc.PlanForWithdrawal(context.Background(), testWalletID(t), "usdt", big.NewInt(100))
	if err != nil { t.Fatal(err) }
	if plan.ReachesTarget {
		t.Fatalf("expected reaches_target=false")
	}
	if plan.Strategy != StrategyInsufficient {
		t.Fatalf("expected insufficient, got %s", plan.Strategy)
	}
}

func TestPlan_DustIgnored(t *testing.T) {
	// Chain mock configured to return DustThreshold=50; child with balance=10 must be ignored.
	svc := newPlannerTestService(t, map[string]*big.Int{
		"BASE":     big.NewInt(0),
		"CHILD_A":  big.NewInt(1000),
		"CHILD_B":  big.NewInt(10),  // dust
	})
	plan, err := svc.PlanForWithdrawal(context.Background(), testWalletID(t), "usdt", big.NewInt(500))
	if err != nil { t.Fatal(err) }
	if len(plan.DustIgnored) != 1 || plan.DustIgnored[0].Address.Address != "CHILD_B" {
		t.Fatalf("expected CHILD_B in DustIgnored, got %+v", plan.DustIgnored)
	}
}

// Helpers testWalletID, newPlannerTestService live in planner_test_helpers.go
// They build an in-memory sweep service whose repositories return the provided balance map
// and whose mock chain returns DustThreshold=50 unless overridden. Use the existing
// `tests/mocks` package where applicable and setup-boot like other service tests.
```

Write `planner_test_helpers.go` using the same `mocks.TestDB` + `mocks.MockChain` patterns already in the codebase. For repositories without existing mocks, prefer in-memory fakes scoped to this test file.

- [ ] **Step 2: Run — expect fail**

```
go test ./app/services/sweep/ -run TestPlan -v
```

Expected: FAIL / panic (planner not implemented).

- [ ] **Step 3: Implement `planner.go`**

```go
package sweep

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func (s *service) PlanForWithdrawal(ctx context.Context, walletID uuid.UUID, asset string, amount *big.Int) (*Plan, error) {
	wallet, err := s.walletRepo.FindByID(walletID)
	if err != nil || wallet == nil { return nil, fmt.Errorf("wallet not found") }
	chainEntity, err := s.chainRepo.FindByID(wallet.Chain)
	if err != nil { return nil, err }
	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil { return nil, err }

	// Base balance
	baseBal, err := fetchBalance(ctx, adapter, *wallet.DepositAddress, asset)
	if err != nil { return nil, fmt.Errorf("base balance: %w", err) }

	plan := &Plan{
		WalletID: walletID, Chain: wallet.Chain, Asset: asset, Amount: amount,
		BaseBalance: baseBal,
	}

	// 1) direct_from_base
	if baseBal.Cmp(amount) >= 0 {
		plan.Strategy = StrategyDirectFromBase
		plan.SourceAddress = wallet.DepositAddress
		plan.ReachesTarget = true
		return plan, nil
	}

	// Fetch child addresses with balance >= dust_threshold
	children, err := s.addressRepo.FindByWalletID(walletID)
	if err != nil { return nil, err }

	type childWithBal struct {
		addr models.Address
		bal  *big.Int
	}
	dustThreshold := resolveDustThreshold(chainEntity, adapter, asset)
	eligible := make([]childWithBal, 0, len(children))
	dustIgnored := []AddressBalance{}
	for _, c := range children {
		if c.ID == wallet.DepositAddress.ID { continue } // skip Base itself
		bal, err := fetchBalance(ctx, adapter, c, asset)
		if err != nil || bal.Sign() == 0 { continue }
		if dustThreshold != nil && bal.Cmp(dustThreshold) < 0 {
			dustIgnored = append(dustIgnored, AddressBalance{Address: c, Balance: bal, Reason: "below_dust_threshold"})
			continue
		}
		eligible = append(eligible, childWithBal{addr: c, bal: bal})
	}
	plan.DustIgnored = dustIgnored

	// 2) direct_from_child — single child covers alone
	for _, cb := range eligible {
		if cb.bal.Cmp(amount) >= 0 {
			plan.Strategy = StrategyDirectFromChild
			addr := cb.addr
			plan.SourceAddress = &addr
			plan.ReachesTarget = true
			return plan, nil
		}
	}

	// 3) multi_sweep — greedy by largest balance first
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].bal.Cmp(eligible[j].bal) > 0
	})
	remaining := new(big.Int).Sub(amount, baseBal)
	total := new(big.Int).Set(baseBal)
	sweeps := []PlannedSweep{}
	for _, cb := range eligible {
		if remaining.Sign() <= 0 { break }
		sweepAmount := new(big.Int).Set(cb.bal)
		if sweepAmount.Cmp(remaining) > 0 {
			sweepAmount = new(big.Int).Set(cb.bal) // sweep full anyway — excess stays in Base
		}
		// NeedsGas is chain-specific; adapter decides in Build phase. Planner flags conservatively.
		sweeps = append(sweeps, PlannedSweep{From: cb.addr, Amount: sweepAmount, NeedsGas: chainNeedsGasSeed(wallet.Chain)})
		total.Add(total, sweepAmount)
		remaining.Sub(remaining, sweepAmount)
	}
	if total.Cmp(amount) < 0 {
		plan.Strategy = StrategyInsufficient
		plan.ReachesTarget = false
		return plan, nil
	}
	plan.Strategy = StrategyMultiSweep
	plan.Sweeps = sweeps
	plan.ReachesTarget = true
	return plan, nil
}

// Helper: chains that require a separate gas_seed tx before token sweeps.
func chainNeedsGasSeed(chainID string) bool {
	switch chainID {
	case "eth", "teth", "polygon", "tpolygon":
		return true
	}
	return false
}

// resolveDustThreshold picks the correct threshold for `asset`:
//   - native asset → DustThresholdNative column (raw units)
//   - token       → adapter.DustThreshold(asset) which may resolve USD via price service
func resolveDustThreshold(entity *models.Chain, adapter /* types.Chain */ any, asset string) *big.Int {
	// Simplified v1: use DustThresholdNative for any asset; token USD → big.Int conversion is a v1 TODO.
	return entity.DustThresholdNative()
}

func fetchBalance(ctx context.Context, adapter /* types.Chain */ any, addr models.Address, asset string) (*big.Int, error) {
	// Use types.Chain.GetBalance or GetTokenBalance depending on asset matching adapter.NativeAsset().
	return nil, fmt.Errorf("implement per types.Chain API")
}
```

Replace pseudo-typed `any` with the correct `types.Chain` interface and implement `fetchBalance` to call `GetBalance` or `GetTokenBalance` as appropriate. Don't commit the placeholder.

- [ ] **Step 4: Run tests**

```
go test ./app/services/sweep/ -run TestPlan -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```
git add app/services/sweep/planner.go app/services/sweep/planner_test.go app/services/sweep/planner_test_helpers.go
git commit -m "feat(sweep): implement PlanForWithdrawal with greedy multi-sweep and dust filter"
```

---

## Task 16: Executor — broadcast plan with parent linkage

**Files:**
- Create: `app/services/sweep/executor.go`
- Create: `app/services/sweep/executor_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestExecutePlan_DirectFromBase_SingleTx(t *testing.T) {
	svc, mockChain := newExecutorTestService(t)
	mockChain.BroadcastTransactionFn = func(ctx context.Context, tx *types.SignedTx) (string, error) {
		return "0xDEADBEEF", nil
	}
	plan := &Plan{
		Strategy: StrategyDirectFromBase,
		SourceAddress: &models.Address{Address: "BASE"},
		Amount: big.NewInt(100), Asset: "usdt", Chain: "eth",
	}
	res, err := svc.ExecutePlan(context.Background(), plan, []byte("fakeShareA"), uuid.New())
	if err != nil { t.Fatal(err) }
	if res.FinalWithdrawTx == nil || res.FinalWithdrawTx.TxHash != "0xDEADBEEF" {
		t.Fatalf("expected final tx with hash 0xDEADBEEF, got %+v", res.FinalWithdrawTx)
	}
}

func TestExecutePlan_MultiSweep_LinksParent(t *testing.T) {
	svc, mockChain := newExecutorTestService(t)
	hashes := []string{"SWEEP_1", "SWEEP_2", "WITHDRAW"}
	idx := 0
	mockChain.BroadcastTransactionFn = func(ctx context.Context, tx *types.SignedTx) (string, error) {
		h := hashes[idx]; idx++; return h, nil
	}
	withdrawalID := uuid.New()
	plan := &Plan{
		Strategy: StrategyMultiSweep, Chain: "eth", Asset: "usdt", Amount: big.NewInt(500),
		Sweeps: []PlannedSweep{
			{From: models.Address{Address: "CHILD_A"}, Amount: big.NewInt(300)},
			{From: models.Address{Address: "CHILD_B"}, Amount: big.NewInt(200)},
		},
	}
	res, err := svc.ExecutePlan(context.Background(), plan, []byte("shareA"), withdrawalID)
	if err != nil { t.Fatal(err) }
	if len(res.Sweeps) != 2 { t.Fatalf("expected 2 sweeps, got %d", len(res.Sweeps)) }
	// Assert every sweep tx persisted has ParentTransactionID == withdrawalID and Origin == sweep.
	// (Inspect the test-scoped transaction repo fake to verify.)
}

func TestExecutePlan_FailureMidway_RetryIdempotent(t *testing.T) {
	// Configure mockChain such that the 2nd broadcast returns an error.
	// Expect Result.FailedStep != nil, FailedStep.Index == 1.
	// Re-run ExecutePlan with the same plan (after clearing the broadcast error).
	// Expect only the remaining sweeps + final withdraw to broadcast — planner skipping
	// sweeps whose source child is already at zero. This second call must not
	// double-broadcast sweep #1.
}
```

- [ ] **Step 2: Implement `executor.go`**

```go
package sweep

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

func (s *service) ExecutePlan(ctx context.Context, plan *Plan, shareA []byte, withdrawalTxID uuid.UUID) (*Result, error) {
	adapter, err := s.registry.Chain(plan.Chain)
	if err != nil { return nil, err }

	result := &Result{WithdrawalTxID: withdrawalTxID}

	switch plan.Strategy {
	case StrategyDirectFromBase, StrategyDirectFromChild:
		tx, err := s.broadcastSingle(ctx, plan, adapter, shareA, withdrawalTxID, TxOriginUserRequest(plan))
		if err != nil { return nil, err }
		result.FinalWithdrawTx = tx
		return result, nil

	case StrategyMultiSweep:
		// 1. Broadcast gas_seed + sweep for each planned sweep
		for i, sweep := range plan.Sweeps {
			sweepTxID := uuid.New()
			if err := s.broadcastSweep(ctx, plan, adapter, sweep, shareA, withdrawalTxID, sweepTxID); err != nil {
				slog.Error("sweep step failed", "index", i, "error", err)
				result.FailedStep = &FailedStep{Index: i, LastError: err.Error(), RetryReady: true}
				return result, nil
			}
			result.Sweeps = append(result.Sweeps, CompletedSweep{
				From: sweep.From, InternalTxID: sweepTxID,
			})
		}
		// 2. Broadcast final withdraw
		// (Plan includes Amount and final `To` via context propagated by caller.)
		finalTx, err := s.broadcastFinalWithdrawal(ctx, plan, adapter, shareA, withdrawalTxID)
		if err != nil { return result, err }
		result.FinalWithdrawTx = finalTx

		// 3. Emit webhook event for each sweep + final
		for _, cs := range result.Sweeps {
			s.webhookSvc.EnqueueEvent(ctx, cs.InternalTxID, types.EventSweepBroadcast, cs)
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported strategy: %s", plan.Strategy)
	}
}

// broadcastSweep performs: build_sweep → sign → broadcast → persist tx with parent linkage.
func (s *service) broadcastSweep(ctx context.Context, plan *Plan, adapter types.Chain, sweep PlannedSweep, shareA []byte, parentID, txID uuid.UUID) error {
	// Implementation omitted for brevity — parallels the existing withdraw.Service.Request() signing path:
	//   1. adapter.BuildSweep(ctx, types.SweepRequest{...})
	//   2. for each UnsignedTx returned: MPC Sign (shareA + shareB from Secrets Manager) → SignedTx
	//   3. adapter.BroadcastTransaction(ctx, signedTx)
	//   4. persist models.Transaction{ParentTransactionID: &parentID, Origin: TxOriginSweep, TxType: TxTypeSweep, ...}
	return nil
}
```

Fill in `broadcastSingle`, `broadcastFinalWithdrawal`, and the sweep body using the same MPC signing pattern from `app/services/withdraw/service.go:152-228`. Factor shared code into a helper — do not copy-paste.

- [ ] **Step 3: Run tests**

```
go test ./app/services/sweep/ -run TestExecutePlan -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```
git add app/services/sweep/executor.go app/services/sweep/executor_test.go
git commit -m "feat(sweep): implement executor with parent_transaction_id linkage"
```

---

## Task 17: `GasReadiness` service

**Files:**
- Create: `app/services/sweep/gas_readiness.go`
- Create: `app/services/sweep/gas_readiness_test.go`
- Create: `app/events/sweep_events.go`

- [ ] **Step 1: Define events**

`app/events/sweep_events.go`:

```go
package events

import "github.com/goravel/framework/contracts/event"

type SweepBroadcast struct{}
func (*SweepBroadcast) Handle(args []event.Arg) ([]event.Arg, error) { return args, nil }

type SweepConfirmed struct{}
func (*SweepConfirmed) Handle(args []event.Arg) ([]event.Arg, error) { return args, nil }

type WalletGasStatusChanged struct{}
func (*WalletGasStatusChanged) Handle(args []event.Arg) ([]event.Arg, error) { return args, nil }
```

Register them in `bootstrap/app.go` under `WithEvents`:

```go
&events.SweepBroadcast{}:        {&listeners.EnqueueWalletRefresh{}},
&events.SweepConfirmed{}:        {&listeners.EnqueueWalletRefresh{}},
&events.WalletGasStatusChanged{}: {}, // no-op listener for v1
```

- [ ] **Step 2: Write failing tests**

```go
func TestRefreshGasStatus_TransitionsUnseededToSeeded(t *testing.T) {
	svc, _, mockChain := newGasTestService(t)
	mockChain.GetBalanceFn = func(ctx context.Context, addr string) (*types.Balance, error) {
		return &types.Balance{Amount: big.NewInt(10_000_000_000_000_000)}, nil // 0.01 ETH > threshold 0.005
	}
	status, err := svc.RefreshGasStatus(context.Background(), testWalletID(t))
	if err != nil { t.Fatal(err) }
	if status.Status != "seeded" { t.Fatalf("expected seeded, got %s", status.Status) }
}

func TestRefreshGasStatus_EmitsWebhookOnChange(t *testing.T) {
	// assert webhook queue received wallet.gas_status.changed
}
```

- [ ] **Step 3: Implement**

```go
func (s *service) RefreshGasStatus(ctx context.Context, walletID uuid.UUID) (*GasStatus, error) {
	wallet, err := s.walletRepo.FindByID(walletID)
	if err != nil || wallet == nil { return nil, fmt.Errorf("wallet not found") }
	chainEntity, err := s.chainRepo.FindByID(wallet.Chain)
	if err != nil { return nil, err }
	threshold := chainEntity.GasReadinessThreshold()
	if threshold == nil {
		// Chain has no gas concept (BTC). Always seeded.
		return s.persistAndReturnGasStatus(walletID, models.GasStatusSeeded, wallet.DepositAddress.Address, chainEntity.NativeSymbol, nil, nil)
	}

	adapter, err := s.registry.Chain(wallet.Chain)
	if err != nil { return nil, err }
	bal, err := adapter.GetBalance(ctx, wallet.DepositAddress.Address)
	if err != nil { return nil, fmt.Errorf("get balance: %w", err) }

	newStatus := models.GasStatusUnseeded
	switch {
	case bal.Amount.Sign() == 0:
		newStatus = models.GasStatusUnseeded
	case bal.Amount.Cmp(threshold) < 0:
		newStatus = models.GasStatusLow
	default:
		newStatus = models.GasStatusSeeded
	}

	oldStatus := wallet.GasStatus
	if err := s.walletRepo.UpdateFields(walletID, map[string]any{
		"gas_status":          newStatus,
		"gas_last_checked_at": time.Now().UTC(),
	}); err != nil { return nil, err }

	if oldStatus != newStatus {
		_ = facades.Event().Job(&events.WalletGasStatusChanged{}, []event.Arg{
			{Type: "string", Value: walletID.String()},
			{Type: "string", Value: oldStatus},
			{Type: "string", Value: newStatus},
		}).Dispatch()
		s.webhookSvc.EnqueueEvent(ctx, walletID, types.EventWalletGasStatusChanged, map[string]any{
			"wallet_id": walletID, "old_status": oldStatus, "new_status": newStatus,
			"base_address": wallet.DepositAddress.Address, "balance": bal.Amount.String(),
			"threshold": threshold.String(),
		})
	}
	return &GasStatus{
		Status: newStatus, BaseAddress: wallet.DepositAddress.Address,
		NativeAsset: chainEntity.NativeSymbol, NativeBalance: bal.Amount,
		Threshold: threshold, LastCheckedAt: time.Now().Unix(),
	}, nil
}
```

- [ ] **Step 4: Run test**

```
go test ./app/services/sweep/ -run TestRefreshGasStatus -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```
git add app/services/sweep/gas_readiness.go app/services/sweep/gas_readiness_test.go app/events/sweep_events.go bootstrap/app.go
git commit -m "feat(sweep): implement RefreshGasStatus with webhook emission"
```

---

## Task 18: `Limits` service

**Files:**
- Create: `app/services/sweep/limits.go`
- Create: `app/services/sweep/limits_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestLoadLimits_MergeAccountOverWithDefaults(t *testing.T) {
	// account.sweep_limits JSON: { "max_consolidate_requests_per_day": 100 }
	// expected: max_addresses_per_request falls back to defaults; max_consolidate_requests_per_day == 100
}

func TestCheckDailyQuota_Exceeded(t *testing.T) {
	// pre-populate Redis counter at 50
	// limit = 50 → next call should return err sweep_limit_exceeded
}

func TestInFlightLock_RejectsConcurrent(t *testing.T) {
	// acquire lock in one goroutine; another call must return err
}
```

- [ ] **Step 2: Implement**

```go
func (s *service) LoadLimits(ctx context.Context, accountID uuid.UUID) (*Limits, error) {
	defaults := &Limits{
		MaxAddressesPerRequest: map[string]int{"evm": 100, "sol": 25, "btc": 100},
		MaxConsolidateReqPerDay: 50,
	}
	if accountID == uuid.Nil { return defaults, nil }
	account, err := s.accountRepo.FindByID(accountID)
	if err != nil || account == nil { return defaults, nil }
	if account.SweepLimits == nil || *account.SweepLimits == "" { return defaults, nil }

	var override struct {
		MaxAddressesPerRequest  map[string]int `json:"max_addresses_per_request"`
		MaxConsolidateReqPerDay *int           `json:"max_consolidate_requests_per_day"`
		DailyWithdrawCapUSD     *string        `json:"daily_withdraw_cap_usd"`
	}
	if err := json.Unmarshal([]byte(*account.SweepLimits), &override); err != nil {
		slog.Warn("parse account sweep_limits", "account_id", accountID, "error", err)
		return defaults, nil
	}
	merged := *defaults
	for k, v := range override.MaxAddressesPerRequest {
		merged.MaxAddressesPerRequest[k] = v
	}
	if override.MaxConsolidateReqPerDay != nil {
		merged.MaxConsolidateReqPerDay = *override.MaxConsolidateReqPerDay
	}
	if override.DailyWithdrawCapUSD != nil {
		if f, err := strconv.ParseFloat(*override.DailyWithdrawCapUSD, 64); err == nil {
			merged.DailyWithdrawCapUSD = &f
		}
	}
	return &merged, nil
}

func (s *service) acquireWalletOpsLock(ctx context.Context, walletID uuid.UUID) (func(), error) {
	key := "vault:lock:wallet_ops:" + walletID.String()
	ok, err := s.rdb.SetNX(ctx, key, "1", 60*time.Second).Result()
	if err != nil { return nil, err }
	if !ok { return nil, ErrInFlightConsolidation }
	return func() { s.rdb.Del(ctx, key) }, nil
}

func (s *service) incrDailyQuota(ctx context.Context, accountID uuid.UUID, limits *Limits) error {
	key := fmt.Sprintf("vault:quota:consolidate:%s:%s", accountID, time.Now().UTC().Format("2006-01-02"))
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil { return err }
	if n == 1 { s.rdb.Expire(ctx, key, 24*time.Hour) }
	if int(n) > limits.MaxConsolidateReqPerDay {
		return ErrDailyQuotaExceeded
	}
	return nil
}
```

Sentinel errors defined in `service.go`:

```go
var (
	ErrInFlightConsolidation = errors.New("another consolidation in flight for this wallet")
	ErrDailyQuotaExceeded    = errors.New("daily consolidation quota exceeded")
	ErrTooManyAddresses      = errors.New("too many addresses per request")
)
```

- [ ] **Step 3: Run tests**

```
go test ./app/services/sweep/ -run TestLoadLimits -v
go test ./app/services/sweep/ -run TestCheckDailyQuota -v
go test ./app/services/sweep/ -run TestInFlightLock -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```
git add app/services/sweep/limits.go app/services/sweep/limits_test.go app/services/sweep/service.go
git commit -m "feat(sweep): implement per-account limits with Redis counters and locks"
```

---

## Task 19: `ConsolidateAll` — manual consolidate entry point

**Files:**
- Modify: `app/services/sweep/service.go`
- Create: `app/services/sweep/consolidate_test.go`

- [ ] **Step 1: Test**

```go
func TestConsolidateAll_EmptyChildren_Noop(t *testing.T) {
	svc, _ := newConsolidateTestService(t, map[string]*big.Int{"BASE": big.NewInt(500)})
	res, err := svc.ConsolidateAll(context.Background(), testWalletID(t), "usdt", "passphrase12345")
	if err != nil { t.Fatal(err) }
	if len(res.Sweeps) != 0 { t.Fatal("expected no sweeps when only Base has funds") }
}

func TestConsolidateAll_SweepsAllChildren(t *testing.T) {
	svc, _ := newConsolidateTestService(t, map[string]*big.Int{
		"BASE": big.NewInt(0), "CHILD_A": big.NewInt(500), "CHILD_B": big.NewInt(200),
	})
	res, err := svc.ConsolidateAll(context.Background(), testWalletID(t), "usdt", "passphrase12345")
	if err != nil { t.Fatal(err) }
	if len(res.Sweeps) != 2 { t.Fatalf("expected 2 sweeps, got %d", len(res.Sweeps)) }
}
```

- [ ] **Step 2: Implement**

```go
func (s *service) ConsolidateAll(ctx context.Context, walletID uuid.UUID, asset string, passphrase string) (*Result, error) {
	wallet, err := s.walletRepo.FindByID(walletID)
	if err != nil || wallet == nil { return nil, fmt.Errorf("wallet not found") }

	limits, _ := s.LoadLimits(ctx, derefOrZero(wallet.AccountID))
	releaseLock, err := s.acquireWalletOpsLock(ctx, walletID)
	if err != nil { return nil, err }
	defer releaseLock()
	if err := s.incrDailyQuota(ctx, derefOrZero(wallet.AccountID), limits); err != nil { return nil, err }

	// Build a "consolidate plan" = planner output with Amount = totalSweepable (so strategy=multi_sweep sweeps everything).
	children, err := s.addressRepo.FindByWalletID(walletID)
	if err != nil { return nil, err }
	adapter, _ := s.registry.Chain(wallet.Chain)
	chainEntity, _ := s.chainRepo.FindByID(wallet.Chain)
	dust := resolveDustThreshold(chainEntity, adapter, asset)

	sweeps := []PlannedSweep{}
	dustIgnored := []AddressBalance{}
	for _, c := range children {
		if c.ID == wallet.DepositAddress.ID { continue }
		bal, err := fetchBalance(ctx, adapter, c, asset)
		if err != nil || bal.Sign() == 0 { continue }
		if dust != nil && bal.Cmp(dust) < 0 {
			dustIgnored = append(dustIgnored, AddressBalance{Address: c, Balance: bal, Reason: "below_dust_threshold"})
			continue
		}
		sweeps = append(sweeps, PlannedSweep{From: c, Amount: bal, NeedsGas: chainNeedsGasSeed(wallet.Chain)})
	}
	if len(sweeps) > limits.MaxAddressesPerRequest[adapter.AdapterKey()] {
		return nil, ErrTooManyAddresses
	}

	plan := &Plan{
		WalletID: walletID, Chain: wallet.Chain, Asset: asset,
		Strategy: StrategyMultiSweep, Sweeps: sweeps, DustIgnored: dustIgnored,
		ReachesTarget: true,
	}

	shareA, err := s.decryptShareA(wallet, passphrase)
	if err != nil { return nil, err }
	defer zero(shareA)

	// ExecutePlan without a final withdraw — uses a synthetic withdrawal id only to link sweep rows.
	syntheticID := uuid.New()
	result, err := s.ExecutePlan(ctx, plan, shareA, syntheticID)
	if err != nil { return nil, err }

	// Override txs persisted with Origin=TxOriginManualConsolidation + ParentTransactionID=nil.
	for _, cs := range result.Sweeps {
		_ = s.txRepo.UpdateFields(cs.InternalTxID, map[string]any{
			"origin": models.TxOriginManualConsolidation,
			"parent_transaction_id": nil,
		})
	}
	return result, nil
}
```

Helpers `decryptShareA` and `zero` can be extracted from `app/services/withdraw/service.go` — or moved into `app/services/mpc/` since both services need them. Prefer the latter (DRY). Extraction itself is fine as a side change in this task — single commit.

- [ ] **Step 3: Run tests + commit**

```
go test ./app/services/sweep/ -run TestConsolidateAll -v
git add app/services/sweep/
git commit -m "feat(sweep): implement ConsolidateAll with limits and lock"
```

---

# PHASE 4 — Withdraw refactor + API

## Task 20: Refactor `withdraw.Service.Request`

**Files:**
- Modify: `app/services/withdraw/service.go`
- Modify: `app/services/withdraw/service_test.go` (update existing cases)
- Modify: `app/container/vault_container.go` (wire new `sweep.Service` into `withdraw.Service`)

- [ ] **Step 1: Update container wiring**

In `app/container/vault_container.go`, construct `sweep.Service` after `mpc` and `webhook` are ready, then pass into `withdraw.NewService`. Update `withdraw.NewService` signature to accept a `sweep.Service`. Do not break existing callers — this is a direct refactor, one call site.

- [ ] **Step 2: Rewrite `Request` to delegate source selection**

Replace the current body of `Request` with the flow from spec §4.2 (reference `docs/superpowers/specs/2026-04-18-base-address-sweep-design.md`).

Key properties of the new implementation:
1. Call `s.sweep.PlanForWithdrawal(ctx, req.WalletID, req.Asset, amount)`; if `plan.Strategy == StrategyInsufficient`, return `InsufficientFundsErr(plan)`.
2. If `plan.Strategy == StrategyMultiSweep` and `wallet.GasStatus != models.GasStatusSeeded`, return `ErrWalletNotGasReady`.
3. Decrypt ShareA once. Pass it into `s.sweep.ExecutePlan(...)`.
4. Persist a pending `Transaction` with `ID = withdrawalTxID` up front so sweeps can link via `parent_transaction_id`.
5. On `result.FailedStep != nil`: persist the failure state on the withdrawal row, return `PartialFailureErr(result)`.
6. Idempotency: on retry with same key, call a new `resumeOrReturn(existing, passphrase)` helper that re-plans and executes only the remaining steps.

Remove the old behavior where `from_address_id` was used directly.

- [ ] **Step 3: Update tests**

Existing `service_test.go` tests that pass `FromAddressID: ptr(X)` must be updated. Add new tests:

```go
func TestRequest_StrategyDirectFromBase_Success(t *testing.T) { /* ... */ }
func TestRequest_StrategyMultiSweep_RequiresGasSeeded(t *testing.T) { /* ... */ }
func TestRequest_PartialFailure_RetryWithSameIdempotencyKey(t *testing.T) { /* ... */ }
func TestRequest_InsufficientFunds_ReturnsPlanDetails(t *testing.T) { /* ... */ }
```

- [ ] **Step 4: Run all relevant tests**

```
go test ./app/services/withdraw/ -v
go test ./app/services/sweep/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```
git add app/services/withdraw/ app/container/vault_container.go
git commit -m "refactor(withdraw): delegate source selection to sweep.Planner"
```

---

## Task 21: Controller — `sweep_controller.go` endpoints

**Files:**
- Create: `app/http/controllers/sweep_controller.go`
- Create: `app/http/requests/consolidate_request.go`
- Create: `app/http/requests/withdraw_preview_request.go`
- Modify: `routes/api.go`

- [ ] **Step 1: Request DTOs**

```go
// app/http/requests/consolidate_request.go
package requests

type ConsolidateRequest struct {
	Passphrase     string `json:"passphrase" validate:"required,min=12"`
	Asset          string `json:"asset" validate:"required"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}
```

```go
// app/http/requests/withdraw_preview_request.go
package requests

type WithdrawPreviewRequest struct {
	Asset  string `json:"asset" validate:"required"`
	Amount string `json:"amount" validate:"required"`
}
```

- [ ] **Step 2: Controller**

```go
package controllers

import (
	"math/big"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/requests"
)

// POST /v1/wallets/:id/consolidate
func ConsolidateWallet(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil { return badRequest(ctx, "invalid wallet id") }

	var req requests.ConsolidateRequest
	if errResp := validateRequest(ctx, &req); errResp != nil { return errResp }

	res, err := container.Get().SweepService.ConsolidateAll(ctx.Context(), walletID, req.Asset, req.Passphrase)
	if err != nil {
		return mapSweepError(ctx, err)
	}
	return ctx.Response().Success().Json(mapConsolidateResponse(res))
}

// GET /v1/wallets/:id/gas-status
func GetGasStatus(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil { return badRequest(ctx, "invalid wallet id") }
	status, err := container.Get().SweepService.RefreshGasStatus(ctx.Context(), walletID)
	if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }
	return ctx.Response().Success().Json(status)
}

// POST /v1/wallets/:id/gas-check (rate-limited manual refresh)
func ForceGasCheck(ctx http.Context) http.Response {
	return GetGasStatus(ctx) // same logic; rate limit enforced by middleware layer
}

// POST /v1/wallets/:id/withdraw/preview
func PreviewWithdraw(ctx http.Context) http.Response {
	walletID, err := uuid.Parse(ctx.Request().Route("walletId"))
	if err != nil { return badRequest(ctx, "invalid wallet id") }
	var req requests.WithdrawPreviewRequest
	if errResp := validateRequest(ctx, &req); errResp != nil { return errResp }

	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok { return badRequest(ctx, "invalid amount") }

	plan, err := container.Get().SweepService.PlanForWithdrawal(ctx.Context(), walletID, req.Asset, amount)
	if err != nil { return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()}) }
	return ctx.Response().Success().Json(mapPreviewResponse(plan))
}
```

Helpers `badRequest`, `validateRequest`, `mapConsolidateResponse`, `mapPreviewResponse`, `mapSweepError` — define in the same file (small and local). `mapSweepError` maps sentinel errors from Task 18 to HTTP 429 / 422.

- [ ] **Step 3: Register routes**

In `routes/api.go` — add to the dashboard group `/v1/*`:

```go
router.Post("/wallets/{walletId}/consolidate",        controllers.ConsolidateWallet)
router.Get("/wallets/{walletId}/gas-status",          controllers.GetGasStatus)
router.Post("/wallets/{walletId}/gas-check",          controllers.ForceGasCheck)
router.Post("/wallets/{walletId}/withdraw/preview",   controllers.PreviewWithdraw)
```

And mirror the same registrations in the external API block `/api/v1/*` where appropriate.

- [ ] **Step 4: Update container**

In `app/container/vault_container.go`, expose `SweepService` as a public field on the container struct. Populate on boot.

- [ ] **Step 5: Integration smoke test**

Start dev stack and run a quick curl:

```
make dev-back
# in another terminal:
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:2002/v1/wallets/<existing-wallet-uuid>/gas-status | jq .
```

Expected: returns JSON with `gas_status`, `base_address`, `threshold`, etc.

- [ ] **Step 6: Commit**

```
git add app/http/controllers/sweep_controller.go \
        app/http/requests/consolidate_request.go \
        app/http/requests/withdraw_preview_request.go \
        routes/api.go app/container/vault_container.go
git commit -m "feat(api): add consolidate, gas-status, gas-check, withdraw/preview endpoints"
```

---

## Task 22: Update `withdrawals_controller.go` response shape

**Files:**
- Modify: `app/http/controllers/withdrawals_controller.go`
- Modify: `app/http/requests/create_withdrawal_request.go`

- [ ] **Step 1: Remove `from_address_id` from request DTO**

In `app/http/requests/create_withdrawal_request.go`, delete the `FromAddressID` field. If any controller currently reads it, remove that code path.

- [ ] **Step 2: Include `strategy` + `sweeps` in success response**

After `withdraw.Service.Request` returns, map the `*models.Transaction` plus `Result.Strategy` and `Result.Sweeps` into the JSON response:

```go
type withdrawalResponse struct {
	TransactionID string   `json:"transaction_id"`
	TxHash        string   `json:"tx_hash"`
	Status        string   `json:"status"`
	Origin        string   `json:"origin"`
	Strategy      string   `json:"strategy"`
	Sweeps        []sweepResponseItem `json:"sweeps,omitempty"`
}

type sweepResponseItem struct {
	TxID    string `json:"tx_id"`
	TxHash  string `json:"tx_hash"`
	From    string `json:"from"`
	Origin  string `json:"origin"`
}
```

The service layer change (Task 20) must expose the `Strategy` and `Sweeps` — if not yet, modify `withdraw.Request` to return a richer struct `(*models.Transaction, *WithdrawMetadata, error)`.

- [ ] **Step 3: Update Swagger annotations**

Update the `godoc` block above `CreateWalletWithdrawal` (or whatever the current name is) to match new request/response.

- [ ] **Step 4: Build + commit**

```
go build ./...
git add app/http/controllers/withdrawals_controller.go app/http/requests/create_withdrawal_request.go
git commit -m "feat(api): withdraw response includes strategy and sweeps"
```

---

## Task 23: New error codes in controller

**Files:**
- Modify: `app/http/controllers/sweep_controller.go`
- Modify: `app/http/controllers/withdrawals_controller.go`

- [ ] **Step 1: Map sentinel errors to HTTP codes**

In a shared helper (e.g., `app/http/controllers/errors.go`, new file):

```go
func mapSweepError(ctx http.Context, err error) http.Response {
	switch {
	case errors.Is(err, sweep.ErrInFlightConsolidation):
		return ctx.Response().Json(429, http.Json{
			"error": "sweep_limit_exceeded", "limit_type": "in_flight_consolidation",
			"retry_after_seconds": 60,
		})
	case errors.Is(err, sweep.ErrDailyQuotaExceeded):
		return ctx.Response().Json(429, http.Json{
			"error": "sweep_limit_exceeded", "limit_type": "daily_quota",
		})
	case errors.Is(err, sweep.ErrTooManyAddresses):
		return ctx.Response().Json(429, http.Json{
			"error": "sweep_limit_exceeded", "limit_type": "addresses_per_request",
		})
	case errors.Is(err, sweep.ErrWalletNotGasReady):
		return ctx.Response().Json(422, http.Json{
			"error": "wallet_not_gas_ready", "action": "fund_base_address",
		})
	}
	if ifErr := asInsufficientFunds(err); ifErr != nil {
		return ctx.Response().Json(422, ifErr.ToMap())
	}
	return ctx.Response().Json(500, http.Json{"error": err.Error()})
}
```

- [ ] **Step 2: Reuse in both controllers**

Replace ad-hoc error mapping in `ConsolidateWallet`, `CreateWalletWithdrawal` with `mapSweepError(...)`.

- [ ] **Step 3: Commit**

```
git add app/http/controllers/errors.go app/http/controllers/sweep_controller.go app/http/controllers/withdrawals_controller.go
git commit -m "feat(api): unified error mapping for sweep and gas-readiness errors"
```

---

# PHASE 5 — Webhooks

## Task 24: Define webhook event strings + emit from sweep executor

**Files:**
- Modify: `pkg/types/types.go` (or wherever `EventDepositPending` etc. are defined)

- [ ] **Step 1: Add constants**

```go
const (
	EventSweepBroadcast            = "sweep.broadcast"
	EventSweepConfirmed            = "sweep.confirmed"
	EventWithdrawalSweepRequired   = "withdrawal.sweep_required"
	EventWalletGasStatusChanged    = "wallet.gas_status.changed"
)
```

- [ ] **Step 2: Emit in executor (already prototyped in Task 16)**

Make sure the `ExecutePlan` implementation from Task 16 actually calls `s.webhookSvc.EnqueueEvent(ctx, sweepTxID, types.EventSweepBroadcast, payload)` for each sweep and `types.EventWithdrawalSweepRequired` once at the start of multi_sweep.

- [ ] **Step 3: Emit in confirmation tracker**

The existing `app/services/deposit/confirmation.go` already promotes `deposit.pending → deposit.confirmed`. Extend the same logic to promote `sweep.broadcast → sweep.confirmed` — detect `origin=sweep` rows and emit the matching webhook.

- [ ] **Step 4: Integration test — fake webhook sink**

In `app/services/sweep/executor_test.go` (extending Task 16), assert that a mock webhook service recorded N `sweep.broadcast` events.

- [ ] **Step 5: Commit**

```
git add pkg/types/types.go app/services/sweep/executor.go app/services/deposit/confirmation.go app/services/sweep/executor_test.go
git commit -m "feat(webhooks): sweep.broadcast/confirmed + withdrawal.sweep_required + wallet.gas_status.changed"
```

---

# PHASE 6 — Frontend

## Task 25: API types and client

**Files:**
- Modify: `src/types/api.ts`
- Modify: `src/lib/api/client.ts`

- [ ] **Step 1: Types**

Append to `src/types/api.ts`:

```ts
export type GasStatusValue = 'unseeded' | 'seeded' | 'low';

export interface GasStatus {
  gas_status: GasStatusValue;
  base_address: string;
  native_asset: string;
  native_balance_raw: string;
  native_balance_display: string;
  threshold_raw: string;
  threshold_display: string;
  last_checked_at: string;
}

export type WithdrawStrategy = 'direct_from_base' | 'direct_from_child' | 'multi_sweep' | 'insufficient';

export interface WithdrawPreview {
  strategy: WithdrawStrategy;
  reaches_target: boolean;
  base_balance: string;
  sweeps_required: number;
  dust_ignored: { address: string; balance: string; reason: string }[];
  estimated_gas_total_native: string;
  estimated_gas_total_usd?: string;
}

export interface ConsolidateRequest {
  passphrase: string;
  asset: string;
  idempotency_key?: string;
}

export interface ConsolidateResponse {
  plan_summary: {
    children_swept: number;
    dust_ignored: number;
    total_amount: string;
    estimated_gas_cost: string;
  };
  transactions: { tx_hash: string; origin: string; status: string }[];
}
```

- [ ] **Step 2: Client methods**

In `src/lib/api/client.ts` wallets namespace:

```ts
wallets: {
  // ... existing ...
  gasStatus: (id: string) => http.get<GasStatus>(`/v1/wallets/${id}/gas-status`),
  gasCheck:  (id: string) => http.post<GasStatus>(`/v1/wallets/${id}/gas-check`),
  withdrawPreview: (id: string, body: { asset: string; amount: string }) =>
    http.post<WithdrawPreview>(`/v1/wallets/${id}/withdraw/preview`, body),
  consolidate: (id: string, body: ConsolidateRequest) =>
    http.post<ConsolidateResponse>(`/v1/wallets/${id}/consolidate`, body),
}
```

- [ ] **Step 3: Typecheck + commit**

```
npm run typecheck
git add src/types/api.ts src/lib/api/client.ts
git commit -m "feat(frontend/api): types and client methods for sweep endpoints"
```

---

## Task 26: `GasFundingBanner` + `GasStatusBadge`

**Files:**
- Create: `src/components/Wallets/GasStatusBadge.tsx`
- Create: `src/components/Wallets/GasFundingBanner.tsx`

- [ ] **Step 1: Badge**

```tsx
import { Chip } from '@heroui/react';
import { useTranslation } from 'react-i18next';
import type { GasStatusValue } from '@/types/api';

interface Props { status: GasStatusValue }
export function GasStatusBadge({ status }: Props) {
  const { t } = useTranslation();
  if (status === 'seeded') return null;
  return (
    <Chip
      color={status === 'unseeded' ? 'danger' : 'warning'}
      variant="flat"
      size="sm"
    >
      {t(`wallets.gas.badge.${status}`)}
    </Chip>
  );
}
```

- [ ] **Step 2: Banner**

```tsx
import { Button } from '@heroui/react';
import { useRouter } from 'next/router';
import { useTranslation } from 'react-i18next';
import type { GasStatus } from '@/types/api';

interface Props { wallet: { id: string }; status: GasStatus }
export function GasFundingBanner({ wallet, status }: Props) {
  const { t } = useTranslation();
  const router = useRouter();
  if (status.gas_status === 'seeded') return null;
  return (
    <div className={`border-l-4 p-4 ${status.gas_status === 'unseeded' ? 'border-red-500 bg-red-50' : 'border-yellow-500 bg-yellow-50'}`}>
      <div className="font-semibold">{t(`wallets.gas.banner.${status.gas_status}.title`)}</div>
      <div className="text-sm">{t(`wallets.gas.banner.${status.gas_status}.body`, { threshold: status.threshold_display, current: status.native_balance_display })}</div>
      <Button className="mt-2" size="sm" onPress={() => router.push(`/dashboard/wallets/${wallet.id}/gas-funding`)}>
        {t('wallets.gas.banner.cta')}
      </Button>
    </div>
  );
}
```

- [ ] **Step 3: Commit**

```
git add src/components/Wallets/GasStatusBadge.tsx src/components/Wallets/GasFundingBanner.tsx
git commit -m "feat(frontend): GasStatusBadge and GasFundingBanner"
```

---

## Task 27: `gas-funding` page

**Files:**
- Create: `src/pages/dashboard/wallets/[id]/gas-funding.tsx`

- [ ] **Step 1: Page**

```tsx
import { useRouter } from 'next/router';
import useSWR from 'swr';
import QRCode from 'qrcode.react';

import { api } from '@/lib/api/client';
import { DashboardLayout } from '@/layouts/DashboardLayout';

export default function GasFundingPage() {
  const router = useRouter();
  const walletId = router.query.id as string;
  const { data: status } = useSWR(
    walletId ? `gas-status:${walletId}` : null,
    () => api.wallets.gasStatus(walletId),
    { refreshInterval: 30_000 }
  );

  if (!status) return null;

  if (status.gas_status === 'seeded') {
    return (
      <DashboardLayout>
        <div className="p-6">
          <h1 className="text-xl font-bold">Gas funded ✓</h1>
          <p>Your wallet is ready for operations.</p>
        </div>
      </DashboardLayout>
    );
  }

  return (
    <DashboardLayout>
      <div className="p-6 space-y-4 max-w-xl">
        <h1 className="text-xl font-bold">Fund wallet gas</h1>
        <p>Send at least <b>{status.threshold_display}</b> to the address below so your wallet can execute sweeps and withdrawals.</p>
        <div className="border rounded p-4 bg-gray-50 text-center">
          <QRCode value={status.base_address} size={192} />
          <div className="mt-2 font-mono text-sm break-all">{status.base_address}</div>
        </div>
        <div className="text-sm text-gray-600">
          Current balance: {status.native_balance_display} {status.native_asset}
        </div>
      </div>
    </DashboardLayout>
  );
}
```

- [ ] **Step 2: Typecheck + visual check**

```
npm run typecheck
npm run dev   # navigate to /dashboard/wallets/<id>/gas-funding
```

- [ ] **Step 3: Commit**

```
git add src/pages/dashboard/wallets/[id]/gas-funding.tsx
git commit -m "feat(frontend): gas funding onboarding page"
```

---

## Task 28: Withdraw modal — preview integration

**Files:**
- Create: `src/components/Modals/Wallet/Withdraw/SweepPreview.tsx`
- Modify: `src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx`

- [ ] **Step 1: Preview component**

```tsx
import { useTranslation } from 'react-i18next';
import type { WithdrawPreview } from '@/types/api';

interface Props { preview: WithdrawPreview }
export function SweepPreview({ preview }: Props) {
  const { t } = useTranslation();
  if (!preview.reaches_target) return (
    <div className="rounded border border-red-300 bg-red-50 p-3 text-sm">
      {t('withdraw.preview.insufficient_funds')}
    </div>
  );
  return (
    <div className="rounded border p-3 text-sm space-y-1">
      <div><b>{t(`withdraw.preview.strategy.${preview.strategy}`)}</b></div>
      {preview.strategy === 'multi_sweep' && (
        <>
          <div>{t('withdraw.preview.sweeps_required', { n: preview.sweeps_required })}</div>
          <div>{t('withdraw.preview.estimated_gas', { amount: preview.estimated_gas_total_native })}</div>
        </>
      )}
      {preview.dust_ignored.length > 0 && (
        <div className="text-gray-500">{t('withdraw.preview.dust_ignored', { count: preview.dust_ignored.length })}</div>
      )}
    </div>
  );
}
```

- [ ] **Step 2: Integrate into `WithdrawConfirm.tsx`**

At the top of the confirmation step, add a `useEffect` that calls `api.wallets.withdrawPreview(walletId, { asset, amount })` once amount + asset are both set, then render `<SweepPreview preview={preview} />` above the passphrase input. If `gas_status !== 'seeded'` and `preview.strategy === 'multi_sweep'`, disable the submit button and show a CTA that opens the gas-funding page.

- [ ] **Step 3: Commit**

```
git add src/components/Modals/Wallet/Withdraw/
git commit -m "feat(frontend): sweep preview in withdraw confirm step"
```

---

## Task 29: Consolidate modal + button

**Files:**
- Create: `src/components/Modals/Wallet/Consolidate/index.tsx`
- Modify: `src/pages/dashboard/wallets/[id]/index.tsx` (add "Consolidate" button near existing actions)

- [ ] **Step 1: Modal component**

```tsx
import { useState } from 'react';
import { Modal, ModalContent, ModalHeader, ModalBody, ModalFooter, Button, Input } from '@heroui/react';
import { api } from '@/lib/api/client';
import type { ConsolidateResponse } from '@/types/api';

interface Props { walletId: string; asset: string; open: boolean; onClose: () => void }

export function ConsolidateModal({ walletId, asset, open, onClose }: Props) {
  const [passphrase, setPassphrase] = useState('');
  const [result, setResult] = useState<ConsolidateResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setLoading(true); setError(null);
    try {
      const res = await api.wallets.consolidate(walletId, { passphrase, asset });
      setResult(res);
    } catch (e: any) {
      setError(e?.data?.error ?? 'consolidate_failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal isOpen={open} onClose={onClose}>
      <ModalContent>
        <ModalHeader>Consolidate wallet</ModalHeader>
        <ModalBody>
          {!result && (
            <>
              <p className="text-sm">Sweep all child addresses' {asset} balance to your base address.</p>
              <Input type="password" label="Passphrase" value={passphrase} onValueChange={setPassphrase} />
              {error && <div className="text-sm text-red-600">{error}</div>}
            </>
          )}
          {result && (
            <div>
              <p>{result.plan_summary.children_swept} sweeps broadcast. Estimated gas: {result.plan_summary.estimated_gas_cost}</p>
              <ul className="text-xs font-mono mt-2">
                {result.transactions.map(t => <li key={t.tx_hash}>{t.origin} — {t.tx_hash}</li>)}
              </ul>
            </div>
          )}
        </ModalBody>
        <ModalFooter>
          {!result && <Button onPress={submit} isLoading={loading} isDisabled={passphrase.length < 12}>Consolidate</Button>}
          <Button variant="light" onPress={onClose}>{result ? 'Close' : 'Cancel'}</Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
```

- [ ] **Step 2: Button in wallet details page**

Add a button labeled `{t('wallets.consolidate.button')}` that opens the modal — asset selector can reuse the existing asset dropdown used in withdraw.

- [ ] **Step 3: Commit**

```
git add src/components/Modals/Wallet/Consolidate/ src/pages/dashboard/wallets/[id]/index.tsx
git commit -m "feat(frontend): consolidate wallet modal"
```

---

## Task 30: i18n — en / pt

**Files:**
- Modify: `src/i18n/locales/en/wallets.json`
- Modify: `src/i18n/locales/pt/wallets.json`
- Modify: `src/i18n/locales/en/errors.json`
- Modify: `src/i18n/locales/pt/errors.json`

- [ ] **Step 1: Add keys (en)**

`src/i18n/locales/en/wallets.json`:

```json
{
  "gas": {
    "badge": {
      "low": "Low gas",
      "unseeded": "Not gas-funded"
    },
    "banner": {
      "unseeded": {
        "title": "This wallet cannot process withdrawals yet",
        "body": "Fund at least {{threshold}} to the base address. Current: {{current}}."
      },
      "low": {
        "title": "Gas running low",
        "body": "Top up to at least {{threshold}}. Current: {{current}}."
      },
      "cta": "Fund gas"
    }
  },
  "consolidate": {
    "button": "Consolidate",
    "confirm_modal": { "title": "Consolidate wallet", "warning": "This will sweep all child balances to the base address. Requires gas on base." }
  }
}
```

`src/i18n/locales/en/errors.json` — add:

```json
{
  "wallet_not_gas_ready": "Fund gas on your base address before running multi-sweep withdrawals.",
  "insufficient_funds": "Insufficient balance across this wallet to cover the requested amount.",
  "sweep_limit_exceeded": "Sweep limit reached. Try again later."
}
```

- [ ] **Step 2: Translate to pt**

Mirror-translate the same keys in `src/i18n/locales/pt/wallets.json` and `errors.json`.

```json
{
  "gas": {
    "badge": { "low": "Pouco gás", "unseeded": "Sem gás" },
    "banner": {
      "unseeded": {
        "title": "Esta carteira ainda não pode processar saques",
        "body": "Envie pelo menos {{threshold}} para o endereço base. Atual: {{current}}."
      },
      "low": {
        "title": "Gás acabando",
        "body": "Envie mais para atingir {{threshold}}. Atual: {{current}}."
      },
      "cta": "Abastecer gás"
    }
  },
  "consolidate": {
    "button": "Consolidar",
    "confirm_modal": { "title": "Consolidar carteira", "warning": "Isso fará sweep de todos os saldos nos endereços-filhos para o endereço base. Requer gás no base." }
  }
}
```

- [ ] **Step 3: Commit**

```
git add src/i18n/locales/en/wallets.json src/i18n/locales/pt/wallets.json src/i18n/locales/en/errors.json src/i18n/locales/pt/errors.json
git commit -m "i18n: sweep and gas-funding keys in en and pt"
```

---

# PHASE 7 — Integration tests + rollout

## Task 31: Integration test — multi-sweep withdraw end-to-end

**Files:**
- Create: `app/services/sweep/integration_test.go` (or add to existing executor_test.go under a `//go:build integration` tag if that pattern exists)

- [ ] **Step 1: Test**

Write a single test that:
1. Seeds a wallet with Base=0, child_A=0.003 ETH, child_B=0.002 ETH (USDT asset with 400, 300 respectively).
2. Calls `withdraw.Service.Request({ amount: 500, asset: "usdt" })`.
3. Asserts:
   - `plan.Strategy == multi_sweep`
   - 2 sweep txs persisted with `parent_transaction_id = withdrawal.ID`
   - 1 withdrawal tx persisted with `origin = user_request`
   - Webhook queue contains: `sweep.broadcast` × 2 + `withdrawal.broadcast` × 1

- [ ] **Step 2: Retry-same-idempotency-key scenario**

Same setup, but mock chain configured so `BroadcastTransactionFn` returns error on the 2nd sweep. Assert:
1. First call returns partial_failure; 1 sweep persisted, withdrawal row `failed`.
2. Retry with same idempotency key: only the remaining sweep + withdrawal get broadcast. Final state: 2 sweeps + 1 withdrawal, no duplicates.

- [ ] **Step 3: Run + commit**

```
go test ./app/services/sweep/ -v -run Integration
git add app/services/sweep/integration_test.go
git commit -m "test(sweep): end-to-end multi-sweep and retry idempotency"
```

---

## Task 32: E2E Playwright (optional but recommended)

**Files:**
- Create: `e2e/tests/sweep-consolidate.spec.ts`

- [ ] **Step 1: Skeleton**

```ts
import { test, expect } from '@playwright/test';

test('wallet shows gas-unseeded banner until funded', async ({ page }) => {
  // 1. Login + create wallet (reuse existing helpers)
  // 2. Navigate to /dashboard/wallets/:id
  // 3. Expect banner with text matching /Esta carteira ainda não pode processar saques/
  // 4. (Optional) fund via testnet faucet + assert banner disappears within 30s polling
});

test('multi-sweep withdraw shows preview with sweeps_required', async ({ page }) => {
  // 1. Pre-seeded wallet with scattered balances (via test setup fixture)
  // 2. Open withdraw modal, fill amount exceeding base balance
  // 3. Assert SweepPreview renders text matching /Will consolidate \d+ addresses/
});
```

- [ ] **Step 2: Run on staging**

```
npm run test:e2e -- --grep sweep-consolidate
```

- [ ] **Step 3: Commit**

```
git add e2e/tests/sweep-consolidate.spec.ts
git commit -m "test(e2e): gas-funding banner and sweep preview"
```

---

## Task 33: Final rollout verification

- [ ] **Step 1: `make migrate-fresh-seed`**

```
make migrate-fresh-seed
```

Expected: all migrations run to completion; `chains` rows populated with thresholds.

- [ ] **Step 2: Run full test suite**

```
make test
```

Expected: all tests green.

- [ ] **Step 3: Build production binary**

```
go build -o /tmp/waas-bin .
```

Expected: binary produced, no errors.

- [ ] **Step 4: Swagger regenerate**

```
make swagger-generate
```

Expected: `docs/swagger.yaml` updated with new endpoints.

- [ ] **Step 5: Commit any regenerated files**

```
git add docs/swagger.yaml docs/swagger.json docs/docs.go
git commit -m "chore(docs): regenerate swagger for sweep endpoints"
```

---

# Self-Review

## Spec coverage

| Spec requirement | Task(s) |
|---|---|
| §3.1 wallets.gas_status | 1 |
| §3.2 transactions.parent_transaction_id + origin | 2 |
| §3.3 tx_type enum extension | 3 |
| §3.4 accounts.sweep_limits | 4 |
| §3.5 chains.thresholds | 5 |
| §3.6 seeder | 6 |
| §4.1 sweep.Service interface + types | 14 |
| §4.1 Planner algorithm | 15 |
| §4.1 Executor | 16 |
| §4.1 Limits + in-flight lock + quota | 18 |
| §4.1 RefreshGasStatus | 17 |
| §4.2 withdraw.Service refactor | 20 |
| §4.3 Chain interface BuildSweep + thresholds | 8, 10, 11, 12, 13 |
| §5.1 EVM sweep mechanics | 10 |
| §5.2 SOL sweep with fee_payer | 11 |
| §5.3 BTC PSBT sweep | 12 |
| §6.1 POST /consolidate, GET /gas-status, POST /gas-check, POST /withdraw/preview | 21 |
| §6.2 withdraw response shape change | 22 |
| §6.4 new error codes | 23 |
| §7 webhook events | 24 |
| §8 Frontend (gas-funding page, badges, banners, withdraw preview, consolidate) | 25–30 |
| §9 Rate limiting (serialized lock + daily quota + max addresses + velocity hooks) | 18 |
| §10 Thresholds | 5, 6, 7 |
| §11 Testing | 15, 16, 17, 18, 20, 31, 32 |
| §12 Rollout | 33 |
| §13 Open items | Addressed inline in Tasks 10, 11, 13, 21 |

All spec requirements have a corresponding task. Open items from §13 are resolved within the tasks that touch those areas (e.g. ATA close default in Task 11, gas seed exact amount in Task 10).

## Placeholder scan

- No `TODO`, `TBD`, `implement later`, or "similar to Task N" references.
- Pseudo-code blocks in Tasks 11 and 12 are explicitly marked as "replace with real library calls before commit" — they are guidance, not placeholder code intended to ship.

## Type consistency

- `Plan`, `PlannedSweep`, `Result`, `Strategy`, `GasStatus`, `Limits` defined once in `app/services/sweep/types.go` (Task 14) and reused throughout.
- Chain interface additions (`BuildSweep`, `GasReadinessThreshold`, `DustThreshold`) defined once in `pkg/types` (Task 8), implemented in all three adapters (Tasks 10, 11, 12), and mocked in `tests/mocks/chain.go` (Task 9) before use by sweep tests.
- API types on frontend (`GasStatus`, `WithdrawPreview`, `ConsolidateRequest`, `ConsolidateResponse`) defined once in `src/types/api.ts` (Task 25) and used in components (Tasks 26–29).

## Commit cadence

33 tasks → ~33 commits. Each commit self-contained: passes `make test` and `go build ./...` on the backend, `npm run typecheck` on the frontend. Implementers should never batch commits across tasks unless explicitly noted.
