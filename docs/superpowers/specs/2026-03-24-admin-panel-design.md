# Vault Admin Panel — Design Spec

**Date:** 2026-03-24
**Project:** Vault Custody Service — Admin Panel + Backend Completion
**Status:** Approved (v2 — post spec review)

---

## Overview

Build a full-stack admin panel for the Vault multi-chain cryptocurrency custody service. The frontend is a mobile-first Next.js admin panel inspired by BitGo's UI. The backend (Go) is extended with missing routes, a multitenant account structure, and user authentication.

---

## Stack

### Frontend
- **Framework:** vinext (Next.js 15 on Cloudflare Workers, Pages Router)
- **UI Library:** HeroUI 2.x + Tailwind CSS
- **Design System:** Copied from `cloubet/front` — `whitelabel.json`, `globals.css`, Tailwind config, HeroUI plugin setup
- **State:** Redux Toolkit + redux-persist (auth session, current account, user profile)
- **Data fetching:** SWR + native `fetch` against Vault REST API
- **Auth:** Short-lived JWT (15 min) + httpOnly refresh token cookie (30 days); HMAC stays for programmatic access tokens
- **Disabled / Feature-flagged off:** Meilisearch instant search, Crisp chat

### Backend
- **Language:** Go 1.22
- **Framework:** Goravel (v1.17.2) with Goravel/Gin adapter — all controllers implement `http.Context`, routes use `facades.Route()`, middleware uses Goravel's contract. This is NOT bare Gin.
- **ORM:** Goravel ORM via `facades.Orm()`
- **Database:** PostgreSQL (existing)
- **Cache:** Redis (existing)
- **Queue:** AWS SQS (existing)

---

## Architecture

### Tenant Hierarchy

```
User (root)
  └── Account [role: owner | admin | auditor | user]
        └── Wallet [user roles: admin | spend | view  (multi-role, TEXT[])]
              ├── Address
              ├── Transaction
              ├── Unspent (UTXO chains: BTC, LTC, DOGE only)
              ├── Whitelist
              └── Webhook
```

A `User` exists at root level and can belong to multiple `Account`s with different roles. All wallet data is scoped to an `Account`. Wallet users carry multiple roles simultaneously (e.g. `["admin","spend","view"]`); account users carry exactly one role per membership.

### Frontend Layout Shells

- **PublicLayout** — landing page + auth pages (no sidebar)
- **DashboardLayout** — top nav (logo, "Assets" nav item, account switcher, notifications, profile) + sidebar

### Dual-Auth Context

Two credential types share the same `/v1/*` prefix:

| Credential | Used by | Account context extraction |
|------------|---------|---------------------------|
| Session JWT | Admin panel UI | Required `X-Account-Id` header; validated against `account_users` for membership |
| HMAC access token | Programmatic API | `account_id` read from the stored `access_tokens` row |

The account context middleware first checks for a `Bearer` JWT; if absent, falls back to HMAC `X-API-Key` + `X-API-Signature`. Both paths populate a shared `AccountContext` struct passed through the request lifecycle.

---

## Database Models

### New / Updated Models

```sql
-- Users (root level, owns sessions)
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email VARCHAR UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  full_name VARCHAR,
  totp_secret TEXT,
  totp_enabled BOOLEAN DEFAULT false,
  status VARCHAR DEFAULT 'active',
  created_at TIMESTAMP DEFAULT NOW()
);

-- 2FA recovery codes (single-use)
CREATE TABLE totp_recovery_codes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users ON DELETE CASCADE,
  code_hash TEXT NOT NULL,
  used_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);

-- JWT refresh tokens (for revocation)
CREATE TABLE refresh_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users ON DELETE CASCADE,
  token_hash TEXT NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  revoked_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);

-- Password reset tokens
CREATE TABLE password_reset_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users ON DELETE CASCADE,
  token_hash TEXT NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  used_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);

-- Accounts (replaces "Enterprise")
CREATE TABLE accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR NOT NULL,
  status VARCHAR DEFAULT 'active',  -- active | archived | frozen
  view_all_wallets BOOLEAN DEFAULT false,
  created_at TIMESTAMP DEFAULT NOW()
);

-- Account membership
-- Soft-delete via deleted_at so re-adding a user preserves audit history
CREATE TABLE account_users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID REFERENCES accounts ON DELETE CASCADE,
  user_id UUID REFERENCES users ON DELETE CASCADE,
  role VARCHAR NOT NULL,           -- owner | admin | auditor | user  (single role)
  status VARCHAR DEFAULT 'active',
  added_by UUID REFERENCES users,
  deleted_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);
-- Partial unique index: enforces exactly one active membership per (account, user).
-- Soft-deleted rows (deleted_at IS NOT NULL) are excluded, allowing clean re-add.
-- Re-add = application clears deleted_at on the existing row (does not insert a new row).
CREATE UNIQUE INDEX account_users_active_unique ON account_users (account_id, user_id)
  WHERE deleted_at IS NULL;

-- Access tokens (scoped to account, created by a user)
CREATE TABLE access_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID REFERENCES accounts ON DELETE CASCADE,
  created_by UUID REFERENCES users,
  name VARCHAR NOT NULL,
  token_hash TEXT NOT NULL,
  permissions TEXT[],
  ip_cidr TEXT,
  spending_limit JSONB,
  valid_until TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);

-- Wallet users (multi-role per user)
CREATE TABLE wallet_users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id UUID REFERENCES wallets ON DELETE CASCADE,
  user_id UUID REFERENCES users ON DELETE CASCADE,
  roles TEXT[] NOT NULL,           -- ["admin","spend","view"] — multi-role
  status VARCHAR DEFAULT 'active',
  deleted_at TIMESTAMP,
  created_at TIMESTAMP DEFAULT NOW()
);
CREATE UNIQUE INDEX wallet_users_active_unique ON wallet_users (wallet_id, user_id)
  WHERE deleted_at IS NULL;

-- Whitelist
CREATE TABLE whitelist_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id UUID REFERENCES wallets ON DELETE CASCADE,
  label VARCHAR,
  address TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT NOW()
);

-- Wallets: add columns (migrations)
ALTER TABLE wallets ADD COLUMN account_id UUID REFERENCES accounts;
ALTER TABLE wallets ADD COLUMN label VARCHAR;
ALTER TABLE wallets ADD COLUMN status VARCHAR DEFAULT 'active';

-- Fee settings are UTXO-chain-specific (BTC, LTC, DOGE).
-- Stored as nullable; ignored silently for EVM/Solana chains.
-- The API and UI must gate these fields based on wallet.chain.
ALTER TABLE wallets ADD COLUMN fee_rate_min INTEGER;        -- sats/vB, nullable
ALTER TABLE wallets ADD COLUMN fee_rate_max INTEGER;        -- sats/vB, nullable
ALTER TABLE wallets ADD COLUMN fee_multiplier NUMERIC;      -- nullable

-- required_approvals: reserved for future multi-sig approval workflow.
-- Currently stored but not enforced. Set to 1 (default) for all wallets.
ALTER TABLE wallets ADD COLUMN required_approvals INTEGER DEFAULT 1;
ALTER TABLE wallets ADD COLUMN frozen_until TIMESTAMP;

-- Addresses: add metadata columns
ALTER TABLE addresses ADD COLUMN label VARCHAR;
ALTER TABLE addresses ADD COLUMN created_by UUID REFERENCES users;

-- Webhooks: add wallet_id (migration from flat to wallet-scoped)
-- The existing flat /v1/webhooks endpoints are DEPRECATED.
-- They remain functional for 1 release cycle then are removed.
-- New code targets /v1/wallets/:id/webhooks.
ALTER TABLE webhooks ADD COLUMN wallet_id UUID REFERENCES wallets;

-- Withdrawals: add an explicit withdrawals table for status tracking
-- The existing flow creates a Transaction and returns it.
-- A new withdrawals record is created alongside the transaction.
CREATE TABLE withdrawals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id UUID REFERENCES wallets ON DELETE CASCADE,
  transaction_id UUID REFERENCES transactions,
  account_id UUID REFERENCES accounts,
  status VARCHAR DEFAULT 'pending',
  amount NUMERIC NOT NULL,
  destination_address TEXT NOT NULL,
  fee_estimate NUMERIC,
  note TEXT,
  created_by UUID REFERENCES users,
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

-- Transactions: add wallet_id scoping
-- Existing /v1/transactions and /v1/transactions/:id are DEPRECATED.
-- They remain for 1 release cycle. New code targets wallet-scoped routes.
ALTER TABLE transactions ADD COLUMN wallet_id UUID REFERENCES wallets;
```

---

## API Routes

All new routes use Goravel's routing DSL (`facades.Route()`) and controller methods implementing `http.Context`. Middleware uses Goravel's `http.Middleware` contract.

### Pagination Convention

All `GET` list endpoints accept:
- `?limit=20` (default 20, max 100)
- `?offset=0`
- `?cursor=<opaque>` (for cursor-based pagination where supported)

### Auth (new — session-based JWT for UI)

```
POST   /auth/register                 → Create user account
POST   /auth/login                    → Email + password → returns access JWT + sets refresh cookie
POST   /auth/refresh                  → Exchange refresh token cookie → new access JWT
POST   /auth/logout                   → Revoke refresh token (sets revoked_at in refresh_tokens)
POST   /auth/2fa/verify               → Verify TOTP code (called after login if 2FA enabled)
POST   /auth/2fa/setup                → Generate 2FA QR + secret (requires active session)
POST   /auth/2fa/confirm              → Confirm TOTP code → enable 2FA + return recovery codes
DELETE /auth/2fa                      → Disable 2FA (requires TOTP confirmation)
GET    /auth/2fa/recovery-codes       → List remaining recovery codes (hashed, shows count only)
POST   /auth/2fa/recovery-codes/use   → Consume a recovery code in place of TOTP
POST   /auth/recover                  → Send password reset email
POST   /auth/recover/confirm          → Reset password with token (also revokes all refresh tokens)
```

**JWT Strategy:**
- Access token: 15-minute lifetime, signed HS256, contains `user_id` + `jti`
- Refresh token: 30-day lifetime, stored as hash in `refresh_tokens` table, sent as httpOnly `Secure` cookie on `/auth` path
- Logout and password change both set `revoked_at` on the refresh token row — stateless access tokens expire naturally within 15 min

### User Profile (new)

```
GET    /v1/user/me                    → Get my profile
PUT    /v1/user/me                    → Update name/email
POST   /v1/user/me/password           → Change password (also revokes all refresh tokens)
GET    /v1/user/me/accounts           → List all accounts I belong to (for switcher); paginated
```

### Accounts (new)

```
GET    /v1/accounts                   → List accounts (paginated)
POST   /v1/accounts                   → Create account
GET    /v1/accounts/:id               → Get account details
PUT    /v1/accounts/:id               → Update settings (name, view_all_wallets)
POST   /v1/accounts/:id/freeze        → Freeze account
POST   /v1/accounts/:id/archive       → Archive account (status → archived)
GET    /v1/accounts/:id/users         → List account users (paginated); includes soft-deleted? no
POST   /v1/accounts/:id/users         → Add user to account (re-add: clears deleted_at)
DELETE /v1/accounts/:id/users/:userId → Soft-delete account user (sets deleted_at)
GET    /v1/accounts/:id/tokens        → List access tokens (paginated)
POST   /v1/accounts/:id/tokens        → Create access token
DELETE /v1/accounts/:id/tokens/:id    → Hard-delete access token
```

### Wallets

```
GET    /v1/wallets                    → List wallets (account-scoped, paginated) — EXISTING
POST   /v1/wallets                    → Create wallet — EXISTING
GET    /v1/wallets/:id                → Get wallet details — EXISTING (already implemented)
PUT    /v1/wallets/:id                → Update wallet name/settings
POST   /v1/wallets/:id/freeze         → Freeze wallet (sets frozen_until)
POST   /v1/wallets/:id/archive        → Archive wallet (status → archived)

-- UTXO-chain wallets only (BTC, LTC, DOGE) --
GET    /v1/wallets/:id/unspents       → List UTXOs (paginated)
POST   /v1/wallets/:id/consolidate    → Consolidate unspents

-- Wallet users --
GET    /v1/wallets/:id/users          → List wallet users (paginated)
POST   /v1/wallets/:id/users          → Add wallet user
DELETE /v1/wallets/:id/users/:userId  → Soft-delete wallet user

-- Whitelist --
GET    /v1/wallets/:id/whitelist      → List whitelist entries (paginated)
POST   /v1/wallets/:id/whitelist      → Add whitelist entry
DELETE /v1/wallets/:id/whitelist/:eid → Hard-delete whitelist entry

-- Webhooks (wallet-scoped; replaces deprecated flat /v1/webhooks) --
GET    /v1/wallets/:id/webhooks       → List webhooks (paginated)
POST   /v1/wallets/:id/webhooks       → Add webhook (requires wallet_id on webhooks table)
DELETE /v1/wallets/:id/webhooks/:wid  → Remove webhook
POST   /v1/wallets/:id/webhooks/:wid/test → Test webhook (fires a test event)

-- Addresses (all already implemented; listed for completeness) --
GET    /v1/wallets/:id/addresses      → List addresses (paginated) — EXISTING
POST   /v1/wallets/:id/addresses      → Generate address — EXISTING
GET    /v1/addresses/:address         → Lookup address — EXISTING

-- Transactions (wallet-scoped; replaces deprecated flat /v1/transactions) --
GET    /v1/wallets/:id/transactions   → List transactions (paginated, filterable by status/asset/type/date)
GET    /v1/wallets/:id/transactions/:txId → Transaction detail

-- Withdrawals --
POST   /v1/wallets/:id/withdrawals    → Create withdrawal (creates withdrawals row + transaction)
GET    /v1/wallets/:id/withdrawals    → List withdrawals for wallet (paginated)
GET    /v1/withdrawals/:id            → Get withdrawal status (by withdrawals.id)
-- Note: withdrawal history is also surfaced through GET /v1/wallets/:id/transactions
-- (transactions of type "withdrawal"). The dedicated withdrawals list adds status/fee detail.
```

### Deprecated Routes (kept for 1 release, then removed)

```
GET    /v1/transactions               → DEPRECATED; use /v1/wallets/:id/transactions
GET    /v1/transactions/:id           → DEPRECATED; use /v1/wallets/:id/transactions/:txId
POST   /v1/webhooks                   → DEPRECATED; use /v1/wallets/:id/webhooks
GET    /v1/webhooks                   → DEPRECATED; use /v1/wallets/:id/webhooks
```

### Goravel Auth Guards

Authentication uses `facades.Auth(ctx)` with two named guards defined in `config/auth.go`:

| Guard | Driver | Used by |
|-------|--------|---------|
| `web` | JWT | Admin panel UI (session-based) |
| `api` | custom | HMAC access tokens (programmatic) |

**Web guard (JWT):**
- `facades.Auth(ctx).Guard("web").Login(&user)` — generates access JWT on login
- `facades.Auth(ctx).Guard("web").Parse(token)` — validates token; use `errors.Is(err, auth.ErrorTokenExpired)` to detect expiry
- `facades.Auth(ctx).Guard("web").User(&user)` — retrieves authenticated user after Parse
- `facades.Auth(ctx).Guard("web").Refresh()` — issues a new JWT; used by `POST /auth/refresh`
- `facades.Auth(ctx).Guard("web").Logout()` — invalidates the token server-side

JWT config in `config/jwt.go`: `JWT_SECRET`, `JWT_TTL=15` (minutes), `JWT_REFRESH_TTL=43200` (30 days in minutes).

The `refresh_tokens` table (spec DB models) stores hashed refresh cookies separately for explicit revocation on logout and password change — Goravel's built-in `Logout()` handles the in-memory invalidation; the DB row handles cross-device/cross-session revocation.

### Authorization (Goravel Gates + Policies)

Role-based access uses `facades.Gate()` instead of a custom RBAC middleware.

**Gate definitions** (registered in `AuthServiceProvider.Boot()`):

```go
// Account-level gates
facades.Gate().Define("account:admin", func(ctx context.Context, args map[string]any) access.Response {
    // check account_users role >= admin
})
facades.Gate().Define("account:owner", ...)
facades.Gate().Define("wallet:spend", ...)
// etc.
```

**Policies** generated via `go run . artisan make:policy AccountPolicy`:
- `app/policies/account_policy.go` — gates for account actions (freeze, archive, manage users, tokens)
- `app/policies/wallet_policy.go` — gates for wallet actions (deposit, withdraw, settings)

**Usage in controllers:**
```go
if facades.Gate().WithContext(ctx).Denies("account:admin", map[string]any{"account": account}) {
    return ctx.Response().Json(403, ...)
}
```

### Middleware (Goravel contracts)

- **SessionAuthMiddleware** — calls `facades.Auth(ctx).Guard("web").Parse(token)`; extracts `user_id`
- **HMACAuthMiddleware** — validates `X-API-Key` + `X-API-Signature`; extracts `account_id` from `access_tokens`
- **AccountContextMiddleware** — requires `X-Account-Id` header for JWT sessions; validates user membership via `account_users`; merges with HMAC context
- **ChainTypeMiddleware** — for UTXO-only endpoints, returns 404 if wallet.chain is not BTC/LTC/DOGE

---

## Frontend Routes

### Public

```
/                                     → Landing page (marketing + signup/login CTA)
/login                                → Login (email + password)
/login/2fa                            → 2FA verification
/login/recover                        → Password recovery (enter email)
/login/recover/confirm                → Recovery confirmation (enter new password)
```

### Dashboard

```
/dashboard/assets                     → Assets page (toggle: by wallets / by assets)
/dashboard/assets/[chain]             → Asset drill-down (wallets for one chain)
                                        NOTE: Pages Router serves static "create" before dynamic [chain]
/dashboard/assets/create              → Create wallet modal (static segment — takes precedence over [chain])

/dashboard/wallets/[id]               → Wallet detail (Overview tab default)
/dashboard/wallets/[id]/deposit       → Deposit modal
/dashboard/wallets/[id]/withdraw      → Withdraw modal
/dashboard/wallets/[id]/addresses/generate   → Generate address modal
/dashboard/wallets/[id]/users/add            → Add wallet user modal
/dashboard/wallets/[id]/whitelist/add        → Add whitelist entry modal
/dashboard/wallets/[id]/whitelist/upload     → CSV upload modal
/dashboard/wallets/[id]/settings/password    → Update wallet password modal
/dashboard/wallets/[id]/settings/freeze      → Freeze wallet modal
/dashboard/wallets/[id]/settings/webhooks/add → Add webhook modal
/dashboard/wallets/[id]/transactions/[txId]   → Transaction detail page

/dashboard/accounts                   → List all accounts (root view)
/dashboard/accounts/create            → Create account modal

/dashboard/settings                   → Account Settings (tabs: General, Developer Options, Users)
/dashboard/settings/tokens/create     → Create access token modal
/dashboard/settings/users/add         → Add account user modal

/dashboard/profile                    → User profile overview
/dashboard/profile/edit               → Edit name/email
/dashboard/profile/password           → Change password
/dashboard/profile/2fa                → Enable/manage 2FA + view recovery code count
```

**Modal routing pattern (Pages Router):** Each modal route renders its parent page in the background with the modal overlaid. Navigating away or pressing Escape closes the modal and returns to the parent URL using `router.back()`.

**Static vs dynamic segment note:** In `pages/dashboard/assets/`, `create.tsx` (static) takes precedence over `[chain].tsx` (dynamic) for the literal path `/assets/create`. This is intentional and correct — do not nest `create` inside `[chain]/`.

---

## Page Inventory

| Page | Description |
|------|-------------|
| Landing `/` | Hero section, product features, CTA to login/signup |
| Login | Email + password form |
| 2FA | TOTP code entry |
| Password Recovery | Email input → confirmation sent |
| Recovery Confirm | New password + confirm |
| Assets — By Wallets | Total USD header; table: wallet name, balance, type, role, Deposit/Withdraw |
| Assets — By Assets | Total USD header; table: asset icon, balance, price, portfolio % bar, Deposit/Withdraw |
| Asset Drill-down | Breadcrumb Assets > [Chain]; total for chain; filtered wallet table |
| Wallet Detail | Header: name (inline edit), wallet ID (copy), Show More; tabs below |
| — Overview tab | Balances table (expandable tokens, Deposit/Withdraw per token) + Transaction History (filterable) |
| — Transactions tab | Full filterable transaction list (non-UTXO chains) |
| — Unspents tab | UTXO list: status, amount, address, unspent ID, Consolidate (UTXO chains only) |
| — Addresses tab | Address list: label+timestamp, address, quantity, total received/sent; Generate Address |
| — Users tab | Wallet users: avatar initials, name/email, role badges, status; Add User |
| — Whitelist tab | Trusted addresses list; empty state; Add + CSV Upload |
| — Settings tab | Name, wallet ID, password, fee rates (UTXO only), fee multiplier (UTXO only), required approvals (display only, future), freeze, bulk withdrawal, archive, webhooks list |
| Transaction Detail | Two-column: details card (status, IDs, from/to, amount, note) + Security Timeline |
| Deposit Modal | QR code + deposit address, chain selector |
| Withdraw Modal | Amount, destination address, note, fee estimate; submit creates withdrawal |
| Create Wallet Modal | Chain selector, wallet name, type |
| Account Settings — General | Account name, ID, role, archive toggle, freeze button, view-all-wallets toggle |
| Account Settings — Developer Options | Access tokens list; Create Access Token modal |
| Account Settings — Users | Account users list (avatar, name/email, role, status); Add User |
| Create Access Token Modal | Name, valid until, permissions checkboxes, IP/CIDR, spending limits, terms |
| Accounts List | All accounts user belongs to; Create Account |
| User Profile | Name, email, 2FA status (enabled + recovery code count), links to edit/password/2FA |
| Profile Edit | Name + email form |
| Profile Password | Current password, new password, confirm |
| Profile 2FA | QR code setup flow, confirm with TOTP code, display recovery codes (one-time reveal) |
| Account Switcher | Dropdown: list of accounts + active checkmark + Create New Account + Enroll in Custody |

---

## Components — Copied from cloubet/front

**Layout & Shell**
- `Header` — strip wallet/currency selector; keep profile + notifications pattern
- `WebMenu` / `MobileMenu` — simplify to Assets + Account Settings nav items
- `LayoutProvider` — shell wiring

**Auth**
- `Authentication/` — login form, 2FA flow, password recovery
- `useAuth` hook — adapted for JWT + refresh token (no GraphQL)
- `useAuthSync` hook — token persistence on mount

**UI Primitives**
- `Button/` — all variants
- `Inputs/` — form inputs
- `Table/` — data table base
- `Modals/` — modal system + `ModalsProvider`
- `Transactions/` — transaction row display

**Design System**
- `whitelabel.json` — color tokens
- `globals.css` — CSS variables, utilities
- `tailwind.config.js` — HeroUI plugin + animations
- `styles/global.ts` — layout class exports

**Copied but disabled via feature flags:**
- Meilisearch: copy config + hook; gate behind `NEXT_PUBLIC_MEILI_ENABLED=false`
- Crisp chat: copy SDK integration; gate behind `NEXT_PUBLIC_CRISP_ENABLED=false`

**Not copied:** GraphQL/Apollo, Pusher, Redux slices for VIP/market/casino/sports, Swiper, game engines

---

## Data Fetching (Frontend)

**API client:** `src/lib/api/client.ts`
- Session requests: `Authorization: Bearer <access_jwt>` + `X-Account-Id: <account_id>`
- Programmatic requests: `X-API-Key` + `X-API-Signature` (HMAC-SHA256)
- Auto-refresh: on 401, attempt `POST /auth/refresh`; if refresh fails, logout
- Uniform error handling: 401 → logout, 4xx → toast, 5xx → error boundary

**SWR hooks:**
```
useWallets(params)                 → GET /v1/wallets?limit&offset
useWallet(id)                      → GET /v1/wallets/:id
useAddresses(walletId, params)     → GET /v1/wallets/:id/addresses?limit&offset
useTransactions(walletId, params)  → GET /v1/wallets/:id/transactions?limit&offset&status&asset
useTransaction(walletId, txId)     → GET /v1/wallets/:id/transactions/:txId
useUnspents(walletId, params)      → GET /v1/wallets/:id/unspents?limit&offset
useWalletUsers(walletId)           → GET /v1/wallets/:id/users
useWhitelist(walletId, params)     → GET /v1/wallets/:id/whitelist?limit&offset
useWebhooks(walletId)              → GET /v1/wallets/:id/webhooks
useWithdrawal(id)                  → GET /v1/withdrawals/:id
useAccount(id)                     → GET /v1/accounts/:id
useAccountUsers(id, params)        → GET /v1/accounts/:id/users?limit&offset
useAccessTokens(accountId, params) → GET /v1/accounts/:id/tokens?limit&offset
useMyAccounts(params)              → GET /v1/user/me/accounts?limit&offset
useMe()                            → GET /v1/user/me
```

**Redux slices (minimal):**
- `auth` — user, access JWT, current account id, token expiry
- `ui` — dismissible banners, feature flags (Meilisearch, Crisp)

---

## Swagger / OpenAPI Documentation

The project already uses `swaggo/swag v1.8.12` with a generated spec at `docs/swagger.json`. Currently only 4 endpoints have annotations. All new and existing endpoints must have complete Swagger annotations before the implementation of each route is considered done.

### Annotation Requirements

Every route must have:
- `@Summary` and `@Description`
- `@Tags` (group by resource: Auth, Users, Accounts, Wallets, Addresses, Transactions, Withdrawals, Webhooks, Whitelist)
- `@Security` (`ApiKeyAuth` for HMAC routes, `BearerAuth` for session JWT routes)
- `@Param` for all path params, query params, and request bodies
- `@Success` with response schema
- `@Failure` for all expected error codes (400, 401, 403, 404, 409, 500)
- `@Router`

### Security Definitions (add to `main.go` global annotation)

```go
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description "Bearer <jwt>"

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key

// @securityDefinitions.apikey SignatureAuth
// @in header
// @name X-API-Signature
```

### New Tags to Add

| Tag | Routes |
|-----|--------|
| Auth | `/auth/*` |
| Users | `/v1/user/me/*` |
| Accounts | `/v1/accounts/*` |
| Wallets | `/v1/wallets/*` (existing + new) |
| Addresses | `/v1/wallets/:id/addresses`, `/v1/addresses/:address` |
| Transactions | `/v1/wallets/:id/transactions/*` |
| Withdrawals | `/v1/wallets/:id/withdrawals`, `/v1/withdrawals/:id` |
| Webhooks | `/v1/wallets/:id/webhooks/*` |
| Whitelist | `/v1/wallets/:id/whitelist/*` |

### Generating Docs

```bash
make swagger-generate
# equivalent to:
go run github.com/swaggo/swag/cmd/swag@v1.8.12 init -g main.go --output ./docs
```

The generated `docs/swagger.json` and `docs/swagger.yaml` are committed to the repository. Swagger UI is served at `/swagger/index.html` (existing, no change needed).

### Implementation Rule

Each route implementation task is not complete until:
1. The Goravel controller method has full Swagger annotations
2. `make swagger-generate` runs without errors
3. The endpoint appears correctly in Swagger UI

---

## Testing Strategy

### Frontend — Unit & Component Tests (Vitest)

Co-located `*.test.tsx` files alongside components and hooks.

**What to test:**
- All SWR hooks — mock `fetch`, assert correct data shape and error states
- `useAuth` — login, logout, token refresh, 2FA flow, account switching
- API client — HMAC signature generation, JWT attach, auto-refresh on 401, error handling
- UI components with logic — wallet table row, asset balance display, role badge, portfolio % bar
- Form validation — login, create access token, withdraw modal

**What NOT to test:** Pure layout/styling, HeroUI primitives

### Frontend — E2E Tests (Playwright)

Located in `front/e2e/`. Run against local stack (vinext dev + backend).
Config: `playwright.config.ts` — Chromium + Mobile Chrome (mobile-first), screenshot on failure.

| Suite | Key Scenarios |
|-------|---------------|
| Auth | Login success, wrong password error, 2FA flow, recovery code fallback, password recovery |
| Assets — By Wallets | Page loads, search filters, Create Wallet modal open/submit |
| Assets — By Assets | Toggle view, portfolio bars render, drill-down navigation |
| Wallet Detail | All tabs navigate, inline name edit |
| Deposit / Withdraw | Modal opens at correct URL, form validates, close returns to wallet |
| Addresses | Generate Address modal, address appears in list |
| Transactions | List loads with filters, click navigates to detail |
| Account Settings | All tabs, token create/remove, user add/remove |
| User Profile | Edit name, change password, 2FA setup + recovery codes |
| Account Switcher | Dropdown shows accounts, switching updates context |
| **Multi-tenant isolation** | User A cannot see User B's account wallets; auditor on Account A is denied Account B's data |

### Backend — Unit Tests (Go / testify)

Existing pattern: co-located `*_test.go` using `testify/assert` + `testify/mock`. Coverage target: 80%+.

**New test files:**

```
app/http/controllers/
  auth_controller_test.go         → login, 2FA, recovery, refresh, logout
  account_controller_test.go      → CRUD, freeze, archive, user management
  user_controller_test.go         → profile, password change, account list
  wallet_settings_test.go         → freeze, archive, fee settings (UTXO-only gate)
  withdrawal_controller_test.go   → create + status lookup

app/services/
  auth/service_test.go            → password hash, JWT generation/refresh, TOTP verify, recovery codes
  account/service_test.go         → multitenant scoping, role checks, soft-delete re-add
  whitelist/service_test.go       → add/remove/list
  withdrawal/service_test.go      → create withdrawal record + transaction

app/http/middleware/
  session_auth_test.go            → JWT validation, expiry, revoked refresh token
  rbac_test.go                    → role-based access per endpoint
  account_context_test.go         → JWT path (X-Account-Id), HMAC path (access_tokens lookup)
  chain_type_test.go              → UTXO-only endpoint returns 404 for EVM/SOL wallets
```

**Integration tests** (`tests/`): extend `testdb.go` helpers to seed multitenant data — accounts, account_users, wallet_users — for realistic scenarios including cross-account isolation assertions.

---

## Goravel Mail (Password Recovery & Invitations)

Mail uses `facades.Mail()` with Mailable classes generated via `go run . artisan make:mail [Name]`.

**Mailable classes to create:**

| Class | Trigger | Content |
|-------|---------|---------|
| `PasswordResetMail` | `POST /auth/recover` | Reset link with token |
| `WelcomeMail` | User registration | Welcome + login link |
| `UserInviteMail` | Add user to account | Invite link |

**Usage pattern:**
```go
facades.Mail().Queue(mails.NewPasswordResetMail(user, resetToken))
```

Emails are queued (not sent inline) to avoid blocking the HTTP response.

**Config:** `config/mail.go` — SMTP credentials, default sender. All emails respect `MAIL_FROM_ADDRESS` and `MAIL_FROM_NAME`. Templates live in `resources/views/mail/`.

## Goravel Encryption (TOTP Secrets)

Sensitive fields are encrypted at rest using `facades.Crypt()` (AES-256-GCM, keyed by `APP_KEY`):

- `users.totp_secret` — encrypted before insert, decrypted on read
- `totp_recovery_codes.code_hash` — bcrypt hashed (not encrypted; one-way)
- `refresh_tokens.token_hash` — bcrypt hashed
- `password_reset_tokens.token_hash` — bcrypt hashed
- `access_tokens.token_hash` — bcrypt hashed

**Usage:**
```go
// Store
encrypted, err := facades.Crypt().EncryptString(totpSecret)

// Read
secret, err := facades.Crypt().DecryptString(user.TotpSecret)
```

Generate app key: `./artisan key:generate` (sets `APP_KEY` in `.env`).

## Environment Variables

```bash
# Frontend
NEXT_PUBLIC_API_URL=https://api.vault.dev
NEXT_PUBLIC_CRISP_ENABLED=false
NEXT_PUBLIC_CRISP_ID=
NEXT_PUBLIC_MEILI_ENABLED=false
NEXT_PUBLIC_MEILI_HOST=
NEXT_PUBLIC_MEILI_API_KEY=

# Backend — existing
DATABASE_URL=
REDIS_URL=
MASTER_KEY_REF=
ETH_RPC_URL=
POLYGON_RPC_URL=
SOLANA_RPC_URL=
BTC_RPC_URL=

# Backend — new
APP_KEY=                        # generate with: ./artisan key:generate (AES-256 for facades.Crypt)
JWT_SECRET=                     # generate with: ./artisan jwt:secret
JWT_TTL=15                      # access token TTL in minutes (config/jwt.go)
JWT_REFRESH_TTL=43200           # refresh token TTL in minutes = 30 days (config/jwt.go)
TOTP_ISSUER=Vault

# Mail (config/mail.go — Goravel mail facade)
MAIL_MAILER=smtp
MAIL_HOST=
MAIL_PORT=587
MAIL_USERNAME=
MAIL_PASSWORD=
MAIL_ENCRYPTION=tls
MAIL_FROM_ADDRESS=noreply@vault.dev
MAIL_FROM_NAME=Vault
```

---

## Mobile-First Notes

- All tables collapse to card stacks on mobile
- Modals become full-screen bottom sheets on mobile
- Sidebar becomes a hamburger drawer on mobile
- Deposit/Withdraw buttons collapse to icon-only on small screens
- Breadcrumb truncates to last 2 segments on mobile
- Fee rate fields (UTXO only) and required_approvals (display only, enforcement reserved for future multi-sig workflow) are hidden on EVM/Solana wallet settings
