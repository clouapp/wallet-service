# MPC Wallet Creation with KeyCard — Design Spec

**Date:** 2026-03-24
**Status:** Approved

---

## Overview

Replace the placeholder `AdminCreateWallet` controller with the full MPC wallet creation flow. When an admin creates a wallet through the dashboard, the backend runs a real MPC key generation ceremony (using LocalStack Secrets Manager in dev), generates a KeyCard PDF (client-side), and requires the user to enter a 6-digit activation code from the PDF before the wallet becomes active.

---

## Section 1 — LocalStack Wiring

LocalStack is already declared in `docker-compose.yml` with `SERVICES=sqs,secretsmanager,kms` on port `4566`.

Explicit endpoint override in `app/container/container.go` (not the providers shim at `app/providers/container.go`). When `AWS_ENDPOINT_URL` is set, pass a custom endpoint resolver to the Secrets Manager client:

```go
smClient := secretsmanager.NewFromConfig(awsCfg)
if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
    smClient = secretsmanager.NewFromConfig(awsCfg,
        secretsmanager.WithEndpointResolverV2(staticEndpointResolver(endpoint)))
}
```

`staticEndpointResolver` is a small local struct implementing `secretsmanager.EndpointResolverV2` that returns the given URL for all regions.

Ensure `.env.dev` contains (it may already be present — add if absent):
```
AWS_ENDPOINT_URL=http://localhost:4566
AWS_ACCESS_KEY_ID=test
AWS_SECRET_ACCESS_KEY=test
AWS_DEFAULT_REGION=us-east-1
```

---

## Section 2 — Database: Model + Migration

### Migration: `20260324000015_alter_wallets_add_keycard_fields`

Two changes:

1. Add `activation_code char(6) nullable` column.
2. Change the `status` column default from `active` to `pending`. Goravel's Blueprint does not support `ChangeDefault` — use a raw DB exec inside the same `Up()`:

```go
func (r *M20260324000015AlterWalletsAddKeycardFields) Up() error {
    if err := facades.Schema().Table("wallets", func(table schema.Blueprint) {
        table.String("activation_code", 6).Nullable()
    }); err != nil {
        return err
    }
    return facades.DB().Exec("ALTER TABLE wallets ALTER COLUMN status SET DEFAULT 'pending'").Error
}

func (r *M20260324000015AlterWalletsAddKeycardFields) Down() error {
    facades.DB().Exec("ALTER TABLE wallets ALTER COLUMN status SET DEFAULT 'active'")
    return facades.Schema().Table("wallets", func(table schema.Blueprint) {
        table.DropColumn("activation_code")
    })
}
```

Existing wallets are unaffected by the default change (already have a `status` value).

### Wallet model changes

Add field:
```go
ActivationCode *string `gorm:"type:char(6)" json:"-"`
```

`activation_code` is never serialised in JSON responses.

---

## Section 3 — Wallet Service

### Interface change

`wallet.Service.CreateWallet` changes signature from:
```go
func (s *Service) CreateWallet(ctx context.Context, chainID, label, passphrase string) (*models.Wallet, error)
```
to:
```go
func (s *Service) CreateWallet(ctx context.Context, chainID, label, passphrase string) (*CreateWalletResult, error)
```

The existing caller is the `CreateWallet` controller in `wallets_controller.go`, which currently routes `POST /api/v1/wallets` (external API). That controller must be updated to read `result.Wallet` and return only the wallet (no keycard data — external API clients don't receive keycard). The existing service test at `wallet/service_test.go` must also be updated to handle the new return type.

### New return type: `CreateWalletResult`

```go
// In package wallet
type CreateWalletResult struct {
    Wallet            *models.Wallet
    EncryptedUserKey  string // JSON {"iv":"<b64>","salt":"<b64>","ct":"<b64>","cipher":"aes-256-gcm","kdf":"argon2id"}
    ServicePublicKey  string // hex of CombinedPubKey
    EncryptedPasscode string // JSON {"iv":"<b64>","ct":"<b64>","cipher":"aes-256-gcm"} — no KDF, WALLET_SERVICE_KEY is high-entropy
    ActivationCode    string // 6-digit zero-padded decimal string e.g. "044321"
}
```

### EncryptedPasscode — purpose and key

`EncryptedPasscode` is stored on the KeyCard so that if the user loses their passphrase, they can contact the service operator who holds `WALLET_SERVICE_KEY` and can decrypt it to restore the passphrase and access to ShareA.

**Key:** `WALLET_SERVICE_KEY` env var — a 64-character lowercase hex string (32 bytes). This is distinct from Goravel's `APP_KEY`. Document in `.env.example`.

**New helper: `mpc.EncryptWithServiceKey`**

Location: `app/services/mpc/keystore.go` (alongside existing `EncryptShare`/`DecryptShare`)

```go
// EncryptWithServiceKey encrypts data with a service-held AES-256-GCM key.
// keyHex must be a 64-character hex string (32 bytes).
// No KDF is applied — the key is assumed to be high-entropy (machine-generated).
// Returns JSON: {"iv":"<b64>","ct":"<b64>","cipher":"aes-256-gcm"}
func EncryptWithServiceKey(data []byte, keyHex string) (string, error)
```

Implementation:
1. `hex.DecodeString(keyHex)` → 32-byte key. If len ≠ 32, return error.
2. `io.ReadFull(rand.Reader, nonce)` — 12-byte nonce.
3. `aes.NewCipher(key)` → `cipher.NewGCM(block)` → `gcm.Seal(nil, nonce, data, nil)`.
4. Return JSON with `iv = base64(nonce)`, `ct = base64(ciphertext)`, `cipher = "aes-256-gcm"`.

### Chain ID normalisation

Applied at the top of `CreateWallet` in the service:

```go
chainID = strings.ToLower(chainID)
if chainID == "matic" {
    chainID = "polygon"
}
```

This keeps normalisation in one place. The stored `chain` value in the DB will be the normalised form (e.g. `"polygon"`, not `"matic"`). The frontend must display the correct label for the stored value.

**Solana:** `keygenEd25519` in `mpc/keygen.go` is a stub that returns an error. The chain `"sol"` is therefore unsupported for wallet creation at v1. SOL must be **removed from the chain dropdown** in `CreateWalletModal`. The `curveForChain` function already handles this gracefully (returns `secp256k1` for unknown chains), but the keygen will fail. Remove SOL rather than surfacing a confusing 500.

Supported chains for `POST /v1/wallets`: `eth`, `polygon`, `btc`.

### Wallet creation steps

1. Normalise chain ID (ToLower + matic→polygon alias).
2. Validate chain is in registry. If not → return error immediately, before generating any key material.
3. Check no wallet exists for this chain (existing guard — keep).
4. MPC `Keygen(ctx, secp256k1)` → `ShareA`, `ShareB`, `CombinedPubKey`.
5. `mpc.EncryptShare(ShareA, passphrase)` → `EncryptedShare{Ciphertext, IV, Salt}`.
6. Format `EncryptedUserKey` JSON (all values base64-encoded):
   `{"iv":"…","salt":"…","ct":"…","cipher":"aes-256-gcm","kdf":"argon2id"}`
7. `mpc.EncryptWithServiceKey([]byte(passphrase), os.Getenv("WALLET_SERVICE_KEY"))` → `EncryptedPasscode` JSON.
8. Generate activation code:
   ```go
   import cryptorand "crypto/rand"
   import "math/big"

   n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
   if err != nil { return nil, err }
   code := fmt.Sprintf("%06d", n.Int64())
   ```
9. Store `ShareB` in Secrets Manager: `vault/wallet/{walletID}/share-b` using `CreateSecret`. Wallet IDs are UUID v4 — no collision possible.
   If any subsequent step (10–13) fails after this point, log `"orphaned secret ARN: <arn>"` as a warning and return the error. All post-`CreateSecret` failures are handled identically.
10. Derive `DepositAddress` from `CombinedPubKey`.
11. Insert wallet: `status = "pending"`, `activation_code = code`, all MPC fields populated.
12. Cache deposit address in Redis if client is non-nil. Redis failure is **non-fatal**: log warning, continue.
13. Return `CreateWalletResult`.

### New service method: `ActivateWallet`

```go
func (s *Service) ActivateWallet(ctx context.Context, walletID uuid.UUID, code string) (*models.Wallet, error)
```

- Load wallet by ID. If not found → `ErrWalletNotFound`.
- If `status != "pending"` → `ErrWalletAlreadyActive`.
- Compare with timing-safe equality:
  ```go
  if subtle.ConstantTimeCompare([]byte(*w.ActivationCode), []byte(code)) != 1 {
      return nil, ErrInvalidActivationCode
  }
  ```
- Update: `status = "active"`, `activation_code = NULL`.
- Return updated wallet.

No TTL on activation code at v1. If a user loses their PDF before activating, they must create a new wallet. The pending wallet remains in the DB and can be cleaned up manually.

---

## Section 4 — Backend: Routes + Controllers

### Remove

`AdminCreateWallet` function and `AdminCreateWalletRequest` struct from `wallets_controller.go`. Remove the corresponding `router.Post("", controllers.AdminCreateWallet)` line from `routes/api.go`.

### Routes (routes/api.go)

In the existing `facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth).Group(...)` block, add:

```go
router.Post("", controllers.CreateWalletAdmin)
```

Inside the nested `router.Prefix("/{walletId}").Group(...)` block (alongside existing Get/Patch/etc. on wallets), add:

```go
r.Post("/activate", controllers.ActivateWallet)
```

### `CreateWalletAdmin` controller

Request bound from body — use a local struct (not the existing `CreateWalletRequest` which has `Passphrase` validation rules appropriate for the external API):

```go
type createWalletAdminBody struct {
    Chain             string `json:"chain"`
    Label             string `json:"label"`
    Passphrase        string `json:"passphrase"`
    ConfirmPassphrase string `json:"confirm_passphrase"`
}
```

Validation in controller:
- `chain` non-empty
- `label` non-empty
- `passphrase` length ≥ 12
- `passphrase == confirm_passphrase` (plain equality is acceptable here — both values arrive in the same authenticated request from the same user)

**Never log `passphrase` or `confirm_passphrase`** — not in error messages, structured logs, or Goravel's request logger.

On 201:
```json
{
  "wallet": { "id": "…", "chain": "eth", "label": "…", "status": "pending", "deposit_address": "…", "created_at": "…" },
  "encrypted_user_key": "{…}",
  "service_public_key": "02abc…",
  "encrypted_passcode": "{…}",
  "activation_code": "822032"
}
```

`activation_code` is intentionally returned in the API response — the frontend renders it on-screen in Step 2 so the user can visually confirm it matches the PDF. The PDF download is gated client-side before the activate button is enabled (no server-side enforcement; see Section 5).

**Update the external `CreateWallet` controller** (for `POST /api/v1/wallets`) to handle the new `*CreateWalletResult` return type: read `result.Wallet` and return only the wallet record (no keycard data for API clients).

### `ActivateWallet` controller

```go
type activateWalletBody struct {
    Code string `json:"code"`
}
```

- Parse `walletId` from route.
- Bind body.
- Call `WalletService.ActivateWallet(ctx, walletID, req.Code)`.

Error → HTTP status:
| Error | Status |
|---|---|
| `ErrWalletNotFound` | 404 |
| `ErrWalletAlreadyActive` | 409 |
| `ErrInvalidActivationCode` | 400 |
| other | 500 |

On 200:
```json
{ "status": "active" }
```

---

## Section 5 — Frontend: Two-Step CreateWalletModal

### State types

```ts
interface KeycardState {
  walletId: string
  walletName: string
  chain: string          // normalised display value, e.g. "ETH"
  encryptedUserKey: string
  servicePublicKey: string
  encryptedPasscode: string
  activationCode: string
}
```

`KeycardState` held in `useState` inside `CreateWalletModal`. Cleared when modal closes. If user closes after Step 1 but before activating, the wallet remains `pending` in the DB — no v1 recovery path.

### Step 1 — Wallet details form

Supported chains (remove SOL — keygen not implemented; remove LTC/DOGE — not in registry):

| Display | Value sent |
|---|---|
| Bitcoin (BTC) | BTC |
| Ethereum (ETH) | ETH |
| Polygon (MATIC) | MATIC |

Fields:
- **Chain** (select, options above)
- **Wallet Name** (text input, required)
- **Passphrase** (password input, show/hide toggle)
- **Confirm Passphrase** (password input, show/hide toggle)

Client-side validation before submit:
- Passphrase ≥ 12 characters
- Both passphrase fields match

Submit: `POST /v1/wallets` with `{ chain, label, passphrase, confirm_passphrase }`.

On 201 → set `keycardState`, set step to `"keycard"`.

The `chain` value stored in `KeycardState` is the display value (`"ETH"`, `"MATIC"`, `"BTC"`). The backend stores the normalised form (`"eth"`, `"polygon"`, `"btc"`); the wallet object in the response contains the stored form. The KeyCard PDF header uses the display value from the request.

### Step 2 — KeyCard + Activation

- Warning: "Download your KeyCard and store it safely before continuing. You will need it to recover your wallet."
- **Download KeyCard PDF** button → calls `generateKeycardPdf(keycardData)` → browser download. After click, set `hasDownloaded = true`.
- Code input: enabled only when `hasDownloaded === true`. 6-digit numeric input, `maxLength={6}`.
- **Activate Wallet** button: enabled only when `hasDownloaded && code.length === 6`.
- On submit: `POST /v1/wallets/{id}/activate` with `{ code }`.
- On 200 → `mutate()` (SWR revalidation) → `router.push(/dashboard/wallets/{id})`.

The "download required before activate" gate is enforced **client-side only** (`hasDownloaded` state flag). The server does not verify that the PDF was downloaded. This is intentional at v1.

---

## Section 6 — KeyCard PDF (`@react-pdf/renderer`)

### Dependencies

```
@react-pdf/renderer
qrcode
@types/qrcode
```

### `lib/keycard/generateKeycardPdf.ts`

```ts
interface KeycardData {
  walletName: string
  chain: string          // display value, e.g. "ETH"
  createdAt: string      // e.g. "Tue Mar 24 2026"
  activationCode: string
  encryptedUserKey: string   // full JSON string
  servicePublicKey: string   // hex string
  encryptedPasscode: string  // full JSON string
}

export async function generateKeycardPdf(data: KeycardData): Promise<void>
// Renders PDF to blob, triggers browser download via URL.createObjectURL
```

### QR code handling

`qrcode.toDataURL(str, { errorCorrectionLevel: 'L' })`.

QR codes are **best-effort visual aids**. The full JSON/hex data is always rendered as a text block in the PDF regardless of QR availability. If `qrcode.toDataURL` rejects (payload too large), render a `"— QR unavailable —"` text placeholder in the QR slot. The text block remains and contains the full data — the KeyCard is still usable without the QR.

### PDF structure (2 pages)

**Page 1:**
- Header row: app wordmark | chain display name (e.g. "ETH") | "KeyCard" label | box containing "Activation Code" label + code in large bold type
- Red warning banner: "Print this document, or keep it securely offline. See below for FAQ."
- Creation date + wallet name (below banner)

**Section A: User Key**
> "Your MPC key share, encrypted with your wallet passphrase."
- Full JSON text block (wrapping monospace)
- QR code (best-effort, see above)

**Section C: Service Public Key**
> "The public key used to verify co-signed transactions."
- Full hex text block
- QR code (best-effort)

**Section D: Encrypted Wallet Password**
> "Your passphrase, encrypted by the service for account recovery purposes."
- Full JSON text block
- QR code (best-effort)

*(Section B is intentionally omitted — the 2-party MPC model has no separate backup key.)*

**Page 2:** FAQ — adapted from BitGo's KeyCard FAQ with service name substituted and Bitcoin-specific references updated to generic "cryptocurrency."

---

## Out of Scope (v1)

- SOL / LTC / DOGE chain support
- Activation code expiry or regeneration UI
- Service-side passcode recovery endpoint (D field written to KeyCard; recovery is manual operator action at v1)
- Wallet ownership check on `ActivateWallet` (any authenticated user with the wallet ID + code can activate; tighten in v2)
- Orphaned Secrets Manager secret cleanup for failed wallet inserts
- Server-side enforcement of PDF download before activation
