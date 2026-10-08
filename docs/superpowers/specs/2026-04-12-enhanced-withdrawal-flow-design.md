# Enhanced Withdrawal Flow — Design Spec

**Date:** 2026-04-12
**Status:** Draft
**Scope:** Backend (Go/Goravel) + Frontend (React/vinext)

---

## 1. Overview

Replace the current single-step withdrawal form with a two-step flow:

1. **Withdrawal Form** — amount, destination address (with real-time per-chain validation), optional note
2. **Confirmation Screen** — summary with estimated network fee, wallet passphrase input, mandatory TOTP 2FA (with inline setup if the user hasn't enabled it yet)

### Key decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Fee estimation | Hybrid — real backend endpoint, graceful frontend degradation | Shows real fees when possible; doesn't block withdrawal if estimation fails |
| Withdrawal auth | Upgrade dashboard endpoint with passphrase + totp_code | Single route, reuses existing MPC decryption logic, adds 2FA security |
| 2FA enforcement | Mandatory for withdrawals; inline setup if not yet enabled | Security requirement — users must have TOTP active before moving funds |
| Address validation | Both client-side (`multicoin-address-validator`) + backend (`blockchain_address` rule) | Instant UX feedback + authoritative server-side check |

---

## 2. User flow

```
┌─────────────────────────────────────────────────────────────┐
│                    STEP 1: Withdrawal Form                  │
│                                                             │
│  ┌─ Wallet Panel ────────────────────────────────────────┐  │
│  │  [TBTC] BTC1                    AVAILABLE 0.00157336  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ⚠ Double-check the address. Blockchain transfers are       │
│    irreversible.                                            │
│                                                             │
│  Amount                                        [Use max]    │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ 0.0                                                 │    │
│  └─────────────────────────────────────────────────────┘    │
│  * Invalid if empty, ≤ 0, or > balance                      │
│                                                             │
│  Destination address                                        │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ tb1... (placeholder varies by chain)                │    │
│  └─────────────────────────────────────────────────────┘    │
│  * Real-time validation via multicoin-address-validator      │
│  * Shows "Invalid {chain} address" on blur if bad           │
│                                                             │
│  Note (optional)                                            │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ Internal reference, ticket ID, etc.                 │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│         [Cancel]                    [Continue →]            │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                 STEP 2: Confirmation Screen                 │
│                                                             │
│  ┌─ Summary ─────────────────────────────────────────────┐  │
│  │  From        BTC1 (wallet label)                      │  │
│  │  To          tb1qr6kzfnw9um0n7c...                    │  │
│  │  Amount      0.001 BTC                                │  │
│  │  Est. Fee    0.00002 BTC  (or "Unavailable")          │  │
│  │  Total       0.00102 BTC  (or just Amount)            │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌─ Authenticate ────────────────────────────────────────┐  │
│  │                                                       │  │
│  │  IF user.totp_enabled:                                │  │
│  │    Wallet Password  [__________________________]      │  │
│  │    2FA Code         [______]                          │  │
│  │                                                       │  │
│  │  IF NOT user.totp_enabled:                            │  │
│  │    ┌─ Enable 2FA ─────────────────────────────────┐   │  │
│  │    │  "2FA is required for withdrawals."          │   │  │
│  │    │  Scan this QR code with your authenticator:  │   │  │
│  │    │  [QR CODE]                                   │   │  │
│  │    │  Manual key: XXXX XXXX XXXX XXXX             │   │  │
│  │    │  Verification code: [______]                 │   │  │
│  │    │  [Verify & Enable 2FA]                       │   │  │
│  │    └──────────────────────────────────────────────┘   │  │
│  │    (once verified, transforms into Password + 2FA)    │  │
│  │                                                       │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│     [← Back]                      [Initiate Withdrawal]     │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. Backend changes

### 3.1 Fee estimation

#### New interface method on `types.Chain`

```go
// pkg/types/types.go
type FeeEstimate struct {
    Fee      string `json:"fee"`
    FeeAsset string `json:"fee_asset"`
    GasPrice string `json:"gas_price,omitempty"`
    GasLimit uint64 `json:"gas_limit,omitempty"`
}

type Chain interface {
    // ... existing methods ...
    EstimateFee(ctx context.Context, req TransferRequest) (*FeeEstimate, error)
}
```

#### Per-chain implementations

**EVM** (`app/services/chain/evm.go`):
- Call `eth_gasPrice` RPC
- Use gas limit 21000 (native) or 65000 (ERC-20 token)
- Return `fee = gasPrice * gasLimit` in wei, `fee_asset = chain native asset`

**Bitcoin** (`app/services/chain/bitcoin.go`):
- Use wallet's `fee_rate_min`/`fee_rate_max` settings, or call a fee rate estimation API
- Estimate fee based on typical transaction size (~250 bytes for simple transfer)
- Return `fee` in satoshis, `fee_asset = "BTC"` (or `"TBTC"` for testnet)

**Solana** (`app/services/chain/solana.go`):
- Return fixed 5000 lamports (standard transaction fee), or call `getFeeForMessage` RPC
- Return `fee` in lamports, `fee_asset = "SOL"`

#### New endpoint

**Route:** `POST /v1/wallets/{walletId}/withdrawals/estimate`
**Middleware:** `SessionAuth`, `AccountHeader`, `WalletContext`
**File:** `app/http/controllers/wallet_withdrawals_controller.go`

**Request:**
```json
{
  "amount": "0.001",
  "destination_address": "tb1q..."
}
```

**Response (success):**
```json
{
  "fee": "0.00002",
  "fee_asset": "BTC",
  "total": "0.00102"
}
```

**Response (failure — non-blocking):**
```json
{
  "error": "fee estimation unavailable",
  "code": "FEE_ESTIMATE_FAILED"
}
```

The frontend treats any error as "fee unavailable" and does not block the withdrawal.

### 3.2 TOTP enrollment endpoints

Currently the auth service has `GenerateTOTP` and `VerifyTOTP` methods but no HTTP endpoints for enrollment. Add two new routes.

**Route:** `POST /v1/auth/2fa/setup`
**Middleware:** `SessionAuth`
**File:** `app/http/controllers/auth_controller.go`

Calls `auth.Service.GenerateTOTP()`, stores the secret temporarily on the user record (but `TotpEnabled` stays `false` until confirmed).

**Response:**
```json
{
  "secret": "JBSWY3DPEHPK3PXP",
  "qr_url": "otpauth://totp/MacroWallets:user@email.com?secret=JBSWY3DPEHPK3PXP&issuer=MacroWallets"
}
```

**Route:** `POST /v1/auth/2fa/confirm`
**Middleware:** `SessionAuth`
**File:** `app/http/controllers/auth_controller.go`

**Request:**
```json
{
  "code": "123456"
}
```

Verifies the TOTP code against the user's stored (but not yet enabled) secret. On success:
- Sets `TotpEnabled = true`
- Generates recovery codes via `auth.Service.GenerateRecoveryCodes()`
- Returns recovery codes (shown once)

**Response:**
```json
{
  "enabled": true,
  "recovery_codes": ["abc123", "def456", "..."]
}
```

### 3.3 Upgrade `CreateWalletWithdrawal`

**File:** `app/http/controllers/wallet_withdrawals_controller.go`
**Request DTO:** `app/http/requests/create_wallet_withdrawal_request.go`

Add two new fields:

```go
type CreateWalletWithdrawalRequest struct {
    Amount             string `form:"amount"              json:"amount"`
    DestinationAddress string `form:"destination_address" json:"destination_address"`
    Note               string `form:"note"                json:"note,omitempty"`
    Passphrase         string `form:"passphrase"          json:"passphrase"`
    TotpCode           string `form:"totp_code"           json:"totp_code"`
}
```

Validation rules:
- `passphrase`: required, `min_len:12`
- `totp_code`: required, `len:6`, `numeric`
- Existing rules for `amount` (`decimal_string`) and `destination_address` (`blockchain_address`) remain

Controller logic (before creating the withdrawal record):

1. **Load user** from session
2. **Verify TOTP is enabled** — if `!user.TotpEnabled`, return 403 `{"error": "2FA must be enabled before withdrawing"}`
3. **Verify TOTP code** — call `authService.VerifyTOTP(user.TotpSecret, req.TotpCode)`. If invalid, return 401 `{"error": "invalid 2FA code"}`
4. **Verify passphrase** — load wallet, attempt `mpcpkg.DecryptShare(wallet.MPCCustomerShare, wallet.MPCShareIV, wallet.MPCShareSalt, req.Passphrase)`. If decryption fails, increment Redis rate limit (`vault:ratelimit:passphrase:{walletID}`). After 5 failures in 60s, return 429 `{"error": "too many failed attempts, try again later"}`. On single failure, return 401 `{"error": "invalid passphrase"}`
5. **Create withdrawal** — existing logic (creates `models.Withdrawal` with status `"pending"`)

---

## 4. Frontend changes

### 4.1 New dependency

Install `multicoin-address-validator` for client-side per-chain address validation.

```bash
cd front && npm install multicoin-address-validator
```

### 4.2 Address validation utility

**File:** `src/utils/validateAddress.ts`

```typescript
import WAValidator from "multicoin-address-validator";

const CHAIN_TO_COIN: Record<string, { coin: string; networkType?: string }> = {
  ETH:     { coin: "eth" },
  POLYGON: { coin: "eth" },
  BSC:     { coin: "eth" },
  AVAX:    { coin: "eth" },
  ARB:     { coin: "eth" },
  OP:      { coin: "eth" },
  BTC:     { coin: "btc" },
  TBTC:    { coin: "btc", networkType: "testnet" },
  SOL:     { coin: "sol" },
};

export function validateAddress(address: string, chain: string): boolean {
  const mapping = CHAIN_TO_COIN[chain.toUpperCase()];
  if (!mapping) return false;
  return WAValidator.validate(address, mapping.coin, mapping.networkType ?? "prod");
}
```

### 4.3 Refactor `WithdrawTab` into two steps

**File:** `src/components/Modals/Wallet/Withdraw/index.tsx`

The parent `WithdrawTab` manages a `step` state (`"form"` | `"confirm"`) and a `formData` state that preserves user input when navigating between steps.

```
WithdrawTab
├── WithdrawForm      (step === "form")
│   ├── Amount input with "Use max" inline button
│   ├── Destination input with per-chain validation
│   ├── Note input
│   └── "Continue" → validates, calls fee estimate, transitions to confirm
│
└── WithdrawConfirm   (step === "confirm")
    ├── Summary (From, To, Amount, Est. Fee, Total)
    ├── TotpSetup (if !user.totp_enabled) — inline QR + verify
    ├── Auth fields (passphrase + totp_code)
    └── "Back" / "Initiate Withdrawal"
```

### 4.4 `WithdrawForm` component

**File:** `src/components/Modals/Wallet/Withdraw/WithdrawForm.tsx`

Enhancements over current `WithdrawTab`:
- **Amount field**: `type="text"` (not `number` — avoids browser quirks with decimals). Numeric-only input filter. "Use max" button fills `wallet.balance`.
- **Destination field**: On blur, calls `validateAddress(value, wallet.chain)`. If invalid, shows `"Invalid {chain} address"`. Also validates on paste.
- **Form submission**: Calls `api.wallets.withdrawals.estimate()` in parallel with local validation. Passes `{ amount, destination, note, feeEstimate }` to the parent for the confirmation step.

### 4.5 `WithdrawConfirm` component

**File:** `src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx`

Renders the confirmation screen matching the BitGo-style reference:
- **Summary section**: Read-only display of From (wallet label + chain badge), To (destination, truncated), Amount, Est. Network Fee (or "Unavailable" with warning), Total
- **Authenticate section**: Depends on `user.totp_enabled`:
  - **TOTP enabled**: Wallet Password input (`type="password"`) + 2FA Code input (`type="text"`, maxLength 6, numeric filter)
  - **TOTP not enabled**: Renders `TotpSetup` component. Once setup completes, re-fetches user data and transitions to the password + 2FA fields
- **Actions**: "Back" preserves form data, "Initiate Withdrawal" calls updated `CreateWalletWithdrawal` with passphrase + totp_code

### 4.6 `TotpSetup` component

**File:** `src/components/Auth/TotpSetup.tsx`

Reusable component (can be used on the confirmation screen and later in Profile/Security settings).

Flow:
1. On mount, calls `POST /v1/auth/2fa/setup` → receives `{ secret, qr_url }`
2. Renders QR code from `qr_url` using `QRCodeSVG` (already installed via `qrcode.react`)
3. Shows manual key (formatted with spaces every 4 chars)
4. Verification code input (6 digits)
5. "Verify & Enable 2FA" button → calls `POST /v1/auth/2fa/confirm` with the code
6. On success, shows recovery codes in a copyable list with a "I've saved these codes" confirmation
7. Calls `onComplete()` callback to notify parent

### 4.7 API client additions

**File:** `src/lib/api/` (existing API module)

```typescript
// New methods
api.wallets.withdrawals.estimate(walletId, { amount, destination_address })
api.auth.twoFactor.setup()
api.auth.twoFactor.confirm({ code })
```

Updated method signature for `api.wallets.withdrawals.create`:
```typescript
api.wallets.withdrawals.create(walletId, {
  amount,
  destination_address,
  note?,
  passphrase,
  totp_code,
})
```

---

## 5. Chain-to-validator mapping

| Wallet `chain` value | `multicoin-address-validator` coin | Testnet? | Address patterns |
|---|---|---|---|
| `ETH`, `POLYGON`, `BSC`, `AVAX`, `ARB`, `OP` | `eth` | No | `0x` + 40 hex chars |
| `BTC` | `btc` | No | `1...`, `3...`, `bc1...` |
| `TBTC` | `btc` | Yes | `m...`, `n...`, `2...`, `tb1...` |
| `SOL` | `sol` | No | 32-44 base58 chars |

All EVM chains share the `eth` validator since they all use the same address format.

---

## 6. Error handling

| Scenario | Frontend behavior | Backend response |
|---|---|---|
| Fee estimation fails | Show "Fee estimate unavailable" with info icon, don't block withdrawal | 422 `{"error": "...", "code": "FEE_ESTIMATE_FAILED"}` |
| Invalid address (client-side) | Inline error below field on blur: "Invalid {chain} address" | N/A (client-side) |
| Invalid address (server-side) | Error toast from submit response | 422 validation error on `destination_address` |
| Amount exceeds balance | Inline error: "Insufficient balance" | 422 validation error on `amount` |
| Amount ≤ 0 or empty | Inline error: "Enter a valid amount" | 422 validation error |
| Wrong passphrase | Inline error below password field | 401 `{"error": "invalid passphrase"}` |
| Passphrase rate limited | Inline error + disable form temporarily | 429 `{"error": "too many failed attempts, try again later"}` |
| Wrong TOTP code | Inline error below 2FA field | 401 `{"error": "invalid 2FA code"}` |
| 2FA not enabled | Show inline TOTP setup (never reaches submit) | 403 `{"error": "2FA must be enabled before withdrawing"}` |
| Wallet frozen | Form disabled with explanation (existing behavior) | N/A (prevented in UI) |
| Network/server error | Generic error toast | 500 |

---

## 7. Security considerations

- **Passphrase never stored client-side** — sent only on the final submit, over HTTPS, used for MPC share decryption server-side, then discarded
- **TOTP rate limiting** — consider adding server-side rate limiting on TOTP verification (e.g., 10 attempts per 5 minutes) to prevent brute force
- **Recovery codes** — shown exactly once during TOTP setup, user must acknowledge they've saved them
- **Session auth remains required** — passphrase + TOTP are additional factors on top of the existing session JWT
- **Redis rate limit on passphrase** — reuses existing mechanism from `withdraw/service.go` (5 failures / 60s window)

---

## 8. Files affected

### Backend (new)
- `app/http/controllers/wallet_withdrawals_controller.go` — `EstimateWithdrawalFee` handler
- `app/http/controllers/auth_controller.go` — `SetupTwoFactor`, `ConfirmTwoFactor` handlers
- `app/http/requests/estimate_withdrawal_request.go` — request DTO
- `app/http/requests/setup_two_factor_request.go` — empty (session auth only)
- `app/http/requests/confirm_two_factor_request.go` — request DTO with `code` field
- `pkg/types/types.go` — `FeeEstimate` struct, `EstimateFee` on `Chain` interface
- `app/services/chain/evm.go` — `EstimateFee` implementation
- `app/services/chain/bitcoin.go` — `EstimateFee` implementation
- `app/services/chain/solana.go` — `EstimateFee` implementation
- `routes/admin.go` — new routes for 2FA setup/confirm and fee estimate

### Backend (modified)
- `app/http/requests/create_wallet_withdrawal_request.go` — add `passphrase`, `totp_code` fields + rules
- `app/http/controllers/wallet_withdrawals_controller.go` — `CreateWalletWithdrawal` adds TOTP + passphrase verification

### Frontend (new)
- `src/utils/validateAddress.ts` — client-side address validation
- `src/components/Modals/Wallet/Withdraw/WithdrawForm.tsx` — step 1 form
- `src/components/Modals/Wallet/Withdraw/WithdrawConfirm.tsx` — step 2 confirmation
- `src/components/Auth/TotpSetup.tsx` — reusable TOTP enrollment component

### Frontend (modified)
- `src/components/Modals/Wallet/Withdraw/index.tsx` — refactored to two-step orchestrator
- `src/lib/api/` — new API methods (estimate, 2FA setup/confirm)
- `src/types/api.ts` — new request/response types
- `package.json` — add `multicoin-address-validator`

---

## 9. Out of scope

- Fiat/crypto amount toggle (Cloubet has this for their betting context; not needed here)
- Whitelist-based bypass of 2FA (possible future enhancement)
- Multi-approval workflows (existing `required_approvals` field — separate feature)
- Email-based 2FA (only TOTP for now)
- Withdrawal status polling / real-time updates after submission
