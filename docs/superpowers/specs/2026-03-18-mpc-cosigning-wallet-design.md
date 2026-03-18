# MPC Co-Signing Wallet — Design Spec

**Date**: 2026-03-18
**Status**: Approved
**Chains**: Bitcoin, Ethereum, Solana

---

## Overview

Implement a non-custodial co-signing custody model inspired by BitGo. Customers own their key material (protected by a passphrase they control). The service holds a separate key share in AWS KMS. Neither party can sign alone — both shares are required for every transaction.

---

## Security Model

```
Wallet Creation:
  Customer calls POST /v1/wallets  { chain, label, passphrase }
       │
       ├─ Service runs tss-lib 2-party keygen (in-process via Go channels)
       ├─ Produces: share_A (customer's) + share_B (service's) + combinedPublicKey
       ├─ share_A  → Argon2id(passphrase) → AES-256-GCM encrypted → stored in DB
       └─ share_B  → stored in AWS KMS (referenced by key ID)

Withdrawal:
  Customer calls POST /v1/wallets/{id}/withdrawals  { to, amount, passphrase }
       │
       ├─ Decrypt share_A using passphrase + stored salt
       ├─ Fetch share_B from AWS KMS
       ├─ Run tss-lib internal signing (both shares in memory, same process)
       ├─ Wipe both shares from memory immediately after signing
       └─ Broadcast signed transaction to chain RPC
```

### Threat Properties

| Threat | Protected? | Reason |
|--------|-----------|--------|
| DB stolen | Yes | share_A is AES-256-GCM encrypted; share_B is in KMS |
| KMS compromised alone | Yes | share_A requires passphrase to decrypt |
| DB + KMS stolen | Yes | share_A still requires correct passphrase |
| DB + KMS + passphrase | No | Full compromise — unavoidable in any custody model |
| Passphrase stored by service | N/A | Passphrase is never stored; provided fresh on every call |

---

## Library

**`bnb-chain/tss-lib` v2.0.2** — the only production Go TSS library supporting both:
- `secp256k1` (ECDSA) — Bitcoin and Ethereum
- `ed25519` (EdDSA) — Solana

Both parties (customer share goroutine, service share goroutine) run in-process and exchange messages via Go channels. No network transport layer required.

### Curve Selection per Chain

| Chain | Curve | Algorithm |
|-------|-------|-----------|
| Bitcoin | secp256k1 | ECDSA |
| Ethereum | secp256k1 | ECDSA |
| Solana | ed25519 | EdDSA |

---

## Database Schema Changes

Modify the existing wallets migration directly (DB can be refreshed). Add the following columns to the `wallets` table:

```sql
mpc_customer_share  BYTEA NOT NULL  -- AES-256-GCM encrypted share_A
mpc_share_iv        BYTEA NOT NULL  -- AES-256-GCM nonce (12 bytes)
mpc_share_salt      BYTEA NOT NULL  -- Argon2id salt (16 bytes)
mpc_service_key_ref TEXT  NOT NULL  -- AWS KMS secret ID for share_B
mpc_public_key      TEXT  NOT NULL  -- Combined public key (used for address derivation)
mpc_curve           TEXT  NOT NULL  -- 'secp256k1' | 'ed25519'
```

### Passphrase Key Derivation

```
passphrase (provided in API call, never stored)
    │
    └─ Argon2id(passphrase, salt, time=3, memory=64MB, threads=4)
            │
            └─ 32-byte key → AES-256-GCM encrypt / decrypt share_A
```

Argon2id is chosen over bcrypt/PBKDF2 for its memory-hardness, making brute-force attacks against a stolen database significantly more expensive.

---

## API Changes

### POST /v1/wallets

Add `passphrase` field to request body.

**Request:**
```json
{
  "chain": "eth",
  "label": "My ETH Wallet",
  "passphrase": "strong-passphrase"
}
```

**Internal flow:**
1. Validate chain is supported and passphrase is non-empty
2. Run `mpc.Keygen(ctx, curve)` → `share_A`, `share_B`, `combinedPublicKey`
3. Generate random 16-byte `salt`, derive 32-byte key via Argon2id
4. Encrypt `share_A` with AES-256-GCM → store `mpc_customer_share`, `mpc_share_iv`, `mpc_share_salt`
5. Store `share_B` in AWS KMS → store returned `mpc_service_key_ref`
6. Derive deposit address from `combinedPublicKey`
7. Persist wallet, return wallet object (no share data, passphrase never echoed)

---

### POST /v1/wallets/{id}/withdrawals

Add `passphrase` field to request body.

**Request:**
```json
{
  "to": "0xABC...",
  "amount": "0.01",
  "passphrase": "strong-passphrase"
}
```

**Internal flow:**
1. Load wallet, validate `to` address and `amount`
2. Derive decryption key from `passphrase` + `mpc_share_salt` via Argon2id
3. AES-256-GCM decrypt `mpc_customer_share` → if decryption fails, return `401 Invalid passphrase`
4. Fetch `share_B` from AWS KMS using `mpc_service_key_ref`
5. Build unsigned transaction (fee estimation, nonce/sequence for the chain)
6. Run `mpc.Sign(ctx, shareA, shareB, txHash)` → signature bytes
7. Wipe `share_A` and `share_B` from memory
8. Broadcast signed transaction to chain RPC
9. Persist `WithdrawalTransaction`, enqueue webhook event
10. Return `{ id, status: "pending", txHash }`

### Error Responses

| Scenario | HTTP | Message |
|----------|------|---------|
| Wrong passphrase | 401 | `invalid passphrase` |
| Chain RPC unavailable | 502 | `chain unavailable` |
| Insufficient balance | 422 | `insufficient funds` |
| KMS unavailable | 503 | `signing service unavailable` |

---

## New Service: `app/services/mpc/`

```
app/services/mpc/
├── service.go       # MPC interface definition
├── keygen.go        # tss-lib 2-party keygen ceremony (Go channels)
├── signing.go       # tss-lib 2-party signing (Go channels)
├── keystore.go      # Argon2id key derivation + AES-256-GCM encrypt/decrypt
└── service_test.go  # Unit tests for all MPC operations
```

### Interface

```go
type Service interface {
    Keygen(ctx context.Context, curve Curve) (*KeygenResult, error)
    Sign(ctx context.Context, shareA, shareB, txHash []byte) ([]byte, error)
}

type Curve string

const (
    CurveSecp256k1 Curve = "secp256k1"
    CurveEd25519   Curve = "ed25519"
)

type KeygenResult struct {
    ShareA         []byte // encrypted and stored in DB
    ShareB         []byte // stored in AWS KMS
    CombinedPubKey []byte // used for address derivation
}
```

### In-Process Party Communication

Both MPC parties run as goroutines within the same process, exchanging tss-lib messages via buffered Go channels. No network layer or external party transport is required.

```
goroutine: customerParty  ←── channels ──→  goroutine: serviceParty
     │                                              │
  share_A result                              share_B result
```

---

## Container Changes

`app/container/container.go` adds:

```go
MPCService  mpc.Service
KMSClient   *kms.Client  // AWS SDK v2 KMS client
```

Both initialized at boot. `MPCService` receives `KMSClient` for share_B storage/retrieval.

---

## Wallet Service Changes

- `CreateWallet(ctx, chain, label, passphrase string)` — passphrase added
- Internally calls `MPCService.Keygen()`, encrypts share_A, stores share_B to KMS

## Withdrawal Service Changes

- `Execute(ctx, msg)` — `WithdrawalMessage` gains `Passphrase string` field
- Decrypts share_A, fetches share_B from KMS, calls `MPCService.Sign()`

---

## Dependencies to Add

```
github.com/bnb-chain/tss-lib/v2
golang.org/x/crypto                     # Argon2id
github.com/aws/aws-sdk-go-v2/service/kms
github.com/aws/aws-sdk-go-v2/service/secretsmanager  # if needed for share_B blobs
```

---

## Out of Scope (v1)

- Passphrase rotation / re-encryption of share_A
- Share refresh (proactive security)
- Multi-device key recovery
- Hardware security module (HSM) integration
- Audit log of signing operations
