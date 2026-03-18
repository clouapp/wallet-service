# MPC Co-Signing Wallet — Design Spec

**Date**: 2026-03-18
**Status**: Approved
**Chains**: Bitcoin, Ethereum, Solana

---

## Overview

Implement a non-custodial co-signing custody model inspired by BitGo. Customers own their key material (protected by a passphrase they control). The service holds a separate key share in AWS Secrets Manager. Neither party can sign alone — both shares are required for every transaction.

---

## Security Model

```
Wallet Creation:
  Customer calls POST /v1/wallets  { chain, label, passphrase }
       │
       ├─ Service runs tss-lib 2-party keygen (in-process via Go channels)
       ├─ Produces: share_A (customer's) + share_B (service's) + combinedPublicKey
       ├─ share_A  → Argon2id(passphrase) → AES-256-GCM encrypted → stored in DB
       └─ share_B  → stored in AWS Secrets Manager (referenced by secret ARN)

Withdrawal (synchronous — no SQS for key material):
  Customer calls POST /v1/wallets/{id}/withdrawals  { to, amount, passphrase }
       │
       ├─ Check idempotency (no lock needed)
       ├─ Acquire per-wallet Redis lock
       ├─ Validate passphrase ≥ 12 characters
       ├─ Check on-chain balance BEFORE loading any key material
       ├─ Decrypt share_A using passphrase + stored salt
       ├─ Fetch share_B from AWS Secrets Manager
       ├─ Run tss-lib internal signing (both shares in memory, same process)
       ├─ Defer zero-wipe both shares (runs on any exit — success or error)
       ├─ Broadcast signed transaction to chain RPC
       ├─ Release Redis lock
       └─ Persist WithdrawalTransaction, enqueue webhook-only SQS message (no passphrase)
```

### Why Withdrawals Are Synchronous

Passing a passphrase through SQS would persist it in the queue (even with SSE), violating the threat model. The SQS `withdrawal_worker` Lambda and its associated SQS queue are **decommissioned** as part of this change. Post-broadcast event notification is enqueued via the existing webhook SQS queue, which carries no key material or passphrase.

### Threat Properties

| Threat | Protected? | Reason |
|--------|-----------|--------|
| DB stolen | Yes | share_A is AES-256-GCM encrypted; share_B is in Secrets Manager |
| Secrets Manager compromised alone | Yes | share_A requires passphrase to decrypt |
| DB + Secrets Manager stolen | Yes | share_A still requires correct passphrase |
| DB + Secrets Manager + passphrase | No | Full compromise — unavoidable in any custody model |
| Passphrase stored by service | N/A | Passphrase is never stored; provided fresh on every call |
| Online brute-force | Mitigated | Sliding-window rate limiting (see Rate Limiting section) |

---

## Library

**`bnb-chain/tss-lib` v2.0.2** — supports both secp256k1 (BTC/ETH) and ed25519 (Solana).

Both parties run in-process via buffered Go channels. No network transport required.

### First Implementation Step: Dependency Resolution

Before writing any MPC code, run:

```bash
go get github.com/bnb-chain/tss-lib/v2@v2.0.2
```

Confirm the module graph resolves cleanly against the existing `goravel/framework` dependency tree. `tss-lib` has non-trivial transitive dependencies (gmp, protobuf-generated code, pre-computed parameters) that can conflict. If `go get` fails, the fallback in order of preference is: (1) vendor the library, (2) use a compatible fork. Do not proceed with implementation until `go build ./...` passes with the new dependency.

### Solana ed25519 Compatibility Gate

**Implementation is blocked on this verification.** Before any Solana-related MPC code is merged, the following test must pass:

1. Use `tss-lib` v2.0.2 to generate an ed25519 2-party keypair via `Keygen`
2. Sign a known Solana transaction message hash with both shares
3. Verify the resulting signature against the public key using `crypto/ed25519.Verify`
4. Submit the signed transaction to Solana Devnet and confirm it is accepted

If this test fails, Solana MPC is deferred to v2 and the `CurveEd25519` constant and all ed25519 paths are excluded from the v1 implementation.

### Curve Selection per Chain

| Chain | Curve | Algorithm |
|-------|-------|-----------|
| Bitcoin | secp256k1 | ECDSA |
| Ethereum | secp256k1 | ECDSA |
| Solana | ed25519 | EdDSA |

---

## Database Schema

Initial migration — no existing migration files in the project. The wallets table is created with MPC columns from the start:

```sql
CREATE TABLE wallets (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain               VARCHAR(50)  NOT NULL,
    label               VARCHAR(255),
    -- MPC key material
    mpc_customer_share  BYTEA NOT NULL,  -- AES-256-GCM ciphertext || 16-byte GCM tag
    mpc_share_iv        BYTEA NOT NULL,  -- AES-256-GCM nonce, exactly 12 bytes
    mpc_share_salt      BYTEA NOT NULL,  -- Argon2id salt, exactly 16 bytes
    mpc_secret_arn      TEXT  NOT NULL,  -- AWS Secrets Manager ARN for share_B
    mpc_public_key      TEXT  NOT NULL,  -- hex-encoded compressed pubkey: 33 bytes secp256k1 (66 hex chars) or 32 bytes ed25519 (64 hex chars). Always stored compressed. Ethereum address derivation must first decompress to 65 bytes.
    mpc_curve           TEXT  NOT NULL,  -- 'secp256k1' | 'ed25519'
    created_at          TIMESTAMP DEFAULT NOW()
);
```

### AES-256-GCM Wire Format

`mpc_customer_share` stores `ciphertext || tag` as a single byte slice. The 16-byte GCM authentication tag is appended to the ciphertext before storage. The IV is stored separately in `mpc_share_iv`. Decryption must strip the last 16 bytes as the tag.

### Public Key Storage Format

- secp256k1: 33-byte compressed point, hex-encoded (66 hex characters)
- ed25519: 32-byte public key, hex-encoded (64 hex characters)

### Passphrase Key Derivation

```
passphrase (provided in API call, minimum 12 characters, never stored)
    │
    └─ Argon2id(passphrase, salt, time=3, memory=64MB, threads=4)
            │
            └─ 32-byte key → AES-256-GCM encrypt / decrypt share_A
```

**Lambda memory requirement**: The 64 MB Argon2id parameter, combined with tss-lib signing buffers and Go runtime overhead, requires a minimum Lambda memory allocation of **512 MB** for the API function. Lambda timeout must be set to **≥ 30 seconds** to accommodate: Argon2id (~1-3s) + tss-lib signing (~1-5s) + RPC broadcast latency.

---

## MPC Service Interface

```go
// Curve identifies the elliptic curve used for MPC operations.
type Curve string

const (
    CurveSecp256k1 Curve = "secp256k1"
    CurveEd25519   Curve = "ed25519"
)

// KeygenResult holds the outputs of a 2-party MPC key generation ceremony.
type KeygenResult struct {
    ShareA         []byte // customer's share — must be encrypted before storage
    ShareB         []byte // service's share — must be sent to Secrets Manager
    CombinedPubKey []byte // compressed public key (33 bytes secp256k1; 32 bytes ed25519)
}

// SignInputs carries all transaction data required for signing.
// Bitcoin may have multiple inputs (one hash per UTXO); ETH/Solana have a single hash.
type SignInputs struct {
    TxHashes [][]byte // one entry per input
}

type Service interface {
    Keygen(ctx context.Context, curve Curve) (*KeygenResult, error)
    Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error)
}
```

---

## Address Derivation for MPC Wallets

Each MPC wallet has **exactly one deposit address** in v1, derived from `CombinedPubKey`:

| Chain | Derivation |
|-------|-----------|
| Bitcoin | P2WPKH — SegWit v0 address from 33-byte compressed secp256k1 pubkey |
| Ethereum | `0x` + last 20 bytes of `Keccak256(uncompressed_pubkey[1:])` |
| Solana | Base58Check of the 32-byte ed25519 public key |

The `GenerateAddress` endpoint (`POST /v1/wallets/{id}/addresses`) always returns HTTP 422 in v1, since all wallets are MPC wallets and multi-address derivation is out of scope. The route remains registered but returns a clear error message: `"address derivation not supported for MPC wallets in v1"`.

---

## API Changes

### POST /v1/wallets

Adds `passphrase` field to request body.

**Request:**
```json
{ "chain": "eth", "label": "My ETH Wallet", "passphrase": "strong-passphrase-12chars" }
```

**Validation:** passphrase non-empty and ≥ 12 characters (HTTP 400 otherwise); chain must be supported.

**Flow:**
1. Validate chain and passphrase
2. `mpc.Keygen(ctx, curve)` → `KeygenResult`
3. Generate random 16-byte `salt`; derive 32-byte key via Argon2id
4. AES-256-GCM encrypt `ShareA` → `ciphertext || tag`; store with IV and salt
5. Store `ShareB` in AWS Secrets Manager → store ARN as `mpc_secret_arn`
6. Derive deposit address from `CombinedPubKey` per chain rules above
7. Persist wallet; return wallet object (no share data, passphrase never returned)

---

### POST /v1/wallets/{id}/withdrawals

Adds `passphrase` field. **Signing is fully synchronous in the API Lambda.**

**Request:**
```json
{ "to": "0xABC...", "amount": "0.01", "passphrase": "strong-passphrase-12chars" }
```

**Flow:**
1. Validate passphrase ≥ 12 characters (HTTP 400) — fast check, no I/O, must run before lock
2. Check idempotency — return existing withdrawal if already submitted (no lock needed)
3. Acquire per-wallet Redis lock (`vault:lock:withdrawal:{wallet_id}`, SET NX PX 60000)
4. Validate `to` address format and `amount` > 0
5. `ChainAdapter.GetBalance()` — return HTTP 422 if insufficient (lock released via defer)
6. Argon2id(passphrase, `mpc_share_salt`) → 32-byte decryption key
7. AES-256-GCM decrypt `mpc_customer_share` → `shareA` — return HTTP 401 on auth failure
8. Fetch `shareB` from Secrets Manager (`mpc_secret_arn`) — return HTTP 503 on failure
9. `defer` zero-wipe: `for i := range shareA { shareA[i] = 0 }` and same for `shareB` — registered immediately after fetch, runs on any exit
10. Build unsigned transaction (fee estimation, nonce/sequence)
11. `mpc.Sign(ctx, curve, shareA, shareB, SignInputs{TxHashes})` → signature bytes
12. Broadcast signed transaction to chain RPC
13. Release Redis lock
14. Persist `WithdrawalTransaction` (no passphrase anywhere in the record)
15. Enqueue webhook-only SQS message via webhook queue (no key material)
16. Return `{ id, status: "pending", txHash }`

### Error Responses

| Scenario | HTTP | Message |
|----------|------|---------|
| Wrong passphrase | 401 | `invalid passphrase` |
| Passphrase too short | 400 | `passphrase must be at least 12 characters` |
| Insufficient balance | 422 | `insufficient funds` |
| Chain RPC unavailable | 502 | `chain unavailable` |
| Secrets Manager unavailable | 503 | `signing service unavailable` |
| Concurrent withdrawal in progress | 409 | `withdrawal already in progress for this wallet` |
| GenerateAddress on MPC wallet | 422 | `address derivation not supported for MPC wallets in v1` |

---

## Memory Wiping

Shares must always be held as `[]byte`, never `string` (strings are immutable in Go and cannot be zeroed). Zero-wipe is registered as a `defer` **immediately after the shares are loaded** (step 9 above), before any code path that could panic or return early:

```go
defer func() {
    for i := range shareA { shareA[i] = 0 }
    for i := range shareB { shareB[i] = 0 }
}()
```

This guarantees wiping on success, error return, and panic paths.

---

## Concurrent Withdrawal Protection

Per-wallet Redis distributed lock:
- Key: `vault:lock:withdrawal:{wallet_id}`
- SET NX PX 60000 (60-second TTL as dead-man switch)
- Acquired after idempotency check (step 2), before any key material is loaded
- Released explicitly after broadcast (step 13); also expires automatically if Lambda crashes
- Contention response: HTTP 409 `withdrawal already in progress for this wallet`

---

## Rate Limiting

Sliding-window rate limit on failed passphrase attempts per wallet:
- Redis key: `vault:ratelimit:passphrase:{wallet_id}`
- Maximum 5 failures per 60-second sliding window
- On 401 response: `INCR` key, set TTL to 60s if key is new
- On limit exceeded: HTTP 429 `too many failed attempts, try again later`
- **Counter is NOT reset on success** — the window counts all failures regardless of intervening successes. Resetting on success would allow bypassing the limit by alternating correct and incorrect passphrases.

---

## New Service: `app/services/mpc/`

```
app/services/mpc/
├── service.go       # Service interface, Curve type, KeygenResult, SignInputs
├── keygen.go        # tss-lib 2-party keygen ceremony via Go channels
├── signing.go       # tss-lib 2-party signing via Go channels
├── keystore.go      # Argon2id key derivation + AES-256-GCM encrypt/decrypt
└── service_test.go  # Unit tests + Solana ed25519 test vector (gating test)
```

---

## Infrastructure Changes

### Decommissioned

The project is not yet in production — no queue draining is needed.

- Delete `handleWithdrawalWorker` function from `main.go`
- Delete the `case "withdrawal_worker"` branch from `main.go`
- `types.WithdrawalMessage` in `pkg/types/types.go`: delete entirely. The webhook notification event uses `types.WebhookMessage`, which already exists and is sufficient.

### New
- `MPCService mpc.Service` in container
- `SecretsManagerClient *secretsmanager.Client` in container (AWS SDK v2)
- API Lambda memory: 512 MB minimum
- API Lambda timeout: 30 seconds minimum

---

## Container Changes

`app/container/container.go` adds:
```go
MPCService           mpc.Service
SecretsManagerClient *secretsmanager.Client
```

Both initialized at boot. `MPCService` receives `SecretsManagerClient`.

---

## Service Changes

- `WalletService.CreateWallet(ctx, chain, label, passphrase string)` — passphrase added
- `WithdrawalService`: synchronous MPC signing path added inline (not via SQS); `Execute()` method removed or repurposed to webhook-only
- `WithdrawalMessage` in `pkg/types/types.go`: passphrase field must **not** be added; scope limited to webhook notification data

---

## Dependencies to Add

```
github.com/bnb-chain/tss-lib/v2
golang.org/x/crypto                                        # Argon2id
github.com/aws/aws-sdk-go-v2/service/secretsmanager
```

---

## Out of Scope (v1)

- Passphrase rotation / re-encryption of share_A
- Share refresh (proactive security)
- Multi-address HD derivation for MPC wallets
- Multi-device key recovery
- HSM integration
- Audit log of signing operations
- Solana MPC (conditional on ed25519 compatibility gate — may be deferred to v2)
