# Wallet Read Model And Refresh Architecture — Design Spec

**Date:** 2026-04-07
**Status:** Proposed
**Reviewed:** Spec self-review complete

## Overview

Add a database-backed wallet read model for balances, token balances, transactions, and Bitcoin UTXOs. The API and dashboard will read from this local copy by default. Wallets will store the current balance summary directly on the `wallets` table, while recent balance history is preserved in snapshots. Refreshes will run through shared Go services, invoked either:

- automatically through Goravel queued jobs for product events and reconciliation
- manually through Goravel Artisan commands, which run synchronously by default and can optionally enqueue jobs

This keeps the existing custody architecture intact:

- provider webhooks remain the primary signal for deposits
- withdrawals remain the source of truth for outbound custody actions
- chain RPC and third-party indexing APIs are used to build and refresh query-optimized read models

## Goals

1. Persist wallet balances, token balances, transaction history, and Bitcoin UTXOs in our database
2. Serve dashboard and API reads from the local database copy by default
3. Support targeted refreshes by wallet, chain, currency, address, or transaction
4. Use Goravel queues for background execution and retries
5. Use Goravel Artisan commands for operator-driven refresh and reconciliation
6. Keep refresh logic shared between commands and jobs
7. Support EVM, Solana, and Bitcoin from the first implementation slice
8. Allow hybrid freshness: read from DB first, but dispatch refresh work when changes are detected

## Non-Goals

- Replacing provider webhook ingest for deposits
- Rebuilding the Android kits inside the backend
- Building a full continuously streaming chain indexer for all supported chains
- Replacing the existing withdrawal security and MPC flow
- Adding user-facing real-time subscriptions in this phase

---

## 1. Chosen Architecture

### Recommended Model

Use a compact read-model layer on top of the current custody stack.

Core principles:

- **DB-first reads:** wallet pages and transaction pages read from local tables
- **Queue-first automation:** product-triggered refreshes enqueue Goravel jobs
- **Sync-first operations:** manual Artisan commands execute refresh logic immediately unless `--queue` is passed
- **Shared refresh services:** commands and jobs call the same domain services
- **Chain-specific refreshers behind shared contracts:** EVM, Solana, and Bitcoin each have focused refresh implementations

### Why This Model

Compared to the Android kits, this design borrows the useful parts:

- persisted local copy
- explicit refresh scopes
- per-chain sync state
- separation between balance refresh and transaction refresh

But it avoids adopting a mobile-SDK-style always-on local indexer. That keeps the backend aligned with the current webhook-first custody model and avoids unnecessary complexity.

---

## 2. Read Consistency Model

### Default Read Path

The API and dashboard read from database snapshots:

- wallet current balance summary from the `wallets` table
- wallet asset breakdown from `wallet_asset_balances`
- wallet balance history from `wallet_balance_snapshots`
- wallet transactions from the existing `transactions` table and `@back/app/models/transaction.go`
- Bitcoin spendable state from `wallet_utxos`
- sync metadata from `wallet_sync_states`

### Refresh Policy

When the system detects a possible state change, it dispatches only the needed refresh work.

Examples:

- withdrawal broadcasted -> refresh wallet balances + refresh affected transaction
- deposit webhook received -> refresh wallet balances + refresh wallet transactions
- new address created -> refresh address-scoped transaction discovery and balances
- stale wallet opened in UI -> mark refresh needed and optionally enqueue targeted jobs

### Freshness Contract

Each read model returns metadata that makes staleness visible:

- `last_synced_at`
- `last_attempted_at`
- `sync_status`
- `sync_error`
- `sync_lag_seconds` or equivalent derived metric

The frontend can show stale data indicators without forcing live chain reads in the request path.

---

## 3. Data Model

### 3.1 `wallets` table extensions

Store the current wallet balance summary directly on `wallets` so common reads do not need a join.

New columns to add to `wallets`:

```sql
ALTER TABLE wallets
    ADD COLUMN balance_asset VARCHAR(32),
    ADD COLUMN balance_raw TEXT,
    ADD COLUMN balance_display TEXT,
    ADD COLUMN balance_usd NUMERIC(28, 10),
    ADD COLUMN balance_last_synced_at TIMESTAMPTZ,
    ADD COLUMN read_model_status wallet_read_model_status;
```

Purpose:

- keep the most commonly used current balance on the main wallet record
- align with the current frontend expectation that a wallet already has `balance` and `balance_usd`
- avoid a mandatory join for wallet list and wallet header reads

`balance_*` on the wallet row is the summary field. Asset-level detail still lives in `wallet_asset_balances`.

### 3.2 `wallet_balance_snapshots`

Stores recent wallet balance history. Keep only the most recent 10 snapshots per wallet and chain.

```sql
CREATE TABLE wallet_balance_snapshots (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id           UUID NOT NULL REFERENCES wallets(id),
    chain_id            VARCHAR(32) NOT NULL REFERENCES chains(id),
    balance_asset       VARCHAR(32) NOT NULL,
    balance_raw         TEXT NOT NULL,
    balance_display     TEXT NOT NULL,
    balance_usd         NUMERIC(28, 10),
    captured_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Purpose:

- keep a short history of wallet balance changes
- support audit and debugging without building a full timeseries system
- make retention simple by trimming to the latest 10 snapshots after each successful refresh

### 3.3 `wallet_asset_balances`

Stores one current row per wallet, chain, and asset.

```sql
CREATE TABLE wallet_asset_balances (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id           UUID NOT NULL REFERENCES wallets(id),
    chain_id            VARCHAR(32) NOT NULL REFERENCES chains(id),
    asset_type          asset_type NOT NULL,
    asset_symbol        VARCHAR(32) NOT NULL,
    asset_name          VARCHAR(128),
    asset_contract      VARCHAR(255),           -- null for native assets
    asset_key           VARCHAR(320) NOT NULL,  -- native: symbol, token: contract address
    decimals            INT NOT NULL,
    amount_raw          TEXT NOT NULL,
    amount_display      TEXT NOT NULL,
    price_usd           NUMERIC(28, 10),        -- optional, nullable in this phase
    value_usd           NUMERIC(28, 10),        -- optional, nullable in this phase
    source_address      TEXT,                   -- optional when balance is address-derived
    last_synced_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(wallet_id, chain_id, asset_key)
);
```

Purpose:

- native balance rows for ETH, MATIC, SOL, BTC
- token balance rows for ERC-20 and SPL tokens
- one canonical table to drive asset breakdown UIs
- avoid nullable-expression uniqueness by storing a normalized `asset_key`

### 3.4 `transactions` table extensions

Reuse the existing `transactions` table and `app/models/transaction.go` as the wallet-facing transaction source. Extend it instead of introducing a parallel `wallet_transactions_index` table.

New columns to add to `transactions`:

```sql
ALTER TABLE transactions
    ADD COLUMN direction transaction_direction,
    ADD COLUMN source transaction_source,
    ADD COLUMN raw_payload JSONB,
    ADD COLUMN synced_at TIMESTAMPTZ;
```

Existing fields to continue using:

- `wallet_id`
- `address_id`
- `chain`
- `tx_type`
- `tx_hash`
- `log_index`
- `from_address`
- `to_address`
- `amount`
- `asset`
- `token_contract`
- `confirmations`
- `required_confs`
- `status`
- `fee`
- `block_number`
- `block_hash`
- `error_message`
- `confirmed_at`

Enum migration notes:

- migrate `transactions.tx_type` from varchar to `transaction_type`
- migrate `transactions.status` from varchar to `transaction_status`
- migrate `wallets.status` from varchar to `wallet_status`

Purpose:

- keep one transaction model instead of splitting custody events and wallet history into separate tables
- reduce duplication and repository divergence
- allow the existing transaction paths to become richer rather than parallel

### 3.5 `wallet_utxos`

Bitcoin-specific local spendable set and history support.

```sql
CREATE TABLE wallet_utxos (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id           UUID NOT NULL REFERENCES wallets(id),
    address_id          UUID REFERENCES addresses(id),
    chain_id            VARCHAR(32) NOT NULL REFERENCES chains(id),
    tx_hash             VARCHAR(255) NOT NULL,
    output_index        INT NOT NULL,
    address             TEXT NOT NULL,
    value_raw           TEXT NOT NULL,
    script_pub_key      TEXT,
    status              utxo_status NOT NULL,
    spent_by_tx_hash    VARCHAR(255),
    block_number        BIGINT,
    block_timestamp     TIMESTAMPTZ,
    confirmations       INT NOT NULL DEFAULT 0,
    last_synced_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(chain_id, tx_hash, output_index)
);
```

Purpose:

- replace the current fake unspents view with real persisted UTXOs
- support accurate Bitcoin balances and spendability
- give Bitcoin the same DB-first read behavior as other chains

### 3.6 `wallet_sync_states`

Tracks refresh health per wallet and scope.

```sql
CREATE TABLE wallet_sync_states (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id           UUID NOT NULL REFERENCES wallets(id),
    chain_id            VARCHAR(32) NOT NULL REFERENCES chains(id),
    sync_scope          wallet_sync_scope NOT NULL,
    status              wallet_sync_status NOT NULL,
    cursor              TEXT,                   -- provider-specific cursor or block/signature marker
    cursor_meta         JSONB,
    last_synced_at      TIMESTAMPTZ,
    last_attempted_at   TIMESTAMPTZ,
    last_error          TEXT,
    next_reconcile_at   TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(wallet_id, chain_id, sync_scope)
);
```

Purpose:

- show whether read models are current
- preserve chain-specific cursors without polluting wallet tables
- support targeted retries and reconciliation

### 3.7 Database Enums

Use PostgreSQL enums for state and type columns instead of generic varchars. Implement them through Goravel migrations, using raw SQL in the migration where needed for enum creation and removal.

Initial enum set:

- `wallet_status`
- `wallet_read_model_status`
- `asset_type`
- `wallet_sync_scope`
- `wallet_sync_status`
- `utxo_status`
- `transaction_status`
- `transaction_type`
- `transaction_direction`
- `transaction_source`

Guidelines:

- enum values should be lower-case and stable
- application constants should mirror DB enum values exactly
- Goravel migrations should create enum types in `Up()` and drop them safely in `Down()`

### 3.8 Existing Tables To Reuse

- `wallets`
- `addresses`
- `transactions` via `app/models/transaction.go`
- `withdrawals`
- `chains`
- `tokens`

The existing `transactions` table remains the custody event ledger and also becomes the wallet transaction read source after the model is extended.

---

## 4. Refresh Service Layer

### Shared Services

Add services under a new domain such as `app/services/refresh/`:

- `Dispatcher`
- `BalanceRefreshService`
- `TransactionRefreshService`
- `UtxoRefreshService`
- `ReconciliationService`

### Shared Contracts

```go
type RefreshScope string

const (
    RefreshScopeBalances     RefreshScope = "balances"
    RefreshScopeTransactions RefreshScope = "transactions"
    RefreshScopeTokens       RefreshScope = "tokens"
    RefreshScopeUtxos        RefreshScope = "utxos"
    RefreshScopeFull         RefreshScope = "full"
)

type RefreshRequest struct {
    ChainID    string
    WalletID   string
    Addresses  []string
    Currency   string
    TxHash     string
    Scope      RefreshScope
    Reason     string
    Force      bool
}
```

Rules:

- all entry points validate requests early and fail fast
- commands and jobs use the same request object
- chain-specific services may reject unsupported combinations with explicit errors

### Dispatcher Responsibilities

- normalize refresh requests
- split `full` into chain-appropriate scopes
- decide whether work runs now or is queued
- deduplicate refresh intent where practical

---

## 5. Goravel Queue Design

### Queue Driver Choice

Use Goravel queues with an SQS-backed custom queue connection in production and non-trivial local environments. Keep `sync` available for tests and simple local flows.

Reasoning:

- SQS is already part of the project architecture
- refresh jobs need background execution and retry support
- SQS is a better fit for distributed background processing already used elsewhere in the system
- Goravel supports custom queue drivers and connections, which we can use to back the queue with SQS ([Goravel Queues](https://www.goravel.dev/digging-deeper/queues.html))

### Queue Connections

Recommended config:

- `sync`: default for tests and minimal local runs
- `sqs`: default for background refresh environments

### Worker Runtime

Background refresh queues require a dedicated Goravel queue worker process in environments that use the SQS queue connection. The API process should dispatch jobs, while worker processes consume them using Goravel's queue worker and runner model ([Goravel Queues](https://www.goravel.dev/digging-deeper/queues.html)).

Implications:

- local development can still use `sync` for simplicity
- staging and production should run at least one separate refresh worker process
- queue routing only provides value once those workers are deployed

### Job Set

Add Goravel jobs under `app/jobs/`:

- `RefreshWalletBalances`
- `RefreshWalletTransactions`
- `RefreshWalletTokens`
- `RefreshWalletUtxos`
- `RefreshTransaction`
- `ReconcileWalletState`

### Queue Names

Use a single queue for this subsystem:

- `blockchain`

### Retry Policy

Each job implements `ShouldRetry(err error, attempt int) (bool, time.Duration)` with chain-aware retry limits.

Guidelines:

- transient provider/network errors: retry with backoff
- validation errors and unsupported scope combinations: do not retry
- stale cursor or provider pagination conflicts: bounded retry, then mark sync state failed

### Automatic Dispatch Rules

Automatic refresh dispatch should use Goravel events and listeners instead of wiring refresh jobs directly into every product service. This keeps wallet creation, deposit ingest, withdrawals, and reconciliation flows decoupled while still allowing queued execution ([Goravel Events](https://www.goravel.dev/digging-deeper/event.html)).

Recommended domain events:

- `WalletCreated`
- `WalletActivated`
- `AddressCreated`
- `DepositDetected`
- `WithdrawalRequested`
- `WithdrawalBroadcasted`
- `TransactionConfirmationChanged`
- `WalletRefreshRequested`

Recommended listener behavior:

- event listeners translate business events into refresh requests
- refresh listeners can run queued on the `blockchain` queue
- listeners dispatch refresh jobs or invoke the dispatcher based on event type

Product-triggered flows dispatch events by default:

- wallet creation
- wallet activation
- address creation
- deposit webhook ingest
- deposit block-scan hit
- withdrawal requested
- withdrawal signed
- withdrawal broadcast
- confirmation changes
- stale read-model detection
- scheduled reconciliation

Important constraint from Goravel events:

- queued listeners that depend on committed database state must be dispatched after the surrounding database transaction has committed ([Goravel Events](https://www.goravel.dev/digging-deeper/event.html))

### Optional Chaining

Use Goravel job chaining only when strict order matters, for example:

`RefreshWalletBalances -> RefreshWalletTransactions -> ReconcileWalletState`

This is useful for wallet activation and some backfills, but should not be the default for every event.

---

## 6. Goravel Command Design

### Command Strategy

Commands are operator entry points and run synchronously by default. They use the Goravel Artisan command system ([Goravel Artisan Console](https://www.goravel.dev/digging-deeper/artisan-console.html)).

Default behavior:

- run refresh logic immediately
- print a human-readable summary
- support `--queue` to dispatch jobs instead

### Command Set

- `refresh:wallet <wallet_id>`
- `refresh:address <chain> <address...>`
- `refresh:currency <currency> <address...>`
- `refresh:tx <chain> <tx_hash>`
- `reconcile:wallet <wallet_id>`
- `reconcile:chain <chain>`

### Shared Options

- `--scope=balances|transactions|tokens|utxos|full`
- `--chain=<chain>`
- `--queue`
- `--force`
- `--dry-run`
- `--reason=<text>`

### Command Resolution Rules

- `refresh:wallet` resolves chain from wallet
- `refresh:address` uses explicit chain and address list
- `refresh:currency` resolves native/token semantics from registered chains and tokens
- `refresh:tx` targets one known chain transaction

If a currency is ambiguous across chains, the command must fail with a clear message and require `--chain`.

---

## 7. Bootstrap And Configuration Changes

### Current Repo Reality

Today the repo has:

- queue provider registered in `config/app.go`
- only a `sync` queue connection configured
- Artisan wired in `main.go`
- no existing event registration
- no existing `bootstrap/jobs.go`
- no existing `bootstrap/commands.go`
- no existing `bootstrap/events.go`
- `bootstrap/app.go` does not call `WithJobs` or `WithCommands`

### Required Changes

#### `bootstrap/app.go`

Extend bootstrapping to include:

- `WithJobs(Jobs)`
- `WithCommands(Commands)`
- `WithEvents(Events)`
- optional `WithRunners(...)` when we want multiple dedicated queue workers with different queue assignments

Add `bootstrap/jobs.go`, `bootstrap/commands.go`, and `bootstrap/events.go` to register the new Goravel jobs, commands, events, and listeners.

#### `config/app.go`

Update `queue` config to include:

- `sync` connection
- `sqs` custom connection via the Goravel custom queue driver path
- existing `failed_jobs` storage

Keep `QUEUE_CONNECTION=sync` as a safe local default if desired, but production and staging should use SQS-backed queues.

#### `main.go`

No structural change is needed for CLI dispatch because `facades.Artisan().Run(os.Args, true)` already exists. The main changes are:

- new commands become discoverable through registration
- queued refresh jobs and queued listeners can run under Goravel queue workers
- deployment must include at least one dedicated queue worker process outside the HTTP request path

---

## 8. Chain-Specific Refresh Strategy

### 8.1 EVM

#### Balances

- native balance from JSON-RPC `eth_getBalance`
- token balances from configured ERC-20 contracts using `eth_call balanceOf`
- persist one row per asset in `wallet_asset_balances`

#### Transactions

Use provider-backed history and indexing where possible instead of raw per-wallet block scans.

Recommended sources:

- RPC for targeted receipts and balance reconciliation
- provider APIs such as Alchemy transfers or equivalent indexed endpoints for wallet history

#### Sync State

Store last provider cursor or last indexed block in `wallet_sync_states.cursor_meta`

### 8.2 Solana

#### Balances

- native balance from `getBalance`
- SPL balances from token accounts
- persist token-account-derived balances into `wallet_asset_balances`

#### Transactions

- address signature pagination
- parsed transaction fetches
- optional Helius enhanced APIs for richer token history

#### Sync State

Store cursor by last signature, last slot, or provider page token as appropriate

### 8.3 Bitcoin

#### Balances

- derive wallet BTC balance from persisted UTXOs
- refresh from indexed address/utxo APIs, not from the current synthetic unspent view

#### Transactions

- ingest address transaction history from Bitcoin index sources
- map spends and receives into the existing `transactions` table

#### UTXOs

- maintain `wallet_utxos` as the spendable source for DB-first Bitcoin reads
- reconcile spent outputs and orphaned outputs during refresh

#### Sync State

Store last seen block height and any provider pagination cursor

---

## 9. API Surface Changes

### Wallet Reads

`GET /v1/wallets/:id` and wallet list responses should be enriched from `wallet_asset_balances` and `wallet_sync_states`.

At minimum expose:

- native balance
- optional USD valuation if available
- `last_synced_at`
- `sync_status`

### Transaction Reads

Wallet transaction endpoints should read from the existing `transactions` table after its model and schema are extended to support richer wallet-facing query needs.

### Manual Refresh Endpoint

Optional but recommended later:

`POST /v1/wallets/:id/refresh`

Behavior:

- validates scope
- dispatches refresh jobs
- returns `202 Accepted`

This is separate from the operator Artisan commands and is not required for the first slice.

---

## 10. Refresh Triggers

### Event-Driven Automatic Triggers

- deposit webhook created or updated a relevant transaction
- block scan detected a deposit
- withdrawal state changed
- wallet activated
- wallet/address backfill required
- scheduled stale-data reconciliation

These flows should emit Goravel events first, and the listeners should enqueue refresh work onto the `blockchain` queue.

### Sync-First Command Triggers

- operator runs a Goravel command with wallet, currency, address, or tx parameters
- command executes immediately unless `--queue` is passed

### Staleness Heuristics

Mark refresh needed when:

- wallet read model has no `last_synced_at`
- last sync failed
- wallet has pending or confirming transactions
- wallet has recent deposit or withdrawal activity
- wallet page opened and data is older than configured freshness threshold

---

## 11. Rollout Plan

### Phase 1: Foundation

- add new tables
- add repositories and models
- add refresh services
- add Goravel jobs and commands
- add SQS-backed queue connection
- add bootstrap registration for jobs, commands, and events

### Phase 2: EVM

- implement EVM balance refresh
- implement EVM transaction refresh
- wire queue triggers for deposit and withdrawal events
- expose DB-backed balances and history in API

### Phase 3: Solana

- implement SOL and SPL balance refresh
- implement Solana transaction refresh
- integrate Helius or equivalent indexed history path where needed

### Phase 4: Bitcoin

- implement UTXO refresh
- implement Bitcoin transaction mirror
- switch wallet unspent views to persisted `wallet_utxos`

### Phase 5: Reconciliation And Hardening

- add scheduled reconciliation jobs
- add stale-data heuristics
- add optional manual refresh endpoint
- tune retries, timeouts, and queue routing

---

## 12. Risks And Mitigations

### Provider Inconsistency

Risk:

- EVM, Solana, and Bitcoin each need different history/indexing sources

Mitigation:

- isolate provider-specific code behind refresh services
- keep shared read-model contracts stable

### Cursor Drift Or Partial Backfill

Risk:

- transactions may be missed or duplicated during paging transitions

Mitigation:

- persist cursors in `wallet_sync_states`
- use idempotent upserts
- allow explicit reconcile commands and jobs

### Command And Job Divergence

Risk:

- manual commands behave differently from queued background execution

Mitigation:

- commands and jobs call the same refresh services
- commands do parameter parsing only

### Bitcoin Complexity

Risk:

- Bitcoin requires real UTXO tracking, which is materially different from EVM and Solana

Mitigation:

- give Bitcoin its own `wallet_utxos` scope and services
- do not force UTXO logic into generic balance-only code paths

### Operational Misconfiguration

Risk:

- jobs are registered but queue connection remains `sync` in environments that expect background execution

Mitigation:

- document `QUEUE_CONNECTION=redis` for staging and production
- log active queue connection at startup

---

## 13. Testing Strategy

### Unit Tests

- refresh request validation
- scope routing
- deduplication and idempotent upserts
- chain-specific parsing and persistence
- command argument validation
- job retry behavior

### Integration Tests

- EVM balance refresh writes expected rows
- Solana token refresh writes SPL balances
- Bitcoin UTXO refresh updates spendable outputs
- deposit and withdrawal events enqueue the right jobs
- queued job execution updates sync state correctly

### Operator Tests

- `refresh:wallet` runs synchronously by default
- `refresh:wallet --queue` dispatches instead of executing
- ambiguous currency refresh fails clearly without enough parameters
- queued listeners do not run before required DB state is committed

---

## 14. File Layout

```text
back/app/jobs/
├── refresh_wallet_balances.go
├── refresh_wallet_transactions.go
├── refresh_wallet_tokens.go
├── refresh_wallet_utxos.go
├── refresh_transaction.go
└── reconcile_wallet_state.go

back/app/events/
├── wallet_created.go
├── wallet_activated.go
├── address_created.go
├── deposit_detected.go
├── withdrawal_broadcasted.go
└── wallet_refresh_requested.go

back/app/listeners/
├── enqueue_wallet_refresh.go
├── enqueue_transaction_refresh.go
└── enqueue_utxo_refresh.go

back/app/console/commands/
├── refresh_wallet.go
├── refresh_address.go
├── refresh_currency.go
├── refresh_tx.go
├── reconcile_wallet.go
└── reconcile_chain.go

back/app/services/refresh/
├── dispatcher.go
├── balances.go
├── transactions.go
├── utxos.go
├── reconcile.go
└── providers/
    ├── evm.go
    ├── solana.go
    └── bitcoin.go

back/app/models/
├── wallet.go                 # extended with current balance summary
├── transaction.go            # extended wallet-facing transaction model
├── wallet_asset_balance.go
├── wallet_balance_snapshot.go
├── wallet_utxo.go
└── wallet_sync_state.go

back/app/repositories/
├── wallet_asset_balance_repository.go
├── wallet_transaction_index_repository.go
├── wallet_utxo_repository.go
└── wallet_sync_state_repository.go

back/bootstrap/
├── app.go
├── events.go
├── jobs.go
└── commands.go

back/database/migrations/
├── xxxxxx_create_blockchain_enum_types.go
├── xxxxxx_add_wallet_read_model_columns.go
├── xxxxxx_create_wallet_balance_snapshots_table.go
├── xxxxxx_create_wallet_asset_balances_table.go
├── xxxxxx_extend_transactions_table_for_wallet_reads.go
├── xxxxxx_create_wallet_utxos_table.go
└── xxxxxx_create_wallet_sync_states_table.go
```

---

## 15. Final Recommendation

Implement a DB-backed read model with:

- Goravel SQS-backed queues for automatic refreshes
- Goravel events and queued listeners for automatic dispatch
- Goravel Artisan commands for sync-first operator refreshes
- shared refresh services for all execution paths
- chain-specific refresh implementations for EVM, Solana, and Bitcoin

This gives `macro-wallets` the most valuable benefits seen in the Android kits, while staying aligned with the current backend architecture and deposit/withdrawal model.
