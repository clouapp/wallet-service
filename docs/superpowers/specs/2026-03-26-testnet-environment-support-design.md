# Testnet Environment Support — Design Spec

**Date:** 2026-03-26
**Status:** Approved (pending implementation plan)

## Overview

Enable per-account testnet/mainnet environment support. Each organization gets a paired production and testnet account. The system routes chains, wallets, transactions, and tokens based on the account's environment. A visible topbar toggle lets users switch between environments. Database-driven chain and token management replaces hardcoded Go registrations.

## Goals

1. Accounts operate in either `prod` or `test` environment, fully isolated
2. Testnet chains use prefixed identifiers (`teth`, `tbtc`, `tpolygon`, `tsol`)
3. Every user has a default account; login always lands with an active account
4. Registration creates paired prod + test accounts atomically
5. Topbar toggle makes the current environment unmistakable
6. Chains and tokens are database-managed, enabling admin addition of new EVM chains without deploys
7. Faucet and resource links help testnet users get started

---

## 1. Account Model Changes

### New fields on `accounts` table

| Column | Type | Description |
|--------|------|-------------|
| `environment` | `ENUM('prod','test')` NOT NULL DEFAULT `'prod'` | Account operating environment |
| `linked_account_id` | `UUID` nullable, FK → `accounts.id`, UNIQUE | Points to the paired account (prod ↔ test) |

**Constraints:**
- `UNIQUE(linked_account_id)` — prevents fan-out (one account can only be linked to one other)
- Application-level validation ensures a prod account only links to a test account and vice versa

### New field on `users` table

| Column | Type | Description |
|--------|------|-------------|
| `default_account_id` | `UUID` nullable, FK → `accounts.id` | Last-selected account, loaded at login |

### Invariants

- Every prod account has exactly one linked test account, and vice versa
- Both accounts in a pair share the same memberships (see Section 5 for sync mechanism)
- Wallet, transaction, and address data is completely isolated per account

### Migration for existing data

- All existing accounts get `environment = 'prod'` (column default)
- A data migration creates a linked testnet account for each existing prod account
- Existing `AccountUser` memberships are mirrored to the new testnet accounts
- Each user's `default_account_id` is set to their first existing account
- Existing wallets with `NULL` AccountID are assigned to a default account; `AccountID` becomes NOT NULL going forward with a migration backfill

---

## 2. Chains & Tokens Data Model

Chains and tokens move from hardcoded Go code to database tables. The in-memory registry is still used at runtime for performance, but populated from the database at boot.

### `chains` table

| Column | Type | Description |
|--------|------|-------------|
| `id` | `VARCHAR(20)` PK | Chain identifier: `eth`, `teth`, `polygon`, `tarbitrum`, etc. |
| `name` | `VARCHAR(100)` NOT NULL | Display name: "Ethereum", "Sepolia", "Polygon Amoy" |
| `adapter_type` | `ENUM('evm','bitcoin','solana')` NOT NULL | Which Go adapter to instantiate |
| `native_symbol` | `VARCHAR(20)` NOT NULL | Native asset symbol: `eth`, `matic`, `sol`, `btc` |
| `native_decimals` | `INT` NOT NULL | 18, 8, 9, etc. |
| `network_id` | `BIGINT` nullable | EVM chain ID (1, 137, 11155111). Null for non-EVM |
| `rpc_url` | `TEXT` NOT NULL | Encrypted RPC endpoint (via Goravel `facades.Crypt()`) |
| `is_testnet` | `BOOLEAN` NOT NULL DEFAULT `false` | Explicit testnet flag |
| `mainnet_chain_id` | `VARCHAR(20)` FK nullable | Testnet → mainnet link: `teth` → `eth` |
| `required_confirmations` | `INT` NOT NULL | 12, 128, 1, 6 |
| `icon_url` | `VARCHAR(500)` nullable | Chain icon for UI display |
| `display_order` | `INT` NOT NULL DEFAULT `0` | Controls UI presentation order |
| `status` | `ENUM('active','disabled')` NOT NULL DEFAULT `'active'` | Enable/disable without deleting |
| `created_at`, `updated_at` | timestamps | |

**Constraints:**
- `CHECK (is_testnet = (mainnet_chain_id IS NOT NULL))` — enforces consistency between the boolean flag and the FK

### `tokens` table

| Column | Type | Description |
|--------|------|-------------|
| `id` | `UUID` PK | |
| `chain_id` | `VARCHAR(20)` FK → `chains.id` | Parent chain |
| `symbol` | `VARCHAR(20)` NOT NULL | `usdt`, `usdc`, `pengu`, `trump` |
| `name` | `VARCHAR(100)` NOT NULL | "Tether USD", "USD Coin", "Pengu" |
| `contract_address` | `VARCHAR(255)` NOT NULL | On-chain contract address |
| `decimals` | `INT` NOT NULL | 6, 18, etc. |
| `icon_url` | `VARCHAR(500)` nullable | Token icon for UI display |
| `status` | `ENUM('active','disabled')` NOT NULL DEFAULT `'active'` | |
| `created_at`, `updated_at` | timestamps | |

**Constraints:**
- `UNIQUE(chain_id, contract_address)` — prevents duplicate token registrations for same contract on same chain

### `chain_resources` table

| Column | Type | Description |
|--------|------|-------------|
| `id` | `UUID` PK | |
| `chain_id` | `VARCHAR(20)` FK → `chains.id` | Parent chain |
| `type` | `ENUM('faucet','explorer','bridge','docs')` NOT NULL | Resource category |
| `name` | `VARCHAR(100)` NOT NULL | Display name: "Sepolia Faucet", "Etherscan" |
| `url` | `VARCHAR(500)` NOT NULL | The link |
| `description` | `TEXT` nullable | Help text: "Get free test ETH" |
| `display_order` | `INT` NOT NULL DEFAULT `0` | Controls UI ordering |
| `status` | `ENUM('active','disabled')` NOT NULL DEFAULT `'active'` | |
| `created_at`, `updated_at` | timestamps | |

**Constraints:**
- `UNIQUE(chain_id, type, url)` — prevents duplicate resource entries

### Adapter type constants (Go)

```go
const (
    AdapterTypeEVM     = "evm"
    AdapterTypeBitcoin = "bitcoin"
    AdapterTypeSolana  = "solana"
)
```

### Initial chain seed data

| ID | Name | Adapter | Network ID | Testnet | Mainnet Equiv |
|----|------|---------|------------|---------|---------------|
| `eth` | Ethereum | evm | 1 | false | — |
| `teth` | Sepolia | evm | 11155111 | true | `eth` |
| `btc` | Bitcoin | bitcoin | — | false | — |
| `tbtc` | Bitcoin Testnet | bitcoin | — | true | `btc` |
| `polygon` | Polygon | evm | 137 | false | — |
| `tpolygon` | Polygon Amoy | evm | 80002 | true | `polygon` |
| `sol` | Solana | solana | — | false | — |
| `tsol` | Solana Devnet | solana | — | true | `sol` |

### RPC URL encryption

- `rpc_url` column stores encrypted values using Goravel's `facades.Crypt()` (reuses the framework's existing encryption, same mechanism used for TOTP secrets)
- Decrypted at boot when loading chains from DB
- If decryption fails or URL is empty, chain is skipped with a warning log (graceful degradation)

### Boot process

1. `Container.Boot()` queries all `active` chains from DB
2. Decrypts each `rpc_url` via `facades.Crypt().DecryptString()`
3. Matches `adapter_type` against constants and instantiates the correct Go adapter
4. EVM chains reuse `NewEVMLive` — adding Arbitrum, Base, Optimism is just a DB row
5. Loads all `active` tokens from DB, registers into the in-memory registry

---

## 3. Topbar Environment Toggle & Testnet Indicator

### The toggle

A `MAINNET | TESTNET` pill in the topbar, positioned right after the logo. Clicking "Testnet" swaps the active account to the linked test account. Clicking "Mainnet" swaps back.

### Visual signals when in testnet mode

| Signal | Description |
|--------|-------------|
| Top border | 2px amber gradient across entire topbar |
| Toggle pill | "Testnet" highlighted in amber (vs green for mainnet) |
| Network badge | Pulsing badge showing active testnet names (Sepolia, Amoy, Devnet) |
| Wallet chips | Show `teth`, `tbtc` prefixed chain names |
| Avatar accent | Amber instead of green |
| Background tint | Subtle warm tint on topbar background |

### Under the hood

1. Reads current account's `linked_account_id`
2. Calls `PATCH /v1/users/me/default-account` (see Section 5 for endpoint definition)
3. Backend updates `user.default_account_id` and returns the updated account data
4. Frontend updates Redux state (`accountId`, `account`, `environment`)
5. All SWR hooks refetch because SWR cache keys include `accountId` (see Section 6)

---

## 4. Registration & Onboarding

### Single-step registration with org creation

Registration form fields: **Full Name**, **Email**, **Password**, **Organization Name** (all required).

**Note:** The current frontend registration form (`front/src/pages/login/register.tsx`) and backend `RegisterRequest` struct both need a new `organization_name` field.

### Backend (`POST /v1/auth/register`) — single transaction

1. Create `User` record
2. Create **production** `Account` (name: org name, `environment: 'prod'`)
3. Create **testnet** `Account` (name: org name, `environment: 'test'`)
4. Cross-link both via `linked_account_id`
5. Create `AccountUser` (role: `owner`) on **both** accounts
6. Set `user.default_account_id` to the production account
7. Return session JWT with `user_id` claim (account is resolved via header, see Section 5)

### Invited users

- Added to both prod and test accounts automatically (membership sync, see Section 5)
- `default_account_id` set to the prod account they were invited to

### Standalone account creation (`POST /v1/accounts`)

The existing `CreateAccount` endpoint is updated to **always create paired accounts**. Creating an account creates both prod and test variants, linked together, with the requesting user as owner of both. This preserves the "every prod account has exactly one linked test account" invariant.

---

## 5. API Routing & Data Isolation

### Core rule

An account's `environment` determines which chains, wallets, transactions, and tokens it can access. The backend enforces this.

### Account resolution middleware

**Current gap:** The existing `AccountContext` middleware reads `{accountId}` from route parameters (for `/v1/accounts/{accountId}/...` routes). However, session-auth routes like `/v1/wallets` and `/v1/chains` use the `X-Account-Id` header sent by the frontend — and no middleware currently reads it.

**New `AccountHeader` middleware** for session-auth routes:

1. Reads `X-Account-Id` from the request header
2. If missing → **400 Bad Request** ("Account ID required")
3. Loads the account from DB by ID
4. Validates the authenticated user is a member of that account (via `account_users` table)
5. If not a member → **403 Forbidden**
6. Sets `account`, `account_id`, and `account_role` on the request context
7. The account's `environment` field is available from the loaded record — no JWT claim needed, single source of truth in the database

This middleware is applied to all session-auth routes that need account scoping: `/v1/wallets`, `/v1/chains`, `/v1/transactions`, etc.

The existing `AccountContext` middleware (route-param based) remains for `/v1/accounts/{accountId}/...` routes.

### JWT approach

- **Session JWT** contains `user_id` only (Goravel's standard `facades.Auth().LoginUsingID()`)
- Account is resolved from `X-Account-Id` header per request
- The frontend sends `X-Account-Id` on every authenticated request (already implemented in `client.ts`)
- **API token JWT** contains `account_id` as before — account context is resolved from the token claim

### Chain-level validation

- `test` account + mainnet chain (`eth`) → **403 rejected**
- `test` account + testnet chain (`teth`) → allowed
- `prod` account + testnet chain (`teth`) → **403 rejected**

### Affected endpoints

| Endpoint | Middleware | Environment filter |
|----------|-----------|-------------------|
| `GET /v1/chains` | `AccountHeader` | Only chains where `is_testnet` matches account environment |
| `GET /v1/chains/:id/tokens` | `AccountHeader` | Only tokens for chains matching account environment |
| `GET /v1/chains/:id/resources` | `AccountHeader` | Resources for chains matching account environment |
| `POST /v1/wallets` | `AccountHeader` | Validates requested chain matches account environment |
| `GET /v1/wallets` | `AccountHeader` | Filtered by `account_id` from context |
| `GET /v1/transactions` | `AccountHeader` | Filtered by wallet → account from context |
| `POST /v1/withdrawals` | `AccountHeader` | Validates wallet belongs to account |

### Membership sync mechanism

When a user is added to or removed from an account, the operation is **mirrored to the linked account within the same database transaction**. This is implemented as application-level logic in the account membership service/controller:

1. `AddAccountUser` handler receives request for account A
2. Within a DB transaction:
   - Creates `AccountUser` on account A
   - Loads `account.linked_account_id` → account B
   - Creates `AccountUser` on account B with the same user and role
3. If either operation fails, the entire transaction rolls back (no partial state)

Same pattern for `RemoveAccountUser` and `UpdateAccountUserRole`.

### New endpoint: `PATCH /v1/users/me/default-account`

Switches the user's active account (used by the environment toggle and AccountSwitcher).

**Request:**
```json
{
  "account_id": "uuid-of-target-account"
}
```

**Validation:**
- `account_id` must reference an account the user belongs to
- No restriction on environment — can switch to any account (prod or test) the user is a member of

**Backend logic:**
1. Validate user membership on target account
2. Update `user.default_account_id` to the new account
3. Return the full account object (including `environment`, `linked_account_id`)

**Response:**
```json
{
  "account": {
    "id": "...",
    "name": "Acme Corp",
    "environment": "test",
    "linked_account_id": "...",
    "status": "active"
  }
}
```

**Note:** No new JWT is issued. The session JWT only contains `user_id`. The frontend updates Redux `accountId` and the `X-Account-Id` header changes on subsequent requests.

---

## 6. Login Flow Changes

### Backend (`POST /v1/auth/login`) response additions

- `account_id` — user's `default_account_id` (or first account if null)
- `account` — full account object including `environment` and `linked_account_id`
- `accounts` — list of all accounts the user belongs to (prod and test, for the AccountSwitcher and toggle)

### Frontend on login

1. Sets `accountId` from response (always present — never null)
2. Stores `account.environment` in Redux (drives topbar toggle state)
3. Stores `account.linked_account_id` (used by toggle to swap)
4. Redirects to `/dashboard/assets`

### Environment toggle click (frontend)

1. Read `linked_account_id` from current account in Redux
2. Call `PATCH /v1/users/me/default-account` with linked account ID
3. On success, update Redux: `accountId`, `account` (including `environment`)
4. SWR hooks refetch automatically — **SWR cache keys must include `accountId`** so that changing the account triggers fresh fetches instead of serving stale data from the previous environment

### `switchAccount` enhancement

The existing `switchAccount` in `useAuth` currently only dispatches to Redux. It must be enhanced to:
1. Call `PATCH /v1/users/me/default-account` to persist the preference
2. Update Redux with the response data
3. The `X-Account-Id` header updates automatically since `accountId` in Redux drives the API client

### Dashboard guard

If `accountId` is null after login (should not happen), redirect to error/onboarding page.

### Known limitation: multi-tab behavior

If a user switches environment in one browser tab, other tabs retain the previous Redux state. This is a known limitation for v1. Future improvement: use `BroadcastChannel` or `localStorage` events to sync environment across tabs.

---

## 7. Frontend Docs Section

The existing Documentation sidebar section is extended:

| Page | Description |
|------|-------------|
| **Chains** | Lists all chains for current environment with native asset, confirmations, explorer links |
| **Tokens** | Lists all tokens per chain with contract addresses and decimals |
| **Faucets** | Only visible in testnet mode; faucet links with descriptions |
| **API Reference** | Existing page, unchanged |

Data sourced from `GET /v1/chains` (filtered by environment) with nested resources and tokens.

### API endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /v1/chains` | Chains + nested resources + nested tokens, filtered by account environment |
| `GET /v1/chains/:id` | Single chain with full details |
| `GET /v1/chains/:id/tokens` | Tokens for a specific chain |
| `GET /v1/chains/:id/resources` | Resources (faucets, explorers) for a chain |

---

## 8. Seeder Updates

The seeder is updated to create the full testnet-ready development environment:

### Accounts

- **Acme Corp (prod)** — `environment: 'prod'`, linked to test account
- **Acme Corp (test)** — `environment: 'test'`, linked to prod account

### Users

- admin, alice, bob — all get memberships on **both** accounts
- `default_account_id` set to prod account for each

### Chains

Seed all 8 chains (4 mainnet + 4 testnet) into the `chains` table with encrypted RPC URLs from env vars.

### Tokens

Seed tokens for both mainnet and testnet chains (USDT, USDC per chain; additional tokens as appropriate).

### Chain resources

Seed explorer links for all 8 chains; faucet links for all 4 testnet chains.

| Chain | Type | Name | URL |
|-------|------|------|-----|
| `teth` | faucet | Sepolia Faucet | `https://sepoliafaucet.com` |
| `teth` | explorer | Sepolia Etherscan | `https://sepolia.etherscan.io` |
| `tbtc` | faucet | Bitcoin Testnet Faucet | `https://coinfaucet.eu/en/btc-testnet` |
| `tbtc` | explorer | Blockstream Testnet | `https://blockstream.info/testnet` |
| `tpolygon` | faucet | Polygon Amoy Faucet | `https://faucet.polygon.technology` |
| `tpolygon` | explorer | Amoy Polygonscan | `https://amoy.polygonscan.com` |
| `tsol` | faucet | Solana Devnet Faucet | `https://faucet.solana.com` |
| `tsol` | explorer | Solana Explorer (Devnet) | `https://explorer.solana.com/?cluster=devnet` |
| `eth` | explorer | Etherscan | `https://etherscan.io` |
| `btc` | explorer | Blockstream | `https://blockstream.info` |
| `polygon` | explorer | Polygonscan | `https://polygonscan.com` |
| `sol` | explorer | Solana Explorer | `https://explorer.solana.com` |

### Wallets

- Existing 3 wallets (eth, btc, polygon) → belong to **prod** account
- New 3 testnet wallets (teth, tbtc, tpolygon) → belong to **test** account

---

## Database Schema Summary

```
accounts
├── environment (enum: prod, test) NOT NULL DEFAULT 'prod'
├── linked_account_id (uuid, FK → accounts, UNIQUE)
└── ... existing fields

users
├── default_account_id (uuid, FK → accounts)
└── ... existing fields

wallets
├── account_id (uuid, FK → accounts) — becomes NOT NULL with migration backfill
└── ... existing fields

chains (NEW)
├── id (varchar PK)
├── name, adapter_type (enum), native_symbol, native_decimals
├── network_id, rpc_url (encrypted via facades.Crypt), is_testnet
├── mainnet_chain_id (FK → chains), required_confirmations
├── icon_url, display_order
├── status (enum)
├── CHECK (is_testnet = (mainnet_chain_id IS NOT NULL))
└── created_at, updated_at

tokens (NEW)
├── id (uuid PK)
├── chain_id (FK → chains)
├── symbol, name, contract_address, decimals
├── icon_url, status (enum)
├── UNIQUE(chain_id, contract_address)
└── created_at, updated_at

chain_resources (NEW)
├── id (uuid PK)
├── chain_id (FK → chains)
├── type (enum: faucet, explorer, bridge, docs)
├── name, url, description
├── display_order, status (enum)
├── UNIQUE(chain_id, type, url)
└── created_at, updated_at
```
