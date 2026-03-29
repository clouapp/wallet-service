# Frontend Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement all dashboard pages and modal routes for the Vault admin panel — Assets, Wallet Detail (all tabs), Transaction Detail, Account Settings, User Profile, Account Switcher, and all URL-addressable modals. This builds on the Foundation plan (Plan 3) which provides the design system, Redux store, API client, and DashboardLayout shell.

**Architecture:** Pages Router (same as cloubet/front). Every modal has its own URL route. Wallet tabs are query-param driven (`?tab=overview`). Data fetching via SWR hooks. Mobile-first HeroUI components. All components co-located with `*.test.tsx` unit tests. E2E suite in `front/e2e/`.

**Prerequisite:** Frontend Foundation plan (Plan 3) must be complete — design system, DashboardLayout, Redux store, API client, and auth pages must be in place.

**Source for copying:** `/Users/raphaelcangucu/projects/cloubet/front/src/`

**Spec:** `docs/superpowers/specs/2026-03-24-admin-panel-design.md`

---

## File Map

```
front/src/
├── pages/
│   └── dashboard/
│       ├── index.tsx                          (redirect → /dashboard/assets)
│       ├── assets/
│       │   ├── index.tsx                      (Assets page: by wallets / by assets toggle)
│       │   ├── create.tsx                     (Create Wallet modal — static segment)
│       │   └── [chain].tsx                    (Asset drill-down: filtered wallet list)
│       ├── wallets/
│       │   └── [id]/
│       │       ├── index.tsx                  (Wallet detail — tab shell)
│       │       ├── deposit.tsx                (Deposit modal)
│       │       ├── withdraw.tsx               (Withdraw modal)
│       │       ├── addresses/
│       │       │   └── generate.tsx           (Generate address modal)
│       │       ├── users/
│       │       │   └── add.tsx                (Add wallet user modal)
│       │       ├── whitelist/
│       │       │   ├── add.tsx                (Add whitelist entry modal)
│       │       │   └── upload.tsx             (CSV upload modal)
│       │       ├── settings/
│       │       │   ├── password.tsx           (Update wallet password modal)
│       │       │   ├── freeze.tsx             (Freeze wallet modal)
│       │       │   └── webhooks/
│       │       │       └── add.tsx            (Add webhook modal)
│       │       └── transactions/
│       │           └── [txId].tsx             (Transaction detail page)
│       ├── accounts/
│       │   ├── index.tsx                      (Accounts list)
│       │   └── create.tsx                     (Create account modal — static segment)
│       ├── settings/
│       │   ├── index.tsx                      (Account Settings — General tab default)
│       │   ├── tokens/
│       │   │   └── create.tsx                 (Create access token modal)
│       │   └── users/
│       │       └── add.tsx                    (Add account user modal)
│       └── profile/
│           ├── index.tsx                      (User profile overview)
│           ├── edit.tsx                       (Edit name/email)
│           ├── password.tsx                   (Change password)
│           └── 2fa.tsx                        (Enable/manage 2FA + recovery codes)
│
├── components/
│   ├── Assets/
│   │   ├── AssetsByWallets.tsx                (table: wallet name, balance, chain, role, Deposit/Withdraw)
│   │   ├── AssetsByAssets.tsx                 (table: icon, balance, price, portfolio % bar, Deposit/Withdraw)
│   │   ├── AssetRow.tsx                       (single row used by both views)
│   │   ├── PortfolioBar.tsx                   (% bar component with tooltip)
│   │   └── ViewToggle.tsx                     (By Wallets / By Assets toggle)
│   ├── Wallets/
│   │   ├── WalletHeader.tsx                   (wallet name inline-edit, ID copy, Show More)
│   │   ├── WalletTabs.tsx                     (tab bar: Overview, Transactions, Unspents, Addresses, Users, Whitelist, Settings)
│   │   ├── tabs/
│   │   │   ├── OverviewTab.tsx                (balances table + transaction history)
│   │   │   ├── TransactionsTab.tsx            (full filterable list)
│   │   │   ├── UnspentsTab.tsx                (UTXO list + Consolidate — UTXO chains only)
│   │   │   ├── AddressesTab.tsx               (address list + Generate Address CTA)
│   │   │   ├── UsersTab.tsx                   (wallet users + Add User CTA)
│   │   │   ├── WhitelistTab.tsx               (whitelist entries + Add + CSV Upload CTAs)
│   │   │   └── SettingsTab.tsx                (name, password, fee rates, freeze, archive, webhooks)
│   │   ├── BalancesTable.tsx                  (expandable token rows, Deposit/Withdraw per token)
│   │   ├── TransactionRow.tsx                 (status badge, amount, from/to, date)
│   │   ├── TransactionFilters.tsx             (status, asset, type, date range)
│   │   ├── UnspentRow.tsx                     (status, amount, address, unspent ID)
│   │   ├── AddressRow.tsx                     (label, address, qty, total received/sent)
│   │   ├── WalletUserRow.tsx                  (avatar initials, name/email, role badges, status)
│   │   ├── WhitelistRow.tsx                   (label, address, delete)
│   │   └── WebhookRow.tsx                     (URL, events, test button, delete)
│   ├── Transactions/
│   │   ├── TransactionDetail.tsx              (two-column: details card + security timeline)
│   │   ├── DetailCard.tsx                     (status, IDs, from/to, amount, note)
│   │   └── SecurityTimeline.tsx               (approval/signature events)
│   ├── Accounts/
│   │   ├── AccountRow.tsx                     (name, ID, role, status, actions)
│   │   └── AccountSwitcher.tsx                (dropdown: accounts list, active checkmark, Create New, Enroll)
│   ├── Settings/
│   │   ├── AccountSettingsTabs.tsx            (General / Developer Options / Users)
│   │   ├── GeneralTab.tsx                     (name, ID, role, archive, freeze, view-all-wallets)
│   │   ├── DeveloperTab.tsx                   (access tokens list)
│   │   ├── UsersSettingsTab.tsx               (account users list)
│   │   ├── AccessTokenRow.tsx                 (name, valid until, permissions, delete)
│   │   └── AccountUserRow.tsx                 (avatar, name/email, role, status, remove)
│   ├── Profile/
│   │   ├── ProfileOverview.tsx                (name, email, 2FA status, recovery code count)
│   │   ├── EditProfileForm.tsx                (name + email form)
│   │   ├── ChangePasswordForm.tsx             (current, new, confirm)
│   │   └── TwoFactorSetup.tsx                 (QR code, confirm TOTP, display recovery codes)
│   └── Modals/
│       ├── DepositModal.tsx                   (QR code, deposit address, chain selector)
│       ├── WithdrawModal.tsx                  (amount, destination, note, fee estimate)
│       ├── CreateWalletModal.tsx              (chain selector, name, type)
│       ├── GenerateAddressModal.tsx           (label field, generate button, result)
│       ├── AddWalletUserModal.tsx             (email lookup, role selector)
│       ├── AddWhitelistModal.tsx              (label, address fields)
│       ├── CsvUploadModal.tsx                 (file drop zone, preview, submit)
│       ├── WalletPasswordModal.tsx            (current password, new password)
│       ├── FreezeWalletModal.tsx              (duration picker, confirm)
│       ├── AddWebhookModal.tsx                (URL, event checkboxes)
│       ├── CreateAccountModal.tsx             (account name field)
│       ├── CreateAccessTokenModal.tsx         (name, valid until, permissions, IP/CIDR, spending limits)
│       └── AddAccountUserModal.tsx            (email lookup, role selector)
│
├── hooks/
│   ├── useWallets.ts
│   ├── useWallet.ts
│   ├── useAddresses.ts
│   ├── useTransactions.ts
│   ├── useTransaction.ts
│   ├── useUnspents.ts
│   ├── useWalletUsers.ts
│   ├── useWhitelist.ts
│   ├── useWebhooks.ts
│   ├── useWithdrawals.ts
│   ├── useWithdrawal.ts
│   ├── useAccount.ts
│   ├── useAccountUsers.ts
│   ├── useAccessTokens.ts
│   ├── useMyAccounts.ts
│   └── useMe.ts
│
└── e2e/
    ├── auth.spec.ts                           (login, wrong password, 2FA, recovery)
    ├── assets.spec.ts                         (by wallets/assets toggle, create wallet)
    ├── wallet-detail.spec.ts                  (tabs, inline edit)
    ├── deposit-withdraw.spec.ts               (modal URL, form, close)
    ├── addresses.spec.ts                      (generate address modal)
    ├── transactions.spec.ts                   (list, filters, detail nav)
    ├── account-settings.spec.ts               (all tabs, token create/remove, user add/remove)
    ├── profile.spec.ts                        (edit name, change password, 2FA setup)
    ├── account-switcher.spec.ts               (dropdown, switch, update context)
    └── multitenant-isolation.spec.ts          (cross-account access denied)
```

---

## Step 1 — SWR Data Fetching Hooks

- [ ] Create `src/hooks/useWallets.ts` — `GET /v1/wallets?limit&offset`; accepts `{ limit, offset, accountId }` params; returns `{ wallets, total, isLoading, error }`
- [ ] Create `src/hooks/useWallet.ts` — `GET /v1/wallets/:id`; returns `{ wallet, isLoading, error, mutate }`
- [ ] Create `src/hooks/useAddresses.ts` — `GET /v1/wallets/:id/addresses?limit&offset`; returns `{ addresses, total, isLoading, error, mutate }`
- [ ] Create `src/hooks/useTransactions.ts` — `GET /v1/wallets/:id/transactions?limit&offset&status&asset&type`; returns `{ transactions, total, isLoading, error }`
- [ ] Create `src/hooks/useTransaction.ts` — `GET /v1/wallets/:id/transactions/:txId`; returns `{ transaction, isLoading, error }`
- [ ] Create `src/hooks/useUnspents.ts` — `GET /v1/wallets/:id/unspents?limit&offset`; only called when wallet chain is BTC/LTC/DOGE; returns `{ unspents, total, isLoading, error }`
- [ ] Create `src/hooks/useWalletUsers.ts` — `GET /v1/wallets/:id/users`; returns `{ users, isLoading, error, mutate }`
- [ ] Create `src/hooks/useWhitelist.ts` — `GET /v1/wallets/:id/whitelist?limit&offset`; returns `{ entries, total, isLoading, error, mutate }`
- [ ] Create `src/hooks/useWebhooks.ts` — `GET /v1/wallets/:id/webhooks`; returns `{ webhooks, isLoading, error, mutate }`
- [ ] Create `src/hooks/useWithdrawals.ts` — `GET /v1/wallets/:id/withdrawals?limit&offset`; accepts `{ walletId, limit, offset }` params; returns `{ withdrawals, total, isLoading, error }`
- [ ] Create `src/hooks/useWithdrawal.ts` — `GET /v1/withdrawals/:id`; returns `{ withdrawal, isLoading, error }`
- [ ] Create `src/hooks/useAccount.ts` — `GET /v1/accounts/:id`; returns `{ account, isLoading, error, mutate }`
- [ ] Create `src/hooks/useAccountUsers.ts` — `GET /v1/accounts/:id/users?limit&offset`; returns `{ users, total, isLoading, error, mutate }`
- [ ] Create `src/hooks/useAccessTokens.ts` — `GET /v1/accounts/:id/tokens?limit&offset`; returns `{ tokens, total, isLoading, error, mutate }`
- [ ] Create `src/hooks/useMyAccounts.ts` — `GET /v1/user/me/accounts?limit&offset`; returns `{ accounts, total, isLoading, error }`
- [ ] Create `src/hooks/useMe.ts` — `GET /v1/user/me`; returns `{ user, isLoading, error, mutate }`
- [ ] Write unit tests for all hooks: `src/hooks/*.test.ts` — mock `fetch`, assert data shape, loading state, error state, and SWR revalidation on mutate

---

## Step 2 — Assets Page

### AssetsByWallets and AssetsByAssets components

- [ ] Create `src/components/Assets/ViewToggle.tsx` — HeroUI Tab or ButtonGroup: "By Wallets" / "By Assets"; drives `?view=wallets|assets` query param
- [ ] Create `src/components/Assets/AssetRow.tsx` — shared row: chain icon, name, balance (USD + native), chain badge; Deposit and Withdraw action buttons (navigate to `/dashboard/wallets/[id]/deposit` and `/dashboard/wallets/[id]/withdraw`)
- [ ] Create `src/components/Assets/PortfolioBar.tsx` — percentage bar with HeroUI Progress or custom div; shows `%` of total USD portfolio; shows tooltip on hover
- [ ] Create `src/components/Assets/AssetsByWallets.tsx` — table columns: Wallet Name, Balance (USD), Chain, Role, Actions; uses `useWallets` hook; supports search/filter; infinite scroll or pagination
- [ ] Create `src/components/Assets/AssetsByAssets.tsx` — grouped by chain: Asset icon, Total Balance (USD), Price (static or placeholder), Portfolio %, Actions; uses `useWallets` aggregated; groups wallets by chain and sums balances; shows `PortfolioBar` per row
- [ ] Write unit tests: `AssetsByWallets.test.tsx`, `AssetsByAssets.test.tsx`, `PortfolioBar.test.tsx` — mock hook return, assert rows render, assert Deposit/Withdraw links point to correct wallet URLs

### Assets page route

- [ ] Create `src/pages/dashboard/assets/index.tsx` — getLayout returns `DashboardLayout`; reads `?view` query param; defaults to `wallets`; renders `ViewToggle` + conditional `AssetsByWallets` or `AssetsByAssets`; shows total USD header (sum of all wallet balances)

---

## Step 3 — Create Wallet Modal

- [ ] Create `src/components/Modals/CreateWalletModal.tsx` — HeroUI Modal (full-screen on mobile); fields: Chain selector (dropdown with icons: BTC, ETH, LTC, DOGE, SOL, etc.), Wallet Name (text), Type (custodial — fixed); Submit calls `POST /v1/wallets`; on success calls `mutate` on `useWallets` and navigates to new wallet detail URL; close navigates to `router.back()`
- [ ] Create `src/pages/dashboard/assets/create.tsx` — renders parent `<AssetsPage />` in background (import and render with `?view=wallets`) + overlays `<CreateWalletModal />`; getLayout returns `DashboardLayout`; this static segment takes precedence over `[chain].tsx`

---

## Step 4 — Asset Drill-Down

- [ ] Create `src/pages/dashboard/assets/[chain].tsx` — getLayout `DashboardLayout`; reads `chain` from `router.query`; renders breadcrumb "Assets > [CHAIN]"; uses `useWallets` filtered by chain; shows `AssetsByWallets` with chain pre-filtered; total balance header for that chain only

---

## Step 5 — Wallet Detail Shell

- [ ] Create `src/components/Wallets/WalletHeader.tsx` — wallet name with inline-edit (click name → input field, blur/Enter saves via `PUT /v1/wallets/:id`); wallet ID with `CopyButton`; "Show More" accordion (label, status badge, chain, created date); freeze/archive status badge if frozen
- [ ] Create `src/components/Wallets/WalletTabs.tsx` — HeroUI Tabs component; tab selection drives `?tab=` query param with these canonical key names: `overview | transactions | unspents | addresses | users | whitelist | settings`; UTXO chains (BTC/LTC/DOGE) render **7 tabs**: Overview, Transactions, Unspents, Addresses, Users, Whitelist, Settings — both Transactions AND Unspents are present simultaneously; non-UTXO chains (ETH, SOL, etc.) render **6 tabs**: Unspents tab is omitted entirely (do NOT rename Transactions to Unspents)
- [ ] Create `src/pages/dashboard/wallets/[id]/index.tsx` — getLayout `DashboardLayout`; calls `useWallet(id)`; renders `WalletHeader` + `WalletTabs`; reads `?tab` param (default `overview`); renders the correct tab component based on param; handles 404 if wallet not found

---

## Step 6 — Wallet Tabs

### Overview tab

- [ ] Create `src/components/Wallets/BalancesTable.tsx` — rows: native token + any sub-tokens; columns: Asset, Balance (native), Balance (USD), Actions (Deposit/Withdraw links); tokens are collapsible per wallet; Deposit navigates to `/dashboard/wallets/[id]/deposit`, Withdraw to `/dashboard/wallets/[id]/withdraw`
- [ ] Create `src/components/Wallets/TransactionRow.tsx` — status badge (confirmed/pending/failed with color), amount (+ or -), asset, from/to address (truncated + copy), date; click navigates to `/dashboard/wallets/[id]/transactions/[txId]`
- [ ] Create `src/components/Wallets/TransactionFilters.tsx` — HeroUI Select inputs: status, asset/chain, type (send/receive); date range picker; resets on clear; drives query params for `useTransactions`
- [ ] Create `src/components/Wallets/tabs/OverviewTab.tsx` — `BalancesTable` at top; "Transaction History" section below with `TransactionFilters` + list of `TransactionRow`; uses `useTransactions(walletId, filters)`; load more pagination

### Transactions tab

- [ ] Create `src/components/Wallets/tabs/TransactionsTab.tsx` — same as Overview's transaction history section but full-page with more columns; uses `useTransactions`; `TransactionFilters` at top; paginated `TransactionRow` list

### Unspents tab (UTXO only)

- [ ] Create `src/components/Wallets/UnspentRow.tsx` — columns: status badge, amount (sats), address (truncated), unspent ID (copy button)
- [ ] Create `src/components/Wallets/tabs/UnspentsTab.tsx` — only rendered for BTC/LTC/DOGE wallets (guard by `wallet.chain`); `useUnspents` hook; list of `UnspentRow`; "Consolidate" button at top right → calls `POST /v1/wallets/:id/consolidate` with confirmation dialog; only shown when `unspents.length > 1`

### Addresses tab

- [ ] Create `src/components/Wallets/AddressRow.tsx` — columns: label+timestamp, address (truncated + copy), quantity, total received, total sent
- [ ] Create `src/components/Wallets/tabs/AddressesTab.tsx` — uses `useAddresses`; list of `AddressRow`; "Generate Address" CTA button → navigates to `/dashboard/wallets/[id]/addresses/generate`; pagination

### Users tab

- [ ] Create `src/components/Wallets/WalletUserRow.tsx` — avatar initials (first letter of name, colored by hash), name, email, role badges (multi-role — TEXT[] from DB: admin/spend/view), status badge; "Remove" action → calls `DELETE /v1/wallets/:id/users/:userId` with confirmation
- [ ] Create `src/components/Wallets/tabs/UsersTab.tsx` — uses `useWalletUsers`; list of `WalletUserRow`; "Add User" CTA → navigates to `/dashboard/wallets/[id]/users/add`

### Whitelist tab

- [ ] Create `src/components/Wallets/WhitelistRow.tsx` — label, address (full + copy), delete button → calls `DELETE /v1/wallets/:id/whitelist/:eid` with confirmation
- [ ] Create `src/components/Wallets/tabs/WhitelistTab.tsx` — uses `useWhitelist`; list of `WhitelistRow`; empty state illustration; "Add Entry" CTA → `/dashboard/wallets/[id]/whitelist/add`; "Upload CSV" CTA → `/dashboard/wallets/[id]/whitelist/upload`; pagination

### Settings tab

- [ ] Create `src/components/Wallets/WebhookRow.tsx` — URL, event badges, "Test" button (calls `POST /v1/wallets/:id/webhooks/:wid/test`), "Delete" button with confirmation
- [ ] Create `src/components/Wallets/tabs/SettingsTab.tsx` — sections:
  - Name: text input + save (PUT)
  - Wallet ID: read-only + copy
  - Password: "Update Password" → navigates to `/dashboard/wallets/[id]/settings/password`
  - Fee Settings (UTXO-only, hidden for EVM/SOL): min fee rate, max fee rate, fee multiplier inputs; save via PUT
  - Required Approvals: read-only display "1 (future feature)"
  - Bulk Withdrawal: "Bulk Withdrawal" button (placeholder UI — shows "Coming soon" modal or disabled state; the underlying `POST /v1/wallets/:id/withdrawals` endpoint supports single withdrawals; bulk is a future workflow; render a disabled button with tooltip in this plan)
  - Danger Zone: "Freeze Wallet" → `/dashboard/wallets/[id]/settings/freeze`; "Archive Wallet" → confirm dialog → `POST /v1/wallets/:id/archive`
  - Webhooks: `useWebhooks` list of `WebhookRow`; "Add Webhook" → `/dashboard/wallets/[id]/settings/webhooks/add`

---

## Step 7 — Wallet Modal Pages

- [ ] Create `src/components/Modals/DepositModal.tsx` — QR code (use `qrcode.react` or similar); deposit address (copy button); chain selector if wallet has multiple deposit addresses; close → `router.back()`; **data source for deposit address:** call `useAddresses(walletId, { limit: 1 })` to fetch the first existing address; if the list is empty, show a "Generate Address" button that calls `POST /v1/wallets/:id/addresses` inline and then displays the result — do not silently fail
- [ ] Create `src/pages/dashboard/wallets/[id]/deposit.tsx` — renders wallet detail in background + `<DepositModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/WithdrawModal.tsx` — fields: Amount (with max button), Destination Address, Note (optional); fee estimate display (calls fee estimate or shows placeholder); Submit → `POST /v1/wallets/:id/withdrawals`; success → navigate to withdrawal status or close; validation: amount > 0, valid address format
- [ ] Create `src/pages/dashboard/wallets/[id]/withdraw.tsx` — background + `<WithdrawModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/GenerateAddressModal.tsx` — label field (optional); Generate button → `POST /v1/wallets/:id/addresses`; result: shows generated address with QR + copy; calls `mutate` on `useAddresses`; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/addresses/generate.tsx` — background + `<GenerateAddressModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/AddWalletUserModal.tsx` — email input (lookup existing users or invite); role checkboxes (multi-role: admin, spend, view); Submit → `POST /v1/wallets/:id/users`; calls `mutate` on `useWalletUsers`; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/users/add.tsx` — background + `<AddWalletUserModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/AddWhitelistModal.tsx` — label (optional), address (required); Submit → `POST /v1/wallets/:id/whitelist`; calls `mutate` on `useWhitelist`; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/whitelist/add.tsx` — background + `<AddWhitelistModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/CsvUploadModal.tsx` — drag-and-drop file zone (HeroUI or custom); parses CSV client-side (label,address columns); preview table of entries to be added; Submit → iterates `POST /v1/wallets/:id/whitelist` per row or batch endpoint; progress indicator; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/whitelist/upload.tsx` — background + `<CsvUploadModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/WalletPasswordModal.tsx` — current password, new password, confirm new password; Submit → `PUT /v1/wallets/:id` with password fields; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/settings/password.tsx` — background + `<WalletPasswordModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/FreezeWalletModal.tsx` — duration options (1h, 24h, 7d, 30d, indefinite); reason note (optional); Submit → `POST /v1/wallets/:id/freeze`; success → calls `mutate` on `useWallet`; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/settings/freeze.tsx` — background + `<FreezeWalletModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/AddWebhookModal.tsx` — URL input; event checkboxes (transaction.confirmed, transaction.pending, withdrawal.created, etc.); Submit → `POST /v1/wallets/:id/webhooks`; calls `mutate` on `useWebhooks`; close → `router.back()`
- [ ] Create `src/pages/dashboard/wallets/[id]/settings/webhooks/add.tsx` — background + `<AddWebhookModal />`; getLayout `DashboardLayout`

---

## Step 8 — Transaction Detail Page

- [ ] Create `src/components/Transactions/DetailCard.tsx` — two columns (mobile: stacked): Status badge, Transaction ID (copy), Wallet ID (copy + link), From address (copy), To address (copy), Amount (native + USD), Fee, Note, Created At, Confirmed At
- [ ] Create `src/components/Transactions/SecurityTimeline.tsx` — vertical timeline of approval/signature events; each event: icon, actor (user or system), action, timestamp; sourced from `transaction.approvals` or `transaction.timeline` field (placeholder if backend doesn't return yet)
- [ ] Create `src/components/Transactions/TransactionDetail.tsx` — responsive two-column layout (desktop) or stacked (mobile): `DetailCard` + `SecurityTimeline`
- [ ] Create `src/pages/dashboard/wallets/[id]/transactions/[txId].tsx` — getLayout `DashboardLayout`; calls `useTransaction(walletId, txId)`; renders `TransactionDetail`; breadcrumb: "Assets > [Wallet Name] > Transactions > [txId short]"; back button

---

## Step 9 — Accounts List

- [ ] Create `src/components/Accounts/AccountRow.tsx` — columns: Account Name, Account ID (truncated + copy), Role, Status badge, Created At; click navigates to `/dashboard/settings?accountId=:id` (account switcher switches then goes to settings) or just switches account
- [ ] Create `src/pages/dashboard/accounts/index.tsx` — getLayout `DashboardLayout`; uses `useMyAccounts`; paginated list of `AccountRow`; "Create Account" CTA → `/dashboard/accounts/create`; empty state

- [ ] Create `src/components/Modals/CreateAccountModal.tsx` — Account Name field; Submit → `POST /v1/accounts`; on success calls `mutate` on `useMyAccounts`; switches active account to new one via Redux dispatch; close → `router.back()`
- [ ] Create `src/pages/dashboard/accounts/create.tsx` — renders `<AccountsPage />` in background + `<CreateAccountModal />`; getLayout `DashboardLayout`

---

## Step 10 — Account Settings Page

### Tabs shell

- [ ] Create `src/components/Settings/AccountSettingsTabs.tsx` — HeroUI Tabs: General, Developer Options, Users; drives `?tab=` query param

### General tab

- [ ] Create `src/components/Settings/GeneralTab.tsx` — sections:
  - Account Name: input + save (PUT `/v1/accounts/:id`)
  - Account ID: read-only + copy
  - Role: badge showing current user's role (from `useAccount` or Redux)
  - View All Wallets: toggle (PUT with `view_all_wallets`)
  - Freeze Account: button → confirm dialog → `POST /v1/accounts/:id/freeze`
  - Archive Account: danger zone button → confirm dialog → `POST /v1/accounts/:id/archive`

### Developer Options tab

- [ ] Create `src/components/Settings/AccessTokenRow.tsx` — name, valid until (or "Never"), permissions list (truncated), IP/CIDR, delete button → confirm → `DELETE /v1/accounts/:id/tokens/:tokenId`; no edit (delete + recreate pattern)
- [ ] Create `src/components/Settings/DeveloperTab.tsx` — uses `useAccessTokens(accountId)`; list of `AccessTokenRow`; "Create Access Token" CTA → `/dashboard/settings/tokens/create`; pagination; note: token value only shown once on creation

### Users settings tab

- [ ] Create `src/components/Settings/AccountUserRow.tsx` — avatar initials, name, email, role badge (single role: owner/admin/auditor/user), status badge, "Remove" → confirm → `DELETE /v1/accounts/:id/users/:userId`; can't remove self if owner
- [ ] Create `src/components/Settings/UsersSettingsTab.tsx` — uses `useAccountUsers(accountId)`; list of `AccountUserRow`; "Add User" CTA → `/dashboard/settings/users/add`; pagination

### Settings page route and modals

- [ ] Create `src/pages/dashboard/settings/index.tsx` — getLayout `DashboardLayout`; reads active accountId from Redux store; calls `useAccount(accountId)`; renders `AccountSettingsTabs` + tab content based on `?tab=` (default `general`)

- [ ] Create `src/components/Modals/CreateAccessTokenModal.tsx` — fields: Name (required), Valid Until (date picker or "Never"), Permissions (checkbox list: wallets:read, wallets:write, transactions:read, withdrawals:write, webhooks:read, webhooks:write), IP/CIDR (optional), Spending Limit (optional JSON input); Submit → `POST /v1/accounts/:id/tokens`; on success: show token value in a "copy it now" modal (one-time reveal) then close to settings
- [ ] Create `src/pages/dashboard/settings/tokens/create.tsx` — renders settings page in background + `<CreateAccessTokenModal />`; getLayout `DashboardLayout`

- [ ] Create `src/components/Modals/AddAccountUserModal.tsx` — email input, role selector (owner/admin/auditor/user); Submit → `POST /v1/accounts/:id/users`; calls `mutate` on `useAccountUsers`; close → `router.back()`
- [ ] Create `src/pages/dashboard/settings/users/add.tsx` — renders settings page in background + `<AddAccountUserModal />`; getLayout `DashboardLayout`

---

## Step 11 — Account Switcher

- [ ] Create `src/components/Accounts/AccountSwitcher.tsx` — HeroUI Dropdown; shows active account name + chevron; dropdown items: list of accounts from `useMyAccounts`; active account has checkmark; "Create New Account" link → `/dashboard/accounts/create`; "Enroll in Custody" link (placeholder / external); clicking an account dispatches `setCurrentAccount(id)` to Redux; the api `client.ts` reads `store.getState().auth.currentAccountId` to attach `X-Account-Id` on every request (already wired in Foundation plan); dispatching triggers SWR global revalidation via `mutate()` with no key (revalidates all); on mobile renders as HeroUI Drawer (bottom sheet) instead of dropdown
- [ ] Integrate `AccountSwitcher` into `Header.tsx` (from Foundation plan): read the file first; insert `<AccountSwitcher />` **after** the logo `<Link>` and **before** the `<WebMenu>` / navigation items `<div>` in the flex row; the Foundation plan's Header already has a placeholder comment `{/* ACCOUNT_SWITCHER_SLOT */}` — if the comment is present, replace it; if absent, insert after the logo link
- [ ] Write unit test: `AccountSwitcher.test.tsx` — mock `useMyAccounts`, assert list renders, assert dispatch called on select, assert mobile bottom sheet vs desktop dropdown based on mocked `useBreakpoints`

---

## Step 12 — User Profile Pages

- [ ] Create `src/components/Profile/ProfileOverview.tsx` — displays: name, email, 2FA status (enabled/disabled badge), recovery codes count (e.g. "8 codes remaining"), links to Edit Profile, Change Password, Manage 2FA
- [ ] Create `src/pages/dashboard/profile/index.tsx` — getLayout `DashboardLayout`; calls `useMe()`; renders `ProfileOverview`

- [ ] Create `src/components/Profile/EditProfileForm.tsx` — Full Name input, Email input; Submit → `PUT /v1/user/me`; calls `mutate` on `useMe()`; updates Redux auth user; success toast
- [ ] Create `src/pages/dashboard/profile/edit.tsx` — follows the modal-over-parent pattern: renders `<ProfileOverviewPage />` in the background + overlays a HeroUI Modal containing `<EditProfileForm />`; close → `router.back()` returns to `/dashboard/profile`; getLayout `DashboardLayout`

- [ ] Create `src/components/Profile/ChangePasswordForm.tsx` — Current Password, New Password, Confirm New Password; client-side validation (min 8 chars, match); Submit → `POST /v1/user/me/password`; on success: auto-logout (all refresh tokens revoked) + redirect to `/login`
- [ ] Create `src/pages/dashboard/profile/password.tsx` — follows the modal-over-parent pattern: renders `<ProfileOverviewPage />` in the background + overlays a HeroUI Modal containing `<ChangePasswordForm />`; close → `router.back()`; getLayout `DashboardLayout`

- [ ] Create `src/components/Profile/TwoFactorSetup.tsx` — three states:
  1. **Not enabled**: shows "Enable 2FA" button → calls `POST /auth/2fa/setup` → shows QR code + manual secret; confirm input for TOTP code → calls `POST /auth/2fa/confirm`; on success: displays recovery codes (one-time reveal, copy all button); shows count going forward
  2. **Enabled**: shows status (enabled), recovery codes count from `GET /auth/2fa/recovery-codes`; "Disable 2FA" button → confirm with TOTP → `DELETE /auth/2fa`
  3. **Confirm step**: between setup and enable — QR shown, waiting for user to enter code
- [ ] Create `src/pages/dashboard/profile/2fa.tsx` — getLayout `DashboardLayout`; calls `useMe()`; renders `TwoFactorSetup` with correct state based on `user.totp_enabled`

---

## Step 13 — Dashboard Redirect and Redirect Guards

- [ ] Create/update `src/pages/dashboard/index.tsx` — `getServerSideProps` redirects to `/dashboard/assets`
- [ ] Add `withAuth` HOC or middleware check in `DashboardLayout.tsx` — if no Redux auth token on mount, redirect to `/login`; if token expired and refresh fails, clear state + redirect
- [ ] Add `withGuest` logic in login pages — if already authenticated, redirect to `/dashboard/assets`

---

## Step 14 — Vitest Unit Tests (Dashboard Components)

- [ ] `AssetsByWallets.test.tsx` — mock `useWallets`, assert table rows render with correct wallet data
- [ ] `AssetsByAssets.test.tsx` — mock `useWallets` aggregated by chain, assert portfolio bar percentage
- [ ] `PortfolioBar.test.tsx` — assert width styles for given percentage values
- [ ] `WalletHeader.test.tsx` — assert inline edit triggers PUT, ID copy button works
- [ ] `WalletTabs.test.tsx` — assert tab navigation updates `?tab=` query param; Unspents tab hidden for ETH wallet
- [ ] `TransactionsTab.test.tsx` — mock `useTransactions`, assert filter changes re-fetch with correct params
- [ ] `UnspentsTab.test.tsx` — mock `useUnspents`, assert Consolidate only shows when unspents > 1
- [ ] `WithdrawModal.test.tsx` — assert amount validation, address validation, submit calls correct endpoint
- [ ] `CreateAccessTokenModal.test.tsx` — assert permissions checkboxes, valid until date picker, submit
- [ ] `AccountSwitcher.test.tsx` — mock `useMyAccounts`, assert dispatch on account select
- [ ] `TwoFactorSetup.test.tsx` — assert three state transitions, recovery codes reveal on confirm
- [ ] `ChangePasswordForm.test.tsx` — assert password match validation, submit triggers logout on success

---

## Step 15 — Playwright E2E Tests

- [ ] Create `front/e2e/auth.spec.ts`:
  - Login success → redirects to `/dashboard/assets`
  - Wrong password → shows error message, stays on login
  - 2FA flow → after login, redirected to `/login/2fa`, enter code → dashboard
  - Recovery code fallback → use recovery code instead of TOTP
  - Password recovery → enter email → success message; confirm link → new password

- [ ] Create `front/e2e/assets.spec.ts`:
  - Assets by wallets view loads, table shows wallets
  - Toggle to "By Assets" view — portfolio bars visible
  - Create Wallet modal: navigate to `/dashboard/assets/create`, URL updates, form submits, new wallet appears
  - Close modal with Escape → returns to `/dashboard/assets`

- [ ] Create `front/e2e/wallet-detail.spec.ts`:
  - Navigate to wallet detail → Overview tab visible
  - Click each tab → URL `?tab=` updates, correct content shows
  - Inline name edit → click name, type new name, press Enter → name updated
  - Unspents tab hidden for ETH wallet, shown for BTC wallet

- [ ] Create `front/e2e/deposit-withdraw.spec.ts`:
  - Click Deposit → URL changes to `/dashboard/wallets/[id]/deposit`; QR code visible
  - Click Withdraw → URL changes to `/dashboard/wallets/[id]/withdraw`; form visible
  - Fill withdraw form with invalid amount → error shown, no submit
  - Close modal → returns to wallet detail URL

- [ ] Create `front/e2e/addresses.spec.ts`:
  - Addresses tab shows address list
  - "Generate Address" → URL `/dashboard/wallets/[id]/addresses/generate` → modal opens
  - Generate → new address appears in result with copy button
  - Close → address list revalidated, new address in list

- [ ] Create `front/e2e/transactions.spec.ts`:
  - Transaction list loads with rows
  - Apply status filter → list updates
  - Click transaction row → navigates to `/dashboard/wallets/[id]/transactions/[txId]`
  - Detail page shows two columns (details + timeline)

- [ ] Create `front/e2e/account-settings.spec.ts`:
  - Account Settings page loads, General tab default
  - Navigate to Developer Options tab → access tokens list
  - Create Access Token modal → `/dashboard/settings/tokens/create` → fill form → submit → token shown once → close
  - Delete token → removed from list
  - Navigate to Users tab → account users list
  - Add User modal → fill email + role → submit → user appears
  - Remove user → removed from list

- [ ] Create `front/e2e/profile.spec.ts`:
  - Profile overview shows name, email, 2FA status
  - Edit Profile → change name → save → overview updated
  - Change Password → enter current + new → submit → logged out, redirected to login
  - 2FA setup → scan QR flow → enter code → recovery codes shown → enabled status

- [ ] Create `front/e2e/accounts.spec.ts`:
  - Accounts list page (`/dashboard/accounts`) loads with account rows
  - "Create Account" button navigates to `/dashboard/accounts/create`
  - Create Account modal opens at correct URL with parent list in background
  - Fill account name → submit → new account appears in list
  - Close modal with Escape → returns to `/dashboard/accounts`

- [ ] Create `front/e2e/account-switcher.spec.ts`:
  - Header shows current account name
  - Click switcher → dropdown shows accounts list
  - Click different account → active checkmark moves, header updates, asset list reloads for new account

- [ ] Create `front/e2e/multitenant-isolation.spec.ts`:
  - User A logs in → sees Account A wallets
  - User A sets `X-Account-Id` to Account B → API returns 403
  - Auditor on Account A attempts to view Account B data → denied
  - User with no account membership → wallet list empty, 403 on account detail

- [ ] Configure `playwright.config.ts` — projects: Chromium (desktop 1280×800) + Mobile Chrome (390×844); screenshot on failure; video on retry; base URL from `PLAYWRIGHT_BASE_URL` env var; test timeout 30s

---

## Step 16 — Type Definitions

> **Execution order note:** These types must be created **before Step 2** (alongside Step 1). All components in Steps 2–15 import from these files. Do not wait until the end.

- [ ] Create `src/types/wallet.ts` — `Wallet`, `WalletUser`, `Address`, `Transaction`, `Unspent`, `Whitelist`, `WhitelistEntry`, `Webhook`, `Withdrawal`
- [ ] Create `src/types/account.ts` — `Account`, `AccountUser`, `AccessToken`
- [ ] Create `src/types/user.ts` — `User`, `RecoveryCodesResponse`
- [ ] Create `src/types/api.ts` — `PaginatedResponse<T>`, `ApiError`, `CreateWalletRequest`, `WithdrawRequest`, `CreateTokenRequest`

---

## Dependency Notes

- **Step 16 (types) and Step 1 (hooks) run first, in parallel** — all other steps depend on both
- **Step 2 before Steps 3 and 4:** Assets components before Create Wallet and Drill-down pages
- **Step 5 before Steps 6 and 7:** wallet detail shell must exist before tabs and modal pages
- **Step 11 before Step 10:** AccountSwitcher must be integrated into Header before Settings page can show active account
- **Steps 8, 9, 10, 12 are independent of each other** — can run in parallel after Step 5 and Step 11 complete

## Mobile-First Notes

- All modals: full-screen on mobile (HeroUI `size="full"` on mobile breakpoint, `size="lg"` on desktop)
- Wallet tabs: HeroUI Tabs with `isVertical={false}`, scrollable on mobile
- Tables: horizontal scroll on mobile with sticky first column
- All forms: `stack` layout on mobile, two-column on desktop
- `TransactionDetail`: stacked on mobile (details then timeline), side-by-side on desktop (`lg:grid-cols-2`)
- `AccountSwitcher`: bottom sheet on mobile (HeroUI Drawer), dropdown on desktop
- Breadcrumbs: truncate to last 2 segments on mobile (hide parent segments, show ellipsis `…`); applies to Transaction Detail and Asset Drill-down breadcrumbs
